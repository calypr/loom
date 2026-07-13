package template

import "testing"

func TestDefaultRegistryHasStableSixTemplateOrder(t *testing.T) {
	registry := DefaultRegistry()
	definitions := registry.Definitions()
	want := []string{"patient-cohort", "specimen-inventory", "file-manifest", "diagnoses", "labs-observations", "study-enrollment"}
	if len(definitions) != len(want) {
		t.Fatalf("definition count = %d, want %d", len(definitions), len(want))
	}
	for i, id := range want {
		if definitions[i].ID != id || definitions[i].Version != 1 {
			t.Fatalf("definition %d = %#v, want %s v1", i, definitions[i], id)
		}
	}
}

func TestRegistryDefinitionsAreDefensiveCopies(t *testing.T) {
	registry := DefaultRegistry()
	definitions := registry.Definitions()
	definitions[0].RootCandidates[0] = "Observation"
	definitions[0].SuggestedColumns[0].FieldRefAlternatives[0] = "Observation.id"
	got, ok := registry.Definition("patient-cohort")
	if !ok {
		t.Fatal("patient-cohort not found")
	}
	if got.RootCandidates[0] != "Patient" || got.SuggestedColumns[0].FieldRefAlternatives[0] != "Patient.identifier_value" {
		t.Fatalf("registry was mutated through returned definition: %#v", got)
	}
}

func TestRegistryRejectsDuplicateAndUnknownDefinitions(t *testing.T) {
	base := Definition{ID: "x", Version: 1, Label: "X", RootCandidates: []string{"Patient"}}
	if _, err := NewRegistry([]Definition{base, base}); err == nil {
		t.Fatal("expected duplicate id error")
	}
	unknown := base
	unknown.ID = "unknown"
	unknown.RootCandidates = []string{"NotFHIR"}
	if _, err := NewRegistry([]Definition{unknown}); err == nil {
		t.Fatal("expected unknown root error")
	}
}

func TestDefaultStartersContainOnlyDefaultSuggestions(t *testing.T) {
	registry := DefaultRegistry()
	definition, ok := registry.Definition("labs-observations")
	if !ok {
		t.Fatal("labs-observations not found")
	}
	snapshot := CapabilitySnapshot{
		Resources: []ResourceCapability{{
			ResourceType: "Observation", Present: true,
			Fields: []FieldCapability{
				{ResourceType: "Observation", FieldRef: "Observation.id"},
				{ResourceType: "Observation", FieldRef: "Observation.code_coding_display"},
				{ResourceType: "Observation", FieldRef: "Observation.valuequantity_value"},
				{ResourceType: "Observation", FieldRef: "Observation.code", PivotCandidate: true, PivotColumns: []string{"A", "B"}},
			},
		}},
	}
	availability := Resolve(definition, snapshot)
	if availability.Status != StatusPartial {
		t.Fatalf("status = %s, want PARTIAL because optional fields are absent", availability.Status)
	}
	if len(availability.Starter.Fields) != 3 {
		t.Fatalf("starter fields = %#v, want 3 default fields", availability.Starter.Fields)
	}
	if len(availability.Starter.Pivots) != 1 || availability.Starter.Pivots[0].Columns[0] != "A" {
		t.Fatalf("starter pivots = %#v", availability.Starter.Pivots)
	}
	if err := ValidateStarter(availability.Starter); err != nil {
		t.Fatal(err)
	}
}
