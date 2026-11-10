package compiler

import "testing"

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
