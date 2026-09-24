package arango

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/catalog"
	arangostore "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestSemanticInventoryPersistsAndPagesAcrossAuthorizedPaths(t *testing.T) {
	if os.Getenv("LOOM_C01_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_C01_ARANGO_INTEGRATION=1 to run semantic inventory Arango proof")
	}
	endpoint := strings.TrimSpace(os.Getenv("LOOM_C01_ARANGO_URL"))
	database := strings.TrimSpace(os.Getenv("LOOM_C01_ARANGO_DATABASE"))
	if endpoint == "" || database == "" {
		t.Fatal("LOOM_C01_ARANGO_URL and an isolated LOOM_C01_ARANGO_DATABASE are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	client, err := arangostore.Open(ctx, endpoint, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	store, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	project := "c01-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	generation := strings.ReplaceAll(uuid.NewString(), "-", "")
	buildsExist, err := client.CollectionExists(ctx, catalog.SemanticInventoryBuildCollection)
	if err != nil {
		t.Fatal(err)
	}
	inventoryExists, err := client.CollectionExists(ctx, catalog.SemanticInventoryCollection)
	if err != nil {
		t.Fatal(err)
	}
	if !buildsExist && !inventoryExists {
		legacyPage, err := store.PageSemanticInventory(ctx, catalog.SemanticInventoryPageOptions{Project: project, DatasetGeneration: generation})
		if err != nil || legacyPage.State != catalog.SemanticInventoryUnknown || len(legacyPage.Entries) != 0 {
			t.Fatalf("database without inventory collections page = state %q entries %d err %v", legacyPage.State, len(legacyPage.Entries), err)
		}
	}
	if err := client.Bootstrap(ctx, arangostore.BootstrapSpec{Collections: []arangostore.CollectionSpec{
		{Name: catalog.SemanticInventoryCollection, Indexes: [][]string{
			{"project", "dataset_generation", "auth_resource_path", "resource_type", "binding_id", "concept_id"},
			{"project", "dataset_generation", "build_id", "binding_id", "concept_id", "auth_resource_path"},
			{"project", "dataset_generation", "build_id", "source_kind", "binding_id", "concept_id", "auth_resource_path"},
			{"project", "dataset_generation", "build_id", "source_kind", "auth_resource_path", "resource_type", "concept_id", "concept_slot_id", "observation.rule_hint"},
		}},
		{Name: catalog.SemanticInventoryEntryCollection, Indexes: [][]string{
			{"project", "dataset_generation", "build_id", "source_kind", "binding_id", "concept_id", "auth_resource_path"},
			{"project", "dataset_generation", "build_id", "source_kind", "resource_type", "binding_id", "concept_id", "auth_resource_path"},
		}},
		{Name: catalog.SemanticInventoryBuildCollection, Indexes: [][]string{{"project", "dataset_generation"}}},
	}}); err != nil {
		t.Fatal(err)
	}

	options := catalog.SemanticInventoryPageOptions{
		Project:                       project,
		DatasetGeneration:             generation,
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(false),
		AuthResourcePaths:             []string{"scope-a"},
		Limit:                         1000,
	}
	page, err := store.PageSemanticInventory(ctx, options)
	if err != nil || page.State != catalog.SemanticInventoryUnknown || len(page.Entries) != 0 {
		t.Fatalf("unmarked generation page = state %q entries %d err %v, want unknown and empty", page.State, len(page.Entries), err)
	}
	build := catalog.NewSemanticInventoryBuild(project, generation, "")
	if err := store.BeginSemanticInventoryBuild(ctx, build); err != nil {
		t.Fatal(err)
	}
	if err := store.AdvanceSemanticInventoryBuild(ctx, build, 23, "private/running-source.ndjson"); err != nil {
		t.Fatal(err)
	}
	page, err = store.PageSemanticInventory(ctx, options)
	if err != nil || page.State != catalog.SemanticInventoryRunning || len(page.Entries) != 0 {
		t.Fatalf("running generation page = state %q entries %d err %v, want hidden incomplete data", page.State, len(page.Entries), err)
	}
	if page.Build.ScannedResources != 0 || page.Build.Checkpoint != "" || len(page.Build.Diagnostics) != 0 {
		t.Fatalf("restricted running page leaked build metadata: %+v", page.Build)
	}

	fixtureRoot := t.TempDir()
	fixtures := []struct {
		scope string
		codes []string
	}{
		{scope: "scope-a", codes: append(inventoryCodes("shared", 100), inventoryCodes("a", 900)...)},
		{scope: "scope-b", codes: append(inventoryCodes("shared", 100), inventoryCodes("b", 100)...)},
	}
	fixtureFiles := make(map[string]string, len(fixtures))
	for _, fixture := range fixtures {
		path := filepath.Join(fixtureRoot, fixture.scope, "Observation.ndjson")
		if err := writeSemanticInventoryFixture(path, fixture.codes, fixture.scope); err != nil {
			t.Fatal(err)
		}
		fixtureFiles[fixture.scope] = path
		if err := ingestSemanticInventoryFixture(ctx, store, project, generation, fixture.scope, fixtureRoot, path); err != nil {
			t.Fatal(err)
		}
	}
	build.ScannedResources = 1200
	build.Checkpoint = "private/snapshot.ndjson"
	build.Diagnostics = []string{"restricted detail"}
	if err := store.CompleteSemanticInventoryBuild(ctx, build, 1200, build.Checkpoint); err != nil {
		t.Fatal(err)
	}

	page, err = store.PageSemanticInventory(ctx, options)
	if err != nil || page.State != catalog.SemanticInventoryComplete {
		t.Fatalf("completed scope page state=%q err=%v", page.State, err)
	}
	if page.Build.ScannedResources != 0 || page.Build.Checkpoint != "" || len(page.Build.Diagnostics) != 0 || page.Build.AuthResourcePath != "" {
		t.Fatalf("restricted build leaked metadata: %+v", page.Build)
	}
	aEntries := collectSemanticInventoryPages(t, ctx, store, options)
	expectedA := append(inventoryCodes("shared", 100), inventoryCodes("a", 900)...)
	assertInventoryEntries(t, aEntries, expectedA, "a")
	assertCodePopulation(t, aEntries, "shared-000", 1)
	assertSourceRecords(t, aEntries, "shared-000", 1)

	options.AuthResourcePaths = []string{"scope-b"}
	bEntries := collectSemanticInventoryPages(t, ctx, store, options)
	expectedB := append(inventoryCodes("shared", 100), inventoryCodes("b", 100)...)
	assertInventoryEntries(t, bEntries, expectedB, "b")
	assertCodePopulation(t, bEntries, "shared-000", 1)

	options.AuthResourcePaths = []string{"scope-b", "scope-a"}
	combined := collectSemanticInventoryPages(t, ctx, store, options)
	expectedCombined := append(append([]string(nil), expectedA...), inventoryCodes("b", 100)...)
	assertInventoryEntries(t, combined, expectedCombined, "combined")
	assertCodePopulation(t, combined, "shared-000", 2)
	assertSourceRecords(t, combined, "shared-000", 0)
	assertBoundedAggregateEvidence(t, combined, "shared-000")

	options.Query = "label scope-a shared"
	searched := collectSemanticInventoryPages(t, ctx, store, options)
	assertInventoryEntries(t, searched, inventoryCodes("shared", 100), "search")
	assertCodePopulation(t, searched, "shared-000", 2)
	options.Query = "valueQuantity.value"
	searched = collectSemanticInventoryPages(t, ctx, store, options)
	assertInventoryEntries(t, searched, expectedCombined, "value path search")
	options.Query = ""

	options.AuthResourcePaths = nil
	options.AuthResourcePathsUnrestricted = catalog.ExplicitAuthResourcePathsUnrestricted(true)
	unrestricted := collectSemanticInventoryPages(t, ctx, store, options)
	assertInventoryEntries(t, unrestricted, expectedCombined, "unrestricted")
	assertCodePopulation(t, unrestricted, "shared-000", 2)

	options.AuthResourcePaths = []string{"scope-a"}
	options.AuthResourcePathsUnrestricted = catalog.ExplicitAuthResourcePathsUnrestricted(false)
	firstPage, err := store.PageSemanticInventory(ctx, options)
	if err != nil || firstPage.NextCursor == "" {
		t.Fatalf("first page cursor=%q err=%v, want another page", firstPage.NextCursor, err)
	}
	options.Cursor = firstPage.NextCursor
	options.AuthResourcePaths = []string{"scope-b"}
	if _, err := store.PageSemanticInventory(ctx, options); err != catalog.ErrSemanticInventoryCursorMismatch {
		t.Fatalf("cursor reused under different auth path: %v", err)
	}

	failedGeneration := generation + "-failed"
	failedBuild := catalog.NewSemanticInventoryBuild(project, failedGeneration, "scope-a")
	if err := store.BeginSemanticInventoryBuild(ctx, failedBuild); err != nil {
		t.Fatal(err)
	}
	if err := store.AdvanceSemanticInventoryBuild(ctx, failedBuild, 9, "private/failed-source.ndjson"); err != nil {
		t.Fatal(err)
	}
	if err := store.FailSemanticInventoryBuild(ctx, failedBuild, "private source detail"); err != nil {
		t.Fatal(err)
	}
	persistedFailure, found, err := store.semanticInventoryBuildByKey(ctx, failedBuild.Key)
	if err != nil || !found || persistedFailure.ScannedResources != 9 || persistedFailure.Checkpoint != "private/failed-source.ndjson" {
		t.Fatalf("failed build watermark = %+v found=%v err=%v, want latest progress preserved", persistedFailure, found, err)
	}
	failedPage, err := store.PageSemanticInventory(ctx, catalog.SemanticInventoryPageOptions{
		Project:                       project,
		DatasetGeneration:             failedGeneration,
		AuthResourcePaths:             []string{"scope-a"},
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(false),
	})
	if err != nil || failedPage.State != catalog.SemanticInventoryFailed || len(failedPage.Entries) != 0 || len(failedPage.Build.Diagnostics) != 0 {
		t.Fatalf("failed restricted page = %+v err %v", failedPage, err)
	}
	if failedPage.Build.ScannedResources != 0 || failedPage.Build.Checkpoint != "" {
		t.Fatalf("restricted failed page leaked progress: %+v", failedPage.Build)
	}

	// Replay the same source files: per-source event keys replace their prior
	// rows. The source files live in t.TempDir and the collection is never cleared.
	for _, fixture := range fixtures {
		if err := ingestSemanticInventoryFixture(ctx, store, project, generation, fixture.scope, fixtureRoot, fixtureFiles[fixture.scope]); err != nil {
			t.Fatal(err)
		}
	}
	options.Cursor = ""
	options.AuthResourcePaths = []string{"scope-a", "scope-b"}
	replayed := collectSemanticInventoryPages(t, ctx, store, options)
	assertInventoryEntries(t, replayed, expectedCombined, "replay")
	assertCodePopulation(t, replayed, "shared-000", 2)
}

func TestSemanticInventorySelectionResolverAggregatesAuthorizedIndexedRows(t *testing.T) {
	if os.Getenv("LOOM_C01_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_C01_ARANGO_INTEGRATION=1 to run semantic inventory Arango proof")
	}
	endpoint := strings.TrimSpace(os.Getenv("LOOM_C01_ARANGO_URL"))
	database := strings.TrimSpace(os.Getenv("LOOM_C01_ARANGO_DATABASE"))
	if endpoint == "" || database == "" {
		t.Fatal("LOOM_C01_ARANGO_URL and an isolated LOOM_C01_ARANGO_DATABASE are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client, err := arangostore.Open(ctx, endpoint, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	store, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareSemanticInventoryBackfill(ctx); err != nil {
		t.Fatal(err)
	}
	project := "c01-resolve-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	generation := strings.ReplaceAll(uuid.NewString(), "-", "")
	build := catalog.NewSemanticInventoryBuild(project, generation, "")
	build.SourceKind = catalog.SemanticInventorySourceRetained
	build.SourceAvailability = catalog.SemanticInventorySourceAvailabilityVerified
	build.EntryIndexVersion = catalog.SemanticInventoryEntryIndexVersion
	if err := store.BeginSemanticInventoryBuild(ctx, build); err != nil {
		t.Fatal(err)
	}
	conceptID, bindingID := "concept-shared", "binding-shared"
	rows := make([]json.RawMessage, 0, 3)
	for _, scope := range []string{"scope-a", "scope-b"} {
		unit := "mg"
		if scope == "scope-b" {
			unit = "mmol/L"
		}
		observation := catalog.SemanticObservation{
			SchemaVersion: catalog.SemanticObservationSchemaVersion,
			Source:        catalog.SemanticObservationSource{Type: "Observation", Path: "code"},
			Key:           catalog.SemanticObservationKey{Selector: "code.coding[]", System: "urn:c01", Code: "shared", Display: "Label " + scope},
			Value:         catalog.SemanticObservationValue{Selector: "valueQuantity.value", Type: "decimal"},
			LogicalType:   "decimal",
			ObservedUnits: []string{unit},
			Population:    1,
			Examples:      []string{"Observation/" + scope},
			Completeness:  catalog.SemanticComplete,
			Status:        "SUPPORTED",
			RuleHint:      "OBSERVATION_CODE_VALUE",
			RuleVersion:   "3",
		}
		entry := map[string]any{
			"_key":               strings.ReplaceAll(uuid.NewString(), "-", ""),
			"project":            project,
			"dataset_generation": generation,
			"build_id":           build.BuildID,
			"source_kind":        build.SourceKind,
			"auth_resource_path": scope,
			"resource_type":      "Observation",
			"binding_id":         bindingID,
			"concept_id":         conceptID,
			"example":            "Observation/" + scope,
			"observation":        observation,
		}
		encoded, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, encoded)
	}
	privateObservation := catalog.SemanticObservation{
		SchemaVersion: catalog.SemanticObservationSchemaVersion,
		Source:        catalog.SemanticObservationSource{Type: "Observation", Path: "code"},
		Key:           catalog.SemanticObservationKey{Selector: "code.coding[]", System: "urn:c01", Code: "scope-b-only", Display: "Private label"},
		Value:         catalog.SemanticObservationValue{Selector: "valueQuantity.value", Type: "decimal"},
		LogicalType:   "decimal", Population: 1, Completeness: catalog.SemanticComplete,
		Status: "SUPPORTED", RuleHint: "OBSERVATION_CODE_VALUE", RuleVersion: "3",
	}
	privateRow, err := json.Marshal(map[string]any{
		"_key":               strings.ReplaceAll(uuid.NewString(), "-", ""),
		"project":            project,
		"dataset_generation": generation,
		"build_id":           build.BuildID,
		"source_kind":        build.SourceKind,
		"auth_resource_path": "scope-b",
		"resource_type":      "Observation",
		"binding_id":         "binding-scope-b-only",
		"concept_id":         "concept-scope-b-only",
		"example":            "Observation/private",
		"observation":        privateObservation,
	})
	if err != nil {
		t.Fatal(err)
	}
	rows = append(rows, privateRow)
	if err := client.InsertBatchRaw(ctx, catalog.SemanticInventoryEntryCollection, rows, true, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteSemanticInventoryBuild(ctx, build, 3, ""); err != nil {
		t.Fatal(err)
	}
	resolve := func(paths []string, unrestricted bool) catalog.SemanticInventoryResolveResult {
		t.Helper()
		result, err := store.ResolveSemanticInventorySelections(ctx, catalog.SemanticInventoryResolveOptions{
			Project: project, DatasetGeneration: generation,
			AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(unrestricted),
			AuthResourcePaths:             paths,
			References:                    []catalog.SemanticInventoryReference{{ConceptID: conceptID, BindingID: bindingID}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	a := resolve([]string{"scope-a"}, false)
	if len(a.Entries) != 1 || a.Entries[0].Observation.Population != 1 || a.Entries[0].Observation.Key.Display != "Label scope-a" {
		t.Fatalf("scope-a resolver result = %+v", a)
	}
	unauthorized, err := store.ResolveSemanticInventorySelections(ctx, catalog.SemanticInventoryResolveOptions{
		Project: project, DatasetGeneration: generation,
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(false),
		AuthResourcePaths:             []string{"scope-a"},
		References:                    []catalog.SemanticInventoryReference{{ConceptID: "concept-scope-b-only", BindingID: "binding-scope-b-only"}},
	})
	if err != nil || len(unauthorized.Entries) != 0 {
		t.Fatalf("out-of-scope exact selection result = %+v err=%v", unauthorized, err)
	}
	combined := resolve([]string{"scope-a", "scope-b"}, false)
	if len(combined.Entries) != 1 || combined.Entries[0].Observation.Population != 2 || !combined.Entries[0].Observation.ExamplesTruncated || !combined.Entries[0].Observation.ObservedUnitsTruncated {
		t.Fatalf("combined resolver result = %+v; want one aggregated row with truthful bounded evidence", combined)
	}
	unrestricted := resolve(nil, true)
	if len(unrestricted.Entries) != 1 || unrestricted.Entries[0].Observation.Population != 2 {
		t.Fatalf("unrestricted resolver result = %+v", unrestricted)
	}
}

func inventoryCodes(prefix string, count int) []string {
	codes := make([]string, count)
	for i := range codes {
		codes[i] = fmt.Sprintf("%s-%03d", prefix, i)
	}
	return codes
}

func writeSemanticInventoryFixture(path string, codes []string, scope string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	for i, code := range codes {
		unit := "mg"
		if scope == "scope-b" {
			unit = "mmol/L"
		}
		payload := map[string]any{
			"resourceType": "Observation",
			"id":           fmt.Sprintf("%s-%d", strings.ReplaceAll(code, "-", "_"), i),
			"component": []any{map[string]any{
				"code": map[string]any{"coding": []any{map[string]any{
					"system": "urn:c01", "version": "v1", "code": code, "display": "Label " + scope + " " + code,
				}}},
				"valueQuantity": map[string]any{"value": float64(i), "unit": unit},
			}},
		}
		if err := encoder.Encode(payload); err != nil {
			_ = file.Close()
			return err
		}
	}
	if scope == "scope-a" {
		payload := map[string]any{
			"resourceType": "Observation",
			"id":           "shared_000_without_value",
			"component": []any{map[string]any{
				"code": map[string]any{"coding": []any{map[string]any{
					"system": "urn:c01", "version": "v1", "code": "shared-000", "display": "Label scope-a shared-000",
				}}},
			}},
		}
		if err := encoder.Encode(payload); err != nil {
			_ = file.Close()
			return err
		}
	}
	return file.Close()
}

func ingestSemanticInventoryFixture(ctx context.Context, store *Store, project, generation, authPath, root, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	sourceFile := filepath.ToSlash(filepath.Clean(relative))
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	batch := make([]catalog.SemanticInventoryContribution, 0, 50)
	profiler := catalog.NewProfilerForGenerationWithLimits(project, generation, authPath, "Observation", nil, catalog.DefaultProfileLimits())
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := store.WriteSemanticInventoryContributions(ctx, batch, 50); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}
	line := 0
	for scanner.Scan() {
		line++
		var payload map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &payload); err != nil {
			return err
		}
		sourceID := fmt.Sprintf("%s#%d", sourceFile, line)
		profiler.ObservePayloadWithInventory(payload, map[string]float64{}, sourceID, func(contribution catalog.SemanticInventoryContribution) {
			batch = append(batch, contribution)
		})
		if len(batch) >= 50 {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return flush()
}

func collectSemanticInventoryPages(t *testing.T, ctx context.Context, store *Store, options catalog.SemanticInventoryPageOptions) []catalog.SemanticInventoryEntry {
	t.Helper()
	entries := make([]catalog.SemanticInventoryEntry, 0)
	options.Cursor = ""
	for {
		page, err := store.PageSemanticInventory(ctx, options)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Entries) > catalog.SemanticInventoryPageLimit {
			t.Fatalf("page size %d exceeds maximum %d", len(page.Entries), catalog.SemanticInventoryPageLimit)
		}
		entries = append(entries, page.Entries...)
		if page.NextCursor == "" {
			return entries
		}
		options.Cursor = page.NextCursor
	}
}

func assertInventoryEntries(t *testing.T, entries []catalog.SemanticInventoryEntry, expectedCodes []string, label string) {
	t.Helper()
	expected := make(map[string]struct{}, len(expectedCodes))
	for _, code := range expectedCodes {
		expected[code] = struct{}{}
	}
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		key := entry.BindingID + "\x00" + entry.ConceptID
		if _, duplicate := seen[key]; duplicate {
			t.Fatalf("%s inventory repeated scoped concept %q", label, entry.Observation.Key.Code)
		}
		seen[key] = struct{}{}
		if entry.Observation.Role == catalog.SemanticRoleCategoricalSlot {
			if entry.Observation.Value.Selector != "component[].code.coding[].code" {
				t.Fatalf("%s unexpected metadata slot: %#v", label, entry)
			}
			continue
		}
		code := entry.Observation.Key.Code
		if _, ok := expected[code]; !ok {
			t.Fatalf("%s inventory contains unexpected code %q", label, code)
		}
		delete(expected, code)
	}
	if len(expected) != 0 {
		for missing := range expected {
			t.Fatalf("%s inventory is missing expected code %q", label, missing)
		}
	}
}

func assertSourceRecords(t *testing.T, entries []catalog.SemanticInventoryEntry, code string, want int64) {
	t.Helper()
	for _, entry := range entries {
		if entry.Observation.Role == catalog.SemanticRoleCodedValue && entry.Observation.Key.Code == code {
			if entry.SourceRecords != want {
				t.Fatalf("%s source records = %d, want %d", code, entry.SourceRecords, want)
			}
			return
		}
	}
	t.Fatalf("no coded value %s", code)
}

func assertCodePopulation(t *testing.T, entries []catalog.SemanticInventoryEntry, code string, want int64) {
	t.Helper()
	for _, entry := range entries {
		if entry.Observation.Key.Code == code {
			if entry.Observation.Population != want {
				t.Fatalf("%s population = %d, want %d", code, entry.Observation.Population, want)
			}
			return
		}
	}
	t.Fatalf("inventory is missing code %q", code)
}

func assertBoundedAggregateEvidence(t *testing.T, entries []catalog.SemanticInventoryEntry, code string) {
	t.Helper()
	for _, entry := range entries {
		if entry.Observation.Key.Code != code {
			continue
		}
		if !entry.Observation.ExamplesTruncated {
			t.Fatalf("aggregated examples for %s were marked complete despite discarded contributions", code)
		}
		if !entry.Observation.ObservedUnitsTruncated {
			t.Fatalf("conflicting aggregated units for %s were marked complete", code)
		}
		return
	}
	t.Fatalf("inventory is missing code %q", code)
}
