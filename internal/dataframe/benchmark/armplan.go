package benchmark

import (
	"fmt"
	"sort"
	"strings"
)

// Run scope and leg plans make "we could only run the PostgreSQL arm" an
// explicit, verified property of the artifact instead of a silent absence.
// The Arango legs are CI-targeted per the coordinator ACK: in worker
// sessions they ship as planned/not_run rows with the named missing
// capability, never as skipped-to-green rows.

const (
	RunScopeBothArms     = "both-arms"
	RunScopePostgresOnly = "postgres-only"
	// RunScopePlanned is the fully-disclosed pre-window artifact: no leg
	// ran, every leg is not_run with its missing capability named, and the
	// report can carry no claims. It exists so a blocked session (e.g.
	// PG-PG-AUTH-PATH-DISCOVERY-ALLGENS) publishes an honest planned
	// report instead of staying silent or dressing an empty matrix up as
	// a pass.
	RunScopePlanned = "planned"
)

func ValidScope(scope string) bool {
	return scope == RunScopeBothArms || scope == RunScopePostgresOnly || scope == RunScopePlanned
}

// LegPlan declares one backend arm's participation in the run.
type LegPlan struct {
	Backend string `json:"backend"`
	Mode    string `json:"mode"` // run | not_run
	// Capability names what was missing for not_run rows, e.g.
	// "no ArangoDB service in worker container; go-arango CI lane targets
	// this leg" — required and non-empty for not_run.
	Capability string `json:"capability,omitempty"`
}

// PlannedNotRun is a per-workload disclosure row.
type PlannedNotRun struct {
	WorkloadID string `json:"workloadId"`
	Backend    string `json:"backend"`
	Reason     string `json:"reason"`
}

// ClickHouseCIVersion is the single source of truth for the ClickHouse
// line the benchmark's publication stages target. It must equal the image
// tag pinned by the repository CI services (currently
// clickhouse/clickhouse-server:25.3 in .github/workflows/tests.yaml); when
// CI bumps the pin, this constant, ClickHouseCIImage, and
// PublicationCapabilityCH follow automatically. One constant keeps the
// harness, the named capability, and the CI lane describing one substrate.
const ClickHouseCIVersion = "25.3"

// ClickHouseCIImage is the container image the CI lanes run the
// publication-dependent stages against. No container engine exists in the
// worker container, so publication runs are planned/not_run there
// (coordinator ruling 2026-11-13: no ClickHouse chasing); the paired CI
// lane provisions the service and runs the whole chain for real.
const ClickHouseCIImage = "clickhouse/clickhouse-server:" + ClickHouseCIVersion

// PublicationCapabilityCH names the missing capability when the
// publication chain cannot execute because ClickHouse is not deployed.
// Publication is structurally ClickHouse-backed: publication.Target has
// exactly one production implementation (ClickHouseBundleStore), and the
// repository publish that seeds every Explorer draft materializes through
// it. The 2026-11-13 local rehearsal disproved the earlier premise that
// the authoring battery could run with clickhouse.enabled=false: the
// first explorer-config publish answers 422 MATERIALIZATION_FAILED
// without a target, so there is no chain-less battery — a run either
// deploys ClickHouse (mode run) or discloses everything downstream of
// upload as not_run (mode excluded).
const PublicationCapabilityCH = "CH-" + ClickHouseCIVersion + "-unavailable-in-container"

// PublicationPlan declares whether the publication chain ran. It is a
// plan-level property so VerifyReport, not the driver's self-description,
// decides whether a report may carry timing evidence.
type PublicationPlan struct {
	// Run is true when publication executed (CI lane with ClickHouse);
	// the timing battery only ever runs alongside it, because authoring
	// publishes through the chain.
	Run bool `json:"run"`
	// Capability names why the chain did not run when Run is false;
	// PublicationCapabilityCH is the canonical value for a ClickHouse-less
	// deployment. A run-mode plan must leave it empty.
	Capability string `json:"capability,omitempty"`
}

// PublicationZeroEvidence reports the excluded mode: the chain and
// everything downstream of upload were excluded, so the run measured
// nothing and a report carrying samples, summaries, claims, control
// values, or non-blocked arms would be forged. This is what VerifyReport
// pins.
func (p RunPlan) PublicationZeroEvidence() bool {
	return p.PublicationExcluded()
}

// ApplyExcludedWorkloadGateReports replaces any gate report for an
// excluded workload with its disclosed blocked row, so no construction
// path can leave a marker-less (or worse, claiming) row for a workload
// that was never allowed to run.
func ApplyExcludedWorkloadGateReports(gates, excludedReports []WorkloadGateReport) []WorkloadGateReport {
	excludedIDs := map[string]bool{}
	for _, g := range excludedReports {
		excludedIDs[g.WorkloadID] = true
	}
	out := make([]WorkloadGateReport, 0, len(gates)+len(excludedReports))
	for _, g := range gates {
		if !excludedIDs[g.WorkloadID] {
			out = append(out, g)
		}
	}
	return append(out, excludedReports...)
}

// PublicationExcluded reports whether the plan declares the publication
// chain not run. A nil plan means the field predates the exclusion model
// and is treated as run for leg/scope checks; VerifyReport still rejects a
// ClickHouse-disabled report claiming it.
func (p RunPlan) PublicationExcluded() bool {
	return p.Publication != nil && !p.Publication.Run
}

// PublicationChainReason prefixes disclosure rows created by excluding
// the publication chain; VerifyReport recognizes the exclusion by it.
const PublicationChainReason = "publication chain not run: "

// WorkloadNotRunReason prefixes disclosure rows for a single workload
// excluded from live legs (disproven or unproven oracle/output agreement);
// VerifyReport recognizes the exclusion by it.
const WorkloadNotRunReason = "workload not run: "

// LegNotRunReason prefixes disclosure rows and blocked-arm details for a
// leg that did not execute at all.
const LegNotRunReason = "leg not run: "

// LiveBatteryExclusionMarker prefixes the ExclusionReason of gate reports
// built by ExcludedWorkloadGateReports. RunStatusScoped skips these rows
// (they are disclosed absences, not failures); VerifyReport is what ties
// the marker back to LiveBatteryExclusions() so it cannot be forged onto
// a workload the harness could have run.
const LiveBatteryExclusionMarker = "live battery exclusion: "

// AppendWorkloadDisclosures adds the disclosure rows for excluded
// workloads on every running leg, skipping rows already present. Both
// benchcli (preflight) and the driver (final plan copy) call it, so it
// must be idempotent.
func AppendWorkloadDisclosures(plan *RunPlan, excluded map[string]string) {
	seen := map[string]bool{}
	for _, row := range plan.PlannedNotRun {
		seen[row.WorkloadID+"|"+row.Backend+"|"+row.Reason] = true
	}
	for _, row := range WorkloadDisclosures(plan.Legs, excluded) {
		if !seen[row.WorkloadID+"|"+row.Backend+"|"+row.Reason] {
			plan.PlannedNotRun = append(plan.PlannedNotRun, row)
		}
	}
}

// WorkloadDisclosures expands the per-workload exclusions across every
// running leg, so an excluded workload cannot quietly keep gate or timing
// rows on a leg that was never allowed to run it.
func WorkloadDisclosures(legs []LegPlan, excluded map[string]string) []PlannedNotRun {
	var rows []PlannedNotRun
	for _, leg := range legs {
		if leg.Mode != "run" {
			continue
		}
		for _, spec := range Battery {
			if reason, ok := excluded[spec.ID]; ok {
				rows = append(rows, PlannedNotRun{
					WorkloadID: spec.ID, Backend: leg.Backend,
					Reason: WorkloadNotRunReason + reason,
				})
			}
		}
	}
	return rows
}

// ExcludedWorkloadGateReports builds the blocked gate reports for
// excluded workloads: running legs blocked by the workload reason,
// not-run legs by their leg capability. Claimable stays false.
func ExcludedWorkloadGateReports(legs []LegPlan, excluded map[string]string) []WorkloadGateReport {
	legCapability := map[string]string{}
	for _, leg := range legs {
		if leg.Mode != "run" {
			legCapability[leg.Backend] = leg.Capability
		}
	}
	detail := func(backend, workloadID string) string {
		if cap, blocked := legCapability[backend]; blocked {
			return LegNotRunReason + cap
		}
		return WorkloadNotRunReason + excluded[workloadID]
	}
	var reports []WorkloadGateReport
	for _, spec := range Battery {
		if _, ok := excluded[spec.ID]; !ok {
			continue
		}
		reports = append(reports, WorkloadGateReport{
			WorkloadID:      spec.ID,
			Arango:          BackendResult{Backend: BackendArango, Outcome: OutcomeLabelBlocked, Detail: detail(BackendArango, spec.ID)},
			Postgres:        BackendResult{Backend: BackendPostgres, Outcome: OutcomeLabelBlocked, Detail: detail(BackendPostgres, spec.ID)},
			ExclusionReason: LiveBatteryExclusionMarker + detail(BackendArango, spec.ID) + " | " + detail(BackendPostgres, spec.ID),
		})
	}
	return reports
}

// PublicationDisclosures expands the publication-chain exclusion across
// every battery workload of every running leg, so "publish excluded"
// cannot quietly leave workload rows claiming measurement on a chain that
// never executed. Legs that did not run are covered by their own leg
// capability rows instead.
func PublicationDisclosures(legs []LegPlan, capability string) []PlannedNotRun {
	var rows []PlannedNotRun
	for _, leg := range legs {
		if leg.Mode != "run" {
			continue
		}
		for _, spec := range Battery {
			rows = append(rows, PlannedNotRun{
				WorkloadID: spec.ID, Backend: leg.Backend,
				Reason: PublicationChainReason + capability,
			})
		}
	}
	return rows
}

// PublicationGateReports builds the honest per-workload gate reports for
// a run whose publication chain was excluded: every arm of every workload
// blocked — not-run legs by their leg capability, run legs by the
// publication capability — nothing claimable.
func PublicationGateReports(legs []LegPlan, capability string) []WorkloadGateReport {
	legCapability := map[string]string{}
	for _, leg := range legs {
		if leg.Mode != "run" {
			legCapability[leg.Backend] = leg.Capability
		}
	}
	detail := func(backend string) string {
		if cap, blocked := legCapability[backend]; blocked {
			return LegNotRunReason + cap
		}
		return PublicationChainReason + capability
	}
	var reports []WorkloadGateReport
	for _, spec := range Battery {
		reports = append(reports, WorkloadGateReport{
			WorkloadID:      spec.ID,
			Arango:          BackendResult{Backend: BackendArango, Outcome: OutcomeLabelBlocked, Detail: detail(BackendArango)},
			Postgres:        BackendResult{Backend: BackendPostgres, Outcome: OutcomeLabelBlocked, Detail: detail(BackendPostgres)},
			ExclusionReason: "publication chain excluded: " + detail(BackendArango) + " | " + detail(BackendPostgres),
		})
	}
	return reports
}

// ValidatePublicationPlan checks the publication declaration is honest in
// shape: an excluded chain must name its missing capability, and a
// run-mode chain must not carry one (a capability names what did not run).
func ValidatePublicationPlan(plan RunPlan) error {
	if plan.Publication == nil || plan.Publication.Run {
		if plan.Publication != nil && plan.Publication.Run && strings.TrimSpace(plan.Publication.Capability) != "" {
			return fmt.Errorf("benchmark: publication plan claims the chain ran while naming a missing capability %q", plan.Publication.Capability)
		}
		return nil
	}
	if strings.TrimSpace(plan.Publication.Capability) == "" {
		return fmt.Errorf("benchmark: publication chain excluded without a named capability")
	}
	return nil
}

// RunPlan is the declared participation of each arm for a run, stored in
// the report and re-checked by VerifyReport: "we could only run the
// PostgreSQL arm" must be a verified property, never a silent absence.
type RunPlan struct {
	Scope         string          `json:"scope"`
	Legs          []LegPlan       `json:"legs"`
	PlannedNotRun []PlannedNotRun `json:"plannedNotRun,omitempty"`
	// Publication declares whether the ClickHouse-backed publication chain
	// executed. nil predates the exclusion model: treated as run for
	// leg/scope checks, but VerifyReport still refuses a publication-run
	// claim from a ClickHouse-disabled identity.
	Publication *PublicationPlan `json:"publication,omitempty"`
}

// BothArmsPlan is the CI-target plan: both services execute every leg.
func BothArmsPlan() RunPlan {
	return RunPlan{
		Scope: RunScopeBothArms,
		Legs: []LegPlan{
			{Backend: BackendArango, Mode: "run"},
			{Backend: BackendPostgres, Mode: "run"},
		},
	}
}

// PostgresOnlyPlan is the worker-session plan: the Arango leg ships as
// not_run with its missing capability named verbatim in every disclosure
// row, per the coordinator ACK (Arango rows stay CI-targeted).
func PostgresOnlyPlan(arangoCapability string) RunPlan {
	return RunPlan{
		Scope: RunScopePostgresOnly,
		Legs: []LegPlan{
			{Backend: BackendArango, Mode: "not_run", Capability: arangoCapability},
			{Backend: BackendPostgres, Mode: "run"},
		},
	}
}

// PlannedPlan is the pre-window disclosure plan: both legs not_run, each
// with its missing capability named verbatim (finding ids welcome, e.g.
// "blocked by PG-PG-AUTH-PATH-DISCOVERY-ALLGENS: ..."). A planned run
// measures nothing and claims nothing; VerifyReport enforces that.
func PlannedPlan(arangoCapability, postgresCapability string) RunPlan {
	return RunPlan{
		Scope: RunScopePlanned,
		Legs: []LegPlan{
			{Backend: BackendArango, Mode: "not_run", Capability: arangoCapability},
			{Backend: BackendPostgres, Mode: "not_run", Capability: postgresCapability},
		},
	}
}

// PlannedNotRunForBackends expands one disclosure reason across every
// battery workload for each not-run leg, so no row can be quietly dropped.
func PlannedNotRunForBackends(backends []string) []PlannedNotRun {
	var rows []PlannedNotRun
	for _, backend := range backends {
		for _, spec := range Battery {
			rows = append(rows, PlannedNotRun{WorkloadID: spec.ID, Backend: backend, Reason: "leg not run"})
		}
	}
	return rows
}

// PlannedGateReports builds the honest per-workload gate reports for a
// planned run: every arm blocked with its leg capability in the detail,
// nothing claimable. VerifyReport's not-run rules (blocked arm, disclosure
// row, zero samples/claims) then bind the artifact.
func PlannedGateReports(legs []LegPlan) []WorkloadGateReport {
	capability := map[string]string{}
	for _, leg := range legs {
		capability[leg.Backend] = leg.Capability
	}
	detail := func(backend string) string { return LegNotRunReason + capability[backend] }
	var reports []WorkloadGateReport
	for _, spec := range Battery {
		reports = append(reports, WorkloadGateReport{
			WorkloadID: spec.ID,
			Arango:     BackendResult{Backend: BackendArango, Outcome: OutcomeLabelBlocked, Detail: detail(BackendArango)},
			Postgres:   BackendResult{Backend: BackendPostgres, Outcome: OutcomeLabelBlocked, Detail: detail(BackendPostgres)},
			// Claimable stays false: a planned run measured nothing.
			ExclusionReason: "planned run: " + detail(BackendArango) + " | " + detail(BackendPostgres),
		})
	}
	return reports
}

// ValidateLegPlan checks the plan is total, closed, and honest.
func ValidateLegPlan(scope string, legs []LegPlan) error {
	if !ValidScope(scope) {
		return fmt.Errorf("benchmark: unknown run scope %q", scope)
	}
	byBackend := map[string]LegPlan{}
	for _, leg := range legs {
		if !ValidBackend(leg.Backend) {
			return fmt.Errorf("benchmark: leg plan has unknown backend %q", leg.Backend)
		}
		if leg.Mode != "run" && leg.Mode != "not_run" {
			return fmt.Errorf("benchmark: leg %s has unknown mode %q", leg.Backend, leg.Mode)
		}
		if leg.Mode == "not_run" && strings.TrimSpace(leg.Capability) == "" {
			return fmt.Errorf("benchmark: leg %s is not_run without a named capability", leg.Backend)
		}
		if _, dup := byBackend[leg.Backend]; dup {
			return fmt.Errorf("benchmark: duplicate leg for %s", leg.Backend)
		}
		byBackend[leg.Backend] = leg
	}
	for _, backend := range []string{BackendArango, BackendPostgres} {
		if _, ok := byBackend[backend]; !ok {
			return fmt.Errorf("benchmark: leg plan missing %s", backend)
		}
	}
	switch scope {
	case RunScopePlanned:
		for _, backend := range []string{BackendArango, BackendPostgres} {
			if byBackend[backend].Mode != "not_run" || strings.TrimSpace(byBackend[backend].Capability) == "" {
				return fmt.Errorf("benchmark: planned scope requires %s not_run WITH a named capability", backend)
			}
		}
	case RunScopePostgresOnly:
		if byBackend[BackendPostgres].Mode != "run" {
			return fmt.Errorf("benchmark: postgres-only scope requires the postgres leg to run")
		}
		if byBackend[BackendArango].Mode != "not_run" || strings.TrimSpace(byBackend[BackendArango].Capability) == "" {
			return fmt.Errorf("benchmark: postgres-only scope requires the arango leg not_run WITH a named capability")
		}
	default:
		for _, backend := range []string{BackendArango, BackendPostgres} {
			if byBackend[backend].Mode != "run" {
				return fmt.Errorf("benchmark: both-arms scope requires %s to run", backend)
			}
		}
	}
	return nil
}

// ValidatePlannedNotRun checks disclosure rows reference real workloads
// and backends with non-empty reasons.
func ValidatePlannedNotRun(rows []PlannedNotRun) error {
	for _, row := range rows {
		if _, ok := batterySpec(row.WorkloadID); !ok {
			return fmt.Errorf("benchmark: planned/not_run references unknown workload %q", row.WorkloadID)
		}
		if !ValidBackend(row.Backend) {
			return fmt.Errorf("benchmark: planned/not_run references unknown backend %q", row.Backend)
		}
		if strings.TrimSpace(row.Reason) == "" {
			return fmt.Errorf("benchmark: planned/not_run for %s/%s has no reason", row.WorkloadID, row.Backend)
		}
	}
	return nil
}

// NotRunBackends lists the backends whose legs did not execute.
func (p RunPlan) NotRunBackends() []string {
	var out []string
	for _, leg := range p.Legs {
		if leg.Mode == "not_run" {
			out = append(out, leg.Backend)
		}
	}
	return out
}

// ExpectedOutcomeForScope returns the outcome an arm must show when its leg
// did not run, so gates can distinguish "expectedly absent" from "failed".
func ExpectedOutcomeForScope(leg LegPlan) (WorkloadOutcome, string) {
	if leg.Mode == "not_run" {
		return OutcomeLabelBlocked, LegNotRunReason + leg.Capability
	}
	return "", ""
}

// RunStatusScoped aggregates gate reports under the run scope. In
// postgres-only scope the arango arm's blocked outcome is the EXPECTED
// posture (its capability is named in the leg plan) and does not fail the
// run; everything else behaves as in both-arms scope.
func RunStatusScoped(reports []WorkloadGateReport, scope string) (string, []string) {
	if len(reports) == 0 {
		return "failed", []string{"no workloads were evaluated; an empty matrix is not a pass"}
	}
	claimable := 0
	byID := append([]WorkloadGateReport(nil), reports...)
	sort.Slice(byID, func(i, j int) bool { return byID[i].WorkloadID < byID[j].WorkloadID })
	var reasons []string
	for _, report := range byID {
		if scope == RunScopePlanned {
			// Nothing ran. Both arms must carry the expected blocked
			// posture with their capability in the detail; anything else
			// (a passed arm, a measured-looking row) is a forgery signal.
			if report.Arango.Outcome != OutcomeLabelBlocked || report.Postgres.Outcome != OutcomeLabelBlocked {
				reasons = append(reasons, fmt.Sprintf("%s: planned run must show both arms blocked, got arango=%s postgres=%s",
					report.WorkloadID, report.Arango.Outcome, report.Postgres.Outcome))
			}
			continue
		}
		armOutcomes := []WorkloadOutcome{report.Arango.Outcome, report.Postgres.Outcome}
		if scope == RunScopePostgresOnly {
			// Only the PostgreSQL arm decides; the Arango arm must be the
			// expected blocked posture, anything else is a forgery signal.
			if report.Arango.Outcome != OutcomeLabelBlocked {
				reasons = append(reasons, report.WorkloadID+": postgres-only run must show the arango arm blocked, got "+string(report.Arango.Outcome))
				continue
			}
			armOutcomes = []WorkloadOutcome{report.Postgres.Outcome}
		}
		if strings.HasPrefix(report.ExclusionReason, LiveBatteryExclusionMarker) {
			// A disclosed live-battery exclusion is neither a pass nor
			// a failure: it is a named absence. VerifyReport ties the
			// marker to LiveBatteryExclusions() and requires the
			// blocked posture, a disclosure row, and zero samples for
			// every such row; a marker on anything else is refused
			// there, so skipping here cannot launder a blocked run.
			continue
		}
		blocked := false
		for _, outcome := range armOutcomes {
			if outcome == OutcomeLabelBlocked {
				blocked = true
			}
		}
		if blocked {
			reasons = append(reasons, report.WorkloadID+": blocked")
			continue
		}
		if !report.Claimable {
			reasons = append(reasons, report.WorkloadID+": "+report.ExclusionReason)
			continue
		}
		claimable++
	}
	if len(reasons) > 0 {
		return "failed", reasons
	}
	if claimable == 0 {
		return "failed", []string{"no workload completed its expected arms; unsupported-only matrices are not passes"}
	}
	return "passed", nil
}

// EvaluateGatesSingleArm evaluates the scope-applicable gates for one arm
// with an independent oracle (postgres-only sessions). Cross-backend gates
// cannot apply; that is recorded in the gate detail, not hidden. The other
// arm must already be published as blocked by the caller.
func EvaluateGatesSingleArm(workloadID, backend string, columns []string, expected *CanonicalBag, arm BackendResult, rows []map[string]any, idMap *StorageIDMap, blockedDetail string) WorkloadGateReport {
	blocked := BackendResult{Backend: otherBackend(backend), Outcome: OutcomeLabelBlocked, Detail: blockedDetail}
	report := WorkloadGateReport{WorkloadID: workloadID, Claimable: true}
	if backend == BackendArango {
		report.Arango, report.Postgres = arm, blocked
	} else {
		report.Arango, report.Postgres = blocked, arm
	}
	add := func(gate GateName, pass bool, format string, args ...any) {
		result := GateResult{Gate: gate, Pass: pass}
		if !pass {
			result.Detail = fmt.Sprintf(format, args...)
			report.Claimable = false
			if report.ExclusionReason == "" {
				report.ExclusionReason = string(gate) + ": " + result.Detail
			}
		}
		report.Gates = append(report.Gates, result)
	}
	if arm.Outcome != OutcomeLabelPassed {
		add(GateArmOutcome, false, "%s arm outcome=%s (error=%s)", arm.Backend, arm.Outcome, arm.ErrorCode)
		return report
	}
	add(GateColumnContract, equalStrings(arm.Columns, columns), "%s columns deviate from the authored contract", arm.Backend)
	mapped := idMap.Apply(arm.Columns, rows)
	bag := Bag(CanonicalizeRows(arm.Columns, mapped))
	add(GateBagEquality, arm.BagDigest == bag.Digest(), "%s bag digest does not reproduce from its rows", arm.Backend)
	if arm.Truncated {
		add(GateCompleteness, false, "%s arm consumed a truncated output", arm.Backend)
	}
	if expected != nil {
		if bag.Digest() != expected.Digest() {
			firstA, firstB, _ := FirstDifference(bag, *expected)
			add(GateOracleRows, false, "%s bag differs from independent oracle at arm=%s oracle=%s", arm.Backend, firstA, firstB)
		}
	} else {
		report.Gates = append(report.Gates, GateResult{Gate: GateOracleRows, Pass: true, Detail: "profile has no hand oracle; single-arm scope publishes timings only"})
	}
	report.Gates = append(report.Gates, GateResult{Gate: GateBagEquality, Pass: true, Detail: "cross-backend equality is CI-targeted under " + RunScopePostgresOnly + " scope"})
	return report
}

func otherBackend(backend string) string {
	if backend == BackendArango {
		return BackendPostgres
	}
	return BackendArango
}
