package postgres

// Production-path acceptance witness for the pg-sql-11 child-set SLICE
// arm (Option A ruling, coordinator adjudication (b)). These tests take a
// real recipe bundle through the production pipeline — semantic.
// BuildRecipePlan, ResolveRecipePlan, lower.CompileResolvedRecipePlan with
// the DEFAULT optimization policy (compact projection default-on) — and
// prove, on real planner output rather than hand-built IR, that (1) the
// compact-projection producer attaches Extract.Prepared annotations to the
// RETURN-projection slice extracts and the slice predicate (the
// verified-producer evidence, lower/child_sets.go:552-555 and 573-579),
// and (2) the registered renderer renders such plans: the prepared
// references resolve against the set's registered projection slots and the
// slice renders over the validated item relation. The live leg executes
// the compiled statement on real PostgreSQL 18 (owned obj11_it_*
// namespace, tracked by the TestMain delta audit) over a production-plan
// compiled MULTI-ITEM child set: dedup, the _key default-sort tie-break
// (D4(c)), the LIMIT prefix, and the predicate arm. Hand-built canonical
// shapes stay in objects_test.go as renderer-level tests, per the ruling.

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	pgstore "github.com/calypr/loom/internal/store/postgres"
)

const (
	obj11ProdProject    = "proj-a"
	obj11ProdGeneration = "gen-1"
	obj11ProdEdgeLabel  = "subject_Patient"
)

// obj11WitnessBundle is the CORE production recipe shape the ruling names:
// a root resource, one optional child traversal, and a representative
// slice over that child whose fields re-read the child's own selectors —
// the shape the probe (logs/d3-probe.log) attests the compact-projection
// annotator stamps Extract.Prepared on. Root Observation, child Patient
// via the subject reference (the probe's gender/active child fields).
func obj11WitnessBundle(name string, limit int, whereNeedle string) recipe.Bundle {
	var where *recipe.Filter
	if whereNeedle != "" {
		needle := whereNeedle
		where = &recipe.Filter{Select: "occ_patient.gender", Operator: recipe.FilterEquals,
			Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &needle}}}
	}
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                name,
		TranslationVersion:  "test",
		Outputs: []recipe.Output{{
			Name: "observations", RootResourceType: "Observation", RowGrain: "resource",
			Fields: []recipe.Field{{Name: "status", Expr: recipe.Expression{Select: "root.status"}}},
			Traversals: []recipe.Traversal{{
				Name: obj11ProdEdgeLabel, Alias: "occ_patient", ToResourceType: "Patient", MatchMode: recipe.MatchOptional,
				// No plain Fields: the compact-projection collector
				// registers slots from the SLICE's own selectors
				// (lower/child_sets.go:463-470 collects predicate, sort,
				// and projections), so the annotation fires without the
				// reduced-field projection — whose presence-on-direct-value
				// combination is a separately registered gap
				// (selectors.go classifyProjection).
				Slices: []recipe.RepresentativeSlice{{
					Name: "representatives", Limit: limit, Where: where,
					Fields: []recipe.Field{
						{Name: "gender", Expr: recipe.Expression{Select: "occ_patient.gender"}},
						{Name: "active", Expr: recipe.Expression{Select: "occ_patient.active"}},
					},
				}},
			}},
		}},
	}
}

// obj11RegisteredSurfacePolicy is the production policy narrowed to the
// traversal shape the PostgreSQL spine registers: native typed-identity
// joins. The endpoint_traversal rule is default-ON for Arango, and
// endpoint-lookup plans fail closed on the PostgreSQL surface as their own
// separately registered capability gap (render.go package doc; the
// sets.go correlated-subplan contract pins the native shape) — a plan on
// that arm never reaches any RETURN projection, so it cannot evidence the
// slice surface. The rule ablation uses the sanctioned benchmark/parity
// surface (ir.PhysicalOptimizationPolicy.WithRule, the documented
// single-rule switch), keeping compact_set_projection — the Option A
// producer — at its production default.
func obj11RegisteredSurfacePolicy() ir.PhysicalOptimizationPolicy {
	return ir.DefaultPhysicalOptimizationPolicy().WithRule(ir.PhysicalOptimizationRuleEndpointTraversal, false)
}

// obj11CompileProduction runs the production lowering exactly as
// compiler.CompileRecipeOutput does for a plan-bearing output: lower via
// lower.CompileResolvedRecipePlan, take the finalized physical plan, no
// hand-shaping. The compile target only selects the renderer (the AQL
// target renders AQL text); the physical plan below is the identical
// production artifact the PostgreSQL compile path feeds the renderer.
func obj11CompileProductionWithPolicy(t *testing.T, bundle recipe.Bundle, policy ir.PhysicalOptimizationPolicy) ir.PhysicalPlan {
	t.Helper()
	bindings := recipe.RuntimeBindings{
		Project: obj11ProdProject, DatasetGeneration: obj11ProdGeneration,
		AuthResourcePaths: []string{"FHIR/Observation", "FHIR/Patient"},
	}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatalf("BuildRecipePlan: %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		t.Fatalf("ResolveRecipePlan: %v", err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, policy)
	if err != nil {
		t.Fatalf("lower.CompileResolvedRecipePlan: %v", err)
	}
	if len(compiled.Outputs) != 1 {
		t.Fatalf("compiled outputs = %d, want 1", len(compiled.Outputs))
	}
	output := compiled.Outputs[0]
	if output.OptimizedPlan != nil {
		return *output.OptimizedPlan
	}
	return output.Plan
}

// obj11CompileProduction compiles under the registered-surface policy
// (native traversal, compact projection production-default) — the policy
// the PostgreSQL spine executes child sets under.
func obj11CompileProduction(t *testing.T, bundle recipe.Bundle) ir.PhysicalPlan {
	t.Helper()
	return obj11CompileProductionWithPolicy(t, bundle, obj11RegisteredSurfacePolicy())
}

// obj11ProductionSlice locates the production RETURN-projection SLICE and
// the child set it reads, failing the witness when the planner did not
// emit the annotated production shape at all.
func obj11ProductionSlice(t *testing.T, plan ir.PhysicalPlan) (*ir.PhysicalSlice, *ir.PhysicalSet) {
	t.Helper()
	var set *ir.PhysicalSet
	for index := range plan.Operations {
		operation := plan.Operations[index]
		if operation.Kind == ir.PhysicalSetOp && operation.Set != nil &&
			operation.Set.Projection != nil && len(operation.Set.Projection.Fields) > 0 {
			set = operation.Set
		}
	}
	if set == nil {
		t.Fatalf("production plan carries no compact-projection child set with registered slots:\n%s", obj11PlanShape(plan))
	}
	for index := range plan.Operations {
		operation := plan.Operations[index]
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for projectionIndex := range operation.Return.Projections {
			expression := operation.Return.Projections[projectionIndex].Expression
			if expression != nil && expression.Kind == ir.PhysicalSliceExpression && expression.Slice != nil {
				if expression.Slice.Source.Variable != set.Variable {
					t.Fatalf("production SLICE reads %q, want the child set %q", expression.Slice.Source.Variable, set.Variable)
				}
				return expression.Slice, set
			}
		}
	}
	t.Fatalf("production plan carries no RETURN-projection SLICE:\n%s", obj11PlanShape(plan))
	return nil, nil
}

func obj11PlanShape(plan ir.PhysicalPlan) string {
	var b strings.Builder
	for index, operation := range plan.Operations {
		b.WriteString(string(operation.Kind))
		if operation.Set != nil {
			b.WriteString("(" + operation.Set.Variable + ")")
		}
		if index+1 < len(plan.Operations) {
			b.WriteString(" -> ")
		}
	}
	return b.String()
}

// TestObject11ProductionPlanProducerWitnessRenders is the offline
// acceptance leg: real planner output carries the Prepared annotations on
// the slice item extracts (the Option A mandate's premise), and the
// registered renderer renders the plan — the resolution, not a refusal.
func TestObject11ProductionPlanProducerWitnessRenders(t *testing.T) {
	plan := obj11CompileProduction(t, obj11WitnessBundle("obj11_producer_witness", 10, ""))
	slice, set := obj11ProductionSlice(t, plan)

	// Producer premise under the FULLY DEFAULT policy (compact projection
	// AND endpoint traversal as production ships them): the annotator
	// stamps the slice extracts regardless of the traversal strategy. This
	// is the d3-probe finding reproduced on real planner output inside the
	// test suite; rendering under that policy stays blocked by the
	// separately registered endpoint-lookup gap, not by anything in the
	// slice surface.
	defaultPlan := obj11CompileProductionWithPolicy(t, obj11WitnessBundle("obj11_default_policy_premise", 10, ""), ir.DefaultPhysicalOptimizationPolicy())
	defaultSlice, defaultSet := obj11ProductionSlice(t, defaultPlan)
	for _, projection := range defaultSlice.Projections {
		if projection.Expression.Extract == nil || projection.Expression.Extract.Prepared == nil {
			t.Fatalf("default-policy planner did NOT annotate slice projection %q (Option A premise broken — investigate the lowering): %+v", projection.Name, projection.Expression)
		}
		if projection.Expression.Extract.Prepared.SetVariable != defaultSet.Variable {
			t.Fatalf("default-policy annotation names %q, want set %q", projection.Expression.Extract.Prepared.SetVariable, defaultSet.Variable)
		}
	}

	// Producer witness: every slice item extract carries Extract.Prepared
	// naming this set and one of its registered slots — the production
	// annotation the probe attested (logs/d3-probe.log), now on real
	// planner output.
	registeredSlots := map[string]bool{}
	for _, field := range set.Projection.Fields {
		registeredSlots[field.Name] = true
	}
	for _, projection := range slice.Projections {
		extract := projection.Expression.Extract
		if extract == nil || extract.Prepared == nil {
			t.Fatalf("production planner did NOT annotate slice projection %q (Option A premise broken — investigate the lowering):\n%+v", projection.Name, projection.Expression)
		}
		if extract.Prepared.SetVariable != set.Variable || !registeredSlots[extract.Prepared.Field] {
			t.Fatalf("production annotation names %s.%v, want a slot of set %q (registered: %v)",
				extract.Prepared.SetVariable, extract.Prepared.Field, set.Variable, set.Projection.Fields)
		}
	}

	// Resolution witness: the plan renders (this is the shape that, under
	// the superseded Option B, failed closed — the parity gap the ruling
	// closed), over the validated item relation with the EMPTY_ON_NULL
	// aggregate, and the annotation never reaches SQL text.
	rendered, err := RenderPhysicalPlan(plan, mustBinding(t))
	if err != nil {
		t.Fatalf("production compact-projection slice plan must render after Option A: %v", err)
	}
	for _, want := range []string{"(SELECT DISTINCT ", "COALESCE(jsonb_agg(", `item_key ASC LIMIT $`} {
		if !strings.Contains(rendered.SQL, want) {
			t.Fatalf("rendered production statement lost %q:\n%s", want, rendered.SQL)
		}
	}
	for _, forbidden := range []string{"prepared", "__loom_projection", "__loom_reduced"} {
		if strings.Contains(rendered.SQL, forbidden) {
			t.Fatalf("%q leaked into rendered statement text:\n%s", forbidden, rendered.SQL)
		}
	}
}

// TestObject11ProductionPlanPredicateProducerWitnessRenders pins the
// predicate arm of the annotation on production output: the slice Where
// lowers to a typed predicate whose left extraction carries the Prepared
// reference, and the plan renders.
func TestObject11ProductionPlanPredicateProducerWitnessRenders(t *testing.T) {
	plan := obj11CompileProduction(t, obj11WitnessBundle("obj11_predicate_witness", 10, "male"))
	slice, set := obj11ProductionSlice(t, plan)
	if slice.Predicate == nil || slice.Predicate.Comparison == nil ||
		slice.Predicate.Comparison.LeftExpression == nil ||
		slice.Predicate.Comparison.LeftExpression.Extract == nil {
		t.Fatalf("production slice Where did not lower to the registered predicate shape: %+v", slice.Predicate)
	}
	prepared := slice.Predicate.Comparison.LeftExpression.Extract.Prepared
	if prepared == nil || prepared.SetVariable != set.Variable {
		t.Fatalf("production planner did NOT annotate the predicate extraction (Option A premise broken — investigate the lowering): %+v", slice.Predicate.Comparison.LeftExpression.Extract)
	}
	if _, err := RenderPhysicalPlan(plan, mustBinding(t)); err != nil {
		t.Fatalf("production predicate-arm plan must render after Option A: %v", err)
	}
}

// TestObject11LiveProductionPlanSliceLeg is the live acceptance leg (the
// tie-break leg the ruling requires over a production-plan compiled
// multi-item child set): dedup, the production _key default sort (D4(c)),
// the LIMIT prefix against a direct-SQL witness, the predicate arm, and
// the empty-array parent grain — on real planner output executed against
// real PostgreSQL 18.
func TestObject11LiveProductionPlanSliceLeg(t *testing.T) {
	svc := newOwnedService(t, 0, "obj11_it")
	insert := func(resourceType, logicalID, authPath string, payload map[string]any) {
		svc.insertResource(t, svc.schema, obj11ProdProject, obj11ProdGeneration, resourceType, logicalID, authPath, payload)
	}
	insert("Observation", "o-1", "FHIR/Observation", map[string]any{"status": "final"})
	insert("Observation", "o-2", "FHIR/Observation", map[string]any{"status": "final"})
	insert("Patient", "p-a", "FHIR/Patient", map[string]any{"gender": "female", "active": true})
	insert("Patient", "p-b", "FHIR/Patient", map[string]any{"gender": "female", "active": true})
	insert("Patient", "p-c", "FHIR/Patient", map[string]any{"gender": "male", "active": true})
	// Denied-auth child: the scope block must drop it from the items.
	insert("Patient", "p-denied", "FHIR/Denied", map[string]any{"gender": "denied", "active": true})
	edge := func(from, to, sourcePath string, ordinal int64, authPath string) {
		svc.insertEdge(t, pgstore.EdgeOccurrenceRow{
			Project: obj11ProdProject, DatasetGeneration: obj11ProdGeneration, AuthResourcePath: authPath,
			Label: obj11ProdEdgeLabel, SourceResourceType: "Observation", SourceLogicalID: from,
			TargetResourceType: "Patient", TargetLogicalID: to, SourcePath: sourcePath, SourceOrdinal: ordinal,
		})
	}
	edge("o-1", "p-a", "subject[0]", 0, "FHIR/Observation")
	edge("o-1", "p-b", "subject[0]", 0, "FHIR/Observation")
	edge("o-1", "p-b", "subject[1]", 1, "FHIR/Observation") // duplicate occurrence: dedups to one item
	edge("o-1", "p-c", "subject[0]", 0, "FHIR/Observation")
	edge("o-1", "p-denied", "subject[0]", 0, "FHIR/Denied")

	// Locate the slice cell positionally from the compiled RETURN.
	plan := obj11CompileProduction(t, obj11WitnessBundle("obj11_live_witness", 10, ""))
	slice, _ := obj11ProductionSlice(t, plan)
	_ = slice
	returnOp := plan.Operations[len(plan.Operations)-1]
	sliceIndex := -1
	for index, projection := range returnOp.Return.Projections {
		if projection.Expression != nil && projection.Expression.Kind == ir.PhysicalSliceExpression {
			sliceIndex = index
		}
	}
	if sliceIndex < 0 {
		t.Fatalf("slice projection not found in RETURN")
	}

	rows := map[string][]string{}
	for _, row := range svc.runRendered(t, obj11RenderOK(t, svc, plan)) {
		rows[row[0]] = row
	}
	if len(rows) != 2 {
		t.Fatalf("both root rows must survive (parent grain), got %v", rows)
	}
	// Production default sort is the _key identity (lower/root_projection.
	// go:403-406): items order p-a, p-b, p-c; the duplicate occurrence of
	// p-b contributes ONE item; the denied-auth patient is scope-dropped.
	const wantItems = "[map[active:true gender:female] map[active:true gender:female] map[active:true gender:male]]"
	if got := rows["o-1"][sliceIndex]; got != wantItems {
		t.Fatalf("production slice items = %q, want %q (dedup + _key identity order + auth scope)", got, wantItems)
	}
	if got := rows["o-2"][sliceIndex]; got != "[]" {
		t.Fatalf("childless root must render the EMPTY_ON_NULL array, got %q", got)
	}

	// LIMIT prefix leg: the compiled LIMIT (2) must yield exactly the
	// first two witness items (identity order over the deduped children).
	direct := svc.witness(t, `SELECT DISTINCT x.logical_id
		FROM "`+svc.schema+`"."fhir_edge_occurrence" e
		INNER JOIN "`+svc.schema+`"."fhir_resource" x
		  ON x.project = $1 AND x.dataset_generation = $2
		     AND x.auth_resource_path IN ('FHIR/Observation','FHIR/Patient')
		     AND x.resource_type = 'Patient' AND x.logical_id = e.target_logical_id
		WHERE e.project = $1 AND e.dataset_generation = $2
		  AND e.auth_resource_path IN ('FHIR/Observation','FHIR/Patient')
		  AND e.label = $3 AND e.source_resource_type = 'Observation'
		  AND e.source_logical_id = 'o-1' AND e.target_resource_type = 'Patient'
		ORDER BY x.logical_id ASC`, obj11ProdProject, obj11ProdGeneration, obj11ProdEdgeLabel)
	if len(direct) != 3 || direct[0][0] != "p-a" || direct[1][0] != "p-b" || direct[2][0] != "p-c" {
		t.Fatalf("direct witness order drifted (dedup?): %v", direct)
	}
	limitedPlan := obj11CompileProduction(t, obj11WitnessBundle("obj11_live_limited", 2, ""))
	limitedRows := map[string][]string{}
	for _, row := range svc.runRendered(t, obj11RenderOK(t, svc, limitedPlan)) {
		limitedRows[row[0]] = row
	}
	const wantPrefix = "[map[active:true gender:female] map[active:true gender:female]]"
	if got := limitedRows["o-1"][sliceIndex]; got != wantPrefix {
		t.Fatalf("LIMIT 2 must render the witness prefix, got %q", got)
	}

	// Predicate arm on production output: gender = "male".
	predicatePlan := obj11CompileProduction(t, obj11WitnessBundle("obj11_live_predicate", 10, "male"))
	predicateRows := map[string][]string{}
	for _, row := range svc.runRendered(t, obj11RenderOK(t, svc, predicatePlan)) {
		predicateRows[row[0]] = row
	}
	const wantMale = "[map[active:true gender:male]]"
	if got := predicateRows["o-1"][sliceIndex]; got != wantMale {
		t.Fatalf("production predicate arm = %q, want %q", got, wantMale)
	}
}
