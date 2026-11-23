package compiler

import "github.com/calypr/loom/internal/dataframe/compiler/ir"

func normalizeDatasetGeneration(generation string) string {
	return ir.NormalizeDatasetGeneration(generation)
}
func physicalScopeWindowEnd(operations []ir.PhysicalOperation, start int) int {
	return ir.PhysicalScopeWindowEnd(operations, start)
}
func physicalPlanDiagnostics(plan ir.PhysicalPlan) ir.CompilerPlanDiagnostics {
	return ir.PhysicalPlanDiagnostics(plan)
}
