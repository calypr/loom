package authpolicy

import (
	"context"
	"errors"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

type Operation string

const (
	OperationQueryRead           Operation = "query_read"
	OperationDataframeRead       Operation = "dataframe_read"
	OperationRecipeControl       Operation = "recipe_control"
	OperationRecipeAuthorization Operation = "recipe_authorization"
	OperationConcealedLookup     Operation = "concealed_lookup"
)

func Classify(err error, operation Operation) error {
	if err == nil {
		return nil
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
