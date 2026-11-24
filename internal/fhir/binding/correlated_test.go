package binding

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/fhir/schema"
)

func TestValidateCorrelatedBindingKeepsCodingWithinComponent(t *testing.T) {
	binding := CorrelatedBinding{
		OwnerPath: "component[]", KeyPath: "component[].code.coding[]",
		SystemPath: "system", CodePath: "code", ValuePath: "valueQuantity.value",
		ChoiceArms: []string{"valueQuantity"}, LogicalType: "decimal",
	}
	checked, err := ValidateCorrelatedBinding("Observation", binding)
	if err != nil {
		t.Fatal(err)
	}
	if checked.OwnerResource != "ObservationComponent" || checked.KeyResource != "Coding" || checked.KeySelector.CanonicalPath() != "code.coding[]" {
		t.Fatalf("checked binding = %#v", checked)
	}
	if checked.ValueSelector.CanonicalPath() != "valueQuantity.value" || checked.UnitSelector == nil || checked.UnitSelector.CanonicalPath() != "valueQuantity.unit" {
		t.Fatalf("checked value selectors = %#v", checked)
	}
}

func TestValidateCorrelatedBindingOptsIntoBlankStringFallbackOnlyWhenDeclared(t *testing.T) {
	binding := CorrelatedBinding{
		KeyPath: "code.coding[]", SystemPath: "system", CodePath: "code",
		ValuePath: "valueCodeableConcept.text", ValueFallback: []string{"valueCodeableConcept.coding[].display"},
		FallbackOnBlankString: true, ChoiceArms: []string{"valueCodeableConcept"}, LogicalType: "string",
	}
	checked, err := ValidateCorrelatedBinding("Observation", binding)
	if err != nil {
		t.Fatal(err)
	}
	if !checked.FallbackOnBlankString || len(checked.ValueFallbacks) != 1 || checked.ValueFallbacks[0].CanonicalPath() != "valueCodeableConcept.coding[].display" {
		t.Fatalf("checked CodeableConcept fallback = %#v", checked)
	}
	binding.ValueFallback = nil
	if _, err := ValidateCorrelatedBinding("Observation", binding); err == nil {
		t.Fatal("blank-string fallback without a fallback selector was accepted")
	}
	binding.FallbackOnBlankString = false
	binding.ValueFallback = []string{"valueCodeableConcept.coding[].display"}
	checked, err = ValidateCorrelatedBinding("Observation", binding)
	if err != nil || checked.FallbackOnBlankString {
		t.Fatalf("default correlated binding = %#v, %v; want opt-in disabled", checked, err)
	}
}

func TestValidateCorrelatedBindingUsesMatchedCodingValueScope(t *testing.T) {
	binding := CorrelatedBinding{
		OwnerPath: "component[]", KeyPath: "component[].code.coding[]",
		ValueScope: CorrelatedValueScopeCoding, SystemPath: "system", CodePath: "code",
		ValuePath: "display", ValueFallback: []string{"code"}, FallbackOnBlankString: true, LogicalType: "string",
	}
	checked, err := ValidateCorrelatedBinding("Observation", binding)
	if err != nil {
		t.Fatal(err)
	}
	if checked.ValueScope != CorrelatedValueScopeCoding || checked.KeyResource != "Coding" ||
		checked.ValueSelector.CanonicalPath() != "display" || len(checked.ValueFallbacks) != 1 || checked.ValueFallbacks[0].CanonicalPath() != "code" ||
		!checked.FallbackOnBlankString || checked.ValuePrimitive != schema.PrimitiveString || checked.UnitSelector != nil {
		t.Fatalf("checked same-Coding value binding = %#v", checked)
	}
}

func TestValidateCorrelatedBindingUsesCodingVersionSelector(t *testing.T) {
	binding := CorrelatedBinding{
		OwnerPath: "component[]", KeyPath: "component[].code.coding[]", VersionPath: "version",
		ValueScope: CorrelatedValueScopeCoding, SystemPath: "system", CodePath: "code",
		ValuePath: "display", LogicalType: "string",
	}
	checked, err := ValidateCorrelatedBinding("Observation", binding)
	if err != nil {
		t.Fatal(err)
	}
	if checked.VersionSelector == nil || checked.VersionSelector.CanonicalPath() != "version" {
		t.Fatalf("version selector = %#v, want Coding.version", checked.VersionSelector)
	}

	for name, path := range map[string]string{
		"wrong Coding field":   "display",
		"owner-relative field": "valueQuantity.value",
	} {
		t.Run(name, func(t *testing.T) {
			invalid := binding
			invalid.VersionPath = path
			if _, err := ValidateCorrelatedBinding("Observation", invalid); err == nil {
				t.Fatalf("versionPath %q unexpectedly validated", path)
			}
		})
	}

	binding.ValueScope = CorrelatedValueScopeOwner
	if _, err := ValidateCorrelatedBinding("Observation", binding); err == nil || !strings.Contains(err.Error(), "requires CODING") {
		t.Fatalf("owner-scoped versionPath error = %v", err)
	}
}

func TestValidateCorrelatedBindingSupportsSingletonCodingValueScope(t *testing.T) {
	binding := CorrelatedBinding{
		OwnerPath: "input[]", KeyPath: "input[].valueCoding", VersionPath: "version",
		ValueScope: CorrelatedValueScopeCoding, SystemPath: "system", CodePath: "code",
		ValuePath: "display", LogicalType: "string",
	}
	checked, err := ValidateCorrelatedBinding("Task", binding)
	if err != nil {
		t.Fatal(err)
	}
	if checked.OwnerResource != "TaskInput" || checked.KeyResource != "Coding" || checked.KeyIsArray ||
		checked.KeySelector.CanonicalPath() != "valueCoding" || checked.VersionSelector == nil || checked.VersionSelector.CanonicalPath() != "version" {
		t.Fatalf("checked singleton Coding binding = %#v", checked)
	}

	binding.ValueScope = CorrelatedValueScopeOwner
	binding.VersionPath = ""
	if _, err := ValidateCorrelatedBinding("Task", binding); err == nil || !strings.Contains(err.Error(), "repeated Coding item") {
		t.Fatalf("owner-scoped singleton Coding key error = %v", err)
	}
}

func TestValidateCorrelatedBindingCodingValueScopeRejectsOwnerPathsAndUnknownScopes(t *testing.T) {
	base := CorrelatedBinding{
		OwnerPath: "component[]", KeyPath: "component[].code.coding[]",
		ValueScope: CorrelatedValueScopeCoding, SystemPath: "system", CodePath: "code",
		ValuePath: "display", LogicalType: "string",
	}
	for name, mutate := range map[string]func(*CorrelatedBinding){
		"owner value path": func(binding *CorrelatedBinding) { binding.ValuePath = "valueQuantity.value" },
		"owner fallback path": func(binding *CorrelatedBinding) {
			binding.ValueFallback = []string{"valueCodeableConcept.coding[].display"}
		},
		"unknown scope": func(binding *CorrelatedBinding) { binding.ValueScope = "SIBLING" },
		"non-Coding key resource": func(binding *CorrelatedBinding) {
			binding.OwnerPath = "extension[]"
			binding.KeyPath = "extension[].extension[]"
			binding.SystemPath = "url"
			binding.CodePath = "url"
		},
	} {
		t.Run(name, func(t *testing.T) {
			binding := base
			mutate(&binding)
			if _, err := ValidateCorrelatedBinding("Observation", binding); err == nil {
				t.Fatal("invalid Coding value scope unexpectedly validated")
			}
		})
	}
}

func TestCorrelatedBindingLegacyJSONOmitsValueScope(t *testing.T) {
	binding := CorrelatedBinding{
		OwnerPath: "component[]", KeyPath: "component[].code.coding[]",
		SystemPath: "system", CodePath: "code", ValuePath: "valueQuantity.value", LogicalType: "decimal",
	}
	encoded, err := json.Marshal(binding)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"ownerPath":"component[]","keyPath":"component[].code.coding[]","systemPath":"system","codePath":"code","valuePath":"valueQuantity.value","logicalType":"decimal"}`
	if string(encoded) != want {
		t.Fatalf("legacy binding JSON = %s, want byte-identical %s", encoded, want)
	}
}

func TestCorrelatedKeyVersionJSONIsOptionalAndRoundTrips(t *testing.T) {
	legacy, err := json.Marshal(CorrelatedKey{System: "urn:example", Code: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(legacy), `{"system":"urn:example","code":"x"}`; got != want {
		t.Fatalf("legacy key JSON = %s, want %s", got, want)
	}

	encoded, err := json.Marshal(CorrelatedKey{System: "urn:example", Code: "x", Version: "v2"})
	if err != nil {
		t.Fatal(err)
	}
	var decoded CorrelatedKey
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != (CorrelatedKey{System: "urn:example", Code: "x", Version: "v2"}) {
		t.Fatalf("decoded key = %#v", decoded)
	}
}

func TestValidateCorrelatedBindingUsesGeneratedChoiceMetadataForAnyOwner(t *testing.T) {
	binding := CorrelatedBinding{
		OwnerPath:  "dosageInstruction[].doseAndRate[]",
		KeyPath:    "dosageInstruction[].doseAndRate[].type.coding[]",
		SystemPath: "system", CodePath: "code", ValuePath: "doseQuantity.value",
		ChoiceArms: []string{"doseQuantity"}, LogicalType: "decimal",
	}
	checked, err := ValidateCorrelatedBinding("MedicationRequest", binding)
	if err != nil {
		t.Fatal(err)
	}
	if checked.OwnerResource != "DosageDoseAndRate" || checked.KeyResource != "Coding" || checked.ValueSelector.CanonicalPath() != "doseQuantity.value" || checked.UnitSelector == nil || checked.UnitSelector.CanonicalPath() != "doseQuantity.unit" {
		t.Fatalf("checked MedicationRequest binding = %#v", checked)
	}

	binding.ChoiceArms = []string{"type"}
	if _, err := ValidateCorrelatedBinding("MedicationRequest", binding); err == nil || !strings.Contains(err.Error(), "not a choice arm") {
		t.Fatalf("non-choice field error = %v", err)
	}

	binding.ChoiceArms = []string{"doseQuantity", "rateQuantity"}
	binding.LogicalType = "string"
	if _, err := ValidateCorrelatedBinding("MedicationRequest", binding); err == nil || !strings.Contains(err.Error(), "outside valuePath") {
		t.Fatalf("cross-group choice error = %v", err)
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

func TestValidateExtensionBindingCarriesOptInBlankStringFallback(t *testing.T) {
	binding := ExtensionBinding{
		OwnerPath: "extension[]", URLPath: []string{"urn:leaf"},
		ValuePath: "valueCodeableConcept.text", ValueFallback: []string{"valueCodeableConcept.coding[].display"},
		FallbackOnBlankString: true, ChoiceArms: []string{"valueCodeableConcept"}, LogicalType: "string",
	}
	checked, err := ValidateExtensionBinding("Observation", binding)
	if err != nil {
		t.Fatal(err)
	}
	if !checked.FallbackOnBlankString || len(checked.ValueFallbacks) != 1 || checked.ValueFallbacks[0].CanonicalPath() != "valueCodeableConcept.coding[].display" {
		t.Fatalf("checked Extension fallback = %#v", checked)
	}
	binding.ValueFallback = nil
	if _, err := ValidateExtensionBinding("Observation", binding); err == nil {
		t.Fatal("Extension blank-string fallback without a fallback selector was accepted")
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

func TestCorrelatedSelectionScopeRequiresExplicitCodingVersion(t *testing.T) {
	binding := CorrelatedBinding{
		KeyPath: "code.coding[]", SystemPath: "system", CodePath: "code", VersionPath: "version",
		ValueScope: CorrelatedValueScopeCoding, SelectionScope: CorrelatedSelectionScopeSystemVersion,
		ValuePath: "display", ValueFallback: []string{"code"}, LogicalType: "string",
	}
	checked, err := ValidateCorrelatedBinding("Condition", binding)
	if err != nil {
		t.Fatal(err)
	}
	if checked.SelectionScope != CorrelatedSelectionScopeSystemVersion || checked.VersionSelector == nil {
		t.Fatalf("checked category binding = %#v", checked)
	}
	for name, mutate := range map[string]func(*CorrelatedBinding){
		"missing version": func(b *CorrelatedBinding) { b.VersionPath = "" },
		"owner value": func(b *CorrelatedBinding) {
			b.ValueScope, b.ValuePath, b.VersionPath = CorrelatedValueScopeOwner, "code.text", ""
			b.ValueFallback = nil
		},
		"unknown selection":     func(b *CorrelatedBinding) { b.SelectionScope = "ALL_CODES" },
		"wrong system selector": func(b *CorrelatedBinding) { b.SystemPath = "display" },
		"wrong code selector":   func(b *CorrelatedBinding) { b.CodePath = "display" },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := binding
			mutate(&invalid)
			if _, err := ValidateCorrelatedBinding("Condition", invalid); err == nil {
				t.Fatal("invalid category binding accepted")
			}
		})
	}
	for _, version := range []string{"", "v1"} {
		if err := ValidateCorrelatedKey(CorrelatedSelectionScopeSystemVersion, CorrelatedKey{System: "urn:test", Version: version}); err != nil {
			t.Fatalf("category version %q: %v", version, err)
		}
	}
	for _, test := range []struct {
		scope CorrelatedSelectionScope
		key   CorrelatedKey
	}{
		{key: CorrelatedKey{System: "urn:test"}},
		{scope: CorrelatedSelectionScopeExact, key: CorrelatedKey{System: "urn:test"}},
		{scope: CorrelatedSelectionScopeSystemVersion, key: CorrelatedKey{System: "urn:test", Code: "normal"}},
		{scope: CorrelatedSelectionScopeSystemVersion},
	} {
		if err := ValidateCorrelatedKey(test.scope, test.key); err == nil {
			t.Fatalf("invalid selection accepted: %#v", test)
		}
	}
}

func TestCorrelatedSelectionScopePreservesLegacyBindingJSON(t *testing.T) {
	for name, test := range map[string]struct {
		binding CorrelatedBinding
		want    string
	}{
		"CODED_CONCEPT_V1": {
			binding: CorrelatedBinding{KeyPath: "code.coding[]", VersionPath: "version", ValueScope: CorrelatedValueScopeCoding, SystemPath: "system", CodePath: "code", ValuePath: "display", ValueFallback: []string{"code"}, FallbackOnBlankString: true, LogicalType: "string"},
			want:    `{"keyPath":"code.coding[]","versionPath":"version","valueScope":"CODING","systemPath":"system","codePath":"code","valuePath":"display","valueFallback":["code"],"fallbackOnBlankString":true,"logicalType":"string"}`,
		},
		"CODED_VALUE_V1": {
			binding: CorrelatedBinding{KeyPath: "code.coding[]", SystemPath: "system", CodePath: "code", ValuePath: "valueQuantity.value", LogicalType: "decimal"},
			want:    `{"keyPath":"code.coding[]","systemPath":"system","codePath":"code","valuePath":"valueQuantity.value","logicalType":"decimal"}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(test.binding)
			if err != nil || string(encoded) != test.want {
				t.Fatalf("legacy binding JSON = %s, error=%v, want %s", encoded, err, test.want)
			}
			test.binding.SelectionScope = CorrelatedSelectionScopeSystemVersion
			encoded, err = json.Marshal(test.binding)
			if err != nil || !strings.Contains(string(encoded), `"selectionScope":"SYSTEM_VERSION"`) {
				t.Fatalf("explicit category JSON = %s, error=%v", encoded, err)
			}
			var decoded CorrelatedBinding
			if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.SelectionScope != CorrelatedSelectionScopeSystemVersion {
				t.Fatalf("category JSON round-trip = %#v, error=%v", decoded, err)
			}
		})
	}
}
