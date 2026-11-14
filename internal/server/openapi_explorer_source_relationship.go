package server

import (
	"context"
	"errors"
	"net/http"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func (h *explorerHTTPHandlers) proposeSourceRelationshipDirect(ctx context.Context, project, explorerID string, body *loomapi.SourceRelationshipProposalRequest) (loomapi.SourceRelationshipProposal, error) {
	var empty loomapi.SourceRelationshipProposal
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return empty, err
	}
	if body == nil {
		return empty, malformedRouteError("proposeExplorerSourceRelationship", nil)
	}
	input, err := directAuthoringJSON[lifecycle.SourceRelationshipProposalRequest](*body)
	if err != nil {
		return empty, malformedRouteError("proposeExplorerSourceRelationship", err)
	}
	input.Project, input.ExplorerID = project, explorerID
	var encoder *previewResponseEncoder
	if input.IncludePreview {
		input.PreviewSinkFactory = func(receipt *explorer.CompilationReceipt, columns []explorer.EmittedColumn) (func(map[string]any) error, error) {
			var err error
			encoder, err = newPreviewResponseEncoder(receipt, input.OutputID, columns, maxExplorerPreviewResponseBytes)
			if err != nil {
				return nil, err
			}
			return encoder.Visit, nil
		}
	}
	value, err := h.application.ProposeSourceRelationship(ctx, input)
	if err != nil {
		var authoringError *explorer.AuthoringError
		if input.IncludePreview && !errors.As(err, &authoringError) {
			return empty, previewRouteError(err)
		}
		return empty, err
	}
	result, err := directAuthoringJSON[loomapi.SourceRelationshipProposal](value)
	if err != nil {
		return empty, err
	}
	if input.IncludePreview {
		if value.NativePreview == nil || encoder == nil {
			return empty, previewRouteError(errors.New("source proposal preview sink was not configured"))
		}
		preview, err := previewResponseFromEncoder(encoder, value.NativePreview.Summary)
		if err != nil {
			return empty, err
		}
		result.Preview = &preview
	}
	return result, nil
}

func (r *HTTPRoutes) ProposeExplorerSourceRelationship(ctx context.Context, request loomapi.ProposeExplorerSourceRelationshipRequestObject) (loomapi.ProposeExplorerSourceRelationshipResponseObject, error) {
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerSourceRelationship", explorerUnavailable("construction-proposal", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.ProposeExplorerSourceRelationship503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.proposeSourceRelationshipDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.ProposeExplorerSourceRelationship200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerSourceRelationship", err)
	switch status {
	case http.StatusBadRequest:
		return loomapi.ProposeExplorerSourceRelationship400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case http.StatusUnauthorized:
		return loomapi.ProposeExplorerSourceRelationship401JSONResponse{ServiceUnauthorizedJSONResponse: authoringUnauthorizedResponse(failure)}, nil
	case http.StatusForbidden:
		return loomapi.ProposeExplorerSourceRelationship403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusNotFound:
		return loomapi.ProposeExplorerSourceRelationship404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.ProposeExplorerSourceRelationship409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusRequestEntityTooLarge:
		return loomapi.ProposeExplorerSourceRelationship413JSONResponse{AuthoringPayloadTooLargeJSONResponse: loomapi.AuthoringPayloadTooLargeJSONResponse(failure)}, nil
	case http.StatusTooManyRequests:
		return loomapi.ProposeExplorerSourceRelationship429JSONResponse{AuthoringTooManyRequestsJSONResponse: loomapi.AuthoringTooManyRequestsJSONResponse(failure)}, nil
	case 499:
		return loomapi.ProposeExplorerSourceRelationship499JSONResponse{AuthoringClientClosedRequestJSONResponse: loomapi.AuthoringClientClosedRequestJSONResponse(failure)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.ProposeExplorerSourceRelationship422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.ProposeExplorerSourceRelationship500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.ProposeExplorerSourceRelationship503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	case http.StatusGatewayTimeout:
		return loomapi.ProposeExplorerSourceRelationship504JSONResponse{AuthoringGatewayTimeoutJSONResponse: loomapi.AuthoringGatewayTimeoutJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("proposeExplorerSourceRelationship", status)
	}
}
