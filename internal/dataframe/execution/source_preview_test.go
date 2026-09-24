package execution

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/spec"
)

type syntheticSourceEvaluator struct{}

func (syntheticSourceEvaluator) EvaluateRow(variables map[string]any) (map[string]any, error) {
	root, ok := variables["root"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("root is missing")
	}
	payload, ok := root["payload"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("payload is missing")
	}
	set, ok := variables["child_set_1"].([]any)
	if !ok {
		return nil, fmt.Errorf("child set is missing")
	}
	return map[string]any{"_key": root["_key"], "id": payload["id"], "child_count": len(set)}, nil
}

func TestSourcePageStreamsOrderedRootsOncePerSource(t *testing.T) {
	var rootPages int
	var sourceQueries atomic.Int32
	queryRows := func(_ context.Context, query string, _ int, binds map[string]any, visit func(map[string]any) error) error {
		if query == "keys" {
			rootPages++
			after, _ := binds[compiler.RootPageAfterKeyBind].(string)
			page := []string{"a", "b", "c"}
			count := 0
			for _, key := range page {
				if key <= after || count == 2 {
					continue
				}
				if err := visit(map[string]any{"_key": key}); err != nil {
					return err
				}
				count++
			}
			return nil
		}
		keys, ok := binds[compiler.RootPageKeysBind].([]string)
		if !ok {
			return fmt.Errorf("missing selected-root keys")
		}
		sourceQueries.Add(1)
		for _, key := range keys {
			value := any(map[string]any{"id": key})
			if query == "child-source" {
				value = []any{map[string]any{"payload": map[string]any{"id": key + "-child"}}}
			} else if query != "root-source" {
				return fmt.Errorf("unexpected query")
			}
			if err := visit(map[string]any{"_key": key, "value": value}); err != nil {
				return err
			}
		}
		return nil
	}
	page := compiler.CompiledOutputSourcePage{
		RootKeysQuery: "keys", RootKeysBindVars: map[string]any{},
		Sources: []compiler.CompiledSourceQuery{
			{Variable: "root", Query: "root-source", BindVars: map[string]any{}},
			{Variable: "child_set_1", Query: "child-source", BindVars: map[string]any{}},
		},
	}
	stream := OutputStream{
		RowIdentity: &spec.RowIdentity{Fields: []string{"project", "_key"}},
		bindVars:    map[string]any{"project": "P1"}, stream: queryRows,
		batchSize: 100, rootPageRows: 2,
		sourcePage: &page, sourceProgram: syntheticSourceEvaluator{},
	}
	var ids []string
	err := stream.streamRaw(context.Background(), func(row map[string]any) error {
		ids = append(ids, row["id"].(string))
		if row["child_count"] != 1 || row["__loom_row_id"] == nil {
			return fmt.Errorf("source row lost its child count or stable identity")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids, []string{"a", "b", "c"}) || rootPages != 2 || sourceQueries.Load() != 4 {
		t.Fatalf("source page ids=%v key pages=%d source queries=%d", ids, rootPages, sourceQueries.Load())
	}
}

func TestSourcePageRejectsMisorderedSetRows(t *testing.T) {
	page := compiler.CompiledOutputSourcePage{
		RootKeysQuery: "keys", RootKeysBindVars: map[string]any{},
		Sources: []compiler.CompiledSourceQuery{
			{Variable: "root", Query: "root-source"},
			{Variable: "child_set_1", Query: "child-source"},
		},
	}
	stream := OutputStream{
		RowIdentity: &spec.RowIdentity{Fields: []string{"project", "_key"}},
		bindVars:    map[string]any{"project": "P1"}, batchSize: 100, rootPageRows: 2,
		sourcePage: &page, sourceProgram: syntheticSourceEvaluator{},
		stream: func(_ context.Context, query string, _ int, _ map[string]any, visit func(map[string]any) error) error {
			if query == "keys" {
				return visit(map[string]any{"_key": "a"})
			}
			if query == "root-source" {
				return visit(map[string]any{"_key": "a", "value": map[string]any{"id": "a"}})
			}
			return visit(map[string]any{"_key": "different", "value": []any{}})
		},
	}
	err := stream.streamRaw(context.Background(), func(map[string]any) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "identity/order mismatch") {
		t.Fatalf("misordered source error = %v", err)
	}
}

func TestSourcePageReportsOversizedDataAsPreviewLimit(t *testing.T) {
	page := compiler.CompiledOutputSourcePage{
		Sources: []compiler.CompiledSourceQuery{{Variable: "root", Query: "root-source"}},
	}
	stream := OutputStream{
		sourcePage: &page,
		stream: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
			return visit(map[string]any{"_key": "a", "value": map[string]any{"large": strings.Repeat("x", maxSourcePageBytes)}})
		},
	}
	called := false
	err := stream.streamSourceRows(context.Background(), []string{"a"}, func(map[string]any) error {
		called = true
		return nil
	})
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodePreviewResponseTooLarge) || called {
		t.Fatalf("oversized source page error = %v, visit called = %t", err, called)
	}
}
