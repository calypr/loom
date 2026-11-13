package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/explorer"
)

var pgstate01NameRe = regexp.MustCompile(`^pgstate01_it_[0-9]+_[0-9a-f]+$`)

var (
	draftCASOwnedMu  sync.Mutex
	draftCASOwnedDBs = map[string]string{}
)

func draftCASRegister(db, schema string) {
	draftCASOwnedMu.Lock()
	defer draftCASOwnedMu.Unlock()
	draftCASOwnedDBs[db] = schema
}

func newDraftFixture(t *testing.T) *ownerFixture {
	t.Helper()
	raw := testPostgresURL(t)

	u, err := parseDraftURL(raw)
	if err != nil {
		t.Fatalf("LOOM_TEST_POSTGRES_URL must be a postgres:// or postgresql:// URL: %v", err)
	}

	var secrets []string
	secrets = append(secrets, raw)
	if u.User != nil {
		secrets = append(secrets, u.User.String())
		if pw, ok := u.User.Password(); ok && pw != "" {
			secrets = append(secrets, pw)
		}
	}
	scrub := func(v any) string {
		s := fmt.Sprintf("%v", v)
		for _, secret := range secrets {
			if secret != "" {
				s = strings.ReplaceAll(s, secret, "<redacted>")
			}
		}
		return s
	}
	fail := func(step string, err error) {
		t.Fatalf("%s: %s", step, scrub(err))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	t.Cleanup(cancel)

	admin, err := openDraftPool(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	t.Cleanup(admin.Close)

	dbName := fmt.Sprintf("pgstate01_it_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !pgstate01NameRe.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}

	t.Cleanup(func() {
		var mine int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_database WHERE datname = $1`, dbName).Scan(&mine); err != nil {
			t.Errorf("leftover audit: query database catalog: %s", scrub(err))
		}
		if mine != 0 {
			t.Errorf("leftover audit: isolated database %s still exists", dbName)
		}
	})

	if q := u.Query(); q.Get("dbname") != "" {
		q.Del("dbname")
		u.RawQuery = q.Encode()
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
	draftCASRegister(dbName, "")
	u.Path = "/" + dbName
	dsn := u.String()
	secrets = append(secrets, dsn)

	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(ctx),
			`DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %s", scrub(err))
		}
	})

	iso, err := openDraftPool(ctx, dsn)
	if err != nil {
		fail("connect to isolated database", err)
	}
	t.Cleanup(iso.Close)

	schema := fmt.Sprintf("pgstate01_it_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !pgstate01NameRe.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	draftCASRegister(dbName, schema)
	t.Cleanup(func() {
		if _, err := iso.Exec(context.WithoutCancel(ctx),
			`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})

	return &ownerFixture{ctx: ctx, admin: admin, iso: iso, dbName: dbName, schema: schema, dsn: dsn, scrub: scrub, fail: fail}
}

func (f *ownerFixture) historyTable() string {
	return pgx.Identifier{f.schema, DraftRevisionsTable}.Sanitize()
}

func (f *ownerFixture) newPinnedStore(t *testing.T) (*Store, int64) {
	t.Helper()
	client, err := pgstore.Open(f.ctx, pgstore.Config{
		DSN:            f.dsn,
		MaxConns:       1,
		MinConns:       0,
		AcquireTimeout: 15 * time.Second,
	})
	if err != nil {
		f.fail("open pinned pg-store pool", err)
	}
	t.Cleanup(client.Close)
	store, err := New(client, f.schema)
	if err != nil {
		f.fail("build Explorer draft store", err)
	}
	return store, draftClientBackendPID(t, f, client)
}

func (f *ownerFixture) newPinnedWitness(t *testing.T) (*pgstore.Client, int64) {
	t.Helper()
	client, err := pgstore.Open(f.ctx, pgstore.Config{
		DSN:            f.dsn,
		MaxConns:       1,
		MinConns:       0,
		AcquireTimeout: 15 * time.Second,
	})
	if err != nil {
		f.fail("open pinned witness pool", err)
	}
	t.Cleanup(client.Close)
	return client, draftClientBackendPID(t, f, client)
}

func draftClientBackendPID(t *testing.T, f *ownerFixture, client *pgstore.Client) int64 {
	t.Helper()
	conn, err := client.Acquire(f.ctx)
	if err != nil {
		f.fail("acquire backend-pid connection", err)
	}
	defer conn.Release()
	var pid int64
	if err := conn.QueryRow(f.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		f.fail("read pg_backend_pid", err)
	}
	if pid <= 0 {
		t.Fatalf("pg_backend_pid() returned %d, want a live backend", pid)
	}
	return pid
}

func requireDistinctBackends(t *testing.T, where string, pids ...int64) {
	t.Helper()
	seen := make(map[int64]string, len(pids))
	for i, pid := range pids {
		if prev, dup := seen[pid]; dup {
			t.Fatalf("%s: %s and writer %d share backend pid %d; CAS claims require distinct real PG connections",
				where, prev, i, pid)
		}
		seen[pid] = fmt.Sprintf("writer %d", i)
	}
}

const draftCfgOld = `{"documents":[{"outputId":"patients","construction":{"steps":[{"id":"pivot","outputs":[{"id":"col_age","name":"age"}]}]}}]}`

const draftCfgNew = `{"documents":[{"outputId":"patients","construction":{"steps":[{"id":"pivot","outputs":[{"id":"col_age","name":"age"}]},{"id":"derive","outputs":[{"id":"col_ratio","name":"ratio"}]}]}}]}`

const draftResultsOK = `{"matchedRows": 12, "traceId": "trc_draft"}`

var seedUpdatedAt = time.Date(2026, 2, 3, 4, 5, 6, 654321000, time.UTC)

const seedUpdatedAtText = "2026-02-03 04:05:06.654321"

func seedDraftOwner(t *testing.T, f *ownerFixture, store *Store, project, id string) explorer.Explorer {
	t.Helper()
	e := explorer.Explorer{
		Project:                       project,
		ExplorerID:                    id,
		Title:                         "Patients",
		ManagementMode:                explorer.ManagementInteractive,
		DraftConfig:                   []byte(draftCfgOld),
		DraftVersion:                  4,
		DraftDigest:                   "sha256:old-draft",
		DraftSnapshotToken:            "snapshot-old",
		DraftSourceGeneration:         "generation-old",
		DraftAuthorizationScopeDigest: "scope-old",
		PreviousDraftRevisionID:       "",
		LastAuthoringCommandID:        "seed-cmd",
		LastAuthoringCommandDigest:    "sha256:seed-cmd",
		LastAuthoringCommandResults:   []byte(draftResultsOK),
		ActiveRevisionID:              "published-revision-9",
		UpdatedBy:                     "alice",
		UpdatedAt:                     seedUpdatedAt,
	}
	if _, err := store.Create(f.ctx, e); err != nil {
		f.fail("seed draft owner", err)
	}
	return e
}

func draftCandidate(seed explorer.Explorer, suffix, digest string) explorer.Explorer {
	candidate := seed
	candidate.Title = "Patients " + suffix
	candidate.DraftConfig = []byte(draftCfgNew)
	candidate.DraftVersion = 999
	candidate.DraftDigest = digest
	candidate.DraftSnapshotToken = "snapshot-" + suffix
	candidate.DraftSourceGeneration = "generation-" + suffix
	candidate.DraftAuthorizationScopeDigest = "scope-" + suffix
	candidate.PreviousDraftRevisionID = "forged-previous-id"
	candidate.LastAuthoringCommandID = "cmd-" + suffix
	candidate.LastAuthoringCommandDigest = "sha256:cmd-" + suffix
	candidate.LastAuthoringCommandResults = []byte(draftResultsOK)
	candidate.ActiveRevisionID = "stale-revision-must-not-overwrite"
	candidate.UpdatedBy = "bob"
	candidate.UpdatedAt = time.Time{}
	return candidate
}

type literalHistoryRow struct {
	ID                       string
	Title                    string
	DraftVersion             int64
	DraftDigest              string
	DraftConfigText          string
	DraftConfigKind          string
	SnapshotToken            string
	SourceGeneration         string
	AuthorizationScopeDigest string
	UpdatedBy                string
	UpdatedAtText            string
}

func (f *ownerFixture) historyRows(t *testing.T, project, explorerID string) []literalHistoryRow {
	t.Helper()
	rows, err := f.iso.Query(f.ctx, `SELECT
        id, title, draft_version, draft_digest,
        COALESCE(draft_config::text, ''), COALESCE(jsonb_typeof(draft_config), 'missing'),
        snapshot_token, source_generation, authorization_scope_digest, updated_by,
        to_char(updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS.US')
    FROM `+f.historyTable()+` WHERE project = $1 AND explorer_id = $2 ORDER BY draft_version`,
		project, explorerID)
	if err != nil {
		f.fail("read history rows", err)
	}
	defer rows.Close()
	out := []literalHistoryRow{}
	for rows.Next() {
		var r literalHistoryRow
		if err := rows.Scan(&r.ID, &r.Title, &r.DraftVersion, &r.DraftDigest,
			&r.DraftConfigText, &r.DraftConfigKind, &r.SnapshotToken, &r.SourceGeneration,
			&r.AuthorizationScopeDigest, &r.UpdatedBy, &r.UpdatedAtText); err != nil {
			f.fail("scan history row", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		f.fail("read history rows", err)
	}
	return out
}

func (f *ownerFixture) historyCount(t *testing.T, project, explorerID string) int {
	t.Helper()
	var n int
	if err := f.iso.QueryRow(f.ctx,
		`SELECT count(*) FROM `+f.historyTable()+` WHERE project = $1 AND explorer_id = $2`,
		project, explorerID).Scan(&n); err != nil {
		f.fail("count history rows", err)
	}
	return n
}

func requireExactPreimageRow(t *testing.T, r literalHistoryRow, project, explorerID string, version int64) {
	t.Helper()
	wantID := draftRevisionKey(project, explorerID, version)
	if r.ID != wantID {
		t.Fatalf("history id = %q, want derived %q", r.ID, wantID)
	}
	if r.Title != "Patients" || r.DraftDigest != "sha256:old-draft" ||
		r.SnapshotToken != "snapshot-old" || r.SourceGeneration != "generation-old" ||
		r.AuthorizationScopeDigest != "scope-old" || r.UpdatedBy != "alice" ||
		r.UpdatedAtText != seedUpdatedAtText || r.DraftVersion != version {
		t.Fatalf("history preimage scalars changed: %+v", r)
	}
	if r.DraftConfigKind != "object" {
		t.Fatalf("history preimage config kind = %q, want object", r.DraftConfigKind)
	}
	if !strings.Contains(r.DraftConfigText, `"col_age"`) || strings.Contains(r.DraftConfigText, `"col_ratio"`) {
		t.Fatalf("history preimage is not the exact prior workspace: %s", r.DraftConfigText)
	}
}

func TestDraftCASRevisionKeyDerivation(t *testing.T) {
	for _, c := range []struct {
		project, explorerID string
		version             int64
		want                string
	}{
		{"project-a", "patients", 4, "draft_revision_14a3b30bb25b88835660c485a8a8a6fdc4a4e40ec9a12444d2b1c7d283499c00"},
		{"project-a", "patients", 0, "draft_revision_3a07ca4c963c00a696fb0d04080151732edf235771b51d96a8a6a92283b0b88a"},
		{"proj state", "exp/ents", 12345, "draft_revision_e3966c30839b313412cb0b45895a98b51a323953645c56d9a34f5990745fa12e"},
	} {
		want := "draft_revision_" + sha256DraftKeyParts(c.project, c.explorerID, fmt.Sprint(c.version))
		if want != c.want {
			t.Fatalf("local recomputation disagrees with the pinned literal: %q vs %q", want, c.want)
		}
		if got := draftRevisionKey(c.project, c.explorerID, c.version); got != c.want {
			t.Fatalf("draftRevisionKey(%q,%q,%d) = %q, want %q", c.project, c.explorerID, c.version, got, want)
		}
	}
	if draftRevisionKey("a", "b", 1) == draftRevisionKey("a", "b", 2) {
		t.Fatal("version does not change the derived id")
	}
	if draftRevisionKey("a", "b", 1) == draftRevisionKey("ab", "", 1) {
		t.Fatal("unseparated identity parts collide")
	}
}

func sha256DraftKeyParts(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestDraftCASStatementSQLContract(t *testing.T) {
	store, err := New(&pgstore.Client{}, "loom_control")
	if err != nil {
		t.Fatalf("New = %v", err)
	}
	if !strings.Contains(store.drafts.preimageRead, `"loom_control"."explorers"`) ||
		!strings.Contains(store.drafts.historyInsert, `"loom_control"."explorers_draft_revisions"`) ||
		!strings.Contains(store.drafts.ownerCAS, `"loom_control"."explorers"`) ||
		!strings.Contains(store.drafts.revisionGet, `"loom_control"."explorers_draft_revisions"`) {
		t.Fatal("every draft statement must bind the schema-qualified, sanitized tables")
	}
	for _, guard := range []string{
		"WHERE project = $1 AND explorer_id = $2 AND draft_version = $3",
		"($4 = '' OR draft_digest = $4)",
	} {
		if !strings.Contains(store.drafts.preimageRead, guard) {
			t.Errorf("preimage read missing guard %q", guard)
		}
	}
	for _, part := range []string{
		"WHERE project = $1 AND explorer_id = $2 AND draft_version = $3",
		"($17 = '' OR draft_digest = $17)",
		"draft_version = $15",
		"previous_draft_revision_id = $16",
		"RETURNING",
	} {
		if !strings.Contains(store.drafts.ownerCAS, part) {
			t.Errorf("owner CAS missing %q", part)
		}
	}
	for _, forbidden := range []string{"active_revision_id", "management_mode"} {
		setClause := store.drafts.ownerCAS[strings.Index(store.drafts.ownerCAS, "SET"):]
		setClause = setClause[:strings.Index(setClause, "WHERE")]
		if strings.Contains(setClause, forbidden+" =") {
			t.Errorf("owner CAS must never rewrite %s", forbidden)
		}
	}
	if !strings.Contains(store.drafts.historyInsert, "VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)") {
		t.Error("history insert must use twelve ordered parameters")
	}
	for _, guard := range []string{"WHERE project = $1 AND explorer_id = $2 AND id = $3"} {
		if !strings.Contains(store.drafts.revisionGet, guard) {
			t.Errorf("revision read missing scope %q", guard)
		}
	}
}

func TestDraftCASStoresExactPreimageAndPreservesPublishedHead(t *testing.T) {
	f := newDraftFixture(t)
	f.migrate(t)
	writer, writerPID := f.newPinnedStore(t)
	witness, witnessPID := f.newPinnedWitness(t)
	requireDistinctBackends(t, "exact-preimage round trip", writerPID, witnessPID)

	seed := seedDraftOwner(t, f, writer, "proj_pre", "patients")

	before := f.literalRow(t, "proj_pre", "patients")
	if before.DraftVersion != 4 || before.DraftDigest != "sha256:old-draft" ||
		before.PreviousDraftRevisionID != "" || before.ActiveRevisionID != "published-revision-9" ||
		before.UpdatedAtText != seedUpdatedAtText {
		t.Fatalf("seed head drifted before the CAS: %+v", before)
	}

	candidate := draftCandidate(seed, "v2", "sha256:new-draft")
	stored, err := writer.SaveDraft(f.ctx, candidate, 4, "sha256:old-draft")
	if err != nil {
		f.fail("save draft", err)
	}
	if stored.DraftVersion != 5 {
		t.Fatalf("stored draftVersion = %d, want 5 (candidate 999 ignored, +1 exactly once)", stored.DraftVersion)
	}
	if stored.ActiveRevisionID != "published-revision-9" {
		t.Fatalf("SaveDraft overwrote the published head with %q", stored.ActiveRevisionID)
	}
	wantPrevID := draftRevisionKey("proj_pre", "patients", 4)
	if stored.PreviousDraftRevisionID != wantPrevID {
		t.Fatalf("previousDraftRevisionId = %q, want derived %q", stored.PreviousDraftRevisionID, wantPrevID)
	}
	if !stored.UpdatedAt.After(seedUpdatedAt) {
		t.Fatalf("updatedAt = %s, want the adapter-owned now after the seed literal", stored.UpdatedAt)
	}

	after := f.literalRow(t, "proj_pre", "patients")
	if after.DraftVersion != 5 || after.DraftDigest != "sha256:new-draft" ||
		after.DraftSnapshotToken != "snapshot-v2" || after.DraftSourceGeneration != "generation-v2" ||
		after.DraftAuthzScopeDigest != "scope-v2" || after.Title != "Patients v2" ||
		after.LastAuthoringCommandID != "cmd-v2" || after.LastAuthoringCommandDigest != "sha256:cmd-v2" ||
		after.UpdatedBy != "bob" || after.PreviousDraftRevisionID != wantPrevID ||
		after.ActiveRevisionID != "published-revision-9" || after.ManagementMode != "INTERACTIVE" {
		t.Fatalf("winner head wrong: %+v", after)
	}
	if after.UpdatedAtText == seedUpdatedAtText {
		t.Fatal("updatedAt kept the stored preimage instant; the adapter must own it")
	}
	if after.DraftConfigKind != "object" || !strings.Contains(after.DraftConfigText, `"col_ratio"`) {
		t.Fatalf("winner config must carry the candidate workspace: %+v", after)
	}

	rows := f.historyRows(t, "proj_pre", "patients")
	if len(rows) != 1 {
		t.Fatalf("history rows = %d, want exactly 1", len(rows))
	}
	requireExactPreimageRow(t, rows[0], "proj_pre", "patients", 4)

	revision, err := func() (*explorer.DraftRevision, error) {
		reader, err := New(witness, f.schema)
		if err != nil {
			f.fail("build reader store", err)
		}
		return reader.GetDraftRevision(f.ctx, "proj_pre", "patients", wantPrevID)
	}()
	if err != nil {
		f.fail("get draft revision", err)
	}
	if revision.ID != wantPrevID || revision.Project != "proj_pre" || revision.ExplorerID != "patients" ||
		revision.DraftVersion != 4 || revision.DraftDigest != "sha256:old-draft" ||
		revision.Title != "Patients" || revision.SnapshotToken != "snapshot-old" ||
		revision.SourceGeneration != "generation-old" || revision.AuthorizationScopeDigest != "scope-old" ||
		revision.UpdatedBy != "alice" || !revision.UpdatedAt.Equal(seedUpdatedAt) {
		t.Fatalf("revision read-back = %+v", revision)
	}
	if !jsonLikeContains(t, revision.DraftConfig, "col_age") || jsonLikeContains(t, revision.DraftConfig, "col_ratio") {
		t.Fatalf("revision config is not the exact prior workspace: %s", revision.DraftConfig)
	}
}

func TestDraftCASMissingAndJSONNullDraftConfigAreDistinct(t *testing.T) {
	f := newDraftFixture(t)
	f.migrate(t)
	writer, writerPID := f.newPinnedStore(t)
	_, witnessPID := f.newPinnedWitness(t)
	requireDistinctBackends(t, "null-state round trip", writerPID, witnessPID)

	seed := seedDraftOwner(t, f, writer, "proj_null", "patients")
	if _, err := f.iso.Exec(f.ctx,
		`UPDATE `+f.ownerTable()+` SET draft_config = 'null'::jsonb WHERE project = $1 AND explorer_id = $2`,
		"proj_null", "patients"); err != nil {
		f.fail("set json-null config", err)
	}
	if row := f.literalRow(t, "proj_null", "patients"); row.DraftConfigKind != "null" {
		t.Fatalf("seed config kind = %q, want jsonb 'null' distinct from SQL NULL", row.DraftConfigKind)
	}

	candidate := draftCandidate(seed, "cleared", "sha256:cleared")
	candidate.DraftConfig = nil
	stored, err := writer.SaveDraft(f.ctx, candidate, 4, "sha256:old-draft")
	if err != nil {
		f.fail("save draft with nil config", err)
	}
	if stored.DraftConfig != nil {
		t.Fatalf("nil candidate config survived as %s", stored.DraftConfig)
	}
	head := f.literalRow(t, "proj_null", "patients")
	if head.DraftConfigKind != "missing" {
		t.Fatalf("head config kind = %q, want missing (SQL NULL)", head.DraftConfigKind)
	}
	rows := f.historyRows(t, "proj_null", "patients")
	if len(rows) != 1 || rows[0].DraftConfigKind != "null" {
		t.Fatalf("preimage must retain the JSON-null literal: %+v", rows)
	}
}

func TestDraftCASConcurrentSameVersionWritesHaveOneWinnerTwoConnections(t *testing.T) {
	f := newDraftFixture(t)
	f.migrate(t)
	storeA, pidA := f.newPinnedStore(t)
	storeB, pidB := f.newPinnedStore(t)
	_, witnessPID := f.newPinnedWitness(t)
	requireDistinctBackends(t, "concurrent same-version race", pidA, pidB, witnessPID)

	seed := seedDraftOwner(t, f, storeA, "proj_race", "patients")

	type outcome struct {
		suffix string
		saved  *explorer.Explorer
		err    error
	}
	start := make(chan struct{})
	results := make(chan outcome, 2)
	for i, store := range []*Store{storeA, storeB} {
		suffix := fmt.Sprintf("w%d", i)
		candidate := draftCandidate(seed, suffix, "sha256:draft-"+suffix)
		go func(store *Store, suffix string, candidate explorer.Explorer) {
			<-start
			saved, err := store.SaveDraft(f.ctx, candidate, 4, "sha256:old-draft")
			results <- outcome{suffix: suffix, saved: saved, err: err}
		}(store, suffix, candidate)
	}
	close(start)

	wins, winner, loserErrs := 0, outcome{}, []error{}
	for range 2 {
		r := <-results
		if r.err == nil {
			wins++
			winner = r
		} else {
			if !errors.Is(r.err, explorer.ErrDraftConflict) {
				t.Fatalf("loser error = %v, want explorer.ErrDraftConflict", r.err)
			}
			loserErrs = append(loserErrs, r.err)
		}
	}
	if wins != 1 {
		t.Fatalf("same version/digest produced %d winners, want exactly 1", wins)
	}

	head := f.literalRow(t, "proj_race", "patients")
	if head.DraftVersion != 5 {
		t.Fatalf("head draft_version = %d, want 5: two same-version CASes must increment once", head.DraftVersion)
	}
	if head.LastAuthoringCommandID != "cmd-"+winner.suffix || head.DraftDigest != "sha256:draft-"+winner.suffix {
		t.Fatalf("head belongs to %q instead of the winner %q: %+v", head.LastAuthoringCommandID, winner.suffix, head)
	}
	if head.ActiveRevisionID != "published-revision-9" {
		t.Fatalf("race overwrote the published head: %+v", head)
	}
	if head.PreviousDraftRevisionID != draftRevisionKey("proj_race", "patients", 4) {
		t.Fatalf("winner did not link the preimage: %+v", head)
	}

	rows := f.historyRows(t, "proj_race", "patients")
	if len(rows) != 1 {
		t.Fatalf("history rows after the race = %d, want exactly 1", len(rows))
	}
	requireExactPreimageRow(t, rows[0], "proj_race", "patients", 4)
}

func TestDraftCASStaleWriterMapsToConflictWithPreservedDuplicateKeyCause(t *testing.T) {
	f := newDraftFixture(t)
	f.migrate(t)
	storeA, pidA := f.newPinnedStore(t)
	storeB, pidB := f.newPinnedStore(t)
	requireDistinctBackends(t, "stale-writer cause retention", pidA, pidB)

	seed := seedDraftOwner(t, f, storeA, "proj_cause", "patients")

	inserted := make(chan struct{})
	releaseA := make(chan struct{})
	released := false
	release := func() {
		if !released {
			released = true
			close(releaseA)
		}
	}
	t.Cleanup(release)
	storeA.afterHistoryInsert = func(ctx context.Context, _ pgx.Tx) error {
		close(inserted)
		select {
		case <-releaseA:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	type outcome struct {
		suffix string
		saved  *explorer.Explorer
		err    error
	}
	resultsA := make(chan outcome, 1)
	go func() {
		saved, err := storeA.SaveDraft(f.ctx, draftCandidate(seed, "first", "sha256:first"), 4, "sha256:old-draft")
		resultsA <- outcome{suffix: "first", saved: saved, err: err}
	}()

	select {
	case <-inserted:
	case <-time.After(30 * time.Second):
		release()
		t.Fatal("winner writer never reached the post-insert seam")
	}

	resultsB := make(chan outcome, 1)
	go func() {
		saved, err := storeB.SaveDraft(f.ctx, draftCandidate(seed, "stale", "sha256:stale"), 4, "sha256:old-draft")
		resultsB <- outcome{suffix: "stale", saved: saved, err: err}
	}()

	if !waitForLockWait(f, pidB) {
		release()
		t.Fatalf("stale writer backend %d never entered a PostgreSQL lock wait behind the uncommitted preimage", pidB)
	}
	release()

	resA := <-resultsA
	if resA.err != nil || resA.saved == nil {
		t.Fatalf("first writer must win: %v", resA.err)
	}
	resB := <-resultsB
	if resB.err == nil || resB.saved != nil {
		t.Fatalf("stale writer must lose, got saved=%v err=%v", resB.saved, resB.err)
	}
	if !errors.Is(resB.err, explorer.ErrDraftConflict) {
		t.Fatalf("stale writer error = %v, want explorer.ErrDraftConflict", resB.err)
	}
	var pgErr *pgconn.PgError
	if !errors.As(resB.err, &pgErr) {
		t.Fatalf("stale writer lost the PostgreSQL cause: %v (%T)", resB.err, resB.err)
	}
	if pgErr.Code != duplicateKeySQLState || pgErr.ConstraintName != historyIdentityConstraint {
		t.Fatalf("stale writer cause = %s/%s, want %s/%s (constraint %q)", pgErr.Code, pgErr.ConstraintName,
			duplicateKeySQLState, "unique_violation", historyIdentityConstraint)
	}

	head := f.literalRow(t, "proj_cause", "patients")
	if head.DraftVersion != 5 || head.LastAuthoringCommandID != "cmd-first" {
		t.Fatalf("loser changed the head: %+v", head)
	}
	if rows := f.historyRows(t, "proj_cause", "patients"); len(rows) != 1 || rows[0].Title != "Patients" {
		t.Fatalf("loser left history behind: %+v", rows)
	}
}

func waitForLockWait(f *ownerFixture, pid int64) bool {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		if err := f.admin.QueryRow(f.ctx,
			`SELECT count(*) FROM pg_stat_activity WHERE pid = $1 AND state = 'active' AND wait_event_type = 'Lock'`,
			pid).Scan(&waiting); err == nil && waiting == 1 {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func TestDraftCASStaleRetryAfterWinnerCommitIsRejected(t *testing.T) {
	f := newDraftFixture(t)
	f.migrate(t)
	winnerStore, winnerPID := f.newPinnedStore(t)
	staleStore, stalePID := f.newPinnedStore(t)
	requireDistinctBackends(t, "stale retry after committed winner", winnerPID, stalePID)

	seed := seedDraftOwner(t, f, winnerStore, "proj_retry", "patients")
	if _, err := winnerStore.SaveDraft(f.ctx, draftCandidate(seed, "won", "sha256:won"), 4, "sha256:old-draft"); err != nil {
		f.fail("winner save", err)
	}
	head := f.literalRow(t, "proj_retry", "patients")
	if head.DraftVersion != 5 {
		t.Fatalf("head after winner = %+v, want draft_version 5", head)
	}
	if rows := f.historyCount(t, "proj_retry", "patients"); rows != 1 {
		t.Fatalf("history after winner = %d, want 1", rows)
	}

	stale := draftCandidate(seed, "late", "sha256:late")
	if _, err := staleStore.SaveDraft(f.ctx, stale, 4, "sha256:old-draft"); !errors.Is(err, explorer.ErrDraftConflict) {
		t.Fatalf("stale retry error = %v, want ErrDraftConflict", err)
	}
	if _, err := staleStore.SaveDraft(f.ctx, stale, 4); !errors.Is(err, explorer.ErrDraftConflict) {
		t.Fatalf("stale version without digest error = %v, want ErrDraftConflict", err)
	}

	after := f.literalRow(t, "proj_retry", "patients")
	if after != head {
		t.Fatalf("rejected stale retry changed the head:\n got %+v\nwant %+v", after, head)
	}
	if rows := f.historyCount(t, "proj_retry", "patients"); rows != 1 {
		t.Fatalf("rejected stale retries persisted history: %d rows", rows)
	}
	if rows := f.historyRows(t, "proj_retry", "patients"); rows[0].Title != "Patients" {
		t.Fatalf("rejected stale retries rewrote the preimage: %+v", rows[0])
	}
}

func TestDraftCASRollsBackHistoryWhenOwnerCASFailsAfterInsert(t *testing.T) {
	f := newDraftFixture(t)
	f.migrate(t)
	writerStore, writerPID := f.newPinnedStore(t)
	takeover, takeoverPID := f.newPinnedWitness(t)
	requireDistinctBackends(t, "CAS fails after insert", writerPID, takeoverPID)

	seed := seedDraftOwner(t, f, writerStore, "proj_roll", "patients")

	inserted := make(chan struct{})
	bumped := make(chan struct{})
	released := false
	release := func() {
		if !released {
			released = true
			close(bumped)
		}
	}
	t.Cleanup(release)
	writerStore.afterHistoryInsert = func(ctx context.Context, _ pgx.Tx) error {
		close(inserted)
		select {
		case <-bumped:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	type outcome struct {
		saved *explorer.Explorer
		err   error
	}
	results := make(chan outcome, 1)
	go func() {
		saved, err := writerStore.SaveDraft(f.ctx, draftCandidate(seed, "phantom", "sha256:phantom"), 4, "sha256:old-draft")
		results <- outcome{saved: saved, err: err}
	}()

	select {
	case <-inserted:
	case <-time.After(30 * time.Second):
		release()
		t.Fatal("writer never reached the post-insert seam")
	}

	takeoverConn, err := takeover.Acquire(f.ctx)
	if err != nil {
		release()
		f.fail("acquire takeover connection", err)
	}
	tag, execErr := takeoverConn.Exec(f.ctx,
		`UPDATE `+f.ownerTable()+` SET draft_version = 5, draft_digest = 'sha256:taken-over'`+
			` WHERE project = $1 AND explorer_id = $2 AND draft_version = 4`,
		"proj_roll", "patients")
	takeoverConn.Release()
	if execErr != nil || tag.RowsAffected() != 1 {
		release()
		f.fail("takeover bump", fmt.Errorf("rows=%d err=%w", tag.RowsAffected(), execErr))
	}
	release()

	res := <-results
	if res.err == nil || !errors.Is(res.err, explorer.ErrDraftConflict) {
		t.Fatalf("post-insert CAS failure error = %v, want ErrDraftConflict", res.err)
	}
	if res.saved != nil {
		t.Fatalf("failed CAS returned a head: %+v", res.saved)
	}
	if rows := f.historyRows(t, "proj_roll", "patients"); len(rows) != 0 {
		t.Fatalf("aborted writer left history behind: %+v", rows)
	}
	head := f.literalRow(t, "proj_roll", "patients")
	if head.DraftVersion != 5 || head.DraftDigest != "sha256:taken-over" ||
		head.PreviousDraftRevisionID != "" || head.LastAuthoringCommandID != "seed-cmd" {
		t.Fatalf("aborted writer changed the head: %+v", head)
	}
}

func TestDraftCASRollsBackHistoryOnFaultAfterInsert(t *testing.T) {
	f := newDraftFixture(t)
	f.migrate(t)
	writerStore, writerPID := f.newPinnedStore(t)
	_, witnessPID := f.newPinnedWitness(t)
	requireDistinctBackends(t, "fault after insert", writerPID, witnessPID)

	seed := seedDraftOwner(t, f, writerStore, "proj_fault", "patients")
	before := f.literalRow(t, "proj_fault", "patients")

	injected := errors.New("controlled transport failure after history insert")
	writerStore.afterHistoryInsert = func(context.Context, pgx.Tx) error {
		return injected
	}

	saved, err := writerStore.SaveDraft(f.ctx, draftCandidate(seed, "ghost", "sha256:ghost"), 4, "sha256:old-draft")
	if !errors.Is(err, injected) {
		t.Fatalf("fault error = %v, want the injected cause retained", err)
	}
	if saved != nil {
		t.Fatalf("faulted save returned a head: %+v", saved)
	}
	if rows := f.historyCount(t, "proj_fault", "patients"); rows != 0 {
		t.Fatalf("fault after history insert left %d history rows", rows)
	}
	if after := f.literalRow(t, "proj_fault", "patients"); after != before {
		t.Fatalf("fault after history insert changed the head:\n got %+v\nwant %+v", after, before)
	}
}

func TestDraftCASRevisionMonotonicityAcrossConnections(t *testing.T) {
	f := newDraftFixture(t)
	f.migrate(t)
	writer, writerPID := f.newPinnedStore(t)
	readerStore, readerPID := f.newPinnedStore(t)
	requireDistinctBackends(t, "monotonic revisions", writerPID, readerPID)

	seed := seedDraftOwner(t, f, writer, "proj_mono", "patients")

	digest := "sha256:old-draft"
	title := "Patients"
	for i := 0; i < 3; i++ {
		expected := int64(4 + i)
		nextDigest := fmt.Sprintf("sha256:v%d", 5+i)
		suffix := fmt.Sprintf("step%d", i)
		candidate := draftCandidate(seed, suffix, nextDigest)
		candidate.Title = title + "/" + suffix
		stored, err := writer.SaveDraft(f.ctx, candidate, expected, digest)
		if err != nil {
			f.fail(fmt.Sprintf("monotonic save %d", i), err)
		}
		if stored.DraftVersion != expected+1 {
			t.Fatalf("save %d: draftVersion = %d, want exactly %d (monotonic +1)", i, stored.DraftVersion, expected+1)
		}

		head, err := readerStore.Get(f.ctx, "proj_mono", "patients")
		if err != nil {
			f.fail("monotonic head read", err)
		}
		if head.DraftVersion != expected+1 || head.DraftDigest != nextDigest {
			t.Fatalf("save %d: cross-backend head = %+v", i, head)
		}
		wantPrev := draftRevisionKey("proj_mono", "patients", expected)
		if head.PreviousDraftRevisionID != wantPrev {
			t.Fatalf("save %d: previous id = %q, want %q", i, head.PreviousDraftRevisionID, wantPrev)
		}
		revision, err := readerStore.GetDraftRevision(f.ctx, "proj_mono", "patients", wantPrev)
		if err != nil {
			f.fail("monotonic revision read", err)
		}
		if revision.DraftVersion != expected || revision.DraftDigest != digest || revision.Title != title {
			t.Fatalf("save %d: preimage = %+v, want version %d digest %q title %q",
				i, revision, expected, digest, title)
		}
		digest = nextDigest
		title = candidate.Title
	}

	rows := f.historyRows(t, "proj_mono", "patients")
	if len(rows) != 3 {
		t.Fatalf("history rows = %d, want 3", len(rows))
	}
	for i, r := range rows {
		if r.DraftVersion != int64(4+i) {
			t.Fatalf("history[%d].draft_version = %d, want %d (gap-free, ordered)", i, r.DraftVersion, 4+i)
		}
		if r.ID != draftRevisionKey("proj_mono", "patients", int64(4+i)) {
			t.Fatalf("history[%d].id = %q, want derived key", i, r.ID)
		}
		wantAuthor := "bob"
		if i == 0 {
			wantAuthor = "alice"
		}
		if r.UpdatedBy != wantAuthor {
			t.Fatalf("history[%d].updated_by = %q, want the stored-state author %q", i, r.UpdatedBy, wantAuthor)
		}
	}
	var future int
	if err := f.iso.QueryRow(f.ctx, `SELECT count(*) FROM `+f.historyTable()+
		` WHERE project = $1 AND explorer_id = $2 AND draft_version >= 7`, "proj_mono", "patients").Scan(&future); err != nil {
		f.fail("probe for future history", err)
	}
	if future != 0 {
		t.Fatalf("%d history rows beyond the last preimage, want 0", future)
	}
	authors, err := f.iso.Query(f.ctx, `SELECT DISTINCT updated_by FROM `+f.historyTable()+
		` WHERE project = $1 AND explorer_id = $2 ORDER BY updated_by`, "proj_mono", "patients")
	if err != nil {
		f.fail("read history authors", err)
	}
	defer authors.Close()
	seen := []string{}
	for authors.Next() {
		var by string
		if err := authors.Scan(&by); err != nil {
			f.fail("scan history author", err)
		}
		seen = append(seen, by)
	}
	if err := authors.Err(); err != nil {
		f.fail("read history authors", err)
	}
	if len(seen) != 2 || seen[0] != "alice" || seen[1] != "bob" {
		t.Fatalf("preimage authors = %v, want exactly {alice, bob}: the seed's author plus the repeated CAS owner, each on their own immutable rows", seen)
	}
}

func TestDraftCASGetDraftRevisionScopesOpaqueIDsToProjectExplorer(t *testing.T) {
	f := newDraftFixture(t)
	f.migrate(t)
	writer, writerPID := f.newPinnedStore(t)
	stranger, strangerPID := f.newPinnedStore(t)
	requireDistinctBackends(t, "opaque id scoping", writerPID, strangerPID)

	seedA := seedDraftOwner(t, f, writer, "proj_a", "patients")
	seedB := seedDraftOwner(t, f, writer, "proj_b", "patients")
	seedBY := seedDraftOwner(t, f, writer, "proj_b", "labs")
	for _, pair := range []struct {
		seed explorer.Explorer
		id   string
	}{{seedA, "proj_a"}, {seedB, "proj_b"}, {seedBY, "proj_b"}} {
		if _, err := writer.SaveDraft(f.ctx, draftCandidate(pair.seed, "rev", "sha256:rev-"+pair.id), 4, "sha256:old-draft"); err != nil {
			f.fail("seed history", err)
		}
	}
	idA := draftRevisionKey("proj_a", "patients", 4)
	idB := draftRevisionKey("proj_b", "patients", 4)
	if idA == idB {
		t.Fatal("different projects derived the same revision id")
	}

	rev, err := stranger.GetDraftRevision(f.ctx, "proj_a", "patients", idA)
	if err != nil || rev.ID != idA {
		f.fail("legitimate scoped read", err)
	}

	if _, err := stranger.GetDraftRevision(f.ctx, "proj_b", "patients", idA); !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("cross-project read of %q = %v, want concealed ErrNotFound", idA, err)
	}
	if _, err := stranger.GetDraftRevision(f.ctx, "proj_b", "labs", idB); !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("cross-explorer read of %q = %v, want concealed ErrNotFound", idB, err)
	}
	if _, err := stranger.GetDraftRevision(f.ctx, "proj_a", "patients", "draft_revision_"+strings.Repeat("0", 64)); !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("nonexistent id = %v, want concealed ErrNotFound", err)
	}
	for _, blank := range []struct{ project, explorerID, revisionID string }{
		{"", "patients", idA}, {"proj_a", "", idA}, {"proj_a", "patients", ""},
		{"   ", "patients", idA},
	} {
		if _, err := stranger.GetDraftRevision(f.ctx, blank.project, blank.explorerID, blank.revisionID); !errors.Is(err, explorer.ErrNotFound) {
			t.Fatalf("blank part %#v = %v, want ErrNotFound", blank, err)
		}
	}

	for _, pair := range []struct {
		project, explorerID string
	}{
		{"proj_a", "patients"}, {"proj_b", "patients"}, {"proj_b", "labs"},
	} {
		if n := f.historyCount(t, pair.project, pair.explorerID); n != 1 {
			t.Fatalf("history for %s/%s = %d rows, want the preimage intact at 1", pair.project, pair.explorerID, n)
		}
	}
}

func TestZZZeroLeftoverPgStateExplorerDraftCasFixtures(t *testing.T) {
	raw := testPostgresURL(t)
	draftCASOwnedMu.Lock()
	owned := make(map[string]string, len(draftCASOwnedDBs))
	for db, schema := range draftCASOwnedDBs {
		owned[db] = schema
	}
	draftCASOwnedMu.Unlock()
	if len(owned) == 0 {
		t.Skip("this process created no pgstate01_it_* fixtures")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := openDraftPool(ctx, raw)
	if err != nil {
		t.Fatalf("connect to test service for audit: %v", err)
	}
	defer admin.Close()

	for db, schema := range owned {
		if !strings.HasPrefix(db, "pgstate01_it_") {
			t.Fatalf("registry holds foreign database %q", db)
		}
		var count int
		if err := admin.QueryRow(ctx, "SELECT COUNT(*) FROM pg_database WHERE datname = $1", db).Scan(&count); err != nil {
			t.Errorf("audit owned database %s: %v", db, err)
			continue
		}
		if count != 0 {
			t.Errorf("leftover owned database %s after all cleanups", db)
			if schema != "" {
				u, err := parseDraftURL(raw)
				if err == nil {
					if q := u.Query(); q.Get("dbname") != "" {
						q.Del("dbname")
						u.RawQuery = q.Encode()
					}
					u.Path = "/" + db
					if probe, err := openDraftPool(ctx, u.String()); err == nil {
						var schemaCount int
						if err := probe.QueryRow(ctx, "SELECT COUNT(*) FROM pg_namespace WHERE nspname = $1", schema).Scan(&schemaCount); err != nil {
							t.Errorf("audit owned schema %s in %s: %v", schema, db, err)
						} else if schemaCount != 0 {
							t.Errorf("leftover owned schema %s in leftover database %s", schema, db)
						}
						probe.Close()
					}
				}
			}
		}
	}
}

func parseDraftURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return nil, fmt.Errorf("url is not a postgres:// or postgresql:// URL")
	}
	return u, nil
}

func openDraftPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	return pgxpool.New(ctx, dsn)
}

func jsonLikeContains(t *testing.T, raw []byte, needle string) bool {
	t.Helper()
	if len(raw) == 0 {
		return false
	}
	return strings.Contains(string(raw), needle)
}
