package aql

import (
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

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
