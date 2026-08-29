package compiler

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/optimize"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestOptimizePhysicalPlanSharesEquivalentTypedPrefixes(t *testing.T) {
	plan := physicalScopedSiblingPlan(t)
	optimized, err := optimize.OptimizePhysicalPlanWithPolicy(plan, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("OptimizePhysicalPlan() error = %v", err)
	}
	if optimized.SharedTraversalCount != 1 {
		t.Fatalf("shared traversal count = %d, want 1", optimized.SharedTraversalCount)
	}
	if !slices.Contains(optimized.AppliedRules, OptimizerRuleTraversalSharing) {
		t.Fatalf("missing sharing rule: %#v", optimized.AppliedRules)
	}
	if len(optimized.Operations) != len(plan.Operations)+1 {
		t.Fatalf("optimized operation count = %d, want %d", len(optimized.Operations), len(plan.Operations)+1)
	}
	if err := optimized.Validate(); err != nil {
		t.Fatalf("optimized plan invalid: %v", err)
	}
	var broad, subsets int
	for _, op := range optimized.Operations {
		if op.Kind != ir.PhysicalSetOp {
			continue
		}
		if op.Set.SourceSetVariable == "" {
			broad++
		} else {
			subsets++
		}
	}
	if broad != 1 || subsets != 2 {
		t.Fatalf("broad=%d subsets=%d, want 1/2", broad, subsets)
	}
}

func TestOptimizePhysicalPlanSharesRepeatedLookups(t *testing.T) {
	plan, err := buildGenericPhysicalPlan(semantic.OutputPlan{Root: semantic.SemanticNode{Alias: "root", ResourceType: "Patient"}})
	if err != nil {
		t.Fatal(err)
	}
	plan.BindVars["lookup_a"] = "a"
	plan.BindVars["lookup_b"] = "b"
	source := ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{Variable: "root", Path: []string{"payload", "identifier"}}}
	key := ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{Variable: "lookup_item", Path: []string{"value"}}}
	value := key
	returnOp := &plan.Operations[len(plan.Operations)-1]
	for _, item := range []struct{ name, bind string }{{"a", "lookup_a"}, {"b", "lookup_b"}} {
		expression := ir.PhysicalExpression{Kind: ir.PhysicalLookupExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Lookup: &ir.PhysicalLookup{Source: source, ItemVariable: "lookup_item", ItemKey: key, ItemValue: value, MatchBindKey: item.bind}}
		returnOp.Return.Projections = append(returnOp.Return.Projections, ir.PhysicalProjection{Name: item.name, Expression: &expression})
	}
	optimized, err := optimize.OptimizePhysicalPlanWithPolicy(plan, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	var lets, lookups int
	for _, operation := range optimized.Operations {
		if operation.Kind == ir.PhysicalExpressionLetOp {
			lets++
		}
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.Expression != nil && projection.Expression.Kind == ir.PhysicalObjectLookupExpression {
				lookups++
			}
		}
	}
	if lets != 1 || lookups != 2 {
		t.Fatalf("shared lookup plan has %d lets and %d object lookups, want 1/2", lets, lookups)
	}
}

func TestOptimizePhysicalPlanKeepsConsumerProjectionOffBroadSharedSet(t *testing.T) {
	plan := physicalScopedSiblingPlan(t)
	for _, set := range physicalSets(plan) {
		resourceType := fmt.Sprint(plan.BindVars[set.Subplan.Operations[0].Traversal.TargetTypeBindKey])
		set.Projection = &ir.PhysicalSetProjection{Fields: []ir.PhysicalSetProjectionField{{Name: "__loom_projection_0", ResourceType: resourceType, Selector: mustPhysicalSelector(t, "id")}}}
		set.Output = nil
	}
	optimized, err := optimize.OptimizePhysicalPlanWithPolicy(plan, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	var broad, subsets int
	for _, set := range physicalSets(optimized) {
		if set.SourceSetVariable == "" {
			broad++
			if set.Projection != nil {
				t.Fatalf("heterogeneous shared set retained consumer projection: %#v", set.Projection)
			}
		} else {
			subsets++
			if set.Projection == nil {
				t.Fatalf("typed subset lost consumer projection: %#v", set)
			}
		}
	}
	if broad != 1 || subsets != 2 {
		t.Fatalf("shared projection sets = broad %d subsets %d, want 1/2", broad, subsets)
	}
}

func TestOptimizePhysicalPlanReportsStructuralCostDecision(t *testing.T) {
	optimized, err := optimize.OptimizePhysicalPlanWithPolicy(physicalScopedSiblingPlan(t), ir.PhysicalOptimizationPolicy{Enabled: true, MinimumSavings: 1})
	if err != nil {
		t.Fatal(err)
	}
	decisions := optimized.OptimizationPolicy.Decisions
	if len(decisions) != 1 {
		t.Fatalf("cost-policy decisions = %#v, want one decision", decisions)
	}
	decision := decisions[0]
	if !decision.Enabled || decision.Rule != OptimizerRuleTraversalSharing {
		t.Fatalf("cost-policy decision = %#v, want enabled traversal sharing", decision)
	}
	if decision.EstimatedBaselineWork <= decision.EstimatedOptimizedWork || decision.EstimatedSavings <= 0 {
		t.Fatalf("cost estimate = %#v, want positive savings", decision)
	}
	if optimized.OptimizationPolicy.Policy == "" || !optimized.OptimizationPolicy.Enabled {
		t.Fatalf("missing enabled policy report: %#v", optimized.OptimizationPolicy)
	}
}

func TestOptimizePhysicalPlanCostPolicyCanDisableRewrite(t *testing.T) {
	original := physicalScopedSiblingPlan(t)
	optimized, err := optimize.OptimizePhysicalPlanWithPolicy(original, ir.PhysicalOptimizationPolicy{Enabled: false, MinimumSavings: 1})
	if err != nil {
		t.Fatal(err)
	}
	if optimized.SharedTraversalCount != 0 {
		t.Fatalf("disabled policy shared traversal count = %d, want 0", optimized.SharedTraversalCount)
	}
	if len(optimized.Operations) != len(original.Operations) {
		t.Fatalf("disabled policy changed operation count from %d to %d", len(original.Operations), len(optimized.Operations))
	}
	if len(optimized.OptimizationPolicy.Decisions) != 1 || optimized.OptimizationPolicy.Decisions[0].Enabled {
		t.Fatalf("disabled policy decision = %#v, want explicit rejection", optimized.OptimizationPolicy.Decisions)
	}
	if optimized.OptimizationPolicy.Decisions[0].Reason != "cost policy disabled" {
		t.Fatalf("disabled policy reason = %q", optimized.OptimizationPolicy.Decisions[0].Reason)
	}
}

func TestOptimizePhysicalPlanCostPolicyMinimumSavingsRejectsCandidate(t *testing.T) {
	optimized, err := optimize.OptimizePhysicalPlanWithPolicy(physicalScopedSiblingPlan(t), ir.PhysicalOptimizationPolicy{Enabled: true, MinimumSavings: 999})
	if err != nil {
		t.Fatal(err)
	}
	if optimized.SharedTraversalCount != 0 {
		t.Fatalf("high policy threshold shared traversal count = %d, want 0", optimized.SharedTraversalCount)
	}
	if len(optimized.OptimizationPolicy.Decisions) != 1 || optimized.OptimizationPolicy.Decisions[0].Enabled {
		t.Fatalf("high policy decision = %#v, want rejection", optimized.OptimizationPolicy.Decisions)
	}
	if optimized.OptimizationPolicy.Decisions[0].EstimatedSavings >= 999 {
		t.Fatalf("high policy estimate unexpectedly met threshold: %#v", optimized.OptimizationPolicy.Decisions[0])
	}
}

func TestDefaultPhysicalOptimizationPolicyDeveloperSwitch(t *testing.T) {
	t.Setenv("LOOM_PHYSICAL_COST_POLICY", "off")
	policy := ir.DefaultPhysicalOptimizationPolicy()
	if policy.Enabled {
		t.Fatalf("disabled environment policy = %#v", policy)
	}
	t.Setenv("LOOM_PHYSICAL_COST_POLICY", "on")
	t.Setenv("LOOM_PHYSICAL_COST_MIN_SAVINGS", "17")
	t.Setenv("LOOM_PHYSICAL_RULE_TRAVERSAL_SHARING", "off")
	t.Setenv("LOOM_PHYSICAL_RULE_PREPARED_SELECTORS", "on")
	policy = ir.DefaultPhysicalOptimizationPolicy()
	if !policy.Enabled || policy.MinimumSavings != 17 {
		t.Fatalf("configured environment policy = %#v", policy)
	}
	if policy.RuleEnabled(ir.PhysicalOptimizationRuleTraversalSharing) || !policy.RuleEnabled(ir.PhysicalOptimizationRulePreparedSelectors) {
		t.Fatalf("configured independent rule policy = %#v", policy.RuleOverrides)
	}
}

func TestPhysicalOptimizationPolicyResolvesIndependentRules(t *testing.T) {
	policy := ir.DefaultPhysicalOptimizationPolicy()
	if !policy.RuleEnabled(ir.PhysicalOptimizationRuleTraversalSharing) || !policy.RuleEnabled(ir.PhysicalOptimizationRuleCompactProjection) || policy.RuleEnabled(ir.PhysicalOptimizationRulePreparedSelectors) {
		t.Fatalf("default active rules = %#v", policy)
	}
	for _, rule := range []ir.PhysicalOptimizationRule{
		ir.PhysicalOptimizationRuleNestedSharing,
		ir.PhysicalOptimizationRuleRichConsumerFusion,
	} {
		if policy.RuleEnabled(rule) {
			t.Fatalf("unimplemented rule %q was enabled by default", rule)
		}
	}
	policy = policy.WithRule(ir.PhysicalOptimizationRuleTraversalSharing, false).WithRule(ir.PhysicalOptimizationRulePreparedSelectors, false)
	if policy.RuleEnabled(ir.PhysicalOptimizationRuleTraversalSharing) || policy.RuleEnabled(ir.PhysicalOptimizationRulePreparedSelectors) {
		t.Fatalf("explicit rule overrides were ignored: %#v", policy.RuleOverrides)
	}
}

func TestOptimizePhysicalPlanReportsExplicitRuleDisable(t *testing.T) {
	policy := ir.DefaultPhysicalOptimizationPolicy().WithRule(ir.PhysicalOptimizationRuleTraversalSharing, false)
	optimized, err := optimize.OptimizePhysicalPlanWithPolicy(physicalScopedSiblingPlan(t), policy)
	if err != nil {
		t.Fatal(err)
	}
	if optimized.SharedTraversalCount != 0 {
		t.Fatalf("explicitly disabled sharing changed plan: %d", optimized.SharedTraversalCount)
	}
	if len(optimized.OptimizationPolicy.Decisions) != 1 || optimized.OptimizationPolicy.Decisions[0].Reason != "traversal sharing rule disabled" {
		t.Fatalf("explicit sharing decision = %#v", optimized.OptimizationPolicy.Decisions)
	}
	for _, state := range optimized.OptimizationPolicy.RuleStates {
		if state.Rule == ir.PhysicalOptimizationRuleTraversalSharing && state.Enabled {
			t.Fatalf("sharing rule state remained enabled: %#v", state)
		}
	}
}

func TestBuildGenericPhysicalPlanPolicyDisablesPreparedSelectors(t *testing.T) {
	status := spec.Selector{Steps: []spec.SelectorStep{{Field: "id"}}}
	output := semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Children: []semantic.SemanticNode{{
			Alias: "condition", ResourceType: "Condition", EdgeLabel: "subject_Patient",
			Aggregates: []semantic.SemanticAggregate{{Name: "status_count", Operation: "COUNT_DISTINCT", Selector: &status}},
			Slices:     []semantic.SemanticSlice{{Name: "representative", Limit: 1, Fields: []semantic.SemanticField{testSemanticField("status", status, "")}}},
		}},
	},
	}
	policy := ir.DefaultPhysicalOptimizationPolicy().WithRule(ir.PhysicalOptimizationRuleCompactProjection, false).WithRule(ir.PhysicalOptimizationRulePreparedSelectors, false)
	plan, err := lower.BuildGenericPhysicalPlanWithPolicy(output, semantic.ExecutionContext{Project: "p"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalSetOp && operation.Set != nil && operation.Set.Prepared != nil {
			t.Fatalf("prepared selector set survived explicit disable: %#v", operation.Set.Prepared)
		}
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered.Query, "_prepared") {
		t.Fatalf("prepared selector variable survived explicit disable:\n%s", rendered.Query)
	}
}

func TestBuildGenericPhysicalPlanCompactOutputPolicy(t *testing.T) {
	output := semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Children: []semantic.SemanticNode{{Alias: "condition", ResourceType: "Condition", EdgeLabel: "subject_Patient", Aggregates: []semantic.SemanticAggregate{{Name: "count", Operation: "COUNT"}}}},
	},
	}
	fullPolicy := ir.DefaultPhysicalOptimizationPolicy().WithRule(ir.PhysicalOptimizationRuleCompactProjection, false)
	full, err := lower.BuildGenericPhysicalPlanWithPolicy(output, semantic.ExecutionContext{Project: "p"}, fullPolicy)
	if err != nil {
		t.Fatal(err)
	}
	compactPolicy := ir.DefaultPhysicalOptimizationPolicy()
	compact, err := lower.BuildGenericPhysicalPlanWithPolicy(output, semantic.ExecutionContext{Project: "p"}, compactPolicy)
	if err != nil {
		t.Fatal(err)
	}
	var fullSet, compactSet *ir.PhysicalSet
	for _, operation := range full.Operations {
		if operation.Kind == ir.PhysicalSetOp {
			fullSet = operation.Set
		}
	}
	for _, operation := range compact.Operations {
		if operation.Kind == ir.PhysicalSetOp {
			compactSet = operation.Set
		}
	}
	if fullSet == nil || fullSet.Output != nil {
		t.Fatalf("default policy unexpectedly retained compact output: %#v", fullSet)
	}
	if compactSet == nil || compactSet.Output == nil {
		t.Fatalf("compact policy did not define output: %#v", compactSet)
	}
	if err := compact.Validate(); err != nil {
		t.Fatalf("compact plan validation failed: %v", err)
	}
	if len(compactSet.Output.Fields) != 4 || compactSet.Output.Fields[0] != ir.PhysicalSetGraphIDField || compactSet.Output.Fields[1] != ir.PhysicalSetKeyField {
		t.Fatalf("compact output fields = %#v, want graph identity plus metadata", compactSet.Output.Fields)
	}
}

func TestOptimizePhysicalPlanRendersOneScopedTraversalForSiblingSets(t *testing.T) {
	plan := physicalScopedSiblingPlan(t)
	optimized, err := optimize.OptimizePhysicalPlanWithPolicy(plan, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(optimized)
	if err != nil {
		t.Fatal(err)
	}
	nativeRoot := strings.Count(rendered.Query, "INBOUND root @@child_set_")
	endpointRoot := strings.Count(rendered.Query, "FOR child_set_1_edge IN @@child_set_1_edge_collection")
	if got := nativeRoot + endpointRoot; got != 1 {
		t.Fatalf("rendered sibling group used %d root edge operations, want 1 (native=%d endpoint=%d):\n%s", got, nativeRoot, endpointRoot, rendered.Query)
	}
	if got := strings.Count(rendered.Query, "child_set_1_edge.auth_resource_path IN @auth_resource_paths"); got != 1 {
		t.Fatalf("shared traversal did not retain its edge auth scope exactly once:\n%s", rendered.Query)
	}
	if got := strings.Count(rendered.Query, "child_set_1_node.auth_resource_path IN @auth_resource_paths"); got != 1 {
		t.Fatalf("shared traversal did not retain its node auth scope exactly once:\n%s", rendered.Query)
	}
}

func TestDecomposePhysicalTraversalPrefixIsAlphaEquivalentAcrossSiblingVariables(t *testing.T) {
	plan := physicalScopedSiblingPlan(t)
	sets := physicalSets(plan)
	if len(sets) != 2 {
		t.Fatalf("physical sets = %d, want 2", len(sets))
	}
	first, err := ir.DecomposePhysicalTraversalPrefixAt(plan, *sets[0], physicalSetIndex(plan, *sets[0]))
	if err != nil {
		t.Fatal(err)
	}
	second, err := ir.DecomposePhysicalTraversalPrefixAt(plan, *sets[1], physicalSetIndex(plan, *sets[1]))
	if err != nil {
		t.Fatal(err)
	}
	if first.PrefixKey != second.PrefixKey {
		t.Fatalf("sibling prefixes did not canonicalize:\nfirst=%s\nsecond=%s", first.PrefixKey, second.PrefixKey)
	}
	if first.Subset.TargetTypeBindKey == second.Subset.TargetTypeBindKey {
		t.Fatalf("fixture did not retain distinct target subsets: %#v %#v", first.Subset, second.Subset)
	}
}

func TestOptimizePhysicalPlanRebindsTypedConsumerFiltersToSharedSubsetItems(t *testing.T) {
	plan := physicalScopedSiblingPlanWithFilters(t)
	optimized, err := optimize.OptimizePhysicalPlanWithPolicy(plan, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if err := optimized.Validate(); err != nil {
		t.Fatalf("optimized plan validation: %v", err)
	}
	for _, set := range physicalSets(optimized) {
		if set.SourceSetVariable == "" {
			continue
		}
		if len(set.Subplan.Operations) != 2 {
			t.Fatalf("shared subset %q operations = %#v", set.Variable, set.Subplan.Operations)
		}
		filter := set.Subplan.Operations[1].Filter
		if filter == nil || filter.Expression == nil || filter.Expression.Comparison == nil || filter.Expression.Comparison.LeftExpression == nil || filter.Expression.Comparison.LeftExpression.Extract == nil {
			t.Fatalf("shared subset %q lost its typed consumer filter: %#v", set.Variable, set.Subplan.Operations)
		}
		if got := filter.Expression.Comparison.LeftExpression.Extract.Source.Variable; got != set.ItemVariable {
			t.Fatalf("shared subset %q filter source = %q, want item %q", set.Variable, got, set.ItemVariable)
		}
	}
}

func TestDecomposePhysicalTraversalPrefixRejectsUnscopedOrSharedSets(t *testing.T) {
	plan := physicalScopedSiblingPlan(t)
	set := *physicalSets(plan)[0]
	set.Subplan.Operations = set.Subplan.Operations[:6]
	_, err := ir.DecomposePhysicalTraversalPrefixAt(plan, set, physicalSetIndex(plan, set))
	assertPrefixRejection(t, err, ir.PhysicalPrefixMissingTraversal)

	set = *physicalSets(plan)[0]
	set.SourceSetVariable = "another_set"
	_, err = ir.DecomposePhysicalTraversalPrefixAt(plan, set, physicalSetIndex(plan, set))
	assertPrefixRejection(t, err, ir.PhysicalPrefixSharedSubset)
}

func assertPrefixRejection(t *testing.T, err error, want ir.PhysicalTraversalPrefixRejectionReason) {
	t.Helper()
	if err == nil {
		t.Fatalf("DecomposePhysicalTraversalPrefix() error = nil, want %s", want)
	}
	got, ok := err.(*ir.PhysicalTraversalPrefixError)
	if !ok || got.Reason != want {
		t.Fatalf("prefix error = %#v, want %s", err, want)
	}
}

func physicalSetIndex(plan ir.PhysicalPlan, set ir.PhysicalSet) int {
	for index, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalSetOp && operation.Set != nil && operation.Set.Variable == set.Variable {
			return index
		}
	}
	return -1
}

func physicalScopedSiblingPlan(t *testing.T) ir.PhysicalPlan {
	t.Helper()
	plan, err := buildGenericPhysicalPlanWithContext(semantic.OutputPlan{Root: semantic.SemanticNode{Alias: "root", ResourceType: "Patient", Children: []semantic.SemanticNode{
		{Alias: "condition", ResourceType: "Condition", EdgeLabel: "subject_Patient", Fields: []semantic.SemanticField{testSemanticField("id", mustPhysicalSelector(t, "id"), "")}},
		{Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient", Fields: []semantic.SemanticField{testSemanticField("id", mustPhysicalSelector(t, "id"), "")}},
	}}}, semantic.ExecutionContext{Project: "project-1", AuthResourcePaths: []string{"/programs/p1"}})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func physicalScopedSiblingPlanWithFilters(t *testing.T) ir.PhysicalPlan {
	t.Helper()
	plan := physicalScopedSiblingPlan(t)
	for _, set := range physicalSets(plan) {
		traversal := set.Subplan.Operations[0].Traversal
		predicate := ir.PhysicalPredicate{Operator: string(spec.FilterExists), ValueKind: spec.FilterString,
			LeftExpression: &ir.PhysicalExpression{Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalEmptyOnNull,
				Extract: &ir.PhysicalExtract{Source: ir.PhysicalValue{Variable: traversal.TargetVariable, Path: []string{"payload"}}, ResourceType: fmt.Sprint(plan.BindVars[traversal.TargetTypeBindKey]), Selector: mustPhysicalSelector(t, "id")}}}
		set.Subplan.Operations = append(set.Subplan.Operations, ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Expression: &ir.PhysicalPredicateExpression{Kind: ir.PhysicalComparisonPredicate, Comparison: &predicate}}})
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("filtered fixture validation: %v", err)
	}
	return plan
}

func physicalSets(plan ir.PhysicalPlan) []*ir.PhysicalSet {
	sets := make([]*ir.PhysicalSet, 0)
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalSetOp && operation.Set != nil {
			sets = append(sets, operation.Set)
		}
	}
	return sets
}
