package recipeimport

import (
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestExtensionRuleAppliesToRootAndTraversal(t *testing.T) {
	output := recipe.Output{Name: "patients", RootResourceType: "Patient", RowGrain: "resource",
		Traversals: []recipe.Traversal{{Name: "observations", Alias: "observation", ToResourceType: "Observation", OccurrenceID: "observations"}},
	}
	for _, occurrence := range []string{"", "observations"} {
		name := "root"
		if occurrence != "" {
			name = "traversal"
		}
		t.Run(name, func(t *testing.T) {
			rule := ws.SourceRuleCreate{Kind: SourcePlanExtensionColumn, OccurrenceID: occurrence,
				Parameters: &ws.ImportedSourceParameters{Extension: &ws.ImportedExtensionParameters{
					Name: "extensions", Source: recipe.Expression{Select: "extension[].extension[]"},
				}},
			}
			plan, err := EditPlan(output, SourcePlan(output), ws.CatalogSnapshot{}, PlanEdit{RuleCreate: &rule}, PlanEditInput{})
			if err != nil {
				t.Fatal(err)
			}
			effective, err := ApplySourcePlan(output, &plan)
			if err != nil {
				t.Fatal(err)
			}
			selected, other, alias := effective.ExtensionColumns, effective.Traversals[0].ExtensionColumns, "root"
			if occurrence != "" {
				selected, other, alias = other, selected, effective.Traversals[0].Alias
			}
			if len(selected) != 1 || len(other) != 0 || selected[0].Source.Select != alias+".extension[].extension[]" {
				t.Fatalf("rule did not stay at the selected occurrence: selected=%#v other=%#v", selected, other)
			}
		})
	}
}
