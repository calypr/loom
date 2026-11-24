package aql_test

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/testkit/compilerfixtures"
)

func TestCurrentUnpivotPreservesAuthoredSlotsAndCanonicalRendererOrdering(t *testing.T) {
	compiled, _, err := compileReshapeOracle(reshapeOracleUnpivotOutput(recipe.UnpivotNullPreserve, "s04-unpivot-order"), "project", "generation", 100)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.Render(compiled.Plan, aql.Options{})
	if err != nil {
		t.Fatal(err)
	}
	compilerfixtures.AssertCurrentUnpivotAuthoredSlots(t, compiled, rendered.Query, rendered.BindVars)
}
