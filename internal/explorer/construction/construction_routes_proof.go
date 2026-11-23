package construction

import (
	"context"
	"fmt"
	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	"reflect"
	"strings"

	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

func constructionRouteHasCompilerProof(ctx context.Context, authorized ports.AuthorizedCapability, route []capability.ConstructionRouteStep) bool {
	return constructionRouteHasCompilerProofCached(ctx, authorized, route, nil)
}

func constructionRouteHasCompilerProofCached(ctx context.Context, authorized ports.AuthorizedCapability, route []capability.ConstructionRouteStep, proofs map[capability.ConstructionRouteStep]bool) bool {
	if len(route) == 0 {
		return false
	}
	for _, hop := range route {
		proved, cached := proofs[hop]
		if !cached {
			matchMode := spec.TraversalMatchOptional
			if hop.MatchMode == string(spec.TraversalMatchRequired) {
				matchMode = spec.TraversalMatchRequired
			}
			proof, err := facade.ProbeTraversal(ctx, facade.TraversalRequest{
				Scope: constructionCompilerScope(authorized), RootResourceType: hop.FromResourceType,
				Traversal: facade.Traversal{
					FromResourceType: hop.FromResourceType, EdgeLabel: hop.Relationship,
					ToResourceType: hop.ToResourceType, MatchMode: matchMode,
				},
			})
			proved = err == nil && proof.Traversal != nil && string(proof.Traversal.StorageDirection) == hop.StorageDirection
			if proofs != nil {
				proofs[hop] = proved
			}
		}
		if !proved {
			return false
		}
	}
	return true
}

func proveConstructionCandidate(ctx context.Context, authorized ports.AuthorizedCapability, rootResourceType string, candidate capability.Candidate, route []capability.ConstructionRouteStep) (capability.Candidate, error) {
	compilerRoute := make([]facade.Traversal, 0, len(route))
	for index, step := range route {
		matchMode := spec.TraversalMatchOptional
		if step.MatchMode == string(spec.TraversalMatchRequired) {
			matchMode = spec.TraversalMatchRequired
		}
		traversal := facade.Traversal{
			FromResourceType: step.FromResourceType, EdgeLabel: step.Relationship,
			ToResourceType: step.ToResourceType, MatchMode: matchMode,
		}
		provenEdge, err := facade.ProbeTraversal(ctx, facade.TraversalRequest{
			Scope: constructionCompilerScope(authorized), RootResourceType: step.FromResourceType, Traversal: traversal,
		})
		if err != nil || provenEdge.Traversal == nil || string(provenEdge.Traversal.StorageDirection) != step.StorageDirection {
			return capability.Candidate{}, fmt.Errorf("route step %d compiler proof changed: %w", index, err)
		}
		compilerRoute = append(compilerRoute, traversal)
	}
	proof, err := facade.ProbeCandidate(ctx, facade.CandidateRequest{
		Scope: constructionCompilerScope(authorized), RootResourceType: rootResourceType,
		ResourceType: candidate.ResourceType, FieldRef: candidate.ResourceType + "." + candidate.FieldPath,
		Selector: candidate.FieldPath, Route: compilerRoute,
	})
	if err != nil || proof.Candidate == nil {
		if err == nil {
			err = fmt.Errorf("compiler returned no candidate proof")
		}
		return capability.Candidate{}, err
	}
	updated := candidate
	updated.LogicalType = string(proof.Candidate.Primitive)
	updated.Cardinality = string(proof.Candidate.Cardinality)
	updated.ProjectionModes = make([]capability.ProjectionMode, 0, len(proof.Candidate.ProjectionModes))
	for _, mode := range proof.Candidate.ProjectionModes {
		updated.ProjectionModes = append(updated.ProjectionModes, capability.ProjectionMode(strings.ToUpper(string(mode))))
	}
	return updated, nil
}

func constructionCompilerScope(authorized ports.AuthorizedCapability) facade.Scope {
	return facade.Scope{
		Project: authorized.Snapshot.Identity.Project, DatasetGeneration: authorized.Snapshot.Identity.Generation,
		AuthResourcePaths: append([]string(nil), authorized.Scope.AuthResourcePaths...), AuthScopeMode: authorized.Scope.Mode,
	}
}

func constructionChoiceIdentityMatches(choice capability.ConstructionChoice, submittedID string, identity capability.ConstructionChoiceIdentity) bool {
	if choice.ChoiceID == submittedID {
		return true
	}
	return identity.Version == "construction-choice/v1" && len(identity.Route) == 0 && reflect.DeepEqual(choice.Source, identity.Source)
}

func constructionRouteForOccurrence(snapshot capability.Snapshot, rootResourceType, targetNodeID string, authoredRoot authoringv2.RouteNode, occurrenceID string) ([]capability.ConstructionRouteStep, error) {
	path, found := routePathToOccurrence(authoredRoot, occurrenceID)
	if !found || len(path) == 0 || path[0].OccurrenceID != authoringv2.RootOccurrenceID || path[0].ResourceType != rootResourceType {
		return nil, fmt.Errorf("occurrence is not in this output's saved route")
	}
	var root capability.Node
	for _, node := range snapshot.Nodes {
		if !node.RowRootEligible || node.ResourceType != rootResourceType {
			continue
		}
		if root.ID != "" {
			return nil, fmt.Errorf("authorized row root is ambiguous")
		}
		root = node
	}
	if root.ID == "" {
		return nil, fmt.Errorf("authorized row root is unavailable")
	}
	if snapshot.Policy.Route.MaxHops > 0 && len(path)-1 > snapshot.Policy.Route.MaxHops {
		return nil, fmt.Errorf("saved route exceeds the current route policy")
	}
	if len(path) == 1 {
		if root.ID != targetNodeID {
			return nil, fmt.Errorf("root occurrence does not match the candidate terminal")
		}
		return []capability.ConstructionRouteStep{}, nil
	}

	var matches [][]capability.ConstructionRouteStep
	var visit func(capability.Node, int, []capability.ConstructionRouteStep, map[string]bool) error
	visit = func(current capability.Node, pathIndex int, steps []capability.ConstructionRouteStep, usedEdges map[string]bool) error {
		if len(matches) > 1 {
			return nil
		}
		if pathIndex == len(path) {
			if current.ID == targetNodeID {
				matches = append(matches, capability.CloneConstructionRoute(steps))
			}
			return nil
		}
		parent := path[pathIndex-1]
		authored := path[pathIndex]
		matchMode := string(authored.MatchMode.Normalized())
		edge, err := resolveCapabilityRouteEdge(snapshot, current.ID, parent, authored)
		if err != nil {
			return err
		}
		if !snapshot.Policy.Route.AllowsRepeatedEdges && usedEdges[edge.ID] {
			return fmt.Errorf("saved route repeats an edge forbidden by policy")
		}
		if !snapshot.Policy.Route.AllowsSelfLoops && edge.FromNodeID == edge.ToNodeID {
			return fmt.Errorf("saved route contains a self-loop forbidden by policy")
		}
		to, _ := snapshot.Node(edge.ToNodeID)
		next := append(capability.CloneConstructionRoute(steps), capability.ConstructionRouteStep{
			EdgeID: edge.ID, FromNodeID: edge.FromNodeID, ToNodeID: edge.ToNodeID,
			FromResourceType: edge.SourceResourceType, ToResourceType: edge.TargetResourceType,
			Relationship: edge.Label, StorageDirection: strings.ToUpper(strings.TrimSpace(edge.StorageDirection)), MatchMode: matchMode,
		})
		nextUsed := make(map[string]bool, len(usedEdges)+1)
		for id, used := range usedEdges {
			nextUsed[id] = used
		}
		nextUsed[edge.ID] = true
		if err := visit(to, pathIndex+1, next, nextUsed); err != nil {
			return err
		}
		return nil
	}
	if err := visit(root, 1, nil, map[string]bool{}); err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("saved route does not resolve to the exact candidate terminal")
	}
	if len(matches) != 1 {
		return nil, fmt.Errorf("saved route has multiple authorized capability edge resolutions")
	}
	return reauthorizeConstructionRoute(snapshot, rootResourceType, targetNodeID, matches[0])
}
