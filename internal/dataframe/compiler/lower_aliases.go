package compiler

import "github.com/calypr/loom/internal/dataframe/compiler/lower"

type StorageRoute = lower.StorageRoute

var (
	BuildPhysicalPlan                  = lower.BuildPhysicalPlan
	BuildPhysicalPlanWithPolicy        = lower.BuildPhysicalPlanWithPolicy
	BuildGenericPhysicalPlan           = lower.BuildGenericPhysicalPlan
	BuildGenericPhysicalPlanWithPolicy = lower.BuildGenericPhysicalPlanWithPolicy
	ResolveStorageRoute                = lower.ResolveStorageRoute
	ErrUnsupportedStorageRoute         = lower.ErrUnsupportedStorageRoute
)
