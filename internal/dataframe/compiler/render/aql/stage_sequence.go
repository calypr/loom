package aql

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

const constructionSourceVariable = "__loom_construction_source_projection"

func renderPhysicalStageSequence(plan ir.PhysicalPlan, options physicalRenderOptions) (RenderedPhysicalPlan, error) {
	sequence := plan.StageSequence
	if sequence == nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("physical construction stage sequence is required")
	}
	if rendered, eligible, err := renderRelatedEligibilityCountRows(plan, sequence, options); err != nil {
		return RenderedPhysicalPlan{}, err
	} else if eligible {
		return rendered, nil
	}
	groupPreviewShape, hasRelatedGroupPreview := relatedGroupPreviewShapeFor(plan, sequence, options)
	stages := pruneUnusedRelatedOutputsForCountRows(sequence)
	inlineSourceForKeylessCount := terminalKeylessCountRowsOnly(sequence)
	sourcePlan := ir.ClonePhysicalPlan(plan)
	sourcePlan.StageSequence = nil
	sourcePlan.PreviewSourceWindowByRootID = sequence.PreviewSourceWindowByRootID && sequence.PreviewLimitBindKey != ""
	inlineCodedGroupRoot := codedGroupSourceRootVariable(sourcePlan, sequence, stages, options)
	inlineCodedPivotRoot := codedPivotSourceRootVariable(sourcePlan, sequence, stages, options)
	inlineGroupSourceRow := groupSourceRowVariable(sourcePlan, sequence, stages, options)
	for _, stage := range stages {
		if stage.GroupedPivot != nil && stage.GroupedPivot.CodedCorrelation != nil && inlineCodedPivotRoot == "" {
			return RenderedPhysicalPlan{}, fmt.Errorf("coded Pivot requires the source root to be streamed into its first stage")
		}
	}
	inlineSourceIntoFirstStage := inlineSourceForKeylessCount || inlineCodedGroupRoot != "" || inlineCodedPivotRoot != "" || inlineGroupSourceRow != ""
	if inlineGroupSourceRow != "" || !inlineSourceIntoFirstStage {
		pruneUnusedSourceGroupProjections(&sourcePlan, sequence)
	}
	collectionKeys, err := collectionBindKeys(plan)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	reservedVars := stageSequenceVariableNames(plan)
	for _, name := range []string{"cohort_member", "member", "resource", "selected", "source", "members", "definitions", "all_memberships", "unassigned", "unassigned_records", "unassigned_members", "unassigned_row", "revision", "revision_guard", "selection", "selection_guard", "policy_guard", "rows", "row_value"} {
		reservedVars[name] = struct{}{}
	}
	groupPreviewFrontier := relatedGroupPreviewFrontier{}
	if hasRelatedGroupPreview {
		groupPreviewFrontier, err = renderRelatedGroupPreviewFrontier(plan, sequence, groupPreviewShape, collectionKeys)
		if err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render related Group preview frontier: %w", err)
		}
		options.previewGroupIdentityFilter = &previewGroupIdentityFilter{
			StageIndex:              groupPreviewShape.groupFilterIndex,
			ColumnBindKey:           groupPreviewFrontier.groupIdentityColumnBindKey,
			SelectedGroupIdentities: groupPreviewFrontier.groupIdentitiesVariable,
			BoundedVariable:         groupPreviewFrontier.boundedVariable,
		}
	}
	selectedGroupKeysVariable := ""
	groupKeySourceVariable := ""
	groupIdentityVariable := ""
	groupKeyVariables := []string(nil)
	var groupKeyQuery RenderedPhysicalPlan
	if options.twoScanPivotPreview {
		pivot, ok := terminalPreviewPivot(sequence)
		if !ok {
			return RenderedPhysicalPlan{}, fmt.Errorf("two-scan preview requires one simple terminal Pivot")
		}
		allocator := physicalPlanRenderer{
			bindVars:       runtimePhysicalBindVars(plan.BindVars, collectionKeys),
			collectionKeys: collectionKeys,
			reservedVars:   reservedVars,
			internalPrefix: "construction_",
		}
		selectedGroupKeysVariable = allocator.newInternalVariable("reshape_preview_selected_group_keys")
		groupKeySourceVariable = allocator.newInternalVariable("reshape_preview_key_row")
		groupIdentityVariable = allocator.newInternalVariable("reshape_preview_identity")
		groupKeyVariables = make([]string, len(pivot.GroupKeys))
		for index := range groupKeyVariables {
			groupKeyVariables[index] = allocator.newInternalVariable(fmt.Sprintf("reshape_preview_group_key_%d", index+1))
		}
		keyPlan, keyErr := terminalPivotGroupKeySourcePlan(sourcePlan, pivot)
		if keyErr != nil {
			return RenderedPhysicalPlan{}, keyErr
		}
		groupKeyQuery, err = renderPhysicalPlanWithOptions(keyPlan, physicalRenderOptions{
			rootIndexHint:  options.rootIndexHint,
			internalPrefix: "preview_key_",
		})
		if err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render terminal Pivot group-key scan: %w", err)
		}
		options.pivotGroupTupleFilter = &pivotGroupTupleFilter{
			SelectedGroupKeysVariable: selectedGroupKeysVariable,
			SourcePaths:               options.pivotGroupKeySourcePaths,
		}
	}
	sourceOptions := physicalRenderOptions{
		rootIndexHint:                   options.rootIndexHint,
		disableRootIndex:                options.disableRootIndex,
		pivotGroupTupleFilter:           options.pivotGroupTupleFilter,
		preserveProjectionPresenceNames: options.preserveProjectionPresenceNames,
		projectionPresenceMarkerColumn:  options.projectionPresenceMarkerColumn,
	}
	if inlineGroupSourceRow != "" {
		sourceOptions.terminalReturnVariable = inlineGroupSourceRow
	} else if inlineSourceIntoFirstStage {
		sourceOptions.omitTerminalReturn = true
	}
	source, err := renderPhysicalPlanWithOptions(sourcePlan, sourceOptions)
	if err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("render source projection: %w", err)
	}
	var groupPreviewWindow RenderedPhysicalPlan
	if hasRelatedGroupPreview {
		groupPreviewWindow, err = renderRelatedGroupPreviewSourceWindow(sourcePlan, sourceOptions, groupPreviewFrontier.rootKeysVariable)
		if err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render related Group preview source window: %w", err)
		}
		source.Query = renderRelatedGroupPreviewConditionalSource(
			groupPreviewFrontier.boundedVariable, groupPreviewWindow.Query, source.Query,
		)
	}
	bindVars := make(map[string]any, len(source.BindVars)+len(groupPreviewWindow.BindVars)+len(groupPreviewFrontier.bindVars)+len(groupKeyQuery.BindVars)+len(plan.BindVars))
	for _, values := range []map[string]any{
		groupKeyQuery.BindVars,
		source.BindVars,
		groupPreviewWindow.BindVars,
		groupPreviewFrontier.bindVars,
		runtimePhysicalBindVars(plan.BindVars, collectionKeys),
	} {
		if err := mergeRenderedBindVars(bindVars, values); err != nil {
			return RenderedPhysicalPlan{}, err
		}
	}
	presenceMarkerRows := make(map[string]struct{}, len(stages)+1)
	if options.projectionPresenceMarkerColumn != "" {
		for _, stage := range stages {
			if stage.InputRowVariable != "" {
				presenceMarkerRows[stage.InputRowVariable] = struct{}{}
			}
		}
		presenceMarkerRows["__loom_construction_final_row"] = struct{}{}
	}
	renderer := physicalPlanRenderer{
		bindVars:                        bindVars,
		collectionKeys:                  collectionKeys,
		setVariables:                    map[string]string{},
		reservedVars:                    reservedVars,
		internalPrefix:                  "construction_",
		dynamicPivotPreview:             options.dynamicPivotPreview,
		preserveProjectionPresenceNames: options.preserveProjectionPresenceNames,
		projectionPresenceMarkerColumn:  options.projectionPresenceMarkerColumn,
		projectionPresenceMarkerRows:    presenceMarkerRows,
		previewGroupIdentityFilter:      options.previewGroupIdentityFilter,
	}
	relatedExpandPreviewStageIndex, hasRelatedExpandPreviewStage := terminalRelatedExpandPreviewStage(sequence)
	if options.omitTerminalRowSort || options.terminalProjectionColumn != "" || options.projectionPresenceMarkerColumn != "" ||
		options.twoScanPivotPreview || options.dynamicPivotPreview {
		hasRelatedExpandPreviewStage = false
	}
	relatedExpandPreviewLimitBindKey := ""
	if hasRelatedExpandPreviewStage {
		if options.relatedExpandPreviewLimit > 0 {
			relatedExpandPreviewLimitBindKey = renderer.newInternalBindKey("related_expand_preview_limit")
			renderer.bindVars[relatedExpandPreviewLimitBindKey] = options.relatedExpandPreviewLimit
		} else if previewLimit, ok := renderer.bindVars[sequence.PreviewLimitBindKey].(int); sequence.PreviewLimitBindKey != "" && ok && previewLimit > 0 {
			relatedExpandPreviewLimitBindKey = sequence.PreviewLimitBindKey
		}
	}
	if sequence.RowLineageReturn != nil {
		return renderer.renderConstructionRowLineage(source.Query, stages, *sequence.RowLineageReturn)
	}

	lines := make([]string, 0, 16)
	if hasRelatedGroupPreview {
		lines = append(lines, strings.Split(strings.TrimSuffix(groupPreviewFrontier.query, "\n"), "\n")...)
	}
	if options.twoScanPivotPreview {
		pivot, _ := terminalPreviewPivot(sequence)
		groupLines, groupErr := renderTerminalPivotGroupIdentitySelection(
			groupKeyQuery.Query, *pivot, selectedGroupKeysVariable,
			groupKeySourceVariable, groupKeyVariables, groupIdentityVariable, renderer.bindVars,
			collectionKeys, sequence.PreviewLimitBindKey,
		)
		if groupErr != nil {
			return RenderedPhysicalPlan{}, groupErr
		}
		lines = append(lines, groupLines...)
	}
	priorRows := constructionSourceVariable
	if !inlineSourceIntoFirstStage {
		lines = append(lines, fmt.Sprintf("LET %s = (", constructionSourceVariable))
		for _, line := range strings.Split(strings.TrimSuffix(source.Query, "\n"), "\n") {
			lines = append(lines, "  "+line)
		}
		lines = append(lines, ")")
	} else {
		priorRows = ""
	}
	for index, stage := range stages {
		stageRows := fmt.Sprintf("__loom_construction_stage_%d", index+1)
		lines = append(lines, fmt.Sprintf("LET %s = (", stageRows))
		inlineCodedPivotStage := index == 0 && inlineCodedPivotRoot != "" && stage.Kind == ir.PhysicalStagePivotOp
		if inlineSourceIntoFirstStage && index == 0 {
			for _, line := range strings.Split(strings.TrimSuffix(source.Query, "\n"), "\n") {
				lines = append(lines, "  "+line)
			}
		}
		if stage.Kind == ir.PhysicalStageGroupOp {
			rendered, renderErr := renderer.renderConstructionGroupStage(stage, constructionGroupInput{
				RowsVariable:     priorRows,
				SourceRowInScope: inlineGroupSourceRow != "" && index == 0,
			})
			if renderErr != nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("render stage %q group: %w", stage.ID, renderErr)
			}
			lines = append(lines, rendered...)
			lines = append(lines, ")")
			priorRows = stageRows
			continue
		}
		if stage.Kind == ir.PhysicalStageCodedGroupOp {
			rootVariable := ""
			if inlineCodedGroupRoot != "" && index == 0 {
				rootVariable = inlineCodedGroupRoot
			}
			rendered, renderErr := renderer.renderConstructionCodedGroupStage(stage, priorRows, rootVariable)
			if renderErr != nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("render stage %q coded group: %w", stage.ID, renderErr)
			}
			lines = append(lines, rendered...)
			lines = append(lines, ")")
			priorRows = stageRows
			continue
		}
		if stage.Kind == ir.PhysicalStageCohortGroupOp {
			rendered, renderErr := renderer.renderConstructionCohortGroupStage(stage, priorRows)
			if renderErr != nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("render stage %q cohort group: %w", stage.ID, renderErr)
			}
			lines = append(lines, rendered...)
			lines = append(lines, ")")
			priorRows = stageRows
			continue
		}
		if !inlineCodedPivotStage {
			lines = append(lines, fmt.Sprintf("  FOR %s IN %s", stage.InputRowVariable, priorRows))
		}
		if hasRelatedGroupPreview && options.previewGroupIdentityFilter != nil &&
			options.previewGroupIdentityFilter.StageIndex == index {
			filter := options.previewGroupIdentityFilter
			lines = append(lines, fmt.Sprintf(
				"  FILTER !%s OR %s[@%s] IN %s", filter.BoundedVariable, stage.InputRowVariable,
				filter.ColumnBindKey, filter.SelectedGroupIdentities,
			))
		}
		switch stage.Kind {
		case ir.PhysicalStagePivotOp:
			if stage.GroupedPivot == nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("stage %q is missing grouped pivot payload", stage.ID)
			}
			// The final construction query sorts on row identity after all stages.
			// A one-input-per-group pivot needs no intermediate group-key sort.
			previewLimitBindKey := ""
			if sequence.PreviewTerminalPivotWindow && index == len(stages)-1 && !options.twoScanPivotPreview {
				previewLimitBindKey = sequence.PreviewLimitBindKey
			}
			if inlineCodedPivotStage && stage.GroupedPivot.CodedSourceVariable != inlineCodedPivotRoot {
				return RenderedPhysicalPlan{}, fmt.Errorf("coded Pivot source variable differs from the inlined root scan")
			}
			rendered, renderErr := renderer.renderGroupedTablePivot(*stage.GroupedPivot, !stage.GroupedPivot.OneInputRowPerGroup, previewLimitBindKey)
			if renderErr != nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("render stage %q pivot: %w", stage.ID, renderErr)
			}
			lines = appendIndented(lines, rendered)
			lines = append(lines, "  RETURN "+stage.OutputRowVariable)
		case ir.PhysicalStageUnpivotOp:
			if stage.Unpivot == nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("stage %q is missing unpivot payload", stage.ID)
			}
			rendered, renderErr := renderer.renderTableUnpivot(*stage.Unpivot)
			if renderErr != nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("render stage %q unpivot: %w", stage.ID, renderErr)
			}
			lines = appendIndented(lines, rendered)
			lines = append(lines, "  RETURN "+stage.OutputRowVariable)
		case ir.PhysicalStageExpandOp:
			rendered, renderErr := renderer.renderConstructionExpandStage(stage)
			if renderErr != nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("render stage %q expand: %w", stage.ID, renderErr)
			}
			lines = appendIndented(lines, rendered)
		case ir.PhysicalStageRelatedExpandOp:
			previewLimitBindKey := ""
			if index == relatedExpandPreviewStageIndex {
				previewLimitBindKey = relatedExpandPreviewLimitBindKey
			}
			rendered, renderErr := renderer.renderConstructionRelatedExpandStage(stage, previewLimitBindKey)
			if renderErr != nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("render stage %q related expansion: %w", stage.ID, renderErr)
			}
			lines = appendIndented(lines, rendered)
		case ir.PhysicalStageDeriveOp, ir.PhysicalStageFilterOp, ir.PhysicalStageRelatedEligibilityOp, ir.PhysicalStageRelatedSourceOp, ir.PhysicalStageRelatedFieldOp:
			for operationIndex, operation := range stage.DerivedLets {
				if operation.Kind != ir.PhysicalExpressionLetOp {
					return RenderedPhysicalPlan{}, fmt.Errorf("stage %q derived operation %d has kind %q", stage.ID, operationIndex, operation.Kind)
				}
				rendered, renderErr := renderer.renderExpressionLet(operation, "  ")
				if renderErr != nil {
					return RenderedPhysicalPlan{}, fmt.Errorf("render stage %q derived operation %d: %w", stage.ID, operationIndex, renderErr)
				}
				lines = append(lines, rendered...)
			}
			if stage.Filter != nil {
				rendered, renderErr := renderer.renderScopeOperation(ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: stage.Filter}, "  ")
				if renderErr != nil {
					return RenderedPhysicalPlan{}, fmt.Errorf("render stage %q filter: %w", stage.ID, renderErr)
				}
				lines = append(lines, rendered...)
			}
			object, renderErr := renderer.renderReturn(ir.PhysicalReturn{Projections: stage.OutputProjections})
			if renderErr != nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("render stage %q output row: %w", stage.ID, renderErr)
			}
			lines = append(lines, fmt.Sprintf("  LET %s = %s", stage.OutputRowVariable, object))
			lines = append(lines, "  RETURN "+stage.OutputRowVariable)
		default:
			return RenderedPhysicalPlan{}, fmt.Errorf("stage %q has unsupported operation %q", stage.ID, stage.Kind)
		}
		lines = append(lines, ")")
		priorRows = stageRows
	}
	finalRow := "__loom_construction_final_row"
	if hasRelatedGroupPreview && options.previewGroupIdentityFilter != nil && options.previewGroupIdentityFilter.StageIndex < 0 {
		groupRows := renderer.newInternalVariable("related_group_preview_filtered_rows")
		lines = append(lines, fmt.Sprintf("LET %s = (", groupRows))
		lines = append(lines, fmt.Sprintf("  FOR %s IN %s", finalRow, priorRows))
		filter := options.previewGroupIdentityFilter
		lines = append(lines, fmt.Sprintf(
			"  FILTER !%s OR %s[@%s] IN %s", filter.BoundedVariable, finalRow,
			filter.ColumnBindKey, filter.SelectedGroupIdentities,
		))
		lines = append(lines, "  RETURN "+finalRow, ")")
		priorRows = groupRows
	}
	lines = append(lines, fmt.Sprintf("FOR %s IN %s", finalRow, priorRows))
	if !options.omitTerminalRowSort && (len(stages) == 0 || stages[len(stages)-1].Kind != ir.PhysicalStageGroupOp && stages[len(stages)-1].Kind != ir.PhysicalStageCohortGroupOp) {
		lines = append(lines, fmt.Sprintf("SORT %s.%s ASC", finalRow, sequence.FinalRowIdentity))
	}
	if sequence.PreviewLimitBindKey != "" {
		lines = append(lines, "LIMIT @"+sequence.PreviewLimitBindKey)
	}
	projectionCapacity := len(sequence.FinalColumns)
	if options.terminalProjectionColumn != "" {
		projectionCapacity = 1
	}
	projections := make([]ir.PhysicalProjection, 0, projectionCapacity)
	for _, column := range sequence.FinalColumns {
		if options.terminalProjectionColumn != "" && column.Name != options.terminalProjectionColumn {
			continue
		}
		if column.Name == "auth_resource_path" && sequence.OutputAuthResourcePathBindKey != "" {
			return RenderedPhysicalPlan{}, fmt.Errorf("final stage already declares the reserved authorization path column")
		}
		projection := ir.PhysicalProjection{
			Name: column.Name, Hidden: column.Internal,
			Value: ir.PhysicalValue{Variable: finalRow, Path: []string{column.Name}},
		}
		if options.terminalProjectionColumn != "" {
			projection.Presence = &ir.PhysicalProjectionPresence{
				Source: ir.PhysicalValue{Variable: finalRow}, Paths: [][]string{{column.Name}},
			}
		}
		projections = append(projections, projection)
	}
	if sequence.OutputAuthResourcePathBindKey != "" && options.terminalProjectionColumn == "" {
		projections = append(projections, ir.PhysicalProjection{
			Name: "auth_resource_path", Hidden: true,
			Value: ir.PhysicalValue{BindKey: sequence.OutputAuthResourcePathBindKey},
		})
	}
	returned, err := renderer.renderReturn(ir.PhysicalReturn{Projections: projections})
	if err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("render final stage output: %w", err)
	}
	if sequence.CellTraceReturn != nil {
		terminal := *sequence.CellTraceReturn
		terminal.Value = ir.PhysicalExpression{
			Kind:  ir.PhysicalValueExpression,
			Value: &ir.PhysicalValue{Variable: finalRow, Path: []string{terminal.Construction.OutputColumn}},
		}
		identity := ir.PhysicalExpression{
			Kind:  ir.PhysicalValueExpression,
			Value: &ir.PhysicalValue{Variable: finalRow, Path: []string{terminal.Construction.RowIdentityColumn}},
		}
		if terminal.Construction.RowIdentityColumn == "__loom_row_id" {
			terminal.ExplicitIdentity = &identity
			terminal.IdentityParts = nil
		} else {
			terminal.ExplicitIdentity = nil
			terminal.IdentityParts = []ir.PhysicalPopulationMappingIdentityPart{{
				Name: terminal.Construction.RowIdentityColumn, Expression: identity,
			}}
		}
		traceLines, traceErr := renderer.renderCellTraceReturn(terminal)
		if traceErr != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render construction cell trace: %w", traceErr)
		}
		lines = append(lines, traceLines...)
	} else {
		lines = append(lines, "RETURN "+returned)
	}
	query := strings.Join(lines, "\n") + "\n"
	return RenderedPhysicalPlan{
		Query: query, BindVars: pruneUnusedRuntimeBindVars(renderer.bindVars, query),
		PartialValidation: hasRelatedGroupPreview,
	}, nil
}

// groupSourceRowVariable returns the first Group input row variable when a
// terminal keyed COUNT_ROWS Group can consume its direct root source in scope.
// The generic source RETURN is rendered as a LET before the Group COLLECT, so
// source rows are never collected into an intermediate array.
func groupSourceRowVariable(sourcePlan ir.PhysicalPlan, sequence *ir.PhysicalStageSequence, stages []ir.PhysicalConstructionStage, options physicalRenderOptions) string {
	if sequence == nil || len(stages) != 1 || sequence.RowLineageReturn != nil || sequence.CellTraceReturn != nil ||
		sequence.PreviewSourceWindowByRootID || sequence.PreviewTerminalPivotWindow || sequence.OutputAuthResourcePathBindKey != "" ||
		options.omitTerminalReturn || options.terminalReturnVariable != "" || options.terminalProjectionColumn != "" ||
		options.omitTerminalRowSort || options.projectionPresenceMarkerColumn != "" ||
		len(options.preserveProjectionPresenceNames) != 0 || options.twoScanPivotPreview || options.dynamicPivotPreview {
		return ""
	}
	stage := stages[0]
	group := stage.Group
	if stage.ID != sequence.FinalStageID || stage.InputStageID != sequence.SourceStageID ||
		stage.Kind != ir.PhysicalStageGroupOp || group == nil || len(group.Keys) == 0 ||
		group.RootContributorInputColumn != "" ||
		!constructionGroupCountsOnlyRows(group) ||
		(len(group.RowValues) > 0 && !constructionGroupRowValuesCanAggregate(group.RowValues)) {
		return ""
	}
	if len(sourcePlan.Operations) < 6 || sourcePlan.PreviewSourceWindowByRootID ||
		sourcePlan.Operations[0].Kind != ir.PhysicalRootScanOp || sourcePlan.Operations[0].RootScan == nil ||
		sourcePlan.Operations[0].RootScan.Population != nil {
		return ""
	}
	last := len(sourcePlan.Operations) - 1
	if sourcePlan.Operations[last].Kind != ir.PhysicalReturnOp || sourcePlan.Operations[last].Return == nil {
		return ""
	}
	for index, operation := range sourcePlan.Operations[:last] {
		switch {
		case index == 0:
			if operation.Kind != ir.PhysicalRootScanOp || operation.RootScan == nil {
				return ""
			}
		case index < 5:
			if operation.Kind != ir.PhysicalFilterOp && operation.Kind != ir.PhysicalDerivedLetOp {
				return ""
			}
		default:
			if operation.Kind != ir.PhysicalFilterOp && operation.Kind != ir.PhysicalExpressionLetOp {
				return ""
			}
		}
	}
	if stage.InputRowVariable == "" {
		return ""
	}
	if _, collision := physicalPlanVariableNames(sourcePlan)[stage.InputRowVariable]; collision {
		return ""
	}
	return stage.InputRowVariable
}

// codedGroupSourceRootVariable returns the physical root variable only when
// the first construction stage can consume the source query's rows directly.
// The source query still renders every root filter, auth predicate, population
// selection, and execution window; only its terminal projection is omitted.
func codedGroupSourceRootVariable(sourcePlan ir.PhysicalPlan, sequence *ir.PhysicalStageSequence, stages []ir.PhysicalConstructionStage, options physicalRenderOptions) string {
	if sequence == nil || len(stages) == 0 || sequence.RowLineageReturn != nil || sequence.CellTraceReturn != nil ||
		options.terminalProjectionColumn != "" || options.projectionPresenceMarkerColumn != "" ||
		len(options.preserveProjectionPresenceNames) != 0 || options.twoScanPivotPreview ||
		stages[0].Kind != ir.PhysicalStageCodedGroupOp || stages[0].CodedGroup == nil ||
		stages[0].InputStageID != sequence.SourceStageID || !stages[0].CodedGroup.SourceRowsUnique ||
		stages[0].CodedGroup.SourceIdentityColumn != "_key" || len(sourcePlan.Operations) < 2 ||
		sourcePlan.Operations[0].Kind != ir.PhysicalRootScanOp || sourcePlan.Operations[0].RootScan == nil {
		return ""
	}
	root := sourcePlan.Operations[0].RootScan
	if root.Variable == "" || root.CollectionBindKey != stages[0].CodedGroup.RootCollectionBindKey {
		return ""
	}
	returns := 0
	for index, operation := range sourcePlan.Operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 || operation.RootScan == nil {
				return ""
			}
		case ir.PhysicalFilterOp, ir.PhysicalDerivedLetOp, ir.PhysicalExpressionLetOp,
			ir.PhysicalSetOp, ir.PhysicalSortOp, ir.PhysicalLimitOp:
			// These source operations preserve one row per selected root.
		case ir.PhysicalReturnOp:
			returns++
			if operation.Return == nil || index != len(sourcePlan.Operations)-1 {
				return ""
			}
		default:
			return ""
		}
	}
	if returns != 1 {
		return ""
	}
	return root.Variable
}

// codedPivotSourceRootVariable returns the exact direct root scan variable only
// when CODED_PIVOT is the first source stage. The renderer embeds that source
// query into the Pivot subquery and evaluates each root's coded cells before
// returning it, so a full payload array is never materialized between stages.
func codedPivotSourceRootVariable(sourcePlan ir.PhysicalPlan, sequence *ir.PhysicalStageSequence, stages []ir.PhysicalConstructionStage, options physicalRenderOptions) string {
	if sequence == nil || len(stages) == 0 || sequence.RowLineageReturn != nil || sequence.CellTraceReturn != nil ||
		options.terminalProjectionColumn != "" || options.projectionPresenceMarkerColumn != "" ||
		len(options.preserveProjectionPresenceNames) != 0 || options.twoScanPivotPreview ||
		stages[0].Kind != ir.PhysicalStagePivotOp || stages[0].GroupedPivot == nil ||
		stages[0].GroupedPivot.CodedCorrelation == nil || stages[0].InputStageID != sequence.SourceStageID ||
		stages[0].GroupedPivot.CodedSourceVariable == "" || !stages[0].GroupedPivot.OneInputRowPerGroup ||
		sequence.SourceRowIdentity != "_key" || len(sourcePlan.Operations) < 2 ||
		sourcePlan.Operations[0].Kind != ir.PhysicalRootScanOp || sourcePlan.Operations[0].RootScan == nil {
		return ""
	}
	root := sourcePlan.Operations[0].RootScan
	if root.Variable == "" || root.Variable != stages[0].GroupedPivot.CodedSourceVariable ||
		root.CollectionBindKey == "" || sourcePlan.BindVars[root.CollectionBindKey] != stages[0].GroupedPivot.CodedCorrelation.ResourceType {
		return ""
	}
	returns := 0
	for index, operation := range sourcePlan.Operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 || operation.RootScan == nil {
				return ""
			}
		case ir.PhysicalFilterOp, ir.PhysicalDerivedLetOp, ir.PhysicalExpressionLetOp,
			ir.PhysicalSetOp, ir.PhysicalSortOp, ir.PhysicalLimitOp:
			// Source filters, authorization, and row-preserving LETs remain in
			// scope while each direct root is pivoted.
		case ir.PhysicalReturnOp:
			returns++
			if operation.Return == nil || index != len(sourcePlan.Operations)-1 {
				return ""
			}
		default:
			return ""
		}
	}
	if returns != 1 {
		return ""
	}
	return root.Variable
}

func pruneUnusedSourceGroupProjections(plan *ir.PhysicalPlan, sequence *ir.PhysicalStageSequence) {
	if sequence.CellTraceReturn != nil || len(sequence.Stages) == 0 {
		return
	}
	groupStage := sequence.Stages[0]
	if groupStage.Kind != ir.PhysicalStageGroupOp || groupStage.Group == nil || groupStage.InputStageID != sequence.SourceStageID {
		return
	}
	required := make(map[string]bool, len(groupStage.Group.Keys)+len(groupStage.Group.Aggregates)+len(groupStage.Group.RowValues))
	for _, key := range groupStage.Group.Keys {
		required[key.InputColumn] = true
	}
	for _, aggregate := range groupStage.Group.Aggregates {
		if aggregate.InputColumn != "" {
			required[aggregate.InputColumn] = true
		}
	}
	for _, rowValue := range groupStage.Group.RowValues {
		required[rowValue.InputColumn] = true
	}
	if groupStage.Group.RootContributorInputColumn != "" {
		required[groupStage.Group.RootContributorInputColumn] = true
	}
	for index := range plan.Operations {
		operation := &plan.Operations[index]
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		projections := operation.Return.Projections[:0]
		for _, projection := range operation.Return.Projections {
			if projection.Hidden || required[projection.Name] {
				projections = append(projections, projection)
			}
		}
		operation.Return.Projections = projections
	}
	pruneUnusedSourceGroupOperations(plan)
}

// pruneUnusedSourceGroupOperations removes root-row LETs and sets that cannot
// affect a source Group's retained projection, filters, or execution window.
// It only handles the row-preserving root-scan shape; plans with any other
// top-level operation keep their original source query.
func pruneUnusedSourceGroupOperations(plan *ir.PhysicalPlan) {
	if plan == nil || len(plan.Operations) < 2 ||
		plan.Operations[0].Kind != ir.PhysicalRootScanOp || plan.Operations[0].RootScan == nil ||
		plan.Operations[0].RootScan.Population != nil {
		return
	}
	rootVariable := plan.Operations[0].RootScan.Variable
	if rootVariable == "" {
		return
	}

	returnCount := 0
	for index, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 {
				return
			}
		case ir.PhysicalFilterOp:
			if operation.Filter == nil {
				return
			}
		case ir.PhysicalDerivedLetOp:
			if operation.DerivedLet == nil || operation.DerivedLet.Variable == "" {
				return
			}
		case ir.PhysicalExpressionLetOp:
			if operation.ExpressionLet == nil || operation.ExpressionLet.Variable == "" {
				return
			}
		case ir.PhysicalSetOp:
			if operation.Set == nil || operation.Set.Variable == "" {
				return
			}
		case ir.PhysicalSortOp:
			if operation.Sort == nil {
				return
			}
		case ir.PhysicalLimitOp:
			if operation.Limit == nil {
				return
			}
		case ir.PhysicalReturnOp:
			returnCount++
			if operation.Return == nil || index != len(plan.Operations)-1 {
				return
			}
			for _, projection := range operation.Return.Projections {
				if !directRootCategoryProjection(projection, rootVariable) {
					return
				}
			}
		default:
			return
		}
	}
	if returnCount != 1 {
		return
	}

	needed := map[string]struct{}{rootVariable: {}}
	keep := make([]bool, len(plan.Operations))
	keep[0] = true
	for index, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalFilterOp || operation.Kind == ir.PhysicalSortOp ||
			operation.Kind == ir.PhysicalLimitOp || operation.Kind == ir.PhysicalReturnOp {
			keep[index] = true
			addSourceGroupOperationReferences(needed, operation)
		}
	}

	for index := len(plan.Operations) - 1; index > 0; index-- {
		operation := plan.Operations[index]
		variables, producer := sourceGroupOperationVariables(operation)
		if !producer {
			continue
		}
		live := false
		for _, variable := range variables {
			if _, found := needed[variable]; found {
				live = true
				break
			}
		}
		if !live {
			continue
		}
		keep[index] = true
		addSourceGroupOperationReferences(needed, operation)
	}

	operations := plan.Operations[:0]
	for index, operation := range plan.Operations {
		if keep[index] {
			operations = append(operations, operation)
		}
	}
	plan.Operations = operations
}

func sourceGroupOperationVariables(operation ir.PhysicalOperation) ([]string, bool) {
	switch operation.Kind {
	case ir.PhysicalDerivedLetOp:
		return []string{operation.DerivedLet.Variable}, true
	case ir.PhysicalExpressionLetOp:
		return []string{operation.ExpressionLet.Variable}, true
	case ir.PhysicalSetOp:
		variables := []string{operation.Set.Variable}
		if operation.Set.Reduction != nil {
			variables = append(variables, operation.Set.Reduction.Variable)
		}
		if operation.Set.Prepared != nil {
			variables = append(variables, operation.Set.Prepared.Variable)
		}
		return variables, true
	default:
		return nil, false
	}
}

func addSourceGroupOperationReferences(needed map[string]struct{}, operation ir.PhysicalOperation) {
	physicalValueType := reflect.TypeOf(ir.PhysicalValue{})
	physicalObjectLookupType := reflect.TypeOf(ir.PhysicalObjectLookup{})
	physicalObjectKeysType := reflect.TypeOf(ir.PhysicalObjectKeys{})
	physicalPreparedReferenceType := reflect.TypeOf(ir.PhysicalPreparedReference{})
	physicalPreparedSetType := reflect.TypeOf(ir.PhysicalPreparedSet{})
	physicalSetType := reflect.TypeOf(ir.PhysicalSet{})
	physicalSubplanType := reflect.TypeOf(ir.PhysicalSubplan{})
	visited := map[uintptr]struct{}{}
	var visit func(reflect.Value)
	visit = func(value reflect.Value) {
		if !value.IsValid() {
			return
		}
		if value.Type() == physicalValueType {
			variable := value.FieldByName("Variable").String()
			if variable != "" {
				needed[variable] = struct{}{}
			}
			return
		}
		switch value.Type() {
		case physicalObjectLookupType:
			addSourceGroupVariableReference(needed, value.FieldByName("ObjectVariable").String())
		case physicalObjectKeysType:
			addSourceGroupVariableReference(needed, value.FieldByName("ObjectVariable").String())
		case physicalPreparedReferenceType:
			addSourceGroupVariableReference(needed, value.FieldByName("SetVariable").String())
		case physicalPreparedSetType:
			addSourceGroupVariableReference(needed, value.FieldByName("SourceSetVariable").String())
		case physicalSetType:
			addSourceGroupVariableReference(needed, value.FieldByName("SourceSetVariable").String())
		case physicalSubplanType:
			captures := value.FieldByName("Captures")
			for index := 0; index < captures.Len(); index++ {
				addSourceGroupVariableReference(needed, captures.Index(index).String())
			}
		}
		switch value.Kind() {
		case reflect.Interface, reflect.Pointer:
			if value.IsNil() {
				return
			}
			if value.Kind() == reflect.Pointer {
				pointer := value.Pointer()
				if _, found := visited[pointer]; found {
					return
				}
				visited[pointer] = struct{}{}
			}
			visit(value.Elem())
		case reflect.Struct:
			for index := 0; index < value.NumField(); index++ {
				field := value.Field(index)
				if field.CanInterface() {
					visit(field)
				}
			}
		case reflect.Slice, reflect.Array:
			for index := 0; index < value.Len(); index++ {
				visit(value.Index(index))
			}
		case reflect.Map:
			iterator := value.MapRange()
			for iterator.Next() {
				visit(iterator.Value())
			}
		}
	}
	visit(reflect.ValueOf(operation))
	if operation.Kind == ir.PhysicalSetOp && operation.Set != nil && operation.Set.SourceSetVariable != "" {
		needed[operation.Set.SourceSetVariable] = struct{}{}
	}
}

func addSourceGroupVariableReference(needed map[string]struct{}, variable string) {
	if variable != "" {
		needed[variable] = struct{}{}
	}
}

func directRootCategoryProjection(projection ir.PhysicalProjection, root string) bool {
	if projection.Presence != nil && (projection.Presence.Source.Variable != root || projection.Presence.Source.BindKey != "") {
		return false
	}
	if projection.Expression == nil {
		return projection.Value.Variable == root && projection.Value.BindKey == "" && len(projection.Value.Path) > 0
	}
	expression := projection.Expression
	if expression.Kind != ir.PhysicalExtractExpression || expression.Cardinality != ir.PhysicalScalarCardinality || expression.Extract == nil {
		return false
	}
	extract := expression.Extract
	if extract.ExecutionMode != ir.PhysicalSelectorDirectScalar || extract.Source.Variable != root || extract.Source.BindKey != "" ||
		len(extract.Source.Path) == 0 || len(extract.Fallbacks) != 0 || extract.Prepared != nil || extract.Distinct ||
		extract.UnitNormalization != nil || extract.Selector.Filter != nil || len(extract.Selector.Steps) == 0 {
		return false
	}
	for _, step := range extract.Selector.Steps {
		if step.Field == "" || step.Iterate || step.Index != nil {
			return false
		}
	}
	return true
}

func terminalKeylessCountRowsOnly(sequence *ir.PhysicalStageSequence) bool {
	if sequence == nil || sequence.CellTraceReturn != nil || sequence.PreviewSourceWindowByRootID || len(sequence.Stages) != 1 {
		return false
	}
	stage := sequence.Stages[0]
	return stage.ID == sequence.FinalStageID &&
		stage.InputStageID == sequence.SourceStageID &&
		stage.Kind == ir.PhysicalStageGroupOp &&
		stage.Group != nil &&
		len(stage.Group.Keys) == 0 &&
		constructionGroupCountsOnlyRows(stage.Group)
}

// pruneUnusedRelatedOutputsForCountRows keeps row-changing stages intact and
// drops only related values that later supported stages cannot read.
func pruneUnusedRelatedOutputsForCountRows(sequence *ir.PhysicalStageSequence) []ir.PhysicalConstructionStage {
	stages := append([]ir.PhysicalConstructionStage(nil), sequence.Stages...)
	if sequence.CellTraceReturn != nil || len(stages) < 2 {
		return stages
	}
	terminal := stages[len(stages)-1]
	if terminal.ID != sequence.FinalStageID || terminal.Kind != ir.PhysicalStageGroupOp || terminal.Group == nil ||
		len(terminal.Group.Keys) != 0 || !constructionGroupCountsOnlyRows(terminal.Group) {
		return stages
	}

	for index := 0; index < len(stages)-1; index++ {
		columnID := ""
		switch stages[index].Kind {
		case ir.PhysicalStageRelatedSourceOp:
			if stages[index].RelatedSource != nil {
				columnID = stages[index].RelatedSource.OutputColumnID
			}
		case ir.PhysicalStageRelatedFieldOp:
			if stages[index].RelatedField != nil {
				columnID = stages[index].RelatedField.OutputColumnID
			}
		}
		if columnID == "" {
			continue
		}

		sawRelatedExpand := false
		unused := true
		for downstream := index + 1; downstream < len(stages)-1; downstream++ {
			stage := stages[downstream]
			switch stage.Kind {
			case ir.PhysicalStageRelatedSourceOp:
				if stage.RelatedSource == nil || columnID == stage.RelatedSource.AnchorColumnID {
					unused = false
				}
			case ir.PhysicalStageRelatedFieldOp:
				activeRecordID := ""
				if stage.RelatedField != nil {
					activeRecordID = stageInputColumnID(stage, stage.RelatedField.ActiveRecordColumn)
				}
				if stage.RelatedField == nil || activeRecordID == "" || columnID == activeRecordID {
					unused = false
				}
			case ir.PhysicalStageRelatedExpandOp:
				if stage.RelatedExpand == nil {
					unused = false
				} else {
					sawRelatedExpand = true
					if columnID == stage.RelatedExpand.AnchorColumnID || columnID == stage.RelatedExpand.ParentIdentityColumnID {
						unused = false
					}
				}
			default:
				unused = false
			}
			if !unused {
				break
			}
		}
		if !unused || !sawRelatedExpand {
			continue
		}

		for downstream := index; downstream < len(stages)-1; downstream++ {
			stage := &stages[downstream]
			outputIDs := make(map[string]string, len(stage.OutputColumns))
			for _, column := range stage.OutputColumns {
				outputIDs[column.Name] = column.ID
			}
			projections := make([]ir.PhysicalProjection, 0, len(stage.OutputProjections))
			for _, projection := range stage.OutputProjections {
				if outputIDs[projection.Name] != columnID {
					projections = append(projections, projection)
				}
			}
			stage.OutputProjections = projections
		}
	}
	return stages
}

func stageInputColumnID(stage ir.PhysicalConstructionStage, name string) string {
	for _, column := range stage.InputColumns {
		if column.Name == name {
			return column.ID
		}
	}
	return ""
}

func constructionGroupCountsOnlyRows(group *ir.PhysicalStageGroup) bool {
	if len(group.Aggregates) == 0 {
		return false
	}
	for _, aggregate := range group.Aggregates {
		if aggregate.Operation != "COUNT_ROWS" {
			return false
		}
	}
	return true
}

func appendIndented(target, rendered []string) []string {
	for _, line := range rendered {
		target = append(target, "  "+line)
	}
	return target
}

func terminalPreviewPivot(sequence *ir.PhysicalStageSequence) (*ir.PhysicalGroupedPivot, bool) {
	if sequence == nil || !sequence.PreviewTerminalPivotWindow || sequence.PreviewLimitBindKey == "" ||
		sequence.PreviewSourceWindowByRootID || sequence.CellTraceReturn != nil || len(sequence.Stages) != 1 {
		return nil, false
	}
	stage := sequence.Stages[0]
	if stage.ID != sequence.FinalStageID || stage.InputStageID != sequence.SourceStageID ||
		stage.Kind != ir.PhysicalStagePivotOp || stage.GroupedPivot == nil ||
		stage.GroupedPivot.OneInputRowPerGroup || len(stage.GroupedPivot.GroupKeys) == 0 ||
		stage.GroupedPivot.CategoryPresence != nil || stage.GroupedPivot.CategoryPresenceColumn != "" {
		return nil, false
	}
	return stage.GroupedPivot, true
}

func terminalPivotGroupKeySourcePlan(sourcePlan ir.PhysicalPlan, pivot *ir.PhysicalGroupedPivot) (ir.PhysicalPlan, error) {
	if pivot == nil {
		return ir.PhysicalPlan{}, fmt.Errorf("terminal Pivot group-key scan is missing its Pivot")
	}
	sourcePlan = ir.ClonePhysicalPlan(sourcePlan)
	keyColumns := make(map[string]bool, len(pivot.GroupKeys))
	for _, key := range pivot.GroupKeys {
		keyColumns[key.Column] = false
	}
	found := false
	for index := range sourcePlan.Operations {
		operation := &sourcePlan.Operations[index]
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		found = true
		projections := make([]ir.PhysicalProjection, 0, len(keyColumns))
		for _, projection := range operation.Return.Projections {
			if _, required := keyColumns[projection.Name]; required {
				projections = append(projections, projection)
				keyColumns[projection.Name] = true
			}
		}
		missing := make([]string, 0, len(keyColumns))
		for _, key := range pivot.GroupKeys {
			if !keyColumns[key.Column] {
				missing = append(missing, key.Column)
			}
		}
		if len(missing) != 0 {
			return ir.PhysicalPlan{}, fmt.Errorf("terminal Pivot group-key scan is missing source projections %v", missing)
		}
		operation.Return.Projections = projections
		break
	}
	if !found {
		return ir.PhysicalPlan{}, fmt.Errorf("terminal Pivot group-key scan has no source RETURN")
	}
	return sourcePlan, nil
}

func renderTerminalPivotGroupIdentitySelection(sourceQuery string, pivot ir.PhysicalGroupedPivot, selectedGroupKeysVariable, sourceVariable string, groupKeyVariables []string, identityVariable string, bindVars map[string]any, collectionKeys map[string]struct{}, limitBindKey string) ([]string, error) {
	if len(groupKeyVariables) != len(pivot.GroupKeys) {
		return nil, fmt.Errorf("terminal Pivot group-key variables = %d, want %d", len(groupKeyVariables), len(pivot.GroupKeys))
	}
	groupKeyValues := make([]string, 0, len(pivot.GroupKeys))
	collectKeys := make([]string, 0, len(pivot.GroupKeys))
	renderer := physicalPlanRenderer{bindVars: bindVars, collectionKeys: collectionKeys, internalPrefix: "construction_"}
	for index, key := range pivot.GroupKeys {
		groupKeyValues = append(groupKeyValues, groupKeyVariables[index])
		columnBind := renderer.newInternalBindKey("reshape_preview_group_column")
		renderer.bindVars[columnBind] = key.Column
		collectKeys = append(collectKeys, fmt.Sprintf("%s = %s[@%s]", groupKeyVariables[index], sourceVariable, columnBind))
	}
	identity, err := renderer.renderGroupedPivotIdentity(pivot, groupKeyValues)
	if err != nil {
		return nil, fmt.Errorf("render terminal Pivot group identity selection: %w", err)
	}
	groupTuple := groupKeyValues[0]
	if len(groupKeyValues) > 1 {
		groupTuple = "[" + strings.Join(groupKeyValues, ", ") + "]"
	}
	lines := []string{
		fmt.Sprintf("LET %s = (", selectedGroupKeysVariable),
		fmt.Sprintf("  FOR %s IN (", sourceVariable),
	}
	for _, line := range strings.Split(strings.TrimSuffix(sourceQuery, "\n"), "\n") {
		lines = append(lines, "    "+line)
	}
	lines = append(lines,
		"  )",
		"  COLLECT "+strings.Join(collectKeys, ", "),
		"  LET "+identityVariable+" = "+identity,
		"  SORT "+identityVariable+" ASC",
		"  LIMIT @"+limitBindKey,
		"  RETURN "+groupTuple,
		")",
	)
	return lines, nil
}

func mergeRenderedBindVars(target, source map[string]any) error {
	for key, value := range source {
		if existing, found := target[key]; found && !reflect.DeepEqual(existing, value) {
			return fmt.Errorf("physical plan bind variable %q has conflicting values across source scans", key)
		}
		target[key] = value
	}
	return nil
}

func stageSequenceVariableNames(plan ir.PhysicalPlan) map[string]struct{} {
	reserved := physicalPlanVariableNames(plan)
	reserved[constructionSourceVariable] = struct{}{}
	reserved["__loom_construction_final_row"] = struct{}{}
	for index := range plan.StageSequence.Stages {
		reserved[fmt.Sprintf("__loom_construction_stage_%d", index+1)] = struct{}{}
	}
	return reserved
}
