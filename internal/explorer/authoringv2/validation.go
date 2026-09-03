package authoringv2

import (
	"fmt"
	"strings"
)

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
	return d.validateSemantic()
}

func (w Workspace) Validate() error {
	if w.APIVersion != APIVersion || w.Kind != WorkspaceKind {
		return fmt.Errorf("unsupported V2 workspace protocol or kind")
	}
	if strings.TrimSpace(w.Explorer.Title) == "" {
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
		visible := 0
		for _, column := range document.Columns {
			if column.Table != nil && (column.Table.Visible == nil || *column.Table.Visible) {
				visible++
			}
		}
		if visible == 0 {
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
	if s.Workspace == nil {
		return nil
	}
	return s.Workspace.Validate()
}

func emptyID(value string) bool {
	return strings.TrimSpace(value) == "" || value != strings.TrimSpace(value)
}

// CanonicalJSON and Digest provide content-addressed, deterministic wire
// representations. Catalog collections and unordered selections are sorted;
// routeSteps retain their authored path order.
