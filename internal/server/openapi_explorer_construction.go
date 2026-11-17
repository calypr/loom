package server

import (
	"context"
	"encoding/json"
	"errors"

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

func (h *explorerHTTPHandlers) searchRelatedExpandContributorsDirect(ctx context.Context, project, explorerID string, body *loomapi.RelatedExpandContributorChoiceSearchRequest) (loomapi.RelatedExpandContributorChoiceSearchResponse, error) {
	var result loomapi.RelatedExpandContributorChoiceSearchResponse
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("related-expand-contributors", errors.New("request body with exact stage and route choice identities is required"))
	}
	request := lifecycle.RelatedExpandContributorChoiceSearchRequest{
		Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
		ExpectedDraftVersion: int64(body.ExpectedDraftVersion), ExpectedDraftDigest: body.ExpectedDraftDigest,
		OutputID: body.OutputId, StageID: body.StageId, RouteChoiceID: body.RouteChoiceId,
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
	value, err := h.application.SearchRelatedExpandContributorChoices(ctx, request)
	if err != nil {
		return result, err
	}
	return directAuthoringJSON[loomapi.RelatedExpandContributorChoiceSearchResponse](value)
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
	value, err := r.explorer.searchConstructionChoicesDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.SearchExplorerConstructionChoices200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "searchExplorerConstructionChoices", err)
	return loomapi.NewSearchExplorerConstructionChoicesErrorResponse(status, openAPIErrorResponseBody(status, failure))
}

func (r *HTTPRoutes) SearchExplorerRelatedExpandChoices(ctx context.Context, request loomapi.SearchExplorerRelatedExpandChoicesRequestObject) (loomapi.SearchExplorerRelatedExpandChoicesResponseObject, error) {
	value, err := r.explorer.searchRelatedExpandChoicesDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.SearchExplorerRelatedExpandChoices200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "searchExplorerRelatedExpandChoices", err)
	return loomapi.NewSearchExplorerRelatedExpandChoicesErrorResponse(status, openAPIErrorResponseBody(status, failure))
}

func (r *HTTPRoutes) SearchExplorerRelatedFieldChoices(ctx context.Context, request loomapi.SearchExplorerRelatedFieldChoicesRequestObject) (loomapi.SearchExplorerRelatedFieldChoicesResponseObject, error) {
	value, err := r.explorer.searchRelatedFieldChoicesDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.SearchExplorerRelatedFieldChoices200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "searchExplorerRelatedFieldChoices", err)
	return loomapi.NewSearchExplorerRelatedFieldChoicesErrorResponse(status, openAPIErrorResponseBody(status, failure))
}

func (r *HTTPRoutes) SearchExplorerRelatedExpandContributors(ctx context.Context, request loomapi.SearchExplorerRelatedExpandContributorsRequestObject) (loomapi.SearchExplorerRelatedExpandContributorsResponseObject, error) {
	value, err := r.explorer.searchRelatedExpandContributorsDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.SearchExplorerRelatedExpandContributors200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "searchExplorerRelatedExpandContributors", err)
	return loomapi.NewSearchExplorerRelatedExpandContributorsErrorResponse(status, openAPIErrorResponseBody(status, failure))
}

func (r *HTTPRoutes) SearchExplorerPopulationRoutes(ctx context.Context, request loomapi.SearchExplorerPopulationRoutesRequestObject) (loomapi.SearchExplorerPopulationRoutesResponseObject, error) {
	value, err := r.explorer.searchPopulationRoutesDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.SearchExplorerPopulationRoutes200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "searchExplorerPopulationRoutes", err)
	return loomapi.NewSearchExplorerPopulationRoutesErrorResponse(status, openAPIErrorResponseBody(status, failure))
}

func (r *HTTPRoutes) GetExplorerColumnSource(ctx context.Context, request loomapi.GetExplorerColumnSourceRequestObject) (loomapi.GetExplorerColumnSourceResponseObject, error) {
	value, err := r.explorer.columnSourceDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.GetExplorerColumnSource200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "getExplorerColumnSource", err)
	return loomapi.NewGetExplorerColumnSourceErrorResponse(status, openAPIErrorResponseBody(status, failure))
}
