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
