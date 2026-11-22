package ingest

import (
	"context"
	"encoding/json"

	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/catalog/semanticinventory"
	publicationarango "github.com/calypr/loom/internal/dataset/arango"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

const EdgeCollection = "fhir_edge"

func OpenBackend(ctx context.Context, opts arangostore.ConnectionOptions) (*arangostore.Client, error) {
	return arangostore.Open(ctx, opts.URL, opts.Database)
}

func BootstrapSpecWithReporter(resourceTypes []string, truncate bool, reporter EventSink) arangostore.BootstrapSpec {
	collections := make([]arangostore.CollectionSpec, 0, len(resourceTypes)+4)
	for _, name := range resourceTypes {
		indexes := [][]string{
			{"project", "dataset_generation", "id"},
			{"project", "dataset_generation", "auth_resource_path", "_key"},
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
			PersistentIndexes: []arangostore.PersistentIndexSpec{{
				Fields:       []string{"project", "dataset_generation", "auth_resource_path", "from_type", "label", "to_type"},
				StoredValues: []string{"source_file"},
			}},
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
		arangostore.CollectionSpec{
			Name: semanticinventory.Collection,
			Indexes: [][]string{
				{"project", "dataset_generation", "build_id"},
				{"project", "dataset_generation", "auth_resource_path", "resource_type", "binding_id", "concept_id"},
				{"project", "dataset_generation", "build_id", "binding_id", "concept_id", "auth_resource_path"},
				{"project", "dataset_generation", "build_id", "source_kind", "binding_id", "concept_id", "auth_resource_path"},
				{"project", "dataset_generation", "build_id", "source_id"},
			},
		},
		arangostore.CollectionSpec{
			Name: semanticinventory.EntryCollection,
			Indexes: [][]string{
				{"project", "dataset_generation", "build_id", "source_kind", "binding_id", "concept_id", "auth_resource_path"},
				{"project", "dataset_generation", "build_id", "source_kind", "resource_type", "binding_id", "concept_id", "auth_resource_path"},
			},
		},
		arangostore.CollectionSpec{
			Name: semanticinventory.BuildCollection,
			Indexes: [][]string{
				{"project", "dataset_generation"},
			},
		},
		arangostore.CollectionSpec{
			Name: catalog.ResourceInventoryCollection,
			Indexes: [][]string{
				{"project", "dataset_generation", "record_type", "resource_type", "auth_resource_path"},
				{"project", "dataset_generation", "build_id", "record_type"},
			},
		},
	)
	return arangostore.BootstrapSpec{
		Collections: collections,
		Reporter: func(event string, fields map[string]any) {
			EmitEvent(reporter, event, fields)
		},
	}
}

func LifecycleBootstrapSpecWithReporter(reporter EventSink) arangostore.BootstrapSpec {
	return arangostore.BootstrapSpec{
		Collections: publicationarango.CollectionSpecs(),
		Reporter: func(event string, fields map[string]any) {
			EmitEvent(reporter, event, fields)
		},
	}
}

func InsertRawDocuments(ctx context.Context, backend *arangostore.Client, collection string, docs []json.RawMessage, overwrite bool, writeAPI string) error {
	return backend.InsertBatchRaw(ctx, collection, docs, overwrite, writeAPI)
}
