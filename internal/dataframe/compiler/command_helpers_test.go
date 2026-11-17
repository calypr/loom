package compiler

import (
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"strings"
	"testing"

	arangofactory "github.com/calypr/loom/internal/backends/arango"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

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
	if cause, ok := arangofactory.DiagnosticCause(err); ok {
		return cause
	}
	return err.Error()
}

func testAQLView(t *testing.T, compiledCommand command.CompiledCommand) (string, map[string]any) {
	t.Helper()
	query, bindVars, err := compiledCommand.AQLParameters()
	if err != nil {
		t.Fatalf("dispatch compiled command to AQL view: %v", err)
	}
	return query, bindVars
}

func testAQLCommand(query string, bindVars map[string]any) command.CompiledCommand {
	compiledCommand, err := command.NewAQLCommand(query, bindVars)
	if err != nil {
		panic(err)
	}
	return compiledCommand
}

func testAQLText(t *testing.T, compiledCommand command.CompiledCommand) string {
	t.Helper()
	query, _ := testAQLView(t, compiledCommand)
	return query
}

func testAQLBinds(t *testing.T, compiledCommand command.CompiledCommand) map[string]any {
	t.Helper()
	_, bindVars := testAQLView(t, compiledCommand)
	return bindVars
}
