package postgres

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

// The post-window surface (post_window.go) registers what the compiler scheduler legally emits
// after the execution window: dead child-route traversals, child sets, and SUM/COUNT/MIN/MAX
// AGGREGATE RETURN expressions over the registered child-set projection slots. The witness plan
// is the AuthorWorkload output aggregate_sum_age_by_patient from the #159 paired benchmark
// (compiler/output.go places the SORT window before the child-route LETs).

func postWindowSetField(name string, steps ...spec.SelectorStep) ir.PhysicalSetProjectionField {
	return ir.PhysicalSetProjectionField{
		Name: name, ResourceType: "Observation",
		Selector:      spec.Selector{Steps: steps},
		ExecutionMode: ir.PhysicalSelectorConditionalArray,
	}
}

func postWindowCodeSteps() []spec.SelectorStep {
	return []spec.SelectorStep{{Field: "code"}, {Field: "coding", Iterate: true}, {Field: "code"}}
}

func postWindowValueSteps() []spec.SelectorStep {
	return []spec.SelectorStep{{Field: "valueQuantity"}, {Field: "value"}}
}

// postWindowAggregateSet is the lowering's child set (lower/child_sets.go
// buildOptionalChildPhysicalSet): one depth-one hop plus its canonical scope block, two
// projection slots, no reduction — the set is consumed only by the RETURN aggregates.
func postWindowAggregateSet(endpointStrategy bool) ir.PhysicalSet {
	traversal := setsTraversal()
	if endpointStrategy {
		traversal.Strategy = ir.PhysicalTraversalEndpointLookup
		traversal.EndpointField, traversal.EndpointJoinField = "_from", "_to"
		traversal.EndpointIndexFields = []string{"_from", "project", "dataset_generation", "label", "to_type"}
	}
	operations := []ir.PhysicalOperation{{Kind: ir.PhysicalTraversalOp, Traversal: &traversal}}
	operations = appendScopeBlock(operations, []string{setsEdgeVariable, setsTargetVariable}, setsPrefix+"_scope_auth")
	return ir.PhysicalSet{
		Variable: setsPrefix,
		Subplan: ir.PhysicalSubplan{
			Captures:   []string{"root"},
			Operations: operations,
			Return: ir.PhysicalExpression{
				Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalObjectCardinality,
				NullBehavior: ir.PhysicalPreserveNull,
				Value:        &ir.PhysicalValue{Variable: setsTargetVariable},
			},
		},
		Unique:    true,
		SortByKey: true,
		Projection: &ir.PhysicalSetProjection{Fields: []ir.PhysicalSetProjectionField{
			postWindowSetField("__loom_projection_0", postWindowCodeSteps()...),
			(func() ir.PhysicalSetProjectionField {
				field := postWindowSetField("__loom_projection_1", postWindowValueSteps()...)
				field.ExecutionMode = ir.PhysicalSelectorDirectScalar
				return field
			})(),
		}},
	}
}

func postWindowSlotExpression(field string, mode ir.PhysicalSelectorExecutionMode, steps []spec.SelectorStep) *ir.PhysicalExpression {
	return &ir.PhysicalExpression{
		Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality,
		NullBehavior: ir.PhysicalEmptyOnNull,
		Extract: &ir.PhysicalExtract{
			Source:        ir.PhysicalValue{Variable: setsPrefix, Path: []string{"payload"}},
			ResourceType:  "Observation",
			Selector:      spec.Selector{Steps: steps},
			ExecutionMode: mode,
			Prepared:      &ir.PhysicalPreparedReference{SetVariable: setsPrefix, Field: field},
		},
	}
}

func postWindowAggregateProjection(name string, operation ir.PhysicalAggregateOperation, withValue, withPredicate bool) ir.PhysicalProjection {
	aggregate := &ir.PhysicalAggregate{
		Source:    ir.PhysicalValue{Variable: setsPrefix},
		Operation: operation,
	}
	if withValue {
		aggregate.Value = postWindowSlotExpression("__loom_projection_1", ir.PhysicalSelectorDirectScalar, postWindowValueSteps())
	}
	if withPredicate {
		aggregate.Predicate = &ir.PhysicalPredicateExpression{
			Kind: ir.PhysicalComparisonPredicate,
			Comparison: &ir.PhysicalPredicate{
				Operator:       "EQUALS",
				LeftExpression: postWindowSlotExpression("__loom_projection_0", ir.PhysicalSelectorConditionalArray, postWindowCodeSteps()),
				Right:          &ir.PhysicalValue{BindKey: setsPrefix + "_predicate_value"},
				Quantifier:     spec.QuantifierAny,
				ValueKind:      "CODE",
			},
		}
	}
	return ir.PhysicalProjection{
		Name: name,
		Expression: &ir.PhysicalExpression{
			Kind: ir.PhysicalAggregateExpression, Cardinality: ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalEmptyOnNull, Aggregate: aggregate,
		},
	}
}

// postWindowAggregatePlan places the SORT window before the child set and the aggregate RETURN,
// exactly where compiler/output.go puts the generic window relative to the child-route LETs.
func postWindowAggregatePlan(set ir.PhysicalSet, projections ...ir.PhysicalProjection) ir.PhysicalPlan {
	plan := setsPlan()
	plan.BindVars[setsPrefix+"_predicate_value"] = "asthma"
	sortOp := ir.PhysicalOperation{Kind: ir.PhysicalSortOp, Sort: &ir.PhysicalSort{
		Keys: []ir.PhysicalValue{{Variable: "root", Path: []string{"logical_id"}}},
	}}
	rootScan := plan.Operations[0]
	all := append([]ir.PhysicalProjection{{Name: "patient_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}}}, projections...)
	ret := ir.PhysicalOperation{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: all}}
	plan.Operations = []ir.PhysicalOperation{rootScan, sortOp, {Kind: ir.PhysicalSetOp, Set: &set}, ret}
	return plan
}

func TestPostWindowAggregateReducersRenderAsCorrelatedSublinks(t *testing.T) {
	plan := postWindowAggregatePlan(postWindowAggregateSet(false),
		postWindowAggregateProjection("sum_age", ir.PhysicalSumAggregate, true, true),
		postWindowAggregateProjection("count_age", ir.PhysicalCountAggregate, false, true),
		postWindowAggregateProjection("min_age", ir.PhysicalMinAggregate, true, true),
		postWindowAggregateProjection("max_age", ir.PhysicalMaxAggregate, true, true),
	)
	rendered := renderOK(t, plan)
	sql := rendered.SQL

	for _, forbidden := range []string{"LEFT JOIN", "DISTINCT"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("aggregate sublinks must not add outer joins or deduplicate item rows (%q present):\n%s", forbidden, sql)
		}
	}
	for _, want := range []string{
		// SUM: registered numeric parity (operand clamp + IEEE band), empty -> NULL, non-numeric guard.
		"CASE WHEN COUNT(*) = 0 THEN NULL::jsonb",
		"bool_and(jsonb_typeof(",
		"SUM(",
		"ASSERT:",
		// COUNT over the raw set array (Value-less): counts items, no slot unnest.
		"to_jsonb(COUNT(*))",
		// MIN/MAX: ArangoDB type order (booleans before numbers) with the reached-item sublink.
		"WHEN 'boolean' THEN 1 WHEN 'number' THEN 2 WHEN 'string' THEN 3 WHEN 'array' THEN 4",
		"ORDER BY",
		"LIMIT 1",
		// The predicate is the ANY-quantified match over the code slot; the reached items join
		// the edge occurrence table (per occurrence, not deduplicated).
		"EXISTS (SELECT 1 FROM jsonb_array_elements(",
		"fhir_edge_occurrence",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("aggregate statement is missing %q:\n%s", want, sql)
		}
	}
}

func TestPostWindowCountWithValueCountsSlotsNotRows(t *testing.T) {
	// AQL: LENGTH(FOR v IN FLATTEN(values) FILTER v != null RETURN 1) (render/aql/collections.go:395-402).
	bare := renderOK(t, postWindowAggregatePlan(postWindowAggregateSet(false),
		postWindowAggregateProjection("count_age", ir.PhysicalCountAggregate, false, false)))
	withValue := renderOK(t, postWindowAggregatePlan(postWindowAggregateSet(false),
		postWindowAggregateProjection("count_age", ir.PhysicalCountAggregate, true, false)))
	if strings.Contains(bare.SQL, "CROSS JOIN LATERAL jsonb_array_elements") {
		t.Fatalf("Value-less COUNT must count the set array without a slot unnest:\n%s", bare.SQL)
	}
	if !strings.Contains(withValue.SQL, "CROSS JOIN LATERAL jsonb_array_elements") ||
		!strings.Contains(withValue.SQL, "to_jsonb(COUNT(*))") {
		t.Fatalf("COUNT with a Value must count the unnested non-null slots:\n%s", withValue.SQL)
	}
}

func TestPostWindowAggregateSurfaceRefusals(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ir.PhysicalPlan)
		want   string
	}{
		{"mean has no registered reducer", func(plan *ir.PhysicalPlan) {
			plan.Operations[3].Return.Projections[2].Expression.Aggregate.Operation = ir.PhysicalMeanAggregate
		}, "no registered PostgreSQL reducer"},
		{"scalar cardinality is the registered form", func(plan *ir.PhysicalPlan) {
			plan.Operations[3].Return.Projections[2].Expression.Cardinality = ir.PhysicalArrayCardinality
		}, "SCALAR-cardinality aggregates"},
		{"preserve-null behavior is outside the registered form", func(plan *ir.PhysicalPlan) {
			plan.Operations[3].Return.Projections[2].Expression.NullBehavior = ir.PhysicalPreserveNull
		}, "EMPTY_ON_NULL aggregates"},
		{"value must name a declared projection slot", func(plan *ir.PhysicalPlan) {
			plan.Operations[3].Return.Projections[2].Expression.Aggregate.Value.Extract.Prepared.Field = "__loom_projection_9"
		}, "does not declare"},
		{"distinct slots change element multiplicity", func(plan *ir.PhysicalPlan) {
			plan.Operations[3].Return.Projections[2].Expression.Aggregate.Value.Extract.Distinct = true
		}, "element multiplicity"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := postWindowAggregatePlan(postWindowAggregateSet(false),
				postWindowAggregateProjection("count_age", ir.PhysicalCountAggregate, false, false),
				postWindowAggregateProjection("sum_age", ir.PhysicalSumAggregate, true, false))
			test.mutate(&plan)
			_, err := RenderPhysicalPlan(plan, mustBinding(t))
			wantUnsupported(t, err, test.name)
			if !strings.Contains(err.Error(), string(ir.PhysicalAggregateExpression)) ||
				!strings.Contains(err.Error(), "no registered PostgreSQL rendering") {
				t.Fatalf("%s: aggregate gate text drifted: %v", test.name, err)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("%s: error = %v, want substring %q", test.name, err, test.want)
			}
		})
	}
}

func TestPostWindowSetWithoutRegisteredConsumerKeepsTheWindowGuard(t *testing.T) {
	set := postWindowAggregateSet(false)
	plan := postWindowAggregatePlan(set,
		postWindowAggregateProjection("count_age", ir.PhysicalCountAggregate, false, false))
	// Break the only consumer: the RETURN no longer reads the set.
	plan.Operations[3].Return.Projections = plan.Operations[3].Return.Projections[:1]
	_, err := RenderPhysicalPlan(plan, mustBinding(t))
	wantUnsupported(t, err, "unconsumed post-window set")
	if !strings.Contains(err.Error(), "sets must precede the execution window") {
		t.Fatalf("the window guard must stay fail-closed: %v", err)
	}
}

func TestPostWindowEndpointLookupIsAdmittedOnlyForAggregateConsumers(t *testing.T) {
	// The aggregate surface registered AQL parity for both access-path strategies; the
	// reduction surface keeps the fail-closed refusal until its owner adjudicates the shape.
	rendered := renderOK(t, postWindowAggregatePlan(postWindowAggregateSet(true),
		postWindowAggregateProjection("count_age", ir.PhysicalCountAggregate, false, false)))
	aggregateSQL := rendered.SQL

	native := postWindowAggregatePlan(postWindowAggregateSet(false),
		postWindowAggregateProjection("count_age", ir.PhysicalCountAggregate, false, false))
	nativeSQL := renderOK(t, native).SQL
	if aggregateSQL != nativeSQL {
		t.Fatalf("endpoint-lookup must normalize to the registered join byte-identically:\n%s\n---\n%s", aggregateSQL, nativeSQL)
	}

	// The reduction-consumed surface keeps the typed refusal.
	reductionPlan := setsPlanWithSet(setsChildSet(ir.PhysicalSetReductionFirst), "__loom_reduced_0")
	for index := range reductionPlan.Operations {
		if reductionPlan.Operations[index].Kind == ir.PhysicalSetOp {
			traversal := reductionPlan.Operations[index].Set.Subplan.Operations[0].Traversal
			traversal.Strategy = ir.PhysicalTraversalEndpointLookup
			traversal.EndpointField, traversal.EndpointJoinField = "_from", "_to"
			traversal.EndpointIndexFields = []string{"_from", "project", "dataset_generation", "label", "to_type"}
		}
	}
	_, err := RenderPhysicalPlan(reductionPlan, mustBinding(t))
	wantUnsupported(t, err, "endpoint-lookup on the reduction surface")
	if !strings.Contains(err.Error(), "no proven PostgreSQL contract for this consumer") {
		t.Fatalf("the reduction surface must keep the fail-closed refusal: %v", err)
	}
}

func TestPostWindowDeadTraversalRendersNothingAndLocatedConsumersRefuse(t *testing.T) {
	base := postWindowAggregatePlan(postWindowAggregateSet(false),
		postWindowAggregateProjection("count_age", ir.PhysicalCountAggregate, false, false))
	baseline := renderOK(t, base).SQL

	// A second child-route traversal after the window whose target nobody reads is a dead LET;
	// the AQL arm binds it and drops it, so the PostgreSQL arm must emit the identical statement.
	dead := postWindowAggregatePlan(postWindowAggregateSet(false),
		postWindowAggregateProjection("count_age", ir.PhysicalCountAggregate, false, false))
	deadTraversal := setsTraversal()
	deadEdge, deadTarget := "route_edge_9", "route_node_9"
	deadTraversal.EdgeVariable, deadTraversal.TargetVariable = deadEdge, deadTarget
	deadOps := appendScopeBlock([]ir.PhysicalOperation{{Kind: ir.PhysicalTraversalOp, Traversal: &deadTraversal}},
		[]string{deadEdge, deadTarget}, "route_9_scope_auth")
	deadOps = append([]ir.PhysicalOperation{dead.Operations[0], dead.Operations[1], dead.Operations[2]}, deadOps...)
	deadOps = append(deadOps, dead.Operations[3])
	dead.Operations = deadOps
	rendered := renderOK(t, dead).SQL
	if rendered != baseline {
		t.Fatalf("a dead post-window traversal must not change the statement:\n%s\n---\n%s", rendered, baseline)
	}

	// The same traversal whose target IS read must refuse, naming the consuming operation.
	live := postWindowAggregatePlan(postWindowAggregateSet(false),
		postWindowAggregateProjection("count_age", ir.PhysicalCountAggregate, false, false))
	liveTraversal := setsTraversal()
	liveTraversal.EdgeVariable, liveTraversal.TargetVariable = deadEdge, deadTarget
	liveRet := live.Operations[3]
	liveRet.Return.Projections = append(liveRet.Return.Projections, ir.PhysicalProjection{
		Name: "leak", Value: ir.PhysicalValue{Variable: deadTarget, Path: []string{"logical_id"}}})
	liveOps := appendScopeBlock([]ir.PhysicalOperation{{Kind: ir.PhysicalTraversalOp, Traversal: &liveTraversal}},
		[]string{deadEdge, deadTarget}, "route_9_scope_auth")
	liveOps = append([]ir.PhysicalOperation{live.Operations[0], live.Operations[1], live.Operations[2]}, liveOps...)
	liveOps = append(liveOps, liveRet)
	live.Operations = liveOps
	_, err := RenderPhysicalPlan(live, mustBinding(t))
	wantUnsupported(t, err, "post-window traversal with a live consumer")
	if !strings.Contains(err.Error(), deadTarget) || !strings.Contains(err.Error(), "not dead") {
		t.Fatalf("the refusal must name the read variable and the dead-LET rule: %v", err)
	}
}

// TestPostWindowAggregateOverNonSetSourceRefused keeps the aggregate gate honest on a plan
// whose SET is accepted for another reason: the reducer must still refuse a source that is not
// a registered child set.
func TestPostWindowAggregateOverNonSetSourceRefused(t *testing.T) {
	plan := setsPlanWithSet(setsChildSet(ir.PhysicalSetReductionFirst), "__loom_reduced_0")
	plan.BindVars[setsPrefix+"_predicate_value"] = "asthma"
	returnOp := plan.Operations[len(plan.Operations)-1]
	returnOp.Return.Projections = append(returnOp.Return.Projections,
		postWindowAggregateProjection("count_all", ir.PhysicalCountAggregate, false, false))
	plan.Operations[len(plan.Operations)-1].Return.Projections[len(plan.Operations[len(plan.Operations)-1].Return.Projections)-1].
		Expression.Aggregate.Source = ir.PhysicalValue{Variable: "root"}
	_, err := RenderPhysicalPlan(plan, mustBinding(t))
	wantUnsupported(t, err, "aggregate over a non-set source")
	if !strings.Contains(err.Error(), "not a registered child set") {
		t.Fatalf("the gate must name the unregistered source: %v", err)
	}
}
