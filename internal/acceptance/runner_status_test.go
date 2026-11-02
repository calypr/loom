package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var (
	errFixtureFetchDown = errors.New("fixture endpoint down (runner status-label lock)")
	errTargetUnreach    = errors.New("acceptance target unreachable (runner status-label lock)")
	errLeaseCloseLock   = errors.New("lease close failed (runner status-label lock)")
)

type statusFailingTarget struct{ err error }

func (t statusFailingTarget) Acquire(context.Context, RunSpec) (Lease, error) { return nil, t.err }

type statusCloseFailLease struct {
	staticLease
	closeErr error
}

func (l statusCloseFailLease) Close(context.Context) error { return l.closeErr }

type statusCloseFailTarget struct {
	conn     Connections
	closeErr error
}

func (t statusCloseFailTarget) Acquire(_ context.Context, spec RunSpec) (Lease, error) {
	ns, err := namespaceFromSpec(spec)
	if err != nil {
		return nil, err
	}
	return statusCloseFailLease{staticLease: staticLease{conn: t.conn, ns: ns}, closeErr: t.closeErr}, nil
}

func cohortFetcher(t *testing.T, danglingObservation bool) *Fetcher {
	t.Helper()
	patientIDs := []string{"patient-00", "patient-01"}
	byType := map[string][][]byte{"ResearchStudy": {testResource("ResearchStudy", "study-1", "v1", nil)}}
	for _, typ := range []string{"Patient", "Specimen", "Observation", "DocumentReference", "Condition"} {
		for i, patient := range patientIDs {
			id := fmt.Sprintf("%s-%02d", strings.ToLower(typ), i)
			extra := map[string]any{}
			if typ != "Patient" {
				reference := "Patient/" + patient
				if typ == "Observation" && i == 0 && danglingObservation {
					reference = "Patient/not-in-cohort"
				}
				extra["subject"] = map[string]any{"reference": reference}
			}
			byType[typ] = append(byType[typ], testResource(typ, id, "v1", extra))
		}
	}
	resources := map[string][]ResourceLock{}
	for typ, items := range byType {
		for _, item := range items {
			resources[typ] = append(resources[typ], lockResource(typ, resourceID(item), "v1", item))
		}
	}
	server := testServer(t, patientIDs, byType, &atomic.Int64{})
	lock := testLock(server.URL, patientIDs, resources)
	return &Fetcher{CacheDir: t.TempDir(), Lock: lock, Limits: Limits{Patients: 2, Resources: 100, Bytes: 1 << 20, Pages: 10}, HTTPClient: &http.Client{Timeout: 10 * time.Second}}
}

func persistedReport(t *testing.T, dir string) Report {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil {
		t.Fatalf("read persisted report: %v", err)
	}
	var got Report
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode persisted report: %v\n%s", err, raw)
	}
	return got
}

func TestRunPreservesFixtureAcquireFailureCode(t *testing.T) {
	fetcher := testLockedFetcher(t, &http.Client{Transport: rejectRoundTripper(func(*http.Request) (*http.Response, error) {
		return nil, errFixtureFetchDown
	})})
	cfg := testRunConfig(t, fetcher)
	runner, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	report, runErr := runner.Run(context.Background())
	if !errors.Is(runErr, errFixtureFetchDown) {
		t.Fatalf("Run error=%v want the fixture acquire failure", runErr)
	}
	if report.Status != "FIXTURE_UNAVAILABLE" {
		t.Fatalf("fixture-stage failure code lost: report.Status=%q want FIXTURE_UNAVAILABLE; the deferred status setting must not replace the earliest failure class with the generic FAILED", report.Status)
	}
	if report.Error == "" {
		t.Fatal("fixture failure must remain recorded in report.Error")
	}
	if got := persistedReport(t, cfg.ArtifactDir); got.Status != "FIXTURE_UNAVAILABLE" {
		t.Fatalf("persisted report.json status=%q want FIXTURE_UNAVAILABLE; CI triage reads this field", got.Status)
	}
}

func TestRunPreservesFixtureClosureFailureCode(t *testing.T) {
	fetcher := cohortFetcher(t, true)
	cfg := testRunConfig(t, fetcher)
	runner, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	report, runErr := runner.Run(context.Background())
	if runErr == nil || !strings.Contains(runErr.Error(), "fixture closure") {
		t.Fatalf("Run error=%v want a fixture closure violation", runErr)
	}
	if report.Status != "FIXTURE_INVALID" {
		t.Fatalf("fixture-stage failure code lost: report.Status=%q want FIXTURE_INVALID; the deferred status setting must not replace the earliest failure class with the generic FAILED", report.Status)
	}
	if got := persistedReport(t, cfg.ArtifactDir); got.Status != "FIXTURE_INVALID" {
		t.Fatalf("persisted report.json status=%q want FIXTURE_INVALID; CI triage reads this field", got.Status)
	}
}

func TestRunPreservesTargetFailureCode(t *testing.T) {
	cfg := testRunConfig(t, testLockedFetcher(t, nil))
	cfg.Target = statusFailingTarget{err: errTargetUnreach}
	runner, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	report, runErr := runner.Run(context.Background())
	if !errors.Is(runErr, errTargetUnreach) {
		t.Fatalf("Run error=%v want the target acquire failure", runErr)
	}
	if report.Status != "TARGET_UNAVAILABLE" {
		t.Fatalf("target-stage failure code lost: report.Status=%q want TARGET_UNAVAILABLE", report.Status)
	}
	if got := persistedReport(t, cfg.ArtifactDir); got.Status != "TARGET_UNAVAILABLE" {
		t.Fatalf("persisted report.json status=%q want TARGET_UNAVAILABLE; CI triage reads this field", got.Status)
	}
}

func TestRunSuccessThroughStagesSetsNoFailureCode(t *testing.T) {
	fetcher := cohortFetcher(t, false)
	cfg := testRunConfig(t, fetcher)
	runner, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	report, runErr := runner.Run(context.Background())
	if runErr == nil || !strings.Contains(runErr.Error(), "acceptance oracle") {
		t.Fatalf("Run error=%v want the scenario to fail in the oracle load, so the target and fixture stages must have succeeded", runErr)
	}
	if report.Status != "FAILED" {
		t.Fatalf("uncategorized scenario failure must keep the generic fallback: report.Status=%q want FAILED", report.Status)
	}
	got := persistedReport(t, cfg.ArtifactDir)
	if got.Status != "FAILED" {
		t.Fatalf("persisted report.json status=%q want FAILED", got.Status)
	}
	for _, stage := range []string{"FIXTURE_UNAVAILABLE", "FIXTURE_INVALID", "TARGET_UNAVAILABLE"} {
		if got.Status == stage || got.BaseStatus == stage {
			t.Fatalf("succeeded stages must set no failure code but report carries %q (status=%q base_status=%q)", stage, got.Status, got.BaseStatus)
		}
	}
}

func TestRunLeaseCloseFailurePreservesEarliestFailureCode(t *testing.T) {
	fetcher := testLockedFetcher(t, &http.Client{Transport: rejectRoundTripper(func(*http.Request) (*http.Response, error) {
		return nil, errFixtureFetchDown
	})})
	cfg := testRunConfig(t, fetcher)
	cfg.Target = statusCloseFailTarget{conn: cfg.Target.(StaticTarget).Conn, closeErr: errLeaseCloseLock}
	runner, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	report, runErr := runner.Run(context.Background())
	if !errors.Is(runErr, errFixtureFetchDown) || !errors.Is(runErr, errLeaseCloseLock) {
		t.Fatalf("Run error=%v must join the fixture failure and the lease close failure", runErr)
	}
	if report.Status != "FIXTURE_UNAVAILABLE" {
		t.Fatalf("late lease-close failure must not erase the earliest failure class: report.Status=%q want FIXTURE_UNAVAILABLE", report.Status)
	}
	if !strings.Contains(report.Error, errFixtureFetchDown.Error()) || !strings.Contains(report.Error, "lease close") {
		t.Fatalf("report.Error=%q must keep the root failure and record the lease close failure", report.Error)
	}
	if got := persistedReport(t, cfg.ArtifactDir); got.Status != "FIXTURE_UNAVAILABLE" {
		t.Fatalf("persisted report.json status=%q want FIXTURE_UNAVAILABLE", got.Status)
	}
}
