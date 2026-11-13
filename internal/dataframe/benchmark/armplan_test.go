package benchmark

import (
	"encoding/json"
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
		DatasetDigest:      strings.Repeat("b", 64),
		PostgresVersion:    "PostgreSQL 18.x",
		ClickHouseDisabled: true,
		Config:             RunConfig{Warmup: 1, Iterations: 4, Concurrency: 1, Rounds: 2},
	}
	controls := ControlRuns{
		StabilityCVLimit: StabilityCVLimit,
		// the arango self-ratio cannot exist without the leg: 0 = absent,
		// never a forged 1.0
		SelfRatioArango: 0, SelfRatioPostgres: 1.0,
		EmptyFloorMillis: 0.4, RatioPolicy: "ratio-only-both-passed",
	}
	plan := PostgresOnlyPlan(testArangoCapability)
	plan.PlannedNotRun = PlannedNotRunForBackends([]string{BackendArango})
	for i := range plan.PlannedNotRun {
		plan.PlannedNotRun[i].Reason = "leg not run: " + testArangoCapability
	}
	gate := postgresOnlyGate(WorkloadFilterActiveGender, strings.Repeat("c", 64))
	var samples []Sample
	samples = append(samples, sample(BackendPostgres, WorkloadFilterActiveGender, OutcomePassed, 5, true))
	for i := 0; i < 4; i++ {
		samples = append(samples, sample(BackendPostgres, WorkloadFilterActiveGender, OutcomePassed, 10+float64(i), false))
	}
	report, err := BuildReport(identity, plan, controls, []WorkloadGateReport{gate}, samples)
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
			r.Samples = append(r.Samples, sample(BackendArango, WorkloadFilterActiveGender, OutcomePassed, 20, false))
			r.Summaries = append(r.Summaries, SummaryRow{
				WorkloadID: WorkloadFilterActiveGender, Backend: BackendArango, Cache: CacheWarm,
				Summary: Summarize([]Sample{sample(BackendArango, WorkloadFilterActiveGender, OutcomePassed, 20, false)}),
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
				WorkloadID: WorkloadFilterActiveGender,
				Arango:     Summarize([]Sample{sample(BackendArango, WorkloadFilterActiveGender, OutcomePassed, 40, false)}),
				Postgres:   Summarize([]Sample{sample(BackendPostgres, WorkloadFilterActiveGender, OutcomePassed, 10, false)}),
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
		r.Plan.PlannedNotRun = []PlannedNotRun{{WorkloadID: WorkloadFilterActiveGender, Backend: BackendArango, Reason: "pretend we skipped it"}}
	}))
}

func TestRenderMarkdown(t *testing.T) {
	both := RenderMarkdown(validReport(t))
	for _, want := range []string{
		"Status: passed", "Scope: `both-arms`", "ratio-only-both-passed",
		"arango/postgres", "Empty-result floor", "1.000",
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
