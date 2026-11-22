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

	if err := validatePhysicalExpressionObjectCycles(PhysicalExpression{
		Aggregate: &PhysicalAggregate{Predicate: &predicate},
	}); err != nil {
		return err
	}
	return validatePhysicalPredicateExpressionValidated(predicate, defined, bindVars)
}

func validatePhysicalPredicateExpressionValidated(predicate PhysicalPredicateExpression, defined map[string]bool, bindVars map[string]any) error {
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
			if err := validatePhysicalPredicateExpressionValidated(child, defined, bindVars); err != nil {
				return fmt.Errorf("predicate child %d: %w", index, err)
			}
		}
		return nil
	case PhysicalNotPredicate:
		if predicate.Comparison != nil || predicate.Exists != nil || len(predicate.Children) != 1 {
			return fmt.Errorf("NOT predicate requires exactly one child predicate")
		}
		return validatePhysicalPredicateExpressionValidated(predicate.Children[0], defined, bindVars)
	case PhysicalExistsPredicate:
		if predicate.Comparison != nil || len(predicate.Children) != 0 || predicate.Exists == nil {
			return fmt.Errorf("EXISTS predicate requires exactly one subplan")
		}
		if predicate.Exists.Sort != nil || predicate.Exists.Unique {
			return fmt.Errorf("EXISTS subplan cannot use projection-only sort or unique modifiers")
		}
		return validatePhysicalSubplan(*predicate.Exists, defined, bindVars)
	default:
		return fmt.Errorf("unknown predicate kind %q", predicate.Kind)
	}
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
