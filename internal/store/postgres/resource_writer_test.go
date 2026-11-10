package postgres

// Real-PostgreSQL acceptance tests for the bounded, idempotent resource
// batch writer (pg-ingest-01 store surface; check-2:
// go test ./internal/store/postgres -run PostgresResourceWriter -count=1).
//
// Every test drives the real ResourceWriter against the PostgreSQL 18
// service named by LOOM_TEST_POSTGRES_URL through readiness-checked pools.
// Fixtures are literal rows inserted by this file; witnesses are literal
// values read back by direct SQL: typed columns, canonicalized JSONB
// payload texts, scope fingerprints, and the ledger marker are pinned to
// golden literals captured from PostgreSQL 18 itself, not computed by the
// writer under test. Nothing is mocked: the live claims (writes, replays,
// races, commit accounting) all run through the real store engine.
//
// Non-vacuity discipline: every refusal has a paired control that commits
// (a guard that always refused would fail its control), and the replay
// guard is mutation-probed — a tampered ledger digest turns the idempotent
// no-op into the typed identity conflict, proving the replay path really
// reads the digest. Atomicity claims run crash-window fault injection at
// every pre-commit step of the resource path, each followed by a retry
// that must fully converge. Race surfaces run at least ten rounds each on
// two real connections with distinct pg_backend_pid values witnessed
// inside the batch transactions, and every abort converges to a
// byte-identical witness pinned to a literal.
//
// Fixture databases and schemas are uniquely owned as verify_ing01_store_*
// and dropped by the test that created them. The final TestZZ audit is
// ownership-scoped: it checks each exact name this file created and counts
// only its own verify_ing01_store_* prefix against the baseline captured
// before this process created anything, so concurrent sibling workers
// sweeping their own prefixes neither race this audit nor trip it.

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/source"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	ing01StorePrefix = "verify_ing01_store_"
	ing01NameFmt     = ing01StorePrefix + "%d_%s"
)

var ing01StoreNamePattern = regexp.MustCompile(`^verify_ing01_store_[0-9]+_[0-9a-f]+$`)

// Ownership registry: every database this file verified into existence is
// recorded here, and TestZZ asserts each exact name is gone. Ownership
// begins at verified CREATE, exactly as the per-test cleanup drops them.
var (
	ing01OwnedMu     sync.Mutex
	ing01OwnedByName = map[string]string{} // database -> owned schema
)

func ing01Register(db, schema string) {
	ing01OwnedMu.Lock()
	defer ing01OwnedMu.Unlock()
	ing01OwnedByName[db] = schema
}

// ing01Baseline is the count of verify_ing01_store_* databases visible
// before this process created its first fixture. Only this file's tests
// create that prefix, so TestZZ can assert the count returned to it.
var (
	ing01BaselineOnce  sync.Once
	ing01BaselineCount int64
	ing01BaselineErr   error
)

func ing01CaptureBaseline(raw string) {
	ing01BaselineOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		admin, err := pgxpool.New(ctx, raw)
		if err != nil {
			ing01BaselineErr = fmt.Errorf("connect for baseline: %w", err)
			return
		}
		defer admin.Close()
		var count int64
		if err := admin.QueryRow(ctx,
			"SELECT count(*) FROM pg_database WHERE datname LIKE 'verify\\_ing01\\_store\\_%' ESCAPE '\\'").
			Scan(&count); err != nil {
			ing01BaselineErr = fmt.Errorf("count baseline: %w", err)
			return
		}
		ing01BaselineCount = count
	})
}

type ing01Service struct {
	ctx    context.Context
	admin  *pgxpool.Pool // service database
	iso    *pgxpool.Pool // isolated database (direct-SQL witnesses)
	dsn    string        // isolated database DSN (credentials scrubbed on failure)
	schema string
	scrub  func(any) string
	fail   func(step string, err error)
}

func newIngest01Service(t *testing.T) *ing01Service {
	t.Helper()
	raw := testPostgresURL(t)
	ing01CaptureBaseline(raw)
	if ing01BaselineErr != nil {
		t.Fatalf("capture pg_database baseline: %v", ing01BaselineErr)
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

	dbName := fmt.Sprintf(ing01NameFmt, time.Now().UnixNano(), randomHex(t))
	if !ing01StoreNamePattern.MatchString(dbName) {
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

	schema := fmt.Sprintf(ing01NameFmt, time.Now().UnixNano(), randomHex(t))
	if !ing01StoreNamePattern.MatchString(schema) {
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

	ing01Register(dbName, schema)
	return &ing01Service{ctx: ctx, admin: admin, iso: iso, dsn: dsn, schema: schema, scrub: scrub, fail: fail}
}

// migrate applies the full embedded history through the readiness-checked
// pool, so the resource path runs on the real migrations 000002 and 000008.
func (s *ing01Service) migrate(t *testing.T) {
	t.Helper()
	if _, err := MigrateSchema(s.ctx, s.openClient(t, 4), s.schema); err != nil {
		s.fail("MigrateSchema", err)
	}
}

func (s *ing01Service) openClient(t *testing.T, maxConns int32) *Client {
	t.Helper()
	client, err := Open(s.ctx, Config{
		DSN:            s.dsn,
		MaxConns:       maxConns,
		MinConns:       0,
		AcquireTimeout: 30 * time.Second,
	})
	if err != nil {
		s.fail("open client pool", err)
	}
	t.Cleanup(client.Close)
	return client
}

// openAppClient opens a bounded pool whose backends carry app as
// application_name, so pg_stat_activity can identify a specific pool's
// connection during a race.
func (s *ing01Service) openAppClient(t *testing.T, app string, maxConns int32) *Client {
	t.Helper()
	u, err := url.Parse(s.dsn)
	if err != nil {
		s.fail("parse isolated dsn for app client", err)
	}
	q := u.Query()
	q.Set("application_name", app)
	u.RawQuery = q.Encode()
	client, err := Open(s.ctx, Config{
		DSN:            u.String(),
		MaxConns:       maxConns,
		MinConns:       0,
		AcquireTimeout: 30 * time.Second,
	})
	if err != nil {
		s.fail("open app client pool", err)
	}
	t.Cleanup(client.Close)
	return client
}

func (s *ing01Service) newWriterOn(t *testing.T, src ConnSource) *ResourceWriter {
	t.Helper()
	writer, err := NewResourceWriter(src, s.schema)
	if err != nil {
		s.fail("new resource writer", err)
	}
	return writer
}

func (s *ing01Service) newWriter(t *testing.T) *ResourceWriter {
	t.Helper()
	return s.newWriterOn(t, s.openClient(t, 4))
}

func (s *ing01Service) qualified(table string) string {
	return pgx.Identifier{s.schema, table}.Sanitize()
}

func (s *ing01Service) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := s.iso.Exec(s.ctx, sql, args...); err != nil {
		s.fail("fixture exec", err)
	}
}

func (s *ing01Service) mustInt64(t *testing.T, sql string, args ...any) int64 {
	t.Helper()
	var v int64
	if err := s.iso.QueryRow(s.ctx, sql, args...).Scan(&v); err != nil {
		s.fail("witness "+sql, err)
	}
	return v
}

// poolBackendPID witnesses the single backend behind a MaxConns=1 pool by
// dialing it now; every later acquire reuses that same backend.
func (s *ing01Service) poolBackendPID(t *testing.T, client *Client) int64 {
	t.Helper()
	conn, err := client.Acquire(s.ctx)
	if err != nil {
		s.fail("acquire for backend pid", err)
	}
	defer conn.Release()
	var pid int64
	if err := conn.QueryRow(s.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		s.fail("witness pg_backend_pid", err)
	}
	return pid
}

// waitBackendWaiting polls pg_stat_activity until the backend serving
// application app is actively blocked on a lock, and returns its pid.
func (s *ing01Service) waitBackendWaiting(t *testing.T, app string) int64 {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var pid int64
		var waitType, waitEvent *string
		err := s.iso.QueryRow(s.ctx,
			"SELECT pid, wait_event_type, wait_event FROM pg_stat_activity"+
				" WHERE application_name = $1 AND state = 'active'", app).
			Scan(&pid, &waitType, &waitEvent)
		if err == nil && waitType != nil && *waitType == "Lock" {
			return pid
		}
		if time.Now().After(deadline) {
			s.fail("waiting for backend "+app+" to block on a lock",
				fmt.Errorf("backend never entered a lock wait"))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// ---------------------------------------------------------------------------
// Literal fixtures and golden witnesses. The payload texts and fingerprints
// below are the exact values PostgreSQL 18 returns — captured from the
// server and frozen, so JSONB canonicalization or digest drift fails the
// suite loudly instead of silently re-fingerprinting history.
// ---------------------------------------------------------------------------

const (
	ing01Project = "prj-ing01"
	ing01GenA    = "gen-a"

	ing01PayloadA   = `{"resourceType":"Patient","id":"p-1","active":true,"meta":{"tag":null},"link":[{"code":"X"}]}`
	ing01PayloadB   = `{"resourceType":"Patient","id":"p-1","active":false}`
	ing01PayloadKey = `{"id":"p-2","resourceType":"Patient"}`

	// Canonical payload texts exactly as PostgreSQL 18 returns them
	// (JSONB sorts object keys by length, then bytes, and normalizes
	// separators).
	ing01PayloadAText   = `{"id": "p-1", "link": [{"code": "X"}], "meta": {"tag": null}, "active": true, "resourceType": "Patient"}`
	ing01PayloadBText   = `{"id": "p-1", "active": false, "resourceType": "Patient"}`
	ing01PayloadKeyText = `{"id": "p-2", "resourceType": "Patient"}`

	// Golden digests: lowercase hex SHA-256 of the canonical encoding of
	// the pinned golden batches (the exact batch identity, scope, and rows
	// below), captured from BatchWriter's digest.
	ing01GoldenBatchID = "ing01/golden/0"
	ing01GoldenDigest  = "511de6c949331c638d82e53430c523d2fb345b63f53f7bf50f32fa4358aa1ff6"
	ing01LegacyBatchID = "ing01/legacy/0"
	ing01LegacyDigest  = "587b4e7659240ecf902bab11984a63fddd3a2475b227458867e6354c3790f2df"

	// Scope fingerprints: md5 over "type|logical_id|auth_path|payload::text
	// (or <SQL-NULL>)" per row, ordered by (resource_type, logical_id,
	// auth_resource_path) — a plan-independent byte-exact content witness.
	ing01FingerprintA = "ddf4b412b24330dcfdf5f84759ed764e"
	ing01FingerprintB = "d74ad9a14d6bb357e2c93cee012b3ac0"
)

// ing01GoldenRows returns the four-row golden resource set: a nested
// payload with a present-null key, a payload whose input key order differs
// from the canonical order, a SQL NULL payload, and a JSON null document.
// The logical contract carries no storage sequence at all — the storage
// identity is always server-assigned.
func ing01GoldenRows(project, generation string) []source.Resource {
	return []source.Resource{
		{Project: project, DatasetGeneration: generation, ResourceType: "Patient", LogicalID: "p-1",
			AuthResourcePath: "/fhir", Payload: []byte(ing01PayloadA)},
		{Project: project, DatasetGeneration: generation, ResourceType: "Patient", LogicalID: "p-2",
			AuthResourcePath: "", Payload: []byte(ing01PayloadKey)},
		{Project: project, DatasetGeneration: generation, ResourceType: "Observation", LogicalID: "obs-9",
			AuthResourcePath: "/fhir", Payload: nil},
		{Project: project, DatasetGeneration: generation, ResourceType: "Provenance", LogicalID: "prov-0",
			AuthResourcePath: "/fhir", Payload: []byte(`null`)},
	}
}

func ing01GoldenBatch(id, project, generation string) ResourceBatch {
	return ResourceBatch{ID: id, Project: project, DatasetGeneration: generation, Rows: ing01GoldenRows(project, generation)}
}

// ing01BatchDigest recomputes the canonical engine digest of a resource
// batch, so per-round race batches are witnessed against the same encoding
// the ledger stores, while the pinned golden literals guard the encoding
// itself.
func ing01BatchDigest(b ResourceBatch) string {
	return batchDigest(Batch{
		ID:                b.ID,
		Project:           b.Project,
		DatasetGeneration: b.DatasetGeneration,
		Resources:         b.Rows,
	})
}

type ing01LedgerWitness struct {
	digest     string
	resources  int64
	edges      int64
	project    string
	generation string
}

func (s *ing01Service) witnessLedger(t *testing.T, batchID string) (ing01LedgerWitness, bool) {
	t.Helper()
	var w ing01LedgerWitness
	err := s.iso.QueryRow(s.ctx,
		"SELECT digest, resource_count, edge_count, project, dataset_generation FROM "+
			s.qualified(batchLedgerTableName)+" WHERE batch_id = $1", batchID).
		Scan(&w.digest, &w.resources, &w.edges, &w.project, &w.generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return ing01LedgerWitness{}, false
	}
	if err != nil {
		s.fail("witness ledger row", err)
	}
	return w, true
}

func (s *ing01Service) countIn(t *testing.T, project, generation string) int64 {
	t.Helper()
	return s.mustInt64(t, "SELECT count(*) FROM "+s.qualified(resourceTableName)+
		" WHERE project = $1 AND dataset_generation = $2", project, generation)
}

// scopeFingerprint is a byte-exact witness over every stored row of one
// scope: type, logical id, auth path, and the exact JSONB payload text (or
// the SQL-NULL marker), in stable logical order.
func (s *ing01Service) scopeFingerprint(t *testing.T, project, generation string) string {
	t.Helper()
	var fp string
	sql := "SELECT coalesce(md5(string_agg(entry, E'\n' ORDER BY resource_type, logical_id, auth_resource_path)),'EMPTY') FROM (" +
		"SELECT resource_type, logical_id, auth_resource_path," +
		" resource_type || '|' || logical_id || '|' || auth_resource_path || '|' ||" +
		" coalesce(payload::text, '<SQL-NULL>') AS entry" +
		" FROM " + s.qualified(resourceTableName) + " WHERE project = $1 AND dataset_generation = $2) x"
	if err := s.iso.QueryRow(s.ctx, sql, project, generation).Scan(&fp); err != nil {
		s.fail("scope fingerprint", err)
	}
	return fp
}

// requireResourceRow witnesses one stored row's typed columns and exact
// payload text (or SQL NULL) against literals.
func (s *ing01Service) requireResourceRow(t *testing.T, project, generation, rtype, lid, authPath string, wantPayloadText *string) {
	t.Helper()
	var gotAuth string
	var isNull bool
	var payload *string
	err := s.iso.QueryRow(s.ctx,
		"SELECT auth_resource_path, payload IS NULL, payload::text FROM "+s.qualified(resourceTableName)+
			" WHERE project = $1 AND dataset_generation = $2 AND resource_type = $3 AND logical_id = $4",
		project, generation, rtype, lid).Scan(&gotAuth, &isNull, &payload)
	if err != nil {
		s.fail("witness resource row "+rtype+"/"+lid, err)
	}
	if gotAuth != authPath {
		t.Fatalf("resource %s/%s auth path = %q, want %q", rtype, lid, gotAuth, authPath)
	}
	if wantPayloadText == nil {
		if !isNull {
			t.Fatalf("resource %s/%s payload = %v, want SQL NULL", rtype, lid, *payload)
		}
		return
	}
	if isNull {
		t.Fatalf("resource %s/%s payload is SQL NULL, want document %s", rtype, lid, *wantPayloadText)
	}
	if *payload != *wantPayloadText {
		t.Fatalf("resource %s/%s payload text = %s, want %s", rtype, lid, *payload, *wantPayloadText)
	}
}

// requireNothingWritten witnesses that a scope holds no rows and a batch
// identity holds no ledger marker: the whole-batch rollback contract.
func (s *ing01Service) requireNothingWritten(t *testing.T, project, generation, batchID string) {
	t.Helper()
	if n := s.countIn(t, project, generation); n != 0 {
		t.Fatalf("fhir_resource rows for %s/%s = %d, want 0", project, generation, n)
	}
	if _, found := s.witnessLedger(t, batchID); found {
		t.Fatalf("batch_write_ledger holds %q, want no marker", batchID)
	}
}

// ---------------------------------------------------------------------------
// Surface contract without a database.
// ---------------------------------------------------------------------------

func TestPostgresResourceWriterConstructorRejectsInvalidInputs(t *testing.T) {
	if _, err := NewResourceWriter(nil, "verify_ing01_store_ok"); err == nil {
		t.Fatal("nil connection source accepted, want refusal")
	}
	raw := testPostgresURL(t)
	client, err := Open(context.Background(), Config{DSN: raw, MaxConns: 1, MinConns: 0, AcquireTimeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("open client: %v", err)
	}
	defer client.Close()
	for _, bad := range []string{"", "BadName", "1leading", strings.Repeat("s", 64), "semi;colon"} {
		if _, err := NewResourceWriter(client, bad); !errors.Is(err, ErrInvalidSchemaName) {
			t.Fatalf("schema %q error = %v, want ErrInvalidSchemaName", bad, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Exact writes, ledger accounting, and the golden literals.
// ---------------------------------------------------------------------------

func TestPostgresResourceWriterWritesBoundedBatchWithExactColumnsAndLedgerMarker(t *testing.T) {
	s := newIngest01Service(t)
	s.migrate(t)
	writer := s.newWriter(t)

	batch := ing01GoldenBatch(ing01GoldenBatchID, ing01Project, ing01GenA)
	res, err := writer.WriteResourceBatch(s.ctx, batch)
	if err != nil {
		s.fail("WriteResourceBatch", err)
	}
	if res.BatchID != batch.ID || res.Replay || res.Committed != 4 || res.SkippedIdentical != 0 {
		t.Fatalf("result = %+v, want the golden 4/0 non-replay accounting", res)
	}
	if res.Digest != ing01GoldenDigest {
		t.Fatalf("digest = %s, want the pinned golden %s", res.Digest, ing01GoldenDigest)
	}
	// The same batch through the engine's own digest function must produce
	// the identical pinned literal: the resource surface fingerprints
	// exactly what the engine commits.
	if got := ing01BatchDigest(batch); got != ing01GoldenDigest {
		t.Fatalf("engine digest = %s, want the pinned golden %s", got, ing01GoldenDigest)
	}

	// Direct-SQL witnesses over literal expected rows: typed columns and
	// exact canonical JSONB texts, including SQL NULL versus JSON null.
	s.requireResourceRow(t, ing01Project, ing01GenA, "Patient", "p-1", "/fhir", strPtr(ing01PayloadAText))
	s.requireResourceRow(t, ing01Project, ing01GenA, "Patient", "p-2", "", strPtr(ing01PayloadKeyText))
	s.requireResourceRow(t, ing01Project, ing01GenA, "Observation", "obs-9", "/fhir", nil)
	s.requireResourceRow(t, ing01Project, ing01GenA, "Provenance", "prov-0", "/fhir", strPtr("null"))
	if n := s.countIn(t, ing01Project, ing01GenA); n != 4 {
		t.Fatalf("fhir_resource rows = %d, want 4", n)
	}
	if fp := s.scopeFingerprint(t, ing01Project, ing01GenA); fp != ing01FingerprintA {
		t.Fatalf("scope fingerprint = %s, want the pinned golden %s", fp, ing01FingerprintA)
	}

	// Storage identity discipline: resource_seq is server-assigned and
	// unique across the four rows.
	seqs := s.mustInt64(t, "SELECT count(DISTINCT resource_seq) FROM "+s.qualified(resourceTableName)+
		" WHERE project = $1 AND dataset_generation = $2", ing01Project, ing01GenA)
	if seqs != 4 {
		t.Fatalf("distinct resource_seq = %d, want 4 server-assigned identities", seqs)
	}

	// The ledger marker carries the exact commit accounting.
	w, found := s.witnessLedger(t, batch.ID)
	if !found {
		t.Fatal("no ledger marker after commit")
	}
	if w.digest != ing01GoldenDigest || w.resources != 4 || w.edges != 0 || w.project != ing01Project || w.generation != ing01GenA {
		t.Fatalf("ledger marker = %+v, want golden digest with 4/0 under %s/%s", w, ing01Project, ing01GenA)
	}
}

func TestPostgresResourceWriterLegacyEmptyGenerationNamespaceCommits(t *testing.T) {
	s := newIngest01Service(t)
	s.migrate(t)
	writer := s.newWriter(t)

	rows := []source.Resource{{
		Project: ing01Project, DatasetGeneration: "", ResourceType: "Patient", LogicalID: "p-1",
		AuthResourcePath: "/fhir", Payload: []byte(ing01PayloadA),
	}}
	batch := ResourceBatch{ID: ing01LegacyBatchID, Project: ing01Project, DatasetGeneration: "", Rows: rows}
	res, err := writer.WriteResourceBatch(s.ctx, batch)
	if err != nil {
		s.fail("legacy WriteResourceBatch", err)
	}
	if res.Committed != 1 || res.Replay {
		t.Fatalf("legacy result = %+v, want a fresh single-row commit", res)
	}
	if res.Digest != ing01LegacyDigest || ing01BatchDigest(batch) != ing01LegacyDigest {
		t.Fatalf("legacy digest = %s (engine %s), want the pinned literal %s",
			res.Digest, ing01BatchDigest(batch), ing01LegacyDigest)
	}
	s.requireResourceRow(t, ing01Project, "", "Patient", "p-1", "/fhir", strPtr(ing01PayloadAText))
	// The empty generation is its own namespace: it never merges with gen-a.
	if n := s.countIn(t, ing01Project, ing01GenA); n != 0 {
		t.Fatalf("gen-a rows = %d, want 0 — the legacy namespace must not touch gen-a", n)
	}
}

// ---------------------------------------------------------------------------
// Idempotent replay, with the non-vacuity mutation probe.
// ---------------------------------------------------------------------------

func TestPostgresResourceWriterIdenticalReplayIsNoOpAndTamperedDigestRefuses(t *testing.T) {
	s := newIngest01Service(t)
	s.migrate(t)
	writer := s.newWriter(t)

	batchID := "ing01/replay/0"
	batch := ing01GoldenBatch(batchID, ing01Project, ing01GenA)
	first, err := writer.WriteResourceBatch(s.ctx, batch)
	if err != nil {
		s.fail("first WriteResourceBatch", err)
	}
	before := s.scopeFingerprint(t, ing01Project, ing01GenA)

	// Replay of identical committed input: a ledger no-op with the original
	// accounting, byte-identical results on repeated calls.
	for attempt := 0; attempt < 3; attempt++ {
		replay, err := writer.WriteResourceBatch(s.ctx, batch)
		if err != nil {
			s.fail("replay WriteResourceBatch", err)
		}
		if !replay.Replay || replay.Committed != first.Committed || replay.SkippedIdentical != first.SkippedIdentical ||
			replay.Digest != first.Digest || replay.BatchID != first.BatchID {
			t.Fatalf("replay attempt %d = %+v, want a byte-identical replay of %+v", attempt, replay, first)
		}
		if n := s.countIn(t, ing01Project, ing01GenA); n != 4 {
			t.Fatalf("rows after replay %d = %d, want 4 — replay must write nothing", attempt, n)
		}
	}
	if fp := s.scopeFingerprint(t, ing01Project, ing01GenA); fp != before {
		t.Fatalf("scope fingerprint drifted across replays: %s != %s", fp, before)
	}

	// Mutation probe (non-vacuity): the replay path really reads the
	// ledger digest. Tamper it and the same "identical" replay must
	// become the typed identity conflict instead of a silent no-op.
	tampered := strings.Repeat("ab", 32)
	s.exec(t, "UPDATE "+s.qualified(batchLedgerTableName)+" SET digest = $1 WHERE batch_id = $2",
		tampered, batchID)
	_, err = writer.WriteResourceBatch(s.ctx, batch)
	var conflict *BatchIdentityConflictError
	if !errors.As(err, &conflict) {
		s.fail("replay against tampered digest", fmt.Errorf("err = %v, want *BatchIdentityConflictError", err))
	}
	wantDigest := ing01BatchDigest(batch)
	if conflict.BatchID != batchID || conflict.LedgerDigest != tampered || conflict.ProvidedDigest != wantDigest {
		t.Fatalf("identity conflict = %+v, want tampered ledger digest %s against computed digest %s",
			conflict, tampered, wantDigest)
	}
	if fp := s.scopeFingerprint(t, ing01Project, ing01GenA); fp != before {
		t.Fatalf("tampered replay mutated the scope: %s != %s", fp, before)
	}
}

// ---------------------------------------------------------------------------
// Duplicate natural identity with different payload: typed error, prior
// row preserved, whole batch rolled back.
// ---------------------------------------------------------------------------

func TestPostgresResourceWriterDuplicateNaturalIdentityDifferentPayloadRefused(t *testing.T) {
	s := newIngest01Service(t)
	s.migrate(t)
	writer := s.newWriter(t)

	if _, err := writer.WriteResourceBatch(s.ctx, ing01GoldenBatch("ing01/dup/first", ing01Project, ing01GenA)); err != nil {
		s.fail("first commit", err)
	}
	storedFingerprint := s.scopeFingerprint(t, ing01Project, ing01GenA)

	// A second batch re-offers p-1 under the same natural identity with a
	// different payload, plus a brand-new row. The whole batch must roll
	// back with the typed stored-row conflict.
	rows := append(ing01GoldenRows(ing01Project, ing01GenA), source.Resource{
		Project: ing01Project, DatasetGeneration: ing01GenA, ResourceType: "Encounter", LogicalID: "enc-1",
		AuthResourcePath: "/fhir", Payload: []byte(`{"resourceType":"Encounter","id":"enc-1"}`),
	})
	rows[0].Payload = []byte(ing01PayloadB)
	_, err := writer.WriteResourceBatch(s.ctx, ResourceBatch{
		ID: "ing01/dup/second", Project: ing01Project, DatasetGeneration: ing01GenA, Rows: rows,
	})
	var conflict *BatchResourceConflictError
	if !errors.As(err, &conflict) {
		s.fail("duplicate payload batch", fmt.Errorf("err = %v, want *BatchResourceConflictError", err))
	}
	if !errors.Is(err, ErrBatchRowConflict) {
		t.Fatalf("duplicate payload error does not match ErrBatchRowConflict: %v", err)
	}
	if conflict.InBatch {
		t.Fatalf("conflict reports in-batch, want the stored-row path: %+v", conflict)
	}
	if conflict.Project != ing01Project || conflict.DatasetGeneration != ing01GenA ||
		conflict.ResourceType != "Patient" || conflict.LogicalID != "p-1" {
		t.Fatalf("conflict identity = %+v, want %s/%s Patient/p-1", conflict, ing01Project, ing01GenA)
	}

	// The prior payload is preserved byte-identically and the batch's
	// other rows (the fresh Encounter) wrote nothing.
	if fp := s.scopeFingerprint(t, ing01Project, ing01GenA); fp != storedFingerprint {
		t.Fatalf("stored scope drifted after refused batch: %s != %s", fp, storedFingerprint)
	}
	if n := s.mustInt64(t, "SELECT count(*) FROM "+s.qualified(resourceTableName)+
		" WHERE resource_type = 'Encounter' AND logical_id = 'enc-1'"); n != 0 {
		t.Fatalf("refused batch leaked its fresh Encounter row (count %d), want whole-batch rollback", n)
	}
	if _, found := s.witnessLedger(t, "ing01/dup/second"); found {
		t.Fatal("refused batch left a ledger marker, want none")
	}

	// Guard non-vacuity: the same batch with the duplicate payload restored
	// to the stored content commits its new row (identical duplicates
	// collapse), proving the guard refuses content drift, not duplicates.
	rows[0].Payload = []byte(ing01PayloadA)
	res, err := writer.WriteResourceBatch(s.ctx, ResourceBatch{
		ID: "ing01/dup/control", Project: ing01Project, DatasetGeneration: ing01GenA, Rows: rows,
	})
	if err != nil {
		s.fail("control batch", err)
	}
	if res.Committed != 1 || res.SkippedIdentical != 4 {
		t.Fatalf("control result = %+v, want 1 committed (the Encounter) and 4 identical rows skipped", res)
	}
}

func TestPostgresResourceWriterInBatchDuplicateDifferentPayloadRollsBackWholeBatch(t *testing.T) {
	s := newIngest01Service(t)
	s.migrate(t)
	writer := s.newWriter(t)

	// Rows 0 and 2 share the natural identity with different payloads.
	rows := []source.Resource{
		{Project: ing01Project, DatasetGeneration: ing01GenA, ResourceType: "Patient", LogicalID: "p-9",
			AuthResourcePath: "/fhir", Payload: []byte(`{"resourceType":"Patient","id":"p-9","v":1}`)},
		{Project: ing01Project, DatasetGeneration: ing01GenA, ResourceType: "Patient", LogicalID: "p-10",
			AuthResourcePath: "", Payload: []byte(`{"resourceType":"Patient","id":"p-10"}`)},
		{Project: ing01Project, DatasetGeneration: ing01GenA, ResourceType: "Patient", LogicalID: "p-9",
			AuthResourcePath: "/fhir", Payload: []byte(`{"resourceType":"Patient","id":"p-9","v":2}`)},
	}
	_, err := writer.WriteResourceBatch(s.ctx, ResourceBatch{
		ID: "ing01/inbatch/bad", Project: ing01Project, DatasetGeneration: ing01GenA, Rows: rows,
	})
	var conflict *BatchResourceConflictError
	if !errors.As(err, &conflict) {
		s.fail("in-batch duplicate batch", fmt.Errorf("err = %v, want *BatchResourceConflictError", err))
	}
	if !conflict.InBatch || conflict.ResourceType != "Patient" || conflict.LogicalID != "p-9" {
		t.Fatalf("in-batch conflict = %+v, want the in-batch Patient/p-9 conflict", conflict)
	}
	s.requireNothingWritten(t, ing01Project, ing01GenA, "ing01/inbatch/bad")

	// Guard non-vacuity: identical in-batch duplicates are legal and
	// collapse to a single published row.
	rows[2].Payload = []byte(`{"resourceType":"Patient","id":"p-9","v":1}`)
	res, err := writer.WriteResourceBatch(s.ctx, ResourceBatch{
		ID: "ing01/inbatch/control", Project: ing01Project, DatasetGeneration: ing01GenA, Rows: rows,
	})
	if err != nil {
		s.fail("in-batch control", err)
	}
	if res.Committed != 2 || res.SkippedIdentical != 1 {
		t.Fatalf("in-batch control = %+v, want 2 committed and 1 collapsed duplicate", res)
	}
	if n := s.countIn(t, ing01Project, ing01GenA); n != 2 {
		t.Fatalf("rows after collapse = %d, want 2", n)
	}
}

func TestPostgresResourceWriterIdenticalRowsAcrossDistinctBatchesAreIdempotent(t *testing.T) {
	s := newIngest01Service(t)
	s.migrate(t)
	writer := s.newWriter(t)

	batchA := ing01GoldenBatch("ing01/cross/a", ing01Project, ing01GenA)
	first, err := writer.WriteResourceBatch(s.ctx, batchA)
	if err != nil {
		s.fail("first batch", err)
	}
	if first.Committed != 4 || first.Digest != ing01BatchDigest(batchA) {
		t.Fatalf("first = %+v, want 4 committed under the engine digest", first)
	}
	// A distinct batch identity re-offering the identical rows: row-level
	// idempotency (not a ledger replay) commits nothing.
	batchB := ing01GoldenBatch("ing01/cross/b", ing01Project, ing01GenA)
	second, err := writer.WriteResourceBatch(s.ctx, batchB)
	if err != nil {
		s.fail("second batch", err)
	}
	if second.Replay {
		t.Fatalf("second = %+v, want row-level idempotency, not a ledger replay", second)
	}
	if second.Committed != 0 || second.SkippedIdentical != 4 {
		t.Fatalf("second = %+v, want 0 committed and 4 identical rows skipped", second)
	}
	if n := s.countIn(t, ing01Project, ing01GenA); n != 4 {
		t.Fatalf("rows after identical second batch = %d, want 4", n)
	}
	// Both markers stand with their own exact accounting.
	if _, found := s.witnessLedger(t, batchA.ID); !found {
		t.Fatal("first batch marker missing")
	}
	if w, found := s.witnessLedger(t, batchB.ID); !found || w.resources != 0 || w.digest != ing01BatchDigest(batchB) {
		t.Fatalf("second marker = %+v (found %v), want its own engine digest with 0 committed rows", w, found)
	}
}

// ---------------------------------------------------------------------------
// Pre-round-trip refusals: exact named errors with nothing written, and a
// closed pool proving the refusal happens before a connection is acquired.
// ---------------------------------------------------------------------------

func TestPostgresResourceWriterRejectsInvalidBatchesBeforeRoundTrip(t *testing.T) {
	s := newIngest01Service(t)
	s.migrate(t)

	// A closed pool fails any Acquire, so a validation error that outranks
	// the connectivity failure proves the refusal happened before any
	// round trip.
	closed := s.openClient(t, 1)
	closed.Close()
	writer := s.newWriterOn(t, closed)

	good := ing01GoldenRows(ing01Project, "gen-reject")
	mutate := func(i int, f func(*source.Resource)) []source.Resource {
		rows := ing01GoldenRows(ing01Project, "gen-reject")
		f(&rows[i])
		return rows
	}
	overCapacity := make([]source.Resource, 0, MaxBatchResources+1)
	for i := 0; i <= MaxBatchResources; i++ {
		overCapacity = append(overCapacity, source.Resource{
			Project: ing01Project, DatasetGeneration: "gen-over", ResourceType: "Patient",
			LogicalID: fmt.Sprintf("p-%d", i), AuthResourcePath: "/fhir",
		})
	}
	longID := strings.Repeat("x", MaxBatchIDLength+1)

	cases := []struct {
		name     string
		batch    ResourceBatch
		contains string
	}{
		{"blank id", ResourceBatch{ID: "  ", Project: ing01Project, DatasetGeneration: "gen-reject", Rows: good}, "batch id is blank"},
		{"over-long id", ResourceBatch{ID: longID, Project: ing01Project, DatasetGeneration: "gen-reject", Rows: good}, "at most 255"},
		{"blank project", ResourceBatch{ID: "ing01/reject/project", Project: " ", DatasetGeneration: "gen-reject", Rows: good}, "project is blank"},
		{"no rows", ResourceBatch{ID: "ing01/reject/empty", Project: ing01Project, DatasetGeneration: "gen-reject", Rows: nil}, "carries no rows"},
		{"over capacity", ResourceBatch{ID: "ing01/reject/over", Project: ing01Project, DatasetGeneration: "gen-over", Rows: overCapacity}, "want at most 1000"},
		{"foreign row", ResourceBatch{ID: "ing01/reject/foreign", Project: ing01Project, DatasetGeneration: "gen-reject",
			Rows: mutate(0, func(r *source.Resource) { r.DatasetGeneration = "gen-other" })}, "want the batch scope"},
		{"blank logical id", ResourceBatch{ID: "ing01/reject/lid", Project: ing01Project, DatasetGeneration: "gen-reject",
			Rows: mutate(1, func(r *source.Resource) { r.LogicalID = " " })}, "logical id is blank"},
		{"invalid json payload", ResourceBatch{ID: "ing01/reject/json", Project: ing01Project, DatasetGeneration: "gen-reject",
			Rows: mutate(1, func(r *source.Resource) { r.Payload = []byte(`{"broken":`) })}, "not valid JSON"},
	}
	for _, tc := range cases {
		_, err := writer.WriteResourceBatch(s.ctx, tc.batch)
		if !errors.Is(err, ErrInvalidBatch) {
			t.Fatalf("%s: err = %v, want ErrInvalidBatch", tc.name, err)
		}
		if !strings.Contains(err.Error(), tc.contains) {
			t.Fatalf("%s: err = %v, want it to name %q", tc.name, err, tc.contains)
		}
		// Not a connectivity failure: the closed pool was never reached.
		if errors.Is(err, ErrBatchWrite) {
			t.Fatalf("%s: refusal reached the engine: %v", tc.name, err)
		}
	}

	// Direct-SQL witnesses: none of the refused scopes persisted anything.
	for _, generation := range []string{"gen-reject", "gen-over", "gen-other"} {
		s.requireNothingWritten(t, ing01Project, generation, "unused-marker-name")
	}
	for _, id := range []string{"ing01/reject/project", "ing01/reject/empty", "ing01/reject/over",
		"ing01/reject/foreign", "ing01/reject/seq", "ing01/reject/lid", "ing01/reject/json"} {
		if _, found := s.witnessLedger(t, id); found {
			t.Fatalf("refused batch %q left a ledger marker", id)
		}
	}

	// Guard non-vacuity: the identical well-formed batch (same rows the
	// defect cases carried) commits fine through a live pool.
	live := s.newWriter(t)
	res, err := live.WriteResourceBatch(s.ctx, ResourceBatch{
		ID: "ing01/reject/control", Project: ing01Project, DatasetGeneration: "gen-reject", Rows: good,
	})
	if err != nil {
		s.fail("control commit", err)
	}
	if res.Committed != 4 {
		t.Fatalf("control = %+v, want 4 committed", res)
	}
}

// ---------------------------------------------------------------------------
// Crash-window fault injection: batch/ledger atomicity at every pre-commit
// step of the resource path, each followed by a converging retry.
// ---------------------------------------------------------------------------

func TestPostgresResourceWriterCrashWindowAtEveryStepBeforeCommitLeavesNothing(t *testing.T) {
	s := newIngest01Service(t)
	s.migrate(t)
	writer := s.newWriter(t)

	steps := []string{
		stepBegin, stepReadLedger, stepClaimLedger, stepStageResources,
		stepCheckResources, stepPublishResourc, stepFinalizeLedger, stepCommit,
	}
	for i, step := range steps {
		batch := ing01GoldenBatch(fmt.Sprintf("ing01/crash/%d", i), ing01Project, ing01GenA)
		writer.batch.failHook = func(current string) error {
			if current == step {
				return fmt.Errorf("injected crash before %s", current)
			}
			return nil
		}
		_, err := writer.WriteResourceBatch(s.ctx, batch)
		writer.batch.failHook = nil
		if err == nil {
			t.Fatalf("crash at %s: WriteResourceBatch succeeded, want failure", step)
		}
		if !errors.Is(err, ErrBatchWrite) {
			t.Fatalf("crash at %s: err = %v, want ErrBatchWrite", step, err)
		}
		// The batch and its ledger marker are atomic: neither survived.
		s.requireNothingWritten(t, ing01Project, ing01GenA, batch.ID)

		// Crash-window convergence: the identity was not burned and a
		// retry after the crash fully commits.
		retry, err := writer.WriteResourceBatch(s.ctx, batch)
		if err != nil {
			s.fail(fmt.Sprintf("retry after crash at %s", step), err)
		}
		wantDigest := ing01BatchDigest(batch)
		if retry.Replay || retry.Committed != 4 || retry.Digest != wantDigest {
			t.Fatalf("retry after crash at %s = %+v, want the fresh golden commit under %s", step, retry, wantDigest)
		}
		if n := s.countIn(t, ing01Project, ing01GenA); n != 4 {
			t.Fatalf("rows after retry from crash at %s = %d, want 4", step, n)
		}
		if w, found := s.witnessLedger(t, batch.ID); !found || w.resources != 4 || w.digest != wantDigest {
			t.Fatalf("marker after retry from crash at %s = %+v (found %v), want the golden accounting", step, w, found)
		}
		// Clean the scope for the next step's round.
		s.exec(t, "DELETE FROM "+s.qualified(resourceTableName)+" WHERE project = $1 AND dataset_generation = $2",
			ing01Project, ing01GenA)
		s.exec(t, "DELETE FROM "+s.qualified(batchLedgerTableName)+" WHERE batch_id = $1", batch.ID)
	}
}

// ---------------------------------------------------------------------------
// Reported counts change only at commit: two real connections, a lock
// holds the batch open, and the witness sees only the pre-commit state.
// ---------------------------------------------------------------------------

func TestPostgresResourceWriterReportedCountsChangeOnlyAtCommitTwoConnections(t *testing.T) {
	s := newIngest01Service(t)
	s.migrate(t)

	writerClient := s.openAppClient(t, "ing01-commit-w", 1)
	writer := s.newWriterOn(t, writerClient)
	writerPID := s.poolBackendPID(t, writerClient)

	observer, err := pgxpool.New(s.ctx, s.dsn)
	if err != nil {
		s.fail("open observer pool", err)
	}
	t.Cleanup(observer.Close)

	obsConn, err := observer.Acquire(s.ctx)
	if err != nil {
		s.fail("acquire observer connection", err)
	}
	defer obsConn.Release()
	var observerPID int64
	if err := obsConn.QueryRow(s.ctx, "SELECT pg_backend_pid()").Scan(&observerPID); err != nil {
		s.fail("witness observer pid", err)
	}
	if observerPID == writerPID {
		t.Fatalf("observer and writer share backend %d, want two distinct real PG connections", observerPID)
	}

	// The observer holds the resource table exclusively: the batch will
	// block at its first resource-table touch, uncommitted.
	if _, err := obsConn.Exec(s.ctx, "BEGIN"); err != nil {
		s.fail("observer begin", err)
	}
	if _, err := obsConn.Exec(s.ctx, "LOCK TABLE "+s.qualified(resourceTableName)+" IN ACCESS EXCLUSIVE MODE"); err != nil {
		s.fail("observer lock", err)
	}

	batch := ing01GoldenBatch("ing01/commit/visibility", ing01Project, ing01GenA)
	type outcome struct {
		res ResourceBatchResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := writer.WriteResourceBatch(context.Background(), batch)
		done <- outcome{res, err}
	}()

	// The writer backend must be genuinely blocked on the observer's
	// lock — proof the batch is mid-transaction, not committed yet.
	blockedPID := s.waitBackendWaiting(t, "ing01-commit-w")
	if blockedPID != writerPID || blockedPID == observerPID {
		t.Fatalf("blocked backend pid = %d, want the writer backend %d (observer is %d)",
			blockedPID, writerPID, observerPID)
	}

	// Mid-transaction witness from the lock holder itself (a third
	// connection's SELECT would queue behind the same ACCESS EXCLUSIVE
	// lock): the scope still holds zero rows — the persisted count has
	// not moved. The ledger table carries no lock, so it is witnessed
	// from the independent pool.
	var midRows int64
	if err := obsConn.QueryRow(s.ctx, "SELECT count(*) FROM "+s.qualified(resourceTableName)+
		" WHERE project = $1 AND dataset_generation = $2", ing01Project, ing01GenA).Scan(&midRows); err != nil {
		s.fail("mid-transaction row witness", err)
	}
	if midRows != 0 {
		t.Fatalf("rows visible mid-transaction = %d, want 0 — counts may only change at commit", midRows)
	}
	if _, found := s.witnessLedger(t, batch.ID); found {
		t.Fatal("ledger marker visible mid-transaction, want none — marker and rows commit together")
	}

	// Release the lock: the batch proceeds to commit.
	if _, err := obsConn.Exec(s.ctx, "COMMIT"); err != nil {
		s.fail("observer commit", err)
	}
	var o outcome
	select {
	case o = <-done:
	case <-time.After(60 * time.Second):
		s.fail("waiting for batch commit", errors.New("batch never completed after the lock was released"))
	}
	if o.err != nil {
		s.fail("WriteResourceBatch", o.err)
	}
	if o.res.Committed != 4 || o.res.Digest != ing01BatchDigest(batch) {
		t.Fatalf("committed result = %+v, want the golden 4-row commit", o.res)
	}
	// Post-commit: the direct-SQL count moved to exactly the reported
	// number, and only now.
	if n := s.countIn(t, ing01Project, ing01GenA); n != o.res.Committed {
		t.Fatalf("rows after commit = %d, want the reported %d", n, o.res.Committed)
	}
	if w, found := s.witnessLedger(t, batch.ID); !found || w.resources != 4 {
		t.Fatalf("ledger marker = %+v (found %v), want the 4-row accounting after commit", w, found)
	}
}

// ---------------------------------------------------------------------------
// Two-real-connection races. Every surface: at least ten rounds, distinct
// pg_backend_pid witnessed inside each batch transaction, byte-identical
// post-convergence witnesses pinned to literals.
// ---------------------------------------------------------------------------

// ing01RacePair builds two writers on separate single-connection pools and
// captures each batch transaction's pg_backend_pid at the begin step —
// the one step every path, including ledger fast-path replays and lost
// claim races, is guaranteed to reach.
type ing01RacePair struct {
	w1, w2 *ResourceWriter
	pid1   *int64
	pid2   *int64
	mu     sync.Mutex
}

func (s *ing01Service) newRacePair(t *testing.T) *ing01RacePair {
	t.Helper()
	c1 := s.openAppClient(t, fmt.Sprintf("ing01-race-a-%d", time.Now().UnixNano()), 1)
	c2 := s.openAppClient(t, fmt.Sprintf("ing01-race-b-%d", time.Now().UnixNano()), 1)
	// Force each single-connection pool to its one backend now, so the
	// batch transaction is proven to run on the witnessed pid.
	pidA := s.poolBackendPID(t, c1)
	pidB := s.poolBackendPID(t, c2)
	if pidA == pidB || pidA == 0 || pidB == 0 {
		t.Fatalf("race pools share backend %d/%d, want distinct connections", pidA, pidB)
	}
	pair := &ing01RacePair{pid1: new(int64), pid2: new(int64)}
	pair.w1 = s.newWriterOn(t, c1)
	pair.w2 = s.newWriterOn(t, c2)
	install := func(w *ResourceWriter, slot *int64) {
		w.batch.inTxProbe = func(step, _ string, tx pgx.Tx) {
			if step != stepBegin {
				return
			}
			pair.mu.Lock()
			defer pair.mu.Unlock()
			if *slot == 0 {
				var pid int64
				if err := tx.QueryRow(s.ctx, "SELECT pg_backend_pid()").Scan(&pid); err == nil {
					*slot = pid
				}
			}
		}
	}
	install(pair.w1, pair.pid1)
	install(pair.w2, pair.pid2)
	t.Cleanup(func() {
		pair.w1.batch.inTxProbe = nil
		pair.w2.batch.inTxProbe = nil
	})
	return pair
}

type ing01RaceOutcome struct {
	res ResourceBatchResult
	err error
}

// raceBoth launches one batch on each of the pair's two connections,
// released simultaneously by a start barrier.
func (p *ing01RacePair) raceBoth(s *ing01Service, batchA, batchB ResourceBatch) (ing01RaceOutcome, ing01RaceOutcome) {
	p.mu.Lock()
	*p.pid1, *p.pid2 = 0, 0
	p.mu.Unlock()
	start := make(chan struct{})
	ra := make(chan ing01RaceOutcome, 1)
	rb := make(chan ing01RaceOutcome, 1)
	go func() { <-start; res, err := p.w1.WriteResourceBatch(s.ctx, batchA); ra <- ing01RaceOutcome{res, err} }()
	go func() { <-start; res, err := p.w2.WriteResourceBatch(s.ctx, batchB); rb <- ing01RaceOutcome{res, err} }()
	close(start)
	return <-ra, <-rb
}

// requireDistinctBackends witnesses that this round really ran on two
// distinct PG backends (the probes recorded each batch transaction).
func (p *ing01RacePair) requireDistinctBackends(t *testing.T, round int) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if *p.pid1 == 0 || *p.pid2 == 0 || *p.pid1 == *p.pid2 {
		t.Fatalf("round %d backends = (%d, %d), want two distinct pg_backend_pid values",
			round, *p.pid1, *p.pid2)
	}
}

func TestPostgresResourceWriterConcurrentIdenticalBatchCommitsOnceTwoConnections(t *testing.T) {
	const rounds = 12
	s := newIngest01Service(t)
	s.migrate(t)
	pair := s.newRacePair(t)

	for round := 0; round < rounds; round++ {
		generation := fmt.Sprintf("gen-race1-%d", round)
		batch := ing01GoldenBatch(fmt.Sprintf("ing01/race1/%d", round), ing01Project, generation)
		wantDigest := ing01BatchDigest(batch)

		o1, o2 := pair.raceBoth(s, batch, batch)
		pair.requireDistinctBackends(t, round)
		if o1.err != nil {
			s.fail(fmt.Sprintf("round %d connection A", round), o1.err)
		}
		if o2.err != nil {
			s.fail(fmt.Sprintf("round %d connection B", round), o2.err)
		}

		// Exactly one committed fresh, the other replayed the original
		// accounting; the observable accounting is byte-identical.
		fresh, replays := 0, 0
		for _, r := range []ResourceBatchResult{o1.res, o2.res} {
			if r.Replay {
				replays++
			} else {
				fresh++
			}
			if r.Digest != wantDigest || r.Committed != 4 || r.SkippedIdentical != 0 {
				t.Fatalf("round %d result = %+v, want the golden 4-row accounting under digest %s",
					round, r, wantDigest)
			}
		}
		if fresh != 1 || replays != 1 {
			t.Fatalf("round %d had %d fresh and %d replays, want 1/1", round, fresh, replays)
		}
		if o1.res.Digest != o2.res.Digest || o1.res.Committed != o2.res.Committed ||
			o1.res.SkippedIdentical != o2.res.SkippedIdentical || o1.res.BatchID != o2.res.BatchID {
			t.Fatalf("round %d results diverged: %+v vs %+v", round, o1.res, o2.res)
		}

		// Post-convergence witnesses: committed once, marker once.
		if n := s.countIn(t, ing01Project, generation); n != 4 {
			t.Fatalf("round %d rows = %d, want exactly 4 (committed once)", round, n)
		}
		if fp := s.scopeFingerprint(t, ing01Project, generation); fp != ing01FingerprintA {
			t.Fatalf("round %d fingerprint = %s, want the pinned golden %s", round, fp, ing01FingerprintA)
		}
		w, found := s.witnessLedger(t, batch.ID)
		if !found || w.resources != 4 || w.digest != wantDigest {
			t.Fatalf("round %d marker = %+v (found %v), want the golden accounting", round, w, found)
		}
	}
}

func TestPostgresResourceWriterConcurrentIdentityReuseDifferentContentRefusesOneTwoConnections(t *testing.T) {
	const rounds = 12
	s := newIngest01Service(t)
	s.migrate(t)
	pair := s.newRacePair(t)

	for round := 0; round < rounds; round++ {
		generation := fmt.Sprintf("gen-race2-%d", round)
		batchID := fmt.Sprintf("ing01/race2/%d", round)

		contentA := ing01GoldenBatch(batchID, ing01Project, generation)
		contentB := ing01GoldenBatch(batchID, ing01Project, generation)
		contentB.Rows[0].Payload = []byte(ing01PayloadB) // same identity, different payload
		digestA := ing01BatchDigest(contentA)
		digestB := ing01BatchDigest(contentB)

		o1, o2 := pair.raceBoth(s, contentA, contentB)
		pair.requireDistinctBackends(t, round)

		// Exactly one side committed; the other got the typed identity
		// conflict naming the committed digest versus its own.
		type side struct {
			outcome        ing01RaceOutcome
			ownBatch       ResourceBatch
			ownDigest      string
			otherDigest    string
			ownPayload     string
			ownFingerprint string
		}
		sides := []side{
			{outcome: o1, ownBatch: contentA, ownDigest: digestA, otherDigest: digestB,
				ownPayload: ing01PayloadAText, ownFingerprint: ing01FingerprintA},
			{outcome: o2, ownBatch: contentB, ownDigest: digestB, otherDigest: digestA,
				ownPayload: ing01PayloadBText, ownFingerprint: ing01FingerprintB},
		}
		var winner *side
		var loser *side
		for i := range sides {
			var conflict *BatchIdentityConflictError
			switch {
			case errors.As(sides[i].outcome.err, &conflict):
				if loser != nil {
					t.Fatalf("round %d: both sides got identity conflicts", round)
				}
				loser = &sides[i]
				if conflict.BatchID != batchID || conflict.ProvidedDigest != sides[i].ownDigest ||
					conflict.LedgerDigest != sides[i].otherDigest {
					t.Fatalf("round %d conflict = %+v, want provided %s against ledger %s",
						round, conflict, sides[i].ownDigest, sides[i].otherDigest)
				}
			case sides[i].outcome.err != nil:
				s.fail(fmt.Sprintf("round %d unexpected loser classification", round), sides[i].outcome.err)
			default:
				if winner != nil {
					t.Fatalf("round %d: both sides committed", round)
				}
				winner = &sides[i]
			}
		}
		if winner == nil || loser == nil {
			t.Fatalf("round %d outcomes = (%v, %v), want one commit and one *BatchIdentityConflictError",
				round, o1.err, o2.err)
		}
		if winner.outcome.res.Committed != 4 || winner.outcome.res.Digest != winner.ownDigest {
			t.Fatalf("round %d winner result = %+v, want the 4-row commit under %s",
				round, winner.outcome.res, winner.ownDigest)
		}

		// Post-abort convergence, byte-identical to the pinned witness for
		// the winner's content: the loser's content is absent everywhere.
		if fp := s.scopeFingerprint(t, ing01Project, generation); fp != winner.ownFingerprint {
			t.Fatalf("round %d fingerprint = %s, want the winner's pinned %s", round, fp, winner.ownFingerprint)
		}
		s.requireResourceRow(t, ing01Project, generation, "Patient", "p-1", "/fhir", strPtr(winner.ownPayload))
		if n := s.mustInt64(t, "SELECT count(*) FROM "+s.qualified(batchLedgerTableName)+" WHERE batch_id = $1", batchID); n != 1 {
			t.Fatalf("round %d ledger rows for %q = %d, want exactly 1", round, batchID, n)
		}
	}
}

func TestPostgresResourceWriterConcurrentDuplicateIdentityDifferentPayloadConvergesTwoConnections(t *testing.T) {
	const rounds = 12
	s := newIngest01Service(t)
	s.migrate(t)
	pair := s.newRacePair(t)

	// Two candidate payloads for the same natural identity, one per
	// connection, under distinct batch identities. Both converged payload
	// texts are pinned literals.
	aPayload := `{"resourceType":"Patient","id":"p-dup","race":"a"}`
	bPayload := `{"resourceType":"Patient","id":"p-dup","race":"b"}`
	const (
		aPayloadText = `{"id": "p-dup", "race": "a", "resourceType": "Patient"}`
		bPayloadText = `{"id": "p-dup", "race": "b", "resourceType": "Patient"}`
	)

	for round := 0; round < rounds; round++ {
		generation := fmt.Sprintf("gen-race3-%d", round)
		row := func(payload string) source.Resource {
			return source.Resource{Project: ing01Project, DatasetGeneration: generation,
				ResourceType: "Patient", LogicalID: "p-dup", AuthResourcePath: "/fhir", Payload: []byte(payload)}
		}
		// A fresh companion row per batch proves the loser's whole batch
		// rolled back, not just the conflicting row.
		companion := func(name string) source.Resource {
			return source.Resource{Project: ing01Project, DatasetGeneration: generation,
				ResourceType: "Observation", LogicalID: "obs-" + name, AuthResourcePath: "",
				Payload: []byte(`{"resourceType":"Observation","id":"obs-` + name + `"}`)}
		}
		batchA := ResourceBatch{ID: fmt.Sprintf("ing01/race3/a/%d", round), Project: ing01Project,
			DatasetGeneration: generation, Rows: []source.Resource{row(aPayload), companion("a")}}
		batchB := ResourceBatch{ID: fmt.Sprintf("ing01/race3/b/%d", round), Project: ing01Project,
			DatasetGeneration: generation, Rows: []source.Resource{row(bPayload), companion("b")}}

		o1, o2 := pair.raceBoth(s, batchA, batchB)
		pair.requireDistinctBackends(t, round)

		// Exactly one batch committed (2 rows). The other is refused —
		// either the stored-row conflict (its guard saw the committed row)
		// or the exact-accounting divergence (its publish raced the
		// winner's commit). Both are typed, whole-batch rollbacks. The
		// ownCompanion is the row that side contributed under a distinct
		// natural identity, so its fate isolates whole-batch rollback:
		// the loser's own companion must be gone.
		type side struct {
			outcome      ing01RaceOutcome
			batch        ResourceBatch
			payloadText  string
			ownCompanion string
		}
		sides := []side{
			{outcome: o1, batch: batchA, payloadText: aPayloadText, ownCompanion: "obs-a"},
			{outcome: o2, batch: batchB, payloadText: bPayloadText, ownCompanion: "obs-b"},
		}
		var winner, loser *side
		for i := range sides {
			if sides[i].outcome.err == nil {
				if winner != nil {
					t.Fatalf("round %d: both batches committed", round)
				}
				winner = &sides[i]
				continue
			}
			if loser != nil {
				t.Fatalf("round %d: both batches refused (%v / %v)", round, o1.err, o2.err)
			}
			loser = &sides[i]
			if !errors.Is(sides[i].outcome.err, ErrBatchRowConflict) &&
				!errors.Is(sides[i].outcome.err, ErrBatchAccounting) {
				s.fail(fmt.Sprintf("round %d loser classification", round),
					fmt.Errorf("err = %v, want ErrBatchRowConflict or ErrBatchAccounting", sides[i].outcome.err))
			}
		}
		if winner == nil || loser == nil || winner.outcome.res.Committed != 2 {
			t.Fatalf("round %d outcomes = (%+v/%v, %+v/%v), want one 2-row commit and one typed refusal",
				round, o1.res, o1.err, o2.res, o2.err)
		}

		// Post-abort convergence, byte-identical to the winner's pinned
		// witness: one stored payload with exactly the winner's text, and
		// the loser contributed neither its own rows nor its marker.
		s.requireResourceRow(t, ing01Project, generation, "Patient", "p-dup", "/fhir", strPtr(winner.payloadText))
		if n := s.countIn(t, ing01Project, generation); n != 2 {
			t.Fatalf("round %d rows = %d, want exactly 2 — the loser's rows must be rolled back", round, n)
		}
		if n := s.mustInt64(t, "SELECT count(*) FROM "+s.qualified(resourceTableName)+
			" WHERE dataset_generation = $1 AND logical_id = $2", generation, loser.ownCompanion); n != 0 {
			t.Fatalf("round %d loser's own companion row %s survived (%d), want whole-batch rollback",
				round, loser.ownCompanion, n)
		}
		if _, found := s.witnessLedger(t, loser.batch.ID); found {
			t.Fatalf("round %d loser batch %q left a ledger marker", round, loser.batch.ID)
		}
		if _, found := s.witnessLedger(t, winner.batch.ID); !found {
			t.Fatalf("round %d winner batch %q marker missing", round, winner.batch.ID)
		}
	}
}

// ---------------------------------------------------------------------------
// Ownership-scoped zero-leftover audit.
// ---------------------------------------------------------------------------

// TestZZZeroLeftoverPostgresResourceWriterFixtures is the ownership-scoped
// zero-leftover audit for this file. It checks, by exact name only, every
// database (and the schema that lived inside it) this file verified into
// existence, then asserts the count of verify_ing01_store_* databases has
// returned to the baseline captured before this process created anything.
// It never inspects other prefixes, so concurrent sibling workers sweeping
// their own prefixes neither race this audit nor trip it.
func TestZZZeroLeftoverPostgresResourceWriterFixtures(t *testing.T) {
	raw := testPostgresURL(t)
	ing01OwnedMu.Lock()
	owned := make(map[string]string, len(ing01OwnedByName))
	for db, schema := range ing01OwnedByName {
		owned[db] = schema
	}
	ing01OwnedMu.Unlock()
	if len(owned) == 0 {
		t.Skip("this process created no verify_ing01_store_* fixtures")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect to test service for audit: %v", err)
	}
	defer admin.Close()

	for db, schema := range owned {
		if !strings.HasPrefix(db, ing01StorePrefix) {
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

	// pg_database baseline: only this file creates the
	// verify_ing01_store_* prefix, so its count must have returned to the
	// pre-test baseline.
	var visible int64
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_database WHERE datname LIKE 'verify\\_ing01\\_store\\_%' ESCAPE '\\'").
		Scan(&visible); err != nil {
		t.Fatalf("audit verify_ing01_store_* database count: %v", err)
	}
	if visible != ing01BaselineCount {
		t.Errorf("verify_ing01_store_* databases = %d, want baseline %d", visible, ing01BaselineCount)
	}
}
