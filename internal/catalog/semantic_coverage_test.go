package catalog

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/fhir/schema"
)

func TestSemanticCoverageCensusCountsOwnersAndCodingsByConcretePath(t *testing.T) {
	index, err := schema.GeneratedIndex()
	if err != nil {
		t.Fatalf("load generated FHIR schema: %v", err)
	}
	payload := json.RawMessage(`{
		"resourceType":"Observation",
		"status":"final",
		"code":{"coding":[{"system":"urn:loinc","code":"root"}]},
		"component":[
			{
				"code":{"coding":[{"system":"urn:test","code":"component-a"}]},
				"valueCodeableConcept":{"coding":[
					{"system":"urn:value","code":"one"},
					{"system":"urn:value","code":"two"},
					{"system":"urn:value","code":"three"}
				]}
			},
			{
				"code":{"coding":[{"system":"urn:test","code":"component-b"}]},
				"valueCodeableConcept":{"coding":[{"system":"urn:value","code":"three"}]}
			}
		],
		"extension":[{"url":"parent","extension":[
			{"url":"leaf","valueCodeableConcept":{"text":"nested text"}}
		]}]
	}`)

	census, err := CensusSemanticCoverage(index, "Observation", "source-1", payload)
	if err != nil {
		t.Fatalf("census semantic coverage: %v", err)
	}
	if census.Counts.CodeableConceptOwners != 6 {
		t.Fatalf("CodeableConcept owner count = %d, want 6", census.Counts.CodeableConceptOwners)
	}
	if census.Counts.CodingOccurrences != 7 {
		t.Fatalf("Coding occurrence count = %d, want 7", census.Counts.CodingOccurrences)
	}
	if census.Counts.CodeableConceptOwners == census.Counts.CodingOccurrences {
		t.Fatal("CodeableConcept owners and Coding occurrences were conflated")
	}

	var componentValueOwners []string
	for _, path := range census.Paths {
		if path.Kind != SemanticCoverageCodeableConcept || path.CanonicalPath != "component[].valueCodeableConcept" {
			continue
		}
		componentValueOwners = append(componentValueOwners, path.OwnerPathWitnesses...)
	}
	want := []string{
		"component[0].valueCodeableConcept",
		"component[1].valueCodeableConcept",
	}
	if !reflect.DeepEqual(componentValueOwners, want) {
		t.Fatalf("component value owner witnesses = %#v, want %#v", componentValueOwners, want)
	}
}
