package compilerfixtures

import (
	"math"

	"github.com/calypr/loom/internal/dataframe/spec"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func ConstructionOracleOutput() recipe.Output {
	stringAlpha, stringBeta := "alpha", "beta"
	minimumTotal := 10.0
	return recipe.Output{
		Name: "construction_oracle", RootResourceType: "Observation", RowGrain: "observation", RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "group_text", ColumnID: "group_text_id", Expr: recipe.Expression{Select: "root.status"}},
			{Name: "group_number", ColumnID: "group_number_id", Expr: recipe.Expression{Select: "root.valueInteger"}},
			{Name: "category", ColumnID: "category_id", Expr: recipe.Expression{Select: "root.code.text"}},
			{Name: "amount", ColumnID: "amount_id", Expr: recipe.Expression{Select: "root.valueQuantity.value"}},
		},
		Construction: &recipe.Construction{
			Version: 1,
			SourceColumns: []recipe.StageColumn{
				{ID: "group_text_id", Name: "group_text"}, {ID: "group_number_id", Name: "group_number"},
				{ID: "category_id", Name: "category"}, {ID: "amount_id", Name: "amount"},
			},
			Steps: []recipe.ConstructionStep{
				{
					ID: "pivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{
						ConstructionID: "oracle_pivot", GroupKeyIDs: []string{"group_text_id", "group_number_id"},
						CategoryColumnID: "category_id", ValueColumnID: "amount_id",
						Categories: []recipe.ConstructionPivotCategory{
							{Key: ReshapeOracleString(stringAlpha), OutputColumnID: "alpha_id"},
							{Key: ReshapeOracleString(stringBeta), OutputColumnID: "beta_id"},
						},
						DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
						UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
					}},
					Outputs: []recipe.StageColumn{
						{ID: "group_text_id", Name: "group_text"}, {ID: "group_number_id", Name: "group_number"},
						{ID: "alpha_id", Name: "alpha"}, {ID: "beta_id", Name: "beta"},
					},
				},
				{
					ID: "derive", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "pivot"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionDeriveOp, Derive: &recipe.ConstructionDerive{
						ConstructionID: "oracle_total", OutputColumnID: "total_id", Operation: recipe.DerivedAdd,
						Left:               recipe.ConstructionOperand{Kind: recipe.DerivedColumnOperand, ColumnID: "alpha_id"},
						Right:              recipe.ConstructionOperand{Kind: recipe.DerivedColumnOperand, ColumnID: "beta_id"},
						MissingInputPolicy: recipe.MissingInputPropagateNull,
					}},
					Outputs: []recipe.StageColumn{
						{ID: "group_text_id", Name: "group_text"}, {ID: "group_number_id", Name: "group_number"},
						{ID: "alpha_id", Name: "alpha"}, {ID: "beta_id", Name: "beta"}, {ID: "total_id", Name: "total"},
					},
				},
				{
					ID: "filter", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "derive"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
						ColumnID: "total_id", Operator: recipe.FilterGreaterEq,
						Values: []spec.FilterValue{{Kind: recipe.FilterDecimal, Decimal: &minimumTotal}},
					}},
					Outputs: []recipe.StageColumn{
						{ID: "group_text_id", Name: "group_text"}, {ID: "group_number_id", Name: "group_number"},
						{ID: "alpha_id", Name: "alpha"}, {ID: "beta_id", Name: "beta"}, {ID: "total_id", Name: "total"},
					},
				},
				{
					ID: "unpivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "filter"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionUnpivotOp, Unpivot: &recipe.ConstructionUnpivot{
						ConstructionID: "oracle_unpivot", Inputs: []recipe.ConstructionUnpivotInput{
							{ColumnID: "alpha_id", Key: ReshapeOracleString(stringAlpha)},
							{ColumnID: "beta_id", Key: ReshapeOracleString(stringBeta)},
						}, KeyOutputColumnID: "measure_id", ValueOutputColumnID: "amount_output_id", NullRowPolicy: recipe.UnpivotNullPreserve,
					}},
					Outputs: []recipe.StageColumn{
						{ID: "group_text_id", Name: "group_text"}, {ID: "group_number_id", Name: "group_number"},
						{ID: "total_id", Name: "total"}, {ID: "measure_id", Name: "measure"}, {ID: "amount_output_id", Name: "amount"},
					},
				},
			},
		},
	}
}

func ConstructionNumericEqual(value any, want float64) bool {
	switch got := value.(type) {
	case float64:
		return math.Abs(got-want) < 1e-9
	case int64:
		return float64(got) == want
	case int:
		return float64(got) == want
	default:
		return false
	}
}
