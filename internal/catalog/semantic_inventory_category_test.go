package catalog

import (
	"testing"

	"github.com/calypr/loom/internal/catalog/semanticinventory"
)

func TestSemanticInventoryFamilyIdentityPreservesExactScope(t *testing.T) {
	base := semanticinventory.FamilyReference{
		BindingID: "binding", SourceKind: semanticinventory.SourceRetained,
		ResourceType: "Specimen", SourcePath: "type.coding[]", System: "urn:specimen", Version: "",
	}
	want := semanticinventory.FamilyID(base)
	for name, change := range map[string]func(*semanticinventory.FamilyReference){
		"version":  func(ref *semanticinventory.FamilyReference) { ref.Version = "v1" },
		"system":   func(ref *semanticinventory.FamilyReference) { ref.System = "urn:other" },
		"path":     func(ref *semanticinventory.FamilyReference) { ref.SourcePath = "category.coding[]" },
		"resource": func(ref *semanticinventory.FamilyReference) { ref.ResourceType = "Condition" },
		"binding":  func(ref *semanticinventory.FamilyReference) { ref.BindingID = "other" },
		"source":   func(ref *semanticinventory.FamilyReference) { ref.SourceKind = semanticinventory.SourceFile },
	} {
		t.Run(name, func(t *testing.T) {
			changed := base
			change(&changed)
			if semanticinventory.FamilyID(changed) == want {
				t.Fatal("different family scope reused the same identity")
			}
		})
	}
	base.FamilyID = "caller-supplied"
	if semanticinventory.FamilyID(base) != want {
		t.Fatal("family identity depended on a caller-supplied identity")
	}
}

func TestSemanticInventoryCursorSeparatesCategoryAndExactSelections(t *testing.T) {
	opts := semanticinventory.PageOptions{
		Project: "project", DatasetGeneration: "generation", Limit: 1,
		AuthResourcePathsUnrestricted: ExplicitAuthResourcePathsUnrestricted(false),
		AuthResourcePaths:             []string{"scope-a"},
	}
	const legacyDigest = "d5446b0c580976d96512098f83c73d6ea412699551538c73bf0f0259da6bd965"
	if got := semanticinventory.PageDigest(opts, "build", 1); got != legacyDigest {
		t.Fatalf("legacy exact cursor digest changed: %q", got)
	}
	legacy := semanticinventory.EncodeCursor(opts, "build", "binding", "concept")
	opts.SelectionMode = semanticinventory.ExactConcept
	if _, _, err := semanticinventory.DecodeCursor(legacy, opts, "build"); err != nil {
		t.Fatalf("explicit exact mode rejected legacy cursor: %v", err)
	}
	opts.SelectionMode = semanticinventory.SystemVersionCategory
	if _, _, err := semanticinventory.DecodeCursor(legacy, opts, "build"); err != semanticinventory.ErrSemanticInventoryCursorMismatch {
		t.Fatalf("category request reused exact cursor: %v", err)
	}
	category := semanticinventory.EncodeCursor(opts, "build", "binding", `family:["Specimen","type.coding[]","urn:specimen",""]`)
	for name, change := range map[string]func(*semanticinventory.PageOptions){
		"mode":       func(o *semanticinventory.PageOptions) { o.SelectionMode = semanticinventory.ExactConcept },
		"scope":      func(o *semanticinventory.PageOptions) { o.AuthResourcePaths = []string{"scope-b"} },
		"resource":   func(o *semanticinventory.PageOptions) { o.ResourceType = "Condition" },
		"query":      func(o *semanticinventory.PageOptions) { o.Query = "Tumor" },
		"generation": func(o *semanticinventory.PageOptions) { o.DatasetGeneration = "another" },
		"project":    func(o *semanticinventory.PageOptions) { o.Project = "another" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := opts
			change(&changed)
			if _, _, err := semanticinventory.DecodeCursor(category, changed, "build"); err != semanticinventory.ErrSemanticInventoryCursorMismatch {
				t.Fatalf("changed request accepted category cursor: %v", err)
			}
		})
	}
	if _, _, err := semanticinventory.DecodeCursor(category, opts, "other-build"); err != semanticinventory.ErrSemanticInventoryCursorMismatch {
		t.Fatalf("changed build accepted category cursor: %v", err)
	}
}
