package compilerfixtures

import (
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func BindOracleCurrentDerived(output recipe.Output) recipe.Output {
	if output.Construction == nil || len(output.DerivedColumns) == 0 {
		return output
	}
	construction := *output.Construction
	construction.Steps = append([]recipe.ConstructionStep{}, construction.Steps...)
	for _, column := range output.DerivedColumns {
		previous := construction.Steps[len(construction.Steps)-1]
		outputs := append(append([]recipe.StageColumn{}, previous.Outputs...), recipe.StageColumn{ID: column.Name, Name: column.Name, Label: column.Label})
		operand := func(input recipe.DerivedOperand) recipe.ConstructionOperand {
			return recipe.ConstructionOperand{Kind: input.Kind, ColumnID: input.Column, Literal: input.Literal}
		}
		construction.Steps = append(construction.Steps, recipe.ConstructionStep{ID: column.ConstructionID, Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: previous.ID}}, Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionDeriveOp, Derive: &recipe.ConstructionDerive{ConstructionID: column.ConstructionID, OutputColumnID: column.Name, Operation: column.Operation, Left: operand(column.Left), Right: operand(column.Right), MissingInputPolicy: column.MissingInputPolicy, DivisionByZeroPolicy: column.DivisionByZeroPolicy}}, Outputs: outputs})
	}
	output.Construction = &construction
	output.DerivedColumns = nil
	return output
}

func CurrentOracleDerivedPivotOutput(withFilter bool) recipe.Output {
	output := CurrentOraclePivotOutput(recipe.PivotUnlistedCategoryError)
	previous := output.Construction.Steps[0]
	outputs := append(append([]recipe.StageColumn{}, previous.Outputs...), recipe.StageColumn{ID: "alpha_plus_zero", Name: "alpha_plus_zero"})
	derive := recipe.ConstructionStep{ID: "derive", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "pivot"}}, Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionDeriveOp, Derive: &recipe.ConstructionDerive{ConstructionID: "s04-derived-after-pivot", OutputColumnID: "alpha_plus_zero", Operation: recipe.DerivedAdd, Left: recipe.ConstructionOperand{Kind: recipe.DerivedColumnOperand, ColumnID: "alpha"}, Right: recipe.ConstructionOperand{Kind: recipe.DerivedColumnOperand, ColumnID: "zero"}, MissingInputPolicy: recipe.MissingInputPropagateNull}}, Outputs: outputs}
	output.Construction.Steps = append(output.Construction.Steps, derive)
	if withFilter {
		zero := 0.0
		output.Construction.Steps = append(output.Construction.Steps, recipe.ConstructionStep{ID: "filter", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "derive"}}, Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{ColumnID: "alpha_plus_zero", Operator: recipe.FilterGreaterEq, Values: []recipe.FilterValue{{Kind: recipe.FilterDecimal, Decimal: &zero}}}}, Outputs: outputs})
	}
	return output
}
