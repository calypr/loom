package capability

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// The observation interfaces intentionally speak only in evidence. They do
// not expose generated schema or database handles, so an observer can never
// turn an accidental profiler row into an executable capability on its own.
type ResourceObservation struct {
	ResourceType  string
	Populated     bool
	DocumentCount int64
}

type RelationshipObservation struct {
	SourceResourceType   string
	TargetResourceType   string
	Label                string
	StorageDirection     string
	ObservedEdgeCount    int64
	AllowsRepeatedTarget bool
}

type FieldObservation struct {
	ResourceType          string
	Path                  string
	Label                 string
	LogicalType           string
	Cardinality           string
	Observed              bool
	ObservedDocumentCount int64
	Populated             bool
	SuggestedValues       []string
	SuggestionsComplete   bool
	SuggestionsTruncated  bool
}

type ResourceInventory interface {
	ListResources(context.Context) ([]ResourceObservation, error)
}
type RelationshipEvidence interface {
	ListRelationships(context.Context) ([]RelationshipObservation, error)
}
type FieldEvidence interface {
	ListFields(context.Context) ([]FieldObservation, error)
}

// Observer is a convenience for adapters that naturally collect all three
// evidence classes together. Builder accepts the individual interfaces too.
type Observer interface {
	ResourceInventory
	RelationshipEvidence
	FieldEvidence
}

type NodeProof struct {
	Allowed             bool
	RowRootEligible     bool
	RowGrain            string
	SupportedOperations []Operation
	Reason              string
}

type EdgeProof struct {
	Allowed bool
	Reason  string
}

type CandidateProof struct {
	Allowed             bool
	LogicalType         string
	Cardinality         string
	ProjectionModes     []ProjectionMode
	FilterOperators     []FilterOperator
	ChartAggregations   []ChartAggregation
	SupportedOperations []Operation
	Reason              string
}

// CompilerProbe is the compiler's proof boundary. A Builder advertises an
// operation only after the corresponding probe succeeds. In particular, a
// field is not trusted merely because the observation source reported it.
type CompilerProbe interface {
	ProbeNode(context.Context, Node) (NodeProof, error)
	ProbeEdge(context.Context, Edge) (EdgeProof, error)
	ProbeCandidate(context.Context, Candidate) (CandidateProof, error)
}

type Builder struct {
	Identity  SnapshotIdentity
	Policy    Policy
	Complete  bool
	Truncated bool

	Resources     ResourceInventory
	Relationships RelationshipEvidence
	Fields        FieldEvidence
	Compiler      CompilerProbe
}

const DefaultSuggestionLimit = 32

func NewBuilder(identity SnapshotIdentity, resources ResourceInventory, relationships RelationshipEvidence, fields FieldEvidence, compiler CompilerProbe) Builder {
	return Builder{Identity: identity, Resources: resources, Relationships: relationships, Fields: fields, Compiler: compiler, Complete: true, Policy: Policy{Route: RoutePolicy{AllowsRepeatedEdges: true, AllowsSelfLoops: true}, Projection: ProjectionPolicy{SuggestionLimit: DefaultSuggestionLimit}}}
}

// Build is pure: all values returned by an adapter are copied before they are
// sorted or used, and no adapter or compiler state is modified.
func (b Builder) Build(ctx context.Context) (Snapshot, error) {
	identity := b.Identity
	identity.Project = strings.TrimSpace(identity.Project)
	identity.Generation = strings.TrimSpace(identity.Generation)
	if identity.Project == "" || identity.Generation == "" {
		return b.failed(identity, "INVALID_SNAPSHOT_IDENTITY", "project and generation are required"), fmt.Errorf("project and generation are required")
	}
	if b.Complete == false && !b.Truncated {
		// Explicitly incomplete discovery is a BUILDING snapshot, not an empty
		// READY graph. The caller may publish it after a later complete build.
	}
	diags := []Diagnostic{}
	if b.Resources == nil || b.Relationships == nil || b.Fields == nil || b.Compiler == nil {
		return b.failedWith(identity, diags, "MISSING_CAPABILITY_DEPENDENCY", "resource, relationship, field, and compiler interfaces are required"), fmt.Errorf("capability dependencies are incomplete")
	}
	resources, err := b.Resources.ListResources(ctx)
	if err != nil {
		return b.failedWith(identity, diags, "RESOURCE_INVENTORY_FAILED", err.Error()), err
	}
	relationships, err := b.Relationships.ListRelationships(ctx)
	if err != nil {
		return b.failedWith(identity, diags, "RELATIONSHIP_EVIDENCE_FAILED", err.Error()), err
	}
	// Field enrichment is intentionally mandatory and fetched even when no
	// relationship evidence exists. An omitted enrichment source must fail
	// closed rather than silently yielding a field-less usable catalog.
	fields, err := b.Fields.ListFields(ctx)
	if err != nil {
		return b.failedWith(identity, diags, "FIELD_ENRICHMENT_FAILED", err.Error()), err
	}

	policy := b.Policy
	if policy.Projection.Version == "" {
		policy.Projection.Version = identity.ProjectionPolicyVersion
	}
	if policy.Route.Version == "" {
		policy.Route.Version = identity.TraversalPolicyVersion
	}
	if policy.Route.MaxHops < 0 {
		return b.failedWith(identity, diags, "INVALID_ROUTE_POLICY", "route MaxHops cannot be negative"), fmt.Errorf("route MaxHops cannot be negative")
	}
	// Zero means unlimited. Defaults explicitly advertise the two traversal
	// forms that the policy permits rather than relying on omitted JSON fields.
	if !policy.Route.AllowsRepeatedEdges && !policy.Route.AllowsSelfLoops {
		policy.Route.AllowsRepeatedEdges, policy.Route.AllowsSelfLoops = true, true
	}
	if policy.Projection.SuggestionLimit == 0 {
		policy.Projection.SuggestionLimit = DefaultSuggestionLimit
	}
	identity.TraversalPolicyVersion = first(identity.TraversalPolicyVersion, policy.Route.Version)
	identity.ProjectionPolicyVersion = first(identity.ProjectionPolicyVersion, policy.Projection.Version)
	idSeed := identityDigest(identity)

	// A resource type is a concrete generated FHIR type, not a collection or
	// arbitrary path. Deduplicate only exact resource observations; their
	// populatedness remains an annotation of the single capability.
	resourceMap := map[string]ResourceObservation{}
	for _, raw := range resources {
		t := strings.TrimSpace(raw.ResourceType)
		if !validResourceType(t) {
			diags = append(diags, diag("INVALID_RESOURCE_TYPE", "resource type is not a concrete generated type", t))
			continue
		}
		if prior, ok := resourceMap[t]; ok {
			prior.Populated = prior.Populated || raw.Populated
			if raw.DocumentCount > prior.DocumentCount {
				prior.DocumentCount = raw.DocumentCount
			}
			resourceMap[t] = prior
		} else {
			raw.ResourceType = t
			resourceMap[t] = raw
		}
	}
	types := make([]string, 0, len(resourceMap))
	for t := range resourceMap {
		types = append(types, t)
	}
	sort.Strings(types)
	nodes := []Node{}
	auditNodes := []Node{}
	nodeByType := map[string]Node{}
	for _, t := range types {
		o := resourceMap[t]
		n := Node{ID: digestID("n_", idSeed, t), ResourceType: t, Populated: o.Populated, DocumentCount: o.DocumentCount}
		if !o.Populated && o.DocumentCount <= 0 {
			n.BlockedReason = "resource collection is not populated"
			auditNodes = append(auditNodes, n)
			diags = append(diags, diag("NOT_POPULATED", "resource inventory did not observe any rows", t))
			continue
		}
		proof, e := b.Compiler.ProbeNode(ctx, n)
		if e != nil || !proof.Allowed || !proof.RowRootEligible || len(proof.SupportedOperations) == 0 {
			reason := proof.Reason
			if e != nil {
				reason = e.Error()
			}
			if reason == "" {
				reason = "compiler did not prove a supported row root"
			}
			n.BlockedReason = reason
			auditNodes = append(auditNodes, n)
			diags = append(diags, diag("NODE_NOT_PROVEN", "resource observation failed compiler proof", t))
			continue
		}
		n.RowRootEligible = proof.RowRootEligible
		n.RowGrain = proof.RowGrain
		n.SupportedOperations = append([]Operation(nil), proof.SupportedOperations...)
		n.CapabilityVersion = identity.CompilerVersion
		nodes = append(nodes, n)
		nodeByType[t] = n
	}

	// Sort relationships by their complete identity before assigning ordinals;
	// this makes repeated edges deterministic and preserves self-loops.
	sort.SliceStable(relationships, func(i, j int) bool {
		return relationshipSortKey(relationships[i]) < relationshipSortKey(relationships[j])
	})
	edges := []Edge{}
	auditEdges := []Edge{}
	edgeOrd := map[string]int{}
	for _, r := range relationships {
		src, dst := strings.TrimSpace(r.SourceResourceType), strings.TrimSpace(r.TargetResourceType)
		base := relationshipKey(RelationshipObservation{SourceResourceType: src, TargetResourceType: dst, Label: strings.TrimSpace(r.Label), StorageDirection: strings.TrimSpace(r.StorageDirection)})
		ord := edgeOrd[base]
		edgeOrd[base] = ord + 1
		e := Edge{ID: digestID("e_", idSeed, base, fmt.Sprint(ord)), FromNodeID: digestID("n_", idSeed, src), ToNodeID: digestID("n_", idSeed, dst), Label: strings.TrimSpace(r.Label), StorageDirection: strings.TrimSpace(r.StorageDirection), SourceResourceType: src, TargetResourceType: dst, ObservedEdgeCount: r.ObservedEdgeCount, AllowsRepeatedTarget: r.AllowsRepeatedTarget}
		if !validResourceType(src) || !validResourceType(dst) || e.Label == "" || nodeByType[src].ID == "" || nodeByType[dst].ID == "" {
			e.BlockedReason = "relationship endpoints or label are not a usable generated traversal"
			auditEdges = append(auditEdges, e)
			diags = append(diags, diag("INVALID_RELATIONSHIP", "relationship endpoint or label is invalid or unproven", base))
			continue
		}
		proof, eProbe := b.Compiler.ProbeEdge(ctx, e)
		if eProbe != nil || !proof.Allowed {
			reason := proof.Reason
			if eProbe != nil {
				reason = eProbe.Error()
			}
			if reason == "" {
				reason = "compiler did not prove traversal"
			}
			e.BlockedReason = reason
			auditEdges = append(auditEdges, e)
			diags = append(diags, diag("EDGE_NOT_PROVEN", "relationship observation failed compiler proof", base))
			continue
		}
		edges = append(edges, e)
	}

	// Candidate IDs are local to this snapshot and occurrence-stable even when
	// evidence repeats the same path. Paths are validated before any probe.
	sort.SliceStable(fields, func(i, j int) bool { return fieldSortKey(fields[i]) < fieldSortKey(fields[j]) })
	candidates := []Candidate{}
	auditCandidates := []Candidate{}
	candOrd := map[string]int{}
	for _, f := range fields {
		t, path := strings.TrimSpace(f.ResourceType), canonicalPath(f.Path)
		base := t + "\x00" + path
		ord := candOrd[base]
		candOrd[base] = ord + 1
		c := Candidate{ID: digestID("c_", idSeed, base, fmt.Sprint(ord)), NodeID: digestID("n_", idSeed, t), ResourceType: t, FieldPath: path, Label: first(strings.TrimSpace(f.Label), path), LogicalType: f.LogicalType, Cardinality: f.Cardinality, Observed: f.Observed, ObservedDocumentCount: f.ObservedDocumentCount, Populated: f.Populated}
		c.SuggestedValues = append([]string(nil), f.SuggestedValues...)
		c.SuggestionsComplete = f.SuggestionsComplete
		c.SuggestionsTruncated = f.SuggestionsTruncated
		sort.Strings(c.SuggestedValues)
		if policy.Projection.SuggestionLimit > 0 && len(c.SuggestedValues) > policy.Projection.SuggestionLimit {
			c.SuggestedValues = c.SuggestedValues[:policy.Projection.SuggestionLimit]
			c.SuggestionsTruncated = true
			c.SuggestionsComplete = false
		}
		if !validResourceType(t) || path == "" || nodeByType[t].ID == "" {
			c.BlockedReason = "field path or owning resource is not usable"
			auditCandidates = append(auditCandidates, c)
			diags = append(diags, diag("INVALID_FIELD_PATH", "field evidence did not resolve to a usable node/path", base))
			continue
		}
		proof, eProbe := b.Compiler.ProbeCandidate(ctx, c)
		if eProbe != nil || !proof.Allowed || len(proof.ProjectionModes) == 0 || len(proof.SupportedOperations) == 0 {
			reason := proof.Reason
			if eProbe != nil {
				reason = eProbe.Error()
			}
			if reason == "" {
				reason = "compiler did not prove field projection"
			}
			c.BlockedReason = reason
			auditCandidates = append(auditCandidates, c)
			diags = append(diags, diag("CANDIDATE_NOT_PROVEN", "field observation failed compiler proof", base))
			continue
		}
		c.LogicalType = first(proof.LogicalType, c.LogicalType)
		c.Cardinality = first(proof.Cardinality, c.Cardinality)
		c.ProjectionModes = append([]ProjectionMode(nil), proof.ProjectionModes...)
		c.FilterOperators = append([]FilterOperator(nil), proof.FilterOperators...)
		c.ChartAggregations = append([]ChartAggregation(nil), proof.ChartAggregations...)
		c.SupportedOperations = append([]Operation(nil), proof.SupportedOperations...)
		candidates = append(candidates, c)
	}
	status := StatusReady
	if !b.Complete || b.Truncated {
		status = StatusBuilding
	}
	if len(diags) > 0 && (b.Fields == nil) {
		status = StatusFailed
	}
	s := NewSnapshot(identity, policy, status, b.Complete, b.Truncated, nodes, edges, candidates, diags)
	s.AuditNodes = auditNodes
	s.AuditEdges = auditEdges
	s.AuditCandidates = auditCandidates
	s = rehash(s)
	return s, nil
}

func (b Builder) failed(identity SnapshotIdentity, code, message string) Snapshot {
	return b.failedWith(identity, nil, code, message)
}
func (b Builder) failedWith(identity SnapshotIdentity, ds []Diagnostic, code, message string) Snapshot {
	ds = append(ds, diag(code, message, ""))
	return NewSnapshot(identity, b.Policy, StatusFailed, false, b.Truncated, nil, nil, nil, ds)
}
func rehash(s Snapshot) Snapshot {
	s.Token = ""
	p := s.CanonicalPayload()
	sum := sha256sum(p)
	s.Token = "sha256:" + sum
	return s
}
func identityDigest(identity SnapshotIdentity) string {
	raw, _ := json.Marshal(identity)
	return sha256sum(raw)
}
func sha256sum(p []byte) string { h := sha256.New(); h.Write(p); return fmt.Sprintf("%x", h.Sum(nil)) }
func diag(code, message, detail string) Diagnostic {
	d := Diagnostic{Severity: "ERROR", Stage: "capability", Code: code, Message: message}
	if detail != "" {
		d.Details = map[string]any{"value": detail}
	}
	return d
}
func first(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
func relationshipKey(r RelationshipObservation) string {
	return strings.TrimSpace(r.SourceResourceType) + "\x00" + strings.TrimSpace(r.TargetResourceType) + "\x00" + strings.TrimSpace(r.Label) + "\x00" + strings.TrimSpace(r.StorageDirection)
}
func relationshipSortKey(r RelationshipObservation) string {
	return relationshipKey(r) + fmt.Sprintf("\x00%d\x00%d", r.ObservedEdgeCount, boolInt(r.AllowsRepeatedTarget))
}
func fieldKey(f FieldObservation) string {
	return strings.TrimSpace(f.ResourceType) + "\x00" + canonicalPath(f.Path) + "\x00" + strings.TrimSpace(f.Label) + "\x00" + f.LogicalType
}
func fieldSortKey(f FieldObservation) string {
	values := append([]string(nil), f.SuggestedValues...)
	sort.Strings(values)
	return fieldKey(f) + "\x00" + f.Cardinality + fmt.Sprintf("\x00%d\x00%d\x00%d\x00%d\x00%d\x00%s", f.ObservedDocumentCount, boolInt(f.Observed), boolInt(f.Populated), boolInt(f.SuggestionsComplete), boolInt(f.SuggestionsTruncated), strings.Join(values, "\x01"))
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

var resourceTypePattern = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
var pathSegmentPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*(\[\])?$`)

func validResourceType(s string) bool { return resourceTypePattern.MatchString(strings.TrimSpace(s)) }
func canonicalPath(raw string) string {
	p := strings.TrimSpace(raw)
	if p == "" || strings.HasPrefix(p, ".") || strings.HasSuffix(p, ".") || strings.Contains(p, "..") {
		return ""
	}
	segs := strings.Split(p, ".")
	for i := range segs {
		segs[i] = strings.TrimSpace(segs[i])
		if !pathSegmentPattern.MatchString(segs[i]) {
			return ""
		}
	}
	return strings.Join(segs, ".")
}
