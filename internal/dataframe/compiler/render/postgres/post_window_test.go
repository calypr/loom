package postgres

import (
	"regexp"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

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

	itemsRegion := regexp.MustCompile(`(?s)\(SELECT DISTINCT x1\."logical_id".*?\) AS a\d+`)
	regions := itemsRegion.FindAllString(sql, -1)
	if len(regions) != 4 {
		t.Fatalf("every aggregate needs its own DISTINCT items subselect, got %d:\n%s", len(regions), sql)
	}
	for _, region := range regions {
		if regexp.MustCompile(`\ba\d+\."payload"`).MatchString(region) {
			t.Fatalf("items subselect references its own alias (unexecutable plan analysis):\n%s", region)
		}
		if !strings.Contains(region, `x1."payload"`) {
			t.Fatalf("items subselect predicate must read the reached target's payload column:\n%s", region)
		}
	}

	if strings.Contains(sql, "LEFT JOIN") {
		t.Fatalf("aggregate sublinks must not add outer joins:\n%s", sql)
	}
	for _, want := range []string{
		"CASE WHEN COUNT(*) = 0 THEN NULL::jsonb",
		"bool_and(jsonb_typeof(",
		"SUM(",
		"ASSERT:",
		"to_jsonb(COUNT(*))",
		"WHEN 'boolean' THEN 1 WHEN 'number' THEN 2 WHEN 'string' THEN 3 WHEN 'array' THEN 4",
		"ORDER BY",
		"LIMIT 1",
		"EXISTS (SELECT 1 FROM jsonb_array_elements(",
		"fhir_edge_occurrence",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("aggregate statement is missing %q:\n%s", want, sql)
		}
	}
}

func TestPostWindowCountWithValueCountsSlotsNotRows(t *testing.T) {
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
	plan.Operations[3].Return.Projections = plan.Operations[3].Return.Projections[:1]
	_, err := RenderPhysicalPlan(plan, mustBinding(t))
	wantUnsupported(t, err, "unconsumed post-window set")
	if !strings.Contains(err.Error(), "sets must precede the execution window") {
		t.Fatalf("the window guard must stay fail-closed: %v", err)
	}
}

func TestPostWindowEndpointLookupIsAdmittedOnlyForAggregateConsumers(t *testing.T) {
	rendered := renderOK(t, postWindowAggregatePlan(postWindowAggregateSet(true),
		postWindowAggregateProjection("count_age", ir.PhysicalCountAggregate, false, false)))
	aggregateSQL := rendered.SQL

	native := postWindowAggregatePlan(postWindowAggregateSet(false),
		postWindowAggregateProjection("count_age", ir.PhysicalCountAggregate, false, false))
	nativeSQL := renderOK(t, native).SQL
	if aggregateSQL != nativeSQL {
		t.Fatalf("endpoint-lookup must normalize to the registered join byte-identically:\n%s\n---\n%s", aggregateSQL, nativeSQL)
	}

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

func TestPostWindowEndpointLookupRefusesFieldsThatDisagreeWithDirection(t *testing.T) {
	withTraversal := func(mutate func(*ir.PhysicalTraversal)) ir.PhysicalPlan {
		set := postWindowAggregateSet(true)
		mutate(set.Subplan.Operations[0].Traversal)
		return postWindowAggregatePlan(set,
			postWindowAggregateProjection("count_age", ir.PhysicalCountAggregate, false, false))
	}

	inboundPairOnOutbound := withTraversal(func(traversal *ir.PhysicalTraversal) {
		traversal.EndpointField, traversal.EndpointJoinField = "_to", "_from"
		traversal.EndpointIndexFields = []string{"_to", "project", "dataset_generation", "label", "from_type"}
	})
	_, err := RenderPhysicalPlan(inboundPairOnOutbound, mustBinding(t))
	wantUnsupported(t, err, "endpoint fields paired for the opposite direction")
	if !strings.Contains(err.Error(), "must fail closed") {
		t.Fatalf("the direction mismatch must fail closed, not normalize: %v", err)
	}

	swapOnlyJoinField := withTraversal(func(traversal *ir.PhysicalTraversal) {
		traversal.EndpointJoinField = "_from"
	})
	_, err = RenderPhysicalPlan(swapOnlyJoinField, mustBinding(t))
	wantUnsupported(t, err, "endpoint join field swapped against the direction")

	unknownDirection := withTraversal(func(traversal *ir.PhysicalTraversal) {
		traversal.Direction = ir.PhysicalTraversalDirection("SELF")
	})
	_, err = RenderPhysicalPlan(unknownDirection, mustBinding(t))
	if err == nil || !strings.Contains(err.Error(), "invalid traversal direction") {
		t.Fatalf("an undirected endpoint lookup must be refused, got: %v", err)
	}
}

func TestPostWindowDeadTraversalRendersNothingAndLocatedConsumersRefuse(t *testing.T) {
	base := postWindowAggregatePlan(postWindowAggregateSet(false),
		postWindowAggregateProjection("count_age", ir.PhysicalCountAggregate, false, false))
	baseline := renderOK(t, base).SQL

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

func deadTraversalOps(plan ir.PhysicalPlan, edgeVariable, targetVariable string) ir.PhysicalPlan {
	traversal := setsTraversal()
	traversal.EdgeVariable, traversal.TargetVariable = edgeVariable, targetVariable
	block := appendScopeBlock([]ir.PhysicalOperation{{Kind: ir.PhysicalTraversalOp, Traversal: &traversal}},
		[]string{edgeVariable, targetVariable}, "dead_scope_auth")
	ops := []ir.PhysicalOperation{plan.Operations[0], plan.Operations[1], plan.Operations[2]}
	ops = append(ops, block...)
	ops = append(ops, plan.Operations[3])
	plan.Operations = ops
	return plan
}

func TestPostWindowDeadTraversalNearMisses(t *testing.T) {
	const deadEdge, deadTarget = "dead_edge", "dead_node"

	t.Run("consumer in a later subplan block", func(t *testing.T) {
		base := postWindowAggregatePlan(postWindowAggregateSet(false),
			postWindowAggregateProjection("count_age", ir.PhysicalCountAggregate, false, false))
		plan := deadTraversalOps(base, deadEdge, deadTarget)
		poison := postWindowAggregateSet(false)
		poison.Subplan.Captures = []string{"root", deadTarget}
		poison.Variable = "child_set_1"
		last := len(plan.Operations) - 1
		plan.Operations = append(plan.Operations[:last],
			append([]ir.PhysicalOperation{{Kind: ir.PhysicalSetOp, Set: &poison}}, plan.Operations[last:]...)...)
		_, err := RenderPhysicalPlan(plan, mustBinding(t))
		wantUnsupported(t, err, "dead hop read from a later subplan")
		if !strings.Contains(err.Error(), deadTarget) || !strings.Contains(err.Error(), "not dead") {
			t.Fatalf("refusal must name the read variable and the dead-LET rule: %v", err)
		}
	})

	t.Run("reducer references the dead target", func(t *testing.T) {
		base := postWindowAggregatePlan(postWindowAggregateSet(false),
			postWindowAggregateProjection("count_age", ir.PhysicalCountAggregate, false, false))
		plan := deadTraversalOps(base, deadEdge, deadTarget)
		projections := plan.Operations[len(plan.Operations)-1].Return.Projections
		projections[1].Expression.Aggregate.Value = postWindowSlotExpression("__loom_projection_1",
			ir.PhysicalSelectorDirectScalar, postWindowValueSteps())
		projections[1].Expression.Aggregate.Value.Extract.Source.Variable = deadTarget
		_, err := RenderPhysicalPlan(plan, mustBinding(t))
		wantUnsupported(t, err, "reducer reading the dead target")
		if !strings.Contains(err.Error(), deadTarget) || !strings.Contains(err.Error(), "not dead") {
			t.Fatalf("refusal must name the read variable and the dead-LET rule: %v", err)
		}
	})

	t.Run("subplan sort key references the dead target", func(t *testing.T) {
		base := postWindowAggregatePlan(postWindowAggregateSet(false),
			postWindowAggregateProjection("count_age", ir.PhysicalCountAggregate, false, false))
		plan := deadTraversalOps(base, deadEdge, deadTarget)
		poison := postWindowAggregateSet(false)
		poison.Subplan.Captures = []string{"root", deadTarget}
		poison.Subplan.Sort = &ir.PhysicalValue{Variable: deadTarget, Path: []string{"payload", "status"}}
		poison.Variable = "child_set_1"
		last := len(plan.Operations) - 1
		plan.Operations = append(plan.Operations[:last],
			append([]ir.PhysicalOperation{{Kind: ir.PhysicalSetOp, Set: &poison}}, plan.Operations[last:]...)...)
		_, err := RenderPhysicalPlan(plan, mustBinding(t))
		wantUnsupported(t, err, "subplan sort key reading the dead target")
		if !strings.Contains(err.Error(), deadTarget) || !strings.Contains(err.Error(), "not dead") {
			t.Fatalf("refusal must name the read variable and the dead-LET rule: %v", err)
		}
	})
}

func TestPostWindowUnrimmedAggregateOperationsRefuse(t *testing.T) {
	for _, operation := range []ir.PhysicalAggregateOperation{
		ir.PhysicalMeanAggregate, ir.PhysicalExistsAggregate, ir.PhysicalCountDistinctAggregate,
		ir.PhysicalDistinctValuesAggregate, ir.PhysicalFirstAggregate, ir.PhysicalCollectAggregate,
		ir.PhysicalRequireOneAggregate, ir.PhysicalContainsAllAggregate, ir.PhysicalFirstOrderedAggregate,
	} {
		t.Run(string(operation), func(t *testing.T) {
			plan := postWindowAggregatePlan(postWindowAggregateSet(false),
				postWindowAggregateProjection("count_age", ir.PhysicalCountAggregate, false, false),
				postWindowAggregateProjection("sum_age", ir.PhysicalSumAggregate, true, false))
			aggregate := plan.Operations[3].Return.Projections[2].Expression.Aggregate
			aggregate.Operation = operation
			switch operation {
			case ir.PhysicalExistsAggregate:
				aggregate.Value = nil
			case ir.PhysicalContainsAllAggregate:
				plan.BindVars["required_values"] = []string{"asthma"}
				aggregate.RequiredValuesBindKey = "required_values"
			case ir.PhysicalFirstOrderedAggregate:
				stamp := postWindowSlotExpression("__loom_projection_1", ir.PhysicalSelectorDirectScalar, postWindowValueSteps())
				aggregate.ContributorWindow = &ir.PhysicalContributorWindow{
					Timestamp: *stamp, Anchor: *stamp, Precision: "INSTANT",
				}
				aggregate.Ordering = &ir.PhysicalTemporalOrdering{Timestamp: *stamp, Direction: "ASC", TiePolicy: "RESOURCE_KEY"}
			}
			_, err := RenderPhysicalPlan(plan, mustBinding(t))
			wantUnsupported(t, err, "unrimmed aggregate operation "+string(operation))
			if !strings.Contains(err.Error(), string(ir.PhysicalAggregateExpression)) ||
				!strings.Contains(err.Error(), "no registered PostgreSQL rendering") {
				t.Fatalf("aggregate gate text drifted: %v", err)
			}
		})
	}
}

func TestPostWindowReducersPreserveRowCounts(t *testing.T) {
	const tailMarker = `."fhir_resource" AS r0 WHERE r0."project"`

	baseline := postWindowAggregatePlan(postWindowAggregateSet(false))
	baseline.Operations = []ir.PhysicalOperation{baseline.Operations[0], baseline.Operations[1], baseline.Operations[3]}
	baseline.Operations[2].Return.Projections = baseline.Operations[2].Return.Projections[:1]
	baselineSQL := renderOK(t, baseline).SQL

	full := postWindowAggregatePlan(postWindowAggregateSet(false),
		postWindowAggregateProjection("sum_age", ir.PhysicalSumAggregate, true, true),
		postWindowAggregateProjection("count_age", ir.PhysicalCountAggregate, false, true),
		postWindowAggregateProjection("min_age", ir.PhysicalMinAggregate, true, true),
		postWindowAggregateProjection("max_age", ir.PhysicalMaxAggregate, true, true))
	fullSQL := renderOK(t, full).SQL

	baselineTail := baselineSQL[strings.Index(baselineSQL, tailMarker):]
	fullTail := fullSQL[strings.Index(fullSQL, tailMarker):]
	if baselineTail != fullTail {
		t.Fatalf("post-window reducers changed the outer row spine:\nbaseline: %s\nfull:     %s", baselineTail, fullTail)
	}
	if strings.Count(fullSQL, "AS r0") != 1 || strings.Contains(fullSQL, "LEFT JOIN") {
		t.Fatalf("reducers must stay scalar sublinks over one root scan:\n%s", fullSQL)
	}
}

func TestPostWindowReducersUnderPreviewLimit(t *testing.T) {
	plan := postWindowAggregatePlan(postWindowAggregateSet(false),
		postWindowAggregateProjection("count_age", ir.PhysicalCountAggregate, false, false))
	plan.BindVars["preview_limit"] = 25
	limitOp := ir.PhysicalOperation{Kind: ir.PhysicalLimitOp, Limit: &ir.PhysicalLimit{BindKey: "preview_limit"}}
	ops := append([]ir.PhysicalOperation{}, plan.Operations[:2]...)
	ops = append(ops, limitOp, plan.Operations[2], plan.Operations[3])
	plan.Operations = ops

	rendered := renderOK(t, plan)
	if !strings.Contains(rendered.SQL, "LIMIT $") {
		t.Fatalf("preview LIMIT must stay on the execution window:\n%s", rendered.SQL)
	}
	for _, want := range []string{"to_jsonb(COUNT(*))", `AS "c1"`} {
		if !strings.Contains(rendered.SQL, want) {
			t.Fatalf("preview-path statement is missing %q:\n%s", want, rendered.SQL)
		}
	}
	sawLimitParameter := false
	for _, parameter := range rendered.Args {
		if parameter.Type == command.PostgreSQLParameterBigint && parameter.Value == int64(25) {
			sawLimitParameter = true
		}
	}
	if !sawLimitParameter {
		t.Fatalf("preview LIMIT parameter bigint 25 missing: %#v", rendered.Args)
	}
}
