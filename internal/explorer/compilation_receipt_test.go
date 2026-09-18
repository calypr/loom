package explorer

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func testReceipt() CompilationReceipt {
	r := CompilationReceipt{
		ReceiptFormatVersion:     CompilationReceiptFormatVersion,
		CompilerContractVersion:  CompilationReceiptCompilerContractVersion,
		Project:                  "project-a",
		ExplorerID:               "explorer-a",
		IntentDigest:             "sha256:intent",
		SnapshotToken:            "sha256:snapshot",
		AuthorizationScopeDigest: "sha256:scope",
		CapabilitySchemaDigest:   "sha256:schema",
		ShapeDigest:              "sha256:shape",
		SourceGeneration:         "generation-a",
		RecipeDigest:             "sha256:recipe",
		ResolvedSchemaDigest:     "sha256:resolved-schema",
		NormalizedBundle:         json.RawMessage(`{"documents":[]}`),
		Bundle:                   recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "receipt-test", TranslationVersion: "test", Outputs: []recipe.Output{{Name: "out", RootResourceType: "Patient", RowGrain: "patient"}}},
		CompiledConfig:           json.RawMessage(`{"views":[]}`),
		PublicOutputContract:     json.RawMessage(`{"outputs":[{"outputId":"out","columns":[],"lossless":true,"mlReady":true}]}`),
		OutputColumnProvenance:   map[string]map[string]string{"out": {"__loom_row_id": "EXPLICIT"}},
		Warnings:                 []CompilationWarning{{Code: "EMPTY_OUTPUT", Message: "output has no selected fields"}},
	}
	r.ResolvedRecipeDigest, _ = r.Bundle.Digest()
	r.OutputContractDigest, _ = CompilationArtifactDigest(r.PublicOutputContract)
	r.CompilationKey, _ = CompilationKey(r)
	return r
}

func TestCompilationReceiptIdentityExcludesMutableMetadata(t *testing.T) {
	base := testReceipt()
	id, err := ReceiptID(base)
	if err != nil {
		t.Fatal(err)
	}
	base.ID = "some-other-id"
	base.RequestID = "request-2"
	base.CreatedAt = time.Now().UTC()
	got, err := ReceiptID(base)
	if err != nil {
		t.Fatal(err)
	}
	if got != id {
		t.Fatalf("mutable metadata changed receipt ID: %q != %q", got, id)
	}
}

func TestCompilationReceiptIdentityIncludesDurableColumnProvenance(t *testing.T) {
	base := testReceipt()
	first, err := ReceiptID(base)
	if err != nil {
		t.Fatal(err)
	}
	base.OutputColumnProvenance["out"]["__loom_row_id"] = "DISCOVERED"
	second, err := ReceiptID(base)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("durable publication provenance did not change receipt identity")
	}
}

func TestCompilationReceiptRejectsV2V7Contract(t *testing.T) {
	receipt := testReceipt()
	receipt.ReceiptFormatVersion = 2
	receipt.CompilerContractVersion = "loom.explorer.compiler/v7"
	if err := receipt.Validate(); err == nil {
		t.Fatal("accepted obsolete v2/v7 receipt")
	}
}

func TestCompilationKeyChangesWithScopeAndCompilerContract(t *testing.T) {
	base := testReceipt()
	first, err := CompilationKey(base)
	if err != nil {
		t.Fatal(err)
	}
	base.AuthorizationScopeDigest = "sha256:other-scope"
	second, err := CompilationKey(base)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("scope did not change compilation key")
	}
	base = testReceipt()
	base.CompilerContractVersion = "loom.explorer.compiler/next"
	third, err := CompilationKey(base)
	if err != nil {
		t.Fatal(err)
	}
	if first == third {
		t.Fatal("compiler contract did not change compilation key")
	}
}

func TestCompilationKeyIncludesResolvedInputsIdentity(t *testing.T) {
	base := testReceipt()
	first, err := CompilationKey(base)
	if err != nil {
		t.Fatal(err)
	}
	base.ResolvedInputsDigest = "sha256:resolved-inputs"
	second, err := CompilationKey(base)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("resolved input identity did not change compilation key")
	}
}

func TestCompilationReceiptValidateRejectsArtifactlessLegacyReceipt(t *testing.T) {
	r := CompilationReceipt{Project: "project-a", ExplorerID: "explorer-a", ID: "receipt_legacy"}
	if err := r.Validate(); !errors.Is(err, ErrReceiptRecompileRequired) {
		t.Fatalf("error=%v, want %v", err, ErrReceiptRecompileRequired)
	}
}

func TestCompilationReceiptValidateID(t *testing.T) {
	r := testReceipt()
	var err error
	r.ID, err = ReceiptID(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	r.ID = "receipt_wrong"
	if err := r.ValidateID(); err == nil {
		t.Fatal("accepted mismatched receipt ID")
	}
}

func TestCompilationReceiptPreservesHistoricalContractReaders(t *testing.T) {
	for _, version := range []string{"loom.explorer.compiler/v10", "loom.explorer.compiler/v11", "loom.explorer.compiler/v12"} {
		t.Run(version, func(t *testing.T) {
			r := testReceipt()
			r.CompilerContractVersion = version
			r.ShapeDigest = ""
			r.CompilationKey, _ = CompilationKey(r)
			r.ID, _ = ReceiptID(r)
			if err := r.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReceiptContractSupportedForExecutionOnlyCurrentAndPrevious(t *testing.T) {
	r := testReceipt()
	if !ReceiptContractSupportedForExecution(r) {
		t.Fatal("current receipt was not executable")
	}
	r.ReceiptFormatVersion = 3
	r.CompilerContractVersion = "loom.explorer.compiler/v13"
	if !ReceiptContractSupportedForExecution(r) {
		t.Fatal("immediately previous receipt was not executable")
	}
	r.CompilerContractVersion = "loom.explorer.compiler/v12"
	if ReceiptContractSupportedForExecution(r) {
		t.Fatal("older receipt was executable")
	}
}

func TestCompilationReceiptPreservesAndAuthenticatesFrozenInterpretation(t *testing.T) {
	revision, err := PrepareInterpretationRevision(InterpretationRevision{
		Project: "project-a", LibraryID: "library-a", Author: "tester", Explanation: "frozen", CreatedAt: time.Unix(1, 0).UTC(),
		Rules: []InterpretationRule{{ID: "rule", Match: InterpretationStructuralMatch{ResourceType: "Patient"}, Definition: InterpretationFeatureDefinition{Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt := testReceipt()
	receipt.ReceiptFormatVersion = 3
	receipt.CompilerContractVersion = "loom.explorer.compiler/v13"
	receipt.ResolvedInterpretations = []ResolvedInterpretation{{OutputID: "out", Column: "value", OccurrenceID: "base", Revision: revision, SelectedRuleID: "rule", Definition: revision.Rules[0].Definition}}
	receipt.CompilationKey, err = CompilationKey(receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt.ID, err = ReceiptID(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := receipt.Validate(); err != nil {
		t.Fatalf("valid frozen historical receipt rejected: %v", err)
	}
	receipt.ResolvedInterpretations[0].Revision.Project = "project-b"
	if err := receipt.Validate(); err == nil {
		t.Fatal("cross-project frozen interpretation was accepted")
	}
	receipt.ResolvedInterpretations[0].Revision.Project = "project-a"
	receipt.ResolvedInterpretations[0].Definition.Source.Field.Path = "name.family"
	if err := receipt.Validate(); err == nil {
		t.Fatal("tampered frozen interpretation was accepted")
	}
}

func TestCompilationReceiptCurrentContractRequiresShapeDigest(t *testing.T) {
	r := testReceipt()
	r.ShapeDigest = ""
	r.CompilationKey, _ = CompilationKey(r)
	if err := r.Validate(); err == nil {
		t.Fatal("accepted a current receipt without a generation shape digest")
	}
}

func TestCompilationReceiptValidateRejectsForgedCompilationArtifacts(t *testing.T) {
	r := testReceipt()
	r.ID, _ = ReceiptID(r)
	r.CompilationKey = "compile_forged"
	if err := r.Validate(); err == nil {
		t.Fatal("accepted a forged compilation key")
	}
	r = testReceipt()
	r.PublicOutputContract = json.RawMessage(`{"outputs":[{"id":"forged"}]}`)
	r.ID, _ = ReceiptID(r)
	if err := r.Validate(); err == nil {
		t.Fatal("accepted a public output contract with a stale digest")
	}
}

func TestCompilationReceiptJSONRoundTripKeepsIdentity(t *testing.T) {
	r := testReceipt()
	first, err := ReceiptID(r)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var decoded CompilationReceipt
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	second, err := ReceiptID(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("JSON round trip changed receipt ID: %q != %q", first, second)
	}
}
