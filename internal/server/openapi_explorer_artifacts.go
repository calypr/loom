package server

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func (h *explorerHTTPHandlers) prepareArtifactDirect(ctx context.Context, project, explorerID string, body *loomapi.PrepareExplorerArtifactJSONRequestBody) (loomapi.Artifact, error) {
	var response loomapi.Artifact
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return response, err
	}
	if h == nil || h.application == nil {
		return response, explorerUnavailable("artifact", "ARTIFACT_UNAVAILABLE", "artifact export is not configured")
	}
	if body == nil {
		return response, malformedRouteError("artifact", errors.New("request body is required"))
	}
	result, err := h.application.PrepareArtifact(ctx, lifecycle.ArtifactRequest{
		Project: project, ExplorerID: explorerID, RevisionID: body.RevisionId,
		OutputID: body.OutputId, IdempotencyKey: body.IdempotencyKey,
	})
	if err != nil {
		return response, err
	}
	if result.Record == nil || result.Record.State != explorer.ArtifactComplete || result.Record.CompletedAt == nil {
		return response, explorerUnavailable("artifact", "ARTIFACT_NOT_COMPLETE", "artifact preparation did not produce a complete archive")
	}
	return directAuthoringJSON[loomapi.Artifact](*result.Record)
}

func (h *explorerHTTPHandlers) openArtifactDirect(ctx context.Context, project, explorerID, artifactID string) (loomapi.DownloadExplorerArtifact200ApplicationzipResponse, error) {
	var response loomapi.DownloadExplorerArtifact200ApplicationzipResponse
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return response, err
	}
	if h == nil || h.application == nil {
		return response, explorerUnavailable("artifact", "ARTIFACT_UNAVAILABLE", "artifact export is not configured")
	}
	reader, record, err := h.application.OpenArtifact(ctx, lifecycle.ArtifactOpenRequest{Project: project, ExplorerID: explorerID, ArtifactID: artifactID})
	if err != nil {
		return response, err
	}
	if reader == nil || record == nil || record.State != explorer.ArtifactComplete {
		if reader != nil {
			_ = reader.Close()
		}
		return response, explorerUnavailable("artifact", "ARTIFACT_NOT_COMPLETE", "artifact is not complete")
	}
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": record.Filename})
	digest := record.ArchiveSHA256
	return loomapi.DownloadExplorerArtifact200ApplicationzipResponse{
		Body: reader, ContentLength: record.Bytes,
		Headers: loomapi.DownloadExplorerArtifact200ResponseHeaders{ContentDisposition: &disposition, XLoomArtifactSHA256: &digest},
	}, nil
}

func (r *HTTPRoutes) PrepareExplorerArtifact(ctx context.Context, request loomapi.PrepareExplorerArtifactRequestObject) (loomapi.PrepareExplorerArtifactResponseObject, error) {
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "prepareExplorerArtifact", explorerUnavailable("artifact", "ARTIFACT_UNAVAILABLE", "artifact export is not configured"))
		return loomapi.PrepareExplorerArtifact503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.prepareArtifactDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.PrepareExplorerArtifact200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "prepareExplorerArtifact", err)
	switch status {
	case http.StatusUnauthorized:
		return loomapi.PrepareExplorerArtifact401JSONResponse{ServiceUnauthorizedJSONResponse: authoringUnauthorizedResponse(failure)}, nil
	case http.StatusBadRequest:
		return loomapi.PrepareExplorerArtifact400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case http.StatusForbidden:
		return loomapi.PrepareExplorerArtifact403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusNotFound:
		return loomapi.PrepareExplorerArtifact404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.PrepareExplorerArtifact409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusRequestEntityTooLarge:
		return loomapi.PrepareExplorerArtifact413JSONResponse{AuthoringPayloadTooLargeJSONResponse: loomapi.AuthoringPayloadTooLargeJSONResponse(failure)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.PrepareExplorerArtifact422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.PrepareExplorerArtifact500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.PrepareExplorerArtifact503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	case http.StatusGatewayTimeout:
		return loomapi.PrepareExplorerArtifact504JSONResponse{AuthoringGatewayTimeoutJSONResponse: loomapi.AuthoringGatewayTimeoutJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("prepareExplorerArtifact", status)
	}
}

func (r *HTTPRoutes) DownloadExplorerArtifact(ctx context.Context, request loomapi.DownloadExplorerArtifactRequestObject) (loomapi.DownloadExplorerArtifactResponseObject, error) {
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "downloadExplorerArtifact", explorerUnavailable("artifact", "ARTIFACT_UNAVAILABLE", "artifact export is not configured"))
		return loomapi.DownloadExplorerArtifact503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.openArtifactDirect(ctx, string(request.Project), string(request.ExplorerId), request.ArtifactId)
	if err == nil {
		return loomapi.DownloadExplorerArtifact200ApplicationzipResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "downloadExplorerArtifact", err)
	var lifecycleErr *lifecycle.Error
	if errors.As(err, &lifecycleErr) && lifecycleErr.Code == "ARTIFACT_EXPIRED" {
		status = http.StatusGone
	}
	switch status {
	case http.StatusUnauthorized:
		return loomapi.DownloadExplorerArtifact401JSONResponse{ServiceUnauthorizedJSONResponse: authoringUnauthorizedResponse(failure)}, nil
	case http.StatusForbidden:
		return loomapi.DownloadExplorerArtifact403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusNotFound:
		return loomapi.DownloadExplorerArtifact404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.DownloadExplorerArtifact409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusGone:
		return loomapi.DownloadExplorerArtifact410JSONResponse(failure), nil
	case http.StatusInternalServerError:
		return loomapi.DownloadExplorerArtifact500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.DownloadExplorerArtifact503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	default:
		return nil, fmt.Errorf("downloadExplorerArtifact returned undocumented HTTP status %d", status)
	}
}
