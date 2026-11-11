package postgres

// pg-sql-20 tests for the authored finite graph path union registered in
// graph.go. Offline: golden SQL, ordered typed arguments, positional
// columns, determinism, hostile parameterization, and the full fail-closed
// refusal matrix with entry==dispatch message equality. Live (service-gated
// by LOOM_TEST_POSTGRES_URL, owned namespace verify_sql20_*, tracked in the
// TestMain delta audit composed in main_test.go): direct graph-path oracle
// tests on real PostgreSQL 18 with literal expected rows, scope controls
// over every node and edge, the limit+1 lookahead witness (behaviorally
// proving dedupe-before-limit), and EXPLAIN plan-time proofs (no Recursive
// node, Limit above the dedupe WindowAgg, REQUIRED-match semi-join inside
// the seed CTE). Failure output flows through svc.scrub; the service URL is
// never printed or embedded.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	pgstore "github.com/calypr/loom/internal/backends/postgres/control"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/source"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Offline helpers: structurally valid fixtures come from the real planner
// (lower.BuildGraphPhysicalPlan), so the renderer is exercised on exactly
// the plan shape production emits. Mutations are applied to copied plans to
// probe the closed surface.
// ---------------------------------------------------------------------------

const (
	graph20Project    = "pgsq20-proj"
	graph20Generation = "gen-1"
	graph20AuthPath   = "FHIR/Patient"
)

func graph20Context() semantic.ExecutionContext {
	return semantic.ExecutionContext{
		Project:           graph20Project,
		DatasetGeneration: graph20Generation,
		AuthResourcePaths: []string{graph20AuthPath},
	}
}

func graph20PlanFor(t *testing.T, root semantic.SemanticNode, limit int) ir.PhysicalPlan {
	t.Helper()
	plan, err := lower.BuildGraphPhysicalPlan(semantic.OutputPlan{Root: root}, graph20Context(), limit, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("BuildGraphPhysicalPlan: %v", err)
	}
	return plan
}

func graph20Root(children ...semantic.SemanticNode) semantic.SemanticNode {
	return semantic.SemanticNode{Alias: "root", ResourceType: "Patient", Children: children}
}

func graph20Condition(alias string, mode spec.TraversalMatchMode, children ...semantic.SemanticNode) semantic.SemanticNode {
	return semantic.SemanticNode{Alias: alias, ResourceType: "Condition", EdgeLabel: "subject_Patient", MatchMode: mode, Children: children}
}

func graph20Observation(alias string, mode spec.TraversalMatchMode) semantic.SemanticNode {
	return semantic.SemanticNode{Alias: alias, ResourceType: "Observation", EdgeLabel: "subject_Patient", MatchMode: mode}
}

func graph20Procedure(alias string, mode spec.TraversalMatchMode) semantic.SemanticNode {
	return semantic.SemanticNode{Alias: alias, ResourceType: "Procedure", EdgeLabel: "subject_Patient", MatchMode: mode}
}

// graph20FocusedObservation is the nested finite branch: Condition -[focus_Condition]-> Observation.
func graph20FocusedObservation(alias string, mode spec.TraversalMatchMode) semantic.SemanticNode {
	return semantic.SemanticNode{Alias: alias, ResourceType: "Observation", EdgeLabel: "focus_Condition", MatchMode: mode}
}

func graph20Binding(t *testing.T) ir.PostgresStorageBinding {
	t.Helper()
	binding, err := ir.NewPostgresStorageBinding("loom")
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding: %v", err)
	}
	return binding
}

func graph20Render(t *testing.T, plan ir.PhysicalPlan) command.RenderedPostgresPhysicalPlan {
	t.Helper()
	rendered, err := RenderGraphPhysicalPlan(plan, graph20Binding(t))
	if err != nil {
		t.Fatalf("RenderGraphPhysicalPlan: %v", err)
	}
	return rendered
}

// graph20ArgsValues projects the sealed ordered arguments onto comparable
// plain values (JSONB parameters normalize through their raw bytes).
func graph20ArgsValues(rendered command.RenderedPostgresPhysicalPlan) []any {
	values := make([]any, 0, len(rendered.Args))
	for _, parameter := range rendered.Args {
		switch value := parameter.Value.(type) {
		case json.RawMessage:
			values = append(values, string(value))
		default:
			values = append(values, value)
		}
	}
	return values
}

func graph20ParamTypes(rendered command.RenderedPostgresPhysicalPlan) []command.PostgreSQLParameterType {
	types := make([]command.PostgreSQLParameterType, 0, len(rendered.Args))
	for _, parameter := range rendered.Args {
		types = append(types, parameter.Type)
	}
	return types
}

func graph20ClonePlan(plan ir.PhysicalPlan) ir.PhysicalPlan {
	clone := plan
	clone.Operations = append([]ir.PhysicalOperation(nil), plan.Operations...)
	clone.BindVars = map[string]any{}
	for key, value := range plan.BindVars {
		clone.BindVars[key] = value
	}
	return clone
}

// ---------------------------------------------------------------------------
// Offline: golden contract of the closed slice, root-only optional shape,
// required one-hop, siblings, and the nested finite branch.
// ---------------------------------------------------------------------------

func TestGraph20RenderRootOnlyOptionalGolden(t *testing.T) {
	// All-optional tree: the planner keeps the root-only set and emits no
	// REQUIRED-match semi-join (lower/graph_plan.go:79-84,
	// graphRequiredSemiJoinRoot).
	plan := graph20PlanFor(t, graph20Root(graph20Procedure("proc", spec.TraversalMatchOptional)), 3)
	if last := plan.Operations[len(plan.Operations)-1].GraphReturn; len(last.PathSets) != 2 || last.PathSets[0] != "path_root" || last.PathSets[1] != "path_1" {
		t.Fatalf("planner PathSets = %v, want [path_root path_1]", last.PathSets)
	}
	rendered := graph20Render(t, plan)

	if !reflect.DeepEqual(rendered.OutputColumns, []string{"paths"}) {
		t.Fatalf("output columns = %v, want [paths]", rendered.OutputColumns)
	}
	wantTypes := []command.PostgreSQLParameterType{
		command.PostgreSQLParameterText, command.PostgreSQLParameterText, command.PostgreSQLParameterJSONB, // project, generation, auth paths
		command.PostgreSQLParameterText, command.PostgreSQLParameterText, // Patient, seed alias
		command.PostgreSQLParameterBigint,                                                                 // seed route order
		command.PostgreSQLParameterText, command.PostgreSQLParameterText, command.PostgreSQLParameterText, // label, reached type (shared with the node resourceType bind by the addParameter cache), terminal alias
		command.PostgreSQLParameterBigint, // extend route order
		command.PostgreSQLParameterBigint, // LIMIT limit+1
	}
	if got := graph20ParamTypes(rendered); !reflect.DeepEqual(got, wantTypes) {
		t.Fatalf("argument types =\n%v\nwant\n%v", got, wantTypes)
	}
	wantValues := []any{
		graph20Project, graph20Generation, `["FHIR/Patient"]`,
		"Patient", "root",
		int64(0),
		"subject_Patient", "Procedure", "proc",
		int64(1),
		int64(4), // lookahead: limit+1, never limit
	}
	if got := graph20ArgsValues(rendered); !reflect.DeepEqual(got, wantValues) {
		t.Fatalf("argument values =\n%v\nwant\n%v", got, wantValues)
	}

	// Golden statement. Storage identity strings (r0, e2, x2, path_*,
	// graph_paths, g_union, g_row) are renderer-owned; every value sits at
	// its $n position. The dedupe WindowAgg precedes ORDER BY and the
	// bound LIMIT, and the public projection is exactly the UNSET-ed
	// public fields (render/aql/graph_render.go:170 parity).
	const wantSQL = `WITH path_0 AS (SELECT jsonb_build_array(r0."resource_type", r0."logical_id") AS "path_identity", $6::bigint AS "route_order", $5::text AS "terminal_alias", jsonb_build_array(jsonb_build_object('alias', $5::text, 'resourceType', $4::text, 'id', r0."logical_id", 'key', r0."logical_id", 'payload', r0."payload")) AS "nodes", '[]'::jsonb AS "relationships", '[]'::jsonb AS "occurrence_trail", r0."resource_type" AS "resource_type", r0."logical_id" AS "logical_id" FROM "loom"."fhir_resource" AS r0 WHERE r0."project" = $1 AND r0."dataset_generation" = $2 AND r0."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb)) AND r0."resource_type" = $4), path_1 AS (SELECT p."path_identity" || jsonb_build_array(x1."resource_type", x1."logical_id", $7::text) AS "path_identity", $10::bigint AS "route_order", $9::text AS "terminal_alias", p."nodes" || jsonb_build_array(jsonb_build_object('alias', $9::text, 'resourceType', $8::text, 'id', x1."logical_id", 'key', x1."logical_id", 'payload', x1."payload")) AS "nodes", p."relationships" || jsonb_build_array(jsonb_build_object('alias', $9::text, 'label', $7::text, 'fromResourceType', $4::text, 'toResourceType', $8::text)) AS "relationships", p."occurrence_trail" || jsonb_build_array(e1."edge_seq") AS "occurrence_trail", x1."resource_type" AS "resource_type", x1."logical_id" AS "logical_id" FROM path_0 AS p INNER JOIN "loom"."fhir_edge_occurrence" AS e1 ON e1."project" = $1 AND e1."dataset_generation" = $2 AND e1."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb)) AND e1."label" = $7 AND e1."target_resource_type" = $4 AND e1."target_logical_id" = p."logical_id" AND e1."source_resource_type" = $8 INNER JOIN "loom"."fhir_resource" AS x1 ON x1."project" = $1 AND x1."dataset_generation" = $2 AND x1."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb)) AND x1."resource_type" = $8 AND x1."logical_id" = e1."source_logical_id"), graph_paths AS (SELECT *, ROW_NUMBER() OVER (PARTITION BY path_identity ORDER BY route_order ASC, occurrence_trail ASC) AS rn FROM (SELECT "path_identity", "route_order", "terminal_alias", "nodes", "relationships", "occurrence_trail" FROM path_0 UNION ALL SELECT "path_identity", "route_order", "terminal_alias", "nodes", "relationships", "occurrence_trail" FROM path_1) AS g_union) SELECT jsonb_build_object('terminalAlias', terminal_alias, 'nodes', nodes, 'relationships', relationships) AS "c0" FROM graph_paths AS g_row WHERE g_row.rn = 1 ORDER BY g_row.route_order ASC, g_row.path_identity::text COLLATE "C" ASC LIMIT $11`
	if rendered.SQL != wantSQL {
		t.Fatalf("golden SQL drift:\n got %s\nwant %s", rendered.SQL, wantSQL)
	}

	// Alive assertions over the golden's structural claims: root set
	// present, no semi-join in a required-free plan, dedupe before the
	// bound limit, renderer identity never public.
	for _, forbidden := range []string{"Recursive", "__loom_", "recursive"} {
		if strings.Contains(rendered.SQL, forbidden) {
			t.Fatalf("statement must never contain %q: %s", forbidden, rendered.SQL)
		}
	}
	limitIndex := strings.Index(rendered.SQL, " LIMIT $")
	dedupeIndex := strings.Index(rendered.SQL, "ROW_NUMBER() OVER (PARTITION BY path_identity")
	if !(dedupeIndex >= 0 && limitIndex > dedupeIndex) {
		t.Fatal("bound LIMIT must sit after the dedupe window (render/aql/graph_render.go:164-170 order)")
	}
}

func TestGraph20RenderRequiredOneHop(t *testing.T) {
	plan := graph20PlanFor(t, graph20Root(graph20Condition("cond", spec.TraversalMatchRequired)), 100)
	last := plan.Operations[len(plan.Operations)-1].GraphReturn
	if len(last.PathSets) != 1 || last.PathSets[0] != "path_1" {
		t.Fatalf("required tree must drop the root-only set, PathSets = %v", last.PathSets)
	}
	rendered := graph20Render(t, plan)

	// The REQUIRED match enters as a semi-join over the seed row, before
	// any expansion (lower/required_match.go:12-15 "root semi-join ...
	// before the root sort/window"; traversal.go renderRequiredMatchExists).
	seedBody := rendered.SQL
	end := strings.Index(seedBody, "), path_1 AS (")
	if end < 0 {
		t.Fatalf("seed CTE boundary missing:\n%s", rendered.SQL)
	}
	seedBody = seedBody[:end]
	if !strings.Contains(seedBody, "EXISTS (SELECT 1 FROM") {
		t.Fatalf("REQUIRED-match EXISTS semi-join missing from the seed CTE:\n%s", seedBody)
	}
	if strings.Index(rendered.SQL, "EXISTS (SELECT 1 FROM") > strings.Index(rendered.SQL, "), path_1 AS (") {
		t.Fatal("the semi-join must gate root membership inside path_0, never the expanded rows")
	}
	if values := graph20ArgsValues(rendered); values[len(values)-1] != int64(101) {
		t.Fatalf("lookahead argument must be limit+1 (101), got %v", values[len(values)-1])
	}
	// The reached target side is auth-scoped exactly like the root: same
	// canonical ANY-over-$3 conjunct, same label/endpoint bind chain.
	if count := strings.Count(rendered.SQL, `= ANY (SELECT jsonb_array_elements_text($3::jsonb))`); count != 5 {
		t.Fatalf("scope conjuncts = %d, want 5 (root, hop edge, hop target, semi-join edge, semi-join target)", count)
	}
}

func TestGraph20RenderCompositeRouteOrderAndUnionArms(t *testing.T) {
	// Sibling branches plus one nested finite branch: cond REQUIRED with a
	// nested focus_Condition Observation, and an optional direct
	// Observation sibling. The planner numbers routes DFS-order
	// (lower/graph_plan.go:85-90), the renderer binds those orders, and
	// the union arms follow PathSets order.
	plan := graph20PlanFor(t, graph20Root(
		graph20Condition("cond", spec.TraversalMatchRequired, graph20FocusedObservation("obs2", spec.TraversalMatchRequired)),
		graph20Observation("obs", spec.TraversalMatchOptional),
	), 5)
	last := plan.Operations[len(plan.Operations)-1].GraphReturn
	if !reflect.DeepEqual(last.PathSets, []string{"path_1", "path_2", "path_3"}) {
		t.Fatalf("PathSets = %v", last.PathSets)
	}
	rendered := graph20Render(t, plan)

	// Union arms exactly equal PathSets order over renderer-minted CTEs.
	unionStart := strings.Index(rendered.SQL, "FROM (SELECT \"path_identity\"")
	unionEnd := strings.Index(rendered.SQL, ") AS g_union")
	if unionStart < 0 || unionEnd < unionStart {
		t.Fatalf("union block missing:\n%s", rendered.SQL)
	}
	arms := strings.Split(rendered.SQL[unionStart:unionEnd], " UNION ALL ")
	if len(arms) != 3 {
		t.Fatalf("union arms = %d, want 3", len(arms))
	}
	for index, wantCTE := range []string{"FROM path_1", "FROM path_2", "FROM path_3"} {
		if !strings.HasSuffix(arms[index], wantCTE) {
			t.Fatalf("arm %d must read %s: %s", index, wantCTE, arms[index])
		}
	}

	// Bound route orders, in plan order: the seed set carries 0, then the
	// authored extends 1, 2, 3. A plan string never names a CTE or order:
	// route orders arrive as $n bigints only (the 6 value excluded by the
	// filter below is the limit+1 lookahead bind).
	var orders []int64
	for _, parameter := range rendered.Args {
		if parameter.Type == command.PostgreSQLParameterBigint && parameter.Value != int64(6) {
			orders = append(orders, parameter.Value.(int64))
		}
	}
	if !reflect.DeepEqual(orders, []int64{0, 1, 2, 3}) {
		t.Fatalf("bound bigints (route orders) = %v, want [0 1 2 3]", orders)
	}
	if strings.Contains(rendered.SQL, "path_2 AS") != strings.Contains(rendered.SQL, "path_3 AS") {
		t.Fatal("every declared extend set must materialize as its own CTE")
	}
	// Nested correlation: the focus hop reads the authored parent set
	// (the cond extend's CTE), not the sibling set. DFS numbering
	// (lower/graph_plan.go:85-90) makes the cond extend path_1 and its
	// nested focus extend path_2; derive the numbers from the plan.
	setNumber := map[string]int{}
	for _, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalPathSeedOp:
			setNumber[operation.PathSeed.Variable] = 0
		case ir.PhysicalPathExtendOp:
			setNumber[operation.PathExtend.Variable] = len(setNumber)
		}
	}
	nestedSet, nestedParent := "", -1
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalPathExtendOp && operation.PathExtend.Relationship.FromResourceType == "Condition" {
			nestedSet = operation.PathExtend.Variable
			nestedParent = setNumber[operation.PathExtend.SourceVariable]
		}
	}
	if nestedSet == "" || nestedParent < 0 {
		t.Fatal("composite fixture lost its nested focus extend")
	}
	nestedNumber := setNumber[nestedSet]
	nested := rendered.SQL[strings.Index(rendered.SQL, "path_"+strconv.Itoa(nestedNumber)+" AS ("):]
	// CTE numbering is consecutive (renderer-owned "path_"+index); cut the
	// slice at the exact next CTE or the dedupe CTE — "), path_" alone
	// would also match the "AS \"path_identity\"" projection text.
	if end := strings.Index(nested, "), path_"+strconv.Itoa(nestedNumber+1)+" AS ("); end >= 0 {
		nested = nested[:end]
	} else if end := strings.Index(nested, "), graph_paths AS ("); end >= 0 {
		nested = nested[:end]
	}
	if wantParent := " FROM path_" + strconv.Itoa(nestedParent) + " AS p"; !strings.Contains(nested, wantParent) {
		t.Fatalf("nested extend must correlate to its authored parent set path_%d:\n%s", nestedParent, nested)
	}
	// The nested hop carries its own canonical edge+target scope (its own
	// edge-side and target-side conjunct chains), independent of the
	// parent's — scope applies to every node and edge of every route.
	if strings.Count(nested, `= ANY (SELECT jsonb_array_elements_text($3::jsonb))`) != 2 {
		t.Fatalf("nested extend must carry the edge and target scope chains:\n%s", nested)
	}
}

func TestGraph20RenderDeterminismAndHostileParameterization(t *testing.T) {
	root := graph20Root(graph20Condition("cond", spec.TraversalMatchRequired))
	first := graph20Render(t, graph20PlanFor(t, root, 7))
	second := graph20Render(t, graph20PlanFor(t, root, 7))
	if first.SQL != second.SQL {
		t.Fatal("rendering is not byte-stable across runs")
	}
	if !reflect.DeepEqual(graph20ArgsValues(first), graph20ArgsValues(second)) {
		t.Fatal("arguments are not value-stable across runs")
	}

	// Hostile scope strings cross only the $n boundary. Payload content is
	// read from storage columns and never crosses at all.
	hostileContext := semantic.ExecutionContext{
		Project:           `p'gsq20"proj; DROP`,
		DatasetGeneration: "gen-$1--x",
		AuthResourcePaths: []string{`FHIR/Weird "Path" /*`, "FHIR/日本語"},
	}
	plan, err := lower.BuildGraphPhysicalPlan(semantic.OutputPlan{Root: root}, hostileContext, 2, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("hostile BuildGraphPhysicalPlan: %v", err)
	}
	rendered := graph20Render(t, plan)
	for _, hostile := range []string{`p'gsq20"proj; DROP`, "gen-$1--x", `FHIR/Weird "Path" /*`, "FHIR/日本語"} {
		if strings.Contains(rendered.SQL, hostile) {
			t.Fatalf("hostile value %q leaked into SQL text", hostile)
		}
	}
	found := map[string]bool{}
	for _, parameter := range rendered.Args {
		if text, ok := parameter.Value.(string); ok {
			found[text] = true
		}
		if raw, ok := parameter.Value.(json.RawMessage); ok && strings.Contains(string(raw), "日本語") {
			found["jsonb-unicode"] = true
		}
	}
	for _, hostile := range []string{`p'gsq20"proj; DROP`, "gen-$1--x", "jsonb-unicode"} {
		if !found[hostile] {
			t.Fatalf("hostile value %q must arrive as an ordered argument", hostile)
		}
	}
	// No AQL bind syntax survives the seal (command.NewRenderedPostgresPhysical-
	// Plan enforces this at construction; assert the renderer emits none).
	if regexp.MustCompile(`@[A-Za-z_]+`).MatchString(rendered.SQL) {
		t.Fatalf("AQL bind syntax in statement: %s", rendered.SQL)
	}
}

func TestGraph20CanonicalDenyReadsZeroRows(t *testing.T) {
	// scope_allowed bound false is the repository-wide canonical deny:
	// the shared structural scope chain closes every row read with a
	// literal zero-row conjunct (scope.go), including all path CTEs.
	plan := graph20PlanFor(t, graph20Root(graph20Observation("obs", spec.TraversalMatchOptional)), 4)
	plan.BindVars["scope_allowed"] = false
	rendered := graph20Render(t, plan)
	if count := strings.Count(rendered.SQL, " AND FALSE"); count == 0 {
		t.Fatalf("canonical deny must close the scope chains with AND FALSE:\n%s", rendered.SQL)
	}
}

// ---------------------------------------------------------------------------
// Offline: the closed-surface refusal matrix. Every refusal is the typed
// capability-gap error, yields no SQL, and carries the same message through
// both doors (RenderPhysicalPlan delegation and RenderGraphPhysicalPlan).
// ---------------------------------------------------------------------------

func graph20Refusal(t *testing.T, plan ir.PhysicalPlan, wantContains string) {
	t.Helper()
	_, entryErr := RenderGraphPhysicalPlan(plan, graph20Binding(t))
	_, dispatchErr := RenderPhysicalPlan(plan, graph20Binding(t))
	if entryErr == nil {
		t.Fatalf("plan accepted, want refusal containing %q", wantContains)
	}
	if dispatchErr == nil || dispatchErr.Error() != entryErr.Error() {
		t.Fatalf("entry==dispatch equality broken:\nentry:    %v\ndispatch: %v", entryErr, dispatchErr)
	}
	if !strings.Contains(entryErr.Error(), wantContains) {
		t.Fatalf("refusal %q does not contain %q", entryErr.Error(), wantContains)
	}
}

func graph20FindOp(t *testing.T, plan ir.PhysicalPlan, kind ir.PhysicalOperationKind) int {
	t.Helper()
	for index, operation := range plan.Operations {
		if operation.Kind == kind {
			return index
		}
	}
	t.Fatalf("plan has no %s operation", kind)
	return -1
}

func graph20RequiredOneHopPlan(t *testing.T) ir.PhysicalPlan {
	return graph20PlanFor(t, graph20Root(graph20Condition("cond", spec.TraversalMatchRequired)), 10)
}

func TestGraph20RefusalMatrix(t *testing.T) {
	base := graph20RequiredOneHopPlan(t)

	t.Run("SecondPathSeed", func(t *testing.T) {
		plan := graph20ClonePlan(base)
		seed := plan.Operations[graph20FindOp(t, plan, ir.PhysicalPathSeedOp)]
		ops := append(append([]ir.PhysicalOperation{}, plan.Operations[:len(plan.Operations)-1]...), seed)
		plan.Operations = append(ops, plan.Operations[len(plan.Operations)-1])
		graph20Refusal(t, plan, "second PATH_SEED")
	})
	t.Run("PathExtendBeforeSeed", func(t *testing.T) {
		plan := graph20ClonePlan(base)
		seedIdx, extendIdx := graph20FindOp(t, plan, ir.PhysicalPathSeedOp), graph20FindOp(t, plan, ir.PhysicalPathExtendOp)
		ops := plan.Operations
		ops[seedIdx], ops[extendIdx] = ops[extendIdx], ops[seedIdx]
		graph20Refusal(t, plan, "PATH_EXTEND before PATH_SEED")
	})
	t.Run("NonEmptySourcePath", func(t *testing.T) {
		plan := graph20ClonePlan(base)
		extendIdx := graph20FindOp(t, plan, ir.PhysicalPathExtendOp)
		extend := *plan.Operations[extendIdx].PathExtend
		extend.SourcePath = []string{"terminal"}
		plan.Operations[extendIdx].PathExtend = &extend
		graph20Refusal(t, plan, "non-empty SourcePath")
	})
	t.Run("ExtendNodeMustReadTarget", func(t *testing.T) {
		plan := graph20ClonePlan(base)
		extendIdx := graph20FindOp(t, plan, ir.PhysicalPathExtendOp)
		extend := *plan.Operations[extendIdx].PathExtend
		extend.Node.Value = ir.PhysicalValue{Variable: extend.Traversal.TargetVariable, Path: []string{"payload"}}
		plan.Operations[extendIdx].PathExtend = &extend
		graph20Refusal(t, plan, "must read the reached target row itself")
	})
	t.Run("SeedNodeMustReadRoot", func(t *testing.T) {
		plan := graph20ClonePlan(base)
		seedIdx := graph20FindOp(t, plan, ir.PhysicalPathSeedOp)
		seed := *plan.Operations[seedIdx].PathSeed
		seed.Node.Value = ir.PhysicalValue{BindKey: "sneaky"}
		plan.Operations[seedIdx].PathSeed = &seed
		graph20Refusal(t, plan, "seed node must read the root row itself")
	})
	t.Run("ExtendFilterOnEdgeVariable", func(t *testing.T) {
		plan := graph20ClonePlan(base)
		extendIdx := graph20FindOp(t, plan, ir.PhysicalPathExtendOp)
		extend := *plan.Operations[extendIdx].PathExtend
		// Appended after the canonical block — the only position the
		// surface admits a plan-authored FILTER.
		scope := append(append([]ir.PhysicalOperation(nil), extend.Scope...),
			ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{
				Expression: &ir.PhysicalPredicateExpression{Kind: ir.PhysicalComparisonPredicate, Comparison: &ir.PhysicalPredicate{
					Operator: "EQUALS",
					LeftExpression: &ir.PhysicalExpression{Kind: ir.PhysicalExtractExpression, Extract: &ir.PhysicalExtract{
						Source: ir.PhysicalValue{Variable: extend.Traversal.EdgeVariable, Path: []string{"payload"}},
					}},
					Right: &ir.PhysicalValue{BindKey: "sneaky"},
				}},
			}})
		extend.Scope = scope
		plan.Operations[extendIdx].PathExtend = &extend
		graph20Refusal(t, plan, "only read the reached target's payload")
	})
	t.Run("ExtendFilterBarePredicateForm", func(t *testing.T) {
		plan := graph20ClonePlan(base)
		extendIdx := graph20FindOp(t, plan, ir.PhysicalPathExtendOp)
		extend := *plan.Operations[extendIdx].PathExtend
		// Appended after the canonical block: a bare-Predicate filter is
		// never the planner's extend-filter form (graph_plan.go:313-315).
		scope := append(append([]ir.PhysicalOperation(nil), extend.Scope...),
			ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator: "EQUALS", Left: ir.PhysicalValue{Variable: extend.Traversal.TargetVariable, Path: []string{"status"}},
				Right: &ir.PhysicalValue{BindKey: "sneaky"},
			}}})
		extend.Scope = scope
		plan.Operations[extendIdx].PathExtend = &extend
		graph20Refusal(t, plan, "comparison-expression form")
	})
	t.Run("CorrelatedPathFilter", func(t *testing.T) {
		valueKind := spec.FilterCode
		// The correlated filter rides an OPTIONAL node so lowering routes
		// it through appendGraphPathFilters (graph_plan.go:259-278), the
		// producer that preserves Correlation on the comparison. Fixture
		// shape per spec/correlated_selection_test.go: a SYSTEM_VERSION
		// selection carries a system but no code.
		child := graph20Procedure("proc", spec.TraversalMatchOptional)
		child.Filters = []spec.TypedFilter{{
			FieldRef: "Condition.code", Selector: "code.coding[].code", FieldKind: valueKind,
			Repeated: true, Quantifier: spec.QuantifierAny, Operator: spec.FilterEquals,
			Values: []spec.FilterValue{{Kind: valueKind, Code: &spec.CodeValue{System: "http://loinc.org", Code: "systolic"}}},
			Correlation: &fhirschema.CorrelatedBinding{
				KeyPath: "code.coding[]", SystemPath: "system", CodePath: "code",
				ValueScope:     fhirschema.CorrelatedValueScopeCoding,
				SelectionScope: fhirschema.CorrelatedSelectionScopeExact,
				ValuePath:      "display", LogicalType: "string",
			},
		}}
		_, err := RenderGraphPhysicalPlan(graph20PlanFor(t, graph20Root(child), 5), graph20Binding(t))
		if err == nil || !strings.Contains(err.Error(), "correlated CODE path filters") {
			t.Fatalf("correlated extend filter must fail closed, got %v", err)
		}
	})
	t.Run("ExtraDerivedLetInExtendScope", func(t *testing.T) {
		plan := graph20ClonePlan(base)
		extendIdx := graph20FindOp(t, plan, ir.PhysicalPathExtendOp)
		extend := *plan.Operations[extendIdx].PathExtend
		extend.Scope = append(append([]ir.PhysicalOperation(nil), extend.Scope...), ir.PhysicalOperation{
			Kind:       ir.PhysicalDerivedLetOp,
			DerivedLet: &ir.PhysicalDerivedLet{Variable: "sneaky", Operator: "LENGTH", Inputs: []ir.PhysicalValue{{Variable: extend.Traversal.TargetVariable}}},
		})
		plan.Operations[extendIdx].PathExtend = &extend
		graph20Refusal(t, plan, "only the canonical AUTH_RESOURCE_PATH_ALLOWED LET")
	})
	t.Run("TraversalInsideGraphSlice", func(t *testing.T) {
		plan := graph20ClonePlan(base)
		traversal := plan.Operations[graph20FindOp(t, plan, ir.PhysicalPathExtendOp)].PathExtend.Traversal
		ops := append([]ir.PhysicalOperation{}, plan.Operations[:len(plan.Operations)-1]...)
		ops = append(ops, ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Traversal: &traversal})
		plan.Operations = append(ops, plan.Operations[len(plan.Operations)-1])
		graph20Refusal(t, plan, "is not part of the closed graph slice")
	})
	t.Run("ExtendOnUnknownSourceSet", func(t *testing.T) {
		plan := graph20ClonePlan(base)
		extendIdx := graph20FindOp(t, plan, ir.PhysicalPathExtendOp)
		extend := *plan.Operations[extendIdx].PathExtend
		extend.SourceVariable = "path_99"
		plan.Operations[extendIdx].PathExtend = &extend
		graph20Refusal(t, plan, "not a set defined by this plan")
	})
	t.Run("PathSetsNotAscending", func(t *testing.T) {
		plan := graph20PlanFor(t, graph20Root(
			graph20Condition("cond", spec.TraversalMatchRequired),
			graph20Observation("obs", spec.TraversalMatchRequired),
		), 5)
		last := *plan.Operations[len(plan.Operations)-1].GraphReturn
		last.PathSets = []string{"path_2", "path_1"}
		plan.Operations[len(plan.Operations)-1].GraphReturn = &last
		graph20Refusal(t, plan, "not route-order ascending")
	})
	t.Run("PathSetsUnknownSet", func(t *testing.T) {
		plan := graph20ClonePlan(base)
		last := *plan.Operations[len(plan.Operations)-1].GraphReturn
		last.PathSets = []string{"path_1", "path_7"}
		plan.Operations[len(plan.Operations)-1].GraphReturn = &last
		graph20Refusal(t, plan, "not a set defined by this plan")
	})
	t.Run("GraphReturnNotFinal", func(t *testing.T) {
		plan := graph20ClonePlan(base)
		plan.Operations = append(plan.Operations, ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS"}}})
		graph20Refusal(t, plan, "GRAPH_RETURN as the final operation")
	})
	t.Run("SecondGraphReturn", func(t *testing.T) {
		plan := graph20ClonePlan(base)
		last := plan.Operations[len(plan.Operations)-1]
		plan.Operations = append(plan.Operations[:len(plan.Operations)-1], last, last)
		graph20Refusal(t, plan, "is not part of the closed graph slice")
	})
	t.Run("AnyDirection", func(t *testing.T) {
		plan := graph20ClonePlan(base)
		extendIdx := graph20FindOp(t, plan, ir.PhysicalPathExtendOp)
		extend := *plan.Operations[extendIdx].PathExtend
		extend.Traversal.Direction = ir.PhysicalAny
		plan.Operations[extendIdx].PathExtend = &extend
		graph20Refusal(t, plan, "ANY-direction traversal has no proven PostgreSQL contract")
	})
	t.Run("EndpointLookupStrategyNormalizesToRegisteredJoin", func(t *testing.T) {
		// The optimizer enables endpoint_traversal by default
		// (ir/policy.go:60-62), so planner output reaches the graph
		// renderer under this strategy; the AQL endpoint arm
		// (render/aql/graph_render.go:115-124) and the native arm (:125-131)
		// select the same edge set on the flat edge relation, so the
		// strategy label must not change the registered PostgreSQL join.
		plan := graph20ClonePlan(base)
		extendIdx := graph20FindOp(t, plan, ir.PhysicalPathExtendOp)
		extend := *plan.Operations[extendIdx].PathExtend
		extend.Traversal.Strategy = ir.PhysicalTraversalEndpointLookup
		extend.Traversal.EndpointField = "subject"
		extend.Traversal.EndpointJoinField = "subject"
		extend.Traversal.EndpointIndexFields = []string{"subject"}
		plan.Operations[extendIdx].PathExtend = &extend
		native := graph20Render(t, base).SQL
		labelled := graph20Render(t, plan).SQL
		if native != labelled {
			t.Fatalf("endpoint-lookup strategy must not alter the registered join:\nnative:   %s\nlabelled: %s", native, labelled)
		}
	})
	t.Run("EndpointLookupTruncatedPayloadRefuses", func(t *testing.T) {
		plan := graph20ClonePlan(base)
		extendIdx := graph20FindOp(t, plan, ir.PhysicalPathExtendOp)
		extend := *plan.Operations[extendIdx].PathExtend
		extend.Traversal.Strategy = ir.PhysicalTraversalEndpointLookup
		extend.Traversal.EndpointField = ""
		plan.Operations[extendIdx].PathExtend = &extend
		graph20Refusal(t, plan, "endpoint-lookup traversal payload is incomplete")
	})
	t.Run("UnknownStrategyStillRefused", func(t *testing.T) {
		plan := graph20ClonePlan(base)
		extendIdx := graph20FindOp(t, plan, ir.PhysicalPathExtendOp)
		extend := *plan.Operations[extendIdx].PathExtend
		extend.Traversal.Strategy = ir.PhysicalTraversalStrategy("wild")
		plan.Operations[extendIdx].PathExtend = &extend
		graph20Refusal(t, plan, "unsupported traversal strategy")
	})
	t.Run("NodeTypeMismatchFailsClosed", func(t *testing.T) {
		plan := graph20ClonePlan(base)
		extendIdx := graph20FindOp(t, plan, ir.PhysicalPathExtendOp)
		extend := *plan.Operations[extendIdx].PathExtend
		extend.Node.ResourceType = "Observation" // traversal target bind says Condition
		plan.Operations[extendIdx].PathExtend = &extend
		graph20Refusal(t, plan, "does not match the traversal target type bind")
	})
	t.Run("RelationshipLabelBindMismatchFailsClosed", func(t *testing.T) {
		// The AQL twin filters edges on EdgeLabelBindKey but renders the
		// public label and dedupe identity from Relationship.LabelBindKey
		// (render/aql/graph_render.go:119/:128 vs :144-145); the planner
		// always sets one key for both (lower/graph_plan.go:126), so a
		// hand-diverged plan must fail closed rather than read one bind
		// where the twin reads the other.
		plan := graph20ClonePlan(base)
		extendIdx := graph20FindOp(t, plan, ir.PhysicalPathExtendOp)
		extend := *plan.Operations[extendIdx].PathExtend
		relationship := extend.Relationship
		relationship.LabelBindKey = "sneaky_label_bind"
		plan.BindVars["sneaky_label_bind"] = "totally_different"
		extend.Relationship = relationship
		plan.Operations[extendIdx].PathExtend = &extend
		graph20Refusal(t, plan, "does not equal the traversal edge label bind")
	})
	t.Run("SeedRouteOrderNotZero", func(t *testing.T) {
		plan := graph20ClonePlan(base)
		seedIdx := graph20FindOp(t, plan, ir.PhysicalPathSeedOp)
		seed := *plan.Operations[seedIdx].PathSeed
		seed.RouteOrder = 7
		plan.Operations[seedIdx].PathSeed = &seed
		graph20Refusal(t, plan, "seed route order 7 is not 0")
	})
	t.Run("BrokenChainType", func(t *testing.T) {
		plan := graph20ClonePlan(base)
		extendIdx := graph20FindOp(t, plan, ir.PhysicalPathExtendOp)
		extend := *plan.Operations[extendIdx].PathExtend
		extend.Relationship.FromResourceType = "AllergyIntolerance"
		plan.Operations[extendIdx].PathExtend = &extend
		graph20Refusal(t, plan, "does not continue the authored chain")
	})
	t.Run("MissingAuthModeBind", func(t *testing.T) {
		// A missing plan bind is refused by plan validation at the door
		// (bind completeness precedes loadScope's typed scope checks,
		// which govern present-but-wrong values); the contract here is
		// fail-closed refusal with entry==dispatch equality.
		plan := graph20ClonePlan(base)
		delete(plan.BindVars, "auth_resource_paths_unrestricted")
		graph20Refusal(t, plan, "auth_resource_paths_unrestricted")
	})
	t.Run("ClickHouseEngine", func(t *testing.T) {
		plan := graph20ClonePlan(base)
		plan.Engine = ir.PhysicalEngineClickHouse
		// The door's own engine gate is defense in depth: plan.Validate
		// already refuses a ClickHouse-engined plan at the same door with
		// its typed message; either typed refusal is the contract.
		graph20Refusal(t, plan, "ClickHouse")
	})
	t.Run("StageSequence", func(t *testing.T) {
		plan := graph20ClonePlan(base)
		plan.StageSequence = &ir.PhysicalStageSequence{}
		graph20Refusal(t, plan, "stage")
	})
	t.Run("DeferredLets", func(t *testing.T) {
		plan := graph20ClonePlan(base)
		plan.DeferredExpressionLets = []ir.PhysicalOperation{{Kind: ir.PhysicalExpressionLetOp, ExpressionLet: &ir.PhysicalExpressionLet{Variable: "later"}}}
		graph20Refusal(t, plan, "deferred expression lets")
	})
	t.Run("RootRegionNonCanonicalLet", func(t *testing.T) {
		plan := graph20ClonePlan(base)
		ops := append([]ir.PhysicalOperation{}, plan.Operations[:1]...)
		ops = append(ops, ir.PhysicalOperation{Kind: ir.PhysicalDerivedLetOp, DerivedLet: &ir.PhysicalDerivedLet{Variable: "root_hack", Operator: "LENGTH", Inputs: []ir.PhysicalValue{{Variable: "root"}}}})
		ops = append(ops, plan.Operations[1:]...)
		plan.Operations = ops
		graph20Refusal(t, plan, "not a canonical scope operation")
	})
}

func TestGraph20SpineDispatchUntouched(t *testing.T) {
	// A non-graph plan still meets the spine's default-arm refusal with the
	// merged message: the single dispatch hunk cannot reroute spine plans.
	// A legitimately-shaped non-graph plan (root scan, non-canonical
	// derived let, return — the shape root_scan_test.go builds) passes
	// plan validation and meets the default-arm refusal at
	// render.go:390 for the non-canonical DERIVED_LET.
	plan := ir.PhysicalPlan{
		Version: 1,
		BindVars: map[string]any{
			"project": "p", "dataset_generation": "gen-1",
			"auth_resource_paths":              []string{"FHIR/Patient"},
			"auth_resource_paths_unrestricted": false, "scope_allowed": true,
			"root_collection": "fhir_resource",
		},
		Operations: []ir.PhysicalOperation{
			{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
			{Kind: ir.PhysicalDerivedLetOp, DerivedLet: &ir.PhysicalDerivedLet{Variable: "root_len", Operator: "LENGTH", Inputs: []ir.PhysicalValue{{Variable: "root", Path: []string{"payload"}}}}},
			{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}}}}},
		},
	}
	_, err := RenderPhysicalPlan(plan, graph20Binding(t))
	if !errors.Is(err, ErrUnsupportedPostgresPlan) || !strings.Contains(err.Error(), `operation "DERIVED_LET" is not part of the v1 PostgreSQL render surface`) {
		t.Fatalf("spine default-arm refusal drifted: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Live acceptance (never skipped inside the granted window; the sibling
// discipline is that the delivery battery always runs with the service, and
// a missing service is a blocker, never a pass). Owned namespace:
// verify_sql20_*; tracked in the TestMain delta audit.
// ---------------------------------------------------------------------------

var (
	graph20FixturesMu      sync.Mutex
	graph20TrackedFixtures = map[string]bool{}
)

func graph20LiveService(t *testing.T) *pgir02Service {
	t.Helper()
	svc := newOwnedService(t, 0, "verify_sql20_it")
	graph20TrackFixtures(t, svc)
	return svc
}

func graph20TrackFixtures(t *testing.T, svc *pgir02Service) {
	t.Helper()
	var database string
	if err := svc.iso.QueryRow(svc.ctx, "SELECT current_database()").Scan(&database); err != nil {
		svc.fail("track owned database", err)
	}
	graph20FixturesMu.Lock()
	defer graph20FixturesMu.Unlock()
	graph20TrackedFixtures[database] = true
	graph20TrackedFixtures[svc.schema] = true
}

var graph20OwnedNamePattern = regexp.MustCompile(`^verify_sql20_it_[0-9]+_[0-9a-f]+$`)

func graph20RunDeltaAudit(inner func() int) int {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		return inner()
	}
	beforeList, err := graph20SnapshotOwnedResources(context.Background(), raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pg-sql-20 delta audit: snapshot before run: %v\n", err)
		return 1
	}
	before := make(map[string]bool, len(beforeList))
	for _, name := range beforeList {
		before[name] = true
	}
	code := inner()
	after, err := graph20SnapshotOwnedResources(context.Background(), raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pg-sql-20 delta audit: snapshot after run: %v\n", err)
		code = 1
	}
	graph20FixturesMu.Lock()
	defer graph20FixturesMu.Unlock()
	for _, name := range after {
		if before[name] {
			continue
		}
		if graph20TrackedFixtures[name] {
			fmt.Fprintf(os.Stderr, "pg-sql-20 delta audit: %q was created and tracked by this run and still survives\n", name)
			code = 1
			continue
		}
		fmt.Fprintf(os.Stderr, "pg-sql-20 delta audit: note — %q was born during this run but is not owned by it (sibling lane?)\n", name)
	}
	return code
}

func graph20SnapshotOwnedResources(ctx context.Context, raw string) ([]string, error) {
	pool, err := pgxpool.New(ctx, raw)
	if err != nil {
		return nil, err
	}
	defer pool.Close()
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var names []string
	for _, query := range []string{
		`SELECT datname FROM pg_database WHERE datname LIKE 'verify\_sql20\_%' ESCAPE '\'`,
		`SELECT nspname FROM pg_namespace WHERE nspname LIKE 'verify\_sql20\_%' ESCAPE '\'`,
	} {
		rows, err := pool.Query(queryCtx, query)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return nil, err
			}
			names = append(names, name)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return names, nil
}

// graph20Seed mirrors the sibling seeding path: literal rows through the
// real store repositories, so stored identity/auth facts are the same the
// production writers produce.
func graph20SeedResource(t *testing.T, svc *pgir02Service, project, generation, resourceType, logicalID, authPath string, payload json.RawMessage) {
	t.Helper()
	repo, err := pgstore.NewResourceRepository(svc.client, svc.schema)
	if err != nil {
		t.Fatalf("resource repository: %v", err)
	}
	if _, err := repo.InsertResource(svc.ctx, source.Resource{
		Project: project, DatasetGeneration: generation, ResourceType: resourceType,
		LogicalID: logicalID, AuthResourcePath: authPath, Payload: payload,
	}); err != nil {
		svc.fail("insert resource "+resourceType+"/"+logicalID, err)
	}
}

// graph20SeedEdge writes one occurrence at source_path "graph20"; the
// occurrence identity (migrations 000004: project, generation, endpoints,
// label, source_path, source_ordinal) admits parallel duplicates only at
// distinct source ordinals, which the dedupe witness needs.
func graph20SeedEdge(t *testing.T, svc *pgir02Service, project, generation, edgeAuth, label, sourceType, sourceID, targetType, targetID string, ordinalOverrides ...int64) int64 {
	t.Helper()
	repo, err := pgstore.NewEdgeRepository(svc.client, svc.schema)
	if err != nil {
		t.Fatalf("edge repository: %v", err)
	}
	ordinal := int64(0)
	if len(ordinalOverrides) > 0 {
		ordinal = ordinalOverrides[0]
	}
	seq, err := repo.InsertEdge(svc.ctx, pgstore.EdgeOccurrenceRow{
		Project: project, DatasetGeneration: generation, AuthResourcePath: edgeAuth,
		Label: label, SourceResourceType: sourceType, SourceLogicalID: sourceID,
		TargetResourceType: targetType, TargetLogicalID: targetID,
		SourcePath: "graph20", SourceOrdinal: ordinal,
	})
	if err != nil {
		svc.fail("insert edge "+sourceID+"->"+targetID+" @"+label, err)
	}
	return seq
}

// graph20RenderIn renders against the owned service's schema — the live
// statements execute where the fixtures were written (bindingFor is the
// sibling helper from render_pg18_test.go); the "loom" graph20Binding is
// the offline shape-only binding.
func graph20RenderIn(t *testing.T, svc *pgir02Service, plan ir.PhysicalPlan) command.RenderedPostgresPhysicalPlan {
	t.Helper()
	rendered, err := RenderGraphPhysicalPlan(plan, bindingFor(t, svc.schema))
	if err != nil {
		svc.fail("RenderGraphPhysicalPlan", err)
	}
	return rendered
}

func graph20ExecPaths(t *testing.T, svc *pgir02Service, rendered command.RenderedPostgresPhysicalPlan) []map[string]any {
	t.Helper()
	args := make([]any, len(rendered.Args))
	for index, parameter := range rendered.Args {
		args[index] = parameter.Value
	}
	rows, err := svc.iso.Query(svc.ctx, rendered.SQL, args...)
	if err != nil {
		svc.fail("execute graph statement "+svc.scrub(rendered.SQL), err)
	}
	defer rows.Close()
	var paths []map[string]any
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			svc.fail("scan path object", err)
		}
		var object map[string]any
		if err := json.Unmarshal(raw, &object); err != nil {
			svc.fail("decode path object "+svc.scrub(string(raw)), err)
		}
		paths = append(paths, object)
	}
	if err := rows.Err(); err != nil {
		svc.fail("drain path rows", err)
	}
	return paths
}

// graph20Path is the public path object shape after the AQL-parity UNSET:
// {terminalAlias, nodes[{alias,resourceType,id,key,payload}],
// relationships[{alias,label,fromResourceType,toResourceType}]}.
func graph20Path(terminal string, nodes []map[string]any, relationships []map[string]any) map[string]any {
	object := map[string]any{"terminalAlias": terminal, "nodes": nodes, "relationships": relationships}
	if relationships == nil {
		object["relationships"] = []map[string]any{}
	}
	return object
}

func graph20Node(alias, resourceType, id, key string, payload string) map[string]any {
	var decoded any
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		panic(err)
	}
	return map[string]any{"alias": alias, "resourceType": resourceType, "id": id, "key": key, "payload": decoded}
}

func graph20Relationship(alias, label, fromType, toType string) map[string]any {
	return map[string]any{"alias": alias, "label": label, "fromResourceType": fromType, "toResourceType": toType}
}

func graph20AssertPaths(t *testing.T, got []map[string]any, want []map[string]any) {
	t.Helper()
	if len(got) != len(want) {
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		t.Fatalf("path sequence mismatch\n got %d rows: %s\nwant %d rows: %s", len(got), gotJSON, len(want), wantJSON)
	}
	// The live rows arrive through encoding/json ([]interface{} of
	// map[string]interface{}); hand-written wants carry typed slices. A
	// round-trip puts both on the same generic footing before DeepEqual.
	normalizedWant := make([]map[string]any, len(want))
	{
		encoded, err := json.Marshal(want)
		if err != nil {
			t.Fatalf("encode expected paths: %v", err)
		}
		if err := json.Unmarshal(encoded, &normalizedWant); err != nil {
			t.Fatalf("decode expected paths: %v", err)
		}
	}
	for index := range normalizedWant {
		if !reflect.DeepEqual(got[index], normalizedWant[index]) {
			gotJSON, _ := json.Marshal(got[index])
			wantJSON, _ := json.Marshal(normalizedWant[index])
			t.Fatalf("path %d mismatch\n got %s\nwant %s", index, gotJSON, wantJSON)
		}
	}
}

// graph20AcceptanceTree is the ticket fixture: root-only optional handled
// as its own query, and in the composite query a required one-hop, an
// optional miss, sibling branches, and one nested finite branch, with
// parallel duplicate edges on the required hop to prove the dedupe.
func TestGraph20LivePathUnionOracle(t *testing.T) {
	svc := graph20LiveService(t)
	ctx := svc.ctx

	// Roots and documents. p1 carries the full subtree; p2 has no
	// Condition (required exclusion + optional miss); p3 has nothing.
	// Parallel duplicate edges p1→c1 exist solely for the dedupe witness.
	graph20SeedResource(t, svc, graph20Project, graph20Generation, "Patient", "p1", graph20AuthPath, json.RawMessage(`{"resourceType":"Patient","id":"p1","gender":"female"}`))
	graph20SeedResource(t, svc, graph20Project, graph20Generation, "Patient", "p2", graph20AuthPath, json.RawMessage(`{"resourceType":"Patient","id":"p2"}`))
	graph20SeedResource(t, svc, graph20Project, graph20Generation, "Patient", "p3", graph20AuthPath, json.RawMessage(`{"resourceType":"Patient","id":"p3"}`))
	graph20SeedResource(t, svc, graph20Project, graph20Generation, "Condition", "c1", "FHIR/Condition", json.RawMessage(`{"resourceType":"Condition","id":"c1","clinicalStatus":"active"}`))
	graph20SeedResource(t, svc, graph20Project, graph20Generation, "Observation", "o2", "FHIR/Observation", json.RawMessage(`{"resourceType":"Observation","id":"o2","status":"final"}`))
	// Out-of-scope controls that must never surface: another project,
	// another generation, and a denied-path sibling condition.
	graph20SeedResource(t, svc, "pgsq20-proj-b", graph20Generation, "Patient", "p1", "FHIR/Patient", json.RawMessage(`{"resourceType":"Patient","id":"p1","poison":"cross-project"}`))
	// The store requires an edge's source to exist in its own scope, so
	// the cross-project control condition lives IN the foreign project.
	graph20SeedResource(t, svc, "pgsq20-proj-b", graph20Generation, "Condition", "cb", "FHIR/Condition", json.RawMessage(`{"resourceType":"Condition","id":"cb","poison":"cross-project-child"}`))
	graph20SeedResource(t, svc, graph20Project, "gen-2", "Condition", "cg", "FHIR/Condition", json.RawMessage(`{"resourceType":"Condition","id":"cg","poison":"cross-generation"}`))
	graph20SeedResource(t, svc, graph20Project, graph20Generation, "Condition", "c2", "FHIR/Confidential", json.RawMessage(`{"resourceType":"Condition","id":"c2","secret":true}`))
	// The optional-miss sibling route needs a Procedure only in the
	// sibling query (below); here obs (Patient→Observation subject) stays
	// empty for every root: the optional branch contributes zero rows.

	// Edges: the required hop exists TWICE (parallel duplicates, same
	// label and endpoints — the dedupe identity witness).
	duplicateFirst := graph20SeedEdge(t, svc, graph20Project, graph20Generation, "FHIR/Condition", "subject_Patient", "Condition", "c1", "Patient", "p1")
	duplicateSecond := graph20SeedEdge(t, svc, graph20Project, graph20Generation, "FHIR/Condition", "subject_Patient", "Condition", "c1", "Patient", "p1", 1)
	if duplicateFirst == duplicateSecond {
		t.Fatalf("parallel edges must be distinct occurrences, both got seq %d", duplicateFirst)
	}
	graph20SeedEdge(t, svc, graph20Project, graph20Generation, "FHIR/Observation", "focus_Condition", "Observation", "o2", "Condition", "c1")
	// Out-of-scope edge controls: cross-project, cross-generation, and an
	// edge whose own auth path is outside the allow list.
	graph20SeedEdge(t, svc, "pgsq20-proj-b", graph20Generation, "FHIR/Condition", "subject_Patient", "Condition", "cb", "Patient", "p1")
	// The store requires the edge source to exist in its own scope first
	// (InsertEdge source precondition), so the cross-generation control
	// gets a same-scope patient p1 in gen-2.
	graph20SeedResource(t, svc, graph20Project, "gen-2", "Patient", "p1", graph20AuthPath, json.RawMessage(`{"resourceType":"Patient","id":"p1"}`))
	graph20SeedEdge(t, svc, graph20Project, "gen-2", "FHIR/Condition", "subject_Patient", "Condition", "cg", "Patient", "p1")
	graph20SeedEdge(t, svc, graph20Project, graph20Generation, "FHIR/Confidential", "subject_Patient", "Condition", "c2", "Patient", "p1")

	// Alive control: the duplicate really exists as two occurrences.
	var occurrences int
	if err := svc.iso.QueryRow(ctx,
		`SELECT count(*) FROM "`+svc.schema+`"."fhir_edge_occurrence" WHERE label = 'subject_Patient' AND source_resource_type = 'Condition' AND source_logical_id = 'c1' AND target_resource_type = 'Patient' AND target_logical_id = 'p1' AND project = $1 AND dataset_generation = $2`,
		graph20Project, graph20Generation).Scan(&occurrences); err != nil {
		svc.fail("count parallel edges", err)
	}
	if occurrences != 2 {
		t.Fatalf("dedupe fixture precondition: expected 2 parallel occurrences, stored %d", occurrences)
	}

	// ---- Composite query: required one-hop + optional miss sibling +
	// nested finite branch. Literal expected rows, hand-written.
	plan := graph20PlanFor(t, graph20Root(
		graph20Condition("cond", spec.TraversalMatchRequired, graph20FocusedObservation("obs2", spec.TraversalMatchRequired)),
		graph20Observation("obs", spec.TraversalMatchOptional),
	), 10)
	// The auth allow list admits the readable rows of this query: root,
	// condition, and observation (the denied-path c2 stays outside).
	plan.BindVars["auth_resource_paths"] = []string{"FHIR/Patient", "FHIR/Condition", "FHIR/Observation"}
	rendered := graph20RenderIn(t, svc, plan)
	got := graph20ExecPaths(t, svc, rendered)
	want := []map[string]any{
		graph20Path("cond", []map[string]any{
			graph20Node("root", "Patient", "p1", "p1", `{"resourceType":"Patient","id":"p1","gender":"female"}`),
			graph20Node("cond", "Condition", "c1", "c1", `{"resourceType":"Condition","id":"c1","clinicalStatus":"active"}`),
		}, []map[string]any{graph20Relationship("cond", "subject_Patient", "Patient", "Condition")}),
		graph20Path("obs2", []map[string]any{
			graph20Node("root", "Patient", "p1", "p1", `{"resourceType":"Patient","id":"p1","gender":"female"}`),
			graph20Node("cond", "Condition", "c1", "c1", `{"resourceType":"Condition","id":"c1","clinicalStatus":"active"}`),
			graph20Node("obs2", "Observation", "o2", "o2", `{"resourceType":"Observation","id":"o2","status":"final"}`),
		}, []map[string]any{
			graph20Relationship("cond", "subject_Patient", "Patient", "Condition"),
			graph20Relationship("obs2", "focus_Condition", "Condition", "Observation"),
		}),
	}
	graph20AssertPaths(t, got, want)
	if len(got) != 2 {
		t.Fatal("alive control: the parallel-edge duplicate must dedupe to exactly one cond path, and the optional miss contribute nothing")
	}

	// ---- Lookahead: limit+1 rows maximum, and — because the duplicate
	// occurrences sit on route 1 ahead of route 2 — the 2-row result at
	// limit 1 behaviorally proves the bound applies AFTER dedupe: a
	// LIMIT-before-dedupe spelling would consume both raw duplicates and
	// return a single path.
	plan = graph20PlanFor(t, graph20Root(
		graph20Condition("cond", spec.TraversalMatchRequired, graph20FocusedObservation("obs2", spec.TraversalMatchRequired)),
		graph20Observation("obs", spec.TraversalMatchOptional),
	), 1)
	plan.BindVars["auth_resource_paths"] = []string{"FHIR/Patient", "FHIR/Condition", "FHIR/Observation"}
	lookahead := graph20ExecPaths(t, svc, graph20RenderIn(t, svc, plan))
	if len(lookahead) != 2 {
		lookaheadJSON, _ := json.Marshal(lookahead)
		t.Fatalf("limit+1 lookahead over deduped paths must return 2 rows (dedupe precedes the bound), got %s", lookaheadJSON)
	}
	if got := graph20ArgsValues(graph20RenderIn(t, svc, plan)); got[len(got)-1] != int64(2) {
		t.Fatalf("lookahead bound argument must be limit+1=2, got %v", got[len(got)-1])
	}

	// ---- Root-only optional query: no required routes, so the root set
	// is present and every root reads exactly one root path (optional
	// miss contributes zero).
	plan = graph20PlanFor(t, graph20Root(graph20Observation("obs", spec.TraversalMatchOptional)), 10)
	plan.BindVars["auth_resource_paths"] = []string{"FHIR/Patient"}
	roots := graph20ExecPaths(t, svc, graph20RenderIn(t, svc, plan))
	wantRoots := []map[string]any{
		graph20Path("root", []map[string]any{graph20Node("root", "Patient", "p1", "p1", `{"resourceType":"Patient","id":"p1","gender":"female"}`)}, nil),
		graph20Path("root", []map[string]any{graph20Node("root", "Patient", "p2", "p2", `{"resourceType":"Patient","id":"p2"}`)}, nil),
		graph20Path("root", []map[string]any{graph20Node("root", "Patient", "p3", "p3", `{"resourceType":"Patient","id":"p3"}`)}, nil),
	}
	graph20AssertPaths(t, roots, wantRoots)

	// ---- Sibling branches with hits: OPTIONAL cond + OPTIONAL procedure.
	graph20SeedResource(t, svc, graph20Project, graph20Generation, "Procedure", "pr3", "FHIR/Procedure", json.RawMessage(`{"resourceType":"Procedure","id":"pr3","status":"completed"}`))
	graph20SeedEdge(t, svc, graph20Project, graph20Generation, "FHIR/Procedure", "subject_Patient", "Procedure", "pr3", "Patient", "p3")
	plan = graph20PlanFor(t, graph20Root(
		graph20Condition("cond", spec.TraversalMatchOptional),
		graph20Procedure("proc", spec.TraversalMatchOptional),
	), 10)
	plan.BindVars["auth_resource_paths"] = []string{"FHIR/Patient", "FHIR/Condition", "FHIR/Procedure"}
	siblings := graph20ExecPaths(t, svc, graph20RenderIn(t, svc, plan))
	wantSiblings := []map[string]any{
		// Root paths for every root (route 0), then route 1 (cond from
		// p1 only), then route 2 (proc from p3 only) — exact route
		// order, with p1's route-1 duplicate deduplicated.
		graph20Path("root", []map[string]any{graph20Node("root", "Patient", "p1", "p1", `{"resourceType":"Patient","id":"p1","gender":"female"}`)}, nil),
		graph20Path("root", []map[string]any{graph20Node("root", "Patient", "p2", "p2", `{"resourceType":"Patient","id":"p2"}`)}, nil),
		graph20Path("root", []map[string]any{graph20Node("root", "Patient", "p3", "p3", `{"resourceType":"Patient","id":"p3"}`)}, nil),
		graph20Path("cond", []map[string]any{
			graph20Node("root", "Patient", "p1", "p1", `{"resourceType":"Patient","id":"p1","gender":"female"}`),
			graph20Node("cond", "Condition", "c1", "c1", `{"resourceType":"Condition","id":"c1","clinicalStatus":"active"}`),
		}, []map[string]any{graph20Relationship("cond", "subject_Patient", "Patient", "Condition")}),
		graph20Path("proc", []map[string]any{
			graph20Node("root", "Patient", "p3", "p3", `{"resourceType":"Patient","id":"p3"}`),
			graph20Node("proc", "Procedure", "pr3", "pr3", `{"resourceType":"Procedure","id":"pr3","status":"completed"}`),
		}, []map[string]any{graph20Relationship("proc", "subject_Patient", "Patient", "Procedure")}),
	}
	graph20AssertPaths(t, siblings, wantSiblings)

	// ---- Scope controls as independent witnesses (each proves exclusion
	// of one stored row that WOULD otherwise join):
	// auth-denied sibling condition c2 (edge AND node outside the allow
	// list) never contributes, while the allowed c1 route survives.
	plan = graph20PlanFor(t, graph20Root(graph20Condition("cond", spec.TraversalMatchOptional)), 10)
	// c1 and its subject edge are admissible (FHIR/Condition); the denied
	// sibling c2 and its edge carry FHIR/Confidential and stay excluded.
	plan.BindVars["auth_resource_paths"] = []string{"FHIR/Patient", "FHIR/Condition"}
	authDenied := graph20ExecPaths(t, svc, graph20RenderIn(t, svc, plan))
	for _, path := range authDenied {
		if terminal, _ := path["terminalAlias"].(string); terminal == "cond" {
			nodesJSON, _ := json.Marshal(path["nodes"])
			if strings.Contains(string(nodesJSON), `"c2"`) || strings.Contains(string(nodesJSON), "Confidential") {
				t.Fatalf("auth-denied condition leaked: %s", nodesJSON)
			}
		}
	}
	if len(authDenied) != 4 { // 3 root paths + the single c1 cond path (deduped)
		authJSON, _ := json.Marshal(authDenied)
		t.Fatalf("auth-denial control expects 4 paths (3 roots + 1 deduped cond), got %s", authJSON)
	}

	// Project and generation isolation are already proven by the exact
	// literal comparisons above (cross-project/cross-generation rows were
	// stored with poison payloads that would break any equality). Alive
	// control: those poison rows exist.
	var poison int
	if err := svc.iso.QueryRow(ctx,
		`SELECT count(*) FROM "`+svc.schema+`"."fhir_resource" WHERE project = $1 OR dataset_generation = $2`,
		"pgsq20-proj-b", "gen-2").Scan(&poison); err != nil {
		svc.fail("count poison rows", err)
	}
	if poison < 2 {
		t.Fatalf("scope-control precondition: expected cross-scope poison rows, found %d", poison)
	}
}

func TestGraph20LiveExplainProofs(t *testing.T) {
	svc := graph20LiveService(t)
	graph20SeedResource(t, svc, graph20Project, graph20Generation, "Patient", "p1", graph20AuthPath, json.RawMessage(`{"resourceType":"Patient","id":"p1"}`))
	graph20SeedResource(t, svc, graph20Project, graph20Generation, "Condition", "c1", "FHIR/Condition", json.RawMessage(`{"resourceType":"Condition","id":"c1"}`))
	graph20SeedResource(t, svc, graph20Project, graph20Generation, "Observation", "o2", "FHIR/Observation", json.RawMessage(`{"resourceType":"Observation","id":"o2"}`))
	graph20SeedEdge(t, svc, graph20Project, graph20Generation, "FHIR/Condition", "subject_Patient", "Condition", "c1", "Patient", "p1")
	graph20SeedEdge(t, svc, graph20Project, graph20Generation, "FHIR/Observation", "focus_Condition", "Observation", "o2", "Condition", "c1")

	plan := graph20PlanFor(t, graph20Root(graph20Condition("cond", spec.TraversalMatchRequired, graph20FocusedObservation("obs2", spec.TraversalMatchRequired))), 4)
	rendered := graph20RenderIn(t, svc, plan)
	args := make([]any, len(rendered.Args))
	for index, parameter := range rendered.Args {
		args[index] = parameter.Value
	}

	// Plain EXPLAIN plans cleanly (no plan-time fold; the whole statement
	// is a plain WITH — no recursive entry exists to prune or mis-plan).
	if _, err := svc.iso.Exec(svc.ctx, "EXPLAIN "+rendered.SQL, args...); err != nil {
		t.Fatalf("plain EXPLAIN must plan: %s", svc.scrub(err.Error()))
	}

	var raw []byte
	if err := svc.iso.QueryRow(svc.ctx, "EXPLAIN (FORMAT JSON) "+rendered.SQL, args...).Scan(&raw); err != nil {
		svc.fail("EXPLAIN FORMAT JSON", err)
	}
	var decoded []struct {
		Plan map[string]any `json:"Plan"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil || len(decoded) == 0 || decoded[0].Plan == nil {
		t.Fatalf("EXPLAIN JSON decode: %v — %s", err, svc.scrub(string(raw)))
	}
	tree := decoded[0].Plan

	// Proof 1: no Recursive node anywhere (arbitrary unbounded recursive
	// graph search remains unsupported — no WITH RECURSIVE is ever
	// rendered).
	for _, node := range graph20WalkPlanNodes(tree) {
		if nodeType, _ := node["Node Type"].(string); strings.Contains(strings.ToLower(nodeType), "recursive") {
			t.Fatalf("recursive node in plan: %v", nodeType)
		}
		if cteName, _ := node["CTE Name"].(string); cteName != "" && strings.Contains(strings.ToLower(cteName), "recursive") {
			t.Fatalf("recursive CTE in plan: %s", cteName)
		}
	}

	// Proof 2: the bound LIMIT sits ABOVE the dedupe WindowAgg — the
	// lookahead applies to deduplicated path identities, not raw union
	// rows (render/aql/graph_render.go:164-170 order).
	rootType, _ := tree["Node Type"].(string)
	if rootType != "Limit" {
		t.Fatalf("root plan node = %q, want Limit above everything", rootType)
	}
	if graph20FindNode(tree, func(node map[string]any) bool {
		nodeType, _ := node["Node Type"].(string)
		return nodeType == "WindowAgg"
	}) == nil {
		t.Fatalf("dedupe WindowAgg missing from plan:\n%s", svc.scrub(string(raw)))
	}

	// Proof 3: the REQUIRED-match EXISTS semi-joins survive planning as
	// semi-joins (root membership is proven before expansion), and the
	// edge relation is read for them. PostgreSQL inlines single-reference
	// CTEs, so the proof is over the plan tree rather than a CTE named
	// path_0; the WindowAgg/Limit proofs above pin the statement shape.
	if graph20FindNode(tree, func(node map[string]any) bool {
		nodeType, _ := node["Node Type"].(string)
		joinType, _ := node["Join Type"].(string)
		if strings.Contains(nodeType, "Semi") || joinType == "Semi" {
			return true
		}
		for _, key := range []string{"Filter", "Index Cond", "Join Filter", "Subplan Name", "One-Time Filter"} {
			if text, _ := node[key].(string); strings.Contains(text, "SubPlan") {
				return true
			}
		}
		return false
	}) == nil {
		t.Fatalf("REQUIRED-match semi-join missing from plan:\n%s", svc.scrub(string(raw)))
	}
	if graph20FindNode(tree, func(node map[string]any) bool {
		relation, _ := node["Relation Name"].(string)
		return relation == "fhir_edge_occurrence"
	}) == nil {
		t.Fatalf("edge occurrence scan missing from plan:\n%s", svc.scrub(string(raw)))
	}
}

func graph20WalkPlanNodes(node map[string]any) []map[string]any {
	nodes := []map[string]any{node}
	if children, ok := node["Plans"].([]any); ok {
		for _, child := range children {
			if childMap, ok := child.(map[string]any); ok {
				nodes = append(nodes, graph20WalkPlanNodes(childMap)...)
			}
		}
	}
	return nodes
}

func graph20FindNode(node map[string]any, want func(map[string]any) bool) map[string]any {
	for _, candidate := range graph20WalkPlanNodes(node) {
		if want(candidate) {
			return candidate
		}
	}
	return nil
}
