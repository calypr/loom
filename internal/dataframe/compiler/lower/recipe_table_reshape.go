package lower

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
)

const tableReshapeRowID = "__loom_row_id"

func reconcileRecipeTableReshapeSchema(finalSchema, reshapeSchema []CompiledOutputColumn) []CompiledOutputColumn {
	reshapedByName := make(map[string]CompiledOutputColumn, len(reshapeSchema))
	for _, column := range reshapeSchema {
		reshapedByName[column.Name] = column
	}
	result := append([]CompiledOutputColumn(nil), finalSchema...)
	for index := range result {
		if reshaped, ok := reshapedByName[result[index].Name]; ok {
			result[index] = reshaped
		}
	}
	return result
}

func appendRecipeTableReshape(plan *ir.PhysicalPlan, output semantic.OutputPlan, baseSchema []CompiledOutputColumn, identity *spec.RowIdentity) ([]CompiledOutputColumn, error) {
	if output.TableReshape == nil {
		return baseSchema, nil
	}
	if output.GroupRows != nil {
		return nil, fmt.Errorf("explicit group rows cannot be combined with table reshape")
	}
	if identity == nil {
		return nil, fmt.Errorf("table reshape requires a row identity")
	}
	returnIndex := -1
	for index := range plan.Operations {
		if plan.Operations[index].Kind == ir.PhysicalReturnOp && plan.Operations[index].Return != nil {
			returnIndex = index
			break
		}
	}
	if returnIndex < 0 {
		return nil, fmt.Errorf("canonical plan has no RETURN operation for table reshape")
	}
	inputProjections := ir.ClonePhysicalOperation(plan.Operations[returnIndex]).Return.Projections
	schemaByName := make(map[string]CompiledOutputColumn, len(baseSchema))
	projectionByName := make(map[string]ir.PhysicalProjection, len(inputProjections))
	for _, column := range baseSchema {
		schemaByName[column.Name] = column
	}
	for _, projection := range inputProjections {
		projectionByName[projection.Name] = projection
	}

	var operation ir.PhysicalOperation
	var projections []ir.PhysicalProjection
	var schema []CompiledOutputColumn
	switch output.TableReshape.Kind {
	case recipe.TableReshapeGroupedPivot:
		pivot := output.TableReshape.GroupedPivot
		if pivot == nil {
			return nil, fmt.Errorf("grouped pivot payload is required")
		}
		physical, nextProjections, nextSchema, err := lowerRecipeGroupedPivot(plan, *pivot, inputProjections, schemaByName, projectionByName, physicalPlanVariables(plan.Operations))
		if err != nil {
			return nil, err
		}
		physical.OneInputRowPerGroup = constructionRootIDPivotSourceEligible(plan, plan.Source.ResourceType) &&
			groupedPivotHasDirectRootIDKey(plan, physical.GroupKeys, baseSchema, plan.Source.ResourceType)
		operation = ir.PhysicalOperation{Kind: ir.PhysicalGroupedPivotOp, Source: ir.PhysicalSource{SemanticField: "table_shape.reshape"}, GroupedPivot: &physical}
		projections, schema = nextProjections, nextSchema
	case recipe.TableReshapeUnpivot:
		unpivot := output.TableReshape.Unpivot
		if unpivot == nil {
			return nil, fmt.Errorf("unpivot payload is required")
		}
		physical, nextProjections, nextSchema, err := lowerRecipeUnpivot(plan, *unpivot, identity, inputProjections, schemaByName, projectionByName, physicalPlanVariables(plan.Operations))
		if err != nil {
			return nil, err
		}
		operation = ir.PhysicalOperation{Kind: ir.PhysicalUnpivotOp, Source: ir.PhysicalSource{SemanticField: "table_shape.reshape"}, Unpivot: &physical}
		projections, schema = nextProjections, nextSchema
	default:
		return nil, fmt.Errorf("unsupported table reshape kind %q", output.TableReshape.Kind)
	}

	terminal := &plan.Operations[returnIndex]
	terminal.Return.Projections = projections
	plan.Operations = append(plan.Operations, ir.PhysicalOperation{})
	copy(plan.Operations[returnIndex+1:], plan.Operations[returnIndex:])
	plan.Operations[returnIndex] = operation
	*identity = spec.RowIdentity{Grain: identity.Grain, Fields: []string{tableReshapeRowID}}
	return schema, nil
}

func lowerRecipeGroupedPivot(plan *ir.PhysicalPlan, pivot semantic.SemanticGroupedPivot, inputProjections []ir.PhysicalProjection, schema map[string]CompiledOutputColumn, projections map[string]ir.PhysicalProjection, usedVariables map[string]bool) (ir.PhysicalGroupedPivot, []ir.PhysicalProjection, []CompiledOutputColumn, error) {
	categoryColumn, err := requireTableScalarColumn(pivot.CategoryColumn, schema, projections)
	if err != nil {
		return ir.PhysicalGroupedPivot{}, nil, nil, fmt.Errorf("category column: %w", err)
	}
	valueColumn, err := requireTableScalarColumn(pivot.ValueColumn, schema, projections)
	if err != nil {
		return ir.PhysicalGroupedPivot{}, nil, nil, fmt.Errorf("value column: %w", err)
	}
	categoryType, ok := tableReshapeScalarKind(categoryColumn.Kind)
	if !ok {
		return ir.PhysicalGroupedPivot{}, nil, nil, fmt.Errorf("category column %q has unsupported scalar type %q", pivot.CategoryColumn, categoryColumn.Kind)
	}
	valueType, ok := tableReshapeScalarKind(valueColumn.Kind)
	if !ok {
		return ir.PhysicalGroupedPivot{}, nil, nil, fmt.Errorf("value column %q has unsupported scalar type %q", pivot.ValueColumn, valueColumn.Kind)
	}
	if pivot.DuplicatePolicy != recipe.PivotDuplicateError && valueType != "INTEGER" && valueType != "DECIMAL" {
		return ir.PhysicalGroupedPivot{}, nil, nil, fmt.Errorf("duplicate reducer %q requires a numeric value column", pivot.DuplicatePolicy)
	}
	constructionBind := nextTableReshapeBindKey(plan.BindVars, "reshape_construction")
	plan.BindVars[constructionBind] = pivot.ConstructionID
	categoryProjection := projections[pivot.CategoryColumn]
	physical := ir.PhysicalGroupedPivot{
		ConstructionID: pivot.ConstructionID, InputRowVariable: allocateRecipeReshapeVariable(usedVariables, "pivot_input"),
		GroupRowsVariable: allocateRecipeReshapeVariable(usedVariables, "pivot_group_rows"), OutputRowVariable: allocateRecipeReshapeVariable(usedVariables, "pivot_output"),
		InputProjections: ir.ClonePhysicalOperation(ir.PhysicalOperation{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: inputProjections}}).Return.Projections,
		CategoryColumn:   pivot.CategoryColumn, CategoryType: categoryType, ValueColumn: pivot.ValueColumn, ValueType: valueType,
		ConstructionIDBindKey: constructionBind, DuplicatePolicy: string(pivot.DuplicatePolicy),
		MissingCellPolicy: string(pivot.MissingCellPolicy), UnlistedCategoryPolicy: string(pivot.UnlistedCategoryPolicy),
	}
	if categoryProjection.Presence != nil {
		physical.CategoryPresenceColumn = "__loom_reshape_category_present"
		physical.CategoryPresence = categoryProjection.Presence
	}
	for _, name := range pivot.GroupKeys {
		column, err := requireTableScalarColumn(name, schema, projections)
		if err != nil {
			return ir.PhysicalGroupedPivot{}, nil, nil, fmt.Errorf("group key %q: %w", name, err)
		}
		kind, _ := tableReshapeScalarKind(column.Kind)
		physical.GroupKeys = append(physical.GroupKeys, ir.PhysicalGroupedPivotKey{Column: name, Variable: allocateRecipeReshapeVariable(usedVariables, "pivot_group"), Kind: kind})
	}
	for index, category := range pivot.Categories {
		physicalCategory := ir.PhysicalGroupedPivotCategory{Output: category.Output}
		switch category.Key.Kind {
		case recipe.TableScalarNull:
			if err := category.Key.ValidatePivotCategoryKey(); err != nil {
				return ir.PhysicalGroupedPivot{}, nil, nil, fmt.Errorf("category key %d: %w", index, err)
			}
			physicalCategory.MatchKind = ir.PhysicalPivotCategoryNullMatch
		case recipe.TableScalarMissing:
			if err := category.Key.ValidatePivotCategoryKey(); err != nil {
				return ir.PhysicalGroupedPivot{}, nil, nil, fmt.Errorf("category key %d: %w", index, err)
			}
			if physical.CategoryPresence == nil {
				return ir.PhysicalGroupedPivot{}, nil, nil, fmt.Errorf("category key %d MISSING requires a simple scalar selector with preserved property presence", index)
			}
			physicalCategory.MatchKind = ir.PhysicalPivotCategoryMissingMatch
		default:
			if string(category.Key.Kind) != categoryType {
				return ir.PhysicalGroupedPivot{}, nil, nil, fmt.Errorf("category key %d type %q does not match category column type %q", index, category.Key.Kind, categoryColumn.Kind)
			}
			key, err := recipeTableScalarValue(category.Key)
			if err != nil {
				return ir.PhysicalGroupedPivot{}, nil, nil, fmt.Errorf("category key %d: %w", index, err)
			}
			keyBind := nextTableReshapeBindKey(plan.BindVars, fmt.Sprintf("reshape_category_%d", index))
			plan.BindVars[keyBind] = key
			physicalCategory.MatchKind = ir.PhysicalPivotCategoryValueMatch
			physicalCategory.ValueBindKey = keyBind
			physicalCategory.ValueKind = string(category.Key.Kind)
		}
		physical.Categories = append(physical.Categories, physicalCategory)
	}
	if pivot.UnlistedCategoryPolicy == recipe.PivotUnlistedCategoryExcludeWithEvidence {
		physical.UnlistedEvidenceColumn = "__loom_reshape_unlisted_count"
	}

	outputProjections := make([]ir.PhysicalProjection, 0, len(pivot.GroupKeys)+len(pivot.Categories)+2)
	outputSchema := make([]CompiledOutputColumn, 0, cap(outputProjections))
	for _, key := range pivot.GroupKeys {
		column := schema[key]
		outputProjections = append(outputProjections, reshapeOutputProjection(key, physical.OutputRowVariable, false))
		outputSchema = append(outputSchema, column)
	}
	for _, category := range pivot.Categories {
		nullable := valueColumn.Nullable || pivot.MissingCellPolicy == recipe.PivotMissingCellNull
		cardinality := string(expression.RequiredOne)
		if nullable {
			cardinality = string(expression.OptionalOne)
		}
		column := valueColumn
		column.Name = category.Output
		column.SemanticPath = "table_reshape:" + pivot.ConstructionID + ":category:" + category.Output
		column.Cardinality = cardinality
		column.Nullable = nullable
		column.Internal = false
		column.Identity = false
		column.Discovered = false
		outputSchema = append(outputSchema, column)
		outputProjections = append(outputProjections, reshapeOutputProjection(category.Output, physical.OutputRowVariable, false))
	}
	if physical.UnlistedEvidenceColumn != "" {
		outputSchema = append(outputSchema, CompiledOutputColumn{
			Name: physical.UnlistedEvidenceColumn, SemanticPath: "table_reshape:" + pivot.ConstructionID + ":unlisted_count",
			Kind: string(expression.KindInteger), Cardinality: string(expression.RequiredOne), Internal: true,
		})
		outputProjections = append(outputProjections, reshapeOutputProjection(physical.UnlistedEvidenceColumn, physical.OutputRowVariable, true))
	}
	outputSchema = append(outputSchema, reshapeRowIDSchema(pivot.ConstructionID))
	outputProjections = append(outputProjections, reshapeOutputProjection(tableReshapeRowID, physical.OutputRowVariable, true))
	return physical, outputProjections, outputSchema, nil
}

func lowerRecipeUnpivot(plan *ir.PhysicalPlan, unpivot semantic.SemanticUnpivot, identity *spec.RowIdentity, inputProjections []ir.PhysicalProjection, schema map[string]CompiledOutputColumn, projections map[string]ir.PhysicalProjection, usedVariables map[string]bool) (ir.PhysicalUnpivot, []ir.PhysicalProjection, []CompiledOutputColumn, error) {
	keyType := ""
	valueType := ""
	selected := make(map[string]bool, len(unpivot.Inputs))
	constructionBind := nextTableReshapeBindKey(plan.BindVars, "reshape_construction")
	plan.BindVars[constructionBind] = unpivot.ConstructionID
	physical := ir.PhysicalUnpivot{
		ConstructionID: unpivot.ConstructionID, InputRowVariable: allocateRecipeReshapeVariable(usedVariables, "unpivot_input"),
		SlotVariable: allocateRecipeReshapeVariable(usedVariables, "unpivot_slot"), OutputRowVariable: allocateRecipeReshapeVariable(usedVariables, "unpivot_output"),
		InputProjections: ir.ClonePhysicalOperation(ir.PhysicalOperation{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: inputProjections}}).Return.Projections,
		KeyOutput:        unpivot.KeyOutput, ValueOutput: unpivot.ValueOutput, ConstructionIDBindKey: constructionBind,
		NullRowPolicy: string(unpivot.NullRowPolicy),
	}
	for index, input := range unpivot.Inputs {
		column, err := requireTableScalarColumn(input.Column, schema, projections)
		if err != nil {
			return ir.PhysicalUnpivot{}, nil, nil, fmt.Errorf("input %q: %w", input.Column, err)
		}
		keyKind := string(input.Key.Kind)
		if keyType == "" {
			keyType = keyKind
		} else if keyKind != keyType {
			return ir.PhysicalUnpivot{}, nil, nil, fmt.Errorf("unpivot input keys must have one canonical scalar type")
		}
		inputType, ok := tableReshapeScalarKind(column.Kind)
		if !ok {
			return ir.PhysicalUnpivot{}, nil, nil, fmt.Errorf("input %q has unsupported scalar type %q", input.Column, column.Kind)
		}
		if valueType == "" {
			valueType = inputType
		} else if valueType != inputType {
			if valueType == "INTEGER" && inputType == "DECIMAL" {
				valueType = "DECIMAL"
			} else if valueType != "DECIMAL" || inputType != "INTEGER" {
				return ir.PhysicalUnpivot{}, nil, nil, fmt.Errorf("unpivot input %q type %q is incompatible with previous inputs", input.Column, column.Kind)
			}
		}
		key, err := recipeTableScalarValue(input.Key)
		if err != nil {
			return ir.PhysicalUnpivot{}, nil, nil, fmt.Errorf("input %q key: %w", input.Column, err)
		}
		keyBind := nextTableReshapeBindKey(plan.BindVars, fmt.Sprintf("reshape_unpivot_key_%d", index))
		plan.BindVars[keyBind] = key
		physical.Inputs = append(physical.Inputs, ir.PhysicalUnpivotInput{Column: input.Column, KeyBindKey: keyBind, KeyKind: keyKind, ValueKind: inputType})
		selected[input.Column] = true
	}
	physical.KeyType, physical.ValueType = keyType, valueType
	identityProjections := map[string]bool{}
	for _, projection := range inputProjections {
		identityProjections[projection.Name] = true
	}
	identityFields := append([]string(nil), identity.Fields...)
	if _, hasExplicit := identityProjections[tableReshapeRowID]; hasExplicit {
		identityFields = []string{tableReshapeRowID}
	}
	for _, field := range identityFields {
		if identityProjections[field] {
			physical.IdentityParts = append(physical.IdentityParts, ir.PhysicalUnpivotIdentityPart{Name: field, Value: ir.PhysicalValue{Variable: physical.InputRowVariable, Path: []string{field}}})
			continue
		}
		if field == "project" {
			if _, ok := plan.BindVars[field]; ok {
				physical.IdentityParts = append(physical.IdentityParts, ir.PhysicalUnpivotIdentityPart{Name: field, Value: ir.PhysicalValue{BindKey: field}})
				continue
			}
		}
		return ir.PhysicalUnpivot{}, nil, nil, fmt.Errorf("row identity field %q is unavailable for unpivot identity", field)
	}

	outputProjections := make([]ir.PhysicalProjection, 0, len(inputProjections)+3)
	outputSchema := make([]CompiledOutputColumn, 0, len(inputProjections)+3)
	for _, projection := range inputProjections {
		if selected[projection.Name] || projection.Name == tableReshapeRowID {
			continue
		}
		outputProjections = append(outputProjections, reshapeOutputProjection(projection.Name, physical.OutputRowVariable, projection.Hidden))
		column := schema[projection.Name]
		outputSchema = append(outputSchema, column)
	}
	outputSchema = append(outputSchema, CompiledOutputColumn{
		Name: unpivot.KeyOutput, SemanticPath: "table_reshape:" + unpivot.ConstructionID + ":key",
		Kind: unpivotKeyLogicalKind(keyType), Cardinality: string(expression.RequiredOne), Nullable: false,
	})
	outputProjections = append(outputProjections, reshapeOutputProjection(unpivot.KeyOutput, physical.OutputRowVariable, false))
	nullableValue := unpivot.NullRowPolicy == recipe.UnpivotNullPreserve
	valueCardinality := string(expression.RequiredOne)
	if nullableValue {
		valueCardinality = string(expression.OptionalOne)
	}
	outputSchema = append(outputSchema, CompiledOutputColumn{
		Name: unpivot.ValueOutput, SemanticPath: "table_reshape:" + unpivot.ConstructionID + ":value",
		Kind: unpivotValueLogicalKind(valueType), Cardinality: valueCardinality, Nullable: nullableValue,
	})
	outputProjections = append(outputProjections, reshapeOutputProjection(unpivot.ValueOutput, physical.OutputRowVariable, false))
	outputSchema = append(outputSchema, reshapeRowIDSchema(unpivot.ConstructionID))
	outputProjections = append(outputProjections, reshapeOutputProjection(tableReshapeRowID, physical.OutputRowVariable, true))
	return physical, outputProjections, outputSchema, nil
}

func requireTableScalarColumn(name string, schema map[string]CompiledOutputColumn, projections map[string]ir.PhysicalProjection) (CompiledOutputColumn, error) {
	column, ok := schema[name]
	if !ok || column.Internal {
		return CompiledOutputColumn{}, fmt.Errorf("column %q is not a public output column", name)
	}
	projection, ok := projections[name]
	if !ok || projection.Hidden {
		return CompiledOutputColumn{}, fmt.Errorf("column %q has no public physical projection", name)
	}
	if column.Cardinality == string(expression.Many) {
		return CompiledOutputColumn{}, fmt.Errorf("column %q is repeated", name)
	}
	if _, ok := tableReshapeScalarKind(column.Kind); !ok {
		return CompiledOutputColumn{}, fmt.Errorf("column %q has unsupported scalar type %q", name, column.Kind)
	}
	return column, nil
}

func tableReshapeScalarKind(kind string) (string, bool) {
	switch expression.ValueKind(strings.ToLower(strings.TrimSpace(kind))) {
	case expression.KindString, expression.KindDate, expression.KindDateTime, expression.KindCode, expression.KindUUID:
		return string(recipe.TableScalarString), true
	case expression.KindInteger:
		return string(recipe.TableScalarInteger), true
	case expression.KindDecimal:
		return string(recipe.TableScalarDecimal), true
	case expression.KindBoolean:
		return string(recipe.TableScalarBoolean), true
	default:
		return "", false
	}
}

func recipeTableScalarValue(scalar recipe.TableScalar) (any, error) {
	if err := scalar.ValidateConcreteValue(); err != nil {
		return nil, err
	}
	switch scalar.Kind {
	case recipe.TableScalarString:
		return *scalar.String, nil
	case recipe.TableScalarInteger:
		return *scalar.Integer, nil
	case recipe.TableScalarDecimal:
		return *scalar.Decimal, nil
	case recipe.TableScalarBoolean:
		return *scalar.Boolean, nil
	default:
		return nil, fmt.Errorf("unsupported table scalar kind %q", scalar.Kind)
	}
}

func reshapeOutputProjection(name, variable string, hidden bool) ir.PhysicalProjection {
	return ir.PhysicalProjection{Name: name, Hidden: hidden, Value: ir.PhysicalValue{Variable: variable, Path: []string{name}}}
}

func reshapeRowIDSchema(constructionID string) CompiledOutputColumn {
	return CompiledOutputColumn{Name: tableReshapeRowID, SemanticPath: "table_reshape:" + constructionID + ":row_id", Kind: string(expression.KindString), Cardinality: string(expression.RequiredOne), Internal: true, Identity: true}
}

func nextTableReshapeBindKey(bindVars map[string]any, base string) string {
	base = strings.Map(func(character rune) rune {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' {
			return character
		}
		return '_'
	}, base)
	for index := 0; ; index++ {
		key := base
		if index > 0 {
			key = fmt.Sprintf("%s_%d", base, index)
		}
		if _, exists := bindVars[key]; !exists {
			return key
		}
	}
}

func allocateRecipeReshapeVariable(used map[string]bool, suffix string) string {
	for index := 0; ; index++ {
		name := fmt.Sprintf("__loom_reshape_%s_%d", suffix, index)
		if !used[name] {
			used[name] = true
			return name
		}
	}
}

func unpivotKeyLogicalKind(kind string) string {
	switch kind {
	case "STRING":
		return string(expression.KindString)
	case "INTEGER":
		return string(expression.KindInteger)
	case "DECIMAL":
		return string(expression.KindDecimal)
	case "BOOLEAN":
		return string(expression.KindBoolean)
	default:
		return string(expression.KindString)
	}
}

func unpivotValueLogicalKind(kind string) string {
	return unpivotKeyLogicalKind(kind)
}
