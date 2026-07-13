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
	predicate, err := cursorPredicate(decoded, &Sort{Column: "name"})
	if err != nil {
		t.Fatal(err)
	}
	if predicate == "" || predicate == "toUInt64(`__loom_row_id`) > toUInt64(\"42\")" {
		t.Fatalf("unexpected sort predicate %q", predicate)
	}
}

func TestBuildWhereSupportsAggregateFilters(t *testing.T) {
	where, err := buildWhere([]Filter{{Column: "status", Op: "EQ", Value: "active"}}, map[string]struct{}{"status": {}})
	if err != nil {
		t.Fatal(err)
	}
	if len(where) != 1 || where[0] == "" {
		t.Fatalf("where = %#v", where)
	}
}
