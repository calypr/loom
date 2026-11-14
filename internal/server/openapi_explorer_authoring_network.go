package server

import (
	"context"
	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"net/http"
)

func (h *explorerHTTPHandlers) describeAuthoringNetworkDirect(ctx context.Context, project, explorerID string, body *loomapi.AuthoringNetworkRequest) (loomapi.AuthoringNetworkResponse, error) {
	var empty loomapi.AuthoringNetworkResponse
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return empty, err
	}
	if body == nil {
		return empty, malformedRouteError("describeExplorerAuthoringNetwork", nil)
	}
	input, err := directAuthoringJSON[lifecycle.AuthoringNetworkRequest](*body)
	if err != nil {
		return empty, malformedRouteError("describeExplorerAuthoringNetwork", err)
	}
	input.Project, input.ExplorerID = project, explorerID
	value, err := h.application.DescribeAuthoringNetwork(ctx, input)
	if err != nil {
		return empty, err
	}
	return directAuthoringJSON[loomapi.AuthoringNetworkResponse](value)
}

func (r *HTTPRoutes) DescribeExplorerAuthoringNetwork(ctx context.Context, request loomapi.DescribeExplorerAuthoringNetworkRequestObject) (loomapi.DescribeExplorerAuthoringNetworkResponseObject, error) {
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "describeExplorerAuthoringNetwork", explorerUnavailable("construction-proposal", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.DescribeExplorerAuthoringNetwork503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.describeAuthoringNetworkDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.DescribeExplorerAuthoringNetwork200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "describeExplorerAuthoringNetwork", err)
	switch status {
	case http.StatusBadRequest:
		return loomapi.DescribeExplorerAuthoringNetwork400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case http.StatusUnauthorized:
		return loomapi.DescribeExplorerAuthoringNetwork401JSONResponse{ServiceUnauthorizedJSONResponse: authoringUnauthorizedResponse(failure)}, nil
	case http.StatusForbidden:
		return loomapi.DescribeExplorerAuthoringNetwork403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusNotFound:
		return loomapi.DescribeExplorerAuthoringNetwork404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.DescribeExplorerAuthoringNetwork409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.DescribeExplorerAuthoringNetwork422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.DescribeExplorerAuthoringNetwork500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.DescribeExplorerAuthoringNetwork503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	case http.StatusGatewayTimeout:
		return loomapi.DescribeExplorerAuthoringNetwork504JSONResponse{AuthoringGatewayTimeoutJSONResponse: loomapi.AuthoringGatewayTimeoutJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("describeExplorerAuthoringNetwork", status)
	}
}
