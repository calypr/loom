package server

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
)

func testAQLView(t *testing.T, command command.CompiledCommand) (string, map[string]any) {
	t.Helper()
	query, bindVars, err := command.AQLParameters()
	if err != nil {
		t.Fatalf("derive AQL parameters from %s command: %v", command.Backend(), err)
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

func testAQLText(t *testing.T, command command.CompiledCommand) string {
	t.Helper()
	query, _ := testAQLView(t, command)
	return query
}

func testAQLBinds(t *testing.T, command command.CompiledCommand) map[string]any {
	t.Helper()
	_, bindVars := testAQLView(t, command)
	return bindVars
}
