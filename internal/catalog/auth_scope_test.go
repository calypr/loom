package catalog

import "testing"

func TestCatalogBindVarsKeepRestrictedEmptyScopeRestricted(t *testing.T) {
	restricted := ExplicitAuthResourcePathsUnrestricted(false)

	fieldBinds := populatedFieldsBindVars(PopulatedFieldOptions{
		Project:                       "P1",
		AuthResourcePathsUnrestricted: restricted,
		AuthResourcePaths:             []string{},
	})
	if got, ok := fieldBinds["auth_resource_paths_unrestricted"].(bool); !ok || got {
		t.Fatalf("field catalog unrestricted bind = %#v, want false", fieldBinds["auth_resource_paths_unrestricted"])
	}
	if paths, ok := fieldBinds["auth_resource_paths"].([]string); !ok || len(paths) != 0 {
		t.Fatalf("field catalog paths bind = %#v, want empty []string", fieldBinds["auth_resource_paths"])
	}

	referenceBinds := populatedReferencesBindVars(PopulatedReferenceOptions{
		Project:                       "P1",
		AuthResourcePathsUnrestricted: restricted,
		AuthResourcePaths:             []string{},
	}, TraversalModeBuilder)
	if got, ok := referenceBinds["auth_resource_paths_unrestricted"].(bool); !ok || got {
		t.Fatalf("reference catalog unrestricted bind = %#v, want false", referenceBinds["auth_resource_paths_unrestricted"])
	}
}

func TestCatalogBindVarsPreserveLegacyEmptyUnrestrictedScope(t *testing.T) {
	binds := populatedFieldsBindVars(PopulatedFieldOptions{Project: "P1"})
	if got, ok := binds["auth_resource_paths_unrestricted"].(bool); !ok || !got {
		t.Fatalf("legacy empty scope unrestricted bind = %#v, want true", binds["auth_resource_paths_unrestricted"])
	}
}
