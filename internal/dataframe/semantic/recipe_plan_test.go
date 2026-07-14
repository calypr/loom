package semantic

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestDefaultRecipeBuildsTypedSemanticPlan(t *testing.T) {
	bundle, err := recipe.DefaultACEDBundle()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "project"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Outputs) != len(bundle.Outputs) || plan.RecipeDigest == "" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	group := plan.Outputs[len(plan.Outputs)-1]
	if group.Expansion == nil || group.Expansion.As != "member" || group.Identity == nil {
		t.Fatalf("group expansion/identity missing: %#v", group)
	}
	if got := group.Fields[1].Expr.Type.Kind; got != "string" {
		t.Fatalf("member_id type = %s", got)
	}
	if group.Fields[1].Expr.Expression.Call == nil || group.Fields[1].Expr.Expression.Call.Args[0].Selector == nil || group.Fields[1].Expr.Expression.Call.Args[0].Selector.Context != "member" {
		t.Fatalf("member context = %#v", group.Fields[1].Expr.Expression)
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

func TestGraphQLBuilderUsesSameTypedProjectionShape(t *testing.T) {
	plan, err := BuildRecipePlanFromBuilder(Builder{
		Project: "p", RootResourceType: "Patient", RowGrain: RowGrain("patient"),
		Fields: []FieldSelect{{Name: "id", Select: "id"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Outputs) != 1 || len(plan.Outputs[0].Fields) != 1 {
		t.Fatalf("unexpected builder plan: %#v", plan)
	}
	field := plan.Outputs[0].Fields[0]
	if field.Expr.Type.Kind != "string" || field.Expr.Expression.Selector == nil || field.Expr.Expression.Selector.Context != "root" {
		t.Fatalf("unexpected typed builder field: %#v", field.Expr)
	}
}

func TestRecipeRejectsRepeatedIdentity(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "x", TranslationVersion: "1", Outputs: []recipe.Output{{Name: "x", RootResourceType: "Patient", RowGrain: "expanded", Expand: &recipe.Expansion{From: recipe.Expression{Select: "identifier[]"}, As: "item"}, Identity: &recipe.Identity{Name: "id", Expr: recipe.Expression{Select: "root.identifier[].value"}}}}}
	if _, err := BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "p"}); err == nil || !strings.Contains(err.Error(), "scalar") {
		t.Fatalf("expected scalar identity error, got %v", err)
	}
}
