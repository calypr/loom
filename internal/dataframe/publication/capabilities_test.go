package publication

import "testing"

func TestFromClickHouseClassifiesPublishedColumnCapabilities(t *testing.T) {
	for _, test := range []struct {
		name     string
		typeName string
		want     ColumnCapabilities
	}{
		{name: "nullable integer", typeName: "Nullable(Int64)", want: ColumnCapabilities{Logical: "integer", Nullable: true, Filterable: true, Sortable: true, Aggregatable: true}},
		{name: "array nullable number", typeName: "Array(Nullable(Float64))", want: ColumnCapabilities{Logical: "number", Nullable: true, Repeated: true, Filterable: true}},
		{name: "date", typeName: "DateTime64", want: ColumnCapabilities{Logical: "date", Filterable: true, Sortable: true, Aggregatable: true}},
		{name: "string", typeName: "String", want: ColumnCapabilities{Logical: "string", Filterable: true, Sortable: true, Aggregatable: true}},
		{name: "json", typeName: "Map(String, String)", want: ColumnCapabilities{Logical: "json", Filterable: true, Sortable: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := ColumnCapabilitiesFromClickHouse(test.typeName); got != test.want {
				t.Fatalf("ColumnCapabilitiesFromClickHouse(%q) = %#v, want %#v", test.typeName, got, test.want)
			}
		})
	}
}

func TestResolveRecomputesCapabilitiesFromStoredOverrides(t *testing.T) {
	got := ResolveColumnCapabilities("Array(String)", "json", true, false)
	want := ColumnCapabilities{Logical: "json", Nullable: true, Repeated: true, Filterable: true}
	if got != want {
		t.Fatalf("ResolveColumnCapabilities() = %#v, want %#v", got, want)
	}
}
