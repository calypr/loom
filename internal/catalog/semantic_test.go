package catalog

import (
	"encoding/json"
	"reflect"
	"testing"
)

func semanticDocument(t *testing.T, profiler *Profiler, path string) FieldCatalogDocument {
	t.Helper()
	for _, document := range profiler.Documents() {
		if document.Path == path {
			return document
		}
	}
	t.Fatalf("semantic document %q not found", path)
	return FieldCatalogDocument{}
}

func TestSemanticObservationCorrelatesObservationCodeValueAndComponents(t *testing.T) {
	profiler := NewProfilerForGeneration("P", "g", "scope", "Observation", NewShapePlanCache())
	profiler.ObservePayload(map[string]any{
		"meta": map[string]any{"profile": []any{"http://example.org/Profile"}},
		"code": map[string]any{"coding": []any{map[string]any{
			"system": "http://loinc.org", "code": "718-7", "display": "Hemoglobin",
		}}},
		"valueQuantity": map[string]any{"value": 0.0, "unit": "g/dL"},
		"component": []any{map[string]any{
			"code":         map[string]any{"coding": []any{map[string]any{"system": "x", "code": "a", "display": "A"}}},
			"valueBoolean": false,
		}},
	}, map[string]float64{})

	code := semanticDocument(t, profiler, "code")
	if len(code.SemanticObservations) != 1 {
		t.Fatalf("code observations = %#v", code.SemanticObservations)
	}
	observation := code.SemanticObservations[0]
	if observation.Source.Canonical != "Observation.code" || observation.Source.Profile != "http://example.org/Profile" {
		t.Fatalf("source = %#v", observation.Source)
	}
	if observation.Key.System != "http://loinc.org" || observation.Key.Code != "718-7" || observation.Key.Display != "Hemoglobin" {
		t.Fatalf("key = %#v", observation.Key)
	}
	if observation.Value.Selector != "valueQuantity.value" || observation.Value.Type != "number" || observation.Population != 1 {
		t.Fatalf("value = %#v", observation.Value)
	}
	if len(observation.Examples) != 1 || observation.Examples[0] != "0" {
		t.Fatalf("zero example = %#v", observation.Examples)
	}
	component := semanticDocument(t, profiler, "component[]")
	if len(component.SemanticObservations) != 1 || component.SemanticObservations[0].Value.Selector != "component[].valueBoolean" {
		t.Fatalf("component observations = %#v", component.SemanticObservations)
	}
	if got := component.SemanticObservations[0].Examples; !reflect.DeepEqual(got, []string{"false"}) {
		t.Fatalf("false example = %#v", got)
	}
}

func TestSemanticObservationIdentifierAndExtensionNested(t *testing.T) {
	profiler := NewProfilerForGeneration("P", "", "scope", "DocumentReference", NewShapePlanCache())
	profiler.ObservePayload(map[string]any{
		"identifier": []any{map[string]any{"system": "urn:accession", "value": "A-1"}},
		"extension": []any{map[string]any{
			"url": "urn:outer", "valueUrl": "https://example/file",
			"extension": []any{map[string]any{"url": "urn:inner", "valueString": "hash"}},
		}},
	}, map[string]float64{})

	identifier := semanticDocument(t, profiler, "identifier[]")
	if len(identifier.SemanticObservations) != 1 || identifier.SemanticObservations[0].Key.System != "urn:accession" || identifier.SemanticObservations[0].Value.Selector != "identifier[].value" {
		t.Fatalf("identifier = %#v", identifier.SemanticObservations)
	}
	outer := semanticDocument(t, profiler, "extension[]")
	if len(outer.SemanticObservations) != 1 || outer.SemanticObservations[0].Key.Display != "urn:outer" || outer.SemanticObservations[0].Value.Selector != "extension[].valueUrl" {
		t.Fatalf("outer extension = %#v", outer.SemanticObservations)
	}
	inner := semanticDocument(t, profiler, "extension[].extension[]")
	if len(inner.SemanticObservations) != 1 || inner.SemanticObservations[0].Key.Display != "urn:inner" {
		t.Fatalf("inner extension = %#v", inner.SemanticObservations)
	}
}

func TestSemanticObservationBoundsSuppressionAndDeterminism(t *testing.T) {
	build := func(values []any) []SemanticObservation {
		profiler := NewProfilerForGeneration("P", "g", "scope", "Observation", NewShapePlanCache())
		for _, value := range values {
			profiler.ObservePayload(map[string]any{
				"code":        map[string]any{"coding": []any{map[string]any{"code": "x", "display": "X"}}},
				"valueString": value,
			}, map[string]float64{})
		}
		return semanticDocument(t, profiler, "code").SemanticObservations
	}
	values := make([]any, maxSemanticExamples+4)
	for i := range values {
		values[i] = string(rune('a'+i%26)) + string(rune('0'+i/26))
	}
	values = append(values, map[string]any{"secret": "not safe"}, "\x00hidden", "")
	first := build(values)
	reversed := append([]any(nil), values...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	second := build(reversed)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("reordered observations differ:\nfirst=%#v\nsecond=%#v", first, second)
	}
	if len(first) != 1 || len(first[0].Examples) != maxSemanticExamples || !first[0].ExamplesTruncated {
		t.Fatalf("bounded examples = %#v", first)
	}
}

func TestSemanticObservationLegacyWireCompatibility(t *testing.T) {
	var document FieldCatalogDocument
	if err := json.Unmarshal([]byte(`{"_key":"legacy","project":"P","resource_type":"Patient","path":"id","kind":"scalar","doc_count":1}`), &document); err != nil {
		t.Fatal(err)
	}
	if document.SemanticObservations != nil {
		t.Fatalf("legacy semantic observations = %#v", document.SemanticObservations)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" {
		t.Fatal("empty legacy JSON")
	}
}

func TestSemanticObservationMergeAggregatesPopulationAndExamples(t *testing.T) {
	left := NewProfilerForGeneration("P", "g", "scope", "Observation", NewShapePlanCache())
	right := NewProfilerForGeneration("P", "g", "scope", "Observation", NewShapePlanCache())
	payload := func(value string) map[string]any {
		return map[string]any{
			"code":        map[string]any{"coding": []any{map[string]any{"system": "s", "code": "c", "display": "Concept"}}},
			"valueString": value,
		}
	}
	left.ObservePayload(payload("left"), map[string]float64{})
	right.ObservePayload(payload("right"), map[string]float64{})
	if err := left.Merge(right); err != nil {
		t.Fatal(err)
	}
	observation := semanticDocument(t, left, "code").SemanticObservations[0]
	if observation.Population != 2 || !reflect.DeepEqual(observation.Examples, []string{"left", "right"}) {
		t.Fatalf("merged observation = %#v", observation)
	}
}

func TestSemanticObservationDuplicateCodingCountsOneDocument(t *testing.T) {
	profiler := NewProfilerForGeneration("P", "g", "scope", "Observation", NewShapePlanCache())
	payload := map[string]any{
		"resourceType": "Observation",
		"code": map[string]any{"coding": []any{
			map[string]any{"system": "s", "code": "c", "display": "Same"},
			map[string]any{"system": "s", "code": "c", "display": "Same"},
		}},
		"valueString": "value",
	}
	profiler.ObservePayload(payload, map[string]float64{})
	document := semanticDocument(t, profiler, "code")
	if len(document.SemanticObservations) != 1 || document.SemanticObservations[0].Population != 1 {
		t.Fatalf("duplicate coding overcounted: %#v", document.SemanticObservations)
	}
}
