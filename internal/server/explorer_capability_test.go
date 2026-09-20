package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/unit"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

type staticCapabilityEvidence struct {
	inventory     catalog.ResourceInventoryResult
	relationships catalog.RelationshipObservationResult
	fields        catalog.FieldEnrichmentResult
}

func (s staticCapabilityEvidence) DiscoverResourceInventory(context.Context, catalog.ResourceInventoryOptions) (catalog.ResourceInventoryResult, error) {
	return s.inventory, nil
}
func (s staticCapabilityEvidence) DiscoverRelationshipObservations(context.Context, catalog.RelationshipObservationOptions) (catalog.RelationshipObservationResult, error) {
	return s.relationships, nil
}
func (s staticCapabilityEvidence) DiscoverFieldEnrichment(context.Context, catalog.FieldEnrichmentOptions) (catalog.FieldEnrichmentResult, error) {
	return s.fields, nil
}

type staticActiveManifest struct{ manifest dataset.Manifest }

func (s staticActiveManifest) ResolveActiveManifest(context.Context, string) (dataset.Manifest, error) {
	return s.manifest, nil
}

type capabilityTestResourceAccess struct{ resources []string }

func (c capabilityTestResourceAccess) GetAllowedResources(context.Context, string, string, string) ([]string, error) {
	return append([]string(nil), c.resources...), nil
}

func testAuthorizedCapabilitySnapshot(t *testing.T, generation string, scope authscope.ReadScope) capability.Snapshot {
	t.Helper()
	return capability.NewSnapshot(
		capability.SnapshotIdentity{
			Project: "project-a", Generation: generation, AuthorizationScopeDigest: explorerScopeDigest(scope),
			SchemaDigest: strings.Repeat("a", 64), ResourceInventoryDigest: "inventory", RelationshipDigest: "relationships", FieldDigest: "fields",
			ProtocolVersion: explorerCapabilityProtocolVersion, CompilerVersion: explorerCapabilityCompilerVersion,
			TraversalPolicyVersion: explorerTraversalPolicyVersion, ProjectionPolicyVersion: explorerProjectionPolicyVersion,
		},
		capability.Policy{Route: capability.RoutePolicy{Version: explorerTraversalPolicyVersion, AllowsRepeatedEdges: true, AllowsSelfLoops: true}, Projection: capability.ProjectionPolicy{Version: explorerProjectionPolicyVersion}},
		capability.StatusReady, true, false,
		[]capability.Node{{ID: "n_patient", ResourceType: "Patient", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true, SupportedOperations: []capability.Operation{capability.OperationSelect}}},
		nil, nil, nil,
	)
}

func testCapabilityScopeResolver(paths []string) *authscope.ScopeResolver {
	return authscope.NewScopeResolver(authscope.ScopeResolverConfig{
		ResourceAccess: capabilityTestResourceAccess{resources: []string{"/programs/example/projects/allowed"}},
		ListExistingAuthResourcePaths: func(context.Context, catalog.AuthResourcePathOptions) ([]string, error) {
			return append([]string(nil), paths...), nil
		},
	})
}

func TestAuthoringV2CatalogExposesCandidateFieldPath(t *testing.T) {
	snapshot := capability.NewSnapshot(
		capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a"},
		capability.Policy{}, capability.StatusReady, true, false,
		[]capability.Node{{ID: "n_patient", ResourceType: "Patient", RowRootEligible: true}},
		nil,
		[]capability.Candidate{{
			ID: "c_patient_birth_date", NodeID: "n_patient", ResourceType: "Patient",
			FieldPath: "birthDate", Label: "Birth date", LogicalType: "date",
			Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
			AggregateOperations: []capability.AggregateOperationCapability{{
				Operation: capability.AggregateSum, RowContext: capability.AggregateRowsRecords, Supported: false,
				ReasonCode: "NUMERIC_INPUT_REQUIRED", Reason: "SUM requires an integer or decimal input",
			}},
		}},
		nil,
	)

	wire := authoringV2Catalog(snapshot, "default")
	if len(wire.Candidates) != 1 || wire.Candidates[0].FieldPath != "birthDate" {
		t.Fatalf("catalog candidates = %#v", wire.Candidates)
	}
	choice := wire.Candidates[0].ConstructionChoice
	if choice == nil || choice.ChoiceID == "" || len(choice.Options) != 1 || choice.Options[0].Form != capability.ConstructionChoiceValue || choice.Options[0].Decision != capability.ConstructionChoiceDefault {
		t.Fatalf("field construction choice = %#v", choice)
	}
	encoded, err := json.Marshal(wire.Candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &decoded); err != nil || len(decoded["constructionChoice"]) == 0 {
		t.Fatalf("construction choice missing from candidate JSON %s: %v", encoded, err)
	}
	var aggregateChoices []capability.AggregateOperationCapability
	if err := json.Unmarshal(decoded["aggregateOperations"], &aggregateChoices); err != nil || len(aggregateChoices) != 1 || aggregateChoices[0].ReasonCode != "NUMERIC_INPUT_REQUIRED" || aggregateChoices[0].RowContext != capability.AggregateRowsRecords {
		t.Fatalf("typed aggregate choices missing or inaccurate in candidate JSON %s: choices=%#v err=%v", encoded, aggregateChoices, err)
	}
	var generated loomapi.CatalogCandidate
	if err := json.Unmarshal(encoded, &generated); err != nil {
		t.Fatalf("generated candidate boundary rejected choice: %v", err)
	}
	fieldSource, err := generated.ConstructionChoice.Source.AsFieldChoiceSource()
	if err != nil || fieldSource.Kind != loomapi.FieldChoiceSourceKindFIELD || fieldSource.CandidateId != "c_patient_birth_date" || fieldSource.Path != "birthDate" {
		t.Fatalf("generated field choice source=%#v err=%v", fieldSource, err)
	}
}

func TestBuilderStateGeneratedContractPreservesCandidateCapabilities(t *testing.T) {
	state := authoringv2.BuilderState{
		APIVersion:     authoringv2.APIVersion,
		Kind:           authoringv2.StateKind,
		LifecycleState: "NEW",
		Catalog: authoringv2.CatalogSnapshot{
			SourceGeneration:         "generation-a",
			AuthorizationScopeDigest: "scope-a",
			SnapshotToken:            "snapshot-a",
			Complete:                 true,
			Nodes:                    []authoringv2.CatalogNode{},
			Edges:                    []authoringv2.CatalogEdge{},
			Candidates: []authoringv2.CatalogCandidate{{
				ID:                    "c_height_value",
				NodeID:                "n_observation",
				FieldPath:             "valueQuantity.value",
				Label:                 "Height",
				LogicalType:           "decimal",
				Cardinality:           "optional_one",
				Filterable:            true,
				Chartable:             true,
				ProjectionModes:       []string{"VALUE"},
				DefaultProjectionMode: "VALUE",
				AggregateOperations: []capability.AggregateOperationCapability{{
					Operation:  capability.AggregateSum,
					RowContext: capability.AggregateRowsRecords,
					Supported:  true,
				}},
				Transformations: authoringv2.AggregateTransformationCapabilities{
					Temporal: authoringv2.TemporalReductionCapabilities{
						Available: true,
						TimestampFields: []authoringv2.TemporalFieldChoice{{
							CandidateID:  "c_observation_issued",
							NodeID:       "n_observation",
							ResourceType: "Observation",
							FieldPath:    "issued",
							Label:        "Issued",
						}},
						AnchorFields: []authoringv2.TemporalFieldChoice{{
							CandidateID:  "c_patient_birth_date",
							NodeID:       "n_patient",
							ResourceType: "Patient",
							FieldPath:    "birthDate",
							Label:        "Birth date",
						}},
					},
					UnitNormalization: authoringv2.UnitNormalizationCapabilities{
						Available: true,
						Presets: []authoringv2.UnitNormalizationPresetCapability{{
							PolicyID:  "ucum-pressure",
							Version:   "1",
							Target:    unit.UnitIdentity{System: "http://unitsofmeasure.org", Code: "mm[Hg]"},
							Available: true,
						}},
					},
				},
			}},
			RoutePolicy: authoringv2.RoutePolicy{AllowRepeatedEdges: true, AllowSelfLoops: true},
		},
	}

	generated, err := directAuthoringJSON[loomapi.BuilderState](state)
	if err != nil {
		t.Fatalf("convert Builder state through generated contract: %v", err)
	}
	encoded, err := json.Marshal(generated)
	if err != nil {
		t.Fatalf("marshal generated Builder state: %v", err)
	}
	var got authoringv2.BuilderState
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("decode generated Builder state: %v", err)
	}
	if len(got.Catalog.Candidates) != 1 {
		t.Fatalf("generated Builder candidates = %#v, want one candidate", got.Catalog.Candidates)
	}
	candidate := got.Catalog.Candidates[0]
	if len(candidate.AggregateOperations) != 1 {
		t.Fatalf("generated aggregate operations = %#v, want one supported SUM for RECORDS", candidate.AggregateOperations)
	}
	aggregateOperation := candidate.AggregateOperations[0]
	if string(aggregateOperation.Operation) != "SUM" || string(aggregateOperation.RowContext) != "RECORDS" || !aggregateOperation.Supported {
		t.Fatalf("generated aggregate operations = %#v, want supported SUM for RECORDS", candidate.AggregateOperations)
	}
	temporal := candidate.Transformations.Temporal
	if !temporal.Available || len(temporal.TimestampFields) != 1 || len(temporal.AnchorFields) != 1 {
		t.Fatalf("generated temporal capabilities = %#v, want issued timestamp and birthDate anchor", temporal)
	}
	if got, want := temporal.TimestampFields[0], (authoringv2.TemporalFieldChoice{
		CandidateID:  "c_observation_issued",
		NodeID:       "n_observation",
		ResourceType: "Observation",
		FieldPath:    "issued",
		Label:        "Issued",
	}); got != want {
		t.Fatalf("generated temporal timestamp = %#v, want %#v", got, want)
	}
	if got, want := temporal.AnchorFields[0], (authoringv2.TemporalFieldChoice{
		CandidateID:  "c_patient_birth_date",
		NodeID:       "n_patient",
		ResourceType: "Patient",
		FieldPath:    "birthDate",
		Label:        "Birth date",
	}); got != want {
		t.Fatalf("generated temporal anchor = %#v, want %#v", got, want)
	}
	unitCapabilities := candidate.Transformations.UnitNormalization
	if !unitCapabilities.Available || len(unitCapabilities.Presets) != 1 {
		t.Fatalf("generated unit capabilities = %#v, want mm[Hg] target", unitCapabilities)
	}
	if got, want := unitCapabilities.Presets[0], (authoringv2.UnitNormalizationPresetCapability{
		PolicyID:  "ucum-pressure",
		Version:   "1",
		Target:    unit.UnitIdentity{System: "http://unitsofmeasure.org", Code: "mm[Hg]"},
		Available: true,
	}); got != want {
		t.Fatalf("generated unit preset = %#v, want %#v", got, want)
	}
}

func TestAuthoringV2CatalogPreservesDistinctArrayProjectionMode(t *testing.T) {
	snapshot := capability.NewSnapshot(
		capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a"},
		capability.Policy{}, capability.StatusReady, true, false,
		[]capability.Node{{ID: "n_patient", ResourceType: "Patient", RowRootEligible: true}},
		nil,
		[]capability.Candidate{{
			ID: "c_patient_name", NodeID: "n_patient", ResourceType: "Patient", FieldPath: "name[]", Label: "Patient name", LogicalType: "string", Cardinality: "many",
			ProjectionModes: []capability.ProjectionMode{capability.ProjectionArray, capability.ProjectionDistinctArray},
		}},
		nil,
	)

	wire := authoringV2Catalog(snapshot, "default")
	if len(wire.Candidates) != 1 || len(wire.Candidates[0].ProjectionModes) != 2 {
		t.Fatalf("catalog candidates = %#v", wire.Candidates)
	}
	if wire.Candidates[0].ProjectionModes[0] != "ALL" || wire.Candidates[0].ProjectionModes[1] != "DISTINCT" {
		t.Fatalf("projection modes = %#v, want [ALL DISTINCT]", wire.Candidates[0].ProjectionModes)
	}
	choice := wire.Candidates[0].ConstructionChoice
	if choice == nil || len(choice.Options) != 2 || choice.Options[0].Form != capability.ConstructionChoiceAll || choice.Options[0].Decision != capability.ConstructionChoiceDefault {
		t.Fatalf("repeated field construction choice = %#v", choice)
	}
}

func TestAuthoringV2CatalogOmitsCandidateWithoutExecutableConstructionChoice(t *testing.T) {
	snapshot := capability.NewSnapshot(
		capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a"},
		capability.Policy{}, capability.StatusReady, true, false,
		[]capability.Node{{ID: "n_patient", ResourceType: "Patient", RowRootEligible: true}},
		nil,
		[]capability.Candidate{{
			ID: "c_patient_name", NodeID: "n_patient", ResourceType: "Patient", FieldPath: "name[]", Label: "Patient name", LogicalType: "string", Cardinality: "many",
			ProjectionModes: []capability.ProjectionMode{capability.ProjectionIndexed},
		}},
		nil,
	)

	wire := authoringV2Catalog(snapshot, "default")
	if len(wire.Candidates) != 0 || len(wire.Diagnostics) != 1 || wire.Diagnostics[0].Code != "CONSTRUCTION_CHOICE_UNAVAILABLE" {
		t.Fatalf("catalog=%#v, want candidate omitted with diagnostic", wire)
	}
}

func TestExplorerCapabilityResolverAuthorizedCompilationRequiresActiveGeneration(t *testing.T) {
	manifest := testCapabilityManifest(t)
	store := newTestCapabilityStore()
	snapshot := testAuthorizedCapabilitySnapshot(t, "generation-a", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	if _, err := store.Put(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	resolver, err := newExplorerCapabilityResolver(staticCapabilityEvidence{}, nil, staticActiveManifest{manifest: manifest}, store)
	if err != nil {
		t.Fatal(err)
	}
	authorized, err := resolver.ResolveForCompilation(context.Background(), "project-a", snapshot.Token)
	if err != nil {
		t.Fatal(err)
	}
	if authorized.Snapshot.Token != snapshot.Token || authorized.Scope.Mode != authscope.ReadScopeUnrestricted {
		t.Fatalf("authorized compilation capability = %#v", authorized)
	}

	inactive := testAuthorizedCapabilitySnapshot(t, "generation-old", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	if _, err := store.Put(context.Background(), inactive); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolveForCompilation(context.Background(), "project-a", inactive.Token); !errors.Is(err, capability.ErrStaleSnapshot) {
		t.Fatalf("inactive compilation token error = %v, want stale snapshot", err)
	}
}

func TestExplorerCapabilityResolverAuthorizedExecutionRetainsInactiveGeneration(t *testing.T) {
	manifest := testCapabilityManifest(t)
	store := newTestCapabilityStore()
	snapshot := testAuthorizedCapabilitySnapshot(t, "generation-old", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	if _, err := store.Put(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	resolver, err := newExplorerCapabilityResolver(staticCapabilityEvidence{}, nil, staticActiveManifest{manifest: manifest}, store)
	if err != nil {
		t.Fatal(err)
	}
	authorized, err := resolver.ResolveForExecution(context.Background(), "project-a", snapshot.Token)
	if err != nil {
		t.Fatal(err)
	}
	if authorized.Snapshot.Identity.Generation != "generation-old" {
		t.Fatalf("execution generation = %q", authorized.Snapshot.Identity.Generation)
	}
}

func TestExplorerCapabilityResolverEnforcesProjectAuthorizationWithoutScopeResolver(t *testing.T) {
	manifest := testCapabilityManifest(t)
	store := newTestCapabilityStore()
	snapshot := testAuthorizedCapabilitySnapshot(t, "generation-a", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	if _, err := store.Put(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	resolver, err := newExplorerCapabilityResolver(staticCapabilityEvidence{}, nil, staticActiveManifest{manifest: manifest}, store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{Projects: []string{"another-project"}})
	if _, err := resolver.ResolveForExecution(ctx, "project-a", snapshot.Token); !errors.Is(err, authscope.ErrForbidden) {
		t.Fatalf("project authorization error = %v, want forbidden", err)
	}
}

func TestExplorerCapabilityResolverRejectsChangedScopeAndPreservesRestrictedEmpty(t *testing.T) {
	manifest := testCapabilityManifest(t)
	store := newTestCapabilityStore()
	unrestricted := testAuthorizedCapabilitySnapshot(t, "generation-a", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	if _, err := store.Put(context.Background(), unrestricted); err != nil {
		t.Fatal(err)
	}
	scopedResolver, err := newExplorerCapabilityResolver(staticCapabilityEvidence{}, testCapabilityScopeResolver([]string{"example-other"}), staticActiveManifest{manifest: manifest}, store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{AuthorizationHeader: "Bearer token"})
	if _, err := scopedResolver.ResolveForExecution(ctx, "project-a", unrestricted.Token); !errors.Is(err, capability.ErrStaleSnapshot) {
		t.Fatalf("changed scope error = %v, want stale snapshot", err)
	}

	emptyScope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted}
	empty := testAuthorizedCapabilitySnapshot(t, "generation-a", emptyScope)
	if _, err := store.Put(context.Background(), empty); err != nil {
		t.Fatal(err)
	}
	authorized, err := scopedResolver.ResolveForExecution(ctx, "project-a", empty.Token)
	if err != nil {
		t.Fatal(err)
	}
	if authorized.Scope.Unrestricted() || len(authorized.Scope.AuthResourcePaths) != 0 {
		t.Fatalf("restricted-empty scope = %#v", authorized.Scope)
	}
	if authorized.Scope.Mode != emptyScope.Mode {
		t.Fatalf("restricted-empty mode = %q, want %q", authorized.Scope.Mode, emptyScope.Mode)
	}
	// The returned scope is a defensive copy, not the resolver's retained
	// authorization result.
	authorized.Scope.AuthResourcePaths = append(authorized.Scope.AuthResourcePaths, "mutated")
	again, err := scopedResolver.ResolveForExecution(ctx, "project-a", empty.Token)
	if err != nil || len(again.Scope.AuthResourcePaths) != 0 || again.Scope.Unrestricted() {
		t.Fatalf("scope alias leaked after mutation: %#v, %v", again.Scope, err)
	}
}

func TestExplorerCapabilityResolverBuildsAndReusesCompilerProvenSnapshot(t *testing.T) {
	manifest := testCapabilityManifest(t)
	inventory := []catalog.ResourceInventoryObservation{{Project: "project-a", DatasetGeneration: "generation-a", ResourceType: "Patient", DocumentCount: 2}}
	fields := []catalog.FieldEnrichmentObservation{{Project: "project-a", DatasetGeneration: "generation-a", ResourceType: "Patient", Path: "id", Kind: "scalar", DocCount: 2}}
	inventoryDigest, _ := catalog.ResourceInventoryDigest(inventory)
	relationshipDigest, _ := catalog.RelationshipObservationDigest(nil)
	fieldDigest, _ := catalog.FieldEnrichmentDigest(fields)
	evidence := staticCapabilityEvidence{
		inventory:     catalog.ResourceInventoryResult{Values: inventory, Available: true, Complete: true, Status: catalog.EvidenceAvailable, Digest: inventoryDigest},
		relationships: catalog.RelationshipObservationResult{Values: []catalog.RelationshipObservation{}, Available: true, Complete: true, Status: catalog.EvidenceEmpty, Digest: relationshipDigest},
		fields:        catalog.FieldEnrichmentResult{Values: fields, Available: true, Complete: true, Status: catalog.EvidenceAvailable, Digest: fieldDigest},
	}
	repository := newTestCapabilityStore()
	resolver, err := newExplorerCapabilityResolver(evidence, nil, staticActiveManifest{manifest: manifest}, repository)
	if err != nil {
		t.Fatal(err)
	}
	first, err := resolver.Resolve(context.Background(), "project-a", "")
	if err != nil {
		t.Fatal(err)
	}
	if !first.Usable() || len(first.Nodes) != 1 || first.Nodes[0].ResourceType != "Patient" || len(first.Candidates) != 1 || first.Candidates[0].FieldPath != "id" {
		t.Fatalf("snapshot=%#v", first)
	}
	if first.Identity.ResourceInventoryDigest != inventoryDigest || first.Identity.FieldDigest != fieldDigest {
		t.Fatalf("identity=%#v", first.Identity)
	}
	second, err := resolver.Resolve(context.Background(), "project-a", "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	if second.Token != first.Token {
		t.Fatalf("immutable snapshot was rebuilt with a different token: %q != %q", second.Token, first.Token)
	}
	loaded, err := resolver.ResolveToken(context.Background(), "project-a", first.Token)
	if err != nil || loaded.Token != first.Token {
		t.Fatalf("ResolveToken() = %#v, %v", loaded, err)
	}
}

func TestExplorerCapabilityResolverFailsClosedOnIncompleteFieldEnrichment(t *testing.T) {
	manifest := testCapabilityManifest(t)
	inventory := []catalog.ResourceInventoryObservation{{Project: "project-a", DatasetGeneration: "generation-a", ResourceType: "Patient", DocumentCount: 1}}
	digest, _ := catalog.ResourceInventoryDigest(inventory)
	evidence := staticCapabilityEvidence{
		inventory:     catalog.ResourceInventoryResult{Values: inventory, Available: true, Complete: true, Status: catalog.EvidenceAvailable, Digest: digest},
		relationships: catalog.RelationshipObservationResult{Values: []catalog.RelationshipObservation{}, Available: true, Complete: true, Status: catalog.EvidenceEmpty, Digest: "sha256:relationships"},
		fields:        catalog.FieldEnrichmentResult{Values: []catalog.FieldEnrichmentObservation{}, Available: true, Complete: false, Status: catalog.EvidenceIncomplete, Digest: "sha256:fields"},
	}
	resolver, err := newExplorerCapabilityResolver(evidence, nil, staticActiveManifest{manifest: manifest}, newTestCapabilityStore())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(context.Background(), "project-a", ""); err == nil || !strings.Contains(err.Error(), "field enrichment") {
		t.Fatalf("Resolve() error=%v", err)
	}
}

func TestCapabilityEvidencePublishesBothStoredRelationshipDirections(t *testing.T) {
	evidence := capabilityEvidenceFromCatalog(catalog.CapabilityEvidence{
		Relationships: catalog.RelationshipObservationResult{Values: []catalog.RelationshipObservation{{
			Label: "subject_Patient", EdgeCount: 7,
			StorageFromType: "Specimen", StorageToType: "Patient", StorageDirection: "OUTBOUND",
			BuilderFromType: "Patient", BuilderToType: "Specimen", BuilderDirection: "INBOUND",
		}}},
	})
	got := map[string]capability.RelationshipObservation{}
	for _, relationship := range evidence.Relationships {
		got[relationship.SourceResourceType+"->"+relationship.TargetResourceType] = relationship
	}
	if outbound := got["Specimen->Patient"]; outbound.StorageDirection != "OUTBOUND" || outbound.ObservedEdgeCount != 7 {
		t.Fatalf("outbound relationship = %#v", outbound)
	}
	if inbound := got["Patient->Specimen"]; inbound.StorageDirection != "INBOUND" || inbound.ObservedEdgeCount != 7 {
		t.Fatalf("inbound relationship = %#v", inbound)
	}
}

func testCapabilityManifest(t *testing.T) dataset.Manifest {
	t.Helper()
	ref, err := dataset.NewRef("project-a", "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	schema, err := dataset.NewSchemaSnapshot("urn:test", "R5", strings.Repeat("a", 64), []string{"Patient"})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := dataset.NewManifest(ref, schema)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err = manifest.Transition(dataset.StateStaged)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func testAuthoringV2CapabilitySnapshot() capability.Snapshot {
	return capability.NewSnapshot(
		capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: explorerScopeDigest(authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}), SchemaDigest: strings.Repeat("a", 64), ResourceInventoryDigest: "inventory", RelationshipDigest: "relationships", FieldDigest: "fields", ShapeDigest: strings.Repeat("b", 64), ProtocolVersion: explorerCapabilityProtocolVersion, CompilerVersion: explorerCapabilityCompilerVersion, TraversalPolicyVersion: explorerTraversalPolicyVersion, ProjectionPolicyVersion: explorerProjectionPolicyVersion},
		capability.Policy{Route: capability.RoutePolicy{Version: explorerTraversalPolicyVersion, AllowsRepeatedEdges: true, AllowsSelfLoops: true}, Projection: capability.ProjectionPolicy{Version: explorerProjectionPolicyVersion}},
		capability.StatusReady, true, false,
		[]capability.Node{{ID: "n_patient", ResourceType: "Patient", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true, DocumentCount: 1, SupportedOperations: []capability.Operation{capability.OperationSelect}}},
		nil,
		[]capability.Candidate{{ID: "c_patient_id", NodeID: "n_patient", ResourceType: "Patient", FieldPath: "id", Label: "ID", LogicalType: "string", Cardinality: "OPTIONAL_ONE", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar, capability.ProjectionFirst}, SupportedOperations: []capability.Operation{capability.OperationSelect}, Observed: true, Populated: true, SuggestedValues: []string{"patient-1"}, SuggestionsComplete: true}},
		nil,
	)
}
