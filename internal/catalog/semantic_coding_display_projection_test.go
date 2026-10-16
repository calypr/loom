package catalog

import (
	"reflect"
	"sort"
	"testing"
)

func TestSemanticCodeableConceptDisplayProjection(t *testing.T) {
	t.Run("text remains preferred", func(t *testing.T) {
		observation := observeCodeableConceptValue(t, map[string]any{
			"text":   "Preferred text",
			"coding": []any{map[string]any{"system": "urn:value", "code": "value", "display": "Coding display"}},
		})
		if observation.Value.Selector != "valueCodeableConcept.text" ||
			!reflect.DeepEqual(observation.Value.FallbackSelectors, []string{"valueCodeableConcept.coding[].display"}) || !observation.Value.FallbackOnBlankString || observation.Status != "SUPPORTED" ||
			!reflect.DeepEqual(observation.Examples, []string{"Preferred text"}) {
			t.Fatalf("text projection = %#v, want text preference, ordered coding fallback, and its example", observation)
		}
	})

	t.Run("one coding display", func(t *testing.T) {
		observation := observeCodeableConceptValue(t, map[string]any{
			"coding": []any{map[string]any{"system": "urn:value", "code": "value", "display": "Stage I"}},
		})
		if observation.Value.Selector != "valueCodeableConcept.text" ||
			!reflect.DeepEqual(observation.Value.FallbackSelectors, []string{"valueCodeableConcept.coding[].display"}) || !observation.Value.FallbackOnBlankString || observation.Status != "SUPPORTED" ||
			!reflect.DeepEqual(observation.Examples, []string{"Stage I"}) {
			t.Fatalf("single coding display projection = %#v, want text preference, coding fallback, and its example", observation)
		}
	})

	t.Run("blank text falls back to all repeated coding displays", func(t *testing.T) {
		observation := observeCodeableConceptValue(t, map[string]any{
			"text": "  ",
			"coding": []any{
				map[string]any{"system": "urn:value", "code": "first", "display": "Stage I"},
				map[string]any{"system": "urn:value", "code": "blank", "display": "  "},
				map[string]any{"system": "urn:value", "code": "second", "display": "Early stage"},
			},
		})
		got := append([]string(nil), observation.Examples...)
		sort.Strings(got)
		if observation.Value.Selector != "valueCodeableConcept.text" ||
			!reflect.DeepEqual(observation.Value.FallbackSelectors, []string{"valueCodeableConcept.coding[].display"}) || !observation.Value.FallbackOnBlankString || observation.Status != "SUPPORTED" ||
			!reflect.DeepEqual(got, []string{"Early stage", "Stage I"}) {
			t.Fatalf("repeated coding display fallback = %#v, want both displays and SUPPORTED", observation)
		}
	})

	for _, test := range []struct {
		name  string
		value map[string]any
	}{
		{name: "missing displays", value: map[string]any{"coding": []any{map[string]any{"system": "urn:value", "code": "value"}}}},
		{name: "all displays blank", value: map[string]any{"text": " ", "coding": []any{
			map[string]any{"system": "urn:value", "code": "first", "display": ""},
			map[string]any{"system": "urn:value", "code": "second", "display": "  "},
		}}},
	} {
		t.Run(test.name+" remain unsupported", func(t *testing.T) {
			observation := observeCodeableConceptValue(t, test.value)
			if observation.Value.Selector != "valueCodeableConcept.text" ||
				!reflect.DeepEqual(observation.Value.FallbackSelectors, []string{"valueCodeableConcept.coding[].display"}) ||
				!observation.Value.FallbackOnBlankString || observation.Status != SemanticStatusUnsupportedValueProjection || len(observation.Examples) != 0 {
				t.Fatalf("missing coding display fallback = %#v, want unsupported with no examples", observation)
			}
		})
	}
}

func TestSemanticCodeableConceptDisplayProjectionWithinExtensionKeepsRelativeExamples(t *testing.T) {
	profiler := NewProfilerForGeneration("project", "generation", "scope", "Observation", nil)
	profiler.ObservePayload(map[string]any{
		"resourceType": "Observation",
		"extension": []any{map[string]any{
			"url": "urn:study:stage",
			"valueCodeableConcept": map[string]any{
				"text": " ",
				"coding": []any{
					map[string]any{"system": "urn:value", "code": "a", "display": "Stage I"},
					map[string]any{"system": "urn:value", "code": "b", "display": "Stage II"},
				},
			},
		}},
	}, map[string]float64{})
	for _, document := range profiler.Documents() {
		for _, observation := range document.SemanticObservations {
			if observation.RuleHint != "EXTENSION_URL_VALUE" || observation.Key.Display != "urn:study:stage" {
				continue
			}
			if observation.Status != "SUPPORTED" || observation.Value.Selector != "extension[].valueCodeableConcept.text" ||
				!reflect.DeepEqual(observation.Value.FallbackSelectors, []string{"extension[].valueCodeableConcept.coding[].display"}) ||
				!observation.Value.FallbackOnBlankString || !reflect.DeepEqual(observation.Examples, []string{"Stage I", "Stage II"}) {
				t.Fatalf("extension CodeableConcept projection = %#v", observation)
			}
			return
		}
	}
	t.Fatal("extension CodeableConcept semantic observation was not retained")
}

func observeCodeableConceptValue(t *testing.T, value map[string]any) SemanticObservation {
	t.Helper()
	p := NewProfilerForGeneration("project", "generation", "scope", "Observation", nil)
	p.ObservePayload(map[string]any{
		"resourceType": "Observation",
		"code": map[string]any{"coding": []any{
			map[string]any{"system": "urn:study", "code": "stage", "display": "Stage"},
		}},
		"valueCodeableConcept": value,
	}, map[string]float64{})
	for _, document := range p.Documents() {
		for _, observation := range document.SemanticObservations {
			if observation.RuleHint == SemanticRuleHintCodedValueV1 && observation.Key.Code == "stage" {
				return observation
			}
		}
	}
	t.Fatal("Observation value was not retained in the semantic catalog")
	return SemanticObservation{}
}
