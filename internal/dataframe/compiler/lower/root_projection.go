package lower

import (
	"fmt"
	"strings"
)

func rootPhysicalProjections(physical *PhysicalPlan, root SemanticNode) ([]PhysicalProjection, error) {
	projections := []PhysicalProjection{{Name: "_key", Value: PhysicalValue{Variable: "root", Path: []string{"_key"}}}}
	for index, field := range root.Fields {
		selection, err := ResolveSemanticField(root.ResourceType, root.Alias, index, field)
		if err != nil {
			return nil, err
		}
		cardinality, distinct := PhysicalScalarCardinality, false
		switch selection.Projection {
		case ProjectionArray:
			cardinality = PhysicalArrayCardinality
		case ProjectionDistinctArray:
			cardinality, distinct = PhysicalArrayCardinality, true
		case ProjectionScalar, ProjectionFirst:
		default:
			return nil, fmt.Errorf("root field %q has unsupported projection %q", field.Name, selection.Projection)
		}
		expression := PhysicalExpression{
			Kind: PhysicalExtractExpression, Cardinality: cardinality, NullBehavior: PhysicalPreserveNull,
			Extract: &PhysicalExtract{Source: PhysicalValue{Variable: "root", Path: []string{"payload"}}, ResourceType: root.ResourceType, Selector: field.Selector, Fallbacks: append([]Selector(nil), field.Fallbacks...), Distinct: distinct, ExecutionMode: selectorExecutionMode(root.ResourceType, field.Selector, field.Fallbacks...)},
		}
		if cardinality == PhysicalArrayCardinality {
			expression.NullBehavior = PhysicalEmptyOnNull
		}
		projections = append(projections, PhysicalProjection{Name: field.Name, Expression: &expression})
	}
	for _, aggregate := range root.Aggregates {
		expression, err := physicalAggregateExpression(physical, root.ResourceType, PhysicalValue{Variable: "root"}, aggregate, false)
		if err != nil {
			return nil, err
		}
		projections = append(projections, PhysicalProjection{Name: aggregate.Name, Expression: &expression})
	}
	for _, pivot := range root.Pivots {
		pivotProjections, err := physicalPivotProjections(physical, root.ResourceType, PhysicalValue{Variable: "root"}, pivot, false, "")
		if err != nil {
			return nil, err
		}
		projections = append(projections, pivotProjections...)
	}
	for _, slice := range root.Slices {
		expression, err := physicalSliceExpression(physical, root.ResourceType, PhysicalValue{Variable: "root"}, slice, false)
		if err != nil {
			return nil, err
		}
		projections = append(projections, PhysicalProjection{Name: slice.Name, Expression: &expression})
	}
	return projections, nil
}

func physicalPivotProjections(physical *PhysicalPlan, resourceType string, source PhysicalValue, pivot SemanticPivot, sourceIsSet bool, prefix string) ([]PhysicalProjection, error) {
	if len(pivot.Columns) == 0 {
		return nil, fmt.Errorf("pivot %q requires bounded columns", pivot.Name)
	}
	projections := make([]PhysicalProjection, 0, len(pivot.Columns))
	columnsBindKey := "pivot_" + sanitizeColumnName(source.Variable) + "_" + sanitizeColumnName(pivot.Name) + "_columns"
	physical.BindVars[columnsBindKey] = append([]string(nil), pivot.Columns...)
	familyVariable := "__loom_pivot_" + sanitizeColumnName(source.Variable) + "_" + sanitizeColumnName(pivot.Name)
	for index := 1; deferredExpressionVariableExists(*physical, familyVariable); index++ {
		familyVariable = fmt.Sprintf("__loom_pivot_%s_%s_%d", sanitizeColumnName(source.Variable), sanitizeColumnName(pivot.Name), index)
	}
	sharedExpression := PhysicalExpression{Kind: PhysicalPivotExpression, Cardinality: PhysicalObjectCardinality, NullBehavior: PhysicalPreserveNull,
		Pivot: &PhysicalPivotMap{Source: source, ResourceType: resourceType, ItemSource: pivot.ItemSource, ItemResourceType: pivot.ItemResourceType, KeySelector: pivot.ColumnSelector, ValueSelector: pivot.ValueSelector, ValueFallbacks: append([]Selector(nil), pivot.ValueFallbacks...), ColumnsBindKey: columnsBindKey, FlattenSingleColumn: false}}
	physical.DeferredExpressionLets = append(physical.DeferredExpressionLets, PhysicalOperation{Kind: PhysicalExpressionLetOp, Source: PhysicalSource{ResourceType: resourceType, SemanticField: "pivot_family"}, ExpressionLet: &PhysicalExpressionLet{Variable: familyVariable, Expression: sharedExpression}})
	for _, column := range pivot.Columns {
		columnBindKey := columnsBindKey + "_" + sanitizeColumnName(column)
		physical.BindVars[columnBindKey] = column
		expression := PhysicalExpression{Kind: PhysicalObjectLookupExpression, Cardinality: PhysicalScalarCardinality, NullBehavior: PhysicalPreserveNull, ObjectLookup: &PhysicalObjectLookup{ObjectVariable: familyVariable, KeyBindKey: columnBindKey}}
		projections = append(projections, PhysicalProjection{Name: prefix + pivot.Name + "__" + sanitizeColumnName(column), Expression: &expression})
	}
	return projections, nil
}

func deferredExpressionVariableExists(physical PhysicalPlan, variable string) bool {
	for _, operation := range physical.DeferredExpressionLets {
		if operation.ExpressionLet != nil && operation.ExpressionLet.Variable == variable {
			return true
		}
	}
	for _, operation := range physical.Operations {
		if operation.ExpressionLet != nil && operation.ExpressionLet.Variable == variable {
			return true
		}
	}
	return false
}

// physicalAggregateExpression lowers one semantic aggregate into the typed
// aggregate IR. A root source is a singleton document; child sources are
// correlated PhysicalSet variables. Selector extraction remains typed so the
// renderer can choose the correct payload iteration without embedding user
// paths in AQL.
func physicalAggregateExpression(physical *PhysicalPlan, resourceType string, source PhysicalValue, aggregate SemanticAggregate, sourceIsSet bool) (PhysicalExpression, error) {
	op := PhysicalAggregateOperation(strings.ToUpper(strings.TrimSpace(aggregate.Operation)))
	switch op {
	case PhysicalCountAggregate, PhysicalCountDistinctAggregate, PhysicalExistsAggregate, PhysicalDistinctValuesAggregate, PhysicalMinAggregate, PhysicalMaxAggregate, PhysicalFirstAggregate:
	default:
		return PhysicalExpression{}, fmt.Errorf("aggregate %q uses unsupported operation %q", aggregate.Name, aggregate.Operation)
	}
	aggregatePhysical := PhysicalAggregate{Source: source, Operation: op}
	if aggregate.Selector != nil {
		valueSource := source
		if !sourceIsSet && source.Variable != "" && len(source.Path) == 0 {
			valueSource = PhysicalValue{Variable: source.Variable, Path: []string{"payload"}}
		}
		value := PhysicalExpression{Kind: PhysicalExtractExpression, Cardinality: PhysicalArrayCardinality, NullBehavior: PhysicalEmptyOnNull,
			Extract: &PhysicalExtract{Source: valueSource, ResourceType: resourceType, Selector: *aggregate.Selector, ExecutionMode: selectorExecutionMode(resourceType, *aggregate.Selector)}}
		aggregatePhysical.Value = &value
	}
	if aggregate.Predicate != nil {
		leftSource := source
		if !sourceIsSet && source.Variable != "" && len(source.Path) == 0 {
			leftSource = PhysicalValue{Variable: source.Variable, Path: []string{"payload"}}
		}
		left := PhysicalExpression{Kind: PhysicalExtractExpression, Cardinality: PhysicalArrayCardinality, NullBehavior: PhysicalEmptyOnNull,
			Extract: &PhysicalExtract{Source: leftSource, ResourceType: resourceType, Selector: *aggregate.Predicate, ExecutionMode: selectorExecutionMode(resourceType, *aggregate.Predicate)}}
		comparison := &PhysicalPredicate{Operator: "EXISTS", LeftExpression: &left}
		if aggregate.PredicateEquals != "" {
			comparison.Operator = "EQUALS"
			comparison.ValueKind = FilterString
			key := "aggregate_" + sanitizeColumnName(source.Variable) + "_" + sanitizeColumnName(aggregate.Name) + "_predicate_equals"
			physical.BindVars[key] = aggregate.PredicateEquals
			comparison.Right = &PhysicalValue{BindKey: key}
		}
		aggregatePhysical.Predicate = &PhysicalPredicateExpression{Kind: PhysicalComparisonPredicate, Comparison: comparison}
	}
	cardinality := PhysicalScalarCardinality
	nullBehavior := PhysicalEmptyOnNull
	if op == PhysicalDistinctValuesAggregate {
		cardinality = PhysicalArrayCardinality
	}
	return PhysicalExpression{Kind: PhysicalAggregateExpression, Cardinality: cardinality, NullBehavior: nullBehavior, Aggregate: &aggregatePhysical}, nil
}

// physicalSliceExpression lowers a representative slice into a typed,
// bounded projection over either the singleton root document or a correlated
// child set. The source set is already materialized in stable _key order;
// the explicit sort expression below makes that ordering part of the slice
// contract and gives the renderer a deterministic tie-break.
func physicalSliceExpression(physical *PhysicalPlan, resourceType string, source PhysicalValue, slice SemanticSlice, sourceIsSet bool) (PhysicalExpression, error) {
	_ = sourceIsSet
	if slice.Limit <= 0 {
		return PhysicalExpression{}, fmt.Errorf("slice %q requires positive limit", slice.Name)
	}
	limitKey := "slice_" + sanitizeColumnName(source.Variable) + "_" + sanitizeColumnName(slice.Name) + "_limit"
	physical.BindVars[limitKey] = slice.Limit
	// Expressions are anchored to the declared source variable for validation;
	// the renderer rebinds them to its per-item loop variable.
	leftSource := source
	physicalSlice := PhysicalSlice{
		Source:       source,
		LimitBindKey: limitKey,
		Sort:         &PhysicalExpression{Kind: PhysicalValueExpression, Cardinality: PhysicalScalarCardinality, NullBehavior: PhysicalPreserveNull, Value: &PhysicalValue{Variable: source.Variable, Path: []string{"_key"}}},
		Projections:  make([]PhysicalExpressionProjection, 0, len(slice.Fields)),
	}
	if slice.Predicate != nil {
		left := PhysicalExpression{Kind: PhysicalExtractExpression, Cardinality: PhysicalArrayCardinality, NullBehavior: PhysicalEmptyOnNull,
			Extract: &PhysicalExtract{Source: leftSource, ResourceType: resourceType, Selector: *slice.Predicate, ExecutionMode: selectorExecutionMode(resourceType, *slice.Predicate)}}
		comparison := &PhysicalPredicate{Operator: "EXISTS", LeftExpression: &left}
		if slice.PredicateEquals != "" {
			key := "slice_" + sanitizeColumnName(source.Variable) + "_" + sanitizeColumnName(slice.Name) + "_predicate_equals"
			physical.BindVars[key] = slice.PredicateEquals
			comparison.Operator = "EQUALS"
			comparison.ValueKind = FilterString
			comparison.Right = &PhysicalValue{BindKey: key}
		}
		physicalSlice.Predicate = &PhysicalPredicateExpression{Kind: PhysicalComparisonPredicate, Comparison: comparison}
	}
	for index, field := range slice.Fields {
		selection := field
		if selection.Name == "" {
			return PhysicalExpression{}, fmt.Errorf("slice %q field %d requires name", slice.Name, index)
		}
		physicalSlice.Projections = append(physicalSlice.Projections, PhysicalExpressionProjection{
			Name: selection.Name,
			Expression: PhysicalExpression{Kind: PhysicalExtractExpression, Cardinality: PhysicalScalarCardinality, NullBehavior: PhysicalPreserveNull,
				Extract: &PhysicalExtract{Source: leftSource, ResourceType: resourceType, Selector: selection.Selector, Fallbacks: append([]Selector(nil), selection.Fallbacks...), ExecutionMode: selectorExecutionMode(resourceType, selection.Selector, selection.Fallbacks...)}},
		})
	}
	return PhysicalExpression{Kind: PhysicalSliceExpression, Cardinality: PhysicalArrayCardinality, NullBehavior: PhysicalEmptyOnNull, Slice: &physicalSlice}, nil
}

func appendRootPhysicalFilters(physical *PhysicalPlan, root SemanticNode) error {
	for index, filter := range root.Filters {
		if err := ValidateTypedFilterForResource(root.ResourceType, filter); err != nil {
			return fmt.Errorf("root filter %q: %w", filter.FieldRef, err)
		}
		selector, err := ParseSelector(filter.Selector)
		if err != nil {
			return fmt.Errorf("root filter %q selector: %w", filter.FieldRef, err)
		}
		predicate := PhysicalPredicate{
			Operator: string(filter.Operator), Quantifier: filter.Quantifier, ValueKind: filter.FieldKind,
			LeftExpression: &PhysicalExpression{Kind: PhysicalExtractExpression, Cardinality: PhysicalArrayCardinality, NullBehavior: PhysicalEmptyOnNull,
				Extract: &PhysicalExtract{Source: PhysicalValue{Variable: "root", Path: []string{"payload"}}, ResourceType: root.ResourceType, Selector: selector, ExecutionMode: selectorExecutionMode(root.ResourceType, selector)}},
		}
		if filter.Operator != FilterExists && filter.Operator != FilterMissing {
			key := fmt.Sprintf("root_filter_%d_value", index+1)
			if filter.Operator == FilterIn {
				values := make([]any, 0, len(filter.Values))
				for _, value := range filter.Values {
					literal, err := filterLiteral(value)
					if err != nil {
						return err
					}
					values = append(values, literal)
				}
				physical.BindVars[key] = values
			} else {
				literal, err := filterLiteral(filter.Values[0])
				if err != nil {
					return err
				}
				physical.BindVars[key] = literal
			}
			predicate.Right = &PhysicalValue{BindKey: key}
		}
		physical.Operations = append(physical.Operations, PhysicalOperation{Kind: PhysicalFilterOp,
			Source: PhysicalSource{SemanticNode: root.Alias, ResourceType: root.ResourceType, SemanticField: filter.FieldRef},
			Filter: &PhysicalFilter{Expression: &PhysicalPredicateExpression{Kind: PhysicalComparisonPredicate, Comparison: &predicate}}})
	}
	return nil
}

// buildOptionalChildPhysicalSet lowers one root-relative optional traversal.
// The set retains resource documents (rather than pre-shaped maps), allowing
// the renderer to apply each child selector against the document payload at
// the outer return while preserving root row grain.
