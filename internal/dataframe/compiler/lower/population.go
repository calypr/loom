package lower

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

const (
	populationSelectionIDBindKey       = "population_selection_id"
	populationProjectBindKey           = "population_project"
	populationResourceTypeBindKey      = "population_resource_type"
	populationMembersCollectionBindKey = "population_members_collection"
	populationSourceCollectionBindKey  = "population_source_collection"
)

// configurePopulationRootSource changes the root access path from a complete
// resource collection scan to an indexed selection-member scan. It joins the
// selected FHIR resources by id, then traverses the population route in
// reverse until it reaches the dataframe root type.
func configurePopulationRootSource(physical *ir.PhysicalPlan, output semantic.SemanticNode, population *semantic.SemanticPopulation, context semantic.ExecutionContext, policy ir.PhysicalOptimizationPolicy) error {
	if population == nil {
		return nil
	}
	if context.SelectionMembersCollection == "" {
		return fmt.Errorf("population selection members collection binding is required")
	}
	if context.SelectionProject == "" {
		return fmt.Errorf("population selection project binding is required")
	}
	physical.BindVars[populationSelectionIDBindKey] = population.SelectionRevisionID
	physical.BindVars[populationProjectBindKey] = context.SelectionProject
	physical.BindVars[populationResourceTypeBindKey] = population.ResourceType
	physical.BindVars[populationMembersCollectionBindKey] = context.SelectionMembersCollection
	physical.BindVars[populationSourceCollectionBindKey] = population.ResourceType

	memberVariable := "population_member"
	sourceVariable := "population_source"
	source := &ir.PhysicalPopulationRootSource{
		MemberScan: ir.PhysicalCollectionScan{Variable: memberVariable, CollectionBindKey: populationMembersCollectionBindKey},
		MemberID:   ir.PhysicalValue{Variable: memberVariable, Path: []string{"id"}},
	}
	memberFilters := []struct {
		field string
		right ir.PhysicalValue
	}{
		{"selectionId", ir.PhysicalValue{BindKey: populationSelectionIDBindKey}},
		{"project", ir.PhysicalValue{BindKey: populationProjectBindKey}},
		{"generation", ir.PhysicalValue{BindKey: "dataset_generation"}},
		{"resourceType", ir.PhysicalValue{BindKey: populationResourceTypeBindKey}},
	}
	for _, filter := range memberFilters {
		right := filter.right
		source.MemberFilters = append(source.MemberFilters, ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS", Left: ir.PhysicalValue{Variable: memberVariable, Path: []string{filter.field}}, Right: &right}})
	}

	populationNode := semantic.SemanticNode{Alias: "population", ResourceType: population.ResourceType}
	source.ResourceOperations = append(source.ResourceOperations, ir.PhysicalOperation{Kind: ir.PhysicalCollectionScanOp, Source: ir.PhysicalSource{SemanticNode: "population", ResourceType: population.ResourceType}, CollectionScan: &ir.PhysicalCollectionScan{Variable: sourceVariable, CollectionBindKey: populationSourceCollectionBindKey}})
	source.ResourceOperations = appendProjectScope(source.ResourceOperations, []string{sourceVariable}, "", populationNode)
	source.ResourceOperations = appendDatasetGenerationScope(source.ResourceOperations, []string{sourceVariable}, "", populationNode)
	source.ResourceOperations = appendAuthScope(source.ResourceOperations, []ir.PhysicalValue{{Variable: sourceVariable, Path: []string{"auth_resource_path"}}}, "population_source_scope_allowed", populationNode)
	memberID := ir.PhysicalValue{Variable: memberVariable, Path: []string{"id"}}
	source.ResourceOperations = append(source.ResourceOperations, ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS", Left: ir.PhysicalValue{Variable: sourceVariable, Path: []string{"id"}}, Right: &memberID}}})

	types := make([]string, len(population.Route)+1)
	types[0] = output.ResourceType
	for index, step := range population.Route {
		types[index+1] = step.ResourceType
	}
	if types[len(types)-1] != population.ResourceType {
		return fmt.Errorf("population route ends at %q, not selected resource type %q", types[len(types)-1], population.ResourceType)
	}
	terminalVariable := sourceVariable
	for index := len(population.Route) - 1; index >= 0; index-- {
		step := population.Route[index]
		targetVariable := fmt.Sprintf("population_root_node_%d", index)
		edgeVariable := fmt.Sprintf("population_root_edge_%d", index)
		result, err := BuildPhysicalTraversal(TraversalLoweringRequest{
			FromType: types[index+1], EdgeLabel: step.Relationship, ToType: types[index],
			SourceVariable: terminalVariable, TargetVariable: targetVariable, EdgeVariable: edgeVariable,
			BindPrefix: fmt.Sprintf("population_root_route_%d", index), Policy: policy,
		})
		if err != nil {
			return fmt.Errorf("reverse population route step %d: %w", index, err)
		}
		for key, value := range result.BindVars {
			physical.BindVars[key] = value
		}
		node := semantic.SemanticNode{Alias: "population", ResourceType: types[index], EdgeLabel: step.Relationship}
		source.ResourceOperations = append(source.ResourceOperations, ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Source: ir.PhysicalSource{SemanticNode: "population", ResourceType: types[index], Relationship: step.Relationship}, Traversal: &result.Traversal})
		source.ResourceOperations = appendProjectScope(source.ResourceOperations, []string{edgeVariable, targetVariable}, step.Relationship, node)
		source.ResourceOperations = appendDatasetGenerationScope(source.ResourceOperations, []string{edgeVariable, targetVariable}, step.Relationship, node)
		source.ResourceOperations = appendAuthScope(source.ResourceOperations, []ir.PhysicalValue{{Variable: edgeVariable, Path: []string{"auth_resource_path"}}, {Variable: targetVariable, Path: []string{"auth_resource_path"}}}, fmt.Sprintf("population_root_route_%d_scope_allowed", index), node)
		terminalVariable = targetVariable
	}
	source.RootKey = ir.PhysicalValue{Variable: terminalVariable, Path: []string{"_key"}}
	physical.Operations[0].RootScan.Population = source
	return nil
}
