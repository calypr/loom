package celltrace

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/population"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/spec"
)

const MaxCellTraceContributions = 100
const traceReshapeRowID = "__loom_row_id"

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

	ExplicitIdentityObject bool
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
	_ = policy
	physical, err := ir.WithGenericPhysicalExecutionWindow(physical, 0)
	if err != nil {
		return CompiledCellTraceQuery{}, fmt.Errorf("apply cell trace execution window: %w", err)
	}
	traceIdentity := output.RowIdentity
	if physical.StageSequence != nil {
		if flattened, ok := SourceStageReshapeForTrace(physical); ok {
			fields, identityErr := constructionCellTraceIdentityFields(output, *physical.StageSequence)
			if identityErr != nil {
				return CompiledCellTraceQuery{}, identityErr
			}
			traceIdentity = output.RowIdentity.Clone()
			traceIdentity.Fields = fields
			physical = flattened
		} else {
			return compileConstructionCellTrace(output, &physical, column, offset, limit)
		}
	}
	if len(physical.Operations) == 1 && physical.Operations[0].Kind == ir.PhysicalGroupRowsOp && physical.Operations[0].GroupRows != nil {
		return compileGroupRowsCellTrace(output, &physical, column, offset, limit)
	}
	terminalIndex, identityParts, explicitIdentity, err := population.FinalPopulationMappingIdentity(physical, traceIdentity)
	if err != nil {
		return CompiledCellTraceQuery{}, err
	}
	projection, err := traceProjection(physical.Operations[terminalIndex].Return, column)
	if err != nil {
		return CompiledCellTraceQuery{}, err
	}
	value, err := population.PhysicalProjectionExpression(projection)
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
	rendered, err := aql.Render(physical, aql.Options{})
	if err != nil {
		return CompiledCellTraceQuery{}, fmt.Errorf("render cell trace physical plan: %w", err)
	}
	query := CompiledCellTraceQuery{
		Query: rendered.Query, BindVars: rendered.BindVars,
		ValueColumn: ir.PhysicalCellTraceValueField, ContributionsColumn: ir.PhysicalCellTraceContributionsField,
		StatusColumn: ir.PhysicalCellTraceStatusField, HasMoreColumn: ir.PhysicalCellTraceHasMoreField,
		OmissionColumn: ir.PhysicalCellTraceOmissionField, RowIdentity: output.RowIdentity.Clone(),
		ContributionOffset: offset, ContributionLimit: limit, Diagnostics: ir.PhysicalPlanDiagnostics(physical),
	}
	if explicitIdentity != nil {
		query.ExplicitIdentityColumn = ir.PhysicalCellTraceExplicitIdentityField
	} else {
		query.IdentityPartsColumn = ir.PhysicalCellTraceIdentityPartsField
	}
	return query, nil
}

func stageColumnByID(columns []ir.PhysicalStageColumn, id string) (ir.PhysicalStageColumn, bool) {
	for _, column := range columns {
		if column.ID == id {
			return column, true
		}
	}
	return ir.PhysicalStageColumn{}, false
}

func constructionStageDescriptor(stages []lower.CompiledStageDescriptor, id string) (lower.CompiledStageDescriptor, bool) {
	for _, stage := range stages {
		if stage.ID == id {
			return stage, true
		}
	}
	return lower.CompiledStageDescriptor{}, false
}

func compiledStageColumnByID(columns []lower.CompiledOutputColumn, id string) (lower.CompiledOutputColumn, bool) {
	for _, column := range columns {
		if column.ID == id {
			return column, true
		}
	}
	return lower.CompiledOutputColumn{}, false
}

func StageProjectionByName(projections []ir.PhysicalProjection, name string) (ir.PhysicalProjection, bool) {
	for _, projection := range projections {
		if projection.Name == name {
			return projection, true
		}
	}
	return ir.PhysicalProjection{}, false
}

func rootVariable(plan ir.PhysicalPlan) string {
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalRootScanOp && operation.RootScan != nil {
			return operation.RootScan.Variable
		}
	}
	return ""
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

func SourceStageReshapeForTrace(plan ir.PhysicalPlan) (ir.PhysicalPlan, bool) {
	sequence := plan.StageSequence
	if sequence == nil || len(sequence.Stages) == 0 || sequence.Stages[0].InputStageID != sequence.SourceStageID || plan.Validate() != nil {
		return plan, false
	}
	last := len(plan.Operations) - 1
	if last < 0 || plan.Operations[last].Kind != ir.PhysicalReturnOp || plan.Operations[last].Return == nil {
		return plan, false
	}
	stage := sequence.Stages[0]
	source := append([]ir.PhysicalProjection(nil), plan.Operations[last].Return.Projections...)
	var shape ir.PhysicalOperation
	switch stage.Kind {
	case ir.PhysicalStagePivotOp:
		if stage.GroupedPivot == nil || stage.GroupedPivot.CodedCorrelation != nil {
			return plan, false
		}
		pivot := *stage.GroupedPivot
		pivot.InputProjections = source
		for i := range pivot.InputProjections {
			projection := &pivot.InputProjections[i]
			if projection.Name != pivot.CategoryColumn {
				continue
			}
			if pivot.CategoryPresenceFromInput {
				projection.Presence = nil
			} else {
				pivot.CategoryPresence = projection.Presence
				if pivot.CategoryPresence != nil && pivot.CategoryPresenceColumn == "" {
					name := "__loom_evidence_category_present"
					for {
						found := false
						for _, p := range pivot.InputProjections {
							found = found || p.Name == name
						}
						if !found {
							break
						}
						name += "_x"
					}
					pivot.CategoryPresenceColumn = name
				}
			}
		}
		shape = ir.PhysicalOperation{Kind: ir.PhysicalGroupedPivotOp, GroupedPivot: &pivot}
	case ir.PhysicalStageUnpivotOp:
		if stage.Unpivot == nil || len(sequence.Stages) != 1 {
			return plan, false
		}
		unpivot := *stage.Unpivot
		unpivot.InputProjections = source
		shape = ir.PhysicalOperation{Kind: ir.PhysicalUnpivotOp, Unpivot: &unpivot}
	default:
		return plan, false
	}
	operations := append([]ir.PhysicalOperation(nil), plan.Operations[:last]...)
	operations = append(operations, shape)
	projections := stage.OutputProjections
	for _, next := range sequence.Stages[1:] {
		if next.Kind != ir.PhysicalStageDeriveOp {
			return plan, false
		}
		fields := make([]ir.PhysicalExpressionProjection, 0, len(projections))
		for _, projection := range projections {
			expression, err := population.PhysicalProjectionExpression(projection)
			if err != nil {
				return plan, false
			}
			fields = append(fields, ir.PhysicalExpressionProjection{Name: projection.Name, Expression: expression})
		}
		operations = append(operations, ir.PhysicalOperation{Kind: ir.PhysicalExpressionLetOp, ExpressionLet: &ir.PhysicalExpressionLet{Variable: next.InputRowVariable, Expression: ir.PhysicalExpression{Kind: ir.PhysicalObjectExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalPreserveNull, Object: &ir.PhysicalObject{Fields: fields}}}})
		operations = append(operations, next.DerivedLets...)
		projections = next.OutputProjections
	}
	plan.Operations = append(operations, ir.PhysicalOperation{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: projections}})
	plan.StageSequence = nil
	if plan.Validate() != nil {
		return plan, false
	}
	return plan, true
}
