package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/calypr/loom/fhirschema"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataset"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

type RawExporter interface {
	ExportRaw(context.Context, string, string, authscope.ReadScope, io.Writer) error
	ExportRawFiltered(context.Context, RawDumpRequest, authscope.ReadScope, io.Writer) error
	ResolveGeneration(context.Context, string, string) (string, error)
}

type RawDumpRequest struct {
	Project      string
	Generation   string
	ResourceType string
	Limit        int
	Legacy       bool
}

type QueryRowsClient interface {
	QueryRows(context.Context, string, int, map[string]interface{}, arangostore.RowVisitor) error
	CollectionExists(context.Context, string) (bool, error)
}

type ArangoRawExporter struct {
	Query     QueryRowsClient
	Manifests interface {
		ReadManifest(context.Context, dataset.DatasetRef) (dataset.Manifest, error)
		ResolveActiveManifest(context.Context, string) (dataset.Manifest, error)
	}
}

func (e ArangoRawExporter) ExportRaw(ctx context.Context, project, generation string, scope authscope.ReadScope, out io.Writer) error {
	return e.ExportRawFiltered(ctx, RawDumpRequest{Project: project, Generation: generation}, scope, out)
}

func (e ArangoRawExporter) ResolveGeneration(ctx context.Context, project, generation string) (string, error) {
	manifest, err := e.resolveManifest(ctx, project, generation)
	if err != nil {
		return "", err
	}
	return manifest.Dataset.Generation, nil
}

func (e ArangoRawExporter) resolveManifest(ctx context.Context, project, generation string) (dataset.Manifest, error) {
	if generation == "" {
		manifest, err := e.Manifests.ResolveActiveManifest(ctx, project)
		if err != nil {
			return dataset.Manifest{}, err
		}
		if !manifest.IsReady() {
			return dataset.Manifest{}, fmt.Errorf("active dataset generation for %s is not READY", project)
		}
		return manifest, nil
	}
	ref, err := dataset.NewDatasetRef(project, generation)
	if err != nil {
		return dataset.Manifest{}, err
	}
	manifest, err := e.Manifests.ReadManifest(ctx, ref)
	if err != nil {
		return dataset.Manifest{}, err
	}
	if !manifest.IsReady() {
		return dataset.Manifest{}, fmt.Errorf("dataset generation %s/%s is not READY", project, generation)
	}
	return manifest, nil
}

func (e ArangoRawExporter) ExportRawFiltered(ctx context.Context, req RawDumpRequest, scope authscope.ReadScope, out io.Writer) error {
	generation := req.Generation
	resourceTypes := fhirschema.ResourceTypes()
	if !req.Legacy {
		manifest, err := e.resolveManifest(ctx, req.Project, req.Generation)
		if err != nil {
			return err
		}
		generation = manifest.Dataset.Generation
		resourceTypes = manifest.SchemaIdentity.GeneratedResourceTypes()
	}
	if req.ResourceType != "" {
		found := false
		for _, resourceType := range resourceTypes {
			if resourceType == req.ResourceType {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("resource type %q is not supported", req.ResourceType)
		}
		resourceTypes = []string{req.ResourceType}
	}

	encoder := json.NewEncoder(out)
	remaining := req.Limit
	for _, resourceType := range resourceTypes {
		collection := resourceType
		exists, err := e.Query.CollectionExists(ctx, collection)
		if err != nil {
			return fmt.Errorf("inspect resource collection %s: %w", collection, err)
		}
		if !exists {
			continue
		}
		query := "FOR doc IN @@collection FILTER doc.project == @project AND doc.dataset_generation == @generation AND (@auth_resource_paths_unrestricted == true OR doc.auth_resource_path IN @auth_resource_paths) SORT doc._key RETURN doc.payload"
		bindVars := map[string]interface{}{"@collection": collection, "project": req.Project, "generation": nil, "auth_resource_paths_unrestricted": scope.Unrestricted(), "auth_resource_paths": scope.AuthResourcePaths}
		if !req.Legacy {
			bindVars["generation"] = generation
		}
		if remaining > 0 {
			query = "FOR doc IN @@collection FILTER doc.project == @project AND doc.dataset_generation == @generation AND (@auth_resource_paths_unrestricted == true OR doc.auth_resource_path IN @auth_resource_paths) SORT doc._key LIMIT @limit RETURN doc.payload"
			bindVars["limit"] = remaining
		}
		written := 0
		if err := e.Query.QueryRows(ctx, query, 500, bindVars, func(row map[string]any) error {
			written++
			return encoder.Encode(row)
		}); err != nil {
			return fmt.Errorf("export %s/%s %s: %w", req.Project, generation, resourceType, err)
		}
		if remaining > 0 {
			remaining -= written
			if remaining == 0 {
				break
			}
		}
	}
	return nil
}
