package catalog

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/fhir/schema"
)

func TestSchemaSemanticDiscoveryUsesResolvedDatatypesUnderUnfamiliarNames(t *testing.T) {
	index, err := schema.NewIndex([]schema.Definition{
		{
			Name: "SignalPacket",
			Elements: []schema.Element{
				{Name: "measurements", JSONType: schema.JSONTypeArray, ArrayElementType: "SignalReading"},
				{Name: "keys", JSONType: schema.JSONTypeArray, ArrayElementType: "Identifier"},
				{Name: "addons", JSONType: schema.JSONTypeArray, ArrayElementType: "Extension"},
			},
		},
		{
			Name: "SignalReading",
			Elements: []schema.Element{
				{Name: "code", JSONType: schema.JSONTypeObject, ReferencedType: "CodeableConcept"},
				{Name: "valueQuantity", JSONType: schema.JSONTypeObject, ReferencedType: "Quantity", ChoiceGroup: "value", ChoiceGroupRequired: true},
				{Name: "valueString", JSONType: "string", ChoiceGroup: "value", ChoiceGroupRequired: true},
			},
		},
		{Name: "CodeableConcept", Elements: []schema.Element{
			{Name: "coding", JSONType: schema.JSONTypeArray, ArrayElementType: "Coding"},
			{Name: "text", JSONType: "string"},
		}},
		{Name: "Coding", Elements: []schema.Element{
			{Name: "system", JSONType: "string"},
			{Name: "code", JSONType: "string"},
			{Name: "display", JSONType: "string"},
		}},
		{Name: "Quantity", Elements: []schema.Element{
			{Name: "value", JSONType: "number"},
			{Name: "unit", JSONType: "string"},
		}},
		{Name: "Identifier", Elements: []schema.Element{
			{Name: "system", JSONType: "string"},
			{Name: "value", JSONType: "string"},
		}},
		{Name: "Extension", Elements: []schema.Element{
			{Name: "url", JSONType: "string"},
			{Name: "valueString", JSONType: "string", ChoiceGroup: "value"},
		}},
	})
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	p := NewProfilerForGeneration("project", "generation", "scope", "SignalPacket", nil)
	payload := map[string]any{
		"measurements": []any{map[string]any{
			"code": map[string]any{"coding": []any{map[string]any{
				"system": "urn:signals", "code": "zero", "display": "Zero signal",
			}}},
			"valueQuantity": map[string]any{"value": 0.0, "unit": "mg"},
		}},
		"keys": []any{map[string]any{
			"system": "urn:packet", "value": "packet-1",
		}},
		"addons": []any{map[string]any{
			"url": "urn:addon", "valueString": "present",
		}},
	}
	var observations []schemaSemanticEmission
	if err := p.observeSchemaSemantics(index, payload, "", func(observation SemanticObservation, examples []any) {
		observations = append(observations, schemaSemanticEmission{observation: observation, examples: examples})
	}); err != nil {
		t.Fatalf("observeSchemaSemantics: %v", err)
	}

	coded := findSchemaObservation(t, observations, SemanticRuleHintCodedValueV1)
	if coded.observation.Source.Path != "measurements[]" || coded.observation.OwningScope != "measurements[]" || coded.observation.Key.Selector != "code.coding[]" {
		t.Fatalf("coded owner = %#v", coded)
	}
	if coded.observation.Key.System != "urn:signals" || coded.observation.Key.Code != "zero" || coded.observation.Value.Selector != "valueQuantity.value" || coded.observation.Value.Type != "decimal" {
		t.Fatalf("coded pairing = %#v", coded)
	}
	if len(coded.examples) != 1 || fmt.Sprint(coded.examples[0]) != "0" {
		t.Fatalf("coded zero example = %#v", coded.examples)
	}
	if len(coded.observation.ObservedUnits) != 1 || coded.observation.ObservedUnits[0] != "mg" {
		t.Fatalf("coded units = %#v", coded.observation.ObservedUnits)
	}

	identifier := findSchemaObservation(t, observations, "IDENTIFIER_SYSTEM_VALUE")
	if identifier.observation.Source.Path != "keys[]" || identifier.observation.Key.Selector != "keys[].system" || identifier.observation.Value.Selector != "keys[].value" || identifier.observation.Key.System != "urn:packet" {
		t.Fatalf("identifier association = %#v", identifier)
	}

	extension := findSchemaObservation(t, observations, "EXTENSION_URL_VALUE")
	if extension.observation.Source.Path != "addons[]" || extension.observation.Key.Selector != "addons[].url" || extension.observation.Value.Selector != "addons[].valueString" {
		t.Fatalf("extension association = %#v", extension)
	}
	if fmt.Sprint(extension.observation.ExtensionURLPath) != "[urn:addon]" {
		t.Fatalf("extension ancestry = %#v", extension.observation.ExtensionURLPath)
	}
}

func TestSchemaSemanticDiscoveryEmitsStandaloneCategoricalDatatypes(t *testing.T) {
	index, err := schema.NewIndex([]schema.Definition{
		{
			Name: "SignalEnvelope",
			Elements: []schema.Element{
				{Name: "unusualTerms", JSONType: schema.JSONTypeArray, ArrayElementType: "CodeableConcept"},
				{Name: "observations", JSONType: schema.JSONTypeArray, ArrayElementType: "SignalReading"},
				{Name: "unusualCoding", JSONType: schema.JSONTypeObject, ReferencedType: "Coding"},
			},
		},
		{
			Name: "SignalReading",
			Elements: []schema.Element{
				{Name: "measuredValue", JSONType: schema.JSONTypeObject, ReferencedType: "Quantity", ChoiceGroup: "reading"},
			},
		},
		{Name: "CodeableConcept", Elements: []schema.Element{
			{Name: "coding", JSONType: schema.JSONTypeArray, ArrayElementType: "Coding"},
			{Name: "text", JSONType: "string"},
		}},
		{Name: "Coding", Elements: []schema.Element{
			{Name: "system", JSONType: "string"},
			{Name: "version", JSONType: "string"},
			{Name: "code", JSONType: "string"},
			{Name: "display", JSONType: "string"},
		}},
		{Name: "Quantity", Elements: []schema.Element{
			{Name: "value", JSONType: "number"},
			{Name: "unit", JSONType: "string"},
		}},
	})
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	p := NewProfilerForGeneration("project", "generation", "scope", "SignalEnvelope", nil)
	payload := map[string]any{
		"unusualTerms": []any{
			map[string]any{"coding": []any{map[string]any{"system": "urn:custom", "version": "r7", "code": "alpha", "display": "Alpha"}}},
			map[string]any{"text": "Plain label"},
		},
		"observations":  []any{map[string]any{"measuredValue": map[string]any{"value": 7.0, "unit": "mg"}}},
		"unusualCoding": map[string]any{"system": "urn:direct", "version": "d2", "code": "beta", "display": "Beta"},
	}
	var observations []schemaSemanticEmission
	if err := p.observeSchemaSemantics(index, payload, "", func(observation SemanticObservation, examples []any) {
		observations = append(observations, schemaSemanticEmission{observation: observation, examples: examples})
	}); err != nil {
		t.Fatalf("observeSchemaSemantics: %v", err)
	}

	var concept, direct int
	for _, emission := range observations {
		observation := emission.observation
		switch observation.RuleHint {
		case SemanticRuleHintCategoricalSlotV1:
			if observation.Role != SemanticRoleCategoricalSlot || observation.Key.Code != "" || observation.Key.Display != "" || (observation.Key.System == "" && observation.Key.Selector != observation.Value.Selector) {
				t.Fatalf("categorical slot leaked observed coding identity: %#v", observation)
			}
			switch observation.Source.Path {
			case "unusualTerms[]":
				if observation.Key.Selector == "unusualTerms[].coding[]" && observation.Key.System == "urn:custom" && observation.Value.Selector == "unusualTerms[].coding[].code" && len(emission.examples) == 1 {
					var coding map[string]any
					if err := json.Unmarshal([]byte(fmt.Sprint(emission.examples[0])), &coding); err != nil || coding["system"] != "urn:custom" || coding["version"] != "r7" || coding["code"] != "alpha" || coding["display"] != "Alpha" {
						t.Fatalf("categorical domain = %#v", emission.examples)
					}
					concept++
				}
			case "unusualCoding":
				if observation.Key.Selector == "unusualCoding" && observation.Key.System == "urn:direct" && observation.Value.Selector == "unusualCoding.code" && len(emission.examples) == 1 {
					var coding map[string]any
					if err := json.Unmarshal([]byte(fmt.Sprint(emission.examples[0])), &coding); err != nil || coding["system"] != "urn:direct" || coding["version"] != "d2" || coding["code"] != "beta" || coding["display"] != "Beta" {
						t.Fatalf("direct categorical domain = %#v", emission.examples)
					}
					direct++
				}
			}
		case SemanticRuleHintCodedValueV1:
			t.Fatalf("unrelated repeated category and value were paired: %#v", observation)
		}
	}
	if concept != 1 || direct != 1 {
		t.Fatalf("standalone category counts concept/direct = %d/%d, want 1 each; observations=%#v", concept, direct, observations)
	}
}

type schemaSemanticEmission struct {
	observation SemanticObservation
	examples    []any
}

func findSchemaObservation(t *testing.T, observations []schemaSemanticEmission, rule string) schemaSemanticEmission {
	t.Helper()
	for _, observation := range observations {
		if observation.observation.RuleHint == rule {
			return observation
		}
	}
	t.Fatalf("semantic observation with rule %q not found in %#v", rule, observations)
	return schemaSemanticEmission{}
}

func generatedSemanticObservations(t *testing.T, resourceType string, payload map[string]any) []SemanticObservation {
	t.Helper()
	p := NewProfilerForGeneration("project", "generation", "scope", resourceType, nil)
	p.ObservePayload(payload, map[string]float64{})
	var observations []SemanticObservation
	for _, document := range p.Documents() {
		observations = append(observations, document.SemanticObservations...)
	}
	return observations
}

func decodeDomainExample(t *testing.T, raw string) map[string]any {
	t.Helper()
	var domain map[string]any
	if err := json.Unmarshal([]byte(raw), &domain); err != nil {
		t.Fatalf("decode domain example %q: %v", raw, err)
	}
	return domain
}

func TestSchemaSemanticDiscoveryAggregatesConditionCategoricalSlots(t *testing.T) {
	observations := generatedSemanticObservations(t, "Condition", map[string]any{
		"resourceType": "Condition",
		"code": map[string]any{"coding": []any{
			map[string]any{"system": "urn:unknown/", "version": "2026", "code": "MYELOID", "display": "Myeloid sarcoma"},
			map[string]any{"system": "urn:other/", "code": "MS-2", "display": "Alternate label"},
		}},
		"category": []any{
			map[string]any{"coding": []any{map[string]any{"system": "urn:category", "code": "problem-list-item"}}},
			map[string]any{"coding": []any{map[string]any{"system": "urn:category", "code": "encounter-diagnosis"}}},
		},
	})
	var codeSlots, categorySlots int
	for _, observation := range observations {
		if observation.Role != SemanticRoleCategoricalSlot {
			continue
		}
		switch observation.Source.Path {
		case "code":
			codeSlots++
			if observation.Status != "SUPPORTED" || observation.Key.Code != "" || observation.Key.Display != "" || observation.Key.System == "" {
				t.Fatalf("Condition.code leaked observed identity into slot key: %#v", observation)
			}
			if observation.SlotLabel == "Myeloid sarcoma" || observation.SlotLabel == "" || !strings.Contains(observation.SlotLabel, "condition") && !strings.Contains(strings.ToLower(observation.SlotDescription), "condition") {
				t.Fatalf("Condition.code schema presentation = %#v", observation)
			}
			if len(observation.Examples) != 1 {
				t.Fatalf("Condition.code domain examples = %#v, want one namespace-local coding", observation.Examples)
			}
			seen := map[string]bool{}
			systems := map[string]bool{}
			for _, raw := range observation.Examples {
				domain := decodeDomainExample(t, raw)
				seen[fmt.Sprint(domain["code"])] = true
				systems[fmt.Sprint(domain["system"])] = true
			}
			if len(seen) != 1 || len(systems) != 1 {
				t.Fatalf("Condition.code domain lost exact coding values: codes=%#v systems=%#v", seen, systems)
			}
			if observation.Key.System == "urn:unknown/" && !seen["MYELOID"] || observation.Key.System == "urn:other/" && !seen["MS-2"] {
				t.Fatalf("Condition.code namespace crossed coding values: key=%q codes=%#v", observation.Key.System, seen)
			}
		case "category[]":
			categorySlots++
			if observation.Key.Code != "" || observation.Key.System != "urn:category" || len(observation.Examples) != 2 {
				t.Fatalf("Condition.category slot = %#v, want one namespace slot with two domains", observation)
			}
		}
	}
	if codeSlots != 2 || categorySlots != 1 {
		t.Fatalf("Condition structural slot counts code/category = %d/%d, want 2/1", codeSlots, categorySlots)
	}
}

func TestSchemaSemanticDiscoveryUsesPrimitiveBindingsAndKeepsObservationPairs(t *testing.T) {
	observations := generatedSemanticObservations(t, "Observation", map[string]any{
		"resourceType":  "Observation",
		"status":        "final",
		"code":          map[string]any{"coding": []any{map[string]any{"system": "urn:panel/", "code": "height", "display": "Height"}}},
		"valueQuantity": map[string]any{"value": 172.5, "unit": "cm", "code": "cm"},
		"component": []any{map[string]any{
			"code":         map[string]any{"coding": []any{map[string]any{"system": "urn:component/", "code": "systolic"}}},
			"valueInteger": 120,
		}},
	})
	var primitive, rootPair, componentPair, valuePayload, quantityCode int
	for _, observation := range observations {
		switch observation.Role {
		case SemanticRoleCategoricalSlot:
			if observation.Source.Path == "status" {
				primitive++
				if observation.Key.Selector != "status" || observation.Value.Selector != "status" || len(observation.Examples) != 1 || observation.Examples[0] != "final" {
					t.Fatalf("primitive code slot = %#v", observation)
				}
			}
			if observation.Source.Path == "valueCodeableConcept" {
				valuePayload++
			}
			if observation.Source.Path == "valueQuantity.code" {
				quantityCode++
			}
		case SemanticRoleCodedValue:
			if observation.OwningScope == "" && observation.Key.Code == "height" {
				rootPair++
				if observation.Value.Selector != "valueQuantity.value" {
					t.Fatalf("root pair value selector = %#v", observation)
				}
			}
			if observation.OwningScope == "component[]" && observation.Key.Code == "systolic" {
				componentPair++
				if observation.Value.Selector != "valueInteger" {
					t.Fatalf("component pair value selector = %#v", observation)
				}
			}
		}
	}
	if primitive != 1 || rootPair != 1 || componentPair != 1 || valuePayload != 0 || quantityCode != 0 {
		t.Fatalf("Observation semantic roles primitive/root/component/value-payload/quantity-code = %d/%d/%d/%d/%d, want 1/1/1/0/0", primitive, rootPair, componentPair, valuePayload, quantityCode)
	}
}

func TestSchemaSemanticDiscoveryUsesDeclarativeCDARepresentationPairing(t *testing.T) {
	observations := generatedSemanticObservations(t, "SubstanceDefinition", map[string]any{
		"resourceType": "SubstanceDefinition",
		"structure": map[string]any{"representation": []any{
			map[string]any{
				"format":         map[string]any{"coding": []any{map[string]any{"system": "urn:format/", "code": "smiles", "display": "SMILES"}}},
				"representation": "C1=CC=CC=C1",
			},
		}},
	})
	var paired int
	for _, observation := range observations {
		if observation.Role == SemanticRoleCodedValue && observation.Key.Code == "smiles" {
			paired++
			if observation.OwningScope != "structure.representation[]" || observation.Value.Selector != "representation" {
				t.Fatalf("representation pairing = %#v", observation)
			}
		}
	}
	if paired != 1 {
		t.Fatalf("representation pair observations = %d, want 1", paired)
	}
}
