package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/calypr/loom/internal/dataset"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

type RawExporter interface {
	ExportRaw(context.Context, string, string, io.Writer) error
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

func (e ArangoRawExporter) ExportRaw(ctx context.Context, project, generation string, out io.Writer) error {
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
	authResourcePath := ""
	if parts := strings.SplitN(project, "-", 2); len(parts) == 2 {
		authResourcePath = "/programs/" + parts[0] + "/projects/" + parts[1]
	}
	for _, resourceType := range manifest.SchemaIdentity.GeneratedResourceTypes() {
		collection := resourceType
		query := "FOR doc IN @@collection FILTER doc.project == @project AND doc.dataset_generation == @generation AND (@auth_resource_path == \"\" OR doc.auth_resource_path == @auth_resource_path) SORT doc._key RETURN doc.payload"
		if err := e.Query.QueryRows(ctx, query, 500, map[string]interface{}{"@collection": collection, "project": project, "generation": generation, "auth_resource_path": authResourcePath}, func(row map[string]any) error {
			return encoder.Encode(row)
		}); err != nil {
			return fmt.Errorf("export %s/%s %s: %w", project, generation, resourceType, err)
		}
	}
	return nil
}
