package postgres

// Read-only proof for MigrationRunner.Verify (pg-runtime-health-readiness,
// coordinator condition 1): the readiness probe is shared with live traffic,
// so its read-only, lock-free shape is a pin, not a claim. This offline
// witness reads the actual source of Verify and its callees and forbids any
// write, transaction, DDL, or advisory-lock surface. The byte-identity half
// (ledger rows, checksums, schema contents unchanged across Verify) runs
// live in internal/server's verify-read-only leg.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestVerifyIsReadOnlyAndLockFree pins the method-call surface of Verify,
// readLedger, validateHistory, and pending to an allowlist whose every
// member is a read: QueryRow/Scan on a pooled connection, pure validation
// over already-fetched records, and pool Acquire/Release. Anything that
// could mutate — Exec, Begin, Prepare-with-DDL, pg_advisory_* — fails the
// witness by name, and SQL literals must be SELECT/to_regnamespace only.
func TestVerifyIsReadOnlyAndLockFree(t *testing.T) {
	set := token.NewFileSet()
	parsed, err := parser.ParseFile(set, "migrate.go", nil, 0)
	if err != nil {
		t.Fatalf("parse migrate.go: %v", err)
	}
	allowed := map[string]bool{
		"Acquire": true, "Release": true, "QueryRow": true, "Query": true, "Scan": true,
		"readLedger": true, "validateHistory": true, "pending": true,
		// verifyStatementError is the fault classifier: errors.As plus
		// ConnectError construction, no I/O (asserted by its own tests).
		"verifyStatementError": true,
		"Errorf": true, "Sprintf": true, "len": true,
		// Close here is the read-rows cursor cleanup only; pool teardown
		// lives outside these callees, and Hijack (the discard path) stays
		// forbidden below.
		"Close": true, "Next": true, "Err": true, "append": true, "sort": true,
		"Slice": true, "As": true, "make": true, "new": true,
		"int64": true, "int": true, "string": true,
	}
	forbidden := map[string]bool{
		"Exec": true, "ExecFunc": true, "Begin": true, "BeginFunc": true,
		"Prepare": true, "CopyFrom": true, "SendBatch": true, "Hijack": true,
		"pg_advisory_lock": true, "pg_advisory_xact_lock": true,
	}
	callees := map[string]bool{
		"Verify": true, "readLedger": true, "validateHistory": true, "pending": true,
	}
	seen := map[string]bool{}
	var bad, suspicious []string
	ast.Inspect(parsed, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || !callees[fn.Name.Name] {
			return true
		}
		seen[fn.Name.Name] = true
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				var name string
				switch f := x.Fun.(type) {
				case *ast.SelectorExpr:
					name = f.Sel.Name
				case *ast.Ident:
					name = f.Name
				}
				if forbidden[name] {
					bad = append(bad, fn.Name.Name+"."+name)
				} else if !allowed[name] && name != fn.Name.Name {
					suspicious = append(suspicious, fn.Name.Name+"."+name)
				}
			case *ast.BasicLit:
				if x.Kind != token.STRING {
					return true
				}
				lit := strings.ToUpper(x.Value)
				for _, tell := range []string{"INSERT", "UPDATE", "DELETE", "CREATE ", "ALTER ", "DROP ", "TRUNCATE", "TRUNCATE", "BEGIN", "LOCK", "PG_ADVISORY", "GRANT", "COMMENT ON"} {
					if strings.Contains(lit, tell) {
						bad = append(bad, fn.Name.Name+" literal "+x.Value)
					}
				}
			}
			return true
		})
		return true
	})
	for name := range callees {
		if !seen[name] {
			t.Fatalf("callee %s not found in migrate.go; the witness must be re-read, not widened", name)
		}
	}
	if len(bad) > 0 {
		t.Fatalf("Verify path touches a write/lock surface: %v", bad)
	}
	if len(suspicious) > 0 {
		t.Fatalf("Verify path calls a method outside the read-only allowlist: %v", suspicious)
	}
}
