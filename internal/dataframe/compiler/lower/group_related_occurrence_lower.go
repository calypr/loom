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

const groupRelatedOccurrenceKeyColumn = "__loom_group_related_occurrence_key"

func lowerConstructionGroupRelatedScalarSource(
	plan *ir.PhysicalPlan,
	source recipe.ConstructionGroupFieldSource,
	context *CompiledGroupFieldSourceContext,
	stepIndex, sourceIndex int,
	usedVariables map[string]bool,
	stage *ir.PhysicalConstructionStage,
	adapterSlot **ir.PhysicalStageGroupRelatedOccurrenceAdapter,
	policy ir.PhysicalOptimizationPolicy,
) (CompiledOutputColumn, ir.PhysicalProjection, *ir.PhysicalStageGroupFieldSource, error) {
	if source.ValueKind != "SCALAR" {
		return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, fmt.Errorf("related Group source must be SCALAR")
	}
	bound, err := BindGroupRelatedOccurrenceSource(context, source)
	if err != nil {
		return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, err
	}
	if bound.SourceStageID != context.SourceStageID {
		return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, fmt.Errorf("related Group source does not bind the exact input stage")
	}
	related := bound.Input.RelatedOccurrence
	if related == nil {
		return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, fmt.Errorf("related Group source has no related occurrence binding")
	}
	if context.SourceExpansion != nil {
		return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, fmt.Errorf("connected Group route currently requires a direct root source anchor")
	}
	if context.SourceIdentityColumn != "_key" || related.AnchorOccurrenceID != context.RootOccurrenceID ||
		related.AnchorResourceType != context.RootResourceType {
		return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, fmt.Errorf("related Group route must start at the exact direct root occurrence")
	}
	selector, err := spec.ParseSelector(related.CanonicalPath)
	if err != nil || selector.Filter != nil || len(selector.Steps) == 0 {
		return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, fmt.Errorf("related Group path %q is not a canonical scalar selector", related.CanonicalPath)
	}
	for _, part := range selector.Steps {
		if part.Iterate || part.Index != nil {
			return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, fmt.Errorf("related Group path %q is not scalar", related.CanonicalPath)
		}
	}
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, fmt.Errorf("load generated FHIR schema: %w", err)
	}
	facts, err := index.ResolveRowPath(fhirschema.DefinitionName(related.TargetResourceType), related.CanonicalPath)
	if err != nil || facts.CanonicalPath != related.CanonicalPath || facts.Shape != fhirschema.RowPathScalar ||
		facts.Cardinality != fhirschema.RowCardinalityOne || facts.Reference {
		return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, fmt.Errorf("related Group path %q is not a generated scalar on %s", related.CanonicalPath, related.TargetResourceType)
	}
	metadata, ok := fhirschema.ResolveTerminalScalarMetadata(related.TargetResourceType, related.CanonicalPath)
	kind, supported := groupSourcePrimitiveExpressionKind(metadata.Primitive)
	if !ok || metadata.Repeated || !supported {
		return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, fmt.Errorf("related Group path %q has unsupported scalar primitive %q", related.CanonicalPath, metadata.Primitive)
	}
	if adapterSlot == nil {
		return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, fmt.Errorf("related Group source requires its Group-owned adapter slot")
	}
	adapter, err := ensureGroupRelatedOccurrenceAdapter(
		plan, bound, context, stage, stepIndex, usedVariables, adapterSlot, policy,
	)
	if err != nil {
		return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, err
	}
	inputColumn := fmt.Sprintf("__loom_group_field_%d_%d", stepIndex, sourceIndex)
	resultField := fmt.Sprintf("__loom_group_related_value_%d_%d", stepIndex, sourceIndex)
	for _, existing := range adapter.ScalarProjections {
		if existing.InputColumn == inputColumn || existing.ResultField == resultField {
			return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, fmt.Errorf("related Group scalar projection %q is duplicated", related.CanonicalPath)
		}
	}
	var sourceVariable string
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalRootScanOp && operation.RootScan != nil {
			sourceVariable = operation.RootScan.Variable
			break
		}
	}
	if sourceVariable == "" {
		return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, fmt.Errorf("related Group route has no typed root source")
	}
	extract := ir.PhysicalExpression{
		Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull,
		Extract: &ir.PhysicalExtract{
			Source:       ir.PhysicalValue{Variable: adapter.RelatedRecords.RelatedRecords.Sort.Variable, Path: []string{"payload"}},
			ResourceType: related.TargetResourceType, Selector: selector,
			ExecutionMode: ir.PhysicalSelectorDirectScalar,
		},
	}
	if adapter.RelatedRecords.RelatedRecords.Return.Kind != ir.PhysicalObjectExpression || adapter.RelatedRecords.RelatedRecords.Return.Object == nil {
		return CompiledOutputColumn{}, ir.PhysicalProjection{}, nil, fmt.Errorf("related Group route has no typed target projection")
	}
	adapter.RelatedRecords.RelatedRecords.Return.Object.Fields = append(
		adapter.RelatedRecords.RelatedRecords.Return.Object.Fields,
		ir.PhysicalExpressionProjection{Name: resultField, Expression: extract},
	)
	adapter.ScalarProjections = append(adapter.ScalarProjections, ir.PhysicalStageGroupRelatedProjection{
		InputColumn: inputColumn, ResultField: resultField, OwnerResourceType: related.TargetResourceType,
		ValueResourceType: related.ResourceType,
		CandidateID:       related.CandidateID, CanonicalPath: related.CanonicalPath,
	})
	column := CompiledOutputColumn{
		ID: inputColumn, Name: inputColumn, Label: inputColumn, Kind: string(kind),
		Cardinality: string(expression.OptionalOne), Nullable: true, Internal: true,
		SemanticPath: "construction_group_related_field_source:" + bound.SourceShapeID + ":" + related.CanonicalPath,
	}
	projection := ir.PhysicalProjection{
		Name: inputColumn, Hidden: true,
		Value: ir.PhysicalValue{Variable: adapter.RelatedRecords.ItemVariable, Path: []string{resultField}},
	}
	if stage != nil {
		stage.InputColumns = append(stage.InputColumns, toPhysicalStageColumns([]CompiledOutputColumn{column})...)
		stage.InputProjections = append(stage.InputProjections, projection)
	}
	physicalSource := &ir.PhysicalStageGroupFieldSource{
		Kind: ir.PhysicalStageGroupFieldSourceRelatedOccurrence,
		RelatedOccurrence: &ir.PhysicalStageGroupRelatedOccurrenceFieldSource{
			RouteID: related.RouteID, SourceStageID: bound.SourceStageID, SourceShapeID: bound.SourceShapeID,
			AnchorOccurrenceID: related.AnchorOccurrenceID, AnchorNodeID: related.AnchorNodeID,
			AnchorResourceType: related.AnchorResourceType, Route: physicalGroupRelatedRoute(related.Route),
			TargetOccurrenceID: related.TargetOccurrenceID, TargetNodeID: related.TargetNodeID,
			TargetResourceType: related.TargetResourceType, ResourceType: related.ResourceType,
			CandidateID: related.CandidateID, CanonicalPath: related.CanonicalPath,
			EmptyPolicy: string(related.EmptyPolicy), Multiplicity: related.Multiplicity, ProjectionColumn: inputColumn,
		},
	}
	return column, projection, physicalSource, nil
}

func lowerConstructionGroupRelatedCodingKey(
	plan *ir.PhysicalPlan,
	source recipe.ConstructionGroupFieldSource,
	context *CompiledGroupFieldSourceContext,
	output recipe.StageColumn,
	missingPolicy recipe.ConstructionGroupMissingKeyPolicy,
	usedVariables map[string]bool,
	stepIndex, sourceIndex int,
	stage *ir.PhysicalConstructionStage,
	adapterSlot **ir.PhysicalStageGroupRelatedOccurrenceAdapter,
	policy ir.PhysicalOptimizationPolicy,
) (ir.PhysicalStageGroupCodingKey, CompiledOutputColumn, ir.PhysicalProjection, CompiledOutputColumn, ir.PhysicalProjection, error) {
	fail := func(err error) (ir.PhysicalStageGroupCodingKey, CompiledOutputColumn, ir.PhysicalProjection, CompiledOutputColumn, ir.PhysicalProjection, error) {
		return ir.PhysicalStageGroupCodingKey{}, CompiledOutputColumn{}, ir.PhysicalProjection{}, CompiledOutputColumn{}, ir.PhysicalProjection{}, err
	}
	if source.ValueKind != "CODING_IDENTITY" || output.ID == "" || output.Name == "" ||
		(output.Type != "" && output.Type != "INFER" && !strings.EqualFold(output.Type, "string")) {
		return fail(fmt.Errorf("related CODING_IDENTITY Group output must be a string column"))
	}
	bound, err := BindGroupRelatedOccurrenceSource(context, source)
	if err != nil {
		return fail(err)
	}
	if bound.Input == nil || bound.Input.RelatedOccurrence == nil {
		return fail(fmt.Errorf("related Coding source has no related occurrence binding"))
	}
	related := bound.Input.RelatedOccurrence
	if related.ResourceType != "Coding" || related.CodingSelector == "" ||
		related.OrdinalSemantics != recipe.ConstructionGroupRelatedOrdinalSemanticsZeroBasedPosition {
		return fail(fmt.Errorf("related CODING_IDENTITY source has incomplete selected Coding facts"))
	}
	selector, err := spec.ParseSelector(related.CodingSelector)
	if err != nil || selector.Filter != nil || len(selector.Steps) == 0 {
		return fail(fmt.Errorf("related Coding selector %q is not canonical", related.CodingSelector))
	}
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		return fail(fmt.Errorf("load generated FHIR schema: %w", err))
	}
	facts, err := index.ResolveRowPath(fhirschema.DefinitionName(related.TargetResourceType), related.CodingSelector)
	if err != nil || facts.CanonicalPath != related.CodingSelector || facts.FHIRType != "Coding" ||
		facts.Shape != fhirschema.RowPathArray || facts.Cardinality != fhirschema.RowCardinalityMany || facts.Reference {
		return fail(fmt.Errorf("related selector %q is not a generated repeated Coding path on %s", related.CodingSelector, related.TargetResourceType))
	}
	paths, err := index.RepeatedCodingPaths(fhirschema.DefinitionName(related.TargetResourceType))
	if err != nil {
		return fail(fmt.Errorf("enumerate generated Coding paths: %w", err))
	}
	found := false
	for _, path := range paths {
		if path == related.CodingSelector {
			found = true
			break
		}
	}
	if !found {
		return fail(fmt.Errorf("related selector %q is not advertised as a repeated Coding path", related.CodingSelector))
	}
	adapter, err := ensureGroupRelatedOccurrenceAdapter(plan, bound, context, stage, stepIndex, usedVariables, adapterSlot, policy)
	if err != nil {
		return fail(err)
	}
	if adapter.CodingSelector != "" {
		return fail(fmt.Errorf("Group supports one related Coding identity source"))
	}
	codingArrayColumn := fmt.Sprintf("__loom_group_related_codings_%d_%d", stepIndex, sourceIndex)
	membershipColumn := fmt.Sprintf("__loom_group_related_coding_membership_%d", stepIndex)
	arrayProjection := ir.PhysicalExpression{
		Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality,
		NullBehavior: ir.PhysicalEmptyOnNull,
		Extract: &ir.PhysicalExtract{
			Source:       ir.PhysicalValue{Variable: adapter.RelatedRecords.RelatedRecords.Sort.Variable, Path: []string{"payload"}},
			ResourceType: related.TargetResourceType, Selector: selector,
			ExecutionMode: selectorExecutionMode(related.TargetResourceType, selector),
		},
	}
	if adapter.RelatedRecords.RelatedRecords.Return.Kind != ir.PhysicalObjectExpression || adapter.RelatedRecords.RelatedRecords.Return.Object == nil {
		return fail(fmt.Errorf("related Group route has no typed target projection"))
	}
	adapter.RelatedRecords.RelatedRecords.Return.Object.Fields = append(
		adapter.RelatedRecords.RelatedRecords.Return.Object.Fields,
		ir.PhysicalExpressionProjection{Name: codingArrayColumn, Expression: arrayProjection},
	)
	codingEmptyPolicy, ok := lowerGroupCodingEmptyPolicy(related.CodingEmptyPolicy)
	if !ok {
		return fail(fmt.Errorf("related Coding empty policy %q is unsupported", related.CodingEmptyPolicy))
	}
	ordinality := allocateConstructionVariable(usedVariables, "group_related_coding_ordinal", stepIndex)
	codingItem := allocateConstructionVariable(usedVariables, "group_related_coding_item", stepIndex)
	codingHasItem := allocateConstructionVariable(usedVariables, "group_related_coding_has_item", stepIndex)
	codingUnnest := &ir.PhysicalUnnest{
		OutputVariable: codingItem, HasItemVariable: codingHasItem, Ordinality: ordinality,
		EmptyPolicy: codingEmptyPolicy,
		Expression: ir.PhysicalExpression{
			Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalArrayCardinality,
			NullBehavior: ir.PhysicalEmptyOnNull,
			Value:        &ir.PhysicalValue{Variable: adapter.RelatedRecords.ItemVariable, Path: []string{codingArrayColumn}},
		},
	}
	adapter.CodingSelector, adapter.CodingArrayColumn = related.CodingSelector, codingArrayColumn
	adapter.CodingEmptyPolicy, adapter.OrdinalSemantics = codingEmptyPolicy, related.OrdinalSemantics
	adapter.CodingUnnest = codingUnnest
	adapter.CodingMembershipColumn = membershipColumn
	adapter.OccurrenceKeyColumn = groupRelatedOccurrenceKeyColumn
	valueProjection := func(variable string, path ...string) ir.PhysicalExpression {
		value := ir.PhysicalValue{Variable: variable, Path: path}
		return ir.PhysicalExpression{
			Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalPreserveNull, Value: &value,
		}
	}
	membershipProjection := ir.PhysicalProjection{
		Name: membershipColumn, Hidden: true,
		Expression: &ir.PhysicalExpression{
			Kind: ir.PhysicalObjectExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalPreserveNull,
			Object: &ir.PhysicalObject{Fields: []ir.PhysicalExpressionProjection{
				{Name: "system", Expression: valueProjection(codingUnnest.OutputVariable, "system")},
				{Name: "version", Expression: valueProjection(codingUnnest.OutputVariable, "version")},
				{Name: "code", Expression: valueProjection(codingUnnest.OutputVariable, "code")},
				{Name: "display", Expression: valueProjection(codingUnnest.OutputVariable, "display")},
			}},
		},
	}
	occurrenceProjection := ir.PhysicalProjection{
		Name: adapter.OccurrenceKeyColumn, Hidden: true,
		Expression: &ir.PhysicalExpression{
			Kind: ir.PhysicalObjectExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalPreserveNull,
			Object: &ir.PhysicalObject{Fields: []ir.PhysicalExpressionProjection{
				{Name: "root_id", Expression: valueProjection(stage.InputRowVariable, "_key")},
				{Name: "terminal_id", Expression: valueProjection(adapter.RelatedRecords.ItemVariable, "terminal_id")},
				{Name: "item_present", Expression: valueProjection(codingUnnest.HasItemVariable)},
				{Name: "ordinal", Expression: valueProjection(codingUnnest.Ordinality)},
			}},
		},
	}
	membershipInputColumn := CompiledOutputColumn{
		ID: membershipColumn, Name: membershipColumn, Label: membershipColumn,
		Kind: "object", Cardinality: string(expression.OptionalOne), Nullable: true, Internal: true,
		SemanticPath: "construction_group_related_coding_membership:" + bound.SourceShapeID,
	}
	if stage != nil {
		stage.InputColumns = append(stage.InputColumns, toPhysicalStageColumns([]CompiledOutputColumn{membershipInputColumn})...)
		stage.InputProjections = append(stage.InputProjections, membershipProjection)
		foundOccurrenceProjection := false
		for index := range stage.InputProjections {
			if stage.InputProjections[index].Name == adapter.OccurrenceKeyColumn {
				stage.InputProjections[index] = occurrenceProjection
				foundOccurrenceProjection = true
				break
			}
		}
		if !foundOccurrenceProjection {
			return fail(fmt.Errorf("related Group occurrence projection is missing from its owning stage"))
		}
	}
	key := ir.PhysicalStageGroupCodingKey{
		OutputColumn:       output.Name,
		InputTupleVariable: allocateConstructionVariable(usedVariables, "group_coding_input_tuple", stepIndex),
		TupleVariable:      allocateConstructionVariable(usedVariables, "group_coding_tuple", stepIndex),
		LabelVariable:      allocateConstructionVariable(usedVariables, "group_coding_label", stepIndex),
		MembershipColumn:   membershipColumn, SourceStageID: bound.SourceStageID,
		SourceShapeID: bound.SourceShapeID, SourceOccurrenceKeyColumn: adapter.OccurrenceKeyColumn,
		OwnerOccurrenceID: related.TargetOccurrenceID, OwnerResourceType: related.TargetResourceType,
		CodingPath: related.CodingSelector,
		RelatedSource: &ir.PhysicalStageGroupRelatedOccurrenceFieldSource{
			RouteID: related.RouteID, SourceStageID: bound.SourceStageID, SourceShapeID: bound.SourceShapeID,
			AnchorOccurrenceID: related.AnchorOccurrenceID, AnchorNodeID: related.AnchorNodeID,
			AnchorResourceType: related.AnchorResourceType, Route: physicalGroupRelatedRoute(related.Route),
			TargetOccurrenceID: related.TargetOccurrenceID, TargetNodeID: related.TargetNodeID,
			TargetResourceType: related.TargetResourceType, ResourceType: related.ResourceType,
			EmptyPolicy: string(related.EmptyPolicy), Multiplicity: related.Multiplicity,
			CodingSelector: related.CodingSelector, CodingEmptyPolicy: string(related.CodingEmptyPolicy),
			OrdinalSemantics: related.OrdinalSemantics, MembershipColumn: membershipColumn,
		},
	}
	column := CompiledOutputColumn{
		ID: output.ID, Name: output.Name, Label: constructionFirstNonEmpty(output.Label, output.Name),
		SemanticValueKind: CompiledSemanticValueCodingIdentityDisplay,
		Kind:              "string", Cardinality: string(expression.RequiredOne),
		Nullable:     missingPolicy.Normalized() == recipe.ConstructionGroupMissingKeyGroup,
		SemanticPath: "construction_group_related_coding_identity:" + bound.SourceShapeID + ":" + related.CodingSelector,
	}
	projection := ir.PhysicalProjection{Name: output.Name, Value: ir.PhysicalValue{Variable: key.LabelVariable}}
	return key, column, projection, membershipInputColumn, membershipProjection, nil
}

func lowerGroupCodingEmptyPolicy(policy recipe.ConstructionGroupCodingEmptyPolicy) (ir.PhysicalUnnestEmptyPolicy, bool) {
	switch policy {
	case recipe.ConstructionGroupCodingEmptyPolicyGroup:
		return ir.PhysicalUnnestPreserveParent, true
	case recipe.ConstructionGroupCodingEmptyPolicyExclude:
		return ir.PhysicalUnnestExclude, true
	case recipe.ConstructionGroupCodingEmptyPolicyError:
		return ir.PhysicalUnnestError, true
	default:
		return "", false
	}
}

func ensureGroupRelatedOccurrenceAdapter(
	plan *ir.PhysicalPlan,
	source recipe.ConstructionGroupFieldSource,
	context *CompiledGroupFieldSourceContext,
	stage *ir.PhysicalConstructionStage,
	stepIndex int,
	usedVariables map[string]bool,
	adapterSlot **ir.PhysicalStageGroupRelatedOccurrenceAdapter,
	policy ir.PhysicalOptimizationPolicy,
) (*ir.PhysicalStageGroupRelatedOccurrenceAdapter, error) {
	if *adapterSlot != nil {
		adapter := *adapterSlot
		if adapter.RouteID != source.Input.RelatedOccurrence.RouteID ||
			adapter.RelatedRecords.EmptyPolicy != ir.PhysicalUnnestEmptyPolicy(source.Input.RelatedOccurrence.EmptyPolicy) {
			return nil, fmt.Errorf("Group fieldSources select independent related routes or route policies")
		}
		return adapter, nil
	}
	if stage == nil || stage.InputRowVariable == "" {
		return nil, fmt.Errorf("related Group adapter requires its owning typed Group stage")
	}
	related := source.Input.RelatedOccurrence
	if related == nil || related.TargetOccurrenceID == "" {
		return nil, fmt.Errorf("related Group adapter requires a resolved route")
	}
	root := constructionRootScan(plan)
	if root == nil || root.CollectionBindKey == "" || plan.BindVars[root.CollectionBindKey] != context.RootResourceType {
		return nil, fmt.Errorf("related Group adapter requires the exact source root scan")
	}
	rootKey := CompiledOutputColumn{
		ID: "_key", Name: "_key", Kind: string(expression.KindString), Cardinality: string(expression.RequiredOne),
		Internal: true, Identity: true, RootContributorResourceType: context.RootResourceType,
	}
	input := map[string]CompiledOutputColumn{"_key": rootKey}
	if context.SourceIdentityColumn != "_key" {
		return nil, fmt.Errorf("related Group adapter requires direct root-key row identity")
	}
	if err := ensureGroupRelatedRootKeyInput(plan, stage, root); err != nil {
		return nil, err
	}
	outputID := fmt.Sprintf("__loom_group_related_target_%d", stepIndex)
	relatedExpand := recipe.ConstructionRelatedExpand{
		AnchorColumnID: "_key", TargetNodeID: related.TargetNodeID, TargetResourceType: related.TargetResourceType,
		ContributorPolicy: "ALL_MATCHES", EmptyPolicy: related.EmptyPolicy, RelatedRecordColumnID: outputID,
		Route: make([]recipe.ConstructionRelatedRouteStep, 0, len(related.Route)),
	}
	for _, hop := range related.Route {
		relatedExpand.Route = append(relatedExpand.Route, recipe.ConstructionRelatedRouteStep{
			EdgeID: hop.EdgeID, FromNodeID: hop.FromNodeID, ToNodeID: hop.ToNodeID,
			FromResourceType: hop.FromResourceType, ToResourceType: hop.ToResourceType,
			Relationship: hop.Relationship, StorageDirection: hop.StorageDirection, MatchMode: hop.MatchMode,
		})
	}
	outputs := map[string]recipe.StageColumn{outputID: {ID: outputID, Name: outputID, Type: "string", Nullable: related.EmptyPolicy == recipe.ExpansionPreserveParent}}
	syntheticStep := recipe.ConstructionStep{ID: fmt.Sprintf("group_related_%d", stepIndex)}
	physical, _, _, err := lowerConstructionRelatedExpand(
		plan, syntheticStep, relatedExpand, input, outputs, stage.InputRowVariable,
		context.SourceIdentityColumn, context.RootResourceType,
		policy, usedVariables, stepIndex,
	)
	if err != nil {
		return nil, fmt.Errorf("lower Group related route: %w", err)
	}
	physical.RouteID = related.RouteID
	physical.Multiplicity = related.Multiplicity
	physical.AnchorOccurrenceID, physical.AnchorNodeID, physical.AnchorResourceType =
		related.AnchorOccurrenceID, related.AnchorNodeID, related.AnchorResourceType
	physical.Route = physicalGroupRelatedPhysicalRoute(related.Route)
	adapter := &ir.PhysicalStageGroupRelatedOccurrenceAdapter{
		RouteID: related.RouteID, RelatedRecords: physical,
		OccurrenceKeyColumn:  groupRelatedOccurrenceKeyColumn,
		RouteHasItemVariable: allocateConstructionVariable(usedVariables, "group_related_has_item", stepIndex),
	}
	nullOrdinalBind := nextTableReshapeBindKey(plan.BindVars, "group_related_null_ordinal")
	plan.BindVars[nullOrdinalBind] = nil
	occurrenceColumn := CompiledOutputColumn{
		ID: adapter.OccurrenceKeyColumn, Name: adapter.OccurrenceKeyColumn, Label: adapter.OccurrenceKeyColumn,
		Kind: "object", Cardinality: string(expression.RequiredOne), Internal: true,
		SemanticPath: "construction_group_related_occurrence:" + source.SourceShapeID,
	}
	occurrenceProjection := ir.PhysicalProjection{
		Name: adapter.OccurrenceKeyColumn, Hidden: true,
		Expression: &ir.PhysicalExpression{
			Kind: ir.PhysicalObjectExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalPreserveNull,
			Object: &ir.PhysicalObject{Fields: []ir.PhysicalExpressionProjection{
				{Name: "root_id", Expression: groupRelatedValueExpression(stage.InputRowVariable, "_key")},
				{Name: "terminal_id", Expression: groupRelatedValueExpression(adapter.RelatedRecords.ItemVariable, "terminal_id")},
				{Name: "item_present", Expression: groupRelatedValueExpression(adapter.RouteHasItemVariable)},
				{Name: "ordinal", Expression: ir.PhysicalExpression{
					Kind: ir.PhysicalLiteralExpression, Cardinality: ir.PhysicalScalarCardinality,
					NullBehavior: ir.PhysicalPreserveNull, Literal: &ir.PhysicalLiteral{BindKey: nullOrdinalBind},
				}},
			}},
		},
	}
	stage.InputColumns = append(stage.InputColumns, toPhysicalStageColumns([]CompiledOutputColumn{occurrenceColumn})...)
	stage.InputProjections = append(stage.InputProjections, occurrenceProjection)
	*adapterSlot = adapter
	return adapter, nil
}

// ensureGroupRelatedRootKeyInput carries the proven source root key through
// the typed Group input row. A streamed source Group otherwise prunes an
// unused source RETURN to {}, leaving the Group-owned route adapter without
// the anchor it needs to re-run the selected route.
func ensureGroupRelatedRootKeyInput(
	plan *ir.PhysicalPlan,
	stage *ir.PhysicalConstructionStage,
	root *ir.PhysicalRootScan,
) error {
	if plan == nil || stage == nil || root == nil || root.Variable == "" || stage.InputRowVariable == "" {
		return fmt.Errorf("related Group adapter requires a typed root-key input")
	}
	rootProjection := ir.PhysicalProjection{
		Name: "_key", Hidden: true,
		Value: ir.PhysicalValue{Variable: root.Variable, Path: []string{"_key"}},
	}
	foundReturn := false
	for index := range plan.Operations {
		operation := &plan.Operations[index]
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		foundReturn = true
		found := false
		for projectionIndex := range operation.Return.Projections {
			projection := &operation.Return.Projections[projectionIndex]
			if projection.Name != "_key" {
				continue
			}
			if projection.Expression != nil || projection.Value.Variable != root.Variable ||
				len(projection.Value.Path) != 1 || projection.Value.Path[0] != "_key" {
				return fmt.Errorf("related Group source _key projection is not the exact root identity")
			}
			projection.Hidden = true
			found = true
			break
		}
		if !found {
			operation.Return.Projections = append(operation.Return.Projections, rootProjection)
		}
		break
	}
	if !foundReturn {
		return fmt.Errorf("related Group source has no terminal physical RETURN for its root identity")
	}

	rootColumn := CompiledOutputColumn{
		ID: "_key", Name: "_key", Kind: string(expression.KindString),
		Cardinality: string(expression.RequiredOne), Internal: true, Identity: true,
		RootContributorResourceType: plan.Source.ResourceType,
	}
	columnFound := false
	for _, column := range stage.InputColumns {
		if column.Name == "_key" {
			if column.Kind != string(expression.KindString) || column.Cardinality != string(expression.RequiredOne) || !column.Internal {
				return fmt.Errorf("related Group input _key is not a typed internal root identity")
			}
			columnFound = true
			break
		}
	}
	if !columnFound {
		stage.InputColumns = append(stage.InputColumns, toPhysicalStageColumns([]CompiledOutputColumn{rootColumn})...)
	}
	projectionFound := false
	for _, projection := range stage.InputProjections {
		if projection.Name != "_key" {
			continue
		}
		if projection.Expression != nil || projection.Value.Variable != stage.InputRowVariable ||
			len(projection.Value.Path) != 1 || projection.Value.Path[0] != "_key" {
			return fmt.Errorf("related Group typed input _key projection is not the exact source identity")
		}
		projectionFound = true
		break
	}
	if !projectionFound {
		stage.InputProjections = append(stage.InputProjections, ir.PhysicalProjection{
			Name: "_key", Hidden: true,
			Value: ir.PhysicalValue{Variable: stage.InputRowVariable, Path: []string{"_key"}},
		})
	}
	return nil
}

func groupRelatedValueExpression(variable string, path ...string) ir.PhysicalExpression {
	value := ir.PhysicalValue{Variable: variable, Path: append([]string(nil), path...)}
	return ir.PhysicalExpression{
		Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull, Value: &value,
	}
}

func physicalGroupRelatedRoute(route []recipe.ConstructionGroupRelatedRouteStep) []ir.PhysicalStageGroupRelatedRouteStep {
	result := make([]ir.PhysicalStageGroupRelatedRouteStep, 0, len(route))
	for _, hop := range route {
		result = append(result, ir.PhysicalStageGroupRelatedRouteStep{
			StepIndex: hop.StepIndex, FromOccurrenceID: hop.FromOccurrenceID, FromNodeID: hop.FromNodeID,
			FromResourceType: hop.FromResourceType, EdgeID: hop.EdgeID, Relationship: hop.Relationship,
			StorageDirection: hop.StorageDirection, MatchMode: hop.MatchMode, ToOccurrenceID: hop.ToOccurrenceID,
			ToNodeID: hop.ToNodeID, ToResourceType: hop.ToResourceType,
		})
	}
	return result
}

func physicalGroupRelatedPhysicalRoute(route []recipe.ConstructionGroupRelatedRouteStep) []ir.PhysicalStageRelatedRouteStep {
	result := make([]ir.PhysicalStageRelatedRouteStep, 0, len(route))
	for _, hop := range route {
		result = append(result, ir.PhysicalStageRelatedRouteStep{
			StepIndex: hop.StepIndex, FromOccurrenceID: hop.FromOccurrenceID, EdgeID: hop.EdgeID,
			FromNodeID: hop.FromNodeID, ToNodeID: hop.ToNodeID, ToOccurrenceID: hop.ToOccurrenceID,
			FromResourceType: hop.FromResourceType, ToResourceType: hop.ToResourceType,
			Relationship: hop.Relationship, StorageDirection: hop.StorageDirection, MatchMode: hop.MatchMode,
		})
	}
	return result
}

func canonicalGroupRelatedPath(selector spec.Selector) string {
	parts := make([]string, 0, len(selector.Steps))
	for _, step := range selector.Steps {
		part := step.Field
		if step.Iterate {
			part += "[]"
		} else if step.Index != nil {
			part += fmt.Sprintf("[%d]", *step.Index)
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ".")
}
