package server

import (
	"context"
	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"net/http"
)

func (h *explorerHTTPHandlers) proposeColumnPolicyDirect(ctx context.Context, project, explorerID string, body *loomapi.ColumnPolicyProposalRequest) (loomapi.ColumnPolicyProposal, error) {
	var empty loomapi.ColumnPolicyProposal
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return empty, err
	}
	if body == nil {
		return empty, malformedRouteError("proposeExplorerColumnPolicy", nil)
	}
	input, err := directAuthoringJSON[lifecycle.ColumnPolicyProposalRequest](*body)
	if err != nil {
		return empty, malformedRouteError("proposeExplorerColumnPolicy", err)
	}
	input.Project, input.ExplorerID = project, explorerID
	value, err := h.application.ProposeColumnPolicy(ctx, input)
	if err != nil {
		return empty, err
	}
	return directAuthoringJSON[loomapi.ColumnPolicyProposal](value)
}

func (r *HTTPRoutes) ProposeExplorerColumnPolicy(ctx context.Context, request loomapi.ProposeExplorerColumnPolicyRequestObject) (loomapi.ProposeExplorerColumnPolicyResponseObject, error) {
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerColumnPolicy", explorerUnavailable("column-policy-proposal", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.ProposeExplorerColumnPolicy503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.proposeColumnPolicyDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.ProposeExplorerColumnPolicy200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerColumnPolicy", err)
	switch status {
	case http.StatusBadRequest:
		return loomapi.ProposeExplorerColumnPolicy400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case http.StatusUnauthorized:
		return loomapi.ProposeExplorerColumnPolicy401JSONResponse{ServiceUnauthorizedJSONResponse: authoringUnauthorizedResponse(failure)}, nil
	case http.StatusForbidden:
		return loomapi.ProposeExplorerColumnPolicy403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusNotFound:
		return loomapi.ProposeExplorerColumnPolicy404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.ProposeExplorerColumnPolicy409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.ProposeExplorerColumnPolicy422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.ProposeExplorerColumnPolicy500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.ProposeExplorerColumnPolicy503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	case http.StatusGatewayTimeout:
		return loomapi.ProposeExplorerColumnPolicy504JSONResponse{AuthoringGatewayTimeoutJSONResponse: loomapi.AuthoringGatewayTimeoutJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("proposeExplorerColumnPolicy", status)
	}
}
