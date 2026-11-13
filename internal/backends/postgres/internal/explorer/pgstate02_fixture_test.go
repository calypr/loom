package postgres

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

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
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
	admin  *pgxpool.Pool
	iso    *pgxpool.Pool
	dbName string
	schema string
	dsn    string
	scrub  func(any) string
	fail   func(step string, err error)
}

func newPGState02Fixture(t *testing.T) *pgState02Fixture {
	t.Helper()
	raw := pgstate02TestPostgresURL(t)

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

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	t.Cleanup(admin.Close)

	dbName := fmt.Sprintf("pgstate02_it_%d_%s", time.Now().UnixNano(), pgstate02RandomHex(t))
	if !pgstate02NameRe.MatchString(dbName) {
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

	schema := fmt.Sprintf("pgstate02_it_%d_%s", time.Now().UnixNano(), pgstate02RandomHex(t))
	if !pgstate02NameRe.MatchString(schema) {
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

	return &pgState02Fixture{ctx: ctx, admin: admin, iso: iso, dbName: dbName, schema: schema, dsn: dsn, scrub: scrub, fail: fail}
}

func (f *pgState02Fixture) receiptTable() string {
	return sanitizeTableRef(f.schema, ReceiptsTable)
}

func (f *pgState02Fixture) revisionTable() string {
	return sanitizeTableRef(f.schema, RevisionsTable)
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
	history, err := OwnerMigrations()
	if err != nil {
		f.fail("owner migrations", err)
	}
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

func (f *pgState02Fixture) rerunMigration(t *testing.T, version int64) {
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
				"SET LOCAL search_path TO "+pgx.Identifier{f.schema}.Sanitize()); err != nil {
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

func requireDistinctBackendsPGState02(t *testing.T, pidA, pidB int32, labelA, labelB string) {
	t.Helper()
	if pidA == pidB {
		t.Fatalf("%s and %s share backend pid %d, want two real PG connections", labelA, labelB, pidA)
	}
}

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
