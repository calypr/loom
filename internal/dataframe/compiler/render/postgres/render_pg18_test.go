package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/source"
	pgstore "github.com/calypr/loom/internal/store/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Real PostgreSQL 18 integration (service-gated by LOOM_TEST_POSTGRES_URL).
// Every acceptance assertion below runs against a real server: each test
// creates one uniquely owned pgir02_it_* database and one owned schema,
// applies the pg-store embedded migrations through the real pg-store-02
// runner, stores literal rows through the merged pg-store-04/05
// repositories, executes the rendered statement with its ordered driver
// arguments, and compares against literal expected rows plus a hand-written
// direct-SQL witness. A skipped required case is a failure for this ticket's
// acceptance; the gate exists only to keep unrelated CI lanes green.
// TestZZNoLeftoverPgIR02Resources audits that nothing survives.
// ---------------------------------------------------------------------------

var pgir02ResourceNamePattern = regexp.MustCompile(`^pgir02_it_[0-9]+_[0-9a-f]+$`)

func testPostgresURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 renderer integration tests")
	}
	return raw
}

func randomHex(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("read random bytes: %v", err)
	}
	return hex.EncodeToString(buf)
}

type pgir02Service struct {
	ctx          context.Context
	admin        *pgxpool.Pool // service database
	iso          *pgxpool.Pool // isolated database
	client       *pgstore.Client
	schema       string
	extraSchemas []string
	resources    *pgstore.ResourceRepository
	edges        *pgstore.EdgeRepository
	scrub        func(any) string
	fail         func(step string, err error)
}

func newPGIR02Service(t *testing.T, extraSchemaCount int) *pgir02Service {
	t.Helper()
	return newOwnedService(t, extraSchemaCount, "pgir02_it")
}

// newOwnedService creates one uniquely owned isolated database plus owned
// schemas under the given resource prefix. Each later ticket registers its
// own prefix so concurrent actors never audit or collide with one another's
// resources (pg-sql-05 uses verify_sql05_*).
func newOwnedService(t *testing.T, extraSchemaCount int, prefix string) *pgir02Service {
	t.Helper()
	raw := testPostgresURL(t)
	namePattern := regexp.MustCompile(`^` + regexp.QuoteMeta(prefix) + `_[0-9]+_[0-9a-f]+$`)

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

	dbName := fmt.Sprintf(prefix+"_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !namePattern.MatchString(dbName) {
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

	schema := fmt.Sprintf(prefix+"_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !namePattern.MatchString(schema) {
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

	client, err := pgstore.Open(ctx, pgstore.Config{
		DSN:            dsn,
		MaxConns:       4,
		MinConns:       1,
		AcquireTimeout: 2 * time.Second,
	})
	if err != nil {
		fail("open pg-store client (requires a PostgreSQL 18 service)", err)
	}
	t.Cleanup(client.Close)
	if _, err := pgstore.MigrateSchema(ctx, client, schema); err != nil {
		fail("migrate owned schema", err)
	}
	resources, err := pgstore.NewResourceRepository(client, schema)
	if err != nil {
		fail("build resource repository", err)
	}
	edges, err := pgstore.NewEdgeRepository(client, schema)
	if err != nil {
		fail("build edge repository", err)
	}
	svc := &pgir02Service{
		ctx: ctx, admin: admin, iso: iso, client: client, schema: schema,
		resources: resources, edges: edges, scrub: scrub, fail: fail,
	}
	for index := 0; index < extraSchemaCount; index++ {
		extra := fmt.Sprintf(prefix+"_%d_%s", time.Now().UnixNano(), randomHex(t))
		if !namePattern.MatchString(extra) {
			t.Fatalf("extra owned schema name %q does not match the required pattern", extra)
		}
		if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+extra+`"`); err != nil {
			fail("create extra schema", err)
		}
		t.Cleanup(func() {
			if _, err := iso.Exec(context.WithoutCancel(ctx),
				`DROP SCHEMA IF EXISTS "`+extra+`" CASCADE`); err != nil {
				t.Errorf("drop extra schema: %s", scrub(err))
			}
		})
		if _, err := pgstore.MigrateSchema(ctx, client, extra); err != nil {
			fail("migrate extra schema", err)
		}
		svc.extraSchemas = append(svc.extraSchemas, extra)
	}
	return svc
}

func (s *pgir02Service) insertResource(t *testing.T, schema string, project, generation, resourceType, logicalID, authPath string, payload any) int64 {
	t.Helper()
	var payloadValue json.RawMessage
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		payloadValue = raw
	}
	repo := s.resources
	if schema != s.schema {
		var err error
		repo, err = pgstore.NewResourceRepository(s.client, schema)
		if err != nil {
			t.Fatalf("build repository for schema %s: %v", schema, err)
		}
	}
	seq, err := repo.InsertResource(s.ctx, source.Resource{
		Project:           project,
		DatasetGeneration: generation,
		ResourceType:      resourceType,
		LogicalID:         logicalID,
		AuthResourcePath:  authPath,
		Payload:           payloadValue,
	})
	if err != nil {
		s.fail("insert resource "+resourceType+"/"+logicalID, err)
	}
	return seq
}

func (s *pgir02Service) insertEdge(t *testing.T, rec pgstore.EdgeOccurrenceRow) int64 {
	t.Helper()
	seq, err := s.edges.InsertEdge(s.ctx, rec)
	if err != nil {
		s.fail("insert edge "+rec.Label+" "+rec.SourceLogicalID+"->"+rec.TargetLogicalID, err)
	}
	return seq
}

// runRendered executes the rendered statement through pgx with its ordered
// driver arguments and returns stringified positional rows. Only text-y
// columns (logical ids, JSON scalars) are selected by these tests, so
// stringify comparison is exact.
func (s *pgir02Service) runRendered(t *testing.T, rendered command.RenderedPostgresPhysicalPlan) [][]string {
	t.Helper()
	args := make([]any, len(rendered.Args))
	for index, parameter := range rendered.Args {
		args[index] = parameter.Value
	}
	rows, err := s.iso.Query(s.ctx, rendered.SQL, args...)
	if err != nil {
		s.fail("execute rendered statement", err)
	}
	defer rows.Close()
	var out [][]string
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			s.fail("read rendered row", err)
		}
		row := make([]string, len(values))
		for index, value := range values {
			row[index] = stringifyCell(value)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		s.fail("finish rendered statement", err)
	}
	return out
}

func (s *pgir02Service) witness(t *testing.T, sql string, args ...any) [][]string {
	t.Helper()
	rows, err := s.iso.Query(s.ctx, sql, args...)
	if err != nil {
		s.fail("execute direct-SQL witness", err)
	}
	defer rows.Close()
	var out [][]string
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			s.fail("read witness row", err)
		}
		row := make([]string, len(values))
		for index, value := range values {
			row[index] = stringifyCell(value)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		s.fail("finish witness", err)
	}
	return out
}

func stringifyCell(value any) string {
	switch typed := value.(type) {
	case nil:
		return "<null>"
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return fmt.Sprintf("%v", typed)
	}
}

func scopedPlan(project, generation string, paths []string, unrestricted bool) ir.PhysicalPlan {
	return ir.PhysicalPlan{
		Version: 1,
		Engine:  ir.PhysicalEngineAQL,
		BindVars: map[string]any{
			"root_collection":                  "Patient",
			"project":                          project,
			"dataset_generation":               generation,
			"auth_resource_paths":              paths,
			"auth_resource_paths_unrestricted": unrestricted,
			"scope_allowed":                    true,
		},
		Operations: []ir.PhysicalOperation{
			{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		},
	}
}

func withReturn(plan ir.PhysicalPlan, projections ...ir.PhysicalProjection) ir.PhysicalPlan {
	plan.Operations = append(plan.Operations, ir.PhysicalOperation{
		Kind:   ir.PhysicalReturnOp,
		Return: &ir.PhysicalReturn{Projections: projections},
	})
	return plan
}

const (
	itProject    = "pgir02-proj"
	itGeneration = "gen-1"
)

func bindingFor(t *testing.T, schema string) ir.PostgresStorageBinding {
	t.Helper()
	binding, err := ir.NewPostgresStorageBinding(schema)
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding(%q): %v", schema, err)
	}
	return binding
}

// ---------------------------------------------------------------------------
// Case 1: scoped read with filter, sort, limit — literal rows and witness.
// ---------------------------------------------------------------------------

func TestPG18ScopedResourceReadMatchesLiteralRowsAndDirectSQLWitness(t *testing.T) {
	svc := newPGIR02Service(t, 0)
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Patient", "p-final", "FHIR/Patient", map[string]any{"status": "final", "score": 3})
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Patient", "p-prelim", "FHIR/Patient", map[string]any{"status": "prelim"})
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Patient", "p-other-path", "FHIR/Encounter", map[string]any{"status": "final"})
	svc.insertResource(t, svc.schema, itProject, "gen-2", "Patient", "p-other-gen", "FHIR/Patient", map[string]any{"status": "final"})
	svc.insertResource(t, svc.schema, "pgir02-other", itGeneration, "Patient", "p-other-proj", "FHIR/Patient", map[string]any{"status": "final"})
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Observation", "o-1", "FHIR/Observation", map[string]any{"status": "final"})

	plan := scopedPlan(itProject, itGeneration, []string{"FHIR/Patient", "FHIR/Encounter"}, false)
	plan.BindVars["status_value"] = "final"
	plan.BindVars["limit"] = 5
	plan.Operations = append(plan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"status"}},
			Right: &ir.PhysicalValue{BindKey: "status_value"},
		}}},
		ir.PhysicalOperation{Kind: ir.PhysicalSortOp, Sort: &ir.PhysicalSort{
			Keys: []ir.PhysicalValue{{Variable: "root", Path: []string{"logical_id"}}},
		}},
		ir.PhysicalOperation{Kind: ir.PhysicalLimitOp, Limit: &ir.PhysicalLimit{BindKey: "limit"}},
	)
	plan = withReturn(plan,
		ir.PhysicalProjection{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
		ir.PhysicalProjection{Name: "auth", Value: ir.PhysicalValue{Variable: "root", Path: []string{"auth_resource_path"}}},
	)

	rendered, err := RenderPhysicalPlan(plan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	// No data in the statement text.
	for _, data := range []string{"final", itProject, itGeneration, "FHIR/Patient", "p-final"} {
		if strings.Contains(rendered.SQL, data) {
			t.Fatalf("value %q leaked into rendered SQL: %s", data, rendered.SQL)
		}
	}
	got := svc.runRendered(t, rendered)
	want := [][]string{
		{"p-final", "FHIR/Patient"},
		{"p-other-path", "FHIR/Encounter"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rendered rows = %v, want %v", got, want)
	}

	// Direct-SQL witness: an independently hand-written statement with the
	// literals inline must yield the identical rows.
	witness := svc.witness(t,
		`SELECT logical_id, auth_resource_path FROM "`+svc.schema+`"."fhir_resource"`+
			` WHERE project = 'pgir02-proj' AND dataset_generation = 'gen-1'`+
			` AND resource_type = 'Patient'`+
			` AND auth_resource_path IN ('FHIR/Patient','FHIR/Encounter')`+
			` AND payload #> '{status}'::text[] = '"final"'::jsonb`+
			` ORDER BY logical_id, resource_seq LIMIT 5`)
	if !reflect.DeepEqual(witness, want) {
		t.Fatalf("witness rows = %v, want %v", witness, want)
	}
}

// ---------------------------------------------------------------------------
// Case 2: restricted-empty auth reads nothing while the witness proves the
// rows exist (absence is authorization, never an empty store).
// ---------------------------------------------------------------------------

func TestPG18RestrictedEmptyAuthReadsNothingWitnessShowsRows(t *testing.T) {
	svc := newPGIR02Service(t, 0)
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Patient", "p-1", "FHIR/Patient", map[string]any{"status": "final"})

	plan := withReturn(scopedPlan(itProject, itGeneration, []string{}, false),
		ir.PhysicalProjection{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}})
	rendered, err := RenderPhysicalPlan(plan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	if got := svc.runRendered(t, rendered); len(got) != 0 {
		t.Fatalf("restricted-empty auth must read nothing, got %v", got)
	}
	if !strings.Contains(rendered.SQL, "$3") || !strings.Contains(rendered.SQL, "auth_resource_path") {
		t.Fatalf("restricted mode must keep the auth predicate: %s", rendered.SQL)
	}
	witness := svc.witness(t,
		`SELECT count(*) FROM "`+svc.schema+`"."fhir_resource" WHERE project = $1 AND dataset_generation = $2 AND resource_type = 'Patient'`,
		itProject, itGeneration)
	if len(witness) != 1 || witness[0][0] != "1" {
		t.Fatalf("witness must show the row exists for the restricted-empty read to mean anything: %v", witness)
	}
}

// ---------------------------------------------------------------------------
// Case 3: explicit unrestricted mode sees every authorized-path state.
// ---------------------------------------------------------------------------

func TestPG18ExplicitUnrestrictedSeesAllPaths(t *testing.T) {
	svc := newPGIR02Service(t, 0)
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Patient", "p-1", "FHIR/Patient", nil)
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Patient", "p-2", "", nil)
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Patient", "p-3", "FHIR/Anything", nil)

	plan := withReturn(scopedPlan(itProject, itGeneration, nil, true),
		ir.PhysicalProjection{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}})
	rendered, err := RenderPhysicalPlan(plan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	if strings.Contains(rendered.SQL, "auth_resource_path") {
		t.Fatalf("explicit unrestricted mode must add no auth predicate: %s", rendered.SQL)
	}
	got := svc.runRendered(t, rendered)
	want := [][]string{{"p-1"}, {"p-2"}, {"p-3"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// Case 4: edge traversal — repeated-owner multiplicity preserved, unresolved
// targets excluded, scope and direction bound exactly.
// ---------------------------------------------------------------------------

func TestPG18EdgeTraversalPreservesMultiplicityAndExcludesUnresolvedTargets(t *testing.T) {
	svc := newPGIR02Service(t, 0)
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Patient", "sp-1", "FHIR/Patient", nil)
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Specimen", "st-1", "FHIR/Specimen", nil)
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Specimen", "st-2", "FHIR/Specimen", nil)

	svc.insertEdge(t, pgstore.EdgeOccurrenceRow{
		Project: itProject, DatasetGeneration: itGeneration, AuthResourcePath: "FHIR/Patient",
		Label: "has-specimen", SourceResourceType: "Patient", SourceLogicalID: "sp-1",
		TargetResourceType: "Specimen", TargetLogicalID: "st-1", SourcePath: "specimen[0]", SourceOrdinal: 0,
	})
	svc.insertEdge(t, pgstore.EdgeOccurrenceRow{
		Project: itProject, DatasetGeneration: itGeneration, AuthResourcePath: "FHIR/Patient",
		Label: "has-specimen", SourceResourceType: "Patient", SourceLogicalID: "sp-1",
		TargetResourceType: "Specimen", TargetLogicalID: "st-1", SourcePath: "specimen[1]", SourceOrdinal: 0,
	})
	svc.insertEdge(t, pgstore.EdgeOccurrenceRow{
		Project: itProject, DatasetGeneration: itGeneration, AuthResourcePath: "FHIR/Patient",
		Label: "has-specimen", SourceResourceType: "Patient", SourceLogicalID: "sp-1",
		TargetResourceType: "Specimen", TargetLogicalID: "st-2", SourcePath: "contained[0].specimen", SourceOrdinal: 0,
	})
	// Unresolved target: the reference is stored, but no Specimen row exists.
	svc.insertEdge(t, pgstore.EdgeOccurrenceRow{
		Project: itProject, DatasetGeneration: itGeneration, AuthResourcePath: "FHIR/Patient",
		Label: "has-specimen", SourceResourceType: "Patient", SourceLogicalID: "sp-1",
		TargetResourceType: "Specimen", TargetLogicalID: "st-ghost", SourcePath: "specimen[9]", SourceOrdinal: 0,
	})

	plan := scopedPlan(itProject, itGeneration, []string{"FHIR/Patient", "FHIR/Specimen"}, false)
	plan.BindVars["edge_collection"] = "fhir_edge"
	plan.BindVars["edge_label"] = "has-specimen"
	plan.BindVars["target_type"] = "Specimen"
	plan.Operations = append(plan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Traversal: &ir.PhysicalTraversal{
			SourceVariable: "root", TargetVariable: "target", EdgeVariable: "edge",
			Direction: ir.PhysicalOutbound, EdgeCollectionBindKey: "edge_collection",
			EdgeLabelBindKey: "edge_label", TargetTypeBindKey: "target_type",
			EdgeTargetTypeField: "to_type", Strategy: ir.PhysicalTraversalNative,
		}},
	)
	plan = withReturn(plan,
		ir.PhysicalProjection{Name: "patient_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
		ir.PhysicalProjection{Name: "specimen_id", Value: ir.PhysicalValue{Variable: "target", Path: []string{"logical_id"}}},
	)
	rendered, err := RenderPhysicalPlan(plan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	for _, data := range []string{"has-specimen", itProject, "Specimen"} {
		if strings.Contains(rendered.SQL, data) {
			t.Fatalf("value %q leaked into traversal SQL: %s", data, rendered.SQL)
		}
	}

	got := svc.runRendered(t, rendered)
	want := [][]string{{"sp-1", "st-1"}, {"sp-1", "st-1"}, {"sp-1", "st-2"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("traversal rows = %v, want repeated-owner multiplicity %v", got, want)
	}

	// Direct-SQL witness: unresolved targets are stored but never join.
	stored := svc.witness(t,
		`SELECT count(*) FROM "`+svc.schema+`"."fhir_edge_occurrence" WHERE project=$1 AND dataset_generation=$2 AND target_logical_id='st-ghost'`,
		itProject, itGeneration)
	if len(stored) != 1 || stored[0][0] != "1" {
		t.Fatalf("witness must show the unresolved occurrence is stored: %v", stored)
	}
	witness := svc.witness(t,
		`SELECT r0.logical_id, r1.logical_id FROM "`+svc.schema+`"."fhir_resource" r0`+
			` JOIN "`+svc.schema+`"."fhir_edge_occurrence" e ON e.project=r0.project AND e.dataset_generation=r0.dataset_generation`+
			` AND e.auth_resource_path='FHIR/Patient' AND e.label='has-specimen'`+
			` AND e.source_resource_type='Patient' AND e.source_logical_id=r0.logical_id`+
			` AND e.target_resource_type='Specimen'`+
			` JOIN "`+svc.schema+`"."fhir_resource" r1 ON r1.project=e.project AND r1.dataset_generation=e.dataset_generation`+
			` AND r1.auth_resource_path='FHIR/Specimen' AND r1.resource_type='Specimen' AND r1.logical_id=e.target_logical_id`+
			` WHERE r0.project=$1 AND r0.dataset_generation=$2 AND r0.resource_type='Patient' AND r0.auth_resource_path='FHIR/Patient'`+
			` ORDER BY r0.resource_seq, e.edge_seq`,
		itProject, itGeneration)
	if !reflect.DeepEqual(witness, want) {
		t.Fatalf("witness rows = %v, want %v", witness, want)
	}
}

func TestPG18InboundTraversalBindsReverseIdentity(t *testing.T) {
	svc := newPGIR02Service(t, 0)
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Specimen", "st-1", "FHIR/Specimen", nil)
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Observation", "o-1", "FHIR/Observation", nil)
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Observation", "o-2", "FHIR/Observation", nil)
	// Forward storage shape (child _from -> parent _to analog): the specimen
	// is the stored source; the dataframe route is INBOUND from specimen...
	// Here: observations reference the specimen; INBOUND from Observation
	// reaches... use a root Observation reaching the Specimen through the
	// stored source side.
	svc.insertEdge(t, pgstore.EdgeOccurrenceRow{
		Project: itProject, DatasetGeneration: itGeneration, AuthResourcePath: "FHIR/Observation",
		Label: "focus-specimen", SourceResourceType: "Observation", SourceLogicalID: "o-1",
		TargetResourceType: "Specimen", TargetLogicalID: "st-1", SourcePath: "focus[0]", SourceOrdinal: 0,
	})

	// INBOUND from the Specimen side: anchored endpoint is target_*, reached
	// endpoint is source_* (an Observation).
	plan := ir.PhysicalPlan{
		Version: 1,
		Engine:  ir.PhysicalEngineAQL,
		BindVars: map[string]any{
			"root_collection":                  "Specimen",
			"project":                          itProject,
			"dataset_generation":               itGeneration,
			"auth_resource_paths":              []string{"FHIR/Specimen", "FHIR/Observation"},
			"auth_resource_paths_unrestricted": false,
			"scope_allowed":                    true,
			"edge_collection":                  "fhir_edge",
			"edge_label":                       "focus-specimen",
			"target_type":                      "Observation",
		},
		Operations: []ir.PhysicalOperation{
			{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
			{Kind: ir.PhysicalTraversalOp, Traversal: &ir.PhysicalTraversal{
				SourceVariable: "root", TargetVariable: "target", EdgeVariable: "edge",
				Direction: ir.PhysicalInbound, EdgeCollectionBindKey: "edge_collection",
				EdgeLabelBindKey: "edge_label", TargetTypeBindKey: "target_type",
				EdgeTargetTypeField: "from_type", Strategy: ir.PhysicalTraversalNative,
			}},
		},
	}
	plan = withReturn(plan,
		ir.PhysicalProjection{Name: "specimen_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
		ir.PhysicalProjection{Name: "observation_id", Value: ir.PhysicalValue{Variable: "target", Path: []string{"logical_id"}}},
	)
	rendered, err := RenderPhysicalPlan(plan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	got := svc.runRendered(t, rendered)
	want := [][]string{{"st-1", "o-1"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("inbound rows = %v, want %v", got, want)
	}
	witness := svc.witness(t,
		`SELECT 1 FROM "`+svc.schema+`"."fhir_edge_occurrence" WHERE project=$1 AND dataset_generation=$2 AND label='focus-specimen' AND target_logical_id='st-1' AND source_logical_id='o-1'`,
		itProject, itGeneration)
	if len(witness) != 1 {
		t.Fatalf("witness occurrence missing: %v", witness)
	}
}

// ---------------------------------------------------------------------------
// Case 5: hostile filter values are inert data.
// ---------------------------------------------------------------------------

func TestPG18HostileFilterValuesAreInert(t *testing.T) {
	svc := newPGIR02Service(t, 0)
	hostile := `x'; DROP TABLE fhir_resource; --`
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Patient", "p-hostile", "FHIR/Patient", map[string]any{"note": hostile})
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Patient", "p-plain", "FHIR/Patient", map[string]any{"note": "plain"})

	plan := scopedPlan(itProject, itGeneration, []string{"FHIR/Patient"}, false)
	plan.BindVars["note_value"] = hostile
	plan.Operations = append(plan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"note"}},
			Right: &ir.PhysicalValue{BindKey: "note_value"},
		}}},
	)
	plan = withReturn(plan,
		ir.PhysicalProjection{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}})
	rendered, err := RenderPhysicalPlan(plan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	if strings.Contains(rendered.SQL, "DROP") || strings.Contains(rendered.SQL, hostile) {
		t.Fatalf("hostile value reached statement text: %s", rendered.SQL)
	}
	got := svc.runRendered(t, rendered)
	if !reflect.DeepEqual(got, [][]string{{"p-hostile"}}) {
		t.Fatalf("hostile equality rows = %v, want the stored hostile row only", got)
	}
	// The tables survived the hostile value.
	for _, table := range []string{"fhir_resource", "fhir_edge_occurrence"} {
		survived := svc.witness(t, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=$2`, svc.schema, table)
		if len(survived) != 1 || survived[0][0] != "1" {
			t.Fatalf("%s table was affected by a bound value", table)
		}
	}
	resourcesRemaining := svc.witness(t,
		`SELECT count(*) FROM "`+svc.schema+`"."fhir_resource" WHERE project=$1 AND dataset_generation=$2`, itProject, itGeneration)
	if len(resourcesRemaining) != 1 || resourcesRemaining[0][0] != "2" {
		t.Fatalf("fhir_resource rows were affected by a bound value: %v", resourcesRemaining)
	}
	witness := svc.witness(t,
		`SELECT logical_id FROM "`+svc.schema+`"."fhir_resource" WHERE project=$1 AND dataset_generation=$2 AND resource_type='Patient' AND payload #> '{note}'::text[] = $3::jsonb`,
		itProject, itGeneration, func() json.RawMessage {
			raw, _ := json.Marshal(hostile)
			return raw
		}())
	if !reflect.DeepEqual(witness, [][]string{{"p-hostile"}}) {
		t.Fatalf("witness rows = %v", witness)
	}
}

// ---------------------------------------------------------------------------
// Case 6: the storage binding selects the owned schema only.
// ---------------------------------------------------------------------------

func TestPG18BindingSelectsOwnedSchemaOnly(t *testing.T) {
	svc := newPGIR02Service(t, 1)
	other := svc.extraSchemas[0]
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Patient", "p-in-owned", "FHIR/Patient", nil)
	// The same logical row placed in the shadow schema must be invisible to
	// a statement built for the owned schema.
	shadowRepo, err := pgstore.NewResourceRepository(svc.client, other)
	if err != nil {
		t.Fatalf("shadow repository: %v", err)
	}
	if _, err := shadowRepo.InsertResource(svc.ctx, source.Resource{
		Project: itProject, DatasetGeneration: itGeneration, ResourceType: "Patient",
		LogicalID: "p-in-shadow", AuthResourcePath: "FHIR/Patient",
	}); err != nil {
		svc.fail("insert into shadow schema", err)
	}

	plan := withReturn(scopedPlan(itProject, itGeneration, []string{"FHIR/Patient"}, false),
		ir.PhysicalProjection{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}})
	rendered, err := RenderPhysicalPlan(plan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	if !strings.Contains(rendered.SQL, `"`+svc.schema+`"."fhir_resource"`) {
		t.Fatalf("statement does not name the bound schema: %s", rendered.SQL)
	}
	got := svc.runRendered(t, rendered)
	if !reflect.DeepEqual(got, [][]string{{"p-in-owned"}}) {
		t.Fatalf("rows = %v, want only the owned-schema row", got)
	}
	// Witness: the shadow row exists in the other schema, so the miss is
	// schema selection, not missing data.
	shadow := svc.witness(t,
		`SELECT logical_id FROM "`+other+`"."fhir_resource" WHERE project=$1 AND dataset_generation=$2`,
		itProject, itGeneration)
	if !reflect.DeepEqual(shadow, [][]string{{"p-in-shadow"}}) {
		t.Fatalf("shadow schema rows = %v, want the shadow row to exist", shadow)
	}
}

// ---------------------------------------------------------------------------
// Case 7: predicate-parity repairs executed on real PG18.
// ---------------------------------------------------------------------------

func TestPG18PredicateParityRepairs(t *testing.T) {
	svc := newPGIR02Service(t, 0)
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Patient", "p-active", "FHIR/Patient", map[string]any{"status": "active"})
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Patient", "p-nostatus", "FHIR/Patient", map[string]any{"note": "x"})

	logicalID := ir.PhysicalProjection{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}}

	// NOT over a payload equality must keep the row missing the compared
	// key: AQL evaluates the missing-key comparison as false, so NOT is
	// true; an unguarded SQL NOT (NULL) would silently drop it.
	notPlan := scopedPlan(itProject, itGeneration, []string{"FHIR/Patient"}, false)
	notPlan.BindVars["status_value"] = "active"
	notPlan.Operations = append(notPlan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Expression: &ir.PhysicalPredicateExpression{
			Kind: ir.PhysicalNotPredicate,
			Children: []ir.PhysicalPredicateExpression{
				{Kind: ir.PhysicalComparisonPredicate, Comparison: &ir.PhysicalPredicate{
					Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"status"}},
					Right: &ir.PhysicalValue{BindKey: "status_value"},
				}},
			},
		}}},
	)
	notPlan = withReturn(notPlan, logicalID)
	notRendered, err := RenderPhysicalPlan(notPlan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("NOT render: %v", err)
	}
	notGot := svc.runRendered(t, notRendered)
	if !reflect.DeepEqual(notGot, [][]string{{"p-nostatus"}}) {
		t.Fatalf("NOT over missing-key rows = %v, want only the missing-key row", notGot)
	}
	witness := svc.witness(t,
		`SELECT logical_id FROM "`+svc.schema+`"."fhir_resource" WHERE project=$1 AND dataset_generation=$2 AND payload #> '{status}'::text[] IS NULL`,
		itProject, itGeneration)
	if !reflect.DeepEqual(witness, [][]string{{"p-nostatus"}}) {
		t.Fatalf("missing-key witness = %v", witness)
	}

	// An empty IN list binds an empty array: the statement executes and
	// selects nobody, matching the AQL x IN [] reading.
	inPlan := scopedPlan(itProject, itGeneration, []string{"FHIR/Patient"}, false)
	inPlan.BindVars["in_values"] = []string{}
	inPlan.Operations = append(inPlan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "IN", Left: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}},
			Right: &ir.PhysicalValue{BindKey: "in_values"},
		}}},
	)
	inPlan = withReturn(inPlan, logicalID)
	inRendered, err := RenderPhysicalPlan(inPlan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("empty IN render: %v", err)
	}
	if got := svc.runRendered(t, inRendered); len(got) != 0 {
		t.Fatalf("empty IN rows = %v, want none", got)
	}

	// Column EXISTS/MISSING are null tests over NOT NULL storage columns:
	// EXISTS keeps every row and MISSING keeps none.
	existsPlan := scopedPlan(itProject, itGeneration, []string{"FHIR/Patient"}, false)
	existsPlan.Operations = append(existsPlan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "EXISTS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}},
		}}},
	)
	existsPlan = withReturn(existsPlan, logicalID)
	existsRendered, err := RenderPhysicalPlan(existsPlan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("EXISTS render: %v", err)
	}
	existsRows := svc.runRendered(t, existsRendered)
	if len(existsRows) != 2 {
		t.Fatalf("column EXISTS rows = %v, want both rows", existsRows)
	}
	missingPlan := scopedPlan(itProject, itGeneration, []string{"FHIR/Patient"}, false)
	missingPlan.Operations = append(missingPlan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "MISSING", Left: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}},
		}}},
	)
	missingPlan = withReturn(missingPlan, logicalID)
	missingRendered, err := RenderPhysicalPlan(missingPlan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("MISSING render: %v", err)
	}
	if got := svc.runRendered(t, missingRendered); len(got) != 0 {
		t.Fatalf("column MISSING rows = %v, want none", got)
	}

	// NOT_EQUALS over a typed column executes two-valued.
	neqPlan := scopedPlan(itProject, itGeneration, []string{"FHIR/Patient"}, false)
	neqPlan.BindVars["id_value"] = "p-active"
	neqPlan.Operations = append(neqPlan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "NOT_EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}},
			Right: &ir.PhysicalValue{BindKey: "id_value"},
		}}},
	)
	neqPlan = withReturn(neqPlan, logicalID)
	neqRendered, err := RenderPhysicalPlan(neqPlan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("column NOT_EQUALS render: %v", err)
	}
	if got := svc.runRendered(t, neqRendered); !reflect.DeepEqual(got, [][]string{{"p-nostatus"}}) {
		t.Fatalf("column NOT_EQUALS rows = %v, want the other row", got)
	}

	// Ordering comparisons against the JSON null literal must follow the
	// AQL order, where null sorts below every value: LTE is the
	// null-or-missing test, GT is the presence test; LT/GTE are constant in
	// AQL and must fail closed with a typed error rather than render a
	// wrong or parameter-stranding plan.
	for _, tc := range []struct {
		operator string
		want     [][]string
	}{
		{"LTE", [][]string{{"p-nostatus"}}},
		{"GT", [][]string{{"p-active"}}},
		{"EQUALS", [][]string{{"p-nostatus"}}},
		{"NOT_EQUALS", [][]string{{"p-active"}}},
	} {
		nullPlan := scopedPlan(itProject, itGeneration, []string{"FHIR/Patient"}, false)
		nullPlan.BindVars["null_value"] = json.RawMessage("null")
		nullPlan.Operations = append(nullPlan.Operations,
			ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator: tc.operator, Left: ir.PhysicalValue{Variable: "root", Path: []string{"status"}},
				Right: &ir.PhysicalValue{BindKey: "null_value"},
			}}},
		)
		nullPlan = withReturn(nullPlan, logicalID)
		nullRendered, err := RenderPhysicalPlan(nullPlan, bindingFor(t, svc.schema))
		if err != nil {
			t.Fatalf("%s null literal render: %v", tc.operator, err)
		}
		got := svc.runRendered(t, nullRendered)
		if len(got) != len(tc.want) {
			t.Fatalf("%s null literal rows = %v, want %v", tc.operator, got, tc.want)
		}
		seen := map[string]bool{}
		for _, row := range got {
			seen[row[0]] = true
		}
		for _, row := range tc.want {
			if !seen[row[0]] {
				t.Fatalf("%s null literal rows = %v, want %v", tc.operator, got, tc.want)
			}
		}
	}
	for _, operator := range []string{"LT", "GTE"} {
		rejectPlan := scopedPlan(itProject, itGeneration, []string{"FHIR/Patient"}, false)
		rejectPlan.BindVars["null_value"] = json.RawMessage("null")
		rejectPlan.Operations = append(rejectPlan.Operations,
			ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator: operator, Left: ir.PhysicalValue{Variable: "root", Path: []string{"status"}},
				Right: &ir.PhysicalValue{BindKey: "null_value"},
			}}},
		)
		rejectPlan = withReturn(rejectPlan, logicalID)
		if _, err := RenderPhysicalPlan(rejectPlan, bindingFor(t, svc.schema)); !errors.Is(err, ErrUnsupportedPostgresPlan) {
			t.Fatalf("%s against null literal error = %v, want ErrUnsupportedPostgresPlan", operator, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Zero-leftover audit (runs last: this file sorts before render_test.go and
// no unit test creates owned resources).
// ---------------------------------------------------------------------------

func TestZZNoLeftoverPgIR02Resources(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		// No service was used; nothing can have been created.
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer admin.Close()
	var databases int
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_database WHERE datname LIKE 'pgir02_it_%'").Scan(&databases); err != nil {
		t.Fatalf("count databases: %v", err)
	}
	if databases != 0 {
		t.Fatalf("%d pgir02_it_* databases still exist", databases)
	}
	var schemas int
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'pgir02_it_%'").Scan(&schemas); err != nil {
		t.Fatalf("count schemas: %v", err)
	}
	if schemas != 0 {
		t.Fatalf("%d pgir02_it_* schemas still exist in the service database", schemas)
	}
}
