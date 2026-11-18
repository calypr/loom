package testkit

import (
	"os"
	"path/filepath"
	"testing"
)

// Essential: Go tests run from their package directory.
func FixturePath(t testing.TB, parts ...string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		_, err := os.Stat(filepath.Join(dir, "go.mod"))
		if err == nil {
			return filepath.Join(append([]string{dir, "tests", "fixtures"}, parts...)...)
		}
		if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find go.mod in a parent directory")
		}
		dir = parent
	}
}
