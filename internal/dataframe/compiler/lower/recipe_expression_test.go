package lower

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
)

func TestLowerRecipeExpressionUsesTypedPhysicalCallsAndBinds(t *testing.T) {
	stringType := expression.Type{Kind: expression.KindString, Cardinality: expression.RequiredOne}
	input := expression.Function("concat",
		expression.Constant(stringType, "left"),
		expression.Constant(stringType, "right"),
	)
	binds := map[string]any{}
	physical, err := LowerRecipeExpression(input, binds, "Patient")
	if err != nil {
		t.Fatal(err)
	}
	if physical.Kind != ir.PhysicalCallExpression || physical.Call == nil || physical.Call.Name != "concat" {
		t.Fatalf("unexpected physical call: %#v", physical)
	}
	if len(physical.Call.Args) != 2 || len(binds) != 2 {
		t.Fatalf("literal arguments were not lowered to bind-backed nodes: physical=%#v binds=%#v", physical, binds)
	}
	for _, arg := range physical.Call.Args {
		if arg.Kind != ir.PhysicalLiteralExpression || arg.Literal == nil {
			t.Fatalf("literal argument was not preserved as physical literal: %#v", arg)
		}
		if _, ok := binds[arg.Literal.BindKey]; !ok {
			t.Fatalf("literal bind %q is missing: %#v", arg.Literal.BindKey, binds)
		}
	}
}

func TestLowerRecipeExpressionSelectorAndCastRemainGeneric(t *testing.T) {
	selector := expression.Select(expression.SelectorRef{Context: "root", Path: "gender"})
	stringType := expression.Type{Kind: expression.KindString, Cardinality: expression.RequiredOne}
	target := expression.Type{Kind: expression.KindString, Cardinality: expression.RequiredOne}
	physical, err := LowerRecipeExpression(expression.Cast(selector, target), map[string]any{}, "Patient")
	if err != nil {
		t.Fatal(err)
	}
	if physical.Kind != ir.PhysicalCallExpression || physical.Call == nil || physical.Call.Name != "cast" || physical.Call.TargetKind != "string" {
		t.Fatalf("cast was not lowered generically: %#v", physical)
	}
	if len(physical.Call.Args) != 1 || physical.Call.Args[0].Kind != ir.PhysicalExtractExpression {
		t.Fatalf("selector argument was not lowered to extract: %#v", physical)
	}
	if got := physical.Call.Args[0].Extract.Source.Variable; got != "root" {
		t.Fatalf("selector context changed during lowering: %q", got)
	}
	// The persisted recipe form carries the target as a second literal
	// argument; the typed call target is the physical metadata representation.
	wireCast := expression.Expression{Kind: expression.CallNode, Call: &expression.Call{
		Name: "cast", Args: []expression.Expression{selector, expression.Constant(stringType, "string")}, Target: &target,
	}}
	physical, err = LowerRecipeExpression(wireCast, map[string]any{}, "Patient")
	if err != nil {
		t.Fatal(err)
	}
	if len(physical.Call.Args) != 1 {
		t.Fatalf("cast target literal leaked as runtime argument: %#v", physical.Call.Args)
	}
}
