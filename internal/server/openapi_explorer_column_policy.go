package server

import (
	"context"
	loomapi "github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func (h *explorerHTTPHandlers) proposeColumnPolicyDirect(ctx context.Context, project, explorerID string, body *loomapi.ColumnPolicyProposalRequest) (loomapi.ColumnPolicyProposal, error) {
	var empty loomapi.ColumnPolicyProposal
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return empty, err
	}
	if body == nil {
		return empty, malformedRouteError("proposeExplorerColumnPolicy", nil)
	}
	input, err := directAuthoringJSON[lifecycle.ColumnPolicyProposalRequest](*body)
	if err != nil {
		return empty, malformedRouteError("proposeExplorerColumnPolicy", err)
	}
	input.Project, input.ExplorerID = project, explorerID
	value, err := h.application.ProposeColumnPolicy(ctx, input)
	if err != nil {
		return empty, err
	}
	return directAuthoringJSON[loomapi.ColumnPolicyProposal](value)
}

func (r *HTTPRoutes) ProposeExplorerColumnPolicy(ctx context.Context, request loomapi.ProposeExplorerColumnPolicyRequestObject) (loomapi.ProposeExplorerColumnPolicyResponseObject, error) {
	value, err := r.explorer.proposeColumnPolicyDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.ProposeExplorerColumnPolicy200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerColumnPolicy", err)
	return loomapi.NewProposeExplorerColumnPolicyErrorResponse(status, failure)
}
