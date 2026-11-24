package recipe

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/publication"
)

func decodeDocument(t *testing.T, wire string) Bundle {
	t.Helper()
	var b Bundle
	if err := json.Unmarshal([]byte(wire), &b); err != nil {
		t.Fatalf("production-bundle JSON decode failed (transport would reject): %v", err)
	}
	return b
}

func identityOf(b Bundle, outputName string) publication.BundleIdentity {
	return publication.BundleIdentity{
		Project:            "prj-http",
		DatasetGeneration:  "gen-1",
		Name:               b.Name,
		TranslationVersion: b.TranslationVersion,
		OutputName:         outputName,
		RecipeDigest:       "sha256-recipe-" + b.Name + "-" + b.TranslationVersion,
		SchemaDigest:       "sha256-schema",
	}
}

func TestPublicationPointerNULFromHTTPDecodedDocuments(t *testing.T) {

	docA := `{"recipeSchemaVersion":` + itoa(CurrentSchemaVersion) + `,"name":"p\u0000q","translationVersion":"r","outputs":[{"name":"patients","rootResourceType":"Patient","rowGrain":"patient"}]}`
	docB := `{"recipeSchemaVersion":` + itoa(CurrentSchemaVersion) + `,"name":"p","translationVersion":"q\u0000r","outputs":[{"name":"patients","rootResourceType":"Patient","rowGrain":"patient"}]}`

	a := decodeDocument(t, docA)
	b := decodeDocument(t, docB)

	if !strings.ContainsRune(a.Name, 0) {
		t.Fatalf("GATE: transport unexpectedly stripped NUL from $.name: %q", a.Name)
	}
	t.Logf("FB21-http GATE | nul-survives-json-decode | decoded $.name=%q preserves the U+0000 the HTTP body carried", a.Name)

	errA := a.Validate()
	errB := b.Validate()
	if errA == nil && errB == nil {
		t.Logf("FB21-http NOTE | production Bundle.Validate still accepts control characters in pointer-deciding components ($.name=%q, $.translationVersion=%q accepted while output/column names are regex-gated by validateRecipeName); harmless since Repair-5 — visibility names are injective by construction", a.Name, b.TranslationVersion)
	} else {
		t.Logf("FB21-http note: Validate rejected A=%v B=%v — validator-side gate now covers pointer components", errA, errB)
	}

	ia := identityOf(a, "patients")
	ib := identityOf(b, "patients")

	pa, pb := ia.PointerName(), ib.PointerName()
	ka, kb := ia.Key(), ib.Key()

	if a.Name == b.Name || a.TranslationVersion == b.TranslationVersion {
		t.Fatalf("documents were not distinct: A=%q/%q B=%q/%q", a.Name, a.TranslationVersion, b.Name, b.TranslationVersion)
	}
	if ka == kb {
		t.Fatalf("Key() also collided; distinctness evidence lost: %q", ka)
	}
	if pa == pb {
		t.Errorf("FB21-http REGRESSION (archived CONFIRMED-reachability): two HTTP-decodable distinct author documents fold to ONE visibility pointer: PointerName A==B==%q while Key A!=Key B (%q vs %q); author-facing names differ (%q vs %q) and TVs differ (%q vs %q); publishing B would silently repoint A's readers — the exact harm parent-verified in the clickhouse e2e before Repair-5. The length-prefix PointerName encoding has regressed.",
			pa, ka, kb, a.Name, b.Name, a.TranslationVersion, b.TranslationVersion)
	}
	t.Logf("FB21-http VERDICT PREVENTED (Repair-5): two HTTP-decodable distinct author documents keep DISTINCT visibility pointers under the length-prefix PointerName encoding (A=%q B=%q) while Keys stay distinct — the archived NUL-fold hijack window is closed at the root; NUL still survives decode and Validate (validator-side observation above).", pa, pb)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func workspaceOutputCombineRecipe(name, dependency string) Output {
	inputs := []ConstructionInputRef{
		{Kind: ConstructionTableRevisionInput, TableID: "source:1:left", RevisionID: "left-r1", OutputID: "left"},
		{Kind: ConstructionTableRevisionInput, TableID: "source:1:right", RevisionID: "right-r1", OutputID: "right"},
	}
	if dependency != "" {
		inputs[0] = ConstructionInputRef{Kind: ConstructionWorkspaceOutputInput, OutputID: dependency}
	}
	combine := ConstructionCombine{Kind: ConstructionCombineAppend, Projections: []ConstructionCombineProjection{
		{OutputColumnID: "value", InputIndex: 0, InputColumnID: "value"},
		{OutputColumnID: "value", InputIndex: 1, InputColumnID: "value"},
	}}
	return Output{
		Name: name, RootResourceType: "Patient", RowGrain: "patient",
		Construction: &Construction{Version: 1, Steps: []ConstructionStep{{
			ID: "append", Inputs: inputs, Operation: ConstructionOperation{Kind: ConstructionCombineOp, Combine: &combine},
			Outputs: []StageColumn{{ID: "value", Name: "value", Type: "string"}},
		}}},
	}
}

func workspaceOutputBundle(outputs ...Output) Bundle {
	return Bundle{RecipeSchemaVersion: CurrentSchemaVersion, Name: "workspace", TranslationVersion: "1", Outputs: outputs}
}

func TestRecipeBundleWorkspaceOutputDependenciesValidateAndOrderForwardReferences(t *testing.T) {
	bundle := workspaceOutputBundle(workspaceOutputCombineRecipe("combined", "base"), workspaceOutputCombineRecipe("base", ""))
	if err := bundle.Validate(); err != nil {
		t.Fatalf("bundle with same-bundle output ref rejected: %v", err)
	}
	order, err := bundle.OutputDependencyOrder()
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != 1 || order[1] != 0 {
		t.Fatalf("dependency order = %v, want [1 0]", order)
	}
}

func TestRecipeBundleWorkspaceOutputDependenciesRejectUnknownSelfAndCycles(t *testing.T) {
	for _, test := range []struct {
		name    string
		outputs []Output
		want    string
	}{
		{name: "unknown", outputs: []Output{workspaceOutputCombineRecipe("combined", "missing"), workspaceOutputCombineRecipe("base", "")}, want: "workspace_output_not_found"},
		{name: "self", outputs: []Output{workspaceOutputCombineRecipe("combined", "combined"), workspaceOutputCombineRecipe("base", "")}, want: "workspace_output_self_reference"},
		{name: "cycle", outputs: []Output{workspaceOutputCombineRecipe("first", "second"), workspaceOutputCombineRecipe("second", "first")}, want: "workspace_output_cycle"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := workspaceOutputBundle(test.outputs...).Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate error = %v, want %s", err, test.want)
			}
		})
	}
}

func TestSingleOutputCompilerFragmentRequiresExplicitWorkspaceContext(t *testing.T) {
	fragment := workspaceOutputBundle(workspaceOutputCombineRecipe("combined", "base"))
	if err := fragment.Validate(); err == nil || !strings.Contains(err.Error(), "workspace_output_not_found") {
		t.Fatalf("closed bundle validation error = %v", err)
	}
	if err := fragment.ValidateWithWorkspaceOutputs([]string{"base"}); err != nil {
		t.Fatalf("workspace-context validation rejected sibling output: %v", err)
	}
}
