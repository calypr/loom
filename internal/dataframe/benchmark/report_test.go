package benchmark

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Ruling (ii): the verifier must refuse a synthetic forgery. Every derived
// number in the report is RECOMPUTED from raw evidence by VerifyReport, and
// this suite proves each tamper path REDs.

func validReport(t *testing.T) *Report {
	t.Helper()
	identity := RunIdentity{
		RunID: "bench-2026-11-13-a", StartedAt: time.Unix(1700000, 0).UTC(),
		Preset: PresetTiny, SourceCommit: strings.Repeat("a", 40),
		DatasetDigest: strings.Repeat("b", 64),
		ArangoVersion: "3.12.x", PostgresVersion: "PostgreSQL 18.x",
		// A timing report describes the CI lane with ClickHouse
		// deployed; the worker container can only build excluded-chain
		// reports (see the publication-exclusion forgery cases).
		ClickHouseDisabled: false,
		Config:             RunConfig{Warmup: 1, Iterations: 4, Concurrency: 1, Rounds: 2},
	}
	controls := ControlRuns{
		StabilityCVLimit: StabilityCVLimit, UnstableCells: nil,
		SelfRatioArango: 1.0, SelfRatioPostgres: 1.0,
		EmptyFloorMillis: 0.4, RatioPolicy: "ratio-only-both-passed",
	}
	// Live battery law: proven workloads carry passing gates and samples;
	// registry-excluded workloads carry disclosed blocked rows and
	// disclosure rows, never samples.
	liveExcluded := LiveBatteryExclusions()
	var gates []WorkloadGateReport
	var samples []Sample
	for _, spec := range Battery {
		if _, excluded := liveExcluded[spec.ID]; excluded {
			continue
		}
		gates = append(gates, WorkloadGateReport{
			WorkloadID: spec.ID, Claimable: true,
			Arango:   BackendResult{Backend: BackendArango, Outcome: OutcomeLabelPassed, RowCount: 5, BagDigest: strings.Repeat("c", 64)},
			Postgres: BackendResult{Backend: BackendPostgres, Outcome: OutcomeLabelPassed, RowCount: 5, BagDigest: strings.Repeat("c", 64)},
			Gates:    []GateResult{{Gate: GateBagEquality, Pass: true}},
		})
		for _, backend := range []string{BackendArango, BackendPostgres} {
			samples = append(samples, sample(backend, spec.ID, OutcomePassed, 5, true)) // warmup
			base := 20.0
			if backend == BackendPostgres {
				base = 10.0
			}
			for i := 0; i < 4; i++ {
				samples = append(samples, sample(backend, spec.ID, OutcomePassed, base+float64(i), false))
			}
		}
	}
	timingPlan := BothArmsPlan()
	timingPlan.Publication = &PublicationPlan{Run: true}
	gates = append(gates, ExcludedWorkloadGateReports(timingPlan.Legs, liveExcluded)...)
	AppendWorkloadDisclosures(&timingPlan, liveExcluded)
	report, err := BuildReport(identity, timingPlan, controls, gates, samples)
	if err != nil {
		t.Fatalf("valid report build failed: %v", err)
	}
	return report
}

// primaryTiming returns the workload validReport puts first among the
// proven (timed) workloads; forgery cases mutate GateReports[0].
func primaryTiming() string {
	for _, spec := range Battery {
		if _, excluded := LiveBatteryExclusions()[spec.ID]; !excluded {
			return spec.ID
		}
	}
	panic("live battery has no proven workloads")
}

func gateForID(r *Report, workloadID string) *WorkloadGateReport {
	for i := range r.GateReports {
		if r.GateReports[i].WorkloadID == workloadID {
			return &r.GateReports[i]
		}
	}
	panic("no gate report for " + workloadID)
}

func roundTrip(t *testing.T, report *Report) *Report {
	t.Helper()
	data, err := MarshalReport(report)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadReport(data)
	if err != nil {
		t.Fatalf("round trip failed: %v", err)
	}
	return loaded
}

func TestReportRoundTrips(t *testing.T) {
	report := validReport(t)
	loaded := roundTrip(t, report)
	if loaded.Status != "passed" || len(loaded.Claims) != len(Battery)-len(LiveBatteryExclusions()) {
		t.Fatalf("expected one claim per proven workload on a passed run: %+v", loaded)
	}
	if loaded.Claims[0].Ratio.ArangoOverPostgres <= 0 {
		t.Fatal("ratio missing")
	}
}

func forge(t *testing.T, mutate func(*Report)) func(*testing.T) {
	return func(t *testing.T) {
		t.Helper()
		report := validReport(t)
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

func TestReportForgerySuite(t *testing.T) {
	cases := map[string]func(*Report){
		"summary-median-tampered": func(r *Report) {
			r.Summaries[0].Summary.MedianMillis = 0.001
		},
		"failed-sample-deleted-from-raw": func(r *Report) {
			// Append a timed-out sample so the summary legitimately has a
			// failure, then delete it from raw: summary says FailedCount 1.
			r.Samples = append(r.Samples, sample(BackendArango, primaryTiming(), OutcomeTimeout, 9000, false))
			sums := r.Summaries
			r.Summaries = nil
			for _, row := range sums {
				if row.Backend == BackendArango {
					row.Summary = Summarize(r.Samples)
				}
				r.Summaries = append(r.Summaries, row)
			}
			var kept []Sample
			for _, s := range r.Samples {
				if s.Outcome != OutcomeTimeout {
					kept = append(kept, s)
				}
			}
			r.Samples = kept
		},
		"status-flipped-to-passed": func(r *Report) {
			r.GateReports[0].Claimable = false
			r.GateReports[0].ExclusionReason = "forged exclusion"
			r.Status = "passed" // gates now imply failed
		},
		"gate-flipped-to-claimable-with-failed-arm": func(r *Report) {
			r.GateReports[0].Postgres.Outcome = OutcomeLabelPassed
			r.Status = "passed"
			r.GateReports[0].Claimable = true
			// Postgres summaries legitimately exist; but flip the gate's
			// bag digest to garbage: shape check must reject it.
			r.GateReports[0].Postgres.BagDigest = "not-hex"
		},
		"dataset-digest-zeroed": func(r *Report) { r.Identity.DatasetDigest = "0" },
		"summary-row-deleted":   func(r *Report) { r.Summaries = r.Summaries[:1] },
		"claim-ratio-fudged":    func(r *Report) { r.Claims[0].Ratio.ArangoOverPostgres *= 0.5 },
		"claim-cites-nonclaimable": func(r *Report) {
			r.GateReports[0].Claimable = false
			r.GateReports[0].ExclusionReason = "forged exclusion"
			r.Status = "failed"
			r.StatusReasons = []string{primaryTiming() + ": forged exclusion"}
			// Claims left in place: a claim on a non-claimable workload
			// must not verify even when the status was honestly flipped.
		},
		"self-ratio-forged-high":     func(r *Report) { r.Controls.SelfRatioArango = 3.0 },
		"empty-floor-never-run":      func(r *Report) { r.Controls.EmptyFloorMillis = 0 },
		"stability-limit-never-set":  func(r *Report) { r.Controls.StabilityCVLimit = 0 },
		"unsupported-list-drifted":   func(r *Report) { r.Unsupported = []string{primaryTiming() + "/postgres"} },
		"exclusion-note-drifted":     func(r *Report) { r.Exclusions = "no workloads excluded from performance claims (lies)" },
		"unknown-backend-in-samples": func(r *Report) { r.Samples[0].Backend = "clickhouse" },
		"duplicate-summary-cell":     func(r *Report) { r.Summaries = append(r.Summaries, r.Summaries[0]) },
		"schema-renamed":             func(r *Report) { r.Schema = "loom.benchmark-report/v0" },
		"runid-blank":                func(r *Report) { r.Identity.RunID = "" },
		"config-zeroed":              func(r *Report) { r.Identity.Config.Iterations = 0 },
		// Live battery law (review r4559430560): excluded workloads must
		// stay blocked, disclosed, and sample-free; the marker may never
		// move; the matrix may never shrink silently.
		"excluded-arm-flipped-to-passed": func(r *Report) {
			g := gateForID(r, WorkloadDeriveAgeBand)
			g.Arango.Outcome = OutcomeLabelPassed
		},
		"excluded-gate-report-deleted": func(r *Report) {
			var kept []WorkloadGateReport
			for _, g := range r.GateReports {
				if g.WorkloadID != WorkloadDeriveAgeBand {
					kept = append(kept, g)
				}
			}
			r.GateReports = kept
		},
		"proven-gate-report-deleted": func(r *Report) {
			var kept []WorkloadGateReport
			for _, g := range r.GateReports {
				if g.WorkloadID != primaryTiming() {
					kept = append(kept, g)
				}
			}
			r.GateReports = kept
		},
		"excluded-workload-samples-injected": func(r *Report) {
			r.Samples = append(r.Samples, sample(BackendPostgres, WorkloadDistinctFamily, OutcomePassed, 10, false))
		},
		"excluded-disclosure-row-deleted": func(r *Report) {
			var kept []PlannedNotRun
			dropped := false
			for _, row := range r.Plan.PlannedNotRun {
				if !dropped && row.WorkloadID == WorkloadDeriveAgeBand && row.Backend == BackendArango {
					dropped = true
					continue
				}
				kept = append(kept, row)
			}
			if !dropped {
				panic("valid report lacks the exclusion disclosure row")
			}
			r.Plan.PlannedNotRun = kept
		},
		"excluded-disclosure-reason-forged": func(r *Report) {
			for i := range r.Plan.PlannedNotRun {
				if r.Plan.PlannedNotRun[i].WorkloadID == WorkloadDeriveAgeBand {
					r.Plan.PlannedNotRun[i].Reason = WorkloadNotRunReason + "because I said so"
					return
				}
			}
			panic("valid report lacks the exclusion disclosure row")
		},
		"marker-forged-on-proven-workload": func(r *Report) {
			gateForID(r, primaryTiming()).ExclusionReason = LiveBatteryExclusionMarker + "hiding a slow workload"
		},
		"excluded-row-forged-for-proven-workload": func(r *Report) {
			reason := LiveBatteryExclusions()[WorkloadDeriveAgeBand]
			r.Plan.PlannedNotRun = append(r.Plan.PlannedNotRun, PlannedNotRun{
				WorkloadID: primaryTiming(), Backend: BackendArango,
				Reason: WorkloadNotRunReason + reason,
			})
		},
	}
	for name, mutate := range cases {
		t.Run(name, forge(t, mutate))
	}
}

func TestReportBlockedStatusRoundTrips(t *testing.T) {
	report := validReport(t)
	report.GateReports[0].Postgres.Outcome = OutcomeLabelBlocked
	report.GateReports[0].Claimable = false
	report.GateReports[0].ExclusionReason = "postgres: blocked"
	report.Status = "failed"
	report.StatusReasons = []string{primaryTiming() + ": blocked"}
	report.Exclusions = FormatExclusionList(report.GateReports)
	report.Claims = nil
	if _, err := LoadReport(mustMarshal(t, report)); err != nil {
		t.Fatalf("honest blocked report must verify: %v", err)
	}
}

func mustMarshal(t *testing.T, r *Report) []byte {
	t.Helper()
	data, err := MarshalReport(r)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestBuildReportRefusesInconsistentConfig(t *testing.T) {
	report := validReport(t)
	_ = report
	_, err := BuildReport(RunIdentity{Config: RunConfig{}}, BothArmsPlan(), ControlRuns{}, nil, nil)
	if err == nil {
		t.Fatal("zero config must be refused at build, not defaulted")
	}
}

// --- timed publication lane (operator amendment 2026-11-13) --------------------

// timedReport builds the paired-timing shape: the ClickHouse-backed
// publication chain is disclosed not_run, but the battery ran for real,
// so samples, summaries, controls, and claims are legitimate evidence
// rather than forgery.
func timedReport(t *testing.T) *Report {
	t.Helper()
	report := validReport(t)
	report.Identity.ClickHouseDisabled = true
	plan := report.Plan
	plan.Publication = &PublicationPlan{Run: false, Battery: true, Capability: PublicationCapabilityTimed}
	plan.PlannedNotRun = append(plan.PlannedNotRun, PublicationDisclosures(plan.Legs, PublicationCapabilityTimed)...)
	built, err := BuildReport(report.Identity, plan, report.Controls, report.GateReports, report.Samples)
	if err != nil {
		t.Fatalf("timed publication report must verify: %v", err)
	}
	return built
}

func TestTimedPublicationCarriesTimingEvidence(t *testing.T) {
	report := timedReport(t)
	if report.Status != "passed" {
		t.Fatalf("timed lane status: %s (%v)", report.Status, report.StatusReasons)
	}
	if len(report.Samples) == 0 || len(report.Summaries) == 0 {
		t.Fatal("timed lane must carry real timing evidence")
	}
	if len(report.Claims) != len(Battery)-len(LiveBatteryExclusions()) {
		t.Fatalf("timed lane claims: %d want %d", len(report.Claims), len(Battery)-len(LiveBatteryExclusions()))
	}
	if !report.Plan.PublicationTimed() || report.Plan.PublicationZeroEvidence() {
		t.Fatalf("timed lane classification wrong: %+v", report.Plan.Publication)
	}
	for _, leg := range report.Plan.Legs {
		disclosed := 0
		for _, row := range report.Plan.PlannedNotRun {
			if row.Backend == leg.Backend && strings.HasPrefix(row.Reason, PublicationChainReason) {
				disclosed++
			}
		}
		if disclosed != len(Battery) {
			t.Fatalf("%s: %d publication-chain disclosure rows want %d (one per workload)", leg.Backend, disclosed, len(Battery))
		}
	}
}

func TestPublicationBatteryFlagMustNotBeFalseToPass(t *testing.T) {
	report := timedReport(t)
	plan := report.Plan
	plan.Publication.Battery = false
	report.Plan = plan
	if err := VerifyReport(report); err == nil {
		t.Fatal("battery=false on a report with timing evidence must be refused")
	}
}

func TestPublicationBatteryRequiresExcludedChain(t *testing.T) {
	plan := BothArmsPlan()
	plan.Publication = &PublicationPlan{Run: true, Battery: true, Capability: PublicationCapabilityTimed}
	if err := ValidatePublicationPlan(plan); err == nil {
		t.Fatal("run=true with battery=true must be refused as contradictory")
	}
}

func TestTimedPublicationRequiresNamedCapability(t *testing.T) {
	plan := BothArmsPlan()
	plan.Publication = &PublicationPlan{Run: false, Battery: true}
	if err := ValidatePublicationPlan(plan); err == nil {
		t.Fatal("timed lane must name the capability it discloses")
	}
}
