package main

import "testing"

func TestCanonicalManifestResourceTypesRequiresCompleteShape(t *testing.T) {
	got, err := canonicalManifestResourceTypes([]string{"Observation", "Patient"})
	if err != nil || len(got) != 2 || got[0] != "Observation" || got[1] != "Patient" {
		t.Fatalf("canonical resource types = %#v err=%v", got, err)
	}
	for _, resourceTypes := range [][]string{nil, {"Patient", "Observation"}, {"Patient", "Patient"}, {"NotAResource"}} {
		if _, err := canonicalManifestResourceTypes(resourceTypes); err == nil {
			t.Fatalf("accepted invalid manifest resource types %#v", resourceTypes)
		}
	}
}
