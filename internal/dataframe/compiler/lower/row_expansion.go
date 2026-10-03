package lower

import (
	"fmt"
	"strconv"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

type physicalExpansionBinding struct {
	Node     semantic.SemanticNode
	Variable string
}

func semanticExpansionOwnerPath(root semantic.SemanticNode, owner semantic.SemanticOccurrence) ([]semantic.SemanticNode, error) {
	if owner.Alias == root.Alias && (owner.OccurrenceID == "" || owner.OccurrenceID == root.OccurrenceID) {
		return []semantic.SemanticNode{root}, nil
	}
	var matches [][]semantic.SemanticNode
	var walk func(semantic.SemanticNode, []semantic.SemanticNode)
	walk = func(node semantic.SemanticNode, path []semantic.SemanticNode) {
		path = append(append([]semantic.SemanticNode(nil), path...), node)
		if node.OccurrenceID == owner.OccurrenceID && node.Alias == owner.Alias && node.ResourceType == owner.ResourceType {
			matches = append(matches, path)
			return
		}
		for _, child := range node.Children {
			walk(child, path)
		}
	}
	walk(root, nil)
	if len(matches) != 1 {
		return nil, fmt.Errorf("row expansion owner occurrence %q resolves to %d semantic routes", owner.OccurrenceID, len(matches))
	}
	return matches[0], nil
}

func appendRecipeRowExpansion(plan *ir.PhysicalPlan, output semantic.OutputPlan, path []semantic.SemanticNode, policy ir.PhysicalOptimizationPolicy) (map[string]physicalExpansionBinding, error) {
	expansion := output.RowExpansion
	if expansion == nil {
		return nil, nil
	}
	if err := expansion.Validate(); err != nil {
		return nil, fmt.Errorf("row expansion: %w", err)
	}
	if len(path) == 0 || path[0].Alias != output.Root.Alias {
		return nil, fmt.Errorf("row expansion owner route does not start at root occurrence")
	}
	owner := path[len(path)-1]
	if owner.Alias != expansion.Owner.Alias || owner.ResourceType != expansion.Owner.ResourceType || owner.OccurrenceID != expansion.Owner.OccurrenceID {
		return nil, fmt.Errorf("row expansion route does not terminate at exact owner occurrence %q", expansion.Owner.OccurrenceID)
	}

	bindings := map[string]physicalExpansionBinding{
		output.Root.OccurrenceID: {Node: output.Root, Variable: "root"},
	}
	parentVariable := "root"
	parentType := output.Root.ResourceType
	route := make([]ir.PhysicalUnnestRouteStep, 0, len(path)-1)
	for index, node := range path[1:] {
		stepNumber := index + 1
		targetVariable := fmt.Sprintf("__loom_expansion_node_%d", stepNumber)
		edgeVariable := fmt.Sprintf("__loom_expansion_edge_%d", stepNumber)
		prefix := fmt.Sprintf("expansion_route_%d", stepNumber)
		traversal, err := BuildPhysicalTraversal(TraversalLoweringRequest{
			FromType: parentType, EdgeLabel: node.EdgeLabel, ToType: node.ResourceType,
			SourceVariable: parentVariable, TargetVariable: targetVariable, EdgeVariable: edgeVariable,
			BindPrefix: prefix, Policy: policy,
		})
		if err != nil {
			return nil, fmt.Errorf("row expansion route step %d: %w", stepNumber, err)
		}
		for key, value := range traversal.BindVars {
			plan.BindVars[key] = value
		}
		scope, err := buildPhysicalChildRouteScope(plan, node, edgeVariable, targetVariable, prefix)
		if err != nil {
			return nil, fmt.Errorf("row expansion route occurrence %q: %w", node.OccurrenceID, err)
		}
		route = append(route, ir.PhysicalUnnestRouteStep{OccurrenceID: node.OccurrenceID, Traversal: traversal.Traversal, Scope: scope})
		bindings[node.OccurrenceID] = physicalExpansionBinding{Node: node, Variable: targetVariable}
		parentVariable, parentType = targetVariable, node.ResourceType
	}

	contexts := recipeExpressionContexts(output)
	source, err := lowerRecipeExpressionScoped(expansion.Source.Expression, plan.BindVars, output.RootResourceType, contexts)
	if err != nil {
		return nil, fmt.Errorf("row expansion source: %w", err)
	}
	rewriteRecipeExpressionBinding(&source, expansion.Owner.Alias, ir.PhysicalValue{Variable: parentVariable, Path: []string{"payload"}})
	if source.Extract != nil && source.Extract.Source.Variable != parentVariable {
		return nil, fmt.Errorf("row expansion source did not bind to owner occurrence %q", expansion.Owner.OccurrenceID)
	}
	if source.Value != nil && source.Value.Variable != parentVariable {
		return nil, fmt.Errorf("row expansion source did not bind to owner occurrence %q", expansion.Owner.OccurrenceID)
	}
	ancestors := recipeExpansionAncestors(source)
	var emptyPolicy ir.PhysicalUnnestEmptyPolicy
	switch expansion.EmptyPolicy {
	case semantic.ExpansionExclude:
		emptyPolicy = ir.PhysicalUnnestExclude
	case semantic.ExpansionPreserveParent:
		emptyPolicy = ir.PhysicalUnnestPreserveParent
	case semantic.ExpansionError:
		emptyPolicy = ir.PhysicalUnnestError
	default:
		return nil, fmt.Errorf("unsupported row expansion empty policy %q", expansion.EmptyPolicy)
	}
	ordinality := expansion.Ordinality
	if ordinality == "" {
		ordinality = "__loom_expansion_ordinal"
	}
	operation := ir.PhysicalOperation{Kind: ir.PhysicalUnnestOp,
		Source: ir.PhysicalSource{ResourceType: expansion.Owner.ResourceType, SemanticField: "expand"},
		Unnest: &ir.PhysicalUnnest{
			Owner:          ir.PhysicalUnnestOwner{OccurrenceID: expansion.Owner.OccurrenceID, ResourceType: expansion.Owner.ResourceType, RootVariable: "root", OwnerVariable: parentVariable, Route: route},
			OutputVariable: expansion.ItemBinding, Ordinality: ordinality, HasItemVariable: "__loom_has_expanded_item",
			Expression: source, Ancestors: ancestors, EmptyPolicy: emptyPolicy,
		},
	}
	plan.Operations = append(plan.Operations, operation)
	return bindings, nil
}

func recipeExpansionAncestors(source ir.PhysicalExpression) []ir.PhysicalUnnestAncestor {
	if source.Extract == nil {
		return nil
	}
	selector := source.Extract.Selector
	if len(selector.Steps) < 2 {
		return nil
	}
	ancestors := make([]ir.PhysicalUnnestAncestor, 0, len(selector.Steps)-1)
	for index, step := range selector.Steps[:len(selector.Steps)-1] {
		if !step.Iterate {
			continue
		}
		ancestors = append(ancestors, ir.PhysicalUnnestAncestor{
			StepIndex: index,
			Variable:  fmt.Sprintf("__loom_expansion_ancestor_%d", len(ancestors)),
		})
	}
	if len(ancestors) == 0 {
		return nil
	}
	return ancestors
}

func appendRecipeExpansionIdentity(plan *ir.PhysicalPlan, output semantic.OutputPlan) error {
	if !output.ExpansionIdentity {
		return nil
	}
	var expansion *ir.PhysicalUnnest
	for index := range plan.Operations {
		if plan.Operations[index].Kind != ir.PhysicalUnnestOp || plan.Operations[index].Unnest == nil {
			continue
		}
		if expansion != nil {
			return fmt.Errorf("expansion identity requires exactly one row expansion")
		}
		expansion = plan.Operations[index].Unnest
	}
	if expansion == nil {
		return fmt.Errorf("expansion identity requires a row expansion")
	}
	occurrenceID := expansion.Owner.OccurrenceID
	if occurrenceID == "" {
		occurrenceID = "root"
	}
	occurrenceBind := "expansion_identity_occurrence"
	for suffix := 1; ; suffix++ {
		if _, exists := plan.BindVars[occurrenceBind]; !exists {
			break
		}
		occurrenceBind = "expansion_identity_occurrence_" + strconv.Itoa(suffix)
	}
	plan.BindVars[occurrenceBind] = occurrenceID
	fields := []ir.PhysicalExpressionProjection{
		{Name: "root_id", Expression: unnestValue(ir.PhysicalValue{Variable: expansion.Owner.RootVariable, Path: []string{"_id"}})},
		{Name: "occurrence_id", Expression: ir.PhysicalExpression{Kind: ir.PhysicalLiteralExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Literal: &ir.PhysicalLiteral{BindKey: occurrenceBind}}},
	}
	width := len(strconv.Itoa(maxExpansionIdentityIndex(expansion.Owner.Route)))
	for index, step := range expansion.Owner.Route {
		name := fmt.Sprintf("edge_%0*d_id", width, index)
		fields = append(fields, ir.PhysicalExpressionProjection{Name: name, Expression: unnestValue(ir.PhysicalValue{Variable: step.Traversal.EdgeVariable, Path: []string{"_id"}})})
	}
	fields = append(fields,
		ir.PhysicalExpressionProjection{Name: "owner_id", Expression: unnestValue(ir.PhysicalValue{Variable: expansion.Owner.OwnerVariable, Path: []string{"_id"}})},
		ir.PhysicalExpressionProjection{Name: "item_present", Expression: unnestValue(ir.PhysicalValue{Variable: expansion.HasItemVariable})},
		ir.PhysicalExpressionProjection{Name: "ordinal", Expression: unnestValue(ir.PhysicalValue{Variable: expansion.Ordinality})},
	)
	identity := ir.PhysicalExpression{Kind: ir.PhysicalObjectExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalPreserveNull, Object: &ir.PhysicalObject{Fields: fields}}
	for index := range plan.Operations {
		operation := &plan.Operations[index]
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		operation.Return.Projections = append(operation.Return.Projections, ir.PhysicalProjection{Name: "__loom_expansion_identity", Hidden: true, Expression: &identity})
		return nil
	}
	return fmt.Errorf("canonical plan has no RETURN operation for expansion identity")
}

func unnestValue(value ir.PhysicalValue) ir.PhysicalExpression {
	return ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &value}
}

func maxExpansionIdentityIndex(route []ir.PhysicalUnnestRouteStep) int {
	if len(route) < 2 {
		return 0
	}
	return len(route) - 1
}

func expansionItemResourceType(output semantic.OutputPlan) string {
	if output.RowExpansion == nil {
		return ""
	}
	contexts := recipeExpressionContexts(output)
	return contexts[output.RowExpansion.ItemBinding]
}
