package unit

import (
	"errors"
	"testing"
)

func TestResolveArithmeticUnitPreservesSupportedResultIdentity(t *testing.T) {
	length := UnitIdentity{System: "http://unitsofmeasure.org", Code: "cm"}
	for _, test := range []struct {
		name      string
		operation ArithmeticOperation
		left      ArithmeticOperand
		right     ArithmeticOperand
		want      *UnitIdentity
	}{
		{"unitless add", ArithmeticAdd, ColumnArithmeticOperand(nil), LiteralArithmeticOperand(), nil},
		{"unitless subtract", ArithmeticSubtract, LiteralArithmeticOperand(), ColumnArithmeticOperand(nil), nil},
		{"same unit add", ArithmeticAdd, ColumnArithmeticOperand(&length), ColumnArithmeticOperand(&length), &length},
		{"same unit subtract", ArithmeticSubtract, ColumnArithmeticOperand(&length), ColumnArithmeticOperand(&length), &length},
		{"unit-bearing column times literal", ArithmeticMultiply, ColumnArithmeticOperand(&length), LiteralArithmeticOperand(), &length},
		{"literal times unit-bearing column", ArithmeticMultiply, LiteralArithmeticOperand(), ColumnArithmeticOperand(&length), &length},
		{"unitless multiply", ArithmeticMultiply, ColumnArithmeticOperand(nil), LiteralArithmeticOperand(), nil},
		{"unit-bearing column divided by literal", ArithmeticDivide, ColumnArithmeticOperand(&length), LiteralArithmeticOperand(), &length},
		{"unitless divide", ArithmeticDivide, LiteralArithmeticOperand(), ColumnArithmeticOperand(nil), nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := ResolveArithmeticUnit(test.operation, test.left, test.right)
			if err != nil {
				t.Fatal(err)
			}
			if !sameUnitIdentity(got, test.want) {
				t.Fatalf("result unit = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestResolveArithmeticUnitNormalizesReturnedIdentity(t *testing.T) {
	left := UnitIdentity{System: " http://unitsofmeasure.org ", Code: " cm "}
	right := UnitIdentity{System: "http://unitsofmeasure.org", Code: "cm"}
	got, err := ResolveArithmeticUnit(ArithmeticAdd, ColumnArithmeticOperand(&left), ColumnArithmeticOperand(&right))
	if err != nil {
		t.Fatal(err)
	}
	want := UnitIdentity{System: "http://unitsofmeasure.org", Code: "cm"}
	if !sameUnitIdentity(got, &want) {
		t.Fatalf("normalized result unit = %#v, want %#v", got, want)
	}
}

func TestResolveArithmeticUnitRejectsUnrepresentableOrIncompatibleResults(t *testing.T) {
	length := UnitIdentity{System: "http://unitsofmeasure.org", Code: "cm"}
	mass := UnitIdentity{System: "http://unitsofmeasure.org", Code: "kg"}
	for _, test := range []struct {
		name      string
		operation ArithmeticOperation
		left      ArithmeticOperand
		right     ArithmeticOperand
		reason    ArithmeticUnitErrorReason
	}{
		{"add different identities", ArithmeticAdd, ColumnArithmeticOperand(&length), ColumnArithmeticOperand(&mass), ArithmeticUnitIncompatible},
		{"add unit and unitless", ArithmeticAdd, ColumnArithmeticOperand(&length), ColumnArithmeticOperand(nil), ArithmeticUnitIncompatible},
		{"subtract different identities", ArithmeticSubtract, ColumnArithmeticOperand(&length), ColumnArithmeticOperand(&mass), ArithmeticUnitIncompatible},
		{"multiply unit-bearing columns", ArithmeticMultiply, ColumnArithmeticOperand(&length), ColumnArithmeticOperand(&length), ArithmeticUnitUnrepresentable},
		{"multiply unit-bearing and unitless column", ArithmeticMultiply, ColumnArithmeticOperand(&length), ColumnArithmeticOperand(nil), ArithmeticUnitUnrepresentable},
		{"divide unit-bearing columns", ArithmeticDivide, ColumnArithmeticOperand(&length), ColumnArithmeticOperand(&length), ArithmeticUnitUnrepresentable},
		{"divide by unitless column", ArithmeticDivide, ColumnArithmeticOperand(&length), ColumnArithmeticOperand(nil), ArithmeticUnitUnrepresentable},
		{"unitless divided by unit-bearing column", ArithmeticDivide, LiteralArithmeticOperand(), ColumnArithmeticOperand(&length), ArithmeticUnitUnrepresentable},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := ResolveArithmeticUnit(test.operation, test.left, test.right)
			if err == nil || got != nil {
				t.Fatalf("unit resolution = (%#v, %v), want a typed rejection", got, err)
			}
			var unitErr *ArithmeticUnitError
			if !errors.As(err, &unitErr) || unitErr.Reason != test.reason {
				t.Fatalf("error = %v, want ArithmeticUnitError reason %s", err, test.reason)
			}
		})
	}
}

func TestResolveArithmeticUnitRejectsInvalidOperationAndIdentity(t *testing.T) {
	unitValue := UnitIdentity{Code: "cm"}
	for _, test := range []struct {
		name      string
		operation ArithmeticOperation
		operand   ArithmeticOperand
		reason    ArithmeticUnitErrorReason
	}{
		{"invalid operation", ArithmeticOperation(255), LiteralArithmeticOperand(), ArithmeticUnitInvalidOperation},
		{"invalid identity", ArithmeticAdd, ColumnArithmeticOperand(&unitValue), ArithmeticUnitInvalidIdentity},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ResolveArithmeticUnit(test.operation, test.operand, LiteralArithmeticOperand())
			var unitErr *ArithmeticUnitError
			if !errors.As(err, &unitErr) || unitErr.Reason != test.reason {
				t.Fatalf("error = %v, want ArithmeticUnitError reason %s", err, test.reason)
			}
		})
	}
}

func sameUnitIdentity(left, right *UnitIdentity) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}
