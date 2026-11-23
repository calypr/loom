package compiler

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
)

func TestAuthResourcePathProjectionPreservesOpaqueColumnIDCollision(t *testing.T) {
	output := constructionPreviewPivotOutput("root.id", false)
	output.Fields[0].ColumnID = "__loom_auth_resource_path"
	output.Construction.SourceColumns[0].ID = "__loom_auth_resource_path"
	output.Construction.Steps[0].Operation.Pivot.GroupKeyIDs[0] = "__loom_auth_resource_path"
	output.Construction.Steps[0].Outputs[0].ID = "__loom_auth_resource_path"
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: output.Name, TranslationVersion: "test", Outputs: []recipe.Output{output}}
	if err := bundle.Validate(); err != nil {
		t.Fatalf("invalid fixture: %v", err)
	}
	for _, include := range []bool{false, true} {
		bindings := recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation", IncludeAuthResourcePath: include}
		plan, err := recipeplan.BuildRecipePlan(bundle, bindings)
		if err != nil {
			t.Fatal(err)
		}
		resolved, err := recipeplan.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = CompileResolvedRecipePlanWithPolicy(resolved, 0, ir.DefaultPhysicalOptimizationPolicy()); err != nil {
			t.Errorf("valid opaque column ID failed only with auth projection=%v: %v", include, err)
		}
	}
}
