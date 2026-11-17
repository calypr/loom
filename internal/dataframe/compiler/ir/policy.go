package ir

import (
	"os"
	"strconv"
	"strings"
)

type PhysicalOptimizationPolicy struct {
	Enabled        bool
	MinimumSavings int
	RuleOverrides  map[PhysicalOptimizationRule]bool
}

type PhysicalOptimizationRule string

const (
	PhysicalOptimizationRuleTraversalSharing  PhysicalOptimizationRule = "traversal_sharing"
	PhysicalOptimizationRulePreparedSelectors PhysicalOptimizationRule = "prepared_selectors"
	PhysicalOptimizationRuleCompactProjection PhysicalOptimizationRule = "compact_set_projection"
	PhysicalOptimizationRuleEndpointTraversal PhysicalOptimizationRule = "endpoint_traversal"
)

var allPhysicalOptimizationRules = []PhysicalOptimizationRule{
	PhysicalOptimizationRuleTraversalSharing,
	PhysicalOptimizationRulePreparedSelectors,
	PhysicalOptimizationRuleCompactProjection,
	PhysicalOptimizationRuleEndpointTraversal,
}

func (policy PhysicalOptimizationPolicy) RuleEnabled(rule PhysicalOptimizationRule) bool {
	if !policy.Enabled {
		return false
	}
	if policy.RuleOverrides != nil {
		if enabled, ok := policy.RuleOverrides[rule]; ok {
			return enabled
		}
	}
	switch rule {
	case PhysicalOptimizationRuleTraversalSharing, PhysicalOptimizationRuleCompactProjection, PhysicalOptimizationRuleEndpointTraversal:
		return true
	default:
		return false
	}
}

func (policy PhysicalOptimizationPolicy) WithRule(rule PhysicalOptimizationRule, enabled bool) PhysicalOptimizationPolicy {
	if policy.RuleOverrides == nil {
		policy.RuleOverrides = make(map[PhysicalOptimizationRule]bool)
	} else {
		copy := make(map[PhysicalOptimizationRule]bool, len(policy.RuleOverrides)+1)
		for key, value := range policy.RuleOverrides {
			copy[key] = value
		}
		policy.RuleOverrides = copy
	}
	policy.RuleOverrides[rule] = enabled
	return policy
}

type PhysicalOptimizationDecision struct {
	Rule                   string
	Enabled                bool
	CandidateSets          int
	EstimatedBaselineWork  int
	EstimatedOptimizedWork int
	EstimatedSavings       int
	Reason                 string
}

type PhysicalOptimizationReport struct {
	Policy         string
	Enabled        bool
	MinimumSavings int
	RuleStates     []PhysicalOptimizationRuleState
	Decisions      []PhysicalOptimizationDecision
}

type PhysicalOptimizationRuleState struct {
	Rule    PhysicalOptimizationRule
	Enabled bool
	Reason  string
}

const physicalOptimizationPolicyName = "conservative-structural-v1"

func DefaultPhysicalOptimizationPolicy() PhysicalOptimizationPolicy {
	policy := PhysicalOptimizationPolicy{Enabled: true, MinimumSavings: 1}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOOM_PHYSICAL_COST_POLICY"))) {
	case "off", "0", "false", "disabled":
		policy.Enabled = false
	}
	if raw := strings.TrimSpace(os.Getenv("LOOM_PHYSICAL_COST_MIN_SAVINGS")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value >= 0 {
			policy.MinimumSavings = value
		}
	}
	for _, setting := range []struct {
		name string
		rule PhysicalOptimizationRule
	}{
		{name: "LOOM_PHYSICAL_RULE_TRAVERSAL_SHARING", rule: PhysicalOptimizationRuleTraversalSharing},
		{name: "LOOM_PHYSICAL_RULE_PREPARED_SELECTORS", rule: PhysicalOptimizationRulePreparedSelectors},
		{name: "LOOM_PHYSICAL_RULE_COMPACT_PROJECTION", rule: PhysicalOptimizationRuleCompactProjection},
		{name: "LOOM_PHYSICAL_RULE_ENDPOINT_TRAVERSAL", rule: PhysicalOptimizationRuleEndpointTraversal},
	} {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(setting.name))) {
		case "on", "1", "true", "enabled":
			policy = policy.WithRule(setting.rule, true)
		case "off", "0", "false", "disabled":
			policy = policy.WithRule(setting.rule, false)
		}
	}
	return policy
}

func newPhysicalOptimizationReport(policy PhysicalOptimizationPolicy) PhysicalOptimizationReport {
	if policy.MinimumSavings < 0 {
		policy.MinimumSavings = 0
	}
	report := PhysicalOptimizationReport{
		Policy:         physicalOptimizationPolicyName,
		Enabled:        policy.Enabled,
		MinimumSavings: policy.MinimumSavings,
	}
	for _, rule := range allPhysicalOptimizationRules {
		enabled := policy.RuleEnabled(rule)
		reason := "disabled until implemented and profile-gated"
		if enabled {
			reason = "enabled; candidate-specific decisions are reported separately"
		}
		if !policy.Enabled {
			reason = "global optimization policy disabled"
		} else if override, ok := policy.RuleOverrides[rule]; ok {
			if override {
				reason = "enabled by explicit policy override"
			} else {
				reason = "disabled by explicit policy override"
			}
		}
		report.RuleStates = append(report.RuleStates, PhysicalOptimizationRuleState{Rule: rule, Enabled: enabled, Reason: reason})
	}
	return report
}

func (report *PhysicalOptimizationReport) addDecision(decision PhysicalOptimizationDecision) {
	if report == nil {
		return
	}
	report.Decisions = append(report.Decisions, decision)
}

func (report *PhysicalOptimizationReport) AddDecision(decision PhysicalOptimizationDecision) {
	report.addDecision(decision)
}

func estimateTraversalSharingWork(prefix PhysicalTraversalPrefixDecomposition, candidateSets int) (baseline, optimized, savings int) {
	if candidateSets < 2 {
		return 0, 0, 0
	}
	prefixOperations := 1 + prefix.Prefix.ScopeOperationCount
	if prefix.Prefix.SourceVariable == "" {
		return 0, 0, 0
	}
	baseline = prefixOperations * candidateSets
	optimized = prefixOperations + candidateSets
	savings = baseline - optimized
	return baseline, optimized, savings
}

func estimatePreparedSelectorWork(selectorUseCount int) (baseline, optimized, savings int) {
	if selectorUseCount < 2 {
		return 0, 0, 0
	}
	baseline = selectorUseCount * 2
	optimized = selectorUseCount + 1
	savings = baseline - optimized
	return baseline, optimized, savings
}

func clonePhysicalOptimizationReport(report PhysicalOptimizationReport) PhysicalOptimizationReport {
	copy := report
	copy.RuleStates = append([]PhysicalOptimizationRuleState(nil), report.RuleStates...)
	copy.Decisions = append([]PhysicalOptimizationDecision(nil), report.Decisions...)
	return copy
}

func NewPhysicalOptimizationReport(policy PhysicalOptimizationPolicy) PhysicalOptimizationReport {
	return newPhysicalOptimizationReport(policy)
}

func EstimateTraversalSharingWork(prefix PhysicalTraversalPrefixDecomposition, candidateSets int) (int, int, int) {
	return estimateTraversalSharingWork(prefix, candidateSets)
}

func EstimatePreparedSelectorWork(selectorUseCount int) (int, int, int) {
	return estimatePreparedSelectorWork(selectorUseCount)
}
