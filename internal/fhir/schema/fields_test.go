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
