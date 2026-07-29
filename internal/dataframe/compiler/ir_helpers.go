package compiler

import "github.com/calypr/loom/internal/dataframe/compiler/ir"

func normalizeDatasetGeneration(generation string) string {
	return ir.NormalizeDatasetGeneration(generation)
}
func physicalScopeWindowEnd(operations []PhysicalOperation, start int) int {
	return ir.PhysicalScopeWindowEnd(operations, start)
}
func physicalPlanDiagnostics(plan PhysicalPlan) CompilerPlanDiagnostics {
	return ir.PhysicalPlanDiagnostics(plan)
}
