package queryapi

import (
	"context"
	"errors"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

// classifyError is the one transport-neutral classification boundary for
// query services. Drivers and Fence errors are kept as causes, while public
// callers receive a stable code and never a driver message.
func classifyError(err error) error {
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
		return dataframeerrors.Wrap(err, dataframeerrors.CodeForbidden, "")
	default:
		return err
	}
}

func queryInvalid(code dataframeerrors.ErrorCode, err error) error {
	if err == nil {
		return dataframeerrors.NewError(code, "")
	}
	if _, ok := dataframeerrors.AsUserError(err); ok {
		return err
	}
	return dataframeerrors.Wrap(err, code, "")
}

func queryBackend(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := dataframeerrors.AsUserError(err); ok {
		return classifyError(err)
	}
	if errors.Is(err, authscope.ErrUnauthenticated) || errors.Is(err, authscope.ErrForbidden) ||
		errors.Is(err, authscope.ErrAuthorizationBackendUnavailable) || errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return classifyError(err)
	}
	return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
}

func queryInvalidErrorOrBackend(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := dataframeerrors.AsUserError(err); ok {
		return classifyError(err)
	}
	var validation *recipe.ValidationError
	if errors.As(err, &validation) {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidRequest, "", dataframeerrors.WithFieldPath(validation.Path), dataframeerrors.WithDetails(map[string]any{"validationCode": validation.Code}))
	}
	return queryBackend(err)
}
