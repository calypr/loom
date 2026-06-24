package ingest

import (
	"context"
	"encoding/json"

	arangostore "github.com/calypr/loom/internal/store/arango"
)

const EdgeCollection = "fhir_edge"
const PatientFileRollupCollection = "patient_file_rollup"
const ScalarIndexCollection = "fhir_scalar_index"

func openBackend(ctx context.Context, opts arangostore.ConnectionOptions) (*arangostore.Client, error) {
	return arangostore.Open(ctx, opts.URL, opts.Database)
}

func bootstrapSpecWithReporter(resourceTypes []string, truncate bool, reporter EventSink) arangostore.BootstrapSpec {
	collections := make([]arangostore.CollectionSpec, 0, len(resourceTypes)+3)
	for _, name := range resourceTypes {
		indexes := [][]string{{"project"}, {"id"}, {"project", "id"}, {"project", "auth_resource_path"}}
		if name == "Patient" {
			indexes = append(indexes, []string{"project", "_key"}, []string{"project", "auth_resource_path", "_key"})
		}
		if name == "DocumentReference" {
			indexes = append(indexes, []string{"project", "auth_resource_path", "_key"})
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
			},
		},
		arangostore.CollectionSpec{
			Name:     PatientFileRollupCollection,
			Truncate: truncate,
			Indexes: [][]string{
				{"project", "patient_key"},
				{"project", "auth_resource_path", "patient_key"},
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

func insertRawDocuments(ctx context.Context, backend *arangostore.Client, collection string, docs []json.RawMessage, overwrite bool, writeAPI string) error {
	return backend.InsertBatchRaw(ctx, collection, docs, overwrite, writeAPI)
}
