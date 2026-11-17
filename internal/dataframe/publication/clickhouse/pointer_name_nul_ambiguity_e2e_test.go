package clickhouse

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/publication"
)

func TestPointerNameNulAmbiguityHijacksVisibilityPointerAgainstArango(t *testing.T) {
	common := publication.BundleIdentity{
		Project: "project", DatasetGeneration: "generation", OutputName: "output",
		RecipeDigest: "recipe-digest", SchemaDigest: "schema-digest",
		ScopeDigest: "scope-digest", EngineVersion: "engine-version",
	}
	first := common
	first.Name = "a\x00b"
	second := common
	second.Name = "a"
	second.TranslationVersion = "b"

	if first.Key() == second.Key() {
		t.Fatalf("distinct bundle identities produced the same key %q", first.Key())
	}
	if first.PointerName() == second.PointerName() {
		t.Fatalf("distinct bundle identities produced the same visibility pointer %q", first.PointerName())
	}
}
