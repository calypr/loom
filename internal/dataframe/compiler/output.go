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
