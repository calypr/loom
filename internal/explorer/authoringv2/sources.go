package authoringv2

import (
	"fmt"
	"reflect"
)

func applyUpdateImportedSourceStepVariants(workspace *Workspace, catalog CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	position := documentIndex(workspace, command.OutputID)
	if position < 0 || command.resolvedImportedDocument == nil {
		return result, fmt.Errorf("source change requires a lifecycle-resolved imported document")
	}
	current := workspace.Documents[position]
	resolved := *command.resolvedImportedDocument
	if current.ImportedRecipe == nil || resolved.ImportedRecipe == nil || *current.ImportedRecipe != *resolved.ImportedRecipe || resolved.Output.ID != current.Output.ID {
		return result, fmt.Errorf("source change does not match its pinned output")
	}
	if current.RootResourceType != resolved.RootResourceType || !reflect.DeepEqual(current.Output, resolved.Output) || !reflect.DeepEqual(current.Population, resolved.Population) || !reflect.DeepEqual(current.Frames, resolved.Frames) || !reflect.DeepEqual(current.Construction, resolved.Construction) || !reflect.DeepEqual(current.ArchiveRetiredShape, resolved.ArchiveRetiredShape) || !reflect.DeepEqual(current.FixedFilters, resolved.FixedFilters) || !reflect.DeepEqual(current.Actions, resolved.Actions) {
		return result, fmt.Errorf("source edits must preserve the row root, presentation, population, and all downstream authored work")
	}
	workspace.Documents[position] = resolved
	return CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}, nil
}

func applySetFrameSourceVariants(workspace *Workspace, catalog CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	return applyFrameSourceCommand(workspace, command)
}
