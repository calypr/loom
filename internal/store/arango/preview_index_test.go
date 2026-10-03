package arango

import (
	"testing"

	driver "github.com/arangodb/go-driver/v2/arangodb"
)

func TestValidPreviewCoveringIndexRequiresCompilerOwnedPaths(t *testing.T) {
	fields := []string{"project", "dataset_generation", "auth_resource_path", "payload.subject.reference", "_key"}
	if !validPreviewCoveringIndex("Specimen", "loom_pivot_preview_abcdef12", fields) {
		t.Fatal("valid covering index specification was rejected")
	}
	categoryFields := []string{"project", "dataset_generation", "payload.status", "auth_resource_path"}
	if !validPreviewCoveringIndex("Observation", "loom_pivot_preview_category", categoryFields) {
		t.Fatal("valid category-ordered index specification was rejected")
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
			if validPreviewCoveringIndex(test.collection, test.indexName, test.fields) {
				t.Fatal("invalid covering index specification was accepted")
			}
		})
	}
}

func TestValidPreviewCoveringIndexGroupProjectionFields(t *testing.T) {
	fields := []string{"project", "dataset_generation", "auth_resource_path", "payload.collection.bodySite.reference.reference", "payload.type.coding"}
	if !validPreviewCoveringIndex("Specimen", "loom_pivot_preview_abcdef12", fields) {
		t.Fatal("valid Group covering index specification was rejected")
	}
	for _, test := range []struct {
		name   string
		fields []string
	}{
		{name: "unsafe projection", fields: []string{"project", "dataset_generation", "auth_resource_path", "payload.type.coding]"}},
		{name: "duplicate projection", fields: []string{"project", "dataset_generation", "auth_resource_path", "payload.type.coding", "payload.type.coding"}},
		{name: "missing authorization field", fields: []string{"project", "dataset_generation", "payload.type.coding"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if validPreviewCoveringIndex("Specimen", "loom_pivot_preview_abcdef12", test.fields) {
				t.Fatal("invalid covering index specification was accepted")
			}
		})
	}
}

func TestPreviewCoveringIndexMatchesFieldsForIdempotence(t *testing.T) {
	fields := []string{"project", "dataset_generation", "auth_resource_path"}
	no := false
	index := driver.IndexResponse{
		Type:               driver.IndexType("persistent"),
		IndexSharedOptions: driver.IndexSharedOptions{Unique: &no, Sparse: &no},
		RegularIndex: &driver.IndexOptions{
			Fields: append([]string(nil), fields...),
		},
	}
	if !previewCoveringIndexMatches(index, fields) {
		t.Fatal("matching index definition was not recognized")
	}
	index.RegularIndex.Fields = []string{"project", "dataset_generation", "auth_resource_path", "payload.type.coding"}
	if previewCoveringIndexMatches(index, fields) {
		t.Fatal("different field tuple was treated as the same index")
	}
	index.RegularIndex.Fields = append([]string(nil), fields...)
	index.Sparse = boolPointer(true)
	if previewCoveringIndexMatches(index, fields) {
		t.Fatal("sparse index was accepted for a compiler covering index")
	}
	index.Sparse = boolPointer(false)
	index.Unique = boolPointer(true)
	if previewCoveringIndexMatches(index, fields) {
		t.Fatal("unique index was accepted for a compiler covering index")
	}
}

func boolPointer(value bool) *bool { return &value }
