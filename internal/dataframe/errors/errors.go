package dataframeerrors

import (
	"context"
	"errors"
	"reflect"
	"strings"
)

// ErrorCode is the stable, transport-neutral identifier for a dataframe
// request failure. Clients should branch on this value, not on Error().
type ErrorCode string

const (
	CodeProjectRequired             ErrorCode = "PROJECT_REQUIRED"
	CodeRootResourceTypeRequired    ErrorCode = "ROOT_RESOURCE_TYPE_REQUIRED"
	CodeUnauthorizedProject         ErrorCode = "UNAUTHORIZED_PROJECT"
	CodeUnknownField                ErrorCode = "UNKNOWN_FIELD"
	CodeFieldNotPopulated           ErrorCode = "FIELD_NOT_POPULATED"
	CodeInvalidTraversal            ErrorCode = "INVALID_TRAVERSAL"
	CodeUnsafeTraversalRoute        ErrorCode = "UNSAFE_TRAVERSAL_ROUTE"
	CodeInvalidFilter               ErrorCode = "INVALID_FILTER"
	CodeUnboundedPivot              ErrorCode = "UNBOUNDED_PIVOT"
	CodeInvalidPivotColumn          ErrorCode = "INVALID_PIVOT_COLUMN"
	CodeInvalidSlice                ErrorCode = "INVALID_SLICE"
	CodePlanTooExpensive            ErrorCode = "PLAN_TOO_EXPENSIVE"
	CodeInvalidCursor               ErrorCode = "INVALID_CURSOR"
	CodeStaleCursor                 ErrorCode = "STALE_CURSOR"
	CodeDatasetGenerationChanged    ErrorCode = "DATASET_GENERATION_CHANGED"
	CodeUnsupportedExportFormat     ErrorCode = "UNSUPPORTED_EXPORT_FORMAT"
	CodeClientCanceled              ErrorCode = "CLIENT_CANCELED"
	CodeBackendUnavailable          ErrorCode = "BACKEND_UNAVAILABLE"
	CodeDatasetNotFound             ErrorCode = "DATASET_NOT_FOUND"
	CodeSchemaConflict              ErrorCode = "SCHEMA_CONFLICT"
	CodeInternalError               ErrorCode = "INTERNAL_ERROR"
	CodeInvalidResourceType         ErrorCode = "INVALID_RESOURCE_TYPE"
	CodeInvalidLimit                ErrorCode = "INVALID_LIMIT"
	CodeNoActiveGeneration          ErrorCode = "NO_ACTIVE_GENERATION"
	CodeResourceDecodeFailed        ErrorCode = "RESOURCE_DECODE_FAILED"
	CodeReferenceNotResolved        ErrorCode = "REFERENCE_NOT_RESOLVED"
	CodeQueryDepthExceeded          ErrorCode = "QUERY_DEPTH_EXCEEDED"
	CodeInvalidRequest              ErrorCode = "INVALID_REQUEST"
	CodeInvalidData                 ErrorCode = "INVALID_DATA"
	CodeUnauthenticated             ErrorCode = "UNAUTHENTICATED"
	CodeForbidden                   ErrorCode = "FORBIDDEN"
	CodeRecipeNotFound              ErrorCode = "RECIPE_NOT_FOUND"
	CodeRecipeExecutionNotFound     ErrorCode = "RECIPE_EXECUTION_NOT_FOUND"
	CodeExportLimitExceeded         ErrorCode = "EXPORT_LIMIT_EXCEEDED"
	CodeIngestPreflightFailed       ErrorCode = "INGEST_PREFLIGHT_FAILED"
	CodeGenerationLoadIncomplete    ErrorCode = "GENERATION_LOAD_INCOMPLETE"
	CodeGenerationActivationUnknown ErrorCode = "GENERATION_ACTIVATION_UNKNOWN"
	CodeInvalidGenerationFile       ErrorCode = "INVALID_GENERATION_FILE"
	CodeDuplicateGenerationFile     ErrorCode = "DUPLICATE_GENERATION_FILE"
	CodePublicationInProgress       ErrorCode = "PUBLICATION_IN_PROGRESS"
	CodePublicationConflict         ErrorCode = "PUBLICATION_CONFLICT"
	CodePublicationLeaseLost        ErrorCode = "PUBLICATION_LEASE_LOST"
	CodeOutputEncodingFailed        ErrorCode = "OUTPUT_ENCODING_FAILED"
)

var AllErrorCodes = []ErrorCode{
	CodeProjectRequired, CodeRootResourceTypeRequired, CodeUnauthorizedProject,
	CodeUnknownField, CodeFieldNotPopulated, CodeInvalidTraversal, CodeUnsafeTraversalRoute,
	CodeInvalidFilter, CodeUnboundedPivot, CodeInvalidPivotColumn, CodeInvalidSlice,
	CodePlanTooExpensive, CodeInvalidCursor, CodeStaleCursor, CodeDatasetGenerationChanged,
	CodeUnsupportedExportFormat, CodeClientCanceled, CodeBackendUnavailable, CodeDatasetNotFound,
	CodeSchemaConflict, CodeInternalError, CodeInvalidResourceType, CodeInvalidLimit,
	CodeNoActiveGeneration, CodeResourceDecodeFailed, CodeReferenceNotResolved, CodeQueryDepthExceeded,
	CodeInvalidRequest, CodeInvalidData, CodeUnauthenticated, CodeForbidden, CodeRecipeNotFound,
	CodeRecipeExecutionNotFound, CodeExportLimitExceeded, CodeIngestPreflightFailed,
	CodeGenerationLoadIncomplete, CodeGenerationActivationUnknown, CodeInvalidGenerationFile,
	CodeDuplicateGenerationFile, CodePublicationInProgress, CodePublicationConflict,
	CodePublicationLeaseLost, CodeOutputEncodingFailed,
}

// UserError is the semantic error contract shared by GraphQL, preview, and
// export adapters. Details are intentionally a safe, copied view.
type UserError interface {
	error
	Code() string
	FieldPath() []string
	Details() map[string]any
	Retryable() bool
}

// Error is Loom's typed semantic error. The cause remains available to
// server-side logs and errors.Is/errors.As callers, but is never exposed by
// the transport mappers unless it is itself a safe UserError.
type Error struct {
	code      ErrorCode
	message   string
	fieldPath []string
	details   map[string]any
	retryable bool
	cause     error
}

type errorOptions struct {
	fieldPath []string
	details   map[string]any
	retryable bool
	cause     error
}

// ErrorOption configures a typed dataframe Error.
type ErrorOption func(*errorOptions)

func WithFieldPath(path ...string) ErrorOption {
	return func(opts *errorOptions) { opts.fieldPath = append([]string(nil), path...) }
}

func WithDetails(details map[string]any) ErrorOption {
	return func(opts *errorOptions) { opts.details = cloneSafeDetails(details) }
}

func WithRetryable(retryable bool) ErrorOption {
	return func(opts *errorOptions) { opts.retryable = retryable }
}

func WithCause(cause error) ErrorOption {
	return func(opts *errorOptions) { opts.cause = cause }
}

func NewError(code ErrorCode, message string, options ...ErrorOption) *Error {
	opts := errorOptions{}
	for _, option := range options {
		if option != nil {
			option(&opts)
		}
	}
	if message == "" {
		message = defaultMessage(code)
	}
	return &Error{
		code:      code,
		message:   message,
		fieldPath: append([]string(nil), opts.fieldPath...),
		details:   cloneSafeDetails(opts.details),
		retryable: opts.retryable,
		cause:     opts.cause,
	}
}

func Wrap(cause error, code ErrorCode, message string, options ...ErrorOption) *Error {
	options = append(options, WithCause(cause))
	return NewError(code, message, options...)
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *Error) Code() string {
	if e == nil {
		return string(CodeInternalError)
	}
	return string(e.code)
}

func (e *Error) FieldPath() []string {
	if e == nil {
		return nil
	}
	return append([]string(nil), e.fieldPath...)
}

func (e *Error) Details() map[string]any {
	if e == nil {
		return nil
	}
	return cloneSafeDetails(e.details)
}

func (e *Error) Retryable() bool {
	return e != nil && e.retryable
}

// These sentinels let semantic owners wrap backend failures without making
// the GraphQL or HTTP layer inspect driver-specific errors.
var (
	ErrBackendUnavailable = errors.New("dataframe backend unavailable")
	ErrClientCanceled     = errors.New("dataframe client canceled")
)

// AsUserError extracts a typed semantic error through arbitrary wrapping.
func AsUserError(err error) (UserError, bool) {
	if err == nil {
		return nil, false
	}
	var userErr UserError
	if errors.As(err, &userErr) {
		return userErr, true
	}
	return nil, false
}

// Normalize converts only well-defined transport-neutral conditions. Unknown
// errors intentionally become INTERNAL_ERROR; no English error text is parsed.
func Normalize(err error) UserError {
	if err == nil {
		return nil
	}
	if userErr, ok := AsUserError(err); ok {
		// Preserve our concrete type without adding an unnecessary wrapper. An
		// adapter-owned implementation is copied through Error so its details
		// receive the same redaction and defensive-copy rules.
		if typed, ok := userErr.(*Error); ok {
			return typed
		}
		code := ErrorCode(userErr.Code())
		return Wrap(err, code, defaultMessage(code), WithFieldPath(userErr.FieldPath()...), WithDetails(userErr.Details()), WithRetryable(userErr.Retryable()))
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, ErrClientCanceled):
		return Wrap(err, CodeClientCanceled, defaultMessage(CodeClientCanceled))
	case errors.Is(err, context.DeadlineExceeded):
		return Wrap(err, CodeBackendUnavailable, defaultMessage(CodeBackendUnavailable), WithRetryable(true))
	case errors.Is(err, ErrBackendUnavailable):
		return Wrap(err, CodeBackendUnavailable, defaultMessage(CodeBackendUnavailable), WithRetryable(true))
	default:
		return Wrap(err, CodeInternalError, defaultMessage(CodeInternalError))
	}
}

func PublicMessage(err error) string {
	if err == nil {
		return ""
	}
	userErr := Normalize(err)
	if userErr == nil {
		return "internal server error"
	}
	return defaultMessage(ErrorCode(userErr.Code()))
}

// SanitizePersistedFailure converts an opaque durable failure string into the
// stable public contract. Durable records intentionally retain only a legacy
// text field, so unknown text is always treated as an internal failure and is
// never returned to clients.
func SanitizePersistedFailure(raw string) (message, code string, retryable bool) {
	if strings.TrimSpace(raw) == "" {
		return "", "", false
	}
	normalized := Normalize(errors.New(raw))
	return PublicMessage(normalized), normalized.Code(), normalized.Retryable()
}

func IsUserCorrectable(code ErrorCode) bool {
	switch code {
	case CodeProjectRequired, CodeRootResourceTypeRequired, CodeUnauthorizedProject,
		CodeUnknownField, CodeFieldNotPopulated, CodeInvalidTraversal, CodeUnsafeTraversalRoute,
		CodeInvalidFilter, CodeUnboundedPivot, CodeInvalidPivotColumn, CodeInvalidSlice,
		CodePlanTooExpensive, CodeInvalidCursor, CodeStaleCursor, CodeUnsupportedExportFormat,
		CodeInvalidResourceType, CodeInvalidLimit, CodeInvalidRequest, CodeInvalidData,
		CodeRecipeNotFound, CodeRecipeExecutionNotFound, CodeExportLimitExceeded,
		CodeIngestPreflightFailed, CodeGenerationLoadIncomplete, CodeInvalidGenerationFile,
		CodeDuplicateGenerationFile:
		return true
	default:
		return false
	}
}

func IsRetryableCode(code ErrorCode) bool {
	return code == CodeBackendUnavailable || code == CodePublicationInProgress || code == CodePublicationConflict || code == CodePublicationLeaseLost
}

func IsOperatorFailure(code ErrorCode) bool {
	return code == CodeBackendUnavailable || code == CodeInternalError || code == CodePublicationLeaseLost
}

func defaultMessage(code ErrorCode) string {
	switch code {
	case CodeProjectRequired:
		return "project is required"
	case CodeRootResourceTypeRequired:
		return "root resource type is required"
	case CodeUnauthorizedProject:
		return "the requested project is not available"
	case CodeUnknownField:
		return "the selected field is not recognized"
	case CodeFieldNotPopulated:
		return "the selected field is not populated in this dataset"
	case CodeInvalidTraversal:
		return "the requested traversal is invalid"
	case CodeUnsafeTraversalRoute:
		return "the requested traversal route is not supported"
	case CodeInvalidFilter:
		return "the requested filter is invalid"
	case CodeUnboundedPivot:
		return "the pivot must use a bounded set of columns"
	case CodeInvalidPivotColumn:
		return "the selected pivot column is invalid"
	case CodeInvalidSlice:
		return "the requested representative slice is invalid"
	case CodePlanTooExpensive:
		return "the dataframe request exceeds the preview cost limit"
	case CodeInvalidCursor:
		return "the preview cursor is invalid"
	case CodeStaleCursor:
		return "the preview cursor is no longer valid"
	case CodeDatasetGenerationChanged:
		return "the dataset changed while the request was being prepared"
	case CodeUnsupportedExportFormat:
		return "the requested export format is not supported"
	case CodeClientCanceled:
		return "the request was canceled"
	case CodeInvalidResourceType:
		return "the requested resource type is invalid"
	case CodeInvalidLimit:
		return "the requested limit is invalid"
	case CodeNoActiveGeneration:
		return "the project has no active dataset generation"
	case CodeResourceDecodeFailed:
		return "the resource could not be decoded"
	case CodeReferenceNotResolved:
		return "the requested reference could not be resolved"
	case CodeQueryDepthExceeded:
		return "the reference query depth limit was exceeded"
	case CodeBackendUnavailable:
		return "the dataframe backend is temporarily unavailable"
	case CodeDatasetNotFound:
		return "the requested dataset was not found"
	case CodeSchemaConflict:
		return "the published dataset sources have incompatible schemas"
	case CodeInvalidRequest:
		return "the request is invalid"
	case CodeInvalidData:
		return "the submitted data is invalid"
	case CodeUnauthenticated:
		return "authentication is required"
	case CodeForbidden:
		return "the caller is not permitted to perform this operation"
	case CodeRecipeNotFound:
		return "the requested recipe was not found"
	case CodeRecipeExecutionNotFound:
		return "the requested recipe execution was not found"
	case CodeExportLimitExceeded:
		return "the export exceeds the configured limit"
	case CodeIngestPreflightFailed:
		return "ingestion preflight failed"
	case CodeGenerationLoadIncomplete:
		return "dataset generation loading was incomplete"
	case CodeGenerationActivationUnknown:
		return "dataset generation activation status is unknown"
	case CodeInvalidGenerationFile:
		return "a generation file name is invalid"
	case CodeDuplicateGenerationFile:
		return "a generation file name is duplicated"
	case CodePublicationInProgress:
		return "an identical publication is already in progress"
	case CodePublicationConflict:
		return "the publication changed while it was being committed"
	case CodePublicationLeaseLost:
		return "publication ownership was lost"
	case CodeOutputEncodingFailed:
		return "the response data could not be encoded"
	default:
		return "internal server error"
	}
}

func cloneSafeDetails(details map[string]any) map[string]any {
	if len(details) == 0 {
		return nil
	}
	result := make(map[string]any, len(details))
	for key, value := range details {
		if sensitiveDetailKey(key) {
			continue
		}
		if safe, ok := cloneSafeValue(value); ok {
			result[key] = safe
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func sensitiveDetailKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, token := range []string{"aql", "bind", "credential", "secret", "token", "password", "collection", "filesystem", "stack"} {
		if strings.Contains(key, token) {
			return true
		}
	}
	return key == "path" || key == "query" || key == "raw" || strings.Contains(key, "path")
}

func cloneSafeValue(value any) (any, bool) {
	if value == nil {
		return nil, true
	}
	switch typed := value.(type) {
	case string, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return typed, true
	case []string:
		return append([]string(nil), typed...), true
	case []any:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			safe, ok := cloneSafeValue(item)
			if !ok {
				return nil, false
			}
			result = append(result, safe)
		}
		return result, true
	case map[string]any:
		return cloneSafeDetails(typed), true
	default:
		// Accept only primitive aliases, not arbitrary structs such as Arango
		// driver errors or query objects.
		rv := reflect.ValueOf(value)
		if rv.IsValid() && rv.Kind() == reflect.String {
			return rv.String(), true
		}
		return nil, false
	}
}

// Ensure the concrete type continues to satisfy the public contract.
var _ UserError = (*Error)(nil)
