package arango

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	arangostore "github.com/calypr/loom/internal/store/arango"
	store "github.com/calypr/loom/internal/store/arango"
	storearango "github.com/calypr/loom/internal/store/arango"
	"github.com/calypr/loom/internal/testkit/arangotest"
	"github.com/google/uuid"
)

func TestResourceInventoryPersistsAndScopesCountsAcrossStoreReopen(t *testing.T) {
	owned := arangotest.Owned(t, "loom_test_resource_inventory_")
	endpoint, database := owned.URL, owned.Name

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	project := "resource-inventory-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	generation := strings.ReplaceAll(uuid.NewString(), "-", "")
	otherGeneration := generation + "-other"
	otherProject := project + "-other"
	resourceTypes := []string{"Observation", "Patient"}
	cleanupPairs := [][2]string{
		{project, generation}, {project, otherGeneration}, {otherProject, generation},
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		cleanupClient, err := arangostore.Open(cleanupCtx, endpoint, database)
		if err != nil {
			t.Errorf("open Arango client for fixture cleanup: %v", err)
			return
		}
		defer cleanupClient.Close(context.Background())
		for _, resourceType := range resourceTypes {
			exists, err := cleanupClient.CollectionExists(cleanupCtx, resourceType)
			if err != nil {
				t.Errorf("check %s fixture collection during cleanup: %v", resourceType, err)
				continue
			}
			if !exists {
				continue
			}
			for _, pair := range cleanupPairs {
				if err := cleanupClient.ExecuteAQL(cleanupCtx, deleteResourceInventoryFixtureAQL, map[string]any{
					"@collection": resourceType, "project": pair[0], "dataset_generation": pair[1],
				}); err != nil {
					t.Errorf("remove UUID-scoped %s fixture rows for project/generation %s/%s: %v", resourceType, pair[0], pair[1], err)
				}
			}
		}
		if exists, err := cleanupClient.CollectionExists(cleanupCtx, catalog.ResourceInventoryCollection); err != nil {
			t.Errorf("check resource inventory fixture collection during cleanup: %v", err)
		} else if exists {
			if err := cleanupClient.ExecuteAQL(cleanupCtx, deleteResourceInventoryFixtureAQL, map[string]any{
				"@collection": catalog.ResourceInventoryCollection, "project": project, "dataset_generation": generation,
			}); err != nil {
				t.Errorf("remove UUID-scoped resource inventory rows: %v", err)
			}
		}
	})

	client := owned.Client

	if err := client.Bootstrap(ctx, arangostore.BootstrapSpec{Collections: []arangostore.CollectionSpec{
		{Name: "Observation"}, {Name: "Patient"},
	}}); err != nil {
		t.Fatalf("bootstrap resource inventory fixture collections: %v", err)
	}

	fixtures := []struct {
		project    string
		generation string
		path       string
		resource   string
		count      int
	}{
		{project, generation, "scope-a", "Patient", 2},
		{project, generation, "scope-a", "Observation", 1},
		{project, generation, "scope-b", "Patient", 1},
		{project, generation, "scope-b", "Observation", 3},
		{project, otherGeneration, "scope-a", "Patient", 5},
		{project, otherGeneration, "scope-a", "Observation", 7},
		{otherProject, generation, "scope-a", "Patient", 11},
		{otherProject, generation, "scope-a", "Observation", 13},
	}
	for _, fixture := range fixtures {
		if err := insertResourceInventoryFixtureRows(ctx, client, fixture.resource, fixture.project, fixture.generation, fixture.path, fixture.count); err != nil {
			t.Fatalf("insert %s fixture for project/generation %s/%s: %v", fixture.resource, fixture.project, fixture.generation, err)
		}
	}

	store, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	rebuildOptions := catalog.ResourceInventoryRebuildOptions{
		Project: project, DatasetGeneration: generation, ResourceTypes: resourceTypes,
	}
	firstSummary, err := store.RebuildResourceInventory(ctx, rebuildOptions)
	if err != nil {
		t.Fatalf("rebuild inventory: %v", err)
	}
	firstBuild, firstRows := readPersistedResourceInventory(t, ctx, client, project, generation)
	if firstBuild.BuildID == "" || firstBuild.Project != project || firstBuild.DatasetGeneration != generation || !firstBuild.Complete {
		t.Fatalf("persisted build marker = %+v", firstBuild)
	}
	if firstBuild.ResourceTypeCount != len(resourceTypes) || firstBuild.CoverageDigest == "" || firstBuild.CountRowCount != 4 || len(firstRows) != 4 {
		t.Fatalf("persisted build/count rows = marker %+v, rows %+v; want two types and four scoped rows", firstBuild, firstRows)
	}
	if firstSummary.BuildID != firstBuild.BuildID || firstSummary.CoverageDigest != firstBuild.CoverageDigest || firstSummary.ResourceTypeCount != firstBuild.ResourceTypeCount || firstSummary.CountRowCount != firstBuild.CountRowCount {
		t.Fatalf("rebuild summary %+v does not match persisted marker %+v", firstSummary, firstBuild)
	}
	for _, row := range firstRows {
		if row.BuildID != firstBuild.BuildID {
			t.Fatalf("count row BuildID %q does not match marker BuildID %q: %+v", row.BuildID, firstBuild.BuildID, row)
		}
	}
	assertPersistedResourceInventoryCounts(t, firstRows, map[string]int64{
		"Patient/scope-a": 2, "Patient/scope-b": 1,
		"Observation/scope-a": 1, "Observation/scope-b": 3,
	})

	if err := owned.Close(ctx); err != nil {
		t.Fatalf("close first Arango client: %v", err)
	}
	client, err = arangostore.Open(ctx, endpoint, database)
	if err != nil {
		t.Fatalf("reopen Arango client: %v", err)
	}
	defer client.Close(context.Background())
	store, err = New(client)
	if err != nil {
		t.Fatal(err)
	}
	reopenedBuild, reopenedRows := readPersistedResourceInventory(t, ctx, client, project, generation)
	if !reflect.DeepEqual(reopenedBuild, firstBuild) || !reflect.DeepEqual(reopenedRows, firstRows) {
		t.Fatalf("resource inventory did not survive Store reopen: before marker/rows=%+v/%+v, after=%+v/%+v", firstBuild, firstRows, reopenedBuild, reopenedRows)
	}

	repeatedSummary, err := store.RebuildResourceInventory(ctx, rebuildOptions)
	if err != nil {
		t.Fatalf("repeat inventory rebuild: %v", err)
	}
	repeatedBuild, repeatedRows := readPersistedResourceInventory(t, ctx, client, project, generation)
	if repeatedSummary.BuildID != firstSummary.BuildID || repeatedBuild.BuildID != firstBuild.BuildID {
		t.Fatalf("rebuild changed BuildID from summary/marker %q/%q to %q/%q", firstSummary.BuildID, firstBuild.BuildID, repeatedSummary.BuildID, repeatedBuild.BuildID)
	}
	if !reflect.DeepEqual(repeatedSummary, firstSummary) {
		t.Fatalf("repeated rebuild summary changed: first=%+v repeated=%+v", firstSummary, repeatedSummary)
	}
	if !reflect.DeepEqual(repeatedBuild, firstBuild) || !reflect.DeepEqual(repeatedRows, firstRows) {
		t.Fatalf("repeated immutable-generation rebuild changed its identity or counts: first=%+v/%+v repeated=%+v/%+v", firstBuild, firstRows, repeatedBuild, repeatedRows)
	}

	recorder := &resourceInventoryQueryRecorder{client: client}
	store, err = New(recorder)
	if err != nil {
		t.Fatal(err)
	}
	readOptions := catalog.ResourceInventoryOptions{Project: project, DatasetGeneration: generation, ResourceTypes: resourceTypes}
	readOptions.AuthResourcePathsUnrestricted = catalog.ExplicitAuthResourcePathsUnrestricted(true)
	unrestricted, err := store.DiscoverResourceInventory(ctx, readOptions)
	if err != nil {
		t.Fatalf("read unrestricted resource inventory: %v", err)
	}
	assertResourceInventoryAvailable(t, unrestricted, map[string]int64{"Patient": 3, "Observation": 4})

	readOptions.AuthResourcePathsUnrestricted = catalog.ExplicitAuthResourcePathsUnrestricted(false)
	readOptions.AuthResourcePaths = []string{"scope-a"}
	scopeA, err := store.DiscoverResourceInventory(ctx, readOptions)
	if err != nil {
		t.Fatalf("read scope-a resource inventory: %v", err)
	}
	assertResourceInventoryAvailable(t, scopeA, map[string]int64{"Patient": 2, "Observation": 1})

	readOptions.AuthResourcePaths = []string{"scope-b"}
	scopeB, err := store.DiscoverResourceInventory(ctx, readOptions)
	if err != nil {
		t.Fatalf("read scope-b resource inventory: %v", err)
	}
	assertResourceInventoryAvailable(t, scopeB, map[string]int64{"Patient": 1, "Observation": 3})

	readOptions.AuthResourcePaths = []string{}
	emptyScopeQueryStart := len(recorder.queries)
	emptyScope, err := store.DiscoverResourceInventory(ctx, readOptions)
	if err != nil {
		t.Fatalf("read restricted-empty resource inventory: %v", err)
	}
	if !emptyScope.Available || !emptyScope.Complete || len(emptyScope.Values) != 0 {
		t.Fatalf("restricted-empty inventory = available %v complete %v values %+v; want available, complete, and empty", emptyScope.Available, emptyScope.Complete, emptyScope.Values)
	}
	for _, query := range recorder.queries[emptyScopeQueryStart:] {
		if query.query == readResourceInventoryCountsAQL {
			t.Fatalf("restricted-empty inventory read count rows despite an empty authorization scope: %s", query.query)
		}
	}

	for _, foreign := range []struct {
		project    string
		generation string
	}{
		{project: project, generation: otherGeneration},
		{project: otherProject, generation: generation},
	} {
		foreignResult, err := store.DiscoverResourceInventory(ctx, catalog.ResourceInventoryOptions{
			Project: foreign.project, DatasetGeneration: foreign.generation, ResourceTypes: resourceTypes,
			AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(true),
		})
		if !errors.Is(err, ErrResourceInventoryBuildUnavailable) {
			t.Fatalf("read unbuilt foreign project/generation %s/%s error = %v; want unavailable build sentinel", foreign.project, foreign.generation, err)
		}
		if foreignResult.Available || foreignResult.Status != catalog.EvidenceUnavailable || len(foreignResult.Values) != 0 {
			t.Fatalf("unbuilt foreign project/generation %s/%s leaked inventory: %+v", foreign.project, foreign.generation, foreignResult)
		}
	}
	assertNoRawResourceCountQueries(t, recorder.queries, resourceTypes)
}

func insertResourceInventoryFixtureRows(ctx context.Context, client *arangostore.Client, resourceType, project, generation, authPath string, count int) error {
	rows := make([]json.RawMessage, 0, count)
	for i := 0; i < count; i++ {
		doc, err := json.Marshal(map[string]any{
			"_key": uuid.NewString(), "project": project, "dataset_generation": generation,
			"auth_resource_path": authPath,
		})
		if err != nil {
			return err
		}
		rows = append(rows, doc)
	}
	return client.InsertBatchRaw(ctx, resourceType, rows, false, "")
}

type resourceInventoryPersistedMarker struct {
	Key               string
	Project           string
	DatasetGeneration string
	BuildID           string
	CoverageDigest    string
	ResourceTypeCount int
	CountRowCount     int
	Complete          bool
}

func readPersistedResourceInventory(t *testing.T, ctx context.Context, client *arangostore.Client, project, generation string) (resourceInventoryPersistedMarker, []catalog.ResourceInventoryCount) {
	t.Helper()
	var marker resourceInventoryPersistedMarker
	err := client.QueryRows(ctx, persistedResourceInventoryByTypeAQL, 100, map[string]any{
		"@collection": catalog.ResourceInventoryCollection,
		"project":     project, "dataset_generation": generation,
		"record_type": catalog.ResourceInventoryBuildRecordType,
	}, func(row map[string]any) error {
		if marker.Key != "" {
			return fmt.Errorf("multiple resource inventory build markers for one project generation")
		}
		resourceTypeCount, err := decodeInt64(row["resource_type_count"])
		if err != nil {
			return fmt.Errorf("decode marker resource_type_count: %w", err)
		}
		countRowCount, err := decodeInt64(row["count_row_count"])
		if err != nil {
			return fmt.Errorf("decode marker count_row_count: %w", err)
		}
		complete, _ := row["complete"].(bool)
		marker = resourceInventoryPersistedMarker{
			Key: stringValue(row["_key"]), Project: stringValue(row["project"]),
			DatasetGeneration: stringValue(row["dataset_generation"]), BuildID: stringValue(row["build_id"]),
			CoverageDigest: stringValue(row["coverage_digest"]), ResourceTypeCount: int(resourceTypeCount),
			CountRowCount: int(countRowCount), Complete: complete,
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read persisted resource inventory build marker: %v", err)
	}
	var rows []catalog.ResourceInventoryCount
	err = client.QueryRows(ctx, persistedResourceInventoryByTypeAQL, 100, map[string]any{
		"@collection": catalog.ResourceInventoryCollection,
		"project":     project, "dataset_generation": generation,
		"record_type": catalog.ResourceInventoryCountRecordType,
	}, func(row map[string]any) error {
		rows = append(rows, catalog.ResourceInventoryCount{
			Key: stringValue(row["_key"]), Project: stringValue(row["project"]),
			DatasetGeneration: stringValue(row["dataset_generation"]), BuildID: stringValue(row["build_id"]),
			AuthResourcePath: stringValue(row["auth_resource_path"]), ResourceType: stringValue(row["resource_type"]),
			DocumentCount: 0,
		})
		count, err := decodeInt64(row["document_count"])
		if err != nil {
			return fmt.Errorf("decode count row document_count: %w", err)
		}
		rows[len(rows)-1].DocumentCount = count
		return nil
	})
	if err != nil {
		t.Fatalf("read persisted resource inventory count rows: %v", err)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ResourceType != rows[j].ResourceType {
			return rows[i].ResourceType < rows[j].ResourceType
		}
		return rows[i].AuthResourcePath < rows[j].AuthResourcePath
	})
	return marker, rows
}

func assertPersistedResourceInventoryCounts(t *testing.T, rows []catalog.ResourceInventoryCount, expected map[string]int64) {
	t.Helper()
	actual := make(map[string]int64, len(rows))
	for _, row := range rows {
		if row.Key == "" || row.BuildID == "" || row.Project == "" || row.DatasetGeneration == "" {
			t.Fatalf("persisted count row lacks identity fields: %+v", row)
		}
		actual[row.ResourceType+"/"+row.AuthResourcePath] = row.DocumentCount
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("persisted resource inventory counts = %v, want %v", actual, expected)
	}
}

func assertResourceInventoryAvailable(t *testing.T, result catalog.ResourceInventoryResult, expected map[string]int64) {
	t.Helper()
	if !result.Available || !result.Complete || result.Status != catalog.EvidenceAvailable {
		t.Fatalf("resource inventory result is not complete and available: %+v", result)
	}
	actual := make(map[string]int64, len(result.Values))
	for _, value := range result.Values {
		actual[value.ResourceType] += value.DocumentCount
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("resource inventory counts = %v, want %v", actual, expected)
	}
}

type resourceInventoryRecordedQuery struct {
	query string
	vars  map[string]any
}

type resourceInventoryQueryRecorder struct {
	client
	queries []resourceInventoryRecordedQuery
}

func (r *resourceInventoryQueryRecorder) QueryRows(ctx context.Context, query string, batchSize int, bindVars map[string]interface{}, visit arangostore.RowVisitor) error {
	vars := make(map[string]any, len(bindVars))
	for key, value := range bindVars {
		vars[key] = value
	}
	r.queries = append(r.queries, resourceInventoryRecordedQuery{query: query, vars: vars})
	return r.client.QueryRows(ctx, query, batchSize, bindVars, visit)
}

func assertNoRawResourceCountQueries(t *testing.T, queries []resourceInventoryRecordedQuery, resourceTypes []string) {
	t.Helper()
	for _, recorded := range queries {
		if strings.Contains(recorded.query, "@@resource_collection") {
			t.Fatalf("ordinary inventory read issued raw resource collection query: %s", recorded.query)
		}
		for _, resourceType := range resourceTypes {
			collectionBound := false
			for key, value := range recorded.vars {
				if strings.HasPrefix(key, "@") && value == resourceType {
					collectionBound = true
					break
				}
			}
			if collectionBound || strings.Contains(recorded.query, "FOR d IN "+resourceType) {
				t.Fatalf("ordinary inventory read counted raw %s collection: %s", resourceType, recorded.query)
			}
		}
	}
}

const persistedResourceInventoryByTypeAQL = `
FOR d IN @@collection
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER d.record_type == @record_type
  SORT d.resource_type, d.auth_resource_path
  RETURN d`

const deleteResourceInventoryFixtureAQL = `
FOR d IN @@collection
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  REMOVE d IN @@collection`

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

	union, err := store.DiscoverExistingAuthResourcePaths(ctx, authscope.AuthResourcePathOptions{Project: project, AllGenerations: true})
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

	scoped, err := store.DiscoverExistingAuthResourcePaths(ctx, authscope.AuthResourcePathOptions{Project: project, DatasetGeneration: alpha})
	if err != nil {
		t.Fatalf("scoped discovery: %v", err)
	}
	if want := []string{"grants/alpha"}; !reflect.DeepEqual(scoped, want) {
		t.Fatalf("scoped discovery (generation %s)=%v; want %v (generation-scoped query must keep binding @dataset_generation)", alpha, scoped, want)
	}

	noGeneration, err := store.DiscoverExistingAuthResourcePaths(ctx, authscope.AuthResourcePathOptions{Project: project})
	if err != nil {
		t.Fatalf("generation-less discovery: %v", err)
	}
	if want := []string{"grants/none"}; !reflect.DeepEqual(noGeneration, want) {
		t.Fatalf("generation-less discovery=%v; want %v (empty generation must stay 'row has no generation')", noGeneration, want)
	}

	if _, err := store.DiscoverExistingAuthResourcePaths(ctx, authscope.AuthResourcePathOptions{Project: project, AllGenerations: true, DatasetGeneration: alpha}); !errors.Is(err, ErrAllGenerationsWithGeneration) {
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

type fb27ctlArangoClient struct {
	queries []string
	vars    []map[string]any
}

func (c *fb27ctlArangoClient) QueryRows(_ context.Context, query string, _ int, vars map[string]any, _ store.RowVisitor) error {
	c.queries = append(c.queries, query)
	copied := make(map[string]any, len(vars))
	for k, v := range vars {
		copied[k] = v
	}
	c.vars = append(c.vars, copied)
	return nil
}

func (c *fb27ctlArangoClient) InsertBatchRaw(context.Context, string, []json.RawMessage, bool, string) error {
	panic("unused by the auth path discovery controls")
}

func (c *fb27ctlArangoClient) ExecuteAQL(context.Context, string, map[string]any) error {
	panic("unused by the auth path discovery controls")
}

func (c *fb27ctlArangoClient) CollectionExists(context.Context, string) (bool, error) {
	panic("unused by the auth path discovery controls")
}

func (c *fb27ctlArangoClient) Bootstrap(context.Context, store.BootstrapSpec) error {
	panic("unused by the auth path discovery controls")
}

func fb27ctlDiscover(t *testing.T, opts authscope.AuthResourcePathOptions) ([]string, error) {
	t.Helper()
	client := &fb27ctlArangoClient{}
	s, err := New(client)
	if err != nil {
		t.Fatalf("VERDICT=UNEXPECTED store construction = %v", err)
	}
	paths, err := s.DiscoverExistingAuthResourcePaths(context.Background(), opts)
	if err != nil {
		if len(client.queries) != 0 {
			t.Fatalf("VERDICT=UNEXPECTED rejected request reached Arango (%d queries) for %+v: %v", len(client.queries), opts, err)
		}
		return paths, err
	}
	if len(client.queries) != 1 {
		t.Fatalf("VERDICT=UNEXPECTED accepted request issued %d queries for %+v, want 1", len(client.queries), opts)
	}
	if paths == nil {
		t.Fatalf("VERDICT=UNEXPECTED accepted request returned nil paths for %+v", opts)
	}
	return paths, nil
}

func TestFB27AllGenerationsWithSelectedGenerationIsRejected(t *testing.T) {
	for _, generation := range []string{"gen-1", " gen-1 ", "sha256:0000000000000000000000000000000000000000000000000000000000000000"} {
		_, err := fb27ctlDiscover(t, authscope.AuthResourcePathOptions{
			Project:           "projA",
			DatasetGeneration: generation,
			AllGenerations:    true,
		})
		if err == nil {
			t.Fatalf("VERDICT=CONFIRMED (pre-fix archived shape): AllGenerations with datasetGeneration %q was accepted, so discovery silently ignored the selected generation", generation)
		}
		if !errors.Is(err, ErrAllGenerationsWithGeneration) {
			t.Fatalf("VERDICT=UNEXPECTED conflict error must wrap ErrAllGenerationsWithGeneration, got %v", err)
		}
		for _, want := range []string{"AllGenerations", "datasetGeneration", generation} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("VERDICT=UNEXPECTED conflict error %q must name the conflicting option and its value %q", err, want)
			}
		}
	}
	t.Logf("VERDICT=REPAIRED AllGenerations plus a selected generation is refused before any query")
}

func TestFB27SupportedAuthResourcePathOptionShapesStillDiscover(t *testing.T) {
	cases := []struct {
		name string
		opts authscope.AuthResourcePathOptions
	}{
		{"all generations with empty generation", authscope.AuthResourcePathOptions{Project: "projA", AllGenerations: true}},
		{"all generations with whitespace only generation", authscope.AuthResourcePathOptions{Project: "projA", DatasetGeneration: "   ", AllGenerations: true}},
		{"generation scoped", authscope.AuthResourcePathOptions{Project: "projA", DatasetGeneration: " gen-1 "}},
		{"legacy null namespace", authscope.AuthResourcePathOptions{Project: "projA"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := fb27ctlDiscover(t, tc.opts); err != nil {
				t.Fatalf("VERDICT=UNEXPECTED valid options %+v rejected: %v", tc.opts, err)
			}
		})
	}
}
