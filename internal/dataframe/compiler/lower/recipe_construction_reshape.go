package lower

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func lowerConstructionGroup(
	plan *ir.PhysicalPlan,
	group recipe.ConstructionGroup,
	declarations []recipe.StageColumn,
	input map[string]CompiledOutputColumn,
	outputs map[string]recipe.StageColumn,
	usedVariables map[string]bool,
	stepIndex int,
) (ir.PhysicalStageGroup, []ir.PhysicalProjection, []CompiledOutputColumn, error) {
	constructionBind := nextTableReshapeBindKey(plan.BindVars, "construction_group_id")
	plan.BindVars[constructionBind] = group.ConstructionID
	physical := ir.PhysicalStageGroup{
		GroupRowsVariable:     allocateConstructionVariable(usedVariables, "group_rows", stepIndex),
		IdentityVariable:      allocateConstructionVariable(usedVariables, "group_identity", stepIndex),
		ConstructionIDBindKey: constructionBind,
		MissingKeyPolicy:      ir.PhysicalStageGroupMissingKeyPolicy(group.MissingKeyPolicy.Normalized()),
	}
	metadataByOutputID := make(map[string]CompiledOutputColumn, len(declarations))
	projectionByOutputID := make(map[string]ir.PhysicalProjection, len(declarations))
	for _, key := range group.Keys {
		inputColumn, ok := input[key.InputColumnID]
		if !ok || inputColumn.Internal {
			return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("group key input column ID %q is not public", key.InputColumnID)
		}
		kind, scalar := tableReshapeScalarKind(inputColumn.Kind)
		if !scalar || inputColumn.Cardinality == string(expression.Many) {
			return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("group key column %q must be a public scalar column", inputColumn.Name)
		}
		output, ok := outputs[key.OutputColumnID]
		if !ok {
			return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("group key output column ID %q is missing from step schema", key.OutputColumnID)
		}
		physicalKey := ir.PhysicalStageGroupKey{
			InputColumn: inputColumn.Name, OutputColumn: output.Name,
			Variable: allocateConstructionVariable(usedVariables, "group_key", stepIndex), Kind: kind,
		}
		physical.Keys = append(physical.Keys, physicalKey)
		column := inputColumn
		column.ID, column.Name = output.ID, output.Name
		column.Label = constructionFirstNonEmpty(output.Label, inputColumn.Label, output.Name)
		metadataByOutputID[output.ID] = column
		projectionByOutputID[output.ID] = ir.PhysicalProjection{Name: output.Name, Value: ir.PhysicalValue{Variable: physicalKey.Variable}}
	}
	for _, aggregate := range group.Aggregates {
		output, ok := outputs[aggregate.OutputColumnID]
		if !ok {
			return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("group aggregate output column ID %q is missing from step schema", aggregate.OutputColumnID)
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
				return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("group aggregate input column ID %q is not public", aggregate.InputColumnID)
			}
			inputKind, scalar := tableReshapeScalarKind(inputColumn.Kind)
			if !scalar || inputColumn.Cardinality == string(expression.Many) {
				return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("group aggregate input column %q must be a public scalar column", inputColumn.Name)
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
					return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("group aggregate %s requires a numeric input column", aggregate.Operation)
				}
				column.Kind = inputColumn.Kind
				column.Nullable = true
				column.NormalizedUnit = cloneUnitIdentity(inputColumn.NormalizedUnit)
			case recipe.ConstructionGroupMean:
				if inputKind != string(recipe.TableScalarInteger) && inputKind != string(recipe.TableScalarDecimal) {
					return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("group aggregate MEAN requires a numeric input column")
				}
				column.Kind = string(expression.KindDecimal)
				column.Nullable = true
				column.NormalizedUnit = cloneUnitIdentity(inputColumn.NormalizedUnit)
			}
		default:
			return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("unsupported group aggregate operation %q", aggregate.Operation)
		}
		physicalAggregate.OutputKind = column.Kind
		metadataByOutputID[output.ID] = column
		projectionByOutputID[output.ID] = ir.PhysicalProjection{Name: output.Name, Value: ir.PhysicalValue{Variable: physicalAggregate.Variable}}
		physical.Aggregates = append(physical.Aggregates, physicalAggregate)
	}
	compiled := make([]CompiledOutputColumn, 0, len(declarations)+1)
	projections := make([]ir.PhysicalProjection, 0, len(declarations)+1)
	for _, declaration := range declarations {
		column, ok := metadataByOutputID[declaration.ID]
		if !ok {
			return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("group output schema contains unexpected column ID %q", declaration.ID)
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
	inputKind, scalar := tableReshapeScalarKind(inputColumn.Kind)
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
