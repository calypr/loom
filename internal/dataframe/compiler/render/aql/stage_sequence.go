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
	sourcePlan := ir.ClonePhysicalPlan(plan)
	sourcePlan.StageSequence = nil
	source, err := RenderPhysicalPlan(sourcePlan)
	if err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("render source projection: %w", err)
	}
	collectionKeys, err := collectionBindKeys(sourcePlan)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	bindVars := make(map[string]any, len(source.BindVars)+len(plan.BindVars))
	for key, value := range source.BindVars {
		bindVars[key] = value
	}
	for key, value := range plan.BindVars {
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
	for index, stage := range sequence.Stages {
		stageRows := fmt.Sprintf("__loom_construction_stage_%d", index+1)
		lines = append(lines, fmt.Sprintf("LET %s = (", stageRows))
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
		case ir.PhysicalStageDeriveOp, ir.PhysicalStageFilterOp:
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
		projections = append(projections, ir.PhysicalProjection{
			Name: column.Name, Hidden: column.Internal,
			Value: ir.PhysicalValue{Variable: finalRow, Path: []string{column.Name}},
		})
	}
	returned, err := renderer.renderReturn(ir.PhysicalReturn{Projections: projections})
	if err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("render final stage output: %w", err)
	}
	lines = append(lines, "RETURN "+returned)
	query := strings.Join(lines, "\n") + "\n"
	return RenderedPhysicalPlan{Query: query, BindVars: pruneUnusedRuntimeBindVars(renderer.bindVars, query)}, nil
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
