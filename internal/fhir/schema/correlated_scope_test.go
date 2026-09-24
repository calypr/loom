package schema

import "testing"

func TestCorrelatedBindingAcceptsSingletonCodingKey(t *testing.T) {
	binding := CorrelatedBinding{OwnerPath: "extension[].valueUsageContext", KeyPath: "extension[].valueUsageContext.code", SystemPath: "system", CodePath: "code", ValuePath: "valueQuantity.value", LogicalType: "decimal", ChoiceArms: []string{"valueQuantity"}}
	checked, err := ValidateCorrelatedBinding("Patient", binding)
	if err != nil {
		t.Fatal(err)
	}
	if checked.KeyResource != "Coding" || checked.KeySelector.CanonicalPath() != "code" {
		t.Fatalf("wrong key scope: %+v", checked)
	}
}

func TestCorrelatedBindingRejectsNonCodingObject(t *testing.T) {
	_, err := ValidateCorrelatedBinding("Observation", CorrelatedBinding{KeyPath: "subject", SystemPath: "display", CodePath: "reference", ValuePath: "valueString", LogicalType: "string"})
	if err == nil {
		t.Fatal("Reference object accepted as a Coding key")
	}
}
