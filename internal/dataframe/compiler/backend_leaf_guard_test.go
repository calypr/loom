package compiler

// Leaf law (arch-backend-06): the compiled command is the compiler's leaf
// output. The only vendor-adjacent edge in the dataframe pipeline is
// adapter -> compiler/command; the compiler subtree itself may never
// import a backend implementation package (internal/backends/...) or a
// database driver (github.com/jackc/pgx/..., github.com/arangodb/...).
//
// This guard is the Go-side twin of scripts/check_dataframe_package_boundaries.sh.
// Both entrypoints delegate to the single authoritative parsed-import
// implementation in scripts/packagepolicy (arch-backend-09): this test no
// longer carries its own scanner or rule set, so the shell check and the
// go-test gate cannot drift, and verification failures (missing
// directories, walk errors, parse errors) are structurally distinct from
// import findings — the retired local scanner folded filesystem errors
// into its offender list, which let the real-adapter control "pass" after
// internal/dataframe/postgres was deleted (arch-backend-09 fixed defect
// class; see TestBackendOwnershipGuardContract in
// backend_ownership_guard_contract_test.go for the full ownership
// contract).

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

	// Positive scan of the live subtree: clean findings AND no
	// verification failures; an FS/parse failure is a verification
	// failure, never a pass and never "detection".
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
		// Sabotage controls: a planted backends import and a planted
		// driver import must each be reported, and the report must
		// name the victim file AND the offending import. A guard that
		// silently passes sabotage is not a gate.
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
		// The same scanner must flag the REAL relocated PostgreSQL row
		// adapter (it legitimately imports jackc/pgx outside the
		// compiler subtree), and the control must be satisfied by a
		// concrete rows.go pgx import finding — not by filesystem
		// errors from a path that no longer exists (the defect this
		// ticket fixed: the retired control scanned the deleted
		// internal/dataframe/postgres directory).
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
		// The ArangoDB twin (arch-backend-07): the same scanner must
		// flag the real ArangoDB adapter tree, which legitimately
		// imports github.com/arangodb/go-driver outside the compiler
		// subtree. Same defect class: findings only, never walk
		// errors.
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
		// A malformed Go file fails verification; it never counts as
		// having caught an import violation (retained crafted/malformed
		// control from the arch-backend-09 amendment).
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
