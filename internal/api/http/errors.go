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
	if _, ok := httpCodePolicies[code]; ok {
		return code
	}
	return "INTERNAL_ERROR"
}

func statusForCode(code string, fallback int) int {
	if policy, ok := httpCodePolicies[code]; ok {
		return policy.Status
	}
	if fallback >= 400 {
		return fallback
	}
	return http.StatusBadRequest
}

type httpCodePolicy struct {
	Status int
}

// httpCodePolicies is the transport registry. Domain ErrorCode declarations
// are checked against this table by TestHTTPCodePolicyCoversEveryPublicError.
// Entries with no special transport status intentionally use HTTP 400.
var httpCodePolicies = map[string]httpCodePolicy{
	"PROJECT_REQUIRED": {http.StatusBadRequest}, "ROOT_RESOURCE_TYPE_REQUIRED": {http.StatusBadRequest}, "UNAUTHORIZED_PROJECT": {http.StatusForbidden},
	"UNKNOWN_FIELD": {http.StatusBadRequest}, "FIELD_NOT_POPULATED": {http.StatusBadRequest}, "INVALID_TRAVERSAL": {http.StatusBadRequest}, "UNSAFE_TRAVERSAL_ROUTE": {http.StatusBadRequest},
	"INVALID_FILTER": {http.StatusBadRequest}, "UNBOUNDED_PIVOT": {http.StatusBadRequest}, "INVALID_PIVOT_COLUMN": {http.StatusBadRequest}, "INVALID_SLICE": {http.StatusBadRequest}, "PLAN_TOO_EXPENSIVE": {http.StatusTooManyRequests},
	"INVALID_CURSOR": {http.StatusBadRequest}, "STALE_CURSOR": {http.StatusConflict}, "DATASET_GENERATION_CHANGED": {http.StatusConflict}, "UNSUPPORTED_EXPORT_FORMAT": {http.StatusBadRequest}, "CLIENT_CANCELED": {499},
	"BACKEND_UNAVAILABLE": {http.StatusServiceUnavailable}, "DATASET_NOT_FOUND": {http.StatusNotFound}, "SCHEMA_CONFLICT": {http.StatusConflict}, "INTERNAL_ERROR": {http.StatusInternalServerError},
	"INVALID_RESOURCE_TYPE": {http.StatusBadRequest}, "INVALID_LIMIT": {http.StatusBadRequest}, "NO_ACTIVE_GENERATION": {http.StatusNotFound}, "RESOURCE_DECODE_FAILED": {http.StatusBadRequest}, "REFERENCE_NOT_RESOLVED": {http.StatusBadRequest},
	"QUERY_DEPTH_EXCEEDED": {http.StatusBadRequest}, "INVALID_REQUEST": {http.StatusBadRequest}, "INVALID_DATA": {http.StatusUnprocessableEntity}, "UNAUTHENTICATED": {http.StatusUnauthorized}, "FORBIDDEN": {http.StatusForbidden},
	"RECIPE_NOT_FOUND": {http.StatusNotFound}, "RECIPE_RESOLUTION_FAILED": {http.StatusUnprocessableEntity}, "RECIPE_EXECUTION_NOT_FOUND": {http.StatusNotFound}, "EXPORT_LIMIT_EXCEEDED": {http.StatusRequestEntityTooLarge},
	"INGEST_PREFLIGHT_FAILED": {http.StatusUnprocessableEntity}, "GENERATION_LOAD_INCOMPLETE": {http.StatusUnprocessableEntity}, "GENERATION_ACTIVATION_UNKNOWN": {http.StatusConflict}, "INVALID_GENERATION_FILE": {http.StatusBadRequest}, "DUPLICATE_GENERATION_FILE": {http.StatusBadRequest},
	"PUBLICATION_IN_PROGRESS": {http.StatusConflict}, "PUBLICATION_CONFLICT": {http.StatusConflict}, "PUBLICATION_LEASE_LOST": {http.StatusServiceUnavailable}, "PUBLICATION_FAILED": {http.StatusServiceUnavailable}, "OUTPUT_ENCODING_FAILED": {http.StatusInternalServerError},
	"DYNAMIC_SCHEMA_DRIFT": {http.StatusConflict}, "RECIPE_CONTRACT_VIOLATION": {http.StatusConflict}, "INVALID_SELECTOR": {http.StatusBadRequest}, "RECEIPT_STORE_UNAVAILABLE": {http.StatusServiceUnavailable}, "PREVIEW_TIMEOUT": {http.StatusGatewayTimeout},
	"PREVIEW_RESPONSE_TOO_LARGE": {http.StatusRequestEntityTooLarge}, "QUERY_MEMORY_LIMIT_EXCEEDED": {http.StatusServiceUnavailable}, "QUERY_RESOURCE_LIMIT_EXCEEDED": {http.StatusServiceUnavailable}, "QUERY_BACKEND_OUT_OF_MEMORY": {http.StatusServiceUnavailable},
	"NOT_FOUND": {http.StatusNotFound}, "METHOD_NOT_ALLOWED": {http.StatusMethodNotAllowed}, "PAYLOAD_TOO_LARGE": {http.StatusRequestEntityTooLarge}, "UNSUPPORTED_MEDIA_TYPE": {http.StatusUnsupportedMediaType}, "GRAPHQL_VALIDATION_FAILED": {http.StatusBadRequest},
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
