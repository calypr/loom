package schema

import "testing"

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

func TestLookupFieldDoesNotExposeCachedPredicatePaths(t *testing.T) {
	var path string
	for _, field := range FieldsForResource("Observation") {
		if len(field.PredicatePaths) > 0 {
			path = field.Path
			break
		}
	}
	if path == "" {
		t.Fatal("Observation has no field with predicate paths")
	}

	first, ok := LookupField("Observation", path)
	if !ok {
		t.Fatalf("field %q disappeared", path)
	}
	want := first.PredicatePaths[0]
	first.PredicatePaths[0] = "mutated-by-caller"
	second, ok := LookupField("Observation", path)
	if !ok || second.PredicatePaths[0] != want {
		t.Fatalf("cached field changed after caller mutation: %#v, found=%t", second, ok)
	}
}
