package postgres

// Real-PostgreSQL acceptance tests for the execution row-streaming adapter
// (pg-exec-01; check-2: go test ./internal/backends/postgres/internal/rows -count=1 under
// LOOM_TEST_POSTGRES_URL against an isolated PostgreSQL 18 service).
//
// Every test drives the real Executor against the PostgreSQL 18 service
// named by LOOM_TEST_POSTGRES_URL. Fixtures are literal rows inserted by
// this file; witnesses are literal values read back through the adapter and
// by direct SQL: exact column names, driver-decoded values, canonicalized
// JSONB texts, verbatim json texts, SQLSTATEs, and constraint names are
// pinned to golden literals captured from PostgreSQL 18 itself. Nothing is
// mocked here: the live claims (streaming, parameterization, visitor
// stopping, cancellation, error causes, races, crash windows) all run
// through the real server engine. The fixture refuses to run against any
// non-18 server.
//
// Non-vacuity discipline: every refusal has a paired control that proceeds
// (a guard that always refused would fail its control), and every claim is
// mutation-probed — the punctuation payload really breaks query shape when
// interpolated (42601), the aborted writer really writes (its in-transaction
// digest differs from the committed golden), the contract really reads the
// server's field names (a quoted alias that matches the declaration
// streams). Race surfaces run at least ten rounds each on two real
// connections with distinct pg_backend_pid values witnessed inside the
// streamed rows, and every abort converges to a byte-identical witness
// pinned to a literal. Crash-window injections terminate the real backend
// at every durability seam the adapter exposes (query start, mid-stream,
// scan window, visitor stop), each followed by convergence witnesses.
//
// Fixture databases and schemas are uniquely owned as verify_exec01_* and
// dropped by the test that created them. The final TestZZ audit is
// ownership-scoped: it checks each exact name this file created and counts
// only its own verify_exec01_* prefix against the baseline captured before
// this process created anything, so concurrent sibling workers sweeping
// their own prefixes neither race this audit nor trip it.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/calypr/loom/internal/dataframe/execution"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	storepg "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	exec01Prefix  = "verify_exec01_"
	exec01NameFmt = exec01Prefix + "%d_%s"
)

var exec01NamePattern = regexp.MustCompile(`^verify_exec01_[0-9]+_[0-9a-f]+$`)

// Ownership registry: every database this file verified into existence is
// recorded here, and TestZZ asserts each exact name is gone. Ownership
// begins at verified CREATE, exactly as the per-test cleanup drops them.
var (
	exec01OwnedMu     sync.Mutex
	exec01OwnedByName = map[string]string{} // database -> owned schema
)

func exec01Register(db, schema string) {
	exec01OwnedMu.Lock()
	defer exec01OwnedMu.Unlock()
	exec01OwnedByName[db] = schema
}

// exec01Baseline is the count of verify_exec01_* databases visible before
// this process created its first fixture. Only this file's tests create that
// prefix, so TestZZ can assert the count returned to it.
var (
	exec01BaselineOnce  sync.Once
	exec01BaselineCount int64
	exec01BaselineErr   error
)

func exec01CaptureBaseline(raw string) {
	exec01BaselineOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		admin, err := pgxpool.New(ctx, raw)
		if err != nil {
			exec01BaselineErr = fmt.Errorf("connect for baseline: %w", err)
			return
		}
		defer admin.Close()
		var count int64
		if err := admin.QueryRow(ctx,
			"SELECT count(*) FROM pg_database WHERE datname LIKE 'verify\\_exec01\\_%' ESCAPE '\\'").
			Scan(&count); err != nil {
			exec01BaselineErr = fmt.Errorf("count baseline: %w", err)
			return
		}
		exec01BaselineCount = count
	})
}

func exec01URL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 integration tests")
	}
	return raw
}

type exec01Service struct {
	ctx    context.Context
	admin  *pgxpool.Pool // service database
	iso    *pgxpool.Pool // isolated database (direct-SQL witnesses)
	dsn    string        // isolated database DSN (credentials scrubbed on failure)
	schema string        // owned schema inside the isolated database
	scrub  func(any) string
	fail   func(step string, err error)
}

func newExec01Service(t *testing.T) *exec01Service {
	t.Helper()
	raw := exec01URL(t)
	exec01CaptureBaseline(raw)
	if exec01BaselineErr != nil {
		t.Fatalf("capture pg_database baseline: %v", exec01BaselineErr)
	}

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

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	t.Cleanup(admin.Close)

	// PostgreSQL 18 only: the fixture refuses any other server rather than
	// claiming PG18 parity against a different engine.
	var versionNum int64
	var version string
	if err := admin.QueryRow(ctx,
		"SELECT current_setting('server_version_num')::int8, current_setting('server_version')").
		Scan(&versionNum, &version); err != nil {
		fail("read server version", err)
	}
	if err := storepg.CheckServerVersion(versionNum, version); err != nil {
		t.Fatalf("test service is not the required backend: %v", err)
	}

	dbName := fmt.Sprintf(exec01NameFmt, time.Now().UnixNano(), exec01RandomHex(t))
	if !exec01NamePattern.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
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
	u.Path = "/" + dbName
	dsn := u.String()
	secrets = append(secrets, dsn)

	iso, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fail("connect to isolated database", err)
	}
	t.Cleanup(iso.Close)

	schema := fmt.Sprintf(exec01NameFmt, time.Now().UnixNano(), exec01RandomHex(t))
	if !exec01NamePattern.MatchString(schema) {
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

	exec01Register(dbName, schema)
	return &exec01Service{ctx: ctx, admin: admin, iso: iso, dsn: dsn, schema: schema, scrub: scrub, fail: fail}
}

func exec01RandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return hex.EncodeToString(b[:])
}

// pool returns a bounded pool into the isolated database whose sessions are
// tagged application_name = verify_exec01_<suffix> so session accounting can
// distinguish this suite from anything else on the service.
func (s *exec01Service) pool(t *testing.T, maxConns int32, suffix string) *pgxpool.Pool {
	t.Helper()
	return s.poolOn(t, s.dsn, maxConns, suffix)
}

func (s *exec01Service) poolOn(t *testing.T, dsn string, maxConns int32, suffix string) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		s.fail("parse pool config", err)
	}
	cfg.MaxConns = maxConns
	cfg.MinConns = 0
	cfg.ConnConfig.RuntimeParams["application_name"] = exec01Prefix + suffix
	pool, err := pgxpool.NewWithConfig(s.ctx, cfg)
	if err != nil {
		s.fail("build pool", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// exec returns an Executor streaming through pool.
func (s *exec01Service) exec(t *testing.T, pool Querier) *Executor {
	t.Helper()
	executor, err := NewExecutor(pool)
	if err != nil {
		s.fail("NewExecutor", err)
	}
	return executor
}

func (s *exec01Service) table(name string) string { return `"` + s.schema + `"."` + name + `"` }

// waitForActive blocks until a session tagged with app is actively running a
// statement whose text contains tag, and returns its pid. It is the
// non-vacuity witness that an abort landed on a live server-side execution.
func (s *exec01Service) waitForActive(t *testing.T, app, tag string) int64 {
	t.Helper()
	var pid int64
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := s.admin.QueryRow(s.ctx,
			`SELECT pid FROM pg_stat_activity
			 WHERE application_name = $1 AND query LIKE $2 AND state = 'active' AND pid <> pg_backend_pid() LIMIT 1`,
			app, "%"+tag+"%").Scan(&pid); err == nil {
			return pid
		}
		if time.Now().After(deadline) {
			s.fail("tagged stream never became active on the server", fmt.Errorf("app %q tag %q", app, tag))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func pgSQL(t *testing.T, sql string, args ...command.PostgreSQLParameter) command.CompiledCommand {
	t.Helper()
	compiledCommand, err := command.NewPostgreSQLCommand(sql, args)
	if err != nil {
		t.Fatalf("NewPostgreSQLCommand(%q): %v", sql, err)
	}
	return compiledCommand
}

func bigint(v int64) command.PostgreSQLParameter {
	return command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: v}
}

func textParam(v string) command.PostgreSQLParameter {
	return command.PostgreSQLParameter{Type: command.PostgreSQLParameterText, Value: v}
}

func jsonbParam(raw string) command.PostgreSQLParameter {
	return command.PostgreSQLParameter{Type: command.PostgreSQLParameterJSONB, Value: json.RawMessage(raw)}
}

func sqlNullParam() command.PostgreSQLParameter {
	return command.PostgreSQLParameter{Type: command.PostgreSQLParameterNull, Value: nil}
}

// expectQueryError asserts an execution-seam failure keeps its class, its
// operation seam, and the boundary classification carrying the exact pinned
// SQLSTATE, while the server message fragment stays reachable only through
// the cause-identity accessor (*storepg.TranslatedError).Cause. After the
// arch-backend-03 boundary-through, the public rendering carries the stable
// condition and SQLSTATE only: the server message (relation names,
// constraint names, statement fragments) must not appear in it.
func expectQueryError(t *testing.T, err error, wantOp, wantState, wantMessage string) {
	t.Helper()
	if !errors.Is(err, ErrQuery) {
		t.Fatalf("error %v does not match ErrQuery", err)
	}
	var qe *QueryError
	if !errors.As(err, &qe) {
		t.Fatalf("error %v is not a *QueryError", err)
	}
	if !strings.HasPrefix(err.Error(), "postgres rows: "+wantOp+":") {
		t.Fatalf("error %q did not surface at seam %q", err, wantOp)
	}
	var translated *storepg.TranslatedError
	if !errors.As(err, &translated) {
		t.Fatalf("error %v lost the boundary classification", err)
	}
	if translated.SQLState != wantState {
		t.Fatalf("SQLSTATE = %q, want %q (error %v)", translated.SQLState, wantState, err)
	}
	if strings.Contains(err.Error(), wantMessage) {
		t.Fatalf("public error %q discloses the server message %q", err, wantMessage)
	}
	var pgErr *pgconn.PgError
	if !errors.As(translated.Cause(), &pgErr) {
		t.Fatalf("translated error lost the *pgconn.PgError cause through Cause(): %v", err)
	}
	if !strings.Contains(pgErr.Message, wantMessage) {
		t.Fatalf("server message %q does not contain %q", pgErr.Message, wantMessage)
	}
}

// ---------------------------------------------------------------------------
// Golden literals captured from PostgreSQL 18 itself (LOAD-BEARING pins).
// ---------------------------------------------------------------------------

const (
	goldenLabel1   = "alpha"
	goldenLabel2   = "beta"
	goldenDate1    = "2026-02-13"
	goldenDate2    = "2025-12-31"
	goldenTS1      = "2026-02-13T10:20:30Z"
	goldenTS2      = "2025-12-31T23:59:59.5Z"
	goldenNote2    = "plain note; with punctuation"
	goldenMeta1Raw = `{"z": 1,"a":2}` // json columns preserve input text verbatim
	// jsonb normalizes on the server; the adapter passes those bytes
	// through unchanged, which is exactly this pinned text.
	goldenPayload1Read = `{"a": 1, "b": 2}`
	goldenPayload2Read = `[true, null]`

	goldenPunctKill      = `'); DROP TABLE exec01_punct; --`
	goldenPunctStudents  = `Robert'); DROP TABLE students;--`
	goldenPunctDollar    = `cost $1 plus 'quotes' and "dquotes"`
	goldenPunctBackslash = `\\'; SELECT count(*) FROM pg_class; --`

	// Race witnesses: "id|label|score" lines joined by ";".
	goldenRaceDigest      = "1|alpha|1.5;2|beta|-0.25;3|gamma|7"
	goldenRaceDigestDirty = "1|alpha|1001.5;2|beta|-0.25;3|gamma|7"
)

func goldenDate(t *testing.T, literal string) time.Time {
	t.Helper()
	parsed, err := time.Parse("2006-01-02", literal)
	if err != nil {
		t.Fatalf("golden date literal %q: %v", literal, err)
	}
	return parsed
}

func goldenInstant(t *testing.T, literal string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, literal)
	if err != nil {
		t.Fatalf("golden timestamp literal %q: %v", literal, err)
	}
	return parsed
}

// ---------------------------------------------------------------------------
// Acceptance 1: a temporary PG table with two typed rows streams exact
// column names and values through the visitor.
// ---------------------------------------------------------------------------

func TestExec01TempTableStreamsExactTypedRows(t *testing.T) {
	s := newExec01Service(t)
	pool := s.pool(t, 2, "temp")
	conn, err := pool.Acquire(s.ctx)
	if err != nil {
		s.fail("acquire pinned connection", err)
	}
	defer conn.Release()
	executor := s.exec(t, conn)

	if _, err := conn.Exec(s.ctx, `CREATE TEMP TABLE exec01_typed_rows(
		id bigint NOT NULL PRIMARY KEY,
		label text NOT NULL,
		score double precision NOT NULL,
		active boolean NOT NULL,
		event_date date NOT NULL,
		event_ts timestamptz NOT NULL,
		payload jsonb NOT NULL,
		meta json NOT NULL,
		note text)`); err != nil {
		s.fail("create temp fixture table", err)
	}
	if _, err := conn.Exec(s.ctx, `INSERT INTO exec01_typed_rows
		(id, label, score, active, event_date, event_ts, payload, meta, note) VALUES
		(1, 'alpha', 1.5, true,  '2026-02-13', '2026-02-13T10:20:30Z', '{"b": 2, "a": 1}'::jsonb, '{"z": 1,"a":2}'::json, NULL),
		(2, 'beta', -0.25, false, '2025-12-31', '2025-12-31T23:59:59.5Z', '[true, null]'::jsonb, 'null'::json, 'plain note; with punctuation')`); err != nil {
		s.fail("insert temp fixture rows", err)
	}

	columns := []string{"id", "label", "score", "active", "event_date", "event_ts", "payload", "meta", "note"}
	compiledCommand := pgSQL(t, `SELECT id, label, score, active, event_date, event_ts, payload, meta, note
		FROM exec01_typed_rows WHERE id IN ($1, $2) ORDER BY id`, bigint(1), bigint(2))

	var visited []map[string]any
	if err := executor.SourceRows(s.ctx, execution.RowsRequest{Command: compiledCommand, DeclaredColumns: columns}, func(row map[string]any) error {
		visited = append(visited, row)
		return nil
	}); err != nil {
		s.fail("stream typed rows", err)
	}
	if len(visited) != 2 {
		t.Fatalf("visited %d rows, want exactly 2", len(visited))
	}

	for i, row := range visited {
		if len(row) != len(columns) {
			t.Fatalf("row %d has %d keys, want exactly the %d declared columns", i, len(row), len(columns))
		}
		for _, column := range columns {
			if _, ok := row[column]; !ok {
				t.Fatalf("row %d is missing declared column %q", i, column)
			}
		}
	}

	first, second := visited[0], visited[1]
	if first["id"] != int64(1) || second["id"] != int64(2) {
		t.Fatalf("ids = %#v, %#v, want int64(1), int64(2)", first["id"], second["id"])
	}
	if first["label"] != goldenLabel1 || second["label"] != goldenLabel2 {
		t.Fatalf("labels = %#v, %#v, want %q, %q", first["label"], second["label"], goldenLabel1, goldenLabel2)
	}
	if first["score"] != 1.5 || second["score"] != -0.25 {
		t.Fatalf("scores = %#v, %#v, want 1.5, -0.25", first["score"], second["score"])
	}
	if first["active"] != true || second["active"] != false {
		t.Fatalf("actives = %#v, %#v, want true, false", first["active"], second["active"])
	}
	if day, ok := first["event_date"].(time.Time); !ok || !day.Equal(goldenDate(t, goldenDate1)) {
		t.Fatalf("event_date row 1 = %#v, want %s", first["event_date"], goldenDate1)
	}
	if day, ok := second["event_date"].(time.Time); !ok || !day.Equal(goldenDate(t, goldenDate2)) {
		t.Fatalf("event_date row 2 = %#v, want %s", second["event_date"], goldenDate2)
	}
	if stamp, ok := first["event_ts"].(time.Time); !ok || stamp.UTC().Format(time.RFC3339Nano) != goldenTS1 {
		t.Fatalf("event_ts row 1 = %#v, want %s", first["event_ts"], goldenTS1)
	}
	if stamp, ok := second["event_ts"].(time.Time); !ok || stamp.UTC().Format(time.RFC3339Nano) != goldenTS2 {
		t.Fatalf("event_ts row 2 = %#v, want %s", second["event_ts"], goldenTS2)
	}
	// jsonb arrives canonicalized by the server and untouched by Go; json
	// arrives as the verbatim stored text. The two pins differ on purpose.
	if raw, ok := first["payload"].(json.RawMessage); !ok || string(raw) != goldenPayload1Read {
		t.Fatalf("payload row 1 = %#v, want json.RawMessage(%q)", first["payload"], goldenPayload1Read)
	}
	if raw, ok := second["payload"].(json.RawMessage); !ok || string(raw) != goldenPayload2Read {
		t.Fatalf("payload row 2 = %#v, want json.RawMessage(%q)", second["payload"], goldenPayload2Read)
	}
	if raw, ok := first["meta"].(json.RawMessage); !ok || string(raw) != goldenMeta1Raw {
		t.Fatalf("meta row 1 = %#v, want verbatim json.RawMessage(%q)", first["meta"], goldenMeta1Raw)
	}
	if raw, ok := second["meta"].(json.RawMessage); !ok || string(raw) != "null" {
		t.Fatalf("meta row 2 = %#v, want JSON null value", second["meta"])
	}
	if first["note"] != nil {
		t.Fatalf("note row 1 = %#v, want nil for SQL NULL", first["note"])
	}
	if second["note"] != goldenNote2 {
		t.Fatalf("note row 2 = %#v, want %q", second["note"], goldenNote2)
	}

	// Control: the identical statement with two absent ids streams nothing
	// and is not an error (a stream that always errored would fail here).
	empty := pgSQL(t, `SELECT id, label, score, active, event_date, event_ts, payload, meta, note
		FROM exec01_typed_rows WHERE id IN ($1, $2) ORDER BY id`, bigint(98), bigint(99))
	emptyVisits := 0
	if err := executor.SourceRows(s.ctx, execution.RowsRequest{Command: empty, DeclaredColumns: columns}, func(map[string]any) error { emptyVisits++; return nil }); err != nil {
		s.fail("empty control stream", err)
	}
	if emptyVisits != 0 {
		t.Fatalf("empty control visited %d rows, want 0", emptyVisits)
	}
}

// ---------------------------------------------------------------------------
// Acceptance 2: SQL punctuation in a bound value remains a value and cannot
// change query shape.
// ---------------------------------------------------------------------------

func TestExec01BoundPunctuationRemainsAValue(t *testing.T) {
	s := newExec01Service(t)
	pool := s.pool(t, 4, "punct")
	executor := s.exec(t, pool)

	if _, err := s.iso.Exec(s.ctx, `CREATE TABLE `+s.table("exec01_punct")+`(
		label text PRIMARY KEY, body text NOT NULL)`); err != nil {
		s.fail("create punctuation fixture", err)
	}
	if _, err := s.iso.Exec(s.ctx, `INSERT INTO `+s.table("exec01_punct")+`(label, body) VALUES ($1, $2), ($3, $4)`,
		goldenPunctKill, goldenPunctStudents, goldenPunctDollar, goldenPunctBackslash); err != nil {
		s.fail("insert punctuation fixtures", err)
	}

	// Bound round-trip: every payload comes back byte-identical, including
	// the literal "$1" text inside a value, which must not rebind.
	bind := pgSQL(t, `SELECT label, body FROM `+s.table("exec01_punct")+` WHERE label = $1 AND body = $2`,
		textParam(goldenPunctKill), textParam(goldenPunctStudents))
	var rows []map[string]any
	if err := executor.SourceRows(s.ctx, execution.RowsRequest{Command: bind, DeclaredColumns: []string{"label", "body"}}, func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	}); err != nil {
		s.fail("bound punctuation stream", err)
	}
	if len(rows) != 1 {
		t.Fatalf("bound punctuation stream returned %d rows, want 1", len(rows))
	}
	if rows[0]["label"] != goldenPunctKill || rows[0]["body"] != goldenPunctStudents {
		t.Fatalf("bound values altered: %#v", rows[0])
	}

	dollar := pgSQL(t, `SELECT label, body FROM `+s.table("exec01_punct")+` WHERE label = $1`, textParam(goldenPunctDollar))
	if err := executor.SourceRows(s.ctx, execution.RowsRequest{Command: dollar, DeclaredColumns: []string{"label", "body"}}, func(row map[string]any) error {
		if row["label"] != goldenPunctDollar || row["body"] != goldenPunctBackslash {
			t.Fatalf("dollar-quoted value altered: %#v", row)
		}
		return nil
	}); err != nil {
		s.fail("dollar payload stream", err)
	}

	// Query shape did not change: both fixture rows still exist, the
	// attempted DROP/TRUNCATE victims are untouched, and no stray table was
	// created by the payloads.
	var rowCount int64
	if err := s.iso.QueryRow(s.ctx, `SELECT count(*) FROM `+s.table("exec01_punct")).Scan(&rowCount); err != nil {
		s.fail("count punctuation rows", err)
	}
	if rowCount != 2 {
		t.Fatalf("fixture table holds %d rows after bound punctuation, want 2 (shape changed)", rowCount)
	}
	var killedTable int64
	if err := s.iso.QueryRow(s.ctx, `SELECT count(*) FROM pg_class WHERE relname = 'exec01_punct'`).Scan(&killedTable); err != nil {
		s.fail("inspect pg_class", err)
	}
	if killedTable != 1 {
		t.Fatalf("exec01_punct appears %d times in pg_class, want exactly 1", killedTable)
	}

	// Mutation probe: the same payload interpolated into the statement text
	// instead of bound really does change query shape (syntax error). This
	// proves the parameterization claim above is not vacuous.
	interpolated := pgSQL(t, `SELECT label FROM `+s.table("exec01_punct")+` WHERE label = '`+goldenPunctKill+`'`)
	interpolatedVisits := 0
	err := executor.SourceRows(s.ctx, execution.RowsRequest{Command: interpolated, DeclaredColumns: []string{"label"}}, func(map[string]any) error { interpolatedVisits++; return nil })
	expectQueryError(t, err, "query", "42601", `syntax error at or near ")"`)
	if interpolatedVisits != 0 {
		t.Fatalf("interpolated payload visited %d rows, want 0", interpolatedVisits)
	}

	// Positive structural witness: PostgreSQL flushes large result rows as
	// they are produced, so with a >8KB padded row and a server-side sleep
	// the first row reaches the visitor while the statement is still
	// active. The server's own pg_stat_activity then shows the
	// parameterized statement text, never the bound value.
	witnessSQL := `SELECT pg_backend_pid() AS backend, pg_sleep(0.3) IS NOT NULL AS slept, $1::text AS payload, repeat(md5('exec01-witness'), 2000) AS pad FROM generate_series(1, 2) g`
	witness := pgSQL(t, witnessSQL, textParam(goldenPunctKill))
	witnessVisits := 0
	witnessStarted := time.Now()
	err = executor.SourceRows(s.ctx, execution.RowsRequest{Command: witness, DeclaredColumns: []string{"backend", "slept", "payload", "pad"}}, func(row map[string]any) error {
		witnessVisits++
		if witnessVisits != 1 {
			return nil
		}
		if elapsed := time.Since(witnessStarted); elapsed < 250*time.Millisecond {
			t.Fatalf("witness row 1 arrived after %v; the in-flight window was not real", elapsed)
		}
		backend, ok := row["backend"].(int32)
		if !ok {
			t.Fatalf("backend column decoded as %#v", row["backend"])
		}
		var activityQuery, state string
		if err := s.admin.QueryRow(s.ctx,
			`SELECT query, state FROM pg_stat_activity WHERE pid = $1 AND pid <> pg_backend_pid()`, int64(backend)).
			Scan(&activityQuery, &state); err != nil {
			s.fail("read live activity", err)
		}
		if state != "active" {
			t.Fatalf("stream was not still executing when row 1 reached the visitor: state=%q", state)
		}
		if !strings.Contains(activityQuery, "$1") {
			t.Fatalf("live server statement is not parameterized: %q", activityQuery)
		}
		if strings.Contains(activityQuery, "DROP TABLE") || strings.Contains(activityQuery, goldenPunctKill) {
			t.Fatalf("bound value leaked into the server statement text: %q", activityQuery)
		}
		if row["payload"] != goldenPunctKill {
			t.Fatalf("bound payload altered mid-stream: %#v", row["payload"])
		}
		return nil
	})
	if err != nil {
		s.fail("activity-witness stream", err)
	}
	if witnessVisits != 2 {
		t.Fatalf("activity-witness stream visited %d rows, want 2", witnessVisits)
	}
}

// ---------------------------------------------------------------------------
// Acceptance 3a: a visitor error stops iteration and rows close.
// ---------------------------------------------------------------------------

func TestExec01VisitorErrorStopsIterationAndClosesRows(t *testing.T) {
	s := newExec01Service(t)
	pool := s.pool(t, 2, "visitor")
	conn, err := pool.Acquire(s.ctx)
	if err != nil {
		s.fail("acquire pinned connection", err)
	}
	defer conn.Release()
	executor := s.exec(t, conn)

	if _, err := conn.Exec(s.ctx, `CREATE TEMP TABLE exec01_three(id bigint PRIMARY KEY, label text NOT NULL)`); err != nil {
		s.fail("create three-row fixture", err)
	}
	if _, err := conn.Exec(s.ctx, `INSERT INTO exec01_three(id, label) VALUES (1, 'a'), (2, 'b'), (3, 'c')`); err != nil {
		s.fail("insert three rows", err)
	}

	streamSQL := `SELECT id, label FROM exec01_three ORDER BY id`
	compiledCommand := pgSQL(t, streamSQL)

	boom := errors.New("visitor boom")
	visits := 0
	err = executor.SourceRows(s.ctx, execution.RowsRequest{Command: compiledCommand, DeclaredColumns: []string{"id", "label"}}, func(row map[string]any) error {
		visits++
		if visits == 2 {
			return boom
		}
		return nil
	})
	if err != boom {
		t.Fatalf("visitor error altered: %#v", err)
	}
	if visits != 2 {
		t.Fatalf("visited %d rows, want iteration to stop at the second", visits)
	}

	// Rows closed: the same connection is immediately reusable, and the
	// abandoned stream left no server-side cursor behind.
	var reuse int64
	if err := conn.QueryRow(s.ctx, `SELECT 42::bigint`).Scan(&reuse); err != nil {
		s.fail("connection not reusable after visitor stop", err)
	}
	if reuse != 42 {
		t.Fatalf("reuse witness = %d, want 42", reuse)
	}
	var cursors int64
	if err := conn.QueryRow(s.ctx,
		`SELECT count(*) FROM pg_cursors WHERE statement LIKE 'SELECT id, label FROM exec01_three%'`).Scan(&cursors); err != nil {
		s.fail("count cursors", err)
	}
	if cursors != 0 {
		t.Fatalf("abandoned stream left %d server-side cursor(s)", cursors)
	}

	// Control: the identical statement drains fully when the visitor accepts.
	full := 0
	if err := executor.SourceRows(s.ctx, execution.RowsRequest{Command: compiledCommand, DeclaredColumns: []string{"id", "label"}}, func(map[string]any) error { full++; return nil }); err != nil {
		s.fail("full drain control", err)
	}
	if full != 3 {
		t.Fatalf("control drained %d rows, want 3", full)
	}
}

// ---------------------------------------------------------------------------
// Acceptance 3b: cancellation returns an errors.Is-compatible context error.
// ---------------------------------------------------------------------------

func TestExec01CancellationReturnsCompatibleContextErrors(t *testing.T) {
	s := newExec01Service(t)
	// Pure streaming over the service database: no fixture objects created.
	pool := s.poolOn(t, s.dsn, 3, "cancel")
	executor := s.exec(t, pool)

	// A 2-second in-flight window: pgx holds result delivery open while the
	// server computes, so a cancel or deadline that lands while the query
	// is active on the server is a real mid-execution abort. The active
	// witness below proves the abort landed on a live server query; the
	// control's timing pin proves the window was real (non-vacuity).
	streamSQL := `SELECT pg_sleep(0.05) IS NULL AS tick, g FROM generate_series(1::bigint, 40::bigint) g`
	columns := []string{"tick", "g"}

	t.Run("client cancel during in-flight execution is errors.Is-compatible", func(t *testing.T) {
		ctx, cancel := context.WithCancel(s.ctx)
		defer cancel()
		tagged := `SELECT /*exec01-client-cancel*/ pg_sleep(0.05) IS NULL AS tick, g FROM generate_series(1::bigint, 40::bigint) g`
		errc := make(chan error, 1)
		go func() {
			err := executor.streamTagged(ctx, tagged)
			errc <- err
		}()
		s.waitForActive(t, exec01Prefix+"cancel", "exec01-client-cancel")
		cancel()
		err := <-errc
		if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrQuery) {
			t.Fatalf("cancellation error not errors.Is-compatible: %v", err)
		}
	})

	t.Run("control without cancel streams every row slowly", func(t *testing.T) {
		started := time.Now()
		visits := 0
		if err := executor.SourceRows(s.ctx, execution.RowsRequest{Command: pgSQL(t, streamSQL), DeclaredColumns: columns}, func(row map[string]any) error {
			visits++
			if _, ok := row["g"].(int64); !ok {
				t.Fatalf("unexpected streamed row: %#v", row)
			}
			return nil
		}); err != nil {
			s.fail("uncancelled control", err)
		}
		elapsed := time.Since(started)
		if visits != 40 {
			t.Fatalf("control streamed %d rows, want all 40", visits)
		}
		// Load-bearing non-vacuity pin: the stream really is slow, so the
		// cancel/deadline subtests above cut a live execution and a fast
		// no-op query would fail this pin.
		if elapsed < 1500*time.Millisecond {
			t.Fatalf("control stream completed in %v; the in-flight window was not real", elapsed)
		}
	})

	t.Run("deadline expiry is errors.Is-compatible", func(t *testing.T) {
		tagged := `SELECT /*exec01-deadline*/ pg_sleep(0.05) IS NULL AS tick, g FROM generate_series(1::bigint, 40::bigint) g`
		ctx, cancel := context.WithTimeout(s.ctx, 350*time.Millisecond)
		defer cancel()
		err := executor.streamTagged(ctx, tagged)
		if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrQuery) {
			t.Fatalf("deadline error not errors.Is-compatible: %v", err)
		}
	})

	t.Run("already-cancelled context fails at the seam", func(t *testing.T) {
		ctx, cancel := context.WithCancel(s.ctx)
		cancel()
		err := executor.SourceRows(ctx, execution.RowsRequest{Command: pgSQL(t, streamSQL), DeclaredColumns: columns}, func(map[string]any) error { return nil })
		if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrQuery) {
			t.Fatalf("pre-cancelled context error not errors.Is-compatible: %v", err)
		}
	})

	t.Run("server-side cancel keeps its SQLSTATE cause", func(t *testing.T) {
		tagged := `SELECT /*exec01-server-cancel*/ pg_sleep(0.05) IS NULL AS tick, g FROM generate_series(1::bigint, 40::bigint) g`
		errc := make(chan error, 1)
		go func() {
			err := executor.streamTagged(s.ctx, tagged)
			errc <- err
		}()
		pid := s.waitForActive(t, exec01Prefix+"cancel", "exec01-server-cancel")
		var cancelled bool
		if err := s.admin.QueryRow(s.ctx, `SELECT pg_cancel_backend($1)`, pid).Scan(&cancelled); err != nil {
			s.fail("pg_cancel_backend", err)
		}
		if !cancelled {
			t.Fatal("pg_cancel_backend reported no cancellation")
		}
		err := <-errc
		expectQueryError(t, err, "rows", "57014", "canceling statement due to user request")
	})

	// Pool converged after every abort.
	var witness int64
	if err := pool.QueryRow(s.ctx, `SELECT 7::bigint`).Scan(&witness); err != nil {
		s.fail("pool health after aborts", err)
	}
	if witness != 7 {
		t.Fatalf("pool witness = %d, want 7", witness)
	}
}

// streamTagged runs the server-cancel probe statement through the same
// executor machinery with the fixed single-column contract.
func (e *Executor) streamTagged(ctx context.Context, sql string) error {
	compiledCommand, err := command.NewPostgreSQLCommand(sql, nil)
	if err != nil {
		return err
	}
	return e.SourceRows(ctx, execution.RowsRequest{Command: compiledCommand, DeclaredColumns: []string{"tick", "g"}}, func(map[string]any) error { return nil })
}

// ---------------------------------------------------------------------------
// Acceptance 4: SQL errors retain an identifiable wrapped cause.
// ---------------------------------------------------------------------------

func TestExec01SQLErrorsRetainIdentifiableCauses(t *testing.T) {
	s := newExec01Service(t)
	pool := s.pool(t, 3, "sqlerr")
	executor := s.exec(t, pool)

	if _, err := s.iso.Exec(s.ctx, `CREATE TABLE `+s.table("exec01_three")+`(id bigint PRIMARY KEY, label text NOT NULL)`); err != nil {
		s.fail("create sqlerr fixture", err)
	}
	if _, err := s.iso.Exec(s.ctx, `INSERT INTO `+s.table("exec01_three")+`(id, label) VALUES (1, 'a'), (2, 'b')`); err != nil {
		s.fail("insert sqlerr rows", err)
	}
	if _, err := s.iso.Exec(s.ctx, `CREATE TABLE `+s.table("exec01_uniq")+`(label text PRIMARY KEY)`); err != nil {
		s.fail("create unique fixture", err)
	}
	if _, err := s.iso.Exec(s.ctx, `INSERT INTO `+s.table("exec01_uniq")+`(label) VALUES ('dup')`); err != nil {
		s.fail("seed unique fixture", err)
	}

	t.Run("undefined relation keeps SQLSTATE 42P01", func(t *testing.T) {
		err := executor.SourceRows(s.ctx, execution.RowsRequest{Command: pgSQL(t, `SELECT id FROM `+s.table("exec01_nope")+` ORDER BY id`), DeclaredColumns: []string{"id"}}, func(map[string]any) error { return nil })
		expectQueryError(t, err, "query", "42P01", `relation "`+s.schema+`.exec01_nope" does not exist`)
		// Control: the identical shape over the real table streams.
		count := 0
		if err := executor.SourceRows(s.ctx, execution.RowsRequest{Command: pgSQL(t, `SELECT id FROM `+s.table("exec01_three")+` ORDER BY id`), DeclaredColumns: []string{"id"}}, func(map[string]any) error { count++; return nil }); err != nil {
			s.fail("42P01 control", err)
		}
		if count != 2 {
			t.Fatalf("control streamed %d rows, want 2", count)
		}
	})

	t.Run("syntax error keeps SQLSTATE 42601", func(t *testing.T) {
		err := executor.SourceRows(s.ctx, execution.RowsRequest{Command: pgSQL(t, `SELECT FROM WHERE`), DeclaredColumns: []string{"x"}}, func(map[string]any) error { return nil })
		expectQueryError(t, err, "query", "42601", `syntax error at or near "WHERE"`)
	})

	t.Run("operator type error keeps SQLSTATE 42883", func(t *testing.T) {
		err := executor.SourceRows(s.ctx, execution.RowsRequest{Command: pgSQL(t, `SELECT 1::bigint + $1::text`, textParam("x")), DeclaredColumns: []string{"?"}}, func(map[string]any) error { return nil })
		expectQueryError(t, err, "query", "42883", `operator does not exist: bigint + text`)
		// Control: the typed sibling computes.
		var sum int64
		if err := executor.SourceRows(s.ctx, execution.RowsRequest{Command: pgSQL(t, `SELECT 1::bigint + $1::bigint AS total`, bigint(2)), DeclaredColumns: []string{"total"}}, func(row map[string]any) error { sum, _ = row["total"].(int64); return nil }); err != nil {
			s.fail("42883 control", err)
		}
		if sum != 3 {
			t.Fatalf("typed control total = %d, want 3", sum)
		}
	})

	t.Run("unique violation via RETURNING keeps constraint identity", func(t *testing.T) {
		visits := 0
		err := executor.SourceRows(s.ctx, execution.RowsRequest{Command: pgSQL(t, `INSERT INTO `+s.table("exec01_uniq")+`(label) VALUES ($1) RETURNING label`, textParam("dup")), DeclaredColumns: []string{"label"}}, func(map[string]any) error { visits++; return nil })
		// The duplicate surfaces while streaming results, not at prepare.
		expectQueryError(t, err, "rows", "23505", `duplicate key value violates unique constraint "exec01_uniq_pkey"`)
		if visits != 0 {
			t.Fatalf("failing INSERT RETURNING visited %d rows, want 0", visits)
		}
		var count int64
		if err := s.iso.QueryRow(s.ctx, `SELECT count(*) FROM `+s.table("exec01_uniq")).Scan(&count); err != nil {
			s.fail("count unique rows", err)
		}
		if count != 1 {
			t.Fatalf("unique fixture holds %d rows after failed insert, want 1 (statement must not half-apply)", count)
		}
		// Control: a fresh label inserts and streams its RETURNING row.
		returned := ""
		if err := executor.SourceRows(s.ctx, execution.RowsRequest{Command: pgSQL(t, `INSERT INTO `+s.table("exec01_uniq")+`(label) VALUES ($1) RETURNING label`, textParam("fresh")), DeclaredColumns: []string{"label"}}, func(row map[string]any) error { returned, _ = row["label"].(string); return nil }); err != nil {
			s.fail("RETURNING control", err)
		}
		if returned != "fresh" {
			t.Fatalf("RETURNING label = %q, want %q", returned, "fresh")
		}
	})

	// Pool converged after every refusal.
	var witness int64
	if err := pool.QueryRow(s.ctx, `SELECT 11::bigint`).Scan(&witness); err != nil {
		s.fail("pool health after sql errors", err)
	}
	if witness != 11 {
		t.Fatalf("pool witness = %d, want 11", witness)
	}
}

// ---------------------------------------------------------------------------
// Three-state contract: missing vs JSON-null vs SQL-NULL, end to end.
// ---------------------------------------------------------------------------

func TestExec01ThreeStatePreservation(t *testing.T) {
	s := newExec01Service(t)
	pool := s.pool(t, 3, "states")
	executor := s.exec(t, pool)

	if _, err := s.iso.Exec(s.ctx, `CREATE TABLE `+s.table("exec01_states")+`(
		id bigint PRIMARY KEY, j jsonb, jj json, t text, ab jsonb)`); err != nil {
		s.fail("create three-state fixture", err)
	}

	// The adapter itself performs the insert: jsonb JSON null, an
	// SQL-null-typed parameter, a text value that merely spells "null",
	// and a missing argument (column ab omitted entirely).
	inserted := ""
	if err := executor.SourceRows(s.ctx, execution.RowsRequest{Command: pgSQL(t, `INSERT INTO `+s.table("exec01_states")+`(id, j, jj, t) VALUES ($1, $2, $3, $4) RETURNING id`,
		bigint(1), jsonbParam("null"), sqlNullParam(), textParam("null")), DeclaredColumns: []string{"id"}}, func(row map[string]any) error {
		id, ok := row["id"].(int64)
		if !ok || id != 1 {
			t.Fatalf("RETURNING id = %#v, want int64(1)", row["id"])
		}
		inserted = "1"
		return nil
	}); err != nil {
		s.fail("three-state insert via adapter", err)
	}
	if inserted != "1" {
		t.Fatal("three-state insert did not stream its RETURNING row")
	}

	columns := []string{"j", "jj", "t", "ab", "j_is_null", "j_is_json_null", "j_type", "jj_is_null", "t_is_null", "ab_is_null", "ab_type"}
	compiledCommand := pgSQL(t, `SELECT j, jj, t, ab,
		j IS NULL AS j_is_null, (j = 'null'::jsonb) AS j_is_json_null, jsonb_typeof(j) AS j_type,
		jj IS NULL AS jj_is_null, t IS NULL AS t_is_null, ab IS NULL AS ab_is_null, jsonb_typeof(ab) AS ab_type
		FROM `+s.table("exec01_states")+` WHERE id = $1`, bigint(1))
	var row map[string]any
	if err := executor.SourceRows(s.ctx, execution.RowsRequest{Command: compiledCommand, DeclaredColumns: columns}, func(r map[string]any) error { row = r; return nil }); err != nil {
		s.fail("three-state stream", err)
	}
	if row == nil {
		t.Fatal("three-state row never streamed")
	}

	// JSON null: a value, not SQL NULL.
	if raw, ok := row["j"].(json.RawMessage); !ok || string(raw) != "null" {
		t.Fatalf("jsonb JSON-null = %#v, want json.RawMessage(\"null\")", row["j"])
	}
	if row["j_is_null"] != false || row["j_is_json_null"] != true || row["j_type"] != "null" {
		t.Fatalf("server witnesses disagree with JSON-null: %#v", row)
	}
	// SQL NULL: nil, never a zero value.
	if row["jj"] != nil || row["jj_is_null"] != true {
		t.Fatalf("SQL-null json column = %#v / witness %#v, want nil / true", row["jj"], row["jj_is_null"])
	}
	// The text "null" is a string value, distinct from both nulls.
	if row["t"] != "null" || row["t_is_null"] != false {
		t.Fatalf("text \"null\" = %#v / witness %#v, want \"null\" / false", row["t"], row["t_is_null"])
	}
	// Missing: the omitted argument's column is SQL NULL with no JSON type.
	if row["ab"] != nil || row["ab_is_null"] != true || row["ab_type"] != nil {
		t.Fatalf("missing-column state = %#v, want nil / true / nil", row)
	}

	// Canonicalization stays in Go: the driver's own coercion (float64 into
	// an int8 context is accepted by pgx at the wire level) must be refused
	// by the adapter before the pool is touched.
	counting := &countingQuerier{inner: pool}
	countingExecutor, err := NewExecutor(counting)
	if err != nil {
		s.fail("wrap counting executor", err)
	}
	err = countingExecutor.SourceRows(s.ctx, execution.RowsRequest{Command: pgSQL(t, `SELECT id FROM `+s.table("exec01_states")+` WHERE id = $1`,
		command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: float64(1)}), DeclaredColumns: []string{"id"}}, func(map[string]any) error { return nil })
	requireNamed(t, err, ErrInvalidParameter, "argument 1", `"bigint"`, "must carry an int64")
	if counting.calls != 0 {
		t.Fatalf("invalid parameter reached the pool (%d calls)", counting.calls)
	}
}

// countingQuerier counts delegations so offline-style no-network assertions
// can run against the real pool.
type countingQuerier struct {
	inner Querier
	calls int
}

func (q *countingQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	q.calls++
	return q.inner.Query(ctx, sql, args...)
}

// ---------------------------------------------------------------------------
// Column contract against the real server, with positive alias proof.
// ---------------------------------------------------------------------------

func TestExec01ColumnContractAgainstRealServer(t *testing.T) {
	s := newExec01Service(t)
	pool := s.pool(t, 3, "contract")
	executor := s.exec(t, pool)

	if _, err := s.iso.Exec(s.ctx, `CREATE TABLE `+s.table("exec01_named")+`(id bigint PRIMARY KEY, label text NOT NULL)`); err != nil {
		s.fail("create contract fixture", err)
	}
	if _, err := s.iso.Exec(s.ctx, `INSERT INTO `+s.table("exec01_named")+`(id, label) VALUES (1, 'a')`); err != nil {
		s.fail("insert contract row", err)
	}
	sqlText := `SELECT id, label FROM ` + s.table("exec01_named")

	visits := 0
	visit := func(map[string]any) error { visits++; return nil }

	// Wrong-name probe: server-folded lowercase vs a declared capital name.
	err := executor.SourceRows(s.ctx, execution.RowsRequest{Command: pgSQL(t, sqlText), DeclaredColumns: []string{"ID", "label"}}, visit)
	requireNamed(t, err, ErrColumnContractMismatch, `column 1 is "id", contract declares "ID"`)

	// Wrong-count probes both directions.
	err = executor.SourceRows(s.ctx, execution.RowsRequest{Command: pgSQL(t, sqlText), DeclaredColumns: []string{"id"}}, visit)
	requireNamed(t, err, ErrColumnContractMismatch, "returns 2 column(s), contract declares 1")
	err = executor.SourceRows(s.ctx, execution.RowsRequest{Command: pgSQL(t, sqlText), DeclaredColumns: []string{"id", "label", "extra"}}, visit)
	requireNamed(t, err, ErrColumnContractMismatch, "returns 2 column(s), contract declares 3")
	if visits != 0 {
		t.Fatalf("broken contracts visited %d rows, want 0", visits)
	}

	// Positive pin that the contract compares the server's actual names: a
	// quoted mixed-case alias that matches the declaration streams.
	alias := `SELECT id AS "Id", label AS "Label" FROM ` + s.table("exec01_named")
	aliased := map[string]any{}
	if err := executor.SourceRows(s.ctx, execution.RowsRequest{Command: pgSQL(t, alias), DeclaredColumns: []string{"Id", "Label"}}, func(row map[string]any) error {
		aliased = row
		return nil
	}); err != nil {
		s.fail("quoted-alias contract", err)
	}
	if aliased["Id"] != int64(1) || aliased["Label"] != "a" {
		t.Fatalf("quoted-alias row = %#v", aliased)
	}
}

// ---------------------------------------------------------------------------
// Two-real-connection races: distinct pg_backend_pid, >=10 rounds per
// surface, byte-identical witnesses, post-abort convergence.
// ---------------------------------------------------------------------------

func TestExec01TwoRealConnectionRaces(t *testing.T) {
	s := newExec01Service(t)
	pool := s.pool(t, 6, "race")
	executor := s.exec(t, pool)

	if _, err := s.iso.Exec(s.ctx, `CREATE TABLE `+s.table("exec01_race")+`(
		id bigint PRIMARY KEY, label text NOT NULL, score double precision NOT NULL)`); err != nil {
		s.fail("create race fixture", err)
	}
	if _, err := s.iso.Exec(s.ctx, `INSERT INTO `+s.table("exec01_race")+`(id, label, score) VALUES
		(1, 'alpha', 1.5), (2, 'beta', -0.25), (3, 'gamma', 7)`); err != nil {
		s.fail("insert race rows", err)
	}

	raceSQL := `SELECT pg_backend_pid() AS backend, id, label, score FROM ` + s.table("exec01_race") + ` ORDER BY id`
	raceColumns := []string{"backend", "id", "label", "score"}

	// streamRace streams the golden table and reports the executing backend
	// pid plus a byte-identical digest of the pinned columns.
	streamRace := func(ctx context.Context) (int64, string, error) {
		var backend int64
		var lines []string
		err := executor.SourceRows(ctx, execution.RowsRequest{Command: pgSQL(t, raceSQL), DeclaredColumns: raceColumns}, func(row map[string]any) error {
			pid, ok := row["backend"].(int32)
			if !ok {
				return fmt.Errorf("backend column decoded as %#v", row["backend"])
			}
			id, _ := row["id"].(int64)
			label, _ := row["label"].(string)
			score, ok := row["score"].(float64)
			if !ok {
				return fmt.Errorf("score decoded as %#v", row["score"])
			}
			backend = int64(pid)
			lines = append(lines, fmt.Sprintf("%d|%s|%s", id, label, strconv.FormatFloat(score, 'f', -1, 64)))
			return nil
		})
		if err != nil {
			return 0, "", err
		}
		return backend, strings.Join(lines, ";"), nil
	}

	requireZeroIdleInTx(t, s)

	t.Run("surface A: concurrent readers vs an aborted writer", func(t *testing.T) {
		writer, err := pool.Acquire(s.ctx)
		if err != nil {
			s.fail("acquire writer", err)
		}
		defer writer.Release()
		writerExecutor, err := NewExecutor(writer)
		if err != nil {
			s.fail("writer executor", err)
		}

		for round := 0; round < 10; round++ {
			if _, err := writer.Exec(s.ctx, `BEGIN`); err != nil {
				s.fail("writer begin", err)
			}
			if _, err := writer.Exec(s.ctx, `UPDATE `+s.table("exec01_race")+` SET score = score + 1000 WHERE id = 1`); err != nil {
				s.fail("writer update", err)
			}
			// Load-bearing witness: the uncommitted update is real — the
			// writer's own stream sees the dirty digest. Without this pin a
			// no-op UPDATE would make the whole surface vacuous.
			var dirtyLines []string
			if err := writerExecutor.SourceRows(s.ctx, execution.RowsRequest{Command: pgSQL(t, raceSQL), DeclaredColumns: raceColumns}, func(row map[string]any) error {
				id, _ := row["id"].(int64)
				label, _ := row["label"].(string)
				score, _ := row["score"].(float64)
				dirtyLines = append(dirtyLines, fmt.Sprintf("%d|%s|%s", id, label, strconv.FormatFloat(score, 'f', -1, 64)))
				return nil
			}); err != nil {
				s.fail("writer in-transaction stream", err)
			} else if digest := strings.Join(dirtyLines, ";"); digest != goldenRaceDigestDirty {
				s.fail("writer dirty digest", fmt.Errorf("digest = %q, want %q", digest, goldenRaceDigestDirty))
			}

			type result struct {
				backend int64
				digest  string
				err     error
			}
			start := make(chan struct{})
			results := make(chan result, 2)
			for i := 0; i < 2; i++ {
				go func() {
					<-start
					backend, digest, err := streamRace(s.ctx)
					results <- result{backend, digest, err}
				}()
			}
			close(start)
			first := <-results
			second := <-results
			if _, err := writer.Exec(s.ctx, `ROLLBACK`); err != nil {
				s.fail("writer rollback", err)
			}

			for _, r := range []result{first, second} {
				if r.err != nil {
					s.fail("reader during aborted writer", r.err)
				}
				// Byte-identical post-abort convergence to the pinned
				// committed witness: the aborted write never surfaced.
				if r.digest != goldenRaceDigest {
					t.Fatalf("round %d: reader digest = %q, want %q", round, r.digest, goldenRaceDigest)
				}
			}
			// Two real connections: distinct backends witnessed inside the
			// streamed rows, and neither is the writer's backend.
			if first.backend == second.backend {
				t.Fatalf("round %d: both readers ran on backend %d; two real connections required", round, first.backend)
			}
			var writerPID int64
			if err := writer.QueryRow(s.ctx, `SELECT pg_backend_pid()`).Scan(&writerPID); err != nil {
				s.fail("writer pid", err)
			}
			if first.backend == writerPID || second.backend == writerPID {
				t.Fatalf("round %d: a reader shared the writer backend %d", round, writerPID)
			}
		}
		requireZeroIdleInTx(t, s)
	})

	t.Run("surface B: cancelled sibling converges to the same witness", func(t *testing.T) {
		type result struct {
			backend int64
			digest  string
			err     error
		}
		for round := 0; round < 10; round++ {
			sibConn, err := pool.Acquire(s.ctx)
			if err != nil {
				s.fail("acquire sibling connection", err)
			}
			var sibBackend int64
			if err := sibConn.QueryRow(s.ctx, `SELECT pg_backend_pid()`).Scan(&sibBackend); err != nil {
				sibConn.Release()
				s.fail("sibling backend pid", err)
			}
			sibExecutor, err := NewExecutor(sibConn)
			if err != nil {
				sibConn.Release()
				s.fail("sibling executor", err)
			}

			cctx, ccancel := context.WithCancel(s.ctx)
			siblingErr := make(chan error, 1)
			go func() {
				defer ccancel()
				err := sibExecutor.SourceRows(cctx, execution.RowsRequest{Command: pgSQL(t,
					`SELECT /*exec01-sibling*/ pg_sleep(0.02) IS NULL AS tick, g FROM generate_series(1::bigint, 30::bigint) g`), DeclaredColumns: []string{"tick", "g"}}, func(map[string]any) error { return nil })
				siblingErr <- err
			}()

			// The sibling must be executing on the server while the
			// survivor streams; otherwise the surface is vacuous.
			s.waitForActive(t, exec01Prefix+"race", "exec01-sibling")
			r := <-func() chan result {
				ch := make(chan result, 1)
				go func() {
					backend, digest, err := streamRace(s.ctx)
					ch <- result{backend, digest, err}
				}()
				return ch
			}()
			ccancel()

			sibErr := <-siblingErr
			sibConn.Release()
			if !errors.Is(sibErr, context.Canceled) || !errors.Is(sibErr, ErrQuery) {
				t.Fatalf("round %d: cancelled sibling error not context-compatible: %v", round, sibErr)
			}
			if r.err != nil {
				s.fail("sibling race reader", r.err)
			}
			if r.digest != goldenRaceDigest {
				t.Fatalf("round %d: survivor digest = %q, want byte-identical %q", round, r.digest, goldenRaceDigest)
			}
			if r.backend == 0 {
				t.Fatalf("round %d: survivor backend pid never witnessed", round)
			}
			// Two real connections: the survivor ran somewhere other than
			// the sibling's (cancelled) backend.
			if r.backend == sibBackend {
				t.Fatalf("round %d: survivor shared the sibling backend %d", round, sibBackend)
			}
		}
		requireZeroIdleInTx(t, s)
	})
}

func requireZeroIdleInTx(t *testing.T, s *exec01Service) {
	t.Helper()
	var stuck int64
	if err := s.admin.QueryRow(s.ctx,
		`SELECT count(*) FROM pg_stat_activity WHERE application_name LIKE 'verify\_exec01%' ESCAPE '\' AND state = 'idle in transaction'`).
		Scan(&stuck); err != nil {
		s.fail("count idle-in-transaction sessions", err)
	}
	if stuck != 0 {
		t.Fatalf("%d verify_exec01 session(s) stuck idle in transaction after abort convergence", stuck)
	}
}

// ---------------------------------------------------------------------------
// Crash-window injection at every durability seam, followed by convergence.
// ---------------------------------------------------------------------------

func TestExec01CrashWindowAtEverySeam(t *testing.T) {
	s := newExec01Service(t)

	if _, err := s.iso.Exec(s.ctx, `CREATE TABLE `+s.table("exec01_crash")+`(id bigint PRIMARY KEY, label text NOT NULL)`); err != nil {
		s.fail("create crash fixture", err)
	}
	if _, err := s.iso.Exec(s.ctx, `INSERT INTO `+s.table("exec01_crash")+`(id, label) VALUES (1, 'a'), (2, 'b'), (3, 'c')`); err != nil {
		s.fail("insert crash rows", err)
	}

	terminate := func(pid int64) {
		t.Helper()
		var killed bool
		if err := s.admin.QueryRow(s.ctx, `SELECT pg_terminate_backend($1)`, pid).Scan(&killed); err != nil {
			s.fail("pg_terminate_backend", err)
		}
		if !killed {
			t.Fatalf("pg_terminate_backend(%d) reported no kill", pid)
		}
		// Convergence: the murdered session must vanish.
		deadline := time.Now().Add(10 * time.Second)
		for {
			var alive int64
			if err := s.admin.QueryRow(s.ctx, `SELECT count(*) FROM pg_stat_activity WHERE pid = $1`, pid).Scan(&alive); err != nil {
				s.fail("poll terminated session", err)
			}
			if alive == 0 {
				return
			}
			if time.Now().After(deadline) {
				s.fail("terminated session never vanished", fmt.Errorf("pid %d still visible", pid))
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	// convergenceWitness is the byte-identical post-crash witness, streamed
	// on a fresh pool after every kill.
	convergenceWitness := func(t *testing.T, seam string) {
		t.Helper()
		witnessPool := s.pool(t, 1, "crash-witness")
		witnessExecutor, err := NewExecutor(witnessPool)
		if err != nil {
			s.fail("witness executor", err)
		}
		var digest strings.Builder
		if err := witnessExecutor.SourceRows(s.ctx, execution.RowsRequest{Command: pgSQL(t, `SELECT id, label FROM `+s.table("exec01_crash")+` ORDER BY id`), DeclaredColumns: []string{"id", "label"}}, func(row map[string]any) error {
			id, _ := row["id"].(int64)
			label, _ := row["label"].(string)
			digest.WriteString(fmt.Sprintf("%d|%s;", id, label))
			return nil
		}); err != nil {
			s.fail("post-"+seam+"-convergence stream", err)
		}
		if want := "1|a;2|b;3|c;"; digest.String() != want {
			t.Fatalf("post-%s witness = %q, want byte-identical %q", seam, digest.String(), want)
		}
		requireZeroIdleInTx(t, s)
	}

	t.Run("seam: terminate an in-flight execution", func(t *testing.T) {
		seamPool := s.pool(t, 2, "crash-inflight")
		seamExecutor, err := NewExecutor(seamPool)
		if err != nil {
			s.fail("in-flight executor", err)
		}
		tagged := `SELECT /*exec01-terminate-inflight*/ pg_sleep(0.3) IS NOT NULL AS tick, g FROM generate_series(1::bigint, 15::bigint) g`
		errc := make(chan error, 1)
		go func() {
			err := seamExecutor.SourceRows(s.ctx, execution.RowsRequest{Command: pgSQL(t, tagged), DeclaredColumns: []string{"tick", "g"}}, func(map[string]any) error { return nil })
			errc <- err
		}()
		pid := s.waitForActive(t, exec01Prefix+"crash-inflight", "exec01-terminate-inflight")
		terminate(pid)
		err = <-errc
		if !errors.Is(err, ErrQuery) {
			t.Fatalf("in-flight termination lost the class: %v", err)
		}
		var translated *storepg.TranslatedError
		if !errors.As(err, &translated) || translated.Condition != storepg.ConditionAdminShutdown || translated.SQLState != "57P01" {
			t.Fatalf("in-flight termination lost its boundary classification: %v", err)
		}
		var pgErr *pgconn.PgError
		if !errors.As(translated.Cause(), &pgErr) || pgErr.SQLState() != "57P01" {
			t.Fatalf("in-flight termination lost its cause: %v", err)
		}
		convergenceWitness(t, "in-flight")
	})

	t.Run("seam: visitor stop while the backend dies", func(t *testing.T) {
		seamPool := s.pool(t, 2, "crash-visitor")
		conn, err := seamPool.Acquire(s.ctx)
		if err != nil {
			s.fail("acquire visitor stream connection", err)
		}
		defer conn.Release()
		visitorExecutor, err := NewExecutor(conn)
		if err != nil {
			s.fail("visitor executor", err)
		}
		streamSQL := `SELECT pg_backend_pid() AS backend, id, label FROM ` + s.table("exec01_crash") + ` ORDER BY id`
		boom := errors.New("visitor boom during backend death")
		var visits int
		err = visitorExecutor.SourceRows(s.ctx, execution.RowsRequest{Command: pgSQL(t, streamSQL), DeclaredColumns: []string{"backend", "id", "label"}}, func(row map[string]any) error {
			visits++
			if visits == 1 {
				pid32, _ := row["backend"].(int32)
				terminate(int64(pid32))
			}
			return boom
		})
		// The visitor's error keeps priority even when the backend dies
		// under the same row: iteration stops with the visitor's error.
		if err != boom {
			t.Fatalf("visitor error was replaced by the crash: %#v", err)
		}
		if visits != 1 {
			t.Fatalf("visited %d rows, want 1", visits)
		}
		convergenceWitness(t, "visitor-stop")
	})

	t.Run("seam: scan window crash kills a later row", func(t *testing.T) {
		seamPool := s.pool(t, 2, "crash-scan")
		conn, err := seamPool.Acquire(s.ctx)
		if err != nil {
			s.fail("acquire scan stream connection", err)
		}
		defer conn.Release()
		scanExecutor, err := NewExecutor(conn)
		if err != nil {
			s.fail("scan executor", err)
		}
		// Large rows are flushed as the server produces them, so the stream
		// really is live while row 1 is being visited: 1.6MB rows behind a
		// 300ms per-row sleep.
		streamSQL := `SELECT pg_backend_pid() AS backend, pg_sleep(0.3) IS NOT NULL AS tick, g, repeat(md5(g::text), 50000) AS blob FROM generate_series(1::bigint, 8::bigint) g`
		var visits int
		err = scanExecutor.SourceRows(s.ctx, execution.RowsRequest{Command: pgSQL(t, streamSQL), DeclaredColumns: []string{"backend", "tick", "g", "blob"}}, func(row map[string]any) error {
			visits++
			if visits == 1 {
				if _, ok := row["blob"].(string); !ok {
					t.Fatalf("blob column decoded as %#v", row["blob"])
				}
				pid32, _ := row["backend"].(int32)
				terminate(int64(pid32))
			}
			return nil
		})
		// Rows after the killed window must fail as a query-seam error with
		// an identifiable cause, never as a silent short read.
		if !errors.Is(err, ErrQuery) || err == nil {
			t.Fatalf("scan-window crash was not classified as a query failure: %v", err)
		}
		if visits < 1 || visits >= 8 {
			t.Fatalf("scan-window crash visited %d rows; the kill never landed mid-stream", visits)
		}
		convergenceWitness(t, "scan-window")
	})

	t.Run("seam: query start against a dead connection", func(t *testing.T) {
		seamPool := s.pool(t, 2, "crash-start")
		conn, err := seamPool.Acquire(s.ctx)
		if err != nil {
			s.fail("acquire doomed connection", err)
		}
		defer conn.Release()
		var pid int64
		if err := conn.QueryRow(s.ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
			s.fail("pid of doomed connection", err)
		}
		// Murder the backend while the handle is still held, then start a
		// stream on it: the failure must be classified at the query seam.
		terminate(pid)
		doomedExecutor, err := NewExecutor(conn)
		if err != nil {
			s.fail("doomed executor", err)
		}
		startErr := doomedExecutor.SourceRows(s.ctx, execution.RowsRequest{Command: pgSQL(t, `SELECT g FROM generate_series(1::bigint, 3::bigint) g`), DeclaredColumns: []string{"g"}}, func(map[string]any) error { return nil })
		if startErr == nil || !errors.Is(startErr, ErrQuery) {
			t.Fatalf("query start on a dead connection was not classified: %v", startErr)
		}
		convergenceWitness(t, "query-start")
	})
}

// ---------------------------------------------------------------------------
// Ownership-scoped zero-leftover audit.
// ---------------------------------------------------------------------------

// TestZZZeroLeftoverExec01Fixtures is the ownership-scoped zero-leftover
// audit for this file. It checks, by exact name only, every database (and
// the schema that lived inside it) this file verified into existence, then
// asserts the count of verify_exec01_* databases has returned to the
// baseline captured before this process created anything. It never inspects
// other prefixes, so concurrent sibling workers sweeping their own prefixes
// neither race this audit nor trip it.
func TestZZZeroLeftoverExec01Fixtures(t *testing.T) {
	raw := exec01URL(t)
	exec01OwnedMu.Lock()
	owned := make(map[string]string, len(exec01OwnedByName))
	for db, schema := range exec01OwnedByName {
		owned[db] = schema
	}
	exec01OwnedMu.Unlock()
	if len(owned) == 0 {
		t.Skip("this process created no verify_exec01_* fixtures")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect to test service for audit: %v", err)
	}
	defer admin.Close()

	for db, schema := range owned {
		if !strings.HasPrefix(db, exec01Prefix) {
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
				u, err := url.Parse(raw)
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

	// pg_database baseline: only this file creates the verify_exec01_*
	// prefix, so its count must have returned to the pre-test baseline.
	var visible int64
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_database WHERE datname LIKE 'verify\\_exec01\\_%' ESCAPE '\\'").
		Scan(&visible); err != nil {
		t.Fatalf("audit verify_exec01_* database count: %v", err)
	}
	if visible != exec01BaselineCount {
		t.Errorf("verify_exec01_* databases = %d, want baseline %d", visible, exec01BaselineCount)
	}
}
