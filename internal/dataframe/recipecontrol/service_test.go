package recipecontrol

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeexec"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestControlPlaneUsesOneResolvedPlanForExplainAndPreview(t *testing.T) {
	registry := recipeexec.NewRegistry()
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "r", TranslationVersion: "v", Outputs: []recipe.Output{{Name: "Patient", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "id"}}}}}}
	if _, err := registry.Register(bundle); err != nil {
		t.Fatal(err)
	}
	service := Service{Registry: registry, ScopeDigest: func(recipe.RuntimeBindings) string { return "scope" }}
	bindings := recipe.RuntimeBindings{Project: "p", DatasetGeneration: "g"}
	if explanation, err := service.Explain(context.Background(), "r", bindings); err != nil || len(explanation.Outputs) != 1 {
		t.Fatalf("explain failed: %#v %v", explanation, err)
	}
	preview, err := service.Preview(context.Background(), "r", bindings, func(_ context.Context, plan semantic.ResolvedRecipePlan, limit int) (map[string][]map[string]any, error) {
		if plan.ResolvedSchemaDigest == "" || limit != 0 {
			t.Fatalf("unresolved preview plan: %#v", plan)
		}
		return map[string][]map[string]any{"Patient": {{"id": "p1"}}}, nil
	})
	if err != nil || len(preview.Rows["Patient"]) != 1 {
		t.Fatalf("preview failed: %#v %v", preview, err)
	}
}
