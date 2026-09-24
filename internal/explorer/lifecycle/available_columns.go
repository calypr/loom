package lifecycle

import (
	"context"
	"fmt"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/projectid"
)

type availableColumnRoutes map[catalog.AvailabilityFeature][]capability.ConstructionRouteStep

func (s *Service) availableColumnRoutes(ctx context.Context, authorized AuthorizedCapability, workspace authoringv2.Workspace, outputID string) (availableColumnRoutes, catalog.SemanticInventoryState, error) {
	document := findSemanticOutput(workspace, outputID)
	if document == nil {
		return nil, "", malformed("feature-catalog", "the selected table no longer exists", nil)
	}
	snapshot := authorized.Snapshot
	policy := snapshot.Policy.Route
	if !policy.AllowsRepeatedEdges {
		return nil, "", unavailable("feature-catalog", "AVAILABILITY_POLICY_UNSUPPORTED", "the route policy requires history-aware availability search", nil)
	}
	query := catalog.AvailabilityQuery{
		RootResourceType:  document.RootResourceType,
		AllRoots:          defaultCatalogRecordCohort(workspace, *document),
		Unrestricted:      authorized.Scope.Mode == authscope.ReadScopeUnrestricted,
		AuthResourcePaths: append([]string(nil), authorized.Scope.AuthResourcePaths...),
		MaxHops:           policy.MaxHops,
	}
	if !query.AllRoots {
		if s.config.AvailabilityRoots == nil {
			return nil, "", unavailable("feature-catalog", "AVAILABILITY_COHORT_UNAVAILABLE", "the selected table's root membership could not be resolved", nil)
		}
		var err error
		query.RootIDs, err = s.config.AvailabilityRoots(ctx, authorized, workspace, outputID)
		if err != nil {
			return nil, "", err
		}
	}
	edges := make(map[catalog.AvailabilityRelation]capability.Edge)
	for _, edge := range snapshot.Edges {
		if edge.BlockedReason != "" || edge.ObservedEdgeCount <= 0 || edge.ID == "" || !policy.AllowsSelfLoops && edge.FromNodeID == edge.ToNodeID {
			continue
		}
		from, fromOK := snapshot.Node(edge.FromNodeID)
		to, toOK := snapshot.Node(edge.ToNodeID)
		if !fromOK || !toOK || edge.StorageDirection != "OUTBOUND" && edge.StorageDirection != "INBOUND" {
			continue
		}
		relation := catalog.AvailabilityRelation{FromResourceType: from.ResourceType, ToResourceType: to.ResourceType, Relationship: edge.Label, StorageDirection: edge.StorageDirection}
		if prior, exists := edges[relation]; exists && prior.ID != edge.ID {
			return nil, "", unavailable("feature-catalog", "AMBIGUOUS_AVAILABILITY_RELATION", "the catalog contains duplicate relationship identities", nil)
		}
		edges[relation] = edge
		query.Relations = append(query.Relations, relation)
	}
	result, err := s.config.AvailableColumns(ctx, catalog.AvailabilityOptions{
		Project: projectid.Legacy(snapshot.Identity.Project), DatasetGeneration: snapshot.Identity.Generation, Query: query,
	})
	if err != nil {
		return nil, "", unavailable("feature-catalog", "AVAILABILITY_FAILED", "available columns could not be prepared", err)
	}
	if result.State != catalog.SemanticInventoryComplete {
		return nil, result.State, nil
	}
	routes := make(availableColumnRoutes, len(result.Witnesses))
	for _, witness := range result.Witnesses {
		route := make([]capability.ConstructionRouteStep, 0, len(witness.Route))
		for _, relation := range witness.Route {
			edge, exists := edges[relation]
			if !exists {
				return nil, "", fmt.Errorf("availability witness contains an unapproved relationship")
			}
			route = append(route, capability.ConstructionRouteStep{
				EdgeID: edge.ID, FromNodeID: edge.FromNodeID, ToNodeID: edge.ToNodeID,
				FromResourceType: relation.FromResourceType, ToResourceType: relation.ToResourceType,
				Relationship: relation.Relationship, StorageDirection: relation.StorageDirection, MatchMode: "OPTIONAL",
			})
		}
		routes[witness.Feature] = route
	}
	return routes, result.State, nil
}

func availableFieldChoice(ctx context.Context, authorized AuthorizedCapability, root string, candidate capability.Candidate, route []capability.ConstructionRouteStep) (capability.ConstructionChoice, error) {
	route, err := reauthorizeConstructionRoute(authorized.Snapshot, root, candidate.NodeID, route)
	if err != nil {
		return capability.ConstructionChoice{}, err
	}
	candidate, err = proveConstructionCandidate(ctx, authorized, root, candidate, route)
	if err != nil {
		return capability.ConstructionChoice{}, err
	}
	return capability.NewFieldConstructionChoiceForRoute(authorized.Snapshot.Token, route, candidate)
}
