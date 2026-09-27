package compiler

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/optimize"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

// CompileResolvedRecipePlanWithPolicy is the common execution boundary for a
// resolved recipe bundle. Lowering produces canonical physical plans; this
// function applies the generic optimizer, inserts the typed execution window,
// and renders through RenderPhysicalPlan for each output.
func CompileResolvedRecipePlanWithPolicy(resolved semantic.ResolvedRecipePlan, limit int, policy ir.PhysicalOptimizationPolicy) ([]CompiledQuery, error) {
	compiled, err := lower.CompileResolvedRecipePlan(resolved, policy)
	if err != nil {
		return nil, err
	}
	queries := make([]CompiledQuery, 0, len(compiled.Outputs))
	for _, output := range compiled.Outputs {
		output.TranslationVersion = resolved.SemanticPlan.TranslationVersion
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
func CompileRecipeOutputWithPolicy(output lower.CompiledRecipeOutput, bindings recipe.RuntimeBindings, limit int, policy ir.PhysicalOptimizationPolicy) (CompiledQuery, error) {
	var physical ir.PhysicalPlan
	groupRows := len(output.Plan.Operations) == 1 && output.Plan.Operations[0].Kind == ir.PhysicalGroupRowsOp
	if groupRows {
		physical = clonePhysicalPlan(output.Plan)
	} else if output.OptimizedPlan != nil {
		physical = clonePhysicalPlan(*output.OptimizedPlan)
	} else {
		var err error
		physical, err = optimize.OptimizePhysicalPlanWithPolicy(output.Plan, policy)
		if err != nil {
			return CompiledQuery{}, fmt.Errorf("optimize canonical recipe plan: %w", err)
		}
	}
	physical, err := withGenericPhysicalExecutionWindow(physical, limit)
	if err != nil {
		return CompiledQuery{}, fmt.Errorf("apply canonical recipe execution window: %w", err)
	}
	if bindings.IncludeAuthResourcePath && !groupRows {
		if err := appendAuthResourcePathProjection(&physical); err != nil {
			return CompiledQuery{}, err
		}
	}
	previewCoveringIndex := previewCoveringIndexSpec(physical)
	var rendered aql.RenderedPhysicalPlan
	if previewCoveringIndex != nil {
		rendered, err = aql.RenderPhysicalPlanWithRootIndexHint(physical, previewCoveringIndex.Name)
	} else {
		rendered, err = aql.RenderPhysicalPlan(physical)
	}
	if err != nil {
		return CompiledQuery{}, fmt.Errorf("render canonical recipe physical plan: %w", err)
	}
	columns, pivotFields := physicalProjectionMetadata(physical)
	if len(output.Columns) != 0 {
		columns = append([]string(nil), output.Columns...)
	}
	outputSchema := lower.CloneCompiledOutputSchema(output.OutputSchema)
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
		Project:            bindings.Project,
		DatasetGeneration:  normalizeDatasetGeneration(bindings.DatasetGeneration),
		RootResourceType:   output.RootResourceType,
		TranslationVersion: output.TranslationVersion,
		AuthResourcePaths:  cloneStrings(bindings.AuthResourcePaths),
		PlanMode:           "physical",
		PlanProfile:        "generic_fhir_graph_recipe",
		TraversalCount:     physicalTraversalCount(physical),
		RowIdentity:        output.RowIdentity.Clone(),
		OptimizationRules:  recipeOptimizationRules(physical),
		Query:              rendered.Query,
		BindVars:           rendered.BindVars,
		Columns:            columns,
		OutputSchema:       outputSchema,
		PublicColumns:      publicColumns,
		PivotFields:        pivotFields,
		Limit:              limit,
		PartialValidation: physical.StageSequence != nil && physical.StageSequence.PreviewLimitBindKey != "" &&
			(physical.StageSequence.PreviewSourceWindowByRootID || physical.StageSequence.PreviewTerminalPivotWindow),
		PreviewCoveringIndex: previewCoveringIndex,
		PlanDiagnostics:      physicalPlanDiagnostics(physical),
	}, nil
}

func appendAuthResourcePathProjection(physical *ir.PhysicalPlan) error {
	rootVariable := ""
	for _, operation := range physical.Operations {
		if operation.RootScan != nil {
			rootVariable = operation.RootScan.Variable
			break
		}
	}
	if rootVariable == "" {
		return fmt.Errorf("canonical plan has no root scan for auth_resource_path projection")
	}
	for index := range physical.Operations {
		operation := &physical.Operations[index]
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.Name == "auth_resource_path" {
				return nil
			}
		}
		value := ir.PhysicalValue{Variable: rootVariable, Path: []string{"auth_resource_path"}}
		for previous := 0; previous < index; previous++ {
			reshape := &physical.Operations[previous]
			switch reshape.Kind {
			case ir.PhysicalGroupedPivotOp:
				if reshape.GroupedPivot == nil {
					return fmt.Errorf("grouped pivot is missing payload for auth_resource_path projection")
				}
				pivot := reshape.GroupedPivot
				reserved := []string{pivot.CategoryColumn, pivot.ValueColumn, pivot.CategoryPresenceColumn, pivot.UnlistedEvidenceColumn, "__loom_row_id"}
				for _, key := range pivot.GroupKeys {
					reserved = append(reserved, key.Column)
				}
				for _, category := range pivot.Categories {
					reserved = append(reserved, category.Output)
				}
				column := uniqueAuthResourcePathColumn(pivot.InputProjections, reserved...)
				pivot.InputProjections = append(pivot.InputProjections, ir.PhysicalProjection{
					Name: column, Hidden: true,
					Value: ir.PhysicalValue{Variable: rootVariable, Path: []string{"auth_resource_path"}},
				})
				variable := pivot.GroupRowsVariable + "_auth_resource_path"
				for _, key := range pivot.GroupKeys {
					if key.Variable == variable {
						variable += "_group"
					}
				}
				pivot.GroupKeys = append(pivot.GroupKeys, ir.PhysicalGroupedPivotKey{
					Column: column, Variable: variable, Kind: "STRING", Hidden: true,
				})
				value = ir.PhysicalValue{Variable: pivot.OutputRowVariable, Path: []string{column}}
			case ir.PhysicalUnpivotOp:
				if reshape.Unpivot == nil {
					return fmt.Errorf("unpivot is missing payload for auth_resource_path projection")
				}
				unpivot := reshape.Unpivot
				column := uniqueAuthResourcePathColumn(unpivot.InputProjections, unpivot.KeyOutput, unpivot.ValueOutput, "__loom_row_id")
				unpivot.InputProjections = append(unpivot.InputProjections, ir.PhysicalProjection{
					Name: column, Hidden: true,
					Value: ir.PhysicalValue{Variable: rootVariable, Path: []string{"auth_resource_path"}},
				})
				value = ir.PhysicalValue{Variable: unpivot.OutputRowVariable, Path: []string{column}}
			}
		}
		operation.Return.Projections = append(operation.Return.Projections, ir.PhysicalProjection{
			Name: "auth_resource_path", Hidden: true,
			Value: value,
		})
		return nil
	}
	return fmt.Errorf("canonical plan has no RETURN operation for auth_resource_path projection")
}

func uniqueAuthResourcePathColumn(projections []ir.PhysicalProjection, reserved ...string) string {
	for suffix := 0; ; suffix++ {
		name := "__loom_auth_resource_path"
		if suffix > 0 {
			name = fmt.Sprintf("%s_%d", name, suffix)
		}
		found := false
		for _, projection := range projections {
			if projection.Name == name {
				found = true
				break
			}
		}
		for _, reservedName := range reserved {
			if reservedName == name {
				found = true
				break
			}
		}
		if !found {
			return name
		}
	}
}

func recipeOptimizationRules(plan ir.PhysicalPlan) []string {
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalFilterOp {
			return []string{OptimizerRuleFilterPushdown}
		}
	}
	return nil
}
