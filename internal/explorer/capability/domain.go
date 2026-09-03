// Package capability contains the immutable, compiler-backed contract exposed
// by Explorer's traversal Builder.  It deliberately has no dependency on the
// database, generated schema, or HTTP layers: adapters provide observations and
// compiler proofs through the small interfaces in builder.go.
package capability

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type Status string

const (
	StatusBuilding Status = "BUILDING"
	StatusReady    Status = "READY"
	StatusFailed   Status = "FAILED"
)

type Operation string

const (
	OperationSelect    Operation = "SELECT"
	OperationFilter    Operation = "FILTER"
	OperationChart     Operation = "CHART"
	OperationSort      Operation = "SORT"
	OperationAggregate Operation = "AGGREGATE"
)

type ProjectionMode string

const (
	ProjectionScalar        ProjectionMode = "SCALAR"
	ProjectionFirst         ProjectionMode = "FIRST"
	ProjectionArray         ProjectionMode = "ARRAY"
	ProjectionDistinctArray ProjectionMode = "DISTINCT_ARRAY"
)

type FilterOperator string

const (
	FilterEquals FilterOperator = "EQUALS"
	FilterIn     FilterOperator = "IN"
	FilterExists FilterOperator = "EXISTS"
)

type ChartAggregation string

const (
	ChartCount ChartAggregation = "COUNT"
	ChartSum   ChartAggregation = "SUM"
	ChartMin   ChartAggregation = "MIN"
	ChartMax   ChartAggregation = "MAX"
	ChartAvg   ChartAggregation = "AVG"
)

// SnapshotIdentity contains every external identity that can change the
// meaning of a capability document.  Versions are intentionally independent:
// changing a renderer/compiler artifact must invalidate old Builder state even
// when the observed graph is unchanged.
type SnapshotIdentity struct {
	Project                  string `json:"project"`
	Generation               string `json:"generation"`
	AuthorizationScopeDigest string `json:"authorizationScopeDigest"`
	SchemaDigest             string `json:"schemaDigest"`
	ResourceInventoryDigest  string `json:"resourceInventoryDigest"`
	RelationshipDigest       string `json:"relationshipDigest"`
	FieldDigest              string `json:"fieldDigest"`
	ProtocolVersion          string `json:"protocolVersion"`
	CompilerVersion          string `json:"compilerVersion"`
	TraversalPolicyVersion   string `json:"traversalPolicyVersion"`
	ProjectionPolicyVersion  string `json:"projectionPolicyVersion"`
}

type RoutePolicy struct {
	Version             string `json:"version"`
	MaxHops             int    `json:"maxHops"` // zero means unlimited
	AllowsRepeatedEdges bool   `json:"allowsRepeatedEdges"`
	AllowsSelfLoops     bool   `json:"allowsSelfLoops"`
}

// Allows accepts every finite route (including zero-hop routes, repeated
// edges, and self-loops) unless the caller explicitly configured MaxHops.
// There is deliberately no implicit four-hop limit.
func (p RoutePolicy) Allows(route []string) bool {
	if p.MaxHops > 0 && len(route) > p.MaxHops {
		return false
	}
	for _, id := range route {
		if strings.TrimSpace(id) == "" {
			return false
		}
	}
	return true
}

type ProjectionPolicy struct {
	Version         string           `json:"version"`
	Modes           []ProjectionMode `json:"modes"`
	SuggestionLimit int              `json:"suggestionLimit"`
}

type Policy struct {
	Route      RoutePolicy      `json:"route"`
	Projection ProjectionPolicy `json:"projection"`
}

type Node struct {
	ID                  string      `json:"nodeId"`
	ResourceType        string      `json:"resourceType"`
	RowRootEligible     bool        `json:"rowRootEligible"`
	RowGrain            string      `json:"rowGrain,omitempty"`
	Populated           bool        `json:"populated"`
	DocumentCount       int64       `json:"documentCount,omitempty"`
	SupportedOperations []Operation `json:"supportedOperations"`
	BlockedReason       string      `json:"blockedReason,omitempty"`
	CapabilityVersion   string      `json:"capabilityVersion,omitempty"`
}

type Edge struct {
	ID                   string `json:"edgeId"`
	FromNodeID           string `json:"fromNodeId"`
	ToNodeID             string `json:"toNodeId"`
	Label                string `json:"label"`
	StorageDirection     string `json:"storageDirection,omitempty"`
	SourceResourceType   string `json:"sourceResourceType"`
	TargetResourceType   string `json:"targetResourceType"`
	ObservedEdgeCount    int64  `json:"observedEdgeCount,omitempty"`
	AllowsRepeatedTarget bool   `json:"allowsRepeatedTarget,omitempty"`
	BlockedReason        string `json:"blockedReason,omitempty"`
}

type Candidate struct {
	ID                    string             `json:"candidateId"`
	NodeID                string             `json:"nodeId"`
	ResourceType          string             `json:"resourceType"`
	FieldPath             string             `json:"fieldPath"`
	Label                 string             `json:"label"`
	LogicalType           string             `json:"logicalType"`
	Cardinality           string             `json:"cardinality,omitempty"`
	ProjectionModes       []ProjectionMode   `json:"projectionModes"`
	FilterOperators       []FilterOperator   `json:"filterOperators"`
	ChartAggregations     []ChartAggregation `json:"chartAggregations"`
	SupportedOperations   []Operation        `json:"supportedOperations"`
	Observed              bool               `json:"observed"`
	ObservedDocumentCount int64              `json:"observedDocumentCount,omitempty"`
	Populated             bool               `json:"populated"`
	SuggestedValues       []string           `json:"suggestedValues,omitempty"`
	SuggestionsComplete   bool               `json:"suggestionsComplete"`
	SuggestionsTruncated  bool               `json:"suggestionsTruncated"`
	BlockedReason         string             `json:"blockedReason,omitempty"`
}

type Diagnostic struct {
	Severity  string         `json:"severity"`
	Stage     string         `json:"stage,omitempty"`
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
	Retryable bool           `json:"retryable,omitempty"`
}

// Snapshot is immutable by convention: constructors and accessors copy all
// maps/slices, while Token is derived from a private canonical payload.  The
// exported fields keep the package ergonomic for JSON adapters; use Clone (or
// the accessors) when handing a snapshot to code that may mutate values.
type Snapshot struct {
	Identity        SnapshotIdentity `json:"identity"`
	Policy          Policy           `json:"policy"`
	Status          Status           `json:"status"`
	Complete        bool             `json:"complete"`
	Truncated       bool             `json:"truncated"`
	Nodes           []Node           `json:"nodes"`
	Edges           []Edge           `json:"edges"`
	Candidates      []Candidate      `json:"candidates"`
	Diagnostics     []Diagnostic     `json:"diagnostics"`
	AuditNodes      []Node           `json:"auditNodes,omitempty"`
	AuditEdges      []Edge           `json:"auditEdges,omitempty"`
	AuditCandidates []Candidate      `json:"auditCandidates,omitempty"`
	Token           string           `json:"token"`
}

var (
	ErrSnapshotUnavailable = errors.New("capability snapshot is unavailable")
	ErrStaleSnapshot       = errors.New("capability snapshot token is stale")
)

func (s Snapshot) Usable() bool   { return s.Status == StatusReady && s.Complete && !s.Truncated }
func (s Snapshot) IsUsable() bool { return s.Usable() }

func (s Snapshot) ValidateToken(token string) error {
	if token == "" || token != s.Token {
		return ErrStaleSnapshot
	}
	if !s.Usable() {
		return ErrSnapshotUnavailable
	}
	return nil
}

func (s Snapshot) Clone() Snapshot { return cloneSnapshot(s) }

func (s Snapshot) Node(id string) (Node, bool) {
	for _, n := range s.Nodes {
		if n.ID == id {
			return cloneNodes([]Node{n})[0], true
		}
	}
	return Node{}, false
}
func (s Snapshot) Edge(id string) (Edge, bool) {
	for _, e := range s.Edges {
		if e.ID == id {
			return e, true
		}
	}
	return Edge{}, false
}
func (s Snapshot) Candidate(id string) (Candidate, bool) {
	for _, c := range s.Candidates {
		if c.ID == id {
			return cloneCandidates([]Candidate{c})[0], true
		}
	}
	return Candidate{}, false
}

// CanonicalPayload returns a fresh copy of the exact payload hashed into Token.
func (s Snapshot) CanonicalPayload() []byte {
	c := cloneSnapshot(s)
	c.Token = ""
	raw, _ := json.Marshal(canonicalSnapshot(c))
	return raw
}

func canonicalSnapshot(s Snapshot) Snapshot {
	s.Nodes = append([]Node(nil), s.Nodes...)
	s.Edges = append([]Edge(nil), s.Edges...)
	s.Candidates = append([]Candidate(nil), s.Candidates...)
	s.AuditNodes = append([]Node(nil), s.AuditNodes...)
	s.AuditEdges = append([]Edge(nil), s.AuditEdges...)
	s.AuditCandidates = append([]Candidate(nil), s.AuditCandidates...)
	s.Diagnostics = append([]Diagnostic(nil), s.Diagnostics...)
	sort.Slice(s.Nodes, func(i, j int) bool { return s.Nodes[i].ID < s.Nodes[j].ID })
	sort.Slice(s.Edges, func(i, j int) bool { return s.Edges[i].ID < s.Edges[j].ID })
	sort.Slice(s.Candidates, func(i, j int) bool { return s.Candidates[i].ID < s.Candidates[j].ID })
	sort.Slice(s.AuditNodes, func(i, j int) bool { return s.AuditNodes[i].ID < s.AuditNodes[j].ID })
	sort.Slice(s.AuditEdges, func(i, j int) bool { return s.AuditEdges[i].ID < s.AuditEdges[j].ID })
	sort.Slice(s.AuditCandidates, func(i, j int) bool { return s.AuditCandidates[i].ID < s.AuditCandidates[j].ID })
	sort.Slice(s.Diagnostics, func(i, j int) bool {
		if s.Diagnostics[i].Code != s.Diagnostics[j].Code {
			return s.Diagnostics[i].Code < s.Diagnostics[j].Code
		}
		return s.Diagnostics[i].Message < s.Diagnostics[j].Message
	})
	sort.Slice(s.Policy.Projection.Modes, func(i, j int) bool { return s.Policy.Projection.Modes[i] < s.Policy.Projection.Modes[j] })
	normalizeNodeSlices(s.Nodes)
	normalizeCandidateSlices(s.Candidates)
	normalizeNodeSlices(s.AuditNodes)
	normalizeCandidateSlices(s.AuditCandidates)
	normalizeCandidateSlices(s.Candidates)
	normalizeCandidateValues(s.Candidates)
	normalizeCandidateValues(s.AuditCandidates)
	return s
}

func NewSnapshot(identity SnapshotIdentity, policy Policy, status Status, complete, truncated bool, nodes []Node, edges []Edge, candidates []Candidate, diagnostics []Diagnostic) Snapshot {
	s := Snapshot{Identity: identity, Policy: policy, Status: status, Complete: complete, Truncated: truncated, Nodes: nodes, Edges: edges, Candidates: candidates, Diagnostics: diagnostics}
	s = cloneSnapshot(s)
	payload := s.CanonicalPayload()
	sum := sha256.Sum256(payload)
	s.Token = "sha256:" + hex.EncodeToString(sum[:])
	return s
}

func normalizeNodeSlices(nodes []Node) {
	for i := range nodes {
		sort.Slice(nodes[i].SupportedOperations, func(a, b int) bool { return nodes[i].SupportedOperations[a] < nodes[i].SupportedOperations[b] })
	}
}
func normalizeCandidateSlices(cs []Candidate) {
	for i := range cs {
		sort.Slice(cs[i].ProjectionModes, func(a, b int) bool { return cs[i].ProjectionModes[a] < cs[i].ProjectionModes[b] })
		sort.Slice(cs[i].FilterOperators, func(a, b int) bool { return cs[i].FilterOperators[a] < cs[i].FilterOperators[b] })
		sort.Slice(cs[i].ChartAggregations, func(a, b int) bool { return cs[i].ChartAggregations[a] < cs[i].ChartAggregations[b] })
		sort.Slice(cs[i].SupportedOperations, func(a, b int) bool { return cs[i].SupportedOperations[a] < cs[i].SupportedOperations[b] })
	}
}
func normalizeCandidateValues(cs []Candidate) {
	for i := range cs {
		sort.Strings(cs[i].SuggestedValues)
	}
}

func cloneSnapshot(s Snapshot) Snapshot {
	s.Nodes = cloneNodes(s.Nodes)
	s.AuditNodes = cloneNodes(s.AuditNodes)
	s.Edges = append([]Edge(nil), s.Edges...)
	s.AuditEdges = append([]Edge(nil), s.AuditEdges...)
	s.Candidates = cloneCandidates(s.Candidates)
	s.AuditCandidates = cloneCandidates(s.AuditCandidates)
	s.Diagnostics = cloneDiagnostics(s.Diagnostics)
	s.Policy.Projection.Modes = append([]ProjectionMode(nil), s.Policy.Projection.Modes...)
	return s
}
func cloneNodes(v []Node) []Node {
	out := append([]Node(nil), v...)
	for i := range out {
		out[i].SupportedOperations = append([]Operation(nil), out[i].SupportedOperations...)
	}
	return out
}
func cloneCandidates(v []Candidate) []Candidate {
	out := append([]Candidate(nil), v...)
	for i := range out {
		out[i].ProjectionModes = append([]ProjectionMode(nil), out[i].ProjectionModes...)
		out[i].FilterOperators = append([]FilterOperator(nil), out[i].FilterOperators...)
		out[i].ChartAggregations = append([]ChartAggregation(nil), out[i].ChartAggregations...)
		out[i].SupportedOperations = append([]Operation(nil), out[i].SupportedOperations...)
		out[i].SuggestedValues = append([]string(nil), out[i].SuggestedValues...)
	}
	return out
}
func cloneDiagnostics(v []Diagnostic) []Diagnostic {
	out := append([]Diagnostic(nil), v...)
	for i := range out {
		if v[i].Details != nil {
			out[i].Details = map[string]any{}
			for k, val := range v[i].Details {
				out[i].Details[k] = cloneAny(val)
			}
		}
	}
	return out
}

func cloneAny(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, val := range x {
			m[k] = cloneAny(val)
		}
		return m
	case []any:
		a := make([]any, len(x))
		for i, val := range x {
			a[i] = cloneAny(val)
		}
		return a
	case []string:
		return append([]string(nil), x...)
	case map[string]string:
		return map[string]string(x)
	default:
		return v
	}
}

func digestID(prefix string, values ...string) string {
	h := sha256.New()
	for _, v := range values {
		fmt.Fprintf(h, "%d:%s|", len(v), v)
	}
	return prefix + hex.EncodeToString(h.Sum(nil))[:24]
}
