package server

import (
	"context"
	"net/http"

	loomapi "github.com/calypr/loom/generated/loomapi"
)

func (h *explorerHTTPHandlers) getConstructionInputsOpenAPIDirect(ctx context.Context, project, explorerID string, body *loomapi.ConstructionInputsRequest) (loomapi.ConstructionInputsResponse, error) {
	var result loomapi.ConstructionInputsResponse
	if body == nil {
		_, err := h.getConstructionInputsDirect(ctx, project, explorerID, nil)
		return result, err
	}
	request := &constructionInputsRequest{
		SnapshotToken: body.SnapshotToken, ExpectedDraftVersion: int64(body.ExpectedDraftVersion),
		ExpectedDraftDigest: body.ExpectedDraftDigest,
	}
	if body.Query != nil {
		request.Query = *body.Query
	}
	if body.Cursor != nil {
		request.Cursor = *body.Cursor
	}
	if body.Limit != nil {
		request.Limit = *body.Limit
	}
	value, err := h.getConstructionInputsDirect(ctx, project, explorerID, request)
	if err != nil {
		return result, err
	}
	return directAuthoringJSON[loomapi.ConstructionInputsResponse](value)
}

func (r *HTTPRoutes) GetExplorerConstructionInputs(ctx context.Context, request loomapi.GetExplorerConstructionInputsRequestObject) (loomapi.GetExplorerConstructionInputsResponseObject, error) {
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "getExplorerConstructionInputs", explorerUnavailable("construction-inputs", "AUTHORING_UNAVAILABLE", "Explorer construction input catalog is not configured"))
		return loomapi.GetExplorerConstructionInputs503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.getConstructionInputsOpenAPIDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.GetExplorerConstructionInputs200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "getExplorerConstructionInputs", err)
	switch status {
	case http.StatusBadRequest:
		return loomapi.GetExplorerConstructionInputs400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case http.StatusUnauthorized:
		return loomapi.GetExplorerConstructionInputs401JSONResponse{ServiceUnauthorizedJSONResponse: authoringUnauthorizedResponse(failure)}, nil
	case http.StatusForbidden:
		return loomapi.GetExplorerConstructionInputs403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusNotFound:
		return loomapi.GetExplorerConstructionInputs404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.GetExplorerConstructionInputs409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.GetExplorerConstructionInputs422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.GetExplorerConstructionInputs500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.GetExplorerConstructionInputs503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("getExplorerConstructionInputs", status)
	}
}
