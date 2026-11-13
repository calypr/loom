package benchmark

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Offline driver protocol tests: fake arms serve the public routes; the
// gate EVALUATION itself is covered by gates_test/oracle_test, so these
// tests pin orchestration: route coverage, matched arms, control runs,
// per-scope report shape, and fail-closed aborts.

func driverOptions(fArango, fPostgres *fakeArm, scope string) DriverOptions {
	plan := BothArmsPlan()
	if scope == RunScopePostgresOnly {
		plan = PostgresOnlyPlan("no ArangoDB service in worker container")
	}
	plan.PlannedNotRun = PlannedNotRunForBackends(plan.NotRunBackends())
	for i := range plan.PlannedNotRun {
		plan.PlannedNotRun[i].Reason = "leg not run: worker container lacks the service"
	}
	// every workload output must answer; empty_floor must be zero rows
	for _, f := range []*fakeArm{fArango, fPostgres} {
		if f == nil {
			continue
		}
		f.scopedToken = "Bearer scoped"
		for _, spec := range Battery {
			if _, ok := f.rows[OutputIDFor(spec.ID)]; !ok {
				f.rows[OutputIDFor(spec.ID)] = []map[string]any{{"patient_id": "p1"}}
			}
		}
		f.rows["empty_floor"] = []map[string]any{}
	}
	opts := DriverOptions{
		RunID: "bench-driver-test", StartedAt: time.Unix(1700000000, 0).UTC(),
		Plan: plan, Preset: PresetTiny,
		Params:       DatasetParams{Preset: PresetTiny, Seed: 7, Project: "proj", Generation: "gen1", AuthResourcePath: "/programs/p/projects/proj/x"},
		Config:       RunConfig{Warmup: 1, Iterations: 4, Concurrency: 1, Rounds: 2},
		PreviewLimit: 1000, SourceCommit: strings.Repeat("a", 40),
		UnrestrictedToken: "Bearer unauth", ScopedToken: "Bearer scoped",
		Clock: &scriptedClock{base: time.Unix(1700000000, 0)},
		VersionProbe: func(ctx context.Context, cfg ArmConfig, backend string) (string, error) {
			if backend == BackendArango {
				return "3.12.x-fake", nil
			}
			return "PostgreSQL 18.x-fake", nil
		},
		// Protocol seam only: real evaluation is pinned elsewhere.
		Gates: func(workloadID string, columns []string, expected *CanonicalBag, arango, postgres BackendResult, arangoRows, postgresRows []map[string]any, idMap *StorageIDMap) WorkloadGateReport {
			return WorkloadGateReport{WorkloadID: workloadID, Claimable: true,
				Arango: arango, Postgres: postgres,
				Gates: []GateResult{{Gate: GateBagEquality, Pass: true}}}
		},
		SingleArmGates: func(workloadID, backend string, columns []string, expected *CanonicalBag, arm BackendResult, rows []map[string]any, idMap *StorageIDMap, blockedDetail string) WorkloadGateReport {
			report := WorkloadGateReport{WorkloadID: workloadID, Claimable: true,
				Gates: []GateResult{{Gate: GateOracleRows, Pass: true}}}
			if backend == BackendArango {
				report.Arango = arm
				report.Postgres = BackendResult{Backend: BackendPostgres, Outcome: OutcomeLabelBlocked, Detail: blockedDetail}
			} else {
				report.Postgres = arm
				report.Arango = BackendResult{Backend: BackendArango, Outcome: OutcomeLabelBlocked, Detail: blockedDetail}
			}
			return report
		},
	}
	if fArango != nil {
		opts.Arango = ArmConfig{BaseURL: fArango.server.URL}
	}
	if fPostgres != nil {
		opts.Postgres = ArmConfig{BaseURL: fPostgres.server.URL}
	}
	return opts
}

func TestDriverBothArmsProtocol(t *testing.T) {
	fa, fp := newFakeArm(BackendArango), newFakeArm(BackendPostgres)
	defer fa.close()
	defer fp.close()
	report, evidence, err := RunBenchmark(context.Background(), driverOptions(fa, fp, RunScopeBothArms))
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "passed" || len(report.Claims) == 0 {
		t.Fatalf("expected passing both-arms report with claims: %s %v", report.Status, report.StatusReasons)
	}
	if report.Identity.ArangoVersion == "" || report.Identity.PostgresVersion == "" {
		t.Fatal("live versions must be recorded")
	}
	if report.Controls.EmptyFloorMillis <= 0 || report.Controls.SelfRatioArango < 1 || report.Controls.SelfRatioPostgres < 1 {
		t.Fatalf("controls must be genuinely run: %+v", report.Controls)
	}
	if len(evidence) != 2 || evidence[0].Backend != BackendArango {
		t.Fatalf("evidence: %+v", evidence)
	}
	// both arms must have uploaded the dataset and authored every workload
	for name, f := range map[string]*fakeArm{"arango": fa, "postgres": fp} {
		if f.uploads != 1 {
			t.Fatalf("%s: uploads=%d want 1", name, f.uploads)
		}
		if f.publish != len(Battery)+1 { // + empty floor
			t.Fatalf("%s: publish=%d want %d", name, f.publish, len(Battery)+1)
		}
	}
	// round-trip the artifact
	if _, err := LoadReport(mustMarshal(t, report)); err != nil {
		t.Fatalf("driver report must verify: %v", err)
	}
}

// scriptedClock makes every elapsed measurement exactly 10ms: protocol
// tests must not depend on host jitter. The self-ratio failing above in
// this file is exactly why the clock is injectable.
type scriptedClock struct {
	base time.Time
	n    int
}

func (c *scriptedClock) Now() time.Time {
	c.n++
	return c.base.Add(time.Duration(c.n) * time.Millisecond)
}

func (c *scriptedClock) Since(time.Time) time.Duration { return 10 * time.Millisecond }

func TestDriverPostgresOnlyProtocol(t *testing.T) {
	fp := newFakeArm(BackendPostgres)
	defer fp.close()
	report, evidence, err := RunBenchmark(context.Background(), driverOptions(nil, fp, RunScopePostgresOnly))
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "passed" {
		t.Fatalf("postgres-only pass expected: %v", report.StatusReasons)
	}
	if len(report.Claims) != 0 {
		t.Fatal("postgres-only must have no speed claims")
	}
	if report.Identity.ArangoVersion != "" || report.Controls.SelfRatioArango != 0 {
		t.Fatal("unrun arango leg must carry no version and no self-ratio")
	}
	for _, s := range report.Samples {
		if s.Backend != BackendPostgres {
			t.Fatalf("sample for unrun backend: %+v", s)
		}
	}
	if len(evidence) != 1 || evidence[0].Backend != BackendPostgres {
		t.Fatalf("evidence: %+v", evidence)
	}
	if _, err := LoadReport(mustMarshal(t, report)); err != nil {
		t.Fatalf("postgres-only driver report must verify: %v", err)
	}
}

func TestDriverAbortsBeforeArtifactOnAuthLeak(t *testing.T) {
	fa, fp := newFakeArm(BackendArango), newFakeArm(BackendPostgres)
	defer fa.close()
	defer fp.close()
	fp.leakScoped = true // scoped principal sees rows on postgres: denial broken
	_, _, err := RunBenchmark(context.Background(), driverOptions(fa, fp, RunScopeBothArms))
	if err == nil || !strings.Contains(err.Error(), "authorization gate") {
		t.Fatalf("auth leak must abort before any artifact, got %v", err)
	}
}

func TestDriverRejectsDivergentExplorerIDs(t *testing.T) {
	fa, fp := newFakeArm(BackendArango), newFakeArm(BackendPostgres)
	defer fa.close()
	defer fp.close()
	fa.explorerID = "exp-other"
	_, _, err := RunBenchmark(context.Background(), driverOptions(fa, fp, RunScopeBothArms))
	if err == nil || !strings.Contains(err.Error(), "not matched") {
		t.Fatalf("divergent explorer ids must abort the matched comparison, got %v", err)
	}
}

func TestDriverRequiresRealSourceCommit(t *testing.T) {
	fp := newFakeArm(BackendPostgres)
	defer fp.close()
	opts := driverOptions(nil, fp, RunScopePostgresOnly)
	opts.SourceCommit = "abc"
	if _, _, err := RunBenchmark(context.Background(), opts); err == nil {
		t.Fatal("short source commit must be refused")
	}
}
