package postgres

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

const (
	setsPrefix          = "child_set_0"
	setsEdgeVariable    = "child_set_0_edge"
	setsTargetVariable  = "child_set_0_node"
	setsReducedVariable = "child_set_0_reduced"
)

func setsTraversal() ir.PhysicalTraversal {
	return ir.PhysicalTraversal{
		SourceVariable: "root", TargetVariable: setsTargetVariable, EdgeVariable: setsEdgeVariable,
		Direction:             ir.PhysicalOutbound,
		EdgeCollectionBindKey: setsPrefix + "_edge_collection",
		EdgeLabelBindKey:      setsPrefix + "_edge_label",
		TargetTypeBindKey:     setsPrefix + "_target_type",
		EdgeTargetTypeField:   "to_type", Strategy: ir.PhysicalTraversalNative,
	}
}

func setsPlan() ir.PhysicalPlan {
	plan := basePlan()
	plan.BindVars[setsPrefix+"_edge_collection"] = "fhir_edge"
	plan.BindVars[setsPrefix+"_edge_label"] = "has-observation"
	plan.BindVars[setsPrefix+"_target_type"] = "Observation"
	return plan
}

func setsProjectionField(name, property string) ir.PhysicalSetProjectionField {
	return ir.PhysicalSetProjectionField{
		Name: name, ResourceType: "Observation",
		Selector:      spec.Selector{Steps: []spec.SelectorStep{{Field: property}}},
		ExecutionMode: ir.PhysicalSelectorDirectScalar,
	}
}

func setsChildSet(modes ...ir.PhysicalSetReductionMode) ir.PhysicalSet {
	traversal := setsTraversal()
	operations := []ir.PhysicalOperation{{Kind: ir.PhysicalTraversalOp, Traversal: &traversal}}
	operations = appendScopeBlock(operations, []string{setsEdgeVariable, setsTargetVariable}, setsPrefix+"_scope_auth")
	fields := make([]ir.PhysicalSetProjectionField, 0, len(modes))
	reductionFields := make([]ir.PhysicalSetReductionField, 0, len(modes))
	for index, mode := range modes {
		slot := "__loom_projection_" + strconv.Itoa(index)
		fields = append(fields, setsProjectionField(slot, "status"))
		reductionFields = append(reductionFields, ir.PhysicalSetReductionField{
			Name: "__loom_reduced_" + strconv.Itoa(index), SourceField: slot, Mode: mode,
		})
	}
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
		Projection: &ir.PhysicalSetProjection{
			Fields: fields,
		},
		Reduction: &ir.PhysicalSetReduction{
			Variable: setsReducedVariable, SourceSetVariable: setsPrefix, Fields: reductionFields,
		},
	}
}

func setsReturn(names ...string) ir.PhysicalOperation {
	projections := make([]ir.PhysicalProjection, 0, len(names)+1)
	projections = append(projections, ir.PhysicalProjection{
		Name: "patient_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}})
	for _, name := range names {
		projections = append(projections, ir.PhysicalProjection{
			Name: name, Value: ir.PhysicalValue{Variable: setsReducedVariable, Path: []string{name}}})
	}
	return ir.PhysicalOperation{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: projections}}
}

func setsPlanWithSet(set ir.PhysicalSet, names ...string) ir.PhysicalPlan {
	plan := setsPlan()
	plan.Operations = plan.Operations[:len(plan.Operations)-1]
	plan.Operations = append(plan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalSetOp, Set: &set},
		setsReturn(names...))
	return plan
}

func TestSetChildSetReductionsRenderAsCorrelatedScalarSublinks(t *testing.T) {
	set := setsChildSet(ir.PhysicalSetReductionFirst, ir.PhysicalSetReductionAll, ir.PhysicalSetReductionDistinct)
	rendered := renderOK(t, setsPlanWithSet(set,
		"__loom_reduced_0", "__loom_reduced_1", "__loom_reduced_2"))

	sql := rendered.SQL
	for _, forbidden := range []string{"LEFT JOIN", "LATERAL (SELECT"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("set must not add outer joins (%q present): %s", forbidden, sql)
		}
	}
	for _, want := range []string{
		`ORDER BY i2."logical_id", s2.ord LIMIT 1`,
		`jsonb_agg(s3.v ORDER BY i3."logical_id", s3.ord)`,
		`DISTINCT ON (i4."logical_id", s4.v)`,
	} {
		if got := strings.Count(sql, want); got != 3 {
			t.Fatalf("occurrences of %q = %d, want 3 (one per reduced-variable projection): %s", want, got, sql)
		}
	}
	if got := strings.Count(sql, `= r0."logical_id"`); got != 9 {
		t.Fatalf("correlated item scans = %d, want 9 (3 fields x 3 readers): %s", got, sql)
	}
	if got := strings.Count(sql, `SELECT DISTINCT x1."logical_id"`); got != 9 {
		t.Fatalf("item dedup count = %d, want 9: %s", got, sql)
	}
	if !strings.Contains(sql, "jsonb_build_object($") || !strings.Contains(sql, "::text, (SELECT") {
		t.Fatalf("reduced object must bind text-cast keys over sublink values: %s", sql)
	}
	for _, leaked := range []string{"has-observation", "Observation", "proj-a", "gen-1", "FHIR/Patient", "__loom_reduced_0"} {
		if strings.Contains(sql, leaked) {
			t.Fatalf("value %q leaked into statement text: %s", leaked, sql)
		}
	}
	if !strings.Contains(sql, "$") {
		t.Fatalf("expected bound parameters: %s", sql)
	}
	for _, want := range []string{"e1.", "auth_resource_path"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("sublinks must carry the structural edge scope (%q missing): %s", want, sql)
		}
	}

	again := renderOK(t, setsPlanWithSet(set,
		"__loom_reduced_0", "__loom_reduced_1", "__loom_reduced_2"))
	if again.SQL != sql || !reflect.DeepEqual(again.Args, rendered.Args) {
		t.Fatalf("rendering is not deterministic")
	}
	if got, want := len(rendered.OutputColumns), 4; got != want {
		t.Fatalf("output columns = %d, want %d", got, want)
	}
}

func setsRelatedExistsExpression(negate bool) ir.PhysicalPredicateExpression {
	traversal := setsTraversal()
	operations := []ir.PhysicalOperation{{Kind: ir.PhysicalTraversalOp, Traversal: &traversal}}
	operations = appendScopeBlock(operations, []string{setsEdgeVariable, setsTargetVariable}, setsPrefix+"_scope_auth")
	subplan := ir.PhysicalSubplan{
		Captures:   []string{"root"},
		Operations: operations,
		Return: ir.PhysicalExpression{
			Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalPreserveNull,
			Value:        &ir.PhysicalValue{Variable: setsTargetVariable, Path: []string{"_id"}},
		},
	}
	exists := ir.PhysicalPredicateExpression{Kind: ir.PhysicalExistsPredicate, Exists: &subplan}
	if !negate {
		return exists
	}
	return ir.PhysicalPredicateExpression{Kind: ir.PhysicalNotPredicate, Children: []ir.PhysicalPredicateExpression{exists}}
}

func setsRelatedExistsPlan(negate bool) ir.PhysicalPlan {
	plan := setsPlan()
	plan.Operations = plan.Operations[:len(plan.Operations)-1]
	expression := setsRelatedExistsExpression(negate)
	plan.Operations = append(plan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Expression: &expression}},
		setsReturn())
	return plan
}

func TestRelatedExistsFilterRendersBoundedSemiJoin(t *testing.T) {
	rendered := renderOK(t, setsRelatedExistsPlan(false))
	sql := rendered.SQL
	if !strings.Contains(sql, "AND EXISTS (SELECT 1 FROM ") {
		t.Fatalf("related existence filter must render the bounded semi-join:\n%s", sql)
	}
	if strings.Contains(sql, "LIMIT") {
		t.Fatalf("EXISTS makes the AQL LIMIT 1 witness unnecessary (no LIMIT allowed): %s", sql)
	}
	if !strings.Contains(sql, `= r0."logical_id"`) {
		t.Fatalf("semi-join must correlate on the outer row identity: %s", sql)
	}
	for _, want := range []string{`e1."auth_resource_path"`, `x1."auth_resource_path"`} {
		if !strings.Contains(sql, want) {
			t.Fatalf("semi-join must carry the canonical auth scope (%q missing): %s", want, sql)
		}
	}
	if strings.Contains(sql, "LEFT JOIN") {
		t.Fatalf("semi-join must not add a row-multiplying join: %s", sql)
	}
}

func TestRelatedExistsNotFormIsUnguardedTotalNot(t *testing.T) {
	rendered := renderOK(t, setsRelatedExistsPlan(true))
	sql := rendered.SQL
	if !strings.Contains(sql, "AND NOT (EXISTS (SELECT 1 FROM ") {
		t.Fatalf("registered top-level NOT(EXISTS) form missing:\n%s", sql)
	}
	if strings.Contains(sql, "COALESCE") {
		t.Fatalf("NOT over the total semi-join must stay unguarded: %s", sql)
	}
}

func TestRequiredShapeAndUnknownReturnsKeepTheTraversalPath(t *testing.T) {
	plan := setsPlan()
	plan.Operations = plan.Operations[:len(plan.Operations)-1]
	plan.BindVars["required_0_result"] = 1
	traversal := setsTraversal()
	operations := []ir.PhysicalOperation{{Kind: ir.PhysicalTraversalOp, Traversal: &traversal}}
	subplan := ir.PhysicalSubplan{
		Captures:   []string{"root"},
		Operations: operations,
		Return: ir.PhysicalExpression{
			Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalPreserveNull,
			Value:        &ir.PhysicalValue{BindKey: "required_0_result"},
		},
	}
	exists := ir.PhysicalPredicateExpression{Kind: ir.PhysicalExistsPredicate, Exists: &subplan}
	notExpression := ir.PhysicalPredicateExpression{Kind: ir.PhysicalNotPredicate, Children: []ir.PhysicalPredicateExpression{exists}}
	plan.Operations = append(plan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Expression: &notExpression}},
		setsReturn())
	_, err := RenderPhysicalPlan(plan, mustBinding(t))
	wantUnsupported(t, err, "NOT over the REQUIRED constant-1 EXISTS")

	plan = setsPlan()
	plan.Operations = plan.Operations[:len(plan.Operations)-1]
	traversal = setsTraversal()
	bad := ir.PhysicalSubplan{
		Captures:   []string{"root"},
		Operations: []ir.PhysicalOperation{{Kind: ir.PhysicalTraversalOp, Traversal: &traversal}},
		Return: ir.PhysicalExpression{
			Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalObjectCardinality,
			NullBehavior: ir.PhysicalPreserveNull,
			Value:        &ir.PhysicalValue{Variable: setsTargetVariable},
		},
	}
	expression := ir.PhysicalPredicateExpression{Kind: ir.PhysicalExistsPredicate, Exists: &bad}
	plan.Operations = append(plan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Expression: &expression}},
		setsReturn())
	_, err = RenderPhysicalPlan(plan, mustBinding(t))
	wantUnsupported(t, err, "EXISTS with a non-canonical return")
	if !strings.Contains(err.Error(), "required-match subplan return") {
		t.Fatalf("unknown EXISTS shapes must keep the traversal.go refusal, got: %v", err)
	}
}

func TestSetSubplanFilterRendersSharedSelectorMatch(t *testing.T) {
	set := setsChildSet(ir.PhysicalSetReductionAll)
	filterValue := "final"
	set.Subplan.Operations = append(set.Subplan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{
			Expression: &ir.PhysicalPredicateExpression{Kind: ir.PhysicalComparisonPredicate, Comparison: &ir.PhysicalPredicate{
				Operator: "EQUALS", ValueKind: spec.FilterString,
				LeftExpression: &ir.PhysicalExpression{
					Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality,
					NullBehavior: ir.PhysicalEmptyOnNull,
					Extract: &ir.PhysicalExtract{
						Source:        ir.PhysicalValue{Variable: setsTargetVariable, Path: []string{"payload"}},
						ResourceType:  "Observation",
						Selector:      spec.Selector{Steps: []spec.SelectorStep{{Field: "status"}}},
						ExecutionMode: ir.PhysicalSelectorDirectScalar,
					},
				},
				Right: &ir.PhysicalValue{BindKey: "child_filter_value"},
			}}}})
	plan := setsPlanWithSet(set, "__loom_reduced_0")
	plan.BindVars["child_filter_value"] = filterValue

	rendered := renderOK(t, plan)
	sql := rendered.SQL
	if !strings.Contains(sql, "x1.") || strings.Contains(sql, filterValue) {
		t.Fatalf("child filter value must bind, not inline: %s", sql)
	}
	if got := strings.Count(sql, `x1."payload" #> $`); got < 2 {
		t.Fatalf("child filter must participate in the correlated scan (payload path count %d): %s", got, sql)
	}
	boundFilter := false
	for _, arg := range rendered.Args {
		if strings.Contains(fmt.Sprintf("%s", arg.Value), strconv.Quote(filterValue)) {
			boundFilter = true
		}
	}
	if !boundFilter {
		t.Fatalf("filter value never reached the bind list: %v", rendered.Args)
	}
}

func setsSpineTraversal() ir.PhysicalOperation {
	traversal := setsTraversal()
	return ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Traversal: &traversal}
}

func TestSetCaptureAllowsOptionalAnchorExistanceCaptureRefuses(t *testing.T) {
	optional := setsTraversal()
	optional.TargetVariable = "spine_node"
	plan := setsPlan()
	plan.Operations = plan.Operations[:len(plan.Operations)-1]
	set := setsChildSet(ir.PhysicalSetReductionFirst)
	set.Subplan.Captures = []string{"spine_node"}
	set.Subplan.Operations[0].Traversal.SourceVariable = "spine_node"
	plan.Operations = append(plan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Traversal: &optional},
		ir.PhysicalOperation{Kind: ir.PhysicalSetOp, Set: &set},
		setsReturn("__loom_reduced_0"))
	rendered := renderOK(t, plan)
	if !strings.Contains(rendered.SQL, `= r1."logical_id"`) {
		t.Fatalf("SET sublink must correlate on the OPTIONAL lateral row: %s", rendered.SQL)
	}

	expression := setsRelatedExistsExpression(false)
	expression.Exists.Captures = []string{"spine_node"}
	expression.Exists.Operations[0].Traversal.SourceVariable = "spine_node"
	plan = setsPlan()
	plan.Operations = plan.Operations[:len(plan.Operations)-1]
	plan.Operations = append(plan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Traversal: &optional},
		ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Expression: &expression}},
		setsReturn())
	_, err := RenderPhysicalPlan(plan, mustBinding(t))
	wantUnsupported(t, err, "EXISTS anchored on an OPTIONAL row")
	if !strings.Contains(err.Error(), "NULL-extended anchor") {
		t.Fatalf("refusal must name the anchor rule, got: %v", err)
	}
}

func setsBatteryCase(t *testing.T, name, want string, rendererLevel bool, mutate func(*ir.PhysicalPlan)) {
	t.Helper()
	set := setsChildSet(ir.PhysicalSetReductionFirst)
	plan := setsPlanWithSet(set, "__loom_reduced_0")
	mutate(&plan)
	_, err := RenderPhysicalPlan(plan, mustBinding(t))
	if err == nil {
		t.Fatalf("%s: expected refusal, render succeeded", name)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("%s: error = %v, want substring %q", name, err, want)
	}
	if rendererLevel {
		wantUnsupported(t, err, name)
	}
}

func setsBatterySet(plan *ir.PhysicalPlan) *ir.PhysicalSet {
	for index := range plan.Operations {
		if plan.Operations[index].Kind == ir.PhysicalSetOp {
			return plan.Operations[index].Set
		}
	}
	panic("battery plan has no SET operation")
}

func TestSetSurfaceRefusals(t *testing.T) {
	tests := []struct {
		name          string
		want          string
		rendererLevel bool
		mutate        func(*ir.PhysicalPlan)
	}{
		{
			name:          "missing reduction",
			want:          "no reduction contract",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				set := setsBatterySet(plan)
				set.Reduction = nil
				plan.Operations[len(plan.Operations)-1] = setsReturn()
			},
		},
		{
			name: "missing projection",
			want: "reduction requires a selector projection",
			mutate: func(plan *ir.PhysicalPlan) {
				setsBatterySet(plan).Projection = nil
			},
		},
		{
			name:          "unique flag dropped",
			want:          "Unique and SortByKey",
			rendererLevel: true,
			mutate:        func(plan *ir.PhysicalPlan) { setsBatterySet(plan).Unique = false },
		},
		{
			name:          "sort-by-key flag dropped",
			want:          "Unique and SortByKey",
			rendererLevel: true,
			mutate:        func(plan *ir.PhysicalPlan) { setsBatterySet(plan).SortByKey = false },
		},
		{
			name:          "node-path kind",
			want:          "NODE_PATH",
			rendererLevel: true,
			mutate:        func(plan *ir.PhysicalPlan) { setsBatterySet(plan).Kind = ir.PhysicalNodePathSetKind },
		},
		{
			name:          "unknown kind",
			want:          "unknown set kind",
			rendererLevel: true,
			mutate:        func(plan *ir.PhysicalPlan) { setsBatterySet(plan).Kind = "GRAPH_WALK" },
		},
		{
			name:          "shared subset",
			want:          "shared subsets",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				set := setsBatterySet(plan)
				set.SourceSetVariable = "root"
				set.ItemVariable = "item"
				filter := ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
					Operator: "EQUALS",
					Left:     ir.PhysicalValue{Variable: "root", Path: []string{"project"}},
					Right:    &ir.PhysicalValue{BindKey: "project"},
				}}
				set.Subplan.Operations = []ir.PhysicalOperation{
					{Kind: ir.PhysicalFilterOp, Filter: &filter}}
				set.Subplan.Return.Value = &ir.PhysicalValue{Variable: "item"}
			},
		},
		{
			name:          "prepared set",
			want:          "prepared sets are not implemented",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				setsBatterySet(plan).Prepared = &ir.PhysicalPreparedSet{
					Variable: "child_set_0_prepared", SourceSetVariable: setsPrefix,
					Fields: []ir.PhysicalPreparedField{{
						Name: "__loom_prepared_0", ResourceType: "Observation",
						Selector: spec.Selector{Steps: []spec.SelectorStep{{Field: "status"}}},
					}},
				}
			},
		},
		{
			name:          "subplan sort modifier",
			want:          "projection-only modifiers",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				setsBatterySet(plan).Subplan.Sort = &ir.PhysicalValue{
					Variable: setsTargetVariable, Path: []string{"logical_id"}}
			},
		},
		{
			name:          "chained hops in a set",
			want:          "exactly one depth-one traversal hop",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				set := setsBatterySet(plan)
				second := setsTraversal()
				second.SourceVariable = setsTargetVariable
				second.TargetVariable = setsPrefix + "_node2"
				second.EdgeVariable = setsPrefix + "_edge2"
				set.Subplan.Operations = append(set.Subplan.Operations,
					ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Traversal: &second})
			},
		},
		{
			name:          "nested set in a subplan",
			want:          "has kind \"SET\"",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				set := setsBatterySet(plan)
				traversal := setsTraversal()
				set.Subplan.Operations = append(set.Subplan.Operations,
					ir.PhysicalOperation{Kind: ir.PhysicalSetOp, Set: &ir.PhysicalSet{
						Variable: "inner_set",
						Subplan: ir.PhysicalSubplan{
							Captures:   []string{"root"},
							Operations: []ir.PhysicalOperation{{Kind: ir.PhysicalTraversalOp, Traversal: &traversal}},
							Return: ir.PhysicalExpression{
								Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalObjectCardinality,
								NullBehavior: ir.PhysicalPreserveNull,
								Value:        &ir.PhysicalValue{Variable: setsTargetVariable},
							},
						},
					}})
			},
		},
		{
			name:          "collection scan in a subplan",
			want:          "has kind \"COLLECTION_SCAN\"",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				plan.BindVars["sets_cs_collection"] = "Observation"
				set := setsBatterySet(plan)
				set.Subplan.Operations = append(set.Subplan.Operations,
					ir.PhysicalOperation{Kind: ir.PhysicalCollectionScanOp, CollectionScan: &ir.PhysicalCollectionScan{
						Variable: "cs", CollectionBindKey: "sets_cs_collection",
					}})
			},
		},
		{
			name:          "filter before the hop",
			want:          "may only follow a traversal step",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				set := setsBatterySet(plan)
				filter := ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
					Operator: "EQUALS",
					Left:     ir.PhysicalValue{Variable: "root", Path: []string{"project"}},
					Right:    &ir.PhysicalValue{BindKey: "project"},
				}}
				set.Subplan.Operations = append([]ir.PhysicalOperation{
					{Kind: ir.PhysicalFilterOp, Filter: &filter}}, set.Subplan.Operations...)
			},
		},
		{
			name:          "non-canonical scope block",
			want:          "canonical subplan scope block",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				set := setsBatterySet(plan)
				set.Subplan.Operations[1].Filter.Predicate.Left.Path = []string{"projekt"}
			},
		},
		{
			name:          "direct read of the set variable",
			want:          "does not bind a readable row",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				ret := plan.Operations[len(plan.Operations)-1].Return
				ret.Projections[1].Value = ir.PhysicalValue{Variable: setsPrefix, Path: []string{"payload"}}
			},
		},
		{
			// A post-window set IS registered now when a reducer consumes it (post_window.go
			// acceptPostWindowSet); the window guard must still refuse one with no consumer.
			name:          "set after the execution window without a registered consumer",
			want:          "sets must precede the execution window",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				sortOp := ir.PhysicalOperation{Kind: ir.PhysicalSortOp, Sort: &ir.PhysicalSort{
					Keys: []ir.PhysicalValue{{Variable: "root", Path: []string{"logical_id"}}},
				}}
				rootScan, setOp, ret := plan.Operations[0], plan.Operations[1], plan.Operations[2]
				setOp.Set.Reduction = nil
				ret.Return.Projections = ret.Return.Projections[:1] // drop the reduced-variable read
				plan.Operations = []ir.PhysicalOperation{rootScan, sortOp, setOp, ret}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setsBatteryCase(t, test.name, test.want, test.rendererLevel, test.mutate)
		})
	}
}

func TestRelatedExistsSurfaceRefusals(t *testing.T) {
	tests := []struct {
		name          string
		want          string
		rendererLevel bool
		mutate        func(*ir.PhysicalSubplan, *ir.PhysicalPlan)
	}{
		{
			name: "projection modifiers on an EXISTS subplan",
			want: "projection-only sort or unique modifiers",
			mutate: func(subplan *ir.PhysicalSubplan, _ *ir.PhysicalPlan) {
				subplan.Sort = &ir.PhysicalValue{Variable: setsTargetVariable, Path: []string{"_id"}}
				subplan.Unique = true
			},
		},
		{
			name:          "missing scope block on a hop",
			want:          "canonical subplan scope block",
			rendererLevel: true,
			mutate: func(subplan *ir.PhysicalSubplan, _ *ir.PhysicalPlan) {
				subplan.Operations = subplan.Operations[:len(subplan.Operations)-1]
			},
		},
		{
			name:          "chained hop without its own block",
			want:          "canonical subplan scope block",
			rendererLevel: true,
			mutate: func(subplan *ir.PhysicalSubplan, _ *ir.PhysicalPlan) {
				second := setsTraversal()
				second.SourceVariable = setsTargetVariable
				second.TargetVariable = setsPrefix + "_node2"
				second.EdgeVariable = setsPrefix + "_edge2"
				subplan.Operations = append(subplan.Operations,
					ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Traversal: &second})
				subplan.Return.Value.Variable = setsPrefix + "_node2"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expression := setsRelatedExistsExpression(false)
			plan := setsPlan()
			plan.Operations = plan.Operations[:len(plan.Operations)-1]
			test.mutate(expression.Exists, &plan)
			plan.Operations = append(plan.Operations,
				ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Expression: &expression}},
				setsReturn())
			_, err := RenderPhysicalPlan(plan, mustBinding(t))
			if err == nil {
				t.Fatalf("%s: expected refusal, render succeeded", test.name)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("%s: error = %v, want substring %q", test.name, err, test.want)
			}
			if test.rendererLevel {
				wantUnsupported(t, err, test.name)
			}
		})
	}
}

func TestTwoSetsRenderIndependentReducedObjects(t *testing.T) {
	first := setsChildSet(ir.PhysicalSetReductionFirst)
	second := setsChildSet(ir.PhysicalSetReductionDistinct)
	second.Variable = "child_set_1"
	second.Subplan.Captures = []string{"root"}
	second.Reduction.Variable = "child_set_1_reduced"
	second.Reduction.SourceSetVariable = "child_set_1"
	second.Reduction.Fields[0].Name = "__loom_reduced_0"
	second.Projection.Fields[0].Name = "__loom_projection_0"
	second.Projection.Fields[0].Selector = spec.Selector{Steps: []spec.SelectorStep{{Field: "code"}}}

	plan := setsPlan()
	plan.Operations = plan.Operations[:len(plan.Operations)-1]
	plan.Operations = append(plan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalSetOp, Set: &first},
		ir.PhysicalOperation{Kind: ir.PhysicalSetOp, Set: &second},
		ir.PhysicalOperation{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
			{Name: "patient_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
			{Name: "first_status", Value: ir.PhysicalValue{Variable: "child_set_0_reduced", Path: []string{"__loom_reduced_0"}}},
			{Name: "distinct_codes", Value: ir.PhysicalValue{Variable: "child_set_1_reduced", Path: []string{"__loom_reduced_0"}}},
		}}})
	rendered := renderOK(t, plan)
	if got := strings.Count(rendered.SQL, "jsonb_build_object("); got != 2 {
		t.Fatalf("reduced objects = %d, want one per set: %s", got, rendered.SQL)
	}
	bound := map[string]bool{}
	for _, arg := range rendered.Args {
		if text, ok := arg.Value.(string); ok {
			bound[text] = true
		}
	}
	for _, want := range []string{"{status}", "{code}"} {
		if !bound[want] {
			t.Fatalf("selector path %q never bound: %v", want, rendered.Args)
		}
	}
}

func setsAnchorPrefixedPlan(t *testing.T, anchorKind string) ir.PhysicalPlan {
	t.Helper()
	traversal := setsTraversal()
	anchorVariable := "related_eligibility_0_anchor"
	operations := make([]ir.PhysicalOperation, 0, 10)
	switch anchorKind {
	case "scan":
		operations = append(operations,
			ir.PhysicalOperation{Kind: ir.PhysicalCollectionScanOp, CollectionScan: &ir.PhysicalCollectionScan{
				Variable: anchorVariable, CollectionBindKey: "root_collection",
			}},
			ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator: "EQUALS",
				Left:     ir.PhysicalValue{Variable: anchorVariable, Path: []string{"_key"}},
				Right:    &ir.PhysicalValue{Variable: "root", Path: []string{"_key"}},
			}}})
	case "lookup":
		operations = append(operations,
			ir.PhysicalOperation{Kind: ir.PhysicalDocumentLookupOp, DocumentLookup: &ir.PhysicalDocumentLookup{
				Variable: anchorVariable, CollectionBindKey: "related_eligibility_anchor_collection",
				ExactID: ir.PhysicalValue{Variable: "root", Path: []string{"focus_id"}},
			}})
	default:
		t.Fatalf("unknown anchor kind %q", anchorKind)
	}
	operations = appendScopeBlock(operations, []string{anchorVariable}, "related_eligibility_0_anchor_scope_allowed")
	operations = append(operations, ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Traversal: &traversal})
	operations = appendScopeBlock(operations, []string{setsEdgeVariable, setsTargetVariable}, setsPrefix+"_scope_auth")
	subplan := ir.PhysicalSubplan{
		Captures:   []string{"root"},
		Operations: operations,
		Return: ir.PhysicalExpression{
			Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalPreserveNull,
			Value:        &ir.PhysicalValue{Variable: setsTargetVariable, Path: []string{"_id"}},
		},
	}
	expression := ir.PhysicalPredicateExpression{Kind: ir.PhysicalExistsPredicate, Exists: &subplan}
	plan := setsPlan()
	plan.BindVars["related_eligibility_anchor_collection"] = "Observation"
	plan.Operations = plan.Operations[:len(plan.Operations)-1]
	plan.Operations = append(plan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Expression: &expression}},
		setsReturn())
	return plan
}

func TestRelatedExistsAnchorPrefixedProductionShapeStaysRefused(t *testing.T) {
	for _, anchorKind := range []string{"scan", "lookup"} {
		plan := setsAnchorPrefixedPlan(t, anchorKind)
		_, err := RenderPhysicalPlan(plan, mustBinding(t))
		wantUnsupported(t, err, "anchor-prefixed ("+anchorKind+") eligibility subplan")
		if !strings.Contains(err.Error(), "has kind") {
			t.Fatalf("%s anchor refusal must name the anchor operation kind: %v", anchorKind, err)
		}
	}
}

func setsRelatedExistsTwoHopPlan() ir.PhysicalPlan {
	hopOne := setsTraversal()
	hopTwo := setsTraversal()
	hopTwo.SourceVariable = setsTargetVariable
	hopTwo.TargetVariable = setsPrefix + "_node2"
	hopTwo.EdgeVariable = setsPrefix + "_edge2"
	hopTwo.EdgeCollectionBindKey = setsPrefix + "_edge_collection2"
	hopTwo.EdgeLabelBindKey = setsPrefix + "_edge_label2"
	hopTwo.TargetTypeBindKey = setsPrefix + "_target_type2"
	operations := []ir.PhysicalOperation{{Kind: ir.PhysicalTraversalOp, Traversal: &hopOne}}
	operations = appendScopeBlock(operations, []string{setsEdgeVariable, setsTargetVariable}, setsPrefix+"_scope_auth")
	operations = append(operations, ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Traversal: &hopTwo})
	operations = appendScopeBlock(operations,
		[]string{setsPrefix + "_edge2", setsPrefix + "_node2"}, setsPrefix+"_scope_auth2")
	subplan := ir.PhysicalSubplan{
		Captures:   []string{"root"},
		Operations: operations,
		Return: ir.PhysicalExpression{
			Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalPreserveNull,
			Value:        &ir.PhysicalValue{Variable: setsPrefix + "_node2", Path: []string{"_id"}},
		},
	}
	expression := ir.PhysicalPredicateExpression{Kind: ir.PhysicalExistsPredicate, Exists: &subplan}
	plan := setsPlan()
	plan.BindVars[setsPrefix+"_edge_collection2"] = "fhir_edge"
	plan.BindVars[setsPrefix+"_edge_label2"] = "has-specimen"
	plan.BindVars[setsPrefix+"_target_type2"] = "Specimen"
	plan.Operations = plan.Operations[:len(plan.Operations)-1]
	plan.Operations = append(plan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Expression: &expression}},
		setsReturn())
	return plan
}

func TestRelatedExistsChainedRouteGoldenAssemblesBothHops(t *testing.T) {
	rendered := renderOK(t, setsRelatedExistsTwoHopPlan())
	sql := rendered.SQL
	for _, want := range []string{
		`FROM "pgir02_it_unit"."fhir_edge_occurrence" AS e1`,
		`INNER JOIN "pgir02_it_unit"."fhir_resource" AS x1 ON`,
		`INNER JOIN "pgir02_it_unit"."fhir_edge_occurrence" AS e2 ON`,
		`INNER JOIN "pgir02_it_unit"."fhir_resource" AS x2 ON`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("chained FROM missing %q:\n%s", want, sql)
		}
	}
	if !strings.Contains(sql, `e1."source_logical_id" = r0."logical_id"`) {
		t.Fatalf("first hop must correlate on the outer row: %s", sql)
	}
	if !strings.Contains(sql, `e2."source_logical_id" = x1."logical_id"`) {
		t.Fatalf("second hop must anchor on x1 with its full predicate chain: %s", sql)
	}
	if !strings.Contains(sql, `e2."label" = $`) || !strings.Contains(sql, `e2."target_resource_type" = $`) {
		t.Fatalf("second hop must bind its label and reached type: %s", sql)
	}
	for _, want := range []string{`e1."auth_resource_path"`, `x1."auth_resource_path"`, `e2."auth_resource_path"`, `x2."auth_resource_path"`} {
		if !strings.Contains(sql, want) {
			t.Fatalf("chained semi-join must carry the canonical auth scope (%q missing): %s", want, sql)
		}
	}
	if strings.Contains(sql, "LIMIT") {
		t.Fatalf("EXISTS stays limit-free: %s", sql)
	}
	if got := len(rendered.Args); got != 8 {
		t.Fatalf("chained parameter count = %d, want 8 (deduped discriminators): %v", got, rendered.Args)
	}
	for _, leaked := range []string{"has-specimen", "Specimen", "has-observation"} {
		if strings.Contains(sql, leaked) {
			t.Fatalf("value %q leaked into statement text: %s", leaked, sql)
		}
	}
	again := renderOK(t, setsRelatedExistsTwoHopPlan())
	if again.SQL != sql || !reflect.DeepEqual(again.Args, rendered.Args) {
		t.Fatalf("chained rendering is not deterministic")
	}
}
