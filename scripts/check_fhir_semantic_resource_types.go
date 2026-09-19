package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/fhir/schema"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	violations, err := semanticResourceTypeViolations(root)
	if err != nil {
		fail("check semantic ResourceType literals: %v", err)
	}
	if len(violations) > 0 {
		for _, violation := range violations {
			fmt.Fprintln(os.Stderr, violation)
		}
		fail("semantic production package contains hardcoded concrete FHIR ResourceType names")
	}
}

func semanticResourceTypeViolations(root string) ([]string, error) {
	dir := filepath.Join(root, "internal", "fhir", "semantic")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read semantic package: %w", err)
	}
	resourceTypes := make(map[string]struct{})
	for _, resourceType := range schema.ResourceTypes() {
		resourceTypes[string(resourceType)] = struct{}{}
	}

	fset := token.NewFileSet()
	violations := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if literal, ok := node.(*ast.BasicLit); ok && literal.Kind == token.STRING {
				checkLiteral(literal, resourceTypes, fset, path, &violations)
			}
			return true
		})
	}
	return violations, nil
}

func checkLiteral(literal *ast.BasicLit, resourceTypes map[string]struct{}, fset *token.FileSet, path string, violations *[]string) {
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		return
	}
	if _, ok := resourceTypes[value]; !ok {
		return
	}
	position := fset.Position(literal.Pos())
	*violations = append(*violations, fmt.Sprintf("%s:%d: concrete FHIR ResourceType literal %q", path, position.Line, value))
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
