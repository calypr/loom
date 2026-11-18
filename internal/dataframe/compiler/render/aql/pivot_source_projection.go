package aql

import "github.com/calypr/loom/internal/dataframe/compiler/ir"

func pruneUnusedSourcePivotProjections(plan *ir.PhysicalPlan, sequence *ir.PhysicalStageSequence, stages []ir.PhysicalConstructionStage) {
	if sequence.CellTraceReturn != nil || sequence.RowLineageReturn != nil || sequence.PopulationMappingReturn != nil || len(stages) == 0 {
		return
	}
	stage := &stages[0]
	if stage.Kind != ir.PhysicalStagePivotOp || stage.InputStageID != sequence.SourceStageID || stage.GroupedPivot == nil {
		return
	}
	pivot := *stage.GroupedPivot
	if pivot.CodedCorrelation != nil || pivot.CategoryPresence != nil || len(plan.Operations) == 0 {
		return
	}
	last := &plan.Operations[len(plan.Operations)-1]
	if last.Kind != ir.PhysicalReturnOp || last.Return == nil {
		return
	}
	required := map[string]bool{pivot.CategoryColumn: true, pivot.ValueColumn: true}
	for _, key := range pivot.GroupKeys {
		required[key.Column] = true
	}
	for _, value := range pivot.RowValues {
		required[value.InputColumn] = true
	}
	if pivot.RootContributorInputColumn != "" {
		required[pivot.RootContributorInputColumn] = true
	}
	if pivot.CategoryPresenceFromInput {
		required[pivot.CategoryPresenceColumn] = true
	}
	sourceNames := make(map[string]bool, len(last.Return.Projections))
	for _, projection := range last.Return.Projections {
		sourceNames[projection.Name] = true
	}
	inputs := make([]ir.PhysicalProjection, 0, len(required))
	found := make(map[string]bool, len(required))
	for _, projection := range pivot.InputProjections {
		if projection.Expression != nil || projection.Presence != nil || projection.PresenceOutput ||
			projection.Value.Variable != stage.InputRowVariable || projection.Value.BindKey != "" ||
			len(projection.Value.Path) != 1 || projection.Value.Path[0] != projection.Name || !sourceNames[projection.Name] {
			return
		}
		if projection.Hidden || required[projection.Name] {
			inputs = append(inputs, projection)
			found[projection.Name] = true
		}
	}
	for name := range required {
		if name == "" || !found[name] {
			return
		}
	}
	projections := make([]ir.PhysicalProjection, 0, len(inputs))
	for _, projection := range last.Return.Projections {
		if projection.Hidden || found[projection.Name] {
			projections = append(projections, projection)
		}
	}
	last.Return.Projections = projections
	pivot.InputProjections = inputs
	stage.GroupedPivot = &pivot
	pruneUnusedSourceGroupOperations(plan)
}
