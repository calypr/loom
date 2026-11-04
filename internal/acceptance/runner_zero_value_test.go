package acceptance

import (
	"context"
	"errors"
	"net/http"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

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
