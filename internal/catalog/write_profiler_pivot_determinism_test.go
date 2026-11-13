package catalog

import (
	"encoding/json"
	"fmt"
	"testing"
)

func pivotDeterminismObservations() []map[string]any {
	return []map[string]any{
		{
			"code":          map[string]any{"coding": []any{map[string]any{"system": "https://bench.example/codes", "code": "QUANTITY", "display": "Quantity Display"}}},
			"valueQuantity": map[string]any{"value": 7.5, "unit": "kg", "system": "http://unitsofmeasure.org", "code": "kg"},
		},
		{
			"code":        map[string]any{"coding": []any{map[string]any{"system": "https://bench.example/codes", "code": "STRING", "display": "String Display"}}},
			"valueString": "self-reported:no",
		},
		{
			"code":                 map[string]any{"coding": []any{map[string]any{"system": "https://bench.example/codes", "code": "CONCEPT_TEXT", "display": "Concept Text Display"}}},
			"valueCodeableConcept": map[string]any{"text": "STAGE-A"},
		},
		{
			"code":                 map[string]any{"coding": []any{map[string]any{"system": "https://bench.example/codes", "code": "CONCEPT_CODING", "display": "Concept Coding Display"}}},
			"valueCodeableConcept": map[string]any{"coding": []any{map[string]any{"system": "https://bench.example/codes", "code": "STAGE-B", "display": "Stage B"}}},
		},
		{
			"code": map[string]any{
				"text":   "Mixed Code Text",
				"coding": []any{map[string]any{"system": "https://bench.example/codes", "code": "MIXED", "display": "Mixed Display"}},
			},
			"valueQuantity":        map[string]any{"value": 3, "unit": "cm"},
			"valueCodeableConcept": map[string]any{"text": "STAGE-C"},
			"valueString":          "mixed-string",
		},
		{
			"code": map[string]any{"coding": []any{map[string]any{"system": "https://bench.example/codes", "code": "NO_VALUE", "display": "No Value Display"}}},
		},
	}
}

func pivotDeterminismPermutations(count int) [][]int {
	remaining := make([]int, count)
	for index := range remaining {
		remaining[index] = index
	}
	results := [][]int(nil)
	var build func(prefix []int, rest []int)
	build = func(prefix []int, rest []int) {
		if len(rest) == 0 {
			permutation := append([]int(nil), prefix...)
			results = append(results, permutation)
			return
		}
		for index := range rest {
			next := append([]int(nil), rest[:index]...)
			next = append(next, rest[index+1:]...)
			build(append(append([]int(nil), prefix...), rest[index]), next)
		}
	}
	build(nil, remaining)
	return results
}

func pivotDeterminismProfile(t *testing.T, docs []map[string]any) string {
	t.Helper()
	profiler := NewProfilerForGeneration("TEST", "pivot-determinism", "pathA", "Observation", NewShapePlanCache())
	for _, doc := range docs {
		if err := profiler.ObservePayload(doc, map[string]float64{}); err != nil {
			t.Fatalf("observe payload: %v", err)
		}
	}
	encoded, err := json.Marshal(profiler.Documents())
	if err != nil {
		t.Fatalf("marshal documents: %v", err)
	}
	return string(encoded)
}

func pivotDeterminismFieldDocument(t *testing.T, encoded string, path string) FieldCatalogDocument {
	t.Helper()
	var docs []FieldCatalogDocument
	if err := json.Unmarshal([]byte(encoded), &docs); err != nil {
		t.Fatalf("unmarshal documents: %v", err)
	}
	for _, doc := range docs {
		if doc.Path == path {
			return doc
		}
	}
	t.Fatalf("no field document for path %q in %s", path, encoded)
	return FieldCatalogDocument{}
}

func TestFieldCatalogObservationPivotDefaultsIgnoreDocumentArrivalOrder(t *testing.T) {
	docs := pivotDeterminismObservations()
	var canonical string
	for permutation, order := range pivotDeterminismPermutations(len(docs)) {
		ordered := make([]map[string]any, 0, len(order))
		for _, index := range order {
			ordered = append(ordered, docs[index])
		}
		encoded := pivotDeterminismProfile(t, ordered)
		if permutation == 0 {
			canonical = encoded
		}
		if encoded != canonical {
			t.Fatalf("permutation %d (order %v) produced a different stored profile than permutation 0\nfirst: %s\nother: %s", permutation, order, canonical, encoded)
		}
	}
	found := pivotDeterminismFieldDocument(t, canonical, "code")
	if found.PivotValueSelect != "valueQuantity.value" {
		t.Fatalf("pivot value selector = %q, want %q", found.PivotValueSelect, "valueQuantity.value")
	}
	if found.PivotColumnSelect != "code.text" {
		t.Fatalf("pivot column selector = %q, want %q", found.PivotColumnSelect, "code.text")
	}
	if found.PivotKind != pivotKindObservation {
		t.Fatalf("pivot kind = %q, want %q", found.PivotKind, pivotKindObservation)
	}
}

func TestFieldCatalogObservationPivotValueSelectorFollowsArmPrecedence(t *testing.T) {
	cases := []struct {
		name string
		docs []map[string]any
		want string
	}{
		{
			name: "coding-display-beats-string",
			docs: []map[string]any{
				{"code": map[string]any{"coding": []any{map[string]any{"code": "A", "display": "Alpha"}}}, "valueString": "plain"},
				{"code": map[string]any{"coding": []any{map[string]any{"code": "B", "display": "Beta"}}}, "valueCodeableConcept": map[string]any{"coding": []any{map[string]any{"code": "C", "display": "Gamma"}}}},
			},
			want: "valueCodeableConcept.coding[].display",
		},
		{
			name: "concept-text-beats-coding-display",
			docs: []map[string]any{
				{"code": map[string]any{"coding": []any{map[string]any{"code": "B", "display": "Beta"}}}, "valueCodeableConcept": map[string]any{"coding": []any{map[string]any{"code": "C", "display": "Gamma"}}}},
				{"code": map[string]any{"coding": []any{map[string]any{"code": "A", "display": "Alpha"}}}, "valueCodeableConcept": map[string]any{"text": "STAGE-A"}},
			},
			want: "valueCodeableConcept.text",
		},
		{
			name: "quantity-beats-all",
			docs: []map[string]any{
				{"code": map[string]any{"coding": []any{map[string]any{"code": "A", "display": "Alpha"}}}, "valueString": "plain"},
				{"code": map[string]any{"coding": []any{map[string]any{"code": "B", "display": "Beta"}}}, "valueCodeableConcept": map[string]any{"text": "STAGE-A"}},
				{"code": map[string]any{"coding": []any{map[string]any{"code": "C", "display": "Gamma"}}}, "valueQuantity": map[string]any{"value": 1.5}},
			},
			want: "valueQuantity.value",
		},
	}
	for _, testCase := range cases {
		reversed := make([]map[string]any, 0, len(testCase.docs))
		for index := len(testCase.docs) - 1; index >= 0; index-- {
			reversed = append(reversed, testCase.docs[index])
		}
		forward := pivotDeterminismProfile(t, testCase.docs)
		backward := pivotDeterminismProfile(t, reversed)
		if forward != backward {
			t.Fatalf("%s: arrival order changed the stored profile\nforward:  %s\nbackward: %s", testCase.name, forward, backward)
		}
		found := pivotDeterminismFieldDocument(t, forward, "code")
		if found.PivotValueSelect != testCase.want {
			t.Fatalf("%s: pivot value selector = %q, want %q", testCase.name, found.PivotValueSelect, testCase.want)
		}
	}
}

func pivotDeterminismMergePair(t *testing.T, leftDocs, rightDocs []map[string]any, swap bool) string {
	t.Helper()
	left := NewProfilerForGeneration("TEST", "pivot-determinism", "pathA", "Observation", NewShapePlanCache())
	right := NewProfilerForGeneration("TEST", "pivot-determinism", "pathA", "Observation", NewShapePlanCache())
	for _, doc := range leftDocs {
		if err := left.ObservePayload(doc, map[string]float64{}); err != nil {
			t.Fatalf("observe left payload: %v", err)
		}
	}
	for _, doc := range rightDocs {
		if err := right.ObservePayload(doc, map[string]float64{}); err != nil {
			t.Fatalf("observe right payload: %v", err)
		}
	}
	destination, source := left, right
	if swap {
		destination, source = right, left
	}
	if err := destination.Merge(source); err != nil {
		t.Fatalf("merge: %v", err)
	}
	encoded, err := json.Marshal(destination.Documents())
	if err != nil {
		t.Fatalf("marshal documents: %v", err)
	}
	return string(encoded)
}

func TestFieldCatalogObservationPivotDefaultsStableAcrossMergeOrder(t *testing.T) {
	leftDocs := []map[string]any{
		{"code": map[string]any{"coding": []any{map[string]any{"code": "Q", "display": "Quantity"}}}, "valueQuantity": map[string]any{"value": 2}},
		{"code": map[string]any{"coding": []any{map[string]any{"code": "N", "display": "None"}}}},
	}
	rightDocs := []map[string]any{
		{"code": map[string]any{"coding": []any{map[string]any{"code": "S", "display": "String"}}}, "valueString": "later"},
		{"code": map[string]any{"coding": []any{map[string]any{"code": "CT", "display": "Concept"}}}, "valueCodeableConcept": map[string]any{"text": "STAGE-A"}},
	}
	forward := pivotDeterminismMergePair(t, leftDocs, rightDocs, false)
	backward := pivotDeterminismMergePair(t, leftDocs, rightDocs, true)
	if forward != backward {
		t.Fatalf("merge direction changed the stored profile\nleft-into-right:  %s\nright-into-left: %s", forward, backward)
	}
	found := pivotDeterminismFieldDocument(t, forward, "code")
	if found.PivotValueSelect != "valueQuantity.value" {
		t.Fatalf("pivot value selector = %q, want %q", found.PivotValueSelect, "valueQuantity.value")
	}
}

func pivotDeterminismCreationOrderRuns(t *testing.T) []string {
	emptyCodeWithArm := map[string]any{
		"code":          map[string]any{},
		"valueQuantity": map[string]any{"value": 9},
	}
	codeShapeWithoutArm := map[string]any{
		"code": map[string]any{"coding": []any{map[string]any{"code": "C", "display": "Concept"}}},
	}
	return []string{
		pivotDeterminismProfile(t, []map[string]any{emptyCodeWithArm, codeShapeWithoutArm}),
		pivotDeterminismProfile(t, []map[string]any{codeShapeWithoutArm, emptyCodeWithArm}),
		pivotDeterminismMergePair(t, []map[string]any{emptyCodeWithArm}, []map[string]any{codeShapeWithoutArm}, false),
		pivotDeterminismMergePair(t, []map[string]any{emptyCodeWithArm}, []map[string]any{codeShapeWithoutArm}, true),
	}
}

func TestFieldCatalogObservationPivotStatConvergesAcrossCreationOrder(t *testing.T) {
	runs := pivotDeterminismCreationOrderRuns(t)
	for index := 1; index < len(runs); index++ {
		if runs[index] != runs[0] {
			t.Fatalf("run %d produced a different stored profile than run 0\nbase: %s\nrun%d: %s", index, runs[0], index, runs[index])
		}
	}
	found := pivotDeterminismFieldDocument(t, runs[0], "code")
	if !found.PivotCandidate {
		t.Fatalf("pivot candidate = false, want true in %s", runs[0])
	}
	if found.PivotKind != pivotKindObservation {
		t.Fatalf("pivot kind = %q, want %q", found.PivotKind, pivotKindObservation)
	}
	if found.PivotItemResourceType != "Observation" {
		t.Fatalf("pivot item resource type = %q, want %q", found.PivotItemResourceType, "Observation")
	}
	if len(found.PivotValueSelectors) == 0 {
		t.Fatalf("pivot value selectors empty, want spec defaults in %s", runs[0])
	}
	if fmt.Sprint(found.PivotFamily) != "OBSERVATION_CODE_VALUE" {
		t.Fatalf("pivot family = %q, want OBSERVATION_CODE_VALUE", found.PivotFamily)
	}
}
