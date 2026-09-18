package authoringv2

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	RowChangeReady     = "READY"
	RowChangeBlocked   = "BLOCKED"
	RowChangeNoChange  = "NO_CHANGE"
	RowReferenceRoute  = "route"
	RowReferenceColumn = "column"
)

// RowChangeRequest identifies a candidate row root and any deliberate choices
// needed to make the route rebase unambiguous.
type RowChangeRequest struct {
	OutputID         string              `json:"outputId"`
	RootNodeID       string              `json:"rootNodeId"`
	RootOccurrenceID string              `json:"rootOccurrenceId,omitempty"`
	RouteRebase      []RouteRebaseChoice `json:"routeRebase,omitempty"`
}

// RouteRebaseChoice selects the catalog edge used when an existing route edge
// must be inverted. OccurrenceID names the original parent that becomes a
// child after rerooting.
type RouteRebaseChoice struct {
	OccurrenceID string `json:"occurrenceId"`
	EdgeID       string `json:"edgeId"`
}

type RowChangeProposal struct {
	OutputID             string              `json:"outputId"`
	RootNodeID           string              `json:"rootNodeId"`
	RootOccurrenceID     string              `json:"rootOccurrenceId"`
	SourceDocumentDigest string              `json:"sourceDocumentDigest"`
	RouteRebase          []RouteRebaseChoice `json:"routeRebase"`
	PreservedFeatureKeys []string            `json:"preservedFeatureKeys"`
}

type RowChangeUnresolvedReference struct {
	Kind         string   `json:"kind"`
	ID           string   `json:"id"`
	Code         string   `json:"code"`
	Message      string   `json:"message"`
	Alternatives []string `json:"alternatives,omitempty"`
}

type RowChangeAssessment struct {
	Status                    string                         `json:"status"`
	CurrentRootResourceType   string                         `json:"currentRootResourceType"`
	CandidateRootResourceType string                         `json:"candidateRootResourceType"`
	Proposal                  *RowChangeProposal             `json:"proposal,omitempty"`
	PreservedFeatureKeys      []string                       `json:"preservedFeatureKeys"`
	Unresolved                []RowChangeUnresolvedReference `json:"unresolved"`
}

// AssessRowChange is read-only. It promotes an authored descendant occurrence
// to the root only when every relationship on the existing root-to-descendant
// path has one explicit or unambiguous inverse catalog edge.
func AssessRowChange(workspace Workspace, catalog CatalogSnapshot, request RowChangeRequest) (RowChangeAssessment, error) {
	documentIndex := documentIndex(&workspace, request.OutputID)
	node, nodeOK := catalogNode(catalog, request.RootNodeID)
	if documentIndex < 0 || !nodeOK || !node.RowRootEligible {
		return RowChangeAssessment{}, fmt.Errorf("output or eligible root node was not found")
	}
	document := workspace.Documents[documentIndex]
	assessment := RowChangeAssessment{
		Status:                    RowChangeBlocked,
		CurrentRootResourceType:   document.RootResourceType,
		CandidateRootResourceType: node.ResourceType,
		PreservedFeatureKeys:      documentFeatureKeys(document),
		Unresolved:                []RowChangeUnresolvedReference{},
	}
	if document.RootResourceType == node.ResourceType {
		assessment.Status = RowChangeNoChange
		return assessment, nil
	}

	candidates := routeOccurrences(document.Route, node.ResourceType)
	selected, unresolved := selectRootOccurrence(candidates, request.RootOccurrenceID, node.ResourceType)
	if unresolved != nil {
		assessment.Unresolved = append(assessment.Unresolved, *unresolved)
		return assessment, nil
	}

	path, ok := routePath(document.Route, selected.OccurrenceID)
	if !ok || len(path) < 2 {
		return RowChangeAssessment{}, fmt.Errorf("selected row occurrence path was not found")
	}
	routeRebase := make([]RouteRebaseChoice, 0, len(path)-1)
	for index := 0; index < len(path)-1; index++ {
		parent, child := path[index], path[index+1]
		reverseEdges := catalogEdgesBetweenResourceTypes(catalog, child.ResourceType, parent.ResourceType)
		choice, unresolved := selectReverseEdge(reverseEdges, request.RouteRebase, parent.OccurrenceID)
		if unresolved != nil {
			assessment.Unresolved = append(assessment.Unresolved, *unresolved)
			return assessment, nil
		}
		routeRebase = append(routeRebase, RouteRebaseChoice{OccurrenceID: parent.OccurrenceID, EdgeID: choice.ID})
	}
	proposal := RowChangeProposal{
		OutputID:             request.OutputID,
		RootNodeID:           request.RootNodeID,
		RootOccurrenceID:     selected.OccurrenceID,
		SourceDocumentDigest: documentDigest(document),
		RouteRebase:          routeRebase,
		PreservedFeatureKeys: append([]string(nil), assessment.PreservedFeatureKeys...),
	}
	if _, err := applyRowChange(document, catalog, proposal); err != nil {
		assessment.Unresolved = append(assessment.Unresolved, RowChangeUnresolvedReference{
			Kind: RowReferenceRoute, ID: selected.OccurrenceID, Code: "INVALID_REBASED_ROUTE", Message: err.Error(),
		})
		return assessment, nil
	}
	assessment.Status = RowChangeReady
	assessment.Proposal = &proposal
	return assessment, nil
}

func ApplyRowChange(document Document, catalog CatalogSnapshot, proposal RowChangeProposal) (Document, error) {
	if document.Output.ID != proposal.OutputID || documentDigest(document) != proposal.SourceDocumentDigest {
		return Document{}, fmt.Errorf("ROW_REBASE_PROPOSAL_STALE: the table changed after row rebase assessment")
	}
	workspace := Workspace{Documents: []Document{document}}
	assessment, err := AssessRowChange(workspace, catalog, RowChangeRequest{
		OutputID: proposal.OutputID, RootNodeID: proposal.RootNodeID, RootOccurrenceID: proposal.RootOccurrenceID, RouteRebase: proposal.RouteRebase,
	})
	if err != nil {
		return Document{}, err
	}
	if assessment.Status != RowChangeReady || assessment.Proposal == nil {
		return Document{}, fmt.Errorf("ROW_REBASE_UNRESOLVED: row rebase requires explicit resolution")
	}
	if !rowChangeProposalEqual(*assessment.Proposal, proposal) {
		return Document{}, fmt.Errorf("ROW_REBASE_PROPOSAL_STALE: the assessed row rebase no longer matches the table")
	}
	return applyRowChange(document, catalog, proposal)
}

func applyRowChange(document Document, catalog CatalogSnapshot, proposal RowChangeProposal) (Document, error) {
	cloned, err := cloneDocument(document)
	if err != nil {
		return Document{}, err
	}
	document = cloned
	path, ok := routePath(document.Route, proposal.RootOccurrenceID)
	if !ok || len(path) < 2 {
		return Document{}, fmt.Errorf("row root occurrence %q is not an authored descendant", proposal.RootOccurrenceID)
	}
	choices := make(map[string]CatalogEdge, len(proposal.RouteRebase))
	for _, choice := range proposal.RouteRebase {
		if _, duplicate := choices[choice.OccurrenceID]; duplicate {
			return Document{}, fmt.Errorf("row rebase repeats inverse edge choice for occurrence %q", choice.OccurrenceID)
		}
		edge, found := catalogEdge(catalog, choice.EdgeID)
		if !found {
			return Document{}, fmt.Errorf("row rebase edge %q was not found", choice.EdgeID)
		}
		choices[choice.OccurrenceID] = edge
	}
	if len(choices) != len(path)-1 {
		return Document{}, fmt.Errorf("row rebase requires one explicit inverse edge for every path relationship")
	}
	for index := 0; index < len(path)-1; index++ {
		parent, child := path[index], path[index+1]
		edge, found := choices[parent.OccurrenceID]
		if !found {
			return Document{}, fmt.Errorf("row rebase has no inverse edge for occurrence %q", parent.OccurrenceID)
		}
		from, fromOK := catalogNode(catalog, edge.FromNodeID)
		to, toOK := catalogNode(catalog, edge.ToNodeID)
		if !fromOK || !toOK || from.ResourceType != child.ResourceType || to.ResourceType != parent.ResourceType {
			return Document{}, fmt.Errorf("row rebase edge %q does not invert occurrence %q", edge.ID, parent.OccurrenceID)
		}
	}

	selected := path[len(path)-1]
	reversed := path[0]
	reversed.OccurrenceID = selected.OccurrenceID
	reversed.Relationship = choices[path[0].OccurrenceID].Label
	reversed.MatchMode = path[1].MatchMode
	reversed.Children = routeChildrenWithout(path[0], path[1].OccurrenceID)
	for index := 1; index < len(path)-1; index++ {
		parent := path[index]
		parent.Relationship = choices[parent.OccurrenceID].Label
		parent.MatchMode = path[index+1].MatchMode
		parent.Children = append(routeChildrenWithout(parent, path[index+1].OccurrenceID), reversed)
		reversed = parent
	}

	newRoot := selected
	newRoot.OccurrenceID = RootOccurrenceID
	newRoot.Relationship = ""
	newRoot.MatchMode = ""
	newRoot.Children = append(append([]RouteNode(nil), selected.Children...), reversed)

	for index := range document.Columns {
		switch document.Columns[index].OccurrenceID {
		case RootOccurrenceID:
			document.Columns[index].OccurrenceID = selected.OccurrenceID
		case selected.OccurrenceID:
			document.Columns[index].OccurrenceID = RootOccurrenceID
		}
	}
	document.RootResourceType = selected.ResourceType
	document.Route = newRoot
	if document.Population != nil {
		prefix := make([]PopulationRouteStep, 0, len(path)-1)
		for index := len(path) - 2; index >= 0; index-- {
			prefix = append(prefix, PopulationRouteStep{ResourceType: path[index].ResourceType, Relationship: choices[path[index].OccurrenceID].Label})
		}
		document.Population.Route = append(prefix, document.Population.Route...)
	}
	if err := validateRebasedRoute(document, catalog); err != nil {
		return Document{}, err
	}
	return document, nil
}

func routeOccurrences(route RouteNode, resourceType string) []RouteNode {
	result := []RouteNode{}
	for _, child := range route.Children {
		if child.ResourceType == resourceType {
			result = append(result, child)
		}
		result = append(result, routeOccurrences(child, resourceType)...)
	}
	return result
}

func routePath(route RouteNode, occurrenceID string) ([]RouteNode, bool) {
	if route.OccurrenceID == occurrenceID {
		return []RouteNode{route}, true
	}
	for _, child := range route.Children {
		path, found := routePath(child, occurrenceID)
		if found {
			return append([]RouteNode{route}, path...), true
		}
	}
	return nil, false
}

func routeChildrenWithout(route RouteNode, occurrenceID string) []RouteNode {
	children := make([]RouteNode, 0, len(route.Children))
	for _, child := range route.Children {
		if child.OccurrenceID != occurrenceID {
			children = append(children, child)
		}
	}
	return children
}

func selectRootOccurrence(candidates []RouteNode, requested, resourceType string) (RouteNode, *RowChangeUnresolvedReference) {
	if requested != "" {
		for _, candidate := range candidates {
			if candidate.OccurrenceID == requested {
				return candidate, nil
			}
		}
		return RouteNode{}, &RowChangeUnresolvedReference{Kind: RowReferenceRoute, ID: requested, Code: "ROW_ROOT_OCCURRENCE_NOT_FOUND", Message: "the selected row occurrence is not an authored descendant with the requested resource type"}
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	alternatives := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		alternatives = append(alternatives, candidate.OccurrenceID)
	}
	sort.Strings(alternatives)
	code, message := "ROW_ROOT_OCCURRENCE_NOT_FOUND", "the requested resource type is not an authored descendant of the current row root"
	if len(candidates) > 1 {
		code, message = "AMBIGUOUS_ROW_ROOT_OCCURRENCE", "several route occurrences can become the row root; choose one explicitly"
	}
	return RouteNode{}, &RowChangeUnresolvedReference{Kind: RowReferenceRoute, ID: resourceType, Code: code, Message: message, Alternatives: alternatives}
}

func catalogEdgesBetweenResourceTypes(catalog CatalogSnapshot, fromResourceType, toResourceType string) []CatalogEdge {
	result := []CatalogEdge{}
	for _, edge := range catalog.Edges {
		from, fromOK := catalogNode(catalog, edge.FromNodeID)
		to, toOK := catalogNode(catalog, edge.ToNodeID)
		if fromOK && toOK && from.ResourceType == fromResourceType && to.ResourceType == toResourceType {
			result = append(result, edge)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func selectReverseEdge(candidates []CatalogEdge, choices []RouteRebaseChoice, occurrenceID string) (CatalogEdge, *RowChangeUnresolvedReference) {
	requested := ""
	for _, choice := range choices {
		if choice.OccurrenceID == occurrenceID {
			requested = choice.EdgeID
			break
		}
	}
	if requested != "" {
		for _, candidate := range candidates {
			if candidate.ID == requested {
				return candidate, nil
			}
		}
		return CatalogEdge{}, &RowChangeUnresolvedReference{Kind: RowReferenceRoute, ID: occurrenceID, Code: "INVALID_ROUTE_REBASE_EDGE", Message: "the selected inverse relationship cannot connect the new row root to the previous root"}
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	alternatives := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		alternatives = append(alternatives, candidate.ID)
	}
	code, message := "MISSING_ROUTE_REBASE_EDGE", "no inverse relationship can preserve features on the previous row root"
	if len(candidates) > 1 {
		code, message = "AMBIGUOUS_ROUTE_REBASE_EDGE", "several inverse relationships can preserve the previous row root; choose one explicitly"
	}
	return CatalogEdge{}, &RowChangeUnresolvedReference{Kind: RowReferenceRoute, ID: occurrenceID, Code: code, Message: message, Alternatives: alternatives}
}

func validateRebasedRoute(document Document, catalog CatalogSnapshot) error {
	maxDepth := 0
	var walk func(RouteNode, int, map[string]bool) error
	walk = func(node RouteNode, depth int, used map[string]bool) error {
		if depth > maxDepth {
			maxDepth = depth
		}
		for _, child := range node.Children {
			matches := []CatalogEdge{}
			for _, edge := range catalog.Edges {
				from, fromOK := catalogNode(catalog, edge.FromNodeID)
				to, toOK := catalogNode(catalog, edge.ToNodeID)
				if fromOK && toOK && from.ResourceType == node.ResourceType && to.ResourceType == child.ResourceType && edge.Label == child.Relationship {
					matches = append(matches, edge)
				}
			}
			if len(matches) == 0 {
				return fmt.Errorf("rebased occurrence %q has no catalog relationship from %s to %s", child.OccurrenceID, node.ResourceType, child.ResourceType)
			}
			edgeID := matches[0].ID
			if used[edgeID] && !catalog.RoutePolicy.AllowRepeatedEdges {
				return fmt.Errorf("rebased route repeats edge %q", edgeID)
			}
			next := make(map[string]bool, len(used)+1)
			for id, value := range used {
				next[id] = value
			}
			next[edgeID] = true
			if err := walk(child, depth+1, next); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(document.Route, 0, map[string]bool{}); err != nil {
		return err
	}
	if catalog.RoutePolicy.MaxHops != nil && maxDepth > *catalog.RoutePolicy.MaxHops {
		return fmt.Errorf("ROUTE_TOO_LONG: rebased route exceeds capability route policy (maxHops=%d, hops=%d)", *catalog.RoutePolicy.MaxHops, maxDepth)
	}
	if document.Population != nil && catalog.RoutePolicy.MaxHops != nil && len(document.Population.Route) > *catalog.RoutePolicy.MaxHops {
		return fmt.Errorf("ROUTE_TOO_LONG: rebased population route exceeds capability route policy (maxHops=%d, hops=%d)", *catalog.RoutePolicy.MaxHops, len(document.Population.Route))
	}
	return document.Validate()
}

func documentFeatureKeys(document Document) []string {
	keys := make([]string, 0, len(document.Columns))
	for _, column := range document.Columns {
		keys = append(keys, column.Column)
	}
	sort.Strings(keys)
	return keys
}

func documentDigest(document Document) string {
	raw, _ := json.Marshal(document)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func cloneDocument(document Document) (Document, error) {
	raw, err := json.Marshal(document)
	if err != nil {
		return Document{}, fmt.Errorf("clone row-change document: %w", err)
	}
	var cloned Document
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return Document{}, fmt.Errorf("clone row-change document: %w", err)
	}
	return cloned, nil
}

func rowChangeProposalEqual(left, right RowChangeProposal) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func (p RowChangeProposal) validate() error {
	if strings.TrimSpace(p.OutputID) == "" || strings.TrimSpace(p.RootNodeID) == "" || strings.TrimSpace(p.RootOccurrenceID) == "" || strings.TrimSpace(p.SourceDocumentDigest) == "" {
		return fmt.Errorf("rowChange requires outputId, rootNodeId, rootOccurrenceId, and sourceDocumentDigest")
	}
	if len(p.RouteRebase) == 0 {
		return fmt.Errorf("rowChange.routeRebase is required")
	}
	return nil
}
