package authoringv2

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/calypr/loom/internal/catalog"
)

func TestSemanticCodeableConceptPlanCarriesOrderedValueFallbacks(t *testing.T) {
	observation := catalog.SemanticObservation{
		SchemaVersion: catalog.SemanticObservationSchemaVersion,
		Source:        catalog.SemanticObservationSource{Type: "Observation", Path: "code"},
		Key:           catalog.SemanticObservationKey{Selector: "code.coding[]", System: "urn:study", Code: "stage"},
		Value: catalog.SemanticObservationValue{
			Selector:              "valueCodeableConcept.text",
			Type:                  "string",
			FallbackSelectors:     []string{"valueCodeableConcept.coding[].display"},
			FallbackOnBlankString: true,
		},
		ChoiceArm: "valueCodeableConcept", LogicalType: "string", Completeness: catalog.SemanticComplete,
		Status: "SUPPORTED", RuleHint: catalog.SemanticRuleHintCodedValueV1, RuleVersion: strconv.Itoa(catalog.SemanticObservationRuleVersion),
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
	genericString.Value = catalog.SemanticObservationValue{
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
	observation := catalog.SemanticObservation{
		SchemaVersion: catalog.SemanticObservationSchemaVersion,
		Source:        catalog.SemanticObservationSource{Type: "Observation", Path: "extension[]"},
		Key:           catalog.SemanticObservationKey{Selector: "extension[].url", Display: "urn:study:stage"},
		Value: catalog.SemanticObservationValue{
			Selector: "extension[].valueCodeableConcept.text", Type: "string",
			FallbackSelectors: []string{"extension[].valueCodeableConcept.coding[].display"}, FallbackOnBlankString: true,
		},
		OwningScope: "extension[]", ExtensionURLPath: []string{"urn:study:stage"},
		ChoiceArm: "valueCodeableConcept", LogicalType: "string", Completeness: catalog.SemanticComplete,
		Status: "SUPPORTED", RuleHint: "EXTENSION_URL_VALUE", RuleVersion: strconv.Itoa(catalog.SemanticObservationRuleVersion),
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
