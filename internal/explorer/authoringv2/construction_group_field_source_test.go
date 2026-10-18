package authoringv2

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConstructionGroupKeyRequiresExactlyOneSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "legacy public column", body: `{"inputColumnId":"site","outputColumnId":"site_group"}`, want: true},
		{name: "field choice", body: `{"fieldSource":{"kind":"CHOICE","choiceId":"signed-choice"},"outputColumnId":"status_group"}`, want: true},
		{name: "legacy semantic choice", body: `{"semanticCategory":{"kind":"CHOICE","choiceId":"semantic-choice"},"outputColumnId":"category_group"}`, want: true},
		{name: "missing source", body: `{"outputColumnId":"group"}`},
		{name: "two sources", body: `{"inputColumnId":"site","semanticCategory":{"kind":"CHOICE","choiceId":"semantic-choice"},"outputColumnId":"group"}`},
		{name: "unknown sibling source", body: `{"inputColumnId":"site","fieldSource":{"kind":"CHOICE","choiceId":"signed-choice"},"outputColumnId":"group"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var key ConstructionGroupKey
			err := json.Unmarshal([]byte(test.body), &key)
			if (err == nil) != test.want {
				t.Fatalf("json.Unmarshal() error = %v, want success %t", err, test.want)
			}
		})
	}
}

func TestConstructionGroupFieldSourceBindingVariantsAreStrict(t *testing.T) {
	t.Parallel()

	validRootScalar := `{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"source_projection","sourceShapeId":"shape-root","sourceRole":"ROOT","occurrenceId":"root","resourceType":"Observation","ownerOccurrenceId":"root","ownerResourceType":"Observation","candidateId":"status-candidate","canonicalPath":"status"}`
	validAncestorScalar := `{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"source_projection","sourceShapeId":"shape-a","sourceRole":"ANCESTOR","occurrenceId":"observation","resourceType":"ObservationComponent","ownerOccurrenceId":"observation","ownerResourceType":"Observation","candidateId":"component-value","canonicalPath":"valueQuantity.value","boundarySelector":"component[]"}`
	validCoding := `{"kind":"BINDING","valueKind":"CODING_IDENTITY","sourceStageId":"source_projection","sourceShapeId":"shape-b","sourceRole":"CURRENT_ITEM","occurrenceId":"observation","resourceType":"Coding","ownerOccurrenceId":"observation","ownerResourceType":"Observation","canonicalPath":"valueCodeableConcept.coding[]"}`
	for _, body := range []string{validRootScalar, validAncestorScalar, validCoding} {
		var source ConstructionGroupFieldSource
		if err := json.Unmarshal([]byte(body), &source); err != nil {
			t.Fatalf("decode valid BINDING %s: %v", body, err)
		}
		if err := validateConstructionGroupFieldSource(source); err != nil {
			t.Fatalf("validate valid BINDING %s: %v", body, err)
		}
	}

	invalid := []string{
		`{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"source_projection","sourceShapeId":"shape-a","sourceRole":"ANCESTOR","occurrenceId":"observation","resourceType":"ObservationComponent","ownerOccurrenceId":"observation","ownerResourceType":"Observation","candidateId":"component-value","canonicalPath":"valueQuantity.value"}`,
		`{"kind":"BINDING","valueKind":"CODING_IDENTITY","sourceStageId":"source_projection","sourceShapeId":"shape-b","sourceRole":"CURRENT_ITEM","occurrenceId":"observation","resourceType":"Coding","ownerOccurrenceId":"observation","ownerResourceType":"Observation","candidateId":"fake-candidate","canonicalPath":"valueCodeableConcept.coding[]"}`,
		`{"kind":"BINDING","valueKind":"CODING_IDENTITY","sourceStageId":"source_projection","sourceShapeId":"shape-b","sourceRole":"ROOT","occurrenceId":"observation","resourceType":"Coding","ownerOccurrenceId":"observation","ownerResourceType":"Observation","canonicalPath":"valueCodeableConcept.coding[]"}`,
		`{"kind":"BINDING","valueKind":"CODING_IDENTITY","sourceStageId":"source_projection","sourceShapeId":"shape-b","sourceRole":"CURRENT_ITEM","occurrenceId":"observation","resourceType":"Coding","ownerOccurrenceId":"observation","ownerResourceType":"Observation","canonicalPath":"valueCodeableConcept.coding[]","boundarySelector":"valueCodeableConcept.coding[]"}`,
		`{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"source_projection","sourceShapeId":"shape-a","sourceRole":"ANCESTOR","occurrenceId":"observation","resourceType":"ObservationComponent","ownerOccurrenceId":"observation","ownerResourceType":"Observation","candidateId":"component-value","canonicalPath":"valueQuantity.value","boundarySelector":"component[]","arbitraryPath":"Observation.secret"}`,
	}
	for _, body := range invalid {
		var source ConstructionGroupFieldSource
		if err := json.Unmarshal([]byte(body), &source); err == nil {
			if err := validateConstructionGroupFieldSource(source); err == nil {
				t.Errorf("accepted invalid BINDING %s", body)
			}
		}
	}
}

func TestConstructionGroupFieldSourcePreservesLegacyCurrentStageWire(t *testing.T) {
	t.Parallel()

	legacy := []string{
		`{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"source_projection","sourceShapeId":"shape-root","sourceRole":"ROOT","occurrenceId":"root","resourceType":"Observation","ownerOccurrenceId":"root","ownerResourceType":"Observation","candidateId":"status-candidate","canonicalPath":"status"}`,
		`{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"source_projection","sourceShapeId":"shape-ancestor","sourceRole":"ANCESTOR","occurrenceId":"observation","resourceType":"ObservationComponent","ownerOccurrenceId":"observation","ownerResourceType":"Observation","candidateId":"component-value","canonicalPath":"valueQuantity.value","boundarySelector":"component[]"}`,
		`{"kind":"BINDING","valueKind":"CODING_IDENTITY","sourceStageId":"source_projection","sourceShapeId":"shape-coding","sourceRole":"CURRENT_ITEM","occurrenceId":"observation","resourceType":"Coding","ownerOccurrenceId":"observation","ownerResourceType":"Observation","canonicalPath":"valueCodeableConcept.coding[]"}`,
	}
	for _, raw := range legacy {
		var source ConstructionGroupFieldSource
		if err := json.Unmarshal([]byte(raw), &source); err != nil {
			t.Fatalf("decode legacy BINDING: %v", err)
		}
		if err := validateConstructionGroupFieldSource(source); err != nil {
			t.Fatalf("validate legacy BINDING: %v", err)
		}
		encoded, err := json.Marshal(source)
		if err != nil {
			t.Fatalf("marshal legacy BINDING: %v", err)
		}
		if string(encoded) != raw {
			t.Fatalf("legacy BINDING wire changed:\n got %s\nwant %s", encoded, raw)
		}
	}
}

func TestConstructionGroupFieldSourceUsesExplicitInputSum(t *testing.T) {
	t.Parallel()

	valid := []string{
		`{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"source_projection","sourceShapeId":"shape-root","input":{"kind":"CURRENT_STAGE","currentStage":{"sourceRole":"ROOT","occurrenceId":"root","ownerResourceType":"Observation","resourceType":"Observation","candidateId":"status-candidate","canonicalPath":"status"}}}`,
		`{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"source_projection","sourceShapeId":"shape-related","input":{"kind":"RELATED_OCCURRENCE","relatedOccurrence":{"anchorOccurrenceId":"root","anchorNodeId":"node-root","anchorResourceType":"Patient","route":[{"stepIndex":0,"fromOccurrenceId":"root","fromNodeId":"node-root","fromResourceType":"Patient","edgeId":"edge-subject","relationship":"subject","storageDirection":"INBOUND","matchMode":"OPTIONAL","toOccurrenceId":"observation-occurrence","toNodeId":"node-observation","toResourceType":"Observation"}],"targetOccurrenceId":"observation-occurrence","targetNodeId":"node-observation","targetResourceType":"Observation","resourceType":"Observation","candidateId":"status-candidate","canonicalPath":"status","emptyPolicy":"PRESERVE_PARENT","multiplicity":"DISTINCT_TARGET_PER_INPUT"}}}`,
		`{"kind":"BINDING","valueKind":"CODING_IDENTITY","sourceStageId":"source_projection","sourceShapeId":"shape-related-coding","input":{"kind":"RELATED_OCCURRENCE","relatedOccurrence":{"anchorOccurrenceId":"root","anchorNodeId":"node-root","anchorResourceType":"Patient","route":[{"stepIndex":0,"fromOccurrenceId":"root","fromNodeId":"node-root","fromResourceType":"Patient","edgeId":"edge-subject","relationship":"subject","storageDirection":"INBOUND","matchMode":"OPTIONAL","toOccurrenceId":"observation-occurrence","toNodeId":"node-observation","toResourceType":"Observation"}],"targetOccurrenceId":"observation-occurrence","targetNodeId":"node-observation","targetResourceType":"Observation","resourceType":"Coding","emptyPolicy":"PRESERVE_PARENT","multiplicity":"DISTINCT_TARGET_PER_INPUT","codingSelector":"valueCodeableConcept.coding[]","codingEmptyPolicy":"GROUP","ordinalSemantics":"ZERO_BASED_POSITION_IN_FLATTENED_EXPANSION_ARRAY"}}}`,
	}
	for _, raw := range valid {
		var source ConstructionGroupFieldSource
		if err := json.Unmarshal([]byte(raw), &source); err != nil {
			t.Fatalf("decode explicit input BINDING: %v", err)
		}
		if err := validateConstructionGroupFieldSource(source); err != nil {
			t.Fatalf("validate explicit input BINDING: %v", err)
		}
		encoded, err := json.Marshal(source)
		if err != nil {
			t.Fatalf("marshal explicit input BINDING: %v", err)
		}
		if string(encoded) != raw {
			t.Fatalf("input BINDING wire changed:\n got %s\nwant %s", encoded, raw)
		}
	}

	invalid := []string{
		`{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"source_projection","sourceShapeId":"shape","input":{"kind":"CURRENT_STAGE","currentStage":{"sourceRole":"ROOT","occurrenceId":"root","ownerResourceType":"Observation","resourceType":"Observation","candidateId":"status","canonicalPath":"status"},"relatedOccurrence":{"anchorOccurrenceId":"root"}}}`,
		`{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"source_projection","sourceShapeId":"shape","input":{"kind":"CURRENT_STAGE","currentStage":{"sourceRole":"ROOT","occurrenceId":"root","ownerResourceType":"Observation","resourceType":"Observation","candidateId":"status","canonicalPath":"status","untrustedPath":"Observation.secret"}}}`,
		`{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"source_projection","sourceShapeId":"shape","sourceRole":"ROOT","occurrenceId":"root","resourceType":"Observation","ownerOccurrenceId":"root","ownerResourceType":"Observation","candidateId":"status","canonicalPath":"status","input":{"kind":"CURRENT_STAGE","currentStage":{"sourceRole":"ROOT","occurrenceId":"root","ownerResourceType":"Observation","resourceType":"Observation","candidateId":"status","canonicalPath":"status"}}}`,
		`{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"source_projection","sourceShapeId":"shape","input":{"kind":"RELATED_OCCURRENCE","relatedOccurrence":{"anchorOccurrenceId":"root","anchorNodeId":"node-root","anchorResourceType":"Patient","route":[{"stepIndex":0,"fromOccurrenceId":"root","fromNodeId":"node-root","fromResourceType":"Patient","edgeId":"edge-subject","relationship":"subject","storageDirection":"INBOUND","matchMode":"OPTIONAL","toOccurrenceId":"different-occurrence","toNodeId":"node-other","toResourceType":"Observation"}],"targetOccurrenceId":"observation-occurrence","targetNodeId":"node-observation","targetResourceType":"Observation","resourceType":"Observation","candidateId":"status","canonicalPath":"status","emptyPolicy":"PRESERVE_PARENT","multiplicity":"DISTINCT_TARGET_PER_INPUT"}}}`,
		`{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"source_projection","sourceShapeId":"shape","input":{"kind":"RELATED_OCCURRENCE","relatedOccurrence":{"anchorOccurrenceId":"root","anchorNodeId":"node-root","anchorResourceType":"Patient","route":[{"stepIndex":0,"fromOccurrenceId":"root","fromNodeId":"node-root","fromResourceType":"Patient","edgeId":"edge-subject","relationship":"subject","storageDirection":"INBOUND","matchMode":"OPTIONAL","toOccurrenceId":"observation-occurrence","toNodeId":"node-observation","toResourceType":"Observation"}],"targetOccurrenceId":"observation-occurrence","targetNodeId":"node-observation","targetResourceType":"Observation","resourceType":"Observation","candidateId":"status","canonicalPath":"status","emptyPolicy":"PRESERVE_PARENT","multiplicity":"ALL_TARGETS_PER_INPUT"}}}`,
		`{"kind":"BINDING","valueKind":"CODING_IDENTITY","sourceStageId":"source_projection","sourceShapeId":"shape","input":{"kind":"RELATED_OCCURRENCE","relatedOccurrence":{"anchorOccurrenceId":"root","anchorNodeId":"node-root","anchorResourceType":"Patient","route":[{"stepIndex":0,"fromOccurrenceId":"root","fromNodeId":"node-root","fromResourceType":"Patient","edgeId":"edge-subject","relationship":"subject","storageDirection":"INBOUND","matchMode":"OPTIONAL","toOccurrenceId":"observation-occurrence","toNodeId":"node-observation","toResourceType":"Observation"}],"targetOccurrenceId":"observation-occurrence","targetNodeId":"node-observation","targetResourceType":"Observation","resourceType":"Coding","emptyPolicy":"PRESERVE_PARENT","multiplicity":"DISTINCT_TARGET_PER_INPUT","codingSelector":"valueCodeableConcept.coding[]","ordinalSemantics":"ZERO_BASED_POSITION_IN_FLATTENED_EXPANSION_ARRAY"}}}`,
	}
	for _, raw := range invalid {
		var source ConstructionGroupFieldSource
		if err := json.Unmarshal([]byte(raw), &source); err == nil {
			if err := validateConstructionGroupFieldSource(source); err == nil {
				t.Errorf("accepted invalid input BINDING %s", raw)
			}
		}
	}
}

func TestConstructionGroupFieldSourceChoiceCannotCarryInput(t *testing.T) {
	t.Parallel()

	var source ConstructionGroupFieldSource
	err := json.Unmarshal([]byte(`{"kind":"CHOICE","choiceId":"signed","input":{"kind":"CURRENT_STAGE","currentStage":{}}}`), &source)
	if err == nil || !strings.Contains(err.Error(), "CHOICE requires only choiceId") {
		t.Fatalf("decode CHOICE with binding input error = %v", err)
	}
}

func TestConstructionGroupFieldSourceNestedCurrentStageValidateCloneJSONCycle(t *testing.T) {
	t.Parallel()

	raw := `{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"stage-a","sourceShapeId":"shape-a","input":{"kind":"CURRENT_STAGE","currentStage":{"sourceRole":"ROOT","occurrenceId":"root","ownerResourceType":"Patient","resourceType":"Patient","candidateId":"status-candidate","canonicalPath":"active"}}}`
	var original ConstructionGroupFieldSource
	if err := json.Unmarshal([]byte(raw), &original); err != nil {
		t.Fatalf("decode nested current-stage BINDING: %v", err)
	}
	if err := original.Validate(); err != nil {
		t.Fatalf("validate nested current-stage BINDING: %v", err)
	}

	construction := &Construction{Version: 1, Steps: []ConstructionStep{{
		ID:     "group-step",
		Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}},
		Operation: ConstructionOperation{Kind: ConstructionOperationGroup, Group: &ConstructionGroup{
			ConstructionID: "construction-a",
			Keys:           []ConstructionGroupKey{{OutputColumnID: "active", FieldSource: &original}},
		}},
		Outputs: []StageColumn{{ID: "active", Name: "Active", Type: "boolean"}},
	}}}
	cloned, err := cloneConstruction(construction)
	if err != nil {
		t.Fatalf("clone construction: %v", err)
	}
	encoded, err := json.Marshal(cloned)
	if err != nil {
		t.Fatalf("marshal cloned construction: %v", err)
	}
	var roundTripped Construction
	if err := json.Unmarshal(encoded, &roundTripped); err != nil {
		t.Fatalf("unmarshal cloned construction: %v", err)
	}
	got := roundTripped.Steps[0].Operation.Group.Keys[0].FieldSource
	if got == nil || got.Input == nil || got.Input.Kind != "CURRENT_STAGE" || got.Input.CurrentStage == nil {
		t.Fatalf("nested CURRENT_STAGE provenance lost through clone/JSON cycle: %#v", got)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("validate round-tripped nested CURRENT_STAGE BINDING: %v", err)
	}
}

func TestConstructionGroupFieldSourceRejectsNestedInputWithLegacyFlatFields(t *testing.T) {
	t.Parallel()

	var source ConstructionGroupFieldSource
	err := json.Unmarshal([]byte(`{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"stage-a","sourceShapeId":"shape-a","sourceRole":"ROOT","occurrenceId":"root","ownerResourceType":"Patient","resourceType":"Patient","candidateId":"status-candidate","canonicalPath":"active","input":{"kind":"CURRENT_STAGE","currentStage":{"sourceRole":"ROOT","occurrenceId":"root","ownerResourceType":"Patient","resourceType":"Patient","candidateId":"status-candidate","canonicalPath":"active"}}}`), &source)
	if err == nil || !strings.Contains(err.Error(), "cannot combine input with legacy flat fields") {
		t.Fatalf("decode mixed nested and flat BINDING error = %v", err)
	}
}

func TestConstructionRowValueHasOneExactLegacyOrScalarFieldSource(t *testing.T) {
	t.Parallel()

	var value ConstructionRowValue
	if err := json.Unmarshal([]byte(`{"fieldSource":{"kind":"CHOICE","choiceId":"source-choice"},"outputColumnId":"tags","policy":"ALL"}`), &value); err != nil {
		t.Fatalf("decode scalar Group row-value CHOICE: %v", err)
	}
	if value.InputColumnID != "" || value.FieldSource == nil || value.FieldSource.Kind != "CHOICE" || value.OutputColumnID != "tags" {
		t.Fatalf("decoded field-source row value = %#v", value)
	}
	step := ConstructionStep{
		Operation: ConstructionOperation{Kind: ConstructionOperationGroup, Group: &ConstructionGroup{}},
		RowValues: []ConstructionRowValue{value},
		Outputs:   []StageColumn{{ID: "tags", Name: "tags", Type: "array"}},
	}
	if err := validateConstructionRowValues(step, map[string]StageColumn{}); err != nil {
		t.Fatalf("validate scalar Group row-value CHOICE: %v", err)
	}

	for _, raw := range []string{
		`{"outputColumnId":"tags","policy":"ALL"}`,
		`{"inputColumnId":"input","fieldSource":{"kind":"CHOICE","choiceId":"source-choice"},"outputColumnId":"tags","policy":"ALL"}`,
		`{"inputColumnId":"input","outputColumnId":"tags","policy":"ALL","arbitraryPath":"Observation.secret"}`,
	} {
		var invalid ConstructionRowValue
		if err := json.Unmarshal([]byte(raw), &invalid); err == nil {
			t.Errorf("accepted invalid row-value source shape %s", raw)
		}
	}

	step.RowValues[0].FieldSource = &ConstructionGroupFieldSource{
		Kind: "BINDING", ValueKind: "CODING_IDENTITY", SourceStageID: "source_projection", SourceShapeID: "shape",
		SourceRole: "CURRENT_ITEM", OccurrenceID: "observation", ResourceType: "Coding", OwnerOccurrenceID: "observation",
		OwnerResourceType: "Observation", CanonicalPath: "valueCodeableConcept.coding[]",
	}
	if err := validateConstructionRowValues(step, map[string]StageColumn{}); err == nil {
		t.Fatal("accepted CODING_IDENTITY as a row-value scalar")
	}
}
