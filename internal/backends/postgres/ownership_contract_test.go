package postgres_test

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

	"github.com/calypr/loom/internal/backends/postgres/control"
	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/server"
)

type driverRegisterEntry struct {
	prefix      string
	claimedLive bool
}

var productionDriverImportAllowlist = []driverRegisterEntry{
	{prefix: "internal/backends/", claimedLive: true},
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot locate the repository root")
	}
	dir := filepath.Dir(file)
	root := dir
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
	factoryOpenLiveAssembly(t)

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

		t.Run("named_exception_shrank_to_zero_in_the_move_commit", func(t *testing.T) {
			var touched []string
			walkProductionGo(t, filepath.Join(root, "internal", "ingest"), func(relPath, absPath string) {
				for _, importPath := range fileImports(t, absPath) {
					if strings.HasPrefix(importPath, "github.com/jackc/pgx") {
						touched = append(touched, relPath)
					}
				}
			})
			sort.Strings(touched)
			if len(touched) != 0 {
				t.Fatalf("ingest driver-touch set = %v, want [] (the named exception was retired in the move commit; driver classes arrive only through the factory)", touched)
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
		base := func() control.Config {
			return control.Config{
				DSN:            "postgres://loom:secret@localhost:5432/loom",
				MaxConns:       4,
				MinConns:       1,
				AcquireTimeout: 30 * time.Second,
				Setup:          control.SetupMigrate,
				Schemas:        control.SeriesSchemas{control.SeriesSource: "loom_source"},
			}
		}
		if err := base().Validate(); err != nil {
			t.Fatalf("baseline valid config rejected: %v", err)
		}
		cases := []struct {
			name     string
			defect   func(*control.Config)
			sentinel error
		}{
			{"blank_dsn", func(c *control.Config) { c.DSN = "  " }, control.ErrInvalidConfig},
			{"zero_max_conns", func(c *control.Config) { c.MaxConns = 0 }, control.ErrInvalidConfig},
			{"negative_min_conns", func(c *control.Config) { c.MinConns = -1 }, control.ErrInvalidConfig},
			{"min_above_max", func(c *control.Config) { c.MinConns = 8 }, control.ErrInvalidConfig},
			{"nonpositive_acquire_timeout", func(c *control.Config) { c.AcquireTimeout = 0 }, control.ErrInvalidConfig},
			{"foreign_application_name_prefix", func(c *control.Config) { c.ApplicationName = "somebody-elses-tag" }, control.ErrInvalidConfig},
			{"unsafe_application_name_characters", func(c *control.Config) { c.ApplicationName = "loom-pg-host=evil" }, control.ErrInvalidConfig},
			{"missing_setup_policy", func(c *control.Config) { c.Setup = "" }, control.ErrInvalidConfig},
			{"unknown_setup_policy", func(c *control.Config) { c.Setup = "yolo" }, control.ErrInvalidConfig},
			{"blank_assigned_schema", func(c *control.Config) { c.Schemas = control.SeriesSchemas{control.SeriesSource: " "} }, control.ErrSeriesAssignment},
			{"unknown_series", func(c *control.Config) { c.Schemas = control.SeriesSchemas{"not_a_series": "ok"} }, control.ErrSeriesAssignment},
			{"unsafe_schema_name", func(c *control.Config) {
				c.Schemas = control.SeriesSchemas{control.SeriesSource: "Bad Name; DROP"}
			}, control.ErrInvalidSchemaName},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				cfg := base()
				tc.defect(&cfg)
				err := cfg.Validate()
				if !errors.Is(err, tc.sentinel) {
					t.Fatalf("defect %s: err = %v, want errors.Is(_, %v)", tc.name, err, tc.sentinel)
				}
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
		for _, series := range control.AllSeries() {
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
		cfg := server.Config{}
		cfg.Server.Backend = "postgres"
		cfg.Server.PostgreSQL.Host = "192.0.2.1"
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
