package httpapi

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
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

// StatusForErrorCode exposes the same semantic HTTP classification used by
// REST handlers to transports that already have a stable Loom error code.
func StatusForErrorCode(code string) int {
	return statusForCode(normalizeHTTPCode(code), 0)
}

func normalizeHTTPCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	code = strings.ReplaceAll(code, "-", "_")
	switch code {
	case "PROJECT_REQUIRED", "ROOT_RESOURCE_TYPE_REQUIRED", "UNAUTHORIZED_PROJECT", "UNKNOWN_FIELD", "FIELD_NOT_POPULATED", "INVALID_TRAVERSAL", "UNSAFE_TRAVERSAL_ROUTE", "INVALID_FILTER", "UNBOUNDED_PIVOT", "INVALID_PIVOT_COLUMN", "INVALID_SLICE", "PLAN_TOO_EXPENSIVE", "INVALID_CURSOR", "STALE_CURSOR", "DATASET_GENERATION_CHANGED", "UNSUPPORTED_EXPORT_FORMAT", "CLIENT_CANCELED", "BACKEND_UNAVAILABLE", "RECEIPT_STORE_UNAVAILABLE", "PREVIEW_TIMEOUT", "PREVIEW_RESPONSE_TOO_LARGE", "DYNAMIC_SCHEMA_DRIFT", "RECIPE_CONTRACT_VIOLATION", "DATASET_NOT_FOUND", "SCHEMA_CONFLICT", "INVALID_RESOURCE_TYPE", "NO_ACTIVE_GENERATION", "RESOURCE_DECODE_FAILED", "REFERENCE_NOT_RESOLVED", "QUERY_DEPTH_EXCEEDED", "INVALID_REQUEST", "INVALID_DATA", "INVALID_SELECTOR", "UNAUTHENTICATED", "FORBIDDEN", "RECIPE_NOT_FOUND", "RECIPE_RESOLUTION_FAILED", "RECIPE_EXECUTION_NOT_FOUND", "EXPORT_LIMIT_EXCEEDED", "INGEST_PREFLIGHT_FAILED", "GENERATION_LOAD_INCOMPLETE", "GENERATION_ACTIVATION_UNKNOWN", "INVALID_GENERATION_FILE", "DUPLICATE_GENERATION_FILE", "PUBLICATION_IN_PROGRESS", "PUBLICATION_CONFLICT", "PUBLICATION_LEASE_LOST", "OUTPUT_ENCODING_FAILED", "NOT_FOUND", "METHOD_NOT_ALLOWED", "PAYLOAD_TOO_LARGE", "GRAPHQL_VALIDATION_FAILED":
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
	case "SCHEMA_CONFLICT", "STALE_CURSOR", "DATASET_GENERATION_CHANGED", "DYNAMIC_SCHEMA_DRIFT", "RECIPE_CONTRACT_VIOLATION":
		return http.StatusConflict
	case "INVALID_DATA", "RECIPE_RESOLUTION_FAILED", "INGEST_PREFLIGHT_FAILED", "GENERATION_LOAD_INCOMPLETE":
		return http.StatusUnprocessableEntity
	case "INVALID_SELECTOR", "INVALID_REQUEST", "INVALID_LIMIT", "INVALID_RESOURCE_TYPE":
		return http.StatusBadRequest
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
	case "PREVIEW_RESPONSE_TOO_LARGE":
		return http.StatusRequestEntityTooLarge
	case "PLAN_TOO_EXPENSIVE":
		return http.StatusTooManyRequests
	case "UNSUPPORTED_MEDIA_TYPE":
		return http.StatusUnsupportedMediaType
	case "BACKEND_UNAVAILABLE", "RECEIPT_STORE_UNAVAILABLE":
		return http.StatusServiceUnavailable
	case "PREVIEW_TIMEOUT":
		return http.StatusGatewayTimeout
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
