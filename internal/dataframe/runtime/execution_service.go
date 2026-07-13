package runtime

import (
	"context"
	"fmt"
	"sort"
	"time"
)

func (s *Service) runQuery(ctx context.Context, compiled CompiledQuery) (*Result, error) {
	rows := make([]map[string]any, 0, compiled.Limit)
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

// Stream compiles the same catalog- and authorization-validated request used
// by Run, but delivers flattened rows as Arango yields them instead of
// retaining the complete dataframe in Loom memory. Each invocation receives a
// distinct top-level row map.
func (s *Service) Stream(ctx context.Context, req RunRequest, visit func(map[string]any) error) (StreamResult, error) {
	if visit == nil {
		return StreamResult{}, fmt.Errorf("row visitor is required")
	}
	started := time.Now()
	compiled, diagnostics, err := s.compileRunRequestWithDiagnostics(ctx, req)
	if err != nil {
		return StreamResult{}, err
	}
	result, err := s.streamQuery(ctx, compiled, visit)
	if err != nil {
		return result, err
	}
	diagnostics.ArangoQuery = result.Diagnostics.ArangoQuery
	diagnostics.RowMaterialization = result.Diagnostics.RowMaterialization
	diagnostics.ResultAssembly = result.Diagnostics.ResultAssembly
	diagnostics.Total = time.Since(started)
	result.Diagnostics = diagnostics
	return result, nil
}

func (s *Service) streamQuery(ctx context.Context, compiled CompiledQuery, visit func(map[string]any) error) (StreamResult, error) {
	if visit == nil {
		return StreamResult{}, fmt.Errorf("row visitor is required")
	}
	columns := materializedColumns(compiled.Columns, compiled.PivotFields)
	seenColumns := make(map[string]struct{}, len(columns))
	for _, col := range columns {
		seenColumns[col] = struct{}{}
	}

	extraColumns := map[string]struct{}{}
	rowCount := 0
	var rowMaterialization time.Duration
	queryStarted := time.Now()
	err := s.executeRows(ctx, ExecuteQueryOptions{
		ConnectionOptions: s.connOpts,
		BatchSize:         1000,
	}, compiled.Query, compiled.BindVars, func(row map[string]any) error {
		rowStarted := time.Now()
		defer func() { rowMaterialization += time.Since(rowStarted) }()
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
	queryElapsed := time.Since(queryStarted)
	arangoQuery := queryElapsed - rowMaterialization
	if arangoQuery < 0 {
		arangoQuery = 0
	}
	assemblyStarted := time.Now()
	newColumns := make([]string, 0, len(extraColumns))
	for column := range extraColumns {
		newColumns = append(newColumns, column)
	}
	sort.Strings(newColumns)
	columns = append(columns, newColumns...)
	result := StreamResult{
		Columns:  columns,
		RowCount: rowCount,
		Diagnostics: QueryDiagnostics{
			ArangoQuery:        arangoQuery,
			RowMaterialization: rowMaterialization,
			ResultAssembly:     time.Since(assemblyStarted),
		},
	}
	if err != nil {
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
