package postgres

import (
	"context"
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

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/source"
	pgstore "github.com/calypr/loom/internal/store/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Fixtures. The registered slice is ROOT_SCAN, UNNEST+, RETURN. Selectors
// are schema-legal (ir.Validate schema-checks them): Patient.identifier[]
// for the single-expansion shape and Observation.component[].code.coding[]
// for the nested repeated-array shape, mirroring what lower/row_expansion.go
// emits for recipe row expansions.
// ---------------------------------------------------------------------------

func arrayExtract(variable, resourceType string, steps ...spec.SelectorStep) ir.PhysicalExpression {
	return ir.PhysicalExpression{
		Kind:         ir.PhysicalExtractExpression,
		Cardinality:  ir.PhysicalArrayCardinality,
		NullBehavior: ir.PhysicalPreserveNull,
		Extract: &ir.PhysicalExtract{
			Source:       ir.PhysicalValue{Variable: variable, Path: []string{"payload"}},
			ResourceType: resourceType,
			Selector:     spec.Selector{Steps: steps},
		},
	}
}

func identifierExpansion(policy ir.PhysicalUnnestEmptyPolicy) ir.PhysicalUnnest {
	return ir.PhysicalUnnest{
		Owner:          ir.PhysicalUnnestOwner{OccurrenceID: "root", ResourceType: "Patient", RootVariable: "root", OwnerVariable: "root"},
		OutputVariable: "item", Ordinality: "position", HasItemVariable: "has_item",
		Expression:  arrayExtract("root", "Patient", spec.SelectorStep{Field: "identifier", Iterate: true}),
		EmptyPolicy: policy,
	}
}

func codingExpansion(policy ir.PhysicalUnnestEmptyPolicy) ir.PhysicalUnnest {
	return ir.PhysicalUnnest{
		Owner:          ir.PhysicalUnnestOwner{OccurrenceID: "obs-owner", ResourceType: "Observation", RootVariable: "root", OwnerVariable: "root"},
		OutputVariable: "item", Ordinality: "position", HasItemVariable: "has_item",
		Expression: arrayExtract("root", "Observation",
			spec.SelectorStep{Field: "component", Iterate: true},
			spec.SelectorStep{Field: "code"},
			spec.SelectorStep{Field: "coding", Iterate: true}),
		Ancestors:   []ir.PhysicalUnnestAncestor{{StepIndex: 0, Variable: "component_item"}},
		EmptyPolicy: policy,
	}
}

func unnestOp(unnest ir.PhysicalUnnest) ir.PhysicalOperation {
	return ir.PhysicalOperation{Kind: ir.PhysicalUnnestOp, Unnest: &unnest}
}

func valueProjection(name, variable string, path ...string) ir.PhysicalProjection {
	return ir.PhysicalProjection{Name: name, Value: ir.PhysicalValue{Variable: variable, Path: path}}
}

func expressionProjection(name, variable string, path ...string) ir.PhysicalProjection {
	value := ir.PhysicalValue{Variable: variable, Path: path}
	return ir.PhysicalProjection{Name: name, Expression: &ir.PhysicalExpression{
		Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull, Value: &value,
	}}
}

func unnestPlan(projections ...ir.PhysicalProjection) ir.PhysicalPlan {
	plan := basePlan()
	unnest := identifierExpansion(ir.PhysicalUnnestExclude)
	plan.Operations = []ir.PhysicalOperation{plan.Operations[0], unnestOp(unnest),
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: projections}}}
	return plan
}

func expansionProjections() []ir.PhysicalProjection {
	return []ir.PhysicalProjection{
		valueProjection("logical_id", "root", "logical_id"),
		valueProjection("item", "item"),
		valueProjection("position", "position"),
		valueProjection("has_item", "has_item"),
	}
}

// ---------------------------------------------------------------------------
// Registered-shape rendering: INNER, OUTER, nested ancestors, owner route.
// ---------------------------------------------------------------------------

func TestUnnestInnerRendersCrossJoinLateralWithZeroBasedStableOrdinality(t *testing.T) {
	rendered := renderOK(t, unnestPlan(expansionProjections()...))
	sql := rendered.SQL

	for _, want := range []string{
		`CROSS JOIN LATERAL (SELECT u1_e0.item AS item, ROW_NUMBER() OVER (ORDER BY u1_e0.ord) AS rn FROM jsonb_array_elements(`,
		`) WITH ORDINALITY AS u1_e0(item, ord) WHERE jsonb_typeof(u1_e0.item) <> 'null') AS u1`,
		`ORDER BY r0."resource_seq" ASC, u1.rn ASC`,
		`(u1.item) AS "c1"`,
		`(u1.rn - 1) AS "c2"`,
		`(u1.rn IS NOT NULL) AS "c3"`,
		// The terminal source form reproduces the AQL iterate ternary plus
		// the RETURN/FLATTEN wrap: the falsy set (null, false, 0, "") reads
		// as the empty array, an array passes through, and a truthy scalar
		// becomes its single item — all with renderer-owned constants.
		`IS NULL OR jsonb_typeof((r0."payload" #> $5::text[])) = 'null' THEN '[]'::jsonb`,
		`= 'array' THEN (r0."payload" #> $5::text[])`,
		`WHEN (r0."payload" #> $5::text[]) = 'false'::jsonb OR (r0."payload" #> $5::text[]) = '0'::jsonb OR (r0."payload" #> $5::text[]) = '""'::jsonb THEN '[]'::jsonb`,
		`ELSE jsonb_build_array((r0."payload" #> $5::text[])) END`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL missing %q:\n%s", want, sql)
		}
	}
	if strings.Contains(sql, "LEFT JOIN") {
		t.Fatalf("INNER rendered OUTER padding:\n%s", sql)
	}
	// No value or selector field is interpolated: "identifier" exists only
	// as a bound text[] parameter, never as SQL text.
	if strings.Contains(sql, "identifier") {
		t.Fatalf("selector field leaked into SQL text: %s", sql)
	}
	if _, err := compiler.NewRenderedPostgresPhysicalPlan(sql, rendered.Args, rendered.OutputColumns); err != nil {
		t.Fatalf("rendered plan contract violated: %v", err)
	}
	if !reflect.DeepEqual(rendered.OutputColumns, []string{"logical_id", "item", "position", "has_item"}) {
		t.Fatalf("output columns = %v", rendered.OutputColumns)
	}

	// The path parameter is the whole selector path, bound once.
	if len(rendered.Args) != 5 {
		t.Fatalf("args = %v, want four scope args plus one path", rendered.Args)
	}
	if got := rendered.Args[4]; got.Type != compiler.PostgreSQLParameterText || got.Value != "{identifier}" {
		t.Fatalf("path argument = %#v, want text {identifier}", got)
	}

	// Entry and dispatch agree byte-for-byte.
	viaEntry, err := RenderUnnest(unnestPlan(expansionProjections()...), mustBinding(t))
	if err != nil {
		t.Fatalf("RenderUnnest: %v", err)
	}
	if viaEntry.SQL != rendered.SQL || !reflect.DeepEqual(viaEntry.Args, rendered.Args) {
		t.Fatalf("entry and dispatch disagree:\n%s\n%v\nvs\n%s\n%v", viaEntry.SQL, viaEntry.Args, rendered.SQL, rendered.Args)
	}
}

func TestUnnestOuterPadsWithLeftJoinLateralOnTrue(t *testing.T) {
	plan := basePlan()
	unnest := identifierExpansion(ir.PhysicalUnnestPreserveParent)
	plan.Operations = []ir.PhysicalOperation{plan.Operations[0], unnestOp(unnest),
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: expansionProjections()}}}
	rendered := renderOK(t, plan)
	sql := rendered.SQL
	if !strings.Contains(sql, `LEFT JOIN LATERAL (SELECT u1_e0.item AS item, ROW_NUMBER() OVER (ORDER BY u1_e0.ord) AS rn FROM jsonb_array_elements(`) ||
		!strings.Contains(sql, `WHERE jsonb_typeof(u1_e0.item) <> 'null') AS u1 ON true`) {
		t.Fatalf("OUTER did not render LEFT JOIN LATERAL ... ON true with the terminal null-item filter:\n%s", sql)
	}
	if strings.Contains(sql, "CROSS JOIN LATERAL") {
		t.Fatalf("OUTER must not use CROSS JOIN LATERAL for its outer boundary:\n%s", sql)
	}
}

func TestUnnestNestedRepeatedArraysChainExpansionsAndCaptureAncestors(t *testing.T) {
	plan := basePlan()
	plan.BindVars["root_collection"] = "Observation"
	unnest := codingExpansion(ir.PhysicalUnnestExclude)
	projections := append(expansionProjections(), valueProjection("component", "component_item"))
	plan.Operations = []ir.PhysicalOperation{plan.Operations[0], unnestOp(unnest),
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: projections}}}
	rendered := renderOK(t, plan)
	sql := rendered.SQL

	for _, want := range []string{
		// With ancestors the AQL reference is the lineage FOR-loop ternary
		// for every step (terminal included): the falsy set reads as the
		// empty array, a truthy scalar reaches jsonb_array_elements and
		// raises, matching AQL's FOR-over-non-array error.
		`FROM jsonb_array_elements(CASE WHEN (r0."payload" #> $5::text[]) IS NULL OR jsonb_typeof((r0."payload" #> $5::text[])) = 'null' THEN '[]'::jsonb WHEN jsonb_typeof((r0."payload" #> $5::text[])) = 'array' THEN (r0."payload" #> $5::text[]) WHEN (r0."payload" #> $5::text[]) = 'false'::jsonb OR (r0."payload" #> $5::text[]) = '0'::jsonb OR (r0."payload" #> $5::text[]) = '""'::jsonb THEN '[]'::jsonb ELSE (r0."payload" #> $5::text[]) END) WITH ORDINALITY AS u1_e0(value, ord)`,
		`CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN (u1_e0.value #> $6::text[]) IS NULL OR jsonb_typeof((u1_e0.value #> $6::text[])) = 'null' THEN '[]'::jsonb WHEN jsonb_typeof((u1_e0.value #> $6::text[])) = 'array' THEN (u1_e0.value #> $6::text[]) WHEN (u1_e0.value #> $6::text[]) = 'false'::jsonb OR (u1_e0.value #> $6::text[]) = '0'::jsonb OR (u1_e0.value #> $6::text[]) = '""'::jsonb THEN '[]'::jsonb ELSE (u1_e0.value #> $6::text[]) END) WITH ORDINALITY AS u1_e1(item, ord)`,
		// The terminal null-item filter reproduces the AQL terminal
		// FILTER ... != null; ROW_NUMBER then numbers survivors densely.
		`WHERE jsonb_typeof(u1_e1.item) <> 'null'`,
		// Row-major carrier position = ROW_NUMBER over the step positions.
		`SELECT u1_e1.item AS item, u1_e0.value AS ancestor_0, ROW_NUMBER() OVER (ORDER BY u1_e0.ord, u1_e1.ord) AS rn`,
		`ORDER BY r0."resource_seq" ASC, u1.rn ASC`,
		`(u1.ancestor_0) AS "c4"`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL missing %q:\n%s", want, sql)
		}
	}
	// Both paths are parameters, deduplicated by their literals.
	var pathCount int
	for _, argument := range rendered.Args {
		if text, ok := argument.Value.(string); ok && (text == "{component}" || text == "{code,coding}") {
			pathCount++
		}
	}
	if pathCount != 2 {
		t.Fatalf("path parameters = %v, want exactly {component} and {code,coding}: %#v", pathCount, rendered.Args)
	}
	for _, leaked := range []string{"component", "coding"} {
		if strings.Contains(sql, leaked) {
			t.Fatalf("selector field %q leaked into SQL text: %s", leaked, sql)
		}
	}
}

func TestUnnestOwnerRouteRendersTypedIdentityJoinBeforeLaterals(t *testing.T) {
	plan := basePlan()
	// The PostgreSQL edge table comes from the storage registry, so the
	// collection bind is unread plan data: a sentinel proves no bind
	// value can reach SQL text.
	plan.BindVars["edge_collection"] = "SENTINEL-unused-edge-collection-bind"
	plan.BindVars["edge_label"] = "has-observation"
	plan.BindVars["target_type"] = "Observation"
	unnest := codingExpansion(ir.PhysicalUnnestPreserveParent)
	unnest.Owner = ir.PhysicalUnnestOwner{
		OccurrenceID: "obs-owner", ResourceType: "Observation", RootVariable: "root", OwnerVariable: "obs",
		Route: []ir.PhysicalUnnestRouteStep{{OccurrenceID: "obs-owner", Traversal: ir.PhysicalTraversal{
			SourceVariable: "root", TargetVariable: "obs", EdgeVariable: "obs_edge",
			Direction: ir.PhysicalOutbound, EdgeCollectionBindKey: "edge_collection",
			EdgeLabelBindKey: "edge_label", TargetTypeBindKey: "target_type",
			EdgeTargetTypeField: "to_type", Strategy: ir.PhysicalTraversalNative,
		}}},
	}
	unnest.Expression = arrayExtract("obs", "Observation",
		spec.SelectorStep{Field: "component", Iterate: true},
		spec.SelectorStep{Field: "code"},
		spec.SelectorStep{Field: "coding", Iterate: true})
	projections := []ir.PhysicalProjection{
		valueProjection("patient_id", "root", "logical_id"),
		valueProjection("observation_id", "obs", "logical_id"),
		valueProjection("item", "item"),
		valueProjection("position", "position"),
		valueProjection("has_item", "has_item"),
		valueProjection("component", "component_item"),
	}
	plan.Operations = []ir.PhysicalOperation{plan.Operations[0], unnestOp(unnest),
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: projections}}}
	rendered := renderOK(t, plan)
	sql := rendered.SQL

	joinAt := strings.Index(sql, "INNER JOIN")
	lateralAt := strings.Index(sql, "JOIN LATERAL")
	orderAt := strings.Index(sql, "ORDER BY")
	if joinAt < 0 || lateralAt < 0 || !(joinAt < lateralAt && lateralAt < orderAt) {
		t.Fatalf("storage join and lateral must sit between FROM and ORDER BY in that order:\n%s", sql)
	}
	if !strings.Contains(sql, `ORDER BY r0."resource_seq" ASC, e1."edge_seq" ASC, u1.rn ASC`) {
		t.Fatalf("route edge_seq and expansion rn must both extend the total order:\n%s", sql)
	}
	if !strings.Contains(sql, `(r1."payload" #>`) {
		t.Fatalf("owner-route expansion must extract from the joined owner row:\n%s", sql)
	}
	for _, leaked := range []string{"has-observation", "SENTINEL-unused-edge-collection-bind"} {
		if strings.Contains(sql, leaked) {
			t.Fatalf("route bind value %q leaked into SQL text: %s", leaked, sql)
		}
	}
}

func TestUnnestRenderIsDeterministicAndDoesNotAliasPlanData(t *testing.T) {
	plan := unnestPlan(expansionProjections()...)
	first := renderOK(t, plan)
	second := renderOK(t, plan)
	if first.SQL != second.SQL || !reflect.DeepEqual(first.Args, second.Args) || !reflect.DeepEqual(first.OutputColumns, second.OutputColumns) {
		t.Fatalf("repeated render changed the statement")
	}
	capturedSQL, capturedArgs := first.SQL, first.Args
	plan.BindVars["project"] = "mutated"
	plan.BindVars["dataset_generation"] = "mutated"
	plan.BindVars["auth_resource_paths"].([]string)[0] = "MUTATED"
	plan.BindVars["root_collection"] = "Observation"
	if _, err := RenderPhysicalPlan(plan, mustBinding(t)); err == nil {
		t.Fatalf("render after mutation accepted a resource-type contradiction")
	}
	if first.SQL != capturedSQL || !reflect.DeepEqual(first.Args, capturedArgs) {
		t.Fatalf("render aliased plan data")
	}
}

// aqlScopeOperations builds the explicit generic-navigation scope block the
// AQL renderer requires (the PostgreSQL renderer derives the same canonical
// scope from binds through scope.go): project, dataset generation, and the
// AUTH_RESOURCE_PATH_ALLOWED chain.
func aqlScopeOperations(variable string) []ir.PhysicalOperation {
	return []ir.PhysicalOperation{
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "EQUALS", Left: ir.PhysicalValue{Variable: variable, Path: []string{"project"}},
			Right: &ir.PhysicalValue{BindKey: "project"}}}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "EQUALS", Left: ir.PhysicalValue{Variable: variable, Path: []string{"dataset_generation"}},
			Right: &ir.PhysicalValue{BindKey: "dataset_generation"}}}},
		{Kind: ir.PhysicalDerivedLetOp, DerivedLet: &ir.PhysicalDerivedLet{
			Variable: "scope_auth", Operator: "AUTH_RESOURCE_PATH_ALLOWED",
			Inputs: []ir.PhysicalValue{
				{Variable: variable, Path: []string{"auth_resource_path"}},
				{BindKey: "auth_resource_paths"},
				{BindKey: "auth_resource_paths_unrestricted"},
			}}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "scope_auth"},
			Right: &ir.PhysicalValue{BindKey: "scope_allowed"}}}},
	}
}

func TestUnnestCommandDispatchStaysFailClosedForPostgreSQL(t *testing.T) {
	// Capability discovery stays closed: the registration surface is
	// RenderUnnest, not command dispatch (pg-sql-01/02/03 discipline).
	plan := unnestPlan(expansionProjections()...)
	unnestOpCopy := plan.Operations[1]
	unnestOpCopy.Source = ir.PhysicalSource{ResourceType: "Patient", SemanticField: "expand"}
	returnOperation := plan.Operations[2]
	operations := append([]ir.PhysicalOperation{plan.Operations[0]}, aqlScopeOperations("root")...)
	operations = append(operations, unnestOpCopy, returnOperation)
	plan.Operations = operations
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatalf("aql render: %v", err)
	}
	if _, err := compiler.CompiledCommandForBackend(compiler.CompiledBackendAQL, rendered); err != nil {
		t.Fatalf("AQL command dispatch: %v", err)
	}
	if _, err := compiler.CompiledCommandForBackend(compiler.CompiledBackendPostgreSQL, rendered); !errors.Is(err, compiler.ErrCompiledCommandBackendNotRegistered) {
		t.Fatalf("PostgreSQL command dispatch error = %v, want not-registered while the planner gate stays fail-closed", err)
	}

	// The same registered plan still renders through PostgreSQL, proving
	// the fail-closed command gate and the registered renderer are
	// separate surfaces, not an accident of the plan shape.
	if _, err := RenderUnnest(unnestPlan(expansionProjections()...), mustBinding(t)); err != nil {
		t.Fatalf("RenderUnnest on the registered shape: %v", err)
	}
}

func TestUnnestZeroValueBindingFailsLikeSiblings(t *testing.T) {
	if _, err := RenderUnnest(unnestPlan(expansionProjections()...), ir.PostgresStorageBinding{}); !errors.Is(err, ir.ErrUnregisteredPostgresIdentifier) {
		t.Fatalf("zero binding error = %v, want ErrUnregisteredPostgresIdentifier", err)
	}
}

// ---------------------------------------------------------------------------
// Fail-closed matrix. Every refusal must be a typed capability gap; nothing
// outside the registered shape may produce SQL, and no case may fall back.
// ---------------------------------------------------------------------------

func TestUnnestRejectsSurfaceWithTypedErrors(t *testing.T) {
	mutate := func(edit func(*ir.PhysicalPlan, *ir.PhysicalUnnest)) func(*ir.PhysicalPlan) {
		return func(plan *ir.PhysicalPlan) {
			edit(plan, plan.Operations[1].Unnest)
		}
	}
	cases := []struct {
		name   string
		mutate func(*ir.PhysicalPlan)
	}{
		{"no unnest", func(p *ir.PhysicalPlan) { p.Operations = []ir.PhysicalOperation{p.Operations[0], p.Operations[2]} }},
		{"unnest before root scan", func(p *ir.PhysicalPlan) { p.Operations = []ir.PhysicalOperation{p.Operations[1], p.Operations[2]} }},
		{"return not last", mutate(func(p *ir.PhysicalPlan, _ *ir.PhysicalUnnest) {
			p.Operations = []ir.PhysicalOperation{p.Operations[0], p.Operations[1], p.Operations[2], p.Operations[2]}
		})},
		{"missing return", mutate(func(p *ir.PhysicalPlan, _ *ir.PhysicalUnnest) { p.Operations = p.Operations[:2] })},
		{"second root scan", mutate(func(p *ir.PhysicalPlan, _ *ir.PhysicalUnnest) {
			p.Operations = []ir.PhysicalOperation{p.Operations[0], p.Operations[0], p.Operations[1], p.Operations[2]}
		})},
		{"filter coexists with unnest", mutate(func(p *ir.PhysicalPlan, _ *ir.PhysicalUnnest) {
			p.BindVars["status_value"] = "final"
			p.Operations = []ir.PhysicalOperation{p.Operations[0],
				{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
					Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"status"}},
					Right: &ir.PhysicalValue{BindKey: "status_value"}}}},
				p.Operations[1], p.Operations[2]}
		})},
		{"sort coexists with unnest", mutate(func(p *ir.PhysicalPlan, _ *ir.PhysicalUnnest) {
			p.Operations = []ir.PhysicalOperation{p.Operations[0], p.Operations[1],
				{Kind: ir.PhysicalSortOp, Sort: &ir.PhysicalSort{Keys: []ir.PhysicalValue{{Variable: "root", Path: []string{"logical_id"}}}}},
				p.Operations[2]}
		})},
		{"limit coexists with unnest", mutate(func(p *ir.PhysicalPlan, _ *ir.PhysicalUnnest) {
			p.Operations = []ir.PhysicalOperation{p.Operations[0], p.Operations[1],
				{Kind: ir.PhysicalLimitOp, Limit: &ir.PhysicalLimit{BindKey: "limit"}}, p.Operations[2]}
		})},
		{"traversal op coexists with unnest", mutate(func(p *ir.PhysicalPlan, _ *ir.PhysicalUnnest) {
			addTraversal(p, ir.PhysicalOutbound, "to_type", "", nil)
			p.Operations = []ir.PhysicalOperation{p.Operations[0], p.Operations[1], p.Operations[1], p.Operations[2]}
		})},
		{"population root", mutate(func(p *ir.PhysicalPlan, _ *ir.PhysicalUnnest) {
			p.Operations[0].RootScan.Population = &ir.PhysicalPopulationRootSource{}
		})},
		{"error policy is a capability gap", mutate(func(_ *ir.PhysicalPlan, u *ir.PhysicalUnnest) { u.EmptyPolicy = ir.PhysicalUnnestError })},
		{"unknown policy", mutate(func(_ *ir.PhysicalPlan, u *ir.PhysicalUnnest) { u.EmptyPolicy = "SOMETIMES" })},
		{"missing output variables", mutate(func(_ *ir.PhysicalPlan, u *ir.PhysicalUnnest) { u.OutputVariable = "" })},
		{"missing has-item variable", mutate(func(_ *ir.PhysicalPlan, u *ir.PhysicalUnnest) { u.HasItemVariable = "" })},
		{"value-expression source", mutate(func(_ *ir.PhysicalPlan, u *ir.PhysicalUnnest) {
			value := ir.PhysicalValue{Variable: "root", Path: []string{"payload"}}
			u.Expression = ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalArrayCardinality,
				NullBehavior: ir.PhysicalPreserveNull, Value: &value}
		})},
		{"scalar cardinality source", mutate(func(_ *ir.PhysicalPlan, u *ir.PhysicalUnnest) {
			u.Expression.Cardinality = ir.PhysicalScalarCardinality
		})},
		{"omit-nulls source behavior", mutate(func(_ *ir.PhysicalPlan, u *ir.PhysicalUnnest) { u.Expression.NullBehavior = ir.PhysicalOmitNulls })},
		{"extract payload missing", mutate(func(_ *ir.PhysicalPlan, u *ir.PhysicalUnnest) { u.Expression.Extract = nil })},
		{"conditional-array mode", mutate(func(_ *ir.PhysicalPlan, u *ir.PhysicalUnnest) {
			u.Expression.Extract.ExecutionMode = ir.PhysicalSelectorConditionalArray
		})},
		{"direct-scalar mode", mutate(func(_ *ir.PhysicalPlan, u *ir.PhysicalUnnest) {
			u.Expression.Extract.ExecutionMode = ir.PhysicalSelectorDirectScalar
		})},
		{"prepared reference", mutate(func(_ *ir.PhysicalPlan, u *ir.PhysicalUnnest) {
			u.Expression.Extract.Prepared = &ir.PhysicalPreparedReference{SetVariable: "set_1", Field: "field"}
		})},
		{"fallback selectors", mutate(func(_ *ir.PhysicalPlan, u *ir.PhysicalUnnest) {
			u.Expression.Extract.Fallbacks = []ir.PhysicalSelectorFallback{{
				Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}}, ResourceType: "Patient",
				Selector: spec.Selector{Steps: []spec.SelectorStep{{Field: "name", Iterate: true}}},
			}}
		})},
		{"unit normalization", mutate(func(_ *ir.PhysicalPlan, u *ir.PhysicalUnnest) {
			u.Expression.Extract.UnitNormalization = &ir.PhysicalUnitNormalization{}
		})},
		{"distinct expansion", mutate(func(_ *ir.PhysicalPlan, u *ir.PhysicalUnnest) { u.Expression.Extract.Distinct = true })},
		{"selector filter", mutate(func(_ *ir.PhysicalPlan, u *ir.PhysicalUnnest) {
			u.Expression.Extract.Selector.Filter = &spec.ContainsFilter{Field: "system", Needle: "urn:x"}
		})},
		{"empty selector", mutate(func(_ *ir.PhysicalPlan, u *ir.PhysicalUnnest) { u.Expression.Extract.Selector.Steps = nil })},
		{"non-iterated terminal step", mutate(func(_ *ir.PhysicalPlan, u *ir.PhysicalUnnest) {
			u.Expression.Extract.Selector.Steps[0].Iterate = false
		})},
		{"indexed step", mutate(func(_ *ir.PhysicalPlan, u *ir.PhysicalUnnest) {
			zero := 0
			u.Expression.Extract.Selector.Steps[0].Index = &zero
		})},
		{"property-less step", mutate(func(_ *ir.PhysicalPlan, u *ir.PhysicalUnnest) {
			u.Expression.Extract.Selector.Steps[0].Field = ""
		})},
		{"unsafe path segment", mutate(func(_ *ir.PhysicalPlan, u *ir.PhysicalUnnest) {
			u.Expression.Extract.Selector.Steps[0].Field = "bad-name"
		})},
		{"source not payload", mutate(func(_ *ir.PhysicalPlan, u *ir.PhysicalUnnest) {
			u.Expression.Extract.Source.Path = []string{"logical_id"}
		})},
		{"source is bind", mutate(func(_ *ir.PhysicalPlan, u *ir.PhysicalUnnest) {
			p := u // keep the mutation local to the extract payload
			p.Expression.Extract.Source.BindKey = "doc"
		})},
		{"source variable not owner", mutate(func(_ *ir.PhysicalPlan, u *ir.PhysicalUnnest) {
			u.Expression.Extract.Source.Variable = "elsewhere"
		})},
		{"resource type mismatch", mutate(func(_ *ir.PhysicalPlan, u *ir.PhysicalUnnest) {
			u.Expression.Extract.ResourceType = "Observation"
		})},
		{"resource type blank", mutate(func(_ *ir.PhysicalPlan, u *ir.PhysicalUnnest) {
			u.Expression.Extract.ResourceType = ""
		})},
		{"route scope operations", mutate(func(p *ir.PhysicalPlan, u *ir.PhysicalUnnest) {
			p.BindVars["edge_collection"] = "fhir_edge"
			p.BindVars["edge_label"] = "has-observation"
			p.BindVars["target_type"] = "Observation"
			u.Owner = ir.PhysicalUnnestOwner{OccurrenceID: "obs-owner", ResourceType: "Patient", RootVariable: "root", OwnerVariable: "obs",
				Route: []ir.PhysicalUnnestRouteStep{{OccurrenceID: "obs-owner", Traversal: ir.PhysicalTraversal{
					SourceVariable: "root", TargetVariable: "obs", EdgeVariable: "obs_edge",
					Direction: ir.PhysicalOutbound, EdgeCollectionBindKey: "edge_collection",
					EdgeLabelBindKey: "edge_label", TargetTypeBindKey: "target_type",
					EdgeTargetTypeField: "to_type", Strategy: ir.PhysicalTraversalNative,
				}, Scope: []ir.PhysicalOperation{{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
					Operator: "EXISTS", Left: ir.PhysicalValue{Variable: "obs", Path: []string{"status"}}}}}}}}}
			u.Expression = arrayExtract("obs", "Patient", spec.SelectorStep{Field: "identifier", Iterate: true})
		})},
		{"route any direction", mutate(func(p *ir.PhysicalPlan, u *ir.PhysicalUnnest) {
			planRouteTraversal(p, u, ir.PhysicalAny, "")
		})},
		{"route endpoint lookup", mutate(func(p *ir.PhysicalPlan, u *ir.PhysicalUnnest) {
			planRouteTraversal(p, u, ir.PhysicalOutbound, ir.PhysicalTraversalEndpointLookup)
		})},
		{"route chain break", mutate(func(p *ir.PhysicalPlan, u *ir.PhysicalUnnest) {
			planRouteTraversal(p, u, ir.PhysicalOutbound, "")
			u.Owner.Route[0].Traversal.SourceVariable = "not-root"
		})},
		{"route does not end at owner", mutate(func(p *ir.PhysicalPlan, u *ir.PhysicalUnnest) {
			planRouteTraversal(p, u, ir.PhysicalOutbound, "")
			u.Owner.OwnerVariable = "unreached"
		})},
		{"ancestor indexes a non-iterated prefix step", mutate(func(_ *ir.PhysicalPlan, u *ir.PhysicalUnnest) {
			u.Ancestors = []ir.PhysicalUnnestAncestor{{StepIndex: 1, Variable: "code_item"}}
		})},
		{"ancestor indexes the terminal step", mutate(func(_ *ir.PhysicalPlan, u *ir.PhysicalUnnest) {
			u.Ancestors = []ir.PhysicalUnnestAncestor{{StepIndex: 2, Variable: "coding_item"}}
		})},
		{"ancestor out of range", mutate(func(_ *ir.PhysicalPlan, u *ir.PhysicalUnnest) {
			u.Ancestors = []ir.PhysicalUnnestAncestor{{StepIndex: 3, Variable: "component_item"}}
		})},
		{"ancestor repeats a step", mutate(func(_ *ir.PhysicalPlan, u *ir.PhysicalUnnest) {
			u.Ancestors = []ir.PhysicalUnnestAncestor{
				{StepIndex: 0, Variable: "component_item"}, {StepIndex: 0, Variable: "component_item_2"}}
		})},
		{"zero projections", mutate(func(p *ir.PhysicalPlan, _ *ir.PhysicalUnnest) {
			p.Operations[2].Return.Projections = nil
		})},
		{"unnamed projection", mutate(func(p *ir.PhysicalPlan, _ *ir.PhysicalUnnest) {
			p.Operations[2].Return.Projections[0].Name = ""
		})},
		{"hidden projection", mutate(func(p *ir.PhysicalPlan, _ *ir.PhysicalUnnest) {
			p.Operations[2].Return.Projections[0].Hidden = true
		})},
		{"presence sidecar", mutate(func(p *ir.PhysicalPlan, _ *ir.PhysicalUnnest) {
			p.Operations[2].Return.Projections = append(p.Operations[2].Return.Projections, ir.PhysicalProjection{
				Name: "has_identifier", PresenceOutput: true,
				Presence: &ir.PhysicalProjectionPresence{
					Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
					Paths:  [][]string{{"identifier"}},
				},
			})
		})},
		{"selector projection", mutate(func(p *ir.PhysicalPlan, _ *ir.PhysicalUnnest) {
			projection := ir.PhysicalProjection{Name: "identifier_system"}
			source := arrayExtract("root", "Patient", spec.SelectorStep{Field: "identifier"})
			source.Cardinality = ir.PhysicalScalarCardinality
			projection.Expression = &source
			p.Operations[2].Return.Projections = append(p.Operations[2].Return.Projections, projection)
		})},
		{"bind-backed projection", mutate(func(p *ir.PhysicalPlan, _ *ir.PhysicalUnnest) {
			p.BindVars["const"] = "x"
			p.Operations[2].Return.Projections[0] = ir.PhysicalProjection{Name: "c", Value: ir.PhysicalValue{BindKey: "const"}}
		})},
		{"bind-backed expression projection", mutate(func(p *ir.PhysicalPlan, _ *ir.PhysicalUnnest) {
			p.BindVars["const"] = "x"
			value := ir.PhysicalValue{BindKey: "const"}
			p.Operations[2].Return.Projections[0] = ir.PhysicalProjection{Name: "c", Expression: &ir.PhysicalExpression{
				Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, Value: &value}}
		})},
		{"expression kind outside the slice", mutate(func(p *ir.PhysicalPlan, _ *ir.PhysicalUnnest) {
			p.Operations[2].Return.Projections[0] = ir.PhysicalProjection{Name: "c", Expression: &ir.PhysicalExpression{
				Kind: ir.PhysicalLiteralExpression, Cardinality: ir.PhysicalScalarCardinality}}
		})},
		{"derived output variable collides with a row", mutate(func(p *ir.PhysicalPlan, u *ir.PhysicalUnnest) {
			u.OutputVariable = "root"
		})},
		{"duplicate expansion output variables", mutate(func(p *ir.PhysicalPlan, u *ir.PhysicalUnnest) {
			p.Operations = []ir.PhysicalOperation{p.Operations[0], unnestOp(*u), unnestOp(*u), p.Operations[2]}
		})},
		{"path over non-jsonb derived value", mutate(func(p *ir.PhysicalPlan, _ *ir.PhysicalUnnest) {
			p.Operations[2].Return.Projections[2] = valueProjection("position_value", "position", "x")
		})},
	}
	for _, tc := range cases {
		plan := unnestPlan(expansionProjections()...)
		tc.mutate(&plan)
		if _, err := RenderUnnest(plan, mustBinding(t)); !errors.Is(err, ErrUnsupportedPostgresPlan) {
			t.Errorf("%s: RenderUnnest error = %v, want ErrUnsupportedPostgresPlan", tc.name, err)
		}
		if _, err := RenderPhysicalPlan(plan, mustBinding(t)); err == nil {
			t.Errorf("%s: core dispatch accepted an unregistered shape", tc.name)
		}
	}
}

// planRouteTraversal attaches the minimal typed-identity route step shared
// by the route refusal cases.
func planRouteTraversal(p *ir.PhysicalPlan, u *ir.PhysicalUnnest, direction ir.PhysicalTraversalDirection, strategy ir.PhysicalTraversalStrategy) {
	p.BindVars["edge_collection"] = "fhir_edge"
	p.BindVars["edge_label"] = "has-observation"
	p.BindVars["target_type"] = "Patient"
	u.Owner = ir.PhysicalUnnestOwner{OccurrenceID: "obs-owner", ResourceType: "Patient", RootVariable: "root", OwnerVariable: "obs",
		Route: []ir.PhysicalUnnestRouteStep{{OccurrenceID: "obs-owner", Traversal: ir.PhysicalTraversal{
			SourceVariable: "root", TargetVariable: "obs", EdgeVariable: "obs_edge",
			Direction: direction, EdgeCollectionBindKey: "edge_collection",
			EdgeLabelBindKey: "edge_label", TargetTypeBindKey: "target_type",
			EdgeTargetTypeField: func() string {
				if direction == ir.PhysicalInbound {
					return "from_type"
				}
				return "to_type"
			}(),
			Strategy: strategy, EndpointField: "_from", EndpointJoinField: "_to",
		}}}}
	u.Expression = arrayExtract("obs", "Patient", spec.SelectorStep{Field: "identifier", Iterate: true})
}

func TestUnnestRejectsOwnerTypeContradictionAgainstScannedRows(t *testing.T) {
	// Internally consistent (extract type == owner type) but contradicts
	// the row the scan actually reads: fail closed, never render.
	plan := unnestPlan(expansionProjections()...)
	unnest := plan.Operations[1].Unnest
	unnest.Owner.ResourceType = "Observation"
	unnest.Expression.Extract.ResourceType = "Observation"
	if _, err := RenderUnnest(plan, mustBinding(t)); !errors.Is(err, ErrUnsupportedPostgresPlan) {
		t.Fatalf("owner/row type contradiction error = %v, want ErrUnsupportedPostgresPlan", err)
	}
}

// ---------------------------------------------------------------------------
// Real PostgreSQL 18 integration (service-gated by LOOM_TEST_POSTGRES_URL).
//
// Every acceptance assertion below runs against a real PostgreSQL 18
// server: each test owns one uniquely named verify_sql09_* database and
// one owned schema, applies the pg-store embedded migrations through the
// real runner, stores literal rows through the merged pg-store
// repositories, executes the rendered statement with its ordered driver
// arguments, and compares against literal expected rows plus a hand-written
// direct-SQL witness. Required cases must not skip; TestZZNoLeftoverSQL09
// Resources audits that nothing survives.
// ---------------------------------------------------------------------------

var sql09ResourceNamePattern = regexp.MustCompile(`^verify_sql09_[0-9]+_[0-9a-f]+$`)

type sql09Service struct {
	ctx       context.Context
	admin     *pgxpool.Pool
	iso       *pgxpool.Pool
	client    *pgstore.Client
	schema    string
	resources *pgstore.ResourceRepository
	fail      func(step string, err error)
}

// sql09ServiceURL gates the live cases the same way the sibling pgir02
// suite does so unrelated lanes stay green. Acceptance for this ticket is
// the run WITH the service set: the acceptance executor must verify zero
// SKIPs for the TestPG18Unnest* cases; a skipped required case is a
// failure, never a pass.
func sql09ServiceURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 unnest acceptance tests")
	}
	return raw
}

func newSQL09Service(t *testing.T) *sql09Service {
	t.Helper()
	raw := sql09ServiceURL(t)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("LOOM_TEST_POSTGRES_URL must be a postgres:// or postgresql:// URL")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect to test service: %v", err)
	}
	t.Cleanup(admin.Close)

	dbName := fmt.Sprintf("verify_sql09_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !sql09ResourceNamePattern.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		t.Fatalf("create isolated database: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(ctx),
			`DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %v", err)
		}
	})

	if q := u.Query(); q.Get("dbname") != "" {
		q.Del("dbname")
		u.RawQuery = q.Encode()
	}
	u.Path = "/" + dbName
	dsn := u.String()

	iso, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to isolated database: %v", err)
	}
	t.Cleanup(iso.Close)

	schema := fmt.Sprintf("verify_sql09_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !sql09ResourceNamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatalf("create owned schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := iso.Exec(context.WithoutCancel(ctx),
			`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %v", err)
		}
	})

	client, err := pgstore.Open(ctx, pgstore.Config{DSN: dsn, MaxConns: 4, MinConns: 1, AcquireTimeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("open pg-store client (requires a PostgreSQL 18 service): %v", err)
	}
	t.Cleanup(client.Close)
	if _, err := pgstore.MigrateSchema(ctx, client, schema); err != nil {
		t.Fatalf("migrate owned schema: %v", err)
	}
	resources, err := pgstore.NewResourceRepository(client, schema)
	if err != nil {
		t.Fatalf("build resource repository: %v", err)
	}
	return &sql09Service{ctx: ctx, admin: admin, iso: iso, client: client, schema: schema, resources: resources,
		fail: func(step string, err error) { t.Fatalf("%s: %v", step, err) }}
}

func (s *sql09Service) insertResource(t *testing.T, project, generation, resourceType, logicalID, authPath string, payload json.RawMessage) {
	t.Helper()
	if _, err := s.resources.InsertResource(s.ctx, source.Resource{
		Project: project, DatasetGeneration: generation, ResourceType: resourceType,
		LogicalID: logicalID, AuthResourcePath: authPath, Payload: payload,
	}); err != nil {
		s.fail("insert resource "+resourceType+"/"+logicalID, err)
	}
}

func (s *sql09Service) insertEdge(t *testing.T, rec pgstore.EdgeOccurrenceRow) {
	t.Helper()
	edges, err := pgstore.NewEdgeRepository(s.client, s.schema)
	if err != nil {
		s.fail("build edge repository", err)
	}
	if _, err := edges.InsertEdge(s.ctx, rec); err != nil {
		s.fail("insert edge "+rec.Label+" "+rec.SourceLogicalID+"->"+rec.TargetLogicalID, err)
	}
}

func (s *sql09Service) runRendered(t *testing.T, rendered compiler.RenderedPostgresPhysicalPlan) [][]string {
	t.Helper()
	return s.queryRows(t, rendered.SQL, parameterValues(rendered.Args)...)
}

// sql09StringifyCell stringifies a driver value for literal comparison.
// pgx decodes jsonb into Go structures, which print lossily; JSON values
// therefore round-trip through encoding/json to canonical text. The
// jsonb 'null' scalar decodes to Go nil like SQL NULL, so any
// null-versus-null-free claim is witnessed with SQL-level `IS NULL`
// probes over the rendered statement rather than with driver values (see
// sql09IsNullProbe).
func sql09StringifyCell(value any) string {
	switch typed := value.(type) {
	case nil:
		return "<null>"
	case string:
		return typed
	case []byte:
		return string(typed)
	case map[string]any, []any, json.RawMessage:
		if out, err := json.Marshal(typed); err == nil {
			return string(out)
		}
	}
	return fmt.Sprintf("%v", value)
}

func (s *sql09Service) queryRows(t *testing.T, sql string, args ...any) [][]string {
	t.Helper()
	rows, err := s.iso.Query(s.ctx, sql, args...)
	if err != nil {
		s.fail("execute statement: "+sql, err)
	}
	defer rows.Close()
	var out [][]string
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			s.fail("read row", err)
		}
		row := make([]string, len(values))
		for index, value := range values {
			row[index] = sql09StringifyCell(value)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		s.fail("finish statement", err)
	}
	return out
}

func (s *sql09Service) witness(t *testing.T, sql string, args ...any) [][]string {
	t.Helper()
	return s.queryRows(t, sql, args...)
}

// sql09IsNullProbe wraps a rendered statement as a subquery and asks the
// server, not the driver, which output cells are SQL NULL. Because the
// driver decodes the jsonb 'null' scalar to Go nil like SQL NULL, only
// this server-side probe can prove that OUTER pad rows carry SQL NULL
// item/ordinality while every surviving item row carries present values.
func (s *sql09Service) sql09IsNullProbe(t *testing.T, rendered compiler.RenderedPostgresPhysicalPlan, columns ...string) [][]string {
	t.Helper()
	selects := make([]string, 0, len(columns))
	for _, column := range columns {
		selects = append(selects, column+" IS NULL")
	}
	sql := "SELECT " + strings.Join(selects, ", ") + " FROM (" + rendered.SQL + ") AS sql09_probe"
	return s.queryRows(t, sql, parameterValues(rendered.Args)...)
}

func parameterValues(args []compiler.PostgreSQLParameter) []any {
	values := make([]any, len(args))
	for index, parameter := range args {
		values[index] = parameter.Value
	}
	return values
}

// sql09CanonicalCell canonicalizes a stringified cell so JSON values
// compare by structure (jsonb text orders and spaces keys its own way)
// while non-JSON cells such as "<null>" and "true" compare literally.
func sql09CanonicalCell(cell string) string {
	var value any
	if err := json.Unmarshal([]byte(cell), &value); err != nil {
		return cell
	}
	out, err := json.Marshal(value)
	if err != nil {
		return cell
	}
	return string(out)
}

func sql09CanonicalRows(rows [][]string) [][]string {
	out := make([][]string, len(rows))
	for index, row := range rows {
		cells := make([]string, len(row))
		for cellIndex, cell := range row {
			cells[cellIndex] = sql09CanonicalCell(cell)
		}
		out[index] = cells
	}
	return out
}

const (
	sql09Project    = "sql09-proj"
	sql09Generation = "gen-1"
)

func sql09JSON(t *testing.T, document string) json.RawMessage {
	t.Helper()
	if !json.Valid([]byte(document)) {
		t.Fatalf("fixture document is not valid JSON: %s", document)
	}
	return json.RawMessage(document)
}

func sql09CanonicalObject(t *testing.T, document string) string {
	t.Helper()
	return sql09CanonicalCell(string(sql09JSON(t, document)))
}

// sql09PatientUnnestPlan builds the registered INNER or OUTER plan over the
// Patient identifier[] expansion.
func sql09PatientUnnestPlan(policy ir.PhysicalUnnestEmptyPolicy) ir.PhysicalPlan {
	plan := scopedPlan(sql09Project, sql09Generation, []string{"FHIR/Patient", "FHIR/Observation"}, false)
	plan.Operations = append(plan.Operations, unnestOp(identifierExpansion(policy)))
	return withReturn(plan, expansionProjections()...)
}

// sql09ObservationUnnestPlan builds the registered plan over the nested
// Observation component[].code.coding[] expansion with ancestor capture.
func sql09ObservationUnnestPlan(policy ir.PhysicalUnnestEmptyPolicy) ir.PhysicalPlan {
	plan := scopedPlan(sql09Project, sql09Generation, []string{"FHIR/Patient", "FHIR/Observation"}, false)
	plan.BindVars["root_collection"] = "Observation"
	plan.Operations = append(plan.Operations, unnestOp(codingExpansion(policy)))
	return withReturn(plan,
		valueProjection("logical_id", "root", "logical_id"),
		valueProjection("component", "component_item"),
		valueProjection("item", "item"),
		valueProjection("position", "position"),
		valueProjection("has_item", "has_item"),
	)
}

func sql09Binding(t *testing.T, svc *sql09Service) ir.PostgresStorageBinding {
	t.Helper()
	binding, err := ir.NewPostgresStorageBinding(svc.schema)
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding: %v", err)
	}
	return binding
}

// ---------------------------------------------------------------------------
// Case 1: INNER over missing / JSON null / [] / [null] / duplicates.
// ---------------------------------------------------------------------------

func TestPG18UnnestInnerEmitsLiteralRowsAndPreservesDuplicates(t *testing.T) {
	svc := newSQL09Service(t)
	svc.insertResource(t, sql09Project, sql09Generation, "Patient", "p-missing", "FHIR/Patient", sql09JSON(t, `{}`))
	svc.insertResource(t, sql09Project, sql09Generation, "Patient", "p-null", "FHIR/Patient", sql09JSON(t, `{"identifier": null}`))
	svc.insertResource(t, sql09Project, sql09Generation, "Patient", "p-empty", "FHIR/Patient", sql09JSON(t, `{"identifier": []}`))
	svc.insertResource(t, sql09Project, sql09Generation, "Patient", "p-nopayload", "FHIR/Patient", nil)
	svc.insertResource(t, sql09Project, sql09Generation, "Patient", "p-nullitem", "FHIR/Patient", sql09JSON(t, `{"identifier": [null]}`))
	dupA := `{"system": "urn:x", "value": "a"}`
	svc.insertResource(t, sql09Project, sql09Generation, "Patient", "p-dups", "FHIR/Patient",
		sql09JSON(t, `{"identifier": [`+dupA+`, `+dupA+`, {"system": "urn:x", "value": "b"}, `+dupA+`]}`))
	svc.insertResource(t, sql09Project, sql09Generation, "Patient", "p-dup-scalars", "FHIR/Patient",
		sql09JSON(t, `{"identifier": ["a", "a", "b", "a"]}`))
	// The AQL reference (terminal FILTER ... != null) drops null items and
	// then numbers survivors, so ["a", null, "b"] must yield ordinality
	// 0 and 1 — the post-filter dense index, not stored positions 0 and 2.
	svc.insertResource(t, sql09Project, sql09Generation, "Patient", "p-mixed", "FHIR/Patient",
		sql09JSON(t, `{"identifier": ["a", null, "b"]}`))
	// AQL ternary parity at the terminal: a stored falsy scalar (0) is the
	// empty source; a truthy scalar (5) becomes its single item row.
	svc.insertResource(t, sql09Project, sql09Generation, "Patient", "p-falsy-zero", "FHIR/Patient", sql09JSON(t, `{"identifier": 0}`))
	svc.insertResource(t, sql09Project, sql09Generation, "Patient", "p-truthy-scalar", "FHIR/Patient", sql09JSON(t, `{"identifier": 5}`))
	svc.insertResource(t, "sql09-other-project", sql09Generation, "Patient", "p-scope", "FHIR/Patient",
		sql09JSON(t, `{"identifier": [{"value": "out-of-scope"}]}`))

	rendered, err := RenderUnnest(sql09PatientUnnestPlan(ir.PhysicalUnnestExclude), sql09Binding(t, svc))
	if err != nil {
		t.Fatalf("RenderUnnest: %v", err)
	}
	got := sql09CanonicalRows(svc.runRendered(t, rendered))

	want := [][]string{
		{"p-dups", sql09CanonicalObject(t, dupA), "0", "true"},
		{"p-dups", sql09CanonicalObject(t, dupA), "1", "true"},
		{"p-dups", sql09CanonicalObject(t, `{"system": "urn:x", "value": "b"}`), "2", "true"},
		{"p-dups", sql09CanonicalObject(t, dupA), "3", "true"},
		{"p-dup-scalars", "a", "0", "true"},
		{"p-dup-scalars", "a", "1", "true"},
		{"p-dup-scalars", "b", "2", "true"},
		{"p-dup-scalars", "a", "3", "true"},
		{"p-mixed", "a", "0", "true"},
		{"p-mixed", "b", "1", "true"},
		{"p-truthy-scalar", "5", "0", "true"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("INNER rows = %v, want %v", got, want)
	}
	// Server-side authority: no emitted item is SQL NULL; the terminal
	// filter removed stored null items, so an emitted item is always a
	// present jsonb value.
	itemNull := svc.sql09IsNullProbe(t, rendered, "c1")
	if len(itemNull) != len(want) {
		t.Fatalf("item IS NULL probe = %v, want one boolean per emitted row", itemNull)
	}
	for index, row := range itemNull {
		if row[0] != "false" {
			t.Fatalf("INNER row %d item cell IS NULL = %s, want false", index, row[0])
		}
	}

	// Witness 1: INNER emits no rows for the empty-source forms — the
	// missing key, JSON null, [], SQL-NULL payload, and, per the AQL
	// reference, [null] (its only item is a dropped terminal null) and the
	// falsy scalar 0 — and none for the out-of-scope row.
	for _, absent := range []string{"p-missing", "p-null", "p-empty", "p-nopayload", "p-nullitem", "p-falsy-zero", "p-scope"} {
		for _, row := range got {
			// Exact logical-id comparison: "p-null" is a proper prefix
			// of "p-nullitem", so substring matching would false-positive.
			if row[0] == absent {
				t.Fatalf("INNER emitted empty/out-of-scope source %q: %v", absent, row)
			}
		}
	}
	for _, row := range got {
		if strings.Contains(strings.Join(row, "|"), "out-of-scope") {
			t.Fatalf("INNER emitted out-of-scope payload: %v", row)
		}
	}
	// Witness 2: duplicate preservation is real — p-dups stores four
	// identifier elements with only two distinct values; the expansion
	// must show all four positions.
	dupCount := svc.witness(t,
		`SELECT count(*), count(DISTINCT e ->> 'value') FROM "`+svc.schema+`"."fhir_resource" r,`+
			` jsonb_array_elements(r.payload -> 'identifier') e`+
			` WHERE r.project=$1 AND r.dataset_generation=$2 AND r.logical_id='p-dups'`,
		sql09Project, sql09Generation)
	if len(dupCount) != 1 || dupCount[0][0] != "4" || dupCount[0][1] != "2" {
		t.Fatalf("duplicate witness = %v, want 4 elements / 2 distinct values", dupCount)
	}
	// Witness 3: [null] really stores a JSON null element that the
	// expansion drops (the AQL terminal FILTER behavior) — the stored
	// element's jsonb_typeof is 'null', and the surviving population is
	// zero; p-mixed likewise survives two items, not three.
	nullSurvivors := svc.witness(t,
		`SELECT logical_id, count(e) FILTER (WHERE jsonb_typeof(e) <> 'null') FROM "`+svc.schema+`"."fhir_resource" r,`+
			` jsonb_array_elements(r.payload -> 'identifier') e`+
			` WHERE r.project=$1 AND r.dataset_generation=$2 AND r.logical_id IN ('p-nullitem','p-mixed')`+
			` GROUP BY logical_id ORDER BY min(resource_seq)`,
		sql09Project, sql09Generation)
	if len(nullSurvivors) != 2 || nullSurvivors[0][0] != "p-nullitem" || nullSurvivors[0][1] != "0" ||
		nullSurvivors[1][0] != "p-mixed" || nullSurvivors[1][1] != "2" {
		t.Fatalf("null-survivor witness = %v, want p-nullitem=0 survivors and p-mixed=2", nullSurvivors)
	}
	// Witness 4: every source form the AQL reference treats as empty —
	// missing key, JSON null, [], SQL-NULL payload, [null] (empty after
	// the terminal null-item filter), and the stored falsy scalar 0 (the
	// ternary's falsy set) — contributes zero expansion rows.
	emptyForms := svc.witness(t,
		`SELECT logical_id, CASE`+
			` WHEN payload -> 'identifier' IS NULL OR jsonb_typeof(payload -> 'identifier') IN ('null','false') THEN 0`+
			` WHEN jsonb_typeof(payload -> 'identifier') = 'array'`+
			` THEN (SELECT count(*) FROM jsonb_array_elements(payload -> 'identifier') e WHERE jsonb_typeof(e) <> 'null')`+
			` WHEN payload -> 'identifier' = '0'::jsonb OR payload -> 'identifier' = '""'::jsonb THEN 0`+
			` ELSE -1 END FROM "`+svc.schema+`"."fhir_resource"`+
			` WHERE project=$1 AND dataset_generation=$2`+
			` AND logical_id IN ('p-missing','p-null','p-empty','p-nopayload','p-nullitem','p-falsy-zero') ORDER BY resource_seq`,
		sql09Project, sql09Generation)
	if len(emptyForms) != 6 {
		t.Fatalf("empty-source witness rows = %v, want six zero-row sources", emptyForms)
	}
	for _, row := range emptyForms {
		if row[1] != "0" {
			t.Fatalf("empty-source %s has %s surviving items, want 0", row[0], row[1])
		}
	}
}

// ---------------------------------------------------------------------------
// Case 2: OUTER emits exactly one null-item row per empty source.
// ---------------------------------------------------------------------------

func TestPG18UnnestOuterEmitsOneNullItemRowPerEmptySource(t *testing.T) {
	svc := newSQL09Service(t)
	svc.insertResource(t, sql09Project, sql09Generation, "Patient", "p-missing", "FHIR/Patient", sql09JSON(t, `{}`))
	svc.insertResource(t, sql09Project, sql09Generation, "Patient", "p-null", "FHIR/Patient", sql09JSON(t, `{"identifier": null}`))
	svc.insertResource(t, sql09Project, sql09Generation, "Patient", "p-empty", "FHIR/Patient", sql09JSON(t, `{"identifier": []}`))
	svc.insertResource(t, sql09Project, sql09Generation, "Patient", "p-nopayload", "FHIR/Patient", nil)
	svc.insertResource(t, sql09Project, sql09Generation, "Patient", "p-nullitem", "FHIR/Patient", sql09JSON(t, `{"identifier": [null]}`))
	dupA := `{"system": "urn:x", "value": "a"}`
	svc.insertResource(t, sql09Project, sql09Generation, "Patient", "p-dups", "FHIR/Patient",
		sql09JSON(t, `{"identifier": [`+dupA+`, `+dupA+`, {"system": "urn:x", "value": "b"}, `+dupA+`]}`))
	svc.insertResource(t, sql09Project, sql09Generation, "Patient", "p-dup-scalars", "FHIR/Patient",
		sql09JSON(t, `{"identifier": ["a", "a", "b", "a"]}`))
	svc.insertResource(t, sql09Project, sql09Generation, "Patient", "p-mixed", "FHIR/Patient",
		sql09JSON(t, `{"identifier": ["a", null, "b"]}`))
	// Per the AQL reference both are empty sources: [null] holds only a
	// dropped terminal null item, and 0 is a ternary falsy — OUTER pads
	// each exactly once, like every other empty form.
	svc.insertResource(t, sql09Project, sql09Generation, "Patient", "p-falsy-zero", "FHIR/Patient", sql09JSON(t, `{"identifier": 0}`))
	svc.insertResource(t, sql09Project, sql09Generation, "Patient", "p-truthy-scalar", "FHIR/Patient", sql09JSON(t, `{"identifier": 5}`))

	rendered, err := RenderUnnest(sql09PatientUnnestPlan(ir.PhysicalUnnestPreserveParent), sql09Binding(t, svc))
	if err != nil {
		t.Fatalf("RenderUnnest: %v", err)
	}
	got := sql09CanonicalRows(svc.runRendered(t, rendered))

	pad := func(id string) []string { return []string{id, "<null>", "<null>", "false"} }
	want := [][]string{
		pad("p-missing"), pad("p-null"), pad("p-empty"), pad("p-nopayload"), pad("p-nullitem"),
		{"p-dups", sql09CanonicalObject(t, dupA), "0", "true"},
		{"p-dups", sql09CanonicalObject(t, dupA), "1", "true"},
		{"p-dups", sql09CanonicalObject(t, `{"system": "urn:x", "value": "b"}`), "2", "true"},
		{"p-dups", sql09CanonicalObject(t, dupA), "3", "true"},
		{"p-dup-scalars", "a", "0", "true"},
		{"p-dup-scalars", "a", "1", "true"},
		{"p-dup-scalars", "b", "2", "true"},
		{"p-dup-scalars", "a", "3", "true"},
		{"p-mixed", "a", "0", "true"},
		{"p-mixed", "b", "1", "true"},
		pad("p-falsy-zero"),
		{"p-truthy-scalar", "5", "0", "true"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OUTER rows = %v, want %v", got, want)
	}

	// Witness: the pad rows come from the LEFT JOIN, never from data —
	// a hand-written AQL-reference pipeline (falsy set to [], truthy
	// scalar wrapped once, null items dropped) over the same rows yields
	// exactly the eleven non-pad rows, so the six extra rows are pad.
	dataRows := svc.witness(t,
		`SELECT r.logical_id FROM "`+svc.schema+`"."fhir_resource" r,`+
			` jsonb_array_elements(CASE`+
			` WHEN r.payload -> 'identifier' IS NULL THEN '[]'::jsonb`+
			` WHEN jsonb_typeof(r.payload -> 'identifier') = 'array' THEN r.payload -> 'identifier'`+
			` WHEN r.payload -> 'identifier' IN ('null'::jsonb, 'false'::jsonb, '0'::jsonb, '""'::jsonb) THEN '[]'::jsonb`+
			` ELSE jsonb_build_array(r.payload -> 'identifier') END) e`+
			` WHERE jsonb_typeof(e) <> 'null'`+
			` AND r.project=$1 AND r.dataset_generation=$2 AND r.auth_resource_path = ANY ('{FHIR/Patient,FHIR/Observation}'::text[])`+
			` ORDER BY r.resource_seq`,
		sql09Project, sql09Generation)
	if len(dataRows) != 11 {
		t.Fatalf("witness item rows = %d rows %v, want the eleven non-pad rows", len(dataRows), dataRows)
	}
	// Pad rows: exactly the six empty sources ([null] and falsy 0 among
	// them), each exactly once. has_item is a total boolean and never
	// SQL NULL; pad item and ordinality are server-side SQL NULL.
	nullProbe := svc.sql09IsNullProbe(t, rendered, "c1", "c2", "c3")
	if len(nullProbe) != len(want) {
		t.Fatalf("pad probe = %v, want one row per OUTER row", nullProbe)
	}
	pads := 0
	for index, row := range nullProbe {
		isPad := want[index][3] == "false"
		wantItemNull, wantOrdinalityNull := "false", "false"
		if isPad {
			wantItemNull, wantOrdinalityNull = "true", "true"
			pads++
			if row[0] != "true" || row[1] != "true" {
				t.Fatalf("OUTER pad row %d (logical_id %s): item/ordinality IS NULL = %s/%s, want true/true",
					index, got[index][0], row[0], row[1])
			}
		} else if row[0] != wantItemNull || row[1] != wantOrdinalityNull {
			t.Fatalf("OUTER item row %d (logical_id %s): item/ordinality IS NULL = %s/%s, want false/false",
				index, got[index][0], row[0], row[1])
		}
		if row[2] != "false" {
			t.Fatalf("OUTER row %d: has_item is SQL NULL; it is a total boolean, never SQL NULL", index)
		}
	}
	if pads != 6 {
		t.Fatalf("pad rows = %d, want exactly one per empty source (6)", pads)
	}
}

// ---------------------------------------------------------------------------
// Case 3: nested repeated arrays — row-major order, ancestry, OUTER pads.
// ---------------------------------------------------------------------------

func TestPG18UnnestNestedRepeatedArraysPreserveOrderAndAncestry(t *testing.T) {
	svc := newSQL09Service(t)
	componentZero := `{"code": {"coding": [{"system": "urn:sys1", "code": "c1"}, {"system": "urn:sys1", "code": "c2"}]}}`
	componentOne := `{"code": {"coding": [{"system": "urn:sys2", "code": "c9"}]}}`
	svc.insertResource(t, sql09Project, sql09Generation, "Observation", "o-nested", "FHIR/Observation",
		sql09JSON(t, `{"component": [`+componentZero+`, `+componentOne+`]}`))
	svc.insertResource(t, sql09Project, sql09Generation, "Observation", "o-empty-components", "FHIR/Observation", sql09JSON(t, `{"component": []}`))
	svc.insertResource(t, sql09Project, sql09Generation, "Observation", "o-null-components", "FHIR/Observation", sql09JSON(t, `{"component": null}`))
	svc.insertResource(t, sql09Project, sql09Generation, "Observation", "o-missing", "FHIR/Observation", sql09JSON(t, `{}`))
	// A stored falsy scalar at a prefix iterate field is AQL-ternary-empty:
	// zero INNER rows, exactly one OUTER pad, no execution error.
	svc.insertResource(t, sql09Project, sql09Generation, "Observation", "o-falsy-zero", "FHIR/Observation", sql09JSON(t, `{"component": 0}`))

	inner, err := RenderUnnest(sql09ObservationUnnestPlan(ir.PhysicalUnnestExclude), sql09Binding(t, svc))
	if err != nil {
		t.Fatalf("RenderUnnest INNER: %v", err)
	}
	got := sql09CanonicalRows(svc.runRendered(t, inner))

	want := [][]string{
		{"o-nested", sql09CanonicalObject(t, componentZero), sql09CanonicalObject(t, `{"system": "urn:sys1", "code": "c1"}`), "0", "true"},
		{"o-nested", sql09CanonicalObject(t, componentZero), sql09CanonicalObject(t, `{"system": "urn:sys1", "code": "c2"}`), "1", "true"},
		{"o-nested", sql09CanonicalObject(t, componentOne), sql09CanonicalObject(t, `{"system": "urn:sys2", "code": "c9"}`), "2", "true"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("nested INNER rows = %v, want row-major carrier order %v", got, want)
	}

	// Witness: the ancestor column carries the exact enclosing component
	// object, element-for-element from the stored array.
	stored := svc.witness(t,
		`SELECT payload -> 'component' -> 0, payload -> 'component' -> 1 FROM "`+svc.schema+`"."fhir_resource"`+
			` WHERE project=$1 AND dataset_generation=$2 AND logical_id='o-nested'`,
		sql09Project, sql09Generation)
	if len(stored) != 1 {
		t.Fatalf("stored components witness = %v", stored)
	}
	enclosing := []string{sql09CanonicalCell(stored[0][0]), sql09CanonicalCell(stored[0][1])}
	for index, row := range want {
		expectedEnclosing := enclosing[0]
		if index == 2 {
			expectedEnclosing = enclosing[1]
		}
		if row[1] != expectedEnclosing {
			t.Fatalf("row %d ancestor = %v, want the exact enclosing component %v", index, row[1], expectedEnclosing)
		}
	}

	// OUTER pads each empty nested source exactly once; INNER drops it.
	outer, err := RenderUnnest(sql09ObservationUnnestPlan(ir.PhysicalUnnestPreserveParent), sql09Binding(t, svc))
	if err != nil {
		t.Fatalf("RenderUnnest OUTER: %v", err)
	}
	gotOuter := sql09CanonicalRows(svc.runRendered(t, outer))
	if len(gotOuter) != 7 {
		t.Fatalf("nested OUTER rows = %v, want three items plus four pads", gotOuter)
	}
	for index, row := range gotOuter[3:] {
		wantID := []string{"o-empty-components", "o-null-components", "o-missing", "o-falsy-zero"}[index]
		if !reflect.DeepEqual(row, []string{wantID, "<null>", "<null>", "<null>", "false"}) {
			t.Fatalf("nested OUTER pad %d = %v, want one null-item row for %s", index, row, wantID)
		}
	}

	// Witness: the AQL-reference pipeline (falsy set empty, arrays
	// expanded, no terminal-null filtering needed at the prefix) counts
	// three carriers, so the four pads are renderer-generated, not data.
	carriers := svc.witness(t,
		`SELECT count(*) FROM "`+svc.schema+`"."fhir_resource" r,`+
			` jsonb_array_elements(CASE WHEN r.payload -> 'component' IS NULL`+
			`  OR jsonb_typeof(r.payload -> 'component') <> 'array' THEN '[]'::jsonb ELSE r.payload -> 'component' END) c,`+
			` jsonb_array_elements(CASE WHEN c -> 'code' -> 'coding' IS NULL`+
			`  OR jsonb_typeof(c -> 'code' -> 'coding') <> 'array' THEN '[]'::jsonb ELSE c -> 'code' -> 'coding' END) e`+
			` WHERE r.project=$1 AND r.dataset_generation=$2`,
		sql09Project, sql09Generation)
	if len(carriers) != 1 || carriers[0][0] != "3" {
		t.Fatalf("carrier witness = %v, want 3", carriers)
	}
}

// TestPG18UnnestPrefixTruthyScalarRaisesLikeAQL pins the other side of the
// AQL ternary: a truthy non-array at a prefix iterate field is not silently
// empty — AQL's FOR-over-non-array raises, and the PostgreSQL expansion must
// raise through jsonb_array_elements instead of inventing rows.
func TestPG18UnnestPrefixTruthyScalarRaisesLikeAQL(t *testing.T) {
	svc := newSQL09Service(t)
	svc.insertResource(t, sql09Project, sql09Generation, "Observation", "o-truthy-prefix", "FHIR/Observation",
		sql09JSON(t, `{"component": 5}`))
	outer, err := RenderUnnest(sql09ObservationUnnestPlan(ir.PhysicalUnnestPreserveParent), sql09Binding(t, svc))
	if err != nil {
		t.Fatalf("RenderUnnest OUTER: %v", err)
	}
	// pgx surfaces a server-side query error through rows.Err(), not the
	// Query return; drain and Close so the borrowed connection returns
	// to the pool either way.
	rows, err := svc.iso.Query(svc.ctx, outer.SQL, parameterValues(outer.Args)...)
	if err == nil {
		for rows.Next() {
		}
		err = rows.Err()
		rows.Close()
	}
	if err == nil {
		t.Fatalf("truthy scalar prefix expanded without error; AQL parity requires an execution error")
	}
	// The falsy sibling must not error: prove the guard by rendering the
	// same shape with the falsy value and reading zero INNER rows.
	svc2 := newSQL09Service(t)
	svc2.insertResource(t, sql09Project, sql09Generation, "Observation", "o-falsy-false", "FHIR/Observation",
		sql09JSON(t, `{"component": false}`))
	svc2.insertResource(t, sql09Project, sql09Generation, "Observation", "o-falsy-string", "FHIR/Observation",
		sql09JSON(t, `{"component": ""}`))
	inner, err := RenderUnnest(sql09ObservationUnnestPlan(ir.PhysicalUnnestExclude), sql09Binding(t, svc2))
	if err != nil {
		t.Fatalf("RenderUnnest INNER: %v", err)
	}
	if rows := svc2.runRendered(t, inner); len(rows) != 0 {
		t.Fatalf("falsy prefix rows = %v, want none (AQL ternary falsy set)", rows)
	}
}

// ---------------------------------------------------------------------------
// Case 4: owner route over typed edges keeps parent identity, duplicate
// edge multiplicity, and per-occurrence expansion order.
// ---------------------------------------------------------------------------

func TestPG18UnnestOwnerRouteKeepsParentIdentityAndEdgeMultiplicity(t *testing.T) {
	svc := newSQL09Service(t)
	svc.insertResource(t, sql09Project, sql09Generation, "Patient", "p-route", "FHIR/Patient", sql09JSON(t, `{"gender": "female"}`))
	codingSet := func(system, code string) string {
		return `{"component": [{"code": {"coding": [{"system": "` + system + `", "code": "` + code + `"}]}}]}`
	}
	svc.insertResource(t, sql09Project, sql09Generation, "Observation", "o-obs1", "FHIR/Observation", sql09JSON(t, codingSet("urn:obs1", "o1")))
	svc.insertResource(t, sql09Project, sql09Generation, "Observation", "o-obs2", "FHIR/Observation", sql09JSON(t, codingSet("urn:obs2", "o2")))
	for index, target := range []string{"o-obs1", "o-obs1", "o-obs2"} {
		svc.insertEdge(t, pgstore.EdgeOccurrenceRow{
			Project: sql09Project, DatasetGeneration: sql09Generation, AuthResourcePath: "FHIR/Patient",
			Label: "has-observation", SourceResourceType: "Patient", SourceLogicalID: "p-route",
			TargetResourceType: "Observation", TargetLogicalID: target,
			SourcePath: fmt.Sprintf("observation[%d]", index), SourceOrdinal: int64(index),
		})
	}

	plan := scopedPlan(sql09Project, sql09Generation, []string{"FHIR/Patient", "FHIR/Observation"}, false)
	// The PostgreSQL edge table comes from the storage registry, so the
	// collection bind is unread plan data: a sentinel proves no bind
	// value can reach SQL text.
	plan.BindVars["edge_collection"] = "SENTINEL-unused-edge-collection-bind"
	plan.BindVars["edge_label"] = "has-observation"
	plan.BindVars["target_type"] = "Observation"
	unnest := codingExpansion(ir.PhysicalUnnestPreserveParent)
	unnest.Owner = ir.PhysicalUnnestOwner{
		OccurrenceID: "obs-owner", ResourceType: "Observation", RootVariable: "root", OwnerVariable: "obs",
		Route: []ir.PhysicalUnnestRouteStep{{OccurrenceID: "obs-owner", Traversal: ir.PhysicalTraversal{
			SourceVariable: "root", TargetVariable: "obs", EdgeVariable: "obs_edge",
			Direction: ir.PhysicalOutbound, EdgeCollectionBindKey: "edge_collection",
			EdgeLabelBindKey: "edge_label", TargetTypeBindKey: "target_type",
			EdgeTargetTypeField: "to_type", Strategy: ir.PhysicalTraversalNative,
		}}},
	}
	unnest.Expression = arrayExtract("obs", "Observation",
		spec.SelectorStep{Field: "component", Iterate: true},
		spec.SelectorStep{Field: "code"},
		spec.SelectorStep{Field: "coding", Iterate: true})
	plan.Operations = append(plan.Operations, unnestOp(unnest))
	plan = withReturn(plan,
		valueProjection("patient_id", "root", "logical_id"),
		valueProjection("observation_id", "obs", "logical_id"),
		valueProjection("item", "item"),
		valueProjection("position", "position"),
		valueProjection("has_item", "has_item"),
	)
	rendered, err := RenderUnnest(plan, sql09Binding(t, svc))
	if err != nil {
		t.Fatalf("RenderUnnest: %v", err)
	}
	got := sql09CanonicalRows(svc.runRendered(t, rendered))

	want := [][]string{
		{"p-route", "o-obs1", sql09CanonicalObject(t, `{"system": "urn:obs1", "code": "o1"}`), "0", "true"},
		{"p-route", "o-obs1", sql09CanonicalObject(t, `{"system": "urn:obs1", "code": "o1"}`), "0", "true"},
		{"p-route", "o-obs2", sql09CanonicalObject(t, `{"system": "urn:obs2", "code": "o2"}`), "0", "true"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("owner-route rows = %v, want the duplicate occurrence expanded per edge %v", got, want)
	}

	// Witness: the duplicate edge occurrence is stored as two rows and
	// the direct join expands each exactly once; parent identity columns
	// stay attributed to the same patient across all expansions.
	storedEdges := svc.witness(t,
		`SELECT count(*) FROM "`+svc.schema+`"."fhir_edge_occurrence" WHERE project=$1 AND dataset_generation=$2 AND label='has-observation'`,
		sql09Project, sql09Generation)
	if len(storedEdges) != 1 || storedEdges[0][0] != "3" {
		t.Fatalf("stored edge occurrences = %v, want 3", storedEdges)
	}
	witnessRows := svc.witness(t,
		`SELECT r0.logical_id, r1.logical_id, e.item ->> 'code' FROM "`+svc.schema+`"."fhir_resource" r0`+
			` JOIN "`+svc.schema+`"."fhir_edge_occurrence" e0 ON e0.project=r0.project AND e0.dataset_generation=r0.dataset_generation`+
			` AND e0.label='has-observation' AND e0.source_resource_type='Patient' AND e0.source_logical_id=r0.logical_id`+
			` JOIN "`+svc.schema+`"."fhir_resource" r1 ON r1.project=e0.project AND r1.dataset_generation=e0.dataset_generation`+
			` AND r1.resource_type='Observation' AND r1.logical_id=e0.target_logical_id`+
			` CROSS JOIN LATERAL jsonb_array_elements(r1.payload -> 'component') c`+
			` CROSS JOIN LATERAL jsonb_array_elements(c -> 'code' -> 'coding') e(item)`+
			` WHERE r0.project=$1 AND r0.dataset_generation=$2 AND r0.logical_id='p-route'`+
			` ORDER BY r0.resource_seq, e0.edge_seq`,
		sql09Project, sql09Generation)
	if len(witnessRows) != 3 {
		t.Fatalf("witness rows = %v, want 3", witnessRows)
	}
	for index, row := range witnessRows {
		if row[0] != want[index][0] || row[1] != want[index][1] {
			t.Fatalf("witness row %d identity = %v, want %v", index, row, want[index])
		}
	}
}

// ---------------------------------------------------------------------------
// Case 5: scope fences the expansion and hostile selectors stay inert.
// ---------------------------------------------------------------------------

func TestPG18UnnestScopeFencesExpandedRows(t *testing.T) {
	svc := newSQL09Service(t)
	svc.insertResource(t, sql09Project, sql09Generation, "Patient", "p-in", "FHIR/Patient", sql09JSON(t, `{"identifier": [{"value": "seen"}]}`))
	// Same project, unauthorized auth path: AUTH_RESOURCE_PATH_ALLOWED is
	// part of every expanded row's scope, INNER and OUTER alike.
	svc.insertResource(t, sql09Project, sql09Generation, "Patient", "p-secret", "FHIR/Secret", sql09JSON(t, `{"identifier": [{"value": "leak"}]}`))
	// Same auth path, other dataset generation.
	svc.insertResource(t, sql09Project, "gen-2", "Patient", "p-gen2", "FHIR/Patient", sql09JSON(t, `{"identifier": [{"value": "leak2"}]}`))

	for _, policy := range []ir.PhysicalUnnestEmptyPolicy{ir.PhysicalUnnestExclude, ir.PhysicalUnnestPreserveParent} {
		rendered, err := RenderUnnest(sql09PatientUnnestPlan(policy), sql09Binding(t, svc))
		if err != nil {
			t.Fatalf("RenderUnnest(%s): %v", policy, err)
		}
		for _, row := range svc.runRendered(t, rendered) {
			joined := strings.Join(row, "|")
			if strings.Contains(joined, "leak") || row[0] == "p-secret" || row[0] == "p-gen2" {
				t.Fatalf("%s expansion leaked a row outside scope: %v", policy, row)
			}
		}
	}
	stored := svc.witness(t,
		`SELECT count(*) FROM "`+svc.schema+`"."fhir_resource" WHERE project=$1 AND logical_id IN ('p-secret','p-gen2')`,
		sql09Project)
	if len(stored) != 1 || stored[0][0] != "2" {
		t.Fatalf("witness must show both out-of-scope rows are stored: %v", stored)
	}
	// The selector field never reaches SQL text (parameterized path).
	rendered, err := RenderUnnest(sql09PatientUnnestPlan(ir.PhysicalUnnestExclude), sql09Binding(t, svc))
	if err != nil {
		t.Fatalf("RenderUnnest: %v", err)
	}
	if strings.Contains(rendered.SQL, "identifier") {
		t.Fatalf("selector path leaked into SQL text: %s", rendered.SQL)
	}
}

// ---------------------------------------------------------------------------
// Zero-leftover audit (runs last in file order).
// ---------------------------------------------------------------------------

func TestZZNoLeftoverSQL09Resources(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
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
		"SELECT count(*) FROM pg_database WHERE datname LIKE 'verify_sql09_%'").Scan(&databases); err != nil {
		t.Fatalf("count databases: %v", err)
	}
	if databases != 0 {
		t.Fatalf("%d verify_sql09_* databases still exist", databases)
	}
	var schemas int
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'verify_sql09_%'").Scan(&schemas); err != nil {
		t.Fatalf("count schemas: %v", err)
	}
	if schemas != 0 {
		t.Fatalf("%d verify_sql09_* schemas still exist in the service database", schemas)
	}
}
