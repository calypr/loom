package dataframe

import (
	"context"
)

func (s *Service) runQuery(ctx context.Context, compiled CompiledQuery) (*Result, error) {
	rows := make([]map[string]any, 0, compiled.Limit)
	rowCount := 0
	columns := materializedColumns(compiled.Columns, compiled.PivotFields)
	seenColumns := make(map[string]struct{}, len(columns))
	for _, col := range columns {
		seenColumns[col] = struct{}{}
	}

	err := s.executeRows(ctx, ExecuteQueryOptions{
		ConnectionOptions: s.connOpts,
		BatchSize:         1000,
	}, compiled.Query, compiled.BindVars, func(row map[string]any) error {
		flatRow := flattenPivotFields(cloneRow(row), compiled.PivotFields)
		for key := range flatRow {
			if _, ok := seenColumns[key]; ok {
				continue
			}
			seenColumns[key] = struct{}{}
			columns = append(columns, key)
		}
		rows = append(rows, flatRow)
		rowCount++
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &Result{
		Columns:  columns,
		Rows:     rows,
		RowCount: rowCount,
	}, nil
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
		for key, item := range obj {
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
