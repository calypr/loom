package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/explorer"
)

const cfgAlpha = `{"title": "Alpha", "filters": [{"column": "day", "op": "eq", "value": "2026-01-01"}], "beta": true, "limit": 25, "note": null}`

const literalCfgAlphaText = `{"beta": true, "note": null, "limit": 25, "title": "Alpha", "filters": [{"op": "eq", "value": "2026-01-01", "column": "day"}]}`

const cfgBeta = `{"z": 1, "a": {"deep": [true, false, null]}, "m": "keep"}`

const literalCfgBetaText = `{"a": {"deep": [true, false, null]}, "m": "keep", "z": 1}`

const resultsOK = `{"matchedRows": 4021, "traceId": "trc_01"}`

const literalResultsText = `{"traceId": "trc_01", "matchedRows": 4021}`

var ownerUpdatedAt = time.Date(2026, 1, 2, 3, 4, 5, 123456000, time.UTC)

const ownerUpdatedAtText = "2026-01-02 03:04:05.123456"

func fullOwner(project, id string) explorer.Explorer {
	return explorer.Explorer{
		Project:                       project,
		ExplorerID:                    id,
		Title:                         "Alpha Dashboard",
		ManagementMode:                explorer.ManagementRepository,
		DraftConfig:                   json.RawMessage(cfgAlpha),
		DraftVersion:                  3,
		DraftDigest:                   "sha256:aaa",
		DraftSnapshotToken:            "snap-1",
		DraftSourceGeneration:         "gen_2026_01",
		DraftAuthorizationScopeDigest: "authz-scope-1",
		PreviousDraftRevisionID:       "draftrev_prev",
		LastAuthoringCommandID:        "cmd-1",
		LastAuthoringCommandDigest:    "sha256:cmd",
		LastAuthoringCommandResults:   json.RawMessage(resultsOK),
		ActiveRevisionID:              "rev_active",
		UpdatedBy:                     "builder@calypr",
		UpdatedAt:                     ownerUpdatedAt,
	}
}

func jsonEqual(t *testing.T, want, got json.RawMessage, field string) {
	t.Helper()
	if (want == nil) != (got == nil) {
		t.Fatalf("%s: missing-ness changed: want %q got %q", field, want, got)
	}
	if want == nil {
		return
	}
	if string(want) == "null" || string(got) == "null" {
		if !(string(want) == "null" && string(got) == "null") {
			t.Fatalf("%s: JSON-null lost: want %q got %q", field, want, got)
		}
		return
	}
	var wantValue, gotValue any
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("%s: want payload invalid: %v", field, err)
	}
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("%s: got payload invalid (jsonb loss?): %v", field, err)
	}
	if !reflect.DeepEqual(wantValue, gotValue) {
		t.Fatalf("%s: payload not lossless: want %s got %s", field, want, got)
	}
}

func requireOwnerEqual(t *testing.T, want, got explorer.Explorer, where string) {
	t.Helper()
	if got.Project != want.Project || got.ExplorerID != want.ExplorerID {
		t.Fatalf("%s: identity changed: got %q/%q want %q/%q", where, got.Project, got.ExplorerID, want.Project, want.ExplorerID)
	}
	if got.Title != want.Title || got.ManagementMode != want.ManagementMode {
		t.Fatalf("%s: presentation fields changed: got title=%q mode=%q want title=%q mode=%q",
			where, got.Title, got.ManagementMode, want.Title, want.ManagementMode)
	}
	if got.DraftVersion != want.DraftVersion || got.DraftDigest != want.DraftDigest ||
		got.DraftSnapshotToken != want.DraftSnapshotToken ||
		got.DraftSourceGeneration != want.DraftSourceGeneration ||
		got.DraftAuthorizationScopeDigest != want.DraftAuthorizationScopeDigest ||
		got.PreviousDraftRevisionID != want.PreviousDraftRevisionID ||
		got.LastAuthoringCommandID != want.LastAuthoringCommandID ||
		got.LastAuthoringCommandDigest != want.LastAuthoringCommandDigest ||
		got.ActiveRevisionID != want.ActiveRevisionID ||
		got.UpdatedBy != want.UpdatedBy {
		t.Fatalf("%s: typed draft/authoring fields changed: got %+v want %+v", where, got, want)
	}
	jsonEqual(t, want.DraftConfig, got.DraftConfig, where+" draftConfig")
	jsonEqual(t, want.LastAuthoringCommandResults, got.LastAuthoringCommandResults, where+" lastAuthoringCommandResults")
	if !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("%s: updatedAt instant changed: got %s want %s", where, got.UpdatedAt, want.UpdatedAt)
	}
}

var pgstate01v1NameRe = regexp.MustCompile(`^pgstate01v1_it_[0-9]+_[0-9a-f]+$`)

func testPostgresURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 Explorer-owner integration tests")
	}
	return raw
}

func randomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return hex.EncodeToString(b[:])
}

type ownerFixture struct {
	ctx    context.Context
	admin  *pgxpool.Pool
	iso    *pgxpool.Pool
	dbName string
	schema string
	dsn    string
	scrub  func(any) string
	fail   func(step string, err error)
}

func newOwnerFixture(t *testing.T) *ownerFixture {
	t.Helper()
	raw := testPostgresURL(t)

	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("LOOM_TEST_POSTGRES_URL must be a postgres:// or postgresql:// URL")
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

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	t.Cleanup(admin.Close)

	dbName := fmt.Sprintf("pgstate01v1_it_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !pgstate01v1NameRe.MatchString(dbName) {
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
		var namespaceLeftovers int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_database WHERE datname LIKE 'pgstate01v1_it_%'`).Scan(&namespaceLeftovers); err != nil {
			t.Errorf("leftover audit: query database namespace: %s", scrub(err))
		}
		if namespaceLeftovers != 0 {
			t.Errorf("leftover audit: %d pgstate01v1_it_* databases remain", namespaceLeftovers)
		}
		var schemaLeftovers int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'pgstate01v1_it_%'`).Scan(&schemaLeftovers); err != nil {
			t.Errorf("leftover audit: query schema namespace: %s", scrub(err))
		}
		if schemaLeftovers != 0 {
			t.Errorf("leftover audit: %d pgstate01v1_it_* schemas remain in the service database", schemaLeftovers)
		}
	})

	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(ctx),
			`DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %s", scrub(err))
		}
	})

	if q := u.Query(); q.Get("dbname") != "" {
		q.Del("dbname")
		u.RawQuery = q.Encode()
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
	u.Path = "/" + dbName
	dsn := u.String()
	secrets = append(secrets, dsn)
	if probe, err := url.Parse(dsn); err != nil ||
		probe.Query().Get("dbname") != "" || probe.Path != "/"+dbName {
		t.Fatal("isolated test DSN does not provably target the isolated database")
	}

	iso, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fail("connect to isolated database", err)
	}
	t.Cleanup(iso.Close)

	schema := fmt.Sprintf("pgstate01v1_it_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !pgstate01v1NameRe.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	t.Cleanup(func() {
		if _, err := iso.Exec(context.WithoutCancel(ctx),
			`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})

	return &ownerFixture{ctx: ctx, admin: admin, iso: iso, dbName: dbName, schema: schema, dsn: dsn, scrub: scrub, fail: fail}
}

func (f *ownerFixture) ownerTable() string {
	return pgx.Identifier{f.schema, OwnerTable}.Sanitize()
}

func (f *ownerFixture) newStore(t *testing.T) *Store {
	t.Helper()
	client, err := pgstore.Open(f.ctx, pgstore.Config{
		DSN:            f.dsn,
		MaxConns:       4,
		MinConns:       0,
		AcquireTimeout: 10 * time.Second,
	})
	if err != nil {
		f.fail("open pg-store pool", err)
	}
	t.Cleanup(client.Close)
	store, err := New(client, f.schema)
	if err != nil {
		f.fail("build Explorer owner store", err)
	}
	return store
}

func (f *ownerFixture) migrate(t *testing.T) pgstore.MigrateResult {
	t.Helper()
	client, err := pgstore.Open(f.ctx, pgstore.Config{
		DSN:            f.dsn,
		MaxConns:       2,
		MinConns:       0,
		AcquireTimeout: 10 * time.Second,
	})
	if err != nil {
		f.fail("open migration pool", err)
	}
	defer client.Close()
	result, err := MigrateSchema(f.ctx, client, f.schema)
	if err != nil {
		f.fail("migrate owner schema", err)
	}
	return result
}

type literalRow struct {
	Title                      string
	ManagementMode             string
	DraftVersion               int64
	DraftDigest                string
	DraftSnapshotToken         string
	DraftSourceGeneration      string
	DraftAuthzScopeDigest      string
	PreviousDraftRevisionID    string
	LastAuthoringCommandID     string
	LastAuthoringCommandDigest string
	ActiveRevisionID           string
	UpdatedBy                  string
	UpdatedAtText              string
	DraftConfigText            string
	DraftConfigKind            string
	ResultsText                string
	ResultsKind                string
}

func (f *ownerFixture) literalRow(t *testing.T, project, id string) literalRow {
	t.Helper()
	var row literalRow
	err := f.iso.QueryRow(f.ctx, `SELECT
        title, management_mode, draft_version, draft_digest, draft_snapshot_token,
        draft_source_generation, draft_authorization_scope_digest,
        previous_draft_revision_id, last_authoring_command_id, last_authoring_command_digest,
        active_revision_id, updated_by,
        to_char(updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS.US'),
        COALESCE(draft_config::text, ''), COALESCE(jsonb_typeof(draft_config), 'missing'),
        COALESCE(last_authoring_command_results::text, ''), COALESCE(jsonb_typeof(last_authoring_command_results), 'missing')
    FROM `+f.ownerTable()+` WHERE project = $1 AND explorer_id = $2`, project, id).Scan(
		&row.Title, &row.ManagementMode, &row.DraftVersion, &row.DraftDigest,
		&row.DraftSnapshotToken, &row.DraftSourceGeneration, &row.DraftAuthzScopeDigest,
		&row.PreviousDraftRevisionID, &row.LastAuthoringCommandID, &row.LastAuthoringCommandDigest,
		&row.ActiveRevisionID, &row.UpdatedBy, &row.UpdatedAtText,
		&row.DraftConfigText, &row.DraftConfigKind,
		&row.ResultsText, &row.ResultsKind)
	if err != nil {
		f.fail("read literal owner row", err)
	}
	return row
}

func (f *ownerFixture) ownerCount(t *testing.T, project string) int {
	t.Helper()
	var n int
	if err := f.iso.QueryRow(f.ctx,
		`SELECT count(*) FROM `+f.ownerTable()+` WHERE project = $1`, project).Scan(&n); err != nil {
		f.fail("count owner rows", err)
	}
	return n
}

func (f *ownerFixture) requireLiteralFullOwner(t *testing.T, project, id string) {
	t.Helper()
	row := f.literalRow(t, project, id)
	want := literalRow{
		Title:                      "Alpha Dashboard",
		ManagementMode:             "REPOSITORY",
		DraftVersion:               3,
		DraftDigest:                "sha256:aaa",
		DraftSnapshotToken:         "snap-1",
		DraftSourceGeneration:      "gen_2026_01",
		DraftAuthzScopeDigest:      "authz-scope-1",
		PreviousDraftRevisionID:    "draftrev_prev",
		LastAuthoringCommandID:     "cmd-1",
		LastAuthoringCommandDigest: "sha256:cmd",
		ActiveRevisionID:           "rev_active",
		UpdatedBy:                  "builder@calypr",
		UpdatedAtText:              ownerUpdatedAtText,
		DraftConfigText:            literalCfgAlphaText,
		DraftConfigKind:            "object",
		ResultsText:                literalResultsText,
		ResultsKind:                "object",
	}
	if row != want {
		t.Fatalf("literal owner row mismatch:\n got %+v\nwant %+v", row, want)
	}
}

func requirePgErr(t *testing.T, err error, sqlstate, step string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("%s: error = %v (%T), want *pgconn.PgError", step, err, err)
	}
	if pgErr.Code != sqlstate {
		t.Fatalf("%s: sqlstate = %s, want %s (error: %v)", step, pgErr.Code, sqlstate, err)
	}
}

func TestPostgresExplorerOwnerMigrationHistoryLoads(t *testing.T) {
	migrations, err := OwnerMigrations()
	if err != nil {
		t.Fatalf("OwnerMigrations() = %v", err)
	}
	embeddedHistoryFiles, _ := pgstate02EnumerateEmbeddedMigrations(t)
	if len(migrations) != len(embeddedHistoryFiles) {
		t.Fatalf("migration count = %d, want %d (one per file in the embedded migrations directory)", len(migrations), len(embeddedHistoryFiles))
	}
	want := []struct {
		version  int64
		name     string
		checksum string
	}{
		{1, "ledger", "46a99180c6c6c7d41ca13520e020b71827725ec8059cce0142e278fb7577f086"},
		{2, "explorer_owner", "fc85316901b79234695695f972d8abc17d29a726293b0ff6584c96aed1cc7056"},
		{3, "explorer_draft_revision", "4bf4fe35485cb7adc6ea46812b0ae004e89ff935a14f14dd80a300bb3cc86c3f"},
		{4, "capability_artifacts", "bec3e49f897b28ef71d7d69a5f7c71bb11d13b7c631ee26724de36a89d639222"},
		{5, "explorer_compilation_receipt", "f32b3f9c821b5451e64b3d2972fb49e61f4e0c5bfcf88c69bc5b6aa7298fc33a"},
		{6, "explorer_revision", "3f6c44bff72d7837634e8c170f0b97e256e38d35f7c844fad27f4fbda38ecc09"},
		{7, "interpretation_library", "5e308b21a3f567b3066c46458606dd361a9c90646db54ee71618902a544a9ba2"},
		{8, "selection_revisions", "562a61ef33786cf674f1c87552e2dc8e70691b34116b7f415d5f81960ed8d65d"},
		{9, "explorer_explicit_groups", "c635cb5ef7c3c04655b4f45b01ba0fe834a7f181f70638f300878caf16c9a14f"},
		{10, "explorer_explicit_group_memberships", "02e0c9ab8bf9864e7d9c82471eacc7406fe2301b025e459784044c4a44489ccd"},
		{11, "explorer_explicit_group_definition_guard_hardening", "fb45d8f8c312f8f6adb119465da5ed89ac0ddd14929a2c26aa4c917db6d704ec"},
	}
	if len(want) != len(migrations) {
		t.Fatalf("checksum registry holds %d entries, want %d (one per embedded migration file): add the new migration's name and sha256 pin deliberately", len(want), len(migrations))
	}
	for i, m := range migrations {
		if m.Version != want[i].version || m.Name != want[i].name {
			t.Fatalf("migration %d = %d/%s, want %d/%s",
				i, m.Version, m.Name, want[i].version, want[i].name)
		}
		if m.Checksum != want[i].checksum {
			t.Fatalf("migration %d/%s checksum = %s, want pinned %s: migration SQL drifted",
				m.Version, m.Name, m.Checksum, want[i].checksum)
		}
		upperSQL := strings.ToUpper(m.SQL)
		if !strings.Contains(upperSQL, "CREATE TABLE IF NOT EXISTS") &&
			!strings.Contains(upperSQL, "CREATE OR REPLACE FUNCTION") {
			t.Fatalf("migration %d/%s must be rerun-safe (CREATE TABLE IF NOT EXISTS, or CREATE OR REPLACE FUNCTION for guard hardening); it never truncates", m.Version, m.Name)
		}
	}
	sqlFor := func(name string) string {
		for _, m := range migrations {
			if m.Name == name {
				return m.SQL
			}
		}
		t.Fatalf("migration %s missing", name)
		return ""
	}
	if !strings.Contains(sqlFor("ledger"), "schema_migrations") {
		t.Fatal("ledger bootstrap must create the pg-store-02 ledger table")
	}
	if !strings.Contains(sqlFor("explorer_owner"), `PRIMARY KEY (project, explorer_id)`) {
		t.Fatal("owner table must be unique on (project, explorer_id) identity")
	}
	if !strings.Contains(sqlFor("explorer_compilation_receipt"), `PRIMARY KEY (project, explorer_id, id)`) ||
		!strings.Contains(sqlFor("explorer_compilation_receipt"), "compilation_receipts_frozen") {
		t.Fatal("receipt table must be unique on the receipt identity and ship its frozen trigger")
	}
	if !strings.Contains(sqlFor("explorer_revision"), "explorer_revisions_guard") {
		t.Fatal("revision table must ship its frozen-column, insert-once DELETE, and monotone-transition trigger")
	}
}

func TestPostgresExplorerOwnerStoreValidationAndSQLContract(t *testing.T) {
	if _, err := New(nil, "loom"); err == nil {
		t.Fatal("New accepted a nil client")
	}
	for _, bad := range []string{"", " ", "Loom", "1loom", "loom;drop", `loom"x`, "loom x", strings.Repeat("a", 64)} {
		if _, err := New(&pgstore.Client{}, bad); err == nil {
			t.Errorf("New accepted invalid schema %q", bad)
		} else if !strings.Contains(err.Error(), "invalid schema") {
			t.Errorf("New(%q) error = %v, want invalid-schema error", bad, err)
		}
	}
	store, err := New(&pgstore.Client{}, "loom_control")
	if err != nil {
		t.Fatalf("New with a valid schema = %v", err)
	}
	if !strings.Contains(store.insert, `ON CONFLICT (project, explorer_id) DO NOTHING`) {
		t.Error("Create statement must be insert-once: ON CONFLICT (project, explorer_id) DO NOTHING")
	}
	if !strings.Contains(store.insert, "RETURNING") {
		t.Error("Create statement must RETURNING the stored row")
	}
	if !strings.Contains(store.insert, `"loom_control"."`+OwnerTable+`"`) {
		t.Error("Create statement must bind the schema-qualified, sanitized table")
	}
	if !strings.Contains(store.get, `WHERE project = $1 AND explorer_id = $2`) {
		t.Error("Get statement must scope by project AND explorer_id")
	}
	if !strings.Contains(store.list, `WHERE project = $1 ORDER BY explorer_id`) {
		t.Error("List statement must be project-scoped and sorted by explorer_id")
	}
	for i := 1; i <= len(ownerColumns); i++ {
		if !strings.Contains(store.insert, fmt.Sprintf("$%d", i)) {
			t.Fatalf("Create statement is missing ordered parameter $%d", i)
		}
	}
	if strings.Contains(store.insert, fmt.Sprintf("$%d", len(ownerColumns)+1)) {
		t.Fatalf("Create statement carries more than %d parameters", len(ownerColumns))
	}
	if got := len(ownerArgs(fullOwner("p", "e"))); got != len(ownerColumns) {
		t.Fatalf("ownerArgs produced %d values, want %d (one per column)", got, len(ownerColumns))
	}
}

func TestPostgresExplorerOwnerJSONBoundary(t *testing.T) {
	if b := jsonParam(nil); b != nil {
		t.Errorf("jsonParam(nil) = %q, want SQL NULL", b)
	}
	if b := jsonParam(json.RawMessage{}); b != nil {
		t.Errorf("jsonParam(empty) = %q, want SQL NULL (missing)", b)
	}
	if b := jsonParam(json.RawMessage("null")); string(b) != "null" {
		t.Errorf("jsonParam(null literal) = %q, want to pass through as jsonb 'null'", b)
	}
}

func TestPostgresExplorerOwnerCreateGetRoundTripLiteralRows(t *testing.T) {
	f := newOwnerFixture(t)
	f.migrate(t)

	writer := f.newStore(t)
	want := fullOwner("proj_alpha", "exp_1")
	created, err := writer.Create(f.ctx, want)
	if err != nil {
		f.fail("create owner", err)
	}
	requireOwnerEqual(t, want, *created, "Create result")

	f.requireLiteralFullOwner(t, "proj_alpha", "exp_1")
	if n := f.ownerCount(t, "proj_alpha"); n != 1 {
		t.Fatalf("owner row count = %d, want exactly 1", n)
	}

	reader := f.newStore(t)
	got, err := reader.Get(f.ctx, "proj_alpha", "exp_1")
	if err != nil {
		f.fail("get owner on fresh pool", err)
	}
	requireOwnerEqual(t, want, *got, "fresh-pool Get")

	listed, err := reader.List(f.ctx, "proj_alpha")
	if err != nil {
		f.fail("list owners on fresh pool", err)
	}
	if len(listed) != 1 {
		t.Fatalf("List returned %d owners, want 1", len(listed))
	}
	requireOwnerEqual(t, want, listed[0], "fresh-pool List")
}

func TestPostgresExplorerOwnerCreateDuplicateInsertOnce(t *testing.T) {
	f := newOwnerFixture(t)
	f.migrate(t)

	writer := f.newStore(t)
	first := fullOwner("proj_dup", "exp_same")
	if _, err := writer.Create(f.ctx, first); err != nil {
		f.fail("create first owner", err)
	}

	second := fullOwner("proj_dup", "exp_same")
	second.Title = "Overwrite Attempt"
	second.ManagementMode = explorer.ManagementInteractive
	second.DraftConfig = json.RawMessage(cfgBeta)
	second.DraftVersion = 99
	second.DraftDigest = "sha256:evil"
	second.LastAuthoringCommandResults = nil
	conflict, err := writer.Create(f.ctx, second)
	if !errors.Is(err, explorer.ErrDraftConflict) {
		t.Fatalf("duplicate Create error = %v, want explorer.ErrDraftConflict", err)
	}
	if conflict != nil {
		t.Fatalf("duplicate Create returned %+v, want nil", conflict)
	}

	reader := f.newStore(t)
	got, err := reader.Get(f.ctx, "proj_dup", "exp_same")
	if err != nil {
		f.fail("get owner after duplicate", err)
	}
	requireOwnerEqual(t, first, *got, "owner after duplicate Create")
	f.requireLiteralFullOwner(t, "proj_dup", "exp_same")

	other := fullOwner("proj_other", "exp_same")
	if _, err := writer.Create(f.ctx, other); err != nil {
		f.fail("create same id under another project", err)
	}
	if n := f.ownerCount(t, "proj_dup"); n != 1 {
		t.Fatalf("proj_dup owner count = %d, want 1", n)
	}
	if n := f.ownerCount(t, "proj_other"); n != 1 {
		t.Fatalf("proj_other owner count = %d, want 1", n)
	}

	cols := strings.Join(ownerColumns, ", ")
	placeholders := ownerPlaceholders()
	_, err = f.iso.Exec(f.ctx, `INSERT INTO `+f.ownerTable()+` (`+cols+`) VALUES (`+placeholders+`)`,
		ownerArgs(first)...)
	requirePgErr(t, err, "23505", "raw duplicate insert")
}

func TestPostgresExplorerOwnerGetAndListProjectScope(t *testing.T) {
	f := newOwnerFixture(t)
	f.migrate(t)

	writer := f.newStore(t)
	ids := []string{"default", "Zeta", "alpha", "_x", "beta"}
	for _, project := range []string{"proj_scope_a", "proj_scope_b"} {
		for _, id := range ids {
			if _, err := writer.Create(f.ctx, fullOwner(project, id)); err != nil {
				f.fail("create "+project+"/"+id, err)
			}
		}
	}
	if _, err := writer.Create(f.ctx, fullOwner("proj_scope_b", "only_b")); err != nil {
		f.fail("create only-b owner", err)
	}

	reader := f.newStore(t)

	if _, err := reader.Get(f.ctx, "proj_scope_a", "only_b"); !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("cross-project Get error = %v, want explorer.ErrNotFound", err)
	}
	if _, err := reader.Get(f.ctx, "proj_scope_a", "missing"); !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("missing Get error = %v, want explorer.ErrNotFound", err)
	}
	if _, err := reader.Get(f.ctx, "proj_scope_b", "only_b"); err != nil {
		f.fail("get only-b owner from owning project", err)
	}

	listed, err := reader.List(f.ctx, "proj_scope_a")
	if err != nil {
		f.fail("list proj_scope_a", err)
	}
	wantOrder := []string{"Zeta", "_x", "alpha", "beta", "default"}
	if len(listed) != len(wantOrder) {
		t.Fatalf("List returned %d owners, want %d (project-scoped)", len(listed), len(wantOrder))
	}
	for i, want := range wantOrder {
		if listed[i].ExplorerID != want {
			gotIDs := make([]string, len(listed))
			for j, e := range listed {
				gotIDs[j] = e.ExplorerID
			}
			t.Fatalf("List order = %v, want byte order %v", gotIDs, wantOrder)
		}
		if listed[i].Project != "proj_scope_a" {
			t.Fatalf("List leaked project %q", listed[i].Project)
		}
	}

	empty, err := reader.List(f.ctx, "proj_never_created")
	if err != nil {
		f.fail("list unknown project", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("unknown-project List = %#v, want empty non-nil slice", empty)
	}
}

func TestPostgresExplorerOwnerNullSemantics(t *testing.T) {
	f := newOwnerFixture(t)
	f.migrate(t)

	writer := f.newStore(t)

	missing := fullOwner("proj_null", "exp_missing")
	missing.DraftConfig = nil
	missing.LastAuthoringCommandResults = nil
	if _, err := writer.Create(f.ctx, missing); err != nil {
		f.fail("create missing-payload owner", err)
	}

	jsonNull := fullOwner("proj_null", "exp_jsonnull")
	jsonNull.DraftConfig = json.RawMessage("null")
	jsonNull.LastAuthoringCommandResults = json.RawMessage("null")
	if _, err := writer.Create(f.ctx, jsonNull); err != nil {
		f.fail("create json-null-payload owner", err)
	}

	doc := fullOwner("proj_null", "exp_doc")
	if _, err := writer.Create(f.ctx, doc); err != nil {
		f.fail("create document-payload owner", err)
	}

	reader := f.newStore(t)

	missingRow := f.literalRow(t, "proj_null", "exp_missing")
	if missingRow.DraftConfigKind != "missing" || missingRow.DraftConfigText != "" {
		t.Fatalf("missing draft_config physical form = %q/%q, want SQL NULL", missingRow.DraftConfigKind, missingRow.DraftConfigText)
	}
	if missingRow.ResultsKind != "missing" || missingRow.ResultsText != "" {
		t.Fatalf("missing results physical form = %q/%q, want SQL NULL", missingRow.ResultsKind, missingRow.ResultsText)
	}
	jsonNullRow := f.literalRow(t, "proj_null", "exp_jsonnull")
	if jsonNullRow.DraftConfigKind != "null" || jsonNullRow.DraftConfigText != "null" {
		t.Fatalf("json-null draft_config physical form = %q/%q, want jsonb 'null' (not SQL NULL)",
			jsonNullRow.DraftConfigKind, jsonNullRow.DraftConfigText)
	}
	if jsonNullRow.ResultsKind != "null" || jsonNullRow.ResultsText != "null" {
		t.Fatalf("json-null results physical form = %q/%q, want jsonb 'null'", jsonNullRow.ResultsKind, jsonNullRow.ResultsText)
	}
	docRow := f.literalRow(t, "proj_null", "exp_doc")
	if docRow.DraftConfigKind != "object" || docRow.DraftConfigText != literalCfgAlphaText {
		t.Fatalf("document draft_config literal = %q/%q, want %q", docRow.DraftConfigKind, docRow.DraftConfigText, literalCfgAlphaText)
	}

	gotMissing, err := reader.Get(f.ctx, "proj_null", "exp_missing")
	if err != nil {
		f.fail("get missing-payload owner", err)
	}
	if gotMissing.DraftConfig != nil || gotMissing.LastAuthoringCommandResults != nil {
		t.Fatalf("missing payloads did not read back nil: %q / %q", gotMissing.DraftConfig, gotMissing.LastAuthoringCommandResults)
	}
	gotJSONNull, err := reader.Get(f.ctx, "proj_null", "exp_jsonnull")
	if err != nil {
		f.fail("get json-null-payload owner", err)
	}
	if string(gotJSONNull.DraftConfig) != "null" || gotJSONNull.DraftConfig == nil {
		t.Fatalf("json-null draft_config read back as %q, want RawMessage(\"null\")", gotJSONNull.DraftConfig)
	}
	if string(gotJSONNull.LastAuthoringCommandResults) != "null" {
		t.Fatalf("json-null results read back as %q, want RawMessage(\"null\")", gotJSONNull.LastAuthoringCommandResults)
	}
	gotDoc, err := reader.Get(f.ctx, "proj_null", "exp_doc")
	if err != nil {
		f.fail("get document-payload owner", err)
	}
	requireOwnerEqual(t, doc, *gotDoc, "document-payload read-back")
}

func TestPostgresExplorerOwnerMigrateRerunPreservesState(t *testing.T) {
	f := newOwnerFixture(t)

	first := f.migrate(t)
	embeddedFiles, embeddedMax := pgstate02EnumerateEmbeddedMigrations(t)
	if first.LatestVersion != embeddedMax || len(first.Applied) != len(embeddedFiles) {
		t.Fatalf("first migrate = %+v, want latest %d with %d applied rows (one per embedded migration file; version 4 is the capability ticket's reserved slot)", first, embeddedMax, len(embeddedFiles))
	}

	store := f.newStore(t)
	want := fullOwner("proj_rerun", "exp_keep")
	if _, err := store.Create(f.ctx, want); err != nil {
		f.fail("create owner before rerun", err)
	}

	second := f.migrate(t)
	third := f.migrate(t)
	for _, rerun := range []pgstore.MigrateResult{second, third} {
		if rerun.LatestVersion != first.LatestVersion {
			t.Fatalf("rerun latest version = %d, want %d", rerun.LatestVersion, first.LatestVersion)
		}
		if len(rerun.Applied) != len(first.Applied) {
			t.Fatalf("rerun recorded %d rows, want %d (no duplicate history)", len(rerun.Applied), len(first.Applied))
		}
		for i, rec := range rerun.Applied {
			if rec.Version != first.Applied[i].Version || rec.Name != first.Applied[i].Name ||
				rec.Checksum != first.Applied[i].Checksum {
				t.Fatalf("rerun applied[%d] = %+v, want %+v", i, rec, first.Applied[i])
			}
		}
	}

	migrations, err := OwnerMigrations()
	if err != nil {
		t.Fatalf("OwnerMigrations() = %v", err)
	}
	type ledgerRow struct {
		Version  int64
		Name     string
		Checksum string
	}
	var ledger []ledgerRow
	rows, err := f.iso.Query(f.ctx, `SELECT version, name, checksum FROM `+
		pgx.Identifier{f.schema, pgstore.LedgerTable}.Sanitize()+` ORDER BY version`)
	if err != nil {
		f.fail("read ledger", err)
	}
	for rows.Next() {
		var r ledgerRow
		if err := rows.Scan(&r.Version, &r.Name, &r.Checksum); err != nil {
			rows.Close()
			f.fail("scan ledger", err)
		}
		ledger = append(ledger, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		f.fail("read ledger", err)
	}
	if len(ledger) != len(migrations) {
		t.Fatalf("ledger holds %d rows, want %d", len(ledger), len(migrations))
	}
	for i, want := range migrations {
		if ledger[i] != (ledgerRow{want.Version, want.Name, want.Checksum}) {
			t.Fatalf("ledger[%d] = %+v, want %+v", i, ledger[i], want)
		}
	}

	reader := f.newStore(t)
	got, err := reader.Get(f.ctx, "proj_rerun", "exp_keep")
	if err != nil {
		f.fail("get owner after migrate reruns", err)
	}
	requireOwnerEqual(t, want, *got, "owner after migrate reruns")
	f.requireLiteralFullOwner(t, "proj_rerun", "exp_keep")

	if _, err := MigrateSchema(f.ctx, nil, f.schema); err == nil {
		t.Fatal("MigrateSchema accepted a nil client")
	}
	missingClient, err := pgstore.Open(f.ctx, pgstore.Config{
		DSN: f.dsn, MaxConns: 2, MinConns: 0, AcquireTimeout: 10 * time.Second,
	})
	if err != nil {
		f.fail("open probe pool", err)
	}
	defer missingClient.Close()
	if _, err := MigrateSchema(f.ctx, missingClient, f.schema+"_never_created"); !errors.Is(err, pgstore.ErrInvalidSchemaName) {
		t.Fatalf("MigrateSchema on missing schema error = %v, want ErrInvalidSchemaName", err)
	}
}

func TestPostgresExplorerOwnerMixedHistoryIsRefused(t *testing.T) {
	f := newOwnerFixture(t)

	f.migrate(t)
	store := f.newStore(t)
	owner := fullOwner("proj_ownership", "exp_keep")
	if _, err := store.Create(f.ctx, owner); err != nil {
		f.fail("create owner before mixed-history refusal check", err)
	}
	client, err := pgstore.Open(f.ctx, pgstore.Config{
		DSN: f.dsn, MaxConns: 2, MinConns: 0, AcquireTimeout: 10 * time.Second,
	})
	if err != nil {
		f.fail("open source-store pool", err)
	}
	defer client.Close()
	if _, err := pgstore.MigrateSchema(f.ctx, client, f.schema); !errors.Is(err, pgstore.ErrMigrationChecksumMismatch) {
		t.Fatalf("source-store history on this package's schema error = %v, want ErrMigrationChecksumMismatch", err)
	}
	var ledgerVersions []int64
	rows, err := f.iso.Query(f.ctx, `SELECT version FROM `+
		pgx.Identifier{f.schema, pgstore.LedgerTable}.Sanitize()+` ORDER BY version`)
	if err != nil {
		f.fail("read ledger after refused run", err)
	}
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			f.fail("scan ledger", err)
		}
		ledgerVersions = append(ledgerVersions, v)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		f.fail("read ledger after refused run", err)
	}
	embeddedFiles, _ := pgstate02EnumerateEmbeddedMigrations(t)
	wantLedgerVersions := make([]int64, 0, len(ledgerVersions))
	for _, file := range embeddedFiles {
		fileVersion, err := pgstate02MigrationFileVersion(file)
		if err != nil {
			f.fail("parse embedded migration file name", err)
		}
		wantLedgerVersions = append(wantLedgerVersions, fileVersion)
	}
	if len(ledgerVersions) != len(wantLedgerVersions) {
		t.Fatalf("ledger versions after refused source-store run = %v, want the embedded history %v (refused before applying)", ledgerVersions, wantLedgerVersions)
	}
	for i, want := range wantLedgerVersions {
		if ledgerVersions[i] != want {
			t.Fatalf("ledger versions after refused source-store run = %v, want %v (refused before applying)", ledgerVersions, wantLedgerVersions)
		}
	}
	reader := f.newStore(t)
	got, err := reader.Get(f.ctx, "proj_ownership", "exp_keep")
	if err != nil {
		f.fail("owner must survive the refused mixed-history run", err)
	}
	requireOwnerEqual(t, owner, *got, "owner after refused mixed-history run")

	pgStoreSchema := f.schema + "_source"
	if _, err := f.iso.Exec(f.ctx, `CREATE SCHEMA "`+pgStoreSchema+`"`); err != nil {
		f.fail("create source-store schema", err)
	}
	t.Cleanup(func() {
		if _, err := f.iso.Exec(context.WithoutCancel(f.ctx),
			`DROP SCHEMA IF EXISTS "`+pgStoreSchema+`" CASCADE`); err != nil {
			t.Errorf("drop source-store schema: %s", f.scrub(err))
		}
	})
	if _, err := pgstore.MigrateSchema(f.ctx, client, pgStoreSchema); err != nil {
		f.fail("apply source-store history to its own schema", err)
	}
	if _, err := MigrateSchema(f.ctx, client, pgStoreSchema); !errors.Is(err, pgstore.ErrMigrationChecksumMismatch) {
		t.Fatalf("this package's history on a source-store schema error = %v, want ErrMigrationChecksumMismatch", err)
	}
	var ownerTableExists bool
	if err := f.iso.QueryRow(f.ctx,
		`SELECT to_regclass($1) IS NOT NULL`,
		pgx.Identifier{pgStoreSchema, OwnerTable}.Sanitize()).Scan(&ownerTableExists); err != nil {
		f.fail("check owner table absence", err)
	}
	if ownerTableExists {
		t.Fatal("refused mixed-history run created the explorers table; the runner must fail before applying")
	}
}

func TestPostgresExplorerOwnerSchemaConstraints(t *testing.T) {
	f := newOwnerFixture(t)
	f.migrate(t)

	writer := f.newStore(t)
	bogusMode := fullOwner("proj_checks", "exp_store_mode")
	bogusMode.ManagementMode = explorer.ManagementMode("SOMETHING_ELSE")
	_, err := writer.Create(f.ctx, bogusMode)
	requirePgErr(t, err, "23514", "store create with unknown management mode")
	if errors.Is(err, explorer.ErrDraftConflict) || errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("constraint failure misclassified as owner semantics: %v", err)
	}

	cols := strings.Join(ownerColumns, ", ")
	placeholders := ownerPlaceholders()
	rawInsert := `INSERT INTO ` + f.ownerTable() + ` (` + cols + `) VALUES (` + placeholders + `)`

	bogus := fullOwner("proj_checks", "exp_mode")
	bogus.ManagementMode = explorer.ManagementMode("SOMETHING_ELSE")
	_, err = f.iso.Exec(f.ctx, rawInsert, ownerArgs(bogus)...)
	requirePgErr(t, err, "23514", "unknown management mode")

	negative := fullOwner("proj_checks", "exp_version")
	negative.DraftVersion = -1
	_, err = f.iso.Exec(f.ctx, rawInsert, ownerArgs(negative)...)
	requirePgErr(t, err, "23514", "negative draft version")

	_, err = f.iso.Exec(f.ctx, rawInsert,
		"proj_checks", "exp_json", "t", "REPOSITORY",
		"this is not json", int64(1),
		"", "", "", "", "", "", "", nil, "", "", ownerUpdatedAt)
	requirePgErr(t, err, "22P02", "non-JSON draft payload")

	if n := f.ownerCount(t, "proj_checks"); n != 0 {
		t.Fatalf("rejected inserts left %d rows, want 0", n)
	}
}
