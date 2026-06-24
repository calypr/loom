package dataframebuilder

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/catalog"
)

func TestDiscoveredFieldHintsUseDefaultRefsOnly(t *testing.T) {
	fields := []catalog.PopulatedField{
		{ResourceType: "Patient", Path: "identifier[].value", Kind: "scalar"},
	}
	hints := discoveredFieldHints("Patient", fields)
	if len(hints) != 1 {
		t.Fatalf("hint count = %d, want 1", len(hints))
	}
	if hints[0].FieldRef != "Patient.identifier_value" {
		t.Fatalf("unexpected fieldRef: %q", hints[0].FieldRef)
	}
	if hints[0].Label != "Identifier Value" {
		t.Fatalf("unexpected label: %q", hints[0].Label)
	}
	if hints[0].Selector.SourcePath != "identifier[]" || hints[0].Selector.ValuePath != "value" {
		t.Fatalf("unexpected selector: %#v", hints[0].Selector)
	}
}

func TestResolveFieldRefAcceptsDefaultGeneratedRefs(t *testing.T) {
	discovered := []catalog.PopulatedField{
		{ResourceType: "Patient", Path: "identifier[].value"},
	}
	selector, err := resolveFieldRef("Patient", discovered, "Patient.identifier_value")
	if err != nil {
		t.Fatal(err)
	}
	if selector != "identifier[].value" {
		t.Fatalf("unexpected selector: %q", selector)
	}
}

func TestResolveFieldRefRejectsCuratedAlias(t *testing.T) {
	discovered := []catalog.PopulatedField{
		{ResourceType: "Patient", Path: "extension[].valueCode"},
	}
	_, err := resolveFieldRef("Patient", discovered, "Patient.birth_sex")
	if err == nil || !strings.Contains(err.Error(), `unknown fieldRef "Patient.birth_sex"`) {
		t.Fatalf("expected curated alias rejection, got %v", err)
	}
}

func TestResolvePivotFieldRefRejectsCuratedAlias(t *testing.T) {
	discovered := []catalog.PopulatedField{
		{ResourceType: "Patient", Path: "extension[].valueCode"},
	}
	_, err := resolvePivotFieldRef("Patient", discovered, "Patient.birth_sex")
	if err == nil || !strings.Contains(err.Error(), `unknown pivot fieldRef "Patient.birth_sex"`) {
		t.Fatalf("expected curated pivot alias rejection, got %v", err)
	}
}
