package explorerapi

import (
	"context"

	loomapi "github.com/calypr/loom/api/loomapi"
	explorer "github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func (r *Handlers) BrowseExplorerFrameSourceOptions(ctx context.Context, request loomapi.BrowseExplorerFrameSourceOptionsRequestObject) (loomapi.BrowseExplorerFrameSourceOptionsResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.BrowseExplorerFrameSourceOptionsJSONRequestBody) (loomapi.FrameSourceOptionsResponse, error) {
		var result loomapi.FrameSourceOptionsResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("frame-source-options", nil)
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
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.BrowseExplorerFrameSourceOptions200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "browseExplorerFrameSourceOptions", err)
	return loomapi.NewBrowseExplorerFrameSourceOptionsErrorResponse(status, failure)
}
