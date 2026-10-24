# Builder mutation probe proposals

Status: model-derived review queue. These are candidate actions and tests, not executable mutants.

## Summary

| Measure | Count |
| --- | ---: |
| actions | 118 |
| high | 44 |
| medium | 24 |
| low | 50 |
| withCandidateTests | 68 |
| withoutCandidateTests | 50 |
| guardCandidates | 12 |
| withGuardCandidates | 6 |

## Review queue

| Priority | Model action | Routes | Candidate tests | Candidate source |
| --- | --- | ---: | --- | --- |
| high | `BuilderJourney.PreviewComplete` | 6 | `TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
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
| high | `ConstructionLifecycle.StartPreview` | 6 | `TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `ConstructionLifecycle.StartProposal` | 6 | `TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `Interpretation.BeginCreate` | 1 | `TestGeneratedExplorerMutationRoutesForwardAuthResourcePath` internal/server/openapi_explorer_write_scope_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `Interpretation.CancelCreate` | 1 | `TestGeneratedExplorerMutationRoutesForwardAuthResourcePath` internal/server/openapi_explorer_write_scope_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `Interpretation.CreateBlocked` | 1 | `TestGeneratedExplorerMutationRoutesForwardAuthResourcePath` internal/server/openapi_explorer_write_scope_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `Interpretation.CreateFailed` | 1 | `TestGeneratedExplorerMutationRoutesForwardAuthResourcePath` internal/server/openapi_explorer_write_scope_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `Interpretation.CreateRevision` | 1 | `TestGeneratedExplorerMutationRoutesForwardAuthResourcePath` internal/server/openapi_explorer_write_scope_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `Interpretation.ReviewStale` | 1 | `TestInterpretationPreviewRouteRejectsMissingBody` internal/server/openapi_interpretation_routes_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `ReconcileLifecycle.LateReconcileIgnored` | 1 | `TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt` internal/server/explorer_receipt_contract_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `ReconcileLifecycle.WorkspaceChangeStartsReconcile` | 1 | `TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt` internal/server/explorer_receipt_contract_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
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
| low | `BuilderJourney.AddColumn` | 0 | none | none |
| low | `BuilderJourney.CancelFilter` | 0 | none | none |
| low | `BuilderJourney.ChoicePreview` | 0 | none | none |
| low | `BuilderJourney.ChoicePreviewComplete` | 0 | none | none |
| low | `BuilderJourney.ChoicePreviewError` | 0 | none | none |
| low | `BuilderJourney.EditGroup` | 0 | none | none |
| low | `BuilderJourney.EditGroupAgain` | 0 | none | none |
| low | `BuilderJourney.Group` | 0 | none | none |
| low | `BuilderJourney.GroupAgain` | 0 | none | none |
| low | `BuilderJourney.GroupEditCancel` | 0 | none | none |
| low | `BuilderJourney.GroupRemovalCancel` | 0 | none | none |
| low | `BuilderJourney.LatePreviewCompleteIgnored` | 0 | none | none |
| low | `BuilderJourney.LatePreviewErrorIgnored` | 0 | none | none |
| low | `BuilderJourney.Pivot` | 0 | none | none |
| low | `BuilderJourney.Preview` | 0 | none | none |
| low | `BuilderJourney.PreviewError` | 0 | none | none |
| ... | 58 more in the JSON report | | | |

## Guard candidates

| Model action | File | Line | Mutation |
| --- | --- | ---: | --- |
| `BuilderJourney.ChoicePreviewComplete` | `ui/packages/loom-ui/src/features/ExplorerBuilder/components/ConceptCatalog.tsx` | 1564 | `if (!resolved.complete && !nextCursor && choicesForCurrentTable(finalGroup).length === 0) {` -> `if (!resolved.complete && !nextCursor) {` |
| `BuilderJourney.Group` | `ui/packages/loom-ui/src/features/ExplorerBuilder/builderContractAdapters/shapeCombine.ts` | 342 | `if (choiceKind === 'EXPLICIT_GROUP' && screen.queryByLabelText('Unmatched record policy')) {` -> `if (choiceKind === 'EXPLICIT_GROUP') {` |
| `BuilderJourney.Group` | `ui/packages/loom-ui/src/features/ExplorerBuilder/builderContractAdapters/shapeCombine.ts` | 373 | `if (!memberId || !label || !Array.isArray(groupIndexes) || groupIndexes.some((value) => typeof value !== 'number')) {` -> `if (!memberId || !label || !Array.isArray(groupIndexes)) {` |
| `BuilderJourney.Group` | `ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx` | 3304 | `if ((kind === 'expand' || kind === 'group') && table && constructionLifecycle.capabilities.status === 'ready') {` -> `if ((kind === 'expand' || kind === 'group') && table) {` |
| `BuilderJourney.Pivot` | `ui/packages/loom-ui/src/features/ExplorerBuilder/components/TableShapeSettingsPanel.tsx` | 168 | `if (discovery.catalogId !== capabilities.catalogId || !pivotPairMatches(discovery, pair)) {` -> `if (discovery.catalogId !== capabilities.catalogId) {` |
| `BuilderJourney.Pivot` | `ui/packages/loom-ui/src/features/ExplorerBuilder/components/TableShapeSettingsPanel.tsx` | 222 | `if (discovery.catalogId !== session.catalogId || !pivotPairMatches(discovery, pair)) {` -> `if (discovery.catalogId !== session.catalogId) {` |
| `BuilderJourney.Pivot` | `ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx` | 2288 | `if (form.kind === 'pivot' && next.kind === 'pivot' && previousRequestIdentity !== nextRequestIdentity) {` -> `if (form.kind === 'pivot' && next.kind === 'pivot') {` |
| `BuilderJourney.Preview` | `ui/packages/loom-ui/src/features/ExplorerBuilder/builderContractAdapters/core.ts` | 691 | `if (action === 'retry_preview_once' || action === 'refresh_preview_catalog') {` -> `if (action === 'retry_preview_once') {` |
| `BuilderJourney.Preview` | `ui/packages/loom-ui/src/features/ExplorerBuilder/builderContractAdapters/peripheral.tsx` | 470 | `if (stepKind === 'preview' || screenAction(values) === 'review') {` -> `if (stepKind === 'preview') {` |
| `BuilderJourney.Preview` | `ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx` | 1925 | `if (response.preview.rows === null || response.preview.outputId !== table.outputId) {` -> `if (response.preview.rows === null) {` |
| `BuilderJourney.PreviewRequestCancelled` | `ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx` | 2918 | `if (execution.previewRequest && execution.previewGeneration === previewGeneration.current) {` -> `if (execution.previewRequest) {` |
| `BuilderJourney.Reload` | `ui/packages/loom-ui/src/features/ExplorerBuilder/useAutomaticPreview.ts` | 49 | `if (!options?.reload && settledRef.current?.key === request.key) {` -> `if (!options?.reload) {` |

## Bounds

The queue selects unprobed model actions and ranks existing tests by exact obligation, suspected action/model, then route overlap. Guard candidates are syntactic removals of the last `&&` or `||` operand in an `if` guard; a reviewer must confirm that the guard implements the action before adding it as an executable probe.
