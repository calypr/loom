package capability

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
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

var (
	ErrCanonicalConstructionRouteUnavailable = errors.New("no observed construction route is available")
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

// PlanConstructionRoutes enumerates directed, policy-allowed routes in stable
// edge-identity order. It preserves every distinct route and never prefers a
// shorter path. The five-hop default is an explicit search budget when policy
// is unbounded; reaching that horizon is reported as truncated.
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
	path := make([]ConstructionRouteStep, 0, maxHops)
	usedEdges := make(map[string]bool, maxHops)
	expansions := 0
	searchTruncated := false
	resultsCapped := false
	var walk func(nodeID string, hops int)
	walk = func(nodeID string, hops int) {
		if resultsCapped || expansions >= constructionRouteMaxExpansions {
			searchTruncated = true
			return
		}
		if targetSet[nodeID] {
			if len(routes) < constructionRouteMaxResults {
				routes = append(routes, cloneConstructionRoute(path))
			} else {
				resultsCapped = true
				searchTruncated = true
				return
			}
		}
		if hops >= maxHops {
			policyAllowsDeeperRoute := snapshot.Policy.Route.MaxHops == 0 || snapshot.Policy.Route.MaxHops > maxHops
			if maxHops == constructionRouteMaxHops && policyAllowsDeeperRoute && canExtendRoute(adjacency[nodeID], nodeID, snapshot.Policy.Route, usedEdges) {
				searchTruncated = true
			}
			return
		}
		for _, edge := range adjacency[nodeID] {
			if !snapshot.Policy.Route.Allows(append(routeEdgeIDs(path), edge.ID)) {
				continue
			}
			if edge.FromNodeID == edge.ToNodeID && !snapshot.Policy.Route.AllowsSelfLoops {
				continue
			}
			if usedEdges[edge.ID] && !snapshot.Policy.Route.AllowsRepeatedEdges {
				continue
			}
			expansions++
			usedEdges[edge.ID] = true
			path = append(path, ConstructionRouteStep{
				EdgeID: edge.ID, FromNodeID: edge.FromNodeID, ToNodeID: edge.ToNodeID,
				FromResourceType: edge.SourceResourceType, ToResourceType: edge.TargetResourceType,
				Relationship: edge.Label, StorageDirection: strings.ToUpper(edge.StorageDirection), MatchMode: "OPTIONAL",
			})
			walk(edge.ToNodeID, hops+1)
			path = path[:len(path)-1]
			delete(usedEdges, edge.ID)
			if resultsCapped || expansions >= constructionRouteMaxExpansions {
				searchTruncated = true
				return
			}
		}
	}
	walk(root.ID, 0)

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

type canonicalRouteCandidate struct {
	route    []ConstructionRouteStep
	observed []int64
	identity string
}

// PlanCanonicalConstructionRoute returns the ranked shortest route through
// observed capability edges. It compares observed relationship counts from the
// row root outward, then uses stable edge identity for reproducibility.
func PlanCanonicalConstructionRoute(snapshot Snapshot, rootResource, targetNodeID string) ([]ConstructionRouteStep, error) {
	if err := snapshot.ValidateToken(snapshot.Token); err != nil {
		return nil, err
	}
	root, ok := uniqueRootNode(snapshot, strings.TrimSpace(rootResource))
	if !ok {
		return nil, fmt.Errorf("row root %q is not unique in the capability snapshot", rootResource)
	}
	if _, ok := snapshot.Node(targetNodeID); !ok {
		return nil, fmt.Errorf("route target node %q is not in the capability snapshot", targetNodeID)
	}
	if root.ID == targetNodeID {
		return []ConstructionRouteStep{}, nil
	}

	adjacency := make(map[string][]Edge)
	for _, edge := range snapshot.Edges {
		if edge.BlockedReason != "" || edge.ObservedEdgeCount <= 0 || edge.ID == "" || edge.FromNodeID == "" || edge.ToNodeID == "" || edge.Label == "" {
			continue
		}
		direction := strings.ToUpper(strings.TrimSpace(edge.StorageDirection))
		if direction != "INBOUND" && direction != "OUTBOUND" {
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
			return routeEdgeKey(adjacency[nodeID][i]) < routeEdgeKey(adjacency[nodeID][j])
		})
	}

	best := map[string]canonicalRouteCandidate{root.ID: {route: []ConstructionRouteStep{}, observed: []int64{}, identity: ""}}
	distance := map[string]int{root.ID: 0}
	currentLayer := []string{root.ID}
	targetDistance := -1
	for depth := 0; len(currentLayer) > 0 && targetDistance < 0; depth++ {
		if snapshot.Policy.Route.MaxHops > 0 && depth >= snapshot.Policy.Route.MaxHops {
			break
		}
		nextLayer := make([]string, 0, len(currentLayer))
		queued := make(map[string]bool, len(currentLayer))
		for _, fromID := range currentLayer {
			from := best[fromID]
			for _, edge := range adjacency[fromID] {
				toDistance := depth + 1
				if snapshot.Policy.Route.MaxHops > 0 && toDistance > snapshot.Policy.Route.MaxHops {
					continue
				}
				step := ConstructionRouteStep{
					EdgeID: edge.ID, FromNodeID: edge.FromNodeID, ToNodeID: edge.ToNodeID,
					FromResourceType: edge.SourceResourceType, ToResourceType: edge.TargetResourceType,
					Relationship: edge.Label, StorageDirection: strings.ToUpper(strings.TrimSpace(edge.StorageDirection)), MatchMode: "OPTIONAL",
				}
				candidate := canonicalRouteCandidate{
					route:    append(cloneConstructionRoute(from.route), step),
					observed: append(append([]int64(nil), from.observed...), edge.ObservedEdgeCount),
					identity: joinCanonicalRouteIdentity(from.identity, routeEdgeKey(edge)),
				}
				priorDistance, seen := distance[edge.ToNodeID]
				if !seen {
					distance[edge.ToNodeID] = toDistance
					best[edge.ToNodeID] = candidate
					if !queued[edge.ToNodeID] {
						nextLayer = append(nextLayer, edge.ToNodeID)
						queued[edge.ToNodeID] = true
					}
					if edge.ToNodeID == targetNodeID {
						targetDistance = toDistance
					}
					continue
				}
				if priorDistance == toDistance && canonicalRouteBetter(candidate, best[edge.ToNodeID]) {
					best[edge.ToNodeID] = candidate
				}
			}
		}
		currentLayer = nextLayer
	}

	candidate, found := best[targetNodeID]
	if !found {
		return nil, ErrCanonicalConstructionRouteUnavailable
	}
	return cloneConstructionRoute(candidate.route), nil
}

func canonicalRouteBetter(candidate, current canonicalRouteCandidate) bool {
	for index := range candidate.observed {
		if candidate.observed[index] != current.observed[index] {
			return candidate.observed[index] > current.observed[index]
		}
	}
	return candidate.identity < current.identity
}

func joinCanonicalRouteIdentity(prefix, edge string) string {
	if prefix == "" {
		return edge
	}
	return prefix + "\x00" + edge
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

func canExtendRoute(edges []Edge, nodeID string, policy RoutePolicy, used map[string]bool) bool {
	for _, edge := range edges {
		if edge.FromNodeID != nodeID || (!policy.AllowsSelfLoops && edge.FromNodeID == edge.ToNodeID) || (!policy.AllowsRepeatedEdges && used[edge.ID]) {
			continue
		}
		return true
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
