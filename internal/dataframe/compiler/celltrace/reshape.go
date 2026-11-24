package celltrace

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func traceReshapeLineage(plan ir.PhysicalPlan, projection ir.PhysicalProjection, value ir.PhysicalExpression, root string) *ir.PhysicalCellTraceReshape {
	var pivot *ir.PhysicalGroupedPivot
	var unpivot *ir.PhysicalUnpivot
	for index := range plan.Operations {
		operation := plan.Operations[index]
		if operation.Kind == ir.PhysicalGroupedPivotOp && operation.GroupedPivot != nil {
			pivot = operation.GroupedPivot
		}
		if operation.Kind == ir.PhysicalUnpivotOp && operation.Unpivot != nil {
			unpivot = operation.Unpivot
		}
	}
	if pivot != nil {
		if source, ok := pivotCellTraceSource(*pivot, projection.Name, root); ok {
			return &ir.PhysicalCellTraceReshape{OutputVariable: pivot.OutputRowVariable, Sources: []ir.PhysicalCellTraceReshapeSource{source}, OmissionCode: source.OmissionCode}
		}
		lets := make(map[string]ir.PhysicalExpression)
		for _, operation := range plan.Operations {
			if operation.Kind == ir.PhysicalExpressionLetOp && operation.ExpressionLet != nil {
				lets[operation.ExpressionLet.Variable] = operation.ExpressionLet.Expression
			}
		}
		sources := make([]ir.PhysicalCellTraceReshapeSource, 0, 2)
		visitedVariables := map[string]bool{}
		unsupported := false
		found := collectPivotTraceSources(value, pivot, lets, root, visitedVariables, &sources, &unsupported)
		if found {
			omission := ""
			if unsupported {
				omission = ir.PhysicalCellTraceDerivedOmission
			}
			if len(sources) == 0 && omission == "" {
				omission = ir.PhysicalCellTraceDerivedLiteralOnly
			}
			return &ir.PhysicalCellTraceReshape{OutputVariable: pivot.OutputRowVariable, Sources: sources, OmissionCode: omission}
		}
	}
	if unpivot != nil {
		if projection.Name == unpivot.KeyOutput {
			return &ir.PhysicalCellTraceReshape{OutputVariable: unpivot.OutputRowVariable, OmissionCode: ir.PhysicalCellTraceGeneratedKeyOmission}
		}
		if projection.Name == unpivot.ValueOutput {
			lineage := &ir.PhysicalCellTraceReshape{OutputVariable: unpivot.OutputRowVariable}
			for _, input := range unpivot.Inputs {
				source := ir.PhysicalCellTraceReshapeSource{
					Kind: ir.PhysicalCellTraceUnpivotValue, SourceColumn: input.Column,
					SourcePresenceField: tracePresenceField(unpivot.InputProjections, input.Column),
				}
				if !traceableRootScalarProjection(unpivot.InputProjections, input.Column, root) {
					source.OmissionCode = ir.PhysicalCellTraceSourceOmission
					lineage.OmissionCode = ir.PhysicalCellTraceSourceOmission
				}
				lineage.Sources = append(lineage.Sources, source)
			}
			return lineage
		}
		if projection.Name != unpivot.KeyOutput && projection.Name != traceReshapeRowID && !projection.Hidden && unpivotPreservedProjection(*unpivot, projection.Name) {
			source := ir.PhysicalCellTraceReshapeSource{
				Kind: ir.PhysicalCellTraceUnpivotField, SourceColumn: projection.Name,
				SourcePresenceField: tracePresenceField(unpivot.InputProjections, projection.Name),
			}
			if !traceableRootScalarProjection(unpivot.InputProjections, projection.Name, root) {
				source.OmissionCode = ir.PhysicalCellTraceSourceOmission
			}
			return &ir.PhysicalCellTraceReshape{OutputVariable: unpivot.OutputRowVariable, Sources: []ir.PhysicalCellTraceReshapeSource{source}, OmissionCode: source.OmissionCode}
		}
	}
	return nil
}

func unpivotPreservedProjection(unpivot ir.PhysicalUnpivot, column string) bool {
	selected := make(map[string]struct{}, len(unpivot.Inputs))
	for _, input := range unpivot.Inputs {
		selected[input.Column] = struct{}{}
	}
	for _, projection := range unpivot.InputProjections {
		if projection.Name != column {
			continue
		}
		_, isSelected := selected[column]
		return !isSelected && column != traceReshapeRowID
	}
	return false
}

func collectPivotTraceSources(expression ir.PhysicalExpression, pivot *ir.PhysicalGroupedPivot, lets map[string]ir.PhysicalExpression, root string, visited map[string]bool, sources *[]ir.PhysicalCellTraceReshapeSource, unsupported *bool) bool {
	switch expression.Kind {
	case ir.PhysicalLiteralExpression:
		return true
	case ir.PhysicalValueExpression:
		if expression.Value == nil {
			*unsupported = true
			return false
		}
		value := *expression.Value
		if value.Variable == pivot.OutputRowVariable && len(value.Path) == 1 {
			source, ok := pivotCellTraceSource(*pivot, value.Path[0], root)
			if !ok {
				*unsupported = true
				return false
			}
			for _, existing := range *sources {
				if existing.Kind == source.Kind && existing.SourceColumn == source.SourceColumn && categoryTraceOutput(existing) == categoryTraceOutput(source) {
					return true
				}
			}
			*sources = append(*sources, source)
			if source.OmissionCode != "" {
				*unsupported = true
			}
			return true
		}
		if value.Variable == pivot.OutputRowVariable {
			*unsupported = true
			return false
		}
		let, exists := lets[value.Variable]
		if exists && len(value.Path) == 1 && let.Kind == ir.PhysicalObjectExpression && let.Object != nil {
			key := value.Variable + "." + value.Path[0]
			if visited[key] {
				*unsupported = true
				return false
			}
			visited[key] = true
			for _, field := range let.Object.Fields {
				if field.Name == value.Path[0] {
					return collectPivotTraceSources(field.Expression, pivot, lets, root, visited, sources, unsupported)
				}
			}
			*unsupported = true
			return false
		}
		if !exists || len(value.Path) != 0 {
			*unsupported = true
			return false
		}
		if visited[value.Variable] {
			return true
		}
		visited[value.Variable] = true
		return collectPivotTraceSources(let, pivot, lets, root, visited, sources, unsupported)
	case ir.PhysicalCallExpression:
		if expression.Call == nil {
			*unsupported = true
			return false
		}
		found := false
		for _, argument := range expression.Call.Args {
			if collectPivotTraceSources(argument, pivot, lets, root, visited, sources, unsupported) {
				found = true
			}
		}
		return found
	default:
		*unsupported = true
		return false
	}
}

func categoryTraceOutput(source ir.PhysicalCellTraceReshapeSource) string {
	if source.Category != nil {
		return source.Category.Output
	}
	return ""
}

func pivotCellTraceSource(pivot ir.PhysicalGroupedPivot, output string, root string) (ir.PhysicalCellTraceReshapeSource, bool) {
	for _, key := range pivot.GroupKeys {
		if key.Column != output {
			continue
		}
		source := ir.PhysicalCellTraceReshapeSource{
			Kind: ir.PhysicalCellTracePivotGroupKey, GroupRowsVariable: pivot.GroupRowsVariable,
			InputRowVariable: pivot.InputRowVariable, OneInputRowPerGroup: pivot.OneInputRowPerGroup,
			SourceColumn: key.Column, SourcePresenceField: tracePresenceField(pivot.InputProjections, key.Column),
		}
		if !traceableRootScalarProjection(pivot.InputProjections, key.Column, root) {
			source.OmissionCode = ir.PhysicalCellTraceSourceOmission
		}
		return source, true
	}
	for _, category := range pivot.Categories {
		if category.Output != output {
			continue
		}
		source := ir.PhysicalCellTraceReshapeSource{
			Kind: ir.PhysicalCellTracePivotCell, GroupRowsVariable: pivot.GroupRowsVariable,
			InputRowVariable: pivot.InputRowVariable, OneInputRowPerGroup: pivot.OneInputRowPerGroup,
			SourceColumn: pivot.ValueColumn, SourcePresenceField: tracePresenceField(pivot.InputProjections, pivot.ValueColumn),
			CategoryColumn: pivot.CategoryColumn, CategoryPresenceField: pivot.CategoryPresenceColumn,
			CategoryType: pivot.CategoryType, Category: &category, ValueColumn: pivot.ValueColumn, ValueColumnType: pivot.ValueType,
			DuplicatePolicy: pivot.DuplicatePolicy,
		}
		if !traceableRootScalarProjection(pivot.InputProjections, pivot.ValueColumn, root) {
			source.OmissionCode = ir.PhysicalCellTraceSourceOmission
		}
		return source, true
	}
	return ir.PhysicalCellTraceReshapeSource{}, false
}

func tracePresenceField(projections []ir.PhysicalProjection, column string) string {
	for index, projection := range projections {
		if projection.Name == column {
			return fmt.Sprintf("%s%d", ir.PhysicalCellTraceSourcePresencePrefix, index)
		}
	}
	return ""
}

func traceableRootScalarProjection(projections []ir.PhysicalProjection, column, root string) bool {
	if root == "" {
		return false
	}
	for _, projection := range projections {
		if projection.Name != column || projection.Presence == nil {
			continue
		}
		presence := projection.Presence
		if presence.Source.Variable != root || len(presence.Source.Path) != 1 || presence.Source.Path[0] != "payload" || len(presence.Paths) == 0 {
			return false
		}
		if projection.Expression == nil || projection.Expression.Kind != ir.PhysicalExtractExpression || projection.Expression.Extract == nil {
			return false
		}
		extract := projection.Expression.Extract
		if extract.Prepared != nil || extract.Distinct || extract.Source.Variable != root || len(extract.Source.Path) != 1 || extract.Source.Path[0] != "payload" || extract.Selector.Filter != nil || len(extract.Fallbacks) != 0 {
			return false
		}
		for _, step := range extract.Selector.Steps {
			if step.Field == "" || step.Iterate || step.Index != nil {
				return false
			}
		}
		return len(extract.Selector.Steps) > 0 && projection.Expression.Cardinality == ir.PhysicalScalarCardinality
	}
	return false
}
