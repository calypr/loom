package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/capabilitystore"
	"github.com/gofiber/fiber/v3"
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

func TestExplorerCapabilityResolverAuthorizedCompilationRequiresActiveGeneration(t *testing.T) {
	manifest := testCapabilityManifest(t)
	store := capabilitystore.NewMemoryStore()
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
	store := capabilitystore.NewMemoryStore()
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
	store := capabilitystore.NewMemoryStore()
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
	store := capabilitystore.NewMemoryStore()
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
	repository := capabilitystore.NewMemoryStore()
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
	resolver, err := newExplorerCapabilityResolver(evidence, nil, staticActiveManifest{manifest: manifest}, capabilitystore.NewMemoryStore())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(context.Background(), "project-a", ""); err == nil || !strings.Contains(err.Error(), "field enrichment") {
		t.Fatalf("Resolve() error=%v", err)
	}
}

func TestExplorerAuthoringV2HardCutCompilesAndPreviewsReceipt(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	store := explorer.NewMemoryStore()
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateEmptyInteractive(context.Background(), "project-a", "custom", "Custom", "test"); err != nil {
		t.Fatal(err)
	}
	legacy := legacyCatalogSnapshot(snapshot)
	compile := func(ctx context.Context, request ExplorerAuthoringV1CompileRequest) (ExplorerAuthoringV1CompileResult, error) {
		return ResolveAuthoringBundle(ctx, nil, func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) { return legacy, nil }, request)
	}
	app := fiber.New()
	RegisterExplorerAuthoringV2Routes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, ExplorerV2LifecycleConfig{
		Capability: func(context.Context, string, string, string) (capability.Snapshot, error) { return snapshot, nil },
		CapabilityToken: func(_ context.Context, _ string, token string) (capability.Snapshot, error) {
			if err := snapshot.ValidateToken(token); err != nil {
				return capability.Snapshot{}, err
			}
			return snapshot, nil
		},
		AuthorizedCapabilityExecution: func(_ context.Context, _, token string) (AuthorizedCapability, error) {
			if err := snapshot.ValidateToken(token); err != nil {
				return AuthorizedCapability{}, err
			}
			return AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
		},
		AuthoringCompile: compile,
		Preview: func(_ context.Context, _ recipe.Bundle, _ recipe.RuntimeBindings) (map[string][]map[string]any, error) {
			return map[string][]map[string]any{"patients": {{"id": "patient-1"}}}, nil
		},
	})

	get := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/custom/authoring/v2/builder", "")
	if get.StatusCode != http.StatusOK {
		t.Fatalf("GET builder status=%d body=%s", get.StatusCode, get.Body)
	}
	legacyResponse := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/custom/authoring/v1/builder", "")
	if legacyResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("V1 Builder remains registered: status=%d", legacyResponse.StatusCode)
	}
	suggestions := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/custom/authoring/v2/capabilities/"+snapshot.Token+"/candidates/c_patient_id/suggestions", "")
	if suggestions.StatusCode != http.StatusOK || !strings.Contains(suggestions.Body, `"patient-1"`) {
		t.Fatalf("candidate suggestions status=%d body=%s", suggestions.StatusCode, suggestions.Body)
	}
	body := `{"workspace":{"apiVersion":"` + authoringv2.APIVersion + `","kind":"` + authoringv2.WorkspaceKind + `","documents":[{"kind":"` + authoringv2.Kind + `","output":{"id":"patients","title":"Patients"},"rootNodeId":"n_patient","routeSteps":[],"selections":[{"candidateId":"c_patient_id","occurrenceId":"base","projectionMode":"FIRST"}],"presentation":{}}],"tabs":[{"id":"patients-tab","title":"Patients","outputId":"patients","order":0,"visible":true}]},"snapshotToken":"` + snapshot.Token + `"}`
	compiled := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/builder", body)
	if compiled.StatusCode != http.StatusOK {
		t.Fatalf("POST builder status=%d body=%s", compiled.StatusCode, compiled.Body)
	}
	var result struct {
		ReceiptID string `json:"receiptId"`
	}
	if err := json.Unmarshal([]byte(compiled.Body), &result); err != nil {
		t.Fatal(err)
	}
	if result.ReceiptID == "" {
		t.Fatal("compile result omitted receiptId")
	}
	receipt, err := service.CompilationReceipt(context.Background(), result.ReceiptID)
	if err != nil {
		t.Fatal(err)
	}
	if got := receipt.Bundle.Outputs[0].Fields[0].ValueMode; got != recipe.ValueModeFirst {
		t.Fatalf("V2 projection mode was not carried into the executable recipe: %q", got)
	}
	preview := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/preview", `{"receiptId":"`+result.ReceiptID+`","outputId":"patients","limit":5}`)
	if preview.StatusCode != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", preview.StatusCode, preview.Body)
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
		capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: explorerScopeDigest(authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}), SchemaDigest: strings.Repeat("a", 64), ResourceInventoryDigest: "inventory", RelationshipDigest: "relationships", FieldDigest: "fields", ProtocolVersion: explorerCapabilityProtocolVersion, CompilerVersion: explorerCapabilityCompilerVersion, TraversalPolicyVersion: explorerTraversalPolicyVersion, ProjectionPolicyVersion: explorerProjectionPolicyVersion},
		capability.Policy{Route: capability.RoutePolicy{Version: explorerTraversalPolicyVersion, AllowsRepeatedEdges: true, AllowsSelfLoops: true}, Projection: capability.ProjectionPolicy{Version: explorerProjectionPolicyVersion}},
		capability.StatusReady, true, false,
		[]capability.Node{{ID: "n_patient", ResourceType: "Patient", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true, DocumentCount: 1, SupportedOperations: []capability.Operation{capability.OperationSelect}}},
		nil,
		[]capability.Candidate{{ID: "c_patient_id", NodeID: "n_patient", ResourceType: "Patient", FieldPath: "id", Label: "ID", LogicalType: "string", Cardinality: "OPTIONAL_ONE", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar, capability.ProjectionFirst}, SupportedOperations: []capability.Operation{capability.OperationSelect}, Observed: true, Populated: true, SuggestedValues: []string{"patient-1"}, SuggestionsComplete: true}},
		nil,
	)
}
