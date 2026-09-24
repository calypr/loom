package catalog

import (
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/fhir/schema"
)

type semanticPatternOracle struct {
	role         SemanticObservationRole
	sourcePath   string
	owner        string
	keySelector  string
	system       string
	code         string
	valuePath    string
	valueType    string
	presentation string
	choiceArm    string
	status       string
	examples     []string
	observedUnit []string
}

func TestSemanticProfilerCrossResourcePatternOracles(t *testing.T) {
	tests := []struct {
		name         string
		resourceType string
		payload      func(reverse bool) map[string]any
		want         []semanticPatternOracle
	}{
		{
			name:         "Task input and output",
			resourceType: "Task",
			payload:      taskSemanticPatternPayload,
			want: []semanticPatternOracle{
				semanticPatternPair("input[]", "type.coding[]", "urn:task", "input-bool", "valueBoolean", "boolean", "valueBoolean", "false"),
				semanticPatternSlot("input[].type", "input[].type.coding[]", "input[].type.coding[].code", "urn:task"),
				semanticPatternPairNoExample("input[]", "type.coding[]", "urn:task", "input-empty", "valueString", "string", "valueString"),
				semanticPatternPair("output[]", "type.coding[]", "urn:task", "output-zero", "valueInteger", "integer", "valueInteger", "0"),
				semanticPatternSlot("output[].type", "output[].type.coding[]", "output[].type.coding[].code", "urn:task"),
			},
		},
		{
			name:         "Group characteristic",
			resourceType: "Group",
			payload:      groupSemanticPatternPayload,
			want: []semanticPatternOracle{
				semanticPatternPair("characteristic[]", "code.coding[]", "urn:group", "sex", "valueBoolean", "boolean", "valueBoolean", "false"),
				semanticPatternSlot("characteristic[].code", "characteristic[].code.coding[]", "characteristic[].code.coding[].code", "urn:group"),
				semanticPatternPair("characteristic[]", "code.coding[]", "urn:group", "age", "valueQuantity.value", "decimal", "valueQuantity", "0"),
			},
		},
		{
			name:         "SubstanceDefinition property and representation",
			resourceType: "SubstanceDefinition",
			payload:      substanceSemanticPatternPayload,
			want: []semanticPatternOracle{
				semanticPatternPair("property[]", "type.coding[]", "urn:substance", "density", "valueQuantity.value", "decimal", "valueQuantity", "0"),
				semanticPatternSlot("property[].type", "property[].type.coding[]", "property[].type.coding[].code", "urn:substance"),
				semanticPatternPair("property[]", "type.coding[]", "urn:substance", "active", "valueBoolean", "boolean", "valueBoolean", "false"),
				{
					role:        SemanticRoleCodedValue,
					sourcePath:  "structure.representation[]",
					owner:       "structure.representation[]",
					keySelector: "format.coding[]",
					system:      "urn:format",
					code:        "smiles",
					valuePath:   "representation",
					valueType:   "string",
					status:      "SUPPORTED",
					examples:    []string{"C1=CC=CC=C1"},
				},
				semanticPatternSlot("structure.representation[].format", "structure.representation[].format.coding[]", "structure.representation[].format.coding[].code", "urn:format"),
			},
		},
		{
			name:         "Observation root distractors and metadata slots",
			resourceType: "Observation",
			payload:      observationSemanticPatternPayload,
			want: []semanticPatternOracle{
				{
					role:         SemanticRoleCodedValue,
					sourcePath:   "code.coding[]",
					keySelector:  "code.coding[]",
					system:       "urn:observation",
					code:         "height",
					valuePath:    "valueQuantity.value",
					valueType:    "decimal",
					choiceArm:    "valueQuantity",
					status:       "SUPPORTED",
					examples:     []string{"0"},
					observedUnit: []string{"cm"},
				},
				semanticPatternSlot("code", "code.coding[]", "code.coding[].code", "urn:observation"),
				semanticPatternSlot("category[]", "category[].coding[]", "category[].coding[].code", "urn:category"),
				semanticPatternSlot("interpretation[]", "interpretation[].coding[]", "interpretation[].coding[].code", "urn:interpretation"),
			},
		},
		{
			name:         "Observation component keys stay with their values",
			resourceType: "Observation",
			payload:      componentSemanticPatternPayload,
			want: []semanticPatternOracle{
				semanticPatternPair("component[]", "code.coding[]", "urn:component:A", "shared", "valueInteger", "integer", "valueInteger", "0"),
				semanticPatternPair("component[]", "code.coding[]", "urn:component:B", "shared", "valueInteger", "integer", "valueInteger", "1"),
				semanticPatternSlot("component[].code", "component[].code.coding[]", "component[].code.coding[].code", "urn:component:A"),
				semanticPatternSlot("component[].code", "component[].code.coding[]", "component[].code.coding[].code", "urn:component:B"),
			},
		},
		{
			name:         "UsageContext singleton Coding key",
			resourceType: "UsageContext",
			payload:      usageContextSemanticPatternPayload,
			want: []semanticPatternOracle{
				{
					role:        SemanticRoleCodedValue,
					sourcePath:  "code",
					keySelector: "code",
					system:      "urn:usage",
					code:        "setting",
					valuePath:   "valueQuantity.value",
					valueType:   "decimal",
					choiceArm:   "valueQuantity",
					status:      "SUPPORTED",
					examples:    []string{"0"},
				},
				semanticPatternSlot("code", "code", "code.code", "urn:usage"),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := semanticProfilerPatternObservations(t, test.resourceType, test.payload(false))
			reordered := semanticProfilerPatternObservations(t, test.resourceType, test.payload(true))
			if !reflect.DeepEqual(got, reordered) {
				t.Fatalf("semantic observations changed with JSON object property order:\nfirst=%#v\nreordered=%#v", got, reordered)
			}
			if len(got) != len(test.want) {
				t.Fatalf("semantic observation count = %d, want %d: %#v", len(got), len(test.want), got)
			}
			for _, want := range test.want {
				assertSemanticPatternOracle(t, got, want)
			}
			if test.name == "Observation root distractors and metadata slots" {
				for _, observation := range got {
					if observation.Role == SemanticRoleCodedValue && observation.Key.Code == "height" && observation.Value.Selector != "valueQuantity.value" {
						t.Fatalf("root Observation paired an unrelated choice: %#v", observation)
					}
					if observation.Role == SemanticRoleCategoricalSlot && observation.Value.Selector == "valueQuantity.value" {
						t.Fatalf("root Observation emitted a duplicate raw value path: %#v", observation)
					}
				}
			}
		})
	}
}

func semanticPatternPair(owner, keySelector, system, code, valuePath, valueType, choiceArm, example string) semanticPatternOracle {
	return semanticPatternOracle{
		role:        SemanticRoleCodedValue,
		sourcePath:  owner,
		owner:       owner,
		keySelector: keySelector,
		system:      system,
		code:        code,
		valuePath:   valuePath,
		valueType:   valueType,
		choiceArm:   choiceArm,
		status:      "SUPPORTED",
		examples:    []string{example},
	}
}

func semanticPatternPairNoExample(owner, keySelector, system, code, valuePath, valueType, choiceArm string) semanticPatternOracle {
	oracle := semanticPatternPair(owner, keySelector, system, code, valuePath, valueType, choiceArm, "")
	oracle.examples = nil
	return oracle
}

func semanticPatternSlot(sourcePath, keySelector, valuePath string, systems ...string) semanticPatternOracle {
	system := ""
	if len(systems) > 0 {
		system = systems[0]
	}
	return semanticPatternOracle{
		role:         SemanticRoleCategoricalSlot,
		sourcePath:   sourcePath,
		owner:        sourcePath,
		keySelector:  keySelector,
		system:       system,
		valuePath:    valuePath,
		valueType:    "string",
		presentation: schema.ValuePresentationDisplayOrCode,
		status:       "SUPPORTED",
	}
}

func assertSemanticPatternOracle(t *testing.T, observations []SemanticObservation, want semanticPatternOracle) {
	t.Helper()
	var matches []SemanticObservation
	for _, observation := range observations {
		if observation.Role != want.role ||
			observation.Source.Path != want.sourcePath ||
			observation.OwningScope != want.owner ||
			observation.Key.Selector != want.keySelector ||
			observation.Key.System != want.system ||
			observation.Key.Code != want.code ||
			observation.Value.Selector != want.valuePath {
			continue
		}
		matches = append(matches, observation)
	}
	if len(matches) != 1 {
		t.Fatalf("semantic oracle match count for %#v = %d, want 1: %#v", want, len(matches), observations)
	}
	got := matches[0]
	if got.Value.Type != want.valueType || got.LogicalType != want.valueType || got.Value.Presentation != want.presentation || got.ChoiceArm != want.choiceArm || got.Status != want.status {
		t.Fatalf("semantic oracle output = %#v, want type=%q logical=%q arm=%q status=%q", got, want.valueType, want.valueType, want.choiceArm, want.status)
	}
	if want.examples != nil && !reflect.DeepEqual(got.Examples, want.examples) {
		t.Fatalf("semantic oracle examples = %#v, want %#v for %#v", got.Examples, want.examples, want)
	}
	if want.observedUnit != nil && !reflect.DeepEqual(got.ObservedUnits, want.observedUnit) {
		t.Fatalf("semantic oracle units = %#v, want %#v for %#v", got.ObservedUnits, want.observedUnit, want)
	}
}

func semanticProfilerPatternObservations(t *testing.T, resourceType string, payload map[string]any) []SemanticObservation {
	t.Helper()
	profiler := NewProfilerForGeneration("project", "generation", "scope", resourceType, nil)
	profiler.ObservePayload(payload, map[string]float64{})
	var observations []SemanticObservation
	for _, document := range profiler.Documents() {
		observations = append(observations, document.SemanticObservations...)
	}
	return observations
}

type semanticPatternField struct {
	name  string
	value any
}

func semanticPatternMap(reverse bool, fields ...semanticPatternField) map[string]any {
	result := make(map[string]any, len(fields))
	if reverse {
		for index := len(fields) - 1; index >= 0; index-- {
			result[fields[index].name] = fields[index].value
		}
		return result
	}
	for _, field := range fields {
		result[field.name] = field.value
	}
	return result
}

func semanticPatternCoding(system, code string) map[string]any {
	return map[string]any{"system": system, "code": code}
}

func taskSemanticPatternPayload(reverse bool) map[string]any {
	return semanticPatternMap(reverse,
		semanticPatternField{name: "resourceType", value: "Task"},
		semanticPatternField{name: "input", value: []any{
			semanticPatternMap(reverse,
				semanticPatternField{name: "type", value: map[string]any{"coding": []any{semanticPatternCoding("urn:task", "input-bool")}}},
				semanticPatternField{name: "valueBoolean", value: false},
			),
			semanticPatternMap(!reverse,
				semanticPatternField{name: "valueString", value: ""},
				semanticPatternField{name: "type", value: map[string]any{"coding": []any{semanticPatternCoding("urn:task", "input-empty")}}},
			),
		}},
		semanticPatternField{name: "output", value: []any{
			semanticPatternMap(!reverse,
				semanticPatternField{name: "valueInteger", value: 0},
				semanticPatternField{name: "type", value: map[string]any{"coding": []any{semanticPatternCoding("urn:task", "output-zero")}}},
			),
		}},
	)
}

func groupSemanticPatternPayload(reverse bool) map[string]any {
	return semanticPatternMap(reverse,
		semanticPatternField{name: "resourceType", value: "Group"},
		semanticPatternField{name: "characteristic", value: []any{
			semanticPatternMap(reverse,
				semanticPatternField{name: "code", value: map[string]any{"coding": []any{semanticPatternCoding("urn:group", "sex")}}},
				semanticPatternField{name: "valueBoolean", value: false},
			),
			semanticPatternMap(!reverse,
				semanticPatternField{name: "valueQuantity", value: map[string]any{"unit": "kg", "value": 0.0}},
				semanticPatternField{name: "code", value: map[string]any{"coding": []any{semanticPatternCoding("urn:group", "age")}}},
			),
		}},
	)
}

func substanceSemanticPatternPayload(reverse bool) map[string]any {
	return semanticPatternMap(reverse,
		semanticPatternField{name: "resourceType", value: "SubstanceDefinition"},
		semanticPatternField{name: "property", value: []any{
			semanticPatternMap(reverse,
				semanticPatternField{name: "type", value: map[string]any{"coding": []any{semanticPatternCoding("urn:substance", "density")}}},
				semanticPatternField{name: "valueQuantity", value: map[string]any{"unit": "g/mL", "value": 0.0}},
			),
			semanticPatternMap(!reverse,
				semanticPatternField{name: "valueBoolean", value: false},
				semanticPatternField{name: "type", value: map[string]any{"coding": []any{semanticPatternCoding("urn:substance", "active")}}},
			),
		}},
		semanticPatternField{name: "structure", value: map[string]any{
			"representation": []any{
				semanticPatternMap(reverse,
					semanticPatternField{name: "format", value: map[string]any{"coding": []any{semanticPatternCoding("urn:format", "smiles")}}},
					semanticPatternField{name: "representation", value: "C1=CC=CC=C1"},
				),
			},
		}},
	)
}

func observationSemanticPatternPayload(reverse bool) map[string]any {
	return semanticPatternMap(reverse,
		semanticPatternField{name: "resourceType", value: "Observation"},
		semanticPatternField{name: "effectiveDateTime", value: "2026-09-20T00:00:00Z"},
		semanticPatternField{name: "code", value: map[string]any{"coding": []any{semanticPatternCoding("urn:observation", "height")}}},
		semanticPatternField{name: "category", value: []any{map[string]any{"coding": []any{semanticPatternCoding("urn:category", "vital-sign")}}}},
		semanticPatternField{name: "interpretation", value: []any{map[string]any{"coding": []any{semanticPatternCoding("urn:interpretation", "normal")}}}},
		semanticPatternField{name: "valueQuantity", value: map[string]any{"unit": "cm", "value": 0.0}},
	)
}

func componentSemanticPatternPayload(reverse bool) map[string]any {
	return semanticPatternMap(reverse,
		semanticPatternField{name: "resourceType", value: "Observation"},
		semanticPatternField{name: "component", value: []any{
			semanticPatternMap(reverse,
				semanticPatternField{name: "code", value: map[string]any{"coding": []any{semanticPatternCoding("urn:component:A", "shared")}}},
				semanticPatternField{name: "valueInteger", value: 0},
			),
			semanticPatternMap(!reverse,
				semanticPatternField{name: "valueInteger", value: 1},
				semanticPatternField{name: "code", value: map[string]any{"coding": []any{semanticPatternCoding("urn:component:B", "shared")}}},
			),
		}},
	)
}

func usageContextSemanticPatternPayload(reverse bool) map[string]any {
	return semanticPatternMap(reverse,
		semanticPatternField{name: "resourceType", value: "UsageContext"},
		semanticPatternField{name: "valueQuantity", value: map[string]any{"unit": "mg", "value": 0.0}},
		semanticPatternField{name: "code", value: semanticPatternCoding("urn:usage", "setting")},
	)
}
