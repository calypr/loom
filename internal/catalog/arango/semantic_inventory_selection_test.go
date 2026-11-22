package arango

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/catalog/codedconcept"
	"github.com/calypr/loom/internal/catalog/semanticinventory"
)

func TestPageSemanticInventoryDoesNotAdvertiseOlderCompletedBuild(t *testing.T) {
	project, generation := "project", "generation"
	currentBuildID := semanticinventory.BuildID(project, generation)
	olderBuild := semanticinventory.NewBuild(project, generation, "")
	olderBuild.BuildID = "older-build"
	olderBuild.State = semanticinventory.Complete
	olderBuild.EntryIndexVersion = semanticinventory.EntryIndexVersion
	olderBuild.SourceAvailability = semanticinventory.SourceAvailabilityVerified
	staleEntry := semanticinventory.Entry{
		ConceptID: "stale-concept",
		BindingID: "stale-binding",
		Observation: codedconcept.Observation{
			SchemaVersion: codedconcept.ObservationSchemaVersion,
			Completeness:  codedconcept.Complete,
			Status:        "SUPPORTED",
		},
	}

	tests := []struct {
		name        string
		current     *semanticinventory.Build
		wantState   semanticinventory.State
		wantBuildID string
	}{
		{name: "current build absent", wantState: semanticinventory.Unknown},
		{name: "current build running", current: func() *semanticinventory.Build {
			build := semanticinventory.NewBuild(project, generation, "")
			build.State = semanticinventory.Running
			return &build
		}(), wantState: semanticinventory.Running, wantBuildID: currentBuildID},
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
					semanticinventory.BuildCollection: true,
					semanticinventory.EntryCollection: true,
				},
			}
			adapter, err := New(client)
			if err != nil {
				t.Fatal(err)
			}
			page, err := adapter.PageSemanticInventory(context.Background(), semanticinventory.PageOptions{
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
