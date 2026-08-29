package lower

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
)

const (
	maxGraphTraversalDeclarations = 32
	graphLookaheadBindKey         = "graph_limit"
)

// BuildGraphPhysicalPlan lowers an explicit graph frontend onto the same
// scoped root/traversal lowering used by dataframe recipes. Each route gets
// its own path set, so sibling branches form independent unions.
func BuildGraphPhysicalPlan(output semantic.OutputPlan, context semantic.ExecutionContext, limit int, policy ir.PhysicalOptimizationPolicy) (ir.PhysicalPlan, error) {
	if limit < 1 || limit > 10000 {
		return ir.PhysicalPlan{}, fmt.Errorf("graph limit must be between 1 and 10000")
	}
	if strings.TrimSpace(context.Project) == "" {
		return ir.PhysicalPlan{}, fmt.Errorf("semantic plan project is required")
	}
	output.Root = normalizeGraphMatchModes(output.Root)
	if err := semantic.ValidateSemanticGraph(output.Root); err != nil {
		return ir.PhysicalPlan{}, err
	}
	if countGraphTraversals(output.Root) > maxGraphTraversalDeclarations {
		return ir.PhysicalPlan{}, fmt.Errorf("graph traversal declaration count exceeds %d", maxGraphTraversalDeclarations)
	}
	if err := validateGraphNode(output.Root, true); err != nil {
		return ir.PhysicalPlan{}, err
	}

	physical := ir.PhysicalPlan{
		Version: 1,
		Source:  ir.PhysicalSource{SemanticNode: output.Root.Alias, ResourceType: output.Root.ResourceType},
		BindVars: map[string]any{
			"root_collection":                  output.Root.ResourceType,
			"project":                          context.Project,
			datasetGenerationBindKey:           datasetGenerationBindValue(context.DatasetGeneration),
			"auth_resource_paths":              append([]string(nil), context.AuthResourcePaths...),
			"auth_resource_paths_unrestricted": semanticAuthScopeUnrestricted(context),
			"scope_allowed":                    true,
			graphLookaheadBindKey:              limit + 1,
		},
		Operations: []ir.PhysicalOperation{{
			Kind:     ir.PhysicalRootScanOp,
			Source:   ir.PhysicalSource{SemanticNode: output.Root.Alias, ResourceType: output.Root.ResourceType},
			RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"},
		}},
	}
	physical.Operations = appendProjectScope(physical.Operations, []string{"root"}, "", output.Root)
	physical.Operations = appendDatasetGenerationScope(physical.Operations, []string{"root"}, "", output.Root)
	physical.Operations = appendAuthScope(physical.Operations, []ir.PhysicalValue{{Variable: "root", Path: []string{"auth_resource_path"}}}, "root_scope_allowed", output.Root)
	if err := appendRootPhysicalFilters(&physical, output.Root); err != nil {
		return ir.PhysicalPlan{}, err
	}
	if err := appendRequiredTraversalMatchFilters(&physical, graphRequiredSemiJoinRoot(output.Root)); err != nil {
		return ir.PhysicalPlan{}, err
	}

	physical.Operations = append(physical.Operations, ir.PhysicalOperation{
		Kind:   ir.PhysicalPathSeedOp,
		Source: ir.PhysicalSource{SemanticNode: output.Root.Alias, ResourceType: output.Root.ResourceType},
		PathSeed: &ir.PhysicalPathSeed{Variable: "path_root", RouteOrder: 0, Node: ir.PhysicalPathNode{
			Alias: graphAlias(output.Root, "root"), ResourceType: output.Root.ResourceType,
			Value: ir.PhysicalValue{Variable: "root"},
		}},
	})

	// A root-only path is useful when the request has only OPTIONAL branches:
	// an optional miss leaves the root eligible and contributes no synthetic
	// child. Once any REQUIRED route is declared, however, returning the root
	// itself would leak a path the caller did not ask for; required semi-joins
	// below still gate root membership.
	pathSets := make([]string, 0, countGraphTraversals(output.Root)+1)
	if !graphHasRequiredTraversal(output.Root) {
		pathSets = append(pathSets, "path_root")
	}
	routeOrder := 1
	var walk func(parent semantic.SemanticNode, sourcePathSet string) error
	walk = func(parent semantic.SemanticNode, sourcePathSet string) error {
		for _, child := range parent.Children {
			index := routeOrder
			routeOrder++
			targetVariable := fmt.Sprintf("graph_node_%d", index)
			edgeVariable := fmt.Sprintf("graph_edge_%d", index)
			sourceVariable := fmt.Sprintf("graph_source_%d", index)
			traversalResult, err := BuildPhysicalTraversal(TraversalLoweringRequest{
				FromType: parent.ResourceType, EdgeLabel: child.EdgeLabel, ToType: child.ResourceType,
				SourceVariable: sourceVariable, TargetVariable: targetVariable, EdgeVariable: edgeVariable,
				BindPrefix: fmt.Sprintf("graph_traversal_%d", index), Policy: policy,
			})
			if err != nil {
				return fmt.Errorf("graph traversal %q: %w", child.Alias, err)
			}
			for key, value := range traversalResult.BindVars {
				physical.BindVars[key] = value
			}
			traversal := traversalResult.Traversal
			traversal.SourceVariable = sourceVariable
			setVariable := fmt.Sprintf("path_%d", index)
			matchMode := child.MatchMode
			if strings.TrimSpace(string(matchMode)) == "" {
				matchMode = spec.TraversalMatchRequired
			}
			pathSets = append(pathSets, setVariable)
			scope := make([]ir.PhysicalOperation, 0, 8+len(child.Filters))
			scope = appendProjectScope(scope, []string{edgeVariable, targetVariable}, child.EdgeLabel, child)
			scope = appendDatasetGenerationScope(scope, []string{edgeVariable, targetVariable}, child.EdgeLabel, child)
			scope = appendAuthScope(scope, []ir.PhysicalValue{{Variable: edgeVariable, Path: []string{"auth_resource_path"}}, {Variable: targetVariable, Path: []string{"auth_resource_path"}}}, fmt.Sprintf("graph_traversal_%d_scope_allowed", index), child)
			if err := appendGraphPathFilters(&scope, child, targetVariable, physical.BindVars); err != nil {
				return err
			}
			physical.Operations = append(physical.Operations, ir.PhysicalOperation{
				Kind:   ir.PhysicalPathExtendOp,
				Source: ir.PhysicalSource{SemanticNode: child.Alias, ResourceType: child.ResourceType, Relationship: child.EdgeLabel},
				PathExtend: &ir.PhysicalPathExtend{
					Variable: setVariable, SourceVariable: sourcePathSet, SourcePath: nil, Traversal: traversal,
					Node:         ir.PhysicalPathNode{Alias: graphAlias(child, child.Alias), ResourceType: child.ResourceType, Value: ir.PhysicalValue{Variable: targetVariable}},
					Relationship: ir.PhysicalPathRelationship{Alias: child.Alias, LabelBindKey: traversal.EdgeLabelBindKey, FromResourceType: parent.ResourceType, ToResourceType: child.ResourceType},
					MatchMode:    string(matchMode), RouteOrder: index, Scope: scope,
				},
			})
			if err := walk(child, setVariable); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(output.Root, "path_root"); err != nil {
		return ir.PhysicalPlan{}, err
	}
	physical.Operations = append(physical.Operations, ir.PhysicalOperation{Kind: ir.PhysicalGraphReturnOp, GraphReturn: &ir.PhysicalGraphReturn{PathSets: pathSets, LimitBindKey: graphLookaheadBindKey}})
	if err := physical.Validate(); err != nil {
		return ir.PhysicalPlan{}, fmt.Errorf("validate graph physical plan: %w", err)
	}
	return physical, nil
}

func normalizeGraphMatchModes(node semantic.SemanticNode) semantic.SemanticNode {
	copy := node
	copy.Children = make([]semantic.SemanticNode, len(node.Children))
	for index, child := range node.Children {
		childCopy := normalizeGraphMatchModes(child)
		if strings.TrimSpace(string(childCopy.MatchMode)) == "" {
			childCopy.MatchMode = spec.TraversalMatchRequired
		}
		copy.Children[index] = childCopy
	}
	return copy
}

// graphRequiredSemiJoinRoot keeps REQUIRED membership proofs for routes that
// are reachable from the root through required ancestors. A required child
// below an OPTIONAL branch cannot disqualify the root when that branch is
// absent; its path set simply contributes no rows.
func graphRequiredSemiJoinRoot(node semantic.SemanticNode) semantic.SemanticNode {
	copy := node
	copy.Children = make([]semantic.SemanticNode, len(node.Children))
	for index, child := range node.Children {
		copy.Children[index] = graphRequiredSemiJoinNode(child, true)
	}
	return copy
}

func graphRequiredSemiJoinNode(node semantic.SemanticNode, ancestorsRequired bool) semantic.SemanticNode {
	copy := node
	if !ancestorsRequired {
		copy.MatchMode = spec.TraversalMatchOptional
	}
	childAncestorsRequired := ancestorsRequired && node.MatchMode.Required()
	copy.Children = make([]semantic.SemanticNode, len(node.Children))
	for index, child := range node.Children {
		copy.Children[index] = graphRequiredSemiJoinNode(child, childAncestorsRequired)
	}
	return copy
}

// CompileResolvedGraphPlan selects the single graph output from the resolved
// recipe boundary and lowers it to graph/path IR. The graph frontend creates
// one output deliberately; accepting multiple outputs here would make path
// union and lookahead-limit semantics ambiguous.
func CompileResolvedGraphPlan(resolved semantic.ResolvedRecipePlan, limit int, policy ir.PhysicalOptimizationPolicy) (ir.PhysicalPlan, error) {
	if len(resolved.SemanticPlan.Outputs) != 1 {
		return ir.PhysicalPlan{}, fmt.Errorf("graph query requires exactly one output, got %d", len(resolved.SemanticPlan.Outputs))
	}
	output := resolved.SemanticPlan.Outputs[0]
	context := semantic.ExecutionContext{
		Project:           resolved.SemanticPlan.Bindings.Project,
		DatasetGeneration: resolved.SemanticPlan.Bindings.DatasetGeneration,
		AuthResourcePaths: append([]string(nil), resolved.SemanticPlan.Bindings.AuthResourcePaths...),
		AuthScopeMode:     resolved.SemanticPlan.Bindings.AuthScopeMode,
	}
	return BuildGraphPhysicalPlan(output, context, limit, policy)
}

func graphAlias(node semantic.SemanticNode, fallback string) string {
	if strings.TrimSpace(node.Alias) != "" {
		return node.Alias
	}
	return fallback
}

func countGraphTraversals(node semantic.SemanticNode) int {
	count := len(node.Children)
	for _, child := range node.Children {
		count += countGraphTraversals(child)
	}
	return count
}

func graphHasRequiredTraversal(node semantic.SemanticNode) bool {
	var walk func(semantic.SemanticNode, bool) bool
	walk = func(parent semantic.SemanticNode, requiredPrefix bool) bool {
		for _, child := range parent.Children {
			mode := child.MatchMode
			if strings.TrimSpace(string(mode)) == "" {
				mode = spec.TraversalMatchRequired
			}
			childRequired := mode == spec.TraversalMatchRequired
			if requiredPrefix && childRequired {
				return true
			}
			if walk(child, requiredPrefix && childRequired) {
				return true
			}
		}
		return false
	}
	return walk(node, true)
}

func validateGraphNode(node semantic.SemanticNode, root bool) error {
	if !root && node.MatchMode != spec.TraversalMatchRequired && node.MatchMode != spec.TraversalMatchOptional && node.MatchMode != "" {
		return fmt.Errorf("graph traversal %q has invalid match mode %q", node.Alias, node.MatchMode)
	}
	if len(node.Fields) > 0 || len(node.Pivots) > 0 || len(node.Aggregates) > 0 || len(node.Slices) > 0 || len(node.DynamicMaps) > 0 {
		return fmt.Errorf("graph traversal %q cannot declare dataframe shaping fields", node.Alias)
	}
	for _, child := range node.Children {
		if err := validateGraphNode(child, false); err != nil {
			return err
		}
	}
	return nil
}

func appendGraphPathFilters(operations *[]ir.PhysicalOperation, node semantic.SemanticNode, targetVariable string, binds map[string]any) error {
	for index, filter := range node.Filters {
		if err := spec.ValidateTypedFilterForResource(node.ResourceType, filter); err != nil {
			return fmt.Errorf("graph traversal %q filter %q: %w", node.Alias, filter.FieldRef, err)
		}
		selector, err := spec.ParseSelector(filter.Selector)
		if err != nil {
			return fmt.Errorf("graph traversal %q filter selector: %w", node.Alias, err)
		}
		predicate := ir.PhysicalPredicate{Operator: string(filter.Operator), Quantifier: filter.Quantifier, ValueKind: filter.FieldKind,
			LeftExpression: &ir.PhysicalExpression{Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalEmptyOnNull,
				Extract: &ir.PhysicalExtract{Source: ir.PhysicalValue{Variable: targetVariable, Path: []string{"payload"}}, ResourceType: node.ResourceType, Selector: selector, ExecutionMode: selectorExecutionMode(node.ResourceType, selector)}},
		}
		if filter.Operator != spec.FilterExists && filter.Operator != spec.FilterMissing {
			if len(filter.Values) == 0 {
				return fmt.Errorf("graph traversal %q filter %q has no value", node.Alias, filter.FieldRef)
			}
			key := fmt.Sprintf("graph_%s_filter_%d_value", sanitizeColumnName(node.Alias), index+1)
			if filter.Operator == spec.FilterIn {
				values := make([]any, 0, len(filter.Values))
				for _, value := range filter.Values {
					literal, err := filterLiteral(value)
					if err != nil {
						return err
					}
					values = append(values, literal)
				}
				binds[key] = values
				predicate.Right = &ir.PhysicalValue{BindKey: key}
			} else {
				literal, err := filterLiteral(filter.Values[0])
				if err != nil {
					return err
				}
				binds[key] = literal
				predicate.Right = &ir.PhysicalValue{BindKey: key}
			}
		}
		*operations = append(*operations, ir.PhysicalOperation{Kind: ir.PhysicalFilterOp,
			Source: ir.PhysicalSource{SemanticNode: node.Alias, ResourceType: node.ResourceType, Relationship: node.EdgeLabel, SemanticField: filter.FieldRef},
			Filter: &ir.PhysicalFilter{Expression: &ir.PhysicalPredicateExpression{Kind: ir.PhysicalComparisonPredicate, Comparison: &predicate}}})
	}
	return nil
}
