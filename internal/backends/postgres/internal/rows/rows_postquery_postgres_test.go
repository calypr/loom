package postgres

// pg-exec-03 rows-side acceptance battery: the PostgreSQL execution row
// adapter retains post-query marker maps, ordered arguments, JSONB values,
// and the SQL-NULL / JSON-null distinction exactly as the shared Loom
// materializer upstream expects (internal/dataframe/execution/postquery.go).
// The adapter's own decoding contract is exercised HERE, in the package
// whose code it tests (coordinator ruling on the ticket path substitution:
// the adapter moved from internal/dataframe/postgres/rows.go to this
// package at arch-backend-06 #125). Execution-side materialization legs
// live in internal/dataframe/execution instead.
//
// Live legs drive the real Executor against the PostgreSQL 18 service
// named by LOOM_TEST_POSTGRES_URL through real tagged commands and literal
// inserted rows; witnesses are literal values read back by direct SQL
// (server-canonical jsonb text) compared to what the visitor received.
// Offline legs drive the same Executor through the package's scripted
// querier to pin the driver-buffer discipline for marker documents.
//
// Fixture databases and schemas are uniquely owned as verify_exec03v1rw_*
// — this file's OWN prefix, distinct from the execution-side battery's
// verify_exec03v1_* (PR #129 review): go test runs the two packages as
// concurrent test binaries, so one shared prefix would let each TestZZ
// census sweep the sibling's live fixtures and report false survivors.
// This follows the one-prefix-per-file precedent of the sibling batteries
// (verify_exec04_pv_, verify_exec04_pg_, verify_exec04_srv_,
// verify_exec05_; see the header note in
// internal/backends/postgres/engine_postgres_pg18_test.go on tolerating
// concurrent sibling workers via own-prefix-only reads). Names are
// dropped by the test that created them; the TestZZ audit is
// ownership-scoped, reads only this prefix, and cleanup drops only the
// exact minted names.

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
	"github.com/calypr/loom/internal/dataframe/execution"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const exec03rwPrefix = "verify_exec03v1rw_"

var exec03rwNamePattern = regexp.MustCompile(`^verify_exec03v1rw_[0-9]+_[0-9a-f]+$`)

var (
	exec03rwOwnedMu     sync.Mutex
	exec03rwOwnedByName = map[string]string{}
)

func exec03rwRegister(db, schema string) {
	exec03rwOwnedMu.Lock()
	defer exec03rwOwnedMu.Unlock()
	exec03rwOwnedByName[db] = schema
}

func exec03rwRegistrySnapshot() map[string]string {
	exec03rwOwnedMu.Lock()
	defer exec03rwOwnedMu.Unlock()
	out := make(map[string]string, len(exec03rwOwnedByName))
	for db, schema := range exec03rwOwnedByName {
		out[db] = schema
	}
	return out
}

var (
	exec03rwBaselineOnce  sync.Once
	exec03rwBaselineNames []string
	exec03rwBaselineErr   error
)

func exec03rwCaptureBaseline(raw string) {
	exec03rwBaselineOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		admin, err := pgxpool.New(ctx, raw)
		if err != nil {
			exec03rwBaselineErr = fmt.Errorf("connect for baseline: %w", err)
			return
		}
		defer admin.Close()
		rows, err := admin.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE 'verify\\_exec03v1rw\\_%' ESCAPE '\\'")
		if err != nil {
			exec03rwBaselineErr = fmt.Errorf("read baseline: %w", err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				exec03rwBaselineErr = fmt.Errorf("scan baseline: %w", err)
				return
			}
			exec03rwBaselineNames = append(exec03rwBaselineNames, name)
		}
	})
}

func exec03rwRandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return hex.EncodeToString(b[:])
}

func exec03rwScrubberFor(raw string) (func(any) string, func(string)) {
	secrets := []string{raw}
	if u, err := url.Parse(raw); err == nil && u.User != nil {
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
	return scrub, func(s string) { secrets = append(secrets, s) }
}

type exec03rwService struct {
	ctx    context.Context
	admin  *pgxpool.Pool
	iso    *pgxpool.Pool
	schema string
	scrub  func(any) string
	fail   func(step string, err error)
}

func newExec03rwService(t *testing.T) *exec03rwService {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 row-adapter post-query tests")
	}
	scrub, addSecret := exec03rwScrubberFor(raw)
	exec03rwCaptureBaseline(raw)
	if exec03rwBaselineErr != nil {
		t.Fatalf("capture pg_database baseline: %s", scrub(exec03rwBaselineErr))
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("LOOM_TEST_POSTGRES_URL must be a postgres:// or postgresql:// URL")
	}
	fail := func(step string, err error) { t.Fatalf("%s: %s", step, scrub(err)) }
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	t.Cleanup(admin.Close)

	var versionNum int64
	var version string
	if err := admin.QueryRow(ctx, "SELECT current_setting('server_version_num')::int8, current_setting('server_version')").Scan(&versionNum, &version); err != nil {
		fail("read server version", err)
	}
	// Mirror internal/backends/postgres/internal/store.CheckServerVersion
	// (18 only) without importing the store into this package's tests.
	if versionNum/10000 != 18 {
		t.Fatalf("test service is not PostgreSQL 18: %s", version)
	}

	dbName := fmt.Sprintf("%s%d_%s", exec03rwPrefix, time.Now().UnixNano(), exec03rwRandomHex(t))
	if !exec03rwNamePattern.MatchString(dbName) {
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
	exec03rwRegister(dbName, "")
	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(ctx), `DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %s", scrub(err))
		}
	})

	if q := u.Query(); q.Get("dbname") != "" {
		q.Del("dbname")
		u.RawQuery = q.Encode()
	}
	u.Path = "/" + dbName
	dsn := u.String()
	addSecret(dsn)

	iso, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fail("connect to isolated database", err)
	}
	t.Cleanup(iso.Close)

	schema := fmt.Sprintf("%s%d_%s", exec03rwPrefix, time.Now().UnixNano(), exec03rwRandomHex(t))
	if !exec03rwNamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	exec03rwRegister(dbName, schema)
	t.Cleanup(func() {
		if _, err := iso.Exec(context.WithoutCancel(ctx), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})
	return &exec03rwService{ctx: ctx, admin: admin, iso: iso, schema: schema, scrub: scrub, fail: fail}
}

// Marker documents: the same Loom post-query marker shapes the execution
// materializer consumes (byte-identical to the constants in
// internal/dataframe/execution/engine_postgres_postquery_pg18_test.go).
const (
	exec03rwUUID5Wire = `{"__loom_exact_uuid_operation":"uuid5","__loom_exact_uuid_args":["6ba7b810-9dad-11d1-80b4-00c04fd430c8","www.widgets.com"]}`
	exec03rwUUID3Wire = `{"__loom_exact_uuid_operation":"uuid3","__loom_exact_uuid_args":["6ba7b810-9dad-11d1-80b4-00c04fd430c8","www.widgets.com"]}`
)

// streamViaAdapter runs one tagged statement through the production
// Executor over the caller-owned pool and returns the visited rows.
func (s *exec03rwService) streamViaAdapter(t *testing.T, sql string, args []command.PostgreSQLParameter, columns []string) []map[string]any {
	t.Helper()
	tagged, err := command.NewPostgreSQLCommand(sql, args)
	if err != nil {
		t.Fatalf("NewPostgreSQLCommand: %v", err)
	}
	executor, err := NewExecutor(s.iso)
	if err != nil {
		s.fail("create executor", err)
	}
	var rows []map[string]any
	err = executor.SourceRows(s.ctx, execution.RowsRequest{Command: tagged, DeclaredColumns: columns}, func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		s.fail("SourceRows: "+sql, err)
	}
	return rows
}

// TestExec03RowsMarkerJSONBPreservedVerbatim pins that the adapter hands
// the visitor the server-canonical marker document bytes for a jsonb
// column (and the extracted sub-object for a path projection), including
// the ordered argument array of a marker, so the upstream materializer
// sees the exact marker map and ordered args. The witness is the server's
// own canonicalized jsonb text read by direct SQL.
func TestExec03RowsMarkerJSONBPreservedVerbatim(t *testing.T) {
	s := newExec03rwService(t)
	ctx := s.ctx
	if _, err := s.iso.Exec(ctx, `CREATE TABLE "`+s.schema+`".exec03rw_markers (id bigint PRIMARY KEY, payload jsonb)`); err != nil {
		s.fail("create fixture table", err)
	}
	orderedPair := `{"__loom_postquery_call":"concat","__loom_postquery_args":["a","b"]}`
	swappedPair := `{"__loom_postquery_call":"concat","__loom_postquery_args":["b","a"]}`
	deep := `{"level":{"list":[` + exec03rwUUID5Wire + `,null,1.5]}}`
	for _, row := range []struct {
		id      int64
		payload string
	}{
		{1, `{"gender":` + exec03rwUUID5Wire + `}`},
		{2, `{"op":` + orderedPair + `}`},
		{3, `{"op":` + swappedPair + `}`},
		{4, `{"nested":` + deep + `}`},
	} {
		if _, err := s.iso.Exec(ctx, `INSERT INTO "`+s.schema+`".exec03rw_markers (id, payload) VALUES ($1, $2::jsonb)`, row.id, row.payload); err != nil {
			s.fail("insert fixture row", err)
		}
	}
	rows := s.streamViaAdapter(t,
		`SELECT id, payload FROM "`+s.schema+`".exec03rw_markers ORDER BY id`,
		[]command.PostgreSQLParameter{},
		[]string{"id", "payload"})
	if len(rows) != 4 {
		t.Fatalf("visited %d rows, want 4", len(rows))
	}
	// Server-canonical witnesses read by direct SQL (jsonb canonicalizes
	// key order; the adapter must match it byte-for-byte).
	for _, want := range []struct {
		id     int64
		column string
	}{{1, "gender"}, {2, "op"}, {3, "op"}, {4, "nested"}} {
		var canonical string
		if err := s.iso.QueryRow(ctx, `SELECT (payload #> $1)::text FROM "`+s.schema+`".exec03rw_markers WHERE id = $2`, []string{want.column}, want.id).Scan(&canonical); err != nil {
			s.fail("read canonical witness", err)
		}
		got, ok := rows[want.id-1]["payload"].(json.RawMessage)
		if !ok {
			// Rows are keyed by the declared contract: id and payload.
			t.Fatalf("row %d payload column has Go type %T, want json.RawMessage", want.id, rows[want.id-1]["payload"])
		}
		// Compare the extracted path within the visited document against
		// the server canonical text.
		var visited any
		if err := json.Unmarshal(got, &visited); err != nil {
			t.Fatalf("visited payload is not valid JSON: %v", err)
		}
		doc, ok := visited.(map[string]any)
		if !ok {
			t.Fatalf("visited payload is not an object: %T", visited)
		}
		encoded, err := json.Marshal(doc[want.column])
		if err != nil {
			t.Fatalf("encode visited column: %v", err)
		}
		if string(encoded) != canonical {
			// json.Marshal re-canonicalizes numbers/escapes; a mismatch
			// must be structural, so re-encode the server text through
			// the same codec before comparing.
			var serverValue any
			if err := json.Unmarshal([]byte(canonical), &serverValue); err != nil {
				t.Fatalf("server canonical not decodable: %v", err)
			}
			serverEncoded, err := json.Marshal(serverValue)
			if err != nil || string(serverEncoded) != string(encoded) {
				t.Fatalf("visited %s for id %d = %s, server canonical (re-encoded) = %s (err %v)", want.column, want.id, encoded, serverEncoded, err)
			}
		}
	}
	// Ordered arguments survive decoding: the only difference between rows
	// 2 and 3 is argument order, and the visited bytes must preserve it
	// (the server canonicalizes jsonb, so the check is structural over the
	// visited bytes, not a raw-substring match).
	if string(rows[1]["payload"].(json.RawMessage)) == string(rows[2]["payload"].(json.RawMessage)) {
		t.Fatal("ordered marker arguments collapsed: [a,b] and [b,a] visited identically")
	}
	for i, want := range [][]any{{any("a"), any("b")}, {any("b"), any("a")}} {
		var doc struct {
			Op struct {
				Args []any `json:"__loom_postquery_args"`
			} `json:"op"`
		}
		if err := json.Unmarshal(rows[i+1]["payload"].(json.RawMessage), &doc); err != nil {
			t.Fatalf("row %d visited bytes do not decode: %v", i+2, err)
		}
		if len(doc.Op.Args) != 2 || doc.Op.Args[0] != want[0] || doc.Op.Args[1] != want[1] {
			t.Fatalf("row %d ordered arguments drifted to %#v, want %#v", i+2, doc.Op.Args, want)
		}
	}
}

// TestExec03RowsThreeStateAroundMarkers pins the adapter's three-state
// contract specifically around marker documents: a SQL NULL jsonb column
// visits as untyped nil, a stored JSON null visits as the non-nil bytes
// json.RawMessage("null"), and the two never collapse into each other.
// (The execution-side convergence to nil is the materializer's public
// contract, pinned in internal/dataframe/execution.)
func TestExec03RowsThreeStateAroundMarkers(t *testing.T) {
	s := newExec03rwService(t)
	ctx := s.ctx
	if _, err := s.iso.Exec(ctx, `CREATE TABLE "`+s.schema+`".exec03rw_states (id bigint PRIMARY KEY, payload jsonb)`); err != nil {
		s.fail("create fixture table", err)
	}
	for _, row := range []struct {
		id      int64
		payload any
	}{
		{1, nil}, // SQL NULL
		{2, "null"},
		{3, exec03rwUUID5Wire},
	} {
		if _, err := s.iso.Exec(ctx, `INSERT INTO "`+s.schema+`".exec03rw_states (id, payload) VALUES ($1, $2::jsonb)`, row.id, row.payload); err != nil {
			s.fail("insert fixture row", err)
		}
	}
	rows := s.streamViaAdapter(t,
		`SELECT id, payload FROM "`+s.schema+`".exec03rw_states ORDER BY id`,
		[]command.PostgreSQLParameter{},
		[]string{"id", "payload"})
	if len(rows) != 3 {
		t.Fatalf("visited %d rows, want 3", len(rows))
	}
	if value, present := rows[0]["payload"]; !present || value != nil {
		t.Fatalf("SQL NULL column visited as %#v (present %v), want untyped nil", value, present)
	}
	raw, ok := rows[1]["payload"].(json.RawMessage)
	if !ok {
		t.Fatalf("JSON null column visited as %T, want json.RawMessage", rows[1]["payload"])
	}
	if string(raw) != "null" {
		t.Fatalf("JSON null column bytes = %q, want \"null\"", raw)
	}
	if marker, ok := rows[2]["payload"].(json.RawMessage); !ok || !strings.Contains(string(marker), "__loom_exact_uuid_operation") {
		t.Fatalf("marker column visited as %#v", rows[2]["payload"])
	}
	// Direct-SQL control: the server really stores these three states
	// distinctly (type is null only for the SQL NULL row).
	// COALESCE keeps the SQL-NULL row scannable: jsonb_typeof of a SQL
	// NULL column is itself SQL NULL.
	var sqlNullState, jsonNullState string
	if err := s.iso.QueryRow(ctx, `SELECT COALESCE(jsonb_typeof(payload), '<sql-null>') FROM "`+s.schema+`".exec03rw_states WHERE id = 1`).Scan(&sqlNullState); err != nil {
		s.fail("read sql-null typeof", err)
	}
	if err := s.iso.QueryRow(ctx, `SELECT COALESCE(jsonb_typeof(payload), '<sql-null>') FROM "`+s.schema+`".exec03rw_states WHERE id = 2`).Scan(&jsonNullState); err != nil {
		s.fail("read json-null typeof", err)
	}
	if sqlNullState != "<sql-null>" || jsonNullState != "null" {
		t.Fatalf("server states drifted: sql-null typeof %q, json-null typeof %q", sqlNullState, jsonNullState)
	}
}

// TestExec03RowsMalformedMarkerRefusedServerSide pins that malformed
// marker JSON cannot enter a jsonb column at all (SQLSTATE 22P02 at
// INSERT): the materializer's invalid-JSON refusal upstream is
// defense-in-depth, unreachable from a live PostgreSQL server. The
// well-formed marker is the paired control that proceeds.
func TestExec03RowsMalformedMarkerRefusedServerSide(t *testing.T) {
	s := newExec03rwService(t)
	ctx := s.ctx
	if _, err := s.iso.Exec(ctx, `CREATE TABLE "`+s.schema+`".exec03rw_malformed (id bigint PRIMARY KEY, payload jsonb)`); err != nil {
		s.fail("create fixture table", err)
	}
	_, err := s.iso.Exec(ctx, `INSERT INTO "`+s.schema+`".exec03rw_malformed (id, payload) VALUES ($1, $2::jsonb)`,
		int64(1), `{"__loom_exact_uuid_operation":"uuid5"`)
	if err == nil {
		t.Fatal("malformed marker JSON was accepted into a jsonb column")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.SQLState() != "22P02" {
		s.fail("malformed marker insert must be 22P02", err)
	}
	// ALIVE control: the well-formed marker inserts and streams.
	if _, err := s.iso.Exec(ctx, `INSERT INTO "`+s.schema+`".exec03rw_malformed (id, payload) VALUES ($1, $2::jsonb)`,
		int64(2), exec03rwUUID5Wire); err != nil {
		s.fail("well-formed marker control", err)
	}
	rows := s.streamViaAdapter(t,
		`SELECT payload FROM "`+s.schema+`".exec03rw_malformed WHERE id = 2`,
		[]command.PostgreSQLParameter{},
		[]string{"payload"})
	if len(rows) != 1 || string(rows[0]["payload"].(json.RawMessage)) == "" {
		t.Fatalf("control row = %v", rows)
	}
}

// TestExec03RowsDeepNestedMarkerPassthrough pins that a deeply nested
// marker document (200 levels of legitimate object nesting around the
// marker — well inside the server's jsonb input bound, which accepted
// depth 200 in the probe that pinned this fixture) passes through the
// adapter byte-identically as ONE json.RawMessage: the adapter performs
// no JSON decoding, so depth bounds are owned by the materializer's
// encoding/json decoder, not the row path.
func TestExec03RowsDeepNestedMarkerPassthrough(t *testing.T) {
	s := newExec03rwService(t)
	ctx := s.ctx
	if _, err := s.iso.Exec(ctx, `CREATE TABLE "`+s.schema+`".exec03rw_deep (id bigint PRIMARY KEY, payload jsonb)`); err != nil {
		s.fail("create fixture table", err)
	}
	const depth = 200
	document := `{"a":` + strings.Repeat(`{"a":`, depth) + exec03rwUUID5Wire + strings.Repeat(`}`, depth) + `}`
	if _, err := s.iso.Exec(ctx, `INSERT INTO "`+s.schema+`".exec03rw_deep (id, payload) VALUES ($1, $2::jsonb)`, int64(1), document); err != nil {
		s.fail("insert deep fixture", err)
	}
	rows := s.streamViaAdapter(t,
		`SELECT payload FROM "`+s.schema+`".exec03rw_deep WHERE id = 1`,
		[]command.PostgreSQLParameter{},
		[]string{"payload"})
	if len(rows) != 1 {
		t.Fatalf("rows = %v", rows)
	}
	raw, ok := rows[0]["payload"].(json.RawMessage)
	if !ok {
		t.Fatalf("deep payload visited as %T, want a single json.RawMessage", rows[0]["payload"])
	}
	// The marker survives at full depth: count the nesting in the visited
	// bytes and require the marker inside, so a truncating adapter dies.
	if got := strings.Count(string(raw), `{"a":`); got != depth+1 {
		t.Fatalf("visited document carries %d nesting levels, want %d", got, depth+1)
	}
	if !strings.Contains(string(raw), "__loom_exact_uuid_operation") {
		t.Fatal("marker bytes lost in passthrough")
	}
}

// TestExec03RowsVisitedMarkerDoesNotAliasDriverBuffer is offline (scripted
// querier, no service): the adapter copies the jsonb bytes, so a retained
// visited RawMessage is immune to reuse of the driver-owned buffer — the
// precondition for the upstream materializer safely evaluating a marker
// after the driver has advanced the cursor.
func TestExec03RowsVisitedMarkerDoesNotAliasDriverBuffer(t *testing.T) {
	marker := []byte(exec03rwUUID5Wire)
	rows := &scriptedRows{
		fds:       []pgconn.FieldDescription{jsonbFD("payload")},
		rowValues: [][]any{{marker}},
		shared:    [][]byte{marker},
	}
	querier := &recordingQuerier{rows: rows}
	executor, err := NewExecutor(querier)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	tagged, err := command.NewPostgreSQLCommand(`SELECT payload FROM t`, []command.PostgreSQLParameter{})
	if err != nil {
		t.Fatalf("NewPostgreSQLCommand: %v", err)
	}
	var visited json.RawMessage
	err = executor.SourceRows(context.Background(), execution.RowsRequest{Command: tagged, DeclaredColumns: []string{"payload"}}, func(row map[string]any) error {
		raw, ok := row["payload"].(json.RawMessage)
		if !ok {
			return fmt.Errorf("visited value is %T, want json.RawMessage", row["payload"])
		}
		visited = raw
		return nil
	})
	if err != nil {
		t.Fatalf("SourceRows: %v", err)
	}
	// Corrupt the driver-owned source buffer after the visit; the retained
	// visited bytes must be unchanged.
	for i := range marker {
		marker[i] = 'X'
	}
	if string(visited) != exec03rwUUID5Wire {
		t.Fatalf("retained marker aliased the driver buffer: %s", visited)
	}
}

// TestZZExec03RowsZeroLeftoverFixtures is the ownership-scoped audit for
// this file: every exact name this process created must be gone, and no
// unregistered survivor may remain under this file's own prefix. The
// LIKE read is own-prefix only; nothing here drops by pattern. The
// baseline capture is repeated here (idempotent via sync.Once) so
// running this audit alone with -run still compares against the
// pre-existing databases instead of an empty baseline, which would flag
// any healthy concurrent run as survivors.
func TestZZExec03RowsZeroLeftoverFixtures(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 row-adapter post-query tests")
	}
	scrub, _ := exec03rwScrubberFor(raw)
	exec03rwCaptureBaseline(raw)
	if exec03rwBaselineErr != nil {
		t.Fatalf("capture pg_database baseline for audit: %s", scrub(exec03rwBaselineErr))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect for audit: %s", scrub(err))
	}
	defer admin.Close()
	owned := exec03rwRegistrySnapshot()
	for db := range owned {
		var count int
		if err := admin.QueryRow(ctx, "SELECT count(*) FROM pg_database WHERE datname = $1", db).Scan(&count); err != nil {
			t.Fatalf("audit %s: %s", db, scrub(err))
		}
		if count != 0 {
			t.Errorf("owned fixture database %s survived", db)
		}
	}
	baseline := map[string]bool{}
	for _, name := range exec03rwBaselineNames {
		baseline[name] = true
	}
	rows, err := admin.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE 'verify\\_exec03v1rw\\_%' ESCAPE '\\'")
	if err != nil {
		t.Fatalf("audit sweep: %s", scrub(err))
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("audit scan: %s", scrub(err))
		}
		if !baseline[name] {
			t.Errorf("unregistered survivor under owned prefix: %s", name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("audit rows: %s", scrub(err))
	}
}
