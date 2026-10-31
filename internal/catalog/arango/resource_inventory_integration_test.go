package arango

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

	"github.com/calypr/loom/internal/catalog"
	arangostore "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestResourceInventoryPersistsAndScopesCountsAcrossStoreReopen(t *testing.T) {
	if os.Getenv("LOOM_C01_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_C01_ARANGO_INTEGRATION=1 to run resource inventory Arango proof")
	}
	endpoint := strings.TrimSpace(os.Getenv("LOOM_C01_ARANGO_URL"))
	database := strings.TrimSpace(os.Getenv("LOOM_C01_ARANGO_DATABASE"))
	if endpoint == "" || database == "" {
		t.Fatal("LOOM_C01_ARANGO_URL and an isolated LOOM_C01_ARANGO_DATABASE are required")
	}

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

	client, err := arangostore.Open(ctx, endpoint, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	// This proof owns its fixture prerequisites even on an empty test database.
	// Do not truncate existing collections: fixture rows remain UUID-scoped.
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

	if err := client.Close(ctx); err != nil {
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
