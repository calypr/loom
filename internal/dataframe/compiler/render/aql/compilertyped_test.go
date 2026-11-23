package aql

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func categoryScanIdentifierByte(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

func TestTypedCategoryTerminalOwnsRootHintDistinctAndLimitWithoutMutatingSource(t *testing.T) {
	output := compilePopulationMappingOutput(t, recipe.Output{
		Name: "typed_categories", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{{Name: "category", Expr: recipe.Expression{Select: "root.status"}}},
	})
	plan := ir.ClonePhysicalPlan(output.Plan)
	plan.StageSequence = nil
	before, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := RenderCategoryScan(plan, ir.PhysicalCategoryScanReturn{
		CategoryColumn: "category", PresenceMarkerColumn: "typed_presence", MaxValues: 4, StreamRoot: true, RootIndexHint: "typed_category_index",
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("typed terminal mutated canonical source plan")
	}
	if !strings.Contains(rendered.Query, `indexHint: "typed_category_index", forceIndexHint: false`) {
		t.Fatal("typed source hint missing or forcing")
	}
	if strings.Contains(rendered.Query, "LET __loom_category_rows = (") {
		t.Fatal("direct root terminal materialized a source relation")
	}
	distinct := strings.LastIndex(rendered.Query, "COLLECT __loom_category_group_present")
	limit := strings.LastIndex(rendered.Query, "LIMIT @__loom_category_limit")
	if distinct < 0 || limit < distinct || rendered.BindVars[categoryLimitBind] != 5 || strings.Contains(rendered.Query[:distinct], "LIMIT @__loom_category_limit") {
		t.Fatal("typed terminal must bound only after distinct with overflow row")
	}
	assertCategoryScanBindVarsMatchQuery(t, rendered.Query, rendered.BindVars)
}
