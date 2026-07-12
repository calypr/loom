package dataframe

import "fmt"

const genericPhysicalExecutionLimitBind = "limit"

// compileGenericPhysicalExecution is the executable bridge from the generic
// semantic graph to the typed physical renderer. Its caller has already
// established that the request is navigation-only, so the physical return
// shape (_key at the root row grain) is exactly the public dataframe shape.
func compileGenericPhysicalExecution(semantic SemanticPlan, lowered Builder, limit int) (CompiledQuery, error) {
	physical, err := BuildGenericPhysicalPlan(semantic)
	if err != nil {
		return CompiledQuery{}, fmt.Errorf("build generic physical execution plan: %w", err)
	}
	physical, err = withGenericPhysicalExecutionWindow(physical, limit)
	if err != nil {
		return CompiledQuery{}, err
	}
	rendered, err := RenderPhysicalPlan(physical)
	if err != nil {
		return CompiledQuery{}, fmt.Errorf("render generic physical execution plan: %w", err)
	}

	return CompiledQuery{
		Project:           semantic.Project,
		DatasetGeneration: normalizeDatasetGeneration(semantic.DatasetGeneration),
		RootResourceType:  semantic.Root.ResourceType,
		AuthResourcePaths: cloneStrings(semantic.AuthResourcePaths),
		PlanMode:          planMode(lowered.PlanHint),
		PlanProfile:       planProfile(lowered.PlanHint),
		NamedSetCount:     planNamedSetCount(lowered.PlanHint),
		FileSummaries:     planFileSummaries(lowered.PlanHint),
		StudyLookup:       planStudyLookup(lowered.PlanHint),
		OptimizationRules: planAppliedRules(lowered.PlanHint),
		RowIdentity:       planRowIdentity(lowered.PlanHint),
		Query:             rendered.Query,
		BindVars:          rendered.BindVars,
		Columns:           []string{"_key"},
		PivotFields:       nil,
		Limit:             limit,
	}, nil
}

// withGenericPhysicalExecutionWindow inserts the deterministic root ordering
// and optional preview bound before any traversal LET subquery. This matches
// the established lowered renderer's row-grain semantics while ensuring an
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

	out := cloneCompilerPhysicalPlan(plan)
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
