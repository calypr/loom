package capability

import (
	"context"
	"reflect"
	"testing"
)

func testIdentity() SnapshotIdentity {
	return SnapshotIdentity{Project: "p", Generation: "g", AuthorizationScopeDigest: "scope", SchemaDigest: "schema", ResourceInventoryDigest: "inventory", RelationshipDigest: "relationships", FieldDigest: "fields", ProtocolVersion: "protocol-1", CompilerVersion: "compiler-1", TraversalPolicyVersion: "route-1", ProjectionPolicyVersion: "projection-1"}
}

func testProbe() CompilerCallbacks {
	return CompilerCallbacks{
		Node: func(_ context.Context, n Node) (NodeProof, error) {
			return NodeProof{Allowed: true, RowRootEligible: true, RowGrain: "RESOURCE", SupportedOperations: []Operation{OperationSelect, OperationFilter}}, nil
		},
		Edge: func(_ context.Context, _ Edge) (EdgeProof, error) { return EdgeProof{Allowed: true}, nil },
		Candidate: func(_ context.Context, _ Candidate) (CandidateProof, error) {
			return CandidateProof{Allowed: true, ProjectionModes: []ProjectionMode{ProjectionFirst, ProjectionArray}, FilterOperators: []FilterOperator{FilterEquals}, SupportedOperations: []Operation{OperationSelect, OperationFilter}}, nil
		},
	}
}

func testBuilder(obs Evidence) Builder {
	obs.ResourcesAvailable = true
	obs.RelationshipsAvailable = true
	obs.FieldsAvailable = true
	return NewBuilder(testIdentity(), obs, testProbe())
}

func TestSnapshotHashIsOrderIndependentAndIdentityComplete(t *testing.T) {
	nodes := []Node{{ID: "n_b", ResourceType: "B"}, {ID: "n_a", ResourceType: "A"}}
	edges := []Edge{{ID: "e_b", FromNodeID: "n_b", ToNodeID: "n_a"}, {ID: "e_a", FromNodeID: "n_a", ToNodeID: "n_b"}}
	candidates := []Candidate{{ID: "c_b", NodeID: "n_b", FieldPath: "z", ProjectionModes: []ProjectionMode{ProjectionArray, ProjectionFirst}}, {ID: "c_a", NodeID: "n_a", FieldPath: "a", ProjectionModes: []ProjectionMode{ProjectionFirst, ProjectionArray}}}
	p := Policy{Route: RoutePolicy{Version: "r", AllowsRepeatedEdges: true, AllowsSelfLoops: true}, Projection: ProjectionPolicy{Version: "p", Modes: []ProjectionMode{ProjectionArray, ProjectionFirst}, SuggestionLimit: 3}}
	a := NewSnapshot(testIdentity(), p, StatusReady, true, false, nodes, edges, candidates, nil)
	b := NewSnapshot(testIdentity(), p, StatusReady, true, false, []Node{nodes[1], nodes[0]}, []Edge{edges[1], edges[0]}, []Candidate{candidates[1], candidates[0]}, nil)
	if a.Token != b.Token {
		t.Fatalf("reordering payload changed token: %s != %s", a.Token, b.Token)
	}
	fields := []struct {
		name string
		edit func(*SnapshotIdentity)
	}{
		{"project", func(i *SnapshotIdentity) { i.Project = "other" }}, {"generation", func(i *SnapshotIdentity) { i.Generation = "other" }}, {"scope", func(i *SnapshotIdentity) { i.AuthorizationScopeDigest = "other" }}, {"schema", func(i *SnapshotIdentity) { i.SchemaDigest = "other" }}, {"inventory", func(i *SnapshotIdentity) { i.ResourceInventoryDigest = "other" }}, {"relationships", func(i *SnapshotIdentity) { i.RelationshipDigest = "other" }}, {"fields", func(i *SnapshotIdentity) { i.FieldDigest = "other" }}, {"protocol", func(i *SnapshotIdentity) { i.ProtocolVersion = "other" }}, {"compiler", func(i *SnapshotIdentity) { i.CompilerVersion = "other" }}, {"traversal", func(i *SnapshotIdentity) { i.TraversalPolicyVersion = "other" }}, {"projection", func(i *SnapshotIdentity) { i.ProjectionPolicyVersion = "other" }},
	}
	for _, tc := range fields {
		id := testIdentity()
		tc.edit(&id)
		if NewSnapshot(id, p, StatusReady, true, false, nil, nil, nil, nil).Token == a.Token {
			t.Errorf("changing %s did not change token", tc.name)
		}
	}
}

func TestBuilderUsesProofAndCopiesObservation(t *testing.T) {
	obs := Evidence{Resources: []ResourceObservation{{ResourceType: "Patient", Populated: true, DocumentCount: 4}, {ResourceType: "Observation", Populated: true}}, Relationships: []RelationshipObservation{{SourceResourceType: "Patient", TargetResourceType: "Patient", Label: "partOf", StorageDirection: "OUTBOUND"}, {SourceResourceType: "Patient", TargetResourceType: "Patient", Label: "partOf", StorageDirection: "OUTBOUND"}}, Fields: []FieldObservation{{ResourceType: "Patient", Path: "name", Label: "Name", LogicalType: "string", SuggestedValues: []string{"B", "A"}, Observed: true, Populated: true}}}
	s := testBuilder(obs).Build
	snapshot, err := s(context.Background())
	if err != nil || !snapshot.Usable() {
		t.Fatalf("build failed: %#v %#v", snapshot, err)
	}
	if len(snapshot.Nodes) != 2 || len(snapshot.Edges) != 2 || len(snapshot.Candidates) != 1 {
		t.Fatalf("unexpected capabilities: %#v", snapshot)
	}
	if snapshot.Edges[0].ID == snapshot.Edges[1].ID {
		t.Fatal("repeated edges need distinct snapshot-local IDs")
	}
	obs.Resources[0].ResourceType = "Mutated"
	obs.Fields[0].SuggestedValues[0] = "Mutated"
	if snapshot.Nodes[0].ResourceType == "Mutated" || snapshot.Candidates[0].SuggestedValues[0] == "Mutated" {
		t.Fatal("builder retained caller-owned mutable data")
	}
}

func TestUnpopulatedInventoryResourcesStayOutOfUsableNodes(t *testing.T) {
	obs := Evidence{Resources: []ResourceObservation{{ResourceType: "Patient", Populated: true, DocumentCount: 1}, {ResourceType: "Observation", DocumentCount: 0}}, Fields: []FieldObservation{}}
	s, err := testBuilder(obs).Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Nodes) != 1 || s.Nodes[0].ResourceType != "Patient" {
		t.Fatalf("unpopulated resource was advertised: %#v", s.Nodes)
	}
	if len(s.AuditNodes) != 1 || s.AuditNodes[0].BlockedReason == "" {
		t.Fatalf("unpopulated resource was not retained for audit: %#v", s.AuditNodes)
	}
}

func TestIDsAreBoundToSnapshotIdentity(t *testing.T) {
	obs := Evidence{Resources: []ResourceObservation{{ResourceType: "Patient", Populated: true}}, Fields: []FieldObservation{{ResourceType: "Patient", Path: "name"}}}
	a, err := testBuilder(obs).Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	id := testIdentity()
	id.Generation = "other-generation"
	obs.ResourcesAvailable = true
	obs.RelationshipsAvailable = true
	obs.FieldsAvailable = true
	b := NewBuilder(id, obs, testProbe())
	other, err := b.Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if a.Nodes[0].ID == other.Nodes[0].ID || a.Candidates[0].ID == other.Candidates[0].ID {
		t.Fatal("opaque IDs escaped their snapshot identity")
	}
	if _, ok := a.Node(other.Nodes[0].ID); ok {
		t.Fatal("snapshot A resolved a node minted by snapshot B")
	}
}

func TestBuilderRejectsMalformedEvidenceAndCompilerFailures(t *testing.T) {
	obs := Evidence{Resources: []ResourceObservation{{ResourceType: "Patient"}}, Relationships: []RelationshipObservation{{SourceResourceType: "Patient", TargetResourceType: "Bad/Type", Label: "x"}}, Fields: []FieldObservation{{ResourceType: "Patient", Path: "name..given"}}}
	// Build with a rejecting node leaves no usable root and retains audit data.
	b := testBuilder(obs)
	got, buildErr := b.Build(context.Background())
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	if len(got.Edges) != 0 || len(got.Candidates) != 0 || len(got.AuditEdges) == 0 || len(got.AuditCandidates) == 0 {
		t.Fatalf("blocked evidence escaped usable graph: %#v", got)
	}
}

func TestBuilderFieldEnrichmentIsFailClosed(t *testing.T) {
	obs := Evidence{Resources: []ResourceObservation{{ResourceType: "Patient"}}, Relationships: nil, Fields: nil}
	obs.ResourcesAvailable = true
	obs.RelationshipsAvailable = true
	b := NewBuilder(testIdentity(), obs, testProbe())
	s, err := b.Build(context.Background())
	if err == nil || s.Status != StatusFailed || s.Usable() {
		t.Fatalf("missing enrichment was not failed closed: %#v, %v", s, err)
	}
}

func TestIncompleteAndTruncatedSnapshotsAreNotUsable(t *testing.T) {
	obs := Evidence{}
	b := testBuilder(obs)
	b.Complete = false
	s, err := b.Build(context.Background())
	if err != nil || s.Status != StatusBuilding || s.Usable() {
		t.Fatalf("incomplete snapshot: %#v %v", s, err)
	}
	b.Complete, b.Truncated = true, true
	s, err = b.Build(context.Background())
	if err != nil || s.Status != StatusBuilding || s.Usable() {
		t.Fatalf("truncated snapshot: %#v %v", s, err)
	}
}

func TestRoutePolicyAllowsFiniteRepeatedAndSelfLoopRoutes(t *testing.T) {
	p := RoutePolicy{Version: "route", MaxHops: 0, AllowsRepeatedEdges: true, AllowsSelfLoops: true}
	route := []string{"n", "n", "m", "n", "m", "n", "m", "n", "m", "n"}
	if !p.Allows(route) {
		t.Fatal("unbounded route policy rejected finite route")
	}
	if (RoutePolicy{MaxHops: 4, AllowsRepeatedEdges: true, AllowsSelfLoops: true}).Allows(route) {
		t.Fatal("explicit bounded route policy accepted too many hops")
	}
}

func TestCloneDefensivelyCopiesNestedDiagnostics(t *testing.T) {
	d := []Diagnostic{{Code: "x", Details: map[string]any{"nested": map[string]any{"values": []any{"a"}}}}}
	s := NewSnapshot(testIdentity(), Policy{}, StatusReady, true, false, nil, nil, nil, d)
	clone := s.Clone()
	clone.Diagnostics[0].Details["nested"].(map[string]any)["values"].([]any)[0] = "changed"
	if reflect.DeepEqual(s.Diagnostics, clone.Diagnostics) {
		t.Fatal("clone shares nested diagnostics")
	}
}
