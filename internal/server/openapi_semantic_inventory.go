package server

import (
	"context"
	"errors"
	"net/http"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func (h *explorerHTTPHandlers) browseSemanticInventoryDirect(ctx context.Context, project, explorerID string, body *loomapi.BrowseExplorerSemanticInventoryJSONRequestBody) (loomapi.SemanticInventoryBrowseResponse, error) {
	var result loomapi.SemanticInventoryBrowseResponse
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("catalog", errors.New("request body is required"))
	}
	req := lifecycle.BrowseSemanticInventoryRequest{Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken, RowRoot: body.RowRoot}
	if body.OutputId != nil {
		req.OutputID = *body.OutputId
	}
	if body.FrameId != nil {
		req.FrameID = *body.FrameId
	}
	if body.SourceChoiceId != nil {
		req.SourceChoiceID = *body.SourceChoiceId
	}
	if body.ResourceType != nil {
		req.ResourceType = *body.ResourceType
	}
	if body.Query != nil {
		req.Query = *body.Query
	}
	if body.Cursor != nil {
		req.Cursor = *body.Cursor
	}
	if body.Limit != nil {
		req.Limit = *body.Limit
	}
	value, err := h.application.BrowseSemanticInventory(ctx, req)
	if err != nil {
		return result, err
	}
	return directAuthoringJSON[loomapi.SemanticInventoryBrowseResponse](value)
}

func (r *HTTPRoutes) BrowseExplorerSemanticInventory(ctx context.Context, request loomapi.BrowseExplorerSemanticInventoryRequestObject) (loomapi.BrowseExplorerSemanticInventoryResponseObject, error) {
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "browseExplorerSemanticInventory", explorerUnavailable("catalog", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.BrowseExplorerSemanticInventory503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.browseSemanticInventoryDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.BrowseExplorerSemanticInventory200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "browseExplorerSemanticInventory", err)
	switch status {
	case http.StatusBadRequest:
		return loomapi.BrowseExplorerSemanticInventory400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case http.StatusForbidden:
		return loomapi.BrowseExplorerSemanticInventory403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.BrowseExplorerSemanticInventory409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.BrowseExplorerSemanticInventory500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.BrowseExplorerSemanticInventory503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("browseExplorerSemanticInventory", status)
	}
}
