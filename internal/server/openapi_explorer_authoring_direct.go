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

func (h *explorerHTTPHandlers) authoringWriteDirect(ctx context.Context, project string) error {
	if h == nil || h.authorizer == nil {
		return explorerUnavailable("authorization", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured")
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := h.authorizer.AuthorizeWrite(ctx, principal, project, ""); err != nil {
		return &explorer.AuthoringError{Status: http.StatusForbidden, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "authorization", Code: "FORBIDDEN", Message: "forbidden"}, Cause: err}
	}
	return nil
}

func (h *explorerHTTPHandlers) getAuthoringCapabilityDirect(ctx context.Context, project string) (loomapi.AuthoringCapability, error) {
	var result loomapi.AuthoringCapability
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	return loomapi.AuthoringCapability{ApiVersion: loomapi.LoomCalyprOrgexplorerAuthoringv2, Kind: loomapi.ExplorerAuthoringCapabilities, Operations: []loomapi.AuthoringCapabilityOperations{loomapi.Builder, loomapi.Suggestions, loomapi.Preview, loomapi.Publish, loomapi.Commands, loomapi.Reconcile}, PreviewLimits: []int{10, 25, 50, 100}, Features: loomapi.AuthoringFeatures{EmissionFilters: true, EmissionCharts: true}}, nil
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

func (h *explorerHTTPHandlers) applyAuthoringCommandsDirect(ctx context.Context, project, explorerID string, body *loomapi.ApplyExplorerBuilderCommandsJSONRequestBody) (loomapi.ApplyCommandsResponse, error) {
	var result loomapi.ApplyCommandsResponse
	if err := h.authoringWriteDirect(ctx, project); err != nil {
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

func (h *explorerHTTPHandlers) compileAuthoringDirect(ctx context.Context, project, explorerID string, body *loomapi.CompileExplorerBuilderJSONRequestBody) (loomapi.CompileResponse, error) {
	var result loomapi.CompileResponse
	if err := h.authoringWriteDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("intent", errors.New("request body is required"))
	}
	receipt, err := h.application.Compile(ctx, lifecycle.CompileRequest{Project: project, ExplorerID: explorerID, Workspace: body.Workspace, SnapshotToken: body.SnapshotToken, RequestID: requestIDFromContext(ctx)})
	if err != nil {
		return result, err
	}
	return v2ReceiptResponse(receipt, body.Workspace), nil
}

func (h *explorerHTTPHandlers) reconcileAuthoringDirect(ctx context.Context, project, explorerID string, body *loomapi.ReconcileExplorerBuilderJSONRequestBody) (loomapi.CompileResponse, error) {
	var result loomapi.CompileResponse
	if err := h.authoringWriteDirect(ctx, project); err != nil {
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
		return result, err
	}
	if finish == nil {
		return result, errors.New("native preview response sink was not configured")
	}
	encoded, err := finish()
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(encoded, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (h *explorerHTTPHandlers) publishAuthoringDirect(ctx context.Context, project, explorerID string, body *loomapi.PublishExplorerJSONRequestBody) (loomapi.PublishResponse, error) {
	var result loomapi.PublishResponse
	if err := h.authoringWriteDirect(ctx, project); err != nil {
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
