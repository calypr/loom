package semantic

import (
	"context"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/schema"
)

func TestDefaultRecipeBuildsTypedSemanticPlan(t *testing.T) {
	bundle := resolvedDefaultACEDBundle(t)
	plan, err := BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "project"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Outputs) != len(bundle.Outputs) || plan.RecipeDigest == "" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	group := plan.Outputs[len(plan.Outputs)-1]
	if group.Unnest == nil || group.Unnest.As != "member" || group.Unnest.JoinMode != UnnestInner || group.Identity == nil {
		t.Fatalf("group expansion/identity missing: %#v", group)
	}
	if got := group.Fields[1].Expr.Type.Kind; got != "string" {
		t.Fatalf("member_id type = %s", got)
	}
	if group.Fields[1].Expr.Expression.Call == nil || group.Fields[1].Expr.Expression.Call.Args[0].Selector == nil || group.Fields[1].Expr.Expression.Call.Args[0].Selector.Context != "member" {
		t.Fatalf("member context = %#v", group.Fields[1].Expr.Expression)
	}
}

func resolvedDefaultACEDBundle(t *testing.T) recipe.Bundle {
	t.Helper()
	bundle, err := recipe.DefaultACEDBundle()
	if err != nil {
		t.Fatal(err)
	}
	resourceTypes := []string{"DocumentReference", "Specimen", "Patient", "ResearchStudy", "Observation", "ResearchSubject", "Condition", "MedicationAdministration", "Group"}
	byType := make(map[string][]schema.FieldCandidate, len(resourceTypes))
	for _, resourceType := range resourceTypes {
		byType[resourceType] = []schema.FieldCandidate{
			{ResourceType: resourceType, Path: "id", Kind: "scalar"},
			{ResourceType: resourceType, Path: "identifier[].system", Kind: "scalar", DistinctValues: []string{"system"}},
			{ResourceType: resourceType, Path: "identifier[].value", Kind: "scalar"},
			{ResourceType: resourceType, Path: "extension[].url", Kind: "scalar", DistinctValues: []string{"url"}},
		}
		if resourceType == "Observation" {
			byType[resourceType] = append(byType[resourceType],
				schema.FieldCandidate{ResourceType: resourceType, Path: "component[].code.text", Kind: "scalar", DistinctValues: []string{"component"}},
				schema.FieldCandidate{ResourceType: resourceType, Path: "component[].valueString", Kind: "scalar"},
				schema.FieldCandidate{ResourceType: resourceType, Path: "component[].valueInteger", Kind: "scalar"},
				schema.FieldCandidate{ResourceType: resourceType, Path: "component[].valueBoolean", Kind: "scalar"},
				schema.FieldCandidate{ResourceType: resourceType, Path: "component[].valueDateTime", Kind: "scalar"},
				schema.FieldCandidate{ResourceType: resourceType, Path: "component[].valueQuantity.value", Kind: "scalar"},
				schema.FieldCandidate{ResourceType: resourceType, Path: "code", Kind: "codeable_concept", PivotCandidate: true, PivotFamily: "observation_code_value", PivotColumns: []string{"code"}, PivotColumnSelect: "code.coding[].display", PivotValueSelect: "valueString"},
				schema.FieldCandidate{ResourceType: resourceType, Path: "component[].code", Kind: "codeable_concept", PivotCandidate: true, PivotFamily: "codeable_concept", PivotColumns: []string{"component"}, PivotColumnSelect: "component[].code.coding[].display", PivotValueSelect: "component[].code.coding[].display"},
			)
		}
	}
	resolved, err := schema.Resolve(context.Background(), bundle, schema.Scope{Project: "test", DatasetGeneration: "generation"}, testDefaultDiscovery{fields: byType})
	if err != nil {
		t.Fatal(err)
	}
	return resolved.Bundle
}

type testDefaultDiscovery struct {
	fields map[string][]schema.FieldCandidate
}

func (d testDefaultDiscovery) Fields(_ context.Context, _ schema.Scope, resourceType string) ([]schema.FieldCandidate, error) {
	return append([]schema.FieldCandidate(nil), d.fields[resourceType]...), nil
}

func TestSemanticUnnestRejectsNonRepeatedAndUnsafeBindings(t *testing.T) {
	base := SemanticUnnest{
		Source: SemanticExpression{Type: expression.Type{Kind: expression.KindString, Cardinality: expression.OptionalOne}},
		As:     "item", JoinMode: UnnestInner,
	}
	if err := base.Validate(); err == nil || !strings.Contains(err.Error(), "repeated") {
		t.Fatalf("expected repeated-source validation error, got %v", err)
	}
	base.Source.Type.Cardinality = expression.Many
	base.As = "item.value"
	if err := base.Validate(); err == nil || !strings.Contains(err.Error(), "safe logical name") {
		t.Fatalf("expected safe-binding validation error, got %v", err)
	}
}

func TestSemanticUnnestOuterModeAndOrdinalityAreExplicit(t *testing.T) {
	unnest := SemanticUnnest{
		Source: SemanticExpression{Type: expression.Type{Kind: expression.KindObject, Cardinality: expression.Many}},
		As:     "item", Ordinality: "item_index", JoinMode: UnnestOuter,
	}
	if err := unnest.Validate(); err != nil {
		t.Fatal(err)
	}
	if unnest.JoinMode != UnnestOuter || unnest.Ordinality != "item_index" {
		t.Fatalf("unnest mode/ordinality changed: %#v", unnest)
	}
}

func TestSemanticUnnestRejectsBindingCollision(t *testing.T) {
	unnest := SemanticUnnest{
		Source: SemanticExpression{Type: expression.Type{Kind: expression.KindObject, Cardinality: expression.Many}},
		As:     "item", Ordinality: "item", JoinMode: UnnestInner,
	}
	if err := unnest.Validate(); err == nil || !strings.Contains(err.Error(), "differ") {
		t.Fatalf("expected ordinality collision error, got %v", err)
	}
}

func TestDynamicItemExpressionsUseItemScope(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "dynamic-item", TranslationVersion: "1", Outputs: []recipe.Output{{Name: "x", RootResourceType: "Patient", RowGrain: "patient", DynamicColumns: []recipe.DynamicColumn{{Name: "extension", Source: recipe.Expression{Select: "root.extension[]"}, Key: &recipe.Expression{Select: "item.url"}, Value: &recipe.Expression{Select: "item.url"}, Columns: []string{"http://example.org/code"}}}}}}
	if _, err := BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "p"}); err != nil {
		t.Fatalf("dynamic item expressions were not scoped: %v", err)
	}
}

func TestRecipeRejectsUndefinedAndShadowedAliases(t *testing.T) {
	base := `{"recipeSchemaVersion":1,"name":"x","translationVersion":"1","outputs":[{"name":"x","rootResourceType":"Patient","rowGrain":"patient","fields":[{"name":"id","expr":{"select":"missing.id"}}]}]}`
	bundle, err := recipe.Parse([]byte(base))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "p"}); err == nil || !strings.Contains(err.Error(), "context") {
		t.Fatalf("expected undefined context error, got %v", err)
	}

	shadowed := `{"recipeSchemaVersion":1,"name":"x","translationVersion":"1","outputs":[{"name":"x","rootResourceType":"Patient","rowGrain":"patient","fields":[{"name":"id","expr":{"select":"root.id"}}],"traversals":[{"name":"subject","alias":"root","toResourceType":"Patient"}]}]}`
	bundle, err = recipe.Parse([]byte(shadowed))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "p"}); err == nil || !strings.Contains(err.Error(), "shadows") {
		t.Fatalf("expected shadowing error, got %v", err)
	}
}

func TestRecipeRejectsRepeatedIdentity(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "x", TranslationVersion: "1", Outputs: []recipe.Output{{Name: "x", RootResourceType: "Patient", RowGrain: "expanded", Expand: &recipe.Expansion{From: recipe.Expression{Select: "identifier[]"}, As: "item"}, Identity: &recipe.Identity{Name: "id", Expr: recipe.Expression{Select: "root.identifier[].value"}}}}}
	if _, err := BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "p"}); err == nil || !strings.Contains(err.Error(), "scalar") {
		t.Fatalf("expected scalar identity error, got %v", err)
	}
}
