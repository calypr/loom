package arango

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/catalog/fieldprofile"
	"github.com/calypr/loom/internal/semantic"
	arangostore "github.com/calypr/loom/internal/store/arango"
	"github.com/calypr/loom/internal/testkit"
	"github.com/calypr/loom/internal/testkit/arangotest"
	"github.com/google/uuid"
)

func TestSemanticInventoryPersistsAndPagesAcrossAuthorizedPaths(t *testing.T) {
	owned := arangotest.Owned(t, "loom_test_semantic_inventory_")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	client := owned.Client
	store, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	project := "c01-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	generation := strings.ReplaceAll(uuid.NewString(), "-", "")
	buildsExist, err := client.CollectionExists(ctx, semantic.BuildCollection)
	if err != nil {
		t.Fatal(err)
	}
	inventoryExists, err := client.CollectionExists(ctx, semantic.Collection)
	if err != nil {
		t.Fatal(err)
	}
	if !buildsExist && !inventoryExists {
		legacyPage, err := store.PageSemanticInventory(ctx, semantic.PageOptions{Project: project, DatasetGeneration: generation})
		if err != nil || legacyPage.State != semantic.Unknown || len(legacyPage.Entries) != 0 {
			t.Fatalf("database without inventory collections page = state %q entries %d err %v", legacyPage.State, len(legacyPage.Entries), err)
		}
	}
	if err := client.Bootstrap(ctx, arangostore.BootstrapSpec{Collections: []arangostore.CollectionSpec{
		{Name: semantic.Collection, Indexes: [][]string{
			{"project", "dataset_generation", "build_id"},
			{"project", "dataset_generation", "auth_resource_path", "resource_type", "binding_id", "concept_id"},
			{"project", "dataset_generation", "build_id", "binding_id", "concept_id", "auth_resource_path"},
			{"project", "dataset_generation", "build_id", "source_kind", "binding_id", "concept_id", "auth_resource_path"},
			{"project", "dataset_generation", "build_id", "source_id"},
		}},
		{Name: semantic.EntryCollection, Indexes: [][]string{
			{"project", "dataset_generation", "build_id", "source_kind", "binding_id", "concept_id", "auth_resource_path"},
			{"project", "dataset_generation", "build_id", "source_kind", "resource_type", "binding_id", "concept_id", "auth_resource_path"},
		}},
		{Name: semantic.BuildCollection, Indexes: [][]string{{"project", "dataset_generation"}}},
	}}); err != nil {
		t.Fatal(err)
	}

	options := semantic.PageOptions{
		Project:                       project,
		DatasetGeneration:             generation,
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(false),
		AuthResourcePaths:             []string{"scope-a"},
		Limit:                         1000,
	}
	page, err := store.PageSemanticInventory(ctx, options)
	if err != nil || page.State != semantic.Unknown || len(page.Entries) != 0 {
		t.Fatalf("unmarked generation page = state %q entries %d err %v, want unknown and empty", page.State, len(page.Entries), err)
	}
	build := semantic.NewBuild(project, generation, "")
	if err := store.BeginSemanticInventoryBuild(ctx, build); err != nil {
		t.Fatal(err)
	}
	if err := store.AdvanceSemanticInventoryBuild(ctx, build, 23, "private/running-source.ndjson"); err != nil {
		t.Fatal(err)
	}
	page, err = store.PageSemanticInventory(ctx, options)
	if err != nil || page.State != semantic.Running || len(page.Entries) != 0 {
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
	if err != nil || page.State != semantic.Complete {
		t.Fatalf("completed scope page state=%q err=%v", page.State, err)
	}
	if page.Build.ScannedResources != 0 || page.Build.Checkpoint != "" || len(page.Build.Diagnostics) != 0 || page.Build.AuthResourcePath != "" {
		t.Fatalf("restricted build leaked metadata: %+v", page.Build)
	}
	aEntries := collectSemanticInventoryPages(t, ctx, store, options)
	expectedA := append(inventoryCodes("shared", 100), inventoryCodes("a", 900)...)
	assertInventoryEntries(t, aEntries, expectedA, "a")
	assertCodePopulation(t, aEntries, "shared-000", 1)

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
	if _, err := store.PageSemanticInventory(ctx, options); err != semantic.ErrSemanticInventoryCursorMismatch {
		t.Fatalf("cursor reused under different auth path: %v", err)
	}

	failedGeneration := generation + "-failed"
	failedBuild := semantic.NewBuild(project, failedGeneration, "scope-a")
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
	failedPage, err := store.PageSemanticInventory(ctx, semantic.PageOptions{
		Project:                       project,
		DatasetGeneration:             failedGeneration,
		AuthResourcePaths:             []string{"scope-a"},
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(false),
	})
	if err != nil || failedPage.State != semantic.Failed || len(failedPage.Entries) != 0 || len(failedPage.Build.Diagnostics) != 0 {
		t.Fatalf("failed restricted page = %+v err %v", failedPage, err)
	}
	if failedPage.Build.ScannedResources != 0 || failedPage.Build.Checkpoint != "" {
		t.Fatalf("restricted failed page leaked progress: %+v", failedPage.Build)
	}

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

func TestSemanticInventoryMaterializationMatchesPreaggregatedLegacyRowsAgainstArango(t *testing.T) {
	fixtures := []semantic.Contribution{
		inventoryAggregationFixture("a", 1, false),
		inventoryAggregationFixture("a", 2, true),
		inventoryAggregationFixture("a", 1, false),
		inventoryAggregationFixture("Z", 1, false),
	}
	fixtureAggregates := preaggregatedLegacyInventoryFixtures(fixtures)
	if len(fixtureAggregates) != 2 || fixtureAggregates[0].Observation.Population != 4 || !fixtureAggregates[0].Observation.ExamplesTruncated {
		t.Fatalf("preaggregated legacy fixtures = %#v, want display a population 4 with truncation plus separate Z", fixtureAggregates)
	}
	owned := arangotest.Owned(t, "loom_semantic_aggregation_")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	store, err := New(owned.Client)
	if err != nil {
		t.Fatal(err)
	}
	if err := owned.Client.Bootstrap(ctx, arangostore.BootstrapSpec{Collections: []arangostore.CollectionSpec{
		{Name: semantic.Collection},
		{Name: semantic.EntryCollection},
		{Name: semantic.BuildCollection},
	}}); err != nil {
		t.Fatal(err)
	}
	project := "aggregation-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	var rowLevel, aggregated []semantic.Entry
	var rawPopulation int64
	for _, fixture := range fixtures {
		rawPopulation += fixture.Observation.Population
	}
	for index, generation := range []string{"row-level", "aggregated"} {
		build := semantic.NewBuild(project, generation, "scope")
		if err := store.BeginSemanticInventoryBuild(ctx, build); err != nil {
			t.Fatal(err)
		}
		rowContributions := make([]semantic.Contribution, 0, len(fixtures))
		for ordinal, fixture := range fixtures {
			fixture.Project = project
			fixture.DatasetGeneration = generation
			fixture.BuildID = build.BuildID
			fixture.AuthResourcePath = "scope"
			fixture.SourceKind = semantic.SourceFile
			fixture.SourceID = fmt.Sprintf("META/Observation.ndjson#%d", ordinal+1)
			fixture.Ordinal = ordinal
			fixture.Key = fmt.Sprintf("row-%d-%s", ordinal, generation)
			fixture.Observation.SchemaVersion = build.ObservationSchema
			rowContributions = append(rowContributions, fixture)
		}
		contributions := rowContributions
		if index == 1 {
			contributions = preaggregatedLegacyInventoryFixtures(rowContributions)
			if len(contributions) != 2 {
				t.Fatalf("aggregated rows = %d, want two distinct ICU reducer inputs", len(contributions))
			}
			for ordinal, contribution := range contributions {
				contribution.Ordinal = ordinal
				contribution.SourceID = "META/Observation.ndjson"
				contribution.Key = fmt.Sprintf("aggregate-%d-%s", ordinal, generation)
				contributions[ordinal] = contribution
			}
		}
		if err := store.WriteSemanticInventoryContributions(ctx, contributions, 100); err != nil {
			t.Fatal(err)
		}
		build, err = store.EnsureSemanticInventoryEntries(ctx, build)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CompleteSemanticInventoryBuild(ctx, build, rawPopulation, "META/Observation.ndjson"); err != nil {
			t.Fatal(err)
		}
		if build.EntryIndexVersion != semantic.EntryIndexVersion {
			t.Fatalf("entry index version = %d, want %d", build.EntryIndexVersion, semantic.EntryIndexVersion)
		}
		page, err := store.PageSemanticInventory(ctx, semantic.PageOptions{
			Project: project, DatasetGeneration: generation,
			AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(true), Limit: 10,
		})
		if err != nil || page.State != semantic.Complete {
			t.Fatalf("page state=%s err=%v", page.State, err)
		}
		resolved, err := store.ResolveSemanticInventorySelections(ctx, semantic.ResolveOptions{
			Project: project, DatasetGeneration: generation,
			AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(true),
			References:                    []semantic.Reference{{BindingID: "binding", ConceptID: "concept"}},
		})
		if err != nil || resolved.State != semantic.Complete {
			t.Fatalf("resolve state=%s err=%v", resolved.State, err)
		}
		if index == 0 {
			rowLevel = page.Entries
			if !reflect.DeepEqual(page.Entries, resolved.Entries) {
				t.Fatalf("row-level page entries differ from resolve entries")
			}
		} else {
			aggregated = page.Entries
			if !reflect.DeepEqual(page.Entries, resolved.Entries) {
				t.Fatalf("aggregated page entries differ from resolve entries")
			}
		}
	}
	if !reflect.DeepEqual(rowLevel, aggregated) {
		t.Fatalf("aggregated materialized entries differ from row-level entries\nrow level: %+v\naggregated: %+v", rowLevel, aggregated)
	}
	if len(aggregated) != 1 {
		t.Fatalf("aggregated entries=%+v, want one materialized entry", aggregated)
	}
	if aggregated[0].Observation.Population != rawPopulation {
		t.Fatalf("aggregated population=%d, want %d", aggregated[0].Observation.Population, rawPopulation)
	}
}

func preaggregatedLegacyInventoryFixtures(rows []semantic.Contribution) []semantic.Contribution {
	byDisplay := make(map[string]semantic.Contribution)
	for _, row := range rows {
		display := row.Observation.Key.Display
		current, exists := byDisplay[display]
		if !exists {
			byDisplay[display] = row
			continue
		}
		current.Observation.Population += row.Observation.Population
		current.Observation.ExamplesTruncated = current.Observation.ExamplesTruncated || row.Observation.ExamplesTruncated
		byDisplay[display] = current
	}
	return []semantic.Contribution{byDisplay["a"], byDisplay["Z"]}
}

func inventoryAggregationFixture(display string, population int64, truncated bool) semantic.Contribution {
	return semantic.Contribution{
		Project: "project", DatasetGeneration: "generation", AuthResourcePath: "scope", ResourceType: "Observation",
		BuildID: semantic.BuildID("project", "generation"), SourceKind: semantic.SourceFile,
		SourceID: "META/Observation.ndjson#1", Ordinal: 0, ConceptID: "concept", BindingID: "binding", Example: display,
		Observation: semantic.Observation{
			SchemaVersion: 4,
			Source:        semantic.Source{Canonical: "canonical", Type: "Observation", Profile: "profile", Path: "path"},
			Key:           semantic.Key{Selector: "code", System: "urn:system", Version: "1", Code: "code", Display: display},
			Value:         semantic.Value{Selector: "value", Type: "string", FallbackSelectors: []string{"a", "b"}},
			OwningScope:   "scope", ObservedUnits: []string{display}, Completeness: semantic.CompletenessComplete,
			Population: population, Examples: []string{display}, ExamplesTruncated: truncated,
		},
	}
}

func TestSemanticInventoryFallbackPolicySurvivesMaterializationAndReadback(t *testing.T) {
	owned := arangotest.Owned(t, "loom_test_semantic_inventory_")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client := owned.Client
	store, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareSemanticInventoryBackfill(ctx); err != nil {
		t.Fatal(err)
	}

	project := "c01-fallback-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	generation := strings.ReplaceAll(uuid.NewString(), "-", "")
	build := semantic.NewBuild(project, generation, "")
	build.SourceKind = semantic.SourceRetained
	build.SourceAvailability = semantic.SourceAvailabilityVerified
	if err := store.BeginSemanticInventoryBuild(ctx, build); err != nil {
		t.Fatal(err)
	}

	witnessIDs := []string{
		"26c7500c-f54f-5229-b5a7-a6757614bc21",
		"64704041-c4da-5375-aac0-88f951f78344",
	}
	emitter := semantic.NewEmitter(project, generation)
	contributions := make([]semantic.Contribution, 0, len(witnessIDs))
	for _, id := range witnessIDs {
		payload := map[string]any{
			"resourceType": "Observation",
			"id":           id,
			"code": map[string]any{"coding": []any{map[string]any{
				"system": "http://snomed.info/sct", "code": "1222593009", "display": "American Joint Committee on Cancer pathological stage group allowable value",
			}}},
			"valueCodeableConcept": map[string]any{"coding": []any{map[string]any{
				"system": "http://snomed.info/sct", "code": "1222593009", "display": "Stage I",
			}}},
		}
		emitter.ObservePayload(payload, "Observation", "scope-a", "retained:Observation/"+id, func(contribution semantic.Contribution) {
			contributions = append(contributions, contribution)
		})
	}
	if len(contributions) != len(witnessIDs) {
		t.Fatalf("fallback witness contributions = %d, want %d", len(contributions), len(witnessIDs))
	}
	if got := semanticBindingIDForIntegrationTest(contributions[0].Observation); got != contributions[0].BindingID {
		t.Fatalf("test binding identity does not match production emitter: got %q, emitted %q", got, contributions[0].BindingID)
	}
	orderedFallbacks := []string{"valueCodeableConcept.coding[].display", "valueCodeableConcept.text"}
	orderedBindingID := ""
	for _, contribution := range append([]semantic.Contribution(nil), contributions...) {
		ordered := contribution
		ordered.SourceID += ":ordered-fallback-policy"
		ordered.Key = "ordered-policy-" + uuid.NewString()
		ordered.Observation.Value.FallbackSelectors = append([]string(nil), orderedFallbacks...)
		ordered.BindingID = semanticBindingIDForIntegrationTest(ordered.Observation)
		if orderedBindingID == "" {
			orderedBindingID = ordered.BindingID
		} else if ordered.BindingID != orderedBindingID {
			t.Fatalf("same ordered fallback policy produced binding IDs %q and %q", orderedBindingID, ordered.BindingID)
		}
		contributions = append(contributions, ordered)
	}
	legacy := semantic.Contribution{
		Key: "legacy-primary-only", Project: project, DatasetGeneration: generation,
		AuthResourcePath: "scope-a", ResourceType: "Observation", BuildID: build.BuildID,
		SourceKind: semantic.SourceRetained, SourceID: "retained:Observation/legacy-primary-only",
		ConceptID: "legacy-primary-only-concept", BindingID: "legacy-primary-only-binding",
		Observation: semantic.Observation{
			SchemaVersion: semantic.ObservationSchemaVersion,
			Source:        semantic.Source{Type: "Observation", Path: "valueQuantity.value"},
			Key:           semantic.Key{Selector: "code.coding[]", System: "urn:legacy", Code: "primary-only"},
			Value:         semantic.Value{Selector: "valueQuantity.value", Type: "decimal"},
			LogicalType:   "decimal", Population: 1, Completeness: semantic.CompletenessComplete,
			Status: "SUPPORTED", RuleHint: "OBSERVATION_CODE_VALUE", RuleVersion: "4",
		},
	}
	contributions = append(contributions, legacy)
	if err := store.WriteSemanticInventoryContributions(ctx, contributions, 10); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteSemanticInventoryBuild(ctx, build, int64(len(contributions)), "test"); err != nil {
		t.Fatal(err)
	}

	unrestricted := false
	options := semantic.PageOptions{
		Project: project, DatasetGeneration: generation,
		AuthResourcePathsUnrestricted: &unrestricted, AuthResourcePaths: []string{"scope-a"}, Limit: 10,
	}
	entries := collectSemanticInventoryPages(t, ctx, store, options)
	if len(entries) != 3 {
		t.Fatalf("materialized semantic entries = %d, want two coded fallback policies and one primary-only row: %#v", len(entries), entries)
	}
	var fallbackEntry, orderedEntry, legacyEntry semantic.Entry
	for _, entry := range entries {
		switch {
		case entry.BindingID == contributions[0].BindingID:
			fallbackEntry = entry
		case entry.BindingID == orderedBindingID:
			orderedEntry = entry
		case entry.BindingID == legacy.BindingID:
			legacyEntry = entry
		}
	}
	if fallbackEntry.ConceptID == "" || fallbackEntry.Observation.Value.Selector != "valueCodeableConcept.text" ||
		!reflect.DeepEqual(fallbackEntry.Observation.Value.FallbackSelectors, []string{"valueCodeableConcept.coding[].display"}) ||
		!fallbackEntry.Observation.Value.FallbackOnBlankString || fallbackEntry.Observation.Population != 2 {
		t.Fatalf("materialized fallback policy = %#v", fallbackEntry)
	}
	if orderedEntry.ConceptID != fallbackEntry.ConceptID || orderedEntry.Observation.Population != 2 ||
		!reflect.DeepEqual(orderedEntry.Observation.Value.FallbackSelectors, orderedFallbacks) || !orderedEntry.Observation.Value.FallbackOnBlankString {
		t.Fatalf("materialized ordered fallback policy = %#v", orderedEntry)
	}
	if legacyEntry.ConceptID == "" || legacyEntry.Observation.Value.Selector != "valueQuantity.value" ||
		legacyEntry.Observation.Value.FallbackSelectors != nil || legacyEntry.Observation.Value.FallbackOnBlankString {
		t.Fatalf("materialized legacy primary-only defaults = %#v", legacyEntry)
	}

	references := []semantic.Reference{
		{ConceptID: fallbackEntry.ConceptID, BindingID: fallbackEntry.BindingID},
		{ConceptID: orderedEntry.ConceptID, BindingID: orderedEntry.BindingID},
		{ConceptID: legacyEntry.ConceptID, BindingID: legacyEntry.BindingID},
	}
	resolved, err := store.ResolveSemanticInventorySelections(ctx, semantic.ResolveOptions{
		Project: project, DatasetGeneration: generation,
		AuthResourcePathsUnrestricted: &unrestricted, AuthResourcePaths: []string{"scope-a"}, References: references,
	})
	if err != nil || len(resolved.Entries) != 3 {
		t.Fatalf("resolved semantic entries = %d err=%v, want both fallback policies and legacy row", len(resolved.Entries), err)
	}
	if got := resolved.Entries[0].Observation.Value; got.Selector != "valueCodeableConcept.text" ||
		!reflect.DeepEqual(got.FallbackSelectors, []string{"valueCodeableConcept.coding[].display"}) || !got.FallbackOnBlankString {
		t.Fatalf("resolved fallback policy = %#v", got)
	}
	resolvedByBinding := make(map[string]semantic.Entry, len(resolved.Entries))
	for _, entry := range resolved.Entries {
		resolvedByBinding[entry.BindingID] = entry
	}
	if got := resolvedByBinding[fallbackEntry.BindingID].Observation.Value; got.Selector != "valueCodeableConcept.text" ||
		!reflect.DeepEqual(got.FallbackSelectors, []string{"valueCodeableConcept.coding[].display"}) || !got.FallbackOnBlankString {
		t.Fatalf("resolved fallback policy = %#v", got)
	}
	if got := resolvedByBinding[orderedEntry.BindingID].Observation.Value; !reflect.DeepEqual(got.FallbackSelectors, orderedFallbacks) || !got.FallbackOnBlankString {
		t.Fatalf("resolved ordered fallback policy = %#v", got)
	}
	if got := resolvedByBinding[legacyEntry.BindingID].Observation.Value; got.Selector != "valueQuantity.value" || got.FallbackSelectors != nil || got.FallbackOnBlankString {
		t.Fatalf("resolved legacy primary-only defaults = %#v", got)
	}
}

func semanticBindingIDForIntegrationTest(observation semantic.Observation) string {
	parts := []string{
		"semantic-binding/v2",
		observation.Source.Type,
		observation.Source.Profile,
		observation.Source.Path,
		observation.Source.Canonical,
		observation.OwningScope,
		observation.Key.Selector,
		observation.Value.Selector,
		observation.Value.Type,
		strings.Join(observation.Value.FallbackSelectors, "\x1f"),
		strconv.FormatBool(observation.Value.FallbackOnBlankString),
		observation.ChoiceArm,
		observation.LogicalType,
		observation.RuleHint,
		observation.RuleVersion,
	}
	parts = append(parts, observation.ExtensionURLPath...)
	hash := sha256.New()
	for _, part := range parts {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(part))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func TestSemanticInventorySelectionResolverAggregatesAuthorizedIndexedRows(t *testing.T) {
	owned := arangotest.Owned(t, "loom_test_semantic_inventory_")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client := owned.Client
	store, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareSemanticInventoryBackfill(ctx); err != nil {
		t.Fatal(err)
	}
	project := "c01-resolve-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	generation := strings.ReplaceAll(uuid.NewString(), "-", "")
	build := semantic.NewBuild(project, generation, "")
	build.SourceKind = semantic.SourceRetained
	build.SourceAvailability = semantic.SourceAvailabilityVerified
	build.EntryIndexVersion = semantic.EntryIndexVersion
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
		observation := semantic.Observation{
			SchemaVersion: semantic.ObservationSchemaVersion,
			Source:        semantic.Source{Type: "Observation", Path: "code"},
			Key:           semantic.Key{Selector: "code.coding[]", System: "urn:c01", Code: "shared", Display: "Label " + scope},
			Value:         semantic.Value{Selector: "valueQuantity.value", Type: "decimal"},
			LogicalType:   "decimal",
			ObservedUnits: []string{unit},
			Population:    1,
			Examples:      []string{"Observation/" + scope},
			Completeness:  semantic.CompletenessComplete,
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
	privateObservation := semantic.Observation{
		SchemaVersion: semantic.ObservationSchemaVersion,
		Source:        semantic.Source{Type: "Observation", Path: "code"},
		Key:           semantic.Key{Selector: "code.coding[]", System: "urn:c01", Code: "scope-b-only", Display: "Private label"},
		Value:         semantic.Value{Selector: "valueQuantity.value", Type: "decimal"},
		LogicalType:   "decimal", Population: 1, Completeness: semantic.CompletenessComplete,
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
	if err := client.InsertBatchRaw(ctx, semantic.EntryCollection, rows, true, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteSemanticInventoryBuild(ctx, build, 3, ""); err != nil {
		t.Fatal(err)
	}
	resolve := func(paths []string, unrestricted bool) semantic.ResolveResult {
		t.Helper()
		result, err := store.ResolveSemanticInventorySelections(ctx, semantic.ResolveOptions{
			Project: project, DatasetGeneration: generation,
			AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(unrestricted),
			AuthResourcePaths:             paths,
			References:                    []semantic.Reference{{ConceptID: conceptID, BindingID: bindingID}},
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
	unauthorized, err := store.ResolveSemanticInventorySelections(ctx, semantic.ResolveOptions{
		Project: project, DatasetGeneration: generation,
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(false),
		AuthResourcePaths:             []string{"scope-a"},
		References:                    []semantic.Reference{{ConceptID: "concept-scope-b-only", BindingID: "binding-scope-b-only"}},
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
	batch := make([]semantic.Contribution, 0, 50)
	profiler := fieldprofile.NewProfilerForGenerationWithLimits(project, generation, authPath, "Observation", nil, fieldprofile.DefaultProfileLimits())
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
		profiler.ObservePayloadWithInventory(payload, map[string]float64{}, sourceID, func(contribution semantic.Contribution) {
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

func collectSemanticInventoryPages(t *testing.T, ctx context.Context, store *Store, options semantic.PageOptions) []semantic.Entry {
	t.Helper()
	entries := make([]semantic.Entry, 0)
	options.Cursor = ""
	for {
		page, err := store.PageSemanticInventory(ctx, options)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Entries) > semantic.PageLimit {
			t.Fatalf("page size %d exceeds maximum %d", len(page.Entries), semantic.PageLimit)
		}
		entries = append(entries, page.Entries...)
		if page.NextCursor == "" {
			return entries
		}
		options.Cursor = page.NextCursor
	}
}

func assertInventoryEntries(t *testing.T, entries []semantic.Entry, expectedCodes []string, label string) {
	t.Helper()
	if len(entries) != len(expectedCodes) {
		t.Fatalf("%s inventory rows = %d, want %d", label, len(entries), len(expectedCodes))
	}
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

func assertCodePopulation(t *testing.T, entries []semantic.Entry, code string, want int64) {
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

func assertBoundedAggregateEvidence(t *testing.T, entries []semantic.Entry, code string) {
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

func TestSemanticInventoryPagesAndResolvesCodedCategoryFamilies(t *testing.T) {
	owned := arangotest.Owned(t, "loom_test_coded_category_")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client := owned.Client
	project := "c01-coded-category-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	generation := strings.ReplaceAll(uuid.NewString(), "-", "")
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		for _, collection := range []string{
			semantic.Collection,
			semantic.EntryCollection,
			semantic.BuildCollection,
		} {
			if err := deleteSemanticCategoryFixtureRows(cleanupCtx, client, collection, project, generation); err != nil {
				t.Errorf("clean scoped %s fixture rows: %v", collection, err)
			}
		}
	})

	store, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareSemanticInventoryBackfill(ctx); err != nil {
		t.Fatal(err)
	}
	build := semantic.NewBuild(project, generation, "")
	build.SourceKind = semantic.SourceRetained
	build.SourceAvailability = semantic.SourceAvailabilityVerified
	if err := store.BeginSemanticInventoryBuild(ctx, build); err != nil {
		t.Fatal(err)
	}

	emitter := semantic.NewEmitter(project, generation)
	contributions := make([]semantic.Contribution, 0, 64)
	addCategoryCode := func(scope, code, display, system, version string) {
		t.Helper()
		payload := map[string]any{
			"resourceType": "Observation",
			"id":           strings.ReplaceAll(uuid.NewString(), "-", ""),
			"category": []any{map[string]any{"coding": []any{map[string]any{
				"system": system, "version": version, "code": code, "display": display,
			}}}},
		}
		observed := make([]semantic.Contribution, 0, 1)
		emitter.ObservePayload(payload, "Observation", scope, "retained:Observation/"+payload["id"].(string), func(contribution semantic.Contribution) {
			observed = append(observed, contribution)
		})
		if len(observed) != 1 || observed[0].Observation.RuleHint != semantic.RuleHintCodedConceptV1 {
			t.Fatalf("category %q emitted %#v; want one CODED_CONCEPT_V1 contribution", code, observed)
		}
		contributions = append(contributions, observed...)
	}

	const (
		largeSystem  = "urn:c01:category"
		largeVersion = "2025-01"
		largeCount   = 55
	)
	for i := 0; i < largeCount-1; i++ {
		code := fmt.Sprintf("large-%03d", i)
		addCategoryCode("scope-a", code, "Large category "+code, largeSystem, largeVersion)
	}

	lateCodeOnlyWitness := "zz-late-code-only-witness"
	addCategoryCode("scope-a", lateCodeOnlyWitness, "", largeSystem, largeVersion)
	addCategoryCode("scope-b", "scope-b-private", "AAA scope-b private category", largeSystem, largeVersion)

	smallDisplays := []string{"Small category alpha", "Small category beta"}
	addCategoryCode("scope-a", "small-alpha", smallDisplays[0], "urn:c01:small", "v1")
	addCategoryCode("scope-a", "small-beta", smallDisplays[1], "urn:c01:small", "v1")

	const fallbackSystem = "urn:c01:fallback"
	addCategoryCode("scope-a", "small-fallback-code", "", fallbackSystem, "v1")
	addCategoryCode("scope-a", "other-system-code", "Other system", "urn:c01:other", "v1")
	addCategoryCode("scope-a", "second-version-code", "Second version", largeSystem, "2026-01")
	addCategoryCode("scope-a", "unversioned-code", "Unversioned category", largeSystem, "")

	pairedPayload := map[string]any{
		"resourceType": "Observation",
		"id":           strings.ReplaceAll(uuid.NewString(), "-", ""),
		"code": map[string]any{"coding": []any{map[string]any{
			"system": "urn:c01:paired", "version": "v7", "code": "paired-exact-code", "display": "Paired exact value",
		}}},
		"valueQuantity": map[string]any{"value": float64(7), "unit": "mg"},
	}
	paired := make([]semantic.Contribution, 0, 1)
	emitter.ObservePayload(pairedPayload, "Observation", "scope-a", "retained:Observation/"+pairedPayload["id"].(string), func(contribution semantic.Contribution) {
		paired = append(paired, contribution)
	})
	if len(paired) != 1 || paired[0].Observation.RuleHint != semantic.RuleHintCodedValueV1 || paired[0].Observation.Key.Code != "paired-exact-code" {
		t.Fatalf("paired CODED_VALUE emission = %#v; want exact paired code", paired)
	}
	contributions = append(contributions, paired...)
	if err := store.WriteSemanticInventoryContributions(ctx, contributions, 32); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteSemanticInventoryBuild(ctx, build, int64(len(contributions)), "synthetic-retained-category-fixture"); err != nil {
		t.Fatal(err)
	}

	unrestricted := false
	options := semantic.PageOptions{
		Project:                       project,
		DatasetGeneration:             generation,
		AuthResourcePathsUnrestricted: &unrestricted,
		AuthResourcePaths:             []string{"scope-a"},
		SelectionMode:                 semantic.SystemVersionCategory,
		Limit:                         1,
	}

	queried := options
	queried.Query = lateCodeOnlyWitness
	latePage, err := store.PageSemanticInventory(ctx, queried)
	if err != nil {
		t.Fatal(err)
	}
	if len(latePage.Families) != 1 || len(latePage.Entries) != 0 {
		t.Fatalf("late code-only category query returned %d families and %d exact entries; want one family", len(latePage.Families), len(latePage.Entries))
	}
	largeFamily := latePage.Families[0]
	if largeFamily.System != largeSystem || largeFamily.Version != largeVersion || largeFamily.Population != largeCount {
		t.Fatalf("late code-only query family = %+v; want full %s/%s population %d", largeFamily, largeSystem, largeVersion, largeCount)
	}
	if largeFamily.FamilyID == "" || largeFamily.FamilyID != semantic.FamilyID(largeFamily.FamilyReference) {
		t.Fatalf("large family identity = %+v; want stable content-addressed family id", largeFamily.FamilyReference)
	}

	allRows := collectSemanticCategoryPages(t, ctx, store, options)
	if len(allRows.families) != 6 {
		t.Fatalf("scope-a category families = %d, want large, small, fallback, other-system, other-version, and unversioned: %#v", len(allRows.families), allRows.families)
	}
	if len(allRows.entries) != 1 {
		t.Fatalf("category-mode exact entries = %d, want the paired CODED_VALUE row", len(allRows.entries))
	}
	pairedEntry := allRows.entries[0]
	if pairedEntry.Observation.RuleHint != semantic.RuleHintCodedValueV1 ||
		pairedEntry.Observation.Key.Code != "paired-exact-code" || pairedEntry.Observation.Key.System != "urn:c01:paired" ||
		pairedEntry.Observation.Key.Version != "v7" {
		t.Fatalf("category mode changed or grouped the paired CODED_VALUE row: %+v", pairedEntry)
	}

	familyBySystemVersion := make(map[string]semantic.Family, len(allRows.families))
	for _, family := range allRows.families {
		key := family.System + "\x00" + family.Version
		if _, duplicate := familyBySystemVersion[key]; duplicate {
			t.Fatalf("duplicate category family for exact system/version %q/%q", family.System, family.Version)
		}
		familyBySystemVersion[key] = family
	}
	largeRef := findSemanticCategoryFamily(t, allRows.families, largeSystem, largeVersion)
	smallRef := findSemanticCategoryFamily(t, allRows.families, "urn:c01:small", "v1")
	fallbackRef := findSemanticCategoryFamily(t, allRows.families, fallbackSystem, "v1")
	findSemanticCategoryFamily(t, allRows.families, "urn:c01:other", "v1")
	findSemanticCategoryFamily(t, allRows.families, largeSystem, "2026-01")
	findSemanticCategoryFamily(t, allRows.families, largeSystem, "")
	if largeRef.Population != largeCount {
		t.Fatalf("restricted large family population = %d, want %d; scope-b row leaked", largeRef.Population, largeCount)
	}
	if familyHasExample(largeRef, "AAA scope-b private category") {
		t.Fatalf("restricted large family leaked scope-b example: %+v", largeRef.Examples)
	}
	if !familyHasExample(fallbackRef, "small-fallback-code") {
		t.Fatalf("blank Coding.display did not fall back to code in family examples: %+v", fallbackRef.Examples)
	}
	if !strings.HasSuffix(smallRef.KeySelector, "coding[]") || !strings.HasSuffix(smallRef.Value.Selector, "display") ||
		!smallRef.Value.FallbackOnBlankString || len(smallRef.Value.FallbackSelectors) != 1 ||
		!strings.HasSuffix(smallRef.Value.FallbackSelectors[0], "code") {
		t.Fatalf("category display/code fallback descriptor = %+v", smallRef)
	}

	resolved, err := store.ResolveSemanticInventorySelections(ctx, semantic.ResolveOptions{
		Project:                       project,
		DatasetGeneration:             generation,
		AuthResourcePathsUnrestricted: &unrestricted,
		AuthResourcePaths:             []string{"scope-a"},
		FamilyReferences:              []semantic.FamilyReference{smallRef.FamilyReference},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Entries) != 0 || len(resolved.Families) != 1 {
		t.Fatalf("small family resolution returned %d exact entries and %d families", len(resolved.Entries), len(resolved.Families))
	}
	resolvedSmall := resolved.Families[0]
	if resolvedSmall.FamilyID != smallRef.FamilyID || resolvedSmall.Population != 2 {
		t.Fatalf("resolved small category family = %+v; want both matching Coding rows", resolvedSmall)
	}

	assertSemanticCategoryExamples(t, resolvedSmall, []string{smallDisplays[0]})
	resolvedFallback, err := store.ResolveSemanticInventorySelections(ctx, semantic.ResolveOptions{
		Project:                       project,
		DatasetGeneration:             generation,
		AuthResourcePathsUnrestricted: &unrestricted,
		AuthResourcePaths:             []string{"scope-a"},
		FamilyReferences:              []semantic.FamilyReference{fallbackRef.FamilyReference},
	})
	if err != nil || len(resolvedFallback.Families) != 1 || resolvedFallback.Families[0].Population != 1 {
		t.Fatalf("blank-display fallback family resolution = %+v err=%v; want one matching Coding", resolvedFallback, err)
	}
	assertSemanticCategoryExamples(t, resolvedFallback.Families[0], []string{"small-fallback-code"})

	resolvedLarge, err := store.ResolveSemanticInventorySelections(ctx, semantic.ResolveOptions{
		Project:                       project,
		DatasetGeneration:             generation,
		AuthResourcePathsUnrestricted: &unrestricted,
		AuthResourcePaths:             []string{"scope-a"},
		FamilyReferences:              []semantic.FamilyReference{largeRef.FamilyReference},
	})
	if err != nil || len(resolvedLarge.Families) != 1 || resolvedLarge.Families[0].Population != largeCount {
		t.Fatalf("restricted large family resolution = %+v err=%v; want all %d scope-a rows", resolvedLarge, err, largeCount)
	}
	if familyHasExample(resolvedLarge.Families[0], "AAA scope-b private category") {
		t.Fatalf("restricted large family resolution leaked scope-b example: %+v", resolvedLarge.Families[0].Examples)
	}
}

type semanticCategoryPageRows struct {
	families []semantic.Family
	entries  []semantic.Entry
}

func collectSemanticCategoryPages(t *testing.T, ctx context.Context, store *Store, options semantic.PageOptions) semanticCategoryPageRows {
	t.Helper()
	rows := semanticCategoryPageRows{
		families: make([]semantic.Family, 0),
		entries:  make([]semantic.Entry, 0),
	}
	options.Cursor = ""
	for {
		page, err := store.PageSemanticInventory(ctx, options)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Families)+len(page.Entries) > options.Limit {
			t.Fatalf("category page has %d families and %d exact rows, exceeding combined limit %d", len(page.Families), len(page.Entries), options.Limit)
		}
		rows.families = append(rows.families, page.Families...)
		rows.entries = append(rows.entries, page.Entries...)
		if page.NextCursor == "" {
			return rows
		}
		options.Cursor = page.NextCursor
	}
}

func findSemanticCategoryFamily(t *testing.T, families []semantic.Family, system, version string) semantic.Family {
	t.Helper()
	for _, family := range families {
		if family.System == system && family.Version == version {
			return family
		}
	}
	t.Fatalf("missing category family for system %q version %q in %#v", system, version, families)
	return semantic.Family{}
}

func familyHasExample(family semantic.Family, want string) bool {
	for _, example := range family.Examples {
		if example == want {
			return true
		}
	}
	return false
}

func assertSemanticCategoryExamples(t *testing.T, family semantic.Family, want []string) {
	t.Helper()
	got := append([]string(nil), family.Examples...)
	expected := append([]string(nil), want...)
	sort.Strings(got)
	sort.Strings(expected)
	if !equalStringSlices(got, expected) {
		t.Fatalf("family examples = %#v, want selected Coding display/code values %#v", got, expected)
	}
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func deleteSemanticCategoryFixtureRows(ctx context.Context, client *arangostore.Client, collection, project, generation string) error {
	exists, err := client.CollectionExists(ctx, collection)
	if err != nil || !exists {
		return err
	}
	return client.ExecuteAQL(ctx, `
FOR document IN @@collection
  FILTER document.project == @project
  FILTER document.dataset_generation == @dataset_generation
  REMOVE document IN @@collection`, map[string]interface{}{
		"@collection":        collection,
		"project":            project,
		"dataset_generation": generation,
	})
}

func TestSemanticInventoryCategoryPageLimitsMixedSelections(t *testing.T) {
	build := semantic.NewBuild("project", "generation", "")
	build.State = semantic.Complete
	build.EntryIndexVersion = semantic.EntryIndexVersion
	family := categoryTestFamily()
	measurement := semantic.Entry{
		BindingID: "measurement-binding", ConceptID: "measurement-concept",
		Observation: semantic.Observation{
			RuleHint: semantic.RuleHintCodedValueV1,
			Key:      semantic.Key{System: "urn:measure", Code: "glucose"},
			Value:    semantic.Value{Selector: "valueQuantity.value", Type: "number"},
		},
	}
	const selectionID = `family:["Specimen","type.coding[]","urn:specimen",""]`
	familyRow := map[string]any{"binding_id": family.BindingID, "selection_id": selectionID, "family": testkit.JSONMap(t, family)}
	for _, firstFamily := range []bool{true, false} {
		rows := []map[string]any{familyRow, testkit.JSONMap(t, measurement)}
		if !firstFamily {
			rows[0], rows[1] = rows[1], rows[0]
		}
		client := &evidenceClient{rows: map[string][]map[string]any{
			semanticInventoryBuildsAQL: {testkit.JSONMap(t, build)}, semanticInventoryCategoryPageAQL: rows,
		}}
		adapter, err := New(client)
		if err != nil {
			t.Fatal(err)
		}
		opts := semantic.PageOptions{
			Project: "project", DatasetGeneration: "generation", Limit: 1,
			SelectionMode:                 semantic.SystemVersionCategory,
			AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(false),
			AuthResourcePaths:             []string{"scope-b", "scope-a", "scope-a"},
		}
		page, err := adapter.PageSemanticInventory(context.Background(), opts)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Entries)+len(page.Families) != 1 || page.NextCursor == "" {
			t.Fatalf("mixed page did not retain one selection and a cursor: %#v", page)
		}
		bindingID, selectedID, err := semantic.DecodeCursor(page.NextCursor, opts, build.BuildID)
		if err != nil {
			t.Fatal(err)
		}
		if firstFamily {
			if len(page.Families) != 1 || !reflect.DeepEqual(page.Families[0], family) || bindingID != family.BindingID || selectedID != selectionID {
				t.Fatalf("category page or cursor lost family: %#v, %q/%q", page, bindingID, selectedID)
			}
		} else if len(page.Entries) != 1 || !reflect.DeepEqual(page.Entries[0], measurement) || bindingID != measurement.BindingID || selectedID != measurement.ConceptID {
			t.Fatalf("category page changed the paired measurement: %#v, %q/%q", page, bindingID, selectedID)
		}
		vars := client.vars[1]
		if client.queries[1] != semanticInventoryCategoryPageAQL || vars["auth_resource_paths_unrestricted"] != false || !reflect.DeepEqual(vars["auth_resource_paths"], []string{"scope-a", "scope-b"}) {
			t.Fatalf("category query lost mode or authorization: %#v", vars)
		}
	}
}

func TestResolveSemanticInventoryFamilyUsesCurrentInventoryScope(t *testing.T) {
	for _, indexed := range []bool{true, false} {
		build := semantic.NewBuild("project", "generation", "")
		build.State = semantic.Complete
		collection := semantic.Collection
		if indexed {
			build.EntryIndexVersion = semantic.EntryIndexVersion
			collection = semantic.EntryCollection
		}
		family := categoryTestFamily()
		client := &evidenceClient{rows: map[string][]map[string]any{
			semanticInventoryBuildByKeyAQL:      {testkit.JSONMap(t, build)},
			semanticInventoryResolveFamiliesAQL: {testkit.JSONMap(t, family)},
		}}
		adapter, err := New(client)
		if err != nil {
			t.Fatal(err)
		}
		opts := semantic.ResolveOptions{
			Project: "project", DatasetGeneration: "generation",
			AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(false),
			AuthResourcePaths:             []string{"scope-a"},
			FamilyReferences:              []semantic.FamilyReference{family.FamilyReference},
		}
		result, err := adapter.ResolveSemanticInventorySelections(context.Background(), opts)
		if !indexed {
			if err != ErrSemanticInventoryCategoryIndexUnavailable || len(result.Families) != 0 ||
				!reflect.DeepEqual(client.queries, []string{semanticInventoryBuildByKeyAQL}) ||
				!reflect.DeepEqual(client.collectionChecks, []string{semantic.BuildCollection}) {
				t.Fatalf("unindexed family resolution did not fail before accessing inventory: %#v, %v, queries %#v, collections %#v", result, err, client.queries, client.collectionChecks)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Entries) != 0 || len(result.Families) != 1 || !reflect.DeepEqual(result.Families[0], family) {
			t.Fatalf("typed family resolution changed: %#v", result)
		}
		if len(client.queries) != 2 || client.queries[1] != semanticInventoryResolveFamiliesAQL {
			t.Fatalf("family resolution queried beyond build and inventory: %#v", client.queries)
		}
		vars := client.vars[1]
		if vars["@inventory"] != collection || vars["build_id"] != build.BuildID || vars["project"] != opts.Project || vars["dataset_generation"] != opts.DatasetGeneration ||
			vars["auth_resource_paths_unrestricted"] != false || !reflect.DeepEqual(vars["auth_resource_paths"], opts.AuthResourcePaths) || !reflect.DeepEqual(vars["references"], opts.FamilyReferences) {
			t.Fatalf("family resolver lost immutable scope: %#v", vars)
		}
		client.rows[semanticInventoryResolveFamiliesAQL] = nil
		result, err = adapter.ResolveSemanticInventorySelections(context.Background(), opts)
		if err != nil || len(result.Families) != 0 {
			t.Fatalf("missing authorized family did not stay unresolved: %#v, %v", result, err)
		}
	}
}

func TestSemanticInventoryCategoryPageRequiresEntryIndexAndPreservesExactFallback(t *testing.T) {
	build := semantic.NewBuild("project", "generation", "")
	build.State = semantic.Complete
	entry := semantic.Entry{BindingID: "binding", ConceptID: "concept"}
	for _, mode := range []semantic.SelectionMode{"", semantic.ExactConcept, semantic.SystemVersionCategory} {
		client := &evidenceClient{rows: map[string][]map[string]any{
			semanticInventoryBuildsAQL: {testkit.JSONMap(t, build)},
			semanticInventoryPageAQL:   {testkit.JSONMap(t, entry)},
		}}
		adapter, err := New(client)
		if err != nil {
			t.Fatal(err)
		}
		page, err := adapter.PageSemanticInventory(context.Background(), semantic.PageOptions{
			Project: "project", DatasetGeneration: "generation", SelectionMode: mode,
		})
		if mode == semantic.SystemVersionCategory {
			if err != ErrSemanticInventoryCategoryIndexUnavailable || len(page.Entries)+len(page.Families) != 0 ||
				!reflect.DeepEqual(client.queries, []string{semanticInventoryBuildsAQL}) ||
				!reflect.DeepEqual(client.collectionChecks, []string{semantic.BuildCollection}) {
				t.Fatalf("unindexed category page did not fail before accessing inventory: %#v, %v, queries %#v, collections %#v", page, err, client.queries, client.collectionChecks)
			}
			continue
		}
		if err != nil || len(page.Entries) != 1 || page.Entries[0].ConceptID != entry.ConceptID ||
			!reflect.DeepEqual(client.queries, []string{semanticInventoryBuildsAQL, semanticInventoryPageAQL}) ||
			client.vars[1]["@inventory"] != semantic.Collection {
			t.Fatalf("exact mode %q lost its legacy query and contribution fallback: %#v, %v, queries %#v", mode, page, err, client.queries)
		}
	}
}

func TestResolveSemanticInventoryMixedSelectionsRequireEntryIndex(t *testing.T) {
	build := semantic.NewBuild("project", "generation", "")
	build.State = semantic.Complete
	family := categoryTestFamily()
	client := &evidenceClient{rows: map[string][]map[string]any{
		semanticInventoryBuildByKeyAQL: {testkit.JSONMap(t, build)},
	}}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.ResolveSemanticInventorySelections(context.Background(), semantic.ResolveOptions{
		Project: "project", DatasetGeneration: "generation",
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(false),
		References:                    []semantic.Reference{{BindingID: "measurement-binding", ConceptID: "measurement-concept"}},
		FamilyReferences:              []semantic.FamilyReference{family.FamilyReference},
	})
	if err != ErrSemanticInventoryCategoryIndexUnavailable ||
		!reflect.DeepEqual(client.queries, []string{semanticInventoryBuildByKeyAQL}) ||
		!reflect.DeepEqual(client.collectionChecks, []string{semantic.BuildCollection}) {
		t.Fatalf("mixed category resolution reached contribution fallback: %v, queries %#v, collections %#v", err, client.queries, client.collectionChecks)
	}
}

func TestResolveSemanticInventoryFamilyRejectsTamperedReferences(t *testing.T) {
	family := categoryTestFamily()
	for name, change := range map[string]func(*semantic.FamilyReference){
		"version": func(ref *semantic.FamilyReference) { ref.Version = "v1" },
		"system":  func(ref *semantic.FamilyReference) { ref.System = "urn:other" },
		"source":  func(ref *semantic.FamilyReference) { ref.SourcePath = "category.coding[]" },
		"empty system": func(ref *semantic.FamilyReference) {
			ref.System = ""
			ref.FamilyID = semantic.FamilyID(*ref)
		},
		"unknown source kind": func(ref *semantic.FamilyReference) {
			ref.SourceKind = "unknown"
			ref.FamilyID = semantic.FamilyID(*ref)
		},
	} {
		t.Run(name, func(t *testing.T) {
			ref := family.FamilyReference
			change(&ref)
			client := &evidenceClient{}
			adapter, err := New(client)
			if err != nil {
				t.Fatal(err)
			}
			_, err = adapter.ResolveSemanticInventorySelections(context.Background(), semantic.ResolveOptions{
				Project: "project", DatasetGeneration: "generation",
				AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(false),
				FamilyReferences:              []semantic.FamilyReference{ref},
			})
			if err != ErrInvalidSemanticInventorySelection || len(client.queries) != 0 {
				t.Fatalf("invalid reference reached the inventory: %v, %#v", err, client.queries)
			}
		})
	}
}

func categoryTestFamily() semantic.Family {
	family := semantic.Family{
		FamilyReference: semantic.FamilyReference{
			BindingID: "binding", SourceKind: semantic.SourceFile,
			ResourceType: "Specimen", SourcePath: "type.coding[]", System: "urn:specimen", Version: "",
		},
		Source:      semantic.Source{Type: "Specimen", Path: "type.coding[]"},
		KeySelector: "type.coding[]",
		Value: semantic.Value{
			Selector: "type.coding[].display", Type: "string", FallbackSelectors: []string{"type.coding[].code"}, FallbackOnBlankString: true,
		},
		Population: 2, Examples: []string{"Normal"}, ExamplesTruncated: true,
	}
	family.FamilyID = semantic.FamilyID(family.FamilyReference)
	return family
}

func TestRefreshSemanticInventorySourceAvailabilityUsesExactCompletedBuildCAS(t *testing.T) {
	expected := semantic.NewBuild("project", "generation", "")
	expected.State = semantic.Complete
	expected.SourceKind = semantic.SourceRetained
	expected.SourceAvailability = semantic.SourceAvailabilityUnproven
	expected.ScannedResources = 17
	refreshed := expected
	refreshed.SourceAvailability = semantic.SourceAvailabilityVerified
	client := &evidenceClient{rows: map[string][]map[string]any{
		refreshSemanticInventorySourceAvailabilityAQL: {testkit.JSONMap(t, refreshed)},
	}}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}

	got, updated, err := adapter.RefreshSemanticInventorySourceAvailability(context.Background(), expected)
	if err != nil || !updated || got.SourceAvailability != semantic.SourceAvailabilityVerified {
		t.Fatalf("refreshed build = %+v updated=%t err=%v", got, updated, err)
	}
	if len(client.queries) != 1 || client.queries[0] != refreshSemanticInventorySourceAvailabilityAQL {
		t.Fatalf("queries = %v, want one source-availability CAS", client.queries)
	}
	vars := client.vars[0]
	if vars["key"] != expected.Key || vars["project"] != expected.Project || vars["dataset_generation"] != expected.DatasetGeneration || vars["build_id"] != expected.BuildID || vars["auth_resource_path"] != "" || vars["scanned_resources"] != int64(17) {
		t.Fatalf("CAS scope = %#v", vars)
	}
	for _, fragment := range []string{
		`FILTER d.state == "complete"`,
		`FILTER d.source_kind == "retained_vertex"`,
		`FILTER NOT_NULL(d.auth_resource_path, "") == @auth_resource_path`,
		`FILTER d.source_availability == "unproven"`,
		`FILTER LENGTH(NOT_NULL(d.diagnostics, [])) == 0`,
		`FILTER NOT_NULL(d.scanned_resources, 0) == @scanned_resources`,
	} {
		if !strings.Contains(client.queries[0], fragment) {
			t.Fatalf("CAS query missing %q: %s", fragment, client.queries[0])
		}
	}
}

func TestRefreshSemanticInventorySourceAvailabilityRejectsScopedBuild(t *testing.T) {
	expected := semantic.NewBuild("project", "generation", "scope-a")
	expected.State = semantic.Complete
	expected.SourceKind = semantic.SourceRetained
	expected.SourceAvailability = semantic.SourceAvailabilityUnproven
	client := &evidenceClient{rows: map[string][]map[string]any{}}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}

	got, updated, err := adapter.RefreshSemanticInventorySourceAvailability(context.Background(), expected)
	if err != nil || updated || got.SourceAvailability != semantic.SourceAvailabilityUnproven || len(client.queries) != 0 {
		t.Fatalf("scoped build CAS result = %+v updated=%t err=%v queries=%v", got, updated, err, client.queries)
	}
}

func TestRefreshSemanticInventorySourceAvailabilityLeavesConcurrentChangeAlone(t *testing.T) {
	expected := semantic.NewBuild("project", "generation", "")
	expected.State = semantic.Complete
	expected.SourceKind = semantic.SourceRetained
	expected.SourceAvailability = semantic.SourceAvailabilityUnproven
	client := &evidenceClient{rows: map[string][]map[string]any{}}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}

	got, updated, err := adapter.RefreshSemanticInventorySourceAvailability(context.Background(), expected)
	if err != nil || updated || got.SourceAvailability != semantic.SourceAvailabilityUnproven {
		t.Fatalf("concurrent CAS result = %+v updated=%t err=%v", got, updated, err)
	}
}

func TestRefreshSemanticInventorySourceAvailabilityRejectsDiagnosticBuildBeforeWrite(t *testing.T) {
	expected := semantic.NewBuild("project", "generation", "")
	expected.State = semantic.Complete
	expected.SourceKind = semantic.SourceRetained
	expected.SourceAvailability = semantic.SourceAvailabilityUnproven
	expected.Diagnostics = []string{"source scan diagnostic"}
	client := &evidenceClient{rows: map[string][]map[string]any{}}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}

	got, updated, err := adapter.RefreshSemanticInventorySourceAvailability(context.Background(), expected)
	if err != nil || updated || got.SourceAvailability != semantic.SourceAvailabilityUnproven || len(client.queries) != 0 {
		t.Fatalf("diagnostic build CAS result = %+v updated=%t err=%v queries=%v", got, updated, err, client.queries)
	}
}
