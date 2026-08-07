package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/exec"
)

type invalidRecipeRegistry struct{}

func (invalidRecipeRegistry) LoadRecipe(context.Context, string) (exec.Entry, error) {
	return exec.Entry{Bundle: recipe.Bundle{
		RecipeSchemaVersion: 1,
		Name:                "default",
		TranslationVersion:  "test",
		Outputs: []recipe.Output{{
			Name: "Patient", RootResourceType: "Patient", RowGrain: "patient",
			Fields: []recipe.Field{{Name: "missing", Expr: recipe.Expression{Select: "root.missing"}}},
		}},
	}}, nil
}

func TestMaterializeMarksResolutionFailures(t *testing.T) {
	engine, err := New(Config{
		Registry: invalidRecipeRegistry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = engine.Materialize(context.Background(), "default", recipe.RuntimeBindings{Project: "P1"}, nil)
	var resolution *ResolutionError
	if !errors.As(err, &resolution) {
		t.Fatalf("error = %v, want ResolutionError", err)
	}
}
