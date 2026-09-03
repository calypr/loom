package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
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

func collectionBindKeys(plan ir.PhysicalPlan) (map[string]struct{}, error) {
	keys := map[string]struct{}{}
	var collectOperations func([]ir.PhysicalOperation, string) error
	collectOperations = func(operations []ir.PhysicalOperation, owner string) error {
		for index, operation := range operations {
			switch operation.Kind {
			case ir.PhysicalRootScanOp:
				keys[operation.RootScan.CollectionBindKey] = struct{}{}
			case ir.PhysicalTraversalOp:
				if operation.Traversal.EdgeCollectionBindKey == "" {
					return fmt.Errorf("%s operation %d (TRAVERSAL): edge collection bind key is required", owner, index)
				}
				keys[operation.Traversal.EdgeCollectionBindKey] = struct{}{}
			case ir.PhysicalPathExtendOp:
				if operation.PathExtend.Traversal.EdgeCollectionBindKey == "" {
					return fmt.Errorf("%s operation %d (PATH_EXTEND): edge collection bind key is required", owner, index)
				}
				keys[operation.PathExtend.Traversal.EdgeCollectionBindKey] = struct{}{}
				for _, scope := range operation.PathExtend.Scope {
					if scope.Kind == ir.PhysicalFilterOp && scope.Filter.Expression != nil {
						if err := collectPredicateCollections(*scope.Filter.Expression, collectOperations, owner+" PATH_EXTEND"); err != nil {
							return err
						}
					}
				}
			case ir.PhysicalFilterOp:
				if operation.Filter.Expression != nil {
					if err := collectPredicateCollections(*operation.Filter.Expression, collectOperations, owner); err != nil {
						return err
					}
				}
			case ir.PhysicalSetOp:
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

func collectPredicateCollections(predicate ir.PhysicalPredicateExpression, collectOperations func([]ir.PhysicalOperation, string) error, owner string) error {
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

func validateRenderablePhysicalPlan(plan ir.PhysicalPlan, collectionKeys map[string]struct{}) error {
	for index, operation := range plan.Operations {
		if err := validateRenderableOperation(operation, collectionKeys); err != nil {
			return fmt.Errorf("render operation %d (%s): %w", index, operation.Kind, err)
		}
	}
	return nil
}

func validateRenderableOperation(operation ir.PhysicalOperation, collectionKeys map[string]struct{}) error {
	valueIsCollection := func(value ir.PhysicalValue) error {
		if _, isCollection := collectionKeys[value.BindKey]; isCollection {
			return fmt.Errorf("bind key %q cannot be used as both a collection and scalar bind", value.BindKey)
		}
		return nil
	}
	checkValue := func(value ir.PhysicalValue) error {
		if err := valueIsCollection(value); err != nil {
			return err
		}
		return nil
	}

	switch operation.Kind {
	case ir.PhysicalRootScanOp:
		return nil
	case ir.PhysicalTraversalOp:
		traversal := operation.Traversal
		if traversal.EdgeVariable == "" {
			return fmt.Errorf("TRAVERSAL requires an edge variable for edge-label and project scope checks")
		}
		if traversal.EdgeLabelBindKey == "" || traversal.TargetTypeBindKey == "" {
			return fmt.Errorf("TRAVERSAL requires edge label and target resource type bind keys")
		}
		return nil
	case ir.PhysicalSetOp:
		for index, suboperation := range operation.Set.Subplan.Operations {
			if err := validateRenderableOperation(suboperation, collectionKeys); err != nil {
				return fmt.Errorf("SET subplan operation %d: %w", index, err)
			}
		}
		return nil
	case ir.PhysicalPathSeedOp, ir.PhysicalPathExtendOp, ir.PhysicalGraphReturnOp:
		return nil
	case ir.PhysicalUnnestOp:
		if operation.Unnest == nil {
			return fmt.Errorf("UNNEST requires a payload")
		}
		if operation.Unnest.Expression.Cardinality != ir.PhysicalArrayCardinality {
			return fmt.Errorf("UNNEST source expression must be array-valued")
		}
		return nil
	case ir.PhysicalFilterOp:
		if operation.Filter.Expression != nil {
			return validateRenderablePredicateExpression(*operation.Filter.Expression, collectionKeys)
		}
		switch strings.ToUpper(strings.TrimSpace(operation.Filter.Predicate.Operator)) {
		case "EQUALS", "IN", "GT":
		default:
			return fmt.Errorf("unsupported physical filter operator %q", operation.Filter.Predicate.Operator)
		}
		if operation.Filter.Predicate.Right == nil {
			return fmt.Errorf("physical filter operator %q requires a right value", operation.Filter.Predicate.Operator)
		}
		if err := checkValue(operation.Filter.Predicate.Left); err != nil {
			return err
		}
		return checkValue(*operation.Filter.Predicate.Right)
	case ir.PhysicalDerivedLetOp:
		if strings.ToUpper(strings.TrimSpace(operation.DerivedLet.Operator)) != "AUTH_RESOURCE_PATH_ALLOWED" {
			return fmt.Errorf("unsupported physical derived LET operator %q", operation.DerivedLet.Operator)
		}
		for _, input := range operation.DerivedLet.Inputs {
			if err := checkValue(input); err != nil {
				return err
			}
		}
		return nil
	case ir.PhysicalExpressionLetOp:
		if operation.ExpressionLet == nil {
			return fmt.Errorf("expression LET requires a payload")
		}
		return nil
	case ir.PhysicalSortOp:
		return checkValue(operation.Sort.Value)
	case ir.PhysicalLimitOp:
		if _, isCollection := collectionKeys[operation.Limit.BindKey]; isCollection {
			return fmt.Errorf("bind key %q cannot be used as both a collection and scalar bind", operation.Limit.BindKey)
		}
		return nil
	case ir.PhysicalReturnOp:
		for _, projection := range operation.Return.Projections {
			if projection.Expression != nil {
				if projection.Expression.Kind != ir.PhysicalValueExpression && projection.Expression.Kind != ir.PhysicalExtractExpression && projection.Expression.Kind != ir.PhysicalAggregateExpression && projection.Expression.Kind != ir.PhysicalPivotExpression && projection.Expression.Kind != ir.PhysicalSliceExpression && projection.Expression.Kind != ir.PhysicalObjectLookupExpression && projection.Expression.Kind != ir.PhysicalKeyedMapExpression && projection.Expression.Kind != ir.PhysicalObjectKeysExpression && projection.Expression.Kind != ir.PhysicalKeySetExpression && projection.Expression.Kind != ir.PhysicalObjectExpression && projection.Expression.Kind != ir.PhysicalCallExpression {
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

func validateRenderablePredicateExpression(predicate ir.PhysicalPredicateExpression, collectionKeys map[string]struct{}) error {
	switch predicate.Kind {
	case ir.PhysicalExistsPredicate:
		if predicate.Exists == nil {
			return fmt.Errorf("EXISTS predicate requires a subplan")
		}
		for index, operation := range predicate.Exists.Operations {
			if err := validateRenderableOperation(operation, collectionKeys); err != nil {
				return fmt.Errorf("EXISTS subplan operation %d (%s): %w", index, operation.Kind, err)
			}
		}
		if predicate.Exists.Return.Kind != ir.PhysicalValueExpression || predicate.Exists.Return.Value == nil {
			return fmt.Errorf("EXISTS subplan return must be a physical value expression")
		}
		return nil
	case ir.PhysicalComparisonPredicate:
		if predicate.Comparison == nil {
			return fmt.Errorf("comparison predicate requires a comparison")
		}
		return nil
	case ir.PhysicalAllPredicate, ir.PhysicalAnyPredicate, ir.PhysicalNotPredicate:
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
