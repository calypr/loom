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
	stages := pruneUnusedRelatedOutputsForCountRows(sequence)
	inlineSourceForKeylessCount := terminalKeylessCountRowsOnly(sequence)
	sourcePlan := ir.ClonePhysicalPlan(plan)
	sourcePlan.StageSequence = nil
	sourcePlan.PreviewSourceWindowByRootID = sequence.PreviewSourceWindowByRootID && sequence.PreviewLimitBindKey != ""
	if !inlineSourceForKeylessCount {
		pruneUnusedSourceGroupProjections(&sourcePlan, sequence)
	}
	collectionKeys, err := collectionBindKeys(plan)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	reservedVars := stageSequenceVariableNames(plan)
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
		pivotGroupTupleFilter:           options.pivotGroupTupleFilter,
		preserveProjectionPresenceNames: options.preserveProjectionPresenceNames,
		projectionPresenceMarkerColumn:  options.projectionPresenceMarkerColumn,
	}
	if inlineSourceForKeylessCount {
		sourceOptions.omitTerminalReturn = true
	}
	source, err := renderPhysicalPlanWithOptions(sourcePlan, sourceOptions)
	if err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("render source projection: %w", err)
	}
	bindVars := make(map[string]any, len(source.BindVars)+len(groupKeyQuery.BindVars)+len(plan.BindVars))
	for _, values := range []map[string]any{
		groupKeyQuery.BindVars,
		source.BindVars,
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
	}
	if sequence.RowLineageReturn != nil {
		return renderer.renderConstructionRowLineage(source.Query, stages[0], *sequence.RowLineageReturn)
	}

	lines := make([]string, 0, 16)
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
	if !inlineSourceForKeylessCount {
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
		if inlineSourceForKeylessCount && index == 0 {
			for _, line := range strings.Split(strings.TrimSuffix(source.Query, "\n"), "\n") {
				lines = append(lines, "  "+line)
			}
		}
		if stage.Kind == ir.PhysicalStageGroupOp {
			rendered, renderErr := renderer.renderConstructionGroupStage(stage, priorRows)
			if renderErr != nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("render stage %q group: %w", stage.ID, renderErr)
			}
			lines = append(lines, rendered...)
			lines = append(lines, ")")
			priorRows = stageRows
			continue
		}
		lines = append(lines, fmt.Sprintf("  FOR %s IN %s", stage.InputRowVariable, priorRows))
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
			rendered, renderErr := renderer.renderConstructionRelatedExpandStage(stage)
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
	lines = append(lines, fmt.Sprintf("FOR %s IN %s", finalRow, priorRows))
	if !options.omitTerminalRowSort && (len(stages) == 0 || stages[len(stages)-1].Kind != ir.PhysicalStageGroupOp) {
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
	return RenderedPhysicalPlan{Query: query, BindVars: pruneUnusedRuntimeBindVars(renderer.bindVars, query)}, nil
}

func pruneUnusedSourceGroupProjections(plan *ir.PhysicalPlan, sequence *ir.PhysicalStageSequence) {
	if sequence.CellTraceReturn != nil || len(sequence.Stages) == 0 {
		return
	}
	groupStage := sequence.Stages[0]
	if groupStage.Kind != ir.PhysicalStageGroupOp || groupStage.Group == nil || groupStage.InputStageID != sequence.SourceStageID {
		return
	}
	required := make(map[string]bool, len(groupStage.Group.Keys)+len(groupStage.Group.Aggregates))
	for _, key := range groupStage.Group.Keys {
		required[key.InputColumn] = true
	}
	for _, aggregate := range groupStage.Group.Aggregates {
		if aggregate.InputColumn != "" {
			required[aggregate.InputColumn] = true
		}
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
