package queryapi

import (
	"errors"

	"github.com/calypr/loom/internal/api/authpolicy"
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
	return authpolicy.Classify(err, authpolicy.OperationQueryRead)
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
	classified := classifyError(err)
	if _, ok := dataframeerrors.AsUserError(classified); ok {
		return classified
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
