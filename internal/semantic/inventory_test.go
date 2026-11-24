package semantic

import "testing"

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
	emitter := NewEmitter(project, generation)
	if emitter.buildID != wantBuildID {
		t.Fatalf("emitter cached build ID = %q, want %q", emitter.buildID, wantBuildID)
	}

	for index, sourceID := range []string{"retained:Observation/one", "retained:Observation/two"} {
		var contributions []Contribution
		err := emitter.ObservePayload(
			map[string]any{
				"resourceType": "Observation",
				"component": []any{
					inventoryTestComponent(sourceID+"-a", "v1", "label"),
					inventoryTestComponent(sourceID+"-b", "v1", "label"),
				},
			},
			"Observation", "scope-a", sourceID,
			func(contribution Contribution) {
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
			if contribution.Key != wantKeys[index][ordinal] || contribution.Ordinal != ordinal || contribution.SourceKind != SourceRetained {
				t.Fatalf("source %q contribution identity = key %q ordinal %d source kind %q, want key %q ordinal %d source kind %q", sourceID, contribution.Key, contribution.Ordinal, contribution.SourceKind, wantKeys[index][ordinal], ordinal, SourceRetained)
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
	var emitter Emitter
	var contributions []Contribution
	err := emitter.ObservePayload(
		map[string]any{
			"resourceType": "Observation",
			"component":    []any{inventoryTestComponent("zero-value", "v1", "label")},
		},
		"Observation", "scope-a", "retained:Observation/zero",
		func(contribution Contribution) {
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
		contribution.Ordinal != 0 || contribution.SourceKind != SourceRetained {
		t.Fatalf("zero-value emitter contribution identity = build %q key %q ordinal %d source kind %q", contribution.BuildID, contribution.Key, contribution.Ordinal, contribution.SourceKind)
	}
}

func inventoryTestComponent(code, version, display string) map[string]any {
	return map[string]any{
		"code": map[string]any{"coding": []any{map[string]any{
			"system":  "urn:inventory",
			"version": version,
			"code":    code,
			"display": display,
		}}},
		"valueQuantity": map[string]any{"value": 1.0, "unit": "mg"},
	}
}
