package compiler

import (
	"strings"
	"testing"
)

func TestTypedFilterValidation(t *testing.T) {
	melanoma := "melanoma"
	zero := int64(0)
	tests := []struct {
		name    string
		filter  TypedFilter
		wantErr string
	}{
		{
			name: "string equality",
			filter: TypedFilter{FieldRef: "condition.code.display", FieldKind: FilterString, Operator: FilterEquals,
				Values: []FilterValue{{Kind: FilterString, String: &melanoma}}},
		},
		{
			name: "zero integer is present",
			filter: TypedFilter{FieldRef: "observation.value", FieldKind: FilterInteger, Operator: FilterGreaterEq,
				Values: []FilterValue{{Kind: FilterInteger, Integer: &zero}}},
		},
		{
			name: "repeated requires quantifier",
			filter: TypedFilter{FieldRef: "condition.code", FieldKind: FilterString, Repeated: true, Operator: FilterEquals,
				Values: []FilterValue{{Kind: FilterString, String: &melanoma}}},
			wantErr: "requires ANY, ALL, or NONE",
		},
		{
			name: "scalar rejects quantifier",
			filter: TypedFilter{FieldRef: "patient.gender", FieldKind: FilterString, Quantifier: QuantifierAny, Operator: FilterEquals,
				Values: []FilterValue{{Kind: FilterString, String: &melanoma}}},
			wantErr: "only valid for repeated",
		},
		{
			name:    "exists rejects a value",
			filter:  TypedFilter{FieldRef: "patient.birthDate", FieldKind: FilterDate, Operator: FilterExists, Values: []FilterValue{{Kind: FilterString, String: &melanoma}}},
			wantErr: "requires 0 value",
		},
		{
			name:    "in requires values",
			filter:  TypedFilter{FieldRef: "patient.gender", FieldKind: FilterString, Operator: FilterIn},
			wantErr: "at least one value",
		},
		{
			name: "comparison rejects string",
			filter: TypedFilter{FieldRef: "patient.gender", FieldKind: FilterString, Operator: FilterGreaterThan,
				Values: []FilterValue{{Kind: FilterString, String: &melanoma}}},
			wantErr: "not compatible",
		},
		{
			name: "value kind must match field",
			filter: TypedFilter{FieldRef: "observation.value", FieldKind: FilterDecimal, Operator: FilterEquals,
				Values: []FilterValue{{Kind: FilterInteger, Integer: &zero}}},
			wantErr: "does not match field kind",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.filter.Validate()
			if tt.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestFilterValueDateValidation(t *testing.T) {
	validDate := "2026-07-11"
	partialDate := "2026-07"
	invalidDate := "07/11/2026"
	validDateTime := "2026-07-11T12:30:00-07:00"
	partialDateTime := "2026"
	for _, value := range []FilterValue{
		{Kind: FilterDate, Date: &validDate},
		{Kind: FilterDate, Date: &partialDate},
		{Kind: FilterDateTime, DateTime: &validDateTime},
		{Kind: FilterDateTime, DateTime: &partialDateTime},
	} {
		if err := value.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if err := (FilterValue{Kind: FilterDate, Date: &invalidDate}).Validate(); err == nil {
		t.Fatal("expected invalid date to be rejected")
	}
}

func TestTypedFilterRejectsPartialTemporalOrderedComparison(t *testing.T) {
	partialDate := "2026"
	if err := (TypedFilter{
		FieldRef: "Patient.birth_date", FieldKind: FilterDate, Operator: FilterGreaterThan,
		Values: []FilterValue{{Kind: FilterDate, Date: &partialDate}},
	}).Validate(); err == nil || !strings.Contains(err.Error(), "full YYYY-MM-DD") {
		t.Fatalf("ordered partial date error = %v", err)
	}
}

func TestCodeValueRequiresTerminologyCode(t *testing.T) {
	value := FilterValue{Kind: FilterCode, Code: &CodeValue{Display: "Melanoma"}}
	if err := value.Validate(); err == nil {
		t.Fatal("expected display-only code value to be rejected")
	}
}

func TestOperatorSupportsKind(t *testing.T) {
	if !OperatorSupportsKind(FilterContains, FilterString) {
		t.Fatal("CONTAINS_TEXT should support STRING")
	}
	if OperatorSupportsKind(FilterContains, FilterCode) {
		t.Fatal("CONTAINS_TEXT must not treat CODE display as terminology identity")
	}
	if !OperatorSupportsKind(FilterLessEq, FilterDateTime) {
		t.Fatal("LTE should support DATE_TIME")
	}
}
