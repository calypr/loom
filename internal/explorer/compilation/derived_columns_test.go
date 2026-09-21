package compilation

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestCompileMapsAuthoredDerivedColumnsIntoRecipeContract(t *testing.T) {
	integer := int64(0)
	decimal := float64(2.75)
	document := exactRecodeDocument(nil)
	document.TableShape = &authoringv2.TableShape{Derived: []authoringv2.DerivedConstruction{
		{
			ConstructionID: "calc_scaled_status", Output: authoringv2.ColumnOutput{Column: "scaled_status", Label: "Scaled status"},
			Operation: "MULTIPLY", Left: authoringv2.ArithmeticOperand{Kind: "COLUMN", Column: "status"},
			Right:              authoringv2.ArithmeticOperand{Kind: "LITERAL", Literal: &authoringv2.TableScalar{Kind: "DECIMAL", Decimal: &decimal}},
			MissingInputPolicy: "PROPAGATE_NULL",
		},
		{
			ConstructionID: "calc_present", Output: authoringv2.ColumnOutput{Column: "present", Label: "Present"},
			Operation: "DIVIDE", Left: authoringv2.ArithmeticOperand{Kind: "LITERAL", Literal: &authoringv2.TableScalar{Kind: "INTEGER", Integer: &integer}},
			Right:              authoringv2.ArithmeticOperand{Kind: "COLUMN", Column: "scaled_status"},
			MissingInputPolicy: "ERROR", DivisionByZeroPolicy: "NULL",
		},
	}}
	compiled, err := Compile(context.Background(), "project", "explorer", document, fixtureSnapshotForProject("project"))
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.Bundle.Outputs) != 1 {
		t.Fatalf("compiled outputs = %#v", compiled.Bundle.Outputs)
	}
	want := []recipe.DerivedColumn{
		{
			ConstructionID: "calc_scaled_status", Name: "scaled_status", Label: "Scaled status", Operation: recipe.DerivedMultiply,
			Left:               recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "status"},
			Right:              recipe.DerivedOperand{Kind: recipe.DerivedLiteralOperand, Literal: &recipe.DerivedLiteral{Kind: recipe.NumericDecimal, Decimal: &decimal}},
			MissingInputPolicy: recipe.MissingInputPropagateNull,
		},
		{
			ConstructionID: "calc_present", Name: "present", Label: "Present", Operation: recipe.DerivedDivide,
			Left:               recipe.DerivedOperand{Kind: recipe.DerivedLiteralOperand, Literal: &recipe.DerivedLiteral{Kind: recipe.NumericInteger, Integer: &integer}},
			Right:              recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "scaled_status"},
			MissingInputPolicy: recipe.MissingInputError, DivisionByZeroPolicy: recipe.DivisionByZeroNull,
		},
	}
	if got := compiled.Bundle.Outputs[0].DerivedColumns; !reflect.DeepEqual(got, want) {
		t.Fatalf("recipe derived contract = %#v, want %#v", got, want)
	}
}

func TestCompileRejectsNonNumericDerivedLiteralAtRecipeBoundary(t *testing.T) {
	text := "group"
	document := exactRecodeDocument(nil)
	document.TableShape = &authoringv2.TableShape{Derived: []authoringv2.DerivedConstruction{{
		ConstructionID: "calc_invalid", Output: authoringv2.ColumnOutput{Column: "invalid", Label: "Invalid"},
		Operation: "ADD", Left: authoringv2.ArithmeticOperand{Kind: "LITERAL", Literal: &authoringv2.TableScalar{Kind: "STRING", String: &text}},
		Right:              authoringv2.ArithmeticOperand{Kind: "LITERAL", Literal: &authoringv2.TableScalar{Kind: "INTEGER", Integer: int64Pointer(1)}},
		MissingInputPolicy: "ERROR",
	}}}
	_, err := Compile(context.Background(), "project", "explorer", document, fixtureSnapshotForProject("project"))
	var compileError *Error
	if !errors.As(err, &compileError) || compileError.Code != "INVALID_DERIVED_COLUMN" || !strings.Contains(compileError.Message, "INTEGER or DECIMAL") {
		t.Fatalf("compile error = %v, want INVALID_DERIVED_COLUMN for a string arithmetic literal", err)
	}
}

func int64Pointer(value int64) *int64 { return &value }
