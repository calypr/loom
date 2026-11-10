package postgres

// Real-PostgreSQL acceptance tests for the bounded, idempotent COPY batch
// writer (pg-store-06; check-2:
// go test ./internal/store/postgres -run BatchWriter -count=1).
//
// Every test drives the real BatchWriter against the PostgreSQL 18 service
// named by LOOM_TEST_POSTGRES_URL through the readiness-checked pool.
// Fixtures are literal rows inserted by direct SQL; witnesses are literal
// rows read back by direct SQL. Nothing is mocked. Idempotency proofs are
// non-vacuous: replay-after-crash paths run at every pre-commit step, ledger
// and row fingerprints are pinned to literals, and mutation probes (tampered
// ledger digest, mutated retry payload) show a broken guard would fail the
// test rather than pass silently. The replay and claim races run on two
// real connections with distinct pg_backend_pid, per the
// publication/postgres leases precedent.
//
// Fixture databases and schemas are uniquely owned as pgstore06_it_* and
// dropped by the test that created them; the final TestZZ audit is
// ownership-scoped — it checks each exact name this file created, never a
// global scan, so concurrent sibling workers sweeping their own prefixes
// neither race this audit nor trip it.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

	"github.com/calypr/loom/internal/source"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var pgstore06NamePattern = regexp.MustCompile(`^pgstore06_it_[0-9]+_[0-9a-f]+$`)

// Ownership registry: every resource whose creation this file verified is
// recorded here, and TestZZ asserts each exact name is gone. Ownership
// begins at verified CREATE, exactly as the per-test cleanup drops them.
var (
	batchOwnedMu     sync.Mutex
	batchOwnedByName = map[string]string{} // database -> owned schema
)

func batchRegister(db, schema string) {
	batchOwnedMu.Lock()
	defer batchOwnedMu.Unlock()
	batchOwnedByName[db] = schema
}

type batchService struct {
	ctx    context.Context
	admin  *pgxpool.Pool // service database
	iso    *pgxpool.Pool // isolated database (direct-SQL witnesses)
	dsn    string        // isolated database DSN (credentials scrubbed on failure)
	schema string
	scrub  func(any) string
	fail   func(step string, err error)
}

func newBatchService(t *testing.T) *batchService {
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

	dbName := fmt.Sprintf("pgstore06_it_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !pgstore06NamePattern.MatchString(dbName) {
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

	schema := fmt.Sprintf("pgstore06_it_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !pgstore06NamePattern.MatchString(schema) {
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

	batchRegister(dbName, schema)
	return &batchService{ctx: ctx, admin: admin, iso: iso, dsn: dsn, schema: schema, scrub: scrub, fail: fail}
}

// migrate applies the full embedded history (ledger, fhir_resource,
// fhir_edge_occurrence, the inventory tables, and batch_write_ledger) to the
// owned schema through the readiness-checked pool.
func (s *batchService) migrate(t *testing.T) {
	t.Helper()
	if _, err := MigrateSchema(s.ctx, s.openClient(t, 4), s.schema); err != nil {
		s.fail("MigrateSchema", err)
	}
}

// openClient opens one readiness-checked, bounded pool against the isolated
// database with the given connection bound.
func (s *batchService) openClient(t *testing.T, maxConns int32) *Client {
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
func (s *batchService) openAppClient(t *testing.T, app string, maxConns int32) *Client {
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

func (s *batchService) newWriterOn(t *testing.T, src ConnSource) *BatchWriter {
	t.Helper()
	writer, err := NewBatchWriter(src, s.schema)
	if err != nil {
		s.fail("new batch writer", err)
	}
	return writer
}

func (s *batchService) newWriter(t *testing.T) *BatchWriter {
	t.Helper()
	return s.newWriterOn(t, s.openClient(t, 4))
}

func (s *batchService) qualified(table string) string {
	return pgx.Identifier{s.schema, table}.Sanitize()
}

func (s *batchService) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := s.iso.Exec(s.ctx, sql, args...); err != nil {
		s.fail("fixture exec", err)
	}
}

// mustInt64 witnesses one direct-SQL integer.
func (s *batchService) mustInt64(t *testing.T, sql string, args ...any) int64 {
	t.Helper()
	var v int64
	if err := s.iso.QueryRow(s.ctx, sql, args...).Scan(&v); err != nil {
		s.fail("witness "+sql, err)
	}
	return v
}

// mustText witnesses one direct-SQL text value.
func (s *batchService) mustText(t *testing.T, sql string, args ...any) string {
	t.Helper()
	var v string
	if err := s.iso.QueryRow(s.ctx, sql, args...).Scan(&v); err != nil {
		s.fail("witness "+sql, err)
	}
	return v
}

func (s *batchService) backendPID(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var pid int64
	if err := pool.QueryRow(s.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		s.fail("witness pg_backend_pid", err)
	}
	return pid
}

// ---------------------------------------------------------------------------
// Literal fixtures.
// ---------------------------------------------------------------------------

const (
	batchProject = "prj-batch"
	batchGenA    = "gen-a"
	batchGenB    = "gen-b"
)

func litResource(resourceType, logicalID, authPath string, payload *string) source.Resource {
	rec := source.Resource{
		Project: batchProject, DatasetGeneration: batchGenA,
		ResourceType: resourceType, LogicalID: logicalID, AuthResourcePath: authPath,
	}
	if payload != nil {
		rec.Payload = json.RawMessage(*payload)
	}
	return rec
}

func litEdge(label, srcType, srcID, tgtType, tgtID, sourcePath string, ordinal int64,
	authPath string, key *string) EdgeOccurrenceRow {
	return EdgeOccurrenceRow{
		Project: batchProject, DatasetGeneration: batchGenA, AuthResourcePath: authPath,
		Label: label, SourceResourceType: srcType, SourceLogicalID: srcID,
		TargetResourceType: tgtType, TargetLogicalID: tgtID,
		SourcePath: sourcePath, SourceOrdinal: ordinal, LogicalEdgeKey: key,
	}
}

func strPtr(s string) *string { return &s }

// goldenBatch is the canonical fixture whose digest is pinned to a literal
// below: four resources covering a nested payload, a present-null key, a
// SQL NULL payload, and a JSON null document, plus two edge occurrences
// (one with a logical edge key, one without) whose source publishes in the
// same batch.
func goldenBatch() Batch {
	p1 := `{"resourceType":"Patient","id":"p-1","link":[{"type":{"coding":[{"code":"X"}]},"target":"Organization/o-1"}]}`
	p2 := `{"resourceType":"Patient","id":"p-2","meta":{"tag":null}}`
	return Batch{
		ID: "pgstore06/golden/0", Project: "prj-golden", DatasetGeneration: "gen-golden-1",
		Resources: []source.Resource{
			{Project: "prj-golden", DatasetGeneration: "gen-golden-1", ResourceType: "Patient", LogicalID: "p-1", AuthResourcePath: "/fhir", Payload: json.RawMessage(p1)},
			{Project: "prj-golden", DatasetGeneration: "gen-golden-1", ResourceType: "Patient", LogicalID: "p-2", AuthResourcePath: "", Payload: json.RawMessage(p2)},
			{Project: "prj-golden", DatasetGeneration: "gen-golden-1", ResourceType: "Observation", LogicalID: "obs-9", AuthResourcePath: "/fhir", Payload: nil},
			{Project: "prj-golden", DatasetGeneration: "gen-golden-1", ResourceType: "Provenance", LogicalID: "prov-0", AuthResourcePath: "/fhir", Payload: json.RawMessage(`null`)},
		},
		Edges: []EdgeOccurrenceRow{
			{Project: "prj-golden", DatasetGeneration: "gen-golden-1", AuthResourcePath: "/fhir",
				Label: "referenced-by", SourceResourceType: "Patient", SourceLogicalID: "p-1",
				TargetResourceType: "Organization", TargetLogicalID: "o-1",
				SourcePath: "Patient.link.target", SourceOrdinal: 0, LogicalEdgeKey: strPtr("ek-org-link-1")},
			{Project: "prj-golden", DatasetGeneration: "gen-golden-1", AuthResourcePath: "/fhir",
				Label: "referenced-by", SourceResourceType: "Patient", SourceLogicalID: "p-1",
				TargetResourceType: "Organization", TargetLogicalID: "o-1",
				SourcePath: "Patient.link.target", SourceOrdinal: 1, LogicalEdgeKey: nil},
		},
	}
}

// goldenDigest is the pinned SHA-256 of goldenBatch()'s canonical encoding,
// frozen as a literal so any drift in the digest encoding fails the suite
// loudly instead of silently re-fingerprinting history.
const goldenDigest = "7ad729506a5590958ed4b7dd8e9ea9eed486cbeedff700d6a51f6170e3faac99"

// ---------------------------------------------------------------------------
// Direct-SQL witnesses over literal expected rows.
// ---------------------------------------------------------------------------

func (s *batchService) requireResourceRow(t *testing.T, project, generation, rtype, lid, authPath string, wantPayload *string) {
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
	if wantPayload == nil {
		if !isNull {
			t.Fatalf("resource %s/%s payload = %v, want SQL NULL", rtype, lid, *payload)
		}
		return
	}
	if isNull {
		t.Fatalf("resource %s/%s payload is SQL NULL, want document %s", rtype, lid, *wantPayload)
	}
	if !jsonEquivalent(*payload, *wantPayload) {
		t.Fatalf("resource %s/%s payload = %v, want %s", rtype, lid, *payload, *wantPayload)
	}
}

// jsonEquivalent compares two JSON documents by decoded value, so the
// witness pins semantics (key order and formatting are PostgreSQL's to
// choose in canonical JSONB).
func jsonEquivalent(got, want string) bool {
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		return false
	}
	return reflect.DeepEqual(g, w)
}

func (s *batchService) requireEdgeRow(t *testing.T, project, generation, label, srcType, srcID, tgtType, tgtID, sourcePath string,
	ordinal int64, authPath string, wantKey *string) {
	t.Helper()
	var gotAuth string
	var key *string
	var count int
	err := s.iso.QueryRow(s.ctx,
		"SELECT count(*), max(auth_resource_path), max(logical_edge_key) FROM "+s.qualified(edgeTableName)+
			" WHERE project = $1 AND dataset_generation = $2 AND label = $3"+
			" AND source_resource_type = $4 AND source_logical_id = $5"+
			" AND target_resource_type = $6 AND target_logical_id = $7"+
			" AND source_path = $8 AND source_ordinal = $9",
		project, generation, label, srcType, srcID, tgtType, tgtID, sourcePath, ordinal,
	).Scan(&count, &gotAuth, &key)
	if err != nil {
		s.fail("witness edge row", err)
	}
	if count != 1 {
		t.Fatalf("edge occurrence rows = %d, want exactly 1", count)
	}
	if gotAuth != authPath {
		t.Fatalf("edge auth path = %q, want %q", gotAuth, authPath)
	}
	if (wantKey == nil) != (key == nil) {
		t.Fatalf("edge logical_edge_key = %v, want %v", keyPtrText(key), keyPtrText(wantKey))
	}
	if wantKey != nil && key != nil && *wantKey != *key {
		t.Fatalf("edge logical_edge_key = %q, want %q", *key, *wantKey)
	}
}

func keyPtrText(p *string) string {
	if p == nil {
		return "SQL NULL"
	}
	return fmt.Sprintf("%q", *p)
}

type ledgerWitness struct {
	digest      string
	resources   int64
	edges       int64
	project     string
	generation  string
	committedAt time.Time
}

func (s *batchService) witnessLedger(t *testing.T, batchID string) (ledgerWitness, bool) {
	t.Helper()
	var w ledgerWitness
	err := s.iso.QueryRow(s.ctx,
		"SELECT digest, resource_count, edge_count, project, dataset_generation, committed_at"+
			" FROM "+s.qualified(batchLedgerTableName)+" WHERE batch_id = $1", batchID).
		Scan(&w.digest, &w.resources, &w.edges, &w.project, &w.generation, &w.committedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ledgerWitness{}, false
	}
	if err != nil {
		s.fail("witness ledger row", err)
	}
	return w, true
}

func (s *batchService) countIn(t *testing.T, table, project, generation string) int64 {
	t.Helper()
	return s.mustInt64(t, "SELECT count(*) FROM "+s.qualified(table)+
		" WHERE project = $1 AND dataset_generation = $2", project, generation)
}

func (s *batchService) requireNothingWritten(t *testing.T, project, generation, batchID string) {
	t.Helper()
	if n := s.countIn(t, resourceTableName, project, generation); n != 0 {
		t.Fatalf("fhir_resource rows for %s/%s = %d, want 0", project, generation, n)
	}
	if n := s.countIn(t, edgeTableName, project, generation); n != 0 {
		t.Fatalf("fhir_edge_occurrence rows for %s/%s = %d, want 0", project, generation, n)
	}
	if _, found := s.witnessLedger(t, batchID); found {
		t.Fatalf("batch_write_ledger holds %q, want no marker", batchID)
	}
}

// ---------------------------------------------------------------------------
// Offline validation tests.
// ---------------------------------------------------------------------------

func TestBatchWriterConstructorRejectsInvalidInputs(t *testing.T) {
	if _, err := NewBatchWriter(nil, "ok_schema"); err == nil {
		t.Error("nil connection source: want error")
	}
	for _, bad := range []string{"", "Public", "a.b", "1x", strings.Repeat("a", 64)} {
		if _, err := NewBatchWriter(stubConnSource{}, bad); !errors.Is(err, ErrInvalidSchemaName) {
			t.Errorf("schema %q: NewBatchWriter = %v, want ErrInvalidSchemaName", bad, err)
		}
	}
}

func TestBatchWriterRejectsInvalidBatches(t *testing.T) {
	writer, err := NewBatchWriter(stubConnSource{}, "ok_schema")
	if err != nil {
		t.Fatal(err)
	}
	key := ""
	okResource := litResource("Patient", "p-1", "/fhir", strPtr(`{"id":"p-1"}`))
	okEdge := litEdge("referenced-by", "Patient", "p-1", "Organization", "o-1", "a", 0, "", &key)

	base := func(mutate func(*Batch)) Batch {
		b := Batch{ID: "b/1", Project: batchProject, DatasetGeneration: batchGenA}
		mutate(&b)
		return b
	}
	for _, tc := range []struct {
		name  string
		batch Batch
	}{
		{"blank id", base(func(b *Batch) { b.ID = "  "; b.Resources = []source.Resource{okResource} })},
		{"long id", base(func(b *Batch) {
			b.ID = strings.Repeat("x", MaxBatchIDLength+1)
			b.Resources = []source.Resource{okResource}
		})},
		{"blank project", base(func(b *Batch) { b.Project = ""; b.Resources = []source.Resource{okResource} })},
		{"no rows", base(func(*Batch) {})},
		{"too many resources", base(func(b *Batch) {
			for i := 0; i <= MaxBatchResources; i++ {
				b.Resources = append(b.Resources, okResource)
			}
		})},
		{"too many edges", base(func(b *Batch) {
			for i := 0; i <= MaxBatchEdges; i++ {
				b.Edges = append(b.Edges, okEdge)
			}
		})},
		{"resource scope mismatch", base(func(b *Batch) {
			rec := okResource
			rec.DatasetGeneration = "other-gen"
			b.Resources = []source.Resource{rec}
		})},
		{"edge scope mismatch", base(func(b *Batch) {
			b.Edges = append(b.Edges, EdgeOccurrenceRow{
				Project: "other-prj", DatasetGeneration: batchGenA, AuthResourcePath: "", Label: "l",
				SourceResourceType: "Patient", SourceLogicalID: "p-1",
				TargetResourceType: "Organization", TargetLogicalID: "o-1",
			})
		})},
		{"invalid json payload", base(func(b *Batch) {
			rec := okResource
			rec.Payload = json.RawMessage("{oops")
			b.Resources = []source.Resource{rec}
		})},
		{"blank resource type", base(func(b *Batch) {
			rec := okResource
			rec.ResourceType = ""
			b.Resources = []source.Resource{rec}
		})},
		{"caller-supplied edge seq", base(func(b *Batch) {
			rec := okEdge
			rec.EdgeSeq = 9
			b.Edges = []EdgeOccurrenceRow{rec}
		})},
		{"negative edge ordinal", base(func(b *Batch) {
			rec := okEdge
			rec.SourceOrdinal = -1
			b.Edges = []EdgeOccurrenceRow{rec}
		})},
		{"blank edge label", base(func(b *Batch) {
			rec := okEdge
			rec.Label = ""
			b.Edges = []EdgeOccurrenceRow{rec}
		})},
	} {
		_, err := writer.WriteBatch(context.Background(), tc.batch)
		if !errors.Is(err, ErrInvalidBatch) {
			t.Errorf("%s: WriteBatch = %v, want ErrInvalidBatch", tc.name, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Acceptance 1: COPY staging + atomic publish with exact commit accounting.
// ---------------------------------------------------------------------------

func TestBatchWriterPublishesGoldenBatchViaStagingWithLedgerAccounting(t *testing.T) {
	s := newBatchService(t)
	s.migrate(t)

	type probeFact struct {
		backend     int64
		stgResCount int64
		stgEdgeCnt  int64
		pubResCount int64
		pubEdgeCnt  int64
		stgEdgeVis  bool
		txStart     time.Time
	}
	var facts sync.Map // batchID -> *probeFact
	var probeErrs []string
	var probeMu sync.Mutex
	seenSteps := map[string][]string{}
	var seenMu sync.Mutex

	// One connection in the pool: every write (and the after-the-fact
	// staging-gone witness) runs on the same physical backend, so the
	// ON COMMIT DROP witness is about that session's temp schema.
	writer := s.newWriterOn(t, s.openClient(t, 1))
	writer.inTxProbe = func(step, batchID string, tx pgx.Tx) {
		seenMu.Lock()
		seenSteps[step] = append(seenSteps[step], batchID)
		seenMu.Unlock()
		v, _ := facts.LoadOrStore(batchID, &probeFact{})
		f := v.(*probeFact)
		note := func(err error) {
			probeMu.Lock()
			probeErrs = append(probeErrs, step+": "+err.Error())
			probeMu.Unlock()
		}
		if err := tx.QueryRow(s.ctx, "SELECT pg_backend_pid()").Scan(&f.backend); err != nil {
			note(err)
			return
		}
		switch step {
		case stepStageResources:
			if err := tx.QueryRow(s.ctx,
				"SELECT count(*), coalesce(min(ordinal),-1), coalesce(max(ordinal),-1) FROM "+
					writer.stagingResourceFQN()).Scan(&f.stgResCount, new(int64), new(int64)); err != nil {
				note(err)
			}
		case stepCheckResources:
			// Before publish, nothing has touched the scoped table.
			if err := tx.QueryRow(s.ctx,
				"SELECT count(*) FROM "+s.qualified(resourceTableName)+
					" WHERE project = $1 AND dataset_generation = $2",
				"prj-golden", "gen-golden-1").Scan(&f.pubResCount); err != nil {
				note(err)
			}
		case stepPublishResourc:
			if err := tx.QueryRow(s.ctx,
				"SELECT count(*) FROM "+s.qualified(resourceTableName)+
					" WHERE project = $1 AND dataset_generation = $2",
				"prj-golden", "gen-golden-1").Scan(&f.pubResCount); err != nil {
				note(err)
			}
		case stepCheckEdges:
			if err := tx.QueryRow(s.ctx,
				"SELECT count(*) FROM "+s.qualified(edgeTableName)+
					" WHERE project = $1 AND dataset_generation = $2",
				"prj-golden", "gen-golden-1").Scan(&f.pubEdgeCnt); err != nil {
				note(err)
			}
			// Edges may reference the batch's own resources: they are
			// already published inside this same transaction.
			var resNow int64
			if err := tx.QueryRow(s.ctx,
				"SELECT count(*) FROM "+s.qualified(resourceTableName)+
					" WHERE project = $1 AND dataset_generation = $2",
				"prj-golden", "gen-golden-1").Scan(&resNow); err != nil {
				note(err)
			}
			f.stgEdgeVis = resNow == 4
		case stepPublishEdges:
			if err := tx.QueryRow(s.ctx,
				"SELECT count(*) FROM "+writer.stagingEdgeFQN()).Scan(&f.stgEdgeCnt); err != nil {
				note(err)
			}
		case stepFinalizeLedger:
			// now() is the transaction snapshot: the ledger's
			// committed_at must end up strictly after it, proving the
			// finalize statement re-stamped with clock_timestamp()
			// rather than keeping the claim-time default.
			if err := tx.QueryRow(s.ctx, "SELECT now()").Scan(&f.txStart); err != nil {
				note(err)
			}
		}
	}

	res, err := writer.WriteBatch(s.ctx, goldenBatch())
	if err != nil {
		s.fail("WriteBatch golden", err)
	}
	for _, msg := range probeErrs {
		t.Errorf("probe %s", msg)
	}
	if want := (BatchResult{
		BatchID: "pgstore06/golden/0", Digest: goldenDigest,
		CommittedResources: 4, CommittedEdges: 2,
	}); res != want {
		t.Fatalf("result = %+v, want %+v", res, want)
	}

	v, ok := facts.Load("pgstore06/golden/0")
	if !ok {
		t.Fatal("probe never fired")
	}
	f := v.(*probeFact)
	if f.backend == 0 {
		t.Fatal("probe backend pid not captured")
	}
	if f.stgResCount != 4 {
		t.Errorf("staging resources before publish = %d, want 4 (COPY landed rows in staging)", f.stgResCount)
	}
	if f.pubResCount != 4 {
		t.Errorf("fhir_resource count observed mid-publish = %d, want 4", f.pubResCount)
	}
	if f.pubEdgeCnt != 0 {
		t.Errorf("fhir_edge_occurrence count before edge publish = %d, want 0", f.pubEdgeCnt)
	}
	if !f.stgEdgeVis {
		t.Errorf("batch's own resources were not visible to the edge guard inside the transaction")
	}
	if f.stgEdgeCnt != 2 {
		t.Errorf("staging edges before publish = %d, want 2", f.stgEdgeCnt)
	}
	// check-resources saw the scoped table EMPTY while staging held rows:
	// rows reach the table only through the publish statement.
	if seen := seenSteps[stepCheckResources]; len(seen) != 1 {
		t.Errorf("check-resources fired %d times, want 1", len(seen))
	}

	// Literal expected rows, read back by direct SQL.
	p1 := `{"resourceType":"Patient","id":"p-1","link":[{"type":{"coding":[{"code":"X"}]},"target":"Organization/o-1"}]}`
	p2 := `{"resourceType":"Patient","id":"p-2","meta":{"tag":null}}`
	s.requireResourceRow(t, "prj-golden", "gen-golden-1", "Patient", "p-1", "/fhir", &p1)
	s.requireResourceRow(t, "prj-golden", "gen-golden-1", "Patient", "p-2", "", &p2)
	s.requireResourceRow(t, "prj-golden", "gen-golden-1", "Observation", "obs-9", "/fhir", nil)
	s.requireResourceRow(t, "prj-golden", "gen-golden-1", "Provenance", "prov-0", "/fhir", strPtr(`null`))
	// Missing key vs present-null: p-1 has no meta, p-2 keeps meta.tag as a
	// present JSON null (distinct from SQL NULL and from absence).
	if has := s.mustInt64(t, "SELECT count(*) FROM "+s.qualified(resourceTableName)+
		" WHERE project = $1 AND dataset_generation = $2 AND resource_type = 'Patient'"+
		" AND logical_id = 'p-1' AND payload ? 'meta'", "prj-golden", "gen-golden-1"); has != 0 {
		t.Errorf("p-1 payload gained a meta key")
	}
	if has := s.mustInt64(t, "SELECT count(*) FROM "+s.qualified(resourceTableName)+
		" WHERE project = $1 AND dataset_generation = $2 AND resource_type = 'Patient'"+
		" AND logical_id = 'p-2' AND jsonb_typeof(payload->'meta'->'tag') = 'null'",
		"prj-golden", "gen-golden-1"); has != 1 {
		t.Errorf("p-2 lost its present-null meta.tag")
	}
	s.requireEdgeRow(t, "prj-golden", "gen-golden-1", "referenced-by", "Patient", "p-1",
		"Organization", "o-1", "Patient.link.target", 0, "/fhir", strPtr("ek-org-link-1"))
	s.requireEdgeRow(t, "prj-golden", "gen-golden-1", "referenced-by", "Patient", "p-1",
		"Organization", "o-1", "Patient.link.target", 1, "/fhir", nil)

	// Ledger marker with exact commit accounting and the pinned digest.
	lw, found := s.witnessLedger(t, "pgstore06/golden/0")
	if !found {
		t.Fatal("ledger marker missing after successful batch")
	}
	if lw.digest != goldenDigest || lw.resources != 4 || lw.edges != 2 ||
		lw.project != "prj-golden" || lw.generation != "gen-golden-1" || lw.committedAt.IsZero() {
		t.Fatalf("ledger row = %+v, want digest %s resources 4 edges 2 prj-golden/gen-golden-1",
			lw, goldenDigest)
	}
	if total := s.mustInt64(t, "SELECT count(*) FROM "+s.qualified(batchLedgerTableName)); total != 1 {
		t.Errorf("ledger rows = %d, want exactly 1", total)
	}
	// committed_at is the finalize moment, not the claim-time now()
	// snapshot: it must be strictly after the transaction's snapshot time
	// observed inside the same transaction. A writer that kept the
	// DEFAULT now() would store the transaction start exactly, and this
	// strict comparison would fail.
	if !lw.committedAt.After(f.txStart) {
		t.Errorf("ledger committed_at = %v, want strictly after the batch transaction start %v "+
			"(finalize must stamp clock_timestamp(), not keep the claim-time now() default)",
			lw.committedAt, f.txStart)
	}

	// ON COMMIT DROP: witnessed on the writer's own single-connection pool,
	// so the check runs on the very backend that held the temp tables.
	writerPool := writer.src.(*Client)
	conn, err := writerPool.Acquire(s.ctx)
	if err != nil {
		s.fail("acquire writer pool connection", err)
	}
	defer conn.Release()
	var stagingGone bool
	if err := conn.QueryRow(s.ctx,
		"SELECT to_regclass('pg_temp.stg_batch_resource') IS NULL"+
			" AND to_regclass('pg_temp.stg_batch_edge') IS NULL").Scan(&stagingGone); err != nil {
		s.fail("witness staging drop on writer backend", err)
	}
	if !stagingGone {
		t.Errorf("staging temp tables survived commit on the writer backend")
	}
}

// ---------------------------------------------------------------------------
// Acceptance 2: deterministic identity + digest make retries no-ops and
// reuse-with-different-content a typed conflict. Two real connections.
// ---------------------------------------------------------------------------

func TestBatchWriterReplayOfIdenticalCommittedBatchIsNoOpTwoConnections(t *testing.T) {
	s := newBatchService(t)
	s.migrate(t)

	// The replay must be a real second session, not a session-local
	// illusion: two single-connection pools, two distinct physical
	// backends, witnessed by pg_backend_pid on each writer's own batch
	// transaction (the leases precedent).
	writerA := s.newWriterOn(t, s.openClient(t, 1))
	writerB := s.newWriterOn(t, s.openClient(t, 1))
	var pidA, pidB int64
	captureA := func(step, _ string, tx pgx.Tx) {
		if step == stepBegin {
			if err := tx.QueryRow(s.ctx, "SELECT pg_backend_pid()").Scan(&pidA); err != nil {
				s.fail("witness writer A backend", err)
			}
		}
	}
	captureB := func(step, _ string, tx pgx.Tx) {
		if step == stepBegin {
			if err := tx.QueryRow(s.ctx, "SELECT pg_backend_pid()").Scan(&pidB); err != nil {
				s.fail("witness writer B backend", err)
			}
		}
	}
	writerA.inTxProbe = captureA
	writerB.inTxProbe = captureB

	first, err := writerA.WriteBatch(s.ctx, goldenBatch())
	if err != nil {
		s.fail("first WriteBatch", err)
	}
	if first.Replay || first.CommittedResources != 4 || first.CommittedEdges != 2 {
		t.Fatalf("first result = %+v, want a fresh 4/2 commit", first)
	}
	before, found := s.witnessLedger(t, "pgstore06/golden/0")
	if !found {
		t.Fatal("ledger marker missing after first commit")
	}

	// Replay on the second writer: no-op, original accounting from the
	// ledger, nothing written.
	replay, err := writerB.WriteBatch(s.ctx, goldenBatch())
	if err != nil {
		s.fail("replay WriteBatch", err)
	}
	if !replay.Replay || replay.CommittedResources != 4 || replay.CommittedEdges != 2 ||
		replay.Digest != goldenDigest || replay.BatchID != "pgstore06/golden/0" {
		t.Fatalf("replay result = %+v, want replay of 4/2 with digest %s", replay, goldenDigest)
	}
	if pidA == 0 || pidB == 0 || pidA == pidB {
		t.Fatalf("writer backends A=%d B=%d, want two distinct real PG connections", pidA, pidB)
	}
	after, found := s.witnessLedger(t, "pgstore06/golden/0")
	if !found {
		t.Fatal("ledger marker vanished after replay")
	}
	if !after.committedAt.Equal(before.committedAt) || after.digest != before.digest ||
		after.resources != 4 || after.edges != 2 {
		t.Fatalf("ledger changed across replay: before %+v after %+v", before, after)
	}
	if n := s.countIn(t, resourceTableName, "prj-golden", "gen-golden-1"); n != 4 {
		t.Errorf("fhir_resource rows after replay = %d, want still 4 (no-op)", n)
	}
	if n := s.countIn(t, edgeTableName, "prj-golden", "gen-golden-1"); n != 2 {
		t.Errorf("fhir_edge_occurrence rows after replay = %d, want still 2 (no-op)", n)
	}
}

func TestBatchWriterIdentityReuseWithDifferentContentReturnsTypedConflict(t *testing.T) {
	s := newBatchService(t)
	s.migrate(t)
	writer := s.newWriter(t)

	if _, err := writer.WriteBatch(s.ctx, goldenBatch()); err != nil {
		s.fail("seed WriteBatch", err)
	}

	// Mutation probe: retry the same identity with one payload mutated.
	// A guard that only looked up batch_id (never compared digests) would
	// pass this test vacuously — the typed conflict below is what makes a
	// broken guard fail.
	mutated := goldenBatch()
	mutated.Resources[1].Payload = json.RawMessage(`{"resourceType":"Patient","id":"p-2","meta":{"tag":"mutated"}}`)
	_, err := writer.WriteBatch(s.ctx, mutated)
	var idErr *BatchIdentityConflictError
	if !errors.As(err, &idErr) || !errors.Is(err, ErrBatchIdentityConflict) {
		t.Fatalf("mutated retry = %v, want *BatchIdentityConflictError", err)
	}
	if idErr.BatchID != "pgstore06/golden/0" || idErr.LedgerDigest != goldenDigest ||
		idErr.ProvidedDigest == goldenDigest || len(idErr.ProvidedDigest) != 64 {
		t.Fatalf("conflict fields = %+v, want ledger digest %s and a differing provided digest",
			idErr, goldenDigest)
	}

	// Nothing moved: the ledger keeps the original fingerprint and the
	// stored rows keep their original payloads.
	lw, found := s.witnessLedger(t, "pgstore06/golden/0")
	if !found || lw.digest != goldenDigest || lw.resources != 4 || lw.edges != 2 {
		t.Fatalf("ledger after refused retry = %+v, want the untouched golden row", lw)
	}
	p2 := `{"resourceType":"Patient","id":"p-2","meta":{"tag":null}}`
	s.requireResourceRow(t, "prj-golden", "gen-golden-1", "Patient", "p-2", "", &p2)
	if n := s.countIn(t, resourceTableName, "prj-golden", "gen-golden-1"); n != 4 {
		t.Errorf("fhir_resource rows after refused retry = %d, want still 4", n)
	}
}

func TestBatchWriterTamperedLedgerDigestRefusesIdenticalReplay(t *testing.T) {
	s := newBatchService(t)
	s.migrate(t)
	writer := s.newWriter(t)

	if _, err := writer.WriteBatch(s.ctx, goldenBatch()); err != nil {
		s.fail("seed WriteBatch", err)
	}
	// Simulate a ledger whose fingerprint authority was broken (e.g.
	// hand-edited): an identical replay must NOT report a no-op against a
	// digest it cannot vouch for. Without the digest comparison this would
	// silently claim success — the mutation probe makes that fatal.
	s.exec(t, "UPDATE "+s.qualified(batchLedgerTableName)+" SET digest = $1 WHERE batch_id = $2",
		strings.Repeat("0", 64), "pgstore06/golden/0")
	_, err := writer.WriteBatch(s.ctx, goldenBatch())
	var idErr *BatchIdentityConflictError
	if !errors.As(err, &idErr) || !errors.Is(err, ErrBatchIdentityConflict) {
		t.Fatalf("replay after digest tamper = %v, want *BatchIdentityConflictError", err)
	}
	if idErr.LedgerDigest != strings.Repeat("0", 64) || idErr.ProvidedDigest != goldenDigest {
		t.Fatalf("conflict fields = %+v, want the tampered ledger digest and the golden provided digest", idErr)
	}
	if n := s.countIn(t, resourceTableName, "prj-golden", "gen-golden-1"); n != 4 {
		t.Errorf("fhir_resource rows = %d, want the batch to have written nothing", n)
	}
}

// ---------------------------------------------------------------------------
// Acceptance 3: duplicate natural identities with different content return
// typed conflicts and preserve the prior row; identical replay is idempotent.
// ---------------------------------------------------------------------------

func TestBatchWriterInBatchDuplicateResourceIdentityDifferentContentRollsBack(t *testing.T) {
	s := newBatchService(t)
	s.migrate(t)
	writer := s.newWriter(t)

	mk := func() Batch {
		return Batch{ID: "prj/dup-inbatch", Project: batchProject, DatasetGeneration: batchGenA, Resources: []source.Resource{
			litResource("Patient", "p-dup", "/fhir", strPtr(`{"id":"p-dup","v":"first"}`)),
			litResource("Patient", "p-dup", "/fhir", strPtr(`{"id":"p-dup","v":"second"}`)),
			litResource("Patient", "p-ok", "", nil),
		}}
	}
	_, err := writer.WriteBatch(s.ctx, mk())
	var rowErr *BatchResourceConflictError
	if !errors.As(err, &rowErr) || !errors.Is(err, ErrBatchRowConflict) {
		t.Fatalf("in-batch duplicate = %v, want *BatchResourceConflictError", err)
	}
	if !rowErr.InBatch || rowErr.ResourceType != "Patient" || rowErr.LogicalID != "p-dup" ||
		rowErr.Project != batchProject || rowErr.DatasetGeneration != batchGenA {
		t.Fatalf("conflict fields = %+v, want the in-batch Patient/p-dup identity in batch scope", rowErr)
	}
	// The WHOLE batch rolled back: even the unrelated p-ok row is absent,
	// and the rolled-back claim left no marker.
	s.requireNothingWritten(t, batchProject, batchGenA, "prj/dup-inbatch")

	// Control: the same batch identity minus the duplicate commits, which
	// also proves the rolled-back claim never consumed the identity.
	b := mk()
	b.Resources = append(b.Resources[:0:2], b.Resources[0], b.Resources[2])
	res, err := writer.WriteBatch(s.ctx, b)
	if err != nil {
		s.fail("control WriteBatch", err)
	}
	if res.CommittedResources != 2 || res.Replay {
		t.Fatalf("control result = %+v, want a fresh 2-row commit", res)
	}
}

func TestBatchWriterStoredResourceIdentityDifferentContentPreservesPriorRow(t *testing.T) {
	s := newBatchService(t)
	s.migrate(t)
	writer := s.newWriter(t)

	first := Batch{ID: "prj/stored-first", Project: batchProject, DatasetGeneration: batchGenA, Resources: []source.Resource{
		litResource("Patient", "p-1", "/fhir", strPtr(`{"id":"p-1","v":"stored"}`)),
	}}
	if _, err := writer.WriteBatch(s.ctx, first); err != nil {
		s.fail("seed WriteBatch", err)
	}

	// Different payload under the same natural identity: typed conflict,
	// prior row preserved byte-for-byte.
	mutated := first
	mutated.ID = "prj/stored-second"
	mutated.Resources = []source.Resource{
		litResource("Patient", "p-1", "/fhir", strPtr(`{"id":"p-1","v":"replaced"}`)),
	}
	_, err := writer.WriteBatch(s.ctx, mutated)
	var rowErr *BatchResourceConflictError
	if !errors.As(err, &rowErr) || rowErr.InBatch {
		t.Fatalf("stored-row duplicate = %v, want stored-row *BatchResourceConflictError", err)
	}
	prior := `{"id":"p-1","v":"stored"}`
	s.requireResourceRow(t, batchProject, batchGenA, "Patient", "p-1", "/fhir", &prior)

	// Same payload under the same identity but a different auth path is
	// still different content (auth path is part of the row): conflict.
	authMoved := first
	authMoved.ID = "prj/stored-third"
	authMoved.Resources = []source.Resource{
		litResource("Patient", "p-1", "/other", strPtr(`{"id":"p-1","v":"stored"}`)),
	}
	_, err = writer.WriteBatch(s.ctx, authMoved)
	if !errors.As(err, &rowErr) || rowErr.InBatch {
		t.Fatalf("auth-path-moved duplicate = %v, want stored-row *BatchResourceConflictError", err)
	}
	s.requireResourceRow(t, batchProject, batchGenA, "Patient", "p-1", "/fhir", &prior)

	// The refused attempts wrote no marker and no rows.
	if _, found := s.witnessLedger(t, "prj/stored-second"); found {
		t.Errorf("refused batch prj/stored-second left a marker")
	}
	if _, found := s.witnessLedger(t, "prj/stored-third"); found {
		t.Errorf("refused batch prj/stored-third left a marker")
	}
	if n := s.countIn(t, resourceTableName, batchProject, batchGenA); n != 1 {
		t.Errorf("fhir_resource rows = %d, want exactly 1 (the prior row)", n)
	}
}

func TestBatchWriterIdenticalRowsCollapseWithinAndAcrossBatches(t *testing.T) {
	s := newBatchService(t)
	s.migrate(t)
	writer := s.newWriter(t)

	dup := litResource("Patient", "p-dup", "/fhir", strPtr(`{"id":"p-dup"}`))
	b := Batch{ID: "prj/collapse-1", Project: batchProject, DatasetGeneration: batchGenA, Resources: []source.Resource{dup, dup,
		litResource("Patient", "p-2", "", nil),
		litResource("Patient", "p-2", "", nil),
	}}
	res, err := writer.WriteBatch(s.ctx, b)
	if err != nil {
		s.fail("collapse WriteBatch", err)
	}
	if res.CommittedResources != 2 || res.SkippedIdenticalResources != 2 || res.Replay {
		t.Fatalf("result = %+v, want 2 committed and 2 collapsed", res)
	}
	if n := s.countIn(t, resourceTableName, batchProject, batchGenA); n != 2 {
		t.Fatalf("fhir_resource rows = %d, want 2 distinct rows", n)
	}
	lw, found := s.witnessLedger(t, "prj/collapse-1")
	if !found || lw.resources != 2 {
		t.Fatalf("ledger = %+v, want resource_count 2 (committed rows, not offered rows)", lw)
	}

	// Row-level idempotence across a NEW batch identity: identical content
	// is skipped, not conflicted, and nothing duplicates.
	b2 := Batch{ID: "prj/collapse-2", Project: batchProject, DatasetGeneration: batchGenA, Resources: []source.Resource{dup, dup}}
	res2, err := writer.WriteBatch(s.ctx, b2)
	if err != nil {
		s.fail("cross-batch replay WriteBatch", err)
	}
	if res2.CommittedResources != 0 || res2.SkippedIdenticalResources != 2 || res2.Replay {
		t.Fatalf("cross-batch result = %+v, want 0 committed 2 skipped fresh batch", res2)
	}
	if n := s.countIn(t, resourceTableName, batchProject, batchGenA); n != 2 {
		t.Errorf("fhir_resource rows after cross-batch identical replay = %d, want still 2", n)
	}
}

func TestBatchWriterEdgeDuplicateOccurrenceDifferentContentPreservesPriorRow(t *testing.T) {
	s := newBatchService(t)
	s.migrate(t)
	writer := s.newWriter(t)

	ownerRow := litResource("Patient", "p-1", "/fhir", strPtr(`{"id":"p-1"}`))
	edge := litEdge("referenced-by", "Patient", "p-1", "Organization", "o-1", "Patient.link.target", 0, "/fhir", strPtr("k1"))
	first := Batch{ID: "prj/edge-first", Project: batchProject, DatasetGeneration: batchGenA,
		Resources: []source.Resource{ownerRow}, Edges: []EdgeOccurrenceRow{edge}}
	if _, err := writer.WriteBatch(s.ctx, first); err != nil {
		s.fail("seed edge WriteBatch", err)
	}

	// Same occurrence identity, different logical edge key: typed conflict,
	// the stored key survives.
	reshaped := first
	reshaped.ID = "prj/edge-second"
	reshaped.Resources = nil
	reshaped.Edges = []EdgeOccurrenceRow{
		litEdge("referenced-by", "Patient", "p-1", "Organization", "o-1", "Patient.link.target", 0, "/fhir", strPtr("k2")),
	}
	_, err := writer.WriteBatch(s.ctx, reshaped)
	var edgeErr *BatchEdgeConflictError
	if !errors.As(err, &edgeErr) || !errors.Is(err, ErrBatchRowConflict) || edgeErr.InBatch {
		t.Fatalf("stored edge duplicate = %v, want stored-row *BatchEdgeConflictError", err)
	}
	if edgeErr.SourceLogicalID != "p-1" || edgeErr.Label != "referenced-by" || edgeErr.SourceOrdinal != 0 {
		t.Fatalf("conflict fields = %+v, want the refused occurrence identity", edgeErr)
	}
	s.requireEdgeRow(t, batchProject, batchGenA, "referenced-by", "Patient", "p-1",
		"Organization", "o-1", "Patient.link.target", 0, "/fhir", strPtr("k1"))

	// In-batch duplicate of one occurrence identity with differing auth
	// path: typed conflict, whole batch rolls back.
	dupe := Batch{ID: "prj/edge-dupe", Project: batchProject, DatasetGeneration: batchGenA,
		Resources: []source.Resource{ownerRow},
		Edges: []EdgeOccurrenceRow{
			litEdge("referenced-by", "Patient", "p-1", "Organization", "o-2", "Patient.link.target", 0, "/fhir", nil),
			litEdge("referenced-by", "Patient", "p-1", "Organization", "o-2", "Patient.link.target", 0, "", nil),
		}}
	_, err = writer.WriteBatch(s.ctx, dupe)
	if !errors.As(err, &edgeErr) || !edgeErr.InBatch || edgeErr.TargetLogicalID != "o-2" {
		t.Fatalf("in-batch edge duplicate = %v, want in-batch *BatchEdgeConflictError for o-2", err)
	}
	if n := s.countIn(t, edgeTableName, batchProject, batchGenA); n != 1 {
		t.Errorf("edge rows = %d, want exactly 1 (the prior occurrence)", n)
	}
}

func TestBatchWriterEdgeSourceMissingRollsBackWholeBatch(t *testing.T) {
	s := newBatchService(t)
	s.migrate(t)
	writer := s.newWriter(t)

	ghost := Batch{ID: "prj/edge-ghost", Project: batchProject, DatasetGeneration: batchGenA,
		Resources: []source.Resource{litResource("Patient", "p-ok", "", nil)},
		Edges: []EdgeOccurrenceRow{
			litEdge("referenced-by", "Patient", "p-ghost", "Organization", "o-1", "a", 0, "", nil),
		}}
	_, err := writer.WriteBatch(s.ctx, ghost)
	var missing *EdgeSourceMissingError
	if !errors.As(err, &missing) || !errors.Is(err, ErrEdgeSourceMissing) {
		t.Fatalf("missing source = %v, want *EdgeSourceMissingError", err)
	}
	if missing.Project != batchProject || missing.DatasetGeneration != batchGenA ||
		missing.SourceResourceType != "Patient" || missing.SourceLogicalID != "p-ghost" {
		t.Fatalf("missing-source fields = %+v, want Patient/p-ghost in batch scope", missing)
	}
	// The whole batch rolled back — including its valid resource row.
	s.requireNothingWritten(t, batchProject, batchGenA, "prj/edge-ghost")

	// Control: identical content except the source is present commits both.
	ghost.ID = "prj/edge-ok"
	ghost.Edges[0].SourceLogicalID = "p-ok"
	res, err := writer.WriteBatch(s.ctx, ghost)
	if err != nil {
		s.fail("control edge WriteBatch", err)
	}
	if res.CommittedResources != 1 || res.CommittedEdges != 1 {
		t.Fatalf("control result = %+v, want 1/1", res)
	}
}

// ---------------------------------------------------------------------------
// Acceptance 4: injected failure before commit leaves no rows, no marker;
// result counts equal committed rows.
// ---------------------------------------------------------------------------

func TestBatchWriterInjectedFailureBeforeCommitLeavesNoRowsOrMarker(t *testing.T) {
	s := newBatchService(t)
	s.migrate(t)

	injected := errors.New("injected failure")
	steps := []string{stepBegin, stepReadLedger, stepClaimLedger, stepStageResources,
		stepCheckResources, stepPublishResourc, stepStageEdges, stepCheckEdges,
		stepPublishEdges, stepFinalizeLedger, stepCommit}
	for _, target := range steps {
		writer := s.newWriter(t)
		writer.failHook = func(step string) error {
			if step == target {
				return injected
			}
			return nil
		}
		_, err := writer.WriteBatch(s.ctx, goldenBatch())
		var writeErr *BatchWriteError
		if !errors.As(err, &writeErr) || !errors.Is(err, ErrBatchWrite) || !errors.Is(err, injected) {
			t.Fatalf("failure at %s = %v, want *BatchWriteError chaining the injected error", target, err)
		}
		s.requireNothingWritten(t, "prj-golden", "gen-golden-1", "pgstore06/golden/0")
	}

	// Non-vacuous witnesses: the same batch with no injected failure
	// commits exactly its rows and marker into the very tables the loop
	// above asserted empty, and reusing every burned identity works.
	writer := s.newWriter(t)
	res, err := writer.WriteBatch(s.ctx, goldenBatch())
	if err != nil {
		s.fail("control WriteBatch", err)
	}
	if res.Replay || res.CommittedResources != 4 || res.CommittedEdges != 2 {
		t.Fatalf("control result = %+v, want a fresh 4/2 commit on the same identity", res)
	}
	if n := s.countIn(t, resourceTableName, "prj-golden", "gen-golden-1"); n != 4 {
		t.Errorf("fhir_resource rows after control = %d, want 4", n)
	}
	if n := s.countIn(t, edgeTableName, "prj-golden", "gen-golden-1"); n != 2 {
		t.Errorf("fhir_edge_occurrence rows after control = %d, want 2", n)
	}
	if _, found := s.witnessLedger(t, "pgstore06/golden/0"); !found {
		t.Error("ledger marker missing after control commit")
	}
}

func TestBatchWriterResultCountsEqualCommittedRows(t *testing.T) {
	s := newBatchService(t)
	s.migrate(t)
	writer := s.newWriter(t)

	// Pre-seed one row by direct SQL: the batch offers it again (identical
	// content), so the committed counts must exclude it while the table
	// total includes it.
	s.exec(t, "INSERT INTO "+s.qualified(resourceTableName)+
		" (project, dataset_generation, resource_type, logical_id, auth_resource_path, payload)"+
		" VALUES ($1, $2, 'Patient', 'p-pre', '/fhir', $3::jsonb)",
		batchProject, batchGenA, `{"id":"p-pre"}`)

	b := Batch{ID: "prj/counts", Project: batchProject, DatasetGeneration: batchGenA,
		Resources: []source.Resource{
			litResource("Patient", "p-pre", "/fhir", strPtr(`{"id":"p-pre"}`)), // already stored
			litResource("Patient", "p-new", "", nil),
			litResource("Patient", "p-new", "", nil), // in-batch identical duplicate
		},
		Edges: []EdgeOccurrenceRow{
			litEdge("referenced-by", "Patient", "p-new", "Observation", "o-1", "a", 0, "", nil),
			litEdge("referenced-by", "Patient", "p-new", "Observation", "o-1", "a", 0, "", nil),
		}}
	res, err := writer.WriteBatch(s.ctx, b)
	if err != nil {
		s.fail("counted WriteBatch", err)
	}
	if res.CommittedResources != 1 || res.CommittedEdges != 1 {
		t.Fatalf("result = %+v, want 1 committed resource and 1 committed edge", res)
	}
	if res.SkippedIdenticalResources != 2 || res.SkippedIdenticalEdges != 1 {
		t.Fatalf("result = %+v, want 2 skipped resources and 1 skipped edge", res)
	}
	lw, found := s.witnessLedger(t, "prj/counts")
	if !found || lw.resources != res.CommittedResources || lw.edges != res.CommittedEdges {
		t.Fatalf("ledger = %+v, want the result's committed counts %d/%d",
			lw, res.CommittedResources, res.CommittedEdges)
	}
	if n := s.countIn(t, resourceTableName, batchProject, batchGenA); n != 2 {
		t.Errorf("fhir_resource rows = %d, want 2 (pre-seed + one committed)", n)
	}
	if n := s.countIn(t, edgeTableName, batchProject, batchGenA); n != 1 {
		t.Errorf("fhir_edge_occurrence rows = %d, want 1", n)
	}
}

// ---------------------------------------------------------------------------
// Acceptance (bounded): capacity limits hold at the boundary.
// ---------------------------------------------------------------------------

func TestBatchWriterBoundedCapacity(t *testing.T) {
	s := newBatchService(t)
	s.migrate(t)
	writer := s.newWriter(t)

	full := Batch{ID: "prj/bounded", Project: batchProject, DatasetGeneration: batchGenA}
	for i := 0; i < MaxBatchResources; i++ {
		full.Resources = append(full.Resources,
			litResource("Patient", fmt.Sprintf("b-%04d", i), "", nil))
	}
	for i := 0; i < MaxBatchEdges; i++ {
		full.Edges = append(full.Edges, litEdge("referenced-by", "Patient",
			fmt.Sprintf("b-%04d", i), "Observation", "o-x", "a", int64(i), "", nil))
	}
	res, err := writer.WriteBatch(s.ctx, full)
	if err != nil {
		s.fail("full-capacity WriteBatch", err)
	}
	if res.CommittedResources != MaxBatchResources || res.CommittedEdges != MaxBatchEdges {
		t.Fatalf("full result = %+v, want %d/%d committed", res, MaxBatchResources, MaxBatchEdges)
	}
	if n := s.countIn(t, resourceTableName, batchProject, batchGenA); n != MaxBatchResources {
		t.Errorf("fhir_resource rows = %d, want %d", n, MaxBatchResources)
	}
	if n := s.countIn(t, edgeTableName, batchProject, batchGenA); n != MaxBatchEdges {
		t.Errorf("fhir_edge_occurrence rows = %d, want %d", n, MaxBatchEdges)
	}
}

// ---------------------------------------------------------------------------
// Acceptance 5: two concurrent independent batches for separate generations
// do not share state or serialize on a process-wide mutex. Two real
// connections with distinct pg_backend_pid.
// ---------------------------------------------------------------------------

func TestBatchWriterConcurrentIndependentBatchesOverlapTwoConnections(t *testing.T) {
	s := newBatchService(t)
	s.migrate(t)

	// One writer instance, one pool with two connections: if any hidden
	// process-wide (or writer-wide) mutex serialized batches, the second
	// goroutine could never reach the publish step while the first waits
	// there, and the rendezvous below would time out.
	writer := s.newWriterOn(t, s.openClient(t, 2))

	arrived := map[string]chan struct{}{
		"prj/conc-a": make(chan struct{}),
		"prj/conc-b": make(chan struct{}),
	}
	var pids sync.Map    // batchID -> int64
	var stalled sync.Map // batchID -> bool
	writer.inTxProbe = func(step, batchID string, tx pgx.Tx) {
		if step != stepPublishEdges {
			return
		}
		var pid int64
		if err := tx.QueryRow(s.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			stalled.Store(batchID, true)
			return
		}
		pids.Store(batchID, pid)
		close(arrived[batchID])
		other := "prj/conc-b"
		if batchID == other {
			other = "prj/conc-a"
		}
		select {
		case <-arrived[other]:
		case <-time.After(20 * time.Second):
			stalled.Store(batchID, true)
		}
	}

	type outcome struct {
		res BatchResult
		err error
	}
	outcomes := make(chan outcome, 2)
	go func() {
		res, err := writer.WriteBatch(s.ctx, concurrentBatch("prj/conc-a", batchGenA, "ga"))
		outcomes <- outcome{res, err}
	}()
	go func() {
		res, err := writer.WriteBatch(s.ctx, concurrentBatch("prj/conc-b", batchGenB, "gb"))
		outcomes <- outcome{res, err}
	}()
	var results [2]BatchResult
	for i := 0; i < 2; i++ {
		select {
		case o := <-outcomes:
			if o.err != nil {
				s.fail("concurrent WriteBatch", o.err)
			}
			results[i] = o.res
		case <-time.After(60 * time.Second):
			t.Fatal("concurrent batches did not finish")
		}
	}

	for _, id := range []string{"prj/conc-a", "prj/conc-b"} {
		if v, loaded := stalled.Load(id); loaded && v.(bool) {
			t.Fatalf("batch %s never overlapped the other at the publish step: "+
				"the writer serialized independent batches", id)
		}
	}
	vA, _ := pids.Load("prj/conc-a")
	vB, _ := pids.Load("prj/conc-b")
	if vA == nil || vB == nil || vA.(int64) == vB.(int64) {
		t.Fatalf("concurrent batches ran on backends %v/%v, want two distinct real PG connections", vA, vB)
	}

	// Independent scopes: each generation holds exactly its own rows and
	// its own marker; neither batch leaked rows into the other's scope.
	for _, g := range []struct {
		id, gen, prefix string
	}{{"prj/conc-a", batchGenA, "ga"}, {"prj/conc-b", batchGenB, "gb"}} {
		if n := s.countIn(t, resourceTableName, batchProject, g.gen); n != 2 {
			t.Errorf("fhir_resource rows for %s = %d, want 2 (no cross-batch sharing)", g.gen, n)
		}
		if n := s.countIn(t, edgeTableName, batchProject, g.gen); n != 1 {
			t.Errorf("fhir_edge_occurrence rows for %s = %d, want 1", g.gen, n)
		}
		s.requireResourceRow(t, batchProject, g.gen, "Patient", g.prefix+"-1", "/fhir",
			strPtr(`{"id":"`+g.prefix+`-1"}`))
		s.requireResourceRow(t, batchProject, g.gen, "Patient", g.prefix+"-2", "", nil)
		lw, found := s.witnessLedger(t, g.id)
		if !found || lw.generation != g.gen || lw.resources != 2 || lw.edges != 1 {
			t.Errorf("ledger for %s = %+v, want 2/1 in generation %s", g.id, lw, g.gen)
		}
	}
}

func concurrentBatch(id, generation, prefix string) Batch {
	return Batch{ID: id, Project: batchProject, DatasetGeneration: generation,
		Resources: []source.Resource{
			{Project: batchProject, DatasetGeneration: generation, ResourceType: "Patient",
				LogicalID: prefix + "-1", AuthResourcePath: "/fhir",
				Payload: json.RawMessage(`{"id":"` + prefix + `-1"}`)},
			{Project: batchProject, DatasetGeneration: generation, ResourceType: "Patient",
				LogicalID: prefix + "-2", AuthResourcePath: "", Payload: nil},
		},
		Edges: []EdgeOccurrenceRow{
			{Project: batchProject, DatasetGeneration: generation, AuthResourcePath: "",
				Label: "referenced-by", SourceResourceType: "Patient", SourceLogicalID: prefix + "-1",
				TargetResourceType: "Observation", TargetLogicalID: "o-x",
				SourcePath: "a", SourceOrdinal: 0, LogicalEdgeKey: nil},
		}}
}

// ---------------------------------------------------------------------------
// Claim races on one identity: two real connections, distinct backends.
// ---------------------------------------------------------------------------

// waitBackendWaiting polls pg_stat_activity until the backend serving
// application app is actively blocked on a lock, and returns its pid.
func (s *batchService) waitBackendWaiting(t *testing.T, app string) int64 {
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
			s.fail("waiting for backend "+app+" to block on a row lock",
				fmt.Errorf("backend never entered a lock wait"))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestBatchWriterConcurrentClaimOfSameBatchCommitsOnceAndReplaysTwoConnections(t *testing.T) {
	s := newBatchService(t)
	s.migrate(t)

	claimHeld := make(chan struct{})
	release := make(chan struct{})
	var t1PID int64
	var t1ProbeErr error
	var t1PIDMu sync.Mutex
	writer1 := s.newWriterOn(t, s.openAppClient(t, "pgstore06-t1", 1))
	// The probe runs on the WriteBatch goroutine, so it must never call
	// t.Fatalf there: a Goexit on that goroutine would strand the r1
	// receive below into a package timeout. The probe records its outcome
	// and the main goroutine fails after the channel handoff, like the
	// golden test's probeErrs and the overlap test's stalled flag.
	writer1.inTxProbe = func(step, batchID string, tx pgx.Tx) {
		if step != stepClaimLedger {
			return
		}
		t1PIDMu.Lock()
		t1ProbeErr = tx.QueryRow(s.ctx, "SELECT pg_backend_pid()").Scan(&t1PID)
		t1PIDMu.Unlock()
		close(claimHeld)
		<-release
	}
	writer2 := s.newWriterOn(t, s.openAppClient(t, "pgstore06-t2", 1))

	type outcome struct {
		res BatchResult
		err error
	}
	r1 := make(chan outcome, 1)
	go func() {
		res, err := writer1.WriteBatch(s.ctx, goldenBatch())
		r1 <- outcome{res, err}
	}()

	// T1 owns the claim, uncommitted; if it died before the probe, fail
	// with its outcome instead of blocking forever.
	select {
	case <-claimHeld:
	case o := <-r1:
		s.fail("T1 never reached the claim probe", o.err)
	}
	r2 := make(chan outcome, 1)
	go func() {
		res, err := writer2.WriteBatch(s.ctx, goldenBatch())
		r2 <- outcome{res, err}
	}()

	// T2 must be genuinely blocked on T1's in-flight ledger claim — a
	// session-local illusion is impossible because these are separate
	// pools (verified below via pg_stat_activity pid vs T1's tx pid).
	t2PID := s.waitBackendWaiting(t, "pgstore06-t2")
	close(release)
	o1 := <-r1
	o2 := <-r2
	// Both writer goroutines have handed off; every failure now lands on
	// the main test goroutine.
	t1PIDMu.Lock()
	heldT1, probeErr := t1PID, t1ProbeErr
	t1PIDMu.Unlock()
	if probeErr != nil {
		s.fail("witness t1 backend", probeErr)
	}
	if heldT1 == 0 || heldT1 == t2PID {
		t.Fatalf("backends t1=%d t2=%d, want two distinct real PG connections", heldT1, t2PID)
	}
	if o1.err != nil {
		s.fail("T1 WriteBatch", o1.err)
	}
	if o2.err != nil {
		s.fail("T2 WriteBatch", o2.err)
	}
	if o1.res.Replay || o1.res.CommittedResources != 4 || o1.res.CommittedEdges != 2 {
		t.Fatalf("T1 result = %+v, want the fresh 4/2 commit", o1.res)
	}
	if !o2.res.Replay || o2.res.CommittedResources != 4 || o2.res.CommittedEdges != 2 {
		t.Fatalf("T2 result = %+v, want a replay of the original 4/2 accounting", o2.res)
	}
	if n := s.mustInt64(t, "SELECT count(*) FROM "+s.qualified(batchLedgerTableName)); n != 1 {
		t.Errorf("ledger rows = %d, want exactly 1", n)
	}
	if n := s.countIn(t, resourceTableName, "prj-golden", "gen-golden-1"); n != 4 {
		t.Errorf("fhir_resource rows = %d, want exactly 4 (committed once)", n)
	}
	if n := s.countIn(t, edgeTableName, "prj-golden", "gen-golden-1"); n != 2 {
		t.Errorf("fhir_edge_occurrence rows = %d, want exactly 2 (committed once)", n)
	}
}

func TestBatchWriterAbandonedClaimDoesNotBurnIdentityTwoConnections(t *testing.T) {
	s := newBatchService(t)
	s.migrate(t)

	claimHeld := make(chan struct{})
	release := make(chan struct{})
	injected := errors.New("injected abort")
	writer1 := s.newWriterOn(t, s.openAppClient(t, "pgstore06-t1", 1))
	writer1.inTxProbe = func(step, batchID string, tx pgx.Tx) {
		if step != stepClaimLedger {
			return
		}
		close(claimHeld)
		<-release
	}
	writer1.failHook = func(step string) error {
		if step == stepPublishResourc {
			return injected
		}
		return nil
	}
	writer2 := s.newWriterOn(t, s.openAppClient(t, "pgstore06-t2", 1))

	type outcome struct {
		res BatchResult
		err error
	}
	r1 := make(chan outcome, 1)
	go func() {
		res, err := writer1.WriteBatch(s.ctx, goldenBatch())
		r1 <- outcome{res, err}
	}()
	// If T1 died before the probe, fail with its outcome instead of
	// blocking forever; the probe itself records nothing that could Goexit
	// this goroutine.
	select {
	case <-claimHeld:
	case o := <-r1:
		s.fail("T1 never reached the claim probe", o.err)
	}
	r2 := make(chan outcome, 1)
	go func() {
		res, err := writer2.WriteBatch(s.ctx, goldenBatch())
		r2 <- outcome{res, err}
	}()
	s.waitBackendWaiting(t, "pgstore06-t2")
	close(release) // T1 now aborts during resource publish; T2 must take over

	o1 := <-r1
	if !errors.Is(o1.err, injected) {
		t.Fatalf("T1 = %v, want the injected abort", o1.err)
	}
	o2 := <-r2
	if o2.err != nil {
		s.fail("T2 WriteBatch after takeover", o2.err)
	}
	if o2.res.Replay || o2.res.CommittedResources != 4 || o2.res.CommittedEdges != 2 {
		t.Fatalf("T2 result = %+v, want a fresh 4/2 commit after T1's abandoned claim", o2.res)
	}
	if n := s.countIn(t, resourceTableName, "prj-golden", "gen-golden-1"); n != 4 {
		t.Errorf("fhir_resource rows = %d, want exactly 4", n)
	}
	lw, found := s.witnessLedger(t, "pgstore06/golden/0")
	if !found || lw.digest != goldenDigest {
		t.Fatalf("ledger = %+v found=%v, want the golden marker", lw, found)
	}
}

// ---------------------------------------------------------------------------
// Migration 000008: self-verified pin and runner double-apply probe.
// ---------------------------------------------------------------------------

func TestBatchWriterMigrationChecksumSelfVerifies(t *testing.T) {
	const fileName = "migrations/000008_batch_write_ledger.sql"
	onDisk, err := os.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read %s: %v", fileName, err)
	}
	sum := sha256.Sum256(onDisk)
	diskChecksum := hex.EncodeToString(sum[:])

	want, pinned := wantEmbeddedChecksums[8]
	if !pinned {
		t.Fatal("migration 8 has no pinned checksum in migrate_test.go")
	}
	if want.name != "batch_write_ledger" || want.checksum != diskChecksum {
		t.Fatalf("pin = {Name:%q Checksum:%q}, want {Name:%q Checksum:%q} — the pin must be the "+
			"independently computed sha256 of the file's exact bytes",
			want.name, want.checksum, "batch_write_ledger", diskChecksum)
	}
	for _, m := range mustEmbeddedMigrations(t) {
		if m.Version == 8 {
			if m.Name != want.name || m.Checksum != diskChecksum {
				t.Fatalf("embedded migration 8 = {Name:%q Checksum:%q}, want the disk sha256 %s",
					m.Name, m.Checksum, diskChecksum)
			}
			return
		}
	}
	t.Fatal("migration 8 is absent from the embedded history")
}

func TestBatchWriterMigrationDoubleApplyAndCrashWindowConverge(t *testing.T) {
	s := newBatchService(t)
	client := s.openClient(t, 4)

	first, err := MigrateSchema(s.ctx, client, s.schema)
	if err != nil {
		s.fail("first MigrateSchema", err)
	}
	// pg-catalog-05 appends migration 9 after batch_write_ledger, so the
	// newest applied version is 9; this test pins its own migration 8 row
	// by identity, not by position.
	if first.LatestVersion != 9 {
		t.Fatalf("latest version = %d, want 9", first.LatestVersion)
	}
	if len(first.Applied) < 8 {
		t.Fatalf("applied %d migrations, want at least 8", len(first.Applied))
	}
	last := first.Applied[7]
	if last.Version != 8 || last.Name != "batch_write_ledger" ||
		last.Checksum != wantEmbeddedChecksums[8].checksum {
		t.Fatalf("applied row 8 = %+v, want the pinned batch_write_ledger row", last)
	}

	// Double apply: the same history applies as a no-op and reports the
	// identical ledger.
	second, err := MigrateSchema(s.ctx, client, s.schema)
	if err != nil {
		s.fail("second MigrateSchema", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("double apply changed the result: first %+v second %+v", first, second)
	}
	if n := s.mustInt64(t, "SELECT count(*) FROM "+s.qualified(LedgerTable)+" WHERE version = 8"); n != 1 {
		t.Fatalf("ledger rows for version 8 = %d, want exactly 1 after double apply", n)
	}

	// Crash window the runner documents: committed migrations whose ledger
	// rows were lost. Re-applying must converge (the DDL is IF NOT EXISTS)
	// and re-record each row exactly once. The loss must cover the whole
	// trailing tail (version 8 and the pg-catalog-05 version 9): the
	// runner refuses to re-record a middle version while a later one is
	// still recorded, which is exactly the history-edit guard this ticket
	// relies on.
	s.exec(t, "DELETE FROM "+s.qualified(LedgerTable)+" WHERE version >= 8")
	third, err := MigrateSchema(s.ctx, client, s.schema)
	if err != nil {
		s.fail("third MigrateSchema after lost ledger row", err)
	}
	// The converged history must match row-for-row except applied_at,
	// which legitimately re-stamps the single re-recorded version 8.
	type pin struct {
		version  int64
		name     string
		checksum string
	}
	pins := func(r MigrateResult) []pin {
		out := make([]pin, 0, len(r.Applied))
		for _, a := range r.Applied {
			out = append(out, pin{a.Version, a.Name, a.Checksum})
		}
		return out
	}
	if !reflect.DeepEqual(pins(second), pins(third)) {
		t.Fatalf("crash-window retry changed the recorded history: second %v third %v",
			pins(second), pins(third))
	}
	if n := s.mustInt64(t,
		"SELECT count(*) FROM "+s.qualified(LedgerTable)+" WHERE version = 8 AND checksum = $1",
		wantEmbeddedChecksums[8].checksum); n != 1 {
		t.Fatalf("ledger rows for version 8 after convergence = %d, want exactly 1", n)
	}
	if n := s.mustInt64(t,
		"SELECT count(*) FROM "+s.qualified(LedgerTable)+" WHERE version = 9 AND checksum = $1",
		wantEmbeddedChecksums[9].checksum); n != 1 {
		t.Fatalf("ledger rows for version 9 after convergence = %d, want exactly 1", n)
	}

	// The converged schema is functional: a real batch commits through it.
	writer := s.newWriterOn(t, client)
	res, err := writer.WriteBatch(s.ctx, goldenBatch())
	if err != nil {
		s.fail("WriteBatch after convergence", err)
	}
	if res.Replay || res.CommittedResources != 4 || res.CommittedEdges != 2 {
		t.Fatalf("result after convergence = %+v, want a fresh 4/2 commit", res)
	}
}

// ---------------------------------------------------------------------------
// Ownership-scoped zero-leftover audit.
// ---------------------------------------------------------------------------

// TestZZZeroLeftoverBatchWriterFixtures is the ownership-scoped zero-leftover
// audit. It checks, by exact name only, every database (and the schema that
// lived inside it) this file verified into existence. It performs no global
// scan of pg_database or pg_namespace, so concurrent sibling workers
// sweeping their own prefixes neither race this audit nor trip it.
func TestZZZeroLeftoverBatchWriterFixtures(t *testing.T) {
	raw := testPostgresURL(t)
	batchOwnedMu.Lock()
	owned := make(map[string]string, len(batchOwnedByName))
	for db, schema := range batchOwnedByName {
		owned[db] = schema
	}
	batchOwnedMu.Unlock()
	if len(owned) == 0 {
		t.Skip("this process created no pgstore06_it_* fixtures")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect to test service for audit: %v", err)
	}
	defer admin.Close()

	for db, schema := range owned {
		if !strings.HasPrefix(db, "pgstore06_it_") {
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
				// Report the schema inside the leftover database, exact
				// name only.
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
}
