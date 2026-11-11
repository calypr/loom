package ingest

import (
	"context"
	"encoding/json"

	arangofactory "github.com/calypr/loom/internal/backends/arango"
	"github.com/calypr/loom/internal/catalog"
)

const EdgeCollection = "fhir_edge"

// ConnectionOptions is the ingest spelling of the backend factory config.
type ConnectionOptions = arangofactory.Config

func openBackend(ctx context.Context, opts ConnectionOptions) (*arangofactory.Runtime, error) {
	return arangofactory.Open(ctx, opts)
}

func bootstrapSpecWithReporter(resourceTypes []string, truncate bool, reporter EventSink) arangofactory.BootstrapSpec {
	collections := make([]arangofactory.CollectionSpec, 0, len(resourceTypes)+4)
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
			// before stable key or FHIR-id lookup.
			{"project", "dataset_generation", "_key"},
			{"project", "dataset_generation", "auth_resource_path", "_key"},
			{"project", "dataset_generation", "id"},
			{"project", "dataset_generation", "auth_resource_path", "id"},
		}
		collections = append(collections, arangofactory.CollectionSpec{
			Name:     name,
			Truncate: truncate,
			Indexes:  indexes,
		})
	}
	collections = append(collections,
		arangofactory.CollectionSpec{
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
		arangofactory.CollectionSpec{
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
		arangofactory.CollectionSpec{
			Name:     catalog.RelationshipCatalogCollection,
			Truncate: truncate,
			Indexes: [][]string{
				{"project", "dataset_generation", "to_type"},
				{"project", "dataset_generation", "auth_resource_path", "to_type"},
				{"project", "dataset_generation", "from_type"},
				{"project", "dataset_generation", "auth_resource_path", "from_type"},
			},
		},
		arangofactory.CollectionSpec{
			Name: catalog.SemanticInventoryCollection,
			Indexes: [][]string{
				{"project", "dataset_generation", "build_id"},
				{"project", "dataset_generation", "auth_resource_path", "resource_type", "binding_id", "concept_id"},
				{"project", "dataset_generation", "build_id", "binding_id", "concept_id", "auth_resource_path"},
				{"project", "dataset_generation", "build_id", "source_kind", "binding_id", "concept_id", "auth_resource_path"},
				{"project", "dataset_generation", "build_id", "source_id"},
			},
		},
		arangofactory.CollectionSpec{
			Name: catalog.SemanticInventoryEntryCollection,
			Indexes: [][]string{
				{"project", "dataset_generation", "build_id", "source_kind", "binding_id", "concept_id", "auth_resource_path"},
				{"project", "dataset_generation", "build_id", "source_kind", "resource_type", "binding_id", "concept_id", "auth_resource_path"},
			},
		},
		arangofactory.CollectionSpec{
			Name: catalog.SemanticInventoryBuildCollection,
			Indexes: [][]string{
				{"project", "dataset_generation"},
			},
		},
		arangofactory.CollectionSpec{
			Name: catalog.ResourceInventoryCollection,
			Indexes: [][]string{
				{"project", "dataset_generation", "record_type", "resource_type", "auth_resource_path"},
				{"project", "dataset_generation", "build_id", "record_type"},
			},
		},
	)
	return arangofactory.BootstrapSpec{
		Collections: collections,
		Reporter: func(event string, fields map[string]any) {
			emitEvent(reporter, event, fields)
		},
	}
}

// lifecycleBootstrapSpecWithReporter keeps generation manifests outside the
// truncation-oriented FHIR bootstrap.
func lifecycleBootstrapSpecWithReporter(reporter EventSink) arangofactory.BootstrapSpec {
	return arangofactory.BootstrapSpec{
		Collections: arangofactory.DatasetCollectionSpecs(),
		Reporter: func(event string, fields map[string]any) {
			emitEvent(reporter, event, fields)
		},
	}
}

func insertRawDocuments(ctx context.Context, backend *arangofactory.Client, collection string, docs []json.RawMessage, overwrite bool, writeAPI string) error {
	return backend.InsertBatchRaw(ctx, collection, docs, overwrite, writeAPI)
}
