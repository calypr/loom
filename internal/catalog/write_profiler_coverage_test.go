package catalog

import (
	"testing"
)

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
