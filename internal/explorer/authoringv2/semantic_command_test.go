package authoringv2

import (
	"strconv"
	"testing"

	"github.com/calypr/loom/internal/catalog"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func TestResolveSemanticSelectionPlanStatusesAndTypedSources(t *testing.T) {
	identifier := catalog.SemanticObservation{
		SchemaVersion: catalog.SemanticObservationSchemaVersion,
		Source:        catalog.SemanticObservationSource{Type: "Patient", Path: "identifier[]"},
		Key:           catalog.SemanticObservationKey{Selector: "identifier[].system", System: "urn:study:case-id", Display: "Case identifier"},
		Value:         catalog.SemanticObservationValue{Selector: "identifier[].value", Type: "string"},
		OwningScope:   "identifier[]", LogicalType: "string", Completeness: catalog.SemanticComplete,
		Status: "SUPPORTED", RuleHint: "IDENTIFIER_SYSTEM_VALUE", RuleVersion: strconv.Itoa(catalog.SemanticObservationRuleVersion),
	}
	extension := catalog.SemanticObservation{
		SchemaVersion: catalog.SemanticObservationSchemaVersion,
		Source:        catalog.SemanticObservationSource{Type: "Observation", Path: "extension[].extension[]"},
		Key:           catalog.SemanticObservationKey{Selector: "extension[].extension[].url", Display: "Leaf"},
		Value:         catalog.SemanticObservationValue{Selector: "extension[].extension[].valueString", Type: "string"},
		OwningScope:   "extension[].extension[]", ExtensionURLPath: []string{"urn:parent", "urn:leaf"},
		ChoiceArm: "valueString", LogicalType: "string", Completeness: catalog.SemanticComplete,
		Status: "SUPPORTED", RuleHint: "EXTENSION_URL_VALUE", RuleVersion: strconv.Itoa(catalog.SemanticObservationRuleVersion),
	}
	observation := catalog.SemanticObservation{
		SchemaVersion: catalog.SemanticObservationSchemaVersion,
		Source:        catalog.SemanticObservationSource{Type: "Observation", Path: "code"},
		Key:           catalog.SemanticObservationKey{Selector: "code.coding[]", System: "urn:study", Code: "height", Display: "Height"},
		Value:         catalog.SemanticObservationValue{Selector: "valueQuantity.value", Type: "decimal"},
		ChoiceArm:     "valueQuantity", LogicalType: "decimal", Completeness: catalog.SemanticComplete,
		Status: "SUPPORTED", RuleHint: catalog.SemanticRuleHintCodedValueV1, RuleVersion: strconv.Itoa(catalog.SemanticObservationRuleVersion),
	}
	medicationDose := catalog.SemanticObservation{
		SchemaVersion: catalog.SemanticObservationSchemaVersion,
		Source:        catalog.SemanticObservationSource{Type: "MedicationRequest", Path: "dosageInstruction[].doseAndRate[].type"},
		Key:           catalog.SemanticObservationKey{Selector: "type.coding[]", System: "http://example.org/dose-types", Code: "daily-dose", Display: "Daily dose"},
		Value:         catalog.SemanticObservationValue{Selector: "doseQuantity.value", Type: "decimal"},
		OwningScope:   "dosageInstruction[].doseAndRate[]", ChoiceArm: "doseQuantity", LogicalType: "decimal", Completeness: catalog.SemanticComplete,
		Status: "SUPPORTED", RuleHint: catalog.SemanticRuleHintCodedValueV1, RuleVersion: strconv.Itoa(catalog.SemanticObservationRuleVersion),
	}
	categorical := catalog.SemanticObservation{
		SchemaVersion: catalog.SemanticObservationSchemaVersion,
		Source:        catalog.SemanticObservationSource{Type: "Observation", Path: "category[]"},
		Key:           catalog.SemanticObservationKey{Selector: "category[].coding[]", System: "http://terminology.hl7.org/CodeSystem/observation-category", Code: "laboratory", Display: "Laboratory"},
		Value:         catalog.SemanticObservationValue{Selector: "category[].coding[].code", Type: "code"},
		OwningScope:   "category[]", LogicalType: "code", Completeness: catalog.SemanticComplete,
		Status: "SUPPORTED", RuleHint: catalog.SemanticRuleHintCategoricalCodeV1, RuleVersion: strconv.Itoa(catalog.SemanticObservationRuleVersion),
	}

	identifierWithoutSystem := identifier
	identifierWithoutSystem.Key.System = ""
	identifierWithoutSystem.Status = "UNRESOLVED_SYSTEM"

	versioned := observation
	versioned.Key.Version = "2.77"

	codingOnlyConcept := observation
	codingOnlyConcept.Value = catalog.SemanticObservationValue{Selector: "valueCodeableConcept.text", Type: "string"}
	codingOnlyConcept.ChoiceArm = "valueCodeableConcept"
	codingOnlyConcept.LogicalType = "string"
	codingOnlyConcept.Status = catalog.SemanticStatusUnsupportedValueProjection

	warning := observation
	warning.Status = "DATA_QUALITY_WARNING"

	mixed := observation
	mixed.Status = "MIXED_CHOICE"

	tests := []struct {
		name        string
		observation catalog.SemanticObservation
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
		{name: "categorical CodeableConcept", observation: categorical, status: SemanticReadinessReady, code: "READY", addable: true},
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
	if identifierPlan.Source.Kind != SourceIdentifierBySystem || identifierPlan.Source.Lookup == nil || identifierPlan.Source.Lookup.Identifier == nil || identifierPlan.Source.Lookup.Match != "" || identifierPlan.Source.Lookup.Path != "" || identifierPlan.Source.Lookup.Binding != nil || identifierPlan.Source.Lookup.Extension != nil {
		t.Fatalf("Identifier resolver emitted a legacy or mixed source: %#v", identifierPlan.Source)
	}
	if *identifierPlan.Source.Lookup.Identifier != (fhirschema.IdentifierBinding{OwnerPath: "identifier[]", SystemPath: "system", ValuePath: "value", SystemURI: "urn:study:case-id", LogicalType: "string"}) {
		t.Fatalf("Identifier binding = %#v", identifierPlan.Source.Lookup.Identifier)
	}
	extensionPlan := ResolveSemanticSelectionPlan(extension)
	if extensionPlan.Source == nil || extensionPlan.Source.Lookup == nil || extensionPlan.Source.Lookup.Extension == nil || extensionPlan.Source.Lookup.Extension.OwnerPath != "extension[].extension[]" || extensionPlan.Source.Lookup.Extension.ValuePath != "valueString" || len(extensionPlan.Source.Lookup.Extension.URLPath) != 2 || extensionPlan.Source.Lookup.Extension.URLPath[0] != "urn:parent" || extensionPlan.Source.Lookup.Extension.URLPath[1] != "urn:leaf" || len(extensionPlan.Source.Lookup.Extension.ChoiceArms) != 1 || extensionPlan.Source.Lookup.Extension.ChoiceArms[0] != "valueString" {
		t.Fatalf("Extension resolver lost URL ancestry or exact arm: %#v", extensionPlan.Source)
	}
	observationPlan := ResolveSemanticSelectionPlan(observation)
	if observationPlan.Source == nil || observationPlan.Source.Kind != SourceCodedValue || observationPlan.Source.Lookup == nil || observationPlan.Source.Lookup.Binding == nil || observationPlan.Source.Lookup.Binding.UnitPath != "valueQuantity.unit" || observationPlan.Source.Lookup.Key == nil || observationPlan.Source.Lookup.Key.System != "urn:study" || observationPlan.Source.Lookup.Key.Code != "height" {
		t.Fatalf("coded value source = %#v", observationPlan.Source)
	}
	medicationPlan := ResolveSemanticSelectionPlan(medicationDose)
	if medicationPlan.Source == nil || medicationPlan.Source.Kind != SourceCodedValue || medicationPlan.Source.Lookup == nil || medicationPlan.Source.Lookup.Binding == nil {
		t.Fatalf("nested MedicationRequest source = %#v", medicationPlan.Source)
	}
	binding := medicationPlan.Source.Lookup.Binding
	if binding.OwnerPath != medicationDose.OwningScope || binding.KeyPath != "dosageInstruction[].doseAndRate[].type.coding[]" || binding.SystemPath != "system" || binding.CodePath != "code" || binding.ValuePath != medicationDose.Value.Selector || binding.UnitPath != "doseQuantity.unit" || len(binding.ChoiceArms) != 1 || binding.ChoiceArms[0] != "doseQuantity" {
		t.Fatalf("nested MedicationRequest binding = %#v", binding)
	}
	categoricalPlan := ResolveSemanticSelectionPlan(categorical)
	if categoricalPlan.Source == nil || categoricalPlan.Source.Kind != SourceCodedValue || categoricalPlan.Source.Lookup == nil || categoricalPlan.Source.Lookup.Binding == nil || categoricalPlan.Source.Lookup.Key == nil {
		t.Fatalf("categorical source = %#v", categoricalPlan.Source)
	}
	categoricalBinding := categoricalPlan.Source.Lookup.Binding
	if categoricalBinding.OwnerPath != "" || categoricalBinding.KeyPath != "category[].coding[]" || categoricalBinding.ValueScope != fhirschema.CorrelatedValueKeyItem || categoricalBinding.ValuePath != "code" || categoricalPlan.Source.FieldPath() != "category[].coding[].code" || categoricalPlan.LogicalType != "code" {
		t.Fatalf("categorical binding = %#v", categoricalBinding)
	}
}
