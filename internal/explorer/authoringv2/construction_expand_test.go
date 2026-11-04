package authoringv2

import (
	"reflect"
	"strings"
	"testing"
)

func expandConstruction() *Construction {
	return &Construction{Version: ConstructionVersion, Steps: []ConstructionStep{{
		ID:     "expand_names",
		Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}},
		Operation: ConstructionOperation{Kind: ConstructionOperationExpand, Expand: &ConstructionExpand{
			ConstructionID: "expand_names", InputColumnID: "patient_id", OutputColumnID: "name_item",
			EmptyPolicy: ConstructionExpandEmptyExclude,
		}},
		Outputs: []StageColumn{
			{ID: "name_item", Name: "name_item", Label: "Name item", Type: "string"},
		},
	}}}
}

func expandDocument() Document {
	document := workspaceDocument("expand")
	columns := append([]Column(nil), document.Columns...)
	columns[0].ColumnID = "patient_id"
	document.Columns = columns
	return document
}

func TestConstructionExpandWithoutExpandedFieldValidates(t *testing.T) {
	document := expandDocument()
	document.Construction = expandConstruction()
	if err := document.Validate(); err != nil {
		t.Fatalf("valid expand rejected: %v", err)
	}
}

func TestConstructionExpandedFieldValidPayloadRoundTripsPinned(t *testing.T) {
	document := expandDocument()
	construction := expandConstruction()
	construction.Steps[0].Operation.Expand.ExpandedField = &ConstructionExpandedFieldSource{
		Path: "name", Cardinality: ConstructionExpandedFieldCardinalityMany,
		FHIRType: "HumanName", LogicalType: "string",
	}
	document.Construction = construction
	if err := document.Validate(); err != nil {
		t.Fatalf("valid expandedField rejected: %v", err)
	}
	workspace := constructionWorkspace(document)
	encoded, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical workspace: %v", err)
	}
	decoded, err := DecodeWorkspace(encoded)
	if err != nil {
		t.Fatalf("decode workspace: %v", err)
	}
	got := decoded.Documents[0].Construction
	if !reflect.DeepEqual(got, construction) {
		t.Fatalf("expandedField changed during save/reload:\n got: %#v\nwant: %#v", got.Steps[0].Operation.Expand.ExpandedField, construction.Steps[0].Operation.Expand.ExpandedField)
	}
	if !strings.Contains(string(encoded), `"expandedField"`) {
		t.Fatalf("canonical JSON dropped expandedField: %s", encoded)
	}
}

func TestConstructionExpandedFieldShapeRefusals(t *testing.T) {
	cases := []struct {
		name    string
		field   ConstructionExpandedFieldSource
		wantErr string
	}{
		{"blank path", ConstructionExpandedFieldSource{Path: " ", Cardinality: ConstructionExpandedFieldCardinalityMany, LogicalType: "string"}, "expandedField.path"},
		{"uppercase cardinality", ConstructionExpandedFieldSource{Path: "name", Cardinality: "MANY", LogicalType: "string"}, "expandedField.cardinality"},
		{"empty cardinality", ConstructionExpandedFieldSource{Path: "name", LogicalType: "string"}, "expandedField.cardinality"},
		{"blank logical type", ConstructionExpandedFieldSource{Path: "name", Cardinality: ConstructionExpandedFieldCardinalityMany, LogicalType: " "}, "expandedField.logicalType"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			document := expandDocument()
			construction := expandConstruction()
			field := testCase.field
			construction.Steps[0].Operation.Expand.ExpandedField = &field
			document.Construction = construction
			err := document.Validate()
			if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
				t.Fatalf("got %v, want error containing %q", err, testCase.wantErr)
			}
		})
	}
}
