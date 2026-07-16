package materialization

import "testing"

func TestKeysetCursorRoundTrip(t *testing.T) {
	cursor := encodeCursor("42", "alice")
	decoded, err := decodeCursor(cursor)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.RowID != "42" || decoded.SortValue != "alice" {
		t.Fatalf("decoded cursor = %#v", decoded)
	}
	predicate, args, err := cursorPredicate(decoded, &Sort{Column: "name"})
	if err != nil {
		t.Fatal(err)
	}
	if predicate == "" || len(args) != 3 || predicate == "toUInt64(`__loom_row_id`) > toUInt64(\"42\")" {
		t.Fatalf("unexpected sort predicate %q", predicate)
	}
}

func TestBuildWhereSupportsAggregateFilters(t *testing.T) {
	where, args, err := buildWhere([]Filter{{Column: "status", Op: "EQ", Value: "active"}}, map[string]struct{}{"status": {}})
	if err != nil {
		t.Fatal(err)
	}
	if len(where) != 1 || len(args) != 1 || where[0] != "`status` = ?" {
		t.Fatalf("where = %#v", where)
	}
}

func TestBuildWhereSupportsGenericReaderOperators(t *testing.T) {
	where, args, err := buildWhere([]Filter{
		{Column: "status", Op: "IN", Value: []string{"active", "pending"}},
		{Column: "score", Op: "GTE", Value: 10},
		{Column: "tags", Op: "ARRAY_CONTAINS", Value: "priority"},
		{Column: "deleted_at", Op: "IS_NULL"},
	}, map[string]struct{}{"status": {}, "score": {}, "tags": {}, "deleted_at": {}})
	if err != nil {
		t.Fatal(err)
	}
	if len(where) != 4 || len(args) != 3 {
		t.Fatalf("where = %#v", where)
	}
	for _, expression := range where {
		if expression == "" {
			t.Fatal("empty filter expression")
		}
	}
}
