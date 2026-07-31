package materialization

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

type AggregationSpec struct {
	Name              string
	Kind              string
	Column            string
	Size              int
	Interval          float64
	DateInterval      int
	ExcludeSelfFilter bool
}

type AggregationsRequest struct {
	Filters               []Filter
	Specs                 []AggregationSpec
	AuthResourcePaths     []string
	AuthUnrestricted      bool
	AuthPathsByProject    map[string][]string
	UnrestrictedByProject map[string]bool
}

type AggregationResult struct {
	Name         string           `json:"name"`
	Kind         string           `json:"kind"`
	Columns      []string         `json:"columns"`
	Rows         []map[string]any `json:"rows"`
	MissingCount int64            `json:"missingCount,omitempty"`
	Truncated    bool             `json:"truncated,omitempty"`
}

type AggregationsResult struct {
	Dataset      FederatedDataset
	Aggregations []AggregationResult
}

const (
	defaultAggregationSize = 100
	maxAggregationSize     = 10_000
	maxAggregationSpecs    = 50
)

func (r *Reader) AggregateFederatedBatch(ctx context.Context, projects []string, alias string, req AggregationsRequest) (AggregationsResult, error) {
	if r == nil || r.ClickHouse == nil || r.Catalog == nil {
		return AggregationsResult{}, fmt.Errorf("ClickHouse and bundle catalog dependencies are required")
	}
	if len(req.Specs) == 0 {
		return AggregationsResult{}, fmt.Errorf("at least one aggregation specification is required")
	}
	if len(req.Specs) > maxAggregationSpecs {
		return AggregationsResult{}, fmt.Errorf("too many aggregation specifications")
	}
	dataset, err := r.ResolveFederatedDataset(ctx, projects, alias)
	if err != nil {
		return AggregationsResult{}, err
	}
	return r.AggregateFederatedBatchDataset(ctx, dataset, req)
}

func (r *Reader) AggregateFederatedBatchDataset(ctx context.Context, dataset FederatedDataset, req AggregationsRequest) (AggregationsResult, error) {
	if r == nil || r.ClickHouse == nil {
		return AggregationsResult{}, fmt.Errorf("ClickHouse dependency is required")
	}
	if len(req.Specs) == 0 {
		return AggregationsResult{}, fmt.Errorf("at least one aggregation specification is required")
	}
	if len(req.Specs) > maxAggregationSpecs {
		return AggregationsResult{}, fmt.Errorf("too many aggregation specifications")
	}
	allowed := make(map[string]struct{}, len(dataset.Columns))
	for _, column := range dataset.Columns {
		allowed[column.Name] = struct{}{}
	}
	results := make([]AggregationResult, 0, len(req.Specs))
	for _, spec := range req.Specs {
		result, err := r.aggregateFederatedSpec(ctx, dataset, allowed, req, spec)
		if err != nil {
			return AggregationsResult{}, fmt.Errorf("aggregation %q: %w", spec.Name, err)
		}
		results = append(results, result)
	}
	return AggregationsResult{Dataset: dataset, Aggregations: results}, nil
}

func (r *Reader) aggregateFederatedSpec(ctx context.Context, dataset FederatedDataset, allowed map[string]struct{}, req AggregationsRequest, spec AggregationSpec) (AggregationResult, error) {
	kind := strings.ToUpper(strings.TrimSpace(spec.Kind))
	if strings.TrimSpace(spec.Name) == "" {
		return AggregationResult{}, fmt.Errorf("name is required")
	}
	if kind == "" {
		return AggregationResult{}, fmt.Errorf("kind is required")
	}
	if kind != "MISSING" {
		if _, ok := allowed[spec.Column]; !ok || spec.Column == "__loom_row_id" {
			return AggregationResult{}, fmt.Errorf("column %q is not in federated dataset schema", spec.Column)
		}
	}
	filters := req.Filters
	if spec.ExcludeSelfFilter {
		filters = make([]Filter, 0, len(req.Filters))
		for _, filter := range req.Filters {
			if filter.Column != spec.Column {
				filters = append(filters, filter)
			}
		}
	}
	if kind == "MISSING" {
		return r.aggregateMissing(ctx, dataset, allowed, req, filters, spec)
	}
	size := spec.Size
	if size <= 0 {
		size = defaultAggregationSize
	}
	if size > maxAggregationSize {
		return AggregationResult{}, fmt.Errorf("size exceeds maximum of %d", maxAggregationSize)
	}
	switch kind {
	case "TERMS":
		return r.aggregateTerms(ctx, dataset, allowed, req, filters, spec, size)
	case "HISTOGRAM":
		if spec.Interval <= 0 {
			return AggregationResult{}, fmt.Errorf("interval must be greater than zero")
		}
		return r.aggregateHistogram(ctx, dataset, allowed, req, filters, spec, size)
	case "DATE_HISTOGRAM":
		if spec.DateInterval <= 0 {
			return AggregationResult{}, fmt.Errorf("dateInterval must be greater than zero seconds")
		}
		return r.aggregateDateHistogram(ctx, dataset, allowed, req, filters, spec, size)
	case "STATS":
		return r.aggregateStats(ctx, dataset, allowed, req, filters, spec)
	default:
		return AggregationResult{}, fmt.Errorf("unsupported aggregation kind %q", spec.Kind)
	}
}

func (r *Reader) aggregateTerms(ctx context.Context, dataset FederatedDataset, allowed map[string]struct{}, req AggregationsRequest, filters []Filter, spec AggregationSpec, size int) (AggregationResult, error) {
	branches, args, err := r.aggregateValueBranches(dataset, allowed, req, filters, spec.Column)
	if err != nil {
		return AggregationResult{}, err
	}
	query := fmt.Sprintf("SELECT `__loom_agg_key`, count() AS `__loom_doc_count` FROM (%s) AS __loom_values GROUP BY `__loom_agg_key` ORDER BY `__loom_doc_count` DESC, `__loom_agg_key` ASC LIMIT %d", strings.Join(branches, " UNION ALL "), size+1)
	rows, err := r.ClickHouse.QueryRowsArgs(ctx, query, []string{"__loom_agg_key", "__loom_doc_count"}, args...)
	if err != nil {
		return AggregationResult{}, backendCallError(err)
	}
	missing, err := r.missingCount(ctx, dataset, allowed, req, filters, spec.Column)
	if err != nil {
		return AggregationResult{}, err
	}
	truncated := len(rows) > size
	if truncated {
		rows = rows[:size]
	}
	resultRows := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		resultRows = append(resultRows, map[string]any{"key": row["__loom_agg_key"], "doc_count": row["__loom_doc_count"]})
	}
	return AggregationResult{Name: spec.Name, Kind: "TERMS", Columns: []string{"key", "doc_count"}, Rows: resultRows, MissingCount: missing, Truncated: truncated}, nil
}

func (r *Reader) aggregateHistogram(ctx context.Context, dataset FederatedDataset, allowed map[string]struct{}, req AggregationsRequest, filters []Filter, spec AggregationSpec, size int) (AggregationResult, error) {
	branches, args, err := r.aggregateValueBranches(dataset, allowed, req, filters, spec.Column)
	if err != nil {
		return AggregationResult{}, err
	}
	key := "floor(toFloat64(`__loom_agg_key`) / ?)*?"
	args = append([]any{spec.Interval, spec.Interval}, args...)
	query := fmt.Sprintf("SELECT %s AS `__loom_bucket`, count() AS `__loom_doc_count` FROM (%s) AS __loom_values WHERE `__loom_agg_key` IS NOT NULL GROUP BY `__loom_bucket` ORDER BY `__loom_bucket` ASC LIMIT %d", key, strings.Join(branches, " UNION ALL "), size)
	rows, err := r.ClickHouse.QueryRowsArgs(ctx, query, []string{"__loom_bucket", "__loom_doc_count"}, args...)
	if err != nil {
		return AggregationResult{}, backendCallError(err)
	}
	resultRows := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		resultRows = append(resultRows, map[string]any{"key": row["__loom_bucket"], "doc_count": row["__loom_doc_count"]})
	}
	return AggregationResult{Name: spec.Name, Kind: "HISTOGRAM", Columns: []string{"key", "doc_count"}, Rows: resultRows, Truncated: len(rows) >= size}, nil
}

func (r *Reader) aggregateDateHistogram(ctx context.Context, dataset FederatedDataset, allowed map[string]struct{}, req AggregationsRequest, filters []Filter, spec AggregationSpec, size int) (AggregationResult, error) {
	branches, args, err := r.aggregateValueBranches(dataset, allowed, req, filters, spec.Column)
	if err != nil {
		return AggregationResult{}, err
	}
	query := fmt.Sprintf("SELECT toStartOfInterval(`__loom_agg_key`, toIntervalSecond(?)) AS `__loom_bucket`, count() AS `__loom_doc_count` FROM (%s) AS __loom_values WHERE `__loom_agg_key` IS NOT NULL GROUP BY `__loom_bucket` ORDER BY `__loom_bucket` ASC LIMIT %d", strings.Join(branches, " UNION ALL "), size)
	args = append([]any{spec.DateInterval}, args...)
	rows, err := r.ClickHouse.QueryRowsArgs(ctx, query, []string{"__loom_bucket", "__loom_doc_count"}, args...)
	if err != nil {
		return AggregationResult{}, backendCallError(err)
	}
	resultRows := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		resultRows = append(resultRows, map[string]any{"key": row["__loom_bucket"], "doc_count": row["__loom_doc_count"]})
	}
	return AggregationResult{Name: spec.Name, Kind: "DATE_HISTOGRAM", Columns: []string{"key", "doc_count"}, Rows: resultRows, Truncated: len(rows) >= size}, nil
}

func (r *Reader) aggregateStats(ctx context.Context, dataset FederatedDataset, allowed map[string]struct{}, req AggregationsRequest, filters []Filter, spec AggregationSpec) (AggregationResult, error) {
	branches, args, err := r.aggregateValueBranches(dataset, allowed, req, filters, spec.Column)
	if err != nil {
		return AggregationResult{}, err
	}
	query := fmt.Sprintf("SELECT count() AS `count`, countIf(`__loom_agg_key` IS NOT NULL) AS `value_count`, uniqExactIf(`__loom_agg_key`, `__loom_agg_key` IS NOT NULL) AS `distinct_count`, min(`__loom_agg_key`) AS `min`, max(`__loom_agg_key`) AS `max`, sum(`__loom_agg_key`) AS `sum`, avg(`__loom_agg_key`) AS `avg` FROM (%s) AS __loom_values", strings.Join(branches, " UNION ALL "))
	columns := []string{"count", "value_count", "distinct_count", "min", "max", "sum", "avg"}
	rows, err := r.ClickHouse.QueryRowsArgs(ctx, query, columns, args...)
	if err != nil {
		return AggregationResult{}, backendCallError(err)
	}
	return AggregationResult{Name: spec.Name, Kind: "STATS", Columns: columns, Rows: rows}, nil
}

func (r *Reader) aggregateMissing(ctx context.Context, dataset FederatedDataset, allowed map[string]struct{}, req AggregationsRequest, filters []Filter, spec AggregationSpec) (AggregationResult, error) {
	missing, err := r.missingCount(ctx, dataset, allowed, req, filters, spec.Column)
	if err != nil {
		return AggregationResult{}, err
	}
	return AggregationResult{Name: spec.Name, Kind: "MISSING", Columns: []string{"missing_count"}, Rows: []map[string]any{{"missing_count": missing}}}, nil
}

func (r *Reader) missingCount(ctx context.Context, dataset FederatedDataset, allowed map[string]struct{}, req AggregationsRequest, filters []Filter, column string) (int64, error) {
	branches, args, err := r.aggregateValueBranches(dataset, allowed, req, filters, column)
	if err != nil {
		return 0, err
	}
	query := fmt.Sprintf("SELECT count() AS `__loom_missing` FROM (%s) AS __loom_values WHERE `__loom_agg_key` IS NULL", strings.Join(branches, " UNION ALL "))
	rows, err := r.ClickHouse.QueryRowsArgs(ctx, query, []string{"__loom_missing"}, args...)
	if err != nil {
		return 0, backendCallError(err)
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return numericCount(rows[0]["__loom_missing"])
}

func (r *Reader) aggregateValueBranches(dataset FederatedDataset, allowed map[string]struct{}, req AggregationsRequest, filters []Filter, column string) ([]string, []any, error) {
	if _, ok := allowed[column]; !ok {
		return nil, nil, fmt.Errorf("aggregate column %q is not in federated dataset schema", column)
	}
	unionColumns := []string{column}
	for _, filter := range filters {
		if !contains(unionColumns, filter.Column) {
			unionColumns = append(unionColumns, filter.Column)
		}
	}
	union, args, err := federatedNormalizedUnion(dataset, unionColumns, req.AuthResourcePaths, req.AuthUnrestricted, req.AuthPathsByProject, req.UnrestrictedByProject)
	if err != nil {
		return nil, nil, err
	}
	where, whereArgs, err := buildWhere(filters, allowed)
	if err != nil {
		return nil, nil, err
	}
	args = append(args, whereArgs...)
	branch := fmt.Sprintf("SELECT `%s` AS `__loom_agg_key` FROM (%s) AS __loom_values", column, union)
	if len(where) > 0 {
		branch += " WHERE " + strings.Join(where, " AND ")
	}
	return []string{branch}, args, nil
}

// NormalizeAggregationResults provides deterministic result ordering for
// callers that combine multiple independently executed specifications.
func NormalizeAggregationResults(results []AggregationResult) []AggregationResult {
	copyResults := append([]AggregationResult(nil), results...)
	sort.Slice(copyResults, func(i, j int) bool { return copyResults[i].Name < copyResults[j].Name })
	return copyResults
}
