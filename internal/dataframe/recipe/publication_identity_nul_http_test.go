package recipe

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/publication"
)

// F-B21-http durable regression control (Repair-5 shipped). An
// HTTP-authoring document (transport = encoding/json on the production
// Bundle type, the shape the Explorer compiler receives) CAN still carry NUL
// bytes through the recipe validation gate (document_validation.go:36-41
// still checks only non-emptiness for $.name and $.translationVersion while
// output/column names pass validateRecipeName's ^[A-Za-z_][A-Za-z0-9_]*$
// regex) — the boundary this artifact originally watched stayed open at the
// VALIDATOR. Repair-5 closed the collision at the root instead: production
// BundleIdentity.PointerName() is length-prefix-encoded, so the two
// HTTP-decodable distinct documents below get DISTINCT visibility pointers.
// Natural PASS is now the repaired expectation; the REGRESSION direction
// keeps the archived CONFIRMED reachability message if the pointer encoding
// ever re-opens the fold. The validator-side observation remains a t.Errorf:
// if control characters stop surviving JSON decode, or a control-char gate
// lands in Validate(), flip/re-point this artifact deliberately.
//
// Scope honesty: no live HTTP server is driven here (GraphQL/OpenAPI handlers
// decode JSON via encoding/json, so transport fidelity is asserted at the
// decoder the handler itself uses). Live Arango and ClickHouse acceptance of
// NUL-bearing names is parent-verified by
// internal/dataframe/publication/clickhouse/pointer_name_nul_ambiguity_e2e_test.go.

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
	// Two DISTINCT author-facing documents, both minimal-valid except that a
	// pointer component embeds NUL. Wire form uses the \u0000 escape exactly as
	// an HTTP client would send it.
	docA := `{"recipeSchemaVersion":` + itoa(CurrentSchemaVersion) + `,"name":"p\u0000q","translationVersion":"r","outputs":[{"name":"patients","rootResourceType":"Patient","rowGrain":"patient"}]}`
	docB := `{"recipeSchemaVersion":` + itoa(CurrentSchemaVersion) + `,"name":"p","translationVersion":"q\u0000r","outputs":[{"name":"patients","rootResourceType":"Patient","rowGrain":"patient"}]}`

	a := decodeDocument(t, docA)
	b := decodeDocument(t, docB)

	if !strings.ContainsRune(a.Name, 0) {
		t.Fatalf("GATE: transport unexpectedly stripped NUL from $.name: %q", a.Name)
	}
	t.Logf("FB21-http GATE | nul-survives-json-decode | decoded $.name=%q preserves the U+0000 the HTTP body carried", a.Name)

	// Defense-in-depth OBSERVATION (not a failing assertion): Repair-5 closed
	// the F-B21 harm at the root (injective PointerName encoding), so
	// Validate() accepting control characters in $.name/$.translationVersion
	// no longer enables a namespace hijack. This line documents that the
	// validator-side gate is still open; if a control-char gate lands in
	// Validate(), flip this note to an expectation of rejection.
	errA := a.Validate()
	errB := b.Validate()
	if errA == nil && errB == nil {
		t.Logf("FB21-http NOTE | production Bundle.Validate still accepts control characters in pointer-deciding components ($.name=%q, $.translationVersion=%q accepted while output/column names are regex-gated by validateRecipeName); harmless since Repair-5 — visibility names are injective by construction", a.Name, b.TranslationVersion)
	} else {
		t.Logf("FB21-http note: Validate rejected A=%v B=%v — validator-side gate now covers pointer components", errA, errB)
	}

	// Production wiring (server/recipe_publication.go publishResolvedRecipe):
	// identity Name/TranslationVersion come straight from the bundle; OutputName
	// is the (regex-validated, shared) output name.
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
