package benchmark

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The publication chain is ClickHouse-backed end to end (its only
// publication.Target is ClickHouseBundleStore; the workspace that seeds
// every receipt persists only after materialization). In a ClickHouse-less
// deployment the honest artifact is an excluded-chain report: spawn and
// upload ran for real, everything downstream is planned/not_run with the
// capability named, and zero timing evidence exists. These tests pin the
// honest shape and every deception path around it.

func excludedPlan(scope string) RunPlan {
	var plan RunPlan
	if scope == RunScopePostgresOnly {
		plan = PostgresOnlyPlan("no ArangoDB service in this execution environment; go-arango CI lane targets this leg")
	} else {
		plan = BothArmsPlan()
	}
	plan.Publication = &PublicationPlan{Run: false, Capability: PublicationCapabilityCH}
	plan.PlannedNotRun = PlannedNotRunForBackends(plan.NotRunBackends())
	for i := range plan.PlannedNotRun {
		plan.PlannedNotRun[i].Reason = "leg not run: " + plan.Legs[0].Capability
	}
	plan.PlannedNotRun = append(plan.PlannedNotRun, PublicationDisclosures(plan.Legs, PublicationCapabilityCH)...)
	AppendWorkloadDisclosures(&plan, LiveBatteryExclusions())
	return plan
}

func excludedGates(plan RunPlan) []WorkloadGateReport {
	return ApplyExcludedWorkloadGateReports(
		PublicationGateReports(plan.Legs, PublicationCapabilityCH),
		ExcludedWorkloadGateReports(plan.Legs, LiveBatteryExclusions()))
}

func excludedIdentity(runID string) RunIdentity {
	return RunIdentity{
		RunID: runID, StartedAt: time.Unix(1700000000, 0).UTC(),
		Preset: PresetTiny, SourceCommit: strings.Repeat("a", 40),
		DatasetDigest: strings.Repeat("b", 64),
		// The excluded chain exists precisely because ClickHouse is
		// NOT deployed; the identity must say so.
		ClickHouseDisabled: true,
		Config:             RunConfig{Warmup: 1, Iterations: 4, Concurrency: 1, Rounds: 2},
	}
}

func TestExcludedChainReportIsHonestAndVerifies(t *testing.T) {
	plan := excludedPlan(RunScopeBothArms)
	identity := excludedIdentity("bench-excl-a")
	identity.ArangoVersion = "3.12.x-fake"
	identity.PostgresVersion = "PostgreSQL 18.x-fake"
	report, err := BuildReport(identity, plan, ControlRuns{}, excludedGates(plan), nil)
	if err != nil {
		t.Fatalf("honest excluded-chain report must build: %v", err)
	}
	if len(report.Samples) != 0 || len(report.Summaries) != 0 || len(report.Claims) != 0 {
		t.Fatalf("excluded chain must carry zero timing evidence: %d samples, %d summaries, %d claims",
			len(report.Samples), len(report.Summaries), len(report.Claims))
	}
	liveExcluded := LiveBatteryExclusions()
	for _, gate := range report.GateReports {
		_, isExcludedWorkload := liveExcluded[gate.WorkloadID]
		for _, arm := range []BackendResult{gate.Arango, gate.Postgres} {
			if arm.Outcome != OutcomeLabelBlocked {
				t.Fatalf("%s/%s must be blocked, got %s", gate.WorkloadID, arm.Backend, arm.Outcome)
			}
			if isExcludedWorkload {
				// Both exclusions are true; the row must name the
				// workload reason and carry the live-battery marker.
				if !strings.Contains(arm.Detail, WorkloadNotRunReason) {
					t.Fatalf("%s/%s detail must name the workload exclusion: %q", gate.WorkloadID, arm.Backend, arm.Detail)
				}
				if !strings.HasPrefix(gate.ExclusionReason, LiveBatteryExclusionMarker) {
					t.Fatalf("%s must carry the live-battery marker: %q", gate.WorkloadID, gate.ExclusionReason)
				}
				continue
			}
			if !strings.Contains(arm.Detail, PublicationCapabilityCH) {
				t.Fatalf("%s/%s detail must name the capability: %q", gate.WorkloadID, arm.Backend, arm.Detail)
			}
		}
	}
	if want := 2*len(Battery) + 2*len(liveExcluded); len(report.Plan.PlannedNotRun) != want {
		t.Fatalf("both running legs must each disclose the whole battery plus the exclusions: %d rows want %d", len(report.Plan.PlannedNotRun), want)
	}
	// The artifact must survive marshal + LoadReport verification.
	if _, err := LoadReport(mustMarshal(t, report)); err != nil {
		t.Fatalf("excluded-chain artifact must verify: %v", err)
	}
}

func TestExcludedChainForgeryPaths(t *testing.T) {
	build := func(t *testing.T) *Report {
		t.Helper()
		plan := excludedPlan(RunScopePostgresOnly)
		identity := excludedIdentity("bench-excl-forge")
		identity.PostgresVersion = "PostgreSQL 18.x-fake"
		report, err := BuildReport(identity, plan, ControlRuns{}, excludedGates(plan), nil)
		if err != nil {
			t.Fatalf("baseline excluded report: %v", err)
		}
		return report
	}
	cases := []struct {
		name    string
		tamper  func(*testing.T, *Report)
		wantErr string
	}{
		{"stability limit borrowed into an excluded run", func(t *testing.T, r *Report) {
			r.Controls.StabilityCVLimit = StabilityCVLimit
		}, "control values claimed although the publication chain was excluded"},
		{"empty floor borrowed into an excluded run", func(t *testing.T, r *Report) {
			r.Controls.EmptyFloorMillis = 0.4
		}, "control values claimed although the publication chain was excluded"},
		{"ratio policy cover for an excluded run", func(t *testing.T, r *Report) {
			r.Controls.RatioPolicy = "ratio-only-both-passed"
		}, "control values claimed although the publication chain was excluded"},
		{"self-ratio forged onto an excluded run leg", func(t *testing.T, r *Report) {
			r.Controls.SelfRatioPostgres = 1.0
		}, "control values claimed although the publication chain was excluded"},
		{"samples fabricated into an excluded run", func(t *testing.T, r *Report) {
			r.Samples = append(r.Samples, sample(BackendPostgres, primaryTiming(), OutcomePassed, 10, false))
		}, "timing evidence claimed although the publication chain was excluded"},
		{"one disclosure row quietly dropped", func(t *testing.T, r *Report) {
			// drop the last postgres publication-chain row
			drop, kept := -1, make([]PlannedNotRun, 0, len(r.Plan.PlannedNotRun))
			for i, row := range r.Plan.PlannedNotRun {
				if drop < 0 && row.Backend == BackendPostgres && strings.HasPrefix(row.Reason, PublicationChainReason) {
					drop = i
					continue
				}
				kept = append(kept, row)
			}
			if drop < 0 {
				t.Fatal("baseline lacks a postgres publication disclosure row")
			}
			r.Plan.PlannedNotRun = kept
		}, "without a disclosure row"},
		{"one gate arm flipped to passed", func(t *testing.T, r *Report) {
			r.GateReports[0].Postgres.Outcome = OutcomeLabelPassed
		}, "although the publication chain was excluded"},
		{"chain excluded without naming the capability", func(t *testing.T, r *Report) {
			r.Plan.Publication.Capability = "   "
		}, "publication chain excluded without a named capability"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report := build(t)
			tc.tamper(t, report)
			if err := VerifyReport(report); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

// The ClickHouse-disabled rule closes the 2026-11-13 forgery shape: a
// full timing report stamped from a deployment that cannot host the
// publication chain.
func TestCHDisabledCannotClaimPublicationRan(t *testing.T) {
	report := validReport(t) // CI-lane fixture: ClickHouse deployed
	if err := VerifyReport(report); err != nil {
		t.Fatalf("CI-lane baseline must verify: %v", err)
	}
	report.Identity.ClickHouseDisabled = true
	if err := VerifyReport(report); err == nil ||
		!strings.Contains(err.Error(), "publication chain claimed to run although ClickHouse is disabled") {
		t.Fatalf("want CH-disabled refusal, got %v", err)
	}
	// An explicitly excluded chain is the honest alternative; this
	// timing-bearing fixture must still be refused under it.
	report.Plan.Publication = &PublicationPlan{Run: false, Capability: PublicationCapabilityCH}
	if err := VerifyReport(report); err == nil ||
		!strings.Contains(err.Error(), "although the publication chain was excluded") {
		t.Fatalf("excluded plan must refuse the timing evidence, got %v", err)
	}
}

// A publication-prefixed disclosure row is legal only under an excluding
// plan; elsewhere it is a "silently measured nothing and called it
// disclosed" path.
func TestPublicationRowsOnlyOnExcludingPlans(t *testing.T) {
	report := validReport(t)
	report.Plan.PlannedNotRun = append(report.Plan.PlannedNotRun, PlannedNotRun{
		WorkloadID: WorkloadFilterActiveGender, Backend: BackendArango,
		Reason: PublicationChainReason + PublicationCapabilityCH,
	})
	if err := VerifyReport(report); err == nil || !strings.Contains(err.Error(), "but its leg ran") {
		t.Fatalf("want refusal of publication row on a non-excluding plan, got %v", err)
	}
}

// The driver must run the pre-publication stages for real in the excluded
// shape (arms up, dataset uploaded, versions probed) and refuse to publish
// or measure anything downstream.
func TestDriverExcludedPublicationProtocol(t *testing.T) {
	fa, fp := newFakeArm(BackendArango), newFakeArm(BackendPostgres)
	defer fa.close()
	defer fp.close()
	opts := driverOptions(fa, fp, RunScopeBothArms)
	opts.ClickHouseEnabled = false
	opts.Plan.Publication = &PublicationPlan{Run: false, Capability: PublicationCapabilityCH}
	opts.Plan.PlannedNotRun = append(opts.Plan.PlannedNotRun,
		PublicationDisclosures(opts.Plan.Legs, PublicationCapabilityCH)...)
	report, evidence, err := RunBenchmark(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	for name, f := range map[string]*fakeArm{"arango": fa, "postgres": fp} {
		if f.uploads != 1 {
			t.Fatalf("%s: upload is a non-publication stage and must run: uploads=%d want 1", name, f.uploads)
		}
		if f.publish != 0 {
			t.Fatalf("%s: excluded chain must not publish: publish=%d want 0", name, f.publish)
		}
	}
	if len(report.Samples) != 0 || len(report.Claims) != 0 {
		t.Fatalf("excluded driver report must be timing-free: %d samples, %d claims", len(report.Samples), len(report.Claims))
	}
	c := report.Controls
	if c.StabilityCVLimit != 0 || c.EmptyFloorMillis != 0 || c.SelfRatioArango != 0 ||
		c.SelfRatioPostgres != 0 || c.RatioPolicy != "" || len(c.UnstableCells) != 0 {
		t.Fatalf("excluded driver report must carry zero controls: %+v", c)
	}
	if len(evidence) != 2 {
		t.Fatalf("both arms must be evidenced: %+v", evidence)
	}
	for _, ev := range evidence {
		if ev.Version == "" {
			t.Fatalf("live version probe is a non-publication stage: %+v", ev)
		}
	}
	if _, err := LoadReport(mustMarshal(t, report)); err != nil {
		t.Fatalf("driver excluded-chain artifact must verify: %v", err)
	}
}

func TestDriverRefusesIncoherentPublicationClaims(t *testing.T) {
	fp := newFakeArm(BackendPostgres)
	defer fp.close()
	t.Run("run claimed without ClickHouse", func(t *testing.T) {
		opts := driverOptions(nil, fp, RunScopePostgresOnly)
		opts.ClickHouseEnabled = false
		opts.Plan.Publication = &PublicationPlan{Run: true}
		if _, _, err := RunBenchmark(context.Background(), opts); err == nil ||
			!strings.Contains(err.Error(), "refusing to claim it ran without ClickHouseEnabled") {
			t.Fatalf("want refusal, got %v", err)
		}
	})
	t.Run("excluded without capability", func(t *testing.T) {
		opts := driverOptions(nil, fp, RunScopePostgresOnly)
		opts.Plan.Publication = &PublicationPlan{Run: false}
		if _, _, err := RunBenchmark(context.Background(), opts); err == nil ||
			!strings.Contains(err.Error(), "without a named capability") {
			t.Fatalf("want refusal, got %v", err)
		}
	})
}

// The named capability must track the CI ClickHouse pin so the harness,
// the disclosure text, and the CI lane cannot drift apart silently.
func TestPublicationCapabilityTracksCIPin(t *testing.T) {
	if ClickHouseCIImage != "clickhouse/clickhouse-server:25.3" {
		t.Fatalf("CI image drifted from the repository pin; update the constant deliberately: %s", ClickHouseCIImage)
	}
	if PublicationCapabilityCH != "CH-25.3-unavailable-in-container" {
		t.Fatalf("capability must name the CI ClickHouse version: %s", PublicationCapabilityCH)
	}
}
