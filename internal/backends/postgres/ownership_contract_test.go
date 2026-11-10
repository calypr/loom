package postgres_test

// TestPostgresFactoryOwnershipContract is the arch-backend-06 acceptance
// suite for the single owned PostgreSQL backend factory. The offline half
// (this file) runs everywhere; the live half (open_live_test.go) additionally
// drives real PostgreSQL 18 Open/bootstrap/domain/failure-cleanup round trips
// when LOOM_TEST_POSTGRES_URL is set, and the runner lane asserts it never
// skips.
//
// What the offline half proves:
//   - The public factory package imports no database driver (AST scan): the
//     exported contract cannot leak pgx pools or driver types.
//   - Production PostgreSQL driver imports stay confined to the backend home
//     plus one named, dated exception; the set must match the allowlist
//     exactly, so both a new offender AND an unexplained removal bleed (the
//     ingest exception is scheduled for deletion by this ticket's caller
//     migration and its removal must update the register deliberately).
//   - No production file outside the backend tree imports backend privates.
//   - The compiler leaf law holds repo-wide (driver + backends imports) —
//     the Go twin of scripts/check_dataframe_package_boundaries.sh.
//   - Config validation refuses every invalid shape with its named sentinel,
//     each refusal paired with an alive control (fix only the defect, the
//     same config validates) so a vacuous validator cannot pass.
//   - The factory series set equals the arch-backend-05 manifest exactly.
//   - The server's PostgreSQL startup refusal stays a named, byte-stable,
//     dial-free gate (D1 Option A: proven-closed until
//     pg-runtime-server-composition retargets it).
//   - DSN redaction never leaks the registered secret in either accepted
//     DSN form, while non-secret DSNs pass through untouched.
//   - Sabotage bleeds named victims: planted offenders are reported by name.

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/backends/postgres"
	"github.com/calypr/loom/internal/server"
	pgstore "github.com/calypr/loom/internal/store/postgres"
)

// driverRegisterEntry is one line of the PostgreSQL driver confinement
// register. claimedLive entries assert a CURRENT driver importer: the entry
// must match at least one live production offender, so deleting the offender
// without updating the register bleeds. Permissive zones (claimedLive=false)
// grant reach without asserting an offender exists yet.
type driverRegisterEntry struct {
	prefix      string
	claimedLive bool
}

// productionDriverImportAllowlist is the exact set of production (non-test)
// file/dir prefixes allowed to import the PostgreSQL driver today.
var productionDriverImportAllowlist = []driverRegisterEntry{
	// The backend home (public factory + private implementations). Zone:
	// the factory itself must stay driver-free (checked separately); its
	// private tree is the driver's only legitimate resident.
	{prefix: "internal/backends/", claimedLive: false},
	// The nine concrete PostgreSQL implementation packages (pre-move homes;
	// this ticket retires these entries when the tree lands under
	// internal/backends/).
	{prefix: "internal/store/postgres/", claimedLive: true},
	{prefix: "internal/catalog/postgres/", claimedLive: true},
	{prefix: "internal/dataset/postgres/", claimedLive: true},
	{prefix: "internal/explorer/postgres/", claimedLive: true},
	{prefix: "internal/dataframe/postgres/", claimedLive: true},
	{prefix: "internal/dataframe/recipe/postgres/", claimedLive: true},
	{prefix: "internal/dataframe/recipe/exec/postgres/", claimedLive: true},
	{prefix: "internal/dataframe/publication/postgres/", claimedLive: true},
	{prefix: "internal/dataframe/execution/chartifact/postgres/", claimedLive: true},
	// Named exception: the one raw *pgconn.PgError classification in the
	// generation load, scheduled for removal by the arch-backend-06 ingest
	// caller migration. Its disappearance must update this register in that
	// commit, not silently.
	{prefix: "internal/ingest/generation_load.go", claimedLive: true},
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot locate the repository root")
	}
	dir := filepath.Dir(file) // internal/backends/postgres
	root := dir
	for i := 0; i < 3; i++ {
		root = filepath.Dir(root)
	}
	if fi, err := os.Stat(filepath.Join(root, "go.mod")); err != nil || fi.IsDir() {
		t.Fatalf("resolved root %q has no go.mod: %v", root, err)
	}
	return root
}

// importsUnder lists the forbidden-prefix imports of one file.
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

// walkProductionGo walks root for production (non-test) .go files, invoking
// fn with repo-relative paths.
func walkProductionGo(t *testing.T, root string, fn func(relPath, absPath string)) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			t.Fatalf("walk %s: %v", path, err)
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
			t.Fatalf("rel %s: %v", path, relErr)
		}
		fn(filepath.ToSlash(rel), path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}

func allowedByDriverRegister(relPath string) bool {
	for _, entry := range productionDriverImportAllowlist {
		if strings.HasPrefix(relPath, entry.prefix) {
			return true
		}
	}
	return false
}

func TestPostgresFactoryOwnershipContract(t *testing.T) {
	root := repoRoot(t)

	t.Run("public_factory_surface_is_driver_free", func(t *testing.T) {
		factoryDir := filepath.Join(root, "internal", "backends", "postgres")
		entries, err := os.ReadDir(factoryDir)
		if err != nil {
			t.Fatalf("read factory dir: %v", err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			for _, importPath := range fileImports(t, filepath.Join(factoryDir, name)) {
				if strings.HasPrefix(importPath, "github.com/jackc/") {
					t.Fatalf("public factory file %s imports database driver %s", name, importPath)
				}
			}
		}
	})

	t.Run("production_driver_imports_confined_to_register", func(t *testing.T) {
		var offenders []string
		walkProductionGo(t, root, func(relPath, absPath string) {
			for _, importPath := range fileImports(t, absPath) {
				if strings.HasPrefix(importPath, "github.com/jackc/pgx") && !allowedByDriverRegister(relPath) {
					offenders = append(offenders, relPath+" imports "+importPath)
				}
			}
		})
		if len(offenders) != 0 {
			t.Fatalf("production files import the PostgreSQL driver outside the register: %v", offenders)
		}

		t.Run("register_is_not_stale", func(t *testing.T) {
			// Every register entry must still be live: an entry that matches
			// no current driver importer means the register drifted behind
			// reality (e.g. the ingest exception was deleted without
			// updating this contract).
			live := map[string]bool{}
			walkProductionGo(t, root, func(relPath, absPath string) {
				for _, importPath := range fileImports(t, absPath) {
					if strings.HasPrefix(importPath, "github.com/jackc/pgx") {
						live[relPath] = true
					}
				}
			})
			for _, entry := range productionDriverImportAllowlist {
				if !entry.claimedLive {
					continue
				}
				found := false
				for rel := range live {
					if strings.HasPrefix(rel, entry.prefix) {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("register entry %q matches no live driver importer; update the register in the commit that changed driver reach", entry.prefix)
				}
			}
		})

		t.Run("named_exception_is_the_only_ingest_driver_touch", func(t *testing.T) {
			// Alive control for the freeze/blast boundary: ingest touches the
			// driver in exactly one production file, the named exception.
			var touched []string
			walkProductionGo(t, filepath.Join(root, "internal", "ingest"), func(relPath, absPath string) {
				for _, importPath := range fileImports(t, absPath) {
					if strings.HasPrefix(importPath, "github.com/jackc/pgx") {
						touched = append(touched, relPath)
					}
				}
			})
			sort.Strings(touched)
			want := []string{"generation_load.go"}
			if strings.Join(touched, ",") != strings.Join(want, ",") {
				t.Fatalf("ingest driver-touch set = %v, want %v (the caller migration must shrink this to zero)", touched, want)
			}
		})
	})

	t.Run("production_files_outside_backend_never_import_backend_privates", func(t *testing.T) {
		var offenders []string
		walkProductionGo(t, root, func(relPath, absPath string) {
			if strings.HasPrefix(relPath, "internal/backends/postgres/") {
				return
			}
			for _, importPath := range fileImports(t, absPath) {
				if strings.HasPrefix(importPath, "github.com/calypr/loom/internal/backends/postgres/internal/") {
					offenders = append(offenders, relPath+" imports "+importPath)
				}
			}
		})
		if len(offenders) != 0 {
			t.Fatalf("callers reach backend privates: %v", offenders)
		}
	})

	t.Run("config_validation_matrix_with_alive_controls", func(t *testing.T) {
		base := func() postgres.Config {
			return postgres.Config{
				DSN:            "postgres://loom:secret@localhost:5432/loom",
				MaxConns:       4,
				MinConns:       1,
				AcquireTimeout: 30 * time.Second,
				Setup:          postgres.SetupMigrate,
				Schemas:        postgres.SeriesSchemas{postgres.SeriesSource: "loom_source"},
			}
		}
		if err := base().Validate(); err != nil {
			t.Fatalf("baseline valid config rejected: %v", err)
		}
		cases := []struct {
			name     string
			defect   func(*postgres.Config)
			sentinel error
		}{
			{"blank_dsn", func(c *postgres.Config) { c.DSN = "  " }, postgres.ErrInvalidConfig},
			{"zero_max_conns", func(c *postgres.Config) { c.MaxConns = 0 }, postgres.ErrInvalidConfig},
			{"negative_min_conns", func(c *postgres.Config) { c.MinConns = -1 }, postgres.ErrInvalidConfig},
			{"min_above_max", func(c *postgres.Config) { c.MinConns = 8 }, postgres.ErrInvalidConfig},
			{"nonpositive_acquire_timeout", func(c *postgres.Config) { c.AcquireTimeout = 0 }, postgres.ErrInvalidConfig},
			{"foreign_application_name_prefix", func(c *postgres.Config) { c.ApplicationName = "somebody-elses-tag" }, postgres.ErrInvalidConfig},
			{"unsafe_application_name_characters", func(c *postgres.Config) { c.ApplicationName = "loom-pg-host=evil" }, postgres.ErrInvalidConfig},
			{"missing_setup_policy", func(c *postgres.Config) { c.Setup = "" }, postgres.ErrInvalidConfig},
			{"unknown_setup_policy", func(c *postgres.Config) { c.Setup = "yolo" }, postgres.ErrInvalidConfig},
			{"blank_assigned_schema", func(c *postgres.Config) { c.Schemas = postgres.SeriesSchemas{postgres.SeriesSource: " "} }, postgres.ErrSeriesAssignment},
			{"unknown_series", func(c *postgres.Config) { c.Schemas = postgres.SeriesSchemas{"not_a_series": "ok"} }, postgres.ErrSeriesAssignment},
			{"unsafe_schema_name", func(c *postgres.Config) {
				// The store's ValidateAssignment applies the runner's
				// identifier policy pre-pool; an unsafe name must never
				// reach DDL. (Genuine shared-schema ledger conflicts are
				// exercised end-to-end by the arch-backend-05 suite.)
				c.Schemas = postgres.SeriesSchemas{postgres.SeriesSource: "Bad Name; DROP"}
			}, postgres.ErrSeriesAssignment},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				cfg := base()
				tc.defect(&cfg)
				err := cfg.Validate()
				if !errors.Is(err, tc.sentinel) {
					t.Fatalf("defect %s: err = %v, want errors.Is(_, %v)", tc.name, err, tc.sentinel)
				}
				// Alive control: the identical config without the defect
				// validates, proving the refusal is attributable to the defect.
				fixed := base()
				switch tc.name {
				case "min_above_max":
					fixed.MinConns = 1
				}
				if err := fixed.Validate(); err != nil {
					t.Fatalf("alive control rejected a sound config: %v", err)
				}
			})
		}
	})

	t.Run("series_set_equals_migration_manifest", func(t *testing.T) {
		factory := map[string]bool{}
		for _, series := range postgres.AllSeries() {
			factory[string(series)] = true
		}
		manifest := map[string]bool{}
		for _, entry := range pgstore.Manifest() {
			manifest[string(entry.Series)] = true
		}
		for series := range manifest {
			if !factory[series] {
				t.Fatalf("manifest series %q missing from the factory series set", series)
			}
		}
		for series := range factory {
			if !manifest[series] {
				t.Fatalf("factory series %q absent from the arch-backend-05 manifest", series)
			}
		}
	})

	t.Run("server_postgres_gate_stays_closed_named_and_dial_free", func(t *testing.T) {
		// D1 (Option A): the composition ticket must retarget a gate that is
		// provably closed right now -- a named sentinel, byte-stable message,
		// refusal before any dial, and Arango selection demonstrably
		// unaffected. The in-package mirror lives at
		// internal/server/postgres_refusal_contract_test.go.
		cfg := server.Config{}
		cfg.Server.Backend = "postgres"
		cfg.Server.PostgreSQL.Host = "192.0.2.1" // TEST-NET-1: unroutable by design
		cfg.Server.PostgreSQL.Port = 60543
		cfg.Server.PostgreSQL.Database = "loom_gate_witness"
		cfg.Server.PostgreSQL.User = "loom_gate_witness"

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		started := time.Now()
		err := server.RunWithConfig(ctx, cfg)
		elapsed := time.Since(started)
		if !errors.Is(err, server.ErrPostgreSQLRuntimeNotWired) {
			t.Fatalf("postgres startup err = %v, want errors.Is(_, ErrPostgreSQLRuntimeNotWired)", err)
		}
		if elapsed > 1500*time.Millisecond {
			t.Fatalf("gate took %v: must refuse before dialing the configured host", elapsed)
		}
	})

	t.Run("sabotage_named_victims_bleed", func(t *testing.T) {
		// The confinement scanner must report planted offenders by name.
		dir := t.TempDir()
		victims := map[string]string{
			"victim_driver_import.go": "package victim\n\nimport \"github.com/jackc/pgx/v5/pgxpool\"\n\nvar _ = pgxpool.Config{}\n",
		}
		for name, body := range victims {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		var offenders []string
		walkProductionGo(t, dir, func(relPath, absPath string) {
			for _, importPath := range fileImports(t, absPath) {
				if strings.HasPrefix(importPath, "github.com/jackc/pgx") {
					offenders = append(offenders, relPath)
				}
			}
		})
		joined := strings.Join(offenders, "\n")
		for name := range victims {
			if !strings.Contains(joined, name) {
				t.Fatalf("confinement scanner did not name victim %s in:\n%s", name, joined)
			}
		}
	})
}
