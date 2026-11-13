// Command benchcli is the benchmark driver/verifier entry point the node
// orchestrator invokes. It has exactly two jobs: run a live matched
// benchmark, and verify a report.json artifact. Verification authority lives
// here in Go; node unit tests can never satisfy the live checks (ruling ii).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/calypr/loom/internal/dataframe/benchmark"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "verify":
		runVerify()
	case "run":
		runLive()
	default:
		usage()
		os.Exit(2)
	}
}

func runVerify() {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	reportPath := fs.String("report", "", "path to report.json")
	_ = fs.Parse(os.Args[2:])
	if *reportPath == "" {
		fmt.Fprintln(os.Stderr, "benchcli verify: --report is required")
		os.Exit(2)
	}
	data, err := os.ReadFile(*reportPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "benchcli verify: %v\n", err)
		os.Exit(1)
	}
	report, err := benchmark.LoadReport(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "benchcli verify: REJECTED: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("benchcli verify: OK run=%s scope=%s status=%s claims=%d unsupported=%d\n",
		report.Identity.RunID, report.Plan.Scope, report.Status, len(report.Claims), len(report.Unsupported))
}

type runFlags struct {
	scope             string
	preset            string
	seed              uint64
	project           string
	generation        string
	authResourcePath  string
	warmup            int
	iterations        int
	rounds            int
	concurrency       int
	previewLimit      int
	binary            string
	stderrDir         string
	postgresAttachURL string
	postgresDSN       string
	arangoAttachURL   string
	arangoDSN         string
	tokenUnrestricted string
	tokenScoped       string
	out               string
	runID             string
	sourceCommit      string
	windowNote        string
	emptyFloorIters   int
}

func runLive() {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	var f runFlags
	fs.StringVar(&f.scope, "scope", benchmark.RunScopePostgresOnly, "both-arms | postgres-only")
	fs.StringVar(&f.preset, "preset", benchmark.PresetTiny, "dataset preset: tiny | small | representative")
	fs.Uint64Var(&f.seed, "seed", 7, "deterministic dataset seed")
	fs.StringVar(&f.project, "project", "loom-bench", "benchmark project namespace")
	fs.StringVar(&f.generation, "generation", "", "generation name (default: run id)")
	fs.StringVar(&f.authResourcePath, "auth-resource-path", "/programs/loom/projects/loom-bench/benchmark", "dataset auth scope stamped by the loader")
	fs.IntVar(&f.warmup, "warmup", 1, "warmup iterations per workload x backend")
	fs.IntVar(&f.iterations, "iterations", 8, "measured iterations per workload x backend")
	fs.IntVar(&f.rounds, "rounds", 2, "interleaved rounds")
	fs.IntVar(&f.concurrency, "concurrency", 1, "concurrency knob (recorded; battery runs serialized)")
	fs.IntVar(&f.previewLimit, "preview-limit", 1000, "preview row limit (source-row path is capped at 1000)")
	fs.StringVar(&f.binary, "binary", "", "loom server binary for spawned arms")
	fs.StringVar(&f.stderrDir, "stderr-dir", "", "directory for arm logs (default: <out>/arms)")
	fs.StringVar(&f.postgresAttachURL, "postgres-url", "", "attach to a running PostgreSQL-backed loom server")
	fs.StringVar(&f.postgresDSN, "postgres-dsn", "", "PostgreSQL DSN (spawn config + live version probe)")
	fs.StringVar(&f.arangoAttachURL, "arango-url", "", "attach to a running Arango-backed loom server")
	fs.StringVar(&f.arangoDSN, "arango-dsn", "", "ArangoDB DSN (spawn config + live version probe)")
	fs.StringVar(&f.tokenUnrestricted, "token-unrestricted", "", "bearer for the unrestricted principal (attached arms)")
	fs.StringVar(&f.tokenScoped, "token-scoped", "", "bearer whose fence grants EXCLUDE the dataset path")
	fs.StringVar(&f.out, "out", "bench-out", "output directory; must not contain report.json")
	fs.StringVar(&f.runID, "run-id", "", "run id (default: bench-<utc>-<rand>)")
	fs.StringVar(&f.sourceCommit, "source-commit", "", "full 40-hex SHA the arms were built from")
	fs.StringVar(&f.windowNote, "window-note", "", "who granted the exclusive window and when (recorded in arms.json)")
	fs.IntVar(&f.emptyFloorIters, "empty-floor-iterations", 5, "iterations for the empty-result floor control")
	_ = fs.Parse(os.Args[2:])

	// Fail-closed preflight (exit 3: refused to run; exit 1: ran and failed).
	preflight := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "benchcli run: BLOCKED — "+format+"\n", args...)
		os.Exit(3)
	}
	if os.Getenv("LOOM_BENCH_LIVE") != "1" {
		preflight("live runs require the exclusive window granted by the coordinator (LOOM_BENCH_LIVE=1)")
	}
	if strings.TrimSpace(f.windowNote) == "" {
		preflight("--window-note must record the granted window (granter + time) before any live run")
	}
	if !benchmark.ValidScope(f.scope) {
		preflight("unknown scope %q", f.scope)
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(f.sourceCommit) {
		preflight("--source-commit must be the full 40-hex SHA of the built source")
	}
	if _, err := os.Stat(filepath.Join(f.out, "report.json")); err == nil {
		preflight("%s/report.json already exists; use a fresh --out directory", f.out)
	}
	postgresLeg := f.scope == benchmark.RunScopeBothArms || f.scope == benchmark.RunScopePostgresOnly
	if postgresLeg && strings.TrimSpace(f.postgresDSN) == "" && strings.TrimSpace(f.postgresAttachURL) == "" {
		preflight("the postgres leg runs in scope %s: provide --postgres-dsn and/or --postgres-url", f.scope)
	}
	if strings.TrimSpace(f.postgresDSN) != "" {
		if err := guardPostgresNamespace(f.postgresDSN); err != nil {
			preflight("%v", err)
		}
	}
	if f.scope == benchmark.RunScopePostgresOnly {
		if strings.TrimSpace(f.arangoAttachURL) != "" || strings.TrimSpace(f.arangoDSN) != "" {
			preflight("postgres-only scope must not receive Arango flags; run both-arms scope instead")
		}
	} else {
		if strings.TrimSpace(f.arangoDSN) == "" && strings.TrimSpace(f.arangoAttachURL) == "" {
			preflight("both-arms scope needs --arango-dsn and/or --arango-url")
		}
		if strings.TrimSpace(f.arangoAttachURL) != "" && (strings.TrimSpace(f.tokenUnrestricted) == "" || strings.TrimSpace(f.tokenScoped) == "") {
			preflight("attached arms must be given both --token-unrestricted and --token-scoped")
		}
	}
	spawning := strings.TrimSpace(f.binary) != "" && (strings.TrimSpace(f.postgresAttachURL) == "" ||
		(f.scope == benchmark.RunScopeBothArms && strings.TrimSpace(f.arangoAttachURL) == ""))
	if spawning && strings.TrimSpace(f.tokenUnrestricted) != "" {
		preflight("spawned arms get tokens from the benchmark fence; do not pass --token-unrestricted with --binary")
	}
	if strings.TrimSpace(f.postgresAttachURL) != "" && (strings.TrimSpace(f.tokenUnrestricted) == "" || strings.TrimSpace(f.tokenScoped) == "") {
		preflight("attached postgres arm requires both tokens")
	}
	if f.runID == "" {
		f.runID = fmt.Sprintf("bench-%s-%s", time.Now().UTC().Format("20060102T150405Z"), randomRunIDSuffix())
	}
	if f.generation == "" {
		f.generation = f.runID
	}
	if err := os.MkdirAll(f.out, 0o755); err != nil {
		preflight("cannot create --out: %v", err)
	}

	// Build the driver options.
	plan := benchmark.BothArmsPlan()
	if f.scope == benchmark.RunScopePostgresOnly {
		plan = benchmark.PostgresOnlyPlan("no ArangoDB service in this execution environment; go-arango CI lane targets this leg")
	}
	plan.PlannedNotRun = benchmark.PlannedNotRunForBackends(plan.NotRunBackends())
	for i := range plan.PlannedNotRun {
		plan.PlannedNotRun[i].Reason = "leg not run: " + plan.Legs[0].Capability
	}
	if !planNotRunMatchesBattery(plan) {
		fmt.Fprintln(os.Stderr, "benchcli run: internal plan inconsistency")
		os.Exit(1)
	}

	d := &deployer{flags: &f}
	ctx := context.Background()
	if err := d.prepare(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "benchcli run: %v\n", err)
		os.Exit(1)
	}
	defer d.close()

	stderrDir := f.stderrDir
	if stderrDir == "" {
		stderrDir = filepath.Join(f.out, "arms")
	}
	opts := benchmark.DriverOptions{
		RunID: f.runID, StartedAt: time.Now().UTC(), Plan: plan, Preset: f.preset,
		Params: benchmark.DatasetParams{Preset: f.preset, Seed: f.seed, Project: f.project,
			Generation: f.generation, AuthResourcePath: f.authResourcePath},
		Config:            benchmark.RunConfig{Warmup: f.warmup, Iterations: f.iterations, Concurrency: f.concurrency, Rounds: f.rounds},
		PreviewLimit:      f.previewLimit,
		SourceCommit:      f.sourceCommit,
		UnrestrictedToken: d.unrestrictedToken,
		ScopedToken:       d.scopedToken,
		Arango:            d.arango,
		Postgres:          d.postgres,
		StderrDir:         stderrDir,
		Clock:             benchmark.NewMonotonicClock(),
		VersionProbe:      d.probe,
		EmptyFloorIters:   f.emptyFloorIters,
	}
	report, evidence, err := benchmark.RunBenchmark(ctx, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "benchcli run: FAILED — %v\n", err)
		os.Exit(1)
	}

	// Artifacts: report.json, report.md, arms.json (operational evidence).
	reportJSON, err := benchmark.MarshalReport(report)
	if err != nil {
		fmt.Fprintf(os.Stderr, "benchcli run: marshal: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(f.out, "report.json"), reportJSON, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "benchcli run: write report: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(f.out, "report.md"), []byte(benchmark.RenderMarkdown(report)), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "benchcli run: write markdown: %v\n", err)
		os.Exit(1)
	}
	manifest := map[string]any{"runId": f.runID, "windowNote": f.windowNote, "arms": evidence}
	if d.fence != nil {
		manifest["fenceRequests"] = d.fence.Requests()
	}
	manifestJSON, _ := json.MarshalIndent(manifest, "", " ")
	if err := os.WriteFile(filepath.Join(f.out, "arms.json"), manifestJSON, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "benchcli run: write arms manifest: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("benchcli run: OK run=%s scope=%s status=%s claims=%d out=%s\n",
		report.Identity.RunID, report.Plan.Scope, report.Status, len(report.Claims), f.out)
}

func planNotRunMatchesBattery(plan benchmark.RunPlan) bool {
	counts := map[string]int{}
	for _, row := range plan.PlannedNotRun {
		counts[row.Backend]++
	}
	for _, leg := range plan.Legs {
		want := 0
		if leg.Mode == "not_run" {
			want = len(benchmark.Battery)
		}
		if counts[leg.Backend] != want {
			return false
		}
	}
	return true
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: benchcli verify --report <report.json> | benchcli run [flags]")
}
