package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/engine"
	"github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/explorer"
)

type compilerTestRegistry struct{}

func (compilerTestRegistry) LoadRecipe(context.Context, string) (exec.Entry, error) {
	return exec.Entry{}, nil
}

func TestExplorerV2CompilerUsesActiveGenerationWithoutSnapshotToken(t *testing.T) {
	var resolvedGeneration string
	recipeEngine, err := engine.New(engine.Config{
		Registry: compilerTestRegistry{},
		ResolveBundle: func(_ context.Context, bundle recipe.Bundle, bindings recipe.RuntimeBindings) (recipe.Bundle, error) {
			resolvedGeneration = bindings.DatasetGeneration
			return bundle, nil
		},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	snapshot, err := explorer.NewCatalogSnapshot("project-a", "generation-live", "scope", explorer.Catalog{}, true, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	compiler := explorerV2Compiler(recipeEngine, func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) {
		return snapshot, nil
	})

	var configV2 explorer.ConfigV2
	if err := json.Unmarshal(baselineExplorerConfigV2("project-a", "Default"), &configV2); err != nil {
		t.Fatal(err)
	}
	configV2.Explorer.ID = "custom"
	configV2.Explorer.Management = "interactive"
	bundle := testV2Bundle("compiler-test")
	bundle.Outputs[0].RowGrain = "file"
	configV2.Recipe, err = json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	configV2.Views = []explorer.ConfigView{{
		ID: "documents", Title: "Documents", Output: "DocumentReference",
		Table: explorer.ConfigTable{Columns: []explorer.ConfigColumn{{Column: "id", Label: "ID", Visible: true}}},
	}}
	config, err := json.Marshal(configV2)
	if err != nil {
		t.Fatal(err)
	}
	result, err := compiler(context.Background(), ExplorerV2CompileRequest{
		Project:    "project-a",
		ExplorerID: "custom",
		Config:     json.RawMessage(config),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.SourceGeneration != "generation-live" || resolvedGeneration != "generation-live" {
		t.Fatalf("compiler generation = %q, resolver generation = %q; want generation-live", result.SourceGeneration, resolvedGeneration)
	}
}
