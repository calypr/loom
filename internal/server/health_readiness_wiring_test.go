package server

// Offline wiring witnesses for pg-runtime-health-readiness (L2):
//   - each PostgreSQL composition arm must wire the FULL readiness probe
//     (control Runtime.Readiness) and the safe CoreState classifier;
//   - the ArangoDB arm must keep its published probe closures and must NOT
//     gain a CoreState classifier, so its bodies stay byte-verbatim.
// Plus the classifier's closed-vocabulary mapping cells, offline under any
// environment (the live legs for the same tokens live in
// runtime_postgres_live_test.go and postgres_query_test.go).

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	backendspostgres "github.com/calypr/loom/internal/backends/postgres"
)

// httpConfigLiteral returns the HTTPConfig composite literal passed to the
// httpapi.NewHTTPServer call inside the named function, or fails.
func httpConfigLiteral(t *testing.T, file, funcName string) *ast.CompositeLit {
	t.Helper()
	set := token.NewFileSet()
	parsed, err := parser.ParseFile(set, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	var found *ast.CompositeLit
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != funcName || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "NewHTTPServer" {
				return true
			}
			if len(call.Args) != 1 {
				return true
			}
			lit, ok := call.Args[0].(*ast.CompositeLit)
			if !ok {
				return true
			}
			tv, ok := lit.Type.(*ast.SelectorExpr)
			if !ok || tv.Sel.Name != "HTTPConfig" {
				return true
			}
			if found != nil {
				t.Fatalf("%s has more than one httpapi.NewHTTPServer HTTPConfig literal; the witness must be re-read, not widened", funcName)
			}
			found = lit
			return true
		})
	}
	if found == nil {
		t.Fatalf("no httpapi.NewHTTPServer(HTTPConfig{...}) literal found in %s::%s", file, funcName)
	}
	return found
}

func literalField(lit *ast.CompositeLit, key string) (ast.Expr, bool) {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if k, ok := kv.Key.(*ast.Ident); ok && k.Name == key {
			return kv.Value, true
		}
	}
	return nil, false
}

func callsSelector(node ast.Node, recv, name string) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == recv && sel.Sel.Name == name {
				found = true
			}
		}
		return true
	})
	return found
}

// L2a: the full runtime and first-query route arms both select the full
// probe and the classifier; the Arango arm keeps its published closures and
// gains no classifier (its body shape is pinned in httpapi's
// TestReadinessWithoutClassifierKeepsPublishedBodies).
func TestHealthWiringPerCompositionArm(t *testing.T) {
	markReadinessLeg(t, "TestHealthWiringPerCompositionArm")
	for _, arm := range []struct {
		file, funcName       string
		coreRecv, coreMethod string
	}{
		{file: "runtime.go", funcName: "composePostgreSQLFullRuntime", coreRecv: "runtime", coreMethod: "Readiness"},
		{file: "server.go", funcName: "composePostgreSQLFirstQueryRoute", coreRecv: "runtime", coreMethod: "Readiness"},
	} {
		arm := arm
		t.Run(arm.funcName, func(t *testing.T) {
			lit := httpConfigLiteral(t, arm.file, arm.funcName)
			core, ok := literalField(lit, "CoreReadyCheck")
			if !ok {
				t.Fatal("CoreReadyCheck missing")
			}
			if !callsSelector(core, arm.coreRecv, arm.coreMethod) {
				t.Fatalf("CoreReadyCheck must call %s.%s (the full probe with the ledger check); a narrower hook silently regresses schema-awareness", arm.coreRecv, arm.coreMethod)
			}
			state, ok := literalField(lit, "CoreState")
			if !ok {
				t.Fatal("CoreState classifier missing on the PostgreSQL arm")
			}
			if ident, ok := state.(*ast.Ident); !ok || ident.Name != "postgreSQLCoreReadinessState" {
				t.Fatalf("CoreState must be postgreSQLCoreReadinessState, got %#v", state)
			}
		})
	}

	t.Run("ArangoArm_unchanged_and_without_classifier", func(t *testing.T) {
		lit := httpConfigLiteral(t, "server.go", "run")
		for _, key := range []string{"CoreReadyCheck", "ClickHouseReadyCheck", "ClickHouseEnabled"} {
			if _, ok := literalField(lit, key); !ok {
				t.Fatalf("Arango arm lost its published field %s", key)
			}
		}
		if _, ok := literalField(lit, "CoreState"); ok {
			t.Fatal("the Arango arm must not gain a CoreState classifier: its published bodies stay byte-verbatim")
		}
		core, _ := literalField(lit, "CoreReadyCheck")
		if !callsSelector(core, "lifecycleClient", "QueryRows") {
			t.Fatal("the Arango arm's core probe closure changed; this ticket is additive-only for other backends' arms")
		}
	})
}

// L6 offline classification cell: the token vocabulary is closed and
// complete over the backend's sentinel taxonomy, and a cause whose text
// carries connection material can only ever reach clients as a token.
func TestPostgreSQLCoreReadinessStateClassification(t *testing.T) {
	markReadinessLeg(t, "TestPostgreSQLCoreReadinessStateClassification")
	for _, tt := range []struct {
		name string
		err  error
		want string
	}{
		{"closed", fmt.Errorf("postgres backend: %w", backendspostgres.ErrRuntimeClosed), pgReadinessStateClosed},
		{"connectivity", fmt.Errorf("postgres backend: schema readiness for series %q: %w", "source", backendspostgres.ErrConnect), pgReadinessStateConnectivityDown},
		{"version", fmt.Errorf("ping: %w", backendspostgres.ErrUnsupportedVersion), pgReadinessStateUnsupportedVer},
		{"schema missing", fmt.Errorf("verify: %w: schema \"x\" does not exist", backendspostgres.ErrInvalidSchemaName), pgReadinessStateSchemaMissing},
		{"unmigrated", fmt.Errorf("verify: %w: 7 pending migration(s), first version 1 (ledger)", backendspostgres.ErrMigrationPending), pgReadinessStateSchemaUnmigrated},
		{"checksum", fmt.Errorf("verify: %w", backendspostgres.ErrMigrationChecksumMismatch), pgReadinessStateSchemaMismatch},
		{"future", fmt.Errorf("verify: %w", backendspostgres.ErrFutureSchemaVersion), pgReadinessStateSchemaMismatch},
		{"vanished", fmt.Errorf("verify: %w", backendspostgres.ErrMigrationMissing), pgReadinessStateSchemaMismatch},
		{"out of order", fmt.Errorf("verify: %w", backendspostgres.ErrMigrationOutOfOrder), pgReadinessStateSchemaMismatch},
		{"unknown", errors.New("some other fault"), pgReadinessStateUnknown},
	} {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if got := postgreSQLCoreReadinessState(tt.err); got != tt.want {
				t.Fatalf("postgreSQLCoreReadinessState(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
	if got := postgreSQLCoreReadinessState(nil); got != "" {
		t.Fatalf("a healthy check must classify to the empty token, got %q", got)
	}
	// Credential discipline: the classifier reads only sentinel identities.
	credErr := fmt.Errorf(`acquire: dial tcp postgres://svc:hunter2@db.internal:5432/loom: %w`, backendspostgres.ErrConnect)
	if token := postgreSQLCoreReadinessState(credErr); token != pgReadinessStateConnectivityDown || strings.Contains(token, "hunter2") {
		t.Fatalf("classifier must report the fixed token only, got %q", token)
	}
	// The vocabulary is closed: every produced token is one of the pinned
	// constants, so no raw error text can ever become a body value.
	published := map[string]bool{
		pgReadinessStateClosed: true, pgReadinessStateConnectivityDown: true,
		pgReadinessStateUnsupportedVer: true, pgReadinessStateSchemaMissing: true,
		pgReadinessStateSchemaUnmigrated: true, pgReadinessStateSchemaMismatch: true,
		pgReadinessStateUnknown: true, "": true,
	}
	for _, err := range []error{nil, errors.New("x"), backendspostgres.ErrConnect, backendspostgres.ErrMigrationPending} {
		if !published[postgreSQLCoreReadinessState(err)] {
			t.Fatalf("classifier produced an unpublishable token %q", postgreSQLCoreReadinessState(err))
		}
	}
}
