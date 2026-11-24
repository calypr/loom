package compilerfixtures

import (
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func ConstructionCountRowsGroupPivotLineageOutput() recipe.Output {
	active := "active"
	return recipe.Output{
		Name: "construction_count_rows_group_pivot_lineage", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{
			{Name: "patient_ref", ColumnID: "patient_ref_id", Expr: recipe.Expression{Select: "root.subject.reference"}},
			{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
		},
		Construction: &recipe.Construction{
			Version: 1,
			SourceColumns: []recipe.StageColumn{
				{ID: "patient_ref_id", Name: "patient_ref", Type: "string"},
				{ID: "status_id", Name: "status", Type: "string"},
			},
			Steps: []recipe.ConstructionStep{
				{
					ID: "group", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
						ConstructionID: "group", MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup,
						Keys: []recipe.ConstructionGroupKey{
							{InputColumnID: "patient_ref_id", OutputColumnID: "patient_ref_group_id"},
							{InputColumnID: "status_id", OutputColumnID: "status_group_id"},
						},
						Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"}},
					}},
					Outputs: []recipe.StageColumn{
						{ID: "patient_ref_group_id", Name: "patient_ref", Type: "string"},
						{ID: "status_group_id", Name: "status", Type: "string"},
						{ID: "rows_id", Name: "rows", Type: "integer"},
					},
				},
				{
					ID: "pivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "group"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{
						ConstructionID: "row_lineage_group_pivot", GroupKeyIDs: []string{"patient_ref_group_id"},
						CategoryColumnID: "status_group_id", ValueColumnID: "rows_id",
						Categories: []recipe.ConstructionPivotCategory{
							{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &active}, OutputColumnID: "active_rows_id"},
							{Key: recipe.TableScalar{Kind: recipe.TableScalarNull}, OutputColumnID: "null_status_rows_id"},
						},
						DuplicatePolicy: recipe.PivotDuplicateError, MissingCellPolicy: recipe.PivotMissingCellNull,
						UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
					}},
					Outputs: []recipe.StageColumn{
						{ID: "patient_ref_group_id", Name: "patient_ref", Type: "string"},
						{ID: "active_rows_id", Name: "active_rows", Type: "integer"},
						{ID: "null_status_rows_id", Name: "null_status_rows", Type: "integer"},
					},
				},
			},
		},
	}
}

func DirectRelatedExpandOutput(emptyPolicy recipe.ExpansionEmptyPolicy) recipe.Output {
	output := RelatedExpandOracleOutput(emptyPolicy)
	step := output.Construction.Steps[1]
	step.Inputs = []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}}
	output.Construction.Steps = []recipe.ConstructionStep{step}
	return output
}
