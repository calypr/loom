package benchmark

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The report is the deliverable. Its trust model: every derived number is
// RECOMPUTED from the raw record by VerifyReport, so a hand-edited (or
// partially forged) report fails verification. The verifier is deliberately
// independent of the harness: CI and reviewers run it on the artifact alone.

const ReportSchemaVersion = "loom.benchmark-report/v1"

var (
	sha256HexRe = regexp.MustCompile(`^[0-9a-f]{64}$`)
	idRe        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

// RunIdentity ties the report to exactly what ran.
type RunIdentity struct {
	RunID           string    `json:"runId"`
	StartedAt       time.Time `json:"startedAt"`
	Preset          string    `json:"preset"`
	SourceCommit    string    `json:"sourceCommit"`
	DatasetDigest   string    `json:"datasetDigest"` // fixture manifest digest
	ArangoVersion   string    `json:"arangoVersion"`
	PostgresVersion string    `json:"postgresVersion"`
	// ClickHouseDisabled records the ruling: dataframe legs run with
	// ClickHouse disabled on both arms so the reader path is matched.
	ClickHouseDisabled bool      `json:"clickhouseDisabled"`
	Config             RunConfig `json:"config"`
}

// ControlRuns records the measurement-integrity controls (ruling i). Each
// control must have been RUN; absent controls are a verification failure.
type ControlRuns struct {
	// StabilityCVLimit and the per-cell CVs used to decide stability.
	StabilityCVLimit float64  `json:"stabilityCvLimit"`
	UnstableCells    []string `json:"unstableCells"` // "workload/backend" cells over the limit
	// SelfRatio: backend vs itself across separate rounds; must sit at 1.
	SelfRatioArango   float64 `json:"selfRatioArango"`
	SelfRatioPostgres float64 `json:"selfRatioPostgres"`
	// EmptyResultFloor: timed no-result requests establishing the client
	// overhead floor subtracted nowhere but reported as context.
	EmptyFloorMillis float64 `json:"emptyFloorMillis"`
	// RatioPolicy: ratios are published only for workloads both arms passed.
	RatioPolicy string `json:"ratioPolicy"`
}

// Claim is one publishable performance claim.
type Claim struct {
	WorkloadID string     `json:"workloadId"`
	Arango     Summary    `json:"arango"`
	Postgres   Summary    `json:"postgres"`
	Ratio      SpeedRatio `json:"ratio"`
}

// Report is the full artifact.
type Report struct {
	Schema        string               `json:"schema"`
	Plan          RunPlan              `json:"plan"`
	Identity      RunIdentity          `json:"identity"`
	Status        string               `json:"status"` // passed | failed | blocked
	StatusReasons []string             `json:"statusReasons,omitempty"`
	Controls      ControlRuns          `json:"controls"`
	GateReports   []WorkloadGateReport `json:"gateReports"`
	Samples       []Sample             `json:"samples"`
	Summaries     []SummaryRow         `json:"summaries"`
	Claims        []Claim              `json:"claims"`
	Unsupported   []string             `json:"unsupported"`
	Exclusions    string               `json:"exclusions"`
}

// SummaryRow ties a Summary to its exact cell.
type SummaryRow struct {
	WorkloadID string     `json:"workloadId"`
	Backend    string     `json:"backend"`
	Cache      CacheLabel `json:"cache"`
	Summary    Summary    `json:"summary"`
}

// BuildReport assembles the artifact from harness-collected evidence and
// recomputes summaries/claims/status so the written file is internally
// consistent by construction. claimableOf decides per-workload eligibility
// (the harness passes the gate outcome); unstable cells are excluded from
// claims per the stability control.
func BuildReport(identity RunIdentity, plan RunPlan, controls ControlRuns, gateReports []WorkloadGateReport, samples []Sample) (*Report, error) {
	if err := identity.Config.Validate(); err != nil {
		return nil, err
	}
	if err := ValidateLegPlan(plan.Scope, plan.Legs); err != nil {
		return nil, err
	}
	if err := ValidatePlannedNotRun(plan.PlannedNotRun); err != nil {
		return nil, err
	}
	status, reasons := RunStatusScoped(gateReports, plan.Scope)
	report := &Report{
		Schema:        ReportSchemaVersion,
		Plan:          plan,
		Identity:      identity,
		Status:        status,
		StatusReasons: reasons,
		Controls:      controls,
		GateReports:   gateReports,
		Samples:       samples,
		Unsupported:   UnsupportedCases(gateReports),
		Exclusions:    FormatExclusionList(gateReports),
	}
	byCell := map[string][]Sample{}
	for _, s := range samples {
		key := s.WorkloadID + "|" + s.Backend + "|" + string(s.Cache)
		byCell[key] = append(byCell[key], s)
	}
	keys := make([]string, 0, len(byCell))
	for key := range byCell {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts := splitCell(key)
		report.Summaries = append(report.Summaries, SummaryRow{
			WorkloadID: parts[0], Backend: parts[1], Cache: CacheLabel(parts[2]),
			Summary: Summarize(byCell[key]),
		})
	}
	report.Claims = buildClaims(gateReports, report.Summaries, controls)
	if err := VerifyReport(report); err != nil {
		return nil, fmt.Errorf("benchmark: internally inconsistent report (bug): %w", err)
	}
	return report, nil
}

// claimCellPair resolves the steady-regime cell a claim may cite for each
// arm (review r4559430568): warm preferred; service-warm for an attached
// arm that never restarts; process-cold cells are diagnostic only and are
// never claimed. The same helper drives buildClaims and VerifyReport, so a
// claim can only ever cite what the policy would choose.
func claimCellPair(summaryOf map[string]Summary, workloadID string) (Summary, Summary, bool) {
	var a, p Summary
	ok := true
	for _, backend := range []string{BackendArango, BackendPostgres} {
		found := false
		for _, cache := range []CacheLabel{CacheWarm, CacheServiceWarm} {
			s := summaryOf[workloadID+"|"+backend+"|"+string(cache)]
			if s.Count > 0 && s.FailedCount == 0 {
				if backend == BackendArango {
					a = s
				} else {
					p = s
				}
				found = true
				break
			}
		}
		ok = ok && found
	}
	if !ok {
		return Summary{}, Summary{}, false
	}
	return a, p, true
}

func buildClaims(gateReports []WorkloadGateReport, summaries []SummaryRow, controls ControlRuns) []Claim {
	byCell := map[string]Summary{}
	for _, row := range summaries {
		byCell[row.WorkloadID+"|"+row.Backend+"|"+string(row.Cache)] = row.Summary
	}
	unstable := map[string]bool{}
	for _, cell := range controls.UnstableCells {
		unstable[cell] = true
	}
	var claims []Claim
	for _, gate := range gateReports {
		if !gate.Claimable {
			continue // ratio-only-both-passed policy
		}
		if unstable[gate.WorkloadID+"/"+BackendArango] || unstable[gate.WorkloadID+"/"+BackendPostgres] {
			continue // stability control
		}
		a, p, ok := claimCellPair(byCell, gate.WorkloadID)
		if !ok {
			continue
		}
		ratio, err := NewSpeedRatio(a.MedianMillis, p.MedianMillis)
		if err != nil {
			continue
		}
		claims = append(claims, Claim{WorkloadID: gate.WorkloadID, Arango: a, Postgres: p, Ratio: ratio})
	}
	return claims
}

func splitCell(key string) [3]string {
	var out [3]string
	i, j := 0, 0
	for k := 0; k < len(key); k++ {
		if key[k] == '|' {
			out[i] = key[j:k]
			i++
			j = k + 1
		}
	}
	out[i] = key[j:]
	return out
}

// VerifyReport recomputes everything derivable and fails on any mismatch.
// It is the check a synthetic forgery must not survive (ruling ii).
func VerifyReport(report *Report) error {
	if report.Schema != ReportSchemaVersion {
		return fmt.Errorf("benchmark report: unexpected schema %q", report.Schema)
	}
	if !idRe.MatchString(report.Identity.RunID) {
		return fmt.Errorf("benchmark report: bad runId")
	}
	if err := report.Identity.Config.Validate(); err != nil {
		return fmt.Errorf("benchmark report: %w", err)
	}
	if !sha256HexRe.MatchString(report.Identity.DatasetDigest) {
		return fmt.Errorf("benchmark report: dataset digest is not sha256 hex")
	}
	// The leg plan must be total, closed, and scope-consistent. This runs
	// first because the control rules below branch on how many legs ran.
	if err := ValidateLegPlan(report.Plan.Scope, report.Plan.Legs); err != nil {
		return fmt.Errorf("benchmark report: %w", err)
	}
	if err := ValidatePlannedNotRun(report.Plan.PlannedNotRun); err != nil {
		return fmt.Errorf("benchmark report: %w", err)
	}
	if err := ValidatePublicationPlan(report.Plan); err != nil {
		return fmt.Errorf("benchmark report: %w", err)
	}
	publicationExcluded := report.Plan.PublicationExcluded()
	notRun := map[string]string{}
	pubDisclosed := map[string]bool{}
	runLegs := 0
	for _, leg := range report.Plan.Legs {
		if leg.Mode == "not_run" {
			notRun[leg.Backend] = leg.Capability
		} else {
			runLegs++
		}
	}
	if !publicationExcluded && report.Identity.ClickHouseDisabled && runLegs > 0 {
		// The publication chain is ClickHouse-backed end to end: its only
		// publication.Target is ClickHouseBundleStore, and the workspace
		// that seeds every receipt persists only after materialization.
		// A report from a ClickHouse-disabled deployment that claims the
		// chain ran is the 2026-11-13 forgery shape this rule closes.
		return fmt.Errorf("benchmark report: publication chain claimed to run although ClickHouse is disabled")
	}
	if runLegs > 0 {
		if report.Plan.PublicationZeroEvidence() {
			// Pure excluded mode: the battery itself was excluded, so
			// nothing measured and any control value or timing evidence
			// is forged (stolen limits, borrowed floors, ratio cover).
			// The timed mode (chain excluded, battery run) belongs to the
			// else branch below: its evidence is real and must satisfy
			// the control-presence rules.
			c := report.Controls
			if c.StabilityCVLimit != 0 || c.EmptyFloorMillis != 0 || c.SelfRatioArango != 0 ||
				c.SelfRatioPostgres != 0 || c.RatioPolicy != "" || len(c.UnstableCells) != 0 {
				return fmt.Errorf("benchmark report: control values claimed although the publication chain was excluded")
			}
			// Nothing downstream of publication executed, so no timing
			// evidence may exist, every gate arm must read blocked, and
			// each ran leg must carry one disclosure row per battery
			// workload naming the publication capability.
			if len(report.Samples) != 0 || len(report.Summaries) != 0 || len(report.Claims) != 0 {
				return fmt.Errorf("benchmark report: timing evidence claimed although the publication chain was excluded")
			}
			for _, gate := range report.GateReports {
				for _, arm := range []BackendResult{gate.Arango, gate.Postgres} {
					if arm.Outcome != OutcomeLabelBlocked {
						return fmt.Errorf("benchmark report: %s/%s arm shows %s although the publication chain was excluded",
							gate.WorkloadID, arm.Backend, arm.Outcome)
					}
				}
			}
		} else {
			// A run that measured anything must show the controls ran.
			if report.Controls.StabilityCVLimit <= 0 || report.Controls.RatioPolicy == "" {
				return fmt.Errorf("benchmark report: measurement controls were not run (missing stability limit or ratio policy)")
			}
			if report.Controls.EmptyFloorMillis <= 0 {
				return fmt.Errorf("benchmark report: empty-result floor control was not run")
			}
		}
	} else {
		// Planned report: nothing ran, so a control that claims to have run
		// is a forgery path (stolen limits, borrowed floors, ratio cover).
		c := report.Controls
		if c.StabilityCVLimit != 0 || c.EmptyFloorMillis != 0 || c.SelfRatioArango != 0 ||
			c.SelfRatioPostgres != 0 || c.RatioPolicy != "" || len(c.UnstableCells) != 0 {
			return fmt.Errorf("benchmark report: control values claimed although no leg ran")
		}
	}
	// Live versions: a run leg must carry one; a not-run leg may not claim
	// one it could not have observed.
	if _, absent := notRun[BackendArango]; !absent && strings.TrimSpace(report.Identity.ArangoVersion) == "" {
		return fmt.Errorf("benchmark report: arango leg ran without a live arangoVersion")
	}
	if _, absent := notRun[BackendPostgres]; !absent && strings.TrimSpace(report.Identity.PostgresVersion) == "" {
		return fmt.Errorf("benchmark report: postgres leg ran without a live postgresVersion")
	}
	if _, absent := notRun[BackendArango]; absent && report.Identity.ArangoVersion != "" {
		return fmt.Errorf("benchmark report: arangoVersion claimed for a leg that did not run")
	}
	if _, absent := notRun[BackendPostgres]; absent && report.Identity.PostgresVersion != "" {
		return fmt.Errorf("benchmark report: postgresVersion claimed for a leg that did not run")
	}
	// Self-ratio controls: run legs must have measured one near 1.0; a
	// not-run leg's value must be exactly zero (absent), never a forged 1.
	type selfRatio struct {
		backend string
		value   float64
	}
	for _, sr := range []selfRatio{{BackendArango, report.Controls.SelfRatioArango}, {BackendPostgres, report.Controls.SelfRatioPostgres}} {
		if _, absent := notRun[sr.backend]; absent {
			if sr.value != 0 {
				return fmt.Errorf("benchmark report: self-ratio %v reported for %s although its leg did not run", sr.value, sr.backend)
			}
			continue
		}
		if publicationExcluded {
			// The self-ratio control times published reads; with the
			// chain excluded its absence is already enforced (zero) by
			// the control-values rule above.
			continue
		}
		if sr.value <= 0 {
			return fmt.Errorf("benchmark report: %s self-ratio control was not run", sr.backend)
		}
		if math.Abs(sr.value-1) > 0.05 {
			return fmt.Errorf("benchmark report: %s self-ratio %v off 1.0 beyond 5%% — environment unfit for ratios", sr.backend, sr.value)
		}
	}
	// A not-run leg must be disclosed per workload and honest in the gates:
	// blocked arm, zero samples, zero claims. Anything else is a forgery
	// path ("silently measured nothing and claimed it anyway").
	// Live battery exclusions: workloads whose oracle/output agreement is
	// disproven or unproven may never run on a live leg. The registry is
	// deterministic (decoder verdicts only); VerifyReport is what ties
	// every claim of exclusion back to it.
	liveExcluded := LiveBatteryExclusions()
	disclosed := map[string]bool{}
	wlDisclosed := map[string]bool{}
	for _, row := range report.Plan.PlannedNotRun {
		if _, absent := notRun[row.Backend]; !absent {
			// A disclosure row on a ran leg is only honest when it
			// declares the excluded publication chain under an excluding
			// plan, or a registry-excluded workload under its exact
			// named reason; any other row on a ran leg is a forgery
			// path.
			if publicationExcluded && strings.HasPrefix(row.Reason, PublicationChainReason) {
				pubDisclosed[row.WorkloadID+"|"+row.Backend] = true
				continue
			}
			if reason, excludedWorkload := liveExcluded[row.WorkloadID]; excludedWorkload &&
				strings.HasPrefix(row.Reason, WorkloadNotRunReason) {
				if row.Reason != WorkloadNotRunReason+reason {
					return fmt.Errorf("benchmark report: %s/%s workload-not_run disclosure does not match the exclusion registry",
						row.WorkloadID, row.Backend)
				}
				wlDisclosed[row.WorkloadID+"|"+row.Backend] = true
				continue
			}
			return fmt.Errorf("benchmark report: planned/not_run row for %s/%s but its leg ran", row.WorkloadID, row.Backend)
		}
		disclosed[row.WorkloadID+"|"+row.Backend] = true
	}
	// Every ran leg of an excluded run must carry one publication-chain
	// disclosure row per battery workload, exactly as a not-run leg must
	// carry its leg rows. Checked after the loop above has collected them.
	if publicationExcluded {
		for _, leg := range report.Plan.Legs {
			if leg.Mode != "run" {
				continue
			}
			for _, spec := range Battery {
				if !pubDisclosed[spec.ID+"|"+leg.Backend] {
					return fmt.Errorf("benchmark report: %s/%s publication chain excluded without a disclosure row",
						spec.ID, leg.Backend)
				}
			}
		}
	}
	// Live battery law: every battery workload must appear in the gate
	// reports of every ran leg (a silently shrunk live matrix is the same
	// deception as a silently passed one). Excluded workloads must be
	// blocked, marker-carrying, disclosure-backed, and sample-free; the
	// marker may never sit on a workload the harness was allowed to run.
	liveGate := map[string]WorkloadGateReport{}
	for _, gate := range report.GateReports {
		if _, dup := liveGate[gate.WorkloadID]; dup {
			return fmt.Errorf("benchmark report: duplicate gate report for %s", gate.WorkloadID)
		}
		liveGate[gate.WorkloadID] = gate
	}
	for _, leg := range report.Plan.Legs {
		if leg.Mode != "run" {
			continue
		}
		for _, spec := range Battery {
			gate, ok := liveGate[spec.ID]
			if !ok {
				return fmt.Errorf("benchmark report: %s leg ran without a gate report for %s", leg.Backend, spec.ID)
			}
			arm := gate.Arango
			if leg.Backend == BackendPostgres {
				arm = gate.Postgres
			}
			if _, excludedWorkload := liveExcluded[spec.ID]; excludedWorkload {
				if arm.Outcome != OutcomeLabelBlocked {
					return fmt.Errorf("benchmark report: %s/%s shows %s although the workload is excluded from live legs",
						spec.ID, leg.Backend, arm.Outcome)
				}
				if !strings.HasPrefix(gate.ExclusionReason, LiveBatteryExclusionMarker) {
					return fmt.Errorf("benchmark report: excluded workload %s on %s without the live-battery exclusion marker",
						spec.ID, leg.Backend)
				}
				if !wlDisclosed[spec.ID+"|"+leg.Backend] {
					return fmt.Errorf("benchmark report: %s/%s excluded from live legs without a disclosure row",
						spec.ID, leg.Backend)
				}
				continue
			}
			if strings.HasPrefix(gate.ExclusionReason, LiveBatteryExclusionMarker) {
				return fmt.Errorf("benchmark report: %s carries the live-battery exclusion marker but is not an excluded workload",
					spec.ID)
			}
		}
	}
	for _, s := range report.Samples {
		if _, excludedWorkload := liveExcluded[s.WorkloadID]; excludedWorkload {
			return fmt.Errorf("benchmark report: timing samples exist for %s although it is excluded from live legs",
				s.WorkloadID)
		}
	}
	for _, gate := range report.GateReports {
		for _, arm := range []BackendResult{gate.Arango, gate.Postgres} {
			capability, absent := notRun[arm.Backend]
			if !absent {
				continue
			}
			if arm.Outcome != OutcomeLabelBlocked {
				return fmt.Errorf("benchmark report: %s/%s arm shows %s but its leg was not_run (%s)",
					gate.WorkloadID, arm.Backend, arm.Outcome, capability)
			}
			if !disclosed[gate.WorkloadID+"|"+arm.Backend] {
				return fmt.Errorf("benchmark report: %s/%s leg not_run without a planned/not_run disclosure row",
					gate.WorkloadID, arm.Backend)
			}
		}
	}
	for _, s := range report.Samples {
		if _, absent := notRun[s.Backend]; absent {
			return fmt.Errorf("benchmark report: samples exist for %s but its leg was not_run", s.Backend)
		}
	}
	for _, row := range report.Summaries {
		if _, absent := notRun[row.Backend]; absent {
			return fmt.Errorf("benchmark report: summary rows exist for %s but its leg was not_run", row.Backend)
		}
	}
	for _, claim := range report.Claims {
		if _, absent := notRun[BackendArango]; absent {
			return fmt.Errorf("benchmark report: speed claim for %s exists while the arango leg was not_run", claim.WorkloadID)
		}
		if _, absent := notRun[BackendPostgres]; absent {
			return fmt.Errorf("benchmark report: speed claim for %s exists while the postgres leg was not_run", claim.WorkloadID)
		}
	}
	// Status must follow from the gate reports under the declared scope.
	status, reasons := RunStatusScoped(report.GateReports, report.Plan.Scope)
	if report.Status != status {
		return fmt.Errorf("benchmark report: status %q but gates imply %q", report.Status, status)
	}
	if !sameStrings(report.StatusReasons, reasons) {
		return fmt.Errorf("benchmark report: status reasons do not match gate evaluation")
	}
	// Unsupported list must match the gate reports.
	if !sameStrings(report.Unsupported, UnsupportedCases(report.GateReports)) {
		return fmt.Errorf("benchmark report: unsupported list does not match gate reports")
	}
	if report.Exclusions != FormatExclusionList(report.GateReports) {
		return fmt.Errorf("benchmark report: exclusion note does not match gate reports")
	}
	// Every sample is well-formed.
	for _, s := range report.Samples {
		if !ValidBackend(s.Backend) {
			return fmt.Errorf("benchmark report: sample has unknown backend %q", s.Backend)
		}
		if _, ok := workloadColumns(s.WorkloadID); !ok {
			return fmt.Errorf("benchmark report: sample references unknown workload %q", s.WorkloadID)
		}
		if s.Millis < 0 {
			return fmt.Errorf("benchmark report: negative duration sample")
		}
		switch s.Outcome {
		case OutcomePassed, OutcomeFailed, OutcomeTimeout, OutcomeError:
		default:
			return fmt.Errorf("benchmark report: unknown sample outcome %q", s.Outcome)
		}
	}
	// Summaries are recomputed from samples and must match.
	byCell := map[string][]Sample{}
	for _, s := range report.Samples {
		key := s.WorkloadID + "|" + s.Backend + "|" + string(s.Cache)
		byCell[key] = append(byCell[key], s)
	}
	seen := map[string]bool{}
	for _, row := range report.Summaries {
		key := row.WorkloadID + "|" + row.Backend + "|" + string(row.Cache)
		if seen[key] {
			return fmt.Errorf("benchmark report: duplicate summary cell %s", key)
		}
		seen[key] = true
		expected := Summarize(byCell[key])
		if !summaryNearEqual(expected, row.Summary) {
			return fmt.Errorf("benchmark report: summary for %s does not recompute from raw samples", key)
		}
	}
	for key := range byCell {
		if !seen[key] {
			return fmt.Errorf("benchmark report: raw samples for %s have no summary row", key)
		}
	}
	// Gate bag digests are sha256-shaped when present.
	for _, gate := range report.GateReports {
		for _, arm := range []BackendResult{gate.Arango, gate.Postgres} {
			if arm.BagDigest != "" && !sha256HexRe.MatchString(arm.BagDigest) {
				return fmt.Errorf("benchmark report: %s/%s bag digest is not sha256 hex", gate.WorkloadID, arm.Backend)
			}
		}
	}
	// Claims: each must be recomputable, claimable, stable, and both-clean.
	summaryOf := map[string]Summary{}
	for _, row := range report.Summaries {
		summaryOf[row.WorkloadID+"|"+row.Backend+"|"+string(row.Cache)] = row.Summary
	}
	claimableByID := map[string]WorkloadGateReport{}
	for _, gate := range report.GateReports {
		claimableByID[gate.WorkloadID] = gate
	}
	for _, claim := range report.Claims {
		gate, ok := claimableByID[claim.WorkloadID]
		if !ok || !gate.Claimable {
			return fmt.Errorf("benchmark report: claim for %s is not gate-claimable", claim.WorkloadID)
		}
		na, np, pairOK := claimCellPair(summaryOf, claim.WorkloadID)
		if !pairOK {
			return fmt.Errorf("benchmark report: claim for %s rests on cells with failures or no samples", claim.WorkloadID)
		}
		if !summaryNearEqual(na, claim.Arango) || !summaryNearEqual(np, claim.Postgres) {
			return fmt.Errorf("benchmark report: claim for %s cites summaries that do not recompute", claim.WorkloadID)
		}
		expected, err := NewSpeedRatio(na.MedianMillis, np.MedianMillis)
		if err != nil || math.Abs(expected.ArangoOverPostgres-claim.Ratio.ArangoOverPostgres) > 1e-9 {
			return fmt.Errorf("benchmark report: claim ratio for %s does not match medians", claim.WorkloadID)
		}
	}
	// A passed run must contain evidence for every leg that ran, and under
	// both-arms scope at least one speed claim. A postgres-only pass has
	// no claims by construction: it certifies correctness and timings, not
	// a comparison.
	if report.Status == "passed" {
		if report.Plan.Scope == RunScopePostgresOnly {
			if err := requireRanArms(report); err != nil {
				return err
			}
		} else {
			if len(report.Claims) == 0 {
				return fmt.Errorf("benchmark report: passed run with no claims")
			}
			if err := requireBothArms(report); err != nil {
				return err
			}
		}
	}
	return nil
}

func requireRanArms(report *Report) error {
	// postgres-only pass posture: the ran arm must actually have evidence.
	for _, gate := range report.GateReports {
		if gate.Postgres.Outcome == OutcomeLabelPassed {
			return nil
		}
	}
	return fmt.Errorf("benchmark report: postgres-only pass without any passed postgres arm evidence")
}

func requireBothArms(report *Report) error {
	arms := map[string]bool{}
	for _, gate := range report.GateReports {
		if gate.Arango.Outcome == OutcomeLabelPassed {
			arms[BackendArango] = true
		}
		if gate.Postgres.Outcome == OutcomeLabelPassed {
			arms[BackendPostgres] = true
		}
	}
	if !arms[BackendArango] || !arms[BackendPostgres] {
		return fmt.Errorf("benchmark report: passed run missing a backend arm's evidence")
	}
	return nil
}

func summaryNearEqual(a, b Summary) bool {
	const eps = 1e-6
	near := func(x, y float64) bool {
		return math.Abs(x-y) <= eps*math.Max(1, math.Max(math.Abs(x), math.Abs(y)))
	}
	return a.Count == b.Count && a.FailedCount == b.FailedCount &&
		a.ThroughputMeasured == b.ThroughputMeasured &&
		near(a.MedianMillis, b.MedianMillis) && near(a.MeanMillis, b.MeanMillis) &&
		near(a.VarianceMs, b.VarianceMs) && near(a.StdDevMs, b.StdDevMs) &&
		near(a.MinMillis, b.MinMillis) && near(a.MaxMillis, b.MaxMillis) &&
		near(a.P50Millis, b.P50Millis) && near(a.P95Millis, b.P95Millis) &&
		near(a.CoefficientOfVariation, b.CoefficientOfVariation) &&
		near(a.ThroughputPerSecond, b.ThroughputPerSecond) &&
		a.QuantileMethod == b.QuantileMethod
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// MarshalReport renders the canonical JSON bytes of the artifact.
func MarshalReport(report *Report) ([]byte, error) {
	return json.MarshalIndent(report, "", "  ")
}

// LoadReport parses bytes and verifies the artifact on load. This is the
// entry point CI and the node verifier use.
func LoadReport(data []byte) (*Report, error) {
	var report Report
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("benchmark report: %w", err)
	}
	if err := VerifyReport(&report); err != nil {
		return nil, err
	}
	return &report, nil
}
