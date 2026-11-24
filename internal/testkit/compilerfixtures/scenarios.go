package compilerfixtures

import (
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func ConstructionExpandGroupOutput() recipe.Output {
	output := ConstructionExpandOutput()
	output.Name = "construction_expand_group_oracle"
	output.Construction.Steps = append(output.Construction.Steps, recipe.ConstructionStep{
		ID: "group_tags", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "expand_tags"}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
			ConstructionID: "group_tag_values",
			Keys:           []recipe.ConstructionGroupKey{{InputColumnID: "tag_id", OutputColumnID: "grouped_tag_id"}},
			Aggregates: []recipe.ConstructionGroupAggregate{
				{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"},
				{Operation: recipe.ConstructionGroupCountNonNull, InputColumnID: "status_id", OutputColumnID: "status_count_id"},
				{Operation: recipe.ConstructionGroupCountDistinct, InputColumnID: "status_id", OutputColumnID: "status_distinct_id"},
			},
		}},
		Outputs: []recipe.StageColumn{
			{ID: "grouped_tag_id", Name: "tag"}, {ID: "rows_id", Name: "rows"},
			{ID: "status_count_id", Name: "status_count"}, {ID: "status_distinct_id", Name: "status_distinct"},
		},
	})
	return output
}

func ConstructionExpandOutput() recipe.Output {
	return recipe.Output{
		Name: "construction_expand_oracle", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{
			{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
			{Name: "tags", ColumnID: "tags_id", Expr: recipe.Expression{Select: "root.note[].text"}, ValueMode: recipe.ValueModeAll},
		},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "tags_id", Name: "tags"}},
			Steps: []recipe.ConstructionStep{{
				ID: "expand_tags", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionExpandOp, Expand: &recipe.ConstructionExpand{
					ConstructionID: "expand_tag_values", InputColumnID: "tags_id", OutputColumnID: "tag_id",
					OrdinalColumnID: "ordinal_id", EmptyPolicy: recipe.ExpansionPreserveParent,
				}},
				Outputs: []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "tag_id", Name: "tag"}, {ID: "ordinal_id", Name: "ordinal"}},
			}},
		},
	}
}

func ConstructionGroupRowValuesOutput() recipe.Output {
	return recipe.Output{
		Name: "construction_group_all_row_values", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{
			{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
			{Name: "tags", ColumnID: "tags_id", Expr: recipe.Expression{Select: "root.note[].text"}, ValueMode: recipe.ValueModeAll},
		},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "tags_id", Name: "tags"}},
			Steps: []recipe.ConstructionStep{{
				ID: "group_status", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
					ConstructionID: "group_status",
					Keys:           []recipe.ConstructionGroupKey{{InputColumnID: "status_id", OutputColumnID: "grouped_status_id"}},
					Aggregates:     []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"}},
				}},
				RowValues: []recipe.ConstructionRowValue{{InputColumnID: "tags_id", OutputColumnID: "tag_values_id", Policy: recipe.ConstructionRowValueAll}},
				Outputs: []recipe.StageColumn{
					{ID: "grouped_status_id", Name: "status", Type: "string"},
					{ID: "rows_id", Name: "rows", Type: "integer"},
					{ID: "tag_values_id", Name: "tag_values", Type: "array"},
				},
			}},
		},
	}
}

func ConstructionPreviewMultiKeyPivotOutput() recipe.Output {
	categoryFemale, categoryMale := "female", "male"
	return recipe.Output{
		Name: "two_scan_multi_key_preview", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{
			{Name: "birth_date", ColumnID: "birth_date_id", Expr: recipe.Expression{Select: "root.birthDate"}},
			{Name: "active", ColumnID: "active_id", Expr: recipe.Expression{Select: "root.active"}},
			{Name: "category", ColumnID: "category_id", Expr: recipe.Expression{Select: "root.gender"}},
			{Name: "amount", ColumnID: "amount_id", Expr: recipe.Expression{Select: "root.multipleBirthInteger"}},
		},
		Construction: &recipe.Construction{
			Version: 1,
			SourceColumns: []recipe.StageColumn{
				{ID: "birth_date_id", Name: "birth_date"}, {ID: "active_id", Name: "active"},
				{ID: "category_id", Name: "category"}, {ID: "amount_id", Name: "amount"},
			},
			Steps: []recipe.ConstructionStep{{
				ID:     "pivot_two_scan_multi_key_rows",
				Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{
					ConstructionID: "two_scan_multi_key_pivot", GroupKeyIDs: []string{"birth_date_id", "active_id"},
					CategoryColumnID: "category_id", ValueColumnID: "amount_id",
					Categories: []recipe.ConstructionPivotCategory{
						{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &categoryFemale}, OutputColumnID: "female_amount_id"},
						{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &categoryMale}, OutputColumnID: "male_amount_id"},
					},
					DuplicatePolicy: recipe.PivotDuplicateError, MissingCellPolicy: recipe.PivotMissingCellNull,
					UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
				}},
				Outputs: []recipe.StageColumn{
					{ID: "birth_date_id", Name: "birth_date"}, {ID: "active_id", Name: "active"},
					{ID: "female_amount_id", Name: "female_amount"}, {ID: "male_amount_id", Name: "male_amount"},
				},
			}},
		},
	}
}

func ReshapeOracleStringFilter(value string) spec.FilterValue {
	return spec.FilterValue{Kind: recipe.FilterString, String: &value}
}
