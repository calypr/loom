package lower

// This file is the narrow bridge from the persisted, backend-neutral
// expression AST to the physical expression IR. It deliberately performs no
// output-name or resource-specific dispatch: selectors and calls remain data
// all the way to the renderer.

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/spec"
)

// LowerRecipeExpression lowers one checked recipe expression into the typed
// physical tree. bindVars receives literal values using deterministic,
// collision-safe keys. resourceType is used to retain schema provenance for
// selector validation; it is not used to select implementation behavior.
func LowerRecipeExpression(input expression.Expression, bindVars map[string]any, resourceType string) (ir.PhysicalExpression, error) {
	if bindVars == nil {
		return ir.PhysicalExpression{}, fmt.Errorf("bind vars map is required")
	}
	return lowerRecipeExpression(input, bindVars, resourceType)
}

func lowerRecipeExpression(input expression.Expression, bindVars map[string]any, resourceType string) (ir.PhysicalExpression, error) {
	cardinality := ir.PhysicalScalarCardinality
	behavior := ir.PhysicalPreserveNull
	if input.Type.Valid() {
		if input.Type.Cardinality == expression.Many {
			cardinality = ir.PhysicalArrayCardinality
		}
	}
	switch input.NullBehavior {
	case expression.NullEmpty:
		behavior = ir.PhysicalEmptyOnNull
	case expression.NullError:
		// Physical execution has no error-valued AQL expression. Preserve null
		// here and let the semantic checker reject error-sensitive recipes until
		// an explicit error channel is added to the execution contract.
		behavior = ir.PhysicalPreserveNull
	}
	result := ir.PhysicalExpression{Cardinality: cardinality, NullBehavior: behavior}
	switch input.Kind {
	case expression.SelectorNode:
		if input.Selector == nil {
			return ir.PhysicalExpression{}, fmt.Errorf("selector node is missing selector")
		}
		selectorPath := strings.TrimSpace(input.Selector.Path)
		if err := input.Selector.Validate(); err != nil {
			return ir.PhysicalExpression{}, err
		}
		selector, err := spec.ParseSelector(selectorPath)
		if err != nil {
			return ir.PhysicalExpression{}, fmt.Errorf("parse selector %q: %w", selectorPath, err)
		}
		variable := strings.TrimSpace(input.Selector.Context)
		if variable == "" {
			variable = "root"
		}
		result.Kind = ir.PhysicalExtractExpression
		result.Extract = &ir.PhysicalExtract{
			Source:        ir.PhysicalValue{Variable: variable, Path: []string{"payload"}},
			ResourceType:  resourceType,
			Selector:      selector,
			ExecutionMode: ir.PhysicalSelectorGeneric,
		}
		return result, nil
	case expression.LiteralNode:
		if input.Literal == nil {
			return ir.PhysicalExpression{}, fmt.Errorf("literal node is missing literal")
		}
		key := nextLiteralBindKey(bindVars)
		bindVars[key] = input.Literal.Value
		result.Kind = ir.PhysicalLiteralExpression
		result.Literal = &ir.PhysicalLiteral{BindKey: key}
		return result, nil
	case expression.CallNode:
		if input.Call == nil {
			return ir.PhysicalExpression{}, fmt.Errorf("call node is missing call")
		}
		call := &ir.PhysicalCall{Name: strings.ToLower(strings.TrimSpace(input.Call.Name))}
		if input.Call.Target != nil {
			call.TargetKind = string(input.Call.Target.Kind)
		}
		for index, arg := range input.Call.Args {
			// Recipe wire syntax represents cast as (value, target literal),
			// while the typed AST stores target in Call.Target. The target is
			// metadata, not a runtime physical argument.
			if call.Name == "cast" && input.Call.Target != nil && index == 1 {
				continue
			}
			lowered, err := lowerRecipeExpression(arg, bindVars, resourceType)
			if err != nil {
				return ir.PhysicalExpression{}, fmt.Errorf("call %q argument %d: %w", input.Call.Name, index, err)
			}
			call.Args = append(call.Args, lowered)
		}
		result.Kind = ir.PhysicalCallExpression
		result.Call = call
		return result, nil
	default:
		return ir.PhysicalExpression{}, fmt.Errorf("unsupported expression node %q", input.Kind)
	}
}

func nextLiteralBindKey(bindVars map[string]any) string {
	for index := 0; ; index++ {
		key := fmt.Sprintf("recipe_literal_%d", index)
		if _, exists := bindVars[key]; !exists {
			return key
		}
	}
}
