package recipe

import (
	"math"
	"strings"
	"testing"
)

func TestValidateDerivedColumnsKeepsArithmeticContractClosed(t *testing.T) {
	integer := int64(2)
	decimal := float64(0.5)
	base := DerivedColumn{
		ConstructionID: "calc_total", Name: "total", Label: "Total", Operation: DerivedAdd,
		Left:               DerivedOperand{Kind: DerivedLiteralOperand, Literal: &DerivedLiteral{Kind: NumericInteger, Integer: &integer}},
		Right:              DerivedOperand{Kind: DerivedLiteralOperand, Literal: &DerivedLiteral{Kind: NumericDecimal, Decimal: &decimal}},
		MissingInputPolicy: MissingInputPropagateNull,
	}
	bundle := derivedValidationBundle(base)
	if err := bundle.Validate(); err != nil {
		t.Fatalf("valid derived arithmetic was rejected: %v", err)
	}

	divide := base
	divide.ConstructionID = "calc_divide"
	divide.Name = "ratio"
	divide.Operation = DerivedDivide
	divide.DivisionByZeroPolicy = DivisionByZeroNull
	if err := derivedValidationBundle(divide).Validate(); err != nil {
		t.Fatalf("valid divide policy was rejected: %v", err)
	}

	tests := []struct {
		name string
		edit func([]DerivedColumn) []DerivedColumn
		want string
	}{
		{"invalid operation", func(columns []DerivedColumn) []DerivedColumn { columns[0].Operation = "POWER"; return columns }, "invalid_derived_operation"},
		{"missing policy", func(columns []DerivedColumn) []DerivedColumn { columns[0].MissingInputPolicy = ""; return columns }, "invalid_missing_input_policy"},
		{"divide policy required", func(columns []DerivedColumn) []DerivedColumn { columns[0].Operation = DerivedDivide; return columns }, "invalid_division_by_zero_policy"},
		{"zero policy on add", func(columns []DerivedColumn) []DerivedColumn {
			columns[0].DivisionByZeroPolicy = DivisionByZeroError
			return columns
		}, "invalid_division_by_zero_policy"},
		{"non-numeric literal", func(columns []DerivedColumn) []DerivedColumn { columns[0].Left.Literal.Kind = "STRING"; return columns }, "invalid_derived_literal"},
		{"multiple literal payloads", func(columns []DerivedColumn) []DerivedColumn {
			columns[0].Left.Literal.Decimal = &decimal
			return columns
		}, "invalid_derived_literal"},
		{"non-finite decimal", func(columns []DerivedColumn) []DerivedColumn {
			value := math.Inf(1)
			columns[0].Right.Literal.Decimal = &value
			return columns
		}, "invalid_derived_literal"},
		{"duplicate construction id", func(columns []DerivedColumn) []DerivedColumn {
			other := columns[0]
			other.Name = "other"
			columns = append(columns, other)
			return columns
		}, "duplicate_construction_id"},
		{"duplicate output", func(columns []DerivedColumn) []DerivedColumn {
			other := columns[0]
			other.ConstructionID = "other"
			columns = append(columns, other)
			return columns
		}, "duplicate_derived_column"},
		{"dependency cycle", func(columns []DerivedColumn) []DerivedColumn {
			columns[0].Left = DerivedOperand{Kind: DerivedColumnOperand, Column: "next"}
			other := columns[0]
			other.ConstructionID, other.Name, other.Label = "next_calc", "next", "Next"
			other.Left = DerivedOperand{Kind: DerivedColumnOperand, Column: "total"}
			return append(columns, other)
		}, "derived_column_cycle"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			columns := test.edit([]DerivedColumn{validDerivedColumn()})
			err := derivedValidationBundle(columns...).Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func validDerivedColumn() DerivedColumn {
	integer := int64(2)
	decimal := float64(0.5)
	return DerivedColumn{
		ConstructionID: "calc_total", Name: "total", Label: "Total", Operation: DerivedAdd,
		Left:               DerivedOperand{Kind: DerivedLiteralOperand, Literal: &DerivedLiteral{Kind: NumericInteger, Integer: &integer}},
		Right:              DerivedOperand{Kind: DerivedLiteralOperand, Literal: &DerivedLiteral{Kind: NumericDecimal, Decimal: &decimal}},
		MissingInputPolicy: MissingInputPropagateNull,
	}
}

func derivedValidationBundle(columns ...DerivedColumn) Bundle {
	return Bundle{
		RecipeSchemaVersion: CurrentSchemaVersion, Name: "derived", TranslationVersion: "test",
		Outputs: []Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", DerivedColumns: columns}},
	}
}
