package schema

import "testing"

func TestCorrelatedBindingPreservesStructuredValueTypes(t *testing.T) {
	for _, arm := range []string{"valuePeriod", "valueRange", "valueRatio", "valueAttachment"} {
		binding := CorrelatedBinding{OwnerPath: "input[]", KeyPath: "input[].type.coding[]", SystemPath: "system", CodePath: "code", ValuePath: arm, LogicalType: "object", ChoiceArms: []string{arm}}
		checked, err := ValidateCorrelatedBinding("Task", binding)
		if err != nil || checked.LogicalType != "object" || checked.ValuePrimitive != "" || checked.ValueSelector.CanonicalPath() != arm {
			t.Fatalf("%s: %#v, %v", arm, checked, err)
		}
		binding.LogicalType = "decimal"
		if _, err := ValidateCorrelatedBinding("Task", binding); err == nil {
			t.Fatalf("%s silently accepted a scalar type", arm)
		}
	}
}

func TestCorrelatedDisplayPolicyRequiresCodingValue(t *testing.T) {
	binding := CorrelatedBinding{KeyPath: "code.coding[]", SystemPath: "system", CodePath: "code", ValuePath: "valueString", LogicalType: "string", ValuePresentation: ValuePresentationDisplayOrCode}
	if _, err := ValidateCorrelatedBinding("Observation", binding); err == nil {
		t.Fatal("display policy accepted a non-Coding value")
	}
	binding.ValuePath = "valueCodeableConcept.coding[].code"
	if _, err := ValidateCorrelatedBinding("Observation", binding); err != nil {
		t.Fatal(err)
	}
	binding.ValuePresentation = "GUESS"
	if _, err := ValidateCorrelatedBinding("Observation", binding); err == nil {
		t.Fatal("unknown display policy accepted")
	}
}
