package server

import (
	"context"
	"errors"

	explorer "github.com/calypr/loom/internal/explorer"

	loomapi "github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func (h *explorerHTTPHandlers) browseSemanticInventoryDirect(ctx context.Context, project, explorerID string, body *loomapi.BrowseExplorerSemanticInventoryJSONRequestBody) (loomapi.SemanticInventoryBrowseResponse, error) {
	var result loomapi.SemanticInventoryBrowseResponse
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, explorer.MalformedRouteError("catalog", errors.New("request body is required"))
	}
	req := lifecycle.BrowseSemanticInventoryRequest{Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken, RowRoot: body.RowRoot}
	if body.SelectionMode != nil {
		req.SelectionMode = string(*body.SelectionMode)
	}
	if body.OutputId != nil {
		req.OutputID = *body.OutputId
	}
	if body.FieldPresentation != nil {
		presentation := lifecycle.FieldPresentationRequest{
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
}

func (r *HTTPRoutes) BrowseExplorerSemanticInventory(ctx context.Context, request loomapi.BrowseExplorerSemanticInventoryRequestObject) (loomapi.BrowseExplorerSemanticInventoryResponseObject, error) {
	value, err := r.explorer.browseSemanticInventoryDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.BrowseExplorerSemanticInventory200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "browseExplorerSemanticInventory", err)
	return loomapi.NewBrowseExplorerSemanticInventoryErrorResponse(status, failure)
}
