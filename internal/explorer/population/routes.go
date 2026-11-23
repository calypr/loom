package population

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	"github.com/calypr/loom/internal/explorer/selection"
	"github.com/calypr/loom/internal/projectid"
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

func findPopulationOutput(workspace authoringv2.Workspace, outputID string) *authoringv2.Document {
	for index := range workspace.Documents {
		if workspace.Documents[index].Output.ID == outputID {
			return &workspace.Documents[index]
		}
	}
	return nil
}

func populationRoutePresentation(rootResourceType, terminalResourceType string, route []capability.ConstructionRouteStep) capability.ConstructionChoicePresentation {
	summary := "Use selected " + terminalResourceType + " records"
	if terminalResourceType != rootResourceType {
		summary += " related to " + rootResourceType
	}
	facts := []capability.ConstructionChoiceFact{{Label: "Selected records", Value: terminalResourceType}}
	if len(route) == 0 {
		facts = append(facts, capability.ConstructionChoiceFact{Label: "Route", Value: "Starting resource"})
	} else {
		parts := make([]string, 0, len(route))
		for _, step := range route {
			parts = append(parts, step.FromResourceType+" → "+step.Relationship+" → "+step.ToResourceType)
		}
		facts = append(facts, capability.ConstructionChoiceFact{Label: "Route", Value: strings.Join(parts, " / ")})
	}
	return capability.ConstructionChoicePresentation{Summary: summary, Facts: facts}
}

func invalidPopulationRoute(message string) error {
	return support.Unprocessable("commands", "INVALID_POPULATION_ROUTE", message, fmt.Errorf("population route identity or proof changed"))
}

func (s *Service) SearchPopulationRoutes(ctx context.Context, request PopulationRoutesRequest) (PopulationRoutesResponse, error) {
	result := PopulationRoutesResponse{
		SnapshotToken: request.SnapshotToken, OutputID: request.OutputID,
		SelectionRevisionID: request.SelectionRevisionID, Choices: []PopulationRouteChoice{},
	}
	if strings.TrimSpace(request.Project) == "" || strings.TrimSpace(request.ExplorerID) == "" ||
		strings.TrimSpace(request.SnapshotToken) == "" || strings.TrimSpace(request.OutputID) == "" ||
		strings.TrimSpace(request.SelectionRevisionID) == "" || len(request.Cursor) > 4096 {
		return result, support.Malformed("population-routes", "project, explorerId, snapshotToken, outputId, and selectionRevisionId are required", nil)
	}
	if s.capability().ForCompilation == nil {
		return result, support.Unavailable("population-routes", "CAPABILITY_UNAVAILABLE", "authorized capability resolution is not configured", nil)
	}
	authorized, err := s.capability().ForCompilation(ctx, request.Project, request.SnapshotToken)
	if err != nil {
		return result, support.CapabilityFailure("population-routes", "STALE_CATALOG_SNAPSHOT", "reload the catalog before searching population routes", err)
	}
	if authorized.Snapshot.ValidateToken(request.SnapshotToken) != nil {
		return result, support.Conflict("population-routes", "STALE_CATALOG_SNAPSHOT", "reload the catalog before searching population routes", nil, nil)
	}
	snapshot := authorized.Snapshot
	if projectid.Canonical(snapshot.Identity.Project) != projectid.Canonical(request.Project) || snapshot.Identity.Generation == "" {
		return result, support.Conflict("population-routes", "STALE_CATALOG_SNAPSHOT", "reload the catalog before searching population routes", nil, nil)
	}
	if err := support.ValidateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return result, support.Conflict("population-routes", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	workspace, err := s.config.CurrentWorkspace(ctx, request.Project, request.ExplorerID)
	if err != nil {
		return result, err
	}
	document := findPopulationOutput(workspace, request.OutputID)
	if document == nil || document.Route.OccurrenceID != authoringv2.RootOccurrenceID || document.Route.ResourceType != document.RootResourceType {
		return result, support.Malformed("population-routes", "outputId does not identify a valid row-rooted table", nil)
	}
	selected, err := s.getAuthorizedPopulationSelection(ctx, request.Project, request.SelectionRevisionID, snapshot)
	if err != nil {
		return result, err
	}
	targetIDs := make([]string, 0)
	for _, node := range snapshot.Nodes {
		if node.ResourceType == selected.ResourceType {
			targetIDs = append(targetIDs, node.ID)
		}
	}
	if len(targetIDs) == 0 {
		return result, support.Unprocessable("population-routes", "INVALID_POPULATION_SOURCE", "selected resource type is not in the authorized capability snapshot", nil)
	}
	sourceKey := "population:" + request.OutputID + "\x00" + selected.ID
	page, err := capability.PlanConstructionRoutes(capability.ConstructionRouteSearch{
		Snapshot: snapshot, RootResource: document.RootResourceType, TargetNodeIDs: targetIDs,
		SourceKey: sourceKey, Cursor: request.Cursor, Limit: request.Limit,
	})
	if err != nil {
		return result, support.Conflict("population-routes", "STALE_OR_INVALID_ROUTE_CURSOR", "restart route search for this selection", nil, err)
	}
	result.Complete, result.Truncated, result.NextCursor = page.Complete, page.Truncated, page.NextCursor
	for _, route := range page.Routes {
		targetNodeID := ""
		if len(route) > 0 {
			targetNodeID = route[len(route)-1].ToNodeID
		} else {
			for _, node := range snapshot.Nodes {
				if node.RowRootEligible && node.ResourceType == document.RootResourceType {
					if targetNodeID != "" {
						targetNodeID = ""
						break
					}
					targetNodeID = node.ID
				}
			}
		}
		resolvedRoute, routeErr := support.ReauthorizeConstructionRoute(snapshot, document.RootResourceType, targetNodeID, route)
		if routeErr != nil || !s.provePopulationRoute(ctx, authorized, document.RootResourceType, selected.ResourceType, resolvedRoute) {
			continue
		}
		choiceID, encodeErr := capability.NewPopulationRouteChoiceID(capability.PopulationRouteChoiceIdentity{
			Version: 1, SnapshotToken: snapshot.Token, OutputID: request.OutputID,
			SelectionRevisionID: selected.ID, Route: resolvedRoute,
		})
		if encodeErr != nil {
			continue
		}
		result.Choices = append(result.Choices, PopulationRouteChoice{
			RouteChoiceID: choiceID, Route: capability.CloneConstructionRoute(resolvedRoute),
			Presentation: populationRoutePresentation(document.RootResourceType, selected.ResourceType, resolvedRoute),
		})
	}
	return result, nil
}

func (s *Service) getAuthorizedPopulationSelection(ctx context.Context, project, selectionID string, snapshot capability.Snapshot) (*selection.SelectionRevision, error) {
	selected, err := s.config.Store.GetSelection(ctx, projectid.Canonical(project), strings.TrimSpace(selectionID))
	if err != nil {
		return nil, support.Unavailable("population-routes", "POPULATION_SOURCE_UNAVAILABLE", "the selected resource set could not be loaded", err)
	}
	if selected == nil {
		return nil, support.Unprocessable("population-routes", "INVALID_POPULATION_SOURCE", "the selected resource set is unavailable", nil)
	}
	if err := selected.Validate(); err != nil || !selected.Complete {
		return nil, support.Unprocessable("population-routes", "INVALID_POPULATION_SOURCE", "the selected resource set is incomplete or invalid", err)
	}
	if projectid.Canonical(selected.Project) != projectid.Canonical(snapshot.Identity.Project) ||
		selected.Generation != snapshot.Identity.Generation || selected.ScopeDigest != snapshot.Identity.AuthorizationScopeDigest {
		return nil, support.Conflict("population-routes", "STALE_POPULATION_SELECTION", "the selected resource set belongs to another data generation or authorization scope", nil, nil)
	}
	return selected, nil
}

func (s *Service) provePopulationRoute(ctx context.Context, authorized ports.AuthorizedCapability, rootResourceType, terminalResourceType string, route []capability.ConstructionRouteStep) bool {
	compilerRoute := make([]facade.Traversal, 0, len(route))
	for _, step := range route {
		matchMode := spec.TraversalMatchOptional
		if step.MatchMode == string(spec.TraversalMatchRequired) {
			matchMode = spec.TraversalMatchRequired
		}
		traversal := facade.Traversal{FromResourceType: step.FromResourceType, EdgeLabel: step.Relationship, ToResourceType: step.ToResourceType, MatchMode: matchMode}
		proof, err := facade.ProbeTraversal(ctx, facade.TraversalRequest{
			Scope: s.config.ConstructionCompilerScope(authorized), RootResourceType: step.FromResourceType, Traversal: traversal,
		})
		if err != nil || proof.Traversal == nil || string(proof.Traversal.StorageDirection) != step.StorageDirection {
			return false
		}
		compilerRoute = append(compilerRoute, traversal)
	}
	_, err := facade.ProbeCandidate(ctx, facade.CandidateRequest{
		Scope: s.config.ConstructionCompilerScope(authorized), RootResourceType: rootResourceType,
		ResourceType: terminalResourceType, FieldRef: terminalResourceType + ".id", Selector: "id", Route: compilerRoute,
	})
	return err == nil
}

func (s *Service) PreparePopulationRouteChoices(ctx context.Context, project string, authorized ports.AuthorizedCapability, identities []capability.PopulationRouteChoiceIdentity, workspace authoringv2.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
	if len(commands) == 0 || len(commands) != len(identities) {
		return nil, support.Malformed("commands", "population route choices must resolve one-to-one with commands", nil)
	}
	snapshot := authorized.Snapshot
	if err := support.ValidateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return nil, support.Conflict("commands", "STALE_AUTHORIZATION_SCOPE", "the authorized population route scope changed", nil, err)
	}
	for index := range commands {
		command := &commands[index]
		if command.Type != authoringv2.CommandSetTablePopulation || command.RouteChoiceID() == "" || len(command.EdgeIDs()) != 0 {
			return nil, support.Malformed("commands", "population route choices require only SET_TABLE_POPULATION routeChoiceId commands", nil)
		}
		identity := identities[index]
		if identity.SnapshotToken != snapshot.Token || identity.OutputID != command.OutputID || identity.SelectionRevisionID != command.SelectionRevisionID() {
			return nil, support.Conflict("commands", "STALE_POPULATION_ROUTE", "the population route belongs to another table, selection, or catalog snapshot", nil, nil)
		}
		document := findPopulationOutput(workspace, command.OutputID)
		if document == nil || document.Route.OccurrenceID != authoringv2.RootOccurrenceID || document.Route.ResourceType != document.RootResourceType {
			return nil, support.Malformed("commands", "outputId does not identify a valid row-rooted table", nil)
		}
		selected, err := s.getAuthorizedPopulationSelection(ctx, project, command.SelectionRevisionID(), snapshot)
		if err != nil {
			return nil, err
		}
		targetNodeID := ""
		if len(identity.Route) > 0 {
			targetNodeID = identity.Route[len(identity.Route)-1].ToNodeID
		} else {
			for _, node := range snapshot.Nodes {
				if node.RowRootEligible && node.ResourceType == document.RootResourceType && node.ResourceType == selected.ResourceType {
					if targetNodeID != "" {
						return nil, invalidPopulationRoute("zero-hop row root is ambiguous")
					}
					targetNodeID = node.ID
				}
			}
		}
		route, err := support.ReauthorizeConstructionRoute(snapshot, document.RootResourceType, targetNodeID, identity.Route)
		if err != nil || !s.provePopulationRoute(ctx, authorized, document.RootResourceType, selected.ResourceType, route) {
			return nil, invalidPopulationRoute("the complete population route is no longer authorized and compiler-proved")
		}
		expected, err := capability.NewPopulationRouteChoiceID(capability.PopulationRouteChoiceIdentity{
			Version: 1, SnapshotToken: snapshot.Token, OutputID: command.OutputID,
			SelectionRevisionID: selected.ID, Route: route,
		})
		if err != nil || expected != command.RouteChoiceID() {
			return nil, invalidPopulationRoute("the route choice identity no longer matches this selection")
		}
		steps := make([]authoringv2.PopulationRouteStep, 0, len(route))
		for _, step := range route {
			steps = append(steps, authoringv2.PopulationRouteStep{
				ResourceType: step.ToResourceType, Relationship: step.Relationship,
				CatalogEdgeID: step.EdgeID, StorageDirection: step.StorageDirection,
			})
		}
		command.ResolvedPopulationRoute = steps
	}
	return commands, nil
}
