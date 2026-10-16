package server

import (
	"context"
	"net/http"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func (h *explorerHTTPHandlers) createExplicitGroupRevisionDirect(ctx context.Context, request loomapi.CreateExplorerExplicitGroupRevisionRequestObject) (loomapi.ExplicitGroupRevisionSummary, error) {
	if err := h.authoringWriteDirect(ctx, string(request.Project), authResourcePathFromParam(request.Params.AuthResourcePath)); err != nil {
		return loomapi.ExplicitGroupRevisionSummary{}, err
	}
	if request.Body == nil {
		return loomapi.ExplicitGroupRevisionSummary{}, malformedRouteError("explicit-groups", nil)
	}
	groups := make([]lifecycle.ExplicitGroupInput, len(request.Body.Groups))
	for i, group := range request.Body.Groups {
		groups[i] = lifecycle.ExplicitGroupInput{ID: group.Id, Label: group.Label, Ordinal: group.Ordinal, MemberIDs: group.MemberIds}
	}
	value, err := h.application.CreateExplicitGroupRevision(ctx, lifecycle.ExplicitGroupCreateRequest{
		Project: string(request.Project), ExplorerID: string(request.ExplorerId), SnapshotToken: request.Body.SnapshotToken,
		SelectionID: request.SelectionRevision, IdempotencyKey: request.Body.IdempotencyKey, Groups: groups,
	})
	if err != nil {
		return loomapi.ExplicitGroupRevisionSummary{}, err
	}
	response := loomapi.ExplicitGroupRevisionSummary{
		RevisionId: value.RevisionID, SourceSelectionRevisionId: value.SourceSelectionRevisionID,
		GroupCount: value.GroupCount, MemberCount: value.MemberCount, CreatedAt: value.CreatedAt,
		Groups: make([]loomapi.ExplicitGroupSummary, 0, len(value.Groups)),
	}
	for _, group := range value.Groups {
		response.Groups = append(response.Groups, loomapi.ExplicitGroupSummary{Id: group.ID, Label: group.Label, Ordinal: group.Ordinal, MemberCount: group.MemberCount})
	}
	return response, nil
}

func (r *HTTPRoutes) CreateExplorerExplicitGroupRevision(ctx context.Context, request loomapi.CreateExplorerExplicitGroupRevisionRequestObject) (loomapi.CreateExplorerExplicitGroupRevisionResponseObject, error) {
	var value loomapi.ExplicitGroupRevisionSummary
	var err error
	if r == nil || r.explorer == nil || r.explorer.application == nil {
		err = explorerUnavailable("explicit-groups", "AUTHORING_UNAVAILABLE", "explicit group authoring is not configured")
	} else {
		value, err = r.explorer.createExplicitGroupRevisionDirect(ctx, request)
	}
	if err == nil {
		return loomapi.CreateExplorerExplicitGroupRevision201JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "createExplorerExplicitGroupRevision", err)
	switch status {
	case http.StatusBadRequest:
		return loomapi.CreateExplorerExplicitGroupRevision400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case http.StatusUnauthorized:
		return loomapi.CreateExplorerExplicitGroupRevision401JSONResponse{ServiceUnauthorizedJSONResponse: authoringUnauthorizedResponse(failure)}, nil
	case http.StatusForbidden:
		return loomapi.CreateExplorerExplicitGroupRevision403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusNotFound:
		return loomapi.CreateExplorerExplicitGroupRevision404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.CreateExplorerExplicitGroupRevision409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.CreateExplorerExplicitGroupRevision422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.CreateExplorerExplicitGroupRevision503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.CreateExplorerExplicitGroupRevision500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("createExplorerExplicitGroupRevision", status)
	}
}
