package dataframe

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	dataframemodel "github.com/calypr/loom/internal/api/dataframe"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dfmaterialization "github.com/calypr/loom/internal/dataframe/published"
	httpctx "github.com/calypr/loom/internal/foundation/httpctx"
)

func (s *Service) AggregationsInput(ctx context.Context, input dataframemodel.DataframeAggregationsInput) (dfmaterialization.AggregationsResult, error) {
	selector, err := resolveSelector(input.Selector)
	if err != nil {
		return dfmaterialization.AggregationsResult{}, err
	}
	result, err := s.aggregations(ctx, input, selector)
	if err != nil {
		s.logReadFailure(ctx, "clickhouse_aggregations", selector.Output, err, "project", input.ProjectID)
	}
	return result, err
}

func (s *Service) aggregations(ctx context.Context, input dataframemodel.DataframeAggregationsInput, selector dfmaterialization.DataframeSelector) (dfmaterialization.AggregationsResult, error) {
	started := time.Now()
	materialization, access, err := s.currentProjectDataset(ctx, input.ProjectID, selector)
	if err != nil {
		return dfmaterialization.AggregationsResult{}, mapReaderError(err)
	}
	jobs, err := aggregationJobs(input, convertFilters(input.Filters))
	if err != nil {
		return dfmaterialization.AggregationsResult{}, err
	}
	queryStarted := time.Now()
	batch, err := s.reader.ExecuteAggregateBatch(ctx, materialization, dfmaterialization.AggregateBatchRequest{
		Jobs: jobs, AuthResourcePaths: access.authResourcePaths, Unrestricted: access.unrestricted,
	})
	s.logAggregationBatch(ctx, batch, queryStarted.Sub(started), time.Since(queryStarted))
	if err != nil {
		return dfmaterialization.AggregationsResult{}, mapReaderError(err)
	}
	if err := ctx.Err(); err != nil {
		return dfmaterialization.AggregationsResult{}, mapReaderError(err)
	}
	results := make([]dfmaterialization.AggregationResult, 0, len(batch.Jobs))
	for _, job := range batch.Jobs {
		spec := input.Specs[job.ID]
		if job.Err != nil {
			return dfmaterialization.AggregationsResult{}, fmt.Errorf("aggregation %q: %w", spec.Name, mapReaderError(job.Err))
		}
		results = append(results, dfmaterialization.AggregationResult{
			Name: spec.Name, Kind: string(jobs[job.ID].ResponseMode),
			Columns: job.Columns, Rows: job.Rows, MissingCount: job.MissingCount, Truncated: job.Truncated,
		})
	}
	return dfmaterialization.AggregationsResult{
		Materialization: materialization, Aggregations: dfmaterialization.NormalizeAggregationResults(results),
	}, nil
}

func (s *Service) logAggregationBatch(ctx context.Context, batch dfmaterialization.AggregateBatchResult, planning, query time.Duration) {
	var resultRows int
	for _, job := range batch.Jobs {
		resultRows += len(job.Rows)
	}
	attrs := []any{
		"request_id", httpctx.RequestIDFromContext(ctx), "logical_jobs", batch.LogicalJobs,
		"deduplicated_jobs", batch.DeduplicatedJobs, "filter_groups", batch.FilterGroups,
		"grouping_statements", batch.GroupingStatements, "scalar_statements", batch.ScalarStatements,
		"clickhouse_calls", batch.Statements, "source_count", 1, "planning_ms", planning.Milliseconds(),
		"clickhouse_ms", query.Milliseconds(), "result_rows", resultRows,
	}
	if batch.Statements > 5 {
		s.logger.Warn("dataframe aggregate batch executed", attrs...)
	} else {
		s.logger.Info("dataframe aggregate batch executed", attrs...)
	}
}

func AggregationsJSON(result dfmaterialization.AggregationsResult) (json.RawMessage, error) {
	return json.Marshal(dfmaterialization.NormalizeAggregationResults(result.Aggregations))
}

func aggregationJobs(input dataframemodel.DataframeAggregationsInput, filters []dfmaterialization.Filter) ([]dfmaterialization.AggregateJob, error) {
	if len(input.Specs) == 0 || len(input.Specs) > 50 {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	jobs := make([]dfmaterialization.AggregateJob, 0, len(input.Specs))
	for _, spec := range input.Specs {
		if spec == nil {
			return nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
		}
		mode := dfmaterialization.AggregateResponseMode(strings.ToUpper(strings.TrimSpace(spec.Kind)))
		switch mode {
		case dfmaterialization.AggregateResponseTerms, dfmaterialization.AggregateResponseHistogram,
			dfmaterialization.AggregateResponseDateHistogram, dfmaterialization.AggregateResponseStats,
			dfmaterialization.AggregateResponseMissing:
		default:
			return nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
		}
		jobFilters := filters
		if spec.ExcludeSelfFilter != nil && *spec.ExcludeSelfFilter {
			jobFilters = dfmaterialization.ExcludeSelfFilters(filters, spec.Column)
		}
		job := dfmaterialization.AggregateJob{
			ID: len(jobs), ResponseMode: mode, Filters: jobFilters,
			Column: spec.Column,
		}
		if spec.Size != nil {
			job.Size = *spec.Size
		}
		if spec.Interval != nil {
			job.Interval = *spec.Interval
		}
		if spec.DateInterval != nil {
			job.DateInterval = *spec.DateInterval
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}
