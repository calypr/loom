package explorerapi

import (
	"context"

	loomapi "github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/explorer/constructioninputs"
)

func (r *Handlers) GetExplorerConstructionInputs(ctx context.Context, request loomapi.GetExplorerConstructionInputsRequestObject) (loomapi.GetExplorerConstructionInputsResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.ConstructionInputsRequest) (loomapi.ConstructionInputsResponse, error) {
		var result loomapi.ConstructionInputsResponse
		if body == nil {
			_, err := h.readConstructionInputs(ctx, project, explorerID, nil)
			return result, err
		}
		request := &constructioninputs.Request{
			SnapshotToken: body.SnapshotToken, ExpectedDraftVersion: int64(body.ExpectedDraftVersion),
			ExpectedDraftDigest: body.ExpectedDraftDigest,
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
		value, err := h.readConstructionInputs(ctx, project, explorerID, request)
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.ConstructionInputsResponse](value)
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.GetExplorerConstructionInputs200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "getExplorerConstructionInputs", err)
	return loomapi.NewGetExplorerConstructionInputsErrorResponse(status, failure)
}
