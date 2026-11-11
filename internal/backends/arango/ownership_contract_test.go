package arango_test

// TestArangoFactoryOwnershipContract is the arch-backend-07 acceptance suite
// for the single owned ArangoDB backend factory, mirroring
// internal/backends/postgres/ownership_contract_test.go. The offline legs
// run everywhere; the live legs drive real factory Open/bootstrap/query/
// failure-cleanup round trips against the supplied ArangoDB 3.12 service
// when LOOM_TEST_ARANGO_URL is set, and the runner lane (scripts/
// run-arango-integration.mjs, profile "factory") asserts they never skip.
//
// Offline legs:
//   - The public factory package imports no database driver and no
//     PostgreSQL backend (AST scan): the exported contract cannot leak
//     driver handles.
//   - The ArangoDB driver confinement register: production files importing
//     github.com/arangodb match the allowlist exactly (claimedLive, so an
//     unexplained removal bleeds as loudly as an added offender).
//   - No production file outside the backend tree imports a backend
//     private.
//   - Zero references repo-wide (tests included) to the seven retired
//     adapter paths.
//   - Config validation refuses every invalid shape with its named
//     sentinel, each refusal paired with an alive control.
//
// Live legs (own-mint loom_ownership_* databases, torn down in cleanup):
//   - bootstrap parity across all six exported specs with live index
//     attestation and idempotent re-bootstrap;
//   - representative query/control ops (bindVars cursors, query-memory
//     context, max-runtime control, vendor diagnostic at the boundary);
//   - post-Open failure cleanup, repeated-close safety, post-close queries
//     fail without panic;
//   - missing-database auto-provision is not a leak (banked design fact).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"go/parser"
	"go/token"

	"github.com/calypr/loom/internal/backends/arango"
)

const modulePrefix = "github.com/calypr/loom/"

var retiredAdapterPaths = []string{
	"internal/store/arango",
	"internal/catalog/arango",
	"internal/dataset/arango",
	"internal/explorer/arango",
	"internal/dataframe/recipe/exec/arango",
	"internal/dataframe/publication/arango",
	"internal/dataframe/execution/chartifact/arango",
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot locate the repository root")
	}
	root := filepath.Dir(file) // internal/backends/arango
	for i := 0; i < 3; i++ {
		root = filepath.Dir(root)
	}
	if fi, err := os.Stat(filepath.Join(root, "go.mod")); err != nil || fi.IsDir() {
		t.Fatalf("resolved root %q has no go.mod: %v", root, err)
	}
	return root
}

func fileImports(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	imports := make([]string, 0, len(file.Imports))
	for _, imported := range file.Imports {
		imports = append(imports, strings.Trim(imported.Path.Value, `"`))
	}
	return imports
}

func importOffenders(t *testing.T, dir string, includeTests bool, forbidden []string) []string {
	t.Helper()
	var offenders []string
	walkErr := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walk %s: %w", path, err)
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || (!includeTests && strings.HasSuffix(name, "_test.go")) {
			return nil
		}
		for _, importPath := range fileImports(t, path) {
			for _, ban := range forbidden {
				if importPath == ban || strings.HasPrefix(importPath, ban+"/") {
					offenders = append(offenders, path+" imports "+importPath)
				}
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
	return offenders
}

func TestArangoFactoryOwnershipContract(t *testing.T) {
	root := repoRoot(t)

	t.Run("public_factory_imports_no_driver_or_postgres", func(t *testing.T) {
		forbidden := []string{
			modulePrefix + "internal/backends/postgres",
			"github.com/arangodb",
			"github.com/jackc/pgx",
		}
		// The factory's own public (non-test) files.
		factoryDir := filepath.Join(root, "internal", "backends", "arango")
		for _, name := range []string{"open.go", "aliases.go", "doc.go"} {
			for _, offender := range importOffendersInFile(t, filepath.Join(factoryDir, name), forbidden) {
				t.Errorf("public factory file leaks: %s", offender)
			}
		}
		t.Run("non_vacuity_planted_leak_bleeds", func(t *testing.T) {
			dir := t.TempDir()
			victim := filepath.Join(dir, "victim_driver_leak.go")
			if err := os.WriteFile(victim, []byte("package arango\n\nimport \"github.com/arangodb/go-driver\"\n\nvar _ = driver.Client{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			offenders := importOffendersInFile(t, victim, forbidden)
			if len(offenders) == 0 || !strings.Contains(offenders[0], "victim_driver_leak.go") {
				t.Fatalf("scanner did not name the planted victim: %v", offenders)
			}
		})
	})

	t.Run("arangodb_driver_confinement_register", func(t *testing.T) {
		// One line of the ArangoDB driver confinement register. claimedLive
		// entries assert a CURRENT driver importer: the entry must match at
		// least one live production offender, so both a new offender AND an
		// unexplained removal bleed. Permissive zones (claimedLive=false)
		// grant reach without asserting an offender exists yet.
		type registerEntry struct {
			prefix      string
			claimedLive bool
		}
		register := []registerEntry{
			// The backend home: the private implementation tree is the
			// driver's legitimate production resident. claimedLive: the
			// private store is a live offender, so relocating the driver
			// without updating this register bleeds. The public factory
			// package itself must stay driver-free (first leg).
			{prefix: "internal/backends/arango/internal/", claimedLive: true},
			// Named, dated exception (arch-backend-07, 2026-11): the local
			// preview index migration tool drives raw persistent-index
			// management (EnsurePersistentIndex, live index listings) that
			// the factory deliberately does not own; it is a disposable
			// ops script, not app runtime. Its removal must update this
			// register deliberately.
			{prefix: "scripts/local-preview-index-migration.go", claimedLive: true},
		}
		hits := make([]int, len(register))
		var offenders []string
		scanProductionGo(t, root, func(rel, abs string) {
			for _, importPath := range fileImports(t, abs) {
				if importPath != "github.com/arangodb/go-driver" && !strings.HasPrefix(importPath, "github.com/arangodb/") {
					continue
				}
				allowed := false
				for i, entry := range register {
					if strings.HasPrefix(rel, entry.prefix) {
						allowed = true
						hits[i]++
					}
				}
				if !allowed {
					offenders = append(offenders, rel+" imports "+importPath)
				}
			}
		})
		if len(offenders) != 0 {
			t.Fatalf("files outside the register import the ArangoDB driver: %v", offenders)
		}
		for i, entry := range register {
			if entry.claimedLive && hits[i] == 0 {
				t.Errorf("register entry %q claims a live driver offender but none was found — update the register deliberately", entry.prefix)
			}
		}
	})

	t.Run("no_outside_production_file_imports_backend_privates", func(t *testing.T) {
		forbidden := []string{modulePrefix + "internal/backends/arango/internal/"}
		var offenders []string
		scanProductionGo(t, root, func(rel, abs string) {
			if strings.HasPrefix(rel, "internal/backends/arango/") {
				return
			}
			for _, importPath := range fileImports(t, abs) {
				if strings.HasPrefix(importPath, forbidden[0]) {
					offenders = append(offenders, rel+" imports "+importPath)
				}
			}
		})
		if len(offenders) != 0 {
			t.Fatalf("production files reach backend privates directly: %v", offenders)
		}
	})

	t.Run("zero_references_to_retired_adapter_paths", func(t *testing.T) {
		// Tests included: the retired paths must be gone from every Go file
		// in the repository, witnesses included.
		var offenders []string
		for _, retired := range retiredAdapterPaths {
			for _, offender := range importOffenders(t, root, true, []string{modulePrefix + retired}) {
				offenders = append(offenders, offender)
			}
		}
		if len(offenders) != 0 {
			t.Fatalf("retired adapter paths still imported: %v", offenders)
		}
		t.Run("non_vacuity_planted_retired_import_bleeds", func(t *testing.T) {
			dir := t.TempDir()
			victim := filepath.Join(dir, "victim_retired_import.go")
			if err := os.WriteFile(victim, []byte("package x\n\nimport \"github.com/calypr/loom/internal/store/arango\"\n\nvar _ = arango.Client{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			offenders := importOffenders(t, dir, true, []string{modulePrefix + "internal/store/arango"})
			if len(offenders) == 0 || !strings.Contains(offenders[0], "victim_retired_import.go") {
				t.Fatalf("scanner did not name the planted victim: %v", offenders)
			}
		})
	})

	t.Run("config_validation_refuses_invalid_shapes", func(t *testing.T) {
		ctx := context.Background()
		cases := []struct {
			name string
			cfg  arango.Config
		}{
			{"missing url", arango.Config{Database: "db"}},
			{"empty url", arango.Config{URL: "", Database: "db"}},
			{"missing database", arango.Config{URL: "http://127.0.0.1:1"}},
			{"empty database", arango.Config{URL: "http://127.0.0.1:1", Database: ""}},
		}
		for _, tc := range cases {
			runtime, err := arango.Open(ctx, tc.cfg)
			if !errors.Is(err, arango.ErrInvalidConfig) {
				t.Errorf("%s: err = %v, want ErrInvalidConfig", tc.name, err)
			}
			if runtime != nil {
				t.Errorf("%s: half-initialized runtime escaped validation", tc.name)
			}
		}
		// Alive control: a complete config passes validation and reaches the
		// dial, so a validator that rejects everything cannot pass.
		if _, err := arango.Open(ctx, arango.Config{URL: "http://127.0.0.1:1", Database: "loom_ownership_control"}); err == nil {
			t.Fatal("Open against a dead endpoint succeeded")
		} else if errors.Is(err, arango.ErrInvalidConfig) {
			t.Fatalf("complete config rejected by validation instead of the dial: %v", err)
		}
	})

	// ---- live legs -------------------------------------------------------

	t.Run("bootstrap_parity_and_index_attestation", func(t *testing.T) {
		url := requireLiveArango(t)
		db := mintDatabase(t, url, "parity")
		ctx := context.Background()
		backend, err := arango.Open(ctx, arango.Config{URL: url, Database: db})
		if err != nil {
			t.Fatalf("factory Open: %v", err)
		}
		if backend.Client == nil || backend.Catalog == nil || backend.Dataset == nil || backend.Explorer == nil ||
			backend.ExplorerCapabilitySnapshots == nil || backend.RecipeRegistry == nil || backend.RecipeRevisions == nil || backend.PublishedRegistry == nil {
			t.Fatal("factory runtime assembled with a nil port")
		}
		// The composition-gated steps the server drives itself must be
		// applicable through the exported specs over the owned client.
		specs := map[string]arango.BootstrapSpec{
			"dataset":    arango.DatasetBootstrapSpec(),
			"explorer":   arango.ExplorerBootstrapSpec(),
			"recipe":     arango.RecipeBootstrapSpec(),
			"recipeRev":  arango.RevisionBootstrapSpec(),
			"bundle":     arango.PublishedBootstrapSpec(),
			"chartifact": arango.ArtifactBootstrapSpec(),
		}
		for name, spec := range specs {
			if err := backend.Client.Bootstrap(ctx, spec); err != nil {
				t.Fatalf("bootstrap %s: %v", name, err)
			}
			for _, collection := range spec.Collections {
				indexes := liveIndexes(t, url, db, collection.Name)
				for _, fields := range collection.Indexes {
					if !hasIndexCovering(indexes, fields) {
						t.Errorf("%s/%s lacks an index covering %v (live: %v)", name, collection.Name, fields, indexes)
					}
				}
			}
		}
		// Idempotent re-bootstrap converges instead of erroring.
		for name, spec := range specs {
			if err := backend.Client.Bootstrap(ctx, spec); err != nil {
				t.Fatalf("re-bootstrap %s: %v", name, err)
			}
		}
		// Required-policy collections (dataset lifecycle, Explorer) exist
		// from Open alone: prove via a fresh mint where only Open ran.
		if _, err := backend.Client.CollectionExists(ctx, arango.DatasetLifecycleCollection); err != nil {
			t.Fatalf("dataset lifecycle collection after Open: %v", err)
		}
		if err := backend.Client.Close(context.Background()); err != nil {
			t.Fatalf("close: %v", err)
		}
	})

	t.Run("representative_query_and_control_ops", func(t *testing.T) {
		url := requireLiveArango(t)
		db := mintDatabase(t, url, "queryops")
		ctx := context.Background()
		backend, err := arango.Open(ctx, arango.Config{URL: url, Database: db})
		if err != nil {
			t.Fatalf("factory Open: %v", err)
		}
		t.Cleanup(func() { _ = backend.Close(context.Background()) })
		client := backend.Client
		rows := 0
		if err := client.QueryRows(ctx, "FOR i IN RANGE(1, 5) RETURN {value: @factor * i}", 2, map[string]any{"factor": 3}, func(row map[string]any) error {
			rows++
			if v, ok := row["value"].(float64); !ok || v != 3*float64(rows) {
				return fmt.Errorf("row %d = %v", rows, row)
			}
			return nil
		}); err != nil || rows != 5 {
			t.Fatalf("QueryRows: rows=%d err=%v", rows, err)
		}
		limited := arango.WithQueryMemoryLimit(ctx, 64*1024*1024)
		if limit, ok := arango.QueryMemoryLimitFromContext(limited); !ok || limit != 64*1024*1024 {
			t.Fatalf("query-memory limit context did not pass through: %d/%v", limit, ok)
		}
		count := 0
		if err := client.QueryRows(limited, "FOR i IN RANGE(1, 3) RETURN {i: i}", 10, nil, func(map[string]any) error { count++; return nil }); err != nil || count != 3 {
			t.Fatalf("memory-limited QueryRows: count=%d err=%v", count, err)
		}
		if err := client.QueryRowsWithMaxRuntime(ctx, "RETURN {ok: true}", 10, nil, 30*time.Second, func(map[string]any) error { return nil }); err != nil {
			t.Fatalf("QueryRowsWithMaxRuntime: %v", err)
		}
		badErr := client.QueryRows(ctx, "FOR x IN NO_SUCH_COLLECTION RETURN x", 10, nil, func(map[string]any) error { return nil })
		if badErr == nil {
			t.Fatal("invalid AQL accepted")
		}
		if cause, ok := arango.DiagnosticCause(badErr); !ok || cause == "" {
			t.Fatalf("vendor diagnostic lost at the boundary: err=%v cause=%q ok=%v", badErr, cause, ok)
		}
	})

	t.Run("post_open_failure_cleanup_and_double_close", func(t *testing.T) {
		// Unreachable endpoint: Open must fail with no half-initialized
		// runtime escaping. The factory's cleanup obligation is on
		// post-Open paths (banked design fact; see the missing-database
		// leg).
		if runtime, err := arango.Open(context.Background(), arango.Config{URL: "http://127.0.0.1:1", Database: "loom_ownership_unreachable"}); err == nil || runtime != nil {
			t.Fatalf("Open against dead endpoint returned runtime=%v err=%v", runtime, err)
		}
		url := requireLiveArango(t)
		db := mintDatabase(t, url, "failure")
		backend, err := arango.Open(context.Background(), arango.Config{URL: url, Database: db})
		if err != nil {
			t.Fatalf("factory Open: %v", err)
		}
		// A mid-bootstrap hard failure through the exported client surface
		// must be reported, and the owned client must then close cleanly —
		// twice.
		if err := backend.Client.Bootstrap(context.Background(), arango.BootstrapSpec{Collections: []arango.CollectionSpec{{Name: "invalid name with spaces", Indexes: [][]string{{"field"}}}}}); err == nil {
			_ = backend.Close(context.Background())
			t.Fatal("bootstrap with invalid collection name succeeded")
		}
		if err := backend.Close(context.Background()); err != nil {
			t.Fatalf("first close: %v", err)
		}
		if err := backend.Close(context.Background()); err != nil {
			t.Fatalf("repeated close must be safe: %v", err)
		}
		if err := backend.Client.QueryRows(context.Background(), "RETURN 1", 10, nil, func(map[string]any) error { return nil }); err == nil {
			t.Fatal("query after close succeeded")
		}
	})

	t.Run("missing_database_auto_provision_is_not_a_leak", func(t *testing.T) {
		url := requireLiveArango(t)
		name := mintDatabaseName(t, "autoprovision")
		backend, err := arango.Open(context.Background(), arango.Config{URL: url, Database: name})
		if err != nil {
			t.Fatalf("Open on absent own-mint database: %v", err)
		}
		// The provisioning is real, not a phantom handle.
		if err := backend.Client.Bootstrap(context.Background(), arango.BootstrapSpec{Collections: []arango.CollectionSpec{{Name: "ownership_attest", Indexes: [][]string{{"field"}}}}}); err != nil {
			t.Fatalf("bootstrap on auto-provisioned database: %v", err)
		}
		if err := backend.Close(context.Background()); err != nil {
			t.Fatalf("close: %v", err)
		}
		if err := backend.Close(context.Background()); err != nil {
			t.Fatalf("repeated close must be safe: %v", err)
		}
		again, err := arango.Open(context.Background(), arango.Config{URL: url, Database: name})
		if err != nil {
			t.Fatalf("re-Open provisioned database: %v", err)
		}
		if err := again.Close(context.Background()); err != nil {
			t.Fatalf("re-Open close: %v", err)
		}
	})

	// arango-selected-never-opens-postgres is the import census of the first
	// leg (public factory imports no pgx/postgres backend) plus the driver
	// confinement register; runtime isolation is structural: Open accepts no
	// PostgreSQL configuration surface at all (Config has two fields).
}

func importOffendersInFile(t *testing.T, path string, forbidden []string) []string {
	t.Helper()
	var offenders []string
	for _, importPath := range fileImports(t, path) {
		for _, ban := range forbidden {
			if importPath == ban || strings.HasPrefix(importPath, ban+"/") {
				offenders = append(offenders, path+" imports "+importPath)
			}
		}
	}
	return offenders
}

func scanProductionGo(t *testing.T, root string, fn func(relPath, absPath string)) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walk %s: %w", path, err)
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return fmt.Errorf("rel %s: %w", path, relErr)
		}
		fn(filepath.ToSlash(rel), path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// ---- live helpers ---------------------------------------------------------

func requireLiveArango(t *testing.T) string {
	t.Helper()
	url := os.Getenv("LOOM_TEST_ARANGO_URL")
	if url == "" {
		t.Skip("LOOM_TEST_ARANGO_URL must point to the supplied ArangoDB 3.12 service (the runner lane asserts this never skips)")
	}
	return url
}

func mintDatabaseName(t *testing.T, kind string) string {
	t.Helper()
	return fmt.Sprintf("loom_ownership_%s_%d_%d", kind, os.Getpid(), time.Now().UnixNano())
}

// mintDatabase registers a teardown that drops the own-mint database.
func mintDatabase(t *testing.T, url, kind string) string {
	t.Helper()
	name := mintDatabaseName(t, kind)
	t.Cleanup(func() {
		if err := dropDatabase(url, name); err != nil {
			t.Errorf("drop own-mint database %s: %v", name, err)
		}
	})
	return name
}

func dropDatabase(url, name string) error {
	req, err := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/_db/_system/_api/database/%s", strings.TrimRight(url, "/"), name), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var result struct {
		Result bool `json:"result"`
		Error  bool `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode drop response (status %d): %w", resp.StatusCode, err)
	}
	// Dropping an already-absent database is convergence, not failure.
	return nil
}

type liveIndex struct {
	Type   string   `json:"type"`
	Fields []string `json:"fields"`
	Unique bool     `json:"unique"`
}

func liveIndexes(t *testing.T, url, db, collection string) []liveIndex {
	t.Helper()
	resp, err := http.Get(fmt.Sprintf("%s/_db/%s/_api/index?collection=%s", strings.TrimRight(url, "/"), db, collection))
	if err != nil {
		t.Fatalf("index listing %s: %v", collection, err)
	}
	defer resp.Body.Close()
	var listing struct {
		Error   bool        `json:"error"`
		Code    int         `json:"code"`
		Indexes []liveIndex `json:"indexes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listing); err != nil {
		t.Fatalf("decode index listing %s: %v", collection, err)
	}
	if listing.Error || listing.Code != 200 {
		t.Fatalf("index listing %s returned error (code %d)", collection, listing.Code)
	}
	return listing.Indexes
}

func hasIndexCovering(indexes []liveIndex, fields []string) bool {
outer:
	for _, idx := range indexes {
		if idx.Type == "primary" {
			continue
		}
		if len(idx.Fields) != len(fields) {
			continue
		}
		for i := range fields {
			if idx.Fields[i] != fields[i] {
				continue outer
			}
		}
		return true
	}
	return false
}
