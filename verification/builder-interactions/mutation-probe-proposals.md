# Builder mutation probe proposals

Status: model-derived review queue. These are candidate actions and tests, not executable mutants.

## Summary

| Measure | Count |
| --- | ---: |
| actions | 144 |
| high | 76 |
| medium | 29 |
| low | 39 |
| withCandidateTests | 105 |
| withoutCandidateTests | 39 |
| guardCandidates | 39 |
| withGuardCandidates | 28 |

## Review queue

| Priority | Model action | Routes | Candidate tests | Candidate source |
| --- | --- | ---: | --- | --- |
| high | `BuilderOperations.AuthorStep` | 16 | `TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `BuilderOperations.RouteSavedStep` | 1 | `TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly` internal/server/openapi_explorer_construction_proposal_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `BuilderPreview.BeginApply` | 6 | `TestBuilderFullUIEmptyRootCreationDispatchUsesRealService` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `BuilderPreview.CancelProposal` | 4 | `TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `BuilderPreview.ProposalError` | 2 | `TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `BuilderPreview.ReplaceProposal` | 2 | `TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `BuilderPreview.RetryProposal` | 2 | `TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `BuilderPreview.ScheduleDiscovery` | 1 | `TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly` internal/server/openapi_explorer_construction_proposal_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `BuilderPreview.StartPreview` | 7 | `TestNativeV2RouteUsesAuthorizedPersistedReceipt` internal/server/explorer_receipt_contract_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `BuilderPreview.StartProposal` | 6 | `TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `ConstructionLifecycle.BeginApply` | 6 | `TestBuilderFullUIEmptyRootCreationDispatchUsesRealService` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `ConstructionLifecycle.CancelProposal` | 4 | `TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `ConstructionLifecycle.PreviewComplete` | 7 | `TestMigrateLegacyWorkspaceForCurrentAPIUsesScopedCatalogAndReusesPublishBody` internal/acceptance/scenario_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `ConstructionLifecycle.ProposalComplete` | 6 | `TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `ConstructionLifecycle.ProposalError` | 2 | `TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `ConstructionLifecycle.RetryProposal` | 2 | `TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `ConstructionLifecycle.StartProposal` | 6 | `TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `Interpretation.BeginCreate` | 1 | `TestGeneratedExplorerMutationRoutesForwardAuthResourcePath` internal/server/openapi_explorer_write_scope_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `Interpretation.CancelCreate` | 1 | `TestGeneratedExplorerMutationRoutesForwardAuthResourcePath` internal/server/openapi_explorer_write_scope_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `Interpretation.CreateBlocked` | 1 | `TestGeneratedExplorerMutationRoutesForwardAuthResourcePath` internal/server/openapi_explorer_write_scope_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `Interpretation.CreateFailed` | 1 | `TestGeneratedExplorerMutationRoutesForwardAuthResourcePath` internal/server/openapi_explorer_write_scope_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `Interpretation.CreateRevision` | 1 | `TestGeneratedExplorerMutationRoutesForwardAuthResourcePath` internal/server/openapi_explorer_write_scope_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `Interpretation.ReviewCandidate` | 1 | `TestInterpretationPreviewRouteRejectsMissingBody` internal/server/openapi_interpretation_routes_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `Interpretation.ReviewReady` | 1 | `TestInterpretationPreviewRouteRejectsMissingBody` internal/server/openapi_interpretation_routes_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `Interpretation.ReviewStale` | 1 | `TestInterpretationPreviewRouteRejectsMissingBody` internal/server/openapi_interpretation_routes_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `LifecycleEntry.AuthoredChange` | 2 | `TestMigrateLegacyWorkspaceForCurrentAPIUsesScopedCatalogAndReusesPublishBody` internal/acceptance/scenario_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `LifecycleEntry.CommandStale` | 1 | `TestMigrateLegacyWorkspaceForCurrentAPIUsesScopedCatalogAndReusesPublishBody` internal/acceptance/scenario_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `LifecycleEntry.CommandSuccess` | 1 | `TestMigrateLegacyWorkspaceForCurrentAPIUsesScopedCatalogAndReusesPublishBody` internal/acceptance/scenario_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `LifecycleEntry.GateReconcile` | 1 | `TestMigrateLegacyWorkspaceForCurrentAPIUsesScopedCatalogAndReusesPublishBody` internal/acceptance/scenario_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `LifecycleEntry.ReconcileNeedsRepair` | 1 | `TestMigrateLegacyWorkspaceForCurrentAPIUsesScopedCatalogAndReusesPublishBody` internal/acceptance/scenario_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `LifecycleEntry.ReconcileReceiptBound` | 1 | `TestMigrateLegacyWorkspaceForCurrentAPIUsesScopedCatalogAndReusesPublishBody` internal/acceptance/scenario_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `LifecycleEntry.RetryEntryReconcile` | 1 | `TestMigrateLegacyWorkspaceForCurrentAPIUsesScopedCatalogAndReusesPublishBody` internal/acceptance/scenario_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `ReconcileLifecycle.AuthoredChange` | 2 | `TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt` internal/server/explorer_receipt_contract_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `ReconcileLifecycle.ClearReceipt` | 1 | `TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt` internal/server/explorer_receipt_contract_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `ReconcileLifecycle.ReconcileConflict` | 2 | `TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt` internal/server/explorer_receipt_contract_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `ReconcileLifecycle.ReconcileFailure` | 1 | `TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt` internal/server/explorer_receipt_contract_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `ReconcileLifecycle.ReconcileInvalid` | 2 | `TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt` internal/server/explorer_receipt_contract_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `ReconcileLifecycle.ReconcileStale` | 2 | `TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt` internal/server/explorer_receipt_contract_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `ReconcileLifecycle.ReconcileSuccess` | 1 | `TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt` internal/server/explorer_receipt_contract_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `ReconcileLifecycle.RequestReconcile` | 2 | `TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt` internal/server/explorer_receipt_contract_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `ReconcileLifecycle.RetryReconcile` | 1 | `TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt` internal/server/explorer_receipt_contract_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `RootChange.ApplyPreview` | 1 | `TestAssessAndApplyRowChangeThroughPublicAPI` internal/server/explorer_row_change_route_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `RootChange.AssessmentBlocked` | 1 | `TestAssessAndApplyRowChangeThroughPublicAPI` internal/server/explorer_row_change_route_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `RootChange.AssessmentNoChange` | 1 | `TestAssessAndApplyRowChangeThroughPublicAPI` internal/server/explorer_row_change_route_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `RootChange.AssessmentReady` | 1 | `TestAssessAndApplyRowChangeThroughPublicAPI` internal/server/explorer_row_change_route_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `RootChange.AssessmentStale` | 1 | `TestAssessAndApplyRowChangeThroughPublicAPI` internal/server/explorer_row_change_route_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `RootChange.BeginChange` | 1 | `TestAssessAndApplyRowChangeThroughPublicAPI` internal/server/explorer_row_change_route_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `RootChange.Cancel` | 1 | `TestAssessAndApplyRowChangeThroughPublicAPI` internal/server/explorer_row_change_route_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `RootChange.PreviewReady` | 1 | `TestAssessAndApplyRowChangeThroughPublicAPI` internal/server/explorer_row_change_route_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `RootChange.PreviewUnavailable` | 1 | `TestAssessAndApplyRowChangeThroughPublicAPI` internal/server/explorer_row_change_route_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `RowDefinitionLifecycle.ApplyProposal` | 2 | `TestBuilderFullUIRequestDecoderPreservesNullableBodyAndEndpoint` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `RowDefinitionLifecycle.CancelProposal` | 4 | `TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `RowDefinitionLifecycle.ProposalComplete` | 6 | `TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `RowDefinitionLifecycle.ProposalFailed` | 2 | `TestBuilderFullUIRequestDecoderPreservesNullableBodyAndEndpoint` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `RowDefinitionLifecycle.RequestProposal` | 2 | `TestBuilderFullUIRequestDecoderPreservesNullableBodyAndEndpoint` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `RowDefinitionLifecycle.SelectRow` | 2 | `TestBuilderFullUIRequestDecoderPreservesNullableBodyAndEndpoint` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `WorkspaceLifecycle.AbortPreview` | 1 | `TestMigrateLegacyWorkspaceForCurrentAPIUsesScopedCatalogAndReusesPublishBody` internal/acceptance/scenario_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `WorkspaceLifecycle.AcceptPreview` | 1 | `TestMigrateLegacyWorkspaceForCurrentAPIUsesScopedCatalogAndReusesPublishBody` internal/acceptance/scenario_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `WorkspaceLifecycle.AddRoute` | 1 | `TestBuilderFullUIRequestDecoderPreservesNullableBodyAndEndpoint` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| high | `WorkspaceLifecycle.CommandsApplied` | 1 | `TestBuilderFullUIRequestDecoderPreservesNullableBodyAndEndpoint` internal/explorer/lifecycle/builder_full_ui_replay_test.go | ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx |
| ... | 84 more in the JSON report | | | |

## Guard candidates

| Model action | File | Line | Mutation |
| --- | --- | ---: | --- |
| `BuilderPreview.CancelProposal` | `ui/packages/loom-ui/src/features/ExplorerBuilder/builderContractAdapters/core.ts` | 436 | `if (input.requestStep === null && values.localAction === 'cancel_construction_proposal') {` -> `if (input.requestStep === null) {` |
| `ConstructionLifecycle.CancelProposal` | `ui/packages/loom-ui/src/features/ExplorerBuilder/builderContractAdapters/core.ts` | 436 | `if (input.requestStep === null && values.localAction === 'cancel_construction_proposal') {` -> `if (input.requestStep === null) {` |
| `Interpretation.ReviewCandidate` | `ui/packages/loom-ui/src/features/ExplorerBuilder/components/RowDefinitionSettingsPanel.tsx` | 660 | `if (preview.receiptId !== candidateReceiptId || preview.outputId !== table.outputId) {` -> `if (preview.receiptId !== candidateReceiptId) {` |
| `Interpretation.ReviewReady` | `ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx` | 1703 | `if (response.previewStatus !== 'READY' || !columnId || !rows) {` -> `if (response.previewStatus !== 'READY' || !columnId) {` |
| `RootChange.Cancel` | `ui/packages/loom-ui/src/features/ExplorerBuilder/builderContractAdapters/core.ts` | 436 | `if (input.requestStep === null && values.localAction === 'cancel_construction_proposal') {` -> `if (input.requestStep === null) {` |
| `RootChange.PreviewReady` | `ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx` | 1703 | `if (response.previewStatus !== 'READY' || !columnId || !rows) {` -> `if (response.previewStatus !== 'READY' || !columnId) {` |
| `RowDefinitionLifecycle.ApplyProposal` | `ui/packages/loom-ui/src/features/ExplorerBuilder/builderContractAdapters/core.ts` | 269 | `if (action === 'proposalEndpoint' || action === 'applyEndpoint' || action === 'apply') {` -> `if (action === 'proposalEndpoint' || action === 'applyEndpoint') {` |
| `RowDefinitionLifecycle.CancelProposal` | `ui/packages/loom-ui/src/features/ExplorerBuilder/builderContractAdapters/core.ts` | 436 | `if (input.requestStep === null && values.localAction === 'cancel_construction_proposal') {` -> `if (input.requestStep === null) {` |
| `RowDefinitionLifecycle.RequestProposal` | `ui/packages/loom-ui/src/features/ExplorerBuilder/builderContractAdapters/core.ts` | 436 | `if (input.requestStep === null && values.localAction === 'cancel_construction_proposal') {` -> `if (input.requestStep === null) {` |
| `RowDefinitionLifecycle.RequestProposal` | `ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.ts` | 356 | `if (candidateIntent.current && proposalOwnerRef.current?.requestKey === requestKey) {` -> `if (candidateIntent.current) {` |
| `RowDefinitionLifecycle.SelectRow` | `ui/packages/loom-ui/src/features/ExplorerBuilder/builderContractAdapters/core.ts` | 549 | `if (action !== 'select_row_shape_or_policy' && action !== 'apply_row_definition') {` -> `if (action !== 'select_row_shape_or_policy') {` |
| `RowDefinitionLifecycle.SelectRow` | `ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx` | 1562 | `if (!support?.supported || !selectedStage.rowIdentityColumn) {` -> `if (!support?.supported) {` |
| `RowDefinitionLifecycle.SelectRow` | `ui/packages/loom-ui/src/features/ExplorerBuilder/components/RowDefinitionSettingsPanel.tsx` | 540 | `if (rows.kind === 'EXPANDED' && selection.kind === 'EXPANDED') {` -> `if (rows.kind === 'EXPANDED') {` |
| `WorkspaceLifecycle.ExplorerListError` | `ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx` | 3132 | `if (explorers.error || !builder.data) {` -> `if (explorers.error) {` |
| `WorkspaceLifecycle.RefreshCatalog` | `ui/packages/loom-ui/src/features/ExplorerBuilder/builderContractAdapters/core.ts` | 690 | `if (action === 'retry_preview_once' || action === 'refresh_preview_catalog') {` -> `if (action === 'retry_preview_once') {` |
| `WorkspaceLifecycle.SelectOccurrence` | `ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx` | 2172 | `if (selection.kind === 'compiler-route' || selection.kind === 'compiler-occurrence') {` -> `if (selection.kind === 'compiler-route') {` |
| `WorkspaceLifecycle.SelectTable` | `ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx` | 850 | `if (selectsTableThroughCommand && next.selectedOutputId) {` -> `if (selectsTableThroughCommand) {` |
| `BuilderPreview.AcceptCurrentDraft` | `ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx` | 663 | `if (builder.data && serverDraftKey.current !== builderDataKey) {` -> `if (builder.data) {` |
| `BuilderPreview.AcceptCurrentDraft` | `ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx` | 1674 | `if (!current.catalog.snapshotToken || !current.draftVersion || !current.draftDigest || !table) {` -> `if (!current.catalog.snapshotToken || !current.draftVersion || !current.draftDigest) {` |
| `BuilderPreview.AcceptCurrentDraft` | `ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx` | 1729 | `if (!current.catalog.snapshotToken || !current.draftVersion || !current.draftDigest || !table) {` -> `if (!current.catalog.snapshotToken || !current.draftVersion || !current.draftDigest) {` |
| `BuilderPreview.LateProposalComplete` | `ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/SourceCollectionExpandEditor.tsx` | 183 | `if (!latest || contextIdentity(latest) !== identity || !isCurrentProposal(proposal, latest)) {` -> `if (!latest || contextIdentity(latest) !== identity) {` |
| `BuilderPreview.SetPivotPair` | `ui/packages/loom-ui/src/features/ExplorerBuilder/components/TableShapeSettingsPanel.tsx` | 168 | `if (discovery.catalogId !== capabilities.catalogId || !pivotPairMatches(discovery, pair)) {` -> `if (discovery.catalogId !== capabilities.catalogId) {` |
| `BuilderPreview.SetPivotPair` | `ui/packages/loom-ui/src/features/ExplorerBuilder/components/TableShapeSettingsPanel.tsx` | 222 | `if (discovery.catalogId !== session.catalogId || !pivotPairMatches(discovery, pair)) {` -> `if (discovery.catalogId !== session.catalogId) {` |
| `ColumnPresentation.CreateRelatedAggregate` | `ui/packages/loom-ui/src/features/ExplorerBuilder/builderContractAdapters/peripheral.tsx` | 225 | `if (action === 'aggregate_operation' || action === 'related_field_reduction') {` -> `if (action === 'aggregate_operation') {` |
| `ColumnPresentation.SetRowValuePolicy` | `ui/packages/loom-ui/src/features/ExplorerBuilder/builderContractAdapters/core.ts` | 549 | `if (action !== 'select_row_shape_or_policy' && action !== 'apply_row_definition') {` -> `if (action !== 'select_row_shape_or_policy') {` |
| `ColumnPresentation.SetRowValuePolicy` | `ui/packages/loom-ui/src/features/ExplorerBuilder/builderContractAdapters/shapeCombine.ts` | 720 | `if (rowPolicy !== null && rowPolicy !== undefined) {` -> `if (rowPolicy !== null) {` |
| `ColumnPresentation.SetRowValuePolicy` | `ui/packages/loom-ui/src/features/ExplorerBuilder/components/DatasetReviewPanel.tsx` | 322 | `if (namesRowSetting && scopedTables.length === 1) {` -> `if (namesRowSetting) {` |
| `ConstructionLifecycle.ChangeDraftIdentity` | `ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx` | 2257 | `if (!currentIdentity || !sameRootChangeRequestIdentity(pending.identity, currentIdentity)) {` -> `if (!currentIdentity) {` |
| `ConstructionLifecycle.LateProposalSettlementIgnored` | `ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/SourceCollectionExpandEditor.tsx` | 183 | `if (!latest || contextIdentity(latest) !== identity || !isCurrentProposal(proposal, latest)) {` -> `if (!latest || contextIdentity(latest) !== identity) {` |
| `DraftRestore.StaleDraft` | `ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx` | 2140 | `if (apiError && (isDraftDesynchronized(apiError.code) || isStaleSnapshot(apiError.code))) {` -> `if (apiError) {` |
| ... | | | 9 more in the JSON report |

## Bounds

The queue selects unprobed model actions and ranks existing tests by exact obligation, suspected action/model, then route overlap. Guard candidates are syntactic removals of the last `&&` or `||` operand in an `if` guard; a reviewer must confirm that the guard implements the action before adding it as an executable probe.
