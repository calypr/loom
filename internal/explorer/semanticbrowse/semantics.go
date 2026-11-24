package semanticbrowse

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	"github.com/calypr/loom/internal/explorer/semanticauthoring"
	"github.com/calypr/loom/internal/projectid"
	"github.com/calypr/loom/internal/semantic"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/capability"
)

func semanticChoiceKey(conceptID, bindingID string) string {
	return bindingID + "\x00" + conceptID
}

func constructionChoiceSupports(choice capability.ConstructionChoice, form capability.ConstructionChoiceForm) bool {
	for _, option := range choice.Options {
		if option.Form == form {
			return true
		}
	}
	return false
}

func constructionCompilerScope(authorized ports.AuthorizedCapability) facade.Scope {
	return facade.Scope{Project: authorized.Snapshot.Identity.Project, DatasetGeneration: authorized.Snapshot.Identity.Generation, AuthResourcePaths: append([]string(nil), authorized.Scope.AuthResourcePaths...), AuthScopeMode: authorized.Scope.Mode}
}

func proveConstructionCandidate(ctx context.Context, authorized ports.AuthorizedCapability, root string, candidate capability.Candidate, route []capability.ConstructionRouteStep) (capability.Candidate, error) {
	compilerRoute := make([]facade.Traversal, 0, len(route))
	for index, step := range route {
		match := spec.TraversalMatchOptional
		if step.MatchMode == string(spec.TraversalMatchRequired) {
			match = spec.TraversalMatchRequired
		}
		traversal := facade.Traversal{FromResourceType: step.FromResourceType, EdgeLabel: step.Relationship, ToResourceType: step.ToResourceType, MatchMode: match}
		proof, err := facade.ProbeTraversal(ctx, facade.TraversalRequest{Scope: constructionCompilerScope(authorized), RootResourceType: step.FromResourceType, Traversal: traversal})
		if err != nil || proof.Traversal == nil || string(proof.Traversal.StorageDirection) != step.StorageDirection {
			return capability.Candidate{}, fmt.Errorf("route step %d compiler proof changed: %w", index, err)
		}
		compilerRoute = append(compilerRoute, traversal)
	}
	proof, err := facade.ProbeCandidate(ctx, facade.CandidateRequest{Scope: constructionCompilerScope(authorized), RootResourceType: root, ResourceType: candidate.ResourceType, FieldRef: candidate.ResourceType + "." + candidate.FieldPath, Selector: candidate.FieldPath, Route: compilerRoute})
	if err != nil || proof.Candidate == nil {
		if err == nil {
			err = fmt.Errorf("compiler returned no candidate proof")
		}
		return capability.Candidate{}, err
	}
	candidate.LogicalType = string(proof.Candidate.Primitive)
	candidate.Cardinality = string(proof.Candidate.Cardinality)
	return candidate, nil
}

func constructionRouteForOccurrence(snapshot capability.Snapshot, rootResourceType, targetNodeID string, authoredRoot ws.RouteNode, occurrenceID string) ([]capability.ConstructionRouteStep, error) {
	path := []ws.RouteNode{}
	var walk func(ws.RouteNode, []ws.RouteNode) bool
	walk = func(node ws.RouteNode, prefix []ws.RouteNode) bool {
		prefix = append(append([]ws.RouteNode(nil), prefix...), node)
		if node.OccurrenceID == occurrenceID {
			path = prefix
			return true
		}
		for _, child := range node.Children {
			if walk(child, prefix) {
				return true
			}
		}
		return false
	}
	if !walk(authoredRoot, nil) || len(path) == 0 || path[0].OccurrenceID != ws.RootOccurrenceID || path[0].ResourceType != rootResourceType {
		return nil, fmt.Errorf("occurrence is not in this output's saved route")
	}
	var root capability.Node
	for _, node := range snapshot.Nodes {
		if node.RowRootEligible && node.ResourceType == rootResourceType {
			if root.ID != "" {
				return nil, fmt.Errorf("table row root is ambiguous")
			}
			root = node
		}
	}
	if root.ID == "" {
		return nil, fmt.Errorf("table row root is unavailable")
	}
	current := root
	route := make([]capability.ConstructionRouteStep, 0, len(path)-1)
	for i := 1; i < len(path); i++ {
		child := path[i]
		var match *capability.Edge
		var target capability.Node
		for ei := range snapshot.Edges {
			edge := snapshot.Edges[ei]
			next, ok := snapshot.MatchPopulationRouteEdge(edge, current.ID, current.ResourceType, child.ResourceType, child.Relationship, "")
			if !ok {
				continue
			}
			if match != nil {
				return nil, fmt.Errorf("saved route is ambiguous")
			}
			e := edge
			match = &e
			target = next
		}
		if match == nil {
			return nil, fmt.Errorf("saved route edge is unavailable")
		}
		route = append(route, capability.ConstructionRouteStep{EdgeID: match.ID, FromNodeID: current.ID, ToNodeID: target.ID, FromResourceType: current.ResourceType, ToResourceType: target.ResourceType, Relationship: match.Label, StorageDirection: match.StorageDirection, MatchMode: string(child.MatchMode)})
		current = target
	}
	if current.ID != targetNodeID {
		return nil, fmt.Errorf("saved route does not reach the requested node")
	}
	return route, nil
}

var _ = authscope.ReadScopeUnrestricted
var _ = projectid.Canonical
var _ = semanticauthoring.SemanticReadinessUnsupported
var _ = semantic.Complete
var _ = strings.TrimSpace
var _ = support.Malformed
