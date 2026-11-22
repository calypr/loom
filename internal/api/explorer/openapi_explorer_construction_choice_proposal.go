package explorerapi

import (
	"context"
	"time"

	loomapi "github.com/calypr/loom/api/loomapi"
	executionarango "github.com/calypr/loom/internal/dataframe/execution/arango"
	explorer "github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func (r *Handlers) ProposeExplorerConstructionChoice(ctx context.Context, request loomapi.ProposeExplorerConstructionChoiceRequestObject) (loomapi.ProposeExplorerConstructionChoiceResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.ConstructionChoiceProposalRequest) (loomapi.ConstructionChoiceProposalResponse, error) {
		var result loomapi.ConstructionChoiceProposalResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("construction-choice-proposal", nil)
		}
		choices := make([]lifecycle.ConstructionChoiceProposalSelection, len(body.ConstructionChoices))
		for index, choice := range body.ConstructionChoices {
			choices[index] = lifecycle.ConstructionChoiceProposalSelection{
				ChoiceID: choice.ChoiceId, Form: capability.ConstructionChoiceForm(choice.Form),
			}
			if choice.RowValuePolicy != nil {
				choices[index].RowValuePolicy = authoringv2.ConstructionRowValuePolicy(*choice.RowValuePolicy)
			}
			if choice.FrameId != nil {
				choices[index].FrameID = *choice.FrameId
			}
			if choice.Title != nil {
				choices[index].Title = choice.Title
			}
		}
		request := lifecycle.ConstructionChoiceProposalRequest{
			CommandID: body.CommandId, Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
			ExpectedDraftVersion: int64(body.ExpectedDraftVersion), ExpectedDraftDigest: body.ExpectedDraftDigest,
			OutputID: body.OutputId, ConstructionChoices: choices,
		}
		if body.Limit != nil {
			request.Limit = *body.Limit
		}
		options, encoder := h.proposalPreviewOptions(project, body.OutputId, body.Limit, executionarango.ExplorerPreviewTimeout, false)
		run, err := h.application.ProposeConstructionChoiceAndPreview(ctx, request, options, func(value lifecycle.ConstructionChoiceProposalResponse) error {
			var err error
			result, err = directAuthoringJSON[loomapi.ConstructionChoiceProposalResponse](value)
			if err != nil {
				return err
			}
			if value.PreviewReceiptID == "" || value.CandidateWorkspaceDigest == "" || len(value.CandidateColumnIDs) != len(value.ConstructionChoices) {
				return explorer.MalformedRouteError("construction-choice-proposal", nil)
			}
			return nil
		})
		if err != nil {
			return result, proposalPreviewFailure(err)
		}
		preview, err := encoder.response(run)
		result.PreviewDurationMs = time.Since(run.Started).Milliseconds()
		if err != nil {
			return result, err
		}
		result.Preview = preview
		result.PreviewStatus = loomapi.ConstructionChoiceProposalResponsePreviewStatusREADY
		return result, nil
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.ProposeExplorerConstructionChoice200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerConstructionChoice", err)
	return loomapi.NewProposeExplorerConstructionChoiceErrorResponse(status, failure)
}
