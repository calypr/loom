package server

import (
	"context"
	"net/http"

	loomapi "github.com/calypr/loom/generated/loomapi"
)

func (r *HTTPRoutes) ListExplorers(ctx context.Context, request loomapi.ListExplorersRequestObject) (loomapi.ListExplorersResponseObject, error) {
	value, err := r.explorer.listExplorers(ctx, string(request.Project))
	if err == nil {
		return loomapi.ListExplorers200JSONResponse(value), nil
	}
	status, legacy := explorerLegacyResponse(err)
	return loomapi.NewListExplorersErrorResponse(status, legacy)
}

func (r *HTTPRoutes) CreateExplorer(ctx context.Context, request loomapi.CreateExplorerRequestObject) (loomapi.CreateExplorerResponseObject, error) {
	value, err := r.explorer.createExplorer(ctx, string(request.Project), authResourcePathFromParam(request.Params.AuthResourcePath), request.Body)
	if err == nil {
		return loomapi.CreateExplorer201JSONResponse(value), nil
	}
	status, legacy := explorerLegacyResponse(err)
	return loomapi.NewCreateExplorerErrorResponse(status, legacy)
}

func (r *HTTPRoutes) GetExplorer(ctx context.Context, request loomapi.GetExplorerRequestObject) (loomapi.GetExplorerResponseObject, error) {
	value, err := r.explorer.getExplorer(ctx, string(request.Project), string(request.ExplorerId))
	if err == nil {
		return loomapi.GetExplorer200JSONResponse(value), nil
	}
	status, legacy := explorerLegacyResponse(err)
	return loomapi.NewGetExplorerErrorResponse(status, legacy)
}

func (r *HTTPRoutes) PublishRepositoryExplorerConfig(ctx context.Context, request loomapi.PublishRepositoryExplorerConfigRequestObject) (loomapi.PublishRepositoryExplorerConfigResponseObject, error) {
	value, err := r.explorer.publishRepositoryExplorerConfig(ctx, request)
	if err == nil {
		return loomapi.PublishRepositoryExplorerConfig200JSONResponse(value), nil
	}
	status, legacy := explorerLegacyResponse(err)
	return loomapi.NewPublishRepositoryExplorerConfigErrorResponse(status, legacy)
}

func (r *HTTPRoutes) GetExplorerAuthoringCapability(ctx context.Context, request loomapi.GetExplorerAuthoringCapabilityRequestObject) (loomapi.GetExplorerAuthoringCapabilityResponseObject, error) {
	var err error
	if r == nil || r.explorer == nil {
		err = explorerUnavailable("capability", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured")
	} else {
		value, valueErr := r.explorer.getAuthoringCapabilityDirect(ctx, string(request.Project))
		if valueErr == nil {
			return loomapi.GetExplorerAuthoringCapability200JSONResponse(value), nil
		}
		err = valueErr
	}
	status, failure := authoringErrorForOpenAPI(ctx, "getExplorerAuthoringCapability", err)
	return loomapi.NewGetExplorerAuthoringCapabilityErrorResponse(status, openAPIErrorResponseBody(status, failure))
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
	return loomapi.NewSearchExplorerCandidatesErrorResponse(status, openAPIErrorResponseBody(status, failure))
}

func (r *HTTPRoutes) SearchExplorerSchemaFields(ctx context.Context, request loomapi.SearchExplorerSchemaFieldsRequestObject) (loomapi.SearchExplorerSchemaFieldsResponseObject, error) {
	if r == nil || r.explorer == nil {
		status, failure := authoringErrorForOpenAPI(ctx, "searchExplorerSchemaFields", explorerUnavailable("schema-fields", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		if status == http.StatusServiceUnavailable {
			return loomapi.SearchExplorerSchemaFields503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
		}
		return nil, unexpectedResponseStatus("searchExplorerSchemaFields", status)
	}
	value, err := r.explorer.searchAuthoringSchemaFieldsDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.SearchExplorerSchemaFields200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "searchExplorerSchemaFields", err)
	return loomapi.NewSearchExplorerSchemaFieldsErrorResponse(status, openAPIErrorResponseBody(status, failure))
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
	return loomapi.NewGetExplorerBuilderErrorResponse(status, openAPIErrorResponseBody(status, failure))
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
	return loomapi.NewApplyExplorerBuilderCommandsErrorResponse(status, openAPIErrorResponseBody(status, failure))
}

func (r *HTTPRoutes) AssessExplorerRowChange(ctx context.Context, request loomapi.AssessExplorerRowChangeRequestObject) (loomapi.AssessExplorerRowChangeResponseObject, error) {
	if r == nil || r.explorer == nil {
		status, failure := authoringErrorForOpenAPI(ctx, "assessExplorerRowChange", explorerUnavailable("row-change", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		if status == http.StatusServiceUnavailable {
			return loomapi.AssessExplorerRowChange503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
		}
		return nil, unexpectedResponseStatus("assessExplorerRowChange", status)
	}
	value, err := r.explorer.assessAuthoringRowChangeDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.AssessExplorerRowChange200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "assessExplorerRowChange", err)
	return loomapi.NewAssessExplorerRowChangeErrorResponse(status, openAPIErrorResponseBody(status, failure))
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
	return loomapi.NewReconcileExplorerBuilderErrorResponse(status, openAPIErrorResponseBody(status, failure))
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
	return loomapi.NewPreviewExplorerErrorResponse(status, openAPIErrorResponseBody(status, failure))
}

func (r *HTTPRoutes) TraceExplorerCell(ctx context.Context, request loomapi.TraceExplorerCellRequestObject) (loomapi.TraceExplorerCellResponseObject, error) {
	if r == nil || r.explorer == nil {
		status, failure := authoringErrorForOpenAPI(ctx, "traceExplorerCell", explorerUnavailable("trace", "TRACE_UNAVAILABLE", "Explorer cell tracing is not configured"))
		if status == http.StatusServiceUnavailable {
			return loomapi.TraceExplorerCell503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
		}
		return nil, unexpectedResponseStatus("traceExplorerCell", status)
	}
	value, err := r.explorer.traceExplorerCellDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.TraceExplorerCell200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "traceExplorerCell", err)
	return loomapi.NewTraceExplorerCellErrorResponse(status, openAPIErrorResponseBody(status, failure))
}

func (r *HTTPRoutes) TraceExplorerRowLineage(ctx context.Context, request loomapi.TraceExplorerRowLineageRequestObject) (loomapi.TraceExplorerRowLineageResponseObject, error) {
	if r == nil || r.explorer == nil {
		status, failure := authoringErrorForOpenAPI(ctx, "traceExplorerRowLineage", explorerUnavailable("rowLineage", "ROW_LINEAGE_UNAVAILABLE", "Explorer row lineage is not configured"))
		if status == http.StatusServiceUnavailable {
			return loomapi.TraceExplorerRowLineage503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
		}
		return nil, unexpectedResponseStatus("traceExplorerRowLineage", status)
	}
	value, err := r.explorer.traceExplorerRowLineageDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.TraceExplorerRowLineage200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "traceExplorerRowLineage", err)
	return loomapi.NewTraceExplorerRowLineageErrorResponse(status, openAPIErrorResponseBody(status, failure))
}

func (r *HTTPRoutes) CheckExplorerPopulationMapping(ctx context.Context, request loomapi.CheckExplorerPopulationMappingRequestObject) (loomapi.CheckExplorerPopulationMappingResponseObject, error) {
	project := string(request.Project)
	explorerID := string(request.ExplorerId)
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "checkExplorerPopulationMapping", explorerUnavailable("populationMapping", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.CheckExplorerPopulationMapping503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.populationMappingDirect(ctx, project, explorerID, request.Body)
	if err == nil {
		return loomapi.CheckExplorerPopulationMapping200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "checkExplorerPopulationMapping", err)
	return loomapi.NewCheckExplorerPopulationMappingErrorResponse(status, openAPIErrorResponseBody(status, failure))
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
	return loomapi.NewPublishExplorerErrorResponse(status, openAPIErrorResponseBody(status, failure))
}

func authoringUnauthorizedResponse(failure loomapi.ErrorResponse) loomapi.ServiceUnauthorizedJSONResponse {
	body := loomapi.ServiceErrorResponse{Error: loomapi.ServiceErrorBody{Code: failure.Error.Code, Message: failure.Error.Message, RequestId: failure.Error.RequestId}}
	if failure.Error.AdditionalProperties != nil {
		details := map[string]interface{}{}
		for key, value := range failure.Error.AdditionalProperties {
			details[key] = value
		}
		body.Error.Details = &details
	}
	return loomapi.ServiceUnauthorizedJSONResponse(body)
}

func (r *HTTPRoutes) ListInterpretationLibraries(ctx context.Context, request loomapi.ListInterpretationLibrariesRequestObject) (loomapi.ListInterpretationLibrariesResponseObject, error) {
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "listInterpretationLibraries", explorerUnavailable("interpretations", "INTERPRETATION_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.ListInterpretationLibraries503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.listInterpretationLibrariesDirect(ctx, string(request.Project))
	if err == nil {
		return loomapi.ListInterpretationLibraries200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "listInterpretationLibraries", err)
	return loomapi.NewListInterpretationLibrariesErrorResponse(status, openAPIErrorResponseBody(status, failure))
}

func (r *HTTPRoutes) CreateInterpretationRevision(ctx context.Context, request loomapi.CreateInterpretationRevisionRequestObject) (loomapi.CreateInterpretationRevisionResponseObject, error) {
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "createInterpretationRevision", explorerUnavailable("interpretations", "INTERPRETATION_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.CreateInterpretationRevision503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.createInterpretationRevisionDirect(ctx, string(request.Project), authResourcePathFromParam(request.Params.AuthResourcePath), request.Body)
	if err == nil {
		return loomapi.CreateInterpretationRevision201JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "createInterpretationRevision", err)
	return loomapi.NewCreateInterpretationRevisionErrorResponse(status, openAPIErrorResponseBody(status, failure))
}

func (r *HTTPRoutes) GetInterpretationRevision(ctx context.Context, request loomapi.GetInterpretationRevisionRequestObject) (loomapi.GetInterpretationRevisionResponseObject, error) {
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "getInterpretationRevision", explorerUnavailable("interpretations", "INTERPRETATION_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.GetInterpretationRevision503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.getInterpretationRevisionDirect(ctx, string(request.Project), request.RevisionId)
	if err == nil {
		return loomapi.GetInterpretationRevision200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "getInterpretationRevision", err)
	return loomapi.NewGetInterpretationRevisionErrorResponse(status, openAPIErrorResponseBody(status, failure))
}

func (r *HTTPRoutes) PreviewInterpretationCandidate(ctx context.Context, request loomapi.PreviewInterpretationCandidateRequestObject) (loomapi.PreviewInterpretationCandidateResponseObject, error) {
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "previewInterpretationCandidate", explorerUnavailable("interpretation-preview", "PREVIEW_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.PreviewInterpretationCandidate503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.previewInterpretationCandidateDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.PreviewInterpretationCandidate200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "previewInterpretationCandidate", err)
	return loomapi.NewPreviewInterpretationCandidateErrorResponse(status, openAPIErrorResponseBody(status, failure))
}

func (r *HTTPRoutes) ResolveConfiguredColumnContext(ctx context.Context, request loomapi.ResolveConfiguredColumnContextRequestObject) (loomapi.ResolveConfiguredColumnContextResponseObject, error) {
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "resolveConfiguredColumnContext", explorerUnavailable("interpretation-context", "INTERPRETATION_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.ResolveConfiguredColumnContext503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.resolveConfiguredColumnContextDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.ResolveConfiguredColumnContext200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "resolveConfiguredColumnContext", err)
	return loomapi.NewResolveConfiguredColumnContextErrorResponse(status, openAPIErrorResponseBody(status, failure))
}

func (r *HTTPRoutes) CreateInterpretationRevisionFromColumn(ctx context.Context, request loomapi.CreateInterpretationRevisionFromColumnRequestObject) (loomapi.CreateInterpretationRevisionFromColumnResponseObject, error) {
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "createInterpretationRevisionFromColumn", explorerUnavailable("interpretation-create", "INTERPRETATION_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.CreateInterpretationRevisionFromColumn503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.createInterpretationRevisionFromColumnDirect(ctx, string(request.Project), string(request.ExplorerId), authResourcePathFromParam(request.Params.AuthResourcePath), request.Body)
	if err == nil {
		return loomapi.CreateInterpretationRevisionFromColumn201JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "createInterpretationRevisionFromColumn", err)
	return loomapi.NewCreateInterpretationRevisionFromColumnErrorResponse(status, openAPIErrorResponseBody(status, failure))
}
