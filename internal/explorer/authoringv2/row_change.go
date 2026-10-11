package authoringv2

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	RowChangeReady        = "READY"
	RowChangeBlocked      = "BLOCKED"
	RowChangeNoChange     = "NO_CHANGE"
	RowChangeModePreserve = "PRESERVE"
	RowChangeModeReplace  = "REPLACE"
	RowReferenceRoute     = "route"
	RowReferenceColumn    = "column"
)

const rowChangeRouteTokenPrefix = "row-change-route:v1:"

type rowChangeRouteToken struct {
	EdgeIDs            []string `json:"edgeIds"`
	AnchorOccurrenceID string   `json:"anchorOccurrenceId"`
}

type rowChangeCandidateRoute struct {
	Token            string
	OccurrenceID     string
	EdgeIDs          []string
	AnchorOccurrence string
	AnchorDepth      int
}

// RowChangeRequest identifies a candidate row root and any deliberate choices
// needed to make the route rebase unambiguous.
type RowChangeRequest struct {
	Mode             string              `json:"mode,omitempty"`
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
	Mode                     string                    `json:"mode,omitempty"`
	OutputID                 string                    `json:"outputId"`
	RootNodeID               string                    `json:"rootNodeId"`
	RootOccurrenceID         string                    `json:"rootOccurrenceId"`
	SourceDocumentDigest     string                    `json:"sourceDocumentDigest"`
	RouteRebase              []RouteRebaseChoice       `json:"routeRebase"`
	PreservedFeatureKeys     []string                  `json:"preservedFeatureKeys"`
	CatalogSnapshotToken     string                    `json:"catalogSnapshotToken,omitempty"`
	CatalogGeneration        string                    `json:"catalogGeneration,omitempty"`
	CatalogDigest            string                    `json:"catalogDigest,omitempty"`
	RemovedFeatureKeys       []string                  `json:"removedFeatureKeys"`
	WorkspaceOutputConsumers []WorkspaceOutputConsumer `json:"workspaceOutputConsumers"`
	SharedFilterBindings     []SharedFilterBindingRef  `json:"sharedFilterBindings"`
}

type WorkspaceOutputConsumer struct {
	OutputID   string `json:"outputId"`
	StepID     string `json:"stepId"`
	InputIndex int    `json:"inputIndex"`
}

type SharedFilterBindingRef struct {
	Name         string `json:"name"`
	OutputID     string `json:"outputId"`
	Column       string `json:"column"`
	BindingIndex int    `json:"bindingIndex"`
}

type RowChangeUnresolvedReference struct {
	Kind         string   `json:"kind"`
	ID           string   `json:"id"`
	Code         string   `json:"code"`
	Message      string   `json:"message"`
	Alternatives []string `json:"alternatives,omitempty"`
}

type RowChangeAssessment struct {
	Mode                      string                         `json:"mode,omitempty"`
	Status                    string                         `json:"status"`
	CurrentRootResourceType   string                         `json:"currentRootResourceType"`
	CandidateRootResourceType string                         `json:"candidateRootResourceType"`
	Proposal                  *RowChangeProposal             `json:"proposal,omitempty"`
	PreservedFeatureKeys      []string                       `json:"preservedFeatureKeys"`
	RemovedFeatureKeys        []string                       `json:"removedFeatureKeys"`
	WorkspaceOutputConsumers  []WorkspaceOutputConsumer      `json:"workspaceOutputConsumers"`
	SharedFilterBindings      []SharedFilterBindingRef       `json:"sharedFilterBindings"`
	Unresolved                []RowChangeUnresolvedReference `json:"unresolved"`
}

// AssessRowChange is read-only. It promotes an authored descendant occurrence
// or derives a preserving catalog route to an eligible root absent from the
// authored route, provided each relationship can be inverted.
func AssessRowChange(workspace Workspace, catalog CatalogSnapshot, request RowChangeRequest) (RowChangeAssessment, error) {
	if request.Mode == RowChangeModeReplace {
		return AssessRowReplacement(workspace, catalog, request)
	}
	if request.Mode != "" && request.Mode != RowChangeModePreserve {
		return RowChangeAssessment{}, fmt.Errorf("row-change mode %q is unsupported", request.Mode)
	}
	documentIndex := documentIndex(&workspace, request.OutputID)
	node, nodeOK := catalogNode(catalog, request.RootNodeID)
	if documentIndex < 0 || !nodeOK || !node.RowRootEligible {
		return RowChangeAssessment{}, fmt.Errorf("output or eligible root node was not found")
	}
	document := workspace.Documents[documentIndex]
	assessment := RowChangeAssessment{
		Mode:                      RowChangeModePreserve,
		Status:                    RowChangeBlocked,
		CurrentRootResourceType:   document.RootResourceType,
		CandidateRootResourceType: node.ResourceType,
		PreservedFeatureKeys:      documentFeatureKeys(document),
		RemovedFeatureKeys:        []string{},
		WorkspaceOutputConsumers:  []WorkspaceOutputConsumer{},
		SharedFilterBindings:      []SharedFilterBindingRef{},
		Unresolved:                []RowChangeUnresolvedReference{},
	}
	rootNodeID, routeNodeIDs, resolved := resolveDocumentRouteNodeIDs(document.Route, catalog)
	if !resolved {
		assessment.Unresolved = append(assessment.Unresolved, RowChangeUnresolvedReference{
			Kind: RowReferenceRoute, ID: document.Route.OccurrenceID, Code: "ROW_ROOT_ROUTE_UNRESOLVED",
			Message: "the current route does not identify one exact catalog root, so its rows cannot be preserved safely",
		})
		return assessment, nil
	}
	if document.RootResourceType == node.ResourceType && rootNodeID == request.RootNodeID {
		assessment.Status = RowChangeNoChange
		return assessment, nil
	}
	sourceDocument, err := cloneDocument(document)
	if err != nil {
		return RowChangeAssessment{}, err
	}

	authoredCandidates := make([]RouteNode, 0)
	for _, occurrence := range routeOccurrences(document.Route, node.ResourceType) {
		if routeNodeIDs[occurrence.OccurrenceID] == request.RootNodeID {
			authoredCandidates = append(authoredCandidates, occurrence)
		}
	}
	var selected RouteNode
	var derived *rowChangeCandidateRoute
	if strings.HasPrefix(request.RootOccurrenceID, rowChangeRouteTokenPrefix) {
		derivedCandidates := deriveRowChangeCandidateRoutes(document, catalog, rootNodeID, request.RootNodeID, routeNodeIDs)
		for index := range derivedCandidates {
			if derivedCandidates[index].Token == request.RootOccurrenceID {
				derived = &derivedCandidates[index]
				break
			}
		}
		if derived == nil {
			assessment.Unresolved = append(assessment.Unresolved, RowChangeUnresolvedReference{
				Kind: RowReferenceRoute, ID: request.RootOccurrenceID, Code: "ROW_ROOT_OCCURRENCE_NOT_FOUND",
				Message: "the selected catalog path is no longer available for this table",
			})
			return assessment, nil
		}
	} else if request.RootOccurrenceID != "" || len(authoredCandidates) > 0 {
		candidate, unresolved := selectRootOccurrence(authoredCandidates, request.RootOccurrenceID, node.ResourceType)
		if unresolved != nil {
			assessment.Unresolved = append(assessment.Unresolved, *unresolved)
			return assessment, nil
		}
		selected = candidate
	} else {
		derivedCandidates := deriveRowChangeCandidateRoutes(document, catalog, rootNodeID, request.RootNodeID, routeNodeIDs)
		if len(derivedCandidates) == 0 {
			assessment.Unresolved = append(assessment.Unresolved, RowChangeUnresolvedReference{
				Kind: RowReferenceRoute, ID: request.RootNodeID, Code: "ROW_ROOT_PATH_NOT_FOUND",
				Message: "no reversible catalog route can preserve the existing table from this root",
			})
			return assessment, nil
		}
		if len(derivedCandidates) > 1 {
			alternatives := make([]string, 0, len(derivedCandidates))
			for _, candidate := range derivedCandidates {
				alternatives = append(alternatives, candidate.Token)
			}
			assessment.Unresolved = append(assessment.Unresolved, RowChangeUnresolvedReference{
				Kind: RowReferenceRoute, ID: request.RootNodeID, Code: "AMBIGUOUS_ROW_ROOT_OCCURRENCE",
				Message:      "several catalog paths can preserve the table; choose one explicitly",
				Alternatives: alternatives,
			})
			return assessment, nil
		}
		derived = &derivedCandidates[0]
	}

	path := []RouteNode(nil)
	pathNodeIDs := []string(nil)
	if derived != nil {
		var err error
		document, err = materializeRowChangeCandidateRoute(document, catalog, rootNodeID, *derived)
		if err != nil {
			assessment.Unresolved = append(assessment.Unresolved, RowChangeUnresolvedReference{
				Kind: RowReferenceRoute, ID: derived.Token, Code: "INVALID_REBASED_ROUTE", Message: err.Error(),
			})
			return assessment, nil
		}
		selected = RouteNode{OccurrenceID: derived.OccurrenceID, ResourceType: node.ResourceType, MatchMode: RouteMatchOptional}
		var ok bool
		path, ok = routePath(document.Route, selected.OccurrenceID)
		if !ok {
			return RowChangeAssessment{}, fmt.Errorf("derived row occurrence path was not materialized")
		}
		pathNodeIDs, ok = routePathCatalogNodeIDs(document.Route, selected.OccurrenceID, rootNodeID, catalog)
		if !ok {
			return RowChangeAssessment{}, fmt.Errorf("derived row occurrence catalog path was not materialized")
		}
	} else {
		var ok bool
		path, ok = routePath(document.Route, selected.OccurrenceID)
		if !ok || len(path) < 2 {
			return RowChangeAssessment{}, fmt.Errorf("selected row occurrence path was not found")
		}
		pathNodeIDs, ok = routePathCatalogNodeIDs(document.Route, selected.OccurrenceID, rootNodeID, catalog)
		if !ok {
			return RowChangeAssessment{}, fmt.Errorf("selected row occurrence catalog path was not found")
		}
	}
	routeRebase := make([]RouteRebaseChoice, 0, len(path)-1)
	for index := 0; index < len(path)-1; index++ {
		parent, child := path[index], path[index+1]
		reverseEdges := catalogEdgesBetweenNodeIDs(catalog, pathNodeIDs[index+1], pathNodeIDs[index])
		choice, unresolved := selectReverseEdge(reverseEdges, request.RouteRebase, parent.OccurrenceID, child.Relationship)
		if unresolved != nil {
			assessment.Unresolved = append(assessment.Unresolved, *unresolved)
			return assessment, nil
		}
		routeRebase = append(routeRebase, RouteRebaseChoice{OccurrenceID: parent.OccurrenceID, EdgeID: choice.ID})
	}
	proposalRootOccurrenceID := selected.OccurrenceID
	if derived != nil {
		proposalRootOccurrenceID = derived.Token
	}
	proposal := RowChangeProposal{
		OutputID:                 request.OutputID,
		RootNodeID:               request.RootNodeID,
		RootOccurrenceID:         proposalRootOccurrenceID,
		SourceDocumentDigest:     documentDigest(sourceDocument),
		RouteRebase:              routeRebase,
		PreservedFeatureKeys:     append([]string(nil), assessment.PreservedFeatureKeys...),
		RemovedFeatureKeys:       []string{},
		WorkspaceOutputConsumers: []WorkspaceOutputConsumer{},
		SharedFilterBindings:     []SharedFilterBindingRef{},
	}
	if _, err := applyRowChange(sourceDocument, catalog, proposal); err != nil {
		assessment.Unresolved = append(assessment.Unresolved, RowChangeUnresolvedReference{
			Kind: RowReferenceRoute, ID: proposal.RootOccurrenceID, Code: "INVALID_REBASED_ROUTE", Message: err.Error(),
		})
		return assessment, nil
	}
	assessment.Status = RowChangeReady
	assessment.Proposal = &proposal
	return assessment, nil
}

// AssessRowReplacement creates a destructive, same-output replacement proposal
// independently of preserve-route reachability.
func AssessRowReplacement(workspace Workspace, catalog CatalogSnapshot, request RowChangeRequest) (RowChangeAssessment, error) {
	index := documentIndex(&workspace, request.OutputID)
	node, found := catalogNode(catalog, request.RootNodeID)
	if index < 0 || !found || !node.RowRootEligible {
		return RowChangeAssessment{}, fmt.Errorf("output or eligible root node was not found")
	}
	document := workspace.Documents[index]
	eligible := 0
	for _, candidate := range catalog.Nodes {
		if candidate.RowRootEligible && candidate.ResourceType == node.ResourceType {
			eligible++
		}
	}
	if eligible != 1 {
		return blockedReplacement(document, node, RowReferenceRoute, request.RootNodeID, "ROW_ROOT_RESOURCE_TYPE_AMBIGUOUS", "replacement requires exactly one eligible catalog node for the selected resource type"), nil
	}
	idCandidate, idErr := replacementIDCandidate(catalog, node)
	if idErr != nil {
		return blockedReplacement(document, node, RowReferenceColumn, request.RootNodeID, "ROW_ROOT_ID_FIELD_UNAVAILABLE", idErr.Error()), nil
	}
	if document.RootResourceType == node.ResourceType {
		rootNodeID, _, resolved := resolveDocumentRouteNodeIDs(document.Route, catalog)
		if resolved && rootNodeID == request.RootNodeID {
			return RowChangeAssessment{Status: RowChangeNoChange, Mode: RowChangeModeReplace, CurrentRootResourceType: document.RootResourceType, CandidateRootResourceType: node.ResourceType, PreservedFeatureKeys: []string{}, RemovedFeatureKeys: []string{}, WorkspaceOutputConsumers: []WorkspaceOutputConsumer{}, SharedFilterBindings: []SharedFilterBindingRef{}, Unresolved: []RowChangeUnresolvedReference{}}, nil
		}
	}
	digest, err := catalog.Digest()
	if err != nil {
		return RowChangeAssessment{}, fmt.Errorf("digest replacement catalog snapshot: %w", err)
	}
	removed := replacementRemovedFeatureKeys(document)
	consumers := workspaceOutputConsumers(workspace, request.OutputID)
	bindings := sharedFilterBindingsForOutput(workspace, request.OutputID)
	candidate := replacementDocument(document, node, *idCandidate)
	if err := candidate.Validate(); err != nil {
		return blockedReplacement(document, node, RowReferenceColumn, "id", "ROW_ROOT_ID_FIELD_UNAVAILABLE", err.Error()), nil
	}
	if err := validateEditableSource(candidate, catalog, RootOccurrenceID, candidate.Columns[0].Source); err != nil {
		return blockedReplacement(document, node, RowReferenceColumn, "id", "ROW_ROOT_ID_FIELD_UNAVAILABLE", err.Error()), nil
	}
	proposal := RowChangeProposal{Mode: RowChangeModeReplace, OutputID: request.OutputID, RootNodeID: request.RootNodeID, RootOccurrenceID: RootOccurrenceID,
		SourceDocumentDigest: documentDigest(document), CatalogSnapshotToken: catalog.SnapshotToken, CatalogGeneration: catalog.SourceGeneration, CatalogDigest: digest,
		RouteRebase: []RouteRebaseChoice{}, PreservedFeatureKeys: []string{}, RemovedFeatureKeys: append([]string{}, removed...), WorkspaceOutputConsumers: append([]WorkspaceOutputConsumer{}, consumers...), SharedFilterBindings: append([]SharedFilterBindingRef{}, bindings...)}
	return RowChangeAssessment{Status: RowChangeReady, CurrentRootResourceType: document.RootResourceType, CandidateRootResourceType: node.ResourceType, Proposal: &proposal,
		Mode: RowChangeModeReplace, PreservedFeatureKeys: []string{}, RemovedFeatureKeys: removed, WorkspaceOutputConsumers: consumers, SharedFilterBindings: bindings, Unresolved: []RowChangeUnresolvedReference{}}, nil
}

// ApplyRowReplacement re-derives the proposal against the current workspace and
// exact authorized catalog snapshot before returning an immutable candidate.
func ApplyRowReplacement(workspace Workspace, catalog CatalogSnapshot, proposal RowChangeProposal) (Workspace, error) {
	if proposal.Mode != RowChangeModeReplace {
		return Workspace{}, fmt.Errorf("row replacement proposal has unsupported mode %q", proposal.Mode)
	}
	assessment, err := AssessRowReplacement(workspace, catalog, RowChangeRequest{Mode: RowChangeModeReplace, OutputID: proposal.OutputID, RootNodeID: proposal.RootNodeID})
	if err != nil {
		return Workspace{}, err
	}
	if assessment.Status != RowChangeReady || assessment.Proposal == nil || !rowChangeProposalEqual(*assessment.Proposal, proposal) {
		return Workspace{}, fmt.Errorf("ROW_REPLACEMENT_PROPOSAL_STALE: the table, catalog, or current-draft references changed after replacement assessment")
	}
	node, _ := catalogNode(catalog, proposal.RootNodeID)
	idCandidate, err := replacementIDCandidate(catalog, node)
	if err != nil {
		return Workspace{}, err
	}
	workspace, err = cloneWorkspace(workspace)
	if err != nil {
		return Workspace{}, err
	}
	index := documentIndex(&workspace, proposal.OutputID)
	workspace.Documents[index] = replacementDocument(workspace.Documents[index], node, *idCandidate)
	for name, bindings := range workspace.SharedFilters {
		kept := make([]SharedFilterBinding, 0, len(bindings))
		for _, binding := range bindings {
			if binding.OutputID != proposal.OutputID {
				kept = append(kept, binding)
			}
		}
		if len(kept) == 0 {
			delete(workspace.SharedFilters, name)
		} else {
			workspace.SharedFilters[name] = kept
		}
	}
	return workspace, nil
}

func replacementDocument(document Document, node CatalogNode, idCandidate CatalogCandidate) Document {
	label := strings.TrimSpace(idCandidate.Label)
	if label == "" {
		label = "ID"
	}
	visible := true
	order := 0
	return Document{Kind: document.Kind, Output: Output{ID: document.Output.ID, Title: document.Output.Title}, RootResourceType: node.ResourceType,
		Route: RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: node.ResourceType}, Rows: RecordsRowDefinition(),
		Columns: []Column{{ColumnID: "id", Column: "id", Label: label, LogicalType: idCandidate.LogicalType, OccurrenceID: RootOccurrenceID,
			Source: ColumnSource{Kind: SourceField, Field: &FieldSource{Path: strings.TrimPrefix(strings.TrimSpace(idCandidate.FieldPath), "root."), ProjectionMode: "VALUE"}},
			Table:  &TablePresentation{Visible: &visible, Order: &order}}},
	}
}

func blockedReplacement(document Document, node CatalogNode, kind, id, code, message string) RowChangeAssessment {
	return RowChangeAssessment{Status: RowChangeBlocked, CurrentRootResourceType: document.RootResourceType, CandidateRootResourceType: node.ResourceType,
		Mode: RowChangeModeReplace, PreservedFeatureKeys: []string{}, RemovedFeatureKeys: []string{}, WorkspaceOutputConsumers: []WorkspaceOutputConsumer{}, SharedFilterBindings: []SharedFilterBindingRef{}, Unresolved: []RowChangeUnresolvedReference{{Kind: kind, ID: id, Code: code, Message: message}}}
}

func replacementIDCandidate(catalog CatalogSnapshot, node CatalogNode) (*CatalogCandidate, error) {
	var selected *CatalogCandidate
	for index := range catalog.Candidates {
		candidate := &catalog.Candidates[index]
		if candidate.NodeID != node.ID || strings.TrimPrefix(strings.TrimSpace(candidate.FieldPath), "root.") != "id" {
			continue
		}
		if selected != nil {
			return nil, fmt.Errorf("the selected root has several catalog candidates for its id field")
		}
		selected = candidate
	}
	cardinality := ""
	if selected != nil {
		cardinality = strings.ToLower(strings.TrimSpace(selected.Cardinality))
	}
	if selected == nil || (!strings.EqualFold(selected.LogicalType, "string") && !strings.EqualFold(selected.LogicalType, "id")) ||
		(cardinality != "optional_one" && cardinality != "required_one") || selected.Repeated || len(selected.RepeatedBoundaries) != 0 || !contains(selected.ProjectionModes, "VALUE") || selected.ConstructionChoice == nil {
		return nil, fmt.Errorf("the selected root does not expose a direct scalar id VALUE projection in the current catalog")
	}
	return selected, nil
}

func replacementRemovedFeatureKeys(document Document) []string {
	keys := []string{"root:" + document.RootResourceType}
	if strings.TrimSpace(document.Output.RowLabel) != "" {
		keys = append(keys, "rowLabel:"+document.Output.RowLabel)
	}
	var visit func(RouteNode)
	visit = func(route RouteNode) {
		if route.OccurrenceID != RootOccurrenceID {
			keys = append(keys, "route:"+route.OccurrenceID)
		}
		for _, child := range route.Children {
			visit(child)
		}
	}
	visit(document.Route)
	if document.Rows.Kind != RowDefinitionRecords {
		keys = append(keys, "rows:"+string(document.Rows.Kind))
	}
	for _, column := range document.Columns {
		keys = append(keys, "column:"+column.Column)
	}
	if document.Population != nil {
		keys = append(keys, "population:"+document.Population.SelectionRevisionID)
		for index, step := range document.Population.Route {
			keys = append(keys, fmt.Sprintf("populationRoute:%d:%s:%s", index, step.ResourceType, step.Relationship))
		}
	}
	for _, frame := range document.Frames {
		keys = append(keys, "frame:"+frame.ID)
	}
	if document.TableShape != nil {
		if document.TableShape.Reshape != nil {
			keys = append(keys, "tableShape:reshape:"+document.TableShape.Reshape.Kind)
		}
		for _, derived := range document.TableShape.Derived {
			keys = append(keys, "tableShape:derived:"+string(derived.ConstructionID))
		}
		if document.TableShape.Reshape == nil && len(document.TableShape.Derived) == 0 {
			keys = append(keys, "tableShape")
		}
	}
	if document.Construction != nil {
		for _, step := range document.Construction.Steps {
			keys = append(keys, "construction:"+step.ID)
		}
		if len(document.Construction.Steps) == 0 {
			keys = append(keys, "construction")
		}
	}
	for _, filter := range document.FixedFilters {
		keys = append(keys, "fixedFilter:"+filter.Column)
	}
	for _, action := range document.Actions {
		keys = append(keys, "action:"+action.Type+":"+action.Title)
	}
	sort.Strings(keys)
	return keys
}

func workspaceOutputConsumers(workspace Workspace, outputID string) []WorkspaceOutputConsumer {
	consumers := []WorkspaceOutputConsumer{}
	for _, document := range workspace.Documents {
		if document.Output.ID == outputID || document.Construction == nil {
			continue
		}
		for _, step := range document.Construction.Steps {
			for inputIndex, input := range step.Inputs {
				if input.Kind == ConstructionInputWorkspaceOutput && input.OutputID == outputID {
					consumers = append(consumers, WorkspaceOutputConsumer{OutputID: document.Output.ID, StepID: step.ID, InputIndex: inputIndex})
				}
			}
		}
	}
	sort.Slice(consumers, func(i, j int) bool {
		if consumers[i].OutputID != consumers[j].OutputID {
			return consumers[i].OutputID < consumers[j].OutputID
		}
		if consumers[i].StepID != consumers[j].StepID {
			return consumers[i].StepID < consumers[j].StepID
		}
		return consumers[i].InputIndex < consumers[j].InputIndex
	})
	return consumers
}

func sharedFilterBindingsForOutput(workspace Workspace, outputID string) []SharedFilterBindingRef {
	refs := []SharedFilterBindingRef{}
	names := make([]string, 0, len(workspace.SharedFilters))
	for name := range workspace.SharedFilters {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for index, binding := range workspace.SharedFilters[name] {
			if binding.OutputID == outputID {
				refs = append(refs, SharedFilterBindingRef{Name: name, OutputID: binding.OutputID, Column: binding.Column, BindingIndex: index})
			}
		}
	}
	return refs
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
	rootNodeID, routeNodeIDs, resolved := resolveDocumentRouteNodeIDs(document.Route, catalog)
	if !resolved {
		return Document{}, fmt.Errorf("current route does not identify one exact catalog root")
	}
	selectedOccurrenceID := proposal.RootOccurrenceID
	if strings.HasPrefix(proposal.RootOccurrenceID, rowChangeRouteTokenPrefix) {
		candidates := deriveRowChangeCandidateRoutes(document, catalog, rootNodeID, proposal.RootNodeID, routeNodeIDs)
		var candidate *rowChangeCandidateRoute
		for index := range candidates {
			if candidates[index].Token == proposal.RootOccurrenceID {
				candidate = &candidates[index]
				break
			}
		}
		if candidate == nil {
			return Document{}, fmt.Errorf("derived row root path %q is unavailable", proposal.RootOccurrenceID)
		}
		selectedOccurrenceID = candidate.OccurrenceID
		document, err = materializeRowChangeCandidateRoute(document, catalog, rootNodeID, *candidate)
		if err != nil {
			return Document{}, err
		}
	}
	path, ok := routePath(document.Route, selectedOccurrenceID)
	if !ok || len(path) < 2 {
		return Document{}, fmt.Errorf("row root occurrence %q is not a route descendant", selectedOccurrenceID)
	}
	pathNodeIDs, ok := routePathCatalogNodeIDs(document.Route, selectedOccurrenceID, rootNodeID, catalog)
	if !ok || len(pathNodeIDs) != len(path) || pathNodeIDs[len(pathNodeIDs)-1] != proposal.RootNodeID {
		return Document{}, fmt.Errorf("row root occurrence %q does not resolve to the selected catalog node", selectedOccurrenceID)
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
		if !fromOK || !toOK || edge.FromNodeID != pathNodeIDs[index+1] || edge.ToNodeID != pathNodeIDs[index] || from.ResourceType != child.ResourceType || to.ResourceType != parent.ResourceType {
			return Document{}, fmt.Errorf("row rebase edge %q does not invert occurrence %q", edge.ID, parent.OccurrenceID)
		}
	}

	selected := path[len(path)-1]
	reversed := path[0]
	reversed.OccurrenceID = selected.OccurrenceID
	reversed.CatalogEdgeID = choices[path[0].OccurrenceID].ID
	reversed.Relationship = choices[path[0].OccurrenceID].Label
	reversed.MatchMode = path[1].MatchMode
	reversed.Children = routeChildrenWithout(path[0], path[1].OccurrenceID)
	for index := 1; index < len(path)-1; index++ {
		parent := path[index]
		parent.CatalogEdgeID = choices[parent.OccurrenceID].ID
		parent.Relationship = choices[parent.OccurrenceID].Label
		parent.MatchMode = path[index+1].MatchMode
		parent.Children = append(routeChildrenWithout(parent, path[index+1].OccurrenceID), reversed)
		reversed = parent
	}

	newRoot := selected
	newRoot.OccurrenceID = RootOccurrenceID
	newRoot.CatalogEdgeID = ""
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
			edge := choices[path[index].OccurrenceID]
			prefix = append(prefix, PopulationRouteStep{
				ResourceType: path[index].ResourceType, Relationship: edge.Label,
				CatalogEdgeID: edge.ID, StorageDirection: edge.StorageDirection,
			})
		}
		document.Population.Route = rebasePopulationRoute(prefix, document.Population.Route, proposal.RootNodeID, rootNodeID, catalog)
	}
	if err := validateRebasedRoute(document, catalog, proposal.RootNodeID); err != nil {
		return Document{}, err
	}
	return document, nil
}

func rebasePopulationRoute(prefix, existing []PopulationRouteStep, newRootNodeID, previousRootNodeID string, catalog CatalogSnapshot) []PopulationRouteStep {
	combined := make([]PopulationRouteStep, 0, len(prefix)+len(existing))
	combined = append(combined, prefix...)
	combined = append(combined, existing...)
	if len(prefix) == 0 || len(existing) == 0 {
		return combined
	}

	prefixNodeIDs, prefixResolved := populationRouteCatalogNodeIDs(prefix, newRootNodeID, catalog)
	if !prefixResolved || prefixNodeIDs[len(prefixNodeIDs)-1] != previousRootNodeID {
		return combined
	}
	if _, existingResolved := populationRouteCatalogNodeIDs(existing, previousRootNodeID, catalog); !existingResolved {
		return combined
	}

	// Only remove a newly added rebase prefix against the old route at their
	// seam. Exact catalog endpoints, relationship labels, and storage directions
	// prove that each removed pair returns to the same node; resource-type
	// equality alone is not sufficient because catalogs can contain distinct
	// nodes with the same FHIR resource type.
	prefixEnd, existingStart := len(prefix), 0
	for prefixEnd > 0 && existingStart < len(existing) && populationRouteStepsAreExactInverses(prefix[prefixEnd-1], existing[existingStart], catalog) {
		prefixEnd--
		existingStart++
	}

	result := make([]PopulationRouteStep, 0, prefixEnd+len(existing)-existingStart)
	result = append(result, prefix[:prefixEnd]...)
	result = append(result, existing[existingStart:]...)
	return result
}

func populationRouteCatalogNodeIDs(route []PopulationRouteStep, rootNodeID string, catalog CatalogSnapshot) ([]string, bool) {
	if _, found := catalogNode(catalog, rootNodeID); !found {
		return nil, false
	}
	result := make([]string, 1, len(route)+1)
	result[0] = rootNodeID
	currentNodeID := rootNodeID
	for _, step := range route {
		if step.CatalogEdgeID == "" {
			return nil, false
		}
		edge, found := catalogEdge(catalog, step.CatalogEdgeID)
		if !found || edge.FromNodeID != currentNodeID || edge.Label == "" || edge.Label != step.Relationship || edge.StorageDirection != step.StorageDirection {
			return nil, false
		}
		next, found := catalogNode(catalog, edge.ToNodeID)
		if !found || next.ResourceType != step.ResourceType {
			return nil, false
		}
		currentNodeID = edge.ToNodeID
		result = append(result, currentNodeID)
	}
	return result, true
}

func populationRouteStepsAreExactInverses(prefix, existing PopulationRouteStep, catalog CatalogSnapshot) bool {
	if prefix.CatalogEdgeID == "" || existing.CatalogEdgeID == "" || prefix.CatalogEdgeID == existing.CatalogEdgeID ||
		prefix.Relationship == "" || prefix.Relationship != existing.Relationship {
		return false
	}
	prefixEdge, prefixFound := catalogEdge(catalog, prefix.CatalogEdgeID)
	existingEdge, existingFound := catalogEdge(catalog, existing.CatalogEdgeID)
	if !prefixFound || !existingFound || prefixEdge.Label == "" || prefixEdge.Label != existingEdge.Label ||
		prefixEdge.FromNodeID == prefixEdge.ToNodeID || prefixEdge.FromNodeID != existingEdge.ToNodeID || prefixEdge.ToNodeID != existingEdge.FromNodeID {
		return false
	}
	return (prefixEdge.StorageDirection == "INBOUND" && existingEdge.StorageDirection == "OUTBOUND") ||
		(prefixEdge.StorageDirection == "OUTBOUND" && existingEdge.StorageDirection == "INBOUND")
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

func resolveDocumentRouteNodeIDs(route RouteNode, catalog CatalogSnapshot) (string, map[string]string, bool) {
	rootCandidates := make([]CatalogNode, 0)
	for _, node := range catalog.Nodes {
		if node.RowRootEligible && node.ResourceType == route.ResourceType {
			rootCandidates = append(rootCandidates, node)
		}
	}
	sort.Slice(rootCandidates, func(i, j int) bool { return rootCandidates[i].ID < rootCandidates[j].ID })
	var rootNodeID string
	var nodeIDs map[string]string
	for _, candidate := range rootCandidates {
		resolved, ok := routeNodeIDsFromRoot(route, candidate.ID, catalog)
		if !ok {
			continue
		}
		if rootNodeID != "" {
			return "", nil, false
		}
		rootNodeID = candidate.ID
		nodeIDs = resolved
	}
	return rootNodeID, nodeIDs, rootNodeID != ""
}

func routeNodeIDsFromRoot(route RouteNode, rootNodeID string, catalog CatalogSnapshot) (map[string]string, bool) {
	result := map[string]string{route.OccurrenceID: rootNodeID}
	var walk func(RouteNode, string) bool
	walk = func(parent RouteNode, parentNodeID string) bool {
		for _, child := range parent.Children {
			edge, err := resolveCatalogRouteEdge(catalog, parentNodeID, parent, child)
			if err != nil {
				return false
			}
			result[child.OccurrenceID] = edge.ToNodeID
			if !walk(child, edge.ToNodeID) {
				return false
			}
		}
		return true
	}
	if !walk(route, rootNodeID) {
		return nil, false
	}
	return result, true
}

func routePathCatalogNodeIDs(route RouteNode, occurrenceID, rootNodeID string, catalog CatalogSnapshot) ([]string, bool) {
	path, found := routePath(route, occurrenceID)
	if !found || len(path) == 0 {
		return nil, false
	}
	nodeIDs, found := routeNodeIDsFromRoot(route, rootNodeID, catalog)
	if !found {
		return nil, false
	}
	result := make([]string, 0, len(path))
	for _, node := range path {
		id, ok := nodeIDs[node.OccurrenceID]
		if !ok {
			return nil, false
		}
		result = append(result, id)
	}
	return result, true
}

func catalogEdgesBetweenNodeIDs(catalog CatalogSnapshot, fromNodeID, toNodeID string) []CatalogEdge {
	result := []CatalogEdge{}
	for _, edge := range catalog.Edges {
		if edge.FromNodeID == fromNodeID && edge.ToNodeID == toNodeID {
			result = append(result, edge)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

type authoredRouteBranch struct {
	OccurrenceID string
	EdgeIDs      []string
}

func deriveRowChangeCandidateRoutes(document Document, catalog CatalogSnapshot, rootNodeID, targetNodeID string, routeNodeIDs map[string]string) []rowChangeCandidateRoute {
	paths := catalogEdgePaths(catalog, rootNodeID, targetNodeID)
	if len(paths) == 0 {
		return nil
	}
	branches := authoredRouteBranches(document.Route, rootNodeID, catalog)
	result := make([]rowChangeCandidateRoute, 0)
	seen := map[string]bool{}
	for _, edgePath := range paths {
		maxPrefix := 0
		anchors := []string{RootOccurrenceID}
		for _, branch := range branches {
			prefix := 0
			for prefix < len(edgePath) && prefix < len(branch.EdgeIDs) && edgePath[prefix] == branch.EdgeIDs[prefix] {
				prefix++
			}
			if prefix == 0 {
				continue
			}
			if prefix > maxPrefix {
				maxPrefix = prefix
				anchors = []string{branch.OccurrenceID}
			} else if prefix == maxPrefix {
				anchors = append(anchors, branch.OccurrenceID)
			}
		}
		if maxPrefix > 0 {
			// A catalog path is attached to the deepest already-authored prefix,
			// keeping authored occurrences and their match modes in place.
			for _, anchor := range anchors {
				if routeNodeIDs[anchor] == targetNodeID {
					continue
				}
				candidate := newRowChangeCandidateRoute(document, edgePath, anchor, maxPrefix)
				if candidate.Token == "" || seen[candidate.Token] {
					continue
				}
				seen[candidate.Token] = true
				result = append(result, candidate)
			}
			continue
		}
		candidate := newRowChangeCandidateRoute(document, edgePath, RootOccurrenceID, 0)
		if candidate.Token != "" && !seen[candidate.Token] {
			seen[candidate.Token] = true
			result = append(result, candidate)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Token < result[j].Token })
	return result
}

func newRowChangeCandidateRoute(document Document, edgeIDs []string, anchorOccurrenceID string, anchorDepth int) rowChangeCandidateRoute {
	token, err := encodeRowChangeRouteToken(rowChangeRouteToken{EdgeIDs: append([]string(nil), edgeIDs...), AnchorOccurrenceID: anchorOccurrenceID})
	if err != nil {
		return rowChangeCandidateRoute{}
	}
	return rowChangeCandidateRoute{
		Token: token, OccurrenceID: commandGeneratedID("row_change_", document.Output.ID, documentDigest(document), token, "selected"), EdgeIDs: append([]string(nil), edgeIDs...),
		AnchorOccurrence: anchorOccurrenceID, AnchorDepth: anchorDepth,
	}
}

func encodeRowChangeRouteToken(token rowChangeRouteToken) (string, error) {
	if len(token.EdgeIDs) == 0 || strings.TrimSpace(token.AnchorOccurrenceID) == "" {
		return "", fmt.Errorf("catalog route choice is incomplete")
	}
	raw, err := json.Marshal(token)
	if err != nil {
		return "", err
	}
	return rowChangeRouteTokenPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func catalogEdgePaths(catalog CatalogSnapshot, rootNodeID, targetNodeID string) [][]string {
	edges := append([]CatalogEdge(nil), catalog.Edges...)
	sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
	type endpoints struct {
		from string
		to   string
	}
	knownNodes := make(map[string]bool, len(catalog.Nodes))
	for _, node := range catalog.Nodes {
		knownNodes[node.ID] = true
	}
	maxDepth := len(knownNodes) - 1
	if catalog.RoutePolicy.MaxHops != nil && *catalog.RoutePolicy.MaxHops < maxDepth {
		maxDepth = *catalog.RoutePolicy.MaxHops
	}
	if maxDepth < 1 || !knownNodes[rootNodeID] || !knownNodes[targetNodeID] {
		return nil
	}
	forward := make(map[string][]CatalogEdge)
	reverse := make(map[string][]CatalogEdge)
	reversibleEndpoints := make(map[endpoints]bool, len(edges))
	for _, edge := range edges {
		if edge.ID == "" || !knownNodes[edge.FromNodeID] || !knownNodes[edge.ToNodeID] || (edge.FromNodeID == edge.ToNodeID && !catalog.RoutePolicy.AllowSelfLoops) {
			continue
		}
		reversibleEndpoints[endpoints{from: edge.FromNodeID, to: edge.ToNodeID}] = true
	}
	for _, edge := range edges {
		if edge.ID == "" || !knownNodes[edge.FromNodeID] || !knownNodes[edge.ToNodeID] || (edge.FromNodeID == edge.ToNodeID && !catalog.RoutePolicy.AllowSelfLoops) {
			continue
		}
		if !reversibleEndpoints[endpoints{from: edge.ToNodeID, to: edge.FromNodeID}] {
			continue
		}
		forward[edge.FromNodeID] = append(forward[edge.FromNodeID], edge)
		reverse[edge.ToNodeID] = append(reverse[edge.ToNodeID], edge)
	}
	distances := func(start string, adjacency map[string][]CatalogEdge, reverseDirection bool, limit int) map[string]int {
		result := map[string]int{start: 0}
		queue := []string{start}
		for head := 0; head < len(queue); head++ {
			current := queue[head]
			depth := result[current]
			if depth >= limit {
				continue
			}
			for _, edge := range adjacency[current] {
				next := edge.ToNodeID
				if reverseDirection {
					next = edge.FromNodeID
				}
				if _, seen := result[next]; seen {
					continue
				}
				result[next] = depth + 1
				queue = append(queue, next)
			}
		}
		return result
	}
	fromRoot := distances(rootNodeID, forward, false, maxDepth)
	minimumDepth, found := fromRoot[targetNodeID]
	if !found {
		return nil
	}
	toTarget := distances(targetNodeID, reverse, true, minimumDepth)
	result := make([][]string, 0)
	path := make([]string, 0, minimumDepth)
	var walk func(string)
	walk = func(current string) {
		if current == targetNodeID {
			result = append(result, append([]string(nil), path...))
			return
		}
		depth := fromRoot[current]
		for _, edge := range forward[current] {
			nextDepth, nextSeen := fromRoot[edge.ToNodeID]
			remainingDepth, canReach := toTarget[edge.ToNodeID]
			if !nextSeen || !canReach || nextDepth != depth+1 || nextDepth+remainingDepth != minimumDepth {
				continue
			}
			path = append(path, edge.ID)
			walk(edge.ToNodeID)
			path = path[:len(path)-1]
		}
	}
	walk(rootNodeID)
	return result
}

func authoredRouteBranches(route RouteNode, rootNodeID string, catalog CatalogSnapshot) []authoredRouteBranch {
	result := []authoredRouteBranch{{OccurrenceID: route.OccurrenceID}}
	var walk func(RouteNode, string, []string)
	walk = func(parent RouteNode, parentNodeID string, path []string) {
		for _, child := range parent.Children {
			edge, err := resolveCatalogRouteEdge(catalog, parentNodeID, parent, child)
			if err != nil {
				continue
			}
			childPath := append(append([]string(nil), path...), edge.ID)
			result = append(result, authoredRouteBranch{OccurrenceID: child.OccurrenceID, EdgeIDs: childPath})
			walk(child, edge.ToNodeID, childPath)
		}
	}
	walk(route, rootNodeID, nil)
	return result
}

func materializeRowChangeCandidateRoute(document Document, catalog CatalogSnapshot, rootNodeID string, candidate rowChangeCandidateRoute) (Document, error) {
	cloned, err := cloneDocument(document)
	if err != nil {
		return Document{}, err
	}
	document = cloned
	anchor := findRoute(&document.Route, candidate.AnchorOccurrence)
	if anchor == nil || candidate.AnchorDepth >= len(candidate.EdgeIDs) {
		return Document{}, fmt.Errorf("catalog route anchor %q is unavailable", candidate.AnchorOccurrence)
	}
	nodeIDs, ok := routeNodeIDsFromRoot(document.Route, rootNodeID, catalog)
	if !ok || nodeIDs[candidate.AnchorOccurrence] == "" {
		return Document{}, fmt.Errorf("catalog route anchor %q has no exact catalog identity", candidate.AnchorOccurrence)
	}
	currentNodeID := nodeIDs[candidate.AnchorOccurrence]
	if candidate.AnchorDepth > len(candidate.EdgeIDs) {
		return Document{}, fmt.Errorf("catalog route anchor is deeper than its selected path")
	}
	for index := candidate.AnchorDepth; index < len(candidate.EdgeIDs); index++ {
		edge, found := catalogEdge(catalog, candidate.EdgeIDs[index])
		if !found || edge.FromNodeID != currentNodeID {
			return Document{}, fmt.Errorf("catalog route edge %q does not extend anchor %q", candidate.EdgeIDs[index], candidate.AnchorOccurrence)
		}
		to, found := catalogNode(catalog, edge.ToNodeID)
		if !found {
			return Document{}, fmt.Errorf("catalog route edge %q has no destination node", edge.ID)
		}
		occurrenceID := candidate.OccurrenceID
		if index < len(candidate.EdgeIDs)-1 {
			occurrenceID = commandGeneratedID("row_change_", candidate.OccurrenceID, candidate.Token, index, "step")
		}
		if findRoute(&document.Route, occurrenceID) != nil {
			return Document{}, fmt.Errorf("derived route occurrence %q collides with an authored occurrence", occurrenceID)
		}
		anchor.Children = append(anchor.Children, RouteNode{
			OccurrenceID: occurrenceID, ResourceType: to.ResourceType,
			CatalogEdgeID: edge.ID, Relationship: edge.Label, MatchMode: RouteMatchOptional,
		})
		anchor = &anchor.Children[len(anchor.Children)-1]
		currentNodeID = edge.ToNodeID
	}
	return document, nil
}

func selectReverseEdge(candidates []CatalogEdge, choices []RouteRebaseChoice, occurrenceID, authoredRelationship string) (CatalogEdge, *RowChangeUnresolvedReference) {
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
	if authoredRelationship != "" {
		var matching CatalogEdge
		for _, candidate := range candidates {
			if candidate.Label != authoredRelationship {
				continue
			}
			if matching.ID != "" {
				matching = CatalogEdge{}
				break
			}
			matching = candidate
		}
		if matching.ID != "" {
			return matching, nil
		}
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

func validateRebasedRoute(document Document, catalog CatalogSnapshot, rootNodeID string) error {
	maxDepth := 0
	var walk func(RouteNode, string, int, map[string]bool) error
	walk = func(node RouteNode, nodeID string, depth int, used map[string]bool) error {
		if depth > maxDepth {
			maxDepth = depth
		}
		for _, child := range node.Children {
			edge, err := resolveCatalogRouteEdge(catalog, nodeID, node, child)
			if err != nil {
				return fmt.Errorf("rebased occurrence %q has no unique catalog relationship from %s to %s: %w", child.OccurrenceID, node.ResourceType, child.ResourceType, err)
			}
			edgeID := edge.ID
			if used[edgeID] && !catalog.RoutePolicy.AllowRepeatedEdges {
				return fmt.Errorf("rebased route repeats edge %q", edgeID)
			}
			if !catalog.RoutePolicy.AllowSelfLoops && edge.FromNodeID == edge.ToNodeID {
				return fmt.Errorf("rebased route uses disallowed self-loop edge %q", edgeID)
			}
			next := make(map[string]bool, len(used)+1)
			for id, value := range used {
				next[id] = value
			}
			next[edgeID] = true
			if err := walk(child, edge.ToNodeID, depth+1, next); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(document.Route, rootNodeID, 0, map[string]bool{}); err != nil {
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
	switch p.Mode {
	case "", RowChangeModePreserve:
		if len(p.RouteRebase) == 0 {
			return fmt.Errorf("rowChange.routeRebase is required")
		}
	case RowChangeModeReplace:
		if p.RootOccurrenceID != RootOccurrenceID || p.CatalogSnapshotToken == "" || p.CatalogGeneration == "" || p.CatalogDigest == "" {
			return fmt.Errorf("replacement rowChange requires base occurrence and exact catalog snapshot identity")
		}
		if len(p.RouteRebase) != 0 || len(p.PreservedFeatureKeys) != 0 {
			return fmt.Errorf("replacement rowChange cannot include preserved route choices or feature keys")
		}
	default:
		return fmt.Errorf("rowChange mode %q is unsupported", p.Mode)
	}
	return nil
}
