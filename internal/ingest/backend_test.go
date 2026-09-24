package ingest

import (
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/catalog"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func TestBootstrapSpecAddsRootPreviewIndexesForEveryFHIRCollection(t *testing.T) {
	resources := []string{"Patient", "Specimen", "Observation", "DocumentReference"}
	spec := bootstrapSpecWithReporter(resources, false, nil)
	for _, resource := range resources {
		collection, found := bootstrapCollection(spec, resource)
		if !found {
			t.Fatalf("bootstrap collection %q is missing", resource)
		}
		for _, required := range [][]string{
			{"project", "_key"},
			{"project", "auth_resource_path", "_key"},
		} {
			if !containsIndex(collection.Indexes, required) {
				t.Fatalf("collection %q indexes %#v do not include required preview index %#v", resource, collection.Indexes, required)
			}
		}
	}
}

func TestBootstrapSpecAddsGenerationScopedIndexesWithoutTraversalSpeculation(t *testing.T) {
	spec := bootstrapSpecWithReporter([]string{"Patient"}, true, nil)
	patient, found := bootstrapCollection(spec, "Patient")
	if !found {
		t.Fatal("Patient bootstrap collection is missing")
	}
	for _, required := range [][]string{
		{"project", "dataset_generation", "_key"},
		{"project", "dataset_generation", "auth_resource_path", "_key"},
		{"project", "dataset_generation", "id"},
		{"project", "dataset_generation", "auth_resource_path", "id"},
	} {
		if !containsIndex(patient.Indexes, required) {
			t.Fatalf("Patient indexes %#v do not include generation index %#v", patient.Indexes, required)
		}
	}

	edges, found := bootstrapCollection(spec, EdgeCollection)
	if !found {
		t.Fatal("edge bootstrap collection is missing")
	}
	for _, required := range [][]string{
		{"project", "dataset_generation", "from_type", "label"},
		{"project", "dataset_generation", "to_type", "label"},
		{"project", "dataset_generation", "auth_resource_path", "from_type", "label"},
		{"project", "dataset_generation", "auth_resource_path", "to_type", "label"},
		{"_to", "project", "dataset_generation", "label", "from_type"},
		{"_from", "project", "dataset_generation", "label", "to_type"},
	} {
		if !containsIndex(edges.Indexes, required) {
			t.Fatalf("edge indexes %#v do not include generation index %#v", edges.Indexes, required)
		}
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
	memberships, found := bootstrapCollection(spec, catalog.FieldSourceMembershipCollection)
	if !found || memberships.Truncate {
		t.Fatalf("field-source membership collection = %#v found=%v, want persistent non-truncated collection", memberships, found)
	}
	if !containsIndex(memberships.Indexes, []string{"project", "dataset_generation", "resource_type", "scalar_paths[*]"}) || len(memberships.Indexes) != 1 {
		t.Fatalf("field-source membership indexes=%v, want exactly one expanded-array index", memberships.Indexes)
	}
	fieldSourceBuilds, found := bootstrapCollection(spec, catalog.FieldSourceMembershipBuildCollection)
	if !found || fieldSourceBuilds.Truncate {
		t.Fatalf("field-source membership build collection = %#v found=%v, want persistent non-truncated collection", fieldSourceBuilds, found)
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
	inventory, found := bootstrapCollection(spec, catalog.SemanticInventoryCollection)
	if !found || inventory.Truncate {
		t.Fatalf("semantic inventory collection = %#v found=%v, want persistent non-truncated collection", inventory, found)
	}
	for _, required := range [][]string{
		{"project", "dataset_generation", "auth_resource_path", "resource_type", "binding_id", "concept_id"},
		{"project", "dataset_generation", "build_id", "binding_id", "concept_id", "auth_resource_path"},
		{"project", "dataset_generation", "build_id", "source_kind", "binding_id", "concept_id", "auth_resource_path"},
	} {
		if !containsIndex(inventory.Indexes, required) {
			t.Fatalf("semantic inventory indexes %#v do not include %#v", inventory.Indexes, required)
		}
	}
	for _, redundant := range [][]string{
		{"project", "dataset_generation", "build_id"},
		{"project", "dataset_generation", "build_id", "source_id"},
	} {
		if containsIndex(inventory.Indexes, redundant) {
			t.Fatalf("semantic inventory indexes %#v retain redundant write-amplifying index %#v", inventory.Indexes, redundant)
		}
	}
	builds, found := bootstrapCollection(spec, catalog.SemanticInventoryBuildCollection)
	if !found || builds.Truncate {
		t.Fatalf("semantic inventory build collection = %#v found=%v, want persistent non-truncated collection", builds, found)
	}

}

func TestLifecycleBootstrapNeverTruncatesMetadata(t *testing.T) {
	spec := lifecycleBootstrapSpecWithReporter(nil)
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
