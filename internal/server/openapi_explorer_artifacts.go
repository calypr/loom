package server

import (
	"context"
	"errors"
	"mime"
	"net/http"

	loomapi "github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func (h *explorerHTTPHandlers) prepareArtifactDirect(ctx context.Context, project, explorerID string, body *loomapi.PrepareExplorerArtifactJSONRequestBody) (loomapi.Artifact, error) {
	var response loomapi.Artifact
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return response, err
	}
	if h == nil || h.application == nil {
		return response, explorer.ExplorerUnavailable("artifact", "ARTIFACT_UNAVAILABLE", "artifact export is not configured")
	}
	if body == nil {
		return response, explorer.MalformedRouteError("artifact", errors.New("request body is required"))
	}
	result, err := h.application.PrepareArtifact(ctx, lifecycle.ArtifactRequest{
		Project: project, ExplorerID: explorerID, RevisionID: body.RevisionId,
		OutputID: body.OutputId, IdempotencyKey: body.IdempotencyKey,
	})
	if err != nil {
		return response, err
	}
	if result.Record == nil || result.Record.State != explorer.ArtifactComplete || result.Record.CompletedAt == nil {
		return response, explorer.ExplorerUnavailable("artifact", "ARTIFACT_NOT_COMPLETE", "artifact preparation did not produce a complete archive")
	}
	return directAuthoringJSON[loomapi.Artifact](*result.Record)
}

func (h *explorerHTTPHandlers) openArtifactDirect(ctx context.Context, project, explorerID, artifactID string) (loomapi.DownloadExplorerArtifact200ApplicationzipResponse, error) {
	var response loomapi.DownloadExplorerArtifact200ApplicationzipResponse
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return response, err
	}
	if h == nil || h.application == nil {
		return response, explorer.ExplorerUnavailable("artifact", "ARTIFACT_UNAVAILABLE", "artifact export is not configured")
	}
	reader, record, err := h.application.OpenArtifact(ctx, lifecycle.ArtifactOpenRequest{Project: project, ExplorerID: explorerID, ArtifactID: artifactID})
	if err != nil {
		return response, err
	}
	if reader == nil || record == nil || record.State != explorer.ArtifactComplete {
		if reader != nil {
			_ = reader.Close()
		}
		return response, explorer.ExplorerUnavailable("artifact", "ARTIFACT_NOT_COMPLETE", "artifact is not complete")
	}
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": record.Filename})
	digest := record.ArchiveSHA256
	return loomapi.DownloadExplorerArtifact200ApplicationzipResponse{
		Body: reader, ContentLength: record.Bytes,
		Headers: loomapi.DownloadExplorerArtifact200ResponseHeaders{ContentDisposition: &disposition, XLoomArtifactSHA256: &digest},
	}, nil
}

func (r *HTTPRoutes) PrepareExplorerArtifact(ctx context.Context, request loomapi.PrepareExplorerArtifactRequestObject) (loomapi.PrepareExplorerArtifactResponseObject, error) {
	value, err := r.explorer.prepareArtifactDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.PrepareExplorerArtifact200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "prepareExplorerArtifact", err)
	return loomapi.NewPrepareExplorerArtifactErrorResponse(status, failure)
}

func (r *HTTPRoutes) DownloadExplorerArtifact(ctx context.Context, request loomapi.DownloadExplorerArtifactRequestObject) (loomapi.DownloadExplorerArtifactResponseObject, error) {
	value, err := r.explorer.openArtifactDirect(ctx, string(request.Project), string(request.ExplorerId), request.ArtifactId)
	if err == nil {
		return loomapi.DownloadExplorerArtifact200ApplicationzipResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "downloadExplorerArtifact", err)
	var lifecycleErr *lifecycle.Error
	if errors.As(err, &lifecycleErr) && lifecycleErr.Code == "ARTIFACT_EXPIRED" {
		status = http.StatusGone
	}
	return loomapi.NewDownloadExplorerArtifactErrorResponse(status, failure)
}
