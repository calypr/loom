package arango

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/semantic"
)

func TestPageSemanticInventoryDoesNotAdvertiseOlderCompletedBuild(t *testing.T) {
	project, generation := "project", "generation"
	currentBuildID := semantic.BuildID(project, generation)
	olderBuild := semantic.NewBuild(project, generation, "")
	olderBuild.BuildID = "older-build"
	olderBuild.State = semantic.Complete
	olderBuild.EntryIndexVersion = semantic.EntryIndexVersion
	olderBuild.SourceAvailability = semantic.SourceAvailabilityVerified
	staleEntry := semantic.Entry{
		ConceptID: "stale-concept",
		BindingID: "stale-binding",
		Observation: semantic.Observation{
			SchemaVersion: semantic.ObservationSchemaVersion,
			Completeness:  semantic.CompletenessComplete,
			Status:        "SUPPORTED",
		},
	}

	tests := []struct {
		name        string
		current     *semantic.Build
		wantState   semantic.State
		wantBuildID string
	}{
		{name: "current build absent", wantState: semantic.Unknown},
		{name: "current build running", current: func() *semantic.Build {
			build := semantic.NewBuild(project, generation, "")
			build.State = semantic.Running
			return &build
		}(), wantState: semantic.Running, wantBuildID: currentBuildID},
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
					semantic.BuildCollection: true,
					semantic.EntryCollection: true,
				},
			}
			adapter, err := New(client)
			if err != nil {
				t.Fatal(err)
			}
			page, err := adapter.PageSemanticInventory(context.Background(), semantic.PageOptions{
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
