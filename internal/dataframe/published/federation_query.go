package published

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

func (r *Reader) PageFederatedDataset(ctx context.Context, dataset FederatedDataset, req FederatedPageRequest) (FederatedPage, error) {
	if r == nil || r.ClickHouse == nil {
		return FederatedPage{}, dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	columns, allowed, err := federatedColumns(dataset, req.Columns, req.Sort)
	if err != nil {
		return FederatedPage{}, err
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
		return FederatedPage{}, dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidCursor, "")
	}
	unionColumns := append([]string(nil), columns...)
	if req.Sort != nil && !contains(unionColumns, req.Sort.Column) {
		unionColumns = append(unionColumns, req.Sort.Column)
	}
	for _, filter := range req.Filters {
		if !contains(unionColumns, filter.Column) {
			unionColumns = append(unionColumns, filter.Column)
		}
	}
	union, unionArgs, err := federatedNormalizedUnion(dataset, unionColumns, req.AccessByProject)
	if err != nil {
		return FederatedPage{}, err
	}
	where, whereArgs, err := buildWhere(req.Filters, allowed)
	if err != nil {
		return FederatedPage{}, err
	}
	queryColumns := append([]string(nil), unionColumns...)
	queryColumns = append(queryColumns, "__loom_row_id", "__loom_global_row_id")
	selectColumns := quotedColumns(queryColumns) + ", count() OVER() AS `__loom_total`"
	readColumns := append(append([]string(nil), queryColumns...), "__loom_total")
	counted := fmt.Sprintf("SELECT %s FROM (%s) AS __loom_federated", selectColumns, union)
	queryArgs := append([]any(nil), unionArgs...)
	queryArgs = append(queryArgs, whereArgs...)
	if len(where) > 0 {
		counted += " WHERE " + strings.Join(where, " AND ")
	}
	query := fmt.Sprintf("SELECT %s FROM (%s) AS __loom_counted", quotedColumns(readColumns), counted)
	if cursor != nil {
		cursorWhere, cursorArgs, err := federatedCursorPredicate(cursor, req.Sort)
		if err != nil {
			return FederatedPage{}, err
		}
		query += " WHERE " + cursorWhere
		queryArgs = append(queryArgs, cursorArgs...)
	}
	if req.Sort != nil {
		direction := "ASC"
		if req.Sort.Desc {
			direction = "DESC"
		}
		query += fmt.Sprintf(" ORDER BY `%s` %s, `__loom_global_row_id` ASC", req.Sort.Column, direction)
	} else {
		query += " ORDER BY `__loom_global_row_id` ASC"
	}
	query += fmt.Sprintf(" LIMIT %d", first+1)
	rows, err := r.ClickHouse.QueryRowsArgs(ctx, query, readColumns, queryArgs...)
	if err != nil {
		return FederatedPage{}, backendCallError(err)
	}
	var count int64
	if len(rows) > 0 {
		count, err = numericCount(rows[0]["__loom_total"])
		if err != nil {
			return FederatedPage{}, err
		}
	} else {
		// A window has no carrier row for an empty page (including an after
		// cursor beyond the end), so only that case needs the count fallback.
		count, err = r.federatedCountDataset(ctx, dataset, allowed, req)
		if err != nil {
			return FederatedPage{}, err
		}
	}
	hasNext := len(rows) > first
	if hasNext {
		rows = rows[:first]
	}
	next := ""
	if hasNext && len(rows) > 0 {
		last := rows[len(rows)-1]
		rowID := fmt.Sprint(last["__loom_global_row_id"])
		var sortValue any
		if req.Sort != nil {
			sortValue = last[req.Sort.Column]
			if sortValue == nil {
				return FederatedPage{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidCursor, "")
			}
		}
		next = encodeCursor(rowID, sortValue)
	}
	for _, row := range rows {
		delete(row, "__loom_total")
		delete(row, "__loom_row_id")
		delete(row, "__loom_global_row_id")
		for _, column := range unionColumns {
			if !contains(columns, column) {
				delete(row, column)
			}
		}
	}
	return FederatedPage{Dataset: dataset, Columns: columns, Rows: rows, TotalCount: count, HasNext: hasNext, NextCursor: next}, nil
}

func (r *Reader) StreamFederatedDataset(ctx context.Context, dataset FederatedDataset, req FederatedStreamRequest, visit func(map[string]any) error) (FederatedDataset, error) {
	if r == nil || r.ClickHouse == nil {
		return FederatedDataset{}, dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	if visit == nil {
		return FederatedDataset{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	columns, allowed, err := federatedColumns(dataset, req.Columns, req.Sort)
	if err != nil {
		return FederatedDataset{}, err
	}
	unionColumns := append([]string(nil), columns...)
	if req.Sort != nil && !contains(unionColumns, req.Sort.Column) {
		unionColumns = append(unionColumns, req.Sort.Column)
	}
	for _, filter := range req.Filters {
		if !contains(unionColumns, filter.Column) {
			unionColumns = append(unionColumns, filter.Column)
		}
	}
	union, args, err := federatedNormalizedUnion(dataset, unionColumns, req.AccessByProject)
	if err != nil {
		return FederatedDataset{}, err
	}
	where, whereArgs, err := buildWhere(req.Filters, allowed)
	if err != nil {
		return FederatedDataset{}, err
	}
	args = append(args, whereArgs...)
	queryColumns := append([]string(nil), unionColumns...)
	queryColumns = append(queryColumns, "__loom_row_id", "__loom_global_row_id")
	query := fmt.Sprintf("SELECT %s FROM (%s) AS __loom_federated", quotedColumns(queryColumns), union)
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	if req.Sort != nil {
		direction := "ASC"
		if req.Sort.Desc {
			direction = "DESC"
		}
		query += fmt.Sprintf(" ORDER BY `%s` %s, `__loom_global_row_id` ASC", req.Sort.Column, direction)
	} else {
		query += " ORDER BY `__loom_global_row_id` ASC"
	}
	err = r.ClickHouse.QueryRowsArgsVisit(ctx, query, queryColumns, func(row map[string]any) error {
		delete(row, "__loom_row_id")
		delete(row, "__loom_global_row_id")
		for _, column := range unionColumns {
			if !contains(columns, column) {
				delete(row, column)
			}
		}
		return visit(row)
	}, args...)
	if err != nil {
		return FederatedDataset{}, backendCallError(err)
	}
	return dataset, nil
}

func (r *Reader) federatedCountDataset(ctx context.Context, dataset FederatedDataset, allowed map[string]struct{}, req FederatedPageRequest) (int64, error) {
	union, args, err := federatedNormalizedUnion(dataset, datasetVisibleColumns(dataset), req.AccessByProject)
	if err != nil {
		return 0, err
	}
	where, whereArgs, err := buildWhere(req.Filters, allowed)
	if err != nil {
		return 0, err
	}
	args = append(args, whereArgs...)
	query := fmt.Sprintf("SELECT count() AS `__loom_total` FROM (%s) AS __loom_federated", union)
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	rows, err := r.ClickHouse.QueryRowsArgs(ctx, query, []string{"__loom_total"}, args...)
	if err != nil {
		return 0, backendCallError(err)
	}
	var total int64
	for _, row := range rows {
		value, err := numericCount(row["__loom_total"])
		if err != nil {
			return 0, err
		}
		total += value
	}
	return total, nil
}

func datasetVisibleColumns(dataset FederatedDataset) []string {
	result := make([]string, 0, len(dataset.Columns))
	for _, column := range dataset.Columns {
		if column.Name != authResourcePathColumn && column.Name != "__loom_row_id" {
			result = append(result, column.Name)
		}
	}
	return result
}

func federatedColumns(dataset FederatedDataset, requested []string, sort *Sort) ([]string, map[string]struct{}, error) {
	for _, column := range requested {
		if column == authResourcePathColumn {
			return nil, nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
		}
	}
	allowed := make(map[string]struct{}, len(dataset.Columns))
	columns := append([]string(nil), requested...)
	for _, column := range dataset.Columns {
		if column.Name == "__loom_row_id" {
			continue
		}
		allowed[column.Name] = struct{}{}
		if len(requested) == 0 {
			if column.Name != authResourcePathColumn {
				columns = append(columns, column.Name)
			}
		}
	}
	if err := validateReaderColumns(columns, allowed); err != nil {
		return nil, nil, err
	}
	if sort != nil {
		if sort.Column == authResourcePathColumn {
			return nil, nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
		}
		if err := validateReaderColumns([]string{sort.Column}, allowed); err != nil {
			return nil, nil, dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidRequest, "")
		}
	}
	return columns, allowed, nil
}

// federatedNormalizedUnion creates the single typed source union used by rows,
// counts and aggregates. User predicates are deliberately applied by callers
// around this union; only per-source authorization remains inside branches.
func federatedNormalizedUnion(dataset FederatedDataset, columns []string, accessByProject map[string]SourceAccess) (string, []any, error) {
	branches := make([]string, 0, len(dataset.Sources))
	args := make([]any, 0)
	for _, source := range dataset.Sources {
		present := make(map[string]Column, len(source.Columns))
		for _, column := range source.Columns {
			present[column.Name] = column
		}
		selects := make([]string, 0, len(columns)+2)
		for _, column := range columns {
			target, ok := findColumn(dataset.Columns, column)
			if !ok {
				return "", nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
			}
			if column == projectIDColumn {
				// The catalog project is authoritative, including for legacy tables
				// without a physical project_id column.
				selects = append(selects, fmt.Sprintf("CAST(? AS %s) AS `project_id`", target.ClickHouse))
				args = append(args, source.Project)
			} else if sourceColumn, exists := present[column]; exists {
				selects = append(selects, fmt.Sprintf("CAST(`%s` AS %s) AS `%s`", sourceColumn.Name, target.ClickHouse, column))
			} else if strings.HasPrefix(target.ClickHouse, "Array(") {
				selects = append(selects, fmt.Sprintf("CAST([] AS %s) AS `%s`", target.ClickHouse, column))
			} else {
				typ := target.ClickHouse
				if !strings.HasPrefix(typ, "Nullable(") {
					typ = "Nullable(" + typ + ")"
				}
				selects = append(selects, fmt.Sprintf("CAST(NULL AS %s) AS `%s`", typ, column))
			}
		}
		selects = append(selects, "toString(`__loom_row_id`) AS `__loom_row_id`", "concat(?, ':', toString(`__loom_row_id`)) AS `__loom_global_row_id`")
		branch := "SELECT " + strings.Join(selects, ", ") + " FROM `" + source.PhysicalTable + "`"
		access := accessByProject[source.Project]
		if !access.Unrestricted {
			if _, ok := present[authResourcePathColumn]; ok {
				branch += " WHERE `auth_resource_path` IN ?"
				args = append(args, source.ID, access.ResourcePaths)
			} else {
				branch += " WHERE 0"
				args = append(args, source.ID)
			}
		} else {
			args = append(args, source.ID)
		}
		branches = append(branches, branch)
	}
	return strings.Join(branches, " UNION ALL "), args, nil
}

func validateReaderColumns(columns []string, allowed map[string]struct{}) error {
	for _, column := range columns {
		if column == "__loom_row_id" || column == "__loom_global_row_id" {
			return dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, fmt.Sprintf("column %q is internal to dataframe pagination", column))
		}
		if _, ok := allowed[column]; !ok {
			return dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, fmt.Sprintf("column %q is not in federated dataset schema", column))
		}
	}
	return nil
}

func quotedColumns(columns []string) string {
	quoted := make([]string, len(columns))
	for index, column := range columns {
		quoted[index] = fmt.Sprintf("`%s`", column)
	}
	return strings.Join(quoted, ", ")
}

func shortQueryID(query string) string {
	digest := sha256.Sum256([]byte(query))
	return hex.EncodeToString(digest[:8])
}

func federatedCursorPredicate(cursor *pageCursor, sort *Sort) (string, []any, error) {
	if cursor == nil || cursor.RowID == "" {
		return "", nil, fmt.Errorf("invalid federated cursor")
	}
	if sort == nil {
		return "`__loom_global_row_id` > ?", []any{cursor.RowID}, nil
	}
	if cursor.SortValue == nil {
		return "", nil, fmt.Errorf("federated keyset cursor is missing sort value")
	}
	op := ">"
	if sort.Desc {
		op = "<"
	}
	return fmt.Sprintf("(`%s` %s ? OR (`%s` = ? AND `__loom_global_row_id` > ?))", sort.Column, op, sort.Column), []any{cursor.SortValue, cursor.SortValue, cursor.RowID}, nil
}
