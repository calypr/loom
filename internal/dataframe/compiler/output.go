package compiler

import "fmt"

// publicOutputColumns returns the ordered transport schema owned by the
// compiler. Physical identity and executor-validation projections are kept in
// OutputSchema but never leak into dataframe JSON/CSV/publication columns.
func publicOutputColumns(schema []CompiledOutputColumn) []string {
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
func PublicOutputColumns(schema []CompiledOutputColumn) []string {
	return publicOutputColumns(schema)
}

const genericPhysicalExecutionLimitBind = "limit"

func physicalProjectionMetadata(plan PhysicalPlan) ([]string, []string) {
	for _, operation := range plan.Operations {
		if operation.Kind != PhysicalReturnOp || operation.Return == nil {
			continue
		}
		columns := make([]string, 0, len(operation.Return.Projections))
		var pivots []string
		for _, projection := range operation.Return.Projections {
			if projection.Hidden {
				continue
			}
			columns = append(columns, projection.Name)
			if projection.Expression != nil && projection.Expression.Kind == PhysicalPivotExpression {
				pivots = append(pivots, projection.Name)
			}
		}
		return columns, pivots
	}
	return nil, nil
}

func physicalTraversalCount(plan PhysicalPlan) int {
	count := 0
	for _, operation := range plan.Operations {
		if operation.Kind == PhysicalTraversalOp || operation.Kind == PhysicalPathExtendOp {
			count++
		}
	}
	return count
}

// withGenericPhysicalExecutionWindow inserts the deterministic root ordering
// and optional preview bound before any traversal LET subquery, ensuring an
// expensive optional navigation is evaluated only for selected root rows.
func withGenericPhysicalExecutionWindow(plan PhysicalPlan, limit int) (PhysicalPlan, error) {
	if err := ValidateGenericPhysicalPlanScope(plan); err != nil {
		return PhysicalPlan{}, fmt.Errorf("validate generic physical execution scope: %w", err)
	}
	if len(plan.Operations) == 0 || plan.Operations[0].Kind != PhysicalRootScanOp || plan.Operations[0].RootScan == nil {
		return PhysicalPlan{}, fmt.Errorf("generic physical execution plan requires a root scan")
	}

	// The generic scope verifier defines the root scope as every operation up
	// to the first traversal or terminal return. BuildGenericPhysicalPlan has
	// already proven that the whole prefix scopes the root correctly.
	insertAt := physicalScopeWindowEnd(plan.Operations, 1)
	// Shared expression LETs are deliberately emitted immediately before
	// RETURN for root-only plans. Put the root execution window before them so
	// AQL can discard rows before evaluating the family maps, while traversal
	// plans still insert the window before their first child SET.
	for insertAt > 1 && insertAt <= len(plan.Operations) && plan.Operations[insertAt-1].Kind == PhysicalExpressionLetOp {
		insertAt--
	}
	if insertAt <= 1 || insertAt >= len(plan.Operations) {
		return PhysicalPlan{}, fmt.Errorf("generic physical execution plan requires a scoped root followed by RETURN or traversal")
	}

	out := clonePhysicalPlan(plan)
	root := out.Operations[0].RootScan.Variable
	window := []PhysicalOperation{{
		Kind:   PhysicalSortOp,
		Source: PhysicalSource{SemanticNode: out.Source.SemanticNode, ResourceType: out.Source.ResourceType, SemanticField: "_key"},
		Sort:   &PhysicalSort{Value: PhysicalValue{Variable: root, Path: []string{"_key"}}},
	}}
	if limit > 0 {
		if _, exists := out.BindVars[genericPhysicalExecutionLimitBind]; exists {
			return PhysicalPlan{}, fmt.Errorf("generic physical execution limit bind %q is already defined", genericPhysicalExecutionLimitBind)
		}
		out.BindVars[genericPhysicalExecutionLimitBind] = limit
		window = append(window, PhysicalOperation{
			Kind:   PhysicalLimitOp,
			Source: PhysicalSource{SemanticNode: out.Source.SemanticNode, ResourceType: out.Source.ResourceType},
			Limit:  &PhysicalLimit{BindKey: genericPhysicalExecutionLimitBind},
		})
	}
	operations := make([]PhysicalOperation, 0, len(out.Operations)+len(window))
	operations = append(operations, out.Operations[:insertAt]...)
	operations = append(operations, window...)
	operations = append(operations, out.Operations[insertAt:]...)
	out.Operations = operations
	if err := ValidateGenericPhysicalPlanScope(out); err != nil {
		return PhysicalPlan{}, fmt.Errorf("validate generic physical execution window: %w", err)
	}
	return out, nil
}
