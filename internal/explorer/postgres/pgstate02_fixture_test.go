package postgres

// Test fixtures for the pg-state-receipt-revision-immutable-v1 state tests
// (receipts_test.go, revisions_test.go). Everything this package creates is
// named pgstate02_it_* -- a namespace disjoint from the owner tests'
// pgstate01v1_it_* and from concurrent sibling workers' prefixes -- and is
// dropped by the test that created it. TestZZNoLeftoverPGState02Resources
// audits the namespace prefix at the end of the file order.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	pgstore "github.com/calypr/loom/internal/store/postgres"
)

var pgstate02NameRe = regexp.MustCompile(`^pgstate02_it_[0-9]+_[0-9a-f]+$`)

func pgstate02TestPostgresURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 Explorer receipt/revision integration tests")
	}
	return raw
}

func pgstate02RandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return hex.EncodeToString(b[:])
}

type pgState02Fixture struct {
	ctx    context.Context
	admin  *pgxpool.Pool // shared service database
	iso    *pgxpool.Pool // isolated database
	dbName string
	schema string
	dsn    string
	scrub  func(any) string
	fail   func(step string, err error)
}

// newPGState02Fixture creates one isolated database (pgstate02_it_*) plus
// one owned schema (pgstate02_it_*) inside it and registers LIFO cleanups:
// drop schema, close iso pool, drop database, then a zero-leftover audit
// scoped to this namespace, then close the admin pool, then cancel the
// context. Only resources this fixture created are ever dropped.
func newPGState02Fixture(t *testing.T) *pgState02Fixture {
	t.Helper()
	raw := pgstate02TestPostgresURL(t)

	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("LOOM_TEST_POSTGRES_URL must be a postgres:// or postgresql:// URL")
	}

	// Scrub every credential-bearing string that could appear in a failure
	// message: the raw service URL, the userinfo, the bare password, and the
	// rewritten isolated-database DSN appended below.
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

	// Registered first so it runs last: the cleanups below still need a live
	// context.
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	// Registered second so it runs second-to-last: the zero-leftover audit
	// below still needs this admin pool.
	t.Cleanup(admin.Close)

	dbName := fmt.Sprintf("pgstate02_it_%d_%s", time.Now().UnixNano(), pgstate02RandomHex(t))
	if !pgstate02NameRe.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}

	// Third-to-last: prove the database (and with it its schema) is gone,
	// and that no pgstate02_it_* leftovers remain anywhere this fixture
	// could have touched. The scan is scoped to this package's own prefix,
	// never a global sweep, so concurrent siblings in other namespaces
	// cannot make it fail.
	t.Cleanup(func() {
		var mine int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_database WHERE datname = $1`, dbName).Scan(&mine); err != nil {
			t.Errorf("leftover audit: query database catalog: %s", scrub(err))
		}
		if mine != 0 {
			t.Errorf("leftover audit: isolated database %s still exists", dbName)
		}
		var namespaceLeftovers int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_database WHERE datname LIKE 'pgstate02\_it\_%' ESCAPE '\'`).Scan(&namespaceLeftovers); err != nil {
			t.Errorf("leftover audit: query database namespace: %s", scrub(err))
		}
		if namespaceLeftovers != 0 {
			t.Errorf("leftover audit: %d pgstate02_it_* databases remain", namespaceLeftovers)
		}
		var schemaLeftovers int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'pgstate02\_it\_%' ESCAPE '\'`).Scan(&schemaLeftovers); err != nil {
			t.Errorf("leftover audit: query schema namespace: %s", scrub(err))
		}
		if schemaLeftovers != 0 {
			t.Errorf("leftover audit: %d pgstate02_it_* schemas remain in the service database", schemaLeftovers)
		}
	})

	// Fourth-to-last: drop only the database this test created.
	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(ctx),
			`DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %s", scrub(err))
		}
	})

	// pgconn lets a dbname query parameter outrank the URL path; drop any
	// dbname the service URL carries so isolation is provable.
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
	// Fifth-to-last: close the isolated-database pool before the drop.
	t.Cleanup(iso.Close)

	schema := fmt.Sprintf("pgstate02_it_%d_%s", time.Now().UnixNano(), pgstate02RandomHex(t))
	if !pgstate02NameRe.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	// Runs first: drop only the schema this test created.
	t.Cleanup(func() {
		if _, err := iso.Exec(context.WithoutCancel(ctx),
			`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})

	return &pgState02Fixture{ctx: ctx, admin: admin, iso: iso, dbName: dbName, schema: schema, dsn: dsn, scrub: scrub, fail: fail}
}

func (f *pgState02Fixture) receiptTable() string {
	return sanitizeTableRef(f.schema, ReceiptsTable)
}

func (f *pgState02Fixture) revisionTable() string {
	return sanitizeTableRef(f.schema, RevisionsTable)
}

// migrate applies this package's full history (ledger + owner + receipts +
// revisions) to the fixture's owned schema through the shared runner.
// The shared Explorer control-plane ledger (coordinator ruling): version 4
// of this schema's ledger belongs to the capability-artifacts ticket, whose
// DDL lives outside the embedded history, at
// tests/migrations-capabilities/000004_capability_artifacts.sql, applied
// through the same pg-store-02 runner on top of OwnerMigrations(). The
// fixture applies the composed history so the double-apply witnesses the
// FULL merged ledger [1 2 3 4 5 6] on the one shared version space -- and
// pins the fixture file byte-for-byte: the file is owned by the merged #81
// and must never drift under this suite.
const (
	pgstate02CapabilityFixtureDir      = "tests/migrations-capabilities"
	pgstate02CapabilityFixtureChecksum = "bec3e49f897b28ef71d7d69a5f7c71bb11d13b7c631ee26724de36a89d639222"
)

func pgstate02CapabilityFixtureMigration(t *testing.T) pgstore.Migration {
	t.Helper()
	fixture, err := pgstore.LoadMigrations(os.DirFS("../../../"), pgstate02CapabilityFixtureDir)
	if err != nil {
		t.Fatalf("load capability fixture migrations: %v", err)
	}
	if len(fixture) != 1 || fixture[0].Version != 4 || fixture[0].Name != "capability_artifacts" {
		t.Fatalf("capability fixture history = %+v, want exactly version 4 capability_artifacts", fixture)
	}
	if fixture[0].Checksum != pgstate02CapabilityFixtureChecksum {
		t.Fatalf("capability fixture checksum = %s, want the byte-frozen merge pin %s: the #81-owned fixture drifted",
			fixture[0].Checksum, pgstate02CapabilityFixtureChecksum)
	}
	return fixture[0]
}

func (f *pgState02Fixture) migrate(t *testing.T) pgstore.MigrateResult {
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
	history := append(append([]pgstore.Migration{}, owner...), pgstate02CapabilityFixtureMigration(t))
	runner, err := pgstore.NewMigrationRunner(client, f.schema, history)
	if err != nil {
		f.fail("new migration runner", err)
	}
	result, err := runner.Migrate(f.ctx)
	if err != nil {
		f.fail("migrate state schema", err)
	}
	return result
}

// rerunMigration applies one embedded migration's exact SQL text again
// through direct SQL. The migrations are convergent (IF NOT EXISTS /
// CREATE OR REPLACE / DROP TRIGGER IF EXISTS + CREATE TRIGGER), so a rerun
// restores the identical guard after a guard-removal probe -- proving the
// same text that created the guard is what blocks the violating write.
func (f *pgState02Fixture) rerunMigration(t *testing.T, version int64) {
	t.Helper()
	migrations, err := OwnerMigrations()
	if err != nil {
		f.fail("load migrations for rerun", err)
	}
	for _, m := range migrations {
		if m.Version == version {
			// Mirror the runner: SET LOCAL search_path to the target schema
			// so the migration's unqualified DDL (tables, functions,
			// triggers) resolves inside this fixture's schema, not public.
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

// newStore opens a fresh, independent readiness-checked pool (its own
// physical connections) and returns a Store over it. Tests use two stores:
// one writes, the other -- opened later, on a brand-new pool -- reads back,
// proving persistence past any single connection or session.
func (f *pgState02Fixture) newStore(t *testing.T) *Store {
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
		f.fail("build Explorer state store", err)
	}
	return store
}

// newDedicatedStore opens a MaxConns=1 pool -- one single physical backend
// connection -- warms it, and returns the Store plus that backend's
// pg_backend_pid. Two dedicated stores are the two real PG connections for
// CAS/claim race proofs: their pids must differ, and every statement that
// store runs provably executes on that one backend.
func (f *pgState02Fixture) newDedicatedStore(t *testing.T) (*Store, int32) {
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
	defer conn.Release()
	var pid int32
	if err := conn.QueryRow(f.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		f.fail("witness dedicated pg-store backend pid", err)
	}
	if pid == 0 {
		t.Fatal("dedicated pg-store pool reported backend pid 0")
	}
	store, err := New(client, f.schema)
	if err != nil {
		f.fail("build dedicated Explorer state store", err)
	}
	return store, pid
}

// newDedicatedPool is the raw-SQL sibling of newDedicatedStore: one physical
// backend connection for direct SQL (guard probes, raw CAS/claim attempts,
// transactions) with its backend pid witnessed up front.
func (f *pgState02Fixture) newDedicatedPool(t *testing.T) (*pgxpool.Pool, int32) {
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

// requireDistinctBackends fails unless the two witnessed backend pids are
// genuinely different PostgreSQL server processes -- the non-illusion
// precondition for every two-connection race proof.
func requireDistinctBackendsPGState02(t *testing.T, pidA, pidB int32, labelA, labelB string) {
	t.Helper()
	if pidA == pidB {
		t.Fatalf("%s and %s share backend pid %d, want two real PG connections", labelA, labelB, pidA)
	}
}

// rowSnapshot renders the complete physically stored row as one deterministic
// text value. Byte-identical snapshots before and after a refused operation
// are the row witness that the refusal wrote nothing.
func (f *pgState02Fixture) rowSnapshot(t *testing.T, table, where string, args ...any) string {
	t.Helper()
	rows, err := f.iso.Query(f.ctx,
		`SELECT (to_jsonb(t))::text FROM `+table+` t WHERE `+where, args...)
	if err != nil {
		f.fail("snapshot row in "+table, err)
	}
	defer rows.Close()
	var out string
	var count int
	for rows.Next() {
		if count > 0 {
			t.Fatalf("snapshot matched more than one row in %s", table)
		}
		if err := rows.Scan(&out); err != nil {
			f.fail("scan row snapshot in "+table, err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		f.fail("snapshot rows in "+table, err)
	}
	if count == 0 {
		t.Fatalf("snapshot matched no row in %s (%s)", table, where)
	}
	return out
}

// frozenSnapshot renders only the revision's frozen columns (the exact
// trigger-guarded set) as one deterministic text value, so a legal lifecycle
// transition can be proven to have left frozen content byte-identical.
func (f *pgState02Fixture) frozenSnapshot(t *testing.T, id string) string {
	t.Helper()
	var out string
	err := f.iso.QueryRow(f.ctx, `SELECT concat_ws(E'\x1f',
		id, project, explorer_id,
		COALESCE(config::text, '<sql-null>'),
		COALESCE(authoring_bundle::text, '<sql-null>'),
		intent_digest, compilation_receipt_id,
		COALESCE(public_output_contract::text, '<sql-null>'),
		recipe::text, recipe_digest, resolved_schema_digest, source_generation,
		COALESCE(materializations::text, '<sql-null>'),
		COALESCE(emitted_columns::text, '<sql-null>'),
		dataset::text,
		COALESCE(quality_reports::text, '<sql-null>'),
		content_digest, created_by,
		to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS.US'))
	FROM `+f.revisionTable()+` WHERE id = $1`, id).Scan(&out)
	if err != nil {
		f.fail("frozen snapshot of revision "+id, err)
	}
	return out
}

// columnKinds reads jsonb typeof witnesses (or 'missing' for SQL NULL) for
// one row, for literal tri-state assertions.
func (f *pgState02Fixture) columnKinds(t *testing.T, table, where string, args []any, columns ...string) []string {
	t.Helper()
	selects := make([]string, len(columns))
	for i, c := range columns {
		selects[i] = fmt.Sprintf("COALESCE(jsonb_typeof(%s), 'missing')", c)
	}
	rows, err := f.iso.Query(f.ctx, "SELECT "+strings.Join(selects, ", ")+" FROM "+table+" WHERE "+where, args...)
	if err != nil {
		f.fail("read jsonb kinds from "+table, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		targets := make([]string, len(columns))
		scanTargets := make([]any, len(columns))
		for i := range targets {
			scanTargets[i] = &targets[i]
		}
		if err := rows.Scan(scanTargets...); err != nil {
			f.fail("scan jsonb kinds from "+table, err)
		}
		out = append(out, targets...)
	}
	if err := rows.Err(); err != nil {
		f.fail("read jsonb kinds from "+table, err)
	}
	if len(out) != len(columns) {
		t.Fatalf("jsonb kinds matched %d rows, want exactly 1 in %s", len(out), table)
	}
	return out
}

func requirePgErrPGState02(t *testing.T, err error, sqlstate, step string) *pgconn.PgError {
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
