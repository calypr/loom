package semantic

import (
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestRecipePlanStoresRootProjectionOrderOnlyOnRootNode(t *testing.T) {
	bundle := recipe.Bundle{
		RecipeSchemaVersion: 1, Name: "canonical-fields", TranslationVersion: "test",
		Outputs: []recipe.Output{{Name: "Patient", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{
			{Name: "id", Expr: recipe.Expression{Select: "id"}},
			{Name: "gender", Expr: recipe.Expression{Select: "gender"}},
		}}},
	}
	plan, err := BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	output := plan.Outputs[0]
	if got := len(output.Root.Fields); got != 2 || output.Root.Fields[0].Name != "id" || output.Root.Fields[1].Name != "gender" {
		t.Fatalf("root fields = %#v, want authored order", output.Root.Fields)
	}
	typ := reflect.TypeOf(output)
	if _, ok := typ.FieldByName("Fields"); ok {
		t.Fatal("OutputPlan retains duplicate root Fields storage")
	}
	if _, ok := typ.FieldByName("DeclaredOrder"); ok {
		t.Fatal("OutputPlan retains duplicate declared order storage")
	}
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
