package runtime

import (
	"context"

	arangostore "github.com/calypr/loom/internal/store/arango"
)

type ExecuteQueryOptions struct {
	arangostore.ConnectionOptions
	BatchSize int
}

func ExecuteQueryRows(ctx context.Context, opts ExecuteQueryOptions, query string, bindVars map[string]any, visit func(map[string]any) error) error {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 1000
	}
	client, err := arangostore.Open(ctx, opts.URL, opts.Database)
	if err != nil {
		return err
	}
	defer client.Close(ctx)
	return client.QueryRows(ctx, query, opts.BatchSize, bindVars, func(row map[string]any) error {
		return visit(row)
	})
}
