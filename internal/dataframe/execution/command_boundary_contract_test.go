package execution

// arch-backend-01 source boundary contract: the only execution boundary this
// package speaks is the typed compiler.CompiledCommand. These contract tests
// pin (1) the adapter seam refuses mismatched, zero-tagged, and unregistered
// command tags before any adapter callback runs, (2) adapter registration
// fields on Config are named typed executor types, and (3) the pre-migration
// loose row-adapter shape (context, query string, batch size, bind map,
// visitor) cannot reappear in this package's non-test sources.

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestSourceCommandBoundaryContractRefusesMismatchedTagBeforeAdapter(t *testing.T) {
	v := &verifyArch01Trace{}
	config := verifyArch01Config(v)
	mismatched, err := compiler.NewAQLCommand("FOR d IN patients RETURN d", map[string]any{"project": "P1"})
	if err != nil {
		t.Fatalf("forge AQL-tagged command: %v", err)
	}
	config.ResolvePostgreSQLCommands = verifyArch01Resolver(&ResolvedPostgreSQLCommand{Command: mismatched, Columns: []string{"id"}}, nil)
	config.PostgreSQLQueryRows = verifyArch01Row(v, &v.pgFull, verifyArch01Rows())
	engine, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, streamErr := engine.Streams(context.Background(), verifyArch01Resolved([]string{"id"}, verifyArch01Schema("id")))
	if !errors.Is(streamErr, compiler.ErrCompiledCommandBackendMismatch) {
		t.Fatalf("AQL-tagged command on the PostgreSQL selection: err = %v, want %v", streamErr, compiler.ErrCompiledCommandBackendMismatch)
	}
	aqlFull, aqlPreview, pgFull, pgPreview, controls, ch := v.snapshot()
	if len(aqlFull) != 0 || len(aqlPreview) != 0 || len(pgFull) != 0 || len(pgPreview) != 0 || len(controls) != 0 || ch != 0 {
		t.Fatalf("mismatched tag reached an adapter before refusal: aql=%v preview=%v pg=%v pgPreview=%v controls=%v ch=%d",
			aqlFull, aqlPreview, pgFull, pgPreview, controls, ch)
	}
}

func TestSourceCommandBoundaryContractZeroTaggedCommandFailsClosed(t *testing.T) {
	v := &verifyArch01Trace{}
	config := verifyArch01Config(v)
	config.ResolvePostgreSQLCommands = verifyArch01Resolver(&ResolvedPostgreSQLCommand{Command: compiler.CompiledCommand{}, Columns: []string{"id"}}, nil)
	config.PostgreSQLQueryRows = verifyArch01Row(v, &v.pgFull, verifyArch01Rows())
	engine, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, streamErr := engine.Streams(context.Background(), verifyArch01Resolved([]string{"id"}, verifyArch01Schema("id")))
	if !errors.Is(streamErr, compiler.ErrCompiledCommandBackendNotRegistered) {
		t.Fatalf("zero-tagged command: err = %v, want %v", streamErr, compiler.ErrCompiledCommandBackendNotRegistered)
	}
	aqlFull, aqlPreview, pgFull, pgPreview, controls, ch := v.snapshot()
	if len(aqlFull) != 0 || len(aqlPreview) != 0 || len(pgFull) != 0 || len(pgPreview) != 0 || len(controls) != 0 || ch != 0 {
		t.Fatalf("zero-tagged command reached an adapter before refusal: aql=%v preview=%v pg=%v pgPreview=%v controls=%v ch=%d",
			aqlFull, aqlPreview, pgFull, pgPreview, controls, ch)
	}
}

func TestSourceCommandBoundaryContractPostgreSQLNeverFallsBackToAQL(t *testing.T) {
	v := &verifyArch01Trace{}
	config := verifyArch01Config(v)
	config.ResolvePostgreSQLCommands = verifyArch01Resolver(&ResolvedPostgreSQLCommand{Command: verifyArch01Command(t), Columns: []string{"id"}}, nil)
	// Deliberately leave PostgreSQLQueryRows unregistered while every other
	// adapter, including the AQL executor, is present. Construction must
	// refuse the combination outright.
	if _, err := New(config); err == nil || !strings.Contains(err.Error(), "PostgreSQL row executor") {
		t.Fatalf("resolver without PostgreSQL executor must be refused at construction: %v", err)
	}
	// The stream-level seam must also fail closed instead of falling back:
	// a PostgreSQL-tagged command with no executor never reaches any adapter.
	command := verifyArch01Command(t)
	stream := OutputStream{Name: "resources", command: command}
	if streamErr := stream.streamRaw(context.Background(), func(map[string]any) error { return nil }); !errors.Is(streamErr, compiler.ErrCompiledCommandBackendNotRegistered) {
		t.Fatalf("PostgreSQL command without executor: err = %v, want %v", streamErr, compiler.ErrCompiledCommandBackendNotRegistered)
	}
	aqlFull, aqlPreview, pgFull, pgPreview, _, ch := v.snapshot()
	if len(pgFull) != 0 || len(pgPreview) != 0 || ch != 0 {
		t.Fatalf("missing-executor path reached an executor: pg=%v pgPreview=%v ch=%d", pgFull, pgPreview, ch)
	}
	if len(aqlFull) != 0 || len(aqlPreview) != 0 {
		t.Fatalf("PostgreSQL request fell back to the AQL executor: full=%v preview=%v", aqlFull, aqlPreview)
	}
}

func TestSourceCommandBoundaryContractAQLWithoutAdapterFailsClosed(t *testing.T) {
	command, err := compiler.NewAQLCommand("FOR d IN patients RETURN d", map[string]any{"project": "P1"})
	if err != nil {
		t.Fatalf("forge AQL command: %v", err)
	}
	stream := OutputStream{Name: "resources", command: command}
	if streamErr := stream.streamRaw(context.Background(), func(map[string]any) error { return nil }); !errors.Is(streamErr, compiler.ErrCompiledCommandBackendNotRegistered) {
		t.Fatalf("AQL command without adapter: err = %v, want %v", streamErr, compiler.ErrCompiledCommandBackendNotRegistered)
	}
}

func TestSourceCommandBoundaryContractAQLExecutorReceivesTypedCommand(t *testing.T) {
	v := &verifyArch01Trace{}
	config := verifyArch01Config(v)
	engine, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resolved, err := engine.CompileResolvedBundle(context.Background(), testResolvedBundle([]string{}),
		recipe.RuntimeBindings{Project: "P1", DatasetGeneration: "G1"})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	streams, err := engine.Streams(context.Background(), resolved)
	if err != nil {
		t.Fatalf("Streams: %v", err)
	}
	if len(streams) == 0 {
		t.Fatal("no streams to exercise the AQL boundary")
	}
	for _, stream := range streams {
		if _, err := stream.Stream(context.Background(), func(map[string]any) error { return nil }); err != nil {
			t.Fatalf("Stream: %v", err)
		}
	}
	aqlFull, _, _, _, _, _ := v.snapshot()
	if len(aqlFull) != 1 || !strings.Contains(aqlFull[0], "RETURN") {
		t.Fatalf("AQL executor did not receive the compiled command text: %v", aqlFull)
	}
	// The boundary itself must hand the adapter a tagged command: capture it
	// directly rather than a derived string.
	captured := compiler.CompiledCommand{}
	captureEngine, err := New(Config{
		Registry:    invalidRecipeRegistry{},
		ScopeDigest: func(recipe.RuntimeBindings) string { return "capture-scope" },
		QueryRows: func(_ context.Context, command compiler.CompiledCommand, _ int, _ RowVisitor) error {
			captured = command
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New capture engine: %v", err)
	}
	captureResolved, err := captureEngine.CompileResolvedBundle(context.Background(), testResolvedBundle([]string{}),
		recipe.RuntimeBindings{Project: "P1", DatasetGeneration: "G1"})
	if err != nil {
		t.Fatalf("capture compile: %v", err)
	}
	captureStreams, err := captureEngine.Streams(context.Background(), captureResolved)
	if err != nil {
		t.Fatalf("capture Streams: %v", err)
	}
	if len(captureStreams) == 0 {
		t.Fatal("no capture streams")
	}
	for _, stream := range captureStreams {
		if _, err := stream.Stream(context.Background(), func(map[string]any) error { return nil }); err != nil {
			t.Fatalf("capture Stream: %v", err)
		}
	}
	if captured.Backend() != compiler.CompiledBackendAQL {
		t.Fatalf("adapter received command tagged %q, want the AQL tag", captured.Backend())
	}
}

// TestSourceCommandBoundaryContractConfigAdaptersAreNamedTypes pins that the
// Config executor fields keep using the closed named executor types, so a
// loose inline row-adapter signature can never return as a Config field.
func TestSourceCommandBoundaryContractConfigAdaptersAreNamedTypes(t *testing.T) {
	fields := parseStructFieldTypes(t, "Config")
	allowed := map[string]bool{
		"QueryRows": true, "PreviewQueryRows": true,
		"PostgreSQLQueryRows": true, "PreviewPostgreSQLQueryRows": true,
		"ClickHouseQueryRows": true,
	}
	for _, field := range fields {
		if !strings.HasSuffix(field.name, "QueryRows") {
			continue
		}
		if !allowed[field.name] {
			t.Fatalf("Config has unexpected QueryRows-typed field %q; the executor field set is closed", field.name)
		}
		if field.inlineFunc {
			t.Fatalf("Config field %q uses an inline func type; row executors must be the named typed contracts", field.name)
		}
		if !namedExecutorTypes[field.typeText] {
			t.Fatalf("Config field %q has executor type %q, want one of the named typed contracts", field.name, field.typeText)
		}
	}
}

var namedExecutorTypes = map[string]bool{
	"QueryRows":           true,
	"PostgreSQLQueryRows": true,
	"ClickHouseQueryRows": true,
}

// TestSourceCommandBoundaryContractNoLooseRowAdapterSignatures rejects the
// pre-migration executor shape — (context.Context, string, int,
// map[string]any, visitor) — anywhere in this package's non-test sources.
func TestSourceCommandBoundaryContractNoLooseRowAdapterSignatures(t *testing.T) {
	fset := token.NewFileSet()
	packages, err := parser.ParseDir(fset, ".", func(info fs.FileInfo) bool {
		return strings.HasSuffix(info.Name(), ".go") && !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse package sources: %v", err)
	}
	for _, pkg := range packages {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(node ast.Node) bool {
				function, ok := node.(*ast.FuncType)
				if !ok || function.Params == nil {
					return true
				}
				if isLooseRowAdapterSignature(function) {
					position := fset.Position(function.Pos())
					t.Errorf("%s: loose row-adapter signature (context, string, int, bind map, visitor) reappeared", position)
				}
				return true
			})
		}
	}
}

func isLooseRowAdapterSignature(signature *ast.FuncType) bool {
	if signature.Params == nil || len(signature.Params.List) < 4 {
		return false
	}
	var flattened []ast.Expr
	for _, field := range signature.Params.List {
		for range field.Names {
			flattened = append(flattened, field.Type)
		}
		if len(field.Names) == 0 {
			flattened = append(flattened, field.Type)
		}
	}
	if len(flattened) < 5 {
		return false
	}
	if !isQualifiedIdent(flattened[0], "context", "Context") {
		return false
	}
	if identName(flattened[1]) != "string" || identName(flattened[2]) != "int" {
		return false
	}
	bindMap, ok := flattened[3].(*ast.MapType)
	if !ok || identName(bindMap.Key) != "string" {
		return false
	}
	switch bindMap.Value.(type) {
	case *ast.Ident, *ast.InterfaceType:
	default:
		return false
	}
	_, isVisitor := flattened[4].(*ast.FuncType)
	return isVisitor
}

type structFieldInfo struct {
	name       string
	typeText   string
	inlineFunc bool
}

func parseStructFieldTypes(t *testing.T, structName string) []structFieldInfo {
	t.Helper()
	fset := token.NewFileSet()
	packages, err := parser.ParseDir(fset, ".", func(info fs.FileInfo) bool {
		return strings.HasSuffix(info.Name(), ".go") && !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse package sources: %v", err)
	}
	var fields []structFieldInfo
	found := false
	for _, pkg := range packages {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				general, ok := decl.(*ast.GenDecl)
				if !ok || general.Tok != token.TYPE {
					continue
				}
				for _, spec := range general.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if !ok || typeSpec.Name.Name != structName {
						continue
					}
					found = true
					subject, ok := typeSpec.Type.(*ast.StructType)
					if !ok {
						t.Fatalf("%s is not a struct", structName)
					}
					for _, field := range subject.Fields.List {
						typeText := exprText(field.Type)
						inline := false
						if _, ok := field.Type.(*ast.FuncType); ok {
							inline = true
						}
						if len(field.Names) == 0 {
							fields = append(fields, structFieldInfo{typeText: typeText, inlineFunc: inline})
							continue
						}
						for _, name := range field.Names {
							fields = append(fields, structFieldInfo{name: name.Name, typeText: typeText, inlineFunc: inline})
						}
					}
				}
			}
		}
	}
	if !found {
		t.Fatalf("type %s not found in package sources", structName)
	}
	return fields
}

func exprText(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.SelectorExpr:
		return exprText(typed.X) + "." + typed.Sel.Name
	case *ast.StarExpr:
		return "*" + exprText(typed.X)
	case *ast.FuncType:
		return "func"
	default:
		return "?"
	}
}

func identName(expr ast.Expr) string {
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

func isQualifiedIdent(expr ast.Expr, pkg, name string) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return identName(selector.X) == pkg && selector.Sel.Name == name
}
