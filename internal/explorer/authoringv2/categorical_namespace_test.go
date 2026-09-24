package authoringv2

import (
	"strconv"
	"testing"

	"github.com/calypr/loom/internal/catalog"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func TestResolveCategoricalSelectionPlanUsesClosedSystemNamespace(t *testing.T) {
	observation := catalog.SemanticObservation{
		SchemaVersion: catalog.SemanticObservationSchemaVersion,
		Role:          catalog.SemanticRoleCategoricalSlot,
		Source:        catalog.SemanticObservationSource{Type: "Observation", Path: "code"},
		Key:           catalog.SemanticObservationKey{Selector: "code.coding[]", System: "urn:example:a"},
		Value:         catalog.SemanticObservationValue{Selector: "code.coding[].code", Type: "string", Presentation: fhirschema.ValuePresentationDisplayOrCode},
		OwningScope:   "code", LogicalType: "string", Completeness: catalog.SemanticComplete,
		Status: "SUPPORTED", RuleHint: catalog.SemanticRuleHintCategoricalSlotV1,
		RuleVersion: strconv.Itoa(catalog.SemanticObservationRuleVersion),
	}
	plan := ResolveSemanticSelectionPlan(observation)
	if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Kind != SourceCategoricalBySystem || plan.Source.Categorical == nil {
		t.Fatalf("categorical plan = %#v", plan)
	}
	source := plan.Source.Categorical
	if source.System != "urn:example:a" || source.Binding.KeyPath != "code.coding[]" || source.Binding.ValuePath != "code" || source.Binding.SystemPath != "system" || len(source.Binding.ValueFallback) != 1 || source.Binding.ValueFallback[0] != "display" {
		t.Fatalf("categorical source = %#v", source)
	}
	if source.Binding.ValuePresentation != fhirschema.ValuePresentationDisplayOrCode {
		t.Fatalf("categorical presentation = %q", source.Binding.ValuePresentation)
	}
}

func TestResolveCategoricalSelectionPlanKeepsTextOnlyAndMissingSystemScalar(t *testing.T) {
	text := catalog.SemanticObservation{
		SchemaVersion: catalog.SemanticObservationSchemaVersion,
		Role:          catalog.SemanticRoleCategoricalSlot,
		Source:        catalog.SemanticObservationSource{Type: "Observation", Path: "code"},
		Key:           catalog.SemanticObservationKey{Selector: "code.text"},
		Value:         catalog.SemanticObservationValue{Selector: "code.text", Type: "string"},
		OwningScope:   "code", LogicalType: "string", Completeness: catalog.SemanticComplete,
		Status: "SUPPORTED", RuleHint: catalog.SemanticRuleHintCategoricalSlotV1, RuleVersion: strconv.Itoa(catalog.SemanticObservationRuleVersion),
	}
	plan := ResolveSemanticSelectionPlan(text)
	if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Kind != SourceField || plan.Source.Field == nil || plan.Source.Field.Path != "code.text" {
		t.Fatalf("text-only plan = %#v", plan)
	}

	missing := text
	missing.Key = catalog.SemanticObservationKey{Selector: "code.coding[]"}
	missing.Value = catalog.SemanticObservationValue{Selector: "code.coding[].code", Type: "string"}
	missing.LogicalType = "string"
	plan = ResolveSemanticSelectionPlan(missing)
	if plan.Readiness.Status != SemanticReadinessNeedsMapping || plan.Readiness.Code != "CATEGORICAL_SYSTEM_MISSING" || plan.Source != nil {
		t.Fatalf("missing-system plan = %#v", plan)
	}
}
