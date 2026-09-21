package server

import (
	"context"
	"errors"
	"net/http"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func (h *explorerHTTPHandlers) browseFeatureCatalogDirect(ctx context.Context, project, explorerID string, body *loomapi.BrowseExplorerFeatureCatalogJSONRequestBody) (loomapi.FeatureCatalogBrowseResponse, error) {
	var result loomapi.FeatureCatalogBrowseResponse
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("feature-catalog", errors.New("request body is required"))
	}
	request := lifecycle.BrowseFeatureCatalogRequest{
		Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
		RowRoot: body.RowRoot, Section: lifecycle.FeatureCatalogSection(body.Section),
	}
	if body.ResourceType != nil {
		request.ResourceType = *body.ResourceType
	}
	if body.NodeId != nil {
		request.NodeID = *body.NodeId
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
	value, err := h.application.BrowseFeatureCatalog(ctx, request)
	if err != nil {
		return result, err
	}
	return directAuthoringJSON[loomapi.FeatureCatalogBrowseResponse](value)
}

func (r *HTTPRoutes) BrowseExplorerFeatureCatalog(ctx context.Context, request loomapi.BrowseExplorerFeatureCatalogRequestObject) (loomapi.BrowseExplorerFeatureCatalogResponseObject, error) {
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "browseExplorerFeatureCatalog", explorerUnavailable("feature-catalog", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.BrowseExplorerFeatureCatalog503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.browseFeatureCatalogDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.BrowseExplorerFeatureCatalog200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "browseExplorerFeatureCatalog", err)
	switch status {
	case http.StatusBadRequest:
		return loomapi.BrowseExplorerFeatureCatalog400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case http.StatusForbidden:
		return loomapi.BrowseExplorerFeatureCatalog403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.BrowseExplorerFeatureCatalog409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.BrowseExplorerFeatureCatalog500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.BrowseExplorerFeatureCatalog503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("browseExplorerFeatureCatalog", status)
	}
}
