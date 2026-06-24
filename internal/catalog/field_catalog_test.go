package catalog

import (
	"slices"
	"testing"

	"arangodb-proto/internal/fhirschema"
)

func TestFieldCatalogProfilerCanonicalPaths(t *testing.T) {
	cache := NewShapePlanCache()
	profiler := NewProfiler("TEST", "pathA", "Observation", cache)
	timings := map[string]float64{}
	payload := map[string]any{
		"identifier": []any{
			map[string]any{"value": "abc"},
		},
		"code": map[string]any{
			"coding": []any{
				map[string]any{
					"display": "Stage",
					"code":    "stage",
				},
			},
		},
		"valueCodeableConcept": map[string]any{
			"text": "Stage IVA",
			"coding": []any{
				map[string]any{
					"display": "Stage IVA",
					"code":    "iva",
				},
			},
		},
	}

	profiler.ObservePayload(payload, timings)
	docs := profiler.Documents()
	if len(docs) == 0 || docs[0].AuthResourcePath != "pathA" {
		t.Fatalf("expected auth_resource_path on catalog docs: %+v", docs)
	}
	paths := make([]string, 0, len(docs))
	for _, doc := range docs {
		paths = append(paths, doc.Path)
	}
	for _, expected := range []string{
		"identifier[]",
		"identifier[].value",
		"code",
		"code.coding[]",
		"code.coding[].display",
		"valueCodeableConcept",
		"valueCodeableConcept.text",
		"valueCodeableConcept.coding[].display",
	} {
		if !slices.Contains(paths, expected) {
			t.Fatalf("expected path %q in %v", expected, paths)
		}
	}
}

func TestFieldCatalogShapeCacheReusesPlans(t *testing.T) {
	cache := NewShapePlanCache()
	profiler := NewProfiler("TEST", "pathA", "Patient", cache)
	timings := map[string]float64{}

	first := map[string]any{
		"identifier": []any{
			map[string]any{"value": "A"},
		},
		"active": true,
	}
	second := map[string]any{
		"identifier": []any{
			map[string]any{"value": "B"},
		},
		"active": false,
	}

	profiler.ObservePayload(first, timings)
	profiler.ObservePayload(second, timings)

	if got := cache.planCount(); got != 1 {
		t.Fatalf("shape cache plan count = %d, want 1", got)
	}

	docs := profiler.Documents()
	docByPath := make(map[string]FieldCatalogDocument)
	for _, doc := range docs {
		docByPath[doc.Path] = doc
	}
	if got := docByPath["identifier[].value"].DocCount; got != 2 {
		t.Fatalf("identifier[].value doc_count = %d, want 2", got)
	}
}

func TestFieldCatalogCodeableConceptPivotMetadata(t *testing.T) {
	cache := NewShapePlanCache()
	profiler := NewProfiler("TEST", "pathA", "Observation", cache)
	timings := map[string]float64{}

	payload := map[string]any{
		"valueCodeableConcept": map[string]any{
			"text": "M0",
			"coding": []any{
				map[string]any{
					"system":  "http://snomed.info/sct",
					"code":    "1222591006",
					"display": "American Joint Committee on Cancer pM0",
				},
			},
		},
	}

	profiler.ObservePayload(payload, timings)
	docs := profiler.Documents()
	var found FieldCatalogDocument
	for _, doc := range docs {
		if doc.Path == "valueCodeableConcept" {
			found = doc
			break
		}
	}
	if !found.PivotCandidate {
		t.Fatalf("expected valueCodeableConcept to be pivot candidate: %+v", found)
	}
	if found.PivotKind != pivotKindCodeableConcept {
		t.Fatalf("pivot kind = %q, want %q", found.PivotKind, pivotKindCodeableConcept)
	}
	if found.PivotFamily != fhirschema.PivotFamilyCodeableConcept {
		t.Fatalf("pivot family = %q, want %q", found.PivotFamily, fhirschema.PivotFamilyCodeableConcept)
	}
	if found.PivotColumnSelect != "valueCodeableConcept.coding[].display" {
		t.Fatalf("unexpected column selector: %q", found.PivotColumnSelect)
	}
	if found.PivotValueSelect != "valueCodeableConcept.coding[].display" {
		t.Fatalf("unexpected value selector: %q", found.PivotValueSelect)
	}
	if !slices.Contains(found.PivotColumns, "American Joint Committee on Cancer pM0") {
		t.Fatalf("missing display pivot column in %+v", found.PivotColumns)
	}
	if !slices.Contains(found.DistinctValues, "M0") {
		t.Fatalf("missing text distinct value in %+v", found.DistinctValues)
	}
}

func TestFieldCatalogObservationPivotMetadata(t *testing.T) {
	cache := NewShapePlanCache()
	profiler := NewProfiler("TEST", "pathA", "Observation", cache)
	timings := map[string]float64{}

	payload := map[string]any{
		"code": map[string]any{
			"coding": []any{
				map[string]any{
					"display": "Tumor Purity",
					"code":    "tumor_purity",
				},
			},
		},
		"valueQuantity": map[string]any{
			"value": 0.82,
			"unit":  "fraction",
		},
	}

	profiler.ObservePayload(payload, timings)
	docs := profiler.Documents()
	var found FieldCatalogDocument
	for _, doc := range docs {
		if doc.Path == "code" {
			found = doc
			break
		}
	}
	if found.PivotKind != pivotKindObservation {
		t.Fatalf("pivot kind = %q, want %q", found.PivotKind, pivotKindObservation)
	}
	if found.PivotFamily != fhirschema.PivotFamilyObservationCodeValue {
		t.Fatalf("pivot family = %q, want %q", found.PivotFamily, fhirschema.PivotFamilyObservationCodeValue)
	}
	if found.PivotColumnSelect != "code.coding[].display" {
		t.Fatalf("unexpected column selector: %q", found.PivotColumnSelect)
	}
	if found.PivotValueSelect != "valueQuantity.value" {
		t.Fatalf("unexpected value selector: %q", found.PivotValueSelect)
	}
}
