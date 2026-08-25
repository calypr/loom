package explorer

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/recipe"
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
		SourceGeneration:         "generation-a",
		RecipeDigest:             "sha256:recipe",
		ResolvedSchemaDigest:     "sha256:resolved-schema",
		NormalizedBundle:         json.RawMessage(`{"documents":[]}`),
		Bundle:                   recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "receipt-test", TranslationVersion: "test", Outputs: []recipe.Output{{Name: "out", RootResourceType: "Patient", RowGrain: "patient"}}},
		CompiledConfig:           json.RawMessage(`{"views":[]}`),
		PublicOutputContract:     json.RawMessage(`{"outputId":"out","columns":[]}`),
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

func TestCompilationReceiptValidateRejectsArtifactlessLegacyReceipt(t *testing.T) {
	r := CompilationReceipt{Project: "project-a", ExplorerID: "explorer-a", ID: "receipt_legacy"}
	if err := r.Validate(); !errors.Is(err, ErrReceiptRecompileRequired) {
		t.Fatalf("error=%v, want %v", err, ErrReceiptRecompileRequired)
	}
}

func TestCompilationReceiptValidateIDAndDeepClone(t *testing.T) {
	r := testReceipt()
	var err error
	r.ID, err = ReceiptID(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	clone, err := CloneCompilationReceipt(r)
	if err != nil {
		t.Fatal(err)
	}
	clone.OutputFingerprints = map[string]string{"out": "fingerprint"}
	clone.Warnings[0].Details = map[string]any{"source": "test"}
	if r.OutputFingerprints != nil || r.Warnings[0].Details != nil {
		t.Fatal("clone mutation changed original receipt")
	}
	clone.ID = "receipt_wrong"
	if err := clone.ValidateID(); err == nil {
		t.Fatal("accepted mismatched receipt ID")
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
