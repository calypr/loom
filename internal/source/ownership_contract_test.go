package source

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

type namedFile struct {
	name string
	f    *ast.File
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot locate the repository root")
	}
	dir := filepath.Dir(file)
	root := filepath.Dir(filepath.Dir(dir))
	if fi, err := os.Stat(filepath.Join(root, "go.mod")); err != nil || fi.IsDir() {
		t.Fatalf("resolved root %q has no go.mod: %v", root, err)
	}
	return root
}

func isStdlibImport(path string) bool {
	first := path
	if i := strings.Index(path, "/"); i >= 0 {
		first = path[:i]
	}
	return !strings.Contains(first, ".")
}

func parsePackageFiles(t *testing.T, dir string, withTests bool) []namedFile {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var files []namedFile
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		if !withTests && strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s/%s: %v", dir, name, err)
		}
		files = append(files, namedFile{name: name, f: f})
	}
	if len(files) == 0 {
		t.Fatalf("no Go files found in %s", dir)
	}
	return files
}

func importedPaths(files []namedFile) []string {
	var out []string
	for _, nf := range files {
		for _, imp := range nf.f.Imports {
			out = append(out, strings.Trim(imp.Path.Value, `"`))
		}
	}
	return out
}

func selectorName(t *testing.T, expr ast.Expr) string {
	t.Helper()
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		pkg, ok := e.X.(*ast.Ident)
		if !ok {
			t.Fatalf("selector %v has non-ident package", expr)
		}
		return pkg.Name + "." + e.Sel.Name
	case *ast.StarExpr:
		return "*" + selectorName(t, e.X)
	case *ast.ArrayType:
		return "[]" + selectorName(t, e.Elt)
	case *ast.MapType:
		return "map[" + selectorName(t, e.Key) + "]" + selectorName(t, e.Value)
	default:
		t.Fatalf("unsupported type expression %T", expr)
		return ""
	}
}

func TestSourceResourceOwnershipContract(t *testing.T) {
	root := repoRoot(t)
	sourceDir := filepath.Join(root, "internal", "source")
	catalogDir := filepath.Join(root, "internal", "catalog")
	storeDir := filepath.Join(root, "internal", "backends", "postgres", "internal", "store")
	ingestDir := filepath.Join(root, "internal", "ingest")

	t.Run("SourceLeafIsStdlibOnly", func(t *testing.T) {
		files := parsePackageFiles(t, sourceDir, false)
		for _, imp := range importedPaths(files) {
			if !isStdlibImport(imp) {
				t.Errorf("internal/source production import %q is not standard library; "+
					"the logical contract leaf must depend on no backend, driver, or catalog package", imp)
			}
		}
		for _, imp := range []string{"github.com/calypr/loom/internal/catalog",
			"github.com/calypr/loom/internal/backends/postgres/internal/store",
			"github.com/jackc/pgx/v5", "github.com/arangodb/go-driver"} {
			for _, got := range importedPaths(files) {
				if got == imp {
					t.Errorf("internal/source imports %q; forbidden for the neutral contract", imp)
				}
			}
		}
	})

	t.Run("ResourceCarriesOnlyLogicalIdentity", func(t *testing.T) {
		want := []string{"Project", "DatasetGeneration", "ResourceType", "LogicalID", "AuthResourcePath", "Payload"}
		typ := reflect.TypeOf(Resource{})
		if typ.NumField() != len(want) {
			t.Fatalf("Resource has %d fields, want exactly %d (%v)", typ.NumField(), len(want), want)
		}
		for i, name := range want {
			f := typ.Field(i)
			if f.Name != name {
				t.Errorf("field %d is %q, want %q", i, f.Name, name)
			}
			if f.Name == "Payload" {
				if f.Type != reflect.TypeOf(json.RawMessage(nil)) {
					t.Errorf("Payload type = %v, want json.RawMessage", f.Type)
				}
				continue
			}
			if f.Type.Kind() != reflect.String {
				t.Errorf("field %s kind = %v, want string", f.Name, f.Type.Kind())
			}
		}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			switch f.Type.Kind() {
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
				reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				t.Errorf("field %s has integer kind %v; the logical contract must not carry a "+
					"storage sequence or any database-assigned number", f.Name, f.Type.Kind())
			}
			if strings.Contains(strings.ToLower(f.Name), "seq") ||
				strings.Contains(strings.ToLower(f.Name), "cursor") {
				t.Errorf("field %s names a storage sequence/cursor; physical layout must stay "+
					"owned by the storage implementation", f.Name)
			}
		}
	})

	t.Run("ScopeTriStatesAreDistinctAndExplicit", func(t *testing.T) {
		typ := reflect.TypeOf(ResourceScope{})
		flag, ok := typ.FieldByName("AuthResourcePathsUnrestricted")
		if !ok {
			t.Fatal("ResourceScope lost AuthResourcePathsUnrestricted")
		}
		if flag.Type.Kind() != reflect.Ptr || flag.Type.Elem().Kind() != reflect.Bool {
			t.Fatalf("AuthResourcePathsUnrestricted type = %v, want *bool (tri-state)", flag.Type)
		}
		yes := true
		no := false
		restricted := ResourceScope{Project: "p", AuthResourcePathsUnrestricted: &no,
			AuthResourcePaths: []string{"a/b"}}
		restrictedEmpty := ResourceScope{Project: "p", AuthResourcePathsUnrestricted: &no}
		unrestricted := ResourceScope{Project: "p", AuthResourcePathsUnrestricted: &yes}
		omittedGeneration := ResourceScope{Project: "p", AuthResourcePathsUnrestricted: &no}
		if reflect.DeepEqual(restrictedEmpty, unrestricted) {
			t.Error("restricted-empty and explicit-unrestricted scopes compare equal; the two modes must stay distinct")
		}
		if reflect.DeepEqual(restricted, restrictedEmpty) || reflect.DeepEqual(restricted, unrestricted) {
			t.Error("a restricted scope with authorized paths must differ from both restricted-empty and unrestricted scopes")
		}
		if omittedGeneration.DatasetGeneration != "" {
			t.Error("omitted dataset generation must be the empty legacy namespace")
		}
		nilFlag := ResourceScope{Project: "p"}
		if nilFlag.AuthResourcePathsUnrestricted != nil {
			t.Error("zero-value scope must leave the unrestricted flag nil (rejected state)")
		}
	})

	t.Run("GenericCatalogExportsNoPostgreSQLRowOrScope", func(t *testing.T) {
		files := parsePackageFiles(t, catalogDir, false)
		var bad []string
		for _, nf := range files {
			f := nf.f
			for _, decl := range f.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.TYPE {
					continue
				}
				for _, spec := range gen.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok || !ts.Name.IsExported() {
						continue
					}
					n := ts.Name.Name
					if strings.HasPrefix(n, "PG") || strings.HasSuffix(n, "ResourceRow") ||
						strings.HasSuffix(n, "ResourceScope") || strings.Contains(n, "ResourceSeq") {
						bad = append(bad, nf.name+":"+n)
					}
				}
			}
		}
		if len(bad) > 0 {
			t.Errorf("internal/catalog exports PostgreSQL storage DTO names %v; physical resource "+
				"rows/scopes belong to the storage implementation", bad)
		}
		forbidden := []string{"internal/store", "internal/backends", "pgx", "arangodb",
			"jackc", "database/sql"}
		for _, imp := range importedPaths(files) {
			for _, bad := range forbidden {
				if strings.Contains(imp, bad) {
					t.Errorf("internal/catalog production import %q touches backend/driver territory (%q)", imp, bad)
				}
			}
		}
	})

	t.Run("PhysicalRowAndWriteSurfacesHaveOneOwnerEach", func(t *testing.T) {
		files := parsePackageFiles(t, storeDir, false)
		holders := map[string]string{}
		structs := map[string]*ast.StructType{}
		for _, nf := range files {
			for _, decl := range nf.f.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.TYPE {
					continue
				}
				for _, spec := range gen.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					st, ok := ts.Type.(*ast.StructType)
					if !ok {
						continue
					}
					structs[ts.Name.Name] = st
					for _, fld := range st.Fields.List {
						for _, name := range fld.Names {
							if name.Name == "ResourceSeq" {
								holders[ts.Name.Name] = nf.name
							}
						}
					}
				}
			}
		}
		if len(holders) != 1 || holders["ResourceRow"] == "" {
			t.Errorf("structs carrying ResourceSeq = %v; exactly one store-owned physical "+
				"row type (ResourceRow) may hold the storage sequence", holders)
		}
		if rr, ok := structs["ResourceRow"]; !ok {
			t.Fatal("the backend store lost its physical ResourceRow owner")
		} else {
			hasSeq := false
			hasLogical := false
			for _, fld := range rr.Fields.List {
				if len(fld.Names) == 0 && selectorName(t, fld.Type) == "source.Resource" {
					hasLogical = true
					continue
				}
				if len(fld.Names) == 1 && fld.Names[0].Name == "ResourceSeq" {
					hasSeq = true
					continue
				}
				t.Errorf("ResourceRow carries an unexpected field %v; the physical row is exactly "+
					"the logical source contract plus the store-assigned sequence", fld.Names)
			}
			if !hasLogical {
				t.Error("ResourceRow must anonymously embed source.Resource so promoted logical fields stay stable")
			}
			if !hasSeq {
				t.Error("ResourceRow must declare the physical ResourceSeq field")
			}
		}

		surfaces := map[string]string{}
		for _, nf := range files {
			base := nf.name
			for _, decl := range nf.f.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					switch d.Name.Name {
					case "InsertResource":
						for _, fl := range d.Type.Params.List {
							for _, nm := range fl.Names {
								if nm.Name == "rec" {
									surfaces[base+":InsertResource.rec"] = selectorName(t, fl.Type)
								}
							}
						}
					}
				case *ast.GenDecl:
					if d.Tok != token.TYPE {
						continue
					}
					for _, spec := range d.Specs {
						ts, ok := spec.(*ast.TypeSpec)
						if !ok {
							continue
						}
						st, ok := ts.Type.(*ast.StructType)
						if !ok {
							continue
						}
						for _, fld := range st.Fields.List {
							for _, nm := range fld.Names {
								key := base + ":" + ts.Name.Name + "." + nm.Name
								if key == "resource_writer.go:ResourceBatch.Rows" ||
									key == "batch_writer.go:Batch.Resources" {
									surfaces[key] = selectorName(t, fld.Type)
								}
							}
						}
					}
				}
			}
		}
		want := map[string]string{
			"resource.go:InsertResource.rec":        "source.Resource",
			"resource_writer.go:ResourceBatch.Rows": "[]source.Resource",
			"batch_writer.go:Batch.Resources":       "[]source.Resource",
		}
		for key, expected := range want {
			got, ok := surfaces[key]
			if !ok {
				t.Errorf("write surface %s not found in the store AST", key)
				continue
			}
			if got != expected {
				t.Errorf("write surface %s has type %q, want %q; callers must never be able to "+
					"offer the physical row (or its server-assigned sequence)", key, got, expected)
			}
		}
	})

	t.Run("IngestNeverTouchesPhysicalSequence", func(t *testing.T) {
		files := parsePackageFiles(t, ingestDir, false)
		var hits []string
		for _, nf := range files {
			ast.Inspect(nf.f, func(n ast.Node) bool {
				switch e := n.(type) {
				case *ast.SelectorExpr:
					if e.Sel.Name == "ResourceSeq" {
						hits = append(hits, nf.name)
					}
				case *ast.Ident:
					if e.Name == "ResourceSeq" {
						hits = append(hits, nf.name)
					}
				}
				return true
			})
		}
		if len(hits) > 0 {
			t.Errorf("internal/ingest production files reference the physical ResourceSeq in %v; "+
				"ingestion must build only the logical source contract", hits)
		}
	})
}
