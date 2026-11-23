package population

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

type PopulationRoutesRequest struct {
	Project             string
	ExplorerID          string
	SnapshotToken       string
	OutputID            string
	SelectionRevisionID string
	Limit               int
	Cursor              string
}

type PopulationRouteChoice struct {
	RouteChoiceID string                                    `json:"routeChoiceId"`
	Route         []capability.ConstructionRouteStep        `json:"route"`
	Presentation  capability.ConstructionChoicePresentation `json:"presentation"`
}

type PopulationRoutesResponse struct {
	SnapshotToken       string                  `json:"snapshotToken"`
	OutputID            string                  `json:"outputId"`
	SelectionRevisionID string                  `json:"selectionRevisionId"`
	Complete            bool                    `json:"complete"`
	Truncated           bool                    `json:"truncated"`
	NextCursor          string                  `json:"nextCursor,omitempty"`
	Choices             []PopulationRouteChoice `json:"choices"`
}

func ValidateRoute(document authoringv2.Document, population authoringv2.Population, selectionType string, snapshot capability.Snapshot) ([]authoringv2.PopulationRouteStep, error) {
	rootNodes := make([]capability.Node, 0, 1)
	for _, node := range snapshot.Nodes {
		if node.ResourceType == document.RootResourceType && node.RowRootEligible {
			rootNodes = append(rootNodes, node)
		}
	}
	if len(rootNodes) != 1 {
		return nil, fmt.Errorf("population root %q is not uniquely eligible", document.RootResourceType)
	}
	if snapshot.Policy.Route.MaxHops > 0 && len(population.Route) > snapshot.Policy.Route.MaxHops {
		return nil, fmt.Errorf("population route exceeds capability maxHops=%d", snapshot.Policy.Route.MaxHops)
	}
	currentID, currentType := rootNodes[0].ID, rootNodes[0].ResourceType
	seenEdges := map[string]bool{}
	resolvedRoute := append([]authoringv2.PopulationRouteStep(nil), population.Route...)
	for index := range resolvedRoute {
		step := resolvedRoute[index]
		if step.StorageDirection != "" && step.StorageDirection != "INBOUND" && step.StorageDirection != "OUTBOUND" {
			return nil, fmt.Errorf("population route[%d] has an unsupported storage direction", index)
		}
		edge, target, err := resolveRouteEdge(snapshot, currentID, currentType, step)
		if err != nil {
			return nil, fmt.Errorf("population route[%d] relationship %q is ambiguous or stale: %w", index, step.Relationship, err)
		}
		if seenEdges[edge.ID] && !snapshot.Policy.Route.AllowsRepeatedEdges {
			return nil, fmt.Errorf("population route[%d] repeats edge %q but repeated edges are not allowed", index, edge.ID)
		}
		if currentType == target.ResourceType && !snapshot.Policy.Route.AllowsSelfLoops {
			return nil, fmt.Errorf("population route[%d] uses self-loop edge %q but self-loops are not allowed", index, edge.ID)
		}
		if resolvedRoute[index].StorageDirection == "" {
			direction := strings.ToUpper(strings.TrimSpace(edge.StorageDirection))
			if direction == "INBOUND" || direction == "OUTBOUND" {
				resolvedRoute[index].StorageDirection = direction
			}
		}
		seenEdges[edge.ID] = true
		currentID, currentType = target.ID, target.ResourceType
	}
	if currentType != strings.TrimSpace(selectionType) {
		return nil, fmt.Errorf("population route terminates at %q, selection contains %q", currentType, selectionType)
	}
	return resolvedRoute, nil
}

func resolveRouteEdge(snapshot capability.Snapshot, currentID, currentType string, step authoringv2.PopulationRouteStep) (capability.Edge, capability.Node, error) {
	valid := func(edge capability.Edge) (capability.Node, bool) {
		return snapshot.MatchPopulationRouteEdge(edge, currentID, currentType, step.ResourceType, step.Relationship, step.StorageDirection)
	}
	if step.CatalogEdgeID != "" {
		edge, found := snapshot.Edge(step.CatalogEdgeID)
		if !found {
			return capability.Edge{}, capability.Node{}, fmt.Errorf("catalog edge %q is unavailable", step.CatalogEdgeID)
		}
		target, ok := valid(edge)
		if !ok {
			return capability.Edge{}, capability.Node{}, fmt.Errorf("catalog edge %q no longer identifies this route step", step.CatalogEdgeID)
		}
		return edge, target, nil
	}
	var match capability.Edge
	var target capability.Node
	for _, edge := range snapshot.Edges {
		resolvedTarget, ok := valid(edge)
		if !ok {
			continue
		}
		if match.ID != "" {
			return capability.Edge{}, capability.Node{}, fmt.Errorf("semantic tuple resolves to multiple catalog edges")
		}
		match, target = edge, resolvedTarget
	}
	if match.ID == "" {
		return capability.Edge{}, capability.Node{}, fmt.Errorf("semantic tuple does not resolve to a catalog edge")
	}
	return match, target, nil
}

func ResolveRouteEdge(snapshot capability.Snapshot, currentID, currentType string, step authoringv2.PopulationRouteStep) (capability.Edge, capability.Node, error) {
	return resolveRouteEdge(snapshot, currentID, currentType, step)
}
