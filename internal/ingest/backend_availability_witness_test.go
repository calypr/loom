package ingest

import (
	"testing"

	"github.com/calypr/loom/internal/catalog"
)

func TestBootstrapSpecAddsPersistentAvailableColumnWitnessCollections(t *testing.T) {
	spec := bootstrapSpecWithReporter([]string{"Patient"}, true, nil)
	rows, found := bootstrapCollection(spec, catalog.AvailableColumnWitnessCollection)
	if !found || rows.Truncate {
		t.Fatalf("availability witness collection = %#v found=%v, want persistent collection", rows, found)
	}
	for _, required := range [][]string{
		{"project", "dataset_generation", "root_resource_type", "input_digest", "schema_version"},
		{"project", "dataset_generation", "root_resource_type", "input_digest", "schema_version", "source.Kind", "source.FieldPath"},
		{"project", "dataset_generation", "root_resource_type", "input_digest", "schema_version", "source.Kind", "source.ConceptID", "source.BindingID"},
	} {
		if !containsIndex(rows.Indexes, required) {
			t.Fatalf("availability witness indexes %#v do not include %#v", rows.Indexes, required)
		}
	}

	builds, found := bootstrapCollection(spec, catalog.AvailableColumnWitnessBuildCollection)
	if !found || builds.Truncate {
		t.Fatalf("availability witness build collection = %#v found=%v, want persistent collection", builds, found)
	}
	if !containsIndex(builds.Indexes, []string{"project", "dataset_generation", "root_resource_type", "input_digest", "schema_version"}) {
		t.Fatalf("availability witness build indexes %#v lack root-scoped lookup", builds.Indexes)
	}
}
