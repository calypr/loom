package ir

import (
	"fmt"

	"strings"
)

func validatePhysicalSlice(slice PhysicalSlice, defined map[string]bool, bindVars map[string]any) error {
	if err := validatePhysicalValue(slice.Source, defined, bindVars); err != nil {
		return err
	}
	if slice.Predicate != nil {
		if err := validatePhysicalPredicateExpression(*slice.Predicate, defined, bindVars); err != nil {
			return fmt.Errorf("slice predicate: %w", err)
		}
	}
	if slice.Sort == nil {
		return fmt.Errorf("slice requires a stable sort expression")
	}
	if err := validatePhysicalExpression(*slice.Sort, defined, bindVars); err != nil {
		return fmt.Errorf("slice sort: %w", err)
	}
	if err := requireBind(bindVars, slice.LimitBindKey); err != nil {
		return err
	}
	limit, ok := bindVars[slice.LimitBindKey].(int)
	if !ok || limit <= 0 {
		return fmt.Errorf("slice limit bind %q must be a positive int", slice.LimitBindKey)
	}
	return validatePhysicalExpressionProjections(slice.Projections, defined, bindVars, "slice")
}

func validatePhysicalObject(object PhysicalObject, defined map[string]bool, bindVars map[string]any) error {
	return validatePhysicalExpressionProjections(object.Fields, defined, bindVars, "object")
}

func validatePhysicalExpressionProjections(projections []PhysicalExpressionProjection, defined map[string]bool, bindVars map[string]any, owner string) error {
	if len(projections) == 0 {
		return fmt.Errorf("%s requires at least one projection", owner)
	}
	seen := map[string]bool{}
	for _, projection := range projections {
		if strings.TrimSpace(projection.Name) == "" || seen[projection.Name] {
			return fmt.Errorf("%s projection name %q is empty or duplicated", owner, projection.Name)
		}
		seen[projection.Name] = true
		if err := validatePhysicalExpression(projection.Expression, defined, bindVars); err != nil {
			return fmt.Errorf("%s projection %q: %w", owner, projection.Name, err)
		}
	}
	return nil
}

func validatePhysicalPredicateExpression(predicate PhysicalPredicateExpression, defined map[string]bool, bindVars map[string]any) error {
	switch predicate.Kind {
	case PhysicalComparisonPredicate:
		if predicate.Comparison == nil || len(predicate.Children) != 0 || predicate.Exists != nil {
			return fmt.Errorf("comparison predicate requires exactly one comparison")
		}
		return validatePhysicalPredicate(*predicate.Comparison, defined, bindVars)
	case PhysicalAllPredicate, PhysicalAnyPredicate:
		if predicate.Comparison != nil || predicate.Exists != nil || len(predicate.Children) == 0 {
			return fmt.Errorf("%s predicate requires one or more child predicates", predicate.Kind)
		}
		for index, child := range predicate.Children {
			if err := validatePhysicalPredicateExpression(child, defined, bindVars); err != nil {
				return fmt.Errorf("predicate child %d: %w", index, err)
			}
		}
		return nil
	case PhysicalNotPredicate:
		if predicate.Comparison != nil || predicate.Exists != nil || len(predicate.Children) != 1 {
			return fmt.Errorf("NOT predicate requires exactly one child predicate")
		}
		return validatePhysicalPredicateExpression(predicate.Children[0], defined, bindVars)
	case PhysicalExistsPredicate:
		if predicate.Comparison != nil || len(predicate.Children) != 0 || predicate.Exists == nil {
			return fmt.Errorf("EXISTS predicate requires exactly one subplan")
		}
		return validatePhysicalSubplan(*predicate.Exists, defined, bindVars)
	default:
		return fmt.Errorf("unknown predicate kind %q", predicate.Kind)
	}
}

func validatePhysicalSubplan(subplan PhysicalSubplan, parent map[string]bool, bindVars map[string]any) error {
	if len(subplan.Captures) == 0 {
		return fmt.Errorf("subplan requires at least one explicit capture")
	}
	defined := make(map[string]bool, len(subplan.Captures))
	for _, capture := range subplan.Captures {
		if !parent[capture] {
			return fmt.Errorf("subplan capture %q is out of scope", capture)
		}
		if err := definePhysicalVariable(defined, capture); err != nil {
			return fmt.Errorf("subplan capture: %w", err)
		}
	}
	if len(subplan.Operations) == 0 {
		return fmt.Errorf("subplan requires at least one operation")
	}
	for index, operation := range subplan.Operations {
		if operation.Kind == PhysicalRootScanOp || operation.Kind == PhysicalReturnOp || operation.Kind == PhysicalSortOp || operation.Kind == PhysicalLimitOp {
			return fmt.Errorf("subplan operation %d cannot be %s", index, operation.Kind)
		}
		if err := operation.validatePayload(); err != nil {
			return fmt.Errorf("subplan operation %d (%s): %w", index, operation.Kind, err)
		}
		switch operation.Kind {
		case PhysicalTraversalOp:
			traversal := operation.Traversal
			if !defined[traversal.SourceVariable] {
				return fmt.Errorf("subplan operation %d: traversal source variable %q is out of scope", index, traversal.SourceVariable)
			}
			if traversal.Direction != PhysicalOutbound && traversal.Direction != PhysicalInbound && traversal.Direction != PhysicalAny {
				return fmt.Errorf("subplan operation %d: invalid traversal direction %q", index, traversal.Direction)
			}
			if traversal.EdgeTargetTypeField != "" && !physicalPathPartPattern.MatchString(traversal.EdgeTargetTypeField) {
				return fmt.Errorf("subplan operation %d: unsafe traversal edge type field %q", index, traversal.EdgeTargetTypeField)
			}
			if err := validatePhysicalTraversalStrategy(*traversal); err != nil {
				return fmt.Errorf("subplan operation %d: %w", index, err)
			}
			if err := requireCollectionBind(bindVars, traversal.EdgeCollectionBindKey); err != nil {
				return fmt.Errorf("subplan operation %d: %w", index, err)
			}
			for _, key := range []string{traversal.EdgeLabelBindKey, traversal.TargetTypeBindKey} {
				if key != "" {
					if err := requireBind(bindVars, key); err != nil {
						return fmt.Errorf("subplan operation %d: %w", index, err)
					}
				}
			}
			if err := definePhysicalVariable(defined, traversal.TargetVariable); err != nil {
				return fmt.Errorf("subplan operation %d: %w", index, err)
			}
			if traversal.EdgeVariable != "" {
				if err := definePhysicalVariable(defined, traversal.EdgeVariable); err != nil {
					return fmt.Errorf("subplan operation %d: %w", index, err)
				}
			}
		case PhysicalFilterOp:
			if err := validatePhysicalFilter(*operation.Filter, defined, bindVars); err != nil {
				return fmt.Errorf("subplan operation %d: %w", index, err)
			}
		case PhysicalDerivedLetOp:
			if err := validatePhysicalDerivedLet(*operation.DerivedLet, defined, bindVars); err != nil {
				return fmt.Errorf("subplan operation %d: %w", index, err)
			}
			if err := definePhysicalVariable(defined, operation.DerivedLet.Variable); err != nil {
				return fmt.Errorf("subplan operation %d: %w", index, err)
			}
		case PhysicalSetOp:
			if err := validatePhysicalSet(*operation.Set, defined, bindVars); err != nil {
				return fmt.Errorf("subplan operation %d set %q: %w", index, operation.Set.Variable, err)
			}
			if err := definePhysicalVariable(defined, operation.Set.Variable); err != nil {
				return fmt.Errorf("subplan operation %d: %w", index, err)
			}
		case PhysicalUnnestOp:
			if err := validatePhysicalUnnest(*operation.Unnest, defined, bindVars); err != nil {
				return fmt.Errorf("subplan operation %d unnest: %w", index, err)
			}
			if err := definePhysicalVariable(defined, operation.Unnest.OutputVariable); err != nil {
				return fmt.Errorf("subplan operation %d: %w", index, err)
			}
			if operation.Unnest.Ordinality != "" {
				if err := definePhysicalVariable(defined, operation.Unnest.Ordinality); err != nil {
					return fmt.Errorf("subplan operation %d: %w", index, err)
				}
			}
		default:
			return fmt.Errorf("subplan operation %d has unsupported kind %q", index, operation.Kind)
		}
	}
	return validatePhysicalExpression(subplan.Return, defined, bindVars)
}

func validatePhysicalValue(value PhysicalValue, defined map[string]bool, bindVars map[string]any) error {
	hasVariable := value.Variable != ""
	hasBind := value.BindKey != ""
	if hasVariable == hasBind {
		return fmt.Errorf("physical value must reference exactly one variable or bind key")
	}
	if hasBind {
		if len(value.Path) != 0 {
			return fmt.Errorf("bind value %q cannot have a path", value.BindKey)
		}
		return requireBind(bindVars, value.BindKey)
	}
	if !defined[value.Variable] {
		return fmt.Errorf("variable %q is out of scope", value.Variable)
	}
	for _, part := range value.Path {
		if !physicalPathPartPattern.MatchString(part) {
			return fmt.Errorf("unsafe path segment %q", part)
		}
	}
	return nil
}
