package server

import (
	"context"
	"errors"
	"net/http"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func (h *explorerHTTPHandlers) pivotProposalDirect(ctx context.Context, project, explorerID string, body *loomapi.PivotProposalRequest) (loomapi.PivotProposalResponse, error) {
	var result loomapi.PivotProposalResponse
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("pivot-proposals", errors.New("request body is required"))
	}
	selections := make([]lifecycle.PivotProposalSelection, len(body.Selections))
	for index, selection := range body.Selections {
		selections[index] = lifecycle.PivotProposalSelection{
			ChoiceID: selection.ChoiceId,
			Form:     capability.ConstructionChoiceForm(selection.Form),
		}
		if selection.Title != nil {
			selections[index].Title = *selection.Title
		}
	}
	value, err := h.application.ProposePivot(ctx, lifecycle.PivotProposalRequest{
		Project: project, ExplorerID: explorerID,
		SnapshotToken: body.SnapshotToken, ExpectedDraftVersion: body.ExpectedDraftVersion,
		ExpectedDraftDigest: body.ExpectedDraftDigest, OutputID: body.OutputId,
		FamilyID: body.FamilyId, CommandID: body.CommandId, Selections: selections,
	})
	if err != nil {
		return result, err
	}
	columns := make([]loomapi.PivotProposalColumn, len(value.Columns))
	for index, column := range value.Columns {
		columns[index] = loomapi.PivotProposalColumn{Code: column.Code, Column: column.Column, Label: column.Label}
	}
	return loomapi.PivotProposalResponse{
		ReceiptId: value.ReceiptID, OutputId: value.OutputID, FamilyId: value.FamilyID,
		DraftDigest: value.DraftDigest, Columns: columns,
	}, nil
}

func (r *HTTPRoutes) ProposeExplorerPivot(ctx context.Context, request loomapi.ProposeExplorerPivotRequestObject) (loomapi.ProposeExplorerPivotResponseObject, error) {
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerPivot", explorerUnavailable("pivot-proposals", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.ProposeExplorerPivot503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.pivotProposalDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.ProposeExplorerPivot200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerPivot", err)
	switch status {
	case http.StatusBadRequest:
		return loomapi.ProposeExplorerPivot400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case http.StatusUnauthorized:
		return loomapi.ProposeExplorerPivot401JSONResponse{ServiceUnauthorizedJSONResponse: authoringUnauthorizedResponse(failure)}, nil
	case http.StatusForbidden:
		return loomapi.ProposeExplorerPivot403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusNotFound:
		return loomapi.ProposeExplorerPivot404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.ProposeExplorerPivot409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.ProposeExplorerPivot422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.ProposeExplorerPivot500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.ProposeExplorerPivot503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("proposeExplorerPivot", status)
	}
}
