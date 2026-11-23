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
	sourceRenderer := stageSourceRenderer{plan: sourcePlan, sequence: sequence, stages: stages, options: options}
	inlineCodedGroupRoot := sourceRenderer.codedGroupSourceRootVariable()
	inlineCodedPivotRoot := sourceRenderer.codedPivotSourceRootVariable()
	inlineStreamingPivotRoot := sourceRenderer.streamingMissingCategoryPivotSourceRootVariable()
	if options.streamingMissingCategoryPivotPreview && inlineStreamingPivotRoot == "" {
		return RenderedPhysicalPlan{}, fmt.Errorf("streaming missing-category Pivot preview source proof failed")
	}
	storedPresenceOutputName := ""
	var storedPresenceParentPath []string
	if options.storedPresenceObject {
		if inlineStreamingPivotRoot == "" {
			return RenderedPhysicalPlan{}, fmt.Errorf("stored-parent presence requires the direct-root streaming Pivot")
		}
		var ok bool
		storedPresenceOutputName, storedPresenceParentPath, ok = streamingPivotStoredPresenceOutputName(sourcePlan, stages[0], inlineStreamingPivotRoot)
		if !ok {
			return RenderedPhysicalPlan{}, fmt.Errorf("stored-parent presence source proof failed")
		}
	}
	if inlineStreamingPivotRoot != "" {
		if err := rebindStreamingPivotSourceProjections(stages, sourcePlan); err != nil {
			return RenderedPhysicalPlan{}, err
		}
	}
	inlineGroupSourceRow := sourceRenderer.groupSourceRowVariable()
	groupRowValueWindowPlan, groupRowValueWindowShape, hasGroupRowValueWindow := terminalGroupRowValuePreviewWindowFor(
		plan, sourcePlan, sequence, options, hasRelatedGroupPreview,
	)
	if hasGroupRowValueWindow {
		inlineGroupSourceRow = ""
	}
	sourceGroupWindowPlan := ir.PhysicalPlan{}
	sourceGroupWindowShape := sourceGroupPreviewShape{}
	hasSourceGroupWindow := false
	if inlineGroupSourceRow != "" {
		sourceGroupWindowPlan, sourceGroupWindowShape, hasSourceGroupWindow = terminalSourceGroupPreviewWindowFor(
			plan, sourcePlan, sequence, stages, options, hasRelatedGroupPreview,
		)
	}
	if sequence.PopulationMappingReturn != nil {
		inlineCodedGroupRoot = ""
		inlineGroupSourceRow = ""
	}
	for _, stage := range stages {
		if stage.GroupedPivot != nil && stage.GroupedPivot.CodedCorrelation != nil && inlineCodedPivotRoot == "" {
			return RenderedPhysicalPlan{}, fmt.Errorf("coded Pivot requires the source root to be streamed into its first stage")
		}
	}
	inlineSourceIntoFirstStage := inlineSourceForKeylessCount || inlineCodedGroupRoot != "" || inlineCodedPivotRoot != "" ||
		inlineStreamingPivotRoot != "" || inlineGroupSourceRow != ""
	if inlineGroupSourceRow != "" || !inlineSourceIntoFirstStage {
		pruneUnusedSourceGroupProjections(&sourcePlan, sequence)
	}
	if !inlineSourceIntoFirstStage {
		pruneUnusedSourcePivotProjections(&sourcePlan, sequence, stages)
	}
	collectionKeys, err := collectionBindKeys(plan)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	reservedVars := stageSequenceReservedVariables(plan)
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
	groupRowValueFrontier := groupRowValuePreviewFrontier{}
	if hasGroupRowValueWindow {
		groupRowValueFrontier, err = renderGroupRowValuePreviewFrontier(
			groupRowValueWindowPlan, groupRowValueWindowShape, sequence, options,
		)
		if err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render terminal Group row-value preview frontier: %w", err)
		}
	}
	sourceGroupFrontier := sourceGroupPreviewFrontier{}
	if hasSourceGroupWindow {
		sourceGroupFrontier, err = renderSourceGroupPreviewFrontier(
			sourceGroupWindowPlan, sourceGroupWindowShape, sequence, collectionKeys,
		)
		if err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render terminal source Group preview frontier: %w", err)
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
		allocator := physicalPlanRenderer{renderContext: renderContext{
			bindVars:       runtimePhysicalBindVars(plan.BindVars, collectionKeys),
			collectionKeys: collectionKeys,
			reservedVars:   reservedVars,
			internalPrefix: "construction_",
		}}
		selectedGroupKeysVariable = allocator.navigation().newInternalVariable("reshape_preview_selected_group_keys")
		groupKeySourceVariable = allocator.navigation().newInternalVariable("reshape_preview_key_row")
		groupIdentityVariable = allocator.navigation().newInternalVariable("reshape_preview_identity")
		groupKeyVariables = make([]string, len(pivot.GroupKeys))
		for index := range groupKeyVariables {
			groupKeyVariables[index] = allocator.navigation().newInternalVariable(fmt.Sprintf("reshape_preview_group_key_%d", index+1))
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
		internalPrefix:                  options.internalPrefix,
		rootIndexHint:                   options.rootIndexHint,
		disableRootIndex:                options.disableRootIndex,
		previewRootKeyWindowVariable:    options.previewRootKeyWindowVariable,
		storedPresenceOutputName:        storedPresenceOutputName,
		storedPresenceParentPath:        storedPresenceParentPath,
		pivotGroupTupleFilter:           options.pivotGroupTupleFilter,
		preserveProjectionPresenceNames: options.preserveProjectionPresenceNames,
		projectionPresenceMarkerColumn:  options.projectionPresenceMarkerColumn,
	}
	if hasGroupRowValueWindow {
		sourceOptions.previewRootKeyWindowVariable = groupRowValueFrontier.rootKeysVariable
	}
	if hasSourceGroupWindow {
		sourceOptions.previewRootKeyWindowVariable = sourceGroupFrontier.rootKeysVariable
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
	var mappingSource RenderedPhysicalPlan
	if sequence.PopulationMappingReturn != nil && inlineSourceIntoFirstStage {
		mappingSourceOptions := sourceOptions
		mappingSourceOptions.terminalReturnVariable = ""
		mappingSourceOptions.omitTerminalReturn = false
		mappingSource, err = renderPhysicalPlanWithOptions(sourcePlan, mappingSourceOptions)
		if err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render original population mapping source rows: %w", err)
		}
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
	bindVars, err := mergeStageBindVars(
		groupKeyQuery.BindVars,
		source.BindVars,
		mappingSource.BindVars,
		groupPreviewWindow.BindVars,
		groupPreviewFrontier.bindVars,
		groupRowValueFrontier.bindVars,
		sourceGroupFrontier.bindVars,
		runtimePhysicalBindVars(plan.BindVars, collectionKeys),
	)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	presenceMarkerRows := projectionPresenceRows(stages, options.projectionPresenceMarkerColumn)
	renderer := physicalPlanRenderer{renderContext: renderContext{
		bindVars:                             bindVars,
		collectionKeys:                       collectionKeys,
		setVariables:                         map[string]string{},
		reservedVars:                         reservedVars,
		internalPrefix:                       options.internalPrefix + "construction_",
		rootVariable:                         inlineStreamingPivotRoot,
		dynamicPivotPreview:                  options.dynamicPivotPreview,
		streamingMissingCategoryPivotPreview: options.streamingMissingCategoryPivotPreview,
		storedPresenceOutputName:             storedPresenceOutputName,
		storedPresenceParentPath:             append([]string(nil), storedPresenceParentPath...),
		preserveProjectionPresenceNames:      options.preserveProjectionPresenceNames,
		projectionPresenceMarkerColumn:       options.projectionPresenceMarkerColumn,
		projectionPresenceMarkerRows:         presenceMarkerRows,
		previewGroupIdentityFilter:           options.previewGroupIdentityFilter,
	}}
	relatedExpandPreviewStageIndex, hasRelatedExpandPreviewStage := terminalRelatedExpandPreviewStage(sequence)
	if options.omitTerminalRowSort || options.terminalProjectionColumn != "" || options.projectionPresenceMarkerColumn != "" ||
		options.twoScanPivotPreview || options.dynamicPivotPreview || options.streamingMissingCategoryPivotPreview {
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
		return renderer.rowLineage().renderConstructionRowLineage(source.Query, stages, *sequence.RowLineageReturn)
	}
	lines := make([]string, 0, 16)
	if hasRelatedGroupPreview {
		lines = append(lines, strings.Split(strings.TrimSuffix(groupPreviewFrontier.query, "\n"), "\n")...)
	} else if hasGroupRowValueWindow {
		lines = append(lines, strings.Split(strings.TrimSuffix(groupRowValueFrontier.query, "\n"), "\n")...)
	} else if hasSourceGroupWindow {
		lines = append(lines, strings.Split(strings.TrimSuffix(sourceGroupFrontier.query, "\n"), "\n")...)
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
	if sequence.PopulationMappingReturn != nil && inlineSourceIntoFirstStage {
		lines = append(lines, fmt.Sprintf("LET %s = (", constructionSourceVariable))
		for _, line := range strings.Split(strings.TrimSuffix(mappingSource.Query, "\n"), "\n") {
			lines = append(lines, "  "+line)
		}
		lines = append(lines, ")")
	}
	if !inlineSourceIntoFirstStage {
		lines = append(lines, fmt.Sprintf("LET %s = (", constructionSourceVariable))
		for _, line := range strings.Split(strings.TrimSuffix(source.Query, "\n"), "\n") {
			lines = append(lines, "  "+line)
		}
		lines = append(lines, ")")
	} else {
		priorRows = ""
	}
	render := stageSequenceRendering{
		renderer: &renderer, sequence: sequence, stages: stages, options: options, source: source,
		inlineSourceIntoFirstStage: inlineSourceIntoFirstStage, inlineCodedGroupRoot: inlineCodedGroupRoot,
		inlineGroupSourceRow: inlineGroupSourceRow, inlineCodedPivotRoot: inlineCodedPivotRoot,
		inlineStreamingPivotRoot: inlineStreamingPivotRoot, hasRelatedGroupPreview: hasRelatedGroupPreview,
		relatedExpandPreviewStageIndex: relatedExpandPreviewStageIndex, relatedExpandPreviewLimitBindKey: relatedExpandPreviewLimitBindKey,
	}
	lines, priorRows, err = render.renderStages(lines, priorRows)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	lines, err = render.renderFinal(lines, priorRows)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	query := strings.Join(lines, "\n") + "\n"
	return RenderedPhysicalPlan{
		Query: query, BindVars: pruneUnusedRuntimeBindVars(renderer.bindVars, query),
		PartialValidation: hasRelatedGroupPreview,
	}, nil
}

func mergeStageBindVars(groups ...map[string]any) (map[string]any, error) {
	result := make(map[string]any)
	for _, values := range groups {
		if err := mergeRenderedBindVars(result, values); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func projectionPresenceRows(stages []ir.PhysicalConstructionStage, markerColumn string) map[string]struct{} {
	rows := make(map[string]struct{}, len(stages)+1)
	if markerColumn == "" {
		return rows
	}
	for _, stage := range stages {
		if stage.InputRowVariable != "" {
			rows[stage.InputRowVariable] = struct{}{}
		}
	}
	rows["__loom_construction_final_row"] = struct{}{}
	return rows
}

func stageSequenceReservedVariables(plan ir.PhysicalPlan) map[string]struct{} {
	reserved := stageSequenceVariableNames(plan)
	for _, name := range []string{"cohort_member", "member", "resource", "selected", "source", "members", "definitions", "all_memberships", "unassigned", "unassigned_records", "unassigned_members", "unassigned_row", "revision", "revision_guard", "selection", "selection_guard", "policy_guard", "rows", "row_value"} {
		reserved[name] = struct{}{}
	}
	return reserved
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
	renderer := physicalPlanRenderer{renderContext: renderContext{bindVars: bindVars, collectionKeys: collectionKeys, internalPrefix: "construction_"}}
	for index, key := range pivot.GroupKeys {
		groupKeyValues = append(groupKeyValues, groupKeyVariables[index])
		columnBind := renderer.newInternalBindKey("reshape_preview_group_column")
		renderer.bindVars[columnBind] = key.Column
		collectKeys = append(collectKeys, fmt.Sprintf("%s = %s[@%s]", groupKeyVariables[index], sourceVariable, columnBind))
	}
	identity, err := renderer.tableShape().renderGroupedPivotIdentity(pivot, groupKeyValues)
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
