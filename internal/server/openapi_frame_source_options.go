package server

import (
	"context"

	loomapi "github.com/calypr/loom/api/loomapi"
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
	value, err := r.explorer.browseFrameSourceOptionsDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.BrowseExplorerFrameSourceOptions200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "browseExplorerFrameSourceOptions", err)
	return loomapi.NewBrowseExplorerFrameSourceOptionsErrorResponse(status, failure)
}
