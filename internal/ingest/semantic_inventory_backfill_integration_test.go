package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/catalog"
	catalogarango "github.com/calypr/loom/internal/catalog/arango"
	publication "github.com/calypr/loom/internal/dataset"
	arangostore "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestRetainedSemanticInventoryBackfillArangoIntegration(t *testing.T) {
	if os.Getenv("LOOM_C01_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_C01_ARANGO_INTEGRATION=1 to run retained semantic inventory Arango proof")
	}
	endpoint := strings.TrimSpace(os.Getenv("LOOM_C01_ARANGO_URL"))
	database := strings.TrimSpace(os.Getenv("LOOM_C01_ARANGO_DATABASE"))
	if endpoint == "" || database == "" {
		t.Fatal("LOOM_C01_ARANGO_URL and an isolated LOOM_C01_ARANGO_DATABASE are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	client, err := arangostore.Open(ctx, endpoint, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	project := "c01-backfill-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	generation := strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := client.Bootstrap(ctx, arangostore.BootstrapSpec{Collections: []arangostore.CollectionSpec{{Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}
	store, err := catalogarango.New(client)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareSemanticInventoryBackfill(ctx); err != nil {
		t.Fatal(err)
	}
	payloadA := backfillIntegrationPayload("scope-a", 1000)
	payloadB := backfillIntegrationPayload("scope-b", 2)
	keys := []string{"c01_" + strings.ReplaceAll(uuid.NewString(), "-", ""), "c01_" + strings.ReplaceAll(uuid.NewString(), "-", "")}
	fixtureDocs := make([]map[string]any, 2)
	for index, fixture := range []struct {
		payload map[string]any
		scope   string
	}{{payloadA, "scope-a"}, {payloadB, "scope-b"}} {
		doc := map[string]any{
			"_key": keys[index], "project": project, "dataset_generation": generation,
			"auth_resource_path": fixture.scope, "payload": fixture.payload,
		}
		encoded, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		fixtureDocs[index] = doc
		if err := client.InsertBatchRaw(ctx, "Observation", []json.RawMessage{encoded}, false, ""); err != nil {
			t.Fatalf("insert isolated retained source %s: %v", fixture.scope, err)
		}
	}
	before := readBackfillIntegrationSources(t, ctx, store, project, generation)
	manifest := integrationBackfillManifest(t, project, generation)
	build := catalog.NewSemanticInventoryBuild(project, generation, "")
	build.SourceKind = catalog.SemanticInventorySourceRetained
	if _, claimed, err := store.ClaimSemanticInventoryBackfill(ctx, build, "integration-owner", time.Minute); err != nil || !claimed {
		t.Fatalf("initial explicit claim claimed=%v err=%v", claimed, err)
	}
	if _, claimed, err := store.ClaimSemanticInventoryBackfill(ctx, build, "integration-contender", time.Minute); err != catalogarango.ErrSemanticInventoryClaimed || claimed {
		t.Fatalf("concurrent explicit claim claimed=%v err=%v, want claim rejection", claimed, err)
	}
	if err := store.FailSemanticInventoryBackfill(ctx, build, "integration-owner", "release test claim"); err != nil {
		t.Fatal(err)
	}
	options := SemanticInventoryBackfillOptions{Project: project, DatasetGeneration: generation, PageSize: 10, BatchSize: 64}
	var progressCount int
	options.Progress = func(SemanticInventoryBackfillProgress) { progressCount++ }
	report, err := BackfillSemanticInventory(ctx, store, manifest, options)
	if err != nil {
		t.Fatal(err)
	}
	if report.State != catalog.SemanticInventoryComplete || report.SourceAvailability != catalog.SemanticInventorySourceAvailabilityVerified || report.ScannedTotal != 2 || report.Contributions != 1002 || progressCount == 0 {
		t.Fatalf("retained backfill report = %+v progress events=%d, want two source rows and 1002 contributions", report, progressCount)
	}
	assertRetainedInventoryPageCodes(t, ctx, store, project, generation, []string{"scope-a"}, 1000, "scope-a", 1)
	assertRetainedInventoryPageCodes(t, ctx, store, project, generation, []string{"scope-b"}, 2, "scope-b", 1)
	assertRetainedInventoryPageCodes(t, ctx, store, project, generation, []string{"scope-a", "scope-b"}, 1001, "scope-a", 2)
	pageAfterReplay, err := collectRetainedInventoryPageCodes(ctx, store, project, generation, []string{"scope-a", "scope-b"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := BackfillSemanticInventory(ctx, store, manifest, options)
	if err != nil || !second.NoOp {
		t.Fatalf("second retained backfill = %+v err=%v, want complete no-op", second, err)
	}
	pageAfterSecondRun, err := collectRetainedInventoryPageCodes(ctx, store, project, generation, []string{"scope-a", "scope-b"})
	if err != nil || !equalStringSets(pageAfterReplay, pageAfterSecondRun) {
		t.Fatalf("second-run inventory changed: before=%d after=%d err=%v", len(pageAfterReplay), len(pageAfterSecondRun), err)
	}
	after := readBackfillIntegrationSources(t, ctx, store, project, generation)
	if len(before) != len(fixtureDocs) || len(after) != len(before) {
		t.Fatalf("retained source row counts before=%d after=%d fixture=%d", len(before), len(after), len(fixtureDocs))
	}
	for key, source := range before {
		if after[key].Key != source.Key || after[key].AuthResourcePath == nil || source.AuthResourcePath == nil || *after[key].AuthResourcePath != *source.AuthResourcePath || string(after[key].Payload) != string(source.Payload) {
			t.Fatalf("retained source %s changed during backfill", key)
		}
	}
}

func backfillIntegrationPayload(scope string, distinct int) map[string]any {
	codes := []string{}
	if scope == "scope-a" {
		for index := 0; index < distinct; index++ {
			codes = append(codes, fmt.Sprintf("concept-%04d", index))
		}
	} else {
		codes = []string{"concept-0000", "scope-b-only"}
	}
	components := make([]any, 0, len(codes))
	for _, code := range codes {
		components = append(components, map[string]any{
			"code":          map[string]any{"coding": []any{map[string]any{"system": "urn:c01-backfill", "version": "v1", "code": code}}},
			"valueQuantity": map[string]any{"value": 1.0, "unit": "mg"},
		})
	}
	return map[string]any{"resourceType": "Observation", "id": scope, "component": components}
}

func readBackfillIntegrationSources(t *testing.T, ctx context.Context, store *catalogarango.Store, project, generation string) map[string]catalogarango.RetainedSemanticInventoryRow {
	t.Helper()
	page, err := store.ReadRetainedSemanticInventoryPage(ctx, project, generation, "Observation", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	rows := make(map[string]catalogarango.RetainedSemanticInventoryRow, len(page.Rows))
	for _, row := range page.Rows {
		rows[row.Key] = row
	}
	return rows
}

func integrationBackfillManifest(t *testing.T, project, generation string) publication.Manifest {
	t.Helper()
	ref, err := publication.NewRef(project, generation)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := publication.NewSchemaSnapshot("c01-backfill", "4.0.1", strings.Repeat("b", 64), []string{"Observation"})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := publication.NewManifest(ref, schema)
	if err != nil {
		t.Fatal(err)
	}
	manifest.State = publication.StateStaged
	return manifest
}

func assertRetainedInventoryPageCodes(t *testing.T, ctx context.Context, store *catalogarango.Store, project, generation string, paths []string, wantCount int, prefix string, sharedPopulation int64) {
	t.Helper()
	codes, err := collectRetainedInventoryPageCodes(ctx, store, project, generation, paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != wantCount {
		t.Fatalf("inventory code count for %v = %d, want %d", paths, len(codes), wantCount)
	}
	if len(paths) == 1 && paths[0] == "scope-a" {
		for index := 0; index < 1000; index++ {
			code := fmt.Sprintf("concept-%04d", index)
			if _, ok := codes[code]; !ok {
				t.Fatalf("scope-a page is missing expected code %q", code)
			}
		}
	}
	shared := codes["concept-0000"]
	if shared.Population != sharedPopulation {
		t.Fatalf("shared concept population for %v = %d, want %d", paths, shared.Population, sharedPopulation)
	}
	if prefix == "scope-b" {
		if _, ok := codes["scope-b-only"]; !ok {
			t.Fatal("scope-b inventory is missing its distinct concept")
		}
		if _, ok := codes["concept-0001"]; ok {
			t.Fatal("scope-b inventory leaked scope-a-only concept concept-0001")
		}
	}
}

func collectRetainedInventoryPageCodes(ctx context.Context, store *catalogarango.Store, project, generation string, paths []string) (map[string]catalog.SemanticObservation, error) {
	options := catalog.SemanticInventoryPageOptions{
		Project: project, DatasetGeneration: generation, AuthResourcePaths: paths,
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(false),
		Limit:                         catalog.SemanticInventoryPageLimit,
	}
	codes := map[string]catalog.SemanticObservation{}
	for {
		page, err := store.PageSemanticInventory(ctx, options)
		if err != nil {
			return nil, err
		}
		if page.State != catalog.SemanticInventoryComplete {
			return nil, fmt.Errorf("inventory state for %v is %q, want complete", paths, page.State)
		}
		if len(page.Entries) > catalog.SemanticInventoryPageLimit {
			return nil, fmt.Errorf("inventory page size %d exceeds %d", len(page.Entries), catalog.SemanticInventoryPageLimit)
		}
		for _, entry := range page.Entries {
			code := entry.Observation.Key.Code
			if _, duplicate := codes[code]; duplicate {
				return nil, fmt.Errorf("inventory page repeated code %q", code)
			}
			codes[code] = entry.Observation
		}
		if page.NextCursor == "" {
			return codes, nil
		}
		options.Cursor = page.NextCursor
	}
}

func equalStringSets(left, right map[string]catalog.SemanticObservation) bool {
	if len(left) != len(right) {
		return false
	}
	for code := range left {
		if _, ok := right[code]; !ok {
			return false
		}
	}
	return true
}
