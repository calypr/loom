package authoringv2

import "fmt"

func resolveCatalogRouteEdge(catalog CatalogSnapshot, parentNodeID string, parent, child RouteNode) (CatalogEdge, error) {
	if child.CatalogEdgeID != "" {
		edge, found := catalogEdge(catalog, child.CatalogEdgeID)
		if !found {
			return CatalogEdge{}, fmt.Errorf("catalog edge %q is unavailable", child.CatalogEdgeID)
		}
		if err := validateCatalogRouteEdge(catalog, parentNodeID, parent, child, edge); err != nil {
			return CatalogEdge{}, err
		}
		return edge, nil
	}

	var match CatalogEdge
	for _, edge := range catalog.Edges {
		if err := validateCatalogRouteEdge(catalog, parentNodeID, parent, child, edge); err != nil {
			continue
		}
		if match.ID != "" {
			return CatalogEdge{}, fmt.Errorf("relationship %q from %s to %s resolves to multiple catalog edges", child.Relationship, parent.ResourceType, child.ResourceType)
		}
		match = edge
	}
	if match.ID == "" {
		return CatalogEdge{}, fmt.Errorf("relationship %q from %s to %s is unavailable", child.Relationship, parent.ResourceType, child.ResourceType)
	}
	return match, nil
}

func validateCatalogRouteEdge(catalog CatalogSnapshot, parentNodeID string, parent, child RouteNode, edge CatalogEdge) error {
	from, fromFound := catalogNode(catalog, edge.FromNodeID)
	to, toFound := catalogNode(catalog, edge.ToNodeID)
	if edge.ID == "" || !fromFound || !toFound || edge.FromNodeID != parentNodeID ||
		from.ResourceType != parent.ResourceType || to.ResourceType != child.ResourceType || edge.Label != child.Relationship {
		return fmt.Errorf("catalog edge %q does not identify the authored route step", edge.ID)
	}
	return nil
}

func catalogNodeIDForOccurrence(route RouteNode, occurrenceID string, catalog CatalogSnapshot) (string, bool) {
	path, found := routePath(route, occurrenceID)
	if !found || len(path) == 0 || path[0].OccurrenceID != RootOccurrenceID {
		return "", false
	}
	root, found := catalogNodeForConstructionRoot(catalog, path[0].ResourceType)
	if !found {
		return "", false
	}
	currentNodeID := root.ID
	for index := 1; index < len(path); index++ {
		edge, err := resolveCatalogRouteEdge(catalog, currentNodeID, path[index-1], path[index])
		if err != nil {
			return "", false
		}
		currentNodeID = edge.ToNodeID
	}
	return currentNodeID, true
}

func routePathUsesCatalogEdge(route RouteNode, occurrenceID, edgeID string, catalog CatalogSnapshot) (bool, error) {
	path, found := routePath(route, occurrenceID)
	if !found || len(path) == 0 {
		return false, fmt.Errorf("route occurrence %q was not found", occurrenceID)
	}
	root, found := catalogNodeForConstructionRoot(catalog, path[0].ResourceType)
	if !found {
		return false, fmt.Errorf("route root %q is unavailable or ambiguous", path[0].ResourceType)
	}
	currentNodeID := root.ID
	for index := 1; index < len(path); index++ {
		resolved, err := resolveCatalogRouteEdge(catalog, currentNodeID, path[index-1], path[index])
		if err != nil {
			return false, err
		}
		if resolved.ID == edgeID {
			return true, nil
		}
		currentNodeID = resolved.ToNodeID
	}
	return false, nil
}

func routeSubtreeUsesCatalogEdge(route RouteNode, nodeID, edgeID string, catalog CatalogSnapshot) (bool, error) {
	for _, child := range route.Children {
		resolved, err := resolveCatalogRouteEdge(catalog, nodeID, route, child)
		if err != nil {
			return false, err
		}
		if resolved.ID == edgeID {
			return true, nil
		}
		used, err := routeSubtreeUsesCatalogEdge(child, resolved.ToNodeID, edgeID, catalog)
		if err != nil || used {
			return used, err
		}
	}
	return false, nil
}
