package catalog

import "testing"

func TestStandaloneCompositeValuesRemainDiscoverable(t *testing.T) {
	p := NewProfilerForGeneration("project", "generation", "scope", "Specimen", nil)
	p.ObservePayload(map[string]any{
		"resourceType": "Specimen",
		"collection": map[string]any{
			"collectedPeriod": map[string]any{"start": "2026-01-01", "end": "2026-01-31"},
			"quantity":        map[string]any{"value": 0, "unit": "mL"},
		},
	}, map[string]float64{})
	want := map[string]bool{"collection.collectedPeriod": false, "collection.quantity": false}
	for _, document := range p.Documents() {
		for _, observation := range document.SemanticObservations {
			if observation.Role != SemanticRoleStructuredSlot {
				continue
			}
			if _, exists := want[observation.Value.Selector]; !exists {
				t.Fatalf("unexpected structured slot: %#v", observation)
			}
			if observation.LogicalType != "object" || observation.Status != "SUPPORTED" {
				t.Fatalf("incomplete structured slot: %#v", observation)
			}
			want[observation.Value.Selector] = true
		}
	}
	for path, found := range want {
		if !found {
			t.Errorf("structured slot %s disappeared", path)
		}
	}
}

func TestPairedStructuredValuesDoNotProduceNestedStandaloneColumns(t *testing.T) {
	p := NewProfilerForGeneration("project", "generation", "scope", "Task", nil)
	p.ObservePayload(map[string]any{
		"resourceType": "Task",
		"input": []any{map[string]any{
			"type":       map[string]any{"coding": []any{map[string]any{"system": "urn:feature", "code": "range"}}},
			"valueRange": map[string]any{"low": map[string]any{"value": 0}, "high": map[string]any{"value": 10}},
		}},
	}, map[string]float64{})
	paired := 0
	for _, document := range p.Documents() {
		for _, observation := range document.SemanticObservations {
			if observation.Role == SemanticRoleStructuredSlot {
				t.Fatalf("paired value leaked as standalone: %#v", observation)
			}
			if observation.Role == SemanticRoleCodedValue {
				paired++
				if observation.Value.Selector != "valueRange" || observation.LogicalType != "object" {
					t.Fatalf("pair did not preserve full range: %#v", observation)
				}
			}
		}
	}
	if paired != 1 {
		t.Fatalf("named ranges = %d, want 1", paired)
	}
}
