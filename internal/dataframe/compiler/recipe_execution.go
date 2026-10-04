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

func workspaceOutputCaptureRequired(output lower.CompiledRecipeOutput) bool {
	if len(output.WorkspaceOutputSources) != 0 {
		return true
	}
	if output.Plan.ClickHouseCombine == nil {
		return false
	}
	for _, input := range output.Plan.ClickHouseCombine.Inputs {
		if input.WorkspaceOutputID != "" {
			return true
		}
	}
	return false
}

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
	if workspaceOutputCaptureRequired(output) {
		return CompiledQuery{}, fmt.Errorf("output %q references same-workspace outputs; server-owned workspace capture is not available", output.Name)
	}
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
	includePreviewSourceID := false
	if bindings.IncludeSourceIdentity {
		var err error
		physical, includePreviewSourceID, err = withPreviewSourceResourceID(output, physical)
		if err != nil {
			return CompiledQuery{}, fmt.Errorf("add preview source identity projection: %w", err)
		}
	}
	if limit > 0 {
		physical = withConstructionPreviewRootIDFilter(output, physical)
	}
	physical, err := withGenericPhysicalExecutionWindow(physical, limit)
	if err != nil {
		return CompiledQuery{}, fmt.Errorf("apply canonical recipe execution window: %w", err)
	}
	physical = withoutUnusedTerminalPivotPresenceCompanions(physical)
	if bindings.IncludeAuthResourcePath && !groupRows {
		if err := appendAuthResourcePathProjection(&physical); err != nil {
			return CompiledQuery{}, err
		}
	}
	previewCoveringIndex := previewCoveringIndexSpec(physical)
	previewGroupScan := previewGroupScanSpec(physical, previewCoveringIndex)
	var rendered aql.RenderedPhysicalPlan
	if previewCoveringIndex != nil {
		if len(previewCoveringIndex.pivotGroupKeyPaths) != 0 {
			rendered, err = aql.RenderPhysicalPlanWithTwoScanPivotPreview(physical, previewCoveringIndex.Name, previewCoveringIndex.pivotGroupKeyPaths)
		} else {
			rendered, err = aql.RenderPhysicalPlanWithRootIndexHint(physical, previewCoveringIndex.Name)
		}
	} else if canRenderDynamicCategoryPivotPreview(physical) {
		rendered, err = aql.RenderPhysicalPlanWithDynamicCategoryPivotPreview(physical)
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
	if includePreviewSourceID {
		outputSchema = append(outputSchema, lower.CompiledOutputColumn{
			ID: ir.PreviewSourceResourceIDColumn, Name: ir.PreviewSourceResourceIDColumn,
			Label: ir.PreviewSourceResourceIDColumn, SemanticPath: "preview:source_resource_id",
			Kind: "string", Cardinality: "optional_one", Nullable: true, Internal: true,
		})
	}
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
		PartialValidation: rendered.PartialValidation || (physical.StageSequence != nil && physical.StageSequence.PreviewLimitBindKey != "" &&
			(physical.StageSequence.PreviewSourceWindowByRootID || physical.StageSequence.PreviewTerminalPivotWindow)),
		PreviewCoveringIndex: previewCoveringIndex,
		PreviewGroupScan:     previewGroupScan,
		PlanDiagnostics:      physicalPlanDiagnostics(physical),
	}, nil
}

// withoutUnusedTerminalPivotPresenceCompanions keeps category-presence
// sidecars out of ordinary terminal Pivot previews. Lowering retains them in
// the canonical plan so a later category-discovery scan can distinguish an
// absent source property from explicit NULL. A frozen Pivot only consumes
// that proof when one of its categories is MISSING; otherwise carrying the
// hidden boolean through the preview would disable the bounded two-scan path.
func withoutUnusedTerminalPivotPresenceCompanions(plan ir.PhysicalPlan) ir.PhysicalPlan {
	sequence := plan.StageSequence
	if sequence == nil || len(sequence.Stages) != 1 || sequence.PreviewLimitBindKey == "" ||
		sequence.PreviewSourceWindowByRootID || !sequence.PreviewTerminalPivotWindow ||
		sequence.CellTraceReturn != nil || sequence.RowLineageReturn != nil || sequence.PopulationMappingReturn != nil {
		return plan
	}
	stage := &sequence.Stages[0]
	if stage.Kind != ir.PhysicalStagePivotOp || stage.GroupedPivot == nil ||
		stage.InputStageID != sequence.SourceStageID || sequence.FinalStageID != stage.ID ||
		sequence.FinalRowIdentity != stage.RowIdentityColumn ||
		stage.GroupedPivot.CategoryPresenceColumn != "" || stage.GroupedPivot.CategoryPresence != nil ||
		stage.GroupedPivot.CategoryPresenceFromInput {
		return plan
	}

	presenceNames := make(map[string]struct{})
	for _, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.PresenceOutput {
				presenceNames[projection.Name] = struct{}{}
			}
		}
	}
	if len(presenceNames) == 0 {
		return plan
	}

	for operationIndex := range plan.Operations {
		operation := &plan.Operations[operationIndex]
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		projections := operation.Return.Projections[:0]
		for _, projection := range operation.Return.Projections {
			if _, unused := presenceNames[projection.Name]; unused && projection.PresenceOutput {
				continue
			}
			projections = append(projections, projection)
		}
		operation.Return.Projections = projections
	}

	filterColumns := func(columns []ir.PhysicalStageColumn) []ir.PhysicalStageColumn {
		filtered := columns[:0]
		for _, column := range columns {
			if _, unused := presenceNames[column.Name]; unused {
				continue
			}
			filtered = append(filtered, column)
		}
		return filtered
	}
	sequence.SourceColumns = filterColumns(sequence.SourceColumns)
	stage.InputColumns = filterColumns(stage.InputColumns)
	stage.InputProjections = filterPresenceProjections(stage.InputProjections, presenceNames)
	stage.GroupedPivot.InputProjections = filterPresenceProjections(stage.GroupedPivot.InputProjections, presenceNames)
	return plan
}

func filterPresenceProjections(projections []ir.PhysicalProjection, presenceNames map[string]struct{}) []ir.PhysicalProjection {
	filtered := projections[:0]
	for _, projection := range projections {
		if _, unused := presenceNames[projection.Name]; unused {
			continue
		}
		filtered = append(filtered, projection)
	}
	return filtered
}

func withPreviewSourceResourceID(output lower.CompiledRecipeOutput, plan ir.PhysicalPlan) (ir.PhysicalPlan, bool, error) {
	if plan.Engine == ir.PhysicalEngineClickHouse || outputHasCompositeSource(output) {
		return plan, false, nil
	}
	rootVariable := ""
	for _, operation := range plan.Operations {
		if operation.RootScan != nil {
			rootVariable = operation.RootScan.Variable
			break
		}
	}
	if rootVariable == "" {
		return plan, false, nil
	}
	projection := ir.PhysicalProjection{
		Name: ir.PreviewSourceResourceIDColumn, Hidden: true,
		Value: ir.PhysicalValue{Variable: rootVariable, Path: []string{"id"}},
	}
	returnFound := false
	for index := range plan.Operations {
		operation := &plan.Operations[index]
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		returnFound = true
		operation.Return.Projections = appendUniqueProjection(operation.Return.Projections, projection)
	}
	if !returnFound {
		return plan, false, nil
	}
	if sequence := plan.StageSequence; sequence != nil {
		sourceColumn := ir.PhysicalStageColumn{
			ID: ir.PreviewSourceResourceIDColumn, Name: ir.PreviewSourceResourceIDColumn,
			Label: ir.PreviewSourceResourceIDColumn, Kind: "string", Cardinality: "optional_one",
			Nullable: true, Internal: true,
		}
		sequence.SourceColumns = appendUniqueStageColumn(sequence.SourceColumns, sourceColumn)
		sequence.FinalColumns = appendUniqueStageColumn(sequence.FinalColumns, sourceColumn)
		for index := range sequence.Stages {
			stage := &sequence.Stages[index]
			inputProjection := ir.PhysicalProjection{
				Name: sourceColumn.Name, Hidden: true,
				Value: ir.PhysicalValue{Variable: stage.InputRowVariable, Path: []string{sourceColumn.Name}},
			}
			stage.InputColumns = appendUniqueStageColumn(stage.InputColumns, sourceColumn)
			stage.OutputColumns = appendUniqueStageColumn(stage.OutputColumns, sourceColumn)
			stage.InputProjections = appendUniqueProjection(stage.InputProjections, inputProjection)
			stage.OutputProjections = appendUniqueProjection(stage.OutputProjections, inputProjection)
			if stage.Unpivot != nil {
				stage.Unpivot.InputProjections = appendUniqueProjection(stage.Unpivot.InputProjections, inputProjection)
				if len(stage.Unpivot.PreservedOutputs) != 0 {
					stage.Unpivot.PreservedOutputs = appendUniqueUnpivotOutput(stage.Unpivot.PreservedOutputs, ir.PhysicalUnpivotOutput{
						InputColumn: sourceColumn.Name, OutputColumn: sourceColumn.Name,
					})
				}
			}
		}
	}
	for index := range plan.Operations {
		operation := &plan.Operations[index]
		if operation.Unpivot == nil {
			continue
		}
		inputProjection := projection
		inputProjection.Value = ir.PhysicalValue{Variable: rootVariable, Path: []string{"id"}}
		operation.Unpivot.InputProjections = appendUniqueProjection(operation.Unpivot.InputProjections, inputProjection)
		if len(operation.Unpivot.PreservedOutputs) != 0 {
			operation.Unpivot.PreservedOutputs = appendUniqueUnpivotOutput(operation.Unpivot.PreservedOutputs, ir.PhysicalUnpivotOutput{
				InputColumn: inputProjection.Name, OutputColumn: inputProjection.Name,
			})
		}
	}
	return plan, true, nil
}

func appendUniqueUnpivotOutput(outputs []ir.PhysicalUnpivotOutput, candidate ir.PhysicalUnpivotOutput) []ir.PhysicalUnpivotOutput {
	for _, output := range outputs {
		if output.InputColumn == candidate.InputColumn {
			return outputs
		}
	}
	return append(outputs, candidate)
}

func appendUniqueProjection(projections []ir.PhysicalProjection, candidate ir.PhysicalProjection) []ir.PhysicalProjection {
	for _, projection := range projections {
		if projection.Name == candidate.Name {
			return projections
		}
	}
	return append(projections, candidate)
}

func appendUniqueStageColumn(columns []ir.PhysicalStageColumn, candidate ir.PhysicalStageColumn) []ir.PhysicalStageColumn {
	for _, column := range columns {
		if column.Name == candidate.Name {
			return columns
		}
	}
	return append(columns, candidate)
}

func outputHasCompositeSource(output lower.CompiledRecipeOutput) bool {
	for _, operation := range output.Plan.Operations {
		if operation.Kind == ir.PhysicalGroupRowsOp || operation.Kind == ir.PhysicalGroupedPivotOp {
			return true
		}
	}
	if sequence := output.Plan.StageSequence; sequence != nil {
		for _, stage := range sequence.Stages {
			if stage.Kind == ir.PhysicalStageCohortGroupOp {
				return true
			}
		}
	}
	for _, stage := range output.Stages {
		if stage.Operation == string(recipe.ConstructionGroupOp) || stage.Operation == string(recipe.ConstructionPivotOp) ||
			stage.Operation == string(recipe.ConstructionCodedGroupOp) {
			return true
		}
	}
	return false
}

func canRenderDynamicCategoryPivotPreview(physical ir.PhysicalPlan) bool {
	sequence := physical.StageSequence
	if sequence == nil || !sequence.PreviewTerminalPivotWindow || sequence.PreviewLimitBindKey == "" ||
		sequence.PreviewSourceWindowByRootID || sequence.CellTraceReturn != nil || len(sequence.Stages) != 1 {
		return false
	}
	stage := sequence.Stages[0]
	return stage.ID == sequence.FinalStageID && stage.InputStageID == sequence.SourceStageID &&
		stage.Kind == ir.PhysicalStagePivotOp && stage.GroupedPivot != nil &&
		!stage.GroupedPivot.OneInputRowPerGroup && len(stage.GroupedPivot.GroupKeys) > 0
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
