package recipeimport

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestDynamicRuleSelectorsStayWithinTheirRepeatedSourceItem(t *testing.T) {
	identifier := func(source, key, value recipe.Expression) *authoringv2.ImportedDynamicParameters {
		return &authoringv2.ImportedDynamicParameters{Name: "values", Source: source, Key: &key, Value: &value}
	}
	system := recipe.Expression{Select: "item.system"}
	lastSystem := recipe.Expression{Call: "last_segment", Args: []recipe.Expression{system}}
	value := recipe.Expression{Select: "item.value"}
	display := recipe.Expression{Select: "item.display"}

	for _, test := range []struct {
		name      string
		resource  string
		rule      *authoringv2.ImportedDynamicParameters
		wantValid bool
	}{
		{"identifier system and value", "Patient", identifier(recipe.Expression{Select: "identifier[]"}, system, value), true},
		{"category coding system and display", "Observation", identifier(recipe.Expression{Select: "category[].coding[]"}, lastSystem, display), true},
		{"value from sibling array", "Observation", identifier(recipe.Expression{Select: "category[].coding[]"}, lastSystem, recipe.Expression{Select: "root.code.coding[].display"}), false},
		{"key from another repeated item", "Observation", identifier(recipe.Expression{Select: "category[].coding[]"}, recipe.Expression{Select: "item.extension[].url"}, display), false},
		{"key call around external selector", "Observation", identifier(recipe.Expression{Select: "category[].coding[]"}, recipe.Expression{Call: "last_segment", Args: []recipe.Expression{{Select: "root.code.coding[].system"}}}, display), false},
		{"value from another document", "Observation", identifier(recipe.Expression{Select: "category[].coding[]"}, lastSystem, recipe.Expression{Document: &recipe.DocumentRef{Context: "another"}}), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			parameters := &authoringv2.ImportedSourceParameters{Dynamic: test.rule}
			err := validateRuleAddition(SourcePlanDynamicColumn, test.resource, parameters)
			if (err == nil) != test.wantValid {
				t.Fatalf("validation error = %v, want valid %v", err, test.wantValid)
			}
		})
	}
}

func TestCategoryCodingPresetAppliesToItsTraversal(t *testing.T) {
	output := recipe.Output{
		Name: "patients", RootResourceType: "Patient", RowGrain: "resource",
		Traversals: []recipe.Traversal{{Name: "observations", Alias: "observation", ToResourceType: "Observation", OccurrenceID: "observations"}},
	}
	params := &authoringv2.ImportedSourceParameters{Dynamic: &authoringv2.ImportedDynamicParameters{
		Name: "category_codings", Source: recipe.Expression{Select: "category[].coding[]"},
		Key:   &recipe.Expression{Call: "last_segment", Args: []recipe.Expression{{Select: "item.system"}}},
		Value: &recipe.Expression{Select: "item.display"},
	}}
	input := authoringv2.SourceRuleCreate{Kind: SourcePlanDynamicColumn, OccurrenceID: "observations", Parameters: params}
	plan, err := EditPlan(output, SourcePlan(output), authoringv2.CatalogSnapshot{}, PlanEdit{RuleCreate: &input}, PlanEditInput{})
	if err != nil {
		t.Fatal(err)
	}
	effective, err := ApplySourcePlan(output, &plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(effective.DynamicColumns) != 0 || len(effective.Traversals[0].DynamicColumns) != 1 {
		t.Fatalf("unexpected category rule scope: %#v", effective)
	}
	rule := effective.Traversals[0].DynamicColumns[0]
	if rule.Source.Select != "observation.category[].coding[]" || rule.Key.Args[0].Select != "item.system" || rule.Value.Select != "item.display" {
		t.Fatalf("category rule lost its paired selectors: %#v", rule)
	}
}
