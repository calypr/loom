package compiler

import (
	"strings"
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	store "github.com/calypr/loom/internal/store/arango"
)

// requireStableRuntimeCode asserts that a query executed through the live
// Arango adapter failed with the expected stable physical-plan ASSERT code.
// The adapter translates recognized ASSERT codes into typed Loom errors with
// public messages (code read via dataframeerrors.AsUserError); codes outside
// the adapter's recognized set still surface verbatim from the driver. The
// returned string is the adapter's diagnostic-cause rendering, which keeps
// the vendor message with per-row detail for disclosure assertions without
// string-matching the public error.
func requireStableRuntimeCode(t *testing.T, err error, code string) string {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want stable code %s", code)
	}
	if userErr, ok := dataframeerrors.AsUserError(err); ok {
		if userErr.Code() != code {
			t.Fatalf("error = %v carries code %q, want stable code %s", err, userErr.Code(), code)
		}
	} else if !strings.Contains(err.Error(), code) {
		t.Fatalf("error = %v, want stable code %s", err, code)
	}
	if cause, ok := store.DiagnosticCause(err); ok {
		return cause
	}
	return err.Error()
}

// testAQLView derives the AQL diagnostic view of a compiled command for
// compiler tests through closed dispatch; a non-AQL-tagged command fails the
// test instead of being silently treated as AQL.
func testAQLView(t *testing.T, command CompiledCommand) (string, map[string]any) {
	t.Helper()
	query, bindVars, err := command.AQLParameters()
	if err != nil {
		t.Fatalf("dispatch compiled command to AQL view: %v", err)
	}
	return query, bindVars
}

// testAQLCommand builds an AQL-tagged command for compiler test fixtures.
func testAQLCommand(query string, bindVars map[string]any) CompiledCommand {
	command, err := NewAQLCommand(query, bindVars)
	if err != nil {
		panic(err)
	}
	return command
}

func testAQLText(t *testing.T, command CompiledCommand) string {
	t.Helper()
	query, _ := testAQLView(t, command)
	return query
}

func testAQLBinds(t *testing.T, command CompiledCommand) map[string]any {
	t.Helper()
	_, bindVars := testAQLView(t, command)
	return bindVars
}
