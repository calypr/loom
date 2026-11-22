package fieldprofile

import (
	"github.com/calypr/loom/internal/catalog/semanticinventory"
	"testing"
)

func TestProfilerReusesSemanticInventoryBuildIDAcrossContributions(t *testing.T) {
	const project = "build-id-cache-project"
	const generation = " generation-7 "
	const wantBuildID = "f907365e5589904236001b479f85e2995f12f29a4b4dd0feaa2f9cfbb2a19a98"
	wantKeys := [][]string{
		{
			"6e4e9036bcba5a9b1f6fea74196af23462ec1e697a28d2adf04d3a12a48d36f9",
			"fd97d4dda48225a60663e807f4fd1d8398eb311ca62d721d9327f2213c5dd043",
		},
		{
			"e0dfd1b1eaee2948cbae840492c858c0cbf6b462e800024e4ba931526ba3b683",
			"528f56d139e40b27140d7ee4b7b21d278abe3673e9be3dca3d354b9a484b45e1",
		},
	}
	profiler := NewProfilerForGenerationWithLimits(project, generation, "scope-a", "Observation", nil, ProfileLimits{})
	if profiler.semanticInventoryBuildID != wantBuildID {
		t.Fatalf("profiler cached build ID = %q, want %q", profiler.semanticInventoryBuildID, wantBuildID)
	}

	for index, sourceID := range []string{"Observations.ndjson#1", "Observations.ndjson#2"} {
		var contributions []semanticinventory.Contribution
		err := profiler.ObservePayloadWithInventory(
			map[string]any{
				"resourceType": "Observation",
				"component": []any{
					inventoryTestComponent(sourceID+"-a", "v1", "label"),
					inventoryTestComponent(sourceID+"-b", "v1", "label"),
				},
			},
			map[string]float64{},
			sourceID,
			func(contribution semanticinventory.Contribution) {
				contributions = append(contributions, contribution)
			},
		)
		if err != nil {
			t.Fatalf("observe source %q: %v", sourceID, err)
		}
		if len(contributions) != len(wantKeys[index]) {
			t.Fatalf("source %q contributions = %d, want %d", sourceID, len(contributions), len(wantKeys[index]))
		}
		for ordinal, contribution := range contributions {
			if contribution.Key != wantKeys[index][ordinal] || contribution.Ordinal != ordinal || contribution.SourceKind != semanticinventory.SourceFile {
				t.Fatalf("source %q contribution identity = key %q ordinal %d source kind %q, want key %q ordinal %d source kind %q", sourceID, contribution.Key, contribution.Ordinal, contribution.SourceKind, wantKeys[index][ordinal], ordinal, semanticinventory.SourceFile)
			}
			if contribution.BuildID != wantBuildID {
				t.Fatalf("source %q contribution build ID = %q, want cached ID %q", sourceID, contribution.BuildID, wantBuildID)
			}
		}
	}
	if profiler.semanticInventoryBuildID != wantBuildID {
		t.Fatalf("profiler cached build ID changed after contributions: got %q, want %q", profiler.semanticInventoryBuildID, wantBuildID)
	}
}
