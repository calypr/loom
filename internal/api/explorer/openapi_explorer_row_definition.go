package explorerapi

import (
	"context"
	"encoding/json"

	loomapi "github.com/calypr/loom/api/loomapi"
	explorer "github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func (r *Handlers) ListExplorerRowDefinitionChoices(ctx context.Context, request loomapi.ListExplorerRowDefinitionChoicesRequestObject) (loomapi.ListExplorerRowDefinitionChoicesResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, params loomapi.ListExplorerRowDefinitionChoicesParams) (loomapi.RowDefinitionChoicesResponse, error) {
		var result loomapi.RowDefinitionChoicesResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		value, err := h.application.ListRowDefinitionChoices(ctx, lifecycle.RowDefinitionChoicesRequest{
			Project: project, ExplorerID: explorerID, OutputID: params.OutputId, SnapshotToken: params.SnapshotToken,
		})
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.RowDefinitionChoicesResponse](value)
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Params)
	if err == nil {
		return loomapi.ListExplorerRowDefinitionChoices200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "listExplorerRowDefinitionChoices", err)
	return loomapi.NewListExplorerRowDefinitionChoicesErrorResponse(status, failure)
}

func (r *Handlers) ProposeExplorerRowDefinition(ctx context.Context, request loomapi.ProposeExplorerRowDefinitionRequestObject) (loomapi.ProposeExplorerRowDefinitionResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.RowDefinitionProposalRequest) (loomapi.RowDefinitionProposal, error) {
		var result loomapi.RowDefinitionProposal
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("row-definition-proposal", nil)
		}
		raw, err := json.Marshal(body)
		if err != nil {
			return result, explorer.MalformedRouteError("row-definition-proposal", err)
		}
		var input struct {
			SnapshotToken        string                           `json:"snapshotToken"`
			ExpectedDraftVersion int64                            `json:"expectedDraftVersion"`
			ExpectedDraftDigest  string                           `json:"expectedDraftDigest"`
			OutputID             string                           `json:"outputId"`
			Selection            lifecycle.RowDefinitionSelection `json:"selection"`
			Limit                int                              `json:"limit,omitempty"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return result, explorer.MalformedRouteError("row-definition-proposal", err)
		}
		value, err := h.application.ProposeRowDefinition(ctx, lifecycle.RowDefinitionProposalRequest{
			Project: project, ExplorerID: explorerID, SnapshotToken: input.SnapshotToken,
			ExpectedDraftVersion: input.ExpectedDraftVersion, ExpectedDraftDigest: input.ExpectedDraftDigest,
			OutputID: input.OutputID, Selection: input.Selection, Limit: input.Limit,
		})
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.RowDefinitionProposal](value)
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.ProposeExplorerRowDefinition200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerRowDefinition", err)
	return loomapi.NewProposeExplorerRowDefinitionErrorResponse(status, failure)
}
