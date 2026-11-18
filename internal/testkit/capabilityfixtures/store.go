package capabilityfixtures

import (
	"context"
	"strings"

	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer/capability"
)

type Store struct {
	byToken    map[string]capability.Snapshot
	byIdentity map[capability.SnapshotIdentity]string
}

func NewStore() *Store {
	return &Store{byToken: make(map[string]capability.Snapshot), byIdentity: make(map[capability.SnapshotIdentity]string)}
}

func (s *Store) Put(_ context.Context, snapshot capability.Snapshot) (*capability.Snapshot, error) {
	stored := snapshot.Clone()
	s.byToken[stored.Token] = stored
	s.byIdentity[stored.Identity] = stored.Token
	result := stored.Clone()
	return &result, nil
}

func (s *Store) GetByToken(_ context.Context, token string) (*capability.Snapshot, error) {
	stored, ok := s.byToken[token]
	if !ok {
		return nil, capability.ErrNotFound
	}
	result := stored.Clone()
	return &result, nil
}

func (s *Store) GetByIdentity(_ context.Context, identity capability.SnapshotIdentity) (*capability.Snapshot, error) {
	token, ok := s.byIdentity[identity]
	if !ok {
		return nil, capability.ErrNotFound
	}
	return s.GetByToken(context.Background(), token)
}

func (s *Store) Reset() {
	s.byToken = make(map[string]capability.Snapshot)
	s.byIdentity = make(map[capability.SnapshotIdentity]string)
}

func (s *Store) DeleteIdentity(identity capability.SnapshotIdentity) {
	delete(s.byIdentity, identity)
}

type StaticEvidence struct {
	Inventory     catalog.ResourceInventoryResult
	Relationships catalog.RelationshipObservationResult
	Fields        catalog.FieldEnrichmentResult
}

func (s StaticEvidence) DiscoverResourceInventory(context.Context, catalog.ResourceInventoryOptions) (catalog.ResourceInventoryResult, error) {
	return s.Inventory, nil
}

func (s StaticEvidence) DiscoverRelationshipObservations(context.Context, catalog.RelationshipObservationOptions) (catalog.RelationshipObservationResult, error) {
	return s.Relationships, nil
}

func (s StaticEvidence) DiscoverFieldEnrichment(context.Context, catalog.FieldEnrichmentOptions) (catalog.FieldEnrichmentResult, error) {
	return s.Fields, nil
}

type StaticActiveManifest struct {
	Manifest dataset.Manifest
}

func (s StaticActiveManifest) ResolveActiveManifest(context.Context, string) (dataset.Manifest, error) {
	return s.Manifest, nil
}

func AuthorizedCapabilitySnapshot(generation, scopeDigest string) capability.Snapshot {
	return capability.NewSnapshot(
		capability.SnapshotIdentity{Project: "project-a", Generation: generation, AuthorizationScopeDigest: scopeDigest, SchemaDigest: strings.Repeat("a", 64), ResourceInventoryDigest: "inventory", RelationshipDigest: "relationships", FieldDigest: "fields", ProtocolVersion: "loom.calypr.org/explorer-authoring/v2", CompilerVersion: "loom-dataframe-compiler-v4", TraversalPolicyVersion: "finite-unbounded-v1", ProjectionPolicyVersion: "compiler-probed-v2"},
		capability.Policy{Route: capability.RoutePolicy{Version: "finite-unbounded-v1", AllowsRepeatedEdges: true, AllowsSelfLoops: true}, Projection: capability.ProjectionPolicy{Version: "compiler-probed-v2"}},
		capability.StatusReady, true, false,
		[]capability.Node{{ID: "n_patient", ResourceType: "Patient", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true, SupportedOperations: []capability.Operation{capability.OperationSelect}}},
		nil, nil, nil,
	)
}

func AuthoringV2CapabilitySnapshot(scopeDigest string) capability.Snapshot {
	return capability.NewSnapshot(
		capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: scopeDigest, SchemaDigest: strings.Repeat("a", 64), ResourceInventoryDigest: "inventory", RelationshipDigest: "relationships", FieldDigest: "fields", ShapeDigest: strings.Repeat("b", 64), ProtocolVersion: "loom.calypr.org/explorer-authoring/v2", CompilerVersion: "loom-dataframe-compiler-v4", TraversalPolicyVersion: "finite-unbounded-v1", ProjectionPolicyVersion: "compiler-probed-v2"},
		capability.Policy{Route: capability.RoutePolicy{Version: "finite-unbounded-v1", AllowsRepeatedEdges: true, AllowsSelfLoops: true}, Projection: capability.ProjectionPolicy{Version: "compiler-probed-v2"}},
		capability.StatusReady, true, false,
		[]capability.Node{{ID: "n_patient", ResourceType: "Patient", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true, DocumentCount: 1, SupportedOperations: []capability.Operation{capability.OperationSelect}}},
		nil,
		[]capability.Candidate{{ID: "c_patient_id", NodeID: "n_patient", ResourceType: "Patient", FieldPath: "id", Label: "ID", LogicalType: "string", Cardinality: "OPTIONAL_ONE", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar, capability.ProjectionFirst}, SupportedOperations: []capability.Operation{capability.OperationSelect}, Observed: true, Populated: true, SuggestedValues: []string{"patient-1"}, SuggestionsComplete: true}},
		nil,
	)
}
