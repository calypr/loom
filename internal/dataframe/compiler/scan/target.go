package scan

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
)

type Target interface{ scanTarget() }

type StageColumn struct {
	StageID       string
	ColumnID      string
	ValueColumnID string
	RequireValue  bool
}

func (StageColumn) scanTarget() {}

type OutputColumn struct {
	Column       string
	ValueColumn  string
	RequireValue bool
}

func (OutputColumn) scanTarget() {}

func Compile(output lower.CompiledRecipeOutput, target Target, maxValues int, policy ir.PhysicalOptimizationPolicy) (CompiledCategoryScanQuery, error) {
	switch target := target.(type) {
	case StageColumn:
		return compileCategoryScanStageWithValuePolicy(output, target.StageID, target.ColumnID, target.ValueColumnID, maxValues, policy, target.RequireValue)
	case OutputColumn:
		if target.RequireValue {
			return compileOutputColumnRequiringValue(output, target.Column, target.ValueColumn, maxValues, policy)
		}
		return compileOutputColumn(output, target.Column, maxValues, policy)
	default:
		return CompiledCategoryScanQuery{}, fmt.Errorf("unsupported category scan target %T", target)
	}
}
