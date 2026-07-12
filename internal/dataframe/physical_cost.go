package dataframe

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
	// policy never disables validation or changes the physical plan supplied by
	// the caller; it only prevents optional traversal-sharing rewrites. Rich
	// selector preparation has its own structural gate during lowering.
	Enabled bool
	// MinimumSavings is the minimum estimated operation-work reduction required
	// before an optional rewrite is applied. The default is one operation.
	MinimumSavings int
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
	Decisions      []PhysicalOptimizationDecision
}

const physicalOptimizationPolicyName = "conservative-structural-v1"

// DefaultPhysicalOptimizationPolicy returns the production policy. The
// LOOM_PHYSICAL_COST_POLICY environment variable is intentionally a local
// developer switch, not a user-controlled query input. Set it to "off" (or
// "0"/"false") to compare the unshared physical shape; all validation and
// result semantics remain unchanged. Rich selector preparation is governed by
// its independent repeated-consumer proof.
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
	return policy
}

func newPhysicalOptimizationReport(policy PhysicalOptimizationPolicy) PhysicalOptimizationReport {
	if policy.MinimumSavings < 0 {
		policy.MinimumSavings = 0
	}
	return PhysicalOptimizationReport{
		Policy:         physicalOptimizationPolicyName,
		Enabled:        policy.Enabled,
		MinimumSavings: policy.MinimumSavings,
	}
}

func (report *PhysicalOptimizationReport) addDecision(decision PhysicalOptimizationDecision) {
	if report == nil {
		return
	}
	report.Decisions = append(report.Decisions, decision)
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
	copy.Decisions = append([]PhysicalOptimizationDecision(nil), report.Decisions...)
	return copy
}
