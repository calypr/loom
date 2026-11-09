package acceptance

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

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
