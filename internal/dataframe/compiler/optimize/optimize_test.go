package optimize

import (
	"bytes"
	"encoding/json"
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

func TestRewritePhysicalOperationVariablesRewritesUnnestOwnerAndExpression(t *testing.T) {
	op := ir.PhysicalOperation{
		Kind: ir.PhysicalUnnestOp,
		Unnest: &ir.PhysicalUnnest{
			Owner:           ir.PhysicalUnnestOwner{ResourceType: "Patient", RootVariable: "target", OwnerVariable: "target"},
			OutputVariable:  "item",
			HasItemVariable: "has_item",
			Expression: ir.PhysicalExpression{
				Kind:         ir.PhysicalValueExpression,
				Cardinality:  ir.PhysicalArrayCardinality,
				NullBehavior: ir.PhysicalEmptyOnNull,
				Value:        &ir.PhysicalValue{Variable: "target", Path: []string{"payload"}},
			},
			EmptyPolicy: ir.PhysicalUnnestExclude,
		},
	}
	rewritten := rewritePhysicalOperationVariables(op, "target", "shared_item", "edge", "shared_edge")
	if got := rewritten.Unnest.Owner.OwnerVariable; got != "shared_item" {
		t.Fatalf("unnest owner variable = %q, want shared_item", got)
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
				Owner:           ir.PhysicalUnnestOwner{OccurrenceID: "root", ResourceType: "Patient", RootVariable: "root", OwnerVariable: "root"},
				OutputVariable:  "expanded",
				HasItemVariable: "has_expanded",
				Expression: ir.PhysicalExpression{
					Kind:         ir.PhysicalValueExpression,
					Cardinality:  ir.PhysicalArrayCardinality,
					NullBehavior: ir.PhysicalEmptyOnNull,
					Value:        &ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
				},
				EmptyPolicy: ir.PhysicalUnnestExclude,
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

func TestRewriteVariablesPreservesNestedReferencesAndInput(t *testing.T) {
	value := func(variable string) ir.PhysicalExpression {
		return ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Value: &ir.PhysicalValue{Variable: variable}}
	}
	input := ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{
		Predicate: ir.PhysicalPredicate{LeftExpression: &ir.PhysicalExpression{Kind: ir.PhysicalCallExpression, Call: &ir.PhysicalCall{Args: []ir.PhysicalExpression{
			value("target"), {Kind: ir.PhysicalObjectExpression, Object: &ir.PhysicalObject{Fields: []ir.PhysicalExpressionProjection{{Expression: value("edge")}}}},
		}}}, Right: &ir.PhysicalValue{Variable: "edge"}},
		Expression: &ir.PhysicalPredicateExpression{Kind: ir.PhysicalExistsPredicate, Exists: &ir.PhysicalSubplan{
			Captures:   []string{"target"},
			Operations: []ir.PhysicalOperation{{Kind: ir.PhysicalDerivedLetOp, DerivedLet: &ir.PhysicalDerivedLet{Inputs: []ir.PhysicalValue{{Variable: "target"}, {Variable: "edge"}}}}},
			Return:     value("target"), Sort: &ir.PhysicalValue{Variable: "target"}, DistinctBy: &ir.PhysicalValue{Variable: "edge"},
		}},
	}}
	before, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	got := rewritePhysicalOperationVariables(input, "target", "shared_item", "edge", "shared_edge")
	call := got.Filter.Predicate.LeftExpression.Call
	if call.Args[0].Value.Variable != "shared_item" || call.Args[1].Object.Fields[0].Expression.Value.Variable != "shared_edge" || got.Filter.Predicate.Right.Variable != "shared_edge" {
		t.Fatalf("nested predicate references were not rewritten: %#v", got.Filter.Predicate)
	}
	subplan := got.Filter.Expression.Exists
	if subplan.Operations[0].DerivedLet.Inputs[0].Variable != "shared_item" || subplan.Operations[0].DerivedLet.Inputs[1].Variable != "shared_edge" || subplan.Return.Value.Variable != "shared_item" {
		t.Fatalf("EXISTS references were not rewritten: %#v", subplan)
	}
	if subplan.Captures[0] != "target" || subplan.Sort.Variable != "target" || subplan.DistinctBy.Variable != "edge" {
		t.Fatalf("rewrite crossed existing subplan metadata frontier: %#v", subplan)
	}
	after, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("rewrite mutated input: before=%s after=%s", before, after)
	}
	call.Args[0].Value.Variable = "mutated"
	subplan.Operations[0].DerivedLet.Inputs[0].Variable = "mutated"
	if input.Filter.Predicate.LeftExpression.Call.Args[0].Value.Variable != "target" || input.Filter.Expression.Exists.Operations[0].DerivedLet.Inputs[0].Variable != "target" {
		t.Fatal("rewritten nested references share mutable storage with input")
	}
}

func TestRewriteVariablesPreservesExistingLexicalFrontier(t *testing.T) {
	for _, input := range []ir.PhysicalOperation{
		{Kind: ir.PhysicalExpressionLetOp, ExpressionLet: &ir.PhysicalExpressionLet{Expression: ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Value: &ir.PhysicalValue{Variable: "target"}}}},
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{{Value: ir.PhysicalValue{Variable: "target"}}}}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{LeftExpression: &ir.PhysicalExpression{Kind: ir.PhysicalSubplanExpression, Subplan: &ir.PhysicalSubplan{Return: ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Value: &ir.PhysicalValue{Variable: "target"}}}}}}},
	} {

		got := rewritePhysicalOperationVariables(input, "target", "shared_item", "edge", "shared_edge")
		before, _ := json.Marshal(ir.CanonicalExecutionPhysicalPlan(ir.PhysicalPlan{Operations: []ir.PhysicalOperation{input}}))
		after, _ := json.Marshal(ir.CanonicalExecutionPhysicalPlan(ir.PhysicalPlan{Operations: []ir.PhysicalOperation{got}}))
		if !bytes.Equal(before, after) {
			t.Fatalf("rewrite crossed %s frontier: before=%s after=%s", input.Kind, before, after)
		}
	}
}
