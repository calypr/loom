package authpolicy

import (
	"context"
	"errors"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

type Operation string

const (
	OperationQueryRead             Operation = "query_read"
	OperationDataframeRead         Operation = "dataframe_read"
	OperationRecipeControl         Operation = "recipe_control"
	OperationRecipeAuthorization   Operation = "recipe_authorization"
	OperationConcealedLookup       Operation = "concealed_lookup"
	OperationExplorerAuthorization Operation = "explorer_authorization"
)

func Classify(err error, operation Operation) error {
	if err == nil {
		return nil
	}
	if operation == OperationExplorerAuthorization {
		return classifyExplorerAuthorization(err)
	}
	if _, ok := dataframeerrors.AsUserError(err); ok {
		return err
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, dataframeerrors.ErrClientCanceled):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeClientCanceled, "")
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, dataframeerrors.ErrBackendUnavailable), errors.Is(err, authscope.ErrAuthorizationBackendUnavailable):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	case errors.Is(err, authscope.ErrUnauthenticated):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeUnauthenticated, "")
	case errors.Is(err, authscope.ErrForbidden):
		return dataframeerrors.Wrap(err, denialCode(operation), "")
	default:
		return err
	}
}

func classifyExplorerAuthorization(err error) error {
	if err == nil {
		return nil
	}
	forbidden := func() error { return dataframeerrors.Wrap(err, dataframeerrors.CodeForbidden, "forbidden") }
	unavailable := func() error {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "authorization backend unavailable", dataframeerrors.WithRetryable(true))
	}
	if errors.Is(err, authscope.ErrForbidden) || errors.Is(err, authscope.ErrUnauthenticated) {
		return forbidden()
	}
	if errors.Is(err, authscope.ErrAuthorizationBackendUnavailable) || errors.Is(err, dataframeerrors.ErrBackendUnavailable) || errors.Is(err, context.DeadlineExceeded) {
		return unavailable()
	}
	var value shared.ArangoError
	if errors.As(err, &value) {
		if arangoPermissionResponse(value) {
			return forbidden()
		}
		return unavailable()
	}
	var pointer *shared.ArangoError
	if errors.As(err, &pointer) && pointer != nil {
		if arangoPermissionResponse(*pointer) {
			return forbidden()
		}
		return unavailable()
	}
	return forbidden()
}

func arangoPermissionResponse(value shared.ArangoError) bool {
	return value.Code == shared.ErrHttpUnauthorized || value.Code == shared.ErrHttpForbidden || value.ErrorNum == shared.ErrForbidden
}

func denialCode(operation Operation) dataframeerrors.ErrorCode {
	switch operation {
	case OperationRecipeControl, OperationRecipeAuthorization:
		return dataframeerrors.CodeUnauthorizedProject
	case OperationConcealedLookup:
		return dataframeerrors.CodeRecipeExecutionNotFound
	default:
		return dataframeerrors.CodeForbidden
	}
}
