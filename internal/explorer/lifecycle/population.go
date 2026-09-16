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
		matches := make([]capability.Edge, 0, 1)
		for _, edge := range snapshot.Edges {
			if edge.FromNodeID != currentID || edge.Label != step.Relationship {
				continue
			}
			target, ok := snapshot.Node(edge.ToNodeID)
			if ok && target.ResourceType == step.ResourceType {
				matches = append(matches, edge)
			}
		}
		if len(matches) != 1 {
			return fmt.Errorf("population route[%d] relationship %q is ambiguous or stale", index, step.Relationship)
		}
		if seenEdges[matches[0].ID] && !snapshot.Policy.Route.AllowsRepeatedEdges {
			return fmt.Errorf("population route[%d] repeats edge %q but repeated edges are not allowed", index, matches[0].ID)
		}
		target, _ := snapshot.Node(matches[0].ToNodeID)
		if currentType == target.ResourceType && !snapshot.Policy.Route.AllowsSelfLoops {
			return fmt.Errorf("population route[%d] uses self-loop edge %q but self-loops are not allowed", index, matches[0].ID)
		}
		seenEdges[matches[0].ID] = true
		currentID, currentType = target.ID, target.ResourceType
	}
	if currentType != strings.TrimSpace(selectionType) {
		return fmt.Errorf("population route terminates at %q, selection contains %q", currentType, selectionType)
	}
	return nil
}
