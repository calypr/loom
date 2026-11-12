// Command check-boundaries runs the authoritative parsed-import boundary
// policy (scripts/packagepolicy) over the repository and, with
// --self-test, over negative fixtures that prove the guards can actually
// go RED (arch-backend-09 acceptance: "negative fixture checks fail for a
// real forbidden import and pass after its removal; a vacuous nonexistent
// path/empty scan fails").
//
// Invoked by scripts/check_dataframe_package_boundaries.sh (Make target
// dataframe-boundaries and the CI go-unit job). Do not add a second rule
// table anywhere else; add rows to scripts/packagepolicy/rules.go.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/calypr/loom/scripts/packagepolicy"
)

func main() {
	selfTest := flag.Bool("self-test", false, "run negative fixture controls instead of the repository scan")
	root := flag.String("root", ".", "repository root")
	flag.Parse()

	if *selfTest {
		if err := runSelfTest(); err != nil {
			fmt.Fprintf(os.Stderr, "boundary self-test FAILED: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("boundary self-test: ok")
		return
	}

	res := packagepolicy.ScanAll(*root)
	for _, e := range res.Errors {
		fmt.Fprintln(os.Stderr, e.String())
	}
	for _, f := range res.Findings {
		fmt.Fprintln(os.Stderr, f.String())
	}
	absences := packagepolicy.CheckAbsences(*root, packagepolicy.RepoAbsences())
	for _, e := range absences {
		fmt.Fprintln(os.Stderr, e.String())
	}
	if len(res.Findings) > 0 || len(res.Errors) > 0 || len(absences) > 0 {
		fmt.Fprintf(os.Stderr, "dataframe package boundary check failed: %d violation(s), %d verification failure(s), %d absence failure(s) over %d file(s)\n",
			len(res.Findings), len(res.Errors), len(absences), res.Scanned)
		os.Exit(1)
	}
	fmt.Printf("dataframe package boundaries: ok (%d rules, %d files scanned)\n",
		len(packagepolicy.RepoRules()), res.Scanned)
}

type control struct {
	name  string
	setup func(t *fixture)
	check func(t *fixture) error
}

type fixture struct {
	dir   string
	phase string
}

func runSelfTest() error {
	for _, c := range controls() {
		dir, err := os.MkdirTemp("", "arch09-selftest-*")
		if err != nil {
			return err
		}
		f := &fixture{dir: dir}
		err = runControl(c, f)
		rmErr := os.RemoveAll(dir)
		if err != nil {
			return fmt.Errorf("control %q: %w", c.name, err)
		}
		if rmErr != nil {
			return fmt.Errorf("control %q: cleanup: %w", c.name, rmErr)
		}
	}
	return nil
}

func runControl(c control, f *fixture) error {
	f.phase = "setup"
	c.setup(f)
	f.phase = "check"
	return c.check(f)
}

func (f *fixture) write(rel, body string) string {
	path := filepath.Join(f.dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		panic(fmt.Sprintf("fixture write %s: %v", path, err))
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		panic(fmt.Sprintf("fixture write %s: %v", path, err))
	}
	return path
}

func forbiddenImportRule() packagepolicy.Rule {
	rule, ok := packagepolicy.ByLabel("compiler-production: no backend implementations or database drivers")
	if !ok {
		panic("authoritative compiler-production rule missing from the table")
	}
	return rule
}

func neutralDomainRule() packagepolicy.Rule {
	rule, ok := packagepolicy.ByLabel("neutral-domains: no backend implementations or database drivers")
	if !ok {
		panic("authoritative neutral-domains rule missing from the table")
	}
	return rule
}

func rendererRules() (packagepolicy.Rule, packagepolicy.Rule) {
	aql, ok := packagepolicy.ByLabel("render-aql: no lowerer, optimizer, runtime, backends, or drivers")
	if !ok {
		panic("authoritative render-aql rule missing from the table")
	}
	pg, ok := packagepolicy.ByLabel("render-postgres: no runtime, backends, stores, or drivers")
	if !ok {
		panic("authoritative render-postgres rule missing from the table")
	}
	return aql, pg
}

func oldPathsRule() packagepolicy.Rule {
	rule, ok := packagepolicy.ByLabel("old-concrete-paths: relocated under internal/backends by arch-backend-06/07")
	if !ok {
		panic("authoritative old-concrete-paths rule missing from the table")
	}
	return rule
}

// querierBridgeAbsenceRule pulls the live table row for the removed
// untyped row-querier bridge so the self-test proves the same entry the
// repository scan runs, not a restated fixture copy.
func querierBridgeAbsenceRule() packagepolicy.AbsenceRule {
	for _, r := range packagepolicy.RepoAbsences() {
		if r.Label == "removed untyped row-querier bridge (arch-backend-09)" {
			return r
		}
	}
	panic("authoritative QuerierAsAny absence rule missing from the table")
}

func expectAbsenceError(errs []packagepolicy.ScanError, kind packagepolicy.ErrorKind, wantSub string) error {
	for _, e := range errs {
		if e.Kind == kind && strings.Contains(e.Reason, wantSub) {
			return nil
		}
	}
	return fmt.Errorf("expected a %s absence failure naming %q; got %v", kind, wantSub, errs)
}

func expectAbsencesClean(errs []packagepolicy.ScanError) error {
	if len(errs) != 0 {
		return fmt.Errorf("expected clean absences; got %v", errs)
	}
	return nil
}

func scanPath(rule packagepolicy.Rule, path string) packagepolicy.Result {
	return packagepolicy.ScanTarget(path, rule.Label, rule.Owner, rule.Forbidden, rule.RejectNonStdlib, rule.Scope, true)
}

func expectFinding(res packagepolicy.Result, ruleLabel, victimFile, victimImport string) error {
	if len(res.Errors) != 0 {
		return fmt.Errorf("verification failures must not stand in for detection: %v", res.Errors)
	}
	for _, f := range res.ImportFindings() {
		if strings.Contains(f.File, victimFile) && strings.Contains(f.Import, victimImport) && f.Rule == ruleLabel {
			if f.Owner == "" {
				return fmt.Errorf("finding %q must carry allowed-owner guidance", f.String())
			}
			if !strings.Contains(f.String(), "allowed owner") {
				return fmt.Errorf("finding rendering must name the allowed owner: %s", f.String())
			}
			return nil
		}
	}
	return fmt.Errorf("guard did not name victim %s importing %s under rule %q; findings: %v",
		victimFile, victimImport, ruleLabel, res.Findings)
}

func expectClean(res packagepolicy.Result) error {
	if !res.Clean() {
		return fmt.Errorf("expected a clean, fully-established scan; findings %v errors %v", res.Findings, res.Errors)
	}
	if res.Scanned == 0 {
		return fmt.Errorf("clean scan covered zero files: vacuous")
	}
	return nil
}

func expectOnlyErrors(res packagepolicy.Result, kind packagepolicy.ErrorKind) error {
	if res.HasImportFindings() {
		return fmt.Errorf("%s must never produce import findings: %v", kind, res.Findings)
	}
	for _, e := range res.Errors {
		if e.Kind == kind {
			return nil
		}
	}
	return fmt.Errorf("expected exactly a %s verification failure; got errors %v", kind, res.Errors)
}

func controls() []control {
	const (
		compilerImport = "github.com/calypr/loom/internal/backends/postgres/internal/store"
		pgxImport      = "github.com/jackc/pgx/v5/pgxpool"
		runtimeImport  = "github.com/calypr/loom/internal/dataframe/execution"
		factoryImport  = "github.com/calypr/loom/internal/backends/postgres"
		physicalRows   = "github.com/calypr/loom/internal/backends/postgres/internal/rows"
		oldStorePG     = "github.com/calypr/loom/internal/store/postgres"
		compilerRoot   = "github.com/calypr/loom/internal/dataframe/compiler"
	)
	goFile := func(pkg, imp string) string {
		if imp == "" {
			return "package " + pkg + "\n"
		}
		return "package " + pkg + "\n\nimport \"" + imp + "\"\n"
	}

	return []control{
		{"compiler-imports-backend-implementation", func(f *fixture) {
			f.write("compiler/victim_backends.go", goFile("compiler", compilerImport))
		}, func(f *fixture) error {
			return expectFinding(scanPath(forbiddenImportRule(), filepath.Join(f.dir, "compiler")),
				forbiddenImportRule().Label, "victim_backends.go", "internal/backends/postgres")
		}},
		{"driver-leak-hidden-in-postgres-renderer", func(f *fixture) {
			// The AQL twin stays clean: a scan of only render/aql
			// would pass while the driver leak sits in
			// render/postgres. Both dialect rules must run.
			f.write("render/aql/clean.go", goFile("aql", ""))
			f.write("render/postgres/victim_pgx.go", goFile("postgres", pgxImport))
		}, func(f *fixture) error {
			aql, pg := rendererRules()
			if err := expectOnlyCleanOrSkip(scanPath(aql, filepath.Join(f.dir, "render/aql"))); err != nil {
				return err
			}
			return expectFinding(scanPath(pg, filepath.Join(f.dir, "render/postgres")),
				pg.Label, "victim_pgx.go", "github.com/jackc/pgx")
		}},
		{"renderer-imports-runtime", func(f *fixture) {
			f.write("render/aql/victim_runtime.go", goFile("aql", runtimeImport))
			f.write("render/postgres/victim_runtime.go", goFile("postgres", runtimeImport))
			f.write("render/postgres/victim_store.go", goFile("postgres", "github.com/calypr/loom/internal/store/clickhouse"))
			f.write("render/postgres/victim_root.go", goFile("postgres", compilerRoot))
		}, func(f *fixture) error {
			aql, pg := rendererRules()
			if err := expectFinding(scanPath(aql, filepath.Join(f.dir, "render/aql")),
				aql.Label, "victim_runtime.go", "internal/dataframe/execution"); err != nil {
				return err
			}
			for _, victim := range []struct{ file, imp string }{
				{"victim_runtime.go", "internal/dataframe/execution"},
				{"victim_store.go", "internal/store/"},
				{"victim_root.go", "internal/dataframe/compiler"},
			} {
				if err := expectFinding(scanPath(pg, filepath.Join(f.dir, "render/postgres")),
					pg.Label, victim.file, victim.imp); err != nil {
					return err
				}
			}
			return nil
		}},
		{"domain-imports-backend-factory", func(f *fixture) {
			f.write("catalog/victim_factory.go", goFile("catalog", factoryImport))
			f.write("catalog/victim_physical_rows.go", goFile("catalog", physicalRows))
		}, func(f *fixture) error {
			rule := neutralDomainRule()
			res := scanPath(rule, filepath.Join(f.dir, "catalog"))
			if err := expectFinding(res, rule.Label, "victim_factory.go", "internal/backends/postgres"); err != nil {
				return err
			}
			return expectFinding(res, rule.Label, "victim_physical_rows.go", "internal/backends/postgres/internal/rows")
		}},
		{"old-concrete-import-path-revived", func(f *fixture) {
			f.write("tool/victim_oldpath.go", goFile("tool", oldStorePG))
		}, func(f *fixture) error {
			rule := oldPathsRule()
			return expectFinding(scanPath(rule, filepath.Join(f.dir, "tool")),
				rule.Label, "victim_oldpath.go", "internal/store/postgres")
		}},
		{"forbidden-import-removal-converges", func(f *fixture) {
			f.write("compiler/leak.go", goFile("compiler", compilerImport))
			f.phase = "remove"
			f.write("compiler/leak.go", goFile("compiler", ""))
		}, func(f *fixture) error {
			return expectClean(scanPath(forbiddenImportRule(), filepath.Join(f.dir, "compiler")))
		}},
		{"vacuous-nonexistent-path-fails", func(f *fixture) {}, func(f *fixture) error {
			return expectOnlyErrors(scanPath(forbiddenImportRule(), filepath.Join(f.dir, "no-such-directory")),
				packagepolicy.ErrMissing)
		}},
		{"vacuous-empty-scan-fails", func(f *fixture) {
			if err := os.MkdirAll(filepath.Join(f.dir, "empty"), 0o755); err != nil {
				panic(err)
			}
		}, func(f *fixture) error {
			return expectOnlyErrors(scanPath(forbiddenImportRule(), filepath.Join(f.dir, "empty")),
				packagepolicy.ErrVacuous)
		}},
		{"malformed-input-is-verification-failure", func(f *fixture) {
			f.write("compiler/broken.go", "!!! not go source >>> {{{")
		}, func(f *fixture) error {
			return expectOnlyErrors(scanPath(forbiddenImportRule(), filepath.Join(f.dir, "compiler")),
				packagepolicy.ErrParse)
		}},
		{"legitimate-adapter-and-factory-domain-imports-accepted", func(f *fixture) {
			f.write("adapter/rows_adapter.go", goFile("adapter", "github.com/calypr/loom/internal/catalog"))
			f.write("factory/factory.go", goFile("factory", "github.com/calypr/loom/internal/dataframe/recipe"))
			f.write("adapter/reads_execution_ports.go", goFile("adapter", runtimeImport))
		}, func(f *fixture) error {
			return expectClean(scanPath(neutralDomainRule(), f.dir))
		}},
		{"removed-bridge-identifier-reappearance-caught", func(f *fixture) {
			f.write("internal/bridge.go", "package internalx\n\n// QuerierAsAny in a comment must never trip the AST scan.\nfunc QuerierAsAny() any { return nil }\n")
			f.write("cmd/tool/tool.go", "package tool\n")
		}, func(f *fixture) error {
			return expectAbsenceError(packagepolicy.CheckAbsences(f.dir, []packagepolicy.AbsenceRule{querierBridgeAbsenceRule()}),
				packagepolicy.ErrVacuous, "QuerierAsAny")
		}},
		{"removed-bridge-identifier-absent-passes", func(f *fixture) {
			f.write("internal/bridge.go", "package internalx\n\n// mentions QuerierAsAny only in a comment and a string\nvar note = \"QuerierAsAny\"\n")
			f.write("cmd/tool/tool.go", "package tool\n")
		}, func(f *fixture) error {
			return expectAbsencesClean(packagepolicy.CheckAbsences(f.dir, []packagepolicy.AbsenceRule{querierBridgeAbsenceRule()}))
		}},
		{"removed-bridge-scan-malformed-is-verification-failure", func(f *fixture) {
			f.write("internal/broken.go", "!!! not go source >>> {{{")
			f.write("cmd/tool/tool.go", "package tool\n")
		}, func(f *fixture) error {
			return expectAbsenceError(packagepolicy.CheckAbsences(f.dir, []packagepolicy.AbsenceRule{querierBridgeAbsenceRule()}),
				packagepolicy.ErrParse, "broken.go")
		}},
		{"removed-bridge-scan-vacuous-roots-fail", func(f *fixture) {
			if err := os.MkdirAll(filepath.Join(f.dir, "internal"), 0o755); err != nil {
				panic(err)
			}
			if err := os.MkdirAll(filepath.Join(f.dir, "cmd"), 0o755); err != nil {
				panic(err)
			}
		}, func(f *fixture) error {
			return expectAbsenceError(packagepolicy.CheckAbsences(f.dir, []packagepolicy.AbsenceRule{querierBridgeAbsenceRule()}),
				packagepolicy.ErrVacuous, "zero Go files")
		}},
	}
}

func expectOnlyCleanOrSkip(res packagepolicy.Result) error {
	// The clean AQL twin in the hidden-leak control proves the
	// single-directory AQL scan is blind; it must itself converge.
	return expectClean(res)
}
