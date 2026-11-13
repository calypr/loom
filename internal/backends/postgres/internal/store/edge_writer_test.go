package postgres

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

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	ing02StorePrefix = "verify_ing02_store_"
	ing02StoreFmt    = ing02StorePrefix + "%d_%s"

	ing02StoreProject = "prj-ing02-store"
	ing02StoreGen     = "gen-build-a"
	ing02StoreAuth    = "/fhir"
)

var ing02StoreNamePattern = regexp.MustCompile(`^verify_ing02_store_[0-9]+_[0-9a-f]+$`)

var (
	ing02StoreOwnedMu     sync.Mutex
	ing02StoreOwnedByName = map[string]string{}
)

func ing02StoreRegister(db, schema string) {
	ing02StoreOwnedMu.Lock()
	defer ing02StoreOwnedMu.Unlock()
	ing02StoreOwnedByName[db] = schema
}

var (
	ing02StoreBaselineOnce  sync.Once
	ing02StoreBaselineCount int64
	ing02StoreBaselineErr   error
)

func ing02StoreCaptureBaseline(raw string) {
	ing02StoreBaselineOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		admin, err := pgxpool.New(ctx, raw)
		if err != nil {
			ing02StoreBaselineErr = fmt.Errorf("connect for baseline: %w", err)
			return
		}
		defer admin.Close()
		var count int64
		if err := admin.QueryRow(ctx,
			"SELECT count(*) FROM pg_database WHERE datname LIKE 'verify\\_ing02\\_store\\_%' ESCAPE '\\'").
			Scan(&count); err != nil {
			ing02StoreBaselineErr = fmt.Errorf("count baseline: %w", err)
			return
		}
		ing02StoreBaselineCount = count
	})
}

type ing02StoreService struct {
	ctx    context.Context
	admin  *pgxpool.Pool
	iso    *pgxpool.Pool
	dsn    string
	schema string
	scrub  func(any) string
	fail   func(step string, err error)
}

func newIng02StoreService(t *testing.T) *ing02StoreService {
	t.Helper()
	raw := testPostgresURL(t)
	ing02StoreCaptureBaseline(raw)
	if ing02StoreBaselineErr != nil {
		t.Fatalf("capture pg_database baseline: %v", ing02StoreBaselineErr)
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

	dbName := fmt.Sprintf(ing02StoreFmt, time.Now().UnixNano(), randomHex(t))
	if !ing02StoreNamePattern.MatchString(dbName) {
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

	schema := fmt.Sprintf(ing02StoreFmt, time.Now().UnixNano(), randomHex(t))
	if !ing02StoreNamePattern.MatchString(schema) {
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

	ing02StoreRegister(dbName, schema)
	return &ing02StoreService{ctx: ctx, admin: admin, iso: iso, dsn: dsn, schema: schema, scrub: scrub, fail: fail}
}

func (s *ing02StoreService) migrate(t *testing.T) {
	t.Helper()
	if _, err := MigrateSchema(s.ctx, s.openClient(t, 4), s.schema); err != nil {
		s.fail("MigrateSchema", err)
	}
}

func (s *ing02StoreService) openClient(t *testing.T, maxConns int32) *Client {
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

func (s *ing02StoreService) openAppClient(t *testing.T, app string, maxConns int32) *Client {
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

func (s *ing02StoreService) newWriterOn(t *testing.T, src ConnSource) *EdgeWriter {
	t.Helper()
	writer, err := NewEdgeWriter(src, s.schema)
	if err != nil {
		s.fail("new edge writer", err)
	}
	return writer
}

func (s *ing02StoreService) newWriter(t *testing.T) *EdgeWriter {
	t.Helper()
	return s.newWriterOn(t, s.openClient(t, 4))
}

func (s *ing02StoreService) qualified(table string) string {
	return pgx.Identifier{s.schema, table}.Sanitize()
}

func (s *ing02StoreService) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := s.iso.Exec(s.ctx, sql, args...); err != nil {
		s.fail("fixture exec "+sql, err)
	}
}

func (s *ing02StoreService) mustInt64(t *testing.T, sql string, args ...any) int64 {
	t.Helper()
	var v int64
	if err := s.iso.QueryRow(s.ctx, sql, args...).Scan(&v); err != nil {
		s.fail("witness "+sql, err)
	}
	return v
}

func (s *ing02StoreService) seedResource(t *testing.T, project, generation, rtype, lid, authPath string) {
	t.Helper()
	s.exec(t, "INSERT INTO "+s.qualified("fhir_resource")+
		" (project, dataset_generation, resource_type, logical_id, auth_resource_path, payload)"+
		" VALUES ($1, $2, $3, $4, $5, $6::jsonb)",
		project, generation, rtype, lid, authPath,
		fmt.Sprintf(`{"resourceType": %q, "id": %q}`, rtype, lid))
}

func (s *ing02StoreService) countEdges(t *testing.T, project, generation string) int64 {
	t.Helper()
	return s.mustInt64(t, "SELECT count(*) FROM "+s.qualified("fhir_edge_occurrence")+
		" WHERE project = $1 AND dataset_generation = $2", project, generation)
}

func (s *ing02StoreService) witnessLedger(t *testing.T, batchID string) (digest string, edges int64, found bool) {
	t.Helper()
	err := s.iso.QueryRow(s.ctx,
		"SELECT digest, edge_count FROM "+s.qualified("batch_write_ledger")+" WHERE batch_id = $1", batchID).
		Scan(&digest, &edges)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, false
	}
	if err != nil {
		s.fail("witness ledger row", err)
	}
	return digest, edges, true
}

func (s *ing02StoreService) requireEdgeRow(t *testing.T, project, generation, authPath, label,
	srcType, srcID, tgtType, tgtID, sourcePath string, sourceOrdinal int64, wantKey *string,
) {
	t.Helper()
	var gotAuth string
	var keyPresent bool
	var key *string
	err := s.iso.QueryRow(s.ctx,
		"SELECT auth_resource_path, logical_edge_key IS NOT NULL, logical_edge_key FROM "+s.qualified("fhir_edge_occurrence")+
			" WHERE project = $1 AND dataset_generation = $2 AND source_resource_type = $3"+
			" AND source_logical_id = $4 AND label = $5 AND target_resource_type = $6"+
			" AND target_logical_id = $7 AND source_path = $8 AND source_ordinal = $9",
		project, generation, srcType, srcID, label, tgtType, tgtID, sourcePath, sourceOrdinal).
		Scan(&gotAuth, &keyPresent, &key)
	if err != nil {
		s.fail(fmt.Sprintf("witness occurrence %s/%s -%s-> %s/%s at %q#%d",
			srcType, srcID, label, tgtType, tgtID, sourcePath, sourceOrdinal), err)
	}
	if gotAuth != authPath {
		t.Fatalf("occurrence %s/%s -%s-> %s/%s at %q#%d auth = %q, want %q",
			srcType, srcID, label, tgtType, tgtID, sourcePath, sourceOrdinal, gotAuth, authPath)
	}
	if (wantKey != nil) != keyPresent {
		t.Fatalf("occurrence %s/%s -%s-> %s/%s at %q#%d key presence = %v, want %v",
			srcType, srcID, label, tgtType, tgtID, sourcePath, sourceOrdinal, keyPresent, wantKey != nil)
	}
	if wantKey != nil && *key != *wantKey {
		t.Fatalf("occurrence %s/%s -%s-> %s/%s at %q#%d key = %q, want %q",
			srcType, srcID, label, tgtType, tgtID, sourcePath, sourceOrdinal, *key, *wantKey)
	}
}

func (s *ing02StoreService) edgeFingerprint(t *testing.T, project, generation string) string {
	t.Helper()
	var fp string
	sql := "SELECT coalesce(md5(string_agg(entry, E'\n' ORDER BY edge_seq)),'EMPTY') FROM (" +
		"SELECT edge_seq, source_resource_type || '|' || source_logical_id || '|' || label || '|' ||" +
		" target_resource_type || '|' || target_logical_id || '|' || source_path || '|' ||" +
		" source_ordinal::text || '|' || auth_resource_path || '|' || coalesce(logical_edge_key, '<SQL-NULL>') AS entry" +
		" FROM " + s.qualified("fhir_edge_occurrence") + " WHERE project = $1 AND dataset_generation = $2) x"
	if err := s.iso.QueryRow(s.ctx, sql, project, generation).Scan(&fp); err != nil {
		s.fail("edge fingerprint", err)
	}
	return fp
}

func (s *ing02StoreService) poolBackendPID(t *testing.T, client *Client) int64 {
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

func (s *ing02StoreService) waitBackendWaiting(t *testing.T, app string) int64 {
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

func ing02Edge(label, srcType, srcID, tgtType, tgtID, sourcePath string, ordinal int64, key *string) EdgeOccurrenceRow {
	return EdgeOccurrenceRow{
		Project: ing02StoreProject, DatasetGeneration: ing02StoreGen, AuthResourcePath: ing02StoreAuth,
		Label: label, SourceResourceType: srcType, SourceLogicalID: srcID,
		TargetResourceType: tgtType, TargetLogicalID: tgtID,
		SourcePath: sourcePath, SourceOrdinal: ordinal, LogicalEdgeKey: key,
	}
}

func duplicateFocusOccurrences() []EdgeOccurrenceRow {
	return []EdgeOccurrenceRow{
		ing02Edge("focus_Observation", "Observation", "obs-1", "Observation", "focus-1", "focus", 0, strPtr("cc574af6-dup-key")),
		ing02Edge("focus_Observation", "Observation", "obs-1", "Observation", "focus-1", "focus", 1, strPtr("cc574af6-dup-key")),
		ing02Edge("focus_observation", "Observation", "focus-1", "Observation", "obs-1", "focus", 0, strPtr("82685ede-rev-key")),
		ing02Edge("subject_Patient", "Observation", "obs-1", "Patient", "patient-missing", "subject", 0, nil),
	}
}

func seedDuplicateFocusScope(t *testing.T, s *ing02StoreService, generation string) {
	t.Helper()
	s.seedResource(t, ing02StoreProject, generation, "Observation", "obs-1", ing02StoreAuth)
	s.seedResource(t, ing02StoreProject, generation, "Observation", "focus-1", ing02StoreAuth)
}

func TestPostgresEdgeWriterConstructorRejectsInvalidInputs(t *testing.T) {
	if _, err := NewEdgeWriter(nil, "ok_schema"); err == nil {
		t.Fatal("NewEdgeWriter(nil) accepted a nil connection source")
	}
	if _, err := NewEdgeWriter(stubConnSource{}, "Ok Schema"); err == nil {
		t.Fatal("NewEdgeWriter accepted an invalid schema name")
	} else if !errors.Is(err, ErrInvalidSchemaName) {
		t.Fatalf("NewEdgeWriter schema error = %v, want ErrInvalidSchemaName", err)
	}
}

type deadConnSource struct{}

func (deadConnSource) Acquire(ctx context.Context) (*pgxpool.Conn, error) {
	return nil, errors.New("deadConnSource must not be contacted")
}

func TestPostgresEdgeWriterRejectsInvalidBatchesBeforeAnyRoundTrip(t *testing.T) {
	writer, err := NewEdgeWriter(deadConnSource{}, "ok_schema")
	if err != nil {
		t.Fatal(err)
	}
	ok := ing02Edge("l", "Observation", "obs-1", "Patient", "p-1", "subject", 0, strPtr("k"))

	cases := []struct {
		name  string
		batch EdgeBatch
	}{
		{"blank id", EdgeBatch{ID: " ", Project: ing02StoreProject, DatasetGeneration: ing02StoreGen, Rows: []EdgeOccurrenceRow{ok}}},
		{"over-long id", EdgeBatch{ID: strings.Repeat("x", MaxBatchIDLength+1), Project: ing02StoreProject, Rows: []EdgeOccurrenceRow{ok}}},
		{"blank project", EdgeBatch{ID: "b/1", Project: "", Rows: []EdgeOccurrenceRow{ok}}},
		{"no rows", EdgeBatch{ID: "b/1", Project: ing02StoreProject, DatasetGeneration: ing02StoreGen}},
		{"no rows with dead pool", EdgeBatch{ID: "b/1", Project: ing02StoreProject, Rows: []EdgeOccurrenceRow{}}},
		{"over capacity", func() EdgeBatch {
			rows := make([]EdgeOccurrenceRow, 0, MaxBatchEdges+1)
			for i := 0; i <= MaxBatchEdges; i++ {
				rec := ok
				rec.SourceOrdinal = int64(i)
				rows = append(rows, rec)
			}
			return EdgeBatch{ID: "b/1", Project: ing02StoreProject, Rows: rows}
		}()},
		{"row scope mismatch", func() EdgeBatch {
			rec := ok
			rec.Project = "other-prj"
			return EdgeBatch{ID: "b/1", Project: ing02StoreProject, Rows: []EdgeOccurrenceRow{rec}}
		}()},
		{"caller-supplied edge seq", func() EdgeBatch {
			rec := ok
			rec.EdgeSeq = 9
			return EdgeBatch{ID: "b/1", Project: ing02StoreProject, Rows: []EdgeOccurrenceRow{rec}}
		}()},
		{"negative ordinal", func() EdgeBatch {
			rec := ok
			rec.SourceOrdinal = -1
			return EdgeBatch{ID: "b/1", Project: ing02StoreProject, Rows: []EdgeOccurrenceRow{rec}}
		}()},
		{"blank label", func() EdgeBatch {
			rec := ok
			rec.Label = " "
			return EdgeBatch{ID: "b/1", Project: ing02StoreProject, Rows: []EdgeOccurrenceRow{rec}}
		}()},
		{"blank target id", func() EdgeBatch {
			rec := ok
			rec.TargetLogicalID = ""
			return EdgeBatch{ID: "b/1", Project: ing02StoreProject, Rows: []EdgeOccurrenceRow{rec}}
		}()},
	}
	for _, tc := range cases {
		_, err := writer.WriteEdgeBatch(context.Background(), tc.batch)
		if !errors.Is(err, ErrInvalidBatch) {
			t.Errorf("%s: WriteEdgeBatch = %v, want ErrInvalidBatch", tc.name, err)
			continue
		}
		if strings.Contains(fmt.Sprint(err), "deadConnSource") {
			t.Errorf("%s: refusal contacted the connection source: %v", tc.name, err)
		}
	}

	if _, err := writer.WriteEdgeBatch(context.Background(), EdgeBatch{
		ID: "b/valid-looking", Project: ing02StoreProject, DatasetGeneration: ing02StoreGen,
		Rows: []EdgeOccurrenceRow{ok},
	}); !strings.Contains(fmt.Sprint(err), "deadConnSource") {
		t.Fatalf("valid batch against the dead pool = %v, want the dead-source contact error (proof the validators passed)", err)
	}
}

const (
	ing02DuplicateBatchID     = "ing02-store/duplicate/0"
	ing02DuplicateBatchDigest = "12fe2c7b603cd41b8f0a32f869738598b3f4aca5406ff5ab34cb1411693e38cb"
)

func TestPostgresEdgeWriterWritesDuplicateReferenceOccurrencesWithExactColumns(t *testing.T) {
	s := newIng02StoreService(t)
	s.migrate(t)
	seedDuplicateFocusScope(t, s, ing02StoreGen)
	writer := s.newWriter(t)

	res, err := writer.WriteEdgeBatch(s.ctx, EdgeBatch{
		ID: ing02DuplicateBatchID, Project: ing02StoreProject, DatasetGeneration: ing02StoreGen,
		Rows: duplicateFocusOccurrences(),
	})
	if err != nil {
		s.fail("WriteEdgeBatch", err)
	}
	if res.BatchID != ing02DuplicateBatchID || res.Committed != 4 || res.SkippedIdentical != 0 || res.Replay {
		t.Fatalf("result = %+v, want a fresh 4-row commit", res)
	}
	if res.Digest != ing02DuplicateBatchDigest {
		t.Fatalf("digest = %s, want the golden %s", res.Digest, ing02DuplicateBatchDigest)
	}

	s.requireEdgeRow(t, ing02StoreProject, ing02StoreGen, ing02StoreAuth,
		"focus_Observation", "Observation", "obs-1", "Observation", "focus-1", "focus", 0, strPtr("cc574af6-dup-key"))
	s.requireEdgeRow(t, ing02StoreProject, ing02StoreGen, ing02StoreAuth,
		"focus_Observation", "Observation", "obs-1", "Observation", "focus-1", "focus", 1, strPtr("cc574af6-dup-key"))
	s.requireEdgeRow(t, ing02StoreProject, ing02StoreGen, ing02StoreAuth,
		"focus_observation", "Observation", "focus-1", "Observation", "obs-1", "focus", 0, strPtr("82685ede-rev-key"))
	s.requireEdgeRow(t, ing02StoreProject, ing02StoreGen, ing02StoreAuth,
		"subject_Patient", "Observation", "obs-1", "Patient", "patient-missing", "subject", 0, nil)

	if n := s.countEdges(t, ing02StoreProject, ing02StoreGen); n != 4 {
		t.Fatalf("edge rows = %d, want 4", n)
	}
	distinct := s.mustInt64(t, "SELECT count(DISTINCT edge_seq) FROM "+s.qualified("fhir_edge_occurrence")+
		" WHERE project = $1 AND dataset_generation = $2", ing02StoreProject, ing02StoreGen)
	if distinct != 4 {
		t.Fatalf("distinct edge_seq = %d, want 4 — occurrences must be independently addressable", distinct)
	}

	digest, edges, found := s.witnessLedger(t, ing02DuplicateBatchID)
	if !found || digest != ing02DuplicateBatchDigest || edges != 4 {
		t.Fatalf("marker = (%s, %d, found %v), want the golden digest with 4 committed rows",
			digest, edges, found)
	}

	mutated := duplicateFocusOccurrences()
	mutated[1].SourceOrdinal = 7
	_, err = writer.WriteEdgeBatch(s.ctx, EdgeBatch{
		ID: ing02DuplicateBatchID, Project: ing02StoreProject, DatasetGeneration: ing02StoreGen,
		Rows: mutated,
	})
	var conflict *BatchIdentityConflictError
	if !errors.As(err, &conflict) || !errors.Is(err, ErrBatchIdentityConflict) {
		t.Fatalf("mutated replay under the golden identity = %v, want *BatchIdentityConflictError", err)
	}
	if conflict.LedgerDigest != ing02DuplicateBatchDigest || conflict.ProvidedDigest == ing02DuplicateBatchDigest {
		t.Fatalf("conflict digests = (%s, %s), want the golden ledger digest against a different provided digest",
			conflict.LedgerDigest, conflict.ProvidedDigest)
	}
	if n := s.countEdges(t, ing02StoreProject, ing02StoreGen); n != 4 {
		t.Fatalf("edge rows after refused mutated replay = %d, want the original 4", n)
	}
	if conflict.LedgerDigest == conflict.ProvidedDigest {
		t.Fatal("identity conflict claimed identical digests")
	}
}

func TestPostgresEdgeWriterEmptyReplayCollapseAndLedgerCountsCommitted(t *testing.T) {
	s := newIng02StoreService(t)
	s.migrate(t)
	seedDuplicateFocusScope(t, s, ing02StoreGen)
	writer := s.newWriter(t)

	rows := duplicateFocusOccurrences()
	first, err := writer.WriteEdgeBatch(s.ctx, EdgeBatch{
		ID: "ing02-store/collapse/first", Project: ing02StoreProject, DatasetGeneration: ing02StoreGen, Rows: rows,
	})
	if err != nil || first.Committed != 4 || first.Replay {
		s.fail("first commit", err)
	}
	before := s.edgeFingerprint(t, ing02StoreProject, ing02StoreGen)

	second, err := writer.WriteEdgeBatch(s.ctx, EdgeBatch{
		ID: "ing02-store/collapse/second", Project: ing02StoreProject, DatasetGeneration: ing02StoreGen, Rows: rows,
	})
	if err != nil {
		s.fail("collapse batch", err)
	}
	if second.Committed != 0 || second.SkippedIdentical != 4 || second.Replay {
		t.Fatalf("collapse result = %+v, want 0 committed / 4 skipped", second)
	}
	if _, edges, found := s.witnessLedger(t, "ing02-store/collapse/second"); !found || edges != 0 {
		t.Fatalf("collapse marker = edges %d found %v, want 0 committed in the ledger", edges, found)
	}
	if fp := s.edgeFingerprint(t, ing02StoreProject, ing02StoreGen); fp != before {
		t.Fatalf("scope drifted on identical re-offer: %s != %s", fp, before)
	}

	for attempt := 0; attempt < 3; attempt++ {
		replay, err := writer.WriteEdgeBatch(s.ctx, EdgeBatch{
			ID: "ing02-store/collapse/first", Project: ing02StoreProject, DatasetGeneration: ing02StoreGen, Rows: rows,
		})
		if err != nil {
			s.fail("replay", err)
		}
		if !replay.Replay || replay.Digest != first.Digest || replay.Committed != first.Committed ||
			replay.SkippedIdentical != first.SkippedIdentical || replay.BatchID != first.BatchID {
			t.Fatalf("replay %d = %+v, want byte-identical accounting of %+v", attempt, replay, first)
		}
	}

	s.exec(t, "UPDATE "+s.qualified("batch_write_ledger")+" SET digest = $2 WHERE batch_id = $1",
		"ing02-store/collapse/first", strings.Repeat("a", 64))
	_, err = writer.WriteEdgeBatch(s.ctx, EdgeBatch{
		ID: "ing02-store/collapse/first", Project: ing02StoreProject, DatasetGeneration: ing02StoreGen, Rows: rows,
	})
	var conflict *BatchIdentityConflictError
	if !errors.As(err, &conflict) || conflict.LedgerDigest != strings.Repeat("a", 64) {
		t.Fatalf("replay after digest tamper = %v, want *BatchIdentityConflictError against the tampered digest", err)
	}
	if fp := s.edgeFingerprint(t, ing02StoreProject, ing02StoreGen); fp != before {
		t.Fatalf("scope drifted on refused replay: %s != %s", fp, before)
	}
}

func TestPostgresEdgeWriterMissingTargetStaysUnresolvedTypedEndpoint(t *testing.T) {
	s := newIng02StoreService(t)
	s.migrate(t)
	seedDuplicateFocusScope(t, s, ing02StoreGen)
	s.seedResource(t, ing02StoreProject, ing02StoreGen, "Patient", "patient-known", ing02StoreAuth)

	writer := s.newWriter(t)
	res, err := writer.WriteEdgeBatch(s.ctx, EdgeBatch{
		ID: "ing02-store/unresolved/0", Project: ing02StoreProject, DatasetGeneration: ing02StoreGen,
		Rows: []EdgeOccurrenceRow{
			ing02Edge("subject_Patient", "Observation", "obs-1", "Patient", "patient-missing", "subject", 0, strPtr("k-miss")),
			ing02Edge("subject_Patient", "Observation", "obs-1", "Patient", "patient-known", "subject", 1, strPtr("k-known")),
		},
	})
	if err != nil || res.Committed != 2 {
		s.fail("unresolved-target batch", err)
	}

	repo, err := NewEdgeRepository(s.openClient(t, 2), s.schema)
	if err != nil {
		s.fail("new edge repository", err)
	}
	unrestricted := true
	rows, hasMore, err := repo.ReadEdgesFromSource(s.ctx, EdgeScope{
		Project: ing02StoreProject, DatasetGeneration: ing02StoreGen,
		AuthResourcePathsUnrestricted: &unrestricted,
	}, "Observation", "obs-1", 0, 10)
	if err != nil {
		s.fail("traverse from source", err)
	}
	if hasMore {
		t.Fatal("hasMore on a two-row scope")
	}
	if len(rows) != 2 {
		t.Fatalf("traversed %d rows, want 2", len(rows))
	}
	seen := map[string]bool{}
	for _, row := range rows {
		seen[row.TargetLogicalID] = true
		switch row.TargetLogicalID {
		case "patient-missing":
			if row.TargetResolved {
				t.Fatal("missing target reported resolved — resolution must be a read-time fact")
			}
		case "patient-known":
			if !row.TargetResolved {
				t.Fatal("stored target reported unresolved — same-scope fhir_resource must resolve it")
			}
		}
	}
	if !seen["patient-missing"] || !seen["patient-known"] {
		t.Fatalf("traversal lost an endpoint: %+v", seen)
	}
}

func TestPostgresEdgeWriterMissingSourceRollsBackWholeBatch(t *testing.T) {
	s := newIng02StoreService(t)
	s.migrate(t)
	writer := s.newWriter(t)

	_, err := writer.WriteEdgeBatch(s.ctx, EdgeBatch{
		ID: "ing02-store/missing-source/0", Project: ing02StoreProject, DatasetGeneration: ing02StoreGen,
		Rows: duplicateFocusOccurrences(),
	})
	var missing *EdgeSourceMissingError
	if !errors.As(err, &missing) || !errors.Is(err, ErrEdgeSourceMissing) {
		s.fail("missing-source batch", fmt.Errorf("err = %v, want *EdgeSourceMissingError", err))
	}
	if missing.SourceResourceType != "Observation" || missing.SourceLogicalID != "obs-1" ||
		missing.Project != ing02StoreProject || missing.DatasetGeneration != ing02StoreGen {
		t.Fatalf("typed missing-source = %+v, want Observation/obs-1 under %s/%s",
			missing, ing02StoreProject, ing02StoreGen)
	}
	if n := s.countEdges(t, ing02StoreProject, ing02StoreGen); n != 0 {
		t.Fatalf("edge rows after refused batch = %d, want 0 — persisted counts must not increment", n)
	}
	if _, _, found := s.witnessLedger(t, "ing02-store/missing-source/0"); found {
		t.Fatal("refused batch left a ledger marker")
	}

	seedDuplicateFocusScope(t, s, ing02StoreGen)
	res, err := writer.WriteEdgeBatch(s.ctx, EdgeBatch{
		ID: "ing02-store/missing-source/control", Project: ing02StoreProject, DatasetGeneration: ing02StoreGen,
		Rows: duplicateFocusOccurrences(),
	})
	if err != nil || res.Committed != 4 || res.Replay {
		s.fail("control batch after seeding", err)
	}
}

func TestPostgresEdgeWriterDuplicateOccurrenceDifferentContentPreservesPrior(t *testing.T) {
	s := newIng02StoreService(t)
	s.migrate(t)
	seedDuplicateFocusScope(t, s, ing02StoreGen)
	writer := s.newWriter(t)

	if _, err := writer.WriteEdgeBatch(s.ctx, EdgeBatch{
		ID: "ing02-store/conflict/first", Project: ing02StoreProject, DatasetGeneration: ing02StoreGen,
		Rows: duplicateFocusOccurrences(),
	}); err != nil {
		s.fail("first commit", err)
	}
	before := s.edgeFingerprint(t, ing02StoreProject, ing02StoreGen)

	conflicting := duplicateFocusOccurrences()
	conflicting[0].LogicalEdgeKey = strPtr("tampered-key")
	conflicting = append(conflicting,
		ing02Edge("component_focus", "Observation", "obs-1", "Observation", "component-1", "component", 0, strPtr("k-new")))
	_, err := writer.WriteEdgeBatch(s.ctx, EdgeBatch{
		ID: "ing02-store/conflict/second", Project: ing02StoreProject, DatasetGeneration: ing02StoreGen,
		Rows: conflicting,
	})
	var conflict *BatchEdgeConflictError
	if !errors.As(err, &conflict) || !errors.Is(err, ErrBatchRowConflict) {
		s.fail("content-drift batch", fmt.Errorf("err = %v, want *BatchEdgeConflictError", err))
	}
	if conflict.InBatch || conflict.SourceLogicalID != "obs-1" || conflict.SourcePath != "focus" ||
		conflict.SourceOrdinal != 0 || conflict.Label != "focus_Observation" {
		t.Fatalf("conflict = %+v, want stored-row occurrence obs-1 focus#0", conflict)
	}
	if fp := s.edgeFingerprint(t, ing02StoreProject, ing02StoreGen); fp != before {
		t.Fatalf("stored scope drifted after the refused batch: %s != %s", fp, before)
	}
	if n := s.mustInt64(t, "SELECT count(*) FROM "+s.qualified("fhir_edge_occurrence")+
		" WHERE target_logical_id = 'component-1'"); n != 0 {
		t.Fatalf("refused batch leaked its fresh row (%d), want whole-batch rollback", n)
	}
	if _, _, found := s.witnessLedger(t, "ing02-store/conflict/second"); found {
		t.Fatal("refused batch left a ledger marker")
	}
	s.requireEdgeRow(t, ing02StoreProject, ing02StoreGen, ing02StoreAuth,
		"focus_Observation", "Observation", "obs-1", "Observation", "focus-1", "focus", 0, strPtr("cc574af6-dup-key"))
}

func TestPostgresEdgeWriterInjectedFailureAtEverySeamLeavesNoRowsOrMarker(t *testing.T) {
	s := newIng02StoreService(t)
	s.migrate(t)
	seedDuplicateFocusScope(t, s, ing02StoreGen)

	seams := []string{stepBegin, stepReadLedger, stepClaimLedger,
		stepStageEdges, stepCheckEdges, stepPublishEdges, stepFinalizeLedger, stepCommit}
	injected := errors.New("injected crash")
	for _, seam := range seams {
		writer := s.newWriter(t)
		fired := false
		writer.batch.failHook = func(step string) error {
			if step == seam {
				fired = true
				return injected
			}
			return nil
		}
		_, err := writer.WriteEdgeBatch(s.ctx, EdgeBatch{
			ID: "ing02-store/crash/" + seam, Project: ing02StoreProject, DatasetGeneration: ing02StoreGen,
			Rows: duplicateFocusOccurrences(),
		})
		var writeErr *BatchWriteError
		if !fired {
			t.Fatalf("seam %s never fired the injected failure", seam)
		}
		if !errors.As(err, &writeErr) || !errors.Is(err, ErrBatchWrite) || !errors.Is(err, injected) {
			t.Fatalf("failure at seam %s = %v, want *BatchWriteError chaining the injected error", seam, err)
		}
		if n := s.countEdges(t, ing02StoreProject, ing02StoreGen); n != 0 {
			t.Fatalf("seam %s: edge rows after injected crash = %d, want 0", seam, n)
		}
		if _, _, found := s.witnessLedger(t, "ing02-store/crash/"+seam); found {
			t.Fatalf("seam %s: injected crash left a ledger marker", seam)
		}
	}

	writer := s.newWriter(t)
	res, err := writer.WriteEdgeBatch(s.ctx, EdgeBatch{
		ID: "ing02-store/crash/" + stepCommit, Project: ing02StoreProject, DatasetGeneration: ing02StoreGen,
		Rows: duplicateFocusOccurrences(),
	})
	if err != nil {
		s.fail("control after crash sweep", err)
	}
	if res.Committed != 4 || res.Replay {
		t.Fatalf("control = %+v, want a fresh 4-row commit on the burned identity", res)
	}
}

func TestPostgresEdgeWriterConcurrentIdenticalBatchCommitsOnceTwoConnections(t *testing.T) {
	const rounds = 12
	s := newIng02StoreService(t)
	s.migrate(t)

	clientA := s.openAppClient(t, "ing02-store-race-a", 1)
	clientB := s.openAppClient(t, "ing02-store-race-b", 1)
	pidA := s.poolBackendPID(t, clientA)
	pidB := s.poolBackendPID(t, clientB)
	if pidA == 0 || pidB == 0 || pidA == pidB {
		t.Fatalf("race pools share backend %d/%d, want distinct real connections", pidA, pidB)
	}
	writerA := s.newWriterOn(t, clientA)
	writerB := s.newWriterOn(t, clientB)

	for round := 0; round < rounds; round++ {
		generation := fmt.Sprintf("gen-race-%d", round)
		seedDuplicateFocusScope(t, s, generation)
		rows := duplicateFocusOccurrences()
		for i := range rows {
			rows[i].DatasetGeneration = generation
		}
		batchID := fmt.Sprintf("ing02-store/race/%d", round)

		start := make(chan struct{})
		type outcome struct {
			res EdgeBatchResult
			err error
		}
		ra := make(chan outcome, 1)
		rb := make(chan outcome, 1)
		race := func(w *EdgeWriter, out chan outcome) {
			<-start
			res, err := w.WriteEdgeBatch(context.Background(), EdgeBatch{
				ID: batchID, Project: ing02StoreProject, DatasetGeneration: generation, Rows: rows,
			})
			out <- outcome{res, err}
		}
		go race(writerA, ra)
		go race(writerB, rb)
		close(start)
		o1, o2 := <-ra, <-rb
		if o1.err != nil {
			s.fail(fmt.Sprintf("round %d connection A", round), o1.err)
		}
		if o2.err != nil {
			s.fail(fmt.Sprintf("round %d connection B", round), o2.err)
		}
		if o1.res.BatchID != o2.res.BatchID || o1.res.Digest != o2.res.Digest ||
			o1.res.Committed != o2.res.Committed || o1.res.SkippedIdentical != o2.res.SkippedIdentical {
			t.Fatalf("round %d results diverged: %+v vs %+v", round, o1.res, o2.res)
		}
		if !o1.res.Replay && !o2.res.Replay || o1.res.Replay && o2.res.Replay {
			t.Fatalf("round %d replays = (%v, %v), want exactly one fresh commit", round, o1.res.Replay, o2.res.Replay)
		}
		if o1.res.Committed != 4 || o1.res.Digest == "" {
			t.Fatalf("round %d shared accounting = %+v, want 4 committed under a real digest", round, o1.res)
		}
		if n := s.countEdges(t, ing02StoreProject, generation); n != 4 {
			t.Fatalf("round %d rows = %d, want exactly 4 (committed once)", round, n)
		}
		digest, edges, found := s.witnessLedger(t, batchID)
		if !found || digest != o1.res.Digest || edges != 4 {
			t.Fatalf("round %d marker = (%s, %d, found %v), want the shared digest with 4 rows",
				round, digest, edges, found)
		}
	}
}

func TestPostgresEdgeWriterConcurrentOccurrenceContentConflictOneWinsTwoConnections(t *testing.T) {
	const rounds = 10
	s := newIng02StoreService(t)
	s.migrate(t)

	clientA := s.openAppClient(t, "ing02-store-conf-a", 1)
	clientB := s.openAppClient(t, "ing02-store-conf-b", 1)
	pidA := s.poolBackendPID(t, clientA)
	pidB := s.poolBackendPID(t, clientB)
	if pidA == 0 || pidB == 0 || pidA == pidB {
		t.Fatalf("race pools share backend %d/%d, want distinct real connections", pidA, pidB)
	}
	writerA := s.newWriterOn(t, clientA)
	writerB := s.newWriterOn(t, clientB)

	for round := 0; round < rounds; round++ {
		generation := fmt.Sprintf("gen-conf-%d", round)
		seedDuplicateFocusScope(t, s, generation)
		mk := func(key string) []EdgeOccurrenceRow {
			rows := duplicateFocusOccurrences()
			for i := range rows {
				rows[i].DatasetGeneration = generation
			}
			rows[0].LogicalEdgeKey = strPtr(key)
			rows[1].LogicalEdgeKey = strPtr(key)
			rows[2].LogicalEdgeKey = strPtr(key)
			return rows
		}

		start := make(chan struct{})
		type outcome struct {
			res EdgeBatchResult
			err error
		}
		ra := make(chan outcome, 1)
		rb := make(chan outcome, 1)
		go func() {
			<-start
			res, err := writerA.WriteEdgeBatch(context.Background(), EdgeBatch{
				ID: fmt.Sprintf("ing02-store/conflict-a/%d", round), Project: ing02StoreProject,
				DatasetGeneration: generation, Rows: mk("winner-key-A"),
			})
			ra <- outcome{res, err}
		}()
		go func() {
			<-start
			res, err := writerB.WriteEdgeBatch(context.Background(), EdgeBatch{
				ID: fmt.Sprintf("ing02-store/conflict-b/%d", round), Project: ing02StoreProject,
				DatasetGeneration: generation, Rows: mk("winner-key-B"),
			})
			rb <- outcome{res, err}
		}()
		close(start)
		o1, o2 := <-ra, <-rb

		var loser outcome
		var winner outcome
		var loserKey string
		var winnerKey string
		switch {
		case o1.err == nil && o2.err != nil:
			winner, loser = o1, o2
			winnerKey, loserKey = "winner-key-A", "winner-key-B"
		case o2.err == nil && o1.err != nil:
			winner, loser = o2, o1
			winnerKey, loserKey = "winner-key-B", "winner-key-A"
		default:
			s.fail(fmt.Sprintf("round %d", round),
				fmt.Errorf("expected exactly one commit, got A=%v B=%v", o1.err, o2.err))
		}
		var conflict *BatchEdgeConflictError
		var accounting *BatchAccountingError
		typedRefusal := (errors.As(loser.err, &conflict) && errors.Is(loser.err, ErrBatchRowConflict) && !conflict.InBatch) ||
			(errors.As(loser.err, &accounting) && errors.Is(loser.err, ErrBatchAccounting) && accounting.Side == "edges")
		if !typedRefusal {
			t.Fatalf("round %d loser err = %v, want stored-row *BatchEdgeConflictError or edge-side *BatchAccountingError",
				round, loser.err)
		}
		if winner.res.Committed != 4 || winner.res.Replay {
			t.Fatalf("round %d winner = %+v, want a fresh 4-row commit", round, winner.res)
		}
		_, err := writerA.WriteEdgeBatch(context.Background(), EdgeBatch{
			ID: fmt.Sprintf("ing02-store/conflict-retry/%d", round), Project: ing02StoreProject,
			DatasetGeneration: generation, Rows: mk(loserKey),
		})
		var retryConflict *BatchEdgeConflictError
		if !errors.As(err, &retryConflict) || !errors.Is(err, ErrBatchRowConflict) || retryConflict.InBatch {
			t.Fatalf("round %d loser retry = %v, want stored-row *BatchEdgeConflictError", round, err)
		}
		rows := mk(winnerKey)
		for _, row := range rows {
			if row.LogicalEdgeKey != nil {
				s.requireEdgeRow(t, ing02StoreProject, generation, ing02StoreAuth,
					row.Label, row.SourceResourceType, row.SourceLogicalID,
					row.TargetResourceType, row.TargetLogicalID, row.SourcePath, row.SourceOrdinal,
					row.LogicalEdgeKey)
			}
		}
		if n := s.countEdges(t, ing02StoreProject, generation); n != 4 {
			t.Fatalf("round %d rows = %d, want exactly 4 — loser rollback plus refused retry store nothing", round, n)
		}
	}
}

func TestPostgresEdgeWriterCountsChangeOnlyAtCommitTwoConnections(t *testing.T) {
	s := newIng02StoreService(t)
	s.migrate(t)

	writerClient := s.openAppClient(t, "ing02-store-commit-w", 1)
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
		seedDuplicateFocusScope(t, s, generation)
		rows := duplicateFocusOccurrences()
		for i := range rows {
			rows[i].DatasetGeneration = generation
		}
		batchID := fmt.Sprintf("ing02-store/visibility/%d", round)

		if _, err := obsConn.Exec(s.ctx, "BEGIN"); err != nil {
			s.fail("observer begin", err)
		}
		if _, err := obsConn.Exec(s.ctx,
			"LOCK TABLE "+s.qualified("fhir_edge_occurrence")+" IN ACCESS EXCLUSIVE MODE"); err != nil {
			s.fail("observer lock", err)
		}

		type outcome struct {
			res EdgeBatchResult
			err error
		}
		done := make(chan outcome, 1)
		go func() {
			res, err := writer.WriteEdgeBatch(context.Background(), EdgeBatch{
				ID: batchID, Project: ing02StoreProject, DatasetGeneration: generation, Rows: rows,
			})
			done <- outcome{res, err}
		}()

		blockedPID := s.waitBackendWaiting(t, "ing02-store-commit-w")
		if blockedPID != writerPID || blockedPID == observerPID {
			t.Fatalf("round %d blocked backend pid = %d, want the writer backend %d (observer is %d)",
				round, blockedPID, writerPID, observerPID)
		}
		var midRows int64
		if err := obsConn.QueryRow(s.ctx, "SELECT count(*) FROM "+s.qualified("fhir_edge_occurrence")+
			" WHERE project = $1 AND dataset_generation = $2", ing02StoreProject, generation).
			Scan(&midRows); err != nil {
			s.fail("mid-transaction row witness", err)
		}
		if midRows != 0 {
			t.Fatalf("round %d rows visible mid-transaction = %d, want 0 — counts may only change at commit", round, midRows)
		}
		if _, _, found := s.witnessLedger(t, batchID); found {
			t.Fatalf("round %d ledger marker visible mid-transaction, want none", round)
		}

		if _, err := obsConn.Exec(s.ctx, "COMMIT"); err != nil {
			s.fail("observer commit", err)
		}
		var o outcome
		select {
		case o = <-done:
		case <-time.After(60 * time.Second):
			s.fail("waiting for batch commit", fmt.Errorf("round %d batch never completed after the lock released", round))
		}
		if o.err != nil {
			s.fail(fmt.Sprintf("round %d WriteEdgeBatch", round), o.err)
		}
		if o.res.Committed != 4 || o.res.Replay {
			t.Fatalf("round %d result = %+v, want a fresh 4-row commit", round, o.res)
		}
		if n := s.countEdges(t, ing02StoreProject, generation); n != o.res.Committed {
			t.Fatalf("round %d rows after commit = %d, want the reported %d", round, n, o.res.Committed)
		}
		if _, edges, found := s.witnessLedger(t, batchID); !found || edges != 4 {
			t.Fatalf("round %d marker missing or miscounted after commit (found %v, edges %d)", round, found, edges)
		}
	}
}

func TestZZZeroLeftoverEdgeWriterFixtures(t *testing.T) {
	raw := testPostgresURL(t)
	ing02StoreOwnedMu.Lock()
	owned := make(map[string]string, len(ing02StoreOwnedByName))
	for db, schema := range ing02StoreOwnedByName {
		owned[db] = schema
	}
	ing02StoreOwnedMu.Unlock()
	if len(owned) == 0 {
		t.Skip("this process created no verify_ing02_store_* fixtures")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect to test service for audit: %v", err)
	}
	defer admin.Close()

	for db, schema := range owned {
		if !strings.HasPrefix(db, ing02StorePrefix) {
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
		"SELECT count(*) FROM pg_database WHERE datname LIKE 'verify\\_ing02\\_store\\_%' ESCAPE '\\'").
		Scan(&visible); err != nil {
		t.Fatalf("audit verify_ing02_store_* database count: %v", err)
	}
	if visible != ing02StoreBaselineCount {
		t.Errorf("verify_ing02_store_* databases = %d, want baseline %d", visible, ing02StoreBaselineCount)
	}
}
