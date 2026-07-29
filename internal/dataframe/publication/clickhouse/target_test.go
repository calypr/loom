package clickhouse

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/publication"
)

func TestToColumnsPreservesNullableFlatTypes(t *testing.T) {
	columns, err := toColumns([]publication.LogicalColumn{
		{Name: "created", Kind: "date-time", Nullable: true},
		{Name: "tags", Kind: "string", Repeated: true, Nullable: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if columns[0].Type != "Nullable(DateTime64(3))" {
		t.Fatalf("created type = %q", columns[0].Type)
	}
	if columns[1].Type != "Array(String)" {
		t.Fatalf("tags type = %q", columns[1].Type)
	}
}
