package arango

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/catalog"
)

func TestPageSemanticInventoryDoesNotAdvertiseOlderCompletedBuild(t *testing.T) {
	project, generation := "project", "generation"
	currentBuildID := catalog.SemanticInventoryBuildID(project, generation)
	olderBuild := catalog.NewSemanticInventoryBuild(project, generation, "")
	olderBuild.BuildID = "older-build"
	olderBuild.State = catalog.SemanticInventoryComplete
	olderBuild.EntryIndexVersion = catalog.SemanticInventoryEntryIndexVersion
	olderBuild.SourceAvailability = catalog.SemanticInventorySourceAvailabilityVerified
	staleEntry := catalog.SemanticInventoryEntry{
		ConceptID: "stale-concept",
		BindingID: "stale-binding",
		Observation: catalog.SemanticObservation{
			SchemaVersion: catalog.SemanticObservationSchemaVersion,
			Completeness:  catalog.SemanticComplete,
			Status:        "SUPPORTED",
		},
	}

	tests := []struct {
		name        string
		current     *catalog.SemanticInventoryBuild
		wantState   catalog.SemanticInventoryState
		wantBuildID string
	}{
		{name: "current build absent", wantState: catalog.SemanticInventoryUnknown},
		{name: "current build running", current: func() *catalog.SemanticInventoryBuild {
			build := catalog.NewSemanticInventoryBuild(project, generation, "")
			build.State = catalog.SemanticInventoryRunning
			return &build
		}(), wantState: catalog.SemanticInventoryRunning, wantBuildID: currentBuildID},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			builds := []map[string]any{semanticInventoryTestRow(t, olderBuild)}
			if test.current != nil {
				builds = append(builds, semanticInventoryTestRow(t, *test.current))
			}
			client := &evidenceClient{
				rows: map[string][]map[string]any{
					semanticInventoryBuildsAQL: builds,
					semanticInventoryPageAQL:   []map[string]any{semanticInventoryTestRow(t, staleEntry)},
				},
				collections: map[string]bool{
					catalog.SemanticInventoryBuildCollection: true,
					catalog.SemanticInventoryEntryCollection: true,
				},
			}
			adapter, err := New(client)
			if err != nil {
				t.Fatal(err)
			}
			page, err := adapter.PageSemanticInventory(context.Background(), catalog.SemanticInventoryPageOptions{
				Project: project, DatasetGeneration: generation,
			})
			if err != nil {
				t.Fatal(err)
			}
			if page.State != test.wantState || page.Build.BuildID != test.wantBuildID || len(page.Entries) != 0 {
				t.Fatalf("page = state %q build %q entries %d, want state %q build %q and no stale entries",
					page.State, page.Build.BuildID, len(page.Entries), test.wantState, test.wantBuildID)
			}
			if len(client.queries) != 1 || client.queries[0] != semanticInventoryBuildsAQL {
				t.Fatalf("queries = %v, want only the current-build lookup; stale entries must not be paged", client.queries)
			}
		})
	}
}
