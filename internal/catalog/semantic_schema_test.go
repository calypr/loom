package catalog

import (
	"fmt"
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
				{Name: "meaning", JSONType: schema.JSONTypeObject, ReferencedType: "CodeableConcept"},
				{Name: "resultQuantity", JSONType: schema.JSONTypeObject, ReferencedType: "Quantity", ChoiceGroup: "result", ChoiceGroupRequired: true},
				{Name: "resultString", JSONType: "string", ChoiceGroup: "result", ChoiceGroupRequired: true},
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
			"meaning": map[string]any{"coding": []any{map[string]any{
				"system": "urn:signals", "code": "zero", "display": "Zero signal",
			}}},
			"resultQuantity": map[string]any{"value": 0.0, "unit": "mg"},
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
	if coded.observation.Source.Path != "measurements[]" || coded.observation.OwningScope != "measurements[]" || coded.observation.Key.Selector != "meaning.coding[]" {
		t.Fatalf("coded owner = %#v", coded)
	}
	if coded.observation.Key.System != "urn:signals" || coded.observation.Key.Code != "zero" || coded.observation.Value.Selector != "resultQuantity.value" || coded.observation.Value.Type != "decimal" {
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

	var concept, direct, textOnly int
	for _, emission := range observations {
		observation := emission.observation
		switch observation.RuleHint {
		case SemanticRuleHintCategoricalCodeV1:
			switch observation.Source.Path {
			case "unusualTerms[]":
				if observation.Key.System == "urn:custom" && observation.Key.Version == "r7" && observation.Key.Code == "alpha" && observation.Key.Display == "Alpha" && observation.Key.Selector == "unusualTerms[].coding[]" && observation.Value.Selector == "unusualTerms[].coding[].code" {
					concept++
				}
			case "unusualCoding":
				if observation.Key.System == "urn:direct" && observation.Key.Version == "d2" && observation.Key.Code == "beta" && observation.Key.Display == "Beta" && observation.Key.Selector == "unusualCoding" && observation.Value.Selector == "unusualCoding.code" {
					direct++
				}
			}
		case SemanticRuleHintCategoricalTextV1:
			if observation.Source.Path == "unusualTerms[]" && observation.Key.Display == "Plain label" && observation.Value.Selector == "unusualTerms[].text" {
				textOnly++
			}
		case SemanticRuleHintCodedValueV1:
			t.Fatalf("unrelated repeated category and value were paired: %#v", observation)
		}
	}
	if concept != 1 || direct != 1 || textOnly != 1 {
		t.Fatalf("standalone category counts concept/direct/text = %d/%d/%d, want 1 each; observations=%#v", concept, direct, textOnly, observations)
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
