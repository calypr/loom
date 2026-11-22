package authoringv2

import (
	"fmt"
	"reflect"
	"strings"
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

func (c Command) validateUpdateImportedSourceStep() error {
	if !commandFieldsPresent(c.OutputID) || c.OutputID != strings.TrimSpace(c.OutputID) {
		return fmt.Errorf("%s requires an exact outputId", c.Type)
	}
	if c.Type == CommandApplyImportedRelationshipProposal {
		if !commandFieldsPresent(c.ProposalID) {
			return fmt.Errorf("proposalId is required")
		}
		return nil
	}
	if c.Type == CommandUpdateImportedSourceStep {
		if c.SourceStep == nil {
			return fmt.Errorf("sourceStep is required")
		}
		return c.SourceStep.Validate()
	}
	if c.SourceStep != nil {
		return fmt.Errorf("REFRESH_IMPORTED_SOURCE accepts only outputId")
	}
	return nil
}

func (c Command) validateSetFrameSource() error {
	if !commandFieldsPresent(c.OutputID, c.FrameChoiceID) || c.FrameForm == "" {
		return fmt.Errorf("SET_FRAME_SOURCE requires outputId, frameChoiceId, and form")
	}
	if c.OutputID != strings.TrimSpace(c.OutputID) || c.FrameChoiceID != strings.TrimSpace(c.FrameChoiceID) || !validFrameForm(c.FrameForm) ||
		c.FrameID != "" || c.ConstructionChoice != nil || c.Title != "" || c.InitialPresentation != "" {
		return fmt.Errorf("SET_FRAME_SOURCE accepts only exact outputId, frameChoiceId, and supported form")
	}
	return nil
}

func (c Command) validateReplaceFrameSource() error {
	if !commandFieldsPresent(c.OutputID, c.FrameID, c.FrameChoiceID) || c.FrameForm == "" {
		return fmt.Errorf("REPLACE_FRAME_SOURCE requires outputId, frameId, frameChoiceId, and form")
	}
	if c.OutputID != strings.TrimSpace(c.OutputID) || c.FrameID != strings.TrimSpace(c.FrameID) || c.FrameChoiceID != strings.TrimSpace(c.FrameChoiceID) || !validFrameForm(c.FrameForm) ||
		c.ConstructionChoice != nil || c.Title != "" || c.InitialPresentation != "" {
		return fmt.Errorf("REPLACE_FRAME_SOURCE accepts only exact outputId, frameId, frameChoiceId, and supported form")
	}
	return nil
}

func (c Command) validateRemoveFrameSource() error {
	if !commandFieldsPresent(c.OutputID, c.FrameID) || c.OutputID != strings.TrimSpace(c.OutputID) || c.FrameID != strings.TrimSpace(c.FrameID) ||
		c.FrameChoiceID != "" || c.FrameForm != "" || c.ConstructionChoice != nil || c.Title != "" || c.InitialPresentation != "" {
		return fmt.Errorf("REMOVE_FRAME_SOURCE requires exact outputId and frameId only")
	}
	return nil
}
