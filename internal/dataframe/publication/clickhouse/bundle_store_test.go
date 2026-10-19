package clickhouse

import (
	"reflect"
	"testing"

	publication "github.com/calypr/loom/internal/dataframe/publication"
	storeclickhouse "github.com/calypr/loom/internal/store/clickhouse"
)

func TestToColumnsStoresLogicalObjectsAsLosslessJSONText(t *testing.T) {
	got, err := toColumns([]publication.LogicalColumn{
		{Name: "profile", Kind: "object", Nullable: true},
		{Name: "owners", Kind: "object", Repeated: true},
		{Name: "label", Kind: "string"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []storeclickhouse.Column{
		{Name: "profile", Type: "Nullable(String)", LogicalType: "object"},
		{Name: "owners", Type: "Array(Nullable(String))", LogicalType: "object"},
		{Name: "label", Type: "String"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("physical columns = %#v, want %#v", got, want)
	}
}
