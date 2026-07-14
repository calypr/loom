package recipeengine

import (
	"context"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeexec"
)

func TestEngineResolvesAndStreamsRecipeThroughOneScopedQuery(t *testing.T) {
	registry := recipeexec.NewRegistry()
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "r", TranslationVersion: "v", Outputs: []recipe.Output{{Name: "Patient", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}}}}}
	if _, err := registry.Register(bundle); err != nil {
		t.Fatal(err)
	}
	var query string
	engine, err := New(Config{Registry: LocalRegistry{Registry: registry}, ScopeDigest: func(recipe.RuntimeBindings) string { return "scope" }, QueryRows: func(_ context.Context, q string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		query = q
		return visit(map[string]any{"id": "p1"})
	}})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := engine.Resolve(context.Background(), "r", recipe.RuntimeBindings{Project: "p", DatasetGeneration: "g"})
	if err != nil {
		t.Fatal(err)
	}
	streams, err := engine.Streams(context.Background(), resolved)
	if err != nil {
		t.Fatal(err)
	}
	if len(streams) != 1 || !strings.Contains(streams[0].Query, "root.project == @project") || !strings.Contains(streams[0].Query, "root.dataset_generation == @dataset_generation") {
		t.Fatalf("unscoped recipe query: %#v", streams)
	}
	result, err := streams[0].Stream(context.Background(), func(row map[string]any) error {
		if row["id"] != "p1" {
			t.Fatalf("unexpected row: %#v", row)
		}
		return nil
	})
	if err != nil || result.RowCount != 1 || !strings.Contains(query, "RETURN") {
		t.Fatalf("stream failed: %#v %v", result, err)
	}
}

func TestEngineExecutesUUIDThroughPostQueryMarker(t *testing.T) {
	registry := recipeexec.NewRegistry()
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "uuid", TranslationVersion: "v", Outputs: []recipe.Output{{Name: "Patient", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Call: "uuid5", Args: []recipe.Expression{{Literal: []byte(`"namespace"`)}, {Literal: []byte(`"name"`)}, {Literal: []byte(`"extra"`)}}}}}}}}
	if _, err := registry.Register(bundle); err != nil {
		t.Fatal(err)
	}
	engine, err := New(Config{Registry: LocalRegistry{Registry: registry}, ScopeDigest: func(recipe.RuntimeBindings) string { return "scope" }, QueryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		return visit(map[string]any{"id": map[string]any{exactUUIDOperationKey: "uuid5", exactUUIDArgsKey: []any{"namespace", "name", "extra"}}})
	}})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := engine.Resolve(context.Background(), "uuid", recipe.RuntimeBindings{Project: "p", DatasetGeneration: "g"})
	if err != nil {
		t.Fatal(err)
	}
	streams, err := engine.Streams(context.Background(), resolved)
	if err != nil || len(streams) != 1 || !strings.Contains(streams[0].Query, exactUUIDOperationKey) {
		t.Fatalf("expected exact UUID marker, streams=%#v err=%v", streams, err)
	}
	_, err = streams[0].Stream(context.Background(), func(row map[string]any) error {
		if row["id"] != "5a5d1afb-8a1f-5864-b1f7-f3f2c50f47a8" {
			t.Fatalf("unexpected UUID result: %#v", row)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestEngineRendersFrozenDynamicColumns(t *testing.T) {
	registry := recipeexec.NewRegistry()
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "dynamic", TranslationVersion: "v", Outputs: []recipe.Output{{Name: "Patient", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}}, DynamicColumns: []recipe.DynamicColumn{{Name: "code", Source: recipe.Expression{Select: "root.identifier[].value"}, Columns: []string{"a"}}}}}}
	if _, err := registry.Register(bundle); err != nil {
		t.Fatal(err)
	}
	engine, err := New(Config{Registry: LocalRegistry{Registry: registry}, ScopeDigest: func(recipe.RuntimeBindings) string { return "scope" }, QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := engine.Resolve(context.Background(), "dynamic", recipe.RuntimeBindings{Project: "p", DatasetGeneration: "g"})
	if err != nil {
		t.Fatal(err)
	}
	streams, err := engine.Streams(context.Background(), resolved)
	if err != nil {
		t.Fatal(err)
	}
	if len(streams) != 1 || !strings.Contains(streams[0].Query, "code_a") || !strings.Contains(streams[0].Query, "recipe_dynamic_item") || !strings.Contains(streams[0].Query, "__loom_dynamic_runtime_keys") {
		t.Fatalf("dynamic column was not rendered: %#v", streams)
	}
}

func TestEngineRendersDynamicItemKeyAndValue(t *testing.T) {
	registry := recipeexec.NewRegistry()
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "dynamic-item", TranslationVersion: "v", Outputs: []recipe.Output{{Name: "Patient", RootResourceType: "Patient", RowGrain: "patient", DynamicColumns: []recipe.DynamicColumn{{Name: "extension", Source: recipe.Expression{Select: "root.extension[]"}, Key: &recipe.Expression{Select: "item.url"}, Value: &recipe.Expression{Select: "item.url"}, Columns: []string{"http://example.org/code"}}}}}}
	if _, err := registry.Register(bundle); err != nil {
		t.Fatal(err)
	}
	engine, err := New(Config{Registry: LocalRegistry{Registry: registry}, ScopeDigest: func(recipe.RuntimeBindings) string { return "scope" }, QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := engine.Resolve(context.Background(), "dynamic-item", recipe.RuntimeBindings{Project: "p", DatasetGeneration: "g"})
	if err != nil {
		t.Fatal(err)
	}
	streams, err := engine.Streams(context.Background(), resolved)
	if err != nil {
		t.Fatal(err)
	}
	if len(streams) != 1 || !strings.Contains(streams[0].Query, "recipe_dynamic_item.url") || !strings.Contains(streams[0].Query, "extension_http___example_org_code") {
		t.Fatalf("dynamic item projection was not rendered: %#v", streams)
	}
}

func TestDefaultBundleBuildsExecutableStreams(t *testing.T) {
	bundle, err := recipe.DefaultACEDBundle()
	if err != nil {
		t.Fatal(err)
	}
	registry := recipeexec.NewRegistry()
	if _, err := registry.Register(bundle); err != nil {
		t.Fatal(err)
	}
	engine, err := New(Config{Registry: LocalRegistry{Registry: registry}, ScopeDigest: func(recipe.RuntimeBindings) string { return "scope" }, QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := engine.Resolve(context.Background(), bundle.Name, recipe.RuntimeBindings{Project: "p", DatasetGeneration: "g"})
	if err != nil {
		t.Fatal(err)
	}
	streams, err := engine.Streams(context.Background(), resolved)
	if err != nil || len(streams) != len(bundle.Outputs) {
		t.Fatalf("default bundle streams = %d, err=%v", len(streams), err)
	}
}
