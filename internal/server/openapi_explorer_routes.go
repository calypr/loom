package server

import (
	"context"
	"net/http"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/gofiber/fiber/v3"
)

func (r *HTTPRoutes) ListExplorers(ctx context.Context, _ loomapi.ListExplorersRequestObject) (loomapi.ListExplorersResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.explorer.lifecycle.list)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		response, decodeErr := decodeResponse[loomapi.ListExplorers200JSONResponse](body)
		return response, decodeErr
	}
	value, decodeErr := legacyError(body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	switch status {
	case 403:
		return loomapi.ListExplorers403JSONResponse{LegacyForbiddenJSONResponse: loomapi.LegacyForbiddenJSONResponse(value)}, nil
	case 500:
		return loomapi.ListExplorers500JSONResponse{LegacyInternalErrorJSONResponse: loomapi.LegacyInternalErrorJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("listExplorers", status)
	}
}

func (r *HTTPRoutes) CreateExplorer(ctx context.Context, _ loomapi.CreateExplorerRequestObject) (loomapi.CreateExplorerResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.explorer.lifecycle.create)
	if err != nil {
		return nil, err
	}
	if status == http.StatusCreated {
		response, decodeErr := decodeResponse[loomapi.CreateExplorer201JSONResponse](body)
		return response, decodeErr
	}
	value, decodeErr := legacyError(body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	switch status {
	case 400:
		return loomapi.CreateExplorer400JSONResponse{LegacyBadRequestJSONResponse: loomapi.LegacyBadRequestJSONResponse(value)}, nil
	case 403:
		return loomapi.CreateExplorer403JSONResponse{LegacyForbiddenJSONResponse: loomapi.LegacyForbiddenJSONResponse(value)}, nil
	case 409:
		return loomapi.CreateExplorer409JSONResponse{LegacyConflictJSONResponse: loomapi.LegacyConflictJSONResponse(value)}, nil
	case 422:
		return loomapi.CreateExplorer422JSONResponse{LegacyUnprocessableJSONResponse: loomapi.LegacyUnprocessableJSONResponse(value)}, nil
	case 500:
		return loomapi.CreateExplorer500JSONResponse{LegacyInternalErrorJSONResponse: loomapi.LegacyInternalErrorJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("createExplorer", status)
	}
}

func (r *HTTPRoutes) GetExplorer(ctx context.Context, _ loomapi.GetExplorerRequestObject) (loomapi.GetExplorerResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.explorer.lifecycle.get)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		response, decodeErr := decodeResponse[loomapi.GetExplorer200JSONResponse](body)
		return response, decodeErr
	}
	value, decodeErr := legacyError(body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	switch status {
	case 403:
		return loomapi.GetExplorer403JSONResponse{LegacyForbiddenJSONResponse: loomapi.LegacyForbiddenJSONResponse(value)}, nil
	case 404:
		return loomapi.GetExplorer404JSONResponse{LegacyNotFoundJSONResponse: loomapi.LegacyNotFoundJSONResponse(value)}, nil
	case 500:
		return loomapi.GetExplorer500JSONResponse{LegacyInternalErrorJSONResponse: loomapi.LegacyInternalErrorJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("getExplorer", status)
	}
}

func (r *HTTPRoutes) PublishRepositoryExplorerConfig(ctx context.Context, _ loomapi.PublishRepositoryExplorerConfigRequestObject) (loomapi.PublishRepositoryExplorerConfigResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.explorer.publishRepositoryConfig)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		response, decodeErr := decodeResponse[loomapi.PublishRepositoryExplorerConfig200JSONResponse](body)
		return response, decodeErr
	}
	value, decodeErr := legacyError(body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	switch status {
	case 400:
		return loomapi.PublishRepositoryExplorerConfig400JSONResponse{LegacyBadRequestJSONResponse: loomapi.LegacyBadRequestJSONResponse(value)}, nil
	case 403:
		return loomapi.PublishRepositoryExplorerConfig403JSONResponse{LegacyForbiddenJSONResponse: loomapi.LegacyForbiddenJSONResponse(value)}, nil
	case 409:
		return loomapi.PublishRepositoryExplorerConfig409JSONResponse{LegacyConflictJSONResponse: loomapi.LegacyConflictJSONResponse(value)}, nil
	case 422:
		return loomapi.PublishRepositoryExplorerConfig422JSONResponse{LegacyUnprocessableJSONResponse: loomapi.LegacyUnprocessableJSONResponse(value)}, nil
	case 500:
		return loomapi.PublishRepositoryExplorerConfig500JSONResponse{LegacyInternalErrorJSONResponse: loomapi.LegacyInternalErrorJSONResponse(value)}, nil
	case 503:
		return loomapi.PublishRepositoryExplorerConfig503JSONResponse{LegacyUnavailableJSONResponse: loomapi.LegacyUnavailableJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("publishRepositoryExplorerConfig", status)
	}
}

func (r *HTTPRoutes) GetRepositoryExplorerConfig(ctx context.Context, _ loomapi.GetRepositoryExplorerConfigRequestObject) (loomapi.GetRepositoryExplorerConfigResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.explorer.getRepositoryConfig)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		response, decodeErr := decodeResponse[loomapi.GetRepositoryExplorerConfig200JSONResponse](body)
		return response, decodeErr
	}
	value, decodeErr := legacyError(body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	switch status {
	case 403:
		return loomapi.GetRepositoryExplorerConfig403JSONResponse{LegacyForbiddenJSONResponse: loomapi.LegacyForbiddenJSONResponse(value)}, nil
	case 404:
		return loomapi.GetRepositoryExplorerConfig404JSONResponse{LegacyNotFoundJSONResponse: loomapi.LegacyNotFoundJSONResponse(value)}, nil
	case 500:
		return loomapi.GetRepositoryExplorerConfig500JSONResponse{LegacyInternalErrorJSONResponse: loomapi.LegacyInternalErrorJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("getRepositoryExplorerConfig", status)
	}
}

func (r *HTTPRoutes) GetExplorerAuthoringCapability(ctx context.Context, _ loomapi.GetExplorerAuthoringCapabilityRequestObject) (loomapi.GetExplorerAuthoringCapabilityResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.explorer.authoring.getCapability)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		value, e := decodeResponse[loomapi.AuthoringCapability](body)
		return loomapi.GetExplorerAuthoringCapability200JSONResponse(value), e
	}
	value, _, e := authoringError(body, status)
	if e != nil {
		return nil, e
	}
	switch status {
	case 403:
		return loomapi.GetExplorerAuthoringCapability403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(value)}, nil
	case 500:
		return loomapi.GetExplorerAuthoringCapability500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("getExplorerAuthoringCapability", status)
	}
}

func (r *HTTPRoutes) SearchExplorerCandidates(ctx context.Context, _ loomapi.SearchExplorerCandidatesRequestObject) (loomapi.SearchExplorerCandidatesResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.explorer.authoring.searchSuggestions)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		value, e := decodeResponse[loomapi.CandidateSearchResponse](body)
		return loomapi.SearchExplorerCandidates200JSONResponse(value), e
	}
	value, _, e := authoringError(body, status)
	if e != nil {
		return nil, e
	}
	switch status {
	case 400:
		return loomapi.SearchExplorerCandidates400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(value)}, nil
	case 403:
		return loomapi.SearchExplorerCandidates403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(value)}, nil
	case 409:
		return loomapi.SearchExplorerCandidates409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(value)}, nil
	case 500:
		return loomapi.SearchExplorerCandidates500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(value)}, nil
	case 503:
		return loomapi.SearchExplorerCandidates503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("searchExplorerCandidates", status)
	}
}

func (r *HTTPRoutes) GetExplorerCandidateSuggestions(ctx context.Context, _ loomapi.GetExplorerCandidateSuggestionsRequestObject) (loomapi.GetExplorerCandidateSuggestionsResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.explorer.authoring.getCandidateSuggestions)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		value, e := decodeResponse[loomapi.CandidateSuggestions](body)
		return loomapi.GetExplorerCandidateSuggestions200JSONResponse(value), e
	}
	value, _, e := authoringError(body, status)
	if e != nil {
		return nil, e
	}
	switch status {
	case 400:
		return loomapi.GetExplorerCandidateSuggestions400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(value)}, nil
	case 403:
		return loomapi.GetExplorerCandidateSuggestions403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(value)}, nil
	case 404:
		return loomapi.GetExplorerCandidateSuggestions404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(value)}, nil
	case 409:
		return loomapi.GetExplorerCandidateSuggestions409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(value)}, nil
	case 500:
		return loomapi.GetExplorerCandidateSuggestions500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(value)}, nil
	case 503:
		return loomapi.GetExplorerCandidateSuggestions503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("getExplorerCandidateSuggestions", status)
	}
}

func (r *HTTPRoutes) GetExplorerBuilder(ctx context.Context, _ loomapi.GetExplorerBuilderRequestObject) (loomapi.GetExplorerBuilderResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.explorer.authoring.getBuilder)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		value, e := decodeResponse[loomapi.BuilderState](body)
		return loomapi.GetExplorerBuilder200JSONResponse(value), e
	}
	value, _, e := authoringError(body, status)
	if e != nil {
		return nil, e
	}
	switch status {
	case 403:
		return loomapi.GetExplorerBuilder403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(value)}, nil
	case 404:
		return loomapi.GetExplorerBuilder404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(value)}, nil
	case 409:
		return loomapi.GetExplorerBuilder409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(value)}, nil
	case 500:
		return loomapi.GetExplorerBuilder500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(value)}, nil
	case 503:
		return loomapi.GetExplorerBuilder503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("getExplorerBuilder", status)
	}
}

func (r *HTTPRoutes) SaveExplorerDraft(ctx context.Context, _ loomapi.SaveExplorerDraftRequestObject) (loomapi.SaveExplorerDraftResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.explorer.authoring.saveDraft)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		value, e := decodeResponse[loomapi.SaveDraftResponse](body)
		return loomapi.SaveExplorerDraft200JSONResponse(value), e
	}
	value, _, e := authoringError(body, status)
	if e != nil {
		return nil, e
	}
	switch status {
	case 400:
		return loomapi.SaveExplorerDraft400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(value)}, nil
	case 403:
		return loomapi.SaveExplorerDraft403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(value)}, nil
	case 404:
		return loomapi.SaveExplorerDraft404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(value)}, nil
	case 409:
		return loomapi.SaveExplorerDraft409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(value)}, nil
	case 422:
		return loomapi.SaveExplorerDraft422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(value)}, nil
	case 500:
		return loomapi.SaveExplorerDraft500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(value)}, nil
	case 503:
		return loomapi.SaveExplorerDraft503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("saveExplorerDraft", status)
	}
}

func (r *HTTPRoutes) ExportExplorerWorkspace(ctx context.Context, _ loomapi.ExportExplorerWorkspaceRequestObject) (loomapi.ExportExplorerWorkspaceResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.explorer.authoring.exportWorkspace)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		value, e := decodeResponse[loomapi.Workspace](body)
		return loomapi.ExportExplorerWorkspace200JSONResponse(value), e
	}
	value, _, e := authoringError(body, status)
	if e != nil {
		return nil, e
	}
	switch status {
	case 403:
		return loomapi.ExportExplorerWorkspace403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(value)}, nil
	case 404:
		return loomapi.ExportExplorerWorkspace404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(value)}, nil
	case 409:
		return loomapi.ExportExplorerWorkspace409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(value)}, nil
	case 500:
		return loomapi.ExportExplorerWorkspace500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("exportExplorerWorkspace", status)
	}
}

func (r *HTTPRoutes) ApplyExplorerBuilderCommands(ctx context.Context, _ loomapi.ApplyExplorerBuilderCommandsRequestObject) (loomapi.ApplyExplorerBuilderCommandsResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.explorer.authoring.applyCommands)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		value, e := decodeResponse[loomapi.ApplyCommandsResponse](body)
		return loomapi.ApplyExplorerBuilderCommands200JSONResponse(value), e
	}
	value, _, e := authoringError(body, status)
	if e != nil {
		return nil, e
	}
	switch status {
	case 400:
		return loomapi.ApplyExplorerBuilderCommands400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(value)}, nil
	case 403:
		return loomapi.ApplyExplorerBuilderCommands403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(value)}, nil
	case 409:
		return loomapi.ApplyExplorerBuilderCommands409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(value)}, nil
	case 422:
		return loomapi.ApplyExplorerBuilderCommands422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(value)}, nil
	case 500:
		return loomapi.ApplyExplorerBuilderCommands500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(value)}, nil
	case 503:
		return loomapi.ApplyExplorerBuilderCommands503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("applyExplorerBuilderCommands", status)
	}
}

func (r *HTTPRoutes) compileExplorer(ctx context.Context, handler fiber.Handler) (loomapi.CompileResponse, loomapi.ErrorResponse, int, error) {
	status, body, err := runFiberHandler(ctx, handler)
	if err != nil {
		return loomapi.CompileResponse{}, loomapi.ErrorResponse{}, 0, err
	}
	if status == http.StatusOK {
		value, e := decodeResponse[loomapi.CompileResponse](body)
		return value, loomapi.ErrorResponse{}, status, e
	}
	value, _, e := authoringError(body, status)
	return loomapi.CompileResponse{}, value, status, e
}

func (r *HTTPRoutes) CompileExplorerBuilder(ctx context.Context, _ loomapi.CompileExplorerBuilderRequestObject) (loomapi.CompileExplorerBuilderResponseObject, error) {
	value, failure, status, err := r.compileExplorer(ctx, r.explorer.authoring.compileBuilder)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		return loomapi.CompileExplorerBuilder200JSONResponse(value), nil
	}
	switch status {
	case 400:
		return loomapi.CompileExplorerBuilder400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case 403:
		return loomapi.CompileExplorerBuilder403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case 409:
		return loomapi.CompileExplorerBuilder409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case 422:
		return loomapi.CompileExplorerBuilder422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case 500:
		return loomapi.CompileExplorerBuilder500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case 503:
		return loomapi.CompileExplorerBuilder503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("compileExplorerBuilder", status)
	}
}

func (r *HTTPRoutes) CompileExplorer(ctx context.Context, _ loomapi.CompileExplorerRequestObject) (loomapi.CompileExplorerResponseObject, error) {
	value, failure, status, err := r.compileExplorer(ctx, r.explorer.authoring.compile)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		return loomapi.CompileExplorer200JSONResponse(value), nil
	}
	switch status {
	case 400:
		return loomapi.CompileExplorer400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case 403:
		return loomapi.CompileExplorer403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case 409:
		return loomapi.CompileExplorer409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case 422:
		return loomapi.CompileExplorer422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case 500:
		return loomapi.CompileExplorer500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case 503:
		return loomapi.CompileExplorer503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("compileExplorer", status)
	}
}

func (r *HTTPRoutes) ReconcileExplorerBuilder(ctx context.Context, _ loomapi.ReconcileExplorerBuilderRequestObject) (loomapi.ReconcileExplorerBuilderResponseObject, error) {
	value, failure, status, err := r.compileExplorer(ctx, r.explorer.authoring.reconcile)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		return loomapi.ReconcileExplorerBuilder200JSONResponse(value), nil
	}
	switch status {
	case 400:
		return loomapi.ReconcileExplorerBuilder400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case 403:
		return loomapi.ReconcileExplorerBuilder403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case 404:
		return loomapi.ReconcileExplorerBuilder404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(failure)}, nil
	case 409:
		return loomapi.ReconcileExplorerBuilder409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case 422:
		return loomapi.ReconcileExplorerBuilder422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case 500:
		return loomapi.ReconcileExplorerBuilder500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case 503:
		return loomapi.ReconcileExplorerBuilder503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("reconcileExplorerBuilder", status)
	}
}

func (r *HTTPRoutes) PreviewExplorer(ctx context.Context, _ loomapi.PreviewExplorerRequestObject) (loomapi.PreviewExplorerResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.explorer.authoring.preview)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		value, e := decodeResponse[loomapi.PreviewResponse](body)
		return loomapi.PreviewExplorer200JSONResponse(value), e
	}
	value, _, e := authoringError(body, status)
	if e != nil {
		return nil, e
	}
	switch status {
	case 400:
		return loomapi.PreviewExplorer400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(value)}, nil
	case 403:
		return loomapi.PreviewExplorer403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(value)}, nil
	case 404:
		return loomapi.PreviewExplorer404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(value)}, nil
	case 409:
		return loomapi.PreviewExplorer409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(value)}, nil
	case 413:
		return loomapi.PreviewExplorer413JSONResponse{AuthoringPayloadTooLargeJSONResponse: loomapi.AuthoringPayloadTooLargeJSONResponse(value)}, nil
	case 422:
		return loomapi.PreviewExplorer422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(value)}, nil
	case 429:
		return loomapi.PreviewExplorer429JSONResponse{AuthoringTooManyRequestsJSONResponse: loomapi.AuthoringTooManyRequestsJSONResponse(value)}, nil
	case 499:
		return loomapi.PreviewExplorer499JSONResponse{AuthoringClientClosedRequestJSONResponse: loomapi.AuthoringClientClosedRequestJSONResponse(value)}, nil
	case 500:
		return loomapi.PreviewExplorer500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(value)}, nil
	case 503:
		return loomapi.PreviewExplorer503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(value)}, nil
	case 504:
		return loomapi.PreviewExplorer504JSONResponse{AuthoringGatewayTimeoutJSONResponse: loomapi.AuthoringGatewayTimeoutJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("previewExplorer", status)
	}
}

func (r *HTTPRoutes) PublishExplorer(ctx context.Context, _ loomapi.PublishExplorerRequestObject) (loomapi.PublishExplorerResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.explorer.authoring.publish)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		value, e := decodeResponse[loomapi.PublishResponse](body)
		return loomapi.PublishExplorer200JSONResponse(value), e
	}
	value, _, e := authoringError(body, status)
	if e != nil {
		return nil, e
	}
	switch status {
	case 400:
		return loomapi.PublishExplorer400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(value)}, nil
	case 403:
		return loomapi.PublishExplorer403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(value)}, nil
	case 404:
		return loomapi.PublishExplorer404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(value)}, nil
	case 409:
		return loomapi.PublishExplorer409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(value)}, nil
	case 422:
		return loomapi.PublishExplorer422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(value)}, nil
	case 500:
		return loomapi.PublishExplorer500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(value)}, nil
	case 503:
		return loomapi.PublishExplorer503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("publishExplorer", status)
	}
}
