package command_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandLeafIsStdlibOnly(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.ImportsOnly)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", name, parseErr)
		}
		for _, imported := range file.Imports {
			path := strings.Trim(imported.Path.Value, `"`)
			if strings.Contains(path, ".") {
				t.Errorf("%s imports %q: the command leaf is standard-library-only", name, path)
			}
		}
	}
}
