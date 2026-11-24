package recipe

import (
	"fmt"
	"math"
	"strings"
)

type DerivedColumn struct {
	ConstructionID       string               `json:"constructionId"`
	Name                 string               `json:"name"`
	Label                string               `json:"label"`
	Operation            DerivedOperation     `json:"operation"`
	Left                 DerivedOperand       `json:"left"`
	Right                DerivedOperand       `json:"right"`
	MissingInputPolicy   MissingInputPolicy   `json:"missingInputPolicy"`
	DivisionByZeroPolicy DivisionByZeroPolicy `json:"divisionByZeroPolicy,omitempty"`
}

type DerivedOperation string

const (
	DerivedAdd      DerivedOperation = "ADD"
	DerivedSubtract DerivedOperation = "SUBTRACT"
	DerivedMultiply DerivedOperation = "MULTIPLY"
	DerivedDivide   DerivedOperation = "DIVIDE"
)

type MissingInputPolicy string

const (
	MissingInputPropagateNull MissingInputPolicy = "PROPAGATE_NULL"
	MissingInputError         MissingInputPolicy = "ERROR"
)

type DivisionByZeroPolicy string

const (
	DivisionByZeroNull  DivisionByZeroPolicy = "NULL"
	DivisionByZeroError DivisionByZeroPolicy = "ERROR"
)

type DerivedOperand struct {
	Kind    DerivedOperandKind `json:"kind"`
	Column  string             `json:"column,omitempty"`
	Literal *DerivedLiteral    `json:"literal,omitempty"`
}

type DerivedOperandKind string

const (
	DerivedColumnOperand  DerivedOperandKind = "COLUMN"
	DerivedLiteralOperand DerivedOperandKind = "LITERAL"
)

type DerivedLiteral struct {
	Kind    NumericKind `json:"kind"`
	Integer *int64      `json:"integer,omitempty"`
	Decimal *float64    `json:"decimal,omitempty"`
}

type NumericKind string

const (
	NumericInteger NumericKind = "INTEGER"
	NumericDecimal NumericKind = "DECIMAL"
)

func (operation DerivedOperation) Valid() bool {
	switch operation {
	case DerivedAdd, DerivedSubtract, DerivedMultiply, DerivedDivide:
		return true
	default:
		return false
	}
}

func (operation DerivedOperation) AcceptsDivisionByZeroPolicy(policy DivisionByZeroPolicy) bool {
	if operation == DerivedDivide {
		return policy == DivisionByZeroNull || policy == DivisionByZeroError
	}
	return operation.Valid() && policy == ""
}

func (policy MissingInputPolicy) Valid() bool {
	return policy == MissingInputPropagateNull || policy == MissingInputError
}

func (kind DerivedOperandKind) ValidPayload(column string, hasLiteral bool) bool {
	switch kind {
	case DerivedColumnOperand:
		return column != "" && !hasLiteral
	case DerivedLiteralOperand:
		return column == "" && hasLiteral
	default:
		return false
	}
}

func (literal DerivedLiteral) ValidPayload() bool {
	switch literal.Kind {
	case NumericInteger:
		return literal.Integer != nil && literal.Decimal == nil
	case NumericDecimal:
		return literal.Decimal != nil && literal.Integer == nil
	default:
		return false
	}
}

func (literal DerivedLiteral) Valid() bool {
	return literal.ValidPayload() && (literal.Kind != NumericDecimal ||
		(!math.IsNaN(*literal.Decimal) && !math.IsInf(*literal.Decimal, 0)))
}

const maxDerivedColumns = 64

func validateDerivedColumns(columns []DerivedColumn, path string) error {
	if len(columns) > maxDerivedColumns {
		return validationError("derived_column_limit", path, fmt.Sprintf("at most %d derived columns are allowed", maxDerivedColumns))
	}
	constructionIDs := make(map[string]bool, len(columns))
	outputs := make(map[string]bool, len(columns))
	for index, column := range columns {
		columnPath := fmt.Sprintf("%s[%d]", path, index)
		if strings.TrimSpace(column.ConstructionID) == "" {
			return validationError("required", columnPath+".constructionId", "constructionId is required")
		}
		if constructionIDs[column.ConstructionID] {
			return validationError("duplicate_construction_id", columnPath+".constructionId", "constructionId must be unique")
		}
		constructionIDs[column.ConstructionID] = true
		if err := validateRecipeName(column.Name, columnPath+".name"); err != nil {
			return err
		}
		if outputs[column.Name] {
			return validationError("duplicate_derived_column", columnPath+".name", "derived column names must be unique")
		}
		outputs[column.Name] = true
		if strings.TrimSpace(column.Label) == "" {
			return validationError("required", columnPath+".label", "label is required")
		}
		switch column.Operation {
		case DerivedAdd, DerivedSubtract, DerivedMultiply:
			if !column.Operation.AcceptsDivisionByZeroPolicy(column.DivisionByZeroPolicy) {
				return validationError("invalid_division_by_zero_policy", columnPath+".divisionByZeroPolicy", "is only valid for DIVIDE")
			}
		case DerivedDivide:
			if !column.Operation.AcceptsDivisionByZeroPolicy(column.DivisionByZeroPolicy) {
				return validationError("invalid_division_by_zero_policy", columnPath+".divisionByZeroPolicy", "DIVIDE requires NULL or ERROR")
			}
		default:
			return validationError("invalid_derived_operation", columnPath+".operation", "must be ADD, SUBTRACT, MULTIPLY, or DIVIDE")
		}
		if !column.MissingInputPolicy.Valid() {
			return validationError("invalid_missing_input_policy", columnPath+".missingInputPolicy", "must be PROPAGATE_NULL or ERROR")
		}
		if err := validateDerivedOperand(column.Left, columnPath+".left"); err != nil {
			return err
		}
		if err := validateDerivedOperand(column.Right, columnPath+".right"); err != nil {
			return err
		}
	}
	return validateDerivedColumnCycles(columns, outputs, path)
}

func validateDerivedOperand(operand DerivedOperand, path string) error {
	switch operand.Kind {
	case DerivedColumnOperand:
		if !operand.Kind.ValidPayload(strings.TrimSpace(operand.Column), operand.Literal != nil) {
			return validationError("invalid_derived_operand", path, "COLUMN requires only a column name")
		}
		if err := validateRecipeName(operand.Column, path+".column"); err != nil {
			return err
		}
	case DerivedLiteralOperand:
		if !operand.Kind.ValidPayload(operand.Column, operand.Literal != nil) {
			return validationError("invalid_derived_operand", path, "LITERAL requires only a literal payload")
		}
		switch operand.Literal.Kind {
		case NumericInteger:
			if !operand.Literal.Valid() {
				return validationError("invalid_derived_literal", path+".literal", "INTEGER requires only an integer payload")
			}
		case NumericDecimal:
			if !operand.Literal.Valid() {
				return validationError("invalid_derived_literal", path+".literal", "DECIMAL requires one finite decimal payload")
			}
		default:
			return validationError("invalid_derived_literal", path+".literal.kind", "must be INTEGER or DECIMAL")
		}
	default:
		return validationError("invalid_derived_operand", path+".kind", "must be COLUMN or LITERAL")
	}
	return nil
}

func validateDerivedColumnCycles(columns []DerivedColumn, derivedOutputs map[string]bool, path string) error {
	definitions := make(map[string]DerivedColumn, len(columns))
	for _, column := range columns {
		definitions[column.Name] = column
	}
	state := make(map[string]uint8, len(columns))
	var visit func(string) error
	visit = func(name string) error {
		switch state[name] {
		case 1:
			return validationError("derived_column_cycle", path, fmt.Sprintf("derived column dependency cycle includes %q", name))
		case 2:
			return nil
		}
		state[name] = 1
		definition := definitions[name]
		for _, operand := range []DerivedOperand{definition.Left, definition.Right} {
			if operand.Kind == DerivedColumnOperand && derivedOutputs[operand.Column] {
				if err := visit(operand.Column); err != nil {
					return err
				}
			}
		}
		state[name] = 2
		return nil
	}
	for _, column := range columns {
		if err := visit(column.Name); err != nil {
			return err
		}
	}
	return nil
}
