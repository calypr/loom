package ir

import (
	"encoding/json"

	"github.com/calypr/loom/internal/dataframe/spec"
)

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	return append([]string(nil), in...)
}

func clonePhysicalBindValue(value any) any {
	switch value := value.(type) {
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			out[i] = clonePhysicalBindValue(item)
		}
		return out
	case []string:
		return append([]string(nil), value...)
	case map[string]any:
		out := make(map[string]any, len(value))
		for key, item := range value {
			out[key] = clonePhysicalBindValue(item)
		}
		return out
	default:
		return value
	}
}

func clonePhysicalPlan(plan PhysicalPlan) PhysicalPlan {
	copy := plan
	copy.BindVars = clonePhysicalBindVars(plan.BindVars)
	copy.OptimizationPolicy = clonePhysicalOptimizationReport(plan.OptimizationPolicy)
	copy.Operations = make([]PhysicalOperation, len(plan.Operations))
	for index, operation := range plan.Operations {
		copy.Operations[index] = clonePhysicalOperation(operation)
	}
	return copy
}

func ClonePhysicalPlan(plan PhysicalPlan) PhysicalPlan { return clonePhysicalPlan(plan) }
func ClonePhysicalOperation(operation PhysicalOperation) PhysicalOperation {
	return clonePhysicalOperation(operation)
}
func ClonePhysicalOperations(operations []PhysicalOperation) []PhysicalOperation {
	return clonePhysicalOperations(operations)
}
func ClonePhysicalPredicateExpression(predicate PhysicalPredicateExpression) PhysicalPredicateExpression {
	return clonePhysicalPredicateExpression(predicate)
}
func ClonePhysicalExpression(expression PhysicalExpression) PhysicalExpression {
	return clonePhysicalExpression(expression)
}
func ClonePhysicalSubplan(subplan PhysicalSubplan) PhysicalSubplan {
	return clonePhysicalSubplan(subplan)
}

// CanonicalExecutionPhysicalPlan returns the renderer-relevant plan contract.
// Compiler diagnostics and semantic source locations are deliberately removed:
// they explain a plan but do not change what the backend executes.
func CanonicalExecutionPhysicalPlan(plan PhysicalPlan) PhysicalPlan {
	out := clonePhysicalPlan(plan)
	out.Source = PhysicalSource{}
	out.DeferredExpressionLets = nil
	out.AppliedRules = nil
	out.SharedTraversalCount = 0
	out.OptimizationPolicy = PhysicalOptimizationReport{}
	out.RequiredMatchReuseCount = 0
	canonicalizePhysicalOperations(out.Operations)
	return out
}

func canonicalizePhysicalOperations(operations []PhysicalOperation) {
	for index := range operations {
		operation := &operations[index]
		operation.Source = PhysicalSource{}
		if operation.Set != nil {
			canonicalizePhysicalSubplan(&operation.Set.Subplan)
		}
		if operation.Filter != nil && operation.Filter.Expression != nil {
			canonicalizePhysicalPredicateExpression(operation.Filter.Expression)
		}
		if operation.DerivedLet != nil && operation.DerivedLet.Expression != nil {
			canonicalizePhysicalExpression(operation.DerivedLet.Expression)
		}
		if operation.ExpressionLet != nil {
			canonicalizePhysicalExpression(&operation.ExpressionLet.Expression)
		}
		if operation.Unnest != nil {
			canonicalizePhysicalExpression(&operation.Unnest.Expression)
		}
		if operation.Return != nil {
			for projection := range operation.Return.Projections {
				canonicalizePhysicalExpression(operation.Return.Projections[projection].Expression)
			}
		}
		if operation.PathExtend != nil {
			canonicalizePhysicalOperations(operation.PathExtend.Scope)
		}
	}
}

func canonicalizePhysicalSubplan(subplan *PhysicalSubplan) {
	if subplan == nil {
		return
	}
	canonicalizePhysicalOperations(subplan.Operations)
	canonicalizePhysicalExpression(&subplan.Return)
}

func canonicalizePhysicalPredicateExpression(predicate *PhysicalPredicateExpression) {
	if predicate == nil {
		return
	}
	if predicate.Comparison != nil {
		canonicalizePhysicalExpression(predicate.Comparison.LeftExpression)
	}
	canonicalizePhysicalSubplan(predicate.Exists)
	for index := range predicate.Children {
		canonicalizePhysicalPredicateExpression(&predicate.Children[index])
	}
}

func canonicalizePhysicalExpression(expression *PhysicalExpression) {
	if expression == nil {
		return
	}
	if expression.Aggregate != nil {
		canonicalizePhysicalExpression(expression.Aggregate.Value)
		canonicalizePhysicalPredicateExpression(expression.Aggregate.Predicate)
	}
	if expression.Slice != nil {
		canonicalizePhysicalPredicateExpression(expression.Slice.Predicate)
		canonicalizePhysicalExpression(expression.Slice.Sort)
		for index := range expression.Slice.Projections {
			canonicalizePhysicalExpression(&expression.Slice.Projections[index].Expression)
		}
	}
	if expression.Lookup != nil {
		canonicalizePhysicalExpression(&expression.Lookup.Source)
		canonicalizePhysicalExpression(&expression.Lookup.ItemKey)
		canonicalizePhysicalExpression(&expression.Lookup.ItemValue)
	}
	if expression.KeyedMap != nil {
		canonicalizePhysicalExpression(&expression.KeyedMap.Source)
		canonicalizePhysicalExpression(&expression.KeyedMap.ItemKey)
		canonicalizePhysicalExpression(&expression.KeyedMap.ItemValue)
		for index := range expression.KeyedMap.ValueFallbacks {
			canonicalizePhysicalExpression(&expression.KeyedMap.ValueFallbacks[index])
		}
	}
	if expression.KeySet != nil {
		canonicalizePhysicalExpression(&expression.KeySet.Source)
		canonicalizePhysicalExpression(&expression.KeySet.ItemKey)
	}
	if expression.Object != nil {
		for index := range expression.Object.Fields {
			canonicalizePhysicalExpression(&expression.Object.Fields[index].Expression)
		}
	}
	if expression.Call != nil {
		for index := range expression.Call.Args {
			canonicalizePhysicalExpression(&expression.Call.Args[index])
		}
	}
}

func clonePhysicalBindVars(bindVars map[string]any) map[string]any {
	if bindVars == nil {
		return nil
	}
	copy := make(map[string]any, len(bindVars))
	for key, value := range bindVars {
		copy[key] = clonePhysicalBindValue(value)
	}
	return copy
}

func clonePhysicalOperation(operation PhysicalOperation) PhysicalOperation {
	copy := operation
	if operation.RootScan != nil {
		rootScanCopy := *operation.RootScan
		copy.RootScan = &rootScanCopy
	}
	if operation.Traversal != nil {
		traversalCopy := *operation.Traversal
		copy.Traversal = &traversalCopy
	}
	if operation.Filter != nil {
		filterCopy := *operation.Filter
		filterCopy.Predicate = clonePhysicalPredicate(operation.Filter.Predicate)
		if operation.Filter.Expression != nil {
			expression := clonePhysicalPredicateExpression(*operation.Filter.Expression)
			filterCopy.Expression = &expression
		}
		copy.Filter = &filterCopy
	}
	if operation.Set != nil {
		setCopy := *operation.Set
		setCopy.Subplan = clonePhysicalSubplan(operation.Set.Subplan)
		if operation.Set.Output != nil {
			outputCopy := *operation.Set.Output
			outputCopy.Fields = append([]PhysicalSetOutputField(nil), operation.Set.Output.Fields...)
			setCopy.Output = &outputCopy
		}
		if operation.Set.Projection != nil {
			projectionCopy := *operation.Set.Projection
			projectionCopy.Fields = append([]PhysicalSetProjectionField(nil), operation.Set.Projection.Fields...)
			setCopy.Projection = &projectionCopy
		}
		if operation.Set.Prepared != nil {
			preparedCopy := *operation.Set.Prepared
			preparedCopy.Fields = append([]PhysicalPreparedField(nil), operation.Set.Prepared.Fields...)
			setCopy.Prepared = &preparedCopy
		}
		copy.Set = &setCopy
	}
	if operation.Unnest != nil {
		unnestCopy := *operation.Unnest
		unnestCopy.Expression = clonePhysicalExpression(operation.Unnest.Expression)
		copy.Unnest = &unnestCopy
	}
	if operation.DerivedLet != nil {
		derivedCopy := *operation.DerivedLet
		derivedCopy.Inputs = make([]PhysicalValue, len(operation.DerivedLet.Inputs))
		for index, input := range operation.DerivedLet.Inputs {
			derivedCopy.Inputs[index] = clonePhysicalValue(input)
		}
		if operation.DerivedLet.Expression != nil {
			expression := clonePhysicalExpression(*operation.DerivedLet.Expression)
			derivedCopy.Expression = &expression
		}
		copy.DerivedLet = &derivedCopy
	}
	if operation.ExpressionLet != nil {
		expressionCopy := *operation.ExpressionLet
		expressionCopy.Expression = clonePhysicalExpression(operation.ExpressionLet.Expression)
		copy.ExpressionLet = &expressionCopy
	}
	if operation.Sort != nil {
		sortCopy := *operation.Sort
		sortCopy.Value = clonePhysicalValue(operation.Sort.Value)
		copy.Sort = &sortCopy
	}
	if operation.Limit != nil {
		limitCopy := *operation.Limit
		copy.Limit = &limitCopy
	}
	if operation.Return != nil {
		returnCopy := *operation.Return
		returnCopy.Projections = make([]PhysicalProjection, len(operation.Return.Projections))
		for index, projection := range operation.Return.Projections {
			projectionCopy := projection
			projectionCopy.Value = clonePhysicalValue(projection.Value)
			if projection.Expression != nil {
				expression := clonePhysicalExpression(*projection.Expression)
				projectionCopy.Expression = &expression
			}
			returnCopy.Projections[index] = projectionCopy
		}
		copy.Return = &returnCopy
	}
	if operation.PathSeed != nil {
		seedCopy := *operation.PathSeed
		seedCopy.Node.Value = clonePhysicalValue(seedCopy.Node.Value)
		copy.PathSeed = &seedCopy
	}
	if operation.PathExtend != nil {
		extendCopy := *operation.PathExtend
		extendCopy.SourcePath = cloneStrings(operation.PathExtend.SourcePath)
		extendCopy.Traversal.EndpointIndexFields = cloneStrings(operation.PathExtend.Traversal.EndpointIndexFields)
		extendCopy.Node.Value = clonePhysicalValue(operation.PathExtend.Node.Value)
		extendCopy.Scope = clonePhysicalOperations(operation.PathExtend.Scope)
		copy.PathExtend = &extendCopy
	}
	if operation.GraphReturn != nil {
		graphCopy := *operation.GraphReturn
		graphCopy.PathSets = cloneStrings(operation.GraphReturn.PathSets)
		copy.GraphReturn = &graphCopy
	}
	return copy
}

func clonePhysicalPredicate(predicate PhysicalPredicate) PhysicalPredicate {
	copy := predicate
	copy.Left = clonePhysicalValue(predicate.Left)
	if predicate.LeftExpression != nil {
		leftExpression := clonePhysicalExpression(*predicate.LeftExpression)
		copy.LeftExpression = &leftExpression
	}
	if predicate.Right != nil {
		rightCopy := clonePhysicalValue(*predicate.Right)
		copy.Right = &rightCopy
	}
	return copy
}

func clonePhysicalPredicateExpression(predicate PhysicalPredicateExpression) PhysicalPredicateExpression {
	copy := predicate
	if predicate.Comparison != nil {
		comparison := clonePhysicalPredicate(*predicate.Comparison)
		copy.Comparison = &comparison
	}
	if predicate.Exists != nil {
		subplan := clonePhysicalSubplan(*predicate.Exists)
		copy.Exists = &subplan
	}
	copy.Children = make([]PhysicalPredicateExpression, len(predicate.Children))
	for index, child := range predicate.Children {
		copy.Children[index] = clonePhysicalPredicateExpression(child)
	}
	return copy
}

func clonePhysicalExpression(expression PhysicalExpression) PhysicalExpression {
	copy := expression
	if expression.Value != nil {
		value := clonePhysicalValue(*expression.Value)
		copy.Value = &value
	}
	if expression.Extract != nil {
		extract := *expression.Extract
		extract.Source = clonePhysicalValue(extract.Source)
		extract.Fallbacks = append([]spec.Selector(nil), extract.Fallbacks...)
		if extract.Prepared != nil {
			prepared := *extract.Prepared
			extract.Prepared = &prepared
		}
		copy.Extract = &extract
	}
	if expression.Pivot != nil {
		pivot := *expression.Pivot
		pivot.Source = clonePhysicalValue(expression.Pivot.Source)
		pivot.ItemSource.Steps = append([]spec.SelectorStep(nil), expression.Pivot.ItemSource.Steps...)
		pivot.ValueFallbacks = append([]spec.Selector(nil), expression.Pivot.ValueFallbacks...)
		pivot.ColumnsBindKey = expression.Pivot.ColumnsBindKey
		if pivot.PreparedKey != nil {
			prepared := *pivot.PreparedKey
			pivot.PreparedKey = &prepared
		}
		if pivot.PreparedValue != nil {
			prepared := *pivot.PreparedValue
			pivot.PreparedValue = &prepared
		}
		copy.Pivot = &pivot
	}
	if expression.Aggregate != nil {
		aggregate := *expression.Aggregate
		aggregate.Source = clonePhysicalValue(aggregate.Source)
		if aggregate.Value != nil {
			value := clonePhysicalExpression(*aggregate.Value)
			aggregate.Value = &value
		}
		copy.Aggregate = &aggregate
	}
	if expression.Slice != nil {
		slice := *expression.Slice
		slice.Source = clonePhysicalValue(slice.Source)
		if slice.Sort != nil {
			sort := clonePhysicalExpression(*slice.Sort)
			slice.Sort = &sort
		}
		if slice.Predicate != nil {
			predicate := clonePhysicalPredicateExpression(*slice.Predicate)
			slice.Predicate = &predicate
		}
		slice.Projections = make([]PhysicalExpressionProjection, len(expression.Slice.Projections))
		for index, projection := range expression.Slice.Projections {
			projectionCopy := projection
			projectionCopy.Expression = clonePhysicalExpression(projection.Expression)
			slice.Projections[index] = projectionCopy
		}
		copy.Slice = &slice
	}
	if expression.Lookup != nil {
		lookup := *expression.Lookup
		lookup.Source = clonePhysicalExpression(expression.Lookup.Source)
		lookup.ItemKey = clonePhysicalExpression(expression.Lookup.ItemKey)
		lookup.ItemValue = clonePhysicalExpression(expression.Lookup.ItemValue)
		copy.Lookup = &lookup
	}
	if expression.ObjectLookup != nil {
		lookup := *expression.ObjectLookup
		copy.ObjectLookup = &lookup
	}
	if expression.KeyedMap != nil {
		keyed := *expression.KeyedMap
		keyed.Source = clonePhysicalExpression(expression.KeyedMap.Source)
		keyed.ItemKey = clonePhysicalExpression(expression.KeyedMap.ItemKey)
		keyed.ItemValue = clonePhysicalExpression(expression.KeyedMap.ItemValue)
		keyed.ValueFallbacks = make([]PhysicalExpression, len(expression.KeyedMap.ValueFallbacks))
		for index := range expression.KeyedMap.ValueFallbacks {
			keyed.ValueFallbacks[index] = clonePhysicalExpression(expression.KeyedMap.ValueFallbacks[index])
		}
		copy.KeyedMap = &keyed
	}
	if expression.ObjectKeys != nil {
		keys := *expression.ObjectKeys
		copy.ObjectKeys = &keys
	}
	if expression.KeySet != nil {
		keySet := *expression.KeySet
		keySet.Source = clonePhysicalExpression(expression.KeySet.Source)
		keySet.ItemKey = clonePhysicalExpression(expression.KeySet.ItemKey)
		copy.KeySet = &keySet
	}
	if expression.Object != nil {
		object := *expression.Object
		object.Fields = make([]PhysicalExpressionProjection, len(expression.Object.Fields))
		for index, field := range expression.Object.Fields {
			fieldCopy := field
			fieldCopy.Expression = clonePhysicalExpression(field.Expression)
			object.Fields[index] = fieldCopy
		}
		copy.Object = &object
	}
	return copy
}

// PhysicalExpressionFingerprint returns a deterministic structural identity
// for optimizer reuse decisions. It intentionally includes typed bind keys
// and cardinality/null contracts but never bind values.
func PhysicalExpressionFingerprint(expression PhysicalExpression) (string, error) {
	if err := validatePhysicalExpressionObjectCycles(expression); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(expression)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func clonePhysicalSubplan(subplan PhysicalSubplan) PhysicalSubplan {
	copy := subplan
	copy.Captures = cloneStrings(subplan.Captures)
	copy.Operations = make([]PhysicalOperation, len(subplan.Operations))
	for i, op := range subplan.Operations {
		copy.Operations[i] = clonePhysicalOperation(op)
	}
	copy.Return = clonePhysicalExpression(subplan.Return)
	return copy
}

func clonePhysicalValue(value PhysicalValue) PhysicalValue {
	copy := value
	copy.Path = cloneStrings(value.Path)
	return copy
}
