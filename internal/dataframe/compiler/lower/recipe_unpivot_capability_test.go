package lower

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestUnpivotCapabilityAllowsSelectingOneScalarColumn(t *testing.T) {
	for _, test := range []struct {
		name    string
		columns []CompiledOutputColumn
		want    bool
	}{
		{name: "one numeric column", columns: []CompiledOutputColumn{{ID: "count", Name: "count", Kind: "integer", Cardinality: "required_one"}}, want: true},
		{name: "incompatible neighbors can be preserved", columns: []CompiledOutputColumn{{ID: "patient", Name: "patient", Kind: "string", Cardinality: "required_one"}, {ID: "count", Name: "count", Kind: "integer", Cardinality: "required_one"}}, want: true},
		{name: "no public scalar", columns: []CompiledOutputColumn{{ID: "values", Name: "values", Kind: "string", Cardinality: "many"}, {ID: "_key", Name: "_key", Kind: "string", Cardinality: "required_one", Internal: true}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, capability := range stageCapabilities(test.columns, false) {
				if capability.Operation == recipe.ConstructionUnpivotOp {
					if capability.Supported != test.want {
						t.Fatalf("Unpivot capability = %t (%s), want %t", capability.Supported, capability.Reason, test.want)
					}
					return
				}
			}
			t.Fatal("Unpivot capability missing")
		})
	}
}
