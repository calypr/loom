package publication

import "testing"

func TestBundleIdentityCanonicalizesAuthorizationPathSet(t *testing.T) {
	first := BundleIdentity{
		Name:              "recipe",
		Project:           "project",
		DatasetGeneration: "generation",
		AuthResourcePaths: []string{"/programs/b", "/programs/a", "/programs/a"},
	}
	second := first
	second.AuthResourcePaths = []string{"/programs/a", "/programs/b"}

	if first.Key() != second.Key() {
		t.Fatalf("equivalent authorization path sets have different keys: %q != %q", first.Key(), second.Key())
	}
	canonical := first.Canonical()
	want := []string{"/programs/a", "/programs/b"}
	if len(canonical.AuthResourcePaths) != len(want) || canonical.AuthResourcePaths[0] != want[0] || canonical.AuthResourcePaths[1] != want[1] {
		t.Fatalf("canonical authorization paths = %#v, want %#v", canonical.AuthResourcePaths, want)
	}
}

func TestFinalSchemaDigestPreservesColumnOrder(t *testing.T) {
	identity := PublicationIdentity{Name: "recipe", RecipeDigest: "recipe", ScopeDigest: "scope", DatasetGeneration: "generation"}
	first := []OutputSchema{{Name: "output", Columns: []LogicalColumn{{Name: "id", Kind: "string"}, {Name: "status", Kind: "string"}}}}
	second := []OutputSchema{{Name: "output", Columns: []LogicalColumn{{Name: "status", Kind: "string"}, {Name: "id", Kind: "string"}}}}

	if FinalSchemaDigest(identity, first) == FinalSchemaDigest(identity, second) {
		t.Fatal("schema digest treats ordered columns as equivalent")
	}
}

func TestBundleIdentityPointerNameIsInjective(t *testing.T) {
	base := BundleIdentity{Project: "proj", DatasetGeneration: "gen", Name: "a", OutputName: "out"}
	pairA := base
	pairB := base
	pairA.Name = "a\x00b"
	pairA.TranslationVersion = ""
	pairB.Name = "a"
	pairB.TranslationVersion = "b"
	if pairA.PointerName() == pairB.PointerName() {
		t.Fatalf("REGRESSION (F-B21 fold returned): PointerName(%q/%q)==PointerName(%q/%q)==%q",
			pairA.Name, pairA.TranslationVersion, pairB.Name, pairB.TranslationVersion, pairA.PointerName())
	}
	if pairA.Key() == pairB.Key() {
		t.Fatalf("precondition: Keys must stay distinct: %q", pairA.Key())
	}

	tvsPoisoned := base
	tvsPoisoned.Name = "ok"
	tvsPoisoned.TranslationVersion = "a\x00b"
	tvsPlain := base
	tvsPlain.Name = "ok"
	tvsPlain.TranslationVersion = "a"
	tvsPlain.OutputName = "\x00b"
	if tvsPoisoned.PointerName() == tvsPlain.PointerName() {
		t.Fatalf("embedded NUL shifted the TranslationVersion/OutputName boundary: %q", tvsPoisoned.PointerName())
	}

	digitSpoof := base
	digitSpoof.Name = "2:a"
	digitSplit := base
	digitSplit.Name = "2"
	digitSplit.TranslationVersion = "a"
	if digitSpoof.PointerName() == digitSplit.PointerName() {
		t.Fatalf("digit/colon spoof folded components: %q", digitSpoof.PointerName())
	}

	multibyte := base
	multibyte.Name = "日"
	if got, want := multibyte.PointerName(), "4:proj3:gen3:日3:out"; got != want {
		t.Fatalf("byte-length prefix = %q, want %q", got, want)
	}

	blankTV := base
	blankTV.TranslationVersion = "   "
	missingTV := base
	missingTV.TranslationVersion = ""
	if blankTV.PointerName() != missingTV.PointerName() {
		t.Fatalf("whitespace-only TranslationVersion must remain absent like the empty string: %q vs %q", blankTV.PointerName(), missingTV.PointerName())
	}
	blankOutput := base
	blankOutput.OutputName = " \t"
	if blankOutput.PointerName() == base.PointerName() {
		t.Fatalf("whitespace-only OutputName no longer excluded (shipped semantics changed): %q", blankOutput.PointerName())
	}
}
