package authoringv2

import (
	"fmt"
	"strings"

	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func applyApplyColumnPolicyProposal(workspace *ws.Workspace, catalog ws.CatalogSnapshot, commandID string, index int, command Command, resolution CommandResolution) (CommandResult, error) {
	return applyColumnPolicyProposal(workspace, catalog, commandID, index, command, resolution)
}

func applyApplyInterpretationCandidate(workspace *ws.Workspace, command Command) (CommandResult, error) {
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
		current.Interpretation = &ws.FeatureInterpretation{
			Kind:   ws.FeatureInterpretationPinned,
			Pinned: &ws.PinnedInterpretation{RevisionID: revisionID},
		}

		current.Contributor = nil
		return CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID, Column: current.Column}, nil
	}
	return result, fmt.Errorf("column %q was not found", command.Column())
}

func applyApplyRowDefinitionProposal(workspace *ws.Workspace, command Command, resolution CommandResolution) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	resolved, ok := resolution.(RowDefinitionResolution)
	if !ok {
		return result, fmt.Errorf("APPLY_ROW_DEFINITION_PROPOSAL has no lifecycle-resolved row definition")
	}
	rows, err := cloneRowDefinition(resolved.Rows)
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

func applyApplyPopulationMemberProposal(workspace *ws.Workspace, command Command, resolution CommandResolution) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	resolved, ok := resolution.(PopulationResolution)
	if !ok {
		return result, fmt.Errorf("APPLY_POPULATION_MEMBER_PROPOSAL has no lifecycle-resolved population")
	}
	documentPos := documentIndex(workspace, command.OutputID)
	if documentPos < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	population := resolved.Population
	population.Route = ws.ClonePopulationRouteSteps(resolved.Population.Route)
	if err := population.Validate(); err != nil {
		return result, fmt.Errorf("resolved population is invalid: %w", err)
	}
	workspace.Documents[documentPos].Population = &population
	return CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}, nil
}

func applyApplyConstructionProposal(workspace *ws.Workspace, command Command, resolution CommandResolution) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	resolved, ok := resolution.(ConstructionResolution)
	if !ok {
		return result, fmt.Errorf("APPLY_CONSTRUCTION_PROPOSAL has no lifecycle-resolved construction")
	}
	documentIndexValue := documentIndex(workspace, command.OutputID)
	if documentIndexValue < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	document, err := ws.StartDocumentConstruction(workspace.Documents[documentIndexValue])
	if err != nil {
		return result, fmt.Errorf("prepare staged construction: %w", err)
	}
	construction, err := cloneConstruction(resolved.Construction)
	if err != nil {
		return result, err
	}
	document.Construction = construction
	rows, err := cloneRowDefinition(resolved.Rows)
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

func applyRestoreDraftRevision(workspace *ws.Workspace, command Command, resolution CommandResolution) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	resolved, ok := resolution.(DraftRevisionResolution)
	if !ok {
		return result, fmt.Errorf("RESTORE_DRAFT_REVISION has no lifecycle-resolved draft revision")
	}
	restored, err := cloneWorkspace(resolved.Workspace)
	if err != nil {
		return result, err
	}
	*workspace = restored
	return CommandResult{Type: CommandResultDraftRestored}, nil
}
