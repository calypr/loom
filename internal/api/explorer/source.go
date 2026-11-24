package explorerapi

import (
	"context"
	"errors"

	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"

	loomapi "github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/proposal"
	"github.com/calypr/loom/internal/explorer/semanticbrowse"
)

func (r *Handlers) ProposeExplorerSourceRelationship(ctx context.Context, request loomapi.ProposeExplorerSourceRelationshipRequestObject) (loomapi.ProposeExplorerSourceRelationshipResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.SourceRelationshipProposalRequest) (loomapi.SourceRelationshipProposal, error) {
		var empty loomapi.SourceRelationshipProposal
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return empty, err
		}
		if body == nil {
			return empty, explorer.MalformedRouteError("proposeExplorerSourceRelationship", nil)
		}
		input, err := directAuthoringJSON[proposal.SourceRelationshipProposalRequest](*body)
		if err != nil {
			return empty, explorer.MalformedRouteError("proposeExplorerSourceRelationship", err)
		}
		input.Project, input.ExplorerID = project, explorerID
		var encoder *previewResponseEncoder
		if input.IncludePreview {
			input.PreviewSinkFactory = func(receipt *receiptmodel.CompilationReceipt, columns []viewer.EmittedColumn) (func(map[string]any) error, error) {
				var err error
				encoder, err = newPreviewResponseEncoder(receipt, input.OutputID, columns, maxExplorerPreviewResponseBytes)
				if err != nil {
					return nil, err
				}
				return encoder.Visit, nil
			}
		}
		value, err := h.application.ProposeSourceRelationship(ctx, input)
		if err != nil {
			var authoringError *explorer.AuthoringError
			if input.IncludePreview && !errors.As(err, &authoringError) {
				return empty, previewRouteError(err)
			}
			return empty, err
		}
		result, err := directAuthoringJSON[loomapi.SourceRelationshipProposal](value)
		if err != nil {
			return empty, err
		}
		if input.IncludePreview {
			if value.NativePreview == nil || encoder == nil {
				return empty, previewRouteError(errors.New("source proposal preview sink was not configured"))
			}
			preview, err := previewResponseFromEncoder(encoder, value.NativePreview.Summary)
			if err != nil {
				return empty, err
			}
			result.Preview = &preview
		}
		return result, nil
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.ProposeExplorerSourceRelationship200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerSourceRelationship", err)
	return loomapi.NewProposeExplorerSourceRelationshipErrorResponse(status, failure)
}

func (r *Handlers) BrowseExplorerFrameSourceOptions(ctx context.Context, request loomapi.BrowseExplorerFrameSourceOptionsRequestObject) (loomapi.BrowseExplorerFrameSourceOptionsResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.BrowseExplorerFrameSourceOptionsJSONRequestBody) (loomapi.FrameSourceOptionsResponse, error) {
		var result loomapi.FrameSourceOptionsResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("frame-source-options", nil)
		}
		request := semanticbrowse.BrowseFrameSourceOptionsRequest{
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

func (r *Handlers) BrowseExplorerSemanticInventory(ctx context.Context, request loomapi.BrowseExplorerSemanticInventoryRequestObject) (loomapi.BrowseExplorerSemanticInventoryResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.BrowseExplorerSemanticInventoryJSONRequestBody) (loomapi.SemanticInventoryBrowseResponse, error) {
		var result loomapi.SemanticInventoryBrowseResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("catalog", errors.New("request body is required"))
		}
		req := semanticbrowse.BrowseSemanticInventoryRequest{Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken, RowRoot: body.RowRoot}
		if body.SelectionMode != nil {
			req.SelectionMode = string(*body.SelectionMode)
		}
		if body.OutputId != nil {
			req.OutputID = *body.OutputId
		}
		if body.FieldPresentation != nil {
			presentation := semanticbrowse.FieldPresentationRequest{
				ExpectedDraftVersion: int64(body.FieldPresentation.ExpectedDraftVersion),
				ExpectedDraftDigest:  body.FieldPresentation.ExpectedDraftDigest,
				SourceNodeID:         body.FieldPresentation.SourceNodeId,
				OccurrenceID:         body.FieldPresentation.OccurrenceId,
				CandidateIDs:         append([]string(nil), body.FieldPresentation.CandidateIds...),
			}
			if body.FieldPresentation.StageId != nil {
				stageID := *body.FieldPresentation.StageId
				presentation.StageID = &stageID
			}
			req.FieldPresentation = &presentation
		}
		if body.FrameId != nil {
			req.FrameID = *body.FrameId
		}
		if body.SourceChoiceId != nil {
			req.SourceChoiceID = *body.SourceChoiceId
		}
		if body.ResourceType != nil {
			req.ResourceType = *body.ResourceType
		}
		if body.Query != nil {
			req.Query = *body.Query
		}
		if body.Cursor != nil {
			req.Cursor = *body.Cursor
		}
		if body.Limit != nil {
			req.Limit = *body.Limit
		}
		value, err := h.application.BrowseSemanticInventory(ctx, req)
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.SemanticInventoryBrowseResponse](value)
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.BrowseExplorerSemanticInventory200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "browseExplorerSemanticInventory", err)
	return loomapi.NewBrowseExplorerSemanticInventoryErrorResponse(status, failure)
}
