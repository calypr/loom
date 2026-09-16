package lower

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

const (
	populationSelectionIDBindKey       = "population_selection_id"
	populationResourceTypeBindKey      = "population_resource_type"
	populationMembersCollectionBindKey = "population_members_collection"
	populationResultBindKey            = "population_match"
)

// appendPopulationSemijoin adds a pre-window root membership predicate. The
// root scan remains the only top-level scan; route traversal and the indexed
// selection-members scan are correlated inside an EXISTS subplan.
func appendPopulationSemijoin(physical *ir.PhysicalPlan, output semantic.SemanticNode, population *semantic.SemanticPopulation, context semantic.ExecutionContext) error {
	if population == nil {
		return nil
	}
	if context.SelectionMembersCollection == "" {
		return fmt.Errorf("population selection members collection binding is required")
	}
	physical.BindVars[populationSelectionIDBindKey] = population.SelectionRevisionID
	physical.BindVars[populationResourceTypeBindKey] = population.ResourceType
	physical.BindVars[populationMembersCollectionBindKey] = context.SelectionMembersCollection
	physical.BindVars[populationResultBindKey] = 1

	subplan := ir.PhysicalSubplan{Captures: []string{"root"}}
	parentVariable, parentType := "root", output.ResourceType
	terminalVariable := parentVariable
	for index, step := range population.Route {
		result, err := BuildPhysicalTraversal(TraversalLoweringRequest{
			FromType: parentType, EdgeLabel: step.Relationship, ToType: step.ResourceType,
			SourceVariable: parentVariable, TargetVariable: fmt.Sprintf("population_node_%d", index), EdgeVariable: fmt.Sprintf("population_edge_%d", index),
			BindPrefix: fmt.Sprintf("population_route_%d", index), Policy: ir.PhysicalOptimizationPolicy{},
		})
		if err != nil {
			return fmt.Errorf("population route step %d: %w", index, err)
		}
		for key, value := range result.BindVars {
			physical.BindVars[key] = value
		}
		subplan.Operations = append(subplan.Operations, ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Source: ir.PhysicalSource{SemanticNode: "population", ResourceType: step.ResourceType, Relationship: step.Relationship}, Traversal: &result.Traversal})
		subplan.Operations = appendProjectScope(subplan.Operations, []string{result.Traversal.EdgeVariable, result.Traversal.TargetVariable}, step.Relationship, semantic.SemanticNode{Alias: "population", ResourceType: step.ResourceType, EdgeLabel: step.Relationship})
		subplan.Operations = appendDatasetGenerationScope(subplan.Operations, []string{result.Traversal.EdgeVariable, result.Traversal.TargetVariable}, step.Relationship, semantic.SemanticNode{Alias: "population", ResourceType: step.ResourceType, EdgeLabel: step.Relationship})
		subplan.Operations = appendAuthScope(subplan.Operations, []ir.PhysicalValue{{Variable: result.Traversal.EdgeVariable, Path: []string{"auth_resource_path"}}, {Variable: result.Traversal.TargetVariable, Path: []string{"auth_resource_path"}}}, fmt.Sprintf("population_route_%d_scope_allowed", index), semantic.SemanticNode{Alias: "population", ResourceType: step.ResourceType, EdgeLabel: step.Relationship})
		parentVariable, parentType = result.Traversal.TargetVariable, step.ResourceType
		terminalVariable = parentVariable
	}

	memberVariable := "population_member"
	subplan.Operations = append(subplan.Operations, ir.PhysicalOperation{Kind: ir.PhysicalCollectionScanOp, Source: ir.PhysicalSource{SemanticNode: "population_members", ResourceType: population.ResourceType}, CollectionScan: &ir.PhysicalCollectionScan{Variable: memberVariable, CollectionBindKey: populationMembersCollectionBindKey}})
	memberFilters := []struct {
		field string
		right ir.PhysicalValue
	}{
		{"selectionId", ir.PhysicalValue{BindKey: populationSelectionIDBindKey}},
		{"project", ir.PhysicalValue{BindKey: "project"}},
		{"generation", ir.PhysicalValue{BindKey: "dataset_generation"}},
		{"resourceType", ir.PhysicalValue{BindKey: populationResourceTypeBindKey}},
	}
	for _, filter := range memberFilters {
		subplan.Operations = append(subplan.Operations, ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS", Left: ir.PhysicalValue{Variable: memberVariable, Path: []string{filter.field}}, Right: &filter.right}}})
	}
	terminalID := ir.PhysicalValue{Variable: terminalVariable, Path: []string{"id"}}
	subplan.Operations = append(subplan.Operations, ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS", Left: ir.PhysicalValue{Variable: memberVariable, Path: []string{"id"}}, Right: &terminalID}}})
	subplan.Return = ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{BindKey: populationResultBindKey}}
	physical.Operations = append(physical.Operations, ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Source: ir.PhysicalSource{SemanticNode: "population", ResourceType: output.ResourceType}, Filter: &ir.PhysicalFilter{Expression: &ir.PhysicalPredicateExpression{Kind: ir.PhysicalExistsPredicate, Exists: &subplan}}})
	return nil
}
