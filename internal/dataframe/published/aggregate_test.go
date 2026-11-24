package published

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataset"

	storeclickhouse "github.com/calypr/loom/internal/store/clickhouse"
	"github.com/google/uuid"
)

type aggregateQueryCall struct {
	query string
	args  []any
}

type aggregateFakeQueryer struct {
	calls []aggregateQueryCall
	rows  [][]map[string]any
	errs  []error
}

func (f *aggregateFakeQueryer) QueryRowsArgs(_ context.Context, query string, _ []string, args ...any) ([]map[string]any, error) {
	index := len(f.calls)
	f.calls = append(f.calls, aggregateQueryCall{query: query, args: append([]any(nil), args...)})
	if index < len(f.errs) && f.errs[index] != nil {
		return nil, f.errs[index]
	}
	if index < len(f.rows) {
		return f.rows[index], nil
	}
	return nil, nil
}

func (f *aggregateFakeQueryer) QueryRowsArgsVisit(ctx context.Context, query string, columns []string, visit func(map[string]any) error, args ...any) error {
	rows, err := f.QueryRowsArgs(ctx, query, columns, args...)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := visit(row); err != nil {
			return err
		}
	}
	return nil
}

func aggregateTestDataset(columnCount int) Materialization {
	columns := make([]Column, columnCount)
	for i := range columns {
		columns[i] = Column{Name: fmt.Sprintf("facet_%03d", i), ClickHouse: "Nullable(String)"}
	}
	return Materialization{ID: "source:Patient", Project: "project", PhysicalTable: "physical_patient", Columns: append([]Column(nil), columns...), Selector: dataset.DataframeSelector{Recipe: "recipe", Output: "Patient"}}
}

func TestAggregatePlanCombines156CountFacets(t *testing.T) {
	dataset := aggregateTestDataset(156)
	jobs := make([]AggregateJob, 0, 158)
	for i := 0; i < 156; i++ {
		jobs = append(jobs, AggregateJob{ID: i, ResponseMode: AggregateResponseLegacy, GroupBy: []string{dataset.Columns[i].Name}, Operation: "COUNT"})
	}
	jobs = append(jobs,
		AggregateJob{ID: 156, ResponseMode: AggregateResponseLegacy, Operation: "COUNT"},
		AggregateJob{ID: 157, ResponseMode: AggregateResponseLegacy, Operation: "COUNT"},
	)
	fake := &aggregateFakeQueryer{}
	result, err := (&Reader{ClickHouse: fake}).ExecuteAggregateBatch(context.Background(), dataset, AggregateBatchRequest{
		Jobs: jobs, Unrestricted: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.LogicalJobs != 158 || result.DeduplicatedJobs != 157 || result.Statements != 1 {
		t.Fatalf("batch counts = logical %d deduplicated %d statements %d", result.LogicalJobs, result.DeduplicatedJobs, result.Statements)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("ClickHouse calls = %d, want 1", len(fake.calls))
	}
	query := fake.calls[0].query
	if !strings.Contains(query, "GROUP BY GROUPING SETS") {
		t.Fatalf("query does not use grouping sets: %s", query)
	}
	if !strings.Contains(query, "SETTINGS group_by_use_nulls = 0") {
		t.Fatalf("query does not pin the ClickHouse-compatible null grouping setting")
	}
	if count := strings.Count(query, "physical_patient"); count != 1 {
		t.Fatalf("physical source occurs %d times, want 1", count)
	}
	if !strings.Contains(query, "arrayJoin") {
		t.Fatalf("query does not use the narrow logical facet fan-out")
	}
	if count := strings.Count(query, "GROUPING SETS"); count != 1 {
		t.Fatalf("query has %d physical grouping statements, want 1", count)
	}
	if count := strings.Count(query, "tuple("); count < 156 {
		t.Fatalf("query has %d logical facet tuples, want at least 156", count)
	}
}

func TestAggregatePlanRanksMultipleTermsByProjectedSlot(t *testing.T) {
	dataset := aggregateTestDataset(2)
	fake := &aggregateFakeQueryer{}
	result, err := (&Reader{ClickHouse: fake}).ExecuteAggregateBatch(context.Background(), dataset, AggregateBatchRequest{
		Jobs: []AggregateJob{
			{ID: 1, ResponseMode: AggregateResponseTerms, Column: "facet_000", Size: 10},
			{ID: 2, ResponseMode: AggregateResponseTerms, Column: "facet_001", Size: 10},
		},
		Unrestricted: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.GroupingStatements != 1 || result.ScalarStatements != 1 {
		t.Fatalf("statement counts = grouping %d scalar %d, want 1 and 1", result.GroupingStatements, result.ScalarStatements)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("ClickHouse calls = %d, want 2", len(fake.calls))
	}
	query := fake.calls[0].query
	if !strings.Contains(query, "multiIf(`__loom_slot` = 0, `facet_000`, `__loom_slot` = 1, `facet_001`, NULL) AS `__loom_order_key`") {
		t.Fatalf("terms order key is not selected by projected slot: %s", query)
	}
	if strings.Contains(query, "SELECT *") {
		t.Fatalf("terms ranking uses an implicit projection: %s", query)
	}
	projected := strings.Index(query, "AS __loom_terms_projected")
	if projected < 0 {
		t.Fatalf("terms projection boundary is missing: %s", query)
	}
	if strings.Contains(query[projected:], "__loom_mask_") {
		t.Fatalf("grouping mask escaped its projection scope: %s", query[projected:])
	}
}

func TestAggregatePlanSeparatesRepeatedTermsColumn(t *testing.T) {
	dataset := aggregateTestDataset(1)
	plan := buildAggregatePlan(dataset, AggregateBatchRequest{Jobs: []AggregateJob{
		{ID: 1, ResponseMode: AggregateResponseTerms, Column: "facet_000", Size: 50},
		{ID: 2, ResponseMode: AggregateResponseTerms, Column: "facet_000", Size: 12},
	}})

	groupingStatements := make([]aggregateStatementPlan, 0, 2)
	for _, statement := range plan.statements {
		if statement.kind == statementTerms {
			groupingStatements = append(groupingStatements, statement)
		}
	}
	if len(groupingStatements) != 2 {
		t.Fatalf("terms statements = %d, want 2", len(groupingStatements))
	}
	for index, statement := range groupingStatements {
		if len(statement.jobs) != 1 {
			t.Fatalf("terms statement %d jobs = %d, want 1", index, len(statement.jobs))
		}
	}
}

func TestAggregatePlanCanonicalizesFilterAndMemberOrder(t *testing.T) {
	dataset := aggregateTestDataset(2)
	first := []Filter{{Column: "facet_001", Op: "IN", Value: []any{"b", "a"}}, {Column: "facet_000", Op: "EQ", Value: "x"}}
	second := []Filter{{Column: "facet_000", Op: "eq", Value: "x"}, {Column: "facet_001", Op: "in", Value: []any{"a", "b"}}}
	plan := buildAggregatePlan(dataset, AggregateBatchRequest{Jobs: []AggregateJob{
		{ID: 1, ResponseMode: AggregateResponseLegacy, GroupBy: []string{"facet_000"}, Operation: "COUNT", Filters: first},
		{ID: 2, ResponseMode: AggregateResponseLegacy, GroupBy: []string{"facet_001"}, Operation: "COUNT", Filters: second},
	}})
	if len(plan.statements) != 1 {
		t.Fatalf("statements = %d, want 1", len(plan.statements))
	}
}

func TestAggregatePlanValidationIsJobLocal(t *testing.T) {
	dataset := aggregateTestDataset(1)
	fake := &aggregateFakeQueryer{}
	result, err := (&Reader{ClickHouse: fake}).ExecuteAggregateBatch(context.Background(), dataset, AggregateBatchRequest{
		Jobs: []AggregateJob{
			{ID: 1, ResponseMode: AggregateResponseLegacy, GroupBy: []string{"missing"}, Operation: "COUNT"},
			{ID: 2, ResponseMode: AggregateResponseLegacy, GroupBy: []string{"facet_000"}, Operation: "COUNT"},
		},
		Unrestricted: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Jobs[0].Err == nil || result.Jobs[1].Err != nil {
		t.Fatalf("job errors = %#v, %#v", result.Jobs[0].Err, result.Jobs[1].Err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("ClickHouse calls = %d, want 1", len(fake.calls))
	}
}

func TestAggregateDedupFansOutIndependentRows(t *testing.T) {
	dataset := aggregateTestDataset(1)
	fake := &aggregateFakeQueryer{rows: [][]map[string]any{{
		{"__loom_slot": int64(0), "__loom_group_json": "[\"x\"]", "__loom_metric": int64(2)},
	}}}
	result, err := (&Reader{ClickHouse: fake}).ExecuteAggregateBatch(context.Background(), dataset, AggregateBatchRequest{
		Jobs: []AggregateJob{
			{ID: 1, ResponseMode: AggregateResponseLegacy, GroupBy: []string{"facet_000"}, Operation: "COUNT"},
			{ID: 2, ResponseMode: AggregateResponseLegacy, GroupBy: []string{"facet_000"}, Operation: "COUNT"},
		},
		Unrestricted: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.DeduplicatedJobs != 1 || len(result.Jobs) != 2 {
		t.Fatalf("result = %#v", result)
	}
	result.Jobs[0].Rows[0]["count"] = int64(9)
	if result.Jobs[1].Rows[0]["count"] != int64(2) {
		t.Fatalf("deduplicated aliases share row maps: %#v", result.Jobs)
	}
}

func TestAggregateBatchClickHouseIntegration(t *testing.T) {
	url := os.Getenv("LOOM_CLICKHOUSE_URL")
	if url == "" {
		t.Skip("LOOM_CLICKHOUSE_URL is not set")
	}
	database := os.Getenv("LOOM_CLICKHOUSE_DATABASE")
	if database == "" {
		database = "loom_test"
	}
	client, err := storeclickhouse.New(storeclickhouse.Options{
		URL: url, Database: database,
		Username: os.Getenv("LOOM_CLICKHOUSE_USERNAME"),
		Password: os.Getenv("LOOM_CLICKHOUSE_PASSWORD"),
		Timeout:  10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx := context.Background()
	if err := client.EnsureDatabase(ctx); err != nil {
		t.Fatal(err)
	}
	tableA := "loom_aggregate_a_" + uuid.NewString()[:8]
	defer client.DropTable(ctx, tableA)
	physicalColumns := []storeclickhouse.Column{
		{Name: "__loom_row_id", Type: "UInt64"},
		{Name: "facet", Type: "Nullable(String)"},
		{Name: "facet_other", Type: "Nullable(String)"},
		{Name: "value", Type: "Nullable(Float64)"},
		{Name: "auth_resource_path", Type: "String"},
	}
	if err := client.CreateTable(ctx, tableA, physicalColumns); err != nil {
		t.Fatal(err)
	}
	if err := client.InsertRows(ctx, tableA, physicalColumns, []map[string]any{
		{"__loom_row_id": uint64(1), "facet": "a", "facet_other": "x", "value": 2.0, "auth_resource_path": "allowed"},
		{"__loom_row_id": uint64(2), "facet": nil, "facet_other": "x", "value": 3.0, "auth_resource_path": "allowed"},
		{"__loom_row_id": uint64(3), "facet": "secret", "facet_other": "secret", "value": 100.0, "auth_resource_path": "denied"},
	}); err != nil {
		t.Fatal(err)
	}
	columns := []Column{
		{Name: "__loom_row_id", ClickHouse: "UInt64"},
		{Name: "facet", ClickHouse: "Nullable(String)"},
		{Name: "facet_other", ClickHouse: "Nullable(String)"},
		{Name: "value", ClickHouse: "Nullable(Float64)"},
		{Name: authResourcePathColumn, ClickHouse: "String"},
	}
	dataset := Materialization{ID: "a:Patient", Project: "a", PhysicalTable: tableA, Columns: columns, Selector: dataset.DataframeSelector{Recipe: "recipe", Output: "Patient"}}
	result, err := (&Reader{ClickHouse: client}).ExecuteAggregateBatch(ctx, dataset, AggregateBatchRequest{
		Jobs: []AggregateJob{
			{ID: 1, ResponseMode: AggregateResponseLegacy, GroupBy: []string{"facet"}, Operation: "COUNT"},
			{ID: 2, ResponseMode: AggregateResponseLegacy, Operation: "COUNT"},
			{ID: 3, ResponseMode: AggregateResponseLegacy, Operation: "SUM", Column: "value"},
			{ID: 4, ResponseMode: AggregateResponseTerms, Column: "facet", Size: 2},
			{ID: 5, ResponseMode: AggregateResponseTerms, Column: "facet_other", Size: 1},
		},
		Unrestricted: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range result.Jobs {
		if job.Err != nil {
			t.Fatalf("job %d: %v", job.ID, job.Err)
		}
	}
	if len(result.Jobs[0].Rows) != 3 {
		t.Fatalf("grouped rows = %#v", result.Jobs[0].Rows)
	}
	if got, err := numericCount(result.Jobs[1].Rows[0]["count"]); err != nil || got != 3 {
		t.Fatalf("ungrouped count = %v, err = %v", result.Jobs[1].Rows, err)
	}
	if got := result.Jobs[3]; len(got.Rows) != 2 || got.Truncated || got.MissingCount != 1 {
		t.Fatalf("facet terms = %#v", got)
	}
	if got := result.Jobs[4]; len(got.Rows) != 1 || !got.Truncated || got.MissingCount != 0 || got.Rows[0]["key"] != "x" {
		t.Fatalf("facet_other terms = %#v", got)
	}
}

func TestExecuteAggregateBatchRichShapesShareScans(t *testing.T) {
	dataset := aggregateTestDataset(1)
	fake := &aggregateFakeQueryer{rows: [][]map[string]any{
		{
			{"__loom_slot": int64(0), "__loom_group_json": "[\"a\"]", "__loom_metric": int64(5)},
			{"__loom_slot": int64(0), "__loom_group_json": "[\"b\"]", "__loom_metric": int64(3)},
			{"__loom_slot": int64(0), "__loom_group_json": "[\"c\"]", "__loom_metric": int64(1)},
		},
		{{
			"__loom_0_count": int64(9), "__loom_0_value_count": int64(8), "__loom_0_distinct_count": int64(3),
			"__loom_0_min": "a", "__loom_0_max": "z", "__loom_0_sum": nil, "__loom_0_avg": nil,
			"__loom_1_missing_count": int64(1), "__loom_2_missing_count": int64(4),
		}},
	}}
	result, err := (&Reader{ClickHouse: fake}).ExecuteAggregateBatch(context.Background(), dataset, AggregateBatchRequest{
		Jobs: []AggregateJob{
			{ID: 1, ResponseMode: AggregateResponseTerms, Column: "facet_000", Size: 2},
			{ID: 2, ResponseMode: AggregateResponseStats, Column: "facet_000"},
			{ID: 3, ResponseMode: AggregateResponseMissing, Column: "facet_000"},
		},
		Unrestricted: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Statements != 2 || result.GroupingStatements != 1 || result.ScalarStatements != 1 {
		t.Fatalf("statement counts = %#v", result)
	}
	if len(result.Jobs[0].Rows) != 2 || !result.Jobs[0].Truncated || result.Jobs[0].MissingCount != 4 {
		t.Fatalf("terms result = %#v", result.Jobs[0])
	}
	if result.Jobs[1].Rows[0]["distinct_count"] != int64(3) {
		t.Fatalf("stats result = %#v", result.Jobs[1])
	}
	if result.Jobs[2].Rows[0]["missing_count"] != int64(1) {
		t.Fatalf("missing result = %#v", result.Jobs[2])
	}
	if !strings.Contains(fake.calls[0].query, "row_number() OVER") {
		t.Fatalf("terms statement does not rank in ClickHouse: %s", fake.calls[0].query)
	}
}

func TestExecuteAggregateBatchUsesTypedMultiColumnOrdering(t *testing.T) {
	dataset := aggregateTestDataset(2)
	fake := &aggregateFakeQueryer{rows: [][]map[string]any{{
		{"__loom_slot": int64(0), "__loom_group_json": "[10,\"b\"]", "__loom_metric": int64(1)},
		{"__loom_slot": int64(0), "__loom_group_json": "[2,\"z\"]", "__loom_metric": int64(1)},
		{"__loom_slot": int64(0), "__loom_group_json": "[2,\"a\"]", "__loom_metric": int64(1)},
		{"__loom_slot": int64(0), "__loom_group_json": "[null,\"a\"]", "__loom_metric": int64(1)},
	}}}
	result, err := (&Reader{ClickHouse: fake}).ExecuteAggregateBatch(context.Background(), dataset, AggregateBatchRequest{
		Jobs:         []AggregateJob{{ID: 1, ResponseMode: AggregateResponseLegacy, GroupBy: []string{"facet_000", "facet_001"}, Operation: "COUNT"}},
		Unrestricted: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	rows := result.Jobs[0].Rows
	if rows[0]["facet_000"].(json.Number).String() != "2" || rows[0]["facet_001"] != "a" {
		t.Fatalf("typed ordering = %#v", rows)
	}
	if rows[len(rows)-1]["facet_000"] != nil {
		t.Fatalf("null key did not sort last: %#v", rows)
	}
}
