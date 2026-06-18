package proto

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"arangodb-proto/internal/store"
	arangostore "arangodb-proto/internal/store/arango"
	surrealstore "arangodb-proto/internal/store/surreal"
)

const EdgeCollection = "fhir_edge"
const PatientFileRollupCollection = "patient_file_rollup"

const (
	backendArango  = "arango"
	backendSurreal = "surreal"
)

type ConnectionOptions struct {
	Backend   string
	URL       string
	Namespace string
	Database  string
	Username  string
	Password  string
	AuthToken string
}

func backendName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return backendArango
	}
	return name
}

func openBackend(ctx context.Context, opts ConnectionOptions) (store.Backend, error) {
	switch backendName(opts.Backend) {
	case backendArango:
		return arangostore.Open(ctx, opts.URL, opts.Database)
	case backendSurreal:
		return surrealstore.Open(ctx, opts.URL, opts.Namespace, opts.Database, opts.Username, opts.Password, opts.AuthToken)
	default:
		return nil, fmt.Errorf("unsupported backend %q", opts.Backend)
	}
}

func bootstrapSpec(resourceTypes []string, truncate bool) store.BootstrapSpec {
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
			Name:     FieldCatalogCollection,
			Truncate: truncate,
			Indexes: [][]string{
				{"project", "resource_type"},
				{"project", "resource_type", "path"},
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
			Emit(event, fields)
		},
	}
}

func helperBootstrapSpec(collections []store.CollectionSpec, truncate bool) store.BootstrapSpec {
	for i := range collections {
		collections[i].Truncate = truncate
	}
	return store.BootstrapSpec{
		Collections: collections,
		Reporter: func(event string, fields map[string]any) {
			Emit(event, fields)
		},
	}
}

func insertRawDocuments(ctx context.Context, backend store.Backend, collection string, docs []json.RawMessage, overwrite bool, writeAPI string) error {
	return backend.InsertBatchRaw(ctx, collection, docs, overwrite, writeAPI)
}
