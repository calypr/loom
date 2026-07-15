package materialization

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/store/clickhouse"
)

type Reader struct {
	ClickHouse *clickhouse.Client
	Registry   Registry
	MaxPage    int
}

type Filter struct {
	Column string
	Op     string
	Value  any
}

type Sort struct {
	Column string
	Desc   bool
}

type PageRequest struct {
	MaterializationID string
	Columns           []string
	Filters           []Filter
	Sort              *Sort
	First             int
	After             string
}

type Page struct {
	Materialization Materialization
	Columns         []string
	Rows            []map[string]any
	HasNext         bool
	NextCursor      string
}

type AggregateRequest struct {
	MaterializationID string
	GroupBy           []string
	Filters           []Filter
	Operation         string
	Column            string
}

type AggregateResult struct {
	Materialization Materialization
	Columns         []string
	Rows            []map[string]any
}

func (r *Reader) Aggregate(ctx context.Context, req AggregateRequest) (AggregateResult, error) {
	if r.ClickHouse == nil || r.Registry == nil {
		return AggregateResult{}, fmt.Errorf("ClickHouse and registry dependencies are required")
	}
	m, err := r.Registry.Get(ctx, req.MaterializationID)
	if err != nil {
		return AggregateResult{}, err
	}
	if m.State != StateReady {
		return AggregateResult{}, fmt.Errorf("materialization %q is not ready", m.ID)
	}
	allowed := map[string]struct{}{}
	for _, column := range m.Columns {
		allowed[column.Name] = struct{}{}
	}
	for _, column := range req.GroupBy {
		if _, ok := allowed[column]; !ok {
			return AggregateResult{}, fmt.Errorf("group column %q is not in materialization schema", column)
		}
	}
	where, err := buildWhere(req.Filters, allowed)
	if err != nil {
		return AggregateResult{}, err
	}
	operation := strings.ToUpper(req.Operation)
	if operation != "COUNT" && operation != "COUNT_DISTINCT" && operation != "SUM" && operation != "MIN" && operation != "MAX" {
		return AggregateResult{}, fmt.Errorf("unsupported dataframe aggregate operation %q", req.Operation)
	}
	if operation != "COUNT" {
		if _, ok := allowed[req.Column]; !ok {
			return AggregateResult{}, fmt.Errorf("aggregate column %q is not in materialization schema", req.Column)
		}
	}
	selects := make([]string, 0, len(req.GroupBy)+1)
	columns := append([]string(nil), req.GroupBy...)
	for _, column := range req.GroupBy {
		selects = append(selects, fmt.Sprintf("`%s`", column))
	}
	metric := "count()"
	metricName := "count"
	if operation == "COUNT_DISTINCT" {
		metric = fmt.Sprintf("uniqExact(`%s`)", req.Column)
		metricName = "count_distinct"
	}
	if operation == "SUM" {
		metric = fmt.Sprintf("sum(`%s`)", req.Column)
		metricName = "sum"
	}
	if operation == "MIN" {
		metric = fmt.Sprintf("min(`%s`)", req.Column)
		metricName = "min"
	}
	if operation == "MAX" {
		metric = fmt.Sprintf("max(`%s`)", req.Column)
		metricName = "max"
	}
	selects = append(selects, metric+" AS `"+metricName+"`")
	columns = append(columns, metricName)
	query := fmt.Sprintf("SELECT %s FROM `%s`", strings.Join(selects, ", "), m.PhysicalTable)
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	if len(req.GroupBy) > 0 {
		query += " GROUP BY " + strings.Join(selects[:len(req.GroupBy)], ", ") + " ORDER BY " + strings.Join(selects[:len(req.GroupBy)], ", ")
	}
	rows, err := r.ClickHouse.QueryRows(ctx, query, columns)
	if err != nil {
		return AggregateResult{}, err
	}
	return AggregateResult{Materialization: m, Columns: columns, Rows: rows}, nil
}

func (r *Reader) Page(ctx context.Context, req PageRequest) (Page, error) {
	if r.ClickHouse == nil || r.Registry == nil {
		return Page{}, fmt.Errorf("ClickHouse and registry dependencies are required")
	}
	m, err := r.Registry.Get(ctx, req.MaterializationID)
	if err != nil {
		return Page{}, err
	}
	if m.State != StateReady {
		return Page{}, fmt.Errorf("materialization %q is not ready", m.ID)
	}
	allowed := map[string]struct{}{}
	for _, column := range m.Columns {
		allowed[column.Name] = struct{}{}
	}
	columns := req.Columns
	if len(columns) == 0 {
		columns = make([]string, 0, len(m.Columns))
		for _, column := range m.Columns {
			if column.Name == "__loom_row_id" {
				continue
			}
			columns = append(columns, column.Name)
		}
	}
	for _, column := range columns {
		if column == "__loom_row_id" {
			return Page{}, fmt.Errorf("column %q is internal to dataframe pagination", column)
		}
		if _, ok := allowed[column]; !ok {
			return Page{}, fmt.Errorf("column %q is not in materialization schema", column)
		}
	}
	if req.Sort != nil {
		if req.Sort.Column == "__loom_row_id" {
			return Page{}, fmt.Errorf("column %q is internal to dataframe pagination", req.Sort.Column)
		}
		if _, ok := allowed[req.Sort.Column]; !ok {
			return Page{}, fmt.Errorf("sort column %q is not in materialization schema", req.Sort.Column)
		}
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
	where, err := buildWhere(req.Filters, allowed)
	if err != nil {
		return Page{}, err
	}
	queryColumns := append([]string(nil), columns...)
	if req.Sort != nil && !contains(queryColumns, req.Sort.Column) {
		queryColumns = append(queryColumns, req.Sort.Column)
	}
	if !contains(queryColumns, "__loom_row_id") {
		queryColumns = append(queryColumns, "__loom_row_id")
	}
	querySelects := make([]string, len(queryColumns))
	for i, column := range queryColumns {
		querySelects[i] = fmt.Sprintf("`%s`", column)
	}
	query := fmt.Sprintf("SELECT %s FROM `%s`", strings.Join(querySelects, ", "), m.PhysicalTable)
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	if cursor != nil {
		cursorWhere, err := cursorPredicate(cursor, req.Sort)
		if err != nil {
			return Page{}, err
		}
		if strings.Contains(query, " WHERE ") {
			query += " AND " + cursorWhere
		} else {
			query += " WHERE " + cursorWhere
		}
	}
	if req.Sort != nil {
		direction := "ASC"
		if req.Sort.Desc {
			direction = "DESC"
		}
		query += fmt.Sprintf(" ORDER BY `%s` %s, `__loom_row_id` ASC", req.Sort.Column, direction)
	} else {
		query += " ORDER BY toUInt64(`__loom_row_id`) ASC"
	}
	query += fmt.Sprintf(" LIMIT %d", first+1)
	rows, err := r.ClickHouse.QueryRows(ctx, query, queryColumns)
	if err != nil {
		return Page{}, err
	}
	hasNext := len(rows) > first
	if hasNext {
		rows = rows[:first]
	}
	next := ""
	if hasNext {
		last := rows[len(rows)-1]
		var sortValue any
		if req.Sort != nil {
			sortValue = last[req.Sort.Column]
			if sortValue == nil {
				return Page{}, fmt.Errorf("cannot create keyset cursor from NULL sort value in %q", req.Sort.Column)
			}
		}
		rowID, ok := last["__loom_row_id"].(string)
		if !ok {
			rowID = fmt.Sprint(last["__loom_row_id"])
		}
		next = encodeCursor(rowID, sortValue)
	}
	for _, row := range rows {
		delete(row, "__loom_row_id")
		if req.Sort != nil && !contains(columns, req.Sort.Column) {
			delete(row, req.Sort.Column)
		}
	}
	return Page{Materialization: m, Columns: columns, Rows: rows, HasNext: hasNext, NextCursor: next}, nil
}

func buildWhere(filters []Filter, allowed map[string]struct{}) ([]string, error) {
	where := make([]string, 0, len(filters))
	for _, filter := range filters {
		if _, ok := allowed[filter.Column]; !ok {
			return nil, fmt.Errorf("filter column %q is not in materialization schema", filter.Column)
		}
		literal, err := jsonLiteral(filter.Value)
		if err != nil {
			return nil, err
		}
		switch strings.ToUpper(filter.Op) {
		case "EQ":
			where = append(where, fmt.Sprintf("`%s` = %s", filter.Column, literal))
		case "CONTAINS":
			where = append(where, fmt.Sprintf("positionCaseInsensitive(toString(`%s`), %s) > 0", filter.Column, literal))
		default:
			return nil, fmt.Errorf("unsupported dataframe filter operation %q", filter.Op)
		}
	}
	return where, nil
}

type pageCursor struct {
	RowID     string `json:"rowId"`
	SortValue any    `json:"sortValue,omitempty"`
}

func encodeCursor(rowID string, sortValue any) string {
	data, _ := json.Marshal(pageCursor{RowID: rowID, SortValue: sortValue})
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeCursor(cursor string) (*pageCursor, error) {
	if cursor == "" {
		return nil, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, fmt.Errorf("invalid dataframe cursor: %w", err)
	}
	var value pageCursor
	if err := json.Unmarshal(data, &value); err != nil || value.RowID == "" {
		return nil, fmt.Errorf("invalid dataframe cursor")
	}
	return &value, nil
}

func cursorPredicate(cursor *pageCursor, sort *Sort) (string, error) {
	rowID, err := jsonLiteral(cursor.RowID)
	if err != nil {
		return "", err
	}
	row := fmt.Sprintf("toUInt64(`__loom_row_id`) > toUInt64(%s)", rowID)
	if sort == nil {
		return row, nil
	}
	if cursor.SortValue == nil {
		return "", fmt.Errorf("keyset cursor is missing sort value")
	}
	literal, err := jsonLiteral(cursor.SortValue)
	if err != nil {
		return "", err
	}
	operator := ">"
	if sort.Desc {
		operator = "<"
	}
	return fmt.Sprintf("(`%s` %s %s OR (`%s` = %s AND %s))", sort.Column, operator, literal, sort.Column, literal, row), nil
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func jsonLiteral(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
