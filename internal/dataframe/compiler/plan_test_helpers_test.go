package compiler

import (
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func buildGenericPhysicalPlan(plan semantic.SemanticPlan) (ir.PhysicalPlan, error) {
	return lower.BuildGenericPhysicalPlanWithPolicy(plan, ir.DefaultPhysicalOptimizationPolicy())
}
