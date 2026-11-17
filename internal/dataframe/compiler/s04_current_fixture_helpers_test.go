package compiler

import (
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"testing"
)

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
func bindOracleCurrentDerived(output recipe.Output) recipe.Output {
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

func TestS04CurrentConstructionFixtureCompilation(t *testing.T) {
	output := reshapeOracleOutput("sum", &oracleReshape{Kind: oracleReshapePivot, GroupedPivot: &oraclePivot{ConstructionID: "s04-pivot-sum", GroupKeys: []string{"group_text", "group_number"}, CategoryColumn: "string_category", ValueColumn: "numeric_value", Categories: []oraclePivotCategory{{Key: reshapeOracleString("alpha"), Output: "alpha", Label: "Alpha"}, {Key: reshapeOracleString("zero"), Output: "zero", Label: "Zero"}}, DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull, UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryExcludeWithEvidence}})
	output.DerivedColumns = []recipe.DerivedColumn{{ConstructionID: "s04-derived-after-pivot", Name: "alpha_plus_zero", Operation: recipe.DerivedAdd, Left: recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "alpha"}, Right: recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "zero"}, MissingInputPolicy: recipe.MissingInputPropagateNull}}
	compiled, _, err := compileReshapeOracle(output, "project", "generation", 100)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.Plan.StageSequence == nil || len(compiled.Plan.StageSequence.Stages) != 2 || compiled.Plan.StageSequence.Stages[0].Kind != ir.PhysicalStagePivotOp || compiled.Plan.StageSequence.Stages[1].Kind != ir.PhysicalStageDeriveOp {
		t.Fatal("S04 fixture bypassed current Pivot/Derive stages")
	}
	for _, stage := range compiled.Plan.StageSequence.Stages {
		found := false
		for _, column := range stage.OutputColumns {
			if column.Name == "__loom_reshape_unlisted_count" && column.Internal {
				found = true
			}
		}
		if !found {
			t.Fatalf("stage %s dropped compiler-owned Pivot exclusion count", stage.ID)
		}
	}
	foundCount := false
	for _, column := range compiled.Plan.StageSequence.FinalColumns {
		if column.Name == "__loom_reshape_unlisted_count" && column.Internal {
			foundCount = true
		}
	}
	if !foundCount {
		t.Fatal("final output dropped Pivot exclusion count")
	}
	if _, err := CompileCellTraceOutputWithPolicy(compiled, "alpha_plus_zero", 0, 100, ir.DefaultPhysicalOptimizationPolicy()); err != nil {
		t.Fatal(err)
	}
	if _, err := CompileTableShapeExclusionsWithPolicy(compiled, 0, 100, ir.DefaultPhysicalOptimizationPolicy()); err != nil {
		t.Fatal(err)
	}
	assertReshapeOracleUnpivotRejectsIncompatibleInputs(t, "project", "generation")
	for _, policy := range []recipe.UnpivotNullRowPolicy{recipe.UnpivotNullPreserve, recipe.UnpivotNullDrop} {
		compiled, _, err := compileReshapeOracle(reshapeOracleUnpivotOutput(policy, "s04-unpivot"), "project", "generation", 100)
		if err != nil {
			t.Fatal(err)
		}
		if compiled.Plan.StageSequence == nil || compiled.Plan.StageSequence.Stages[0].Kind != ir.PhysicalStageUnpivotOp {
			t.Fatal("S04 fixture bypassed current Unpivot stage")
		}
		if outputColumn(compiled.OutputSchema, "amount") != "decimal" {
			t.Fatal("S04 integer/decimal unpivot widening changed")
		}
	}
}
