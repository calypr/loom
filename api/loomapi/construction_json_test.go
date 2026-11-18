package loomapi

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestConstructionCombineGeneratedModelsRoundTrip(t *testing.T) {
	const raw = `{
	  "version": 1,
	  "steps": [
	    {
	      "id": "join",
	      "inputs": [
	        {"kind": "TABLE_REVISION", "tableId": "patients", "revisionId": "r1", "outputId": "out1"},
	        {"kind": "TABLE_REVISION", "tableId": "labs", "revisionId": "r2", "outputId": "out2"}
	      ],
	      "operation": {
	        "kind": "COMBINE",
	        "combine": {
	          "kind": "KEY_JOIN",
	          "keys": [{"leftColumnId": "patient_id", "rightColumnId": "subject_id"}],
	          "projections": [
	            {"outputColumnId": "patient_id", "inputIndex": 0, "inputColumnId": "patient_id"},
	            {"outputColumnId": "lab_value", "inputIndex": 1, "inputColumnId": "result_value"}
	          ],
	          "joinType": "LEFT",
	          "rightMatchPolicy": "PRESERVE_ALL"
	        }
	      },
	      "outputs": [
	        {"id": "patient_id", "name": "patient_id", "label": "Patient ID", "nullable": false},
	        {"id": "lab_value", "name": "lab_value", "label": "Lab value", "nullable": true}
	      ]
	    },
	    {
	      "id": "append",
	      "inputs": [
	        {"kind": "TABLE_REVISION", "tableId": "patients", "revisionId": "r1", "outputId": "out1"},
	        {"kind": "TABLE_REVISION", "tableId": "labs", "revisionId": "r2", "outputId": "out2"}
	      ],
	      "operation": {
	        "kind": "COMBINE",
	        "combine": {
	          "kind": "APPEND",
	          "projections": [
	            {"outputColumnId": "patient_id", "inputIndex": 0, "inputColumnId": "patient_id"},
	            {"outputColumnId": "patient_id", "inputIndex": 1, "inputColumnId": "person_id"}
	          ]
	        }
	      },
	      "outputs": [{"id": "patient_id", "name": "patient_id", "label": "Patient ID"}]
	    },
	    {
	      "id": "membership",
	      "inputs": [
	        {"kind": "TABLE_REVISION", "tableId": "patients", "revisionId": "r1", "outputId": "out1"},
	        {"kind": "TABLE_REVISION", "tableId": "labs", "revisionId": "r2", "outputId": "out2"}
	      ],
	      "operation": {
	        "kind": "COMBINE",
	        "combine": {
	          "kind": "MEMBERSHIP",
	          "keys": [{"leftColumnId": "patient_id", "rightColumnId": "subject_id"}],
	          "projections": [{"outputColumnId": "patient_id", "inputIndex": 0, "inputColumnId": "patient_id"}],
	          "membershipMode": "EXCLUDE"
	        }
	      },
	      "outputs": [{"id": "patient_id", "name": "patient_id", "label": "Patient ID"}]
	    }
	  ]
	}`

	var got Construction
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("decode construction: %v", err)
	}
	if len(got.Steps) != 3 {
		t.Fatalf("decoded %d construction steps, want 3", len(got.Steps))
	}
	join := got.Steps[0].Operation
	if join.Kind != "COMBINE" || join.Combine == nil {
		t.Fatalf("key join operation was not decoded: %+v", join)
	}
	if join.Combine.Kind != "KEY_JOIN" || join.Combine.Keys == nil || len(*join.Combine.Keys) != 1 || len(join.Combine.Projections) != 2 {
		t.Fatalf("key join payload changed: %+v", join.Combine)
	}
	if join.Combine.Projections[1].InputIndex != 1 || join.Combine.Projections[1].InputColumnId != "result_value" {
		t.Fatalf("right-side projection changed: %+v", join.Combine.Projections[1])
	}
	if join.Combine.JoinType == nil || *join.Combine.JoinType != "LEFT" || join.Combine.RightMatchPolicy == nil || *join.Combine.RightMatchPolicy != "PRESERVE_ALL" {
		t.Fatalf("key join policies changed: %+v", join.Combine)
	}
	if got.Steps[1].Operation.Combine == nil || got.Steps[1].Operation.Combine.Kind != "APPEND" {
		t.Fatalf("append payload was not decoded: %+v", got.Steps[1].Operation)
	}
	if got.Steps[2].Operation.Combine == nil || got.Steps[2].Operation.Combine.Kind != "MEMBERSHIP" || got.Steps[2].Operation.Combine.MembershipMode == nil || *got.Steps[2].Operation.Combine.MembershipMode != "EXCLUDE" {
		t.Fatalf("membership payload was not decoded: %+v", got.Steps[2].Operation)
	}
	if got.Steps[0].Inputs[0].Kind != "TABLE_REVISION" || got.Steps[0].Inputs[1].RevisionId == nil || *got.Steps[0].Inputs[1].RevisionId != "r2" {
		t.Fatalf("pinned table inputs changed: %+v", got.Steps[0].Inputs)
	}
	if got.Steps[0].Outputs[0].Nullable == nil || *got.Steps[0].Outputs[0].Nullable || got.Steps[0].Outputs[1].Nullable == nil || !*got.Steps[0].Outputs[1].Nullable {
		t.Fatalf("output nullability changed: %+v", got.Steps[0].Outputs)
	}

	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("encode construction: %v", err)
	}
	var roundTrip Construction
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatalf("decode round-tripped construction: %v", err)
	}
	if !reflect.DeepEqual(roundTrip, got) {
		t.Fatalf("construction changed after round trip\n got: %#v\nwant: %#v", roundTrip, got)
	}
}

func TestConstructionOpenAPISchemaIncludesCombineAndKeepsCardinalityResponseOnly(t *testing.T) {
	type schema struct {
		Type       string            `json:"type"`
		Ref        string            `json:"$ref"`
		Required   []string          `json:"required"`
		Enum       []string          `json:"enum"`
		Minimum    *int              `json:"minimum"`
		MinItems   *int              `json:"minItems"`
		Properties map[string]schema `json:"properties"`
	}
	var document struct {
		Components struct {
			Schemas map[string]schema `json:"schemas"`
		} `json:"components"`
	}
	raw, err := GetSpecJSON()
	if err != nil {
		t.Fatalf("read generated OpenAPI spec: %v", err)
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("decode generated OpenAPI spec: %v", err)
	}
	schemas := document.Components.Schemas
	operation := schemas["ConstructionOperation"]
	for _, kind := range []string{"PIVOT", "DERIVE", "FILTER", "UNPIVOT", "GROUP", "EXPAND", "COMBINE"} {
		if !containsSchemaEnum(operation.Properties["kind"].Enum, kind) {
			t.Errorf("ConstructionOperation.kind enum omits %s", kind)
		}
	}
	if !reflect.DeepEqual(operation.Required, []string{"kind"}) {
		t.Errorf("ConstructionOperation required fields = %v", operation.Required)
	}
	if got := operation.Properties["combine"].Ref; got != "#/components/schemas/ConstructionCombine" {
		t.Fatalf("ConstructionOperation.combine references %q", got)
	}
	combine := schemas["ConstructionCombine"]
	if !reflect.DeepEqual(combine.Required, []string{"kind", "projections"}) {
		t.Fatalf("ConstructionCombine required fields = %v", combine.Required)
	}
	for _, kind := range []string{"KEY_JOIN", "APPEND", "MEMBERSHIP"} {
		if !containsSchemaEnum(combine.Properties["kind"].Enum, kind) {
			t.Errorf("ConstructionCombine.kind enum omits %s", kind)
		}
	}
	if got := combine.Properties["keys"].MinItems; got == nil || *got != 1 {
		t.Errorf("ConstructionCombine.keys.minItems = %v, want 1", got)
	}
	if got := combine.Properties["projections"].MinItems; got == nil || *got != 1 {
		t.Errorf("ConstructionCombine.projections.minItems = %v, want 1", got)
	}
	if got := combine.Properties["joinType"].Enum; !reflect.DeepEqual(got, []string{"INNER", "LEFT"}) {
		t.Errorf("ConstructionCombine.joinType enum = %v", got)
	}
	if got := combine.Properties["rightMatchPolicy"].Enum; !reflect.DeepEqual(got, []string{"PRESERVE_ALL"}) {
		t.Errorf("ConstructionCombine.rightMatchPolicy enum = %v", got)
	}
	if got := combine.Properties["membershipMode"].Enum; !reflect.DeepEqual(got, []string{"INCLUDE", "EXCLUDE"}) {
		t.Errorf("ConstructionCombine.membershipMode enum = %v", got)
	}
	if got := schemas["ConstructionCombineProjection"].Properties["inputIndex"].Minimum; got == nil || *got != 0 {
		t.Errorf("ConstructionCombineProjection.inputIndex.minimum = %v, want 0", got)
	}
	if got := schemas["ConstructionCombineProjection"].Properties["inputIndex"].Type; got != "integer" {
		t.Errorf("ConstructionCombineProjection.inputIndex.type = %q, want integer", got)
	}
	if got := schemas["ConstructionInputRef"].Properties["kind"].Enum; !containsSchemaEnum(got, "TABLE_REVISION") {
		t.Errorf("ConstructionInputRef.kind enum omits TABLE_REVISION: %v", got)
	}
	if got := schemas["ConstructionStageColumn"].Properties["nullable"].Type; got != "boolean" {
		t.Errorf("ConstructionStageColumn.nullable.type = %q, want boolean", got)
	}
	if containsSchemaEnum(schemas["ConstructionStageColumn"].Required, "nullable") {
		t.Error("ConstructionStageColumn.nullable became required")
	}
	if _, exists := schemas["ConstructionStageColumn"].Properties["cardinality"]; exists {
		t.Error("response cardinality leaked into the authored ConstructionStageColumn")
	}
	if _, exists := schemas["ConstructionStageColumnDescriptor"].Properties["nullable"]; exists {
		t.Error("ConstructionStageColumnDescriptor exposes authored nullable metadata")
	}
	if got := schemas["ConstructionStageColumnDescriptor"].Properties["cardinality"].Enum; !reflect.DeepEqual(got, []string{"required_one", "optional_one", "many"}) {
		t.Errorf("response cardinality enum changed: %v", got)
	}
}

func containsSchemaEnum(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
