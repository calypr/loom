package compiler

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
)

const (
	DefaultTableShapeExclusionLimit = 25
	MaxTableShapeExclusionLimit     = 100
)

type TableShapeExclusionRefusalCode string

const (
	TableShapeExclusionUnsupported   TableShapeExclusionRefusalCode = "TABLE_SHAPE_EXCLUSION_UNSUPPORTED"
	TableShapeExclusionNoPolicy      TableShapeExclusionRefusalCode = "TABLE_SHAPE_EXCLUSION_NO_POLICY"
	TableShapeExclusionInvalidOffset TableShapeExclusionRefusalCode = "TABLE_SHAPE_EXCLUSION_OFFSET_INVALID"
	TableShapeExclusionInvalidLimit  TableShapeExclusionRefusalCode = "TABLE_SHAPE_EXCLUSION_LIMIT_INVALID"
)

type TableShapeExclusionRefusal struct {
	Code TableShapeExclusionRefusalCode
}

func (e *TableShapeExclusionRefusal) Error() string { return string(e.Code) }

// CompiledTableShapeExclusionQuery is an exact, bounded diagnostic query
// derived from one finalized output plan. It has no caller-supplied paths or
// query fragments.
type CompiledTableShapeExclusionQuery struct {
	Query                string
	BindVars             map[string]any
	ResourceTypeField    string
	ResourceIDField      string
	IdentityStatusField  string
	CategoryValueField   string
	CategoryPresentField string
	CategoryTypeField    string
	OutputRowIDField     string
	ReasonField          string
	OmissionField        string
	Offset               int
	Limit                int
	Diagnostics          ir.CompilerPlanDiagnostics
}

func CompileTableShapeExclusionsWithPolicy(output lower.CompiledRecipeOutput, offset, limit int, policy ir.PhysicalOptimizationPolicy) (CompiledTableShapeExclusionQuery, error) {
	if offset < 0 {
		return CompiledTableShapeExclusionQuery{}, &TableShapeExclusionRefusal{Code: TableShapeExclusionInvalidOffset}
	}
	if limit <= 0 {
		limit = DefaultTableShapeExclusionLimit
	}
	if limit > MaxTableShapeExclusionLimit {
		return CompiledTableShapeExclusionQuery{}, &TableShapeExclusionRefusal{Code: TableShapeExclusionInvalidLimit}
	}

	_ = policy // Diagnostics execute against the exact canonical plan, before publication optimizations.
	physical, err := withGenericPhysicalExecutionWindow(ir.ClonePhysicalPlan(output.Plan), 0)
	if err != nil {
		return CompiledTableShapeExclusionQuery{}, fmt.Errorf("prepare table-shape exclusion plan: %w", err)
	}
	pivotIndex := -1
	var pivot *ir.PhysicalGroupedPivot
	for index := range physical.Operations {
		operation := &physical.Operations[index]
		switch operation.Kind {
		case ir.PhysicalGroupedPivotOp:
			if pivot != nil || operation.GroupedPivot == nil {
				return CompiledTableShapeExclusionQuery{}, &TableShapeExclusionRefusal{Code: TableShapeExclusionUnsupported}
			}
			pivotIndex, pivot = index, operation.GroupedPivot
		case ir.PhysicalUnpivotOp:
			return CompiledTableShapeExclusionQuery{}, &TableShapeExclusionRefusal{Code: TableShapeExclusionUnsupported}
		}
	}
	if pivot == nil {
		return CompiledTableShapeExclusionQuery{}, &TableShapeExclusionRefusal{Code: TableShapeExclusionUnsupported}
	}
	if pivot.UnlistedCategoryPolicy != string(ir.PhysicalPivotUnlistedCategoryExcludeWithEvidence) {
		return CompiledTableShapeExclusionQuery{}, &TableShapeExclusionRefusal{Code: TableShapeExclusionNoPolicy}
	}

	const offsetBind, limitBind, fetchLimitBind = "table_shape_exclusion_offset", "table_shape_exclusion_limit", "table_shape_exclusion_fetch_limit"
	for _, bind := range []string{offsetBind, limitBind, fetchLimitBind} {
		if _, exists := physical.BindVars[bind]; exists {
			return CompiledTableShapeExclusionQuery{}, fmt.Errorf("table-shape exclusion bind %q is already defined", bind)
		}
	}
	physical.BindVars[offsetBind] = offset
	physical.BindVars[limitBind] = limit
	physical.BindVars[fetchLimitBind] = limit + 1
	terminal := ir.PhysicalTableShapeExclusionReturn{
		Pivot: *pivot, OffsetBindKey: offsetBind, LimitBindKey: limitBind, FetchLimitBindKey: fetchLimitBind,
	}
	physical.Operations = append(physical.Operations[:pivotIndex], ir.PhysicalOperation{
		Kind:                      ir.PhysicalTableShapeExclusionReturnOp,
		Source:                    physical.Operations[pivotIndex].Source,
		TableShapeExclusionReturn: &terminal,
	})
	if err := physical.Validate(); err != nil {
		return CompiledTableShapeExclusionQuery{}, fmt.Errorf("validate table-shape exclusion plan: %w", err)
	}
	if err := ir.ValidateGenericPhysicalPlanScope(physical); err != nil {
		return CompiledTableShapeExclusionQuery{}, fmt.Errorf("verify table-shape exclusion scope: %w", err)
	}
	rendered, err := aql.RenderPhysicalPlan(physical)
	if err != nil {
		return CompiledTableShapeExclusionQuery{}, fmt.Errorf("render table-shape exclusion plan: %w", err)
	}
	if strings.TrimSpace(output.Name) == "" {
		return CompiledTableShapeExclusionQuery{}, fmt.Errorf("table-shape exclusion output name is required")
	}
	return CompiledTableShapeExclusionQuery{
		Query: rendered.Query, BindVars: rendered.BindVars,
		ResourceTypeField:    ir.PhysicalTableShapeExclusionResourceTypeField,
		ResourceIDField:      ir.PhysicalTableShapeExclusionResourceIDField,
		IdentityStatusField:  ir.PhysicalTableShapeExclusionIdentityStatusField,
		CategoryValueField:   ir.PhysicalTableShapeExclusionCategoryValueField,
		CategoryPresentField: ir.PhysicalTableShapeExclusionCategoryPresentField,
		CategoryTypeField:    ir.PhysicalTableShapeExclusionCategoryTypeField,
		OutputRowIDField:     ir.PhysicalTableShapeExclusionOutputRowIDField,
		ReasonField:          ir.PhysicalTableShapeExclusionReasonField,
		OmissionField:        ir.PhysicalTableShapeExclusionOmissionField,
		Offset:               offset, Limit: limit, Diagnostics: physicalPlanDiagnostics(physical),
	}, nil
}
