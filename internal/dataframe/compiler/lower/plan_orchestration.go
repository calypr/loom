package lower

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
	semanticpkg "github.com/calypr/loom/internal/dataframe/semantic"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

// BuildGenericPhysicalPlanWithPolicy threads an explicit optimizer policy
// through physical construction so prepared-selector ablations happen before
// references to prepared variables are attached to projections.
func BuildGenericPhysicalPlanWithPolicy(semantic semanticpkg.SemanticPlan, policy ir.PhysicalOptimizationPolicy) (ir.PhysicalPlan, error) {
	if strings.TrimSpace(semantic.Project) == "" {
		return ir.PhysicalPlan{}, fmt.Errorf("semantic plan project is required")
	}
	if err := semanticpkg.ValidateSemanticGraph(semantic); err != nil {
		return ir.PhysicalPlan{}, err
	}
	if !fhirschema.ResourceExists(semantic.Root.ResourceType) {
		return ir.PhysicalPlan{}, fmt.Errorf("root resource type %q is not represented by the generated FHIR schema", semantic.Root.ResourceType)
	}
	if err := validateGenericPhysicalNode(semantic.Root, true); err != nil {
		return ir.PhysicalPlan{}, err
	}

	physical := ir.PhysicalPlan{
		Version: 1,
		Source: ir.PhysicalSource{
			SemanticNode: semantic.Root.Alias,
			ResourceType: semantic.Root.ResourceType,
		},
		BindVars: map[string]any{
			"root_collection":                  semantic.Root.ResourceType,
			"project":                          semantic.Project,
			datasetGenerationBindKey:           datasetGenerationBindValue(semantic.DatasetGeneration),
			"auth_resource_paths":              append([]string(nil), semantic.AuthResourcePaths...),
			"auth_resource_paths_unrestricted": semanticAuthScopeUnrestricted(semantic),
			"scope_allowed":                    true,
		},
		Operations: []ir.PhysicalOperation{
			{
				Kind:     ir.PhysicalRootScanOp,
				Source:   ir.PhysicalSource{SemanticNode: semantic.Root.Alias, ResourceType: semantic.Root.ResourceType},
				RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"},
			},
		},
	}
	physical.Operations = appendProjectScope(physical.Operations, []string{"root"}, "", semantic.Root)
	physical.Operations = appendDatasetGenerationScope(physical.Operations, []string{"root"}, "", semantic.Root)
	physical.Operations = appendAuthScope(physical.Operations, []ir.PhysicalValue{{Variable: "root", Path: []string{"auth_resource_path"}}}, "root_scope_allowed", semantic.Root)
	if err := appendRootPhysicalFilters(&physical, semantic.Root); err != nil {
		return ir.PhysicalPlan{}, err
	}
	if err := appendRequiredTraversalMatchFilters(&physical, semantic.Root); err != nil {
		return ir.PhysicalPlan{}, err
	}

	childSetIndex := 0
	returnProjections := []ir.PhysicalProjection{}
	var walk func(parent semanticpkg.SemanticNode, parentVariable, projectionPrefix string) error
	walk = func(parent semanticpkg.SemanticNode, parentVariable, projectionPrefix string) error {
		for _, child := range parent.Children {
			if child.MatchMode.Required() {
				// Required routes are represented by the root semi-join emitted
				// above for membership, but they may still need a materialized
				// child set for selected fields or nested shaping. The second
				// physical set is post-window output work; it cannot change root
				// membership because the semi-join remains before SORT/LIMIT.
				if physicalNodeNeedsMaterializedSet(child) {
					childSetIndex++
					childProjectionPrefix := child.Alias
					if semantic.TraversalColumnNaming != recipe.TraversalColumnNamingAlias && projectionPrefix != "" {
						childProjectionPrefix = projectionPrefix + "__" + child.Alias
					}
					set, projections, err := buildOptionalChildPhysicalSet(&physical, childSetIndex, parent, parentVariable, child, childProjectionPrefix, policy)
					if err != nil {
						return err
					}
					physical.Operations = append(physical.Operations, ir.PhysicalOperation{Kind: ir.PhysicalSetOp, Source: ir.PhysicalSource{SemanticNode: child.Alias, ResourceType: child.ResourceType, Relationship: child.EdgeLabel}, Set: &set})
					returnProjections = append(returnProjections, projections...)
					if err := walk(child, set.Variable, childProjectionPrefix); err != nil {
						return err
					}
				}
				continue
			}
			if !physicalNodeNeedsMaterializedSet(child) {
				traversalIndex := 1
				for _, operation := range physical.Operations {
					if operation.Kind == ir.PhysicalTraversalOp {
						traversalIndex++
					}
				}
				nodeVariable := fmt.Sprintf("node_%d", traversalIndex)
				edgeVariable := fmt.Sprintf("edge_%d", traversalIndex)
				traversal, err := BuildPhysicalTraversal(TraversalLoweringRequest{
					FromType: parent.ResourceType, EdgeLabel: child.EdgeLabel, ToType: child.ResourceType,
					SourceVariable: parentVariable, TargetVariable: nodeVariable, EdgeVariable: edgeVariable,
					BindPrefix: fmt.Sprintf("traversal_%d", traversalIndex), Policy: policy,
				})
				if err != nil {
					return err
				}
				for key, value := range traversal.BindVars {
					physical.BindVars[key] = value
				}
				physical.Operations = append(physical.Operations, ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp,
					Source:    ir.PhysicalSource{SemanticNode: child.Alias, ResourceType: child.ResourceType, Relationship: child.EdgeLabel},
					Traversal: &traversal.Traversal})
				physical.Operations = appendProjectScope(physical.Operations, []string{edgeVariable, nodeVariable}, child.EdgeLabel, child)
				physical.Operations = appendDatasetGenerationScope(physical.Operations, []string{edgeVariable, nodeVariable}, child.EdgeLabel, child)
				physical.Operations = appendAuthScope(physical.Operations, []ir.PhysicalValue{{Variable: edgeVariable, Path: []string{"auth_resource_path"}}, {Variable: nodeVariable, Path: []string{"auth_resource_path"}}}, fmt.Sprintf("traversal_%d_scope_allowed", traversalIndex), child)
				if err := walk(child, nodeVariable, projectionPrefix); err != nil {
					return err
				}
				continue
			}
			// Optional children are correlated sets. Keeping them in a LET
			// subquery preserves the parent row grain while allowing typed child
			// filters and projections to be applied before materialization.
			childSetIndex++
			childProjectionPrefix := child.Alias
			if semantic.TraversalColumnNaming != recipe.TraversalColumnNamingAlias && projectionPrefix != "" {
				childProjectionPrefix = projectionPrefix + "__" + child.Alias
			}
			set, projections, err := buildOptionalChildPhysicalSet(&physical, childSetIndex, parent, parentVariable, child, childProjectionPrefix, policy)
			if err != nil {
				return err
			}
			physical.Operations = append(physical.Operations, ir.PhysicalOperation{Kind: ir.PhysicalSetOp, Source: ir.PhysicalSource{SemanticNode: child.Alias, ResourceType: child.ResourceType, Relationship: child.EdgeLabel}, Set: &set})
			returnProjections = append(returnProjections, projections...)
			if err := walk(child, set.Variable, childProjectionPrefix); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(semantic.Root, "root", ""); err != nil {
		return ir.PhysicalPlan{}, err
	}
	projections, err := rootPhysicalProjections(&physical, semantic.Root)
	if err != nil {
		return ir.PhysicalPlan{}, err
	}
	projections = append(projections, returnProjections...)
	physical.Operations = append(physical.Operations, physical.DeferredExpressionLets...)
	physical.DeferredExpressionLets = nil
	physical.Operations = append(physical.Operations, ir.PhysicalOperation{
		Kind:   ir.PhysicalReturnOp,
		Source: ir.PhysicalSource{SemanticNode: semantic.Root.Alias, ResourceType: semantic.Root.ResourceType, SemanticField: "_key"},
		Return: &ir.PhysicalReturn{Projections: projections},
	})
	if err := physical.Validate(); err != nil {
		return ir.PhysicalPlan{}, fmt.Errorf("validate generic physical plan: %w", err)
	}
	if err := ir.ValidateGenericPhysicalPlanScope(physical); err != nil {
		return ir.PhysicalPlan{}, fmt.Errorf("verify generic physical plan scope: %w", err)
	}
	return physical, nil
}

// physicalNodeNeedsMaterializedSet reports whether a node or any optional
// descendant has shaped output. Materializing an otherwise unselected parent
// is necessary to give nested sets a stable correlated source variable.
func physicalNodeNeedsMaterializedSet(node semanticpkg.SemanticNode) bool {
	if len(node.Fields) != 0 || len(node.Filters) != 0 || len(node.Pivots) != 0 || len(node.Aggregates) != 0 || len(node.Slices) != 0 || len(node.DynamicMaps) != 0 {
		return true
	}
	for _, child := range node.Children {
		if child.MatchMode.Required() || physicalNodeNeedsMaterializedSet(child) {
			return true
		}
	}
	return false
}

func appendProjectScope(operations []ir.PhysicalOperation, variables []string, relationship string, node semanticpkg.SemanticNode) []ir.PhysicalOperation {
	right := ir.PhysicalValue{BindKey: "project"}
	for _, variable := range variables {
		operations = append(operations, ir.PhysicalOperation{
			Kind:   ir.PhysicalFilterOp,
			Source: ir.PhysicalSource{SemanticNode: node.Alias, ResourceType: node.ResourceType, Relationship: relationship, SemanticField: "project"},
			Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator: "EQUALS",
				Left:     ir.PhysicalValue{Variable: variable, Path: []string{"project"}},
				Right:    &right,
			}},
		})
	}
	return operations
}

// appendDatasetGenerationScope applies the same exact generation bind to
// every physical document participating in a scan/traversal. With a nil bind
// value this renders `dataset_generation == null`, deliberately isolating
// legacy documents from later generation-qualified loads.
func appendDatasetGenerationScope(operations []ir.PhysicalOperation, variables []string, relationship string, node semanticpkg.SemanticNode) []ir.PhysicalOperation {
	right := ir.PhysicalValue{BindKey: datasetGenerationBindKey}
	for _, variable := range variables {
		operations = append(operations, ir.PhysicalOperation{
			Kind:   ir.PhysicalFilterOp,
			Source: ir.PhysicalSource{SemanticNode: node.Alias, ResourceType: node.ResourceType, Relationship: relationship, SemanticField: datasetGenerationField},
			Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator: "EQUALS",
				Left:     ir.PhysicalValue{Variable: variable, Path: []string{datasetGenerationField}},
				Right:    &right,
			}},
		})
	}
	return operations
}

func appendAuthScope(operations []ir.PhysicalOperation, scopedValues []ir.PhysicalValue, resultVariable string, node semanticpkg.SemanticNode) []ir.PhysicalOperation {
	inputs := append([]ir.PhysicalValue(nil), scopedValues...)
	inputs = append(inputs, ir.PhysicalValue{BindKey: "auth_resource_paths"}, ir.PhysicalValue{BindKey: "auth_resource_paths_unrestricted"})
	operations = append(operations, ir.PhysicalOperation{
		Kind:       ir.PhysicalDerivedLetOp,
		Source:     ir.PhysicalSource{SemanticNode: node.Alias, ResourceType: node.ResourceType, Relationship: node.EdgeLabel, SemanticField: "auth_resource_path"},
		DerivedLet: &ir.PhysicalDerivedLet{Variable: resultVariable, Operator: "AUTH_RESOURCE_PATH_ALLOWED", Inputs: inputs},
	})
	right := ir.PhysicalValue{BindKey: "scope_allowed"}
	return append(operations, ir.PhysicalOperation{
		Kind:   ir.PhysicalFilterOp,
		Source: ir.PhysicalSource{SemanticNode: node.Alias, ResourceType: node.ResourceType, Relationship: node.EdgeLabel, SemanticField: "auth_resource_path"},
		Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS", Left: ir.PhysicalValue{Variable: resultVariable}, Right: &right}},
	})
}

func validateGenericPhysicalNode(node semanticpkg.SemanticNode, root bool) error {
	for _, child := range node.Children {
		if err := validateGenericPhysicalNode(child, false); err != nil {
			return err
		}
	}
	return nil
}
