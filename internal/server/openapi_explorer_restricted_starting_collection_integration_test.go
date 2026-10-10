package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	catalogarango "github.com/calypr/loom/internal/catalog/arango"
	"github.com/calypr/loom/internal/explorer"
	explorerarango "github.com/calypr/loom/internal/explorer/arango"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	storearango "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

const restrictedStartingCollectionTestDatabasePrefix = "loom_test_restricted_starting_collection_"

func TestRestrictedStartingCollectionRejectsDeniedReferences(t *testing.T) {
	arangoURL := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	database := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_DATABASE"))
	if arangoURL == "" && database == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL and a randomized LOOM_TEST_ARANGO_DATABASE")
	}
	if arangoURL == "" || database == "" {
		t.Fatal("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must be set together")
	}
	suffix := strings.TrimPrefix(database, restrictedStartingCollectionTestDatabasePrefix)
	if suffix == database {
		t.Fatalf("refusing non-disposable Arango database %q; require %s<uuid>", database, restrictedStartingCollectionTestDatabasePrefix)
	}
	if _, err := uuid.Parse(suffix); err != nil {
		t.Fatalf("Arango database %q does not have the required randomized UUID suffix: %v", database, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := storearango.Open(ctx, arangoURL, database)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Bootstrap(ctx, storearango.BootstrapSpec{Collections: []storearango.CollectionSpec{
		{Name: "Patient"},
		{Name: "fhir_field_catalog"},
	}}); err != nil {
		t.Fatal(err)
	}

	const (
		project      = "project-a"
		resourceType = "Patient"
		allowedPath  = "programs/collection-repair/allowed"
		deniedPath   = "programs/collection-repair/denied"
	)
	generation := "restricted-starting-collection-" + suffix
	fixtureID := strings.ReplaceAll(uuid.NewString(), "-", "")
	allowedOne := "starting-allowed-one-" + fixtureID
	allowedTwo := "starting-allowed-two-" + fixtureID
	allowedThree := "starting-allowed-three-" + fixtureID
	deniedWitness := "starting-denied-witness-" + fixtureID
	resourceKeys := []string{"starting-" + fixtureID + "-1", "starting-" + fixtureID + "-2", "starting-" + fixtureID + "-3", "starting-" + fixtureID + "-4"}
	catalogKeys := []string{"starting-catalog-" + fixtureID + "-1", "starting-catalog-" + fixtureID + "-2"}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		for _, item := range []struct {
			collection string
			keys       []string
		}{{"Patient", resourceKeys}, {"fhir_field_catalog", catalogKeys}} {
			if err := client.ExecuteAQL(cleanupCtx, `
FOR d IN @@collection
  FILTER d._key IN @keys
  REMOVE d IN @@collection`, map[string]any{"@collection": item.collection, "keys": item.keys}); err != nil {
				t.Errorf("remove owned %s fixture rows: %v", item.collection, err)
			}
		}
	})

	resourceIDs := []string{allowedOne, allowedTwo, allowedThree, deniedWitness}
	resourcePaths := []string{allowedPath, allowedPath, allowedPath, deniedPath}
	resourceDocs := make([]json.RawMessage, 0, len(resourceIDs))
	for index, id := range resourceIDs {
		doc, err := json.Marshal(map[string]any{
			"_key": resourceKeys[index], "id": id, "project": project,
			"dataset_generation": generation, "auth_resource_path": resourcePaths[index],
		})
		if err != nil {
			t.Fatal(err)
		}
		resourceDocs = append(resourceDocs, doc)
	}
	catalogDocs := make([]json.RawMessage, 0, 2)
	for index, path := range []string{allowedPath, deniedPath} {
		doc, err := json.Marshal(map[string]any{
			"_key": catalogKeys[index], "project": project,
			"dataset_generation": generation, "auth_resource_path": path,
		})
		if err != nil {
			t.Fatal(err)
		}
		catalogDocs = append(catalogDocs, doc)
	}
	if err := client.InsertBatchRaw(ctx, "Patient", resourceDocs, false, "import"); err != nil {
		t.Fatal(err)
	}
	if err := client.InsertBatchRaw(ctx, "fhir_field_catalog", catalogDocs, false, "import"); err != nil {
		t.Fatal(err)
	}

	readIDs := func(path string) []string {
		t.Helper()
		ids := make([]string, 0, 4)
		err := client.QueryRows(ctx, `
FOR d IN Patient
  FILTER d.project == @project
  FILTER d.dataset_generation == @generation
  FILTER d.auth_resource_path == @path
  SORT d.id
  RETURN {id: d.id}`, 100, map[string]any{"project": project, "generation": generation, "path": path}, func(row map[string]any) error {
			id, ok := row["id"].(string)
			if !ok {
				return fmt.Errorf("source oracle returned non-string id %T", row["id"])
			}
			ids = append(ids, id)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return ids
	}
	wantAllowedIDs := sortedIDs([]string{allowedOne, allowedTwo, allowedThree})
	if got, want := readIDs(allowedPath), wantAllowedIDs; !reflect.DeepEqual(got, want) {
		t.Fatalf("independent allowed-path fixture=%v; want %v", got, want)
	}
	if got := readIDs(deniedPath); !reflect.DeepEqual(got, []string{deniedWitness}) {
		t.Fatalf("independent denied-path fixture=%v; want [%s]", got, deniedWitness)
	}

	catalogStore, err := catalogarango.New(client)
	if err != nil {
		t.Fatal(err)
	}
	explorerStore, err := explorerarango.New(client)
	if err != nil {
		t.Fatal(err)
	}
	resolver := authscope.NewScopeResolver(authscope.ScopeResolverConfig{
		ListExistingAuthResourcePaths: catalogStore.DiscoverExistingAuthResourcePaths,
	})
	principal, err := (authscope.StaticAuthenticator{Principal: authscope.Principal{
		Subject: "restricted-starting-collection-test",
		// The project grant authorizes Explorer metadata; only this resource
		// path is granted for the active generation.
		AuthResourcePaths: []string{project, allowedPath},
	}}).Authenticate(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	requestCtx := authscope.ContextWithPrincipal(ctx, principal)
	scope, err := resolver.ResolveReadScopeForGeneration(requestCtx, principal, project, generation, nil)
	if err != nil {
		t.Fatal(err)
	}
	if scope.Mode != authscope.ReadScopeRestricted || !reflect.DeepEqual(scope.AuthResourcePaths, []string{allowedPath}) {
		t.Fatalf("actual ScopeResolver returned %#v; want restricted scope [%s]", scope, allowedPath)
	}

	baseSnapshot := testAuthoringV2CapabilitySnapshot()
	identity := baseSnapshot.Identity
	identity.Project = project
	identity.Generation = generation
	identity.AuthorizationScopeDigest = explorerScopeDigest(scope)
	snapshot := capability.NewSnapshot(identity, baseSnapshot.Policy, baseSnapshot.Status, baseSnapshot.Complete, baseSnapshot.Truncated,
		baseSnapshot.Nodes, baseSnapshot.Edges, baseSnapshot.Candidates, baseSnapshot.Diagnostics)
	resolveCapability := func(ctx context.Context, requestedProject, token string) (lifecycle.AuthorizedCapability, error) {
		currentPrincipal, _ := authscope.PrincipalFromContext(ctx)
		currentScope, err := resolver.ResolveReadScopeForGeneration(ctx, currentPrincipal, requestedProject, generation, nil)
		if err != nil {
			return lifecycle.AuthorizedCapability{}, err
		}
		if token != snapshot.Token || explorerScopeDigest(currentScope) != snapshot.Identity.AuthorizationScopeDigest {
			return lifecycle.AuthorizedCapability{}, capability.ErrStaleSnapshot
		}
		return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: currentScope}, nil
	}

	proposalStore := &populationProposalHTTPStore{
		testExplorerStore: newTestExplorerStore(),
		selections:        map[string]explorer.SelectionRevision{},
		members:           map[string][]explorer.SelectionMember{},
	}
	domain, err := explorer.NewService(proposalStore)
	if err != nil {
		t.Fatal(err)
	}
	config := lifecycle.Config{
		SelectionMembersCollection: "loom_explorer_selection_members",
		Capability: lifecycle.CapabilityResolver{
			Current: func(context.Context, string, string, string) (capability.Snapshot, error) {
				return snapshot, nil
			},
			ForCompilation: resolveCapability,
			ForExecution:   resolveCapability,
		},
		SelectionReferenceValidator: explorerStore.ValidateSelectionReferences,
	}
	handlers := newExplorerHTTPHandlers(authscope.ScopeAuthorizer{Resolver: resolver}, resolver.AuthorizeReadProject, domain, config)

	toWireRef := func(id string) loomapi.SelectionResourceRef {
		return loomapi.SelectionResourceRef{Project: project, Generation: generation, ResourceType: resourceType, Id: id}
	}
	selectionRequest := func(key string, ids ...string) loomapi.CreateExplorerSelectionRequestObject {
		refs := make([]loomapi.SelectionResourceRef, 0, len(ids))
		for _, id := range ids {
			refs = append(refs, toWireRef(id))
		}
		path := loomapi.AuthResourcePath(allowedPath)
		resourceTypeValue := resourceType
		return loomapi.CreateExplorerSelectionRequestObject{
			Project: loomapi.Project(project), ExplorerId: loomapi.ExplorerId("restricted-starting-collection"),
			Params: loomapi.CreateExplorerSelectionParams{AuthResourcePath: &path},
			Body: &loomapi.SelectionCreateRequest{
				IdempotencyKey: key, SnapshotToken: snapshot.Token,
				Source: loomapi.SelectionRequestSource{
					Kind:      loomapi.SelectionRequestSourceKindResources,
					Resources: &loomapi.SelectionExplicitResources{ResourceType: &resourceTypeValue, Refs: refs},
				},
			},
		}
	}

	deniedWrite := selectionRequest("denied-write-path", allowedOne)
	deniedPathParam := loomapi.AuthResourcePath(deniedPath)
	deniedWrite.Params.AuthResourcePath = &deniedPathParam
	if _, err := handlers.createSelectionDirect(requestCtx, deniedWrite); !errors.Is(err, authscope.ErrForbidden) {
		t.Fatalf("write under denied auth_resource_path err=%v; want actual ScopeAuthorizer denial", err)
	}

	deniedRefRequest := selectionRequest("denied-reference-witness", allowedOne, deniedWitness)
	if _, err := handlers.createSelectionDirect(requestCtx, deniedRefRequest); !errors.Is(err, explorer.ErrResourceRefScopeMismatch) {
		t.Fatalf("allowed-path create containing denied witness err=%v; want real Arango scope-reference rejection", err)
	}
	proposalStore.selectionMu.Lock()
	partialSelectionCount := len(proposalStore.selections)
	proposalStore.selectionMu.Unlock()
	if partialSelectionCount != 0 {
		t.Fatalf("denied create persisted %d selection headers before rejection", partialSelectionCount)
	}

	created, err := handlers.createSelectionDirect(requestCtx, selectionRequest("allowed-starting-collection", allowedOne, allowedTwo, allowedThree))
	if err != nil {
		t.Fatalf("create allowed-only starting collection: %v", err)
	}
	if created.MemberCount != int64(len(wantAllowedIDs)) || created.ScopeDigest != snapshot.Identity.AuthorizationScopeDigest {
		t.Fatalf("allowed selection header lost exact count/scope: %#v", created)
	}
	selectionPage, err := handlers.getSelectionDirect(requestCtx, loomapi.GetExplorerSelectionRequestObject{
		Project: loomapi.Project(project), ExplorerId: loomapi.ExplorerId("restricted-starting-collection"), SelectionRevision: created.Id,
	})
	if err != nil {
		t.Fatal(err)
	}
	visibleIDs := sortedIDs(selectionMemberIDs(selectionPage.Members))
	if !reflect.DeepEqual(visibleIDs, wantAllowedIDs) {
		t.Fatalf("visible starting collection=%v; want exact allowed rows %v", visibleIDs, wantAllowedIDs)
	}
	assertNoDeniedID(t, deniedWitness, visibleIDs)
}

func sortedIDs(ids []string) []string {
	result := append([]string(nil), ids...)
	sort.Strings(result)
	return result
}

func selectionMemberIDs(members []loomapi.SelectionMember) []string {
	ids := make([]string, 0, len(members))
	for _, member := range members {
		ids = append(ids, member.Ref.Id)
	}
	return ids
}

func assertNoDeniedID(t *testing.T, denied string, values []string) {
	t.Helper()
	for _, value := range values {
		if value == denied {
			t.Fatalf("denied witness %s leaked into %v", denied, values)
		}
	}
}
