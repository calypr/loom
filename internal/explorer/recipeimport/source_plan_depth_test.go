package recipeimport

import (
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"testing"
)

func TestImportSourcePlanPreservesDeepNativeRoutes(t *testing.T) {
	var children []recipe.Traversal
	for i := 0; i < 24; i++ {
		children = []recipe.Traversal{{Name: "link", ToResourceType: "Patient", Traversals: children}}
	}
	o := recipe.Output{Name: "out", RootResourceType: "Patient", RowGrain: "RECORD", Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}}, Traversals: children}
	b := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "seed", TranslationVersion: "v1", Outputs: []recipe.Output{o}}
	if e := b.Validate(); e != nil {
		t.Fatalf("native validation: %v", e)
	}
	t.Log("native Bundle.Validate PASS")
	c := authoringv2.CatalogSnapshot{Nodes: []authoringv2.CatalogNode{{ID: "patient", ResourceType: "Patient"}}}
	_, e := ImportWorkspace(b, c, map[string][]recipe.StageColumn{"out": {{ID: "id", Name: "id", Type: "string"}}})
	if e != nil {
		t.Fatalf("ImportWorkspace: %v", e)
	}
	t.Log("ImportWorkspace PASS")
}
