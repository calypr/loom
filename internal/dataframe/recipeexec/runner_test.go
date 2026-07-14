package recipeexec

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestRegistryIsImmutableAndRunnerIsAtomic(t *testing.T) {
	bundle, err := recipe.Parse([]byte(`{"recipeSchemaVersion":1,"name":"demo","translationVersion":"1","outputs":[{"name":"rows","rootResourceType":"Patient","rowGrain":"patient","fields":[{"name":"id","expr":{"select":"root.id"}}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	if _, err := registry.Register(bundle); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Register(bundle); err != nil {
		t.Fatal(err)
	}
	bundle.TranslationVersion = "2"
	if _, err := registry.Register(bundle); err == nil {
		t.Fatal("expected immutable registration error")
	}
	runner := Runner{Registry: registry, Roots: func(context.Context, recipe.Output, recipe.RuntimeBindings) ([]map[string]any, error) {
		return []map[string]any{{"id": "p1"}}, nil
	}}
	result, err := runner.Run(context.Background(), "demo", recipe.RuntimeBindings{Project: "p"})
	if err != nil || len(result.Outputs["rows"]) != 1 || result.Outputs["rows"][0]["id"] != "p1" {
		t.Fatalf("unexpected result: %#v %v", result, err)
	}
}

func TestDefaultBundleExecutesExpansionFromRecipeData(t *testing.T) {
	bundle, err := recipe.DefaultACEDBundle()
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	if _, err := registry.Register(bundle); err != nil {
		t.Fatal(err)
	}
	runner := Runner{Registry: registry, Roots: func(_ context.Context, output recipe.Output, _ recipe.RuntimeBindings) ([]map[string]any, error) {
		if output.RootResourceType != "Group" {
			return nil, nil
		}
		return []map[string]any{{"id": "g1", "member": []any{map[string]any{"entity": map[string]any{"reference": "Patient/p1"}}}}}, nil
	}}
	result, err := runner.Run(context.Background(), bundle.Name, recipe.RuntimeBindings{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Outputs["GroupMember"]) != 1 || result.Outputs["GroupMember"][0]["member_id"] != "p1" {
		t.Fatalf("unexpected default output: %#v", result.Outputs["GroupMember"])
	}
}
