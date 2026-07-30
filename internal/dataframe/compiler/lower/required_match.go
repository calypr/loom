package lower

import (
	"encoding/json"
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
)

// appendRequiredTraversalMatchFilters lowers every REQUIRED semantic route to
// a correlated typed EXISTS subplan. The subplan is deliberately separate
// from optional traversal materialization: it is a root semi-join and must run
// before the root sort/window, while optional traversal sets remain post-limit.
func appendRequiredTraversalMatchFilters(physical *ir.PhysicalPlan, root semantic.SemanticNode) error {
	nextMatch := 0
	seen := map[string]struct{}{}
	var walk func(semantic.SemanticNode, []semantic.SemanticNode, bool) error
	walk = func(parent semantic.SemanticNode, route []semantic.SemanticNode, requiredPrefix bool) error {
		for _, child := range parent.Children {
			next := append(append([]semantic.SemanticNode(nil), route...), child)
			childRequired := child.MatchMode.Required()
			if childRequired && requiredPrefix {
				// Two required children with the same physical route and typed
				// predicates are the same root semi-join even when their aliases
				// differ. Deduplicating this exact proof is safe: it does not
				// move a predicate across the root execution window or alter any
				// optional child materialization.
				key, err := requiredSemanticRouteKey(root.ResourceType, next)
				if err != nil {
					return err
				}
				if _, duplicate := seen[key]; duplicate {
					physical.RequiredMatchReuseCount++
					continue
				}
				seen[key] = struct{}{}
				predicate, err := buildRequiredTraversalExists(physical, nextMatch, root, next)
				if err != nil {
					return err
				}
				physical.Operations = append(physical.Operations, ir.PhysicalOperation{
					Kind:   ir.PhysicalFilterOp,
					Source: ir.PhysicalSource{SemanticNode: child.Alias, ResourceType: child.ResourceType, Relationship: child.EdgeLabel},
					Filter: &ir.PhysicalFilter{Expression: &predicate},
				})
				nextMatch++
			}
			if err := walk(child, next, requiredPrefix && childRequired); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(root, nil, true)
}

// requiredSemanticRouteKey intentionally excludes aliases and selection
// shape. A required route only contributes root membership, so aliases cannot
// distinguish two predicates. JSON gives us a deterministic key for typed
// filters without using AQL text or resource-specific knowledge.
func requiredSemanticRouteKey(rootResourceType string, route []semantic.SemanticNode) (string, error) {
	type step struct {
		ResourceType string
		EdgeLabel    string
		Filters      []spec.TypedFilter
	}
	steps := make([]step, 0, len(route))
	for _, node := range route {
		steps = append(steps, step{ResourceType: node.ResourceType, EdgeLabel: node.EdgeLabel, Filters: node.Filters})
	}
	encoded, err := json.Marshal(struct {
		Root  string
		Steps []step
	}{Root: rootResourceType, Steps: steps})
	if err != nil {
		return "", fmt.Errorf("encode required traversal reuse key: %w", err)
	}
	return string(encoded), nil
}

func buildRequiredTraversalExists(physical *ir.PhysicalPlan, matchIndex int, root semantic.SemanticNode, routeNodes []semantic.SemanticNode) (ir.PhysicalPredicateExpression, error) {
	if len(routeNodes) == 0 {
		return ir.PhysicalPredicateExpression{}, fmt.Errorf("required traversal match %d has no route steps", matchIndex)
	}

	subplan := ir.PhysicalSubplan{Captures: []string{"root"}}
	parentVariable := "root"
	parentType := root.ResourceType
	for stepIndex, node := range routeNodes {
		route, err := resolveStorageRoute(parentType, node.EdgeLabel, node.ResourceType)
		if err != nil {
			return ir.PhysicalPredicateExpression{}, fmt.Errorf("required traversal match %d step %d: %w", matchIndex, stepIndex, err)
		}
		nodeVariable := fmt.Sprintf("required_%d_node_%d", matchIndex, stepIndex)
		edgeVariable := fmt.Sprintf("required_%d_edge_%d", matchIndex, stepIndex)
		prefix := fmt.Sprintf("required_%d_%d", matchIndex, stepIndex)
		labelBind := prefix + "_label"
		typeBind := prefix + "_target_type"
		edgeCollectionBind := prefix + "_edge_collection"
		physical.BindVars[labelBind] = node.EdgeLabel
		physical.BindVars[typeBind] = node.ResourceType
		physical.BindVars[edgeCollectionBind] = "fhir_edge"

		subplan.Operations = append(subplan.Operations, ir.PhysicalOperation{
			Kind:   ir.PhysicalTraversalOp,
			Source: ir.PhysicalSource{SemanticNode: node.Alias, ResourceType: node.ResourceType, Relationship: node.EdgeLabel},
			Traversal: &ir.PhysicalTraversal{
				SourceVariable: parentVariable, TargetVariable: nodeVariable, EdgeVariable: edgeVariable,
				Direction: route.Direction, EdgeCollectionBindKey: edgeCollectionBind,
				EdgeLabelBindKey: labelBind, TargetTypeBindKey: typeBind,
				EdgeTargetTypeField: route.targetEdgeTypeField(),
			},
		})
		subplan.Operations = appendProjectScope(subplan.Operations, []string{edgeVariable, nodeVariable}, node.EdgeLabel, node)
		subplan.Operations = appendDatasetGenerationScope(subplan.Operations, []string{edgeVariable, nodeVariable}, node.EdgeLabel, node)
		subplan.Operations = appendAuthScope(subplan.Operations, []ir.PhysicalValue{
			{Variable: edgeVariable, Path: []string{"auth_resource_path"}},
			{Variable: nodeVariable, Path: []string{"auth_resource_path"}},
		}, fmt.Sprintf("required_%d_%d_scope_allowed", matchIndex, stepIndex), node)
		for filterIndex, filter := range node.Filters {
			if err := spec.ValidateTypedFilterForResource(node.ResourceType, filter); err != nil {
				return ir.PhysicalPredicateExpression{}, fmt.Errorf("required traversal match %d step %d filter %d: %w", matchIndex, stepIndex, filterIndex, err)
			}
			selector, err := spec.ParseSelector(filter.Selector)
			if err != nil {
				return ir.PhysicalPredicateExpression{}, err
			}
			predicate := ir.PhysicalPredicate{Operator: string(filter.Operator), Quantifier: filter.Quantifier, ValueKind: filter.FieldKind,
				LeftExpression: &ir.PhysicalExpression{Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalEmptyOnNull,
					Extract: &ir.PhysicalExtract{Source: ir.PhysicalValue{Variable: nodeVariable, Path: []string{"payload"}}, ResourceType: node.ResourceType, Selector: selector}}}
			if filter.Operator != spec.FilterExists && filter.Operator != spec.FilterMissing {
				if len(filter.Values) == 0 {
					return ir.PhysicalPredicateExpression{}, fmt.Errorf("required traversal filter %q has no value", filter.FieldRef)
				}
				key := fmt.Sprintf("required_%d_%d_filter_%d_value", matchIndex, stepIndex, filterIndex)
				if filter.Operator == spec.FilterIn {
					values := make([]any, 0, len(filter.Values))
					for _, value := range filter.Values {
						literal, err := filterLiteral(value)
						if err != nil {
							return ir.PhysicalPredicateExpression{}, err
						}
						values = append(values, literal)
					}
					physical.BindVars[key] = values
				} else {
					literal, err := filterLiteral(filter.Values[0])
					if err != nil {
						return ir.PhysicalPredicateExpression{}, err
					}
					physical.BindVars[key] = literal
				}
				predicate.Right = &ir.PhysicalValue{BindKey: key}
			}
			subplan.Operations = append(subplan.Operations, ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Expression: &ir.PhysicalPredicateExpression{Kind: ir.PhysicalComparisonPredicate, Comparison: &predicate}}})
		}
		parentVariable = nodeVariable
		parentType = node.ResourceType
	}
	resultBind := fmt.Sprintf("required_%d_result", matchIndex)
	physical.BindVars[resultBind] = 1
	subplan.Return = ir.PhysicalExpression{
		Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull,
		Value: &ir.PhysicalValue{BindKey: resultBind},
	}
	return ir.PhysicalPredicateExpression{Kind: ir.PhysicalExistsPredicate, Exists: &subplan}, nil
}
