package server

import (
	"context"

	explorer "github.com/calypr/loom/internal/explorer"

	loomapi "github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func (h *explorerHTTPHandlers) describeAuthoringNetworkDirect(ctx context.Context, project, explorerID string, body *loomapi.AuthoringNetworkRequest) (loomapi.AuthoringNetworkResponse, error) {
	var empty loomapi.AuthoringNetworkResponse
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return empty, err
	}
	if body == nil {
		return empty, explorer.MalformedRouteError("describeExplorerAuthoringNetwork", nil)
	}
	input, err := directAuthoringJSON[lifecycle.AuthoringNetworkRequest](*body)
	if err != nil {
		return empty, explorer.MalformedRouteError("describeExplorerAuthoringNetwork", err)
	}
	input.Project, input.ExplorerID = project, explorerID
	value, err := h.application.DescribeAuthoringNetwork(ctx, input)
	if err != nil {
		return empty, err
	}
	return directAuthoringJSON[loomapi.AuthoringNetworkResponse](value)
}

func (r *HTTPRoutes) DescribeExplorerAuthoringNetwork(ctx context.Context, request loomapi.DescribeExplorerAuthoringNetworkRequestObject) (loomapi.DescribeExplorerAuthoringNetworkResponseObject, error) {
	value, err := r.explorer.describeAuthoringNetworkDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.DescribeExplorerAuthoringNetwork200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "describeExplorerAuthoringNetwork", err)
	return loomapi.NewDescribeExplorerAuthoringNetworkErrorResponse(status, failure)
}
