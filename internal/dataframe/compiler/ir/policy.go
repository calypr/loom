package ir

import (
	"os"
	"strconv"
	"strings"
)

// PhysicalOptimizationPolicy is the small, explainable policy used by the
// physical optimizer. It deliberately estimates operation work rather than
// pretending to predict Arango's cost; PROFILE remains the authority for
// deciding whether a rewrite is worthwhile on a particular dataset.
type PhysicalOptimizationPolicy struct {
	// Enabled is false when the policy is disabled explicitly. Disabling the
	// policy never disables validation or changes semantic meaning; it prevents
	// all optional optimizer families while preserving the unoptimized physical
	// execution plan.
	Enabled bool
	// MinimumSavings is the minimum estimated operation-work reduction required
	// before an optional rewrite is applied. The default is one operation.
	MinimumSavings int
	// RuleOverrides contains explicit per-rule ablation decisions. A nil entry
	// preserves the production default: traversal sharing and compact set
	// projection are enabled after their live parity/profile gates. The older
	// second-pass prepared selector experiment and later optimization families
	// remain disabled until their own payload/cost gate has passed.
	RuleOverrides map[PhysicalOptimizationRule]bool
}

// PhysicalOptimizationRule names an independently ablatable optimizer family.
// These names are compiler-owned and are intentionally independent of FHIR
// resource names or rendered AQL fragments.
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

// RuleEnabled resolves one rule without mutating the caller's policy. A
// global disable always wins; an explicit override wins over the defaults.
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

// WithRule returns a copy with one named rule explicitly enabled or disabled.
// It is used by benchmark and parity harnesses to change exactly one rule.
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

// PhysicalOptimizationDecision explains one candidate rewrite. The values are
// intentionally structural and stable across schemas: they count typed
// physical operations, not FHIR resource names or rendered AQL fragments.
type PhysicalOptimizationDecision struct {
	Rule                   string
	Enabled                bool
	CandidateSets          int
	EstimatedBaselineWork  int
	EstimatedOptimizedWork int
	EstimatedSavings       int
	Reason                 string
}

// PhysicalOptimizationReport is attached to an optimized plan and copied
// into compiler diagnostics. A rejection is useful evidence: it tells callers
// why a seemingly similar route was left unshared instead of silently hiding
// the decision in rendered AQL.
type PhysicalOptimizationReport struct {
	Policy         string
	Enabled        bool
	MinimumSavings int
	RuleStates     []PhysicalOptimizationRuleState
	Decisions      []PhysicalOptimizationDecision
}

// PhysicalOptimizationRuleState reports the resolved state of every known
// optimizer family, including families that had no candidate in this plan.
// Decisions remain reserved for candidate-specific estimates and rewrites.
type PhysicalOptimizationRuleState struct {
	Rule    PhysicalOptimizationRule
	Enabled bool
	Reason  string
}

const physicalOptimizationPolicyName = "conservative-structural-v1"

// DefaultPhysicalOptimizationPolicy returns the production policy. The
// LOOM_PHYSICAL_COST_POLICY environment variable is intentionally a local
// developer switch, not a user-controlled query input. Set it to "off" (or
// "0"/"false") to compare the unshared physical shape; all validation and
// result semantics remain unchanged. Compact set projection includes
// traversal-time selector projection for fallback-free rich consumers; set
// LOOM_PHYSICAL_RULE_COMPACT_PROJECTION=off to compare full-node output. The
// older second-pass prepared selector experiment remains opt-in via
// LOOM_PHYSICAL_RULE_PREPARED_SELECTORS=on.
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

// estimateTraversalSharingWork models the prefix and subset operations that
// the rewrite actually changes. Every original set pays for its traversal and
// scope prefix. The shared plan pays for one prefix plus one typed subset per
// consumer. Consumer-specific operations are present in either plan and are
// therefore intentionally excluded from the estimate.
func estimateTraversalSharingWork(prefix PhysicalTraversalPrefixDecomposition, candidateSets int) (baseline, optimized, savings int) {
	if candidateSets < 2 {
		return 0, 0, 0
	}
	// Traversal plus the canonical scope block. The decomposition carries the
	// scope operation count so this estimate remains independent of a specific
	// FHIR route or an incidental renderer variable name.
	prefixOperations := 1 + prefix.Prefix.ScopeOperationCount
	if prefix.Prefix.SourceVariable == "" {
		return 0, 0, 0
	}
	baseline = prefixOperations * candidateSets
	optimized = prefixOperations + candidateSets // one typed subset per set
	savings = baseline - optimized
	return baseline, optimized, savings
}

// estimatePreparedSelectorWork uses a deliberately small structural model:
// selector extraction has a source iteration and a value projection, while a
// prepared value pays one projection plus a cheap field read at each consumer.
// It is only used as a lower-bound gate (PROFILE remains authoritative), so a
// selector must have at least two consumers before preparation is allowed.
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
