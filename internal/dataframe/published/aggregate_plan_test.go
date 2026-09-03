package published

import (
	"context"
	"fmt"
	"strings"
	"testing"
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
	return Materialization{ID: "source:Patient", Project: "project", PhysicalTable: "physical_patient", Columns: append([]Column(nil), columns...), Selector: DataframeSelector{Recipe: "recipe", Output: "Patient"}}
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
