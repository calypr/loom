package runtime

import (
	"context"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

type ExecuteQueryOptions struct {
	BatchSize int
}

func ExecuteQueryRows(ctx context.Context, opts ExecuteQueryOptions, query string, bindVars map[string]any, visit func(map[string]any) error) error {
	return dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
}
