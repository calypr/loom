package benchmark

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// The postgres-only scope is where "we only ran PostgreSQL" lives. It must
// be a verified property: the un-run arm stays blocked, disclosed per
// workload, sample-free, claim-free, version-free — every deception path
// below has to RED.

const testArangoCapability = "no ArangoDB service in worker container; go-arango CI lane targets this leg"

func postgresOnlyGate(workloadID string, bagDigest string) WorkloadGateReport {
	return WorkloadGateReport{
		WorkloadID: workloadID, Claimable: true,
		Arango:   BackendResult{Backend: BackendArango, Outcome: OutcomeLabelBlocked, Detail: "leg not run: " + testArangoCapability},
		Postgres: BackendResult{Backend: BackendPostgres, Outcome: OutcomeLabelPassed, RowCount: 5, BagDigest: bagDigest},
		Gates:    []GateResult{{Gate: GateOracleRows, Pass: true}, {Gate: GateBagEquality, Pass: true, Detail: "cross-backend equality is CI-targeted"}},
	}
}

func validPostgresOnlyReport(t *testing.T) *Report {
	t.Helper()
	identity := RunIdentity{
		RunID: "bench-2026-11-13-pg-a", StartedAt: time.Unix(1700000000, 0).UTC(),
		Preset: PresetTiny, SourceCommit: strings.Repeat("a", 40),
		DatasetDigest:   strings.Repeat("b", 64),
		PostgresVersion: "PostgreSQL 18.x",
		// Timing needs the publication chain, which is ClickHouse-
		// backed: this fixture describes the CI lane with ClickHouse.
		ClickHouseDisabled: false,
		Config:             RunConfig{Warmup: 1, Iterations: 4, Concurrency: 1, Rounds: 2},
	}
	plan := PostgresOnlyPlan(testArangoCapability)
	plan.Publication = &PublicationPlan{Run: true}
	plan.PlannedNotRun = PlannedNotRunForBackends([]string{BackendArango})
	for i := range plan.PlannedNotRun {
		plan.PlannedNotRun[i].Reason = "leg not run: " + testArangoCapability
	}
	liveExcluded := LiveBatteryExclusions()
	var gates []WorkloadGateReport
	var samples []Sample
	for _, spec := range Battery {
		if _, excluded := liveExcluded[spec.ID]; excluded {
			continue
		}
		gates = append(gates, postgresOnlyGate(spec.ID, strings.Repeat("c", 64)))
		samples = append(samples, sample(BackendPostgres, spec.ID, OutcomePassed, 5, true))
		// Halves [10, 10, 10.3, 10.3]: honest self-ratio 1.03. The
		// verifier recomputes controls from raw samples (review
		// r4561644049), so the fixture states what the samples show.
		for i := 0; i < 4; i++ {
			offset := 0.0
			if i >= 2 {
				offset = 0.3
			}
			samples = append(samples, sample(BackendPostgres, spec.ID, OutcomePassed, 10+offset, false))
		}
	}
	controls := ControlRuns{
		StabilityCVLimit: StabilityCVLimit,
		UnstableCells:    ControlUnstableCells(samples, StabilityCVLimit),
		// the arango self-ratio cannot exist without the leg: 0 = absent,
		// never a forged 1.0
		SelfRatioArango: 0, EmptyFloorMillis: 0.4, RatioPolicy: "ratio-only-both-passed",
	}
	if value, ok := ControlSelfRatio(samples, BackendPostgres); ok {
		controls.SelfRatioPostgres = value
	} else {
		t.Fatal("postgres self-ratio not computable from fixture samples")
	}
	gates = ApplyExcludedWorkloadGateReports(gates, ExcludedWorkloadGateReports(plan.Legs, liveExcluded))
	AppendWorkloadDisclosures(&plan, liveExcluded)
	report, err := BuildReport(identity, plan, controls, gates, samples)
	if err != nil {
		t.Fatalf("honest postgres-only report must build: %v", err)
	}
	return report
}

func TestPostgresOnlyReportRoundTrips(t *testing.T) {
	report := validPostgresOnlyReport(t)
	loaded, err := LoadReport(mustMarshal(t, report))
	if err != nil {
		t.Fatalf("honest postgres-only report must verify: %v", err)
	}
	if loaded.Status != "passed" {
		t.Fatalf("postgres-only pass must be a pass: %+v", loaded.StatusReasons)
	}
	if len(loaded.Claims) != 0 {
		t.Fatalf("no speed claims may exist without the arango leg: %+v", loaded.Claims)
	}
}

func TestPostgresOnlyRunStatusIgnoresExpectedBlockedArm(t *testing.T) {
	status, reasons := RunStatusScoped([]WorkloadGateReport{postgresOnlyGate(WorkloadFilterActiveGender, strings.Repeat("c", 64))}, RunScopePostgresOnly)
	if status != "passed" || len(reasons) != 0 {
		t.Fatalf("expected pass, got %s %v", status, reasons)
	}
	// An unexpected arango outcome under postgres-only scope is a forgery
	// signal, not a pass.
	gate := postgresOnlyGate(WorkloadFilterActiveGender, strings.Repeat("c", 64))
	gate.Arango.Outcome = OutcomeLabelPassed
	if status, _ := RunStatusScoped([]WorkloadGateReport{gate}, RunScopePostgresOnly); status != "failed" {
		t.Fatal("arango arm passed under postgres-only scope must fail the run")
	}
	// Empty matrix is never a pass, even scoped.
	if status, _ := RunStatusScoped(nil, RunScopePostgresOnly); status != "failed" {
		t.Fatal("empty scoped matrix must not pass")
	}
}

func TestLegPlanValidation(t *testing.T) {
	if err := ValidateLegPlan(RunScopePostgresOnly, []LegPlan{
		{Backend: BackendArango, Mode: "not_run"},
		{Backend: BackendPostgres, Mode: "run"},
	}); err == nil {
		t.Fatal("not_run leg without capability must be refused")
	}
	if err := ValidateLegPlan(RunScopeBothArms, BothArmsPlan().Legs); err != nil {
		t.Fatalf("both-arms plan must validate: %v", err)
	}
	if err := ValidateLegPlan(RunScopePostgresOnly, BothArmsPlan().Legs); err == nil {
		t.Fatal("postgres-only scope must refuse an executed arango leg")
	}
	if err := ValidateLegPlan("everything", BothArmsPlan().Legs); err == nil {
		t.Fatal("unknown scope must be refused")
	}
}

func forgeWith(t *testing.T, base func(*testing.T) *Report, mutate func(*Report)) func(*testing.T) {
	return func(t *testing.T) {
		t.Helper()
		report := base(t)
		mutate(report)
		data, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := LoadReport(data); err == nil {
			t.Fatal("forged report survived verification")
		}
	}
}

func TestPostgresOnlyForgerySuite(t *testing.T) {
	cases := map[string]func(*Report){
		"arango-arm-faked-passed": func(r *Report) {
			r.GateReports[0].Arango.Outcome = OutcomeLabelPassed
		},
		"arango-samples-smuggled": func(r *Report) {
			r.Samples = append(r.Samples, sample(BackendArango, primaryTiming(), OutcomePassed, 20, false))
			r.Summaries = append(r.Summaries, SummaryRow{
				WorkloadID: primaryTiming(), Backend: BackendArango, Cache: CacheWarm,
				Summary: Summarize([]Sample{sample(BackendArango, primaryTiming(), OutcomePassed, 20, false)}),
			})
		},
		"disclosure-rows-dropped": func(r *Report) {
			r.Plan.PlannedNotRun = nil
		},
		"capability-blanked": func(r *Report) {
			r.Plan.Legs[0].Capability = "  "
		},
		"arango-version-claimed-for-unrun-leg": func(r *Report) {
			r.Identity.ArangoVersion = "3.12.x"
		},
		"postgres-version-hidden": func(r *Report) {
			r.Identity.PostgresVersion = ""
		},
		"speed-claim-injected-without-arango": func(r *Report) {
			r.Claims = append(r.Claims, Claim{
				WorkloadID: primaryTiming(),
				Arango:     Summarize([]Sample{sample(BackendArango, primaryTiming(), OutcomePassed, 40, false)}),
				Postgres:   Summarize([]Sample{sample(BackendPostgres, primaryTiming(), OutcomePassed, 10, false)}),
				Ratio:      SpeedRatio{ArangoOverPostgres: 4.0},
			})
		},
		"unrun-selfratio-forged-to-one": func(r *Report) {
			r.Controls.SelfRatioArango = 1.0
		},
		"scope-lie-arango-leg-marked-run": func(r *Report) {
			r.Plan.Legs[0].Mode = "run"
		},
	}
	for name, mutate := range cases {
		t.Run(name, forgeWith(t, validPostgresOnlyReport, mutate))
	}
	// The converse: disclosure rows for a leg that DID run are lies too.
	t.Run("disclosure-for-ran-leg", forgeWith(t, validReport, func(r *Report) {
		r.Plan.PlannedNotRun = []PlannedNotRun{{WorkloadID: primaryTiming(), Backend: BackendArango, Reason: WorkloadNotRunReason + LiveBatteryExclusions()[WorkloadDeriveAgeBand]}}
	}))
}

func TestRenderMarkdown(t *testing.T) {
	bothReport := validReport(t)
	selfRatioLine := fmt.Sprintf("arango=%.3f, postgres=%.3f",
		bothReport.Controls.SelfRatioArango, bothReport.Controls.SelfRatioPostgres)
	both := RenderMarkdown(bothReport)
	for _, want := range []string{
		"Status: passed", "Scope: `both-arms`", "ratio-only-both-passed",
		"arango/postgres", "Empty-result floor", selfRatioLine,
	} {
		if !strings.Contains(both, want) {
			t.Fatalf("both-arms markdown missing %q", want)
		}
	}
	only := RenderMarkdown(validPostgresOnlyReport(t))
	for _, want := range []string{
		"Scope: `postgres-only`", "not_run", testArangoCapability,
		"not observed (leg not run", "no cross-backend speed claims",
		"never silently skipped",
	} {
		if !strings.Contains(only, want) {
			t.Fatalf("postgres-only markdown missing %q", want)
		}
	}
	if strings.Contains(only, "arango/postgres |") {
		t.Fatal("postgres-only markdown must not contain a claims table")
	}
}

// The planned scope is the pre-window disclosure artifact: nothing ran, so
// nothing may be claimed — not a sample, not a summary, not a control, not
// a version. These tests pin the honest shape and every deception path.

func plannedValidReport(t *testing.T) *Report {
	t.Helper()
	identity := RunIdentity{
		RunID: "bench-planned-a", StartedAt: time.Unix(1700000000, 0).UTC(),
		Preset: PresetTiny, SourceCommit: strings.Repeat("a", 40),
		DatasetDigest:      strings.Repeat("b", 64),
		ClickHouseDisabled: true,
		Config:             RunConfig{Warmup: 1, Iterations: 4, Concurrency: 1, Rounds: 2},
	}
	plan := PlannedPlan(
		"no ArangoDB service in this execution environment; go-arango CI lane targets this leg",
		"blocked by PG-PG-AUTH-PATH-DISCOVERY-ALLGENS: the scoped calypr lane cannot ingest on postgres")
	plan.PlannedNotRun = PlannedNotRunForBackends(plan.NotRunBackends())
	report, err := BuildReport(identity, plan, ControlRuns{}, PlannedGateReports(plan.Legs), nil)
	if err != nil {
		t.Fatalf("valid planned report build failed: %v", err)
	}
	if report.Status != "failed" || len(report.Claims) != 0 {
		t.Fatalf("planned report must be failed with zero claims, got %s / %d claims", report.Status, len(report.Claims))
	}
	if want := 2 * len(Battery); len(report.Plan.PlannedNotRun) != want || len(report.GateReports) != len(Battery) {
		t.Fatalf("planned disclosure must cover the whole battery both legs: %d rows, %d gates, want %d/%d",
			len(report.Plan.PlannedNotRun), len(report.GateReports), want, len(Battery))
	}
	return report
}

func TestPlannedReportRoundTrips(t *testing.T) {
	loaded := roundTrip(t, plannedValidReport(t))
	if loaded.Plan.Scope != RunScopePlanned || loaded.Status != "failed" || len(loaded.Claims) != 0 {
		t.Fatalf("planned round trip drifted: %+v", loaded.Plan)
	}
	if strings.Contains(RenderMarkdown(loaded), "arango/postgres |") {
		t.Fatal("planned markdown must not contain a claims table")
	}
}

func forgePlanned(t *testing.T, mutate func(*Report)) func(*testing.T) {
	return func(t *testing.T) {
		t.Helper()
		report := plannedValidReport(t)
		mutate(report)
		data, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := LoadReport(data); err == nil {
			t.Fatal("forged planned report survived verification")
		}
	}
}

func TestPlannedForgerySuite(t *testing.T) {
	cases := map[string]func(*Report){
		"planned-leg-marked-run":     func(r *Report) { r.Plan.Legs[1].Mode = "run" },
		"planned-capability-blanked": func(r *Report) { r.Plan.Legs[1].Capability = " " },
		"planned-arm-shown-passed": func(r *Report) {
			r.GateReports[0].Postgres.Outcome = OutcomeLabelPassed
			r.GateReports[0].Postgres.RowCount = 5
			r.GateReports[0].Claimable = true
			r.Status = "passed"
			r.StatusReasons = nil
		},
		"planned-status-flipped-passed": func(r *Report) { r.Status = "passed" },
		"planned-stolen-control-limits": func(r *Report) { r.Controls.StabilityCVLimit = StabilityCVLimit },
		"planned-stolen-empty-floor":    func(r *Report) { r.Controls.EmptyFloorMillis = 0.4 },
		"planned-forged-self-ratio":     func(r *Report) { r.Controls.SelfRatioPostgres = 1.0 },
		"planned-forged-ratio-policy":   func(r *Report) { r.Controls.RatioPolicy = "ratio-only-both-passed" },
		"planned-samples-injected": func(r *Report) {
			r.Samples = append(r.Samples, sample(BackendPostgres, r.GateReports[0].WorkloadID, OutcomePassed, 10, false))
		},
		"planned-summaries-injected": func(r *Report) {
			r.Summaries = append(r.Summaries, SummaryRow{WorkloadID: r.GateReports[0].WorkloadID, Backend: BackendPostgres, Cache: CacheWarm, Summary: Summary{Count: 4, MedianMillis: 9}})
		},
		"planned-claim-injected": func(r *Report) {
			r.Claims = append(r.Claims, Claim{WorkloadID: r.GateReports[0].WorkloadID, Ratio: SpeedRatio{ArangoOverPostgres: 2.0}})
		},
		"planned-disclosure-row-dropped": func(r *Report) { r.Plan.PlannedNotRun = r.Plan.PlannedNotRun[1:] },
		"planned-version-claimed":        func(r *Report) { r.Identity.PostgresVersion = "PostgreSQL 18.x" },
		"planned-arango-version-claimed": func(r *Report) { r.Identity.ArangoVersion = "3.12.x" },
	}
	for name, mutate := range cases {
		t.Run(name, forgePlanned(t, mutate))
	}
}

func TestPlannedLegPlanValidation(t *testing.T) {
	plan := PlannedPlan("capability a", "capability b")
	if err := ValidateLegPlan(plan.Scope, plan.Legs); err != nil {
		t.Fatalf("valid planned plan refused: %v", err)
	}
	if err := ValidateLegPlan(RunScopePlanned, []LegPlan{
		{Backend: BackendArango, Mode: "not_run", Capability: "a"},
		{Backend: BackendPostgres, Mode: "run"},
	}); err == nil {
		t.Fatal("planned scope accepted a run leg")
	}
	if err := ValidateLegPlan(RunScopePlanned, []LegPlan{
		{Backend: BackendArango, Mode: "not_run", Capability: "a"},
		{Backend: BackendPostgres, Mode: "not_run"},
	}); err == nil {
		t.Fatal("planned scope accepted an unnamed capability")
	}
	// The live driver must refuse to run a planned scope at all.
	if _, _, err := RunBenchmark(nil, DriverOptions{Plan: PlannedPlan("a", "b")}); err == nil ||
		!strings.Contains(err.Error(), "planned scope") {
		t.Fatalf("driver must refuse planned scope, got %v", err)
	}
}
