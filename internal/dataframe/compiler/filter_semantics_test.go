package compiler

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestValidateTypedFilterForResourceUsesGeneratedPrimitiveMetadata(t *testing.T) {
	female := "female"
	integer := int64(7)

	tests := []struct {
		name         string
		resourceType string
		filter       spec.TypedFilter
		wantErr      string
	}{
		{
			name: "string", resourceType: "Patient",
			filter: spec.TypedFilter{FieldRef: "Patient.gender", Selector: "gender", FieldKind: spec.FilterString, Operator: spec.FilterEquals, Values: []spec.FilterValue{{Kind: spec.FilterString, String: &female}}},
		},
		{
			name: "integer", resourceType: "Observation",
			filter: spec.TypedFilter{FieldRef: "Observation.valueInteger", Selector: "valueInteger", FieldKind: spec.FilterInteger, Operator: spec.FilterGreaterThan, Values: []spec.FilterValue{{Kind: spec.FilterInteger, Integer: &integer}}},
		},
		{
			name: "repeated scalar below repeated object", resourceType: "Observation",
			filter: spec.TypedFilter{FieldRef: "Observation.component_value_integer", Selector: "component[].valueInteger", FieldKind: spec.FilterInteger, Repeated: true, Quantifier: spec.QuantifierAny, Operator: spec.FilterEquals, Values: []spec.FilterValue{{Kind: spec.FilterInteger, Integer: &integer}}},
		},
		{
			name: "mismatched value kind", resourceType: "Patient",
			filter:  spec.TypedFilter{FieldRef: "Patient.gender", Selector: "gender", FieldKind: spec.FilterInteger, Operator: spec.FilterEquals, Values: []spec.FilterValue{{Kind: spec.FilterInteger, Integer: &integer}}},
			wantErr: "incompatible",
		},
		{
			name: "mismatched repeated cardinality", resourceType: "Observation",
			filter:  spec.TypedFilter{FieldRef: "Observation.component_value_integer", Selector: "component[].valueInteger", FieldKind: spec.FilterInteger, Operator: spec.FilterEquals, Values: []spec.FilterValue{{Kind: spec.FilterInteger, Integer: &integer}}},
			wantErr: "repeated",
		},
		{
			name: "implicit repeated navigation", resourceType: "Observation",
			filter:  spec.TypedFilter{FieldRef: "Observation.component_value_integer", Selector: "component.valueInteger", FieldKind: spec.FilterInteger, Operator: spec.FilterEquals, Values: []spec.FilterValue{{Kind: spec.FilterInteger, Integer: &integer}}},
			wantErr: "without []",
		},
		{
			name: "generated date time format", resourceType: "Observation",
			filter: spec.TypedFilter{FieldRef: "Observation.value_date_time", Selector: "valueDateTime", FieldKind: spec.FilterDateTime, Operator: spec.FilterGreaterThan, Values: []spec.FilterValue{{Kind: spec.FilterDateTime, DateTime: stringPtr("2025-01-01T00:00:00Z")}}},
		},
		{
			name: "generated date format", resourceType: "Patient",
			filter: spec.TypedFilter{FieldRef: "Patient.birth_date", Selector: "birthDate", FieldKind: spec.FilterDate, Operator: spec.FilterGreaterEq, Values: []spec.FilterValue{{Kind: spec.FilterDate, Date: stringPtr("2000-01-01")}}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := spec.ValidateTypedFilterForResource(test.resourceType, test.filter)
			if test.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestValidateTypedFilterForResourceRejectsUnpairedCodingSystem(t *testing.T) {
	code := "1234-5"
	err := spec.ValidateTypedFilterForResource("Observation", spec.TypedFilter{
		FieldRef: "Observation.code_coding_code", Selector: "code.coding[].code", FieldKind: spec.FilterCode,
		Repeated: true, Quantifier: spec.QuantifierAny, Operator: spec.FilterEquals,
		Values: []spec.FilterValue{{Kind: spec.FilterCode, Code: &spec.CodeValue{Code: code, System: "http://loinc.org"}}},
	})
	if err == nil || !strings.Contains(err.Error(), "paired Coding") {
		t.Fatalf("error = %v, want paired Coding rejection", err)
	}
}

func stringPtr(value string) *string { return &value }
