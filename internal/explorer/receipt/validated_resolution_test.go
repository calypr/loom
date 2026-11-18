package receipt

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	recipetest "github.com/calypr/loom/internal/testkit/recipetest"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
)

func TestCompileValidatedReceiptResolutionChecksAllOutputsForScopedPreview(t *testing.T) {
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry: recipetest.Registry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "multi-output-preview",
		TranslationVersion:  explorercompilation.TranslationVersion,
		Outputs: []recipe.Output{
			{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{
				{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}},
				{Name: "patient_gender", Expr: recipe.Expression{Select: "root.gender"}},
			}},
			{Name: "specimens", RootResourceType: "Specimen", RowGrain: "resource", Fields: []recipe.Field{{Name: "specimen_id", Expr: recipe.Expression{Select: "root.id"}}}},
		},
	}
	bindings := recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: "generation-a"}
	compiled, err := recipeEngine.CompileResolvedBundle(context.Background(), bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolvedDigest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	fingerprints, provenance, err := ResolvedOutputArtifacts(compiled)
	if err != nil {
		t.Fatal(err)
	}
	receipt := &explorer.CompilationReceipt{
		Bundle:                 bundle,
		RecipeDigest:           compiled.StoredRecipeDigest,
		ResolvedRecipeDigest:   resolvedDigest,
		ResolvedSchemaDigest:   compiled.ResolvedSchemaDigest,
		OutputFingerprints:     fingerprints,
		OutputColumnProvenance: provenance,
		EmittedColumns: []explorer.EmittedColumn{

			{OutputID: "patients", PublicColumn: "patient_gender"},
			{OutputID: "patients", PublicColumn: "patient_id"},
			{OutputID: "specimens", PublicColumn: "specimen_id"},
		},
	}
	previewBindings := bindings
	previewBindings.OutputNames = []string{"patients"}
	resolved, err := CompileValidatedResolution(context.Background(), recipeEngine, receipt, previewBindings)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Compiled.Outputs) != 2 {
		t.Fatalf("validated outputs=%d, want complete receipt with 2 outputs", len(resolved.Compiled.Outputs))
	}
	if len(previewBindings.OutputNames) != 1 || previewBindings.OutputNames[0] != "patients" {
		t.Fatalf("preview execution selection was mutated: %#v", previewBindings.OutputNames)
	}
}

func TestCompileValidatedReceiptResolutionSurvivesCompilerMetadataJSONRoundTrip(t *testing.T) {
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{Registry: recipetest.Registry{}, QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "round-trip", TranslationVersion: explorercompilation.TranslationVersion, Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}, Discovered: true}}}}}
	bindings := recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: "generation-a"}
	compiled, err := recipeEngine.CompileResolvedBundle(context.Background(), bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	fingerprints, provenance, err := ResolvedOutputArtifacts(compiled)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	receipt := explorer.CompilationReceipt{Bundle: bundle, RecipeDigest: compiled.StoredRecipeDigest, ResolvedRecipeDigest: digest, ResolvedSchemaDigest: compiled.ResolvedSchemaDigest, OutputFingerprints: fingerprints, OutputColumnProvenance: provenance, EmittedColumns: []explorer.EmittedColumn{{OutputID: "patients", PublicColumn: "patient_id"}}}
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var stored explorer.CompilationReceipt
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Bundle.Outputs[0].Fields[0].Discovered {
		t.Fatal("compiler-local provenance unexpectedly survived recipe JSON")
	}
	resolved, err := CompileValidatedResolution(context.Background(), recipeEngine, &stored, bindings)
	if err != nil {
		t.Fatal(err)
	}
	restored := false
	for _, column := range resolved.Compiled.Outputs[0].OutputSchema {
		if column.Name == "patient_id" {
			restored = column.Discovered
		}
	}
	if !restored {
		t.Fatal("durable receipt provenance was not restored")
	}
}

func TestCompileValidatedReceiptResolutionSurvivesCohortConstructionRoundTrip(t *testing.T) {
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry: recipetest.Registry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	filterValue := "specimen-a"
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "cohort-receipt-round-trip",
		TranslationVersion:  explorercompilation.TranslationVersion,
		Outputs: []recipe.Output{{
			Name: "specimens", RootResourceType: "Specimen", RowGrain: "groups",
			Fields: []recipe.Field{{Name: "specimen_id", ColumnID: "specimen_id", Expr: recipe.Expression{Select: "root.id"}}},
			Construction: &recipe.Construction{
				Version:       1,
				SourceColumns: []recipe.StageColumn{{ID: "specimen_id", Name: "specimen_id", Type: "string"}},
				Steps: []recipe.ConstructionStep{{
					ID:     "qa-source-filter",
					Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
						ColumnID: "specimen_id", Operator: recipe.FilterEquals,
						Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &filterValue}},
					}},
					Outputs: []recipe.StageColumn{{ID: "specimen_id", Name: "specimen_id", Type: "string"}},
				}},
			},
			GroupRows: &recipe.GroupRows{
				RevisionID: "grouprev_qa", UnassignedMemberPolicy: "GROUP_AS_UNASSIGNED", AfterStepID: "qa-source-filter",
				RowValues: []recipe.GroupRowValuePolicy{{ColumnID: "specimen_id", Policy: recipe.ConstructionRowValueOne}},
			},
		}},
	}
	bindings := recipe.RuntimeBindings{
		Project: "project-a", SelectionProject: "project-a", DatasetGeneration: "generation-a",
		AuthScopeMode: authscope.ReadScopeUnrestricted,
	}
	compiled, err := recipeEngine.CompileResolvedBundle(context.Background(), bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	fingerprints, provenance, err := ResolvedOutputArtifacts(compiled)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := compiled.Bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	stages, err := ConstructionStages(&compiled)
	if err != nil {
		t.Fatal(err)
	}
	receipt := explorer.CompilationReceipt{
		Bundle: compiled.Bundle, RecipeDigest: compiled.StoredRecipeDigest, ResolvedRecipeDigest: digest,
		ResolvedSchemaDigest: compiled.ResolvedSchemaDigest, OutputFingerprints: fingerprints,
		OutputColumnProvenance: provenance, ConstructionStages: stages,
		EmittedColumns: []explorer.EmittedColumn{{OutputID: "specimens", PublicColumn: "group_label"}},
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var stored explorer.CompilationReceipt
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if _, err := CompileValidatedResolution(context.Background(), recipeEngine, &stored, bindings); err != nil {
		t.Fatalf("cohort construction receipt failed deterministic re-lowering: %v", err)
	}
}

func TestPersistValidatedReceiptRejectsRuntimeMismatchBeforeStore(t *testing.T) {
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{Registry: recipetest.Registry{}, QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "pre-store-validation", TranslationVersion: explorercompilation.TranslationVersion, Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}}}}}}
	bindings := recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: "generation-a"}
	resolved, err := recipeEngine.CompileResolvedBundle(context.Background(), bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	fingerprints, provenance, err := ResolvedOutputArtifacts(resolved)
	if err != nil {
		t.Fatal(err)
	}
	bundleDigest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	receipt := &explorer.CompilationReceipt{Bundle: bundle, RecipeDigest: resolved.StoredRecipeDigest, ResolvedRecipeDigest: bundleDigest, ResolvedSchemaDigest: resolved.ResolvedSchemaDigest, OutputFingerprints: fingerprints, OutputColumnProvenance: provenance, EmittedColumns: []explorer.EmittedColumn{{OutputID: "patients", PublicColumn: "patient_id"}}}
	bad := *receipt
	bad.OutputFingerprints = map[string]string{"patients": "runtime-drift"}
	stores := 0
	persist := func(context.Context, explorer.CompilationReceipt) (*explorer.CompilationReceipt, error) {
		stores++
		return receipt, nil
	}
	if _, err := PersistValidatedReceipt(context.Background(), recipeEngine, &bad, bindings, persist); err == nil || !strings.Contains(err.Error(), "COMPILATION_CONTRACT_MISMATCH") {
		t.Fatalf("runtime mismatch error = %v, want COMPILATION_CONTRACT_MISMATCH", err)
	}
	if stores != 0 {
		t.Fatalf("runtime mismatch reached receipt store %d times", stores)
	}
	if _, err := PersistValidatedReceipt(context.Background(), recipeEngine, receipt, bindings, persist); err != nil {
		t.Fatalf("corrected receipt rejected: %v", err)
	}
	if stores != 1 {
		t.Fatalf("corrected receipt store calls = %d, want 1", stores)
	}
}
