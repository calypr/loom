package authoringv2

import (
	"fmt"
	"strings"
)

func applyTableRootRebaseCommand(workspace *Workspace, catalog CatalogSnapshot, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	proposal := *command.RowChange
	if proposal.Mode == RowChangeModeReplace {
		replaced, err := ApplyRowReplacement(*workspace, catalog, proposal)
		if err != nil {
			return result, err
		}
		*workspace = replaced
		result.OutputID = proposal.OutputID
		result.OccurrenceID = RootOccurrenceID
		return result, nil
	}
	document := documentIndex(workspace, proposal.OutputID)
	if document < 0 {
		return result, fmt.Errorf("output %q was not found", proposal.OutputID)
	}
	rebased, err := ApplyRowChange(workspace.Documents[document], catalog, proposal)
	if err != nil {
		return result, err
	}
	workspace.Documents[document] = rebased
	result.OutputID = proposal.OutputID
	result.OccurrenceID = RootOccurrenceID
	return result, nil
}

func applyConstructionChoiceCommand(workspace *Workspace, catalog CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	resolved := command.ResolvedChoice
	if resolved == nil || strings.TrimSpace(resolved.CandidateID) == "" || strings.TrimSpace(resolved.LogicalType) == "" {
		return result, fmt.Errorf("construction choice has no server-resolved source")
	}
	presentation := strings.ToUpper(strings.TrimSpace(command.InitialPresentation))
	if presentation == "" {
		presentation = InitialPresentationTable
	}
	documentPos := documentIndex(workspace, command.OutputID)
	if documentPos < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	document := workspace.Documents[documentPos]
	if document.Route.OccurrenceID != RootOccurrenceID || document.Route.ResourceType != document.RootResourceType {
		return result, fmt.Errorf("table root route is invalid")
	}
	targetResourceType := document.RootResourceType
	if len(resolved.Route) > 0 {
		targetResourceType = resolved.Route[len(resolved.Route)-1].ToResourceType
	}
	if presentation != InitialPresentationTable {
		candidate, ok := catalogCandidate(catalog, resolved.CandidateID)
		if !ok {
			return result, fmt.Errorf("construction choice candidate %q was not found", resolved.CandidateID)
		}
		node, ok := catalogNode(catalog, candidate.NodeID)
		if !ok {
			return result, fmt.Errorf("construction choice candidate %q has no catalog node", resolved.CandidateID)
		}
		if node.ResourceType != targetResourceType || len(resolved.Route) > 0 && node.ID != resolved.Route[len(resolved.Route)-1].ToNodeID {
			return result, fmt.Errorf("construction choice candidate %q does not match its exact terminal route", resolved.CandidateID)
		}
		if presentation == InitialPresentationFilter && !candidate.Filterable {
			return result, fmt.Errorf("construction choice candidate %q does not support filters", resolved.CandidateID)
		}
		if presentation == InitialPresentationChart && !candidate.Chartable {
			return result, fmt.Errorf("construction choice candidate %q does not support charts", resolved.CandidateID)
		}
	}
	occurrenceID, routeErr := ensureConstructionRoute(&workspace.Documents[documentPos], catalog, commandID, index, resolved.Route, resolved.CandidateID)
	if routeErr != nil {
		return result, routeErr
	}
	if err := initializeEmptyConstructionBeforeSourceAdd(workspace, command.OutputID); err != nil {
		return result, err
	}
	command.OccurrenceID = occurrenceID
	applied, err := applyColumnSource(workspace, catalog, commandID, index, command, resolved.Source, resolved.LogicalType, presentation)
	if err != nil {
		return result, err
	}
	if resolved.FrameID != "" {
		if presentation != InitialPresentationTable {
			return result, fmt.Errorf("frame categories can only be added as table columns")
		}
		columns := workspace.Documents[documentPos].Columns
		found := false
		for columnIndex := range columns {
			if columns[columnIndex].Column == applied.Column {
				columns[columnIndex].FrameID = resolved.FrameID
				found = true
				break
			}
		}
		if !found {
			return result, fmt.Errorf("applied frame category column %q was not found", applied.Column)
		}
	}
	return applied, nil
}

func applyInterpretationCandidateCommand(workspace *Workspace, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	if strings.TrimSpace(command.OutputID) == "" || strings.TrimSpace(command.Column) == "" ||
		command.InterpretationCandidate == nil ||
		strings.TrimSpace(command.InterpretationCandidate.CandidateReceiptID) == "" ||
		strings.TrimSpace(command.InterpretationCandidate.RevisionID) == "" ||
		command.InterpretationCandidate.CandidateReceiptID != strings.TrimSpace(command.InterpretationCandidate.CandidateReceiptID) ||
		command.InterpretationCandidate.RevisionID != strings.TrimSpace(command.InterpretationCandidate.RevisionID) {
		return result, fmt.Errorf("APPLY_INTERPRETATION_CANDIDATE requires outputId, column, and exact interpretationCandidate payload")
	}
	document := documentIndex(workspace, command.OutputID)
	if document < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	for index := range workspace.Documents[document].Columns {
		current := &workspace.Documents[document].Columns[index]
		if current.Column != command.Column {
			continue
		}
		revisionID := command.InterpretationCandidate.RevisionID
		current.Interpretation = &FeatureInterpretation{
			Kind:   FeatureInterpretationPinned,
			Pinned: &PinnedInterpretation{RevisionID: revisionID},
		}
		current.Contributor = nil
		return CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID, Column: current.Column}, nil
	}
	return result, fmt.Errorf("column %q was not found", command.Column)
}

func applyRowDefinitionProposalCommand(workspace *Workspace, command Command) (CommandResult, error) {
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
	workspace.Documents[document].Rows = rows
	return result, nil
}

func applyTableShapeProposalCommand(workspace *Workspace, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	if !command.resolvedTableShapeSet {
		return result, fmt.Errorf("APPLY_TABLE_SHAPE_PROPOSAL has no lifecycle-resolved table shape")
	}
	document := documentIndex(workspace, command.OutputID)
	if document < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	shape, err := cloneTableShape(command.resolvedTableShape)
	if err != nil {
		return result, err
	}
	workspace.Documents[document].TableShape = shape
	return CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}, nil
}

func applyPopulationMemberProposalCommand(workspace *Workspace, command Command) (CommandResult, error) {
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

func applyConstructionProposalCommand(workspace *Workspace, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	if !command.resolvedConstructionSet || command.resolvedConstruction == nil || command.resolvedConstructionRows == nil {
		return result, fmt.Errorf("APPLY_CONSTRUCTION_PROPOSAL has no lifecycle-resolved construction")
	}
	documentIndexValue := documentIndex(workspace, command.OutputID)
	if documentIndexValue < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	document, err := UpgradeDocumentToConstruction(workspace.Documents[documentIndexValue])
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
	document.TableShape = nil
	if err := document.Validate(); err != nil {
		return result, fmt.Errorf("resolved construction is invalid: %w", err)
	}
	workspace.Documents[documentIndexValue] = document
	return CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}, nil
}

func applyRestoreDraftRevisionCommand(workspace *Workspace, command Command) (CommandResult, error) {
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
