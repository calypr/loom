package catalog

import "testing"

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
		var contributions []SemanticInventoryContribution
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
			func(contribution SemanticInventoryContribution) {
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
			if contribution.Key != wantKeys[index][ordinal] || contribution.Ordinal != ordinal || contribution.SourceKind != SemanticInventorySourceFile {
				t.Fatalf("source %q contribution identity = key %q ordinal %d source kind %q, want key %q ordinal %d source kind %q", sourceID, contribution.Key, contribution.Ordinal, contribution.SourceKind, wantKeys[index][ordinal], ordinal, SemanticInventorySourceFile)
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

func TestSemanticInventoryEmitterReusesSemanticInventoryBuildIDAcrossPayloads(t *testing.T) {
	const project = "build-id-cache-emitter-project"
	const generation = "generation-9"
	const wantBuildID = "4423c07f58eb7d34710898ac71134def75a846bdeb897b7a69241c9364339377"
	wantKeys := [][]string{
		{
			"92f49f3e719379b8548fc52c04dd75cbc4c8929199d48663534c13986455c48b",
			"ee0dccc8ff35e1343b27107ceb1b5eab2ba34002cdfb8b315b089a2d015f8d49",
		},
		{
			"bc831b941d0940780ff810fd10caef45f52d58e62f8f1bb51ac486c907e17b10",
			"2abae509e484062f6afdea9e1f36af34d267d9e753100db71dc2de3472d80d9d",
		},
	}
	emitter := NewSemanticInventoryEmitter(project, generation)
	if emitter.buildID != wantBuildID {
		t.Fatalf("emitter cached build ID = %q, want %q", emitter.buildID, wantBuildID)
	}

	for index, sourceID := range []string{"retained:Observation/one", "retained:Observation/two"} {
		var contributions []SemanticInventoryContribution
		err := emitter.ObservePayload(
			map[string]any{
				"resourceType": "Observation",
				"component": []any{
					inventoryTestComponent(sourceID+"-a", "v1", "label"),
					inventoryTestComponent(sourceID+"-b", "v1", "label"),
				},
			},
			"Observation", "scope-a", sourceID,
			func(contribution SemanticInventoryContribution) {
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
			if contribution.Key != wantKeys[index][ordinal] || contribution.Ordinal != ordinal || contribution.SourceKind != SemanticInventorySourceRetained {
				t.Fatalf("source %q contribution identity = key %q ordinal %d source kind %q, want key %q ordinal %d source kind %q", sourceID, contribution.Key, contribution.Ordinal, contribution.SourceKind, wantKeys[index][ordinal], ordinal, SemanticInventorySourceRetained)
			}
			if contribution.BuildID != wantBuildID {
				t.Fatalf("source %q contribution build ID = %q, want cached ID %q", sourceID, contribution.BuildID, wantBuildID)
			}
		}
	}
	if emitter.buildID != wantBuildID {
		t.Fatalf("emitter cached build ID changed after payloads: got %q, want %q", emitter.buildID, wantBuildID)
	}
}

func TestZeroValueSemanticInventoryEmitterKeepsComputedBuildIdentity(t *testing.T) {
	var emitter SemanticInventoryEmitter
	var contributions []SemanticInventoryContribution
	err := emitter.ObservePayload(
		map[string]any{
			"resourceType": "Observation",
			"component":    []any{inventoryTestComponent("zero-value", "v1", "label")},
		},
		"Observation", "scope-a", "retained:Observation/zero",
		func(contribution SemanticInventoryContribution) {
			contributions = append(contributions, contribution)
		},
	)
	if err != nil {
		t.Fatalf("observe zero-value emitter: %v", err)
	}
	if len(contributions) != 1 {
		t.Fatalf("zero-value emitter contributions = %d, want one", len(contributions))
	}
	contribution := contributions[0]
	if contribution.BuildID != "f697339bdf8bcca3cae3378784981bea444a9a56fb2aa60f21265c552f6de8a9" ||
		contribution.Key != "aa11bab5442cdf505c1fe8c6e86286469b5798c33165b7d93a26bdcb01f8ee5b" ||
		contribution.Ordinal != 0 || contribution.SourceKind != SemanticInventorySourceRetained {
		t.Fatalf("zero-value emitter contribution identity = build %q key %q ordinal %d source kind %q", contribution.BuildID, contribution.Key, contribution.Ordinal, contribution.SourceKind)
	}
}
