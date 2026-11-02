package publication

import (
	"strings"
	"testing"
)

// historicalBareJoin replays the pre-repair PointerName EXACTLY as shipped:
// conditional optional components joined with a bare "\x00". It pins the
// fault premise (the archived CONFIRMED pairs fold HERE) while production
// PointerName must separate them (Repair-5).
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

// TestPointerNameIsInjectiveOverDistinctComponents drives the archived
// fold-class pairs plus delimiter-spoof neighbours: every DISTINCT
// (component, value) assignment must yield a distinct pointer name.
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

// TestPointerNameOptionalComponentSemanticsAreShipPreserved pins the shipped
// exclusion behaviour (whitespace-only == absent) that the Repair-5 encoder
// must not alter.
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
	// Documented boundary equality (NOT a fold-class defect): blank-valued
	// optional components are EXCLUDED, so (TV="x", Out blank) and
	// (TV blank, Out="x") occupy one 4-component namespace, matching the
	// pre-repair bare join. That boundary cannot steal a pointer: a document
	// publish REQUIRES a non-blank TranslationVersion
	// (internal/dataframe/recipe/document_validation.go:39), so the blank-TV /
	// Output-only twin of a blank-Output identity is never publishable; a
	// blank-Output identity only arises from a multi-output publication —
	// incrementalPublicationOutput returns "" unless there is exactly one
	// binding and one stream
	// (internal/server/recipe_publication.go:214-218) — and its 4 components
	// never collide with a 5-component single-output name; Key() still
	// distinguishes the executions themselves.
	left := withTV("x", "")
	right := withTV("", "x")
	if left.PointerName() != right.PointerName() || historicalBareJoin(left) != historicalBareJoin(right) {
		t.Fatalf("shipped blank-component exclusion semantics changed: prod %q/%q historical %q/%q", left.PointerName(), right.PointerName(), historicalBareJoin(left), historicalBareJoin(right))
	}
}
