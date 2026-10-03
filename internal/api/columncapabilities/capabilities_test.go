package columncapabilities

import "testing"

func TestFromClickHouseClassifiesPublishedColumnCapabilities(t *testing.T) {
	for _, test := range []struct {
		name     string
		typeName string
		want     Tuple
	}{
		{name: "nullable integer", typeName: "Nullable(Int64)", want: Tuple{Logical: "integer", Nullable: true, Filterable: true, Sortable: true, Aggregatable: true}},
		{name: "array nullable number", typeName: "Array(Nullable(Float64))", want: Tuple{Logical: "number", Nullable: true, Repeated: true, Filterable: true}},
		{name: "date", typeName: "DateTime64", want: Tuple{Logical: "date", Filterable: true, Sortable: true, Aggregatable: true}},
		{name: "string", typeName: "String", want: Tuple{Logical: "string", Filterable: true, Sortable: true, Aggregatable: true}},
		{name: "json", typeName: "Map(String, String)", want: Tuple{Logical: "json", Filterable: true, Sortable: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := FromClickHouse(test.typeName); got != test.want {
				t.Fatalf("FromClickHouse(%q) = %#v, want %#v", test.typeName, got, test.want)
			}
		})
	}
}
