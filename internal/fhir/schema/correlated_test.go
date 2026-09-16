package schema

import (
	"strings"
	"testing"
)

func TestValidateCorrelatedBindingKeepsCodingWithinComponent(t *testing.T) {
	binding := CorrelatedBinding{
		OwnerPath: "component[]", KeyPath: "component[].code.coding[]",
		SystemPath: "system", CodePath: "code", ValuePath: "valueQuantity.value",
		ChoiceArms: []string{"valueQuantity"}, LogicalType: "decimal", UnitPath: "valueQuantity.unit",
	}
	checked, err := ValidateCorrelatedBinding("Observation", binding)
	if err != nil {
		t.Fatal(err)
	}
	if checked.OwnerResource != "ObservationComponent" || checked.KeyResource != "Coding" || checked.KeySelector.CanonicalPath() != "code.coding[]" {
		t.Fatalf("checked binding = %#v", checked)
	}
	if checked.ValueSelector.CanonicalPath() != "valueQuantity.value" || checked.UnitSelector == nil {
		t.Fatalf("checked value selectors = %#v", checked)
	}
}

func TestValidateCorrelatedBindingRejectsCrossScopeAndImplicitMixedChoice(t *testing.T) {
	base := CorrelatedBinding{
		OwnerPath: "component[]", KeyPath: "component[].code.coding[]",
		SystemPath: "system", CodePath: "code", ValuePath: "valueQuantity.value", LogicalType: "decimal",
	}
	for name, binding := range map[string]CorrelatedBinding{
		"key outside owner": func() CorrelatedBinding {
			copy := base
			copy.KeyPath = "code.coding[]"
			return copy
		}(),
		"mixed non-string": func() CorrelatedBinding {
			copy := base
			copy.ChoiceArms = []string{"valueQuantity", "valueString"}
			return copy
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ValidateCorrelatedBinding("Observation", binding); err == nil {
				t.Fatal("binding unexpectedly validated")
			} else if name == "mixed non-string" && !strings.Contains(err.Error(), "mixed choice") {
				t.Fatalf("error = %v, want mixed choice", err)
			}
		})
	}
}

func TestValidateCorrelatedBindingRejectsTypeAndChoiceOverrides(t *testing.T) {
	base := CorrelatedBinding{
		OwnerPath: "component[]", KeyPath: "component[].code.coding[]",
		SystemPath: "system", CodePath: "code", ValuePath: "valueQuantity.value", LogicalType: "decimal",
	}
	for name, mutate := range map[string]func(*CorrelatedBinding){
		"wrong logical type":   func(binding *CorrelatedBinding) { binding.LogicalType = "boolean" },
		"unknown logical type": func(binding *CorrelatedBinding) { binding.LogicalType = "money" },
		"unknown choice arm":   func(binding *CorrelatedBinding) { binding.ChoiceArms = []string{"valueMadeUp"} },
		"value outside choice arms": func(binding *CorrelatedBinding) {
			binding.ChoiceArms = []string{"valueString"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			binding := base
			mutate(&binding)
			if _, err := ValidateCorrelatedBinding("Observation", binding); err == nil {
				t.Fatal("binding unexpectedly validated")
			}
		})
	}
}

func TestValidateExtensionBindingKeepsNestedURLAncestry(t *testing.T) {
	binding := ExtensionBinding{
		OwnerPath: "extension[].extension[]", URLPath: []string{"urn:parent:left", "urn:leaf"},
		ValuePath: "valueString", LogicalType: "string", ChoiceArms: []string{"valueString"},
	}
	checked, err := ValidateExtensionBinding("Observation", binding)
	if err != nil {
		t.Fatal(err)
	}
	if checked.OwnerSelector.CanonicalPath() != "" || len(checked.URLSelectors) != 2 || checked.OwnerResource != "Extension" || checked.ValueSelector.CanonicalPath() != "valueString" {
		t.Fatalf("checked extension binding = %#v", checked)
	}
	if _, err := ValidateExtensionBinding("Observation", ExtensionBinding{
		OwnerPath: "extension[].extension[]", URLPath: []string{"urn:parent:left", "urn:leaf"}, ValuePath: "valueInteger", LogicalType: "string",
	}); err == nil {
		t.Fatal("wrong value arm unexpectedly accepted")
	}
	if _, err := ValidateExtensionBinding("Observation", ExtensionBinding{
		OwnerPath: "extension[].extension[]", URLPath: []string{"urn:parent:left"}, ValuePath: "valueString", LogicalType: "string",
	}); err == nil {
		t.Fatal("missing ancestor URL unexpectedly accepted")
	}
}

func TestValidateExtensionBindingSupportsRepeatedBaseOwner(t *testing.T) {
	binding := ExtensionBinding{OwnerPath: "component[].extension[]", URLPath: []string{"urn:leaf"}, ValuePath: "valueString", LogicalType: "string"}
	checked, err := ValidateExtensionBinding("Observation", binding)
	if err != nil {
		t.Fatal(err)
	}
	if checked.OwnerSelector.CanonicalPath() != "component[]" || len(checked.URLSelectors) != 1 {
		t.Fatalf("checked base owner = %#v", checked)
	}
}
