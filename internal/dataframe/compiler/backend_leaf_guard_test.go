package compiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calypr/loom/scripts/packagepolicy"
)

func compilerProductionRule(t *testing.T) packagepolicy.Rule {
	t.Helper()
	rule, ok := packagepolicy.ByLabel("compiler-production: no backend implementations or database drivers")
	if !ok {
		t.Fatal("authoritative compiler-production rule is missing from the packagepolicy table")
	}
	return rule
}

func TestCompilerProductionFilesNeverImportBackendsOrDrivers(t *testing.T) {
	rule := compilerProductionRule(t)

	if res := packagepolicy.ScanTarget(".", rule.Label, rule.Owner, rule.Forbidden, rule.RejectNonStdlib, rule.Scope, true); !res.Clean() {
		for _, f := range res.ImportFindings() {
			t.Error(f.String())
		}
		for _, e := range res.Errors {
			t.Error(e.String())
		}
		t.FailNow()
	}

	t.Run("non_vacuity_named_victims_bleed", func(t *testing.T) {
		dir := t.TempDir()
		victims := map[string]string{
			"victim_backends_import.go": "package compiler\n\nimport \"github.com/calypr/loom/internal/backends/postgres/internal/store\"\n\nvar _ = store.SeriesSource\n",
			"victim_pgx_import.go":      "package compiler\n\nimport \"github.com/jackc/pgx/v5/pgxpool\"\n\nvar _ = pgxpool.Config{}\n",
			"victim_arangodb_import.go": "package compiler\n\nimport \"github.com/arangodb/go-driver\"\n\nvar _ = go-driver.Database\n",
		}
		for name, body := range victims {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		res := packagepolicy.ScanTarget(dir, rule.Label, rule.Owner, rule.Forbidden, rule.RejectNonStdlib, rule.Scope, true)
		if len(res.Errors) != 0 {
			t.Fatalf("fixture scan must not raise verification failures: %v", res.Errors)
		}
		offenders := strings.Join(findingsToStrings(res.ImportFindings()), "\n")
		for name, body := range victims {
			importPath := importPathOf(t, body)
			if !strings.Contains(offenders, name) {
				t.Fatalf("guard did not name victim %s in:\n%s", name, offenders)
			}
			if !strings.Contains(offenders, importPath) {
				t.Fatalf("guard did not name the offending import %q for %s in:\n%s", importPath, name, offenders)
			}
			if !strings.Contains(offenders, "allowed owner") {
				t.Fatalf("guard output must name the allowed owner for %s in:\n%s", name, offenders)
			}
		}
	})

	t.Run("non_vacuity_fires_on_a_real_postgres_adapter", func(t *testing.T) {
		repoRoot := repoRootForGuardTest(t)
		target := filepath.Join(repoRoot, "internal", "backends", "postgres", "internal", "rows")
		res := packagepolicy.ScanTarget(target, rule.Label, rule.Owner, rule.Forbidden, rule.RejectNonStdlib, packagepolicy.AllScopes, true)
		if len(res.Errors) != 0 {
			t.Fatalf("real-adapter control must run cleanly before it can detect: %v", res.Errors)
		}
		if !res.HasImportFindings() {
			t.Fatal("scanner missed the known PostgreSQL driver imports in internal/backends/postgres/internal/rows")
		}
		if !anyFindingNames(res.ImportFindings(), "rows.go", "github.com/jackc/pgx") {
			t.Fatalf("real-adapter control must produce a concrete rows.go pgx import finding, got %v", res.ImportFindings())
		}
	})

	t.Run("non_vacuity_fires_on_a_real_arango_adapter", func(t *testing.T) {
		repoRoot := repoRootForGuardTest(t)
		target := filepath.Join(repoRoot, "internal", "backends", "arango", "internal", "store")
		res := packagepolicy.ScanTarget(target, rule.Label, rule.Owner, rule.Forbidden, rule.RejectNonStdlib, packagepolicy.AllScopes, true)
		if len(res.Errors) != 0 {
			t.Fatalf("real-adapter control must run cleanly before it can detect: %v", res.Errors)
		}
		if !res.HasImportFindings() {
			t.Fatal("scanner missed the known ArangoDB driver imports in internal/backends/arango/internal/store")
		}
	})

	t.Run("non_vacuity_malformed_input_is_verification_failure", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "broken.go"), []byte("!!! not go source >>> {{{"), 0o600); err != nil {
			t.Fatal(err)
		}
		res := packagepolicy.ScanTarget(dir, rule.Label, rule.Owner, rule.Forbidden, rule.RejectNonStdlib, rule.Scope, true)
		if res.HasImportFindings() {
			t.Fatalf("parse failure must never be reported as import detection: %v", res.ImportFindings())
		}
		if len(res.Errors) == 0 {
			t.Fatal("malformed input must fail verification")
		}
	})
}

func repoRootForGuardTest(t *testing.T) string {
	t.Helper()
	root, err := packagepolicy.FindRepoRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func findingsToStrings(findings []packagepolicy.Finding) []string {
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		out = append(out, f.String())
	}
	return out
}

func anyFindingNames(findings []packagepolicy.Finding, fileFragment, importFragment string) bool {
	for _, f := range findings {
		if strings.Contains(f.File, fileFragment) && strings.Contains(f.Import, importFragment) {
			return true
		}
	}
	return false
}

func importPathOf(t *testing.T, body string) string {
	t.Helper()
	start := strings.Index(body, `"`)
	if start < 0 {
		t.Fatalf("fixture body has no import path: %s", body)
	}
	rest := body[start+1:]
	end := strings.Index(rest, `"`)
	if end < 0 {
		t.Fatalf("fixture body has an unterminated import path: %s", body)
	}
	return rest[:end]
}
