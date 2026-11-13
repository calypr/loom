package aql

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func RenderClickHousePrefix(plan ir.PhysicalPlan) (RenderedPhysicalPlan, error) {
	if plan.Engine != ir.PhysicalEngineClickHouse || plan.ClickHousePrefix == nil || plan.ClickHouseCombine == nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("typed composite ClickHouse plan is required")
	}
	if plan.ClickHousePrefix.SourceBackend != ir.PhysicalPrefixSourceAQL {
		return RenderedPhysicalPlan{}, fmt.Errorf("AQL renderer cannot compile a ClickHouse prefix tagged source backend %q", string(plan.ClickHousePrefix.SourceBackend))
	}
	if err := plan.Validate(); err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("validate composite ClickHouse prefix: %w", err)
	}
	prefix := ir.ClonePhysicalPlan(plan)
	prefix.Engine = ir.PhysicalEngineAQL
	prefix.ClickHouseCombine = nil
	prefix.ClickHousePrefix = nil
	if prefix.StageSequence == nil || prefix.StageSequence.PreviewLimitBindKey != "" {
		return RenderedPhysicalPlan{}, fmt.Errorf("private ClickHouse prefix must be an unbounded typed AQL stage sequence")
	}
	return RenderPhysicalPlan(prefix)
}
