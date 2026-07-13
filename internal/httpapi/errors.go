package httpapi

import (
	"errors"
	"net/http"

	"github.com/calypr/loom/internal/dataframe"
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

func (e MappedError) Error() string {
	return e.Body.Error.Message
}

func (e MappedError) Unwrap() error { return e.Cause }

// MapDataframeError maps a service error without inspecting its text. Unknown
// errors are always redacted to INTERNAL_ERROR.
func MapDataframeError(err error, requestID string) MappedError {
	userErr := dataframe.Normalize(err)
	if userErr == nil {
		return MappedError{}
	}
	code := userErr.Code()
	return MappedError{
		Status: statusForDataframeCode(dataframe.ErrorCode(code)),
		Body: ErrorResponse{Error: HTTPErrorBody{
			Code:      code,
			Message:   dataframe.PublicMessage(userErr),
			FieldPath: append([]string(nil), userErr.FieldPath()...),
			Details:   userErr.Details(),
			Retryable: userErr.Retryable(),
			RequestID: requestID,
		}},
		Cause: err,
	}
}

func statusForDataframeCode(code dataframe.ErrorCode) int {
	switch code {
	case dataframe.CodeProjectRequired,
		dataframe.CodeRootResourceTypeRequired,
		dataframe.CodeUnknownField,
		dataframe.CodeFieldNotPopulated,
		dataframe.CodeInvalidTraversal,
		dataframe.CodeUnsafeTraversalRoute,
		dataframe.CodeInvalidFilter,
		dataframe.CodeUnboundedPivot,
		dataframe.CodeInvalidPivotColumn,
		dataframe.CodeInvalidSlice,
		dataframe.CodeInvalidCursor,
		dataframe.CodeStaleCursor,
		dataframe.CodeUnsupportedExportFormat:
		return http.StatusBadRequest
	case dataframe.CodeUnauthorizedProject:
		return http.StatusForbidden
	case dataframe.CodeClientCanceled:
		return 499 // Client Closed Request, used by Fiber-compatible adapters.
	case dataframe.CodeDatasetGenerationChanged:
		return http.StatusConflict
	case dataframe.CodePlanTooExpensive:
		return http.StatusUnprocessableEntity
	case dataframe.CodeBackendUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// IsMappedError lets a coordinator preserve an already mapped error while
// adding request logging.
func IsMappedError(err error) bool {
	var mapped MappedError
	return errors.As(err, &mapped)
}
