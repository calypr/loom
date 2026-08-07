package compiler

import (
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func clonePhysicalPlan(plan ir.PhysicalPlan) ir.PhysicalPlan { return ir.ClonePhysicalPlan(plan) }

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	return append([]string(nil), in...)
}
