package recipeboundary

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The map evaluator is a reference implementation for differential tests. It
// must never become the server's production translation path.
func TestProductionPackagesDoNotImportReferenceEvaluator(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate boundary test")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	packages := []string{"internal/server", "internal/httpapi", "internal/dataframe/runtime", "internal/dataframe/materialization", "graphqlapi"}
	for _, pkg := range packages {
		dir := filepath.Join(root, pkg)
		err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(data), "internal/dataframe/recipeeval") {
				return &forbiddenImportError{path: path}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

type forbiddenImportError struct{ path string }

func (e *forbiddenImportError) Error() string {
	return "production package imports reference recipe evaluator: " + e.path
}
