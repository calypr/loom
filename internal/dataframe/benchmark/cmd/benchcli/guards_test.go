package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/benchmark"
	"github.com/calypr/loom/internal/projectid"
)

// The benchcli guards are fail-closed surfaces: they keep a benchmark run
// inside loom_bench_* namespaces, keep credentials out of error text, and
// refuse incoherent arm topologies before anything spawns. Each refusal
// here is a production guarantee, so each is pinned.

func TestGuardPostgresNamespace(t *testing.T) {
	cases := []struct {
		name    string
		dsn     string
		wantErr string
	}{
		{"benchmark db passes", "postgresql://u:p@127.0.0.1:55432/loom_bench_x1?sslmode=disable", ""},
		{"shared test db refused", "postgresql://u:p@127.0.0.1:55432/loom_test?sslmode=disable", "outside the benchmark namespace"},
		{"postgres db refused", "postgresql://u:p@127.0.0.1:55432/postgres?sslmode=disable", "outside the benchmark namespace"},
		{"prefix lookalike refused", "postgresql://u:p@127.0.0.1:55432/loom_benchx?sslmode=disable", "outside the benchmark namespace"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := guardPostgresNamespace(tc.dsn)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("valid benchmark dsn refused: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestGuardErrorsNeverLeakCredentials(t *testing.T) {
	const secret = "s3cr3t-pw-9f2a"
	dsn := "postgresql://u:" + secret + "@127.0.0.1:55432/loom_test?sslmode=disable"
	err := guardPostgresNamespace(dsn)
	if err == nil {
		t.Fatal("shared db accepted")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("guard error leaked the password: %v", err)
	}
	if _, _, _, _, _, _, perr := parsePostgresDSN("not a dsn at all " + secret); perr == nil || strings.Contains(perr.Error(), secret) {
		t.Fatalf("parsePostgresDSN error missing or leaked: %v", perr)
	}
	if _, _, aerr := parseArangoDSN("arango://u:" + secret + "@127.0.0.1:8529/loom_bench_x"); aerr == nil || strings.Contains(aerr.Error(), secret) {
		t.Fatalf("parseArangoDSN error missing or leaked: %v", aerr)
	}
}

func TestParsePostgresDSNFully(t *testing.T) {
	host, port, database, user, password, sslmode, err := parsePostgresDSN("postgresql://loom_test:pw@127.0.0.1:55432/loom_bench_a?sslmode=disable")
	if err != nil {
		t.Fatalf("valid dsn refused: %v", err)
	}
	if host != "127.0.0.1" || port != 55432 || database != "loom_bench_a" || user != "loom_test" || password != "pw" || sslmode != "disable" {
		t.Fatalf("unexpected parse: %s:%d/%s user=%s pw=%s ssl=%s", host, port, database, user, password, sslmode)
	}
	if _, port, _, _, _, _, err := parsePostgresDSN("postgresql://u@127.0.0.1/loom_bench_a?sslmode=disable"); err != nil || port != 5432 {
		t.Fatalf("default port: %d, %v", port, err)
	}
}

func TestParseArangoDSN(t *testing.T) {
	rawURL, database, err := parseArangoDSN("http://root:pw@127.0.0.1:8529/loom_bench_ar")
	if err != nil {
		t.Fatalf("valid arango dsn refused: %v", err)
	}
	if database != "loom_bench_ar" || !strings.HasPrefix(rawURL, "http://") || strings.Contains(rawURL, "loom_bench_ar") {
		t.Fatalf("unexpected split: rawURL=%q db=%q", rawURL, database)
	}
	for _, bad := range []string{
		"arango://root@127.0.0.1:8529/loom_bench_ar", // scheme
		"http://root@127.0.0.1:8529/",                // no database
		"http://root@127.0.0.1:8529/production",      // outside namespace
	} {
		if _, _, err := parseArangoDSN(bad); err == nil {
			t.Fatalf("accepted bad arango dsn %q", bad)
		}
	}
}

func TestBenchDatabaseNameGuard(t *testing.T) {
	for _, ok := range []string{"loom_bench_probe1", "loom_bench_abcd", "loom_bench_" + strings.Repeat("z", 48)} {
		if !benchDatabaseRe.MatchString(ok) {
			t.Fatalf("rejected valid database name %q", ok)
		}
	}
	for _, bad := range []string{"", "loom_bench_abc", "loom_test", "postgres", "loom_bench_UPPER", "public", "loom_bench_", "xloom_bench_abcd"} {
		if benchDatabaseRe.MatchString(bad) {
			t.Fatalf("accepted out-of-namespace database name %q", bad)
		}
	}
}

func TestSetupSchemaPrefixGuard(t *testing.T) {
	// setupPG validates the prefix before any connect, so these refusals
	// are hermetic and must not need a database.
	if err := setupPG(context.Background(), "postgresql://no:such@127.0.0.1:1/postgres", "loom_bench_guarded", "public"); err == nil ||
		!strings.Contains(err.Error(), "outside the benchmark namespace") {
		t.Fatalf("schema prefix public accepted: %v", err)
	}
	if err := setupPG(context.Background(), "postgresql://no:such@127.0.0.1:1/postgres", "loom_bench_guarded", "loom_bench"); err != nil &&
		strings.Contains(err.Error(), "outside the benchmark namespace") {
		t.Fatalf("canonical loom_bench prefix refused: %v", err)
	}
}

func TestWithDatabase(t *testing.T) {
	swapped, err := withDatabase("postgresql://u:p@127.0.0.1:55432/postgres?sslmode=disable", "loom_bench_t")
	if err != nil || !strings.Contains(swapped, "/loom_bench_t") || !strings.Contains(swapped, "sslmode=disable") {
		t.Fatalf("swap failed: %q %v", swapped, err)
	}
	if _, err := withDatabase("host=127.0.0.1 dbname=postgres", "loom_bench_t"); err == nil {
		t.Fatal("keyword DSN form accepted for admin")
	}
	if _, err := withDatabase("mysql://u:p@127.0.0.1/db", "loom_bench_t"); err == nil {
		t.Fatal("non-postgres scheme accepted for admin")
	}
}

func TestAdminDSNFromEnv(t *testing.T) {
	t.Setenv("LOOM_BENCH_ADMIN_DSN", "  ")
	if _, err := adminDSNFromEnv(); err == nil {
		t.Fatal("blank admin DSN accepted")
	}
	t.Setenv("LOOM_BENCH_ADMIN_DSN", "postgresql://u:p@127.0.0.1:55432/postgres")
	if dsn, err := adminDSNFromEnv(); err != nil || !strings.HasPrefix(dsn, "postgresql://") {
		t.Fatalf("admin DSN round trip: %q %v", dsn, err)
	}
}

func TestPlanNotRunMatchesBatteryBothWays(t *testing.T) {
	planned := benchmark.PlannedPlan("cap a", "cap b")
	planned.PlannedNotRun = benchmark.PlannedNotRunForBackends(planned.NotRunBackends())
	if !planNotRunMatchesBattery(planned) {
		t.Fatal("complete two-leg disclosure rejected")
	}
	only := benchmark.PostgresOnlyPlan("cap a")
	only.PlannedNotRun = benchmark.PlannedNotRunForBackends(only.NotRunBackends())
	// the ran leg must also carry one disclosure row per excluded workload
	benchmark.AppendWorkloadDisclosures(&only, benchmark.LiveBatteryExclusions())
	if !planNotRunMatchesBattery(only) {
		t.Fatal("complete postgres-only disclosure rejected")
	}
	planned.PlannedNotRun = planned.PlannedNotRun[1:]
	if planNotRunMatchesBattery(planned) {
		t.Fatal("dropped disclosure row accepted")
	}
	only.PlannedNotRun = append(only.PlannedNotRun, benchmark.PlannedNotRun{WorkloadID: "filter-active-gender", Backend: benchmark.BackendPostgres, Reason: "extra"})
	if planNotRunMatchesBattery(only) {
		t.Fatal("disclosure row for a ran leg accepted")
	}
	// a ran-leg workload row must match the registry exactly
	forged := benchmark.PostgresOnlyPlan("cap a")
	forged.PlannedNotRun = benchmark.PlannedNotRunForBackends(forged.NotRunBackends())
	benchmark.AppendWorkloadDisclosures(&forged, benchmark.LiveBatteryExclusions())
	for i := range forged.PlannedNotRun {
		if strings.HasPrefix(forged.PlannedNotRun[i].Reason, benchmark.WorkloadNotRunReason) {
			forged.PlannedNotRun[i].Reason = benchmark.WorkloadNotRunReason + "because I said so"
			break
		}
	}
	if planNotRunMatchesBattery(forged) {
		t.Fatal("registry-mismatched workload disclosure accepted")
	}
}

func TestWriteRunArtifactsRoundTripsThroughVerifier(t *testing.T) {
	plan := benchmark.PlannedPlan("cap a", "cap b")
	plan.PlannedNotRun = benchmark.PlannedNotRunForBackends(plan.NotRunBackends())
	report, err := benchmark.BuildReport(benchmark.RunIdentity{
		RunID: "bench-clitest-a", StartedAt: time.Now().UTC(), Preset: benchmark.PresetTiny,
		SourceCommit: strings.Repeat("a", 40), DatasetDigest: strings.Repeat("b", 64),
		ClickHouseDisabled: true,
		Config:             benchmark.RunConfig{Warmup: 1, Iterations: 4, Concurrency: 1, Rounds: 2},
	}, plan, benchmark.ControlRuns{}, benchmark.PlannedGateReports(plan.Legs), nil)
	if err != nil {
		t.Fatalf("planned report: %v", err)
	}
	out := t.TempDir()
	writeRunArtifacts(out, report, map[string]any{"runId": "bench-clitest-a", "planned": true})
	for _, name := range []string{"report.json", "report.md", "arms.json"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Fatalf("missing artifact %s: %v", name, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(out, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := benchmark.LoadReport(raw) // LoadReport runs VerifyReport
	if err != nil {
		t.Fatalf("written artifact failed verification: %v", err)
	}
	if loaded.Plan.Scope != benchmark.RunScopePlanned || len(loaded.Claims) != 0 {
		t.Fatalf("artifact drifted: scope=%s claims=%d", loaded.Plan.Scope, len(loaded.Claims))
	}
	var manifest map[string]any
	if err := json.Unmarshal([]byte(mustRead(t, filepath.Join(out, "arms.json"))), &manifest); err != nil {
		t.Fatalf("arms.json not valid JSON: %v", err)
	}
	if manifest["planned"] != true {
		t.Fatal("planned manifest flag missing")
	}
	md := mustRead(t, filepath.Join(out, "report.md"))
	if !strings.Contains(md, benchmark.RunScopePlanned) {
		t.Fatal("markdown must disclose the planned scope")
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestDeployerRefusesIncoherentTopologies(t *testing.T) {
	cases := []struct {
		name    string
		flags   runFlags
		wantErr string
	}{
		{"mixed attached and spawned", runFlags{scope: benchmark.RunScopeBothArms,
			postgresAttachURL: "http://127.0.0.1:1", postgresDSN: dsnForTest,
			arangoDSN: "http://root@127.0.0.1:8529/loom_bench_a"}, "mixing attached and spawned"},
		{"attached without tokens", runFlags{scope: benchmark.RunScopePostgresOnly,
			postgresAttachURL: "http://127.0.0.1:1", postgresDSN: dsnForTest}, "token-unrestricted"},
		{"attached without dsn", runFlags{scope: benchmark.RunScopePostgresOnly,
			postgresAttachURL: "http://127.0.0.1:1",
			tokenUnrestricted: "Bearer x", tokenScoped: "Bearer y"}, "live version probe"},
		{"spawned without binary", runFlags{scope: benchmark.RunScopePostgresOnly,
			postgresDSN: dsnForTest}, "--binary"},
		{"spawned with caller tokens", runFlags{scope: benchmark.RunScopePostgresOnly,
			binary: "/nonexistent/loom-server", postgresDSN: dsnForTest,
			tokenUnrestricted: "Bearer x"}, "benchmark fence"},
		{"spawned without dsn", runFlags{scope: benchmark.RunScopePostgresOnly,
			binary: "/nonexistent/loom-server"}, "requires --postgres-dsn"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &deployer{flags: &tc.flags}
			err := d.prepare(context.Background())
			d.close()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestDeployerSpawnedPostgresOnlyWiresFenceAndScrubbers(t *testing.T) {
	const datasetPath = "/programs/loom/projects/loom-bench/benchmark"
	flags := runFlags{scope: benchmark.RunScopePostgresOnly,
		binary: "/nonexistent/loom-server", postgresDSN: dsnForTest, authResourcePath: datasetPath}
	d := &deployer{flags: &flags}
	if err := d.prepare(context.Background()); err != nil {
		t.Fatalf("valid spawned topology refused: %v", err)
	}
	defer d.close()
	cfg := d.postgres.ConfigYAML
	for _, want := range []string{"trusted_issuer", "mode: calypr", "loom_bench_source", benchmark.ListenPlaceholder} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("arm config missing %q:\n%s", want, cfg)
		}
	}
	if !strings.HasPrefix(d.unrestrictedToken, "Bearer ") || !strings.HasPrefix(d.scopedToken, "Bearer ") {
		t.Fatal("fence tokens not minted")
	}
	if d.unrestrictedToken == d.scopedToken {
		t.Fatal("admin and scoped principals share a token")
	}
	if len(d.postgres.ScrubSecrets) < 2 || d.postgres.ScrubSecrets[0] != dsnForTest {
		t.Fatalf("arm logs would not scrub the DSN: %v", d.postgres.ScrubSecrets)
	}
	// The grant shape is the negative-auth control's basis, checked
	// through the fence's real /user surface: the admin holds the dataset
	// path; the scoped principal must NOT (production denial shape).
	admin := fenceGrants(t, d, d.unrestrictedToken)
	if _, ok := admin[datasetPath]; !ok {
		t.Fatalf("admin token lacks the dataset path: %v", admin)
	}
	scoped := fenceGrants(t, d, d.scopedToken)
	if _, leaked := scoped[datasetPath]; leaked {
		t.Fatalf("scoped token leaks the dataset path: %v", scoped)
	}
	if _, ok := scoped[benchmarkDecoyPath]; !ok {
		t.Fatalf("scoped token lacks the decoy grant the control needs: %v", scoped)
	}
}

func fenceGrants(t *testing.T, d *deployer, token string) map[string]any {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, d.fence.URL()+"/user", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("fence /user returned %d", resp.StatusCode)
	}
	var body struct {
		Authz map[string]any `json:"authz"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Authz == nil {
		t.Fatal("fence /user omitted authz")
	}
	return body.Authz
}

const dsnForTest = "postgresql://u:p@127.0.0.1:55432/loom_bench_clitest?sslmode=disable"

// --- live exit-code contract (review r4559430568, finding 4) -------------------

func liveSample(warmup bool) benchmark.Sample {
	return benchmark.Sample{Backend: benchmark.BackendPostgres, WorkloadID: "select-filter", Warmup: warmup, Outcome: benchmark.OutcomePassed, Cache: benchmark.CacheWarm, Millis: 1}
}

func TestClassifyLiveExit(t *testing.T) {
	cases := []struct {
		name   string
		status string
		sample []benchmark.Sample
		code   int
	}{
		{"passed exits 0", "passed", []benchmark.Sample{liveSample(false)}, 0},
		{"passed with no samples still 0", "passed", nil, 0},
		{"failed with measured samples exits 1 (ran and failed)", "failed", []benchmark.Sample{liveSample(false)}, 1},
		{"blocked with measured samples exits 1", "blocked", []benchmark.Sample{liveSample(false)}, 1},
		{"failed with zero samples exits 3 (disclosed not_run)", "failed", nil, 3},
		{"default publication=excluded shape: failed, zero samples", "failed", []benchmark.Sample{}, 3},
		{"warmup-only samples are not measured evidence", "failed", []benchmark.Sample{liveSample(true), liveSample(true)}, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, verdict := classifyLiveExit(c.status, c.sample)
			if code != c.code {
				t.Fatalf("classifyLiveExit(%q, %d samples) = %d (%s), want %d", c.status, len(c.sample), code, verdict, c.code)
			}
			if c.code == 0 && verdict != "OK" {
				t.Fatalf("exit 0 must print OK, got %q", verdict)
			}
			if c.code == 3 && verdict == "OK" {
				t.Fatal("a disclosed not_run must never carry the success verdict")
			}
		})
	}
}

// grantResourceAccess is a fence stub for the project-read contract.
type grantResourceAccess struct{ resources []string }

func (g grantResourceAccess) GetAllowedResources(context.Context, string, string, string) ([]string, error) {
	return append([]string(nil), g.resources...), nil
}

// The paired CI lane faulted 403 FORBIDDEN (stage authorization) on the
// first-ever scoped GET .../authoring/v2/builder: the harness project was
// "loom-bench" while the fence grants lived under
// /programs/loom/projects/loom-bench/..., whose calypr key is
// "loom-loom-bench". AuthorizeReadProject (compose.go ->
// getAuthoringBuilderDirect) admits a caller only when a grant normalizes
// to projectid.Legacy(project) — the write gate hides this because it
// compares grants against the dataset path itself. This pins the whole
// harness default set against that gate so the shapes can never drift
// apart again.
func TestDefaultFenceGrantsSatisfyTheProjectReadGate(t *testing.T) {
	key := projectid.Legacy(benchmarkDefaultProject)
	for name, path := range map[string]string{
		"dataset": benchmarkDefaultAuthResourcePath,
		"decoy":   benchmarkDecoyPath,
	} {
		if got := authscope.NormalizeAuthResourcePath(path); got != key {
			t.Fatalf("%s path %q normalizes to %q, want the legacy project key %q", name, path, got, key)
		}
	}
	resolver := authscope.NewScopeResolver(authscope.ScopeResolverConfig{
		ResourceAccess: grantResourceAccess{resources: []string{benchmarkDefaultAuthResourcePath, benchmarkDecoyPath}},
	})
	principal := &authscope.Principal{AuthorizationHeader: "Bearer bench-admin"}
	if err := resolver.AuthorizeReadProject(context.Background(), principal, benchmarkDefaultProject); err != nil {
		t.Fatalf("the default admin grants must satisfy the project read gate: %v", err)
	}
	foreign := authscope.NewScopeResolver(authscope.ScopeResolverConfig{
		ResourceAccess: grantResourceAccess{resources: []string{"/programs/other/projects/unrelated/benchmark"}},
	})
	if err := foreign.AuthorizeReadProject(context.Background(), principal, benchmarkDefaultProject); !errors.Is(err, authscope.ErrForbidden) {
		t.Fatalf("a subject without the project grant must stay forbidden, got %v", err)
	}
}
