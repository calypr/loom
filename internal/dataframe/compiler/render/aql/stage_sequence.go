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

type stageSourceRenderer struct {
	plan     ir.PhysicalPlan
	sequence *ir.PhysicalStageSequence
	stages   []ir.PhysicalConstructionStage
	options  physicalRenderOptions
}

func (source stageSourceRenderer) groupSourceRowVariable() string {
	sourcePlan, sequence, stages, options := source.plan, source.sequence, source.stages, source.options
	if sequence == nil || len(stages) != 1 || sequence.RowLineageReturn != nil || sequence.CellTraceReturn != nil ||
		sequence.PreviewSourceWindowByRootID || sequence.PreviewTerminalPivotWindow || sequence.OutputAuthResourcePathBindKey != "" ||
		options.omitTerminalReturn || options.terminalReturnVariable != "" || options.terminalProjectionColumn != "" ||
		options.omitTerminalRowSort || options.projectionPresenceMarkerColumn != "" ||
		len(options.preserveProjectionPresenceNames) != 0 || options.twoScanPivotPreview || options.dynamicPivotPreview {
		return ""
	}
	stage := stages[0]
	group := stage.Group
	keylessRowValueGroup := group != nil && len(group.Keys) == 0 && len(group.RowValues) > 0
	hasGroupKey := group != nil && (len(group.Keys) != 0 || group.SemanticKey != nil || group.CodingKey != nil)
	if stage.ID != sequence.FinalStageID || stage.InputStageID != sequence.SourceStageID ||
		stage.Kind != ir.PhysicalStageGroupOp || group == nil || (!hasGroupKey && !keylessRowValueGroup) ||
		group.RootContributorInputColumn != "" || group.DynamicRuntimeKeysVariable != "" ||
		!constructionGroupCountsOnlyRows(group) ||
		(len(group.RowValues) > 0 && !constructionGroupRowValuesCanAggregate(group.RowValues)) {
		return ""
	}
	if len(sourcePlan.Operations) < 2 || sourcePlan.PreviewSourceWindowByRootID ||
		sourcePlan.Operations[0].Kind != ir.PhysicalRootScanOp || sourcePlan.Operations[0].RootScan == nil {
		return ""
	}
	last := len(sourcePlan.Operations) - 1
	if sourcePlan.Operations[last].Kind != ir.PhysicalReturnOp || sourcePlan.Operations[last].Return == nil {
		return ""
	}
	for index, operation := range sourcePlan.Operations[:last] {
		if index == 0 {
			if operation.Kind != ir.PhysicalRootScanOp || operation.RootScan == nil {
				return ""
			}
			continue
		}
		switch operation.Kind {
		case ir.PhysicalFilterOp, ir.PhysicalDerivedLetOp, ir.PhysicalExpressionLetOp, ir.PhysicalSetOp, ir.PhysicalUnnestOp:
		default:
			return ""
		}
	}
	layout, err := buildNavigationRenderLayout(sourcePlan)
	if err != nil || len(layout.rootWindow) != 0 || len(layout.unnests) > 1 {
		return ""
	}
	if len(layout.unnests) == 1 {
		if group.SemanticKey != nil || group.CodingKey == nil && len(group.Keys) == 0 && len(group.RowValues) == 0 {
			return ""
		}
	}
	for _, item := range layout.postWindow {
		if item.operation.Kind != ir.PhysicalExpressionLetOp {
			return ""
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

func (source stageSourceRenderer) codedGroupSourceRootVariable() string {
	sourcePlan, sequence, stages, options := source.plan, source.sequence, source.stages, source.options
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

func (source stageSourceRenderer) codedPivotSourceRootVariable() string {
	sourcePlan, sequence, stages, options := source.plan, source.sequence, source.stages, source.options
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

func (source stageSourceRenderer) streamingMissingCategoryPivotSourceRootVariable() string {
	sourcePlan, sequence, stages, options := source.plan, source.sequence, source.stages, source.options
	if sequence == nil || !options.streamingMissingCategoryPivotPreview || len(stages) != 1 ||
		sequence.RowLineageReturn != nil || sequence.CellTraceReturn != nil || sequence.PopulationMappingReturn != nil ||
		sequence.PreviewSourceWindowByRootID || sequence.OutputAuthResourcePathBindKey != "" ||
		options.terminalProjectionColumn != "" || options.projectionPresenceMarkerColumn != "" ||
		len(options.preserveProjectionPresenceNames) != 0 || options.twoScanPivotPreview || options.dynamicPivotPreview {
		return ""
	}
	stage := stages[0]
	pivot := stage.GroupedPivot
	if stage.ID != sequence.FinalStageID || stage.InputStageID != sequence.SourceStageID ||
		stage.Kind != ir.PhysicalStagePivotOp || pivot == nil || pivot.OneInputRowPerGroup ||
		pivot.CodedCorrelation != nil || len(pivot.GroupKeys) == 0 || len(pivot.RowValues) != 0 ||
		pivot.RootContributorInputColumn != "" || pivot.RootContributorOutputColumn != "" ||
		!pivot.CategoryPresenceFromInput || pivot.CategoryPresence != nil || pivot.CategoryPresenceColumn == "" {
		return ""
	}
	hasMissing := false
	for _, category := range pivot.Categories {
		if category.MatchKind == ir.PhysicalPivotCategoryMissingMatch {
			hasMissing = true
		}
	}
	if !hasMissing || len(sourcePlan.Operations) < 2 || sourcePlan.PreviewSourceWindowByRootID ||
		sourcePlan.Operations[0].Kind != ir.PhysicalRootScanOp || sourcePlan.Operations[0].RootScan == nil ||
		sourcePlan.Operations[0].RootScan.Population != nil {
		return ""
	}
	rootVariable := sourcePlan.Operations[0].RootScan.Variable
	if rootVariable == "" {
		return ""
	}
	returnCount := 0
	sourceProjectionNames := make(map[string]struct{})
	for index, operation := range sourcePlan.Operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 || operation.RootScan == nil {
				return ""
			}
		case ir.PhysicalFilterOp:
			if operation.Filter == nil {
				return ""
			}
		case ir.PhysicalDerivedLetOp:
			if operation.DerivedLet == nil {
				return ""
			}
		case ir.PhysicalExpressionLetOp:
			if operation.ExpressionLet == nil {
				return ""
			}
		case ir.PhysicalReturnOp:
			returnCount++
			if index != len(sourcePlan.Operations)-1 || operation.Return == nil {
				return ""
			}
			presenceOutputs := 0
			for _, projection := range operation.Return.Projections {
				if projection.Name == "" {
					return ""
				}
				if _, duplicate := sourceProjectionNames[projection.Name]; duplicate {
					return ""
				}
				sourceProjectionNames[projection.Name] = struct{}{}
				if projection.PresenceOutput {
					if projection.Name != pivot.CategoryPresenceColumn || projection.Presence == nil ||
						projection.Presence.Source.Variable != rootVariable || projection.Presence.Source.BindKey != "" {
						return ""
					}
					presenceOutputs++
					continue
				}
				if !directRootCategoryProjection(projection, rootVariable) {
					return ""
				}
			}
			if presenceOutputs != 1 {
				return ""
			}
		default:
			return ""
		}
	}
	if returnCount != 1 {
		return ""
	}
	if !streamingPivotInputsAreSourceRows(*pivot, stage.InputRowVariable, sourceProjectionNames) {
		return ""
	}
	if _, collision := physicalPlanVariableNames(sourcePlan)[pivot.InputRowVariable]; collision {
		return ""
	}
	return rootVariable
}

func streamingPivotStoredPresenceOutputName(sourcePlan ir.PhysicalPlan, stage ir.PhysicalConstructionStage, rootVariable string) (string, []string, bool) {
	pivot := stage.GroupedPivot
	if pivot == nil || pivot.CategoryColumn == "" || pivot.CategoryPresenceColumn == "" {
		return "", nil, false
	}
	var sourceReturn *ir.PhysicalReturn
	for index := range sourcePlan.Operations {
		operation := &sourcePlan.Operations[index]
		if operation.Kind != ir.PhysicalReturnOp {
			continue
		}
		if sourceReturn != nil || index != len(sourcePlan.Operations)-1 || operation.Return == nil {
			return "", nil, false
		}
		sourceReturn = operation.Return
	}
	if sourceReturn == nil {
		return "", nil, false
	}
	var categoryPath []string
	var presence *ir.PhysicalProjectionPresence
	for index := range sourceReturn.Projections {
		projection := &sourceReturn.Projections[index]
		if projection.Name == pivot.CategoryColumn {
			var ok bool
			categoryPath, ok = directRootProjectionPath(*projection, rootVariable)
			if !ok {
				return "", nil, false
			}
		}
		if projection.Name == pivot.CategoryPresenceColumn && projection.PresenceOutput {
			if projection.Presence == nil || projection.Presence.Source.Variable != rootVariable || projection.Presence.Source.BindKey != "" {
				return "", nil, false
			}
			copy := *projection.Presence
			presence = &copy
		}
	}
	if len(categoryPath) < 3 || presence == nil || len(presence.Paths) != 1 || len(presence.Paths[0]) < 1 {
		return "", nil, false
	}
	fullPath := append(append([]string(nil), presence.Source.Path...), presence.Paths[0]...)
	if !reflect.DeepEqual(fullPath, categoryPath) || len(fullPath) < 3 || strings.Join(fullPath[:len(fullPath)-1], ".") == "payload" {
		return "", nil, false
	}
	for _, segment := range fullPath {
		if !previewSourcePathSegmentPattern.MatchString(segment) {
			return "", nil, false
		}
	}
	return pivot.CategoryPresenceColumn, append([]string(nil), fullPath[:len(fullPath)-1]...), true
}

func directRootProjectionPath(projection ir.PhysicalProjection, rootVariable string) ([]string, bool) {
	if projection.Expression == nil {
		if projection.Value.Variable != rootVariable || projection.Value.BindKey != "" || len(projection.Value.Path) == 0 {
			return nil, false
		}
		return append([]string(nil), projection.Value.Path...), true
	}
	expression := projection.Expression
	if expression.Kind != ir.PhysicalExtractExpression || expression.Cardinality != ir.PhysicalScalarCardinality || expression.Extract == nil {
		return nil, false
	}
	extract := expression.Extract
	if extract.ExecutionMode != ir.PhysicalSelectorDirectScalar || extract.Source.Variable != rootVariable || extract.Source.BindKey != "" ||
		len(extract.Source.Path) == 0 || len(extract.Fallbacks) != 0 || extract.Prepared != nil || extract.Distinct ||
		extract.UnitNormalization != nil || extract.Selector.Filter != nil || len(extract.Selector.Steps) == 0 {
		return nil, false
	}
	path := append([]string(nil), extract.Source.Path...)
	for _, step := range extract.Selector.Steps {
		if step.Field == "" || step.Iterate || step.Index != nil {
			return nil, false
		}
		path = append(path, step.Field)
	}
	return path, true
}

func streamingPivotInputsAreSourceRows(pivot ir.PhysicalGroupedPivot, inputRowVariable string, sourceProjectionNames map[string]struct{}) bool {
	if inputRowVariable == "" || len(pivot.InputProjections) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(pivot.InputProjections))
	for _, projection := range pivot.InputProjections {
		if projection.Name == "" || projection.Expression != nil || projection.Presence != nil || projection.PresenceOutput ||
			projection.Value.Variable != inputRowVariable || projection.Value.BindKey != "" ||
			len(projection.Value.Path) != 1 || projection.Value.Path[0] != projection.Name {
			return false
		}
		if _, ok := sourceProjectionNames[projection.Name]; !ok {
			return false
		}
		if _, duplicate := seen[projection.Name]; duplicate {
			return false
		}
		seen[projection.Name] = struct{}{}
	}
	required := []string{pivot.CategoryColumn, pivot.ValueColumn, pivot.CategoryPresenceColumn}
	for _, key := range pivot.GroupKeys {
		required = append(required, key.Column)
	}
	for _, name := range required {
		if name == "" {
			return false
		}
		if _, ok := seen[name]; !ok {
			return false
		}
	}
	return true
}

func rebindStreamingPivotSourceProjections(stages []ir.PhysicalConstructionStage, sourcePlan ir.PhysicalPlan) error {
	if len(stages) != 1 || stages[0].GroupedPivot == nil {
		return fmt.Errorf("streaming missing-category Pivot source rebinding requires one Pivot stage")
	}
	var sourceReturn *ir.PhysicalReturn
	for index := range sourcePlan.Operations {
		operation := &sourcePlan.Operations[index]
		if operation.Kind == ir.PhysicalReturnOp && operation.Return != nil {
			sourceReturn = operation.Return
			break
		}
	}
	if sourceReturn == nil {
		return fmt.Errorf("streaming missing-category Pivot source has no RETURN")
	}
	sourceProjections := make(map[string]ir.PhysicalProjection, len(sourceReturn.Projections))
	for _, projection := range sourceReturn.Projections {
		if projection.Name == "" {
			return fmt.Errorf("streaming missing-category Pivot source has an unnamed projection")
		}
		if _, duplicate := sourceProjections[projection.Name]; duplicate {
			return fmt.Errorf("streaming missing-category Pivot source repeats projection %q", projection.Name)
		}
		sourceProjections[projection.Name] = projection
	}
	pivot := *stages[0].GroupedPivot
	projections := make([]ir.PhysicalProjection, 0, len(pivot.InputProjections))
	for _, input := range pivot.InputProjections {
		projection, ok := sourceProjections[input.Name]
		if !ok {
			return fmt.Errorf("streaming missing-category Pivot source is missing input projection %q", input.Name)
		}
		projection.Hidden = input.Hidden
		projections = append(projections, projection)
	}
	pivot.InputProjections = projections
	stages[0].GroupedPivot = &pivot
	return nil
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
	if groupStage.Group.SemanticKey != nil {
		required[groupStage.Group.SemanticKey.SourceIdentityColumn] = true
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
	visited := make(map[ir.Node]bool)
	var visit func(ir.Node)
	visit = func(node ir.Node) {
		if visited[node] {
			return
		}
		visited[node] = true
		switch node := node.(type) {
		case *ir.PhysicalValue:
			addSourceGroupVariableReference(needed, node.Variable)
		case *ir.PhysicalSubplan:
			for _, capture := range node.Captures {
				addSourceGroupVariableReference(needed, capture)
			}
		case *ir.PhysicalOperation:
			if node.Kind == ir.PhysicalSetOp && node.Set != nil {
				addSourceGroupVariableReference(needed, node.Set.SourceSetVariable)
				if node.Set.Prepared != nil {
					addSourceGroupVariableReference(needed, node.Set.Prepared.SourceSetVariable)
				}
			}
		case *ir.PhysicalExpression:
			switch node.Kind {
			case ir.PhysicalObjectLookupExpression:
				if node.ObjectLookup != nil {
					addSourceGroupVariableReference(needed, node.ObjectLookup.ObjectVariable)
				}
			case ir.PhysicalObjectKeysExpression:
				if node.ObjectKeys != nil {
					addSourceGroupVariableReference(needed, node.ObjectKeys.ObjectVariable)
				}
			case ir.PhysicalExtractExpression:
				if node.Extract != nil && node.Extract.Prepared != nil {
					addSourceGroupVariableReference(needed, node.Extract.Prepared.SetVariable)
				}
			case ir.PhysicalPivotExpression:
				if node.Pivot != nil {
					for _, reference := range []*ir.PhysicalPreparedReference{node.Pivot.PreparedKey, node.Pivot.PreparedValue} {
						if reference != nil {
							addSourceGroupVariableReference(needed, reference.SetVariable)
						}
					}
				}
			}
		}
		for _, child := range ir.Children(node) {
			visit(child)
		}
	}
	visit(&operation)
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
	if expression.Kind != ir.PhysicalExtractExpression || expression.Extract == nil {
		return false
	}
	extract := expression.Extract
	if extract.Source.Variable != root || extract.Source.BindKey != "" ||
		len(extract.Source.Path) == 0 || len(extract.Fallbacks) != 0 || extract.Prepared != nil || extract.Distinct ||
		extract.UnitNormalization != nil || extract.Selector.Filter != nil || len(extract.Selector.Steps) == 0 {
		return false
	}
	switch expression.Cardinality {
	case ir.PhysicalScalarCardinality:
	case ir.PhysicalArrayCardinality:
	default:
		return false
	}
	switch extract.ExecutionMode {
	case ir.PhysicalSelectorDirectScalar:
		if expression.Cardinality != ir.PhysicalScalarCardinality {
			return false
		}
	case ir.PhysicalSelectorConditionalArray:
	default:
		return false
	}
	hasIteration := false
	for _, step := range extract.Selector.Steps {
		if step.Field == "" || step.Index != nil || (extract.ExecutionMode == ir.PhysicalSelectorDirectScalar && step.Iterate) {
			return false
		}
		hasIteration = hasIteration || step.Iterate
	}
	return extract.ExecutionMode == ir.PhysicalSelectorDirectScalar || hasIteration
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
		len(stage.Group.Keys) == 0 && stage.Group.CodingKey == nil &&
		stage.Group.RootContributorInputColumn == "" && stage.Group.DynamicRuntimeKeysVariable == "" &&
		constructionGroupCountsOnlyRows(stage.Group)
}

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
	if group.DynamicRuntimeKeysVariable != "" || len(group.Aggregates) == 0 {
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
