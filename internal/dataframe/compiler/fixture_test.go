package compiler

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestCurrentConstructionOracleFixturesCompileWithoutLegacyReshape(t *testing.T) {
	for _, test := range []struct {
		name   string
		output recipe.Output
		kind   ir.PhysicalStageOperationKind
	}{
		{"pivot", currentOraclePivotOutput(recipe.PivotUnlistedCategoryError), ir.PhysicalStagePivotOp},
		{"unpivot_preserve", currentOracleUnpivotOutput(recipe.UnpivotNullPreserve), ir.PhysicalStageUnpivotOp},
		{"unpivot_drop", currentOracleUnpivotOutput(recipe.UnpivotNullDrop), ir.PhysicalStageUnpivotOp},
	} {
		t.Run(test.name, func(t *testing.T) {
			compiled, query, err := compileCurrentConstructionOracle(test.output, "project", "generation", 100)
			if err != nil {
				t.Fatal(err)
			}
			if compiled.Plan.StageSequence == nil || len(compiled.Plan.StageSequence.Stages) != 1 || compiled.Plan.StageSequence.Stages[0].Kind != test.kind {
				t.Fatalf("fixture did not use one current typed construction stage: %#v", compiled.Plan.StageSequence)
			}
			if err := compiled.Plan.Validate(); err != nil {
				t.Fatal(err)
			}
			if err := ir.ValidateGenericPhysicalPlanScope(compiled.Plan); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(query.Query, "@project") || query.BindVars["project"] != "project" {
				t.Fatal("compiled fixture lost project scope")
			}
			if currentOracleOutputColumnKind(compiled.OutputSchema, "amount") != "decimal" && test.kind == ir.PhysicalStageUnpivotOp {
				t.Fatalf("INTEGER+DECIMAL widening changed: %#v", compiled.OutputSchema)
			}
			t.Logf("stage=%s outputColumns=%v", test.kind, compiled.OutputSchema)
		})
	}
}

func TestCurrentConstructionSentinelPivotFixtureCompiles(t *testing.T) {
	output := currentOraclePivotOutput(recipe.PivotUnlistedCategoryError)
	step := &output.Construction.Steps[0]
	step.Operation.Pivot.Categories = append(step.Operation.Pivot.Categories, recipe.ConstructionPivotCategory{Key: recipe.TableScalar{Kind: recipe.TableScalarNull}, OutputColumnID: "recorded_null"}, recipe.ConstructionPivotCategory{Key: recipe.TableScalar{Kind: recipe.TableScalarMissing}, OutputColumnID: "missing"})
	step.Outputs = append(step.Outputs, recipe.StageColumn{ID: "recorded_null", Name: "recorded_null"}, recipe.StageColumn{ID: "missing", Name: "missing"})
	compiled, _, err := compileCurrentConstructionOracle(output, "project", "generation", 100)
	if err != nil {
		t.Fatal(err)
	}
	pivot := compiled.Plan.StageSequence.Stages[0].GroupedPivot
	if pivot == nil || (pivot.CategoryPresence == nil && !pivot.CategoryPresenceFromInput) || pivot.CategoryPresenceColumn == "" {
		t.Fatalf("NULL/MISSING pivot lacks property-presence contract: %#v", pivot)
	}
	if pivot.Categories[4].MatchKind != ir.PhysicalPivotCategoryNullMatch || pivot.Categories[5].MatchKind != ir.PhysicalPivotCategoryMissingMatch {
		t.Fatalf("sentinel matches changed: %#v", pivot.Categories)
	}
}

func TestCurrentConstructionDirectIDPivotFixtureRetainsFastPath(t *testing.T) {
	output := currentOraclePivotOutput(recipe.PivotUnlistedCategoryError)
	step := &output.Construction.Steps[0]
	step.Operation.Pivot.GroupKeyIDs = []string{"source_id"}
	step.Outputs = append([]recipe.StageColumn{{ID: "source_id", Name: "source_id"}}, step.Outputs[2:]...)
	compiled, query, err := compileCurrentConstructionOracle(output, "project", "generation", 100)
	if err != nil {
		t.Fatal(err)
	}
	pivot := compiled.Plan.StageSequence.Stages[0].GroupedPivot
	if pivot == nil || !pivot.OneInputRowPerGroup {
		t.Fatalf("direct source-id Pivot lost one-input-per-group proof: %#v", pivot)
	}
	if strings.Contains(query.Query, "COLLECT") || strings.Contains(query.Query, "FOR __loom_physical_reshape_cell_") {
		t.Fatalf("direct ID Pivot retained group materialization: %s", query.Query)
	}
}

func currentOracleOutputColumnKind(columns []lower.CompiledOutputColumn, name string) any {
	for _, column := range columns {
		if column.Name == name {
			return column.Kind
		}
	}
	return nil
}
