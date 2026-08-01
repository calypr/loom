package compiler

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestTypedFilterValidation(t *testing.T) {
	melanoma := "melanoma"
	zero := int64(0)
	tests := []struct {
		name    string
		filter  spec.TypedFilter
		wantErr string
	}{
		{
			name: "string equality",
			filter: spec.TypedFilter{FieldRef: "condition.code.display", FieldKind: spec.FilterString, Operator: spec.FilterEquals,
				Values: []spec.FilterValue{{Kind: spec.FilterString, String: &melanoma}}},
		},
		{
			name: "zero integer is present",
			filter: spec.TypedFilter{FieldRef: "observation.value", FieldKind: spec.FilterInteger, Operator: spec.FilterGreaterEq,
				Values: []spec.FilterValue{{Kind: spec.FilterInteger, Integer: &zero}}},
		},
		{
			name: "repeated requires quantifier",
			filter: spec.TypedFilter{FieldRef: "condition.code", FieldKind: spec.FilterString, Repeated: true, Operator: spec.FilterEquals,
				Values: []spec.FilterValue{{Kind: spec.FilterString, String: &melanoma}}},
			wantErr: "requires ANY, ALL, or NONE",
		},
		{
			name: "scalar rejects quantifier",
			filter: spec.TypedFilter{FieldRef: "patient.gender", FieldKind: spec.FilterString, Quantifier: spec.QuantifierAny, Operator: spec.FilterEquals,
				Values: []spec.FilterValue{{Kind: spec.FilterString, String: &melanoma}}},
			wantErr: "only valid for repeated",
		},
		{
			name:    "exists rejects a value",
			filter:  spec.TypedFilter{FieldRef: "patient.birthDate", FieldKind: spec.FilterDate, Operator: spec.FilterExists, Values: []spec.FilterValue{{Kind: spec.FilterString, String: &melanoma}}},
			wantErr: "requires 0 value",
		},
		{
			name:    "in requires values",
			filter:  spec.TypedFilter{FieldRef: "patient.gender", FieldKind: spec.FilterString, Operator: spec.FilterIn},
			wantErr: "at least one value",
		},
		{
			name: "comparison rejects string",
			filter: spec.TypedFilter{FieldRef: "patient.gender", FieldKind: spec.FilterString, Operator: spec.FilterGreaterThan,
				Values: []spec.FilterValue{{Kind: spec.FilterString, String: &melanoma}}},
			wantErr: "not compatible",
		},
		{
			name: "value kind must match field",
			filter: spec.TypedFilter{FieldRef: "observation.value", FieldKind: spec.FilterDecimal, Operator: spec.FilterEquals,
				Values: []spec.FilterValue{{Kind: spec.FilterInteger, Integer: &zero}}},
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
	for _, value := range []spec.FilterValue{
		{Kind: spec.FilterDate, Date: &validDate},
		{Kind: spec.FilterDate, Date: &partialDate},
		{Kind: spec.FilterDateTime, DateTime: &validDateTime},
		{Kind: spec.FilterDateTime, DateTime: &partialDateTime},
	} {
		if err := value.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if err := (spec.FilterValue{Kind: spec.FilterDate, Date: &invalidDate}).Validate(); err == nil {
		t.Fatal("expected invalid date to be rejected")
	}
}

func TestTypedFilterRejectsPartialTemporalOrderedComparison(t *testing.T) {
	partialDate := "2026"
	if err := (spec.TypedFilter{
		FieldRef: "Patient.birth_date", FieldKind: spec.FilterDate, Operator: spec.FilterGreaterThan,
		Values: []spec.FilterValue{{Kind: spec.FilterDate, Date: &partialDate}},
	}).Validate(); err == nil || !strings.Contains(err.Error(), "full YYYY-MM-DD") {
		t.Fatalf("ordered partial date error = %v", err)
	}
}

func TestCodeValueRequiresTerminologyCode(t *testing.T) {
	value := spec.FilterValue{Kind: spec.FilterCode, Code: &spec.CodeValue{Display: "Melanoma"}}
	if err := value.Validate(); err == nil {
		t.Fatal("expected display-only code value to be rejected")
	}
}

func TestOperatorSupportsKind(t *testing.T) {
	if !spec.OperatorSupportsKind(spec.FilterContains, spec.FilterString) {
		t.Fatal("CONTAINS_TEXT should support STRING")
	}
	if spec.OperatorSupportsKind(spec.FilterContains, spec.FilterCode) {
		t.Fatal("CONTAINS_TEXT must not treat CODE display as terminology identity")
	}
	if !spec.OperatorSupportsKind(spec.FilterLessEq, spec.FilterDateTime) {
		t.Fatal("LTE should support DATE_TIME")
	}
}
