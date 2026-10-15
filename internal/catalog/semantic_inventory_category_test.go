package catalog

import "testing"

func TestSemanticInventoryFamilyIdentityPreservesExactScope(t *testing.T) {
	base := SemanticInventoryFamilyReference{
		BindingID: "binding", SourceKind: SemanticInventorySourceRetained,
		ResourceType: "Specimen", SourcePath: "type.coding[]", System: "urn:specimen", Version: "",
	}
	want := SemanticInventoryFamilyID(base)
	for name, change := range map[string]func(*SemanticInventoryFamilyReference){
		"version":  func(ref *SemanticInventoryFamilyReference) { ref.Version = "v1" },
		"system":   func(ref *SemanticInventoryFamilyReference) { ref.System = "urn:other" },
		"path":     func(ref *SemanticInventoryFamilyReference) { ref.SourcePath = "category.coding[]" },
		"resource": func(ref *SemanticInventoryFamilyReference) { ref.ResourceType = "Condition" },
		"binding":  func(ref *SemanticInventoryFamilyReference) { ref.BindingID = "other" },
		"source":   func(ref *SemanticInventoryFamilyReference) { ref.SourceKind = SemanticInventorySourceFile },
	} {
		t.Run(name, func(t *testing.T) {
			changed := base
			change(&changed)
			if SemanticInventoryFamilyID(changed) == want {
				t.Fatal("different family scope reused the same identity")
			}
		})
	}
	base.FamilyID = "caller-supplied"
	if SemanticInventoryFamilyID(base) != want {
		t.Fatal("family identity depended on a caller-supplied identity")
	}
}

func TestSemanticInventoryCursorSeparatesCategoryAndExactSelections(t *testing.T) {
	opts := SemanticInventoryPageOptions{
		Project: "project", DatasetGeneration: "generation", Limit: 1,
		AuthResourcePathsUnrestricted: ExplicitAuthResourcePathsUnrestricted(false),
		AuthResourcePaths:             []string{"scope-a"},
	}
	const legacyDigest = "d5446b0c580976d96512098f83c73d6ea412699551538c73bf0f0259da6bd965"
	if got := SemanticInventoryPageDigest(opts, "build", 1); got != legacyDigest {
		t.Fatalf("legacy exact cursor digest changed: %q", got)
	}
	legacy := EncodeSemanticInventoryCursor(opts, "build", "binding", "concept")
	opts.SelectionMode = SemanticInventoryExactConcept
	if _, _, err := DecodeSemanticInventoryCursor(legacy, opts, "build"); err != nil {
		t.Fatalf("explicit exact mode rejected legacy cursor: %v", err)
	}
	opts.SelectionMode = SemanticInventorySystemVersionCategory
	if _, _, err := DecodeSemanticInventoryCursor(legacy, opts, "build"); err != ErrSemanticInventoryCursorMismatch {
		t.Fatalf("category request reused exact cursor: %v", err)
	}
	category := EncodeSemanticInventoryCursor(opts, "build", "binding", `family:["Specimen","type.coding[]","urn:specimen",""]`)
	for name, change := range map[string]func(*SemanticInventoryPageOptions){
		"mode":       func(o *SemanticInventoryPageOptions) { o.SelectionMode = SemanticInventoryExactConcept },
		"scope":      func(o *SemanticInventoryPageOptions) { o.AuthResourcePaths = []string{"scope-b"} },
		"resource":   func(o *SemanticInventoryPageOptions) { o.ResourceType = "Condition" },
		"query":      func(o *SemanticInventoryPageOptions) { o.Query = "Tumor" },
		"generation": func(o *SemanticInventoryPageOptions) { o.DatasetGeneration = "another" },
		"project":    func(o *SemanticInventoryPageOptions) { o.Project = "another" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := opts
			change(&changed)
			if _, _, err := DecodeSemanticInventoryCursor(category, changed, "build"); err != ErrSemanticInventoryCursorMismatch {
				t.Fatalf("changed request accepted category cursor: %v", err)
			}
		})
	}
	if _, _, err := DecodeSemanticInventoryCursor(category, opts, "other-build"); err != ErrSemanticInventoryCursorMismatch {
		t.Fatalf("changed build accepted category cursor: %v", err)
	}
}
