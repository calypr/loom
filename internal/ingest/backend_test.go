package ingest

import (
	"reflect"
	"testing"

	catalogarango "github.com/calypr/loom/internal/catalog/arango"
	"github.com/calypr/loom/internal/catalog/semanticinventory"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func TestBootstrapSpecAddsMergedIndexesForEveryFHIRCollection(t *testing.T) {
	resources := []string{"Patient", "Specimen", "Observation", "DocumentReference"}
	spec := BootstrapSpecWithReporter(resources, false, nil)
	for _, resource := range resources {
		collection, found := bootstrapCollection(spec, resource)
		if !found {
			t.Fatalf("bootstrap collection %q is missing", resource)
		}
		for _, required := range [][]string{
			{"project", "dataset_generation", "id"},
			{"project", "dataset_generation", "auth_resource_path", "_key"},
		} {
			if !containsIndex(collection.Indexes, required) {
				t.Fatalf("collection %q indexes %#v do not include required preview index %#v", resource, collection.Indexes, required)
			}
		}
	}
}

func TestBootstrapSpecAddsGenerationScopedIndexesWithoutTraversalSpeculation(t *testing.T) {
	spec := BootstrapSpecWithReporter([]string{"Patient"}, true, nil)
	patient, found := bootstrapCollection(spec, "Patient")
	if !found {
		t.Fatal("Patient bootstrap collection is missing")
	}
	for _, required := range [][]string{
		{"project", "dataset_generation", "id"},
		{"project", "dataset_generation", "auth_resource_path", "_key"},
	} {
		if !containsIndex(patient.Indexes, required) {
			t.Fatalf("Patient indexes %#v do not include generation index %#v", patient.Indexes, required)
		}
	}

	edges, found := bootstrapCollection(spec, EdgeCollection)
	if !found {
		t.Fatal("edge bootstrap collection is missing")
	}
	if len(edges.PersistentIndexes) != 1 ||
		!reflect.DeepEqual(edges.PersistentIndexes[0].Fields, []string{"project", "dataset_generation", "auth_resource_path", "from_type", "label", "to_type"}) ||
		!reflect.DeepEqual(edges.PersistentIndexes[0].StoredValues, []string{"source_file"}) {
		t.Fatalf("edge indexes = %+v, want the measured auth/type index with stored source_file", edges.PersistentIndexes)
	}

	fieldCatalog, found := bootstrapCollection(spec, "fhir_field_catalog")
	if !found {
		t.Fatal("field catalog bootstrap collection is missing")
	}
	for _, required := range [][]string{
		{"project", "dataset_generation", "resource_type"},
		{"project", "dataset_generation", "auth_resource_path", "resource_type"},
		{"project", "dataset_generation", "resource_type", "path"},
		{"project", "dataset_generation", "auth_resource_path", "resource_type", "path"},
		{"project", "dataset_generation", "resource_type", "pivot_candidate"},
		{"project", "dataset_generation", "auth_resource_path", "resource_type", "pivot_candidate"},
	} {
		if !containsIndex(fieldCatalog.Indexes, required) {
			t.Fatalf("catalog indexes %#v do not include generation index %#v", fieldCatalog.Indexes, required)
		}
	}
	relationships, found := bootstrapCollection(spec, "fhir_relationship_catalog")
	if !found {
		t.Fatal("relationship catalog bootstrap collection is missing")
	}
	for _, required := range [][]string{
		{"project", "dataset_generation", "to_type"},
		{"project", "dataset_generation", "auth_resource_path", "to_type"},
		{"project", "dataset_generation", "from_type"},
		{"project", "dataset_generation", "auth_resource_path", "from_type"},
	} {
		if !containsIndex(relationships.Indexes, required) {
			t.Fatalf("relationship catalog indexes %#v do not include %#v", relationships.Indexes, required)
		}
	}
	inventory, found := bootstrapCollection(spec, semanticinventory.Collection)
	if !found || inventory.Truncate {
		t.Fatalf("semantic inventory collection = %#v found=%v, want persistent non-truncated collection", inventory, found)
	}
	for _, required := range [][]string{
		{"project", "dataset_generation", "build_id"},
		{"project", "dataset_generation", "auth_resource_path", "resource_type", "binding_id", "concept_id"},
		{"project", "dataset_generation", "build_id", "binding_id", "concept_id", "auth_resource_path"},
		{"project", "dataset_generation", "build_id", "source_kind", "binding_id", "concept_id", "auth_resource_path"},
		{"project", "dataset_generation", "build_id", "source_id"},
	} {
		if !containsIndex(inventory.Indexes, required) {
			t.Fatalf("semantic inventory indexes %#v do not include %#v", inventory.Indexes, required)
		}
	}
	builds, found := bootstrapCollection(spec, semanticinventory.BuildCollection)
	if !found || builds.Truncate {
		t.Fatalf("semantic inventory build collection = %#v found=%v, want persistent non-truncated collection", builds, found)
	}

}

func TestBootstrapSpecDoesNotCreateOptionalSemanticCoverageCollection(t *testing.T) {
	spec := BootstrapSpecWithReporter([]string{"Patient"}, true, nil)
	if _, found := bootstrapCollection(spec, catalogarango.SemanticCoverageCollection); found {
		t.Fatal("normal generation bootstrap created the optional per-source semantic coverage collection")
	}
}

func TestLifecycleBootstrapNeverTruncatesMetadata(t *testing.T) {
	spec := LifecycleBootstrapSpecWithReporter(nil)
	if len(spec.Collections) != 1 {
		t.Fatalf("lifecycle collection count = %d, want 1", len(spec.Collections))
	}
	if spec.Collections[0].Truncate {
		t.Fatalf("lifecycle collection %#v requests truncation", spec.Collections[0])
	}
}

func bootstrapCollection(spec arangostore.BootstrapSpec, name string) (arangostore.CollectionSpec, bool) {
	for _, collection := range spec.Collections {
		if collection.Name == name {
			return collection, true
		}
	}
	return arangostore.CollectionSpec{}, false
}

func containsIndex(indexes [][]string, want []string) bool {
	for _, index := range indexes {
		if reflect.DeepEqual(index, want) {
			return true
		}
	}
	return false
}
