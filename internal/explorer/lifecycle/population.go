package lifecycle

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/projectid"
)

// resolveWorkspacePopulations verifies every durable population against the
// exact capability snapshot and returns the bounded identity needed by the
// receipt compiler. Selection member rows are intentionally not loaded here.
func (s *Service) resolveWorkspacePopulations(ctx context.Context, project string, workspace authoringv2.Workspace, snapshot capability.Snapshot, scopeDigest string) (explorercompilation.ResolvedInputs, error) {
	project = projectid.Canonical(project)
	inputs := explorercompilation.ResolvedInputs{}
	for index, document := range workspace.Documents {
		population := document.Population
		if population == nil {
			continue
		}
		selection, err := s.store.GetSelection(ctx, project, population.SelectionRevisionID)
		if err != nil {
			return inputs, fmt.Errorf("population at documents[%d]: selection %q could not be resolved: %w", index, population.SelectionRevisionID, err)
		}
		if selection == nil {
			return inputs, fmt.Errorf("population at documents[%d]: selection %q was not found", index, population.SelectionRevisionID)
		}
		if err := selection.Validate(); err != nil {
			return inputs, fmt.Errorf("population at documents[%d]: selection %q is invalid or incomplete: %w", index, population.SelectionRevisionID, err)
		}
		if !selection.Complete {
			return inputs, fmt.Errorf("population at documents[%d]: selection %q is incomplete", index, population.SelectionRevisionID)
		}
		if projectid.Canonical(selection.Project) != project {
			return inputs, fmt.Errorf("population at documents[%d]: selection project does not match the workspace project", index)
		}
		if strings.TrimSpace(selection.Generation) != strings.TrimSpace(snapshot.Identity.Generation) {
			return inputs, fmt.Errorf("population at documents[%d]: selection generation is stale", index)
		}
		if strings.TrimSpace(selection.ScopeDigest) != strings.TrimSpace(snapshot.Identity.AuthorizationScopeDigest) || (scopeDigest != "" && strings.TrimSpace(selection.ScopeDigest) != strings.TrimSpace(scopeDigest)) {
			return inputs, fmt.Errorf("population at documents[%d]: selection authorization scope is stale", index)
		}
		if err := validatePopulationRoute(document, *population, selection.ResourceType, snapshot); err != nil {
			return inputs, fmt.Errorf("population at documents[%d]: %w", index, err)
		}
		inputs.Populations = append(inputs.Populations, explorercompilation.ResolvedPopulation{
			OutputID: document.Output.ID, SelectionRevisionID: selection.ID, MembershipDigest: selection.MembershipDigest,
			MemberCount: selection.MemberCount, ResourceType: selection.ResourceType, Route: append([]authoringv2.PopulationRouteStep(nil), population.Route...),
		})
	}
	return inputs.Canonical(), nil
}

func validatePopulationRoute(document authoringv2.Document, population authoringv2.Population, selectionType string, snapshot capability.Snapshot) error {
	rootNodes := make([]capability.Node, 0, 1)
	for _, node := range snapshot.Nodes {
		if node.ResourceType == document.RootResourceType && node.RowRootEligible {
			rootNodes = append(rootNodes, node)
		}
	}
	if len(rootNodes) != 1 {
		return fmt.Errorf("population root %q is not uniquely eligible", document.RootResourceType)
	}
	if snapshot.Policy.Route.MaxHops > 0 && len(population.Route) > snapshot.Policy.Route.MaxHops {
		return fmt.Errorf("population route exceeds capability maxHops=%d", snapshot.Policy.Route.MaxHops)
	}
	currentID, currentType := rootNodes[0].ID, rootNodes[0].ResourceType
	seenEdges := map[string]bool{}
	for index, step := range population.Route {
		edge, target, err := resolvePopulationRouteEdge(snapshot, currentID, currentType, step)
		if err != nil {
			return fmt.Errorf("population route[%d] relationship %q is ambiguous or stale: %w", index, step.Relationship, err)
		}
		if seenEdges[edge.ID] && !snapshot.Policy.Route.AllowsRepeatedEdges {
			return fmt.Errorf("population route[%d] repeats edge %q but repeated edges are not allowed", index, edge.ID)
		}
		if currentType == target.ResourceType && !snapshot.Policy.Route.AllowsSelfLoops {
			return fmt.Errorf("population route[%d] uses self-loop edge %q but self-loops are not allowed", index, edge.ID)
		}
		seenEdges[edge.ID] = true
		currentID, currentType = target.ID, target.ResourceType
	}
	if currentType != strings.TrimSpace(selectionType) {
		return fmt.Errorf("population route terminates at %q, selection contains %q", currentType, selectionType)
	}
	return nil
}

func resolvePopulationRouteEdge(snapshot capability.Snapshot, currentID, currentType string, step authoringv2.PopulationRouteStep) (capability.Edge, capability.Node, error) {
	valid := func(edge capability.Edge) (capability.Node, bool) {
		from, fromOK := snapshot.Node(edge.FromNodeID)
		to, toOK := snapshot.Node(edge.ToNodeID)
		direction := strings.ToUpper(strings.TrimSpace(edge.StorageDirection))
		return to, edge.ID != "" && edge.BlockedReason == "" && edge.FromNodeID == currentID &&
			fromOK && toOK && from.ResourceType == currentType && (edge.SourceResourceType == "" || edge.SourceResourceType == currentType) &&
			to.ResourceType == step.ResourceType && (edge.TargetResourceType == "" || edge.TargetResourceType == step.ResourceType) && edge.Label == step.Relationship &&
			(direction == "" || direction == "INBOUND" || direction == "OUTBOUND")
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
