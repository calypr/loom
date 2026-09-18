package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func directAuthoringJSON[T any](value any) (T, error) {
	var result T
	raw, err := json.Marshal(value)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (h *explorerHTTPHandlers) authoringReadDirect(ctx context.Context, project string) error {
	if h == nil || h.authorizeRead == nil {
		return explorerUnavailable("authorization", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured")
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := h.authorizeRead(ctx, principal, project); err != nil {
		return &explorer.AuthoringError{Status: http.StatusForbidden, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "authorization", Code: "FORBIDDEN", Message: "forbidden"}, Cause: err}
	}
	return nil
}

func authResourcePathFromParam(value *loomapi.AuthResourcePath) string {
	if value == nil {
		return ""
	}
	return string(*value)
}

func (h *explorerHTTPHandlers) authoringWriteDirect(ctx context.Context, project, authResourcePath string) error {
	if h == nil || h.authorizer == nil {
		return explorerUnavailable("authorization", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured")
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := h.authorizer.AuthorizeWrite(ctx, principal, project, authResourcePath); err != nil {
		return &explorer.AuthoringError{Status: http.StatusForbidden, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "authorization", Code: "FORBIDDEN", Message: "forbidden"}, Cause: err}
	}
	return nil
}

func (h *explorerHTTPHandlers) getAuthoringCapabilityDirect(ctx context.Context, project string) (loomapi.AuthoringCapability, error) {
	var result loomapi.AuthoringCapability
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	return loomapi.AuthoringCapability{ApiVersion: loomapi.LoomCalyprOrgexplorerAuthoringv2, Kind: loomapi.ExplorerAuthoringCapabilities, Operations: []loomapi.AuthoringCapabilityOperations{loomapi.Builder, loomapi.Suggestions, loomapi.RowChange, loomapi.Preview, loomapi.InterpretationPreview, loomapi.Publish, loomapi.Commands, loomapi.Reconcile}, PreviewLimits: []int{10, 25, 50, 100}, Features: loomapi.AuthoringFeatures{EmissionFilters: true, EmissionCharts: true}}, nil
}

func (h *explorerHTTPHandlers) listInterpretationLibrariesDirect(ctx context.Context, project string) (loomapi.InterpretationLibraryListResponse, error) {
	var result loomapi.InterpretationLibraryListResponse
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	value, err := h.application.ListInterpretationLibraries(ctx, project)
	if err != nil {
		return result, err
	}
	result.Project = value.Project
	result.Libraries = make([]loomapi.InterpretationLibraryView, 0, len(value.Libraries))
	for _, view := range value.Libraries {
		library, convertErr := directAuthoringJSON[loomapi.InterpretationLibrary](view.Library)
		if convertErr != nil {
			return result, convertErr
		}
		wire := loomapi.InterpretationLibraryView{Library: library}
		if view.Head != nil {
			head, convertErr := directAuthoringJSON[loomapi.InterpretationRevision](*view.Head)
			if convertErr != nil {
				return result, convertErr
			}
			wire.Head = &head
		}
		result.Libraries = append(result.Libraries, wire)
	}
	return result, nil
}

func (h *explorerHTTPHandlers) getInterpretationRevisionDirect(ctx context.Context, project, revisionID string) (loomapi.InterpretationRevision, error) {
	var result loomapi.InterpretationRevision
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	value, err := h.application.GetInterpretationRevision(ctx, lifecycle.GetInterpretationRevisionRequest{Project: project, RevisionID: revisionID})
	if err != nil {
		return result, err
	}
	return directAuthoringJSON[loomapi.InterpretationRevision](value)
}

func (h *explorerHTTPHandlers) createInterpretationRevisionDirect(ctx context.Context, project, authResourcePath string, body *loomapi.CreateInterpretationRevisionJSONRequestBody) (loomapi.InterpretationRevision, error) {
	var result loomapi.InterpretationRevision
	if err := h.authoringWriteDirect(ctx, project, authResourcePath); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("interpretations", errors.New("request body is required"))
	}
	applicability, err := directAuthoringJSON[explorer.InterpretationApplicability](body.Applicability)
	if err != nil {
		return result, malformedRouteError("interpretations", err)
	}
	rules, err := directAuthoringJSON[[]explorer.InterpretationRule](body.Rules)
	if err != nil {
		return result, malformedRouteError("interpretations", err)
	}
	parentRevisionID := ""
	if body.ParentRevisionId != nil {
		parentRevisionID = *body.ParentRevisionId
	}
	value, err := h.application.CreateInterpretationRevision(ctx, lifecycle.CreateInterpretationRevisionRequest{
		Project: project, LibraryID: body.LibraryId, ParentRevisionID: parentRevisionID,
		Applicability: applicability, Rules: rules, Explanation: body.Explanation,
		Author: subjectFromContext(ctx),
	})
	if err != nil {
		return result, err
	}
	return directAuthoringJSON[loomapi.InterpretationRevision](value)
}

func (h *explorerHTTPHandlers) previewInterpretationCandidateDirect(ctx context.Context, project, explorerID string, body *loomapi.PreviewInterpretationCandidateJSONRequestBody) (loomapi.InterpretationPreviewResponse, error) {
	var result loomapi.InterpretationPreviewResponse
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("interpretation-preview", errors.New("request body is required"))
	}
	limit := dataframeexecution.DefaultPreviewLimit
	if body.Limit != nil {
		limit = *body.Limit
	}
	previewCtx, cancel := context.WithTimeout(ctx, explorerPreviewTimeout)
	defer cancel()
	value, err := h.application.PreviewInterpretationCandidate(previewCtx, lifecycle.PreviewInterpretationCandidateRequest{
		Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
		ExpectedDraftVersion: body.ExpectedDraftVersion, ExpectedDraftDigest: body.ExpectedDraftDigest,
		OutputID: body.OutputId, Column: body.Column, RevisionID: body.RevisionId, Limit: limit,
	})
	if err != nil {
		var lifecycleErr *lifecycle.Error
		if errors.As(err, &lifecycleErr) {
			return result, err
		}
		return result, previewRouteError(err)
	}
	result, err = directAuthoringJSON[loomapi.InterpretationPreviewResponse](value)
	if err != nil {
		return result, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	if len(encoded) > maxExplorerPreviewResponseBytes {
		return result, previewRouteError(&previewResponseTooLargeError{Limit: maxExplorerPreviewResponseBytes})
	}
	return result, nil
}

func (h *explorerHTTPHandlers) searchAuthoringSuggestionsDirect(ctx context.Context, project, explorerID string, body *loomapi.SearchExplorerCandidatesJSONRequestBody) (loomapi.CandidateSearchResponse, error) {
	var result loomapi.CandidateSearchResponse
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("suggestions", errors.New("request body is required"))
	}
	query := ""
	if body.Query != nil {
		query = *body.Query
	}
	value, err := h.application.Suggestions(ctx, lifecycle.SuggestionsRequest{Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken, NodeID: body.NodeId, Query: query})
	if err != nil {
		return result, err
	}
	candidates := make([]loomapi.CatalogCandidate, 0, len(value.Candidates))
	for _, candidate := range value.Candidates {
		wire, convertErr := directAuthoringJSON[loomapi.CatalogCandidate](candidate)
		if convertErr != nil {
			return result, convertErr
		}
		candidates = append(candidates, wire)
	}
	return loomapi.CandidateSearchResponse{ApiVersion: loomapi.LoomCalyprOrgexplorerAuthoringv2, Kind: loomapi.ExplorerBuilderCandidateSuggestions, SnapshotToken: value.SnapshotToken, NodeId: value.NodeID, Candidates: candidates, Diagnostics: []loomapi.Diagnostic{}}, nil
}

func (h *explorerHTTPHandlers) getAuthoringBuilderDirect(ctx context.Context, project, explorerID string) (loomapi.BuilderState, error) {
	var result loomapi.BuilderState
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	value, err := h.application.Builder(ctx, lifecycle.BuilderRequest{Project: project, ExplorerID: explorerID})
	if err != nil {
		return result, err
	}
	return directAuthoringJSON[loomapi.BuilderState](value)
}

func (h *explorerHTTPHandlers) applyAuthoringCommandsDirect(ctx context.Context, project, explorerID, authResourcePath string, body *loomapi.ApplyExplorerBuilderCommandsJSONRequestBody) (loomapi.ApplyCommandsResponse, error) {
	var result loomapi.ApplyCommandsResponse
	if err := h.authoringWriteDirect(ctx, project, authResourcePath); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("commands", errors.New("request body is required"))
	}
	request, err := directAuthoringJSON[authoringv2.ApplyCommandsRequest](*body)
	if err != nil {
		return result, malformedRouteError("commands", err)
	}
	value, err := h.application.ApplyCommands(ctx, project, explorerID, request, subjectFromContext(ctx))
	if err != nil {
		return result, err
	}
	return directAuthoringJSON[loomapi.ApplyCommandsResponse](value)
}

func (h *explorerHTTPHandlers) assessAuthoringRowChangeDirect(ctx context.Context, project, explorerID string, body *loomapi.AssessExplorerRowChangeJSONRequestBody) (loomapi.RowChangeAssessmentResponse, error) {
	var result loomapi.RowChangeAssessmentResponse
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("row-change", errors.New("request body is required"))
	}
	rootOccurrenceID := ""
	if body.RootOccurrenceId != nil {
		rootOccurrenceID = *body.RootOccurrenceId
	}
	routeRebase := []authoringv2.RouteRebaseChoice{}
	if body.RouteRebase != nil {
		converted, err := directAuthoringJSON[[]authoringv2.RouteRebaseChoice](*body.RouteRebase)
		if err != nil {
			return result, malformedRouteError("row-change", err)
		}
		routeRebase = converted
	}
	value, err := h.application.AssessRowChange(ctx, lifecycle.AssessRowChangeRequest{
		Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
		DraftVersion: body.DraftVersion, DraftDigest: body.DraftDigest, OutputID: body.OutputId,
		RootNodeID: body.RootNodeId, RootOccurrenceID: rootOccurrenceID, RouteRebase: routeRebase,
	})
	if err != nil {
		return result, err
	}
	return directAuthoringJSON[loomapi.RowChangeAssessmentResponse](struct {
		authoringv2.RowChangeAssessment
		SnapshotToken string           `json:"snapshotToken"`
		DraftVersion  int64            `json:"draftVersion"`
		DraftDigest   string           `json:"draftDigest"`
		Diagnostics   []map[string]any `json:"diagnostics"`
	}{
		RowChangeAssessment: value.Assessment,
		SnapshotToken:       value.SnapshotToken,
		DraftVersion:        value.DraftVersion,
		DraftDigest:         value.DraftDigest,
		Diagnostics:         []map[string]any{},
	})
}

func (h *explorerHTTPHandlers) reconcileAuthoringDirect(ctx context.Context, project, explorerID, authResourcePath string, body *loomapi.ReconcileExplorerBuilderJSONRequestBody) (loomapi.CompileResponse, error) {
	var result loomapi.CompileResponse
	if err := h.authoringWriteDirect(ctx, project, authResourcePath); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("reconcile", errors.New("request body is required"))
	}
	receipt, err := h.application.Reconcile(ctx, lifecycle.ReconcileRequest{Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken, DraftVersion: body.DraftVersion, DraftDigest: body.DraftDigest})
	if err != nil {
		return result, err
	}
	return v2ReceiptResponse(receipt, authoringv2.Workspace{}), nil
}

func (h *explorerHTTPHandlers) previewAuthoringDirect(ctx context.Context, project, explorerID string, body *loomapi.PreviewExplorerJSONRequestBody) (loomapi.PreviewResponse, error) {
	var result loomapi.PreviewResponse
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("preview", errors.New("receiptId and outputId are required"))
	}
	limit := dataframeexecution.DefaultPreviewLimit
	if body.Limit != nil {
		limit = *body.Limit
		if limit == 0 {
			limit = -1
		}
	}
	var finish func() ([]byte, error)
	_, err := h.application.Preview(ctx, lifecycle.PreviewRequest{Project: project, ExplorerID: explorerID, ReceiptID: body.ReceiptId, OutputID: body.OutputId, Limit: limit, SinkFactory: func(receipt *explorer.CompilationReceipt, columns []explorer.EmittedColumn) (func(map[string]any) error, error) {
		encoder, encoderErr := newPreviewResponseEncoder(receipt, body.OutputId, columns, maxExplorerPreviewResponseBytes)
		if encoderErr != nil {
			return nil, encoderErr
		}
		finish = encoder.Finish
		return encoder.Visit, nil
	}})
	if err != nil {
		return result, previewRouteError(err)
	}
	if finish == nil {
		return result, previewRouteError(errors.New("native preview response sink was not configured"))
	}
	encoded, err := finish()
	if err != nil {
		return result, previewRouteError(err)
	}
	if err := json.Unmarshal(encoded, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (h *explorerHTTPHandlers) traceExplorerCellDirect(ctx context.Context, project, explorerID string, body *loomapi.TraceExplorerCellJSONRequestBody) (loomapi.CellTraceResponse, error) {
	var result loomapi.CellTraceResponse
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("trace", errors.New("receiptId, outputId, rowId, and column are required"))
	}
	request := lifecycle.CellTraceRequest{
		Project: project, ExplorerID: explorerID,
		ReceiptID: body.ReceiptId, OutputID: body.OutputId,
		RowID: body.RowId, Column: body.Column,
	}
	if body.Offset != nil {
		request.Offset = *body.Offset
	}
	if body.Limit != nil {
		request.Limit = *body.Limit
	}
	value, err := h.application.CellTrace(ctx, request)
	if err != nil {
		return result, err
	}
	result.Binding = loomapi.CellTraceBinding{
		ReceiptId: value.Binding.ReceiptID, OutputId: value.Binding.OutputID,
		Project: value.Binding.Project, ExplorerId: value.Binding.ExplorerID,
		Generation: value.Binding.Generation, ScopeDigest: value.Binding.ScopeDigest,
	}
	result.Feature = loomapi.CellTraceFeature{
		OutputId: value.Feature.OutputID, Column: value.Feature.Column,
		AuthoredColumn: value.Feature.AuthoredColumn, OccurrenceId: value.Feature.OccurrenceID,
		Label: value.Feature.Label, LogicalType: value.Feature.LogicalType,
		ProjectionMode: value.Feature.ProjectionMode, Lossless: value.Feature.Lossless,
		LossReasons: append([]string{}, value.Feature.LossReasons...),
	}
	if value.Feature.SourceResourceType != "" {
		result.Feature.SourceResourceType = &value.Feature.SourceResourceType
	}
	if value.Feature.SourcePath != "" {
		result.Feature.SourcePath = &value.Feature.SourcePath
	}
	trace := loomapi.CellTraceTrace{
		RowId: value.Trace.RowID, Column: value.Trace.Column, Value: value.Trace.Value,
		Status: loomapi.CellTraceTraceStatus(value.Trace.Status), HasMore: value.Trace.HasMore,
		NextOffset: value.Trace.NextOffset, Complete: value.Trace.Complete,
	}
	if value.Trace.OmissionCode != "" {
		trace.OmissionCode = &value.Trace.OmissionCode
	}
	trace.Contributions = make([]loomapi.CellTraceContribution, 0, len(value.Trace.Contributions))
	for _, contribution := range value.Trace.Contributions {
		resourceType, resourceID := contribution.ResourceType, contribution.ResourceID
		wire := loomapi.CellTraceContribution{Value: contribution.Value}
		if resourceType != "" {
			wire.ResourceType = &resourceType
		}
		if resourceID != "" {
			wire.ResourceId = &resourceID
		}
		trace.Contributions = append(trace.Contributions, wire)
	}
	result.Trace = trace
	return result, nil
}

func (h *explorerHTTPHandlers) populationMappingDirect(ctx context.Context, project, explorerID string, body *loomapi.CheckExplorerPopulationMappingJSONRequestBody) (loomapi.PopulationMappingResponse, error) {
	var result loomapi.PopulationMappingResponse
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("populationMapping", errors.New("receiptId and outputId are required"))
	}
	request := lifecycle.PopulationMappingRequest{Project: project, ExplorerID: explorerID, ReceiptID: body.ReceiptId, OutputID: body.OutputId}
	if body.Cursor != nil {
		request.Cursor = *body.Cursor
	}
	if body.Limit != nil {
		request.Limit = *body.Limit
	}
	value, err := h.application.PopulationMapping(ctx, request)
	if err != nil {
		return result, err
	}
	result.Binding = loomapi.PopulationMappingBinding{
		ReceiptId: value.Report.Binding.ReceiptID, OutputId: value.Report.Binding.OutputID,
		Project: value.Report.Binding.Project, ExplorerId: value.Report.Binding.ExplorerID,
		Generation: value.Report.Binding.Generation, ScopeDigest: value.Report.Binding.ScopeDigest,
		SelectionRevisionId: value.Report.Binding.SelectionRevisionID, MembershipDigest: value.Report.Binding.MembershipDigest,
		ResourceType: value.Report.Binding.ResourceType,
	}
	result.Status = loomapi.PopulationMappingResponseStatus(value.Report.Status)
	result.Unmapped = make([]loomapi.SelectionResourceRef, 0, len(value.Report.Unmapped))
	for _, ref := range value.Report.Unmapped {
		result.Unmapped = append(result.Unmapped, loomapi.SelectionResourceRef{Project: ref.Project, Generation: ref.Generation, ResourceType: ref.ResourceType, Id: ref.ID})
	}
	if value.Report.NextCursor != "" {
		result.NextCursor = &value.Report.NextCursor
	}
	if value.Report.Counts != nil {
		result.Counts = &loomapi.PopulationMappingCounts{Selected: value.Report.Counts.Selected, Mapped: value.Report.Counts.Mapped, Unmapped: value.Report.Counts.Unmapped, EmittedRows: value.Report.Counts.EmittedRows}
	}
	result.Diagnostics = make([]loomapi.Diagnostic, 0, len(value.Report.Diagnostics))
	for _, diagnostic := range value.Report.Diagnostics {
		result.Diagnostics = append(result.Diagnostics, loomapi.Diagnostic{Severity: "INFO", Stage: "populationMapping", Code: diagnostic.Code, Message: diagnostic.Message})
	}
	return result, nil
}

func (h *explorerHTTPHandlers) publishAuthoringDirect(ctx context.Context, project, explorerID, authResourcePath string, body *loomapi.PublishExplorerJSONRequestBody) (loomapi.PublishResponse, error) {
	var result loomapi.PublishResponse
	if err := h.authoringWriteDirect(ctx, project, authResourcePath); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("publish", errors.New("receiptId is required"))
	}
	value, err := h.application.Publish(ctx, lifecycle.PublishRequest{Project: project, ExplorerID: explorerID, ReceiptID: body.ReceiptId, Actor: subjectFromContext(ctx)})
	if err != nil {
		return result, err
	}
	outputs := make([]loomapi.PublicationOutput, 0, len(value.Revision.Materializations))
	for _, materialization := range value.Revision.Materializations {
		outputs = append(outputs, loomapi.PublicationOutput{OutputId: firstNonEmpty(materialization.OutputID, materialization.Output), State: "READY", MaterializationId: materialization.MaterializationID})
	}
	return loomapi.PublishResponse{ApiVersion: loomapi.LoomCalyprOrgexplorerAuthoringv2, Kind: loomapi.ExplorerBuilderPublication, ReceiptId: value.Receipt.ID, RevisionId: value.Revision.ID, State: string(value.Revision.Status), Outputs: outputs, Diagnostics: []loomapi.Diagnostic{}}, nil
}

func authoringErrorForOpenAPI(ctx context.Context, operation string, err error) (int, loomapi.ErrorResponse) {
	requestID := requestIDFromContext(ctx)
	status := http.StatusInternalServerError
	code, message, stage, path := "INTERNAL_ERROR", "internal server error", "internal", ""
	var details map[string]any
	var cause error = err
	var lifecycleErr *lifecycle.Error
	var authoringErr *explorer.AuthoringError
	if errors.As(err, &lifecycleErr) {
		status = lifecycleErrorStatus(lifecycleErr.Class)
		code, message, stage, path, details, cause = lifecycleErr.Code, lifecycleErr.Message, lifecycleErr.Stage, lifecycleErr.Path, lifecycleErr.Details, lifecycleErr.Cause
	} else if errors.As(err, &authoringErr) {
		status = authoringErr.Status
		code, message, stage, path, details = authoringErr.Diagnostic.Code, authoringErr.Diagnostic.Message, authoringErr.Diagnostic.Stage, authoringErr.Diagnostic.JSONPath, authoringErr.Diagnostic.Details
		if authoringErr.Cause != nil {
			cause = authoringErr.Cause
		}
	} else if errors.Is(err, explorer.ErrNotFound) {
		status, code, message = http.StatusNotFound, "NOT_FOUND", "Explorer resource not found"
	}
	if stage == "" {
		stage = operation
	}
	if code == "" {
		code = "INTERNAL_ERROR"
	}
	if message == "" {
		message = "internal server error"
	}
	if cause != nil && status >= 500 {
		slog.Error("Explorer authoring request failed", "request_id", requestID, "operation", operation, "status", status, "stage", stage, "code", code, "json_path", path, "cause", cause, "cause_type", fmt.Sprintf("%T", cause))
	}
	diagnostic := loomapi.Diagnostic{Severity: "error", Stage: stage, Code: code, Message: message}
	if path != "" {
		diagnostic.JsonPath = &path
	}
	if requestID != "" {
		diagnostic.RequestId = &requestID
	}
	if details != nil {
		converted := map[string]interface{}(details)
		diagnostic.Details = &converted
	}
	diagnostics := []loomapi.Diagnostic{diagnostic}
	body := loomapi.ErrorBody{Code: code, Message: message, Diagnostic: &diagnostic}
	if requestID != "" {
		body.RequestId = &requestID
	}
	if details != nil {
		body.AdditionalProperties = map[string]interface{}{"details": details}
	}
	return status, loomapi.ErrorResponse{Error: body, Diagnostics: &diagnostics}
}

func lifecycleErrorStatus(class lifecycle.ErrorClass) int {
	switch class {
	case lifecycle.ClassMalformed:
		return http.StatusBadRequest
	case lifecycle.ClassForbidden:
		return http.StatusForbidden
	case lifecycle.ClassNotFound:
		return http.StatusNotFound
	case lifecycle.ClassConflict:
		return http.StatusConflict
	case lifecycle.ClassUnprocessable:
		return http.StatusUnprocessableEntity
	case lifecycle.ClassUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
