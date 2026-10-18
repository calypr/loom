package authoringv2

import (
	"encoding/json"
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
