package lower

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func lowerConstructionGroupAggregates(group recipe.ConstructionGroup, input map[string]CompiledOutputColumn, outputs map[string]recipe.StageColumn, usedVariables map[string]bool, stepIndex int) ([]ir.PhysicalStageGroupAggregate, map[string]CompiledOutputColumn, map[string]ir.PhysicalProjection, error) {
	aggregates := make([]ir.PhysicalStageGroupAggregate, 0, len(group.Aggregates))
	metadata := make(map[string]CompiledOutputColumn, len(group.Aggregates))
	projections := make(map[string]ir.PhysicalProjection, len(group.Aggregates))
	for _, aggregate := range group.Aggregates {
		output, ok := outputs[aggregate.OutputColumnID]
		if !ok {
			return nil, nil, nil, fmt.Errorf("group aggregate output column ID %q is missing from step schema", aggregate.OutputColumnID)
		}
		physicalAggregate := ir.PhysicalStageGroupAggregate{
			Operation: string(aggregate.Operation), Output: output.Name,
			Variable: allocateConstructionVariable(usedVariables, "group_aggregate", stepIndex),
		}
		column := CompiledOutputColumn{
			ID: output.ID, Name: output.Name, Label: constructionFirstNonEmpty(output.Label, output.Name),
			SemanticPath: "construction_group:" + group.ConstructionID,
			Cardinality:  string(expression.RequiredOne),
		}
		switch aggregate.Operation {
		case recipe.ConstructionGroupCountRows:
			column.Kind = string(expression.KindInteger)
		case recipe.ConstructionGroupCountNonNull, recipe.ConstructionGroupCountDistinct,
			recipe.ConstructionGroupSum, recipe.ConstructionGroupMin, recipe.ConstructionGroupMax, recipe.ConstructionGroupMean:
			inputColumn, ok := input[aggregate.InputColumnID]
			if !ok || inputColumn.Internal {
				return nil, nil, nil, fmt.Errorf("group aggregate input column ID %q is not public", aggregate.InputColumnID)
			}
			inputKind, scalar := TableReshapeScalarKind(inputColumn.Kind)
			if !scalar || inputColumn.Cardinality == string(expression.Many) {
				return nil, nil, nil, fmt.Errorf("group aggregate input column %q must be a public scalar column", inputColumn.Name)
			}
			physicalAggregate.InputColumn, physicalAggregate.InputKind = inputColumn.Name, inputKind
			switch aggregate.Operation {
			case recipe.ConstructionGroupCountNonNull, recipe.ConstructionGroupCountDistinct:
				column.Kind = string(expression.KindInteger)
			case recipe.ConstructionGroupMin, recipe.ConstructionGroupMax:
				column.Kind = inputColumn.Kind
				column.Nullable = true
				column.NormalizedUnit = cloneUnitIdentity(inputColumn.NormalizedUnit)
			case recipe.ConstructionGroupSum:
				if inputKind != string(recipe.TableScalarInteger) && inputKind != string(recipe.TableScalarDecimal) {
					return nil, nil, nil, fmt.Errorf("group aggregate %s requires a numeric input column", aggregate.Operation)
				}
				column.Kind = inputColumn.Kind
				column.Nullable = true
				column.NormalizedUnit = cloneUnitIdentity(inputColumn.NormalizedUnit)
			case recipe.ConstructionGroupMean:
				if inputKind != string(recipe.TableScalarInteger) && inputKind != string(recipe.TableScalarDecimal) {
					return nil, nil, nil, fmt.Errorf("group aggregate MEAN requires a numeric input column")
				}
				column.Kind = string(expression.KindDecimal)
				column.Nullable = true
				column.NormalizedUnit = cloneUnitIdentity(inputColumn.NormalizedUnit)
			}
		default:
			return nil, nil, nil, fmt.Errorf("unsupported group aggregate operation %q", aggregate.Operation)
		}
		physicalAggregate.OutputKind = column.Kind
		metadata[output.ID] = column
		projections[output.ID] = ir.PhysicalProjection{Name: output.Name, Value: ir.PhysicalValue{Variable: physicalAggregate.Variable}}
		aggregates = append(aggregates, physicalAggregate)
	}
	return aggregates, metadata, projections, nil
}

func lowerConstructionRowValues(
	rowValues []recipe.ConstructionRowValue,
	input map[string]CompiledOutputColumn,
	outputs map[string]recipe.StageColumn,
	constructionID string,
	usedVariables map[string]bool,
	stepIndex int,
	groupSourceContexts ...*constructionGroupRowValueSourceContext,
) ([]ir.PhysicalStageRowValue, []CompiledOutputColumn, error) {
	var groupSourceContext *constructionGroupRowValueSourceContext
	if len(groupSourceContexts) > 1 {
		return nil, nil, fmt.Errorf("row value lowering received multiple Group source contexts")
	}
	if len(groupSourceContexts) == 1 {
		groupSourceContext = groupSourceContexts[0]
	}
	physical := make([]ir.PhysicalStageRowValue, 0, len(rowValues))
	compiled := make([]CompiledOutputColumn, 0, len(rowValues))
	for index, rowValue := range rowValues {
		var inputColumn CompiledOutputColumn
		var sourceBinding *ir.PhysicalStageGroupFieldSource
		if rowValue.FieldSource != nil {
			if rowValue.InputColumnID != "" || rowValue.FieldSource.ValueKind != "SCALAR" || groupSourceContext == nil ||
				groupSourceContext.Plan == nil || groupSourceContext.SourceContext == nil || groupSourceContext.InputStageID == "" ||
				groupSourceContext.Stage == nil || rowValue.FieldSource.SourceStageID != groupSourceContext.InputStageID {
				return nil, nil, fmt.Errorf("row value fieldSource must bind the owning Group's exact input stage and source shape")
			}
			var projection ir.PhysicalProjection
			var err error
			inputColumn, projection, sourceBinding, err = lowerConstructionGroupScalarSource(
				groupSourceContext.Plan, *rowValue.FieldSource, groupSourceContext.SourceContext,
				stepIndex, groupSourceContext.FirstSourceIndex+index, usedVariables, groupSourceContext.Stage,
				constructionGroupFieldSourceLoweringOptions{RelatedAdapterSlot: groupSourceContext.RelatedAdapterSlot, Policy: groupSourceContext.Policy},
			)
			if err != nil {
				return nil, nil, fmt.Errorf("row value fieldSource: %w", err)
			}
			_ = projection
		} else {
			var ok bool
			inputColumn, ok = input[rowValue.InputColumnID]
			if !ok || inputColumn.Internal {
				return nil, nil, fmt.Errorf("row value input column ID %q is not public", rowValue.InputColumnID)
			}
		}
		inputKind, scalar := constructionRowValueKind(inputColumn.Kind)
		if !scalar {
			return nil, nil, fmt.Errorf("row value input column %q has unsupported scalar or array item type %q", inputColumn.Name, inputColumn.Kind)
		}
		output, ok := outputs[rowValue.OutputColumnID]
		if !ok {
			return nil, nil, fmt.Errorf("row value output column ID %q is missing from step schema", rowValue.OutputColumnID)
		}
		if output.Type != "" && output.Type != "INFER" {
			want := inputColumn.Kind
			if rowValue.Policy == recipe.ConstructionRowValueAll {
				want = "array"
			}
			if output.Type != want {
				return nil, nil, fmt.Errorf("row value output %q type %q does not match inferred type %q", output.ID, output.Type, want)
			}
		}
		many := inputColumn.Cardinality == string(expression.Many)
		cardinality, nullable := string(expression.OptionalOne), true
		if rowValue.Policy == recipe.ConstructionRowValueAll {
			cardinality, nullable = string(expression.Many), false
		}
		if rowValue.Policy != recipe.ConstructionRowValueAll && rowValue.Policy != recipe.ConstructionRowValueOne {
			return nil, nil, fmt.Errorf("row value policy %q is unsupported", rowValue.Policy)
		}
		physical = append(physical, ir.PhysicalStageRowValue{
			InputColumn: inputColumn.Name, InputKind: inputKind, InputMany: many,
			FieldSource: sourceBinding,
			Output:      output.Name, Policy: string(rowValue.Policy),
			Variable: allocateConstructionVariable(usedVariables, "row_value", stepIndex),
		})
		compiled = append(compiled, CompiledOutputColumn{
			ID: output.ID, Name: output.Name, Label: constructionFirstNonEmpty(output.Label, output.Name),
			SemanticPath: "construction_row_value:" + constructionID + ":" + inputColumn.SemanticPath,
			Kind:         inputColumn.Kind, Cardinality: cardinality, Nullable: nullable,
		})
	}
	return physical, compiled, nil
}

type constructionGroupRowValueSourceContext struct {
	Plan               *ir.PhysicalPlan
	SourceContext      *CompiledGroupFieldSourceContext
	InputStageID       string
	Stage              *ir.PhysicalConstructionStage
	FirstSourceIndex   int
	RelatedAdapterSlot **ir.PhysicalStageGroupRelatedOccurrenceAdapter
	Policy             ir.PhysicalOptimizationPolicy
}

func constructionRowValueKind(kind string) (string, bool) {
	if scalar, ok := TableReshapeScalarKind(kind); ok {
		return scalar, true
	}
	if expression.ValueKind(kind) == expression.KindObject {
		return "OBJECT", true
	}
	return "", false
}

func constructionSourceRowValueProjections(plan *ir.PhysicalPlan, rowValues []ir.PhysicalStageRowValue) ([]ir.PhysicalProjection, error) {
	if len(rowValues) == 0 {
		return nil, nil
	}
	var sourceReturn *ir.PhysicalReturn
	for index := len(plan.Operations) - 1; index >= 0; index-- {
		operation := plan.Operations[index]
		if operation.Kind == ir.PhysicalReturnOp && operation.Return != nil {
			sourceReturn = operation.Return
			break
		}
	}
	if sourceReturn == nil {
		return nil, fmt.Errorf("row values require a resolved source projection")
	}
	byName := make(map[string]ir.PhysicalProjection, len(sourceReturn.Projections))
	for _, projection := range sourceReturn.Projections {
		byName[projection.Name] = projection
	}
	seen := make(map[string]bool, len(rowValues))
	projections := make([]ir.PhysicalProjection, 0, len(rowValues))
	for _, rowValue := range rowValues {
		if seen[rowValue.InputColumn] {
			continue
		}
		projection, ok := byName[rowValue.InputColumn]
		if !ok {
			return nil, fmt.Errorf("row value source column %q has no resolved source projection", rowValue.InputColumn)
		}
		projection.Hidden = true
		projections = append(projections, projection)
		seen[rowValue.InputColumn] = true
	}
	return projections, nil
}

func lowerConstructionExpand(
	plan *ir.PhysicalPlan,
	expand recipe.ConstructionExpand,
	declarations []recipe.StageColumn,
	input map[string]CompiledOutputColumn,
	outputs map[string]recipe.StageColumn,
	inputRow string,
	usedVariables map[string]bool,
	stepIndex int,
) (ir.PhysicalStageExpand, []ir.PhysicalProjection, []CompiledOutputColumn, error) {
	inputColumn, ok := input[expand.InputColumnID]
	if !ok || inputColumn.Internal {
		return ir.PhysicalStageExpand{}, nil, nil, fmt.Errorf("expand input column ID %q is not public", expand.InputColumnID)
	}
	if inputColumn.Cardinality != string(expression.Many) {
		return ir.PhysicalStageExpand{}, nil, nil, fmt.Errorf("expand input column %q must be array-valued", inputColumn.Name)
	}
	inputKind, scalar := TableReshapeScalarKind(inputColumn.Kind)
	if !scalar {
		return ir.PhysicalStageExpand{}, nil, nil, fmt.Errorf("expand input column %q must contain scalar values", inputColumn.Name)
	}
	itemOutput, ok := outputs[expand.OutputColumnID]
	if !ok {
		return ir.PhysicalStageExpand{}, nil, nil, fmt.Errorf("expand item output ID %q is missing from step schema", expand.OutputColumnID)
	}
	constructionBind := nextTableReshapeBindKey(plan.BindVars, "construction_expand_id")
	plan.BindVars[constructionBind] = expand.ConstructionID
	policy := expand.EmptyPolicy.Normalized()
	physical := ir.PhysicalStageExpand{
		ItemsVariable:    allocateConstructionVariable(usedVariables, "expand_items", stepIndex),
		IndexVariable:    allocateConstructionVariable(usedVariables, "expand_index", stepIndex),
		ItemVariable:     allocateConstructionVariable(usedVariables, "expand_item", stepIndex),
		IdentityVariable: allocateConstructionVariable(usedVariables, "expand_identity", stepIndex),
		InputColumn:      inputColumn.Name, OutputColumn: itemOutput.Name, InputKind: inputKind,
		ConstructionIDBindKey: constructionBind, EmptyPolicy: ir.PhysicalUnnestEmptyPolicy(policy),
	}
	metadataByOutputID := make(map[string]CompiledOutputColumn, len(declarations))
	projectionByOutputID := make(map[string]ir.PhysicalProjection, len(declarations))
	itemColumn := inputColumn
	itemColumn.ID, itemColumn.Name = itemOutput.ID, itemOutput.Name
	itemColumn.Label = constructionFirstNonEmpty(itemOutput.Label, itemOutput.Name)
	itemColumn.Cardinality = string(expression.RequiredOne)
	if policy == recipe.ExpansionPreserveParent {
		itemColumn.Cardinality = string(expression.OptionalOne)
		itemColumn.Nullable = true
	}
	itemColumn.SemanticPath = "construction_expand:" + expand.ConstructionID
	metadataByOutputID[itemColumn.ID] = itemColumn
	projectionByOutputID[itemColumn.ID] = ir.PhysicalProjection{Name: itemOutput.Name, Value: ir.PhysicalValue{Variable: physical.ItemVariable}}
	if expand.OrdinalColumnID != "" {
		ordinalOutput := outputs[expand.OrdinalColumnID]
		physical.OrdinalColumn = ordinalOutput.Name
		ordinalCardinality := string(expression.RequiredOne)
		if policy == recipe.ExpansionPreserveParent {
			ordinalCardinality = string(expression.OptionalOne)
		}
		ordinal := CompiledOutputColumn{
			ID: ordinalOutput.ID, Name: ordinalOutput.Name,
			Label:        constructionFirstNonEmpty(ordinalOutput.Label, ordinalOutput.Name),
			SemanticPath: "construction_expand:" + expand.ConstructionID + ":ordinal",
			Kind:         string(expression.KindInteger), Cardinality: ordinalCardinality,
			Nullable: policy == recipe.ExpansionPreserveParent,
		}
		metadataByOutputID[ordinal.ID] = ordinal
		projectionByOutputID[ordinal.ID] = ir.PhysicalProjection{Name: ordinalOutput.Name, Value: ir.PhysicalValue{Variable: physical.IndexVariable}}
	}
	for _, declaration := range declarations {
		if _, exists := metadataByOutputID[declaration.ID]; exists {
			continue
		}
		prior, exists := input[declaration.ID]
		if !exists || prior.Internal || declaration.ID == expand.InputColumnID {
			return ir.PhysicalStageExpand{}, nil, nil, fmt.Errorf("expand output column ID %q is not an item, ordinal, or preserved input", declaration.ID)
		}
		prior.ID, prior.Name = declaration.ID, declaration.Name
		prior.Label = constructionFirstNonEmpty(declaration.Label, prior.Label, declaration.Name)
		metadataByOutputID[declaration.ID] = prior
		columnBind := nextTableReshapeBindKey(plan.BindVars, "construction_expand_column")
		plan.BindVars[columnBind] = input[declaration.ID].Name
		lookup := ir.PhysicalExpression{
			Kind: ir.PhysicalObjectLookupExpression, Cardinality: ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalPreserveNull,
			ObjectLookup: &ir.PhysicalObjectLookup{ObjectVariable: inputRow, KeyBindKey: columnBind},
		}
		projectionByOutputID[declaration.ID] = ir.PhysicalProjection{Name: declaration.Name, Expression: &lookup}
	}
	compiled := make([]CompiledOutputColumn, 0, len(declarations)+1)
	projections := make([]ir.PhysicalProjection, 0, len(declarations)+1)
	for _, declaration := range declarations {
		column, ok := metadataByOutputID[declaration.ID]
		if !ok {
			return ir.PhysicalStageExpand{}, nil, nil, fmt.Errorf("expand output schema contains unexpected column ID %q", declaration.ID)
		}
		column.Label = constructionFirstNonEmpty(declaration.Label, column.Label, declaration.Name)
		column.Name = declaration.Name
		projection := projectionByOutputID[declaration.ID]
		projection.Name = declaration.Name
		compiled = append(compiled, column)
		projections = append(projections, projection)
	}
	projections = append(projections, ir.PhysicalProjection{
		Name: constructionRowID, Hidden: true, Value: ir.PhysicalValue{Variable: physical.IdentityVariable},
	})
	return physical, projections, compiled, nil
}
