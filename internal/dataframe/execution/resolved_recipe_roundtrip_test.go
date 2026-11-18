package execution

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestResolvedEmptyCatalogColumnsSurviveReceiptJSONRoundTrip(t *testing.T) {
	engine, err := New(Config{
		Registry:  invalidRecipeRegistry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	bindings := recipe.RuntimeBindings{Project: "empty-schema-project", SelectionProject: "empty-schema-project", DatasetGeneration: "generation-a"}
	resolved := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "empty-schema", TranslationVersion: "test",
		Outputs: []recipe.Output{{
			Name: "patients", RootResourceType: "Patient", RowGrain: "patient",
			Fields:           []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
			ExtensionColumns: []recipe.ExtensionColumn{{Name: "extensions", Source: recipe.Expression{Select: "root.extension[]"}, Columns: []recipe.ExtensionColumnMapping{}, MaxColumns: 8}},
		}},
	}
	raw, err := json.Marshal(resolved)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := recipe.Parse(raw)
	if err != nil {
		t.Fatalf("parse resolved receipt bundle: %v", err)
	}
	output := decoded.Outputs[0]
	if output.ExtensionColumns[0].Columns == nil || len(output.ExtensionColumns[0].Columns) != 0 {
		t.Fatalf("resolved extension empty schema lost its non-nil empty state: %#v", output.ExtensionColumns[0].Columns)
	}
	if _, err := engine.CompileResolvedBundle(context.Background(), decoded, bindings); err != nil {
		t.Fatalf("compile receipt-round-tripped resolved empty schemas: %v", err)
	}
}

func TestUnresolvedNilCatalogColumnsRemainOmittedAndRejected(t *testing.T) {
	engine, err := New(Config{
		Registry:  invalidRecipeRegistry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	seed := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "unresolved-schema", TranslationVersion: "test",
		Outputs: []recipe.Output{{
			Name: "patients", RootResourceType: "Patient", RowGrain: "patient",
			Fields:           []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
			ExtensionColumns: []recipe.ExtensionColumn{{Name: "extensions", Source: recipe.Expression{Select: "root.extension[]"}, Columns: nil, MaxColumns: 8}},
		}},
	}
	raw, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"columns"`) {
		t.Fatalf("unresolved nil columns must remain omitted: %s", raw)
	}
	decoded, err := recipe.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Outputs[0].ExtensionColumns[0].Columns != nil {
		t.Fatal("unresolved columns changed from nil to resolved empty")
	}
	if _, err := engine.CompileResolvedBundle(context.Background(), decoded, recipe.RuntimeBindings{Project: "empty-schema-project"}); err == nil || !strings.Contains(err.Error(), "extension column \"extensions\" is unresolved") {
		t.Fatalf("unresolved empty declaration compile error = %v, want explicit unresolved source", err)
	}
}
