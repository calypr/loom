// Command gqlgenfix corrects two invalid pointer returns emitted by the pinned
// gqlgen version for Loom's generated GraphQL types.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
)

func main() {
	if len(os.Args) != 2 {
		fail("usage: gqlgenfix GENERATED_GO_FILE")
	}
	path := os.Args[1]
	contents, err := os.ReadFile(path)
	if err != nil {
		fail("read %s: %v", path, err)
	}

	source, err := fixGeneratedSource(string(contents))
	if err != nil {
		fail("fix generated source: %v", err)
	}
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		fail("write %s: %v", path, err)
	}
}

func fixGeneratedSource(source string) (string, error) {
	var err error
	source, err = replaceInFunction(source,
		"func (ec *executionContext) unmarshalNJSON2",
		"&res",
		"res",
	)
	if err != nil {
		return "", fmt.Errorf("fix JSON unmarshal return: %w", err)
	}
	source, err = replaceInFunctionIfPresent(source,
		"func (ec *executionContext) unmarshalNFhirAggregateInput",
		"return res, graphql.ErrorOnPath(ctx, err)",
		"return &res, graphql.ErrorOnPath(ctx, err)",
	)
	if err != nil {
		return "", fmt.Errorf("fix aggregate input unmarshal return: %w", err)
	}
	if !strings.Contains(source, "func MarshalJSON(") {
		var importErr error
		source, importErr = addImport(source, "io")
		if importErr != nil {
			return "", importErr
		}
		source += jsonScalarHelpers
	}
	return source, nil
}

const jsonScalarHelpers = `

// JSON scalar support is appended by cmd/gqlgenfix because gqlgen does not
// emit helpers for the encoding/json.RawMessage model mapping.
func MarshalJSON(v json.RawMessage) graphql.Marshaler {
	return graphql.WriterFunc(func(w io.Writer) {
		_, _ = w.Write(v)
	})
}

func (ec *executionContext) unmarshalInputJSON(ctx context.Context, v any) (json.RawMessage, error) {
	return json.Marshal(v)
}

func (ec *executionContext) _JSON(ctx context.Context, sel ast.SelectionSet, v json.RawMessage) graphql.Marshaler {
	return MarshalJSON(v)
}
`

func replaceInFunctionIfPresent(contents, signature, old, replacement string) (string, error) {
	if !strings.Contains(contents, signature) {
		return contents, nil
	}
	return replaceInFunction(contents, signature, old, replacement)
}

func replaceInFunction(contents, signature, old, replacement string) (string, error) {
	name := strings.TrimPrefix(signature, "func (ec *executionContext) ")
	if index := strings.IndexByte(name, '('); index >= 0 {
		name = name[:index]
	}
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "generated.go", contents, 0)
	if err != nil {
		return "", fmt.Errorf("parse generated source while locating %q: %w", name, err)
	}
	var function *ast.FuncDecl
	ast.Inspect(file, func(node ast.Node) bool {
		declaration, ok := node.(*ast.FuncDecl)
		if !ok || declaration.Name == nil || (declaration.Name.Name != name && !strings.HasPrefix(declaration.Name.Name, name)) {
			return true
		}
		function = declaration
		return false
	})
	if function == nil || function.Body == nil {
		return "", fmt.Errorf("generated function %q not found", name)
	}
	fileToken := fileSet.File(function.Pos())
	var returnStart, returnEnd int
	var returnText string
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if returnText != "" {
			return false
		}
		returnStmt, ok := node.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		start := fileToken.Offset(returnStmt.Pos())
		end := fileToken.Offset(returnStmt.End())
		if start < 0 || end > len(contents) || end <= start {
			return true
		}
		candidate := contents[start:end]
		if strings.Contains(candidate, old) || strings.Contains(candidate, replacement) {
			returnStart, returnEnd, returnText = start, end, candidate
		}
		return false
	})
	if returnText == "" {
		return "", fmt.Errorf("generated function %q has neither expected return nor corrected return", name)
	}
	if strings.Contains(returnText, old) {
		return contents[:returnStart] + strings.Replace(returnText, old, replacement, 1) + contents[returnEnd:], nil
	}
	return contents, nil
}

func addImport(source, importPath string) (string, error) {
	quoted := fmt.Sprintf("\"%s\"", importPath)
	if strings.Contains(source, quoted) {
		return source, nil
	}
	if start := strings.Index(source, "import ("); start >= 0 {
		lineEnd := strings.IndexByte(source[start:], '\n')
		if lineEnd < 0 {
			return "", fmt.Errorf("generated import block has no line ending; add %s manually", quoted)
		}
		lineEnd += start + 1
		return source[:lineEnd] + "\t" + quoted + "\n" + source[lineEnd:], nil
	}
	if start := strings.Index(source, "import "); start >= 0 {
		lineEnd := strings.IndexByte(source[start:], '\n')
		if lineEnd < 0 {
			return "", fmt.Errorf("generated import declaration has no line ending; add %s manually", quoted)
		}
		lineEnd += start + 1
		return source[:lineEnd] + "import " + quoted + "\n" + source[lineEnd:], nil
	}
	return "", fmt.Errorf("generated import block not found; add %s manually", quoted)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
