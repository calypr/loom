package catalog

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/calypr/loom/fhirschema"
)

func TestFieldCatalogRetainsAllDynamicKeys(t *testing.T) {
	stat := fieldCatalogStats{distinctSet: make(map[string]struct{}), pivotColumnSet: make(map[string]struct{})}
	for i := range 257 {
		stat.addDistinct(strconv.Itoa(i))
	}
	if stat.distinctTruncated || len(stat.distinctValues) != 257 {
		t.Fatalf("distinct values = %d, truncated = %v", len(stat.distinctValues), stat.distinctTruncated)
	}
	for i := range 257 {
		stat.addPivotColumn(strconv.Itoa(i))
	}
	if stat.distinctTruncated || len(stat.pivotColumns) != 257 {
		t.Fatalf("pivot columns = %d, truncated = %v", len(stat.pivotColumns), stat.distinctTruncated)
	}
}

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

func TestGenerationProfilerSeparatesCatalogDocumentsAndKeys(t *testing.T) {
	cache := NewShapePlanCache()
	payload := map[string]any{"id": "patient-1"}
	timings := map[string]float64{}

	legacy := NewProfiler("P1", "scopeA", "Patient", cache)
	generationA := NewProfilerForGeneration("P1", " generation/a ", "scopeA", "Patient", cache)
	generationB := NewProfilerForGeneration("P1", "generation a", "scopeA", "Patient", cache)
	canonicalGenerationA := NewProfilerForGeneration("P1", "generation/a", "scopeA", "Patient", cache)
	for _, profiler := range []*Profiler{legacy, generationA, generationB, canonicalGenerationA} {
		profiler.ObservePayload(payload, timings)
	}

	legacyDocument := catalogDocumentForPath(t, legacy.Documents(), "id")
	generationADocument := catalogDocumentForPath(t, generationA.Documents(), "id")
	generationBDocument := catalogDocumentForPath(t, generationB.Documents(), "id")
	canonicalGenerationADocument := catalogDocumentForPath(t, canonicalGenerationA.Documents(), "id")

	if got, want := legacyDocument.Key, fieldCatalogKey("P1", "scopeA", "Patient", "id"); got != want {
		t.Fatalf("legacy catalog key = %q, want established layout %q", got, want)
	}
	if legacyDocument.DatasetGeneration != "" {
		t.Fatalf("legacy document generation = %q, want empty legacy namespace", legacyDocument.DatasetGeneration)
	}
	if got, want := generationADocument.DatasetGeneration, "generation/a"; got != want {
		t.Fatalf("generation document generation = %q, want normalized %q", got, want)
	}
	if got, want := generationADocument.Key, canonicalGenerationADocument.Key; got != want {
		t.Fatalf("equivalent normalized generation produced different keys: %q != %q", got, want)
	}
	for _, key := range []string{generationADocument.Key, generationBDocument.Key} {
		if !strings.HasPrefix(key, generationFieldCatalogKeyPrefix) {
			t.Fatalf("generation catalog key %q does not use digest namespace %q", key, generationFieldCatalogKeyPrefix)
		}
		if got, want := len(key), len(generationFieldCatalogKeyPrefix)+64; got != want {
			t.Fatalf("generation catalog key length = %d, want SHA-256 key length %d", got, want)
		}
	}
	if generationADocument.Key == legacyDocument.Key || generationBDocument.Key == legacyDocument.Key || generationADocument.Key == generationBDocument.Key {
		t.Fatalf("catalog keys must isolate legacy and each generation: legacy=%q generationA=%q generationB=%q", legacyDocument.Key, generationADocument.Key, generationBDocument.Key)
	}

	// These generation values collide under sanitizeCollectionKey, which is why
	// a direct sanitized suffix would not be a safe immutable namespace.
	if got, want := sanitizeCollectionKey("generation/a"), sanitizeCollectionKey("generation a"); got != want {
		t.Fatalf("test setup expected sanitized generation collision: %q != %q", got, want)
	}
	if generationADocument.Key == generationBDocument.Key {
		t.Fatalf("digest keys collided for distinct generation identities")
	}
}

func TestLegacyProfilerConstructorAndKeyCompatibility(t *testing.T) {
	cache := NewShapePlanCache()
	payload := map[string]any{
		"id":     "patient-1",
		"active": true,
	}
	timings := map[string]float64{}

	legacy := NewProfiler(" Project One ", "scope A", "Patient", cache)
	emptyGeneration := NewProfilerForGeneration(" Project One ", " \t ", "scope A", "Patient", cache)
	legacy.ObservePayload(payload, timings)
	emptyGeneration.ObservePayload(payload, timings)

	legacyDocuments := legacy.Documents()
	emptyGenerationDocuments := emptyGeneration.Documents()
	if !reflect.DeepEqual(legacyDocuments, emptyGenerationDocuments) {
		t.Fatalf("legacy and empty-generation documents differ:\nlegacy=%+v\nempty=%+v", legacyDocuments, emptyGenerationDocuments)
	}
	for _, document := range legacyDocuments {
		if got, want := document.Key, fieldCatalogKey(" Project One ", "scope A", "Patient", document.Path); got != want {
			t.Fatalf("legacy key for %q = %q, want established layout %q", document.Path, got, want)
		}
		encoded, err := json.Marshal(document)
		if err != nil {
			t.Fatalf("marshal legacy document: %v", err)
		}
		if strings.Contains(string(encoded), `"dataset_generation"`) {
			t.Fatalf("legacy document JSON unexpectedly changed namespace: %s", encoded)
		}
	}
	if got, want := fieldCatalogKeyForGeneration("P1", "  ", "scopeA", "Patient", "id"), fieldCatalogKey("P1", "scopeA", "Patient", "id"); got != want {
		t.Fatalf("blank generation key = %q, want exact legacy key %q", got, want)
	}
}

func TestProfilerMergeRejectsIdentityMismatchBeforeMutatingStats(t *testing.T) {
	identity := func(project, generation, authResourcePath, resourceType string) *Profiler {
		return NewProfilerForGeneration(project, generation, authResourcePath, resourceType, NewShapePlanCache())
	}
	cases := []struct {
		name   string
		source *Profiler
	}{
		{name: "project", source: identity("P2", "generation-a", "scopeA", "Patient")},
		{name: "generation", source: identity("P1", "generation-b", "scopeA", "Patient")},
		{name: "auth resource path", source: identity("P1", "generation-a", "scopeB", "Patient")},
		{name: "resource type", source: identity("P1", "generation-a", "scopeA", "Observation")},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			destination := identity("P1", " generation-a ", "scopeA", "Patient")
			destination.ObservePayload(map[string]any{"id": "left"}, map[string]float64{})
			test.source.ObservePayload(map[string]any{"id": "right"}, map[string]float64{})
			before := destination.Documents()

			err := destination.Merge(test.source)
			if !errors.Is(err, ErrProfilerIdentityMismatch) {
				t.Fatalf("Merge() error = %v, want ErrProfilerIdentityMismatch", err)
			}
			if after := destination.Documents(); !reflect.DeepEqual(after, before) {
				t.Fatalf("rejected Merge() mutated destination:\nbefore=%+v\nafter=%+v", before, after)
			}
		})
	}
}

func TestProfilerMergeNormalizesGenerationBeforeComparisonAndPersistence(t *testing.T) {
	left := NewProfilerForGeneration("P1", " generation-a ", "scopeA", "Patient", NewShapePlanCache())
	right := NewProfilerForGeneration("P1", "generation-a", "scopeA", "Patient", NewShapePlanCache())
	left.ObservePayload(map[string]any{"id": "left"}, map[string]float64{})
	right.ObservePayload(map[string]any{"id": "right"}, map[string]float64{})

	if err := left.Merge(right); err != nil {
		t.Fatalf("Merge() normalized-equivalent generations: %v", err)
	}
	document := catalogDocumentForPath(t, left.Documents(), "id")
	if got, want := document.DatasetGeneration, "generation-a"; got != want {
		t.Fatalf("persisted generation = %q, want normalized %q", got, want)
	}
	if got, want := document.DocCount, int64(2); got != want {
		t.Fatalf("merged doc count = %d, want %d", got, want)
	}
}

func catalogDocumentForPath(t *testing.T, documents []FieldCatalogDocument, path string) FieldCatalogDocument {
	t.Helper()
	for _, document := range documents {
		if document.Path == path {
			return document
		}
	}
	t.Fatalf("catalog document path %q not found in %+v", path, documents)
	return FieldCatalogDocument{}
}
