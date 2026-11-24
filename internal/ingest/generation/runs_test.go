package generation

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/calypr/loom/internal/semantic"
)

func TestSemanticInventoryEntryRunsMergeRepeatedEntryGroups(t *testing.T) {
	project, generation, auth, resource := "project", "generation", "scope", "Observation"
	buildID := semantic.BuildID(project, generation)
	makeAggregator := func(example string, population int64) *semantic.EntryAggregator {
		aggregator := semantic.NewEntryAggregator(project, generation, auth, resource, semantic.SourceFile)
		observation := semantic.Observation{
			SchemaVersion: semantic.ObservationSchemaVersion,
			Source:        semantic.Source{Type: resource, Path: "value"},
			Key:           semantic.Key{System: "urn:test", Code: "same", Display: example},
			Completeness:  semantic.CompletenessComplete, Population: population,
		}
		if err := aggregator.Add(semantic.Contribution{
			Project: project, DatasetGeneration: generation, AuthResourcePath: auth, ResourceType: resource,
			BuildID: buildID, SourceKind: semantic.SourceFile, BindingID: "binding", ConceptID: "concept",
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
	var got []semantic.EntryPartial
	if err := mergeSemanticInventoryEntryRuns(context.Background(), []string{first, second}, func(value semantic.EntryPartial) error {
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
	buildID := semantic.BuildID(project, generation)
	makeRun := func(population int64) string {
		aggregator := semantic.NewEntryAggregator(project, generation, auth, resource, semantic.SourceFile)
		if err := aggregator.Add(semantic.Contribution{
			Project: project, DatasetGeneration: generation, AuthResourcePath: auth, ResourceType: resource,
			BuildID: buildID, SourceKind: semantic.SourceFile, BindingID: "binding", ConceptID: "concept",
			Example: "example", Observation: semantic.Observation{Population: population},
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
	var merged []semantic.EntryPartial
	if err := mergeSemanticInventoryEntryRuns(context.Background(), paths, func(value semantic.EntryPartial) error {
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
	err := mergeSemanticInventoryEntryRuns(ctx, paths, func(semantic.EntryPartial) error {
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

func TestSemanticInventoryEntryRunDecodeClearsOmittedFields(t *testing.T) {
	project, generation, auth, resource := "project", "generation", "scope", "Observation"
	buildID := semantic.BuildID(project, generation)
	withStatus := semantic.EntryPartial{Contribution: semantic.Contribution{
		Project: project, DatasetGeneration: generation, AuthResourcePath: auth, ResourceType: resource,
		BuildID: buildID, SourceKind: semantic.SourceFile, BindingID: "binding", ConceptID: "a",
		Observation: semantic.Observation{Status: "active"},
	}}
	withoutStatus := semantic.EntryPartial{Contribution: semantic.Contribution{
		Project: project, DatasetGeneration: generation, AuthResourcePath: auth, ResourceType: resource,
		BuildID: buildID, SourceKind: semantic.SourceFile, BindingID: "binding", ConceptID: "b",
	}}
	path, err := createSemanticInventoryEntryRun(func(emit func(semantic.EntryPartial) error) error {
		if err := emit(withStatus); err != nil {
			return err
		}
		return emit(withoutStatus)
	})
	if err != nil {
		t.Fatal(err)
	}
	var values []semantic.EntryPartial
	if err := mergeSemanticInventoryEntryRuns(t.Context(), []string{path}, func(value semantic.EntryPartial) error {
		values = append(values, value)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || values[0].Contribution.Observation.Status != "active" || values[1].Contribution.Observation.Status != "" {
		t.Fatalf("decoded run statuses = %+v, want active then empty", values)
	}
}
