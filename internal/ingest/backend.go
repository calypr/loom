package ingest

import (
	"context"
	"encoding/json"

	"github.com/calypr/loom/internal/catalog"
	datasetarango "github.com/calypr/loom/internal/dataset/arango"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

const EdgeCollection = "fhir_edge"

func openBackend(ctx context.Context, opts arangostore.ConnectionOptions) (*arangostore.Client, error) {
	return arangostore.Open(ctx, opts.URL, opts.Database)
}

func bootstrapSpecWithReporter(resourceTypes []string, truncate bool, reporter EventSink) arangostore.BootstrapSpec {
	collections := make([]arangostore.CollectionSpec, 0, len(resourceTypes)+3)
	for _, name := range resourceTypes {
		// Every generated FHIR resource is a possible dataframe root. The
		// compiler applies project and optional authorization scope before a
		// stable _key sort/limit, so these are not Patient-specific indexes.
		// Without the _key suffix Arango can choose the primary index only to
		// satisfy SORT root._key, then scan an entire resource collection before
		// it finds a project's first preview rows.
		indexes := [][]string{
			{"project"},
			{"id"},
			{"project", "id"},
			{"project", "_key"},
			{"project", "auth_resource_path"},
			{"project", "auth_resource_path", "_key"},
			// Dataset generations use immutable, hashed physical keys. These
			// indexes support the compiler's mandatory project/generation scope
			// before stable key or FHIR-id lookup without changing legacy plans.
			{"project", "dataset_generation", "_key"},
			{"project", "dataset_generation", "auth_resource_path", "_key"},
			{"project", "dataset_generation", "id"},
			{"project", "dataset_generation", "auth_resource_path", "id"},
		}
		collections = append(collections, arangostore.CollectionSpec{
			Name:     name,
			Truncate: truncate,
			Indexes:  indexes,
		})
	}
	collections = append(collections,
		arangostore.CollectionSpec{
			Name:     EdgeCollection,
			Edge:     true,
			Truncate: truncate,
			Indexes: [][]string{
				{"project", "label"},
				{"project", "from_type", "label"},
				{"project", "to_type", "label"},
				{"project", "dataset_generation", "from_type", "label"},
				{"project", "dataset_generation", "to_type", "label"},
				{"project", "dataset_generation", "auth_resource_path", "from_type", "label"},
				{"project", "dataset_generation", "auth_resource_path", "to_type", "label"},
				// These are vertex-centric persistent indexes. A dataframe
				// traversal always starts at one known root/parent endpoint and
				// applies exact project, generation, label, and target-type
				// predicates. The default Arango edge index can locate the
				// endpoint but must inspect every adjacent edge; these compound
				// indexes let the optimizer restrict the adjacency list before
				// materializing candidate edges. Keep the endpoint first: `_to`
				// is INBOUND and `_from` is OUTBOUND.
				{"_to", "project", "dataset_generation", "label", "from_type"},
				{"_from", "project", "dataset_generation", "label", "to_type"},
			},
		},
		arangostore.CollectionSpec{
			Name:     "fhir_field_catalog",
			Truncate: truncate,
			Indexes: [][]string{
				{"project", "resource_type"},
				{"project", "auth_resource_path", "resource_type"},
				{"project", "resource_type", "path"},
				{"project", "auth_resource_path", "resource_type", "path"},
				{"project", "resource_type", "pivot_candidate"},
				{"project", "dataset_generation", "resource_type"},
				{"project", "dataset_generation", "auth_resource_path", "resource_type"},
				{"project", "dataset_generation", "resource_type", "path"},
				{"project", "dataset_generation", "auth_resource_path", "resource_type", "path"},
				{"project", "dataset_generation", "resource_type", "pivot_candidate"},
				{"project", "dataset_generation", "auth_resource_path", "resource_type", "pivot_candidate"},
			},
		},
		arangostore.CollectionSpec{
			Name:     catalog.RelationshipCatalogCollection,
			Truncate: truncate,
			Indexes: [][]string{
				{"project", "dataset_generation", "to_type"},
				{"project", "dataset_generation", "auth_resource_path", "to_type"},
				{"project", "dataset_generation", "from_type"},
				{"project", "dataset_generation", "auth_resource_path", "from_type"},
			},
		},
	)
	return arangostore.BootstrapSpec{
		Collections: collections,
		Reporter: func(event string, fields map[string]any) {
			emitEvent(reporter, event, fields)
		},
	}
}

// lifecycleBootstrapSpecWithReporter keeps generation manifests outside the
// truncation-oriented FHIR bootstrap. Load invokes this spec first when a
// caller explicitly selects immutable dataset-generation mode.
func lifecycleBootstrapSpecWithReporter(reporter EventSink) arangostore.BootstrapSpec {
	return arangostore.BootstrapSpec{
		Collections: datasetarango.CollectionSpecs(),
		Reporter: func(event string, fields map[string]any) {
			emitEvent(reporter, event, fields)
		},
	}
}

func insertRawDocuments(ctx context.Context, backend *arangostore.Client, collection string, docs []json.RawMessage, overwrite bool, writeAPI string) error {
	return backend.InsertBatchRaw(ctx, collection, docs, overwrite, writeAPI)
}
