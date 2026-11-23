package ir

import (
)

import "fmt"

const GenericPhysicalExecutionLimitBind = "limit"

func WithGenericPhysicalExecutionWindow(plan PhysicalPlan, limit int) (PhysicalPlan, error) {
	if plan.StageSequence != nil {
		out := ClonePhysicalPlan(plan)
		if limit > 0 {
			if _, exists := out.BindVars[GenericPhysicalExecutionLimitBind]; exists {
				return PhysicalPlan{}, fmt.Errorf("generic physical execution limit bind %q is already defined", GenericPhysicalExecutionLimitBind)
			}
			out.BindVars[GenericPhysicalExecutionLimitBind] = limit
			out.StageSequence.PreviewLimitBindKey = GenericPhysicalExecutionLimitBind
			if out.StageSequence.PreviewSourceWindowByRootID && !InsertConstructionPreviewRootIDWindow(&out) {

				out.StageSequence.PreviewSourceWindowByRootID = false
			}
			out.StageSequence.PreviewTerminalPivotWindow = PreviewTerminalPivotWindowEligible(out.StageSequence)
		}
		if err := ValidateGenericPhysicalPlanScope(out); err != nil {
			return PhysicalPlan{}, fmt.Errorf("validate construction physical execution scope: %w", err)
		}
		if err := out.Validate(); err != nil {
			return PhysicalPlan{}, fmt.Errorf("validate construction physical execution window: %w", err)
		}
		return out, nil
	}
	if len(plan.Operations) == 1 && plan.Operations[0].Kind == PhysicalGroupRowsOp && plan.Operations[0].GroupRows != nil {
		out := ClonePhysicalPlan(plan)
		if limit > 0 {
			if _, exists := out.BindVars[GenericPhysicalExecutionLimitBind]; exists {
				return PhysicalPlan{}, fmt.Errorf("generic physical execution limit bind %q is already defined", GenericPhysicalExecutionLimitBind)
			}
			out.BindVars[GenericPhysicalExecutionLimitBind] = limit
			out.Operations[0].GroupRows.LimitBindKey = GenericPhysicalExecutionLimitBind
		}
		if err := out.Validate(); err != nil {
			return PhysicalPlan{}, fmt.Errorf("validate grouped physical execution window: %w", err)
		}
		return out, nil
	}
	if err := ValidateGenericPhysicalPlanScope(plan); err != nil {
		return PhysicalPlan{}, fmt.Errorf("validate generic physical execution scope: %w", err)
	}
	if len(plan.Operations) == 0 || plan.Operations[0].Kind != PhysicalRootScanOp || plan.Operations[0].RootScan == nil {
		return PhysicalPlan{}, fmt.Errorf("generic physical execution plan requires a root scan")
	}
	if reshapeIndex, outputVariable := physicalTableReshapeOutput(plan.Operations); reshapeIndex >= 0 {
		out := ClonePhysicalPlan(plan)
		if limit > 0 {
			if _, exists := out.BindVars[GenericPhysicalExecutionLimitBind]; exists {
				return PhysicalPlan{}, fmt.Errorf("generic physical execution limit bind %q is already defined", GenericPhysicalExecutionLimitBind)
			}
			out.BindVars[GenericPhysicalExecutionLimitBind] = limit
		}
		window := []PhysicalOperation{{
			Kind: PhysicalSortOp, Source: PhysicalSource{SemanticField: "table_shape.row_id"},
			Sort: &PhysicalSort{Keys: []PhysicalValue{{Variable: outputVariable, Path: []string{"__loom_row_id"}}}},
		}}
		if limit > 0 {
			window = append(window, PhysicalOperation{
				Kind: PhysicalLimitOp, Source: PhysicalSource{SemanticField: "table_shape.row_id"},
				Limit: &PhysicalLimit{BindKey: GenericPhysicalExecutionLimitBind},
			})
		}
		insertAt := reshapeIndex + 1
		operations := make([]PhysicalOperation, 0, len(out.Operations)+len(window))
		operations = append(operations, out.Operations[:insertAt]...)
		operations = append(operations, window...)
		operations = append(operations, out.Operations[insertAt:]...)
		out.Operations = operations
		if err := ValidateGenericPhysicalPlanScope(out); err != nil {
			return PhysicalPlan{}, fmt.Errorf("validate reshaped physical execution window: %w", err)
		}
		return out, nil
	}

	insertAt := physicalScopeWindowEnd(plan.Operations, 1)
	unnestIndex := -1
	for index, operation := range plan.Operations {
		if operation.Kind == PhysicalUnnestOp {
			if unnestIndex >= 0 {
				return PhysicalPlan{}, fmt.Errorf("generic execution window supports one row expansion")
			}
			unnestIndex = index
		}
	}
	if unnestIndex >= 0 {

		if unnestIndex > insertAt {
			return PhysicalPlan{}, fmt.Errorf("generic execution window requires row expansion before child SET/traversal operations")
		}
		insertAt = unnestIndex + 1
	} else {

		for insertAt > 1 && insertAt <= len(plan.Operations) && plan.Operations[insertAt-1].Kind == PhysicalExpressionLetOp {
			insertAt--
		}
	}
	if insertAt <= 1 || insertAt >= len(plan.Operations) {
		return PhysicalPlan{}, fmt.Errorf("generic physical execution plan requires a scoped root followed by RETURN or traversal")
	}

	out := ClonePhysicalPlan(plan)
	root := out.Operations[0].RootScan.Variable
	rootKey := PhysicalValue{Variable: root, Path: []string{"_key"}}
	keys := []PhysicalValue{rootKey}
	unnestCount := 0
	for _, operation := range out.Operations {
		if operation.Kind != PhysicalUnnestOp || operation.Unnest == nil {
			continue
		}
		unnestCount++
		if unnestCount > 1 {
			return PhysicalPlan{}, fmt.Errorf("generic execution window supports one row expansion")
		}
		keys = PhysicalUnnestSortKeys(*operation.Unnest)
	}
	window := []PhysicalOperation{{
		Kind:   PhysicalSortOp,
		Source: PhysicalSource{SemanticNode: out.Source.SemanticNode, ResourceType: out.Source.ResourceType, SemanticField: "_key"},
		Sort:   &PhysicalSort{Keys: keys},
	}}
	if limit > 0 {
		if _, exists := out.BindVars[GenericPhysicalExecutionLimitBind]; exists {
			return PhysicalPlan{}, fmt.Errorf("generic physical execution limit bind %q is already defined", GenericPhysicalExecutionLimitBind)
		}
		out.BindVars[GenericPhysicalExecutionLimitBind] = limit
		window = append(window, PhysicalOperation{
			Kind:   PhysicalLimitOp,
			Source: PhysicalSource{SemanticNode: out.Source.SemanticNode, ResourceType: out.Source.ResourceType},
			Limit:  &PhysicalLimit{BindKey: GenericPhysicalExecutionLimitBind},
		})
	}
	operations := make([]PhysicalOperation, 0, len(out.Operations)+len(window))
	operations = append(operations, out.Operations[:insertAt]...)
	operations = append(operations, window...)
	operations = append(operations, out.Operations[insertAt:]...)
	out.Operations = operations
	if err := ValidateGenericPhysicalPlanScope(out); err != nil {
		return PhysicalPlan{}, fmt.Errorf("validate generic physical execution window: %w", err)
	}
	return out, nil
}

func PreviewTerminalPivotWindowEligible(sequence *PhysicalStageSequence) bool {
	if sequence == nil || sequence.PreviewSourceWindowByRootID || len(sequence.Stages) == 0 {
		return false
	}
	stage := sequence.Stages[len(sequence.Stages)-1]
	return stage.ID == sequence.FinalStageID &&
		stage.Kind == PhysicalStagePivotOp &&
		stage.GroupedPivot != nil &&
		!stage.GroupedPivot.OneInputRowPerGroup &&
		len(stage.GroupedPivot.GroupKeys) > 0 &&
		stage.OutputRowVariable == stage.GroupedPivot.OutputRowVariable &&
		stage.RowIdentityColumn == sequence.FinalRowIdentity
}

func InsertConstructionPreviewRootIDWindow(plan *PhysicalPlan) bool {
	if plan == nil || plan.StageSequence == nil || plan.StageSequence.PreviewLimitBindKey == "" ||
		len(plan.Operations) < 2 || plan.Operations[0].Kind != PhysicalRootScanOp || plan.Operations[0].RootScan == nil {
		return false
	}
	root := plan.Operations[0].RootScan.Variable
	insertAt := -1
	lastFilter := 0
	for index, operation := range plan.Operations {
		switch operation.Kind {
		case PhysicalRootScanOp:
			if index != 0 {
				return false
			}
		case PhysicalFilterOp:
			lastFilter = index
		case PhysicalDerivedLetOp, PhysicalExpressionLetOp, PhysicalSetOp:
		case PhysicalReturnOp:
			if insertAt >= 0 || index != len(plan.Operations)-1 {
				return false
			}
			insertAt = index
		default:
			return false
		}
	}
	if insertAt <= 1 {
		return false
	}
	insertAt = lastFilter + 1
	window := []PhysicalOperation{
		{
			Kind:   PhysicalSortOp,
			Source: PhysicalSource{SemanticNode: plan.Source.SemanticNode, ResourceType: plan.Source.ResourceType, SemanticField: "id"},

			Sort: &PhysicalSort{Keys: []PhysicalValue{{Variable: root, Path: []string{"id"}}}},
		},
		{
			Kind:   PhysicalLimitOp,
			Source: PhysicalSource{SemanticNode: plan.Source.SemanticNode, ResourceType: plan.Source.ResourceType},
			Limit:  &PhysicalLimit{BindKey: plan.StageSequence.PreviewLimitBindKey},
		},
	}
	operations := make([]PhysicalOperation, 0, len(plan.Operations)+len(window))
	operations = append(operations, plan.Operations[:insertAt]...)
	operations = append(operations, window...)
	operations = append(operations, plan.Operations[insertAt:]...)
	plan.Operations = operations
	return true
}

func physicalTableReshapeOutput(operations []PhysicalOperation) (int, string) {
	for index, operation := range operations {
		switch operation.Kind {
		case PhysicalGroupedPivotOp:
			if operation.GroupedPivot != nil {
				return index, operation.GroupedPivot.OutputRowVariable
			}
		case PhysicalUnpivotOp:
			if operation.Unpivot != nil {
				return index, operation.Unpivot.OutputRowVariable
			}
		}
	}
	return -1, ""
}

func AppendUniqueProjection(projections []PhysicalProjection, candidate PhysicalProjection) []PhysicalProjection {
	for _, projection := range projections {
		if projection.Name == candidate.Name {
			return projections
		}
	}
	return append(projections, candidate)
}
