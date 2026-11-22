package generation

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/catalog/codedconcept"
	"github.com/calypr/loom/internal/catalog/semanticinventory"
)

func TestSemanticInventoryRunMergeIsWorkerIndependent(t *testing.T) {
	project := "project"
	generation := "generation"
	buildID := semanticinventory.BuildID(project, generation)
	makeAggregator := func() *semanticinventory.Aggregator {
		return semanticinventory.NewAggregator(project, generation, "scope", "Observation", "META/Observation.ndjson")
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
	var merged []semanticinventory.Contribution
	if err := mergeSemanticInventoryRuns(t.Context(), []string{secondPath, firstPath}, func(value semanticinventory.Contribution) error {
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
	buildID := semanticinventory.BuildID(project, generation)
	makeAggregator := func(example string, population int64) *semanticinventory.EntryAggregator {
		aggregator := semanticinventory.NewEntryAggregator(project, generation, auth, resource, semanticinventory.SourceFile)
		observation := codedconcept.Observation{
			SchemaVersion: codedconcept.ObservationSchemaVersion,
			Source:        codedconcept.Source{Type: resource, Path: "value"},
			Key:           codedconcept.Key{System: "urn:test", Code: "same", Display: example},
			Completeness:  codedconcept.Complete, Population: population,
		}
		if err := aggregator.Add(semanticinventory.Contribution{
			Project: project, DatasetGeneration: generation, AuthResourcePath: auth, ResourceType: resource,
			BuildID: buildID, SourceKind: semanticinventory.SourceFile, BindingID: "binding", ConceptID: "concept",
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
	var got []semanticinventory.EntryPartial
	if err := mergeSemanticInventoryEntryRuns(context.Background(), []string{first, second}, func(value semanticinventory.EntryPartial) error {
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
	buildID := semanticinventory.BuildID(project, generation)
	makeRun := func(population int64) string {
		aggregator := semanticinventory.NewEntryAggregator(project, generation, auth, resource, semanticinventory.SourceFile)
		if err := aggregator.Add(semanticinventory.Contribution{
			Project: project, DatasetGeneration: generation, AuthResourcePath: auth, ResourceType: resource,
			BuildID: buildID, SourceKind: semanticinventory.SourceFile, BindingID: "binding", ConceptID: "concept",
			Example: "example", Observation: codedconcept.Observation{Population: population},
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
	var merged []semanticinventory.EntryPartial
	if err := mergeSemanticInventoryEntryRuns(context.Background(), paths, func(value semanticinventory.EntryPartial) error {
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
	err := mergeSemanticInventoryEntryRuns(ctx, paths, func(semanticinventory.EntryPartial) error {
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
	buildID := semanticinventory.BuildID(project, generation)
	aggregator := semanticinventory.NewAggregator(project, generation, "scope", "Observation", "META/Observation.ndjson")
	withStatus := semanticInventoryRunFixture(project, generation, buildID, "a", 1)
	withStatus.Observation.Status = "active"
	withoutStatus := semanticInventoryRunFixture(project, generation, buildID, "b", 1)
	for _, contribution := range []semanticinventory.Contribution{withStatus, withoutStatus} {
		if err := aggregator.Add(contribution); err != nil {
			t.Fatal(err)
		}
	}
	path, err := writeSemanticInventoryRun(aggregator)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(path) })
	var values []semanticinventory.Contribution
	if err := mergeSemanticInventoryRuns(t.Context(), []string{path}, func(value semanticinventory.Contribution) error {
		values = append(values, value)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || values[0].Observation.Status != "active" || values[1].Observation.Status != "" {
		t.Fatalf("decoded run statuses = %+v, want active then empty", values)
	}
}

func semanticInventoryRunFixture(project, generation, buildID, display string, population int64) semanticinventory.Contribution {
	return semanticinventory.Contribution{
		Project: project, DatasetGeneration: generation, AuthResourcePath: "scope", ResourceType: "Observation",
		BuildID: buildID, SourceKind: semanticinventory.SourceFile, SourceID: "META/Observation.ndjson#1",
		ConceptID: "concept", BindingID: "binding", Example: display,
		Observation: codedconcept.Observation{
			SchemaVersion: 4,
			Source:        codedconcept.Source{Type: "Observation", Path: "path"},
			Key:           codedconcept.Key{Code: "code", Display: display},
			Value:         codedconcept.Value{Type: "string"},
			Completeness:  codedconcept.Complete, Population: population, Examples: []string{display},
		},
	}
}

func TestSemanticInventoryRunMergeAcrossBoundedGroups(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	runCount := semanticInventoryRunFanIn*semanticInventoryRunFanIn + 1
	paths := make([]string, 0, runCount)
	for range runCount {
		path, err := createSemanticInventoryRun(func(emit func(semanticinventory.Contribution) error) error {
			return emit(semanticInventoryRunFixture("project", "generation", "build", "same", 1))
		})
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	var merged []semanticinventory.Contribution
	err := mergeSemanticInventoryRuns(t.Context(), paths, func(value semanticinventory.Contribution) error {
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
	path, err := createSemanticInventoryRun(func(emit func(semanticinventory.Contribution) error) error {
		return emit(before)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	err = mergeSemanticInventoryRuns(t.Context(), []string{path}, func(after semanticinventory.Contribution) error {
		if semanticinventory.ContributionOrderKey(before) != semanticinventory.ContributionOrderKey(after) {
			t.Fatal("spill round trip changed the ordering key for omitted slices")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
