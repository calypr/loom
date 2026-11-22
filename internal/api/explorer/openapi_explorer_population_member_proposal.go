package explorerapi

import (
	"context"
	"time"

	loomapi "github.com/calypr/loom/api/loomapi"
	executionarango "github.com/calypr/loom/internal/dataframe/execution/arango"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func (r *Handlers) ProposeExplorerPopulationMemberRemoval(ctx context.Context, request loomapi.ProposeExplorerPopulationMemberRemovalRequestObject) (loomapi.ProposeExplorerPopulationMemberRemovalResponseObject, error) {
	if r.application == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerPopulationMemberRemoval", explorer.ExplorerUnavailable("population-member-proposal", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.ProposeExplorerPopulationMemberRemoval503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.PopulationMemberRemovalProposalRequest) (loomapi.PopulationMemberRemovalProposalResponse, error) {
		var result loomapi.PopulationMemberRemovalProposalResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("population-member-proposal", nil)
		}
		removedMember, err := directAuthoringJSON[explorer.ResourceRef](body.RemovedMember)
		if err != nil {
			return result, explorer.MalformedRouteError("population-member-proposal", err)
		}
		request := lifecycle.PopulationMemberRemovalProposalRequest{
			Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
			ExpectedDraftVersion: body.ExpectedDraftVersion, ExpectedDraftDigest: body.ExpectedDraftDigest,
			OutputID: body.OutputId, BaseSelectionID: body.BaseSelectionRevisionId, RemovedMember: removedMember,
		}
		if body.Limit != nil {
			request.Limit = *body.Limit
		}
		var proposalID string
		options, encoder := h.proposalPreviewOptions(project, body.OutputId, body.Limit, executionarango.ExplorerPreviewTimeout, false)
		run, err := h.application.ProposePopulationMemberRemovalAndPreview(ctx, request, options, func(value lifecycle.PopulationMemberRemovalProposalResponse) error {
			if value.ProposalID == "" || value.BaseSelection == nil || value.CandidateSelection == nil || value.PreviewStatus != "PREVIEW_PENDING" {
				return explorer.MalformedRouteError("population-member-proposal", nil)
			}
			proposalID = value.ProposalID
			var err error
			result, err = directAuthoringJSON[loomapi.PopulationMemberRemovalProposalResponse](value)
			return err
		})
		if err != nil {
			return result, proposalPreviewFailure(err)
		}
		preview, err := encoder.response(run)
		result.PreviewDurationMs = time.Since(run.Started).Milliseconds()
		if err != nil {
			return result, err
		}
		if preview.ReceiptId != proposalID || preview.OutputId != body.OutputId {
			return result, explorer.MalformedRouteError("population-member-proposal", nil)
		}
		result.Preview = preview
		result.PreviewStatus = loomapi.PopulationMemberRemovalProposalResponsePreviewStatusREADY
		return result, nil
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.ProposeExplorerPopulationMemberRemoval200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerPopulationMemberRemoval", err)
	return loomapi.NewProposeExplorerPopulationMemberRemovalErrorResponse(status, failure)
}
