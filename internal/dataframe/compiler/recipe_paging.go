package compiler

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/optimize"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

const (
	RootPageAfterKeyBind = "loom_root_page_after_key"
	RootPageSizeBind     = genericPhysicalExecutionLimitBind
	RootPageKeysBind     = "loom_root_page_keys"
)

// CompiledOutputPage contains immutable query templates for bounded root
// execution. RootKeys selects the next root page even when those roots emit no
// output rows. Rows executes the complete output for exactly that page.
type CompiledOutputPage struct {
	RootKeysQuery    string
	RootKeysBindVars map[string]any
	RowsQuery        string
	RowsBindVars     map[string]any
	RowsDiagnostics  ir.CompilerPlanDiagnostics
}

// CompileRecipeOutputPageWithPolicy builds typed key-discovery and selected-
// root templates. Page binds are compiler-owned and callers may only replace
// their values between executions.
func CompileRecipeOutputPageWithPolicy(output lower.CompiledRecipeOutput, bindings recipe.RuntimeBindings, pageSize int, policy ir.PhysicalOptimizationPolicy) (CompiledOutputPage, error) {
	if pageSize < 1 {
		return CompiledOutputPage{}, fmt.Errorf("root page size must be positive")
	}
	physical, err := optimizedOutputPlan(output, policy)
	if err != nil {
		return CompiledOutputPage{}, err
	}
	keysPlan, err := rootKeysPagePlan(physical, pageSize)
	if err != nil {
		return CompiledOutputPage{}, fmt.Errorf("build root-key page: %w", err)
	}
	rowsPlan, err := selectedRootsPlan(physical)
	if err != nil {
		return CompiledOutputPage{}, fmt.Errorf("build selected-root page: %w", err)
	}
	rowsPlan, err = withGenericPhysicalExecutionWindow(rowsPlan, 0)
	if err != nil {
		return CompiledOutputPage{}, fmt.Errorf("apply selected-root execution window: %w", err)
	}
	if bindings.IncludeAuthResourcePath {
		if err := appendAuthResourcePathProjection(&rowsPlan); err != nil {
			return CompiledOutputPage{}, err
		}
	}
	keys, err := aql.RenderPhysicalPlan(keysPlan)
	if err != nil {
		return CompiledOutputPage{}, fmt.Errorf("render root-key page: %w", err)
	}
	rows, err := aql.RenderPhysicalPlan(rowsPlan)
	if err != nil {
		return CompiledOutputPage{}, fmt.Errorf("render selected-root page: %w", err)
	}
	return CompiledOutputPage{
		RootKeysQuery: keys.Query, RootKeysBindVars: keys.BindVars,
		RowsQuery: rows.Query, RowsBindVars: rows.BindVars, RowsDiagnostics: physicalPlanDiagnostics(rowsPlan),
	}, nil
}

func optimizedOutputPlan(output lower.CompiledRecipeOutput, policy ir.PhysicalOptimizationPolicy) (ir.PhysicalPlan, error) {
	if output.OptimizedPlan != nil {
		return clonePhysicalPlan(*output.OptimizedPlan), nil
	}
	physical, err := optimize.OptimizePhysicalPlanWithPolicy(output.Plan, policy)
	if err != nil {
		return ir.PhysicalPlan{}, fmt.Errorf("optimize canonical recipe plan: %w", err)
	}
	return physical, nil
}

func rootKeysPagePlan(plan ir.PhysicalPlan, pageSize int) (ir.PhysicalPlan, error) {
	if err := ir.ValidateGenericPhysicalPlanScope(plan); err != nil {
		return ir.PhysicalPlan{}, err
	}
	if len(plan.Operations) < 6 || plan.Operations[0].RootScan == nil {
		return ir.PhysicalPlan{}, fmt.Errorf("generic physical plan requires a root scan and scope")
	}
	for _, key := range []string{RootPageAfterKeyBind, RootPageSizeBind} {
		if _, exists := plan.BindVars[key]; exists {
			return ir.PhysicalPlan{}, fmt.Errorf("root page bind %q is already defined", key)
		}
	}
	out := clonePhysicalPlan(plan)
	root := out.Operations[0].RootScan.Variable
	insertAt := rootPageInsertionIndex(out.Operations)
	out.BindVars[RootPageAfterKeyBind] = ""
	out.BindVars[RootPageSizeBind] = pageSize
	source := ir.PhysicalSource{SemanticNode: out.Source.SemanticNode, ResourceType: out.Source.ResourceType, SemanticField: "_key"}
	left := ir.PhysicalValue{Variable: root, Path: []string{"_key"}}
	right := ir.PhysicalValue{BindKey: RootPageAfterKeyBind}
	out.Operations = append(append([]ir.PhysicalOperation(nil), out.Operations[:insertAt]...),
		ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Source: source, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "GT", Left: left, Right: &right}}},
		ir.PhysicalOperation{Kind: ir.PhysicalSortOp, Source: source, Sort: &ir.PhysicalSort{Value: left}},
		ir.PhysicalOperation{Kind: ir.PhysicalLimitOp, Source: source, Limit: &ir.PhysicalLimit{BindKey: RootPageSizeBind}},
		ir.PhysicalOperation{Kind: ir.PhysicalReturnOp, Source: source, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{{Name: "_key", Value: left}}}},
	)
	if err := ir.ValidateGenericPhysicalPlanScope(out); err != nil {
		return ir.PhysicalPlan{}, err
	}
	return out, nil
}

func selectedRootsPlan(plan ir.PhysicalPlan) (ir.PhysicalPlan, error) {
	if _, exists := plan.BindVars[RootPageKeysBind]; exists {
		return ir.PhysicalPlan{}, fmt.Errorf("root page bind %q is already defined", RootPageKeysBind)
	}
	out := clonePhysicalPlan(plan)
	root := out.Operations[0].RootScan.Variable
	insertAt := rootPageInsertionIndex(out.Operations)
	out.BindVars[RootPageKeysBind] = []string{}
	left := ir.PhysicalValue{Variable: root, Path: []string{"_key"}}
	right := ir.PhysicalValue{BindKey: RootPageKeysBind}
	filter := ir.PhysicalOperation{
		Kind:   ir.PhysicalFilterOp,
		Source: ir.PhysicalSource{SemanticNode: out.Source.SemanticNode, ResourceType: out.Source.ResourceType, SemanticField: "_key"},
		Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "IN", Left: left, Right: &right}},
	}
	operations := make([]ir.PhysicalOperation, 0, len(out.Operations)+1)
	operations = append(operations, out.Operations[:insertAt]...)
	operations = append(operations, filter)
	operations = append(operations, out.Operations[insertAt:]...)
	out.Operations = operations
	if err := ir.ValidateGenericPhysicalPlanScope(out); err != nil {
		return ir.PhysicalPlan{}, err
	}
	return out, nil
}

// rootPageInsertionIndex returns the boundary after scoped root predicates and
// before the first cardinality-changing or navigation operation.
func rootPageInsertionIndex(operations []ir.PhysicalOperation) int {
	index := 5 // ROOT_SCAN plus the canonical four-operation root scope block.
	for index < len(operations) {
		operation := operations[index]
		if operation.Kind != ir.PhysicalFilterOp {
			break
		}
		index++
	}
	return index
}
