package server

import (
	"context"

	loomapi "github.com/calypr/loom/api/loomapi"
)

func (r *HTTPRoutes) ListExplorers(ctx context.Context, request loomapi.ListExplorersRequestObject) (loomapi.ListExplorersResponseObject, error) {
	value, err := r.explorer.listExplorers(ctx, string(request.Project))
	if err == nil {
		return loomapi.ListExplorers200JSONResponse(value), nil
	}
	status, failure := explorerErrorResponse(err)
	return loomapi.NewListExplorersErrorResponse(status, failure)
}

func (r *HTTPRoutes) CreateExplorer(ctx context.Context, request loomapi.CreateExplorerRequestObject) (loomapi.CreateExplorerResponseObject, error) {
	value, err := r.explorer.createExplorer(ctx, string(request.Project), authResourcePathFromParam(request.Params.AuthResourcePath), request.Body)
	if err == nil {
		return loomapi.CreateExplorer201JSONResponse(value), nil
	}
	status, failure := explorerErrorResponse(err)
	return loomapi.NewCreateExplorerErrorResponse(status, failure)
}

func (r *HTTPRoutes) GetExplorer(ctx context.Context, request loomapi.GetExplorerRequestObject) (loomapi.GetExplorerResponseObject, error) {
	value, err := r.explorer.getExplorer(ctx, string(request.Project), string(request.ExplorerId))
	if err == nil {
		return loomapi.GetExplorer200JSONResponse(value), nil
	}
	status, failure := explorerErrorResponse(err)
	return loomapi.NewGetExplorerErrorResponse(status, failure)
}

func (r *HTTPRoutes) PublishRepositoryExplorerConfig(ctx context.Context, request loomapi.PublishRepositoryExplorerConfigRequestObject) (loomapi.PublishRepositoryExplorerConfigResponseObject, error) {
	value, err := r.explorer.publishRepositoryExplorerConfig(ctx, request)
	if err == nil {
		return loomapi.PublishRepositoryExplorerConfig200JSONResponse(value), nil
	}
	status, failure := explorerErrorResponse(err)
	return loomapi.NewPublishRepositoryExplorerConfigErrorResponse(status, failure)
}

func (r *HTTPRoutes) GetExplorerAuthoringCapability(ctx context.Context, request loomapi.GetExplorerAuthoringCapabilityRequestObject) (loomapi.GetExplorerAuthoringCapabilityResponseObject, error) {
	value, err := r.explorer.getAuthoringCapabilityDirect(ctx, string(request.Project))
	if err == nil {
		return loomapi.GetExplorerAuthoringCapability200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "getExplorerAuthoringCapability", err)
	return loomapi.NewGetExplorerAuthoringCapabilityErrorResponse(status, failure)
}

func (r *HTTPRoutes) SearchExplorerCandidates(ctx context.Context, request loomapi.SearchExplorerCandidatesRequestObject) (loomapi.SearchExplorerCandidatesResponseObject, error) {
	value, err := r.explorer.searchAuthoringSuggestionsDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.SearchExplorerCandidates200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "searchExplorerCandidates", err)
	return loomapi.NewSearchExplorerCandidatesErrorResponse(status, failure)
}

func (r *HTTPRoutes) SearchExplorerSchemaFields(ctx context.Context, request loomapi.SearchExplorerSchemaFieldsRequestObject) (loomapi.SearchExplorerSchemaFieldsResponseObject, error) {
	value, err := r.explorer.searchAuthoringSchemaFieldsDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.SearchExplorerSchemaFields200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "searchExplorerSchemaFields", err)
	return loomapi.NewSearchExplorerSchemaFieldsErrorResponse(status, failure)
}

func (r *HTTPRoutes) GetExplorerBuilder(ctx context.Context, request loomapi.GetExplorerBuilderRequestObject) (loomapi.GetExplorerBuilderResponseObject, error) {
	value, err := r.explorer.getAuthoringBuilderDirect(ctx, string(request.Project), string(request.ExplorerId))
	if err == nil {
		return loomapi.GetExplorerBuilder200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "getExplorerBuilder", err)
	return loomapi.NewGetExplorerBuilderErrorResponse(status, failure)
}

func (r *HTTPRoutes) ApplyExplorerBuilderCommands(ctx context.Context, request loomapi.ApplyExplorerBuilderCommandsRequestObject) (loomapi.ApplyExplorerBuilderCommandsResponseObject, error) {
	value, err := r.explorer.applyAuthoringCommandsDirect(ctx, string(request.Project), string(request.ExplorerId), authResourcePathFromParam(request.Params.AuthResourcePath), request.Body)
	if err == nil {
		return loomapi.ApplyExplorerBuilderCommands200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "applyExplorerBuilderCommands", err)
	return loomapi.NewApplyExplorerBuilderCommandsErrorResponse(status, failure)
}

func (r *HTTPRoutes) AssessExplorerRowChange(ctx context.Context, request loomapi.AssessExplorerRowChangeRequestObject) (loomapi.AssessExplorerRowChangeResponseObject, error) {
	value, err := r.explorer.assessAuthoringRowChangeDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.AssessExplorerRowChange200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "assessExplorerRowChange", err)
	return loomapi.NewAssessExplorerRowChangeErrorResponse(status, failure)
}

func (r *HTTPRoutes) ReconcileExplorerBuilder(ctx context.Context, request loomapi.ReconcileExplorerBuilderRequestObject) (loomapi.ReconcileExplorerBuilderResponseObject, error) {
	project := string(request.Project)
	explorerID := string(request.ExplorerId)
	value, err := r.explorer.reconcileAuthoringDirect(ctx, project, explorerID, authResourcePathFromParam(request.Params.AuthResourcePath), request.Body)
	if err == nil {
		return loomapi.ReconcileExplorerBuilder200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "reconcileExplorerBuilder", err)
	return loomapi.NewReconcileExplorerBuilderErrorResponse(status, failure)
}

func (r *HTTPRoutes) PreviewExplorer(ctx context.Context, request loomapi.PreviewExplorerRequestObject) (loomapi.PreviewExplorerResponseObject, error) {
	project := string(request.Project)
	explorerID := string(request.ExplorerId)
	value, err := r.explorer.previewAuthoringDirect(ctx, project, explorerID, request.Body)
	if err == nil {
		return loomapi.PreviewExplorer200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "previewExplorer", err)
	return loomapi.NewPreviewExplorerErrorResponse(status, failure)
}

func (r *HTTPRoutes) TraceExplorerCell(ctx context.Context, request loomapi.TraceExplorerCellRequestObject) (loomapi.TraceExplorerCellResponseObject, error) {
	value, err := r.explorer.traceExplorerCellDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.TraceExplorerCell200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "traceExplorerCell", err)
	return loomapi.NewTraceExplorerCellErrorResponse(status, failure)
}

func (r *HTTPRoutes) TraceExplorerRowLineage(ctx context.Context, request loomapi.TraceExplorerRowLineageRequestObject) (loomapi.TraceExplorerRowLineageResponseObject, error) {
	value, err := r.explorer.traceExplorerRowLineageDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.TraceExplorerRowLineage200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "traceExplorerRowLineage", err)
	return loomapi.NewTraceExplorerRowLineageErrorResponse(status, failure)
}

func (r *HTTPRoutes) CheckExplorerPopulationMapping(ctx context.Context, request loomapi.CheckExplorerPopulationMappingRequestObject) (loomapi.CheckExplorerPopulationMappingResponseObject, error) {
	project := string(request.Project)
	explorerID := string(request.ExplorerId)
	value, err := r.explorer.populationMappingDirect(ctx, project, explorerID, request.Body)
	if err == nil {
		return loomapi.CheckExplorerPopulationMapping200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "checkExplorerPopulationMapping", err)
	return loomapi.NewCheckExplorerPopulationMappingErrorResponse(status, failure)
}

func (r *HTTPRoutes) PublishExplorer(ctx context.Context, request loomapi.PublishExplorerRequestObject) (loomapi.PublishExplorerResponseObject, error) {
	project := string(request.Project)
	explorerID := string(request.ExplorerId)
	value, err := r.explorer.publishAuthoringDirect(ctx, project, explorerID, authResourcePathFromParam(request.Params.AuthResourcePath), request.Body)
	if err == nil {
		return loomapi.PublishExplorer200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "publishExplorer", err)
	return loomapi.NewPublishExplorerErrorResponse(status, failure)
}

func (r *HTTPRoutes) ListInterpretationLibraries(ctx context.Context, request loomapi.ListInterpretationLibrariesRequestObject) (loomapi.ListInterpretationLibrariesResponseObject, error) {
	value, err := r.explorer.listInterpretationLibrariesDirect(ctx, string(request.Project))
	if err == nil {
		return loomapi.ListInterpretationLibraries200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "listInterpretationLibraries", err)
	return loomapi.NewListInterpretationLibrariesErrorResponse(status, failure)
}

func (r *HTTPRoutes) CreateInterpretationRevision(ctx context.Context, request loomapi.CreateInterpretationRevisionRequestObject) (loomapi.CreateInterpretationRevisionResponseObject, error) {
	value, err := r.explorer.createInterpretationRevisionDirect(ctx, string(request.Project), authResourcePathFromParam(request.Params.AuthResourcePath), request.Body)
	if err == nil {
		return loomapi.CreateInterpretationRevision201JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "createInterpretationRevision", err)
	return loomapi.NewCreateInterpretationRevisionErrorResponse(status, failure)
}

func (r *HTTPRoutes) GetInterpretationRevision(ctx context.Context, request loomapi.GetInterpretationRevisionRequestObject) (loomapi.GetInterpretationRevisionResponseObject, error) {
	value, err := r.explorer.getInterpretationRevisionDirect(ctx, string(request.Project), request.RevisionId)
	if err == nil {
		return loomapi.GetInterpretationRevision200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "getInterpretationRevision", err)
	return loomapi.NewGetInterpretationRevisionErrorResponse(status, failure)
}

func (r *HTTPRoutes) PreviewInterpretationCandidate(ctx context.Context, request loomapi.PreviewInterpretationCandidateRequestObject) (loomapi.PreviewInterpretationCandidateResponseObject, error) {
	value, err := r.explorer.previewInterpretationCandidateDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.PreviewInterpretationCandidate200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "previewInterpretationCandidate", err)
	return loomapi.NewPreviewInterpretationCandidateErrorResponse(status, failure)
}

func (r *HTTPRoutes) ResolveConfiguredColumnContext(ctx context.Context, request loomapi.ResolveConfiguredColumnContextRequestObject) (loomapi.ResolveConfiguredColumnContextResponseObject, error) {
	value, err := r.explorer.resolveConfiguredColumnContextDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.ResolveConfiguredColumnContext200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "resolveConfiguredColumnContext", err)
	return loomapi.NewResolveConfiguredColumnContextErrorResponse(status, failure)
}

func (r *HTTPRoutes) CreateInterpretationRevisionFromColumn(ctx context.Context, request loomapi.CreateInterpretationRevisionFromColumnRequestObject) (loomapi.CreateInterpretationRevisionFromColumnResponseObject, error) {
	value, err := r.explorer.createInterpretationRevisionFromColumnDirect(ctx, string(request.Project), string(request.ExplorerId), authResourcePathFromParam(request.Params.AuthResourcePath), request.Body)
	if err == nil {
		return loomapi.CreateInterpretationRevisionFromColumn201JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "createInterpretationRevisionFromColumn", err)
	return loomapi.NewCreateInterpretationRevisionFromColumnErrorResponse(status, failure)
}
