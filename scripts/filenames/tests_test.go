package main

import (
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestInventoryOneWordStem(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":    "module example.org/loom\n",
		"lookup.go": "package lookup\nfunc lookup() {}\n",
		"lookup_test.go": `package lookup
import ("os"; "testing")
const testURL = "LOOKUP_URL"
func requireURL(t *testing.T) {
	if os.Getenv(testURL) == "" {
		t.Skip("missing URL")
	}
}
func TestLookup(t *testing.T) {
	requireURL(t)
	lookup()
}`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pd := &pkgData{list: listedPackage{
		ImportPath: "example.org/loom/internal/lookup",
		Dir:        root,
		GoFiles:    []string{"lookup.go", "lookup_test.go"},
	}}
	packages, err := goList(root, "-export", "-deps", "-json", "os", "testing")
	if err != nil {
		t.Fatal(err)
	}
	if err := loadPackage(pd, newExportImporter(token.NewFileSet(), exportMap(packages)), root); err != nil {
		t.Fatal(err)
	}
	record := describeTestFile(pd, pd.files[1], "lookup_test.go", "example.org/loom", map[string]string{
		"internal/lookup::lookup": "lookup.go",
	})
	if record.Path != "lookup_test.go" || record.Package != "lookup" || record.Lines != 12 || record.Error != "" {
		t.Fatalf("unexpected record: %+v", record)
	}
	want := testFunction{Lines: 4, Refs: map[string]int{"lookup.go": 1}, Env: []string{"Getenv:LOOKUP_URL", "Skip:missing URL"}}
	if !reflect.DeepEqual(record.Tests, []string{"TestLookup"}) ||
		!reflect.DeepEqual(record.Helpers, []string{"testURL", "requireURL"}) ||
		!reflect.DeepEqual(record.Functions["TestLookup"], want) {
		t.Fatalf("unexpected declarations or evidence: %+v", record)
	}
}
