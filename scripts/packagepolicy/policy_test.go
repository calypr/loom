package packagepolicy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestForbiddenMatcherHandlesTrailingSlashPrefixes(t *testing.T) {
	f := Forbidden{Prefix: []string{"a/b/", "c/d"}, Exact: []string{"e/f"}}
	for _, imp := range []string{"a/b", "a/b/c", "c/d", "c/d/e", "e/f"} {
		if !f.matches(imp) {
			t.Fatalf("must match %q", imp)
		}
	}
	for _, imp := range []string{"a/bx", "c/other", "e/f/g", "e/fx"} {
		if f.matches(imp) {
			t.Fatalf("must not match %q", imp)
		}
	}
}

func TestScanTargetCleanTreeIsNotVacuous(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ok.go"), "package ok\n\nimport \"strings\"\n\nvar _ = strings.TrimSpace\n")
	res := ScanTarget(dir, "test", "owner", backendImports, false, Production, true)
	if !res.Clean() {
		t.Fatalf("clean tree must converge: %+v %+v", res.Findings, res.Errors)
	}
	if res.Scanned != 1 {
		t.Fatalf("clean tree must report scanned files, got %d", res.Scanned)
	}
}

func TestScanTargetMissingRootIsErrorNotFinding(t *testing.T) {
	res := ScanTarget(filepath.Join(t.TempDir(), "nope"), "test", "owner", backendImports, false, AllScopes, true)
	if res.HasImportFindings() {
		t.Fatalf("a missing directory must never produce import findings: %+v", res.Findings)
	}
	if len(res.Errors) != 1 || res.Errors[0].Kind != ErrMissing {
		t.Fatalf("missing root must be exactly one %s error, got %+v", ErrMissing, res.Errors)
	}
}

func TestScanTargetEmptyTreeIsVacuousFailure(t *testing.T) {
	res := ScanTarget(t.TempDir(), "test", "owner", backendImports, false, AllScopes, true)
	if res.HasImportFindings() {
		t.Fatalf("empty tree must produce no findings: %+v", res.Findings)
	}
	if len(res.Errors) != 1 || res.Errors[0].Kind != ErrVacuous {
		t.Fatalf("empty tree must fail as %s, got %+v", ErrVacuous, res.Errors)
	}
}

func TestScanTargetMalformedFileIsParseFailureNotDetection(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "broken.go"), "this is not go source {{{ >>>")
	res := ScanTarget(dir, "test", "owner", backendImports, false, AllScopes, true)
	if res.HasImportFindings() {
		t.Fatalf("a parse failure must never count as import detection: %+v", res.Findings)
	}
	if len(res.Errors) != 1 || res.Errors[0].Kind != ErrParse {
		t.Fatalf("malformed file must fail as %s, got %+v", ErrParse, res.Errors)
	}
}

func TestScanTargetReportsForbiddenImports(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "leak.go"),
		"package leak\n\nimport \"github.com/calypr/loom/internal/backends/postgres/internal/store\"\n")
	writeFile(t, filepath.Join(dir, "driver.go"),
		"package leak\n\nimport \"github.com/jackc/pgx/v5/pgxpool\"\n")
	res := ScanTarget(dir, "test-rule", "the owning backend factory", backendImports, false, AllScopes, true)
	if len(res.Findings) != 2 || len(res.Errors) != 0 {
		t.Fatalf("want 2 findings 0 errors, got %+v %+v", res.Findings, res.Errors)
	}
	first := res.Findings[0].String()
	for _, want := range []string{"driver.go", "github.com/jackc/pgx", `rule "test-rule"`, "allowed owner: the owning backend factory"} {
		if !contains(first, want) {
			t.Fatalf("finding %q must name %q", first, want)
		}
	}
}

func TestScanTargetExactAndPrefixAndNonStdlib(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"),
		"package a\n\nimport (\n\t_ \"github.com/calypr/loom/internal/dataframe/compiler\"\n\t_ \"github.com/calypr/loom/internal/dataframe/compiler/ir\"\n\t_ \"strings\"\n)\n")
	f := Forbidden{Prefix: []string{"x/none"}, Exact: []string{"github.com/calypr/loom/internal/dataframe/compiler"}}
	res := ScanTarget(dir, "exact", "owner", f, false, AllScopes, true)
	if len(res.Findings) != 1 || res.Findings[0].Import != "github.com/calypr/loom/internal/dataframe/compiler" {
		t.Fatalf("exact match must hit only the root path: %+v", res.Findings)
	}
	res = ScanTarget(dir, "leaf", "owner", Forbidden{}, true, AllScopes, true)
	if len(res.Findings) != 2 {
		t.Fatalf("non-stdlib rejection must flag both dotted imports: %+v", res.Findings)
	}
}

func TestScopeSeparatesProductionFromTestImports(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "prod.go"),
		"package p\n\nimport \"strings\"\n\nvar _ = strings.TrimSpace\n")
	writeFile(t, filepath.Join(dir, "prod_test.go"),
		"package p\n\nimport \"github.com/calypr/loom/internal/backends/postgres\"\n")
	if res := ScanTarget(dir, "r", "o", backendImports, false, Production, true); !res.Clean() {
		t.Fatalf("production scope must ignore test imports: %+v", res.Findings)
	}
	res := ScanTarget(dir, "r", "o", backendImports, false, TestsOnly, true)
	if len(res.Findings) != 1 {
		t.Fatalf("test scope must see the test import: %+v", res.Findings)
	}
}

func TestNonRecursiveSkipsSubdirectories(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "root.go"),
		"package p\n\nimport \"github.com/calypr/loom/internal/backends/postgres\"\n")
	writeFile(t, filepath.Join(dir, "sub", "deep.go"),
		"package sub\n\nimport \"github.com/jackc/pgx/v5\"\n")
	res := ScanTarget(dir, "r", "o", backendImports, false, AllScopes, false)
	if len(res.Findings) != 1 || !contains(res.Findings[0].File, "root.go") {
		t.Fatalf("non-recursive scan must cover only root files: %+v", res.Findings)
	}
}

func TestScanRuleAppliesAllowFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "grain.go"),
		"package spec\n\nimport \"github.com/calypr/loom/internal/dataframe/errors\"\n")
	writeFile(t, filepath.Join(dir, "other.go"),
		"package spec\n\nimport \"github.com/calypr/loom/internal/dataframe/errors\"\n")
	rule := Rule{Label: "spec", Owner: "o", Targets: []string{"."}, Recursive: true, Scope: Production,
		Forbidden:  Forbidden{Prefix: []string{"github.com/calypr/loom/internal/dataframe/errors"}},
		AllowFiles: []string{"grain.go"}}
	res := ScanRule(dir, rule)
	if len(res.Findings) != 1 || !contains(res.Findings[0].File, "other.go") {
		t.Fatalf("allowlisted file must be tolerated, others must not: %+v", res.Findings)
	}
}

func TestCheckAbsences(t *testing.T) {
	dir := t.TempDir()
	if errs := CheckAbsences(dir, []AbsenceRule{{Label: "l", Owner: "o", Paths: []string{"gone"}}}); len(errs) != 0 {
		t.Fatalf("absent path must pass: %+v", errs)
	}
	if err := os.MkdirAll(filepath.Join(dir, "gone"), 0o755); err != nil {
		t.Fatal(err)
	}
	errs := CheckAbsences(dir, []AbsenceRule{{Label: "l", Owner: "the factory", Paths: []string{"gone"}}})
	if len(errs) != 1 || !contains(errs[0].Reason, "the factory") {
		t.Fatalf("recreated path must fail with owner guidance: %+v", errs)
	}
}

func TestRepoRulesTableIsSane(t *testing.T) {
	seen := map[string]bool{}
	for _, rule := range RepoRules() {
		if rule.Label == "" || rule.Owner == "" || len(rule.Targets) == 0 {
			t.Fatalf("rule must carry label, owner, and targets: %+v", rule)
		}
		if seen[rule.Label] {
			t.Fatalf("duplicate rule label %q", rule.Label)
		}
		seen[rule.Label] = true
		if _, ok := ByLabel(rule.Label); !ok {
			t.Fatalf("ByLabel must find %q", rule.Label)
		}
		if !rule.RejectNonStdlib && len(rule.Forbidden.Prefix)+len(rule.Forbidden.Exact) == 0 {
			t.Fatalf("rule %q forbids nothing", rule.Label)
		}
	}
	if _, ok := ByLabel("does-not-exist"); ok {
		t.Fatal("unknown label must not resolve")
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func TestIdentifierAbsenceCatchesReappearance(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "internal", "bridge.go"),
		"package internalx\n\nfunc QuerierAsAny() any { return nil }\n")
	rule := AbsenceRule{Label: "bridge", Owner: "typed Querier", Identifiers: []string{"QuerierAsAny"}, ScanRoots: []string{"internal"}}
	errs := CheckAbsences(root, []AbsenceRule{rule})
	if len(errs) != 1 || errs[0].Kind != ErrVacuous {
		t.Fatalf("planted identifier must produce one vacuity-class absence failure, got %v", errs)
	}
	if !strings.Contains(errs[0].Reason, "QuerierAsAny") || !strings.Contains(errs[0].Path, "bridge.go") {
		t.Fatalf("absence failure must name identifier and file: %s", errs[0].Reason)
	}
}

func TestIdentifierAbsenceIgnoresCommentsAndStrings(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "internal", "note.go"),
		"package internalx\n\n// QuerierAsAny was deleted; mentioned here for history.\nvar note = \"QuerierAsAny\"\n")
	rule := AbsenceRule{Label: "bridge", Owner: "typed Querier", Identifiers: []string{"QuerierAsAny"}, ScanRoots: []string{"internal"}}
	if errs := CheckAbsences(root, []AbsenceRule{rule}); len(errs) != 0 {
		t.Fatalf("comment/string mentions are not AST identifiers: %v", errs)
	}
}

func TestIdentifierAbsenceMalformedIsVerificationFailureNotDetection(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "internal", "broken.go"), "!!! not go source >>> {{{")
	rule := AbsenceRule{Label: "bridge", Owner: "typed Querier", Identifiers: []string{"QuerierAsAny"}, ScanRoots: []string{"internal"}}
	errs := CheckAbsences(root, []AbsenceRule{rule})
	if len(errs) != 1 || errs[0].Kind != ErrParse {
		t.Fatalf("malformed source must be a parse failure, got %v", errs)
	}
}

func TestIdentifierAbsenceEmptyRootsAreVacuous(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	rule := AbsenceRule{Label: "bridge", Owner: "typed Querier", Identifiers: []string{"QuerierAsAny"}, ScanRoots: []string{"internal"}}
	errs := CheckAbsences(root, []AbsenceRule{rule})
	if len(errs) != 1 || errs[0].Kind != ErrVacuous || !strings.Contains(errs[0].Reason, "zero Go files") {
		t.Fatalf("zero scanned files must fail as vacuity, got %v", errs)
	}
	if errs := CheckAbsences(root, []AbsenceRule{{Label: "bridge", Identifiers: []string{"X"}, ScanRoots: []string{"no-such"}}}); len(errs) == 0 || errs[0].Kind != ErrMissing {
		t.Fatalf("missing scan root must be a missing-target failure, got %v", errs)
	}
}

func TestOldConcretePathsTableIsPinned(t *testing.T) {
	table := OldConcreteImportPaths()
	if len(table) != 16 {
		t.Fatalf("retired table changed size (%d); retirements are added deliberately, never dropped silently: %v", len(table), table)
	}
	seen := map[string]bool{}
	for _, p := range table {
		if p == "" || strings.Contains(p, " ") || !strings.Contains(p, "/") {
			t.Fatalf("malformed retired path %q", p)
		}
		if seen[p] {
			t.Fatalf("duplicate retired path %q", p)
		}
		seen[p] = true
	}
	for _, want := range [][]string{{"internal", "store", "postgres"}, {"internal", "store", "arango"}, {"internal", "dataframe", "execution", "chartifact", "arango"}} {
		path := strings.Join(want, "/")
		if !seen[path] {
			t.Fatalf("retired table must contain %s", path)
		}
	}
}

func writeGo(t *testing.T, root, rel, body string) {
	t.Helper()
	writeFile(t, filepath.Join(root, rel), body)
}

func TestNoReExportCatchesShimShapes(t *testing.T) {
	root := t.TempDir()
	writeGo(t, root, "control/defs.go", "package control\n\nimport \"errors\"\n\ntype Config struct{ N int }\n\nvar ErrInvalid = errors.New(\"x\")\n\nfunc NewStore() *Store { return nil }\n\ntype Store struct{}\n")
	writeGo(t, root, "postgres/factory.go", "package postgres\n\nimport \"github.com/calypr/loom/internal/backends/postgres/control\"\n\nfunc Open(c control.Config) error { return nil }\n")
	rule := NoReExportRule{Label: "facade", Owner: "import control directly",
		Target: "postgres", OwnerPkg: "control", OwnerImport: "github.com/calypr/loom/internal/backends/postgres/control"}
	if errs := CheckNoReExport(root, rule); len(errs) != 0 {
		t.Fatalf("composition must be legal: %v", errs)
	}
	writeGo(t, root, "postgres/shim.go", "package postgres\n\nimport \"github.com/calypr/loom/internal/backends/postgres/control\"\n\n"+
		"type Config = control.Config\n\nvar ErrInvalid = control.ErrInvalid\n\nfunc NewStore() *control.Store { return control.NewStore() }\n\ntype Store struct{ *control.Store }\n")
	errs := CheckNoReExport(root, rule)
	want := map[string]bool{"Config": false, "ErrInvalid": false, "NewStore": false, "Store": false}
	for _, e := range errs {
		for name := range want {
			if strings.Contains(e.Reason, name) {
				want[name] = true
			}
		}
	}
	for name, seen := range want {
		if !seen {
			t.Fatalf("shim shape %s not caught; errors: %v", name, errs)
		}
	}
	rule.AllowedEmbeds = []string{"Store"}
	errs = CheckNoReExport(root, rule)
	for _, e := range errs {
		if strings.Contains(e.Reason, "embeds") {
			t.Fatalf("AllowedEmbeds must sanction Store embedding: %v", errs)
		}
	}
	if errs := CheckNoReExport(t.TempDir(), rule); len(errs) == 0 || errs[0].Kind != ErrMissing {
		t.Fatalf("missing owner package must fail as missing-target, got %v", errs)
	}
}
