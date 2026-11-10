package server

// Live PostgreSQL 18 wiring tests for sanitized recipe Explain
// (pg-exec-10; check-2: go test ./internal/server -run PostgresExplain with
// LOOM_TEST_POSTGRES_URL). Offline tests pin the fail-closed wiring
// contract; live tests drive the real store pool, the shared DataFrame
// PostgreSQL row executor, and the execution sanitizer against the real
// PG18 server with hostile sentinels in identifiers, statement literals,
// and bound values.
//
// Fixtures are uniquely owned as verify_exec10w_* and dropped by the test
// that created them; TestZZ is ownership-scoped (exact registered names and
// own prefix only, never a global pg_database count). PG-mutating runs hold
// a session-level advisory lock so concurrent cross-package witnesses
// serialize.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	storepg "github.com/calypr/loom/internal/store/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	exec10wPrefix  = "verify_exec10w_"
	exec10wNameFmt = exec10wPrefix + "%d_%s"
)

var exec10wNamePattern = regexp.MustCompile(`^verify_exec10w_[0-9]+_[0-9a-f]+$`)

const (
	exec10wTable   = "pat_SENTINEL-TBL-9c14"
	exec10wIndex   = "pat_resource_seq_SENTINEL-IDX-4e02"
	exec10wColumn  = "note_SENTINEL-COL-7a51"
	exec10wLiteral = "SENTINEL-LIT-bf23"
	exec10wBind    = "SENTINEL-BIND-13ad"
)

func exec10wSentinels() []string {
	return []string{exec10wTable, exec10wIndex, exec10wColumn, exec10wLiteral, exec10wBind}
}

var (
	exec10wOwnedMu   sync.Mutex
	exec10wOwnedDBs  = map[string]string{}
	exec10wOwnedRole = map[string]bool{}
)

func exec10wRegisterDB(db, schema string) {
	exec10wOwnedMu.Lock()
	defer exec10wOwnedMu.Unlock()
	exec10wOwnedDBs[db] = schema
}

func exec10wRegisterRole(role string) {
	exec10wOwnedMu.Lock()
	defer exec10wOwnedMu.Unlock()
	exec10wOwnedRole[role] = true
}

func exec10wSnapshot() (map[string]string, map[string]bool) {
	exec10wOwnedMu.Lock()
	defer exec10wOwnedMu.Unlock()
	dbs := make(map[string]string, len(exec10wOwnedDBs))
	for k, v := range exec10wOwnedDBs {
		dbs[k] = v
	}
	roles := make(map[string]bool, len(exec10wOwnedRole))
	for k, v := range exec10wOwnedRole {
		roles[k] = v
	}
	return dbs, roles
}

var (
	exec10wBaselineOnce  sync.Once
	exec10wBaselineDBs   []string
	exec10wBaselineRoles []string
	exec10wBaselineErr   error
)

func exec10wCaptureBaseline(raw string) {
	exec10wBaselineOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		admin, err := pgxpool.New(ctx, raw)
		if err != nil {
			exec10wBaselineErr = fmt.Errorf("connect for baseline: %w", err)
			return
		}
		defer admin.Close()
		rows, err := admin.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE 'verify\\_exec10w\\_%' ESCAPE '\\'")
		if err != nil {
			exec10wBaselineErr = fmt.Errorf("read database baseline: %w", err)
			return
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				exec10wBaselineErr = fmt.Errorf("scan database baseline: %w", err)
				return
			}
			exec10wBaselineDBs = append(exec10wBaselineDBs, name)
		}
		rows.Close()
		roleRows, err := admin.Query(ctx, "SELECT rolname FROM pg_roles WHERE rolname LIKE 'verify\\_exec10w\\_%' ESCAPE '\\'")
		if err != nil {
			exec10wBaselineErr = fmt.Errorf("read role baseline: %w", err)
			return
		}
		for roleRows.Next() {
			var name string
			if err := roleRows.Scan(&name); err != nil {
				roleRows.Close()
				exec10wBaselineErr = fmt.Errorf("scan role baseline: %w", err)
				return
			}
			exec10wBaselineRoles = append(exec10wBaselineRoles, name)
		}
		roleRows.Close()
	})
}

func exec10wURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 Explain wiring integration tests")
	}
	return raw
}

func exec10wRandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return hex.EncodeToString(b[:])
}

type exec10wService struct {
	ctx      context.Context
	iso      *pgxpool.Pool
	client   *storepg.Client
	database string
	dsn      string
	schema   string
	secrets  []string
	fail     func(step string, err error)
}

func newExec10wService(t *testing.T) *exec10wService {
	t.Helper()
	raw := exec10wURL(t)
	exec10wCaptureBaseline(raw)
	if exec10wBaselineErr != nil {
		t.Fatalf("capture pg_database/pg_roles baseline: %v", exec10wBaselineErr)
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("LOOM_TEST_POSTGRES_URL must be a postgres:// or postgresql:// URL")
	}
	secrets := []string{raw}
	if u.User != nil {
		secrets = append(secrets, u.User.String())
		if pw, ok := u.User.Password(); ok && pw != "" {
			secrets = append(secrets, pw)
		}
	}
	fail := func(step string, err error) {
		text := fmt.Sprintf("%v", err)
		for _, secret := range secrets {
			if secret != "" {
				text = strings.ReplaceAll(text, secret, "<redacted>")
			}
		}
		t.Fatalf("%s: %s", step, text)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	t.Cleanup(admin.Close)
	lockConn, err := admin.Acquire(ctx)
	if err != nil {
		fail("acquire serialization connection", err)
	}
	if _, err := lockConn.Exec(ctx, `SELECT pg_advisory_lock(hashtext('loom/pg-exec-10/live-mutation')::bigint)`); err != nil {
		lockConn.Release()
		fail("take PG mutation serialization lock", err)
	}
	t.Cleanup(func() {
		_, _ = lockConn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(hashtext('loom/pg-exec-10/live-mutation')::bigint)`)
		lockConn.Release()
	})

	dbName := fmt.Sprintf(exec10wNameFmt, time.Now().UnixNano(), exec10wRandomHex(t))
	if !exec10wNamePattern.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
	var created int64
	if err := admin.QueryRow(ctx, "SELECT count(*) FROM pg_database WHERE datname = $1", dbName).Scan(&created); err != nil {
		fail("verify isolated database creation", err)
	}
	if created != 1 {
		t.Fatalf("isolated database %q did not verifiably come into existence", dbName)
	}
	exec10wRegisterDB(dbName, "")
	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(ctx),
			`DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %s", err)
		}
	})

	if q := u.Query(); q.Get("dbname") != "" {
		q.Del("dbname")
		u.RawQuery = q.Encode()
	}
	u.Path = "/" + dbName
	dsn := u.String()
	secrets = append(secrets, dsn, dbName)

	// Production pool path: the readiness-checked store pool refuses any
	// server that is not PostgreSQL 18.
	client, err := storepg.Open(ctx, storepg.Config{DSN: dsn, MaxConns: 4, MinConns: 1, AcquireTimeout: 10 * time.Second})
	if err != nil {
		fail("open readiness-checked PostgreSQL 18 pool", err)
	}
	t.Cleanup(client.Close)

	iso, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fail("connect fixture writer", err)
	}
	t.Cleanup(iso.Close)

	schema := fmt.Sprintf(exec10wNameFmt, time.Now().UnixNano(), exec10wRandomHex(t))
	if !exec10wNamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	exec10wRegisterDB(dbName, schema)
	t.Cleanup(func() {
		if _, err := iso.Exec(context.WithoutCancel(ctx),
			`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", err)
		}
	})
	return &exec10wService{ctx: ctx, iso: iso, client: client, database: dbName, dsn: dsn, schema: schema, secrets: append(secrets, schema), fail: fail}
}

func (s *exec10wService) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := s.iso.Exec(s.ctx, sql, args...); err != nil {
		s.fail("fixture exec", err)
	}
}

func (s *exec10wService) seedFixture(t *testing.T) string {
	t.Helper()
	table := `"` + s.schema + `"."` + exec10wTable + `"`
	s.exec(t, `CREATE TABLE `+table+` (resource_seq bigint, "`+exec10wColumn+`" text)`)
	s.exec(t, `CREATE INDEX "`+exec10wIndex+`" ON `+table+` (resource_seq)`)
	s.exec(t, `INSERT INTO `+table+` SELECT i::bigint, 'note-' || i::text FROM generate_series(1, 500) AS i`)
	s.exec(t, `ANALYZE `+table)
	return table
}

func (s *exec10wService) assertSentinelFree(t *testing.T, surface, value string) {
	t.Helper()
	for _, sentinel := range exec10wSentinels() {
		if strings.Contains(value, sentinel) {
			t.Fatalf("%s leaks hostile sentinel %q", surface, sentinel)
		}
	}
	for _, secret := range s.secrets {
		if secret != "" && strings.Contains(value, secret) {
			t.Fatalf("%s leaks connection/schema secret material", surface)
		}
	}
}

// Offline: the wiring surface fails closed, with types, and never crosses
// the backend-tag boundary.
func TestPostgresExplainWiringNilExecutorFailsClosed(t *testing.T) {
	compiledCommand, err := command.NewPostgreSQLCommand(`SELECT id FROM t WHERE x = $1`,
		[]command.PostgreSQLParameter{{Type: command.PostgreSQLParameterText, Value: "v"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = explainPostgreSQLCompiledQuery(context.Background(), nil, dataframeexecution.CompiledQuery{Command: compiledCommand})
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok {
		t.Fatalf("unwired PostgreSQL Explain must fail with a typed error: %v", err)
	}
	if userErr.Code() != string(dataframeerrors.CodeBackendUnavailable) {
		t.Fatalf("code: %s", userErr.Code())
	}
	if userErr.Retryable() {
		t.Fatal("an unwired runtime is not retryable until the runtime exists")
	}
	details, _ := json.Marshal(userErr.Details())
	if !strings.Contains(string(details), "postgresql") {
		t.Fatalf("details must name the backend: %s", details)
	}
	if strings.Contains(err.Error(), "arangodb") || strings.Contains(strings.ToLower(err.Error()), "fallback") && !strings.Contains(err.Error(), "never") {
		t.Fatalf("failure must not mention or offer an Arango fallback: %v", err)
	}
}

func TestPostgresExplainWiringCrossTagRefusals(t *testing.T) {
	// The Arango policy surface refuses a PostgreSQL-tagged command through
	// closed dispatch before touching the client (nil client would panic if
	// the driver call were reached).
	pgCommand, err := command.NewPostgreSQLCommand(`SELECT 1`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := explainCompiledQuery(context.Background(), nil, dataframeexecution.CompiledQuery{Command: pgCommand}); !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
		t.Fatalf("PG-tagged command into the Arango surface: %v", err)
	}
	// The EXPLAIN runner builder refuses a nil querier rather than
	// inventing a connection.
	if _, err := newPostgreSQLPlanJSONExecutor(nil); err == nil {
		t.Fatal("nil querier must fail closed")
	}
	// The execution boundary refuses an AQL-tagged command before the
	// runner is created.
	aqlCommand, err := command.NewAQLCommand("FOR d IN c RETURN d", nil)
	if err != nil {
		t.Fatal(err)
	}
	querierCalled := false
	runner, err := newPostgreSQLPlanJSONExecutor(countingQuerier{called: &querierCalled})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := explainPostgreSQLCompiledQuery(context.Background(), runner, dataframeexecution.CompiledQuery{Command: aqlCommand}); !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
		t.Fatalf("AQL-tagged command into the PostgreSQL surface: %v", err)
	}
	if querierCalled {
		t.Fatal("cross-tag command reached the querier")
	}
}

type countingQuerier struct{ called *bool }

func (q countingQuerier) Query(context.Context, string, ...any) (pgx.Rows, error) {
	*q.called = true
	return nil, errors.New("unexpected querier call")
}

// live: the server-side wiring over the readiness-checked store pool.
func TestPostgresExplainWiringLivePG18SanitizedPlan(t *testing.T) {
	s := newExec10wService(t)
	table := s.seedFixture(t)
	conn, err := s.client.Acquire(s.ctx)
	if err != nil {
		s.fail("acquire pool connection", err)
	}
	defer conn.Release()
	runner, err := newPostgreSQLPlanJSONExecutor(conn)
	if err != nil {
		s.fail("newPostgreSQLPlanJSONExecutor", err)
	}
	sqlText := fmt.Sprintf(`SELECT "%s" FROM %s WHERE "%s" = $1 AND "%s"::text <> '%s' ORDER BY resource_seq`,
		exec10wColumn, table, exec10wColumn, exec10wColumn, exec10wLiteral)
	compiledCommand, err := command.NewPostgreSQLCommand(sqlText, []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterText, Value: exec10wBind},
	})
	if err != nil {
		t.Fatal(err)
	}
	assessment, err := explainPostgreSQLCompiledQuery(s.ctx, runner, dataframeexecution.CompiledQuery{Command: compiledCommand})
	if err != nil {
		s.fail("live explain through server wiring", err)
	}
	if assessment.Backend != string(command.CompiledBackendPostgreSQL) {
		t.Fatalf("backend tag: %q", assessment.Backend)
	}
	if len(assessment.Nodes) == 0 || len(assessment.FullScans) == 0 {
		t.Fatalf("expected a sequential-scan plan: %+v", assessment)
	}
	rendered, err := json.Marshal(assessment)
	if err != nil {
		t.Fatal(err)
	}
	s.assertSentinelFree(t, "wired live assessment", string(rendered))
	// Paired control: the same wiring assesses the indexed fixture query
	// with index evidence, proving the guards are selective.
	indexedSQL := fmt.Sprintf(`SELECT resource_seq FROM %s WHERE resource_seq = $1 ORDER BY resource_seq`, table)
	indexedCommand, err := command.NewPostgreSQLCommand(indexedSQL, []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterBigint, Value: int64(42)},
	})
	if err != nil {
		t.Fatal(err)
	}
	indexed, err := explainPostgreSQLCompiledQuery(s.ctx, runner, dataframeexecution.CompiledQuery{Command: indexedCommand})
	if err != nil {
		s.fail("live indexed explain through server wiring", err)
	}
	if len(indexed.IndexScans) == 0 {
		t.Fatalf("indexed fixture query must classify index scans: %+v", indexed)
	}
	renderedIndexed, _ := json.Marshal(indexed)
	s.assertSentinelFree(t, "wired live indexed assessment", string(renderedIndexed))

	// Permission-denied EXPLAIN through the production wiring itself: the
	// rows adapter classifies the 42501 at the store/postgres boundary and
	// execution passes the classification through; the typed failure
	// renders no identifiers.
	role := fmt.Sprintf(exec10wPrefix+"r_%d_%s", time.Now().UnixNano(), exec10wRandomHex(t))
	if !regexp.MustCompile(`^verify_exec10w_r_[0-9]+_[0-9a-f]+$`).MatchString(role) {
		t.Fatalf("role name %q outside the owned pattern", role)
	}
	if _, err := s.iso.Exec(s.ctx, `CREATE ROLE "`+role+`" NOSUPERUSER NOLOGIN`); err != nil {
		s.fail("create role", err)
	}
	exec10wRegisterRole(role)
	t.Cleanup(func() {
		if _, err := s.iso.Exec(context.WithoutCancel(s.ctx), `REVOKE ALL ON `+table+` FROM "`+role+`"`); err != nil {
			t.Errorf("revoke table from role %s: %v", role, err)
		}
		if _, err := s.iso.Exec(context.WithoutCancel(s.ctx), `DROP ROLE IF EXISTS "`+role+`"`); err != nil {
			t.Errorf("drop role %s: %v", role, err)
		}
	})
	// Use a fresh single connection so SET ROLE does not poison the pool.
	plain, err := pgx.Connect(s.ctx, s.dsn)
	if err != nil {
		s.fail("connect dedicated permission connection", err)
	}
	defer plain.Close(s.ctx)
	if _, err := plain.Exec(s.ctx, `SET ROLE "`+role+`"`); err != nil {
		s.fail("set role", err)
	}
	planExecutor, err := newPostgreSQLPlanJSONExecutor(plain)
	if err != nil {
		s.fail("build production plan executor", err)
	}
	_, err = explainPostgreSQLCompiledQuery(s.ctx, planExecutor, dataframeexecution.CompiledQuery{Command: indexedCommand})
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeInternalError) {
		t.Fatalf("permission-denied EXPLAIN must carry the boundary internal classification: %v", err)
	}
	var translated *storepg.TranslatedError
	if !errors.As(err, &translated) || translated.Condition != storepg.ConditionServerRejected || translated.SQLState != "42501" {
		t.Fatalf("permission-denied EXPLAIN boundary classification: %v", err)
	}
	s.assertSentinelFree(t, "permission error surface", err.Error())
}

func pgValues(args []command.PostgreSQLParameter) []any {
	values := make([]any, 0, len(args))
	for _, arg := range args {
		values = append(values, arg.Value)
	}
	return values
}

// TestZZZeroLeftoverExec10WiringPostgresFixtures audits this file's own
// ownership only.
func TestZZZeroLeftoverExec10WiringPostgresFixtures(t *testing.T) {
	raw := exec10wURL(t)
	exec10wCaptureBaseline(raw)
	if exec10wBaselineErr != nil {
		t.Fatalf("capture baseline: %v", exec10wBaselineErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer admin.Close()
	dbs, roles := exec10wSnapshot()
	for db := range dbs {
		var exists bool
		if err := admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`, db).Scan(&exists); err != nil {
			t.Fatalf("check %s: %v", db, err)
		}
		if exists {
			t.Fatalf("created database %s was not dropped", db)
		}
	}
	for role := range roles {
		var exists bool
		if err := admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname = $1)`, role).Scan(&exists); err != nil {
			t.Fatalf("check role %s: %v", role, err)
		}
		if exists {
			t.Fatalf("created role %s was not dropped", role)
		}
	}
	count := func(query string, baseline []string, kind string) {
		rows, err := admin.Query(ctx, query)
		if err != nil {
			t.Fatalf("%s prefix scan: %v", kind, err)
		}
		var survivors []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				t.Fatalf("%s prefix read: %v", kind, err)
			}
			survivors = append(survivors, name)
		}
		rows.Close()
		if len(survivors) != len(baseline) {
			t.Fatalf("%s verify_exec10w_* survivors beyond baseline: got %v, baseline %v", kind, survivors, baseline)
		}
		known := map[string]bool{}
		for _, name := range baseline {
			known[name] = true
		}
		for _, name := range survivors {
			if !known[name] {
				t.Fatalf("unregistered verify_exec10w_* %s survivor (fatal): %s", kind, name)
			}
		}
	}
	count("SELECT datname FROM pg_database WHERE datname LIKE 'verify\\_exec10w\\_%' ESCAPE '\\'", exec10wBaselineDBs, "database")
	count("SELECT rolname FROM pg_roles WHERE rolname LIKE 'verify\\_exec10w\\_%' ESCAPE '\\'", exec10wBaselineRoles, "role")
}
