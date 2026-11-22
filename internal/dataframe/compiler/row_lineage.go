package compiler

import (
	"fmt"
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
	expandedCodingGroup := rowLineageExpandedCodingGroup(output)
	relatedCodingGroup := rowLineageRelatedCodingGroup(output)
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
	_ = policy
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
	} else if rowLineageConstructionGroupPivotPreimage(output) {
		stage := physical.StageSequence.Stages[1]
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
	occurrenceKeyColumn := rowLineageOccurrenceKey
	if expandedCodingGroup {
		occurrenceKeyColumn = physical.StageSequence.Stages[0].Group.CodingKey.SourceOccurrenceKeyColumn
	} else if relatedCodingGroup {
		occurrenceKeyColumn = physical.StageSequence.Stages[0].Group.RelatedOccurrence.OccurrenceKeyColumn
	}
	for index := range physical.Operations {
		operation := &physical.Operations[index]
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		returnCount++
		operation.Return.Projections = appendUniqueProjection(operation.Return.Projections,
			ir.PhysicalProjection{Name: rowLineageResourceID, Hidden: true, Value: ir.PhysicalValue{Variable: root, Path: []string{"payload", "id"}}},
		)
		if expandedCodingGroup {
			found := false
			for _, projection := range operation.Return.Projections {
				if projection.Name == occurrenceKeyColumn {
					found = true
					break
				}
			}
			if !found {
				return CompiledRowLineageQuery{}, fmt.Errorf("expanded Group source is missing exact occurrence projection %q", occurrenceKeyColumn)
			}
		} else if !relatedCodingGroup {
			operation.Return.Projections = appendUniqueProjection(operation.Return.Projections,
				ir.PhysicalProjection{Name: rowLineageOccurrenceKey, Hidden: true, Value: ir.PhysicalValue{Variable: root, Path: []string{"_key"}}},
			)
		}
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
		OccurrenceKeyColumn: occurrenceKeyColumn,
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
