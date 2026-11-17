package server

import (
	"context"
	"errors"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func selectionRefs(refs []loomapi.SelectionResourceRef) []explorer.ResourceRef {
	result := make([]explorer.ResourceRef, len(refs))
	for i, ref := range refs {
		result[i] = explorer.ResourceRef{Project: ref.Project, Generation: ref.Generation, ResourceType: ref.ResourceType, ID: ref.Id}
	}
	return result
}

func (h *explorerHTTPHandlers) createSelectionDirect(ctx context.Context, request loomapi.CreateExplorerSelectionRequestObject) (loomapi.SelectionRevision, error) {
	var result loomapi.SelectionRevision
	if err := h.authoringWriteDirect(ctx, string(request.Project), authResourcePathFromParam(request.Params.AuthResourcePath)); err != nil {
		return result, err
	}
	if request.Body == nil {
		return result, malformedRouteError("selection", errors.New("request body is required"))
	}
	body := request.Body
	intent := lifecycle.SelectionIntentCreateRequest{
		Project: string(request.Project), ExplorerID: string(request.ExplorerId), Actor: subjectFromContext(ctx),
		SnapshotToken: body.SnapshotToken, IdempotencyKey: body.IdempotencyKey,
		Source: lifecycle.SelectionSourceIntent{Kind: string(body.Source.Kind)},
	}
	if body.Source.Resources != nil {
		intent.Source.Resources = selectionRefs(body.Source.Resources.Refs)
		if body.Source.Resources.ResourceType != nil {
			intent.ResourceType = *body.Source.Resources.ResourceType
		}
	}
	if source := body.Source.PublishedOutput; source != nil {
		intent.Source.PublishedOutput = &lifecycle.PublishedOutputIntent{RevisionID: source.RevisionId, OutputID: source.OutputId}
		if source.Filters != nil {
			filters, err := directAuthoringJSON[[]published.Filter](*source.Filters)
			if err != nil {
				return result, malformedRouteError("selection", err)
			}
			intent.Source.PublishedOutput.Filters = filters
		}
	}
	if source := body.Source.SelectionRevision; source != nil {
		intent.Source.SelectionRevisionID = source.SelectionRevisionId
	}
	if body.Exclusions != nil {
		intent.Exclusions = selectionRefs(*body.Exclusions)
	}
	value, err := h.application.CreateSelection(ctx, intent)
	if err != nil {
		return result, err
	}
	return directAuthoringJSON[loomapi.SelectionRevision](value.Header)
}

func (h *explorerHTTPHandlers) getSelectionDirect(ctx context.Context, request loomapi.GetExplorerSelectionRequestObject) (loomapi.SelectionPage, error) {
	var result loomapi.SelectionPage
	if err := h.authoringReadDirect(ctx, string(request.Project)); err != nil {
		return result, err
	}
	intent := lifecycle.SelectionReadIntentRequest{Project: string(request.Project), ExplorerID: string(request.ExplorerId), RevisionID: request.SelectionRevision, Limit: 100}
	if request.Params.Cursor != nil {
		intent.Cursor = *request.Params.Cursor
	}
	if request.Params.Limit != nil {
		intent.Limit = *request.Params.Limit
	}
	value, err := h.application.ReadSelection(ctx, intent)
	if err != nil {
		return result, err
	}
	revision, err := directAuthoringJSON[loomapi.SelectionRevision](value.Header)
	if err != nil {
		return result, err
	}
	members := make([]loomapi.SelectionMember, 0, len(value.Members))
	for _, member := range value.Members {
		wire, err := directAuthoringJSON[loomapi.SelectionMember](member)
		if err != nil {
			return result, err
		}
		members = append(members, wire)
	}
	result = loomapi.SelectionPage{Revision: revision, Members: members}
	if value.NextCursor != "" {
		result.NextCursor = &value.NextCursor
	}
	return result, nil
}

func (r *HTTPRoutes) CreateExplorerSelection(ctx context.Context, request loomapi.CreateExplorerSelectionRequestObject) (loomapi.CreateExplorerSelectionResponseObject, error) {
	var value loomapi.SelectionRevision
	var err error
	if r.explorer.application == nil {
		err = explorerUnavailable("selection", "AUTHORING_UNAVAILABLE", "Explorer selections are not configured")
	} else {
		value, err = r.explorer.createSelectionDirect(ctx, request)
	}
	if err == nil {
		return loomapi.CreateExplorerSelection201JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "createExplorerSelection", err)
	return loomapi.NewCreateExplorerSelectionErrorResponse(status, openAPIErrorResponseBody(status, failure))
}

func (r *HTTPRoutes) GetExplorerSelection(ctx context.Context, request loomapi.GetExplorerSelectionRequestObject) (loomapi.GetExplorerSelectionResponseObject, error) {
	var value loomapi.SelectionPage
	var err error
	if r.explorer.application == nil {
		err = explorerUnavailable("selection", "AUTHORING_UNAVAILABLE", "Explorer selections are not configured")
	} else {
		value, err = r.explorer.getSelectionDirect(ctx, request)
	}
	if err == nil {
		return loomapi.GetExplorerSelection200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "getExplorerSelection", err)
	return loomapi.NewGetExplorerSelectionErrorResponse(status, openAPIErrorResponseBody(status, failure))
}
