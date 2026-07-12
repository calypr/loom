package fhirschema

import "testing"

func TestResourceTypesExposeSchemaDerivedConcreteRootsOnly(t *testing.T) {
	for _, resourceType := range []string{
		"DiagnosticReport",
		"MedicationRequest",
		"MedicationStatement",
		"Procedure",
		"Task",
	} {
		if !HasResource(resourceType) {
			t.Errorf("HasResource(%q) = false; concrete schema root is missing", resourceType)
		}
	}
	for _, definition := range []string{"Address", "PatientContact", "Resource"} {
		if HasResource(definition) {
			t.Errorf("HasResource(%q) = true; backbone or abstract definition must not be a compiler root", definition)
		}
	}
}
