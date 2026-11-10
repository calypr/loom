package compiler

// Leaf law (arch-backend-06): the compiled command is the compiler's leaf
// output. The only vendor-adjacent edge in the dataframe pipeline is
// adapter -> compiler/command; the compiler subtree itself may never import
// a backend implementation package (internal/backends/...) or a database
// driver (github.com/jackc/pgx/...). This guard is the Go-side twin of the
// scripts/check_dataframe_package_boundaries.sh rule, so the leaf law is
// enforced by every go test run, not only by the shell check.

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go/parser"
	"go/token"
)

// forbiddenBackendImports are import-path prefixes that would invert the
// adapter -> compiler edge: a backend implementation package or a database
// driver reached directly from compiler code.
var forbiddenBackendImports = []string{
	"github.com/calypr/loom/internal/backends",
	"github.com/jackc/pgx",
}

// backendImportOffenders walks root recursively and reports every production
// (non-test) Go file that imports a forbidden prefix. A parse failure is
// itself an offense: an unparsable file in the compiler subtree cannot be
// trusted to respect the leaf law. Offender strings name the file so sabotage
// bleeds a named victim.
func backendImportOffenders(root string, includeTests bool) []string {
	var offenders []string
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			offenders = append(offenders, path+": "+err.Error())
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		if !includeTests && strings.HasSuffix(name, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			offenders = append(offenders, path+": "+parseErr.Error())
			return nil
		}
		for _, imported := range file.Imports {
			importPath := strings.Trim(imported.Path.Value, `"`)
			for _, forbidden := range forbiddenBackendImports {
				if importPath == forbidden || strings.HasPrefix(importPath, forbidden+"/") {
					offenders = append(offenders, path+" imports "+importPath)
				}
			}
		}
		return nil
	})
	if walkErr != nil {
		offenders = append(offenders, "walk "+root+": "+walkErr.Error())
	}
	return offenders
}

func TestCompilerProductionFilesNeverImportBackendsOrDrivers(t *testing.T) {
	if offenders := backendImportOffenders(".", false); len(offenders) != 0 {
		t.Fatalf("compiler production files import a backend implementation or a database driver: %v", offenders)
	}

	t.Run("non_vacuity_named_victims_bleed", func(t *testing.T) {
		// Sabotage controls: a planted backends import and a planted driver
		// import must each be reported, and the report must name the victim
		// file. A guard that silently passes sabotage is not a gate.
		dir := t.TempDir()
		victims := map[string]string{
			"victim_backends_import.go": "package compiler\n\nimport \"github.com/calypr/loom/internal/backends/postgres/internal/store\"\n\nvar _ = store.SeriesSource\n",
			"victim_pgx_import.go":      "package compiler\n\nimport \"github.com/jackc/pgx/v5/pgxpool\"\n\nvar _ = pgxpool.Config{}\n",
		}
		for name, body := range victims {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		offenders := strings.Join(backendImportOffenders(dir, false), "\n")
		for name := range victims {
			if !strings.Contains(offenders, name) {
				t.Fatalf("guard did not name victim %s in:\n%s", name, offenders)
			}
		}
	})

	t.Run("non_vacuity_fires_on_a_real_postgres_adapter", func(t *testing.T) {
		// The same scanner must flag the real PostgreSQL row adapter (it
		// legitimately imports jackc/pgx outside the compiler subtree).
		if offenders := backendImportOffenders(filepath.Join("..", "postgres"), false); len(offenders) == 0 {
			t.Fatal("scanner missed the known PostgreSQL driver imports in internal/backends/postgres/internal/rows")
		}
	})
}
