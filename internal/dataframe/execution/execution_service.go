package execution

import (
	"context"
	"fmt"
	"github.com/calypr/loom/internal/dataframe/compiler"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"sort"
	"time"
)

var now = time.Now

func (s *Service) runQuery(ctx context.Context, compiled compiler.CompiledQuery) (*Result, error) {
	var rows []map[string]any
	streamed, err := s.streamQuery(ctx, compiled, func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &Result{
		Columns:     streamed.Columns,
		Rows:        rows,
		RowCount:    streamed.RowCount,
		Diagnostics: streamed.Diagnostics,
	}, nil
}

func (s *Service) RunCompiled(ctx context.Context, compiled compiler.CompiledQuery) (*Result, error) {
	started := time.Now()
	result, err := s.runQuery(ctx, compiled)
	if err != nil {
		return nil, err
	}
	result.Diagnostics.Plan = compiled.PlanDiagnostics
	result.Diagnostics.Total = time.Since(started)
	return result, nil
}

func (s *Service) streamQuery(ctx context.Context, compiled compiler.CompiledQuery, visit func(map[string]any) error) (streamResult, error) {
	if visit == nil {
		return streamResult{}, fmt.Errorf("row visitor is required")
	}
	if err := contextError(ctx); err != nil {
		return streamResult{}, err
	}
	columns := materializedColumns(compiled.Columns, compiled.PivotFields)
	seenColumns := make(map[string]struct{}, len(columns))
	for _, col := range columns {
		seenColumns[col] = struct{}{}
	}

	extraColumns := map[string]struct{}{}
	rowCount := 0
	var rowMaterialization time.Duration
	queryStarted := now()
	if s.sourceRows == nil {
		return streamResult{}, dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	err := s.sourceRows(ctx, RowsRequest{Command: compiled.Command, DeclaredColumns: append([]string(nil), compiled.Columns...), BatchSize: 1000}, func(row map[string]any) error {
		if err := contextError(ctx); err != nil {
			return err
		}
		rowStarted := now()
		defer func() { rowMaterialization += now().Sub(rowStarted) }()
		flatRow := flattenPivotFields(cloneRow(row), compiled.PivotFields)
		for key := range flatRow {
			if _, ok := seenColumns[key]; ok {
				continue
			}
			seenColumns[key] = struct{}{}
			extraColumns[key] = struct{}{}
		}
		if err := visit(flatRow); err != nil {
			return err
		}
		rowCount++
		return nil
	})
	sourceQuery := now().Sub(queryStarted) - rowMaterialization
	if sourceQuery < 0 {
		sourceQuery = 0
	}
	assemblyStarted := time.Now()
	newColumns := make([]string, 0, len(extraColumns))
	for column := range extraColumns {
		newColumns = append(newColumns, column)
	}
	sort.Strings(newColumns)
	columns = append(columns, newColumns...)
	result := streamResult{
		Columns:  columns,
		RowCount: rowCount,
		Diagnostics: QueryDiagnostics{
			SourceQuery:        sourceQuery,
			SourceBackend:      compiled.Command.Backend(),
			RowMaterialization: rowMaterialization,
			ResultAssembly:     time.Since(assemblyStarted),
		},
	}
	if err != nil {
		return result, err
	}
	if err := contextError(ctx); err != nil {
		return result, err
	}
	return result, nil
}

func materializedColumns(columns []string, pivotFields []string) []string {
	if len(columns) == 0 {
		return []string{}
	}
	skip := make(map[string]struct{}, len(pivotFields))
	for _, field := range pivotFields {
		skip[field] = struct{}{}
	}
	out := make([]string, 0, len(columns))
	for _, col := range columns {
		if _, ok := skip[col]; ok {
			continue
		}
		out = append(out, col)
	}
	return out
}

func flattenPivotFields(row map[string]any, pivotFields []string) map[string]any {
	for _, field := range pivotFields {
		value, ok := row[field]
		if !ok {
			continue
		}
		obj, ok := value.(map[string]any)
		if !ok {
			continue
		}
		delete(row, field)
		keys := make([]string, 0, len(obj))
		for key := range obj {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			item := obj[key]
			row[sanitizeColumnName(field+"__"+key)] = item
		}
	}
	return row
}

func cloneRow(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
