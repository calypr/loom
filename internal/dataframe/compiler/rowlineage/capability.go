package rowlineage

import (
	"reflect"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
)

func RowLineageCapabilityForOutput(output lower.CompiledRecipeOutput) RowLineageCapability {
	sequence := output.Plan.StageSequence
	if sequence == nil || len(sequence.Stages) == 0 {
		if rowLineageStandaloneGroupRows(output) {
			return RowLineageCapability{Available: true}
		}
		for _, operation := range output.Plan.Operations {
			if operation.Kind == ir.PhysicalGroupRowsOp {
				return RowLineageCapability{ReasonCode: "ROW_LINEAGE_IDENTITY_UNAVAILABLE", Operation: string(operation.Kind)}
			}
		}
		return RowLineageCapability{ReasonCode: "ROW_LINEAGE_OPERATION_UNSUPPORTED", Operation: "SOURCE"}
	}
	if RowLineageRelatedCodingGroup(output) {
		return RowLineageCapability{Available: true}
	}
	if rowLineageHasRelatedCodingGroup(sequence) {
		return RowLineageCapability{ReasonCode: "ROW_LINEAGE_RELATED_GROUP_UNSUPPORTED", Operation: "GROUP"}
	}
	if rowLineageHasRelatedExpand(sequence) {
		return rowLineageRelatedSequenceCapability(output)
	}
	if rowLineageCohortGroupFilterSuffix(output) {
		return RowLineageCapability{Available: true}
	}
	if rowLineageConstructionGroupPivotPreimage(output) {
		return RowLineageCapability{Available: true}
	}
	stage := sequence.Stages[0]
	if stage.Kind == ir.PhysicalStageGroupOp && stage.Group != nil && stage.Group.SemanticKey != nil && len(sequence.Stages) != 1 {
		return RowLineageCapability{ReasonCode: "ROW_LINEAGE_SEMANTIC_GROUP_COMPOSITION_UNSUPPORTED", Operation: string(stage.Kind)}
	}
	if stage.InputStageID != sequence.SourceStageID {
		return RowLineageCapability{ReasonCode: "ROW_LINEAGE_OPERATION_UNSUPPORTED", Operation: string(stage.Kind)}
	}
	if len(sequence.Stages) > 1 && !rowLineageGroupFilterSuffix(sequence) {
		return RowLineageCapability{ReasonCode: "ROW_LINEAGE_OPERATION_UNSUPPORTED", Operation: string(sequence.Stages[1].Kind)}
	}
	if stage.Kind == ir.PhysicalStageCodedGroupOp {
		coded := stage.CodedGroup
		if coded == nil || sequence.SourceRowIdentity != "_key" || coded.SourceIdentityColumn != "_key" ||
			coded.ResourceType != output.RootResourceType {
			return RowLineageCapability{ReasonCode: "ROW_LINEAGE_ROOT_ANCHOR_UNSUPPORTED", Operation: string(stage.Kind)}
		}
		if !rowLineageHasDirectRootSource(output.Plan.Operations) {
			return RowLineageCapability{ReasonCode: "ROW_LINEAGE_SOURCE_NOT_DIRECT", Operation: string(stage.Kind)}
		}
		if !rowLineageHasPhysicalIdentity(output, stage, sequence) {
			return RowLineageCapability{ReasonCode: "ROW_LINEAGE_IDENTITY_UNAVAILABLE", Operation: string(stage.Kind)}
		}
		return RowLineageCapability{Available: true}
	}
	if RowLineageExpandedCodingGroup(output) {
		return RowLineageCapability{Available: true}
	}
	if rowLineageConstructionPivotPreimage(output) {
		return RowLineageCapability{Available: true}
	}
	if stage.Kind != ir.PhysicalStageGroupOp || stage.Group == nil {
		return RowLineageCapability{ReasonCode: "ROW_LINEAGE_OPERATION_UNSUPPORTED", Operation: string(stage.Kind)}
	}
	for _, operation := range output.Plan.Operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp, ir.PhysicalFilterOp, ir.PhysicalSetOp, ir.PhysicalReturnOp, ir.PhysicalDerivedLetOp, ir.PhysicalExpressionLetOp:
		default:
			return RowLineageCapability{ReasonCode: "ROW_LINEAGE_SOURCE_NOT_DIRECT", Operation: string(operation.Kind)}
		}
	}
	if sequence.Stages[len(sequence.Stages)-1].ID != sequence.FinalStageID ||
		!rowLineageHasPhysicalIdentity(output, sequence.Stages[len(sequence.Stages)-1], sequence) {
		return RowLineageCapability{ReasonCode: "ROW_LINEAGE_IDENTITY_UNAVAILABLE", Operation: string(stage.Kind)}
	}
	return RowLineageCapability{Available: true}
}

func RowLineageRelatedCodingGroup(output lower.CompiledRecipeOutput) bool {
	sequence := output.Plan.StageSequence
	if sequence == nil || sequence.SourceRowIdentity != "_key" || len(sequence.Stages) != 1 ||
		output.RootResourceType == "" || output.Plan.Source.ResourceType != output.RootResourceType || output.RowIdentity == nil ||
		!rowLineageHasDirectRootSource(output.Plan.Operations) {
		return false
	}
	stage := sequence.Stages[0]
	group := stage.Group
	if stage.Kind != ir.PhysicalStageGroupOp || group == nil || stage.InputStageID != sequence.SourceStageID ||
		stage.ID != sequence.FinalStageID || stage.RowIdentityColumn != sequence.FinalRowIdentity ||
		!rowLineageHasPhysicalIdentity(output, stage, sequence) || group.SemanticKey != nil ||
		group.RootContributorInputColumn != "" || group.CodingKey == nil || group.RelatedOccurrence == nil ||
		len(group.Aggregates) == 0 {
		return false
	}
	for _, aggregate := range group.Aggregates {
		if aggregate.Operation != "COUNT_ROWS" || aggregate.InputColumn != "" || aggregate.InputKind != "" {
			return false
		}
	}
	coding, adapter := group.CodingKey, group.RelatedOccurrence
	related := coding.RelatedSource
	if related == nil || related.RouteID == "" || related.RouteID != adapter.RouteID ||
		related.SourceStageID != sequence.SourceStageID || related.SourceShapeID == "" ||
		coding.SourceStageID != related.SourceStageID || coding.SourceShapeID != related.SourceShapeID ||
		coding.SourceOccurrenceKeyColumn == "" || coding.SourceOccurrenceKeyColumn != adapter.OccurrenceKeyColumn ||
		coding.SourceUnnest != nil || coding.OwnerOccurrenceID != related.TargetOccurrenceID ||
		coding.OwnerResourceType != related.TargetResourceType || coding.CodingPath == "" ||
		coding.CodingPath != related.CodingSelector || coding.CodingPath != adapter.CodingSelector ||
		coding.MembershipColumn == "" || coding.MembershipColumn != related.MembershipColumn ||
		coding.MembershipColumn != adapter.CodingMembershipColumn || adapter.CodingArrayColumn == "" ||
		adapter.CodingUnnest == nil || adapter.CodingUnnest.Ordinality == "" || adapter.OrdinalSemantics != "ZERO_BASED_POSITION_IN_FLATTENED_EXPANSION_ARRAY" ||
		adapter.CodingUnnest.EmptyPolicy != adapter.CodingEmptyPolicy || adapter.RelatedRecords.RouteID != adapter.RouteID ||
		adapter.RouteHasItemVariable == "" || !rowLineageRelatedCodingEmptyPolicyMatches(related.CodingEmptyPolicy, adapter.CodingEmptyPolicy) ||
		related.OrdinalSemantics != adapter.OrdinalSemantics || related.EmptyPolicy != string(adapter.RelatedRecords.EmptyPolicy) ||
		related.Multiplicity != adapter.RelatedRecords.Multiplicity ||
		adapter.RelatedRecords.AnchorKind != "root" || adapter.RelatedRecords.AnchorColumnID != "_key" ||
		adapter.RelatedRecords.ParentIdentityColumn != "_key" || len(adapter.RelatedRecords.Route) == 0 ||
		related.AnchorOccurrenceID != adapter.RelatedRecords.AnchorOccurrenceID ||
		related.AnchorNodeID != adapter.RelatedRecords.AnchorNodeID || related.AnchorResourceType != adapter.RelatedRecords.AnchorResourceType ||
		related.TargetOccurrenceID != adapter.RelatedRecords.Route[len(adapter.RelatedRecords.Route)-1].ToOccurrenceID ||
		related.TargetNodeID != adapter.RelatedRecords.TargetNodeID || related.TargetResourceType != adapter.RelatedRecords.TargetResourceType ||
		!rowLineageRelatedRouteMatches(related.Route, adapter.RelatedRecords.Route) {
		return false
	}
	scalarColumns := make(map[string]struct{}, len(adapter.ScalarProjections))
	for _, projection := range adapter.ScalarProjections {
		if projection.InputColumn == "" || projection.ResultField == "" {
			return false
		}
		scalarColumns[projection.InputColumn] = struct{}{}
	}
	for _, key := range group.Keys {
		if key.InputColumn == "" || key.OutputColumn == "" || key.Variable == "" || key.Kind == "" ||
			!rowLineageRelatedGroupSourceAvailable(key.FieldSource, key.InputColumn, scalarColumns, related, sequence.SourceStageID, stage.InputColumns) {
			return false
		}
		if !rowLineageStageHasColumn(stage.OutputColumns, key.OutputColumn) {
			return false
		}
	}
	for _, rowValue := range group.RowValues {
		if rowValue.InputColumn == "" || rowValue.Output == "" ||
			!rowLineageRelatedGroupSourceAvailable(rowValue.FieldSource, rowValue.InputColumn, scalarColumns, related, sequence.SourceStageID, stage.InputColumns) {
			return false
		}
	}
	return rowLineageStageHasColumn(stage.OutputColumns, coding.OutputColumn)
}

func rowLineageRelatedCodingEmptyPolicyMatches(sourcePolicy string, physicalPolicy ir.PhysicalUnnestEmptyPolicy) bool {
	switch sourcePolicy {
	case "GROUP":
		return physicalPolicy == ir.PhysicalUnnestPreserveParent
	case "EXCLUDE":
		return physicalPolicy == ir.PhysicalUnnestExclude
	case "ERROR":
		return physicalPolicy == ir.PhysicalUnnestError
	default:
		return false
	}
}

func rowLineageHasRelatedCodingGroup(sequence *ir.PhysicalStageSequence) bool {
	if sequence == nil {
		return false
	}
	for _, stage := range sequence.Stages {
		if stage.Group != nil && (stage.Group.RelatedOccurrence != nil ||
			(stage.Group.CodingKey != nil && stage.Group.CodingKey.RelatedSource != nil)) {
			return true
		}
	}
	return false
}

func rowLineageRelatedGroupSourceAvailable(
	fieldSource *ir.PhysicalStageGroupFieldSource,
	inputColumn string,
	relatedScalarColumns map[string]struct{},
	groupRoute *ir.PhysicalStageGroupRelatedOccurrenceFieldSource,
	sourceStageID string,
	stageInputs []ir.PhysicalStageColumn,
) bool {
	if !rowLineageStageHasColumn(stageInputs, inputColumn) {
		return false
	}
	if fieldSource == nil {
		return inputColumn != ""
	}
	if fieldSource.Kind == ir.PhysicalStageGroupFieldSourceRelatedOccurrence {
		source := fieldSource.RelatedOccurrence
		_, projected := relatedScalarColumns[inputColumn]
		return source != nil && source.ProjectionColumn == inputColumn && projected &&
			rowLineageSameRelatedGroupRoute(source, groupRoute)
	}
	if fieldSource.Kind != ir.PhysicalStageGroupFieldSourceCurrentStage || fieldSource.RelatedOccurrence != nil ||
		fieldSource.SourceStageID != sourceStageID || fieldSource.SourceShapeID == "" || fieldSource.Role == "" {
		return false
	}
	return inputColumn != ""
}

func rowLineageSameRelatedGroupRoute(
	left *ir.PhysicalStageGroupRelatedOccurrenceFieldSource,
	right *ir.PhysicalStageGroupRelatedOccurrenceFieldSource,
) bool {
	return left != nil && right != nil && left.RouteID == right.RouteID &&
		left.SourceStageID == right.SourceStageID &&
		left.AnchorOccurrenceID == right.AnchorOccurrenceID && left.AnchorNodeID == right.AnchorNodeID &&
		left.AnchorResourceType == right.AnchorResourceType && reflect.DeepEqual(left.Route, right.Route) &&
		left.TargetOccurrenceID == right.TargetOccurrenceID && left.TargetNodeID == right.TargetNodeID &&
		left.TargetResourceType == right.TargetResourceType && left.EmptyPolicy == right.EmptyPolicy &&
		left.Multiplicity == right.Multiplicity
}

func rowLineageRelatedRouteMatches(
	source []ir.PhysicalStageGroupRelatedRouteStep,
	actual []ir.PhysicalStageRelatedRouteStep,
) bool {
	if len(source) == 0 || len(source) != len(actual) {
		return false
	}
	for index, route := range source {
		step := actual[index]
		if route.StepIndex != index || route.StepIndex != step.StepIndex ||
			route.FromOccurrenceID != step.FromOccurrenceID || route.ToOccurrenceID != step.ToOccurrenceID ||
			route.EdgeID != step.EdgeID || route.FromNodeID != step.FromNodeID || route.ToNodeID != step.ToNodeID ||
			route.FromResourceType != step.FromResourceType || route.ToResourceType != step.ToResourceType ||
			route.Relationship != step.Relationship || route.StorageDirection != step.StorageDirection ||
			route.MatchMode != step.MatchMode {
			return false
		}
	}
	return true
}

func rowLineageStageHasColumn(columns []ir.PhysicalStageColumn, name string) bool {
	for _, column := range columns {
		if column.Name == name && column.ID != "" {
			return true
		}
	}
	return false
}

func RowLineageExpandedCodingGroup(output lower.CompiledRecipeOutput) bool {
	sequence := output.Plan.StageSequence
	if sequence == nil || sequence.SourceRowIdentity != "__loom_expansion_identity" || len(sequence.Stages) != 1 ||
		output.RootResourceType == "" || output.Plan.Source.ResourceType != output.RootResourceType || output.RowIdentity == nil {
		return false
	}
	stage := sequence.Stages[0]
	group := stage.Group
	if stage.Kind != ir.PhysicalStageGroupOp || group == nil || group.SemanticKey != nil || group.CodingKey == nil ||
		stage.InputStageID != sequence.SourceStageID || stage.ID != sequence.FinalStageID ||
		stage.RowIdentityColumn != sequence.FinalRowIdentity || !rowLineageHasPhysicalIdentity(output, stage, sequence) ||
		group.RootContributorInputColumn != "" || group.CodingKey.SourceStageID != sequence.SourceStageID ||
		group.CodingKey.SourceShapeID == "" || group.CodingKey.SourceOccurrenceKeyColumn != "__loom_group_source_occurrence_key" ||
		group.CodingKey.OwnerOccurrenceID == "" || group.CodingKey.OwnerResourceType == "" || group.CodingKey.CodingPath == "" ||
		group.CodingKey.SourceUnnest == nil || len(group.Aggregates) == 0 {
		return false
	}
	for _, aggregate := range group.Aggregates {
		if aggregate.Operation != "COUNT_ROWS" || aggregate.InputColumn != "" || aggregate.InputKind != "" {
			return false
		}
	}
	if sequence.Stages[0].Group.CodingKey.SourceUnnest.Ordinality == "" {
		return false
	}
	rootScans, unnests, returns := 0, 0, 0
	var sourceUnnest *ir.PhysicalUnnest
	for index, operation := range output.Plan.Operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 || operation.RootScan == nil {
				return false
			}
			rootScans++
		case ir.PhysicalUnnestOp:
			if operation.Unnest == nil {
				return false
			}
			unnests++
			sourceUnnest = operation.Unnest
		case ir.PhysicalFilterOp, ir.PhysicalSetOp, ir.PhysicalDerivedLetOp, ir.PhysicalExpressionLetOp:
		case ir.PhysicalReturnOp:
			returns++
			if operation.Return == nil || index != len(output.Plan.Operations)-1 {
				return false
			}
		default:
			return false
		}
	}
	return rootScans == 1 && unnests == 1 && returns == 1 && sourceUnnest != nil &&
		ir.PhysicalUnnestExactGroupShapeEqual(*sourceUnnest, *group.CodingKey.SourceUnnest) &&
		sourceUnnest.Owner.OccurrenceID == group.CodingKey.OwnerOccurrenceID &&
		sourceUnnest.Owner.ResourceType == group.CodingKey.OwnerResourceType && sourceUnnest.Ordinality != "" &&
		sourceUnnest.Expression.Extract != nil && sourceUnnest.Expression.Extract.Selector.CanonicalPath() == group.CodingKey.CodingPath
}

func rowLineageConstructionPivotPreimage(output lower.CompiledRecipeOutput) bool {
	sequence := output.Plan.StageSequence
	if sequence == nil || sequence.SourceRowIdentity != "_key" || len(sequence.Stages) != 1 ||
		!rowLineageHasDirectRootSource(output.Plan.Operations) || output.RowIdentity == nil {
		return false
	}
	stage := sequence.Stages[0]
	pivot := stage.GroupedPivot
	return stage.ID != "" && stage.InputStageID == sequence.SourceStageID &&
		stage.Kind == ir.PhysicalStagePivotOp && pivot != nil &&
		pivot.CodedCorrelation == nil && len(pivot.GroupKeys) > 0 && len(pivot.Categories) > 0 &&
		pivot.UnlistedCategoryPolicy == "ERROR" &&
		(pivot.DuplicatePolicy == "ERROR" || pivot.DuplicatePolicy == "SUM" || pivot.DuplicatePolicy == "MIN" || pivot.DuplicatePolicy == "MAX") &&
		(pivot.MissingCellPolicy == "ERROR" || pivot.MissingCellPolicy == "NULL") && stage.RowIdentityColumn != "" &&
		stage.RowIdentityColumn == sequence.FinalRowIdentity && sequence.FinalStageID == stage.ID &&
		output.RootResourceType != "" && rowLineageHasPhysicalIdentity(output, stage, sequence)
}

func rowLineageConstructionGroupPivotPreimage(output lower.CompiledRecipeOutput) bool {
	sequence := output.Plan.StageSequence
	if sequence == nil || sequence.SourceRowIdentity != "_key" || len(sequence.Stages) != 2 ||
		!rowLineageHasDirectRootSource(output.Plan.Operations) || output.RowIdentity == nil || output.RootResourceType == "" {
		return false
	}

	groupStage, pivotStage := sequence.Stages[0], sequence.Stages[1]
	group, pivot := groupStage.Group, pivotStage.GroupedPivot
	if groupStage.Kind != ir.PhysicalStageGroupOp || group == nil || groupStage.InputStageID != sequence.SourceStageID ||
		pivotStage.Kind != ir.PhysicalStagePivotOp || pivot == nil || pivotStage.InputStageID != groupStage.ID ||
		pivotStage.ID != sequence.FinalStageID || pivotStage.RowIdentityColumn != sequence.FinalRowIdentity ||
		!rowLineageHasPhysicalIdentity(output, pivotStage, sequence) ||
		pivot.CodedCorrelation != nil || pivot.CategoryPresence != nil || len(pivot.GroupKeys) == 0 || len(pivot.Categories) == 0 ||
		pivot.UnlistedCategoryPolicy != "ERROR" ||
		(pivot.DuplicatePolicy != "ERROR" && pivot.DuplicatePolicy != "SUM" && pivot.DuplicatePolicy != "MIN" && pivot.DuplicatePolicy != "MAX") ||
		(pivot.MissingCellPolicy != "ERROR" && pivot.MissingCellPolicy != "NULL") ||
		pivot.UnlistedEvidenceColumn != "" || len(pivot.RowValues) != 0 ||
		len(group.Aggregates) != 1 || group.Aggregates[0].Operation != "COUNT_ROWS" ||
		group.Aggregates[0].InputColumn != "" || group.Aggregates[0].InputKind != "" ||
		group.Aggregates[0].OutputKind != "integer" || len(group.RowValues) != 0 || len(group.Keys) != len(pivot.GroupKeys)+1 ||
		pivot.ValueColumn != group.Aggregates[0].Output || pivot.ValueType != "INTEGER" {
		return false
	}
	if group.MissingKeyPolicy != ir.PhysicalStageGroupMissingKeyGroup &&
		group.MissingKeyPolicy != ir.PhysicalStageGroupMissingKeyExclude &&
		group.MissingKeyPolicy != ir.PhysicalStageGroupMissingKeyError {
		return false
	}

	groupKeys := make(map[string]ir.PhysicalStageGroupKey, len(group.Keys))
	for _, key := range group.Keys {
		if key.OutputColumn == "" || key.InputColumn == "" || groupKeys[key.OutputColumn].OutputColumn != "" {
			return false
		}
		groupKeys[key.OutputColumn] = key
	}
	categoryKey, ok := groupKeys[pivot.CategoryColumn]
	if !ok || categoryKey.Kind != pivot.CategoryType {
		return false
	}
	delete(groupKeys, pivot.CategoryColumn)
	for _, key := range pivot.GroupKeys {
		groupOutput := key.Column
		groupKey, exists := groupKeys[groupOutput]
		if !exists || groupKey.Kind != key.Kind {
			return false
		}
		delete(groupKeys, groupOutput)
	}
	return len(groupKeys) == 0
}

func rowLineageHasRelatedExpand(sequence *ir.PhysicalStageSequence) bool {
	if sequence == nil {
		return false
	}
	for _, stage := range sequence.Stages {
		if stage.Kind == ir.PhysicalStageRelatedExpandOp {
			return true
		}
	}
	return false
}

func rowLineageRelatedSequenceCapability(output lower.CompiledRecipeOutput) RowLineageCapability {
	sequence := output.Plan.StageSequence
	if sequence == nil || sequence.SourceRowIdentity != "_key" || !rowLineageHasDirectRootSource(output.Plan.Operations) {
		return RowLineageCapability{ReasonCode: "ROW_LINEAGE_SOURCE_NOT_DIRECT", Operation: "RELATED_EXPAND"}
	}
	priorIdentity := sequence.SourceRowIdentity
	owners := 0
	for _, stage := range sequence.Stages {
		if stage.InputStageID == "" || stage.RowIdentityColumn == "" {
			return RowLineageCapability{ReasonCode: "ROW_LINEAGE_IDENTITY_UNAVAILABLE", Operation: string(stage.Kind)}
		}
		switch stage.Kind {
		case ir.PhysicalStageDeriveOp, ir.PhysicalStageFilterOp, ir.PhysicalStageRelatedSourceOp,
			ir.PhysicalStageRelatedEligibilityOp, ir.PhysicalStageRelatedFieldOp:
			if stage.RowIdentityColumn != priorIdentity {
				return RowLineageCapability{ReasonCode: "ROW_LINEAGE_IDENTITY_UNAVAILABLE", Operation: string(stage.Kind)}
			}
		case ir.PhysicalStageRelatedExpandOp:
			related := stage.RelatedExpand
			if related == nil || !rowLineageRelatedRouteSupported(*related, output.Plan.BindVars) {
				return RowLineageCapability{ReasonCode: "ROW_LINEAGE_ROUTE_UNSUPPORTED", Operation: string(stage.Kind)}
			}
			if related.AnchorKind != "root" && related.AnchorKind != "activeRelatedRecord" {
				return RowLineageCapability{ReasonCode: "ROW_LINEAGE_ROOT_ANCHOR_UNSUPPORTED", Operation: string(stage.Kind)}
			}
			if related.AnchorKind == "root" && related.AnchorColumnID != "_key" {
				return RowLineageCapability{ReasonCode: "ROW_LINEAGE_ROOT_ANCHOR_UNSUPPORTED", Operation: string(stage.Kind)}
			}
			owners++
		default:
			return RowLineageCapability{ReasonCode: "ROW_LINEAGE_OPERATION_UNSUPPORTED", Operation: string(stage.Kind)}
		}
		priorIdentity = stage.RowIdentityColumn
	}
	last := sequence.Stages[len(sequence.Stages)-1]
	if owners == 0 || last.ID != sequence.FinalStageID || last.RowIdentityColumn != sequence.FinalRowIdentity ||
		!rowLineageHasPhysicalIdentity(output, last, sequence) {
		return RowLineageCapability{ReasonCode: "ROW_LINEAGE_IDENTITY_UNAVAILABLE", Operation: string(last.Kind)}
	}
	return RowLineageCapability{Available: true}
}

func rowLineageRelatedRouteSupported(related ir.PhysicalStageRelatedExpand, bindVars map[string]any) bool {
	if len(related.Route) == 0 {
		return false
	}
	traversals := make([]ir.PhysicalOperation, 0, len(related.Route))
	for _, operation := range related.RelatedRecords.Operations {
		if operation.Kind == ir.PhysicalTraversalOp {
			traversals = append(traversals, operation)
		}
	}
	if len(traversals) != len(related.Route) {
		return false
	}
	for index, hop := range related.Route {
		if hop.EdgeID == "" || hop.FromNodeID == "" || hop.ToNodeID == "" || hop.FromResourceType == "" ||
			hop.ToResourceType == "" || hop.Relationship == "" ||
			(hop.StorageDirection != "INBOUND" && hop.StorageDirection != "OUTBOUND") ||
			(hop.MatchMode != "OPTIONAL" && hop.MatchMode != "REQUIRED") ||
			index > 0 && (related.Route[index-1].ToNodeID != hop.FromNodeID || related.Route[index-1].ToResourceType != hop.FromResourceType) {
			return false
		}
		traversal := traversals[index]
		wantDirection := ir.PhysicalInbound
		if hop.StorageDirection == "OUTBOUND" {
			wantDirection = ir.PhysicalOutbound
		}
		if traversal.Traversal == nil || traversal.Source.SemanticNode != hop.ToNodeID ||
			traversal.Source.ResourceType != hop.ToResourceType || traversal.Source.Relationship != hop.Relationship ||
			traversal.Traversal.Direction != wantDirection || traversal.Traversal.TargetTypeBindKey == "" ||
			bindVars[traversal.Traversal.TargetTypeBindKey] != hop.ToResourceType {
			return false
		}
	}
	last := related.Route[len(related.Route)-1]
	return last.ToNodeID == related.TargetNodeID && last.ToResourceType == related.TargetResourceType
}

func rowLineageStandaloneGroupRows(output lower.CompiledRecipeOutput) bool {
	if output.Plan.StageSequence != nil || len(output.Plan.Operations) != 1 || output.RowIdentity == nil ||
		output.RowIdentity.Grain != "groups" || !reflect.DeepEqual(output.RowIdentity.Fields, []string{"group_revision_id", "group_id"}) ||
		output.RootResourceType == "" || output.Plan.Source.ResourceType != output.RootResourceType {
		return false
	}
	operation := output.Plan.Operations[0]
	if operation.Kind != ir.PhysicalGroupRowsOp || operation.GroupRows == nil || operation.Source.ResourceType != output.RootResourceType {
		return false
	}
	rows := operation.GroupRows
	revisionID, ok := output.Plan.BindVars[rows.RevisionIDBindKey].(string)
	return ok && strings.TrimSpace(revisionID) != "" &&
		rows.RevisionCollectionBindKey != "" && rows.SelectionCollectionBindKey != "" &&
		rows.DefinitionsCollectionBindKey != "" && rows.MembershipsCollectionBindKey != "" &&
		rows.SelectionMembersCollectionBindKey != "" && rows.ResourceCollectionBindKey != ""
}

func rowLineageCohortGroupFilterSuffix(output lower.CompiledRecipeOutput) bool {
	sequence := output.Plan.StageSequence
	if sequence == nil || len(sequence.Stages) < 2 || sequence.SourceRowIdentity != "_key" ||
		sequence.Stages[0].Kind != ir.PhysicalStageCohortGroupOp || sequence.Stages[0].CohortGroup == nil ||
		sequence.Stages[0].InputStageID != sequence.SourceStageID || !rowLineageHasDirectRootSource(output.Plan.Operations) ||
		output.RowIdentity == nil || output.RowIdentity.Grain != "groups" {
		return false
	}
	rows := sequence.Stages[0].CohortGroup.Rows
	revisionID, ok := output.Plan.BindVars[rows.RevisionIDBindKey].(string)
	if !ok || strings.TrimSpace(revisionID) == "" || rows.RevisionCollectionBindKey == "" ||
		rows.SelectionCollectionBindKey == "" || rows.DefinitionsCollectionBindKey == "" ||
		rows.MembershipsCollectionBindKey == "" || rows.SelectionMembersCollectionBindKey == "" ||
		rows.ResourceCollectionBindKey == "" {
		return false
	}
	prior := sequence.Stages[0]
	for _, stage := range sequence.Stages[1:] {
		if stage.Kind != ir.PhysicalStageFilterOp || stage.Filter == nil || stage.Filter.Expression != nil ||
			stage.InputStageID != prior.ID || stage.RowIdentityColumn != prior.RowIdentityColumn ||
			!reflect.DeepEqual(stage.InputColumns, prior.OutputColumns) ||
			stage.Filter.Predicate.Left.Variable != stage.InputRowVariable || len(stage.Filter.Predicate.Left.Path) != 1 {
			return false
		}
		columnName := stage.Filter.Predicate.Left.Path[0]
		if columnName != "group_id" && columnName != "group_label" && columnName != "group_ordinal" {
			return false
		}
		column, found := physicalStageColumn(stage.InputColumns, columnName)
		outputColumn, outputFound := physicalStageColumn(stage.OutputColumns, columnName)
		if !found || !outputFound || column.Internal || column.Cardinality == "many" ||
			!reflect.DeepEqual(column, outputColumn) {
			return false
		}
		operator := strings.ToUpper(strings.TrimSpace(stage.Filter.Predicate.Operator))
		switch operator {
		case "EXISTS", "MISSING":
			if stage.Filter.Predicate.Right != nil {
				return false
			}
		case "EQUALS", "NOT_EQUALS", "IN", "GT", "GTE", "LT", "LTE", "CONTAINS_TEXT":
			if stage.Filter.Predicate.Right == nil || stage.Filter.Predicate.Right.BindKey == "" {
				return false
			}
		default:
			return false
		}
		prior = stage
	}
	last := sequence.Stages[len(sequence.Stages)-1]
	return prior.ID == sequence.FinalStageID && prior.RowIdentityColumn == sequence.FinalRowIdentity &&
		rowLineageHasPhysicalIdentity(output, last, sequence)
}

func physicalStageColumn(columns []ir.PhysicalStageColumn, name string) (ir.PhysicalStageColumn, bool) {
	for _, column := range columns {
		if column.Name == name {
			return column, true
		}
	}
	return ir.PhysicalStageColumn{}, false
}

func rowLineageGroupFilterSuffix(sequence *ir.PhysicalStageSequence) bool {
	if sequence == nil || len(sequence.Stages) < 2 || sequence.Stages[0].Kind != ir.PhysicalStageGroupOp || sequence.Stages[0].Group == nil {
		return false
	}
	prior := sequence.Stages[0]
	for _, stage := range sequence.Stages[1:] {
		if stage.Kind != ir.PhysicalStageFilterOp || stage.Filter == nil || stage.InputStageID != prior.ID ||
			stage.RowIdentityColumn != prior.RowIdentityColumn || !reflect.DeepEqual(stage.InputColumns, prior.OutputColumns) ||
			!reflect.DeepEqual(stage.OutputColumns, stage.InputColumns) {
			return false
		}
		prior = stage
	}
	return prior.ID == sequence.FinalStageID && prior.RowIdentityColumn == sequence.FinalRowIdentity
}

func rowLineageHasPhysicalIdentity(output lower.CompiledRecipeOutput, stage ir.PhysicalConstructionStage, sequence *ir.PhysicalStageSequence) bool {

	return output.RowIdentity != nil && stage.RowIdentityColumn != "" && stage.RowIdentityColumn == sequence.FinalRowIdentity
}

func rowLineageHasDirectRootSource(operations []ir.PhysicalOperation) bool {
	rootScans := 0
	for _, operation := range operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			rootScans++
			if operation.RootScan == nil {
				return false
			}
		case ir.PhysicalFilterOp, ir.PhysicalSetOp, ir.PhysicalReturnOp, ir.PhysicalDerivedLetOp, ir.PhysicalExpressionLetOp:
		default:
			return false
		}
	}
	return rootScans == 1
}
