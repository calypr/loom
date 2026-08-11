package published

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	bundlepublication "github.com/calypr/loom/internal/dataframe/publication"
	publication "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/store/clickhouse"
)

type Reader struct {
	ClickHouse             *clickhouse.Client
	Catalog                bundlepublication.BundleCatalog
	Logger                 *slog.Logger
	MaxPage                int
	ActiveManifestResolver publication.ActiveResolver
	// LegacyTranslationVersion maps pre-versioned execution rows only during
	// the compatibility window. New rows always carry their exact version.
	LegacyTranslationVersion string
	ProjectStatusResolver    ProjectStatusResolver
	ReleaseExecutionResolver ReleaseExecutionResolver
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
	Columns []string
	Filters []Filter
	Sort    *Sort
	First   int
	After   string
}

type Page struct {
	Materialization Materialization
	Columns         []string
	Rows            []map[string]any
	TotalCount      int64
	HasNext         bool
	NextCursor      string
}

type AggregateResult struct {
	Materialization Materialization
	Columns         []string
	Rows            []map[string]any
}

type AggregateRequest struct {
	GroupBy   []string
	Filters   []Filter
	Operation string
	Column    string
}

// AggregatePublishedID preserves the legacy single-materialization query
// path. Federated callers use AggregateFederatedDataset instead.
func (r *Reader) AggregatePublishedID(ctx context.Context, id string, req AggregateRequest) (AggregateResult, error) {
	if r == nil || r.ClickHouse == nil || r.Catalog == nil {
		return AggregateResult{}, dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	m, err := r.publishedByID(ctx, id)
	if err != nil {
		return AggregateResult{}, err
	}
	return r.aggregateResolved(ctx, req, m)
}

func (r *Reader) aggregateResolved(ctx context.Context, req AggregateRequest, m Materialization) (AggregateResult, error) {
	if m.State != StateReady {
		return AggregateResult{}, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
	}
	allowed := make(map[string]struct{}, len(m.Columns))
	for _, column := range m.Columns {
		allowed[column.Name] = struct{}{}
	}
	for _, column := range req.GroupBy {
		if _, ok := allowed[column]; !ok {
			return AggregateResult{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
		}
	}
	where, args, err := buildWhere(req.Filters, allowed)
	if err != nil {
		return AggregateResult{}, err
	}
	operation := strings.ToUpper(req.Operation)
	if operation != "COUNT" && operation != "COUNT_DISTINCT" && operation != "SUM" && operation != "AVG" && operation != "MIN" && operation != "MAX" {
		return AggregateResult{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	if operation != "COUNT" {
		if _, ok := allowed[req.Column]; !ok {
			return AggregateResult{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
		}
	}
	selects := make([]string, 0, len(req.GroupBy)+1)
	columns := append([]string(nil), req.GroupBy...)
	for _, column := range req.GroupBy {
		selects = append(selects, fmt.Sprintf("`%s`", column))
	}
	metric := "count()"
	metricName := "count"
	switch operation {
	case "COUNT_DISTINCT":
		metric, metricName = fmt.Sprintf("uniqExact(`%s`)", req.Column), "count_distinct"
	case "SUM":
		metric, metricName = fmt.Sprintf("sum(`%s`)", req.Column), "sum"
	case "AVG":
		metric, metricName = fmt.Sprintf("avg(`%s`)", req.Column), "avg"
	case "MIN":
		metric, metricName = fmt.Sprintf("min(`%s`)", req.Column), "min"
	case "MAX":
		metric, metricName = fmt.Sprintf("max(`%s`)", req.Column), "max"
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
	rows, err := r.ClickHouse.QueryRowsArgs(ctx, query, columns, args...)
	if err != nil {
		return AggregateResult{}, backendCallError(err)
	}
	return AggregateResult{Materialization: m, Columns: columns, Rows: rows}, nil
}

// PagePublishedID preserves the legacy single-materialization row query path.
func (r *Reader) PagePublishedID(ctx context.Context, id string, req PageRequest) (Page, error) {
	if r == nil || r.ClickHouse == nil || r.Catalog == nil {
		return Page{}, dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	m, err := r.publishedByID(ctx, id)
	if err != nil {
		return Page{}, err
	}
	return r.pageResolved(ctx, req, m)
}

func (r *Reader) DatasetByPublishedID(ctx context.Context, id string) (Materialization, error) {
	if r == nil || r.Catalog == nil {
		return Materialization{}, fmt.Errorf("bundle catalog dependency is required")
	}
	return r.publishedByID(ctx, id)
}

// StreamPublishedID reads one immutable published materialization directly by
// ID. It intentionally does not consult the mutable logical pointer, so a
// share URL remains readable after a newer publication replaces the current
// pointer.
func (r *Reader) StreamPublishedID(ctx context.Context, id string, req FederatedStreamRequest, visit func(map[string]any) error) error {
	if r == nil || r.ClickHouse == nil || visit == nil {
		return dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	m, err := r.publishedByID(ctx, id)
	if err != nil {
		return err
	}
	if m.State != StateReady {
		return dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
	}
	allowed := make(map[string]struct{}, len(m.Columns))
	for _, column := range m.Columns {
		allowed[column.Name] = struct{}{}
	}
	columns := append([]string(nil), req.Columns...)
	if len(columns) == 0 {
		for _, column := range m.Columns {
			if column.Name != "__loom_row_id" {
				columns = append(columns, column.Name)
			}
		}
	}
	for _, column := range columns {
		if column == "__loom_row_id" {
			return dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
		}
		if _, ok := allowed[column]; !ok {
			return dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
		}
	}
	if req.Sort != nil {
		if _, ok := allowed[req.Sort.Column]; !ok || req.Sort.Column == "__loom_row_id" {
			return dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
		}
	}
	where, args, err := buildWhere(req.Filters, allowed)
	if err != nil {
		return err
	}
	queryColumns := append([]string(nil), columns...)
	if req.Sort != nil && !contains(queryColumns, req.Sort.Column) {
		queryColumns = append(queryColumns, req.Sort.Column)
	}
	queryColumns = append(queryColumns, "__loom_row_id")
	selects := make([]string, len(queryColumns))
	for i, column := range queryColumns {
		selects[i] = fmt.Sprintf("`%s`", column)
	}
	query := fmt.Sprintf("SELECT %s FROM `%s`", strings.Join(selects, ", "), m.PhysicalTable)
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	if req.Sort != nil {
		direction := "ASC"
		if req.Sort.Desc {
			direction = "DESC"
		}
		query += fmt.Sprintf(" ORDER BY `%s` %s, toString(`__loom_row_id`) ASC", req.Sort.Column, direction)
	} else {
		query += " ORDER BY toString(`__loom_row_id`) ASC"
	}
	err = r.ClickHouse.QueryRowsArgsVisit(ctx, query, queryColumns, func(row map[string]any) error {
		delete(row, "__loom_row_id")
		if req.Sort != nil && !contains(columns, req.Sort.Column) {
			delete(row, req.Sort.Column)
		}
		return visit(row)
	}, args...)
	if err != nil {
		return backendCallError(err)
	}
	return nil
}

func (r *Reader) publishedByID(ctx context.Context, id string) (Materialization, error) {
	parts := strings.SplitN(id, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return Materialization{}, fmt.Errorf("invalid published dataset id %q", id)
	}
	execution, err := r.Catalog.GetExecution(ctx, parts[0])
	if err != nil {
		return Materialization{}, err
	}
	if !execution.State.Successful() {
		return Materialization{}, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
	}
	for _, output := range execution.Outputs {
		if output.Name == parts[1] {
			if !output.Queryable() {
				return Materialization{}, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
			}
			return publishedMaterialization(execution, output, output.Name), nil
		}
	}
	return Materialization{}, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
}

func (r *Reader) pageResolved(ctx context.Context, req PageRequest, m Materialization) (Page, error) {
	if m.State != StateReady {
		return Page{}, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
	}
	allowed := make(map[string]struct{}, len(m.Columns))
	for _, column := range m.Columns {
		allowed[column.Name] = struct{}{}
	}
	columns := append([]string(nil), req.Columns...)
	if len(columns) == 0 {
		for _, column := range m.Columns {
			if column.Name != "__loom_row_id" {
				columns = append(columns, column.Name)
			}
		}
	}
	for _, column := range columns {
		if column == "__loom_row_id" {
			return Page{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
		}
		if _, ok := allowed[column]; !ok {
			return Page{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
		}
	}
	if req.Sort != nil {
		if _, ok := allowed[req.Sort.Column]; !ok || req.Sort.Column == "__loom_row_id" {
			return Page{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
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
	where, whereArgs, err := buildWhere(req.Filters, allowed)
	if err != nil {
		return Page{}, err
	}
	countQuery := fmt.Sprintf("SELECT count() AS `__loom_total` FROM `%s`", m.PhysicalTable)
	if len(where) > 0 {
		countQuery += " WHERE " + strings.Join(where, " AND ")
	}
	countRows, err := r.ClickHouse.QueryRowsArgs(ctx, countQuery, []string{"__loom_total"}, whereArgs...)
	if err != nil {
		return Page{}, backendCallError(err)
	}
	if len(countRows) == 0 {
		return Page{}, fmt.Errorf("ClickHouse count query returned no rows")
	}
	totalCount, err := numericCount(countRows[0]["__loom_total"])
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
	selects := make([]string, len(queryColumns))
	for i, column := range queryColumns {
		selects[i] = fmt.Sprintf("`%s`", column)
	}
	query := fmt.Sprintf("SELECT %s FROM `%s`", strings.Join(selects, ", "), m.PhysicalTable)
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	if cursor != nil {
		cursorWhere, cursorArgs, err := cursorPredicate(cursor, req.Sort)
		if err != nil {
			return Page{}, err
		}
		whereArgs = append(whereArgs, cursorArgs...)
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
		query += fmt.Sprintf(" ORDER BY `%s` %s, toString(`__loom_row_id`) ASC", req.Sort.Column, direction)
	} else {
		// Published recipe identities are deterministic strings, while legacy
		// tables may use UInt64. Ordering through the shared string
		// representation keeps keyset pagination valid for both schemas.
		query += " ORDER BY toString(`__loom_row_id`) ASC"
	}
	query += fmt.Sprintf(" LIMIT %d", first+1)
	rows, err := r.ClickHouse.QueryRowsArgs(ctx, query, queryColumns, whereArgs...)
	if err != nil {
		return Page{}, backendCallError(err)
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
				return Page{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidCursor, "")
			}
		}
		next = encodeCursor(fmt.Sprint(last["__loom_row_id"]), sortValue)
	}
	for _, row := range rows {
		delete(row, "__loom_row_id")
		if req.Sort != nil && !contains(columns, req.Sort.Column) {
			delete(row, req.Sort.Column)
		}
	}
	return Page{Materialization: m, Columns: columns, Rows: rows, TotalCount: totalCount, HasNext: hasNext, NextCursor: next}, nil
}

func numericCount(value any) (int64, error) {
	switch typed := value.(type) {
	case int:
		return int64(typed), nil
	case int64:
		return typed, nil
	case float64:
		return int64(typed), nil
	case json.Number:
		return typed.Int64()
	case string:
		return strconv.ParseInt(typed, 10, 64)
	default:
		return 0, fmt.Errorf("ClickHouse count returned unsupported value %T", value)
	}
}

func buildWhere(filters []Filter, allowed map[string]struct{}) ([]string, []any, error) {
	where := make([]string, 0, len(filters))
	args := make([]any, 0, len(filters))
	for _, filter := range filters {
		if _, ok := allowed[filter.Column]; !ok {
			return nil, nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidFilter, "")
		}
		switch strings.ToUpper(filter.Op) {
		case "EQ":
			where = append(where, fmt.Sprintf("`%s` = ?", filter.Column))
			args = append(args, filter.Value)
		case "NEQ":
			where = append(where, fmt.Sprintf("`%s` != ?", filter.Column))
			args = append(args, filter.Value)
		case "IN", "NOT_IN":
			if emptyFilterCollection(filter.Value) {
				if strings.EqualFold(filter.Op, "IN") {
					where = append(where, "0")
				} else {
					where = append(where, "1")
				}
				continue
			}
			op := "IN"
			if strings.EqualFold(filter.Op, "NOT_IN") {
				op = "NOT IN"
			}
			where = append(where, fmt.Sprintf("`%s` %s ?", filter.Column, op))
			args = append(args, filter.Value)
		case "LT", "LTE", "GT", "GTE":
			op := map[string]string{"LT": "<", "LTE": "<=", "GT": ">", "GTE": ">="}[strings.ToUpper(filter.Op)]
			where = append(where, fmt.Sprintf("`%s` %s ?", filter.Column, op))
			args = append(args, filter.Value)
		case "CONTAINS":
			where = append(where, fmt.Sprintf("positionCaseInsensitive(toString(`%s`), ?) > 0", filter.Column))
			args = append(args, filter.Value)
		case "STARTS_WITH":
			where = append(where, fmt.Sprintf("startsWith(toString(`%s`), ?)", filter.Column))
			args = append(args, filter.Value)
		case "EXISTS":
			where = append(where, fmt.Sprintf("isNotNull(`%s`)", filter.Column))
		case "IS_NULL":
			where = append(where, fmt.Sprintf("isNull(`%s`)", filter.Column))
		case "ARRAY_CONTAINS":
			where = append(where, fmt.Sprintf("has(`%s`, ?)", filter.Column))
			args = append(args, filter.Value)
		case "ARRAY_OVERLAPS":
			where = append(where, fmt.Sprintf("hasAny(`%s`, ?)", filter.Column))
			args = append(args, filter.Value)
		default:
			return nil, nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidFilter, "")
		}
	}
	return where, args, nil
}

func emptyFilterCollection(value any) bool {
	switch typed := value.(type) {
	case []string:
		return len(typed) == 0
	case []any:
		return len(typed) == 0
	case nil:
		return true
	default:
		return false
	}
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
		return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidCursor, "")
	}
	var value pageCursor
	if err := json.Unmarshal(data, &value); err != nil || value.RowID == "" {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidCursor, "")
	}
	return &value, nil
}

func cursorPredicate(cursor *pageCursor, sort *Sort) (string, []any, error) {
	row := "toString(`__loom_row_id`) > ?"
	if sort == nil {
		return row, []any{cursor.RowID}, nil
	}
	if cursor.SortValue == nil {
		return "", nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidCursor, "")
	}
	operator := ">"
	if sort.Desc {
		operator = "<"
	}
	return fmt.Sprintf("(`%s` %s ? OR (`%s` = ? AND %s))", sort.Column, operator, sort.Column, row), []any{cursor.SortValue, cursor.SortValue, cursor.RowID}, nil
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
