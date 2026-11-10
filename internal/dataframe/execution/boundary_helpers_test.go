package execution

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
)

// aqlView derives the AQL diagnostic view of a command for test assertions
// through closed dispatch; a non-AQL-tagged command fails the test instead of
// being silently treated as AQL.
func aqlView(t *testing.T, command compiler.CompiledCommand) (string, map[string]any) {
	t.Helper()
	query, bindVars, err := command.AQLParameters()
	if err != nil {
		t.Fatalf("dispatch compiled command to AQL view: %v", err)
	}
	return query, bindVars
}

// mustAQLCommand builds an AQL-tagged command for tests exercising the typed
// source boundary. It panics on an invalid command so a broken fixture fails
// loudly at construction instead of silently executing nothing.
func mustAQLCommand(query string, bindVars map[string]any) compiler.CompiledCommand {
	command, err := compiler.NewAQLCommand(query, bindVars)
	if err != nil {
		panic(err)
	}
	return command
}

// aqlRows adapts a loose legacy test fake (query text, batch, bind map,
// visitor) into the typed AQL QueryRows contract. The fake never sees a
// command it cannot interpret: dispatch through AQLParameters rejects a
// non-AQL-tagged command before the fake runs.
func aqlRows(fake func(context.Context, string, int, map[string]any, func(map[string]any) error) error) QueryRows {
	return func(ctx context.Context, command compiler.CompiledCommand, batch int, visit RowVisitor) error {
		query, bindVars, dispatchErr := command.AQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}
		return fake(ctx, query, batch, bindVars, visit)
	}
}

// commandText and commandBinds derive single diagnostic views of a command.
func commandText(t *testing.T, command compiler.CompiledCommand) string {
	t.Helper()
	query, _ := aqlView(t, command)
	return query
}

func commandBinds(t *testing.T, command compiler.CompiledCommand) map[string]any {
	t.Helper()
	_, bindVars := aqlView(t, command)
	return bindVars
}
