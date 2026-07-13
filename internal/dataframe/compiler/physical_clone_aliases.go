package compiler

import "github.com/calypr/loom/internal/dataframe/compiler/ir"

func clonePhysicalPlan(plan PhysicalPlan) PhysicalPlan { return ir.ClonePhysicalPlan(plan) }
func clonePhysicalOperation(operation PhysicalOperation) PhysicalOperation {
	return ir.ClonePhysicalOperation(operation)
}
func clonePhysicalOperations(operations []PhysicalOperation) []PhysicalOperation {
	return ir.ClonePhysicalOperations(operations)
}
func clonePhysicalPredicate(predicate PhysicalPredicate) PhysicalPredicate {
	return ir.ClonePhysicalPredicate(predicate)
}
func clonePhysicalPredicateExpression(predicate PhysicalPredicateExpression) PhysicalPredicateExpression {
	return ir.ClonePhysicalPredicateExpression(predicate)
}
func clonePhysicalExpression(expression PhysicalExpression) PhysicalExpression {
	return ir.ClonePhysicalExpression(expression)
}
func clonePhysicalSubplan(subplan PhysicalSubplan) PhysicalSubplan {
	return ir.ClonePhysicalSubplan(subplan)
}
