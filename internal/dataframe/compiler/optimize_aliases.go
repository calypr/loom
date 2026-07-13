package compiler

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/optimize"
)

var (
	OptimizePhysicalPlan           = optimize.OptimizePhysicalPlan
	OptimizePhysicalPlanWithPolicy = optimize.OptimizePhysicalPlanWithPolicy
)

func valueString(value any) string {
	return fmt.Sprint(value)
}
