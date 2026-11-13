package execution

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
)

func aqlView(t *testing.T, command command.CompiledCommand) (string, map[string]any) {
	t.Helper()
	query, bindVars, err := command.AQLParameters()
	if err != nil {
		t.Fatalf("dispatch compiled command to AQL view: %v", err)
	}
	return query, bindVars
}

func mustAQLCommand(query string, bindVars map[string]any) command.CompiledCommand {
	compiledCommand, err := command.NewAQLCommand(query, bindVars)
	if err != nil {
		panic(err)
	}
	return compiledCommand
}

func aqlRows(fake func(context.Context, string, int, map[string]any, func(map[string]any) error) error) SourceRows {
	return func(ctx context.Context, request RowsRequest, visit RowVisitor) error {
		command := request.Command
		batch := request.BatchSize
		query, bindVars, dispatchErr := command.AQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}
		return fake(ctx, query, batch, bindVars, visit)
	}
}

func commandText(t *testing.T, command command.CompiledCommand) string {
	t.Helper()
	query, _ := aqlView(t, command)
	return query
}

func commandBinds(t *testing.T, command command.CompiledCommand) map[string]any {
	t.Helper()
	_, bindVars := aqlView(t, command)
	return bindVars
}
