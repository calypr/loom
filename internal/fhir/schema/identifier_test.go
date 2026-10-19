package schema

import "testing"

func TestValidateIdentifierBindingUsesOneRepeatedIdentifierItem(t *testing.T) {
	binding := IdentifierBinding{
		OwnerPath: "identifier[]", SystemPath: "system", ValuePath: "value",
		SystemURI: " https://proteomic.datacommons.cancer.gov/pdc/case_id ", LogicalType: "string",
	}
	checked, err := ValidateIdentifierBinding("Patient", binding)
	if err != nil {
		t.Fatal(err)
	}
	if checked.OwnerResource != "Identifier" || checked.OwnerSelector.CanonicalPath() != "identifier[]" || checked.SystemSelector.CanonicalPath() != "system" || checked.ValueSelector.CanonicalPath() != "value" || checked.SystemURI != "https://proteomic.datacommons.cancer.gov/pdc/case_id" || checked.LogicalType != "string" {
		t.Fatalf("checked identifier binding = %#v", checked)
	}
}

func TestValidateIdentifierBindingRejectsUnpairedOrInvalidSelectors(t *testing.T) {
	base := IdentifierBinding{OwnerPath: "identifier[]", SystemPath: "system", ValuePath: "value", SystemURI: "urn:system", LogicalType: "string"}
	tests := map[string]func(*IdentifierBinding){
		"owner is not repeated Identifier":         func(binding *IdentifierBinding) { binding.OwnerPath = "name[]" },
		"system selector is not Identifier.system": func(binding *IdentifierBinding) { binding.SystemPath = "value" },
		"value selector is not Identifier.value":   func(binding *IdentifierBinding) { binding.ValuePath = "system" },
		"namespace is absent":                      func(binding *IdentifierBinding) { binding.SystemURI = " " },
		"logical type is incompatible":             func(binding *IdentifierBinding) { binding.LogicalType = "decimal" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			binding := base
			mutate(&binding)
			if _, err := ValidateIdentifierBinding("Patient", binding); err == nil {
				t.Fatalf("invalid binding was accepted: %#v", binding)
			}
		})
	}
}
