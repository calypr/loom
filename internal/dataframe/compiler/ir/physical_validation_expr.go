package ir

import (
	"fmt"

	"strings"
)

func validatePhysicalExpression(expression PhysicalExpression, defined map[string]bool, bindVars map[string]any) error {
	if err := validatePhysicalExpressionObjectCycles(expression); err != nil {
		return err
	}
	if !expression.Cardinality.valid() {
		return fmt.Errorf("expression has invalid cardinality %q", expression.Cardinality)
	}
	if !expression.NullBehavior.valid() {
		return fmt.Errorf("expression has invalid null behavior %q", expression.NullBehavior)
	}
	payloads := 0
	if expression.Value != nil {
		payloads++
	}
	if expression.Literal != nil {
		payloads++
	}
	if expression.Extract != nil {
		payloads++
	}
	if expression.Aggregate != nil {
		payloads++
	}
	if expression.Pivot != nil {
		payloads++
	}
	if expression.Slice != nil {
		payloads++
	}
	if expression.ObjectLookup != nil {
		payloads++
	}
	if expression.KeyedMap != nil {
		payloads++
	}
	if expression.ObjectKeys != nil {
		payloads++
	}
	if expression.KeySet != nil {
		payloads++
	}
	if expression.Object != nil {
		payloads++
	}
	if expression.Call != nil {
		payloads++
	}
	if payloads != 1 {
		return fmt.Errorf("expression must contain exactly one payload")
	}
	switch expression.Kind {
	case PhysicalValueExpression:
		if expression.Value == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		return validatePhysicalValue(*expression.Value, defined, bindVars)
	case PhysicalLiteralExpression:
		if expression.Literal == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		if err := requireBind(bindVars, expression.Literal.BindKey); err != nil {
			return err
		}
		return nil
	case PhysicalExtractExpression:
		if expression.Extract == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		return validatePhysicalExtract(*expression.Extract, defined, bindVars)
	case PhysicalAggregateExpression:
		if expression.Aggregate == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		return validatePhysicalAggregate(*expression.Aggregate, defined, bindVars)
	case PhysicalPivotExpression:
		if expression.Pivot == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		return validatePhysicalPivot(*expression.Pivot, defined, bindVars)
	case PhysicalSliceExpression:
		if expression.Slice == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		return validatePhysicalSlice(*expression.Slice, defined, bindVars)
	case PhysicalObjectLookupExpression:
		if expression.ObjectLookup == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		if !defined[expression.ObjectLookup.ObjectVariable] {
			return fmt.Errorf("object lookup references undefined variable %q", expression.ObjectLookup.ObjectVariable)
		}
		return requireBind(bindVars, expression.ObjectLookup.KeyBindKey)
	case PhysicalKeyedMapExpression:
		if expression.KeyedMap == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		return validatePhysicalKeyedMap(*expression.KeyedMap, defined, bindVars)
	case PhysicalObjectKeysExpression:
		if expression.ObjectKeys == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		if !defined[expression.ObjectKeys.ObjectVariable] {
			return fmt.Errorf("object keys references undefined variable %q", expression.ObjectKeys.ObjectVariable)
		}
		return nil
	case PhysicalKeySetExpression:
		if expression.KeySet == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		return validatePhysicalKeySet(*expression.KeySet, defined, bindVars)
	case PhysicalObjectExpression:
		if expression.Object == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		return validatePhysicalObject(*expression.Object, defined, bindVars)
	case PhysicalCallExpression:
		if expression.Call == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		return validatePhysicalCall(*expression.Call, defined, bindVars)
	default:
		return fmt.Errorf("unknown expression kind %q", expression.Kind)
	}
}

func validatePhysicalKeyedMap(keyed PhysicalKeyedMap, defined map[string]bool, bindVars map[string]any) error {
	if keyed.Source.Cardinality != PhysicalArrayCardinality {
		return fmt.Errorf("keyed map source expression must be array-valued, got %q", keyed.Source.Cardinality)
	}
	if !physicalVariablePattern.MatchString(keyed.ItemVariable) {
		return fmt.Errorf("keyed map item variable %q is unsafe", keyed.ItemVariable)
	}
	if keyed.Reduction != PhysicalMapFirst && keyed.Reduction != PhysicalMapFirstSorted {
		return fmt.Errorf("unsupported keyed map reduction %q", keyed.Reduction)
	}
	if err := validatePhysicalExpression(keyed.Source, defined, bindVars); err != nil {
		return fmt.Errorf("keyed map source: %w", err)
	}
	itemDefined := make(map[string]bool, len(defined)+1)
	for name, value := range defined {
		itemDefined[name] = value
	}
	itemDefined[keyed.ItemVariable] = true
	if err := validatePhysicalExpression(keyed.ItemKey, itemDefined, bindVars); err != nil {
		return fmt.Errorf("keyed map item key: %w", err)
	}
	if err := validatePhysicalExpression(keyed.ItemValue, itemDefined, bindVars); err != nil {
		return fmt.Errorf("keyed map item value: %w", err)
	}
	for index, fallback := range keyed.ValueFallbacks {
		if err := validatePhysicalExpression(fallback, itemDefined, bindVars); err != nil {
			return fmt.Errorf("keyed map fallback %d: %w", index, err)
		}
	}
	return nil
}

func validatePhysicalKeySet(keySet PhysicalKeySet, defined map[string]bool, bindVars map[string]any) error {
	if !physicalVariablePattern.MatchString(keySet.ItemVariable) {
		return fmt.Errorf("key set item variable %q is unsafe", keySet.ItemVariable)
	}
	if keySet.Source.Cardinality != PhysicalArrayCardinality {
		return fmt.Errorf("key set source expression must be array-valued, got %q", keySet.Source.Cardinality)
	}
	if err := validatePhysicalExpression(keySet.Source, defined, bindVars); err != nil {
		return fmt.Errorf("key set source: %w", err)
	}
	itemDefined := make(map[string]bool, len(defined)+1)
	for name, value := range defined {
		itemDefined[name] = value
	}
	itemDefined[keySet.ItemVariable] = true
	if err := validatePhysicalExpression(keySet.ItemKey, itemDefined, bindVars); err != nil {
		return fmt.Errorf("key set item key: %w", err)
	}
	return nil
}

// validatePhysicalCall checks the small, backend-neutral operator vocabulary
// accepted by the AQL renderer. Keeping this list here prevents a recipe from
// smuggling arbitrary function or query text through the physical plan.
func validatePhysicalCall(call PhysicalCall, defined map[string]bool, bindVars map[string]any) error {
	name := strings.ToLower(strings.TrimSpace(call.Name))
	if name == "" {
		return fmt.Errorf("call name is required")
	}
	known := map[string]bool{
		"coalesce": true, "coalesce_string": true, "fallback": true, "first": true, "all": true, "distinct": true,
		"canonical_json": true,
		"concat":         true, "join": true, "cast": true, "reference_id": true,
		"path_segment": true, "basename": true, "last_segment": true,
		"sanitize_name": true, "sanitize_graphql_name": true, "uuid3": true, "uuid5": true,
		"if": true, "case": true, "not": true, "and": true, "or": true,
		"eq": true, "neq": true, "gt": true, "gte": true, "lt": true, "lte": true,
		"contains": true,
	}
	if !known[name] {
		return fmt.Errorf("unsupported call %q", call.Name)
	}
	if name == "cast" {
		if len(call.Args) != 1 || strings.TrimSpace(call.TargetKind) == "" {
			return fmt.Errorf("cast requires one argument and a target kind")
		}
		switch strings.ToLower(strings.TrimSpace(call.TargetKind)) {
		case "string", "integer", "decimal", "boolean", "date", "date_time", "code", "uuid":
		default:
			return fmt.Errorf("cast target kind %q is unsupported", call.TargetKind)
		}
	} else if call.TargetKind != "" {
		return fmt.Errorf("target kind is only valid for cast")
	}
	for index, arg := range call.Args {
		if err := validatePhysicalExpression(arg, defined, bindVars); err != nil {
			return fmt.Errorf("call %q argument %d: %w", name, index, err)
		}
	}
	return nil
}

// validatePhysicalExpressionObjectCycles protects the recursive expression
// validator from a malformed in-memory plan containing a cycle of
// PhysicalObject pointers. JSON decoding cannot produce such a cycle, but
// plans are also assembled by compiler stages and tests, where pointers can
// be wired directly. The active/visited split permits shared (DAG) objects
// while rejecting only true recursion.
func validatePhysicalExpressionObjectCycles(expression PhysicalExpression) error {
	active := map[*PhysicalObject]bool{}
	visited := map[*PhysicalObject]bool{}
	var visitExpression func(PhysicalExpression) error
	var visitObject func(*PhysicalObject) error
	var visitPredicate func(*PhysicalPredicateExpression) error
	var visitSubplan func(PhysicalSubplan) error
	visitExpression = func(current PhysicalExpression) error {
		if current.Object != nil {
			if err := visitObject(current.Object); err != nil {
				return err
			}
		}
		if current.Aggregate != nil && current.Aggregate.Value != nil {
			if err := visitExpression(*current.Aggregate.Value); err != nil {
				return err
			}
		}
		if current.Call != nil {
			for index := range current.Call.Args {
				if err := visitExpression(current.Call.Args[index]); err != nil {
					return err
				}
			}
		}
		if current.Aggregate != nil && current.Aggregate.Predicate != nil {
			if err := visitPredicate(current.Aggregate.Predicate); err != nil {
				return err
			}
		}
		if current.Slice != nil {
			if current.Slice.Predicate != nil {
				if err := visitPredicate(current.Slice.Predicate); err != nil {
					return err
				}
			}
			if current.Slice.Sort != nil {
				if err := visitExpression(*current.Slice.Sort); err != nil {
					return err
				}
			}
			for _, projection := range current.Slice.Projections {
				if err := visitExpression(projection.Expression); err != nil {
					return err
				}
			}
		}
		if current.KeyedMap != nil {
			if err := visitExpression(current.KeyedMap.Source); err != nil {
				return err
			}
			if err := visitExpression(current.KeyedMap.ItemKey); err != nil {
				return err
			}
			if err := visitExpression(current.KeyedMap.ItemValue); err != nil {
				return err
			}
			for _, fallback := range current.KeyedMap.ValueFallbacks {
				if err := visitExpression(fallback); err != nil {
					return err
				}
			}
		}
		return nil
	}
	visitPredicate = func(predicate *PhysicalPredicateExpression) error {
		if predicate == nil {
			return nil
		}
		if predicate.Comparison != nil && predicate.Comparison.LeftExpression != nil {
			if err := visitExpression(*predicate.Comparison.LeftExpression); err != nil {
				return err
			}
		}
		for index := range predicate.Children {
			if err := visitPredicate(&predicate.Children[index]); err != nil {
				return err
			}
		}
		if predicate.Exists != nil {
			return visitSubplan(*predicate.Exists)
		}
		return nil
	}
	visitSubplan = func(subplan PhysicalSubplan) error {
		for _, operation := range subplan.Operations {
			switch operation.Kind {
			case PhysicalFilterOp:
				if operation.Filter != nil && operation.Filter.Expression != nil {
					if err := visitPredicate(operation.Filter.Expression); err != nil {
						return err
					}
				}
			case PhysicalSetOp:
				if operation.Set != nil {
					if err := visitSubplan(operation.Set.Subplan); err != nil {
						return err
					}
				}
			case PhysicalUnnestOp:
				if operation.Unnest != nil {
					if err := visitExpression(operation.Unnest.Expression); err != nil {
						return err
					}
				}
			}
		}
		return visitExpression(subplan.Return)
	}
	visitObject = func(object *PhysicalObject) error {
		if active[object] {
			return fmt.Errorf("physical object expression contains a recursive cycle")
		}
		if visited[object] {
			return nil
		}
		active[object] = true
		for _, field := range object.Fields {
			if err := visitExpression(field.Expression); err != nil {
				return err
			}
		}
		delete(active, object)
		visited[object] = true
		return nil
	}
	return visitExpression(expression)
}

func (cardinality PhysicalCardinality) valid() bool {
	return cardinality == PhysicalScalarCardinality || cardinality == PhysicalArrayCardinality || cardinality == PhysicalObjectCardinality
}

func (behavior PhysicalNullBehavior) valid() bool {
	return behavior == PhysicalPreserveNull || behavior == PhysicalOmitNulls || behavior == PhysicalEmptyOnNull
}
