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

	source := string(contents)
	source, err = replaceInFunction(source,
		"func (ec *executionContext) unmarshalNJSON2",
		"return &res, graphql.ErrorOnPath(ctx, err)",
		"return res, graphql.ErrorOnPath(ctx, err)",
	)
	if err != nil {
		fail("fix JSON unmarshal return: %v", err)
	}
	source, err = replaceInFunction(source,
		"func (ec *executionContext) unmarshalNFhirAggregateInput",
		"return res, graphql.ErrorOnPath(ctx, err)",
		"return &res, graphql.ErrorOnPath(ctx, err)",
	)
	if err != nil {
		fail("fix aggregate input unmarshal return: %v", err)
	}
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		fail("write %s: %v", path, err)
	}
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
