package compiler

import "github.com/calypr/loom/internal/dataframe/compiler/lower"

type StorageRoute = lower.StorageRoute
type RecipePhysicalPlan = lower.RecipePhysicalPlan
type RecipePhysicalOutput = lower.RecipePhysicalOutput
type RecipePhysicalExpansion = lower.RecipePhysicalExpansion
type RecipePhysicalProjection = lower.RecipePhysicalProjection
type RecipePhysicalDynamicMap = lower.RecipePhysicalDynamicMap
type RecipePhysicalTraversal = lower.RecipePhysicalTraversal

var (
	BuildPhysicalPlan                  = lower.BuildPhysicalPlan
	BuildPhysicalPlanWithPolicy        = lower.BuildPhysicalPlanWithPolicy
	BuildGenericPhysicalPlan           = lower.BuildGenericPhysicalPlan
	BuildGenericPhysicalPlanWithPolicy = lower.BuildGenericPhysicalPlanWithPolicy
	ResolveStorageRoute                = lower.ResolveStorageRoute
	ErrUnsupportedStorageRoute         = lower.ErrUnsupportedStorageRoute
	LowerResolvedRecipePlan            = lower.LowerResolvedRecipePlan
)
