package scope

import "github.com/calypr/loom/internal/dataframe/compiler/ir"

type WholeRelationScopeIdentity = ir.WholeRelationScopeIdentity
type WholeRelationScopeEvidence = ir.WholeRelationScopeEvidence

var (
	NewWholeRelationScopeEvidence    = ir.NewWholeRelationScopeEvidence
	ValidateGenericPhysicalPlanScope = ir.ValidateGenericPhysicalPlanScope
)
