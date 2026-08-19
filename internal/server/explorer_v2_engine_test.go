package server

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
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

func TestExplorerV2CompilerResolvesRootCandidatesForRequestedOutput(t *testing.T) {
	firstOutput := testV2Bundle("compiler-test").Outputs[0]
	secondOutput := recipe.Output{
		Name:             "SubstanceDefinition",
		RootResourceType: "SubstanceDefinition",
		RowGrain:         "substance_definition",
		Fields: []recipe.Field{{
			Name:     "id",
			FieldRef: "SubstanceDefinition.id",
			Expr:     recipe.Expression{Select: "root.id"},
		}},
	}
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "compiler-test",
		TranslationVersion:  "interactive",
		Outputs:             []recipe.Output{firstOutput, secondOutput},
	}
	configV2 := explorer.ConfigV2{
		APIVersion: explorer.ConfigV2APIVersion,
		Kind:       "ExplorerConfig",
		Project:    "project-a",
		Explorer: explorer.ConfigExplorer{
			ID:         "custom",
			Title:      "Custom",
			Management: "interactive",
		},
		Views: []explorer.ConfigView{{
			ID: "substance-definition", Title: "Substance definitions", Output: secondOutput.Name,
			Table: explorer.ConfigTable{Columns: []explorer.ConfigColumn{{Column: "id", Visible: true}}},
		}},
	}
	configV2.Recipe, _ = json.Marshal(bundle)
	config, err := json.Marshal(configV2)
	if err != nil {
		t.Fatal(err)
	}

	firstNodeID := explorer.OpaqueID("n_", firstOutput.RootResourceType)
	secondNodeID := explorer.OpaqueID("n_", secondOutput.RootResourceType)
	secondSelectionID := explorer.OpaqueID("s_", "SubstanceDefinition\x00id")
	snapshot, err := explorer.NewCatalogSnapshot(
		"project-a",
		"generation-live",
		"scope",
		explorer.Catalog{
			Nodes: []explorer.CatalogNode{
				{ID: firstNodeID, ResourceType: firstOutput.RootResourceType},
				{ID: secondNodeID, ResourceType: secondOutput.RootResourceType},
			},
			Selections: map[string]explorer.CatalogSelection{
				secondSelectionID: {
					ID:     secondSelectionID,
					NodeID: secondNodeID,
				},
			},
		},
		true,
		false,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	compiler := explorerV2Compiler(nil, func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) {
		return snapshot, nil
	})
	_, err = compiler(context.Background(), ExplorerV2CompileRequest{
		Project:       "project-a",
		ExplorerID:    "custom",
		Config:        config,
		SnapshotToken: snapshot.Token,
		Output:        secondOutput.Name,
		SelectedCandidateIDsByNode: map[string][]string{
			"root": {secondSelectionID},
		},
	})
	if err != nil {
		t.Fatalf("requested output root candidate was rejected: %v", err)
	}
}

func TestExplorerV2CompilerDistinguishesMissingAndWrongNodeCandidates(t *testing.T) {
	bundle := testV2Bundle("compiler-test")
	configV2 := explorer.ConfigV2{
		APIVersion: explorer.ConfigV2APIVersion,
		Kind:       "ExplorerConfig",
		Project:    "project-a",
		Explorer: explorer.ConfigExplorer{
			ID: "custom", Title: "Custom", Management: "interactive",
		},
		Views: []explorer.ConfigView{{
			ID: "documents", Title: "Documents", Output: bundle.Outputs[0].Name,
			Table: explorer.ConfigTable{Columns: []explorer.ConfigColumn{{Column: "id", Visible: true}}},
		}},
	}
	configV2.Recipe, _ = json.Marshal(bundle)
	config, err := json.Marshal(configV2)
	if err != nil {
		t.Fatal(err)
	}

	rootNodeID := explorer.OpaqueID("n_", bundle.Outputs[0].RootResourceType)
	otherNodeID := explorer.OpaqueID("n_", "SubstanceDefinition")
	wrongSelectionID := explorer.OpaqueID("s_", "SubstanceDefinition\x00id")
	snapshot, err := explorer.NewCatalogSnapshot(
		"project-a",
		"generation-live",
		"scope",
		explorer.Catalog{
			Nodes: []explorer.CatalogNode{
				{ID: rootNodeID, ResourceType: bundle.Outputs[0].RootResourceType},
				{ID: otherNodeID, ResourceType: "SubstanceDefinition"},
			},
			Selections: map[string]explorer.CatalogSelection{
				wrongSelectionID: {ID: wrongSelectionID, NodeID: otherNodeID},
			},
		},
		true,
		false,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	compiler := explorerV2Compiler(nil, func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) {
		return snapshot, nil
	})
	compile := func(candidateID string) error {
		_, err := compiler(context.Background(), ExplorerV2CompileRequest{
			Project: "project-a", ExplorerID: "custom", Config: config,
			SnapshotToken: snapshot.Token, Output: bundle.Outputs[0].Name,
			SelectedCandidateIDsByNode: map[string][]string{"root": {candidateID}},
		})
		return err
	}

	missingErr := compile("s_missing")
	if missingErr == nil || !strings.Contains(missingErr.Error(), "is absent from the current snapshot") {
		t.Fatalf("missing candidate error = %v", missingErr)
	}
	wrongNodeErr := compile(wrongSelectionID)
	if wrongNodeErr == nil || !strings.Contains(wrongNodeErr.Error(), "belongs to node "+strconv.Quote(otherNodeID)) {
		t.Fatalf("wrong-node candidate error = %v", wrongNodeErr)
	}
}
