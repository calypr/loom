package ir

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
func ClonePhysicalPredicate(predicate PhysicalPredicate) PhysicalPredicate {
	return clonePhysicalPredicate(predicate)
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
	if operation.DerivedLet != nil {
		derivedCopy := *operation.DerivedLet
		derivedCopy.Inputs = make([]PhysicalValue, len(operation.DerivedLet.Inputs))
		for index, input := range operation.DerivedLet.Inputs {
			derivedCopy.Inputs[index] = clonePhysicalValue(input)
		}
		copy.DerivedLet = &derivedCopy
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
			returnCopy.Projections[index] = projectionCopy
		}
		copy.Return = &returnCopy
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
		extract.Fallbacks = append([]Selector(nil), extract.Fallbacks...)
		if extract.Prepared != nil {
			prepared := *extract.Prepared
			extract.Prepared = &prepared
		}
		copy.Extract = &extract
	}
	if expression.Pivot != nil {
		pivot := *expression.Pivot
		pivot.Source = clonePhysicalValue(expression.Pivot.Source)
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
