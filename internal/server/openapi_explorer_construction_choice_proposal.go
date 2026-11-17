package server

import (
	"context"
	"time"

	loomapi "github.com/calypr/loom/generated/loomapi"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func (h *explorerHTTPHandlers) proposeConstructionChoiceDirect(ctx context.Context, project, explorerID string, body *loomapi.ConstructionChoiceProposalRequest) (loomapi.ConstructionChoiceProposalResponse, error) {
	var result loomapi.ConstructionChoiceProposalResponse
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("construction-choice-proposal", nil)
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
	value, err := h.application.ProposeConstructionChoice(ctx, request)
	if err != nil {
		return result, err
	}
	result, err = directAuthoringJSON[loomapi.ConstructionChoiceProposalResponse](value)
	if err != nil {
		return result, err
	}
	if value.PreviewReceiptID == "" || value.CandidateWorkspaceDigest == "" || len(value.CandidateColumnIDs) != len(value.ConstructionChoices) {
		return result, malformedRouteError("construction-choice-proposal", nil)
	}
	limit := dataframeexecution.DefaultPreviewLimit
	if body.Limit != nil {
		limit = *body.Limit
	}
	started := time.Now()
	preview, err := h.previewAuthoringDirect(ctx, project, explorerID, &loomapi.PreviewExplorerJSONRequestBody{
		ReceiptId: value.PreviewReceiptID, OutputId: body.OutputId, Limit: &limit,
	})
	result.PreviewDurationMs = time.Since(started).Milliseconds()
	if err != nil {
		return result, err
	}
	result.Preview = preview
	result.PreviewStatus = loomapi.ConstructionChoiceProposalResponsePreviewStatusREADY
	return result, nil
}

func (r *HTTPRoutes) ProposeExplorerConstructionChoice(ctx context.Context, request loomapi.ProposeExplorerConstructionChoiceRequestObject) (loomapi.ProposeExplorerConstructionChoiceResponseObject, error) {
	value, err := r.explorer.proposeConstructionChoiceDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.ProposeExplorerConstructionChoice200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerConstructionChoice", err)
	return loomapi.NewProposeExplorerConstructionChoiceErrorResponse(status, failure)
}
