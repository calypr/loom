package benchmark_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backendspostgres "github.com/calypr/loom/internal/backends/postgres/control"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/benchmark"
)

// TestScratchLiveUploadProbe is the live proof for defect
// PG-PG-AUTH-PATH-DISCOVERY-ALLGENS (see the session artifact
// pg-pg-auth-path-discovery-allgens-evidence.md). It spawns the real
// server binary against a scratch benchmark database, uploads through the
// same Arm.UploadGeneration path benchcli uses, and calls the store-level
// AllGenerations listing directly.
//
// It is opt-in by substrate: it skips unless the scratch DSN file exists
// (default /tmp/opencode/.bench-dsn, override
// LOOM_BENCH_SCRATCH_DSN_FILE) and spawns the binary at
// LOOM_BENCH_SCRATCH_BINARY, default /tmp/opencode/loom-server.
//
// Two-world consistency is the pin: the defect linked the two surfaces —
// the store-level AllGenerations refusal made AuthorizeWrite 403 every
// scoped upload whose principal held the matching grant, and the
// bootstrap branch was unreachable. So the two surfaces must agree:
// defect present = upload refused AND listing refused; fix present =
// upload accepted AND listing answers. Mixed outcomes mean the server
// binary and this library disagree about the fix (substrate drift).

func TestScratchLiveUploadProbe(t *testing.T) {
	dsnFile := os.Getenv("LOOM_BENCH_SCRATCH_DSN_FILE")
	if dsnFile == "" {
		dsnFile = "/tmp/opencode/.bench-dsn"
	}
	dsnRaw, err := os.ReadFile(dsnFile)
	if err != nil {
		t.Skipf("scratch substrate absent (%s): %v", dsnFile, err)
	}
	dsn := strings.TrimSpace(string(dsnRaw))
	binary := os.Getenv("LOOM_BENCH_SCRATCH_BINARY")
	if binary == "" {
		binary = "/tmp/opencode/loom-server"
	}
	const path = "/programs/loom/projects/loom-bench/benchmark"

	fence, err := benchmark.NewMockFence()
	if err != nil {
		t.Fatalf("fence: %v", err)
	}
	defer fence.Close()
	fence.Grant("bench-admin", path, "/programs/loom/projects/loom-bench/decoy")
	token := "Bearer " + benchmark.MakeBearerToken(fence.URL(), "bench-admin")

	host, port, database, user, password, sslmode, err := splitDSNForScratch(dsn)
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}
	portNum := 0
	fmt.Sscanf(port, "%d", &portNum)
	backendYAML := benchmark.PostgresBackendYAML(host, portNum, database, user, password, sslmode, "loom_bench")
	cfgYAML := benchmark.BuildServerConfigYAML(benchmark.ListenPlaceholder, "postgres", backendYAML, fence.URL(), "040f587511a7e4de")

	freePort, err := benchmark.FreePort()
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	listen := fmt.Sprintf("127.0.0.1:%d", freePort)
	cfgYAML = strings.Replace(cfgYAML, benchmark.ListenPlaceholder, listen, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	stderrDir := os.Getenv("LOOM_BENCH_SCRATCH_STDERR_DIR")
	if stderrDir == "" {
		stderrDir = t.TempDir()
	}
	// The server resolves its graph schema relative to the CWD, and go test
	// runs in the package directory, so pin the repository root explicitly.
	workDir := os.Getenv("LOOM_BENCH_SCRATCH_WORKDIR")
	if workDir == "" {
		dir, err := os.Getwd()
		if err != nil {
			t.Fatalf("workdir: %v", err)
		}
		for {
			if _, err := os.Stat(dir + "/schemas/graph-fhir.json"); err == nil {
				workDir = dir
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				t.Fatal("no schemas/graph-fhir.json found walking up from cwd; set LOOM_BENCH_SCRATCH_WORKDIR")
			}
			dir = parent
		}
	}
	spec := benchmark.ServerSpec{
		BinaryPath: binary,
		Backend:    "postgres",
		ConfigYAML: cfgYAML,
		ReadyURL:   "http://" + listen + "/readyz",
		Timeout:    60 * time.Second,
		StderrDir:  stderrDir,
		WorkDir:    workDir,
	}
	proc, err := benchmark.SpawnServer(ctx, spec, nil, listen)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	defer proc.Stop()

	arm := &benchmark.Arm{Backend: "postgres", BaseURL: "http://" + listen, Client: &http.Client{Timeout: 60 * time.Second}}
	fixtures, fixtureErr := benchmark.GenerateFiles(benchmark.DatasetParams{Preset: benchmark.PresetTiny, Seed: 1})
	if fixtureErr != nil {
		t.Fatalf("fixtures: %v", fixtureErr)
	}
	uploadErr := arm.UploadGeneration(ctx, token, "loom-bench", "bench-probe-1", fixtures.Files, path)
	t.Logf("upload outcome: %v (fence requests=%d)", uploadErr, fence.Requests())

	// Store-level surface on the same database.
	probeRuntime, err := backendspostgres.Open(ctx, backendspostgres.Config{
		DSN: dsn, MaxConns: 2, MinConns: 1, AcquireTimeout: 30 * time.Second,
		Schemas: backendspostgres.SeriesSchemas{
			"source": "loom_bench_source", "explorer_control": "loom_bench_explorer_control",
			"publication_control": "loom_bench_publication_control", "dataset_release": "loom_bench_dataset_release",
			"recipe_chains": "loom_bench_recipe_chains", "recipe_exec_registries": "loom_bench_recipe_exec_registries",
		},
		Setup: backendspostgres.SetupNone,
	})
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	defer func() { _ = probeRuntime.Close() }()
	paths, listErr := probeRuntime.DiscoverExistingAuthResourcePaths(ctx, catalog.AuthResourcePathOptions{Project: "loom-bench", AllGenerations: true})
	t.Logf("AllGenerations listing outcome: %v (paths=%v)", listErr, paths)

	if (uploadErr == nil) != (listErr == nil) {
		t.Errorf("surface drift: uploadErr=%v listErr=%v — the server binary and this library must agree on the fix state of the AllGenerations read (substrate pin: defect-era binary with defect-era library, or fix with fix)", uploadErr, listErr)
	}
	if uploadErr != nil && !strings.Contains(uploadErr.Error(), "403") {
		t.Errorf("defect-world upload should be the scoped-lane 403, got: %v", uploadErr)
	}
}

func splitDSNForScratch(dsn string) (host, port, database, user, password, sslmode string, err error) {
	// postgresql://user:pass@host:port/db?sslmode=disable
	if !strings.HasPrefix(dsn, "postgresql://") {
		return "", "", "", "", "", "", fmt.Errorf("unexpected dsn scheme")
	}
	rest := strings.TrimPrefix(dsn, "postgresql://")
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return "", "", "", "", "", "", fmt.Errorf("no userinfo")
	}
	userinfo, rest := rest[:at], rest[at+1:]
	if c := strings.Index(userinfo, ":"); c >= 0 {
		user, password = userinfo[:c], userinfo[c+1:]
	} else {
		user = userinfo
	}
	var query, hostport string
	hostport, query, _ = strings.Cut(rest, "?")
	hostport, database, _ = strings.Cut(hostport, "/")
	host, port, _ = strings.Cut(hostport, ":")
	for _, kv := range strings.Split(query, "&") {
		if k, v, ok := strings.Cut(kv, "="); ok && k == "sslmode" {
			sslmode = v
		}
	}
	if host == "" || port == "" || database == "" {
		return "", "", "", "", "", "", fmt.Errorf("incomplete dsn")
	}
	return host, port, database, user, password, sslmode, nil
}
