package arango

import (
	"context"
	"errors"
	"testing"
)

type queryRowsFunc func(context.Context, string, int, map[string]interface{}, RowVisitor) error

func (f queryRowsFunc) QueryRows(ctx context.Context, query string, batchSize int, bindVars map[string]interface{}, visit RowVisitor) error {
	return f(ctx, query, batchSize, bindVars, visit)
}

func TestQueryHasRowDrainsRowsAndPreservesQueryError(t *testing.T) {
	queryErr := errors.New("cursor failed after rows")
	visited := 0
	fake := queryRowsFunc(func(_ context.Context, query string, batchSize int, binds map[string]interface{}, visit RowVisitor) error {
		if query != "RETURN 1" || batchSize != 7 || binds["key"] != "value" {
			t.Fatalf("query args = %q, %d, %#v", query, batchSize, binds)
		}
		for range 3 {
			visited++
			if err := visit(nil); err != nil {
				return err
			}
		}
		return queryErr
	})

	found, err := QueryHasRow(context.Background(), fake, "RETURN 1", 7, map[string]interface{}{"key": "value"})
	if !found || !errors.Is(err, queryErr) || visited != 3 {
		t.Fatalf("QueryHasRow() = %v, %v after %d rows; want true, cursor error, 3 rows", found, err, visited)
	}
}

func TestQueryHasRowReturnsFalseForEmptyResults(t *testing.T) {
	fake := queryRowsFunc(func(_ context.Context, _ string, _ int, _ map[string]interface{}, _ RowVisitor) error { return nil })
	found, err := QueryHasRow(context.Background(), fake, "RETURN 1", 1, nil)
	if found || err != nil {
		t.Fatalf("QueryHasRow() = %v, %v; want false, nil", found, err)
	}
}

func TestQueryLastPreservesLastRowWins(t *testing.T) {
	fake := queryRowsFunc(func(_ context.Context, _ string, _ int, _ map[string]interface{}, visit RowVisitor) error {
		for _, row := range []map[string]any{{"value": "first"}, {"value": "last"}} {
			if err := visit(row); err != nil {
				return err
			}
		}
		return nil
	})
	got, found, err := QueryLast[struct {
		Value string `json:"value"`
	}](context.Background(), fake, "RETURN 1", 2, nil)
	if err != nil || !found || got.Value != "last" {
		t.Fatalf("QueryLast() = %#v, %v, %v; want last row", got, found, err)
	}
}

func TestDecodeValueReturnsPartialValueOnUnmarshalError(t *testing.T) {
	type value struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	decoded, err := DecodeValue[value](map[string]any{"name": "Ada", "age": "invalid"})
	if err == nil || decoded.Name != "Ada" {
		t.Fatalf("DecodeValue() = %#v, %v; want partial name and unmarshal error", decoded, err)
	}
}
