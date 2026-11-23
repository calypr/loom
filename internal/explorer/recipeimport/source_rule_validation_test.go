package recipeimport

import (
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestSourceRuleDatatypeValidation(t *testing.T) {
	system := recipe.Expression{Call: "last_segment", Args: []recipe.Expression{{Select: "item.system"}}}
	display, sibling := recipe.Expression{Select: "item.display"}, recipe.Expression{Select: "root.code.coding[].display"}
	category := func(value *recipe.Expression) *ws.ImportedSourceParameters {
		return &ws.ImportedSourceParameters{Dynamic: &ws.ImportedDynamicParameters{
			Name: "category", Source: recipe.Expression{Select: "category[].coding[]"}, Key: &system, Value: value,
		}}
	}
	extension := func(path string) *ws.ImportedSourceParameters {
		return &ws.ImportedSourceParameters{Extension: &ws.ImportedExtensionParameters{
			Name: "extensions", Source: recipe.Expression{Select: path},
		}}
	}
	catalog := func(excluded string) *ws.ImportedSourceParameters {
		return &ws.ImportedSourceParameters{Catalog: &ws.ImportedCatalogParameters{
			Name: "fields", IncludePaths: []string{"identifier[].value"}, ExcludePaths: []string{excluded},
		}}
	}
	badNaming := catalog("identifier[].system")
	badNaming.Catalog.Naming = "unsupported"

	for _, tc := range []struct {
		name, kind, resourceType string
		params                   *ws.ImportedSourceParameters
		valid                    bool
	}{
		{"category coding", SourcePlanDynamicColumn, "Observation", category(&display), true},
		{"resource without category", SourcePlanDynamicColumn, "Patient", category(&display), false},
		{"category sibling value", SourcePlanDynamicColumn, "Observation", category(&sibling), false},
		{"nested extension", SourcePlanExtensionColumn, "Patient", extension("extension[].extension[]"), true},
		{"non-extension path", SourcePlanExtensionColumn, "Patient", extension("identifier[]"), false},
		{"catalog paths", SourcePlanCatalogProjection, "Patient", catalog("identifier[].system"), true},
		{"unknown exclusion", SourcePlanCatalogProjection, "Patient", catalog("unavailable"), false},
		{"unknown naming policy", SourcePlanCatalogProjection, "Patient", badNaming, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRuleAddition(tc.kind, tc.resourceType, tc.params)
			if (err == nil) != tc.valid {
				t.Fatalf("valid = %v, received error %v", tc.valid, err)
			}
		})
	}
}

func TestSourceRuleColumnLimitIsBounded(t *testing.T) {
	output := recipe.Output{Name: "patients", RootResourceType: "Patient", RowGrain: "resource"}
	limit := 4
	create := ws.SourceRuleCreate{
		Kind: SourcePlanCatalogProjection, MaxColumns: &limit,
		Parameters: &ws.ImportedSourceParameters{Catalog: &ws.ImportedCatalogParameters{
			Name: "fields", IncludePaths: []string{"gender"},
		}},
	}
	plan, err := EditPlan(output, SourcePlan(output), ws.CatalogSnapshot{}, PlanEdit{RuleCreate: &create}, PlanEditInput{})
	if err != nil {
		t.Fatal(err)
	}
	effective, err := ApplySourcePlan(output, &plan)
	if err != nil {
		t.Fatal(err)
	}
	if effective.CatalogProjections[0].MaxColumns != limit {
		t.Fatalf("compiled limit = %d, want %d", effective.CatalogProjections[0].MaxColumns, limit)
	}
	create.MaxColumns = nil
	withDefault, err := EditPlan(output, SourcePlan(output), ws.CatalogSnapshot{}, PlanEdit{RuleCreate: &create}, PlanEditInput{})
	if err != nil {
		t.Fatal(err)
	}
	step := withDefault.Steps[len(withDefault.Steps)-1]
	if step.MaxColumns == nil || *step.MaxColumns != ws.MaxImportedRuleColumns {
		t.Fatalf("default limit is not available when editing: %+v", step)
	}
	excess := ws.MaxImportedRuleColumns + 1
	change := authoringv2.ImportedSourceStepChange{StepID: plan.RuleAdditions[0].StepID, MaxColumns: &excess}
	if _, err := EditPlan(output, plan, ws.CatalogSnapshot{}, PlanEdit{StepChanges: []authoringv2.ImportedSourceStepChange{change}}, PlanEditInput{}); err == nil {
		t.Fatal("accepted a column limit above the rule maximum")
	}
}

func TestSourcePlanRejects129thRule(t *testing.T) {
	plan := ws.ImportedSourcePlan{RuleAdditions: make([]ws.ImportedSourceRuleAddition, 129)}
	if err := plan.Validate(); err == nil || err.Error() != "source plan has more than 128 rule additions" {
		t.Fatalf("expected the rule addition cap, got %v", err)
	}
}
