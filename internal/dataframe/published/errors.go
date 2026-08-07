package published

import (
	"context"
	"errors"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

// backendCallError classifies errors returned by ClickHouse/catalog calls.
// Typed request errors and cancellation remain intact; an untyped dependency
// failure is retryable and keeps its private cause for server-side logging.
func backendCallError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := dataframeerrors.AsUserError(err); ok {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
}
