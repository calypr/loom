package lifecycle

import (
	"context"
	"fmt"
	"strings"

	compilerprobe "github.com/calypr/loom/internal/dataframe/compiler/capability"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
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

func (s *Service) SearchPopulationRoutes(ctx context.Context, request PopulationRoutesRequest) (PopulationRoutesResponse, error) {
	result := PopulationRoutesResponse{
		SnapshotToken: request.SnapshotToken, OutputID: request.OutputID,
		SelectionRevisionID: request.SelectionRevisionID, Choices: []PopulationRouteChoice{},
	}
	if strings.TrimSpace(request.Project) == "" || strings.TrimSpace(request.ExplorerID) == "" ||
		strings.TrimSpace(request.SnapshotToken) == "" || strings.TrimSpace(request.OutputID) == "" ||
		strings.TrimSpace(request.SelectionRevisionID) == "" || len(request.Cursor) > 4096 {
		return result, malformed("population-routes", "project, explorerId, snapshotToken, outputId, and selectionRevisionId are required", nil)
	}
	if s.config.Capability.ForCompilation == nil {
		return result, unavailable("population-routes", "CAPABILITY_UNAVAILABLE", "authorized capability resolution is not configured", nil)
	}
	authorized, err := s.config.Capability.ForCompilation(ctx, request.Project, request.SnapshotToken)
	if err != nil || authorized.Snapshot.ValidateToken(request.SnapshotToken) != nil {
		return result, conflict("population-routes", "STALE_CATALOG_SNAPSHOT", "reload the catalog before searching population routes", nil, err)
	}
	snapshot := authorized.Snapshot
	if projectid.Canonical(snapshot.Identity.Project) != projectid.Canonical(request.Project) || snapshot.Identity.Generation == "" {
		return result, conflict("population-routes", "STALE_CATALOG_SNAPSHOT", "reload the catalog before searching population routes", nil, nil)
	}
	if err := validateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return result, conflict("population-routes", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	workspace, err := s.currentWorkspace(ctx, request.Project, request.ExplorerID)
	if err != nil {
		return result, err
	}
	document := findSemanticOutput(workspace, request.OutputID)
	if document == nil || document.Route.OccurrenceID != authoringv2.RootOccurrenceID || document.Route.ResourceType != document.RootResourceType {
		return result, malformed("population-routes", "outputId does not identify a valid row-rooted table", nil)
	}
	selection, err := s.getAuthorizedPopulationSelection(ctx, request.Project, request.SelectionRevisionID, snapshot)
	if err != nil {
		return result, err
	}
	targetIDs := make([]string, 0)
	for _, node := range snapshot.Nodes {
		if node.ResourceType == selection.ResourceType {
			targetIDs = append(targetIDs, node.ID)
		}
	}
	if len(targetIDs) == 0 {
		return result, unprocessable("population-routes", "INVALID_POPULATION_SOURCE", "selected resource type is not in the authorized capability snapshot", nil)
	}
	sourceKey := "population:" + request.OutputID + "\x00" + selection.ID
	page, err := capability.PlanConstructionRoutes(capability.ConstructionRouteSearch{
		Snapshot: snapshot, RootResource: document.RootResourceType, TargetNodeIDs: targetIDs,
		SourceKey: sourceKey, Cursor: request.Cursor, Limit: request.Limit,
	})
	if err != nil {
		return result, conflict("population-routes", "STALE_OR_INVALID_ROUTE_CURSOR", "restart route search for this selection", nil, err)
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
		resolvedRoute, routeErr := reauthorizeConstructionRoute(snapshot, document.RootResourceType, targetNodeID, route)
		if routeErr != nil || !provePopulationRoute(ctx, authorized, document.RootResourceType, selection.ResourceType, resolvedRoute) {
			continue
		}
		choiceID, encodeErr := capability.NewPopulationRouteChoiceID(capability.PopulationRouteChoiceIdentity{
			Version: 1, SnapshotToken: snapshot.Token, OutputID: request.OutputID,
			SelectionRevisionID: selection.ID, Route: resolvedRoute,
		})
		if encodeErr != nil {
			continue
		}
		result.Choices = append(result.Choices, PopulationRouteChoice{
			RouteChoiceID: choiceID, Route: cloneConstructionRoute(resolvedRoute),
			Presentation: populationRoutePresentation(document.RootResourceType, selection.ResourceType, resolvedRoute),
		})
	}
	return result, nil
}

func (s *Service) getAuthorizedPopulationSelection(ctx context.Context, project, selectionID string, snapshot capability.Snapshot) (*explorer.SelectionRevision, error) {
	selection, err := s.store.GetSelection(ctx, projectid.Canonical(project), strings.TrimSpace(selectionID))
	if err != nil || selection == nil {
		return nil, unprocessable("population-routes", "INVALID_POPULATION_SOURCE", "the selected resource set is unavailable", err)
	}
	if err := selection.Validate(); err != nil || !selection.Complete {
		return nil, unprocessable("population-routes", "INVALID_POPULATION_SOURCE", "the selected resource set is incomplete or invalid", err)
	}
	if projectid.Canonical(selection.Project) != projectid.Canonical(snapshot.Identity.Project) ||
		selection.Generation != snapshot.Identity.Generation || selection.ScopeDigest != snapshot.Identity.AuthorizationScopeDigest {
		return nil, conflict("population-routes", "STALE_POPULATION_SELECTION", "the selected resource set belongs to another data generation or authorization scope", nil, nil)
	}
	return selection, nil
}

func provePopulationRoute(ctx context.Context, authorized AuthorizedCapability, rootResourceType, terminalResourceType string, route []capability.ConstructionRouteStep) bool {
	compilerRoute := make([]compilerprobe.Traversal, 0, len(route))
	for _, step := range route {
		matchMode := spec.TraversalMatchOptional
		if step.MatchMode == string(spec.TraversalMatchRequired) {
			matchMode = spec.TraversalMatchRequired
		}
		traversal := compilerprobe.Traversal{FromResourceType: step.FromResourceType, EdgeLabel: step.Relationship, ToResourceType: step.ToResourceType, MatchMode: matchMode}
		proof, err := compilerprobe.ProbeTraversal(ctx, compilerprobe.TraversalRequest{
			Scope: constructionCompilerScope(authorized), RootResourceType: step.FromResourceType, Traversal: traversal,
		})
		if err != nil || proof.Traversal == nil || string(proof.Traversal.StorageDirection) != step.StorageDirection {
			return false
		}
		compilerRoute = append(compilerRoute, traversal)
	}
	_, err := compilerprobe.ProbeCandidate(ctx, compilerprobe.CandidateRequest{
		Scope: constructionCompilerScope(authorized), RootResourceType: rootResourceType,
		ResourceType: terminalResourceType, FieldRef: terminalResourceType + ".id", Selector: "id", Route: compilerRoute,
	})
	return err == nil
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

func cloneConstructionRoute(route []capability.ConstructionRouteStep) []capability.ConstructionRouteStep {
	result := make([]capability.ConstructionRouteStep, len(route))
	copy(result, route)
	return result
}

func (s *Service) preparePopulationRouteChoices(ctx context.Context, project string, authorized AuthorizedCapability, identities []capability.PopulationRouteChoiceIdentity, workspace authoringv2.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
	if len(commands) == 0 || len(commands) != len(identities) {
		return nil, malformed("commands", "population route choices must resolve one-to-one with commands", nil)
	}
	snapshot := authorized.Snapshot
	for index := range commands {
		command := &commands[index]
		if command.Type != authoringv2.CommandSetTablePopulation || command.RouteChoiceID == "" || len(command.EdgeIDs) != 0 {
			return nil, malformed("commands", "population route choices require only SET_TABLE_POPULATION routeChoiceId commands", nil)
		}
		identity := identities[index]
		if identity.SnapshotToken != snapshot.Token || identity.OutputID != command.OutputID || identity.SelectionRevisionID != command.SelectionRevisionID {
			return nil, conflict("commands", "STALE_POPULATION_ROUTE", "the population route belongs to another table, selection, or catalog snapshot", nil, nil)
		}
		document := findSemanticOutput(workspace, command.OutputID)
		if document == nil || document.Route.OccurrenceID != authoringv2.RootOccurrenceID || document.Route.ResourceType != document.RootResourceType {
			return nil, malformed("commands", "outputId does not identify a valid row-rooted table", nil)
		}
		selection, err := s.getAuthorizedPopulationSelection(ctx, project, command.SelectionRevisionID, snapshot)
		if err != nil {
			return nil, err
		}
		targetNodeID := ""
		if len(identity.Route) > 0 {
			targetNodeID = identity.Route[len(identity.Route)-1].ToNodeID
		} else {
			for _, node := range snapshot.Nodes {
				if node.RowRootEligible && node.ResourceType == document.RootResourceType && node.ResourceType == selection.ResourceType {
					if targetNodeID != "" {
						return nil, invalidPopulationRoute("zero-hop row root is ambiguous")
					}
					targetNodeID = node.ID
				}
			}
		}
		route, err := reauthorizeConstructionRoute(snapshot, document.RootResourceType, targetNodeID, identity.Route)
		if err != nil || !provePopulationRoute(ctx, authorized, document.RootResourceType, selection.ResourceType, route) {
			return nil, invalidPopulationRoute("the complete population route is no longer authorized and compiler-proved")
		}
		expected, err := capability.NewPopulationRouteChoiceID(capability.PopulationRouteChoiceIdentity{
			Version: 1, SnapshotToken: snapshot.Token, OutputID: command.OutputID,
			SelectionRevisionID: selection.ID, Route: route,
		})
		if err != nil || expected != command.RouteChoiceID {
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

func invalidPopulationRoute(message string) error {
	return unprocessable("commands", "INVALID_POPULATION_ROUTE", message, fmt.Errorf("population route identity or proof changed"))
}
