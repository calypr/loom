package arango

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/catalog"
	arangostore "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestSemanticInventoryPagesAndResolvesCodedCategoryFamilies(t *testing.T) {
	if os.Getenv("LOOM_C01_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_C01_ARANGO_INTEGRATION=1 to run coded category Arango proof")
	}
	endpoint := strings.TrimSpace(os.Getenv("LOOM_C01_ARANGO_URL"))
	database := strings.TrimSpace(os.Getenv("LOOM_C01_ARANGO_DATABASE"))
	if endpoint == "" || database == "" {
		t.Fatal("LOOM_C01_ARANGO_URL and an isolated LOOM_C01_ARANGO_DATABASE are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client, err := arangostore.Open(ctx, endpoint, database)
	if err != nil {
		t.Fatal(err)
	}
	project := "c01-coded-category-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	generation := strings.ReplaceAll(uuid.NewString(), "-", "")
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		for _, collection := range []string{
			catalog.SemanticInventoryCollection,
			catalog.SemanticInventoryEntryCollection,
			catalog.SemanticInventoryBuildCollection,
		} {
			if err := deleteSemanticCategoryFixtureRows(cleanupCtx, client, collection, project, generation); err != nil {
				t.Errorf("clean scoped %s fixture rows: %v", collection, err)
			}
		}
		if err := client.Close(cleanupCtx); err != nil {
			t.Errorf("close Arango client: %v", err)
		}
	})

	store, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareSemanticInventoryBackfill(ctx); err != nil {
		t.Fatal(err)
	}
	build := catalog.NewSemanticInventoryBuild(project, generation, "")
	build.SourceKind = catalog.SemanticInventorySourceRetained
	build.SourceAvailability = catalog.SemanticInventorySourceAvailabilityVerified
	if err := store.BeginSemanticInventoryBuild(ctx, build); err != nil {
		t.Fatal(err)
	}

	emitter := catalog.NewSemanticInventoryEmitter(project, generation)
	contributions := make([]catalog.SemanticInventoryContribution, 0, 64)
	addCategoryCode := func(scope, code, display, system, version string) {
		t.Helper()
		payload := map[string]any{
			"resourceType": "Observation",
			"id":           strings.ReplaceAll(uuid.NewString(), "-", ""),
			"category": []any{map[string]any{"coding": []any{map[string]any{
				"system": system, "version": version, "code": code, "display": display,
			}}}},
		}
		observed := make([]catalog.SemanticInventoryContribution, 0, 1)
		emitter.ObservePayload(payload, "Observation", scope, "retained:Observation/"+payload["id"].(string), func(contribution catalog.SemanticInventoryContribution) {
			observed = append(observed, contribution)
		})
		if len(observed) != 1 || observed[0].Observation.RuleHint != catalog.SemanticRuleHintCodedConceptV1 {
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
	// This code sorts after the ordinary members and is matched by code because
	// its display is absent. It must still select the complete family.
	lateCodeOnlyWitness := "zz-late-code-only-witness"
	addCategoryCode("scope-a", lateCodeOnlyWitness, "", largeSystem, largeVersion)
	addCategoryCode("scope-b", "scope-b-private", "AAA scope-b private category", largeSystem, largeVersion)

	smallDisplays := []string{"Small category alpha", "Small category beta"}
	addCategoryCode("scope-a", "small-alpha", smallDisplays[0], "urn:c01:small", "v1")
	addCategoryCode("scope-a", "small-beta", smallDisplays[1], "urn:c01:small", "v1")
	// Keep the fallback in a one-code family so its representative example is
	// deterministic while still exercising indexed family aggregation.
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
	paired := make([]catalog.SemanticInventoryContribution, 0, 1)
	emitter.ObservePayload(pairedPayload, "Observation", "scope-a", "retained:Observation/"+pairedPayload["id"].(string), func(contribution catalog.SemanticInventoryContribution) {
		paired = append(paired, contribution)
	})
	if len(paired) != 1 || paired[0].Observation.RuleHint != catalog.SemanticRuleHintCodedValueV1 || paired[0].Observation.Key.Code != "paired-exact-code" {
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
	options := catalog.SemanticInventoryPageOptions{
		Project:                       project,
		DatasetGeneration:             generation,
		AuthResourcePathsUnrestricted: &unrestricted,
		AuthResourcePaths:             []string{"scope-a"},
		SelectionMode:                 catalog.SemanticInventorySystemVersionCategory,
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
	if largeFamily.FamilyID == "" || largeFamily.FamilyID != catalog.SemanticInventoryFamilyID(largeFamily.SemanticInventoryFamilyReference) {
		t.Fatalf("large family identity = %+v; want stable content-addressed family id", largeFamily.SemanticInventoryFamilyReference)
	}

	allRows := collectSemanticCategoryPages(t, ctx, store, options)
	if len(allRows.families) != 6 {
		t.Fatalf("scope-a category families = %d, want large, small, fallback, other-system, other-version, and unversioned: %#v", len(allRows.families), allRows.families)
	}
	if len(allRows.entries) != 1 {
		t.Fatalf("category-mode exact entries = %d, want the paired CODED_VALUE row", len(allRows.entries))
	}
	pairedEntry := allRows.entries[0]
	if pairedEntry.Observation.RuleHint != catalog.SemanticRuleHintCodedValueV1 ||
		pairedEntry.Observation.Key.Code != "paired-exact-code" || pairedEntry.Observation.Key.System != "urn:c01:paired" ||
		pairedEntry.Observation.Key.Version != "v7" {
		t.Fatalf("category mode changed or grouped the paired CODED_VALUE row: %+v", pairedEntry)
	}

	familyBySystemVersion := make(map[string]catalog.SemanticInventoryFamily, len(allRows.families))
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

	resolved, err := store.ResolveSemanticInventorySelections(ctx, catalog.SemanticInventoryResolveOptions{
		Project:                       project,
		DatasetGeneration:             generation,
		AuthResourcePathsUnrestricted: &unrestricted,
		AuthResourcePaths:             []string{"scope-a"},
		FamilyReferences:              []catalog.SemanticInventoryFamilyReference{smallRef.SemanticInventoryFamilyReference},
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
	// Family DTOs intentionally carry one deterministic representative example;
	// the population and descriptor cover the full system/version family.
	assertSemanticCategoryExamples(t, resolvedSmall, []string{smallDisplays[0]})
	resolvedFallback, err := store.ResolveSemanticInventorySelections(ctx, catalog.SemanticInventoryResolveOptions{
		Project:                       project,
		DatasetGeneration:             generation,
		AuthResourcePathsUnrestricted: &unrestricted,
		AuthResourcePaths:             []string{"scope-a"},
		FamilyReferences:              []catalog.SemanticInventoryFamilyReference{fallbackRef.SemanticInventoryFamilyReference},
	})
	if err != nil || len(resolvedFallback.Families) != 1 || resolvedFallback.Families[0].Population != 1 {
		t.Fatalf("blank-display fallback family resolution = %+v err=%v; want one matching Coding", resolvedFallback, err)
	}
	assertSemanticCategoryExamples(t, resolvedFallback.Families[0], []string{"small-fallback-code"})

	resolvedLarge, err := store.ResolveSemanticInventorySelections(ctx, catalog.SemanticInventoryResolveOptions{
		Project:                       project,
		DatasetGeneration:             generation,
		AuthResourcePathsUnrestricted: &unrestricted,
		AuthResourcePaths:             []string{"scope-a"},
		FamilyReferences:              []catalog.SemanticInventoryFamilyReference{largeRef.SemanticInventoryFamilyReference},
	})
	if err != nil || len(resolvedLarge.Families) != 1 || resolvedLarge.Families[0].Population != largeCount {
		t.Fatalf("restricted large family resolution = %+v err=%v; want all %d scope-a rows", resolvedLarge, err, largeCount)
	}
	if familyHasExample(resolvedLarge.Families[0], "AAA scope-b private category") {
		t.Fatalf("restricted large family resolution leaked scope-b example: %+v", resolvedLarge.Families[0].Examples)
	}
}

type semanticCategoryPageRows struct {
	families []catalog.SemanticInventoryFamily
	entries  []catalog.SemanticInventoryEntry
}

func collectSemanticCategoryPages(t *testing.T, ctx context.Context, store *Store, options catalog.SemanticInventoryPageOptions) semanticCategoryPageRows {
	t.Helper()
	rows := semanticCategoryPageRows{
		families: make([]catalog.SemanticInventoryFamily, 0),
		entries:  make([]catalog.SemanticInventoryEntry, 0),
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

func findSemanticCategoryFamily(t *testing.T, families []catalog.SemanticInventoryFamily, system, version string) catalog.SemanticInventoryFamily {
	t.Helper()
	for _, family := range families {
		if family.System == system && family.Version == version {
			return family
		}
	}
	t.Fatalf("missing category family for system %q version %q in %#v", system, version, families)
	return catalog.SemanticInventoryFamily{}
}

func familyHasExample(family catalog.SemanticInventoryFamily, want string) bool {
	for _, example := range family.Examples {
		if example == want {
			return true
		}
	}
	return false
}

func assertSemanticCategoryExamples(t *testing.T, family catalog.SemanticInventoryFamily, want []string) {
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
