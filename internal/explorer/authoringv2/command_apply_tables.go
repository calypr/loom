package authoringv2

import (
	"fmt"
	"sort"
	"strings"
)

func applyCreateTable(workspace *Workspace, catalog CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	node, ok := catalogNode(catalog, command.RootNodeID)
	if !ok || !node.RowRootEligible {
		return result, fmt.Errorf("rootNodeId %q is not an eligible catalog root", command.RootNodeID)
	}
	outputID := commandGeneratedID("out_", commandID, index, command.Type)
	tabID := commandGeneratedID("tab_", commandID, index, command.Type)
	for _, document := range workspace.Documents {
		if document.Output.ID == outputID {
			return CommandResult{Type: CommandResultTableCreated, OutputID: outputID, TabID: tabID}, nil
		}
	}
	title := strings.TrimSpace(command.Title)
	workspace.Documents = append(workspace.Documents, Document{Kind: Kind, Output: Output{ID: outputID, Title: title}, RootResourceType: node.ResourceType, Route: RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: node.ResourceType}, Rows: RecordsRowDefinition(), Columns: []Column{}})
	workspace.Tabs = append(workspace.Tabs, Tab{ID: tabID, Title: title, OutputID: outputID, Order: len(workspace.Tabs), Visible: true})
	return CommandResult{Type: CommandResultTableCreated, OutputID: outputID, TabID: tabID, OccurrenceID: RootOccurrenceID}, nil
}

func applyDuplicateTable(workspace *Workspace, catalog CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	sourceIndex := documentIndex(workspace, command.SourceOutputID)
	if sourceIndex < 0 {
		return result, fmt.Errorf("source output %q was not found", command.SourceOutputID)
	}
	outputID := commandGeneratedID("out_", commandID, index, command.Type)
	tabID := commandGeneratedID("tab_", commandID, index, command.Type)
	copy := workspace.Documents[sourceIndex]
	copy.Output.ID, copy.Output.Title = outputID, strings.TrimSpace(command.Title)
	workspace.Documents = append(workspace.Documents, copy)
	workspace.Tabs = append(workspace.Tabs, Tab{ID: tabID, Title: copy.Output.Title, OutputID: outputID, Order: len(workspace.Tabs), Visible: true})
	return CommandResult{Type: CommandResultTableCreated, OutputID: outputID, TabID: tabID, OccurrenceID: RootOccurrenceID}, nil
}

func applyDeleteTable(workspace *Workspace, catalog CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	document := documentIndex(workspace, command.OutputID)
	if document < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	workspace.Documents = append(workspace.Documents[:document], workspace.Documents[document+1:]...)
	workspace.Tabs = removeOutputTab(workspace.Tabs, command.OutputID)
	cleanupWorkspaceBindings(workspace)
	return result, nil
}

func applyRenameTable(workspace *Workspace, catalog CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	document := documentIndex(workspace, command.OutputID)
	if document < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	title := strings.TrimSpace(command.Title)
	workspace.Documents[document].Output.Title = title
	for i := range workspace.Tabs {
		if workspace.Tabs[i].OutputID == command.OutputID {
			workspace.Tabs[i].Title = title
		}
	}
	return result, nil
}

func applyReorderTables(workspace *Workspace, catalog CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	if len(command.OutputIDs) != len(workspace.Documents) {
		return result, fmt.Errorf("outputIds must list every table exactly once")
	}
	positions := map[string]int{}
	seen := map[string]bool{}
	for order, outputID := range command.OutputIDs {
		if documentIndex(workspace, outputID) < 0 || seen[outputID] {
			return result, fmt.Errorf("outputIds contains an unknown or duplicate output")
		}
		seen[outputID] = true
		positions[outputID] = order + 1
	}
	sort.SliceStable(workspace.Tabs, func(i, j int) bool {
		return positions[workspace.Tabs[i].OutputID] < positions[workspace.Tabs[j].OutputID]
	})
	for order := range workspace.Tabs {
		workspace.Tabs[order].Order = order
	}
	return result, nil
}

func applySetTableRoot(workspace *Workspace, catalog CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	document := documentIndex(workspace, command.OutputID)
	node, ok := catalogNode(catalog, command.RootNodeID)
	if document < 0 || !ok || !node.RowRootEligible {
		return result, fmt.Errorf("output or eligible root node was not found")
	}
	current := &workspace.Documents[document]
	if current.RootResourceType == node.ResourceType {
		return result, nil
	}
	if current.RootResourceType != node.ResourceType && documentHasConfiguredMeaning(*current) {
		return result, fmt.Errorf("ROOT_REBASE_REQUIRED: changing table root would discard configured routes, columns, filters, actions, or population")
	}
	current.RootResourceType = node.ResourceType
	current.Route = RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: node.ResourceType}
	return result, nil
}

func applyApplyTableRootRebase(workspace *Workspace, catalog CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
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

func applySetTablePopulation(workspace *Workspace, catalog CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	document := documentIndex(workspace, command.OutputID)
	if document < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	if command.RouteChoiceID != "" {
		if command.ResolvedPopulationRoute == nil {
			return result, fmt.Errorf("population route choice has no server resolution")
		}
		setPopulationRoute(&workspace.Documents[document], command.SelectionRevisionID, command.ResolvedPopulationRoute)
	} else if err := setPopulation(&workspace.Documents[document], catalog, command.SelectionRevisionID, command.EdgeIDs); err != nil {
		return result, err
	}
	return result, nil
}

func applyClearTablePopulation(workspace *Workspace, catalog CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	document := documentIndex(workspace, command.OutputID)
	if document < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	workspace.Documents[document].Population = nil
	return result, nil
}

func (c Command) validateCreateTable() error {
	if !commandFieldsPresent(c.Title, c.RootNodeID) {
		return fmt.Errorf("CREATE_TABLE requires title and rootNodeId")
	}
	return nil
}

func (c Command) validateDuplicateTable() error {
	if !commandFieldsPresent(c.SourceOutputID, c.Title) {
		return fmt.Errorf("DUPLICATE_TABLE requires sourceOutputId and title")
	}
	return nil
}

func (c Command) validateDeleteTable() error {
	if !commandFieldsPresent(c.OutputID) {
		return fmt.Errorf("DELETE_TABLE requires outputId")
	}
	return nil
}

func (c Command) validateRenameTable() error {
	if !commandFieldsPresent(c.OutputID, c.Title) {
		return fmt.Errorf("RENAME_TABLE requires outputId and title")
	}
	return nil
}

func (c Command) validateReorderTables() error {
	if len(c.OutputIDs) == 0 {
		return fmt.Errorf("REORDER_TABLES requires outputIds")
	}
	return nil
}

func (c Command) validateSetTableRoot() error {
	if !commandFieldsPresent(c.OutputID, c.RootNodeID) {
		return fmt.Errorf("SET_TABLE_ROOT requires outputId and rootNodeId")
	}
	return nil
}

func (c Command) validateApplyTableRootRebase() error {
	if c.RowChange == nil {
		return fmt.Errorf("APPLY_TABLE_ROOT_REBASE requires rowChange")
	}
	if err := c.RowChange.validate(); err != nil {
		return err
	}
	if c.OutputID != "" && c.OutputID != c.RowChange.OutputID {
		return fmt.Errorf("APPLY_TABLE_ROOT_REBASE outputId must match rowChange.outputId")
	}
	return nil
}

func (c Command) validateSetTablePopulation() error {
	if !commandFieldsPresent(c.OutputID, c.SelectionRevisionID) {
		return fmt.Errorf("SET_TABLE_POPULATION requires outputId and selectionRevisionId")
	}
	if strings.TrimSpace(c.RouteChoiceID) != "" && len(c.EdgeIDs) != 0 {
		return fmt.Errorf("SET_TABLE_POPULATION cannot combine routeChoiceId with client edgeIds")
	}
	return nil
}

func (c Command) validateClearTablePopulation() error {
	if !commandFieldsPresent(c.OutputID) {
		return fmt.Errorf("CLEAR_TABLE_POPULATION requires outputId")
	}
	return nil
}
