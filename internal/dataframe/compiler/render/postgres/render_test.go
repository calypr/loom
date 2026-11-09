package postgres

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

const testSchema = "pgir02_it_unit"

func mustBinding(t *testing.T) ir.PostgresStorageBinding {
	t.Helper()
	binding, err := ir.NewPostgresStorageBinding(testSchema)
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding: %v", err)
	}
	return binding
}

// basePlan builds the smallest plan that passes ir validation and the
// PostgreSQL gate: a scoped direct root scan with a positional return.
func basePlan() ir.PhysicalPlan {
	return ir.PhysicalPlan{
		Version: 1,
		Engine:  ir.PhysicalEngineAQL,
		BindVars: map[string]any{
			"root_collection":                  "Patient",
			"project":                          "proj-a",
			"dataset_generation":               "gen-1",
			"auth_resource_paths":              []string{"FHIR/Patient", "FHIR/Observation"},
			"auth_resource_paths_unrestricted": false,
			"limit":                            10,
		},
		Operations: []ir.PhysicalOperation{
			{
				Kind:     ir.PhysicalRootScanOp,
				RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"},
			},
			{
				Kind: ir.PhysicalReturnOp,
				Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
					{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
				}},
			},
		},
	}
}

func renderOK(t *testing.T, plan ir.PhysicalPlan) compiler.RenderedPostgresPhysicalPlan {
	t.Helper()
	rendered, err := RenderPhysicalPlan(plan, mustBinding(t))
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	return rendered
}

func wantUnsupported(t *testing.T, err error, context string) {
	t.Helper()
	if !errors.Is(err, ErrUnsupportedPostgresPlan) {
		t.Fatalf("%s: error = %v (%T), want ErrUnsupportedPostgresPlan", context, err, err)
	}
}

// ---------------------------------------------------------------------------
// Trusted identifier contract
// ---------------------------------------------------------------------------

func TestStorageBindingAcceptsOnlyRegisteredIdentifiers(t *testing.T) {
	binding := mustBinding(t)
	if err := binding.Validate(); err != nil {
		t.Fatalf("constructed binding rejected: %v", err)
	}
	if got, want := binding.Resource.Identifier().SQLText(), `"pgir02_it_unit"."fhir_resource"`; got != want {
		t.Fatalf("resource SQLText = %q, want %q", got, want)
	}
	if got, want := binding.Edge.Identifier().SQLText(), `"pgir02_it_unit"."fhir_edge_occurrence"`; got != want {
		t.Fatalf("edge SQLText = %q, want %q", got, want)
	}
	if _, err := ir.NewPostgresStorageBinding(`public"; DROP TABLE fhir_resource; --`); !errors.Is(err, ir.ErrUnregisteredPostgresIdentifier) {
		t.Fatalf("injection schema error = %v, want ErrUnregisteredPostgresIdentifier", err)
	}
	for _, schema := range []string{"", " ", "Public", "public name", "0abc", strings.Repeat("a", 64)} {
		if _, err := ir.NewPostgresStorageBinding(schema); !errors.Is(err, ir.ErrUnregisteredPostgresIdentifier) {
			t.Errorf("schema %q error = %v, want ErrUnregisteredPostgresIdentifier", schema, err)
		}
	}
	if _, err := ir.NewPostgresTableIdentifier("public", ir.PostgresLogicalTableKind("clickhouse_rows")); !errors.Is(err, ir.ErrUnregisteredPostgresIdentifier) {
		t.Fatalf("unknown logical kind error = %v, want ErrUnregisteredPostgresIdentifier", err)
	}
}

func TestStorageBindingZeroValueFailsRenderer(t *testing.T) {
	if err := (ir.PostgresStorageBinding{}).Validate(); !errors.Is(err, ir.ErrUnregisteredPostgresIdentifier) {
		t.Fatalf("zero binding error = %v, want ErrUnregisteredPostgresIdentifier", err)
	}
	if _, err := RenderPhysicalPlan(basePlan(), ir.PostgresStorageBinding{}); !errors.Is(err, ir.ErrUnregisteredPostgresIdentifier) {
		t.Fatalf("zero binding render error = %v, want ErrUnregisteredPostgresIdentifier", err)
	}
}

func TestColumnAllowlistsMatchMigrationsLiterally(t *testing.T) {
	resourceColumns, err := ir.RegisteredPostgresColumnNames(ir.PostgresResourceLogicalTable)
	if err != nil {
		t.Fatalf("resource columns: %v", err)
	}
	wantResource := []string{"auth_resource_path", "dataset_generation", "logical_id", "payload", "project", "recorded_at", "resource_seq", "resource_type"}
	if !reflect.DeepEqual(resourceColumns, wantResource) {
		t.Fatalf("resource columns = %v, want %v", resourceColumns, wantResource)
	}
	edgeColumns, err := ir.RegisteredPostgresColumnNames(ir.PostgresEdgeOccurrenceLogicalTable)
	if err != nil {
		t.Fatalf("edge columns: %v", err)
	}
	wantEdge := []string{
		"auth_resource_path", "dataset_generation", "edge_seq", "label", "logical_edge_key",
		"project", "recorded_at", "source_logical_id", "source_ordinal", "source_path",
		"source_resource_type", "target_logical_id", "target_resource_type",
	}
	if !reflect.DeepEqual(edgeColumns, wantEdge) {
		t.Fatalf("edge columns = %v, want %v", edgeColumns, wantEdge)
	}
	binding := mustBinding(t)
	if _, err := binding.Resource.Column("secret_column"); !errors.Is(err, ir.ErrUnregisteredPostgresIdentifier) {
		t.Fatalf("unregistered column error = %v, want ErrUnregisteredPostgresIdentifier", err)
	}
	if _, err := (ir.PostgresLogicalTable{}).Column("project"); !errors.Is(err, ir.ErrUnregisteredPostgresIdentifier) {
		t.Fatalf("zero logical table column error = %v, want ErrUnregisteredPostgresIdentifier", err)
	}
}

// ---------------------------------------------------------------------------
// Deterministic SQL and ordered arguments
// ---------------------------------------------------------------------------

func TestRenderScopedRootScanIsExactAndParameterized(t *testing.T) {
	plan := basePlan()
	plan.Operations = plan.Operations[:1]
	plan.Operations = append(plan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalSortOp, Sort: &ir.PhysicalSort{
			Keys: []ir.PhysicalValue{{Variable: "root", Path: []string{"logical_id"}}},
		}},
		ir.PhysicalOperation{Kind: ir.PhysicalLimitOp, Limit: &ir.PhysicalLimit{BindKey: "limit"}},
		ir.PhysicalOperation{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
			{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
			{Name: "status", Value: ir.PhysicalValue{Variable: "root", Path: []string{"status"}}},
		}}},
	)
	rendered := renderOK(t, plan)

	wantSQL := `SELECT r0."logical_id" AS "c0", (r0."payload" #> $6::text[]) AS "c1"` +
		` FROM "pgir02_it_unit"."fhir_resource" AS r0` +
		` WHERE r0."project" = $1 AND r0."dataset_generation" = $2` +
		` AND r0."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb))` +
		` AND r0."resource_type" = $4` +
		` ORDER BY r0."logical_id" ASC, r0."resource_seq" ASC LIMIT $5`
	if rendered.SQL != wantSQL {
		t.Fatalf("SQL mismatch:\n got %s\nwant %s", rendered.SQL, wantSQL)
	}
	wantArgs := []compiler.PostgreSQLParameter{
		{Type: compiler.PostgreSQLParameterText, Value: "proj-a"},
		{Type: compiler.PostgreSQLParameterText, Value: "gen-1"},
		{Type: compiler.PostgreSQLParameterJSONB, Value: json.RawMessage(`["FHIR/Patient","FHIR/Observation"]`)},
		{Type: compiler.PostgreSQLParameterText, Value: "Patient"},
		{Type: compiler.PostgreSQLParameterBigint, Value: int64(10)},
		{Type: compiler.PostgreSQLParameterText, Value: "{status}"},
	}
	if !reflect.DeepEqual(rendered.Args, wantArgs) {
		t.Fatalf("args mismatch:\n got %#v\nwant %#v", rendered.Args, wantArgs)
	}
	if !reflect.DeepEqual(rendered.OutputColumns, []string{"logical_id", "status"}) {
		t.Fatalf("output columns = %v", rendered.OutputColumns)
	}
	if strings.Contains(rendered.SQL, "@") {
		t.Fatalf("rendered PostgreSQL SQL carries AQL bind syntax: %s", rendered.SQL)
	}
}

func TestRenderOutboundTraversalSQLIsExact(t *testing.T) {
	plan := basePlan()
	plan.BindVars["edge_collection"] = "fhir_edge"
	plan.BindVars["edge_label"] = "has-specimen"
	plan.BindVars["target_type"] = "Specimen"
	plan.Operations = []ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		{Kind: ir.PhysicalTraversalOp, Traversal: &ir.PhysicalTraversal{
			SourceVariable: "root", TargetVariable: "target", EdgeVariable: "edge",
			Direction: ir.PhysicalOutbound, EdgeCollectionBindKey: "edge_collection",
			EdgeLabelBindKey: "edge_label", TargetTypeBindKey: "target_type",
			EdgeTargetTypeField: "to_type", Strategy: ir.PhysicalTraversalNative,
		}},
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
			{Name: "patient_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
			{Name: "specimen_id", Value: ir.PhysicalValue{Variable: "target", Path: []string{"logical_id"}}},
		}}},
	}
	rendered := renderOK(t, plan)
	wantSQL := `SELECT r0."logical_id" AS "c0", r1."logical_id" AS "c1"` +
		` FROM "pgir02_it_unit"."fhir_resource" AS r0` +
		` INNER JOIN "pgir02_it_unit"."fhir_edge_occurrence" AS e1` +
		` ON e1."project" = $1 AND e1."dataset_generation" = $2` +
		` AND e1."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb))` +
		` AND e1."label" = $5 AND e1."source_resource_type" = $4` +
		` AND e1."source_logical_id" = r0."logical_id" AND e1."target_resource_type" = $6` +
		` INNER JOIN "pgir02_it_unit"."fhir_resource" AS r1` +
		` ON r1."project" = $1 AND r1."dataset_generation" = $2` +
		` AND r1."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb))` +
		` AND r1."resource_type" = $6 AND r1."logical_id" = e1."target_logical_id"` +
		` WHERE r0."project" = $1 AND r0."dataset_generation" = $2` +
		` AND r0."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb))` +
		` AND r0."resource_type" = $4` +
		` ORDER BY r0."resource_seq" ASC, e1."edge_seq" ASC`
	if rendered.SQL != wantSQL {
		t.Fatalf("SQL mismatch:\n got %s\nwant %s", rendered.SQL, wantSQL)
	}
	if len(rendered.Args) != 6 {
		t.Fatalf("args = %d, want 6 (project, generation, auth paths, root type, label, target type)", len(rendered.Args))
	}
	// The plan's own Arango metadata never reaches SQL: the edge "collection"
	// bind value and plan variable names are not SQL text.
	for _, leaked := range []string{"fhir_edge\"", "\"edge\"", "\"target\"", "\"root\""} {
		if strings.Contains(rendered.SQL, leaked) {
			t.Fatalf("plan data %q leaked into SQL: %s", leaked, rendered.SQL)
		}
	}
}

func TestRenderInboundTraversalBindsReverseEndpointColumns(t *testing.T) {
	plan := basePlan()
	plan.BindVars["edge_collection"] = "fhir_edge"
	plan.BindVars["edge_label"] = "has-specimen"
	plan.BindVars["target_type"] = "Observation"
	plan.Operations = []ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		{Kind: ir.PhysicalTraversalOp, Traversal: &ir.PhysicalTraversal{
			SourceVariable: "root", TargetVariable: "target",
			Direction: ir.PhysicalInbound, EdgeCollectionBindKey: "edge_collection",
			EdgeLabelBindKey: "edge_label", TargetTypeBindKey: "target_type",
			EdgeTargetTypeField: "from_type", Strategy: ir.PhysicalTraversalNative,
		}},
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
			{Name: "observation_id", Value: ir.PhysicalValue{Variable: "target", Path: []string{"logical_id"}}},
		}}},
	}
	rendered := renderOK(t, plan)
	// INBOUND anchors the stored source side's opposite: the anchored
	// endpoint is target_* and the reached endpoint is source_*.
	for _, want := range []string{
		`e1."target_resource_type" = $4`,
		`e1."target_logical_id" = r0."logical_id"`,
		`e1."source_resource_type" = $6`,
		`r1."logical_id" = e1."source_logical_id"`,
	} {
		if !strings.Contains(rendered.SQL, want) {
			t.Fatalf("inbound SQL missing %q: %s", want, rendered.SQL)
		}
	}
}

// ---------------------------------------------------------------------------
// Filter rendering: values and paths never become SQL text
// ---------------------------------------------------------------------------

func TestRenderFiltersBindValuesAndPaths(t *testing.T) {
	binding := mustBinding(t)
	plan := basePlan()
	plan.BindVars["status_value"] = "final"
	plan.BindVars["count_value"] = float64(3)
	plan.BindVars["in_values"] = []string{"a", "b"}
	plan.BindVars["since"] = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	plan.Operations = []ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"status"}},
			Right: &ir.PhysicalValue{BindKey: "status_value"},
		}}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "GT", Left: ir.PhysicalValue{Variable: "root", Path: []string{"repeats"}},
			Right: &ir.PhysicalValue{BindKey: "count_value"},
		}}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "IN", Left: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}},
			Right: &ir.PhysicalValue{BindKey: "in_values"},
		}}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "GTE", Left: ir.PhysicalValue{Variable: "root", Path: []string{"recorded_at"}},
			Right: &ir.PhysicalValue{BindKey: "since"},
		}}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "EXISTS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"subject"}},
		}}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "MISSING", Left: ir.PhysicalValue{Variable: "root", Path: []string{"deceased"}},
		}}},
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
			{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
		}}},
	}
	_ = binding
	rendered := renderOK(t, plan)
	for _, want := range []string{
		`(r0."payload" #> $5::text[]) = ($6::jsonb)`,
		`(r0."payload" #> $7::text[]) > ($8::jsonb)`,
		`r0."logical_id" = ANY (SELECT jsonb_array_elements_text($9::jsonb))`,
		`r0."recorded_at" >= $10`,
		`COALESCE(jsonb_typeof(r0."payload" #> $11::text[]), 'null') <> 'null'`,
		`COALESCE(jsonb_typeof(r0."payload" #> $12::text[]), 'null') = 'null'`,
	} {
		if !strings.Contains(rendered.SQL, want) {
			t.Fatalf("SQL missing %q:\n%s", want, rendered.SQL)
		}
	}
	for _, leaked := range []string{"final", "\"a\"", "2026-11-09", "subject", "status"} {
		if strings.Contains(rendered.SQL, leaked) {
			t.Fatalf("filter data %q leaked into SQL:\n%s", leaked, rendered.SQL)
		}
	}
	if got := rendered.Args[5]; got.Type != compiler.PostgreSQLParameterJSONB || string(got.Value.(json.RawMessage)) != `"final"` {
		t.Fatalf("status value arg = %#v", got)
	}
	if got := rendered.Args[7]; string(got.Value.(json.RawMessage)) != `3` {
		t.Fatalf("numeric value arg = %#v", got)
	}
	if got := rendered.Args[8]; string(got.Value.(json.RawMessage)) != `["a","b"]` {
		t.Fatalf("IN value arg = %#v", got)
	}
}

func TestRenderNotEqualsUsesDistinctFromForMissingParity(t *testing.T) {
	plan := basePlan()
	plan.BindVars["status_value"] = "final"
	plan.Operations = []ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "NOT_EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"status"}},
			Right: &ir.PhysicalValue{BindKey: "status_value"},
		}}},
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
			{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
		}}},
	}
	rendered := renderOK(t, plan)
	if !strings.Contains(rendered.SQL, `(r0."payload" #> $5::text[]) IS DISTINCT FROM ($6::jsonb)`) {
		t.Fatalf("NOT_EQUALS must be distinct-from semantics:\n%s", rendered.SQL)
	}
}

func TestRenderCompoundPredicateExpression(t *testing.T) {
	plan := basePlan()
	plan.BindVars["status_value"] = "final"
	plan.BindVars["gender_value"] = "female"
	plan.Operations = []ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Expression: &ir.PhysicalPredicateExpression{
			Kind: ir.PhysicalAllPredicate,
			Children: []ir.PhysicalPredicateExpression{
				{Kind: ir.PhysicalComparisonPredicate, Comparison: &ir.PhysicalPredicate{
					Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"gender"}},
					Right: &ir.PhysicalValue{BindKey: "gender_value"},
				}},
				{Kind: ir.PhysicalNotPredicate, Children: []ir.PhysicalPredicateExpression{
					{Kind: ir.PhysicalComparisonPredicate, Comparison: &ir.PhysicalPredicate{
						Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"status"}},
						Right: &ir.PhysicalValue{BindKey: "status_value"},
					}},
				}},
			},
		}}},
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
			{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
		}}},
	}
	rendered := renderOK(t, plan)
	// The payload child is three-valued (missing key -> SQL NULL), so NOT
	// must be COALESCE-guarded: AQL keeps the row (NOT(false) after its
	// null coercion), an unguarded NOT (NULL) would drop it.
	if !strings.Contains(rendered.SQL, `COALESCE(NOT ((r0."payload" #> $7::text[]) = ($8::jsonb)), true)`) {
		t.Fatalf("compound predicate SQL:\n%s", rendered.SQL)
	}
}

// ---------------------------------------------------------------------------
// Injection: plan strings never become SQL
// ---------------------------------------------------------------------------

func TestPlanStringsNeverBecomeSQLText(t *testing.T) {
	plan := basePlan()
	evil := `x"; DROP TABLE fhir_resource; --`
	evilPath := "evil_payload_field"
	plan.BindVars["project"] = evil
	plan.BindVars["dataset_generation"] = `g"; DELETE FROM fhir_edge_occurrence WHERE 1=1 --`
	plan.BindVars["auth_resource_paths"] = []string{evil}
	plan.BindVars["root_collection"] = evil
	plan.BindVars["status_value"] = `s' OR '1'='1`
	plan.Operations = []ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{evilPath}},
			Right: &ir.PhysicalValue{BindKey: "status_value"},
		}}},
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
			{Name: evil, Value: ir.PhysicalValue{Variable: "root", Path: []string{evilPath}}},
		}}},
	}
	rendered := renderOK(t, plan)
	for _, leaked := range []string{"DROP", "DELETE", evil, `s' OR '1'='1`} {
		if strings.Contains(rendered.SQL, leaked) {
			t.Fatalf("plan text %q leaked into SQL:\n%s", leaked, rendered.SQL)
		}
	}
	// The hostile values survive as bound data.
	if rendered.OutputColumns[0] != evil {
		t.Fatalf("output name not carried as data: %v", rendered.OutputColumns)
	}
	var sawProject, sawGeneration, sawAuth, sawValue bool
	for _, arg := range rendered.Args {
		switch value := arg.Value.(type) {
		case string:
			sawProject = sawProject || value == evil
			sawGeneration = sawGeneration || strings.HasPrefix(value, `g"; DELETE`)
		case json.RawMessage:
			sawAuth = sawAuth || strings.Contains(string(value), "DROP")
			sawValue = sawValue || string(value) == `"s' OR '1'='1"`
		}
	}
	if !sawProject || !sawGeneration || !sawAuth || !sawValue {
		t.Fatalf("hostile values missing from ordered args (project=%t generation=%t auth=%t value=%t): %#v",
			sawProject, sawGeneration, sawAuth, sawValue, rendered.Args)
	}
}

// ---------------------------------------------------------------------------
// Typed errors: unsupported cases fail closed, never fall back
// ---------------------------------------------------------------------------

func TestRenderFailsClosedForScopeDefects(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing project", func(b map[string]any) { delete(b, "project") }},
		{"blank project", func(b map[string]any) { b["project"] = "  " }},
		{"missing generation", func(b map[string]any) { delete(b, "dataset_generation") }},
		{"missing auth mode", func(b map[string]any) { delete(b, "auth_resource_paths_unrestricted") }},
		{"missing auth paths", func(b map[string]any) { delete(b, "auth_resource_paths") }},
		{"unrestricted with paths", func(b map[string]any) { b["auth_resource_paths_unrestricted"] = true }},
	} {
		plan := basePlan()
		tc.mutate(plan.BindVars)
		_, err := RenderPhysicalPlan(plan, mustBinding(t))
		if !errors.Is(err, ErrInvalidPostgresPlanScope) {
			t.Errorf("%s: error = %v, want ErrInvalidPostgresPlanScope", tc.name, err)
		}
	}
}

func TestRenderRestrictedEmptyAuthPathsBindsEmptySet(t *testing.T) {
	plan := basePlan()
	plan.BindVars["auth_resource_paths"] = []string{}
	rendered := renderOK(t, plan)
	if !strings.Contains(rendered.SQL, `= ANY (SELECT jsonb_array_elements_text($3::jsonb))`) {
		t.Fatalf("restricted mode must bind the (empty) path set:\n%s", rendered.SQL)
	}
	if string(rendered.Args[2].Value.(json.RawMessage)) != "[]" {
		t.Fatalf("empty auth path set must bind as [], got %#v", rendered.Args[2])
	}
}

func TestRenderExplicitUnrestrictedDropsAuthPredicate(t *testing.T) {
	plan := basePlan()
	plan.BindVars["auth_resource_paths_unrestricted"] = true
	plan.BindVars["auth_resource_paths"] = []string(nil)
	rendered := renderOK(t, plan)
	if strings.Contains(rendered.SQL, "auth_resource_path") {
		t.Fatalf("explicit unrestricted mode must not add an auth predicate:\n%s", rendered.SQL)
	}
}

func TestRenderRejectsUnsupportedSurfaceWithTypedErrors(t *testing.T) {
	// Cases marked gate=true must reach the PostgreSQL capability gate and
	// fail with ErrUnsupportedPostgresPlan. The rest are rejected fail-closed
	// (rendering must never succeed) but may stop earlier at the frozen
	// ir.Validate invariants; a silent fallback is the only unacceptable
	// outcome.
	cases := []struct {
		name   string
		gate   bool
		mutate func(*ir.PhysicalPlan)
	}{
		{"clickhouse engine", false, func(p *ir.PhysicalPlan) { p.Engine = ir.PhysicalEngineClickHouse }},
		{"population root", false, func(p *ir.PhysicalPlan) {
			p.Operations[0].RootScan.Population = &ir.PhysicalPopulationRootSource{}
		}},
		{"cohort root", false, func(p *ir.PhysicalPlan) {
			p.Operations[0].RootScan.CohortSource = &ir.PhysicalCohortRootSource{}
		}},
		{"candidate seed", false, func(p *ir.PhysicalPlan) {
			p.Operations[0].RootScan.PageCandidateSeed = &ir.PhysicalRootPageCandidateSeed{}
		}},
		{"unnest", false, func(p *ir.PhysicalPlan) {
			p.Operations = append(p.Operations[:1],
				ir.PhysicalOperation{Kind: ir.PhysicalUnnestOp, Unnest: &ir.PhysicalUnnest{}}, p.Operations[1])
		}},
		{"collection scan", false, func(p *ir.PhysicalPlan) {
			p.Operations = append(p.Operations[:1],
				ir.PhysicalOperation{Kind: ir.PhysicalCollectionScanOp, CollectionScan: &ir.PhysicalCollectionScan{}}, p.Operations[1])
		}},
		{"grouped pivot", false, func(p *ir.PhysicalPlan) {
			p.Operations = append(p.Operations[:1],
				ir.PhysicalOperation{Kind: ir.PhysicalGroupedPivotOp, GroupedPivot: &ir.PhysicalGroupedPivot{}}, p.Operations[1])
		}},
		{"missing return", false, func(p *ir.PhysicalPlan) { p.Operations = p.Operations[:1] }},
		{"any direction traversal", true, func(p *ir.PhysicalPlan) {
			addTraversal(p, ir.PhysicalAny, "to_type", "", nil)
		}},
		{"endpoint lookup strategy", true, func(p *ir.PhysicalPlan) {
			addTraversal(p, ir.PhysicalOutbound, "to_type", ir.PhysicalTraversalEndpointLookup, []string{"_from"})
		}},
		{"unproven discriminator", true, func(p *ir.PhysicalPlan) {
			addTraversal(p, ir.PhysicalOutbound, "from_type", "", nil)
		}},
		{"contains_text filter", true, func(p *ir.PhysicalPlan) {
			p.BindVars["needle"] = "abc"
			p.Operations = append(p.Operations[:1], ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{
				Predicate: ir.PhysicalPredicate{
					Operator: "CONTAINS_TEXT", Left: ir.PhysicalValue{Variable: "root", Path: []string{"name"}},
					Right: &ir.PhysicalValue{BindKey: "needle"},
				}}}, p.Operations[1])
		}},
		{"column IN with non-slice", true, func(p *ir.PhysicalPlan) {
			p.BindVars["bad_in"] = "not-a-list"
			p.Operations = append(p.Operations[:1], ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{
				Predicate: ir.PhysicalPredicate{
					Operator: "IN", Left: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}},
					Right: &ir.PhysicalValue{BindKey: "bad_in"},
				}}}, p.Operations[1])
		}},
		{"payload IN", true, func(p *ir.PhysicalPlan) {
			p.BindVars["bad_in"] = []string{"a"}
			p.Operations = append(p.Operations[:1], ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{
				Predicate: ir.PhysicalPredicate{
					Operator: "IN", Left: ir.PhysicalValue{Variable: "root", Path: []string{"status"}},
					Right: &ir.PhysicalValue{BindKey: "bad_in"},
				}}}, p.Operations[1])
		}},
		{"payload sort", true, func(p *ir.PhysicalPlan) {
			p.Operations = append(p.Operations[:1], ir.PhysicalOperation{Kind: ir.PhysicalSortOp, Sort: &ir.PhysicalSort{
				Keys: []ir.PhysicalValue{{Variable: "root", Path: []string{"status"}}}}}, p.Operations[1])
		}},
		{"hidden projection", true, func(p *ir.PhysicalPlan) {
			p.Operations[1].Return.Projections[0].Hidden = true
		}},
		{"bind-backed projection", true, func(p *ir.PhysicalPlan) {
			p.BindVars["const"] = "x"
			p.Operations[1].Return.Projections[0].Value = ir.PhysicalValue{BindKey: "const"}
		}},
	}
	for _, tc := range cases {
		plan := basePlan()
		tc.mutate(&plan)
		_, err := RenderPhysicalPlan(plan, mustBinding(t))
		if err == nil {
			t.Errorf("%s: render succeeded, want fail-closed rejection", tc.name)
			continue
		}
		if tc.gate && !errors.Is(err, ErrUnsupportedPostgresPlan) {
			t.Errorf("%s: error = %v, want ErrUnsupportedPostgresPlan", tc.name, err)
		}
	}
}

func addTraversal(p *ir.PhysicalPlan, direction ir.PhysicalTraversalDirection, field string, strategy ir.PhysicalTraversalStrategy, indexFields []string) {
	p.BindVars["edge_collection"] = "fhir_edge"
	p.BindVars["edge_label"] = "l"
	p.BindVars["target_type"] = "Specimen"
	p.Operations = []ir.PhysicalOperation{p.Operations[0],
		{Kind: ir.PhysicalTraversalOp, Traversal: &ir.PhysicalTraversal{
			SourceVariable: "root", TargetVariable: "target", Direction: direction,
			EdgeCollectionBindKey: "edge_collection", EdgeLabelBindKey: "edge_label",
			TargetTypeBindKey: "target_type", EdgeTargetTypeField: field,
			Strategy: strategy, EndpointIndexFields: indexFields,
		}},
		p.Operations[1]}
	if strategy == ir.PhysicalTraversalEndpointLookup {
		traversal := p.Operations[1].Traversal
		traversal.EndpointField, traversal.EndpointJoinField = "_from", "_to"
	}
}

func TestRenderRejectsInvalidPlanStructurally(t *testing.T) {
	plan := basePlan()
	plan.Version = 0
	if _, err := RenderPhysicalPlan(plan, mustBinding(t)); err == nil || !strings.Contains(err.Error(), "validate physical plan") {
		t.Fatalf("invalid plan accepted: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Determinism and non-aliasing
// ---------------------------------------------------------------------------

func filteredPlan() ir.PhysicalPlan {
	plan := basePlan()
	plan.BindVars["status_value"] = "final"
	plan.Operations = append(plan.Operations[:1],
		ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"status"}},
			Right: &ir.PhysicalValue{BindKey: "status_value"},
		}}},
		ir.PhysicalOperation{Kind: ir.PhysicalLimitOp, Limit: &ir.PhysicalLimit{BindKey: "limit"}},
		basePlan().Operations[1])
	return plan
}

func TestRenderIsDeterministicAndDoesNotAliasPlanData(t *testing.T) {
	plan := filteredPlan()
	first := renderOK(t, plan)
	second := renderOK(t, plan)
	if first.SQL != second.SQL {
		t.Fatalf("repeated render changed SQL:\n%s\n%s", first.SQL, second.SQL)
	}
	if !reflect.DeepEqual(first.Args, second.Args) {
		t.Fatalf("repeated render changed args:\n%#v\n%#v", first.Args, second.Args)
	}

	// Freeze what the first render captured.
	capturedSQL, capturedArgs := first.SQL, first.Args
	capturedColumns := first.OutputColumns

	// Mutate every class of plan data the renderer read.
	plan.BindVars["project"] = "mutated"
	plan.BindVars["dataset_generation"] = "mutated"
	plan.BindVars["auth_resource_paths"].([]string)[0] = "MUTATED"
	plan.BindVars["root_collection"] = "Observation"
	plan.BindVars["limit"] = 999
	plan.BindVars["status_value"] = "mutated"

	if _, err := RenderPhysicalPlan(plan, mustBinding(t)); err != nil {
		t.Fatalf("render after mutation: %v", err)
	}
	if first.SQL != capturedSQL || !reflect.DeepEqual(first.Args, capturedArgs) || !reflect.DeepEqual(first.OutputColumns, capturedColumns) {
		t.Fatalf("rendered result aliased input plan data")
	}

	// A fresh, identical plan must re-render byte-identically to the first.
	third := renderOK(t, filteredPlan())
	if third.SQL != capturedSQL || !reflect.DeepEqual(third.Args, capturedArgs) {
		t.Fatalf("identical re-plan did not re-render identically:\n first %#v\n third %#v", first, third)
	}

	// Mutating the result slices must not affect later renders.
	first.Args[0].Value = "tampered"
	first.OutputColumns[0] = "tampered"
	fourth := renderOK(t, filteredPlan())
	if fourth.SQL != second.SQL || !reflect.DeepEqual(fourth.Args, second.Args) {
		t.Fatalf("result mutation leaked into the renderer")
	}
}

func TestRenderedResultCopiesConstructorSlices(t *testing.T) {
	args := []compiler.PostgreSQLParameter{{Type: compiler.PostgreSQLParameterText, Value: "x"}}
	columns := []string{"a"}
	rendered, err := compiler.NewRenderedPostgresPhysicalPlan(`SELECT $1`, args, columns)
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	args[0].Value = "mutated"
	columns[0] = "mutated"
	if rendered.Args[0].Value != "x" || rendered.OutputColumns[0] != "a" {
		t.Fatalf("constructor aliased caller slices: %#v %v", rendered.Args, rendered.OutputColumns)
	}
}

func TestRenderNotEqualsOnTypedColumnIsTwoValued(t *testing.T) {
	plan := basePlan()
	plan.BindVars["id_value"] = "x"
	plan.Operations = []ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "NOT_EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}},
			Right: &ir.PhysicalValue{BindKey: "id_value"},
		}}},
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
			{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
		}}},
	}
	rendered := renderOK(t, plan)
	if !strings.Contains(rendered.SQL, `r0."logical_id" IS DISTINCT FROM $5`) {
		t.Fatalf("NOT_EQUALS over a typed column must render IS DISTINCT FROM:\n%s", rendered.SQL)
	}
	if strings.Contains(rendered.SQL, `"x"`) {
		t.Fatalf("bind value leaked into SQL:\n%s", rendered.SQL)
	}
	if got := rendered.Args[4]; got.Type != compiler.PostgreSQLParameterText || got.Value != "x" {
		t.Fatalf("NOT_EQUALS column value arg = %#v", got)
	}
}

func TestRenderEmptyColumnINBindsEmptyArray(t *testing.T) {
	plan := basePlan()
	plan.BindVars["in_values"] = []string{}
	plan.Operations = []ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "IN", Left: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}},
			Right: &ir.PhysicalValue{BindKey: "in_values"},
		}}},
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
			{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
		}}},
	}
	rendered := renderOK(t, plan)
	if !strings.Contains(rendered.SQL, `r0."logical_id" = ANY (SELECT jsonb_array_elements_text($5::jsonb))`) {
		t.Fatalf("empty IN SQL:\n%s", rendered.SQL)
	}
	// JSON null would make jsonb_array_elements_text raise at execution; an
	// empty array makes ANY false, matching the AQL x IN [] reading.
	if got := rendered.Args[4]; got.Type != compiler.PostgreSQLParameterJSONB || string(got.Value.(json.RawMessage)) != `[]` {
		t.Fatalf("empty IN arg = %#v, want JSONB []", got)
	}
}

func TestRenderExistsMissingOverTypedColumnsUseNullTests(t *testing.T) {
	plan := basePlan()
	plan.Operations = []ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "EXISTS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}},
		}}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "MISSING", Left: ir.PhysicalValue{Variable: "root", Path: []string{"recorded_at"}},
		}}},
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
			{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
		}}},
	}
	rendered := renderOK(t, plan)
	if !strings.Contains(rendered.SQL, `r0."logical_id" IS NOT NULL`) || !strings.Contains(rendered.SQL, `r0."recorded_at" IS NULL`) {
		t.Fatalf("column EXISTS/MISSING must be null tests:\n%s", rendered.SQL)
	}
	if strings.Contains(rendered.SQL, "jsonb_typeof") {
		t.Fatalf("jsonb_typeof over a typed column cannot execute:\n%s", rendered.SQL)
	}
}

func TestRenderPayloadNullLiteralComparisonsUsePresenceForm(t *testing.T) {
	plan := basePlan()
	plan.BindVars["null_value"] = json.RawMessage("null")
	plan.Operations = []ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"status"}},
			Right: &ir.PhysicalValue{BindKey: "null_value"},
		}}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "NOT_EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"deceased"}},
			Right: &ir.PhysicalValue{BindKey: "null_value"},
		}}},
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
			{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
		}}},
	}
	rendered := renderOK(t, plan)
	// AQL reads missing and stored JSON null alike as null; the jsonb
	// equality would drop missing-key rows, so the null literal maps to the
	// null-or-missing presence test.
	if !strings.Contains(rendered.SQL, `COALESCE(jsonb_typeof(r0."payload" #> $5::text[]), 'null') = 'null'`) {
		t.Fatalf("EQUALS null literal must use the presence form:\n%s", rendered.SQL)
	}
	if !strings.Contains(rendered.SQL, `COALESCE(jsonb_typeof(r0."payload" #> $6::text[]), 'null') <> 'null'`) {
		t.Fatalf("NOT_EQUALS null literal must negate the presence form:\n%s", rendered.SQL)
	}
}

func TestRenderNotOverColumnComparisonStaysPlain(t *testing.T) {
	plan := basePlan()
	plan.BindVars["id_value"] = "x"
	plan.Operations = []ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Expression: &ir.PhysicalPredicateExpression{
			Kind: ir.PhysicalNotPredicate,
			Children: []ir.PhysicalPredicateExpression{
				{Kind: ir.PhysicalComparisonPredicate, Comparison: &ir.PhysicalPredicate{
					Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}},
					Right: &ir.PhysicalValue{BindKey: "id_value"},
				}},
			},
		}}},
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
			{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
		}}},
	}
	rendered := renderOK(t, plan)
	if !strings.Contains(rendered.SQL, `NOT (r0."logical_id" = $5)`) {
		t.Fatalf("NOT over a two-valued column child must stay plain:\n%s", rendered.SQL)
	}
	if strings.Contains(rendered.SQL, "COALESCE") {
		t.Fatalf("two-valued child must not be COALESCE-wrapped:\n%s", rendered.SQL)
	}
}
