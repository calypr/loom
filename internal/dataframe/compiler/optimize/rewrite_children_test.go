package optimize

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

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
		// Compare execution bytes so unrelated cloning policy does not define the frontier.
		got := rewritePhysicalOperationVariables(input, "target", "shared_item", "edge", "shared_edge")
		before, _ := json.Marshal(ir.CanonicalExecutionPhysicalPlan(ir.PhysicalPlan{Operations: []ir.PhysicalOperation{input}}))
		after, _ := json.Marshal(ir.CanonicalExecutionPhysicalPlan(ir.PhysicalPlan{Operations: []ir.PhysicalOperation{got}}))
		if !bytes.Equal(before, after) {
			t.Fatalf("rewrite crossed %s frontier: before=%s after=%s", input.Kind, before, after)
		}
	}
}
