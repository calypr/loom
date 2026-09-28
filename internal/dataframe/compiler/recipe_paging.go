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
	if bindings.PreviewLimit > 0 {
		physical = withConstructionPreviewRootIDFilter(output, physical)
	}
	keysPlan, err := rootKeysPagePlan(physical, pageSize)
	if err != nil {
		return CompiledOutputPage{}, fmt.Errorf("build root-key page: %w", err)
	}
	rowsPlan, err := selectedRootsPlan(physical)
	if err != nil {
		return CompiledOutputPage{}, fmt.Errorf("build selected-root page: %w", err)
	}
	if bindings.IncludeSourceIdentity {
		rowsPlan, _, err = withPreviewSourceResourceID(output, rowsPlan)
		if err != nil {
			return CompiledOutputPage{}, fmt.Errorf("add preview source identity to selected-root page: %w", err)
		}
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
	rootPageFilters := rootPageConstructionFilters(out)
	if len(rootPageFilters) != 0 {
		insertAt := rootPageInsertionIndex(out.Operations)
		operations := make([]ir.PhysicalOperation, 0, len(out.Operations)+len(rootPageFilters))
		operations = append(operations, out.Operations[:insertAt]...)
		operations = append(operations, rootPageFilters...)
		operations = append(operations, out.Operations[insertAt:]...)
		out.Operations = operations
	}
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
	// Root-key discovery deliberately stops before the typed construction
	// stages. Its RETURN contains only _key, so carrying the final row schema
	// through validation would incorrectly require every source-stage column.
	// The selected-roots rows plan retains this sequence and executes it fully.
	out.StageSequence = nil
	if err := ir.ValidateGenericPhysicalPlanScope(out); err != nil {
		return ir.PhysicalPlan{}, err
	}
	return out, nil
}

// rootPageConstructionFilters moves only a leading run of FILTER stages over
// source-projection columns into root-key discovery. Those filters preserve
// row identity and cardinality, so applying them before the root key window
// avoids paging through roots that the construction immediately discards.
// Filters after a transforming stage, or filters whose source projection is
// not available at root scope, remain in the selected-root query.
func rootPageConstructionFilters(plan ir.PhysicalPlan) []ir.PhysicalOperation {
	sequence := plan.StageSequence
	if sequence == nil || len(sequence.Stages) == 0 || len(plan.Operations) == 0 {
		return nil
	}
	var sourceProjections map[string]ir.PhysicalProjection
	for index := len(plan.Operations) - 1; index >= 0; index-- {
		operation := plan.Operations[index]
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		sourceProjections = make(map[string]ir.PhysicalProjection, len(operation.Return.Projections))
		for _, projection := range operation.Return.Projections {
			sourceProjections[projection.Name] = projection
		}
		break
	}
	if len(sourceProjections) == 0 {
		return nil
	}
	root := plan.Operations[0].RootScan.Variable
	operations := make([]ir.PhysicalOperation, 0, len(sequence.Stages)*2)
	priorStageID := sequence.SourceStageID
	for _, stage := range sequence.Stages {
		if stage.Kind != ir.PhysicalStageFilterOp || stage.Filter == nil || len(stage.DerivedLets) != 0 || stage.InputStageID != priorStageID {
			break
		}
		filter, projection, ok := rootPageConstructionFilter(stage, sourceProjections, root)
		if !ok {
			break
		}
		if projection.Expression != nil {
			operations = append(operations, ir.PhysicalOperation{
				Kind: ir.PhysicalExpressionLetOp,
				ExpressionLet: &ir.PhysicalExpressionLet{
					Variable: stage.InputRowVariable, Expression: ir.ClonePhysicalExpression(*projection.Expression),
				},
			})
			// The reduced root query binds the projected value itself, not the
			// stage's row object, so the stage column path must be dropped.
			filter.Predicate.Left = ir.PhysicalValue{Variable: stage.InputRowVariable}
		} else {
			filter.Predicate.Left = projection.Value
		}
		operations = append(operations, ir.PhysicalOperation{
			Kind:   ir.PhysicalFilterOp,
			Source: ir.PhysicalSource{SemanticNode: plan.Source.SemanticNode, ResourceType: plan.Source.ResourceType, SemanticField: projection.Name},
			Filter: &filter,
		})
		sourceProjections = rootPagePassThroughProjections(stage, sourceProjections)
		priorStageID = stage.ID
	}
	return operations
}

func rootPageConstructionFilter(stage ir.PhysicalConstructionStage, projections map[string]ir.PhysicalProjection, root string) (ir.PhysicalFilter, ir.PhysicalProjection, bool) {
	if stage.Filter.Expression != nil {
		return ir.PhysicalFilter{}, ir.PhysicalProjection{}, false
	}
	left := stage.Filter.Predicate.Left
	if left.Variable != stage.InputRowVariable || left.BindKey != "" || len(left.Path) != 1 {
		return ir.PhysicalFilter{}, ir.PhysicalProjection{}, false
	}
	projection, ok := projections[left.Path[0]]
	if !ok || !rootPageProjectionAvailable(projection, root) {
		return ir.PhysicalFilter{}, ir.PhysicalProjection{}, false
	}
	filter := ir.ClonePhysicalOperation(ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: stage.Filter}).Filter
	if filter == nil {
		return ir.PhysicalFilter{}, ir.PhysicalProjection{}, false
	}
	return *filter, projection, true
}

func rootPageProjectionAvailable(projection ir.PhysicalProjection, root string) bool {
	if projection.Expression != nil {
		expression := projection.Expression
		if expression.Kind == ir.PhysicalExtractExpression && expression.Extract != nil {
			return expression.Extract.Prepared == nil && expression.Extract.Source.Variable == root
		}
		if expression.Kind == ir.PhysicalValueExpression && expression.Value != nil {
			return expression.Value.Variable == root
		}
		return false
	}
	return projection.Value.Variable == root
}

func rootPagePassThroughProjections(stage ir.PhysicalConstructionStage, projections map[string]ir.PhysicalProjection) map[string]ir.PhysicalProjection {
	outputs := make(map[string]ir.PhysicalProjection, len(stage.OutputProjections))
	for _, output := range stage.OutputProjections {
		value := output.Value
		if output.Expression != nil || value.Variable != stage.InputRowVariable || value.BindKey != "" || len(value.Path) != 1 {
			continue
		}
		projection, ok := projections[value.Path[0]]
		if !ok {
			continue
		}
		projection.Name = output.Name
		outputs[output.Name] = projection
	}
	return outputs
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
