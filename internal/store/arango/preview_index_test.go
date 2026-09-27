package arango

import "testing"

func TestValidPivotPreviewIndexRequiresCompilerOwnedPaths(t *testing.T) {
	fields := []string{"project", "dataset_generation", "auth_resource_path", "payload.subject.reference", "_key"}
	if !validPivotPreviewIndex("Specimen", "loom_pivot_preview_abcdef12", fields) {
		t.Fatal("valid covering index specification was rejected")
	}
	for _, test := range []struct {
		name       string
		collection string
		indexName  string
		fields     []string
	}{
		{name: "unsafe collection", collection: "Specimen;DROP", indexName: "loom_pivot_preview_abcdef12", fields: fields},
		{name: "foreign index", collection: "Specimen", indexName: "user_index", fields: fields},
		{name: "unsafe field", collection: "Specimen", indexName: "loom_pivot_preview_abcdef12", fields: []string{"project", "dataset_generation", "auth_resource_path", "payload.x]"}},
		{name: "duplicate field", collection: "Specimen", indexName: "loom_pivot_preview_abcdef12", fields: []string{"project", "dataset_generation", "auth_resource_path", "_key", "_key"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if validPivotPreviewIndex(test.collection, test.indexName, test.fields) {
				t.Fatal("invalid covering index specification was accepted")
			}
		})
	}
}
