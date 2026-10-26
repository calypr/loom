package server

import (
	"context"
	"net/http"
	"time"

	loomapi "github.com/calypr/loom/generated/loomapi"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func (h *explorerHTTPHandlers) proposePopulationMemberRemovalDirect(ctx context.Context, project, explorerID string, body *loomapi.PopulationMemberRemovalProposalRequest) (loomapi.PopulationMemberRemovalProposalResponse, error) {
	var result loomapi.PopulationMemberRemovalProposalResponse
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("population-member-proposal", nil)
	}
	removedMember, err := directAuthoringJSON[explorer.ResourceRef](body.RemovedMember)
	if err != nil {
		return result, malformedRouteError("population-member-proposal", err)
	}
	request := lifecycle.PopulationMemberRemovalProposalRequest{
		Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
		ExpectedDraftVersion: body.ExpectedDraftVersion, ExpectedDraftDigest: body.ExpectedDraftDigest,
		OutputID: body.OutputId, BaseSelectionID: body.BaseSelectionRevisionId, RemovedMember: removedMember,
	}
	if body.Limit != nil {
		request.Limit = *body.Limit
	}
	value, err := h.application.ProposePopulationMemberRemoval(ctx, request)
	if err != nil {
		return result, err
	}
	if value.ProposalID == "" || value.BaseSelection == nil || value.CandidateSelection == nil || value.PreviewStatus != "PREVIEW_PENDING" {
		return result, malformedRouteError("population-member-proposal", nil)
	}
	result, err = directAuthoringJSON[loomapi.PopulationMemberRemovalProposalResponse](value)
	if err != nil {
		return result, err
	}
	limit := dataframeexecution.DefaultPreviewLimit
	if body.Limit != nil {
		limit = *body.Limit
	}
	started := time.Now()
	preview, err := h.previewAuthoringDirect(ctx, project, explorerID, &loomapi.PreviewExplorerJSONRequestBody{
		ReceiptId: value.ProposalID, OutputId: body.OutputId, Limit: &limit,
	})
	result.PreviewDurationMs = time.Since(started).Milliseconds()
	if err != nil {
		return result, err
	}
	if preview.ReceiptId != value.ProposalID || preview.OutputId != body.OutputId {
		return result, malformedRouteError("population-member-proposal", nil)
	}
	result.Preview = preview
	result.PreviewStatus = loomapi.PopulationMemberRemovalProposalResponsePreviewStatusREADY
	return result, nil
}

func (r *HTTPRoutes) ProposeExplorerPopulationMemberRemoval(ctx context.Context, request loomapi.ProposeExplorerPopulationMemberRemovalRequestObject) (loomapi.ProposeExplorerPopulationMemberRemovalResponseObject, error) {
	if r == nil || r.explorer == nil || r.explorer.application == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerPopulationMemberRemoval", explorerUnavailable("population-member-proposal", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.ProposeExplorerPopulationMemberRemoval503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.proposePopulationMemberRemovalDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.ProposeExplorerPopulationMemberRemoval200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerPopulationMemberRemoval", err)
	switch status {
	case http.StatusBadRequest:
		return loomapi.ProposeExplorerPopulationMemberRemoval400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case http.StatusUnauthorized:
		return loomapi.ProposeExplorerPopulationMemberRemoval401JSONResponse{ServiceUnauthorizedJSONResponse: authoringUnauthorizedResponse(failure)}, nil
	case http.StatusForbidden:
		return loomapi.ProposeExplorerPopulationMemberRemoval403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusNotFound:
		return loomapi.ProposeExplorerPopulationMemberRemoval404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.ProposeExplorerPopulationMemberRemoval409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.ProposeExplorerPopulationMemberRemoval422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.ProposeExplorerPopulationMemberRemoval500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.ProposeExplorerPopulationMemberRemoval503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	case http.StatusGatewayTimeout:
		return loomapi.ProposeExplorerPopulationMemberRemoval504JSONResponse{AuthoringGatewayTimeoutJSONResponse: loomapi.AuthoringGatewayTimeoutJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("proposeExplorerPopulationMemberRemoval", status)
	}
}
