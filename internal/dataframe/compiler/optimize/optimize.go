package optimize

import (
	"fmt"
	"sort"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

const OptimizerRuleTraversalSharing = "share_identical_traversals"

// OptimizePhysicalPlan applies semantics-preserving physical rewrites using
// the conservative structural cost policy. An unshared plan is always
// available as the correctness oracle, and the local policy switch can be used
// to compare the two physical shapes without changing request semantics.
func OptimizePhysicalPlan(plan ir.PhysicalPlan) (ir.PhysicalPlan, error) {
	return OptimizePhysicalPlanWithPolicy(plan, ir.DefaultPhysicalOptimizationPolicy())
}

// OptimizePhysicalPlanWithPolicy is the explicit form used by tests and
// benchmarking tools. The policy decides only whether an optional rewrite is
// enabled; it never relaxes physical-plan validation.
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
			// Ineligibility is an expected optimizer outcome. The original plan
			// remains the execution oracle.
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
		// Sharing is useful only when it removes target-type-specific traversals.
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
	if policy.RuleEnabled(ir.PhysicalOptimizationRuleKeyedMapSharing) {
		sharePhysicalLookupFamilies(&out, policy)
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
	// The broad set changes its target bind from one type to a type list. Deep
	// clone before that change: its first consumer must retain the original
	// single target-type bind for the typed subset.
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
	// Deterministic order is part of plan stability.
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
	base.Subplan.Operations[0].Traversal = &t
	base.Subplan.Operations = ir.ClonePhysicalOperations(original.Subplan.Operations[:7])
	base.Variable = baseName
	base.SourceSetVariable, base.ItemVariable = "", ""
	// The broad source must remain a full node because sibling consumers may
	// require different compact fields. Each typed subset retains its own
	// proven output contract below; unioning those contracts into the broad
	// traversal would make sharing resource/cardinality dependent.
	base.Output = nil
	// Prepared values belong to typed consumer sets. The broad shared neighbor
	// set must contain raw nodes only; consumers prepare their own projections
	// after resource-type filtering.
	base.Prepared = nil
	// Traversal-time selector projections are also consumer-specific. Keeping
	// the first sibling's projection on the broad heterogeneous set evaluates
	// selectors against the wrong resource payload and leaves typed subsets
	// with empty values.
	base.Projection = nil
	base.Subplan.Return = ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{Variable: t.TargetVariable}}
	// Rebuild operation stream, inserting the broad traversal immediately before
	// the first consumer and replacing each consumer with a typed subset.
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
			// The typed consumer now reads the shared subset rather than the
			// original traversal set. Rebind the prepared projection's source
			// to the consumer set so rendering defines it in the same scope.
			prepared := *set.Prepared
			prepared.SourceSetVariable = set.Variable
			set.Prepared = &prepared
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
func rewritePhysicalValue(v ir.PhysicalValue, fromTarget, toTarget, fromEdge, toEdge string) ir.PhysicalValue {
	if v.Variable == fromTarget {
		v.Variable = toTarget
	}
	if v.Variable == fromEdge {
		v.Variable = toEdge
	}
	return v
}
func rewritePhysicalOperationVariables(op ir.PhysicalOperation, fromTarget, toTarget, fromEdge, toEdge string) ir.PhysicalOperation {
	if op.Traversal != nil {
		t := *op.Traversal
		if t.SourceVariable == fromTarget {
			t.SourceVariable = toTarget
		}
		if t.SourceVariable == fromEdge {
			t.SourceVariable = toEdge
		}
		if t.TargetVariable == fromTarget {
			t.TargetVariable = toTarget
		}
		if t.TargetVariable == fromEdge {
			t.TargetVariable = toEdge
		}
		if t.EdgeVariable == fromTarget {
			t.EdgeVariable = toTarget
		}
		if t.EdgeVariable == fromEdge {
			t.EdgeVariable = toEdge
		}
		op.Traversal = &t
	}
	if op.Filter != nil {
		f := *op.Filter
		f.Predicate.Left = rewritePhysicalValue(f.Predicate.Left, fromTarget, toTarget, fromEdge, toEdge)
		if f.Predicate.LeftExpression != nil {
			e := rewritePhysicalExpressionVariables(*f.Predicate.LeftExpression, fromTarget, toTarget, fromEdge, toEdge)
			f.Predicate.LeftExpression = &e
		}
		if f.Predicate.Right != nil {
			v := rewritePhysicalValue(*f.Predicate.Right, fromTarget, toTarget, fromEdge, toEdge)
			f.Predicate.Right = &v
		}
		if f.Expression != nil {
			expression := rewritePhysicalPredicateExpressionVariables(*f.Expression, fromTarget, toTarget, fromEdge, toEdge)
			f.Expression = &expression
		}
		op.Filter = &f
	}
	if op.DerivedLet != nil {
		d := *op.DerivedLet
		for i := range d.Inputs {
			d.Inputs[i] = rewritePhysicalValue(d.Inputs[i], fromTarget, toTarget, fromEdge, toEdge)
		}
		if d.Expression != nil {
			expression := rewritePhysicalExpressionVariables(*d.Expression, fromTarget, toTarget, fromEdge, toEdge)
			d.Expression = &expression
		}
		op.DerivedLet = &d
	}
	if op.Unnest != nil {
		u := *op.Unnest
		if u.InputVariable == fromTarget {
			u.InputVariable = toTarget
		}
		if u.InputVariable == fromEdge {
			u.InputVariable = toEdge
		}
		u.Expression = rewritePhysicalExpressionVariables(u.Expression, fromTarget, toTarget, fromEdge, toEdge)
		op.Unnest = &u
	}
	return op
}

func rewritePhysicalExpressionVariables(expression ir.PhysicalExpression, fromTarget, toTarget, fromEdge, toEdge string) ir.PhysicalExpression {
	if expression.Value != nil {
		v := rewritePhysicalValue(*expression.Value, fromTarget, toTarget, fromEdge, toEdge)
		expression.Value = &v
	}
	if expression.Extract != nil {
		e := *expression.Extract
		e.Source = rewritePhysicalValue(e.Source, fromTarget, toTarget, fromEdge, toEdge)
		expression.Extract = &e
	}
	if expression.Aggregate != nil {
		a := *expression.Aggregate
		a.Source = rewritePhysicalValue(a.Source, fromTarget, toTarget, fromEdge, toEdge)
		if a.Value != nil {
			v := rewritePhysicalExpressionVariables(*a.Value, fromTarget, toTarget, fromEdge, toEdge)
			a.Value = &v
		}
		if a.Predicate != nil {
			predicate := rewritePhysicalPredicateExpressionVariables(*a.Predicate, fromTarget, toTarget, fromEdge, toEdge)
			a.Predicate = &predicate
		}
		expression.Aggregate = &a
	}
	if expression.Pivot != nil {
		pivot := *expression.Pivot
		pivot.Source = rewritePhysicalValue(pivot.Source, fromTarget, toTarget, fromEdge, toEdge)
		expression.Pivot = &pivot
	}
	if expression.Slice != nil {
		slice := *expression.Slice
		slice.Source = rewritePhysicalValue(slice.Source, fromTarget, toTarget, fromEdge, toEdge)
		if slice.Predicate != nil {
			predicate := rewritePhysicalPredicateExpressionVariables(*slice.Predicate, fromTarget, toTarget, fromEdge, toEdge)
			slice.Predicate = &predicate
		}
		if slice.Sort != nil {
			sort := rewritePhysicalExpressionVariables(*slice.Sort, fromTarget, toTarget, fromEdge, toEdge)
			slice.Sort = &sort
		}
		for index := range slice.Projections {
			slice.Projections[index].Expression = rewritePhysicalExpressionVariables(slice.Projections[index].Expression, fromTarget, toTarget, fromEdge, toEdge)
		}
		expression.Slice = &slice
	}
	if expression.Object != nil {
		object := *expression.Object
		object.Fields = append([]ir.PhysicalExpressionProjection(nil), object.Fields...)
		for index := range object.Fields {
			object.Fields[index].Expression = rewritePhysicalExpressionVariables(object.Fields[index].Expression, fromTarget, toTarget, fromEdge, toEdge)
		}
		expression.Object = &object
	}
	if expression.Call != nil {
		call := *expression.Call
		call.Args = append([]ir.PhysicalExpression(nil), call.Args...)
		for index := range call.Args {
			call.Args[index] = rewritePhysicalExpressionVariables(call.Args[index], fromTarget, toTarget, fromEdge, toEdge)
		}
		expression.Call = &call
	}
	return expression
}

func rewritePhysicalPredicateExpressionVariables(predicate ir.PhysicalPredicateExpression, fromTarget, toTarget, fromEdge, toEdge string) ir.PhysicalPredicateExpression {
	predicate = ir.ClonePhysicalPredicateExpression(predicate)
	if predicate.Comparison != nil {
		comparison := *predicate.Comparison
		comparison.Left = rewritePhysicalValue(comparison.Left, fromTarget, toTarget, fromEdge, toEdge)
		if comparison.LeftExpression != nil {
			expression := rewritePhysicalExpressionVariables(*comparison.LeftExpression, fromTarget, toTarget, fromEdge, toEdge)
			comparison.LeftExpression = &expression
		}
		if comparison.Right != nil {
			right := rewritePhysicalValue(*comparison.Right, fromTarget, toTarget, fromEdge, toEdge)
			comparison.Right = &right
		}
		predicate.Comparison = &comparison
	}
	for index := range predicate.Children {
		predicate.Children[index] = rewritePhysicalPredicateExpressionVariables(predicate.Children[index], fromTarget, toTarget, fromEdge, toEdge)
	}
	if predicate.Exists != nil {
		subplan := ir.ClonePhysicalSubplan(*predicate.Exists)
		for index := range subplan.Operations {
			subplan.Operations[index] = rewritePhysicalOperationVariables(subplan.Operations[index], fromTarget, toTarget, fromEdge, toEdge)
		}
		subplan.Return = rewritePhysicalExpressionVariables(subplan.Return, fromTarget, toTarget, fromEdge, toEdge)
		predicate.Exists = &subplan
	}
	return predicate
}
