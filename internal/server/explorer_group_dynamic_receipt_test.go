package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
)

func TestGroupedDynamicSourceReceiptReplaysWithoutDiscardingValidation(t *testing.T) {
	engine, err := dataframeexecution.New(dataframeexecution.Config{Registry: compilerTestRegistry{}, ScopeDigest: func(recipe.RuntimeBindings) string { return "scope" }, QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "grouped-dynamic-source", TranslationVersion: "test", Outputs: []recipe.Output{{
		Name: "patients", RootResourceType: "Patient", RowGrain: "patient",
		Fields:         []recipe.Field{{Name: "gender", ColumnID: "gender_id", Expr: recipe.Expression{Select: "root.gender"}}},
		DynamicColumns: []recipe.DynamicColumn{{Name: "identifier", Source: recipe.Expression{Select: "root.identifier[]"}, Key: &recipe.Expression{Select: "item.value"}, Columns: []string{"x"}}},
		Construction: &recipe.Construction{Version: 1, SourceColumns: []recipe.StageColumn{{ID: "gender_id", Name: "gender", Type: "string"}, {ID: "identifier_x", Name: "identifier_x"}}, Steps: []recipe.ConstructionStep{{
			ID: "group", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}}, Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{ConstructionID: "group", Keys: []recipe.ConstructionGroupKey{{InputColumnID: "gender_id", OutputColumnID: "group_gender"}}, Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "group_count"}}}}, Outputs: []recipe.StageColumn{{ID: "group_gender", Name: "gender", Type: "string"}, {ID: "group_count", Name: "row_count", Type: "integer"}},
		}}},
	}}}
	bindings := recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: "generation-a"}
	resolved, err := engine.CompileResolvedBundle(context.Background(), bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Compiled.Outputs[0].DynamicColumns) != 1 {
		t.Fatal("source dynamic validation metadata lost")
	}
	fingerprints, provenance, err := resolvedOutputArtifacts(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := provenance["patients"]["identifier_x"]; ok {
		t.Fatal("Group exposed source-only dynamic projection")
	}
	digest, err := resolved.Bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	stages, err := receiptConstructionStages(&resolved)
	if err != nil {
		t.Fatal(err)
	}
	receipt := explorer.CompilationReceipt{Bundle: resolved.Bundle, RecipeDigest: resolved.StoredRecipeDigest, ResolvedRecipeDigest: digest, ResolvedSchemaDigest: resolved.ResolvedSchemaDigest, OutputFingerprints: fingerprints, OutputColumnProvenance: provenance, ConstructionStages: stages}
	for _, output := range resolved.Compiled.Outputs {
		for _, column := range output.OutputSchema {
			if !column.Internal && !column.Identity {
				receipt.EmittedColumns = append(receipt.EmittedColumns, explorer.EmittedColumn{OutputID: output.Name, PublicColumn: column.Name})
			}
		}
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var stored explorer.CompilationReceipt
	if err = json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	replay, err := compileValidatedReceiptResolution(context.Background(), engine, &stored, bindings)
	if err != nil {
		t.Fatalf("unchanged Group with source-only dynamic checks cannot replay its receipt: %v", err)
	}
	if len(replay.Compiled.Outputs[0].DynamicColumns) != 1 {
		t.Fatal("receipt replay discarded source dynamic validation")
	}
	for _, change := range []struct {
		name  string
		apply func()
		undo  func()
	}{
		{"key", func() { replay.Compiled.Outputs[0].DynamicColumns[0].SourceKey = "unexpected" }, func() { replay.Compiled.Outputs[0].DynamicColumns[0].SourceKey = "x" }},
		{"cardinality", func() {
			replay.Compiled.Outputs[0].DynamicColumns[0].Many = !replay.Compiled.Outputs[0].DynamicColumns[0].Many
		}, func() {
			replay.Compiled.Outputs[0].DynamicColumns[0].Many = !replay.Compiled.Outputs[0].DynamicColumns[0].Many
		}},
	} {
		change.apply()
		if err := validateReceiptResolution(&stored, &replay); err == nil {
			t.Fatalf("receipt accepted changed source dynamic %s validation", change.name)
		}
		change.undo()
	}
	delete(stored.OutputColumnProvenance["patients"], "row_count")
	if _, err := compileValidatedReceiptResolution(context.Background(), engine, &stored, bindings); err == nil || !strings.Contains(err.Error(), "provenance") {
		t.Fatalf("receipt accepted missing final Group column provenance: %v", err)
	}
}
