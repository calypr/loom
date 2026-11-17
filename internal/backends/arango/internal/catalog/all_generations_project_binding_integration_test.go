package arango

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	storearango "github.com/calypr/loom/internal/backends/arango/internal/store"
	"github.com/calypr/loom/internal/catalog"
	"github.com/google/uuid"
)

const genBindTestDatabasePrefix = "loom_catalog_genbind_"

func openGenBindHarness(t *testing.T, budget time.Duration) (context.Context, *storearango.Client) {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	if endpoint == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL to run the all-generations project-binding Arango proof")
	}
	database := genBindTestDatabasePrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	if !strings.HasPrefix(database, genBindTestDatabasePrefix) {
		t.Fatalf("verification database %q escapes the %s prefix", database, genBindTestDatabasePrefix)
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	t.Cleanup(cancel)
	client, err := storearango.Open(ctx, endpoint, database)
	if err != nil {
		t.Fatalf("open owned database %s: %v", database, err)
	}
	t.Logf("GENBIND_DATABASE=%s", database)
	t.Cleanup(func() {
		_ = client.Close(context.Background())
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		request, err := http.NewRequestWithContext(cleanupCtx, http.MethodDelete, strings.TrimRight(endpoint, "/")+"/_api/database/"+url.PathEscape(database), nil)
		if err != nil {
			t.Errorf("build cleanup request for %s: %v", database, err)
			return
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Errorf("cleanup %s: %v", database, err)
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusNotFound {
			t.Errorf("cleanup %s: HTTP %d", database, response.StatusCode)
		}
	})
	if err := client.Bootstrap(ctx, storearango.BootstrapSpec{Collections: []storearango.CollectionSpec{
		{Name: "fhir_field_catalog"},
	}}); err != nil {
		t.Fatalf("bootstrap fhir_field_catalog: %v", err)
	}
	return ctx, client
}

func insertGenBindCatalogRow(t *testing.T, ctx context.Context, client *storearango.Client, key, project string, datasetGeneration *string, authResourcePath string) {
	t.Helper()
	row := map[string]any{"_key": key, "project": project, "auth_resource_path": authResourcePath}
	if datasetGeneration != nil {
		row["dataset_generation"] = *datasetGeneration
	}
	encoded, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("encode catalog fixture %s: %v", key, err)
	}
	if err := client.InsertBatchRaw(ctx, "fhir_field_catalog", []json.RawMessage{encoded}, false, "import"); err != nil {
		t.Fatalf("insert catalog fixture %s: %v", key, err)
	}
}

func genBindDiscoveryErrorShape(t *testing.T, err error, label string) {
	t.Helper()
	if err == nil {
		return
	}
	if strings.Contains(err.Error(), "was not declared in the query") {
		t.Fatalf("%s: undeclared bind parameter returned from Arango (pre-fix F-B27 regression): %v", label, err)
	}
}

func TestDiscoverExistingAuthResourcePathsAllGenerationsIsCrossGenerationUnionAgainstArango(t *testing.T) {
	ctx, client := openGenBindHarness(t, 60*time.Second)
	store, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	uid := strings.ReplaceAll(uuid.NewString(), "-", "")
	project := "genbind-project-" + uid
	otherProject := "genbind-other-project-" + uid
	alpha, beta := "gen-alpha-"+uid, "gen-beta-"+uid

	insertGenBindCatalogRow(t, ctx, client, "genbind-a1", project, &alpha, "grants/alpha")
	insertGenBindCatalogRow(t, ctx, client, "genbind-b1", project, &beta, "grants/beta")
	insertGenBindCatalogRow(t, ctx, client, "genbind-n1", project, nil, "grants/none")
	insertGenBindCatalogRow(t, ctx, client, "genbind-e1", project, &alpha, "")
	insertGenBindCatalogRow(t, ctx, client, "genbind-x1", otherProject, &alpha, "grants/cross-project")

	union, err := store.DiscoverExistingAuthResourcePaths(ctx, catalog.AuthResourcePathOptions{Project: project, AllGenerations: true})
	genBindDiscoveryErrorShape(t, err, "AllGenerations discovery")
	if err != nil {
		t.Fatalf("AllGenerations discovery: %v", err)
	}
	if want := []string{"grants/alpha", "grants/beta", "grants/none"}; !reflect.DeepEqual(union, want) {
		t.Fatalf("AllGenerations union=%v; want %v (cross-generation union must include gen-only rows and drop the empty path)", union, want)
	}
	for _, path := range union {
		if path == "grants/cross-project" {
			t.Fatalf("AllGenerations discovery leaked another project's path: %v", union)
		}
	}

	scoped, err := store.DiscoverExistingAuthResourcePaths(ctx, catalog.AuthResourcePathOptions{Project: project, DatasetGeneration: alpha})
	if err != nil {
		t.Fatalf("scoped discovery: %v", err)
	}
	if want := []string{"grants/alpha"}; !reflect.DeepEqual(scoped, want) {
		t.Fatalf("scoped discovery (generation %s)=%v; want %v (generation-scoped query must keep binding @dataset_generation)", alpha, scoped, want)
	}

	noGeneration, err := store.DiscoverExistingAuthResourcePaths(ctx, catalog.AuthResourcePathOptions{Project: project})
	if err != nil {
		t.Fatalf("generation-less discovery: %v", err)
	}
	if want := []string{"grants/none"}; !reflect.DeepEqual(noGeneration, want) {
		t.Fatalf("generation-less discovery=%v; want %v (empty generation must stay 'row has no generation')", noGeneration, want)
	}

	if _, err := store.DiscoverExistingAuthResourcePaths(ctx, catalog.AuthResourcePathOptions{Project: project, AllGenerations: true, DatasetGeneration: alpha}); !errors.Is(err, ErrAllGenerationsWithGeneration) {
		t.Fatalf("AllGenerations with selected generation err=%v; want %v", err, ErrAllGenerationsWithGeneration)
	}
}

func TestAuthorizeWriteProjectBindingAgainstArangoRecordedCatalogPaths(t *testing.T) {
	ctx, client := openGenBindHarness(t, 60*time.Second)
	store, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	uid := strings.ReplaceAll(uuid.NewString(), "-", "")
	boundProject := "genbind-bound-" + uid
	otherProject := "genbind-other-" + uid
	emptyProject := "genbind-empty-" + uid
	alpha, beta := "gen-alpha-"+uid, "gen-beta-"+uid

	insertGenBindCatalogRow(t, ctx, client, "genbind-bound-a", boundProject, &alpha, "grants/recorded")
	insertGenBindCatalogRow(t, ctx, client, "genbind-bound-b", boundProject, &beta, "grants/recorded-cross-gen")
	insertGenBindCatalogRow(t, ctx, client, "genbind-other-x", otherProject, &alpha, "grants/foreign")

	resolver := authscope.NewScopeResolver(authscope.ScopeResolverConfig{
		ListExistingAuthResourcePaths: store.DiscoverExistingAuthResourcePaths,
	})
	principal, err := (authscope.StaticAuthenticator{Principal: authscope.Principal{
		Subject:           "genbind-restricted-test",
		AuthResourcePaths: []string{boundProject, otherProject, "grants/recorded", "grants/recorded-cross-gen", "grants/foreign", "grants/unrecorded"},
	}}).Authenticate(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	requestCtx := authscope.ContextWithPrincipal(ctx, principal)

	if err := resolver.AuthorizeWrite(requestCtx, principal, boundProject, "grants/recorded"); err != nil {
		t.Fatalf("AuthorizeWrite recorded path (generation %s): %v", alpha, err)
	}
	if err := resolver.AuthorizeWrite(requestCtx, principal, boundProject, "grants/recorded-cross-gen"); err != nil {
		t.Fatalf("AuthorizeWrite recorded path held only in a different generation (union requirement): %v", err)
	}
	err = resolver.AuthorizeWrite(requestCtx, principal, boundProject, "grants/foreign")
	genBindDiscoveryErrorShape(t, err, "AuthorizeWrite foreign path")
	if !errors.Is(err, authscope.ErrForbidden) {
		t.Fatalf("AuthorizeWrite path recorded only by another project err=%v; want %v (F-B27 project binding)", err, authscope.ErrForbidden)
	}
	bootstrapErr := resolver.AuthorizeWrite(requestCtx, principal, emptyProject, "grants/unrecorded")
	genBindDiscoveryErrorShape(t, bootstrapErr, "AuthorizeWrite bootstrap project")
	if bootstrapErr != nil {
		t.Fatalf("AuthorizeWrite bootstrap project (no recorded paths) err=%v; want first-generation bootstrap allow", bootstrapErr)
	}
}
