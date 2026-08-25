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
	"sort"
	"strings"
)

const (
	APIVersion                   = "loom.calypr.org/explorer-authoring/v2"
	Kind                         = "ExplorerBuilderDocument"
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
	APIVersion   string                  `json:"apiVersion"`
	Kind         string                  `json:"kind"`
	Output       Output                  `json:"output"`
	RootNodeID   string                  `json:"rootNodeId"`
	RouteSteps   []RouteStep             `json:"routeSteps"`
	Selections   []Selection             `json:"selections"`
	Presentation map[string]Presentation `json:"presentation"`
}

type Output struct {
	ID    string `json:"id"`
	Title string `json:"title"`
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

// Presentation contains display intent only. In particular, it has no
// selector, expression, physical collection, or generated-column fields.
type Presentation struct {
	Label   string `json:"label,omitempty"`
	Visible *bool  `json:"visible,omitempty"`
	Order   *int   `json:"order,omitempty"`
}

// CatalogSnapshot is an immutable, authorization-scoped projection. The
// token identifies the exact snapshot used to validate a document.
type CatalogSnapshot struct {
	APIVersion               string              `json:"apiVersion"`
	Kind                     string              `json:"kind"`
	Project                  string              `json:"project"`
	ExplorerID               string              `json:"explorerId"`
	SourceGeneration         string              `json:"sourceGeneration"`
	AuthorizationScopeDigest string              `json:"authorizationScopeDigest"`
	ResolvedSchemaDigest     string              `json:"resolvedSchemaDigest,omitempty"`
	SnapshotToken            string              `json:"snapshotToken"`
	Complete                 bool                `json:"complete"`
	Truncated                bool                `json:"truncated"`
	Diagnostics              []CatalogDiagnostic `json:"diagnostics"`
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
}

type CatalogCandidate struct {
	ID                    string   `json:"candidateId"`
	NodeID                string   `json:"nodeId"`
	Label                 string   `json:"label"`
	LogicalType           string   `json:"logicalType"`
	Filterable            bool     `json:"filterable"`
	Chartable             bool     `json:"chartable"`
	ProjectionModes       []string `json:"projectionModes"`
	DefaultProjectionMode string   `json:"defaultProjectionMode"`
	FilterOperators       []string `json:"filterOperators"`
	ChartOperations       []string `json:"chartOperations"`
	Cardinality           string   `json:"cardinality,omitempty"`
	Populated             bool     `json:"populated"`
	Count                 *int64   `json:"count,omitempty"`
	SuggestionsAvailable  bool     `json:"suggestionsAvailable"`
	SuggestionsComplete   bool     `json:"suggestionsComplete"`
	SuggestionsTruncated  bool     `json:"suggestionsTruncated"`
	SuggestionCount       int      `json:"suggestionCount"`
}

// RoutePolicy has no default hop ceiling: nil MaxHops means every finite
// route is valid. Repeated edges and self-loops are explicit capabilities.
type RoutePolicy struct {
	MaxHops            *int `json:"maxHops,omitempty"`
	Unbounded          bool `json:"unbounded"`
	AllowRepeatedEdges bool `json:"allowRepeatedEdges"`
	AllowSelfLoops     bool `json:"allowSelfLoops"`
}

// BuilderState joins one document to the one catalog snapshot that proves it.
type BuilderState struct {
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Document   *Document       `json:"document,omitempty"`
	Catalog    CatalogSnapshot `json:"catalog"`
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
	if d.APIVersion != APIVersion || d.Kind != Kind {
		return fmt.Errorf("unsupported V2 document protocol or kind")
	}
	if emptyID(d.Output.ID) || strings.TrimSpace(d.Output.Title) == "" {
		return fmt.Errorf("output id and title are required")
	}
	if emptyID(d.RootNodeID) {
		return fmt.Errorf("rootNodeId is required")
	}
	seenOccurrences := map[string]bool{RootOccurrenceID: true}
	for i, step := range d.RouteSteps {
		if emptyID(step.EdgeID) {
			return fmt.Errorf("routeSteps[%d].edgeId is required", i)
		}
		if step.OccurrenceID != "" {
			if step.OccurrenceID == RootOccurrenceID {
				return fmt.Errorf("routeSteps[%d] cannot author the derived base occurrence", i)
			}
			if emptyID(step.OccurrenceID) {
				return fmt.Errorf("routeSteps[%d].occurrenceId is invalid", i)
			}
			if seenOccurrences[step.OccurrenceID] {
				return fmt.Errorf("duplicate route occurrence id %q", step.OccurrenceID)
			}
			seenOccurrences[step.OccurrenceID] = true
		}
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
		key := selection.CandidateID + "\x00" + selection.OccurrenceID
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
	if s.Document == nil {
		return nil
	}
	if err := s.Document.Validate(); err != nil {
		return err
	}
	edges, candidates, nodes := s.Catalog.edgeIndex(), s.Catalog.candidateIndex(), s.Catalog.nodeIndex()
	if nodes[s.Document.RootNodeID].ID == "" {
		return fmt.Errorf("document rootNodeId %q is stale", s.Document.RootNodeID)
	}
	seenEdges := map[string]bool{}
	current := s.Document.RootNodeID
	for i, step := range s.Document.RouteSteps {
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
	if s.Catalog.RoutePolicy.MaxHops != nil && len(s.Document.RouteSteps) > *s.Catalog.RoutePolicy.MaxHops {
		return fmt.Errorf("route exceeds routePolicy.maxHops")
	}
	occ, err := s.Document.Occurrences(s.Catalog)
	if err != nil {
		return err
	}
	occByID := map[string]RouteOccurrence{}
	for _, item := range occ {
		occByID[item.ID] = item
	}
	for i, selection := range s.Document.Selections {
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
	return nil
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
	var document []byte
	var err error
	if s.Document == nil {
		document = []byte("null")
	} else {
		document, err = s.Document.CanonicalJSON()
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
		Document   json.RawMessage `json:"document"`
		Catalog    json.RawMessage `json:"catalog"`
	}{s.APIVersion, s.Kind, document, catalog})
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
	var out Document
	if err := strictDecode(raw, &out); err != nil {
		return out, err
	}
	if err := out.Validate(); err != nil {
		return out, err
	}
	return out, nil
}

func DecodeExplorerBuilderDocumentV2(raw []byte) (ExplorerBuilderDocumentV2, error) {
	return DecodeDocument(raw)
}
func DecodeCatalog(raw []byte) (CatalogSnapshot, error) {
	var out CatalogSnapshot
	if err := strictDecode(raw, &out); err != nil {
		return out, err
	}
	if err := out.Validate(); err != nil {
		return out, err
	}
	return out, nil
}

func DecodeExplorerBuilderCatalogV2(raw []byte) (ExplorerBuilderCatalogV2, error) {
	return DecodeCatalog(raw)
}
func DecodeBuilderState(raw []byte) (BuilderState, error) {
	var out BuilderState
	if err := strictDecode(raw, &out); err != nil {
		return out, err
	}
	if err := out.Validate(); err != nil {
		return out, err
	}
	return out, nil
}

func DecodeExplorerBuilderStateV2(raw []byte) (ExplorerBuilderStateV2, error) {
	return DecodeBuilderState(raw)
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
