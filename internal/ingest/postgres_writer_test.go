package ingest

// Real-PostgreSQL acceptance tests for the ingest-layer PostgreSQL resource
// writer (pg-ingest-01; check-2:
// go test ./internal/ingest ./internal/store/postgres -run PostgresResourceWriter -count=1).
//
// These tests drive the real PostgresResourceWriter against the PostgreSQL
// 18 service named by LOOM_TEST_POSTGRES_URL. Rows are built by the real
// production row builders (generated and generic, generation-qualified
// through generationRowBuilder) exactly as loadFile builds them, committed
// through the real store batch writer, and witnessed by direct SQL against
// golden literals captured from PostgreSQL 18. Nothing is mocked for a
// live claim: the only double in this file is a recording decorator used
// exclusively for the "refused before any round trip" and batch-content
// capture claims — it never stands in for a commit.
//
// Non-vacuity: every refusal case has a paired control that passes the
// same gate, refusals are proven to stop before the store surface
// (recorder call count) and before the database (zero rows and zero ledger
// markers witnessed by direct SQL on the live stack), and the identity
// checks are fed real builder output. Race surfaces run at least ten
// rounds each on two real single-connection pools whose distinct
// pg_backend_pid values are witnessed, with byte-identical post-convergence
// witnesses.
//
// Fixture databases and schemas are uniquely owned as verify_ing01_ing_*
// and dropped by the test that created them; the TestZZ audit is
// ownership-scoped (exact names) and counts only its own
// verify_ing01_ing_* prefix against the baseline captured before this
// process created anything, so concurrent sibling workers (including the
// store-surface tests under verify_ing01_store_*) neither race nor trip it.

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/catalog"
	postgresstore "github.com/calypr/loom/internal/store/postgres"
	"github.com/bmeg/jsonschemagraph/graph"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	ing01IngestPrefix = "verify_ing01_ing_"
	ing01IngestFmt    = ing01IngestPrefix + "%d_%s"

	ing01IngestProject = "prj-ing01-ing"
	ing01IngestGen     = "gen-build-a"
	ing01IngestAuth    = "/fhir"
)

var ing01IngestNamePattern = regexp.MustCompile(`^verify_ing01_ing_[0-9]+_[0-9a-f]+$`)

func ing01TestPostgresURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 integration tests")
	}
	return raw
}

var ing01IngestOwnedMu sync.Mutex
var ing01IngestOwnedByName = map[string]string{}

func ing01IngestRegister(db, schema string) {
	ing01IngestOwnedMu.Lock()
	defer ing01IngestOwnedMu.Unlock()
	ing01IngestOwnedByName[db] = schema
}

var (
	ing01IngestBaselineOnce  sync.Once
	ing01IngestBaselineCount int64
	ing01IngestBaselineErr   error
)

func ing01IngestCaptureBaseline(raw string) {
	ing01IngestBaselineOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		admin, err := pgxpool.New(ctx, raw)
		if err != nil {
			ing01IngestBaselineErr = fmt.Errorf("connect for baseline: %w", err)
			return
		}
		defer admin.Close()
		var count int64
		if err := admin.QueryRow(ctx,
			"SELECT count(*) FROM pg_database WHERE datname LIKE 'verify\\_ing01\\_ing\\_%' ESCAPE '\\'").
			Scan(&count); err != nil {
			ing01IngestBaselineErr = fmt.Errorf("count baseline: %w", err)
			return
		}
		ing01IngestBaselineCount = count
	})
}

func ing01RandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return fmt.Sprintf("%x", b[:])
}

func str(s string) *string { return &s }

type ing01IngestService struct {
	ctx    context.Context
	admin  *pgxpool.Pool
	iso    *pgxpool.Pool // isolated database (direct-SQL witnesses)
	dsn    string
	schema string
	scrub  func(any) string
	fail   func(step string, err error)
}

func newIngest01IngestService(t *testing.T) *ing01IngestService {
	t.Helper()
	raw := ing01TestPostgresURL(t)
	ing01IngestCaptureBaseline(raw)
	if ing01IngestBaselineErr != nil {
		t.Fatalf("capture pg_database baseline: %v", ing01IngestBaselineErr)
	}

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

	dbName := fmt.Sprintf(ing01IngestFmt, time.Now().UnixNano(), ing01RandomHex(t))
	if !ing01IngestNamePattern.MatchString(dbName) {
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

	schema := fmt.Sprintf(ing01IngestFmt, time.Now().UnixNano(), ing01RandomHex(t))
	if !ing01IngestNamePattern.MatchString(schema) {
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

	ing01IngestRegister(dbName, schema)
	return &ing01IngestService{ctx: ctx, admin: admin, iso: iso, dsn: dsn, schema: schema, scrub: scrub, fail: fail}
}

func (s *ing01IngestService) migrate(t *testing.T) {
	t.Helper()
	client := s.openClient(t, 4)
	if _, err := postgresstore.MigrateSchema(s.ctx, client, s.schema); err != nil {
		s.fail("MigrateSchema", err)
	}
}

func (s *ing01IngestService) openClient(t *testing.T, maxConns int32) *postgresstore.Client {
	t.Helper()
	client, err := postgresstore.Open(s.ctx, postgresstore.Config{
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

func (s *ing01IngestService) openAppClient(t *testing.T, app string, maxConns int32) *postgresstore.Client {
	t.Helper()
	u, err := url.Parse(s.dsn)
	if err != nil {
		s.fail("parse isolated dsn for app client", err)
	}
	q := u.Query()
	q.Set("application_name", app)
	u.RawQuery = q.Encode()
	client, err := postgresstore.Open(s.ctx, postgresstore.Config{
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

func (s *ing01IngestService) newWriterOn(t *testing.T, src postgresstore.ConnSource) *PostgresResourceWriter {
	t.Helper()
	writer, err := NewPostgresResourceWriter(src, s.schema)
	if err != nil {
		s.fail("new postgres resource writer", err)
	}
	return writer
}

func (s *ing01IngestService) newWriter(t *testing.T) *PostgresResourceWriter {
	t.Helper()
	return s.newWriterOn(t, s.openClient(t, 4))
}

func (s *ing01IngestService) qualified(table string) string {
	return pgx.Identifier{s.schema, table}.Sanitize()
}

func (s *ing01IngestService) mustInt64(t *testing.T, sql string, args ...any) int64 {
	t.Helper()
	var v int64
	if err := s.iso.QueryRow(s.ctx, sql, args...).Scan(&v); err != nil {
		s.fail("witness "+sql, err)
	}
	return v
}

func (s *ing01IngestService) countIn(t *testing.T, project, generation string) int64 {
	t.Helper()
	return s.mustInt64(t, "SELECT count(*) FROM "+s.qualified("fhir_resource")+
		" WHERE project = $1 AND dataset_generation = $2", project, generation)
}

func (s *ing01IngestService) requireResourceRow(t *testing.T, project, generation, rtype, lid, authPath string, wantPayloadText *string) {
	t.Helper()
	var gotAuth string
	var isNull bool
	var payload *string
	err := s.iso.QueryRow(s.ctx,
		"SELECT auth_resource_path, payload IS NULL, payload::text FROM "+s.qualified("fhir_resource")+
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

func (s *ing01IngestService) witnessLedger(t *testing.T, batchID string) (digest string, resources int64, found bool) {
	t.Helper()
	err := s.iso.QueryRow(s.ctx,
		"SELECT digest, resource_count FROM "+s.qualified("batch_write_ledger")+" WHERE batch_id = $1", batchID).
		Scan(&digest, &resources)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, false
	}
	if err != nil {
		s.fail("witness ledger row", err)
	}
	return digest, resources, true
}

func (s *ing01IngestService) scopeFingerprint(t *testing.T, project, generation string) string {
	t.Helper()
	var fp string
	sql := "SELECT coalesce(md5(string_agg(entry, E'\n' ORDER BY resource_type, logical_id, auth_resource_path)),'EMPTY') FROM (" +
		"SELECT resource_type, logical_id, auth_resource_path," +
		" resource_type || '|' || logical_id || '|' || auth_resource_path || '|' ||" +
		" coalesce(payload::text, '<SQL-NULL>') AS entry" +
		" FROM " + s.qualified("fhir_resource") + " WHERE project = $1 AND dataset_generation = $2) x"
	if err := s.iso.QueryRow(s.ctx, sql, project, generation).Scan(&fp); err != nil {
		s.fail("scope fingerprint", err)
	}
	return fp
}

func (s *ing01IngestService) poolBackendPID(t *testing.T, client *postgresstore.Client) int64 {
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

func (s *ing01IngestService) waitBackendWaiting(t *testing.T, app string) int64 {
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
// Built-row fixtures: real builders, real generation qualification.
// ---------------------------------------------------------------------------

const (
	ing01ResearchSubjectLine = `{"resourceType":"ResearchSubject","id":"research-subject-1","status":"active","study":{"reference":"ResearchStudy/study-1"},"subject":{"reference":"Patient/patient-1"},"project_id":"source-value","unknown_payload_field":{"nested":["kept",42]}}`
	ing01SubstanceLine       = `{"resourceType":"SubstanceDefinition","id":"substance-1","name":[{"name":"Gemcitabine"}]}`

	// Canonical payload texts witnessed from PostgreSQL 18 (JSONB sorts
	// keys by length then bytes). The generated loader injects
	// project_id over the source value; the generic builder injects
	// project_id and the auth path through extraArgs.
	ing01ResearchSubjectPayloadText = `{"id": "research-subject-1", "study": {"reference": "ResearchStudy/study-1"}, "status": "active", "subject": {"reference": "Patient/patient-1"}, "project_id": "prj-ing01-ing", "resourceType": "ResearchSubject", "unknown_payload_field": {"nested": ["kept", 42]}}`
	ing01SubstancePayloadText       = `{"id": "substance-1", "name": [{"name": "Gemcitabine"}], "project_id": "prj-ing01-ing", "resourceType": "SubstanceDefinition", "auth_resource_path": "/fhir"}`

	// Golden batch digests captured from PostgreSQL 18 through the ledger.
	ing01BuiltBatchID     = "ing01-ing/built/0"
	ing01BuiltBatchDigest = "7fe13c3d615ec75a169f03491708478a30756bb68aa8d7c02bbd3dc0cbc040e2"
	ing01HandBatchID      = "ing01-ing/hand/0"
	ing01HandBatchDigest  = "9bf323bc4bcba52cc891cacce1007c3bebdcabf3c182711d6c4a50fc3de4903f"
)

func ing01LoadGraphSchema(t *testing.T) *graph.GraphSchema {
	t.Helper()
	schema, err := graph.Load(repoPath(t, "schemas", "graph-fhir.json"))
	if err != nil {
		t.Fatalf("load graph schema: %v", err)
	}
	return schema
}

// buildGeneratedRow builds one generation-qualified row with the real
// generated builder, exactly as loadFile wraps it.
func buildGeneratedRow(t *testing.T, project, generation, authPath, resourceType, line string) rowBuildResult {
	t.Helper()
	var builder RowBuilder = NewGeneratedRowBuilder(project, authPath)
	if strings.TrimSpace(generation) != "" {
		var err error
		builder, err = newGenerationRowBuilder(builder, project, generation)
		if err != nil {
			t.Fatalf("newGenerationRowBuilder: %v", err)
		}
	}
	built, errorType, err := builder.Build(resourceType, []byte(line), map[string]float64{})
	if err != nil {
		t.Fatalf("build %s row: kind=%s err=%v", resourceType, errorType, err)
	}
	return built
}

// buildGenericRow builds one generation-qualified row with the real
// generic builder and the repository graph schema.
func buildGenericRow(t *testing.T, project, generation, authPath, resourceType, line string) rowBuildResult {
	t.Helper()
	schema := ing01LoadGraphSchema(t)
	class := schema.GetClass(resourceType)
	if class == nil {
		t.Fatalf("class %q not found", resourceType)
	}
	var builder RowBuilder = NewGenericRowBuilder(project, class, schema, graphExtraArgs(project, authPath))
	if strings.TrimSpace(generation) != "" {
		var err error
		builder, err = newGenerationRowBuilder(builder, project, generation)
		if err != nil {
			t.Fatalf("newGenerationRowBuilder: %v", err)
		}
	}
	built, errorType, err := builder.Build(resourceType, []byte(line), map[string]float64{})
	if err != nil {
		t.Fatalf("build %s row: kind=%s err=%v", resourceType, errorType, err)
	}
	return built
}

// handBuiltVertex assembles a built-vertex row verbatim: the payload bytes
// land exactly as given (or the key is omitted when payload == "").
func handBuiltVertex(project, generation, resourceType, logicalID, authPath, payload string, extraFields map[string]string) rowBuildResult {
	var b strings.Builder
	b.WriteString(`{"_key":"`)
	b.WriteString(SanitizeKey(logicalID))
	b.WriteString(`","id":"`)
	b.WriteString(logicalID)
	b.WriteString(`","project":"`)
	b.WriteString(project)
	b.WriteString(`","project_id":"`)
	b.WriteString(project)
	b.WriteString(`","resourceType":"`)
	b.WriteString(resourceType)
	b.WriteString(`"`)
	if authPath != "" {
		b.WriteString(`,"auth_resource_path":"`)
		b.WriteString(authPath)
		b.WriteString(`"`)
	}
	if generation != "" {
		b.WriteString(`,"dataset_generation":"`)
		b.WriteString(generation)
		b.WriteString(`"`)
	}
	for k, v := range extraFields {
		b.WriteString(`,"`)
		b.WriteString(k)
		b.WriteString(`":`)
		b.WriteString(v)
	}
	if payload != "" {
		b.WriteString(`,"payload":`)
		b.WriteString(payload)
	}
	b.WriteString(`}`)
	return rowBuildResult{vertex: json.RawMessage(b.String())}
}

// replaceField rewrites one top-level field of a built vertex. The result
// is only used to feed defect cases, so key order churn is acceptable.
func replaceField(t *testing.T, built rowBuildResult, field string, rawValue json.RawMessage) rowBuildResult {
	t.Helper()
	document, err := decodeTopLevelDocument(built.vertex)
	if err != nil {
		t.Fatalf("decode vertex for defect injection: %v", err)
	}
	document[field] = rawValue
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("re-encode vertex: %v", err)
	}
	built.vertex = encoded
	return built
}

func dropField(t *testing.T, built rowBuildResult, field string) rowBuildResult {
	t.Helper()
	document, err := decodeTopLevelDocument(built.vertex)
	if err != nil {
		t.Fatalf("decode vertex for defect injection: %v", err)
	}
	delete(document, field)
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("re-encode vertex: %v", err)
	}
	built.vertex = encoded
	return built
}

// ---------------------------------------------------------------------------
// Recording store-surface decorator: used ONLY for the absence/capture
// claims. Live commit claims always run through the real surface.
// ---------------------------------------------------------------------------

type ing01Recorder struct {
	mu      sync.Mutex
	calls   int
	batches []postgresstore.ResourceBatch
}

func (r *ing01Recorder) WriteResourceBatch(_ context.Context, batch postgresstore.ResourceBatch) (postgresstore.ResourceBatchResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.batches = append(r.batches, batch)
	return postgresstore.ResourceBatchResult{
		BatchID:   batch.ID,
		Digest:    "recorder-digest",
		Committed: int64(len(batch.Rows)),
	}, nil
}

func (r *ing01Recorder) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func (r *ing01Recorder) lastBatch(t *testing.T) postgresstore.ResourceBatch {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.calls == 0 {
		t.Fatal("recorder has no recorded batch")
	}
	return r.batches[r.calls-1]
}

// ---------------------------------------------------------------------------
// End-to-end exact writes through the real builders.
// ---------------------------------------------------------------------------

func TestPostgresResourceWriterWritesBuiltRowsWithExactTypedColumnsAndLedger(t *testing.T) {
	s := newIngest01IngestService(t)
	s.migrate(t)
	writer := s.newWriter(t)

	rows := []rowBuildResult{
		buildGeneratedRow(t, ing01IngestProject, ing01IngestGen, ing01IngestAuth, "ResearchSubject", ing01ResearchSubjectLine),
		buildGenericRow(t, ing01IngestProject, ing01IngestGen, ing01IngestAuth, "SubstanceDefinition", ing01SubstanceLine),
		// A hand-built row that advertises a storage identity: the writer
		// must never read it — resource_seq is server-assigned.
		handBuiltVertex(ing01IngestProject, ing01IngestGen, "Observation", "hand-1", ing01IngestAuth,
			`{"resourceType":"Observation","id":"hand-1","effective":"2026"}`,
			map[string]string{"resource_seq": "999", "logical_key": `"hand-1"`}),
	}
	scope := PGResourceBatchScope{
		BatchID: ing01BuiltBatchID, Project: ing01IngestProject,
		DatasetGeneration: ing01IngestGen, AuthResourcePath: ing01IngestAuth,
	}
	res, err := writer.WriteResourceBatch(s.ctx, scope, rows)
	if err != nil {
		s.fail("WriteResourceBatch", err)
	}
	if res.BatchID != ing01BuiltBatchID || res.Offered != 3 || res.Committed != 3 ||
		res.SkippedIdentical != 0 || res.Replay {
		t.Fatalf("result = %+v, want a fresh 3-row commit", res)
	}
	if res.Digest != ing01BuiltBatchDigest {
		t.Fatalf("digest = %s, want the pinned golden %s", res.Digest, ing01BuiltBatchDigest)
	}

	// Direct-SQL witnesses: exact typed columns and canonical JSONB texts.
	s.requireResourceRow(t, ing01IngestProject, ing01IngestGen, "ResearchSubject", "research-subject-1",
		ing01IngestAuth, str(ing01ResearchSubjectPayloadText))
	s.requireResourceRow(t, ing01IngestProject, ing01IngestGen, "SubstanceDefinition", "substance-1",
		ing01IngestAuth, str(ing01SubstancePayloadText))
	s.requireResourceRow(t, ing01IngestProject, ing01IngestGen, "Observation", "hand-1",
		ing01IngestAuth, str(`{"id": "hand-1", "effective": "2026", "resourceType": "Observation"}`))
	if n := s.countIn(t, ing01IngestProject, ing01IngestGen); n != 3 {
		t.Fatalf("rows = %d, want 3", n)
	}

	// The advertised storage identity was ignored: server-assigned seqs
	// are distinct and none carries the advertised 999.
	var advertised bool
	if err := s.iso.QueryRow(s.ctx,
		"SELECT coalesce(bool_or(resource_seq = 999), false) FROM "+s.qualified("fhir_resource")+
			" WHERE project = $1 AND dataset_generation = $2", ing01IngestProject, ing01IngestGen).
		Scan(&advertised); err != nil {
		s.fail("witness server-assigned seqs", err)
	}
	if advertised {
		t.Fatal("a caller-advertised resource_seq was stored; the storage identity must be server-assigned")
	}
	distinct := s.mustInt64(t, "SELECT count(DISTINCT resource_seq) FROM "+s.qualified("fhir_resource")+
		" WHERE project = $1 AND dataset_generation = $2", ing01IngestProject, ing01IngestGen)
	if distinct != 3 {
		t.Fatalf("distinct resource_seq = %d, want 3", distinct)
	}

	// The ledger marker matches the reported digest and exact accounting.
	digest, resources, found := s.witnessLedger(t, ing01BuiltBatchID)
	if !found || digest != ing01BuiltBatchDigest || resources != 3 {
		t.Fatalf("marker = (%s, %d, found %v), want the golden digest and 3 committed rows",
			digest, resources, found)
	}
}

func TestPostgresResourceWriterBatchContentMatchesLiteralCommittedBatch(t *testing.T) {
	recorder := &ing01Recorder{}
	writer := &PostgresResourceWriter{committer: recorder}

	rows := []rowBuildResult{
		handBuiltVertex(ing01IngestProject, ing01IngestGen, "Observation", "cap-1", ing01IngestAuth,
			`{"resourceType":"Observation","id":"cap-1","status":"final"}`, nil),
		handBuiltVertex(ing01IngestProject, ing01IngestGen, "Observation", "cap-2", ing01IngestAuth,
			`null`, nil),
		handBuiltVertex(ing01IngestProject, ing01IngestGen, "Observation", "cap-3", ing01IngestAuth,
			"", nil),
	}
	scope := PGResourceBatchScope{
		BatchID: "ing01-ing/capture/0", Project: ing01IngestProject,
		DatasetGeneration: ing01IngestGen, AuthResourcePath: ing01IngestAuth,
	}
	if _, err := writer.WriteResourceBatch(context.Background(), scope, rows); err != nil {
		t.Fatalf("WriteResourceBatch: %v", err)
	}
	if got := recorder.callCount(); got != 1 {
		t.Fatalf("committer calls = %d, want 1", got)
	}
	// The exact batch handed to the store surface, as literals: scope,
	// ordered typed columns, the canonical encoding of each payload
	// document (sorted keys, compact, numeric literals verbatim), and a
	// nil payload for the absent key.
	want := postgresstore.ResourceBatch{
		ID:                "ing01-ing/capture/0",
		Project:           ing01IngestProject,
		DatasetGeneration: ing01IngestGen,
		Rows: []catalog.PGResourceRow{
			{Project: ing01IngestProject, DatasetGeneration: ing01IngestGen, ResourceType: "Observation",
				LogicalID: "cap-1", AuthResourcePath: ing01IngestAuth,
				Payload: json.RawMessage(`{"id":"cap-1","resourceType":"Observation","status":"final"}`)},
			{Project: ing01IngestProject, DatasetGeneration: ing01IngestGen, ResourceType: "Observation",
				LogicalID: "cap-2", AuthResourcePath: ing01IngestAuth, Payload: json.RawMessage(`null`)},
			{Project: ing01IngestProject, DatasetGeneration: ing01IngestGen, ResourceType: "Observation",
				LogicalID: "cap-3", AuthResourcePath: ing01IngestAuth, Payload: nil},
		},
	}
	if got := recorder.lastBatch(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("committed batch drifted from the literal:\n got %+v\nwant %+v", got, want)
	}
}

// TestPostgresResourceWriterCanonicalPayloadEncoding pins the exact bytes
// the writer commits payloads as: keys sorted by byte order, insignificant
// whitespace gone, numeric literals verbatim (no float round trip),
// duplicate keys last-wins, and malformed input (including trailing
// content) refused. The ledger digest hashes these bytes, so identical
// content must always encode identically — across processes included.
func TestPostgresResourceWriterCanonicalPayloadEncoding(t *testing.T) {
	for _, tc := range []struct{ name, in, out string }{
		{"sorted keys, compact, numbers verbatim, duplicates last-wins",
			`{ "b" : 1 , "a" : [ 2.50 , 1e3 , {"z":null,"y":[]} ] , "a2":true , "b":2 }`,
			`{"a":[2.50,1e3,{"y":[],"z":null}],"a2":true,"b":2}`},
		{"null document stays null", "  null  ", `null`},
		{"empty object", `{}`, `{}`},
		{"unicode escapes round trip", `{"k":"\u00e9"}`, `{"k":"é"}`},
	} {
		got, err := canonicalPayload([]byte(tc.in))
		if err != nil {
			t.Fatalf("canonicalPayload(%s): %v", tc.in, err)
		}
		if string(got) != tc.out {
			t.Fatalf("canonicalPayload(%s) = %s, want %s", tc.in, got, tc.out)
		}
	}
	for _, malformed := range []string{`{`, `{"a":1}{"b":2}`, `{"a":}`, ``, `[1,2`, `nul`, `12abc`} {
		if _, err := canonicalPayload([]byte(malformed)); err == nil {
			t.Fatalf("canonicalPayload(%q) accepted malformed JSON", malformed)
		}
	}
}

// ---------------------------------------------------------------------------
// Legacy namespace and payload null fidelity.
// ---------------------------------------------------------------------------

func TestPostgresResourceWriterLegacyNamespaceAndPayloadNullFidelity(t *testing.T) {
	s := newIngest01IngestService(t)
	s.migrate(t)
	writer := s.newWriter(t)

	// Legacy batch: generation "" and auth "" accept unstamped,
	// auth-less vertices. Payload fidelity: an absent payload key stores
	// SQL NULL; the bytes "null" store the JSON null document; the two
	// stay distinct.
	rows := []rowBuildResult{
		handBuiltVertex(ing01IngestProject, "", "Patient", "legacy-1", "",
			`{"resourceType":"Patient","id":"legacy-1"}`, nil),
		handBuiltVertex(ing01IngestProject, "", "Patient", "legacy-null", "", `null`, nil),
		handBuiltVertex(ing01IngestProject, "", "Patient", "legacy-absent", "", "", nil),
	}
	scope := PGResourceBatchScope{BatchID: ing01HandBatchID, Project: ing01IngestProject}
	res, err := writer.WriteResourceBatch(s.ctx, scope, rows)
	if err != nil {
		s.fail("legacy WriteResourceBatch", err)
	}
	if res.Committed != 3 || res.Replay || res.Digest != ing01HandBatchDigest {
		t.Fatalf("legacy result = %+v, want a fresh 3-row commit under the pinned digest %s",
			res, ing01HandBatchDigest)
	}
	s.requireResourceRow(t, ing01IngestProject, "", "Patient", "legacy-1", "", str(`{"id": "legacy-1", "resourceType": "Patient"}`))
	s.requireResourceRow(t, ing01IngestProject, "", "Patient", "legacy-null", "", str("null"))
	s.requireResourceRow(t, ing01IngestProject, "", "Patient", "legacy-absent", "", nil)

	// SQL NULL versus JSON null are distinguishable through SQL itself.
	nulls := s.mustInt64(t, "SELECT count(*) FROM "+s.qualified("fhir_resource")+
		" WHERE project = $1 AND dataset_generation = '' AND payload IS NULL", ing01IngestProject)
	if nulls != 1 {
		t.Fatalf("SQL NULL payloads = %d, want exactly 1 (absent key)", nulls)
	}
	jsonNulls := s.mustInt64(t, "SELECT count(*) FROM "+s.qualified("fhir_resource")+
		" WHERE project = $1 AND dataset_generation = '' AND payload = 'null'::jsonb", ing01IngestProject)
	if jsonNulls != 1 {
		t.Fatalf("JSON null documents = %d, want exactly 1 (explicit null)", jsonNulls)
	}
	digest, resources, found := s.witnessLedger(t, ing01HandBatchID)
	if !found || digest != ing01HandBatchDigest || resources != 3 {
		t.Fatalf("legacy marker = (%s, %d, found %v), want the pinned digest and 3 rows", digest, resources, found)
	}
}

// ---------------------------------------------------------------------------
// Replay and duplicate identity through the full ingest stack.
// ---------------------------------------------------------------------------

func TestPostgresResourceWriterReplayIdenticalBatchIsLedgerNoOp(t *testing.T) {
	s := newIngest01IngestService(t)
	s.migrate(t)
	writer := s.newWriter(t)

	rows := []rowBuildResult{
		buildGeneratedRow(t, ing01IngestProject, ing01IngestGen, ing01IngestAuth, "ResearchSubject", ing01ResearchSubjectLine),
		handBuiltVertex(ing01IngestProject, ing01IngestGen, "Observation", "replay-1", ing01IngestAuth,
			`{"resourceType":"Observation","id":"replay-1"}`, nil),
	}
	scope := PGResourceBatchScope{
		BatchID: "ing01-ing/replay/0", Project: ing01IngestProject,
		DatasetGeneration: ing01IngestGen, AuthResourcePath: ing01IngestAuth,
	}
	first, err := writer.WriteResourceBatch(s.ctx, scope, rows)
	if err != nil {
		s.fail("first WriteResourceBatch", err)
	}
	before := s.scopeFingerprint(t, ing01IngestProject, ing01IngestGen)

	for attempt := 0; attempt < 3; attempt++ {
		replay, err := writer.WriteResourceBatch(s.ctx, scope, rows)
		if err != nil {
			s.fail("replay", err)
		}
		// Byte-identical accounting to the original commit, differing
		// only in the Replay flag itself.
		if !replay.Replay || replay.BatchID != first.BatchID || replay.Digest != first.Digest ||
			replay.Offered != first.Offered || replay.Committed != first.Committed ||
			replay.SkippedIdentical != first.SkippedIdentical {
			t.Fatalf("replay attempt %d = %+v, want a byte-identical replay of %+v", attempt, replay, first)
		}
	}
	if n := s.countIn(t, ing01IngestProject, ing01IngestGen); n != 2 {
		t.Fatalf("rows after replays = %d, want 2 — replay must write nothing", n)
	}
	if fp := s.scopeFingerprint(t, ing01IngestProject, ing01IngestGen); fp != before {
		t.Fatalf("fingerprint drifted across replays: %s != %s", fp, before)
	}
	if _, _, found := s.witnessLedger(t, scope.BatchID); !found {
		t.Fatal("ledger marker missing after replays")
	}
}

func TestPostgresResourceWriterDuplicateNaturalIdentityDifferentPayloadRefused(t *testing.T) {
	s := newIngest01IngestService(t)
	s.migrate(t)
	writer := s.newWriter(t)

	scope := PGResourceBatchScope{
		BatchID: "ing01-ing/dup/first", Project: ing01IngestProject,
		DatasetGeneration: ing01IngestGen, AuthResourcePath: ing01IngestAuth,
	}
	rows := []rowBuildResult{
		buildGeneratedRow(t, ing01IngestProject, ing01IngestGen, ing01IngestAuth, "ResearchSubject", ing01ResearchSubjectLine),
	}
	if _, err := writer.WriteResourceBatch(s.ctx, scope, rows); err != nil {
		s.fail("first commit", err)
	}
	stored := s.scopeFingerprint(t, ing01IngestProject, ing01IngestGen)

	// Same logical id, different payload (status changed), plus a fresh
	// row: the whole second batch must roll back with the typed conflict.
	changedLine := strings.Replace(ing01ResearchSubjectLine, `"status":"active"`, `"status":"withdrawn"`, 1)
	second := []rowBuildResult{
		buildGeneratedRow(t, ing01IngestProject, ing01IngestGen, ing01IngestAuth, "ResearchSubject", changedLine),
		handBuiltVertex(ing01IngestProject, ing01IngestGen, "Observation", "dup-extra-1", ing01IngestAuth,
			`{"resourceType":"Observation","id":"dup-extra-1"}`, nil),
	}
	secondScope := scope
	secondScope.BatchID = "ing01-ing/dup/second"
	_, err := writer.WriteResourceBatch(s.ctx, secondScope, second)
	var conflict *postgresstore.BatchResourceConflictError
	if !errors.As(err, &conflict) {
		s.fail("duplicate payload batch", fmt.Errorf("err = %v, want *postgres.BatchResourceConflictError", err))
	}
	if !errors.Is(err, postgresstore.ErrBatchRowConflict) {
		t.Fatalf("err does not match ErrBatchRowConflict: %v", err)
	}
	if conflict.InBatch || conflict.ResourceType != "ResearchSubject" || conflict.LogicalID != "research-subject-1" ||
		conflict.Project != ing01IngestProject || conflict.DatasetGeneration != ing01IngestGen {
		t.Fatalf("conflict = %+v, want stored-row ResearchSubject/research-subject-1 under %s/%s",
			conflict, ing01IngestProject, ing01IngestGen)
	}
	if fp := s.scopeFingerprint(t, ing01IngestProject, ing01IngestGen); fp != stored {
		t.Fatalf("stored scope drifted after refused batch: %s != %s", fp, stored)
	}
	if n := s.mustInt64(t, "SELECT count(*) FROM "+s.qualified("fhir_resource")+
		" WHERE logical_id = 'dup-extra-1'"); n != 0 {
		t.Fatalf("refused batch leaked its fresh row (%d), want whole-batch rollback", n)
	}
	if _, _, found := s.witnessLedger(t, secondScope.BatchID); found {
		t.Fatal("refused batch left a ledger marker")
	}

	// Guard non-vacuity: identical re-offer under a fresh batch identity
	// commits zero rows (identical duplicates collapse), proving the
	// refusal above was about content drift, not duplication.
	thirdScope := scope
	thirdScope.BatchID = "ing01-ing/dup/control"
	res, err := writer.WriteResourceBatch(s.ctx, thirdScope, rows)
	if err != nil {
		s.fail("control re-offer", err)
	}
	if res.Committed != 0 || res.SkippedIdentical != 1 || res.Replay {
		t.Fatalf("control = %+v, want 0 committed and 1 identical row skipped", res)
	}
}

// ---------------------------------------------------------------------------
// Refusal matrix: exact typed errors, recorder proof of zero store calls,
// and direct-SQL witnesses of zero rows/markers on the live stack.
// ---------------------------------------------------------------------------

func ing01GoodVertex() rowBuildResult {
	return handBuiltVertex(ing01IngestProject, ing01IngestGen, "Observation", "refuse-ok", ing01IngestAuth,
		`{"resourceType":"Observation","id":"refuse-ok"}`, nil)
}

type ing01Refusal struct {
	name     string
	scope    PGResourceBatchScope
	rows     []rowBuildResult
	scopeErr bool
	field    string
	row      int
	column   string
	contains string
}

func ing01RefusalCases(t *testing.T, goodScope PGResourceBatchScope, goodRows []rowBuildResult) []ing01Refusal {
	overCapacity := make([]rowBuildResult, 0, postgresstore.MaxBatchResources+1)
	for i := 0; i <= postgresstore.MaxBatchResources; i++ {
		overCapacity = append(overCapacity, handBuiltVertex(
			ing01IngestProject, ing01IngestGen, "Observation", fmt.Sprintf("over-%d", i), ing01IngestAuth,
			`{"resourceType":"Observation","id":"over"}`, nil))
	}
	return []ing01Refusal{
		{name: "blank batch id",
			scope:    PGResourceBatchScope{BatchID: " ", Project: ing01IngestProject, DatasetGeneration: ing01IngestGen, AuthResourcePath: ing01IngestAuth},
			rows:     goodRows, scopeErr: true, field: "batch id", contains: "blank"},
		{name: "over-long batch id",
			scope:    PGResourceBatchScope{BatchID: strings.Repeat("x", postgresstore.MaxBatchIDLength+1), Project: ing01IngestProject, DatasetGeneration: ing01IngestGen, AuthResourcePath: ing01IngestAuth},
			rows:     goodRows, scopeErr: true, field: "batch id", contains: "at most 255"},
		{name: "blank project",
			scope:    PGResourceBatchScope{BatchID: "ing01-ing/refuse/project", Project: " ", DatasetGeneration: ing01IngestGen, AuthResourcePath: ing01IngestAuth},
			rows:     goodRows, scopeErr: true, field: "project", contains: "blank"},
		{name: "empty batch",
			scope: goodScope, rows: nil, scopeErr: true, field: "rows", contains: "no rows"},
		{name: "over capacity",
			scope: PGResourceBatchScope{BatchID: "ing01-ing/refuse/over", Project: ing01IngestProject, DatasetGeneration: ing01IngestGen, AuthResourcePath: ing01IngestAuth},
			rows:  overCapacity, scopeErr: true, field: "rows", contains: "at most 1000"},
		{name: "vertex project mismatch", scope: goodScope,
			rows: []rowBuildResult{replaceFieldDirect(t, ing01GoodVertex(), "project", json.RawMessage(`"wrong-project"`))},
			row: 0, column: "project", contains: "does not match the batch project"},
		{name: "vertex project_id missing", scope: goodScope,
			rows: []rowBuildResult{dropFieldDirect(t, ing01GoodVertex(), "project_id")},
			row: 0, column: "project_id", contains: "missing project_id"},
		{name: "vertex project_id mismatch", scope: goodScope,
			rows: []rowBuildResult{replaceFieldDirect(t, ing01GoodVertex(), "project_id", json.RawMessage(`"other-project"`))},
			row: 0, column: "project_id", contains: "does not match the batch project"},
		{name: "missing generation stamp", scope: goodScope,
			rows: []rowBuildResult{dropFieldDirect(t, ing01GoodVertex(), "dataset_generation")},
			row: 0, column: "dataset_generation", contains: "no dataset_generation stamp"},
		{name: "generation stamp mismatch", scope: goodScope,
			rows: []rowBuildResult{replaceFieldDirect(t, ing01GoodVertex(), "dataset_generation", json.RawMessage(`"gen-other"`))},
			row: 0, column: "dataset_generation", contains: "does not match the batch generation"},
		{name: "stamped row under legacy batch",
			scope: PGResourceBatchScope{BatchID: "ing01-ing/refuse/legacy", Project: ing01IngestProject},
			rows:  []rowBuildResult{ing01GoodVertex()},
			row:   0, column: "dataset_generation", contains: "legacy (unversioned) namespace"},
		{name: "blank resource type", scope: goodScope,
			rows: []rowBuildResult{replaceFieldDirect(t, ing01GoodVertex(), "resourceType", json.RawMessage(`" "`))},
			row: 0, column: "resourceType", contains: "resourceType must be a non-empty string"},
		{name: "blank logical id", scope: goodScope,
			rows: []rowBuildResult{replaceFieldDirect(t, ing01GoodVertex(), "id", json.RawMessage(`""`))},
			row: 0, column: "id", contains: "id must be a non-empty string"},
		{name: "auth path mismatch", scope: goodScope,
			rows: []rowBuildResult{replaceFieldDirect(t, ing01GoodVertex(), "auth_resource_path", json.RawMessage(`"/other"`))},
			row: 0, column: "auth_resource_path", contains: "does not match the batch auth path"},
		{name: "missing auth key under scoped batch", scope: goodScope,
			rows: []rowBuildResult{dropFieldDirect(t, ing01GoodVertex(), "auth_resource_path")},
			row: 0, column: "auth_resource_path", contains: "no auth_resource_path"},
		{name: "undecodable vertex", scope: goodScope,
			rows: []rowBuildResult{{vertex: json.RawMessage(`{"broken":`)}},
			row: 0, column: "vertex", contains: "undecodable"},
	}
}

func TestPostgresResourceWriterRejectsInvalidScopeAndIdentityBeforeAnyRow(t *testing.T) {
	recorder := &ing01Recorder{}
	recWriter := &PostgresResourceWriter{committer: recorder}

	goodScope := PGResourceBatchScope{
		BatchID: "ing01-ing/refuse", Project: ing01IngestProject,
		DatasetGeneration: ing01IngestGen, AuthResourcePath: ing01IngestAuth,
	}
	goodRows := []rowBuildResult{ing01GoodVertex()}

	for _, tc := range ing01RefusalCases(t, goodScope, goodRows) {
		_, err := recWriter.WriteResourceBatch(context.Background(), tc.scope, tc.rows)
		if !errors.Is(err, ErrInvalidPGResourceWrite) {
			t.Fatalf("%s: err = %v, want ErrInvalidPGResourceWrite", tc.name, err)
		}
		if tc.scopeErr {
			var scopeErr *PGResourceWriteScopeError
			if !errors.As(err, &scopeErr) {
				t.Fatalf("%s: err = %v, want *PGResourceWriteScopeError", tc.name, err)
			}
			if scopeErr.Field != tc.field || !strings.Contains(scopeErr.Reason, tc.contains) {
				t.Fatalf("%s: scope error = %+v, want field %q naming %q", tc.name, scopeErr, tc.field, tc.contains)
			}
		} else {
			var rowErr *PGResourceRowIdentityError
			if !errors.As(err, &rowErr) {
				t.Fatalf("%s: err = %v, want *PGResourceRowIdentityError", tc.name, err)
			}
			if rowErr.Row != tc.row || rowErr.Column != tc.column ||
				!strings.Contains(rowErr.Reason, tc.contains) {
				t.Fatalf("%s: row error = %+v, want row %d column %q naming %q",
					tc.name, rowErr, tc.row, tc.column, tc.contains)
			}
		}
		// Control: the untouched good batch must pass the same gate — a
		// guard that always refused would fail here.
		if _, err := recWriter.WriteResourceBatch(context.Background(), goodScope, goodRows); err != nil {
			t.Fatalf("%s: control batch refused: %v", tc.name, err)
		}
	}

	// Absence proof: exactly the controls reached the store surface; not
	// one refusal got a call. The recorder never touches a database, so
	// this is the "no round trip" half of the pre-write guarantee; the
	// database half is witnessed live below.
	cases := ing01RefusalCases(t, goodScope, goodRows)
	if got := recorder.callCount(); got != len(cases) {
		t.Fatalf("committer calls = %d, want %d (controls only — every refusal must stop before the store)",
			got, len(cases))
	}

	// Live-stack witness for representative row-level defects: same typed
	// error, zero rows, and zero ledger markers.
	s := newIngest01IngestService(t)
	s.migrate(t)
	live := s.newWriter(t)
	liveCases := []struct {
		name  string
		rows  []rowBuildResult
		batch string
	}{
		{"project mismatch live", []rowBuildResult{replaceFieldDirect(t, ing01GoodVertex(), "project", json.RawMessage(`"wrong-project"`))}, "live-project-mismatch"},
		{"missing stamp live", []rowBuildResult{dropFieldDirect(t, ing01GoodVertex(), "dataset_generation")}, "live-missing-stamp"},
		{"undecodable live", []rowBuildResult{{vertex: json.RawMessage(`[1,2,3]`)}}, "live-undecodable"},
	}
	for _, lc := range liveCases {
		scope := goodScope
		scope.BatchID = "ing01-ing/refuse/" + lc.batch
		_, err := live.WriteResourceBatch(s.ctx, scope, lc.rows)
		if !errors.Is(err, ErrInvalidPGResourceWrite) {
			t.Fatalf("%s: live err = %v, want ErrInvalidPGResourceWrite", lc.name, err)
		}
		if n := s.countIn(t, ing01IngestProject, ing01IngestGen); n != 0 {
			t.Fatalf("%s: live scope holds %d rows, want 0 — refusal must precede any write", lc.name, n)
		}
		if _, _, found := s.witnessLedger(t, scope.BatchID); found {
			t.Fatalf("%s: refused batch left a ledger marker", lc.name)
		}
	}

	// Live guard non-vacuity: the identical good batch (only a distinct
	// identity) commits through the same writer and the same schema.
	scope := goodScope
	scope.BatchID = "ing01-ing/refuse/live-control"
	res, err := live.WriteResourceBatch(s.ctx, scope, []rowBuildResult{
		handBuiltVertex(ing01IngestProject, ing01IngestGen, "Observation", "refuse-ok", ing01IngestAuth,
			`{"resourceType":"Observation","id":"refuse-ok"}`, nil)})
	if err != nil {
		s.fail("live control commit", err)
	}
	if res.Committed != 1 || res.Replay {
		t.Fatalf("live control = %+v, want a fresh single-row commit", res)
	}
	if n := s.countIn(t, ing01IngestProject, ing01IngestGen); n != 1 {
		t.Fatalf("rows after live control = %d, want 1", n)
	}
}

// replaceFieldDirect/dropFieldDirect wrap the defect injectors so the
// refusal table below reads as pure data.
func replaceFieldDirect(t *testing.T, built rowBuildResult, field string, rawValue json.RawMessage) rowBuildResult {
	t.Helper()
	return replaceField(t, built, field, rawValue)
}

func dropFieldDirect(t *testing.T, built rowBuildResult, field string) rowBuildResult {
	t.Helper()
	return dropField(t, built, field)
}

// ---------------------------------------------------------------------------
// Two-real-connection race surfaces (>= 10 rounds each, distinct
// pg_backend_pid witnesses, byte-identical convergence).
// ---------------------------------------------------------------------------

func TestPostgresResourceWriterConcurrentIdenticalBatchCommitsOnceTwoConnections(t *testing.T) {
	const rounds = 12
	s := newIngest01IngestService(t)
	s.migrate(t)

	clientA := s.openAppClient(t, "ing01-ing-race-a", 1)
	clientB := s.openAppClient(t, "ing01-ing-race-b", 1)
	pidA := s.poolBackendPID(t, clientA)
	pidB := s.poolBackendPID(t, clientB)
	if pidA == 0 || pidB == 0 || pidA == pidB {
		t.Fatalf("race pools share backend %d/%d, want distinct real connections", pidA, pidB)
	}
	writerA := s.newWriterOn(t, clientA)
	writerB := s.newWriterOn(t, clientB)

	for round := 0; round < rounds; round++ {
		generation := fmt.Sprintf("gen-race-%d", round)
		rows := []rowBuildResult{
			buildGeneratedRow(t, ing01IngestProject, generation, ing01IngestAuth, "ResearchSubject", ing01ResearchSubjectLine),
			handBuiltVertex(ing01IngestProject, generation, "Observation", fmt.Sprintf("race-obs-%d", round), ing01IngestAuth,
				`{"resourceType":"Observation"}`, nil),
		}
		scope := PGResourceBatchScope{
			BatchID: fmt.Sprintf("ing01-ing/race/%d", round), Project: ing01IngestProject,
			DatasetGeneration: generation, AuthResourcePath: ing01IngestAuth,
		}

		start := make(chan struct{})
		type outcome struct {
			res PGResourceWriteResult
			err error
		}
		ra := make(chan outcome, 1)
		rb := make(chan outcome, 1)
		go func() { <-start; res, err := writerA.WriteResourceBatch(s.ctx, scope, rows); ra <- outcome{res, err} }()
		go func() { <-start; res, err := writerB.WriteResourceBatch(s.ctx, scope, rows); rb <- outcome{res, err} }()
		close(start)
		o1, o2 := <-ra, <-rb
		if o1.err != nil {
			s.fail(fmt.Sprintf("round %d connection A", round), o1.err)
		}
		if o2.err != nil {
			s.fail(fmt.Sprintf("round %d connection B", round), o2.err)
		}

		// Byte-identical observable accounting except for the Replay flag;
		// exactly one fresh commit.
		if o1.res.BatchID != o2.res.BatchID || o1.res.Digest != o2.res.Digest ||
			o1.res.Offered != o2.res.Offered || o1.res.Committed != o2.res.Committed ||
			o1.res.SkippedIdentical != o2.res.SkippedIdentical {
			t.Fatalf("round %d results diverged: %+v vs %+v", round, o1.res, o2.res)
		}
		if !o1.res.Replay && !o2.res.Replay || o1.res.Replay && o2.res.Replay {
			t.Fatalf("round %d replays = (%v, %v), want exactly one fresh commit", round, o1.res.Replay, o2.res.Replay)
		}
		if o1.res.Committed != 2 || o1.res.Offered != 2 || o1.res.SkippedIdentical != 0 || o1.res.Digest == "" {
			t.Fatalf("round %d shared accounting = %+v, want 2 committed under a real digest", round, o1.res)
		}

		// Post-convergence: committed once, marker once, and the digests
		// agree with the ledger.
		if n := s.countIn(t, ing01IngestProject, generation); n != 2 {
			t.Fatalf("round %d rows = %d, want exactly 2 (committed once)", round, n)
		}
		digest, resources, found := s.witnessLedger(t, scope.BatchID)
		if !found || digest != o1.res.Digest || resources != 2 {
			t.Fatalf("round %d marker = (%s, %d, found %v), want the shared digest with 2 rows",
				round, digest, resources, found)
		}
	}
}

func TestPostgresResourceWriterReportedCountsChangeOnlyAtCommitTwoConnections(t *testing.T) {
	s := newIngest01IngestService(t)
	s.migrate(t)

	writerClient := s.openAppClient(t, "ing01-ing-commit-w", 1)
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

	const rounds = 12
	for round := 0; round < rounds; round++ {
		generation := fmt.Sprintf("gen-visibility-%d", round)
		rows := []rowBuildResult{
			handBuiltVertex(ing01IngestProject, generation, "Observation", fmt.Sprintf("vis-%d", round), ing01IngestAuth,
				`{"resourceType":"Observation"}`, nil),
		}
		scope := PGResourceBatchScope{
			BatchID: fmt.Sprintf("ing01-ing/visibility/%d", round), Project: ing01IngestProject,
			DatasetGeneration: generation, AuthResourcePath: ing01IngestAuth,
		}

		// The observer holds the resource table exclusively: the batch
		// blocks at its first resource-table touch, uncommitted.
		if _, err := obsConn.Exec(s.ctx, "BEGIN"); err != nil {
			s.fail("observer begin", err)
		}
		if _, err := obsConn.Exec(s.ctx, "LOCK TABLE "+s.qualified("fhir_resource")+" IN ACCESS EXCLUSIVE MODE"); err != nil {
			s.fail("observer lock", err)
		}

		type outcome struct {
			res PGResourceWriteResult
			err error
		}
		done := make(chan outcome, 1)
		go func() {
			res, err := writer.WriteResourceBatch(context.Background(), scope, rows)
			done <- outcome{res, err}
		}()

		// The writer backend is genuinely blocked on the observer's lock.
		blockedPID := s.waitBackendWaiting(t, "ing01-ing-commit-w")
		if blockedPID != writerPID || blockedPID == observerPID {
			t.Fatalf("round %d blocked backend pid = %d, want the writer backend %d (observer is %d)",
				round, blockedPID, writerPID, observerPID)
		}

		// Mid-transaction witness from the lock holder itself (a third
		// connection's SELECT would queue behind the same ACCESS EXCLUSIVE
		// lock): persisted count has not moved. The ledger table carries
		// no lock, so it is witnessed from the independent pool.
		var midRows int64
		if err := obsConn.QueryRow(s.ctx, "SELECT count(*) FROM "+s.qualified("fhir_resource")+
			" WHERE project = $1 AND dataset_generation = $2", ing01IngestProject, generation).
			Scan(&midRows); err != nil {
			s.fail("mid-transaction row witness", err)
		}
		if midRows != 0 {
			t.Fatalf("round %d rows visible mid-transaction = %d, want 0 — counts may only change at commit", round, midRows)
		}
		if _, _, found := s.witnessLedger(t, scope.BatchID); found {
			t.Fatalf("round %d ledger marker visible mid-transaction, want none", round)
		}

		if _, err := obsConn.Exec(s.ctx, "COMMIT"); err != nil {
			s.fail("observer commit", err)
		}
		var o outcome
		select {
		case o = <-done:
		case <-time.After(60 * time.Second):
			s.fail("waiting for batch commit", fmt.Errorf("round %d batch never completed after the lock was released", round))
		}
		if o.err != nil {
			s.fail(fmt.Sprintf("round %d WriteResourceBatch", round), o.err)
		}
		if o.res.Committed != 1 || o.res.Replay {
			t.Fatalf("round %d result = %+v, want a fresh single-row commit", round, o.res)
		}
		// Post-commit: the direct-SQL count moved to exactly the reported
		// number, and only now.
		if n := s.countIn(t, ing01IngestProject, generation); n != int64(o.res.Committed) {
			t.Fatalf("round %d rows after commit = %d, want the reported %d", round, n, o.res.Committed)
		}
		if _, resources, found := s.witnessLedger(t, scope.BatchID); !found || resources != 1 {
			t.Fatalf("round %d marker missing or miscounted after commit (found %v, resources %d)", round, found, resources)
		}
	}
}

// ---------------------------------------------------------------------------
// Ownership-scoped zero-leftover audit.
// ---------------------------------------------------------------------------

// TestZZZeroLeftoverIngestPostgresResourceWriterFixtures is the
// ownership-scoped zero-leftover audit for this file: exact-name checks for
// every database (and schema inside it) this file verified into existence,
// then a count of the file's own verify_ing01_ing_* prefix against the
// pre-test baseline. It never inspects other prefixes, so concurrent
// sibling workers (store-surface tests included) neither race nor trip it.
func TestZZZeroLeftoverIngestPostgresResourceWriterFixtures(t *testing.T) {
	raw := ing01TestPostgresURL(t)
	ing01IngestOwnedMu.Lock()
	owned := make(map[string]string, len(ing01IngestOwnedByName))
	for db, schema := range ing01IngestOwnedByName {
		owned[db] = schema
	}
	ing01IngestOwnedMu.Unlock()
	if len(owned) == 0 {
		t.Skip("this process created no verify_ing01_ing_* fixtures")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect to test service for audit: %v", err)
	}
	defer admin.Close()

	for db, schema := range owned {
		if !strings.HasPrefix(db, ing01IngestPrefix) {
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

	var visible int64
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_database WHERE datname LIKE 'verify\\_ing01\\_ing\\_%' ESCAPE '\\'").
		Scan(&visible); err != nil {
		t.Fatalf("audit verify_ing01_ing_* database count: %v", err)
	}
	if visible != ing01IngestBaselineCount {
		t.Errorf("verify_ing01_ing_* databases = %d, want baseline %d", visible, ing01IngestBaselineCount)
	}
}
