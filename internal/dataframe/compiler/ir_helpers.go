package compiler

import "github.com/calypr/loom/internal/dataframe/compiler/ir"

func datasetGenerationBindValue(generation string) any {
	return ir.DatasetGenerationBindValue(generation)
}
func normalizeDatasetGeneration(generation string) string {
	return ir.NormalizeDatasetGeneration(generation)
}
func physicalScopeWindowEnd(operations []PhysicalOperation, start int) int {
	return ir.PhysicalScopeWindowEnd(operations, start)
}
func physicalPlanDiagnostics(plan PhysicalPlan) CompilerPlanDiagnostics {
	return ir.PhysicalPlanDiagnostics(plan)
}
func newPhysicalOptimizationReport(policy PhysicalOptimizationPolicy) PhysicalOptimizationReport {
	return ir.NewPhysicalOptimizationReport(policy)
}
func clonePhysicalOptimizationReport(report PhysicalOptimizationReport) PhysicalOptimizationReport {
	return ir.ClonePhysicalOptimizationReport(report)
}
func estimateTraversalSharingWork(prefix PhysicalTraversalPrefixDecomposition, candidateSets int) (int, int, int) {
	return ir.EstimateTraversalSharingWork(prefix, candidateSets)
}
func estimatePreparedSelectorWork(selectorUseCount int) (int, int, int) {
	return ir.EstimatePreparedSelectorWork(selectorUseCount)
}
