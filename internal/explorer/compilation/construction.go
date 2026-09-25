package compilation

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

// recipeConstruction maps durable construction intent into the recipe's
// resolved compiler contract. Source names come from the resolved public
// emissions, while IDs and labels come from authored projection slots.
func recipeConstruction(authored *authoringv2.Construction, columns []authoringv2.Column, emitted []explorer.EmittedColumn) (*recipe.Construction, error) {
	if authored == nil {
		return nil, nil
	}
	sourceColumns, err := recipeConstructionSourceColumns(columns, emitted)
	if err != nil {
		return nil, err
	}
	construction := &recipe.Construction{
		Version:       authored.Version,
		SourceColumns: sourceColumns,
		Steps:         make([]recipe.ConstructionStep, 0, len(authored.Steps)),
	}
	for index, step := range authored.Steps {
		mapped, err := recipeConstructionStep(step)
		if err != nil {
			return nil, fmt.Errorf("steps[%d]: %w", index, err)
		}
		construction.Steps = append(construction.Steps, mapped)
	}
	return construction, nil
}

func recipeConstructionSourceColumns(columns []authoringv2.Column, emitted []explorer.EmittedColumn) ([]recipe.StageColumn, error) {
	emissionsByAuthoredName := make(map[string][]explorer.EmittedColumn, len(columns))
	for _, emission := range emitted {
		for _, authoredName := range emission.AuthoredColumns {
			emissionsByAuthoredName[authoredName] = append(emissionsByAuthoredName[authoredName], emission)
		}
	}

	result := make([]recipe.StageColumn, 0, len(columns))
	usedEmissions := make(map[string]string, len(columns))
	for index, column := range columns {
		if !requiredConstructionID(column.ColumnID) {
			return nil, fmt.Errorf("columns[%d] requires a stable columnId", index)
		}
		if strings.EqualFold(strings.TrimSpace(column.Source.ProjectionMode()), "INDEXED") {
			return nil, fmt.Errorf("columns[%d] INDEXED projection has multiple public emissions without stable child column IDs", index)
		}
		matches := emissionsByAuthoredName[column.Column]
		if len(matches) != 1 {
			return nil, fmt.Errorf("columns[%d] authored slot %q resolves to %d public emissions; staged construction requires exactly one", index, column.Column, len(matches))
		}
		emission := matches[0]
		if strings.TrimSpace(emission.EmissionID) == "" || strings.TrimSpace(emission.PublicColumn) == "" {
			return nil, fmt.Errorf("columns[%d] authored slot %q has an incomplete resolved public emission", index, column.Column)
		}
		if prior, exists := usedEmissions[emission.EmissionID]; exists {
			return nil, fmt.Errorf("columns[%d] and %q resolve to the same public emission %q", index, prior, emission.EmissionID)
		}
		usedEmissions[emission.EmissionID] = column.Column
		result = append(result, recipe.StageColumn{
			ID: column.ColumnID, Name: emission.PublicColumn, Label: column.Label, Type: column.LogicalType,
		})
	}
	if len(usedEmissions) != len(emitted) {
		return nil, fmt.Errorf("resolved source schema has %d public emissions for %d authored slots", len(emitted), len(columns))
	}
	return result, nil
}

func requiredConstructionID(value string) bool {
	return strings.TrimSpace(value) != "" && strings.TrimSpace(value) == value
}

func recipeConstructionStep(step authoringv2.ConstructionStep) (recipe.ConstructionStep, error) {
	inputs := make([]recipe.ConstructionInputRef, 0, len(step.Inputs))
	for _, input := range step.Inputs {
		inputs = append(inputs, recipe.ConstructionInputRef{
			Kind: recipe.ConstructionInputKind(input.Kind), StepID: input.StepID,
			TableID: input.TableID, RevisionID: input.RevisionID, OutputID: input.OutputID,
		})
	}
	operation, err := recipeConstructionOperation(step.Operation)
	if err != nil {
		return recipe.ConstructionStep{}, err
	}
	outputs := make([]recipe.StageColumn, 0, len(step.Outputs))
	for _, output := range step.Outputs {
		outputs = append(outputs, recipe.StageColumn{ID: output.ID, Name: output.Name, Label: output.Label, Type: output.Type})
	}
	return recipe.ConstructionStep{ID: step.ID, Inputs: inputs, Operation: operation, Outputs: outputs}, nil
}

func recipeConstructionOperation(authored authoringv2.ConstructionOperation) (recipe.ConstructionOperation, error) {
	operation := recipe.ConstructionOperation{Kind: recipe.ConstructionOperationKind(authored.Kind)}
	switch authored.Kind {
	case authoringv2.ConstructionOperationPivot:
		if authored.Pivot == nil {
			return recipe.ConstructionOperation{}, fmt.Errorf("pivot payload is required")
		}
		pivot := authored.Pivot
		mapped := &recipe.ConstructionPivot{
			ConstructionID: pivot.ConstructionID, GroupKeyIDs: append([]string(nil), pivot.GroupKeyIDs...),
			CategoryColumnID: pivot.CategoryColumnID, ValueColumnID: pivot.ValueColumnID,
			DuplicatePolicy:        recipe.PivotDuplicatePolicy(pivot.DuplicatePolicy),
			MissingCellPolicy:      recipe.PivotMissingCellPolicy(pivot.MissingCellPolicy),
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryPolicy(pivot.UnlistedCategoryPolicy),
			Categories:             make([]recipe.ConstructionPivotCategory, 0, len(pivot.Categories)),
		}
		for index, category := range pivot.Categories {
			key, err := recipeTableScalar(category.Key, tableScalarPivotCategoryKey)
			if err != nil {
				return recipe.ConstructionOperation{}, fmt.Errorf("pivot.categories[%d]: %w", index, err)
			}
			mapped.Categories = append(mapped.Categories, recipe.ConstructionPivotCategory{Key: key, OutputColumnID: category.OutputColumnID})
		}
		operation.Pivot = mapped
	case authoringv2.ConstructionOperationDerive:
		if authored.Derive == nil {
			return recipe.ConstructionOperation{}, fmt.Errorf("derive payload is required")
		}
		left, err := recipeConstructionOperand(authored.Derive.Left)
		if err != nil {
			return recipe.ConstructionOperation{}, fmt.Errorf("derive.left: %w", err)
		}
		right, err := recipeConstructionOperand(authored.Derive.Right)
		if err != nil {
			return recipe.ConstructionOperation{}, fmt.Errorf("derive.right: %w", err)
		}
		derive := authored.Derive
		operation.Derive = &recipe.ConstructionDerive{
			ConstructionID: derive.ConstructionID, OutputColumnID: derive.OutputColumnID,
			Operation: recipe.DerivedOperation(derive.Operation), Left: left, Right: right,
			MissingInputPolicy:   recipe.MissingInputPolicy(derive.MissingInputPolicy),
			DivisionByZeroPolicy: recipe.DivisionByZeroPolicy(derive.DivisionByZeroPolicy),
		}
	case authoringv2.ConstructionOperationFilter:
		if authored.Filter == nil {
			return recipe.ConstructionOperation{}, fmt.Errorf("filter payload is required")
		}
		values := make([]recipe.FilterValue, 0, len(authored.Filter.Values))
		for index, value := range authored.Filter.Values {
			if !recipe.FilterValueKind(value.Kind).Valid() {
				return recipe.ConstructionOperation{}, fmt.Errorf("filter.values[%d] has unsupported kind %q", index, value.Kind)
			}
			mapped := recipe.FilterValue{
				Kind: recipe.FilterValueKind(value.Kind), String: value.String, Boolean: value.Boolean,
				Integer: value.Integer, Decimal: value.Decimal, Date: value.Date, DateTime: value.DateTime,
			}
			if value.Code != nil {
				mapped.Code = &recipe.CodeValue{System: value.Code.System, Code: value.Code.Code, Display: value.Code.Display}
			}
			values = append(values, mapped)
		}
		operation.Filter = &recipe.ConstructionFilter{
			ColumnID: authored.Filter.ColumnID, Operator: recipe.FilterOperator(authored.Filter.Operator), Values: values,
		}
	case authoringv2.ConstructionOperationUnpivot:
		if authored.Unpivot == nil {
			return recipe.ConstructionOperation{}, fmt.Errorf("unpivot payload is required")
		}
		unpivot := authored.Unpivot
		mapped := &recipe.ConstructionUnpivot{
			ConstructionID: unpivot.ConstructionID, KeyOutputColumnID: unpivot.KeyOutputColumnID,
			ValueOutputColumnID: unpivot.ValueOutputColumnID,
			NullRowPolicy:       recipe.UnpivotNullRowPolicy(unpivot.NullRowPolicy),
			Inputs:              make([]recipe.ConstructionUnpivotInput, 0, len(unpivot.Inputs)),
		}
		for index, input := range unpivot.Inputs {
			key, err := recipeTableScalar(input.Key, tableScalarUnpivotKey)
			if err != nil {
				return recipe.ConstructionOperation{}, fmt.Errorf("unpivot.inputs[%d]: %w", index, err)
			}
			mapped.Inputs = append(mapped.Inputs, recipe.ConstructionUnpivotInput{ColumnID: input.ColumnID, Key: key})
		}
		operation.Unpivot = mapped
	case authoringv2.ConstructionOperationGroup:
		if authored.Group == nil {
			return recipe.ConstructionOperation{}, fmt.Errorf("group payload is required")
		}
		group := authored.Group
		mapped := &recipe.ConstructionGroup{
			ConstructionID: group.ConstructionID,
			Keys:           make([]recipe.ConstructionGroupKey, 0, len(group.Keys)),
			Aggregates:     make([]recipe.ConstructionGroupAggregate, 0, len(group.Aggregates)),
		}
		for _, key := range group.Keys {
			mapped.Keys = append(mapped.Keys, recipe.ConstructionGroupKey{
				InputColumnID: key.InputColumnID, OutputColumnID: key.OutputColumnID,
			})
		}
		for _, aggregate := range group.Aggregates {
			mapped.Aggregates = append(mapped.Aggregates, recipe.ConstructionGroupAggregate{
				Operation:     recipe.ConstructionGroupAggregateOp(aggregate.Operation),
				InputColumnID: aggregate.InputColumnID, OutputColumnID: aggregate.OutputColumnID,
			})
		}
		operation.Group = mapped
	case authoringv2.ConstructionOperationExpand:
		if authored.Expand == nil {
			return recipe.ConstructionOperation{}, fmt.Errorf("expand payload is required")
		}
		expand := authored.Expand
		operation.Expand = &recipe.ConstructionExpand{
			ConstructionID: expand.ConstructionID, InputColumnID: expand.InputColumnID,
			OutputColumnID: expand.OutputColumnID, OrdinalColumnID: expand.OrdinalColumnID,
			EmptyPolicy: recipe.ExpansionEmptyPolicy(expand.EmptyPolicy),
		}
	default:
		return recipe.ConstructionOperation{}, fmt.Errorf("unsupported operation kind %q", authored.Kind)
	}
	return operation, nil
}

func recipeConstructionOperand(operand authoringv2.ConstructionOperand) (recipe.ConstructionOperand, error) {
	result := recipe.ConstructionOperand{Kind: recipe.DerivedOperandKind(operand.Kind), ColumnID: operand.ColumnID}
	switch operand.Kind {
	case authoringv2.ConstructionColumnOperand:
		return result, nil
	case authoringv2.ConstructionLiteralOperand:
		if operand.Literal == nil {
			return recipe.ConstructionOperand{}, fmt.Errorf("literal payload is required")
		}
		literal := &recipe.DerivedLiteral{Kind: recipe.NumericKind(operand.Literal.Kind), Integer: operand.Literal.Integer, Decimal: operand.Literal.Decimal}
		result.ColumnID = ""
		result.Literal = literal
		return result, nil
	default:
		return recipe.ConstructionOperand{}, fmt.Errorf("unsupported operand kind %q", operand.Kind)
	}
}
