package dataframe

import "fmt"

// BuildPhysicalPlan is the compiler's direct semantic-to-physical boundary.
//
// It deliberately accepts SemanticPlan rather than the public request
// builder, keeping storage and AQL details out of semantic validation.
func BuildPhysicalPlan(semantic SemanticPlan) (PhysicalPlan, error) {
	if err := ValidateSemanticGraph(semantic); err != nil {
		return PhysicalPlan{}, fmt.Errorf("validate semantic plan: %w", err)
	}
	if reason := genericPhysicalPlanUnavailableReason(semantic.Root); reason != "" {
		return PhysicalPlan{}, fmt.Errorf("physical lowering does not yet support %s", reason)
	}
	return BuildGenericPhysicalPlan(semantic)
}
