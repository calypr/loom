package ir

import (
	"fmt"

	"strings"
)

func (p PhysicalPlan) Validate() error {
	if p.Version <= 0 {
		return fmt.Errorf("physical plan version must be positive")
	}
	for key := range p.BindVars {
		if !physicalBindKeyPattern.MatchString(key) {
			return fmt.Errorf("unsafe bind key %q", key)
		}
	}
	defined := map[string]bool{}
	rootScans := 0
	returns := 0
	for i, operation := range p.Operations {
		if returns > 0 {
			return fmt.Errorf("operation %d appears after RETURN", i)
		}
		if err := operation.validatePayload(); err != nil {
			return fmt.Errorf("operation %d (%s): %w", i, operation.Kind, err)
		}
		switch operation.Kind {
		case PhysicalRootScanOp:
			rootScans++
			if rootScans > 1 {
				return fmt.Errorf("operation %d: physical plan has multiple root scans", i)
			}
			if err := requireBind(p.BindVars, operation.RootScan.CollectionBindKey); err != nil {
				return fmt.Errorf("operation %d: %w", i, err)
			}
			if err := requireCollectionBind(p.BindVars, operation.RootScan.CollectionBindKey); err != nil {
				return fmt.Errorf("operation %d: %w", i, err)
			}
			if err := definePhysicalVariable(defined, operation.RootScan.Variable); err != nil {
				return fmt.Errorf("operation %d: %w", i, err)
			}
		case PhysicalTraversalOp:
			traversal := operation.Traversal
			if !defined[traversal.SourceVariable] {
				return fmt.Errorf("operation %d: traversal source variable %q is out of scope", i, traversal.SourceVariable)
			}
			if traversal.Direction != PhysicalOutbound && traversal.Direction != PhysicalInbound && traversal.Direction != PhysicalAny {
				return fmt.Errorf("operation %d: invalid traversal direction %q", i, traversal.Direction)
			}
			if traversal.EdgeTargetTypeField != "" && !physicalPathPartPattern.MatchString(traversal.EdgeTargetTypeField) {
				return fmt.Errorf("operation %d: unsafe traversal edge type field %q", i, traversal.EdgeTargetTypeField)
			}
			if err := validatePhysicalTraversalStrategy(*traversal); err != nil {
				return fmt.Errorf("operation %d: %w", i, err)
			}
			for _, key := range []string{traversal.EdgeCollectionBindKey, traversal.EdgeLabelBindKey, traversal.TargetTypeBindKey} {
				if key != "" {
					if err := requireBind(p.BindVars, key); err != nil {
						return fmt.Errorf("operation %d: %w", i, err)
					}
				}
			}
			if traversal.EdgeCollectionBindKey == "" {
				return fmt.Errorf("operation %d: traversal edge collection bind key is required", i)
			}
			if err := requireCollectionBind(p.BindVars, traversal.EdgeCollectionBindKey); err != nil {
				return fmt.Errorf("operation %d: %w", i, err)
			}
			if err := definePhysicalVariable(defined, traversal.TargetVariable); err != nil {
				return fmt.Errorf("operation %d: %w", i, err)
			}
			if traversal.EdgeVariable != "" {
				if err := definePhysicalVariable(defined, traversal.EdgeVariable); err != nil {
					return fmt.Errorf("operation %d: %w", i, err)
				}
			}
		case PhysicalFilterOp:
			if err := validatePhysicalFilter(*operation.Filter, defined, p.BindVars); err != nil {
				return fmt.Errorf("operation %d: %w", i, err)
			}
		case PhysicalDerivedLetOp:
			derived := operation.DerivedLet
			if err := validatePhysicalDerivedLet(*derived, defined, p.BindVars); err != nil {
				return fmt.Errorf("operation %d: %w", i, err)
			}
			if err := definePhysicalVariable(defined, derived.Variable); err != nil {
				return fmt.Errorf("operation %d: %w", i, err)
			}
		case PhysicalExpressionLetOp:
			if operation.ExpressionLet == nil || strings.TrimSpace(operation.ExpressionLet.Variable) == "" {
				return fmt.Errorf("operation %d: expression LET payload and variable are required", i)
			}
			if err := validatePhysicalExpression(operation.ExpressionLet.Expression, defined, p.BindVars); err != nil {
				return fmt.Errorf("operation %d expression LET: %w", i, err)
			}
			if err := definePhysicalVariable(defined, operation.ExpressionLet.Variable); err != nil {
				return fmt.Errorf("operation %d: %w", i, err)
			}
		case PhysicalSetOp:
			set := operation.Set
			if err := validatePhysicalSet(*set, defined, p.BindVars); err != nil {
				return fmt.Errorf("operation %d set %q: %w", i, set.Variable, err)
			}
			if err := definePhysicalVariable(defined, set.Variable); err != nil {
				return fmt.Errorf("operation %d: %w", i, err)
			}
			if set.Prepared != nil {
				if err := definePhysicalVariable(defined, set.Prepared.Variable); err != nil {
					return fmt.Errorf("operation %d prepared set: %w", i, err)
				}
			}
		case PhysicalUnnestOp:
			if err := validatePhysicalUnnest(*operation.Unnest, defined, p.BindVars); err != nil {
				return fmt.Errorf("operation %d unnest: %w", i, err)
			}
			if err := definePhysicalVariable(defined, operation.Unnest.OutputVariable); err != nil {
				return fmt.Errorf("operation %d: %w", i, err)
			}
			if operation.Unnest.Ordinality != "" {
				if err := definePhysicalVariable(defined, operation.Unnest.Ordinality); err != nil {
					return fmt.Errorf("operation %d: %w", i, err)
				}
			}
		case PhysicalSortOp:
			if err := validatePhysicalValue(operation.Sort.Value, defined, p.BindVars); err != nil {
				return fmt.Errorf("operation %d: %w", i, err)
			}
		case PhysicalLimitOp:
			if err := requireBind(p.BindVars, operation.Limit.BindKey); err != nil {
				return fmt.Errorf("operation %d: %w", i, err)
			}
			limit, ok := p.BindVars[operation.Limit.BindKey].(int)
			if !ok || limit <= 0 {
				return fmt.Errorf("operation %d: limit bind %q must be a positive int", i, operation.Limit.BindKey)
			}
		case PhysicalReturnOp:
			returns++
			seenNames := map[string]bool{}
			for _, projection := range operation.Return.Projections {
				if strings.TrimSpace(projection.Name) == "" || seenNames[projection.Name] {
					return fmt.Errorf("operation %d: return projection name %q is empty or duplicated", i, projection.Name)
				}
				seenNames[projection.Name] = true
				if err := validatePhysicalProjection(projection, defined, p.BindVars); err != nil {
					return fmt.Errorf("operation %d projection %q: %w", i, projection.Name, err)
				}
			}
		}
	}
	if rootScans != 1 {
		return fmt.Errorf("physical plan requires exactly one root scan")
	}
	if returns != 1 {
		return fmt.Errorf("physical plan requires exactly one RETURN")
	}
	return nil
}

func validatePhysicalTraversalStrategy(traversal PhysicalTraversal) error {
	strategy := traversal.Strategy
	if strategy == "" || strategy == PhysicalTraversalNative {
		return nil
	}
	if strategy != PhysicalTraversalEndpointLookup {
		return fmt.Errorf("unsupported traversal strategy %q", strategy)
	}
	if traversal.Direction != PhysicalInbound && traversal.Direction != PhysicalOutbound {
		return fmt.Errorf("endpoint lookup requires INBOUND or OUTBOUND direction")
	}
	if !physicalPathPartPattern.MatchString(traversal.EndpointField) || !physicalPathPartPattern.MatchString(traversal.EndpointJoinField) {
		return fmt.Errorf("endpoint lookup requires safe endpoint and join fields")
	}
	if len(traversal.EndpointIndexFields) == 0 {
		return fmt.Errorf("endpoint lookup requires declared compound index fields")
	}
	for _, field := range traversal.EndpointIndexFields {
		if !physicalPathPartPattern.MatchString(field) {
			return fmt.Errorf("endpoint lookup has unsafe index field %q", field)
		}
	}
	return nil
}

func validatePhysicalSet(set PhysicalSet, parent map[string]bool, bindVars map[string]any) error {
	if set.Projection != nil {
		if len(set.Projection.Fields) == 0 {
			return fmt.Errorf("set %q projection requires at least one field", set.Variable)
		}
		seenProjectionFields := map[string]bool{}
		for _, field := range set.Projection.Fields {
			if !physicalVariablePattern.MatchString(field.Name) || seenProjectionFields[field.Name] {
				return fmt.Errorf("set %q projection field %q is unsafe or duplicated", set.Variable, field.Name)
			}
			seenProjectionFields[field.Name] = true
			if !schemaDefinitionExists(field.ResourceType) {
				return fmt.Errorf("set %q projection field %q has invalid resource type %q", set.Variable, field.Name, field.ResourceType)
			}
			if err := validatePhysicalSelector(field.ResourceType, field.Selector); err != nil {
				return fmt.Errorf("set %q projection field %q selector: %w", set.Variable, field.Name, err)
			}
		}
	}
	if set.Output != nil {
		if len(set.Output.Fields) == 0 {
			return fmt.Errorf("set %q compact output requires at least one retained field", set.Variable)
		}
		seenOutputFields := map[PhysicalSetOutputField]bool{}
		for _, field := range set.Output.Fields {
			switch field {
			case PhysicalSetGraphIDField, PhysicalSetKeyField, PhysicalSetIDField, PhysicalSetResourceTypeField, PhysicalSetPayloadField:
			default:
				return fmt.Errorf("set %q compact output field %q is unsupported", set.Variable, field)
			}
			if seenOutputFields[field] {
				return fmt.Errorf("set %q compact output field %q is duplicated", set.Variable, field)
			}
			seenOutputFields[field] = true
		}
		if !seenOutputFields[PhysicalSetGraphIDField] || !seenOutputFields[PhysicalSetKeyField] {
			return fmt.Errorf("set %q compact output must retain _id and _key", set.Variable)
		}
	}
	if set.Prepared != nil {
		prepared := set.Prepared
		if !physicalVariablePattern.MatchString(prepared.Variable) || !physicalVariablePattern.MatchString(prepared.SourceSetVariable) {
			return fmt.Errorf("prepared set variables must be safe")
		}
		if prepared.SourceSetVariable != set.Variable {
			return fmt.Errorf("prepared set source %q must equal owning set %q", prepared.SourceSetVariable, set.Variable)
		}
		if len(prepared.Fields) == 0 {
			return fmt.Errorf("prepared set %q requires at least one field", prepared.Variable)
		}
		seen := map[string]bool{}
		for _, field := range prepared.Fields {
			if !physicalVariablePattern.MatchString(field.Name) || seen[field.Name] {
				return fmt.Errorf("prepared set field %q is unsafe or duplicated", field.Name)
			}
			seen[field.Name] = true
			if !schemaDefinitionExists(field.ResourceType) {
				return fmt.Errorf("prepared set field %q has invalid resource type %q", field.Name, field.ResourceType)
			}
			if err := validatePhysicalSelector(field.ResourceType, field.Selector); err != nil {
				return fmt.Errorf("prepared set field %q selector: %w", field.Name, err)
			}
		}
	}
	if set.SourceSetVariable == "" {
		return validatePhysicalSubplan(set.Subplan, parent, bindVars)
	}
	if !physicalVariablePattern.MatchString(set.ItemVariable) {
		return fmt.Errorf("shared subset %q has unsafe item variable", set.ItemVariable)
	}
	if !parent[set.SourceSetVariable] {
		return fmt.Errorf("shared subset source %q is out of scope", set.SourceSetVariable)
	}
	if len(set.Subplan.Captures) != 1 || set.Subplan.Captures[0] != set.SourceSetVariable {
		return fmt.Errorf("shared subset %q must capture exactly its source set", set.Variable)
	}
	defined := map[string]bool{set.SourceSetVariable: true, set.ItemVariable: true}
	for index, operation := range set.Subplan.Operations {
		if operation.Kind != PhysicalFilterOp && operation.Kind != PhysicalDerivedLetOp {
			return fmt.Errorf("shared subset operation %d has unsupported kind %q", index, operation.Kind)
		}
		if operation.Kind == PhysicalFilterOp {
			if err := validatePhysicalFilter(*operation.Filter, defined, bindVars); err != nil {
				return err
			}
		} else {
			if err := validatePhysicalDerivedLet(*operation.DerivedLet, defined, bindVars); err != nil {
				return err
			}
			if err := definePhysicalVariable(defined, operation.DerivedLet.Variable); err != nil {
				return err
			}
		}
	}
	return validatePhysicalExpression(set.Subplan.Return, defined, bindVars)
}

func validatePhysicalUnnest(unnest PhysicalUnnest, defined map[string]bool, bindVars map[string]any) error {
	if !physicalVariablePattern.MatchString(unnest.InputVariable) {
		return fmt.Errorf("unnest input variable %q is unsafe", unnest.InputVariable)
	}
	if !defined[unnest.InputVariable] {
		return fmt.Errorf("unnest input variable %q is out of scope", unnest.InputVariable)
	}
	if !physicalVariablePattern.MatchString(unnest.OutputVariable) {
		return fmt.Errorf("unnest output variable %q is unsafe", unnest.OutputVariable)
	}
	if unnest.OutputVariable == unnest.InputVariable {
		return fmt.Errorf("unnest output variable %q shadows its input variable", unnest.OutputVariable)
	}
	if unnest.Ordinality != "" {
		if !physicalVariablePattern.MatchString(unnest.Ordinality) {
			return fmt.Errorf("unnest ordinality variable %q is unsafe", unnest.Ordinality)
		}
		if unnest.Ordinality == unnest.InputVariable || unnest.Ordinality == unnest.OutputVariable {
			return fmt.Errorf("unnest ordinality variable %q shadows an existing unnest binding", unnest.Ordinality)
		}
	}
	switch unnest.JoinMode {
	case PhysicalUnnestInner, PhysicalUnnestOuter:
	default:
		return fmt.Errorf("unsupported unnest join mode %q", unnest.JoinMode)
	}
	if unnest.Expression.Cardinality != PhysicalArrayCardinality {
		return fmt.Errorf("unnest source expression must be array-valued, got %q", unnest.Expression.Cardinality)
	}
	if err := validatePhysicalExpression(unnest.Expression, defined, bindVars); err != nil {
		return fmt.Errorf("unnest source expression: %w", err)
	}
	return nil
}

func (operation PhysicalOperation) validatePayload() error {
	payloads := 0
	if operation.RootScan != nil {
		payloads++
	}
	if operation.Traversal != nil {
		payloads++
	}
	if operation.Filter != nil {
		payloads++
	}
	if operation.DerivedLet != nil {
		payloads++
	}
	if operation.ExpressionLet != nil {
		payloads++
	}
	if operation.Set != nil {
		payloads++
	}
	if operation.Unnest != nil {
		payloads++
	}
	if operation.Sort != nil {
		payloads++
	}
	if operation.Limit != nil {
		payloads++
	}
	if operation.Return != nil {
		payloads++
	}
	if payloads != 1 {
		return fmt.Errorf("operation must contain exactly one payload")
	}
	valid := (operation.Kind == PhysicalRootScanOp && operation.RootScan != nil) ||
		(operation.Kind == PhysicalTraversalOp && operation.Traversal != nil) ||
		(operation.Kind == PhysicalFilterOp && operation.Filter != nil) ||
		(operation.Kind == PhysicalDerivedLetOp && operation.DerivedLet != nil) ||
		(operation.Kind == PhysicalExpressionLetOp && operation.ExpressionLet != nil) ||
		(operation.Kind == PhysicalSetOp && operation.Set != nil) ||
		(operation.Kind == PhysicalUnnestOp && operation.Unnest != nil) ||
		(operation.Kind == PhysicalSortOp && operation.Sort != nil) ||
		(operation.Kind == PhysicalLimitOp && operation.Limit != nil) ||
		(operation.Kind == PhysicalReturnOp && operation.Return != nil)
	if !valid {
		return fmt.Errorf("payload does not match operation kind")
	}
	return nil
}

func definePhysicalVariable(defined map[string]bool, variable string) error {
	if !physicalVariablePattern.MatchString(variable) {
		return fmt.Errorf("unsafe variable name %q", variable)
	}
	if defined[variable] {
		return fmt.Errorf("variable %q is already defined", variable)
	}
	defined[variable] = true
	return nil
}

func requireBind(bindVars map[string]any, key string) error {
	if !physicalBindKeyPattern.MatchString(key) {
		return fmt.Errorf("unsafe bind key %q", key)
	}
	if _, ok := bindVars[key]; !ok {
		return fmt.Errorf("bind key %q is not defined", key)
	}
	return nil
}

func requireCollectionBind(bindVars map[string]any, key string) error {
	value, ok := bindVars[key]
	if !ok {
		return fmt.Errorf("bind key %q is not defined", key)
	}
	collection, ok := value.(string)
	if !ok || strings.TrimSpace(collection) == "" {
		return fmt.Errorf("collection bind key %q must have a non-empty string value", key)
	}
	return nil
}

func validatePhysicalPredicate(predicate PhysicalPredicate, defined map[string]bool, bindVars map[string]any) error {
	operator := strings.ToUpper(strings.TrimSpace(predicate.Operator))
	switch operator {
	case "EQUALS", "NOT_EQUALS", "IN", "EXISTS", "MISSING", "CONTAINS_TEXT", "GT", "GTE", "LT", "LTE":
	default:
		return fmt.Errorf("unknown physical filter operator %q", predicate.Operator)
	}
	hasLeftValue := predicate.Left.Variable != "" || predicate.Left.BindKey != "" || len(predicate.Left.Path) != 0
	hasLeftExpression := predicate.LeftExpression != nil
	if hasLeftValue == hasLeftExpression {
		return fmt.Errorf("physical filter predicate requires exactly one left value or expression")
	}
	if hasLeftExpression {
		if err := validatePhysicalExpression(*predicate.LeftExpression, defined, bindVars); err != nil {
			return fmt.Errorf("physical filter predicate left expression: %w", err)
		}
		if predicate.LeftExpression.Cardinality != PhysicalArrayCardinality {
			return fmt.Errorf("physical filter predicate left expression must be array-valued")
		}
		if !predicate.ValueKind.Valid() {
			return fmt.Errorf("physical filter predicate value kind %q is invalid", predicate.ValueKind)
		}
		if predicate.Quantifier != "" && !predicate.Quantifier.Valid() {
			return fmt.Errorf("physical filter predicate quantifier %q is invalid", predicate.Quantifier)
		}
	} else if err := validatePhysicalValue(predicate.Left, defined, bindVars); err != nil {
		return err
	}
	requiresRight := operator != "EXISTS" && operator != "MISSING"
	if requiresRight != (predicate.Right != nil) {
		return fmt.Errorf("physical filter operator %s right value presence is invalid", operator)
	}
	if predicate.Right != nil {
		if err := validatePhysicalValue(*predicate.Right, defined, bindVars); err != nil {
			return err
		}
	}
	return nil
}

func validatePhysicalFilter(filter PhysicalFilter, defined map[string]bool, bindVars map[string]any) error {
	legacy := strings.TrimSpace(filter.Predicate.Operator) != ""
	rich := filter.Expression != nil
	if legacy == rich {
		return fmt.Errorf("filter requires exactly one legacy predicate or predicate expression")
	}
	if legacy {
		return validatePhysicalPredicate(filter.Predicate, defined, bindVars)
	}
	return validatePhysicalPredicateExpression(*filter.Expression, defined, bindVars)
}

func validatePhysicalDerivedLet(derived PhysicalDerivedLet, defined map[string]bool, bindVars map[string]any) error {
	legacy := strings.TrimSpace(derived.Operator) != "" || len(derived.Inputs) != 0
	rich := derived.Expression != nil
	if legacy == rich {
		return fmt.Errorf("derived LET requires exactly one legacy operation or expression")
	}
	if rich {
		return validatePhysicalExpression(*derived.Expression, defined, bindVars)
	}
	if strings.TrimSpace(derived.Operator) == "" {
		return fmt.Errorf("derived LET operator is required")
	}
	for _, input := range derived.Inputs {
		if err := validatePhysicalValue(input, defined, bindVars); err != nil {
			return err
		}
	}
	return nil
}

func validatePhysicalProjection(projection PhysicalProjection, defined map[string]bool, bindVars map[string]any) error {
	hasValue := projection.Value.Variable != "" || projection.Value.BindKey != "" || len(projection.Value.Path) != 0
	hasExpression := projection.Expression != nil
	if hasValue == hasExpression {
		return fmt.Errorf("projection requires exactly one value or expression")
	}
	if hasExpression {
		return validatePhysicalExpression(*projection.Expression, defined, bindVars)
	}
	return validatePhysicalValue(projection.Value, defined, bindVars)
}
