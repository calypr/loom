package published

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

func (r *Reader) ExecuteAggregateBatch(ctx context.Context, dataset Materialization, req AggregateBatchRequest) (AggregateBatchResult, error) {
	result := AggregateBatchResult{LogicalJobs: len(req.Jobs)}
	if r == nil || r.ClickHouse == nil {
		return result, dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	seenIDs := make(map[int]struct{}, len(req.Jobs))
	for _, job := range req.Jobs {
		if _, exists := seenIDs[job.ID]; exists {
			return result, fmt.Errorf("aggregate planner invariant: duplicate job ID %d", job.ID)
		}
		seenIDs[job.ID] = struct{}{}
	}
	plan := buildAggregatePlan(dataset, req)
	result.DeduplicatedJobs = len(plan.jobs)
	filterGroups := make(map[string]struct{})
	for _, statement := range plan.statements {
		if len(statement.jobs) > 0 {
			filterGroups[statement.jobs[0].filterKey] = struct{}{}
		}
	}
	result.FilterGroups = len(filterGroups)
	if r.Logger != nil {
		r.Logger.Debug("dataframe aggregate plan built",
			"selector", dataset.Selector.Key(),
			"source_count", 1,
			"logical_jobs", result.LogicalJobs,
			"valid_jobs", len(plan.jobs),
			"deduplicated_jobs", result.DeduplicatedJobs,
			"statement_count", len(plan.statements),
			"filter_groups", result.FilterGroups,
		)
	}
	for _, statement := range plan.statements {
		if err := ctx.Err(); err != nil {
			return assembleAggregateBatchResult(req.Jobs, plan, result), err
		}
		sql, err := buildAggregateSQL(dataset, req, statement)
		if err != nil {
			setStatementError(plan.results, statement, err)
			continue
		}
		result.Statements++
		if statement.kind == statementScalar {
			result.ScalarStatements++
		} else {
			result.GroupingStatements++
		}
		statementStarted := time.Now()
		if r.Logger != nil {
			r.Logger.Debug("dataframe aggregate statement started",
				"selector", dataset.Selector.Key(),
				"statement_kind", string(statement.kind),
				"job_count", len(statement.jobs),
				"grouping_sets", sql.groupingSets,
				"source_count", 1,
				"source_column_count", len(sql.sourceColumns),
				"query_id", shortQueryID(sql.query),
				"query_bytes", len(sql.query),
				"bind_count", len(sql.args),
			)
		}
		rows, err := r.ClickHouse.QueryRowsArgs(ctx, sql.query, sql.columns, sql.args...)
		if err != nil {
			if r.Logger != nil {
				r.Logger.Error("clickhouse aggregate failed",
					"selector", dataset.Selector.Key(),
					"statement_kind", string(statement.kind),
					"job_count", len(statement.jobs),
					"grouping_sets", sql.groupingSets,
					"source_count", 1,
					"source_column_count", len(sql.sourceColumns),
					"query_id", shortQueryID(sql.query),
					"query_bytes", len(sql.query),
					"bind_count", len(sql.args),
					"duration_ms", time.Since(statementStarted).Milliseconds(),
					"failure_class", aggregateFailureClass(err),
					"error", err,
				)
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
				if ctx.Err() != nil {
					return assembleAggregateBatchResult(req.Jobs, plan, result), ctx.Err()
				}
				return assembleAggregateBatchResult(req.Jobs, plan, result), err
			}
			setStatementError(plan.results, statement, backendCallError(err))
			continue
		}
		if err := decodeAggregateStatement(plan.results, statement, rows); err != nil {
			setStatementError(plan.results, statement, err)
			if r.Logger != nil {
				r.Logger.Error("dataframe aggregate statement decode failed",
					"selector", dataset.Selector.Key(),
					"statement_kind", string(statement.kind),
					"job_count", len(statement.jobs),
					"query_id", shortQueryID(sql.query),
					"duration_ms", time.Since(statementStarted).Milliseconds(),
					"result_rows", len(rows),
					"error", err,
				)
			}
		} else if r.Logger != nil {
			r.Logger.Debug("dataframe aggregate statement completed",
				"selector", dataset.Selector.Key(),
				"statement_kind", string(statement.kind),
				"job_count", len(statement.jobs),
				"grouping_sets", sql.groupingSets,
				"query_id", shortQueryID(sql.query),
				"duration_ms", time.Since(statementStarted).Milliseconds(),
				"result_rows", len(rows),
			)
		}
	}
	return assembleAggregateBatchResult(req.Jobs, plan, result), nil
}

func aggregateFailureClass(err error) string {
	if err == nil {
		return ""
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "memory limit exceeded"), strings.Contains(message, "overcommittracker"):
		return "memory_limit"
	case strings.Contains(message, "timeout"), strings.Contains(message, "deadline exceeded"):
		return "timeout"
	case strings.Contains(message, "cancel"):
		return "canceled"
	default:
		return "clickhouse"
	}
}

func setStatementError(results map[int]AggregateJobResult, statement aggregateStatementPlan, err error) {
	for _, job := range statement.jobs {
		for _, id := range job.aliases {
			results[id] = AggregateJobResult{ID: id, Err: err}
		}
	}
}

func decodeAggregateStatement(results map[int]AggregateJobResult, statement aggregateStatementPlan, rows []map[string]any) error {
	if statement.kind == statementScalar {
		return decodeScalarStatement(results, statement, rows)
	}
	return decodeGroupedStatement(results, statement, rows)
}

func decodeGroupedStatement(results map[int]AggregateJobResult, statement aggregateStatementPlan, rows []map[string]any) error {
	bySlot := make([][]map[string]any, len(statement.jobs))
	missing := make([]int64, len(statement.jobs))
	for _, row := range rows {
		slot64, err := numericCount(row["__loom_slot"])
		if err != nil || slot64 < 0 || int(slot64) >= len(statement.jobs) {
			return fmt.Errorf("ClickHouse aggregate returned invalid slot %v", row["__loom_slot"])
		}
		slot := int(slot64)
		keys, err := decodeGroupJSON(row["__loom_group_json"])
		if err != nil {
			return err
		}
		planned := statement.jobs[slot]
		switch statement.kind {
		case statementTerms:
			if len(keys) != 1 {
				return fmt.Errorf("ClickHouse aggregate returned %d group keys, want 1", len(keys))
			}
			key := keys[0]
			if key == nil {
				count, countErr := numericCount(row["__loom_metric"])
				if countErr != nil {
					return countErr
				}
				missing[slot] += count
				continue
			}
			bySlot[slot] = append(bySlot[slot], map[string]any{"key": key, "doc_count": row["__loom_metric"]})
		case statementBucket:
			if len(keys) != 1 {
				return fmt.Errorf("ClickHouse aggregate returned %d group keys, want 1", len(keys))
			}
			bySlot[slot] = append(bySlot[slot], map[string]any{"key": keys[0], "doc_count": row["__loom_metric"]})
		default:
			if len(keys) != len(planned.job.GroupBy) {
				return fmt.Errorf("ClickHouse aggregate returned %d group keys, want %d", len(keys), len(planned.job.GroupBy))
			}
			value := make(map[string]any, len(planned.job.GroupBy)+1)
			for index, column := range planned.job.GroupBy {
				value[column] = keys[index]
			}
			value[legacyMetricName(planned.job.Operation)] = row["__loom_metric"]
			bySlot[slot] = append(bySlot[slot], value)
		}
	}
	for slot, planned := range statement.jobs {
		jobRows := bySlot[slot]
		columns := append([]string(nil), planned.job.GroupBy...)
		truncated := false
		switch statement.kind {
		case statementTerms:
			columns = []string{"key", "doc_count"}
			sort.SliceStable(jobRows, func(i, j int) bool {
				left, _ := numericCount(jobRows[i]["doc_count"])
				right, _ := numericCount(jobRows[j]["doc_count"])
				if left != right {
					return left > right
				}
				return compareAggregateValues(jobRows[i]["key"], jobRows[j]["key"]) < 0
			})
			truncated = len(jobRows) > planned.job.Size
			if truncated {
				jobRows = jobRows[:planned.job.Size]
			}
		case statementBucket:
			columns = []string{"key", "doc_count"}
			sort.SliceStable(jobRows, func(i, j int) bool {
				return compareAggregateValues(jobRows[i]["key"], jobRows[j]["key"]) < 0
			})
			truncated = len(jobRows) >= planned.job.Size
		default:
			columns = append(columns, legacyMetricName(planned.job.Operation))
			sort.SliceStable(jobRows, func(i, j int) bool {
				for _, column := range planned.job.GroupBy {
					if compared := compareAggregateValues(jobRows[i][column], jobRows[j][column]); compared != 0 {
						return compared < 0
					}
				}
				return false
			})
		}
		fanoutJobResult(results, planned, AggregateJobResult{
			ID: planned.job.ID, Columns: columns, Rows: jobRows,
			MissingCount: missing[slot], Truncated: truncated,
		})
	}
	return nil
}

func decodeScalarStatement(results map[int]AggregateJobResult, statement aggregateStatementPlan, rows []map[string]any) error {
	row := map[string]any{}
	if len(rows) > 0 {
		row = rows[0]
	}
	for slot, planned := range statement.jobs {
		if planned.job.ResponseMode == AggregateResponseStats {
			columns := []string{"count", "value_count", "distinct_count", "min", "max", "sum", "avg"}
			output := make(map[string]any, len(columns))
			for _, column := range columns {
				output[column] = row[fmt.Sprintf("__loom_%d_%s", slot, column)]
			}
			fanoutJobResult(results, planned, AggregateJobResult{ID: planned.job.ID, Columns: columns, Rows: []map[string]any{output}})
			continue
		}
		value := row[fmt.Sprintf("__loom_%d_missing_count", slot)]
		if value == nil {
			value = int64(0)
		}
		if planned.job.ResponseMode == AggregateResponseTerms {
			missing, err := numericCount(value)
			if err != nil {
				return err
			}
			for _, id := range planned.aliases {
				result := results[id]
				result.MissingCount = missing
				results[id] = result
			}
			continue
		}
		fanoutJobResult(results, planned, AggregateJobResult{ID: planned.job.ID, Columns: []string{"missing_count"}, Rows: []map[string]any{{"missing_count": value}}})
	}
	return nil
}

func fanoutJobResult(results map[int]AggregateJobResult, job *plannedAggregateJob, base AggregateJobResult) {
	for _, id := range job.aliases {
		copyResult := base
		copyResult.ID = id
		copyResult.Columns = append([]string(nil), base.Columns...)
		copyResult.Rows = cloneAggregateRows(base.Rows)
		results[id] = copyResult
	}
}

func assembleAggregateBatchResult(inputs []AggregateJob, plan aggregatePlan, result AggregateBatchResult) AggregateBatchResult {
	result.Jobs = make([]AggregateJobResult, 0, len(inputs))
	for _, input := range inputs {
		value, ok := plan.results[input.ID]
		if !ok {
			value = AggregateJobResult{ID: input.ID, Err: fmt.Errorf("aggregate job did not produce a result")}
		}
		result.Jobs = append(result.Jobs, value)
	}
	return result
}

func cloneAggregateRows(rows []map[string]any) []map[string]any {
	result := make([]map[string]any, len(rows))
	for i, row := range rows {
		result[i] = make(map[string]any, len(row))
		for key, value := range row {
			result[i][key] = value
		}
	}
	return result
}

func legacyMetricName(operation string) string {
	if operation == "" {
		return "count"
	}
	return strings.ToLower(operation)
}

func decodeGroupJSON(value any) ([]any, error) {
	if values, ok := value.([]any); ok {
		return values, nil
	}
	var data []byte
	switch typed := value.(type) {
	case string:
		data = []byte(typed)
	case []byte:
		data = typed
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		data = encoded
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var result []any
	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("decode aggregate group key: %w", err)
	}
	return result, nil
}

func compareAggregateValues(left, right any) int {
	if left == nil && right == nil {
		return 0
	}
	if left == nil {
		return 1
	}
	if right == nil {
		return -1
	}
	if lnum, ok := aggregateNumber(left); ok {
		if rnum, ok := aggregateNumber(right); ok {
			return lnum.Cmp(rnum)
		}
	}
	if lbool, ok := left.(bool); ok {
		if rbool, ok := right.(bool); ok {
			if lbool == rbool {
				return 0
			}
			if !lbool {
				return -1
			}
			return 1
		}
	}
	if lstr, ok := left.(string); ok {
		if rstr, ok := right.(string); ok {
			return strings.Compare(lstr, rstr)
		}
	}
	if larray, ok := left.([]any); ok {
		if rarray, ok := right.([]any); ok {
			for i := 0; i < len(larray) && i < len(rarray); i++ {
				if compared := compareAggregateValues(larray[i], rarray[i]); compared != 0 {
					return compared
				}
			}
			if len(larray) < len(rarray) {
				return -1
			}
			if len(larray) > len(rarray) {
				return 1
			}
			return 0
		}
	}
	ljson, _ := json.Marshal(left)
	rjson, _ := json.Marshal(right)
	return bytes.Compare(ljson, rjson)
}

func aggregateNumber(value any) (*big.Rat, bool) {
	var text string
	switch typed := value.(type) {
	case json.Number:
		text = string(typed)
	case int:
		text = strconv.Itoa(typed)
	case int8:
		text = strconv.FormatInt(int64(typed), 10)
	case int16:
		text = strconv.FormatInt(int64(typed), 10)
	case int32:
		text = strconv.FormatInt(int64(typed), 10)
	case int64:
		text = strconv.FormatInt(typed, 10)
	case uint:
		text = strconv.FormatUint(uint64(typed), 10)
	case uint8:
		text = strconv.FormatUint(uint64(typed), 10)
	case uint16:
		text = strconv.FormatUint(uint64(typed), 10)
	case uint32:
		text = strconv.FormatUint(uint64(typed), 10)
	case uint64:
		text = strconv.FormatUint(typed, 10)
	case float32:
		text = strconv.FormatFloat(float64(typed), 'g', -1, 32)
	case float64:
		text = strconv.FormatFloat(typed, 'g', -1, 64)
	default:
		return nil, false
	}
	valueRat, ok := new(big.Rat).SetString(text)
	return valueRat, ok
}
