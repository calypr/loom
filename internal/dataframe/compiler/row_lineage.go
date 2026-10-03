package compiler

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
)

const (
	MaxRowLineageContributors = 100
	DefaultRowLineageLimit    = 25
	rowLineageRowIDBind       = "row_lineage_row_id"
	rowLineageOffsetBind      = "row_lineage_offset"
	rowLineageLimitBind       = "row_lineage_limit"
	rowLineageFetchLimitBind  = "row_lineage_fetch_limit"
	rowLineageResourceID      = "__loom_row_lineage_resource_id"
	rowLineageOccurrenceKey   = "__loom_row_lineage_occurrence_key"
)

type RowLineageCapability struct {
	Available  bool
	ReasonCode string
	Operation  string
}

type RowLineageUnsupportedError struct {
	Capability RowLineageCapability
}

func (err *RowLineageUnsupportedError) Error() string {
	if err == nil {
		return "row lineage is unsupported"
	}
	return fmt.Sprintf("row lineage is unsupported for %s (%s)", err.Capability.Operation, err.Capability.ReasonCode)
}

var _ error = (*RowLineageUnsupportedError)(nil)

type CompiledRowLineageQuery struct {
	Query              string
	BindVars           map[string]any
	Offset             int
	Limit              int
	FoundColumn        string
	ContributorsColumn string
	HasMoreColumn      string
}

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
	if rowLineageHasRelatedExpand(sequence) {
		return rowLineageRelatedSequenceCapability(output)
	}
	if rowLineageCohortGroupFilterSuffix(output) {
		return RowLineageCapability{Available: true}
	}
	stage := sequence.Stages[0]
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

// rowLineageConstructionPivotPreimage admits only a direct terminal ordinary
// construction Pivot. Its typed key tuple selects the multi-root preimage;
// mixed-owner sequences remain unsupported until their owner chain is decoded.
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
	// Publication-grain metadata need not name a construction stage's physical row ID.
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

func CompileRowLineageOutput(output lower.CompiledRecipeOutput, rowID string, offset, limit int, policy ir.PhysicalOptimizationPolicy) (CompiledRowLineageQuery, error) {
	rowID = strings.TrimSpace(rowID)
	if rowID == "" {
		return CompiledRowLineageQuery{}, fmt.Errorf("row lineage row ID is required")
	}
	if offset < 0 {
		return CompiledRowLineageQuery{}, fmt.Errorf("row lineage offset cannot be negative")
	}
	if limit == 0 {
		limit = DefaultRowLineageLimit
	}
	if limit < 1 || limit > MaxRowLineageContributors {
		return CompiledRowLineageQuery{}, fmt.Errorf("row lineage limit must be between 1 and %d", MaxRowLineageContributors)
	}
	capability := RowLineageCapabilityForOutput(output)
	if !capability.Available {
		return CompiledRowLineageQuery{}, &RowLineageUnsupportedError{Capability: capability}
	}
	if output.RootResourceType == "" {
		return CompiledRowLineageQuery{}, fmt.Errorf("row lineage source resource type is required")
	}
	if output.RowIdentity == nil {
		return CompiledRowLineageQuery{}, fmt.Errorf("row lineage requires stable output row identity")
	}
	if rowLineageCohortGroupFilterSuffix(output) {
		sequence := output.Plan.StageSequence
		groupStage := sequence.Stages[0]
		rows := groupStage.CohortGroup.Rows
		revisionID, _ := output.Plan.BindVars[rows.RevisionIDBindKey].(string)
		requestedRevisionID, requestedGroupID, canonical := parseGroupRowsRowID(rowID)
		if !canonical || requestedRevisionID != revisionID {
			requestedRevisionID, requestedGroupID = "", ""
		}
		rendered, err := aql.RenderPhysicalCohortGroupRowsLineage(
			ir.ClonePhysicalPlan(output.Plan), groupStage, sequence.Stages[1:], requestedRevisionID, requestedGroupID, offset, limit,
		)
		if err != nil {
			return CompiledRowLineageQuery{}, fmt.Errorf("render composed cohort row lineage: %w", err)
		}
		return CompiledRowLineageQuery{
			Query: rendered.Query, BindVars: rendered.BindVars, Offset: offset, Limit: limit,
			FoundColumn: "found", ContributorsColumn: "contributors", HasMoreColumn: "hasMore",
		}, nil
	}
	if rowLineageStandaloneGroupRows(output) {
		rows := output.Plan.Operations[0].GroupRows
		revisionID, _ := output.Plan.BindVars[rows.RevisionIDBindKey].(string)
		requestedRevisionID, requestedGroupID, canonical := parseGroupRowsRowID(rowID)
		if !canonical || requestedRevisionID != revisionID {
			requestedRevisionID, requestedGroupID = "", ""
		}
		rendered, err := aql.RenderPhysicalGroupRowsLineage(ir.ClonePhysicalPlan(output.Plan), requestedRevisionID, requestedGroupID, offset, limit)
		if err != nil {
			return CompiledRowLineageQuery{}, fmt.Errorf("render explicit group row lineage: %w", err)
		}
		return CompiledRowLineageQuery{
			Query: rendered.Query, BindVars: rendered.BindVars, Offset: offset, Limit: limit,
			FoundColumn: "found", ContributorsColumn: "contributors", HasMoreColumn: "hasMore",
		}, nil
	}
	physical := ir.ClonePhysicalPlan(output.Plan)
	_ = policy // Row lineage uses the canonical pre-optimization stage semantics.
	if physical.StageSequence == nil {
		return CompiledRowLineageQuery{}, fmt.Errorf("row lineage requires a construction stage sequence")
	}
	physical, err := withGenericPhysicalExecutionWindow(physical, 0)
	if err != nil {
		return CompiledRowLineageQuery{}, fmt.Errorf("apply row lineage execution window: %w", err)
	}
	root := ""
	for _, operation := range physical.Operations {
		if operation.Kind == ir.PhysicalRootScanOp && operation.RootScan != nil {
			root = operation.RootScan.Variable
			break
		}
	}
	if root == "" {
		return CompiledRowLineageQuery{}, fmt.Errorf("row lineage requires a direct root source")
	}
	var lineageTrace *ir.PhysicalRowLineageTrace
	if rowLineageHasRelatedExpand(physical.StageSequence) {
		rootKeyBindKey := "row_lineage_root_key"
		lineageTrace = &ir.PhysicalRowLineageTrace{RootKeyBindKey: rootKeyBindKey}
		rootKey, decoded, decodedOK := decodeRelatedRowLineageIdentity(rowID, physical.StageSequence.Stages, physical.BindVars)
		var rootKeyValue any = rootKey
		if !decodedOK {
			// Null cannot be a stored Arango _key, so malformed, wrong-stage, and
			// wrong-owner identities become an unmatchable query.
			rootKeyValue = nil
			for stageIndex, stage := range physical.StageSequence.Stages {
				if stage.Kind != ir.PhysicalStageRelatedExpandOp {
					continue
				}
				decoded = append(decoded, decodedRelatedLineageStage{
					StageID: stage.ID, RowID: rowID, TerminalID: "", RowKind: "RELATED", StageIndex: stageIndex,
				})
			}
		}
		physical.BindVars[rootKeyBindKey] = rootKeyValue
		for _, owner := range decoded {
			rowBindKey, terminalBindKey := relatedLineageStageBindKeys(owner.StageIndex)
			physical.BindVars[rowBindKey] = owner.RowID
			physical.BindVars[terminalBindKey] = owner.TerminalID
			lineageTrace.Stages = append(lineageTrace.Stages, ir.PhysicalRowLineageStageMatch{
				StageID: owner.StageID, Kind: ir.PhysicalStageRelatedExpandOp,
				StageRowIDBindKey: rowBindKey, RelatedTerminalIDBindKey: terminalBindKey,
				RelatedRowKind: owner.RowKind,
			})
		}
		rootFilter := ir.PhysicalOperation{
			Kind: ir.PhysicalFilterOp,
			Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator: "EQUALS", Left: ir.PhysicalValue{Variable: root, Path: []string{"_key"}},
				Right: &ir.PhysicalValue{BindKey: rootKeyBindKey},
			}},
		}
		if len(physical.Operations) == 0 || physical.Operations[0].Kind != ir.PhysicalRootScanOp {
			return CompiledRowLineageQuery{}, fmt.Errorf("row lineage could not anchor its direct root source")
		}
		if err := ir.ValidateGenericPhysicalPlanScope(physical); err != nil {
			return CompiledRowLineageQuery{}, fmt.Errorf("validate row lineage source scopes before anchoring: %w", err)
		}
		anchorIndex := ir.PhysicalScopeWindowEnd(physical.Operations, 1)
		if anchorIndex <= 1 || anchorIndex >= len(physical.Operations) {
			return CompiledRowLineageQuery{}, fmt.Errorf("row lineage source has no validated root scope before its projection")
		}
		physical.Operations = append(physical.Operations[:anchorIndex], append([]ir.PhysicalOperation{rootFilter}, physical.Operations[anchorIndex:]...)...)
	} else if rowLineageConstructionPivotPreimage(output) {
		stage := physical.StageSequence.Stages[0]
		match := ir.PhysicalRowLineageStageMatch{
			StageID: stage.ID, Kind: ir.PhysicalStagePivotOp, StageRowIDBindKey: rowLineageRowIDBind,
			IdentityKeyBindKeys: make([]string, len(stage.GroupedPivot.GroupKeys)),
		}
		for index := range stage.GroupedPivot.GroupKeys {
			bindKey := fmt.Sprintf("row_lineage_pivot_group_key_%d", index)
			physical.BindVars[bindKey] = nil
			match.IdentityKeyBindKeys[index] = bindKey
		}
		if decoded, ok := decodeConstructionPivotLineageIdentity(rowID, *stage.GroupedPivot); ok {
			for index, bindKey := range match.IdentityKeyBindKeys {
				physical.BindVars[bindKey] = decoded[index]
			}
		}
		lineageTrace = &ir.PhysicalRowLineageTrace{Stages: []ir.PhysicalRowLineageStageMatch{match}}
	}
	returnCount := 0
	for index := range physical.Operations {
		operation := &physical.Operations[index]
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		returnCount++
		operation.Return.Projections = appendUniqueProjection(operation.Return.Projections,
			ir.PhysicalProjection{Name: rowLineageResourceID, Hidden: true, Value: ir.PhysicalValue{Variable: root, Path: []string{"payload", "id"}}},
		)
		operation.Return.Projections = appendUniqueProjection(operation.Return.Projections,
			ir.PhysicalProjection{Name: rowLineageOccurrenceKey, Hidden: true, Value: ir.PhysicalValue{Variable: root, Path: []string{"_key"}}},
		)
	}
	if returnCount != 1 {
		return CompiledRowLineageQuery{}, fmt.Errorf("row lineage direct source requires one terminal projection")
	}
	physical.BindVars[rowLineageRowIDBind] = rowID
	physical.BindVars[rowLineageOffsetBind] = offset
	physical.BindVars[rowLineageLimitBind] = limit
	physical.BindVars[rowLineageFetchLimitBind] = limit + 1
	physical.StageSequence.RowLineageReturn = &ir.PhysicalRowLineageReturn{
		RowIDBindKey: rowLineageRowIDBind, OffsetBindKey: rowLineageOffsetBind,
		LimitBindKey: rowLineageLimitBind, FetchLimitBindKey: rowLineageFetchLimitBind,
		Trace:        lineageTrace,
		ResourceType: output.RootResourceType, ResourceIDColumn: rowLineageResourceID,
		OccurrenceKeyColumn: rowLineageOccurrenceKey,
	}
	if err := physical.Validate(); err != nil {
		return CompiledRowLineageQuery{}, fmt.Errorf("validate row lineage physical plan: %w", err)
	}
	if err := ir.ValidateGenericPhysicalPlanScope(physical); err != nil {
		return CompiledRowLineageQuery{}, fmt.Errorf("verify row lineage physical scope: %w", err)
	}
	rendered, err := aql.RenderPhysicalPlan(physical)
	if err != nil {
		return CompiledRowLineageQuery{}, fmt.Errorf("render row lineage physical plan: %w", err)
	}
	return CompiledRowLineageQuery{
		Query: rendered.Query, BindVars: rendered.BindVars, Offset: offset, Limit: limit,
		FoundColumn: "found", ContributorsColumn: "contributors", HasMoreColumn: "hasMore",
	}, nil
}

func decodeConstructionPivotLineageIdentity(rowID string, pivot ir.PhysicalGroupedPivot) ([]any, bool) {
	decoder := json.NewDecoder(strings.NewReader(rowID))
	decoder.UseNumber()
	var identity []json.RawMessage
	if decoder.Decode(&identity) != nil || len(identity) != len(pivot.GroupKeys)+2 {
		return nil, false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, false
	}
	var marker, constructionID string
	if json.Unmarshal(identity[0], &marker) != nil || marker != "GROUPED_PIVOT" ||
		json.Unmarshal(identity[1], &constructionID) != nil || constructionID != pivot.ConstructionID {
		return nil, false
	}
	keys := make([]any, len(pivot.GroupKeys))
	for index, key := range pivot.GroupKeys {
		var pair []json.RawMessage
		if json.Unmarshal(identity[index+2], &pair) != nil || len(pair) != 2 {
			return nil, false
		}
		var kind string
		if json.Unmarshal(pair[0], &kind) != nil || kind != key.Kind {
			return nil, false
		}
		value, valid := decodePivotLineageScalar(pair[1], kind)
		if !valid {
			return nil, false
		}
		keys[index] = value
	}
	return keys, true
}

func decodePivotLineageScalar(raw json.RawMessage, kind string) (any, bool) {
	if string(raw) == "null" {
		return nil, true
	}
	switch kind {
	case "STRING":
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return nil, false
		}
		return value, true
	case "INTEGER":
		var value json.Number
		if json.Unmarshal(raw, &value) != nil {
			return nil, false
		}
		integer, err := value.Int64()
		return integer, err == nil
	case "DECIMAL":
		var value json.Number
		if json.Unmarshal(raw, &value) != nil {
			return nil, false
		}
		decimal, err := value.Float64()
		return decimal, err == nil && !math.IsInf(decimal, 0) && !math.IsNaN(decimal)
	case "BOOLEAN":
		var value bool
		if json.Unmarshal(raw, &value) != nil {
			return nil, false
		}
		return value, true
	default:
		return nil, false
	}
}

func parseGroupRowsRowID(rowID string) (revisionID, groupID string, canonical bool) {
	decoder := json.NewDecoder(strings.NewReader(rowID))
	decoder.DisallowUnknownFields()
	var identity map[string]string
	if decoder.Decode(&identity) != nil || len(identity) != 2 || identity["group_revision_id"] == "" || identity["group_id"] == "" {
		return "", "", false
	}
	if decoder.Decode(new(any)) == nil {
		return "", "", false
	}
	encoded, err := json.Marshal(identity)
	if err != nil || string(encoded) != rowID {
		return "", "", false
	}
	return identity["group_revision_id"], identity["group_id"], true
}

func parseRelatedExpandRowID(rowID string, constructionID any, preserveEmpty bool) (string, string, string, bool) {
	var fields [][]string
	if err := json.Unmarshal([]byte(rowID), &fields); err != nil || len(fields) != 3 || len(fields[0]) != 2 || len(fields[1]) != 2 || len(fields[2]) == 0 {
		return "", "", "", false
	}
	expectedConstruction, constructionOK := constructionID.(string)
	if fields[0][0] != "input" || fields[0][1] == "" || fields[1][0] != "construction" ||
		!constructionOK || fields[1][1] != expectedConstruction {
		return "", "", "", false
	}
	if len(fields[2]) == 1 {
		if fields[2][0] == "empty" && preserveEmpty {
			return fields[0][1], "", "EMPTY", true
		}
		return "", "", "", false
	}
	if len(fields[2]) != 2 {
		return "", "", "", false
	}
	terminalID := fields[2][1]
	if fields[2][0] != "related" || terminalID == "" {
		return "", "", "", false
	}
	collection, key, ok := strings.Cut(terminalID, "/")
	if !ok || collection == "" || key == "" {
		return "", "", "", false
	}
	return fields[0][1], terminalID, "RELATED", true
}

type decodedRelatedLineageStage struct {
	StageID    string
	RowID      string
	TerminalID string
	RowKind    string
	StageIndex int
}

func relatedLineageStageBindKeys(stageIndex int) (rowIDBindKey, terminalIDBindKey string) {
	return fmt.Sprintf("row_lineage_stage_%d_row_id", stageIndex), fmt.Sprintf("row_lineage_stage_%d_terminal_id", stageIndex)
}

func decodeRelatedRowLineageIdentity(rowID string, stages []ir.PhysicalConstructionStage, bindVars map[string]any) (string, []decodedRelatedLineageStage, bool) {
	current := rowID
	backward := make([]decodedRelatedLineageStage, 0)
	for index := len(stages) - 1; index >= 0; index-- {
		stage := stages[index]
		if stage.Kind != ir.PhysicalStageRelatedExpandOp {
			continue
		}
		related := stage.RelatedExpand
		if related == nil {
			return "", nil, false
		}
		parent, terminalID, rowKind, ok := parseRelatedExpandRowID(
			current, bindVars[related.ConstructionIDBindKey],
			related.EmptyPolicy == ir.PhysicalUnnestPreserveParent,
		)
		if !ok {
			return "", nil, false
		}
		backward = append(backward, decodedRelatedLineageStage{
			StageID: stage.ID, RowID: current, TerminalID: terminalID, RowKind: rowKind, StageIndex: index,
		})
		current = parent
	}
	if current == "" || len(backward) == 0 {
		return "", nil, false
	}
	for left, right := 0, len(backward)-1; left < right; left, right = left+1, right-1 {
		backward[left], backward[right] = backward[right], backward[left]
	}
	return current, backward, true
}
