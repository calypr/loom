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
