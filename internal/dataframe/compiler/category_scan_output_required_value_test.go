package compiler

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestCompileCategoryScanOutputRequiringValueFiltersEmptyPivotColumns(t *testing.T) {
	output := compilePopulationMappingOutput(t, recipe.Output{
		Name: "Dataframe", RootResourceType: "Patient", RowGrain: "patient", RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "gender", ColumnID: "gender_id", Expr: recipe.Expression{Select: "root.gender"}},
			{Name: "birth_date", ColumnID: "birth_date_id", Expr: recipe.Expression{Select: "root.birthDate"}},
		},
	})
	category, value := "gender", "birth_date"
	scanned, err := CompileCategoryScanOutputRequiringValue(output, category, value, 256, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !scanned.Proof.ValuePresenceRequired {
		t.Fatalf("requiring-value proof did not record its value policy: %#v", scanned.Proof)
	}
	if scanned.Proof.ValueColumnID != value {
		t.Fatalf("requiring-value proof bound value column %q, want public column %q: %#v", scanned.Proof.ValueColumnID, value, scanned.Proof)
	}
	if !strings.Contains(scanned.Query, "FILTER __loom_category_row[@__loom_category_value_column] != null") {
		t.Fatalf("requiring-value category scan omitted its non-null value filter:\n%s", scanned.Query)
	}
	if got := scanned.BindVars["__loom_category_value_column"]; got != value {
		t.Fatalf("requiring-value category scan bound value column to %#v, want %s", got, value)
	}
	baseline, err := CompileCategoryScanOutputWithPolicy(output, category, 256, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(baseline.Query, "__loom_category_nonnull") {
		t.Fatalf("fixture does not exercise the category-only indexed shortcut:\n%s", baseline.Query)
	}
	if strings.Contains(scanned.Query, "__loom_category_nonnull") {
		t.Fatalf("requiring-value category scan used the category-only indexed shortcut:\n%s", scanned.Query)
	}
}
