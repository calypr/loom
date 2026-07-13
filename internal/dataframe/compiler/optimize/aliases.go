package optimize

import (
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

type (
	PhysicalPlan                         = ir.PhysicalPlan
	PhysicalOperation                    = ir.PhysicalOperation
	PhysicalOperationKind                = ir.PhysicalOperationKind
	PhysicalFilter                       = ir.PhysicalFilter
	PhysicalDerivedLet                   = ir.PhysicalDerivedLet
	PhysicalProjection                   = ir.PhysicalProjection
	PhysicalReturn                       = ir.PhysicalReturn
	PhysicalAggregate                    = ir.PhysicalAggregate
	PhysicalPivotMap                     = ir.PhysicalPivotMap
	PhysicalSlice                        = ir.PhysicalSlice
	PhysicalSet                          = ir.PhysicalSet
	PhysicalSetProjection                = ir.PhysicalSetProjection
	PhysicalSetProjectionField           = ir.PhysicalSetProjectionField
	PhysicalExpressionProjection         = ir.PhysicalExpressionProjection
	PhysicalPreparedReference            = ir.PhysicalPreparedReference
	PhysicalTraversal                    = ir.PhysicalTraversal
	PhysicalTraversalPrefixDecomposition = ir.PhysicalTraversalPrefixDecomposition
	PhysicalValue                        = ir.PhysicalValue
	PhysicalExpression                   = ir.PhysicalExpression
	PhysicalPredicateExpression          = ir.PhysicalPredicateExpression
	PhysicalPredicate                    = ir.PhysicalPredicate
	PhysicalSubplan                      = ir.PhysicalSubplan
	PhysicalOptimizationPolicy           = ir.PhysicalOptimizationPolicy
	PhysicalOptimizationRule             = ir.PhysicalOptimizationRule
	PhysicalOptimizationDecision         = ir.PhysicalOptimizationDecision
	Selector                             = spec.Selector
)

const (
	PhysicalSetOp                             = ir.PhysicalSetOp
	PhysicalFilterOp                          = ir.PhysicalFilterOp
	PhysicalTraversalOp                       = ir.PhysicalTraversalOp
	PhysicalOptimizationRuleTraversalSharing  = ir.PhysicalOptimizationRuleTraversalSharing
	PhysicalOptimizationRuleEndpointTraversal = ir.PhysicalOptimizationRuleEndpointTraversal
	PhysicalTraversalEndpointLookup           = ir.PhysicalTraversalEndpointLookup
	PhysicalValueExpression                   = ir.PhysicalValueExpression
	PhysicalObjectCardinality                 = ir.PhysicalObjectCardinality
	PhysicalPreserveNull                      = ir.PhysicalPreserveNull
	OptimizerRuleTraversalSharing             = "share_identical_traversals"
)

var (
	DefaultPhysicalOptimizationPolicy = ir.DefaultPhysicalOptimizationPolicy
	DecomposePhysicalTraversalPrefix  = ir.DecomposePhysicalTraversalPrefix
)

func clonePhysicalPlan(plan PhysicalPlan) PhysicalPlan { return ir.ClonePhysicalPlan(plan) }
func clonePhysicalOperation(operation PhysicalOperation) PhysicalOperation {
	return ir.ClonePhysicalOperation(operation)
}
func clonePhysicalOperations(operations []PhysicalOperation) []PhysicalOperation {
	return ir.ClonePhysicalOperations(operations)
}
func clonePhysicalPredicateExpression(predicate PhysicalPredicateExpression) PhysicalPredicateExpression {
	return ir.ClonePhysicalPredicateExpression(predicate)
}
func clonePhysicalSubplan(subplan PhysicalSubplan) PhysicalSubplan {
	return ir.ClonePhysicalSubplan(subplan)
}

func newPhysicalOptimizationReport(policy PhysicalOptimizationPolicy) ir.PhysicalOptimizationReport {
	return ir.NewPhysicalOptimizationReport(policy)
}

func estimateTraversalSharingWork(prefix PhysicalTraversalPrefixDecomposition, candidateSets int) (int, int, int) {
	return ir.EstimateTraversalSharingWork(prefix, candidateSets)
}

func sanitizeColumnName(in string) string {
	var out []rune
	for _, r := range in {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			out = append(out, r)
		} else {
			out = append(out, '_')
		}
	}
	return string(out)
}

type storageRoute struct{ Direction ir.PhysicalTraversalDirection }

func (route storageRoute) endpointLookupFields() (string, string, []string, bool) {
	switch route.Direction {
	case ir.PhysicalInbound:
		return "_to", "_from", []string{"_to", "project", "dataset_generation", "label", "from_type"}, true
	case ir.PhysicalOutbound:
		return "_from", "_to", []string{"_from", "project", "dataset_generation", "label", "to_type"}, true
	default:
		return "", "", nil, false
	}
}
