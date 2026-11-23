package main

import (
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type testInventoryImporter func(string) (*types.Package, error)

func (load testInventoryImporter) Import(path string) (*types.Package, error) {
	return load(path)
}

func TestInventoryOneWordStem(t *testing.T) {
	root := t.TempDir()
	const module = "example.org/loom"
	const sharedPath = module + "/internal/testkit/shared"
	files := map[string]string{
		"go.mod":    "module " + module + "\n",
		"lookup.go": "package lookup\nfunc lookup() {}\n",
		"shared.go": `package shared
import ("os"; "testing")
func Require(t *testing.T) {
	if os.Getenv("SHARED_URL") == "" {
		t.Skip("missing shared URL")
	}
}`,
		"lookup_test.go": `package lookup
import ("os"; "testing"; "example.org/loom/internal/testkit/shared")
const testURL = "LOOKUP_URL"
func requireURL(t *testing.T) {
	if os.Getenv(testURL) == "" {
		t.Skip("missing URL")
	}
}
func TestLookup(t *testing.T) {
	requireURL(t)
	shared.Require(t)
	lookup()
}`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	packages, err := goList(root, "-export", "-deps", "-json", "os", "testing")
	if err != nil {
		t.Fatal(err)
	}
	imports := newExportImporter(token.NewFileSet(), exportMap(packages))
	shared := &pkgData{list: listedPackage{
		ImportPath: sharedPath,
		Dir:        root,
		GoFiles:    []string{"shared.go"},
	}}
	if err := loadPackage(shared, imports, root); err != nil {
		t.Fatal(err)
	}
	if shared.checkErr != nil {
		t.Fatal(shared.checkErr)
	}
	fixtureImports := testInventoryImporter(func(path string) (*types.Package, error) {
		if path == sharedPath {
			return shared.pkg, nil
		}
		return imports.Import(path)
	})
	pd := &pkgData{list: listedPackage{
		ImportPath: module + "/internal/lookup",
		Dir:        root,
		GoFiles:    []string{"lookup.go", "lookup_test.go"},
	}}
	if err := loadPackage(pd, fixtureImports, root); err != nil {
		t.Fatal(err)
	}
	helpers := map[string]testHelper{}
	for obj, decl := range testHelperDeclarations(shared, shared.files[0]) {
		helpers[symbolKey(obj, module)] = testHelper{pkg: shared, decl: decl}
	}
	record := describeTestFile(pd, pd.files[1], "lookup_test.go", module, map[string]string{
		"internal/lookup::lookup": "lookup.go",
	}, helpers)
	if record.Path != "lookup_test.go" || record.Package != "lookup" || record.Lines != 13 || record.Error != "" {
		t.Fatalf("unexpected record: %+v", record)
	}
	want := testFunction{
		Lines: 5,
		Refs:  map[string]int{"lookup.go": 1},
		Env:   []string{"Getenv:LOOKUP_URL", "Getenv:SHARED_URL", "Skip:missing URL", "Skip:missing shared URL"},
	}
	if !reflect.DeepEqual(record.Tests, []string{"TestLookup"}) ||
		!reflect.DeepEqual(record.Helpers, []string{"testURL", "requireURL"}) ||
		!reflect.DeepEqual(record.Functions["TestLookup"], want) || !reflect.DeepEqual(record.Env, want.Env) {
		t.Fatalf("unexpected declarations or evidence: %+v", record)
	}
}
