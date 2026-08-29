package authoringv2

import (
	"fmt"
	"strings"
)

func (d Document) Occurrences(c CatalogSnapshot) ([]RouteOccurrence, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	edges := c.edgeIndex()
	current := d.RootNodeID
	occ := []RouteOccurrence{{ID: RootOccurrenceID, NodeID: current}}
	for i, step := range d.RouteSteps {
		e := edges[step.EdgeID]
		if e == nil {
			return nil, fmt.Errorf("route step %d references unknown edge %q", i, step.EdgeID)
		}
		if e.FromNodeID != current {
			return nil, fmt.Errorf("route step %d is disconnected: edge starts at %q, expected %q", i, e.FromNodeID, current)
		}
		id := step.OccurrenceID
		if id == "" {
			id = DerivedOccurrenceID(i)
		}
		occ = append(occ, RouteOccurrence{ID: id, NodeID: e.ToNodeID, IncomingEdgeID: e.ID})
		current = e.ToNodeID
	}
	return occ, nil
}

func (d Document) TailOccurrence(c CatalogSnapshot) (RouteOccurrence, error) {
	occ, err := d.Occurrences(c)
	if err != nil {
		return RouteOccurrence{}, err
	}
	return occ[len(occ)-1], nil
}

func (d Document) TailOccurrenceID(c CatalogSnapshot) (string, error) {
	tail, err := d.TailOccurrence(c)
	if err != nil {
		return "", err
	}
	return tail.ID, nil
}

func DerivedOccurrenceID(stepIndex int) string {
	return fmt.Sprintf("step-%d", stepIndex+1)
}

func (d Document) Validate() error {
	if d.Kind != Kind {
		return fmt.Errorf("unsupported V2 document protocol or kind")
	}
	if emptyID(d.Output.ID) || strings.TrimSpace(d.Output.Title) == "" {
		return fmt.Errorf("output id and title are required")
	}
	if !physicalColumnPattern.MatchString(d.Output.ID) {
		return fmt.Errorf("output id must contain only letters, digits, and underscores and may not start with a digit")
	}
	if d.semantic() {
		return d.validateSemantic()
	}
	if emptyID(d.RootNodeID) {
		return fmt.Errorf("rootNodeId is required")
	}
	seenOccurrences := map[string]bool{RootOccurrenceID: true}
	for i, step := range d.RouteSteps {
		if emptyID(step.EdgeID) {
			return fmt.Errorf("routeSteps[%d].edgeId is required", i)
		}
		occurrenceID := step.OccurrenceID
		if occurrenceID == "" {
			occurrenceID = DerivedOccurrenceID(i)
		}
		if step.OccurrenceID != "" {
			if step.OccurrenceID == RootOccurrenceID {
				return fmt.Errorf("routeSteps[%d] cannot author the derived base occurrence", i)
			}
			if emptyID(step.OccurrenceID) {
				return fmt.Errorf("routeSteps[%d].occurrenceId is invalid", i)
			}
		}
		if seenOccurrences[occurrenceID] {
			return fmt.Errorf("duplicate route occurrence id %q", occurrenceID)
		}
		seenOccurrences[occurrenceID] = true
	}
	seenSelections := map[string]bool{}
	for i, selection := range d.Selections {
		if emptyID(selection.CandidateID) {
			return fmt.Errorf("selections[%d].candidateId is required", i)
		}
		if strings.TrimSpace(selection.ProjectionMode) == "" {
			return fmt.Errorf("selections[%d].projectionMode is required", i)
		}
		if selection.OccurrenceID == "base" && selection.OccurrenceID != "" { /* base is valid */
		}
		if selection.OccurrenceID != "" && emptyID(selection.OccurrenceID) {
			return fmt.Errorf("selections[%d].occurrenceId is invalid", i)
		}
		key := selection.CandidateID + "\x00" + selection.OccurrenceID + "\x00" + selection.ProjectionMode
		if seenSelections[key] {
			return fmt.Errorf("duplicate selection %q", selection.CandidateID)
		}
		seenSelections[key] = true
	}
	for key, p := range d.Presentation {
		if emptyID(key) {
			return fmt.Errorf("presentation keys must be non-empty IDs")
		}
		if p.Order != nil && *p.Order < 0 {
			return fmt.Errorf("presentation[%q].order must not be negative", key)
		}
	}
	return nil
}

func (w Workspace) Validate() error {
	if w.APIVersion != APIVersion || w.Kind != WorkspaceKind {
		return fmt.Errorf("unsupported V2 workspace protocol or kind")
	}
	semanticWorkspace := false
	for _, document := range w.Documents {
		semanticWorkspace = semanticWorkspace || document.semantic()
	}
	if semanticWorkspace && strings.TrimSpace(w.Explorer.Title) == "" {
		return fmt.Errorf("explorer.title is required")
	}
	outputs, tabs, tabOutputs, orders := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[int]bool{}
	for i, d := range w.Documents {
		if err := d.Validate(); err != nil {
			return fmt.Errorf("documents[%d]: %w", i, err)
		}
		if outputs[d.Output.ID] {
			return fmt.Errorf("DUPLICATE_OUTPUT_ID: documents[%d].output.id", i)
		}
		outputs[d.Output.ID] = true
	}
	for i, tab := range w.Tabs {
		if emptyID(tab.ID) || strings.TrimSpace(tab.Title) == "" || emptyID(tab.OutputID) || tab.Order < 0 {
			return fmt.Errorf("tabs[%d] is invalid", i)
		}
		if tabs[tab.ID] {
			return fmt.Errorf("DUPLICATE_TAB_ID: tabs[%d].id", i)
		}
		if tabOutputs[tab.OutputID] || !outputs[tab.OutputID] {
			return fmt.Errorf("INVALID_TAB_OUTPUT_MAPPING: tabs[%d].outputId", i)
		}
		if orders[tab.Order] {
			return fmt.Errorf("INVALID_TAB_ORDER: tabs[%d].order", i)
		}
		tabs[tab.ID], tabOutputs[tab.OutputID], orders[tab.Order] = true, true, true
	}
	if len(w.Tabs) != len(w.Documents) {
		return fmt.Errorf("INVALID_TAB_OUTPUT_MAPPING: exactly one tab is required per document")
	}
	for output := range outputs {
		if !tabOutputs[output] {
			return fmt.Errorf("INVALID_TAB_OUTPUT_MAPPING: output %q has no tab", output)
		}
	}
	for i := 0; i < len(orders); i++ {
		if !orders[i] {
			return fmt.Errorf("INVALID_TAB_ORDER: orders must be contiguous from zero")
		}
	}
	if err := w.validateSemanticBindings(); err != nil {
		return err
	}
	return nil
}

// ValidateForPublication applies constraints that are intentionally too strict
// for mutable Builder state. Changing a table root or route temporarily clears
// its selections; that intermediate workspace remains compilable, but a visible
// table must have at least one visible column before publication.
func (w Workspace) ValidateForPublication() error {
	visibleOutputs := make(map[string]bool, len(w.Tabs))
	for _, tab := range w.Tabs {
		visibleOutputs[tab.OutputID] = tab.Visible
	}
	for i, document := range w.Documents {
		if !visibleOutputs[document.Output.ID] {
			continue
		}
		if document.semantic() {
			visible := 0
			for _, column := range document.Columns {
				if column.Table != nil && (column.Table.Visible == nil || *column.Table.Visible) {
					visible++
				}
			}
			if visible == 0 {
				return fmt.Errorf("NO_VISIBLE_COLUMNS: documents[%d]", i)
			}
			continue
		}
		hidden := 0
		for _, presentation := range document.Presentation {
			if presentation.Visible != nil && !*presentation.Visible {
				hidden++
			}
		}
		if len(document.Selections) == 0 || hidden == len(document.Selections) {
			return fmt.Errorf("NO_VISIBLE_COLUMNS: documents[%d]", i)
		}
	}
	return nil
}

func (c CatalogSnapshot) Validate() error {
	if c.APIVersion != APIVersion || c.Kind != CatalogKind {
		return fmt.Errorf("unsupported V2 catalog protocol or kind")
	}
	if strings.TrimSpace(c.SnapshotToken) == "" {
		return fmt.Errorf("catalog snapshotToken is required")
	}
	if strings.TrimSpace(c.Project) == "" || strings.TrimSpace(c.ExplorerID) == "" || strings.TrimSpace(c.SourceGeneration) == "" || strings.TrimSpace(c.AuthorizationScopeDigest) == "" {
		return fmt.Errorf("catalog project, explorerId, sourceGeneration, and authorizationScopeDigest are required")
	}
	if !c.Complete || c.Truncated {
		return fmt.Errorf("catalog snapshot is incomplete or truncated")
	}
	if c.RoutePolicy.MaxHops != nil && *c.RoutePolicy.MaxHops < 0 {
		return fmt.Errorf("routePolicy.maxHops must not be negative")
	}
	if c.RoutePolicy.MaxHops == nil && !c.RoutePolicy.Unbounded {
		return fmt.Errorf("routePolicy must explicitly declare an unbounded route when maxHops is absent")
	}
	if c.RoutePolicy.MaxHops != nil && c.RoutePolicy.Unbounded {
		return fmt.Errorf("routePolicy cannot be both bounded and unbounded")
	}
	nodes := map[string]CatalogNode{}
	for i, n := range c.Nodes {
		if emptyID(n.ID) || strings.TrimSpace(n.ResourceType) == "" {
			return fmt.Errorf("nodes[%d] is invalid", i)
		}
		if _, ok := nodes[n.ID]; ok {
			return fmt.Errorf("duplicate catalog node %q", n.ID)
		}
		nodes[n.ID] = n
	}
	edges := map[string]CatalogEdge{}
	for i, e := range c.Edges {
		if emptyID(e.ID) || nodes[e.FromNodeID].ID == "" || nodes[e.ToNodeID].ID == "" {
			return fmt.Errorf("edges[%d] has stale node reference", i)
		}
		if _, ok := edges[e.ID]; ok {
			return fmt.Errorf("duplicate catalog edge %q", e.ID)
		}
		if e.FromNodeID == e.ToNodeID && !c.RoutePolicy.AllowSelfLoops {
			return fmt.Errorf("self-loop edge %q is not allowed by route policy", e.ID)
		}
		edges[e.ID] = e
	}
	candidates := map[string]CatalogCandidate{}
	for i, candidate := range c.Candidates {
		if emptyID(candidate.ID) || nodes[candidate.NodeID].ID == "" {
			return fmt.Errorf("candidates[%d] has stale node reference", i)
		}
		if _, ok := candidates[candidate.ID]; ok {
			return fmt.Errorf("duplicate catalog candidate %q", candidate.ID)
		}
		if len(candidate.ProjectionModes) == 0 || candidate.DefaultProjectionMode == "" {
			return fmt.Errorf("candidate %q must advertise projection modes and a default", candidate.ID)
		}
		if candidate.DefaultProjectionMode != "" {
			foundMode := false
			for _, mode := range candidate.ProjectionModes {
				if strings.TrimSpace(mode) == "" {
					return fmt.Errorf("candidates[%d] contains an empty projection mode", i)
				}
				if mode == candidate.DefaultProjectionMode {
					foundMode = true
				}
			}
			if len(candidate.ProjectionModes) > 0 && !foundMode {
				return fmt.Errorf("candidate %q default projection mode is not listed", candidate.ID)
			}
		}
		if candidate.Count != nil && *candidate.Count < 0 {
			return fmt.Errorf("candidate %q count must not be negative", candidate.ID)
		}
		if candidate.SuggestionCount < 0 {
			return fmt.Errorf("candidate %q suggestionCount must not be negative", candidate.ID)
		}
		if candidate.SuggestionsComplete && candidate.SuggestionsTruncated {
			return fmt.Errorf("candidate %q suggestions cannot be complete and truncated", candidate.ID)
		}
		candidates[candidate.ID] = candidate
	}
	return nil
}

func (s BuilderState) Validate() error {
	if s.DraftVersion < 0 {
		return fmt.Errorf("draftVersion must not be negative")
	}
	if s.APIVersion != APIVersion || s.Kind != StateKind {
		return fmt.Errorf("unsupported V2 builder state protocol or kind")
	}
	if err := s.Catalog.Validate(); err != nil {
		return err
	}
	if s.LifecycleState == "" {
		if s.Workspace == nil {
			s.LifecycleState = LifecycleNew
		} else {
			s.LifecycleState = LifecycleReady
		}
	}
	if s.LifecycleState != LifecycleNew && s.LifecycleState != LifecycleReady {
		return fmt.Errorf("unsupported lifecycleState %q", s.LifecycleState)
	}
	if s.LifecycleState == LifecycleNew && s.Workspace != nil {
		return fmt.Errorf("NEW Builder state must have workspace null")
	}
	if s.LifecycleState == LifecycleReady && s.Workspace == nil {
		return fmt.Errorf("READY Builder state requires workspace")
	}
	workspace := s.Workspace
	if workspace == nil && s.Document != nil {
		workspace = &Workspace{APIVersion: APIVersion, Kind: WorkspaceKind, Documents: []Document{*s.Document}, Tabs: []Tab{{ID: s.Document.Output.ID, Title: s.Document.Output.Title, OutputID: s.Document.Output.ID, Order: 0, Visible: true}}}
	}
	if workspace == nil {
		return nil
	}
	if err := workspace.Validate(); err != nil {
		return err
	}
	for i := range workspace.Documents {
		document := workspace.Documents[i]
		if document.semantic() {
			continue
		}
		one := s
		one.Workspace = nil
		one.Document = &document
		if err := one.validateDocument(); err != nil {
			return fmt.Errorf("documents[%d]: %w", i, err)
		}
	}
	return nil
}

func (s BuilderState) validateDocument() error {
	d := s.Document
	if d == nil {
		return nil
	}
	edges, candidates, nodes := s.Catalog.edgeIndex(), s.Catalog.candidateIndex(), s.Catalog.nodeIndex()
	if nodes[d.RootNodeID].ID == "" {
		return fmt.Errorf("document rootNodeId %q is stale", d.RootNodeID)
	}
	if !nodes[d.RootNodeID].RowRootEligible {
		return fmt.Errorf("ROW_ROOT_NOT_ELIGIBLE: rootNodeId %q", d.RootNodeID)
	}
	seenEdges := map[string]bool{}
	current := d.RootNodeID
	for i, step := range d.RouteSteps {
		e, ok := edges[step.EdgeID]
		if !ok {
			return fmt.Errorf("routeSteps[%d] references stale edge %q", i, step.EdgeID)
		}
		if e.FromNodeID != current {
			return fmt.Errorf("routeSteps[%d] is disconnected: edge starts at %q, expected %q", i, e.FromNodeID, current)
		}
		if seenEdges[e.ID] && !s.Catalog.RoutePolicy.AllowRepeatedEdges {
			return fmt.Errorf("repeated edge %q is not allowed by route policy", e.ID)
		}
		seenEdges[e.ID] = true
		current = e.ToNodeID
	}
	if s.Catalog.RoutePolicy.MaxHops != nil && len(d.RouteSteps) > *s.Catalog.RoutePolicy.MaxHops {
		return fmt.Errorf("route exceeds routePolicy.maxHops")
	}
	occ, err := d.Occurrences(s.Catalog)
	if err != nil {
		return err
	}
	occByID := map[string]RouteOccurrence{}
	for _, item := range occ {
		occByID[item.ID] = item
	}
	for i, selection := range d.Selections {
		candidate, ok := candidates[selection.CandidateID]
		if !ok {
			return fmt.Errorf("selections[%d] references stale candidate %q", i, selection.CandidateID)
		}
		id := selection.OccurrenceID
		if id == "" {
			id = occ[len(occ)-1].ID
		}
		item, ok := occByID[id]
		if !ok {
			return fmt.Errorf("selections[%d] references stale occurrence %q", i, id)
		}
		if candidate.NodeID != item.NodeID {
			return fmt.Errorf("selection %q belongs to node %q, not occurrence %q node %q", selection.CandidateID, candidate.NodeID, id, item.NodeID)
		}
		projectionAllowed := false
		for _, mode := range candidate.ProjectionModes {
			if selection.ProjectionMode == mode {
				projectionAllowed = true
				break
			}
		}
		if !projectionAllowed {
			return fmt.Errorf("selection %q projection mode %q is not advertised", selection.CandidateID, selection.ProjectionMode)
		}
	}
	seenResolvedSelections := map[string]bool{}
	for i, selection := range d.Selections {
		occurrenceID := effectiveOccurrence(*d, s.Catalog, selection.OccurrenceID)
		key := selection.CandidateID + "\x00" + occurrenceID + "\x00" + selection.ProjectionMode
		if seenResolvedSelections[key] {
			return fmt.Errorf("duplicate selection at selections[%d]", i)
		}
		seenResolvedSelections[key] = true
	}
	selectionKeys := map[string]bool{}
	for _, selection := range d.Selections {
		selectionKeys[PresentationKey(selection.CandidateID, effectiveOccurrence(*d, s.Catalog, selection.OccurrenceID), selection.ProjectionMode)] = true
	}
	for key, p := range d.Presentation {
		if !selectionKeys[key] {
			return fmt.Errorf("presentation %q does not resolve to a selection", key)
		}
		for _, selection := range d.Selections {
			if PresentationKey(selection.CandidateID, effectiveOccurrence(*d, s.Catalog, selection.OccurrenceID), selection.ProjectionMode) != key {
				continue
			}
			candidate := candidates[selection.CandidateID]
			if p.Filter != nil && !candidate.Filterable {
				return fmt.Errorf("UNSUPPORTED_FILTER: presentation %q", key)
			}
			if p.Chart != nil && !candidate.Chartable {
				return fmt.Errorf("UNSUPPORTED_CHART: presentation %q", key)
			}
		}
	}
	return nil
}

func effectiveOccurrence(d Document, c CatalogSnapshot, authored string) string {
	if authored != "" {
		return authored
	}
	occ, err := d.Occurrences(c)
	if err != nil || len(occ) == 0 {
		return authored
	}
	return occ[len(occ)-1].ID
}

func (c CatalogSnapshot) nodeIndex() map[string]CatalogNode {
	out := map[string]CatalogNode{}
	for _, n := range c.Nodes {
		out[n.ID] = n
	}
	return out
}
func (c CatalogSnapshot) edgeIndex() map[string]*CatalogEdge {
	out := map[string]*CatalogEdge{}
	for i := range c.Edges {
		out[c.Edges[i].ID] = &c.Edges[i]
	}
	return out
}
func (c CatalogSnapshot) candidateIndex() map[string]CatalogCandidate {
	out := map[string]CatalogCandidate{}
	for _, n := range c.Candidates {
		out[n.ID] = n
	}
	return out
}

func emptyID(value string) bool {
	return strings.TrimSpace(value) == "" || value != strings.TrimSpace(value)
}

// CanonicalJSON and Digest provide content-addressed, deterministic wire
// representations. Catalog collections and unordered selections are sorted;
// routeSteps retain their authored path order.
