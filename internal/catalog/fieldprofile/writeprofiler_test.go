package fieldprofile

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/catalog/fieldcatalog"
	fhirbinding "github.com/calypr/loom/internal/fhir/binding"
	"github.com/calypr/loom/internal/semantic"
	"github.com/calypr/loom/internal/testkit/semanticfixtures"
)

func NewShapePlanCache() *ShapePlanCache {
	return NewShapePlanCacheWithLimits(DefaultProfileLimits().MaxShapePlans, 0)
}

func NewProfilerForGeneration(project, datasetGeneration, authResourcePath, resourceType string, cache *ShapePlanCache) *Profiler {
	return NewProfilerForGenerationWithLimits(project, datasetGeneration, authResourcePath, resourceType, cache, DefaultProfileLimits())
}

func TestFieldCatalogRetainsAllDynamicKeys(t *testing.T) {
	profiler := NewProfilerForGeneration("project", "generation", "", "Observation", NewShapePlanCache())
	stat := fieldCatalogStats{distinctSet: make(map[string]struct{}), pivotColumnSet: make(map[string]struct{})}
	for i := range 257 {
		profiler.addDistinctWithLimits(&stat, strconv.Itoa(i))
	}
	if stat.distinctTruncated || len(stat.distinctValues) != 257 {
		t.Fatalf("distinct values = %d, truncated = %v", len(stat.distinctValues), stat.distinctTruncated)
	}
	for i := range 257 {
		profiler.addPivotColumnWithLimits(&stat, strconv.Itoa(i))
	}
	if stat.distinctTruncated || len(stat.pivotColumns) != 257 {
		t.Fatalf("pivot columns = %d, truncated = %v", len(stat.pivotColumns), stat.distinctTruncated)
	}
}

func TestSharedShapePlansKeepRetentionBudgetsPerProfiler(t *testing.T) {
	limits := DefaultProfileLimits()
	limits.MaxShapePlans = 1
	limits.MaxRetainedBytes = 1 << 20
	shared := NewShapePlanCacheWithLimits(limits.MaxShapePlans, limits.MaxRetainedBytes)
	first := NewProfilerForGenerationWithLimits("project", "generation", "", "Patient",
		NewShapePlanCacheWithSharedPlans(limits.MaxShapePlans, limits.MaxRetainedBytes, shared), limits)
	for _, payload := range []map[string]any{
		{"resourceType": "Patient", "id": "one"},
		{"resourceType": "Patient", "id": "two", "active": true},
	} {
		if err := first.ObservePayload(payload, map[string]float64{}); err != nil {
			t.Fatal(err)
		}
	}
	if !first.Truncated() {
		t.Fatal("first profiler did not reach its one-plan cache limit")
	}
	second := NewProfilerForGenerationWithLimits("project", "generation", "", "Patient",
		NewShapePlanCacheWithSharedPlans(limits.MaxShapePlans, limits.MaxRetainedBytes, shared), limits)
	if err := second.ObservePayload(map[string]any{"resourceType": "Patient", "id": "one"}, map[string]float64{}); err != nil {
		t.Fatal(err)
	}
	if second.Truncated() {
		t.Fatal("another file's shape cache truncation leaked into this profiler")
	}
}

func TestFieldCatalogProfilerBoundsHighCardinalityValues(t *testing.T) {
	limits := ProfileLimits{
		MaxFields:                  8,
		MaxDistinctValuesPerField:  2,
		MaxDistinctValueBytes:      4,
		MaxPivotColumnsPerField:    2,
		MaxExtensionValuesPerField: 2,
		MaxShapePlans:              1,
	}
	profiler := NewProfilerForGenerationWithLimits("TEST", "generation-a", "", "Patient", nil, limits)
	for _, id := range []string{"a", "b", "c", "too-long"} {
		profiler.ObservePayload(map[string]any{"id": id}, map[string]float64{})
	}

	var idField fieldcatalog.FieldCatalogDocument
	for _, document := range profiler.Documents() {
		if document.Path == "id" {
			idField = document
			break
		}
	}
	if len(idField.DistinctValues) != 2 || !idField.DistinctTruncated {
		t.Fatalf("bounded id catalog = %#v, want two values and truncation", idField)
	}
}

func TestFieldCatalogProfilerBoundsAggregateRetainedBytes(t *testing.T) {
	limits := ProfileLimits{
		MaxFields:                  8,
		MaxDistinctValuesPerField:  8,
		MaxDistinctValueBytes:      64,
		MaxPivotColumnsPerField:    8,
		MaxExtensionValuesPerField: 8,
		MaxShapePlans:              8,
		MaxRetainedBytes:           300,
	}
	profiler := NewProfilerForGenerationWithLimits("TEST", "generation-a", "", "Patient", nil, limits)
	for _, value := range []string{"one", "two", "three", "four"} {
		profiler.ObservePayload(map[string]any{"id": value}, map[string]float64{})
	}

	idField := catalogDocumentForPath(t, profiler.Documents(), "id")
	if profiler.RetentionBytes() > limits.MaxRetainedBytes {
		t.Fatalf("retained bytes = %d, want at most %d", profiler.RetentionBytes(), limits.MaxRetainedBytes)
	}
	if !profiler.Truncated() || !idField.DistinctTruncated {
		t.Fatalf("profiler truncation = %v, id field = %#v, want aggregate truncation evidence", profiler.Truncated(), idField)
	}
}

func TestFieldCatalogProfilerMergeHonorsAggregateRetainedBytes(t *testing.T) {
	limits := ProfileLimits{
		MaxFields:                  8,
		MaxDistinctValuesPerField:  8,
		MaxDistinctValueBytes:      64,
		MaxPivotColumnsPerField:    8,
		MaxExtensionValuesPerField: 8,
		MaxShapePlans:              8,
		MaxRetainedBytes:           300,
	}
	cache := NewShapePlanCacheWithLimits(limits.MaxShapePlans, limits.MaxRetainedBytes)
	left := NewProfilerForGenerationWithLimits("TEST", "generation-a", "", "Patient", cache, limits)
	right := NewProfilerForGenerationWithLimits("TEST", "generation-a", "", "Patient", cache, limits)
	left.ObservePayload(map[string]any{"id": "left"}, map[string]float64{})
	right.ObservePayload(map[string]any{"id": "right"}, map[string]float64{})

	if err := left.Merge(right); err != nil {
		t.Fatal(err)
	}
	if left.RetentionBytes() > limits.MaxRetainedBytes || !left.Truncated() {
		t.Fatalf("merged profiler retained bytes = %d, truncated = %v, want bounded truncated result", left.RetentionBytes(), left.Truncated())
	}
}

func TestShapePlanCacheHonorsLimit(t *testing.T) {
	cache := NewShapePlanCacheWithLimits(1, 0)
	cache.getOrBuild("shape-a", map[string]any{"id": "a"})
	cache.getOrBuild("shape-b", map[string]any{"name": "b"})
	if got := len(cache.plans); got != 1 {
		t.Fatalf("shape plan cache size = %d, want 1", got)
	}
}

func TestShapePlanCacheHonorsByteLimit(t *testing.T) {
	cache := NewShapePlanCacheWithLimits(8, 100)
	cache.getOrBuild("shape-a", map[string]any{"id": "a", "name": "b"})
	if got := cache.retainedBytes; got > cache.maxBytes {
		t.Fatalf("shape plan cache bytes = %d, want at most %d", got, cache.maxBytes)
	}
	cache.getOrBuild("shape-b", map[string]any{"long": strings.Repeat("value", 32)})
	if got := cache.retainedBytes; got > cache.maxBytes {
		t.Fatalf("shape plan cache bytes after oversized plan = %d, want at most %d", got, cache.maxBytes)
	}
}

func TestFieldCatalogProfilerRecordsExactNestedArrayMaxima(t *testing.T) {
	profiler := NewProfilerForGeneration("TEST", "generation-a", "", "Patient", nil)
	profiler.ObservePayload(map[string]any{
		"name": []any{
			map[string]any{"given": []any{"A", "B"}},
			map[string]any{"given": []any{"C", "D", "E"}},
		},
	}, map[string]float64{})

	maxima := map[string]int{}
	for _, document := range profiler.Documents() {
		if document.Kind == fieldKindArray {
			maxima[document.Path] = document.MaxItems
		}
	}
	if maxima["name[]"] != 2 || maxima["name[].given[]"] != 3 {
		t.Fatalf("array maxima = %#v, want name=2 and given=3", maxima)
	}
}

func TestFieldCatalogProfilerCanonicalPaths(t *testing.T) {
	cache := NewShapePlanCache()
	profiler := NewProfilerForGeneration("TEST", "", "pathA", "Observation", cache)
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

func TestFieldCatalogProfilerCorrelatesExtensionValuesRecursively(t *testing.T) {
	profiler := NewProfilerForGeneration("TEST", "generation-1", "pathA", "DocumentReference", NewShapePlanCache())
	profiler.ObservePayload(map[string]any{
		"content": []any{map[string]any{
			"attachment": map[string]any{"url": "file:///not-an-extension-url", "extension": []any{map[string]any{
				"url":       "http://example.org/source_path",
				"valueUrl":  "https://source.example/file",
				"extension": []any{map[string]any{"url": "http://example.org/sha256", "valueString": "abc123"}},
			}}},
		}},
	}, map[string]float64{})

	byPath := make(map[string]fieldcatalog.FieldCatalogDocument)
	for _, document := range profiler.Documents() {
		byPath[document.Path] = document
	}
	outer := byPath["content[].attachment.extension[].url"]
	if len(outer.ExtensionValues) != 1 || !reflect.DeepEqual(outer.ExtensionValues[0], fieldcatalog.ExtensionValueObservation{URL: "http://example.org/source_path", SourcePath: "content[].attachment.extension[]", ValuePath: "valueUrl", ValueType: "string"}) {
		t.Fatalf("outer extension observations = %#v", outer.ExtensionValues)
	}
	inner := byPath["content[].attachment.extension[].extension[].url"]
	if len(inner.ExtensionValues) != 1 || inner.ExtensionValues[0].SourcePath != "content[].attachment.extension[].extension[]" || inner.ExtensionValues[0].ValuePath != "valueString" {
		t.Fatalf("nested extension observations = %#v", inner.ExtensionValues)
	}
}

func TestFieldCatalogProfilerSkipsLoomMetadata(t *testing.T) {
	profiler := NewProfilerForGeneration("TEST", "", "pathA", "Patient", NewShapePlanCache())
	profiler.ObservePayload(map[string]any{
		"id":                 "patient-1",
		"project_id":         "TEST",
		"auth_resource_path": "pathA",
		"dataset_generation": "generation-1",
	}, map[string]float64{})

	for _, doc := range profiler.Documents() {
		if isLoomMetadataField(doc.Path) {
			t.Fatalf("Loom metadata entered FHIR field catalog: %q", doc.Path)
		}
	}
}

func TestFieldCatalogShapeCacheReusesPlans(t *testing.T) {
	cache := NewShapePlanCache()
	profiler := NewProfilerForGeneration("TEST", "", "pathA", "Patient", cache)
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
	docByPath := make(map[string]fieldcatalog.FieldCatalogDocument)
	for _, doc := range docs {
		docByPath[doc.Path] = doc
	}
	if got := docByPath["identifier[].value"].DocCount; got != 2 {
		t.Fatalf("identifier[].value doc_count = %d, want 2", got)
	}
}

func TestFieldCatalogCodeableConceptPivotMetadata(t *testing.T) {
	cache := NewShapePlanCache()
	profiler := NewProfilerForGeneration("TEST", "", "pathA", "Observation", cache)
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
	var found fieldcatalog.FieldCatalogDocument
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
	if found.PivotFamily != fhirbinding.PivotFamilyCodeableConcept {
		t.Fatalf("pivot family = %q, want %q", found.PivotFamily, fhirbinding.PivotFamilyCodeableConcept)
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
	profiler := NewProfilerForGeneration("TEST", "", "pathA", "Observation", cache)
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
	var found fieldcatalog.FieldCatalogDocument
	for _, doc := range docs {
		if doc.Path == "code" {
			found = doc
			break
		}
	}
	if found.PivotKind != pivotKindObservation {
		t.Fatalf("pivot kind = %q, want %q", found.PivotKind, pivotKindObservation)
	}
	if found.PivotFamily != fhirbinding.PivotFamilyObservationCodeValue {
		t.Fatalf("pivot family = %q, want %q", found.PivotFamily, fhirbinding.PivotFamilyObservationCodeValue)
	}
	if found.PivotColumnSelect != "code.coding[].display" {
		t.Fatalf("unexpected column selector: %q", found.PivotColumnSelect)
	}
	if found.PivotValueSelect != "valueQuantity.value" {
		t.Fatalf("unexpected value selector: %q", found.PivotValueSelect)
	}
}

func TestProfilerCoverageRetainsRawCodingPathWithUnrepresentedMetadata(t *testing.T) {
	profiler := NewProfilerForGeneration("project", "generation", "scope-a", "Observation", nil)
	inventoryProfiler := NewProfilerForGeneration("project", "generation", "scope-a", "Observation", nil)
	payload := map[string]any{
		"resourceType": "Observation",
		"status":       "final",
		"code": map[string]any{"coding": []any{map[string]any{
			"system": "urn:test", "version": "v1", "code": "one", "userSelected": true,
		}}},
	}
	var contribution SemanticCoverageSourceContribution
	var coverageSemantic []semantic.Contribution
	err := profiler.ObservePayloadWithCoverage(
		payload,
		map[string]float64{},
		"retained:Observation/raw-coding",
		"schema-sha256-test",
		func(value semantic.Contribution) { coverageSemantic = append(coverageSemantic, value) },
		nil,
		func(value SemanticCoverageSourceContribution) error {
			contribution = value
			return nil
		},
	)
	if err != nil {
		t.Fatalf("observe payload with coverage: %v", err)
	}
	var inventorySemantic []semantic.Contribution
	inventoryProfiler.ObservePayloadWithInventory(payload, map[string]float64{}, "retained:Observation/raw-coding", func(value semantic.Contribution) {
		inventorySemantic = append(inventorySemantic, value)
	})
	if !reflect.DeepEqual(profiler.Documents(), inventoryProfiler.Documents()) {
		t.Fatalf("coverage field documents differ from inventory-only output\ncoverage: %#v\ninventory: %#v", profiler.Documents(), inventoryProfiler.Documents())
	}
	if !reflect.DeepEqual(coverageSemantic, inventorySemantic) {
		t.Fatalf("coverage semantic inventory differs from inventory-only output\ncoverage: %#v\ninventory: %#v", coverageSemantic, inventorySemantic)
	}
	for _, path := range contribution.Paths {
		if path.Kind == SemanticCoverageCoding && path.CanonicalPath == "code.coding[]" {
			if path.Disposition != SemanticCoverageRawRetainedDisposition || path.Reason != SemanticCoverageReasonUnrepresentedMetadata {
				t.Fatalf("coding path disposition/reason = %q/%q, want raw retained for unrepresented metadata", path.Disposition, path.Reason)
			}
			return
		}
	}
	t.Fatalf("coverage omitted raw-retained code.coding[] path: %#v", contribution.Paths)
}

func TestGenerationProfilerSeparatesCatalogDocumentsAndKeys(t *testing.T) {
	cache := NewShapePlanCache()
	payload := map[string]any{"id": "patient-1"}
	timings := map[string]float64{}

	legacy := NewProfilerForGeneration("P1", "", "scopeA", "Patient", cache)
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

	legacy := NewProfilerForGeneration(" Project One ", "", "scope A", "Patient", cache)
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

func catalogDocumentForPath(t *testing.T, documents []fieldcatalog.FieldCatalogDocument, path string) fieldcatalog.FieldCatalogDocument {
	t.Helper()
	for _, document := range documents {
		if document.Path == path {
			return document
		}
	}
	t.Fatalf("catalog document path %q not found in %+v", path, documents)
	return fieldcatalog.FieldCatalogDocument{}
}

func (p *Profiler) RetentionBytes() int {
	if p.budget != nil {
		return p.budget.usage()
	}
	return p.retainedBytes
}

func (b *retentionBudget) usage() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

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
				!observation.Value.FallbackOnBlankString || observation.Status != semantic.StatusUnsupportedValueProjection || len(observation.Examples) != 0 {
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

func observeCodeableConceptValue(t *testing.T, value map[string]any) semantic.Observation {
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
			if observation.RuleHint == semantic.RuleHintCodedValueV1 && observation.Key.Code == "stage" {
				return observation
			}
		}
	}
	t.Fatal("Observation value was not retained in the semantic catalog")
	return semantic.Observation{}
}

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
		var contributions []semantic.Contribution
		err := profiler.ObservePayloadWithInventory(
			map[string]any{
				"resourceType": "Observation",
				"component": []any{
					semanticfixtures.InventoryComponent(sourceID+"-a", "v1", "label"),
					semanticfixtures.InventoryComponent(sourceID+"-b", "v1", "label"),
				},
			},
			map[string]float64{},
			sourceID,
			func(contribution semantic.Contribution) {
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
			if contribution.Key != wantKeys[index][ordinal] || contribution.Ordinal != ordinal || contribution.SourceKind != semantic.SourceFile {
				t.Fatalf("source %q contribution identity = key %q ordinal %d source kind %q, want key %q ordinal %d source kind %q", sourceID, contribution.Key, contribution.Ordinal, contribution.SourceKind, wantKeys[index][ordinal], ordinal, semantic.SourceFile)
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

func TestProfilerDistinctLimitDoesNotLoseFieldCoverage(t *testing.T) {
	limits := DefaultProfileLimits()
	limits.MaxDistinctValuesPerField = 1
	profiler := NewProfilerForGenerationWithLimits("p", "g", "scope", "Patient", nil, limits)
	profiler.ObservePayload(map[string]any{"id": "one"}, map[string]float64{})
	profiler.ObservePayload(map[string]any{"id": "two"}, map[string]float64{})

	documents := profiler.Documents()
	if len(documents) != 1 || documents[0].EnrichmentComplete == nil || !*documents[0].EnrichmentComplete {
		t.Fatalf("distinct cap field coverage = %#v", documents)
	}
	if !documents[0].DistinctTruncated || documents[0].EnrichmentTruncated {
		t.Fatalf("distinct cap flags = %#v", documents[0])
	}
}

func TestProfilerDroppedFieldPathsPersistIncompleteCoverage(t *testing.T) {
	limits := DefaultProfileLimits()
	limits.MaxFields = 2
	source := NewProfilerForGenerationWithLimits("p", "g", "scope", "Patient", nil, limits)
	source.ObservePayload(map[string]any{"id": "one", "active": true}, map[string]float64{})

	mergedLimits := limits
	mergedLimits.MaxFields = 1
	merged := NewProfilerForGenerationWithLimits("p", "g", "scope", "Patient", nil, mergedLimits)
	if err := merged.Merge(source); err != nil {
		t.Fatal(err)
	}
	documents := merged.Documents()
	if len(documents) != 1 || documents[0].EnrichmentComplete == nil || *documents[0].EnrichmentComplete || !documents[0].EnrichmentTruncated {
		t.Fatalf("dropped path flags = %#v", documents)
	}

	zeroLimits := DefaultProfileLimits()
	zeroLimits.MaxRetainedBytes = 1
	zero := NewProfilerForGenerationWithLimits("p", "g", "scope", "Patient", nil, zeroLimits)
	zero.ObservePayload(map[string]any{"id": "one"}, map[string]float64{})
	status := zero.Documents()
	if len(status) != 1 || status[0].RecordType != FieldProfileStatusRecordType || status[0].EnrichmentComplete == nil || *status[0].EnrichmentComplete || !status[0].EnrichmentTruncated {
		t.Fatalf("zero-row status marker = %#v", status)
	}
}
