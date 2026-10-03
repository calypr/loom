package aql

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// RenderClickHousePrefix exposes the typed AQL prefix of a composite physical
// plan as an unbounded stream query. The plan must already have passed the
// composite validator, which binds the AQL sequence's final stage to the
// terminal Combine input and rejects unsupported scope propagation.
func RenderClickHousePrefix(plan ir.PhysicalPlan) (RenderedPhysicalPlan, error) {
	if plan.Engine != ir.PhysicalEngineClickHouse || plan.ClickHousePrefix == nil || plan.ClickHouseCombine == nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("typed composite ClickHouse plan is required")
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
