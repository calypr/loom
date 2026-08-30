package published

import (
	"context"
	"fmt"
	"strings"
)

// PageRequest describes a bounded keyset page over one published table.
// AuthResourcePaths is the already-resolved effective read scope. An
// unrestricted request deliberately omits the auth_resource_path predicate.
type PageRequest struct {
	Columns           []string
	Filters           []Filter
	Sort              *Sort
	First             int
	After             string
	AuthResourcePaths []string
	Unrestricted      bool
}

// StreamRequest describes a bounded-memory scan over one published table.
type StreamRequest struct {
	Columns           []string
	Filters           []Filter
	Sort              *Sort
	AuthResourcePaths []string
	Unrestricted      bool
}

// Page reads one active/queryable materialization. It never constructs a
// cross-project UNION and therefore carries only one table's cursor state.
func (r *Reader) Page(ctx context.Context, materialization Materialization, req PageRequest) (Page, error) {
	if r == nil || r.ClickHouse == nil {
		return Page{}, backendUnavailable()
	}
	columns, allowed, err := readerColumns(materialization.Columns, req.Columns, req.Sort)
	if err != nil {
		return Page{}, err
	}
	first := req.First
	if first <= 0 {
		first = 100
	}
	if r.MaxPage > 0 && first > r.MaxPage {
		first = r.MaxPage
	}
	cursor, err := decodeCursor(req.After)
	if err != nil {
		return Page{}, err
	}
	queryColumns := append([]string(nil), columns...)
	if req.Sort != nil && !contains(queryColumns, req.Sort.Column) {
		queryColumns = append(queryColumns, req.Sort.Column)
	}
	queryColumns = append(queryColumns, "__loom_row_id")
	selectColumns := quotedColumns(queryColumns) + ", count() OVER() AS `__loom_total`"
	readColumns := append(append([]string(nil), queryColumns...), "__loom_total")
	query := fmt.Sprintf("SELECT %s FROM `%s`", selectColumns, materialization.PhysicalTable)
	args := make([]any, 0)
	where := make([]string, 0, len(req.Filters)+1)
	if !req.Unrestricted {
		if hasColumn(materialization.Columns, authResourcePathColumn) {
			where = append(where, "`auth_resource_path` IN ?")
			args = append(args, req.AuthResourcePaths)
		} else {
			where = append(where, "0")
		}
	}
	filterWhere, filterArgs, err := buildWhere(req.Filters, allowed)
	if err != nil {
		return Page{}, err
	}
	where = append(where, filterWhere...)
	args = append(args, filterArgs...)
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	if cursor != nil {
		cursorWhere, cursorArgs, err := pageCursorPredicate(cursor, req.Sort)
		if err != nil {
			return Page{}, err
		}
		if len(where) > 0 {
			query += " AND " + cursorWhere
		} else {
			query += " WHERE " + cursorWhere
		}
		args = append(args, cursorArgs...)
	}
	if req.Sort != nil {
		direction := "ASC"
		if req.Sort.Desc {
			direction = "DESC"
		}
		query += fmt.Sprintf(" ORDER BY `%s` %s, `__loom_row_id` ASC", req.Sort.Column, direction)
	} else {
		query += " ORDER BY `__loom_row_id` ASC"
	}
	query += fmt.Sprintf(" LIMIT %d", first+1)
	rows, err := r.ClickHouse.QueryRowsArgs(ctx, query, readColumns, args...)
	if err != nil {
		return Page{}, backendCallError(err)
	}
	var total int64
	if len(rows) > 0 {
		total, err = numericCount(rows[0]["__loom_total"])
		if err != nil {
			return Page{}, err
		}
	} else {
		total, err = r.count(ctx, materialization, req, allowed)
		if err != nil {
			return Page{}, err
		}
	}
	hasNext := len(rows) > first
	if hasNext {
		rows = rows[:first]
	}
	next := ""
	if hasNext && len(rows) > 0 {
		last := rows[len(rows)-1]
		var sortValue any
		if req.Sort != nil {
			sortValue = last[req.Sort.Column]
			if sortValue == nil {
				return Page{}, invalidCursor()
			}
		}
		next = encodeCursor(fmt.Sprint(last["__loom_row_id"]), sortValue)
	}
	for _, row := range rows {
		delete(row, "__loom_total")
		delete(row, "__loom_row_id")
		for _, column := range queryColumns {
			if !contains(columns, column) {
				delete(row, column)
			}
		}
	}
	return Page{Materialization: materialization, Columns: columns, Rows: rows, TotalCount: total, HasNext: hasNext, NextCursor: next}, nil
}

// Stream scans one published table without retaining all rows in memory.
func (r *Reader) Stream(ctx context.Context, materialization Materialization, req StreamRequest, visit func(map[string]any) error) error {
	if r == nil || r.ClickHouse == nil {
		return backendUnavailable()
	}
	if visit == nil {
		return invalidRequest()
	}
	columns, allowed, err := readerColumns(materialization.Columns, req.Columns, req.Sort)
	if err != nil {
		return err
	}
	queryColumns := append([]string(nil), columns...)
	if req.Sort != nil && !contains(queryColumns, req.Sort.Column) {
		queryColumns = append(queryColumns, req.Sort.Column)
	}
	queryColumns = append(queryColumns, "__loom_row_id")
	query := fmt.Sprintf("SELECT %s FROM `%s`", quotedColumns(queryColumns), materialization.PhysicalTable)
	args := make([]any, 0)
	where := make([]string, 0, len(req.Filters)+1)
	if !req.Unrestricted {
		if hasColumn(materialization.Columns, authResourcePathColumn) {
			where = append(where, "`auth_resource_path` IN ?")
			args = append(args, req.AuthResourcePaths)
		} else {
			where = append(where, "0")
		}
	}
	filterWhere, filterArgs, err := buildWhere(req.Filters, allowed)
	if err != nil {
		return err
	}
	where = append(where, filterWhere...)
	args = append(args, filterArgs...)
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	if req.Sort != nil {
		direction := "ASC"
		if req.Sort.Desc {
			direction = "DESC"
		}
		query += fmt.Sprintf(" ORDER BY `%s` %s, `__loom_row_id` ASC", req.Sort.Column, direction)
	} else {
		query += " ORDER BY `__loom_row_id` ASC"
	}
	if err := r.ClickHouse.QueryRowsArgsVisit(ctx, query, queryColumns, func(row map[string]any) error {
		delete(row, "__loom_row_id")
		for _, column := range queryColumns {
			if !contains(columns, column) {
				delete(row, column)
			}
		}
		return visit(row)
	}, args...); err != nil {
		return backendCallError(err)
	}
	return nil
}

func readerColumns(source []Column, requested []string, sortBy *Sort) ([]string, map[string]struct{}, error) {
	for _, column := range requested {
		if column == authResourcePathColumn {
			return nil, nil, invalidRequest()
		}
	}
	allowed := make(map[string]struct{}, len(source))
	columns := append([]string(nil), requested...)
	for _, column := range source {
		if column.Name == "__loom_row_id" || column.Name == authResourcePathColumn {
			continue
		}
		allowed[column.Name] = struct{}{}
		if len(requested) == 0 {
			columns = append(columns, column.Name)
		}
	}
	if err := validateReaderColumns(columns, allowed); err != nil {
		return nil, nil, err
	}
	if sortBy != nil {
		if sortBy.Column == authResourcePathColumn {
			return nil, nil, invalidRequest()
		}
		if err := validateReaderColumns([]string{sortBy.Column}, allowed); err != nil {
			return nil, nil, err
		}
	}
	return columns, allowed, nil
}

func (r *Reader) count(ctx context.Context, materialization Materialization, req PageRequest, allowed map[string]struct{}) (int64, error) {
	query := fmt.Sprintf("SELECT count() AS `__loom_total` FROM `%s`", materialization.PhysicalTable)
	args := make([]any, 0)
	where := make([]string, 0, len(req.Filters)+1)
	if !req.Unrestricted {
		if hasColumn(materialization.Columns, authResourcePathColumn) {
			where = append(where, "`auth_resource_path` IN ?")
			args = append(args, req.AuthResourcePaths)
		} else {
			where = append(where, "0")
		}
	}
	filterWhere, filterArgs, err := buildWhere(req.Filters, allowed)
	if err != nil {
		return 0, err
	}
	where = append(where, filterWhere...)
	args = append(args, filterArgs...)
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	rows, err := r.ClickHouse.QueryRowsArgs(ctx, query, []string{"__loom_total"}, args...)
	if err != nil {
		return 0, backendCallError(err)
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return numericCount(rows[0]["__loom_total"])
}

func pageCursorPredicate(cursor *pageCursor, sortBy *Sort) (string, []any, error) {
	if cursor == nil || cursor.RowID == "" {
		return "", nil, invalidCursor()
	}
	if sortBy == nil {
		return "`__loom_row_id` > ?", []any{cursor.RowID}, nil
	}
	if cursor.SortValue == nil {
		return "", nil, invalidCursor()
	}
	op := ">"
	if sortBy.Desc {
		op = "<"
	}
	return fmt.Sprintf("(`%s` %s ? OR (`%s` = ? AND `__loom_row_id` > ?))", sortBy.Column, op, sortBy.Column), []any{cursor.SortValue, cursor.SortValue, cursor.RowID}, nil
}

func hasColumn(columns []Column, name string) bool {
	for _, column := range columns {
		if column.Name == name {
			return true
		}
	}
	return false
}
