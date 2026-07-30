package graphqlapi

import (
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// PresentError converts a service error to the GraphQL error shape used by
// the frontend. The original error remains attached to gqlerror.Error.Err so
// server logging and errors.Is/errors.As retain their normal behavior.
func PresentError(err error, requestID string) *gqlerror.Error {
	if err == nil {
		return nil
	}
	userErr := dataframeerrors.Normalize(err)
	return &gqlerror.Error{
		Err:        err,
		Message:    dataframeerrors.PublicMessage(userErr),
		Extensions: ExtensionsForError(userErr, requestID),
	}
}

// GraphQLError is an error-returning convenience for resolver code.
func GraphQLError(err error, requestID string) error {
	return PresentError(err, requestID)
}

// ExtensionsForError returns a fresh map suitable for a GraphQL error
// response. It deliberately exposes only the stable semantic contract.
func ExtensionsForError(err error, requestID string) map[string]any {
	userErr := dataframeerrors.Normalize(err)
	if userErr == nil {
		return nil
	}
	extensions := map[string]any{
		"code":      userErr.Code(),
		"fieldPath": append([]string(nil), userErr.FieldPath()...),
		"retryable": userErr.Retryable(),
	}
	if details := userErr.Details(); len(details) > 0 {
		extensions["details"] = details
	}
	if requestID != "" {
		extensions["requestId"] = requestID
	}
	return extensions
}
