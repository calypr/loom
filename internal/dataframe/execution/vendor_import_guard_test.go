package execution

import (
	"context"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	arangofactory "github.com/calypr/loom/internal/backends/arango"
)

var forbiddenVendorImports = []string{
	"github.com/calypr/loom/internal/backends/arango/internal/store",
	"github.com/arangodb/go-driver",
	"github.com/jackc/pgx",
}

func vendorImportOffenders(dir string, includeTests bool) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{"read " + dir + ": " + err.Error()}
	}
	var offenders []string
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		if !includeTests && strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ImportsOnly)
		if parseErr != nil {
			offenders = append(offenders, name+": "+parseErr.Error())
			continue
		}
		for _, imported := range file.Imports {
			path := strings.Trim(imported.Path.Value, `"`)
			for _, forbidden := range forbiddenVendorImports {
				if path == forbidden || strings.HasPrefix(path, forbidden+"/") {
					offenders = append(offenders, name+" imports "+path)
				}
			}
		}
	}
	return offenders
}

func TestProductionExecutionFilesNeverImportDatabaseVendors(t *testing.T) {
	if offenders := vendorImportOffenders(".", false); len(offenders) != 0 {
		t.Fatalf("execution production files import database vendors: %v", offenders)
	}

	t.Run("non_vacuity_scanner_fires_on_a_crafted_import", func(t *testing.T) {
		dir := t.TempDir()
		crafted := "package execution\n\nimport \"github.com/calypr/loom/internal/backends/arango/internal/store\"\n\nvar _ = arango.Client{}\n"
		if err := os.WriteFile(filepath.Join(dir, "arango_import_witness.go"), []byte(crafted), 0o600); err != nil {
			t.Fatal(err)
		}
		if offenders := vendorImportOffenders(dir, false); len(offenders) == 0 {
			t.Fatal("guard did not fire on a re-added ArangoDB import; it would not die when vendors return")
		}
	})

	t.Run("non_vacuity_scanner_fires_on_a_real_vendor_package", func(t *testing.T) {
		if offenders := vendorImportOffenders(filepath.Join("..", "..", "backends", "arango", "internal", "store"), false); len(offenders) == 0 {
			t.Fatal("scanner missed the known vendor imports in the ArangoDB adapter")
		}
	})
}

func TestQueryMemoryBudgetIsExecutionOwnedNeutralValue(t *testing.T) {
	if _, ok := QueryMemoryBudgetFromContext(context.Background()); ok {
		t.Fatal("unset context reported a budget")
	}
	ctx := WithQueryMemoryBudget(context.Background(), 4096)
	if budget, ok := QueryMemoryBudgetFromContext(ctx); !ok || budget != 4096 {
		t.Fatalf("budget = %d/%t, want 4096/true", budget, ok)
	}
	if _, ok := arangofactory.QueryMemoryLimitFromContext(ctx); ok {
		t.Fatal("execution attached the ArangoDB memory-limit mechanism itself")
	}
}
