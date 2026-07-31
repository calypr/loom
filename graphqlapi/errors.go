package graphqlapi

import (
	"errors"
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
	result := &gqlerror.Error{
		Err:        err,
		Message:    dataframeerrors.PublicMessage(userErr),
		Extensions: ExtensionsForError(userErr, requestID),
	}
	var original *gqlerror.Error
	if errors.As(err, &original) && original != nil {
		result.Path, result.Locations = original.Path, original.Locations
	}
	return result
}

func PresentGraphQLError(err error, requestID string) *gqlerror.Error {
	var gqlErr *gqlerror.Error
	if errors.As(err, &gqlErr) && gqlErr != nil {
		// Parser and validation errors are already safe and actionable. Preserve
		// their path/location while giving clients one stable non-retryable code.
		if gqlErr.Err == nil {
			copy := *gqlErr
			copy.Extensions = map[string]any{"code": "GRAPHQL_VALIDATION_FAILED", "retryable": false}
			if requestID != "" {
				copy.Extensions["requestId"] = requestID
			}
			return &copy
		}
		presented := PresentError(gqlErr.Err, requestID)
		presented.Path, presented.Locations = gqlErr.Path, gqlErr.Locations
		return presented
	}
	return PresentError(err, requestID)
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
