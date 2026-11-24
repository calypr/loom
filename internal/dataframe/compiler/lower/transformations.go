package lower

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/columntransform"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func appendRecipeColumnTransformations(plan *ir.PhysicalPlan, transformations []recipe.ColumnTransformation) error {
	if len(transformations) == 0 {
		return nil
	}
	returnIndex := -1
	for index := range plan.Operations {
		if plan.Operations[index].Kind == ir.PhysicalReturnOp && plan.Operations[index].Return != nil {
			returnIndex = index
			break
		}
	}
	if returnIndex < 0 {
		return fmt.Errorf("canonical plan has no RETURN operation for column transformations")
	}
	projections := make(map[string]int, len(plan.Operations[returnIndex].Return.Projections))
	for index, projection := range plan.Operations[returnIndex].Return.Projections {
		projections[projection.Name] = index
	}
	for _, transformation := range transformations {
		index, found := projections[transformation.Column]
		if !found {
			return fmt.Errorf("column transformation target %q is not a public output column", transformation.Column)
		}
		projection := &plan.Operations[returnIndex].Return.Projections[index]
		if projection.Hidden {
			return fmt.Errorf("column transformation target %q is hidden", transformation.Column)
		}
		var source ir.PhysicalExpression
		if projection.Expression != nil {
			source = *projection.Expression
		} else {
			source = ir.PhysicalExpression{
				Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
				NullBehavior: ir.PhysicalPreserveNull, Value: &projection.Value,
			}
		}
		if source.Cardinality != ir.PhysicalScalarCardinality {
			return fmt.Errorf("column transformation target %q is not scalar", transformation.Column)
		}
		value, err := lowerValueTransformation(source, transformation.Transformation, plan.BindVars)
		if err != nil {
			return fmt.Errorf("column transformation target %q: %w", transformation.Column, err)
		}
		projection.Value = ir.PhysicalValue{}
		projection.Expression = &value
	}
	return nil
}

func lowerValueTransformation(source ir.PhysicalExpression, transformation columntransform.ValueTransformation, bindVars map[string]any) (ir.PhysicalExpression, error) {
	if err := transformation.Validate(); err != nil {
		return ir.PhysicalExpression{}, err
	}
	switch transformation.Kind {
	case columntransform.KindExactCategoryRecode:
		recode := transformation.ExactCategoryRecode
		args := []ir.PhysicalExpression{
			physicalCall("eq", source, physicalLiteral(bindVars, nil)),
			physicalLiteral(bindVars, nil),
		}
		for _, mapping := range recode.Mappings {
			args = append(args,
				physicalCall("eq", source, physicalLiteral(bindVars, mapping.From)),
				physicalLiteral(bindVars, mapping.To),
			)
		}
		if recode.UnknownPolicy == columntransform.UnknownKeepOriginal {
			args = append(args, source)
		} else {
			args = append(args, physicalCall("assert",
				physicalLiteral(bindVars, false), physicalLiteral(bindVars, "CATEGORY_RECODE_UNKNOWN_VALUE")))
		}
		return physicalCall("case", args...), nil
	default:
		return ir.PhysicalExpression{}, fmt.Errorf("unsupported column transformation kind %q", transformation.Kind)
	}
}

func physicalCall(name string, args ...ir.PhysicalExpression) ir.PhysicalExpression {
	return ir.PhysicalExpression{
		Kind: ir.PhysicalCallExpression, Cardinality: ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull, Call: &ir.PhysicalCall{Name: name, Args: args},
	}
}

func physicalLiteral(bindVars map[string]any, value any) ir.PhysicalExpression {
	key := "category_recode_value"
	for suffix := 0; ; suffix++ {
		candidate := fmt.Sprintf("%s_%d", key, suffix)
		if _, exists := bindVars[candidate]; exists {
			continue
		}
		bindVars[candidate] = value
		return ir.PhysicalExpression{
			Kind: ir.PhysicalLiteralExpression, Cardinality: ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalPreserveNull, Literal: &ir.PhysicalLiteral{BindKey: candidate},
		}
	}
}
