package capability

import "testing"

func TestIsRepeatedCardinality(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		value string
		want  bool
	}{
		{value: "required_one"},
		{value: "optional_one"},
		{value: "OPTIONAL_ONE"},
		{value: "scalar"},
		{value: "many", want: true},
		{value: "MANY", want: true},
		{value: "unknown_observed_many", want: true},
	} {
		if got := IsRepeatedCardinality(test.value); got != test.want {
			t.Errorf("IsRepeatedCardinality(%q) = %v, want %v", test.value, got, test.want)
		}
	}
}

func TestCloneConceptCandidatesDetachesNestedSlices(t *testing.T) {
	input := []ConceptCandidate{{
		SourcePath:       "Patient.extension",
		ExtensionURLPath: []string{"one"},
		ObservedUnits:    []string{"unit"},
		Examples:         []string{"example"},
	}}
	clone := CloneConceptCandidates(input)
	input[0].ExtensionURLPath[0] = "changed"
	input[0].ObservedUnits[0] = "changed"
	input[0].Examples[0] = "changed"
	if clone[0].ExtensionURLPath[0] != "one" || clone[0].ObservedUnits[0] != "unit" || clone[0].Examples[0] != "example" {
		t.Fatalf("clone shares nested candidate slices: %#v", clone[0])
	}
	if CloneConceptCandidates(nil) != nil || CloneConceptCandidates([]ConceptCandidate{}) != nil {
		t.Fatal("empty concept candidates must clone to nil")
	}
}
