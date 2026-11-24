package execution

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
)

func TestPreviewProjectionRowsCombinesCompleteOrderedRows(t *testing.T) {
	parts := []compiler.CompiledOutputPagePartition{{RowsQuery: "first", RowsBindVars: map[string]any{"scope": "a"}}, {RowsQuery: "second", RowsBindVars: map[string]any{"scope": "a"}}}
	keys := []string{"a", "b"}
	query := func(_ context.Context, q string, _ int, binds map[string]any, visit func(map[string]any) error) error {
		if !reflect.DeepEqual(binds[compiler.RootPageKeysBind], keys) || binds["scope"] != "a" {
			t.Fatalf("incorrect bindings: %#v", binds)
		}
		rows := []map[string]any{{"_key": "a", "first": 1}, {"_key": "b", "first": 2}}
		if q == "second" {
			rows = []map[string]any{{"_key": "a", "second": nil}, {"_key": "b", "second": "v"}}
		}
		for _, row := range rows {
			if err := visit(row); err != nil {
				return err
			}
		}
		return nil
	}
	got, err := previewProjectionRows(context.Background(), query, parts, keys, 25)
	want := []map[string]any{{"_key": "a", "first": 1, "second": nil}, {"_key": "b", "first": 2, "second": "v"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("rows=%#v error=%v", got, err)
	}
}

func TestPreviewProjectionRowsRejectsIncompleteOrConflictingParts(t *testing.T) {
	parts := []compiler.CompiledOutputPagePartition{{RowsQuery: "first"}, {RowsQuery: "second"}}
	for _, tc := range []struct {
		name    string
		second  []map[string]any
		failure bool
	}{
		{name: "missing", second: nil},
		{name: "wrong order", second: []map[string]any{{"_key": "b"}, {"_key": "a"}}},
		{name: "duplicate", second: []map[string]any{{"_key": "a"}, {"_key": "a"}}},
		{name: "missing key", second: []map[string]any{{"value": 1}, {"_key": "b"}}},
		{name: "unexpected key", second: []map[string]any{{"_key": "a"}, {"_key": "outside"}}},
		{name: "conflicting value", second: []map[string]any{{"_key": "a", "value": 2}, {"_key": "b"}}},
		{name: "query error", failure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure := errors.New("query failed")
			query := func(_ context.Context, q string, _ int, _ map[string]any, visit func(map[string]any) error) error {
				rows := []map[string]any{{"_key": "a", "value": 1}, {"_key": "b"}}
				if q == "second" {
					if tc.failure {
						return failure
					}
					rows = tc.second
				}
				for _, row := range rows {
					if err := visit(row); err != nil {
						return err
					}
				}
				return nil
			}
			got, err := previewProjectionRows(context.Background(), query, parts, []string{"a", "b"}, 25)
			if err == nil || got != nil {
				t.Fatalf("rows=%#v error=%v; want no partial rows and error", got, err)
			}
			if tc.failure && !errors.Is(err, failure) {
				t.Fatalf("lost query error: %v", err)
			}
		})
	}
}

func TestPreviewProjectionRowsRejectsMissingProjectedColumns(t *testing.T) {
	parts := []compiler.CompiledOutputPagePartition{{RowsQuery: "partial", ProjectionNames: []string{"_key", "value"}}}
	query := func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		return visit(map[string]any{"_key": "a"})
	}
	rows, err := previewProjectionRows(context.Background(), query, parts, []string{"a"}, 25)
	if err == nil || rows != nil {
		t.Fatalf("rows=%v error=%v; want incomplete row rejected", rows, err)
	}
}

func TestPreviewRootPagesUseOneSnapshotAndReturnFullRows(t *testing.T) {
	var calls []string
	snapshot := func(ctx context.Context, run func(context.Context, QueryRows) error) error {
		calls = append(calls, "begin")
		query := func(_ context.Context, name string, _ int, binds map[string]any, visit func(map[string]any) error) error {
			calls = append(calls, name)
			switch name {
			case "keys":
				return visit(map[string]any{"_key": "a"})
			case "first":
				return visit(map[string]any{"_key": "a", "a": 1})
			case "second":
				return visit(map[string]any{"_key": "a", "b": 2})
			default:
				t.Fatalf("unexpected query %q", name)
				return nil
			}
		}
		err := run(ctx, query)
		calls = append(calls, "end")
		return err
	}
	stream := OutputStream{rootPageRows: 2, batchSize: 10, withPreviewReadSnapshot: snapshot, page: &compiler.CompiledOutputPage{
		RootKeysQuery: "keys", RowsQuery: "full", RowsPartitions: []compiler.CompiledOutputPagePartition{{RowsQuery: "first"}, {RowsQuery: "second"}},
	}}
	var rows []map[string]any
	err := stream.streamRaw(context.Background(), func(row map[string]any) error { rows = append(rows, row); return nil })
	if err != nil || !reflect.DeepEqual(calls, []string{"begin", "keys", "first", "second", "end"}) || !reflect.DeepEqual(rows, []map[string]any{{"_key": "a", "a": 1, "b": 2}}) {
		t.Fatalf("calls=%v rows=%v error=%v", calls, rows, err)
	}
}

func TestPreviewRootPagesWithoutSnapshotUseUnsplitQuery(t *testing.T) {
	var calls []string
	stream := OutputStream{rootPageRows: 2, batchSize: 10, page: &compiler.CompiledOutputPage{RootKeysQuery: "keys", RowsQuery: "full", RowsPartitions: []compiler.CompiledOutputPagePartition{{RowsQuery: "part"}}}, stream: func(_ context.Context, q string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		calls = append(calls, q)
		return visit(map[string]any{"_key": "a"})
	}}
	err := stream.streamRaw(context.Background(), func(map[string]any) error { return nil })
	if err != nil || !reflect.DeepEqual(calls, []string{"keys", "full"}) {
		t.Fatalf("calls=%v error=%v", calls, err)
	}
}
