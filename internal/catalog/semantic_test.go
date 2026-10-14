package catalog

import (
	"reflect"
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
	pairedValues := 0
	for _, observation := range observations {
		if observation.RuleHint == SemanticRuleHintCodedValueV1 && observation.OwningScope == "component[]" && observation.Key.Code == "shared" {
			pairedValues++
		}
		if observation.RuleHint == SemanticRuleHintCodedConceptV1 && observation.OwningScope == "component[]" && observation.Key.Code == "shared" {
			t.Fatalf("paired CodeableConcept was also emitted as standalone: %#v", observation)
		}
		if len(observation.ExtensionURLPath) == 2 && observation.ExtensionURLPath[0] == "parent" && observation.ExtensionURLPath[1] == "leaf" {
			if observation.Examples[0] != "left-only" {
				t.Fatalf("nested extension example = %#v", observation)
			}
			if pairedValues != 2 {
				t.Fatalf("paired coded observations = %d, want 2", pairedValues)
			}
			return
		}
	}
	t.Fatal("nested extension ancestry was not retained")
}

func TestStandaloneCodingScopesObserveEachIdentifiedCodingAtItsOwnerScope(t *testing.T) {
	payload := map[string]any{
		"resourceType": "Observation",
		"status":       "final",
		"meta": map[string]any{"security": []any{
			map[string]any{"system": "urn:security", "code": "security-code", "display": "Security label"},
		}},
		"identifier": []any{map[string]any{
			"system": "urn:identifier",
			"value":  "identifier-value",
			"type": map[string]any{"coding": []any{
				map[string]any{"system": "urn:identifier-type", "code": "identifier-type"},
			}},
		}},
		"extension": []any{map[string]any{
			"url": "urn:extension",
			"valueCodeableConcept": map[string]any{"coding": []any{
				map[string]any{"system": "urn:extension-value", "code": "extension-code"},
			}},
		}},
		"code": map[string]any{"coding": []any{
			map[string]any{"system": "urn:root", "code": "root-code", "display": "Root label"},
		}},
		"component": []any{map[string]any{
			"code": map[string]any{"coding": []any{
				map[string]any{"system": "urn:panel", "version": "v1", "code": "alpha", "display": "Alpha"},
				map[string]any{"system": "urn:panel", "version": "v2", "code": "beta", "display": "  "},
				map[string]any{"system": "", "code": "missing-system", "display": "Unidentified system"},
				map[string]any{"system": "urn:panel", "code": "", "display": "Unidentified code"},
			}},
		}},
	}

	var contributions []SemanticInventoryContribution
	emitter := NewSemanticInventoryEmitter("project", "generation")
	emitter.ObservePayload(payload, "Observation", "scope", "observation-1", func(contribution SemanticInventoryContribution) {
		contributions = append(contributions, contribution)
	})
	byCode := map[string]SemanticInventoryContribution{}
	for _, contribution := range contributions {
		if contribution.Observation.RuleHint == SemanticRuleHintCodedConceptV1 {
			byCode[contribution.Observation.Key.Code] = contribution
		}
	}
	if len(byCode) != 4 {
		t.Fatalf("standalone coded concept contributions = %d, want root, security, alpha, and beta: %#v", len(byCode), byCode)
	}

	root := byCode["root-code"].Observation
	if root.OwningScope != "" || root.Source.Path != "code.coding[]" || root.Source.Canonical != "Observation.code.coding[]" || root.Key.Selector != "code.coding[]" || root.Value.Selector != "code.coding[].display" || !reflect.DeepEqual(root.Value.FallbackSelectors, []string{"code.coding[].code"}) {
		t.Fatalf("root standalone observation = %#v", root)
	}

	security := byCode["security-code"].Observation
	if security.OwningScope != "" || security.Source.Path != "meta.security[]" || security.Source.Canonical != "Observation.meta.security[]" || security.Key.Selector != "meta.security[]" || security.Value.Selector != "meta.security[].display" || !reflect.DeepEqual(security.Value.FallbackSelectors, []string{"meta.security[].code"}) {
		t.Fatalf("direct Coding observation = %#v", security)
	}

	alpha := byCode["alpha"]
	if alpha.ConceptID == "" || alpha.BindingID == "" || alpha.Observation.Population != 1 || alpha.Observation.Source.Path != "component[].code.coding[]" || alpha.Observation.Source.Canonical != "Observation.component[].code.coding[]" || alpha.Observation.OwningScope != "component[]" {
		t.Fatalf("alpha standalone identity or owner scope = %#v", alpha)
	}
	if alpha.Observation.Key.System != "urn:panel" || alpha.Observation.Key.Version != "v1" || alpha.Observation.Key.Code != "alpha" || alpha.Observation.Key.Display != "Alpha" || alpha.Example != "Alpha" {
		t.Fatalf("alpha coding projection = %#v", alpha)
	}
	if alpha.Observation.Key.Selector != "code.coding[]" || alpha.Observation.Value.Selector != "code.coding[].display" || alpha.Observation.Value.Type != "string" || !reflect.DeepEqual(alpha.Observation.Value.FallbackSelectors, []string{"code.coding[].code"}) || !alpha.Observation.Value.FallbackOnBlankString {
		t.Fatalf("alpha display/fallback selectors = %#v", alpha.Observation.Value)
	}

	beta := byCode["beta"]
	if beta.Observation.Key.System != "urn:panel" || beta.Observation.Key.Version != "v2" || beta.Observation.Key.Code != "beta" || beta.Observation.Key.Display != "  " || beta.Example != "beta" {
		t.Fatalf("beta coding projection mixed a sibling coding's display or fallback: %#v", beta)
	}
	if beta.Observation.Value.Selector != "code.coding[].display" || !reflect.DeepEqual(beta.Observation.Value.FallbackSelectors, []string{"code.coding[].code"}) || !beta.Observation.Value.FallbackOnBlankString {
		t.Fatalf("beta display/fallback selectors = %#v", beta.Observation.Value)
	}
}

func TestStandaloneScalarCodingChoiceUsesRepeatedOwnerAndExcludesIdentifierAndExtension(t *testing.T) {
	payload := map[string]any{
		"resourceType": "Task",
		"status":       "requested",
		"intent":       "order",
		"input": []any{map[string]any{
			"type": map[string]any{"text": "Task result"},
			"valueCoding": map[string]any{
				"system":  "urn:task-result",
				"version": "2025",
				"code":    "approved",
				"display": "Approved",
			},
		}},
		"identifier": []any{map[string]any{
			"system": "urn:task-identifier",
			"value":  "task-1",
			"type": map[string]any{"coding": []any{
				map[string]any{"system": "urn:identifier-type", "code": "identifier-type", "display": "Identifier type"},
			}},
		}},
		"extension": []any{map[string]any{
			"url": "urn:task-extension",
			"valueCoding": map[string]any{
				"system":  "urn:extension-value",
				"code":    "extension-code",
				"display": "Extension value",
			},
		}},
	}

	var contributions []SemanticInventoryContribution
	emitter := NewSemanticInventoryEmitter("project", "generation")
	emitter.ObservePayload(payload, "Task", "scope", "task-1", func(contribution SemanticInventoryContribution) {
		contributions = append(contributions, contribution)
	})
	var standalone []SemanticInventoryContribution
	for _, contribution := range contributions {
		if contribution.Observation.RuleHint == SemanticRuleHintCodedConceptV1 {
			standalone = append(standalone, contribution)
		}
	}
	if len(standalone) != 1 {
		t.Fatalf("standalone Task Coding contributions = %d, want only valueCoding: %#v", len(standalone), standalone)
	}

	contribution := standalone[0]
	observation := contribution.Observation
	if observation.Source.Type != "Task" || observation.Source.Path != "input[].valueCoding" || observation.Source.Canonical != "Task.input[].valueCoding" ||
		observation.OwningScope != "input[]" || observation.Key.Selector != "valueCoding" ||
		observation.Value.Selector != "valueCoding.display" || !reflect.DeepEqual(observation.Value.FallbackSelectors, []string{"valueCoding.code"}) {
		t.Fatalf("scalar Coding owner-relative selectors = %#v", observation)
	}
	if observation.Key.System != "urn:task-result" || observation.Key.Version != "2025" || observation.Key.Code != "approved" || observation.Key.Display != "Approved" || contribution.Example != "Approved" || observation.Population != 1 {
		t.Fatalf("scalar Coding identity/display = %#v", contribution)
	}
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

func TestSemanticObservationProjectionNeedsObservedScalar(t *testing.T) {
	tests := []struct {
		name     string
		arm      string
		value    map[string]any
		selector string
		status   string
	}{
		{name: "coding-only CodeableConcept", arm: "valueCodeableConcept", value: map[string]any{"coding": []any{map[string]any{"system": "http://snomed.info/sct", "code": "1222593009"}}}, selector: "valueCodeableConcept.text", status: SemanticStatusUnsupportedValueProjection},
		{name: "blank CodeableConcept text", arm: "valueCodeableConcept", value: map[string]any{"text": "  ", "coding": []any{map[string]any{"system": "http://snomed.info/sct", "code": "1222593009"}}}, selector: "valueCodeableConcept.text", status: SemanticStatusUnsupportedValueProjection},
		{name: "Quantity without value", arm: "valueQuantity", value: map[string]any{"unit": "days"}, selector: "valueQuantity.value", status: SemanticStatusUnsupportedValueProjection},
		{name: "Period without start", arm: "valuePeriod", value: map[string]any{"end": "2026-09-01"}, selector: "valuePeriod.start", status: SemanticStatusUnsupportedValueProjection},
		{name: "Range without low value", arm: "valueRange", value: map[string]any{"low": map[string]any{"unit": "days"}}, selector: "valueRange.low.value", status: SemanticStatusUnsupportedValueProjection},
		{name: "Ratio without numerator value", arm: "valueRatio", value: map[string]any{"numerator": map[string]any{"unit": "mg"}}, selector: "valueRatio.numerator.value", status: SemanticStatusUnsupportedValueProjection},
		{name: "CodeableConcept with text", arm: "valueCodeableConcept", value: map[string]any{"text": "Stage I"}, selector: "valueCodeableConcept.text", status: "SUPPORTED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := NewProfilerForGeneration("project", "generation", "scope", "Observation", nil)
			p.ObservePayload(map[string]any{
				"resourceType": "Observation",
				"code": map[string]any{"coding": []any{
					map[string]any{"system": "urn:study", "code": "stage", "display": "Stage"},
				}},
				test.arm: test.value,
			}, map[string]float64{})

			for _, document := range p.Documents() {
				for _, observation := range document.SemanticObservations {
					if observation.RuleHint != SemanticRuleHintCodedValueV1 || observation.Key.Code != "stage" {
						continue
					}
					if observation.Value.Selector != test.selector || observation.Status != test.status {
						t.Fatalf("profiled value projection = %#v, want selector %q and status %q", observation, test.selector, test.status)
					}
					return
				}
			}
			t.Fatal("Observation value was not retained in the semantic catalog")
		})
	}
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
