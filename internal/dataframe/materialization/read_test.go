package materialization

import (
	"encoding/json"
	"reflect"
	"testing"
)

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

func TestNumericCountAcceptsClickHouseRepresentations(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  int64
	}{
		{name: "integer", value: int64(12), want: 12},
		{name: "json number", value: json.Number("34"), want: 34},
		{name: "string", value: "4764", want: 4764},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := numericCount(test.value)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("numericCount(%#v) = %d, want %d", test.value, got, test.want)
			}
		})
	}
}

func TestFederatedRowUnionUsesPerProjectScopes(t *testing.T) {
	dataset := FederatedDataset{
		Columns: []Column{{Name: "id"}, {Name: "auth_resource_path"}},
		Sources: []Materialization{
			{ID: "source-a", Project: "project-a", PhysicalTable: "table_a"},
			{ID: "source-b", Project: "project-b", PhysicalTable: "table_b"},
		},
	}
	columns, allowed, err := federatedColumns(dataset, []string{"id"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	query, args, err := federatedRowUnion(
		dataset, columns, []Filter{{Column: "id", Op: "EQ", Value: "patient-1"}}, allowed,
		nil, false,
		map[string][]string{"project-a": {"scope-a"}},
		map[string]bool{"project-b": true},
	)
	if err != nil {
		t.Fatal(err)
	}
	wantQuery := "SELECT `id`, toString(`__loom_row_id`) AS `__loom_row_id`, concat(?, ':', toString(`__loom_row_id`)) AS `__loom_global_row_id` FROM `table_a` WHERE `id` = ? AND `auth_resource_path` IN ? UNION ALL SELECT `id`, toString(`__loom_row_id`) AS `__loom_row_id`, concat(?, ':', toString(`__loom_row_id`)) AS `__loom_global_row_id` FROM `table_b` WHERE `id` = ?"
	if query != wantQuery {
		t.Fatalf("query = %q, want %q", query, wantQuery)
	}
	if want := []any{"source-a", "patient-1", []string{"scope-a"}, "source-b", "patient-1"}; !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestFederatedSourceWherePreservesRestrictedEmptyScope(t *testing.T) {
	source := Materialization{Project: "project-a"}
	allowed := map[string]struct{}{"auth_resource_path": {}}
	where, args, err := federatedSourceWhere(source, nil, allowed, nil, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"0"}; !reflect.DeepEqual(where, want) {
		t.Fatalf("where = %#v, want %#v", where, want)
	}
	if len(args) != 0 {
		t.Fatalf("args = %#v, want none for a false predicate", args)
	}

	where, args, err = federatedSourceWhere(source, nil, allowed, nil, true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(where) != 0 || len(args) != 0 {
		t.Fatalf("unrestricted source produced where=%#v args=%#v", where, args)
	}
}
