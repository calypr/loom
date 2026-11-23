package aql

import (
	"fmt"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"strings"
)

const constructionGroupRelatedCodingOrdinalSemantics = "ZERO_BASED_POSITION_IN_FLATTENED_EXPANSION_ARRAY"

type constructionGroupInput struct {
	RowsVariable     string
	SourceRowInScope bool
}

func validateConstructionGroupRelatedOccurrenceRendererShape(adapter *ir.PhysicalStageGroupRelatedOccurrenceAdapter) error {
	if adapter.RouteID == "" || adapter.RelatedRecords.RouteID != adapter.RouteID ||
		adapter.RelatedRecords.ItemVariable == "" || adapter.RelatedRecords.IndexVariable == "" ||
		adapter.RelatedRecords.IdentityVariable == "" || adapter.RelatedRecords.RelatedRecordsVariable == "" ||
		adapter.RelatedRecords.ConstructionIDBindKey == "" || adapter.RelatedRecords.ParentIdentityColumn == "" ||
		adapter.RouteHasItemVariable == "" || adapter.OccurrenceKeyColumn == "" {
		return fmt.Errorf("related Group adapter is missing its exact route or occurrence identity")
	}
	if len(adapter.RelatedRecords.Route) == 0 || adapter.RelatedRecords.RelatedRecords.Return.Kind != ir.PhysicalObjectExpression ||
		adapter.RelatedRecords.RelatedRecords.Return.Object == nil {
		return fmt.Errorf("related Group adapter has no typed target projection")
	}
	seenInputs := make(map[string]struct{}, len(adapter.ScalarProjections))
	seenFields := make(map[string]struct{}, len(adapter.ScalarProjections))
	for _, projection := range adapter.ScalarProjections {
		if projection.InputColumn == "" || projection.ResultField == "" || projection.OwnerResourceType == "" ||
			projection.ValueResourceType == "" || projection.CandidateID == "" || projection.CanonicalPath == "" {
			return fmt.Errorf("related Group adapter has an incomplete scalar projection")
		}
		if _, exists := seenInputs[projection.InputColumn]; exists {
			return fmt.Errorf("related Group adapter repeats scalar input column %q", projection.InputColumn)
		}
		if _, exists := seenFields[projection.ResultField]; exists {
			return fmt.Errorf("related Group adapter repeats target result field %q", projection.ResultField)
		}
		seenInputs[projection.InputColumn] = struct{}{}
		seenFields[projection.ResultField] = struct{}{}
	}
	if adapter.CodingSelector == "" {
		if adapter.CodingUnnest != nil || adapter.CodingArrayColumn != "" || adapter.CodingMembershipColumn != "" || adapter.OrdinalSemantics != "" {
			return fmt.Errorf("related Group adapter has Coding details without a Coding selector")
		}
		return nil
	}
	if adapter.CodingUnnest == nil || adapter.CodingArrayColumn == "" || adapter.CodingMembershipColumn == "" ||
		adapter.OrdinalSemantics == "" || adapter.CodingUnnest.OutputVariable == "" ||
		adapter.CodingUnnest.HasItemVariable == "" || adapter.CodingUnnest.Ordinality == "" ||
		adapter.OrdinalSemantics != constructionGroupRelatedCodingOrdinalSemantics || len(adapter.CodingUnnest.Ancestors) != 0 ||
		adapter.CodingUnnest.Expression.Kind != ir.PhysicalValueExpression || adapter.CodingUnnest.Expression.Value == nil ||
		adapter.CodingUnnest.Expression.Value.Variable != adapter.RelatedRecords.ItemVariable ||
		len(adapter.CodingUnnest.Expression.Value.Path) != 1 || adapter.CodingUnnest.Expression.Value.Path[0] != adapter.CodingArrayColumn {
		return fmt.Errorf("related Group adapter has an incomplete exact Coding expansion")
	}
	if adapter.CodingEmptyPolicy != ir.PhysicalUnnestPreserveParent &&
		adapter.CodingEmptyPolicy != ir.PhysicalUnnestExclude && adapter.CodingEmptyPolicy != ir.PhysicalUnnestError {
		return fmt.Errorf("related Group adapter has unsupported Coding empty policy %q", adapter.CodingEmptyPolicy)
	}
	if adapter.CodingUnnest.EmptyPolicy != adapter.CodingEmptyPolicy {
		return fmt.Errorf("related Group Coding expansion policy does not match its typed unnest")
	}
	return nil
}

func validateConstructionGroupRelatedRootKeyInput(
	stage ir.PhysicalConstructionStage,
	adapter *ir.PhysicalStageGroupRelatedOccurrenceAdapter,
) error {
	if stage.InputRowVariable == "" || adapter == nil || adapter.RelatedRecords.AnchorResourceType == "" {
		return fmt.Errorf("related Group source scope has no typed root identity")
	}
	columnFound := false
	for _, column := range stage.InputColumns {
		if column.Name != "_key" {
			continue
		}
		if column.Kind != "string" || column.Cardinality != "required_one" || !column.Internal ||
			column.RootContributorResourceType != adapter.RelatedRecords.AnchorResourceType {
			return fmt.Errorf("related Group source _key is not the exact typed root identity")
		}
		columnFound = true
		break
	}
	if !columnFound {
		return fmt.Errorf("related Group source scope is missing its typed root _key input")
	}
	projectionFound := false
	for _, projection := range stage.InputProjections {
		if projection.Name != "_key" {
			continue
		}
		if !projection.Hidden || projection.Expression != nil || projection.Value.Variable != stage.InputRowVariable ||
			len(projection.Value.Path) != 1 || projection.Value.Path[0] != "_key" {
			return fmt.Errorf("related Group source _key projection is not the exact input identity")
		}
		projectionFound = true
		break
	}
	if !projectionFound {
		return fmt.Errorf("related Group source scope is missing its typed _key input projection")
	}
	return nil
}

func codingIdentityMembershipTypeAssertions(membership string) []string {
	lines := make([]string, 0, 3)
	for _, field := range []string{"system", "version", "display"} {
		value := fmt.Sprintf("%s.%s", membership, field)
		lines = append(lines, fmt.Sprintf("  FILTER ASSERT(%s == null OR TYPENAME(%s) == \"string\", \"CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH\")", value, value))
	}
	return lines
}

func codingIdentityTupleExpression(membership string) string {
	system := fmt.Sprintf("%s.system", membership)
	version := fmt.Sprintf("(TRIM(%s.version) != \"\" ? %s.version : null)", membership, membership)
	code := fmt.Sprintf("(TYPENAME(%s.code) == \"string\" AND TRIM(%s.code) != \"\" ? %s.code : null)", membership, membership, membership)
	return fmt.Sprintf("[%s, %s, %s]", system, version, code)
}

func codingIdentityMissingCodeFilter(policy ir.PhysicalStageGroupMissingKeyPolicy, tupleVariable string) (string, error) {
	switch policy {
	case ir.PhysicalStageGroupMissingKeyGroup:
		return "", nil
	case ir.PhysicalStageGroupMissingKeyExclude:
		return fmt.Sprintf("FILTER %s[2] != null", tupleVariable), nil
	case ir.PhysicalStageGroupMissingKeyError:
		return fmt.Sprintf("FILTER ASSERT(%s[2] != null, \"CONSTRUCTION_GROUP_MISSING_KEY\")", tupleVariable), nil
	default:
		return "", fmt.Errorf("unsupported missing-key policy %q", policy)
	}
}

func constructionGroupRowValuesCanAggregate(rowValues []ir.PhysicalStageRowValue) bool {
	if len(rowValues) == 0 {
		return false
	}
	for _, rowValue := range rowValues {
		if rowValue.Policy != "ALL" && rowValue.Policy != "ONE" {
			return false
		}
	}
	return true
}

const semanticGroupProjectionSampleLimit = 512

func semanticGroupProjectionPreviewShape(
	plan ir.PhysicalPlan,
) (*ir.PhysicalRootScan, *ir.PhysicalConstructionStage, *ir.PhysicalStageGroupSemanticKey, error) {
	if plan.Engine == ir.PhysicalEngineClickHouse {
		return nil, nil, nil, fmt.Errorf("semantic Group projection preview requires AQL")
	}
	if err := plan.Validate(); err != nil {
		return nil, nil, nil, fmt.Errorf("validate semantic Group projection plan: %w", err)
	}
	if err := ir.ValidateGenericPhysicalPlanScope(plan); err != nil {
		return nil, nil, nil, fmt.Errorf("verify semantic Group projection source scope: %w", err)
	}
	sequence := plan.StageSequence
	if sequence == nil || sequence.PreviewSourceWindowByRootID || sequence.PreviewTerminalPivotWindow ||
		sequence.CellTraceReturn != nil || sequence.RowLineageReturn != nil || sequence.PopulationMappingReturn != nil ||
		sequence.OutputAuthResourcePathBindKey != "" || len(sequence.Stages) != 1 ||
		sequence.SourceStageID == "" || sequence.FinalStageID == "" {
		return nil, nil, nil, fmt.Errorf("semantic Group projection preview requires one unwindowed direct source Group")
	}
	if sequence.PreviewLimitBindKey != "" {
		limit, ok := plan.BindVars[sequence.PreviewLimitBindKey].(int)
		if !ok || limit <= 0 {
			return nil, nil, nil, fmt.Errorf("semantic Group projection preview has an invalid result limit")
		}
	}
	stage := &sequence.Stages[0]
	group := stage.Group
	if stage.ID != sequence.FinalStageID || stage.InputStageID != sequence.SourceStageID ||
		stage.Kind != ir.PhysicalStageGroupOp || group == nil || len(group.Keys) != 0 ||
		group.SemanticKey == nil || group.CodingKey != nil || group.RelatedOccurrence != nil ||
		len(group.Aggregates) != 1 || group.Aggregates[0].Operation != "COUNT_ROWS" ||
		group.Aggregates[0].InputColumn != "" || group.Aggregates[0].InputKind != "" ||
		len(group.RowValues) != 0 || group.DynamicRuntimeKeysVariable != "" || group.RootContributorInputColumn != "" || group.RootContributorInputMany ||
		group.RootContributorOutputColumn != "" || group.RootContributorVariable != "" ||
		group.MissingKeyPolicy != ir.PhysicalStageGroupMissingKeyGroup &&
			group.MissingKeyPolicy != ir.PhysicalStageGroupMissingKeyExclude &&
			group.MissingKeyPolicy != ir.PhysicalStageGroupMissingKeyError ||
		stage.InputRowVariable == "" || stage.OutputRowVariable == "" || group.IdentityVariable == "" ||
		group.ConstructionIDBindKey == "" || group.Aggregates[0].Output == "" || group.Aggregates[0].Variable == "" {
		return nil, nil, nil, fmt.Errorf("semantic Group projection preview requires one COUNT_ROWS semantic key without row values or lineage")
	}
	if sequence.FinalRowIdentity != "__loom_row_id" {
		return nil, nil, nil, fmt.Errorf("semantic Group projection preview requires its semantic tuple identity")
	}
	semantic := group.SemanticKey
	if semantic.OutputColumn == "" || semantic.TupleVariable == "" || semantic.LabelVariable == "" ||
		semantic.SourceRootVariable == "" || semantic.SourceIdentityColumn != "_key" ||
		semantic.RootCollectionBindKey == "" || semantic.ResourceType == "" || semantic.CodingPath == "" ||
		semantic.BindingID == "" || semantic.RuleVersion == "" || semantic.FamilyID == "" || semantic.System == "" ||
		len(semantic.PathSegments) == 0 || semantic.PathSegments[len(semantic.PathSegments)-1].Name != "coding" ||
		!semantic.PathSegments[len(semantic.PathSegments)-1].Repeated {
		return nil, nil, nil, fmt.Errorf("semantic Group key is not one complete repeated Coding projection")
	}
	for index, segment := range semantic.PathSegments {
		if !previewSourcePathSegmentPattern.MatchString(segment.Name) {
			return nil, nil, nil, fmt.Errorf("semantic Group Coding path segment %d is unsafe", index)
		}
	}
	if _, ok := plan.BindVars[group.ConstructionIDBindKey]; !ok {
		return nil, nil, nil, fmt.Errorf("semantic Group construction identity bind is missing")
	}
	rootOperation := plan.Operations[0]
	if rootOperation.Kind != ir.PhysicalRootScanOp || rootOperation.RootScan == nil {
		return nil, nil, nil, fmt.Errorf("semantic Group projection preview requires a direct root scan")
	}
	root := rootOperation.RootScan
	collection, ok := plan.BindVars[root.CollectionBindKey].(string)
	if !ok || collection == "" || root.Variable == "" || root.Population != nil || root.CohortSource != nil ||
		root.PageCandidateSeed != nil || plan.PreviewSourceWindowByRootID ||
		root.CollectionBindKey != semantic.RootCollectionBindKey || root.Variable != semantic.SourceRootVariable ||
		collection != semantic.ResourceType || collection != plan.Source.ResourceType {
		return nil, nil, nil, fmt.Errorf("semantic Group projection preview source is not the exact direct root")
	}
	if len(plan.Operations) < 5 || plan.Operations[len(plan.Operations)-1].Kind != ir.PhysicalReturnOp ||
		plan.Operations[len(plan.Operations)-1].Return == nil {
		return nil, nil, nil, fmt.Errorf("semantic Group projection preview source has no terminal projection")
	}
	for index, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 {
				return nil, nil, nil, fmt.Errorf("semantic Group projection preview has more than one root scan")
			}
		case ir.PhysicalFilterOp:
		case ir.PhysicalDerivedLetOp:
			if operation.DerivedLet == nil || operation.DerivedLet.Operator != "AUTH_RESOURCE_PATH_ALLOWED" {
				return nil, nil, nil, fmt.Errorf("semantic Group projection preview has a non-scope derived stage")
			}
		case ir.PhysicalReturnOp:
			if index != len(plan.Operations)-1 {
				return nil, nil, nil, fmt.Errorf("semantic Group projection preview has an intermediate return")
			}
		default:
			return nil, nil, nil, fmt.Errorf("semantic Group projection preview does not support source operation %q", operation.Kind)
		}
	}
	if len(stage.OutputProjections) != 3 {
		return nil, nil, nil, fmt.Errorf("semantic Group projection preview has unexpected output projections")
	}
	seenSemantic, seenCount, seenIdentity := false, false, false
	for _, projection := range stage.OutputProjections {
		if projection.Expression != nil || projection.Presence != nil || projection.PresenceOutput {
			return nil, nil, nil, fmt.Errorf("semantic Group projection preview has a transformed output")
		}
		switch projection.Name {
		case semantic.OutputColumn:
			seenSemantic = projection.Value.Variable == semantic.LabelVariable
		case group.Aggregates[0].Output:
			seenCount = projection.Value.Variable == group.Aggregates[0].Variable
		case "__loom_row_id":
			seenIdentity = projection.Hidden && projection.Value.Variable == group.IdentityVariable
		default:
			return nil, nil, nil, fmt.Errorf("semantic Group projection preview has an unexpected output column %q", projection.Name)
		}
	}
	if !seenSemantic || !seenCount || !seenIdentity {
		return nil, nil, nil, fmt.Errorf("semantic Group projection preview outputs do not match its tuple, count, and identity")
	}
	if _, err := RenderPhysicalRootScopeCount(plan); err != nil {
		return nil, nil, nil, fmt.Errorf("semantic Group projection preview has no exact scoped count: %w", err)
	}
	return root, stage, semantic, nil
}

func renderSemanticGroupProjectionPreview(plan ir.PhysicalPlan, indexName string, disableIndex, assessment bool) (RenderedPhysicalPlan, error) {
	root, stage, semantic, err := semanticGroupProjectionPreviewShape(plan)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	if disableIndex && indexName != "" {
		return RenderedPhysicalPlan{}, fmt.Errorf("a disabled root index cannot also carry index hint %q", indexName)
	}
	if indexName != "" && !rootIndexHintPattern.MatchString(indexName) {
		return RenderedPhysicalPlan{}, fmt.Errorf("root index hint %q is not a safe index name", indexName)
	}
	collectionKeys, err := collectionBindKeys(plan)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	sourcePlan := ir.ClonePhysicalPlan(plan)
	sourcePlan.StageSequence = nil
	sourcePlan.PreviewSourceWindowByRootID = false
	source, err := renderPhysicalPlanWithOptions(sourcePlan, physicalRenderOptions{
		rootIndexHint: indexName, disableRootIndex: disableIndex,
		internalPrefix: "semantic_group_projection_source_", omitTerminalReturn: true,
	})
	if err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("render semantic Group projection source: %w", err)
	}
	bindVars := make(map[string]any, len(source.BindVars)+len(plan.BindVars)+16)
	if err := mergeRenderedBindVars(bindVars, source.BindVars); err != nil {
		return RenderedPhysicalPlan{}, err
	}
	if err := mergeRenderedBindVars(bindVars, runtimePhysicalBindVars(plan.BindVars, collectionKeys)); err != nil {
		return RenderedPhysicalPlan{}, err
	}
	renderer := physicalPlanRenderer{renderContext: renderContext{
		bindVars: bindVars, collectionKeys: collectionKeys,
		reservedVars: stageSequenceVariableNames(plan), internalPrefix: "semantic_group_projection_",
	}}
	for variable := range physicalPlanVariableNames(sourcePlan) {
		renderer.reservedVars[variable] = struct{}{}
	}
	lines := make([]string, 0, 48)
	if assessment {
		lines, err = renderer.renderSemanticGroupProjectionAssessment(source.Query, root, semantic)
	} else {
		lines, err = renderer.renderSemanticGroupProjectionFactorization(
			source.Query, root, stage, semantic, plan.StageSequence.PreviewLimitBindKey,
		)
	}
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	query := strings.Join(lines, "\n") + "\n"
	return RenderedPhysicalPlan{Query: query, BindVars: pruneUnusedRuntimeBindVars(renderer.bindVars, query)}, nil
}
