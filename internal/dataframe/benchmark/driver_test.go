package benchmark

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	// The fake arms serve the whole battery, i.e. a CI-lane deployment
	// with ClickHouse; the excluded-chain shape is pinned separately.
	plan.Publication = &PublicationPlan{Run: true}
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
		ClickHouseEnabled: true,
		Clock:             &scriptedClock{base: time.Unix(1700000000, 0)},
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
	// both arms must have uploaded the dataset and authored every
	// live-battery (proven) workload; registry-excluded workloads are
	// never authored (review r4559430560)
	live := 0
	for _, spec := range Battery {
		if _, excluded := LiveBatteryExclusions()[spec.ID]; !excluded {
			live++
		}
	}
	if live == 0 || live == len(Battery) {
		t.Fatalf("expected a partially excluded live battery, got %d of %d", live, len(Battery))
	}
	for name, f := range map[string]*fakeArm{"arango": fa, "postgres": fp} {
		if f.uploads != 1 {
			t.Fatalf("%s: uploads=%d want 1", name, f.uploads)
		}
		if f.publish != live+2 { // + catalog bootstrap + empty floor
			t.Fatalf("%s: publish=%d want %d", name, f.publish, live+2)
		}
		// the writable wire is closed over aggregate predicates: every
		// published workspace must bind exactly like the server's body bind
		// does, predicate workspaces must carry contributor intent, and no
		// body may smuggle the legacy where field
		bodies := f.publishedBodies()
		if len(bodies) != live+2 {
			t.Fatalf("%s: published bodies=%d want %d", name, len(bodies), live+2)
		}
		// the catalog fetch is bootstrapped by publishing the empty-floor
		// workspace first: the default explorer exists only after the first
		// explorer-config publish, so the builder read depends on this order
		if floor, err := BuildEmptyFloorWorkspace(); err != nil {
			t.Fatal(err)
		} else if string(bodies[0]) != string(floor) {
			t.Fatalf("%s: first publish must be the empty-floor catalog bootstrap", name)
		}
		var withContributor, withWhere int
		for _, body := range bodies {
			var workspace map[string]any
			if err := json.Unmarshal(body, &workspace); err != nil {
				t.Fatalf("%s: published workspace does not bind: %v", name, err)
			}
			if strings.Contains(string(body), `"contributor"`) {
				withContributor++
			}
			if strings.Contains(string(body), `"where"`) {
				withWhere++
			}
		}
		if withWhere != 0 {
			t.Fatalf("%s: a published workspace still carries the legacy where wire", name)
		}
		if withContributor == 0 {
			t.Fatalf("%s: no published workspace carries contributor intent", name)
		}
	}
	// both arms must publish byte-identical workloads (matched arms)
	ta, tp := fa.publishedBodies(), fp.publishedBodies()
	for i := range ta {
		if string(ta[i]) != string(tp[i]) {
			t.Fatalf("arm %d published different bytes on the two drivers:\n%s\n%s", i, ta[i], tp[i])
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

// TestDriverSpawnedArmsProduceClaims pins the operator lane end to end
// (review r4561031154): spawned arms restart every round, so the schedule
// must yield a warm cell per workload per arm and the report must carry
// claims. The dummy binary sleeps; the fake serves the arm routes and
// /readyz on the address the driver chose.
func TestDriverSpawnedArmsProduceClaims(t *testing.T) {
	fa, fp := newFakeArm(BackendArango), newFakeArm(BackendPostgres)
	defer fa.close()
	defer fp.close()
	dir := t.TempDir()
	script := filepath.Join(dir, "dummy-arm.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 300\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	opts := driverOptions(fa, fp, RunScopeBothArms)
	opts.Config = RunConfig{Warmup: 1, Iterations: 10, Concurrency: 1, Rounds: 5}
	opts.StderrDir = dir
	opts.Arango = ArmConfig{BinaryPath: script, ConfigYAML: "listen: unused"}
	opts.Postgres = ArmConfig{BinaryPath: script, ConfigYAML: "listen: unused"}
	fakes := map[string]*fakeArm{BackendArango: fa, BackendPostgres: fp}
	opts.Spawn = func(ctx context.Context, spec ServerSpec, secrets []string, listenAddr string) (*ServerProcess, error) {
		f, ok := fakes[spec.Backend]
		if !ok {
			return nil, fmt.Errorf("no fake arm for backend %q", spec.Backend)
		}
		if err := f.serveOn(listenAddr); err != nil {
			return nil, err
		}
		return SpawnServer(ctx, spec, secrets, listenAddr)
	}
	report, evidence, err := RunBenchmark(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "passed" {
		t.Fatalf("spawned run must pass: %s %v", report.Status, report.StatusReasons)
	}
	if len(report.Claims) == 0 {
		t.Fatal("spawned lane must produce claims from warm cells")
	}
	for _, claim := range report.Claims {
		if claim.Arango.Count < 1 || claim.Postgres.Count < 1 {
			t.Fatalf("claim cites empty cells: %+v", claim)
		}
	}
	if len(evidence) != 2 {
		t.Fatalf("evidence: %+v", evidence)
	}
	for _, e := range evidence {
		if !e.Spawned || e.LogPath == "" {
			t.Fatalf("both arms must be spawned with logs: %+v", e)
		}
	}
}

// TestDriverRunsSetupProofOnBothArms pins the wiring (review r4561644037):
// the live setup path must publish every reconciled receipt through the
// authoring route on both arms and prove matched state before timing.
func TestDriverRunsSetupProofOnBothArms(t *testing.T) {
	fa, fp := newFakeArm(BackendArango), newFakeArm(BackendPostgres)
	defer fa.close()
	defer fp.close()
	report, _, err := RunBenchmark(context.Background(), driverOptions(fa, fp, RunScopeBothArms))
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "passed" {
		t.Fatalf("matched lane must pass: %s %v", report.Status, report.StatusReasons)
	}
	live := 0
	for _, spec := range Battery {
		if _, excluded := LiveBatteryExclusions()[spec.ID]; !excluded {
			live++
		}
	}
	for name, f := range map[string]*fakeArm{"arango": fa, "postgres": fp} {
		publishes := 0
		for _, c := range f.routeCalls() {
			if strings.Contains(c, ":publish:") {
				publishes++
			}
		}
		if publishes != live {
			t.Fatalf("%s: authoring publishes=%d want one per live workload (%d)", name, publishes, live)
		}
	}
}

func firstLiveWorkload(t *testing.T) string {
	t.Helper()
	ex := LiveBatteryExclusions()
	for _, spec := range Battery {
		if _, bad := ex[spec.ID]; !bad {
			return spec.ID
		}
	}
	t.Fatal("no live workload")
	return ""
}

// TestDriverAbortsBeforeTimingOnDivergentSetupProof: a cross-arm draft
// digest divergence must abort the run with no report and no samples;
// timing may never start on a divergent setup.
func TestDriverAbortsBeforeTimingOnDivergentSetupProof(t *testing.T) {
	fa, fp := newFakeArm(BackendArango), newFakeArm(BackendPostgres)
	defer fa.close()
	defer fp.close()
	fa.mu.Lock()
	fa.activeDigest = "sha256:arango-only"
	fa.mu.Unlock()
	report, _, err := RunBenchmark(context.Background(), driverOptions(fa, fp, RunScopeBothArms))
	if err == nil || !strings.Contains(err.Error(), "matched-state proof diverged") {
		t.Fatalf("digest divergence must abort before timing: %v", err)
	}
	if report != nil {
		t.Fatal("no report may exist when the matched-state proof fails")
	}
	// the only preview calls allowed are the two proof previews themselves
	// (one per arm, first live workload); any measured-phase, gate, or
	// later-workload preview would mean timing started on a divergent setup
	previews := 0
	want := ":preview:" + OutputIDFor(firstLiveWorkload(t))
	for _, f := range []*fakeArm{fa, fp} {
		for _, c := range f.routeCalls() {
			if !strings.Contains(c, ":preview:") {
				continue
			}
			previews++
			if !strings.HasSuffix(c, want) {
				t.Fatalf("preview route ran beyond the first workload's setup proof: %s", c)
			}
		}
	}
	if previews != 2 {
		t.Fatalf("proof previews=%d want 2 (one per arm, first workload), got no proof or timing ran", previews)
	}
}
