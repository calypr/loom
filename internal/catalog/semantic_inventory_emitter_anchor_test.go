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

func TestSemanticInventoryEmitterVersionChangesBuildIdentityWithoutBindingIdentity(t *testing.T) {
	const project = "loom_dev_cda_fhir"
	const generation = "cda-fhir-v1"
	const previousBuildID = "ca8ea62fdb8cc19ecfae1c23e286ae2dd082c38a297200cfc46c2c3124cc1858"
	const previousBuildKey = "sib_e3ee57e08f7504a36f0ae8c108ff6612b01cd05d63d955e4a945a85165ddcea0"

	build := NewSemanticInventoryBuild(project, generation, "")
	if build.BuildID == previousBuildID || build.BuildID != SemanticInventoryBuildID(project, generation) {
		t.Fatalf("new emitter build ID = %q, prior = %q", build.BuildID, previousBuildID)
	}
	if build.Key == previousBuildKey || build.Key != SemanticInventoryBuildKey(project, generation) {
		t.Fatalf("new emitter build key = %q, prior = %q", build.Key, previousBuildKey)
	}

	binding := inventoryTestObservation("v1", "label")
	const wantBindingID = "45665d85e72ad3c6da5aba8c4554cfa7c194c8e3b9c95b8c43aee78724ee13b3"
	if got := semanticBindingID(binding); got != wantBindingID {
		t.Fatalf("emitter revision changed the saved semantic binding ID: got %q, want %q", got, wantBindingID)
	}
}
