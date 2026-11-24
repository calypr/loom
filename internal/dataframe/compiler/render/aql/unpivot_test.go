package aql_test

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func assertCurrentUnpivotAuthoredSlots(t *testing.T, output lower.CompiledRecipeOutput, query string, bindings map[string]any) {
	t.Helper()
	if output.Plan.StageSequence == nil || len(output.Plan.StageSequence.Stages) != 1 || output.Plan.StageSequence.Stages[0].Unpivot == nil {
		t.Fatal("missing current Unpivot stage")
	}
	u := output.Plan.StageSequence.Stages[0].Unpivot
	if len(u.Inputs) != 2 || u.Inputs[0].Column != "group_number" || u.Inputs[1].Column != "numeric_value" || bindings[u.Inputs[0].KeyBindKey] != "z-integer" || bindings[u.Inputs[1].KeyBindKey] != "a-decimal" || u.Inputs[0].KeyKind != "STRING" || u.Inputs[1].KeyKind != "STRING" {
		t.Fatalf("Unpivot authored slot order changed: %#v, bindings=%#v", u.Inputs, bindings)
	}
	first := strings.Index(query, "{key: @"+u.Inputs[0].KeyBindKey+",")
	second := strings.Index(query, "{key: @"+u.Inputs[1].KeyBindKey+",")
	if first < 0 || second <= first {
		t.Fatalf("AQL slot enumeration lost authored [z-integer, a-decimal] order: %s", query)
	}
	if !strings.Contains(query, "SORT __loom_construction_final_row.__loom_row_id ASC") {
		t.Fatalf("current Unpivot lost canonical identity ordering: %s", query)
	}
}

func TestCurrentUnpivotPreservesAuthoredSlotsAndCanonicalRendererOrdering(t *testing.T) {
	compiled, _, err := compileReshapeOracle(reshapeOracleUnpivotOutput(recipe.UnpivotNullPreserve, "s04-unpivot-order"), "project", "generation", 100)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.Render(compiled.Plan, aql.Options{})
	if err != nil {
		t.Fatal(err)
	}
	assertCurrentUnpivotAuthoredSlots(t, compiled, rendered.Query, rendered.BindVars)
}
