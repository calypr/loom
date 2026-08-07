package runtime

import (
	"context"
	"reflect"
	"testing"
)

func TestFlattenPivotFieldsCreatesStableFlattenedKeys(t *testing.T) {
	row := flattenPivotFields(map[string]any{
		"pivot": map[string]any{
			"zeta":  "z",
			"alpha": "a",
		},
		"keep": "value",
	}, []string{"pivot"})

	if _, ok := row["pivot"]; ok {
		t.Fatalf("pivot object was not removed: %#v", row)
	}
	want := map[string]any{
		"pivot__alpha": "a",
		"pivot__zeta":  "z",
		"keep":         "value",
	}
	if !reflect.DeepEqual(row, want) {
		t.Fatalf("flattenPivotFields() = %#v, want %#v", row, want)
	}
}

func TestRunQueryAppendsDynamicColumnsInStableOrder(t *testing.T) {
	svc := NewService(ServiceConfig{
		QueryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
			for _, row := range []map[string]any{
				{"_key": "first", "pivot": map[string]any{"zeta": "z", "alpha": "a"}},
				{"_key": "second", "pivot": map[string]any{"beta": "b"}},
			} {
				if err := visit(row); err != nil {
					return err
				}
			}
			return nil
		},
	})

	result, err := svc.runQuery(context.Background(), CompiledQuery{
		Query:       "RETURN []",
		Columns:     []string{"_key", "pivot"},
		PivotFields: []string{"pivot"},
		Limit:       2,
	})
	if err != nil {
		t.Fatalf("runQuery() error = %v", err)
	}
	wantColumns := []string{"_key", "pivot__alpha", "pivot__beta", "pivot__zeta"}
	if !reflect.DeepEqual(result.Columns, wantColumns) {
		t.Fatalf("Columns = %#v, want %#v", result.Columns, wantColumns)
	}
	if result.RowCount != 2 {
		t.Fatalf("RowCount = %d, want 2", result.RowCount)
	}
}

func TestStreamQueryDeliversFlattenedRowsWithoutCollectingResultRows(t *testing.T) {
	svc := NewService(ServiceConfig{
		QueryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
			for _, row := range []map[string]any{
				{"_key": "first", "pivot": map[string]any{"zeta": "z", "alpha": "a"}},
				{"_key": "second", "pivot": map[string]any{"beta": "b"}},
			} {
				if err := visit(row); err != nil {
					return err
				}
			}
			return nil
		},
	})

	gotRows := []map[string]any{}
	result, err := svc.streamQuery(context.Background(), CompiledQuery{
		Query:       "RETURN []",
		Columns:     []string{"_key", "pivot"},
		PivotFields: []string{"pivot"},
		Limit:       2,
	}, func(row map[string]any) error {
		gotRows = append(gotRows, row)
		return nil
	})
	if err != nil {
		t.Fatalf("streamQuery() error = %v", err)
	}
	if result.RowCount != 2 {
		t.Fatalf("RowCount = %d, want 2", result.RowCount)
	}
	wantColumns := []string{"_key", "pivot__alpha", "pivot__beta", "pivot__zeta"}
	if !reflect.DeepEqual(result.Columns, wantColumns) {
		t.Fatalf("Columns = %#v, want %#v", result.Columns, wantColumns)
	}
	wantRows := []map[string]any{
		{"_key": "first", "pivot__alpha": "a", "pivot__zeta": "z"},
		{"_key": "second", "pivot__beta": "b"},
	}
	if !reflect.DeepEqual(gotRows, wantRows) {
		t.Fatalf("streamed rows = %#v, want %#v", gotRows, wantRows)
	}
}

func TestStreamQueryReturnsPartialProgressWhenVisitorStops(t *testing.T) {
	svc := NewService(ServiceConfig{
		QueryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
			if err := visit(map[string]any{"_key": "first"}); err != nil {
				return err
			}
			return visit(map[string]any{"_key": "second"})
		},
	})
	stop := assertError("stop")
	result, err := svc.streamQuery(context.Background(), CompiledQuery{Query: "RETURN []", Columns: []string{"_key"}}, func(map[string]any) error {
		return stop
	})
	if err != stop {
		t.Fatalf("streamQuery() error = %v, want visitor error %v", err, stop)
	}
	if result.RowCount != 0 {
		t.Fatalf("RowCount = %d, want 0 because the visitor rejected the first row", result.RowCount)
	}
}

type assertError string

func (e assertError) Error() string { return string(e) }
