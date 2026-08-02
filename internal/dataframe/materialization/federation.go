package materialization

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	publication "github.com/calypr/loom/internal/publication"
)

// FederatedDataset is the logical reader view of one alias across all
// authorized project publications. Project remains source metadata; it is
// intentionally not a public dataframe row column.
type FederatedDataset struct {
	Name             string
	Revision         string
	Sources          []Materialization
	Columns          []Column
	RowCount         int64
	RowCountComplete bool
}

// SourceAccess is the already-resolved visibility for one published project.
// Missing entries are intentionally treated as restricted with no paths.
type SourceAccess struct {
	ResourcePaths []string
	Unrestricted  bool
}

// FederatedPageRequest is the projectless reader request. The caller supplies
// the already-resolved effective scope; this type never accepts a project
// selector from the browser.
type FederatedPageRequest struct {
	Columns         []string
	Filters         []Filter
	Sort            *Sort
	First           int
	After           string
	AccessByProject map[string]SourceAccess
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
	Columns         []string
	Filters         []Filter
	Sort            *Sort
	AccessByProject map[string]SourceAccess
}

// FederatedAggregateRequest describes an aggregate over the same authorized
// union used by row reads.
type FederatedAggregateRequest struct {
	GroupBy         []string
	Filters         []Filter
	Operation       string
	Column          string
	AccessByProject map[string]SourceAccess
}

type FederatedAggregateResult struct {
	Dataset FederatedDataset
	Columns []string
	Rows    []map[string]any
}

func resolveFederatedDataset(ctx context.Context, catalog BundleCatalog, active publication.ActiveResolver, projects []string, alias string) (FederatedDataset, error) {
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
	activeGeneration := make(map[string]string, len(uniqueProjects))
	if active != nil {
		for _, project := range uniqueProjects {
			manifest, resolveErr := publication.ResolveActive(ctx, active, project)
			if errors.Is(resolveErr, publication.ErrNoActiveGeneration) {
				continue
			}
			if resolveErr != nil {
				return FederatedDataset{}, resolveErr
			}
			activeGeneration[project] = manifest.Dataset.Generation
		}
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
		if active != nil {
			generation, ok := activeGeneration[execution.Project]
			if !ok || execution.DatasetGeneration != generation {
				continue
			}
		}
		pointer, pointerErr := catalog.GetPointer(ctx, execution.PointerName())
		if pointerErr != nil {
			return FederatedDataset{}, fmt.Errorf("resolve dataframe pointer: %w", pointerErr)
		}
		if pointer.ExecutionID != execution.ID {
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
		return FederatedDataset{}, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
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
		return FederatedDataset{}, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
	}
	sort.Slice(sources, func(i, j int) bool {
		if sources[i].Project != sources[j].Project {
			return sources[i].Project < sources[j].Project
		}
		if sources[i].DatasetGeneration != sources[j].DatasetGeneration {
			return sources[i].DatasetGeneration < sources[j].DatasetGeneration
		}
		return sources[i].ID < sources[j].ID
	})
	columns, err := reconcileFederatedColumns(sources)
	if err != nil {
		return FederatedDataset{}, dataframeerrors.Wrap(err, dataframeerrors.CodeSchemaConflict, "")
	}
	rowCount := int64(0)
	parts := make([]string, 0, len(sources))
	for _, source := range sources {
		rowCount += source.RowCount
		parts = append(parts, source.ID, source.DatasetGeneration, source.PhysicalTable)
	}
	for _, column := range columns {
		parts = append(parts, column.Name, column.ClickHouse)
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
	type parsedType struct {
		base            string
		array, nullable bool
	}
	parse := func(value string) (parsedType, error) {
		value = strings.TrimSpace(value)
		result := parsedType{}
		if strings.HasPrefix(value, "Nullable(") && strings.HasSuffix(value, ")") {
			result.nullable = true
			value = value[len("Nullable(") : len(value)-1]
		}
		if strings.HasPrefix(value, "Array(") && strings.HasSuffix(value, ")") {
			result.array = true
			value = value[len("Array(") : len(value)-1]
		}
		if value == "" || strings.HasPrefix(value, "Nullable(") || strings.HasPrefix(value, "Array(") {
			return result, fmt.Errorf("invalid ClickHouse type %q", value)
		}
		result.base = value
		return result, nil
	}
	all := map[string]parsedType{}
	firstOrder := make([]string, 0)
	firstIndex := map[string]int{}
	for sourceIndex, source := range sources {
		seen := map[string]struct{}{}
		for _, column := range source.Columns {
			if column.Name == "__loom_row_id" {
				continue
			}
			if _, ok := seen[column.Name]; ok {
				return nil, fmt.Errorf("duplicate column %q in source %q", column.Name, source.ID)
			}
			seen[column.Name] = struct{}{}
			parsed, err := parse(column.ClickHouse)
			if err != nil {
				return nil, err
			}
			if sourceIndex == 0 {
				firstIndex[column.Name] = len(firstOrder)
				firstOrder = append(firstOrder, column.Name)
			}
			if previous, ok := all[column.Name]; ok {
				if previous.base != parsed.base || previous.array != parsed.array {
					return nil, fmt.Errorf("incompatible schema column %q", column.Name)
				}
				previous.nullable = previous.nullable || parsed.nullable
				all[column.Name] = previous
			} else {
				all[column.Name] = parsed
			}
		}
	}
	newNames := make([]string, 0)
	for name := range all {
		if _, ok := firstIndex[name]; !ok {
			newNames = append(newNames, name)
		}
	}
	sort.Strings(newNames)
	order := append(firstOrder, newNames...)
	result := make([]Column, 0, len(order))
	for _, name := range order {
		parsed := all[name]
		typ := parsed.base
		if parsed.array {
			typ = "Array(" + typ + ")"
		}
		if parsed.nullable && !parsed.array {
			typ = "Nullable(" + typ + ")"
		}
		result = append(result, Column{Name: name, ClickHouse: typ})
	}
	return result, nil
}

func nowPlusSecond() time.Time {
	return time.Now().UTC().Add(time.Second)
}

func (r *Reader) ResolveFederatedDataset(ctx context.Context, projects []string, alias string) (FederatedDataset, error) {
	if r == nil || r.Catalog == nil {
		return FederatedDataset{}, fmt.Errorf("bundle catalog dependency is required")
	}
	return resolveFederatedDataset(ctx, r.Catalog, r.ActiveManifestResolver, projects, alias)
}

// CurrentFederatedSources returns the exact READY pointer-backed publications
// without touching schema reconciliation. It is the authorization snapshot
// boundary used by GraphQL and export callers.
func (r *Reader) CurrentFederatedSources(ctx context.Context, projects []string, alias string) ([]Materialization, error) {
	if r == nil || r.Catalog == nil {
		return nil, fmt.Errorf("bundle catalog dependency is required")
	}
	listed, ok := r.Catalog.(StaleBundleCatalog)
	if !ok {
		return nil, fmt.Errorf("bundle catalog does not support dataset resolution")
	}
	projects = normalizedProjects(projects)
	alias = strings.TrimSpace(alias)
	if len(projects) == 0 || alias == "" {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
	}
	executions, err := listed.ListExecutions(ctx, BundleReady, nowPlusSecond())
	if err != nil {
		return nil, err
	}
	activeGenerations := map[string]string{}
	if r.ActiveManifestResolver != nil {
		for _, project := range projects {
			manifest, resolveErr := publication.ResolveActive(ctx, r.ActiveManifestResolver, project)
			if errors.Is(resolveErr, publication.ErrNoActiveGeneration) {
				continue
			}
			if resolveErr != nil {
				return nil, resolveErr
			}
			activeGenerations[project] = manifest.Dataset.Generation
		}
	}
	allowed := make(map[string]struct{}, len(projects))
	for _, project := range projects {
		allowed[project] = struct{}{}
	}
	latest := map[string]BundleExecution{}
	for _, execution := range executions {
		if execution.State != BundleReady {
			continue
		}
		if _, ok := allowed[execution.Project]; !ok {
			continue
		}
		if r.ActiveManifestResolver != nil {
			generation, ok := activeGenerations[execution.Project]
			if !ok || execution.DatasetGeneration != generation {
				continue
			}
		}
		pointer, pointerErr := r.Catalog.GetPointer(ctx, execution.PointerName())
		if pointerErr != nil {
			return nil, fmt.Errorf("resolve dataframe pointer: %w", pointerErr)
		}
		if pointer.ExecutionID != execution.ID {
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
	result := make([]Materialization, 0, len(latest))
	for _, project := range projects {
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
				result = append(result, publishedMaterialization(execution, output, alias))
				break
			}
		}
	}
	if len(result) == 0 {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Project != result[j].Project {
			return result[i].Project < result[j].Project
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

func (r *Reader) CurrentFederatedAliases(ctx context.Context, projects []string) ([]string, error) {
	if r == nil || r.Catalog == nil {
		return nil, fmt.Errorf("bundle catalog dependency is required")
	}
	listed, ok := r.Catalog.(StaleBundleCatalog)
	if !ok {
		return nil, fmt.Errorf("bundle catalog does not support dataset listing")
	}
	projects = normalizedProjects(projects)
	allowed := map[string]struct{}{}
	for _, project := range projects {
		allowed[project] = struct{}{}
	}
	activeGenerations := map[string]string{}
	if r.ActiveManifestResolver != nil {
		for _, project := range projects {
			manifest, err := publication.ResolveActive(ctx, r.ActiveManifestResolver, project)
			if errors.Is(err, publication.ErrNoActiveGeneration) {
				continue
			}
			if err != nil {
				return nil, err
			}
			activeGenerations[project] = manifest.Dataset.Generation
		}
	}
	executions, err := listed.ListExecutions(ctx, BundleReady, nowPlusSecond())
	if err != nil {
		return nil, err
	}
	aliases := map[string]struct{}{}
	for _, execution := range executions {
		if _, ok := allowed[execution.Project]; !ok {
			continue
		}
		if r.ActiveManifestResolver != nil {
			generation, ok := activeGenerations[execution.Project]
			if !ok || generation != execution.DatasetGeneration {
				continue
			}
		}
		pointer, err := r.Catalog.GetPointer(ctx, execution.PointerName())
		if err != nil {
			return nil, err
		}
		if pointer.ExecutionID != execution.ID {
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
	result := make([]string, 0, len(aliases))
	for alias := range aliases {
		result = append(result, alias)
	}
	sort.Strings(result)
	return result, nil
}

func ReconcileFederatedDataset(alias string, sources []Materialization) (FederatedDataset, error) {
	if len(sources) == 0 {
		return FederatedDataset{}, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
	}
	sort.Slice(sources, func(i, j int) bool {
		if sources[i].Project != sources[j].Project {
			return sources[i].Project < sources[j].Project
		}
		return sources[i].ID < sources[j].ID
	})
	columns, err := reconcileFederatedColumns(sources)
	if err != nil {
		return FederatedDataset{}, dataframeerrors.Wrap(err, dataframeerrors.CodeSchemaConflict, "")
	}
	parts := make([]string, 0, len(sources))
	var rowCount int64
	for _, source := range sources {
		rowCount += source.RowCount
		parts = append(parts, source.ID, source.DatasetGeneration, source.PhysicalTable)
	}
	for _, column := range columns {
		parts = append(parts, column.Name, column.ClickHouse)
	}
	sort.Strings(parts)
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return FederatedDataset{Name: alias, Revision: hex.EncodeToString(digest[:]), Sources: append([]Materialization(nil), sources...), Columns: columns, RowCount: rowCount}, nil
}

// PublishedProjects returns project identities that have a current READY
// generation. It is used only as a candidate set when the authenticator does
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
		if pointerErr != nil {
			return nil, fmt.Errorf("resolve dataframe pointer: %w", pointerErr)
		}
		if pointer.ExecutionID == execution.ID {
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
		return FederatedPage{}, dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	dataset, err := r.ResolveFederatedDataset(ctx, projects, alias)
	if err != nil {
		return FederatedPage{}, err
	}
	return r.PageFederatedDataset(ctx, dataset, req)
}

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
	query := fmt.Sprintf("SELECT %s FROM (%s) AS __loom_federated", quotedColumns(queryColumns), union)
	queryArgs := append([]any(nil), unionArgs...)
	queryArgs = append(queryArgs, whereArgs...)
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	if cursor != nil {
		cursorWhere, cursorArgs, err := federatedCursorPredicate(cursor, req.Sort)
		if err != nil {
			return FederatedPage{}, err
		}
		if strings.Contains(query, " WHERE ") {
			query += " AND " + cursorWhere
		} else {
			query += " WHERE " + cursorWhere
		}
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
		return FederatedPage{}, backendCallError(err)
	}
	count, err := r.federatedCountDataset(ctx, dataset, allowed, req)
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
				return FederatedPage{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidCursor, "")
			}
		}
		next = encodeCursor(rowID, sortValue)
	}
	for _, row := range rows {
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

// StreamFederated visits rows from the authorized current publication union
// without buffering the complete result in Loom. Internal source identifiers
// are removed before the visitor is called.
func (r *Reader) StreamFederated(ctx context.Context, projects []string, alias string, req FederatedStreamRequest, visit func(map[string]any) error) (FederatedDataset, error) {
	if r == nil || r.ClickHouse == nil || r.Catalog == nil {
		return FederatedDataset{}, dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	if visit == nil {
		return FederatedDataset{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	dataset, err := r.ResolveFederatedDataset(ctx, projects, alias)
	if err != nil {
		return FederatedDataset{}, err
	}
	return r.StreamFederatedDataset(ctx, dataset, req, visit)
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

func (r *Reader) AggregateFederated(ctx context.Context, projects []string, alias string, req FederatedAggregateRequest) (FederatedAggregateResult, error) {
	if r == nil || r.ClickHouse == nil || r.Catalog == nil {
		return FederatedAggregateResult{}, dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	dataset, err := r.ResolveFederatedDataset(ctx, projects, alias)
	if err != nil {
		return FederatedAggregateResult{}, err
	}
	return r.AggregateFederatedDataset(ctx, dataset, req)
}

func (r *Reader) AggregateFederatedDataset(ctx context.Context, dataset FederatedDataset, req FederatedAggregateRequest) (FederatedAggregateResult, error) {
	if r == nil || r.ClickHouse == nil {
		return FederatedAggregateResult{}, dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	allowed := make(map[string]struct{}, len(dataset.Columns))
	for _, column := range dataset.Columns {
		if column.Name == authResourcePathColumn || column.Name == "__loom_row_id" {
			continue
		}
		allowed[column.Name] = struct{}{}
	}
	for _, column := range req.GroupBy {
		if _, ok := allowed[column]; !ok {
			return FederatedAggregateResult{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
		}
	}
	operation := strings.ToUpper(req.Operation)
	if operation != "COUNT" && operation != "COUNT_DISTINCT" && operation != "SUM" && operation != "AVG" && operation != "MIN" && operation != "MAX" {
		return FederatedAggregateResult{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	if operation != "COUNT" {
		if _, ok := allowed[req.Column]; !ok {
			return FederatedAggregateResult{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
		}
	}
	columns := append([]string(nil), req.GroupBy...)
	metricName := strings.ToLower(operation)
	if operation == "COUNT" {
		metricName = "count"
	}
	columns = append(columns, metricName)
	unionColumns := append([]string(nil), req.GroupBy...)
	if operation != "COUNT" && !contains(unionColumns, req.Column) {
		unionColumns = append(unionColumns, req.Column)
	}
	for _, filter := range req.Filters {
		if !contains(unionColumns, filter.Column) {
			unionColumns = append(unionColumns, filter.Column)
		}
	}
	union, args, err := federatedNormalizedUnion(dataset, unionColumns, req.AccessByProject)
	if err != nil {
		return FederatedAggregateResult{}, err
	}
	where, whereArgs, err := buildWhere(req.Filters, allowed)
	if err != nil {
		return FederatedAggregateResult{}, err
	}
	args = append(args, whereArgs...)
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
	selectExpr := make([]string, 0, len(req.GroupBy)+1)
	for _, group := range req.GroupBy {
		selectExpr = append(selectExpr, fmt.Sprintf("`%s`", group))
	}
	selectExpr = append(selectExpr, metric+" AS `"+metricName+"`")
	query := fmt.Sprintf("SELECT %s FROM (%s) AS __loom_aggregate", strings.Join(selectExpr, ", "), union)
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	if len(req.GroupBy) > 0 {
		query += " ORDER BY " + quotedColumns(req.GroupBy)
	}
	rows, err := r.ClickHouse.QueryRowsArgs(ctx, query, columns, args...)
	if err != nil {
		return FederatedAggregateResult{}, backendCallError(err)
	}
	return FederatedAggregateResult{Dataset: dataset, Columns: columns, Rows: rows}, nil
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
			if sourceColumn, exists := present[column]; exists {
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
