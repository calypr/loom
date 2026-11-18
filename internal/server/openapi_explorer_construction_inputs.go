package server

import (
	"context"

	loomapi "github.com/calypr/loom/generated/loomapi"
)

func (h *explorerHTTPHandlers) getConstructionInputsOpenAPIDirect(ctx context.Context, project, explorerID string, body *loomapi.ConstructionInputsRequest) (loomapi.ConstructionInputsResponse, error) {
	var result loomapi.ConstructionInputsResponse
	if body == nil {
		_, err := h.getConstructionInputsDirect(ctx, project, explorerID, nil)
		return result, err
	}
	request := &constructionInputsRequest{
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
	value, err := h.getConstructionInputsDirect(ctx, project, explorerID, request)
	if err != nil {
		return result, err
	}
	return directAuthoringJSON[loomapi.ConstructionInputsResponse](value)
}

func (r *HTTPRoutes) GetExplorerConstructionInputs(ctx context.Context, request loomapi.GetExplorerConstructionInputsRequestObject) (loomapi.GetExplorerConstructionInputsResponseObject, error) {
	value, err := r.explorer.getConstructionInputsOpenAPIDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.GetExplorerConstructionInputs200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "getExplorerConstructionInputs", err)
	return loomapi.NewGetExplorerConstructionInputsErrorResponse(status, failure)
}
