package lower

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestCompileConstructionUsesTypedIntermediateStages(t *testing.T) {
	output := constructionTestOutput()
	compiled := compileDerivedTestOutput(t, output)
	if compiled.Plan.StageSequence == nil {
		t.Fatal("compiled plan has no typed stage sequence")
	}
	if got := len(compiled.Stages); got != 5 {
		t.Fatalf("compiled stage descriptors = %d, want source plus four operations", got)
	}
	wantOperations := []ir.PhysicalStageOperationKind{
		ir.PhysicalStagePivotOp, ir.PhysicalStageDeriveOp, ir.PhysicalStageFilterOp, ir.PhysicalStageUnpivotOp,
	}
	for index, want := range wantOperations {
		stage := compiled.Plan.StageSequence.Stages[index]
		if stage.Kind != want {
			t.Fatalf("stage %d operation = %q, want %q", index, stage.Kind, want)
		}
		if stage.InputStageID != compiled.Stages[index].ID {
			t.Fatalf("stage %q input = %q, want immediately preceding stage %q", stage.ID, stage.InputStageID, compiled.Stages[index].ID)
		}
	}
	wantNames := []string{"group", "total", "measure", "amount", "__loom_row_id"}
	if got := compiledSchemaNames(compiled.OutputSchema); !equalStringSlices(got, wantNames) {
		t.Fatalf("final construction schema = %#v, want %#v", got, wantNames)
	}
	if compiled.OutputSchema[0].ID != "group_id" || compiled.OutputSchema[1].ID != "total_id" || compiled.OutputSchema[2].ID != "measure_id" || compiled.OutputSchema[3].ID != "amount_id" {
		t.Fatalf("stable column IDs were lost in final schema: %#v", compiled.OutputSchema)
	}
	if compiled.Stages[0].ID != recipe.ConstructionSourceProjectionID || len(compiled.Stages[0].Capabilities) != 4 {
		t.Fatalf("source stage descriptor lacks exact source capabilities: %#v", compiled.Stages[0])
	}

	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"COLLECT", " + ", "FILTER", "TABLE_UNPIVOT", "__loom_construction_stage_4"} {
		if !strings.Contains(rendered.Query, expected) {
			t.Fatalf("rendered stage sequence is missing %q: %s", expected, rendered.Query)
		}
	}
	if strings.Contains(rendered.Query, "LIMIT @") {
		t.Fatalf("lowering inserted a preview limit before the caller requested one: %s", rendered.Query)
	}
}

func TestCompileSourceOnlyConstructionKeepsSourcePlan(t *testing.T) {
	output := constructionTestOutput()
	output.Construction = &recipe.Construction{
		Version: 1,
		SourceColumns: []recipe.StageColumn{
			{ID: "group_id", Name: "group"},
			{ID: "category_id", Name: "category"},
			{ID: "amount_id", Name: "amount"},
		},
	}
	compiled := compileDerivedTestOutput(t, output)
	if compiled.Plan.StageSequence != nil {
		t.Fatal("source-only construction created a fake physical stage")
	}
	if len(compiled.Stages) != 1 || compiled.Stages[0].ID != recipe.ConstructionSourceProjectionID {
		t.Fatalf("source-only descriptors = %#v", compiled.Stages)
	}
	if got, want := compiledSchemaNames(compiled.OutputSchema), []string{"group", "category", "amount", "_key"}; !equalStringSlices(got, want) {
		t.Fatalf("source-only schema = %#v, want %#v", got, want)
	}
}

func constructionTestOutput() recipe.Output {
	groupLabel, categoryLabel, amountLabel := "Group", "Category", "Amount"
	integerZero, integerOne := int64(0), int64(1)
	stringA, stringB := "a", "b"
	filterValue := int64(5)
	return recipe.Output{
		Name: "construction_test", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{
			{Name: "group", ColumnID: "group_id", Label: groupLabel, Expr: recipe.Expression{Select: "gender"}},
			{Name: "category", ColumnID: "category_id", Label: categoryLabel, Expr: recipe.Expression{Select: "multipleBirthInteger"}},
			{Name: "amount", ColumnID: "amount_id", Label: amountLabel, Expr: recipe.Expression{Select: "multipleBirthInteger"}},
		},
		Construction: &recipe.Construction{
			Version: 1,
			SourceColumns: []recipe.StageColumn{
				{ID: "group_id", Name: "group", Label: groupLabel},
				{ID: "category_id", Name: "category", Label: categoryLabel},
				{ID: "amount_id", Name: "amount", Label: amountLabel},
			},
			Steps: []recipe.ConstructionStep{
				{
					ID: "pivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{
						ConstructionID: "pivot_values", GroupKeyIDs: []string{"group_id"}, CategoryColumnID: "category_id", ValueColumnID: "amount_id",
						Categories: []recipe.ConstructionPivotCategory{
							{Key: recipe.TableScalar{Kind: recipe.TableScalarInteger, Integer: &integerZero}, OutputColumnID: "amount_a_id"},
							{Key: recipe.TableScalar{Kind: recipe.TableScalarInteger, Integer: &integerOne}, OutputColumnID: "amount_b_id"},
						},
						DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
						UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
					}},
					Outputs: []recipe.StageColumn{{ID: "group_id", Name: "group", Label: groupLabel}, {ID: "amount_a_id", Name: "amount_a"}, {ID: "amount_b_id", Name: "amount_b"}},
				},
				{
					ID: "derive", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "pivot"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionDeriveOp, Derive: &recipe.ConstructionDerive{
						ConstructionID: "derive_total", OutputColumnID: "total_id", Operation: recipe.DerivedAdd,
						Left:               recipe.ConstructionOperand{Kind: recipe.DerivedColumnOperand, ColumnID: "amount_a_id"},
						Right:              recipe.ConstructionOperand{Kind: recipe.DerivedLiteralOperand, Literal: &recipe.DerivedLiteral{Kind: recipe.NumericInteger, Integer: &integerOne}},
						MissingInputPolicy: recipe.MissingInputPropagateNull,
					}},
					Outputs: []recipe.StageColumn{{ID: "group_id", Name: "group", Label: groupLabel}, {ID: "amount_a_id", Name: "amount_a"}, {ID: "amount_b_id", Name: "amount_b"}, {ID: "total_id", Name: "total"}},
				},
				{
					ID: "filter", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "derive"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
						ColumnID: "total_id", Operator: recipe.FilterGreaterEq,
						Values: []recipe.FilterValue{{Kind: recipe.FilterInteger, Integer: &filterValue}},
					}},
					Outputs: []recipe.StageColumn{{ID: "group_id", Name: "group", Label: groupLabel}, {ID: "amount_a_id", Name: "amount_a"}, {ID: "amount_b_id", Name: "amount_b"}, {ID: "total_id", Name: "total"}},
				},
				{
					ID: "unpivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "filter"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionUnpivotOp, Unpivot: &recipe.ConstructionUnpivot{
						ConstructionID: "unpivot_values", Inputs: []recipe.ConstructionUnpivotInput{
							{ColumnID: "amount_a_id", Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &stringA}},
							{ColumnID: "amount_b_id", Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &stringB}},
						}, KeyOutputColumnID: "measure_id", ValueOutputColumnID: "amount_id", NullRowPolicy: recipe.UnpivotNullPreserve,
					}},
					Outputs: []recipe.StageColumn{{ID: "group_id", Name: "group", Label: groupLabel}, {ID: "total_id", Name: "total"}, {ID: "measure_id", Name: "measure"}, {ID: "amount_id", Name: "amount"}},
				},
			},
		},
	}
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
