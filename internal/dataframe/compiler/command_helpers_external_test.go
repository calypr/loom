package compiler_test

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
)

// testAQLView derives the AQL diagnostic view of a compiled command for
// external-package tests. A dispatch failure is a test bug, not a runtime
// condition, so it fails the test immediately.
func testAQLView(t *testing.T, compiledCommand command.CompiledCommand) (string, map[string]any) {
	t.Helper()
	query, bindVars, err := compiledCommand.AQLParameters()
	if err != nil {
		t.Fatalf("derive AQL parameters from %s command: %v", compiledCommand.Backend(), err)
	}
	return query, bindVars
}

func testAQLCommand(t *testing.T, query string, bindVars map[string]any) command.CompiledCommand {
	t.Helper()
	compiledCommand, err := command.NewAQLCommand(query, bindVars)
	if err != nil {
		t.Fatalf("build AQL command for test: %v", err)
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
