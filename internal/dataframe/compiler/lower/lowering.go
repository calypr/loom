package lower

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	semanticpkg "github.com/calypr/loom/internal/dataframe/semantic"
)

// BuildPhysicalPlan is the compiler's direct semantic-to-physical boundary.
//
// It deliberately accepts SemanticPlan rather than the public request
// builder, keeping storage and AQL details out of semantic validation.
func BuildPhysicalPlan(semantic semanticpkg.SemanticPlan) (ir.PhysicalPlan, error) {
	return BuildPhysicalPlanWithPolicy(semantic, ir.DefaultPhysicalOptimizationPolicy())
}

// BuildPhysicalPlanWithPolicy is the policy-aware semantic-to-physical
// boundary used by ablation and parity harnesses. Production callers should
// use BuildPhysicalPlan unless they are deliberately comparing one optimizer
// rule.
func BuildPhysicalPlanWithPolicy(semantic semanticpkg.SemanticPlan, policy ir.PhysicalOptimizationPolicy) (ir.PhysicalPlan, error) {
	if err := semanticpkg.ValidateSemanticGraph(semantic); err != nil {
		return ir.PhysicalPlan{}, fmt.Errorf("validate semantic plan: %w", err)
	}
	if reason := genericPhysicalPlanUnavailableReason(semantic.Root); reason != "" {
		return ir.PhysicalPlan{}, fmt.Errorf("physical lowering does not yet support %s", reason)
	}
	return BuildGenericPhysicalPlanWithPolicy(semantic, policy)
}
