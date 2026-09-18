package ir

import "github.com/calypr/loom/internal/dataframe/spec"

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
		if operation.RootScan != nil && operation.RootScan.Population != nil {
			for filter := range operation.RootScan.Population.MemberFilters {
				canonicalizePhysicalPredicateExpression(operation.RootScan.Population.MemberFilters[filter].Expression)
			}
			canonicalizePhysicalOperations(operation.RootScan.Population.ResourceOperations)
		}
		if operation.Set != nil {
			canonicalizePhysicalSubplan(&operation.Set.Subplan)
		}
		if operation.Filter != nil && operation.Filter.Expression != nil {
			canonicalizePhysicalPredicateExpression(operation.Filter.Expression)
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
		if operation.PopulationMappingReturn != nil {
			canonicalizePhysicalExpression(&operation.PopulationMappingReturn.Members)
			for part := range operation.PopulationMappingReturn.IdentityParts {
				canonicalizePhysicalExpression(&operation.PopulationMappingReturn.IdentityParts[part].Expression)
			}
			if operation.PopulationMappingReturn.ExplicitIdentity != nil {
				canonicalizePhysicalExpression(operation.PopulationMappingReturn.ExplicitIdentity)
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
	if expression.Subplan != nil {
		canonicalizePhysicalSubplan(expression.Subplan)
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
		if operation.RootScan.Population != nil {
			populationCopy := *operation.RootScan.Population
			populationCopy.MemberFilters = make([]PhysicalFilter, len(operation.RootScan.Population.MemberFilters))
			for index, filter := range operation.RootScan.Population.MemberFilters {
				populationCopy.MemberFilters[index] = filter
				populationCopy.MemberFilters[index].Predicate = clonePhysicalPredicate(filter.Predicate)
				if filter.Expression != nil {
					expression := clonePhysicalPredicateExpression(*filter.Expression)
					populationCopy.MemberFilters[index].Expression = &expression
				}
			}
			populationCopy.ResourceOperations = clonePhysicalOperations(operation.RootScan.Population.ResourceOperations)
			populationCopy.RootKey = clonePhysicalValue(operation.RootScan.Population.RootKey)
			populationCopy.MemberID = clonePhysicalValue(operation.RootScan.Population.MemberID)
			rootScanCopy.Population = &populationCopy
		}
		copy.RootScan = &rootScanCopy
	}
	if operation.Traversal != nil {
		traversalCopy := *operation.Traversal
		traversalCopy.EndpointIndexFields = cloneStrings(operation.Traversal.EndpointIndexFields)
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
		if operation.Set.Reduction != nil {
			reductionCopy := *operation.Set.Reduction
			reductionCopy.Fields = append([]PhysicalSetReductionField(nil), operation.Set.Reduction.Fields...)
			setCopy.Reduction = &reductionCopy
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
	if operation.CollectionScan != nil {
		collectionCopy := *operation.CollectionScan
		copy.CollectionScan = &collectionCopy
	}
	if operation.PopulationMappingReturn != nil {
		mappingCopy := *operation.PopulationMappingReturn
		mappingCopy.Members = clonePhysicalExpression(operation.PopulationMappingReturn.Members)
		mappingCopy.IdentityParts = make([]PhysicalPopulationMappingIdentityPart, len(operation.PopulationMappingReturn.IdentityParts))
		for index, part := range operation.PopulationMappingReturn.IdentityParts {
			mappingCopy.IdentityParts[index] = part
			mappingCopy.IdentityParts[index].Expression = clonePhysicalExpression(part.Expression)
		}
		if operation.PopulationMappingReturn.ExplicitIdentity != nil {
			explicit := clonePhysicalExpression(*operation.PopulationMappingReturn.ExplicitIdentity)
			mappingCopy.ExplicitIdentity = &explicit
		}
		copy.PopulationMappingReturn = &mappingCopy
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
	copy.Correlation = clonePhysicalCorrelation(predicate.Correlation)
	return copy
}

func clonePhysicalCorrelation(correlation *PhysicalCorrelation) *PhysicalCorrelation {
	if correlation == nil {
		return nil
	}
	copy := *correlation
	copy.Source = clonePhysicalValue(correlation.Source)
	copy.OwnerSelector.Steps = append([]spec.SelectorStep(nil), correlation.OwnerSelector.Steps...)
	copy.KeySelector.Steps = append([]spec.SelectorStep(nil), correlation.KeySelector.Steps...)
	copy.SystemSelector.Steps = append([]spec.SelectorStep(nil), correlation.SystemSelector.Steps...)
	copy.CodeSelector.Steps = append([]spec.SelectorStep(nil), correlation.CodeSelector.Steps...)
	copy.ValueSelector.Steps = append([]spec.SelectorStep(nil), correlation.ValueSelector.Steps...)
	copy.ValueFallbacks = make([]spec.Selector, len(correlation.ValueFallbacks))
	for index := range correlation.ValueFallbacks {
		copy.ValueFallbacks[index] = correlation.ValueFallbacks[index]
		copy.ValueFallbacks[index].Steps = append([]spec.SelectorStep(nil), correlation.ValueFallbacks[index].Steps...)
	}
	copy.ChoiceArms = append([]string(nil), correlation.ChoiceArms...)
	copy.ChoiceSelectors = make([]spec.Selector, len(correlation.ChoiceSelectors))
	for index := range correlation.ChoiceSelectors {
		copy.ChoiceSelectors[index] = correlation.ChoiceSelectors[index]
		copy.ChoiceSelectors[index].Steps = append([]spec.SelectorStep(nil), correlation.ChoiceSelectors[index].Steps...)
	}
	copy.ExtensionURLSelectors = make([]spec.Selector, len(correlation.ExtensionURLSelectors))
	for index := range correlation.ExtensionURLSelectors {
		copy.ExtensionURLSelectors[index] = correlation.ExtensionURLSelectors[index]
		copy.ExtensionURLSelectors[index].Steps = append([]spec.SelectorStep(nil), correlation.ExtensionURLSelectors[index].Steps...)
	}
	copy.ExtensionURLBindKeys = append([]string(nil), correlation.ExtensionURLBindKeys...)
	return &copy
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
		pivot.ColumnAliases = make(map[string]string, len(expression.Pivot.ColumnAliases))
		for key, value := range expression.Pivot.ColumnAliases {
			pivot.ColumnAliases[key] = value
		}
		pivot.Correlation = clonePhysicalCorrelation(expression.Pivot.Correlation)
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
	if expression.Subplan != nil {
		subplan := clonePhysicalSubplan(*expression.Subplan)
		copy.Subplan = &subplan
	}
	return copy
}

func clonePhysicalSubplan(subplan PhysicalSubplan) PhysicalSubplan {
	copy := subplan
	copy.Captures = cloneStrings(subplan.Captures)
	copy.Operations = make([]PhysicalOperation, len(subplan.Operations))
	for i, op := range subplan.Operations {
		copy.Operations[i] = clonePhysicalOperation(op)
	}
	copy.Return = clonePhysicalExpression(subplan.Return)
	if subplan.Sort != nil {
		sort := clonePhysicalValue(*subplan.Sort)
		copy.Sort = &sort
	}
	return copy
}

func clonePhysicalValue(value PhysicalValue) PhysicalValue {
	copy := value
	copy.Path = cloneStrings(value.Path)
	return copy
}
