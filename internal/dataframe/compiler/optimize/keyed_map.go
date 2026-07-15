package optimize

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

type keyedMapCandidate struct {
	Signature string
	Uses      []*ir.PhysicalExpression
	Lookup    ir.PhysicalLookup
}

type keyedMapBinding struct {
	Variable string
	Lookup   ir.PhysicalLookup
}

// sharePhysicalLookupFamilies converts repeated source-bearing lookup
// expressions in one RETURN scope into one keyed map and constant-time object
// lookups. The pass is deliberately lexical: candidates are collected per
// RETURN operation, so an optional child scope or an unnest can never leak a
// binding into a different row grain.
func sharePhysicalLookupFamilies(plan *PhysicalPlan, policy PhysicalOptimizationPolicy) {
	for returnIndex := range plan.Operations {
		operation := &plan.Operations[returnIndex]
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		candidates := map[string]*keyedMapCandidate{}
		for projectionIndex := range operation.Return.Projections {
			collectLookupCandidates(operation.Return.Projections[projectionIndex].Expression, candidates)
		}
		keys := make([]string, 0, len(candidates))
		for key, candidate := range candidates {
			if len(candidate.Uses) >= 2 {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		if len(keys) == 0 {
			continue
		}
		bindings := make(map[string]keyedMapBinding, len(keys))
		lets := make([]ir.PhysicalOperation, 0, len(keys))
		for _, key := range keys {
			candidate := candidates[key]
			baseline := len(candidate.Uses) * 3
			optimized := len(candidate.Uses) + 1
			decision := ir.PhysicalOptimizationDecision{Rule: string(PhysicalOptimizationRuleKeyedMapSharing), CandidateSets: len(candidate.Uses), EstimatedBaselineWork: baseline, EstimatedOptimizedWork: optimized, EstimatedSavings: baseline - optimized}
			if !policy.RuleEnabled(PhysicalOptimizationRuleKeyedMapSharing) {
				decision.Reason = "keyed map sharing disabled by policy"
				plan.OptimizationPolicy.AddDecision(decision)
				continue
			}
			if decision.EstimatedSavings < policy.MinimumSavings {
				decision.Reason = fmt.Sprintf("estimated savings %d is below policy minimum %d", decision.EstimatedSavings, policy.MinimumSavings)
				plan.OptimizationPolicy.AddDecision(decision)
				continue
			}
			digest := sha256.Sum256([]byte(key))
			variable := "__loom_shared_lookup_" + hex.EncodeToString(digest[:])[:12]
			bindings[key] = keyedMapBinding{Variable: variable, Lookup: candidate.Lookup}
			lets = append(lets, ir.PhysicalOperation{Kind: ir.PhysicalExpressionLetOp, Source: ir.PhysicalSource{SemanticField: "shared_lookup_family"}, ExpressionLet: &ir.PhysicalExpressionLet{Variable: variable, Expression: ir.PhysicalExpression{Kind: ir.PhysicalKeyedMapExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalPreserveNull, KeyedMap: &ir.PhysicalKeyedMap{Source: ir.ClonePhysicalExpression(candidate.Lookup.Source), ItemVariable: candidate.Lookup.ItemVariable, ItemKey: ir.ClonePhysicalExpression(candidate.Lookup.ItemKey), ItemValue: ir.ClonePhysicalExpression(candidate.Lookup.ItemValue), Reduction: ir.PhysicalMapFirst}}}})
			decision.Enabled = true
			decision.Reason = "repeated lookup source and selectors share one lexical keyed map"
			plan.OptimizationPolicy.AddDecision(decision)
		}
		if len(bindings) == 0 {
			continue
		}
		for projectionIndex := range operation.Return.Projections {
			replaceLookupCandidates(operation.Return.Projections[projectionIndex].Expression, bindings)
		}
		operations := make([]ir.PhysicalOperation, 0, len(plan.Operations)+len(lets))
		operations = append(operations, plan.Operations[:returnIndex]...)
		operations = append(operations, lets...)
		operations = append(operations, plan.Operations[returnIndex:]...)
		plan.Operations = operations
		returnIndex += len(lets)
	}
}

func collectLookupCandidates(expression *ir.PhysicalExpression, candidates map[string]*keyedMapCandidate) {
	if expression == nil {
		return
	}
	if expression.Kind == ir.PhysicalLookupExpression && expression.Lookup != nil && expression.Lookup.ItemVariable != "" && expression.Lookup.MatchBindKey != "" {
		keyed := ir.PhysicalKeyedMap{Source: ir.ClonePhysicalExpression(expression.Lookup.Source), ItemVariable: expression.Lookup.ItemVariable, ItemKey: ir.ClonePhysicalExpression(expression.Lookup.ItemKey), ItemValue: ir.ClonePhysicalExpression(expression.Lookup.ItemValue), Reduction: ir.PhysicalMapFirst}
		candidateExpression := ir.PhysicalExpression{Kind: ir.PhysicalKeyedMapExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalPreserveNull, KeyedMap: &keyed}
		signature, err := ir.PhysicalExpressionFingerprint(candidateExpression)
		if err == nil {
			candidate := candidates[signature]
			if candidate == nil {
				candidate = &keyedMapCandidate{Signature: signature, Lookup: *expression.Lookup}
				candidates[signature] = candidate
			}
			candidate.Uses = append(candidate.Uses, expression)
		}
	}
	if expression.Object != nil {
		for index := range expression.Object.Fields {
			collectLookupCandidates(&expression.Object.Fields[index].Expression, candidates)
		}
	}
}

func replaceLookupCandidates(expression *ir.PhysicalExpression, bindings map[string]keyedMapBinding) {
	if expression == nil {
		return
	}
	if expression.Kind == ir.PhysicalLookupExpression && expression.Lookup != nil {
		keyed := ir.PhysicalKeyedMap{Source: ir.ClonePhysicalExpression(expression.Lookup.Source), ItemVariable: expression.Lookup.ItemVariable, ItemKey: ir.ClonePhysicalExpression(expression.Lookup.ItemKey), ItemValue: ir.ClonePhysicalExpression(expression.Lookup.ItemValue), Reduction: ir.PhysicalMapFirst}
		candidateExpression := ir.PhysicalExpression{Kind: ir.PhysicalKeyedMapExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalPreserveNull, KeyedMap: &keyed}
		if signature, err := ir.PhysicalExpressionFingerprint(candidateExpression); err == nil {
			if binding, ok := bindings[signature]; ok {
				matchBindKey := expression.Lookup.MatchBindKey
				expression.Kind = ir.PhysicalObjectLookupExpression
				expression.Lookup = nil
				expression.KeyedMap = nil
				expression.ObjectLookup = &ir.PhysicalObjectLookup{ObjectVariable: binding.Variable, KeyBindKey: matchBindKey}
			}
		}
	}
	if expression.Object != nil {
		for index := range expression.Object.Fields {
			replaceLookupCandidates(&expression.Object.Fields[index].Expression, bindings)
		}
	}
}
