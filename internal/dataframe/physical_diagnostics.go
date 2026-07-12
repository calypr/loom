package dataframe

import "sort"

// CompilerPlanDiagnostics describes work the physical renderer will ask AQL to
// perform. It deliberately reports compiler facts, not estimated database
// cost: use it alongside Arango PROFILE to decide which rewrite is worthwhile.
type CompilerPlanDiagnostics struct {
	TraversalSets                     int
	SharedTraversalCount              int
	RequiredMatchReuseCount           int
	ScopedSharingCandidateGroups      int
	ScopedSharingCandidateSets        int
	PotentialSharingOpportunityGroups int
	PotentialSharingOpportunitySets   int
	RichSourceReuse                   []RichSourceReuse
	// OptimizationPolicy is the explainable decision record for optional
	// physical rewrites. It reports both enabled rewrites and conservative
	// rejections so rendered AQL is never the only evidence of a decision.
	OptimizationPolicy PhysicalOptimizationReport
}

// RichSourceReuse identifies a materialized relationship set that is scanned
// repeatedly by rich projections. A high count does not mean the traversal is
// repeated; it means aggregate/pivot/slice operations each loop over the same
// already-materialized child set.
type RichSourceReuse struct {
	SourceSet          string
	AggregateConsumers int
	PivotConsumers     int
	SliceConsumers     int
}

func (r RichSourceReuse) TotalConsumers() int {
	return r.AggregateConsumers + r.PivotConsumers + r.SliceConsumers
}

func physicalPlanDiagnostics(plan PhysicalPlan) CompilerPlanDiagnostics {
	diagnostics := CompilerPlanDiagnostics{
		SharedTraversalCount:    plan.SharedTraversalCount,
		RequiredMatchReuseCount: plan.RequiredMatchReuseCount,
		OptimizationPolicy:      clonePhysicalOptimizationReport(plan.OptimizationPolicy),
	}
	groups := map[string][]int{}
	potentialGroups := map[string][]int{}
	for i, operation := range plan.Operations {
		if operation.Kind != PhysicalSetOp || operation.Set == nil {
			continue
		}
		set := operation.Set
		if set.SourceSetVariable == "" && len(set.Subplan.Operations) > 0 && set.Subplan.Operations[0].Traversal != nil {
			diagnostics.TraversalSets++
			key := physicalTraversalOpportunityKey(plan, *set)
			potentialGroups[key] = append(potentialGroups[key], i)
			if decomposition, err := DecomposePhysicalTraversalPrefix(plan, *set); err == nil {
				groups[decomposition.PrefixKey] = append(groups[decomposition.PrefixKey], i)
			}
		}
	}
	for _, indices := range potentialGroups {
		if len(indices) < 2 || !multipleTargetTypes(plan, indices) {
			continue
		}
		diagnostics.PotentialSharingOpportunityGroups++
		diagnostics.PotentialSharingOpportunitySets += len(indices)
	}
	for _, indices := range groups {
		if len(indices) < 2 || !multipleTargetTypes(plan, indices) {
			continue
		}
		diagnostics.ScopedSharingCandidateGroups++
		diagnostics.ScopedSharingCandidateSets += len(indices)
	}

	uses := map[string]*RichSourceReuse{}
	for _, operation := range plan.Operations {
		if operation.Kind != PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			countRichSourceReuse(projection.Expression, uses)
		}
	}
	for _, use := range uses {
		if use.TotalConsumers() > 1 {
			diagnostics.RichSourceReuse = append(diagnostics.RichSourceReuse, *use)
		}
	}
	sort.Slice(diagnostics.RichSourceReuse, func(i, j int) bool {
		return diagnostics.RichSourceReuse[i].SourceSet < diagnostics.RichSourceReuse[j].SourceSet
	})
	return diagnostics
}

// physicalTraversalOpportunityKey intentionally ignores scoped filters and
// semantic provenance. It identifies broader neighbor traversals that could
// serve multiple typed children once the scope-safe rewrite exists.
func physicalTraversalOpportunityKey(plan PhysicalPlan, set PhysicalSet) string {
	traversal := set.Subplan.Operations[0].Traversal
	if traversal == nil {
		return ""
	}
	return traversal.SourceVariable + "|" + string(traversal.Direction) + "|" + valueString(plan.BindVars[traversal.EdgeCollectionBindKey]) + "|" + valueString(plan.BindVars[traversal.EdgeLabelBindKey]) + "|" + traversal.EdgeTargetTypeField
}

func multipleTargetTypes(plan PhysicalPlan, indices []int) bool {
	types := map[string]bool{}
	for _, index := range indices {
		traversal := plan.Operations[index].Set.Subplan.Operations[0].Traversal
		if traversal == nil {
			continue
		}
		if value, ok := plan.BindVars[traversal.TargetTypeBindKey].(string); ok {
			types[value] = true
		}
	}
	return len(types) > 1
}

func countRichSourceReuse(expression *PhysicalExpression, uses map[string]*RichSourceReuse) {
	if expression == nil {
		return
	}
	add := func(source string, kind PhysicalExpressionKind) {
		if source == "" {
			return
		}
		use := uses[source]
		if use == nil {
			use = &RichSourceReuse{SourceSet: source}
			uses[source] = use
		}
		switch kind {
		case PhysicalAggregateExpression:
			use.AggregateConsumers++
		case PhysicalPivotExpression:
			use.PivotConsumers++
		case PhysicalSliceExpression:
			use.SliceConsumers++
		}
	}
	switch expression.Kind {
	case PhysicalAggregateExpression:
		if expression.Aggregate != nil {
			add(expression.Aggregate.Source.Variable, expression.Kind)
		}
	case PhysicalPivotExpression:
		if expression.Pivot != nil {
			add(expression.Pivot.Source.Variable, expression.Kind)
		}
	case PhysicalSliceExpression:
		if expression.Slice != nil {
			add(expression.Slice.Source.Variable, expression.Kind)
		}
	case PhysicalObjectExpression:
		if expression.Object != nil {
			for _, field := range expression.Object.Fields {
				field := field.Expression
				countRichSourceReuse(&field, uses)
			}
		}
	}
}
