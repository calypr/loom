package lower

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func BindGroupRelatedOccurrenceSource(
	context *CompiledGroupFieldSourceContext,
	source recipe.ConstructionGroupFieldSource,
) (recipe.ConstructionGroupFieldSource, error) {
	if context == nil || source.Kind != "BINDING" || source.ChoiceID != "" || context.SourceStageID == "" || context.SourceShapeID == "" {
		return recipe.ConstructionGroupFieldSource{}, fmt.Errorf("related Group binding requires the exact compiler source context and a resolved BINDING")
	}
	input, err := recipeGroupFieldSourceInput(source)
	if err != nil || input.Kind != "RELATED_OCCURRENCE" || input.RelatedOccurrence == nil {
		return recipe.ConstructionGroupFieldSource{}, fmt.Errorf("related Group binding requires a RELATED_OCCURRENCE input")
	}
	if source.ValueKind != "SCALAR" && source.ValueKind != "CODING_IDENTITY" {
		return recipe.ConstructionGroupFieldSource{}, fmt.Errorf("related Group binding has unsupported valueKind %q", source.ValueKind)
	}
	related, shapeID, err := ResolveGroupRelatedOccurrenceSource(context, source.ValueKind, *input.RelatedOccurrence)
	if err != nil {
		return recipe.ConstructionGroupFieldSource{}, err
	}
	if source.SourceStageID != "" && source.SourceStageID != context.SourceStageID {
		return recipe.ConstructionGroupFieldSource{}, fmt.Errorf("related Group binding names a different source stage")
	}
	if source.SourceShapeID != "" && source.SourceShapeID != shapeID {
		return recipe.ConstructionGroupFieldSource{}, fmt.Errorf("related Group binding source shape does not match the compiler-derived route shape")
	}
	source.SourceStageID, source.SourceShapeID = context.SourceStageID, shapeID
	source.Input = &recipe.ConstructionGroupFieldSourceInput{Kind: "RELATED_OCCURRENCE", RelatedOccurrence: &related}
	source.SourceRole, source.OccurrenceID, source.OwnerResourceType = "", "", ""
	source.ResourceType, source.CandidateID, source.CanonicalPath, source.BoundarySelector = "", "", "", ""
	return source, nil
}

func ResolveGroupRelatedOccurrenceSource(
	context *CompiledGroupFieldSourceContext,
	valueKind string,
	source recipe.ConstructionGroupRelatedOccurrenceFieldSource,
) (recipe.ConstructionGroupRelatedOccurrenceFieldSource, string, error) {
	if context == nil || context.SourceStageID == "" || context.SourceShapeID == "" {
		return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", fmt.Errorf("related Group route requires an exact compiler source context")
	}
	source.Route = append([]recipe.ConstructionGroupRelatedRouteStep(nil), source.Route...)
	anchorOccurrence, anchorResource := context.RootOccurrenceID, context.RootResourceType
	if context.SourceExpansion != nil {
		anchorOccurrence, anchorResource = context.SourceExpansion.OwnerOccurrenceID, context.SourceExpansion.OwnerResourceType
	}
	if anchorOccurrence == "" || anchorResource == "" {
		return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", fmt.Errorf("related Group route anchor is missing its compiler-issued occurrence/resource identity")
	}
	if source.AnchorOccurrenceID == "" {
		source.AnchorOccurrenceID = anchorOccurrence
	}
	if source.AnchorOccurrenceID != anchorOccurrence || source.AnchorNodeID == "" || source.AnchorResourceType != anchorResource {
		return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", fmt.Errorf("related Group route anchor is not the exact current source occurrence")
	}
	if len(source.Route) == 0 {
		return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", fmt.Errorf("related Group route is empty")
	}
	fromOccurrence, fromNode, fromResource := source.AnchorOccurrenceID, source.AnchorNodeID, source.AnchorResourceType
	for index := range source.Route {
		hop := &source.Route[index]
		if hop.StepIndex != index || hop.FromNodeID != fromNode || hop.FromResourceType != fromResource ||
			!relatedGroupRouteStepComplete(*hop) {
			return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", fmt.Errorf("related Group route step %d is incomplete or discontinuous", index)
		}
		if hop.FromOccurrenceID == "" {
			hop.FromOccurrenceID = fromOccurrence
		}
		if hop.FromOccurrenceID != fromOccurrence {
			return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", fmt.Errorf("related Group route step %d has a different source occurrence", index)
		}
		wantOccurrence := relatedGroupOccurrenceID(context.SourceStageID, context.SourceShapeID, index, fromOccurrence, *hop)
		if hop.ToOccurrenceID != "" && hop.ToOccurrenceID != wantOccurrence {
			return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", fmt.Errorf("related Group route step %d target occurrence does not match compiler derivation", index)
		}
		hop.ToOccurrenceID = wantOccurrence
		fromOccurrence, fromNode, fromResource = hop.ToOccurrenceID, hop.ToNodeID, hop.ToResourceType
	}
	if source.TargetOccurrenceID != "" && source.TargetOccurrenceID != fromOccurrence {
		return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", fmt.Errorf("related Group target occurrence does not match compiler derivation")
	}
	if source.TargetNodeID != "" && source.TargetNodeID != fromNode {
		return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", fmt.Errorf("related Group target node does not match terminal route node")
	}
	if source.TargetResourceType != "" && source.TargetResourceType != fromResource {
		return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", fmt.Errorf("related Group target resource does not match terminal route resource")
	}
	source.TargetOccurrenceID, source.TargetNodeID, source.TargetResourceType = fromOccurrence, fromNode, fromResource
	wantValueResourceType, err := relatedGroupValueResourceType(valueKind, source)
	if err != nil {
		return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", err
	}
	if source.ResourceType != "" && source.ResourceType != wantValueResourceType {
		return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", fmt.Errorf("related Group field datatype %q does not match generated selected value type %q", source.ResourceType, wantValueResourceType)
	}
	source.ResourceType = wantValueResourceType
	if source.EmptyPolicy != recipe.ExpansionPreserveParent && source.EmptyPolicy != recipe.ExpansionExclude && source.EmptyPolicy != recipe.ExpansionError {
		return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", fmt.Errorf("related Group route has unsupported empty policy")
	}
	if source.Multiplicity != recipe.ConstructionGroupRelatedMultiplicityDistinctTargetPerInput {
		return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", fmt.Errorf("related Group route has unsupported multiplicity")
	}
	routeID, err := relatedGroupRouteID(context, source)
	if err != nil {
		return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", err
	}
	if source.RouteID != "" && source.RouteID != routeID {
		return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", fmt.Errorf("related Group routeId does not match compiler derivation")
	}
	source.RouteID = routeID
	shapeID, err := relatedGroupFieldSourceShapeID(context, valueKind, source)
	if err != nil {
		return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", err
	}
	return source, shapeID, nil
}

func relatedGroupValueResourceType(
	valueKind string,
	source recipe.ConstructionGroupRelatedOccurrenceFieldSource,
) (string, error) {
	if valueKind == "CODING_IDENTITY" {
		return "Coding", nil
	}
	if source.CanonicalPath == "" {
		return "", fmt.Errorf("related Group scalar source has no selected canonical path")
	}
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		return "", fmt.Errorf("load generated FHIR schema: %w", err)
	}
	facts, err := index.ResolveRowPath(fhirschema.DefinitionName(source.TargetResourceType), source.CanonicalPath)
	if err != nil || facts.CanonicalPath != source.CanonicalPath || facts.Shape != fhirschema.RowPathScalar || facts.FHIRType == "" {
		return "", fmt.Errorf("related Group path %q is not a generated scalar on %s", source.CanonicalPath, source.TargetResourceType)
	}
	return facts.FHIRType, nil
}

func recipeGroupFieldSourceInput(source recipe.ConstructionGroupFieldSource) (recipe.ConstructionGroupFieldSourceInput, error) {
	if source.Input != nil {
		return *source.Input, nil
	}
	return recipe.ConstructionGroupFieldSourceInput{}, fmt.Errorf("related Group binding must use the nested source union")
}

func relatedGroupRouteStepComplete(step recipe.ConstructionGroupRelatedRouteStep) bool {
	return step.FromNodeID != "" && step.FromResourceType != "" && step.EdgeID != "" && step.Relationship != "" &&
		(step.StorageDirection == "INBOUND" || step.StorageDirection == "OUTBOUND") &&
		(step.MatchMode == "OPTIONAL" || step.MatchMode == "REQUIRED") && step.ToNodeID != "" && step.ToResourceType != ""
}

func relatedGroupOccurrenceID(sourceStage, sourceShape string, index int, fromOccurrence string, step recipe.ConstructionGroupRelatedRouteStep) string {
	identity := struct {
		Version          int
		SourceStage      string
		SourceShape      string
		StepIndex        int
		FromOccurrenceID string
		FromNodeID       string
		FromResourceType string
		EdgeID           string
		Relationship     string
		StorageDirection string
		MatchMode        string
		ToNodeID         string
		ToResourceType   string
	}{1, sourceStage, sourceShape, index, fromOccurrence, step.FromNodeID, step.FromResourceType, step.EdgeID,
		step.Relationship, step.StorageDirection, step.MatchMode, step.ToNodeID, step.ToResourceType}
	encoded, _ := json.Marshal(identity)
	digest := sha256.Sum256(encoded)
	return "related-occurrence-v1:" + hex.EncodeToString(digest[:16])
}

func relatedGroupRouteID(context *CompiledGroupFieldSourceContext, related recipe.ConstructionGroupRelatedOccurrenceFieldSource) (string, error) {
	identity := struct {
		Version          int
		SourceStageID    string
		SourceShapeID    string
		AnchorOccurrence string
		Route            []recipe.ConstructionGroupRelatedRouteStep
		TargetOccurrence string
	}{1, context.SourceStageID, context.SourceShapeID, related.AnchorOccurrenceID, related.Route, related.TargetOccurrenceID}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "related-route-v1:" + hex.EncodeToString(digest[:]), nil
}

func relatedGroupFieldSourceShapeID(
	context *CompiledGroupFieldSourceContext,
	valueKind string,
	related recipe.ConstructionGroupRelatedOccurrenceFieldSource,
) (string, error) {
	canonical := struct {
		Version           int
		SourceStageID     string
		SourceShapeID     string
		ValueKind         string
		AnchorOccurrence  string
		AnchorNode        string
		AnchorResource    string
		Route             []recipe.ConstructionGroupRelatedRouteStep
		TargetOccurrence  string
		TargetNode        string
		TargetResource    string
		ResourceType      string
		CandidateID       string
		CanonicalPath     string
		RouteEmptyPolicy  recipe.ExpansionEmptyPolicy
		Multiplicity      string
		CodingSelector    string
		CodingEmptyPolicy recipe.ConstructionGroupCodingEmptyPolicy
		OrdinalSemantics  string
	}{
		Version: 1, SourceStageID: context.SourceStageID, SourceShapeID: context.SourceShapeID, ValueKind: valueKind,
		AnchorOccurrence: related.AnchorOccurrenceID, AnchorNode: related.AnchorNodeID, AnchorResource: related.AnchorResourceType,
		Route: related.Route, TargetOccurrence: related.TargetOccurrenceID, TargetNode: related.TargetNodeID,
		TargetResource: related.TargetResourceType, ResourceType: related.ResourceType, CandidateID: related.CandidateID,
		CanonicalPath: related.CanonicalPath, RouteEmptyPolicy: related.EmptyPolicy, Multiplicity: related.Multiplicity,
		CodingSelector: related.CodingSelector, CodingEmptyPolicy: related.CodingEmptyPolicy,
		OrdinalSemantics: related.OrdinalSemantics,
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "group-related-source-shape-v1:" + hex.EncodeToString(digest[:]), nil
}

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
	lowerer := constructionLowerer{plan: plan, policy: policy, usedVariables: usedVariables}
	physical, _, _, err := lowerer.lowerConstructionRelatedExpand(
		syntheticStep, relatedExpand, input, outputs, stage.InputRowVariable,
		context.SourceIdentityColumn, context.RootResourceType, stepIndex,
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
