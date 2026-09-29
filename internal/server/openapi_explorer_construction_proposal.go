package server

import (
	"context"
	"net/http"
	"time"

	loomapi "github.com/calypr/loom/generated/loomapi"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func (h *explorerHTTPHandlers) getConstructionCapabilitiesDirect(ctx context.Context, project, explorerID string, body *loomapi.ConstructionCapabilitiesRequest) (loomapi.ConstructionCapabilitiesResponse, error) {
	var result loomapi.ConstructionCapabilitiesResponse
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("construction-capabilities", nil)
	}
	value, err := h.application.GetConstructionCapabilities(ctx, lifecycle.ConstructionCapabilitiesRequest{
		Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
		ExpectedDraftVersion: int64(body.ExpectedDraftVersion), ExpectedDraftDigest: body.ExpectedDraftDigest,
		OutputID: body.OutputId, StageID: body.StageId,
	})
	if err != nil {
		return result, err
	}
	return directAuthoringJSON[loomapi.ConstructionCapabilitiesResponse](value)
}

func (h *explorerHTTPHandlers) discoverConstructionCategoriesDirect(ctx context.Context, project, explorerID string, body *loomapi.ConstructionCategoryDiscoveryRequest) (loomapi.ConstructionCategoryDiscoveryResponse, error) {
	var result loomapi.ConstructionCategoryDiscoveryResponse
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("construction-category-discovery", nil)
	}
	value, err := h.application.DiscoverConstructionCategories(ctx, lifecycle.ConstructionCategoryDiscoveryRequest{
		Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
		ExpectedDraftVersion: int64(body.ExpectedDraftVersion), ExpectedDraftDigest: body.ExpectedDraftDigest,
		OutputID: body.OutputId, StageID: body.StageId,
		CategoryColumnID: body.CategoryColumnId, ValueColumnID: body.ValueColumnId,
	})
	if err != nil {
		return result, err
	}
	return directAuthoringJSON[loomapi.ConstructionCategoryDiscoveryResponse](value)
}

func (h *explorerHTTPHandlers) proposeConstructionDirect(ctx context.Context, project, explorerID string, body *loomapi.ConstructionProposalRequest) (loomapi.ConstructionProposalResponse, error) {
	var result loomapi.ConstructionProposalResponse
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("construction-proposal", nil)
	}
	candidate, err := directAuthoringJSON[authoringv2.Construction](body.CandidateConstruction)
	if err != nil {
		return result, malformedRouteError("construction-proposal", err)
	}
	changedStepID := ""
	if body.ChangedStepId != nil {
		changedStepID = *body.ChangedStepId
	}
	request := lifecycle.ConstructionProposalRequest{
		Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
		ExpectedDraftVersion: int64(body.ExpectedDraftVersion), ExpectedDraftDigest: body.ExpectedDraftDigest,
		OutputID: body.OutputId, ChangedStepID: changedStepID,
		CandidateConstruction: candidate,
	}
	if body.GroupSource != nil {
		request.GroupSource = &lifecycle.ConstructionGroupSourceSelection{
			RowChoiceID: body.GroupSource.RowChoiceId,
			ColumnID:    body.GroupSource.ColumnId,
		}
	}
	if body.RemoveStepIds != nil {
		request.RemoveStepIDs = append([]string(nil), (*body.RemoveStepIds)...)
	}
	if body.Limit != nil {
		request.Limit = *body.Limit
	}
	value, err := h.application.ProposeConstruction(ctx, request)
	if err != nil {
		return result, err
	}
	result, err = directAuthoringJSON[loomapi.ConstructionProposalResponse](value)
	if err != nil {
		return result, err
	}
	if result.PreviewStatus == loomapi.ConstructionProposalResponsePreviewStatus("NEEDS_REPAIR") {
		return result, nil
	}
	if result.PreviewStatus != loomapi.ConstructionProposalResponsePreviewStatus("PREVIEW_PENDING") {
		return result, malformedRouteError("construction-proposal", nil)
	}
	if result.ProposalId == nil || *result.ProposalId == "" {
		return result, malformedRouteError("construction-proposal", nil)
	}
	limit := dataframeexecution.DefaultPreviewLimit
	if body.Limit != nil {
		limit = *body.Limit
	}
	started := time.Now()
	preview, err := h.previewAuthoringDirect(ctx, project, explorerID, &loomapi.PreviewExplorerJSONRequestBody{
		ReceiptId: *result.ProposalId, OutputId: body.OutputId, Limit: &limit,
	})
	result.PreviewDurationMs = time.Since(started).Milliseconds()
	if err != nil {
		return result, err
	}
	result.Preview = &preview
	result.PreviewStatus = loomapi.ConstructionProposalResponsePreviewStatus("READY")
	return result, nil
}

func (r *HTTPRoutes) GetExplorerConstructionCapabilities(ctx context.Context, request loomapi.GetExplorerConstructionCapabilitiesRequestObject) (loomapi.GetExplorerConstructionCapabilitiesResponseObject, error) {
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "getExplorerConstructionCapabilities", explorerUnavailable("construction-capabilities", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.GetExplorerConstructionCapabilities503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.getConstructionCapabilitiesDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.GetExplorerConstructionCapabilities200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "getExplorerConstructionCapabilities", err)
	switch status {
	case http.StatusBadRequest:
		return loomapi.GetExplorerConstructionCapabilities400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case http.StatusUnauthorized:
		return loomapi.GetExplorerConstructionCapabilities401JSONResponse{ServiceUnauthorizedJSONResponse: authoringUnauthorizedResponse(failure)}, nil
	case http.StatusForbidden:
		return loomapi.GetExplorerConstructionCapabilities403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusNotFound:
		return loomapi.GetExplorerConstructionCapabilities404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.GetExplorerConstructionCapabilities409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.GetExplorerConstructionCapabilities422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.GetExplorerConstructionCapabilities500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.GetExplorerConstructionCapabilities503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("getExplorerConstructionCapabilities", status)
	}
}

func (r *HTTPRoutes) DiscoverExplorerConstructionCategories(ctx context.Context, request loomapi.DiscoverExplorerConstructionCategoriesRequestObject) (loomapi.DiscoverExplorerConstructionCategoriesResponseObject, error) {
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "discoverExplorerConstructionCategories", explorerUnavailable("construction-category-discovery", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.DiscoverExplorerConstructionCategories503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.discoverConstructionCategoriesDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.DiscoverExplorerConstructionCategories200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "discoverExplorerConstructionCategories", err)
	switch status {
	case http.StatusBadRequest:
		return loomapi.DiscoverExplorerConstructionCategories400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case http.StatusUnauthorized:
		return loomapi.DiscoverExplorerConstructionCategories401JSONResponse{ServiceUnauthorizedJSONResponse: authoringUnauthorizedResponse(failure)}, nil
	case http.StatusForbidden:
		return loomapi.DiscoverExplorerConstructionCategories403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusNotFound:
		return loomapi.DiscoverExplorerConstructionCategories404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.DiscoverExplorerConstructionCategories409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.DiscoverExplorerConstructionCategories422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.DiscoverExplorerConstructionCategories500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.DiscoverExplorerConstructionCategories503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("discoverExplorerConstructionCategories", status)
	}
}

func (r *HTTPRoutes) ProposeExplorerConstruction(ctx context.Context, request loomapi.ProposeExplorerConstructionRequestObject) (loomapi.ProposeExplorerConstructionResponseObject, error) {
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerConstruction", explorerUnavailable("construction-proposal", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.ProposeExplorerConstruction503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.proposeConstructionDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.ProposeExplorerConstruction200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerConstruction", err)
	switch status {
	case http.StatusBadRequest:
		return loomapi.ProposeExplorerConstruction400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case http.StatusUnauthorized:
		return loomapi.ProposeExplorerConstruction401JSONResponse{ServiceUnauthorizedJSONResponse: authoringUnauthorizedResponse(failure)}, nil
	case http.StatusForbidden:
		return loomapi.ProposeExplorerConstruction403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusNotFound:
		return loomapi.ProposeExplorerConstruction404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.ProposeExplorerConstruction409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.ProposeExplorerConstruction422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.ProposeExplorerConstruction500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.ProposeExplorerConstruction503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	case http.StatusGatewayTimeout:
		return loomapi.ProposeExplorerConstruction504JSONResponse{AuthoringGatewayTimeoutJSONResponse: loomapi.AuthoringGatewayTimeoutJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("proposeExplorerConstruction", status)
	}
}
