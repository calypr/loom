package materializationapi

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/calypr/loom/generated/graphql/graph/model"
	httpapi "github.com/calypr/loom/internal/api/http"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dfmaterialization "github.com/calypr/loom/internal/dataframe/published"
)

const aggregateFallbackDelay = 2 * time.Millisecond

type aggregateOperationStateKey struct{}

type aggregateOperationState struct {
	service       *Service
	operationCtx  context.Context
	expectedCalls int
	receivedCalls int
	pending       []*aggregateCall
	timer         *time.Timer
	dispatching   bool
	dispatchCalls func(context.Context, []*aggregateCall)
	lastEnqueue   time.Time

	mu          sync.Mutex
	projects    *projectsCacheEntry
	federations map[string]*federationCacheEntry
}

type aggregateCallKind uint8

const (
	aggregateCallLegacy aggregateCallKind = iota
	aggregateCallRich
)

type aggregateCall struct {
	kind     aggregateCallKind
	selector dfmaterialization.DataframeSelector
	filters  []dfmaterialization.Filter
	legacy   model.DataframeAggregateInput
	rich     model.DataframeAggregationsInput
	result   chan aggregateCallResult
}

type aggregateCallResult struct {
	legacy dfmaterialization.AggregateResult
	rich   dfmaterialization.AggregationsResult
	err    error
}

func (s *Service) WithOperationContext(ctx context.Context, expectedCalls int) context.Context {
	state := &aggregateOperationState{
		service: s, expectedCalls: expectedCalls,
		federations: make(map[string]*federationCacheEntry),
	}
	result := context.WithValue(ctx, aggregateOperationStateKey{}, state)
	state.operationCtx = result
	if done := result.Done(); done != nil {
		go func() {
			<-done
			state.cancelPending(result.Err())
		}()
	}
	if s != nil && s.logger != nil {
		s.logger.Debug("dataframe aggregate operation installed",
			"request_id", httpapi.RequestIDFromContext(result),
			"expected_calls", expectedCalls,
		)
	}
	return result
}

func aggregateStateFromContext(ctx context.Context) *aggregateOperationState {
	state, _ := ctx.Value(aggregateOperationStateKey{}).(*aggregateOperationState)
	return state
}

func (s *Service) submitAggregateCall(ctx context.Context, call *aggregateCall) aggregateCallResult {
	call.result = make(chan aggregateCallResult, 1)
	state := aggregateStateFromContext(ctx)
	if state == nil || state.service != s || state.expectedCalls <= 0 {
		s.dispatchAggregateCalls(ctx, []*aggregateCall{call})
	} else {
		state.enqueue(call)
	}
	select {
	case result := <-call.result:
		return result
	case <-ctx.Done():
		return aggregateCallResult{err: ctx.Err()}
	}
}

func (s *aggregateOperationState) enqueue(call *aggregateCall) {
	s.mu.Lock()
	if err := s.operationCtx.Err(); err != nil {
		s.mu.Unlock()
		call.result <- aggregateCallResult{err: err}
		return
	}
	s.pending = append(s.pending, call)
	s.receivedCalls++
	s.lastEnqueue = time.Now()
	if s.timer == nil {
		s.timer = time.AfterFunc(aggregateFallbackDelay, s.fallbackDispatch)
	}
	if s.receivedCalls >= s.expectedCalls {
		if s.timer != nil {
			s.timer.Stop()
			s.timer = nil
		}
		batch := s.takePendingLocked()
		s.mu.Unlock()
		go s.dispatch(batch)
		return
	}
	s.mu.Unlock()
}

func (s *aggregateOperationState) fallbackDispatch() {
	s.mu.Lock()
	if remaining := aggregateFallbackDelay - time.Since(s.lastEnqueue); remaining > 0 {
		s.timer = time.AfterFunc(remaining, s.fallbackDispatch)
		s.mu.Unlock()
		return
	}
	s.timer = nil
	batch := s.takePendingLocked()
	s.mu.Unlock()
	if s.service != nil && s.service.logger != nil && len(batch) > 0 {
		s.service.logger.Debug("dataframe aggregate fallback dispatch",
			"request_id", httpapi.RequestIDFromContext(s.operationCtx),
			"batch_calls", len(batch),
			"expected_calls", s.expectedCalls,
			"received_calls", s.receivedCalls,
		)
	}
	s.dispatch(batch)
}

func (s *aggregateOperationState) takePendingLocked() []*aggregateCall {
	batch := s.pending
	s.pending = nil
	if len(batch) > 0 {
		s.dispatching = true
	}
	return batch
}

func (s *aggregateOperationState) dispatch(batch []*aggregateCall) {
	if len(batch) > 0 {
		if s.service != nil && s.service.logger != nil {
			s.service.logger.Debug("dataframe aggregate dispatch",
				"request_id", httpapi.RequestIDFromContext(s.operationCtx),
				"batch_calls", len(batch),
				"expected_calls", s.expectedCalls,
				"received_calls", s.receivedCalls,
			)
		}
		if s.dispatchCalls != nil {
			s.dispatchCalls(s.operationCtx, batch)
		} else {
			s.service.dispatchAggregateCalls(s.operationCtx, batch)
		}
	}
	s.mu.Lock()
	s.dispatching = false
	if len(s.pending) > 0 && s.timer == nil {
		s.timer = time.AfterFunc(aggregateFallbackDelay, s.fallbackDispatch)
	}
	s.mu.Unlock()
}

func (s *aggregateOperationState) cancelPending(err error) {
	s.mu.Lock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	pending := s.pending
	s.pending = nil
	s.mu.Unlock()
	for _, call := range pending {
		call.result <- aggregateCallResult{err: err}
	}
}

type aggregateExecutionGroup struct {
	dataset dfmaterialization.FederatedDataset
	access  map[string]dfmaterialization.SourceAccess
	calls   []*aggregateCall
}

func (s *Service) dispatchAggregateCalls(ctx context.Context, calls []*aggregateCall) {
	started := time.Now()
	if len(calls) == 0 {
		return
	}
	if s.reader == nil {
		for _, call := range calls {
			call.result <- aggregateCallResult{err: readerUnavailable()}
		}
		return
	}
	principal, err := s.principal(ctx)
	if err != nil {
		for _, call := range calls {
			call.result <- aggregateCallResult{err: mapReaderError(err)}
		}
		return
	}

	groups := make(map[string]*aggregateExecutionGroup)
	groupKeys := make([]string, 0)
	for _, call := range calls {
		dataset, access, resolveErr := s.authorizedFederation(ctx, principal, call.selector, call.filters)
		if resolveErr != nil {
			call.result <- aggregateCallResult{err: mapReaderError(resolveErr)}
			continue
		}
		if len(dataset.Sources) == 0 {
			call.result <- aggregateCallResult{err: dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")}
			continue
		}
		key := aggregateDatasetKey(dataset)
		group := groups[key]
		if group == nil {
			group = &aggregateExecutionGroup{dataset: dataset, access: access}
			groups[key] = group
			groupKeys = append(groupKeys, key)
		}
		group.calls = append(group.calls, call)
	}
	sort.Strings(groupKeys)

	logicalJobs, deduplicated, statements, filterGroups, groupingStatements, scalarStatements, resultRows, sourceCount := 0, 0, 0, 0, 0, 0, 0, 0
	clickhouseStarted := time.Now()
	for _, key := range groupKeys {
		group := groups[key]
		sourceCount += len(group.dataset.Sources)
		jobs := make([]dfmaterialization.AggregateJob, 0)
		callIDs := make(map[*aggregateCall][]int)
		nextID := 0
		for _, call := range group.calls {
			converted, convertErr := aggregateJobsForCall(call, nextID)
			if convertErr != nil {
				call.result <- aggregateCallResult{err: convertErr}
				continue
			}
			ids := make([]int, len(converted))
			for index := range converted {
				ids[index] = converted[index].ID
			}
			callIDs[call] = ids
			nextID += len(converted)
			jobs = append(jobs, converted...)
		}
		if len(jobs) == 0 {
			continue
		}
		logicalJobs += len(jobs)
		batch, batchErr := s.reader.ExecuteAggregateBatch(ctx, group.dataset, dfmaterialization.AggregateBatchRequest{
			Jobs: jobs, AccessByProject: group.access,
		})
		deduplicated += batch.DeduplicatedJobs
		statements += batch.Statements
		filterGroups += batch.FilterGroups
		groupingStatements += batch.GroupingStatements
		scalarStatements += batch.ScalarStatements
		if batchErr != nil {
			for call := range callIDs {
				call.result <- aggregateCallResult{err: mapReaderError(batchErr)}
			}
			continue
		}
		byID := make(map[int]dfmaterialization.AggregateJobResult, len(batch.Jobs))
		for _, job := range batch.Jobs {
			byID[job.ID] = job
			resultRows += len(job.Rows)
		}
		for call, ids := range callIDs {
			s.completeAggregateCall(group.dataset, call, ids, byID)
		}
	}
	clickhouseElapsed := time.Since(clickhouseStarted)
	attrs := []any{
		"request_id", httpapi.RequestIDFromContext(ctx),
		"logical_graphql_calls", len(calls), "logical_jobs", logicalJobs,
		"deduplicated_jobs", deduplicated, "selector_groups", len(groups),
		"filter_groups", filterGroups, "grouping_statements", groupingStatements,
		"scalar_statements", scalarStatements, "clickhouse_calls", statements,
		"source_count", sourceCount, "planning_ms", time.Since(started).Milliseconds() - clickhouseElapsed.Milliseconds(),
		"clickhouse_ms", clickhouseElapsed.Milliseconds(), "result_rows", resultRows,
	}
	if statements > 5 {
		s.logger.Warn("dataframe aggregate batch executed", attrs...)
	} else {
		s.logger.Info("dataframe aggregate batch executed", attrs...)
	}
}

func aggregateDatasetKey(dataset dfmaterialization.FederatedDataset) string {
	ids := make([]string, 0, len(dataset.Sources)+len(dataset.ProjectStatuses)+2)
	for _, source := range dataset.Sources {
		ids = append(ids, "source:"+source.ID)
	}
	for _, status := range dataset.ProjectStatuses {
		ids = append(ids, "project:"+status.ProjectID+":"+string(status.State))
	}
	sort.Strings(ids)
	return dataset.Selector.Key() + "\x00" + dataset.Revision + "\x00" + fmt.Sprint(dataset.ExpectedProjects) + "\x00" + strings.Join(ids, "\x1f")
}

func aggregateJobsForCall(call *aggregateCall, firstID int) ([]dfmaterialization.AggregateJob, error) {
	if call.kind == aggregateCallLegacy {
		return []dfmaterialization.AggregateJob{{
			ID: firstID, ResponseMode: dfmaterialization.AggregateResponseLegacy,
			Filters: call.filters, GroupBy: append([]string(nil), call.legacy.GroupBy...),
			Operation: call.legacy.Operation, Column: stringValue(call.legacy.Column),
		}}, nil
	}
	if len(call.rich.Specs) == 0 {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	if len(call.rich.Specs) > 50 {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	jobs := make([]dfmaterialization.AggregateJob, 0, len(call.rich.Specs))
	for _, spec := range call.rich.Specs {
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
		filters := call.filters
		if spec.ExcludeSelfFilter != nil && *spec.ExcludeSelfFilter {
			filters = dfmaterialization.ExcludeSelfFilters(filters, spec.Column)
		}
		job := dfmaterialization.AggregateJob{
			ID: firstID + len(jobs), ResponseMode: mode, Filters: filters,
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

func (s *Service) completeAggregateCall(dataset dfmaterialization.FederatedDataset, call *aggregateCall, ids []int, byID map[int]dfmaterialization.AggregateJobResult) {
	if call.kind == aggregateCallLegacy {
		job := byID[ids[0]]
		if job.Err != nil {
			call.result <- aggregateCallResult{err: mapReaderError(job.Err)}
			return
		}
		call.result <- aggregateCallResult{legacy: dfmaterialization.AggregateResult{
			Materialization: federatedMaterialization(dataset), Columns: job.Columns, Rows: job.Rows,
		}}
		return
	}
	results := make([]dfmaterialization.AggregationResult, 0, len(ids))
	for index, id := range ids {
		job := byID[id]
		if job.Err != nil {
			call.result <- aggregateCallResult{err: fmt.Errorf("aggregation %q: %w", call.rich.Specs[index].Name, mapReaderError(job.Err))}
			return
		}
		spec := call.rich.Specs[index]
		results = append(results, dfmaterialization.AggregationResult{
			Name: spec.Name, Kind: strings.ToUpper(strings.TrimSpace(spec.Kind)),
			Columns: job.Columns, Rows: job.Rows, MissingCount: job.MissingCount, Truncated: job.Truncated,
		})
	}
	call.result <- aggregateCallResult{rich: dfmaterialization.AggregationsResult{
		Dataset: dataset, Aggregations: dfmaterialization.NormalizeAggregationResults(results),
	}}
}
