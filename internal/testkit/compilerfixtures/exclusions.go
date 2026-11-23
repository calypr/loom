package compilerfixtures

import (
	"github.com/calypr/loom/internal/dataframe/recipe"
)

type OracleReshape struct {
	Kind         string
	GroupedPivot *OraclePivot
	Unpivot      *OracleUnpivot
}

const (
	OracleReshapePivot   = "GROUPED_PIVOT"
	OracleReshapeUnpivot = "UNPIVOT"
)

type OraclePivot struct {
	ConstructionID              string
	GroupKeys                   []string
	CategoryColumn, ValueColumn string
	Categories                  []OraclePivotCategory
	DuplicatePolicy             recipe.PivotDuplicatePolicy
	MissingCellPolicy           recipe.PivotMissingCellPolicy
	UnlistedCategoryPolicy      recipe.PivotUnlistedCategoryPolicy
}

type OraclePivotCategory struct {
	Key           recipe.TableScalar
	Output, Label string
}

type OracleUnpivot struct {
	ConstructionID                               string
	Inputs                                       []OracleUnpivotInput
	KeyOutput, KeyLabel, ValueOutput, ValueLabel string
	NullRowPolicy                                recipe.UnpivotNullRowPolicy
}

type OracleUnpivotInput struct {
	Column string
	Key    recipe.TableScalar
}

func BindOracleCurrentConstruction(output recipe.Output, shape *OracleReshape) recipe.Output {
	if shape == nil {
		return output
	}
	construction := &recipe.Construction{Version: 1, Steps: []recipe.ConstructionStep{}}
	byName := map[string]recipe.StageColumn{}
	for i := range output.Fields {
		field := &output.Fields[i]
		field.ColumnID = field.Name
		column := recipe.StageColumn{ID: field.Name, Name: field.Name, Label: field.Label}
		construction.SourceColumns = append(construction.SourceColumns, column)
		byName[column.Name] = column
	}
	step := recipe.ConstructionStep{Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}}}
	switch shape.Kind {
	case OracleReshapePivot:
		p := shape.GroupedPivot
		step.ID = "pivot"
		pivot := &recipe.ConstructionPivot{ConstructionID: p.ConstructionID, GroupKeyIDs: append([]string{}, p.GroupKeys...), CategoryColumnID: p.CategoryColumn, ValueColumnID: p.ValueColumn, DuplicatePolicy: p.DuplicatePolicy, MissingCellPolicy: p.MissingCellPolicy, UnlistedCategoryPolicy: p.UnlistedCategoryPolicy}
		for _, name := range p.GroupKeys {
			column := byName[name]
			if column.ID == "" {
				column = recipe.StageColumn{ID: name, Name: name}
			}
			step.Outputs = append(step.Outputs, column)
		}
		for _, category := range p.Categories {
			pivot.Categories = append(pivot.Categories, recipe.ConstructionPivotCategory{Key: category.Key, OutputColumnID: category.Output})
			step.Outputs = append(step.Outputs, recipe.StageColumn{ID: category.Output, Name: category.Output, Label: category.Label})
		}
		step.Operation = recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: pivot}
	case OracleReshapeUnpivot:
		u := shape.Unpivot
		step.ID = "unpivot"
		unpivot := &recipe.ConstructionUnpivot{ConstructionID: u.ConstructionID, KeyOutputColumnID: u.KeyOutput, ValueOutputColumnID: u.ValueOutput, NullRowPolicy: u.NullRowPolicy}
		selected := map[string]bool{}
		for _, input := range u.Inputs {
			unpivot.Inputs = append(unpivot.Inputs, recipe.ConstructionUnpivotInput{ColumnID: input.Column, Key: input.Key})
			selected[input.Column] = true
		}
		for _, column := range construction.SourceColumns {
			if !selected[column.ID] {
				step.Outputs = append(step.Outputs, column)
			}
		}
		step.Outputs = append(step.Outputs, recipe.StageColumn{ID: u.KeyOutput, Name: u.KeyOutput, Label: u.KeyLabel}, recipe.StageColumn{ID: u.ValueOutput, Name: u.ValueOutput, Label: u.ValueLabel})
		step.Operation = recipe.ConstructionOperation{Kind: recipe.ConstructionUnpivotOp, Unpivot: unpivot}
	default:
		step.ID = "invalid-shape"
	}
	construction.Steps = append(construction.Steps, step)
	output.Construction = construction
	return output
}

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
