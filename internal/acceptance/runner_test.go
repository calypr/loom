package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
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

var errFixtureFetchHeld = errors.New("fixture fetch held open by test")

type gatedRoundTripper struct {
	mu      sync.Mutex
	active  int
	maximum int
	total   int
	entered chan struct{}
	release chan struct{}
}

func (g *gatedRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	g.mu.Lock()
	g.active++
	g.total++
	if g.active > g.maximum {
		g.maximum = g.active
	}
	g.mu.Unlock()
	select {
	case g.entered <- struct{}{}:
	default:
	}
	<-g.release
	g.mu.Lock()
	g.active--
	g.mu.Unlock()
	return nil, errFixtureFetchHeld
}

func (g *gatedRoundTripper) snapshot() (int, int, int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.active, g.maximum, g.total
}

func testRunConfig(t *testing.T, fetcher *Fetcher) Config {
	t.Helper()
	return Config{
		Target: StaticTarget{Conn: Connections{
			LoomURL:       "http://127.0.0.1:1",
			ArangoURL:     "http://127.0.0.1:1",
			ClickHouseURL: "http://127.0.0.1:1",
		}},
		Fixture:       fetcher,
		Run:           RunSpec{RunID: RunID("0123456789abcdef")},
		ArtifactDir:   t.TempDir(),
		WorkspacePath: t.TempDir(),
		OraclePath:    t.TempDir() + "/oracle.json",
		HTTPClient:    &http.Client{Timeout: 5 * time.Second},
	}
}

func testLockedFetcher(t *testing.T, client *http.Client) *Fetcher {
	t.Helper()
	lock, _ := fixtureFixture(t, "http://fixture.invalid", 2)
	return &Fetcher{CacheDir: t.TempDir(), Lock: lock, Limits: Limits{Patients: 2, Resources: 100, Bytes: 1 << 20, Pages: 10}, HTTPClient: client}
}

func runFixtureAcquireFailure(err error) bool {
	return err != nil && errors.Is(err, errFixtureFetchHeld)
}

type runOutcome struct {
	status string
	err    error
}

func startRun(runner *Runner) <-chan runOutcome {
	done := make(chan runOutcome, 1)
	go func() {
		report, err := runner.Run(context.Background())
		done <- runOutcome{status: report.Status, err: err}
	}()
	return done
}

func TestRunnerAcquiresThroughSuppliedFetcherMutex(t *testing.T) {
	fetcher := testLockedFetcher(t, &http.Client{Transport: rejectRoundTripper(func(*http.Request) (*http.Response, error) {
		return nil, errFixtureFetchHeld
	})})
	runner, err := New(testRunConfig(t, fetcher))
	if err != nil {
		t.Fatal(err)
	}
	if runner.cfg.Fixture != fetcher {
		t.Fatal("New must retain the supplied *Fetcher so its mutex is the guard for every run")
	}
	fetcher.mu.Lock()
	done := startRun(runner)
	select {
	case outcome := <-done:
		fetcher.mu.Unlock()
		t.Fatalf("Run acquired the fixture while the caller held the fetcher mutex, so the run used a copied guard: status=%s err=%v", outcome.status, outcome.err)
	case <-time.After(500 * time.Millisecond):
	}
	fetcher.mu.Unlock()
	select {
	case outcome := <-done:
		if !runFixtureAcquireFailure(outcome.err) {
			t.Fatalf("resumed run status=%s err=%v", outcome.status, outcome.err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Run never resumed after the caller released the fetcher mutex")
	}
}

func TestRunnerSerializesConcurrentFixtureFetches(t *testing.T) {
	gate := &gatedRoundTripper{entered: make(chan struct{}, 8), release: make(chan struct{})}
	fetcher := testLockedFetcher(t, &http.Client{Transport: gate})
	runner, err := New(testRunConfig(t, fetcher))
	if err != nil {
		t.Fatal(err)
	}
	first := startRun(runner)
	select {
	case <-gate.entered:
	case <-time.After(30 * time.Second):
		close(gate.release)
		t.Fatal("first run never reached the fixture fetch")
	}
	second := startRun(runner)
	overlapped := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, total := gate.snapshot(); total >= 2 {
			overlapped = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, maximum, totalDuringHold := gate.snapshot()
	close(gate.release)
	firstOutcome, secondOutcome := <-first, <-second
	if overlapped || maximum != 1 {
		t.Fatalf("concurrent runs fetched through independent guards: maximum=%d concurrent fetches during hold, total=%d", maximum, totalDuringHold)
	}
	for _, outcome := range []runOutcome{firstOutcome, secondOutcome} {
		if !runFixtureAcquireFailure(outcome.err) {
			t.Fatalf("run status=%s err=%v", outcome.status, outcome.err)
		}
	}
}

func TestNewRejectsMissingFetcher(t *testing.T) {
	fetcher := testLockedFetcher(t, nil)
	for name, cfg := range map[string]Config{"nil fetcher": testRunConfig(t, nil), "empty cache directory": testRunConfig(t, &Fetcher{Lock: fetcher.Lock})} {
		if _, err := New(cfg); err == nil || !strings.Contains(err.Error(), "fixture cache directory is required") {
			t.Fatalf("New(%s) error=%v", name, err)
		}
	}
}

func callRun(t *testing.T, runner *Runner, ctx context.Context) (report Report, runErr error, recovered any, stack string) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			recovered = r
			stack = string(debug.Stack())
		}
	}()
	report, runErr = runner.Run(ctx)
	return report, runErr, nil, ""
}

func zeroValueFaultConfig(t *testing.T, mutate func(*Config)) Config {
	t.Helper()
	fetcher := testLockedFetcher(t, nil)
	cfg := Config{
		Target: StaticTarget{Conn: Connections{
			LoomURL:       "http://127.0.0.1:1",
			ArangoURL:     "http://127.0.0.1:1",
			ClickHouseURL: "http://127.0.0.1:1",
		}},
		Fixture:       fetcher,
		Run:           RunSpec{RunID: RunID("0123456789abcdef")},
		ArtifactDir:   t.TempDir(),
		WorkspacePath: t.TempDir(),
		OraclePath:    t.TempDir() + "/oracle.json",
		HTTPClient:    &http.Client{Timeout: 5 * time.Second},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return cfg
}

func TestRunRejectsStructLiteralRunnerWithNamedFault(t *testing.T) {
	cases := []struct {
		name      string
		cfg       Config
		faultText string
	}{
		{"zero value", Config{}, "acceptance target is required"},
		{"nil target", zeroValueFaultConfig(t, func(c *Config) { c.Target = nil }), "acceptance target is required"},
		{"nil fixture", zeroValueFaultConfig(t, func(c *Config) { c.Fixture = nil }), "fixture cache directory is required"},
		{"empty cache directory", zeroValueFaultConfig(t, func(c *Config) { c.Fixture = &Fetcher{Lock: testLockedFetcher(t, nil).Lock} }), "fixture cache directory is required"},
		{"missing workspace path", zeroValueFaultConfig(t, func(c *Config) { c.WorkspacePath = "   " }), "workspace path is required"},
		{"missing artifact directory", zeroValueFaultConfig(t, func(c *Config) { c.ArtifactDir = "" }), "artifact directory is required"},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &Runner{cfg: tc.cfg}
			report, runErr, recovered, stack := callRun(t, runner, ctx)
			if recovered != nil {
				t.Fatalf("Runner{} constructed as a struct literal bypassing New must fault with a named error, not a raw nil deref:\npanic: %v\n%s", recovered, stack)
			}
			if runErr == nil {
				t.Fatalf("Run accepted a Runner{} with %s and returned no fault; report=%+v", tc.name, report)
			}
			if errors.Is(runErr, context.Canceled) {
				t.Fatalf("the entry invariant must fire before any context handling, but Run reported a context fault: %v", runErr)
			}
			if report.Status != "" || report.Error != "" {
				t.Fatalf("a pre-run invariant fault must not fabricate a run outcome; got status=%q error=%q", report.Status, report.Error)
			}
			if !strings.Contains(runErr.Error(), "acceptance runner constructed without New") {
				t.Fatalf("Run error must name the New-bypass fault; got %v", runErr)
			}
			if !strings.Contains(runErr.Error(), tc.faultText) {
				t.Fatalf("Run error must name the violated invariant %q (same fault New rejects at construction); got %v", tc.faultText, runErr)
			}
		})
	}
}

func TestRunStructLiteralWithCompleteConfigKeepsRuntimeErrors(t *testing.T) {
	cfg := zeroValueFaultConfig(t, func(c *Config) {
		c.Run.Project = "NCPI_ACCEPTANCE"
		c.Run.Generation = "tcga-brca-locked"
		c.Run.ArangoDatabase = databaseName(c.Run.RunID)
		c.Run.ClickHouseDatabase = databaseName(c.Run.RunID)
	})
	runner := &Runner{cfg: cfg}
	report, runErr, recovered, stack := callRun(t, runner, context.Background())
	if recovered != nil {
		t.Fatalf("a complete struct-literal Runner must not panic:\npanic: %v\n%s", recovered, stack)
	}
	if runErr == nil || !strings.Contains(runErr.Error(), "FHIR request") {
		t.Fatalf("a complete literal may fail only on runtime fixture acquisition, not on the entry invariant; runErr=%v", runErr)
	}
	if report.Status != "FIXTURE_UNAVAILABLE" {
		t.Fatalf("runtime fixture failure must keep its class; report.Status=%q", report.Status)
	}
}

func TestNewHappyPathStaysUnchanged(t *testing.T) {
	fetcher := testLockedFetcher(t, nil)
	cfg := zeroValueFaultConfig(t, nil)
	cfg.Fixture = fetcher
	runner, err := New(cfg)
	if err != nil {
		t.Fatalf("New happy path regressed: %v", err)
	}
	if runner == nil {
		t.Fatal("New returned a nil Runner on the happy path")
	}
	if runner.cfg.Fixture != fetcher {
		t.Fatal("New must retain the supplied *Fetcher")
	}
	if !databaseRE.MatchString(runner.cfg.Run.ArangoDatabase) || !databaseRE.MatchString(runner.cfg.Run.ClickHouseDatabase) {
		t.Fatalf("New must default run-specific database names, got arango=%q clickhouse=%q", runner.cfg.Run.ArangoDatabase, runner.cfg.Run.ClickHouseDatabase)
	}
	if runner.cfg.SourceCommit != "acceptance" {
		t.Fatalf("New must default SourceCommit, got %q", runner.cfg.SourceCommit)
	}
}
