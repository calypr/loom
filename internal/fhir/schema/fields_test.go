package schema

import (
	"reflect"
	"sync"
	"testing"
)

func TestFieldsForResourceIncludesExpectedPaths(t *testing.T) {
	cases := []struct {
		resourceType string
		path         string
	}{
		{"Patient", "identifier[].value"},
		{"Patient", "extension[].valueCode"},
		{"Condition", "code.coding[].display"},
		{"Specimen", "type.coding[].display"},
		{"ResearchSubject", "study.reference"},
		{"DocumentReference", "content[].attachment.title"},
		{"Observation", "code.coding[].display"},
		{"ImagingStudy", "series[].instance[].uid"},
		{"MedicationAdministration", "status"},
		{"Group", "member[].entity.reference"},
		{"ResearchStudy", "identifier[].value"},
	}
	for _, tc := range cases {
		if _, ok := LookupField(tc.resourceType, tc.path); !ok {
			t.Fatalf("expected %s path %q to exist", tc.resourceType, tc.path)
		}
	}
}

var lookupBenchmarkField FieldSpec

func BenchmarkLookupField(b *testing.B) {
	const resource, path = "Observation", "code.coding[].display"
	expected, ok := LookupField(resource, path)
	if !ok || expected.Path != path {
		b.Fatal("lookup preflight failed")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		field, found := LookupField(resource, path)
		if !found || field.Path != path {
			b.Fatal("lookup failed")
		}
		lookupBenchmarkField = field
	}
}

func TestFieldAccessorsOwnReturnedSlices(t *testing.T) {
	original, ok := LookupField("Observation", "code.coding[].display")
	if !ok || len(original.PredicatePaths) == 0 {
		t.Fatal("missing predicate fixture")
	}
	want := append([]string(nil), original.PredicatePaths...)
	original.PredicatePaths[0] = "mutated"
	all := FieldsForResource("Observation")
	for i := range all {
		if all[i].Path == original.Path {
			if !reflect.DeepEqual(all[i].PredicatePaths, want) {
				t.Fatal("lookup mutation leaked")
			}
			all[i].PredicatePaths[0] = "mutated again"
			all[i].Path = "mutated path"
		}
	}
	restored, ok := LookupField("Observation", original.Path)
	if !ok || !reflect.DeepEqual(restored.PredicatePaths, want) {
		t.Fatal("field list mutation leaked")
	}
}
func TestFieldLookupParallelOwnership(t *testing.T) {
	var workers sync.WaitGroup
	for i := 0; i < 16; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 25; j++ {
				field, ok := LookupField("Patient", "identifier[].value")
				if !ok || len(field.PredicatePaths) == 0 {
					t.Error("missing field")
					return
				}
				field.PredicatePaths[0] = "private"
				fields := FieldsForResource("Patient")
				if len(fields) == 0 {
					t.Error("missing resource")
					return
				}
				fields[0].Path = "private"
			}
		}()
	}
	workers.Wait()
}
func TestUnknownFieldAndResource(t *testing.T) {
	for _, resource := range []string{"Patient", "NotAFHIRResource"} {
		field, ok := LookupField(resource, "nonexistent")
		if ok || !reflect.DeepEqual(field, FieldSpec{}) {
			t.Fatalf("unexpected lookup %#v %v", field, ok)
		}
	}
	if fields := FieldsForResource("NotAFHIRResource"); fields == nil || len(fields) != 0 {
		t.Fatalf("unknown resource fields %#v", fields)
	}
}

func TestResourceFieldCachePublishesOneInitializationAndSkipsUnknownNames(t *testing.T) {
	const resourceType = "Patient"
	previous, hadPrevious := resourceCache.Load(resourceType)
	resourceCache.Delete(resourceType)
	defer func() {
		resourceCache.Delete(resourceType)
		if hadPrevious {
			resourceCache.Store(resourceType, previous)
		}
	}()

	var workers sync.WaitGroup
	results := make(chan []FieldSpec, 16)
	for worker := 0; worker < cap(results); worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			results <- cachedResourceFields(resourceType).fields
		}()
	}
	workers.Wait()
	close(results)
	var expected []FieldSpec
	for fields := range results {
		if len(fields) == 0 {
			t.Fatal("known resource fields were empty")
		}
		if expected == nil {
			expected = fields
			continue
		}
		if !reflect.DeepEqual(fields, expected) {
			t.Fatal("concurrent callers observed different field definitions")
		}
	}
	entry, ok := resourceCache.Load(resourceType)
	if !ok || len(entry.(*resourceFieldsEntry).fields.byPath) == 0 {
		t.Fatal("known resource cache entry was not initialized")
	}

	const unknown = "RoundFourUnknownResource"
	cachedResourceFields(unknown)
	if _, ok := resourceCache.Load(unknown); ok {
		t.Fatal("unknown resource name was retained in the cache")
	}
}
