package published

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

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
		AccessByProject: map[string]SourceAccess{"project": {Unrestricted: true}},
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
		Jobs:            []AggregateJob{{ID: 1, ResponseMode: AggregateResponseLegacy, GroupBy: []string{"facet_000", "facet_001"}, Operation: "COUNT"}},
		AccessByProject: map[string]SourceAccess{"project": {Unrestricted: true}},
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
