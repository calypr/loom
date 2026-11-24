package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

type stageSequenceRendering struct {
	renderer                         *physicalPlanRenderer
	sequence                         *ir.PhysicalStageSequence
	stages                           []ir.PhysicalConstructionStage
	options                          physicalRenderOptions
	source                           RenderedPhysicalPlan
	inlineSourceIntoFirstStage       bool
	inlineCodedGroupRoot             string
	inlineGroupSourceRow             string
	inlineCodedPivotRoot             string
	inlineStreamingPivotRoot         string
	hasRelatedGroupPreview           bool
	relatedExpandPreviewStageIndex   int
	relatedExpandPreviewLimitBindKey string
}

func (render stageSequenceRendering) renderStages(lines []string, priorRows string) ([]string, string, error) {
	options := render.options
	source := render.source
	stages := render.stages
	sequence := render.sequence
	renderer := render.renderer
	inlineCodedPivotRoot := render.inlineCodedPivotRoot
	inlineStreamingPivotRoot := render.inlineStreamingPivotRoot
	inlineSourceIntoFirstStage := render.inlineSourceIntoFirstStage
	inlineCodedGroupRoot := render.inlineCodedGroupRoot
	inlineGroupSourceRow := render.inlineGroupSourceRow
	hasRelatedGroupPreview := render.hasRelatedGroupPreview
	relatedExpandPreviewStageIndex := render.relatedExpandPreviewStageIndex
	relatedExpandPreviewLimitBindKey := render.relatedExpandPreviewLimitBindKey
	for index, stage := range stages {
		stageRows := fmt.Sprintf("__loom_construction_stage_%d", index+1)
		lines = append(lines, fmt.Sprintf("LET %s = (", stageRows))
		inlineCodedPivotStage := index == 0 && inlineCodedPivotRoot != "" && stage.Kind == ir.PhysicalStagePivotOp
		inlineStreamingPivotStage := index == 0 && inlineStreamingPivotRoot != "" && stage.Kind == ir.PhysicalStagePivotOp
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
				return nil, "", fmt.Errorf("render stage %q group: %w", stage.ID, renderErr)
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
				return nil, "", fmt.Errorf("render stage %q coded group: %w", stage.ID, renderErr)
			}
			lines = append(lines, rendered...)
			lines = append(lines, ")")
			priorRows = stageRows
			continue
		}
		if stage.Kind == ir.PhysicalStageCohortGroupOp {
			rendered, renderErr := renderer.renderConstructionCohortGroupStage(stage, priorRows)
			if renderErr != nil {
				return nil, "", fmt.Errorf("render stage %q cohort group: %w", stage.ID, renderErr)
			}
			lines = append(lines, rendered...)
			lines = append(lines, ")")
			priorRows = stageRows
			continue
		}
		if !inlineCodedPivotStage && !inlineStreamingPivotStage {
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
				return nil, "", fmt.Errorf("stage %q is missing grouped pivot payload", stage.ID)
			}

			previewLimitBindKey := ""
			if sequence.PreviewTerminalPivotWindow && index == len(stages)-1 && !options.twoScanPivotPreview {
				previewLimitBindKey = sequence.PreviewLimitBindKey
			}
			if inlineCodedPivotStage && stage.GroupedPivot.CodedSourceVariable != inlineCodedPivotRoot {
				return nil, "", fmt.Errorf("coded Pivot source variable differs from the inlined root scan")
			}
			rendered, renderErr := renderer.tableShape().renderGroupedTablePivot(*stage.GroupedPivot, !stage.GroupedPivot.OneInputRowPerGroup, previewLimitBindKey)
			if renderErr != nil {
				return nil, "", fmt.Errorf("render stage %q pivot: %w", stage.ID, renderErr)
			}
			lines = appendIndented(lines, rendered)
			lines = append(lines, "  RETURN "+stage.OutputRowVariable)
		case ir.PhysicalStageUnpivotOp:
			if stage.Unpivot == nil {
				return nil, "", fmt.Errorf("stage %q is missing unpivot payload", stage.ID)
			}
			rendered, renderErr := renderer.tableShape().renderTableUnpivot(*stage.Unpivot)
			if renderErr != nil {
				return nil, "", fmt.Errorf("render stage %q unpivot: %w", stage.ID, renderErr)
			}
			lines = appendIndented(lines, rendered)
			lines = append(lines, "  RETURN "+stage.OutputRowVariable)
		case ir.PhysicalStageExpandOp:
			rendered, renderErr := renderer.renderConstructionExpandStage(stage)
			if renderErr != nil {
				return nil, "", fmt.Errorf("render stage %q expand: %w", stage.ID, renderErr)
			}
			lines = appendIndented(lines, rendered)
		case ir.PhysicalStageRelatedExpandOp:
			previewLimitBindKey := ""
			if index == relatedExpandPreviewStageIndex {
				previewLimitBindKey = relatedExpandPreviewLimitBindKey
			}
			rendered, renderErr := renderer.navigation().renderConstructionRelatedExpandStage(stage, previewLimitBindKey)
			if renderErr != nil {
				return nil, "", fmt.Errorf("render stage %q related expansion: %w", stage.ID, renderErr)
			}
			lines = appendIndented(lines, rendered)
		case ir.PhysicalStageDeriveOp, ir.PhysicalStageFilterOp, ir.PhysicalStageRelatedEligibilityOp, ir.PhysicalStageRelatedSourceOp, ir.PhysicalStageRelatedFieldOp:
			for operationIndex, operation := range stage.DerivedLets {
				if operation.Kind != ir.PhysicalExpressionLetOp {
					return nil, "", fmt.Errorf("stage %q derived operation %d has kind %q", stage.ID, operationIndex, operation.Kind)
				}
				rendered, renderErr := renderer.renderExpressionLet(operation, "  ")
				if renderErr != nil {
					return nil, "", fmt.Errorf("render stage %q derived operation %d: %w", stage.ID, operationIndex, renderErr)
				}
				lines = append(lines, rendered...)
			}
			if stage.Filter != nil {
				rendered, renderErr := renderer.renderScopeOperation(ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: stage.Filter}, "  ")
				if renderErr != nil {
					return nil, "", fmt.Errorf("render stage %q filter: %w", stage.ID, renderErr)
				}
				lines = append(lines, rendered...)
			}
			object, renderErr := renderer.renderReturn(ir.PhysicalReturn{Projections: stage.OutputProjections})
			if renderErr != nil {
				return nil, "", fmt.Errorf("render stage %q output row: %w", stage.ID, renderErr)
			}
			lines = append(lines, fmt.Sprintf("  LET %s = %s", stage.OutputRowVariable, object))
			lines = append(lines, "  RETURN "+stage.OutputRowVariable)
		default:
			return nil, "", fmt.Errorf("stage %q has unsupported operation %q", stage.ID, stage.Kind)
		}
		lines = append(lines, ")")
		priorRows = stageRows
	}
	return lines, priorRows, nil
}

func (render stageSequenceRendering) renderFinal(lines []string, priorRows string) ([]string, error) {
	options := render.options
	stages := render.stages
	sequence := render.sequence
	renderer := render.renderer
	hasRelatedGroupPreview := render.hasRelatedGroupPreview
	finalRow := "__loom_construction_final_row"
	if hasRelatedGroupPreview && options.previewGroupIdentityFilter != nil && options.previewGroupIdentityFilter.StageIndex < 0 {
		groupRows := renderer.navigation().newInternalVariable("related_group_preview_filtered_rows")
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
		if column.Name == sequence.OutputAuthResourcePathColumn {
			continue
		}
		if options.terminalProjectionColumn != "" && column.Name != options.terminalProjectionColumn {
			continue
		}
		if column.Name == "auth_resource_path" && sequence.OutputAuthResourcePathBindKey != "" {
			return nil, fmt.Errorf("final stage already declares the reserved authorization path column")
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
	if sequence.OutputAuthResourcePathColumn != "" && options.terminalProjectionColumn == "" {
		projections = append(projections, ir.PhysicalProjection{Name: "auth_resource_path", Hidden: true, Value: ir.PhysicalValue{Variable: finalRow, Path: []string{sequence.OutputAuthResourcePathColumn}}})
	}
	returned, err := renderer.renderReturn(ir.PhysicalReturn{Projections: projections})
	if err != nil {
		return nil, fmt.Errorf("render final stage output: %w", err)
	}
	if sequence.PopulationMappingReturn != nil {
		mappingLines := renderer.renderConstructionPopulationMappingReturn(*sequence.PopulationMappingReturn, constructionSourceVariable, finalRow)
		lines = append(lines, mappingLines...)
	} else if sequence.CellTraceReturn != nil {
		terminal := *sequence.CellTraceReturn
		terminal.Value = ir.PhysicalExpression{
			Kind:  ir.PhysicalValueExpression,
			Value: &ir.PhysicalValue{Variable: finalRow, Path: []string{terminal.Construction.OutputColumn}},
		}
		identity := ir.PhysicalExpression{
			Kind:  ir.PhysicalValueExpression,
			Value: &ir.PhysicalValue{Variable: finalRow, Path: []string{terminal.Construction.RowIdentityColumn}},
		}
		identityFields := terminal.Construction.RowIdentityFields
		if len(identityFields) == 1 && identityFields[0] == terminal.Construction.RowIdentityColumn && terminal.Construction.RowIdentityColumn == "__loom_row_id" {
			terminal.ExplicitIdentity = &identity
			terminal.IdentityParts = nil
		} else if len(identityFields) == 2 && identityFields[0] == "project" && identityFields[1] == "_key" && terminal.Construction.RowIdentityColumn == "_key" {
			terminal.ExplicitIdentity = nil
			terminal.IdentityParts = []ir.PhysicalPopulationMappingIdentityPart{
				{Name: "project", Expression: ir.PhysicalExpression{
					Kind: ir.PhysicalValueExpression, Value: &ir.PhysicalValue{BindKey: "project"},
				}},
				{Name: "_key", Expression: identity},
			}
		} else if len(identityFields) == 1 && identityFields[0] == terminal.Construction.RowIdentityColumn {
			terminal.ExplicitIdentity = nil
			terminal.IdentityParts = []ir.PhysicalPopulationMappingIdentityPart{{
				Name: terminal.Construction.RowIdentityColumn, Expression: identity,
			}}
		} else {
			return nil, fmt.Errorf("construction trace has an unsupported published row identity")
		}
		traceLines, traceErr := renderer.cellTraces().renderCellTraceReturn(terminal)
		if traceErr != nil {
			return nil, fmt.Errorf("render construction cell trace: %w", traceErr)
		}
		lines = append(lines, traceLines...)
	} else {
		lines = append(lines, "RETURN "+returned)
	}
	return lines, nil
}
