package dataframe

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/fhirschema"
)

// BuildGenericPhysicalPlan lowers only the navigation skeleton of an already
// validated semantic plan. Selection, user filtering, pivots, aggregation, and
// slicing remain intentionally unsupported until their physical operators are
// frozen. The returned plan is inspectable and renderer-independent.
func BuildGenericPhysicalPlan(semantic SemanticPlan) (PhysicalPlan, error) {
	if strings.TrimSpace(semantic.Project) == "" {
		return PhysicalPlan{}, fmt.Errorf("semantic plan project is required")
	}
	if err := ValidateSemanticGraph(semantic); err != nil {
		return PhysicalPlan{}, err
	}
	if !fhirschema.ResourceExists(semantic.Root.ResourceType) {
		return PhysicalPlan{}, fmt.Errorf("root resource type %q is not represented by the generated FHIR schema", semantic.Root.ResourceType)
	}
	if err := validateNavigationOnlyNode(semantic.Root); err != nil {
		return PhysicalPlan{}, err
	}

	physical := PhysicalPlan{
		Version: 1,
		Source: PhysicalSource{
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
		Operations: []PhysicalOperation{
			{
				Kind:     PhysicalRootScanOp,
				Source:   PhysicalSource{SemanticNode: semantic.Root.Alias, ResourceType: semantic.Root.ResourceType},
				RootScan: &PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"},
			},
		},
	}
	physical.Operations = appendProjectScope(physical.Operations, []string{"root"}, "", semantic.Root)
	physical.Operations = appendDatasetGenerationScope(physical.Operations, []string{"root"}, "", semantic.Root)
	physical.Operations = appendAuthScope(physical.Operations, []PhysicalValue{{Variable: "root", Path: []string{"auth_resource_path"}}}, "root_scope_allowed", semantic.Root)

	nextTraversal := 0
	var walk func(parent SemanticNode, parentVariable string) error
	walk = func(parent SemanticNode, parentVariable string) error {
		for _, child := range parent.Children {
			route, err := resolveStorageRoute(parent.ResourceType, child.EdgeLabel, child.ResourceType)
			if err != nil {
				return err
			}
			nextTraversal++
			nodeVariable := fmt.Sprintf("node_%d", nextTraversal)
			edgeVariable := fmt.Sprintf("edge_%d", nextTraversal)
			labelBind := fmt.Sprintf("traversal_%d_label", nextTraversal)
			typeBind := fmt.Sprintf("traversal_%d_target_type", nextTraversal)
			edgeCollectionBind := fmt.Sprintf("traversal_%d_edge_collection", nextTraversal)
			physical.BindVars[labelBind] = child.EdgeLabel
			physical.BindVars[typeBind] = child.ResourceType
			physical.BindVars[edgeCollectionBind] = "fhir_edge"
			source := PhysicalSource{SemanticNode: child.Alias, ResourceType: child.ResourceType, Relationship: child.EdgeLabel}
			physical.Operations = append(physical.Operations, PhysicalOperation{
				Kind:   PhysicalTraversalOp,
				Source: source,
				Traversal: &PhysicalTraversal{
					SourceVariable:        parentVariable,
					TargetVariable:        nodeVariable,
					EdgeVariable:          edgeVariable,
					Direction:             route.Direction,
					EdgeCollectionBindKey: edgeCollectionBind,
					EdgeLabelBindKey:      labelBind,
					TargetTypeBindKey:     typeBind,
					EdgeTargetTypeField:   route.targetEdgeTypeField(),
				},
			})
			// Edge metadata is not a substitute for the target resource's
			// tenant boundary. Scope both documents before any subsequent
			// traversal or return can observe the target node.
			physical.Operations = appendProjectScope(physical.Operations, []string{edgeVariable, nodeVariable}, child.EdgeLabel, child)
			physical.Operations = appendDatasetGenerationScope(physical.Operations, []string{edgeVariable, nodeVariable}, child.EdgeLabel, child)
			physical.Operations = appendAuthScope(physical.Operations, []PhysicalValue{
				{Variable: edgeVariable, Path: []string{"auth_resource_path"}},
				{Variable: nodeVariable, Path: []string{"auth_resource_path"}},
			}, fmt.Sprintf("traversal_%d_scope_allowed", nextTraversal), child)
			if err := walk(child, nodeVariable); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(semantic.Root, "root"); err != nil {
		return PhysicalPlan{}, err
	}
	physical.Operations = append(physical.Operations, PhysicalOperation{
		Kind:   PhysicalReturnOp,
		Source: PhysicalSource{SemanticNode: semantic.Root.Alias, ResourceType: semantic.Root.ResourceType, SemanticField: "_key"},
		Return: &PhysicalReturn{Projections: []PhysicalProjection{{Name: "_key", Value: PhysicalValue{Variable: "root", Path: []string{"_key"}}}}},
	})
	if err := physical.Validate(); err != nil {
		return PhysicalPlan{}, fmt.Errorf("validate generic physical plan: %w", err)
	}
	if err := ValidateGenericPhysicalPlanScope(physical); err != nil {
		return PhysicalPlan{}, fmt.Errorf("verify generic physical plan scope: %w", err)
	}
	return physical, nil
}

func appendProjectScope(operations []PhysicalOperation, variables []string, relationship string, node SemanticNode) []PhysicalOperation {
	right := PhysicalValue{BindKey: "project"}
	for _, variable := range variables {
		operations = append(operations, PhysicalOperation{
			Kind:   PhysicalFilterOp,
			Source: PhysicalSource{SemanticNode: node.Alias, ResourceType: node.ResourceType, Relationship: relationship, SemanticField: "project"},
			Filter: &PhysicalFilter{Predicate: PhysicalPredicate{
				Operator: "EQUALS",
				Left:     PhysicalValue{Variable: variable, Path: []string{"project"}},
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
func appendDatasetGenerationScope(operations []PhysicalOperation, variables []string, relationship string, node SemanticNode) []PhysicalOperation {
	right := PhysicalValue{BindKey: datasetGenerationBindKey}
	for _, variable := range variables {
		operations = append(operations, PhysicalOperation{
			Kind:   PhysicalFilterOp,
			Source: PhysicalSource{SemanticNode: node.Alias, ResourceType: node.ResourceType, Relationship: relationship, SemanticField: datasetGenerationField},
			Filter: &PhysicalFilter{Predicate: PhysicalPredicate{
				Operator: "EQUALS",
				Left:     PhysicalValue{Variable: variable, Path: []string{datasetGenerationField}},
				Right:    &right,
			}},
		})
	}
	return operations
}

func appendAuthScope(operations []PhysicalOperation, scopedValues []PhysicalValue, resultVariable string, node SemanticNode) []PhysicalOperation {
	inputs := append([]PhysicalValue(nil), scopedValues...)
	inputs = append(inputs, PhysicalValue{BindKey: "auth_resource_paths"}, PhysicalValue{BindKey: "auth_resource_paths_unrestricted"})
	operations = append(operations, PhysicalOperation{
		Kind:       PhysicalDerivedLetOp,
		Source:     PhysicalSource{SemanticNode: node.Alias, ResourceType: node.ResourceType, Relationship: node.EdgeLabel, SemanticField: "auth_resource_path"},
		DerivedLet: &PhysicalDerivedLet{Variable: resultVariable, Operator: "AUTH_RESOURCE_PATH_ALLOWED", Inputs: inputs},
	})
	right := PhysicalValue{BindKey: "scope_allowed"}
	return append(operations, PhysicalOperation{
		Kind:   PhysicalFilterOp,
		Source: PhysicalSource{SemanticNode: node.Alias, ResourceType: node.ResourceType, Relationship: node.EdgeLabel, SemanticField: "auth_resource_path"},
		Filter: &PhysicalFilter{Predicate: PhysicalPredicate{Operator: "EQUALS", Left: PhysicalValue{Variable: resultVariable}, Right: &right}},
	})
}

func validateNavigationOnlyNode(node SemanticNode) error {
	if node.MatchMode.required() {
		return fmt.Errorf("semantic node %q requires a relationship match, which is not supported by the navigation-only physical plan", node.Alias)
	}
	if len(node.Fields) != 0 || len(node.Filters) != 0 || len(node.Pivots) != 0 || len(node.Aggregates) != 0 || len(node.Slices) != 0 {
		return fmt.Errorf("semantic node %q contains selections or filters not supported by generic physical navigation", node.Alias)
	}
	for _, child := range node.Children {
		if err := validateNavigationOnlyNode(child); err != nil {
			return err
		}
	}
	return nil
}
