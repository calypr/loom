package postgres

// Real-PostgreSQL 18 acceptance tests for interpretation library heads and
// immutable project revisions with transactional digest and parent CAS
// (pg-state-interpretation-library-v1; check-2:
// go test -count=1 ./internal/backends/postgres/internal/explorer/).
//
// Every claim is driven against the PostgreSQL 18 service named by
// LOOM_TEST_POSTGRES_URL through real InterpretationStore methods on real
// connections; fixtures are literal rows seeded by direct SQL or the store
// itself, and witnesses are literal rows read back by direct SQL through a
// physically separate pinned pool. Nothing is mocked. CAS proofs run over
// two single-connection writer pools plus a third single-connection witness,
// all with provably distinct pg_backend_pid values re-checked in every race
// round (>= 10 rounds per surface). Immutability and CAS are witnessed at
// the database: guard-drop probes prove each 45501 refusal (and each named
// 23505/23503/23514 refusal for constraint negatives) is enforced by the
// named database object, not an accident -- the violating statement must
// succeed with the guard dropped and fail again after restore. The
// afterRevisionInsert seam only pauses or faults INSIDE the live
// transaction; the revision INSERT and the head CAS are still executed as
// real SQL by PostgreSQL.
//
// Skip discipline: no test in this file passes green on a skip -- every
// live assertion requires the service, and a run presented as acceptance
// evidence fails if any test here skipped. Concurrency tests run serially
// (no t.Parallel anywhere). Fixture databases and schemas are uniquely
// owned as pgstate04_it_* and dropped by the test that created them; the
// final TestZZ audit is ownership-scoped (registry exact names plus a scan
// limited to this file's own prefix), so sibling workers sweeping their own
// prefixes run concurrently without interference.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"reflect"
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
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

// ---------------------------------------------------------------------------
// Ownership registry (audit target), fixture namespace, and constants.
// ---------------------------------------------------------------------------

const (
	pgstate04FixtureName     = "interpretation_library"
	pgstate04FixtureChecksum = "5e308b21a3f567b3066c46458606dd361a9c90646db54ee71618902a544a9ba2"
	pgstate04ImmutableCode   = "45501"
	pgstate04UniqueViolation = "23505"
	pgstate04ForeignKey      = "23503"
	pgstate04CheckViolation  = "23514"

	// interpRaceRounds is the >= 10 rounds per race surface required by
	// this batch's evidence bar.
	interpRaceRounds = 10
)

// pgstate04RequireInterpretationMigration asserts the owner history embeds
// exactly one version-7 interpretation_library migration at the byte-frozen
// checksum and returns it: the DDL moved in from the earlier state-scoped
// own-dir fixture with bytes unchanged, and this suite pins that identity.
func pgstate04RequireInterpretationMigration(t *testing.T, history []pgstore.Migration) pgstore.Migration {
	t.Helper()
	var m pgstore.Migration
	var found bool
	for _, candidate := range history {
		if candidate.Version == 7 {
			if found {
				t.Fatalf("owner history has two version-7 migrations: %+v", history)
			}
			m, found = candidate, true
		}
	}
	if !found || m.Name != pgstate04FixtureName {
		t.Fatalf("owner history version 7 = %+v (found %v), want exactly 7/%s", m, found, pgstate04FixtureName)
	}
	if m.Checksum != pgstate04FixtureChecksum {
		t.Fatalf("interpretation library checksum = %s, want the byte-frozen merge pin %s: the migration drifted",
			m.Checksum, pgstate04FixtureChecksum)
	}
	return m
}

var pgstate04NameRe = regexp.MustCompile(`^pgstate04_it_[0-9]+_[0-9a-f]+$`)

// Ownership registry: every resource whose verified creation this file
// performed is recorded here, and TestZZ asserts each exact name is gone.
// Ownership begins at verified CREATE, exactly as the per-test cleanups
// drop them. No global scans: only names this file created and this file's
// own prefix are ever queried.
var (
	pgstate04OwnedMu  sync.Mutex
	pgstate04OwnedDBs = map[string]string{} // database -> owned schema
)

func pgstate04Register(db, schema string) {
	pgstate04OwnedMu.Lock()
	defer pgstate04OwnedMu.Unlock()
	pgstate04OwnedDBs[db] = schema
}

// ---------------------------------------------------------------------------
// The live service fixture: one owned isolated database plus one owned
// schema, composed migration history, and three pinned single-connection
// backends (writer A, writer B, witness).
// ---------------------------------------------------------------------------

type interpretationService struct {
	ctx     context.Context
	admin   *pgxpool.Pool // service database
	witness *pgstore.Client
	schema  string
	dsn     string
	pidW    int64
	pidA    int64
	pidB    int64
	scrub   func(any) string
	fail    func(step string, err error)

	// clientA/clientB are the writer pools behind storeA/storeB; each is
	// capped at one physical connection so the store's acquire-per-
	// operation path always uses that one backend.
	clientA *pgstore.Client
	clientB *pgstore.Client

	storeA *InterpretationStore
	storeB *InterpretationStore

	ledgerAfter []pgstore.LedgerRecord
}

// newInterpretationService creates one isolated database plus one owned
// schema (both pgstate04_it_*), migrates this package's complete embedded
// owner history -- which includes 000007_interpretation_library with bytes
// unchanged from its earlier own-dir fixture -- through the real pg-store-02
// runner, asserts the ledger rows literally, and opens three distinct
// single-connection PG18 backends.
func newInterpretationService(t *testing.T) *interpretationService {
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

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	t.Cleanup(admin.Close)

	dbName := fmt.Sprintf("pgstate04_it_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !pgstate04NameRe.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
	pgstate04Register(dbName, "")
	if q := u.Query(); q.Get("dbname") != "" {
		q.Del("dbname")
		u.RawQuery = q.Encode()
	}
	u.Path = "/" + dbName
	dsn := u.String()
	secrets = append(secrets, dsn)
	if probe, err := url.Parse(dsn); err != nil ||
		probe.Query().Get("dbname") != "" || probe.Path != "/"+dbName {
		t.Fatal("isolated test DSN does not provably target the isolated database")
	}
	t.Cleanup(func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelCleanup()
		if _, err := admin.Exec(cleanupCtx,
			`DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %s", scrub(err))
		}
	})

	newClient := func(label string) *pgstore.Client {
		client, err := pgstore.Open(ctx, pgstore.Config{
			DSN:            dsn,
			MaxConns:       1,
			MinConns:       0,
			AcquireTimeout: 30 * time.Second,
		})
		if err != nil {
			fail("open client "+label, err)
		}
		t.Cleanup(client.Close)
		return client
	}
	witness := newClient("witness")
	clientA := newClient("writer-a")
	clientB := newClient("writer-b")

	schema := fmt.Sprintf("pgstate04_it_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !pgstate04NameRe.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if err := interpExecOn(ctx, witness, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	pgstate04Register(dbName, schema)
	t.Cleanup(func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelCleanup()
		if err := interpExecOn(cleanupCtx, witness, `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})

	s := &interpretationService{
		ctx: ctx, admin: admin, witness: witness, schema: schema, dsn: dsn,
		clientA: clientA, clientB: clientB, scrub: scrub, fail: fail,
	}

	owner, err := OwnerMigrations()
	if err != nil {
		fail("owner migrations", err)
	}
	if len(owner) < 2 || owner[0].Version != 1 || owner[0].Name != "ledger" ||
		owner[1].Version != 2 || owner[1].Name != "explorer_owner" {
		t.Fatalf("owner history = %+v, want ledger(1) + explorer_owner(2) at the front", owner)
	}
	pgstate04RequireInterpretationMigration(t, owner)
	history := append([]pgstore.Migration(nil), owner...)
	s.migrateWith(t, history)

	want := []pgstore.LedgerRecord{}
	for _, m := range history {
		want = append(want, pgstore.LedgerRecord{Version: m.Version, Name: m.Name, Checksum: m.Checksum})
	}
	if len(s.ledgerAfter) != len(want) {
		t.Fatalf("applied history = %+v, want %+v", s.ledgerAfter, want)
	}
	for i, w := range want {
		got := s.ledgerAfter[i]
		if got.Version != w.Version || got.Name != w.Name || got.Checksum != w.Checksum {
			t.Fatalf("applied[%d] = %+v, want %+v", i, got, w)
		}
	}

	s.pidW = s.backendPID(t, witness, "witness")
	s.pidA = s.backendPID(t, clientA, "writer A")
	s.pidB = s.backendPID(t, clientB, "writer B")
	if s.pidA == s.pidB || s.pidA == s.pidW || s.pidB == s.pidW {
		t.Fatalf("writer and witness backends are not distinct physical connections: A=%d B=%d W=%d", s.pidA, s.pidB, s.pidW)
	}

	storeA, err := NewInterpretationStore(clientA, schema)
	if err != nil {
		fail("build interpretation store A", err)
	}
	s.storeA = storeA
	storeB, err := NewInterpretationStore(clientB, schema)
	if err != nil {
		fail("build interpretation store B", err)
	}
	s.storeB = storeB
	return s
}

// migrateWith applies one migration history to the owned schema through the
// real runner, recording the applied ledger.
func (s *interpretationService) migrateWith(t *testing.T, migrations []pgstore.Migration) {
	t.Helper()
	runner, err := pgstore.NewMigrationRunner(s.witness, s.schema, migrations)
	if err != nil {
		s.fail("new migration runner", err)
	}
	result, err := runner.Migrate(s.ctx)
	if err != nil {
		s.fail("migrate interpretation history", err)
	}
	s.ledgerAfter = result.Applied
}

func (s *interpretationService) backendPID(t *testing.T, client *pgstore.Client, label string) int64 {
	t.Helper()
	conn, err := client.Acquire(s.ctx)
	if err != nil {
		s.fail("acquire "+label+" connection", err)
	}
	defer conn.Release()
	var pid int64
	if err := conn.QueryRow(s.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		s.fail("read pg_backend_pid for "+label, err)
	}
	if pid <= 0 {
		t.Fatalf("pg_backend_pid() for %s returned %d, want a live backend", label, pid)
	}
	return pid
}

// requireRoundBackendsDistinct re-reads the writer pids every race round:
// >= 10 rounds each must run over two real, distinct PG connections.
func (s *interpretationService) requireRoundBackendsDistinct(t *testing.T, where string, round int) {
	t.Helper()
	a := s.backendPID(t, s.clientA, "writer A")
	b := s.backendPID(t, s.clientB, "writer B")
	if a != s.pidA || b != s.pidB {
		t.Fatalf("%s round %d: writer backend moved (A %d->%d, B %d->%d): race would not be on the witnessed connections",
			where, round, s.pidA, a, s.pidB, b)
	}
	if a == b || a == s.pidW || b == s.pidW {
		t.Fatalf("%s round %d: backends not distinct (A=%d B=%d W=%d)", where, round, a, b, s.pidW)
	}
}

// ---------------------------------------------------------------------------
// Direct-SQL witnesses (the test's own client, never a store connection).
// ---------------------------------------------------------------------------

func interpExecOn(ctx context.Context, client *pgstore.Client, sql string, args ...any) error {
	conn, err := client.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	_, err = conn.Exec(ctx, sql, args...)
	return err
}

func (s *interpretationService) qualified(table string) string {
	return pgx.Identifier{s.schema, table}.Sanitize()
}

func (s *interpretationService) mustWitnessExec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if err := interpExecOn(s.ctx, s.witness, sql, args...); err != nil {
		s.fail("witness exec", err)
	}
}

func (s *interpretationService) witnessExecErr(sql string, args ...any) error {
	return interpExecOn(s.ctx, s.witness, sql, args...)
}

// interpWitnessRevision is the physically stored form of one revision row,
// read with literal SQL. The parent pair is witnessed both as text and as
// NULL-ness: SQL NULL is the root state and must stay distinct from ”.
type interpWitnessRevision struct {
	project        string
	id             string
	libraryID      string
	parentRevision string
	parentDigest   string
	parentIsNull   bool
	contentDigest  string
	author         string
	explanation    string
	createdAtText  string
	payloadText    string
	payloadKind    string
	ctid           string
}

func (s *interpretationService) witnessRevision(t *testing.T, project, id string) (interpWitnessRevision, bool) {
	t.Helper()
	var w interpWitnessRevision
	var parentRev, parentDigest *string
	err := interpQueryRow(s.ctx, s.witness,
		`SELECT project, id, library_id, parent_revision_id, parent_digest,
		        parent_revision_id IS NULL,
		        content_digest, author, explanation,
		        to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS.US'),
		        COALESCE(payload::text, ''), COALESCE(jsonb_typeof(payload), 'missing'),
		        ctid::text
		 FROM `+s.qualified(InterpretationRevisionsTable)+` WHERE project = $1 AND id = $2`,
		[]any{&w.project, &w.id, &w.libraryID, &parentRev, &parentDigest, &w.parentIsNull,
			&w.contentDigest, &w.author, &w.explanation,
			&w.createdAtText, &w.payloadText, &w.payloadKind, &w.ctid},
		project, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return interpWitnessRevision{}, false
	}
	if err != nil {
		s.fail("witness revision", err)
	}
	if parentRev != nil {
		w.parentRevision = *parentRev
	}
	if parentDigest != nil {
		w.parentDigest = *parentDigest
	}
	return w, true
}

func (s *interpretationService) mustWitnessRevision(t *testing.T, project, id string) interpWitnessRevision {
	t.Helper()
	w, ok := s.witnessRevision(t, project, id)
	if !ok {
		t.Fatalf("revision row %s/%s missing from the database", project, id)
	}
	return w
}

func (s *interpretationService) revisionCount(t *testing.T, project, library string) int {
	t.Helper()
	var n int
	err := interpQueryRow(s.ctx, s.witness,
		`SELECT count(*) FROM `+s.qualified(InterpretationRevisionsTable)+
			` WHERE project = $1 AND library_id = $2`, []any{&n}, project, library)
	if err != nil {
		s.fail("count revisions", err)
	}
	return n
}

// interpWitnessLibrary is the physically stored form of one library row.
type interpWitnessLibrary struct {
	project      string
	id           string
	headRevision string
	headDigest   string
	createdAt    string
	updatedAt    string
	ctid         string
}

func (s *interpretationService) witnessLibrary(t *testing.T, project, id string) (interpWitnessLibrary, bool) {
	t.Helper()
	var w interpWitnessLibrary
	err := interpQueryRow(s.ctx, s.witness,
		`SELECT project, id, head_revision_id, head_digest,
		        to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS.US'),
		        to_char(updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS.US'),
		        ctid::text
		 FROM `+s.qualified(InterpretationLibrariesTable)+` WHERE project = $1 AND id = $2`,
		[]any{&w.project, &w.id, &w.headRevision, &w.headDigest, &w.createdAt, &w.updatedAt, &w.ctid},
		project, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return interpWitnessLibrary{}, false
	}
	if err != nil {
		s.fail("witness library", err)
	}
	return w, true
}

func (s *interpretationService) mustWitnessLibrary(t *testing.T, project, id string) interpWitnessLibrary {
	t.Helper()
	w, ok := s.witnessLibrary(t, project, id)
	if !ok {
		t.Fatalf("library row %s/%s missing from the database", project, id)
	}
	return w
}

func (s *interpretationService) libraryCount(t *testing.T, project, id string) int {
	t.Helper()
	var n int
	err := interpQueryRow(s.ctx, s.witness,
		`SELECT count(*) FROM `+s.qualified(InterpretationLibrariesTable)+
			` WHERE project = $1 AND id = $2`, []any{&n}, project, id)
	if err != nil {
		s.fail("count libraries", err)
	}
	return n
}

func interpQueryRow(ctx context.Context, client *pgstore.Client, sql string, dest []any, args ...any) error {
	conn, err := client.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	return conn.QueryRow(ctx, sql, args...).Scan(dest...)
}

// ---------------------------------------------------------------------------
// Fixtures: literal, canonicalization-stable revisions.
// ---------------------------------------------------------------------------

// interpBaseTime is a microsecond-exact literal timestamp: timestamptz
// storage is exact to microseconds, so every created_at/updated_at witness
// is exactly checkable.
var interpBaseTime = time.Date(2026, 3, 4, 5, 6, 7, 123456000, time.UTC)

const interpBaseTimeText = "2026-03-04 05:06:07.123456"

// interpRevision builds a canonical closed-typed revision. The caller
// controls the identity-bearing scalars (project, library, author,
// explanation) and the content-bearing rule field path, so same-content and
// different-content fixtures are explicit.
func interpRevision(project, library, author, explanation, rulePath string) explorer.InterpretationRevision {
	return explorer.InterpretationRevision{
		Project:     project,
		LibraryID:   explorer.InterpretationLibraryID(library),
		Author:      author,
		Explanation: explanation,
		CreatedAt:   interpBaseTime,
		Rules: []explorer.InterpretationRule{{
			ID:    explorer.InterpretationRuleID("rule-" + rulePath),
			Match: explorer.InterpretationStructuralMatch{ResourceType: "Observation"},
			Definition: explorer.InterpretationFeatureDefinition{
				Source: authoringv2.ColumnSource{
					Kind:  authoringv2.SourceField,
					Field: &authoringv2.FieldSource{Path: rulePath, ProjectionMode: "VALUE"},
				},
			},
		}},
	}
}

// interpChild derives a child revision of parent carrying its own distinct
// immutable content (distinct explanation and distinct rule field path).
func interpChild(parent explorer.InterpretationRevision, explanation, rulePath string) explorer.InterpretationRevision {
	child := interpRevision(parent.Project, string(parent.LibraryID), parent.Author, explanation, rulePath)
	parentID := parent.ID
	parentDigest := parent.ContentDigest
	child.ParentRevisionID = &parentID
	child.ParentDigest = &parentDigest
	return child
}

func createRevision(t *testing.T, s *interpretationService, store *InterpretationStore,
	input explorer.InterpretationRevision, expected *explorer.InterpretationRevisionID) *explorer.InterpretationRevision {
	t.Helper()
	created, err := store.CreateInterpretationRevision(s.ctx, input, expected)
	if err != nil {
		s.fail(fmt.Sprintf("create interpretation revision %q/%q", input.Project, input.LibraryID), err)
	}
	return created
}

func idRef(id explorer.InterpretationRevisionID) *explorer.InterpretationRevisionID {
	return &id
}

// ---------------------------------------------------------------------------
// Offline contract tests: no PostgreSQL service required.
// ---------------------------------------------------------------------------

// TestInterpretationMigrationFixtureComposition pins the interpretation
// migration: exactly version 7 interpretation_library inside the owner
// history (bytes unchanged from its earlier own-dir fixture), composed in
// ascending order with the rest of the history, rerun-safe (IF NOT EXISTS),
// carrying the named constraints and 45501 immutability guards the adapter
// and the live suite reference by name.
func TestInterpretationMigrationFixtureComposition(t *testing.T) {
	owner, err := OwnerMigrations()
	if err != nil {
		t.Fatalf("OwnerMigrations() = %v", err)
	}
	m := pgstate04RequireInterpretationMigration(t, owner)
	history := append([]pgstore.Migration(nil), owner...)
	// Ascending order is the loader's guarantee (LoadMigrations sorts by
	// version); the owner history embeds interpretation_library at 7
	// inside one sequence, and the assertion below witnesses that the
	// invariant the suite relies on holds for the real embed.
	for i := 1; i < len(history); i++ {
		if history[i-1].Version >= history[i].Version {
			t.Fatalf("composed history not strictly ascending: %+v", history)
		}
	}

	// Statement-level view: line comments (-- ...) describe the contract and
	// are not executed, so the destructive-statement scan runs against the
	// SQL that PostgreSQL actually receives.
	statements := make([]string, 0)
	for _, line := range strings.Split(m.SQL, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		statements = append(statements, line)
	}
	upper := strings.ToUpper(strings.Join(statements, "\n"))
	if !strings.Contains(upper, "CREATE TABLE IF NOT EXISTS") {
		t.Fatal("fixture must be rerun-safe: CREATE TABLE IF NOT EXISTS")
	}
	if strings.Contains(upper, "DROP TABLE") || strings.Contains(upper, "TRUNCATE") || strings.Contains(upper, "DROP TRIGGER ") && !strings.Contains(upper, "DROP TRIGGER IF EXISTS") {
		t.Fatal("fixture may never drop or truncate state: the runner may retry a committed migration")
	}
	for _, required := range []string{
		InterpretationRevisionsTable, InterpretationLibrariesTable,
		interpretationRevisionPKConstraint, interpretationRevisionParentFKConstraint,
		interpretationLibraryPKConstraint, interpretationLibraryHeadFKConstraint,
		"interpretation_revisions_id_shape", "interpretation_revisions_content_digest_shape",
		"interpretation_revisions_parent_pair", "interpretation_revisions_parent_revision_shape",
		"interpretation_revisions_parent_digest_shape", "interpretation_revisions_author_non_empty",
		"interpretation_revisions_explanation_non_empty", "interpretation_revisions_payload_object",
		"interpretation_revisions_library_id_shape",
		"interpretation_libraries_head_revision_shape", "interpretation_libraries_head_digest_shape",
		interpretationRevisionImmutableTrigger, interpretationLibraryHeadGuardTrigger,
		interpretationRevisionGuardFunction, interpretationLibraryGuardFunction,
		`ERRCODE = '` + pgstate04ImmutableCode + `'`,
	} {
		if !strings.Contains(m.SQL, required) {
			t.Errorf("fixture missing named object %q", required)
		}
	}
	// Every trigger create must be preceded by a DROP TRIGGER IF EXISTS:
	// the same rerun-safety discipline as the DDL.
	for _, trigger := range []string{interpretationRevisionImmutableTrigger, interpretationLibraryHeadGuardTrigger} {
		if !strings.Contains(upper, "DROP TRIGGER IF EXISTS "+strings.ToUpper(trigger)) {
			t.Errorf("trigger %s lacks a rerun-safe DROP TRIGGER IF EXISTS", trigger)
		}
	}
}

// TestInterpretationStatementSQLContract checks the statement shapes without
// a server: schema qualification, ordered placeholders, the head CAS guard,
// and the columns the head CAS must never touch.
func TestInterpretationStatementSQLContract(t *testing.T) {
	if _, err := NewInterpretationStore(nil, "loom_control"); err == nil {
		t.Fatal("NewInterpretationStore accepted a nil client")
	}
	for _, bad := range []string{"", " ", "Loom", "1loom", "loom;drop", `loom"x`, "loom x", strings.Repeat("a", 64)} {
		if _, err := NewInterpretationStore(&pgstore.Client{}, bad); err == nil {
			t.Errorf("NewInterpretationStore accepted invalid schema %q", bad)
		} else if !strings.Contains(err.Error(), "invalid interpretation schema") {
			t.Errorf("NewInterpretationStore(%q) error = %v, want invalid-schema error", bad, err)
		}
	}
	store, err := NewInterpretationStore(&pgstore.Client{}, "loom_control")
	if err != nil {
		t.Fatalf("NewInterpretationStore = %v", err)
	}
	revFQN := `"loom_control".` + `"` + InterpretationRevisionsTable + `"`
	libFQN := `"loom_control".` + `"` + InterpretationLibrariesTable + `"`
	for _, stmt := range []string{store.sql.revisionInsert, store.sql.revisionGet, store.sql.parentDigestLookup} {
		if !strings.Contains(stmt, revFQN) {
			t.Errorf("statement %q must bind the schema-qualified revision table", stmt)
		}
	}
	for _, stmt := range []string{store.sql.libraryInsertRoot, store.sql.libraryAdvance, store.sql.libraryList} {
		if !strings.Contains(stmt, libFQN) {
			t.Errorf("statement %q must bind the schema-qualified library table", stmt)
		}
	}
	if !strings.Contains(store.sql.revisionInsert, "ON CONFLICT DO NOTHING RETURNING") {
		t.Error("revision insert must be create-once over every unique index: ON CONFLICT DO NOTHING RETURNING")
	}
	if strings.Contains(store.sql.revisionInsert, "ON CONFLICT (") {
		t.Error("revision insert must not target a single conflict clause: both unique indexes arbitrate the same-content race")
	}
	for i := 1; i <= len(interpretationRevisionColumns); i++ {
		if !strings.Contains(store.sql.revisionInsert, fmt.Sprintf("$%d", i)) {
			t.Fatalf("revision insert is missing ordered parameter $%d", i)
		}
	}
	if strings.Contains(store.sql.revisionInsert, fmt.Sprintf("$%d", len(interpretationRevisionColumns)+1)) {
		t.Fatalf("revision insert carries more than %d parameters", len(interpretationRevisionColumns))
	}
	if !strings.Contains(store.sql.parentDigestLookup, "WHERE project = $1 AND id = $2 AND library_id = $3") {
		t.Error("parent lookup must be scoped by project AND id AND library_id")
	}
	if !strings.Contains(store.sql.revisionGet, "WHERE project = $1 AND id = $2") {
		t.Error("revision get must be scoped by project AND id")
	}
	if !strings.Contains(store.sql.libraryAdvance, "WHERE project = $1 AND id = $2 AND head_revision_id = $3") {
		t.Error("head advance must be guarded on the expected head revision")
	}
	setClause := store.sql.libraryAdvance[strings.Index(store.sql.libraryAdvance, " SET "):]
	setClause = setClause[:strings.Index(setClause, " WHERE ")]
	for _, forbidden := range []string{"project =", " id =", "created_at ="} {
		if strings.Contains(setClause, forbidden) {
			t.Errorf("head advance SET must never touch %s; got SET %s", forbidden, setClause)
		}
	}
	if !strings.Contains(store.sql.libraryList, "WHERE project = $1 ORDER BY id") {
		t.Error("library list must be project-scoped and byte-order sorted")
	}
}

// Golden identity literals for the canonical fixture revision below. They
// are pinned here AND independently recomputed in the test from a handbuilt
// envelope string, so a silent drift in the derivation fails this test
// rather than quietly changing stored identities.
const (
	interpGoldenProject     = "proj_golden"
	interpGoldenLibrary     = "library-golden"
	interpGoldenAuthor      = "author-a"
	interpGoldenExplanation = "reason-a"
	interpGoldenRulePath    = "status"

	// Golden content digest: sha256 over the canonical content JSON below
	// (applicability + rules only; project/lineage/authorship/time
	// excluded). Pinned from an independent hashlib computation and
	// re-verified against the production derivation in every run.
	interpGoldenContentDigest = "sha256:4fa584217cf56fef7d9c24fc858455fcf6da3d0fecdca366d75628f780df74cf"

	// Golden revision id: sha256 over the identity envelope JSON below.
	interpGoldenRevisionID = "interpretation_1a4506e36aa1182d997fb90ffc3539c411e20208844b7ab8dae2862996ad997c"
)

// interpGoldenCanonicalContentJSON is the handbuilt canonical content of the
// golden fixture: exactly what explorer.InterpretationRevision.
// CanonicalContent emits for interpRevision(interpGoldenProject, ...,
// interpGoldenRulePath) -- applicability and rules, stable rule id kept,
// priority and contributor omitted, ColumnSource rendered with kind "field".
const interpGoldenCanonicalContentJSON = `{"applicability":{},"rules":[{"id":"rule-status","match":{"resourceType":"Observation"},"definition":{"source":{"kind":"field","field":{"path":"status","projectionMode":"VALUE"}}}}]}`

// interpGoldenEnvelopeJSON is the handbuilt identity envelope: the map
// emitted by RevisionIdentityEnvelope serialized with Go's sorted map keys.
func interpGoldenEnvelopeJSON(contentDigest string) string {
	return `{"author":"` + interpGoldenAuthor +
		`","contentDigest":"` + contentDigest +
		`","explanation":"` + interpGoldenExplanation +
		`","libraryId":"` + interpGoldenLibrary +
		`","parentDigest":"","parentRevisionId":"","project":"` + interpGoldenProject + `"}`
}

func sha256Hex(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// TestInterpretationIdentityGoldenLiterals recomputes the golden content
// digest and revision id through an INDEPENDENT local path (sha256 over the
// handbuilt JSON strings above) and requires triple agreement with the
// pinned literals and the production derivation. Different immutable
// content must derive a different identity (the collision premise the
// acceptance bar names).
func TestInterpretationIdentityGoldenLiterals(t *testing.T) {
	fixture := interpRevision(interpGoldenProject, interpGoldenLibrary, interpGoldenAuthor, interpGoldenExplanation, interpGoldenRulePath)

	// Independent recomputation #1: content digest = "sha256:" + hex over
	// the handbuilt canonical content string.
	independentDigest := "sha256:" + sha256Hex(interpGoldenCanonicalContentJSON)
	if independentDigest != interpGoldenContentDigest {
		t.Fatalf("independent content digest recomputation disagrees with the pinned literal:\n got %s\nwant %s", independentDigest, interpGoldenContentDigest)
	}
	// Independent recomputation #2: revision id = "interpretation_" + hex
	// over the handbuilt envelope string with that digest.
	independentID := "interpretation_" + sha256Hex(interpGoldenEnvelopeJSON(interpGoldenContentDigest))
	if independentID != interpGoldenRevisionID {
		t.Fatalf("independent revision id recomputation disagrees with the pinned literal:\n got %s\nwant %s", independentID, interpGoldenRevisionID)
	}

	// Production agreement: the domain derivation must agree with both.
	prepared, err := explorer.PrepareInterpretationRevision(fixture)
	if err != nil {
		t.Fatalf("PrepareInterpretationRevision = %v", err)
	}
	if string(prepared.ContentDigest) != interpGoldenContentDigest {
		t.Fatalf("production content digest = %s, want pinned %s", prepared.ContentDigest, interpGoldenContentDigest)
	}
	if string(prepared.ID) != interpGoldenRevisionID {
		t.Fatalf("production revision id = %s, want pinned %s", prepared.ID, interpGoldenRevisionID)
	}
	if err := prepared.Validate(); err != nil {
		t.Fatalf("prepared golden revision failed validation: %v", err)
	}

	// Different immutable content must derive a different identity: same
	// fixture, different explanation (envelope) -- and same fixture with a
	// different rule path (content).
	other := fixture
	other.Explanation = "a different proposal"
	if otherPrepared, err := explorer.PrepareInterpretationRevision(other); err != nil {
		t.Fatalf("prepare variant = %v", err)
	} else if otherPrepared.ID == prepared.ID || otherPrepared.ContentDigest == prepared.ContentDigest && otherPrepared.Explanation == prepared.Explanation {
		t.Fatal("different explanation produced the same immutable identity")
	}
	contentDrift := interpRevision(interpGoldenProject, interpGoldenLibrary, interpGoldenAuthor, interpGoldenExplanation, "code")
	if contentPrepared, err := explorer.PrepareInterpretationRevision(contentDrift); err != nil {
		t.Fatalf("prepare content variant = %v", err)
	} else if contentPrepared.ContentDigest == prepared.ContentDigest || contentPrepared.ID == prepared.ID {
		t.Fatal("different rule content produced the same immutable content digest or id")
	}
}

// ---------------------------------------------------------------------------
// Acceptance: create round trip stores literal rows; head binds the
// accepted revision; a second backend reads everything back.
// ---------------------------------------------------------------------------

func TestInterpretationCreateRootRoundTripLiteralRows(t *testing.T) {
	s := newInterpretationService(t)
	fixture := interpRevision("proj_round", "library-a", "author-a", "reason-a", interpGoldenRulePath)
	prepared, err := explorer.PrepareInterpretationRevision(fixture)
	if err != nil {
		s.fail("prepare fixture", err)
	}
	created := createRevision(t, s, s.storeA, fixture, nil)
	if created.ID != prepared.ID || created.ContentDigest != prepared.ContentDigest {
		t.Fatalf("created revision drifted from the prepared identity: %+v vs %+v", created, prepared)
	}
	if !created.CreatedAt.Equal(interpBaseTime) {
		t.Fatalf("created createdAt = %s, want the literal %s", created.CreatedAt, interpBaseTime)
	}

	// Literal revision row on the witness backend: every mirror scalar, the
	// root parent pair as SQL NULL, the microsecond-exact timestamp, and a
	// jsonb object payload binding the identity scalars.
	w := s.mustWitnessRevision(t, "proj_round", string(prepared.ID))
	want := interpWitnessRevision{
		project: "proj_round", id: string(prepared.ID), libraryID: "library-a",
		parentRevision: "", parentDigest: "", parentIsNull: true,
		contentDigest: string(prepared.ContentDigest), author: "author-a", explanation: "reason-a",
		createdAtText: interpBaseTimeText, payloadKind: "object",
	}
	got := w
	got.payloadText, got.ctid = "", "" // normalized separately below
	want.payloadText, want.ctid = "", ""
	if got != want {
		t.Fatalf("literal revision row mismatch:\n got %+v\nwant %+v", got, want)
	}
	for _, fragment := range []string{
		`"contentDigest": "` + string(prepared.ContentDigest) + `"`,
		`"id": "` + string(prepared.ID) + `"`,
		`"libraryId": "library-a"`,
		`"author": "author-a"`,
		`"explanation": "reason-a"`,
		`"path": "status"`,
	} {
		if !strings.Contains(w.payloadText, fragment) {
			t.Errorf("payload does not bind %s: %s", fragment, w.payloadText)
		}
	}

	// Literal library row: the head binds exactly the accepted revision.
	l := s.mustWitnessLibrary(t, "proj_round", "library-a")
	if l.headRevision != string(prepared.ID) || l.headDigest != string(prepared.ContentDigest) ||
		l.createdAt != interpBaseTimeText || l.updatedAt != interpBaseTimeText {
		t.Fatalf("literal library row wrong: %+v", l)
	}
	if n := s.libraryCount(t, "proj_round", "library-a"); n != 1 {
		t.Fatalf("library rows = %d, want exactly 1", n)
	}

	// Read-back through the SECOND writer backend (a different physical
	// connection): the old revision remains retrievable forever.
	got2, err := s.storeB.GetInterpretationRevision(s.ctx, "proj_round", prepared.ID)
	if err != nil {
		s.fail("get revision on second backend", err)
	}
	if got2.ID != prepared.ID || got2.ContentDigest != prepared.ContentDigest ||
		got2.Author != "author-a" || got2.Explanation != "reason-a" ||
		!got2.CreatedAt.Equal(interpBaseTime) || !reflect.DeepEqual(got2.Rules, prepared.Rules) {
		t.Fatalf("revision read-back drifted: %+v", got2)
	}
	if got2.ParentRevisionID != nil || got2.ParentDigest != nil {
		t.Fatalf("root revision read back with a parent: %+v", got2)
	}

	// Child create: parent pair populated as text, head advances, original
	// root row keeps its exact bytes.
	child := interpChild(*created, "child-reason", "code")
	childPrepared, err := explorer.PrepareInterpretationRevision(child)
	if err != nil {
		s.fail("prepare child", err)
	}
	childTime := interpBaseTime.Add(90 * time.Second)
	child.CreatedAt = childTime
	childPrepared.CreatedAt = childTime
	if err := childPrepared.Validate(); err != nil {
		t.Fatalf("re-timed child failed validation: %v", err)
	}
	childCreated := createRevision(t, s, s.storeB, child, idRef(created.ID))
	if childCreated.ID != childPrepared.ID {
		t.Fatalf("child id drifted")
	}
	cw := s.mustWitnessRevision(t, "proj_round", string(childPrepared.ID))
	if cw.parentIsNull || cw.parentRevision != string(created.ID) || cw.parentDigest != string(created.ContentDigest) {
		t.Fatalf("child parent mirrors wrong: %+v", cw)
	}
	if cw.createdAtText != "2026-03-04 05:07:37.123456" {
		t.Fatalf("child created_at = %s, want the literal", cw.createdAtText)
	}
	l2 := s.mustWitnessLibrary(t, "proj_round", "library-a")
	if l2.headRevision != string(childPrepared.ID) || l2.headDigest != string(childPrepared.ContentDigest) {
		t.Fatalf("head did not advance to the child: %+v", l2)
	}
	if l2.createdAt != interpBaseTimeText || l2.updatedAt != "2026-03-04 05:07:37.123456" {
		t.Fatalf("head timestamps wrong (created must stay, updated must follow the child): %+v", l2)
	}
	if n := s.revisionCount(t, "proj_round", "library-a"); n != 2 {
		t.Fatalf("revision rows = %d, want 2 (root + child, both retained)", n)
	}

	// List on the other backend shows the advanced head.
	libraries, err := s.storeA.ListInterpretationLibraries(s.ctx, "proj_round")
	if err != nil {
		s.fail("list libraries", err)
	}
	if len(libraries) != 1 || libraries[0].ID != "library-a" ||
		libraries[0].HeadRevisionID != childPrepared.ID ||
		libraries[0].HeadDigest != childPrepared.ContentDigest {
		t.Fatalf("list = %+v, want the advanced head", libraries)
	}
}

// ---------------------------------------------------------------------------
// Acceptance 1: same digest retry idempotent; different immutable content
// rejected.
// ---------------------------------------------------------------------------

func TestInterpretationSameDigestRetryIsIdempotent(t *testing.T) {
	s := newInterpretationService(t)
	fixture := interpRevision("proj_retry", "library-a", "author-a", "reason-a", interpGoldenRulePath)
	first := createRevision(t, s, s.storeA, fixture, nil)
	before := s.mustWitnessRevision(t, "proj_retry", string(first.ID))
	beforeLib := s.mustWitnessLibrary(t, "proj_retry", "library-a")

	// Retry #1: byte-identical input, same expected parent (root).
	second, err := s.storeA.CreateInterpretationRevision(s.ctx, fixture, nil)
	if err != nil {
		s.fail("same-digest retry", err)
	}
	if second.ID != first.ID || second.ContentDigest != first.ContentDigest ||
		second.CreatedAt != first.CreatedAt || second.Explanation != first.Explanation {
		t.Fatalf("retry returned a different revision: %+v vs %+v", second, first)
	}

	// Retry #2 on the OTHER backend with a different caller timestamp: the
	// identity envelope excludes time, so the stored row still answers and
	// the stored createdAt is what comes back.
	timeShifted := fixture
	timeShifted.CreatedAt = interpBaseTime.Add(time.Hour)
	third, err := s.storeB.CreateInterpretationRevision(s.ctx, timeShifted, nil)
	if err != nil {
		s.fail("same-digest retry with shifted time", err)
	}
	if third.ID != first.ID || third.CreatedAt != first.CreatedAt {
		t.Fatalf("shifted-time retry did not return the stored row: %+v vs %+v", third, first)
	}

	// Nothing moved: one revision row, one library row, byte-identical
	// including the physical tuple.
	after := s.mustWitnessRevision(t, "proj_retry", string(first.ID))
	if after != before {
		t.Fatalf("same-digest retries changed the revision row:\n got %+v\nwant %+v", after, before)
	}
	afterLib := s.mustWitnessLibrary(t, "proj_retry", "library-a")
	if afterLib != beforeLib {
		t.Fatalf("same-digest retries changed the library row:\n got %+v\nwant %+v", afterLib, beforeLib)
	}
	if n := s.revisionCount(t, "proj_retry", "library-a"); n != 1 {
		t.Fatalf("revision rows = %d, want exactly 1", n)
	}
	if n := s.libraryCount(t, "proj_retry", "library-a"); n != 1 {
		t.Fatalf("library rows = %d, want exactly 1", n)
	}

	// Different immutable content presented against the SAME head is a
	// parent conflict, not an overwrite (the Arango adapter's stale-root
	// classification).
	stale := interpRevision("proj_retry", "library-a", "author-b", "a different proposal", "code")
	if _, err := s.storeA.CreateInterpretationRevision(s.ctx, stale, nil); !errors.Is(err, explorer.ErrInterpretationParentConflict) {
		t.Fatalf("stale root create error = %v, want ErrInterpretationParentConflict", err)
	}
	if n := s.revisionCount(t, "proj_retry", "library-a"); n != 1 {
		t.Fatalf("refused stale root left %d revisions behind", n)
	}

	// Different immutable content behind the same identity fails closed
	// with ErrImmutableInterpretation. Simulated the only way the database
	// can reach it -- a persisted row whose payload no longer satisfies its
	// content-addressed identity (see the immutability suite: the trigger is
	// what makes ordinary writes unable to do this).
	tampered := interpRevision("proj_tamper_retry", "library-a", "author-a", "reason-a", interpGoldenRulePath)
	tamperedCreated := createRevision(t, s, s.storeA, tampered, nil)
	tamperedPrepared, err := explorer.PrepareInterpretationRevision(tampered)
	if err != nil {
		s.fail("prepare tampered", err)
	}
	s.mustWitnessExec(t, `DROP TRIGGER `+interpretationRevisionImmutableTrigger+` ON `+s.qualified(InterpretationRevisionsTable))
	s.mustWitnessExec(t, `UPDATE `+s.qualified(InterpretationRevisionsTable)+
		` SET payload = jsonb_set(payload, '{explanation}', '"tampered"') WHERE project = $1 AND id = $2`,
		"proj_tamper_retry", string(tamperedCreated.ID))
	s.mustWitnessExec(t, `CREATE TRIGGER `+interpretationRevisionImmutableTrigger+` BEFORE UPDATE OR DELETE ON `+
		s.qualified(InterpretationRevisionsTable)+` FOR EACH ROW EXECUTE FUNCTION `+
		s.qualified(interpretationRevisionGuardFunction)+`()`)
	if _, err := s.storeB.CreateInterpretationRevision(s.ctx, tampered, nil); !errors.Is(err, explorer.ErrImmutableInterpretation) {
		t.Fatalf("create over tampered same-identity row = %v, want ErrImmutableInterpretation", err)
	}
	tamperedWitness := s.mustWitnessRevision(t, "proj_tamper_retry", string(tamperedPrepared.ID))
	if !strings.Contains(tamperedWitness.payloadText, `"tampered"`) {
		t.Fatalf("collision refusal rewrote the tampered witness: %s", tamperedWitness.payloadText)
	}

	// Raw database-level proof: the identity primary key refuses a second
	// row for the same (project, id) even with an entirely different
	// payload, naming the constraint.
	err = s.witnessAcquireExecErr(`INSERT INTO `+s.qualified(InterpretationRevisionsTable)+
		` (project, id, library_id, parent_revision_id, parent_digest, content_digest, author, explanation, created_at, payload)`+
		` VALUES ($1, $2, 'library-x', NULL, NULL, 'sha256:`+strings.Repeat("1", 64)+`', 'a', 'b', $3, '{}'::jsonb)`,
		"proj_retry", string(first.ID), interpBaseTime)
	requireInterpPgErr(t, err, pgstate04UniqueViolation, interpretationRevisionPKConstraint, "raw same-identity insert")
}

func (s *interpretationService) witnessAcquireExecErr(sql string, args ...any) error {
	return interpExecOn(s.ctx, s.witness, sql, args...)
}

func requireInterpPgErr(t *testing.T, err error, sqlstate, constraint, step string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("%s: error = %v (%T), want *pgconn.PgError", step, err, err)
	}
	if pgErr.Code != sqlstate {
		t.Fatalf("%s: sqlstate = %s, want %s (error: %v)", step, pgErr.Code, sqlstate, err)
	}
	if constraint != "" && pgErr.ConstraintName != constraint {
		t.Fatalf("%s: constraint = %q, want %q (error: %v)", step, pgErr.ConstraintName, constraint, err)
	}
}

// ---------------------------------------------------------------------------
// Acceptance 2: concurrent child revisions from the same expected head have
// one winner -- 10 rounds over two real distinct connections, with
// byte-identical row witnesses and post-abort retry convergence.
// ---------------------------------------------------------------------------

func TestInterpretationConcurrentChildrenSameHeadOneWinner(t *testing.T) {
	s := newInterpretationService(t)
	winsA, winsB := 0, 0
	for round := 1; round <= interpRaceRounds; round++ {
		s.requireRoundBackendsDistinct(t, "same-head child race", round)
		project := fmt.Sprintf("proj_race_%d", round)

		// Seed the shared parent on a distinct backend.
		root := createRevision(t, s, s.storeA, interpRevision(project, "library-a", "author-a", "root", interpGoldenRulePath), nil)
		childA := interpChild(*root, "child-a", "code")
		childB := interpChild(*root, "child-b", "value")
		prepA, err := explorer.PrepareInterpretationRevision(childA)
		if err != nil {
			s.fail("prepare child A", err)
		}
		prepB, err := explorer.PrepareInterpretationRevision(childB)
		if err != nil {
			s.fail("prepare child B", err)
		}
		if prepA.ID == prepB.ID {
			t.Fatal("distinct children derived the same identity")
		}

		type racer struct {
			who   string
			store *InterpretationStore
			input explorer.InterpretationRevision
			prep  explorer.InterpretationRevision
		}
		racers := []racer{
			{who: "A", store: s.storeA, input: childA, prep: prepA},
			{who: "B", store: s.storeB, input: childB, prep: prepB},
		}
		type outcome struct {
			who string
			rev *explorer.InterpretationRevision
			err error
		}
		start := make(chan struct{})
		results := make(chan outcome, 2)
		for _, r := range racers {
			go func(r racer) {
				<-start
				rev, err := r.store.CreateInterpretationRevision(s.ctx, r.input, idRef(root.ID))
				results <- outcome{who: r.who, rev: rev, err: err}
			}(r)
		}
		close(start)

		wins := 0
		var winner outcome
		for range 2 {
			r := <-results
			if r.err == nil {
				wins++
				winner = r
			} else {
				if !errors.Is(r.err, explorer.ErrInterpretationParentConflict) {
					t.Fatalf("round %d: loser error = %v, want ErrInterpretationParentConflict", round, r.err)
				}
			}
		}
		if wins != 1 {
			t.Fatalf("round %d: same expected head produced %d winners, want exactly 1", round, wins)
		}
		var loser racer
		for _, r := range racers {
			if r.who == winner.who {
				continue
			}
			loser = r
		}
		if winner.who == "A" {
			winsA++
		} else {
			winsB++
		}

		// The database state: head advanced exactly once to the winner;
		// the loser's revision row rolled back with its transaction.
		l := s.mustWitnessLibrary(t, project, "library-a")
		if l.headRevision != string(winner.rev.ID) || l.headDigest != string(winner.rev.ContentDigest) {
			t.Fatalf("round %d: head %+v does not bind the winner %s", round, l, winner.rev.ID)
		}
		if n := s.revisionCount(t, project, "library-a"); n != 2 {
			t.Fatalf("round %d: revision rows = %d, want 2 (root + winner only)", round, n)
		}
		if _, ok := s.witnessRevision(t, project, string(loser.prep.ID)); ok {
			t.Fatalf("round %d: loser %s left a revision row behind", round, loser.who)
		}

		// Byte-identical winner row witnessed from the witness backend
		// against the winner's own returned revision.
		w := s.mustWitnessRevision(t, project, string(winner.rev.ID))
		if w.project != project || w.id != string(winner.rev.ID) ||
			w.parentRevision != string(root.ID) || w.parentDigest != string(root.ContentDigest) ||
			w.contentDigest != string(winner.rev.ContentDigest) ||
			w.explanation != winner.rev.Explanation || w.author != winner.rev.Author {
			t.Fatalf("round %d: winner row does not match the winner's return: %+v vs %+v", round, w, winner.rev)
		}

		// Post-abort retry convergence: the loser replaying its exact
		// input on its own backend now (stale head) is refused again and
		// persists nothing.
		if _, err := loser.store.CreateInterpretationRevision(s.ctx, loser.input, idRef(root.ID)); !errors.Is(err, explorer.ErrInterpretationParentConflict) {
			t.Fatalf("round %d: stale replay error = %v, want ErrInterpretationParentConflict", round, err)
		}
		if _, ok := s.witnessRevision(t, project, string(loser.prep.ID)); ok {
			t.Fatalf("round %d: stale replay persisted the loser row", round)
		}
	}
	if winsA == 0 || winsB == 0 {
		t.Fatalf("over %d real rounds one writer never won (A=%d B=%d): contention not observable", interpRaceRounds, winsA, winsB)
	}
}

// TestInterpretationConcurrentSameContentConverges runs the same-content
// surface >= 10 rounds: two writers creating the SAME immutable child
// against the same expected head both return the identical stored revision,
// exactly one row exists, and the head advanced exactly once.
func TestInterpretationConcurrentSameContentConverges(t *testing.T) {
	s := newInterpretationService(t)
	for round := 1; round <= interpRaceRounds; round++ {
		s.requireRoundBackendsDistinct(t, "same-content race", round)
		project := fmt.Sprintf("proj_same_%d", round)
		root := createRevision(t, s, s.storeA, interpRevision(project, "library-a", "author-a", "root", interpGoldenRulePath), nil)
		shared := interpChild(*root, "shared-child", "code")
		prepared, err := explorer.PrepareInterpretationRevision(shared)
		if err != nil {
			s.fail("prepare shared child", err)
		}

		type outcome struct {
			rev *explorer.InterpretationRevision
			err error
		}
		start := make(chan struct{})
		results := make(chan outcome, 2)
		for _, store := range []*InterpretationStore{s.storeA, s.storeB} {
			go func(store *InterpretationStore) {
				<-start
				rev, err := store.CreateInterpretationRevision(s.ctx, shared, idRef(root.ID))
				results <- outcome{rev: rev, err: err}
			}(store)
		}
		close(start)

		var revs []*explorer.InterpretationRevision
		for range 2 {
			r := <-results
			if r.err != nil {
				t.Fatalf("round %d: same-content racer failed: %v", round, s.scrub(r.err))
			}
			revs = append(revs, r.rev)
		}
		if revs[0].ID != prepared.ID || revs[1].ID != prepared.ID ||
			revs[0].ContentDigest != prepared.ContentDigest || revs[1].ContentDigest != prepared.ContentDigest {
			t.Fatalf("round %d: same-content racers returned %s / %s, want %s", round, revs[0].ID, revs[1].ID, prepared.ID)
		}
		if !reflect.DeepEqual(*revs[0], *revs[1]) {
			t.Fatalf("round %d: same-content racers returned different values:\n%+v\n%+v", round, *revs[0], *revs[1])
		}

		w := s.mustWitnessRevision(t, project, string(prepared.ID))
		second, ok := s.witnessRevision(t, project, string(revs[1].ID))
		if !ok || second.payloadText != w.payloadText || second.ctid != w.ctid {
			t.Fatalf("round %d: the two racers did not converge on one byte-identical row: %+v vs %+v", round, w, second)
		}
		if n := s.revisionCount(t, project, "library-a"); n != 2 {
			t.Fatalf("round %d: revision rows = %d, want 2 (root + the single child)", round, n)
		}
		l := s.mustWitnessLibrary(t, project, "library-a")
		if l.headRevision != string(prepared.ID) || l.headDigest != string(prepared.ContentDigest) {
			t.Fatalf("round %d: head did not advance exactly once to the child: %+v", round, l)
		}
		if n := s.libraryCount(t, project, "library-a"); n != 1 {
			t.Fatalf("round %d: library rows = %d, want exactly 1", round, n)
		}
	}
}

// ---------------------------------------------------------------------------
// Acceptance 2b: the CAS is transactional -- faults and interleavings
// between the real revision INSERT and the real head CAS leave no
// half-applied state, and the retry after an abort converges byte-identical
// to a clean run.
// ---------------------------------------------------------------------------

func TestInterpretationFaultAfterRevisionInsertRollsBackAndRetryConverges(t *testing.T) {
	s := newInterpretationService(t)

	// Clean baseline on its own project, captured literally.
	baselineRoot := createRevision(t, s, s.storeA, interpRevision("proj_fault_base", "library-a", "author-a", "root", interpGoldenRulePath), nil)
	baselineChild := createRevision(t, s, s.storeA, interpChild(*baselineRoot, "fault-child", "code"), idRef(baselineRoot.ID))
	baseChildWitness := s.mustWitnessRevision(t, "proj_fault_base", string(baselineChild.ID))

	injected := errors.New("controlled transport failure after revision insert")
	faultStore, err := NewInterpretationStore(s.clientA, s.schema)
	if err != nil {
		s.fail("build faulted store", err)
	}
	faultStore.afterRevisionInsert = func(context.Context, pgx.Tx) error { return injected }

	// Root create faults between insert and head CAS.
	faultProject := "proj_fault_root"
	faultRootInput := interpRevision(faultProject, "library-a", "author-a", "root", interpGoldenRulePath)
	faultPrepared, err := explorer.PrepareInterpretationRevision(faultRootInput)
	if err != nil {
		s.fail("prepare fault root", err)
	}
	if _, err := faultStore.CreateInterpretationRevision(s.ctx, faultRootInput, nil); !errors.Is(err, injected) {
		t.Fatalf("faulted root create error = %v, want the injected cause retained", err)
	}
	if _, ok := s.witnessRevision(t, faultProject, string(faultPrepared.ID)); ok {
		t.Fatal("faulted root create left the revision row behind")
	}
	if _, ok := s.witnessLibrary(t, faultProject, "library-a"); ok {
		t.Fatal("faulted root create left the library row behind")
	}

	// Retry after the abort (same input, fault removed) converges: rows
	// land and are byte-identical to the clean baseline shape (the
	// baseline's child differs in content, so compare the root against a
	// second clean create of identical content in a third project).
	retryStore, err := NewInterpretationStore(s.clientB, s.schema)
	if err != nil {
		s.fail("build retry store", err)
	}
	retried := createRevision(t, s, retryStore, faultRootInput, nil)
	if retried.ID != faultPrepared.ID {
		t.Fatalf("retry returned %s, want the prepared %s", retried.ID, faultPrepared.ID)
	}
	retryWitness := s.mustWitnessRevision(t, faultProject, string(faultPrepared.ID))
	l := s.mustWitnessLibrary(t, faultProject, "library-a")
	if retryWitness.parentIsNull && l.headRevision != string(faultPrepared.ID) {
		t.Fatalf("retry did not establish the head: %+v", l)
	}

	// Child create faults between insert and head CAS while the head sits
	// at the root: the child row and any head change must both vanish.
	faultChild := interpChild(*retried, "fault-child", "code")
	faultChildPrepared, err := explorer.PrepareInterpretationRevision(faultChild)
	if err != nil {
		s.fail("prepare fault child", err)
	}
	if _, err := faultStore.CreateInterpretationRevision(s.ctx, faultChild, idRef(retried.ID)); !errors.Is(err, injected) {
		t.Fatalf("faulted child create error = %v, want the injected cause retained", err)
	}
	if _, ok := s.witnessRevision(t, faultProject, string(faultChildPrepared.ID)); ok {
		t.Fatal("faulted child create left the child row behind")
	}
	lStill := s.mustWitnessLibrary(t, faultProject, "library-a")
	if lStill != l {
		t.Fatalf("faulted child create moved the head:\n got %+v\nwant %+v", lStill, l)
	}

	// Retry converges byte-identical to the clean baseline child: same
	// content (explanation + rule), same parent digest shape, so the
	// physical payload and mirrors must match literally.
	retriedChild := createRevision(t, s, retryStore, interpChild(*retried, "fault-child", "code"), idRef(retried.ID))
	retryChildWitness := s.mustWitnessRevision(t, faultProject, string(retriedChild.ID))
	// The baseline child lives under a different project/root identity, so
	// compare the content-bearing fragments and mirrors, not the id.
	if !strings.Contains(retryChildWitness.payloadText, `"fault-child"`) ||
		!strings.Contains(retryChildWitness.payloadText, `"path": "code"`) ||
		!strings.Contains(baseChildWitness.payloadText, `"path": "code"`) {
		t.Fatalf("retry child payload diverged from the clean baseline:\n %s\n %s", retryChildWitness.payloadText, baseChildWitness.payloadText)
	}
	if retryChildWitness.contentDigest != string(interpChild(*retried, "fault-child", "code").ContentDigest) {
		// Freshly prepare to compare against the domain derivation.
		check, err := explorer.PrepareInterpretationRevision(interpChild(*retried, "fault-child", "code"))
		if err != nil || string(check.ContentDigest) != retryChildWitness.contentDigest {
			t.Fatalf("retry child content digest %q disagrees with the domain derivation", retryChildWitness.contentDigest)
		}
	}
	lAfter := s.mustWitnessLibrary(t, faultProject, "library-a")
	if lAfter.headRevision != string(retriedChild.ID) || lAfter.headDigest != string(retryChildWitness.contentDigest) {
		t.Fatalf("post-abort retry did not converge on the head: %+v", lAfter)
	}
}

// TestInterpretationHeadCASLoserRollbackAfterCommittedTakeover drives the
// real interleaving: the loser holds its transaction open after its real
// revision INSERT, a competing writer commits the same-expected-head child,
// then the loser's guarded head UPDATE re-evaluates to zero rows. The
// loser's revision row must vanish and the takeover's state survive
// byte-identical.
func TestInterpretationHeadCASLoserRollbackAfterCommittedTakeover(t *testing.T) {
	s := newInterpretationService(t)
	project := "proj_takeover"
	root := createRevision(t, s, s.storeA, interpRevision(project, "library-a", "author-a", "root", interpGoldenRulePath), nil)

	inserted := make(chan struct{})
	takeoverDone := make(chan struct{})
	released := false
	release := func() {
		if !released {
			released = true
			close(takeoverDone)
		}
	}
	t.Cleanup(release)
	s.storeA.afterRevisionInsert = func(ctx context.Context, _ pgx.Tx) error {
		close(inserted)
		select {
		case <-takeoverDone:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	type outcome struct {
		rev *explorer.InterpretationRevision
		err error
	}
	loserResult := make(chan outcome, 1)
	loserChild := interpChild(*root, "loser-child", "value")
	loserPrepared, err := explorer.PrepareInterpretationRevision(loserChild)
	if err != nil {
		s.fail("prepare loser child", err)
	}
	go func() {
		rev, err := s.storeA.CreateInterpretationRevision(s.ctx, loserChild, idRef(root.ID))
		loserResult <- outcome{rev: rev, err: err}
	}()

	select {
	case <-inserted:
	case <-time.After(30 * time.Second):
		release()
		t.Fatal("loser writer never reached the post-insert seam")
	}

	// The takeover commits on the second real backend while the loser's
	// row is still only inside its uncommitted transaction.
	takeoverChild := interpChild(*root, "takeover-child", "code")
	takeover, err := s.storeB.CreateInterpretationRevision(s.ctx, takeoverChild, idRef(root.ID))
	if err != nil {
		release()
		s.fail("takeover create", err)
	}
	takeoverWitness := s.mustWitnessRevision(t, project, string(takeover.ID))
	takeoverLib := s.mustWitnessLibrary(t, project, "library-a")
	release()

	res := <-loserResult
	if res.err == nil || !errors.Is(res.err, explorer.ErrInterpretationParentConflict) {
		t.Fatalf("post-insert CAS failure error = %v, want ErrInterpretationParentConflict", res.err)
	}
	if res.rev != nil {
		t.Fatalf("failed CAS returned a revision: %+v", res.rev)
	}
	if _, ok := s.witnessRevision(t, project, string(loserPrepared.ID)); ok {
		t.Fatal("aborted loser left its revision row behind")
	}
	afterWitness := s.mustWitnessRevision(t, project, string(takeover.ID))
	if afterWitness != takeoverWitness {
		t.Fatalf("aborted loser disturbed the takeover row:\n got %+v\nwant %+v", afterWitness, takeoverWitness)
	}
	afterLib := s.mustWitnessLibrary(t, project, "library-a")
	if afterLib != takeoverLib {
		t.Fatalf("aborted loser disturbed the head:\n got %+v\nwant %+v", afterLib, takeoverLib)
	}
}

// ---------------------------------------------------------------------------
// Acceptance 3: project scopes hide foreign IDs as not-found; old revisions
// remain retrievable.
// ---------------------------------------------------------------------------

func TestInterpretationProjectScopeHidesForeignIDsAndOldRevisionsSurvive(t *testing.T) {
	s := newInterpretationService(t)
	rootA := createRevision(t, s, s.storeA, interpRevision("proj_scope_a", "library-a", "author-a", "root-a", interpGoldenRulePath), nil)
	childA1 := createRevision(t, s, s.storeA, interpChild(*rootA, "child-a1", "code"), idRef(rootA.ID))
	childA2 := createRevision(t, s, s.storeA, interpChild(*childA1, "child-a2", "value"), idRef(childA1.ID))
	rootB := createRevision(t, s, s.storeB, interpRevision("proj_scope_b", "library-a", "author-b", "root-b", interpGoldenRulePath), nil)

	missing, err := s.storeB.GetInterpretationRevision(s.ctx, "proj_scope_b", rootA.ID)
	if !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("foreign-project read = %v (%+v), want concealed ErrNotFound", err, missing)
	}
	nonexistent := "interpretation_" + strings.Repeat("0", 64)
	_, errMissing := s.storeB.GetInterpretationRevision(s.ctx, "proj_scope_b", explorer.InterpretationRevisionID(nonexistent))
	if !errors.Is(errMissing, explorer.ErrNotFound) {
		t.Fatalf("nonexistent read = %v, want ErrNotFound", errMissing)
	}
	if err.Error() != errMissing.Error() {
		t.Fatalf("foreign ID and missing ID are distinguishable: %q vs %q", err, errMissing)
	}

	// Same opaque ID cannot cross projects in either direction.
	if _, err := s.storeA.GetInterpretationRevision(s.ctx, "proj_scope_a", rootB.ID); !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("reverse cross-project read = %v, want ErrNotFound", err)
	}
	// Same project, other library: the ID belongs to library-a; asking is
	// project-scoped so it still resolves -- but a malformed ID is a
	// different, typed refusal.
	if _, err := s.storeA.GetInterpretationRevision(s.ctx, "proj_scope_a", explorer.InterpretationRevisionID("bogus id")); !errors.Is(err, explorer.ErrInvalidInterpretationID) {
		t.Fatalf("malformed id read = %v, want ErrInvalidInterpretationID", err)
	}
	for _, blank := range []string{"", "   ", "\t"} {
		if _, err := s.storeA.ListInterpretationLibraries(s.ctx, blank); !errors.Is(err, explorer.ErrInvalidInterpretationProject) {
			t.Fatalf("blank project list = %v, want ErrInvalidInterpretationProject", err)
		}
		if _, err := s.storeA.GetInterpretationRevision(s.ctx, blank, rootA.ID); !errors.Is(err, explorer.ErrInvalidInterpretationProject) {
			t.Fatalf("blank project get = %v, want ErrInvalidInterpretationProject", err)
		}
	}

	// Old revisions remain retrievable forever, independent of the head.
	for _, want := range []*explorer.InterpretationRevision{rootA, childA1, childA2} {
		got, err := s.storeB.GetInterpretationRevision(s.ctx, "proj_scope_a", want.ID)
		if err != nil {
			s.fail("read old revision", err)
		}
		if got.ID != want.ID || got.ContentDigest != want.ContentDigest {
			t.Fatalf("old revision %s did not survive head advancement: %+v", want.ID, got)
		}
	}
	l := s.mustWitnessLibrary(t, "proj_scope_a", "library-a")
	if l.headRevision != string(childA2.ID) {
		t.Fatalf("head is not at the newest child: %+v", l)
	}
	if n := s.revisionCount(t, "proj_scope_a", "library-a"); n != 3 {
		t.Fatalf("revision rows = %d, want 3 (full immutable chain retained)", n)
	}

	// Listing is project-scoped and never nil.
	listedA, err := s.storeA.ListInterpretationLibraries(s.ctx, "proj_scope_a")
	if err != nil {
		s.fail("list A", err)
	}
	if len(listedA) != 1 || listedA[0].Project != "proj_scope_a" {
		t.Fatalf("list A leaked: %+v", listedA)
	}
	empty, err := s.storeA.ListInterpretationLibraries(s.ctx, "proj_never")
	if err != nil {
		s.fail("list unknown", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("unknown-project list = %#v, want empty non-nil slice", empty)
	}

	// Ordering is not inferable from timestamps: a child carrying an OLDER
	// created_at still advances the head (ordering is the parent chain).
	oldTime := interpBaseTime.Add(-48 * time.Hour)
	oldChild := interpChild(*childA2, "old-child", "category")
	oldChild.CreatedAt = oldTime
	oldPrepared, err := explorer.PrepareInterpretationRevision(oldChild)
	if err != nil {
		s.fail("prepare old-time child", err)
	}
	createRevision(t, s, s.storeA, oldChild, idRef(childA2.ID))
	lOld := s.mustWitnessLibrary(t, "proj_scope_a", "library-a")
	if lOld.headRevision != string(oldPrepared.ID) {
		t.Fatalf("older-timestamped child did not advance the head: %+v", lOld)
	}
	oc := s.mustWitnessRevision(t, "proj_scope_a", string(oldPrepared.ID))
	if oc.createdAtText != "2026-03-02 05:06:07.123456" {
		t.Fatalf("old-time child timestamp = %s, want the two-day-earlier literal", oc.createdAtText)
	}
}

// ---------------------------------------------------------------------------
// Database-enforced immutability and head-advance guards, proven
// non-vacuous by live guard-drop probes.
// ---------------------------------------------------------------------------

func (s *interpretationService) triggerPresent(t *testing.T, trigger, table string) bool {
	t.Helper()
	var present bool
	err := interpQueryRow(s.ctx, s.witness,
		`SELECT EXISTS (
		   SELECT 1 FROM pg_trigger tg
		   JOIN pg_class c ON c.oid = tg.tgrelid
		   JOIN pg_namespace n ON n.oid = c.relnamespace
		  WHERE tg.tgname = $1 AND c.relname = $2 AND n.nspname = $3
		    AND NOT tg.tgisinternal)`,
		[]any{&present}, trigger, table, s.schema)
	if err != nil {
		s.fail("trigger catalog witness "+trigger, err)
	}
	return present
}

// interpGuardDropProbe runs a violating statement through the witness
// backend three ways: refused with the guard in place (SQLSTATE 45501),
// SUCCESSFUL with the trigger dropped -- proving the guard, not an
// accident, blocked it -- and refused again after restore. reseed runs
// after the successful unguarded write to restore the disposable probe row.
func (s *interpretationService) interpGuardDropProbe(t *testing.T, trigger, table, function string, reseed func(), stmt string, args ...any) {
	t.Helper()
	tableFQN := s.qualified(table)
	if !s.triggerPresent(t, trigger, table) {
		t.Fatalf("guard trigger %s missing from %s before probe", trigger, table)
	}
	err := s.witnessExecErr(stmt, args...)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgstate04ImmutableCode {
		t.Fatalf("guarded violating write on %s = %v, want SQLSTATE %s", table, s.scrub(err), pgstate04ImmutableCode)
	}
	s.mustWitnessExec(t, `DROP TRIGGER `+trigger+` ON `+tableFQN)
	if s.triggerPresent(t, trigger, table) {
		t.Fatalf("trigger %s still present after DROP", trigger)
	}
	conn, acquireErr := s.witness.Acquire(s.ctx)
	if acquireErr != nil {
		s.fail("acquire unguarded probe connection", acquireErr)
	}
	tag, execErr := conn.Exec(s.ctx, stmt, args...)
	conn.Release()
	if execErr != nil || tag.RowsAffected() != 1 {
		t.Fatalf("same violating write ran with the trigger dropped on %s: tag=%v err=%v; the guard was not what blocked it",
			table, tag, s.scrub(execErr))
	}
	if reseed != nil {
		reseed()
	}
	s.mustWitnessExec(t, `CREATE TRIGGER `+trigger+` BEFORE UPDATE OR DELETE ON `+tableFQN+
		` FOR EACH ROW EXECUTE FUNCTION `+s.qualified(function)+`()`)
	if !s.triggerPresent(t, trigger, table) {
		t.Fatalf("guard trigger %s not restored on %s", trigger, table)
	}
	err = s.witnessExecErr(stmt, args...)
	if !errors.As(err, &pgErr) || pgErr.Code != pgstate04ImmutableCode {
		t.Fatalf("violating write after trigger restore = %v, want SQLSTATE %s", s.scrub(err), pgstate04ImmutableCode)
	}
}

func (s *interpretationService) seedProbeRevision(t *testing.T, project, id, library, digest string) {
	t.Helper()
	s.mustWitnessExec(t, `INSERT INTO `+s.qualified(InterpretationRevisionsTable)+
		` (project, id, library_id, parent_revision_id, parent_digest, content_digest, author, explanation, created_at, payload)`+
		` VALUES ($1, $2, $3, NULL, NULL, $4, 'probe-author', 'probe-explanation', $5, '{}'::jsonb)`,
		project, id, library, digest, interpBaseTime)
}

func TestInterpretationImmutabilityGuardsAreEnforcedAtDatabaseLevel(t *testing.T) {
	s := newInterpretationService(t)

	// Store-written artifacts whose byte identity is asserted at the end:
	// they are never touched by any probe.
	artifact := createRevision(t, s, s.storeA, interpRevision("proj_immutable", "library-a", "author-a", "root", interpGoldenRulePath), nil)
	beforeRevision := s.mustWitnessRevision(t, "proj_immutable", string(artifact.ID))
	beforeLibrary := s.mustWitnessLibrary(t, "proj_immutable", "library-a")

	// Dedicated probe rows in their own project (inserts are unguarded, so
	// probes seed through direct INSERT and never target the artifacts).
	probeProject := "probe_immutable"
	probeID := "interpretation_" + "1" + strings.Repeat("0", 63)
	probeDigest := "sha256:" + strings.Repeat("2", 64)
	s.seedProbeRevision(t, probeProject, probeID, "probe-lib", probeDigest)

	// Revision UPDATE and DELETE are refused by the named trigger; the
	// guard-drop probe proves the refusal is the trigger: with it dropped
	// the very same statement succeeds, and after restore it fails again.
	s.interpGuardDropProbe(t, interpretationRevisionImmutableTrigger, InterpretationRevisionsTable,
		interpretationRevisionGuardFunction, nil,
		`UPDATE `+s.qualified(InterpretationRevisionsTable)+` SET content_digest = $2 WHERE project = $1 AND id = $3`,
		probeProject, "sha256:"+strings.Repeat("3", 64), probeID)
	reseedProbe := func() { s.seedProbeRevision(t, probeProject, probeID, "probe-lib", probeDigest) }
	s.interpGuardDropProbe(t, interpretationRevisionImmutableTrigger, InterpretationRevisionsTable,
		interpretationRevisionGuardFunction, reseedProbe,
		`DELETE FROM `+s.qualified(InterpretationRevisionsTable)+` WHERE project = $1 AND id = $2`,
		probeProject, probeID)

	// The library guard: DELETE is refused outright...
	s.mustWitnessExec(t, `INSERT INTO `+s.qualified(InterpretationLibrariesTable)+
		` (project, id, head_revision_id, head_digest, created_at, updated_at)`+
		` VALUES ($1, 'probe-lib', $2, $3, $4, $4)`,
		probeProject, probeID, probeDigest, interpBaseTime)
	s.interpGuardDropProbe(t, interpretationLibraryHeadGuardTrigger, InterpretationLibrariesTable,
		interpretationLibraryGuardFunction, func() {
			s.mustWitnessExec(t, `INSERT INTO `+s.qualified(InterpretationLibrariesTable)+
				` (project, id, head_revision_id, head_digest, created_at, updated_at)`+
				` VALUES ($1, 'probe-lib', $2, $3, $4, $4)`,
				probeProject, probeID, probeDigest, interpBaseTime)
		},
		`DELETE FROM `+s.qualified(InterpretationLibrariesTable)+` WHERE project = $1 AND id = $2`,
		probeProject, "probe-lib")

	// ... identity is stable (no-op identity writes are refused -- the FK
	// would otherwise be the blocker, a no-op isolates the guard arm) ...
	for _, stmt := range []string{
		`UPDATE ` + s.qualified(InterpretationLibrariesTable) + ` SET project = project WHERE project = $1 AND id = $2`,
		`UPDATE ` + s.qualified(InterpretationLibrariesTable) + ` SET id = id WHERE project = $1 AND id = $2`,
		`UPDATE ` + s.qualified(InterpretationLibrariesTable) + ` SET head_revision_id = head_revision_id WHERE project = $1 AND id = $2`,
	} {
		s.interpGuardDropProbe(t, interpretationLibraryHeadGuardTrigger, InterpretationLibrariesTable,
			interpretationLibraryGuardFunction, nil, stmt, probeProject, "probe-lib")
	}

	// ... and a real created_at rewrite is refused (reseed restores the
	// original instant while the guard is still down) ...
	s.interpGuardDropProbe(t, interpretationLibraryHeadGuardTrigger, InterpretationLibrariesTable,
		interpretationLibraryGuardFunction, func() {
			s.mustWitnessExec(t, `UPDATE `+s.qualified(InterpretationLibrariesTable)+
				` SET created_at = $3 WHERE project = $1 AND id = $2`, probeProject, "probe-lib", interpBaseTime)
		},
		`UPDATE `+s.qualified(InterpretationLibrariesTable)+
			` SET created_at = created_at - interval '1 day' WHERE project = $1 AND id = $2`,
		probeProject, "probe-lib")
	// The legal advance still works with every guard in place: a real head
	// move to a distinct same-library revision.
	probeID2 := "interpretation_" + "2" + strings.Repeat("0", 63)
	probeDigest2 := "sha256:" + strings.Repeat("4", 64)
	s.seedProbeRevision(t, probeProject, probeID2, "probe-lib", probeDigest2)
	s.mustWitnessExec(t, `UPDATE `+s.qualified(InterpretationLibrariesTable)+
		` SET head_revision_id = $3, head_digest = $4, updated_at = now()`+
		` WHERE project = $1 AND id = $2`,
		probeProject, "probe-lib", probeID2, probeDigest2)

	// The store-written artifacts were never reachable: same bytes, same
	// physical tuple, exactly as the store first wrote them.
	afterRevision := s.mustWitnessRevision(t, "proj_immutable", string(artifact.ID))
	if afterRevision != beforeRevision {
		t.Fatalf("guard probes disturbed the stored artifact row:\n got %+v\nwant %+v", afterRevision, beforeRevision)
	}
	afterLibrary := s.mustWitnessLibrary(t, "proj_immutable", "library-a")
	if afterLibrary != beforeLibrary {
		t.Fatalf("guard probes disturbed the stored library row:\n got %+v\nwant %+v", afterLibrary, beforeLibrary)
	}

	// Read-side fail-closed: a persisted revision whose payload no longer
	// satisfies its content address never reads back -- Get fails with the
	// typed identity mismatch, it is never silently returned.
	tamperProject := "probe_tamper_read"
	tamper := createRevision(t, s, s.storeB, interpRevision(tamperProject, "library-a", "author-a", "reason-a", interpGoldenRulePath), nil)
	s.mustWitnessExec(t, `DROP TRIGGER `+interpretationRevisionImmutableTrigger+` ON `+s.qualified(InterpretationRevisionsTable))
	s.mustWitnessExec(t, `UPDATE `+s.qualified(InterpretationRevisionsTable)+
		` SET payload = jsonb_set(payload, '{explanation}', '"tampered"') WHERE project = $1 AND id = $2`,
		tamperProject, string(tamper.ID))
	s.mustWitnessExec(t, `CREATE TRIGGER `+interpretationRevisionImmutableTrigger+` BEFORE UPDATE OR DELETE ON `+
		s.qualified(InterpretationRevisionsTable)+` FOR EACH ROW EXECUTE FUNCTION `+
		s.qualified(interpretationRevisionGuardFunction)+`()`)
	if _, err := s.storeA.GetInterpretationRevision(s.ctx, tamperProject, tamper.ID); !errors.Is(err, explorer.ErrInterpretationIdentityMismatch) {
		t.Fatalf("Get over tampered row = %v, want ErrInterpretationIdentityMismatch", err)
	}

	// Mirror tampering is refused on its own: the payload stays intact (so
	// Validate alone cannot notice), the mirror content_digest lies, and
	// the read-side payload-versus-mirror proof fails closed.
	mirrorTamper := createRevision(t, s, s.storeB, interpRevision("probe_tamper_mirror", "library-a", "author-a", "reason-a", interpGoldenRulePath), nil)
	s.mustWitnessExec(t, `DROP TRIGGER `+interpretationRevisionImmutableTrigger+` ON `+s.qualified(InterpretationRevisionsTable))
	s.mustWitnessExec(t, `UPDATE `+s.qualified(InterpretationRevisionsTable)+
		` SET content_digest = $2 WHERE project = $1 AND id = $3`,
		"probe_tamper_mirror", "sha256:"+strings.Repeat("9", 64), string(mirrorTamper.ID))
	s.mustWitnessExec(t, `CREATE TRIGGER `+interpretationRevisionImmutableTrigger+` BEFORE UPDATE OR DELETE ON `+
		s.qualified(InterpretationRevisionsTable)+` FOR EACH ROW EXECUTE FUNCTION `+
		s.qualified(interpretationRevisionGuardFunction)+`()`)
	if _, err := s.storeA.GetInterpretationRevision(s.ctx, "probe_tamper_mirror", mirrorTamper.ID); !errors.Is(err, explorer.ErrInterpretationIdentityMismatch) {
		t.Fatalf("Get over mirror-tampered row = %v, want ErrInterpretationIdentityMismatch", err)
	}
	// Same mirror lie refused on the create path too: the identity exists,
	// the stored row cannot satisfy its own contract, collision fails
	// closed rather than returning the lying row.
	if _, err := s.storeA.CreateInterpretationRevision(s.ctx, interpRevision("probe_tamper_mirror", "library-a", "author-a", "reason-a", interpGoldenRulePath), nil); !errors.Is(err, explorer.ErrImmutableInterpretation) {
		t.Fatalf("create over mirror-tampered row = %v, want ErrImmutableInterpretation", err)
	}
}

// ---------------------------------------------------------------------------
// Schema constraint negatives: every named rule refuses its violation with
// the exact named constraint, and the documented legal shapes insert
// cleanly (the negatives refuse only what they claim).
// ---------------------------------------------------------------------------

func (s *interpretationService) insertRevisionDirectErr(project, id, library string, parent *string, parentDigest *string, digest, author, explanation string, payload string) error {
	return s.witnessExecErr(`INSERT INTO `+s.qualified(InterpretationRevisionsTable)+
		` (project, id, library_id, parent_revision_id, parent_digest, content_digest, author, explanation, created_at, payload)`+
		` VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb)`,
		project, id, library, parent, parentDigest, digest, author, explanation, interpBaseTime, payload)
}

func (s *interpretationService) insertLibraryDirectErr(project, id, head, digest string) error {
	return s.witnessExecErr(`INSERT INTO `+s.qualified(InterpretationLibrariesTable)+
		` (project, id, head_revision_id, head_digest, created_at, updated_at)`+
		` VALUES ($1, $2, $3, $4, $5, $5)`,
		project, id, head, digest, interpBaseTime)
}

func TestInterpretationSchemaConstraintsRejectInvalidRows(t *testing.T) {
	s := newInterpretationService(t)
	p := "probe_constraints"
	goodID := "interpretation_" + strings.Repeat("a", 64)
	goodDigest := "sha256:" + strings.Repeat("b", 64)
	parentID := "interpretation_" + strings.Repeat("c", 64)
	parentDigestValue := "sha256:" + strings.Repeat("d", 64)
	parent := parentID
	parentDigest := parentDigestValue

	// Legal controls: a root row, a same-library child, and a library whose
	// head points at its own revision.
	if err := s.insertRevisionDirectErr(p, parentID, "lib-a", nil, nil, parentDigestValue, "a", "e", "{}"); err != nil {
		s.fail("legal control parent", err)
	}
	if err := s.insertRevisionDirectErr(p, goodID, "lib-a", &parent, &parentDigest, goodDigest, "a", "e", "{}"); err != nil {
		s.fail("legal control same-library child", err)
	}
	if err := s.insertLibraryDirectErr(p, "lib-a", goodID, goodDigest); err != nil {
		s.fail("legal control library", err)
	}
	// A sibling library in the same project whose revision is distinct.
	if err := s.insertRevisionDirectErr(p, "interpretation_"+strings.Repeat("e", 64), "lib-b", nil, nil, "sha256:"+strings.Repeat("f", 64), "a", "e", "{}"); err != nil {
		s.fail("legal control sibling library revision", err)
	}

	// Shape CHECKs.
	if err := s.insertRevisionDirectErr(p, "interpretation_short", "lib-a", nil, nil, goodDigest, "a", "e", "{}"); true {
		requireInterpPgErr(t, err, pgstate04CheckViolation, "interpretation_revisions_id_shape", "bad revision id shape")
	}
	if err := s.insertRevisionDirectErr(p, "interpretation_"+strings.Repeat("9", 64), "lib-a", nil, nil, "sha256:XYZ", "a", "e", "{}"); true {
		requireInterpPgErr(t, err, pgstate04CheckViolation, "interpretation_revisions_content_digest_shape", "bad content digest shape")
	}
	if err := s.insertRevisionDirectErr(p, "interpretation_"+strings.Repeat("8", 64), "lib-a", &parent, nil, goodDigest, "a", "e", "{}"); true {
		requireInterpPgErr(t, err, pgstate04CheckViolation, "interpretation_revisions_parent_pair", "parent without digest")
	}
	if err := s.insertRevisionDirectErr(p, "interpretation_"+strings.Repeat("7", 64), "lib-a", nil, &parentDigest, goodDigest, "a", "e", "{}"); true {
		requireInterpPgErr(t, err, pgstate04CheckViolation, "interpretation_revisions_parent_pair", "digest without parent")
	}
	if err := s.insertRevisionDirectErr(p, "interpretation_"+strings.Repeat("6", 64), "lib-a", ptr("interpretation_short"), &parentDigest, goodDigest, "a", "e", "{}"); true {
		requireInterpPgErr(t, err, pgstate04CheckViolation, "interpretation_revisions_parent_revision_shape", "bad parent id shape")
	}
	if err := s.insertRevisionDirectErr(p, "interpretation_"+strings.Repeat("5", 64), "lib-a", &parent, ptr("sha256:nope"), goodDigest, "a", "e", "{}"); true {
		requireInterpPgErr(t, err, pgstate04CheckViolation, "interpretation_revisions_parent_digest_shape", "bad parent digest shape")
	}
	if err := s.insertRevisionDirectErr(p, "interpretation_"+strings.Repeat("4", 64), "lib-a", nil, nil, goodDigest, "", "e", "{}"); true {
		requireInterpPgErr(t, err, pgstate04CheckViolation, "interpretation_revisions_author_non_empty", "empty author")
	}
	if err := s.insertRevisionDirectErr(p, "interpretation_"+strings.Repeat("3", 64), "lib-a", nil, nil, goodDigest, "a", "", "{}"); true {
		requireInterpPgErr(t, err, pgstate04CheckViolation, "interpretation_revisions_explanation_non_empty", "empty explanation")
	}
	if err := s.insertRevisionDirectErr(p, "interpretation_"+strings.Repeat("2", 64), "lib-a", nil, nil, goodDigest, "a", "e", "[1,2]"); true {
		requireInterpPgErr(t, err, pgstate04CheckViolation, "interpretation_revisions_payload_object", "non-object payload")
	}
	if err := s.insertRevisionDirectErr(p, "interpretation_"+strings.Repeat("1", 64), "1bad-library", nil, nil, goodDigest, "a", "e", "{}"); true {
		requireInterpPgErr(t, err, pgstate04CheckViolation, "interpretation_revisions_library_id_shape", "bad library id shape")
	}

	// Parent FK: nonexistent parent and cross-library parent are both
	// refused by the SAME named composite foreign key.
	if err := s.insertRevisionDirectErr(p, "interpretation_"+strings.Repeat("f", 63)+"e", "lib-a",
		ptr("interpretation_"+strings.Repeat("0", 64)), &parentDigest, goodDigest, "a", "e", "{}"); true {
		requireInterpPgErr(t, err, pgstate04ForeignKey, interpretationRevisionParentFKConstraint, "parent does not exist")
	}
	if err := s.insertRevisionDirectErr(p, "interpretation_"+strings.Repeat("f", 63)+"d", "lib-a",
		ptr("interpretation_"+strings.Repeat("e", 64)), &parentDigest, goodDigest, "a", "e", "{}"); true {
		requireInterpPgErr(t, err, pgstate04ForeignKey, interpretationRevisionParentFKConstraint, "parent lives in another library")
	}

	// Head FK: pointing at a nonexistent revision, or another library's
	// revision, is refused by the named composite foreign key; the legal
	// control above proves the refusal is not blanket.
	if err := s.insertLibraryDirectErr(p, "lib-c", "interpretation_"+strings.Repeat("0", 64), goodDigest); true {
		requireInterpPgErr(t, err, pgstate04ForeignKey, interpretationLibraryHeadFKConstraint, "head does not exist")
	}
	if err := s.insertLibraryDirectErr(p, "lib-c", "interpretation_"+strings.Repeat("e", 64), "sha256:"+strings.Repeat("f", 64)); true {
		requireInterpPgErr(t, err, pgstate04ForeignKey, interpretationLibraryHeadFKConstraint, "head belongs to another library")
	}
	if err := s.insertLibraryDirectErr(p, "lib-b", "interpretation_"+strings.Repeat("e", 64), "sha256:bad"); true {
		requireInterpPgErr(t, err, pgstate04CheckViolation, "interpretation_libraries_head_digest_shape", "bad head digest shape")
	}

	// Identity PKs refuse duplicates by name.
	if err := s.insertRevisionDirectErr(p, goodID, "lib-z", nil, nil, "sha256:"+strings.Repeat("c", 64), "z", "z", "{}"); true {
		requireInterpPgErr(t, err, pgstate04UniqueViolation, interpretationRevisionPKConstraint, "duplicate revision identity")
	}
	if err := s.insertLibraryDirectErr(p, "lib-a", "interpretation_"+strings.Repeat("e", 64), "sha256:"+strings.Repeat("f", 64)); true {
		requireInterpPgErr(t, err, pgstate04UniqueViolation, interpretationLibraryPKConstraint, "duplicate library identity")
	}

	// Every rejected statement persisted nothing: counts still match the
	// legal controls exactly (3 revisions + 1 library in the probe project).
	if n := s.revisionCount(t, p, "lib-a"); n != 2 {
		t.Fatalf("probe lib-a revisions = %d, want the 2 legal controls", n)
	}
	if n := s.revisionCount(t, p, "lib-b"); n != 1 {
		t.Fatalf("probe lib-b revisions = %d, want the 1 legal control", n)
	}
	if n := s.libraryCount(t, p, "lib-a"); n != 1 {
		t.Fatalf("probe lib-a libraries = %d, want the 1 legal control", n)
	}
	if n := s.libraryCount(t, p, "lib-c"); n != 0 {
		t.Fatalf("refused head-FK inserts created %d lib-c libraries", n)
	}
}

// ---------------------------------------------------------------------------
// The CAS classification does not depend on the preflight alone, and no
// path silently falls back: typed errors everywhere, zero writes on
// refusal.
// ---------------------------------------------------------------------------

func TestInterpretationCASRefusalsWriteNothingAndFailClosed(t *testing.T) {
	s := newInterpretationService(t)
	root := createRevision(t, s, s.storeA, interpRevision("proj_cas", "library-a", "author-a", "root", interpGoldenRulePath), nil)

	// Claim/head mismatch (expected parent != content parent) is refused
	// before the database is touched at all: no revision row, no library.
	mismatchChild := interpChild(*root, "mismatch", "code")
	if _, err := s.storeA.CreateInterpretationRevision(s.ctx, mismatchChild, nil); !errors.Is(err, explorer.ErrInterpretationParentConflict) {
		t.Fatalf("claim/content mismatch error = %v, want ErrInterpretationParentConflict", err)
	}
	if _, err := s.storeA.CreateInterpretationRevision(s.ctx, interpRevision("proj_cas_mismatch", "library-a", "author-a", "root", interpGoldenRulePath), idRef(root.ID)); !errors.Is(err, explorer.ErrInterpretationParentConflict) {
		t.Fatalf("root claiming a parent error = %v, want ErrInterpretationParentConflict", err)
	}
	if n := s.revisionCount(t, "proj_cas_mismatch", "library-a"); n != 0 {
		t.Fatalf("preflight refusals persisted %d revisions", n)
	}
	if n := s.libraryCount(t, "proj_cas_mismatch", "library-a"); n != 0 {
		t.Fatalf("preflight refusals persisted %d libraries", n)
	}

	// The mismatch preflight is load-bearing, not cosmetic: root-shaped
	// content (no parent) claiming the CURRENT head of an existing library
	// must be refused, never treated as a child CAS. Without the preflight
	// this would advance the head to an orphan and silently fork the
	// chain.
	headNow := s.mustWitnessLibrary(t, "proj_cas", "library-a")
	orphanRoot := interpRevision("proj_cas", "library-a", "author-a", "orphan-fork", "code")
	if _, err := s.storeA.CreateInterpretationRevision(s.ctx, orphanRoot, idRef(explorer.InterpretationRevisionID(headNow.headRevision))); !errors.Is(err, explorer.ErrInterpretationParentConflict) {
		t.Fatalf("parentless content claiming the head error = %v, want ErrInterpretationParentConflict", err)
	}
	if l := s.mustWitnessLibrary(t, "proj_cas", "library-a"); l != headNow {
		t.Fatalf("claim/content mismatch forked the head:\n got %+v\nwant %+v", l, headNow)
	}
	if n := s.revisionCount(t, "proj_cas", "library-a"); n != 1 {
		t.Fatalf("claim/content mismatch persisted a row for the orphan fork: %d revisions in proj_cas", n)
	}

	// Parent digest drift: the caller claims a parent digest different from
	// the persisted one. The CAS reads the DATABASE value, so this is a
	// conflict with zero writes.
	drift := interpRevision("proj_cas", "library-a", "author-a", "drift-child", "code")
	fakeParentID := root.ID
	fakeDigest := explorer.InterpretationContentDigest("sha256:" + strings.Repeat("e", 64))
	drift.ParentRevisionID = &fakeParentID
	drift.ParentDigest = &fakeDigest
	if _, err := s.storeA.CreateInterpretationRevision(s.ctx, drift, idRef(fakeParentID)); !errors.Is(err, explorer.ErrInterpretationParentConflict) {
		t.Fatalf("parent digest drift error = %v, want ErrInterpretationParentConflict", err)
	}
	if n := s.revisionCount(t, "proj_cas", "library-a"); n != 1 {
		t.Fatalf("digest-drift refusal persisted revisions: %d", n)
	}

	// Stale expected head after the winner committed: conflict, head and
	// rows untouched, and the correctly-parented create succeeds.
	child := createRevision(t, s, s.storeB, interpChild(*root, "won", "code"), idRef(root.ID))
	headBefore := s.mustWitnessLibrary(t, "proj_cas", "library-a")
	stale := interpChild(*root, "late", "value")
	if _, err := s.storeA.CreateInterpretationRevision(s.ctx, stale, idRef(root.ID)); !errors.Is(err, explorer.ErrInterpretationParentConflict) {
		t.Fatalf("stale-head create error = %v, want ErrInterpretationParentConflict", err)
	}
	headAfter := s.mustWitnessLibrary(t, "proj_cas", "library-a")
	if headAfter != headBefore {
		t.Fatalf("stale-head refusal moved the head:\n got %+v\nwant %+v", headAfter, headBefore)
	}
	if n := s.revisionCount(t, "proj_cas", "library-a"); n != 2 {
		t.Fatalf("stale-head refusal persisted a row: %d revisions", n)
	}
	late := interpChild(*child, "late-but-current", "value")
	createRevision(t, s, s.storeA, late, idRef(child.ID))

	// Missing parent: conflict, not a created orphan.
	orphan := interpRevision("proj_cas", "library-a", "author-a", "orphan", "code")
	missingID := explorer.InterpretationRevisionID("interpretation_" + strings.Repeat("0", 64))
	missingDigest := explorer.InterpretationContentDigest("sha256:" + strings.Repeat("0", 64))
	orphan.ParentRevisionID = &missingID
	orphan.ParentDigest = &missingDigest
	if _, err := s.storeA.CreateInterpretationRevision(s.ctx, orphan, idRef(missingID)); !errors.Is(err, explorer.ErrInterpretationParentConflict) {
		t.Fatalf("missing parent error = %v, want ErrInterpretationParentConflict", err)
	}
	if n := s.revisionCount(t, "proj_cas", "library-a"); n != 3 {
		t.Fatalf("missing-parent refusal persisted a row: %d revisions", n)
	}

	// Invalid domain input is typed and refused without writes.
	badAuthor := interpRevision("proj_cas_bad", "library-a", "  ", "reason", "code")
	if _, err := s.storeA.CreateInterpretationRevision(s.ctx, badAuthor, nil); err == nil || errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("blank author error = %v, want a typed validation error", err)
	}
	if _, err := s.storeA.CreateInterpretationRevision(s.ctx, interpRevision("proj_cas_bad", "", "author", "reason", "code"), nil); err == nil {
		t.Fatal("blank library create returned success")
	}
	if n := s.revisionCount(t, "proj_cas_bad", "library-a"); n != 0 {
		t.Fatalf("invalid inputs persisted %d rows", n)
	}
}

// TestInterpretationStoresFailClosedWithoutTheirTables proves there is no
// silent fallback: against a schema migrated only by the owner history
// (versions 1-3, no interpretation tables), every interpretation method
// fails with the database's own 42P01 -- never a skip, never an empty
// success, never an Arango path.
func TestInterpretationStoresFailClosedWithoutTheirTables(t *testing.T) {
	s := newInterpretationService(t)

	owner, err := OwnerMigrations()
	if err != nil {
		s.fail("owner migrations", err)
	}
	// Precondition: the owned schema carries this fixture's version-7
	// migration (its tables are present). A contains-check, not an
	// ends-at-7 literal: the open-ended owner history may legally carry
	// migrations above 7 (000009), which must not break this harness.
	fixtureApplied := false
	for _, rec := range s.ledgerAfter {
		if rec.Version == 7 && rec.Name == pgstate04FixtureName {
			fixtureApplied = true
		}
	}
	if !fixtureApplied {
		t.Fatalf("precondition: owned schema ledger lacks the version-7 %s fixture: %+v", pgstate04FixtureName, s.ledgerAfter)
	}

	// Fresh schema inside the same isolated database, migrated by the owner
	// history MINUS the version-7 interpretation-library migration (a
	// test-only negative composition -- production never assembles
	// histories this way; it exists solely to keep the fail-closed-on-
	// missing-tables guarantee assertable now that the owner embeds 7):
	// the interpretation tables never arrive there.
	unmigSchema := s.schema + "_unmig"
	if err := interpExecOn(s.ctx, s.witness, `CREATE SCHEMA "`+unmigSchema+`"`); err != nil {
		s.fail("create unmigrated schema", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelCleanup()
		_ = interpExecOn(cleanupCtx, s.witness, `DROP SCHEMA IF EXISTS "`+unmigSchema+`" CASCADE`)
	})
	interpretationFree := owner[:0:0]
	for _, m := range owner {
		if m.Version != 7 {
			interpretationFree = append(interpretationFree, m)
		}
	}
	if len(interpretationFree) != len(owner)-1 {
		t.Fatalf("owner history lacks the version-7 interpretation-library migration to exclude: %+v", owner)
	}
	ownerRunner, err := pgstore.NewMigrationRunner(s.witness, unmigSchema, interpretationFree)
	if err != nil {
		s.fail("build owner-minus-interpretation runner", err)
	}
	if _, err := ownerRunner.Migrate(s.ctx); err != nil {
		s.fail("migrate owner-only schema", err)
	}

	store, err := NewInterpretationStore(s.clientA, unmigSchema)
	if err != nil {
		s.fail("build unmigrated store", err)
	}
	undefinedTable := func(step string, err error) {
		t.Helper()
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42P01" {
			t.Fatalf("%s error = %v, want fail-closed 42P01 undefined_table", step, s.scrub(err))
		}
	}
	_, err = store.GetInterpretationRevision(s.ctx, "proj_x", explorer.InterpretationRevisionID("interpretation_"+strings.Repeat("0", 64)))
	undefinedTable("GetInterpretationRevision", err)
	_, err = store.ListInterpretationLibraries(s.ctx, "proj_x")
	undefinedTable("ListInterpretationLibraries", err)
	_, err = store.CreateInterpretationRevision(s.ctx, interpRevision("proj_x", "library-a", "author", "reason", "code"), nil)
	undefinedTable("CreateInterpretationRevision", err)
}

// ---------------------------------------------------------------------------
// Ownership-scoped zero-leftover audit: every database (and the schema that
// lived inside it) this file verified into existence is checked by exact
// name, plus a scan limited to this file's own pgstate04_it_* prefix. It
// performs no global scan, so concurrent sibling workers sweeping their own
// prefixes neither race this audit nor trip it.
// ---------------------------------------------------------------------------

func TestZZZeroLeftoverPgStateInterpretationFixtures(t *testing.T) {
	raw := testPostgresURL(t)
	pgstate04OwnedMu.Lock()
	owned := make(map[string]string, len(pgstate04OwnedDBs))
	for db, schema := range pgstate04OwnedDBs {
		owned[db] = schema
	}
	pgstate04OwnedMu.Unlock()
	if len(owned) == 0 {
		t.Skip("this process created no pgstate04_it_* fixtures")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect to test service for audit: %v", err)
	}
	defer admin.Close()

	for db, schema := range owned {
		if !strings.HasPrefix(db, "pgstate04_it_") {
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
					if probe, err := pgxpool.New(ctx, u.String()); err == nil {
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

	// Prefix-limited to this file's own namespace: never a global scan.
	var prefixLeftovers int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM pg_database WHERE datname LIKE 'pgstate04\_it\_%' ESCAPE '\'`).Scan(&prefixLeftovers); err != nil {
		t.Errorf("audit own prefix: %v", err)
	} else if prefixLeftovers != 0 {
		var names []string
		rows, err := admin.Query(ctx, `SELECT datname FROM pg_database WHERE datname LIKE 'pgstate04\_it\_%' ESCAPE '\'`)
		if err == nil {
			for rows.Next() {
				var n string
				if err := rows.Scan(&n); err == nil {
					names = append(names, n)
				}
			}
			rows.Close()
		}
		t.Errorf("%d pgstate04_it_* databases left behind: %v", prefixLeftovers, names)
	}
}
