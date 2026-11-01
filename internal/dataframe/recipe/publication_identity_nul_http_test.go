package recipe

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/publication"
)

// F-B21-http: does an HTTP-authoring document (transport = encoding/json on
// the production Bundle type, the shape the Explorer compiler receives) carry
// NUL bytes through the production validation gate and collide two distinct
// recipes' visibility pointers? Red while the documented-promise boundary is
// open (document_validation.go:36-41 checks only non-emptiness for $.name and
// $.translationVersion while output/column names pass validateRecipeName's
// ^[A-Za-z_][A-Za-z0-9_]*$ regex). This probe retires itself in the correct
// direction: the day validateRecipeName (or a control-char gate) also gates
// $.name/$.translationVersion, the acceptance assertions below flip and this
// artifact must be re-pointed at the new gate.
//
// Scope honesty: no live HTTP server is driven here (GraphQL/OpenAPI handlers
// decode JSON via encoding/json, so transport fidelity is asserted at the
// decoder the handler itself uses). Live Arango and ClickHouse acceptance of
// NUL-bearing names is already parent-verified by
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

	errA := a.Validate()
	errB := b.Validate()
	if errA == nil && errB == nil {
		t.Errorf("FB21-http RED-ON-PURPOSE | production Bundle.Validate accepted control characters in pointer-deciding components: $.name=%q accepted, $.translationVersion=%q accepted while every output/column name is regex-gated by validateRecipeName", a.Name, b.TranslationVersion)
	} else {
		t.Logf("FB21-http boundary closed: Validate rejected A=%v B=%v — gate now covers pointer components; retarget this artifact", errA, errB)
	}

	// Production wiring (server/recipe_publication.go publishResolvedRecipe):
	// identity Name/TranslationVersion come straight from the bundle; OutputName
	// is the (regex-validated, shared) output name.
	ia := identityOf(a, "patients")
	ib := identityOf(b, "patients")

	pa, pb := ia.PointerName(), ib.PointerName()
	ka, kb := ia.Key(), ib.Key()

	if pa != pb {
		t.Fatalf("FB21-http probe lost its premise — pointer collision did not occur: A=%q B=%q", pa, pb)
	}
	if a.Name == b.Name || a.TranslationVersion == b.TranslationVersion {
		t.Fatalf("documents were not distinct: A=%q/%q B=%q/%q", a.Name, a.TranslationVersion, b.Name, b.TranslationVersion)
	}
	if ka == kb {
		t.Fatalf("Key() also collided; distinctness evidence lost: %q", ka)
	}
	t.Errorf("FB21-http CONFIRMED-reachability | two HTTP-decodable distinct author documents fold to ONE visibility pointer: PointerName A==B==%q while Key A!=Key B (%q vs %q); author-facing names differ (%q vs %q) and TVs differ (%q vs %q); publishing B silently repoints A's readers, exactly as parent-verified in the clickhouse e2e",
		pa, ka, kb, a.Name, b.Name, a.TranslationVersion, b.TranslationVersion)
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
