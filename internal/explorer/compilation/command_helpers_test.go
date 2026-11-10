package compilation

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
)

// testAQLView derives the AQL diagnostic view of a compiled command for tests.
// A dispatch failure is a test bug, not a runtime condition.
func testAQLView(t *testing.T, command compiler.CompiledCommand) (string, map[string]any) {
	t.Helper()
	query, bindVars, err := command.AQLParameters()
	if err != nil {
		t.Fatalf("derive AQL parameters from %s command: %v", command.Backend(), err)
	}
	return query, bindVars
}

func testAQLCommand(t *testing.T, query string, bindVars map[string]any) compiler.CompiledCommand {
	t.Helper()
	command, err := compiler.NewAQLCommand(query, bindVars)
	if err != nil {
		t.Fatalf("build AQL command for test: %v", err)
	}
	return command
}

func testAQLText(t *testing.T, command compiler.CompiledCommand) string {
	t.Helper()
	query, _ := testAQLView(t, command)
	return query
}

func testAQLBinds(t *testing.T, command compiler.CompiledCommand) map[string]any {
	t.Helper()
	_, bindVars := testAQLView(t, command)
	return bindVars
}
