package compilation

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func recipeDerivedColumns(shape *authoringv2.TableShape) ([]recipe.DerivedColumn, error) {
	if shape == nil || len(shape.Derived) == 0 {
		return nil, nil
	}
	columns := make([]recipe.DerivedColumn, 0, len(shape.Derived))
	for index, authored := range shape.Derived {
		left, err := recipeDerivedOperand(authored.Left)
		if err != nil {
			return nil, fmt.Errorf("derived[%d].left: %w", index, err)
		}
		right, err := recipeDerivedOperand(authored.Right)
		if err != nil {
			return nil, fmt.Errorf("derived[%d].right: %w", index, err)
		}
		columns = append(columns, recipe.DerivedColumn{
			ConstructionID:       string(authored.ConstructionID),
			Name:                 authored.Output.Column,
			Label:                authored.Output.Label,
			Operation:            recipe.DerivedOperation(authored.Operation),
			Left:                 left,
			Right:                right,
			MissingInputPolicy:   recipe.MissingInputPolicy(authored.MissingInputPolicy),
			DivisionByZeroPolicy: recipe.DivisionByZeroPolicy(authored.DivisionByZeroPolicy),
		})
	}
	return columns, nil
}

func recipeDerivedOperand(operand authoringv2.ArithmeticOperand) (recipe.DerivedOperand, error) {
	switch operand.Kind {
	case "COLUMN":
		return recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: operand.Column}, nil
	case "LITERAL":
		if operand.Literal == nil {
			return recipe.DerivedOperand{}, fmt.Errorf("literal payload is required")
		}
		literal := &recipe.DerivedLiteral{}
		switch operand.Literal.Kind {
		case "INTEGER":
			literal.Kind = recipe.NumericInteger
			literal.Integer = operand.Literal.Integer
		case "DECIMAL":
			literal.Kind = recipe.NumericDecimal
			literal.Decimal = operand.Literal.Decimal
		default:
			return recipe.DerivedOperand{}, fmt.Errorf("arithmetic literal must be INTEGER or DECIMAL")
		}
		return recipe.DerivedOperand{Kind: recipe.DerivedLiteralOperand, Literal: literal}, nil
	default:
		return recipe.DerivedOperand{}, fmt.Errorf("operand kind must be COLUMN or LITERAL")
	}
}
