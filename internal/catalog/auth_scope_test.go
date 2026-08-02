package catalog

import "testing"

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
