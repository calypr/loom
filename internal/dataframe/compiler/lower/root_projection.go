package lower

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func rootPhysicalProjections(physical *ir.PhysicalPlan, root semantic.SemanticNode) ([]ir.PhysicalProjection, error) {
	projections := []ir.PhysicalProjection{{Name: "_key", Value: ir.PhysicalValue{Variable: "root", Path: []string{"_key"}}}}
	for index, field := range root.Fields {
		// Whole-document projections are lowered from the canonical expression
		// after the generic semantic graph has established root scope. They do
		// not have a FHIR selector to resolve here.
		if field.Expr != nil && field.Expr.Kind == expression.DocumentRefNode {
			placeholder := lowerDocumentRef(*field.Expr.Document, ir.PhysicalPreserveNull)
			projections = append(projections, ir.PhysicalProjection{Name: field.Name, Expression: &placeholder})
			continue
		}
		selection, err := semantic.ResolveSemanticField(root.ResourceType, root.Alias, index, field)
		if err != nil {
			return nil, err
		}
		cardinality, distinct := ir.PhysicalScalarCardinality, false
		switch selection.Projection {
		case spec.ProjectionArray:
			cardinality = ir.PhysicalArrayCardinality
		case spec.ProjectionDistinctArray:
			cardinality, distinct = ir.PhysicalArrayCardinality, true
		case spec.ProjectionScalar, spec.ProjectionFirst:
		default:
			return nil, fmt.Errorf("root field %q has unsupported projection %q", field.Name, selection.Projection)
		}
		expression := ir.PhysicalExpression{
			Kind: ir.PhysicalExtractExpression, Cardinality: cardinality, NullBehavior: ir.PhysicalPreserveNull,
			Extract: &ir.PhysicalExtract{Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}}, ResourceType: root.ResourceType, Selector: field.Selector, Fallbacks: append([]spec.Selector(nil), field.Fallbacks...), Distinct: distinct, ExecutionMode: selectorExecutionMode(root.ResourceType, field.Selector, field.Fallbacks...)},
		}
		if cardinality == ir.PhysicalArrayCardinality {
			expression.NullBehavior = ir.PhysicalEmptyOnNull
		}
		projections = append(projections, ir.PhysicalProjection{Name: field.Name, Expression: &expression})
	}
	for _, aggregate := range root.Aggregates {
		expression, err := physicalAggregateExpression(physical, root.ResourceType, ir.PhysicalValue{Variable: "root"}, aggregate, false)
		if err != nil {
			return nil, err
		}
		projections = append(projections, ir.PhysicalProjection{Name: aggregate.Name, Expression: &expression})
	}
	for _, pivot := range root.Pivots {
		pivotProjections, err := physicalPivotProjections(physical, root.ResourceType, ir.PhysicalValue{Variable: "root"}, pivot, "")
		if err != nil {
			return nil, err
		}
		projections = append(projections, pivotProjections...)
	}
	for _, slice := range root.Slices {
		expression, err := physicalSliceExpression(physical, root.ResourceType, ir.PhysicalValue{Variable: "root"}, slice)
		if err != nil {
			return nil, err
		}
		projections = append(projections, ir.PhysicalProjection{Name: slice.Name, Expression: &expression})
	}
	return projections, nil
}

func physicalPivotProjections(physical *ir.PhysicalPlan, resourceType string, source ir.PhysicalValue, pivot semantic.SemanticPivot, prefix string) ([]ir.PhysicalProjection, error) {
	if len(pivot.Columns) == 0 {
		return nil, fmt.Errorf("pivot %q requires bounded columns", pivot.Name)
	}
	projections := make([]ir.PhysicalProjection, 0, len(pivot.Columns))
	columnsBindKey := "pivot_" + sanitizeColumnName(source.Variable) + "_" + sanitizeColumnName(pivot.Name) + "_columns"
	physical.BindVars[columnsBindKey] = append([]string(nil), pivot.Columns...)
	familyVariable := "__loom_pivot_" + sanitizeColumnName(source.Variable) + "_" + sanitizeColumnName(pivot.Name)
	for index := 1; deferredExpressionVariableExists(*physical, familyVariable); index++ {
		familyVariable = fmt.Sprintf("__loom_pivot_%s_%s_%d", sanitizeColumnName(source.Variable), sanitizeColumnName(pivot.Name), index)
	}
	sharedExpression := ir.PhysicalExpression{Kind: ir.PhysicalPivotExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalPreserveNull,
		Pivot: &ir.PhysicalPivotMap{Source: source, ResourceType: resourceType, ItemSource: pivot.ItemSource, ItemResourceType: pivot.ItemResourceType, KeySelector: pivot.ColumnSelector, ValueSelector: pivot.ValueSelector, ValueFallbacks: append([]spec.Selector(nil), pivot.ValueFallbacks...), StringifyValue: pivot.StringifyValue, ColumnsBindKey: columnsBindKey, FlattenSingleColumn: false}}
	physical.DeferredExpressionLets = append(physical.DeferredExpressionLets, ir.PhysicalOperation{Kind: ir.PhysicalExpressionLetOp, Source: ir.PhysicalSource{ResourceType: resourceType, SemanticField: "pivot_family"}, ExpressionLet: &ir.PhysicalExpressionLet{Variable: familyVariable, Expression: sharedExpression}})
	for _, column := range pivot.Columns {
		columnBindKey := columnsBindKey + "_" + sanitizeColumnName(column)
		physical.BindVars[columnBindKey] = column
		expression := ir.PhysicalExpression{Kind: ir.PhysicalObjectLookupExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, ObjectLookup: &ir.PhysicalObjectLookup{ObjectVariable: familyVariable, KeyBindKey: columnBindKey}}
		projections = append(projections, ir.PhysicalProjection{Name: prefix + pivot.Name + "__" + sanitizeColumnName(column), Expression: &expression})
	}
	return projections, nil
}

func deferredExpressionVariableExists(physical ir.PhysicalPlan, variable string) bool {
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
func physicalAggregateExpression(physical *ir.PhysicalPlan, resourceType string, source ir.PhysicalValue, aggregate semantic.SemanticAggregate, sourceIsSet bool) (ir.PhysicalExpression, error) {
	op := ir.PhysicalAggregateOperation(strings.ToUpper(strings.TrimSpace(aggregate.Operation)))
	switch op {
	case ir.PhysicalCountAggregate, ir.PhysicalCountDistinctAggregate, ir.PhysicalExistsAggregate, ir.PhysicalDistinctValuesAggregate, ir.PhysicalMinAggregate, ir.PhysicalMaxAggregate, ir.PhysicalFirstAggregate:
	default:
		return ir.PhysicalExpression{}, fmt.Errorf("aggregate %q uses unsupported operation %q", aggregate.Name, aggregate.Operation)
	}
	aggregatePhysical := ir.PhysicalAggregate{Source: source, Operation: op}
	if aggregate.Selector != nil {
		valueSource := source
		if !sourceIsSet && source.Variable != "" && len(source.Path) == 0 {
			valueSource = ir.PhysicalValue{Variable: source.Variable, Path: []string{"payload"}}
		}
		value := ir.PhysicalExpression{Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalEmptyOnNull,
			Extract: &ir.PhysicalExtract{Source: valueSource, ResourceType: resourceType, Selector: *aggregate.Selector, ExecutionMode: selectorExecutionMode(resourceType, *aggregate.Selector)}}
		aggregatePhysical.Value = &value
	}
	if aggregate.Predicate != nil {
		leftSource := source
		if !sourceIsSet && source.Variable != "" && len(source.Path) == 0 {
			leftSource = ir.PhysicalValue{Variable: source.Variable, Path: []string{"payload"}}
		}
		left := ir.PhysicalExpression{Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalEmptyOnNull,
			Extract: &ir.PhysicalExtract{Source: leftSource, ResourceType: resourceType, Selector: *aggregate.Predicate, ExecutionMode: selectorExecutionMode(resourceType, *aggregate.Predicate)}}
		comparison := &ir.PhysicalPredicate{Operator: "EXISTS", LeftExpression: &left}
		if aggregate.PredicateEquals != "" {
			comparison.Operator = "EQUALS"
			comparison.ValueKind = spec.FilterString
			key := "aggregate_" + sanitizeColumnName(source.Variable) + "_" + sanitizeColumnName(aggregate.Name) + "_predicate_equals"
			physical.BindVars[key] = aggregate.PredicateEquals
			comparison.Right = &ir.PhysicalValue{BindKey: key}
		}
		aggregatePhysical.Predicate = &ir.PhysicalPredicateExpression{Kind: ir.PhysicalComparisonPredicate, Comparison: comparison}
	}
	cardinality := ir.PhysicalScalarCardinality
	nullBehavior := ir.PhysicalEmptyOnNull
	if op == ir.PhysicalDistinctValuesAggregate {
		cardinality = ir.PhysicalArrayCardinality
	}
	return ir.PhysicalExpression{Kind: ir.PhysicalAggregateExpression, Cardinality: cardinality, NullBehavior: nullBehavior, Aggregate: &aggregatePhysical}, nil
}

// physicalSliceExpression lowers a representative slice into a typed,
// bounded projection over either the singleton root document or a correlated
// child set. The source set is already materialized in stable _key order;
// the explicit sort expression below makes that ordering part of the slice
// contract and gives the renderer a deterministic tie-break.
func physicalSliceExpression(physical *ir.PhysicalPlan, resourceType string, source ir.PhysicalValue, slice semantic.SemanticSlice) (ir.PhysicalExpression, error) {
	if slice.Limit <= 0 {
		return ir.PhysicalExpression{}, fmt.Errorf("slice %q requires positive limit", slice.Name)
	}
	limitKey := "slice_" + sanitizeColumnName(source.Variable) + "_" + sanitizeColumnName(slice.Name) + "_limit"
	physical.BindVars[limitKey] = slice.Limit
	// Expressions are anchored to the declared source variable for validation;
	// the renderer rebinds them to its per-item loop variable.
	leftSource := source
	physicalSlice := ir.PhysicalSlice{
		Source:       source,
		LimitBindKey: limitKey,
		Sort:         &ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{Variable: source.Variable, Path: []string{"_key"}}},
		Projections:  make([]ir.PhysicalExpressionProjection, 0, len(slice.Fields)),
	}
	if slice.Predicate != nil {
		left := ir.PhysicalExpression{Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalEmptyOnNull,
			Extract: &ir.PhysicalExtract{Source: leftSource, ResourceType: resourceType, Selector: *slice.Predicate, ExecutionMode: selectorExecutionMode(resourceType, *slice.Predicate)}}
		comparison := &ir.PhysicalPredicate{Operator: "EXISTS", LeftExpression: &left}
		if slice.PredicateEquals != "" {
			key := "slice_" + sanitizeColumnName(source.Variable) + "_" + sanitizeColumnName(slice.Name) + "_predicate_equals"
			physical.BindVars[key] = slice.PredicateEquals
			comparison.Operator = "EQUALS"
			comparison.ValueKind = spec.FilterString
			comparison.Right = &ir.PhysicalValue{BindKey: key}
		}
		physicalSlice.Predicate = &ir.PhysicalPredicateExpression{Kind: ir.PhysicalComparisonPredicate, Comparison: comparison}
	}
	for index, field := range slice.Fields {
		selection := field
		if selection.Name == "" {
			return ir.PhysicalExpression{}, fmt.Errorf("slice %q field %d requires name", slice.Name, index)
		}
		physicalSlice.Projections = append(physicalSlice.Projections, ir.PhysicalExpressionProjection{
			Name: selection.Name,
			Expression: ir.PhysicalExpression{Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull,
				Extract: &ir.PhysicalExtract{Source: leftSource, ResourceType: resourceType, Selector: selection.Selector, Fallbacks: append([]spec.Selector(nil), selection.Fallbacks...), ExecutionMode: selectorExecutionMode(resourceType, selection.Selector, selection.Fallbacks...)}},
		})
	}
	return ir.PhysicalExpression{Kind: ir.PhysicalSliceExpression, Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalEmptyOnNull, Slice: &physicalSlice}, nil
}

func appendRootPhysicalFilters(physical *ir.PhysicalPlan, root semantic.SemanticNode) error {
	for index, filter := range root.Filters {
		if err := spec.ValidateTypedFilterForResource(root.ResourceType, filter); err != nil {
			return fmt.Errorf("root filter %q: %w", filter.FieldRef, err)
		}
		selector, err := spec.ParseSelector(filter.Selector)
		if err != nil {
			return fmt.Errorf("root filter %q selector: %w", filter.FieldRef, err)
		}
		predicate := ir.PhysicalPredicate{
			Operator: string(filter.Operator), Quantifier: filter.Quantifier, ValueKind: filter.FieldKind,
			LeftExpression: &ir.PhysicalExpression{Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalEmptyOnNull,
				Extract: &ir.PhysicalExtract{Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}}, ResourceType: root.ResourceType, Selector: selector, ExecutionMode: selectorExecutionMode(root.ResourceType, selector)}},
		}
		if filter.Operator != spec.FilterExists && filter.Operator != spec.FilterMissing {
			key := fmt.Sprintf("root_filter_%d_value", index+1)
			if filter.Operator == spec.FilterIn {
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
			predicate.Right = &ir.PhysicalValue{BindKey: key}
		}
		physical.Operations = append(physical.Operations, ir.PhysicalOperation{Kind: ir.PhysicalFilterOp,
			Source: ir.PhysicalSource{SemanticNode: root.Alias, ResourceType: root.ResourceType, SemanticField: filter.FieldRef},
			Filter: &ir.PhysicalFilter{Expression: &ir.PhysicalPredicateExpression{Kind: ir.PhysicalComparisonPredicate, Comparison: &predicate}}})
	}
	return nil
}

// buildOptionalChildPhysicalSet lowers one root-relative optional traversal.
// The set retains resource documents (rather than pre-shaped maps), allowing
// the renderer to apply each child selector against the document payload at
// the outer return while preserving root row grain.
