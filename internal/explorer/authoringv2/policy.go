package authoringv2

import (
	"fmt"
)

type resolvedColumnPolicy struct{ columnID, projectionMode string }

func applyColumnPolicyProposal(workspace *Workspace, catalog CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	if command.resolvedColumnPolicy == nil {
		return result, fmt.Errorf("column policy requires a lifecycle-resolved proposal")
	}
	pos := documentIndex(workspace, command.OutputID)
	if pos < 0 {
		return result, fmt.Errorf("column policy output was not found")
	}
	document := workspace.Documents[pos]
	if document.ImportedRecipe != nil {
		return result, fmt.Errorf("COLUMN_POLICY_IMPORTED_SOURCE_REQUIRED: use the exact imported source step")
	}
	var target *Column
	for i := range document.Columns {
		if document.Columns[i].ColumnID != command.resolvedColumnPolicy.columnID {
			continue
		}
		if target != nil {
			return result, fmt.Errorf("column policy column identity is ambiguous")
		}
		target = &document.Columns[i]
	}
	if target == nil {
		return result, fmt.Errorf("column policy column identity was not found")
	}
	if target.Source.Kind != SourceField || target.Source.Field == nil {
		return result, fmt.Errorf("column policy requires a saved source field")
	}
	source := target.Source
	field := *source.Field
	field.ProjectionMode = command.resolvedColumnPolicy.projectionMode
	source.Field = &field
	return applyCommand(workspace, catalog, commandID, index, Command{Type: CommandUpdateColumnSource, OutputID: command.OutputID, Payload: &ColumnPayload{Column: target.Column, Source: &source}})
}
