package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func (h *explorerHTTPHandlers) searchConstructionChoicesDirect(ctx context.Context, project, explorerID string, body *loomapi.ConstructionChoiceSearchRequest) (loomapi.ConstructionChoiceSearchResponse, error) {
	var result loomapi.ConstructionChoiceSearchResponse
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("construction-choices", errors.New("request body and exact source identity are required"))
	}
	sourceJSON, err := body.Source.MarshalJSON()
	if err != nil {
		return result, malformedRouteError("construction-choices", err)
	}
	var source lifecycle.ConstructionChoiceSearchSource
	if err := json.Unmarshal(sourceJSON, &source); err != nil {
		return result, malformedRouteError("construction-choices", err)
	}
	request := lifecycle.ConstructionChoiceSearchRequest{
		Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken, OutputID: body.OutputId, Source: source,
	}
	if body.OccurrenceId != nil {
		request.OccurrenceID = *body.OccurrenceId
	}
	if body.Limit != nil {
		request.Limit = *body.Limit
	}
	if body.Cursor != nil {
		request.Cursor = *body.Cursor
	}
	value, err := h.application.SearchConstructionChoices(ctx, request)
	if err != nil {
		return result, err
	}
	return directAuthoringJSON[loomapi.ConstructionChoiceSearchResponse](value)
}

func (h *explorerHTTPHandlers) searchRelatedExpandChoicesDirect(ctx context.Context, project, explorerID string, body *loomapi.RelatedExpandChoiceSearchRequest) (loomapi.RelatedExpandChoiceSearchResponse, error) {
	var result loomapi.RelatedExpandChoiceSearchResponse
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("related-expand-choices", errors.New("request body with exact stage and target identities is required"))
	}
	request := lifecycle.RelatedExpandChoiceSearchRequest{
		Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
		ExpectedDraftVersion: int64(body.ExpectedDraftVersion), ExpectedDraftDigest: body.ExpectedDraftDigest,
		OutputID: body.OutputId, StageID: body.StageId, AnchorColumnID: body.AnchorColumnId, TargetResourceType: body.TargetResourceType,
	}
	if body.Limit != nil {
		request.Limit = *body.Limit
	}
	if body.Cursor != nil {
		request.Cursor = *body.Cursor
	}
	value, err := h.application.SearchRelatedExpandChoices(ctx, request)
	if err != nil {
		return result, err
	}
	return directAuthoringJSON[loomapi.RelatedExpandChoiceSearchResponse](value)
}

func (h *explorerHTTPHandlers) searchRelatedFieldChoicesDirect(ctx context.Context, project, explorerID string, body *loomapi.RelatedFieldChoiceSearchRequest) (loomapi.RelatedFieldChoiceSearchResponse, error) {
	var result loomapi.RelatedFieldChoiceSearchResponse
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("related-field-choices", errors.New("request body with exact stage identity is required"))
	}
	request := lifecycle.RelatedFieldChoiceSearchRequest{
		Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
		ExpectedDraftVersion: int64(body.ExpectedDraftVersion), ExpectedDraftDigest: body.ExpectedDraftDigest,
		OutputID: body.OutputId, StageID: body.StageId,
	}
	if body.Query != nil {
		request.Query = *body.Query
	}
	if body.Limit != nil {
		request.Limit = *body.Limit
	}
	if body.Cursor != nil {
		request.Cursor = *body.Cursor
	}
	value, err := h.application.SearchRelatedFieldChoices(ctx, request)
	if err != nil {
		return result, err
	}
	return directAuthoringJSON[loomapi.RelatedFieldChoiceSearchResponse](value)
}

func (h *explorerHTTPHandlers) searchPopulationRoutesDirect(ctx context.Context, project, explorerID string, body *loomapi.PopulationRoutesRequest) (loomapi.PopulationRoutesResponse, error) {
	var result loomapi.PopulationRoutesResponse
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("population-routes", errors.New("request body is required"))
	}
	request := lifecycle.PopulationRoutesRequest{
		Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
		OutputID: body.OutputId, SelectionRevisionID: body.SelectionRevisionId,
	}
	if body.Limit != nil {
		request.Limit = *body.Limit
	}
	if body.Cursor != nil {
		request.Cursor = *body.Cursor
	}
	value, err := h.application.SearchPopulationRoutes(ctx, request)
	if err != nil {
		return result, err
	}
	return directAuthoringJSON[loomapi.PopulationRoutesResponse](value)
}

func (h *explorerHTTPHandlers) columnSourceDirect(ctx context.Context, project, explorerID string, body *loomapi.ColumnSourceRequest) (loomapi.ColumnSourceResponse, error) {
	var result loomapi.ColumnSourceResponse
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("column-source", errors.New("request body is required"))
	}
	value, err := h.application.ColumnSource(ctx, lifecycle.ColumnSourceRequest{
		Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
		OutputID: body.OutputId, Column: body.Column,
	})
	if err != nil {
		return result, err
	}
	return directAuthoringJSON[loomapi.ColumnSourceResponse](value)
}

func (r *HTTPRoutes) SearchExplorerConstructionChoices(ctx context.Context, request loomapi.SearchExplorerConstructionChoicesRequestObject) (loomapi.SearchExplorerConstructionChoicesResponseObject, error) {
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "searchExplorerConstructionChoices", explorerUnavailable("construction-choices", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.SearchExplorerConstructionChoices503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.searchConstructionChoicesDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.SearchExplorerConstructionChoices200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "searchExplorerConstructionChoices", err)
	switch status {
	case http.StatusBadRequest:
		return loomapi.SearchExplorerConstructionChoices400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case http.StatusUnauthorized:
		return loomapi.SearchExplorerConstructionChoices401JSONResponse{ServiceUnauthorizedJSONResponse: authoringUnauthorizedResponse(failure)}, nil
	case http.StatusForbidden:
		return loomapi.SearchExplorerConstructionChoices403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusNotFound:
		return loomapi.SearchExplorerConstructionChoices404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.SearchExplorerConstructionChoices409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.SearchExplorerConstructionChoices422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.SearchExplorerConstructionChoices500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.SearchExplorerConstructionChoices503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("searchExplorerConstructionChoices", status)
	}
}

func (r *HTTPRoutes) SearchExplorerRelatedExpandChoices(ctx context.Context, request loomapi.SearchExplorerRelatedExpandChoicesRequestObject) (loomapi.SearchExplorerRelatedExpandChoicesResponseObject, error) {
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "searchExplorerRelatedExpandChoices", explorerUnavailable("related-expand-choices", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.SearchExplorerRelatedExpandChoices503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.searchRelatedExpandChoicesDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.SearchExplorerRelatedExpandChoices200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "searchExplorerRelatedExpandChoices", err)
	switch status {
	case http.StatusBadRequest:
		return loomapi.SearchExplorerRelatedExpandChoices400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case http.StatusUnauthorized:
		return loomapi.SearchExplorerRelatedExpandChoices401JSONResponse{ServiceUnauthorizedJSONResponse: authoringUnauthorizedResponse(failure)}, nil
	case http.StatusForbidden:
		return loomapi.SearchExplorerRelatedExpandChoices403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusNotFound:
		return loomapi.SearchExplorerRelatedExpandChoices404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.SearchExplorerRelatedExpandChoices409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.SearchExplorerRelatedExpandChoices422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.SearchExplorerRelatedExpandChoices500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.SearchExplorerRelatedExpandChoices503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("searchExplorerRelatedExpandChoices", status)
	}
}

func (r *HTTPRoutes) SearchExplorerRelatedFieldChoices(ctx context.Context, request loomapi.SearchExplorerRelatedFieldChoicesRequestObject) (loomapi.SearchExplorerRelatedFieldChoicesResponseObject, error) {
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "searchExplorerRelatedFieldChoices", explorerUnavailable("related-field-choices", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.SearchExplorerRelatedFieldChoices503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.searchRelatedFieldChoicesDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.SearchExplorerRelatedFieldChoices200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "searchExplorerRelatedFieldChoices", err)
	switch status {
	case http.StatusBadRequest:
		return loomapi.SearchExplorerRelatedFieldChoices400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case http.StatusUnauthorized:
		return loomapi.SearchExplorerRelatedFieldChoices401JSONResponse{ServiceUnauthorizedJSONResponse: authoringUnauthorizedResponse(failure)}, nil
	case http.StatusForbidden:
		return loomapi.SearchExplorerRelatedFieldChoices403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusNotFound:
		return loomapi.SearchExplorerRelatedFieldChoices404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.SearchExplorerRelatedFieldChoices409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.SearchExplorerRelatedFieldChoices422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.SearchExplorerRelatedFieldChoices500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.SearchExplorerRelatedFieldChoices503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("searchExplorerRelatedFieldChoices", status)
	}
}

func (r *HTTPRoutes) SearchExplorerPopulationRoutes(ctx context.Context, request loomapi.SearchExplorerPopulationRoutesRequestObject) (loomapi.SearchExplorerPopulationRoutesResponseObject, error) {
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "searchExplorerPopulationRoutes", explorerUnavailable("population-routes", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.SearchExplorerPopulationRoutes503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.searchPopulationRoutesDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.SearchExplorerPopulationRoutes200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "searchExplorerPopulationRoutes", err)
	switch status {
	case http.StatusBadRequest:
		return loomapi.SearchExplorerPopulationRoutes400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case http.StatusUnauthorized:
		return loomapi.SearchExplorerPopulationRoutes401JSONResponse{ServiceUnauthorizedJSONResponse: authoringUnauthorizedResponse(failure)}, nil
	case http.StatusForbidden:
		return loomapi.SearchExplorerPopulationRoutes403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusNotFound:
		return loomapi.SearchExplorerPopulationRoutes404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.SearchExplorerPopulationRoutes409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.SearchExplorerPopulationRoutes422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.SearchExplorerPopulationRoutes500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.SearchExplorerPopulationRoutes503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("searchExplorerPopulationRoutes", status)
	}
}

func (r *HTTPRoutes) GetExplorerColumnSource(ctx context.Context, request loomapi.GetExplorerColumnSourceRequestObject) (loomapi.GetExplorerColumnSourceResponseObject, error) {
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "getExplorerColumnSource", explorerUnavailable("column-source", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.GetExplorerColumnSource503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.columnSourceDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.GetExplorerColumnSource200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "getExplorerColumnSource", err)
	switch status {
	case http.StatusBadRequest:
		return loomapi.GetExplorerColumnSource400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case http.StatusUnauthorized:
		return loomapi.GetExplorerColumnSource401JSONResponse{ServiceUnauthorizedJSONResponse: authoringUnauthorizedResponse(failure)}, nil
	case http.StatusForbidden:
		return loomapi.GetExplorerColumnSource403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusNotFound:
		return loomapi.GetExplorerColumnSource404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.GetExplorerColumnSource409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.GetExplorerColumnSource500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.GetExplorerColumnSource503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("getExplorerColumnSource", status)
	}
}
