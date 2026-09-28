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

func RowLineageCapabilityForOutput(output lower.CompiledRecipeOutput) RowLineageCapability {
	sequence := output.Plan.StageSequence
	if sequence == nil || len(sequence.Stages) == 0 {
		return RowLineageCapability{ReasonCode: "ROW_LINEAGE_OPERATION_UNSUPPORTED", Operation: "SOURCE"}
	}
	if len(sequence.Stages) != 1 {
		return RowLineageCapability{ReasonCode: "ROW_LINEAGE_OPERATION_UNSUPPORTED", Operation: string(sequence.Stages[1].Kind)}
	}
	stage := sequence.Stages[0]
	if stage.Kind != ir.PhysicalStageGroupOp || stage.Group == nil || stage.InputStageID != sequence.SourceStageID || stage.ID != sequence.FinalStageID {
		return RowLineageCapability{ReasonCode: "ROW_LINEAGE_OPERATION_UNSUPPORTED", Operation: string(stage.Kind)}
	}
	for _, operation := range output.Plan.Operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp, ir.PhysicalFilterOp, ir.PhysicalSetOp, ir.PhysicalReturnOp, ir.PhysicalDerivedLetOp, ir.PhysicalExpressionLetOp:
		default:
			return RowLineageCapability{ReasonCode: "ROW_LINEAGE_SOURCE_NOT_DIRECT", Operation: string(operation.Kind)}
		}
	}
	if output.RowIdentity == nil || len(output.RowIdentity.Fields) != 1 || output.RowIdentity.Fields[0] != sequence.FinalRowIdentity {
		return RowLineageCapability{ReasonCode: "ROW_LINEAGE_IDENTITY_UNAVAILABLE", Operation: string(stage.Kind)}
	}
	return RowLineageCapability{Available: true}
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
