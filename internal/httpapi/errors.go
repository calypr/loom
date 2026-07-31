package httpapi

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/ingest"
	"github.com/gofiber/fiber/v3"
)

// ErrorResponse is the transport-neutral JSON payload the HTTP adapter can
// write before a streaming response has begun.
type ErrorResponse struct {
	Error HTTPErrorBody `json:"error"`
}

type HTTPErrorBody struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	FieldPath []string       `json:"fieldPath,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
	Retryable bool           `json:"retryable"`
	RequestID string         `json:"requestId,omitempty"`
}

// MappedError is the status and safe body chosen for a semantic error. It is
// intentionally independent of Fiber so the same mapping can be tested and
// reused by the export route and future HTTP adapters.
type MappedError struct {
	Status int
	Body   ErrorResponse
	Cause  error
}

// MapDataframeError maps a service error without inspecting its text. Unknown
// errors are always redacted to INTERNAL_ERROR. The original cause is kept
// only for operator logging.
func MapDataframeError(err error, requestID string) MappedError {
	if err == nil {
		return MappedError{}
	}
	if errors.Is(err, authscope.ErrUnauthenticated) {
		err = dataframeerrors.Wrap(err, dataframeerrors.CodeUnauthenticated, "")
	}
	if errors.Is(err, authscope.ErrForbidden) {
		err = dataframeerrors.Wrap(err, dataframeerrors.CodeForbidden, "")
	}
	if errors.Is(err, authscope.ErrAuthorizationBackendUnavailable) {
		err = dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	var preflightErr *ingest.PreflightError
	if errors.As(err, &preflightErr) {
		return MappedError{Status: http.StatusUnprocessableEntity, Body: ErrorResponse{Error: HTTPErrorBody{
			Code: "INGEST_PREFLIGHT_FAILED", Message: messageForCode("INGEST_PREFLIGHT_FAILED"), Details: preflightDetails(preflightErr), RequestID: requestID,
		}}, Cause: err}
	}
	var incompleteErr *ingest.GenerationLoadIncompleteError
	if errors.As(err, &incompleteErr) {
		return MappedError{Status: http.StatusUnprocessableEntity, Body: ErrorResponse{Error: HTTPErrorBody{
			Code: "GENERATION_LOAD_INCOMPLETE", Message: messageForCode("GENERATION_LOAD_INCOMPLETE"), Details: map[string]any{
				"validationErrors": incompleteErr.ValidationErrors,
				"generationErrors": incompleteErr.GenerationErrors,
				"edgeErrors":       incompleteErr.EdgeErrors,
			}, RequestID: requestID,
		}}, Cause: err}
	}
	var activationErr *ingest.ActivationOutcomeError
	if errors.As(err, &activationErr) {
		return MappedError{Status: http.StatusConflict, Body: ErrorResponse{Error: HTTPErrorBody{
			Code: "GENERATION_ACTIVATION_UNKNOWN", Message: messageForCode("GENERATION_ACTIVATION_UNKNOWN"), Retryable: false, RequestID: requestID,
		}}, Cause: err}
	}
	if errors.Is(err, os.ErrInvalid) {
		return MappedError{Status: http.StatusBadRequest, Body: ErrorResponse{Error: HTTPErrorBody{
			Code: "INVALID_REQUEST", Message: messageForCode("INVALID_REQUEST"), RequestID: requestID,
		}}, Cause: err}
	}
	var fiberErr *fiber.Error
	if errors.As(err, &fiberErr) {
		code := "INTERNAL_ERROR"
		switch fiberErr.Code {
		case http.StatusNotFound:
			code = "NOT_FOUND"
		case http.StatusMethodNotAllowed:
			code = "METHOD_NOT_ALLOWED"
		case http.StatusRequestEntityTooLarge:
			code = "PAYLOAD_TOO_LARGE"
		case http.StatusBadRequest:
			code = "INVALID_REQUEST"
		case http.StatusUnsupportedMediaType:
			code = "UNSUPPORTED_MEDIA_TYPE"
		}
		return MappedError{Status: fiberErr.Code, Body: ErrorResponse{Error: HTTPErrorBody{Code: code, Message: messageForCode(code), RequestID: requestID}}, Cause: err}
	}

	if mapped, ok := err.(*apiError); ok {
		if mapped.Cause != nil {
			// A route may have a useful transport default, but a typed cause is
			// authoritative (for example a ClickHouse outage wrapped by export).
			if _, typed := dataframeerrors.AsUserError(mapped.Cause); typed || errors.Is(mapped.Cause, dataframeerrors.ErrBackendUnavailable) || errors.Is(mapped.Cause, context.DeadlineExceeded) || errors.Is(mapped.Cause, context.Canceled) {
				causeMapped := MapDataframeError(mapped.Cause, requestID)
				causeMapped.Body.Error.FieldPath = append([]string(nil), mapped.FieldPath...)
				if len(mapped.Details) != 0 {
					causeMapped.Body.Error.Details = mapped.Details
				}
				return causeMapped
			}
			// A legacy authorization adapter may still return an opaque denial;
			// retain the route's safe FORBIDDEN/UNAUTHENTICATED classification.
			code := normalizeHTTPCode(mapped.Code)
			if code == "INVALID_DATA" {
				return MappedError{Status: statusForCode(code, mapped.Status), Body: ErrorResponse{Error: HTTPErrorBody{Code: code, Message: messageForCode(code), Retryable: false, RequestID: requestID}}, Cause: mapped.Cause}
			}
			if code != "FORBIDDEN" && code != "UNAUTHENTICATED" {
				return MapDataframeError(mapped.Cause, requestID)
			}
		}
		code := normalizeHTTPCode(mapped.Code)
		return MappedError{
			Status: statusForCode(code, mapped.Status),
			Body: ErrorResponse{Error: HTTPErrorBody{
				Code: code, Message: messageForCode(code), Retryable: dataframeerrors.IsRetryableCode(dataframeerrors.ErrorCode(code)), RequestID: requestID,
			}},
			Cause: mapped.Cause,
		}
	}

	if userErr, ok := dataframeerrors.AsUserError(err); ok {
		code := normalizeHTTPCode(userErr.Code())
		return MappedError{
			Status: statusForCode(code, 0),
			Body: ErrorResponse{Error: HTTPErrorBody{
				Code: code, Message: dataframeerrors.PublicMessage(err), FieldPath: userErr.FieldPath(), Details: userErr.Details(), Retryable: userErr.Retryable() || dataframeerrors.IsRetryableCode(dataframeerrors.ErrorCode(code)), RequestID: requestID,
			}},
			Cause: err,
		}
	}

	code := "INTERNAL_ERROR"
	if errors.Is(err, context.Canceled) {
		code = "CLIENT_CANCELED"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, dataframeerrors.ErrBackendUnavailable) {
		code = "BACKEND_UNAVAILABLE"
	}
	return MappedError{
		Status: statusForCode(code, 0),
		Body:   ErrorResponse{Error: HTTPErrorBody{Code: code, Message: messageForCode(code), Retryable: code == "BACKEND_UNAVAILABLE", RequestID: requestID}},
		Cause:  err,
	}
}

func preflightDetails(err *ingest.PreflightError) map[string]any {
	if err == nil || len(err.Report.Issues) == 0 {
		return nil
	}
	issues := make([]map[string]any, 0, len(err.Report.Issues))
	for _, issue := range err.Report.Issues {
		item := map[string]any{"code": issue.Code}
		if issue.File != "" {
			item["file"] = issue.File
		}
		if issue.ResourceType != "" {
			item["resourceType"] = issue.ResourceType
		}
		if issue.Row > 0 {
			item["row"] = issue.Row
		}
		issues = append(issues, item)
	}
	return map[string]any{"issues": issues}
}

func normalizeHTTPCode(code string) string {
	if strings.EqualFold(strings.TrimSpace(code), "legacy_import_disabled") {
		// Preserve this pre-existing control-plane response for clients while
		// all semantic data errors use the shared uppercase registry.
		return "legacy_import_disabled"
	}
	code = strings.ToUpper(strings.TrimSpace(code))
	code = strings.ReplaceAll(code, "-", "_")
	switch code {
	case "UNAUTHORIZED", "AUTHENTICATION_REQUIRED":
		return "UNAUTHENTICATED"
	case "FORBIDDEN":
		return "FORBIDDEN"
	case "MISSING_PROJECT":
		return "PROJECT_REQUIRED"
	case "MISSING_RESOURCE_TYPE":
		return "INVALID_RESOURCE_TYPE"
	case "INVALID_LIMIT":
		return "INVALID_LIMIT"
	case "GENERATION_NOT_FOUND":
		return "NO_ACTIVE_GENERATION"
	case "UNSUPPORTED_MEDIA_TYPE":
		return "UNSUPPORTED_MEDIA_TYPE"
	case "EXPORT_NOT_CONFIGURED":
		return "BACKEND_UNAVAILABLE"
	case "STAGE_FAILED":
		return "BACKEND_UNAVAILABLE"
	case "INTERNAL_ERROR":
		return "INTERNAL_ERROR"
	case "INVALID_NDJSON", "INVALID_MULTIPART_FORM", "INVALID_IMPORT_REQUEST", "BULK_LOAD_FAILED", "RAW_LOAD_FAILED", "INVALID_GENERATION_REQUEST", "INVALID_EXPORT_REQUEST", "MISSING_DATA_TYPE", "MISSING_EXPORT_FORMAT", "INVALID_TRUNCATE", "INVALID_USE_GENERIC", "INVALID_FILE_COUNT", "MISSING_FILE", "MISSING_GENERATION", "MISSING_GENERATION_IDENTITY":
		return "INVALID_REQUEST"
	case "EXPORT_FAILED":
		return "INTERNAL_ERROR"
	case "PROJECT_REQUIRED", "ROOT_RESOURCE_TYPE_REQUIRED", "UNAUTHORIZED_PROJECT", "UNKNOWN_FIELD", "FIELD_NOT_POPULATED", "INVALID_TRAVERSAL", "UNSAFE_TRAVERSAL_ROUTE", "INVALID_FILTER", "UNBOUNDED_PIVOT", "INVALID_PIVOT_COLUMN", "INVALID_SLICE", "PLAN_TOO_EXPENSIVE", "INVALID_CURSOR", "STALE_CURSOR", "DATASET_GENERATION_CHANGED", "UNSUPPORTED_EXPORT_FORMAT", "CLIENT_CANCELED", "BACKEND_UNAVAILABLE", "DATASET_NOT_FOUND", "SCHEMA_CONFLICT", "INVALID_RESOURCE_TYPE", "NO_ACTIVE_GENERATION", "RESOURCE_DECODE_FAILED", "REFERENCE_NOT_RESOLVED", "QUERY_DEPTH_EXCEEDED", "INVALID_REQUEST", "INVALID_DATA", "UNAUTHENTICATED", "RECIPE_NOT_FOUND", "RECIPE_EXECUTION_NOT_FOUND", "EXPORT_LIMIT_EXCEEDED", "INGEST_PREFLIGHT_FAILED", "GENERATION_LOAD_INCOMPLETE", "GENERATION_ACTIVATION_UNKNOWN", "INVALID_GENERATION_FILE", "DUPLICATE_GENERATION_FILE", "PUBLICATION_IN_PROGRESS", "PUBLICATION_CONFLICT", "PUBLICATION_LEASE_LOST", "OUTPUT_ENCODING_FAILED", "NOT_FOUND", "METHOD_NOT_ALLOWED", "PAYLOAD_TOO_LARGE", "LEGACY_IMPORT_DISABLED":
		return code
	default:
		return "INTERNAL_ERROR"
	}
}

func statusForCode(code string, fallback int) int {
	switch code {
	case "UNAUTHENTICATED":
		return http.StatusUnauthorized
	case "FORBIDDEN", "UNAUTHORIZED_PROJECT":
		return http.StatusForbidden
	case "DATASET_NOT_FOUND", "NO_ACTIVE_GENERATION", "RECIPE_NOT_FOUND", "RECIPE_EXECUTION_NOT_FOUND":
		return http.StatusNotFound
	case "SCHEMA_CONFLICT", "STALE_CURSOR", "DATASET_GENERATION_CHANGED":
		return http.StatusConflict
	case "INVALID_DATA", "INGEST_PREFLIGHT_FAILED", "GENERATION_LOAD_INCOMPLETE":
		return http.StatusUnprocessableEntity
	case "GENERATION_ACTIVATION_UNKNOWN":
		return http.StatusConflict
	case "PUBLICATION_IN_PROGRESS", "PUBLICATION_CONFLICT":
		return http.StatusConflict
	case "PUBLICATION_LEASE_LOST":
		return http.StatusServiceUnavailable
	case "OUTPUT_ENCODING_FAILED":
		return http.StatusInternalServerError
	case "EXPORT_LIMIT_EXCEEDED":
		return http.StatusRequestEntityTooLarge
	case "UNSUPPORTED_MEDIA_TYPE":
		return http.StatusUnsupportedMediaType
	case "BACKEND_UNAVAILABLE":
		return http.StatusServiceUnavailable
	case "CLIENT_CANCELED":
		return 499
	case "INTERNAL_ERROR":
		return http.StatusInternalServerError
	}
	if fallback >= 400 {
		return fallback
	}
	return http.StatusBadRequest
}

func messageForCode(code string) string {
	if msg := dataframeerrors.PublicMessage(dataframeerrors.NewError(dataframeerrors.ErrorCode(code), "")); msg != "internal server error" {
		return msg
	}
	switch code {
	case "UNAUTHENTICATED":
		return "authentication is required"
	case "FORBIDDEN", "UNAUTHORIZED_PROJECT":
		return "the requested resource is not available"
	case "INVALID_REQUEST":
		return "the request is invalid"
	case "INVALID_DATA", "INGEST_PREFLIGHT_FAILED", "GENERATION_LOAD_INCOMPLETE":
		return "the uploaded data is invalid"
	case "UNSUPPORTED_MEDIA_TYPE":
		return "the request media type is not supported"
	case "BACKEND_UNAVAILABLE":
		return "the backend is temporarily unavailable"
	case "GENERATION_ACTIVATION_UNKNOWN":
		return "generation activation status is unknown; inspect the active generation before retrying"
	case "PUBLICATION_IN_PROGRESS":
		return "an identical publication is already in progress"
	case "PUBLICATION_CONFLICT":
		return "the publication changed while it was being committed"
	case "PUBLICATION_LEASE_LOST":
		return "publication ownership was lost"
	case "OUTPUT_ENCODING_FAILED":
		return "the response data could not be encoded"
	case "CLIENT_CANCELED":
		return "the request was canceled"
	case "DATASET_NOT_FOUND":
		return "the requested dataset was not found"
	case "SCHEMA_CONFLICT":
		return "the published dataset sources have incompatible schemas"
	case "LEGACY_IMPORT_DISABLED", "legacy_import_disabled":
		return "the requested import mode is disabled"
	case "NOT_FOUND":
		return "the requested route was not found"
	case "METHOD_NOT_ALLOWED":
		return "the requested method is not allowed"
	case "PAYLOAD_TOO_LARGE":
		return "the request payload is too large"
	default:
		return "internal server error"
	}
}

// IsMappedError lets a coordinator preserve an already mapped error while
// adding request logging.
func IsMappedError(err error) bool {
	var mapped *apiError
	return errors.As(err, &mapped)
}
