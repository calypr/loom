package dump

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	publication "github.com/calypr/loom/internal/dataset"
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
}

type QueryRowsClient interface {
	QueryRows(context.Context, string, int, map[string]interface{}, arangostore.RowVisitor) error
	CollectionExists(context.Context, string) (bool, error)
}

type ArangoRawExporter struct {
	Query     QueryRowsClient
	Manifests interface {
		ReadManifest(context.Context, publication.Ref) (publication.Manifest, error)
		ResolveActiveManifest(context.Context, string) (publication.Manifest, error)
	}
}

func (e ArangoRawExporter) ExportRaw(ctx context.Context, project, generationID string, scope authscope.ReadScope, out io.Writer) error {
	return e.ExportRawFiltered(ctx, RawDumpRequest{Project: project, Generation: generationID}, scope, out)
}

func (e ArangoRawExporter) ResolveGeneration(ctx context.Context, project, generationID string) (string, error) {
	manifest, err := e.resolveManifest(ctx, project, generationID)
	if err != nil {
		return "", err
	}
	return manifest.Dataset.Generation, nil
}

func (e ArangoRawExporter) resolveManifest(ctx context.Context, project, generationID string) (publication.Manifest, error) {
	if generationID == "" {
		manifest, err := e.Manifests.ResolveActiveManifest(ctx, project)
		if err != nil {
			return publication.Manifest{}, classifyRawExportError(err)
		}
		if !manifest.IsStaged() {
			return publication.Manifest{}, dataframeerrors.NewError(dataframeerrors.CodeNoActiveGeneration, "")
		}
		return manifest, nil
	}
	ref, err := publication.NewRef(project, generationID)
	if err != nil {
		return publication.Manifest{}, dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidRequest, "")
	}
	manifest, err := e.Manifests.ReadManifest(ctx, ref)
	if err != nil {
		return publication.Manifest{}, classifyRawExportError(err)
	}
	if !manifest.IsStaged() {
		return publication.Manifest{}, dataframeerrors.NewError(dataframeerrors.CodeNoActiveGeneration, "")
	}
	return manifest, nil
}

func (e ArangoRawExporter) ExportRawFiltered(ctx context.Context, req RawDumpRequest, scope authscope.ReadScope, out io.Writer) error {
	generation := req.Generation
	manifest, err := e.resolveManifest(ctx, req.Project, req.Generation)
	if err != nil {
		return err
	}
	generation = manifest.Dataset.Generation
	resourceTypes := manifest.SchemaIdentity.GeneratedResourceTypes
	if req.ResourceType != "" {
		found := false
		for _, resourceType := range resourceTypes {
			if resourceType == req.ResourceType {
				found = true
				break
			}
		}
		if !found {
			return dataframeerrors.NewError(dataframeerrors.CodeInvalidResourceType, "")
		}
		resourceTypes = []string{req.ResourceType}
	}

	encoder := json.NewEncoder(out)
	remaining := req.Limit
	for _, resourceType := range resourceTypes {
		collection := resourceType
		exists, err := e.Query.CollectionExists(ctx, collection)
		if err != nil {
			return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
		}
		if !exists {
			continue
		}
		query := "FOR doc IN @@collection FILTER doc.project == @project AND doc.dataset_generation == @generation AND (@auth_resource_paths_unrestricted == true OR doc.auth_resource_path IN @auth_resource_paths) SORT doc._key RETURN doc.payload"
		bindVars := map[string]interface{}{"@collection": collection, "project": req.Project, "generation": generation, "auth_resource_paths_unrestricted": scope.Unrestricted(), "auth_resource_paths": scope.AuthResourcePaths}
		if remaining > 0 {
			query = "FOR doc IN @@collection FILTER doc.project == @project AND doc.dataset_generation == @generation AND (@auth_resource_paths_unrestricted == true OR doc.auth_resource_path IN @auth_resource_paths) SORT doc._key LIMIT @limit RETURN doc.payload"
			bindVars["limit"] = remaining
		}
		written := 0
		if err := e.Query.QueryRows(ctx, query, 500, bindVars, func(row map[string]any) error {
			written++
			return encoder.Encode(row)
		}); err != nil {
			return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
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

func classifyRawExportError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := dataframeerrors.AsUserError(err); ok {
		return err
	}
	if errors.Is(err, publication.ErrNoActiveGeneration) {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeNoActiveGeneration, "")
	}
	return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
}
