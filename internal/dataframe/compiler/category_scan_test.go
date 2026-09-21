package compiler

import (
	"errors"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestCompileCategoryScanWrapsCompleteFinalOutputWithBoundedDistinct(t *testing.T) {
	two := int64(2)
	output := compilePopulationMappingOutput(t, recipe.Output{
		Name: "Observations", RootResourceType: "Observation", RowGrain: "observation",
		RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "id", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "base", Expr: recipe.Expression{Select: "root.valueInteger"}},
		},
		DerivedColumns: []recipe.DerivedColumn{{
			ConstructionID: "double-base", Name: "doubled", Label: "Doubled", Operation: recipe.DerivedMultiply,
			Left:               recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "base"},
			Right:              recipe.DerivedOperand{Kind: recipe.DerivedLiteralOperand, Literal: &recipe.DerivedLiteral{Kind: recipe.NumericInteger, Integer: &two}},
			MissingInputPolicy: recipe.MissingInputError,
		}},
	})
	compiled, err := CompileCategoryScanOutputWithPolicy(output, "doubled", 256, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"LET __loom_category_rows = (", "HAS(__loom_category_row, @__loom_category_column)",
		"__loom_category_row[@__loom_category_column]", "COLLECT __loom_category_group_present",
		"SORT __loom_category_group_present ASC", "LIMIT @__loom_category_limit",
	} {
		if !strings.Contains(compiled.Query, want) {
			t.Fatalf("category query missing %q:\n%s", want, compiled.Query)
		}
	}
	if strings.Contains(compiled.Query, "[\"doubled\"]") {
		t.Fatalf("category column was interpolated into AQL:\n%s", compiled.Query)
	}
	if got := compiled.BindVars[categoryColumnBind]; got != "doubled" {
		t.Fatalf("column bind = %#v", got)
	}
	if got := compiled.BindVars[categoryLimitBind]; got != 257 {
		t.Fatalf("limit bind = %#v", got)
	}
	collectAt := strings.LastIndex(compiled.Query, "COLLECT __loom_category_group_present")
	limitAt := strings.LastIndex(compiled.Query, "LIMIT @__loom_category_limit")
	if collectAt < 0 || limitAt < collectAt {
		t.Fatalf("distinct must precede category limit:\n%s", compiled.Query)
	}
	if strings.Contains(compiled.Query[:collectAt], "LIMIT @limit") {
		t.Fatalf("category scan retained a preview/root limit before distinct:\n%s", compiled.Query)
	}
	if compiled.Proof.OutputSchemaDigest == "" || compiled.Proof.PlanFingerprint == "" || compiled.Proof.QueryFingerprint == "" || compiled.Proof.Fingerprint == "" {
		t.Fatalf("incomplete proof: %#v", compiled.Proof)
	}
	if compiled.Proof.Output != "Observations" || compiled.Proof.Column != "doubled" || compiled.Proof.MaxValues != 256 {
		t.Fatalf("wrong proof binding: %#v", compiled.Proof)
	}
}

func TestCompileCategoryScanRefusesNonPublicOrNonScalarColumns(t *testing.T) {
	base := lower.CompiledRecipeOutput{OutputSchema: []lower.CompiledOutputColumn{
		{Name: "internal", Kind: string(expression.KindString), Cardinality: string(expression.RequiredOne), Internal: true},
		{Name: "identity", Kind: string(expression.KindString), Cardinality: string(expression.RequiredOne), Identity: true},
		{Name: "repeated", Kind: string(expression.KindString), Cardinality: string(expression.Many)},
		{Name: "object", Kind: string(expression.KindObject), Cardinality: string(expression.OptionalOne)},
		{Name: "unknown_kind", Kind: "mystery", Cardinality: string(expression.OptionalOne)},
	}}
	tests := []struct {
		name string
		code CategoryScanRefusalCode
	}{
		{"missing", CategoryScanColumnUnknown},
		{"internal", CategoryScanColumnInternal},
		{"identity", CategoryScanColumnIdentity},
		{"repeated", CategoryScanColumnRepeated},
		{"object", CategoryScanColumnUnsupported},
		{"unknown_kind", CategoryScanColumnUnsupported},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := CompileCategoryScanOutputWithPolicy(base, test.name, 256, ir.DefaultPhysicalOptimizationPolicy())
			var refusal *CategoryScanRefusal
			if !errors.As(err, &refusal) || refusal.Code != test.code {
				t.Fatalf("error = %#v, want %s", err, test.code)
			}
			if code, ok := CategoryScanRefusalCodeOf(err); !ok || code != test.code {
				t.Fatalf("typed code = %q/%t", code, ok)
			}
		})
	}
}

func TestCompileCategoryScanProofChangesWithSchemaAndSelectedColumn(t *testing.T) {
	output := compilePopulationMappingOutput(t, recipe.Output{
		Name: "Patients", RootResourceType: "Patient", RowGrain: "patient", RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}, {Name: "gender", Expr: recipe.Expression{Select: "root.gender"}}},
	})
	left, err := CompileCategoryScanOutputWithPolicy(output, "gender", 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rightOutput := output
	rightOutput.OutputSchema = lower.CloneCompiledOutputSchema(output.OutputSchema)
	rightOutput.OutputSchema[0].SemanticPath += ":changed"
	right, err := CompileCategoryScanOutputWithPolicy(rightOutput, "gender", 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if left.Proof.OutputSchemaDigest == right.Proof.OutputSchemaDigest || left.Proof.Fingerprint == right.Proof.Fingerprint {
		t.Fatalf("proof was not bound to exact schema: %#v %#v", left.Proof, right.Proof)
	}
}
