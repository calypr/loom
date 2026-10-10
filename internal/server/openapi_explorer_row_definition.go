package server

import (
	"context"
	"encoding/json"
	"net/http"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func (h *explorerHTTPHandlers) listRowDefinitionChoicesDirect(ctx context.Context, project, explorerID string, params loomapi.ListExplorerRowDefinitionChoicesParams) (loomapi.RowDefinitionChoicesResponse, error) {
	var result loomapi.RowDefinitionChoicesResponse
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	value, err := h.application.ListRowDefinitionChoices(ctx, lifecycle.RowDefinitionChoicesRequest{
		Project: project, ExplorerID: explorerID, OutputID: params.OutputId, SnapshotToken: params.SnapshotToken,
	})
	if err != nil {
		return result, err
	}
	return directAuthoringJSON[loomapi.RowDefinitionChoicesResponse](value)
}

func (h *explorerHTTPHandlers) proposeRowDefinitionDirect(ctx context.Context, project, explorerID string, body *loomapi.RowDefinitionProposalRequest) (loomapi.RowDefinitionProposal, error) {
	var result loomapi.RowDefinitionProposal
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("row-definition-proposal", nil)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return result, malformedRouteError("row-definition-proposal", err)
	}
	var input struct {
		SnapshotToken        string                           `json:"snapshotToken"`
		ExpectedDraftVersion int64                            `json:"expectedDraftVersion"`
		ExpectedDraftDigest  string                           `json:"expectedDraftDigest"`
		OutputID             string                           `json:"outputId"`
		Selection            lifecycle.RowDefinitionSelection `json:"selection"`
		Limit                int                              `json:"limit,omitempty"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return result, malformedRouteError("row-definition-proposal", err)
	}
	value, err := h.application.ProposeRowDefinition(ctx, lifecycle.RowDefinitionProposalRequest{
		Project: project, ExplorerID: explorerID, SnapshotToken: input.SnapshotToken,
		ExpectedDraftVersion: input.ExpectedDraftVersion, ExpectedDraftDigest: input.ExpectedDraftDigest,
		OutputID: input.OutputID, Selection: input.Selection, Limit: input.Limit,
	})
	if err != nil {
		return result, err
	}
	return directAuthoringJSON[loomapi.RowDefinitionProposal](value)
}

func (r *HTTPRoutes) ListExplorerRowDefinitionChoices(ctx context.Context, request loomapi.ListExplorerRowDefinitionChoicesRequestObject) (loomapi.ListExplorerRowDefinitionChoicesResponseObject, error) {
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "listExplorerRowDefinitionChoices", explorerUnavailable("row-definition-choices", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.ListExplorerRowDefinitionChoices503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.listRowDefinitionChoicesDirect(ctx, string(request.Project), string(request.ExplorerId), request.Params)
	if err == nil {
		return loomapi.ListExplorerRowDefinitionChoices200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "listExplorerRowDefinitionChoices", err)
	switch status {
	case http.StatusBadRequest:
		return loomapi.ListExplorerRowDefinitionChoices400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case http.StatusUnauthorized:
		return loomapi.ListExplorerRowDefinitionChoices401JSONResponse{ServiceUnauthorizedJSONResponse: authoringUnauthorizedResponse(failure)}, nil
	case http.StatusForbidden:
		return loomapi.ListExplorerRowDefinitionChoices403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusNotFound:
		return loomapi.ListExplorerRowDefinitionChoices404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.ListExplorerRowDefinitionChoices409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.ListExplorerRowDefinitionChoices422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.ListExplorerRowDefinitionChoices500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.ListExplorerRowDefinitionChoices503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("listExplorerRowDefinitionChoices", status)
	}
}

func (r *HTTPRoutes) ProposeExplorerRowDefinition(ctx context.Context, request loomapi.ProposeExplorerRowDefinitionRequestObject) (loomapi.ProposeExplorerRowDefinitionResponseObject, error) {
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerRowDefinition", explorerUnavailable("row-definition-proposal", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.ProposeExplorerRowDefinition503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.proposeRowDefinitionDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.ProposeExplorerRowDefinition200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerRowDefinition", err)
	switch status {
	case http.StatusBadRequest:
		return loomapi.ProposeExplorerRowDefinition400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case http.StatusUnauthorized:
		return loomapi.ProposeExplorerRowDefinition401JSONResponse{ServiceUnauthorizedJSONResponse: authoringUnauthorizedResponse(failure)}, nil
	case http.StatusForbidden:
		return loomapi.ProposeExplorerRowDefinition403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusNotFound:
		return loomapi.ProposeExplorerRowDefinition404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.ProposeExplorerRowDefinition409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.ProposeExplorerRowDefinition422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.ProposeExplorerRowDefinition500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.ProposeExplorerRowDefinition503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("proposeExplorerRowDefinition", status)
	}
}
