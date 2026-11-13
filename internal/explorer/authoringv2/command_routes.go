package authoringv2

import (
	"fmt"
)

func applyAddRouteCommand(workspace *Workspace, catalog CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	document := documentIndex(workspace, command.OutputID)
	if document < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	parent := findRoute(&workspace.Documents[document].Route, command.ParentOccurrenceID)
	edge, ok := catalogEdge(catalog, command.EdgeID)
	if parent == nil || !ok {
		return result, fmt.Errorf("parent occurrence or edge was not found")
	}
	parentDepth, depthFound := routeDepth(&workspace.Documents[document].Route, command.ParentOccurrenceID)
	if !depthFound {
		return result, fmt.Errorf("parent occurrence or edge was not found")
	}
	if catalog.RoutePolicy.MaxHops != nil && parentDepth+1 > *catalog.RoutePolicy.MaxHops {
		return result, fmt.Errorf("ROUTE_TOO_LONG: route exceeds capability route policy (maxHops=%d, hops=%d)", *catalog.RoutePolicy.MaxHops, parentDepth+1)
	}
	from, fromOK := catalogNode(catalog, edge.FromNodeID)
	to, toOK := catalogNode(catalog, edge.ToNodeID)
	parentNodeID, parentNodeFound := catalogNodeIDForOccurrence(workspace.Documents[document].Route, parent.OccurrenceID, catalog)
	if !fromOK || !toOK || !parentNodeFound || edge.FromNodeID != parentNodeID || from.ResourceType != parent.ResourceType {
		return result, fmt.Errorf("edge %q does not extend occurrence %q", edge.ID, command.ParentOccurrenceID)
	}
	if !catalog.RoutePolicy.AllowRepeatedEdges {
		used, err := routePathUsesCatalogEdge(workspace.Documents[document].Route, command.ParentOccurrenceID, edge.ID, catalog)
		if err != nil {
			return result, fmt.Errorf("route parent does not resolve to an exact path: %w", err)
		}
		if used {
			return result, fmt.Errorf("edge %q is already used in this route", edge.ID)
		}
	}
	occurrenceID := commandGeneratedID("occ_", commandID, index, command.Type)
	if findRoute(&workspace.Documents[document].Route, occurrenceID) != nil {
		return CommandResult{Type: CommandResultRouteAdded, OutputID: command.OutputID, OccurrenceID: occurrenceID}, nil
	}
	parent.Children = append(parent.Children, RouteNode{OccurrenceID: occurrenceID, ResourceType: to.ResourceType, CatalogEdgeID: edge.ID, Relationship: edge.Label})
	return CommandResult{Type: CommandResultRouteAdded, OutputID: command.OutputID, OccurrenceID: occurrenceID}, nil
}

func applyUpdateRouteEdgeCommand(workspace *Workspace, catalog CatalogSnapshot, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	document := documentIndex(workspace, command.OutputID)
	if document < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	parent, occurrence := findRouteWithParent(&workspace.Documents[document].Route, command.OccurrenceID)
	edge, ok := catalogEdge(catalog, command.EdgeID)
	if parent == nil || occurrence == nil || !ok {
		return result, fmt.Errorf("route occurrence, parent, or edge was not found")
	}
	parentNodeID, parentNodeFound := catalogNodeIDForOccurrence(workspace.Documents[document].Route, parent.OccurrenceID, catalog)
	occurrenceNodeID, occurrenceNodeFound := catalogNodeIDForOccurrence(workspace.Documents[document].Route, occurrence.OccurrenceID, catalog)
	if !parentNodeFound || !occurrenceNodeFound {
		return result, fmt.Errorf("route occurrence does not resolve to one exact catalog path")
	}
	from, fromOK := catalogNode(catalog, edge.FromNodeID)
	to, toOK := catalogNode(catalog, edge.ToNodeID)
	if !fromOK || !toOK || edge.FromNodeID != parentNodeID || edge.ToNodeID != occurrenceNodeID || from.ResourceType != parent.ResourceType || to.ResourceType != occurrence.ResourceType {
		return result, fmt.Errorf("edge %q cannot replace the relationship for occurrence %q", edge.ID, command.OccurrenceID)
	}
	if !catalog.RoutePolicy.AllowRepeatedEdges {
		usedOnPath, err := routePathUsesCatalogEdge(workspace.Documents[document].Route, parent.OccurrenceID, edge.ID, catalog)
		if err != nil {
			return result, fmt.Errorf("route parent does not resolve to an exact path: %w", err)
		}
		usedInSubtree, err := routeSubtreeUsesCatalogEdge(*occurrence, occurrenceNodeID, edge.ID, catalog)
		if err != nil {
			return result, fmt.Errorf("route subtree does not resolve to exact paths: %w", err)
		}
		if usedOnPath || usedInSubtree {
			return result, fmt.Errorf("edge %q is already used in this route", edge.ID)
		}
	}
	occurrence.CatalogEdgeID = edge.ID
	occurrence.Relationship = edge.Label
	result.OccurrenceID = occurrence.OccurrenceID
	return result, nil
}

func applySetRouteMatchModeCommand(workspace *Workspace, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	document := documentIndex(workspace, command.OutputID)
	if document < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	occurrence := findRoute(&workspace.Documents[document].Route, command.OccurrenceID)
	if occurrence == nil || occurrence.OccurrenceID == RootOccurrenceID {
		return result, fmt.Errorf("non-root route occurrence %q was not found", command.OccurrenceID)
	}
	occurrence.MatchMode = command.MatchMode.Normalized()
	result.OccurrenceID = occurrence.OccurrenceID
	return result, nil
}

func applyRemoveRouteCommand(workspace *Workspace, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	document := documentIndex(workspace, command.OutputID)
	if document < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	removed := map[string]bool{}
	if !removeRoute(&workspace.Documents[document].Route, command.OccurrenceID, removed) {
		return result, fmt.Errorf("occurrence %q was not found", command.OccurrenceID)
	}
	columns := workspace.Documents[document].Columns[:0]
	for _, column := range workspace.Documents[document].Columns {
		if !removed[column.OccurrenceID] {
			columns = append(columns, column)
		}
	}
	workspace.Documents[document].Columns = columns
	cleanupDocumentReferences(&workspace.Documents[document])
	cleanupWorkspaceBindings(workspace)
	return result, nil
}
