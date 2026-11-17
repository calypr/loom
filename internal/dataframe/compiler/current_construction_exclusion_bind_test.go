package compiler

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestCurrentPivotExclusionsBindCategoryTypeOnlyForValueMatches(t *testing.T) {
	for _, test := range []struct {
		name        string
		withLiteral bool
	}{
		{name: "NULL and MISSING only"},
		{name: "NULL MISSING and literal", withLiteral: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := currentNullMissingExclusionOutput()
			if test.withLiteral {
				known := "known"
				pivot := output.Construction.Steps[0].Operation.Pivot
				pivot.Categories = append(pivot.Categories, recipe.ConstructionPivotCategory{
					Key:            recipe.TableScalar{Kind: recipe.TableScalarString, String: &known},
					OutputColumnID: "literal_value",
				})
				output.Construction.Steps[0].Outputs = append(output.Construction.Steps[0].Outputs,
					recipe.StageColumn{ID: "literal_value", Name: "literal_value"})
			}
			compiled, _, err := compileCurrentConstructionOracle(output, "project", "generation", 10)
			if err != nil {
				t.Fatal(err)
			}
			query, err := CompileTableShapeExclusionsWithPolicy(compiled, 0, 10, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatal(err)
			}
			const typeBind = "__loom_physical_exclusion_category_type"
			value, bound := query.BindVars[typeBind]
			referenced := strings.Contains(query.Query, "== @"+typeBind+" AND ")
			if bound != test.withLiteral || referenced != test.withLiteral {
				t.Fatalf("category type bind: supplied=%t referenced=%t; literal category=%t", bound, referenced, test.withLiteral)
			}
			if bound && value != "string" {
				t.Fatalf("literal category type bind=%#v, want string", value)
			}
		})
	}
}
