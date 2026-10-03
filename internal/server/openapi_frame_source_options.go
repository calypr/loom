package server

import (
	"context"
	"net/http"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func (h *explorerHTTPHandlers) browseFrameSourceOptionsDirect(ctx context.Context, project, explorerID string, body *loomapi.BrowseExplorerFrameSourceOptionsJSONRequestBody) (loomapi.FrameSourceOptionsResponse, error) {
	var result loomapi.FrameSourceOptionsResponse
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("frame-source-options", nil)
	}
	request := lifecycle.BrowseFrameSourceOptionsRequest{
		Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken, OutputID: body.OutputId,
	}
	if body.ResourceType != nil {
		request.ResourceType = *body.ResourceType
	}
	if body.Query != nil {
		request.Query = *body.Query
	}
	if body.Cursor != nil {
		request.Cursor = *body.Cursor
	}
	if body.Limit != nil {
		request.Limit = *body.Limit
	}
	value, err := h.application.BrowseFrameSourceOptions(ctx, request)
	if err != nil {
		return result, err
	}
	return directAuthoringJSON[loomapi.FrameSourceOptionsResponse](value)
}

func (r *HTTPRoutes) BrowseExplorerFrameSourceOptions(ctx context.Context, request loomapi.BrowseExplorerFrameSourceOptionsRequestObject) (loomapi.BrowseExplorerFrameSourceOptionsResponseObject, error) {
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "browseExplorerFrameSourceOptions", explorerUnavailable("frame-source-options", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.BrowseExplorerFrameSourceOptions503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.browseFrameSourceOptionsDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.BrowseExplorerFrameSourceOptions200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "browseExplorerFrameSourceOptions", err)
	switch status {
	case http.StatusBadRequest:
		return loomapi.BrowseExplorerFrameSourceOptions400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case http.StatusForbidden:
		return loomapi.BrowseExplorerFrameSourceOptions403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusNotFound:
		return loomapi.BrowseExplorerFrameSourceOptions404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.BrowseExplorerFrameSourceOptions409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.BrowseExplorerFrameSourceOptions422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.BrowseExplorerFrameSourceOptions500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.BrowseExplorerFrameSourceOptions503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("browseExplorerFrameSourceOptions", status)
	}
}
