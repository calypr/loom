package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"

	"golang.org/x/sync/errgroup"

	"github.com/calypr/loom/internal/dataframe/compiler"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

const maxSourcePageBytes = 64 << 20

type sourceRowEvaluator interface {
	EvaluateRow(map[string]any) (map[string]any, error)
}

// streamSourcePage evaluates supported terminal columns once per selected
// root, after their scoped source sets have each been acquired once. The
// existing query executor remains responsible for every DB interaction.
func (s OutputStream) streamSourcePage(ctx context.Context, visit func(map[string]any) error) error {
	if s.sourcePage == nil || s.sourceProgram == nil || s.rootPageRows < 1 {
		return fmt.Errorf("source page is incomplete")
	}
	after := ""
	for {
		keys := make([]string, 0, s.rootPageRows)
		binds := cloneBindVars(s.sourcePage.RootKeysBindVars)
		binds[compiler.RootPageAfterKeyBind] = after
		if err := s.stream(ctx, s.sourcePage.RootKeysQuery, s.batchSize, binds, func(row map[string]any) error {
			key, ok := row["_key"].(string)
			if !ok || key == "" || (len(keys) != 0 && key <= keys[len(keys)-1]) {
				return fmt.Errorf("source page root keys are missing or out of order")
			}
			keys = append(keys, key)
			return nil
		}); err != nil {
			return err
		}
		if len(keys) == 0 {
			return nil
		}
		if err := s.streamSourceRows(ctx, keys, visit); err != nil {
			return err
		}
		after = keys[len(keys)-1]
		if len(keys) < s.rootPageRows {
			return nil
		}
	}
}

func (s OutputStream) streamSourceRows(ctx context.Context, keys []string, visit func(map[string]any) error) error {
	sources := s.sourcePage.Sources
	if len(sources) == 0 || sources[0].Variable == "" {
		return fmt.Errorf("source page has no root source")
	}
	values := make([][]any, len(sources))
	var pageBytes atomic.Int64
	group, queryCtx := errgroup.WithContext(ctx)
	group.SetLimit(3)
	for index, source := range sources {
		group.Go(func() error {
			binds := cloneBindVars(source.BindVars)
			binds[compiler.RootPageKeysBind] = keys
			rows := make([]any, 0, len(keys))
			err := s.stream(queryCtx, source.Query, s.batchSize, binds, func(row map[string]any) error {
				if len(rows) >= len(keys) {
					return fmt.Errorf("source %d returned excess rows", index+1)
				}
				key, ok := row["_key"].(string)
				if !ok || key != keys[len(rows)] {
					return fmt.Errorf("source %d row identity/order mismatch at row %d", index+1, len(rows)+1)
				}
				encoded, err := json.Marshal(row)
				if err != nil {
					return fmt.Errorf("source %d returned an unencodable row: %w", index+1, err)
				}
				if current := pageBytes.Add(int64(len(encoded))); current > maxSourcePageBytes {
					return dataframeerrors.NewError(dataframeerrors.CodePreviewResponseTooLarge,
						"preview source page is too large; try fewer rows or exclude large repeated-value columns",
						dataframeerrors.WithDetails(map[string]any{
							"source_page_bytes":       current,
							"source_page_limit_bytes": maxSourcePageBytes,
							"source_page_roots":       len(keys),
						}),
					)
				}
				value, exists := row["value"]
				if !exists {
					return fmt.Errorf("source %d row is missing its value", index+1)
				}
				rows = append(rows, value)
				return nil
			})
			if err != nil {
				return err
			}
			if len(rows) != len(keys) {
				return fmt.Errorf("source %d returned %d of %d root rows", index+1, len(rows), len(keys))
			}
			values[index] = rows
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return err
	}
	for rowIndex, key := range keys {
		payload, ok := values[0][rowIndex].(map[string]any)
		if !ok {
			return fmt.Errorf("source root payload has the wrong shape at row %d", rowIndex+1)
		}
		variables := make(map[string]any, len(sources))
		variables[sources[0].Variable] = map[string]any{"_key": key, "payload": payload}
		for index := 1; index < len(sources); index++ {
			set, ok := values[index][rowIndex].([]any)
			if !ok {
				return fmt.Errorf("source set %d has the wrong shape at row %d", index, rowIndex+1)
			}
			variables[sources[index].Variable] = set
		}
		row, err := s.sourceProgram.EvaluateRow(variables)
		if err != nil {
			return fmt.Errorf("evaluate source row %d: %w", rowIndex+1, err)
		}
		if err := ensureStableRowIdentity(row, s.RowIdentity, s.bindVars); err != nil {
			return fmt.Errorf("source row %d: %w", rowIndex+1, err)
		}
		if err := visit(row); err != nil {
			return err
		}
	}
	return nil
}
