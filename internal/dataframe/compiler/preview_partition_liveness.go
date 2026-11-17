package compiler

import "github.com/calypr/loom/internal/dataframe/compiler/ir"

// prunePreviewPartitionPlan removes only unreferenced scalar VALUE/LITERAL
// expression LETs from a validated source-row plan. All other operations stay
// in their original order: they may affect scope, authorization, membership,
// identity, ordering, validation, or runtime errors. Complex live expressions
// and their subplans remain intact.
//
// The reference walker is deliberately closed over the current typed IR. Any
// unrecognized operation or expression makes the proof fail and leaves the
// caller's full-row query as its fallback.
func prunePreviewPartitionPlan(plan ir.PhysicalPlan) (ir.PhysicalPlan, bool) {
	if plan.Validate() != nil || !previewLivenessPlanShape(plan) {
		return ir.PhysicalPlan{}, false
	}
	uses := make(map[string]bool)
	for _, operation := range plan.Operations {
		if !collectPreviewOperationUses(operation, uses) {
			// PhysicalPlan.Validate owns the source-plan proof. If this visitor
			// encounters a newer payload, keep the entire plan intact and let
			// terminal projection partitioning proceed without liveness pruning.
			return ir.ClonePhysicalPlan(plan), true
		}
	}
	out := ir.ClonePhysicalPlan(plan)
	operations := make([]ir.PhysicalOperation, 0, len(out.Operations))
	removed := 0
	for _, operation := range out.Operations {
		if operation.Kind == ir.PhysicalExpressionLetOp && operation.ExpressionLet != nil &&
			previewExpressionLetIsPureTotal(*operation.ExpressionLet) && !uses[operation.ExpressionLet.Variable] {
			removed++
			continue
		}
		operations = append(operations, operation)
	}
	if removed == 0 {
		return ir.ClonePhysicalPlan(plan), true
	}
	out.Operations = operations
	if out.Validate() != nil {
		return ir.PhysicalPlan{}, false
	}
	return out, true
}

func previewLivenessPlanShape(plan ir.PhysicalPlan) bool {
	if plan.StageSequence != nil || plan.Engine == ir.PhysicalEngineClickHouse || len(plan.Operations) < 2 {
		return false
	}
	if plan.Operations[0].Kind != ir.PhysicalRootScanOp || plan.Operations[0].RootScan == nil ||
		plan.Operations[0].RootScan.Population != nil || plan.Operations[0].RootScan.CohortSource != nil {
		return false
	}
	last := len(plan.Operations) - 1
	if plan.Operations[last].Kind != ir.PhysicalReturnOp || plan.Operations[last].Return == nil {
		return false
	}
	for index, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 {
				return false
			}
		case ir.PhysicalTraversalOp, ir.PhysicalFilterOp, ir.PhysicalDerivedLetOp,
			ir.PhysicalExpressionLetOp, ir.PhysicalSetOp, ir.PhysicalSortOp,
			ir.PhysicalLimitOp:
			if index == last {
				return false
			}
		case ir.PhysicalReturnOp:
			if index != last {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func previewExpressionLetIsPureTotal(operation ir.PhysicalExpressionLet) bool {
	expression := operation.Expression
	if expression.Cardinality != ir.PhysicalScalarCardinality || expression.NullBehavior != ir.PhysicalPreserveNull {
		return false
	}
	switch expression.Kind {
	case ir.PhysicalValueExpression:
		return expression.Value != nil && noOtherExpressionPayload(expression)
	case ir.PhysicalLiteralExpression:
		return expression.Literal != nil && expression.Literal.BindKey != "" && noOtherExpressionPayload(expression)
	default:
		return false
	}
}

func noOtherExpressionPayload(expression ir.PhysicalExpression) bool {
	return expression.Extract == nil && expression.Aggregate == nil && expression.Pivot == nil &&
		expression.OwnerRecords == nil && expression.Slice == nil && expression.ObjectLookup == nil &&
		expression.KeyedMap == nil && expression.ObjectKeys == nil && expression.KeySet == nil &&
		expression.Object == nil && expression.Subplan == nil && expression.Call == nil &&
		expression.RelatedField == nil &&
		(expression.Kind == ir.PhysicalValueExpression && expression.Value != nil && expression.Literal == nil ||
			expression.Kind == ir.PhysicalLiteralExpression && expression.Literal != nil && expression.Value == nil)
}

func collectPreviewOperationUses(operation ir.PhysicalOperation, uses map[string]bool) bool {
	return collectPreviewNodeUses(&operation, uses)
}

// collectPreviewNodeUses follows the typed physical child graph and extracts
// the few variable-name fields that are not PhysicalValue nodes. Subplans are
// deliberate traversal boundaries: their declared Captures summarize all
// references to outer scopes, while their local operation variables must not
// accidentally keep unrelated source LETs live.
func collectPreviewNodeUses(root ir.Node, uses map[string]bool) bool {
	active := make(map[ir.Node]bool)
	var walk func(ir.Node) bool
	walk = func(node ir.Node) bool {
		if node == nil {
			return true
		}
		if active[node] {
			return false
		}
		active[node] = true
		defer delete(active, node)

		continueChildren := true
		switch value := node.(type) {
		case *ir.PhysicalOperation:
			if !previewOperationPayloadSupported(value) {
				return false
			}
			switch value.Kind {
			case ir.PhysicalTraversalOp:
				markPreviewVariable(uses, value.Traversal.SourceVariable)
			case ir.PhysicalSetOp:
				markPreviewVariable(uses, value.Set.SourceSetVariable)
				if value.Set.Prepared != nil {
					markPreviewVariable(uses, value.Set.Prepared.SourceSetVariable)
				}
				if value.Set.Reduction != nil {
					markPreviewVariable(uses, value.Set.Reduction.SourceSetVariable)
				}
			}
		case *ir.PhysicalExpression:
			if !previewExpressionPayloadSupported(value) {
				return false
			}
			switch value.Kind {
			case ir.PhysicalExtractExpression:
				if value.Extract.Prepared != nil {
					markPreviewVariable(uses, value.Extract.Prepared.SetVariable)
				}
			case ir.PhysicalPivotExpression:
				if value.Pivot.PreparedKey != nil {
					markPreviewVariable(uses, value.Pivot.PreparedKey.SetVariable)
				}
				if value.Pivot.PreparedValue != nil {
					markPreviewVariable(uses, value.Pivot.PreparedValue.SetVariable)
				}
			case ir.PhysicalObjectLookupExpression:
				markPreviewVariable(uses, value.ObjectLookup.ObjectVariable)
			case ir.PhysicalObjectKeysExpression:
				markPreviewVariable(uses, value.ObjectKeys.ObjectVariable)
			}
		case *ir.PhysicalPredicateExpression:
			if !previewPredicatePayloadSupported(value) {
				return false
			}
		case *ir.PhysicalSubplan:
			collectPreviewCaptures(value.Captures, uses)
			if value.Sort != nil {
				markPreviewVariable(uses, value.Sort.Variable)
			}
			if value.DistinctBy != nil {
				markPreviewVariable(uses, value.DistinctBy.Variable)
			}
			continueChildren = false
		case *ir.PhysicalValue:
			markPreviewVariable(uses, value.Variable)
		default:
			return false
		}
		if !continueChildren {
			return true
		}
		for _, child := range ir.Children(node) {
			if !walk(child) {
				return false
			}
		}
		return true
	}
	return walk(root)
}

func markPreviewVariable(uses map[string]bool, name string) {
	if name != "" {
		uses[name] = true
	}
}

func previewOperationPayloadSupported(operation *ir.PhysicalOperation) bool {
	if operation == nil {
		return false
	}
	switch operation.Kind {
	case ir.PhysicalRootScanOp:
		return operation.RootScan != nil
	case ir.PhysicalTraversalOp:
		return operation.Traversal != nil
	case ir.PhysicalFilterOp:
		return operation.Filter != nil
	case ir.PhysicalDerivedLetOp:
		return operation.DerivedLet != nil
	case ir.PhysicalExpressionLetOp:
		return operation.ExpressionLet != nil
	case ir.PhysicalSetOp:
		return operation.Set != nil
	case ir.PhysicalSortOp:
		return operation.Sort != nil
	case ir.PhysicalLimitOp:
		return operation.Limit != nil
	case ir.PhysicalReturnOp:
		return operation.Return != nil
	default:
		return false
	}
}

func previewExpressionPayloadSupported(expression *ir.PhysicalExpression) bool {
	if expression == nil {
		return false
	}
	switch expression.Kind {
	case ir.PhysicalValueExpression:
		return expression.Value != nil
	case ir.PhysicalLiteralExpression:
		return expression.Literal != nil
	case ir.PhysicalExtractExpression:
		return expression.Extract != nil
	case ir.PhysicalAggregateExpression:
		return expression.Aggregate != nil
	case ir.PhysicalPivotExpression:
		return expression.Pivot != nil
	case ir.PhysicalOwnerRecordsExpression:
		return expression.OwnerRecords != nil
	case ir.PhysicalSliceExpression:
		return expression.Slice != nil
	case ir.PhysicalObjectLookupExpression:
		return expression.ObjectLookup != nil
	case ir.PhysicalKeyedMapExpression:
		return expression.KeyedMap != nil
	case ir.PhysicalObjectKeysExpression:
		return expression.ObjectKeys != nil
	case ir.PhysicalKeySetExpression:
		return expression.KeySet != nil
	case ir.PhysicalObjectExpression:
		return expression.Object != nil
	case ir.PhysicalSubplanExpression:
		return expression.Subplan != nil
	case ir.PhysicalCallExpression:
		return expression.Call != nil
	case ir.PhysicalRelatedFieldExpression:
		return expression.RelatedField != nil
	default:
		return false
	}
}

func previewPredicatePayloadSupported(predicate *ir.PhysicalPredicateExpression) bool {
	if predicate == nil {
		return false
	}
	switch predicate.Kind {
	case ir.PhysicalComparisonPredicate:
		return predicate.Comparison != nil
	case ir.PhysicalAllPredicate, ir.PhysicalAnyPredicate, ir.PhysicalNotPredicate:
		return true
	case ir.PhysicalExistsPredicate:
		return predicate.Exists != nil
	default:
		return false
	}
}

func collectPreviewCaptures(captures []string, uses map[string]bool) {
	for _, capture := range captures {
		markPreviewVariable(uses, capture)
	}
}
