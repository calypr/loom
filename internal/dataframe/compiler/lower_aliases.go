package compiler

import "github.com/calypr/loom/internal/dataframe/compiler/lower"

type StorageRoute = lower.StorageRoute
type CompiledRecipe = lower.CompiledRecipe
type CompiledRecipeOutput = lower.CompiledRecipeOutput
type DynamicColumnMetadata = lower.DynamicColumnMetadata
type CompiledOutputColumn = lower.CompiledOutputColumn

var (
	BuildPhysicalPlan                  = lower.BuildPhysicalPlan
	BuildPhysicalPlanWithPolicy        = lower.BuildPhysicalPlanWithPolicy
	BuildGenericPhysicalPlan           = lower.BuildGenericPhysicalPlan
	BuildGenericPhysicalPlanWithPolicy = lower.BuildGenericPhysicalPlanWithPolicy
	ResolveStorageRoute                = lower.ResolveStorageRoute
	ErrUnsupportedStorageRoute         = lower.ErrUnsupportedStorageRoute
	CompileResolvedRecipePlan          = lower.CompileResolvedRecipePlan
	BuildGraphPhysicalPlan             = lower.BuildGraphPhysicalPlan
	CompileResolvedGraphPlan           = lower.CompileResolvedGraphPlan
)
