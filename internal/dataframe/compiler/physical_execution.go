package compiler

import "fmt"

const genericPhysicalExecutionLimitBind = "limit"

// compilePhysicalExecution renders a validated semantic physical plan. Its
// caller has already established that the plan represents the complete
// request, so it never needs compatibility named sets or string lowering.
func compilePhysicalExecution(physical PhysicalPlan, semantic SemanticPlan, limit int) (CompiledQuery, error) {
	var err error
	physical, err = withGenericPhysicalExecutionWindow(physical, limit)
	if err != nil {
		return CompiledQuery{}, err
	}
	rendered, err := RenderPhysicalPlan(physical)
	if err != nil {
		return CompiledQuery{}, fmt.Errorf("render generic physical execution plan: %w", err)
	}

	columns, pivotFields := physicalProjectionMetadata(physical)
	return CompiledQuery{
		Project:           semantic.Project,
		DatasetGeneration: normalizeDatasetGeneration(semantic.DatasetGeneration),
		RootResourceType:  semantic.Root.ResourceType,
		AuthResourcePaths: cloneStrings(semantic.AuthResourcePaths),
		PlanMode:          "physical",
		PlanProfile:       "generic_fhir_graph",
		TraversalCount:    physicalTraversalCount(physical),
		OptimizationRules: physicalOptimizationRules(semantic.Root),
		RowIdentity:       cloneRowIdentity(semantic.RowIdentity),
		Query:             rendered.Query,
		BindVars:          rendered.BindVars,
		Columns:           columns,
		PivotFields:       pivotFields,
		Limit:             limit,
		PlanDiagnostics:   physicalPlanDiagnostics(physical),
	}, nil
}

func physicalOptimizationRules(node SemanticNode) []string {
	rules := make([]string, 0, 2)
	if len(node.Filters) != 0 {
		rules = append(rules, OptimizerRuleFilterPushdown)
	}
	for _, child := range node.Children {
		if child.MatchMode.required() {
			rules = appendUniqueRule(rules, OptimizerRuleRelationshipSemiJoin)
		}
		for _, rule := range physicalOptimizationRules(child) {
			rules = appendUniqueRule(rules, rule)
		}
	}
	if len(rules) == 0 {
		return nil
	}
	return rules
}

func physicalProjectionMetadata(plan PhysicalPlan) ([]string, []string) {
	for _, operation := range plan.Operations {
		if operation.Kind != PhysicalReturnOp || operation.Return == nil {
			continue
		}
		columns := make([]string, 0, len(operation.Return.Projections))
		var pivots []string
		for _, projection := range operation.Return.Projections {
			columns = append(columns, projection.Name)
			if projection.Expression != nil && projection.Expression.Kind == PhysicalPivotExpression {
				pivots = append(pivots, projection.Name)
			}
		}
		return columns, pivots
	}
	return nil, nil
}

func physicalTraversalCount(plan PhysicalPlan) int {
	count := 0
	for _, operation := range plan.Operations {
		if operation.Kind == PhysicalTraversalOp {
			count++
		}
	}
	return count
}

// withGenericPhysicalExecutionWindow inserts the deterministic root ordering
// and optional preview bound before any traversal LET subquery, ensuring an
// expensive optional navigation is evaluated only for selected root rows.
func withGenericPhysicalExecutionWindow(plan PhysicalPlan, limit int) (PhysicalPlan, error) {
	if err := ValidateGenericPhysicalPlanScope(plan); err != nil {
		return PhysicalPlan{}, fmt.Errorf("validate generic physical execution scope: %w", err)
	}
	if len(plan.Operations) == 0 || plan.Operations[0].Kind != PhysicalRootScanOp || plan.Operations[0].RootScan == nil {
		return PhysicalPlan{}, fmt.Errorf("generic physical execution plan requires a root scan")
	}

	// The generic scope verifier defines the root scope as every operation up
	// to the first traversal or terminal return. BuildGenericPhysicalPlan has
	// already proven that the whole prefix scopes the root correctly.
	insertAt := physicalScopeWindowEnd(plan.Operations, 1)
	if insertAt <= 1 || insertAt >= len(plan.Operations) {
		return PhysicalPlan{}, fmt.Errorf("generic physical execution plan requires a scoped root followed by RETURN or traversal")
	}

	out := clonePhysicalPlan(plan)
	root := out.Operations[0].RootScan.Variable
	window := []PhysicalOperation{{
		Kind:   PhysicalSortOp,
		Source: PhysicalSource{SemanticNode: out.Source.SemanticNode, ResourceType: out.Source.ResourceType, SemanticField: "_key"},
		Sort:   &PhysicalSort{Value: PhysicalValue{Variable: root, Path: []string{"_key"}}},
	}}
	if limit > 0 {
		if _, exists := out.BindVars[genericPhysicalExecutionLimitBind]; exists {
			return PhysicalPlan{}, fmt.Errorf("generic physical execution limit bind %q is already defined", genericPhysicalExecutionLimitBind)
		}
		out.BindVars[genericPhysicalExecutionLimitBind] = limit
		window = append(window, PhysicalOperation{
			Kind:   PhysicalLimitOp,
			Source: PhysicalSource{SemanticNode: out.Source.SemanticNode, ResourceType: out.Source.ResourceType},
			Limit:  &PhysicalLimit{BindKey: genericPhysicalExecutionLimitBind},
		})
	}
	operations := make([]PhysicalOperation, 0, len(out.Operations)+len(window))
	operations = append(operations, out.Operations[:insertAt]...)
	operations = append(operations, window...)
	operations = append(operations, out.Operations[insertAt:]...)
	out.Operations = operations
	if err := ValidateGenericPhysicalPlanScope(out); err != nil {
		return PhysicalPlan{}, fmt.Errorf("validate generic physical execution window: %w", err)
	}
	return out, nil
}
