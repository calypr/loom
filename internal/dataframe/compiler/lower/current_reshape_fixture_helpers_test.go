package lower

import "github.com/calypr/loom/internal/dataframe/recipe"

// These are test-only literal case declarations. They build the current
// recipe.Construction contract directly and are never accepted by production.
type oracleReshape struct {
	Kind         string
	GroupedPivot *oraclePivot
	Unpivot      *oracleUnpivot
}

const (
	oracleReshapePivot   = "GROUPED_PIVOT"
	oracleReshapeUnpivot = "UNPIVOT"
)

type oraclePivot struct {
	ConstructionID              string
	GroupKeys                   []string
	CategoryColumn, ValueColumn string
	Categories                  []oraclePivotCategory
	DuplicatePolicy             recipe.PivotDuplicatePolicy
	MissingCellPolicy           recipe.PivotMissingCellPolicy
	UnlistedCategoryPolicy      recipe.PivotUnlistedCategoryPolicy
}
type oraclePivotCategory struct {
	Key           recipe.TableScalar
	Output, Label string
}
type oracleUnpivot struct {
	ConstructionID                               string
	Inputs                                       []oracleUnpivotInput
	KeyOutput, KeyLabel, ValueOutput, ValueLabel string
	NullRowPolicy                                recipe.UnpivotNullRowPolicy
}
type oracleUnpivotInput struct {
	Column string
	Key    recipe.TableScalar
}

func bindOracleCurrentConstruction(output recipe.Output, shape *oracleReshape) recipe.Output {
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
	case oracleReshapePivot:
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
	case oracleReshapeUnpivot:
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

// S04 appends arithmetic case declarations after constructing its Pivot case.
// Bind them to explicit current DERIVE stages before entering the production frontend.
