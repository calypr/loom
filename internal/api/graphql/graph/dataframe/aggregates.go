package dataframe

import (
	"context"
	"encoding/json"
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

func (s *Service) AggregateInput(ctx context.Context, input model.DataframeAggregateInput) (dfmaterialization.AggregateResult, error) {
	selector, err := resolveSelector(input.Selector)
	if err != nil {
		return dfmaterialization.AggregateResult{}, err
	}
	call := &aggregateCall{project: canonicalProjectID(input.ProjectID), selector: selector, filters: convertFilters(input.Filters), legacy: input}
	result := s.submitAggregateCall(ctx, call)
	if result.err != nil {
		s.logReadFailure(ctx, "clickhouse_aggregate", selector.Output, result.err, "project", input.ProjectID)
	}
	return result.legacy, result.err
}

func (s *Service) AggregationsInput(ctx context.Context, input model.DataframeAggregationsInput) (dfmaterialization.AggregationsResult, error) {
	selector, err := resolveSelector(input.Selector)
	if err != nil {
		return dfmaterialization.AggregationsResult{}, err
	}
	call := &aggregateCall{project: canonicalProjectID(input.ProjectID), selector: selector, filters: convertFilters(input.Filters), rich: input, kind: aggregateCallRich}
	result := s.submitAggregateCall(ctx, call)
	if result.err != nil {
		s.logReadFailure(ctx, "clickhouse_aggregations", selector.Output, result.err, "project", input.ProjectID)
	}
	return result.rich, result.err
}

func AggregationsJSON(result dfmaterialization.AggregationsResult) (json.RawMessage, error) {
	return json.Marshal(dfmaterialization.NormalizeAggregationResults(result.Aggregations))
}

// GraphQL launches sibling resolvers concurrently, but a large selection set
// can take several milliseconds to enqueue its final aggregate call. The
// fallback exists only for directive-skipped fields where expectedCalls cannot
// be reached; normal operations dispatch immediately at the exact count.
const aggregateFallbackDelay = 10 * time.Millisecond

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
	datasets      map[string]*projectDatasetCacheEntry

	mu sync.Mutex
}

type projectDatasetCacheEntry struct {
	ready  chan struct{}
	value  dfmaterialization.Materialization
	access projectAccess
	err    error
}

type aggregateCallKind uint8

const (
	aggregateCallLegacy aggregateCallKind = iota
	aggregateCallRich
)

type aggregateCall struct {
	kind     aggregateCallKind
	project  string
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
		service: s, expectedCalls: expectedCalls, datasets: make(map[string]*projectDatasetCacheEntry),
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

func (s *aggregateOperationState) projectDataset(
	ctx context.Context,
	project string,
	selector dfmaterialization.DataframeSelector,
	resolve func() (dfmaterialization.Materialization, projectAccess, error),
) (dfmaterialization.Materialization, projectAccess, error) {
	key := canonicalProjectID(project) + "\x00" + selector.Key()
	s.mu.Lock()
	if s.datasets == nil {
		s.datasets = make(map[string]*projectDatasetCacheEntry)
	}
	if cached := s.datasets[key]; cached != nil {
		s.mu.Unlock()
		select {
		case <-cached.ready:
			return cached.value, cached.access, cached.err
		case <-ctx.Done():
			return dfmaterialization.Materialization{}, projectAccess{}, ctx.Err()
		}
	}
	entry := &projectDatasetCacheEntry{ready: make(chan struct{})}
	s.datasets[key] = entry
	s.mu.Unlock()

	entry.value, entry.access, entry.err = resolve()
	close(entry.ready)
	return entry.value, entry.access, entry.err
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
	materialization dfmaterialization.Materialization
	access          projectAccess
	calls           []*aggregateCall
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
	resolved := make(map[string]*aggregateExecutionGroup)
	resolutionErrors := make(map[string]error)
	groupKeys := make([]string, 0)
	for _, call := range calls {
		requestKey := canonicalProjectID(call.project) + "\x00" + call.selector.Key()
		if group := resolved[requestKey]; group != nil {
			group.calls = append(group.calls, call)
			continue
		}
		if resolveErr := resolutionErrors[requestKey]; resolveErr != nil {
			call.result <- aggregateCallResult{err: mapReaderError(resolveErr)}
			continue
		}
		materialization, access, resolveErr := s.currentProjectDatasetForPrincipal(ctx, principal, call.project, call.selector)
		if resolveErr != nil {
			resolutionErrors[requestKey] = resolveErr
			call.result <- aggregateCallResult{err: mapReaderError(resolveErr)}
			continue
		}
		key := materialization.ID
		group := groups[key]
		if group == nil {
			group = &aggregateExecutionGroup{materialization: materialization, access: access}
			groups[key] = group
			groupKeys = append(groupKeys, key)
		}
		resolved[requestKey] = group
		group.calls = append(group.calls, call)
	}
	sort.Strings(groupKeys)

	logicalJobs, deduplicated, statements, filterGroups, groupingStatements, scalarStatements, resultRows, sourceCount := 0, 0, 0, 0, 0, 0, 0, 0
	clickhouseStarted := time.Now()
	for _, key := range groupKeys {
		group := groups[key]
		sourceCount++
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
		batch, batchErr := s.reader.ExecuteAggregateBatch(ctx, group.materialization, dfmaterialization.AggregateBatchRequest{
			Jobs: jobs, AuthResourcePaths: group.access.authResourcePaths, Unrestricted: group.access.unrestricted,
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
			s.completeAggregateCall(group.materialization, call, ids, byID)
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

func (s *Service) completeAggregateCall(materialization dfmaterialization.Materialization, call *aggregateCall, ids []int, byID map[int]dfmaterialization.AggregateJobResult) {
	if call.kind == aggregateCallLegacy {
		job := byID[ids[0]]
		if job.Err != nil {
			call.result <- aggregateCallResult{err: mapReaderError(job.Err)}
			return
		}
		call.result <- aggregateCallResult{legacy: dfmaterialization.AggregateResult{
			Materialization: materialization, Columns: job.Columns, Rows: job.Rows,
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
		Materialization: materialization, Aggregations: dfmaterialization.NormalizeAggregationResults(results),
	}}
}
