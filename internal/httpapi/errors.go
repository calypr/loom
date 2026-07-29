package httpapi

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
// errors are always redacted to INTERNAL_ERROR.

// IsMappedError lets a coordinator preserve an already mapped error while
// adding request logging.
