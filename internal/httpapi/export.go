package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataset"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

type RawExporter interface {
	ExportRaw(context.Context, string, string, authscope.ReadScope, io.Writer) error
}

type QueryRowsClient interface {
	QueryRows(context.Context, string, int, map[string]interface{}, arangostore.RowVisitor) error
}

type ArangoRawExporter struct {
	Query     QueryRowsClient
	Manifests interface {
		ReadManifest(context.Context, dataset.DatasetRef) (dataset.Manifest, error)
	}
}

func (e ArangoRawExporter) ExportRaw(ctx context.Context, project, generation string, scope authscope.ReadScope, out io.Writer) error {
	ref, err := dataset.NewDatasetRef(project, generation)
	if err != nil {
		return err
	}
	manifest, err := e.Manifests.ReadManifest(ctx, ref)
	if err != nil {
		return err
	}
	if !manifest.IsReady() {
		return fmt.Errorf("dataset generation %s/%s is not READY", project, generation)
	}
	encoder := json.NewEncoder(out)
	for _, resourceType := range manifest.SchemaIdentity.GeneratedResourceTypes() {
		collection := resourceType
		query := "FOR doc IN @@collection FILTER doc.project == @project AND doc.dataset_generation == @generation AND (@auth_resource_paths_unrestricted == true OR doc.auth_resource_path IN @auth_resource_paths) SORT doc._key RETURN doc.payload"
		if err := e.Query.QueryRows(ctx, query, 500, map[string]interface{}{"@collection": collection, "project": project, "generation": generation, "auth_resource_paths_unrestricted": scope.Unrestricted(), "auth_resource_paths": scope.AuthResourcePaths}, func(row map[string]any) error {
			return encoder.Encode(row)
		}); err != nil {
			return fmt.Errorf("export %s/%s %s: %w", project, generation, resourceType, err)
		}
	}
	return nil
}
