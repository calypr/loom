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

// AssessRowChange is read-only. The first supported non-trivial rebase promotes
// one direct child occurrence to the root. This covers a useful row-grain
// change without guessing how to rewrite an arbitrary graph path.
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

	candidates := directChildOccurrences(document.Route, node.ResourceType)
	selected, unresolved := selectRootOccurrence(candidates, request.RootOccurrenceID, node.ResourceType)
	if unresolved != nil {
		assessment.Unresolved = append(assessment.Unresolved, *unresolved)
		return assessment, nil
	}

	reverseEdges := catalogEdgesBetween(catalog, request.RootNodeID, document.RootResourceType)
	choice, unresolved := selectReverseEdge(reverseEdges, request.RouteRebase, document.Route.OccurrenceID)
	if unresolved != nil {
		assessment.Unresolved = append(assessment.Unresolved, *unresolved)
		return assessment, nil
	}
	proposal := RowChangeProposal{
		OutputID:             request.OutputID,
		RootNodeID:           request.RootNodeID,
		RootOccurrenceID:     selected.OccurrenceID,
		SourceDocumentDigest: documentDigest(document),
		RouteRebase:          []RouteRebaseChoice{{OccurrenceID: document.Route.OccurrenceID, EdgeID: choice.ID}},
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
	if len(proposal.RouteRebase) != 1 || proposal.RouteRebase[0].OccurrenceID != RootOccurrenceID {
		return Document{}, fmt.Errorf("row rebase requires exactly one explicit inverse edge for the previous root")
	}
	edge, ok := catalogEdge(catalog, proposal.RouteRebase[0].EdgeID)
	if !ok {
		return Document{}, fmt.Errorf("row rebase edge %q was not found", proposal.RouteRebase[0].EdgeID)
	}
	rootIndex := -1
	for index := range document.Route.Children {
		if document.Route.Children[index].OccurrenceID == proposal.RootOccurrenceID {
			rootIndex = index
			break
		}
	}
	if rootIndex < 0 {
		return Document{}, fmt.Errorf("row root occurrence %q is not a direct child", proposal.RootOccurrenceID)
	}
	selected := document.Route.Children[rootIndex]
	from, fromOK := catalogNode(catalog, edge.FromNodeID)
	to, toOK := catalogNode(catalog, edge.ToNodeID)
	if !fromOK || !toOK || from.ResourceType != selected.ResourceType || to.ResourceType != document.RootResourceType {
		return Document{}, fmt.Errorf("row rebase edge %q does not invert the previous root relationship", edge.ID)
	}

	previousRoot := document.Route
	previousRoot.OccurrenceID = selected.OccurrenceID
	previousRoot.Relationship = edge.Label
	previousRoot.Children = append([]RouteNode(nil), document.Route.Children[:rootIndex]...)
	previousRoot.Children = append(previousRoot.Children, document.Route.Children[rootIndex+1:]...)

	newRoot := selected
	newRoot.OccurrenceID = RootOccurrenceID
	newRoot.Relationship = ""
	newRoot.Children = append(append([]RouteNode(nil), selected.Children...), previousRoot)

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
		document.Population.Route = append([]PopulationRouteStep{{ResourceType: previousRoot.ResourceType, Relationship: edge.Label}}, document.Population.Route...)
	}
	if err := validateRebasedRoute(document, catalog); err != nil {
		return Document{}, err
	}
	return document, nil
}

func directChildOccurrences(route RouteNode, resourceType string) []RouteNode {
	result := []RouteNode{}
	for _, child := range route.Children {
		if child.ResourceType == resourceType {
			result = append(result, child)
		}
	}
	return result
}

func selectRootOccurrence(candidates []RouteNode, requested, resourceType string) (RouteNode, *RowChangeUnresolvedReference) {
	if requested != "" {
		for _, candidate := range candidates {
			if candidate.OccurrenceID == requested {
				return candidate, nil
			}
		}
		return RouteNode{}, &RowChangeUnresolvedReference{Kind: RowReferenceRoute, ID: requested, Code: "ROW_ROOT_OCCURRENCE_NOT_FOUND", Message: "the selected row occurrence is not a direct child with the requested resource type"}
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	alternatives := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		alternatives = append(alternatives, candidate.OccurrenceID)
	}
	sort.Strings(alternatives)
	code, message := "ROW_ROOT_OCCURRENCE_NOT_FOUND", "the requested resource type is not a direct child of the current row root"
	if len(candidates) > 1 {
		code, message = "AMBIGUOUS_ROW_ROOT_OCCURRENCE", "several route occurrences can become the row root; choose one explicitly"
	}
	return RouteNode{}, &RowChangeUnresolvedReference{Kind: RowReferenceRoute, ID: resourceType, Code: code, Message: message, Alternatives: alternatives}
}

func catalogEdgesBetween(catalog CatalogSnapshot, fromNodeID, toResourceType string) []CatalogEdge {
	result := []CatalogEdge{}
	for _, edge := range catalog.Edges {
		if edge.FromNodeID != fromNodeID {
			continue
		}
		to, ok := catalogNode(catalog, edge.ToNodeID)
		if ok && to.ResourceType == toResourceType {
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
