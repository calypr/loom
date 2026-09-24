package lifecycle

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/projectid"
)

const (
	automaticRouteLimit           = 50
	automaticRouteExpansionLimit  = 8192
	automaticRouteValueCheckLimit = 64
	automaticRouteMaxHops         = 10
)

func defaultCatalogRecordCohort(workspace authoringv2.Workspace, document authoringv2.Document) bool {
	return document.Rows.Kind == authoringv2.RowDefinitionRecords && document.Population == nil &&
		len(document.FixedFilters) == 0 && document.TableShape == nil && len(workspace.SharedFilters[document.Output.ID]) == 0 && !hasRequiredCatalogRoute(document.Route)
}

func hasRequiredCatalogRoute(node authoringv2.RouteNode) bool {
	for _, child := range node.Children {
		if child.MatchMode == authoringv2.RouteMatchRequired || hasRequiredCatalogRoute(child) {
			return true
		}
	}
	return false
}

func (s *Service) hasConstructionRouteValue(ctx context.Context, authorized AuthorizedCapability, workspace authoringv2.Workspace, document authoringv2.Document, sourceResourceType string, route []capability.ConstructionRouteStep, source catalog.RouteCoverageSource, buildID string) (bool, error) {
	if s.config.HasRouteValue == nil {
		return false, unavailable("construction-choices", "ROW_COVERAGE_UNAVAILABLE", "fast row-level route checks are not configured", nil)
	}
	if !defaultCatalogRecordCohort(workspace, document) {
		return false, unavailable("construction-choices", "ROW_COVERAGE_PENDING", "Loom has not measured this source against the selected or grouped table rows", nil)
	}
	steps := make([]catalog.RouteCoverageStep, 0, len(route))
	for _, step := range route {
		steps = append(steps, catalog.RouteCoverageStep{
			FromResourceType: step.FromResourceType, ToResourceType: step.ToResourceType,
			Relationship: step.Relationship, StorageDirection: step.StorageDirection,
		})
	}
	exists, err := s.config.HasRouteValue(ctx, catalog.RouteCoverageOptions{
		Project: projectid.Legacy(authorized.Snapshot.Identity.Project), DatasetGeneration: authorized.Snapshot.Identity.Generation,
		BuildID: buildID, AuthResourcePaths: append([]string(nil), authorized.Scope.AuthResourcePaths...),
		AuthResourcePathsUnrestricted: authorized.Scope.Mode == authscope.ReadScopeUnrestricted,
		RootResourceType:              document.RootResourceType, SourceResourceType: sourceResourceType, Route: steps, Source: source,
	})
	if err != nil {
		return false, unavailable("construction-choices", "ROW_COVERAGE_UNAVAILABLE", "Loom could not check this source on the current table rows", err)
	}
	return exists, nil
}

type automaticRouteCursor struct {
	Version   int      `json:"version"`
	Context   string   `json:"context"`
	Depth     int      `json:"depth"`
	Path      []string `json:"path"`
	NextEdges []int    `json:"nextEdges"`
}

type automaticRouteFrame struct {
	NodeID   string
	NextEdge int
}

type automaticRouteIterator struct {
	rootID           string
	targetNodeID     string
	maxHops          int
	policy           capability.RoutePolicy
	adjacency        map[string][]capability.Edge
	depth            int
	path             []capability.ConstructionRouteStep
	frames           []automaticRouteFrame
	usedEdges        map[string]bool
	context          string
	zeroPending      bool
	finished         bool
	horizonTruncated bool
}

func newObservedAutomaticRouteIterator(snapshot capability.Snapshot, rootResource, targetNodeID, context, cursor string) (*automaticRouteIterator, error) {
	var root capability.Node
	for _, node := range snapshot.Nodes {
		if node.RowRootEligible && node.ResourceType == rootResource {
			if root.ID != "" {
				return nil, fmt.Errorf("row root %q is ambiguous", rootResource)
			}
			root = node
		}
	}
	if root.ID == "" {
		return nil, fmt.Errorf("row root %q is unavailable", rootResource)
	}
	if _, ok := snapshot.Node(targetNodeID); !ok {
		return nil, fmt.Errorf("source node %q is unavailable", targetNodeID)
	}
	maxHops := automaticRouteMaxHops
	if snapshot.Policy.Route.MaxHops > 0 && snapshot.Policy.Route.MaxHops < maxHops {
		maxHops = snapshot.Policy.Route.MaxHops
	}
	adjacency := make(map[string][]capability.Edge)
	for _, edge := range snapshot.Edges {
		if edge.ID == "" || edge.BlockedReason != "" || edge.ObservedEdgeCount <= 0 || edge.FromNodeID == edge.ToNodeID && !snapshot.Policy.Route.AllowsSelfLoops {
			continue
		}
		if edge.StorageDirection != "INBOUND" && edge.StorageDirection != "OUTBOUND" {
			continue
		}
		adjacency[edge.FromNodeID] = append(adjacency[edge.FromNodeID], edge)
	}
	for nodeID := range adjacency {
		sort.Slice(adjacency[nodeID], func(i, j int) bool { return adjacency[nodeID][i].ID < adjacency[nodeID][j].ID })
	}
	iterator := &automaticRouteIterator{
		rootID: root.ID, targetNodeID: targetNodeID, maxHops: maxHops,
		policy: snapshot.Policy.Route, adjacency: adjacency, depth: 1,
		frames: []automaticRouteFrame{{NodeID: root.ID}}, usedEdges: map[string]bool{},
		context: context, zeroPending: root.ID == targetNodeID,
	}
	if cursor != "" {
		state, err := decodeAutomaticRouteCursor(cursor, context, maxHops)
		if err != nil {
			return nil, err
		}
		if err := iterator.restore(state); err != nil {
			return nil, err
		}
	}
	return iterator, nil
}

func (i *automaticRouteIterator) next(edgeBudget *int) ([]capability.ConstructionRouteStep, bool, error) {
	if i.zeroPending {
		i.zeroPending = false
		i.finished = true
		return []capability.ConstructionRouteStep{}, true, nil
	}
	for !i.finished {
		if len(i.frames) == 0 {
			if i.depth >= i.maxHops {
				i.finished = true
				return nil, false, nil
			}
			i.depth++
			i.frames = []automaticRouteFrame{{NodeID: i.rootID}}
			i.path = nil
			i.usedEdges = map[string]bool{}
			continue
		}
		top := &i.frames[len(i.frames)-1]
		if top.NodeID == i.targetNodeID {
			if len(i.path) == i.depth {
				route := append([]capability.ConstructionRouteStep(nil), i.path...)
				i.pop()
				return route, true, nil
			}
			i.pop()
			continue
		}
		if len(i.path) == i.depth {
			if i.depth == i.maxHops && (i.policy.MaxHops == 0 || i.policy.MaxHops > i.maxHops) && top.NextEdge < len(i.adjacency[top.NodeID]) {
				if *edgeBudget <= 0 {
					return nil, false, nil
				}
				edge := i.adjacency[top.NodeID][top.NextEdge]
				top.NextEdge++
				*edgeBudget--
				if i.allows(edge) {
					i.horizonTruncated = true
				}
				continue
			}
			i.pop()
			continue
		}
		if top.NextEdge >= len(i.adjacency[top.NodeID]) {
			i.pop()
			continue
		}
		if *edgeBudget <= 0 {
			return nil, false, nil
		}
		edge := i.adjacency[top.NodeID][top.NextEdge]
		top.NextEdge++
		*edgeBudget--
		if !i.allows(edge) {
			continue
		}
		step := capability.ConstructionRouteStep{
			EdgeID: edge.ID, FromNodeID: edge.FromNodeID, ToNodeID: edge.ToNodeID,
			FromResourceType: edge.SourceResourceType, ToResourceType: edge.TargetResourceType,
			Relationship: edge.Label, StorageDirection: strings.ToUpper(edge.StorageDirection), MatchMode: "OPTIONAL",
		}
		i.path = append(i.path, step)
		i.usedEdges[edge.ID] = true
		i.frames = append(i.frames, automaticRouteFrame{NodeID: edge.ToNodeID})
	}
	return nil, false, nil
}

func (i *automaticRouteIterator) allows(edge capability.Edge) bool {
	if !i.policy.AllowsSelfLoops && edge.FromNodeID == edge.ToNodeID {
		return false
	}
	if !i.policy.AllowsRepeatedEdges && i.usedEdges[edge.ID] {
		return false
	}
	pathIDs := make([]string, 0, len(i.path)+1)
	for _, step := range i.path {
		pathIDs = append(pathIDs, step.EdgeID)
	}
	return i.policy.Allows(append(pathIDs, edge.ID))
}

func (i *automaticRouteIterator) pop() {
	if len(i.frames) == 0 {
		return
	}
	i.frames = i.frames[:len(i.frames)-1]
	if len(i.path) == 0 {
		return
	}
	edgeID := i.path[len(i.path)-1].EdgeID
	i.path = i.path[:len(i.path)-1]
	delete(i.usedEdges, edgeID)
}

func (i *automaticRouteIterator) cursor() (string, error) {
	if i.finished {
		return "", nil
	}
	state := automaticRouteCursor{Version: 1, Context: i.context, Depth: i.depth}
	for _, step := range i.path {
		state.Path = append(state.Path, step.EdgeID)
	}
	for _, frame := range i.frames {
		state.NextEdges = append(state.NextEdges, frame.NextEdge)
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return "ar1." + base64.RawURLEncoding.EncodeToString(raw) + "." + hex.EncodeToString(digest[:]), nil
}

func (i *automaticRouteIterator) restore(state automaticRouteCursor) error {
	if state.Depth < 1 || state.Depth > i.maxHops || len(state.Path) > state.Depth || len(state.NextEdges) != len(state.Path)+1 || state.NextEdges[0] < 0 || state.NextEdges[0] > len(i.adjacency[i.rootID]) {
		return fmt.Errorf("automatic route cursor is invalid")
	}
	i.depth = state.Depth
	i.path = nil
	i.frames = []automaticRouteFrame{{NodeID: i.rootID, NextEdge: state.NextEdges[0]}}
	i.usedEdges = map[string]bool{}
	currentNodeID := i.rootID
	for index, edgeID := range state.Path {
		edges := i.adjacency[currentNodeID]
		parentNext := state.NextEdges[index]
		if parentNext < 1 || parentNext > len(edges) {
			return fmt.Errorf("automatic route cursor is invalid")
		}
		edge := edges[parentNext-1]
		if edge.ID != edgeID || !i.allows(edge) || edge.FromNodeID != currentNodeID || currentNodeID == i.targetNodeID {
			return fmt.Errorf("automatic route cursor is invalid")
		}
		i.path = append(i.path, capability.ConstructionRouteStep{
			EdgeID: edge.ID, FromNodeID: edge.FromNodeID, ToNodeID: edge.ToNodeID,
			FromResourceType: edge.SourceResourceType, ToResourceType: edge.TargetResourceType,
			Relationship: edge.Label, StorageDirection: strings.ToUpper(edge.StorageDirection), MatchMode: "OPTIONAL",
		})
		i.usedEdges[edge.ID] = true
		currentNodeID = edge.ToNodeID
		nextEdge := state.NextEdges[index+1]
		if nextEdge < 0 || nextEdge > len(i.adjacency[currentNodeID]) {
			return fmt.Errorf("automatic route cursor is invalid")
		}
		i.frames = append(i.frames, automaticRouteFrame{NodeID: currentNodeID, NextEdge: nextEdge})
	}
	if currentNodeID == i.targetNodeID && len(i.path) < i.depth {
		return fmt.Errorf("automatic route cursor is invalid")
	}
	return nil
}

func decodeAutomaticRouteCursor(value, context string, maxHops int) (automaticRouteCursor, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 3 || parts[0] != "ar1" || len(value) > 4096 {
		return automaticRouteCursor{}, fmt.Errorf("automatic route cursor is invalid")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return automaticRouteCursor{}, fmt.Errorf("automatic route cursor is invalid")
	}
	provided, err := hex.DecodeString(parts[2])
	if err != nil || len(provided) != sha256.Size {
		return automaticRouteCursor{}, fmt.Errorf("automatic route cursor is invalid")
	}
	digest := sha256.Sum256(raw)
	if subtle.ConstantTimeCompare(provided, digest[:]) != 1 {
		return automaticRouteCursor{}, fmt.Errorf("automatic route cursor is invalid")
	}
	var state automaticRouteCursor
	if err := json.Unmarshal(raw, &state); err != nil || state.Version != 1 || state.Context != context || state.Depth < 1 || state.Depth > maxHops || len(state.Path) > state.Depth || len(state.NextEdges) != len(state.Path)+1 {
		return automaticRouteCursor{}, fmt.Errorf("automatic route cursor is stale or invalid")
	}
	return state, nil
}
