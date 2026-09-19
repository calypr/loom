package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"

	"github.com/calypr/loom/internal/fhir/schema"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	dir := filepath.Join(root, "internal", "fhir", "semantic")
	entries, err := os.ReadDir(dir)
	if err != nil {
		fail("read semantic package: %v", err)
	}
	resourceTypes := make(map[string]struct{})
	for _, resourceType := range schema.ResourceTypes() {
		resourceTypes[string(resourceType)] = struct{}{}
	}

	fset := token.NewFileSet()
	violations := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			fail("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if literal, ok := node.(*ast.BasicLit); ok && literal.Kind == token.STRING {
				checkLiteral(literal, resourceTypes, fset, path, &violations)
			}
			return true
		})
	}
	if len(violations) > 0 {
		for _, violation := range violations {
			fmt.Fprintln(os.Stderr, violation)
		}
		fail("semantic package contains hardcoded concrete FHIR ResourceType names")
	}
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
