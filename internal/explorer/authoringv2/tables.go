package authoringv2

import (
	"fmt"
	"sort"
	"strings"

	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func applyCreateTable(workspace *ws.Workspace, catalog ws.CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	node, ok := catalogNode(catalog, command.RootNodeID())
	if !ok || !node.RowRootEligible {
		return result, fmt.Errorf("rootNodeId %q is not an eligible catalog root", command.RootNodeID())
	}
	outputID := commandGeneratedID("out_", commandID, index, command.Type)
	tabID := commandGeneratedID("tab_", commandID, index, command.Type)
	for _, document := range workspace.Documents {
		if document.Output.ID == outputID {
			return CommandResult{Type: CommandResultTableCreated, OutputID: outputID, TabID: tabID}, nil
		}
	}
	title := strings.TrimSpace(command.Title)
	workspace.Documents = append(workspace.Documents, ws.Document{Kind: ws.Kind, Output: ws.Output{ID: outputID, Title: title}, RootResourceType: node.ResourceType, Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: node.ResourceType}, Rows: ws.RecordsRowDefinition(), Columns: []ws.Column{}})
	workspace.Tabs = append(workspace.Tabs, ws.Tab{ID: tabID, Title: title, OutputID: outputID, Order: len(workspace.Tabs), Visible: true})
	return CommandResult{Type: CommandResultTableCreated, OutputID: outputID, TabID: tabID, OccurrenceID: ws.RootOccurrenceID}, nil
}

func applyDuplicateTable(workspace *ws.Workspace, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	sourceIndex := documentIndex(workspace, command.SourceOutputID())
	if sourceIndex < 0 {
		return result, fmt.Errorf("source output %q was not found", command.SourceOutputID())
	}
	outputID := commandGeneratedID("out_", commandID, index, command.Type)
	tabID := commandGeneratedID("tab_", commandID, index, command.Type)
	copy := workspace.Documents[sourceIndex]
	copy.Output.ID, copy.Output.Title = outputID, strings.TrimSpace(command.Title)
	workspace.Documents = append(workspace.Documents, copy)
	workspace.Tabs = append(workspace.Tabs, ws.Tab{ID: tabID, Title: copy.Output.Title, OutputID: outputID, Order: len(workspace.Tabs), Visible: true})
	return CommandResult{Type: CommandResultTableCreated, OutputID: outputID, TabID: tabID, OccurrenceID: ws.RootOccurrenceID}, nil
}

func applyDeleteTable(workspace *ws.Workspace, command Command) (CommandResult, error) {
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

func applyRenameTable(workspace *ws.Workspace, command Command) (CommandResult, error) {
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

func applyReorderTables(workspace *ws.Workspace, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	if len(command.OutputIDs()) != len(workspace.Documents) {
		return result, fmt.Errorf("outputIds must list every table exactly once")
	}
	positions := map[string]int{}
	seen := map[string]bool{}
	for order, outputID := range command.OutputIDs() {
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

func applySetTableRoot(workspace *ws.Workspace, catalog ws.CatalogSnapshot, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	document := documentIndex(workspace, command.OutputID)
	node, ok := catalogNode(catalog, command.RootNodeID())
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
	current.Route = ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: node.ResourceType}
	return result, nil
}

func applyApplyTableRootRebase(workspace *ws.Workspace, catalog ws.CatalogSnapshot, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	proposal := *command.RowChange()
	if proposal.Mode == RowChangeModeReplace {
		replaced, err := ApplyRowReplacement(*workspace, catalog, proposal)
		if err != nil {
			return result, err
		}
		*workspace = replaced
		result.OutputID = proposal.OutputID
		result.OccurrenceID = ws.RootOccurrenceID
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
	result.OccurrenceID = ws.RootOccurrenceID
	return result, nil
}

func applySetTablePopulation(workspace *ws.Workspace, catalog ws.CatalogSnapshot, command Command, resolution CommandResolution) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	document := documentIndex(workspace, command.OutputID)
	if document < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	if command.RouteChoiceID() != "" {
		populationRoute, ok := resolution.(PopulationRouteResolution)
		if !ok || populationRoute.Route == nil {
			return result, fmt.Errorf("population route choice has no server resolution")
		}
		setPopulationRoute(&workspace.Documents[document], command.SelectionRevisionID(), populationRoute.Route)
	} else if err := setPopulation(&workspace.Documents[document], catalog, command.SelectionRevisionID(), command.EdgeIDs()); err != nil {
		return result, err
	}
	return result, nil
}

func applyClearTablePopulation(workspace *ws.Workspace, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	document := documentIndex(workspace, command.OutputID)
	if document < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	workspace.Documents[document].Population = nil
	return result, nil
}
