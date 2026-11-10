package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/source"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Fixture: one isolated database plus one owned schema per test, both named
// pgstore05_it_* and dropped by that test's cleanups. Mirrors the pg-store-04
// fixture with this ticket's namespace.
// ---------------------------------------------------------------------------

var pgstore05EdgeNamePattern = regexp.MustCompile(`^pgstore05_it_[0-9]+_[0-9a-f]+$`)

type edgeService struct {
	ctx    context.Context
	admin  *pgxpool.Pool // service database
	iso    *pgxpool.Pool // isolated database
	dsn    string        // isolated database DSN (credentials scrubbed on failure)
	schema string
	scrub  func(any) string
	fail   func(step string, err error)
}

func newEdgeService(t *testing.T) *edgeService {
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

	dbName := fmt.Sprintf("pgstore05_it_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !pgstore05EdgeNamePattern.MatchString(dbName) {
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

	schema := fmt.Sprintf("pgstore05_it_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !pgstore05EdgeNamePattern.MatchString(schema) {
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

	return &edgeService{ctx: ctx, admin: admin, iso: iso, dsn: dsn, schema: schema, scrub: scrub, fail: fail}
}

// openClient opens a readiness-checked pg-store-01 pool against the isolated
// database and closes it at test end.
func (s *edgeService) openClient(t *testing.T) *Client {
	t.Helper()
	client, err := Open(s.ctx, Config{
		DSN:            s.dsn,
		MaxConns:       4,
		MinConns:       0,
		AcquireTimeout: 10 * time.Second,
	})
	if err != nil {
		s.fail("open client pool", err)
	}
	t.Cleanup(client.Close)
	return client
}

// newRepositories binds an EdgeRepository and a ResourceRepository (the
// source-of-truth table the edge store precondition-checks and resolves
// against) to the owned schema over their own readiness-checked pools.
func (s *edgeService) newRepositories(t *testing.T) (*EdgeRepository, *ResourceRepository) {
	t.Helper()
	edgeRepo, err := NewEdgeRepository(s.openClient(t), s.schema)
	if err != nil {
		s.fail("new edge repository", err)
	}
	resourceRepo, err := NewResourceRepository(s.openClient(t), s.schema)
	if err != nil {
		s.fail("new resource repository", err)
	}
	return edgeRepo, resourceRepo
}

// mustMigrateEmbedded applies the full embedded history (ledger bootstrap +
// 000002_fhir_resource + 000004_edge_occurrence) to the owned schema.
// 000003 is reserved for the generation-manifest ticket and is not this
// suite's concern.
func (s *edgeService) mustMigrateEmbedded(t *testing.T) MigrateResult {
	t.Helper()
	result, err := MigrateSchema(s.ctx, s.openClient(t), s.schema)
	if err != nil {
		s.fail("MigrateSchema", err)
	}
	return result
}

// readLedger witnesses the migration ledger through the test's own pool.
func (s *edgeService) readLedger(t *testing.T) []LedgerRecord {
	t.Helper()
	rows, err := s.iso.Query(s.ctx,
		`SELECT version, name, checksum, applied_at FROM "`+s.schema+`".`+LedgerTable+` ORDER BY version`)
	if err != nil {
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

// mustSeedResource inserts one resource and returns its storage identity.
func (s *edgeService) mustSeedResource(t *testing.T, repo *ResourceRepository,
	project, generation, resourceType, logicalID, authPath string) int64 {
	t.Helper()
	seq, err := repo.InsertResource(s.ctx, source.Resource{
		Project: project, DatasetGeneration: generation, ResourceType: resourceType,
		LogicalID: logicalID, AuthResourcePath: authPath,
		Payload: []byte(`{"resourceType":"` + resourceType + `","id":"` + logicalID + `"}`),
	})
	if err != nil {
		s.fail(fmt.Sprintf("seed resource %s/%s/%s/%s", project, generation, resourceType, logicalID), err)
	}
	return seq
}

// mustInsertEdge inserts one occurrence and returns its server-assigned
// storage identity.
func (s *edgeService) mustInsertEdge(t *testing.T, repo *EdgeRepository, rec EdgeOccurrenceRow) int64 {
	t.Helper()
	seq, err := repo.InsertEdge(s.ctx, rec)
	if err != nil {
		s.fail(fmt.Sprintf("insert edge %s/%s/%s/%s -%s-> %s/%s at path %q ordinal %d",
			rec.Project, rec.DatasetGeneration, rec.SourceResourceType, rec.SourceLogicalID,
			rec.Label, rec.TargetResourceType, rec.TargetLogicalID, rec.SourcePath, rec.SourceOrdinal), err)
	}
	if seq < 1 {
		t.Fatalf("insert returned storage identity %d, want >= 1", seq)
	}
	return seq
}

func (s *edgeService) mustReadFromSource(t *testing.T, repo *EdgeRepository,
	scope EdgeScope, sourceType, sourceID string, afterSeq int64, limit int) ([]EdgeOccurrenceRow, bool) {
	t.Helper()
	rows, hasMore, err := repo.ReadEdgesFromSource(s.ctx, scope, sourceType, sourceID, afterSeq, limit)
	if err != nil {
		s.fail(fmt.Sprintf("source traversal %s/%s/%s/%s after %d",
			scope.Project, scope.DatasetGeneration, sourceType, sourceID, afterSeq), err)
	}
	return rows, hasMore
}

func (s *edgeService) mustReadToTarget(t *testing.T, repo *EdgeRepository,
	scope EdgeScope, targetType, targetID string, afterSeq int64, limit int) ([]EdgeOccurrenceRow, bool) {
	t.Helper()
	rows, hasMore, err := repo.ReadEdgesToTarget(s.ctx, scope, targetType, targetID, afterSeq, limit)
	if err != nil {
		s.fail(fmt.Sprintf("target traversal %s/%s/%s/%s after %d",
			scope.Project, scope.DatasetGeneration, targetType, targetID, afterSeq), err)
	}
	return rows, hasMore
}

// countEdges witnesses the raw row count for one full occurrence identity
// through direct SQL — never through the repository under test.
func (s *edgeService) countEdges(t *testing.T, rec EdgeOccurrenceRow) int64 {
	t.Helper()
	var count int64
	if err := s.iso.QueryRow(s.ctx,
		`SELECT count(*) FROM "`+s.schema+`".fhir_edge_occurrence
		  WHERE project = $1 AND dataset_generation = $2 AND source_resource_type = $3
		    AND source_logical_id = $4 AND label = $5 AND target_resource_type = $6
		    AND target_logical_id = $7 AND source_path = $8 AND source_ordinal = $9`,
		rec.Project, rec.DatasetGeneration, rec.SourceResourceType, rec.SourceLogicalID,
		rec.Label, rec.TargetResourceType, rec.TargetLogicalID, rec.SourcePath, rec.SourceOrdinal,
	).Scan(&count); err != nil {
		s.fail("witness occurrence count", err)
	}
	return count
}

// countResources witnesses the raw fhir_resource row count for one natural
// identity through direct SQL.
func (s *edgeService) countResources(t *testing.T, project, generation, resourceType, logicalID string) int64 {
	t.Helper()
	var count int64
	if err := s.iso.QueryRow(s.ctx,
		`SELECT count(*) FROM "`+s.schema+`".fhir_resource
		  WHERE project = $1 AND dataset_generation = $2 AND resource_type = $3 AND logical_id = $4`,
		project, generation, resourceType, logicalID).Scan(&count); err != nil {
		s.fail("witness resource count", err)
	}
	return count
}

// edgeRowWitness is the literal full-row shape read back through direct SQL.
type edgeRowWitness struct {
	EdgeSeq            int64
	Project            string
	DatasetGeneration  string
	AuthResourcePath   string
	Label              string
	SourceResourceType string
	SourceLogicalID    string
	TargetResourceType string
	TargetLogicalID    string
	SourcePath         string
	SourceOrdinal      int64
	LogicalEdgeKey     *string
	RecordedAtPresent  bool
}

// witnessEdgeRows reads every row for one (project, dataset_generation,
// source, target, label) occurrence group straight from the table, ordered
// by storage identity, for literal expected-row comparison.
func (s *edgeService) witnessEdgeRows(t *testing.T,
	project, generation, sourceType, sourceID, label, targetType, targetID string) []edgeRowWitness {
	t.Helper()
	rows, err := s.iso.Query(s.ctx,
		`SELECT edge_seq, project, dataset_generation, auth_resource_path, label,
		        source_resource_type, source_logical_id, target_resource_type, target_logical_id,
		        source_path, source_ordinal, logical_edge_key, recorded_at IS NOT NULL
		  FROM "`+s.schema+`".fhir_edge_occurrence
		 WHERE project = $1 AND dataset_generation = $2
		   AND source_resource_type = $3 AND source_logical_id = $4
		   AND label = $5 AND target_resource_type = $6 AND target_logical_id = $7
		 ORDER BY edge_seq`,
		project, generation, sourceType, sourceID, label, targetType, targetID)
	if err != nil {
		s.fail("witness edge rows", err)
	}
	defer rows.Close()
	var out []edgeRowWitness
	for rows.Next() {
		var w edgeRowWitness
		if err := rows.Scan(&w.EdgeSeq, &w.Project, &w.DatasetGeneration, &w.AuthResourcePath,
			&w.Label, &w.SourceResourceType, &w.SourceLogicalID, &w.TargetResourceType,
			&w.TargetLogicalID, &w.SourcePath, &w.SourceOrdinal, &w.LogicalEdgeKey, &w.RecordedAtPresent); err != nil {
			s.fail("witness edge rows scan", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		s.fail("witness edge rows err", err)
	}
	return out
}

// ---------------------------------------------------------------------------
// Scope helpers (edge scopes; resource scopes reuse resource_test.go)
// ---------------------------------------------------------------------------

func edgeRestrictedScope(project, generation string, paths ...string) EdgeScope {
	restricted := false
	return EdgeScope{
		Project:                       project,
		DatasetGeneration:             generation,
		AuthResourcePathsUnrestricted: &restricted,
		AuthResourcePaths:             paths,
	}
}

func edgeUnrestrictedScope(project, generation string) EdgeScope {
	unrestricted := true
	return EdgeScope{
		Project:                       project,
		DatasetGeneration:             generation,
		AuthResourcePathsUnrestricted: &unrestricted,
	}
}

// edgeRow builds an occurrence with the common identity fields filled in.
func edgeRow(project, generation, authPath, label, srcType, srcID, tgtType, tgtID, path string, ordinal int64) EdgeOccurrenceRow {
	return EdgeOccurrenceRow{
		Project: project, DatasetGeneration: generation, AuthResourcePath: authPath,
		Label: label, SourceResourceType: srcType, SourceLogicalID: srcID,
		TargetResourceType: tgtType, TargetLogicalID: tgtID,
		SourcePath: path, SourceOrdinal: ordinal,
	}
}

// ---------------------------------------------------------------------------
// Validation unit tests (no live server: everything fails before Acquire).
// ---------------------------------------------------------------------------

func TestEdgeValidationRejectsInvalidInserts(t *testing.T) {
	repo, err := NewEdgeRepository(stubConnSource{}, "pgstore05_stub")
	if err != nil {
		t.Fatalf("NewEdgeRepository: %v", err)
	}
	base := edgeRow("proj", "gen", "practitioner/pr-1", "participant",
		"Encounter", "enc-1", "Practitioner", "pr-9", "participant[0]", 0)
	for _, tc := range []struct {
		name   string
		mutate func(*EdgeOccurrenceRow)
	}{
		{"caller-supplied storage identity", func(r *EdgeOccurrenceRow) { r.EdgeSeq = 7 }},
		{"blank project", func(r *EdgeOccurrenceRow) { r.Project = "  " }},
		{"blank label", func(r *EdgeOccurrenceRow) { r.Label = "\t" }},
		{"blank source type", func(r *EdgeOccurrenceRow) { r.SourceResourceType = "" }},
		{"blank source id", func(r *EdgeOccurrenceRow) { r.SourceLogicalID = " " }},
		{"blank target type", func(r *EdgeOccurrenceRow) { r.TargetResourceType = "" }},
		{"blank target id", func(r *EdgeOccurrenceRow) { r.TargetLogicalID = "\n" }},
		{"negative ordinal", func(r *EdgeOccurrenceRow) { r.SourceOrdinal = -1 }},
	} {
		rec := base
		tc.mutate(&rec)
		_, err := repo.InsertEdge(context.Background(), rec)
		if !errors.Is(err, ErrInvalidEdge) {
			t.Errorf("%s: InsertEdge = %v, want ErrInvalidEdge", tc.name, err)
		}
	}
}

func TestEdgeScopeAndTraversalValidation(t *testing.T) {
	repo, err := NewEdgeRepository(stubConnSource{}, "pgstore05_stub")
	if err != nil {
		t.Fatalf("NewEdgeRepository: %v", err)
	}
	ctx := context.Background()

	if _, err := NewEdgeRepository(nil, "pgstore05_stub"); err == nil {
		t.Error("NewEdgeRepository with nil source succeeded, want error")
	}
	if _, err := NewEdgeRepository(stubConnSource{}, "Bad-Schema"); !errors.Is(err, ErrInvalidSchemaName) {
		t.Errorf("NewEdgeRepository with invalid schema = %v, want ErrInvalidSchemaName", err)
	}

	// nil AuthResourcePathsUnrestricted must never read as unrestricted.
	scope := EdgeScope{Project: "proj", AuthResourcePaths: []string{"a"}}
	if _, _, err := repo.ReadEdgesFromSource(ctx, scope, "Encounter", "enc-1", 0, 10); !errors.Is(err, ErrInvalidEdgeScope) {
		t.Errorf("source traversal with nil unrestricted flag = %v, want ErrInvalidEdgeScope", err)
	}
	if _, _, err := repo.ReadEdgesToTarget(ctx, scope, "Practitioner", "pr-9", 0, 10); !errors.Is(err, ErrInvalidEdgeScope) {
		t.Errorf("target traversal with nil unrestricted flag = %v, want ErrInvalidEdgeScope", err)
	}

	// Unrestricted mode must not carry paths.
	unrestrictedWithPaths := edgeUnrestrictedScope("proj", "gen")
	unrestrictedWithPaths.AuthResourcePaths = []string{"practitioner/pr-1"}
	if _, _, err := repo.ReadEdgesFromSource(ctx, unrestrictedWithPaths, "Encounter", "enc-1", 0, 10); !errors.Is(err, ErrInvalidEdgeScope) {
		t.Errorf("unrestricted scope carrying paths = %v, want ErrInvalidEdgeScope", err)
	}

	// Blank project is rejected.
	if _, _, err := repo.ReadEdgesFromSource(ctx, edgeRestrictedScope("  ", "gen", "a"), "Encounter", "enc-1", 0, 10); !errors.Is(err, ErrInvalidEdgeScope) {
		t.Errorf("blank project = %v, want ErrInvalidEdgeScope", err)
	}

	// Blank traversal discriminators are rejected.
	if _, _, err := repo.ReadEdgesFromSource(ctx, edgeUnrestrictedScope("proj", "gen"), " ", "enc-1", 0, 10); !errors.Is(err, ErrInvalidEdge) {
		t.Errorf("blank source type = %v, want ErrInvalidEdge", err)
	}
	if _, _, err := repo.ReadEdgesFromSource(ctx, edgeUnrestrictedScope("proj", "gen"), "Encounter", "", 0, 10); !errors.Is(err, ErrInvalidEdge) {
		t.Errorf("blank source id = %v, want ErrInvalidEdge", err)
	}
	if _, _, err := repo.ReadEdgesToTarget(ctx, edgeUnrestrictedScope("proj", "gen"), "", "pr-9", 0, 10); !errors.Is(err, ErrInvalidEdge) {
		t.Errorf("blank target type = %v, want ErrInvalidEdge", err)
	}
	if _, _, err := repo.ReadEdgesToTarget(ctx, edgeUnrestrictedScope("proj", "gen"), "Practitioner", " \t", 0, 10); !errors.Is(err, ErrInvalidEdge) {
		t.Errorf("blank target id = %v, want ErrInvalidEdge", err)
	}

	// Page bounds are rejected before touching the server.
	for _, limit := range []int{0, -1, MaxEdgeTraversalLimit + 1} {
		if _, _, err := repo.ReadEdgesFromSource(ctx, edgeUnrestrictedScope("proj", "gen"), "Encounter", "enc-1", 0, limit); !errors.Is(err, ErrInvalidEdgeScope) {
			t.Errorf("limit %d = %v, want ErrInvalidEdgeScope", limit, err)
		}
	}
	if _, _, err := repo.ReadEdgesToTarget(ctx, edgeUnrestrictedScope("proj", "gen"), "Practitioner", "pr-9", -1, 10); !errors.Is(err, ErrInvalidEdgeScope) {
		t.Errorf("negative cursor = %v, want ErrInvalidEdgeScope", err)
	}
}

// ---------------------------------------------------------------------------
// Real PostgreSQL 18 integration (service-gated by LOOM_TEST_POSTGRES_URL).
// ---------------------------------------------------------------------------

// TestEdgeMigrationCreatesTypedOccurrenceTable applies the embedded history
// to the owned schema twice and witnesses the fhir_edge_occurrence table
// against literal expectations: every scope/endpoint/occurrence column typed
// and NOT NULL except the nullable diagnostic logical_edge_key, the storage
// identity an always-generated bigint, the full occurrence identity a UNIQUE
// constraint, and both scoped traversal indexes present.
func TestEdgeMigrationCreatesTypedOccurrenceTable(t *testing.T) {
	svc := newEdgeService(t)
	first := svc.mustMigrateEmbedded(t)
	second := svc.mustMigrateEmbedded(t)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("re-apply changed the result:\n1st: %+v\n2nd: %+v", first, second)
	}
	assertLedgerEquals(t, svc.readLedger(t), mustEmbeddedMigrations(t))

	// The edge migration must be recorded under this exact name at version 4
	// (000003 is reserved for the generation-manifest ticket).
	var recordedName string
	if err := svc.iso.QueryRow(svc.ctx,
		`SELECT name FROM "`+svc.schema+`".`+LedgerTable+` WHERE version = 4`).Scan(&recordedName); err != nil {
		svc.fail("witness version 4 ledger row", err)
	}
	if recordedName != "edge_occurrence" {
		t.Fatalf("version 4 ledger name = %q, want edge_occurrence", recordedName)
	}

	type columnWitness struct {
		Name     string
		Type     string
		NotNull  bool
		Identity string
	}
	rows, err := svc.iso.Query(svc.ctx, `
		SELECT a.attname, format_type(a.atttypid, a.atttypmod), a.attnotnull,
		       CASE WHEN a.attidentity = 'a' THEN 'a'
		            WHEN a.attidentity = 'd' THEN 'd'
		            ELSE '' END
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relname = 'fhir_edge_occurrence'
		  AND a.attnum > 0 AND NOT a.attisdropped
		ORDER BY a.attnum`, svc.schema)
	if err != nil {
		svc.fail("witness columns", err)
	}
	defer rows.Close()
	var columns []columnWitness
	for rows.Next() {
		var col columnWitness
		if err := rows.Scan(&col.Name, &col.Type, &col.NotNull, &col.Identity); err != nil {
			svc.fail("witness columns scan", err)
		}
		columns = append(columns, col)
	}
	if err := rows.Err(); err != nil {
		svc.fail("witness columns rows", err)
	}
	wantColumns := []columnWitness{
		{"edge_seq", "bigint", true, "a"},
		{"project", "text", true, ""},
		{"dataset_generation", "text", true, ""},
		{"auth_resource_path", "text", true, ""},
		{"label", "text", true, ""},
		{"source_resource_type", "text", true, ""},
		{"source_logical_id", "text", true, ""},
		{"target_resource_type", "text", true, ""},
		{"target_logical_id", "text", true, ""},
		{"source_path", "text", true, ""},
		{"source_ordinal", "bigint", true, ""},
		{"logical_edge_key", "text", false, ""},
		{"recorded_at", "timestamp with time zone", true, ""},
	}
	if !reflect.DeepEqual(columns, wantColumns) {
		t.Fatalf("fhir_edge_occurrence columns = %+v, want %+v", columns, wantColumns)
	}

	constraintRows, err := svc.iso.Query(svc.ctx, `
		SELECT con.conname, pg_get_constraintdef(con.oid)
		FROM pg_constraint con
		JOIN pg_class c ON c.oid = con.conrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relname = 'fhir_edge_occurrence'
		  AND con.contype IN ('p', 'u', 'c')
		ORDER BY con.conname`, svc.schema)
	if err != nil {
		svc.fail("witness constraints", err)
	}
	defer constraintRows.Close()
	var constraints [][2]string
	for constraintRows.Next() {
		var name, def string
		if err := constraintRows.Scan(&name, &def); err != nil {
			svc.fail("witness constraints scan", err)
		}
		constraints = append(constraints, [2]string{name, def})
	}
	if err := constraintRows.Err(); err != nil {
		svc.fail("witness constraints rows", err)
	}
	wantConstraints := [][2]string{
		{"fhir_edge_occurrence_identity_ux",
			`UNIQUE (project, dataset_generation, source_resource_type, source_logical_id, label, target_resource_type, target_logical_id, source_path, source_ordinal)`},
		{"fhir_edge_occurrence_label_present", `CHECK ((label <> ''::text))`},
		{"fhir_edge_occurrence_ordinal_nonnegative", `CHECK ((source_ordinal >= 0))`},
		{"fhir_edge_occurrence_pkey", `PRIMARY KEY (edge_seq)`},
		{"fhir_edge_occurrence_project_present", `CHECK ((project <> ''::text))`},
		{"fhir_edge_occurrence_source_id_present", `CHECK ((source_logical_id <> ''::text))`},
		{"fhir_edge_occurrence_source_type_present", `CHECK ((source_resource_type <> ''::text))`},
		{"fhir_edge_occurrence_target_id_present", `CHECK ((target_logical_id <> ''::text))`},
		{"fhir_edge_occurrence_target_type_present", `CHECK ((target_resource_type <> ''::text))`},
	}
	if !reflect.DeepEqual(constraints, wantConstraints) {
		t.Fatalf("fhir_edge_occurrence constraints = %+v, want %+v", constraints, wantConstraints)
	}

	indexRows, err := svc.iso.Query(svc.ctx, `
		SELECT indexname, indexdef FROM pg_indexes
		WHERE schemaname = $1 AND tablename = 'fhir_edge_occurrence'
		ORDER BY indexname`, svc.schema)
	if err != nil {
		svc.fail("witness indexes", err)
	}
	defer indexRows.Close()
	var indexes [][2]string
	for indexRows.Next() {
		var name, def string
		if err := indexRows.Scan(&name, &def); err != nil {
			svc.fail("witness indexes scan", err)
		}
		indexes = append(indexes, [2]string{name, def})
	}
	if err := indexRows.Err(); err != nil {
		svc.fail("witness indexes rows", err)
	}
	wantIndexes := [][2]string{
		{"fhir_edge_occurrence_identity_ux",
			`CREATE UNIQUE INDEX fhir_edge_occurrence_identity_ux ON ` + svc.schema + `.fhir_edge_occurrence USING btree (project, dataset_generation, source_resource_type, source_logical_id, label, target_resource_type, target_logical_id, source_path, source_ordinal)`},
		{"fhir_edge_occurrence_pkey",
			`CREATE UNIQUE INDEX fhir_edge_occurrence_pkey ON ` + svc.schema + `.fhir_edge_occurrence USING btree (edge_seq)`},
		{"fhir_edge_occurrence_source_traversal_ix",
			`CREATE INDEX fhir_edge_occurrence_source_traversal_ix ON ` + svc.schema + `.fhir_edge_occurrence USING btree (project, dataset_generation, source_resource_type, source_logical_id, auth_resource_path, edge_seq)`},
		{"fhir_edge_occurrence_target_traversal_ix",
			`CREATE INDEX fhir_edge_occurrence_target_traversal_ix ON ` + svc.schema + `.fhir_edge_occurrence USING btree (project, dataset_generation, target_resource_type, target_logical_id, auth_resource_path, edge_seq)`},
	}
	if !reflect.DeepEqual(indexes, wantIndexes) {
		t.Fatalf("fhir_edge_occurrence indexes = %+v, want %+v", indexes, wantIndexes)
	}

	var rowCount int64
	if err := svc.iso.QueryRow(svc.ctx,
		`SELECT count(*) FROM "`+svc.schema+`".fhir_edge_occurrence`).Scan(&rowCount); err != nil {
		svc.fail("count edges", err)
	}
	if rowCount != 0 {
		t.Fatalf("fresh fhir_edge_occurrence has %d rows, want 0", rowCount)
	}
}

// TestEdgeInsertRequiresSourceInSameScope proves the source precondition: an
// edge lands only when its source resource exists in the same
// project/dataset_generation, and a missing source (absent entirely, or
// present only under another generation or project) returns the typed
// *EdgeSourceMissingError with zero rows written.
func TestEdgeInsertRequiresSourceInSameScope(t *testing.T) {
	svc := newEdgeService(t)
	svc.mustMigrateEmbedded(t)
	edgeRepo, resourceRepo := svc.newRepositories(t)

	svc.mustSeedResource(t, resourceRepo, "proj-s", "gen-1", "Encounter", "enc-1", "practitioner/pr-1")
	// Decoy source that exists only under gen-2 and only under proj-other.
	svc.mustSeedResource(t, resourceRepo, "proj-s", "gen-2", "Encounter", "enc-late", "practitioner/pr-1")
	svc.mustSeedResource(t, resourceRepo, "proj-other", "gen-1", "Encounter", "enc-1", "practitioner/pr-1")

	// In-scope source: the edge lands.
	landed := edgeRow("proj-s", "gen-1", "practitioner/pr-1", "participant",
		"Encounter", "enc-1", "Practitioner", "pr-9", "participant[0]", 0)
	seq := svc.mustInsertEdge(t, edgeRepo, landed)
	if seq < 1 {
		t.Fatalf("storage identity %d, want >= 1", seq)
	}

	missingCases := []struct {
		name string
		rec  EdgeOccurrenceRow
	}{
		{"no source at all", edgeRow("proj-s", "gen-1", "", "participant",
			"Encounter", "enc-nowhere", "Practitioner", "pr-9", "participant[0]", 0)},
		{"source only in another generation", edgeRow("proj-s", "gen-1", "", "participant",
			"Encounter", "enc-late", "Practitioner", "pr-9", "participant[0]", 0)},
		{"source only in another project", edgeRow("proj-missing", "gen-1", "", "participant",
			"Encounter", "enc-1", "Practitioner", "pr-9", "participant[0]", 0)},
	}
	for _, tc := range missingCases {
		_, err := edgeRepo.InsertEdge(svc.ctx, tc.rec)
		if !errors.Is(err, ErrEdgeSourceMissing) {
			t.Errorf("%s: InsertEdge = %v, want ErrEdgeSourceMissing", tc.name, err)
			continue
		}
		var missing *EdgeSourceMissingError
		if !errors.As(err, &missing) {
			t.Errorf("%s: error type = %T, want *EdgeSourceMissingError", tc.name, err)
			continue
		}
		want := &EdgeSourceMissingError{
			Project:            tc.rec.Project,
			DatasetGeneration:  tc.rec.DatasetGeneration,
			SourceResourceType: tc.rec.SourceResourceType,
			SourceLogicalID:    tc.rec.SourceLogicalID,
		}
		if !reflect.DeepEqual(missing, want) {
			t.Errorf("%s: error = %+v, want %+v", tc.name, missing, want)
		}
		// Direct SQL witness: the refused insert wrote nothing.
		if count := svc.countEdges(t, tc.rec); count != 0 {
			t.Errorf("%s: refused insert left %d rows, want 0", tc.name, count)
		}
	}

	// The in-scope edge survived untouched while all three refusals ran.
	if count := svc.countEdges(t, landed); count != 1 {
		t.Fatalf("in-scope occurrence has %d rows, want exactly 1", count)
	}
}

// TestEdgeUnresolvedTargetPreservesIdentityAndResolvesLater proves the
// unresolved-target contract: an edge whose target resource does not exist
// is stored with target resource type and logical id intact and reports
// TargetResolved=false; once the target resource is stored in the same
// scope, the very same edge row reports TargetResolved=true — resolution is
// read-time state, not a stored flag rewritten at target insert.
func TestEdgeUnresolvedTargetPreservesIdentityAndResolvesLater(t *testing.T) {
	svc := newEdgeService(t)
	svc.mustMigrateEmbedded(t)
	edgeRepo, resourceRepo := svc.newRepositories(t)

	svc.mustSeedResource(t, resourceRepo, "proj-u", "gen-1", "Observation", "obs-src", "")
	rec := edgeRow("proj-u", "gen-1", "", "focus",
		"Observation", "obs-src", "Condition", "cond-ghost", "focus", 0)
	rec.LogicalEdgeKey = ptr("Observation_obs-src_focus_Condition_cond-ghost")
	insertedSeq := svc.mustInsertEdge(t, edgeRepo, rec)

	// Direct SQL witness: the edge row exists; no target resource exists.
	if svc.countResources(t, "proj-u", "gen-1", "Condition", "cond-ghost") != 0 {
		t.Fatal("witness setup: Condition/cond-ghost must not exist yet")
	}
	witnesses := svc.witnessEdgeRows(t, "proj-u", "gen-1", "Observation", "obs-src", "focus", "Condition", "cond-ghost")
	if len(witnesses) != 1 {
		t.Fatalf("unresolved target stored %d rows, want exactly 1", len(witnesses))
	}
	want := edgeRowWitness{
		EdgeSeq: insertedSeq,
		Project: "proj-u", DatasetGeneration: "gen-1", AuthResourcePath: "",
		Label: "focus", SourceResourceType: "Observation", SourceLogicalID: "obs-src",
		TargetResourceType: "Condition", TargetLogicalID: "cond-ghost",
		SourcePath: "focus", SourceOrdinal: 0,
		LogicalEdgeKey:    ptr("Observation_obs-src_focus_Condition_cond-ghost"),
		RecordedAtPresent: true,
	}
	if !reflect.DeepEqual(witnesses[0], want) {
		t.Fatalf("stored edge row = %+v, want %+v", witnesses[0], want)
	}

	// Traversal reads the unresolved target's identity back verbatim.
	rows, hasMore := svc.mustReadFromSource(t, edgeRepo, edgeUnrestrictedScope("proj-u", "gen-1"),
		"Observation", "obs-src", 0, 10)
	if len(rows) != 1 || hasMore {
		t.Fatalf("source traversal = %d rows (hasMore=%t), want 1 row", len(rows), hasMore)
	}
	row := rows[0]
	if row.TargetResourceType != "Condition" || row.TargetLogicalID != "cond-ghost" {
		t.Errorf("unresolved target identity = %s/%s, want Condition/cond-ghost preserved",
			row.TargetResourceType, row.TargetLogicalID)
	}
	if row.TargetResolved {
		t.Errorf("TargetResolved = true before the target resource exists")
	}
	if row.SourceResourceType != "Observation" || row.SourceLogicalID != "obs-src" ||
		row.Label != "focus" || row.SourcePath != "focus" || row.SourceOrdinal != 0 {
		t.Errorf("occurrence identity drifted on read: %+v", row)
	}
	if row.LogicalEdgeKey == nil || *row.LogicalEdgeKey != "Observation_obs-src_focus_Condition_cond-ghost" {
		t.Errorf("logical edge key = %v, want the stored diagnostic", row.LogicalEdgeKey)
	}

	// Store the target in the same scope: the same row now resolves.
	svc.mustSeedResource(t, resourceRepo, "proj-u", "gen-1", "Condition", "cond-ghost", "")
	rows, _ = svc.mustReadFromSource(t, edgeRepo, edgeUnrestrictedScope("proj-u", "gen-1"),
		"Observation", "obs-src", 0, 10)
	if len(rows) != 1 {
		t.Fatalf("after target insert, traversal = %d rows, want 1 (no duplicate edge)", len(rows))
	}
	if !rows[0].TargetResolved {
		t.Errorf("TargetResolved = false after the target resource was stored in scope")
	}
	if rows[0].EdgeSeq != row.EdgeSeq {
		t.Errorf("edge storage identity changed from %d to %d: target insert must not rewrite edges",
			row.EdgeSeq, rows[0].EdgeSeq)
	}
}

// TestEdgeRepeatedReferenceMultiplicityPreserved proves the acceptance case
// the Arango endpoint key cannot express: two identical target/label
// references from different source path occurrences are two rows with
// distinct source path/ordinal identities (and the same collapsed
// logical_edge_key, the exact multiplicity an endpoint-keyed store loses),
// while the fully identical occurrence is a typed conflict that never
// stores a second row.
func TestEdgeRepeatedReferenceMultiplicityPreserved(t *testing.T) {
	svc := newEdgeService(t)
	svc.mustMigrateEmbedded(t)
	edgeRepo, resourceRepo := svc.newRepositories(t)

	svc.mustSeedResource(t, resourceRepo, "proj-m", "gen-1", "Encounter", "enc-1", "")
	const collapsedKey = "Encounter_enc-1_participant_Practitioner_pr-1"
	first := edgeRow("proj-m", "gen-1", "", "participant",
		"Encounter", "enc-1", "Practitioner", "pr-1", "participant[0]", 0)
	first.LogicalEdgeKey = ptr(collapsedKey)
	second := edgeRow("proj-m", "gen-1", "", "participant",
		"Encounter", "enc-1", "Practitioner", "pr-1", "participant[1]", 1)
	second.LogicalEdgeKey = ptr(collapsedKey)

	seqA := svc.mustInsertEdge(t, edgeRepo, first)
	seqB := svc.mustInsertEdge(t, edgeRepo, second)
	if seqA == seqB {
		t.Fatalf("both repeated references returned storage identity %d, want two distinct rows", seqA)
	}

	// Direct SQL witness: exactly two literal rows, identical endpoints and
	// label, distinct path/ordinal identities, shared collapsed key.
	witnesses := svc.witnessEdgeRows(t, "proj-m", "gen-1", "Encounter", "enc-1", "participant", "Practitioner", "pr-1")
	if len(witnesses) != 2 {
		t.Fatalf("repeated reference stored %d rows, want 2: %+v", len(witnesses), witnesses)
	}
	wantRows := []edgeRowWitness{
		{EdgeSeq: seqA, Project: "proj-m", DatasetGeneration: "gen-1", AuthResourcePath: "",
			Label: "participant", SourceResourceType: "Encounter", SourceLogicalID: "enc-1",
			TargetResourceType: "Practitioner", TargetLogicalID: "pr-1",
			SourcePath: "participant[0]", SourceOrdinal: 0,
			LogicalEdgeKey: ptr(collapsedKey), RecordedAtPresent: true},
		{EdgeSeq: seqB, Project: "proj-m", DatasetGeneration: "gen-1", AuthResourcePath: "",
			Label: "participant", SourceResourceType: "Encounter", SourceLogicalID: "enc-1",
			TargetResourceType: "Practitioner", TargetLogicalID: "pr-1",
			SourcePath: "participant[1]", SourceOrdinal: 1,
			LogicalEdgeKey: ptr(collapsedKey), RecordedAtPresent: true},
	}
	if !reflect.DeepEqual(witnesses, wantRows) {
		t.Fatalf("stored rows =\n%+v\nwant\n%+v", witnesses, wantRows)
	}

	// Same path, new ordinal: the ordinal alone distinguishes occurrences.
	third := edgeRow("proj-m", "gen-1", "", "participant",
		"Encounter", "enc-1", "Practitioner", "pr-1", "participant[0]", 1)
	third.LogicalEdgeKey = ptr(collapsedKey)
	svc.mustInsertEdge(t, edgeRepo, third)
	if got := svc.countEdges(t, third); got != 1 {
		t.Fatalf("same-path new-ordinal occurrence has %d rows, want 1", got)
	}

	// The fully identical occurrence is refused — even with a different
	// auth path, which is not part of the identity — and the stored rows
	// (identity, payload columns, auth path) are untouched.
	duplicate := first
	duplicate.AuthResourcePath = "practitioner/pr-other"
	_, err := edgeRepo.InsertEdge(svc.ctx, duplicate)
	if !errors.Is(err, ErrEdgeConflict) {
		t.Fatalf("exact duplicate insert = %v, want ErrEdgeConflict", err)
	}
	var conflict *EdgeConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error type = %T, want *EdgeConflictError", err)
	}
	wantConflict := &EdgeConflictError{
		Project: "proj-m", DatasetGeneration: "gen-1", AuthResourcePath: "practitioner/pr-other",
		Label: "participant", SourceResourceType: "Encounter", SourceLogicalID: "enc-1",
		TargetResourceType: "Practitioner", TargetLogicalID: "pr-1",
		SourcePath: "participant[0]", SourceOrdinal: 0,
	}
	if !reflect.DeepEqual(conflict, wantConflict) {
		t.Errorf("conflict = %+v, want %+v", conflict, wantConflict)
	}
	if got := svc.countEdges(t, first); got != 1 {
		t.Errorf("exact duplicate identity holds %d rows after the conflict, want exactly 1 (no silent upsert)", got)
	}
	after := svc.witnessEdgeRows(t, "proj-m", "gen-1", "Encounter", "enc-1", "participant", "Practitioner", "pr-1")
	if len(after) != 3 {
		t.Fatalf("occurrence group has %d rows after conflict, want 3", len(after))
	}
	for _, w := range after {
		if w.EdgeSeq == seqA && w.AuthResourcePath != "" {
			t.Errorf("the conflict replaced the stored auth path with %q", w.AuthResourcePath)
		}
	}
}

// TestEdgeTraversalsBindScopeAndOrder pages both traversal directions across
// decoys from other projects, generations, and unauthorized auth paths, and
// asserts literal pages in storage order. Seeding is deliberate: the target
// ids' lexical order contradicts the insertion/storage order, so a
// regression that drops ORDER BY edge_seq fails the literal expectations.
func TestEdgeTraversalsBindScopeAndOrder(t *testing.T) {
	svc := newEdgeService(t)
	svc.mustMigrateEmbedded(t)
	edgeRepo, resourceRepo := svc.newRepositories(t)

	// Every edge's source must exist in its own scope, decoys included.
	svc.mustSeedResource(t, resourceRepo, "proj-t", "gen-1", "Encounter", "enc-w", "practitioner/pr-1")
	svc.mustSeedResource(t, resourceRepo, "proj-t", "gen-1", "Encounter", "enc-w2", "practitioner/pr-1")
	svc.mustSeedResource(t, resourceRepo, "proj-x", "gen-1", "Encounter", "enc-w", "practitioner/pr-1")
	svc.mustSeedResource(t, resourceRepo, "proj-t", "gen-2", "Encounter", "enc-w", "practitioner/pr-1")
	// Only two targets are backed by resources in scope; the walked rows
	// for the others must report TargetResolved=false.
	svc.mustSeedResource(t, resourceRepo, "proj-t", "gen-1", "Patient", "pt-2", "practitioner/pr-1")
	svc.mustSeedResource(t, resourceRepo, "proj-t", "gen-1", "Patient", "pt-4", "practitioner/pr-1")

	inScope := []EdgeOccurrenceRow{
		edgeRow("proj-t", "gen-1", "practitioner/pr-1", "have", "Encounter", "enc-w", "Patient", "pt-1", "p", 0),
		edgeRow("proj-t", "gen-1", "practitioner/pr-1", "have", "Encounter", "enc-w", "Patient", "pt-2", "p", 0),
		edgeRow("proj-t", "gen-1", "practitioner/pr-1", "have", "Encounter", "enc-w", "Patient", "pt-3", "p", 0),
		edgeRow("proj-t", "gen-1", "practitioner/pr-1", "have", "Encounter", "enc-w", "Patient", "pt-4", "p", 0),
		edgeRow("proj-t", "gen-1", "practitioner/pr-1", "have", "Encounter", "enc-w", "Patient", "pt-5", "p", 0),
	}
	decoys := []EdgeOccurrenceRow{
		// Same source identity, other project / other generation /
		// unauthorized auth path — interleaved so their storage identities
		// land among the in-scope rows.
		edgeRow("proj-x", "gen-1", "practitioner/pr-1", "have", "Encounter", "enc-w", "Patient", "x1", "p", 0),
		edgeRow("proj-t", "gen-2", "practitioner/pr-1", "have", "Encounter", "enc-w", "Patient", "pt-6", "p", 0),
		edgeRow("proj-t", "gen-1", "practitioner/pr-forbidden", "have", "Encounter", "enc-w", "Patient", "pt-7", "p", 0),
		// A second authorized source pointing at one of the walked targets.
		edgeRow("proj-t", "gen-1", "practitioner/pr-1", "have", "Encounter", "enc-w2", "Patient", "pt-2", "p", 0),
	}
	// Storage order deliberately contradicts the target ids' lexical order:
	// pt-3, x1, pt-1, pt-6, pt-5, pt-7, pt-2(enc-w), pt-4, pt-2(enc-w2).
	insertOrder := []EdgeOccurrenceRow{
		inScope[2], decoys[0], inScope[0], decoys[1], inScope[4], decoys[2], inScope[1], inScope[3], decoys[3],
	}
	for _, rec := range insertOrder {
		svc.mustInsertEdge(t, edgeRepo, rec)
	}

	// Source-directed walk: limit 2 over the authorized path list. Literal
	// pages in storage order — the in-scope targets only.
	wantPages := [][]string{{"pt-3", "pt-1"}, {"pt-5", "pt-2"}, {"pt-4"}}
	scope := edgeRestrictedScope("proj-t", "gen-1", "practitioner/pr-1")
	var walked []EdgeOccurrenceRow
	cursor := int64(0)
	pages := 0
	for {
		rows, hasMore := svc.mustReadFromSource(t, edgeRepo, scope, "Encounter", "enc-w", cursor, 2)
		if pages < len(wantPages) {
			if len(rows) != len(wantPages[pages]) {
				t.Fatalf("page %d returned %d rows, want %d", pages, len(rows), len(wantPages[pages]))
			}
			for i, row := range rows {
				if row.TargetLogicalID != wantPages[pages][i] {
					t.Fatalf("page %d position %d returned target %q, want %q: the traversal is not ordered by storage identity",
						pages, i, row.TargetLogicalID, wantPages[pages][i])
				}
			}
		}
		pages++
		if pages > 10 {
			t.Fatal("source walk did not terminate within 10 pages")
		}
		for _, row := range rows {
			if row.Project != "proj-t" || row.DatasetGeneration != "gen-1" {
				t.Fatalf("source traversal crossed project/generation: %+v", row)
			}
			if row.AuthResourcePath != "practitioner/pr-1" {
				t.Fatalf("source traversal crossed auth path: %+v", row)
			}
			if row.EdgeSeq <= cursor {
				t.Fatalf("row %+v did not advance past cursor %d", row, cursor)
			}
			// pt-2 and pt-4 were seeded as resources; the rest are dangling
			// references whose resolution must read false.
			wantResolved := row.TargetLogicalID == "pt-2" || row.TargetLogicalID == "pt-4"
			if row.TargetResolved != wantResolved {
				t.Errorf("target %s TargetResolved = %t, want %t", row.TargetLogicalID, row.TargetResolved, wantResolved)
			}
		}
		walked = append(walked, rows...)
		if len(rows) > 0 {
			cursor = rows[len(rows)-1].EdgeSeq
		}
		if !hasMore {
			break
		}
	}
	if pages != len(wantPages) {
		t.Errorf("walked %d pages, want %d (five in-scope rows at limit 2)", pages, len(wantPages))
	}
	if len(walked) != 5 {
		t.Fatalf("source walk returned %d rows, want 5: %+v", len(walked), walked)
	}

	// Past the end: empty page, no more rows.
	rows, hasMore := svc.mustReadFromSource(t, edgeRepo, scope, "Encounter", "enc-w", cursor, 2)
	if len(rows) != 0 || hasMore {
		t.Errorf("read past the end returned %d rows (hasMore=%t), want empty", len(rows), hasMore)
	}

	// Restricted with no authorized paths reads nothing, without error.
	rows, hasMore, err := edgeRepo.ReadEdgesFromSource(svc.ctx, edgeRestrictedScope("proj-t", "gen-1"),
		"Encounter", "enc-w", 0, 10)
	if err != nil || len(rows) != 0 || hasMore {
		t.Errorf("restricted-empty source traversal = (%d rows, hasMore=%t, err=%v), want empty with no error",
			len(rows), hasMore, err)
	}

	// Unrestricted still cannot cross project or generation, but sees the
	// forbidden-path row.
	rows, hasMore = svc.mustReadFromSource(t, edgeRepo, edgeUnrestrictedScope("proj-t", "gen-1"),
		"Encounter", "enc-w", 0, MaxEdgeTraversalLimit)
	if hasMore {
		t.Fatal("unrestricted traversal reported more rows past a full page")
	}
	if len(rows) != 6 {
		t.Fatalf("unrestricted traversal returned %d rows, want 6 (five plus the forbidden-path row)", len(rows))
	}
	for _, row := range rows {
		if row.Project != "proj-t" || row.DatasetGeneration != "gen-1" {
			t.Fatalf("unrestricted traversal crossed project/generation: %+v", row)
		}
	}

	// Target-directed traversal: Patient/pt-2 is pointed at by enc-w and
	// enc-w2 in scope; the same target identity under another generation
	// (pt-6 no — distinct id) and the decoy ids never appear. Literal rows.
	rows, hasMore = svc.mustReadToTarget(t, edgeRepo, edgeRestrictedScope("proj-t", "gen-1", "practitioner/pr-1"),
		"Patient", "pt-2", 0, 10)
	if hasMore {
		t.Fatal("target traversal reported more rows past a full page")
	}
	if len(rows) != 2 {
		t.Fatalf("target traversal returned %d rows, want 2: %+v", len(rows), rows)
	}
	wantSources := []string{"enc-w", "enc-w2"} // insertion/storage order
	for i, row := range rows {
		if row.SourceLogicalID != wantSources[i] {
			t.Errorf("target traversal position %d came from %q, want %q", i, row.SourceLogicalID, wantSources[i])
		}
		if row.TargetResourceType != "Patient" || row.TargetLogicalID != "pt-2" || !row.TargetResolved {
			t.Errorf("target traversal row wrong: %+v", row)
		}
	}

	// The gen-2-only decoy edge (target pt-6) is invisible to the gen-1
	// target-directed walk: traversal cannot resolve across generation.
	rows, _ = svc.mustReadToTarget(t, edgeRepo, edgeRestrictedScope("proj-t", "gen-1", "practitioner/pr-1"),
		"Patient", "pt-6", 0, 10)
	if len(rows) != 0 {
		t.Errorf("target traversal resolved a gen-2-only decoy edge into gen-1: %+v", rows)
	}
	rows, _ = svc.mustReadToTarget(t, edgeRepo, edgeRestrictedScope("proj-x", "gen-1", "practitioner/pr-1"),
		"Patient", "x1", 0, 10)
	if len(rows) != 1 || rows[0].Project != "proj-x" {
		t.Errorf("other project sees %d rows for its own decoy, want exactly 1: %+v", len(rows), rows)
	}
}

// TestEdgeTargetResolutionBindsProjectAndGeneration proves the traversal's
// resolution join binds the edge's own project and dataset_generation: a
// target resource that exists only under another generation or another
// project leaves the edge unresolved in its own scope.
func TestEdgeTargetResolutionBindsProjectAndGeneration(t *testing.T) {
	svc := newEdgeService(t)
	svc.mustMigrateEmbedded(t)
	edgeRepo, resourceRepo := svc.newRepositories(t)

	svc.mustSeedResource(t, resourceRepo, "proj-r", "gen-1", "Encounter", "enc-r", "")
	rec := edgeRow("proj-r", "gen-1", "", "diagnosis",
		"Encounter", "enc-r", "Condition", "cond-r", "diagnosis[0]", 0)
	svc.mustInsertEdge(t, edgeRepo, rec)

	// Target exists only under gen-2 of the same project...
	svc.mustSeedResource(t, resourceRepo, "proj-r", "gen-2", "Condition", "cond-r", "")
	// ...and only under another project's gen-1.
	svc.mustSeedResource(t, resourceRepo, "proj-other-r", "gen-1", "Condition", "cond-r", "")

	rows, _ := svc.mustReadFromSource(t, edgeRepo, edgeUnrestrictedScope("proj-r", "gen-1"),
		"Encounter", "enc-r", 0, 10)
	if len(rows) != 1 {
		t.Fatalf("traversal returned %d rows, want 1", len(rows))
	}
	if rows[0].TargetResolved {
		t.Error("TargetResolved = true while the target exists only under another generation/project")
	}

	// Same story for the target-directed read.
	rows, _ = svc.mustReadToTarget(t, edgeRepo, edgeUnrestrictedScope("proj-r", "gen-1"),
		"Condition", "cond-r", 0, 10)
	if len(rows) != 1 || rows[0].TargetResolved {
		t.Errorf("target-directed traversal returned %+v, want the edge unresolved", rows)
	}

	// Storing the target in the edge's own scope resolves it.
	svc.mustSeedResource(t, resourceRepo, "proj-r", "gen-1", "Condition", "cond-r", "")
	rows, _ = svc.mustReadToTarget(t, edgeRepo, edgeUnrestrictedScope("proj-r", "gen-1"),
		"Condition", "cond-r", 0, 10)
	if len(rows) != 1 || !rows[0].TargetResolved {
		t.Errorf("target-directed traversal returned %+v, want the edge resolved after the in-scope target insert", rows)
	}
}

// TestEdgeLogicalKeyDistinguishesSQLNullFromEmptyString proves the three-way
// distinction on the diagnostic key: no key supplied is SQL NULL, a present
// key whose value is the empty string is not NULL, and both read back
// through the repository as their distinct Go forms.
func TestEdgeLogicalKeyDistinguishesSQLNullFromEmptyString(t *testing.T) {
	svc := newEdgeService(t)
	svc.mustMigrateEmbedded(t)
	edgeRepo, resourceRepo := svc.newRepositories(t)

	svc.mustSeedResource(t, resourceRepo, "proj-k", "gen-1", "Observation", "obs-k", "")
	noKey := edgeRow("proj-k", "gen-1", "", "focus", "Observation", "obs-k", "Patient", "k-1", "focus", 0)
	emptyKey := edgeRow("proj-k", "gen-1", "", "focus", "Observation", "obs-k", "Patient", "k-2", "focus", 0)
	emptyKey.LogicalEdgeKey = ptr("")
	svc.mustInsertEdge(t, edgeRepo, noKey)
	svc.mustInsertEdge(t, edgeRepo, emptyKey)

	// Direct SQL witnesses.
	var noKeyIsNull, emptyKeyIsNull bool
	var emptyKeyValue *string
	if err := svc.iso.QueryRow(svc.ctx,
		`SELECT logical_edge_key IS NULL FROM "`+svc.schema+`".fhir_edge_occurrence
		  WHERE target_logical_id = 'k-1' AND source_logical_id = 'obs-k'`).Scan(&noKeyIsNull); err != nil {
		svc.fail("witness absent logical key", err)
	}
	if err := svc.iso.QueryRow(svc.ctx,
		`SELECT logical_edge_key IS NULL, logical_edge_key FROM "`+svc.schema+`".fhir_edge_occurrence
		  WHERE target_logical_id = 'k-2' AND source_logical_id = 'obs-k'`).Scan(&emptyKeyIsNull, &emptyKeyValue); err != nil {
		svc.fail("witness empty logical key", err)
	}
	if !noKeyIsNull {
		t.Error("absent logical key was stored as a value, want SQL NULL")
	}
	if emptyKeyIsNull || emptyKeyValue == nil || *emptyKeyValue != "" {
		t.Errorf("empty-string key witness = (isNull=%t, value=%v), want a present empty string", emptyKeyIsNull, emptyKeyValue)
	}

	// Repository witnesses.
	rows, _ := svc.mustReadFromSource(t, edgeRepo, edgeUnrestrictedScope("proj-k", "gen-1"),
		"Observation", "obs-k", 0, 10)
	if len(rows) != 2 {
		t.Fatalf("traversal returned %d rows, want 2", len(rows))
	}
	for _, row := range rows {
		switch row.TargetLogicalID {
		case "k-1":
			if row.LogicalEdgeKey != nil {
				t.Errorf("absent key read back as %q, want nil", *row.LogicalEdgeKey)
			}
		case "k-2":
			if row.LogicalEdgeKey == nil || *row.LogicalEdgeKey != "" {
				t.Errorf("empty key read back as %v, want pointer to the empty string", row.LogicalEdgeKey)
			}
		default:
			t.Errorf("unexpected row %+v", row)
		}
	}
}

// TestEdgeNoLeftovers runs after every other edge test in this file (Go runs
// a package's tests in source order and each test's cleanups finish before
// the next test starts). It asserts this ticket's suite left zero owned
// resources behind: no pgstore05_it_* database on the service and no
// pgstore05_it_* schema in the service database. A hit here means an earlier
// run died before its cleanups converged; drop only resources carrying this
// ticket's unique prefix.
func TestEdgeNoLeftovers(t *testing.T) {
	raw := testPostgresURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect to test service: %v", err)
	}
	defer admin.Close()

	var leftDatabases []string
	rows, err := admin.Query(ctx,
		`SELECT datname FROM pg_database WHERE datname LIKE 'pgstore05_it_%' ORDER BY datname`)
	if err != nil {
		t.Fatalf("probe leftover databases: %v", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatalf("probe leftover databases scan: %v", err)
		}
		leftDatabases = append(leftDatabases, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("probe leftover databases rows: %v", err)
	}
	rows.Close()
	for _, name := range leftDatabases {
		if !pgstore05EdgeNamePattern.MatchString(name) {
			t.Fatalf("leftover database %q does not carry this ticket's prefix; refusing to drop it", name)
		}
		if _, err := admin.Exec(ctx, `DROP DATABASE IF EXISTS "`+name+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop leftover database %s: %v", name, err)
		} else {
			t.Errorf("dropped leftover database %s; an earlier edge test leaked it", name)
		}
	}

	var leftSchemas []string
	rows, err = admin.Query(ctx,
		`SELECT nspname FROM pg_namespace WHERE nspname LIKE 'pgstore05_it_%' ORDER BY nspname`)
	if err != nil {
		t.Fatalf("probe leftover schemas: %v", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatalf("probe leftover schemas scan: %v", err)
		}
		leftSchemas = append(leftSchemas, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("probe leftover schemas rows: %v", err)
	}
	rows.Close()
	for _, name := range leftSchemas {
		if !pgstore05EdgeNamePattern.MatchString(name) {
			t.Fatalf("leftover schema %q does not carry this ticket's prefix; refusing to drop it", name)
		}
		if _, err := admin.Exec(ctx, `DROP SCHEMA IF EXISTS "`+name+`" CASCADE`); err != nil {
			t.Errorf("drop leftover schema %s: %v", name, err)
		} else {
			t.Errorf("dropped leftover schema %s; an earlier edge test leaked it", name)
		}
	}
}

// ptr returns a pointer to v, for nullable column expectations.
func ptr[T any](v T) *T { return &v }
