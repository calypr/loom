# Builder mutation probe proposals

Status: model-derived review queue. These are candidate actions and tests, not executable mutants.

## Summary

| Measure | Count |
| --- | ---: |
| actions | 94 |
| high | 46 |
| medium | 24 |
| low | 24 |
| withCandidateTests | 70 |
| withoutCandidateTests | 24 |
| guardCandidates | 0 |
| withGuardCandidates | 0 |

## Review queue

| Priority | Model action | Routes | Candidate tests | Candidate source |
| --- | --- | ---: | --- | --- |
| high | `BuilderOperations.AuthorStep` | 16 | `TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `BuilderOperations.RouteSavedStep` | 1 | `TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly` internal/server/openapi_explorer_construction_proposal_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `BuilderPreview.BeginApply` | 6 | `TestBuilderFullUIEmptyRootCreationDispatchUsesRealService` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `BuilderPreview.ReplaceProposal` | 2 | `TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `BuilderPreview.RetryProposal` | 2 | `TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `BuilderPreview.ScheduleDiscovery` | 1 | `TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly` internal/server/openapi_explorer_construction_proposal_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `BuilderPreview.StartPreview` | 6 | `TestNativeV2RouteUsesAuthorizedPersistedReceipt` internal/server/explorer_receipt_contract_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `BuilderPreview.StartProposal` | 6 | `TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `ConstructionLifecycle.BeginApply` | 6 | `TestBuilderFullUIEmptyRootCreationDispatchUsesRealService` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `ConstructionLifecycle.PreviewComplete` | 6 | `TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `ConstructionLifecycle.ProposalComplete` | 6 | `TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `ConstructionLifecycle.ProposalError` | 2 | `TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `ConstructionLifecycle.RetryProposal` | 2 | `TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `ConstructionLifecycle.StartProposal` | 6 | `TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `Interpretation.BeginCreate` | 1 | `TestGeneratedExplorerMutationRoutesForwardAuthResourcePath` internal/server/openapi_explorer_write_scope_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `Interpretation.CancelCreate` | 1 | `TestGeneratedExplorerMutationRoutesForwardAuthResourcePath` internal/server/openapi_explorer_write_scope_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `Interpretation.CreateBlocked` | 1 | `TestGeneratedExplorerMutationRoutesForwardAuthResourcePath` internal/server/openapi_explorer_write_scope_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `Interpretation.CreateFailed` | 1 | `TestGeneratedExplorerMutationRoutesForwardAuthResourcePath` internal/server/openapi_explorer_write_scope_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `Interpretation.CreateRevision` | 1 | `TestGeneratedExplorerMutationRoutesForwardAuthResourcePath` internal/server/openapi_explorer_write_scope_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `Interpretation.ReviewStale` | 1 | `TestInterpretationPreviewRouteRejectsMissingBody` internal/server/openapi_interpretation_routes_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `LifecycleEntry.AuthoredChange` | 1 | `TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt` internal/server/explorer_receipt_contract_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `ReconcileLifecycle.AuthoredChange` | 1 | `TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt` internal/server/explorer_receipt_contract_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `ReconcileLifecycle.ClearReceipt` | 1 | `TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt` internal/server/explorer_receipt_contract_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `ReconcileLifecycle.ReconcileConflict` | 1 | `TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt` internal/server/explorer_receipt_contract_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `ReconcileLifecycle.ReconcileFailure` | 0 | `TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt` internal/server/explorer_receipt_contract_test.go | none |
| high | `ReconcileLifecycle.RetryReconcile` | 1 | `TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt` internal/server/explorer_receipt_contract_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `RootChange.ApplyPreview` | 1 | `TestAssessAndApplyRowChangeThroughPublicAPI` internal/server/explorer_row_change_route_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `RootChange.AssessmentBlocked` | 1 | `TestAssessAndApplyRowChangeThroughPublicAPI` internal/server/explorer_row_change_route_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `RootChange.AssessmentNoChange` | 1 | `TestAssessAndApplyRowChangeThroughPublicAPI` internal/server/explorer_row_change_route_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `RootChange.AssessmentReady` | 1 | `TestAssessAndApplyRowChangeThroughPublicAPI` internal/server/explorer_row_change_route_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `RootChange.AssessmentStale` | 1 | `TestAssessAndApplyRowChangeThroughPublicAPI` internal/server/explorer_row_change_route_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `RootChange.BeginChange` | 1 | `TestAssessAndApplyRowChangeThroughPublicAPI` internal/server/explorer_row_change_route_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `RootChange.PreviewUnavailable` | 1 | `TestAssessAndApplyRowChangeThroughPublicAPI` internal/server/explorer_row_change_route_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `RowDefinitionLifecycle.ProposalComplete` | 6 | `TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `RowDefinitionLifecycle.ProposalFailed` | 2 | `TestBuilderFullUIRequestDecoderPreservesNullableBodyAndEndpoint` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `WorkspaceLifecycle.AddRoute` | 1 | `TestBuilderFullUIRequestDecoderPreservesNullableBodyAndEndpoint` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `WorkspaceLifecycle.CommandsApplied` | 1 | `TestBuilderFullUIRequestDecoderPreservesNullableBodyAndEndpoint` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `WorkspaceLifecycle.CreateExplorer` | 2 | `TestMigrateLegacyWorkspaceForCurrentAPIUsesScopedCatalogAndReusesPublishBody` internal/acceptance/scenario_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `WorkspaceLifecycle.DeleteCapability` | 2 | `TestMigrateLegacyWorkspaceForCurrentAPIUsesScopedCatalogAndReusesPublishBody` internal/acceptance/scenario_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `WorkspaceLifecycle.DeleteExplorer` | 2 | `TestMigrateLegacyWorkspaceForCurrentAPIUsesScopedCatalogAndReusesPublishBody` internal/acceptance/scenario_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `WorkspaceLifecycle.ExplorerListComplete` | 2 | `TestMigrateLegacyWorkspaceForCurrentAPIUsesScopedCatalogAndReusesPublishBody` internal/acceptance/scenario_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `WorkspaceLifecycle.HydrateBuilder` | 3 | `TestMigrateLegacyWorkspaceForCurrentAPIUsesScopedCatalogAndReusesPublishBody` internal/acceptance/scenario_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `WorkspaceLifecycle.PreviewComplete` | 6 | `TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `WorkspaceLifecycle.RemoveRoute` | 1 | `TestBuilderFullUIRequestDecoderPreservesNullableBodyAndEndpoint` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `WorkspaceLifecycle.SelectExplorer` | 3 | `TestMigrateLegacyWorkspaceForCurrentAPIUsesScopedCatalogAndReusesPublishBody` internal/acceptance/scenario_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `WorkspaceLifecycle.StartPreview` | 6 | `TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| low | `BuilderOperations.ResetWorkspace` | 0 | none | none |
| low | `BuilderOperations.UndoLastStep` | 0 | none | none |
| low | `BuilderPreview.AdoptDiscovery` | 0 | none | none |
| low | `BuilderPreview.ChangeDraft` | 0 | none | none |
| low | `BuilderPreview.ChangePivotKey` | 0 | none | none |
| low | `BuilderPreview.LatePreviewComplete` | 0 | none | none |
| low | `BuilderPreview.UnmountEditor` | 0 | none | none |
| low | `ConstructionLifecycle.LatePreviewSettlementIgnored` | 0 | none | none |
| low | `ConstructionLifecycle.PreviewError` | 0 | none | none |
| low | `DraftRestore.CancelRestore` | 0 | none | none |
| low | `DraftRestore.RestoreComplete` | 0 | none | none |
| low | `DraftRestore.SaveDraft` | 0 | none | none |
| low | `DraftRestore.StartRestore` | 0 | none | none |
| low | `LifecycleEntry.CommandStale` | 0 | none | none |
| ... | 34 more in the JSON report | | | |

## Guard candidates

No syntactic guard candidate matched an unprobed action.

## Bounds

The queue selects unprobed model actions and ranks existing tests by exact obligation, suspected action/model, then route overlap. Guard candidates are syntactic removals of the last `&&` or `||` operand in an `if` guard; a reviewer must confirm that the guard implements the action before adding it as an executable probe.
