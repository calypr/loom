// Package authoringv2 contains the hard-cut Explorer Builder authoring
// protocol. It is deliberately separate from the V1 HTTP and persistence
// contracts: this package describes user intent and catalog facts only.
package authoringv2

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
)

const (
	APIVersion                   = "loom.calypr.org/explorer-authoring/v2"
	Kind                         = "ExplorerBuilderDocument"
	WorkspaceKind                = "ExplorerBuilderWorkspace"
	StateKind                    = "ExplorerBuilderState"
	CatalogKind                  = "ExplorerBuilderCatalog"
	AuthoringV2APIVersion        = APIVersion
	ExplorerBuilderV2Kind        = Kind
	ExplorerBuilderStateV2Kind   = StateKind
	ExplorerBuilderCatalogV2Kind = CatalogKind
	RootOccurrenceID             = "base"
)

// Versioned aliases keep call sites explicit while retaining concise domain
// names inside this package.
type AuthoringDocumentV2 = Document
type ExplorerBuilderDocumentV2 = Document
type ExplorerBuilderWorkspaceV2 = Workspace
type BuilderStateV2 = BuilderState
type ExplorerBuilderStateV2 = BuilderState
type CatalogProjectionV2 = CatalogSnapshot
type ExplorerBuilderCatalogV2 = CatalogSnapshot
type RoutePolicyV2 = RoutePolicy
type RouteStepV2 = RouteStep
type SelectionV2 = Selection
type OutputV2 = Output
type PresentationV2 = Presentation
type CatalogNodeV2 = CatalogNode
type CatalogEdgeV2 = CatalogEdge
type CatalogCandidateV2 = CatalogCandidate

// Document is the complete durable Builder intent. A route is a finite edge
// path from RootNodeID. Its root occurrence is always "base" and its tail is
// computed from the path; neither is a second authored node identity.
type Document struct {
	APIVersion       string        `json:"-"`
	Kind             string        `json:"kind"`
	Output           Output        `json:"output"`
	RootResourceType string        `json:"rootResourceType,omitempty"`
	Route            RouteNode     `json:"route,omitempty"`
	Columns          []Column      `json:"columns,omitempty"`
	FixedFilters     []FixedFilter `json:"fixedFilters,omitempty"`
	Actions          []Action      `json:"actions,omitempty"`

	// The opaque, linear authoring model is retained only for source-compatible
	// internal tests while the hard-cut wire decoder rejects those fields.
	RootNodeID   string                  `json:"-"`
	RouteSteps   []RouteStep             `json:"-"`
	Selections   []Selection             `json:"-"`
	Presentation map[string]Presentation `json:"-"`
}

type Output struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	RowLabel string `json:"rowLabel,omitempty"`
}

type RouteStep struct {
	EdgeID       string `json:"edgeId"`
	OccurrenceID string `json:"occurrenceId,omitempty"`
}

type Selection struct {
	CandidateID    string `json:"candidateId"`
	OccurrenceID   string `json:"occurrenceId,omitempty"`
	ProjectionMode string `json:"projectionMode"`
}

// Workspace is the atomic authoring unit. Documents are independent table
// intents; tabs provide their ordered, visible runtime presentation.
type Workspace struct {
	APIVersion    string                           `json:"apiVersion"`
	Kind          string                           `json:"kind"`
	Explorer      ExplorerMetadata                 `json:"explorer"`
	Documents     []Document                       `json:"documents"`
	Tabs          []Tab                            `json:"tabs"`
	SharedFilters map[string][]SharedFilterBinding `json:"sharedFilters,omitempty"`
	FileActions   *FileActions                     `json:"fileActions,omitempty"`
}

type Tab struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	OutputID string `json:"outputId"`
	Order    int    `json:"order"`
	Visible  bool   `json:"visible"`
}

// Presentation contains display intent only. In particular, it has no
// selector, expression, physical collection, or generated-column fields.
type Presentation struct {
	Label   string              `json:"label,omitempty"`
	Visible *bool               `json:"visible,omitempty"`
	Order   *int                `json:"order,omitempty"`
	Table   *TablePresentation  `json:"table,omitempty"`
	Filter  *FilterPresentation `json:"filter,omitempty"`
	Chart   *ChartPresentation  `json:"chart,omitempty"`
}

type TablePresentation struct {
	Visible      *bool  `json:"visible,omitempty"`
	Order        *int   `json:"order,omitempty"`
	Pinned       bool   `json:"pinned,omitempty"`
	CellRenderer string `json:"cellRenderer,omitempty"`
}
type FilterPresentation struct {
	Label string `json:"label,omitempty"`
	Order *int   `json:"order,omitempty"`
}
type ChartPresentation struct {
	Type  string `json:"type"`
	Title string `json:"title,omitempty"`
	Order *int   `json:"order,omitempty"`
}

func PresentationKey(candidateID, occurrenceID, projectionMode string) string {
	encode := func(value string) string { return strings.ReplaceAll(url.QueryEscape(value), "+", "%20") }
	return encode(candidateID) + "::" + encode(occurrenceID) + "::" + encode(projectionMode)
}

// CatalogSnapshot is an immutable, authorization-scoped projection. The
// token identifies the exact snapshot used to validate a document.
type CatalogSnapshot struct {
	APIVersion               string              `json:"-"`
	Kind                     string              `json:"-"`
	Project                  string              `json:"-"`
	ExplorerID               string              `json:"-"`
	SourceGeneration         string              `json:"generation"`
	AuthorizationScopeDigest string              `json:"authorizationScopeDigest"`
	ResolvedSchemaDigest     string              `json:"resolvedSchemaDigest,omitempty"`
	SnapshotToken            string              `json:"snapshotToken"`
	Complete                 bool                `json:"complete"`
	Truncated                bool                `json:"-"`
	Diagnostics              []CatalogDiagnostic `json:"-"`
	Nodes                    []CatalogNode       `json:"nodes"`
	Edges                    []CatalogEdge       `json:"edges"`
	Candidates               []CatalogCandidate  `json:"candidates"`
	RoutePolicy              RoutePolicy         `json:"routePolicy"`
}

type CatalogDiagnostic struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

type CatalogNode struct {
	ID              string `json:"nodeId"`
	ResourceType    string `json:"resourceType"`
	RowRootEligible bool   `json:"rowRootEligible"`
	RowGrain        string `json:"rowGrain,omitempty"`
	Populated       bool   `json:"populated"`
	DocumentCount   *int64 `json:"documentCount,omitempty"`
}

type CatalogEdge struct {
	ID         string `json:"edgeId"`
	FromNodeID string `json:"fromNodeId"`
	ToNodeID   string `json:"toNodeId"`
	Label      string `json:"label"`
	Populated  bool   `json:"populated"`
}

type CatalogCandidate struct {
	ID                    string   `json:"candidateId"`
	NodeID                string   `json:"nodeId"`
	FieldPath             string   `json:"fieldPath"`
	Label                 string   `json:"label"`
	LogicalType           string   `json:"logicalType"`
	Repeated              bool     `json:"repeated"`
	Filterable            bool     `json:"filterable"`
	Chartable             bool     `json:"chartable"`
	ProjectionModes       []string `json:"projectionModes"`
	DefaultProjectionMode string   `json:"defaultProjectionMode"`
	FilterOperators       []string `json:"-"`
	ChartOperations       []string `json:"-"`
	Cardinality           string   `json:"-"`
	Populated             bool     `json:"-"`
	Count                 *int64   `json:"-"`
	SuggestionsAvailable  bool     `json:"-"`
	SuggestionsComplete   bool     `json:"-"`
	SuggestionsTruncated  bool     `json:"-"`
	SuggestionCount       int      `json:"-"`
}

// RoutePolicy has no default hop ceiling: nil MaxHops means every finite
// route is valid. Repeated edges and self-loops are explicit capabilities.
type RoutePolicy struct {
	MaxHops            *int `json:"maxSteps"`
	Unbounded          bool `json:"-"`
	AllowRepeatedEdges bool `json:"allowRepeatedEdges"`
	AllowSelfLoops     bool `json:"allowSelfLoops"`
}

// BuilderState joins one workspace to the one catalog snapshot that proves it.
type BuilderState struct {
	APIVersion     string     `json:"apiVersion"`
	Kind           string     `json:"kind"`
	LifecycleState string     `json:"lifecycleState"`
	Workspace      *Workspace `json:"workspace"`
	// Document is retained only as a source-compatible internal migration aid.
	Document *Document       `json:"-"`
	Catalog  CatalogSnapshot `json:"catalog"`
}

// RouteOccurrence is derived, never wire-authored. The first occurrence has
// ID "base". Unnamed route steps receive stable step-N IDs; TailOccurrence
// returns the final derived occurrence (base for an empty route).
type RouteOccurrence struct {
	ID             string
	NodeID         string
	IncomingEdgeID string
}

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
func (d Document) CanonicalJSON() ([]byte, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	n := d
	n.RouteSteps = append([]RouteStep(nil), d.RouteSteps...)
	n.Selections = append([]Selection(nil), d.Selections...)
	sort.SliceStable(n.Selections, func(i, j int) bool {
		left := n.Selections[i].CandidateID + "\x00" + n.Selections[i].OccurrenceID
		right := n.Selections[j].CandidateID + "\x00" + n.Selections[j].OccurrenceID
		return left < right
	})
	if n.RouteSteps == nil {
		n.RouteSteps = []RouteStep{}
	}
	if n.Selections == nil {
		n.Selections = []Selection{}
	}
	if n.Presentation == nil {
		n.Presentation = map[string]Presentation{}
	}
	return json.Marshal(n)
}

func (w Workspace) CanonicalJSON() ([]byte, error) {
	if err := w.Validate(); err != nil {
		return nil, err
	}
	n := w.NormalizePresentationOrders()
	n.Documents = append([]Document(nil), n.Documents...)
	for i := range n.Documents {
		n.Documents[i].APIVersion = ""
		n.Documents[i].Selections = append([]Selection(nil), n.Documents[i].Selections...)
		sort.SliceStable(n.Documents[i].Selections, func(a, b int) bool {
			left := n.Documents[i].Selections[a]
			right := n.Documents[i].Selections[b]
			return left.CandidateID+"\x00"+left.OccurrenceID+"\x00"+left.ProjectionMode < right.CandidateID+"\x00"+right.OccurrenceID+"\x00"+right.ProjectionMode
		})
		if n.Documents[i].RouteSteps == nil {
			n.Documents[i].RouteSteps = []RouteStep{}
		}
		if n.Documents[i].Selections == nil {
			n.Documents[i].Selections = []Selection{}
		}
		if n.Documents[i].Presentation == nil {
			n.Documents[i].Presentation = map[string]Presentation{}
		}
	}
	n.Tabs = append([]Tab(nil), w.Tabs...)
	if n.Documents == nil {
		n.Documents = []Document{}
	}
	if n.Tabs == nil {
		n.Tabs = []Tab{}
	}
	return json.Marshal(n)
}

// NormalizePresentationOrders gives every table column one unambiguous,
// contiguous presentation position. Authored order is the primary key and the
// stable public column identity breaks ties. The normalized order also becomes
// the recipe projection order, so presentation and execution cannot disagree.
//
// Duplicate presentation positions are valid mutable Builder input. Freezing
// them here keeps equivalent requests from depending on frontend collection or
// map iteration order and makes the normalized workspace safe to persist and
// return to the Builder.
func (w Workspace) NormalizePresentationOrders() Workspace {
	n := w
	n.Documents = append([]Document(nil), w.Documents...)
	for documentIndex := range n.Documents {
		document := &n.Documents[documentIndex]
		columns := append([]Column(nil), document.Columns...)
		for columnIndex := range columns {
			column := &columns[columnIndex]
			if column.Table != nil {
				table := *column.Table
				column.Table = &table
			}
			if column.Filter != nil {
				filter := *column.Filter
				column.Filter = &filter
			}
			if column.Chart != nil {
				chart := *column.Chart
				column.Chart = &chart
			}
		}
		sort.SliceStable(columns, func(i, j int) bool {
			left, right := columns[i], columns[j]
			leftClass, leftOrder := presentationOrder(left)
			rightClass, rightOrder := presentationOrder(right)
			if leftClass != rightClass {
				return leftClass < rightClass
			}
			if leftOrder != rightOrder {
				return leftOrder < rightOrder
			}
			return left.Column < right.Column
		})
		tableOrder := 0
		for columnIndex := range columns {
			if columns[columnIndex].Table == nil {
				continue
			}
			value := tableOrder
			columns[columnIndex].Table.Order = &value
			tableOrder++
		}
		normalizeFilterOrders(columns)
		normalizeChartOrders(columns)
		document.Columns = columns
	}
	return n
}

func normalizeFilterOrders(columns []Column) {
	indexes := make([]int, 0, len(columns))
	for index := range columns {
		if columns[index].Filter != nil {
			indexes = append(indexes, index)
		}
	}
	sort.SliceStable(indexes, func(i, j int) bool {
		left, right := columns[indexes[i]], columns[indexes[j]]
		return auxiliaryPresentationLess(left.Filter.Order, left.Column, right.Filter.Order, right.Column)
	})
	for order, index := range indexes {
		value := order
		columns[index].Filter.Order = &value
	}
}

func normalizeChartOrders(columns []Column) {
	indexes := make([]int, 0, len(columns))
	for index := range columns {
		if columns[index].Chart != nil {
			indexes = append(indexes, index)
		}
	}
	sort.SliceStable(indexes, func(i, j int) bool {
		left, right := columns[indexes[i]], columns[indexes[j]]
		return auxiliaryPresentationLess(left.Chart.Order, left.Column, right.Chart.Order, right.Column)
	})
	for order, index := range indexes {
		value := order
		columns[index].Chart.Order = &value
	}
}

func auxiliaryPresentationLess(leftOrder *int, leftColumn string, rightOrder *int, rightColumn string) bool {
	if (leftOrder == nil) != (rightOrder == nil) {
		return leftOrder != nil
	}
	if leftOrder != nil && *leftOrder != *rightOrder {
		return *leftOrder < *rightOrder
	}
	return leftColumn < rightColumn
}

func presentationOrder(column Column) (class, order int) {
	if column.Table == nil {
		return 2, 0
	}
	if column.Table.Order == nil {
		return 1, 0
	}
	return 0, *column.Table.Order
}

func (w Workspace) Digest() (string, error) {
	raw, err := w.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (d Document) Digest() (string, error) {
	raw, err := d.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (c CatalogSnapshot) CanonicalJSON() ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	n := c
	n.Nodes = append([]CatalogNode(nil), c.Nodes...)
	n.Edges = append([]CatalogEdge(nil), c.Edges...)
	n.Candidates = append([]CatalogCandidate(nil), c.Candidates...)
	n.Diagnostics = append([]CatalogDiagnostic(nil), c.Diagnostics...)
	for i := range n.Candidates {
		n.Candidates[i].ProjectionModes = append([]string(nil), n.Candidates[i].ProjectionModes...)
		n.Candidates[i].FilterOperators = append([]string(nil), n.Candidates[i].FilterOperators...)
		n.Candidates[i].ChartOperations = append([]string(nil), n.Candidates[i].ChartOperations...)
		sort.Strings(n.Candidates[i].ProjectionModes)
		sort.Strings(n.Candidates[i].FilterOperators)
		sort.Strings(n.Candidates[i].ChartOperations)
	}
	sort.Slice(n.Nodes, func(i, j int) bool { return n.Nodes[i].ID < n.Nodes[j].ID })
	sort.Slice(n.Edges, func(i, j int) bool { return n.Edges[i].ID < n.Edges[j].ID })
	sort.Slice(n.Candidates, func(i, j int) bool { return n.Candidates[i].ID < n.Candidates[j].ID })
	sort.Slice(n.Diagnostics, func(i, j int) bool {
		if n.Diagnostics[i].Code != n.Diagnostics[j].Code {
			return n.Diagnostics[i].Code < n.Diagnostics[j].Code
		}
		return n.Diagnostics[i].Message < n.Diagnostics[j].Message
	})
	return json.Marshal(n)
}

func (c CatalogSnapshot) Digest() (string, error) {
	raw, err := c.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (s BuilderState) CanonicalJSON() ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	var workspace []byte
	var err error
	if s.Workspace == nil {
		workspace = []byte("null")
	} else {
		workspace, err = s.Workspace.CanonicalJSON()
		if err != nil {
			return nil, err
		}
	}
	catalog, err := s.Catalog.CanonicalJSON()
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		APIVersion string          `json:"apiVersion"`
		Kind       string          `json:"kind"`
		Workspace  json.RawMessage `json:"workspace"`
		Catalog    json.RawMessage `json:"catalog"`
	}{s.APIVersion, s.Kind, workspace, catalog})
}

func (s BuilderState) Digest() (string, error) {
	raw, err := s.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func DecodeDocument(raw []byte) (Document, error) {
	var wire struct {
		APIVersion   string                  `json:"apiVersion,omitempty"`
		Kind         string                  `json:"kind"`
		Output       Output                  `json:"output"`
		RootNodeID   string                  `json:"rootNodeId"`
		RouteSteps   []RouteStep             `json:"routeSteps"`
		Selections   []Selection             `json:"selections"`
		Presentation map[string]Presentation `json:"presentation"`
	}
	if err := strictDecode(raw, &wire); err != nil {
		return Document{}, err
	}
	out := Document{APIVersion: wire.APIVersion, Kind: wire.Kind, Output: wire.Output, RootNodeID: wire.RootNodeID, RouteSteps: wire.RouteSteps, Selections: wire.Selections, Presentation: wire.Presentation}
	if err := out.Validate(); err != nil {
		return out, err
	}
	return out, nil
}

func DecodeWorkspace(raw []byte) (Workspace, error) {
	var out Workspace
	if err := strictDecode(raw, &out); err != nil {
		return out, err
	}
	if err := out.Validate(); err != nil {
		return out, err
	}
	return out, nil
}

func strictDecode(raw []byte, target any) error {
	if err := rejectDuplicateKeys(raw); err != nil {
		return fmt.Errorf("strict decode: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return fmt.Errorf("strict decode: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("strict decode: trailing JSON value")
		}
		return fmt.Errorf("strict decode: %w", err)
	}
	return nil
}

func rejectDuplicateKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var walk func() error
	walk = func() error {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for dec.More() {
				key, err := dec.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok {
					return fmt.Errorf("object key is not a string")
				}
				if seen[name] {
					return fmt.Errorf("duplicate JSON key %q", name)
				}
				seen[name] = true
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		case '[':
			for dec.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		}
		return nil
	}
	if err := walk(); err != nil {
		return err
	}
	return nil
}
