package server

import (
	"context"
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
	groupStepID := ""
	if body.GroupStepId != nil {
		groupStepID = *body.GroupStepId
	}
	value, err := h.application.GetConstructionCapabilities(ctx, lifecycle.ConstructionCapabilitiesRequest{
		Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
		ExpectedDraftVersion: int64(body.ExpectedDraftVersion), ExpectedDraftDigest: body.ExpectedDraftDigest,
		OutputID: body.OutputId, StageID: body.StageId, GroupStepID: groupStepID,
		IncludeGroupChoices: body.IncludeGroupChoices != nil && *body.IncludeGroupChoices,
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
	request, err := directAuthoringJSON[lifecycle.ConstructionCategoryDiscoveryRequest](body)
	if err != nil {
		return result, malformedRouteError("construction-category-discovery", err)
	}
	request.Project, request.ExplorerID = project, explorerID
	value, err := h.application.DiscoverConstructionCategories(ctx, request)
	if err != nil {
		return result, err
	}
	return directAuthoringJSON[loomapi.ConstructionCategoryDiscoveryResponse](value)
}

func constructionProposalPreviewBudget(ctx context.Context, candidate authoringv2.Construction) (context.Context, time.Duration) {
	for _, step := range candidate.Steps {
		if step.Operation.Kind == authoringv2.ConstructionOperationPivot {
			return withPivotPreviewQueryBudget(ctx), pivotCategoryScanTimeout
		}
	}
	return ctx, explorerPreviewTimeout
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
	if body.PivotSources != nil {
		request.PivotSources = make([]lifecycle.ConstructionPivotSourceSelection, 0, len(*body.PivotSources))
		for _, selection := range *body.PivotSources {
			request.PivotSources = append(request.PivotSources, lifecycle.ConstructionPivotSourceSelection{
				ChoiceID: selection.ChoiceId, ColumnID: selection.ColumnId,
			})
		}
	}
	if body.FilterSources != nil {
		request.FilterSources = make([]lifecycle.ConstructionFilterSourceSelection, 0, len(*body.FilterSources))
		for _, selection := range *body.FilterSources {
			request.FilterSources = append(request.FilterSources, lifecycle.ConstructionFilterSourceSelection{
				ChoiceID: selection.ChoiceId, ColumnID: selection.ColumnId,
			})
		}
	}
	if body.DeriveSources != nil {
		request.DeriveSources = make([]lifecycle.ConstructionDeriveSourceSelection, 0, len(*body.DeriveSources))
		for _, selection := range *body.DeriveSources {
			request.DeriveSources = append(request.DeriveSources, lifecycle.ConstructionDeriveSourceSelection{
				ChoiceID: selection.ChoiceId, ColumnID: selection.ColumnId,
			})
		}
	}
	if body.UnpivotSources != nil {
		request.UnpivotSources = make([]lifecycle.ConstructionUnpivotSourceSelection, 0, len(*body.UnpivotSources))
		for _, selection := range *body.UnpivotSources {
			request.UnpivotSources = append(request.UnpivotSources, lifecycle.ConstructionUnpivotSourceSelection{
				ChoiceID: selection.ChoiceId, ColumnID: selection.ColumnId,
			})
		}
	}
	if body.ExpandSources != nil {
		request.ExpandSources = make([]lifecycle.ConstructionExpandSourceSelection, 0, len(*body.ExpandSources))
		for _, selection := range *body.ExpandSources {
			request.ExpandSources = append(request.ExpandSources, lifecycle.ConstructionExpandSourceSelection{
				ChoiceID: selection.ChoiceId, ColumnID: selection.ColumnId,
			})
		}
	}
	if body.GroupSources != nil {
		request.GroupSources = make([]lifecycle.ConstructionGroupSourceSelection, 0, len(*body.GroupSources))
		for _, selection := range *body.GroupSources {
			request.GroupSources = append(request.GroupSources, lifecycle.ConstructionGroupSourceSelection{RowChoiceID: selection.RowChoiceId, ColumnID: selection.ColumnId})
		}
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
	previewCtx, previewTimeout := constructionProposalPreviewBudget(ctx, candidate)
	started := time.Now()
	preview, err := h.previewAuthoringDirectWithTimeout(previewCtx, project, explorerID, &loomapi.PreviewExplorerJSONRequestBody{
		ReceiptId: *result.ProposalId, OutputId: body.OutputId, Limit: &limit,
	}, previewTimeout, true)
	result.PreviewDurationMs = time.Since(started).Milliseconds()
	if err != nil {
		return result, err
	}
	result.Preview = &preview
	result.PreviewStatus = loomapi.ConstructionProposalResponsePreviewStatus("READY")
	return result, nil
}

func (r *HTTPRoutes) GetExplorerConstructionCapabilities(ctx context.Context, request loomapi.GetExplorerConstructionCapabilitiesRequestObject) (loomapi.GetExplorerConstructionCapabilitiesResponseObject, error) {
	value, err := r.explorer.getConstructionCapabilitiesDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.GetExplorerConstructionCapabilities200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "getExplorerConstructionCapabilities", err)
	return loomapi.NewGetExplorerConstructionCapabilitiesErrorResponse(status, openAPIErrorResponseBody(status, failure))
}

func (r *HTTPRoutes) DiscoverExplorerConstructionCategories(ctx context.Context, request loomapi.DiscoverExplorerConstructionCategoriesRequestObject) (loomapi.DiscoverExplorerConstructionCategoriesResponseObject, error) {
	value, err := r.explorer.discoverConstructionCategoriesDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.DiscoverExplorerConstructionCategories200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "discoverExplorerConstructionCategories", err)
	return loomapi.NewDiscoverExplorerConstructionCategoriesErrorResponse(status, openAPIErrorResponseBody(status, failure))
}

func (r *HTTPRoutes) ProposeExplorerConstruction(ctx context.Context, request loomapi.ProposeExplorerConstructionRequestObject) (loomapi.ProposeExplorerConstructionResponseObject, error) {
	value, err := r.explorer.proposeConstructionDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.ProposeExplorerConstruction200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerConstruction", err)
	return loomapi.NewProposeExplorerConstructionErrorResponse(status, openAPIErrorResponseBody(status, failure))
}
