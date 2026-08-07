package optimize

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func TestOptimizePhysicalPlanDoesNotShareAcrossUnnestBarrier(t *testing.T) {
	plan := optimizerBarrierPlan()
	first := plan.Operations[1].Set
	second := plan.Operations[3].Set
	firstPrefix, err := ir.DecomposePhysicalTraversalPrefixAt(plan, *first, 1)
	if err != nil {
		t.Fatalf("first prefix: %v", err)
	}
	secondPrefix, err := ir.DecomposePhysicalTraversalPrefixAt(plan, *second, 3)
	if err != nil {
		t.Fatalf("second prefix: %v", err)
	}
	if firstPrefix.PrefixKey == secondPrefix.PrefixKey {
		t.Fatalf("prefixes across unnest barrier unexpectedly matched: %s", firstPrefix.PrefixKey)
	}
	if firstPrefix.Prefix.UnnestScopeIdentity != "" {
		t.Fatalf("root-side prefix unexpectedly carried unnest scope: %q", firstPrefix.Prefix.UnnestScopeIdentity)
	}
	if secondPrefix.Prefix.UnnestScopeIdentity == "" {
		t.Fatal("post-unnest prefix omitted active unnest scope identity")
	}

	optimized, err := OptimizePhysicalPlanWithPolicy(plan, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("OptimizePhysicalPlan() error = %v", err)
	}
	if optimized.SharedTraversalCount != 0 {
		t.Fatalf("sharing crossed unnest barrier: count=%d", optimized.SharedTraversalCount)
	}
}

func TestRewritePhysicalOperationVariablesRewritesUnnestInputAndExpression(t *testing.T) {
	op := ir.PhysicalOperation{
		Kind: ir.PhysicalUnnestOp,
		Unnest: &ir.PhysicalUnnest{
			InputVariable:  "target",
			OutputVariable: "item",
			Expression: ir.PhysicalExpression{
				Kind:         ir.PhysicalValueExpression,
				Cardinality:  ir.PhysicalArrayCardinality,
				NullBehavior: ir.PhysicalEmptyOnNull,
				Value:        &ir.PhysicalValue{Variable: "target", Path: []string{"payload"}},
			},
			JoinMode: ir.PhysicalUnnestInner,
		},
	}
	rewritten := rewritePhysicalOperationVariables(op, "target", "shared_item", "edge", "shared_edge")
	if got := rewritten.Unnest.InputVariable; got != "shared_item" {
		t.Fatalf("unnest input variable = %q, want shared_item", got)
	}
	if got := rewritten.Unnest.Expression.Value.Variable; got != "shared_item" {
		t.Fatalf("unnest expression source = %q, want shared_item", got)
	}
}

func optimizerBarrierPlan() ir.PhysicalPlan {
	binds := map[string]any{
		"root_collection":                  "Patient",
		"edge_collection":                  "fhir_edge",
		"condition_label":                  "subject_Patient",
		"condition_type":                   "Condition",
		"specimen_label":                   "subject_Patient",
		"specimen_type":                    "Specimen",
		"project":                          "project-1",
		"dataset_generation":               nil,
		"auth_resource_paths":              []string{},
		"auth_resource_paths_unrestricted": true,
		"scope_allowed":                    true,
	}
	return ir.PhysicalPlan{
		Version:  1,
		BindVars: binds,
		Operations: []ir.PhysicalOperation{
			{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
			{Kind: ir.PhysicalSetOp, Set: optimizerBarrierSet("condition_set", "condition_label", "condition_type")},
			{Kind: ir.PhysicalUnnestOp, Unnest: &ir.PhysicalUnnest{
				InputVariable:  "root",
				OutputVariable: "expanded",
				Expression: ir.PhysicalExpression{
					Kind:         ir.PhysicalValueExpression,
					Cardinality:  ir.PhysicalArrayCardinality,
					NullBehavior: ir.PhysicalEmptyOnNull,
					Value:        &ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
				},
				JoinMode: ir.PhysicalUnnestInner,
			}},
			{Kind: ir.PhysicalSetOp, Set: optimizerBarrierSet("specimen_set", "specimen_label", "specimen_type")},
			{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{{Name: "id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"_key"}}}}}},
		},
	}
}

func optimizerBarrierSet(variable, labelBind, typeBind string) *ir.PhysicalSet {
	traversal := ir.PhysicalTraversal{
		SourceVariable:        "root",
		TargetVariable:        variable + "_node",
		EdgeVariable:          variable + "_edge",
		Direction:             ir.PhysicalInbound,
		EdgeCollectionBindKey: "edge_collection",
		EdgeLabelBindKey:      labelBind,
		TargetTypeBindKey:     typeBind,
		EdgeTargetTypeField:   "from_type",
	}
	scope := []ir.PhysicalOperation{
		{Kind: ir.PhysicalTraversalOp, Traversal: &traversal},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS", Left: ir.PhysicalValue{Variable: traversal.EdgeVariable, Path: []string{"project"}}, Right: &ir.PhysicalValue{BindKey: "project"}}}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS", Left: ir.PhysicalValue{Variable: traversal.TargetVariable, Path: []string{"project"}}, Right: &ir.PhysicalValue{BindKey: "project"}}}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS", Left: ir.PhysicalValue{Variable: traversal.EdgeVariable, Path: []string{"dataset_generation"}}, Right: &ir.PhysicalValue{BindKey: "dataset_generation"}}}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS", Left: ir.PhysicalValue{Variable: traversal.TargetVariable, Path: []string{"dataset_generation"}}, Right: &ir.PhysicalValue{BindKey: "dataset_generation"}}}},
		{Kind: ir.PhysicalDerivedLetOp, DerivedLet: &ir.PhysicalDerivedLet{Variable: variable + "_scope", Operator: "AUTH_RESOURCE_PATH_ALLOWED", Inputs: []ir.PhysicalValue{{Variable: traversal.EdgeVariable, Path: []string{"auth_resource_path"}}, {Variable: traversal.TargetVariable, Path: []string{"auth_resource_path"}}, {BindKey: "auth_resource_paths"}, {BindKey: "auth_resource_paths_unrestricted"}}}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS", Left: ir.PhysicalValue{Variable: variable + "_scope"}, Right: &ir.PhysicalValue{BindKey: "scope_allowed"}}}},
	}
	return &ir.PhysicalSet{Variable: variable, Subplan: ir.PhysicalSubplan{Captures: []string{"root"}, Operations: scope, Return: ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{Variable: traversal.TargetVariable}}}}
}
