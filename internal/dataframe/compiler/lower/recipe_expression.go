package lower

// This file is the narrow bridge from the persisted, backend-neutral
// expression AST to the physical expression IR. It deliberately performs no
// output-name or resource-specific dispatch: selectors and calls remain data
// all the way to the renderer.

import (
	"fmt"
	"strings"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
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
	case expression.DocumentRefNode:
		if input.Document == nil {
			return ir.PhysicalExpression{}, fmt.Errorf("document reference node is missing document")
		}
		return lowerDocumentRef(*input.Document, behavior), nil
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
		// Semantic recipe plans normally carry a checked expression type, but
		// the narrow public lowering entrypoint also accepts the persisted AST.
		// Recover repetition from generated schema metadata in that case so a
		// repeated FHIR selector cannot silently become a scalar physical value.
		if !input.Type.Valid() {
			if metadata, ok := fhirschema.ResolveTerminalScalarMetadata(resourceType, selector.CanonicalPath()); ok && metadata.Repeated {
				cardinality = ir.PhysicalArrayCardinality
			}
		}
		result.Cardinality = cardinality
		variable := strings.TrimSpace(input.Selector.Context)
		if variable == "" {
			variable = "root"
		}
		result.Kind = ir.PhysicalExtractExpression
		result.Extract = &ir.PhysicalExtract{
			Source:        ir.PhysicalValue{Variable: variable, Path: []string{"payload"}},
			ResourceType:  resourceType,
			Selector:      selector,
			ExecutionMode: selectorExecutionModeForExpression(resourceType, selector, nil),
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

// lowerDocumentRef materializes the storage envelope needed by whole-resource
// consumers while retaining the payload as one nested object. It deliberately
// uses only typed PhysicalValue nodes; no backend query text is embedded here.
func lowerDocumentRef(ref expression.DocumentRef, behavior ir.PhysicalNullBehavior) ir.PhysicalExpression {
	variable := strings.TrimSpace(ref.Context)
	if variable == "" {
		variable = "root"
	}
	value := func(path ...string) ir.PhysicalExpression {
		cardinality := ir.PhysicalScalarCardinality
		if len(path) == 1 && path[0] == "payload" {
			cardinality = ir.PhysicalObjectCardinality
		}
		return ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: cardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{Variable: variable, Path: path}}
	}
	return ir.PhysicalExpression{Kind: ir.PhysicalObjectExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: behavior, Object: &ir.PhysicalObject{Fields: []ir.PhysicalExpressionProjection{
		{Name: "payload", Expression: value("payload")},
		{Name: "id", Expression: value("id")},
		{Name: "resourceType", Expression: value("resourceType")},
		{Name: "key", Expression: value("_key")},
	}}}
}

func nextLiteralBindKey(bindVars map[string]any) string {
	for index := 0; ; index++ {
		key := fmt.Sprintf("recipe_literal_%d", index)
		if _, exists := bindVars[key]; !exists {
			return key
		}
	}
}
