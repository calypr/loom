package proto

import (
	"context"
	"encoding/json"

	"arangodb-proto/internal/catalog"
	"arangodb-proto/internal/dbio"
	"arangodb-proto/internal/store"
)

const EdgeCollection = "fhir_edge"
const PatientFileRollupCollection = "patient_file_rollup"
const ScalarIndexCollection = "fhir_scalar_index"

type ConnectionOptions = dbio.ConnectionOptions

const (
	backendArango   = dbio.BackendArango
	backendPostgres = dbio.BackendPostgres
	backendSurreal  = dbio.BackendSurreal
)

func backendName(name string) string { return dbio.BackendName(name) }

func openBackend(ctx context.Context, opts ConnectionOptions) (store.Backend, error) {
	return dbio.OpenBackend(ctx, opts)
}

func bootstrapSpec(resourceTypes []string, truncate bool) store.BootstrapSpec {
	return bootstrapSpecWithReporter(resourceTypes, truncate, nil)
}

func bootstrapSpecWithReporter(resourceTypes []string, truncate bool, reporter EventSink) store.BootstrapSpec {
	collections := make([]store.CollectionSpec, 0, len(resourceTypes)+3)
	for _, name := range resourceTypes {
		indexes := [][]string{{"project"}, {"id"}, {"project", "id"}, {"project", "auth_resource_path"}}
		if name == "Patient" {
			indexes = append(indexes, []string{"project", "_key"}, []string{"project", "auth_resource_path", "_key"})
		}
		if name == "DocumentReference" {
			indexes = append(indexes, []string{"project", "auth_resource_path", "_key"})
		}
		collections = append(collections, store.CollectionSpec{
			Name:     name,
			Truncate: truncate,
			Indexes:  indexes,
		})
	}
	collections = append(collections,
		store.CollectionSpec{
			Name:     EdgeCollection,
			Edge:     true,
			Truncate: truncate,
			Indexes: [][]string{
				{"project", "label"},
				{"project", "from_type", "label"},
				{"project", "to_type", "label"},
			},
		},
		store.CollectionSpec{
			Name:     catalog.FieldCatalogCollection,
			Truncate: truncate,
			Indexes: [][]string{
				{"project", "resource_type"},
				{"project", "auth_resource_path", "resource_type"},
				{"project", "resource_type", "path"},
				{"project", "auth_resource_path", "resource_type", "path"},
				{"project", "resource_type", "pivot_candidate"},
			},
		},
		store.CollectionSpec{
			Name:     PatientFileRollupCollection,
			Truncate: truncate,
			Indexes: [][]string{
				{"project", "patient_key"},
				{"project", "auth_resource_path", "patient_key"},
			},
		},
	)
	return store.BootstrapSpec{
		Collections: collections,
		Reporter: func(event string, fields map[string]any) {
			emitEvent(reporter, event, fields)
		},
	}
}

func helperBootstrapSpec(collections []store.CollectionSpec, truncate bool) store.BootstrapSpec {
	return helperBootstrapSpecWithReporter(collections, truncate, nil)
}

func helperBootstrapSpecWithReporter(collections []store.CollectionSpec, truncate bool, reporter EventSink) store.BootstrapSpec {
	for i := range collections {
		collections[i].Truncate = truncate
	}
	return store.BootstrapSpec{
		Collections: collections,
		Reporter: func(event string, fields map[string]any) {
			emitEvent(reporter, event, fields)
		},
	}
}

func insertRawDocuments(ctx context.Context, backend store.Backend, collection string, docs []json.RawMessage, overwrite bool, writeAPI string) error {
	return backend.InsertBatchRaw(ctx, collection, docs, overwrite, writeAPI)
}
