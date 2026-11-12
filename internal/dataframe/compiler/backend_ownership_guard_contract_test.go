package compiler

// TestBackendOwnershipGuardContract is the executable form of the accepted
// backend ownership boundaries (ticket arch-backend-09, batch
// c4b53e23b9d190fe / backend-architecture-20261109, consolidated into this
// existing real package per the backend-simplification-20261110-clarify
// precedence ruling — no empty ./internal/backends root package exists or
// is created for it).
//
// One authoritative parsed-import implementation (scripts/packagepolicy)
// drives every case here, the shell entrypoint
// (scripts/check_dataframe_package_boundaries.sh -> check-boundaries CLI),
// and TestCompilerProductionFilesNeverImportBackendsOrDrivers. Required
// named subtests (event gate): compiler_backends_and_drivers,
// neutral_domain_backend_import, renderer_runtime_import,
// legitimate_adapter_domain_import, actual_relocated_adapter_import,
// missing_or_malformed_input_is_error.
//
// Sabotage fixtures are built only under t.TempDir(): no planted file ever
// touches the repository or the pushed commits.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calypr/loom/scripts/packagepolicy"
)

func guardRule(t *testing.T, label string) packagepolicy.Rule {
	t.Helper()
	rule, ok := packagepolicy.ByLabel(label)
	if !ok {
		t.Fatalf("authoritative rule %q is missing from the packagepolicy table", label)
	}
	return rule
}

func guardScan(t *testing.T, rule packagepolicy.Rule, target string, scope packagepolicy.Scope) packagepolicy.Result {
	t.Helper()
	return packagepolicy.ScanTarget(target, rule.Label, rule.Owner, rule.Forbidden, rule.RejectNonStdlib, scope, true)
}

func guardWrite(t *testing.T, dir, rel, imp string) string {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var body string
	if imp == "" {
		body = "package fixture\n"
	} else {
		body = "package fixture\n\nimport \"" + imp + "\"\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// mustOldPath returns the authoritative retired-path table entry built
// from segments (e.g. "store", "arango"). Segments keep this Go file free
// of contiguous retired-path citations, which the arango ownership text
// guard forbids even in comments; the returned value is still matched
// against packagepolicy.OldConcreteImportPaths(), so a fixture can only
// plant a path the guard actually knows.
func mustOldPath(t *testing.T, segments ...string) string {
	t.Helper()
	candidate := "internal/" + strings.Join(segments, "/")
	for _, p := range packagepolicy.OldConcreteImportPaths() {
		if p == candidate {
			return p
		}
	}
	t.Fatalf("planted old path %q is not in the authoritative retired table", candidate)
	return ""
}

func requireFinding(t *testing.T, res packagepolicy.Result, ruleLabel, victimFile, victimImport string) {
	t.Helper()
	if len(res.Errors) != 0 {
		t.Fatalf("verification failures must never stand in for import detection: %v", res.Errors)
	}
	for _, f := range res.ImportFindings() {
		if strings.Contains(f.File, victimFile) && strings.Contains(f.Import, victimImport) && f.Rule == ruleLabel {
			line := f.String()
			if !strings.Contains(line, victimFile) || !strings.Contains(line, victimImport) {
				t.Fatalf("finding must name the offending file and import: %s", line)
			}
			if !strings.Contains(line, "allowed owner") || f.Owner == "" {
				t.Fatalf("finding must carry allowed-owner guidance: %s", line)
			}
			return
		}
	}
	t.Fatalf("guard did not report %s importing %s under rule %q; findings: %v", victimFile, victimImport, ruleLabel, res.Findings)
}

func requireClean(t *testing.T, res packagepolicy.Result) {
	t.Helper()
	if !res.Clean() {
		t.Fatalf("expected a clean fully-established scan; findings %v errors %v", res.Findings, res.Errors)
	}
	if res.Scanned == 0 {
		t.Fatal("clean scan covered zero files: vacuous")
	}
}

func TestBackendOwnershipGuardContract(t *testing.T) {
	repoRoot := repoRootForGuardTest(t)

	// Named positive for the whole authoritative table: the repository
	// as it stands passes every boundary rule with no verification
	// failures and every obsolete path absent.
	tableScan := packagepolicy.ScanAll(repoRoot)
	for _, f := range tableScan.ImportFindings() {
		t.Error(f.String())
	}
	for _, e := range tableScan.Errors {
		t.Error(e.String())
	}
	for _, e := range packagepolicy.CheckAbsences(repoRoot, packagepolicy.RepoAbsences()) {
		t.Error(e.String())
	}
	if t.Failed() {
		t.Fatal("repository violates the authoritative package boundary table (see failures above)")
	}

	compilerRule := guardRule(t, "compiler-production: no backend implementations or database drivers")
	neutralRule := guardRule(t, "neutral-domains: no backend implementations or database drivers")
	aqlRule := guardRule(t, "render-aql: no lowerer, optimizer, runtime, backends, or drivers")
	postgresRenderRule := guardRule(t, "render-postgres: no runtime, backends, stores, or drivers")
	oldPathsRule := guardRule(t, "old-concrete-paths: relocated under internal/backends by arch-backend-06/07")

	t.Run("compiler_backends_and_drivers", func(t *testing.T) {
		// Positive: the live compiler subtree is vendor-free.
		requireClean(t, packagepolicy.ScanRule(repoRoot, compilerRule))

		// Negative: each historical leak shape is planted and must
		// bleed a named victim (file + offending import + owner).
		dir := t.TempDir()
		guardWrite(t, dir, "compiler/backends_leak.go", "github.com/calypr/loom/internal/backends/postgres/internal/store")
		guardWrite(t, dir, "compiler/pgx_leak.go", "github.com/jackc/pgx/v5/pgxpool")
		guardWrite(t, dir, "compiler/driver_leak.go", "github.com/arangodb/go-driver")
		res := guardScan(t, compilerRule, filepath.Join(dir, "compiler"), packagepolicy.Production)
		requireFinding(t, res, compilerRule.Label, "backends_leak.go", "internal/backends/postgres/internal/store")
		requireFinding(t, res, compilerRule.Label, "pgx_leak.go", "github.com/jackc/pgx")
		requireFinding(t, res, compilerRule.Label, "driver_leak.go", "github.com/arangodb")
	})

	t.Run("neutral_domain_backend_import", func(t *testing.T) {
		// Positive: the neutral domain trees are backend-free.
		requireClean(t, packagepolicy.ScanRule(repoRoot, neutralRule))

		// Negative fixtures for the actual historical leak shapes:
		// Planted retired-path imports are taken from the authoritative
		// table (scripts/packagepolicy/old_concrete_paths.txt) rather
		// than restated here: the arango ownership contract forbids any
		// Go file from citing a retired path, guard data included.
		dir := t.TempDir()
		// (a) execution importing Arango preview types — both the
		// revived old path and the new private location are refused.
		guardWrite(t, dir, "execution/preview_old.go", "github.com/calypr/loom/"+mustOldPath(t, "store", "arango"))
		guardWrite(t, dir, "execution/preview_private.go", "github.com/calypr/loom/internal/backends/arango/internal/explorer")
		// (b) generic catalog importing PG physical rows.
		guardWrite(t, dir, "catalog/rows_old.go", "github.com/calypr/loom/"+mustOldPath(t, "store", "postgres"))
		guardWrite(t, dir, "catalog/rows_private.go", "github.com/calypr/loom/internal/backends/postgres/internal/rows")
		// (c) domain importing the backend factory directly.
		guardWrite(t, dir, "dataset/factory_leak.go", "github.com/calypr/loom/internal/backends/postgres")

		res := guardScan(t, neutralRule, dir, packagepolicy.Production)
		requireFinding(t, res, neutralRule.Label, "preview_private.go", "internal/backends/arango/internal/explorer")
		requireFinding(t, res, neutralRule.Label, "rows_private.go", "internal/backends/postgres/internal/rows")
		requireFinding(t, res, neutralRule.Label, "factory_leak.go", "internal/backends/postgres")
		old := guardScan(t, oldPathsRule, dir, packagepolicy.AllScopes)
		requireFinding(t, old, oldPathsRule.Label, "preview_old.go", mustOldPath(t, "store", "arango"))
		requireFinding(t, old, oldPathsRule.Label, "rows_old.go", mustOldPath(t, "store", "postgres"))

		// Removal converges: after the forbidden imports are taken
		// out, the identical target scans clean.
		for _, rel := range []string{
			"execution/preview_old.go", "execution/preview_private.go",
			"catalog/rows_old.go", "catalog/rows_private.go",
			"dataset/factory_leak.go",
		} {
			guardWrite(t, dir, rel, "")
		}
		requireClean(t, guardScan(t, neutralRule, dir, packagepolicy.Production))
		requireClean(t, guardScan(t, oldPathsRule, dir, packagepolicy.AllScopes))
	})

	t.Run("renderer_runtime_import", func(t *testing.T) {
		// Positive: BOTH renderer dialect directories are checked
		// against the runtime/store/driver/backend bans — no leak may
		// hide by landing in only one dialect (the AQL-only blindness
		// this ticket closes).
		requireClean(t, packagepolicy.ScanRule(repoRoot, aqlRule))
		requireClean(t, packagepolicy.ScanRule(repoRoot, postgresRenderRule))

		dir := t.TempDir()
		// Runtime import in each dialect.
		guardWrite(t, dir, "render/aql/runtime_leak.go", "github.com/calypr/loom/internal/dataframe/execution")
		guardWrite(t, dir, "render/postgres/runtime_leak.go", "github.com/calypr/loom/internal/dataframe/execution")
		// Compiler-root import in each dialect (renderer-to-root ban).
		guardWrite(t, dir, "render/aql/root_leak.go", "github.com/calypr/loom/internal/dataframe/compiler")
		guardWrite(t, dir, "render/postgres/root_leak.go", "github.com/calypr/loom/internal/dataframe/compiler")
		// Driver leak placed ONLY in the PostgreSQL dialect: an
		// AQL-only scan would be blind, so the dialect-paired scan
		// is what must catch it.
		guardWrite(t, dir, "render/aql/clean.go", "")
		guardWrite(t, dir, "render/postgres/driver_leak.go", "github.com/jackc/pgx/v5/pgxpool")
		// Store leak in the PostgreSQL dialect.
		guardWrite(t, dir, "render/postgres/store_leak.go", "github.com/calypr/loom/internal/store/clickhouse")

		aqlRes := guardScan(t, aqlRule, filepath.Join(dir, "render/aql"), packagepolicy.Production)
		requireFinding(t, aqlRes, aqlRule.Label, "runtime_leak.go", "internal/dataframe/execution")
		requireFinding(t, aqlRes, aqlRule.Label, "root_leak.go", "internal/dataframe/compiler")

		pgRes := guardScan(t, postgresRenderRule, filepath.Join(dir, "render/postgres"), packagepolicy.Production)
		requireFinding(t, pgRes, postgresRenderRule.Label, "runtime_leak.go", "internal/dataframe/execution")
		requireFinding(t, pgRes, postgresRenderRule.Label, "root_leak.go", "internal/dataframe/compiler")
		requireFinding(t, pgRes, postgresRenderRule.Label, "driver_leak.go", "github.com/jackc/pgx")
		requireFinding(t, pgRes, postgresRenderRule.Label, "store_leak.go", "internal/store/clickhouse")
	})

	t.Run("legitimate_adapter_domain_import", func(t *testing.T) {
		// Accepted shapes: a private adapter consuming domain
		// contracts (catalog, recipe) and shared execution ports, and
		// a startup factory wired to domain ports, must not trip the
		// domain/backend-ownership rules. This is the same direction
		// the real relocated adapters use.
		dir := t.TempDir()
		guardWrite(t, dir, "adapter/rows_adapter.go", "github.com/calypr/loom/internal/catalog")
		guardWrite(t, dir, "adapter/recipe_reader.go", "github.com/calypr/loom/internal/dataframe/recipe")
		guardWrite(t, dir, "adapter/execution_ports.go", "github.com/calypr/loom/internal/dataframe/execution")
		guardWrite(t, dir, "factory/factory.go", "github.com/calypr/loom/internal/dataset")
		requireClean(t, guardScan(t, neutralRule, dir, packagepolicy.Production))

		// The accepted adapter shape must be non-vacuous: swapping
		// one file for a real backend import flips it RED.
		guardWrite(t, dir, "factory/factory.go", "github.com/calypr/loom/internal/backends/arango/internal/store")
		res := guardScan(t, neutralRule, dir, packagepolicy.Production)
		requireFinding(t, res, neutralRule.Label, "factory.go", "internal/backends/arango/internal/store")
	})

	t.Run("actual_relocated_adapter_import", func(t *testing.T) {
		// The historical real-adapter negative control: scan an
		// EXISTING relocated adapter tree and assert a concrete
		// rows.go pgx import finding. Missing directories, parse
		// failures and execution errors fail verification; they
		// cannot count as successfully catching an import violation
		// (the fixed defect class: the retired control scanned the
		// deleted internal/dataframe/postgres path and "passed" on
		// folded-in filesystem errors).
		res := guardScan(t, compilerRule, filepath.Join(repoRoot, "internal", "backends", "postgres", "internal", "rows"), packagepolicy.Production)
		if len(res.Errors) != 0 {
			t.Fatalf("real-adapter control must run with zero verification failures, got %v", res.Errors)
		}
		if res.Scanned == 0 {
			t.Fatal("real-adapter control scanned zero files: vacuous")
		}
		var named bool
		for _, f := range res.ImportFindings() {
			if strings.Contains(f.File, "rows.go") && strings.HasPrefix(f.Import, "github.com/jackc/pgx") {
				named = true
			}
		}
		if !named {
			t.Fatalf("expected a concrete rows.go pgx import finding, got %v", res.ImportFindings())
		}

		// Arango twin.
		arango := guardScan(t, compilerRule, filepath.Join(repoRoot, "internal", "backends", "arango", "internal", "store"), packagepolicy.Production)
		if len(arango.Errors) != 0 || !arango.HasImportFindings() {
			t.Fatalf("Arango real-adapter control failed: findings %v errors %v", arango.ImportFindings(), arango.Errors)
		}
		var driverNamed bool
		for _, f := range arango.ImportFindings() {
			if strings.HasPrefix(f.Import, "github.com/arangodb") {
				driverNamed = true
			}
		}
		if !driverNamed {
			t.Fatalf("expected a concrete go-driver import finding in the Arango adapter, got %v", arango.ImportFindings())
		}
	})

	t.Run("missing_or_malformed_input_is_error", func(t *testing.T) {
		// A nonexistent path can never satisfy an import-detection
		// control: zero import findings, a missing-target verification
		// failure.
		dir := t.TempDir()
		missing := guardScan(t, compilerRule, filepath.Join(dir, "does-not-exist"), packagepolicy.Production)
		if missing.HasImportFindings() {
			t.Fatalf("missing path produced import findings: %v", missing.ImportFindings())
		}
		if !hasErrorKind(missing.Errors, packagepolicy.ErrMissing) {
			t.Fatalf("missing path must fail as %s, got %v", packagepolicy.ErrMissing, missing.Errors)
		}

		// An empty tree fails as vacuity, never as convergence.
		empty := filepath.Join(dir, "empty")
		if err := os.MkdirAll(empty, 0o755); err != nil {
			t.Fatal(err)
		}
		vacuous := guardScan(t, compilerRule, empty, packagepolicy.Production)
		if vacuous.HasImportFindings() {
			t.Fatalf("empty scan produced import findings: %v", vacuous.ImportFindings())
		}
		if !hasErrorKind(vacuous.Errors, packagepolicy.ErrVacuous) {
			t.Fatalf("empty scan must fail as %s, got %v", packagepolicy.ErrVacuous, vacuous.Errors)
		}

		// Malformed Go input fails verification; it never counts as
		// having caught an import violation.
		badDir := filepath.Join(dir, "bad")
		guardWrite(t, badDir, "broken.go", "")
		if err := os.WriteFile(filepath.Join(badDir, "broken.go"), []byte("!!! not go source >>> {{{"), 0o600); err != nil {
			t.Fatal(err)
		}
		malformed := guardScan(t, compilerRule, badDir, packagepolicy.Production)
		if malformed.HasImportFindings() {
			t.Fatalf("malformed input produced import findings: %v", malformed.ImportFindings())
		}
		if !hasErrorKind(malformed.Errors, packagepolicy.ErrParse) {
			t.Fatalf("malformed input must fail as %s, got %v", packagepolicy.ErrParse, malformed.Errors)
		}
	})
}

func hasErrorKind(errors []packagepolicy.ScanError, kind packagepolicy.ErrorKind) bool {
	for _, e := range errors {
		if e.Kind == kind {
			return true
		}
	}
	return false
}
