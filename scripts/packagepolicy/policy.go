// Package packagepolicy is the single authoritative parsed-import
// implementation of Loom's package-boundary ownership policy (arch-backend-09).
//
// Every boundary entrypoint runs this one implementation:
//
//   - scripts/check-boundaries (CLI, invoked by
//     scripts/check_dataframe_package_boundaries.sh, which Make and CI use)
//     scans the repository and runs the negative fixture controls;
//   - internal/dataframe/compiler/backend_leaf_guard_test.go
//     (TestCompilerProductionFilesNeverImportBackendsOrDrivers) delegates
//     its scan here;
//   - internal/dataframe/compiler/backend_ownership_guard_contract_test.go
//     (TestBackendOwnershipGuardContract) drives the same rules and scanner.
//
// Design invariants (the defect class this package exists to kill):
//
//   - Findings and errors live in separate channels. A missing directory,
//     a walk failure, or a parse failure is a VERIFICATION failure, never
//     an import finding. A control may only be satisfied by ImportFindings;
//     the retired scanner folded fs.WalkDir errors into its offender list,
//     so a control aimed at a deleted directory "passed" on FS errors
//     (arch-backend-09 fix: backend_leaf_guard_test.go scanning the dead
//     internal/dataframe/postgres path).
//   - Vacuity is refused: a nonexistent target root and a target that
//     yields zero in-scope Go files are both errors, never a silent pass.
//   - Production and test imports are checked separately (Scope), so
//     external test packages may use the public backend factory while
//     production renderer/domain code stays driver-, store-, and
//     runtime-free.
package packagepolicy

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go/ast"
	"go/parser"
	"go/token"
)

// Scope selects which files a rule inspects.
type Scope int

const (
	// Production skips *_test.go files.
	Production Scope = iota
	// TestsOnly inspects only *_test.go files.
	TestsOnly
	// AllScopes inspects every Go file.
	AllScopes
)

// Forbidden names imports a rule rejects. Prefix entries match the exact
// path or any path below it; Exact entries match only the identical path.
type Forbidden struct {
	Prefix []string
	Exact  []string
}

func (f Forbidden) matches(importPath string) bool {
	for _, prefix := range f.Prefix {
		// Table prefixes may or may not carry a trailing slash; both
		// must match the package itself and everything below it.
		p := strings.TrimSuffix(prefix, "/")
		if importPath == p || strings.HasPrefix(importPath, p+"/") {
			return true
		}
	}
	for _, exact := range f.Exact {
		if importPath == exact {
			return true
		}
	}
	return false
}

// Rule is one parsed-import boundary rule from the authoritative table.
type Rule struct {
	Label     string
	Owner     string // allowed-owner guidance emitted with every finding
	Targets   []string
	Recursive bool
	Scope     Scope
	Forbidden Forbidden
	// RejectNonStdlib reports every import whose first path segment
	// contains a dot (the stdlib has none): the command-leaf law.
	RejectNonStdlib bool
	// AllowFiles lists repo-root-relative files whose findings are
	// tolerated (registered interim allowlist entries only).
	AllowFiles []string
}

// ErrorKind classifies verification failures. They are structurally
// distinct from import findings.
type ErrorKind string

const (
	ErrMissing ErrorKind = "missing-target"
	ErrNotDir  ErrorKind = "target-not-a-directory"
	ErrWalk    ErrorKind = "walk-failure"
	ErrParse   ErrorKind = "parse-failure"
	ErrVacuous ErrorKind = "vacuous-scan"
)

// ScanError is a verification failure: the scan could not establish
// anything about the target, so no control may be satisfied by it.
type ScanError struct {
	Kind   ErrorKind
	Path   string
	Reason string
}

func (e ScanError) String() string {
	return fmt.Sprintf("boundary verification failure [%s] %s: %s", e.Kind, e.Path, e.Reason)
}

// Finding is one confirmed forbidden import in a parsed file.
type Finding struct {
	Rule   string
	File   string
	Import string
	Line   int
	Owner  string
}

func (f Finding) String() string {
	return fmt.Sprintf("boundary violation: %s:%d rule %q imports %q; allowed owner: %s",
		f.File, f.Line, f.Rule, f.Import, f.Owner)
}

// Result separates confirmed findings from verification failures.
type Result struct {
	Findings []Finding
	Errors   []ScanError
	Scanned  int
}

// ImportFindings returns only confirmed import findings; verification
// errors are excluded by construction, so a missing directory can never
// count as import detection.
func (r Result) ImportFindings() []Finding {
	out := make([]Finding, 0, len(r.Findings))
	out = append(out, r.Findings...)
	return out
}

func (r Result) HasImportFindings() bool { return len(r.Findings) > 0 }

// Clean reports a scan that fully established its negative: no forbidden
// imports and no verification failures.
func (r Result) Clean() bool { return len(r.Findings) == 0 && len(r.Errors) == 0 }

func (r *Result) addFinding(f Finding) { r.Findings = append(r.Findings, f) }
func (r *Result) addError(e ScanError) { r.Errors = append(r.Errors, e) }

// ScanTarget walks one directory (recursively when recursive) or one Go
// file and reports forbidden imports and verification failures.
func ScanTarget(target, label, owner string, forbidden Forbidden, rejectNonStdlib bool, scope Scope, recursive bool) Result {
	var res Result
	info, err := os.Stat(target)
	if err != nil {
		res.addError(ScanError{Kind: ErrMissing, Path: target, Reason: err.Error()})
		return res
	}
	if !info.IsDir() {
		if !strings.HasSuffix(target, ".go") {
			res.addError(ScanError{Kind: ErrNotDir, Path: target, Reason: "target is neither a directory nor a .go file"})
			return res
		}
		if inScope(target, scope) {
			scanFile(&res, target, label, owner, forbidden, rejectNonStdlib)
		}
		finalize(&res)
		return res
	}
	visit := func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			res.addError(ScanError{Kind: ErrWalk, Path: path, Reason: err.Error()})
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || !inScope(entry.Name(), scope) {
			return nil
		}
		scanFile(&res, path, label, owner, forbidden, rejectNonStdlib)
		return nil
	}
	if recursive {
		if err := filepath.WalkDir(target, visit); err != nil {
			res.addError(ScanError{Kind: ErrWalk, Path: target, Reason: err.Error()})
		}
	} else {
		entries, err := os.ReadDir(target)
		if err != nil {
			res.addError(ScanError{Kind: ErrWalk, Path: target, Reason: err.Error()})
			finalize(&res)
			return res
		}
		for _, entry := range entries {
			_ = visit(filepath.Join(target, entry.Name()), entry, nil)
		}
	}
	if res.Scanned == 0 && len(res.Errors) == 0 {
		res.addError(ScanError{Kind: ErrVacuous, Path: target,
			Reason: "no Go files in scope; a boundary scan over an empty target proves nothing"})
	}
	finalize(&res)
	return res
}

func inScope(name string, scope Scope) bool {
	isTest := strings.HasSuffix(name, "_test.go")
	switch scope {
	case Production:
		return strings.HasSuffix(name, ".go") && !isTest
	case TestsOnly:
		return isTest
	default:
		return strings.HasSuffix(name, ".go")
	}
}

func scanFile(res *Result, path, label, owner string, forbidden Forbidden, rejectNonStdlib bool) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	if err != nil {
		res.addError(ScanError{Kind: ErrParse, Path: path, Reason: err.Error()})
		return
	}
	res.Scanned++
	for _, imported := range file.Imports {
		importPath := strings.Trim(imported.Path.Value, `"`)
		line := fset.Position(imported.Pos()).Line
		bad := forbidden.matches(importPath)
		if !bad && rejectNonStdlib && nonStdlib(importPath) {
			bad = true
		}
		if bad {
			res.addFinding(Finding{Rule: label, File: path, Import: importPath, Line: line, Owner: owner})
		}
	}
}

func nonStdlib(importPath string) bool {
	if importPath == "" {
		return true
	}
	first := importPath
	if i := strings.Index(importPath, "/"); i >= 0 {
		first = importPath[:i]
	}
	return strings.Contains(first, ".")
}

func finalize(res *Result) {
	sort.Slice(res.Findings, func(i, j int) bool {
		if res.Findings[i].File != res.Findings[j].File {
			return res.Findings[i].File < res.Findings[j].File
		}
		return res.Findings[i].Import < res.Findings[j].Import
	})
	sort.Slice(res.Errors, func(i, j int) bool {
		if res.Errors[i].Path != res.Errors[j].Path {
			return res.Errors[i].Path < res.Errors[j].Path
		}
		return res.Errors[i].Kind < res.Errors[j].Kind
	})
}

// ScanRule runs one table rule against repoRoot by joining each target.
func ScanRule(repoRoot string, rule Rule) Result {
	var res Result
	for _, target := range rule.Targets {
		sub := ScanTarget(filepath.Join(repoRoot, target), rule.Label, rule.Owner,
			rule.Forbidden, rule.RejectNonStdlib, rule.Scope, rule.Recursive)
		for _, f := range sub.Findings {
			if !allowedFile(rule.AllowFiles, repoRoot, f.File) {
				res.addFinding(f)
			}
		}
		res.Errors = append(res.Errors, sub.Errors...)
		res.Scanned += sub.Scanned
	}
	finalize(&res)
	return res
}

func allowedFile(allow []string, repoRoot, file string) bool {
	rel, err := filepath.Rel(repoRoot, file)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	for _, a := range allow {
		if a == rel {
			return true
		}
	}
	return false
}

// ScanAll runs the authoritative table against repoRoot.
func ScanAll(repoRoot string) Result {
	var res Result
	for _, rule := range RepoRules() {
		sub := ScanRule(repoRoot, rule)
		res.Findings = append(res.Findings, sub.Findings...)
		res.Errors = append(res.Errors, sub.Errors...)
		res.Scanned += sub.Scanned
	}
	finalize(&res)
	return res
}

// AbsenceRule asserts that obsolete paths stay absent (removed by the
// factory tickets; their return is a remnant regression). Identifiers
// asserts that removed API names stay absent from the parsed AST of every
// Go file under ScanRoots: a deleted bridge or forwarding symbol that
// reappears anywhere — including in tests — fails the table. Parsed AST
// matching means comments and string literals can never trip the check,
// and a parse failure is a verification error, never a detection.
type AbsenceRule struct {
	Label       string
	Owner       string
	Paths       []string
	Identifiers []string
	ScanRoots   []string
}

// NoReExportRule asserts that a facade package (Target) does not
// re-export the public surface of an owning package (Owner): no top-level
// `Name = owner.Name` alias and no `func Name(...) owner.T` forwarding
// declaration for any symbol the owning package exports. This is the
// machine check behind "compat forwarding APIs absent" — the aliases.go
// defect class: a compatibility shim re-publishing another package's
// vocabulary from the old package path.
type NoReExportRule struct {
	Label    string
	Owner    string // allowed-owner guidance emitted with every failure
	Target   string // repo-root-relative facade directory (its own package)
	OwnerPkg string // repo-root-relative owning package directory
	// OwnerImport is the import path Target uses for OwnerPkg when
	// matching qualified forwarding declarations.
	OwnerImport string
	// AllowedEmbeds names types where a same-named struct in Target may
	// embed the owner's same-named type: the sanctioned composition
	// shape (root Runtime embeds the control plane and adds the data
	// ports). Every entry is an explicit, auditable ownership decision,
	// not a blanket exemption; any OTHER same-named embedding still
	// fails as method-set re-publication.
	AllowedEmbeds []string
}

// CheckNoReExport parses both packages and reports facade declarations
// that re-publish owning-package exports. A facade importing the owner is
// legal (composition); only re-export declarations fail. Zero parsed
// files in either package is a vacuity failure.
func CheckNoReExport(repoRoot string, rule NoReExportRule) []ScanError {
	ownerNames, errs := exportedNames(filepath.Join(repoRoot, rule.OwnerPkg), rule.OwnerPkg)
	if len(errs) > 0 {
		return errs
	}
	if len(ownerNames) == 0 {
		return []ScanError{{Kind: ErrVacuous, Path: rule.OwnerPkg,
			Reason: fmt.Sprintf("no-re-export check (%s): owner package exports no names — table defect", rule.Label)}}
	}
	targetFiles, errs := goFiles(filepath.Join(repoRoot, rule.Target), rule.Target)
	if len(errs) > 0 {
		return errs
	}
	var out []ScanError
	for _, path := range targetFiles {
		rel, rerr := filepath.Rel(repoRoot, path)
		if rerr != nil {
			rel = path
		}
		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			out = append(out, ScanError{Kind: ErrParse, Path: rel, Reason: perr.Error()})
			continue
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				if d.Tok != token.TYPE && d.Tok != token.VAR && d.Tok != token.CONST {
					continue
				}
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.ValueSpec:
						for i, name := range s.Names {
							if !name.IsExported() || i >= len(s.Values) {
								continue
							}
							if sel, ok := s.Values[i].(*ast.SelectorExpr); ok &&
								qualifiedOwner(sel, rule.OwnerImport) && ownerNames[sel.Sel.Name] {
								out = append(out, ScanError{Kind: ErrVacuous, Path: rel,
									Reason: fmt.Sprintf("facade re-exports owner symbol %s (%s); allowed owner: %s",
										name.Name, rule.Label, rule.Owner)})
							}
						}
					case *ast.TypeSpec:
						if !s.Name.IsExported() {
							continue
						}
						// type Name = owner.Name (alias form of the shim).
						if s.Assign == token.NoPos {
							if _, ok := s.Type.(*ast.StructType); !ok {
								continue
							}
						}
						if s.Assign != token.NoPos {
							if sel, ok := s.Type.(*ast.SelectorExpr); ok &&
								qualifiedOwner(sel, rule.OwnerImport) && ownerNames[sel.Sel.Name] {
								out = append(out, ScanError{Kind: ErrVacuous, Path: rel,
									Reason: fmt.Sprintf("facade re-exports owner type %s (%s); allowed owner: %s",
										s.Name.Name, rule.Label, rule.Owner)})
							}
							continue
						}
						// type Name struct { *owner.T } — embedding
						// promotes the owner's whole method set onto the
						// facade: the same re-publish, spelled as
						// composition. Only same-named embeddings fail,
						// and only unless the name is an explicit
						// AllowedEmbeds ownership decision.
						if st, ok := s.Type.(*ast.StructType); ok {
							allowed := false
							for _, a := range rule.AllowedEmbeds {
								if a == s.Name.Name {
									allowed = true
									break
								}
							}
							if !allowed {
								for _, fld := range st.Fields.List {
									if len(fld.Names) > 0 {
										continue
									}
									if sel, ok := embeddedSelector(fld.Type); ok &&
										qualifiedOwner(sel, rule.OwnerImport) && ownerNames[sel.Sel.Name] &&
										sel.Sel.Name == s.Name.Name {
										out = append(out, ScanError{Kind: ErrVacuous, Path: rel,
											Reason: fmt.Sprintf("facade %s embeds owner type %s, promoting its method set (%s); allowed owner: %s",
												s.Name.Name, sel.Sel.Name, rule.Label, rule.Owner)})
									}
								}
							}
						}
					}
				}
			case *ast.FuncDecl:
				if d.Recv != nil || !d.Name.IsExported() || d.Body == nil {
					continue
				}
				// Forwarding wrapper: exported func whose body only
				// returns a single owner symbol call (or forwards args).
				if len(d.Body.List) == 1 {
					ret, ok := d.Body.List[0].(*ast.ReturnStmt)
					if !ok || len(ret.Results) != 1 {
						continue
					}
					call, ok := ret.Results[0].(*ast.CallExpr)
					if !ok {
						continue
					}
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok &&
						qualifiedOwner(sel, rule.OwnerImport) && ownerNames[sel.Sel.Name] {
						out = append(out, ScanError{Kind: ErrVacuous, Path: rel,
							Reason: fmt.Sprintf("facade forwards to owner symbol %s (%s); allowed owner: %s",
								sel.Sel.Name, rule.Label, rule.Owner)})
					}
				}
			}
		}
	}
	return out
}

func qualifiedOwner(sel *ast.SelectorExpr, ownerImport string) bool {
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == importLeafName(ownerImport)
}

// embeddedSelector unwraps an embedded field type (*owner.T or owner.T)
// to its qualified selector.
func embeddedSelector(expr ast.Expr) (*ast.SelectorExpr, bool) {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	sel, ok := expr.(*ast.SelectorExpr)
	return sel, ok
}

// importLeafName derives the default identifier for an import path
// (last segment; hyphens to underscores is out of scope — none used).
func importLeafName(importPath string) string {
	if i := strings.LastIndexByte(importPath, '/'); i >= 0 {
		return importPath[i+1:]
	}
	return importPath
}

// exportedNames parses every Go file in dir and returns its exported
// top-level declaration names (types, vars, consts, funcs, methods).
func exportedNames(dir, label string) (map[string]bool, []ScanError) {
	files, errs := goFiles(dir, label)
	if len(errs) > 0 {
		return nil, errs
	}
	names := map[string]bool{}
	for _, path := range files {
		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			return nil, []ScanError{{Kind: ErrParse, Path: path, Reason: perr.Error()}}
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.ValueSpec:
						for _, n := range s.Names {
							if n.IsExported() {
								names[n.Name] = true
							}
						}
					case *ast.TypeSpec:
						if s.Name.IsExported() {
							names[s.Name.Name] = true
						}
					}
				}
			case *ast.FuncDecl:
				if d.Name.IsExported() {
					names[d.Name.Name] = true
				}
			}
		}
	}
	if len(names) == 0 {
		return nil, []ScanError{{Kind: ErrVacuous, Path: label,
			Reason: fmt.Sprintf("no-re-export check parsed zero exported names in %s: vacuous", label)}}
	}
	return names, nil
}

// goFiles returns the non-test .go files directly under dir (one
// package). Missing/empty trees are verification failures.
func goFiles(dir, label string) ([]string, []ScanError) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, []ScanError{{Kind: ErrMissing, Path: label, Reason: err.Error()}}
	}
	if !info.IsDir() {
		return nil, []ScanError{{Kind: ErrNotDir, Path: label, Reason: "not a directory"}}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, []ScanError{{Kind: ErrWalk, Path: label, Reason: err.Error()}}
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		files = append(files, filepath.Join(dir, e.Name()))
	}
	if len(files) == 0 {
		return nil, []ScanError{{Kind: ErrVacuous, Path: label, Reason: "no production Go files to parse: vacuous"}}
	}
	return files, nil
}

// RepoNoReExport is the machine-enforced facade law from the
// arch-backend-09 ruling (2026-11-12, Option A): the postgres factory
// root composes control and the private rows bridge but must never
// re-publish control's vocabulary. internal/backends/postgres/aliases.go
// was exactly this shim.
func RepoNoReExport() []NoReExportRule {
	return []NoReExportRule{
		{
			Label:       labelRootNoReExport,
			Owner:       "import internal/backends/postgres/control directly for control-plane symbols; the root package keeps only the factory (Open/Runtime) and the typed data-plane bridges",
			Target:      "internal/backends/postgres",
			OwnerPkg:    "internal/backends/postgres/control",
			OwnerImport: module + "internal/backends/postgres/control",
			// The one sanctioned composition: the factory Runtime embeds
			// the control runtime and adds SourceRows/PlanJSON (ruling
			// 2026-11-12, Option A: "root for Open/Runtime (embedding
			// promotes methods)"). Any other same-named embedding is a
			// re-publication.
			AllowedEmbeds: []string{"Runtime"},
		},
	}
}

// CheckAbsences fails when an obsolete path has been recreated or a
// removed identifier has reappeared.
func CheckAbsences(repoRoot string, rules []AbsenceRule) []ScanError {
	var errs []ScanError
	for _, rule := range rules {
		for _, path := range rule.Paths {
			full := filepath.Join(repoRoot, path)
			if _, err := os.Lstat(full); err == nil {
				errs = append(errs, ScanError{Kind: ErrVacuous, Path: path,
					Reason: fmt.Sprintf("obsolete path exists (%s); allowed owner: %s", rule.Label, rule.Owner)})
			} else if !os.IsNotExist(err) {
				errs = append(errs, ScanError{Kind: ErrWalk, Path: path, Reason: err.Error()})
			}
		}
		if len(rule.Identifiers) > 0 {
			errs = append(errs, checkIdentifierAbsences(repoRoot, rule)...)
		}
	}
	return errs
}

// checkIdentifierAbsences parses every Go file under the rule's scan roots
// and reports removed identifiers found in the AST. Zero scanned files is
// a vacuity failure, never a silent pass.
func checkIdentifierAbsences(repoRoot string, rule AbsenceRule) []ScanError {
	var errs []ScanError
	wanted := make(map[string]bool, len(rule.Identifiers))
	for _, id := range rule.Identifiers {
		wanted[id] = true
	}
	scanned := 0
	for _, root := range rule.ScanRoots {
		full := filepath.Join(repoRoot, root)
		if info, err := os.Stat(full); err != nil {
			errs = append(errs, ScanError{Kind: ErrMissing, Path: root, Reason: err.Error()})
			continue
		} else if !info.IsDir() {
			errs = append(errs, ScanError{Kind: ErrNotDir, Path: root, Reason: "identifier scan root is not a directory"})
			continue
		}
		walkErr := filepath.WalkDir(full, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				errs = append(errs, ScanError{Kind: ErrWalk, Path: path, Reason: err.Error()})
				return nil
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			fset := token.NewFileSet()
			file, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if perr != nil {
				errs = append(errs, ScanError{Kind: ErrParse, Path: path, Reason: perr.Error()})
				return nil
			}
			scanned++
			rel, rerr := filepath.Rel(repoRoot, path)
			if rerr != nil {
				rel = path
			}
			ast.Inspect(file, func(n ast.Node) bool {
				ident, ok := n.(*ast.Ident)
				if !ok || !wanted[ident.Name] {
					return true
				}
				errs = append(errs, ScanError{Kind: ErrVacuous, Path: rel,
					Reason: fmt.Sprintf("removed identifier %s reappeared (%s); allowed owner: %s",
						ident.Name, rule.Label, rule.Owner)})
				return true
			})
			return nil
		})
		if walkErr != nil {
			errs = append(errs, ScanError{Kind: ErrWalk, Path: root, Reason: walkErr.Error()})
		}
	}
	if scanned == 0 && len(errs) == 0 {
		errs = append(errs, ScanError{Kind: ErrVacuous, Path: strings.Join(rule.ScanRoots, ", "),
			Reason: fmt.Sprintf("identifier absence scan (%s) inspected zero Go files", rule.Label)})
	}
	return errs
}

// ByLabel looks up an authoritative rule so every entrypoint runs the same
// table row instead of restating its rule table.
func ByLabel(label string) (Rule, bool) {
	for _, rule := range RepoRules() {
		if rule.Label == label {
			return rule, true
		}
	}
	return Rule{}, false
}

// FindRepoRoot walks up from start until it finds go.mod.
func FindRepoRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod above %s", start)
		}
		dir = parent
	}
}
