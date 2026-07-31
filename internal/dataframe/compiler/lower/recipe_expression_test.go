package lower

import (
	"testing"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/spec"
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

func TestLowerDocumentRefPreservesPayloadAndStorageEnvelope(t *testing.T) {
	physical, err := LowerRecipeExpression(expression.Document("root"), map[string]any{}, "Patient")
	if err != nil {
		t.Fatal(err)
	}
	if physical.Kind != ir.PhysicalObjectExpression || physical.Object == nil {
		t.Fatalf("document is not an object expression: %#v", physical)
	}
	if len(physical.Object.Fields) != 4 {
		t.Fatalf("document fields = %d, want 4", len(physical.Object.Fields))
	}
	want := map[string]string{"payload": "payload", "id": "id", "resourceType": "resourceType", "key": "_key"}
	for _, field := range physical.Object.Fields {
		if field.Expression.Value == nil || field.Expression.Value.Variable != "root" {
			t.Fatalf("field %q source = %#v", field.Name, field.Expression)
		}
		if want[field.Name] != field.Expression.Value.Path[0] {
			t.Fatalf("field %q path = %#v", field.Name, field.Expression.Value.Path)
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
	selector, err := spec.ParseSelector("identifier[].value")
	if err != nil {
		t.Fatal(err)
	}
	selector.Filter = &fhirschema.ContainsFilter{Field: "system", Needle: "case_id"}
	if got := selectorExecutionModeForExpression("Patient", selector, nil); got != ir.PhysicalSelectorGeneric {
		t.Fatalf("predicate selector unexpectedly specialized: %q", got)
	}
	plain, err := spec.ParseSelector("gender")
	if err != nil {
		t.Fatal(err)
	}
	if got := selectorExecutionModeForExpression("Patient", plain, []spec.Selector{selector}); got != ir.PhysicalSelectorGeneric {
		t.Fatalf("fallback selector unexpectedly specialized: %q", got)
	}
}
