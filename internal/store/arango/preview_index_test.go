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

func TestPreviewCoveringIndexDefinitionsListOnlySafeCompilerIndexes(t *testing.T) {
	fields := []string{"project", "dataset_generation", "resourceType", "payload.valueQuantity.code", "auth_resource_path"}
	foreign := previewIndexResponse("user_owned", "Observation/user_owned", fields)
	sparse := previewIndexResponse("loom_pivot_preview_sparse", "Observation/sparse", fields)
	sparse.Sparse = boolPointer(true)
	unique := previewIndexResponse("loom_pivot_preview_unique", "Observation/unique", fields)
	unique.Unique = boolPointer(true)
	wrongType := previewIndexResponse("loom_pivot_preview_wrong_type", "Observation/wrong-type", fields)
	wrongType.Type = driver.IndexType("hash")
	wrongFields := previewIndexResponse("loom_pivot_preview_wrong_fields", "Observation/wrong-fields", []string{"project", "dataset_generation", "payload.valueQuantity.code"})
	want := previewIndexResponse("loom_pivot_preview_existing", "Observation/existing", fields)
	indexes := []driver.IndexResponse{foreign, sparse, unique, wrongType, wrongFields, want}
	definitions := previewCoveringIndexDefinitions("Observation", indexes)
	if len(definitions) != 1 {
		t.Fatalf("preview index definitions = %+v, want only the safe compiler-owned index", definitions)
	}
	if definitions[0].Collection != "Observation" || definitions[0].Name != want.Name || !slices.Equal(definitions[0].Fields, fields) {
		t.Fatalf("preview index definition = %+v, want %q with fields %v", definitions[0], want.Name, fields)
	}
	fields[0] = "mutated"
	if definitions[0].Fields[0] != "project" {
		t.Fatal("preview index metadata aliases the Arango response fields")
	}
}

func TestStoredValuesPreviewIndexRequiresExactProjectionContract(t *testing.T) {
	fields := []string{"project", "dataset_generation", "auth_resource_path", "_key", "payload.id", "payload.status"}
	storedValues := []string{"payload.valueQuantity"}
	if !validPreviewCoveringIndexWithStoredValues("Observation", "loom_pivot_preview_projection", fields, storedValues) {
		t.Fatal("valid direct-root stored-values preview index was rejected")
	}
	for _, test := range []struct {
		name   string
		fields []string
		values []string
	}{
		{name: "missing direct scalar key", fields: []string{"project", "dataset_generation", "auth_resource_path", "_key"}, values: storedValues},
		{name: "nondeterministic key order", fields: []string{"project", "dataset_generation", "auth_resource_path", "_key", "payload.status", "payload.id"}, values: storedValues},
		{name: "key overlaps stored parent", fields: []string{"project", "dataset_generation", "auth_resource_path", "_key", "payload.valueQuantity.code"}, values: storedValues},
		{name: "missing stored values", fields: fields},
		{name: "leaf parent overlap", fields: fields, values: []string{"payload.valueQuantity", "payload.valueQuantity.code"}},
		{name: "unsafe path", fields: fields, values: []string{"payload.valueQuantity.code]"}},
		{name: "duplicate stored value", fields: fields, values: []string{"payload.status", "payload.status"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if validPreviewCoveringIndexWithStoredValues("Observation", "loom_pivot_preview_projection", test.fields, test.values) {
				t.Fatal("invalid stored-values preview index was accepted")
			}
		})
	}
	tooManyFields := []string{"project", "dataset_generation", "auth_resource_path", "_key"}
	for index := 0; index < 29; index++ {
		tooManyFields = append(tooManyFields, fmt.Sprintf("payload.extra%02d", index))
	}
	if validPreviewCoveringIndexWithStoredValues("Observation", "loom_pivot_preview_projection", tooManyFields, storedValues) {
		t.Fatal("more than 32 index fields were accepted")
	}
	tooMany := make([]string, 33)
	for index := range tooMany {
		tooMany[index] = fmt.Sprintf("payload.extra%d", index)
	}
	if validPreviewCoveringIndexWithStoredValues("Observation", "loom_pivot_preview_projection", fields, tooMany) {
		t.Fatal("more than 32 stored values were accepted")
	}
}

func TestEnsurePreviewCoveringIndexWithStoredValuesVerifiesExactInventory(t *testing.T) {
	ctx := context.Background()
	fields := []string{"project", "dataset_generation", "auth_resource_path", "_key", "payload.id", "payload.status"}
	storedValues := []string{"payload.valueQuantity"}
	name := "loom_pivot_preview_projection"
	t.Run("create exact definition", func(t *testing.T) {
		fake := &fakePreviewIndexCollection{}
		if err := ensurePreviewCoveringIndexWithStoredValues(ctx, fake, name, fields, storedValues); err != nil {
			t.Fatal(err)
		}
		if len(fake.indexes) != 1 || !previewCoveringIndexMatchesDefinition(fake.indexes[0], fields, storedValues) {
			t.Fatalf("created stored-values inventory = %#v", fake.indexes)
		}
	})
	t.Run("refuse same name with different stored values", func(t *testing.T) {
		wrong := previewIndexResponseWithStoredValues(name, "Observation/existing", fields, []string{"payload.other"})
		fake := &fakePreviewIndexCollection{indexes: []driver.IndexResponse{wrong}}
		if err := ensurePreviewCoveringIndexWithStoredValues(ctx, fake, name, fields, storedValues); err == nil {
			t.Fatal("stored-values mismatch was treated as an idempotent index")
		}
		if !slices.Equal(fake.events, []string{}) || len(fake.indexes) != 1 {
			t.Fatalf("mismatched inventory was mutated: events=%v indexes=%#v", fake.events, fake.indexes)
		}
	})
}

type fakePreviewIndexCollection struct {
	indexes        []driver.IndexResponse
	events         []string
	createErr      error
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
	index := previewIndexResponseWithStoredValues(options.Name, "Observation/"+options.Name, fields, options.StoredValues)
	f.indexes = append(f.indexes, index)
	return index, true, nil
}

func (f *fakePreviewIndexCollection) DeleteIndexByID(_ context.Context, id string) error {
	f.events = append(f.events, "delete:"+id)
	for index := range f.indexes {
		if f.indexes[index].ID == id {
			f.indexes = append(f.indexes[:index], f.indexes[index+1:]...)
			return nil
		}
	}
	return fmt.Errorf("index %s not found", id)
}

func previewIndexResponse(name, id string, fields []string) driver.IndexResponse {
	return previewIndexResponseWithStoredValues(name, id, fields, nil)
}

func previewIndexResponseWithStoredValues(name, id string, fields, storedValues []string) driver.IndexResponse {
	return driver.IndexResponse{
		Name: name, Type: driver.IndexType("persistent"), IndexSharedOptions: driver.IndexSharedOptions{
			ID: id, Unique: boolPointer(false), Sparse: boolPointer(false),
		},
		RegularIndex: &driver.IndexOptions{
			Fields: append([]string(nil), fields...), StoredValues: append([]string(nil), storedValues...),
		},
	}
}

func boolPointer(value bool) *bool { return &value }

func TestStoredValuesPreviewCoveringIndexCapRequiresPriorMigration(t *testing.T) {
	ctx := context.Background()
	fields := []string{"project", "dataset_generation", "auth_resource_path", "_key", "payload.id", "payload.status", "resourceType"}
	storedValues := []string{"payload.valueQuantity"}
	name := previewCoveringIndexNameWithStoredValues("Observation", fields, storedValues)
	legacyFields := []string{"project", "dataset_generation", "auth_resource_path", "_key", "payload.id", "payload.status"}
	fake := &fakePreviewIndexCollection{indexes: []driver.IndexResponse{
		previewIndexResponseWithStoredValues(previewCoveringIndexNameWithStoredValues("Observation", legacyFields, storedValues), "Observation/legacy", legacyFields, storedValues),
		previewIndexResponse("loom_pivot_preview_other_a", "Observation/other-a", []string{"project", "dataset_generation", "auth_resource_path", "payload.a"}),
		previewIndexResponse("loom_pivot_preview_other_b", "Observation/other-b", []string{"project", "dataset_generation", "auth_resource_path", "payload.b"}),
		previewIndexResponse("loom_pivot_preview_other_c", "Observation/other-c", []string{"project", "dataset_generation", "auth_resource_path", "payload.c"}),
	}}
	if err := ensurePreviewCoveringIndexWithStoredValues(ctx, fake, name, fields, storedValues); !errors.Is(err, ErrPreviewCoveringIndexLimit) {
		t.Fatalf("unmigrated stored-values cap error = %v, want index limit", err)
	}
	if len(fake.events) != 0 || len(fake.indexes) != maxPreviewCoveringIndexesPerCollection {
		t.Fatalf("stored-values cap refusal changed inventory: events=%v indexes=%d", fake.events, len(fake.indexes))
	}
}

func TestPreviewCoveringIndexCapRequiresPriorMigration(t *testing.T) {
	ctx := context.Background()
	fields := []string{"project", "dataset_generation", "resourceType", "payload.status", "auth_resource_path"}
	name := previewCoveringIndexName("Patient", fields)
	legacy := []string{"project", "dataset_generation", "payload.status", "auth_resource_path"}
	fake := &fakePreviewIndexCollection{indexes: []driver.IndexResponse{
		previewIndexResponse(previewCoveringIndexName("Patient", legacy), "Patient/legacy", legacy),
		previewIndexResponse("loom_pivot_preview_other_a", "Patient/other-a", []string{"project", "dataset_generation", "auth_resource_path", "payload.a"}),
		previewIndexResponse("loom_pivot_preview_other_b", "Patient/other-b", []string{"project", "dataset_generation", "auth_resource_path", "payload.b"}),
		previewIndexResponse("loom_pivot_preview_other_c", "Patient/other-c", []string{"project", "dataset_generation", "auth_resource_path", "payload.c"}),
	}}
	if err := ensurePreviewCoveringIndex(ctx, fake, name, fields); !errors.Is(err, ErrPreviewCoveringIndexLimit) {
		t.Fatalf("unmigrated capped inventory error = %v, want index limit", err)
	}
	if len(fake.events) != 0 || len(fake.indexes) != maxPreviewCoveringIndexesPerCollection {
		t.Fatalf("cap refusal changed inventory: events=%v indexes=%d", fake.events, len(fake.indexes))
	}
}

func TestPreviewCoveringIndexAtCapIsIdempotentAfterMigration(t *testing.T) {
	ctx := context.Background()
	fields := []string{"project", "dataset_generation", "resourceType", "payload.status", "auth_resource_path"}
	name := previewCoveringIndexName("Patient", fields)
	fake := &fakePreviewIndexCollection{indexes: []driver.IndexResponse{
		previewIndexResponse(name, "Patient/current", fields),
		previewIndexResponse("loom_pivot_preview_other_a", "Patient/other-a", []string{"project", "dataset_generation", "auth_resource_path", "payload.a"}),
		previewIndexResponse("loom_pivot_preview_other_b", "Patient/other-b", []string{"project", "dataset_generation", "auth_resource_path", "payload.b"}),
		previewIndexResponse("loom_pivot_preview_other_c", "Patient/other-c", []string{"project", "dataset_generation", "auth_resource_path", "payload.c"}),
	}}
	if err := ensurePreviewCoveringIndex(ctx, fake, name, fields); err != nil {
		t.Fatal(err)
	}
	if len(fake.events) != 0 || len(fake.indexes) != maxPreviewCoveringIndexesPerCollection {
		t.Fatalf("existing current index was not idempotent: events=%v indexes=%d", fake.events, len(fake.indexes))
	}
}
