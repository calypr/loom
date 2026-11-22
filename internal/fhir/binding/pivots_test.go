package binding

import (
	"testing"

	"github.com/calypr/loom/internal/fhir/schema"
)

func TestValidatePivotSelectors(t *testing.T) {
	cc, err := ValidatePivotSelectors("Condition", schema.FieldSelectorSpecFromPath("code.coding[].display"), schema.FieldSelectorSpecFromPath("code.text"))
	if err != nil {
		t.Fatalf("codeable concept pivot validation failed: %v", err)
	}
	if cc.Family != PivotFamilyCodeableConcept {
		t.Fatalf("unexpected family: %q", cc.Family)
	}
	if cc.CatalogRootPath != "code" {
		t.Fatalf("unexpected codeable concept catalog root: %q", cc.CatalogRootPath)
	}

	obs, err := ValidatePivotSelectors("Observation", schema.FieldSelectorSpecFromPath("code.coding[].display"), schema.FieldSelectorSpecFromPath("valueQuantity.value"))
	if err != nil {
		t.Fatalf("observation pivot validation failed: %v", err)
	}
	if obs.Family != PivotFamilyObservationCodeValue {
		t.Fatalf("unexpected observation family: %q", obs.Family)
	}
	if obs.CatalogRootPath != "code" {
		t.Fatalf("unexpected observation catalog root: %q", obs.CatalogRootPath)
	}
	component, ok := DefaultPivotSpec("Observation", "component[].code", "")
	if !ok {
		t.Fatal("component pivot was not discovered from generated schema")
	}
	if component.ItemSourcePath != "component[]" || component.ItemResourceType != "ObservationComponent" {
		t.Fatalf("unexpected component item scope: %#v", component)
	}
	if schema.CanonicalPath(component.ColumnSelector) != "code.text" || len(component.ValueSelectors) == 0 {
		t.Fatalf("unexpected component selectors: %#v", component)
	}
	observation, ok := DefaultPivotSpec("Observation", "code", "valueString")
	if !ok || schema.CanonicalPath(observation.ColumnSelector) != "code.text" || schema.CanonicalPath(observation.ValueSelector) != "valueString" {
		t.Fatalf("unexpected observation pivot selectors: %#v", observation)
	}
}

func TestChoiceValueSelectorOptions(t *testing.T) {
	options := ChoiceValueSelectorOptions("Observation")
	if len(options) == 0 {
		t.Fatal("expected observation value selector options")
	}
	found := false
	for _, option := range options {
		if schema.SelectorExpression(option) == "valueQuantity.value" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected valueQuantity.value option, got %#v", options)
	}
}
