package published

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

type plannedAggregateJob struct {
	job       AggregateJob
	filters   []Filter
	filterKey string
	key       string
	aliases   []int
}

type aggregateStatementKind string

const (
	statementLegacy aggregateStatementKind = "legacy"
	statementTerms  aggregateStatementKind = "terms"
	statementBucket aggregateStatementKind = "bucket"
	statementScalar aggregateStatementKind = "scalar"
)

type aggregateStatementPlan struct {
	kind    aggregateStatementKind
	jobs    []*plannedAggregateJob
	filters []Filter
}

type aggregatePlan struct {
	jobs       []*plannedAggregateJob
	statements []aggregateStatementPlan
	results    map[int]AggregateJobResult
}

func ExcludeSelfFilters(filters []Filter, column string) []Filter {
	result := make([]Filter, 0, len(filters))
	for _, filter := range filters {
		if strings.TrimSpace(filter.Column) != strings.TrimSpace(column) {
			result = append(result, filter)
		}
	}
	return result
}

func buildAggregatePlan(dataset Materialization, req AggregateBatchRequest) aggregatePlan {
	plan := aggregatePlan{results: make(map[int]AggregateJobResult, len(req.Jobs))}
	allowed := make(map[string]struct{}, len(dataset.Columns))
	for _, column := range dataset.Columns {
		if !internalAggregateColumn(column.Name) {
			allowed[column.Name] = struct{}{}
		}
	}
	dedup := make(map[string]*plannedAggregateJob)
	for _, input := range req.Jobs {
		job, filters, filterKey, key, err := validateAggregateJob(input, allowed)
		if err != nil {
			plan.results[input.ID] = AggregateJobResult{ID: input.ID, Err: err}
			continue
		}
		if existing := dedup[key]; existing != nil {
			existing.aliases = append(existing.aliases, job.ID)
			continue
		}
		planned := &plannedAggregateJob{job: job, filters: filters, filterKey: filterKey, key: key, aliases: []int{job.ID}}
		dedup[key] = planned
		plan.jobs = append(plan.jobs, planned)
	}

	groups := make(map[string][]*plannedAggregateJob)
	keys := make([]string, 0)
	for _, job := range plan.jobs {
		family := statementFamily(job.job, dataset)
		key := job.filterKey + "\x00" + family
		if _, ok := groups[key]; !ok {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], job)
	}
	sort.Strings(keys)
	for _, key := range keys {
		jobs := groups[key]
		kind := statementKind(jobs[0].job.ResponseMode)
		if kind == statementLegacy || kind == statementTerms || kind == statementBucket {
			for start := 0; start < len(jobs); start += maxGroupingSets {
				end := start + maxGroupingSets
				if end > len(jobs) {
					end = len(jobs)
				}
				plan.statements = append(plan.statements, aggregateStatementPlan{kind: kind, jobs: jobs[start:end], filters: jobs[0].filters})
			}
		} else {
			plan.statements = append(plan.statements, aggregateStatementPlan{kind: kind, jobs: jobs, filters: jobs[0].filters})
		}
	}
	// Terms buckets need a missing count. Fold those expressions into the
	// compatible scalar scan (or create one scalar scan for the filter group)
	// instead of issuing a second query per terms specification.
	for _, job := range plan.jobs {
		if job.job.ResponseMode != AggregateResponseTerms {
			continue
		}
		found := false
		for index := range plan.statements {
			statement := &plan.statements[index]
			if statement.kind == statementScalar && len(statement.jobs) > 0 && statement.jobs[0].filterKey == job.filterKey {
				statement.jobs = append(statement.jobs, job)
				found = true
				break
			}
		}
		if !found {
			plan.statements = append(plan.statements, aggregateStatementPlan{kind: statementScalar, jobs: []*plannedAggregateJob{job}, filters: job.filters})
		}
	}
	ordered := make([]aggregateStatementPlan, 0, len(plan.statements))
	for _, statement := range plan.statements {
		if statement.kind != statementScalar {
			ordered = append(ordered, statement)
		}
	}
	for _, statement := range plan.statements {
		if statement.kind == statementScalar {
			ordered = append(ordered, statement)
		}
	}
	plan.statements = ordered
	return plan
}

func validateAggregateJob(input AggregateJob, allowed map[string]struct{}) (AggregateJob, []Filter, string, string, error) {
	job := input
	job.Operation = strings.ToUpper(strings.TrimSpace(job.Operation))
	job.Column = strings.TrimSpace(job.Column)
	for i := range job.GroupBy {
		job.GroupBy[i] = strings.TrimSpace(job.GroupBy[i])
		if _, ok := allowed[job.GroupBy[i]]; !ok || internalAggregateColumn(job.GroupBy[i]) {
			return job, nil, "", "", aggregateInvalidRequest("group column %q is not in the published dataset schema", job.GroupBy[i])
		}
	}
	filters, filterKey, err := canonicalFilters(job.Filters, allowed)
	if err != nil {
		return job, nil, "", "", err
	}
	switch job.ResponseMode {
	case AggregateResponseLegacy:
		switch job.Operation {
		case "COUNT":
			job.Column = ""
		case "COUNT_DISTINCT", "SUM", "AVG", "MIN", "MAX":
			if _, ok := allowed[job.Column]; !ok || internalAggregateColumn(job.Column) {
				return job, nil, "", "", aggregateInvalidRequest("metric column %q is not in the published dataset schema", job.Column)
			}
		default:
			return job, nil, "", "", aggregateInvalidRequest("unsupported aggregate operation %q", input.Operation)
		}
	case AggregateResponseTerms, AggregateResponseHistogram, AggregateResponseDateHistogram, AggregateResponseStats, AggregateResponseMissing:
		if _, ok := allowed[job.Column]; !ok || internalAggregateColumn(job.Column) {
			return job, nil, "", "", aggregateInvalidRequest("aggregate column %q is not in the published dataset schema", job.Column)
		}
		if job.ResponseMode == AggregateResponseTerms || job.ResponseMode == AggregateResponseHistogram || job.ResponseMode == AggregateResponseDateHistogram {
			if job.Size <= 0 {
				job.Size = defaultAggregationSize
			}
			if job.Size > maxAggregationSize {
				return job, nil, "", "", aggregateInvalidRequest("size exceeds maximum of %d", maxAggregationSize)
			}
		}
		if job.ResponseMode == AggregateResponseHistogram && job.Interval <= 0 {
			return job, nil, "", "", aggregateInvalidRequest("interval must be greater than zero")
		}
		if job.ResponseMode == AggregateResponseDateHistogram && job.DateInterval <= 0 {
			return job, nil, "", "", aggregateInvalidRequest("dateInterval must be greater than zero seconds")
		}
	default:
		return job, nil, "", "", aggregateInvalidRequest("unsupported aggregate response mode %q", job.ResponseMode)
	}
	job.Filters = filters
	keyBytes, _ := json.Marshal([]any{job.ResponseMode, filterKey, job.GroupBy, job.Operation, job.Column, job.Size, job.Interval, job.DateInterval})
	return job, filters, filterKey, string(keyBytes), nil
}

func internalAggregateColumn(column string) bool {
	switch strings.TrimSpace(column) {
	case "__loom_row_id", "__loom_global_row_id", authResourcePathColumn:
		return true
	default:
		return false
	}
}

func canonicalFilters(filters []Filter, allowed map[string]struct{}) ([]Filter, string, error) {
	type token struct {
		encoded string
		filter  Filter
	}
	tokens := make([]token, 0, len(filters))
	for _, input := range filters {
		filter := input
		filter.Column = strings.TrimSpace(filter.Column)
		filter.Op = strings.ToUpper(strings.TrimSpace(filter.Op))
		if _, ok := allowed[filter.Column]; !ok || internalAggregateColumn(filter.Column) {
			return nil, "", dataframeerrors.NewError(dataframeerrors.CodeInvalidFilter, fmt.Sprintf("filter column %q is not in the published dataset schema", filter.Column))
		}
		switch filter.Op {
		case "EQ", "NEQ", "IN", "NOT_IN", "LT", "LTE", "GT", "GTE", "CONTAINS", "STARTS_WITH", "EXISTS", "IS_NULL", "ARRAY_CONTAINS", "ARRAY_OVERLAPS":
		default:
			return nil, "", dataframeerrors.NewError(dataframeerrors.CodeInvalidFilter, fmt.Sprintf("unsupported filter operation %q", input.Op))
		}
		if filter.Op == "IN" || filter.Op == "NOT_IN" || filter.Op == "ARRAY_OVERLAPS" {
			filter.Value = canonicalCollection(filter.Value)
		}
		value, err := json.Marshal(filter.Value)
		if err != nil {
			return nil, "", fmt.Errorf("encode filter value: %w", err)
		}
		encodedBytes, _ := json.Marshal([]json.RawMessage{json.RawMessage(mustJSON(filter.Column)), json.RawMessage(mustJSON(filter.Op)), value})
		tokens = append(tokens, token{encoded: string(encodedBytes), filter: filter})
	}
	sort.SliceStable(tokens, func(i, j int) bool { return tokens[i].encoded < tokens[j].encoded })
	result := make([]Filter, len(tokens))
	parts := make([]string, len(tokens))
	for i, item := range tokens {
		result[i], parts[i] = item.filter, item.encoded
	}
	return result, strings.Join(parts, "\x1e"), nil
}

func aggregateInvalidRequest(format string, args ...any) error {
	return dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, fmt.Sprintf(format, args...))
}

func mustJSON(value string) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}

func canonicalCollection(value any) any {
	var values []any
	switch typed := value.(type) {
	case []any:
		values = append(values, typed...)
	case []string:
		for _, item := range typed {
			values = append(values, item)
		}
	default:
		return value
	}
	sort.SliceStable(values, func(i, j int) bool {
		left, _ := json.Marshal(values[i])
		right, _ := json.Marshal(values[j])
		return string(left) < string(right)
	})
	return values
}

func statementFamily(job AggregateJob, dataset Materialization) string {
	switch job.ResponseMode {
	case AggregateResponseLegacy:
		return "legacy:" + job.Operation + ":" + job.Column
	case AggregateResponseTerms:
		column, _ := findColumn(dataset.Columns, job.Column)
		return "terms:" + column.ClickHouse
	case AggregateResponseHistogram, AggregateResponseDateHistogram:
		return "bucket:" + string(job.ResponseMode)
	case AggregateResponseStats, AggregateResponseMissing:
		return "scalar"
	default:
		return string(job.ResponseMode)
	}
}

func statementKind(mode AggregateResponseMode) aggregateStatementKind {
	switch mode {
	case AggregateResponseLegacy:
		return statementLegacy
	case AggregateResponseTerms:
		return statementTerms
	case AggregateResponseHistogram, AggregateResponseDateHistogram:
		return statementBucket
	default:
		return statementScalar
	}
}
