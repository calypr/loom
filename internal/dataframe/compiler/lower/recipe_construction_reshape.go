package lower

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func lowerConstructionGroup(
	plan *ir.PhysicalPlan,
	group recipe.ConstructionGroup,
	rowValues []recipe.ConstructionRowValue,
	declarations []recipe.StageColumn,
	input map[string]CompiledOutputColumn,
	inputIdentity string,
	inputStageID string,
	rootResourceType string,
	outputs map[string]recipe.StageColumn,
	usedVariables map[string]bool,
	stepIndex int,
	stage *ir.PhysicalConstructionStage,
) (ir.PhysicalStageGroup, []ir.PhysicalProjection, []CompiledOutputColumn, error) {
	constructionBind := nextTableReshapeBindKey(plan.BindVars, "construction_group_id")
	plan.BindVars[constructionBind] = group.ConstructionID
	physical := ir.PhysicalStageGroup{
		GroupRowsVariable:     allocateConstructionVariable(usedVariables, "group_rows", stepIndex),
		IdentityVariable:      allocateConstructionVariable(usedVariables, "group_identity", stepIndex),
		ConstructionIDBindKey: constructionBind,
		MissingKeyPolicy:      ir.PhysicalStageGroupMissingKeyPolicy(group.MissingKeyPolicy.Normalized()),
	}
	var groupFieldContext *CompiledGroupFieldSourceContext
	needsGroupFieldContext := false
	for _, key := range group.Keys {
		if key.FieldSource != nil {
			needsGroupFieldContext = true
		}
	}
	for _, rowValue := range rowValues {
		if rowValue.FieldSource != nil {
			needsGroupFieldContext = true
		}
	}
	if needsGroupFieldContext {
		if inputStageID != recipe.ConstructionSourceProjectionID {
			return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("Group fieldSource must bind the exact input stage")
		}
		var err error
		groupFieldContext, err = describeGroupFieldSourceContext(inputStageID, rootResourceType, inputIdentity, plan)
		if err != nil {
			return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("recompute Group field source context: %w", err)
		}
		if groupFieldContext == nil {
			return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("Group fieldSource has no exact compiler-proven source shape")
		}
	}
	metadataByOutputID := make(map[string]CompiledOutputColumn, len(declarations))
	projectionByOutputID := make(map[string]ir.PhysicalProjection, len(declarations))
	for _, key := range group.Keys {
		if key.SemanticCategory != nil {
			if err := constructionDirectRootRowsUnique(plan, "semantic GROUP"); err != nil {
				return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("semantic Group requires row-preserving direct root input: %w", err)
			}
			if inputStageID != recipe.ConstructionSourceProjectionID || inputIdentity != "_key" {
				return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("semantic group keys require direct root rows identified by _key")
			}
			category := key.SemanticCategory
			if category.Kind != "BINDING" || category.ResourceType != rootResourceType {
				return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("semantic group binding does not match direct root resource %q", rootResourceType)
			}
			if len(plan.Operations) == 0 || plan.Operations[0].Kind != ir.PhysicalRootScanOp || plan.Operations[0].RootScan == nil {
				return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("semantic group requires a direct root resource scan")
			}
			rootScan := plan.Operations[0].RootScan
			if rootScan.CollectionBindKey == "" || plan.BindVars[rootScan.CollectionBindKey] != rootResourceType || rootScan.Variable == "" {
				return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("semantic group root collection does not match selected root resource %q", rootResourceType)
			}
			index, err := fhirschema.GeneratedIndex()
			if err != nil {
				return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("load generated FHIR schema: %w", err)
			}
			facts, err := index.ResolveRowPath(fhirschema.DefinitionName(rootResourceType), category.CodingPath)
			if err != nil || facts.ResourceType != fhirschema.DefinitionName(rootResourceType) ||
				facts.CanonicalPath != category.CodingPath || facts.FHIRType != "Coding" ||
				facts.Cardinality != fhirschema.RowCardinalityMany || facts.Shape != fhirschema.RowPathArray || facts.Reference {
				return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("semantic group path %q is not an executable generated repeated Coding path on %s", category.CodingPath, rootResourceType)
			}
			paths, err := index.RepeatedCodingPaths(fhirschema.DefinitionName(rootResourceType))
			if err != nil {
				return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("enumerate generated Coding paths: %w", err)
			}
			pathFound := false
			for _, path := range paths {
				if path == category.CodingPath {
					pathFound = true
					break
				}
			}
			if !pathFound {
				return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("semantic group path %q is not advertised by the generated FHIR schema", category.CodingPath)
			}
			segments, err := parseCodedGroupPath(category.CodingPath)
			if err != nil {
				return ir.PhysicalStageGroup{}, nil, nil, err
			}
			output, ok := outputs[key.OutputColumnID]
			if !ok {
				return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("semantic group output column ID %q is missing from step schema", key.OutputColumnID)
			}
			if output.Type != "" && output.Type != "INFER" && output.Type != "string" {
				return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("semantic group output %q must be a string column", output.ID)
			}
			physical.SemanticKey = &ir.PhysicalStageGroupSemanticKey{
				OutputColumn: output.Name, TupleVariable: allocateConstructionVariable(usedVariables, "group_semantic_tuple", stepIndex),
				LabelVariable:      allocateConstructionVariable(usedVariables, "group_semantic_label", stepIndex),
				SourceRootVariable: rootScan.Variable, SourceIdentityColumn: inputIdentity,
				RootCollectionBindKey: rootScan.CollectionBindKey, ResourceType: rootResourceType,
				CodingPath: category.CodingPath, PathSegments: segments, BindingID: category.BindingID,
				RuleVersion: category.RuleVersion, FamilyID: category.FamilyID, System: category.System, Version: category.Version,
			}
			metadataByOutputID[output.ID] = CompiledOutputColumn{
				ID: output.ID, Name: output.Name, Label: constructionFirstNonEmpty(output.Label, output.Name),
				Kind: string(expression.KindString), Cardinality: string(expression.RequiredOne),
				Nullable:     group.MissingKeyPolicy.Normalized() == recipe.ConstructionGroupMissingKeyGroup,
				SemanticPath: "construction_semantic_group:" + category.BindingID + ":" + category.FamilyID,
			}
			projectionByOutputID[output.ID] = ir.PhysicalProjection{Name: output.Name, Value: ir.PhysicalValue{Variable: physical.SemanticKey.LabelVariable}}
			continue
		}
		if key.FieldSource != nil {
			source := *key.FieldSource
			output, ok := outputs[key.OutputColumnID]
			if !ok {
				return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("Group fieldSource output column ID %q is missing from step schema", key.OutputColumnID)
			}
			if source.ValueKind == "CODING_IDENTITY" {
				if physical.SemanticKey != nil || physical.CodingKey != nil {
					return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("Group supports at most one coded identity key")
				}
				codingKey, column, projection, _, _, err := lowerConstructionGroupCodingKey(
					plan, source, groupFieldContext, output, group.MissingKeyPolicy, usedVariables, stepIndex, stage,
				)
				if err != nil {
					return ir.PhysicalStageGroup{}, nil, nil, err
				}
				physical.CodingKey = &codingKey
				metadataByOutputID[output.ID] = column
				projectionByOutputID[output.ID] = projection
				continue
			}
			inputColumn, inputProjection, fieldSource, err := lowerConstructionGroupScalarSource(
				plan, source, groupFieldContext, stepIndex, len(physical.Keys), usedVariables, stage,
			)
			if err != nil {
				return ir.PhysicalStageGroup{}, nil, nil, err
			}
			kind, scalar := tableReshapeScalarKind(inputColumn.Kind)
			if !scalar {
				return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("Group fieldSource %q is not a supported scalar", source.CanonicalPath)
			}
			if output.Type != "" && output.Type != "INFER" && !strings.EqualFold(output.Type, inputColumn.Kind) {
				return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("Group fieldSource output %q must preserve scalar type %q", output.ID, inputColumn.Kind)
			}
			physicalKey := ir.PhysicalStageGroupKey{
				InputColumn: inputColumn.Name, OutputColumn: output.Name,
				Variable: allocateConstructionVariable(usedVariables, "group_key", stepIndex), Kind: kind,
				InputInternal: true, FieldSource: fieldSource,
			}
			physical.Keys = append(physical.Keys, physicalKey)
			inputColumn.ID, inputColumn.Name = output.ID, output.Name
			inputColumn.Label = constructionFirstNonEmpty(output.Label, output.Name)
			inputColumn.Internal = false
			metadataByOutputID[output.ID] = inputColumn
			projectionByOutputID[output.ID] = inputProjection
			projectionByOutputID[output.ID] = ir.PhysicalProjection{Name: output.Name, Value: ir.PhysicalValue{Variable: physicalKey.Variable}}
			continue
		}
		inputColumn, ok := input[key.InputColumnID]
		if !ok || inputColumn.Internal {
			return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("group key input column ID %q is not public", key.InputColumnID)
		}
		if inputColumn.SemanticValueKind == CompiledSemanticValueCodingIdentityDisplay {
			return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("group key input column ID %q is a CODING_IDENTITY display label and cannot be reused as scalar identity", key.InputColumnID)
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
	rowValueSource := &constructionGroupRowValueSourceContext{
		Plan: plan, SourceContext: groupFieldContext, InputStageID: inputStageID,
		Stage: stage, FirstSourceIndex: len(group.Keys),
	}
	physicalRowValues, rowValueColumns, err := lowerConstructionRowValues(
		rowValues, input, outputs, group.ConstructionID, usedVariables, stepIndex, rowValueSource,
	)
	if err != nil {
		return ir.PhysicalStageGroup{}, nil, nil, err
	}
	physical.RowValues = physicalRowValues
	for index, rowValue := range physicalRowValues {
		column := rowValueColumns[index]
		metadataByOutputID[column.ID] = column
		projectionByOutputID[column.ID] = ir.PhysicalProjection{Name: column.Name, Value: ir.PhysicalValue{Variable: rowValue.Variable}}
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
			)
			if err != nil {
				return nil, nil, fmt.Errorf("row value fieldSource: %w", err)
			}
			_ = projection // The exact source projection is installed by the shared helper.
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
	Plan             *ir.PhysicalPlan
	SourceContext    *CompiledGroupFieldSourceContext
	InputStageID     string
	Stage            *ir.PhysicalConstructionStage
	FirstSourceIndex int
}

func constructionRowValueKind(kind string) (string, bool) {
	if scalar, ok := tableReshapeScalarKind(kind); ok {
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
