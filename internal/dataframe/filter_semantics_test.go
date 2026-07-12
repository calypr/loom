package dataframe

import (
	"strings"
	"testing"
)

func TestValidateTypedFilterForResourceUsesGeneratedPrimitiveMetadata(t *testing.T) {
	female := "female"
	integer := int64(7)

	tests := []struct {
		name         string
		resourceType string
		filter       TypedFilter
		wantErr      string
	}{
		{
			name: "string", resourceType: "Patient",
			filter: TypedFilter{FieldRef: "Patient.gender", Selector: "gender", FieldKind: FilterString, Operator: FilterEquals, Values: []FilterValue{{Kind: FilterString, String: &female}}},
		},
		{
			name: "integer", resourceType: "Observation",
			filter: TypedFilter{FieldRef: "Observation.valueInteger", Selector: "valueInteger", FieldKind: FilterInteger, Operator: FilterGreaterThan, Values: []FilterValue{{Kind: FilterInteger, Integer: &integer}}},
		},
		{
			name: "repeated scalar below repeated object", resourceType: "Observation",
			filter: TypedFilter{FieldRef: "Observation.component_value_integer", Selector: "component[].valueInteger", FieldKind: FilterInteger, Repeated: true, Quantifier: QuantifierAny, Operator: FilterEquals, Values: []FilterValue{{Kind: FilterInteger, Integer: &integer}}},
		},
		{
			name: "mismatched value kind", resourceType: "Patient",
			filter:  TypedFilter{FieldRef: "Patient.gender", Selector: "gender", FieldKind: FilterInteger, Operator: FilterEquals, Values: []FilterValue{{Kind: FilterInteger, Integer: &integer}}},
			wantErr: "incompatible",
		},
		{
			name: "mismatched repeated cardinality", resourceType: "Observation",
			filter:  TypedFilter{FieldRef: "Observation.component_value_integer", Selector: "component[].valueInteger", FieldKind: FilterInteger, Operator: FilterEquals, Values: []FilterValue{{Kind: FilterInteger, Integer: &integer}}},
			wantErr: "repeated",
		},
		{
			name: "implicit repeated navigation", resourceType: "Observation",
			filter:  TypedFilter{FieldRef: "Observation.component_value_integer", Selector: "component.valueInteger", FieldKind: FilterInteger, Operator: FilterEquals, Values: []FilterValue{{Kind: FilterInteger, Integer: &integer}}},
			wantErr: "without []",
		},
		{
			name: "generated date time format", resourceType: "Observation",
			filter: TypedFilter{FieldRef: "Observation.value_date_time", Selector: "valueDateTime", FieldKind: FilterDateTime, Operator: FilterGreaterThan, Values: []FilterValue{{Kind: FilterDateTime, DateTime: stringPtr("2025-01-01T00:00:00Z")}}},
		},
		{
			name: "generated date format", resourceType: "Patient",
			filter: TypedFilter{FieldRef: "Patient.birth_date", Selector: "birthDate", FieldKind: FilterDate, Operator: FilterGreaterEq, Values: []FilterValue{{Kind: FilterDate, Date: stringPtr("2000-01-01")}}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateTypedFilterForResource(test.resourceType, test.filter)
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
	err := ValidateTypedFilterForResource("Observation", TypedFilter{
		FieldRef: "Observation.code_coding_code", Selector: "code.coding[].code", FieldKind: FilterCode,
		Repeated: true, Quantifier: QuantifierAny, Operator: FilterEquals,
		Values: []FilterValue{{Kind: FilterCode, Code: &CodeValue{Code: code, System: "http://loinc.org"}}},
	})
	if err == nil || !strings.Contains(err.Error(), "paired Coding") {
		t.Fatalf("error = %v, want paired Coding rejection", err)
	}
}

func stringPtr(value string) *string { return &value }
