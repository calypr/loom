package support

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func ResolveCapabilityRouteEdge(snapshot capability.Snapshot, parentNodeID string, parent, child authoringv2.RouteNode) (capability.Edge, error) {
	valid := func(edge capability.Edge) bool {
		from, fromOK := snapshot.Node(edge.FromNodeID)
		to, toOK := snapshot.Node(edge.ToNodeID)
		direction := strings.ToUpper(strings.TrimSpace(edge.StorageDirection))
		return edge.ID != "" && edge.BlockedReason == "" && edge.FromNodeID == parentNodeID && edge.Label == child.Relationship &&
			fromOK && toOK && from.ResourceType == parent.ResourceType && to.ResourceType == child.ResourceType &&
			(edge.SourceResourceType == "" || edge.SourceResourceType == parent.ResourceType) && (edge.TargetResourceType == "" || edge.TargetResourceType == child.ResourceType) &&
			(direction == "" || direction == "INBOUND" || direction == "OUTBOUND")
	}
	if child.CatalogEdgeID != "" {
		edge, found := snapshot.Edge(child.CatalogEdgeID)
		if !found || !valid(edge) {
			return capability.Edge{}, fmt.Errorf("catalog edge %q no longer identifies the saved route step", child.CatalogEdgeID)
		}
		return edge, nil
	}
	var match capability.Edge
	for _, edge := range snapshot.Edges {
		if !valid(edge) {
			continue
		}
		if match.ID != "" {
			return capability.Edge{}, fmt.Errorf("saved route step resolves to multiple capability edges")
		}
		match = edge
	}
	if match.ID == "" {
		return capability.Edge{}, fmt.Errorf("saved route step no longer resolves to a capability edge")
	}
	return match, nil
}

func ReauthorizeConstructionRoute(snapshot capability.Snapshot, rootResourceType, targetNodeID string, route []capability.ConstructionRouteStep) ([]capability.ConstructionRouteStep, error) {
	var root capability.Node
	for _, node := range snapshot.Nodes {
		if node.RowRootEligible && node.ResourceType == rootResourceType {
			if root.ID != "" {
				return nil, fmt.Errorf("row root is ambiguous")
			}
			root = node
		}
	}
	if root.ID == "" {
		return nil, fmt.Errorf("row root is unavailable")
	}
	return ReauthorizeConstructionRouteFromAnchor(snapshot, root.ID, targetNodeID, route)
}

func ReauthorizeConstructionRouteFromAnchor(snapshot capability.Snapshot, anchorNodeID, targetNodeID string, route []capability.ConstructionRouteStep) ([]capability.ConstructionRouteStep, error) {
	current, ok := snapshot.Node(anchorNodeID)
	if !ok || current.ID == "" {
		return nil, fmt.Errorf("row resource anchor is unavailable")
	}
	if snapshot.Policy.Route.MaxHops > 0 && len(route) > snapshot.Policy.Route.MaxHops {
		return nil, fmt.Errorf("route exceeds the current policy")
	}
	seenEdges := make(map[string]bool, len(route))
	resolved := make([]capability.ConstructionRouteStep, 0, len(route))
	for index, step := range route {
		if strings.TrimSpace(step.EdgeID) == "" || step.MatchMode != "OPTIONAL" && step.MatchMode != "REQUIRED" {
			return nil, fmt.Errorf("route step %d is incomplete", index)
		}
		edge, found := snapshot.Edge(step.EdgeID)
		direction := strings.ToUpper(strings.TrimSpace(edge.StorageDirection))
		if !found || edge.BlockedReason != "" || edge.FromNodeID != current.ID ||
			edge.Label != step.Relationship || direction != strings.ToUpper(strings.TrimSpace(step.StorageDirection)) ||
			(edge.SourceResourceType != "" && edge.SourceResourceType != step.FromResourceType) || (edge.TargetResourceType != "" && edge.TargetResourceType != step.ToResourceType) ||
			edge.FromNodeID != step.FromNodeID || edge.ToNodeID != step.ToNodeID {
			return nil, fmt.Errorf("route step %d no longer identifies the authorized capability edge", index)
		}
		if current.ResourceType != step.FromResourceType {
			return nil, fmt.Errorf("route step %d does not extend its parent", index)
		}
		if !snapshot.Policy.Route.AllowsRepeatedEdges && seenEdges[edge.ID] {
			return nil, fmt.Errorf("route repeats an edge forbidden by current policy")
		}
		if !snapshot.Policy.Route.AllowsSelfLoops && edge.FromNodeID == edge.ToNodeID {
			return nil, fmt.Errorf("route contains a self-loop forbidden by current policy")
		}
		if direction != "" && direction != "INBOUND" && direction != "OUTBOUND" {
			return nil, fmt.Errorf("route has an unsupported storage direction")
		}
		to, found := snapshot.Node(edge.ToNodeID)
		if !found || to.ResourceType != step.ToResourceType {
			return nil, fmt.Errorf("route target is unavailable")
		}
		seenEdges[edge.ID] = true
		resolved = append(resolved, step)
		current = to
	}
	if current.ID != targetNodeID {
		return nil, fmt.Errorf("route does not end at the exact source node")
	}
	return resolved, nil
}

func RoutePathToOccurrence(root authoringv2.RouteNode, occurrenceID string) ([]authoringv2.RouteNode, bool) {
	if root.OccurrenceID == occurrenceID {
		return []authoringv2.RouteNode{root}, true
	}
	for _, child := range root.Children {
		path, found := RoutePathToOccurrence(child, occurrenceID)
		if !found {
			continue
		}
		return append([]authoringv2.RouteNode{root}, path...), true
	}
	return nil, false
}
