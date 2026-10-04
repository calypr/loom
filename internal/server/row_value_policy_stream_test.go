package server

import (
	"context"
	"errors"
	"fmt"
	"testing"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/compilation"
)

func TestValidateReceiptFullStreamScansPastPreviewCapAndOnlySelectedOutput(t *testing.T) {
	lateViolation := errors.New("late ONE policy assertion")
	rowsVisited := 0
	queries := 0
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry: compilerTestRegistry{},
		QueryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
			queries++
			if queries != 1 {
				return fmt.Errorf("unrelated receipt output was streamed")
			}
			for index := 0; index < 1007; index++ {
				row := map[string]any{
					"patient_id":    fmt.Sprintf("patient-%04d", index),
					"__loom_row_id": fmt.Sprintf("row-%04d", index),
				}
				if err := visit(row); err != nil {
					return err
				}
				rowsVisited++
			}
			return lateViolation
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "row-value-policy-preflight", TranslationVersion: compilation.TranslationVersion,
		Outputs: []recipe.Output{
			{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}}}},
			{Name: "specimens", RootResourceType: "Specimen", RowGrain: "specimen", Fields: []recipe.Field{{Name: "specimen_id", Expr: recipe.Expression{Select: "root.id"}}}},
		},
	}
	compileBindings := recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: "generation-a"}
	compiled, err := recipeEngine.CompileResolvedBundle(context.Background(), bundle, compileBindings)
	if err != nil {
		t.Fatal(err)
	}
	resolvedDigest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	fingerprints, provenance, err := resolvedOutputArtifacts(compiled)
	if err != nil {
		t.Fatal(err)
	}
	receipt := &explorer.CompilationReceipt{
		Bundle: bundle, RecipeDigest: compiled.StoredRecipeDigest,
		ResolvedRecipeDigest: resolvedDigest, ResolvedSchemaDigest: compiled.ResolvedSchemaDigest,
		OutputFingerprints: fingerprints, OutputColumnProvenance: provenance,
		EmittedColumns: []explorer.EmittedColumn{
			{OutputID: "patients", PublicColumn: "patient_id"},
			{OutputID: "specimens", PublicColumn: "specimen_id"},
		},
	}

	err = validateReceiptFullStream(context.Background(), recipeEngine, receipt, recipe.RuntimeBindings{
		Project: "project-a", DatasetGeneration: "generation-a", PreviewLimit: 0, OutputNames: []string{"patients"},
	})
	if !errors.Is(err, lateViolation) {
		t.Fatalf("full-stream validation error = %v, want late row-value assertion", err)
	}
	if rowsVisited != 1007 {
		t.Fatalf("full stream visited %d rows before the late error, want 1007", rowsVisited)
	}
	if queries != 1 {
		t.Fatalf("query count = %d, want only selected output patients", queries)
	}
}
