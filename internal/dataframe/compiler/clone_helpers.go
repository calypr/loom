package compiler

import (
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func clonePhysicalPlan(plan ir.PhysicalPlan) ir.PhysicalPlan { return ir.ClonePhysicalPlan(plan) }

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	return append([]string(nil), in...)
}

func cloneRowIdentity(identity *spec.RowIdentity) *spec.RowIdentity {
	if identity == nil {
		return nil
	}
	copy := *identity
	copy.Fields = cloneStrings(identity.Fields)
	return &copy
}
