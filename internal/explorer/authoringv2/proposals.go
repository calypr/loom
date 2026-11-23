package authoringv2

import (
	"fmt"
	"strings"
)

func applyApplyColumnPolicyProposal(workspace *Workspace, catalog CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	return applyColumnPolicyProposal(workspace, catalog, commandID, index, command)
}

func applyApplyInterpretationCandidate(workspace *Workspace, catalog CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	if strings.TrimSpace(command.OutputID) == "" || strings.TrimSpace(command.Column()) == "" ||
		command.InterpretationCandidate() == nil ||
		strings.TrimSpace(command.InterpretationCandidate().CandidateReceiptID) == "" ||
		strings.TrimSpace(command.InterpretationCandidate().RevisionID) == "" ||
		command.InterpretationCandidate().CandidateReceiptID != strings.TrimSpace(command.InterpretationCandidate().CandidateReceiptID) ||
		command.InterpretationCandidate().RevisionID != strings.TrimSpace(command.InterpretationCandidate().RevisionID) {
		return result, fmt.Errorf("APPLY_INTERPRETATION_CANDIDATE requires outputId, column, and exact interpretationCandidate payload")
	}
	document := documentIndex(workspace, command.OutputID)
	if document < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	for index := range workspace.Documents[document].Columns {
		current := &workspace.Documents[document].Columns[index]
		if current.Column != command.Column() {
			continue
		}
		revisionID := command.InterpretationCandidate().RevisionID
		current.Interpretation = &FeatureInterpretation{
			Kind:   FeatureInterpretationPinned,
			Pinned: &PinnedInterpretation{RevisionID: revisionID},
		}

		current.Contributor = nil
		return CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID, Column: current.Column}, nil
	}
	return result, fmt.Errorf("column %q was not found", command.Column())
}

func applyApplyRowDefinitionProposal(workspace *Workspace, catalog CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	if command.resolvedRowDefinition == nil {
		return result, fmt.Errorf("APPLY_ROW_DEFINITION_PROPOSAL has no lifecycle-resolved row definition")
	}
	rows, err := cloneRowDefinition(*command.resolvedRowDefinition)
	if err != nil {
		return result, err
	}
	document := documentIndex(workspace, command.OutputID)
	if document < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	if workspace.Documents[document].ImportedRecipe != nil {
		return result, fmt.Errorf("IMPORTED_SOURCE_ROWS_REQUIRED: imported source rows are derived from the immutable pin and source overlay; choose a construction row operation")
	}
	workspace.Documents[document].Rows = rows
	return result, nil
}

func applyApplyPopulationMemberProposal(workspace *Workspace, catalog CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	if !command.resolvedPopulationSet || command.resolvedPopulation == nil {
		return result, fmt.Errorf("APPLY_POPULATION_MEMBER_PROPOSAL has no lifecycle-resolved population")
	}
	documentPos := documentIndex(workspace, command.OutputID)
	if documentPos < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	population := *command.resolvedPopulation
	population.Route = clonePopulationRouteSteps(command.resolvedPopulation.Route)
	if err := population.Validate(); err != nil {
		return result, fmt.Errorf("resolved population is invalid: %w", err)
	}
	workspace.Documents[documentPos].Population = &population
	return CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}, nil
}

func applyApplyConstructionProposal(workspace *Workspace, catalog CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	if !command.resolvedConstructionSet || command.resolvedConstruction == nil || command.resolvedConstructionRows == nil {
		return result, fmt.Errorf("APPLY_CONSTRUCTION_PROPOSAL has no lifecycle-resolved construction")
	}
	documentIndexValue := documentIndex(workspace, command.OutputID)
	if documentIndexValue < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	document, err := StartDocumentConstruction(workspace.Documents[documentIndexValue])
	if err != nil {
		return result, fmt.Errorf("prepare staged construction: %w", err)
	}
	construction, err := cloneConstruction(command.resolvedConstruction)
	if err != nil {
		return result, err
	}
	document.Construction = construction
	rows, err := cloneRowDefinition(*command.resolvedConstructionRows)
	if err != nil {
		return result, err
	}
	document.Rows = rows
	document.ArchiveRetiredShape = nil
	if err := document.Validate(); err != nil {
		return result, fmt.Errorf("resolved construction is invalid: %w", err)
	}
	workspace.Documents[documentIndexValue] = document
	return CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}, nil
}

func applyRestoreDraftRevision(workspace *Workspace, catalog CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	if command.resolvedDraftRevision == nil {
		return result, fmt.Errorf("RESTORE_DRAFT_REVISION has no lifecycle-resolved draft revision")
	}
	restored, err := cloneWorkspace(*command.resolvedDraftRevision)
	if err != nil {
		return result, err
	}
	*workspace = restored
	return CommandResult{Type: CommandResultDraftRestored}, nil
}
