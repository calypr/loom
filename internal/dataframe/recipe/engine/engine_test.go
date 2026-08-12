package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
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

func TestResolveBundleDoesNotPersistAndSupportsSelectedOutputs(t *testing.T) {
	queries := 0
	engine, err := New(Config{
		Registry: invalidRecipeRegistry{},
		QueryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
			queries++
			return visit(map[string]any{"id": "p-1", "__loom_row_id": "row-1"})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "inline", TranslationVersion: "draft", Outputs: []recipe.Output{
		{Name: "Patients", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}}},
		{Name: "Observations", RootResourceType: "Observation", RowGrain: "observation", Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}}},
	}}
	resolved, err := engine.ResolveBundle(context.Background(), bundle, recipe.RuntimeBindings{Project: "project-a", OutputNames: []string{"Patients"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Compiled.Outputs) != 1 || resolved.Compiled.Outputs[0].Name != "Patients" {
		t.Fatalf("compiled outputs = %#v", resolved.Compiled.Outputs)
	}
	rows, err := engine.Preview(context.Background(), resolved, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows["Patients"]) != 1 || queries != 1 {
		t.Fatalf("rows=%#v queries=%d", rows, queries)
	}
}

func TestResolveBundleClassifiesRecipeCompatibilityFailureAsInvalidRequest(t *testing.T) {
	engine, err := New(Config{
		Registry: invalidRecipeRegistry{},
		ResolveBundle: func(context.Context, recipe.Bundle, recipe.RuntimeBindings) (recipe.Bundle, error) {
			return recipe.Bundle{}, fmt.Errorf("selected extension family does not apply")
		},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "inline", TranslationVersion: "draft", Outputs: []recipe.Output{{Name: "Rows", RootResourceType: "Practitioner", RowGrain: "resource", Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}}}}}
	_, err = engine.ResolveBundle(context.Background(), bundle, recipe.RuntimeBindings{Project: "p", OutputNames: []string{"Rows"}})
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeInvalidRequest) || userErr.Retryable() {
		t.Fatalf("error = %#v, want non-retryable INVALID_REQUEST", err)
	}
}

func TestPreviewRejectsUnsupportedLimit(t *testing.T) {
	engine, err := New(Config{Registry: invalidRecipeRegistry{}, QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Preview(context.Background(), Resolved{}, 7); err == nil {
		t.Fatal("Preview accepted unsupported limit")
	}
}
