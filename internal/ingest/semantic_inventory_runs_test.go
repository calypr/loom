package ingest

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/catalog"
)

func TestSemanticInventoryRunMergeIsWorkerIndependent(t *testing.T) {
	project := "project"
	generation := "generation"
	buildID := catalog.SemanticInventoryBuildID(project, generation)
	makeAggregator := func() *catalog.SemanticInventoryAggregator {
		return catalog.NewSemanticInventoryAggregator(project, generation, "scope", "Observation", "META/Observation.ndjson")
	}
	first := makeAggregator()
	second := makeAggregator()
	if err := first.Add(semanticInventoryRunFixture(project, generation, buildID, "a", 2)); err != nil {
		t.Fatal(err)
	}
	if err := second.Add(semanticInventoryRunFixture(project, generation, buildID, "a", 3)); err != nil {
		t.Fatal(err)
	}
	if err := second.Add(semanticInventoryRunFixture(project, generation, buildID, "Z", 1)); err != nil {
		t.Fatal(err)
	}
	firstPath, err := writeSemanticInventoryRun(first)
	if err != nil {
		t.Fatal(err)
	}
	secondPath, err := writeSemanticInventoryRun(second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Remove(firstPath)
		os.Remove(secondPath)
	})
	var merged []catalog.SemanticInventoryContribution
	if err := mergeSemanticInventoryRuns(t.Context(), []string{secondPath, firstPath}, func(value catalog.SemanticInventoryContribution) error {
		merged = append(merged, value)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(merged) != 2 || merged[0].Observation.Population != 1 || merged[1].Observation.Population != 5 {
		t.Fatalf("merged spill values = %+v", merged)
	}
	encoded, err := encodeSemanticInventoryBatch(merged, project, generation, "scope", "Observation", buildID, "META/Observation.ndjson", 0)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := encodeSemanticInventoryBatch(merged, project, generation, "scope", "Observation", buildID, "META/Observation.ndjson", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(encoded, retry) {
		t.Fatal("spill merge changed retry keys")
	}
}

func TestSemanticInventoryEntryRunsMergeRepeatedEntryGroups(t *testing.T) {
	project, generation, auth, resource := "project", "generation", "scope", "Observation"
	buildID := catalog.SemanticInventoryBuildID(project, generation)
	makeAggregator := func(example string, population int64) *catalog.SemanticInventoryEntryAggregator {
		aggregator := catalog.NewSemanticInventoryEntryAggregator(project, generation, auth, resource, catalog.SemanticInventorySourceFile)
		observation := catalog.SemanticObservation{
			SchemaVersion: catalog.SemanticObservationSchemaVersion,
			Source:        catalog.SemanticObservationSource{Type: resource, Path: "value"},
			Key:           catalog.SemanticObservationKey{System: "urn:test", Code: "same", Display: example},
			Completeness:  catalog.SemanticComplete, Population: population,
		}
		if err := aggregator.Add(catalog.SemanticInventoryContribution{
			Project: project, DatasetGeneration: generation, AuthResourcePath: auth, ResourceType: resource,
			BuildID: buildID, SourceKind: catalog.SemanticInventorySourceFile, BindingID: "binding", ConceptID: "concept",
			Example: example, Observation: observation,
		}); err != nil {
			t.Fatal(err)
		}
		return aggregator
	}
	first, err := writeSemanticInventoryEntryRun(makeAggregator("Zulu", 2))
	if err != nil {
		t.Fatal(err)
	}
	second, err := writeSemanticInventoryEntryRun(makeAggregator("Alpha", 3))
	if err != nil {
		t.Fatal(err)
	}
	var got []catalog.SemanticInventoryEntryPartial
	if err := mergeSemanticInventoryEntryRuns(context.Background(), []string{first, second}, func(value catalog.SemanticInventoryEntryPartial) error {
		got = append(got, value)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Contribution.Observation.Population != 5 || got[0].Contribution.Example != "Alpha" {
		t.Fatalf("merged entry partials = %+v", got)
	}
}

func TestSemanticInventoryEntryRunsUseBoundedFanInAndCleanFiles(t *testing.T) {
	project, generation, auth, resource := "project", "generation", "scope", "Observation"
	buildID := catalog.SemanticInventoryBuildID(project, generation)
	makeRun := func(population int64) string {
		aggregator := catalog.NewSemanticInventoryEntryAggregator(project, generation, auth, resource, catalog.SemanticInventorySourceFile)
		if err := aggregator.Add(catalog.SemanticInventoryContribution{
			Project: project, DatasetGeneration: generation, AuthResourcePath: auth, ResourceType: resource,
			BuildID: buildID, SourceKind: catalog.SemanticInventorySourceFile, BindingID: "binding", ConceptID: "concept",
			Example: "example", Observation: catalog.SemanticObservation{Population: population},
		}); err != nil {
			t.Fatal(err)
		}
		path, err := writeSemanticInventoryEntryRun(aggregator)
		if err != nil {
			t.Fatal(err)
		}
		return path
	}
	pathCount := semanticInventoryRunFanIn * 5
	paths := make([]string, 0, pathCount)
	for i := 0; i < pathCount; i++ {
		paths = append(paths, makeRun(1))
	}
	var merged []catalog.SemanticInventoryEntryPartial
	if err := mergeSemanticInventoryEntryRuns(context.Background(), paths, func(value catalog.SemanticInventoryEntryPartial) error {
		merged = append(merged, value)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(merged) != 1 || merged[0].Contribution.Observation.Population != int64(pathCount) {
		t.Fatalf("bounded merge result = %+v, want one group with population %d", merged, pathCount)
	}
	assertInventoryRunFilesRemoved(t, paths)

	paths = paths[:0]
	for i := 0; i < pathCount; i++ {
		paths = append(paths, makeRun(1))
	}
	ctx, cancel := context.WithCancel(context.Background())
	err := mergeSemanticInventoryEntryRuns(ctx, paths, func(catalog.SemanticInventoryEntryPartial) error {
		cancel()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled bounded merge error = %v, want context canceled", err)
	}
	assertInventoryRunFilesRemoved(t, paths)
}

func assertInventoryRunFilesRemoved(t *testing.T, paths []string) {
	t.Helper()
	for _, path := range paths {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("inventory run %q remains after merge, stat error = %v", path, err)
		}
	}
}

func TestSemanticInventoryRunDecodeClearsOmittedFields(t *testing.T) {
	project := "project"
	generation := "generation"
	buildID := catalog.SemanticInventoryBuildID(project, generation)
	aggregator := catalog.NewSemanticInventoryAggregator(project, generation, "scope", "Observation", "META/Observation.ndjson")
	withStatus := semanticInventoryRunFixture(project, generation, buildID, "a", 1)
	withStatus.Observation.Status = "active"
	withoutStatus := semanticInventoryRunFixture(project, generation, buildID, "b", 1)
	for _, contribution := range []catalog.SemanticInventoryContribution{withStatus, withoutStatus} {
		if err := aggregator.Add(contribution); err != nil {
			t.Fatal(err)
		}
	}
	path, err := writeSemanticInventoryRun(aggregator)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(path) })
	var values []catalog.SemanticInventoryContribution
	if err := mergeSemanticInventoryRuns(t.Context(), []string{path}, func(value catalog.SemanticInventoryContribution) error {
		values = append(values, value)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || values[0].Observation.Status != "active" || values[1].Observation.Status != "" {
		t.Fatalf("decoded run statuses = %+v, want active then empty", values)
	}
}

func semanticInventoryRunFixture(project, generation, buildID, display string, population int64) catalog.SemanticInventoryContribution {
	return catalog.SemanticInventoryContribution{
		Project: project, DatasetGeneration: generation, AuthResourcePath: "scope", ResourceType: "Observation",
		BuildID: buildID, SourceKind: catalog.SemanticInventorySourceFile, SourceID: "META/Observation.ndjson#1",
		ConceptID: "concept", BindingID: "binding", Example: display,
		Observation: catalog.SemanticObservation{
			SchemaVersion: 4,
			Source:        catalog.SemanticObservationSource{Type: "Observation", Path: "path"},
			Key:           catalog.SemanticObservationKey{Code: "code", Display: display},
			Value:         catalog.SemanticObservationValue{Type: "string"},
			Completeness:  catalog.SemanticComplete, Population: population, Examples: []string{display},
		},
	}
}

func TestSemanticInventoryRunMergeAcrossBoundedGroups(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	runCount := semanticInventoryRunFanIn*semanticInventoryRunFanIn + 1
	paths := make([]string, 0, runCount)
	for range runCount {
		path, err := createSemanticInventoryRun(func(emit func(catalog.SemanticInventoryContribution) error) error {
			return emit(semanticInventoryRunFixture("project", "generation", "build", "same", 1))
		})
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	var merged []catalog.SemanticInventoryContribution
	err := mergeSemanticInventoryRuns(t.Context(), paths, func(value catalog.SemanticInventoryContribution) error {
		merged = append(merged, value)
		return nil
	})
	if err != nil || len(merged) != 1 || merged[0].Observation.Population != int64(runCount) {
		t.Fatalf("bounded merge = %+v, err = %v, want one row with population %d", merged, err, runCount)
	}
	files, err := os.ReadDir(os.TempDir())
	if err != nil || len(files) != runCount {
		t.Fatalf("spill recovery retained %d files, want %d original runs, err = %v", len(files), runCount, err)
	}
}

func TestSemanticInventoryRunPreservesOmittedSliceIdentity(t *testing.T) {
	before := semanticInventoryRunFixture("project", "generation", "build", "same", 1)
	before.Observation.Value.FallbackSelectors = []string{}
	before.Observation.ObservedUnits = []string{}
	before.Observation.ExtensionURLPath = []string{}
	path, err := createSemanticInventoryRun(func(emit func(catalog.SemanticInventoryContribution) error) error {
		return emit(before)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	err = mergeSemanticInventoryRuns(t.Context(), []string{path}, func(after catalog.SemanticInventoryContribution) error {
		if catalog.SemanticInventoryContributionOrderKey(before) != catalog.SemanticInventoryContributionOrderKey(after) {
			t.Fatal("spill round trip changed the ordering key for omitted slices")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
