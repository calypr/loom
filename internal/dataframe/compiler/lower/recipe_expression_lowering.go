package lower

// This file contains the canonical recipe lowering boundary. Persisted
// recipes are a frontend: after resolution each output is lowered to the same
// ir.PhysicalPlan used by the GraphQL dataframe compiler.

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

// recipeFieldProjectionLowerer lowers rich recipe expressions at the generic
// plan boundary, where the traversal walk has established the physical value
// for every lexical alias. Selector-bearing fields deliberately delegate to
// the existing selector lowerer so projection modes, fallbacks, and prepared
// selector behavior remain unchanged. Owner-relative siblings of a nested row
// expansion bind through the exact captured repeated ancestor.
func recipeFieldProjectionLowerer(output semantic.OutputPlan) semanticFieldProjectionLowerer {
	contexts := recipeExpressionContexts(output)
	return func(physical *ir.PhysicalPlan, node semantic.SemanticNode, index int, field semantic.SemanticField, source ir.PhysicalValue, bindings map[string]physicalSemanticBinding) (ir.PhysicalProjection, error) {
		if field.Expr.Expression.Selector != nil {
			selector := field.Expr.Expression.Selector
			context := strings.TrimSpace(selector.Context)
			if context == "" {
				context = "root"
			}
			if expansion := recipeRowExpansion(output); expansion != nil && context == expansion.ItemBinding {
				binding, ok := bindings[context]
				if !ok {
					return ir.PhysicalProjection{}, fmt.Errorf("field %q expansion item context %q is not in physical scope", field.Name, context)
				}
				itemNode := semantic.SemanticNode{Alias: context, ResourceType: binding.ResourceType}
				projection, err := lowerSemanticFieldProjection(physical, itemNode, index, field, binding.Source, bindings, nil)
				if err != nil {
					return ir.PhysicalProjection{}, err
				}
				if err := bindRecipeFallbackSelectors(output, itemNode, field, binding.Source, bindings, contexts, projection.Expression); err != nil {
					return ir.PhysicalProjection{}, err
				}
				return projection, nil
			}
			if output.RowExpansion != nil {
				if ancestor, relative, resourceType, ok := recipeExpandedAncestorProjection(output.RowExpansion, context, selector.Path); ok {
					projection, err := lowerSemanticFieldProjection(physical, node, index, field, source, bindings, nil)
					if err != nil {
						return ir.PhysicalProjection{}, err
					}
					if projection.Expression == nil || projection.Expression.Extract == nil {
						return ir.PhysicalProjection{}, fmt.Errorf("field %q ancestor selector did not lower to an extract", field.Name)
					}
					extract := projection.Expression.Extract
					extract.Source = ir.PhysicalValue{Variable: ancestor.Variable}
					extract.ResourceType = resourceType
					extract.Selector = relative
					if len(extract.Fallbacks) == 0 {
						extract.ExecutionMode = selectorExecutionModeForExpression(resourceType, relative, nil)
					}
					if err := bindRecipeFallbackSelectors(output, node, field, source, bindings, contexts, projection.Expression); err != nil {
						return ir.PhysicalProjection{}, err
					}
					return projection, nil
				}
			}
			projection, err := lowerSemanticFieldProjection(physical, node, index, field, source, bindings, nil)
			if err != nil {
				return ir.PhysicalProjection{}, err
			}
			if err := bindRecipeFallbackSelectors(output, node, field, source, bindings, contexts, projection.Expression); err != nil {
				return ir.PhysicalProjection{}, err
			}
			return projection, nil
		}
		lowered, err := lowerRecipeExpressionScoped(field.Expr.Expression, physical.BindVars, output.RootResourceType, contexts)
		if err != nil {
			return ir.PhysicalProjection{}, fmt.Errorf("field %q expression: %w", field.Name, err)
		}
		for alias, binding := range bindings {
			rewriteRecipeExpressionBinding(&lowered, alias, binding.Source)
		}
		return ir.PhysicalProjection{Name: field.Name, Expression: &lowered}, nil
	}
}

func bindRecipeFallbackSelectors(
	output semantic.OutputPlan,
	node semantic.SemanticNode,
	field semantic.SemanticField,
	nodeSource ir.PhysicalValue,
	bindings map[string]physicalSemanticBinding,
	contexts map[string]string,
	expression *ir.PhysicalExpression,
) error {
	if expression == nil || expression.Extract == nil {
		return nil
	}
	extract := expression.Extract
	if len(extract.Fallbacks) != len(field.Fallbacks) {
		return fmt.Errorf("field %q lowered %d fallbacks for %d semantic alternatives", field.Name, len(extract.Fallbacks), len(field.Fallbacks))
	}
	for index, semanticFallback := range field.Fallbacks {
		selectorExpression := semanticFallback.Expression.Selector
		if selectorExpression == nil {
			return fmt.Errorf("field %q fallback %d is not a selector", field.Name, index)
		}
		selector, err := spec.ParseSelector(selectorExpression.Path)
		if err != nil {
			return fmt.Errorf("field %q fallback %d selector: %w", field.Name, index, err)
		}
		context := strings.TrimSpace(semanticFallback.Context)
		if context == "" {
			context = strings.TrimSpace(selectorExpression.Context)
		}
		if context == "" {
			context = "root"
		}
		resourceType, ok := contexts[context]
		if !ok {
			return fmt.Errorf("field %q fallback %d context %q is not in physical scope", field.Name, index, context)
		}
		fallbackSource := nodeSource
		if context != node.Alias {
			binding, exists := bindings[context]
			if !exists {
				return fmt.Errorf("field %q fallback %d context %q is not in physical scope", field.Name, index, context)
			}
			fallbackSource = binding.Source
		}
		if output.RowExpansion != nil {
			if binding, relative, ok := recipeExpandedItemSelector(output.RowExpansion, context, selector, bindings); ok {
				fallbackSource = binding.Source
				resourceType = binding.ResourceType
				selector = relative
			} else if ancestor, relative, ancestorResourceType, ok := recipeExpandedAncestorSelector(output.RowExpansion, context, selector); ok {
				fallbackSource = ir.PhysicalValue{Variable: ancestor.Variable}
				resourceType = ancestorResourceType
				selector = relative
			}
		}
		extract.Fallbacks[index] = ir.PhysicalSelectorFallback{
			Source: fallbackSource, ResourceType: resourceType, Selector: selector,
		}
	}
	if len(extract.Fallbacks) != 0 {
		extract.ExecutionMode = ir.PhysicalSelectorGeneric
	}
	return nil
}

func recipeExpandedItemSelector(
	expansion *semantic.SemanticRowExpansion,
	context string,
	selector spec.Selector,
	bindings map[string]physicalSemanticBinding,
) (physicalSemanticBinding, spec.Selector, bool) {
	if expansion == nil || context != expansion.Owner.Alias || expansion.Source.Expression.Selector == nil {
		return physicalSemanticBinding{}, spec.Selector{}, false
	}
	expansionSelector, err := spec.ParseSelector(expansion.Source.Expression.Selector.Path)
	if err != nil {
		return physicalSemanticBinding{}, spec.Selector{}, false
	}
	expansionPath := expansionSelector.CanonicalPath()
	selectorPath := selector.CanonicalPath()
	prefix := expansionPath + "."
	if !strings.HasPrefix(selectorPath, prefix) {
		return physicalSemanticBinding{}, spec.Selector{}, false
	}
	binding, ok := bindings[expansion.ItemBinding]
	if !ok {
		return physicalSemanticBinding{}, spec.Selector{}, false
	}
	relative, err := spec.ParseSelector(strings.TrimPrefix(selectorPath, prefix))
	if err != nil {
		return physicalSemanticBinding{}, spec.Selector{}, false
	}
	return binding, relative, true
}

func recipeExpandedAncestorProjection(expansion *semantic.SemanticRowExpansion, context, fieldPath string) (ir.PhysicalUnnestAncestor, spec.Selector, string, bool) {
	projectionSelector, err := spec.ParseSelector(fieldPath)
	if err != nil {
		return ir.PhysicalUnnestAncestor{}, spec.Selector{}, "", false
	}
	return recipeExpandedAncestorSelector(expansion, context, projectionSelector)
}

// recipeExpandedAncestorSelector finds the nearest repeated item shared by an
// expanded source and an owner-relative sibling selector, then returns the
// sibling suffix relative to that captured item.
func recipeExpandedAncestorSelector(expansion *semantic.SemanticRowExpansion, context string, projectionSelector spec.Selector) (ir.PhysicalUnnestAncestor, spec.Selector, string, bool) {
	if expansion == nil || context != expansion.Owner.Alias || expansion.Source.Expression.Selector == nil {
		return ir.PhysicalUnnestAncestor{}, spec.Selector{}, "", false
	}
	expansionSelector, err := spec.ParseSelector(expansion.Source.Expression.Selector.Path)
	if err != nil || len(expansionSelector.Steps) < 2 {
		return ir.PhysicalUnnestAncestor{}, spec.Selector{}, "", false
	}
	projectionPath := projectionSelector.CanonicalPath()
	expansionPath := expansionSelector.CanonicalPath()
	if projectionPath == expansionPath || strings.HasPrefix(projectionPath, expansionPath+".") {
		return ir.PhysicalUnnestAncestor{}, spec.Selector{}, "", false
	}
	bestStepIndex := -1
	bestAncestorOrdinal := 0
	bestPrefixLength := 0
	var ancestorType string
	for stepIndex, step := range expansionSelector.Steps[:len(expansionSelector.Steps)-1] {
		if !step.Iterate {
			continue
		}
		prefix := (spec.Selector{Steps: expansionSelector.Steps[:stepIndex+1]}).CanonicalPath()
		if !strings.HasPrefix(projectionPath, prefix+".") || len(prefix) <= bestPrefixLength {
			continue
		}
		semantics, ok := fhirschema.ResolveFieldSemantics(expansion.Owner.ResourceType, prefix)
		if !ok || semantics.Kind != fhirschema.FieldKindArray || semantics.Reference == "" {
			continue
		}
		ancestorOrdinal := 0
		for _, candidate := range expansionSelector.Steps[:stepIndex] {
			if candidate.Iterate {
				ancestorOrdinal++
			}
		}
		bestStepIndex = stepIndex
		bestAncestorOrdinal = ancestorOrdinal
		bestPrefixLength = len(prefix)
		ancestorType = semantics.Reference
	}
	if bestPrefixLength == 0 {
		return ir.PhysicalUnnestAncestor{}, spec.Selector{}, "", false
	}
	relative := spec.Selector{
		Steps:  append([]spec.SelectorStep(nil), projectionSelector.Steps[bestStepIndex+1:]...),
		Filter: projectionSelector.Filter,
	}
	if len(relative.Steps) == 0 {
		return ir.PhysicalUnnestAncestor{}, spec.Selector{}, "", false
	}
	return ir.PhysicalUnnestAncestor{StepIndex: bestStepIndex, Variable: fmt.Sprintf("__loom_expansion_ancestor_%d", bestAncestorOrdinal)}, relative, ancestorType, true
}

func recipeSetVariables(plan ir.PhysicalPlan) map[string]string {
	variables := map[string]string{"root": "root"}
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalSetOp && operation.Set != nil && operation.Source.SemanticNode != "" {
			variables[operation.Source.SemanticNode] = operation.Set.Variable
		}
	}
	return variables
}

func insertPhysicalExpressionLet(plan *ir.PhysicalPlan, returnIndex int, variable string, expression ir.PhysicalExpression) {
	operation := ir.PhysicalOperation{Kind: ir.PhysicalExpressionLetOp, Source: ir.PhysicalSource{SemanticField: "shared_family"}, ExpressionLet: &ir.PhysicalExpressionLet{Variable: variable, Expression: expression}}
	plan.Operations = append(plan.Operations, ir.PhysicalOperation{})
	copy(plan.Operations[returnIndex+1:], plan.Operations[returnIndex:])
	plan.Operations[returnIndex] = operation
}

func dynamicFamilyVariable(plan ir.PhysicalPlan, name string) string {
	base := "__loom_family_" + sanitizeColumnName(name)
	if base == "__loom_family_" {
		base = "__loom_family_dynamic"
	}
	used := map[string]bool{}
	for _, operation := range plan.Operations {
		if operation.ExpressionLet != nil {
			used[operation.ExpressionLet.Variable] = true
		}
		if operation.Set != nil {
			used[operation.Set.Variable] = true
		}
	}
	if !used[base] {
		return base
	}
	for index := 1; ; index++ {
		candidate := fmt.Sprintf("%s_%d", base, index)
		if !used[candidate] {
			return candidate
		}
	}
}

func rewriteRecipeExpressionVariable(value *ir.PhysicalExpression, from, to string) {
	if value == nil {
		return
	}
	if value.Value != nil && value.Value.Variable == from {
		value.Value.Variable = to
	}
	if value.Extract != nil {
		if value.Extract.Source.Variable == from {
			value.Extract.Source.Variable = to
		}
		for index := range value.Extract.Fallbacks {
			if value.Extract.Fallbacks[index].Source.Variable == from {
				value.Extract.Fallbacks[index].Source.Variable = to
			}
		}
	}
	if value.Call != nil {
		for index := range value.Call.Args {
			rewriteRecipeExpressionVariable(&value.Call.Args[index], from, to)
		}
	}
	if value.Object != nil {
		for index := range value.Object.Fields {
			rewriteRecipeExpressionVariable(&value.Object.Fields[index].Expression, from, to)
		}
	}
}

// rewriteRecipeExpressionBinding applies a physical lexical binding to all
// value-bearing nodes. Extracts also inherit a payload path when the binding
// is a traversed document variable (as opposed to a materialized child set,
// whose renderer intentionally handles set items through item.payload).
func rewriteRecipeExpressionBinding(value *ir.PhysicalExpression, from string, source ir.PhysicalValue) {
	if value == nil {
		return
	}
	if value.Value != nil && value.Value.Variable == from {
		value.Value.Variable = source.Variable
	}
	if value.Extract != nil {
		if value.Extract.Source.Variable == from {
			value.Extract.Source.Variable = source.Variable
			if len(source.Path) != 0 {
				value.Extract.Source.Path = append([]string(nil), source.Path...)
			}
		}
		for index := range value.Extract.Fallbacks {
			fallback := &value.Extract.Fallbacks[index]
			if fallback.Source.Variable == from {
				fallback.Source.Variable = source.Variable
				if len(source.Path) != 0 {
					fallback.Source.Path = append([]string(nil), source.Path...)
				}
			}
		}
	}
	if value.Call != nil {
		for index := range value.Call.Args {
			rewriteRecipeExpressionBinding(&value.Call.Args[index], from, source)
		}
	}
	if value.Object != nil {
		for index := range value.Object.Fields {
			rewriteRecipeExpressionBinding(&value.Object.Fields[index].Expression, from, source)
		}
	}
}

func recipeExpressionContexts(output semantic.OutputPlan) map[string]string {
	contexts := map[string]string{"root": output.RootResourceType}
	var walk func(semantic.SemanticNode)
	walk = func(node semantic.SemanticNode) {
		for _, child := range node.Children {
			contexts[child.Alias] = child.ResourceType
			walk(child)
		}
	}
	walk(output.Root)
	if expansion := recipeRowExpansion(output); expansion != nil && expansion.Source.Expression.Selector != nil {
		selector := expansion.Source.Expression.Selector
		path := strings.TrimSuffix(strings.TrimPrefix(selector.Path, "."), "[]")
		if semantics, ok := fhirschema.ResolveFieldSemantics(expansion.Owner.ResourceType, path+"[]"); ok && semantics.Reference != "" {
			contexts[expansion.ItemBinding] = semantics.Reference
		}
	}
	return contexts
}

// lowerRecipeExpressionScoped is the recipe-expression counterpart to the
// generic lowerer's selector classifier. A recipe expression may combine
// selectors from several lexical bindings (root plus an expanded item), so a
// single resourceType argument is insufficient. Each selector is lowered in
// its binding's generated schema type and item selectors are rooted at the
// item value rather than at a FHIR document payload.
func lowerRecipeExpressionScoped(input expression.Expression, bindVars map[string]any, rootResourceType string, contexts map[string]string) (ir.PhysicalExpression, error) {
	if input.Document != nil {
		context := strings.TrimSpace(input.Document.Context)
		if context == "" {
			context = "root"
		}
		if _, ok := contexts[context]; !ok {
			return ir.PhysicalExpression{}, fmt.Errorf("document context %q is not in scope", context)
		}
		return lowerDocumentRef(*input.Document, physicalNullBehavior(input.NullBehavior)), nil
	}
	if input.Selector != nil {
		variable := strings.TrimSpace(input.Selector.Context)
		if variable == "" {
			variable = "root"
		}
		resourceType := rootResourceType
		if resolved, ok := contexts[variable]; ok {
			resourceType = resolved
		}
		physical, err := LowerRecipeExpression(input, bindVars, resourceType)
		if err != nil {
			return ir.PhysicalExpression{}, err
		}
		if variable != "root" {
			physical.Extract.Source.Variable = variable
			physical.Extract.Source.Path = nil
		}
		return physical, nil
	}
	if input.Literal != nil {
		return LowerRecipeExpression(input, bindVars, rootResourceType)
	}
	if input.Call == nil {
		return ir.PhysicalExpression{}, fmt.Errorf("recipe expression has no selector, literal, or call")
	}
	cardinality := ir.PhysicalScalarCardinality
	if input.Type.Cardinality == expression.Many {
		cardinality = ir.PhysicalArrayCardinality
	}
	behavior := ir.PhysicalPreserveNull
	if input.NullBehavior == expression.NullEmpty {
		behavior = ir.PhysicalEmptyOnNull
	}
	call := &ir.PhysicalCall{Name: strings.ToLower(strings.TrimSpace(input.Call.Name))}
	if input.Call.Target != nil {
		call.TargetKind = string(input.Call.Target.Kind)
	}
	for index, argument := range input.Call.Args {
		if call.Name == "cast" && input.Call.Target != nil && index == 1 {
			continue
		}
		lowered, err := lowerRecipeExpressionScoped(argument, bindVars, rootResourceType, contexts)
		if err != nil {
			return ir.PhysicalExpression{}, fmt.Errorf("call %q argument %d: %w", input.Call.Name, index, err)
		}
		call.Args = append(call.Args, lowered)
	}
	return ir.PhysicalExpression{Kind: ir.PhysicalCallExpression, Cardinality: cardinality, NullBehavior: behavior, Call: call}, nil
}

func physicalNullBehavior(behavior expression.NullBehavior) ir.PhysicalNullBehavior {
	if behavior == expression.NullEmpty {
		return ir.PhysicalEmptyOnNull
	}
	return ir.PhysicalPreserveNull
}

func appendRecipeIdentity(plan *ir.PhysicalPlan, output semantic.OutputPlan) error {
	if output.Identity == nil {
		return nil
	}
	physical, err := lowerRecipeExpressionScoped(output.Identity.Expression, plan.BindVars, output.RootResourceType, recipeExpressionContexts(output))
	if err != nil {
		return fmt.Errorf("identity: %w", err)
	}
	for index := range plan.Operations {
		operation := &plan.Operations[index]
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		operation.Return.Projections = append(operation.Return.Projections, ir.PhysicalProjection{Name: "__loom_row_id", Expression: &physical})
		return nil
	}
	return fmt.Errorf("canonical plan has no RETURN operation for identity")
}

func recipeRowExpansion(output semantic.OutputPlan) *semantic.SemanticRowExpansion {
	if output.RowExpansion != nil {
		copy := *output.RowExpansion
		return &copy
	}
	return nil
}
