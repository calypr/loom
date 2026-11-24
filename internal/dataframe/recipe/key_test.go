package recipe

import "testing"

func TestExtensionColumnOutputKeyUsesRawAncestorAndStructuralIdentity(t *testing.T) {
	base := ExtensionColumnMapping{
		Name: "parent__value", URL: "https://example.org/value",
		URLPath: []string{"https://example.org/parent"}, SourcePath: "component[].extension[]",
		ValuePath: "valueString", ValueType: "string",
	}
	key := ExtensionColumnOutputKey(base)
	if !ValidExtensionColumnOutputKey(key) {
		t.Fatalf("invalid generated key %q", key)
	}
	valueEvolution := base
	valueEvolution.ValuePath = "valueCode"
	valueEvolution.ValueType = "code"
	valueEvolution.Name = "renamed_column"
	if got := ExtensionColumnOutputKey(valueEvolution); got != key {
		t.Fatalf("value/name evolution changed structural identity: %q != %q", got, key)
	}
	differentAncestor := base
	differentAncestor.URLPath = []string{"https://example.org/other-parent"}
	if got := ExtensionColumnOutputKey(differentAncestor); got == key {
		t.Fatal("different raw ancestor URL paths collided")
	}
	differentSource := base
	differentSource.SourcePath = "modifierExtension[].extension[]"
	if got := ExtensionColumnOutputKey(differentSource); got == key {
		t.Fatal("different structural source paths collided")
	}
}
