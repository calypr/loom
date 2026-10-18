package lower

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

const groupSourceOccurrenceKeyColumn = "__loom_group_source_occurrence_key"

func lowerConstructionGroupScalarSource(
	plan *ir.PhysicalPlan,
	source recipe.ConstructionGroupFieldSource,
	context *CompiledGroupFieldSourceContext,
	stepIndex, keyIndex int,
	usedVariables map[string]bool,
	stage *ir.PhysicalConstructionStage,
) (CompiledOutputColumn, ir.PhysicalProjection, *ir.PhysicalStageGroupFieldSource, error) {
	var normalizeErr error
	source, normalizeErr = normalizeConstructionCurrentGroupFieldSource(source)
	if normalizeErr != nil {
		return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, normalizeErr
	}
	if context == nil || source.SourceStageID != context.SourceStageID || source.SourceShapeID != context.SourceShapeID {
		return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, fmt.Errorf("Group fieldSource does not match the compiler-proven source stage and shape")
	}
	var matches []CompiledGroupFieldOwner
	for _, owner := range context.FieldOwners {
		if owner.Role == source.SourceRole && owner.OccurrenceID == source.OccurrenceID &&
			owner.OwnerResourceType == source.OwnerResourceType && owner.ResourceType == source.ResourceType &&
			owner.BoundarySelector == source.BoundarySelector {
			matches = append(matches, owner)
		}
	}
	if len(matches) != 1 {
		return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, fmt.Errorf("Group fieldSource role %s resolves to %d exact owners", source.SourceRole, len(matches))
	}
	owner := matches[0]
	selector, err := spec.ParseSelector(source.CanonicalPath)
	if err != nil || selector.Filter != nil || len(selector.Steps) == 0 {
		return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, fmt.Errorf("Group fieldSource path %q is not a canonical scalar selector", source.CanonicalPath)
	}
	for _, selectorStep := range selector.Steps {
		if selectorStep.Iterate || selectorStep.Index != nil {
			return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, fmt.Errorf("Group fieldSource path %q is not scalar", source.CanonicalPath)
		}
	}
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, fmt.Errorf("load generated FHIR schema: %w", err)
	}
	facts, err := index.ResolveRowPath(fhirschema.DefinitionName(source.ResourceType), source.CanonicalPath)
	if err != nil || facts.CanonicalPath != source.CanonicalPath || facts.Shape != fhirschema.RowPathScalar ||
		facts.Cardinality != fhirschema.RowCardinalityOne || facts.Reference {
		return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, fmt.Errorf("Group fieldSource path %q is not a generated scalar field on %s", source.CanonicalPath, source.ResourceType)
	}
	metadata, metadataOK := fhirschema.ResolveTerminalScalarMetadata(source.ResourceType, source.CanonicalPath)
	expressionKind, primitiveOK := groupSourcePrimitiveExpressionKind(metadata.Primitive)
	if !metadataOK || metadata.Repeated || !primitiveOK {
		return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, fmt.Errorf("Group fieldSource path %q has unsupported scalar primitive %q", source.CanonicalPath, metadata.Primitive)
	}
	_, supported := tableReshapeScalarKind(string(expressionKind))
	if !supported {
		return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, fmt.Errorf("Group fieldSource path %q has unsupported scalar primitive %q", source.CanonicalPath, metadata.Primitive)
	}
	sourceVariable := ""
	var unnest *ir.PhysicalUnnest
	for operationIndex := range plan.Operations {
		operation := &plan.Operations[operationIndex]
		if operation.Kind == ir.PhysicalUnnestOp && operation.Unnest != nil {
			if unnest != nil {
				return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, fmt.Errorf("Group fieldSource requires exactly one PhysicalUnnest")
			}
			unnest = operation.Unnest
		}
	}
	switch source.SourceRole {
	case "ROOT":
		root := constructionRootScan(plan)
		rootType := ""
		if root != nil {
			rootType, _ = plan.BindVars[root.CollectionBindKey].(string)
		}
		if root == nil || source.OwnerResourceType != rootType ||
			plan.RootOccurrenceID != "" && owner.OccurrenceID != plan.RootOccurrenceID {
			return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, fmt.Errorf("Group ROOT fieldSource does not match the source root occurrence")
		}
		sourceVariable = root.Variable
	case "OWNER":
		if unnest == nil || owner.OccurrenceID != unnest.Owner.OccurrenceID ||
			source.OwnerResourceType != unnest.Owner.ResourceType {
			return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, fmt.Errorf("Group OWNER fieldSource does not match the PhysicalUnnest owner")
		}
		sourceVariable = unnest.Owner.OwnerVariable
	case "ANCESTOR":
		if unnest == nil || source.OwnerResourceType != unnest.Owner.ResourceType || unnest.Expression.Extract == nil {
			return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, fmt.Errorf("Group ANCESTOR fieldSource has no exact repeated-boundary proof")
		}
		ancestorMatches := 0
		for _, ancestor := range unnest.Ancestors {
			if ancestor.StepIndex != owner.StepIndex || selectorPrefix(unnest.Expression.Extract.Selector, ancestor.StepIndex) != owner.BoundarySelector {
				continue
			}
			ancestorMatches++
			sourceVariable = ancestor.Variable
		}
		if ancestorMatches != 1 {
			return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, fmt.Errorf("Group ANCESTOR fieldSource boundary %q resolves to %d exact source variables", owner.BoundarySelector, ancestorMatches)
		}
	case "CURRENT_ITEM":
		if unnest == nil || owner.OccurrenceID != unnest.Owner.OccurrenceID ||
			source.OwnerResourceType != unnest.Owner.ResourceType {
			return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, fmt.Errorf("Group CURRENT_ITEM fieldSource does not match the PhysicalUnnest item")
		}
		sourceVariable = unnest.OutputVariable
	default:
		return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, fmt.Errorf("Group fieldSource role %q is unsupported", source.SourceRole)
	}
	if sourceVariable == "" {
		return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, fmt.Errorf("Group fieldSource has no exact source variable")
	}
	path := []string(nil)
	if source.SourceRole == "ROOT" || source.SourceRole == "OWNER" {
		path = []string{"payload"}
	}
	physicalExpression := ir.PhysicalExpression{
		Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull,
		Extract: &ir.PhysicalExtract{
			Source: ir.PhysicalValue{Variable: sourceVariable, Path: path}, ResourceType: source.ResourceType,
			Selector: selector, ExecutionMode: ir.PhysicalSelectorDirectScalar,
		},
	}
	name := fmt.Sprintf("__loom_group_field_%d_%d", stepIndex, keyIndex)
	column := CompiledOutputColumn{
		ID: name, Name: name, Label: name, Kind: string(expressionKind),
		Cardinality: "optional_one", Nullable: true, Internal: true,
		SemanticPath: "construction_group_field_source:" + source.SourceShapeID + ":" + source.SourceRole + ":" + source.CanonicalPath,
	}
	field := &ir.PhysicalStageGroupFieldSource{Kind: ir.PhysicalStageGroupFieldSourceCurrentStage, PhysicalStageGroupCurrentStageFieldSource: ir.PhysicalStageGroupCurrentStageFieldSource{
		SourceStageID: source.SourceStageID, SourceShapeID: source.SourceShapeID,
		Role: source.SourceRole, OccurrenceID: source.OccurrenceID, OwnerResourceType: source.OwnerResourceType,
		ResourceType: source.ResourceType,
		CandidateID:  source.CandidateID, CanonicalPath: source.CanonicalPath,
		BoundarySelector: owner.BoundarySelector, SourceVariable: sourceVariable,
	}}
	projection := ir.PhysicalProjection{Name: name, Hidden: true, Expression: &physicalExpression}
	if err := appendSourceGroupInputProjection(plan, projection); err != nil {
		return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, err
	}
	if stage != nil {
		stage.InputColumns = append(stage.InputColumns, toPhysicalStageColumns([]CompiledOutputColumn{column})...)
		stage.InputProjections = append(stage.InputProjections, ir.PhysicalProjection{
			Name: name, Hidden: true, Value: ir.PhysicalValue{Variable: stage.InputRowVariable, Path: []string{name}},
		})
	}
	return column, projection, field, nil
}

func normalizeConstructionCurrentGroupFieldSource(source recipe.ConstructionGroupFieldSource) (recipe.ConstructionGroupFieldSource, error) {
	if source.Input == nil {
		return source, nil
	}
	if source.Input.Kind != "CURRENT_STAGE" || source.Input.CurrentStage == nil || source.Input.RelatedOccurrence != nil {
		return recipe.ConstructionGroupFieldSource{}, fmt.Errorf("Group fieldSource requires a CURRENT_STAGE input")
	}
	current := source.Input.CurrentStage
	source.SourceRole, source.OccurrenceID = current.SourceRole, current.OccurrenceID
	source.OwnerResourceType, source.ResourceType = current.OwnerResourceType, current.ResourceType
	source.CandidateID, source.CanonicalPath = current.CandidateID, current.CanonicalPath
	source.BoundarySelector = current.BoundarySelector
	return source, nil
}

func groupSourcePrimitiveExpressionKind(primitive fhirschema.PrimitiveKind) (expression.ValueKind, bool) {
	switch primitive {
	case fhirschema.PrimitiveString:
		return expression.KindString, true
	case fhirschema.PrimitiveBoolean:
		return expression.KindBoolean, true
	case fhirschema.PrimitiveInteger:
		return expression.KindInteger, true
	case fhirschema.PrimitiveDecimal:
		return expression.KindDecimal, true
	case fhirschema.PrimitiveDate:
		return expression.KindDate, true
	case fhirschema.PrimitiveDateTime:
		return expression.KindDateTime, true
	default:
		return "", false
	}
}

func validateGroupCodingIdentitySource(
	plan *ir.PhysicalPlan,
	source recipe.ConstructionGroupFieldSource,
	context *CompiledGroupFieldSourceContext,
	inputStageID string,
) (*ir.PhysicalUnnest, error) {
	var normalizeErr error
	source, normalizeErr = normalizeConstructionCurrentGroupFieldSource(source)
	if normalizeErr != nil {
		return nil, normalizeErr
	}
	if context == nil || source.SourceStageID != inputStageID || source.SourceStageID != context.SourceStageID ||
		source.SourceShapeID != context.SourceShapeID || context.SourceExpansion == nil ||
		source.SourceRole != "CURRENT_ITEM" || source.ResourceType != "Coding" ||
		source.OwnerResourceType != context.SourceExpansion.OwnerResourceType ||
		source.OccurrenceID != context.SourceExpansion.OwnerOccurrenceID || source.CanonicalPath != context.SourceExpansion.Selector {
		return nil, fmt.Errorf("CODING_IDENTITY fieldSource does not match the exact current PhysicalUnnest item")
	}
	var found *ir.PhysicalUnnest
	for index := range plan.Operations {
		operation := &plan.Operations[index]
		if operation.Kind != ir.PhysicalUnnestOp || operation.Unnest == nil {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("CODING_IDENTITY requires exactly one PhysicalUnnest")
		}
		found = operation.Unnest
	}
	if found == nil || found.Expression.Extract == nil || found.Owner.OccurrenceID != source.OccurrenceID ||
		found.Owner.ResourceType != context.SourceExpansion.OwnerResourceType || found.OutputVariable == "" {
		return nil, fmt.Errorf("CODING_IDENTITY has no exact typed PhysicalUnnest proof")
	}
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		return nil, fmt.Errorf("load generated FHIR schema: %w", err)
	}
	facts, err := index.ResolveRowPath(fhirschema.DefinitionName(found.Owner.ResourceType), source.CanonicalPath)
	if err != nil || facts.CanonicalPath != source.CanonicalPath || facts.FHIRType != "Coding" ||
		facts.Cardinality != fhirschema.RowCardinalityMany || facts.Shape != fhirschema.RowPathArray || facts.Reference {
		return nil, fmt.Errorf("CODING_IDENTITY selector %q is not a generated repeated Coding path", source.CanonicalPath)
	}
	return found, nil
}

func lowerConstructionGroupCodingKey(
	plan *ir.PhysicalPlan,
	source recipe.ConstructionGroupFieldSource,
	context *CompiledGroupFieldSourceContext,
	output recipe.StageColumn,
	missingPolicy recipe.ConstructionGroupMissingKeyPolicy,
	usedVariables map[string]bool,
	stepIndex int,
	stage *ir.PhysicalConstructionStage,
) (ir.PhysicalStageGroupCodingKey, CompiledOutputColumn, ir.PhysicalProjection, CompiledOutputColumn, ir.PhysicalProjection, error) {
	unnest, err := validateGroupCodingIdentitySource(plan, source, context, source.SourceStageID)
	if err != nil {
		return ir.PhysicalStageGroupCodingKey{}, CompiledOutputColumn{}, ir.PhysicalProjection{}, CompiledOutputColumn{}, ir.PhysicalProjection{}, err
	}
	if output.ID == "" || output.Name == "" || (output.Type != "" && output.Type != "INFER" && !strings.EqualFold(output.Type, "string")) {
		return ir.PhysicalStageGroupCodingKey{}, CompiledOutputColumn{}, ir.PhysicalProjection{}, CompiledOutputColumn{}, ir.PhysicalProjection{}, fmt.Errorf("CODING_IDENTITY Group output must be a string column")
	}
	ordinal := unnest.Ordinality
	if ordinal == "" {
		ordinal = allocateConstructionVariable(usedVariables, "group_item_ordinal", stepIndex)
		unnest.Ordinality = ordinal
	}
	occurrenceKey, _, err := ensureGroupSourceOccurrenceIdentity(plan, *unnest)
	if err != nil {
		return ir.PhysicalStageGroupCodingKey{}, CompiledOutputColumn{}, ir.PhysicalProjection{}, CompiledOutputColumn{}, ir.PhysicalProjection{}, err
	}
	membershipColumn := fmt.Sprintf("__loom_group_coding_membership_%d", stepIndex)
	itemValue := func(path string) ir.PhysicalExpression {
		value := ir.PhysicalValue{Variable: unnest.OutputVariable, Path: []string{path}}
		return ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &value}
	}
	membership := ir.PhysicalExpression{
		Kind: ir.PhysicalObjectExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalPreserveNull,
		Object: &ir.PhysicalObject{Fields: []ir.PhysicalExpressionProjection{
			{Name: "system", Expression: itemValue("system")},
			{Name: "version", Expression: itemValue("version")},
			{Name: "code", Expression: itemValue("code")},
			{Name: "display", Expression: itemValue("display")},
		}},
	}
	inputColumn := CompiledOutputColumn{
		ID: membershipColumn, Name: membershipColumn, Label: membershipColumn,
		Kind: "object", Cardinality: "optional_one", Nullable: true, Internal: true,
		SemanticPath: "construction_group_coding_membership:" + source.SourceShapeID,
	}
	inputProjection := ir.PhysicalProjection{Name: membershipColumn, Hidden: true, Expression: &membership}
	if err := appendSourceGroupInputProjection(plan, inputProjection); err != nil {
		return ir.PhysicalStageGroupCodingKey{}, CompiledOutputColumn{}, ir.PhysicalProjection{}, CompiledOutputColumn{}, ir.PhysicalProjection{}, err
	}
	if stage != nil {
		stage.InputColumns = append(stage.InputColumns, toPhysicalStageColumns([]CompiledOutputColumn{inputColumn})...)
		stage.InputProjections = append(stage.InputProjections, ir.PhysicalProjection{
			Name: membershipColumn, Hidden: true, Value: ir.PhysicalValue{Variable: stage.InputRowVariable, Path: []string{membershipColumn}},
		})
		occurrenceColumn := CompiledOutputColumn{
			ID: occurrenceKey, Name: occurrenceKey, Label: occurrenceKey, Kind: "object",
			Cardinality: "required_one", Internal: true,
			SemanticPath: "construction_group_occurrence_identity:" + source.SourceShapeID,
		}
		stage.InputColumns = append(stage.InputColumns, toPhysicalStageColumns([]CompiledOutputColumn{occurrenceColumn})...)
		stage.InputProjections = append(stage.InputProjections, ir.PhysicalProjection{
			Name: occurrenceKey, Hidden: true, Value: ir.PhysicalValue{Variable: stage.InputRowVariable, Path: []string{occurrenceKey}},
		})
	}
	key := ir.PhysicalStageGroupCodingKey{
		OutputColumn:       output.Name,
		InputTupleVariable: allocateConstructionVariable(usedVariables, "group_coding_input_tuple", stepIndex),
		TupleVariable:      allocateConstructionVariable(usedVariables, "group_coding_tuple", stepIndex),
		LabelVariable:      allocateConstructionVariable(usedVariables, "group_coding_label", stepIndex),
		MembershipColumn:   membershipColumn, SourceStageID: source.SourceStageID,
		SourceShapeID: source.SourceShapeID, SourceOccurrenceKeyColumn: occurrenceKey,
		OwnerOccurrenceID: unnest.Owner.OccurrenceID, OwnerResourceType: unnest.Owner.ResourceType,
		CodingPath:   source.CanonicalPath,
		SourceUnnest: cloneGroupSourceUnnest(*unnest),
	}
	column := CompiledOutputColumn{
		ID: output.ID, Name: output.Name, Label: constructionFirstNonEmpty(output.Label, output.Name),
		SemanticValueKind: CompiledSemanticValueCodingIdentityDisplay,
		Kind:              "string", Cardinality: "required_one", Nullable: missingPolicy.Normalized() == recipe.ConstructionGroupMissingKeyGroup,
		SemanticPath: "construction_group_coding_identity:" + source.SourceShapeID + ":" + source.CanonicalPath,
	}
	outputProjection := ir.PhysicalProjection{Name: output.Name, Value: ir.PhysicalValue{Variable: key.LabelVariable}}
	return key, column, outputProjection, inputColumn, inputProjection, nil
}

func appendSourceGroupInputProjection(plan *ir.PhysicalPlan, projection ir.PhysicalProjection) error {
	if plan == nil {
		return fmt.Errorf("Group source projection requires a physical plan")
	}
	for index := range plan.Operations {
		operation := &plan.Operations[index]
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, existing := range operation.Return.Projections {
			if existing.Name == projection.Name {
				return fmt.Errorf("Group hidden source projection %q already exists", projection.Name)
			}
		}
		operation.Return.Projections = append(operation.Return.Projections, projection)
		return nil
	}
	return fmt.Errorf("Group source projection has no terminal physical RETURN")
}

func cloneGroupSourceUnnest(unnest ir.PhysicalUnnest) *ir.PhysicalUnnest {
	cloned := ir.ClonePhysicalPlan(ir.PhysicalPlan{Operations: []ir.PhysicalOperation{{Kind: ir.PhysicalUnnestOp, Unnest: &unnest}}})
	return cloned.Operations[0].Unnest
}

func ensureGroupSourceOccurrenceIdentity(plan *ir.PhysicalPlan, unnest ir.PhysicalUnnest) (string, ir.PhysicalProjection, error) {
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalReturnOp && operation.Return != nil {
			for _, projection := range operation.Return.Projections {
				if projection.Name == groupSourceOccurrenceKeyColumn {
					return groupSourceOccurrenceKeyColumn, projection, nil
				}
			}
		}
	}
	if unnest.Ordinality == "" {
		return "", ir.PhysicalProjection{}, fmt.Errorf("expanded Group source has no ordinal identity")
	}
	occurrenceID := unnest.Owner.OccurrenceID
	if occurrenceID == "" {
		return "", ir.PhysicalProjection{}, fmt.Errorf("expanded Group source has no owner occurrence identity")
	}
	occurrenceBind := "construction_group_source_occurrence"
	for suffix := 1; ; suffix++ {
		if _, exists := plan.BindVars[occurrenceBind]; !exists {
			break
		}
		occurrenceBind = fmt.Sprintf("construction_group_source_occurrence_%d", suffix)
	}
	plan.BindVars[occurrenceBind] = occurrenceID
	valueExpression := func(value ir.PhysicalValue) ir.PhysicalExpression {
		return ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &value}
	}
	fields := []ir.PhysicalExpressionProjection{
		{Name: "root_id", Expression: valueExpression(ir.PhysicalValue{Variable: unnest.Owner.RootVariable, Path: []string{"_id"}})},
		{Name: "occurrence_id", Expression: ir.PhysicalExpression{Kind: ir.PhysicalLiteralExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Literal: &ir.PhysicalLiteral{BindKey: occurrenceBind}}},
	}
	for index, step := range unnest.Owner.Route {
		fields = append(fields, ir.PhysicalExpressionProjection{Name: fmt.Sprintf("edge_%02d_id", index), Expression: valueExpression(ir.PhysicalValue{Variable: step.Traversal.EdgeVariable, Path: []string{"_id"}})})
	}
	fields = append(fields,
		ir.PhysicalExpressionProjection{Name: "owner_id", Expression: valueExpression(ir.PhysicalValue{Variable: unnest.Owner.OwnerVariable, Path: []string{"_id"}})},
		ir.PhysicalExpressionProjection{Name: "item_present", Expression: valueExpression(ir.PhysicalValue{Variable: unnest.HasItemVariable})},
		ir.PhysicalExpressionProjection{Name: "ordinal", Expression: valueExpression(ir.PhysicalValue{Variable: unnest.Ordinality})},
	)
	identity := ir.PhysicalExpression{Kind: ir.PhysicalObjectExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalPreserveNull, Object: &ir.PhysicalObject{Fields: fields}}
	projection := ir.PhysicalProjection{Name: groupSourceOccurrenceKeyColumn, Hidden: true, Expression: &identity}
	return groupSourceOccurrenceKeyColumn, projection, appendSourceGroupInputProjection(plan, projection)
}
