package compiler

import "github.com/calypr/loom/internal/dataframe/compiler/ir"

func prunePreviewPartitionPlan(plan ir.PhysicalPlan) (ir.PhysicalPlan, bool) {
	if plan.Validate() != nil || !previewLivenessPlanShape(plan) {
		return ir.PhysicalPlan{}, false
	}
	uses := make(map[string]bool)
	for _, operation := range plan.Operations {
		if !collectPreviewOperationUses(operation, uses) {

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
	value := func(value ir.PhysicalValue) {
		if value.Variable != "" {
			uses[value.Variable] = true
		}
	}
	expression := func(value ir.PhysicalExpression) bool { return collectPreviewExpressionUses(value, uses) }
	predicate := func(value ir.PhysicalPredicateExpression) bool { return collectPreviewPredicateUses(value, uses) }
	switch operation.Kind {
	case ir.PhysicalRootScanOp:
		if operation.RootScan == nil {
			return false
		}
		seed := operation.RootScan.PageCandidateSeed
		if seed != nil {
			collectPreviewSubplanUses(seed.Subplan, uses)
		}
	case ir.PhysicalTraversalOp:
		if operation.Traversal == nil {
			return false
		}
		if operation.Traversal.SourceVariable != "" {
			uses[operation.Traversal.SourceVariable] = true
		}
	case ir.PhysicalFilterOp:
		if operation.Filter == nil {
			return false
		}
		value(operation.Filter.Predicate.Left)
		if operation.Filter.Predicate.LeftExpression != nil && !expression(*operation.Filter.Predicate.LeftExpression) {
			return false
		}
		if operation.Filter.Predicate.Right != nil {
			value(*operation.Filter.Predicate.Right)
		}
		if operation.Filter.Predicate.Correlation != nil {
			value(operation.Filter.Predicate.Correlation.Source)
		}
		if operation.Filter.Expression != nil && !predicate(*operation.Filter.Expression) {
			return false
		}
	case ir.PhysicalDerivedLetOp:
		if operation.DerivedLet == nil {
			return false
		}
		for _, input := range operation.DerivedLet.Inputs {
			value(input)
		}
	case ir.PhysicalExpressionLetOp:
		if operation.ExpressionLet == nil || !expression(operation.ExpressionLet.Expression) {
			return false
		}
	case ir.PhysicalSetOp:
		if operation.Set == nil {
			return false
		}
		if operation.Set.SourceSetVariable != "" {
			uses[operation.Set.SourceSetVariable] = true
		}
		if operation.Set.Prepared != nil && operation.Set.Prepared.SourceSetVariable != "" {
			uses[operation.Set.Prepared.SourceSetVariable] = true
		}
		if operation.Set.Reduction != nil && operation.Set.Reduction.SourceSetVariable != "" {
			uses[operation.Set.Reduction.SourceSetVariable] = true
		}
		collectPreviewSubplanUses(operation.Set.Subplan, uses)
	case ir.PhysicalSortOp:
		if operation.Sort == nil {
			return false
		}
		for _, key := range operation.Sort.Keys {
			value(key)
		}
	case ir.PhysicalLimitOp:
		if operation.Limit == nil {
			return false
		}
	case ir.PhysicalReturnOp:
		if operation.Return == nil {
			return false
		}
		for _, projection := range operation.Return.Projections {
			value(projection.Value)
			if projection.Expression != nil && !expression(*projection.Expression) {
				return false
			}
			if projection.Presence != nil {
				value(projection.Presence.Source)
			}
		}
	default:
		return false
	}
	return true
}

func collectPreviewCaptures(captures []string, uses map[string]bool) {
	for _, capture := range captures {
		if capture != "" {
			uses[capture] = true
		}
	}
}

func collectPreviewSubplanUses(subplan ir.PhysicalSubplan, uses map[string]bool) {
	collectPreviewCaptures(subplan.Captures, uses)
	if subplan.Sort != nil && subplan.Sort.Variable != "" {
		uses[subplan.Sort.Variable] = true
	}
	if subplan.DistinctBy != nil && subplan.DistinctBy.Variable != "" {
		uses[subplan.DistinctBy.Variable] = true
	}
}

func collectPreviewExpressionUses(expression ir.PhysicalExpression, uses map[string]bool) bool {
	value := func(value ir.PhysicalValue) {
		if value.Variable != "" {
			uses[value.Variable] = true
		}
	}
	predicate := func(value *ir.PhysicalPredicateExpression) bool {
		return value == nil || collectPreviewPredicateUses(*value, uses)
	}

	switch expression.Kind {
	case ir.PhysicalValueExpression:
		if expression.Value == nil {
			return false
		}
		value(*expression.Value)
	case ir.PhysicalLiteralExpression:
		if expression.Literal == nil {
			return false
		}
	case ir.PhysicalExtractExpression:
		if expression.Extract == nil {
			return false
		}
		value(expression.Extract.Source)
		for _, fallback := range expression.Extract.Fallbacks {
			value(fallback.Source)
		}
		if expression.Extract.Prepared != nil && expression.Extract.Prepared.SetVariable != "" {
			uses[expression.Extract.Prepared.SetVariable] = true
		}
	case ir.PhysicalAggregateExpression:
		item := expression.Aggregate
		if item == nil {
			return false
		}
		value(item.Source)
		if item.Value != nil && !collectPreviewExpressionUses(*item.Value, uses) {
			return false
		}
		if !predicate(item.Predicate) {
			return false
		}
		if item.ContributorWindow != nil {
			if !collectPreviewExpressionUses(item.ContributorWindow.Timestamp, uses) || !collectPreviewExpressionUses(item.ContributorWindow.Anchor, uses) {
				return false
			}
		}
		if item.Ordering != nil && !collectPreviewExpressionUses(item.Ordering.Timestamp, uses) {
			return false
		}
	case ir.PhysicalPivotExpression:
		item := expression.Pivot
		if item == nil {
			return false
		}
		value(item.Source)
		if item.PreparedKey != nil && item.PreparedKey.SetVariable != "" {
			uses[item.PreparedKey.SetVariable] = true
		}
		if item.PreparedValue != nil && item.PreparedValue.SetVariable != "" {
			uses[item.PreparedValue.SetVariable] = true
		}
		if item.Correlation != nil {
			value(item.Correlation.Source)
		}
	case ir.PhysicalOwnerRecordsExpression:
		if expression.OwnerRecords == nil {
			return false
		}
		value(expression.OwnerRecords.Correlation.Source)
	case ir.PhysicalSliceExpression:
		item := expression.Slice
		if item == nil {
			return false
		}
		value(item.Source)
		if !predicate(item.Predicate) {
			return false
		}
		if item.Sort != nil && !collectPreviewExpressionUses(*item.Sort, uses) {
			return false
		}
		for _, projection := range item.Projections {
			if !collectPreviewExpressionUses(projection.Expression, uses) {
				return false
			}
		}
	case ir.PhysicalObjectLookupExpression:
		if expression.ObjectLookup == nil {
			return false
		}
		if expression.ObjectLookup.ObjectVariable != "" {
			uses[expression.ObjectLookup.ObjectVariable] = true
		}
	case ir.PhysicalKeyedMapExpression:
		item := expression.KeyedMap
		if item == nil {
			return false
		}
		if !collectPreviewExpressionUses(item.Source, uses) || !collectPreviewExpressionUses(item.ItemKey, uses) || !collectPreviewExpressionUses(item.ItemValue, uses) {
			return false
		}
		for _, fallback := range item.ValueFallbacks {
			if !collectPreviewExpressionUses(fallback, uses) {
				return false
			}
		}
	case ir.PhysicalObjectKeysExpression:
		if expression.ObjectKeys == nil {
			return false
		}
		if expression.ObjectKeys.ObjectVariable != "" {
			uses[expression.ObjectKeys.ObjectVariable] = true
		}
	case ir.PhysicalKeySetExpression:
		item := expression.KeySet
		if item == nil {
			return false
		}
		if !collectPreviewExpressionUses(item.Source, uses) || !collectPreviewExpressionUses(item.ItemKey, uses) {
			return false
		}
	case ir.PhysicalObjectExpression:
		if expression.Object == nil {
			return false
		}
		for _, field := range expression.Object.Fields {
			if !collectPreviewExpressionUses(field.Expression, uses) {
				return false
			}
		}
	case ir.PhysicalSubplanExpression:
		if expression.Subplan == nil {
			return false
		}
		collectPreviewSubplanUses(*expression.Subplan, uses)
	case ir.PhysicalCallExpression:
		item := expression.Call
		if item == nil {
			return false
		}
		for _, argument := range item.Args {
			if !collectPreviewExpressionUses(argument, uses) {
				return false
			}
		}
	case ir.PhysicalRelatedFieldExpression:
		if expression.RelatedField == nil {
			return false
		}
		value(expression.RelatedField.DocumentID)
	default:
		return false
	}
	return true
}

func collectPreviewPredicateUses(predicate ir.PhysicalPredicateExpression, uses map[string]bool) bool {
	switch predicate.Kind {
	case ir.PhysicalComparisonPredicate:
		if predicate.Comparison == nil {
			return false
		}
		comparison := predicate.Comparison
		if comparison.Left.Variable != "" {
			uses[comparison.Left.Variable] = true
		}
		if comparison.LeftExpression != nil && !collectPreviewExpressionUses(*comparison.LeftExpression, uses) {
			return false
		}
		if comparison.Right != nil && comparison.Right.Variable != "" {
			uses[comparison.Right.Variable] = true
		}
		if comparison.Correlation != nil && comparison.Correlation.Source.Variable != "" {
			uses[comparison.Correlation.Source.Variable] = true
		}
	case ir.PhysicalAllPredicate, ir.PhysicalAnyPredicate, ir.PhysicalNotPredicate:
		for _, child := range predicate.Children {
			if !collectPreviewPredicateUses(child, uses) {
				return false
			}
		}
	case ir.PhysicalExistsPredicate:
		if predicate.Exists == nil {
			return false
		}
		collectPreviewSubplanUses(*predicate.Exists, uses)
	default:
		return false
	}
	return true
}
