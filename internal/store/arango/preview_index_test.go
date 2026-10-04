package arango

import (
	"context"
	"errors"
	"fmt"
	"slices"
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
	relatedCategoryFields := []string{"project", "dataset_generation", "resourceType", "payload.valueQuantity.code", "auth_resource_path"}
	if !validPreviewCoveringIndex("Observation", "loom_pivot_preview_related_category", relatedCategoryFields) {
		t.Fatal("valid related-category type-ordered index specification was rejected")
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
		{name: "related category type after path", collection: "Observation", indexName: "loom_pivot_preview_abcdef12", fields: []string{"project", "dataset_generation", "payload.status", "resourceType", "auth_resource_path"}},
		{name: "related category missing auth path", collection: "Observation", indexName: "loom_pivot_preview_abcdef12", fields: []string{"project", "dataset_generation", "resourceType", "payload.status"}},
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

func TestRelatedCategoryIndexReplacementStaysWithinCapAndDropsExactLegacyOnly(t *testing.T) {
	ctx := context.Background()
	legacyName := "loom_pivot_preview_legacy_category"
	legacyFields := []string{"project", "dataset_generation", "payload.valueQuantity.code", "auth_resource_path"}
	newName := "loom_pivot_preview_related_category"
	newFields := []string{"project", "dataset_generation", "resourceType", "payload.valueQuantity.code", "auth_resource_path"}
	fake := &fakePreviewIndexCollection{indexes: []driver.IndexResponse{
		previewIndexResponse("loom_pivot_preview_old_a", "Observation/old-a", []string{"project", "dataset_generation", "auth_resource_path", "payload.a"}),
		previewIndexResponse("loom_pivot_preview_old_b", "Observation/old-b", []string{"project", "dataset_generation", "auth_resource_path", "payload.b"}),
		previewIndexResponse("loom_pivot_preview_old_c", "Observation/old-c", []string{"project", "dataset_generation", "auth_resource_path", "payload.c"}),
		previewIndexResponse(legacyName, "Observation/legacy", legacyFields),
	}}
	replacement := &previewIndexReplacement{name: legacyName, fields: legacyFields}
	if err := ensurePreviewCoveringIndex(ctx, fake, newName, newFields, replacement); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(fake.events, []string{"ensure:" + newName, "delete:Observation/legacy"}) {
		t.Fatalf("replacement operation order = %v, want create/verify new before deleting exact legacy", fake.events)
	}
	if len(fake.indexes) != maxPreviewCoveringIndexesPerCollection {
		t.Fatalf("preview index count = %d, want cap %d", len(fake.indexes), maxPreviewCoveringIndexesPerCollection)
	}
	if containsPreviewIndex(fake.indexes, legacyName) || !containsPreviewIndex(fake.indexes, newName) {
		t.Fatalf("replacement inventory = %#v, want new present and exact legacy absent", fake.indexes)
	}
	for _, name := range []string{"loom_pivot_preview_old_a", "loom_pivot_preview_old_b", "loom_pivot_preview_old_c"} {
		if !containsPreviewIndex(fake.indexes, name) {
			t.Errorf("unrelated preview index %q was removed", name)
		}
	}
}

func TestRelatedCategoryIndexReplacementPreservesLegacyOnCreateOrDeleteFailure(t *testing.T) {
	ctx := context.Background()
	legacyName := "loom_pivot_preview_legacy_category"
	legacyFields := []string{"project", "dataset_generation", "payload.valueQuantity.code", "auth_resource_path"}
	newName := "loom_pivot_preview_related_category"
	newFields := []string{"project", "dataset_generation", "resourceType", "payload.valueQuantity.code", "auth_resource_path"}
	replacement := &previewIndexReplacement{name: legacyName, fields: legacyFields}
	newFullInventory := func() []driver.IndexResponse {
		return []driver.IndexResponse{
			previewIndexResponse("loom_pivot_preview_old_a", "Observation/old-a", []string{"project", "dataset_generation", "auth_resource_path", "payload.a"}),
			previewIndexResponse("loom_pivot_preview_old_b", "Observation/old-b", []string{"project", "dataset_generation", "auth_resource_path", "payload.b"}),
			previewIndexResponse("loom_pivot_preview_old_c", "Observation/old-c", []string{"project", "dataset_generation", "auth_resource_path", "payload.c"}),
			previewIndexResponse(legacyName, "Observation/legacy", legacyFields),
		}
	}
	t.Run("create fails before any drop", func(t *testing.T) {
		fake := &fakePreviewIndexCollection{indexes: newFullInventory(), createErr: errors.New("create failed")}
		if err := ensurePreviewCoveringIndex(ctx, fake, newName, newFields, replacement); err == nil {
			t.Fatal("failed replacement create unexpectedly succeeded")
		}
		if containsPreviewIndex(fake.indexes, newName) || !containsPreviewIndex(fake.indexes, legacyName) {
			t.Fatalf("failed creation changed old index inventory: %#v", fake.indexes)
		}
		if len(fake.events) != 1 || fake.events[0] != "ensure:"+newName {
			t.Fatalf("failed creation events = %v, want ensure only", fake.events)
		}
	})
	t.Run("failed drop rolls back newly created replacement", func(t *testing.T) {
		fake := &fakePreviewIndexCollection{indexes: newFullInventory(), deleteErr: errors.New("delete failed"), failDeleteID: "Observation/legacy"}
		if err := ensurePreviewCoveringIndex(ctx, fake, newName, newFields, replacement); err == nil {
			t.Fatal("failed legacy deletion unexpectedly succeeded")
		}
		wantEvents := []string{"ensure:" + newName, "delete:Observation/legacy", "delete:Observation/" + newName}
		if !slices.Equal(fake.events, wantEvents) {
			t.Fatalf("failed deletion events = %v, want replacement rollback %v", fake.events, wantEvents)
		}
		if containsPreviewIndex(fake.indexes, newName) || !containsPreviewIndex(fake.indexes, legacyName) {
			t.Fatalf("failed deletion did not preserve legacy inventory: %#v", fake.indexes)
		}
	})
}

func TestRelatedCategoryIndexReplacementRollsBackUnexpectedCreatedIndex(t *testing.T) {
	ctx := context.Background()
	legacyName := "loom_pivot_preview_legacy_category"
	legacyFields := []string{"project", "dataset_generation", "payload.valueQuantity.code", "auth_resource_path"}
	newName := "loom_pivot_preview_related_category"
	newFields := []string{"project", "dataset_generation", "resourceType", "payload.valueQuantity.code", "auth_resource_path"}
	indexes := []driver.IndexResponse{
		previewIndexResponse("loom_pivot_preview_old_a", "Observation/old-a", []string{"project", "dataset_generation", "auth_resource_path", "payload.a"}),
		previewIndexResponse("loom_pivot_preview_old_b", "Observation/old-b", []string{"project", "dataset_generation", "auth_resource_path", "payload.b"}),
		previewIndexResponse("loom_pivot_preview_old_c", "Observation/old-c", []string{"project", "dataset_generation", "auth_resource_path", "payload.c"}),
		previewIndexResponse(legacyName, "Observation/legacy", legacyFields),
	}
	unexpected := previewIndexResponse(newName, "Observation/unexpected-new", []string{"project", "dataset_generation", "auth_resource_path", "payload.unexpected"})
	fake := &fakePreviewIndexCollection{indexes: indexes, createResponse: &unexpected}
	replacement := &previewIndexReplacement{name: legacyName, fields: legacyFields}
	if err := ensurePreviewCoveringIndex(ctx, fake, newName, newFields, replacement); err == nil {
		t.Fatal("unexpected created index definition was accepted")
	}
	if !slices.Equal(fake.events, []string{"ensure:" + newName, "delete:Observation/unexpected-new"}) {
		t.Fatalf("unexpected index rollback events = %v", fake.events)
	}
	if containsPreviewIndex(fake.indexes, newName) || !containsPreviewIndex(fake.indexes, legacyName) {
		t.Fatalf("verification failure did not remove owned wrong index and preserve legacy: %#v", fake.indexes)
	}
}

func TestRelatedCategoryIndexReplacementRefusesNonmatchingLegacyAtCap(t *testing.T) {
	ctx := context.Background()
	fields := []string{"project", "dataset_generation", "payload.valueQuantity.code", "auth_resource_path"}
	fake := &fakePreviewIndexCollection{indexes: []driver.IndexResponse{
		previewIndexResponse("loom_pivot_preview_a", "Observation/a", []string{"project", "dataset_generation", "auth_resource_path", "payload.a"}),
		previewIndexResponse("loom_pivot_preview_b", "Observation/b", []string{"project", "dataset_generation", "auth_resource_path", "payload.b"}),
		previewIndexResponse("loom_pivot_preview_c", "Observation/c", []string{"project", "dataset_generation", "auth_resource_path", "payload.c"}),
		previewIndexResponse("loom_pivot_preview_different_name", "Observation/legacy", fields),
	}}
	replacement := &previewIndexReplacement{
		name:   "loom_pivot_preview_expected_name",
		fields: fields,
	}
	newFields := []string{"project", "dataset_generation", "resourceType", "payload.valueQuantity.code", "auth_resource_path"}
	if err := ensurePreviewCoveringIndex(ctx, fake, "loom_pivot_preview_new", newFields, replacement); !errors.Is(err, ErrPreviewCoveringIndexLimit) {
		t.Fatalf("nonmatching legacy index error = %v, want cap error", err)
	}
	if len(fake.events) != 0 || len(fake.indexes) != maxPreviewCoveringIndexesPerCollection {
		t.Fatalf("nonmatching legacy changed inventory/events: indexes=%#v events=%v", fake.indexes, fake.events)
	}
}

func TestValidPreviewCoveringIndexReplacementIsSamePathAndExactShape(t *testing.T) {
	newFields := []string{"project", "dataset_generation", "resourceType", "payload.valueQuantity.code", "auth_resource_path"}
	oldFields := []string{"project", "dataset_generation", "payload.valueQuantity.code", "auth_resource_path"}
	valid := &previewIndexReplacement{name: "loom_pivot_preview_old", fields: oldFields}
	if !validPreviewCoveringIndexReplacement("Observation", "loom_pivot_preview_new", newFields, valid) {
		t.Fatal("exact same-path related category replacement was rejected")
	}
	for _, replacement := range []*previewIndexReplacement{
		{name: "loom_pivot_preview_other", fields: []string{"project", "dataset_generation", "payload.other", "auth_resource_path"}},
		{name: "loom_pivot_preview_bad", fields: []string{"project", "dataset_generation", "resourceType", "payload.valueQuantity.code", "auth_resource_path"}},
		{name: "loom_pivot_preview_bad;DROP", fields: oldFields},
	} {
		if validPreviewCoveringIndexReplacement("Observation", "loom_pivot_preview_new", newFields, replacement) {
			t.Errorf("invalid replacement accepted: %+v", replacement)
		}
	}
}

type fakePreviewIndexCollection struct {
	indexes        []driver.IndexResponse
	events         []string
	createErr      error
	deleteErr      error
	failDeleteID   string
	createResponse *driver.IndexResponse
}

func (f *fakePreviewIndexCollection) Indexes(context.Context) ([]driver.IndexResponse, error) {
	return append([]driver.IndexResponse(nil), f.indexes...), nil
}

func (f *fakePreviewIndexCollection) EnsurePersistentIndex(_ context.Context, fields []string, options *driver.CreatePersistentIndexOptions) (driver.IndexResponse, bool, error) {
	f.events = append(f.events, "ensure:"+options.Name)
	if f.createErr != nil {
		return driver.IndexResponse{}, false, f.createErr
	}
	if f.createResponse != nil {
		f.indexes = append(f.indexes, *f.createResponse)
		return *f.createResponse, true, nil
	}
	index := previewIndexResponse(options.Name, "Observation/"+options.Name, fields)
	f.indexes = append(f.indexes, index)
	return index, true, nil
}

func (f *fakePreviewIndexCollection) DeleteIndexByID(_ context.Context, id string) error {
	f.events = append(f.events, "delete:"+id)
	if id == f.failDeleteID && f.deleteErr != nil {
		return f.deleteErr
	}
	for index := range f.indexes {
		if f.indexes[index].ID == id {
			f.indexes = append(f.indexes[:index], f.indexes[index+1:]...)
			return nil
		}
	}
	return fmt.Errorf("index %s not found", id)
}

func previewIndexResponse(name, id string, fields []string) driver.IndexResponse {
	return driver.IndexResponse{
		Name: name, Type: driver.IndexType("persistent"), IndexSharedOptions: driver.IndexSharedOptions{
			ID: id, Unique: boolPointer(false), Sparse: boolPointer(false),
		},
		RegularIndex: &driver.IndexOptions{Fields: append([]string(nil), fields...)},
	}
}

func containsPreviewIndex(indexes []driver.IndexResponse, name string) bool {
	for _, index := range indexes {
		if index.Name == name {
			return true
		}
	}
	return false
}

func boolPointer(value bool) *bool { return &value }
