package optimize

import (
	"fmt"
	"sort"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

const OptimizerRuleTraversalSharing = "share_identical_traversals"

func OptimizePhysicalPlanWithPolicy(plan ir.PhysicalPlan, policy ir.PhysicalOptimizationPolicy) (ir.PhysicalPlan, error) {
	if err := plan.Validate(); err != nil {
		return ir.PhysicalPlan{}, fmt.Errorf("validate physical plan: %w", err)
	}
	out := ir.ClonePhysicalPlan(plan)
	out.OptimizationPolicy = ir.NewPhysicalOptimizationReport(policy)
	groups := map[string][]int{}
	decompositions := map[int]ir.PhysicalTraversalPrefixDecomposition{}
	for i, op := range out.Operations {
		if op.Kind != ir.PhysicalSetOp || op.Set == nil || op.Set.SourceSetVariable != "" {
			continue
		}
		decomposition, err := ir.DecomposePhysicalTraversalPrefixAt(out, *op.Set, i)
		if err != nil {

			continue
		}
		decompositions[i] = decomposition
		groups[decomposition.PrefixKey] = append(groups[decomposition.PrefixKey], i)
	}
	groupKeys := make([]string, 0, len(groups))
	for key := range groups {
		groupKeys = append(groupKeys, key)
	}
	sort.Strings(groupKeys)
	for _, key := range groupKeys {
		indices := groups[key]
		if len(indices) < 2 {
			continue
		}
		decision := ir.PhysicalOptimizationDecision{
			Rule:          OptimizerRuleTraversalSharing,
			CandidateSets: len(indices),
		}

		types := map[string]bool{}
		for _, index := range indices {
			t := decompositions[index].Subset.TargetTypeBindKey
			if value, ok := out.BindVars[t].(string); ok {
				types[value] = true
			}
		}
		if len(types) < 2 {
			decision.Reason = "candidate group has fewer than two target resource types"
			out.OptimizationPolicy.AddDecision(decision)
			continue
		}
		baseline, optimized, savings := ir.EstimateTraversalSharingWork(decompositions[indices[0]], len(indices))
		decision.EstimatedBaselineWork = baseline
		decision.EstimatedOptimizedWork = optimized
		decision.EstimatedSavings = savings
		if !policy.RuleEnabled(ir.PhysicalOptimizationRuleTraversalSharing) {
			if !policy.Enabled {
				decision.Reason = "cost policy disabled"
			} else {
				decision.Reason = "traversal sharing rule disabled"
			}
			out.OptimizationPolicy.AddDecision(decision)
			continue
		}
		if savings < policy.MinimumSavings {
			decision.Reason = fmt.Sprintf("estimated savings %d is below policy minimum %d", savings, policy.MinimumSavings)
			out.OptimizationPolicy.AddDecision(decision)
			continue
		}
		if err := sharePhysicalSetGroup(&out, indices, types, decompositions, policy); err != nil {
			decision.Reason = "rewrite rejected: " + err.Error()
			out.OptimizationPolicy.AddDecision(decision)
			continue
		}
		decision.Enabled = true
		decision.Reason = "estimated prefix work reduction exceeds policy minimum"
		out.OptimizationPolicy.AddDecision(decision)
	}
	if err := out.Validate(); err != nil {
		return ir.PhysicalPlan{}, fmt.Errorf("validate optimized physical plan: %w", err)
	}
	return out, nil
}

func sanitizeColumnName(in string) string {
	out := make([]rune, 0, len(in))
	for _, r := range in {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			out = append(out, r)
		} else {
			out = append(out, '_')
		}
	}
	return string(out)
}

type storageRoute struct{ Direction ir.PhysicalTraversalDirection }

func (route storageRoute) endpointLookupFields() (string, string, []string, bool) {
	switch route.Direction {
	case ir.PhysicalInbound:
		return "_to", "_from", []string{"_to", "project", "dataset_generation", "label", "from_type"}, true
	case ir.PhysicalOutbound:
		return "_from", "_to", []string{"_from", "project", "dataset_generation", "label", "to_type"}, true
	default:
		return "", "", nil, false
	}
}

func sharePhysicalSetGroup(plan *ir.PhysicalPlan, indices []int, types map[string]bool, decompositions map[int]ir.PhysicalTraversalPrefixDecomposition, policy ir.PhysicalOptimizationPolicy) error {
	first := indices[0]

	originalOperation := ir.ClonePhysicalOperation(plan.Operations[first])
	original := *originalOperation.Set
	t := *original.Subplan.Operations[0].Traversal
	base := original
	baseName := "shared_" + sanitizeColumnName(t.SourceVariable) + "_" + sanitizeColumnName(valueString(plan.BindVars[t.EdgeLabelBindKey])) + "_neighbors"
	used := map[string]bool{}
	for _, op := range plan.Operations {
		if op.Set != nil {
			used[op.Set.Variable] = true
		}
	}
	for n := 2; used[baseName]; n++ {
		baseName = fmt.Sprintf("%s_%d", baseName, n)
	}
	typesKey := baseName + "_target_types"
	targetTypes := make([]string, 0, len(types))
	for typ := range types {
		targetTypes = append(targetTypes, typ)
	}

	for i := 0; i < len(targetTypes); i++ {
		for j := i + 1; j < len(targetTypes); j++ {
			if targetTypes[j] < targetTypes[i] {
				targetTypes[i], targetTypes[j] = targetTypes[j], targetTypes[i]
			}
		}
	}
	plan.BindVars[typesKey] = targetTypes
	t.TargetTypeBindKey = typesKey
	if policy.RuleEnabled(ir.PhysicalOptimizationRuleEndpointTraversal) {
		route := storageRoute{Direction: t.Direction}
		if endpoint, join, fields, ok := route.endpointLookupFields(); ok {
			t.Strategy = ir.PhysicalTraversalEndpointLookup
			t.EndpointField, t.EndpointJoinField = endpoint, join
			t.EndpointIndexFields = append([]string(nil), fields...)
		}
	}
	decomposition, ok := decompositions[first]
	if !ok || len(decomposition.PrefixOperations) == 0 {
		return fmt.Errorf("missing traversal prefix decomposition for set %d", first)
	}
	base.Subplan.Operations = ir.ClonePhysicalOperations(decomposition.PrefixOperations)

	base.Subplan.Operations[0].Traversal = &t
	base.Variable = baseName
	base.SourceSetVariable, base.ItemVariable = "", ""

	base.Output = nil

	base.Prepared = nil

	base.Projection = nil

	base.Reduction = nil
	base.Subplan.Return = ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{Variable: t.TargetVariable}}

	indexSet := map[int]bool{}
	for _, i := range indices {
		indexSet[i] = true
	}
	newOps := make([]ir.PhysicalOperation, 0, len(plan.Operations)+1)
	for i, op := range plan.Operations {
		if i == first {
			newOps = append(newOps, ir.PhysicalOperation{Kind: ir.PhysicalSetOp, Source: plan.Operations[first].Source, Set: &base})
		}
		if !indexSet[i] {
			newOps = append(newOps, op)
			continue
		}
		set := *op.Set
		decomposition, found := decompositions[i]
		if !found {
			return fmt.Errorf("shared set %q has no validated traversal-prefix decomposition", set.Variable)
		}
		origTraversal := *set.Subplan.Operations[0].Traversal
		item := set.Variable + "_item"
		typeKey := origTraversal.TargetTypeBindKey
		sub := ir.PhysicalSubplan{Captures: []string{baseName}, Return: ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{Variable: item}}}
		sub.Operations = append(sub.Operations, ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS", Left: ir.PhysicalValue{Variable: item, Path: []string{"resourceType"}}, Right: &ir.PhysicalValue{BindKey: typeKey}}}})
		for _, child := range decomposition.Subset.ConsumerOperations {
			rewritten := rewritePhysicalOperationVariables(child, origTraversal.TargetVariable, item, origTraversal.EdgeVariable, item)
			sub.Operations = append(sub.Operations, rewritten)
		}
		set.SourceSetVariable, set.ItemVariable, set.Subplan = baseName, item, sub
		set.Unique, set.SortByKey = true, true
		if set.Prepared != nil {

			prepared := *set.Prepared
			prepared.SourceSetVariable = set.Variable
			set.Prepared = &prepared
		}
		if set.Reduction != nil {
			reduction := *set.Reduction
			reduction.SourceSetVariable = set.Variable
			set.Reduction = &reduction
		}
		newOps = append(newOps, ir.PhysicalOperation{Kind: ir.PhysicalSetOp, Source: op.Source, Set: &set})
	}
	plan.Operations = newOps
	plan.SharedTraversalCount += len(indices) - 1
	plan.AppliedRules = appendUniqueRule(plan.AppliedRules, OptimizerRuleTraversalSharing)
	return nil
}

func valueString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return "edge"
}
func appendUniqueRule(rules []string, rule string) []string {
	for _, r := range rules {
		if r == rule {
			return rules
		}
	}
	return append(rules, rule)
}
func rewritePhysicalOperationVariables(op ir.PhysicalOperation, fromTarget, toTarget, fromEdge, toEdge string) ir.PhysicalOperation {
	op = ir.ClonePhysicalOperation(op)
	rewritePhysicalNodeVariables(&op, fromTarget, toTarget, fromEdge, toEdge)
	return op
}

func rewritePhysicalNodeVariables(node ir.Node, fromTarget, toTarget, fromEdge, toEdge string) {
	rewrite := func(variable *string) {
		if *variable == fromTarget {
			*variable = toTarget
		}
		if *variable == fromEdge {
			*variable = toEdge
		}
	}
	rewriteUnnest := func(variable *string) {
		if *variable == fromTarget {
			*variable = toTarget
		} else if *variable == fromEdge {
			*variable = toEdge
		}
	}
	allowed := make(map[ir.Node]bool)
	allow := func(n ir.Node) { allowed[n] = true }
	switch n := node.(type) {
	case *ir.PhysicalValue:
		rewrite(&n.Variable)
	case *ir.PhysicalOperation:
		switch n.Kind {
		case ir.PhysicalTraversalOp:
			if n.Traversal != nil {
				rewrite(&n.Traversal.SourceVariable)
				rewrite(&n.Traversal.TargetVariable)
				rewrite(&n.Traversal.EdgeVariable)
			}
		case ir.PhysicalFilterOp:
			if n.Filter != nil {
				allow(&n.Filter.Predicate.Left)
				allow(n.Filter.Predicate.LeftExpression)
				allow(n.Filter.Predicate.Right)
				allow(n.Filter.Expression)
			}
		case ir.PhysicalDerivedLetOp:
			if n.DerivedLet != nil {
				for i := range n.DerivedLet.Inputs {
					allow(&n.DerivedLet.Inputs[i])
				}
			}
		case ir.PhysicalUnnestOp:
			if n.Unnest != nil {
				rewriteUnnest(&n.Unnest.Owner.RootVariable)
				rewriteUnnest(&n.Unnest.Owner.OwnerVariable)
				for i := range n.Unnest.Owner.Route {
					route := &n.Unnest.Owner.Route[i]
					rewriteUnnest(&route.Traversal.SourceVariable)
					rewriteUnnest(&route.Traversal.TargetVariable)
					rewriteUnnest(&route.Traversal.EdgeVariable)
					for j := range route.Scope {
						allow(&route.Scope[j])
					}
				}
				allow(&n.Unnest.Expression)
			}
		}
	case *ir.PhysicalExpression:
		switch n.Kind {
		case ir.PhysicalValueExpression:
			allow(n.Value)
		case ir.PhysicalExtractExpression:
			if n.Extract != nil {
				allow(&n.Extract.Source)
			}
		case ir.PhysicalAggregateExpression:
			if n.Aggregate != nil {
				allow(&n.Aggregate.Source)
				allow(n.Aggregate.Value)
				allow(n.Aggregate.Predicate)
			}
		case ir.PhysicalPivotExpression:
			if n.Pivot != nil {
				allow(&n.Pivot.Source)
			}
		case ir.PhysicalSliceExpression:
			if n.Slice != nil {
				allow(&n.Slice.Source)
				allow(n.Slice.Predicate)
				allow(n.Slice.Sort)
				for i := range n.Slice.Projections {
					allow(&n.Slice.Projections[i].Expression)
				}
			}
		case ir.PhysicalObjectExpression:
			if n.Object != nil {
				for i := range n.Object.Fields {
					allow(&n.Object.Fields[i].Expression)
				}
			}
		case ir.PhysicalCallExpression:
			if n.Call != nil {
				for i := range n.Call.Args {
					allow(&n.Call.Args[i])
				}
			}
		}
	case *ir.PhysicalPredicateExpression:
		switch n.Kind {
		case ir.PhysicalComparisonPredicate:
			if n.Comparison != nil {
				allow(&n.Comparison.Left)
				allow(n.Comparison.LeftExpression)
				allow(n.Comparison.Right)
			}
		case ir.PhysicalAllPredicate, ir.PhysicalAnyPredicate, ir.PhysicalNotPredicate:
			for i := range n.Children {
				allow(&n.Children[i])
			}
		case ir.PhysicalExistsPredicate:
			allow(n.Exists)
		}
	case *ir.PhysicalSubplan:
		for i := range n.Operations {
			allow(&n.Operations[i])
		}
		allow(&n.Return)
	}
	for _, child := range ir.Children(node) {
		if allowed[child] {
			rewritePhysicalNodeVariables(child, fromTarget, toTarget, fromEdge, toEdge)
		}
	}
}
