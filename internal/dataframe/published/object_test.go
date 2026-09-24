package published

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

type objectQueryer struct {
	rows []map[string]any
}

func (q objectQueryer) QueryRowsArgs(context.Context, string, []string, ...any) ([]map[string]any, error) {
	return q.rows, nil
}

func (q objectQueryer) QueryRowsArgsVisit(_ context.Context, _ string, _ []string, visit func(map[string]any) error, _ ...any) error {
	for _, row := range q.rows {
		if err := visit(row); err != nil {
			return err
		}
	}
	return nil
}

func TestPageAndStreamDecodeNewObjectTextWithUseNumber(t *testing.T) {
	materialization := Materialization{
		PhysicalTable: "published_patient",
		Columns: []Column{
			{Name: "specimen_type", LogicalType: "object", ClickHouse: "String", Repeated: true},
		},
	}
	text := `[{"unit":null,"value":9007199254740993},{"value":[]}]`
	rows := []map[string]any{{"__loom_row_id": "1", "__loom_total": int64(1), "specimen_type": text}}
	reader := &Reader{ClickHouse: objectQueryer{rows: rows}}
	page, err := reader.Page(context.Background(), materialization, PageRequest{Unrestricted: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []any{map[string]any{"unit": nil, "value": json.Number("9007199254740993")}, map[string]any{"value": []any{}}}
	if !reflect.DeepEqual(page.Rows[0]["specimen_type"], want) {
		t.Fatalf("page object = %#v, want %#v", page.Rows[0]["specimen_type"], want)
	}
	var streamed []map[string]any
	reader = &Reader{ClickHouse: objectQueryer{rows: []map[string]any{{"__loom_row_id": "1", "specimen_type": text}}}}
	if err := reader.Stream(context.Background(), materialization, StreamRequest{Unrestricted: true}, func(row map[string]any) error {
		streamed = append(streamed, row)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(streamed[0]["specimen_type"], want) {
		t.Fatalf("stream object = %#v, want %#v", streamed[0]["specimen_type"], want)
	}
}

func TestDecodeObjectColumnsPreservesLegacyValuesAndSQLNullAbsence(t *testing.T) {
	row := map[string]any{
		"legacy":       map[string]any{"unit": nil},
		"legacy_null":  nil,
		"legacy_array": []any{map[string]any{"value": nil}},
		"new_null":     "null",
	}
	columns := []Column{
		{Name: "legacy", LogicalType: "object", ClickHouse: "JSON"},
		{Name: "legacy_null", LogicalType: "object", ClickHouse: "Nullable(JSON)"},
		{Name: "legacy_array", LogicalType: "object", ClickHouse: "Array(JSON)"},
		{Name: "new_null", LogicalType: "object", ClickHouse: "Nullable(String)"},
	}
	if err := decodeObjectColumns(columns, row); err != nil {
		t.Fatal(err)
	}
	if _, ok := row["legacy_null"]; ok {
		t.Fatal("SQL-null legacy value was retained")
	}
	if _, ok := row["legacy_array"]; !ok {
		t.Fatal("legacy Array(JSON) value was removed")
	}
	if value, ok := row["new_null"]; !ok || value != nil {
		t.Fatalf("encoded JSON null = %#v/%v, want present nil", value, ok)
	}
}

func TestDecodeObjectColumnsRejectsUnknownPhysicalType(t *testing.T) {
	row := map[string]any{"value": "{}"}
	if err := decodeObjectColumns([]Column{{Name: "value", LogicalType: "object", ClickHouse: "Array(String)"}}, row); err == nil {
		t.Fatal("accepted unsupported object physical type")
	}
	reader := &Reader{ClickHouse: objectQueryer{}}
	_, err := reader.Page(context.Background(), Materialization{
		PhysicalTable: "published_patient",
		Columns:       []Column{{Name: "value", LogicalType: "object", ClickHouse: "Array(String)"}},
	}, PageRequest{Unrestricted: true})
	if err == nil {
		t.Fatal("accepted unsupported object physical type without rows")
	}
}

func TestObjectColumnsAreSelectableButNotFilterableOrSortable(t *testing.T) {
	source := []Column{
		{Name: "value", LogicalType: "object", ClickHouse: "String"},
		{Name: "name", LogicalType: "string", ClickHouse: "String"},
	}
	selected, allowed, err := readerColumns(source, []string{"value"}, nil)
	if err != nil || !reflect.DeepEqual(selected, []string{"value"}) {
		t.Fatalf("object selection = %#v/%v", selected, err)
	}
	if _, ok := allowed["value"]; ok {
		t.Fatal("object column is filterable")
	}
	if _, _, err := readerColumns(source, nil, &Sort{Column: "value"}); err == nil {
		t.Fatal("object column is sortable")
	}
}

func TestObjectColumnsAreRejectedByAggregatePlanning(t *testing.T) {
	dataset := Materialization{Columns: []Column{
		{Name: "value", LogicalType: "object", ClickHouse: "String"},
		{Name: "name", LogicalType: "string", ClickHouse: "String"},
	}}
	plan := buildAggregatePlan(dataset, AggregateBatchRequest{Jobs: []AggregateJob{
		{ID: 1, ResponseMode: AggregateResponseTerms, Column: "value"},
		{ID: 2, ResponseMode: AggregateResponseLegacy, Operation: "COUNT", Filters: []Filter{{Column: "value", Op: "EXISTS"}}},
		{ID: 3, ResponseMode: AggregateResponseLegacy, Operation: "COUNT", GroupBy: []string{"value"}},
	}})
	for _, id := range []int{1, 2, 3} {
		result, ok := plan.results[id]
		if !ok || result.Err == nil {
			t.Fatalf("aggregate job %d = %#v, want an object capability error", id, result)
		}
	}
}
