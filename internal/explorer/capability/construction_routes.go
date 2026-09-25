package capability

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	ConstructionRouteDefaultLimit  = 20
	ConstructionRouteMaxLimit      = 50
	constructionRouteMaxHops       = 5
	constructionRouteMaxExpansions = 30000
	constructionRouteMaxResults    = 4096
)

// ConstructionRouteSearch is an exact server-side route query. TargetNodeID
// and SourceKey are derived from the selected, authorized source at the
// lifecycle boundary; callers must not use this planner as a path authority.
type ConstructionRouteSearch struct {
	Snapshot     Snapshot
	RootResource string
	TargetNodeID string
	// TargetNodeIDs supports route searches whose terminal is one of several
	// equivalent capability nodes (for example a population's resource type).
	// It is mutually exclusive with TargetNodeID.
	TargetNodeIDs []string
	SourceKey     string
	Cursor        string
	Limit         int
}

type ConstructionRoutePage struct {
	Routes     [][]ConstructionRouteStep
	Complete   bool
	Truncated  bool
	NextCursor string
}

type constructionRouteCursor struct {
	Version    int    `json:"version"`
	Snapshot   string `json:"snapshot"`
	RootNodeID string `json:"rootNodeId"`
	TargetKey  string `json:"targetKey"`
	SourceKey  string `json:"sourceKey"`
	Offset     int    `json:"offset"`
}

// PlanConstructionRoutes enumerates directed, policy-allowed routes in
// shortest-first, stable edge-identity order. The five-hop default is an
// explicit search budget when policy is unbounded; reaching that horizon is
// reported as truncated.
func PlanConstructionRoutes(request ConstructionRouteSearch) (ConstructionRoutePage, error) {
	page := ConstructionRoutePage{Routes: [][]ConstructionRouteStep{}}
	snapshot := request.Snapshot
	if err := snapshot.ValidateToken(snapshot.Token); err != nil {
		return page, err
	}
	if strings.TrimSpace(request.RootResource) == "" || strings.TrimSpace(request.SourceKey) == "" ||
		(request.TargetNodeID == "" && len(request.TargetNodeIDs) == 0) || (request.TargetNodeID != "" && len(request.TargetNodeIDs) != 0) {
		return page, fmt.Errorf("route search requires a row root, target node, and exact source identity")
	}
	limit := request.Limit
	if limit == 0 {
		limit = ConstructionRouteDefaultLimit
	}
	if limit < 1 || limit > ConstructionRouteMaxLimit {
		return page, fmt.Errorf("route search limit must be between 1 and %d", ConstructionRouteMaxLimit)
	}
	root, ok := uniqueRootNode(snapshot, request.RootResource)
	if !ok {
		return page, fmt.Errorf("row root %q is not unique in the capability snapshot", request.RootResource)
	}
	targetIDs := append([]string(nil), request.TargetNodeIDs...)
	if request.TargetNodeID != "" {
		targetIDs = []string{request.TargetNodeID}
	}
	sort.Strings(targetIDs)
	targetSet := make(map[string]bool, len(targetIDs))
	for _, targetID := range targetIDs {
		if strings.TrimSpace(targetID) == "" || targetSet[targetID] {
			return page, fmt.Errorf("route target nodes must be unique and non-empty")
		}
		if _, ok := snapshot.Node(targetID); !ok {
			return page, fmt.Errorf("route target node %q is not in the capability snapshot", targetID)
		}
		targetSet[targetID] = true
	}
	targetKey := constructionRouteTargetKey(targetIDs)
	maxHops := constructionRouteMaxHops
	if snapshot.Policy.Route.MaxHops > 0 && snapshot.Policy.Route.MaxHops < maxHops {
		maxHops = snapshot.Policy.Route.MaxHops
	}
	cursor, err := decodeConstructionRouteCursor(request.Cursor)
	if err != nil {
		return page, err
	}
	offset := 0
	if request.Cursor != "" {
		if cursor.Snapshot != snapshot.Token || cursor.RootNodeID != root.ID || cursor.TargetKey != targetKey || cursor.SourceKey != request.SourceKey {
			return page, fmt.Errorf("construction route cursor is stale or belongs to another source")
		}
		offset = cursor.Offset
	}

	adjacency := make(map[string][]Edge)
	for _, edge := range snapshot.Edges {
		if edge.BlockedReason != "" || edge.ID == "" || edge.FromNodeID == "" || edge.ToNodeID == "" || edge.Label == "" {
			continue
		}
		from, fromOK := snapshot.Node(edge.FromNodeID)
		to, toOK := snapshot.Node(edge.ToNodeID)
		if !fromOK || !toOK || from.ResourceType != edge.SourceResourceType || to.ResourceType != edge.TargetResourceType {
			continue
		}
		adjacency[edge.FromNodeID] = append(adjacency[edge.FromNodeID], edge)
	}
	for nodeID := range adjacency {
		sort.Slice(adjacency[nodeID], func(i, j int) bool {
			left, right := adjacency[nodeID][i], adjacency[nodeID][j]
			return routeEdgeKey(left) < routeEdgeKey(right)
		})
	}

	routes := make([][]ConstructionRouteStep, 0, min(limit+1, constructionRouteMaxResults))
	type routeState struct {
		nodeID string
		path   []ConstructionRouteStep
	}
	// Stable-sorted adjacency and FIFO expansion keep hop layers in edge-key order.
	states := []routeState{{nodeID: root.ID}}
	expansions := 0
	searchTruncated := false
	resultsCapped := false
	expansionCapped := false
	recordRoute := func(path []ConstructionRouteStep) bool {
		if len(routes) == constructionRouteMaxResults {
			resultsCapped = true
			searchTruncated = true
			return false
		}
		routes = append(routes, cloneConstructionRoute(path))
		return true
	}
	for stateIndex := 0; stateIndex < len(states); stateIndex++ {
		state := states[stateIndex]
		hops := len(state.path)
		if targetSet[state.nodeID] && !recordRoute(state.path) {
			break
		}
		if hops >= maxHops {
			policyAllowsDeeperRoute := snapshot.Policy.Route.MaxHops == 0 || snapshot.Policy.Route.MaxHops > maxHops
			if maxHops == constructionRouteMaxHops && policyAllowsDeeperRoute && canExtendRoute(adjacency[state.nodeID], state.nodeID, snapshot.Policy.Route, state.path) {
				searchTruncated = true
			}
			continue
		}
		for _, edge := range adjacency[state.nodeID] {
			if !snapshot.Policy.Route.Allows(append(routeEdgeIDs(state.path), edge.ID)) {
				continue
			}
			if edge.FromNodeID == edge.ToNodeID && !snapshot.Policy.Route.AllowsSelfLoops {
				continue
			}
			if !snapshot.Policy.Route.AllowsRepeatedEdges && routeContainsEdge(state.path, edge.ID) {
				continue
			}
			expansions++
			path := append(cloneConstructionRoute(state.path), ConstructionRouteStep{
				EdgeID: edge.ID, FromNodeID: edge.FromNodeID, ToNodeID: edge.ToNodeID,
				FromResourceType: edge.SourceResourceType, ToResourceType: edge.TargetResourceType,
				Relationship: edge.Label, StorageDirection: strings.ToUpper(edge.StorageDirection), MatchMode: "OPTIONAL",
			})
			states = append(states, routeState{nodeID: edge.ToNodeID, path: path})
			if expansions >= constructionRouteMaxExpansions {
				searchTruncated = true
				expansionCapped = true
				break
			}
		}
		if resultsCapped {
			break
		}
		if expansionCapped {
			// Count target routes already reached by the final allowed expansion.
			// The pending states are still in hop and edge-identity order.
			for _, pending := range states[stateIndex+1:] {
				if targetSet[pending.nodeID] && !recordRoute(pending.path) {
					break
				}
			}
			break
		}
	}

	start := min(offset, len(routes))
	end := min(start+limit, len(routes))
	for _, route := range routes[start:end] {
		page.Routes = append(page.Routes, cloneConstructionRoute(route))
	}
	moreKnown := end < len(routes)
	page.Truncated = searchTruncated || moreKnown
	page.Complete = !page.Truncated
	if moreKnown {
		page.NextCursor, err = encodeConstructionRouteCursor(constructionRouteCursor{
			Version: 1, Snapshot: snapshot.Token, RootNodeID: root.ID, TargetKey: targetKey,
			SourceKey: request.SourceKey, Offset: end,
		})
		if err != nil {
			return ConstructionRoutePage{}, err
		}
	}
	return page, nil
}

func constructionRouteTargetKey(targetIDs []string) string {
	canonical, _ := json.Marshal(targetIDs)
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:])
}

func uniqueRootNode(snapshot Snapshot, resourceType string) (Node, bool) {
	var result Node
	for _, node := range snapshot.Nodes {
		if node.ResourceType != resourceType || !node.RowRootEligible {
			continue
		}
		if result.ID != "" {
			return Node{}, false
		}
		result = node
	}
	return result, result.ID != ""
}

func canExtendRoute(edges []Edge, nodeID string, policy RoutePolicy, route []ConstructionRouteStep) bool {
	for _, edge := range edges {
		if edge.FromNodeID != nodeID || (!policy.AllowsSelfLoops && edge.FromNodeID == edge.ToNodeID) || (!policy.AllowsRepeatedEdges && routeContainsEdge(route, edge.ID)) {
			continue
		}
		return true
	}
	return false
}

func routeContainsEdge(route []ConstructionRouteStep, edgeID string) bool {
	for _, step := range route {
		if step.EdgeID == edgeID {
			return true
		}
	}
	return false
}

func routeEdgeIDs(route []ConstructionRouteStep) []string {
	ids := make([]string, 0, len(route)+1)
	for _, step := range route {
		ids = append(ids, step.EdgeID)
	}
	return ids
}

func routeEdgeKey(edge Edge) string {
	return strings.Join([]string{edge.ID, edge.FromNodeID, edge.ToNodeID, edge.Label, edge.StorageDirection}, "\x00")
}

func encodeConstructionRouteCursor(cursor constructionRouteCursor) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return "cr1." + base64.RawURLEncoding.EncodeToString(raw) + "." + hex.EncodeToString(digest[:]), nil
}

func decodeConstructionRouteCursor(value string) (constructionRouteCursor, error) {
	if value == "" {
		return constructionRouteCursor{}, nil
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 || parts[0] != "cr1" || len(value) > 4096 {
		return constructionRouteCursor{}, fmt.Errorf("construction route cursor is invalid")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return constructionRouteCursor{}, fmt.Errorf("construction route cursor is invalid")
	}
	provided, err := hex.DecodeString(parts[2])
	if err != nil || len(provided) != sha256.Size {
		return constructionRouteCursor{}, fmt.Errorf("construction route cursor is invalid")
	}
	digest := sha256.Sum256(raw)
	if subtle.ConstantTimeCompare(provided, digest[:]) != 1 {
		return constructionRouteCursor{}, fmt.Errorf("construction route cursor is invalid")
	}
	var cursor constructionRouteCursor
	if err := json.Unmarshal(raw, &cursor); err != nil || cursor.Version != 1 || cursor.Snapshot == "" || cursor.RootNodeID == "" || cursor.TargetKey == "" || cursor.SourceKey == "" || cursor.Offset < 0 {
		return constructionRouteCursor{}, fmt.Errorf("construction route cursor is invalid")
	}
	return cursor, nil
}
