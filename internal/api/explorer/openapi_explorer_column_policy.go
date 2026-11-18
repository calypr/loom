package explorerapi

import (
	"context"

	loomapi "github.com/calypr/loom/api/loomapi"
	explorer "github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func (r *Handlers) ProposeExplorerColumnPolicy(ctx context.Context, request loomapi.ProposeExplorerColumnPolicyRequestObject) (loomapi.ProposeExplorerColumnPolicyResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.ColumnPolicyProposalRequest) (loomapi.ColumnPolicyProposal, error) {
		var empty loomapi.ColumnPolicyProposal
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return empty, err
		}
		if body == nil {
			return empty, explorer.MalformedRouteError("proposeExplorerColumnPolicy", nil)
		}
		input, err := directAuthoringJSON[lifecycle.ColumnPolicyProposalRequest](*body)
		if err != nil {
			return empty, explorer.MalformedRouteError("proposeExplorerColumnPolicy", err)
		}
		input.Project, input.ExplorerID = project, explorerID
		value, err := h.application.ProposeColumnPolicy(ctx, input)
		if err != nil {
			return empty, err
		}
		return directAuthoringJSON[loomapi.ColumnPolicyProposal](value)
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.ProposeExplorerColumnPolicy200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerColumnPolicy", err)
	return loomapi.NewProposeExplorerColumnPolicyErrorResponse(status, failure)
}
