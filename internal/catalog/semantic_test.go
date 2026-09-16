package catalog

import (
	"testing"
)

func TestSemanticObservationsKeepCodingPairAndOwnerScope(t *testing.T) {
	p := NewProfilerForGeneration("project", "generation", "scope", "Observation", nil)
	payload := map[string]any{
		"resourceType": "Observation",
		"code":         map[string]any{"coding": []any{map[string]any{"system": "urn:root", "code": "root-code"}}},
		"component": []any{
			map[string]any{
				"code": map[string]any{"coding": []any{
					map[string]any{"system": "urn:study:A", "code": "shared"},
					map[string]any{"system": "urn:decoy", "code": "decoy"},
				}},
				"valueQuantity": map[string]any{"value": 111.0, "unit": "cm"},
			},
			map[string]any{
				"code":          map[string]any{"coding": []any{map[string]any{"system": "urn:study:B", "code": "shared"}}},
				"valueQuantity": map[string]any{"value": 222.0, "unit": "cm"},
			},
		},
		"extension": []any{map[string]any{
			"url":       "parent",
			"extension": []any{map[string]any{"url": "leaf", "valueString": "left-only"}},
		}},
	}
	p.ObservePayload(payload, map[string]float64{})
	documents := p.Documents()
	var observations []SemanticObservation
	for _, document := range documents {
		observations = append(observations, document.SemanticObservations...)
	}
	byPair := map[string]SemanticObservation{}
	for _, observation := range observations {
		if observation.OwningScope != "component[]" || observation.Key.Code != "shared" {
			continue
		}
		byPair[observation.Key.System+"\x00"+observation.Key.Code] = observation
	}
	if got := byPair["urn:study:A\x00shared"]; got.Status != "SUPPORTED" || got.Examples[0] != "111" || got.ObservedUnits[0] != "cm" || got.LogicalType != "decimal" || got.Value.Type != "decimal" || got.Value.Selector != "valueQuantity.value" {
		t.Fatalf("A paired observation = %#v", got)
	}
	if got := byPair["urn:study:B\x00shared"]; got.Status != "SUPPORTED" || got.Examples[0] != "222" {
		t.Fatalf("B paired observation = %#v", got)
	}
	for _, observation := range observations {
		if len(observation.ExtensionURLPath) == 2 && observation.ExtensionURLPath[0] == "parent" && observation.ExtensionURLPath[1] == "leaf" {
			if observation.Examples[0] != "left-only" {
				t.Fatalf("nested extension example = %#v", observation)
			}
			return
		}
	}
	t.Fatal("nested extension ancestry was not retained")
}

func TestSemanticObservationsUseGeneratedDatePrimitive(t *testing.T) {
	p := NewProfilerForGeneration("project", "generation", "scope", "Observation", nil)
	p.ObservePayload(map[string]any{
		"resourceType": "Observation",
		"code": map[string]any{"coding": []any{
			map[string]any{"system": "urn:study", "code": "observed-at"},
		}},
		"valueDateTime": "2026-09-16T12:30:00Z",
	}, map[string]float64{})
	for _, document := range p.Documents() {
		for _, observation := range document.SemanticObservations {
			if observation.Key.Code != "observed-at" {
				continue
			}
			if observation.Value.Selector != "valueDateTime" || observation.Value.Type != "date_time" || observation.LogicalType != "date_time" {
				t.Fatalf("date-time observation = %#v", observation)
			}
			return
		}
	}
	t.Fatal("date-time semantic observation was not retained")
}

func TestSemanticObservationMergeDoesNotDoubleCountNewKey(t *testing.T) {
	newPayload := func(system, code string, value float64) map[string]any {
		return map[string]any{
			"resourceType": "Observation",
			"component": []any{map[string]any{
				"code":          map[string]any{"coding": []any{map[string]any{"system": system, "code": code}}},
				"valueQuantity": map[string]any{"value": value, "unit": "cm"},
			}},
		}
	}
	left := NewProfilerForGeneration("project", "generation", "scope", "Observation", nil)
	right := NewProfilerForGeneration("project", "generation", "scope", "Observation", nil)
	left.ObservePayload(newPayload("urn:left", "height", 111), map[string]float64{})
	right.ObservePayload(newPayload("urn:right", "height", 222), map[string]float64{})
	if err := left.Merge(right); err != nil {
		t.Fatal(err)
	}
	for _, document := range left.Documents() {
		for _, observation := range document.SemanticObservations {
			if observation.Key.System == "urn:right" && observation.Key.Code == "height" {
				if observation.Population != 1 {
					t.Fatalf("new merged observation population = %d, want 1: %#v", observation.Population, observation)
				}
				return
			}
		}
	}
	t.Fatal("merged right-hand semantic observation was not retained")
}

func TestSemanticObservationsExposeMissingAndMixedStates(t *testing.T) {
	p := NewProfilerForGeneration("project", "generation", "scope", "Observation", nil)
	p.ObservePayload(map[string]any{
		"resourceType": "Observation",
		"component": []any{
			map[string]any{
				"code":          map[string]any{"coding": []any{map[string]any{"code": "missing-system"}}},
				"valueQuantity": map[string]any{"value": 333.0},
			},
			map[string]any{
				"code":          map[string]any{"coding": []any{map[string]any{"system": "urn:mixed", "code": "mixed"}}},
				"valueString":   "not-numeric",
				"valueQuantity": map[string]any{"value": 1.0, "unit": "mg"},
			},
		},
	}, map[string]float64{})
	var missing, mixed int
	for _, document := range p.Documents() {
		for _, observation := range document.SemanticObservations {
			if observation.Key.Code == "missing-system" && observation.Status == "SUPPORTED" {
				t.Fatal("missing-system candidate was reported supported")
			}
			if observation.Key.Code == "mixed" && observation.Status == "MIXED_CHOICE" {
				mixed++
			}
			if observation.Status == "UNRESOLVED_SYSTEM" {
				missing++
			}
		}
	}
	if missing == 0 {
		t.Fatal("missing system candidate was not retained")
	}
	if mixed < 2 {
		t.Fatalf("mixed choice observations = %d, want both raw arms", mixed)
	}
}
