package semanticauthoring

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/calypr/loom/internal/explorer/workspace"
	fhirbinding "github.com/calypr/loom/internal/fhir/binding"
	"github.com/calypr/loom/internal/semantic"
)

func TestResolveSemanticSelectionPlanStatusesAndTypedSources(t *testing.T) {
	identifier := semantic.Observation{
		SchemaVersion: semantic.ObservationSchemaVersion,
		Source:        semantic.Source{Type: "Patient", Path: "identifier[]"},
		Key:           semantic.Key{Selector: "identifier[].system", System: "urn:study:case-id", Display: "Case identifier"},
		Value:         semantic.Value{Selector: "identifier[].value", Type: "string"},
		OwningScope:   "identifier[]", LogicalType: "string", Completeness: semantic.CompletenessComplete,
		Status: "SUPPORTED", RuleHint: "IDENTIFIER_SYSTEM_VALUE", RuleVersion: strconv.Itoa(semantic.SemanticObservationRuleVersion),
	}
	extension := semantic.Observation{
		SchemaVersion: semantic.ObservationSchemaVersion,
		Source:        semantic.Source{Type: "Observation", Path: "extension[].extension[]"},
		Key:           semantic.Key{Selector: "extension[].extension[].url", Display: "Leaf"},
		Value:         semantic.Value{Selector: "extension[].extension[].valueString", Type: "string"},
		OwningScope:   "extension[].extension[]", ExtensionURLPath: []string{"urn:parent", "urn:leaf"},
		ChoiceArm: "valueString", LogicalType: "string", Completeness: semantic.CompletenessComplete,
		Status: "SUPPORTED", RuleHint: "EXTENSION_URL_VALUE", RuleVersion: strconv.Itoa(semantic.SemanticObservationRuleVersion),
	}
	observation := semantic.Observation{
		SchemaVersion: semantic.ObservationSchemaVersion,
		Source:        semantic.Source{Type: "Observation", Path: "code"},
		Key:           semantic.Key{Selector: "code.coding[]", System: "urn:study", Code: "height", Display: "Height"},
		Value:         semantic.Value{Selector: "valueQuantity.value", Type: "decimal"},
		ChoiceArm:     "valueQuantity", LogicalType: "decimal", Completeness: semantic.CompletenessComplete,
		Status: "SUPPORTED", RuleHint: semantic.RuleHintCodedValueV1, RuleVersion: strconv.Itoa(semantic.SemanticObservationRuleVersion),
	}
	medicationDose := semantic.Observation{
		SchemaVersion: semantic.ObservationSchemaVersion,
		Source:        semantic.Source{Type: "MedicationRequest", Path: "dosageInstruction[].doseAndRate[].type"},
		Key:           semantic.Key{Selector: "type.coding[]", System: "http://example.org/dose-types", Code: "daily-dose", Display: "Daily dose"},
		Value:         semantic.Value{Selector: "doseQuantity.value", Type: "decimal"},
		OwningScope:   "dosageInstruction[].doseAndRate[]", ChoiceArm: "doseQuantity", LogicalType: "decimal", Completeness: semantic.CompletenessComplete,
		Status: "SUPPORTED", RuleHint: semantic.RuleHintCodedValueV1, RuleVersion: strconv.Itoa(semantic.SemanticObservationRuleVersion),
	}

	identifierWithoutSystem := identifier
	identifierWithoutSystem.Key.System = ""
	identifierWithoutSystem.Status = "UNRESOLVED_SYSTEM"

	versioned := observation
	versioned.Key.Version = "2.77"

	codingOnlyConcept := observation
	codingOnlyConcept.Value = semantic.Value{Selector: "valueCodeableConcept.text", Type: "string"}
	codingOnlyConcept.ChoiceArm = "valueCodeableConcept"
	codingOnlyConcept.LogicalType = "string"
	codingOnlyConcept.Status = semantic.StatusUnsupportedValueProjection

	warning := observation
	warning.Status = "DATA_QUALITY_WARNING"

	mixed := observation
	mixed.Status = "MIXED_CHOICE"

	tests := []struct {
		name        string
		observation semantic.Observation
		status      SemanticReadinessStatus
		code        string
		addable     bool
	}{
		{name: "identifier", observation: identifier, status: SemanticReadinessReady, code: "READY", addable: true},
		{name: "nested extension", observation: extension, status: SemanticReadinessReady, code: "READY", addable: true},
		{name: "coded Observation", observation: observation, status: SemanticReadinessReady, code: "READY", addable: true},
		{name: "quality warning", observation: warning, status: SemanticReadinessReadyWithWarning, code: "DATA_QUALITY_WARNING", addable: true},
		{name: "missing Identifier system", observation: identifierWithoutSystem, status: SemanticReadinessNeedsMapping, code: "IDENTIFIER_SYSTEM_MISSING"},
		{name: "mixed value choices", observation: mixed, status: SemanticReadinessNeedsMapping, code: "MULTIPLE_VALUE_CHOICES"},
		{name: "versioned coded value", observation: versioned, status: SemanticReadinessUnsupported, code: "CODED_VALUE_VERSION_UNSUPPORTED"},
		{name: "nested MedicationRequest owner", observation: medicationDose, status: SemanticReadinessReady, code: "READY", addable: true},
		{name: "coding-only CodeableConcept", observation: codingOnlyConcept, status: SemanticReadinessUnsupported, code: "VALUE_PROJECTION_UNSUPPORTED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := ResolveSemanticSelectionPlan(test.observation)
			if plan.Readiness.Status != test.status || plan.Readiness.Code != test.code || plan.Readiness.Message == "" || plan.Readiness.Addable() != test.addable {
				t.Fatalf("readiness = %#v, want status=%s code=%s addable=%t", plan.Readiness, test.status, test.code, test.addable)
			}
			if test.addable != (plan.Source != nil) {
				t.Fatalf("source presence = %t, addable = %t, source=%#v", plan.Source != nil, test.addable, plan.Source)
			}
		})
	}

	identifierPlan := ResolveSemanticSelectionPlan(identifier)
	if identifierPlan.Source.Kind != workspace.SourceIdentifierBySystem || identifierPlan.Source.Lookup == nil || identifierPlan.Source.Lookup.Identifier == nil || identifierPlan.Source.Lookup.Match != "" || identifierPlan.Source.Lookup.Path != "" || identifierPlan.Source.Lookup.Binding != nil || identifierPlan.Source.Lookup.Extension != nil {
		t.Fatalf("Identifier resolver emitted a legacy or mixed source: %#v", identifierPlan.Source)
	}
	if *identifierPlan.Source.Lookup.Identifier != (fhirbinding.IdentifierBinding{OwnerPath: "identifier[]", SystemPath: "system", ValuePath: "value", SystemURI: "urn:study:case-id", LogicalType: "string"}) {
		t.Fatalf("Identifier binding = %#v", identifierPlan.Source.Lookup.Identifier)
	}
	extensionPlan := ResolveSemanticSelectionPlan(extension)
	if extensionPlan.Source == nil || extensionPlan.Source.Lookup == nil || extensionPlan.Source.Lookup.Extension == nil || extensionPlan.Source.Lookup.Extension.OwnerPath != "extension[].extension[]" || extensionPlan.Source.Lookup.Extension.ValuePath != "valueString" || len(extensionPlan.Source.Lookup.Extension.URLPath) != 2 || extensionPlan.Source.Lookup.Extension.URLPath[0] != "urn:parent" || extensionPlan.Source.Lookup.Extension.URLPath[1] != "urn:leaf" || len(extensionPlan.Source.Lookup.Extension.ChoiceArms) != 1 || extensionPlan.Source.Lookup.Extension.ChoiceArms[0] != "valueString" {
		t.Fatalf("Extension resolver lost URL ancestry or exact arm: %#v", extensionPlan.Source)
	}
	observationPlan := ResolveSemanticSelectionPlan(observation)
	if observationPlan.Source == nil || observationPlan.Source.Kind != workspace.SourceCodedValue || observationPlan.Source.Lookup == nil || observationPlan.Source.Lookup.Binding == nil || observationPlan.Source.Lookup.Binding.UnitPath != "valueQuantity.unit" || observationPlan.Source.Lookup.Key == nil || observationPlan.Source.Lookup.Key.System != "urn:study" || observationPlan.Source.Lookup.Key.Code != "height" {
		t.Fatalf("coded value source = %#v", observationPlan.Source)
	}
	medicationPlan := ResolveSemanticSelectionPlan(medicationDose)
	if medicationPlan.Source == nil || medicationPlan.Source.Kind != workspace.SourceCodedValue || medicationPlan.Source.Lookup == nil || medicationPlan.Source.Lookup.Binding == nil {
		t.Fatalf("nested MedicationRequest source = %#v", medicationPlan.Source)
	}
	binding := medicationPlan.Source.Lookup.Binding
	if binding.OwnerPath != medicationDose.OwningScope || binding.KeyPath != "dosageInstruction[].doseAndRate[].type.coding[]" || binding.SystemPath != "system" || binding.CodePath != "code" || binding.ValuePath != medicationDose.Value.Selector || binding.UnitPath != "doseQuantity.unit" || len(binding.ChoiceArms) != 1 || binding.ChoiceArms[0] != "doseQuantity" {
		t.Fatalf("nested MedicationRequest binding = %#v", binding)
	}
}

func TestSemanticCodeableConceptPlanCarriesOrderedValueFallbacks(t *testing.T) {
	observation := semantic.Observation{
		SchemaVersion: semantic.ObservationSchemaVersion,
		Source:        semantic.Source{Type: "Observation", Path: "code"},
		Key:           semantic.Key{Selector: "code.coding[]", System: "urn:study", Code: "stage"},
		Value: semantic.Value{
			Selector:              "valueCodeableConcept.text",
			Type:                  "string",
			FallbackSelectors:     []string{"valueCodeableConcept.coding[].display"},
			FallbackOnBlankString: true,
		},
		ChoiceArm: "valueCodeableConcept", LogicalType: "string", Completeness: semantic.CompletenessComplete,
		Status: "SUPPORTED", RuleHint: semantic.RuleHintCodedValueV1, RuleVersion: strconv.Itoa(semantic.SemanticObservationRuleVersion),
	}

	plan := ResolveSemanticSelectionPlan(observation)
	if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Lookup == nil || plan.Source.Lookup.Binding == nil {
		t.Fatalf("semantic selection plan = %#v, want an addable correlated value source", plan)
	}
	binding := plan.Source.Lookup.Binding
	if binding.ValuePath != "valueCodeableConcept.text" ||
		!reflect.DeepEqual(binding.ValueFallback, []string{"valueCodeableConcept.coding[].display"}) || !binding.FallbackOnBlankString {
		t.Fatalf("authored correlated binding = %#v, want preferred text, ordered coding-display fallback, and blank-string fallback", binding)
	}

	genericString := observation
	genericString.Value = semantic.Value{
		Selector:          "valueCodeableConcept.text",
		Type:              "string",
		FallbackSelectors: []string{"valueCodeableConcept.coding[].display"},
	}
	genericPlan := ResolveSemanticSelectionPlan(genericString)
	if !genericPlan.Readiness.Addable() || genericPlan.Source == nil || genericPlan.Source.Lookup == nil || genericPlan.Source.Lookup.Binding == nil {
		t.Fatalf("generic string plan = %#v, want addable source with default fallback behavior", genericPlan)
	}
	genericBinding := genericPlan.Source.Lookup.Binding
	if genericBinding.FallbackOnBlankString || genericBinding.ValuePath != "valueCodeableConcept.text" ||
		!reflect.DeepEqual(genericBinding.ValueFallback, []string{"valueCodeableConcept.coding[].display"}) {
		t.Fatalf("generic string binding = %#v, want omitted flag to preserve existing empty-string behavior and fallback order", genericBinding)
	}
}

func TestSemanticExtensionCodeableConceptPlanCarriesBlankStringFallback(t *testing.T) {
	observation := semantic.Observation{
		SchemaVersion: semantic.ObservationSchemaVersion,
		Source:        semantic.Source{Type: "Observation", Path: "extension[]"},
		Key:           semantic.Key{Selector: "extension[].url", Display: "urn:study:stage"},
		Value: semantic.Value{
			Selector: "extension[].valueCodeableConcept.text", Type: "string",
			FallbackSelectors: []string{"extension[].valueCodeableConcept.coding[].display"}, FallbackOnBlankString: true,
		},
		OwningScope: "extension[]", ExtensionURLPath: []string{"urn:study:stage"},
		ChoiceArm: "valueCodeableConcept", LogicalType: "string", Completeness: semantic.CompletenessComplete,
		Status: "SUPPORTED", RuleHint: "EXTENSION_URL_VALUE", RuleVersion: strconv.Itoa(semantic.SemanticObservationRuleVersion),
	}
	plan := ResolveSemanticSelectionPlan(observation)
	if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Lookup == nil || plan.Source.Lookup.Extension == nil {
		t.Fatalf("extension semantic plan = %#v, want an addable Extension source", plan)
	}
	binding := plan.Source.Lookup.Extension
	if binding.ValuePath != "valueCodeableConcept.text" || !binding.FallbackOnBlankString ||
		!reflect.DeepEqual(binding.ValueFallback, []string{"valueCodeableConcept.coding[].display"}) {
		t.Fatalf("extension binding = %#v, want relative preferred text and coding display fallback", binding)
	}
}

func TestResolveCodedCategoryPlanUsesSystemVersionScopeAndSameCodingFallback(t *testing.T) {
	for _, version := range []string{"", "v1"} {
		t.Run("version-"+version, func(t *testing.T) {
			observation := standaloneCodingCategoryObservation(version)
			plan := ResolveSemanticSelectionPlan(observation)
			if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Lookup == nil || plan.Source.Lookup.Binding == nil || plan.Source.Lookup.Key == nil {
				t.Fatalf("category plan = %#v", plan)
			}
			binding := plan.Source.Lookup.Binding
			if binding.SelectionScope != fhirbinding.CorrelatedSelectionScopeSystemVersion || binding.ValueScope != fhirbinding.CorrelatedValueScopeCoding || binding.VersionPath != "version" || binding.CodePath != "code" || binding.ValuePath != "display" || len(binding.ValueFallback) != 1 || binding.ValueFallback[0] != "code" || !binding.FallbackOnBlankString {
				t.Fatalf("category binding = %#v", binding)
			}
			if plan.Source.Lookup.Key.System != "https://www.ebi.ac.uk/chembl/compound_name" || plan.Source.Lookup.Key.Code != "" || plan.Source.Lookup.Key.Version != version {
				t.Fatalf("category key = %#v", plan.Source.Lookup.Key)
			}
		})
	}
}

func TestResolveCodedCategoryRejectsAnExactCodeAndLegacyExactPlanRemainsExact(t *testing.T) {
	category := standaloneCodingCategoryObservation("")
	category.Key.Code = "CARBOXYAMIDOTRIAZOLE"
	if plan := ResolveSemanticSelectionPlan(category); plan.Readiness.Addable() || plan.Readiness.Code != "CODED_CATEGORY_HAS_EXACT_CODE" {
		t.Fatalf("category with exact code was not rejected: %#v", plan)
	}

	exact := category
	exact.RuleHint = semantic.RuleHintCodedConceptV1
	exact.Key.Code = "CARBOXYAMIDOTRIAZOLE"
	plan := ResolveSemanticSelectionPlan(exact)
	if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Lookup == nil || plan.Source.Lookup.Binding == nil || plan.Source.Lookup.Key == nil {
		t.Fatalf("legacy exact plan = %#v", plan)
	}
	if plan.Source.Lookup.Binding.SelectionScope != "" || plan.Source.Lookup.Key.Code != "CARBOXYAMIDOTRIAZOLE" {
		t.Fatalf("legacy exact selection semantics changed: %#v", plan.Source.Lookup)
	}
}

func standaloneCodingCategoryObservation(version string) semantic.Observation {
	return semantic.Observation{
		SchemaVersion: semantic.ObservationSchemaVersion,
		Source:        semantic.Source{Type: "Medication", Path: "Medication.code.coding[]"},
		Key: semantic.Key{
			Selector: "code.coding[]", System: "https://www.ebi.ac.uk/chembl/compound_name", Version: version,
		},
		Value: semantic.Value{
			Selector: "code.coding[].display", Type: "string", FallbackSelectors: []string{"code.coding[].code"}, FallbackOnBlankString: true,
		},
		LogicalType: "string", Completeness: semantic.CompletenessComplete, Status: "SUPPORTED",
		RuleHint: semantic.RuleHintCodedCategoryV1, RuleVersion: strconv.Itoa(semantic.SemanticObservationRuleVersion),
	}
}

func TestSemanticCategoryDefaultTitleUsesFieldForGenericSystem(t *testing.T) {
	if got := SemanticCategoryDefaultTitle("Specimen", "Specimen.type.coding[]", "https://cda.readthedocs.io/"); got != "Specimen type" {
		t.Fatalf("generic system category title = %q", got)
	}
	if got := SemanticCategoryDefaultTitle("Medication", "Medication.code.coding[]", "https://www.ebi.ac.uk/chembl/compound_name"); got != "Compound name" {
		t.Fatalf("ChEMBL category title = %q", got)
	}
	if got := SemanticCategoryDefaultTitle("Specimen", "Specimen.type.coding[]", "https://example.org/my-code-system"); got != "My code system" {
		t.Fatalf("meaningful system-tail title = %q", got)
	}
}
