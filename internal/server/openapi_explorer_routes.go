package server

import (
	"context"
	"fmt"
	"net/http"

	loomapi "github.com/calypr/loom/generated/loomapi"
)

func (r *HTTPRoutes) ListExplorers(ctx context.Context, request loomapi.ListExplorersRequestObject) (loomapi.ListExplorersResponseObject, error) {
	value, err := r.explorer.listExplorers(ctx, string(request.Project))
	if err == nil {
		return loomapi.ListExplorers200JSONResponse(value), nil
	}
	status, legacy := explorerLegacyResponse(err)
	switch status {
	case http.StatusForbidden:
		return loomapi.ListExplorers403JSONResponse{LegacyForbiddenJSONResponse: loomapi.LegacyForbiddenJSONResponse(legacy)}, nil
	case http.StatusInternalServerError:
		return loomapi.ListExplorers500JSONResponse{LegacyInternalErrorJSONResponse: loomapi.LegacyInternalErrorJSONResponse(legacy)}, nil
	default:
		return nil, unexpectedResponseStatus("listExplorers", status)
	}
}

func (r *HTTPRoutes) CreateExplorer(ctx context.Context, request loomapi.CreateExplorerRequestObject) (loomapi.CreateExplorerResponseObject, error) {
	value, err := r.explorer.createExplorer(ctx, string(request.Project), authResourcePathFromParam(request.Params.AuthResourcePath), request.Body)
	if err == nil {
		return loomapi.CreateExplorer201JSONResponse(value), nil
	}
	status, legacy := explorerLegacyResponse(err)
	switch status {
	case http.StatusBadRequest:
		return loomapi.CreateExplorer400JSONResponse{LegacyBadRequestJSONResponse: loomapi.LegacyBadRequestJSONResponse(legacy)}, nil
	case http.StatusForbidden:
		return loomapi.CreateExplorer403JSONResponse{LegacyForbiddenJSONResponse: loomapi.LegacyForbiddenJSONResponse(legacy)}, nil
	case http.StatusConflict:
		return loomapi.CreateExplorer409JSONResponse{LegacyConflictJSONResponse: loomapi.LegacyConflictJSONResponse(legacy)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.CreateExplorer422JSONResponse{LegacyUnprocessableJSONResponse: loomapi.LegacyUnprocessableJSONResponse(legacy)}, nil
	case http.StatusInternalServerError:
		return loomapi.CreateExplorer500JSONResponse{LegacyInternalErrorJSONResponse: loomapi.LegacyInternalErrorJSONResponse(legacy)}, nil
	default:
		return nil, unexpectedResponseStatus("createExplorer", status)
	}
}

func (r *HTTPRoutes) GetExplorer(ctx context.Context, request loomapi.GetExplorerRequestObject) (loomapi.GetExplorerResponseObject, error) {
	value, err := r.explorer.getExplorer(ctx, string(request.Project), string(request.ExplorerId))
	if err == nil {
		return loomapi.GetExplorer200JSONResponse(value), nil
	}
	status, legacy := explorerLegacyResponse(err)
	switch status {
	case http.StatusForbidden:
		return loomapi.GetExplorer403JSONResponse{LegacyForbiddenJSONResponse: loomapi.LegacyForbiddenJSONResponse(legacy)}, nil
	case http.StatusNotFound:
		return loomapi.GetExplorer404JSONResponse{LegacyNotFoundJSONResponse: loomapi.LegacyNotFoundJSONResponse(legacy)}, nil
	case http.StatusInternalServerError:
		return loomapi.GetExplorer500JSONResponse{LegacyInternalErrorJSONResponse: loomapi.LegacyInternalErrorJSONResponse(legacy)}, nil
	default:
		return nil, unexpectedResponseStatus("getExplorer", status)
	}
}

func (r *HTTPRoutes) PublishRepositoryExplorerConfig(ctx context.Context, request loomapi.PublishRepositoryExplorerConfigRequestObject) (loomapi.PublishRepositoryExplorerConfigResponseObject, error) {
	value, err := r.explorer.publishRepositoryExplorerConfig(ctx, request)
	if err == nil {
		return loomapi.PublishRepositoryExplorerConfig200JSONResponse(value), nil
	}
	status, legacy := explorerLegacyResponse(err)
	switch status {
	case http.StatusBadRequest:
		return loomapi.PublishRepositoryExplorerConfig400JSONResponse{LegacyBadRequestJSONResponse: loomapi.LegacyBadRequestJSONResponse(legacy)}, nil
	case http.StatusForbidden:
		return loomapi.PublishRepositoryExplorerConfig403JSONResponse{LegacyForbiddenJSONResponse: loomapi.LegacyForbiddenJSONResponse(legacy)}, nil
	case http.StatusConflict:
		return loomapi.PublishRepositoryExplorerConfig409JSONResponse{LegacyConflictJSONResponse: loomapi.LegacyConflictJSONResponse(legacy)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.PublishRepositoryExplorerConfig422JSONResponse{LegacyUnprocessableJSONResponse: loomapi.LegacyUnprocessableJSONResponse(legacy)}, nil
	case http.StatusInternalServerError:
		return loomapi.PublishRepositoryExplorerConfig500JSONResponse{LegacyInternalErrorJSONResponse: loomapi.LegacyInternalErrorJSONResponse(legacy)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.PublishRepositoryExplorerConfig503JSONResponse{LegacyUnavailableJSONResponse: loomapi.LegacyUnavailableJSONResponse(legacy)}, nil
	default:
		return nil, unexpectedResponseStatus("publishRepositoryExplorerConfig", status)
	}
}

func (r *HTTPRoutes) GetExplorerAuthoringCapability(ctx context.Context, request loomapi.GetExplorerAuthoringCapabilityRequestObject) (loomapi.GetExplorerAuthoringCapabilityResponseObject, error) {
	if r == nil || r.explorer == nil {
		_, value := authoringErrorForOpenAPI(ctx, "getExplorerAuthoringCapability", fmt.Errorf("Explorer authoring is not configured"))
		return loomapi.GetExplorerAuthoringCapability500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(value)}, nil
	}
	value, err := r.explorer.getAuthoringCapabilityDirect(ctx, string(request.Project))
	if err == nil {
		return loomapi.GetExplorerAuthoringCapability200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "getExplorerAuthoringCapability", err)
	switch status {
	case http.StatusForbidden:
		return loomapi.GetExplorerAuthoringCapability403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.GetExplorerAuthoringCapability500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("getExplorerAuthoringCapability", status)
	}
}

func (r *HTTPRoutes) SearchExplorerCandidates(ctx context.Context, request loomapi.SearchExplorerCandidatesRequestObject) (loomapi.SearchExplorerCandidatesResponseObject, error) {
	if r == nil || r.explorer == nil {
		status, failure := authoringErrorForOpenAPI(ctx, "searchExplorerCandidates", explorerUnavailable("suggestions", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		if status == http.StatusServiceUnavailable {
			return loomapi.SearchExplorerCandidates503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
		}
		return nil, unexpectedResponseStatus("searchExplorerCandidates", status)
	}
	value, err := r.explorer.searchAuthoringSuggestionsDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.SearchExplorerCandidates200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "searchExplorerCandidates", err)
	switch status {
	case http.StatusBadRequest:
		return loomapi.SearchExplorerCandidates400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case http.StatusForbidden:
		return loomapi.SearchExplorerCandidates403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.SearchExplorerCandidates409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.SearchExplorerCandidates500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.SearchExplorerCandidates503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("searchExplorerCandidates", status)
	}
}

func (r *HTTPRoutes) GetExplorerBuilder(ctx context.Context, request loomapi.GetExplorerBuilderRequestObject) (loomapi.GetExplorerBuilderResponseObject, error) {
	if r == nil || r.explorer == nil {
		status, failure := authoringErrorForOpenAPI(ctx, "getExplorerBuilder", explorerUnavailable("builder", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		if status == http.StatusServiceUnavailable {
			return loomapi.GetExplorerBuilder503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
		}
		return nil, unexpectedResponseStatus("getExplorerBuilder", status)
	}
	value, err := r.explorer.getAuthoringBuilderDirect(ctx, string(request.Project), string(request.ExplorerId))
	if err == nil {
		return loomapi.GetExplorerBuilder200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "getExplorerBuilder", err)
	switch status {
	case http.StatusForbidden:
		return loomapi.GetExplorerBuilder403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusNotFound:
		return loomapi.GetExplorerBuilder404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.GetExplorerBuilder409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.GetExplorerBuilder500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.GetExplorerBuilder503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("getExplorerBuilder", status)
	}
}

func (r *HTTPRoutes) ApplyExplorerBuilderCommands(ctx context.Context, request loomapi.ApplyExplorerBuilderCommandsRequestObject) (loomapi.ApplyExplorerBuilderCommandsResponseObject, error) {
	if r == nil || r.explorer == nil {
		status, failure := authoringErrorForOpenAPI(ctx, "applyExplorerBuilderCommands", explorerUnavailable("commands", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		if status == http.StatusServiceUnavailable {
			return loomapi.ApplyExplorerBuilderCommands503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
		}
		return nil, unexpectedResponseStatus("applyExplorerBuilderCommands", status)
	}
	value, err := r.explorer.applyAuthoringCommandsDirect(ctx, string(request.Project), string(request.ExplorerId), authResourcePathFromParam(request.Params.AuthResourcePath), request.Body)
	if err == nil {
		return loomapi.ApplyExplorerBuilderCommands200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "applyExplorerBuilderCommands", err)
	switch status {
	case http.StatusBadRequest:
		return loomapi.ApplyExplorerBuilderCommands400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case http.StatusForbidden:
		return loomapi.ApplyExplorerBuilderCommands403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.ApplyExplorerBuilderCommands409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.ApplyExplorerBuilderCommands422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.ApplyExplorerBuilderCommands500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.ApplyExplorerBuilderCommands503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("applyExplorerBuilderCommands", status)
	}
}

func (r *HTTPRoutes) ReconcileExplorerBuilder(ctx context.Context, request loomapi.ReconcileExplorerBuilderRequestObject) (loomapi.ReconcileExplorerBuilderResponseObject, error) {
	project := string(request.Project)
	explorerID := string(request.ExplorerId)
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "reconcileExplorerBuilder", explorerUnavailable("reconcile", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.ReconcileExplorerBuilder503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.reconcileAuthoringDirect(ctx, project, explorerID, authResourcePathFromParam(request.Params.AuthResourcePath), request.Body)
	if err == nil {
		return loomapi.ReconcileExplorerBuilder200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "reconcileExplorerBuilder", err)
	switch status {
	case http.StatusBadRequest:
		return loomapi.ReconcileExplorerBuilder400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case http.StatusForbidden:
		return loomapi.ReconcileExplorerBuilder403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusNotFound:
		return loomapi.ReconcileExplorerBuilder404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.ReconcileExplorerBuilder409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.ReconcileExplorerBuilder422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.ReconcileExplorerBuilder500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.ReconcileExplorerBuilder503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("reconcileExplorerBuilder", status)
	}
}

func (r *HTTPRoutes) PreviewExplorer(ctx context.Context, request loomapi.PreviewExplorerRequestObject) (loomapi.PreviewExplorerResponseObject, error) {
	project := string(request.Project)
	explorerID := string(request.ExplorerId)
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "previewExplorer", explorerUnavailable("preview", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.PreviewExplorer503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.previewAuthoringDirect(ctx, project, explorerID, request.Body)
	if err == nil {
		return loomapi.PreviewExplorer200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "previewExplorer", err)
	switch status {
	case http.StatusBadRequest:
		return loomapi.PreviewExplorer400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case http.StatusForbidden:
		return loomapi.PreviewExplorer403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusNotFound:
		return loomapi.PreviewExplorer404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.PreviewExplorer409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusRequestEntityTooLarge:
		return loomapi.PreviewExplorer413JSONResponse{AuthoringPayloadTooLargeJSONResponse: loomapi.AuthoringPayloadTooLargeJSONResponse(failure)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.PreviewExplorer422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case http.StatusTooManyRequests:
		return loomapi.PreviewExplorer429JSONResponse{AuthoringTooManyRequestsJSONResponse: loomapi.AuthoringTooManyRequestsJSONResponse(failure)}, nil
	case 499:
		return loomapi.PreviewExplorer499JSONResponse{AuthoringClientClosedRequestJSONResponse: loomapi.AuthoringClientClosedRequestJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.PreviewExplorer500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.PreviewExplorer503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	case http.StatusGatewayTimeout:
		return loomapi.PreviewExplorer504JSONResponse{AuthoringGatewayTimeoutJSONResponse: loomapi.AuthoringGatewayTimeoutJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("previewExplorer", status)
	}
}

func (r *HTTPRoutes) PublishExplorer(ctx context.Context, request loomapi.PublishExplorerRequestObject) (loomapi.PublishExplorerResponseObject, error) {
	project := string(request.Project)
	explorerID := string(request.ExplorerId)
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "publishExplorer", explorerUnavailable("publish", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.PublishExplorer503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.publishAuthoringDirect(ctx, project, explorerID, authResourcePathFromParam(request.Params.AuthResourcePath), request.Body)
	if err == nil {
		return loomapi.PublishExplorer200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "publishExplorer", err)
	switch status {
	case http.StatusBadRequest:
		return loomapi.PublishExplorer400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case http.StatusForbidden:
		return loomapi.PublishExplorer403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusNotFound:
		return loomapi.PublishExplorer404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.PublishExplorer409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.PublishExplorer422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.PublishExplorer500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.PublishExplorer503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("publishExplorer", status)
	}
}
