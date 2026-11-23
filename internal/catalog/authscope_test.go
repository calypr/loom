package catalog

import (
	"testing"

	"github.com/calypr/loom/internal/semantic"
)

func TestSemanticInventoryFamilyIdentityPreservesExactScope(t *testing.T) {
	base := semantic.FamilyReference{
		BindingID: "binding", SourceKind: semantic.SourceRetained,
		ResourceType: "Specimen", SourcePath: "type.coding[]", System: "urn:specimen", Version: "",
	}
	want := semantic.FamilyID(base)
	for name, change := range map[string]func(*semantic.FamilyReference){
		"version":  func(ref *semantic.FamilyReference) { ref.Version = "v1" },
		"system":   func(ref *semantic.FamilyReference) { ref.System = "urn:other" },
		"path":     func(ref *semantic.FamilyReference) { ref.SourcePath = "category.coding[]" },
		"resource": func(ref *semantic.FamilyReference) { ref.ResourceType = "Condition" },
		"binding":  func(ref *semantic.FamilyReference) { ref.BindingID = "other" },
		"source":   func(ref *semantic.FamilyReference) { ref.SourceKind = semantic.SourceFile },
	} {
		t.Run(name, func(t *testing.T) {
			changed := base
			change(&changed)
			if semantic.FamilyID(changed) == want {
				t.Fatal("different family scope reused the same identity")
			}
		})
	}
	base.FamilyID = "caller-supplied"
	if semantic.FamilyID(base) != want {
		t.Fatal("family identity depended on a caller-supplied identity")
	}
}

func TestSemanticInventoryCursorSeparatesCategoryAndExactSelections(t *testing.T) {
	opts := semantic.PageOptions{
		Project: "project", DatasetGeneration: "generation", Limit: 1,
		AuthResourcePathsUnrestricted: ExplicitAuthResourcePathsUnrestricted(false),
		AuthResourcePaths:             []string{"scope-a"},
	}
	const legacyDigest = "d5446b0c580976d96512098f83c73d6ea412699551538c73bf0f0259da6bd965"
	if got := semantic.PageDigest(opts, "build", 1); got != legacyDigest {
		t.Fatalf("legacy exact cursor digest changed: %q", got)
	}
	legacy := semantic.EncodeCursor(opts, "build", "binding", "concept")
	opts.SelectionMode = semantic.ExactConcept
	if _, _, err := semantic.DecodeCursor(legacy, opts, "build"); err != nil {
		t.Fatalf("explicit exact mode rejected legacy cursor: %v", err)
	}
	opts.SelectionMode = semantic.SystemVersionCategory
	if _, _, err := semantic.DecodeCursor(legacy, opts, "build"); err != semantic.ErrSemanticInventoryCursorMismatch {
		t.Fatalf("category request reused exact cursor: %v", err)
	}
	category := semantic.EncodeCursor(opts, "build", "binding", `family:["Specimen","type.coding[]","urn:specimen",""]`)
	for name, change := range map[string]func(*semantic.PageOptions){
		"mode":       func(o *semantic.PageOptions) { o.SelectionMode = semantic.ExactConcept },
		"scope":      func(o *semantic.PageOptions) { o.AuthResourcePaths = []string{"scope-b"} },
		"resource":   func(o *semantic.PageOptions) { o.ResourceType = "Condition" },
		"query":      func(o *semantic.PageOptions) { o.Query = "Tumor" },
		"generation": func(o *semantic.PageOptions) { o.DatasetGeneration = "another" },
		"project":    func(o *semantic.PageOptions) { o.Project = "another" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := opts
			change(&changed)
			if _, _, err := semantic.DecodeCursor(category, changed, "build"); err != semantic.ErrSemanticInventoryCursorMismatch {
				t.Fatalf("changed request accepted category cursor: %v", err)
			}
		})
	}
	if _, _, err := semantic.DecodeCursor(category, opts, "other-build"); err != semantic.ErrSemanticInventoryCursorMismatch {
		t.Fatalf("changed build accepted category cursor: %v", err)
	}
}

func TestCatalogBindVarsKeepRestrictedEmptyScopeRestricted(t *testing.T) {
	restricted := ExplicitAuthResourcePathsUnrestricted(false)
	if got := EffectiveAuthResourcePathsUnrestricted([]string{}, restricted); got {
		t.Fatalf("restricted empty scope = %v, want false", got)
	}
}

func TestCatalogBindVarsPreserveLegacyEmptyUnrestrictedScope(t *testing.T) {
	if got := EffectiveAuthResourcePathsUnrestricted(nil, nil); !got {
		t.Fatalf("legacy empty scope unrestricted = %v, want true", got)
	}
}
