package catalog

import (
	"reflect"
	"testing"
)

func TestSemanticInventoryEmitterIgnoresRepeatedCodeableConceptAnchor(t *testing.T) {
	payload := map[string]any{
		"resourceType": "Observation",
		"code": map[string]any{"coding": []any{map[string]any{
			"system": "urn:observation", "code": "measurement", "display": "Measurement",
		}}},
		"category": []any{
			map[string]any{"coding": []any{map[string]any{"system": "urn:category", "code": "a", "display": "Category A"}}},
			map[string]any{"coding": []any{map[string]any{"system": "urn:category", "code": "b", "display": "Category B"}}},
		},
		"valueCodeableConcept": map[string]any{
			"text":   "Stage I",
			"coding": []any{map[string]any{"system": "urn:value", "code": "stage-1", "display": "Stage I"}},
		},
	}

	var coded []SemanticInventoryContribution
	NewSemanticInventoryEmitter("project", "generation").ObservePayload(
		payload, "Observation", "", "retained:Observation/witness", func(contribution SemanticInventoryContribution) {
			if contribution.Observation.RuleHint == SemanticRuleHintCodedValueV1 {
				coded = append(coded, contribution)
			}
		},
	)
	if len(coded) != 1 {
		t.Fatalf("coded-value contributions = %d, want one pairing for Observation.code and valueCodeableConcept: %#v", len(coded), coded)
	}
	got := coded[0].Observation
	if got.Source.Path != "code" || got.Key.Selector != "code.coding[]" || got.Key.System != "urn:observation" || got.Key.Code != "measurement" ||
		got.Value.Selector != "valueCodeableConcept.text" || !reflect.DeepEqual(got.Value.FallbackSelectors, []string{"valueCodeableConcept.coding[].display"}) ||
		!got.Value.FallbackOnBlankString || got.Status != "SUPPORTED" || !reflect.DeepEqual(got.Examples, []string{"Stage I"}) {
		t.Fatalf("coded-value projection = %#v", got)
	}
}

func TestSemanticInventoryEmitterStillRejectsAmbiguousScalarConceptAnchors(t *testing.T) {
	payload := map[string]any{
		"resourceType": "Observation",
		"code":         map[string]any{"coding": []any{map[string]any{"system": "urn:observation", "code": "measurement"}}},
		"method":       map[string]any{"coding": []any{map[string]any{"system": "urn:method", "code": "method"}}},
		"valueString":  "observed",
	}

	var coded []SemanticInventoryContribution
	NewSemanticInventoryEmitter("project", "generation").ObservePayload(
		payload, "Observation", "", "retained:Observation/ambiguous", func(contribution SemanticInventoryContribution) {
			if contribution.Observation.RuleHint == SemanticRuleHintCodedValueV1 {
				coded = append(coded, contribution)
			}
		},
	)
	if len(coded) != 0 {
		t.Fatalf("ambiguous non-repeated anchors formed %d coded-value pairings: %#v", len(coded), coded)
	}
}

func TestSemanticInventoryEmitterVersionThreeMatchesCompletedBuild(t *testing.T) {
	const project = "loom_dev_cda_fhir"
	const generation = "cda-fhir-v1"
	const completedBuildID = "606e1763ce95aa39de168217ba8b584090bfe34e09741a7931da67cd0b8810bb"
	const completedBuildKey = "sib_890163afc080cf6e9134a49b09667b93161a47c03862b39ac25459e1be2f6f27"

	build := NewSemanticInventoryBuild(project, generation, "")
	if SemanticInventoryEmitterVersion != 3 || build.BuildID != completedBuildID || build.BuildID != SemanticInventoryBuildID(project, generation) {
		t.Fatalf("semantic-v3 build identity = %d/%q, want completed build %q", SemanticInventoryEmitterVersion, build.BuildID, completedBuildID)
	}
	if build.Key != completedBuildKey || build.Key != SemanticInventoryBuildKey(project, generation) {
		t.Fatalf("semantic-v3 build key = %q, want completed build key %q", build.Key, completedBuildKey)
	}

	observation := inventoryTestObservation("v1", "label")
	if observation.Source.Path != "component[]" || observation.Key.Selector != "code.coding[]" || observation.Key.System != "urn:inventory" || observation.Key.Version != "v1" || observation.Key.Code != "glucose" || observation.Value.Selector != "valueQuantity.value" {
		t.Fatalf("semantic-v3 observation = %#v, want completed code/Quantity binding", observation)
	}
	const wantConceptID = "e7a9d88ce655cd0e8a3a8a293f68a87455049f579fbc1ba767d625faa1462133"
	const wantBindingID = "45665d85e72ad3c6da5aba8c4554cfa7c194c8e3b9c95b8c43aee78724ee13b3"
	if got := semanticConceptID(observation); got != wantConceptID {
		t.Fatalf("semantic-v3 concept ID = %q, want completed ID %q", got, wantConceptID)
	}
	if got := semanticBindingID(observation); got != wantBindingID {
		t.Fatalf("semantic-v3 binding ID = %q, want completed ID %q", got, wantBindingID)
	}
}
