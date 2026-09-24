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
	executionQuery   CompiledQuery
}

// ExecutionQuery returns metadata for the selected-root rows query. The
// execution package uses it instead of separately compiling the full output.
func (p CompiledOutputPage) ExecutionQuery() CompiledQuery {
	return p.executionQuery
}

// CompileRecipeOutputPageShardsWithPolicy returns one selected-root page
// template per bounded flat projection shard. RootKeysQuery and its binds are
// intentionally identical across the returned pages; execution must discover
// one root-key page and apply it to every shard.
func CompileRecipeOutputPageShardsWithPolicy(output lower.CompiledRecipeOutput, bindings recipe.RuntimeBindings, pageSize int, policy ir.PhysicalOptimizationPolicy) ([]CompiledOutputPage, error) {
	if pageSize < 1 {
		return nil, fmt.Errorf("root page size must be positive")
	}
	physical, err := optimizedOutputPlan(output, policy)
	if err != nil {
		return nil, err
	}
	keysPlan, err := rootKeysPagePlan(physical, pageSize)
	if err != nil {
		return nil, fmt.Errorf("build root-key page: %w", err)
	}
	rowsPlan, err := selectedRootsPlan(physical)
	if err != nil {
		return nil, fmt.Errorf("build selected-root page: %w", err)
	}
	rowsPlan, err = withGenericPhysicalExecutionWindow(rowsPlan, 0)
	if err != nil {
		return nil, fmt.Errorf("apply selected-root execution window: %w", err)
	}
	if bindings.IncludeAuthResourcePath {
		if err := appendAuthResourcePathProjection(&rowsPlan); err != nil {
			return nil, err
		}
	}
	rowsPlans, names, err := projectionShardPhysicalPlans(rowsPlan, output, DefaultProjectionShardPolicy())
	if err != nil {
		return nil, err
	}
	keys, err := aql.RenderPhysicalPlan(keysPlan)
	if err != nil {
		return nil, fmt.Errorf("render root-key page: %w", err)
	}
	pages := make([]CompiledOutputPage, 0, len(rowsPlans))
	for index, shardPlan := range rowsPlans {
		rows, err := aql.RenderPhysicalPlan(shardPlan)
		if err != nil {
			return nil, fmt.Errorf("render selected-root projection shard %d: %w", index+1, err)
		}
		columns, pivotFields := physicalProjectionMetadata(shardPlan)
		if len(output.Columns) != 0 {
			columns = append([]string(nil), output.Columns...)
		}
		outputSchema := lower.CloneCompiledOutputSchema(output.OutputSchema)
		publicColumns := publicOutputColumns(outputSchema)
		if len(names) > index {
			publicColumns = append([]string(nil), names[index]...)
		}
		query := CompiledQuery{
			Project: bindings.Project, DatasetGeneration: normalizeDatasetGeneration(bindings.DatasetGeneration), RootResourceType: output.RootResourceType,
			TranslationVersion: output.TranslationVersion, AuthResourcePaths: cloneStrings(bindings.AuthResourcePaths), PlanMode: "physical", PlanProfile: "generic_fhir_graph_recipe",
			TraversalCount: physicalTraversalCount(shardPlan), RowIdentity: output.RowIdentity.Clone(), OptimizationRules: recipeOptimizationRules(shardPlan), Query: rows.Query,
			BindVars: rows.BindVars, Columns: columns, OutputSchema: outputSchema, PublicColumns: publicColumns, PivotFields: pivotFields,
			PlanDiagnostics: physicalPlanDiagnostics(shardPlan),
		}
		pages = append(pages, CompiledOutputPage{
			RootKeysQuery: keys.Query, RootKeysBindVars: keys.BindVars,
			RowsQuery: rows.Query, RowsBindVars: rows.BindVars, RowsDiagnostics: query.PlanDiagnostics, executionQuery: query,
		})
	}
	return pages, nil
}

// CompileRecipeOutputPageWithPolicy builds typed key-discovery and selected-
// root templates. Page binds are compiler-owned and callers may only replace
// their values between executions.
func CompileRecipeOutputPageWithPolicy(output lower.CompiledRecipeOutput, bindings recipe.RuntimeBindings, pageSize int, policy ir.PhysicalOptimizationPolicy) (CompiledOutputPage, error) {
	pages, err := CompileRecipeOutputPageShardsWithPolicy(output, bindings, pageSize, policy)
	if err != nil {
		return CompiledOutputPage{}, err
	}
	if len(pages) != 1 {
		return CompiledOutputPage{}, fmt.Errorf("output %q requires %d projection shards; use CompileRecipeOutputPageShardsWithPolicy", output.Name, len(pages))
	}
	return pages[0], nil
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
		ir.PhysicalOperation{Kind: ir.PhysicalSortOp, Source: source, Sort: &ir.PhysicalSort{Keys: []ir.PhysicalValue{left}}},
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
		if operation.Kind == ir.PhysicalExpressionLetOp {
			// A population LET is immediately followed by its root eligibility
			// filter. Projection-only LETs are intentionally left out of the
			// key-discovery prefix so paging does not evaluate them early.
			if index+1 >= len(operations) || operations[index+1].Kind != ir.PhysicalFilterOp {
				break
			}
		} else if operation.Kind != ir.PhysicalFilterOp {
			break
		}
		index++
	}
	return index
}
