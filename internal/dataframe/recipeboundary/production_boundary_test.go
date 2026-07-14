// Package recipeboundary contains repository-level architecture tests. The
// tests intentionally inspect production source rather than runtime behavior:
// a map evaluator is useful for differential fixtures, but must not become a
// second production compiler.
package recipeboundary

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var productionRoots = []string{
	"../../server",
	"../../../graphqlapi",
	"../runtime",
	"../materialization",
	"../recipecompile",
	"../semantic",
}

var dispatchRoots = append(append([]string(nil), productionRoots...), "../recipeexec")

func TestProductionDoesNotImportReferenceEvaluator(t *testing.T) {
	files := productionFiles(t)
	for _, filename := range files {
		f, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, importSpec := range f.Imports {
			path, _ := strconv.Unquote(importSpec.Path.Value)
			if strings.HasSuffix(path, "/internal/dataframe/recipeeval") && !strings.HasSuffix(filename, "_test.go") {
				t.Errorf("production file %s imports reference evaluator", filename)
			}
		}
	}
}

func TestProductionHasNoDefaultOutputDispatch(t *testing.T) {
	for _, filename := range filesFromRoots(t, dispatchRoots) {
		if strings.HasSuffix(filename, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			caseClause, ok := node.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expr := range caseClause.List {
				if value, ok := stringLiteral(expr); ok && defaultOutputNames[value] {
					t.Errorf("%s dispatches on default output name %q; use recipe data", filename, value)
				}
			}
			return true
		})
		ast.Inspect(file, func(node ast.Node) bool {
			binary, ok := node.(*ast.BinaryExpr)
			if !ok || binary.Op.String() != "==" && binary.Op.String() != "!=" {
				return true
			}
			for _, expr := range []ast.Expr{binary.X, binary.Y} {
				if value, ok := stringLiteral(expr); ok && defaultOutputNames[value] {
					t.Errorf("%s compares a default output name %q; use recipe data", filename, value)
				}
			}
			return true
		})
	}
}

var defaultOutputNames = map[string]bool{
	"DocumentReference":        true,
	"ResearchSubject":          true,
	"MedicationAdministration": true,
	"Specimen":                 true,
	"GroupMember":              true,
}

func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	return value, err == nil
}

func productionFiles(t *testing.T) []string {
	t.Helper()
	return filesFromRoots(t, productionRoots)
}

func filesFromRoots(t *testing.T, roots []string) []string {
	t.Helper()
	files := make([]string, 0)
	for _, root := range roots {
		matches, err := filepath.Glob(filepath.Join(root, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, matches...)
	}
	return files
}
