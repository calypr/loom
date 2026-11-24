package aql_test

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

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
