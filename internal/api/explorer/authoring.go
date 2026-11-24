package explorerapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	loomapi "github.com/calypr/loom/api/loomapi"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/errors/authpolicy"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/interpretation"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	httpctx "github.com/calypr/loom/internal/foundation/httpctx"
)

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

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

func interpretationRevisionSummaryResponse(value interpretation.InterpretationRevisionSummary) loomapi.InterpretationRevisionSummary {
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

func authoringErrorForOpenAPI(ctx context.Context, operation string, err error) (int, loomapi.ErrorResponse) {
	requestID := httpctx.RequestIDFromContext(ctx)
	status := http.StatusInternalServerError
	code, message, stage, path := "INTERNAL_ERROR", "", "internal", ""
	var details map[string]any
	cause := err
	typed := false
	var lifecycleErr *ports.Error
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

func lifecycleErrorStatus(class ports.ErrorClass) int {
	switch class {
	case ports.ClassMalformed:
		return http.StatusBadRequest
	case ports.ClassForbidden:
		return http.StatusForbidden
	case ports.ClassNotFound:
		return http.StatusNotFound
	case ports.ClassConflict:
		return http.StatusConflict
	case ports.ClassUnprocessable:
		return http.StatusUnprocessableEntity
	case ports.ClassUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
