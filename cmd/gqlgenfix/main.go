// Command gqlgenfix corrects two invalid pointer returns emitted by the pinned
// gqlgen version for Loom's generated GraphQL types.
package main

import (
	"fmt"
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
		"return &res, graphql.ErrorOnPath(ctx, err)",
		"return res, graphql.ErrorOnPath(ctx, err)",
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
		const imports = "\t\"errors\"\n"
		if !strings.Contains(source, imports) {
			return "", fmt.Errorf("generated import anchor not found")
		}
		source = strings.Replace(source, imports, imports+"\t\"io\"\n", 1)
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
	start := strings.Index(contents, signature)
	if start < 0 {
		return "", fmt.Errorf("function %q not found", signature)
	}
	rest := contents[start:]
	end := strings.Index(rest, "\n}\n")
	if end < 0 {
		return "", fmt.Errorf("function %q has no closing brace", signature)
	}
	function := rest[:end+3]
	if strings.Contains(function, old) {
		return contents[:start] + strings.Replace(function, old, replacement, 1) + rest[end+3:], nil
	}
	if strings.Contains(function, replacement) {
		return contents, nil
	}
	return "", fmt.Errorf("function %q has neither expected return", signature)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
