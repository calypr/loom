package compiler

import (
	"github.com/calypr/loom/internal/dataframe/compiler/optimize"
)

var (
	OptimizePhysicalPlan           = optimize.OptimizePhysicalPlan
	OptimizePhysicalPlanWithPolicy = optimize.OptimizePhysicalPlanWithPolicy
)
