package materialization

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// FederatedDataset is the logical reader view of one alias across all
// authorized project publications. Project remains source metadata; it is
// intentionally not a public dataframe row column.
type FederatedDataset struct {
	Name     string
	Revision string
	Sources  []Materialization
	Columns  []Column
	RowCount int64
}

// FederatedPageRequest is the projectless reader request. The caller supplies
// the already-resolved effective scope; this type never accepts a project
// selector from the browser.
type FederatedPageRequest struct {
	Columns               []string
	Filters               []Filter
	Sort                  *Sort
	First                 int
	After                 string
	AuthResourcePaths     []string
	AuthUnrestricted      bool
	AuthPathsByProject    map[string][]string
	UnrestrictedByProject map[string]bool
}

// FederatedPage is the projectless row response. Source metadata is retained
// only for internal cursor/revision handling.
type FederatedPage struct {
	Dataset    FederatedDataset
	Columns    []string
	Rows       []map[string]any
	TotalCount int64
	HasNext    bool
	NextCursor string
}

// FederatedStreamRequest describes a bounded-memory scan over the same
// authorized source union used by interactive row reads.
type FederatedStreamRequest struct {
	Columns               []string
	Filters               []Filter
	Sort                  *Sort
	AuthResourcePaths     []string
	AuthUnrestricted      bool
	AuthPathsByProject    map[string][]string
	UnrestrictedByProject map[string]bool
}

// FederatedAggregateRequest describes an aggregate over the same authorized
// union used by row reads.
type FederatedAggregateRequest struct {
	GroupBy               []string
	Filters               []Filter
	Operation             string
	Column                string
	AuthResourcePaths     []string
	AuthUnrestricted      bool
	AuthPathsByProject    map[string][]string
	UnrestrictedByProject map[string]bool
}

type FederatedAggregateResult struct {
	Dataset FederatedDataset
	Columns []string
	Rows    []map[string]any
}

// ResolveFederatedDataset resolves the current READY output for alias from
// each requested authorized project and reconciles one stable public schema.
// The project list is an authorization result, never a browser input.
func ResolveFederatedDataset(ctx context.Context, catalog BundleCatalog, projects []string, alias string) (FederatedDataset, error) {
	if catalog == nil {
		return FederatedDataset{}, fmt.Errorf("bundle catalog is required")
	}
	listed, ok := catalog.(StaleBundleCatalog)
	if !ok {
		return FederatedDataset{}, fmt.Errorf("bundle catalog does not support dataset resolution")
	}
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return FederatedDataset{}, fmt.Errorf("dataType is required")
	}
	uniqueProjects := normalizedProjects(projects)
	if len(uniqueProjects) == 0 {
		return FederatedDataset{}, fmt.Errorf("principal has no authorized projects")
	}
	executions, err := listed.ListExecutions(ctx, BundleReady, nowPlusSecond())
	if err != nil {
		return FederatedDataset{}, err
	}
	allowed := make(map[string]struct{}, len(uniqueProjects))
	for _, project := range uniqueProjects {
		allowed[project] = struct{}{}
	}
	latest := make(map[string]BundleExecution, len(uniqueProjects))
	for _, execution := range executions {
		if execution.State != BundleReady {
			continue
		}
		if _, ok := allowed[execution.Project]; !ok {
			continue
		}
		pointer, pointerErr := catalog.GetPointer(ctx, execution.PointerName())
		if pointerErr != nil || pointer.ExecutionID != execution.ID {
			continue
		}
		if !hasOutputAlias(execution, alias) {
			continue
		}
		current, ok := latest[execution.Project]
		if !ok || execution.UpdatedAt.After(current.UpdatedAt) {
			latest[execution.Project] = execution
		}
	}
	if len(latest) == 0 {
		return FederatedDataset{}, fmt.Errorf("published dataset %q was not found", alias)
	}
	sources := make([]Materialization, 0, len(latest))
	for _, project := range uniqueProjects {
		execution, ok := latest[project]
		if !ok {
			continue
		}
		for _, output := range execution.Outputs {
			outputAlias := output.Alias
			if outputAlias == "" {
				outputAlias = output.Name
			}
			if outputAlias == alias {
				sources = append(sources, publishedMaterialization(execution, output, alias))
				break
			}
		}
	}
	if len(sources) == 0 {
		return FederatedDataset{}, fmt.Errorf("published dataset %q was not found", alias)
	}
	columns, err := reconcileFederatedColumns(sources)
	if err != nil {
		return FederatedDataset{}, err
	}
	rowCount := int64(0)
	parts := make([]string, 0, len(sources))
	for _, source := range sources {
		rowCount += source.RowCount
		parts = append(parts, source.ID, source.DatasetGeneration, source.PhysicalTable)
	}
	sort.Strings(parts)
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return FederatedDataset{Name: alias, Revision: hex.EncodeToString(digest[:]), Sources: sources, Columns: columns, RowCount: rowCount}, nil
}

func normalizedProjects(projects []string) []string {
	seen := make(map[string]struct{}, len(projects))
	result := make([]string, 0, len(projects))
	for _, project := range projects {
		project = strings.TrimSpace(project)
		if project == "" {
			continue
		}
		if _, ok := seen[project]; ok {
			continue
		}
		seen[project] = struct{}{}
		result = append(result, project)
	}
	sort.Strings(result)
	return result
}

func hasOutputAlias(execution BundleExecution, alias string) bool {
	for _, output := range execution.Outputs {
		outputAlias := output.Alias
		if outputAlias == "" {
			outputAlias = output.Name
		}
		if outputAlias == alias {
			return true
		}
	}
	return false
}

func reconcileFederatedColumns(sources []Materialization) ([]Column, error) {
	if len(sources) == 0 {
		return nil, fmt.Errorf("federation has no sources")
	}
	base := make([]Column, 0, len(sources[0].Columns))
	for _, column := range sources[0].Columns {
		if column.Name == "__loom_row_id" {
			continue
		}
		base = append(base, column)
	}
	for _, source := range sources[1:] {
		candidate := make([]Column, 0, len(source.Columns))
		for _, column := range source.Columns {
			if column.Name != "__loom_row_id" {
				candidate = append(candidate, column)
			}
		}
		if len(candidate) != len(base) {
			return nil, fmt.Errorf("incompatible schemas for federated dataset %q", source.Name)
		}
		for index := range base {
			if base[index] != candidate[index] {
				return nil, fmt.Errorf("incompatible schema column %q in federated dataset %q", base[index].Name, source.Name)
			}
		}
	}
	return base, nil
}

func nowPlusSecond() time.Time {
	return time.Now().UTC().Add(time.Second)
}

func (r *Reader) ResolveFederatedDataset(ctx context.Context, projects []string, alias string) (FederatedDataset, error) {
	if r == nil || r.Catalog == nil {
		return FederatedDataset{}, fmt.Errorf("bundle catalog dependency is required")
	}
	return ResolveFederatedDataset(ctx, r.Catalog, projects, alias)
}

func (r *Reader) FederatedDatasets(ctx context.Context, projects []string) ([]FederatedDataset, error) {
	if r == nil || r.Catalog == nil {
		return nil, fmt.Errorf("bundle catalog dependency is required")
	}
	listed, ok := r.Catalog.(StaleBundleCatalog)
	if !ok {
		return nil, fmt.Errorf("bundle catalog does not support dataset listing")
	}
	uniqueProjects := normalizedProjects(projects)
	if len(uniqueProjects) == 0 {
		return nil, fmt.Errorf("principal has no authorized projects")
	}
	allowed := make(map[string]struct{}, len(uniqueProjects))
	for _, project := range uniqueProjects {
		allowed[project] = struct{}{}
	}
	executions, err := listed.ListExecutions(ctx, BundleReady, nowPlusSecond())
	if err != nil {
		return nil, err
	}
	aliases := make(map[string]struct{})
	for _, execution := range executions {
		if execution.State != BundleReady {
			continue
		}
		if _, ok := allowed[execution.Project]; !ok {
			continue
		}
		pointer, pointerErr := r.Catalog.GetPointer(ctx, execution.PointerName())
		if pointerErr != nil || pointer.ExecutionID != execution.ID {
			continue
		}
		for _, output := range execution.Outputs {
			alias := output.Alias
			if alias == "" {
				alias = output.Name
			}
			aliases[alias] = struct{}{}
		}
	}
	names := make([]string, 0, len(aliases))
	for alias := range aliases {
		names = append(names, alias)
	}
	sort.Strings(names)
	result := make([]FederatedDataset, 0, len(names))
	for _, alias := range names {
		dataset, err := ResolveFederatedDataset(ctx, r.Catalog, uniqueProjects, alias)
		if err != nil {
			continue
		}
		result = append(result, dataset)
	}
	return result, nil
}

// PublishedProjects returns project identities that have a current READY
// publication. It is used only as a candidate set when the authenticator does
// not embed project claims; row/source authorization still happens per source.
func (r *Reader) PublishedProjects(ctx context.Context) ([]string, error) {
	if r == nil || r.Catalog == nil {
		return nil, fmt.Errorf("bundle catalog dependency is required")
	}
	listed, ok := r.Catalog.(StaleBundleCatalog)
	if !ok {
		return nil, fmt.Errorf("bundle catalog does not support project discovery")
	}
	executions, err := listed.ListExecutions(ctx, BundleReady, nowPlusSecond())
	if err != nil {
		return nil, err
	}
	projects := make(map[string]struct{})
	for _, execution := range executions {
		if execution.State != BundleReady || execution.Project == "" {
			continue
		}
		pointer, pointerErr := r.Catalog.GetPointer(ctx, execution.PointerName())
		if pointerErr == nil && pointer.ExecutionID == execution.ID {
			projects[execution.Project] = struct{}{}
		}
	}
	result := make([]string, 0, len(projects))
	for project := range projects {
		result = append(result, project)
	}
	sort.Strings(result)
	return result, nil
}

func (r *Reader) PageFederated(ctx context.Context, projects []string, alias string, req FederatedPageRequest) (FederatedPage, error) {
	if r == nil || r.ClickHouse == nil || r.Catalog == nil {
		return FederatedPage{}, fmt.Errorf("ClickHouse and bundle catalog dependencies are required")
	}
	dataset, err := r.ResolveFederatedDataset(ctx, projects, alias)
	if err != nil {
		return FederatedPage{}, err
	}
	allowed := make(map[string]struct{}, len(dataset.Columns))
	for _, column := range dataset.Columns {
		allowed[column.Name] = struct{}{}
	}
	columns := append([]string(nil), req.Columns...)
	if len(columns) == 0 {
		for _, column := range dataset.Columns {
			columns = append(columns, column.Name)
		}
	}
	if err := validateReaderColumns(columns, allowed); err != nil {
		return FederatedPage{}, err
	}
	if req.Sort != nil {
		if err := validateReaderColumns([]string{req.Sort.Column}, allowed); err != nil {
			return FederatedPage{}, fmt.Errorf("sort: %w", err)
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
		return FederatedPage{}, err
	}
	whereBySource := make([]string, 0, len(dataset.Sources))
	whereArgs := make([]any, 0)
	for _, source := range dataset.Sources {
		baseFilters := make([]Filter, 0, len(req.Filters)+1)
		baseFilters = append(baseFilters, req.Filters...)
		unrestricted := req.AuthUnrestricted
		paths := req.AuthResourcePaths
		if req.UnrestrictedByProject != nil {
			unrestricted = req.UnrestrictedByProject[source.Project]
		}
		if req.AuthPathsByProject != nil {
			paths = req.AuthPathsByProject[source.Project]
		}
		if !unrestricted {
			baseFilters = append(baseFilters, Filter{Column: "auth_resource_path", Op: "IN", Value: paths})
		}
		where, args, err := buildWhere(baseFilters, allowed)
		if err != nil {
			return FederatedPage{}, err
		}
		selects := make([]string, 0, len(columns)+2)
		for _, column := range columns {
			selects = append(selects, fmt.Sprintf("`%s`", column))
		}
		selects = append(selects,
			"toString(`__loom_row_id`) AS `__loom_row_id`",
			"concat(?, ':', toString(`__loom_row_id`)) AS `__loom_global_row_id`",
		)
		branch := fmt.Sprintf("SELECT %s FROM `%s`", strings.Join(selects, ", "), source.PhysicalTable)
		if len(where) > 0 {
			branch += " WHERE " + strings.Join(where, " AND ")
		}
		whereBySource = append(whereBySource, branch)
		whereArgs = append(whereArgs, source.ID)
		whereArgs = append(whereArgs, args...)
	}
	union := strings.Join(whereBySource, " UNION ALL ")
	queryColumns := append([]string(nil), columns...)
	queryColumns = append(queryColumns, "__loom_row_id", "__loom_global_row_id")
	query := fmt.Sprintf("SELECT %s FROM (%s) AS __loom_federated", quotedColumns(queryColumns), union)
	queryArgs := append([]any(nil), whereArgs...)
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
	rows, err := r.ClickHouse.QueryRowsArgs(ctx, query, queryColumns, queryArgs...)
	if err != nil {
		return FederatedPage{}, err
	}
	count, err := r.federatedCount(ctx, dataset, allowed, req)
	if err != nil {
		return FederatedPage{}, err
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
				return FederatedPage{}, fmt.Errorf("cannot create keyset cursor from NULL sort value in %q", req.Sort.Column)
			}
		}
		next = encodeCursor(rowID, sortValue)
	}
	for _, row := range rows {
		delete(row, "__loom_row_id")
		delete(row, "__loom_global_row_id")
	}
	return FederatedPage{Dataset: dataset, Columns: columns, Rows: rows, TotalCount: count, HasNext: hasNext, NextCursor: next}, nil
}

// StreamFederated visits rows from the authorized current publication union
// without buffering the complete result in Loom. Internal source identifiers
// are removed before the visitor is called.
func (r *Reader) StreamFederated(ctx context.Context, projects []string, alias string, req FederatedStreamRequest, visit func(map[string]any) error) (FederatedDataset, error) {
	if r == nil || r.ClickHouse == nil || r.Catalog == nil {
		return FederatedDataset{}, fmt.Errorf("ClickHouse and bundle catalog dependencies are required")
	}
	if visit == nil {
		return FederatedDataset{}, fmt.Errorf("dataframe stream visitor is required")
	}
	dataset, err := r.ResolveFederatedDataset(ctx, projects, alias)
	if err != nil {
		return FederatedDataset{}, err
	}
	allowed := make(map[string]struct{}, len(dataset.Columns))
	for _, column := range dataset.Columns {
		allowed[column.Name] = struct{}{}
	}
	columns := append([]string(nil), req.Columns...)
	if len(columns) == 0 {
		for _, column := range dataset.Columns {
			columns = append(columns, column.Name)
		}
	}
	if err := validateReaderColumns(columns, allowed); err != nil {
		return FederatedDataset{}, err
	}
	if req.Sort != nil {
		if err := validateReaderColumns([]string{req.Sort.Column}, allowed); err != nil {
			return FederatedDataset{}, fmt.Errorf("sort: %w", err)
		}
	}
	branches := make([]string, 0, len(dataset.Sources))
	args := make([]any, 0)
	for _, source := range dataset.Sources {
		filters := append([]Filter(nil), req.Filters...)
		unrestricted := req.AuthUnrestricted
		paths := req.AuthResourcePaths
		if req.UnrestrictedByProject != nil {
			unrestricted = req.UnrestrictedByProject[source.Project]
		}
		if req.AuthPathsByProject != nil {
			paths = req.AuthPathsByProject[source.Project]
		}
		if !unrestricted {
			filters = append(filters, Filter{Column: "auth_resource_path", Op: "IN", Value: paths})
		}
		where, branchArgs, err := buildWhere(filters, allowed)
		if err != nil {
			return FederatedDataset{}, err
		}
		selects := make([]string, 0, len(columns)+2)
		for _, column := range columns {
			selects = append(selects, fmt.Sprintf("`%s`", column))
		}
		selects = append(selects,
			"toString(`__loom_row_id`) AS `__loom_row_id`",
			"concat(?, ':', toString(`__loom_row_id`)) AS `__loom_global_row_id`",
		)
		branch := fmt.Sprintf("SELECT %s FROM `%s`", strings.Join(selects, ", "), source.PhysicalTable)
		if len(where) > 0 {
			branch += " WHERE " + strings.Join(where, " AND ")
		}
		branches = append(branches, branch)
		args = append(args, source.ID)
		args = append(args, branchArgs...)
	}
	queryColumns := append([]string(nil), columns...)
	queryColumns = append(queryColumns, "__loom_row_id", "__loom_global_row_id")
	query := fmt.Sprintf("SELECT %s FROM (%s) AS __loom_federated", quotedColumns(queryColumns), strings.Join(branches, " UNION ALL "))
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
		return visit(row)
	}, args...)
	if err != nil {
		return FederatedDataset{}, err
	}
	return dataset, nil
}

func (r *Reader) federatedCount(ctx context.Context, dataset FederatedDataset, allowed map[string]struct{}, req FederatedPageRequest) (int64, error) {
	branches := make([]string, 0, len(dataset.Sources))
	args := make([]any, 0)
	for _, source := range dataset.Sources {
		filters := make([]Filter, 0, len(req.Filters)+1)
		filters = append(filters, req.Filters...)
		unrestricted := req.AuthUnrestricted
		paths := req.AuthResourcePaths
		if req.UnrestrictedByProject != nil {
			unrestricted = req.UnrestrictedByProject[source.Project]
		}
		if req.AuthPathsByProject != nil {
			paths = req.AuthPathsByProject[source.Project]
		}
		if !unrestricted {
			filters = append(filters, Filter{Column: "auth_resource_path", Op: "IN", Value: paths})
		}
		where, branchArgs, err := buildWhere(filters, allowed)
		if err != nil {
			return 0, err
		}
		branch := fmt.Sprintf("SELECT count() AS `__loom_total` FROM `%s`", source.PhysicalTable)
		if len(where) > 0 {
			branch += " WHERE " + strings.Join(where, " AND ")
		}
		branches = append(branches, branch)
		args = append(args, branchArgs...)
	}
	rows, err := r.ClickHouse.QueryRowsArgs(ctx, strings.Join(branches, " UNION ALL "), []string{"__loom_total"}, args...)
	if err != nil {
		return 0, err
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

func (r *Reader) AggregateFederated(ctx context.Context, projects []string, alias string, req FederatedAggregateRequest) (FederatedAggregateResult, error) {
	if r == nil || r.ClickHouse == nil || r.Catalog == nil {
		return FederatedAggregateResult{}, fmt.Errorf("ClickHouse and bundle catalog dependencies are required")
	}
	dataset, err := r.ResolveFederatedDataset(ctx, projects, alias)
	if err != nil {
		return FederatedAggregateResult{}, err
	}
	allowed := make(map[string]struct{}, len(dataset.Columns))
	for _, column := range dataset.Columns {
		allowed[column.Name] = struct{}{}
	}
	for _, column := range req.GroupBy {
		if _, ok := allowed[column]; !ok {
			return FederatedAggregateResult{}, fmt.Errorf("group column %q is not in federated dataset schema", column)
		}
	}
	operation := strings.ToUpper(req.Operation)
	if operation != "COUNT" && operation != "COUNT_DISTINCT" && operation != "SUM" && operation != "AVG" && operation != "MIN" && operation != "MAX" {
		return FederatedAggregateResult{}, fmt.Errorf("unsupported dataframe aggregate operation %q", req.Operation)
	}
	if operation != "COUNT" {
		if _, ok := allowed[req.Column]; !ok {
			return FederatedAggregateResult{}, fmt.Errorf("aggregate column %q is not in federated dataset schema", req.Column)
		}
	}
	columns := append([]string(nil), req.GroupBy...)
	metricName := strings.ToLower(operation)
	if operation == "COUNT" {
		metricName = "count"
	}
	columns = append(columns, metricName)
	branches := make([]string, 0, len(dataset.Sources))
	args := make([]any, 0)
	for _, source := range dataset.Sources {
		filters := append([]Filter(nil), req.Filters...)
		unrestricted := req.AuthUnrestricted
		paths := req.AuthResourcePaths
		if req.UnrestrictedByProject != nil {
			unrestricted = req.UnrestrictedByProject[source.Project]
		}
		if req.AuthPathsByProject != nil {
			paths = req.AuthPathsByProject[source.Project]
		}
		if !unrestricted {
			filters = append(filters, Filter{Column: "auth_resource_path", Op: "IN", Value: paths})
		}
		where, branchArgs, err := buildWhere(filters, allowed)
		if err != nil {
			return FederatedAggregateResult{}, err
		}
		selects := make([]string, 0, len(req.GroupBy)+1)
		for _, group := range req.GroupBy {
			selects = append(selects, fmt.Sprintf("`%s`", group))
		}
		metric := "count()"
		switch operation {
		case "COUNT_DISTINCT":
			metric = fmt.Sprintf("uniqExact(`%s`)", req.Column)
		case "SUM":
			metric = fmt.Sprintf("sum(`%s`)", req.Column)
		case "AVG":
			metric = fmt.Sprintf("avg(`%s`)", req.Column)
		case "MIN":
			metric = fmt.Sprintf("min(`%s`)", req.Column)
		case "MAX":
			metric = fmt.Sprintf("max(`%s`)", req.Column)
		}
		selects = append(selects, metric+" AS `"+metricName+"`")
		branch := fmt.Sprintf("SELECT %s FROM `%s`", strings.Join(selects, ", "), source.PhysicalTable)
		if len(where) > 0 {
			branch += " WHERE " + strings.Join(where, " AND ")
		}
		if len(req.GroupBy) > 0 {
			branch += " GROUP BY " + quotedColumns(req.GroupBy)
		}
		branches = append(branches, branch)
		args = append(args, branchArgs...)
	}
	query := fmt.Sprintf("SELECT %s FROM (%s) AS __loom_aggregate", quotedColumns(columns), strings.Join(branches, " UNION ALL "))
	if len(req.GroupBy) > 0 {
		query += " ORDER BY " + quotedColumns(req.GroupBy)
	}
	rows, err := r.ClickHouse.QueryRowsArgs(ctx, query, columns, args...)
	if err != nil {
		return FederatedAggregateResult{}, err
	}
	return FederatedAggregateResult{Dataset: dataset, Columns: columns, Rows: rows}, nil
}

func validateReaderColumns(columns []string, allowed map[string]struct{}) error {
	for _, column := range columns {
		if column == "__loom_row_id" || column == "__loom_global_row_id" {
			return fmt.Errorf("column %q is internal to dataframe pagination", column)
		}
		if _, ok := allowed[column]; !ok {
			return fmt.Errorf("column %q is not in federated dataset schema", column)
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
