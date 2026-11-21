package explorerapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	loomapi "github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/api/authpolicy"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	executionarango "github.com/calypr/loom/internal/dataframe/execution/arango"
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

func (h *Handlers) authorizeAuthoringRead(ctx context.Context, project string) error {
	if h == nil || h.authorizeRead == nil {
		return explorer.ExplorerUnavailable("authorization", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured")
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := h.authorizeRead(ctx, principal, project); err != nil {
		if authorizerResponseStatus(err) == http.StatusServiceUnavailable {
			return &explorer.AuthoringError{Status: http.StatusServiceUnavailable, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "authorization", Code: authoringBackendUnavailableCode, Message: authoringBackendUnavailableMessage}, Cause: err}
		}
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

const (
	authoringBackendUnavailableCode    = "BACKEND_UNAVAILABLE"
	authoringBackendUnavailableMessage = "authorization backend unavailable"
)

func authorizerResponseStatus(err error) int {
	classified := authpolicy.Classify(err, authpolicy.OperationExplorerAuthorization)
	userErr, ok := dataframeerrors.AsUserError(classified)
	if ok && userErr.Code() == string(dataframeerrors.CodeBackendUnavailable) && userErr.Retryable() {
		return http.StatusServiceUnavailable
	}
	return http.StatusForbidden
}

func (h *Handlers) authorizeAuthoringWrite(ctx context.Context, project, authResourcePath string) error {
	if h == nil || h.authorizer == nil {
		return explorer.ExplorerUnavailable("authorization", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured")
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := h.authorizer.AuthorizeWrite(ctx, principal, project, authResourcePath); err != nil {
		if authorizerResponseStatus(err) == http.StatusServiceUnavailable {
			return &explorer.AuthoringError{Status: http.StatusServiceUnavailable, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "authorization", Code: authoringBackendUnavailableCode, Message: authoringBackendUnavailableMessage}, Cause: err}
		}
		return &explorer.AuthoringError{Status: http.StatusForbidden, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "authorization", Code: "FORBIDDEN", Message: "forbidden"}, Cause: err}
	}
	return nil
}

func configuredColumnContextResponse(value lifecycle.ConfiguredColumnContextResult) (loomapi.ConfiguredColumnContextResponse, error) {
	result := loomapi.ConfiguredColumnContextResponse{
		SnapshotToken: value.SnapshotToken, DraftVersion: value.DraftVersion, DraftDigest: value.DraftDigest,
		Libraries:       make([]loomapi.InterpretationLibrarySummary, 0, len(value.Libraries)),
		PinnedRevisions: make([]loomapi.InterpretationRevisionSummary, 0, len(value.PinnedRevisions)),
		Columns:         make([]loomapi.ConfiguredColumnContext, 0, len(value.Columns)),
	}
	for _, library := range value.Libraries {
		wire := loomapi.InterpretationLibrarySummary{Id: library.ID, UpdatedAt: library.UpdatedAt}
		if library.HeadRevisionID != "" {
			headID := library.HeadRevisionID
			wire.HeadRevisionId = &headID
		}
		if library.HeadDigest != "" {
			headDigest := library.HeadDigest
			wire.HeadDigest = &headDigest
		}
		if library.Head != nil {
			head := interpretationRevisionSummaryResponse(*library.Head)
			wire.Head = &head
		}
		result.Libraries = append(result.Libraries, wire)
	}
	for _, revision := range value.PinnedRevisions {
		result.PinnedRevisions = append(result.PinnedRevisions, interpretationRevisionSummaryResponse(revision))
	}
	for _, column := range value.Columns {
		wire := loomapi.ConfiguredColumnContext{OutputId: column.OutputID, Column: column.Column, OccurrenceId: column.OccurrenceID}
		if ready, ok := column.Resolution.Ready(); ok {
			if err := wire.Resolution.FromConfiguredColumnReadyResolution(loomapi.ConfiguredColumnReadyResolution{
				State:                  loomapi.ConfiguredColumnReadyResolutionStateREADY,
				CapabilityCandidateIds: append(make([]string, 0, len(ready.CapabilityCandidateIDs)), ready.CapabilityCandidateIDs...),
				ApplicableRevisionIds:  append(make([]string, 0, len(ready.ApplicableRevisionIDs)), ready.ApplicableRevisionIDs...),
			}); err != nil {
				return loomapi.ConfiguredColumnContextResponse{}, err
			}
		} else {
			state := loomapi.ConfiguredColumnUnavailableResolutionState(column.Resolution.State())
			if !state.Valid() || column.Resolution.Reason() == "" {
				return loomapi.ConfiguredColumnContextResponse{}, fmt.Errorf("invalid configured-column resolution %q", column.Resolution.State())
			}
			if err := wire.Resolution.FromConfiguredColumnUnavailableResolution(loomapi.ConfiguredColumnUnavailableResolution{State: state, Reason: column.Resolution.Reason()}); err != nil {
				return loomapi.ConfiguredColumnContextResponse{}, err
			}
		}
		result.Columns = append(result.Columns, wire)
	}
	return result, nil
}

func interpretationRevisionSummaryResponse(value lifecycle.InterpretationRevisionSummary) loomapi.InterpretationRevisionSummary {
	return loomapi.InterpretationRevisionSummary{
		Id: value.ID, LibraryId: value.LibraryID, ContentDigest: value.ContentDigest,
		Author: value.Author, Explanation: value.Explanation, CreatedAt: value.CreatedAt,
	}
}

func acceptedConstructionReceiptResponse(receipt loomapi.CompileResponse) loomapi.AcceptedConstructionReceipt {
	return loomapi.AcceptedConstructionReceipt{
		ApiVersion:               receipt.ApiVersion,
		AuthorizationScopeDigest: receipt.AuthorizationScopeDigest,
		CapabilitySchemaDigest:   receipt.CapabilitySchemaDigest,
		CompilerVersion:          receipt.CompilerVersion,
		Diagnostics:              receipt.Diagnostics,
		Generation:               receipt.Generation,
		IntentDigest:             receipt.IntentDigest,
		Kind:                     receipt.Kind,
		OutputContractDigest:     receipt.OutputContractDigest,
		Outputs:                  receipt.Outputs,
		ReceiptId:                receipt.ReceiptId,
		RecipeDigest:             receipt.RecipeDigest,
		ResolvedRecipeDigest:     receipt.ResolvedRecipeDigest,
		ResolvedSchemaDigest:     receipt.ResolvedSchemaDigest,
		ShapeDigest:              receipt.ShapeDigest,
		SnapshotToken:            receipt.SnapshotToken,
	}
}

func optionalEnumString[T ~string](value *T) string {
	if value == nil {
		return ""
	}
	return string(*value)
}

func (h *Handlers) previewSavedReceipt(ctx context.Context, project, explorerID string, body *loomapi.PreviewExplorerJSONRequestBody) (loomapi.PreviewResponse, error) {
	return h.previewSavedReceiptWithTimeout(ctx, project, explorerID, body, executionarango.ExplorerPreviewTimeout, false)
}

func (h *Handlers) previewSavedReceiptWithTimeout(ctx context.Context, project, explorerID string, body *loomapi.PreviewExplorerJSONRequestBody, timeout time.Duration, allowSampledAggregate bool) (loomapi.PreviewResponse, error) {
	var result loomapi.PreviewResponse
	if err := h.authorizeAuthoringRead(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, explorer.MalformedRouteError("preview", errors.New("receiptId and outputId are required"))
	}
	limit := dataframeexecution.DefaultPreviewLimit
	if body.Limit != nil {
		limit = *body.Limit
		if limit == 0 {
			limit = -1
		}
	}
	previewCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var encoder *previewResponseEncoder
	preview, err := h.application.Preview(previewCtx, lifecycle.PreviewRequest{Project: project, ExplorerID: explorerID, ReceiptID: body.ReceiptId, OutputID: body.OutputId, Limit: limit, AllowSampledAggregatePreview: allowSampledAggregate, SinkFactory: func(receipt *explorer.CompilationReceipt, columns []explorer.EmittedColumn) (func(map[string]any) error, error) {
		var encoderErr error
		encoder, encoderErr = newPreviewResponseEncoder(receipt, body.OutputId, columns, maxExplorerPreviewResponseBytes)
		if encoderErr != nil {
			return nil, encoderErr
		}
		return encoder.Visit, nil
	}})
	if err != nil {
		return result, previewRouteError(err)
	}
	if encoder == nil {
		return result, previewRouteError(errors.New("native preview response sink was not configured"))
	}
	return previewResponseFromEncoder(encoder, preview.Summary)
}

func previewResponseFromEncoder(encoder *previewResponseEncoder, summary dataframeexecution.PreviewSummary) (loomapi.PreviewResponse, error) {
	var result loomapi.PreviewResponse
	if encoder == nil {
		return result, errors.New("native preview response sink was not configured")
	}
	encoded, err := encoder.Finish()
	if err != nil {
		return result, previewRouteError(err)
	}
	if err := json.Unmarshal(encoded, &result); err != nil {
		return result, err
	}
	result.Sampled = !summary.Complete || summary.Truncated
	partialValidation := summary.PartialValidation
	result.PartialValidation = &partialValidation
	capability := summary.RowLineageCapability
	result.RowLineageCapability.Status = loomapi.RowLineageCapabilityStatusAVAILABLE
	if !capability.Available {
		result.RowLineageCapability.Status = loomapi.RowLineageCapabilityStatusUNAVAILABLE
		reasonCode := capability.ReasonCode
		if reasonCode == "" {
			reasonCode = "ROW_LINEAGE_UNAVAILABLE"
		}
		result.RowLineageCapability.ReasonCode = &reasonCode
		if capability.Operation != "" {
			operation := capability.Operation
			result.RowLineageCapability.Operation = &operation
		}
	}
	return result, nil
}

func (h *Handlers) publishReceipt(ctx context.Context, project, explorerID, authResourcePath string, body *loomapi.PublishExplorerJSONRequestBody) (loomapi.PublishResponse, error) {
	var result loomapi.PublishResponse
	if err := h.authorizeAuthoringWrite(ctx, project, authResourcePath); err != nil {
		return result, err
	}
	if body == nil {
		return result, explorer.MalformedRouteError("publish", errors.New("receiptId is required"))
	}
	value, err := h.application.Publish(ctx, lifecycle.PublishRequest{Project: project, ExplorerID: explorerID, ReceiptID: body.ReceiptId, Actor: authscope.SubjectFromContext(ctx)})
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
	requestID := httpapi.RequestIDFromContext(ctx)
	status := http.StatusInternalServerError
	code, message, stage, path := "INTERNAL_ERROR", "", "internal", ""
	var details map[string]any
	var cause error = err
	typed := false
	var lifecycleErr *lifecycle.Error
	var authoringErr *explorer.AuthoringError
	hasLifecycleError := errors.As(err, &lifecycleErr)
	hasAuthoringError := errors.As(err, &authoringErr)

	if hasAuthoringError && (!hasLifecycleError || errors.Is(authoringErr, lifecycleErr)) {
		typed = true
		status = authoringErr.Status
		code, message, stage, path, details = authoringErr.Diagnostic.Code, authoringErr.Diagnostic.Message, authoringErr.Diagnostic.Stage, authoringErr.Diagnostic.JSONPath, authoringErr.Diagnostic.Details
		if authoringErr.Cause != nil {
			cause = authoringErr.Cause
		}
	} else if hasLifecycleError {
		typed = true
		status = lifecycleErrorStatus(lifecycleErr.Class)
		code, message, stage, path, details, cause = lifecycleErr.Code, lifecycleErr.Message, lifecycleErr.Stage, lifecycleErr.Path, lifecycleErr.Details, lifecycleErr.Cause
	} else if errors.Is(err, explorer.ErrNotFound) {
		typed = true
		status, code, message = http.StatusNotFound, "NOT_FOUND", "Explorer resource not found"
	}
	if !typed {
		stage = operation
		message = fmt.Sprintf("an unexpected internal error occurred during %s", operation)
		details = map[string]any{"operation": operation, "causeType": fmt.Sprintf("%T", err)}
	}
	if stage == "" {
		stage = operation
	}
	if code == "" {
		code = "INTERNAL_ERROR"
	}
	if message == "" {
		message = fmt.Sprintf("an unexpected internal error occurred during %s", operation)
		if details == nil {
			details = map[string]any{"operation": operation, "causeType": fmt.Sprintf("%T", err)}
		}
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
		converted := map[string]interface{}(details)
		body.Details = &converted
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

func (r *Handlers) ApplyExplorerBuilderCommands(ctx context.Context, request loomapi.ApplyExplorerBuilderCommandsRequestObject) (loomapi.ApplyExplorerBuilderCommandsResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID, authResourcePath string, body *loomapi.ApplyExplorerBuilderCommandsJSONRequestBody) (loomapi.ApplyCommandsResponse, error) {
		var result loomapi.ApplyCommandsResponse
		if err := h.authorizeAuthoringWrite(ctx, project, authResourcePath); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("commands", errors.New("request body is required"))
		}
		request, err := directAuthoringJSON[authoringv2.ApplyCommandsRequest](*body)
		if err != nil {
			return result, explorer.MalformedRouteError("commands", err)
		}
		if len(request.Commands) == 1 && request.Commands[0].Type == authoringv2.CommandApplyConstructionProposal {
			var encoder *previewResponseEncoder
			sinkFactory := func(receipt *explorer.CompilationReceipt, columns []explorer.EmittedColumn) (func(map[string]any) error, error) {
				var err error
				encoder, err = newPreviewResponseEncoder(receipt, request.Commands[0].OutputID, columns, maxExplorerPreviewResponseBytes)
				if err != nil {
					return nil, err
				}
				return encoder.Visit, nil
			}
			applied, err := h.application.ApplyCommandsWithConstructionPreview(ctx, project, explorerID, request, authscope.SubjectFromContext(ctx), sinkFactory)
			if err != nil {
				return result, err
			}
			if applied == nil || applied.Commands == nil {
				return result, explorer.ExplorerUnavailable("commands", "AUTHORING_UNAVAILABLE", "construction Apply returned no command response")
			}
			result, err = directAuthoringJSON[loomapi.ApplyCommandsResponse](applied.Commands)
			if err != nil {
				return result, err
			}
			if applied.Accepted == nil || encoder == nil || applied.Accepted.Preview.Receipt == nil {
				return result, nil
			}
			preview, err := previewResponseFromEncoder(encoder, applied.Accepted.Preview.Summary)
			if err != nil {
				return result, nil
			}
			receipt := v2ReceiptResponse(applied.Accepted.Preview.Receipt, applied.Commands.Workspace)
			result.AcceptedConstruction = &loomapi.AcceptedConstruction{
				Receipt:      acceptedConstructionReceiptResponse(receipt),
				Preview:      preview,
				PreviewLimit: applied.Accepted.PreviewLimit,
			}
			return result, nil
		}
		value, err := h.application.ApplyCommands(ctx, project, explorerID, request, authscope.SubjectFromContext(ctx))
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.ApplyCommandsResponse](value)
	}(r, ctx, string(request.Project), string(request.ExplorerId), authResourcePathFromParam(request.Params.AuthResourcePath), request.Body)
	if err == nil {
		return loomapi.ApplyExplorerBuilderCommands200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "applyExplorerBuilderCommands", err)
	return loomapi.NewApplyExplorerBuilderCommandsErrorResponse(status, failure)
}

func (r *Handlers) AssessExplorerRowChange(ctx context.Context, request loomapi.AssessExplorerRowChangeRequestObject) (loomapi.AssessExplorerRowChangeResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.AssessExplorerRowChangeJSONRequestBody) (loomapi.RowChangeAssessmentResponse, error) {
		var result loomapi.RowChangeAssessmentResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("row-change", errors.New("request body is required"))
		}
		rootOccurrenceID := ""
		if body.RootOccurrenceId != nil {
			rootOccurrenceID = *body.RootOccurrenceId
		}
		routeRebase := []authoringv2.RouteRebaseChoice{}
		if body.RouteRebase != nil {
			converted, err := directAuthoringJSON[[]authoringv2.RouteRebaseChoice](*body.RouteRebase)
			if err != nil {
				return result, explorer.MalformedRouteError("row-change", err)
			}
			routeRebase = converted
		}
		value, err := h.application.AssessRowChange(ctx, lifecycle.AssessRowChangeRequest{
			Project: project, ExplorerID: explorerID, Mode: optionalEnumString(body.Mode), SnapshotToken: body.SnapshotToken,
			DraftVersion: body.DraftVersion, DraftDigest: body.DraftDigest, OutputID: body.OutputId,
			RootNodeID: body.RootNodeId, RootOccurrenceID: rootOccurrenceID, RouteRebase: routeRebase,
		})
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.RowChangeAssessmentResponse](struct {
			authoringv2.RowChangeAssessment
			SnapshotToken        string                        `json:"snapshotToken"`
			DraftVersion         int64                         `json:"draftVersion"`
			DraftDigest          string                        `json:"draftDigest"`
			CandidateReceiptID   string                        `json:"candidateReceiptId,omitempty"`
			CandidateDocument    *authoringv2.Document         `json:"candidateDocument,omitempty"`
			CandidateNetwork     *authoringv2.AuthoringNetwork `json:"candidateNetwork,omitempty"`
			CandidateRowIdentity *authoringv2.RowIdentityPlan  `json:"candidateRowIdentity,omitempty"`
			Diagnostics          []map[string]any              `json:"diagnostics"`
		}{
			RowChangeAssessment: value.Assessment,
			SnapshotToken:       value.SnapshotToken,
			DraftVersion:        value.DraftVersion,
			DraftDigest:         value.DraftDigest,
			CandidateReceiptID:  value.CandidateReceiptID,
			CandidateDocument:   value.CandidateDocument, CandidateNetwork: value.CandidateNetwork, CandidateRowIdentity: value.CandidateRowIdentity,
			Diagnostics: []map[string]any{},
		})
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.AssessExplorerRowChange200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "assessExplorerRowChange", err)
	return loomapi.NewAssessExplorerRowChangeErrorResponse(status, failure)
}

func (r *Handlers) CheckExplorerPopulationMapping(ctx context.Context, request loomapi.CheckExplorerPopulationMappingRequestObject) (loomapi.CheckExplorerPopulationMappingResponseObject, error) {
	project := string(request.Project)
	explorerID := string(request.ExplorerId)
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.CheckExplorerPopulationMappingJSONRequestBody) (loomapi.PopulationMappingResponse, error) {
		var result loomapi.PopulationMappingResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("populationMapping", errors.New("receiptId and outputId are required"))
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
	}(r, ctx, project, explorerID, request.Body)
	if err == nil {
		return loomapi.CheckExplorerPopulationMapping200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "checkExplorerPopulationMapping", err)
	return loomapi.NewCheckExplorerPopulationMappingErrorResponse(status, failure)
}

func (r *Handlers) CreateInterpretationRevision(ctx context.Context, request loomapi.CreateInterpretationRevisionRequestObject) (loomapi.CreateInterpretationRevisionResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, authResourcePath string, body *loomapi.CreateInterpretationRevisionJSONRequestBody) (loomapi.InterpretationRevision, error) {
		var result loomapi.InterpretationRevision
		if err := h.authorizeAuthoringWrite(ctx, project, authResourcePath); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("interpretations", errors.New("request body is required"))
		}
		applicability, err := directAuthoringJSON[explorer.InterpretationApplicability](body.Applicability)
		if err != nil {
			return result, explorer.MalformedRouteError("interpretations", err)
		}
		rules, err := directAuthoringJSON[[]explorer.InterpretationRule](body.Rules)
		if err != nil {
			return result, explorer.MalformedRouteError("interpretations", err)
		}
		parentRevisionID := ""
		if body.ParentRevisionId != nil {
			parentRevisionID = *body.ParentRevisionId
		}
		value, err := h.application.CreateInterpretationRevision(ctx, lifecycle.CreateInterpretationRevisionRequest{
			Project: project, LibraryID: body.LibraryId, ParentRevisionID: parentRevisionID,
			Applicability: applicability, Rules: rules, Explanation: body.Explanation,
			Author: authscope.SubjectFromContext(ctx),
		})
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.InterpretationRevision](value)
	}(r, ctx, string(request.Project), authResourcePathFromParam(request.Params.AuthResourcePath), request.Body)
	if err == nil {
		return loomapi.CreateInterpretationRevision201JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "createInterpretationRevision", err)
	return loomapi.NewCreateInterpretationRevisionErrorResponse(status, failure)
}

func (r *Handlers) CreateInterpretationRevisionFromColumn(ctx context.Context, request loomapi.CreateInterpretationRevisionFromColumnRequestObject) (loomapi.CreateInterpretationRevisionFromColumnResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID, authResourcePath string, body *loomapi.CreateInterpretationRevisionFromColumnJSONRequestBody) (loomapi.InterpretationRevision, error) {
		var result loomapi.InterpretationRevision
		if err := h.authorizeAuthoringWrite(ctx, project, authResourcePath); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("interpretation-create", errors.New("request body is required"))
		}
		parentRevisionID := ""
		if body.ParentRevisionId != nil {
			parentRevisionID = *body.ParentRevisionId
		}
		value, err := h.application.CreateInterpretationRevisionFromColumn(ctx, lifecycle.CreateInterpretationRevisionFromColumnRequest{
			Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
			ExpectedDraftVersion: body.ExpectedDraftVersion, ExpectedDraftDigest: body.ExpectedDraftDigest,
			OutputID: body.OutputId, Column: body.Column, LibraryID: body.LibraryId, ParentRevisionID: parentRevisionID,
			Explanation: body.Explanation, Author: authscope.SubjectFromContext(ctx),
		})
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.InterpretationRevision](value)
	}(r, ctx, string(request.Project), string(request.ExplorerId), authResourcePathFromParam(request.Params.AuthResourcePath), request.Body)
	if err == nil {
		return loomapi.CreateInterpretationRevisionFromColumn201JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "createInterpretationRevisionFromColumn", err)
	return loomapi.NewCreateInterpretationRevisionFromColumnErrorResponse(status, failure)
}

func (r *Handlers) GetExplorerAuthoringCapability(ctx context.Context, request loomapi.GetExplorerAuthoringCapabilityRequestObject) (loomapi.GetExplorerAuthoringCapabilityResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project string) (loomapi.AuthoringCapability, error) {
		var result loomapi.AuthoringCapability
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		return loomapi.AuthoringCapability{ApiVersion: loomapi.LoomCalyprOrgexplorerAuthoringv2, Kind: loomapi.ExplorerAuthoringCapabilities, Operations: []loomapi.AuthoringCapabilityOperations{loomapi.AuthoringCapabilityOperationsBuilder, loomapi.AuthoringCapabilityOperationsSuggestions, loomapi.AuthoringCapabilityOperationsRowChange, loomapi.AuthoringCapabilityOperationsPreview, loomapi.AuthoringCapabilityOperationsInterpretationPreview, loomapi.AuthoringCapabilityOperationsConfiguredColumnContext, loomapi.AuthoringCapabilityOperationsInterpretationCreate, loomapi.AuthoringCapabilityOperationsPublish, loomapi.AuthoringCapabilityOperationsCommands, loomapi.AuthoringCapabilityOperationsReconcile, loomapi.AuthoringCapabilityOperationsSourceRuleCreate}, PreviewLimits: []int{10, 25, 50, 100}, Features: loomapi.AuthoringFeatures{EmissionFilters: true, EmissionCharts: true}}, nil
	}(r, ctx, string(request.Project))
	if err == nil {
		return loomapi.GetExplorerAuthoringCapability200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "getExplorerAuthoringCapability", err)
	return loomapi.NewGetExplorerAuthoringCapabilityErrorResponse(status, failure)
}

func (r *Handlers) GetExplorerBuilder(ctx context.Context, request loomapi.GetExplorerBuilderRequestObject) (loomapi.GetExplorerBuilderResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string) (loomapi.BuilderState, error) {
		var result loomapi.BuilderState
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		value, err := h.application.Builder(ctx, lifecycle.BuilderRequest{Project: project, ExplorerID: explorerID})
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.BuilderState](value)
	}(r, ctx, string(request.Project), string(request.ExplorerId))
	if err == nil {
		return loomapi.GetExplorerBuilder200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "getExplorerBuilder", err)
	return loomapi.NewGetExplorerBuilderErrorResponse(status, failure)
}

func (r *Handlers) GetInterpretationRevision(ctx context.Context, request loomapi.GetInterpretationRevisionRequestObject) (loomapi.GetInterpretationRevisionResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, revisionID string) (loomapi.InterpretationRevision, error) {
		var result loomapi.InterpretationRevision
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		value, err := h.application.GetInterpretationRevision(ctx, lifecycle.GetInterpretationRevisionRequest{Project: project, RevisionID: revisionID})
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.InterpretationRevision](value)
	}(r, ctx, string(request.Project), request.RevisionId)
	if err == nil {
		return loomapi.GetInterpretationRevision200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "getInterpretationRevision", err)
	return loomapi.NewGetInterpretationRevisionErrorResponse(status, failure)
}

func (r *Handlers) ListInterpretationLibraries(ctx context.Context, request loomapi.ListInterpretationLibrariesRequestObject) (loomapi.ListInterpretationLibrariesResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project string) (loomapi.InterpretationLibraryListResponse, error) {
		var result loomapi.InterpretationLibraryListResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
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
	}(r, ctx, string(request.Project))
	if err == nil {
		return loomapi.ListInterpretationLibraries200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "listInterpretationLibraries", err)
	return loomapi.NewListInterpretationLibrariesErrorResponse(status, failure)
}

func (r *Handlers) PreviewInterpretationCandidate(ctx context.Context, request loomapi.PreviewInterpretationCandidateRequestObject) (loomapi.PreviewInterpretationCandidateResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.PreviewInterpretationCandidateJSONRequestBody) (loomapi.InterpretationPreviewResponse, error) {
		var result loomapi.InterpretationPreviewResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("interpretation-preview", errors.New("request body is required"))
		}
		limit := dataframeexecution.DefaultPreviewLimit
		if body.Limit != nil {
			limit = *body.Limit
		}
		previewCtx, cancel := context.WithTimeout(ctx, executionarango.ExplorerPreviewTimeout)
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
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.PreviewInterpretationCandidate200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "previewInterpretationCandidate", err)
	return loomapi.NewPreviewInterpretationCandidateErrorResponse(status, failure)
}

func (r *Handlers) ReconcileExplorerBuilder(ctx context.Context, request loomapi.ReconcileExplorerBuilderRequestObject) (loomapi.ReconcileExplorerBuilderResponseObject, error) {
	project := string(request.Project)
	explorerID := string(request.ExplorerId)
	value, err := func(h *Handlers, ctx context.Context, project, explorerID, authResourcePath string, body *loomapi.ReconcileExplorerBuilderJSONRequestBody) (loomapi.CompileResponse, error) {
		var result loomapi.CompileResponse
		if err := h.authorizeAuthoringWrite(ctx, project, authResourcePath); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("reconcile", errors.New("request body is required"))
		}
		reconciled, err := h.application.ReconcileWithWorkspace(ctx, lifecycle.ReconcileRequest{Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken, DraftVersion: body.DraftVersion, DraftDigest: body.DraftDigest, OutputID: optionalString(body.OutputId)})
		if err != nil {
			return result, err
		}
		return v2ReceiptResponse(reconciled.Receipt, reconciled.Workspace), nil
	}(r, ctx, project, explorerID, authResourcePathFromParam(request.Params.AuthResourcePath), request.Body)
	if err == nil {
		return loomapi.ReconcileExplorerBuilder200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "reconcileExplorerBuilder", err)
	return loomapi.NewReconcileExplorerBuilderErrorResponse(status, failure)
}

func (r *Handlers) ResolveConfiguredColumnContext(ctx context.Context, request loomapi.ResolveConfiguredColumnContextRequestObject) (loomapi.ResolveConfiguredColumnContextResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.ResolveConfiguredColumnContextJSONRequestBody) (loomapi.ConfiguredColumnContextResponse, error) {
		var result loomapi.ConfiguredColumnContextResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("interpretation-context", errors.New("request body is required"))
		}
		value, err := h.application.ConfiguredColumnContext(ctx, lifecycle.ConfiguredColumnContextRequest{
			Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
			ExpectedDraftVersion: body.ExpectedDraftVersion, ExpectedDraftDigest: body.ExpectedDraftDigest,
		})
		if err != nil {
			return result, err
		}
		return configuredColumnContextResponse(value)
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.ResolveConfiguredColumnContext200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "resolveConfiguredColumnContext", err)
	return loomapi.NewResolveConfiguredColumnContextErrorResponse(status, failure)
}

func (r *Handlers) SearchExplorerCandidates(ctx context.Context, request loomapi.SearchExplorerCandidatesRequestObject) (loomapi.SearchExplorerCandidatesResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.SearchExplorerCandidatesJSONRequestBody) (loomapi.CandidateSearchResponse, error) {
		var result loomapi.CandidateSearchResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("suggestions", errors.New("request body is required"))
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
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.SearchExplorerCandidates200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "searchExplorerCandidates", err)
	return loomapi.NewSearchExplorerCandidatesErrorResponse(status, failure)
}

func (r *Handlers) SearchExplorerSchemaFields(ctx context.Context, request loomapi.SearchExplorerSchemaFieldsRequestObject) (loomapi.SearchExplorerSchemaFieldsResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.SchemaFieldSearchRequest) (loomapi.SchemaFieldSearchResponse, error) {
		var result loomapi.SchemaFieldSearchResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("schema-fields", errors.New("request body is required"))
		}
		request := lifecycle.SearchSchemaFieldsRequest{
			Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken, NodeID: body.NodeId,
		}
		if body.Query != nil {
			request.Query = *body.Query
		}
		if body.Limit != nil {
			request.Limit = *body.Limit
		}
		if body.Cursor != nil {
			request.Cursor = *body.Cursor
		}
		value, err := h.application.SearchSchemaFields(ctx, request)
		if err != nil {
			return result, err
		}
		wire, err := directAuthoringJSON[loomapi.SchemaFieldSearchResponse](value)
		wire.ApiVersion = loomapi.LoomCalyprOrgexplorerAuthoringv2
		wire.Kind = loomapi.ExplorerBuilderGeneratedSchemaFields
		return wire, err
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.SearchExplorerSchemaFields200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "searchExplorerSchemaFields", err)
	return loomapi.NewSearchExplorerSchemaFieldsErrorResponse(status, failure)
}

func (r *Handlers) TraceExplorerCell(ctx context.Context, request loomapi.TraceExplorerCellRequestObject) (loomapi.TraceExplorerCellResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.TraceExplorerCellJSONRequestBody) (loomapi.CellTraceResponse, error) {
		var result loomapi.CellTraceResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("trace", errors.New("receiptId, outputId, rowId, and column are required"))
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
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.TraceExplorerCell200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "traceExplorerCell", err)
	return loomapi.NewTraceExplorerCellErrorResponse(status, failure)
}

func (r *Handlers) TraceExplorerRowLineage(ctx context.Context, request loomapi.TraceExplorerRowLineageRequestObject) (loomapi.TraceExplorerRowLineageResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.TraceExplorerRowLineageJSONRequestBody) (loomapi.RowLineageResponse, error) {
		var result loomapi.RowLineageResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("rowLineage", errors.New("receiptId, outputId, and rowId are required"))
		}
		request := lifecycle.RowLineageRequest{
			Project: project, ExplorerID: explorerID,
			ReceiptID: body.ReceiptId, OutputID: body.OutputId, RowID: body.RowId,
		}
		if body.Offset != nil {
			request.Offset = *body.Offset
		}
		if body.Limit != nil {
			request.Limit = *body.Limit
		}
		value, err := h.application.RowLineage(ctx, request)
		if err != nil {
			return result, err
		}
		result.ReceiptId = value.ReceiptID
		result.OutputId = value.OutputID
		result.RowId = value.RowID
		result.Status = loomapi.RowLineageResponseStatus(value.Status)
		result.HasMore = &value.HasMore
		if value.NextOffset != nil {
			result.NextOffset = value.NextOffset
		}
		contributors := make([]loomapi.RowLineageContributor, 0, len(value.Contributors))
		for _, contributor := range value.Contributors {
			contributors = append(contributors, loomapi.RowLineageContributor{
				ResourceType: contributor.ResourceType, ResourceId: contributor.ResourceID,
				OccurrenceKey: contributor.OccurrenceKey,
			})
		}
		result.Contributors = &contributors
		if value.ReasonCode != "" {
			result.ReasonCode = &value.ReasonCode
		}
		if value.Operation != "" {
			result.Operation = &value.Operation
		}
		return result, nil
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.TraceExplorerRowLineage200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "traceExplorerRowLineage", err)
	return loomapi.NewTraceExplorerRowLineageErrorResponse(status, failure)
}
