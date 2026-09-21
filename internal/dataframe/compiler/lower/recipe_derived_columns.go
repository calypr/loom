package lower

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/unit"
)

type derivedColumnMetadata struct {
	Type           expression.Type
	NormalizedUnit *unit.UnitIdentity
}

type derivedOperandValue struct {
	Expression ir.PhysicalExpression
	Type       expression.Type
	Unit       unit.ArithmeticOperand
}

func appendRecipeDerivedColumns(plan *ir.PhysicalPlan, columns []recipe.DerivedColumn, baseSchema []CompiledOutputColumn) (map[string]derivedColumnMetadata, error) {
	if len(columns) == 0 {
		return nil, nil
	}
	returnIndex := -1
	for index := range plan.Operations {
		if plan.Operations[index].Kind == ir.PhysicalReturnOp && plan.Operations[index].Return != nil {
			returnIndex = index
			break
		}
	}
	if returnIndex < 0 {
		return nil, fmt.Errorf("canonical plan has no RETURN operation for derived columns")
	}

	projectionIndexes := make(map[string]int, len(plan.Operations[returnIndex].Return.Projections))
	for index, projection := range plan.Operations[returnIndex].Return.Projections {
		projectionIndexes[projection.Name] = index
	}
	baseTypes := make(map[string]CompiledOutputColumn, len(baseSchema))
	for _, column := range baseSchema {
		baseTypes[column.Name] = column
	}
	derivedByName := make(map[string]recipe.DerivedColumn, len(columns))
	for _, column := range columns {
		if _, exists := baseTypes[column.Name]; exists {
			return nil, fmt.Errorf("derived output %q collides with an existing output column", column.Name)
		}
		if _, exists := derivedByName[column.Name]; exists {
			return nil, fmt.Errorf("duplicate derived output %q", column.Name)
		}
		derivedByName[column.Name] = column
	}

	usedVariables := physicalPlanVariables(plan.Operations)
	variableIndex := 0
	allocateVariable := func() string {
		for {
			candidate := fmt.Sprintf("__loom_derived_%d", variableIndex)
			variableIndex++
			if !usedVariables[candidate] {
				usedVariables[candidate] = true
				return candidate
			}
		}
	}
	baseVariables := make(map[string]string)
	derivedVariables := make(map[string]string, len(columns))
	derivedTypes := make(map[string]derivedColumnMetadata, len(columns))
	state := make(map[string]uint8, len(columns))
	lets := make([]ir.PhysicalOperation, 0, len(columns)*2)

	var compileDerived func(string) error
	var compileOperand func(recipe.DerivedOperand, string) (derivedOperandValue, error)
	compileOperand = func(operand recipe.DerivedOperand, owner string) (derivedOperandValue, error) {
		switch operand.Kind {
		case recipe.DerivedColumnOperand:
			if derived, exists := derivedByName[operand.Column]; exists {
				if err := compileDerived(derived.Name); err != nil {
					return derivedOperandValue{}, err
				}
				metadata := derivedTypes[derived.Name]
				return derivedOperandValue{
					Expression: derivedVariableExpression(derivedVariables[derived.Name]),
					Type:       metadata.Type,
					Unit:       unit.ColumnArithmeticOperand(metadata.NormalizedUnit),
				}, nil
			}
			metadata, exists := baseTypes[operand.Column]
			if !exists || metadata.Internal {
				return derivedOperandValue{}, fmt.Errorf("derived column %q references unknown public output column %q", owner, operand.Column)
			}
			if metadata.Cardinality == string(expression.Many) {
				return derivedOperandValue{}, fmt.Errorf("derived column %q input %q is repeated", owner, operand.Column)
			}
			kind := expression.ValueKind(metadata.Kind)
			if kind != expression.KindInteger && kind != expression.KindDecimal {
				return derivedOperandValue{}, fmt.Errorf("derived column %q input %q must be integer or decimal, got %s", owner, operand.Column, kind)
			}
			cardinality := expression.Cardinality(metadata.Cardinality)
			if cardinality != expression.RequiredOne && cardinality != expression.OptionalOne {
				return derivedOperandValue{}, fmt.Errorf("derived column %q input %q has unsupported cardinality %q", owner, operand.Column, metadata.Cardinality)
			}
			variable, exists := baseVariables[operand.Column]
			if !exists {
				projectionIndex, found := projectionIndexes[operand.Column]
				if !found {
					return derivedOperandValue{}, fmt.Errorf("derived column %q input %q has no physical projection", owner, operand.Column)
				}
				projection := plan.Operations[returnIndex].Return.Projections[projectionIndex]
				if projection.Hidden {
					return derivedOperandValue{}, fmt.Errorf("derived column %q input %q is hidden", owner, operand.Column)
				}
				input := physicalProjectionExpression(projection)
				variable = allocateVariable()
				baseVariables[operand.Column] = variable
				lets = append(lets, recipeDerivedLet(variable, input))
			}
			return derivedOperandValue{
				Expression: derivedVariableExpression(variable),
				Type:       expression.Type{Kind: kind, Cardinality: cardinality},
				Unit:       unit.ColumnArithmeticOperand(metadata.NormalizedUnit),
			}, nil
		case recipe.DerivedLiteralOperand:
			if operand.Literal == nil {
				return derivedOperandValue{}, fmt.Errorf("derived column %q has no literal payload", owner)
			}
			value, typ, err := derivedLiteralExpression(plan.BindVars, *operand.Literal)
			if err != nil {
				return derivedOperandValue{}, err
			}
			return derivedOperandValue{Expression: value, Type: typ, Unit: unit.LiteralArithmeticOperand()}, nil
		default:
			return derivedOperandValue{}, fmt.Errorf("derived column %q has unsupported operand kind %q", owner, operand.Kind)
		}
	}
	compileDerived = func(name string) error {
		switch state[name] {
		case 1:
			return fmt.Errorf("derived column dependency cycle includes %q", name)
		case 2:
			return nil
		}
		column, exists := derivedByName[name]
		if !exists {
			return fmt.Errorf("unknown derived column %q", name)
		}
		state[name] = 1
		left, err := compileOperand(column.Left, name)
		if err != nil {
			return fmt.Errorf("derived column %q left: %w", name, err)
		}
		right, err := compileOperand(column.Right, name)
		if err != nil {
			return fmt.Errorf("derived column %q right: %w", name, err)
		}
		if left.Type.Kind != expression.KindInteger && left.Type.Kind != expression.KindDecimal {
			return fmt.Errorf("derived column %q left operand must be numeric, got %s", name, left.Type.Kind)
		}
		if right.Type.Kind != expression.KindInteger && right.Type.Kind != expression.KindDecimal {
			return fmt.Errorf("derived column %q right operand must be numeric, got %s", name, right.Type.Kind)
		}
		resultUnit, err := unit.ResolveArithmeticUnit(derivedArithmeticOperation(column.Operation), left.Unit, right.Unit)
		if err != nil {
			return fmt.Errorf("derived column %q unit compatibility: %w", name, err)
		}
		resultKind := expression.KindDecimal
		if column.Operation != recipe.DerivedDivide && left.Type.Kind == expression.KindInteger && right.Type.Kind == expression.KindInteger {
			resultKind = expression.KindInteger
		}
		cardinality := expression.RequiredOne
		if column.MissingInputPolicy == recipe.MissingInputPropagateNull || column.Operation == recipe.DerivedDivide && column.DivisionByZeroPolicy == recipe.DivisionByZeroNull {
			cardinality = expression.OptionalOne
		}
		value := derivedArithmeticExpression(plan.BindVars, column, left.Expression, right.Expression)
		variable := allocateVariable()
		lets = append(lets, recipeDerivedLet(variable, value))
		derivedVariables[name] = variable
		derivedTypes[name] = derivedColumnMetadata{Type: expression.Type{Kind: resultKind, Cardinality: cardinality}, NormalizedUnit: resultUnit}
		state[name] = 2
		return nil
	}

	for _, column := range columns {
		if err := compileDerived(column.Name); err != nil {
			return nil, err
		}
	}
	for _, column := range columns {
		variable := derivedVariables[column.Name]
		plan.Operations[returnIndex].Return.Projections = append(plan.Operations[returnIndex].Return.Projections, ir.PhysicalProjection{
			Name: column.Name, Expression: pointerToPhysicalExpression(derivedVariableExpression(variable)),
		})
	}
	operations := make([]ir.PhysicalOperation, 0, len(plan.Operations)+len(lets))
	operations = append(operations, plan.Operations[:returnIndex]...)
	operations = append(operations, lets...)
	operations = append(operations, plan.Operations[returnIndex:]...)
	plan.Operations = operations
	return derivedTypes, nil
}

func derivedArithmeticOperation(operation recipe.DerivedOperation) unit.ArithmeticOperation {
	switch operation {
	case recipe.DerivedAdd:
		return unit.ArithmeticAdd
	case recipe.DerivedSubtract:
		return unit.ArithmeticSubtract
	case recipe.DerivedMultiply:
		return unit.ArithmeticMultiply
	case recipe.DerivedDivide:
		return unit.ArithmeticDivide
	default:
		return 0
	}
}

func physicalProjectionExpression(projection ir.PhysicalProjection) ir.PhysicalExpression {
	if projection.Expression != nil {
		return *projection.Expression
	}
	return ir.PhysicalExpression{
		Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull, Value: &projection.Value,
	}
}

func recipeDerivedLet(variable string, value ir.PhysicalExpression) ir.PhysicalOperation {
	return ir.PhysicalOperation{
		Kind:          ir.PhysicalExpressionLetOp,
		Source:        ir.PhysicalSource{SemanticField: "derived_column"},
		ExpressionLet: &ir.PhysicalExpressionLet{Variable: variable, Expression: value},
	}
}

func derivedVariableExpression(variable string) ir.PhysicalExpression {
	return ir.PhysicalExpression{
		Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{Variable: variable},
	}
}

func derivedLiteralExpression(bindVars map[string]any, literal recipe.DerivedLiteral) (ir.PhysicalExpression, expression.Type, error) {
	var value any
	kind := expression.KindInteger
	switch literal.Kind {
	case recipe.NumericInteger:
		if literal.Integer == nil || literal.Decimal != nil {
			return ir.PhysicalExpression{}, expression.Type{}, fmt.Errorf("INTEGER literal requires only an integer value")
		}
		value = *literal.Integer
	case recipe.NumericDecimal:
		if literal.Decimal == nil || literal.Integer != nil {
			return ir.PhysicalExpression{}, expression.Type{}, fmt.Errorf("DECIMAL literal requires only a decimal value")
		}
		value = *literal.Decimal
		kind = expression.KindDecimal
	default:
		return ir.PhysicalExpression{}, expression.Type{}, fmt.Errorf("unsupported derived literal kind %q", literal.Kind)
	}
	key := "derived_literal"
	for suffix := 0; ; suffix++ {
		candidate := fmt.Sprintf("%s_%d", key, suffix)
		if _, exists := bindVars[candidate]; exists {
			continue
		}
		bindVars[candidate] = value
		return ir.PhysicalExpression{
			Kind: ir.PhysicalLiteralExpression, Cardinality: ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalPreserveNull, Literal: &ir.PhysicalLiteral{BindKey: candidate},
		}, expression.Type{Kind: kind, Cardinality: expression.RequiredOne}, nil
	}
}

func derivedArithmeticExpression(bindVars map[string]any, column recipe.DerivedColumn, left, right ir.PhysicalExpression) ir.PhysicalExpression {
	arguments := make([]ir.PhysicalExpression, 0, 7)
	zero := derivedRawLiteralExpression(bindVars, "derived_null", nil)
	missingError := derivedRawLiteralExpression(bindVars, "derived_error", "DERIVED_MISSING_INPUT")
	appendMissing := func(value ir.PhysicalExpression) {
		condition := derivedPhysicalCall("eq", value, zero)
		if column.MissingInputPolicy == recipe.MissingInputPropagateNull {
			return
		}
		arguments = append(arguments, condition, derivedPhysicalCall("assert", derivedRawLiteralExpression(bindVars, "derived_false", false), missingError))
	}
	if column.MissingInputPolicy == recipe.MissingInputPropagateNull {
		arguments = append(arguments,
			derivedPhysicalCall("or", derivedPhysicalCall("eq", left, zero), derivedPhysicalCall("eq", right, zero)),
			zero,
		)
	} else {
		appendMissing(left)
		appendMissing(right)
	}
	if column.Operation == recipe.DerivedDivide {
		zeroValue := derivedLiteralInt64Expression(bindVars, 0)
		condition := derivedPhysicalCall("eq", right, zeroValue)
		if column.DivisionByZeroPolicy == recipe.DivisionByZeroNull {
			arguments = append(arguments, condition, zero)
		} else {
			arguments = append(arguments, condition, derivedPhysicalCall("assert", derivedRawLiteralExpression(bindVars, "derived_false", false), derivedRawLiteralExpression(bindVars, "derived_error", "DERIVED_DIVISION_BY_ZERO")))
		}
	}
	operator := map[recipe.DerivedOperation]string{
		recipe.DerivedAdd:      "add",
		recipe.DerivedSubtract: "subtract",
		recipe.DerivedMultiply: "multiply",
		recipe.DerivedDivide:   "divide",
	}[column.Operation]
	arguments = append(arguments, derivedPhysicalCall(operator, left, right))
	return derivedPhysicalCall("case", arguments...)
}

func derivedPhysicalCall(name string, arguments ...ir.PhysicalExpression) ir.PhysicalExpression {
	return ir.PhysicalExpression{
		Kind: ir.PhysicalCallExpression, Cardinality: ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull, Call: &ir.PhysicalCall{Name: name, Args: arguments},
	}
}

func derivedRawLiteralExpression(bindVars map[string]any, prefix string, value any) ir.PhysicalExpression {
	for suffix := 0; ; suffix++ {
		key := fmt.Sprintf("%s_%d", prefix, suffix)
		if _, exists := bindVars[key]; exists {
			continue
		}
		bindVars[key] = value
		return ir.PhysicalExpression{
			Kind: ir.PhysicalLiteralExpression, Cardinality: ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalPreserveNull, Literal: &ir.PhysicalLiteral{BindKey: key},
		}
	}
}

func derivedLiteralInt64Expression(bindVars map[string]any, value int64) ir.PhysicalExpression {
	return derivedRawLiteralExpression(bindVars, "derived_zero", value)
}

func pointerToPhysicalExpression(value ir.PhysicalExpression) *ir.PhysicalExpression {
	return &value
}

func physicalPlanVariables(operations []ir.PhysicalOperation) map[string]bool {
	variables := map[string]bool{}
	add := func(name string) {
		if name != "" {
			variables[name] = true
		}
	}
	for _, operation := range operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			add(operation.RootScan.Variable)
		case ir.PhysicalTraversalOp:
			add(operation.Traversal.TargetVariable)
			add(operation.Traversal.EdgeVariable)
		case ir.PhysicalExpressionLetOp:
			add(operation.ExpressionLet.Variable)
		case ir.PhysicalDerivedLetOp:
			add(operation.DerivedLet.Variable)
		case ir.PhysicalSetOp:
			add(operation.Set.Variable)
			if operation.Set.Reduction != nil {
				add(operation.Set.Reduction.Variable)
			}
			if operation.Set.Prepared != nil {
				add(operation.Set.Prepared.Variable)
			}
			add(operation.Set.ItemVariable)
		case ir.PhysicalUnnestOp:
			add(operation.Unnest.OutputVariable)
			add(operation.Unnest.Ordinality)
			add(operation.Unnest.HasItemVariable)
			for _, step := range operation.Unnest.Owner.Route {
				add(step.Traversal.TargetVariable)
				add(step.Traversal.EdgeVariable)
			}
		case ir.PhysicalCollectionScanOp:
			add(operation.CollectionScan.Variable)
		case ir.PhysicalGroupedPivotOp:
			add(operation.GroupedPivot.InputRowVariable)
			add(operation.GroupedPivot.GroupRowsVariable)
			add(operation.GroupedPivot.OutputRowVariable)
			for _, key := range operation.GroupedPivot.GroupKeys {
				add(key.Variable)
			}
		case ir.PhysicalUnpivotOp:
			add(operation.Unpivot.InputRowVariable)
			add(operation.Unpivot.SlotVariable)
			add(operation.Unpivot.OutputRowVariable)
		case ir.PhysicalPathSeedOp:
			add(operation.PathSeed.Variable)
			add(operation.PathSeed.Node.Alias)
		case ir.PhysicalPathExtendOp:
			add(operation.PathExtend.Variable)
			add(operation.PathExtend.Traversal.TargetVariable)
			add(operation.PathExtend.Traversal.EdgeVariable)
			add(operation.PathExtend.Node.Alias)
		}
	}
	return variables
}
