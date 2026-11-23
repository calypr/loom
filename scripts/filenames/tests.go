package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type testFile struct {
	Path            string                  `json:"path"`
	Package         string                  `json:"package_clause"`
	Lines           int                     `json:"lines"`
	Tests           []string                `json:"test_funcs"`
	Helpers         []string                `json:"helpers"`
	HelperFunctions map[string]int          `json:"helper_functions"`
	Env             []string                `json:"env"`
	Refs            map[string]int          `json:"refs"`
	HelperRefs      map[string]int          `json:"helper_refs"`
	Functions       map[string]testFunction `json:"functions"`
	Error           string                  `json:"error,omitempty"`
}

type testFunction struct {
	Lines int            `json:"lines"`
	Refs  map[string]int `json:"refs"`
	Env   []string       `json:"env"`
}

func runTestInventory(root string) error {
	module := modulePath(root)
	packages, err := goList(root, "-export", "-deps", "-test", "-json", "-e", "./internal/...")
	if err != nil {
		return err
	}
	exports := exportMap(packages)
	imp := newExportImporter(token.NewFileSet(), exports)
	var variants []*pkgData
	production := map[string]string{}
	for _, p := range packages {
		if p.ForTest != "" || strings.HasSuffix(p.ImportPath, ".test") || !strings.HasPrefix(p.ImportPath, module+"/internal/") {
			continue
		}
		if len(p.TestGoFiles)+len(p.XTestGoFiles) > 0 {
			if len(p.TestGoFiles) > 0 {
				internal := p
				internal.GoFiles = append(append([]string{}, p.GoFiles...), p.TestGoFiles...)
				variants = append(variants, &pkgData{list: internal})
			}
			if len(p.XTestGoFiles) > 0 {
				external := p
				external.ImportPath += "_test"
				external.GoFiles = p.XTestGoFiles
				variants = append(variants, &pkgData{list: external})
			}
		}
		source := &pkgData{list: p}
		if err := loadPackage(source, imp, root); err != nil {
			return err
		}
		for obj := range source.defUnit {
			key := symbolKey(obj, module)
			if key != "" && obj.Pos().IsValid() {
				production[key] = relPath(root, source.fset.PositionFor(obj.Pos(), false).Filename)
			}
		}
	}

	rows := map[string]testFile{}
	for _, pd := range variants {
		var variantImporter types.Importer = imp
		if original, external := strings.CutSuffix(pd.list.ImportPath, "_test"); external {
			if export := exports[original+" ["+original+".test]"]; export != "" {
				testExports := maps.Clone(exports)
				testExports[original] = export
				variantImporter = newExportImporter(token.NewFileSet(), testExports)
			}
		}
		if err := loadPackage(pd, variantImporter, root); err != nil {
			return err
		}
		for _, f := range pd.files {
			path := relPath(root, pd.fset.File(f.Pos()).Name())
			if strings.HasSuffix(path, "_test.go") {
				rows[path] = describeTestFile(pd, f, path, module, production)
			}
		}
	}

	cmd := exec.Command("git", "ls-files", "-z", "--", "internal/*_test.go")
	cmd.Dir = root
	tracked, err := cmd.Output()
	if err != nil {
		return err
	}
	var result []testFile
	for _, path := range strings.Split(string(tracked), "\x00") {
		if path == "" || strings.Contains(path, "/testdata/") {
			continue
		}
		row, ok := rows[path]
		if !ok {
			pd := &pkgData{fset: token.NewFileSet()}
			f, err := parser.ParseFile(pd.fset, filepath.Join(root, path), nil, parser.ParseComments)
			if err != nil {
				return err
			}
			pd.checkErr = fmt.Errorf("excluded from the active Go build")
			row = describeTestFile(pd, f, path, module, production)
		}
		result = append(result, row)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	dir := filepath.Join(root, ".cache", "testnames")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "inventory.json"), append(data, '\n'), 0o644)
}

func describeTestFile(pd *pkgData, file *ast.File, path, module string, production map[string]string) testFile {
	result := testFile{
		Path: path, Package: file.Name.Name, Lines: pd.fset.File(file.Pos()).LineCount(),
		Tests: []string{}, Helpers: []string{}, HelperFunctions: map[string]int{},
		Refs: map[string]int{}, HelperRefs: map[string]int{}, Functions: map[string]testFunction{},
	}
	if pd.checkErr != nil {
		result.Error = pd.checkErr.Error()
	}
	env := map[string]bool{}
	declarations := map[types.Object]ast.Node{}
	if pd.info != nil {
		for _, f := range pd.files {
			if !strings.HasSuffix(pd.fset.File(f.Pos()).Name(), "_test.go") {
				continue
			}
			for _, decl := range f.Decls {
				switch decl := decl.(type) {
				case *ast.FuncDecl:
					declarations[pd.info.Defs[decl.Name]] = decl
				case *ast.GenDecl:
					for _, spec := range decl.Specs {
						if values, ok := spec.(*ast.ValueSpec); ok {
							for _, name := range values.Names {
								declarations[pd.info.Defs[name]] = values
							}
						}
					}
				}
			}
		}
		delete(declarations, nil)
	}
	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			name := decl.Name.Name
			if decl.Recv != nil {
				name = receiverBase(decl.Recv.List[0].Type) + "." + name
			}
			start := decl.Pos()
			if decl.Doc != nil {
				start = decl.Doc.Pos()
			}
			lines := pd.fset.PositionFor(decl.End(), false).Line - pd.fset.PositionFor(start, false).Line + 1
			if decl.Recv != nil || !goTestName(name) {
				result.Helpers = append(result.Helpers, name)
				result.HelperFunctions[name] = lines
				continue
			}
			refs := map[string]int{}
			tags := map[string]bool{}
			testReferences(pd, decl, module, production, declarations, refs, result.HelperRefs, tags)
			result.Tests = append(result.Tests, name)
			result.Functions[name] = testFunction{
				Lines: lines,
				Refs:  refs, Env: sortedKeys(tags),
			}
			for p, count := range refs {
				result.Refs[p] += count
			}
			for tag := range tags {
				env[tag] = true
			}
		case *ast.GenDecl:
			if decl.Tok != token.IMPORT {
				result.Helpers = append(result.Helpers, valueSpecNames(decl)...)
				for _, spec := range decl.Specs {
					if spec, ok := spec.(*ast.TypeSpec); ok {
						result.Helpers = append(result.Helpers, spec.Name.Name)
					}
				}
			}
		}
	}
	// Essential: helper-only files need environment evidence even without a test entry point.
	testReferences(pd, file, module, nil, declarations, nil, nil, env)
	result.Env = sortedKeys(env)
	return result
}

func goTestName(name string) bool {
	for _, prefix := range []string{"Test", "Benchmark", "Fuzz", "Example"} {
		if name, ok := strings.CutPrefix(name, prefix); ok {
			first, _ := utf8.DecodeRuneInString(name)
			return !unicode.IsLower(first)
		}
	}
	return false
}

func testReferences(pd *pkgData, node ast.Node, module string, production map[string]string, declarations map[types.Object]ast.Node, refs, helperRefs map[string]int, env map[string]bool) {
	seen := map[ast.Node]bool{}
	var visit func(ast.Node, bool)
	visit = func(node ast.Node, direct bool) {
		if seen[node] {
			return
		}
		seen[node] = true
		ast.Inspect(node, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.Ident:
				if pd.info == nil {
					break
				}
				obj := pd.info.Uses[n]
				if decl := declarations[obj]; decl != nil {
					visit(decl, false)
				}
				if obj == nil || obj.Pkg() == nil || !productionSymbol(obj) {
					break
				}
				if path := production[symbolKey(obj, module)]; path != "" {
					if direct {
						refs[path]++
					} else {
						helperRefs[path]++
					}
				}
			case *ast.CallExpr:
				recordTestEnv(pd, n, env)
			}
			return true
		})
	}
	visit(node, true)
}

func productionSymbol(obj types.Object) bool {
	if obj.Parent() == obj.Pkg().Scope() {
		return true
	}
	fn, ok := obj.(*types.Func)
	return ok && fn.Signature().Recv() != nil
}

func recordTestEnv(pd *pkgData, call *ast.CallExpr, env map[string]bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	name := sel.Sel.Name
	if pd.info != nil {
		if obj := pd.info.Uses[sel.Sel]; obj != nil && obj.Pkg() != nil {
			switch obj.Pkg().Path() {
			case "os":
				if name != "Getenv" && name != "LookupEnv" {
					return
				}
			case "testing":
				if name != "Skip" && name != "Skipf" && name != "SkipNow" {
					return
				}
			default:
				return
			}
		} else {
			return
		}
	} else {
		return
	}
	if len(call.Args) > 0 {
		if value := pd.info.Types[call.Args[0]].Value; value != nil {
			if literal, err := strconv.Unquote(value.ExactString()); err == nil {
				env[name+":"+strings.Join(strings.Fields(literal), " ")] = true
				return
			}
		}
	}
	env[name] = true
}
