package compiler

import "github.com/calypr/loom/internal/dataframe/compiler/ir"

func clonePhysicalPlan(plan PhysicalPlan) PhysicalPlan { return ir.ClonePhysicalPlan(plan) }

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	return append([]string(nil), in...)
}

func cloneRowIdentity(identity *RowIdentity) *RowIdentity {
	if identity == nil {
		return nil
	}
	copy := *identity
	copy.Fields = cloneStrings(identity.Fields)
	return &copy
}
