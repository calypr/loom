package compiler

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

// CompileResolvedRecipePlanWithPolicy is the common execution boundary for a
// resolved recipe bundle. Lowering produces canonical physical plans; this
// function applies the generic optimizer, inserts the typed execution window,
// and renders through RenderPhysicalPlan for each output.
func CompileResolvedRecipePlanWithPolicy(resolved semantic.ResolvedRecipePlan, limit int, policy PhysicalOptimizationPolicy) ([]CompiledQuery, error) {
	compiled, err := CompileResolvedRecipePlan(resolved, policy)
	if err != nil {
		return nil, err
	}
	queries := make([]CompiledQuery, 0, len(compiled.Outputs))
	for _, output := range compiled.Outputs {
		query, err := CompileRecipeOutputWithPolicy(output, resolved.SemanticPlan.Bindings, limit, policy)
		if err != nil {
			return nil, fmt.Errorf("output %q: %w", output.Name, err)
		}
		queries = append(queries, query)
	}
	return queries, nil
}

// CompileRecipeOutputWithPolicy applies the common optimizer, execution
// window, and canonical renderer to one already-lowered recipe output.
func CompileRecipeOutputWithPolicy(output CompiledRecipeOutput, bindings recipe.RuntimeBindings, limit int, policy PhysicalOptimizationPolicy) (CompiledQuery, error) {
	var physical PhysicalPlan
	if output.OptimizedPlan != nil {
		physical = clonePhysicalPlan(*output.OptimizedPlan)
	} else {
		var err error
		physical, err = OptimizePhysicalPlanWithPolicy(output.Plan, policy)
		if err != nil {
			return CompiledQuery{}, fmt.Errorf("optimize canonical recipe plan: %w", err)
		}
	}
	physical, err := withGenericPhysicalExecutionWindow(physical, limit)
	if err != nil {
		return CompiledQuery{}, fmt.Errorf("apply canonical recipe execution window: %w", err)
	}
	rendered, err := RenderPhysicalPlan(physical)
	if err != nil {
		return CompiledQuery{}, fmt.Errorf("render canonical recipe physical plan: %w", err)
	}
	columns, pivotFields := physicalProjectionMetadata(physical)
	if len(output.Columns) != 0 {
		columns = append([]string(nil), output.Columns...)
	}
	outputSchema := append([]CompiledOutputColumn(nil), output.OutputSchema...)
	publicColumns := publicOutputColumns(outputSchema)
	if len(publicColumns) == 0 {
		for _, column := range columns {
			if column == "_key" || column == "__loom_row_id" || column == "__loom_dynamic_runtime_keys" {
				continue
			}
			publicColumns = append(publicColumns, column)
		}
	}
	return CompiledQuery{
		Project:           bindings.Project,
		DatasetGeneration: normalizeDatasetGeneration(bindings.DatasetGeneration),
		RootResourceType:  output.RootResourceType,
		AuthResourcePaths: cloneStrings(bindings.AuthResourcePaths),
		PlanMode:          "physical",
		PlanProfile:       "generic_fhir_graph_recipe",
		TraversalCount:    physicalTraversalCount(physical),
		RowIdentity:       cloneRowIdentity(output.RowIdentity),
		OptimizationRules: recipeOptimizationRules(physical),
		Query:             rendered.Query,
		BindVars:          rendered.BindVars,
		Columns:           columns,
		OutputSchema:      outputSchema,
		PublicColumns:     publicColumns,
		PivotFields:       pivotFields,
		Limit:             limit,
		PlanDiagnostics:   physicalPlanDiagnostics(physical),
	}, nil
}

func recipeOptimizationRules(plan PhysicalPlan) []string {
	for _, operation := range plan.Operations {
		if operation.Kind == PhysicalFilterOp {
			return []string{OptimizerRuleFilterPushdown}
		}
	}
	return nil
}
