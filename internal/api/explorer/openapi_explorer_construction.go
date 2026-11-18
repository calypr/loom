package explorerapi

import (
	"context"
	"encoding/json"
	"errors"

	loomapi "github.com/calypr/loom/api/loomapi"
	explorer "github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func (r *Handlers) SearchExplorerConstructionChoices(ctx context.Context, request loomapi.SearchExplorerConstructionChoicesRequestObject) (loomapi.SearchExplorerConstructionChoicesResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.ConstructionChoiceSearchRequest) (loomapi.ConstructionChoiceSearchResponse, error) {
		var result loomapi.ConstructionChoiceSearchResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("construction-choices", errors.New("request body and exact source identity are required"))
		}
		sourceJSON, err := body.Source.MarshalJSON()
		if err != nil {
			return result, explorer.MalformedRouteError("construction-choices", err)
		}
		var source lifecycle.ConstructionChoiceSearchSource
		if err := json.Unmarshal(sourceJSON, &source); err != nil {
			return result, explorer.MalformedRouteError("construction-choices", err)
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
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.SearchExplorerConstructionChoices200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "searchExplorerConstructionChoices", err)
	return loomapi.NewSearchExplorerConstructionChoicesErrorResponse(status, failure)
}

func (r *Handlers) SearchExplorerRelatedExpandChoices(ctx context.Context, request loomapi.SearchExplorerRelatedExpandChoicesRequestObject) (loomapi.SearchExplorerRelatedExpandChoicesResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.RelatedExpandChoiceSearchRequest) (loomapi.RelatedExpandChoiceSearchResponse, error) {
		var result loomapi.RelatedExpandChoiceSearchResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("related-expand-choices", errors.New("request body with exact stage and target identities is required"))
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
		if body.SavedGraphOnly != nil {
			request.SavedGraphOnly = *body.SavedGraphOnly
		}
		value, err := h.application.SearchRelatedExpandChoices(ctx, request)
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.RelatedExpandChoiceSearchResponse](value)
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.SearchExplorerRelatedExpandChoices200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "searchExplorerRelatedExpandChoices", err)
	return loomapi.NewSearchExplorerRelatedExpandChoicesErrorResponse(status, failure)
}

func (r *Handlers) SearchExplorerRelatedFieldChoices(ctx context.Context, request loomapi.SearchExplorerRelatedFieldChoicesRequestObject) (loomapi.SearchExplorerRelatedFieldChoicesResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.RelatedFieldChoiceSearchRequest) (loomapi.RelatedFieldChoiceSearchResponse, error) {
		var result loomapi.RelatedFieldChoiceSearchResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("related-field-choices", errors.New("request body with exact stage identity is required"))
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
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.SearchExplorerRelatedFieldChoices200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "searchExplorerRelatedFieldChoices", err)
	return loomapi.NewSearchExplorerRelatedFieldChoicesErrorResponse(status, failure)
}

func (r *Handlers) SearchExplorerRelatedExpandContributors(ctx context.Context, request loomapi.SearchExplorerRelatedExpandContributorsRequestObject) (loomapi.SearchExplorerRelatedExpandContributorsResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.RelatedExpandContributorChoiceSearchRequest) (loomapi.RelatedExpandContributorChoiceSearchResponse, error) {
		var result loomapi.RelatedExpandContributorChoiceSearchResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("related-expand-contributors", errors.New("request body with exact stage and route choice identities is required"))
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
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.SearchExplorerRelatedExpandContributors200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "searchExplorerRelatedExpandContributors", err)
	return loomapi.NewSearchExplorerRelatedExpandContributorsErrorResponse(status, failure)
}

func (r *Handlers) SearchExplorerPopulationRoutes(ctx context.Context, request loomapi.SearchExplorerPopulationRoutesRequestObject) (loomapi.SearchExplorerPopulationRoutesResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.PopulationRoutesRequest) (loomapi.PopulationRoutesResponse, error) {
		var result loomapi.PopulationRoutesResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("population-routes", errors.New("request body is required"))
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
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.SearchExplorerPopulationRoutes200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "searchExplorerPopulationRoutes", err)
	return loomapi.NewSearchExplorerPopulationRoutesErrorResponse(status, failure)
}

func (r *Handlers) GetExplorerColumnSource(ctx context.Context, request loomapi.GetExplorerColumnSourceRequestObject) (loomapi.GetExplorerColumnSourceResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.ColumnSourceRequest) (loomapi.ColumnSourceResponse, error) {
		var result loomapi.ColumnSourceResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("column-source", errors.New("request body is required"))
		}
		value, err := h.application.ColumnSource(ctx, lifecycle.ColumnSourceRequest{
			Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
			OutputID: body.OutputId, Column: body.Column,
		})
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.ColumnSourceResponse](value)
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.GetExplorerColumnSource200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "getExplorerColumnSource", err)
	return loomapi.NewGetExplorerColumnSourceErrorResponse(status, failure)
}
