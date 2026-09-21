package compiler

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/spec"
)

const MaxCellTraceContributions = 100
const traceReshapeRowID = "__loom_row_id"

// CompiledCellTraceQuery is a specialized terminal over one canonical output
// plan. It retains the exact final value expression and stable row identity;
// only the contributor page is diagnostic-only.
type CompiledCellTraceQuery struct {
	Query                  string
	BindVars               map[string]any
	ValueColumn            string
	ContributionsColumn    string
	StatusColumn           string
	HasMoreColumn          string
	OmissionColumn         string
	IdentityPartsColumn    string
	ExplicitIdentityColumn string
	RowIdentity            *spec.RowIdentity
	ContributionOffset     int
	ContributionLimit      int
	Diagnostics            ir.CompilerPlanDiagnostics
}

func CompileCellTraceOutputWithPolicy(output lower.CompiledRecipeOutput, column string, offset, limit int, policy ir.PhysicalOptimizationPolicy) (CompiledCellTraceQuery, error) {
	column = strings.TrimSpace(column)
	if column == "" {
		return CompiledCellTraceQuery{}, fmt.Errorf("cell trace column is required")
	}
	if output.RowIdentity == nil {
		return CompiledCellTraceQuery{}, fmt.Errorf("cell trace requires a row identity")
	}
	if offset < 0 {
		return CompiledCellTraceQuery{}, fmt.Errorf("cell trace offset cannot be negative")
	}
	if limit <= 0 {
		limit = 25
	}
	if limit > MaxCellTraceContributions {
		return CompiledCellTraceQuery{}, fmt.Errorf("cell trace contribution limit exceeds %d", MaxCellTraceContributions)
	}
	physical := ir.ClonePhysicalPlan(output.Plan)
	_ = policy // The trace deliberately uses the canonical pre-optimization plan.
	physical, err := withGenericPhysicalExecutionWindow(physical, 0)
	if err != nil {
		return CompiledCellTraceQuery{}, fmt.Errorf("apply cell trace execution window: %w", err)
	}
	terminalIndex, identityParts, explicitIdentity, err := finalPopulationMappingIdentity(physical, output.RowIdentity)
	if err != nil {
		return CompiledCellTraceQuery{}, err
	}
	projection, err := traceProjection(physical.Operations[terminalIndex].Return, column)
	if err != nil {
		return CompiledCellTraceQuery{}, err
	}
	value, err := physicalProjectionExpression(projection)
	if err != nil {
		return CompiledCellTraceQuery{}, fmt.Errorf("trace column %q: %w", column, err)
	}
	contribution := traceContributionForProjection(physical, value)
	const offsetBind, limitBind, fetchLimitBind = "cell_trace_offset", "cell_trace_limit", "cell_trace_fetch_limit"
	physical.BindVars[offsetBind] = offset
	physical.BindVars[limitBind] = limit
	physical.BindVars[fetchLimitBind] = limit + 1
	physical.Operations[terminalIndex] = ir.PhysicalOperation{
		Kind: ir.PhysicalCellTraceReturnOp, Source: physical.Operations[terminalIndex].Source,
		CellTraceReturn: &ir.PhysicalCellTraceReturn{
			Value: value, Contribution: contribution, IdentityParts: identityParts, ExplicitIdentity: explicitIdentity,
			OffsetBindKey: offsetBind, LimitBindKey: limitBind, FetchLimitBindKey: fetchLimitBind,
			Reshape: traceReshapeLineage(physical, projection, value, rootVariable(physical)),
		},
	}
	if err := physical.Validate(); err != nil {
		return CompiledCellTraceQuery{}, fmt.Errorf("validate cell trace physical plan: %w", err)
	}
	if err := ir.ValidateGenericPhysicalPlanScope(physical); err != nil {
		return CompiledCellTraceQuery{}, fmt.Errorf("verify cell trace physical scope: %w", err)
	}
	rendered, err := aql.RenderPhysicalPlan(physical)
	if err != nil {
		return CompiledCellTraceQuery{}, fmt.Errorf("render cell trace physical plan: %w", err)
	}
	query := CompiledCellTraceQuery{
		Query: rendered.Query, BindVars: rendered.BindVars,
		ValueColumn: ir.PhysicalCellTraceValueField, ContributionsColumn: ir.PhysicalCellTraceContributionsField,
		StatusColumn: ir.PhysicalCellTraceStatusField, HasMoreColumn: ir.PhysicalCellTraceHasMoreField,
		OmissionColumn: ir.PhysicalCellTraceOmissionField, RowIdentity: output.RowIdentity.Clone(),
		ContributionOffset: offset, ContributionLimit: limit, Diagnostics: physicalPlanDiagnostics(physical),
	}
	if explicitIdentity != nil {
		query.ExplicitIdentityColumn = ir.PhysicalCellTraceExplicitIdentityField
	} else {
		query.IdentityPartsColumn = ir.PhysicalCellTraceIdentityPartsField
	}
	return query, nil
}

func rootVariable(plan ir.PhysicalPlan) string {
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalRootScanOp && operation.RootScan != nil {
			return operation.RootScan.Variable
		}
	}
	return ""
}

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

func traceContributionForProjection(plan ir.PhysicalPlan, value ir.PhysicalExpression) *ir.PhysicalCellTraceContribution {
	if value.Kind != ir.PhysicalValueExpression || value.Value == nil || value.Value.Variable == "" || len(value.Value.Path) != 1 {
		return nil
	}
	for _, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalSetOp || operation.Set == nil || operation.Set.Reduction == nil || operation.Set.Reduction.Variable != value.Value.Variable {
			continue
		}
		for _, field := range operation.Set.Reduction.Fields {
			if field.Name != value.Value.Path[0] {
				continue
			}
			return &ir.PhysicalCellTraceContribution{
				SetVariable: operation.Set.Reduction.SourceSetVariable,
				ValueField:  field.SourceField,
				Lossy:       field.Mode == ir.PhysicalSetReductionFirst,
			}
		}
	}
	return nil
}

func traceProjection(terminal *ir.PhysicalReturn, column string) (ir.PhysicalProjection, error) {
	if terminal == nil {
		return ir.PhysicalProjection{}, fmt.Errorf("cell trace output has no final RETURN")
	}
	for _, projection := range terminal.Projections {
		if projection.Name == column && !projection.Hidden {
			return projection, nil
		}
	}
	return ir.PhysicalProjection{}, fmt.Errorf("cell trace column %q is not a public output projection", column)
}
