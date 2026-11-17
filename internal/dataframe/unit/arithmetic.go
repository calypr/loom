package unit

import "fmt"

type ArithmeticOperation uint8

const (
	invalidArithmeticOperation ArithmeticOperation = iota
	ArithmeticAdd
	ArithmeticSubtract
	ArithmeticMultiply
	ArithmeticDivide
)

func (operation ArithmeticOperation) String() string {
	switch operation {
	case ArithmeticAdd:
		return "ADD"
	case ArithmeticSubtract:
		return "SUBTRACT"
	case ArithmeticMultiply:
		return "MULTIPLY"
	case ArithmeticDivide:
		return "DIVIDE"
	default:
		return "UNKNOWN"
	}
}

type arithmeticOperandKind uint8

const (
	invalidArithmeticOperand arithmeticOperandKind = iota
	columnArithmeticOperand
	literalArithmeticOperand
)

type ArithmeticOperand struct {
	kind arithmeticOperandKind
	unit *UnitIdentity
}

func ColumnArithmeticOperand(normalizedUnit *UnitIdentity) ArithmeticOperand {
	if normalizedUnit == nil {
		return ArithmeticOperand{kind: columnArithmeticOperand}
	}
	identity := *normalizedUnit
	return ArithmeticOperand{kind: columnArithmeticOperand, unit: &identity}
}

func LiteralArithmeticOperand() ArithmeticOperand {
	return ArithmeticOperand{kind: literalArithmeticOperand}
}

type ArithmeticUnitErrorReason string

const (
	ArithmeticUnitInvalidOperation ArithmeticUnitErrorReason = "INVALID_OPERATION"
	ArithmeticUnitInvalidOperand   ArithmeticUnitErrorReason = "INVALID_OPERAND"
	ArithmeticUnitInvalidIdentity  ArithmeticUnitErrorReason = "INVALID_IDENTITY"
	ArithmeticUnitIncompatible     ArithmeticUnitErrorReason = "INCOMPATIBLE_UNITS"
	ArithmeticUnitUnrepresentable  ArithmeticUnitErrorReason = "UNREPRESENTABLE_RESULT"
)

type ArithmeticUnitError struct {
	Operation ArithmeticOperation
	Reason    ArithmeticUnitErrorReason
}

func (e *ArithmeticUnitError) Error() string {
	return fmt.Sprintf("arithmetic operation %s unit resolution failed: %s", e.Operation.String(), e.Reason)
}

func ResolveArithmeticUnit(operation ArithmeticOperation, left, right ArithmeticOperand) (*UnitIdentity, error) {
	if !validArithmeticOperation(operation) {
		return nil, &ArithmeticUnitError{Operation: operation, Reason: ArithmeticUnitInvalidOperation}
	}
	leftKind, leftUnit, err := resolveArithmeticOperand(operation, left)
	if err != nil {
		return nil, err
	}
	rightKind, rightUnit, err := resolveArithmeticOperand(operation, right)
	if err != nil {
		return nil, err
	}
	if leftUnit == nil && rightUnit == nil {
		return nil, nil
	}
	switch operation {
	case ArithmeticAdd, ArithmeticSubtract:
		if leftUnit != nil && rightUnit != nil && leftUnit.Equal(*rightUnit) {
			return normalizedIdentity(leftUnit), nil
		}
		return nil, &ArithmeticUnitError{Operation: operation, Reason: ArithmeticUnitIncompatible}
	case ArithmeticMultiply:
		if leftUnit == nil && rightUnit != nil && leftKind == literalArithmeticOperand && rightKind == columnArithmeticOperand {
			return normalizedIdentity(rightUnit), nil
		}
		if leftUnit != nil && rightUnit == nil && leftKind == columnArithmeticOperand && rightKind == literalArithmeticOperand {
			return normalizedIdentity(leftUnit), nil
		}
		return nil, &ArithmeticUnitError{Operation: operation, Reason: ArithmeticUnitUnrepresentable}
	case ArithmeticDivide:
		if leftUnit != nil && rightUnit == nil && leftKind == columnArithmeticOperand && rightKind == literalArithmeticOperand {
			return normalizedIdentity(leftUnit), nil
		}
		return nil, &ArithmeticUnitError{Operation: operation, Reason: ArithmeticUnitUnrepresentable}
	default:
		return nil, &ArithmeticUnitError{Operation: operation, Reason: ArithmeticUnitInvalidOperation}
	}
}

func resolveArithmeticOperand(operation ArithmeticOperation, operand ArithmeticOperand) (arithmeticOperandKind, *UnitIdentity, *ArithmeticUnitError) {
	switch operand.kind {
	case columnArithmeticOperand:
		if operand.unit == nil {
			return operand.kind, nil, nil
		}
		if !operand.unit.Valid() {
			return invalidArithmeticOperand, nil, &ArithmeticUnitError{Operation: operation, Reason: ArithmeticUnitInvalidIdentity}
		}
		return operand.kind, normalizedIdentity(operand.unit), nil
	case literalArithmeticOperand:
		return operand.kind, nil, nil
	default:
		return invalidArithmeticOperand, nil, &ArithmeticUnitError{Operation: operation, Reason: ArithmeticUnitInvalidOperand}
	}
}

func validArithmeticOperation(operation ArithmeticOperation) bool {
	switch operation {
	case ArithmeticAdd, ArithmeticSubtract, ArithmeticMultiply, ArithmeticDivide:
		return true
	default:
		return false
	}
}

func normalizedIdentity(identity *UnitIdentity) *UnitIdentity {
	if identity == nil {
		return nil
	}
	normalized := identity.normalized()
	return &normalized
}
