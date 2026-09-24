package execution

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestMergeShardedRowsPreservesStatesAndAuthorization(t *testing.T) {
	rows, err := mergeShardedRows([][]map[string]any{
		{
			{"__loom_row_id": "r1", "false_value": false, "zero_value": 0, "empty_value": "", "explicit_null": nil, "auth_resource_path": "/a"},
			{"__loom_row_id": "r2", "false_value": true, "zero_value": 1, "empty_value": "x", "auth_resource_path": "/b"},
		},
		{
			{"__loom_row_id": "r1", "missing_value": "present", "__loom_dynamic_runtime_keys": map[string]any{"family_a": []any{"a"}}},
			{"__loom_row_id": "r2", "missing_value": nil, "__loom_dynamic_runtime_keys": map[string]any{"family_b": []any{"b"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{
		{"__loom_row_id": "r1", "false_value": false, "zero_value": 0, "empty_value": "", "explicit_null": nil, "auth_resource_path": "/a", "missing_value": "present", "__loom_dynamic_runtime_keys": map[string]any{"family_a": []any{"a"}}},
		{"__loom_row_id": "r2", "false_value": true, "zero_value": 1, "empty_value": "x", "auth_resource_path": "/b", "missing_value": nil, "__loom_dynamic_runtime_keys": map[string]any{"family_b": []any{"b"}}},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("merged rows = %#v, want %#v", rows, want)
	}
	public := publicStreamRow(rows[0], []string{"false_value", "zero_value", "empty_value", "explicit_null", "missing_value"})
	if _, ok := public["missing_field"]; ok {
		t.Fatal("missing field was materialized")
	}
	if public["explicit_null"] != nil || public["false_value"] != false || public["zero_value"] != 0 || public["empty_value"] != "" {
		t.Fatalf("public row lost null/false/zero/empty states: %#v", public)
	}
	if public["auth_resource_path"] != "/a" {
		t.Fatalf("authorization path = %#v, want /a", public["auth_resource_path"])
	}
}

func TestMergeShardedRowsRejectsIdentityAndOrderMismatch(t *testing.T) {
	cases := []struct {
		name string
		rows [][]map[string]any
		want string
	}{
		{name: "missing", rows: [][]map[string]any{{{"value": 1}}, {{"__loom_row_id": "r1"}}}, want: "missing __loom_row_id"},
		{name: "order", rows: [][]map[string]any{{{"__loom_row_id": "r1"}, {"__loom_row_id": "r2"}}, {{"__loom_row_id": "r2"}, {"__loom_row_id": "r1"}}}, want: "identity/order mismatch"},
		{name: "duplicate_first", rows: [][]map[string]any{{{"__loom_row_id": "r1"}, {"__loom_row_id": "r1"}}, {{"__loom_row_id": "r1"}, {"__loom_row_id": "r1"}}}, want: "shard 1 returned duplicate"},
		{name: "duplicate_later", rows: [][]map[string]any{{{"__loom_row_id": "r1"}, {"__loom_row_id": "r2"}}, {{"__loom_row_id": "r1"}, {"__loom_row_id": "r1"}}}, want: "shard 2 returned duplicate"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := mergeShardedRows(test.rows); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("merge error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestShardedStreamUsesOneRootPageForEveryShard(t *testing.T) {
	var pageKeys [][]string
	rowCalls := make(map[string]int)
	var rowCallsMu sync.Mutex
	queryRows := func(_ context.Context, query string, _ int, binds map[string]any, visit func(map[string]any) error) error {
		switch {
		case query == "keys":
			after, _ := binds[compiler.RootPageAfterKeyBind].(string)
			keys := []string{"a", "b"}
			if after != "" {
				keys = []string{"c"}
			}
			pageKeys = append(pageKeys, append([]string(nil), keys...))
			for _, key := range keys {
				if err := visit(map[string]any{"_key": key}); err != nil {
					return err
				}
			}
			return nil
		default:
			rowCallsMu.Lock()
			rowCalls[query]++
			rowCallsMu.Unlock()
			keys, _ := binds[compiler.RootPageKeysBind].([]string)
			for _, key := range keys {
				if err := visit(map[string]any{"__loom_row_id": key, query: key}); err != nil {
					return err
				}
			}
			return nil
		}
	}
	page := &compiler.CompiledOutputPage{RootKeysQuery: "keys", RootKeysBindVars: map[string]any{}, RowsQuery: "rows", RowsBindVars: map[string]any{}}
	stream := OutputStream{Name: "wide", rootPageRows: 2, batchSize: 10, stream: queryRows, shards: []outputStreamShard{
		{query: "rows-1", publicColumns: []string{"first"}, page: page},
		{query: "rows-2", publicColumns: []string{"second"}, page: &compiler.CompiledOutputPage{RootKeysQuery: "keys", RootKeysBindVars: map[string]any{}, RowsQuery: "rows-2", RowsBindVars: map[string]any{}}},
	}}
	var got []map[string]any
	if err := stream.streamRaw(context.Background(), func(row map[string]any) error {
		got = append(got, row)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pageKeys, [][]string{{"a", "b"}, {"c"}}) {
		t.Fatalf("root pages = %#v, want two shared pages", pageKeys)
	}
	if !reflect.DeepEqual(rowCalls, map[string]int{"rows": 2, "rows-2": 2}) {
		t.Fatalf("row queries = %#v, want every page on every shard", rowCalls)
	}
	if len(got) != 3 || got[0]["__loom_row_id"] != "a" || got[2]["__loom_row_id"] != "c" {
		t.Fatalf("merged logical rows = %#v", got)
	}
}

func TestShardedStreamExecutesIndependentQueriesConcurrentlyAndMergesInOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	secondStarted := make(chan struct{})
	queryRows := func(ctx context.Context, query string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		if query == "first" {
			select {
			case <-secondStarted:
			case <-ctx.Done():
				return ctx.Err()
			}
			return visit(map[string]any{"__loom_row_id": "row-1", "first": 1})
		}
		close(secondStarted)
		return visit(map[string]any{"__loom_row_id": "row-1", "second": 2})
	}
	stream := OutputStream{Name: "wide", batchSize: 10, stream: queryRows, shards: []outputStreamShard{
		{query: "first", publicColumns: []string{"first"}},
		{query: "second", publicColumns: []string{"second"}},
	}}
	var rows []map[string]any
	if err := stream.streamRaw(ctx, func(row map[string]any) error { rows = append(rows, row); return nil }); err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{{"__loom_row_id": "row-1", "first": 1, "second": 2}}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("merged rows = %#v, want %#v", rows, want)
	}
}

func TestShardedStreamDerivesDefaultProjectKeyIdentity(t *testing.T) {
	page := 0
	queryRows := func(_ context.Context, query string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		if query == "keys" {
			page++
			if page > 1 {
				return nil
			}
			return visit(map[string]any{"_key": "k1"})
		}
		if query == "rows-1" {
			return visit(map[string]any{"_key": "k1", "first": 1})
		}
		return visit(map[string]any{"_key": "k1", "second": 2})
	}
	identity := &spec.RowIdentity{Grain: spec.RowGrainPatient, Fields: []string{"project", "_key"}}
	stream := OutputStream{
		Name: "wide", RowIdentity: identity, rootPageRows: 1, batchSize: 10, stream: queryRows,
		shards: []outputStreamShard{
			{query: "rows-1", bindVars: map[string]any{"project": "project-a"}, publicColumns: []string{"first"}, page: &compiler.CompiledOutputPage{RootKeysQuery: "keys", RootKeysBindVars: map[string]any{}, RowsQuery: "rows-1", RowsBindVars: map[string]any{"project": "project-a"}}},
			{query: "rows-2", bindVars: map[string]any{"project": "project-a"}, publicColumns: []string{"second"}, page: &compiler.CompiledOutputPage{RootKeysQuery: "keys", RootKeysBindVars: map[string]any{}, RowsQuery: "rows-2", RowsBindVars: map[string]any{"project": "project-a"}}},
		},
	}
	var rows []map[string]any
	if err := stream.streamRaw(context.Background(), func(row map[string]any) error { rows = append(rows, row); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["__loom_row_id"] == nil {
		t.Fatalf("derived identity row = %#v", rows)
	}
}
