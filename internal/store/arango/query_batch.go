package arango

import (
	"context"
	"errors"
	"fmt"

	driver "github.com/arangodb/go-driver/v2/arangodb"
	"github.com/arangodb/go-driver/v2/arangodb/shared"
)

type batchQueryer interface {
	QueryBatch(context.Context, string, *driver.QueryOptions, any) (driver.CursorBatch, error)
}

// queryRowsByBatch keeps the driver on its streaming cursor path while decoding
// each result array once per batch instead of calling ReadDocument per row.
func queryRowsByBatch(ctx context.Context, queryer batchQueryer, query string, options *driver.QueryOptions, visit RowVisitor) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	options.Options.Stream = true
	var rows []map[string]any
	cursor, err := queryer.QueryBatch(ctx, query, options, &rows)
	if err != nil {
		return fmt.Errorf("arango query: %w", err)
	}
	defer func() {
		if err := cursor.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close arango query cursor: %w", err))
		}
	}()

	for {
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := visit(row); err != nil {
				return err
			}
		}
		rows = nil
		if err := ctx.Err(); err != nil {
			return err
		}
		if !cursor.HasMoreBatches() {
			return nil
		}
		if err := cursor.ReadNextBatch(ctx, &rows); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if shared.IsNoMoreDocuments(err) {
				return nil
			}
			return fmt.Errorf("read arango query cursor batch: %w", err)
		}
	}
}
