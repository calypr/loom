package compiler

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

const MaxColumnPopulationBatch = 16

type CompiledColumnPopulationScan struct {
	Query     string
	BindVars  map[string]any
	ColumnIDs []string
}

func CompileColumnPopulationScan(output lower.CompiledRecipeOutput, stageID string, columnIDs []string, policy ir.PhysicalOptimizationPolicy) (CompiledColumnPopulationScan, error) {
	if stageID == "" || len(columnIDs) == 0 || len(columnIDs) > MaxColumnPopulationBatch {
		return CompiledColumnPopulationScan{}, fmt.Errorf("column population requires a stage and 1..%d columns", MaxColumnPopulationBatch)
	}
	stage, found := compiledStageByID(output.Stages, stageID)
	if !found {
		return CompiledColumnPopulationScan{}, &CategoryScanRefusal{Code: CategoryScanStageUnknown, Column: stageID}
	}
	selected := make([]lower.CompiledOutputColumn, 0, len(columnIDs))
	seen := map[string]bool{}
	for _, id := range columnIDs {
		if id == "" || seen[id] {
			return CompiledColumnPopulationScan{}, fmt.Errorf("column population IDs must be nonempty and unique")
		}
		seen[id] = true
		column, err := categoryScanStageColumn(stage.Columns, id)
		if err != nil {
			return CompiledColumnPopulationScan{}, err
		}
		if column.Cardinality != string(expression.RequiredOne) && column.Cardinality != string(expression.OptionalOne) {
			return CompiledColumnPopulationScan{}, &CategoryScanRefusal{Code: CategoryScanColumnRepeated, Column: id}
		}
		selected = append(selected, column)
	}
	stageOutput := output
	stageOutput.OutputSchema = lower.CloneCompiledOutputSchema(stage.Columns)
	stageOutput.OptimizedPlan = nil
	stageOutput.Plan = ir.ClonePhysicalPlan(output.Plan)
	if stageOutput.Plan.StageSequence != nil {
		sequence := stageOutput.Plan.StageSequence
		if stageID == sequence.SourceStageID {
			stageOutput.Plan.StageSequence = nil
		} else {
			index := -1
			for i := range sequence.Stages {
				if sequence.Stages[i].ID == stageID {
					index = i
					break
				}
			}
			if index < 0 {
				return CompiledColumnPopulationScan{}, &CategoryScanRefusal{Code: CategoryScanStageUnknown, Column: stageID}
			}
			sequence.Stages = sequence.Stages[:index+1]
			last := sequence.Stages[index]
			sequence.FinalStageID = last.ID
			sequence.FinalRowIdentity = last.RowIdentityColumn
			sequence.FinalColumns = append(sequence.FinalColumns[:0], last.OutputColumns...)
			sequence.PreviewLimitBindKey = ""
			sequence.PreviewSourceWindowByRootID = false
			sequence.PreviewTerminalPivotWindow = false
			sequence.CellTraceReturn = nil
			sequence.RowLineageReturn = nil
			sequence.PopulationMappingReturn = nil
		}
	} else if stageID != recipe.ConstructionSourceProjectionID {
		return CompiledColumnPopulationScan{}, &CategoryScanRefusal{Code: CategoryScanStageUnknown, Column: stageID}
	}
	physical, err := categoryScanPhysicalPlan(stageOutput, policy)
	if err != nil {
		return CompiledColumnPopulationScan{}, err
	}
	if physical.StageSequence == nil {
		physical = narrowSourceCategoryScanProjection(physical, populationColumnNames(selected)...)
	}
	physical, err = withGenericPhysicalExecutionWindow(physical, 0)
	if err != nil {
		return CompiledColumnPopulationScan{}, fmt.Errorf("apply column population execution window: %w", err)
	}
	rendered, err := aql.Render(physical, aql.Options{})
	if err != nil {
		return CompiledColumnPopulationScan{}, fmt.Errorf("render column population stage: %w", err)
	}
	binds := make(map[string]any, len(rendered.BindVars))
	for key, value := range rendered.BindVars {
		binds[key] = value
	}
	var counts []string
	for i, column := range selected {
		key := fmt.Sprintf("__loom_population_column_%d", i)
		binds[key] = column.Name
		counts = append(counts, fmt.Sprintf("%s = SUM(row[@%s] != null ? 1 : 0)", populationResultKey(i), key))
	}
	query := "LET __loom_population_rows = (\n" + rendered.Query + "\n)\nFOR row IN __loom_population_rows\nCOLLECT AGGREGATE " + strings.Join(counts, ", ") + "\nRETURN {"
	fields := make([]string, 0, len(selected))
	for i := range selected {
		fields = append(fields, fmt.Sprintf("%s: NOT_NULL(%s, 0) > 0", populationResultKey(i), populationResultKey(i)))
	}
	query += strings.Join(fields, ", ") + "}"
	ids := append([]string(nil), columnIDs...)
	return CompiledColumnPopulationScan{Query: query, BindVars: binds, ColumnIDs: ids}, nil
}

func populationResultKey(i int) string { return fmt.Sprintf("populated_%d", i) }
func populationColumnNames(columns []lower.CompiledOutputColumn) []string {
	names := make([]string, 0, len(columns))
	for _, c := range columns {
		names = append(names, c.Name)
	}
	return names
}
