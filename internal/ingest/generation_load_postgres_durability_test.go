package ingest

// Durability, crash, race, and cancellation acceptance for the pg-ingest-03
// generation-load orchestrator (second half of check-2).
//
// These tests kill real backends and real OS processes mid-load and prove
// the convergence contract on PostgreSQL 18:
//
//   - pg_terminate_backend injection at every durability seam (manifest
//     create, resource batch, edge batch, field profile, inventory, semantic
//     contributions, staged transition). The kill is witnessed: the victim
//     backend is seen parked on the seam's lock (wait_event_type = 'Lock')
//     before termination, and the termination count must be >= 1. After the
//     kill: the manifest is never STAGED with missing data, the active
//     generation pointer never moves, and the ledger agrees exactly with
//     the committed rows. A replay then converges to the byte-stable
//     fingerprint of a clean load.
//   - Separate-OS-process crash and replay: a child test binary process
//     loads; the parent SIGKILLs it mid-load (no client-side failure
//     handling can run); a second, distinct OS process replays the load and
//     must converge the generation to STAGED with the exact reference
//     fingerprint, and repeat runs read back ErrGenerationAlreadyStaged
//     without writing anything. A modified schema identity is refused by
//     name with a passing non-vacuity control.
//   - Same-generation load races and activation CAS races: at least ten
//     rounds each on two real pools with distinct pg_backend_pid rows,
//     sampled live from pg_stat_activity for a positive overlap witness,
//     with sequential controls that fail when the witness logic itself is
//     broken.
//   - Cancellation branches: a safe-point cancellation (the only branch
//     allowed to mark FAILED) versus a cancellation that races an
//     in-flight statement (must keep the manifest LOADING for replay),
//     each with its paired control.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	publication "github.com/calypr/loom/internal/dataset"
	datasetpostgres "github.com/calypr/loom/internal/dataset/postgres"
)

// ---------------------------------------------------------------------------
// Ledger <-> rows equality: every committed batch is accounted exactly once
// and no row exists without its batch's accounting.
// ---------------------------------------------------------------------------

func ing03AssertLedgerEquality(t *testing.T, service *ing03Service) {
	t.Helper()
	ledger := ing03QIdent(t, service.schema, "batch_write_ledger")
	sumResources := ing03Count(t, service.iso, "SELECT COALESCE(sum(resource_count), 0) FROM "+ledger)
	sumEdges := ing03Count(t, service.iso, "SELECT COALESCE(sum(edge_count), 0) FROM "+ledger)
	totalIDs := ing03Count(t, service.iso, "SELECT count(*) FROM "+ledger)
	distinctIDs := ing03Count(t, service.iso, "SELECT count(DISTINCT batch_id) FROM "+ledger)
	storedResources := service.countTable("fhir_resource", "")
	storedEdges := service.countTable("fhir_edge_occurrence", "")
	if sumResources != storedResources {
		t.Fatalf("ledger resource accounting %d != stored resource rows %d", sumResources, storedResources)
	}
	if sumEdges != storedEdges {
		t.Fatalf("ledger edge accounting %d != stored edge rows %d", sumEdges, storedEdges)
	}
	if distinctIDs != totalIDs {
		t.Fatalf("ledger batch identities not unique: %d rows, %d distinct ids", totalIDs, distinctIDs)
	}
}

// ing03WaitBothWaiting polls pg_stat_activity until one sample shows at
// least two distinct pids (from the named applications) waiting on a lock
// simultaneously, and returns those pids. A single sample is required so
// the witness proves genuine simultaneity, not two sequential waits.
func ing03WaitBothWaiting(t *testing.T, service *ing03Service, appNames []string, timeout time.Duration) []int32 {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pids := map[int32]bool{}
		for _, backend := range service.backendsByApplication(appNames...) {
			if backend.WaitEventType != nil && *backend.WaitEventType == "Lock" {
				pids[backend.Pid] = true
			}
		}
		if len(pids) >= 2 {
			out := make([]int32, 0, len(pids))
			for pid := range pids {
				out = append(out, pid)
			}
			return out
		}
		time.Sleep(2 * time.Millisecond)
	}
	return nil
}

func ing03AssertNeverActive(t *testing.T, service *ing03Service, where string) {
	t.Helper()
	if _, active := service.activePointerGeneration(ing03Project); active {
		t.Fatalf("%s: the active generation pointer moved", where)
	}
}

// ---------------------------------------------------------------------------
// pg_terminate_backend at every durability seam.
// ---------------------------------------------------------------------------

func TestPostgresGenerationLoadKillInjectionAtEveryDurabilitySeam(t *testing.T) {
	// Clean reference load for the byte-stable convergence target.
	reference := newIngest03Service(t)
	referenceDir := t.TempDir()
	ing03WriteFixtures(t, referenceDir, 2, false)
	referenceRef := ing03Ref(t, ing03Project, ing03Generation)
	if _, err := LoadPostgresGeneration(context.Background(),
		reference.loadOptions(referenceDir, ing03Project, &referenceRef),
		reference.pgOptions(ing03AppBase+"-dur-ref")); err != nil {
		t.Fatalf("reference load: %s", reference.scrub(err))
	}
	referenceFingerprint := reference.fingerprint()

	seams := []struct {
		name  string
		table string
		// manifestExpected is the required manifest state right after the
		// kill: absent (create never committed) or LOADING (create
		// committed; the creator is not allowed to mark FAILED when the
		// commit outcome is unknown).
		manifestExpected string
		// rowBarrier: the seam's rows must be absent while the kill holds;
		// table is locked and its statements never committed.
		rowBarrier func(t *testing.T, service *ing03Service)
		// blockRowLevel uses the manifest row lock instead of a table lock.
		rowLevel bool
	}{
		{name: "manifest-create", table: "dataset_generation_manifest", manifestExpected: "absent"},
		{name: "resource-batch", table: "fhir_resource", manifestExpected: "LOADING",
			rowBarrier: func(t *testing.T, service *ing03Service) {
				if count := service.countTable("fhir_resource", ""); count != 0 {
					t.Fatalf("resource rows = %d while the first resource batch was still parked and killed, want 0", count)
				}
			}},
		{name: "edge-batch", table: "fhir_edge_occurrence", manifestExpected: "LOADING"},
		{name: "field-profile", table: "fhir_field_profile", manifestExpected: "LOADING"},
		{name: "inventory", table: "fhir_resource_inventory_count", manifestExpected: "LOADING"},
		{name: "semantic-contributions", table: "fhir_semantic_inventory_contribution", manifestExpected: "LOADING"},
		{name: "staged-transition", table: "dataset_generation_manifest", manifestExpected: "LOADING", rowLevel: true},
	}

	for _, seam := range seams {
		t.Run(seam.name, func(t *testing.T) {
			service := newIngest03Service(t)
			service.migrate()
			metaDir := t.TempDir()
			ing03WriteFixtures(t, metaDir, 2, false)
			ref := ing03Ref(t, ing03Project, ing03Generation)
			app := ing03AppBase + "-dur-" + seam.name

			// The row-level blocker needs the committed manifest row, so
			// it is acquired after the manifest-create event; table-level
			// blockers park the statement before the seam is reached.
			manifestCreated := make(chan struct{})
			var manifestOnce sync.Once
			var release func()
			if !seam.rowLevel {
				release = service.blockTable(seam.table)
			}

			loadErrCh := make(chan error, 1)
			go func() {
				opts := service.loadOptions(metaDir, ing03Project, &ref)
				opts.EventSink = func(event string, _ map[string]any) {
					if event == "go_pg_manifest_create" {
						manifestOnce.Do(func() { close(manifestCreated) })
					}
				}
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				_, err := LoadPostgresGeneration(ctx, opts, service.pgOptions(app))
				loadErrCh <- err
			}()

			if seam.rowLevel {
				select {
				case <-manifestCreated:
				case err := <-loadErrCh:
					t.Fatalf("load ended (%v) before manifest create: kill injection never armed", err)
				case <-time.After(60 * time.Second):
					t.Fatal("manifest create never happened: kill injection never armed")
				}
				// The parent arms the row lock as soon as the committed
				// manifest exists: the load's own transition UPDATE parks
				// behind it on its way to STAGED.
				release = service.blockManifestRow(ing03Project, ing03Generation)
			}

			// Positive witness: the victim backend is parked on the seam
			// lock before anything is terminated. If this fails, the kill
			// would be vacuous and the test must fail.
			if _, parked := service.waitForBackendWaiting(app, 60*time.Second); !parked {
				t.Fatalf("no backend for %s was ever witnessed waiting on the seam lock: the kill injection is vacuous", app)
			}
			killed := service.terminateAppBackends(app)
			if killed < 1 {
				t.Fatalf("pg_terminate_backend terminated %d backends for %s, want >= 1: the kill never landed", killed, app)
			}
			if release != nil {
				release()
			}

			var loadErr error
			select {
			case loadErr = <-loadErrCh:
			case <-time.After(90 * time.Second):
				t.Fatal("load did not return after the kill")
			}
			if loadErr == nil {
				t.Fatal("killed load reported success")
			}
			if !errors.Is(loadErr, ErrGenerationManifestOutcomeUnknown) {
				t.Fatalf("killed load error = %T %v, want %v (the killed commit's outcome is genuinely unknown)",
					loadErr, loadErr, ErrGenerationManifestOutcomeUnknown)
			}

			// Invariants at the moment of the kill: never visible-partial.
			state, found := service.manifestState(ing03Project, ing03Generation)
			switch seam.manifestExpected {
			case "absent":
				if found {
					t.Fatalf("manifest exists in state %q after the manifest-create kill, want absent", state)
				}
			case "LOADING":
				if !found || state != string(publication.StateLoading) {
					t.Fatalf("manifest state = %q found=%v, want LOADING (unknown commit outcome must not mark FAILED)", state, found)
				}
			}
			ing03AssertNeverActive(t, service, "after kill")
			if seam.rowBarrier != nil {
				seam.rowBarrier(t, service)
			}
			ing03AssertLedgerEquality(t, service)

			// Replay convergence: a fresh attempt (resumer of the LOADING
			// claim, or fresh create where absent) must converge to the
			// clean-load fingerprint, byte-stable business content.
			replayCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			if _, err := LoadPostgresGeneration(replayCtx,
				service.loadOptions(metaDir, ing03Project, &ref),
				service.pgOptions(app+"-replay")); err != nil {
				t.Fatalf("replay after kill: %s", service.scrub(err))
			}
			if state, found := service.manifestState(ing03Project, ing03Generation); !found || state != string(publication.StateStaged) {
				t.Fatalf("replayed manifest = %q found=%v, want STAGED", state, found)
			}
			ing03AssertNeverActive(t, service, "after replay")
			ing03AssertLedgerEquality(t, service)
			if got := service.fingerprint(); got != referenceFingerprint {
				t.Fatalf("replayed fingerprint = %s, want the clean-load %s", got, referenceFingerprint)
			}
		})
	}

	// Non-vacuity control: the identical fixture without any kill stages on
	// the first attempt with the reference fingerprint, so every kill
	// assertion above could genuinely have failed.
	control := newIngest03Service(t)
	controlDir := t.TempDir()
	ing03WriteFixtures(t, controlDir, 2, false)
	controlRef := ing03Ref(t, ing03Project, ing03Generation)
	if _, err := LoadPostgresGeneration(context.Background(),
		control.loadOptions(controlDir, ing03Project, &controlRef),
		control.pgOptions(ing03AppBase+"-dur-control")); err != nil {
		t.Fatalf("control load: %s", control.scrub(err))
	}
	if got := control.fingerprint(); got != referenceFingerprint {
		t.Fatalf("control fingerprint = %s, want the reference %s", got, referenceFingerprint)
	}
}

// ---------------------------------------------------------------------------
// Separate-OS-process crash and replay convergence.
// ---------------------------------------------------------------------------

// ing03HelperConfig configures the replay-helper child process. The DSN is
// never carried here: the child derives it from its own inherited
// LOOM_TEST_POSTGRES_URL plus the shared database name.
type ing03HelperConfig struct {
	Database    string `json:"database"`
	Schema      string `json:"schema"`
	MetaDir     string `json:"meta_dir"`
	SchemaFile  string `json:"schema_file"`
	Project     string `json:"project"`
	Generation  string `json:"generation"`
	AuthPath    string `json:"auth_path"`
	Application string `json:"application"`
}

const ing03HelperRunFilter = "^TestPostgresGenerationLoadReplayHelper$"

func TestPostgresGenerationLoadReplayHelper(t *testing.T) {
	rawConfig := os.Getenv("LOOM_ING03_HELPER_CONFIG")
	if rawConfig == "" {
		t.Skip("child-process helper: only runs when invoked with LOOM_ING03_HELPER_CONFIG")
	}
	var config ing03HelperConfig
	if err := json.Unmarshal([]byte(rawConfig), &config); err != nil {
		fmt.Printf("EVENT helper-config-error %v\n", err)
		os.Exit(4)
	}
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		fmt.Println("EVENT helper-missing-url")
		os.Exit(4)
	}
	dsn, err := ing03HelperDSN(raw, config.Database)
	if err != nil {
		fmt.Printf("EVENT helper-dsn-error %v\n", err)
		os.Exit(4)
	}
	ref, err := publication.NewRef(config.Project, config.Generation)
	if err != nil {
		fmt.Printf("EVENT helper-ref-error %v\n", err)
		os.Exit(4)
	}
	opts := LoadOptions{
		Schema:           config.SchemaFile,
		MetaDir:          config.MetaDir,
		Project:          config.Project,
		Dataset:          &ref,
		AuthResourcePath: config.AuthPath,
	}
	opts.EventSink = func(event string, _ map[string]any) {
		fmt.Printf("EVENT %s\n", event)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	// The catalog evidence writers are wired by the caller exactly as a
	// production composition would, over the child's own pool.
	catalogCfg, cfgErr := pgxpool.ParseConfig(dsn)
	if cfgErr != nil {
		fmt.Printf("EVENT helper-pool-config %v\n", cfgErr)
		os.Exit(4)
	}
	catalogCfg.MaxConns = 4
	catalogCfg.ConnConfig.RuntimeParams["application_name"] = config.Application
	catalogPool, poolErr := pgxpool.NewWithConfig(ctx, catalogCfg)
	if poolErr != nil {
		fmt.Printf("EVENT helper-pool-error %v\n", poolErr)
		os.Exit(4)
	}
	defer catalogPool.Close()
	catalog := ing03WireCatalogWriters(catalogPool, config.Schema, nil, func(err error) {
		fmt.Printf("EVENT helper-catalog-wiring %v\n", err)
		os.Exit(4)
	})
	if _, err := LoadPostgresGeneration(ctx, opts, PGSourceOptions{
		DSN: dsn, Schema: config.Schema, ApplicationName: config.Application, MaxConns: 4,
		Catalog: catalog,
	}); err != nil {
		switch {
		case errors.Is(err, ErrGenerationAlreadyStaged):
			fmt.Println("EVENT outcome already-staged")
			os.Exit(2)
		case errors.Is(err, ErrGenerationManifestIdentityConflict):
			fmt.Println("EVENT outcome identity-conflict")
			os.Exit(3)
		case errors.Is(err, ErrGenerationLoadAlreadyFailed):
			fmt.Println("EVENT outcome already-failed")
			os.Exit(6)
		}
		fmt.Printf("EVENT outcome error %v\n", err)
		os.Exit(4)
	}
	fmt.Println("EVENT outcome staged")
	os.Exit(0)
}

// ing03HelperDSN swaps the database of the service URL; the credential
// material stays inside the returned string and is never printed.
func ing03HelperDSN(raw, database string) (string, error) {
	u, err := parsePostgresURL(raw)
	if err != nil {
		return "", err
	}
	if q := u.Query(); q.Get("dbname") != "" {
		q.Del("dbname")
		u.RawQuery = q.Encode()
	}
	u.Path = "/" + database
	return u.String(), nil
}

func parsePostgresURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse postgres URL: %w", err)
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return nil, fmt.Errorf("postgres URL scheme %q is not postgres/postgresql", u.Scheme)
	}
	return u, nil
}

// ing03DatabaseFromDSN extracts the database name component the parent
// created, so child processes can address the same owned fixture without
// the parent ever echoing credentials.
func ing03DatabaseFromDSN(t *testing.T, dsn string) string {
	t.Helper()
	u, err := parsePostgresURL(dsn)
	if err != nil {
		t.Fatalf("parse fixture DSN: %v", err)
	}
	database := strings.TrimPrefix(u.Path, "/")
	if !ing03LoadNamePattern.MatchString(database) {
		t.Fatalf("fixture database %q does not match the owned name pattern", database)
	}
	return database
}

func runIngest03Helper(t *testing.T, service *ing03Service, config ing03HelperConfig) (int, []string, string) {
	t.Helper()
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal helper config: %v", err)
	}
	cmd := exec.Command(os.Args[0], "-test.run="+ing03HelperRunFilter, "-test.timeout=180s", "-test.v")
	cmd.Env = append(os.Environ(), "LOOM_ING03_HELPER_CONFIG="+string(encoded))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("helper stdout: %v", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	cmd.WaitDelay = 30 * time.Second
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	if cmd.Process.Pid == os.Getpid() {
		t.Fatalf("helper reported the parent pid %d: it is not a separate process", cmd.Process.Pid)
	}
	events := []string{}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "EVENT ") {
			events = append(events, strings.TrimPrefix(line, "EVENT "))
		}
	}
	waitErr := cmd.Wait()
	code := 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("wait helper: %v (output=%s)", waitErr, service.scrub(stderr.String()))
		}
	}
	return code, events, service.scrub(stderr.String())
}

func TestPostgresGenerationLoadSeparateProcessCrashReplayConvergence(t *testing.T) {
	shared := newIngest03Service(t)
	shared.migrate()
	dbName := ing03DatabaseFromDSN(t, shared.dsn)

	metaDir := t.TempDir()
	ing03WriteFixtures(t, metaDir, 2, false)
	schemaFile := repoPath(t, "schemas", "graph-fhir.json")

	// Reference fingerprint for the crashing generation's literal name.
	crashGeneration := "gen-crash-03"
	reference := newIngest03Service(t)
	referenceDir := t.TempDir()
	ing03WriteFixtures(t, referenceDir, 2, false)
	referenceRef := ing03Ref(t, ing03Project, crashGeneration)
	if _, err := LoadPostgresGeneration(context.Background(),
		reference.loadOptions(referenceDir, ing03Project, &referenceRef),
		reference.pgOptions(ing03AppBase+"-crash-ref")); err != nil {
		t.Fatalf("reference load: %s", reference.scrub(err))
	}
	referenceFingerprint := reference.fingerprint()

	baseConfig := ing03HelperConfig{
		Database: dbName, Schema: shared.schema, MetaDir: metaDir, SchemaFile: schemaFile,
		Project: ing03Project, Generation: crashGeneration, AuthPath: ing03AuthPath,
	}

	// First process crashes mid-load. The edge table is locked first, and
	// the parent SIGKILLs the child the moment both resource batches are
	// witnessed committed while it is parked on the first edge statement:
	// the crash point is deterministic, and no client failure handling can
	// run — only socket closure reaps the backend.
	crashBlockRelease := shared.blockTable("fhir_edge_occurrence")
	crashConfig := baseConfig
	crashConfig.Application = ing03AppBase + "-crash-victim"
	cmd := exec.Command(os.Args[0], "-test.run="+ing03HelperRunFilter, "-test.timeout=180s", "-test.v")
	encoded, err := json.Marshal(crashConfig)
	if err != nil {
		crashBlockRelease()
		t.Fatalf("marshal crash config: %v", err)
	}
	cmd.Env = append(os.Environ(), "LOOM_ING03_HELPER_CONFIG="+string(encoded))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		crashBlockRelease()
		t.Fatalf("crash stdout: %v", err)
	}
	var crashStderr strings.Builder
	cmd.Stderr = &crashStderr
	cmd.WaitDelay = 30 * time.Second
	if err := cmd.Start(); err != nil {
		crashBlockRelease()
		t.Fatalf("start crash helper: %v", err)
	}
	if cmd.Process.Pid == os.Getpid() || cmd.Process.Pid <= 0 {
		crashBlockRelease()
		t.Fatalf("crash helper pid %d is not a separate live process", cmd.Process.Pid)
	}
	batchCommits := 0
	sawManifestCreate := false
	killed := false
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "EVENT ") {
			continue
		}
		event := strings.TrimPrefix(line, "EVENT ")
		switch event {
		case "go_pg_manifest_create":
			sawManifestCreate = true
		case "go_pg_resource_batch_commit":
			batchCommits++
			if batchCommits == 2 && !killed {
				killed = true
				if err := cmd.Process.Kill(); err != nil {
					crashBlockRelease()
					t.Fatalf("kill crash helper: %v", err)
				}
			}
		}
	}
	if !sawManifestCreate || !killed {
		crashBlockRelease()
		t.Fatalf("crash timing was vacuous: manifestCreate=%v batchCommits=%d", sawManifestCreate, batchCommits)
	}
	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() >= 0 {
			crashBlockRelease()
			t.Fatalf("crash helper exit = %v, want SIGKILL (-1); stderr=%s",
				err, shared.scrub(crashStderr.String()))
		}
	} else {
		crashBlockRelease()
		t.Fatal("crash helper reported clean exit after SIGKILL: it never crashed mid-load")
	}
	// The crashed process is gone; free the edge lock so the replay's edge
	// pass can proceed.
	crashBlockRelease()

	// After the crash: manifest must be LOADING (create was committed —
	// the witnessed batch commits came after it) and never STAGED/FAILED;
	// the pointer never moved; the ledger still equals the committed rows;
	// the parked edge batch wrote nothing.
	state, found := shared.manifestState(ing03Project, crashGeneration)
	if !found || state != string(publication.StateLoading) {
		t.Fatalf("post-crash manifest state = %q found=%v, want LOADING", state, found)
	}
	ing03AssertNeverActive(t, shared, "after process crash")
	ing03AssertLedgerEquality(t, shared)
	if resources := shared.countTable("fhir_resource", ""); resources != 5 {
		t.Fatalf("post-crash resource rows = %d, want the 5 witnessed committed", resources)
	}
	if edges := shared.countTable("fhir_edge_occurrence", ""); edges != 0 {
		t.Fatalf("post-crash edge rows = %d, want 0 (the parked batch never committed)", edges)
	}

	// Replay from a distinct second OS process converges the generation.
	replayConfig := baseConfig
	replayConfig.Application = ing03AppBase + "-crash-replay"
	code, events, stderr := runIngest03Helper(t, shared, replayConfig)
	if code != 0 {
		t.Fatalf("replay process exit = %d (events=%v stderr=%s), want 0 (converged)", code, events, stderr)
	}
	if state, found := shared.manifestState(ing03Project, crashGeneration); !found || state != string(publication.StateStaged) {
		t.Fatalf("replayed manifest = %q found=%v, want STAGED", state, found)
	}
	ing03AssertNeverActive(t, shared, "after replay")
	ing03AssertLedgerEquality(t, shared)
	if got := shared.fingerprint(); got != referenceFingerprint {
		t.Fatalf("cross-process replay fingerprint = %s, want the clean-load %s (byte-stable content)", got, referenceFingerprint)
	}

	// Third distinct process: committed success is read back, never
	// re-run; nothing new is written.
	ledgerBefore := shared.countTable("batch_write_ledger", "")
	repeatConfig := baseConfig
	repeatConfig.Application = ing03AppBase + "-crash-repeat"
	code, events, stderr = runIngest03Helper(t, shared, repeatConfig)
	if code != 2 {
		t.Fatalf("repeat process exit = %d (events=%v stderr=%s), want 2 (ErrGenerationAlreadyStaged)", code, events, stderr)
	}
	if ledgerAfter := shared.countTable("batch_write_ledger", ""); ledgerAfter != ledgerBefore {
		t.Fatalf("already-staged repeat wrote %d new ledger rows, want 0", ledgerAfter-ledgerBefore)
	}
	if got := shared.fingerprint(); got != referenceFingerprint {
		t.Fatalf("already-staged repeat changed the fingerprint to %s, want %s unchanged", got, referenceFingerprint)
	}

	// Identity conflict: a differently-spaced copy of the same schema has
	// a different SHA-256, so the claim must refuse by name.
	tamperedDir := t.TempDir()
	original, err := os.ReadFile(schemaFile)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	tamperedFile := filepath.Join(tamperedDir, "graph-fhir-tampered.json")
	if err := os.WriteFile(tamperedFile, append(append([]byte{}, original...), '\n'), 0o600); err != nil {
		t.Fatalf("write tampered schema: %v", err)
	}
	identityConfig := baseConfig
	identityConfig.Application = ing03AppBase + "-crash-identity"
	identityConfig.SchemaFile = tamperedFile
	code, events, stderr = runIngest03Helper(t, shared, identityConfig)
	if code != 3 {
		t.Fatalf("identity-conflict process exit = %d (events=%v stderr=%s), want 3 (ErrGenerationManifestIdentityConflict)",
			code, events, stderr)
	}
	if got := shared.fingerprint(); got != referenceFingerprint {
		t.Fatalf("refused identity conflict changed content to %s, want %s unchanged", got, referenceFingerprint)
	}

	// Non-vacuity: the tampered schema file is loadable for a fresh
	// generation, proving the refusal above came from the identity
	// mismatch against the stored manifest, not a broken file.
	freshConfig := baseConfig
	freshConfig.Application = ing03AppBase + "-crash-tampered-fresh"
	freshConfig.Generation = "gen-crash-tampered"
	code, events, stderr = runIngest03Helper(t, shared, freshConfig)
	if code != 0 {
		t.Fatalf("tampered-schema control exit = %d (events=%v stderr=%s), want 0: the control could not have failed above",
			code, events, stderr)
	}
}

// ---------------------------------------------------------------------------
// Same-generation load races.
// ---------------------------------------------------------------------------

func TestPostgresGenerationLoadSameGenerationLoadRace(t *testing.T) {
	var referenceFingerprint string
	for round := 1; round <= ing03RaceRounds; round++ {
		service := newIngest03Service(t)
		metaDir := t.TempDir()
		ing03WriteFixtures(t, metaDir, 2, false)
		generation := fmt.Sprintf("gen-race-%02d", round)
		ref := ing03Ref(t, ing03Project, generation)
		apps := []string{
			ing03AppBase + fmt.Sprintf("-race-a-%02d", round),
			ing03AppBase + fmt.Sprintf("-race-b-%02d", round),
		}

		sampling := make(chan int, 1)
		done := make(chan struct{})
		go func() { sampling <- service.maxConcurrentDistinctApps(done, apps[0], apps[1]) }()

		start := make(chan struct{})
		var wg sync.WaitGroup
		results := make([]error, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				_, err := LoadPostgresGeneration(ctx,
					service.loadOptions(metaDir, ing03Project, &ref),
					service.pgOptions(apps[i]))
				results[i] = err
			}(i)
		}
		close(start)
		wg.Wait()
		close(done)
		overlap := <-sampling

		// Positive overlap witness: both racing pools were live
		// simultaneously with distinct application tags; a race that
		// silently serialized must fail here.
		if overlap < 2 {
			t.Fatalf("round %d overlap witness = %d distinct concurrent apps, want >= 2: the race serialized", round, overlap)
		}

		for i, err := range results {
			if err == nil || errors.Is(err, ErrGenerationAlreadyStaged) ||
				errors.Is(err, ErrGenerationLoadAlreadyFailed) ||
				errors.Is(err, ErrGenerationManifestOutcomeUnknown) {
				continue
			}
			t.Fatalf("round %d racer %d error = %T %v, want success, already-staged, already-failed, or outcome-unknown",
				round, i, err, err)
		}
		complete := 0
		for _, err := range results {
			if err == nil || errors.Is(err, ErrGenerationAlreadyStaged) {
				complete++
			}
		}
		if complete == 0 {
			t.Fatalf("round %d: neither racer reported a completed or read-back-staged load: %v / %v", round, results[0], results[1])
		}

		if state, found := service.manifestState(ing03Project, generation); !found || state != string(publication.StateStaged) {
			t.Fatalf("round %d manifest = %q found=%v, want STAGED", round, state, found)
		}
		ing03AssertNeverActive(t, service, fmt.Sprintf("race round %d", round))
		if count := service.countTable("fhir_resource", ""); count != 5 {
			t.Fatalf("round %d resource rows = %d, want exactly 5 (the race duplicated or lost nothing)", round, count)
		}
		ing03AssertLedgerEquality(t, service)

		if round == 1 {
			reference := newIngest03Service(t)
			referenceDir := t.TempDir()
			ing03WriteFixtures(t, referenceDir, 2, false)
			referenceRef := ing03Ref(t, ing03Project, generation)
			if _, err := LoadPostgresGeneration(context.Background(),
				reference.loadOptions(referenceDir, ing03Project, &referenceRef),
				reference.pgOptions(ing03AppBase+"-race-ref")); err != nil {
				t.Fatalf("race reference load: %s", reference.scrub(err))
			}
			referenceFingerprint = reference.fingerprint()
			if got := service.fingerprint(); got != referenceFingerprint {
				t.Fatalf("round %d raced fingerprint = %s, want the single-loader %s", round, got, referenceFingerprint)
			}
		} else if referenceFingerprint == "" {
			t.Fatal("reference fingerprint was never captured")
		}
	}

	// Sequential control: the identical flow serialized under a mutex must
	// show no concurrency; a witness that still reports overlap proves the
	// sampling itself is broken.
	service := newIngest03Service(t)
	metaDir := t.TempDir()
	ing03WriteFixtures(t, metaDir, 2, false)
	ref := ing03Ref(t, ing03Project, "gen-race-ctl")
	apps := []string{ing03AppBase + "-race-ctl-a", ing03AppBase + "-race-ctl-b"}
	sampling := make(chan int, 1)
	done := make(chan struct{})
	go func() { sampling <- service.maxConcurrentDistinctApps(done, apps[0], apps[1]) }()
	var mutex sync.Mutex
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			mutex.Lock()
			defer mutex.Unlock()
			_, err := LoadPostgresGeneration(context.Background(),
				service.loadOptions(metaDir, ing03Project, &ref), service.pgOptions(apps[i]))
			results[i] = err
		}(i)
	}
	wg.Wait()
	close(done)
	if overlap := <-sampling; overlap >= 2 {
		t.Fatalf("sequential control witnessed %d concurrent apps: the overlap sampler is broken", overlap)
	}
	for i, err := range results {
		if err != nil && !errors.Is(err, ErrGenerationAlreadyStaged) {
			t.Fatalf("control racer %d error = %T %v", i, err, err)
		}
	}
	if state, found := service.manifestState(ing03Project, "gen-race-ctl"); !found || state != string(publication.StateStaged) {
		t.Fatalf("control manifest = %q found=%v, want STAGED", state, found)
	}
}

// ---------------------------------------------------------------------------
// Activation CAS races.
// ---------------------------------------------------------------------------

func TestPostgresGenerationLoadActivationCASRace(t *testing.T) {
	for round := 1; round <= ing03RaceRounds; round++ {
		service := newIngest03Service(t)
		metaDir := t.TempDir()
		ing03WriteFixtures(t, metaDir, 2, false)
		generation := fmt.Sprintf("gen-act-race-%02d", round)
		ref := ing03Ref(t, ing03Project, generation)
		if _, err := LoadPostgresGeneration(context.Background(),
			service.loadOptions(metaDir, ing03Project, &ref),
			service.pgOptions(ing03AppBase+fmt.Sprintf("-act-race-load-%02d", round))); err != nil {
			t.Fatalf("round %d load: %s", round, service.scrub(err))
		}

		apps := []string{
			ing03AppBase + fmt.Sprintf("-act-race-a-%02d", round),
			ing03AppBase + fmt.Sprintf("-act-race-b-%02d", round),
		}
		// The pointer table is locked so both activations are verifiably
		// in flight on the CAS path at once (parked on the same lock with
		// distinct pg_backend_pid rows) instead of hoping a sampler catches
		// a millisecond-scale overlap. The CAS semantics run when the lock
		// releases: the loser's compare-and-swap sees the winner's commit.
		release := service.blockTable("dataset_active_generation")
		start := make(chan struct{})
		var wg sync.WaitGroup
		results := make([]error, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
				defer cancel()
				results[i] = ActivatePostgresGeneration(ctx, ref, service.pgOptions(apps[i]))
			}(i)
		}
		close(start)
		parkedPIDs := ing03WaitBothWaiting(t, service, apps, 60*time.Second)
		if len(parkedPIDs) < 2 {
			release()
			t.Fatalf("activation round %d: only %d activations were ever in flight on the CAS lock (%v): the race serialized", round, len(parkedPIDs), parkedPIDs)
		}
		release()
		wg.Wait()
		nils, conflicts := 0, 0
		for i, err := range results {
			switch {
			case err == nil:
				nils++
			case errors.Is(err, datasetpostgres.ErrActivationConflict):
				conflicts++
			default:
				t.Fatalf("activation round %d racer %d error = %T %v, want success or ErrActivationConflict",
					i, round, err, err)
			}
		}
		if nils == 0 {
			t.Fatalf("activation round %d: neither racer succeeded: %v / %v", round, results[0], results[1])
		}
		if pointer, active := service.activePointerGeneration(ing03Project); !active || pointer != generation {
			t.Fatalf("activation round %d pointer = %q active=%v, want %q", round, pointer, active, generation)
		}
	}

	// Sequential control: a plainly serialized activation succeeds, so the
	// CAS verdict above was reachable.
	service := newIngest03Service(t)
	metaDir := t.TempDir()
	ing03WriteFixtures(t, metaDir, 2, false)
	ref := ing03Ref(t, ing03Project, "gen-act-ctl")
	if _, err := LoadPostgresGeneration(context.Background(),
		service.loadOptions(metaDir, ing03Project, &ref),
		service.pgOptions(ing03AppBase+"-act-ctl-load")); err != nil {
		t.Fatalf("control load: %s", service.scrub(err))
	}
	if err := ActivatePostgresGeneration(context.Background(), ref,
		service.pgOptions(ing03AppBase+"-act-ctl")); err != nil {
		t.Fatalf("control activation: %s", service.scrub(err))
	}
	if pointer, active := service.activePointerGeneration(ing03Project); !active || pointer != "gen-act-ctl" {
		t.Fatalf("control pointer = %q active=%v", pointer, active)
	}
}

// ---------------------------------------------------------------------------
// Cancellation branches.
// ---------------------------------------------------------------------------

func TestPostgresGenerationLoadCancellationBranches(t *testing.T) {
	t.Run("safe-point cancellation marks FAILED", func(t *testing.T) {
		service := newIngest03Service(t)
		metaDir := t.TempDir()
		ing03WriteFixtures(t, metaDir, 2, false)
		ref := ing03Ref(t, ing03Project, ing03Generation)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var events []string
		opts := service.loadOptions(metaDir, ing03Project, &ref)
		// Cancel synchronously inside the manifest-create safe point: the
		// very next ctx check runs before any store statement is dispatched,
		// so the safe-point branch is deterministic, not timing-dependent.
		opts.EventSink = func(event string, _ map[string]any) {
			events = append(events, event)
			if event == "go_pg_manifest_create" {
				cancel()
			}
		}
		_, err := LoadPostgresGeneration(ctx, opts, service.pgOptions(ing03AppBase+"-cancel-safe"))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("safe-point cancellation = %T %v, want context.Canceled", err, err)
		}
		if !containsPrefix(events, "go_pg_manifest_create") {
			t.Fatalf("the cancellation was never armed at the safe point: %v", events)
		}
		if containsPrefix(events, "go_pg_resource_batch_commit") {
			t.Fatalf("safe-point cancellation still committed a resource batch: %v", events)
		}
		if state, found := service.manifestState(ing03Project, ing03Generation); !found || state != string(publication.StateFailed) {
			t.Fatalf("safe-point manifest = %q found=%v, want FAILED (the creator owns a known-outcome cancellation)", state, found)
		}
		ing03AssertNeverActive(t, service, "safe-point cancellation")
		if ledger := service.countTable("batch_write_ledger", ""); ledger != 0 {
			t.Fatalf("safe-point cancellation wrote %d ledger rows, want 0", ledger)
		}
		if rows := service.countTable("fhir_resource", ""); rows != 0 {
			t.Fatalf("safe-point cancellation wrote %d resource rows, want 0", rows)
		}
		if activateErr := ActivatePostgresGeneration(context.Background(), ref,
			service.pgOptions(ing03AppBase+"-cancel-safe-act")); !errors.Is(activateErr, publication.ErrGenerationNotReady) {
			t.Fatalf("activation after safe-point cancellation = %v, want ErrGenerationNotReady", activateErr)
		}
		if _, retryErr := LoadPostgresGeneration(context.Background(),
			service.loadOptions(metaDir, ing03Project, &ref),
			service.pgOptions(ing03AppBase+"-cancel-safe-retry")); !errors.Is(retryErr, ErrGenerationLoadAlreadyFailed) {
			t.Fatalf("retry after safe-point cancellation = %v, want ErrGenerationLoadAlreadyFailed", retryErr)
		}
	})

	t.Run("in-flight cancellation keeps LOADING and replay converges", func(t *testing.T) {
		reference := newIngest03Service(t)
		referenceDir := t.TempDir()
		ing03WriteFixtures(t, referenceDir, 2, false)
		referenceRef := ing03Ref(t, ing03Project, ing03Generation)
		if _, err := LoadPostgresGeneration(context.Background(),
			reference.loadOptions(referenceDir, ing03Project, &referenceRef),
			reference.pgOptions(ing03AppBase+"-cancel-ref")); err != nil {
			t.Fatalf("reference load: %s", reference.scrub(err))
		}
		referenceFingerprint := reference.fingerprint()

		service := newIngest03Service(t)
		service.migrate()
		metaDir := t.TempDir()
		ing03WriteFixtures(t, metaDir, 2, false)
		ref := ing03Ref(t, ing03Project, ing03Generation)
		release := service.blockTable("fhir_edge_occurrence")

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var events []string
		var eventsMu sync.Mutex
		opts := service.loadOptions(metaDir, ing03Project, &ref)
		opts.EventSink = func(event string, _ map[string]any) {
			eventsMu.Lock()
			events = append(events, event)
			eventsMu.Unlock()
		}
		app := ing03AppBase + "-cancel-inflight"
		loadErrCh := make(chan error, 1)
		go func() {
			_, err := LoadPostgresGeneration(ctx, opts, service.pgOptions(app))
			loadErrCh <- err
		}()

		// The load must be parked mid-statement on the edge batch (the
		// resource pass is already witnessed committed) before the cancel.
		if _, parked := service.waitForBackendWaiting(app, 60*time.Second); !parked {
			release()
			t.Fatal("the edge statement was never witnessed parked: the cancellation would not race an in-flight statement")
		}
		eventsMu.Lock()
		preCancel := append([]string{}, events...)
		eventsMu.Unlock()
		if !containsPrefix(preCancel, "go_pg_resource_batch_commit") {
			release()
			t.Fatalf("cancellation armed before any resource commit, not mid-statement: %v", preCancel)
		}
		cancel()
		var loadErr error
		select {
		case loadErr = <-loadErrCh:
		case <-time.After(90 * time.Second):
			release()
			t.Fatal("in-flight cancellation never returned")
		}
		release()
		if !errors.Is(loadErr, context.Canceled) {
			t.Fatalf("in-flight cancellation = %T %v, want context.Canceled", loadErr, loadErr)
		}
		if state, found := service.manifestState(ing03Project, ing03Generation); !found || state != string(publication.StateLoading) {
			t.Fatalf("in-flight-cancelled manifest = %q found=%v, want LOADING (the cancelled statement's outcome is unknown)", state, found)
		}
		ing03AssertNeverActive(t, service, "in-flight cancellation")
		ing03AssertLedgerEquality(t, service)

		// Replay converges: the resumer completes what the cancelled
		// attempt left at an unknown point.
		if _, err := LoadPostgresGeneration(context.Background(),
			service.loadOptions(metaDir, ing03Project, &ref),
			service.pgOptions(app+"-replay")); err != nil {
			t.Fatalf("replay after in-flight cancellation: %s", service.scrub(err))
		}
		if state, found := service.manifestState(ing03Project, ing03Generation); !found || state != string(publication.StateStaged) {
			t.Fatalf("replayed manifest = %q found=%v, want STAGED", state, found)
		}
		ing03AssertLedgerEquality(t, service)
		if got := service.fingerprint(); got != referenceFingerprint {
			t.Fatalf("replayed fingerprint = %s, want the clean-load %s", got, referenceFingerprint)
		}

		// Non-vacuity: without the cancellation the same flow stages
		// directly, so the LOADING outcome above was not the ordinary path.
		control := newIngest03Service(t)
		controlDir := t.TempDir()
		ing03WriteFixtures(t, controlDir, 2, false)
		controlRef := ing03Ref(t, ing03Project, ing03Generation)
		if _, err := LoadPostgresGeneration(context.Background(),
			control.loadOptions(controlDir, ing03Project, &controlRef),
			control.pgOptions(ing03AppBase+"-cancel-control")); err != nil {
			t.Fatalf("control load: %s", control.scrub(err))
		}
		if state, found := control.manifestState(ing03Project, ing03Generation); !found || state != string(publication.StateStaged) {
			t.Fatalf("control manifest = %q found=%v, want STAGED", state, found)
		}
	})
}
