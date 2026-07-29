package aql

import (
	"fmt"
	"strings"
)

func (r *physicalPlanRenderer) newInternalBindKey(suffix string) string {
	base := "__loom_physical_" + suffix
	key := base
	for counter := 1; ; counter++ {
		if _, exists := r.bindVars[key]; !exists {
			return key
		}
		key = fmt.Sprintf("%s_%d", base, counter)
	}
}

func collectionBindKeys(plan PhysicalPlan) (map[string]struct{}, error) {
	keys := map[string]struct{}{}
	var collectOperations func([]PhysicalOperation, string) error
	collectOperations = func(operations []PhysicalOperation, owner string) error {
		for index, operation := range operations {
			switch operation.Kind {
			case PhysicalRootScanOp:
				keys[operation.RootScan.CollectionBindKey] = struct{}{}
			case PhysicalTraversalOp:
				if operation.Traversal.EdgeCollectionBindKey == "" {
					return fmt.Errorf("%s operation %d (TRAVERSAL): edge collection bind key is required", owner, index)
				}
				keys[operation.Traversal.EdgeCollectionBindKey] = struct{}{}
			case PhysicalPathExtendOp:
				if operation.PathExtend.Traversal.EdgeCollectionBindKey == "" {
					return fmt.Errorf("%s operation %d (PATH_EXTEND): edge collection bind key is required", owner, index)
				}
				keys[operation.PathExtend.Traversal.EdgeCollectionBindKey] = struct{}{}
				for _, scope := range operation.PathExtend.Scope {
					if scope.Kind == PhysicalFilterOp && scope.Filter.Expression != nil {
						if err := collectPredicateCollections(*scope.Filter.Expression, collectOperations, owner+" PATH_EXTEND"); err != nil {
							return err
						}
					}
				}
			case PhysicalFilterOp:
				if operation.Filter.Expression != nil {
					if err := collectPredicateCollections(*operation.Filter.Expression, collectOperations, owner); err != nil {
						return err
					}
				}
			case PhysicalSetOp:
				if err := collectOperations(operation.Set.Subplan.Operations, owner+" SET"); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := collectOperations(plan.Operations, "render"); err != nil {
		return nil, err
	}
	for key := range keys {
		value, ok := plan.BindVars[key]
		if !ok {
			return nil, fmt.Errorf("collection bind key %q is not defined", key)
		}
		collection, ok := value.(string)
		if !ok || strings.TrimSpace(collection) == "" {
			return nil, fmt.Errorf("collection bind key %q must have a non-empty string value", key)
		}
	}
	return keys, nil
}

func collectPredicateCollections(predicate PhysicalPredicateExpression, collectOperations func([]PhysicalOperation, string) error, owner string) error {
	if predicate.Exists != nil {
		return collectOperations(predicate.Exists.Operations, owner+" EXISTS")
	}
	for _, child := range predicate.Children {
		if err := collectPredicateCollections(child, collectOperations, owner); err != nil {
			return err
		}
	}
	return nil
}

func validateRenderablePhysicalPlan(plan PhysicalPlan, collectionKeys map[string]struct{}) error {
	for index, operation := range plan.Operations {
		if err := validateRenderableOperation(operation, collectionKeys); err != nil {
			return fmt.Errorf("render operation %d (%s): %w", index, operation.Kind, err)
		}
	}
	return nil
}

func validateRenderableOperation(operation PhysicalOperation, collectionKeys map[string]struct{}) error {
	valueIsCollection := func(value PhysicalValue) error {
		if _, isCollection := collectionKeys[value.BindKey]; isCollection {
			return fmt.Errorf("bind key %q cannot be used as both a collection and scalar bind", value.BindKey)
		}
		return nil
	}
	checkValue := func(value PhysicalValue) error {
		if err := valueIsCollection(value); err != nil {
			return err
		}
		return nil
	}

	switch operation.Kind {
	case PhysicalRootScanOp:
		return nil
	case PhysicalTraversalOp:
		traversal := operation.Traversal
		if traversal.EdgeVariable == "" {
			return fmt.Errorf("TRAVERSAL requires an edge variable for edge-label and project scope checks")
		}
		if traversal.EdgeLabelBindKey == "" || traversal.TargetTypeBindKey == "" {
			return fmt.Errorf("TRAVERSAL requires edge label and target resource type bind keys")
		}
		return nil
	case PhysicalSetOp:
		for index, suboperation := range operation.Set.Subplan.Operations {
			if err := validateRenderableOperation(suboperation, collectionKeys); err != nil {
				return fmt.Errorf("SET subplan operation %d: %w", index, err)
			}
		}
		return nil
	case PhysicalPathSeedOp, PhysicalPathExtendOp, PhysicalGraphReturnOp:
		return nil
	case PhysicalUnnestOp:
		if operation.Unnest == nil {
			return fmt.Errorf("UNNEST requires a payload")
		}
		if operation.Unnest.Expression.Cardinality != PhysicalArrayCardinality {
			return fmt.Errorf("UNNEST source expression must be array-valued")
		}
		return nil
	case PhysicalFilterOp:
		if operation.Filter.Expression != nil {
			return validateRenderablePredicateExpression(*operation.Filter.Expression, collectionKeys)
		}
		if strings.ToUpper(strings.TrimSpace(operation.Filter.Predicate.Operator)) != "EQUALS" {
			return fmt.Errorf("unsupported physical filter operator %q", operation.Filter.Predicate.Operator)
		}
		if operation.Filter.Predicate.Right == nil {
			return fmt.Errorf("EQUALS filter requires a right value")
		}
		if err := checkValue(operation.Filter.Predicate.Left); err != nil {
			return err
		}
		return checkValue(*operation.Filter.Predicate.Right)
	case PhysicalDerivedLetOp:
		if strings.ToUpper(strings.TrimSpace(operation.DerivedLet.Operator)) != "AUTH_RESOURCE_PATH_ALLOWED" {
			return fmt.Errorf("unsupported physical derived LET operator %q", operation.DerivedLet.Operator)
		}
		for _, input := range operation.DerivedLet.Inputs {
			if err := checkValue(input); err != nil {
				return err
			}
		}
		return nil
	case PhysicalExpressionLetOp:
		if operation.ExpressionLet == nil {
			return fmt.Errorf("expression LET requires a payload")
		}
		return nil
	case PhysicalSortOp:
		return checkValue(operation.Sort.Value)
	case PhysicalLimitOp:
		if _, isCollection := collectionKeys[operation.Limit.BindKey]; isCollection {
			return fmt.Errorf("bind key %q cannot be used as both a collection and scalar bind", operation.Limit.BindKey)
		}
		return nil
	case PhysicalReturnOp:
		for _, projection := range operation.Return.Projections {
			if projection.Expression != nil {
				if projection.Expression.Kind != PhysicalValueExpression && projection.Expression.Kind != PhysicalExtractExpression && projection.Expression.Kind != PhysicalAggregateExpression && projection.Expression.Kind != PhysicalPivotExpression && projection.Expression.Kind != PhysicalSliceExpression && projection.Expression.Kind != PhysicalLookupExpression && projection.Expression.Kind != PhysicalObjectLookupExpression && projection.Expression.Kind != PhysicalKeyedMapExpression && projection.Expression.Kind != PhysicalObjectKeysExpression && projection.Expression.Kind != PhysicalKeySetExpression && projection.Expression.Kind != PhysicalObjectExpression && projection.Expression.Kind != PhysicalCallExpression {
					return fmt.Errorf("unsupported physical return expression kind %q", projection.Expression.Kind)
				}
				continue
			}
			if err := checkValue(projection.Value); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported physical operation %q", operation.Kind)
	}
}

func validateRenderablePredicateExpression(predicate PhysicalPredicateExpression, collectionKeys map[string]struct{}) error {
	switch predicate.Kind {
	case PhysicalExistsPredicate:
		if predicate.Exists == nil {
			return fmt.Errorf("EXISTS predicate requires a subplan")
		}
		for index, operation := range predicate.Exists.Operations {
			if err := validateRenderableOperation(operation, collectionKeys); err != nil {
				return fmt.Errorf("EXISTS subplan operation %d (%s): %w", index, operation.Kind, err)
			}
		}
		if predicate.Exists.Return.Kind != PhysicalValueExpression || predicate.Exists.Return.Value == nil {
			return fmt.Errorf("EXISTS subplan return must be a physical value expression")
		}
		return nil
	case PhysicalComparisonPredicate:
		if predicate.Comparison == nil {
			return fmt.Errorf("comparison predicate requires a comparison")
		}
		return nil
	case PhysicalAllPredicate, PhysicalAnyPredicate, PhysicalNotPredicate:
		for _, child := range predicate.Children {
			if err := validateRenderablePredicateExpression(child, collectionKeys); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported physical predicate kind %q", predicate.Kind)
	}
}

func runtimePhysicalBindVars(bindVars map[string]any, collectionKeys map[string]struct{}) map[string]any {
	out := make(map[string]any, len(bindVars))
	for key, value := range bindVars {
		if _, collectionBinding := collectionKeys[key]; collectionBinding {
			out["@"+key] = clonePhysicalBindValue(value)
			continue
		}
		out[key] = clonePhysicalBindValue(value)
	}
	return out
}

func clonePhysicalBindValue(value any) any {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = clonePhysicalBindValue(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = clonePhysicalBindValue(item)
		}
		return out
	default:
		return value
	}
}
