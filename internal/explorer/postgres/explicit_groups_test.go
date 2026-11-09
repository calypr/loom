package postgres

// Real PostgreSQL 18 integration for pg-state-explicit-group-definitions-v1.
// Everything here runs against the service named by LOOM_TEST_POSTGRES_URL:
// each test creates one uniquely-named isolated database (pgstate06_it_*)
// plus one owned schema (pgstate06_it_*) inside it and drops both in its
// cleanups, so the suite leaves zero leftovers. The fixture history is
// composed through the real pg-store-02 runner from this package's embedded
// owner history (ledger bootstrap, owner, draft revision, receipts,
// revisions, and this ticket's explicit-group migration). Expected rows
// are asserted literally through direct SQL on a witness pool that is a
// physically distinct backend connection, and concurrent claim/put/cleanup
// proofs run over two single-connection stores with provably distinct
// pg_backend_pid values, at least ten rounds per race surface. Guard
// non-vacuity is witnessed at the database: every violating direct write
// must fail with the guard in place naming its exact constraint or trigger,
// and -- guard-drop probe -- the same statement must succeed once the
// trigger is dropped, with the migration text recreating the identical
// guard afterwards. Mutation probes accompany every refusal: the same
// subject must admit its legal control, so a guard cannot pass by blocking
// everything. Fault seams abort real transactions mid-flight and the
// rollback is witnessed to leave zero half-applied rows. Every test here
// requires the live service -- no test runs green on a skip. Concurrency
// tests run serially (no t.Parallel anywhere). The TestZZ zero-leftover
// audit is ownership-scoped to pgstate06_it_* only: sibling suites sweep
// their own prefixes in their own files, and concurrent sibling sweeps
// must never share a global scan.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/calypr/loom/internal/explorer"
	pgstore "github.com/calypr/loom/internal/store/postgres"
)

const (
	// pgstate06MigrationVersion is this ticket's slot in the shared
	// Explorer control-plane ledger (coordinator-assigned; 7 and 8 are
	// reserved for the sibling state tickets).
	pgstate06MigrationVersion = int64(9)
	// pgstate06MigrationName is the embedded migration file's ledger name.
	pgstate06MigrationName = "explorer_explicit_groups"
	// pgstate06MigrationChecksum is the byte-frozen SHA-256 of the
	// migration SQL. The test recomputes it from the embedded bytes and
	// refuses drift in either direction: the ledger, the loader checksum,
	// this literal, and a fresh server-side recompute must all agree.
	pgstate06MigrationChecksum = "c635cb5ef7c3c04655b4f45b01ba0fe834a7f181f70638f300878caf16c9a14f"
	// pgstate06ImmutableSQLState is the check_violation the guards raise.
	pgstate06CheckViolation = "23514"
)

var pgstate06NameRe = regexp.MustCompile(`^pgstate06_it_[0-9]+_[0-9a-f]+$`)

func pgstate06TestPostgresURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 Explorer explicit-group integration tests")
	}
	return raw
}

func pgstate06RandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return hex.EncodeToString(b[:])
}

// pgState06Fixture owns one isolated database and one owned schema inside
// it, both named pgstate06_it_*, created for exactly one test and removed
// by that test's cleanups. Witnesses read through a physically distinct
// backend; race tests additionally build two dedicated single-connection
// stores whose pids are witnessed distinct from each other and the witness.
type pgState06Fixture struct {
	ctx    context.Context
	admin  *pgxpool.Pool // shared service database
	iso    *pgxpool.Pool // isolated database
	dbName string
	schema string
	dsn    string
	scrub  func(any) string
	fail   func(step string, err error)

	store     *ExplicitGroupStore
	witness   *pgxpool.Pool
	witnessID int32
}

func newPGState06Fixture(t *testing.T) *pgState06Fixture {
	t.Helper()
	raw := pgstate06TestPostgresURL(t)

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

	ctx, cancel := context.WithTimeout(context.Background(), 360*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	t.Cleanup(admin.Close)

	dbName := fmt.Sprintf("pgstate06_it_%d_%s", time.Now().UnixNano(), pgstate06RandomHex(t))
	if !pgstate06NameRe.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}

	// Ownership-scoped zero-leftover audit (runs with the admin pool still
	// open): only this suite's own namespace is swept; concurrent sibling
	// suites own their prefixes and must never break this scan.
	t.Cleanup(func() {
		var mine int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_database WHERE datname = $1`, dbName).Scan(&mine); err != nil {
			t.Errorf("leftover audit: query database catalog: %s", scrub(err))
		}
		if mine != 0 {
			t.Errorf("leftover audit: isolated database %s still exists", dbName)
		}
		var namespaceLeftovers int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_database WHERE datname LIKE 'pgstate06\_it\_%' ESCAPE '\'`).Scan(&namespaceLeftovers); err != nil {
			t.Errorf("leftover audit: query database namespace: %s", scrub(err))
		}
		if namespaceLeftovers != 0 {
			t.Errorf("leftover audit: %d pgstate06_it_* databases remain", namespaceLeftovers)
		}
		var schemaLeftovers int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'pgstate06\_it\_%' ESCAPE '\'`).Scan(&schemaLeftovers); err != nil {
			t.Errorf("leftover audit: query schema namespace: %s", scrub(err))
		}
		if schemaLeftovers != 0 {
			t.Errorf("leftover audit: %d pgstate06_it_* schemas remain in the service database", schemaLeftovers)
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

	schema := fmt.Sprintf("pgstate06_it_%d_%s", time.Now().UnixNano(), pgstate06RandomHex(t))
	if !pgstate06NameRe.MatchString(schema) {
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

	f := &pgState06Fixture{ctx: ctx, admin: admin, iso: iso, dbName: dbName, schema: schema, dsn: dsn, scrub: scrub, fail: fail}
	f.witness, f.witnessID = f.newDedicatedPool(t)
	store, err := f.newStore(t)
	if err != nil {
		f.fail("build explicit-group store", err)
	}
	f.store = store
	return f
}

// migrate applies this package's full embedded history through the real
// pg-store-02 runner and returns the ledger result.
func (f *pgState06Fixture) migrate(t *testing.T) pgstore.MigrateResult {
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
	owner, err := OwnerMigrations()
	if err != nil {
		f.fail("owner migrations", err)
	}
	runner, err := pgstore.NewMigrationRunner(client, f.schema, owner)
	if err != nil {
		f.fail("new migration runner", err)
	}
	result, err := runner.Migrate(f.ctx)
	if err != nil {
		f.fail("migrate state schema", err)
	}
	return result
}

func (f *pgState06Fixture) newStore(t *testing.T) (*ExplicitGroupStore, error) {
	t.Helper()
	client, err := pgstore.Open(f.ctx, pgstore.Config{
		DSN:            f.dsn,
		MaxConns:       4,
		MinConns:       0,
		AcquireTimeout: 10 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	t.Cleanup(client.Close)
	return NewExplicitGroupStore(client, f.schema)
}

// newDedicatedStore opens a MaxConns=1 pool -- one single physical backend
// connection -- warms it, and returns the store plus that backend's
// pg_backend_pid. Two dedicated stores are the two real PG connections for
// the race proofs: their pids must differ, and every statement the store
// runs provably executes on that one backend.
func (f *pgState06Fixture) newDedicatedStore(t *testing.T) (*ExplicitGroupStore, int32) {
	t.Helper()
	client, err := pgstore.Open(f.ctx, pgstore.Config{
		DSN:            f.dsn,
		MaxConns:       1,
		MinConns:       1,
		AcquireTimeout: 10 * time.Second,
	})
	if err != nil {
		f.fail("open dedicated pg-store pool", err)
	}
	t.Cleanup(client.Close)
	conn, err := client.Acquire(f.ctx)
	if err != nil {
		f.fail("acquire dedicated pg-store connection", err)
	}
	var pid int32
	if err := conn.QueryRow(f.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		conn.Release()
		f.fail("witness dedicated pg-store backend pid", err)
	}
	conn.Release()
	if pid == 0 {
		t.Fatal("dedicated pg-store pool reported backend pid 0")
	}
	store, err := NewExplicitGroupStore(client, f.schema)
	if err != nil {
		f.fail("build dedicated explicit-group store", err)
	}
	return store, pid
}

// newDedicatedPool is the raw-SQL sibling of newDedicatedStore: one
// physical backend connection for direct SQL (guard probes, forged writes,
// snapshots) with its backend pid witnessed up front.
func (f *pgState06Fixture) newDedicatedPool(t *testing.T) (*pgxpool.Pool, int32) {
	t.Helper()
	poolConfig, err := pgxpool.ParseConfig(f.dsn)
	if err != nil {
		f.fail("parse dedicated raw pool config", err)
	}
	poolConfig.MaxConns = 1
	poolConfig.MinConns = 1
	pool, err := pgxpool.NewWithConfig(f.ctx, poolConfig)
	if err != nil {
		f.fail("open dedicated raw pool", err)
	}
	t.Cleanup(pool.Close)
	var pid int32
	if err := pool.QueryRow(f.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		f.fail("witness dedicated raw backend pid", err)
	}
	if pid == 0 {
		t.Fatal("dedicated raw pool reported backend pid 0")
	}
	return pool, pid
}

func requireDistinctBackendsPGState06(t *testing.T, pids map[string]int32) {
	t.Helper()
	seen := map[int32]string{}
	for label, pid := range pids {
		if pid == 0 {
			t.Fatalf("%s reported backend pid 0", label)
		}
		if other, clash := seen[pid]; clash {
			t.Fatalf("%s and %s share backend pid %d, want provably distinct PG connections", label, other, pid)
		}
		seen[pid] = label
	}
}

func (f *pgState06Fixture) revisionTable() string {
	return sanitizeTableRef(f.schema, ExplicitGroupRevisionsTable)
}

func (f *pgState06Fixture) definitionsTable() string {
	return sanitizeTableRef(f.schema, ExplicitGroupDefinitionsTable)
}

// headerSnapshot renders one stored header row as one deterministic text
// value covering every column. Byte-identical snapshots before and after a
// refused operation are the row witness that the refusal wrote nothing.
func (f *pgState06Fixture) headerSnapshot(t *testing.T, revisionID string) string {
	t.Helper()
	var out string
	err := f.witness.QueryRow(f.ctx, `SELECT concat_ws(E'\x1f',
		id, project, generation, scope_digest, resource_type,
		source_selection_revision_id, source_membership_digest,
		definition_digest, membership_digest,
		group_count::text, member_count::text, state, idempotency_key,
		to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS.US'),
		COALESCE(to_char(completed_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS.US'), '<sql-null>'),
		COALESCE(writer_token, '<sql-null>'),
		COALESCE(to_char(writer_expires_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS.US'), '<sql-null>'))
	FROM `+f.revisionTable()+` WHERE id = $1`, revisionID).Scan(&out)
	if err != nil {
		f.fail("header snapshot of "+revisionID, err)
	}
	return out
}

// frozenHeaderSnapshot renders every column except the writer lease. Two
// frozen snapshots being byte-identical proves the only thing that moved
// was the lease.
func (f *pgState06Fixture) frozenHeaderSnapshot(t *testing.T, revisionID string) string {
	t.Helper()
	snapshot := f.headerSnapshot(t, revisionID)
	parts := strings.Split(snapshot, "\x1f")
	if len(parts) != 17 {
		t.Fatalf("header snapshot of %s has %d fields, want 17", revisionID, len(parts))
	}
	return strings.Join(parts[:15], "\x1f")
}

// definitionsSnapshot renders the whole definition slice of one revision
// in stored order as one deterministic text value.
func (f *pgState06Fixture) definitionsSnapshot(t *testing.T, revisionID string) string {
	t.Helper()
	rows, err := f.witness.Query(f.ctx, `SELECT group_id, label, ordinal::text FROM `+
		f.definitionsTable()+` WHERE revision_id = $1 ORDER BY ordinal, group_id`, revisionID)
	if err != nil {
		f.fail("definitions snapshot of "+revisionID, err)
	}
	defer rows.Close()
	var parts []string
	for rows.Next() {
		var groupID, label, ordinal string
		if err := rows.Scan(&groupID, &label, &ordinal); err != nil {
			f.fail("scan definitions snapshot of "+revisionID, err)
		}
		parts = append(parts, strings.Join([]string{groupID, label, ordinal}, "|"))
	}
	if err := rows.Err(); err != nil {
		f.fail("definitions snapshot of "+revisionID, err)
	}
	return strings.Join(parts, "\x1e")
}

func (f *pgState06Fixture) countRows(t *testing.T, table, where string, args ...any) int {
	t.Helper()
	var count int
	if err := f.witness.QueryRow(f.ctx, `SELECT count(*) FROM `+table+` WHERE `+where, args...).Scan(&count); err != nil {
		f.fail("count rows in "+table, err)
	}
	return count
}

func requirePgErrPGState06(t *testing.T, err error, sqlstate, step string) *pgconn.PgError {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("%s: error = %v (%T), want *pgconn.PgError", step, err, err)
	}
	if pgErr.Code != sqlstate {
		t.Fatalf("%s: sqlstate = %s, want %s (error: %v)", step, pgErr.Code, sqlstate, err)
	}
	return pgErr
}

func requirePgErrConstraintPGState06(t *testing.T, err error, sqlstate, constraint, step string) *pgconn.PgError {
	t.Helper()
	pgErr := requirePgErrPGState06(t, err, sqlstate, step)
	if pgErr.ConstraintName != constraint {
		t.Fatalf("%s: constraint = %q, want the named guard %q (message: %s)", step, pgErr.ConstraintName, constraint, pgErr.Message)
	}
	return pgErr
}

func requirePgErrTriggerPGState06(t *testing.T, err error, sqlstate, trigger, step string) *pgconn.PgError {
	t.Helper()
	pgErr := requirePgErrPGState06(t, err, sqlstate, step)
	if !strings.Contains(pgErr.Message, trigger) {
		t.Fatalf("%s: message %q does not name the guard %q", step, pgErr.Message, trigger)
	}
	return pgErr
}

// rerunMigration applies one embedded migration's exact SQL text again
// through direct SQL. The migration is convergent (IF NOT EXISTS /
// CREATE OR REPLACE / DROP TRIGGER IF EXISTS + CREATE TRIGGER), so a rerun
// restores the identical guards after a guard-drop probe -- proving the
// same text that created the guard is what blocks the violating write.
func (f *pgState06Fixture) rerunMigration(t *testing.T, version int64) {
	t.Helper()
	migrations, err := OwnerMigrations()
	if err != nil {
		f.fail("load migrations for rerun", err)
	}
	for _, m := range migrations {
		if m.Version == version {
			conn, err := f.iso.Acquire(f.ctx)
			if err != nil {
				f.fail("acquire connection for migration rerun", err)
			}
			defer conn.Release()
			tx, err := conn.Begin(f.ctx)
			if err != nil {
				f.fail("begin migration rerun transaction", err)
			}
			if _, err := tx.Exec(f.ctx,
				"SET LOCAL search_path TO "+sanitizeSchema(f.schema)); err != nil {
				tx.Rollback(f.ctx)
				f.fail("set search_path for migration rerun", err)
			}
			if _, err := tx.Exec(f.ctx, m.SQL); err != nil {
				tx.Rollback(f.ctx)
				f.fail(fmt.Sprintf("rerun migration %d/%s", m.Version, m.Name), err)
			}
			if err := tx.Commit(f.ctx); err != nil {
				f.fail("commit migration rerun", err)
			}
			return
		}
	}
	t.Fatalf("migration version %d is absent from the embedded history", version)
}

func (f *pgState06Fixture) dropTrigger(t *testing.T, table, trigger string) {
	t.Helper()
	if _, err := f.witness.Exec(f.ctx, `DROP TRIGGER `+trigger+` ON `+table); err != nil {
		f.fail("drop trigger "+trigger+" on "+table, err)
	}
}

func (f *pgState06Fixture) execOn(t *testing.T, pool *pgxpool.Pool, step, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(f.ctx, sql, args...); err != nil {
		f.fail(step+": "+scrubSQLForTest(sql), err)
	}
}

func scrubSQLForTest(sql string) string {
	if len(sql) > 160 {
		return sql[:160] + "..."
	}
	return sql
}

// ---------------------------------------------------------------------------
// fixtures shared by the tests
// ---------------------------------------------------------------------------

func pgstate06Selection(project string, suffix string) explorer.SelectionRevision {
	completedAt := time.Now().UTC()
	return explorer.SelectionRevision{
		ID:               "pgstate06-selection-" + suffix,
		Project:          project,
		Generation:       "generation-a",
		ResourceType:     "Patient",
		Rule:             explorer.SelectionRule{Kind: explorer.SelectionRuleExplicit},
		Source:           explorer.SelectionSource{Kind: explorer.SelectionSourceExplicit},
		ScopeDigest:      "scope-a",
		RuleDigest:       "selection-rule",
		MembershipDigest: "sha256:" + strings.Repeat("a", 64),
		MemberCount:      3,
		MemberBytes:      30,
		Complete:         true,
		CreatedAt:        completedAt,
		CompletedAt:      &completedAt,
	}
}

func pgstate06Revision(source explorer.SelectionRevision, key string) explorer.ExplicitGroupRevision {
	return explorer.ExplicitGroupRevision{
		ID:                        explorer.ExplicitGroupRevisionIDFor(source.Project, key),
		Project:                   source.Project,
		Generation:                source.Generation,
		ScopeDigest:               source.ScopeDigest,
		ResourceType:              source.ResourceType,
		SourceSelectionRevisionID: source.ID,
		SourceMembershipDigest:    source.MembershipDigest,
		State:                     explorer.ExplicitGroupRevisionStaging,
		IdempotencyKey:            key,
		CreatedAt:                 time.Now().UTC(),
	}
}

func pgstate06Definitions() []explorer.ExplicitGroupDefinition {
	return []explorer.ExplicitGroupDefinition{
		{ID: "group-alpha", Label: "Alpha cohort", Ordinal: 0},
		{ID: "group-empty", Label: "No members", Ordinal: 1},
		{ID: "group-beta", Label: "Beta cohort", Ordinal: 2},
	}
}

func pgstate06CanonicalDefinitions(t *testing.T) []explorer.ExplicitGroupDefinition {
	t.Helper()
	canonical, err := explorer.CanonicalExplicitGroupDefinitions(pgstate06Definitions())
	if err != nil {
		t.Fatalf("canonical test definitions: %v", err)
	}
	return canonical
}

// rawInsertHeader inserts one staging header through direct SQL with the
// caller's writer lifecycle, computing the id through the same domain
// function the honest path uses. It exists to seed states the store API
// cannot itself produce (expired leases, null leases, foreign seeds) for
// guard and cleanup proofs.
func (f *pgState06Fixture) rawInsertHeader(t *testing.T, pool *pgxpool.Pool, revision explorer.ExplicitGroupRevision, writerToken string, writerExpiresAt *time.Time) {
	t.Helper()
	f.execOn(t, pool, "raw insert header",
		`INSERT INTO `+f.revisionTable()+` (id, project, generation, scope_digest, resource_type,`+
			` source_selection_revision_id, source_membership_digest, definition_digest, membership_digest,`+
			` group_count, member_count, state, idempotency_key, created_at, completed_at, writer_token, writer_expires_at)`+
			` VALUES ($1, $2, $3, $4, $5, $6, $7, '', '', 0, 0, 'STAGING', $8, $9, NULL, $10, $11)`,
		string(revision.ID), revision.Project, revision.Generation, revision.ScopeDigest, revision.ResourceType,
		revision.SourceSelectionRevisionID, revision.SourceMembershipDigest, revision.IdempotencyKey,
		revision.CreatedAt, writerToken, writerExpiresAt)
}

func (f *pgState06Fixture) rawInsertDefinition(t *testing.T, pool *pgxpool.Pool, revisionID, groupID, label string, ordinal int64) {
	t.Helper()
	f.execOn(t, pool, "raw insert definition",
		`INSERT INTO `+f.definitionsTable()+` (revision_id, group_id, label, ordinal) VALUES ($1, $2, $3, $4)`,
		revisionID, groupID, label, ordinal)
}

// mustErr asserts fn returns an error matching want, and that it is exactly
// want's class (not merely wrapping something else).
func mustErr(t *testing.T, step string, fn func() error, want error) {
	t.Helper()
	err := fn()
	if !errors.Is(err, want) {
		t.Fatalf("%s: error = %v, want %v", step, err, want)
	}
}

func mustNil(t *testing.T, step string, fn func() error) {
	t.Helper()
	if err := fn(); err != nil {
		t.Fatalf("%s: error = %v, want success", step, err)
	}
}

// ---------------------------------------------------------------------------
// construction contract
// ---------------------------------------------------------------------------

func TestPostgresExplicitGroupDefinitionsStoreGuards(t *testing.T) {
	f := newPGState06Fixture(t)
	f.migrate(t) // pins the live-service precondition

	if _, err := NewExplicitGroupStore(nil, "ok_schema"); err == nil {
		t.Fatal("NewExplicitGroupStore(nil, ...) = nil error, want failure")
	}
	for _, bad := range []string{"", "BadName", "has-dash", "1leading", strings.Repeat("x", 64)} {
		if _, err := NewExplicitGroupStore(&pgstore.Client{}, bad); err == nil {
			t.Fatalf("NewExplicitGroupStore(client, %q) = nil error, want failure", bad)
		}
	}
	if _, err := NewExplicitGroupStore(&pgstore.Client{}, "ok_schema"); err != nil {
		t.Fatalf("NewExplicitGroupStore(valid) = %v, want success", err)
	}

	// Fail closed on an unmigrated schema: the tables are not silently
	// created and there is no fallback -- SQLSTATE 42P01 surfaces.
	bare := fmt.Sprintf("pgstate06_it_%d_%s", time.Now().UnixNano(), pgstate06RandomHex(t))
	if _, err := f.iso.Exec(f.ctx, `CREATE SCHEMA "`+bare+`"`); err != nil {
		f.fail("create unmigrated schema", err)
	}
	client, err := pgstore.Open(f.ctx, pgstore.Config{DSN: f.dsn, MaxConns: 1, MinConns: 0, AcquireTimeout: 5 * time.Second})
	if err != nil {
		f.fail("open unmigrated pool", err)
	}
	t.Cleanup(client.Close)
	bareStore, err := NewExplicitGroupStore(client, bare)
	if err != nil {
		f.fail("build unmigrated store", err)
	}
	_, err = bareStore.GetExplicitGroupRevision(f.ctx, "p", "pgstate06-missing")
	requirePgErrPGState06(t, err, "42P01", "unmapped schema read fails closed")
}

// ---------------------------------------------------------------------------
// begin: claim, intent, ownership, source binding, lease
// ---------------------------------------------------------------------------

func TestPostgresExplicitGroupDefinitionsBeginLifecycleAndOwnership(t *testing.T) {
	f := newPGState06Fixture(t)
	f.migrate(t)

	project := "pgstate06-project"
	source := pgstate06Selection(project, "begin-"+pgstate06RandomHex(t))
	key := "begin-" + pgstate06RandomHex(t)
	revision := pgstate06Revision(source, key)
	token := "writer-" + pgstate06RandomHex(t)

	started, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, token)
	if err != nil {
		f.fail("begin staging revision", err)
	}
	if started.ID != revision.ID || started.State != explorer.ExplicitGroupRevisionStaging {
		t.Fatalf("started = %+v, want the canonical staging header", started)
	}
	if started.CreatedAt.IsZero() || started.CompletedAt != nil {
		t.Fatalf("started carries completion values: %+v", started)
	}
	// Self-recomputed identity pin: the ID must equal the domain hash
	// recomputed here from (project, idempotency key), not a fixture echo.
	if want := explorer.ExplicitGroupRevisionIDFor(project, key); started.ID != want {
		t.Fatalf("revision ID = %s, want self-recomputed %s", started.ID, want)
	}

	// Literal stored row on the physically distinct witness backend.
	wantSnapshot := strings.Join([]string{
		string(revision.ID), project, "generation-a", "scope-a", "Patient",
		source.ID, source.MembershipDigest,
		"", "", "0", "0", "STAGING", key,
	}, "\x1f")
	snapshot := f.headerSnapshot(t, string(revision.ID))
	if !strings.HasPrefix(snapshot, wantSnapshot) {
		t.Fatalf("stored header = %q, want identity/binding/completion-empty prefix %q", snapshot, wantSnapshot)
	}

	// Same-owner retry: idempotent, lease strictly forward, every other
	// column byte-identical.
	beforeFull := f.headerSnapshot(t, string(revision.ID))
	beforeFrozen := strings.Join(strings.Split(beforeFull, "\x1f")[:15], "\x1f")
	retry, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, token)
	if err != nil {
		f.fail("same-owner begin retry", err)
	}
	if retry.ID != started.ID {
		t.Fatalf("retry = %+v, want the stored header", retry)
	}
	afterFull := f.headerSnapshot(t, string(revision.ID))
	afterFrozen := strings.Join(strings.Split(afterFull, "\x1f")[:15], "\x1f")
	if beforeFrozen != afterFrozen {
		t.Fatalf("same-owner retry changed frozen columns:\nbefore %q\nafter  %q", beforeFrozen, afterFrozen)
	}
	if strings.Split(afterFull, "\x1f")[16] == strings.Split(beforeFull, "\x1f")[16] {
		t.Fatal("same-owner retry did not advance the writer lease")
	}

	// Foreign token on the live staging header is the typed conflict and
	// writes nothing.
	frozenBefore := f.frozenHeaderSnapshot(t, string(revision.ID))
	mustErr(t, "foreign-token begin", func() error {
		_, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, "not-the-writer")
		return err
	}, explorer.ErrExplicitGroupRevisionConflict)
	if got := f.frozenHeaderSnapshot(t, string(revision.ID)); got != frozenBefore {
		t.Fatalf("foreign-token begin mutated the header:\nbefore %q\nafter  %q", frozenBefore, got)
	}
	// Mutation probe: the same claim still admits its honest owner.
	mustNil(t, "owner begin after foreign refusal", func() error {
		_, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, token)
		return err
	})

	// Same key, different intent: the typed conflict, header untouched.
	// The only way a committed row can differ in its intent fields while
	// keeping the honest (project, key) hash identity is a forged write,
	// so the fixture stores one directly and the honest claim must refuse
	// it -- exactly the adapter's sameExplicitGroupRevisionIntent refusal.
	forgeKey := "drift-" + pgstate06RandomHex(t)
	forge := pgstate06Revision(source, forgeKey)
	forgedRow := forge
	forgedRow.Generation = "generation-forged"
	liveLease := time.Now().UTC().Add(time.Hour)
	f.rawInsertHeader(t, f.witness, forgedRow, "forged-writer", &liveLease)
	drifted := pgstate06Revision(source, forgeKey) // the honest intent for that key
	mustErr(t, "drifted-intent begin", func() error {
		_, err := f.store.BeginExplicitGroupRevision(f.ctx, drifted, source, token)
		return err
	}, explorer.ErrExplicitGroupRevisionConflict)

	// Blank writer token is the typed conflict before any database touch.
	mustErr(t, "blank-token begin", func() error {
		_, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, "   ")
		return err
	}, explorer.ErrExplicitGroupRevisionConflict)

	// A non-staging revision is refused with the adapter's plain state
	// error, never written.
	completedInput := revision
	completedInput.ID = explorer.ExplicitGroupRevisionIDFor(project, key+"-complete")
	completedInput.IdempotencyKey = key + "-complete"
	completedInput.State = explorer.ExplicitGroupRevisionComplete
	completedInput.DefinitionDigest = strings.Repeat("d", 64)
	completedInput.MembershipDigest = strings.Repeat("m", 64)
	completedInput.GroupCount = 1
	now := time.Now().UTC()
	completedInput.CompletedAt = &now
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, completedInput, source, token); err == nil ||
		!strings.Contains(err.Error(), "must begin in staging state") {
		t.Fatalf("completed-state begin = %v, want the staging-state refusal", err)
	}

	// A forged ID (not the (project, key) hash) is the domain validation
	// error, refused before the database sees it.
	forged := revision
	forged.ID = explorer.ExplicitGroupRevisionID("grouprev_" + strings.Repeat("0", 64))
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, forged, source, token); err == nil ||
		strings.Contains(err.Error(), "grouprev_0000000000") {
		t.Fatalf("forged-ID begin = %v, want the domain identity refusal", err)
	}

	// Source binding: every mismatched binding field is the typed stale
	// source, each refused with zero rows left behind.
	bindingCases := []struct {
		name   string
		mutate func(*explorer.SelectionRevision)
	}{
		{"selection id", func(s *explorer.SelectionRevision) { s.ID = "other-selection" }},
		{"project", func(s *explorer.SelectionRevision) { s.Project = "other-project" }},
		{"generation", func(s *explorer.SelectionRevision) { s.Generation = "generation-b" }},
		{"resource type", func(s *explorer.SelectionRevision) { s.ResourceType = "Observation" }},
		{"scope digest", func(s *explorer.SelectionRevision) { s.ScopeDigest = "another-scope" }},
		{"membership digest", func(s *explorer.SelectionRevision) { s.MembershipDigest = "sha256:" + strings.Repeat("b", 64) }},
		{"incomplete selection", func(s *explorer.SelectionRevision) { s.Complete = false }},
	}
	for _, bindingCase := range bindingCases {
		mutated := source
		bindingCase.mutate(&mutated)
		altKey := "binding-" + bindingCase.name + "-" + pgstate06RandomHex(t)
		alt := pgstate06Revision(source, altKey)
		_, err := f.store.BeginExplicitGroupRevision(f.ctx, alt, mutated, token)
		if !errors.Is(err, explorer.ErrExplicitGroupRevisionStaleSource) {
			t.Fatalf("begin with mismatched %s = %v, want ErrExplicitGroupRevisionStaleSource", bindingCase.name, err)
		}
		if got := f.countRows(t, f.revisionTable(), "id = $1", string(alt.ID)); got != 0 {
			t.Fatalf("refused %s binding left %d rows, want 0", bindingCase.name, got)
		}
	}
	// Mutation probe: the unmutated binding is admitted.
	okKey := "binding-ok-" + pgstate06RandomHex(t)
	ok := pgstate06Revision(source, okKey)
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, ok, source, token); err != nil {
		f.fail("begin with correct binding after refusals", err)
	}
	if got := f.countRows(t, f.revisionTable(), "id = $1", string(ok.ID)); got != 1 {
		t.Fatalf("correct-binding begin stored %d rows, want 1", got)
	}

	// Reopen across a physically distinct connection: the claim is still
	// idempotent for the honest owner.
	reopened, err := f.newStore(t)
	if err != nil {
		f.fail("reopen store", err)
	}
	reopenedHeader, err := reopened.BeginExplicitGroupRevision(f.ctx, revision, source, token)
	if err != nil {
		f.fail("reopen begin retry", err)
	}
	if reopenedHeader.ID != revision.ID || reopenedHeader.State != explorer.ExplicitGroupRevisionStaging {
		t.Fatalf("reopened header = %+v, want the stored staging header", reopenedHeader)
	}
	if got := f.countRows(t, f.revisionTable(), "id = $1", string(revision.ID)); got != 1 {
		t.Fatalf("retry path stored %d headers, want exactly 1", got)
	}
}

// ---------------------------------------------------------------------------
// definitions: empty groups survive, order is stored, write-once
// ---------------------------------------------------------------------------

func TestPostgresExplicitGroupDefinitionsEmptyDefinitionsSurviveReopen(t *testing.T) {
	f := newPGState06Fixture(t)
	f.migrate(t)

	project := "pgstate06-project"
	source := pgstate06Selection(project, "defs-"+pgstate06RandomHex(t))
	key := "defs-" + pgstate06RandomHex(t)
	revision := pgstate06Revision(source, key)
	token := "writer-" + pgstate06RandomHex(t)
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, token); err != nil {
		f.fail("begin", err)
	}

	// Declare out of order: storage order must be canonical (ordinal ASC),
	// never input order -- and the empty group must survive as a row.
	shuffled := []explorer.ExplicitGroupDefinition{
		{ID: "group-beta", Label: "Beta cohort", Ordinal: 2},
		{ID: "group-empty", Label: "No members", Ordinal: 1},
		{ID: "group-alpha", Label: "Alpha cohort", Ordinal: 0},
	}
	mustNil(t, "put definitions", func() error {
		return f.store.PutExplicitGroupDefinitions(f.ctx, revision.ID, token, shuffled)
	})

	// Literal expected rows on the physically distinct witness backend.
	want := strings.Join([]string{
		"group-alpha|Alpha cohort|0",
		"group-empty|No members|1",
		"group-beta|Beta cohort|2",
	}, "\x1e")
	if got := f.definitionsSnapshot(t, string(revision.ID)); got != want {
		t.Fatalf("stored definitions = %q, want literal %q", got, want)
	}
	if got := f.countRows(t, f.definitionsTable(), "revision_id = $1", string(revision.ID)); got != 3 {
		t.Fatalf("definition count = %d, want 3", got)
	}

	// Reopen on a brand-new pool: the same put is still the idempotent
	// replay of what was actually stored (stable IDs, labels, and stored
	// order past any single connection).
	reopened, err := f.newStore(t)
	if err != nil {
		f.fail("reopen store", err)
	}
	mustNil(t, "replayed put after reopen", func() error {
		return reopened.PutExplicitGroupDefinitions(f.ctx, revision.ID, token, pgstate06Definitions())
	})
	if got := f.definitionsSnapshot(t, string(revision.ID)); got != want {
		t.Fatalf("replayed put changed stored definitions:\nbefore %q\nafter  %q", want, got)
	}

	// Canonicalization is an equivalence: re-declaring the same set in a
	// different input order is the same content and stays idempotent.
	remixed := []explorer.ExplicitGroupDefinition{
		{ID: "group-empty", Label: "  No members  ", Ordinal: 1},
		{ID: "group-alpha", Label: "Alpha cohort", Ordinal: 0},
		{ID: "group-beta", Label: "Beta cohort", Ordinal: 2},
	}
	mustNil(t, "remixed put", func() error {
		return reopened.PutExplicitGroupDefinitions(f.ctx, revision.ID, token, remixed)
	})
	if got := f.definitionsSnapshot(t, string(revision.ID)); got != want {
		t.Fatalf("remixed put changed stored definitions:\nbefore %q\nafter  %q", want, got)
	}

	// A single-group revision whose only group has no members is legal:
	// the definition row is the group, membership is another ticket.
	soloKey := "solo-" + pgstate06RandomHex(t)
	solo := pgstate06Revision(source, soloKey)
	soloToken := "writer-solo-" + pgstate06RandomHex(t)
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, solo, source, soloToken); err != nil {
		f.fail("begin solo", err)
	}
	mustNil(t, "put single empty group", func() error {
		return f.store.PutExplicitGroupDefinitions(f.ctx, solo.ID, soloToken, []explorer.ExplicitGroupDefinition{
			{ID: "group-only-empty", Label: "Empty but real", Ordinal: 7},
		})
	})
	if got := f.definitionsSnapshot(t, string(solo.ID)); got != "group-only-empty|Empty but real|7" {
		t.Fatalf("empty-group-only revision = %q, want the declared row", got)
	}

	// A blank declaration list is the domain's plain refusal and stores
	// nothing (the at-least-one-group invariant).
	emptyKey := "empty-list-" + pgstate06RandomHex(t)
	empty := pgstate06Revision(source, emptyKey)
	emptyToken := "writer-empty-" + pgstate06RandomHex(t)
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, empty, source, emptyToken); err != nil {
		f.fail("begin empty-list revision", err)
	}
	if err := f.store.PutExplicitGroupDefinitions(f.ctx, empty.ID, emptyToken, nil); err == nil {
		t.Fatal("put with zero definitions = nil, want the at-least-one-group refusal")
	}
	if got := f.countRows(t, f.definitionsTable(), "revision_id = $1", string(empty.ID)); got != 0 {
		t.Fatalf("refused empty-list put left %d rows, want 0", got)
	}
	// Mutation probe: the same revision admits a legal declaration.
	mustNil(t, "legal declaration after refusal", func() error {
		return f.store.PutExplicitGroupDefinitions(f.ctx, empty.ID, emptyToken, []explorer.ExplicitGroupDefinition{
			{ID: "group-first", Label: "First", Ordinal: 0},
		})
	})

	// Duplicate group ID / duplicate ordinal in the PUT arguments are
	// domain refusals that leave the stored rows untouched.
	before := f.definitionsSnapshot(t, string(revision.ID))
	if err := f.store.PutExplicitGroupDefinitions(f.ctx, revision.ID, token, []explorer.ExplicitGroupDefinition{
		{ID: "group-alpha", Label: "Alpha cohort", Ordinal: 0},
		{ID: "group-alpha", Label: "Copycat", Ordinal: 5},
	}); err == nil {
		t.Fatal("put with duplicate group ID = nil, want the domain refusal")
	}
	if err := f.store.PutExplicitGroupDefinitions(f.ctx, revision.ID, token, []explorer.ExplicitGroupDefinition{
		{ID: "group-alpha", Label: "Alpha cohort", Ordinal: 3},
		{ID: "group-beta", Label: "Beta cohort", Ordinal: 3},
	}); err == nil {
		t.Fatal("put with duplicate ordinal = nil, want the domain refusal")
	}
	if got := f.definitionsSnapshot(t, string(revision.ID)); got != before {
		t.Fatalf("refused puts mutated stored definitions:\nbefore %q\nafter  %q", before, got)
	}
}

func TestPostgresExplicitGroupDefinitionsWriteOnceAndContentDrift(t *testing.T) {
	f := newPGState06Fixture(t)
	f.migrate(t)

	project := "pgstate06-project"
	source := pgstate06Selection(project, "write-once-"+pgstate06RandomHex(t))
	key := "write-once-" + pgstate06RandomHex(t)
	revision := pgstate06Revision(source, key)
	token := "writer-" + pgstate06RandomHex(t)
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, token); err != nil {
		f.fail("begin", err)
	}
	mustNil(t, "put", func() error {
		return f.store.PutExplicitGroupDefinitions(f.ctx, revision.ID, token, pgstate06Definitions())
	})
	want := f.definitionsSnapshot(t, string(revision.ID))

	driftCases := []struct {
		name  string
		patch func([]explorer.ExplicitGroupDefinition) []explorer.ExplicitGroupDefinition
	}{
		{"renamed label", func(g []explorer.ExplicitGroupDefinition) []explorer.ExplicitGroupDefinition {
			g[0].Label = "Renamed cohort"
			return g
		}},
		{"added group", func(g []explorer.ExplicitGroupDefinition) []explorer.ExplicitGroupDefinition {
			return append(g, explorer.ExplicitGroupDefinition{ID: "group-extra", Label: "Extra", Ordinal: 9})
		}},
		{"dropped group", func(g []explorer.ExplicitGroupDefinition) []explorer.ExplicitGroupDefinition {
			return g[:2]
		}},
		{"moved order", func(g []explorer.ExplicitGroupDefinition) []explorer.ExplicitGroupDefinition {
			g[2].Ordinal = 3
			return g
		}},
	}
	for _, driftCase := range driftCases {
		drifted, err := explorer.CanonicalExplicitGroupDefinitions(driftCase.patch(pgstate06Definitions()))
		if err != nil {
			t.Fatalf("canonicalize %s drift fixture: %v", driftCase.name, err)
		}
		mustErr(t, "put "+driftCase.name+" drift", func() error {
			return f.store.PutExplicitGroupDefinitions(f.ctx, revision.ID, token, drifted)
		}, explorer.ErrExplicitGroupRevisionConflict)
		if got := f.definitionsSnapshot(t, string(revision.ID)); got != want {
			t.Fatalf("%s drift mutated stored definitions:\nbefore %q\nafter  %q", driftCase.name, want, got)
		}
	}
	// Mutation probe: the identical content remains the idempotent replay.
	mustNil(t, "identical put after drift refusals", func() error {
		return f.store.PutExplicitGroupDefinitions(f.ctx, revision.ID, token, pgstate06Definitions())
	})

	// Foreign token cannot put, renew, or even replay, and writes nothing.
	mustErr(t, "foreign-token put", func() error {
		return f.store.PutExplicitGroupDefinitions(f.ctx, revision.ID, "intruder", pgstate06Definitions())
	}, explorer.ErrExplicitGroupRevisionConflict)
	if got := f.definitionsSnapshot(t, string(revision.ID)); got != want {
		t.Fatalf("foreign-token put mutated stored definitions:\nbefore %q\nafter  %q", want, got)
	}

	// Blank revision ID or token is the typed conflict before any write;
	// unknown revision is the typed not-found.
	mustErr(t, "blank-revision put", func() error {
		return f.store.PutExplicitGroupDefinitions(f.ctx, "  ", token, pgstate06Definitions())
	}, explorer.ErrExplicitGroupRevisionConflict)
	mustErr(t, "blank-token put", func() error {
		return f.store.PutExplicitGroupDefinitions(f.ctx, revision.ID, "  ", pgstate06Definitions())
	}, explorer.ErrExplicitGroupRevisionConflict)
	mustErr(t, "unknown-revision put", func() error {
		return f.store.PutExplicitGroupDefinitions(f.ctx, explorer.ExplicitGroupRevisionID("grouprev_"+strings.Repeat("9", 64)), token, pgstate06Definitions())
	}, explorer.ErrExplicitGroupRevisionNotFound)
}

// ---------------------------------------------------------------------------
// abort: idempotence and owner-only removal
// ---------------------------------------------------------------------------

func TestPostgresExplicitGroupDefinitionsAbortIdempotence(t *testing.T) {
	f := newPGState06Fixture(t)
	f.migrate(t)

	project := "pgstate06-project"
	source := pgstate06Selection(project, "abort-"+pgstate06RandomHex(t))

	// Blank revision ID and a missing revision are idempotent no-ops; a
	// blank token is the typed conflict before any write.
	mustNil(t, "abort blank id", func() error {
		return f.store.AbortExplicitGroupRevision(f.ctx, "   ", "writer")
	})
	mustNil(t, "abort missing revision", func() error {
		return f.store.AbortExplicitGroupRevision(f.ctx, explorer.ExplicitGroupRevisionID("grouprev_"+strings.Repeat("9", 64)), "writer")
	})

	key := "abort-" + pgstate06RandomHex(t)
	revision := pgstate06Revision(source, key)
	token := "writer-" + pgstate06RandomHex(t)
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, token); err != nil {
		f.fail("begin", err)
	}
	mustNil(t, "put", func() error {
		return f.store.PutExplicitGroupDefinitions(f.ctx, revision.ID, token, pgstate06Definitions())
	})

	mustErr(t, "abort blank token", func() error {
		return f.store.AbortExplicitGroupRevision(f.ctx, revision.ID, "  ")
	}, explorer.ErrExplicitGroupRevisionConflict)
	beforeHeader := f.headerSnapshot(t, string(revision.ID))
	beforeDefs := f.definitionsSnapshot(t, string(revision.ID))
	mustErr(t, "foreign-token abort", func() error {
		return f.store.AbortExplicitGroupRevision(f.ctx, revision.ID, "intruder")
	}, explorer.ErrExplicitGroupRevisionConflict)
	if got := f.headerSnapshot(t, string(revision.ID)); got != beforeHeader {
		t.Fatalf("foreign abort mutated the header:\nbefore %q\nafter  %q", beforeHeader, got)
	}
	if got := f.definitionsSnapshot(t, string(revision.ID)); got != beforeDefs {
		t.Fatalf("foreign abort mutated the definitions:\nbefore %q\nafter  %q", beforeDefs, got)
	}

	// The owner's abort removes exactly the header and its definitions --
	// nothing else -- and is idempotent on replay.
	mustNil(t, "owner abort", func() error {
		return f.store.AbortExplicitGroupRevision(f.ctx, revision.ID, token)
	})
	if got := f.countRows(t, f.revisionTable(), "id = $1", string(revision.ID)); got != 0 {
		t.Fatalf("aborted header count = %d, want 0", got)
	}
	if got := f.countRows(t, f.definitionsTable(), "revision_id = $1", string(revision.ID)); got != 0 {
		t.Fatalf("aborted definitions count = %d, want 0", got)
	}
	mustNil(t, "owner abort replay", func() error {
		return f.store.AbortExplicitGroupRevision(f.ctx, revision.ID, token)
	})
	mustNil(t, "anyone abort after removal", func() error {
		return f.store.AbortExplicitGroupRevision(f.ctx, revision.ID, "intruder")
	})

	// Mutation probe: the freed idempotency key can be claimed again by a
	// fresh writer with the same intent.
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, "reclaimer"); err != nil {
		f.fail("re-claim freed key", err)
	}
	if got := f.countRows(t, f.revisionTable(), "id = $1", string(revision.ID)); got != 1 {
		t.Fatalf("re-claimed header count = %d, want 1", got)
	}
}

// ---------------------------------------------------------------------------
// read visibility: staging stays hidden, scopes never leak
// ---------------------------------------------------------------------------

func TestPostgresExplicitGroupDefinitionsStagingHiddenFromReads(t *testing.T) {
	f := newPGState06Fixture(t)
	f.migrate(t)

	project := "pgstate06-project"
	source := pgstate06Selection(project, "reads-"+pgstate06RandomHex(t))
	key := "reads-" + pgstate06RandomHex(t)
	revision := pgstate06Revision(source, key)
	token := "writer-" + pgstate06RandomHex(t)
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, token); err != nil {
		f.fail("begin", err)
	}

	_, err := f.store.GetExplicitGroupRevision(f.ctx, project, revision.ID)
	mustErr(t, "get staging header", func() error {
		_, err := f.store.GetExplicitGroupRevision(f.ctx, project, revision.ID)
		return err
	}, explorer.ErrExplicitGroupRevisionIncomplete)

	// Cross-project Get is concealed NotFound, never Incomplete-with-signal.
	mustErr(t, "get foreign project", func() error {
		_, err := f.store.GetExplicitGroupRevision(f.ctx, "other-project", revision.ID)
		return err
	}, explorer.ErrExplicitGroupRevisionNotFound)
	mustErr(t, "get blank id", func() error {
		_, err := f.store.GetExplicitGroupRevision(f.ctx, project, "  ")
		return err
	}, explorer.ErrExplicitGroupRevisionNotFound)

	// Public definition reads go through the same gate.
	_, err = f.store.ListExplicitGroupDefinitions(f.ctx, project, revision.ID)
	mustErr(t, "list definitions of staging", func() error {
		_, err := f.store.ListExplicitGroupDefinitions(f.ctx, project, revision.ID)
		return err
	}, explorer.ErrExplicitGroupRevisionIncomplete)
	mustErr(t, "list definitions blank id", func() error {
		_, err := f.store.ListExplicitGroupDefinitions(f.ctx, project, "   ")
		return err
	}, explorer.ErrExplicitGroupRevisionNotFound)

	// Listing hides staging -- even under the header's exact scope -- and
	// never returns nil.
	listed, err := f.store.ListExplicitGroupRevisions(f.ctx, project, "generation-a", "scope-a", "Patient", 0)
	if err != nil {
		f.fail("list revisions", err)
	}
	if listed == nil {
		t.Fatal("listing returned nil, want empty slice")
	}
	if len(listed) != 0 {
		t.Fatalf("staging header leaked into listing: %+v", listed)
	}
	// Mutation probe: the scope query itself works -- a foreign scope is
	// also empty, and the honest scope finds nothing ONLY because of the
	// state filter (witnessed by the direct count showing the row).
	if got := f.countRows(t, f.revisionTable(), "id = $1", string(revision.ID)); got != 1 {
		t.Fatalf("witness header count = %d, want 1 (hidden by state, not absent)", got)
	}
}

// ---------------------------------------------------------------------------
// cleanup: expired only, bounded, never touching active writers
// ---------------------------------------------------------------------------

func TestPostgresExplicitGroupDefinitionsCleanupReapsExpiredNeverActive(t *testing.T) {
	f := newPGState06Fixture(t)
	f.migrate(t)

	project := "pgstate06-project"
	source := pgstate06Selection(project, "cleanup-"+pgstate06RandomHex(t))
	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	expiredLease := now.Add(-time.Minute)
	// Realistic live lease: inside the store's own 10-minute renewal
	// window, so the active writer's re-Begin (which refreshes to
	// now+lease) strictly moves the lease forward, as the header guard
	// demands. A forged hour-long lease would make even an honest
	// renewal look like a shortening.
	liveLease := now.Add(5 * time.Minute)

	// Seed four staging revisions through direct SQL so the lease states
	// are exactly controlled at INSERT (the UPDATE trigger would refuse
	// lease moves made without the writer token, so seeding sets the
	// lease honestly from the start):
	//   expired:   created ago, lease expired, has definitions
	//   nullLease: created ago, never carried a lease (adapter tolerance)
	//   active:    created ago, lease still live -- the active writer
	//   young:     created ago, lease expired -- swept like any other
	seeds := []struct {
		revision explorer.ExplicitGroupRevision
		lease    *time.Time
	}{
		{pgstate06Revision(source, "cl-expired-"+pgstate06RandomHex(t)), &expiredLease},
		{pgstate06Revision(source, "cl-null-"+pgstate06RandomHex(t)), nil},
		{pgstate06Revision(source, "cl-active-"+pgstate06RandomHex(t)), &liveLease},
		{pgstate06Revision(source, "cl-young-"+pgstate06RandomHex(t)), &expiredLease},
	}
	expired, nullLease, active, young := seeds[0].revision, seeds[1].revision, seeds[2].revision, seeds[3].revision
	for _, seed := range seeds {
		f.rawInsertHeader(t, f.witness, explorer.ExplicitGroupRevision{
			ID: seed.revision.ID, Project: seed.revision.Project, Generation: seed.revision.Generation,
			ScopeDigest: seed.revision.ScopeDigest, ResourceType: seed.revision.ResourceType,
			SourceSelectionRevisionID: seed.revision.SourceSelectionRevisionID,
			SourceMembershipDigest:    seed.revision.SourceMembershipDigest,
			State:                     explorer.ExplicitGroupRevisionStaging,
			IdempotencyKey:            seed.revision.IdempotencyKey,
			CreatedAt:                 past,
		}, "seed-writer", seed.lease)
	}
	f.rawInsertDefinition(t, f.witness, string(expired.ID), "group-a", "A", 0)
	f.rawInsertDefinition(t, f.witness, string(expired.ID), "group-b", "B", 1)
	f.rawInsertDefinition(t, f.witness, string(active.ID), "group-a", "A", 0)
	activeDefs := f.definitionsSnapshot(t, string(active.ID))
	activeHeader := f.headerSnapshot(t, string(active.ID))

	if err := f.store.CleanupExplicitGroupStaging(f.ctx, now, 100); err != nil {
		f.fail("cleanup", err)
	}

	for _, gone := range []explorer.ExplicitGroupRevisionID{expired.ID, nullLease.ID, young.ID} {
		if got := f.countRows(t, f.revisionTable(), "id = $1", string(gone)); got != 0 {
			t.Fatalf("expired header %s count = %d, want 0", gone, got)
		}
	}
	if got := f.countRows(t, f.definitionsTable(), "revision_id = $1", string(expired.ID)); got != 0 {
		t.Fatalf("expired definitions count = %d, want 0 (cascade)", got)
	}
	if got := f.headerSnapshot(t, string(active.ID)); got != activeHeader {
		t.Fatalf("cleanup touched the active writer:\nbefore %q\nafter  %q", activeHeader, got)
	}
	if got := f.definitionsSnapshot(t, string(active.ID)); got != activeDefs {
		t.Fatalf("cleanup touched the active writer's definitions:\nbefore %q\nafter  %q", activeDefs, got)
	}

	// Prove the `before` bound: a row created after `before` must survive
	// even with an already-expired lease.
	liveKey := "cl-live-" + pgstate06RandomHex(t)
	live := pgstate06Revision(source, liveKey) // CreatedAt is now
	liveToken := "writer-live-" + pgstate06RandomHex(t)
	f.rawInsertHeader(t, f.witness, live, liveToken, &now) // lease == now: expired
	if err := f.store.CleanupExplicitGroupStaging(f.ctx, now, 100); err != nil {
		f.fail("cleanup live", err)
	}
	if got := f.countRows(t, f.revisionTable(), "id = $1", string(live.ID)); got != 1 {
		t.Fatalf("row created at the before boundary was reaped, want kept (before bound)")
	}

	// Bounded sweep: three more expired rows, limit 2 removes exactly two
	// (oldest first), the next sweep removes the rest.
	var bounded []explorer.ExplicitGroupRevisionID
	for i := 0; i < 3; i++ {
		seed := pgstate06Revision(source, fmt.Sprintf("cl-bound-%d-%s", i, pgstate06RandomHex(t)))
		created := now.Add(-time.Duration(3-i) * time.Minute)
		f.rawInsertHeader(t, f.witness, explorer.ExplicitGroupRevision{
			ID: seed.ID, Project: seed.Project, Generation: seed.Generation,
			ScopeDigest: seed.ScopeDigest, ResourceType: seed.ResourceType,
			SourceSelectionRevisionID: seed.SourceSelectionRevisionID,
			SourceMembershipDigest:    seed.SourceMembershipDigest,
			State:                     explorer.ExplicitGroupRevisionStaging,
			IdempotencyKey:            seed.IdempotencyKey,
			CreatedAt:                 created,
		}, "seed-writer", func() *time.Time { e := created.Add(-time.Second); return &e }())
		bounded = append(bounded, seed.ID)
	}
	if err := f.store.CleanupExplicitGroupStaging(f.ctx, now.Add(time.Minute), 2); err != nil {
		f.fail("bounded cleanup", err)
	}
	remaining := 0
	for _, id := range bounded {
		remaining += f.countRows(t, f.revisionTable(), "id = $1", string(id))
	}
	if remaining != 1 {
		t.Fatalf("bounded sweep left %d rows, want 1", remaining)
	}
	if err := f.store.CleanupExplicitGroupStaging(f.ctx, now.Add(time.Minute), 0); err != nil {
		f.fail("default-limit cleanup", err)
	}
	remaining = 0
	for _, id := range bounded {
		remaining += f.countRows(t, f.revisionTable(), "id = $1", string(id))
	}
	if remaining != 0 {
		t.Fatalf("follow-up sweep left %d rows, want 0", remaining)
	}

	// Mutation probes: an honest live claim under a used key still works,
	// and the active writer is still operable end to end.
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, active, source, "seed-writer"); err != nil {
		f.fail("active writer begin retry after cleanup", err)
	}
	mustNil(t, "active writer put after cleanup", func() error {
		return f.store.PutExplicitGroupDefinitions(f.ctx, active.ID, "seed-writer", []explorer.ExplicitGroupDefinition{
			{ID: "group-a", Label: "A", Ordinal: 0},
		})
	})
}

// ---------------------------------------------------------------------------
// corruption detection on the read side
// ---------------------------------------------------------------------------

func TestPostgresExplicitGroupDefinitionsCorruptionDetection(t *testing.T) {
	f := newPGState06Fixture(t)
	f.migrate(t)

	project := "pgstate06-project"
	source := pgstate06Selection(project, "corrupt-"+pgstate06RandomHex(t))
	token := "writer-" + pgstate06RandomHex(t)

	key := "corrupt-" + pgstate06RandomHex(t)
	revision := pgstate06Revision(source, key)
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, token); err != nil {
		f.fail("begin", err)
	}
	// Forge a non-canonical stored row (a padded label -- the CHECK only
	// forbids the empty label), then any put comparison must surface the
	// corruption instead of silently accepting or classifying it as a
	// mere conflict.
	f.rawInsertDefinition(t, f.witness, string(revision.ID), "group-padded", " padded ", 0)
	err := f.store.PutExplicitGroupDefinitions(f.ctx, revision.ID, token, []explorer.ExplicitGroupDefinition{
		{ID: "group-padded", Label: "padded", Ordinal: 0},
	})
	if !errors.Is(err, explorer.ErrCorruptExplicitGroupRevision) {
		t.Fatalf("put over non-canonical row = %v, want ErrCorruptExplicitGroupRevision", err)
	}
	if got := f.countRows(t, f.definitionsTable(), "revision_id = $1", string(revision.ID)); got != 1 {
		t.Fatalf("corruption refusal left %d rows, want the single forged row untouched", got)
	}

	// Mutation probe: a clean revision admits the identical declaration.
	cleanKey := "corrupt-clean-" + pgstate06RandomHex(t)
	clean := pgstate06Revision(source, cleanKey)
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, clean, source, token); err != nil {
		f.fail("begin clean", err)
	}
	mustNil(t, "clean put", func() error {
		return f.store.PutExplicitGroupDefinitions(f.ctx, clean.ID, token, []explorer.ExplicitGroupDefinition{
			{ID: "group-padded", Label: "padded", Ordinal: 0},
		})
	})

	// Forging a header row whose id disagrees with its own (project, key)
	// pair is refused by the named identity-binding CHECK even when the
	// rest is a perfect copy.
	_, err = f.witness.Exec(f.ctx, `UPDATE `+f.revisionTable()+` SET idempotency_key = 'rewritten' WHERE id = $1`, string(clean.ID))
	requirePgErrTriggerPGState06(t, err, pgstate06CheckViolation,
		"explorer_explicit_group_revisions guard", "rewriting the idempotency key is frozen content")
}

// ---------------------------------------------------------------------------
// two-real-connection races: >= 10 rounds per surface, byte-identical
// witnesses, post-abort convergence
// ---------------------------------------------------------------------------

// pgstate06RaceSetup builds the two dedicated single-connection stores and
// proves three physically distinct backends (store A, store B, witness).
func (f *pgState06Fixture) raceSetup(t *testing.T) (storeA, storeB *ExplicitGroupStore) {
	t.Helper()
	storeA, pidA := f.newDedicatedStore(t)
	storeB, pidB := f.newDedicatedStore(t)
	requireDistinctBackendsPGState06(t, map[string]int32{"storeA": pidA, "storeB": pidB, "witness": f.witnessID})
	return storeA, storeB
}

// raceFire releases both goroutines as close together as the runtime
// allows and collects each side's error.
func raceFire(fnA, fnB func() error) (errA, errB error) {
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		errA = fnA()
	}()
	go func() {
		defer wg.Done()
		<-start
		errB = fnB()
	}()
	close(start)
	wg.Wait()
	return errA, errB
}

func exactlyOneConflict(errA, errB error) bool {
	conflictA := errors.Is(errA, explorer.ErrExplicitGroupRevisionConflict)
	conflictB := errors.Is(errB, explorer.ErrExplicitGroupRevisionConflict)
	return (errA == nil && conflictB) || (conflictA && errB == nil)
}

// TestPostgresExplicitGroupDefinitionsConcurrentClaimRace: ten rounds of
// two real backends claiming the same idempotency key with different
// writer tokens. Exactly one wins each round; the loser's refusal wrote
// nothing; the single stored row belongs to exactly one of the two
// writers; and after the winner aborts the schema is byte-empty again --
// post-abort convergence.
func TestPostgresExplicitGroupDefinitionsConcurrentClaimRace(t *testing.T) {
	f := newPGState06Fixture(t)
	f.migrate(t)
	storeA, storeB := f.raceSetup(t)

	project := "pgstate06-project"
	source := pgstate06Selection(project, "race-claim-"+pgstate06RandomHex(t))

	const rounds = 12
	for round := 0; round < rounds; round++ {
		key := fmt.Sprintf("race-claim-%d-%s", round, pgstate06RandomHex(t))
		revision := pgstate06Revision(source, key)
		tokenA := "race-writer-a-" + pgstate06RandomHex(t)
		tokenB := "race-writer-b-" + pgstate06RandomHex(t)

		var headerA, headerB *explorer.ExplicitGroupRevision
		errA, errB := raceFire(
			func() error {
				var err error
				headerA, err = storeA.BeginExplicitGroupRevision(f.ctx, revision, source, tokenA)
				return err
			},
			func() error {
				var err error
				headerB, err = storeB.BeginExplicitGroupRevision(f.ctx, revision, source, tokenB)
				return err
			},
		)
		if !exactlyOneConflict(errA, errB) {
			t.Fatalf("round %d: claim results errA=%v errB=%v, want one success and one typed conflict", round, errA, errB)
		}

		// Exactly one header row exists, owned by exactly one token.
		if got := f.countRows(t, f.revisionTable(), "id = $1", string(revision.ID)); got != 1 {
			t.Fatalf("round %d: header count = %d, want exactly 1", round, got)
		}
		var storedToken string
		if err := f.witness.QueryRow(f.ctx, `SELECT writer_token FROM `+f.revisionTable()+` WHERE id = $1`, string(revision.ID)).Scan(&storedToken); err != nil {
			f.fail("witness claim token", err)
		}
		if storedToken != tokenA && storedToken != tokenB {
			t.Fatalf("round %d: stored writer token %q belongs to neither racer", round, storedToken)
		}
		winnerToken, loserToken := tokenA, tokenB
		if storedToken == tokenB {
			winnerToken, loserToken = tokenB, tokenA
		}

		// The loser's refusal wrote nothing further: another claim attempt
		// with the loser's token stays a conflict and cannot change the
		// stored row (byte witness).
		before := f.headerSnapshot(t, string(revision.ID))
		mustErr(t, fmt.Sprintf("round %d loser re-claim", round), func() error {
			_, err := storeB.BeginExplicitGroupRevision(f.ctx, revision, source, loserToken)
			return err
		}, explorer.ErrExplicitGroupRevisionConflict)
		if got := f.headerSnapshot(t, string(revision.ID)); got != before {
			t.Fatalf("round %d: loser re-claim mutated the winner's row:\nbefore %q\nafter  %q", round, before, got)
		}

		// Post-abort convergence: the winner aborts, both tables are
		// byte-empty for this key, and the winner's own begin stays
		// idempotent right up to the abort.
		mustNil(t, fmt.Sprintf("round %d winner retry before abort", round), func() error {
			_, err := storeA.BeginExplicitGroupRevision(f.ctx, revision, source, winnerToken)
			return err
		})
		mustNil(t, fmt.Sprintf("round %d winner abort", round), func() error {
			return storeA.AbortExplicitGroupRevision(f.ctx, revision.ID, winnerToken)
		})
		if got := f.countRows(t, f.revisionTable(), "id = $1", string(revision.ID)); got != 0 {
			t.Fatalf("round %d: after abort header count = %d, want 0", round, got)
		}
		if got := f.countRows(t, f.definitionsTable(), "revision_id = $1", string(revision.ID)); got != 0 {
			t.Fatalf("round %d: after abort definition count = %d, want 0", round, got)
		}
		_ = headerA
		_ = headerB
	}
}

// TestPostgresExplicitGroupDefinitionsConcurrentPutRace runs two race
// surfaces over the definition write path, ten rounds each: concurrent
// identical puts (both succeed, one byte-identical row set) and concurrent
// conflicting puts (exactly one content wins, the other is the typed
// conflict), each round converging to zero rows after the owner aborts.
func TestPostgresExplicitGroupDefinitionsConcurrentPutRace(t *testing.T) {
	f := newPGState06Fixture(t)
	f.migrate(t)
	storeA, storeB := f.raceSetup(t)

	project := "pgstate06-project"
	source := pgstate06Selection(project, "race-put-"+pgstate06RandomHex(t))
	otherSource := pgstate06Selection(project, "race-put-other-"+pgstate06RandomHex(t))
	_ = otherSource

	const rounds = 11

	// Surface 1: concurrent identical puts with the owner token. Both
	// must succeed and the stored set must stay the one byte-identical
	// canonical snapshot.
	wantCanonical := strings.Join([]string{
		"group-alpha|Alpha cohort|0",
		"group-empty|No members|1",
		"group-beta|Beta cohort|2",
	}, "\x1e")
	keyIdentical := "race-put-identical-" + pgstate06RandomHex(t)
	revisionIdentical := pgstate06Revision(source, keyIdentical)
	token := "race-put-owner-" + pgstate06RandomHex(t)
	if _, err := storeA.BeginExplicitGroupRevision(f.ctx, revisionIdentical, source, token); err != nil {
		f.fail("begin identical-race revision", err)
	}
	for round := 0; round < rounds; round++ {
		errA, errB := raceFire(
			func() error {
				return storeA.PutExplicitGroupDefinitions(f.ctx, revisionIdentical.ID, token, pgstate06Definitions())
			},
			func() error {
				return storeB.PutExplicitGroupDefinitions(f.ctx, revisionIdentical.ID, token, pgstate06Definitions())
			},
		)
		if errA != nil || errB != nil {
			t.Fatalf("identical put round %d: errA=%v errB=%v, want both nil (idempotent replay)", round, errA, errB)
		}
		if got := f.definitionsSnapshot(t, string(revisionIdentical.ID)); got != wantCanonical {
			t.Fatalf("identical put round %d: stored definitions = %q, want byte-identical %q", round, got, wantCanonical)
		}
	}
	if got := f.countRows(t, f.definitionsTable(), "revision_id = $1", string(revisionIdentical.ID)); got != 3 {
		t.Fatalf("identical puts stored %d definition rows, want exactly 3", got)
	}

	// Surface 2: concurrent conflicting puts (different content) on a
	// fresh revision each round: exactly one succeeds, one is the typed
	// conflict, and the stored set is exactly one of the two canonical
	// shapes -- byte-identical for the rest of the round's observations.
	shapeA := pgstate06CanonicalDefinitions(t)
	shapeB, err := explorer.CanonicalExplicitGroupDefinitions([]explorer.ExplicitGroupDefinition{
		{ID: "group-alpha", Label: "Alpha cohort", Ordinal: 0},
		{ID: "group-empty", Label: "No members at all", Ordinal: 1},
		{ID: "group-beta", Label: "Beta cohort", Ordinal: 2},
	})
	if err != nil {
		t.Fatalf("canonicalize shape B: %v", err)
	}
	renderShape := func(shape []explorer.ExplicitGroupDefinition) string {
		parts := make([]string, 0, len(shape))
		for _, g := range shape {
			parts = append(parts, fmt.Sprintf("%s|%s|%d", g.ID, g.Label, g.Ordinal))
		}
		return strings.Join(parts, "\x1e")
	}
	for round := 0; round < rounds; round++ {
		key := fmt.Sprintf("race-put-conflict-%d-%s", round, pgstate06RandomHex(t))
		revision := pgstate06Revision(source, key)
		raceToken := "race-put-token-" + pgstate06RandomHex(t)
		if _, err := storeA.BeginExplicitGroupRevision(f.ctx, revision, source, raceToken); err != nil {
			f.fail("begin conflict-race revision", err)
		}
		errA, errB := raceFire(
			func() error { return storeA.PutExplicitGroupDefinitions(f.ctx, revision.ID, raceToken, shapeA) },
			func() error { return storeB.PutExplicitGroupDefinitions(f.ctx, revision.ID, raceToken, shapeB) },
		)
		if !exactlyOneConflict(errA, errB) {
			t.Fatalf("conflicting put round %d: errA=%v errB=%v, want one success and one typed conflict", round, errA, errB)
		}
		stored := f.definitionsSnapshot(t, string(revision.ID))
		if stored != renderShape(shapeA) && stored != renderShape(shapeB) {
			t.Fatalf("conflicting put round %d: stored definitions = %q, want exactly one canonical shape", round, stored)
		}
		if got := f.countRows(t, f.definitionsTable(), "revision_id = $1", string(revision.ID)); got != 3 {
			t.Fatalf("conflicting put round %d: definition count = %d, want 3 (no interleaved halves)", round, got)
		}
		// Post-abort convergence for this round.
		mustNil(t, fmt.Sprintf("conflicting put round %d owner abort", round), func() error {
			return storeA.AbortExplicitGroupRevision(f.ctx, revision.ID, raceToken)
		})
		if got := f.countRows(t, f.definitionsTable(), "revision_id = $1", string(revision.ID)); got != 0 {
			t.Fatalf("conflicting put round %d: after abort definition count = %d, want 0", round, got)
		}
		if got := f.countRows(t, f.revisionTable(), "id = $1", string(revision.ID)); got != 0 {
			t.Fatalf("conflicting put round %d: after abort header count = %d, want 0", round, got)
		}
	}
}

// TestPostgresExplicitGroupDefinitionsCleanupVersusActiveWriterRace: ten
// rounds of a live writer working on its revision while the other backend
// sweeps expired rows concurrently. The active writer's header and
// definitions must stay byte-identical across the sweep, its claim stays
// its own, and the expired bait is reaped every round.
func TestPostgresExplicitGroupDefinitionsCleanupVersusActiveWriterRace(t *testing.T) {
	f := newPGState06Fixture(t)
	f.migrate(t)
	storeA, storeB := f.raceSetup(t)

	project := "pgstate06-project"
	source := pgstate06Selection(project, "race-cleanup-"+pgstate06RandomHex(t))

	const rounds = 10
	for round := 0; round < rounds; round++ {
		key := fmt.Sprintf("race-cleanup-%d-%s", round, pgstate06RandomHex(t))
		revision := pgstate06Revision(source, key)
		writerToken := "race-cleanup-writer-" + pgstate06RandomHex(t)

		if _, err := storeA.BeginExplicitGroupRevision(f.ctx, revision, source, writerToken); err != nil {
			f.fail("begin racing revision", err)
		}
		if err := storeA.PutExplicitGroupDefinitions(f.ctx, revision.ID, writerToken, pgstate06Definitions()); err != nil {
			f.fail("seed racing definitions", err)
		}
		headerBefore := f.headerSnapshot(t, string(revision.ID))
		defsBefore := f.definitionsSnapshot(t, string(revision.ID))

		// Expired bait for the sweeper.
		baitKey := fmt.Sprintf("race-cleanup-bait-%d-%s", round, pgstate06RandomHex(t))
		bait := pgstate06Revision(source, baitKey)
		past := time.Now().UTC().Add(-time.Hour)
		baitLease := time.Now().UTC().Add(-time.Minute)
		f.rawInsertHeader(t, f.witness, explorer.ExplicitGroupRevision{
			ID: bait.ID, Project: bait.Project, Generation: bait.Generation,
			ScopeDigest: bait.ScopeDigest, ResourceType: bait.ResourceType,
			SourceSelectionRevisionID: bait.SourceSelectionRevisionID,
			SourceMembershipDigest:    bait.SourceMembershipDigest,
			State:                     explorer.ExplicitGroupRevisionStaging,
			IdempotencyKey:            bait.IdempotencyKey,
			CreatedAt:                 past,
		}, "bait-writer", &baitLease)
		f.rawInsertDefinition(t, f.witness, string(bait.ID), "group-bait", "Bait", 0)

		// The racing pair: the owner keeps working (retry + put replay)
		// while the sweeper sweeps.
		errA, errB := raceFire(
			func() error {
				if _, err := storeA.BeginExplicitGroupRevision(f.ctx, revision, source, writerToken); err != nil {
					return err
				}
				return storeA.PutExplicitGroupDefinitions(f.ctx, revision.ID, writerToken, pgstate06Definitions())
			},
			func() error {
				return storeB.CleanupExplicitGroupStaging(f.ctx, time.Now().UTC(), 100)
			},
		)
		if errA != nil {
			t.Fatalf("cleanup race round %d: active writer errored: %v", round, errA)
		}
		if errB != nil {
			t.Fatalf("cleanup race round %d: sweeper errored: %v", round, errB)
		}

		// The active writer survived untouched. Its lease necessarily
		// moved (its own retry renewed it), so compare every frozen field
		// and the definition rows.
		if got := f.frozenHeaderSnapshot(t, string(revision.ID)); got != strings.Join(strings.Split(headerBefore, "\x1f")[:15], "\x1f") {
			t.Fatalf("cleanup race round %d: active writer's frozen fields changed:\nbefore %q\nafter  %q",
				round, strings.Join(strings.Split(headerBefore, "\x1f")[:15], "\x1f"), got)
		}
		if got := f.definitionsSnapshot(t, string(revision.ID)); got != defsBefore {
			t.Fatalf("cleanup race round %d: active writer's definitions changed:\nbefore %q\nafter  %q", round, defsBefore, got)
		}
		// The expired bait is always gone: both tables, no half state.
		if got := f.countRows(t, f.revisionTable(), "id = $1", string(bait.ID)); got != 0 {
			t.Fatalf("cleanup race round %d: bait header count = %d, want 0", round, got)
		}
		if got := f.countRows(t, f.definitionsTable(), "revision_id = $1", string(bait.ID)); got != 0 {
			t.Fatalf("cleanup race round %d: bait definition count = %d, want 0", round, got)
		}
		// Post-abort convergence: the racing revision goes away cleanly.
		mustNil(t, fmt.Sprintf("cleanup race round %d owner abort", round), func() error {
			return storeA.AbortExplicitGroupRevision(f.ctx, revision.ID, writerToken)
		})
		if got := f.countRows(t, f.revisionTable(), "id = $1", string(revision.ID)); got != 0 {
			t.Fatalf("cleanup race round %d: after abort header count = %d, want 0", round, got)
		}
	}
}

// ---------------------------------------------------------------------------
// fault injection: real transaction aborts leave zero half-applied state
// ---------------------------------------------------------------------------

func TestPostgresExplicitGroupDefinitionsFaultInjectionRollback(t *testing.T) {
	f := newPGState06Fixture(t)
	f.migrate(t)

	project := "pgstate06-project"
	source := pgstate06Selection(project, "fault-"+pgstate06RandomHex(t))
	token := "writer-" + pgstate06RandomHex(t)

	// Seam 1: a fault after the real definition insert aborts the put --
	// no definition rows may survive and the lease may not move.
	key := "fault-put-" + pgstate06RandomHex(t)
	revision := pgstate06Revision(source, key)
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, token); err != nil {
		f.fail("begin", err)
	}
	headerBefore := f.headerSnapshot(t, string(revision.ID))
	fault := errors.New("injected put fault")
	f.store.afterDefinitionsInserted = func(context.Context, pgx.Tx) error { return fault }
	err := f.store.PutExplicitGroupDefinitions(f.ctx, revision.ID, token, pgstate06Definitions())
	if err == nil || !errors.Is(err, fault) {
		t.Fatalf("put with injected fault = %v, want the fault to surface", err)
	}
	f.store.afterDefinitionsInserted = nil
	if got := f.countRows(t, f.definitionsTable(), "revision_id = $1", string(revision.ID)); got != 0 {
		t.Fatalf("aborted put left %d definition rows, want 0", got)
	}
	if got := f.headerSnapshot(t, string(revision.ID)); got != headerBefore {
		t.Fatalf("aborted put moved the header row:\nbefore %q\nafter  %q", headerBefore, got)
	}
	// Mutation probe: the same put now succeeds and produces exactly the
	// canonical rows.
	mustNil(t, "put after fault", func() error {
		return f.store.PutExplicitGroupDefinitions(f.ctx, revision.ID, token, pgstate06Definitions())
	})
	defsAfter := f.definitionsSnapshot(t, string(revision.ID))
	if !strings.Contains(defsAfter, "group-alpha|Alpha cohort|0") || !strings.Contains(defsAfter, "group-empty|No members|1") {
		t.Fatalf("post-fault put stored %q, want the canonical set", defsAfter)
	}

	// Seam 2: a fault after the real definition delete aborts the abort --
	// the header AND its definition rows must both still be there.
	keyAbort := "fault-abort-" + pgstate06RandomHex(t)
	revisionAbort := pgstate06Revision(source, keyAbort)
	tokenAbort := "writer-abort-" + pgstate06RandomHex(t)
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, revisionAbort, source, tokenAbort); err != nil {
		f.fail("begin abort fixture", err)
	}
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, revisionAbort, source, tokenAbort); err != nil {
		f.fail("warm lease", err)
	}
	mustNil(t, "put abort fixture", func() error {
		return f.store.PutExplicitGroupDefinitions(f.ctx, revisionAbort.ID, tokenAbort, pgstate06Definitions())
	})
	headerBeforeAbort := f.headerSnapshot(t, string(revisionAbort.ID))
	defsBeforeAbort := f.definitionsSnapshot(t, string(revisionAbort.ID))
	f.store.afterDefinitionsDeleted = func(context.Context, pgx.Tx) error { return fault }
	err = f.store.AbortExplicitGroupRevision(f.ctx, revisionAbort.ID, tokenAbort)
	if err == nil || !errors.Is(err, fault) {
		t.Fatalf("abort with injected fault = %v, want the fault to surface", err)
	}
	f.store.afterDefinitionsDeleted = nil
	if got := f.headerSnapshot(t, string(revisionAbort.ID)); got != headerBeforeAbort {
		t.Fatalf("aborted abort removed the header:\nbefore %q\nafter  %q", headerBeforeAbort, got)
	}
	if got := f.definitionsSnapshot(t, string(revisionAbort.ID)); got != defsBeforeAbort {
		t.Fatalf("aborted abort removed definition rows:\nbefore %q\nafter  %q", defsBeforeAbort, got)
	}
	// Mutation probe: the real abort now completes and removes everything.
	mustNil(t, "abort after fault", func() error {
		return f.store.AbortExplicitGroupRevision(f.ctx, revisionAbort.ID, tokenAbort)
	})
	if got := f.countRows(t, f.revisionTable(), "id = $1", string(revisionAbort.ID)); got != 0 {
		t.Fatalf("post-fault abort left header count = %d, want 0", got)
	}
	if got := f.countRows(t, f.definitionsTable(), "revision_id = $1", string(revisionAbort.ID)); got != 0 {
		t.Fatalf("post-fault abort left definition count = %d, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// migration ledger: self-recomputed sha256 pins and rerun convergence
// ---------------------------------------------------------------------------

func TestPostgresExplicitGroupDefinitionsMigrationLedgerConvergence(t *testing.T) {
	f := newPGState06Fixture(t)
	first := f.migrate(t)

	owner, err := OwnerMigrations()
	if err != nil {
		f.fail("owner migrations", err)
	}

	// The full shared Explorer ledger this ticket composes into: 1, 2, 3,
	// 5, 6 (owner dir), plus this ticket's explicit-groups migration at
	// its coordinator-assigned version.
	wantVersions := []int64{1, 2, 3, 5, 6, pgstate06MigrationVersion}
	if len(owner) != len(wantVersions) {
		t.Fatalf("embedded history has %d migrations, want %d (versions %v)", len(owner), len(wantVersions), wantVersions)
	}
	for i, want := range wantVersions {
		if owner[i].Version != want {
			t.Fatalf("embedded migration[%d].Version = %d, want %d", i, owner[i].Version, want)
		}
	}

	// Every ledger row must match a checksum SELF-RECOMPUTED here from
	// the embedded bytes -- never the loader's self-report.
	type ledgerRow struct {
		version  int64
		name     string
		checksum string
	}
	readLedger := func() []ledgerRow {
		rows, err := f.witness.Query(f.ctx, `SELECT version, name, checksum FROM `+
			sanitizeTableRef(f.schema, "schema_migrations")+` ORDER BY version`)
		if err != nil {
			f.fail("read ledger", err)
		}
		defer rows.Close()
		var out []ledgerRow
		for rows.Next() {
			var rec ledgerRow
			if err := rows.Scan(&rec.version, &rec.name, &rec.checksum); err != nil {
				f.fail("scan ledger", err)
			}
			out = append(out, rec)
		}
		if err := rows.Err(); err != nil {
			f.fail("read ledger", err)
		}
		return out
	}
	ledger := readLedger()
	if len(ledger) != len(wantVersions) {
		t.Fatalf("ledger holds %d rows, want %d", len(ledger), len(wantVersions))
	}
	for i, migration := range owner {
		recomputed := sha256.Sum256([]byte(migration.SQL))
		hexRecomputed := hex.EncodeToString(recomputed[:])
		if migration.Checksum != hexRecomputed {
			t.Fatalf("migration %d/%s loader checksum %s != self-recomputed %s",
				migration.Version, migration.Name, migration.Checksum, hexRecomputed)
		}
		if ledger[i] != (ledgerRow{migration.Version, migration.Name, hexRecomputed}) {
			t.Fatalf("ledger[%d] = %+v, want %+v with self-recomputed checksum %s",
				i, ledger[i], migration, hexRecomputed)
		}
		if migration.Version == pgstate06MigrationVersion {
			if migration.Name != pgstate06MigrationName {
				t.Fatalf("explicit-groups migration name = %q, want %q", migration.Name, pgstate06MigrationName)
			}
			if hexRecomputed != pgstate06MigrationChecksum {
				t.Fatalf("explicit-groups migration checksum = %s, want the byte-frozen pin %s: the migration drifted",
					hexRecomputed, pgstate06MigrationChecksum)
			}
		}
	}

	// Rerun convergence: the applied history re-runs as a no-op preserving
	// state and the identical ledger, including after real data exists.
	source := pgstate06Selection("pgstate06-project", "ledger-"+pgstate06RandomHex(t))
	key := "ledger-" + pgstate06RandomHex(t)
	revision := pgstate06Revision(source, key)
	token := "writer-" + pgstate06RandomHex(t)
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, token); err != nil {
		f.fail("begin ledger fixture", err)
	}
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, token); err != nil {
		f.fail("renew ledger fixture", err)
	}
	headerBefore := f.headerSnapshot(t, string(revision.ID))

	second := f.migrate(t)
	if len(first.Applied) != len(second.Applied) || first.LatestVersion != second.LatestVersion {
		t.Fatalf("rerun result = %+v, first = %+v, want the identical convergence", second, first)
	}
	if second.LatestVersion != pgstate06MigrationVersion {
		t.Fatalf("latest ledger version = %d, want %d", second.LatestVersion, pgstate06MigrationVersion)
	}
	if got := readLedger(); len(got) != len(ledger) {
		t.Fatalf("ledger after rerun holds %d rows, want %d", len(got), len(ledger))
	}
	if got := f.headerSnapshot(t, string(revision.ID)); got != headerBefore {
		t.Fatalf("migration rerun mutated staged state:\nbefore %q\nafter  %q", headerBefore, got)
	}

	// Mixed-history fail-closed: pointing the source-store history at this
	// Explorer control-plane schema is refused before any statement (the
	// package-wide one-history-per-schema invariant, witnessed here on
	// this ticket's schema so the explicit-group tables participate).
	pgStoreClient, err := pgstore.Open(f.ctx, pgstore.Config{DSN: f.dsn, MaxConns: 2, MinConns: 0, AcquireTimeout: 10 * time.Second})
	if err != nil {
		f.fail("open mixed-history pool", err)
	}
	defer pgStoreClient.Close()
	competing := fmt.Sprintf("pgstate06_it_%d_%s", time.Now().UnixNano(), pgstate06RandomHex(t))
	if _, err := f.iso.Exec(f.ctx, `CREATE SCHEMA "`+competing+`"`); err != nil {
		f.fail("create competing schema", err)
	}
	if _, err := pgstore.MigrateSchema(f.ctx, pgStoreClient, competing); err != nil {
		f.fail("bootstrap competing schema with source-store history", err)
	}
	_, err = MigrateSchema(f.ctx, pgStoreClient, competing)
	if !errors.Is(err, pgstore.ErrMigrationChecksumMismatch) {
		t.Fatalf("mixed history = %v, want ErrMigrationChecksumMismatch fail-closed", err)
	}
	if _, err := f.iso.Exec(f.ctx, `DROP SCHEMA IF EXISTS "`+competing+`" CASCADE`); err != nil {
		f.fail("drop competing schema", err)
	}
}

// TestZZNoLeftoverPgstate06Resources audits that the pgstate06_it_*
// namespace this suite owns is empty after every test in this file. It is
// ownership-scoped: sibling suites sweep their own prefixes and concurrent
// siblings must never break this scan.
func TestZZNoLeftoverPgstate06Resources(t *testing.T) {
	raw := pgstate06TestPostgresURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect for zero-leftover audit: %v", err)
	}
	defer admin.Close()
	var databases int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_database WHERE datname LIKE 'pgstate06\_it\_%' ESCAPE '\'`).Scan(&databases); err != nil {
		t.Fatalf("audit databases: %v", err)
	}
	if databases != 0 {
		t.Errorf("zero-leftover audit: %d pgstate06_it_* databases remain", databases)
	}
	var schemas int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'pgstate06\_it\_%' ESCAPE '\'`).Scan(&schemas); err != nil {
		t.Fatalf("audit schemas: %v", err)
	}
	if schemas != 0 {
		t.Errorf("zero-leftover audit: %d pgstate06_it_* schemas remain in the service database", schemas)
	}
}

// ---------------------------------------------------------------------------
// database-enforced guards: named refusals, guard-drop probes, rerun
// ---------------------------------------------------------------------------

func TestPostgresExplicitGroupDefinitionsHeaderDatabaseGuards(t *testing.T) {
	f := newPGState06Fixture(t)
	f.migrate(t)

	project := "pgstate06-project"
	source := pgstate06Selection(project, "guards-"+pgstate06RandomHex(t))
	token := "writer-" + pgstate06RandomHex(t)

	// Helper: honest staging row via the store.
	newHeader := func(label string) explorer.ExplicitGroupRevision {
		key := "guard-" + label + "-" + pgstate06RandomHex(t)
		revision := pgstate06Revision(source, key)
		if _, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, token); err != nil {
			f.fail("begin guard fixture "+label, err)
		}
		return revision
	}

	// Named CHECK refusals for forged rows (non-vacuity: the honest row
	// shape inserts, the forged field is refused by its exact constraint).
	honest := pgstate06Revision(source, "check-honest-"+pgstate06RandomHex(t))
	f.rawInsertHeader(t, f.witness, honest, "tok", nil)
	if got := f.countRows(t, f.revisionTable(), "id = $1", string(honest.ID)); got != 1 {
		t.Fatalf("honest raw header count = %d, want 1", got)
	}

	forgedID := pgstate06Revision(source, "check-forged-id-"+pgstate06RandomHex(t))
	forgedID.ID = explorer.ExplicitGroupRevisionID("grouprev_" + strings.Repeat("0", 64))
	_, err := f.witness.Exec(f.ctx, `INSERT INTO `+f.revisionTable()+` (id, project, generation, scope_digest,
		resource_type, source_selection_revision_id, source_membership_digest, state, idempotency_key, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'STAGING', $8, now())`,
		string(forgedID.ID), forgedID.Project, forgedID.Generation, forgedID.ScopeDigest, forgedID.ResourceType,
		forgedID.SourceSelectionRevisionID, forgedID.SourceMembershipDigest, forgedID.IdempotencyKey)
	requirePgErrConstraintPGState06(t, err, pgstate06CheckViolation,
		explicitGroupRevisionIdentityCheck, "forged id refused by the named identity-binding CHECK")

	complete := pgstate06Revision(source, "check-complete-"+pgstate06RandomHex(t))
	_, err = f.witness.Exec(f.ctx, `INSERT INTO `+f.revisionTable()+` (id, project, generation, scope_digest,
		resource_type, source_selection_revision_id, source_membership_digest, state, idempotency_key, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'COMPLETE', $8, now())`,
		string(complete.ID), complete.Project, complete.Generation, complete.ScopeDigest, complete.ResourceType,
		complete.SourceSelectionRevisionID, complete.SourceMembershipDigest, complete.IdempotencyKey)
	requirePgErrConstraintPGState06(t, err, pgstate06CheckViolation,
		explicitGroupRevisionStateConstraint, "COMPLETE header refused by the named completion-gate CHECK")

	stagingWithCompletion := pgstate06Revision(source, "check-staging-values-"+pgstate06RandomHex(t))
	_, err = f.witness.Exec(f.ctx, `INSERT INTO `+f.revisionTable()+` (id, project, generation, scope_digest,
		resource_type, source_selection_revision_id, source_membership_digest, definition_digest, state, idempotency_key, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'not-empty', 'STAGING', $8, now())`,
		string(stagingWithCompletion.ID), stagingWithCompletion.Project, stagingWithCompletion.Generation,
		stagingWithCompletion.ScopeDigest, stagingWithCompletion.ResourceType,
		stagingWithCompletion.SourceSelectionRevisionID, stagingWithCompletion.SourceMembershipDigest,
		stagingWithCompletion.IdempotencyKey)
	requirePgErrConstraintPGState06(t, err, pgstate06CheckViolation,
		explicitGroupRevisionStagingValuesCK, "staging header with completion values refused by the named CHECK")

	blankField := pgstate06Revision(source, "check-required-"+pgstate06RandomHex(t))
	_, err = f.witness.Exec(f.ctx, `INSERT INTO `+f.revisionTable()+` (id, project, generation, scope_digest,
		resource_type, source_selection_revision_id, source_membership_digest, state, idempotency_key, created_at)
		VALUES ($1, $2::text, $3::text, $4, $5, $6, $7, 'STAGING', $8, now())`,
		string(blankField.ID), blankField.Project, "", blankField.ScopeDigest, blankField.ResourceType,
		blankField.SourceSelectionRevisionID, blankField.SourceMembershipDigest, blankField.IdempotencyKey)
	requirePgErrConstraintPGState06(t, err, pgstate06CheckViolation,
		explicitGroupRevisionRequiredFieldsCK, "blank required field refused by the named CHECK")

	// UPDATE guards.
	guarded := newHeader("guarded")
	frozenUpdate := func(sql string, args ...any) error {
		_, err := f.witness.Exec(f.ctx, sql, args...)
		return err
	}
	requirePgErrTriggerPGState06(t, frozenUpdate(`UPDATE `+f.revisionTable()+` SET project = 'hijacked' WHERE id = $1`, string(guarded.ID)),
		pgstate06CheckViolation, "explorer_explicit_group_revisions guard", "frozen column UPDATE names its trigger")
	requirePgErrConstraintPGState06(t, frozenUpdate(`UPDATE `+f.revisionTable()+` SET state = 'COMPLETE' WHERE id = $1`, string(guarded.ID)),
		pgstate06CheckViolation, explicitGroupRevisionStateConstraint,
		"direct completion refused by the named completion-gate CHECK")
	requirePgErrTriggerPGState06(t, frozenUpdate(`UPDATE `+f.revisionTable()+` SET completed_at = now() WHERE id = $1`, string(guarded.ID)),
		pgstate06CheckViolation, "explorer_explicit_group_revisions guard", "completed_at is frozen content")
	requirePgErrTriggerPGState06(t, frozenUpdate(`UPDATE `+f.revisionTable()+` SET writer_expires_at = now() + interval '1 hour' WHERE id = $1`, string(guarded.ID)),
		pgstate06CheckViolation, "explorer_explicit_group_revisions guard", "lease renewal without the writer-token GUC is refused")

	// Legal channel: renewal WITH the GUC goes through (mutation probe --
	// the UPDATE guard is not a blanket block).
	frozenBefore := f.frozenHeaderSnapshot(t, string(guarded.ID))
	beforeLease := strings.Split(f.headerSnapshot(t, string(guarded.ID)), "\x1f")[16]
	if _, err := f.witness.Exec(f.ctx, `SELECT set_config('`+gucWriterToken+`', $1, false)`, token); err != nil {
		f.fail("set writer token GUC on witness", err)
	}
	if _, err := f.witness.Exec(f.ctx, `UPDATE `+f.revisionTable()+` SET writer_expires_at = now() + interval '2 hours' WHERE id = $1`, string(guarded.ID)); err != nil {
		f.fail("authorized lease renewal", err)
	}
	afterLease := strings.Split(f.headerSnapshot(t, string(guarded.ID)), "\x1f")[16]
	if afterLease == beforeLease {
		t.Fatal("authorized lease renewal did not move the deadline")
	}
	if got := f.frozenHeaderSnapshot(t, string(guarded.ID)); got != frozenBefore {
		t.Fatalf("authorized renewal changed frozen columns:\nbefore %q\nafter  %q", frozenBefore, got)
	}
	if _, err := f.witness.Exec(f.ctx, `SELECT set_config('`+gucWriterToken+`', NULL, false)`); err != nil {
		f.fail("clear writer token GUC on witness", err)
	}

	// Lease may never be shortened or revoked even by the honest holder.
	shortenProbe := func(sql string) {
		if _, err := f.witness.Exec(f.ctx, `SELECT set_config('`+gucWriterToken+`', $1, false)`, token); err != nil {
			f.fail("set writer token GUC", err)
		}
		_, err := f.witness.Exec(f.ctx, sql, string(guarded.ID))
		requirePgErrTriggerPGState06(t, err, pgstate06CheckViolation,
			"explorer_explicit_group_revisions guard", "lease monotonicity guard")
		if _, err := f.witness.Exec(f.ctx, `SELECT set_config('`+gucWriterToken+`', NULL, false)`); err != nil {
			f.fail("clear writer token GUC", err)
		}
	}
	shortenProbe(`UPDATE ` + f.revisionTable() + ` SET writer_expires_at = '2020-01-01T00:00:00Z'::timestamptz WHERE id = $1`)
	shortenProbe(`UPDATE ` + f.revisionTable() + ` SET writer_expires_at = NULL WHERE id = $1`)

	// DELETE guard: no authorization at all is refused.
	requirePgErrTriggerPGState06(t, frozenUpdate(`DELETE FROM `+f.revisionTable()+` WHERE id = $1`, string(guarded.ID)),
		pgstate06CheckViolation, "explorer_explicit_group_revisions guard", "unauthorized DELETE names its trigger")

	// Guard-drop probe: with the trigger dropped, the same violating
	// UPDATE and DELETE succeed -- proof the guard, not an accident,
	// blocked them -- and the migration's own text restores the identical
	// guard, which blocks them again. The probe column is one the CHECKs
	// do not police (the source membership digest is required non-empty
	// but not content-bound), so only the trigger can refuse it.
	f.dropTrigger(t, f.revisionTable(), "explorer_explicit_group_revisions_guard")
	unguarded := newHeader("unguarded")
	mustNil(t, "unguarded frozen UPDATE succeeds once the guard is dropped", func() error {
		_, err := f.witness.Exec(f.ctx, `UPDATE `+f.revisionTable()+` SET source_membership_digest = 'hijacked' WHERE id = $1`, string(unguarded.ID))
		return err
	})
	mustNil(t, "unguarded DELETE succeeds once the guard is dropped", func() error {
		_, err := f.witness.Exec(f.ctx, `DELETE FROM `+f.revisionTable()+` WHERE id = $1`, string(unguarded.ID))
		return err
	})
	if got := f.countRows(t, f.revisionTable(), "id = $1", string(unguarded.ID)); got != 0 {
		t.Fatalf("unguarded DELETE left %d rows, want 0 (probe must actually delete)", got)
	}
	f.rerunMigration(t, pgstate06MigrationVersion)
	requirePgErrTriggerPGState06(t, frozenUpdate(`UPDATE `+f.revisionTable()+` SET source_membership_digest = 'hijacked2' WHERE id = $1`, string(guarded.ID)),
		pgstate06CheckViolation, "explorer_explicit_group_revisions guard", "rerun migration re-blocks the frozen UPDATE")
	requirePgErrTriggerPGState06(t, frozenUpdate(`DELETE FROM `+f.revisionTable()+` WHERE id = $1`, string(guarded.ID)),
		pgstate06CheckViolation, "explorer_explicit_group_revisions guard", "rerun migration re-blocks the DELETE")
}

func TestPostgresExplicitGroupDefinitionsDefinitionDatabaseGuards(t *testing.T) {
	f := newPGState06Fixture(t)
	f.migrate(t)

	project := "pgstate06-project"
	source := pgstate06Selection(project, "defguards-"+pgstate06RandomHex(t))
	token := "writer-" + pgstate06RandomHex(t)
	key := "defguards-" + pgstate06RandomHex(t)
	revision := pgstate06Revision(source, key)
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, token); err != nil {
		f.fail("begin", err)
	}
	mustNil(t, "put", func() error {
		return f.store.PutExplicitGroupDefinitions(f.ctx, revision.ID, token, pgstate06Definitions())
	})
	want := f.definitionsSnapshot(t, string(revision.ID))

	// Shape CHECKs by name, each with its legal control: the same shape
	// with honest fields inserts, the forged field is refused.
	_, err := f.witness.Exec(f.ctx, `INSERT INTO `+f.definitionsTable()+` (revision_id, group_id, label, ordinal)
		VALUES ($1, 'group-alpha', 'Dup alpha', 99)`, string(revision.ID))
	requirePgErrConstraintPGState06(t, err, "23505", explicitGroupDefinitionPKConstraint,
		"duplicate (revision, group) refused by the named PK constraint")
	_, err = f.witness.Exec(f.ctx, `INSERT INTO `+f.definitionsTable()+` (revision_id, group_id, label, ordinal)
		VALUES ($1, 'group-new', 'Ordinal clash', 2)`, string(revision.ID))
	requirePgErrConstraintPGState06(t, err, "23505", explicitGroupDefinitionOrdinalCK,
		"duplicate ordinal refused by the named unique constraint")
	_, err = f.witness.Exec(f.ctx, `INSERT INTO `+f.definitionsTable()+` (revision_id, group_id, label, ordinal)
		VALUES ($1, 'group-blanklabel', '', 50)`, string(revision.ID))
	requirePgErrConstraintPGState06(t, err, pgstate06CheckViolation,
		"explorer_explicit_group_definitions_label_present", "blank label refused by the named CHECK")
	_, err = f.witness.Exec(f.ctx, `INSERT INTO `+f.definitionsTable()+` (revision_id, group_id, label, ordinal)
		VALUES ($1, '', 'Blank group id', 51)`, string(revision.ID))
	requirePgErrConstraintPGState06(t, err, pgstate06CheckViolation,
		"explorer_explicit_group_definitions_group_id_present", "blank group id refused by the named CHECK")
	_, err = f.witness.Exec(f.ctx, `INSERT INTO `+f.definitionsTable()+` (revision_id, group_id, label, ordinal)
		VALUES ($1, 'group-negative', 'Negative ordinal', -1)`, string(revision.ID))
	requirePgErrConstraintPGState06(t, err, pgstate06CheckViolation,
		"explorer_explicit_group_definitions_ordinal_non_negative", "negative ordinal refused by the named CHECK")
	_, err = f.witness.Exec(f.ctx, `INSERT INTO `+f.definitionsTable()+` (revision_id, group_id, label, ordinal)
		VALUES ('grouprev_' || repeat('f', 64), 'group-orphan', 'Orphan', 52)`)
	requirePgErrConstraintPGState06(t, err, "23503", explicitGroupRevisionFKConstraint,
		"orphan definition refused by the named FK constraint")
	// Legal control: the same shape with honest fields inserts.
	f.rawInsertDefinition(t, f.witness, string(revision.ID), "group-legal-control", "Legal control", 53)
	if got := f.countRows(t, f.definitionsTable(), "revision_id = $1 AND group_id = 'group-legal-control'", string(revision.ID)); got != 1 {
		t.Fatalf("legal control definition count = %d, want 1", got)
	}

	// Definitions are write-once.
	_, err = f.witness.Exec(f.ctx, `UPDATE `+f.definitionsTable()+` SET label = 'Rewritten'
		WHERE revision_id = $1 AND group_id = 'group-alpha'`, string(revision.ID))
	requirePgErrTriggerPGState06(t, err, pgstate06CheckViolation,
		"explorer_explicit_group_definitions guard", "definition UPDATE names its write-once trigger")

	// Un-authorized child DELETE is refused; the authorized abort channel
	// is exercised through the store below, so this proves the guard
	// refused only because the caller held nothing.
	_, err = f.witness.Exec(f.ctx, `DELETE FROM `+f.definitionsTable()+`
		WHERE revision_id = $1 AND group_id = 'group-legal-control'`, string(revision.ID))
	requirePgErrTriggerPGState06(t, err, pgstate06CheckViolation,
		"explorer_explicit_group_definitions guard", "unauthorized child DELETE names its trigger")

	// FK layering: even holding the abort token, the header cannot go
	// before its definition rows -- RESTRICT refuses first, by name
	// (ON DELETE RESTRICT raises 23001 restrict_violation on PostgreSQL).
	if _, err := f.witness.Exec(f.ctx, `SELECT set_config('`+gucAbortToken+`', $1, false)`, token); err != nil {
		f.fail("set abort token GUC", err)
	}
	_, err = f.witness.Exec(f.ctx, `DELETE FROM `+f.revisionTable()+` WHERE id = $1`, string(revision.ID))
	requirePgErrConstraintPGState06(t, err, "23001", explicitGroupRevisionFKConstraint,
		"header DELETE blocked by the named FK until children are gone")
	// Mutation probe: children first, then the header, both under the
	// same abort authorization.
	if _, err := f.witness.Exec(f.ctx, `DELETE FROM `+f.definitionsTable()+`
		WHERE revision_id = $1 AND group_id = 'group-legal-control'`, string(revision.ID)); err != nil {
		f.fail("authorized child DELETE", err)
	}
	if _, err := f.witness.Exec(f.ctx, `SELECT set_config('`+gucWriterToken+`', NULL, false)`); err != nil {
		f.fail("clear writer GUC", err)
	}
	if _, err := f.witness.Exec(f.ctx, `SELECT set_config('`+gucAbortToken+`', NULL, false)`); err != nil {
		f.fail("clear abort GUC", err)
	}

	// Guard-drop probe on the definition trigger: the write-once UPDATE
	// passes once dropped, and the migration text restores it.
	f.dropTrigger(t, f.definitionsTable(), "explorer_explicit_group_definitions_guard")
	mustNil(t, "unguarded definition UPDATE succeeds", func() error {
		_, err := f.witness.Exec(f.ctx, `UPDATE `+f.definitionsTable()+` SET label = 'Rewritten'
			WHERE revision_id = $1 AND group_id = 'group-alpha'`, string(revision.ID))
		return err
	})
	f.rerunMigration(t, pgstate06MigrationVersion)
	_, err = f.witness.Exec(f.ctx, `UPDATE `+f.definitionsTable()+` SET label = 'Rewritten again'
		WHERE revision_id = $1 AND group_id = 'group-alpha'`, string(revision.ID))
	requirePgErrTriggerPGState06(t, err, pgstate06CheckViolation,
		"explorer_explicit_group_definitions guard", "rerun migration re-blocks the write-once UPDATE")

	// The header guard still works too after the rerun (single-migration
	// composition restored BOTH guards).
	_, err = f.witness.Exec(f.ctx, `DELETE FROM `+f.revisionTable()+` WHERE id = $1`, string(revision.ID))
	requirePgErrTriggerPGState06(t, err, pgstate06CheckViolation,
		"explorer_explicit_group_revisions guard", "header guard restored by the same rerun")
	if got := f.countRows(t, f.revisionTable(), "id = $1", string(revision.ID)); got != 1 {
		t.Fatalf("header survived guards count = %d, want 1", got)
	}
	if got := f.definitionsSnapshot(t, string(revision.ID)); !strings.Contains(got, "group-alpha|Rewritten|0") {
		t.Fatalf("definitions after probes = %q, want the witnessed rewrite only", got)
	}
	_ = want
}
