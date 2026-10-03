package compiler

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
)

// publicOutputColumns returns the ordered transport schema owned by the
// compiler. Physical identity and executor-validation projections are kept in
// OutputSchema but never leak into dataframe JSON/CSV/publication columns.
func publicOutputColumns(schema []lower.CompiledOutputColumn) []string {
	columns := make([]string, 0, len(schema))
	for _, column := range schema {
		if column.Internal {
			continue
		}
		columns = append(columns, column.Name)
	}
	return columns
}

// PublicOutputColumns returns a copy of the compiler-owned transport schema.
// It is used by recipe execution adapters that need to carry the schema with
// streamed rows without re-walking semantic recipe nodes.
func PublicOutputColumns(schema []lower.CompiledOutputColumn) []string {
	return publicOutputColumns(schema)
}

const genericPhysicalExecutionLimitBind = "limit"

func physicalProjectionMetadata(plan ir.PhysicalPlan) ([]string, []string) {
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalGroupRowsOp && operation.GroupRows != nil {
			return []string{"group_revision_id", "group_id", "group_label", "group_ordinal", "members", "__loom_row_id"}, nil
		}
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		columns := make([]string, 0, len(operation.Return.Projections))
		var pivots []string
		for _, projection := range operation.Return.Projections {
			if projection.Hidden {
				continue
			}
			columns = append(columns, projection.Name)
			if projection.Expression != nil && projection.Expression.Kind == ir.PhysicalPivotExpression {
				pivots = append(pivots, projection.Name)
			}
		}
		return columns, pivots
	}
	return nil, nil
}

func physicalTraversalCount(plan ir.PhysicalPlan) int {
	count := 0
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalTraversalOp || operation.Kind == ir.PhysicalPathExtendOp {
			count++
		}
	}
	return count
}

// withGenericPhysicalExecutionWindow inserts the deterministic root ordering
// and optional preview bound before any traversal LET subquery, ensuring an
// expensive optional navigation is evaluated only for selected root rows.
func withGenericPhysicalExecutionWindow(plan ir.PhysicalPlan, limit int) (ir.PhysicalPlan, error) {
	if plan.StageSequence != nil {
		out := clonePhysicalPlan(plan)
		if limit > 0 {
			if _, exists := out.BindVars[genericPhysicalExecutionLimitBind]; exists {
				return ir.PhysicalPlan{}, fmt.Errorf("generic physical execution limit bind %q is already defined", genericPhysicalExecutionLimitBind)
			}
			out.BindVars[genericPhysicalExecutionLimitBind] = limit
			out.StageSequence.PreviewLimitBindKey = genericPhysicalExecutionLimitBind
			if out.StageSequence.PreviewSourceWindowByRootID && !insertConstructionPreviewRootIDWindow(&out) {
				// Optimizer rewrites may make the source shape ineligible. The
				// canonical source path remains correct; only skip this preview
				// optimization when its root-scan insertion point is unavailable.
				out.StageSequence.PreviewSourceWindowByRootID = false
			}
			out.StageSequence.PreviewTerminalPivotWindow = previewTerminalPivotWindowEligible(out.StageSequence)
		}
		if err := ir.ValidateGenericPhysicalPlanScope(out); err != nil {
			return ir.PhysicalPlan{}, fmt.Errorf("validate construction physical execution scope: %w", err)
		}
		if err := out.Validate(); err != nil {
			return ir.PhysicalPlan{}, fmt.Errorf("validate construction physical execution window: %w", err)
		}
		return out, nil
	}
	if len(plan.Operations) == 1 && plan.Operations[0].Kind == ir.PhysicalGroupRowsOp && plan.Operations[0].GroupRows != nil {
		out := clonePhysicalPlan(plan)
		if limit > 0 {
			if _, exists := out.BindVars[genericPhysicalExecutionLimitBind]; exists {
				return ir.PhysicalPlan{}, fmt.Errorf("generic physical execution limit bind %q is already defined", genericPhysicalExecutionLimitBind)
			}
			out.BindVars[genericPhysicalExecutionLimitBind] = limit
			out.Operations[0].GroupRows.LimitBindKey = genericPhysicalExecutionLimitBind
		}
		if err := out.Validate(); err != nil {
			return ir.PhysicalPlan{}, fmt.Errorf("validate grouped physical execution window: %w", err)
		}
		return out, nil
	}
	if err := ir.ValidateGenericPhysicalPlanScope(plan); err != nil {
		return ir.PhysicalPlan{}, fmt.Errorf("validate generic physical execution scope: %w", err)
	}
	if len(plan.Operations) == 0 || plan.Operations[0].Kind != ir.PhysicalRootScanOp || plan.Operations[0].RootScan == nil {
		return ir.PhysicalPlan{}, fmt.Errorf("generic physical execution plan requires a root scan")
	}
	if reshapeIndex, outputVariable := physicalTableReshapeOutput(plan.Operations); reshapeIndex >= 0 {
		out := clonePhysicalPlan(plan)
		if limit > 0 {
			if _, exists := out.BindVars[genericPhysicalExecutionLimitBind]; exists {
				return ir.PhysicalPlan{}, fmt.Errorf("generic physical execution limit bind %q is already defined", genericPhysicalExecutionLimitBind)
			}
			out.BindVars[genericPhysicalExecutionLimitBind] = limit
		}
		window := []ir.PhysicalOperation{{
			Kind: ir.PhysicalSortOp, Source: ir.PhysicalSource{SemanticField: "table_shape.row_id"},
			Sort: &ir.PhysicalSort{Keys: []ir.PhysicalValue{{Variable: outputVariable, Path: []string{"__loom_row_id"}}}},
		}}
		if limit > 0 {
			window = append(window, ir.PhysicalOperation{
				Kind: ir.PhysicalLimitOp, Source: ir.PhysicalSource{SemanticField: "table_shape.row_id"},
				Limit: &ir.PhysicalLimit{BindKey: genericPhysicalExecutionLimitBind},
			})
		}
		insertAt := reshapeIndex + 1
		operations := make([]ir.PhysicalOperation, 0, len(out.Operations)+len(window))
		operations = append(operations, out.Operations[:insertAt]...)
		operations = append(operations, window...)
		operations = append(operations, out.Operations[insertAt:]...)
		out.Operations = operations
		if err := ir.ValidateGenericPhysicalPlanScope(out); err != nil {
			return ir.PhysicalPlan{}, fmt.Errorf("validate reshaped physical execution window: %w", err)
		}
		return out, nil
	}

	// The generic scope verifier defines the root scope as every operation up
	// to the first traversal or terminal return. BuildGenericPhysicalPlan has
	// already proven that the whole prefix scopes the root correctly.
	insertAt := physicalScopeWindowEnd(plan.Operations, 1)
	// Shared expression LETs are deliberately emitted immediately before
	// RETURN for root-only plans. Put the root execution window before them so
	// AQL can discard rows before evaluating the family maps, while traversal
	// plans still insert the window before their first child SET.
	for insertAt > 1 && insertAt <= len(plan.Operations) && plan.Operations[insertAt-1].Kind == ir.PhysicalExpressionLetOp {
		insertAt--
	}
	if insertAt <= 1 || insertAt >= len(plan.Operations) {
		return ir.PhysicalPlan{}, fmt.Errorf("generic physical execution plan requires a scoped root followed by RETURN or traversal")
	}

	out := clonePhysicalPlan(plan)
	root := out.Operations[0].RootScan.Variable
	rootKey := ir.PhysicalValue{Variable: root, Path: []string{"_key"}}
	keys := []ir.PhysicalValue{rootKey}
	unnestCount := 0
	for _, operation := range out.Operations {
		if operation.Kind != ir.PhysicalUnnestOp || operation.Unnest == nil {
			continue
		}
		unnestCount++
		if unnestCount > 1 {
			return ir.PhysicalPlan{}, fmt.Errorf("generic execution window supports one row expansion")
		}
		keys = ir.PhysicalUnnestSortKeys(*operation.Unnest)
	}
	window := []ir.PhysicalOperation{{
		Kind:   ir.PhysicalSortOp,
		Source: ir.PhysicalSource{SemanticNode: out.Source.SemanticNode, ResourceType: out.Source.ResourceType, SemanticField: "_key"},
		Sort:   &ir.PhysicalSort{Keys: keys},
	}}
	if limit > 0 {
		if _, exists := out.BindVars[genericPhysicalExecutionLimitBind]; exists {
			return ir.PhysicalPlan{}, fmt.Errorf("generic physical execution limit bind %q is already defined", genericPhysicalExecutionLimitBind)
		}
		out.BindVars[genericPhysicalExecutionLimitBind] = limit
		window = append(window, ir.PhysicalOperation{
			Kind:   ir.PhysicalLimitOp,
			Source: ir.PhysicalSource{SemanticNode: out.Source.SemanticNode, ResourceType: out.Source.ResourceType},
			Limit:  &ir.PhysicalLimit{BindKey: genericPhysicalExecutionLimitBind},
		})
	}
	operations := make([]ir.PhysicalOperation, 0, len(out.Operations)+len(window))
	operations = append(operations, out.Operations[:insertAt]...)
	operations = append(operations, window...)
	operations = append(operations, out.Operations[insertAt:]...)
	out.Operations = operations
	if err := ir.ValidateGenericPhysicalPlanScope(out); err != nil {
		return ir.PhysicalPlan{}, fmt.Errorf("validate generic physical execution window: %w", err)
	}
	return out, nil
}

func previewTerminalPivotWindowEligible(sequence *ir.PhysicalStageSequence) bool {
	if sequence == nil || sequence.PreviewSourceWindowByRootID || len(sequence.Stages) == 0 {
		return false
	}
	stage := sequence.Stages[len(sequence.Stages)-1]
	return stage.ID == sequence.FinalStageID &&
		stage.Kind == ir.PhysicalStagePivotOp &&
		stage.GroupedPivot != nil &&
		!stage.GroupedPivot.OneInputRowPerGroup &&
		len(stage.GroupedPivot.GroupKeys) > 0 &&
		stage.OutputRowVariable == stage.GroupedPivot.OutputRowVariable &&
		stage.RowIdentityColumn == sequence.FinalRowIdentity
}

func insertConstructionPreviewRootIDWindow(plan *ir.PhysicalPlan) bool {
	if plan == nil || plan.StageSequence == nil || plan.StageSequence.PreviewLimitBindKey == "" ||
		len(plan.Operations) < 2 || plan.Operations[0].Kind != ir.PhysicalRootScanOp || plan.Operations[0].RootScan == nil {
		return false
	}
	root := plan.Operations[0].RootScan.Variable
	insertAt := -1
	lastFilter := 0
	for index, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 {
				return false
			}
		case ir.PhysicalFilterOp:
			lastFilter = index
		case ir.PhysicalDerivedLetOp, ir.PhysicalExpressionLetOp, ir.PhysicalSetOp:
		case ir.PhysicalReturnOp:
			if insertAt >= 0 || index != len(plan.Operations)-1 {
				return false
			}
			insertAt = index
		default:
			return false
		}
	}
	if insertAt <= 1 {
		return false
	}
	insertAt = lastFilter + 1
	window := []ir.PhysicalOperation{
		{
			Kind:   ir.PhysicalSortOp,
			Source: ir.PhysicalSource{SemanticNode: plan.Source.SemanticNode, ResourceType: plan.Source.ResourceType, SemanticField: "id"},
			// Loom's FHIR ingest stores the validated logical resource id both
			// at the document root and in payload. The root field is indexed.
			Sort: &ir.PhysicalSort{Keys: []ir.PhysicalValue{{Variable: root, Path: []string{"id"}}}},
		},
		{
			Kind:   ir.PhysicalLimitOp,
			Source: ir.PhysicalSource{SemanticNode: plan.Source.SemanticNode, ResourceType: plan.Source.ResourceType},
			Limit:  &ir.PhysicalLimit{BindKey: plan.StageSequence.PreviewLimitBindKey},
		},
	}
	operations := make([]ir.PhysicalOperation, 0, len(plan.Operations)+len(window))
	operations = append(operations, plan.Operations[:insertAt]...)
	operations = append(operations, window...)
	operations = append(operations, plan.Operations[insertAt:]...)
	plan.Operations = operations
	return true
}

func physicalTableReshapeOutput(operations []ir.PhysicalOperation) (int, string) {
	for index, operation := range operations {
		switch operation.Kind {
		case ir.PhysicalGroupedPivotOp:
			if operation.GroupedPivot != nil {
				return index, operation.GroupedPivot.OutputRowVariable
			}
		case ir.PhysicalUnpivotOp:
			if operation.Unpivot != nil {
				return index, operation.Unpivot.OutputRowVariable
			}
		}
	}
	return -1, ""
}
