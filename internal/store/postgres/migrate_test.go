package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func migrationMapFS(files map[string]string) fstest.MapFS {
	out := fstest.MapFS{}
	for name, sql := range files {
		out[name] = &fstest.MapFile{Data: []byte(sql)}
	}
	return out
}

func mustLoadMigrations(t *testing.T, files map[string]string) []Migration {
	t.Helper()
	migrations, err := LoadMigrations(migrationMapFS(files), ".")
	if err != nil {
		t.Fatalf("LoadMigrations: %v", err)
	}
	return migrations
}

func mustEmbeddedMigrations(t *testing.T) []Migration {
	t.Helper()
	migrations, err := EmbeddedMigrations()
	if err != nil {
		t.Fatalf("EmbeddedMigrations: %v", err)
	}
	return migrations
}

func migrationSet(t *testing.T, base []Migration, extra map[string]string) []Migration {
	set := append(append([]Migration(nil), base...), mustLoadMigrations(t, extra)...)
	return set
}

type stubConnSource struct{}

func (stubConnSource) Acquire(context.Context) (*pgxpool.Conn, error) {
	return nil, errors.New("stub source is never used")
}

func TestMigrationLoadParsesChecksumsAndSorts(t *testing.T) {
	const alphaSQL = "CREATE TABLE alpha (id bigint PRIMARY KEY);\n"
	const betaSQL = "CREATE TABLE beta (id bigint);\n"
	got, err := LoadMigrations(migrationMapFS(map[string]string{
		"000002_beta.sql":  betaSQL,
		"000001_alpha.sql": alphaSQL,
	}), ".")
	if err != nil {
		t.Fatalf("LoadMigrations: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("loaded %d migrations, want 2", len(got))
	}
	if got[0].Version != 1 || got[0].Name != "alpha" || got[0].SQL != alphaSQL {
		t.Errorf("first migration = %+v, want version 1 alpha", got[0])
	}
	if got[1].Version != 2 || got[1].Name != "beta" {
		t.Errorf("second migration = %+v, want version 2 beta", got[1])
	}
	if got[0].Checksum != migrationChecksum(alphaSQL) {
		t.Errorf("checksum = %s, want sha256 of exact SQL bytes", got[0].Checksum)
	}
	if len(got[1].Checksum) != 64 || strings.ToLower(got[1].Checksum) != got[1].Checksum {
		t.Errorf("checksum %q is not lowercase 64-char hex", got[1].Checksum)
	}
}

func TestMigrationLoadRejectsInvalidFiles(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
	}{
		{"non-sql file", map[string]string{"README.md": "notes", "000001_alpha.sql": "SELECT 1;"}},
		{"missing underscore", map[string]string{"0001alpha.sql": "SELECT 1;"}},
		{"non-numeric version", map[string]string{"v1_alpha.sql": "SELECT 1;"}},
		{"zero version", map[string]string{"000000_alpha.sql": "SELECT 1;"}},
		{"version overflow", map[string]string{"99999999999999999999_alpha.sql": "SELECT 1;"}},
		{"uppercase name", map[string]string{"000001_Alpha.sql": "SELECT 1;"}},
		{"name with dot", map[string]string{"000001_al pha.sql": "SELECT 1;"}},
		{"duplicate version", map[string]string{"000001_alpha.sql": "SELECT 1;", "000001_beta.sql": "SELECT 2;"}},
		{"duplicate name", map[string]string{"000001_alpha.sql": "SELECT 1;", "000002_alpha.sql": "SELECT 2;"}},
		{"empty sql", map[string]string{"000001_alpha.sql": "  \n\t\n"}},
	} {
		_, err := LoadMigrations(migrationMapFS(tc.files), ".")
		if !errors.Is(err, ErrInvalidMigration) {
			t.Errorf("%s: LoadMigrations = %v, want ErrInvalidMigration", tc.name, err)
		}
	}
	if _, err := LoadMigrations(migrationMapFS(nil), "absent"); !errors.Is(err, ErrInvalidMigration) {
		t.Errorf("missing directory: LoadMigrations = %v, want ErrInvalidMigration", err)
	}
}

func TestEmbeddedMigrationsBootstrapsTheLedger(t *testing.T) {
	migrations := mustEmbeddedMigrations(t)
	if len(migrations) != 1 {
		t.Fatalf("embedded history has %d migrations, want exactly the ledger bootstrap", len(migrations))
	}
	m := migrations[0]
	if m.Version != 1 || m.Name != "ledger" {
		t.Fatalf("bootstrap migration = {Version:%d Name:%q}, want {1 ledger}", m.Version, m.Name)
	}
	if !strings.Contains(m.SQL, LedgerTable) {
		t.Fatalf("bootstrap SQL does not create %s", LedgerTable)
	}
	if m.Checksum != migrationChecksum(m.SQL) {
		t.Errorf("bootstrap checksum does not match its SQL")
	}
}

func TestNewMigrationRunnerRejectsInvalidInputs(t *testing.T) {
	for _, tc := range []struct {
		name       string
		src        ConnSource
		schema     string
		migrations []Migration
		want       error
	}{
		{"nil source", nil, "ok_schema", nil, errors.New("sentinel")},
		{"empty schema", stubConnSource{}, "", nil, ErrInvalidSchemaName},
		{"uppercase schema", stubConnSource{}, "Public", nil, ErrInvalidSchemaName},
		{"dotted schema", stubConnSource{}, "public.other", nil, ErrInvalidSchemaName},
		{"leading digit", stubConnSource{}, "1schema", nil, ErrInvalidSchemaName},
		{"overlong schema", stubConnSource{}, strings.Repeat("a", 64), nil, ErrInvalidSchemaName},
		{"zero version", stubConnSource{}, "ok_schema",
			[]Migration{{Version: 0, Name: "a", SQL: "SELECT 1;"}}, ErrInvalidMigration},
		{"bad name", stubConnSource{}, "ok_schema",
			[]Migration{{Version: 1, Name: "Bad-Name", SQL: "SELECT 1;"}}, ErrInvalidMigration},
		{"empty sql", stubConnSource{}, "ok_schema",
			[]Migration{{Version: 1, Name: "a", SQL: "   "}}, ErrInvalidMigration},
		{"duplicate version", stubConnSource{}, "ok_schema",
			[]Migration{{Version: 2, Name: "a", SQL: "SELECT 1;"}, {Version: 2, Name: "b", SQL: "SELECT 2;"}}, ErrInvalidMigration},
		{"duplicate name", stubConnSource{}, "ok_schema",
			[]Migration{{Version: 1, Name: "a", SQL: "SELECT 1;"}, {Version: 2, Name: "a", SQL: "SELECT 2;"}}, ErrInvalidMigration},
	} {
		_, err := NewMigrationRunner(tc.src, tc.schema, tc.migrations)
		if tc.want == ErrInvalidSchemaName || tc.want == ErrInvalidMigration {
			if !errors.Is(err, tc.want) {
				t.Errorf("%s: NewMigrationRunner = %v, want %v", tc.name, err, tc.want)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: NewMigrationRunner succeeded, want error", tc.name)
		}
	}
}

func TestNewMigrationRunnerNormalizesAndSorts(t *testing.T) {
	runner, err := NewMigrationRunner(stubConnSource{}, "pgstore02_it_x", []Migration{
		{Version: 3, Name: "c", SQL: "SELECT 3;"},
		{Version: 1, Name: "a", SQL: "SELECT 1;"},
	})
	if err != nil {
		t.Fatalf("NewMigrationRunner: %v", err)
	}
	if len(runner.migrations) != 2 || runner.migrations[0].Version != 1 || runner.migrations[1].Version != 3 {
		t.Fatalf("history not sorted: %+v", runner.migrations)
	}
	if runner.migrations[0].Checksum != migrationChecksum("SELECT 1;") {
		t.Errorf("checksum not recomputed from SQL: %s", runner.migrations[0].Checksum)
	}
	if runner.ledgerFQN != `"pgstore02_it_x"."schema_migrations"` {
		t.Errorf("ledgerFQN = %s", runner.ledgerFQN)
	}
}

func TestMigrationAdvisoryLockKey(t *testing.T) {
	first := migrationAdvisoryLockKey("pgstore02_it_x")
	if second := migrationAdvisoryLockKey("pgstore02_it_x"); first != second {
		t.Fatalf("key not stable: %d vs %d", first, second)
	}
	if first == migrationAdvisoryLockKey("pgstore02_it_y") {
		t.Fatal("different schemas share an advisory-lock key")
	}
	if first == 0 {
		t.Fatal("advisory-lock key must be nonzero")
	}
}

// ---------------------------------------------------------------------------
// Real PostgreSQL 18 integration (service-gated by LOOM_TEST_POSTGRES_URL).
// ---------------------------------------------------------------------------

var pgstore02ResourceNamePattern = regexp.MustCompile(`^pgstore02_it_[0-9]+_[0-9a-f]+$`)

// migrationService owns one isolated database and one owned schema inside
// it, both named pgstore02_it_*, created for exactly one test and removed by
// that test's cleanups. Nothing outside these two resources is touched.
type migrationService struct {
	ctx    context.Context
	admin  *pgxpool.Pool // service database
	iso    *pgxpool.Pool // isolated database
	dsn    string        // isolated database DSN (credentials scrubbed on failure)
	schema string
	scrub  func(any) string
	fail   func(step string, err error)
}

func newMigrationService(t *testing.T) *migrationService {
	t.Helper()
	raw := testPostgresURL(t)

	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("LOOM_TEST_POSTGRES_URL must be a postgres:// or postgresql:// URL")
	}

	// Scrub every credential-bearing string from failure output.
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

	dbName := fmt.Sprintf("pgstore02_it_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !pgstore02ResourceNamePattern.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
	t.Cleanup(func() {
		// Drop only the database this test created.
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
	u.Path = "/" + dbName
	dsn := u.String()
	secrets = append(secrets, dsn)

	iso, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fail("connect to isolated database", err)
	}
	t.Cleanup(iso.Close)

	schema := fmt.Sprintf("pgstore02_it_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !pgstore02ResourceNamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	t.Cleanup(func() {
		// Drop only the schema this test created.
		if _, err := iso.Exec(context.WithoutCancel(ctx),
			`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})

	return &migrationService{ctx: ctx, admin: admin, iso: iso, dsn: dsn, schema: schema, scrub: scrub, fail: fail}
}

// openClient opens a second readiness-checked pool against the isolated
// database, so a caller gets its own physical connections.
func (s *migrationService) openClient(t *testing.T) *Client {
	t.Helper()
	client, err := Open(s.ctx, Config{
		DSN:            s.dsn,
		MaxConns:       3,
		MinConns:       1,
		AcquireTimeout: 10 * time.Second,
	})
	if err != nil {
		s.fail("open client pool", err)
	}
	t.Cleanup(client.Close)
	return client
}

func (s *migrationService) newRunner(t *testing.T, src ConnSource, migrations []Migration) *MigrationRunner {
	t.Helper()
	runner, err := NewMigrationRunner(src, s.schema, migrations)
	if err != nil {
		t.Fatalf("NewMigrationRunner: %v", err)
	}
	return runner
}

// readLedger witnesses the ledger through the test's own pool, independent
// of the runner.
func (s *migrationService) readLedger(t *testing.T) []LedgerRecord {
	t.Helper()
	rows, err := s.iso.Query(s.ctx,
		`SELECT version, name, checksum, applied_at FROM "`+s.schema+`".`+LedgerTable+` ORDER BY version`)
	if err != nil {
		// A missing ledger table is an empty history, matching the runner.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P01" {
			return nil
		}
		s.fail("witness ledger", err)
	}
	defer rows.Close()
	var out []LedgerRecord
	for rows.Next() {
		var rec LedgerRecord
		if err := rows.Scan(&rec.Version, &rec.Name, &rec.Checksum, &rec.AppliedAt); err != nil {
			s.fail("witness ledger scan", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		s.fail("witness ledger rows", err)
	}
	return out
}

func (s *migrationService) regclass(t *testing.T, relation string) string {
	t.Helper()
	var found *string
	err := s.iso.QueryRow(s.ctx,
		`SELECT to_regclass($1)::text`, s.schema+"."+relation).Scan(&found)
	if err != nil {
		s.fail("witness to_regclass "+relation, err)
	}
	if found == nil {
		return ""
	}
	return *found
}

func (s *migrationService) mustMigrate(t *testing.T, runner *MigrationRunner) MigrateResult {
	t.Helper()
	result, err := runner.Migrate(s.ctx)
	if err != nil {
		s.fail("Migrate", err)
	}
	return result
}

func assertLedgerEquals(t *testing.T, got []LedgerRecord, want []Migration) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("ledger has %d rows, want %d: %+v", len(got), len(want), got)
	}
	seen := make(map[int64]struct{}, len(got))
	for i, rec := range got {
		if rec.Version != want[i].Version || rec.Name != want[i].Name || rec.Checksum != want[i].Checksum {
			t.Fatalf("ledger row %d = %+v, want {Version:%d Name:%q Checksum:%q}",
				i, rec, want[i].Version, want[i].Name, want[i].Checksum)
		}
		if _, dup := seen[rec.Version]; dup {
			t.Fatalf("ledger recorded version %d twice: %+v", rec.Version, got)
		}
		seen[rec.Version] = struct{}{}
	}
}

func TestMigrationConcurrentCallersApplyExactlyOnce(t *testing.T) {
	svc := newMigrationService(t)
	set := migrationSet(t, mustEmbeddedMigrations(t), map[string]string{
		"000002_alpha.sql":  "CREATE TABLE alpha (id bigint PRIMARY KEY);\n",
		"000003_sleepy.sql": "SELECT pg_catalog.pg_sleep(0.75);\nCREATE TABLE beta (id bigint);\n",
	})

	clientA := svc.openClient(t)
	clientB := svc.openClient(t)

	// Prove the two callers run on separate physical connections.
	pidOf := func(client *Client, tag string) int32 {
		conn, err := client.Acquire(svc.ctx)
		if err != nil {
			svc.fail("acquire "+tag+" probe connection", err)
		}
		defer conn.Release()
		var pid int32
		if err := conn.QueryRow(svc.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			svc.fail("read "+tag+" backend pid", err)
		}
		return pid
	}
	if pidA, pidB := pidOf(clientA, "caller A"), pidOf(clientB, "caller B"); pidA == pidB {
		t.Fatalf("both callers share backend pid %d; want separate connections", pidA)
	}

	runnerA := svc.newRunner(t, clientA, set)
	runnerB := svc.newRunner(t, clientB, set)

	start := make(chan struct{})
	var results [2]MigrateResult
	var errs [2]error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		results[0], errs[0] = runnerA.Migrate(svc.ctx)
	}()
	go func() {
		defer wg.Done()
		<-start
		results[1], errs[1] = runnerB.Migrate(svc.ctx)
	}()
	close(start)

	finished := make(chan struct{})
	go func() {
		wg.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(90 * time.Second):
		t.Fatal("concurrent Migrate calls did not finish in 90s; the advisory lock may be deadlocked")
	}
	for i, err := range errs {
		if err != nil {
			svc.fail(fmt.Sprintf("concurrent caller %c", 'A'+i), err)
		}
	}
	if !reflect.DeepEqual(results[0], results[1]) {
		t.Errorf("callers disagree:\nA: %+v\nB: %+v", results[0], results[1])
	}
	if results[0].LatestVersion != 3 || len(results[0].Applied) != 3 {
		t.Fatalf("result = {LatestVersion:%d Applied:%d}, want {3 3}",
			results[0].LatestVersion, len(results[0].Applied))
	}
	// Exactly once: one ledger row per migration, no version twice.
	assertLedgerEquals(t, svc.readLedger(t), set)
	if got := svc.regclass(t, "alpha"); got == "" {
		t.Error("alpha absent from the owned schema")
	}
	if got := svc.regclass(t, "beta"); got == "" {
		t.Error("beta absent from the owned schema")
	}
}

func TestMigrationIdempotentReapplyReturnsSameState(t *testing.T) {
	svc := newMigrationService(t)
	set := migrationSet(t, mustEmbeddedMigrations(t), map[string]string{
		"000002_gamma.sql": "CREATE TABLE gamma (id int);\n",
		"000003_note.sql":  "ALTER TABLE gamma ADD COLUMN note text;\nCREATE INDEX gamma_note_idx ON gamma (note);\n",
	})
	runner := svc.newRunner(t, svc.openClient(t), set)

	first := svc.mustMigrate(t, runner)
	second := svc.mustMigrate(t, runner)
	third := svc.mustMigrate(t, runner)

	if !reflect.DeepEqual(first, second) || !reflect.DeepEqual(second, third) {
		t.Fatalf("re-apply changed the result:\n1st: %+v\n2nd: %+v\n3rd: %+v", first, second, third)
	}
	if first.LatestVersion != 3 || len(first.Applied) != 3 {
		t.Fatalf("result = {LatestVersion:%d Applied:%d}, want {3 3}", first.LatestVersion, len(first.Applied))
	}
	assertLedgerEquals(t, svc.readLedger(t), set)
}

func TestMigrationEditedChecksumFailsBeforeApply(t *testing.T) {
	svc := newMigrationService(t)
	const plainSQL = "CREATE TABLE keep_me (id int);\n"
	const editedSQL = "CREATE TABLE keep_me (id int);\nCREATE TABLE edited_marker (id int);\n"
	applied := migrationSet(t, mustEmbeddedMigrations(t), map[string]string{"000002_plain.sql": plainSQL})
	edited := migrationSet(t, mustEmbeddedMigrations(t), map[string]string{"000002_plain.sql": editedSQL})

	svc.mustMigrate(t, svc.newRunner(t, svc.openClient(t), applied))

	result, err := svc.newRunner(t, svc.openClient(t), edited).Migrate(svc.ctx)
	if !errors.Is(err, ErrMigrationChecksumMismatch) {
		t.Fatalf("Migrate with edited history = %v, want ErrMigrationChecksumMismatch", err)
	}
	if !reflect.DeepEqual(result, MigrateResult{}) {
		t.Errorf("failed Migrate returned result %+v, want zero value", result)
	}
	var mismatch *MigrationChecksumMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("error type = %T, want *MigrationChecksumMismatchError", err)
	}
	if mismatch.Version != 2 || mismatch.Name != "plain" {
		t.Errorf("mismatch = %+v, want version 2 name plain", mismatch)
	}
	if mismatch.LedgerChecksum != migrationChecksum(plainSQL) ||
		mismatch.ComputedChecksum != migrationChecksum(editedSQL) {
		t.Errorf("mismatch checksums = {ledger:%s computed:%s}, want the original and edited sha256",
			mismatch.LedgerChecksum, mismatch.ComputedChecksum)
	}
	// Refused before application: the edited body never ran and the ledger
	// still records the original checksum.
	if got := svc.regclass(t, "edited_marker"); got != "" {
		t.Errorf("edited_marker exists (%s); edited migration was applied", got)
	}
	assertLedgerEquals(t, svc.readLedger(t), applied)
}

func TestMigrationFutureSchemaVersionFailsBeforeApply(t *testing.T) {
	svc := newMigrationService(t)
	full := migrationSet(t, mustEmbeddedMigrations(t), map[string]string{
		"000002_alpha.sql": "CREATE TABLE alpha (id int);\n",
		"000003_beta.sql":  "CREATE TABLE beta (id int);\n",
	})
	svc.mustMigrate(t, svc.newRunner(t, svc.openClient(t), full))

	// This binary only knows versions 1..2 while the ledger holds 3.
	outdated := migrationSet(t, mustEmbeddedMigrations(t), map[string]string{
		"000002_alpha.sql": "CREATE TABLE alpha (id int);\n",
	})
	_, err := svc.newRunner(t, svc.openClient(t), outdated).Migrate(svc.ctx)
	if !errors.Is(err, ErrFutureSchemaVersion) {
		t.Fatalf("Migrate with a future ledger = %v, want ErrFutureSchemaVersion", err)
	}
	var future *FutureSchemaVersionError
	if !errors.As(err, &future) {
		t.Fatalf("error type = %T, want *FutureSchemaVersionError", err)
	}
	if future.LedgerVersion != 3 || future.LedgerName != "beta" || future.LatestEmbedded != 2 {
		t.Errorf("future-version error = %+v, want {LedgerVersion:3 LedgerName:beta LatestEmbedded:2}", future)
	}
	// Nothing was applied or recorded; the ledger is untouched.
	assertLedgerEquals(t, svc.readLedger(t), full)
}

func TestMigrationMissingEmbeddedFileFails(t *testing.T) {
	svc := newMigrationService(t)
	applied := migrationSet(t, mustEmbeddedMigrations(t), map[string]string{
		"000002_alpha.sql": "CREATE TABLE alpha (id int);\n",
		"000003_beta.sql":  "CREATE TABLE beta (id int);\n",
	})
	svc.mustMigrate(t, svc.newRunner(t, svc.openClient(t), applied))

	// Version 3 vanished from the history; version 4 would be pending.
	shuffled := migrationSet(t, mustEmbeddedMigrations(t), map[string]string{
		"000002_alpha.sql": "CREATE TABLE alpha (id int);\n",
		"000004_delta.sql": "CREATE TABLE delta (id int);\n",
	})
	_, err := svc.newRunner(t, svc.openClient(t), shuffled).Migrate(svc.ctx)
	if !errors.Is(err, ErrMigrationMissing) {
		t.Fatalf("Migrate with a vanished migration = %v, want ErrMigrationMissing", err)
	}
	var missing *MissingEmbeddedMigrationError
	if !errors.As(err, &missing) || missing.Version != 3 || missing.LedgerName != "beta" {
		t.Fatalf("error = %v (%T), want MissingEmbeddedMigrationError{3 beta}", err, err)
	}
	if got := svc.regclass(t, "delta"); got != "" {
		t.Errorf("delta exists (%s); pending migration ran despite the missing file", got)
	}
}

func TestMigrationOutOfOrderFails(t *testing.T) {
	svc := newMigrationService(t)
	applied := migrationSet(t, mustEmbeddedMigrations(t), map[string]string{
		"000002_alpha.sql": "CREATE TABLE alpha (id int);\n",
		"000004_delta.sql": "CREATE TABLE delta (id int);\n",
	})
	svc.mustMigrate(t, svc.newRunner(t, svc.openClient(t), applied))

	amended := migrationSet(t, mustEmbeddedMigrations(t), map[string]string{
		"000002_alpha.sql": "CREATE TABLE alpha (id int);\n",
		"000003_beta.sql":  "CREATE TABLE beta (id int);\n",
		"000004_delta.sql": "CREATE TABLE delta (id int);\n",
	})
	_, err := svc.newRunner(t, svc.openClient(t), amended).Migrate(svc.ctx)
	if !errors.Is(err, ErrMigrationOutOfOrder) {
		t.Fatalf("Migrate with an out-of-order file = %v, want ErrMigrationOutOfOrder", err)
	}
	var outOfOrder *OutOfOrderMigrationError
	if !errors.As(err, &outOfOrder) || outOfOrder.Version != 3 || outOfOrder.LatestRecord != 4 {
		t.Fatalf("error = %v (%T), want OutOfOrderMigrationError{Version:3 LatestRecord:4}", err, err)
	}
	if got := svc.regclass(t, "beta"); got != "" {
		t.Errorf("beta exists (%s); out-of-order migration was applied", got)
	}
}

func TestMigrationFailingDDLRollsBackAndKeepsLedgerIntact(t *testing.T) {
	svc := newMigrationService(t)
	failing := migrationSet(t, mustEmbeddedMigrations(t), map[string]string{
		"000002_ok.sql": "CREATE TABLE audit_ok (id int);\nINSERT INTO audit_ok VALUES (1);\n",
		"000003_bad.sql": "CREATE TABLE rollback_probe (id int);\n" +
			"INSERT INTO no_such_table VALUES (1);\n",
	})
	result, err := svc.newRunner(t, svc.openClient(t), failing).Migrate(svc.ctx)
	if !errors.Is(err, ErrMigrationApply) {
		t.Fatalf("Migrate with a failing migration = %v, want ErrMigrationApply", err)
	}
	if !reflect.DeepEqual(result, MigrateResult{}) {
		t.Errorf("failed Migrate returned result %+v, want zero value", result)
	}
	var applyErr *MigrationApplyError
	if !errors.As(err, &applyErr) {
		t.Fatalf("error type = %T, want *MigrationApplyError", err)
	}
	if applyErr.Version != 3 || applyErr.Name != "bad" || applyErr.Op != "apply" {
		t.Errorf("apply error = %+v, want {Version:3 Name:bad Op:apply}", applyErr)
	}
	// The failing statement's class must stay inspectable through the chain.
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42P01" {
		t.Errorf("apply error does not chain the undefined-table PgError: %v", err)
	}

	// The failing migration's DDL rolled back; the prior committed migration
	// and its ledger rows are intact; nothing was recorded for version 3.
	if got := svc.regclass(t, "rollback_probe"); got != "" {
		t.Errorf("rollback_probe exists (%s); failing migration DDL was not rolled back", got)
	}
	if got := svc.regclass(t, "audit_ok"); got == "" {
		t.Error("audit_ok absent; the prior committed migration was disturbed")
	}
	prior := migrationSet(t, mustEmbeddedMigrations(t), map[string]string{
		"000002_ok.sql": "CREATE TABLE audit_ok (id int);\nINSERT INTO audit_ok VALUES (1);\n",
	})
	assertLedgerEquals(t, svc.readLedger(t), prior)

	// Correcting the history converges: version 3 applies, version 2 is not
	// re-run (its inserted row count proves exactly-once).
	fixed := migrationSet(t, mustEmbeddedMigrations(t), map[string]string{
		"000002_ok.sql":  "CREATE TABLE audit_ok (id int);\nINSERT INTO audit_ok VALUES (1);\n",
		"000003_bad.sql": "CREATE TABLE rollback_probe (id int);\n",
	})
	recovered := svc.mustMigrate(t, svc.newRunner(t, svc.openClient(t), fixed))
	if recovered.LatestVersion != 3 {
		t.Fatalf("recovered LatestVersion = %d, want 3", recovered.LatestVersion)
	}
	if got := svc.regclass(t, "rollback_probe"); got == "" {
		t.Error("rollback_probe absent after the corrected migration applied")
	}
	assertLedgerEquals(t, svc.readLedger(t), fixed)
	var auditRows int
	if err := svc.iso.QueryRow(svc.ctx, `SELECT count(*) FROM "`+svc.schema+`".audit_ok`).Scan(&auditRows); err != nil {
		svc.fail("count audit_ok rows", err)
	}
	if auditRows != 1 {
		t.Errorf("audit_ok has %d rows, want 1: migration 000002 was replayed", auditRows)
	}
}

// TestMigrationUnconfirmedUnlockDiscardsConnection covers the
// discardPoolConn branch: a migration whose SQL surrenders the session's
// advisory locks makes the runner's confirming unlock report false, so the
// connection's lock state is unproven and must be discarded rather than
// pooled. The run must surface the unconfirmed release as an error even
// though the migration itself applied and was recorded, and the pool must
// not retain the poisoned connection.
func TestMigrationUnconfirmedUnlockDiscardsConnection(t *testing.T) {
	svc := newMigrationService(t)
	set := migrationSet(t, mustEmbeddedMigrations(t), map[string]string{
		"000002_surrender.sql": "SELECT pg_catalog.pg_advisory_unlock_all();\nCREATE TABLE surrendered (id int);\n",
	})
	client := svc.openClient(t)

	_, err := svc.newRunner(t, client, set).Migrate(svc.ctx)
	if err == nil || !strings.Contains(err.Error(), "already unlocked") {
		t.Fatalf("Migrate with a lock-surrendering migration = %v, want already-unlocked release error", err)
	}
	// The migration still applied and recorded exactly once.
	assertLedgerEquals(t, svc.readLedger(t), set)
	// The unproven connection was discarded, not returned to the pool.
	if acquired := client.pool.Stat().AcquiredConns(); acquired != 0 {
		t.Errorf("pool still holds %d acquired connections; the poisoned connection was not discarded", acquired)
	}
	// The pool is still usable for migrations on this schema.
	again := svc.mustMigrate(t, svc.newRunner(t, client, set))
	if again.LatestVersion != 2 {
		t.Fatalf("follow-up LatestVersion = %d, want 2", again.LatestVersion)
	}
}

// TestMigrationPanicUnderLockReleasesLockAndLeaksNothing pins the
// panic-safety requirement: a panic inside the locked section must still
// release the session-level advisory lock and return (or discard) the
// connection, so a recovered panic upstream cannot permanently wedge every
// future Migrate on the schema or leak a pool connection. The panic itself
// must still propagate to the caller.
func TestMigrationPanicUnderLockReleasesLockAndLeaksNothing(t *testing.T) {
	svc := newMigrationService(t)
	set := migrationSet(t, mustEmbeddedMigrations(t), map[string]string{
		"000002_item.sql": "CREATE TABLE panicked_on (id int);\n",
	})
	injected := errors.New("injected panic under the migration lock")

	client := svc.openClient(t)
	runner := svc.newRunner(t, client, set)
	runner.panicHook = func() { panic(injected) }

	panicked := func() (recovered any) {
		defer func() { recovered = recover() }()
		_, _ = runner.Migrate(svc.ctx)
		return nil
	}()
	if recovered, ok := panicked.(error); !ok || !errors.Is(recovered, injected) {
		t.Fatalf("panic under the lock was not propagated: %v", panicked)
	}
	// Nothing was applied: the panic landed before any ledger work.
	if rows := svc.readLedger(t); len(rows) != 0 {
		t.Errorf("ledger has %d rows after the injected panic, want 0", len(rows))
	}
	// The connection was returned or discarded, never orphaned in the
	// acquired state.
	if acquired := client.pool.Stat().AcquiredConns(); acquired != 0 {
		t.Errorf("pool still holds %d acquired connections after the panic", acquired)
	}
	// The lock is free: a fresh connection can run the whole history now.
	recovered := svc.mustMigrate(t, svc.newRunner(t, svc.openClient(t), set))
	if recovered.LatestVersion != 2 {
		t.Fatalf("post-panic LatestVersion = %d, want 2", recovered.LatestVersion)
	}
	assertLedgerEquals(t, svc.readLedger(t), set)
}

// waitAdvisoryLock blocks until an advisory lock on the isolated database
// appears (granted=wait) or disappears (granted=return) in pg_locks, so the
// contended-acquisition test starts its loser exactly while the holder is
// inside the locked section.
func waitAdvisoryLock(t *testing.T, svc *migrationService, present bool) {
	t.Helper()
	query := `SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND granted
		AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`
	deadline := time.Now().Add(15 * time.Second)
	for {
		var held int64
		if err := svc.iso.QueryRow(svc.ctx, query).Scan(&held); err != nil {
			svc.fail("probe advisory locks", err)
		}
		if (held > 0) == present {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("advisory lock presence did not reach %v within 15s (last count %d)", present, held)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestMigrationContendedAcquireDeadlineIsNamed drives the two-connection
// contended-acquisition path: a holder migrates a sleep-bearing migration
// while a second, separate connection attempts Migrate with a short context
// deadline. The loser must fail with the named acquisition error (chained to
// its deadline/cancellation cause), the racing connection must be closed
// rather than pooled with an unknown lock state, and after the holder
// finishes a fresh caller must acquire the lock immediately -- no residue
// from the aborted attempt.
func TestMigrationContendedAcquireDeadlineIsNamed(t *testing.T) {
	svc := newMigrationService(t)
	set := migrationSet(t, mustEmbeddedMigrations(t), map[string]string{
		"000002_slow.sql": "SELECT pg_catalog.pg_sleep(3);\nCREATE TABLE slow_one (id int);\n",
	})

	holderClient := svc.openClient(t)
	holderRunner := svc.newRunner(t, holderClient, set)
	holderDone := make(chan error, 1)
	go func() {
		_, err := holderRunner.Migrate(svc.ctx)
		holderDone <- err
	}()
	waitAdvisoryLock(t, svc, true)

	loserClient := svc.openClient(t)
	loserRunner := svc.newRunner(t, loserClient, set)
	shortCtx, cancelShort := context.WithTimeout(svc.ctx, 300*time.Millisecond)
	defer cancelShort()
	_, err := loserRunner.Migrate(shortCtx)
	if !errors.Is(err, ErrMigrationLockAcquire) {
		t.Fatalf("contended Migrate = %v, want ErrMigrationLockAcquire", err)
	}
	var pgErr *pgconn.PgError
	if !errors.Is(err, context.DeadlineExceeded) &&
		!(errors.As(err, &pgErr) && pgErr.Code == "57014") {
		t.Fatalf("acquisition error does not chain a deadline or cancellation cause: %v", err)
	}
	// The racing connection was discarded, not pooled with unknown lock
	// state: the loser's pool holds nothing acquired.
	if acquired := loserClient.pool.Stat().AcquiredConns(); acquired != 0 {
		t.Errorf("loser pool still holds %d acquired connections; racing connection was not discarded", acquired)
	}

	// The holder completes normally on its own connection.
	if holderErr := <-holderDone; holderErr != nil {
		svc.fail("holder Migrate", holderErr)
	}

	// No lock residue: a brand-new caller acquires immediately and sees the
	// applied history.
	started := time.Now()
	fresh := svc.mustMigrate(t, svc.newRunner(t, svc.openClient(t), set))
	if waited := time.Since(started); waited > 2*time.Second {
		t.Errorf("fresh Migrate waited %v after the holder finished; the aborted attempt left lock residue", waited)
	}
	if fresh.LatestVersion != 2 {
		t.Fatalf("fresh LatestVersion = %d, want 2", fresh.LatestVersion)
	}
	// The loser's pool recovered and can still migrate on this schema.
	if _, err := loserRunner.Migrate(svc.ctx); err != nil {
		svc.fail("loser client retry after deadline", err)
	}
	assertLedgerEquals(t, svc.readLedger(t), set)
}

func TestMigrationSchemaMustExist(t *testing.T) {
	svc := newMigrationService(t)
	runner, err := NewMigrationRunner(svc.openClient(t), "pgstore02_it_missing", mustEmbeddedMigrations(t))
	if err != nil {
		t.Fatalf("NewMigrationRunner: %v", err)
	}
	if _, err := runner.Migrate(svc.ctx); !errors.Is(err, ErrInvalidSchemaName) {
		t.Fatalf("Migrate into a missing schema = %v, want ErrInvalidSchemaName", err)
	}
}

func TestMigrationEmbeddedBootstrapAppliesIdempotently(t *testing.T) {
	svc := newMigrationService(t)
	first, err := MigrateSchema(svc.ctx, svc.openClient(t), svc.schema)
	if err != nil {
		svc.fail("MigrateSchema", err)
	}
	if first.LatestVersion != 1 || len(first.Applied) != 1 {
		t.Fatalf("bootstrap result = %+v, want one version-1 row", first)
	}
	second, err := MigrateSchema(svc.ctx, svc.openClient(t), svc.schema)
	if err != nil {
		svc.fail("re-apply MigrateSchema", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("bootstrap re-apply changed the result:\n1st: %+v\n2nd: %+v", first, second)
	}
	assertLedgerEquals(t, svc.readLedger(t), mustEmbeddedMigrations(t))
}

// TestMigrationNoLeftovers runs after every other migration test in this
// file (Go runs a package's tests in source order, and each test's cleanups
// finish before the next test starts). It asserts that the suite left zero
// owned resources behind: no pgstore02_it_* database on the service and no
// pgstore02_it_* schema in the service database. A hit here means an earlier
// run died before its cleanups converged; drop only resources carrying this
// ticket's unique prefix.
func TestMigrationNoLeftovers(t *testing.T) {
	raw := testPostgresURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect to test service: %v", err)
	}
	defer admin.Close()

	databases := leftoverNames(t, admin, ctx,
		`SELECT datname FROM pg_database WHERE datname LIKE 'pgstore02_it_%' ORDER BY datname`)
	if len(databases) != 0 {
		t.Errorf("leftover isolated databases: %v", databases)
	}
	schemas := leftoverNames(t, admin, ctx,
		`SELECT n.nspname FROM pg_namespace n WHERE n.nspname LIKE 'pgstore02_it_%' ORDER BY n.nspname`)
	if len(schemas) != 0 {
		t.Errorf("leftover owned schemas in the service database: %v", schemas)
	}
}

func leftoverNames(t *testing.T, pool *pgxpool.Pool, ctx context.Context, query string) []string {
	t.Helper()
	rows, err := pool.Query(ctx, query)
	if err != nil {
		t.Fatalf("leftover probe %q: %v", query, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("leftover probe scan: %v", err)
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("leftover probe rows: %v", err)
	}
	return out
}
