package published

import (
	"context"
	"strings"
	"testing"
)

type pageQueryer struct{ queries []string }

func (q *pageQueryer) QueryRowsArgs(_ context.Context, query string, _ []string, _ ...any) ([]map[string]any, error) {
	q.queries = append(q.queries, query)
	return []map[string]any{{"__loom_row_id": "2", "__loom_total": int64(2)}}, nil
}

func (q *pageQueryer) QueryRowsArgsVisit(context.Context, string, []string, func(map[string]any) error, ...any) error {
	return nil
}

func TestPageCursorAddsWhereForUnrestrictedTable(t *testing.T) {
	queryer := &pageQueryer{}
	materialization := Materialization{
		PhysicalTable: "published_patient",
		Columns:       []Column{{Name: "name", ClickHouse: "String"}},
	}
	_, err := (&Reader{ClickHouse: queryer}).Page(context.Background(), materialization, PageRequest{
		First: 1, After: encodeCursor("1", nil), Unrestricted: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(queryer.queries) != 1 {
		t.Fatalf("queries = %d, want 1", len(queryer.queries))
	}
	query := queryer.queries[0]
	if !strings.Contains(query, " WHERE `__loom_row_id` > ?") {
		t.Fatalf("cursor predicate missing WHERE: %s", query)
	}
	if strings.Contains(query, " AND `__loom_row_id`") {
		t.Fatalf("cursor predicate incorrectly uses AND without WHERE: %s", query)
	}
}
