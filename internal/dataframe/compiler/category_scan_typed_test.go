package compiler

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

// Used only by the test's independent emitted-bind parser.
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
	rendered, err := aql.RenderCategoryScan(plan, ir.PhysicalCategoryScanReturn{
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

func TestTypedCategoryTerminalRejectsMismatchedIndexedSourcePath(t *testing.T) {
	output := compilePopulationMappingOutput(t, recipe.Output{
		Name: "typed_index_proof", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{{Name: "category", Expr: recipe.Expression{Select: "root.status"}}},
	})
	plan := ir.ClonePhysicalPlan(output.Plan)
	plan.StageSequence = nil
	root := ""
	for _, operation := range plan.Operations {
		if operation.RootScan != nil {
			root = operation.RootScan.Variable
		}
	}
	_, err := aql.RenderCategoryScan(plan, ir.PhysicalCategoryScanReturn{
		CategoryColumn: "category", MaxValues: 4, RootIndexHint: "typed_category_index",
		IndexedRoot: &ir.PhysicalIndexedCategoryScan{RootVariable: root, ValuePath: []string{"payload", "valueInteger"}},
	})
	if err == nil {
		t.Fatal("typed category index terminal accepted a different source field")
	}
}

func TestTypedCategoryTerminalRejectsStaleTerminalColumnMetadata(t *testing.T) {
	compiled, _, err := compileCurrentConstructionOracle(currentOracleDerivedPivotOutput(false), "project", "generation", 10)
	if err != nil {
		t.Fatal(err)
	}
	_, err = aql.RenderCategoryScan(compiled.Plan, ir.PhysicalCategoryScanReturn{CategoryColumn: "stale_column", PresenceMarkerColumn: "marker", MaxValues: 4})
	if err == nil {
		t.Fatal("typed terminal accepted a column absent from its actual final stage")
	}
}
