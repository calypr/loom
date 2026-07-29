package lower

import (
	"testing"

	"github.com/calypr/loom/fhirschema"
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

func TestLowerRecipeExpressionSpecializesRepeatedSelector(t *testing.T) {
	selector := expression.Select(expression.SelectorRef{Context: "root", Path: "component[].valueInteger"})
	physical, err := LowerRecipeExpression(selector, map[string]any{}, "Observation")
	if err != nil {
		t.Fatal(err)
	}
	if physical.Kind != ir.PhysicalExtractExpression || physical.Extract == nil {
		t.Fatalf("selector was not lowered to physical extract: %#v", physical)
	}
	if physical.Cardinality != ir.PhysicalArrayCardinality {
		t.Fatalf("schema repetition was not preserved in recipe cardinality: %q", physical.Cardinality)
	}
	if physical.Extract.ExecutionMode != ir.PhysicalSelectorConditionalArray {
		t.Fatalf("schema-proven repeated recipe selector did not specialize: %q", physical.Extract.ExecutionMode)
	}
}

func TestSelectorModeClassifierKeepsPredicateAndFallbackGeneric(t *testing.T) {
	selector, err := ParseSelector("identifier[].value")
	if err != nil {
		t.Fatal(err)
	}
	selector.Filter = &fhirschema.ContainsFilter{Field: "system", Needle: "case_id"}
	if got := selectorExecutionModeForExpression("Patient", selector, nil, ir.PhysicalValue{Variable: "root", Path: []string{"payload"}}, ir.PhysicalArrayCardinality, ir.PhysicalEmptyOnNull); got != ir.PhysicalSelectorGeneric {
		t.Fatalf("predicate selector unexpectedly specialized: %q", got)
	}
	plain, err := ParseSelector("gender")
	if err != nil {
		t.Fatal(err)
	}
	if got := selectorExecutionModeForExpression("Patient", plain, []Selector{selector}, ir.PhysicalValue{Variable: "root", Path: []string{"payload"}}, ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull); got != ir.PhysicalSelectorGeneric {
		t.Fatalf("fallback selector unexpectedly specialized: %q", got)
	}
}
