package dataframe

import (
	"encoding/json"
	"sort"
)

// CompilerPlanDiagnostics describes work the physical renderer will ask AQL to
// perform. It deliberately reports compiler facts, not estimated database
// cost: use it alongside Arango PROFILE to decide which rewrite is worthwhile.
type CompilerPlanDiagnostics struct {
	TraversalSets                     int
	EndpointTraversalCount            int
	NativeTraversalCount              int
	TraversalStrategies               []PhysicalTraversalDecision
	SharedTraversalCount              int
	RequiredMatchReuseCount           int
	ScopedSharingCandidateGroups      int
	ScopedSharingCandidateSets        int
	PotentialSharingOpportunityGroups int
	PotentialSharingOpportunitySets   int
	RichSourceReuse                   []RichSourceReuse
	RichConsumerGroups                []RichConsumerGroup
	// OptimizationPolicy is the explainable decision record for optional
	// physical rewrites. It reports both enabled rewrites and conservative
	// rejections so rendered AQL is never the only evidence of a decision.
	OptimizationPolicy PhysicalOptimizationReport
}

// PhysicalTraversalDecision exposes the route strategy selected by the typed
// physical compiler. It is deliberately metadata-only: no user values or raw
// AQL are included. Native decisions explain whether endpoint lowering was
// disabled by policy or rejected because its validated storage contract was
// unavailable.
type PhysicalTraversalDecision struct {
	SourceVariable      string
	TargetVariable      string
	Direction           PhysicalTraversalDirection
	Strategy            PhysicalTraversalStrategy
	EndpointField       string
	EndpointJoinField   string
	EndpointIndexFields []string
	Relationship        string
	TargetResourceType  string
	Reason              string
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

// RichConsumerGroup is a renderer-independent compatibility classification
// for rich expressions over one materialized set. A group is eligible only
// when its complete semantic expression signature is identical; source reuse
// alone is not enough to fuse predicates, ordering, pivot reduction, or slice
// limits safely.
type RichConsumerGroup struct {
	SourceSet string
	Kind      PhysicalExpressionKind
	Signature string
	Consumers int
	Eligible  bool
	Reason    string
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
	endpointPolicyEnabled := false
	for _, state := range plan.OptimizationPolicy.RuleStates {
		if state.Rule == PhysicalOptimizationRuleEndpointTraversal {
			endpointPolicyEnabled = state.Enabled
			break
		}
	}
	groups := map[string][]int{}
	potentialGroups := map[string][]int{}
	for i, operation := range plan.Operations {
		if operation.Kind == PhysicalTraversalOp && operation.Traversal != nil {
			traversal := operation.Traversal
			strategy := traversal.Strategy
			if strategy == "" {
				strategy = PhysicalTraversalNative
			}
			decision := PhysicalTraversalDecision{
				SourceVariable: traversal.SourceVariable,
				TargetVariable: traversal.TargetVariable,
				Direction: traversal.Direction,
				Strategy: strategy,
				EndpointField: traversal.EndpointField,
				EndpointJoinField: traversal.EndpointJoinField,
				EndpointIndexFields: append([]string(nil), traversal.EndpointIndexFields...),
				Relationship: operation.Source.Relationship,
				TargetResourceType: operation.Source.ResourceType,
			}
			if strategy == PhysicalTraversalEndpointLookup {
				decision.Reason = "endpoint lookup enabled by validated storage-route and index contract"
				diagnostics.EndpointTraversalCount++
			} else {
				diagnostics.NativeTraversalCount++
				if !endpointPolicyEnabled {
					decision.Reason = "endpoint lookup disabled by optimization policy"
				} else {
					decision.Reason = "endpoint contract unavailable; native traversal fallback"
				}
			}
			diagnostics.TraversalStrategies = append(diagnostics.TraversalStrategies, decision)
		}
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
	consumerGroups := map[string]*RichConsumerGroup{}
	for _, operation := range plan.Operations {
		if operation.Kind != PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			collectRichConsumerGroups(projection.Expression, consumerGroups)
		}
	}
	groupKeys := make([]string, 0, len(consumerGroups))
	for key := range consumerGroups {
		groupKeys = append(groupKeys, key)
	}
	sort.Strings(groupKeys)
	for _, key := range groupKeys {
		group := *consumerGroups[key]
		if group.Consumers > 1 {
			group.Eligible = true
			group.Reason = "identical source, operation, and semantic expression"
		} else {
			group.Reason = "single consumer"
		}
		diagnostics.RichConsumerGroups = append(diagnostics.RichConsumerGroups, group)
	}
	return diagnostics
}

func collectRichConsumerGroups(expression *PhysicalExpression, groups map[string]*RichConsumerGroup) {
	if expression == nil {
		return
	}
	var source string
	switch expression.Kind {
	case PhysicalAggregateExpression:
		if expression.Aggregate != nil {
			source = expression.Aggregate.Source.Variable
		}
	case PhysicalPivotExpression:
		if expression.Pivot != nil {
			source = expression.Pivot.Source.Variable
		}
	case PhysicalSliceExpression:
		if expression.Slice != nil {
			source = expression.Slice.Source.Variable
		}
	case PhysicalObjectExpression:
		if expression.Object != nil {
			for index := range expression.Object.Fields {
				collectRichConsumerGroups(&expression.Object.Fields[index].Expression, groups)
			}
		}
		return
	}
	if source == "" {
		return
	}
	payload, err := json.Marshal(expression)
	if err != nil {
		return
	}
	signature := string(payload)
	key := source + "\x00" + string(expression.Kind) + "\x00" + signature
	if group := groups[key]; group != nil {
		group.Consumers++
		return
	}
	groups[key] = &RichConsumerGroup{SourceSet: source, Kind: expression.Kind, Signature: signature, Consumers: 1}
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
