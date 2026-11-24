package publication

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataset"
)

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

func historicalBareJoin(i BundleIdentity) string {
	parts := []string{i.Project, i.DatasetGeneration, i.Name}
	if strings.TrimSpace(i.TranslationVersion) != "" {
		parts = append(parts, i.TranslationVersion)
	}
	if strings.TrimSpace(i.OutputName) != "" {
		parts = append(parts, i.OutputName)
	}
	return strings.Join(parts, "\x00")
}

func TestPointerNameLengthPrefixDefeatsNulFold(t *testing.T) {
	base := BundleIdentity{Project: "pnn-project", DatasetGeneration: "gen-1", OutputName: "pnn_out_shared"}
	nulA := base
	nulA.Name = "a\x00b"
	foldB := base
	foldB.Name = "a"
	foldB.TranslationVersion = "b"

	if nulA.PointerName() == foldB.PointerName() {
		t.Fatalf("REGRESSION (F-B21 namespace hijack returns): Name=%q/TV=%q and Name=%q/TV=%q fold to one visibility pointer %q, matching the archived CONFIRMED state", nulA.Name, nulA.TranslationVersion, foldB.Name, foldB.TranslationVersion, nulA.PointerName())
	}
	if historicalBareJoin(nulA) != historicalBareJoin(foldB) {
		t.Fatalf("fault-control premise broken: the historical bare join must fold these identities (A=%q B=%q)", historicalBareJoin(nulA), historicalBareJoin(foldB))
	}
	if nulA.Key() == foldB.Key() {
		t.Fatalf("precondition broken: Keys must stay distinct: %q", nulA.Key())
	}
	t.Logf("FB21 control: historical bare join folded both to %q; production PointerName now separates %q vs %q", historicalBareJoin(nulA), nulA.PointerName(), foldB.PointerName())
}

func TestPointerNameIsInjectiveOverDistinctComponents(t *testing.T) {
	seen := map[string]string{}
	entries := []struct {
		label string
		id    BundleIdentity
	}{
		{"nulInName", BundleIdentity{Project: "p", DatasetGeneration: "g", Name: "a\x00b", OutputName: "out"}},
		{"namePlusTV", BundleIdentity{Project: "p", DatasetGeneration: "g", Name: "a", TranslationVersion: "b", OutputName: "out"}},
		{"nulDeepName", BundleIdentity{Project: "p", DatasetGeneration: "g", Name: "a\x00b\x00c", OutputName: "out"}},
		{"nulInTV", BundleIdentity{Project: "p", DatasetGeneration: "g", Name: "a", TranslationVersion: "b\x00c", OutputName: "out"}},
		{"colonDigitsName", BundleIdentity{Project: "p", DatasetGeneration: "g", Name: "1:ab", OutputName: "out"}},
		{"nulColonName", BundleIdentity{Project: "p", DatasetGeneration: "g", Name: "\x001:ab", OutputName: "out"}},
		{"nulSplitTVvsOut", BundleIdentity{Project: "p", DatasetGeneration: "g", Name: "n", TranslationVersion: "a\x00b", OutputName: "out"}},
		{"nulSplitOut", BundleIdentity{Project: "p", DatasetGeneration: "g", Name: "n", TranslationVersion: "a", OutputName: "\x00b"}},
	}
	for _, entry := range entries {
		name := entry.id.PointerName()
		if prior, ok := seen[name]; ok {
			t.Fatalf("REGRESSION (F-B21 fold): %s and %s share pointer name %q under the length-prefixed encoding", prior, entry.label, name)
		}
		seen[name] = entry.label
	}
}

func TestPointerNameOptionalComponentSemanticsAreShipPreserved(t *testing.T) {
	base := BundleIdentity{Project: "proj", DatasetGeneration: "gen", Name: "orders"}
	withTV := func(tv, out string) BundleIdentity {
		i := base
		i.TranslationVersion = tv
		i.OutputName = out
		return i
	}
	if withTV("   ", "main").PointerName() != withTV("", "main").PointerName() {
		t.Fatalf("whitespace-only TranslationVersion must stay absent exactly as shipped: %q vs %q", withTV("   ", "main").PointerName(), withTV("", "main").PointerName())
	}
	if withTV("v1", " \t").PointerName() != withTV("v1", "").PointerName() {
		t.Fatalf("whitespace-only OutputName must stay absent exactly as shipped")
	}
	if withTV("v1 ", "main").PointerName() == withTV("v1", "main").PointerName() {
		t.Fatalf("trailing-space TranslationVersion was collapsed; the shipped encoding keeps the raw value inside a live component")
	}

	left := withTV("x", "")
	right := withTV("", "x")
	if left.PointerName() != right.PointerName() || historicalBareJoin(left) != historicalBareJoin(right) {
		t.Fatalf("shipped blank-component exclusion semantics changed: prod %q/%q historical %q/%q", left.PointerName(), right.PointerName(), historicalBareJoin(left), historicalBareJoin(right))
	}
}

type exactExecutionFixture struct {
	project, generation string
	selector            dataset.DataframeSelector
	execution           BundleExecution
	output              BundleOutputRecord
}

func (f *exactExecutionFixture) FindExecutionBySelector(_ context.Context, project, generation string, selector DataframeSelector) (BundleExecution, BundleOutputRecord, error) {
	f.project, f.generation, f.selector = project, generation, selector
	return f.execution, f.output, nil
}

func TestPublicationVerificationUsesExactSelectorAndQueryableEvidence(t *testing.T) {
	verifiedAt := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	selector := dataset.DataframeSelector{Recipe: "core", TranslationVersion: "v1", Output: "Patient"}
	exact := &exactExecutionFixture{
		execution: BundleExecution{ID: "execution-a", BundleIdentity: BundleIdentity{DatasetGeneration: "commit-a"}, State: "READY"},
		output:    BundleOutputRecord{Selector: selector, Name: "Patient", State: "READY", VerifiedAt: &verifiedAt, PhysicalTable: "patient_a"},
	}
	result, err := (publicationVerificationStore{executions: exact}).VerifyPublication(context.Background(), "project-a", "commit-a", selector)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "PUBLISHED" || !result.Queryable || result.ExecutionID != "execution-a" || result.Selector != selector {
		t.Fatalf("verification = %#v", result)
	}
	if exact.project != "project-a" || exact.generation != "commit-a" || exact.selector != selector {
		t.Fatalf("selector lookup = %q %q %#v", exact.project, exact.generation, exact.selector)
	}
}
