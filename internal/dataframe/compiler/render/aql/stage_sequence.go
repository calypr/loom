package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

const constructionSourceVariable = "__loom_construction_source_projection"

func renderPhysicalStageSequence(plan ir.PhysicalPlan) (RenderedPhysicalPlan, error) {
	sequence := plan.StageSequence
	if sequence == nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("physical construction stage sequence is required")
	}
	stages := pruneUnusedRelatedOutputsForCountRows(sequence)
	sourcePlan := ir.ClonePhysicalPlan(plan)
	sourcePlan.StageSequence = nil
	source, err := RenderPhysicalPlan(sourcePlan)
	if err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("render source projection: %w", err)
	}
	collectionKeys, err := collectionBindKeys(plan)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	bindVars := make(map[string]any, len(source.BindVars)+len(plan.BindVars))
	for key, value := range source.BindVars {
		bindVars[key] = value
	}
	for key, value := range runtimePhysicalBindVars(plan.BindVars, collectionKeys) {
		bindVars[key] = value
	}
	renderer := physicalPlanRenderer{
		bindVars:       bindVars,
		collectionKeys: collectionKeys,
		setVariables:   map[string]string{},
		reservedVars:   stageSequenceVariableNames(plan),
		internalPrefix: "construction_",
	}

	lines := []string{fmt.Sprintf("LET %s = (", constructionSourceVariable)}
	for _, line := range strings.Split(strings.TrimSuffix(source.Query, "\n"), "\n") {
		lines = append(lines, "  "+line)
	}
	lines = append(lines, ")")
	priorRows := constructionSourceVariable
	for index, stage := range stages {
		stageRows := fmt.Sprintf("__loom_construction_stage_%d", index+1)
		lines = append(lines, fmt.Sprintf("LET %s = (", stageRows))
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
			rendered, renderErr := renderer.renderGroupedTablePivot(*stage.GroupedPivot)
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
		case ir.PhysicalStageDeriveOp, ir.PhysicalStageFilterOp, ir.PhysicalStageRelatedSourceOp, ir.PhysicalStageRelatedFieldOp:
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
	lines = append(lines, fmt.Sprintf("SORT %s.%s ASC", finalRow, sequence.FinalRowIdentity))
	if sequence.PreviewLimitBindKey != "" {
		lines = append(lines, "LIMIT @"+sequence.PreviewLimitBindKey)
	}
	projections := make([]ir.PhysicalProjection, 0, len(sequence.FinalColumns))
	for _, column := range sequence.FinalColumns {
		if column.Name == "auth_resource_path" && sequence.OutputAuthResourcePathBindKey != "" {
			return RenderedPhysicalPlan{}, fmt.Errorf("final stage already declares the reserved authorization path column")
		}
		projections = append(projections, ir.PhysicalProjection{
			Name: column.Name, Hidden: column.Internal,
			Value: ir.PhysicalValue{Variable: finalRow, Path: []string{column.Name}},
		})
	}
	if sequence.OutputAuthResourcePathBindKey != "" {
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

func stageSequenceVariableNames(plan ir.PhysicalPlan) map[string]struct{} {
	reserved := physicalPlanVariableNames(plan)
	reserved[constructionSourceVariable] = struct{}{}
	reserved["__loom_construction_final_row"] = struct{}{}
	for index := range plan.StageSequence.Stages {
		reserved[fmt.Sprintf("__loom_construction_stage_%d", index+1)] = struct{}{}
	}
	return reserved
}
