# Test obligations

Status: generated from the TLA models and existing test metadata.

## Script quality inventory

678 tracked MJS files are indexed in the JSON report under `scripts`; no new MJS runner is required.

| Role | Files |
| --- | ---: |
| browser-spec | 96 |
| browser-workflow | 147 |
| config | 4 |
| helper-or-oracle | 150 |
| test | 219 |
| tool-or-module | 62 |

### Explicit formal links

- `scripts/run-builder-journey.mjs`: journey-runner-consumes-manual-traces → BuilderJourney
- `scripts/verify-builder-formal.mjs`: configured-model-runner → BuilderOperations, BuilderPreview, WorkspaceLifecycle, RowDefinitionLifecycle, DraftRestore, RootChange, ColumnPresentation, Population, Interpretation, SupportingSurfaces, LifecycleEntry, ReconcileLifecycle, BuilderJourney
- `scripts/verify-builder-interactions.mjs`: configured-model-runner → ConstructionLifecycle
- `scripts/verify-builder-traces.mjs`: trace-validator-not-producer → BuilderPreview, ConstructionLifecycle, ReconcileLifecycle
- `scripts/verify-ui/journey-specs/add-column-preview.spec.mjs`: declared-journey-model-manual-observations → BuilderJourney
- `scripts/verify-ui/journey-specs/builder-journey.spec.mjs`: declared-journey-model-manual-observations → BuilderJourney
- `scripts/verify-ui/journey-specs/edit-remove-reload.spec.mjs`: declared-journey-model-manual-observations → BuilderJourney
- `scripts/verify-ui/journey-specs/filter-preview.spec.mjs`: declared-journey-model-manual-observations → BuilderJourney
- `scripts/verify-ui/journey-specs/group-pivot-preview.spec.mjs`: declared-journey-model-manual-observations → BuilderJourney
- `scripts/verify-ui/journey-specs/load-preview.spec.mjs`: declared-journey-model-manual-observations → BuilderJourney
- `scripts/verify-ui/journey-specs/pivot-error.spec.mjs`: declared-journey-model-manual-observations → BuilderJourney
- `scripts/verify-ui/journey-specs/unpivot-preview.spec.mjs`: declared-journey-model-manual-observations → BuilderJourney

These are source associations, not passing runs. Journey observations are manually assembled; their independent browser/value assertions remain necessary. Model actions and configured invariants are listed per link in JSON.

### Cleanup review order

1. Replace manually assembled journey observations with observed implementation state where a model applies; preserve independent data oracles.
2. Review classified-but-unreplayed late/rejected runtime events before expanding the model claims.
3. Consolidate duplicate test/route parsers and TLC acquisition only after proving equivalent behavior.
4. Investigate `scripts/verify-ui/helpers/playwright-verification.mjs` and `scripts/local-preview-index-explain.mjs`; static audit found no current importer/invocation, but historical and manual use remain unresolved. Neither is approved for deletion.

Tracked paths only. Import-syntax matches are candidates (comments and fixture strings may match); path mentions include documentation and historical records. Neither establishes executable reachability. Reference lists retain eight examples with a full count. Dynamic imports, glob discovery and manual CLI invocation remain unresolved. No row authorizes deletion. Formal links are declared/configured associations, not runtime proof or current run results.

## Summary

| Measure | Count |
| --- | ---: |
| models | 14 |
| actions | 168 |
| invariants | 89 |
| obligations | 257 |
| covered | 6 |
| partial | 89 |
| missing | 162 |
| missingRouteBound | 19 |
| missingCandidate | 70 |
| missingUnbound | 73 |
| tests | 905 |
| testsWithObligations | 10 |
| testsWithoutObligations | 895 |
| obligationsWithMultipleTests | 10 |
| duplicateTestNames | 0 |
| repeatedPlaywrightChecks | 231 |
| sharedProofs | 3 |
| candidateRouteBindings | 152 |

## Obligations with no exact test

- `BuilderJourney.AddColumn` (0 route(s))
- `BuilderJourney.AddColumnAgain` (0 route(s))
- `BuilderJourney.CancelFilter` (0 route(s))
- `BuilderJourney.EditGroup` (0 route(s))
- `BuilderJourney.Group` (0 route(s))
- `BuilderJourney.GroupAgain` (0 route(s))
- `BuilderJourney.LatePreviewCompleteIgnored` (0 route(s))
- `BuilderJourney.LatePreviewErrorIgnored` (0 route(s))
- `BuilderJourney.Pivot` (0 route(s))
- `BuilderJourney.Preview` (0 route(s))
- `BuilderJourney.PreviewComplete` (6 route(s))
- `BuilderJourney.PreviewError` (0 route(s))
- `BuilderJourney.RemoveGroup` (0 route(s))
- `BuilderJourney.Stutter` (0 route(s))
- `BuilderOperations.AuthorStep` (16 route(s))
- `BuilderOperations.ResetWorkspace` (0 route(s))
- `BuilderOperations.RouteSavedStep` (1 route(s))
- `BuilderOperations.UndoLastStep` (0 route(s))
- `BuilderPreview.AcceptCurrentDraft` (0 route(s))
- `BuilderPreview.AdoptDiscovery` (0 route(s))
- `BuilderPreview.CancelProposal` (4 route(s))
- `BuilderPreview.ChangeDraft` (0 route(s))
- `BuilderPreview.ChangePivotKey` (0 route(s))
- `BuilderPreview.LatePreviewComplete` (0 route(s))
- `BuilderPreview.LateProposalComplete` (0 route(s))
- `BuilderPreview.ProposalComplete` (6 route(s))
- `BuilderPreview.ProposalError` (2 route(s))
- `BuilderPreview.ReplaceProposal` (2 route(s))
- `BuilderPreview.RetryProposal` (2 route(s))
- `BuilderPreview.SetPivotPair` (0 route(s))
- `BuilderPreview.StartProposal` (6 route(s))
- `BuilderPreview.UnmountEditor` (0 route(s))
- `ColumnPresentation.CreateRelatedAggregate` (0 route(s))
- `ColumnPresentation.InspectColumn` (1 route(s))
- `ColumnPresentation.InspectRejected` (1 route(s))
- `ColumnPresentation.InspectSucceeded` (1 route(s))
- `ColumnPresentation.MoveColumn` (0 route(s))
- `ColumnPresentation.RemoveConfiguredColumn` (0 route(s))
- `ColumnPresentation.RenameColumn` (0 route(s))
- `ColumnPresentation.SearchColumns` (0 route(s))
- `ColumnPresentation.SetFeaturePolicy` (0 route(s))
- `ColumnPresentation.SetFilterOffered` (0 route(s))
- `ColumnPresentation.SetRowValuePolicy` (0 route(s))
- `ColumnPresentation.ToggleVisibility` (0 route(s))
- `ConstructionLifecycle.BeginApply` (6 route(s))
- `ConstructionLifecycle.CancelProposal` (4 route(s))
- `ConstructionLifecycle.ChangeDraftIdentity` (0 route(s))
- `ConstructionLifecycle.LatePreviewSettlementIgnored` (0 route(s))
- `ConstructionLifecycle.LateProposalSettlementIgnored` (0 route(s))
- `ConstructionLifecycle.PreviewComplete` (6 route(s))
- `ConstructionLifecycle.PreviewError` (0 route(s))
- `ConstructionLifecycle.ProposalComplete` (6 route(s))
- `ConstructionLifecycle.ProposalError` (2 route(s))
- `ConstructionLifecycle.RetryProposal` (2 route(s))
- `ConstructionLifecycle.StartPreview` (6 route(s))
- `ConstructionLifecycle.StartProposal` (6 route(s))
- `DraftRestore.CancelRestore` (0 route(s))
- `DraftRestore.RestoreComplete` (0 route(s))
- `DraftRestore.SaveDraft` (0 route(s))
- `DraftRestore.StaleDraft` (0 route(s))
- `DraftRestore.StartRestore` (0 route(s))
- `Interpretation.ApplyCandidate` (1 route(s))
- `Interpretation.BeginCreate` (1 route(s))
- `Interpretation.CancelCreate` (1 route(s))
- `Interpretation.CreateBlocked` (1 route(s))
- `Interpretation.CreateFailed` (1 route(s))
- `Interpretation.CreateRevision` (1 route(s))
- `Interpretation.ReviewCandidate` (1 route(s))
- `Interpretation.ReviewReady` (1 route(s))
- `Interpretation.ReviewStale` (1 route(s))
- `LifecycleEntry.AuthoredChange` (0 route(s))
- `LifecycleEntry.CommandStale` (0 route(s))
- `LifecycleEntry.CommandSuccess` (0 route(s))
- `LifecycleEntry.GateReconcile` (0 route(s))
- `LifecycleEntry.ParseRoute` (1 route(s))
- `LifecycleEntry.ReconcileNeedsRepair` (0 route(s))
- `LifecycleEntry.ReconcileReceiptBound` (0 route(s))
- `LifecycleEntry.ReplaceExplorer` (1 route(s))
- `LifecycleEntry.RetryEntryReconcile` (0 route(s))
- `LifecycleEntry.SelectionFailed` (1 route(s))
- 82 more in the JSON report.

## Obligations with exact tests

- `BuilderPreview.ApplyAck`: playwright `recompile`, go `TestAssessAndApplyRowChangeThroughPublicAPI`, go `TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt`, go `TestBuilderFullUIEmptyRootCreationDispatchUsesRealService`, go `TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly`, go `TestGeneratedExplorerMutationRoutesForwardAuthResourcePath`, vitest `replays ready replacement: ready A; start B; reject beginApply(A); ready B; apply only B`
- `BuilderPreview.BeginApply`: playwright `recompile`, go `TestAssessAndApplyRowChangeThroughPublicAPI`, go `TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt`, go `TestBuilderFullUIEmptyRootCreationDispatchUsesRealService`, go `TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly`, go `TestGeneratedExplorerMutationRoutesForwardAuthResourcePath`, vitest `replays ready replacement: ready A; start B; reject beginApply(A); ready B; apply only B`
- `BuilderPreview.DiscoveryComplete`: go `TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly`
- `BuilderPreview.PreviewComplete`: playwright `recompile`, go `TestAssessAndApplyRowChangeThroughPublicAPI`, go `TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly`, go `TestNativeV2RouteUsesAuthorizedPersistedReceipt`, go `TestOpenAPIDocumentsOperationSpecificFailureStatuses`, vitest `replays fallback preview schedule: ${schedule}`
- `BuilderPreview.ScheduleDiscovery`: go `TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly`
- `BuilderPreview.StartPreview`: playwright `recompile`, go `TestAssessAndApplyRowChangeThroughPublicAPI`, go `TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly`, go `TestNativeV2RouteUsesAuthorizedPersistedReceipt`, go `TestOpenAPIDocumentsOperationSpecificFailureStatuses`, vitest `replays fallback preview schedule: ${schedule}`

## Obligations with multiple tests

- `BuilderPreview.ApplyAck`: 5 tests (server:go:internal/server/explorer_row_change_route_test.go::TestAssessAndApplyRowChangeThroughPublicAPI, server:go:internal/server/explorer_receipt_contract_test.go::TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt, server:go:internal/explorer/lifecycle/builder_full_ui_replay_test.go::TestBuilderFullUIEmptyRootCreationDispatchUsesRealService, server:go:internal/server/openapi_explorer_construction_proposal_test.go::TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly, server:go:internal/server/openapi_explorer_write_scope_test.go::TestGeneratedExplorerMutationRoutesForwardAuthResourcePath)
- `BuilderPreview.BeginApply`: 5 tests (server:go:internal/server/explorer_row_change_route_test.go::TestAssessAndApplyRowChangeThroughPublicAPI, server:go:internal/server/explorer_receipt_contract_test.go::TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt, server:go:internal/explorer/lifecycle/builder_full_ui_replay_test.go::TestBuilderFullUIEmptyRootCreationDispatchUsesRealService, server:go:internal/server/openapi_explorer_construction_proposal_test.go::TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly, server:go:internal/server/openapi_explorer_write_scope_test.go::TestGeneratedExplorerMutationRoutesForwardAuthResourcePath)
- `BuilderPreview.PreviewComplete`: 4 tests (server:go:internal/server/explorer_row_change_route_test.go::TestAssessAndApplyRowChangeThroughPublicAPI, server:go:internal/server/openapi_explorer_construction_proposal_test.go::TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly, server:go:internal/server/explorer_receipt_contract_test.go::TestNativeV2RouteUsesAuthorizedPersistedReceipt, server:go:internal/server/openapi_route_inventory_test.go::TestOpenAPIDocumentsOperationSpecificFailureStatuses)
- `BuilderPreview.StartPreview`: 4 tests (server:go:internal/server/explorer_row_change_route_test.go::TestAssessAndApplyRowChangeThroughPublicAPI, server:go:internal/server/openapi_explorer_construction_proposal_test.go::TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly, server:go:internal/server/explorer_receipt_contract_test.go::TestNativeV2RouteUsesAuthorizedPersistedReceipt, server:go:internal/server/openapi_route_inventory_test.go::TestOpenAPIDocumentsOperationSpecificFailureStatuses)
- `BuilderPreview.Inv_ApplyAckTargetsInitiatingOwner`: 9 tests (unit:vitest:ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx::replays fallback preview schedule: ${schedule}, unit:vitest:ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx::replays ready replacement: ready A; start B; reject beginApply(A); ready B; apply only B, server:go:internal/server/explorer_row_change_route_test.go::TestAssessAndApplyRowChangeThroughPublicAPI, server:go:internal/server/explorer_receipt_contract_test.go::TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt, server:go:internal/explorer/lifecycle/builder_full_ui_replay_test.go::TestBuilderFullUIEmptyRootCreationDispatchUsesRealService, server:go:internal/server/openapi_explorer_construction_proposal_test.go::TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly, server:go:internal/server/openapi_explorer_write_scope_test.go::TestGeneratedExplorerMutationRoutesForwardAuthResourcePath, server:go:internal/server/explorer_receipt_contract_test.go::TestNativeV2RouteUsesAuthorizedPersistedReceipt, server:go:internal/server/openapi_route_inventory_test.go::TestOpenAPIDocumentsOperationSpecificFailureStatuses)
- `BuilderPreview.Inv_ApplyFailureTargetsInitiatingOwner`: 9 tests (unit:vitest:ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx::replays fallback preview schedule: ${schedule}, unit:vitest:ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx::replays ready replacement: ready A; start B; reject beginApply(A); ready B; apply only B, server:go:internal/server/explorer_row_change_route_test.go::TestAssessAndApplyRowChangeThroughPublicAPI, server:go:internal/server/explorer_receipt_contract_test.go::TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt, server:go:internal/explorer/lifecycle/builder_full_ui_replay_test.go::TestBuilderFullUIEmptyRootCreationDispatchUsesRealService, server:go:internal/server/openapi_explorer_construction_proposal_test.go::TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly, server:go:internal/server/openapi_explorer_write_scope_test.go::TestGeneratedExplorerMutationRoutesForwardAuthResourcePath, server:go:internal/server/explorer_receipt_contract_test.go::TestNativeV2RouteUsesAuthorizedPersistedReceipt, server:go:internal/server/openapi_route_inventory_test.go::TestOpenAPIDocumentsOperationSpecificFailureStatuses)
- `BuilderPreview.Inv_ApplyUsesReviewedPreview`: 9 tests (unit:vitest:ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx::replays fallback preview schedule: ${schedule}, unit:vitest:ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx::replays ready replacement: ready A; start B; reject beginApply(A); ready B; apply only B, server:go:internal/server/explorer_row_change_route_test.go::TestAssessAndApplyRowChangeThroughPublicAPI, server:go:internal/server/explorer_receipt_contract_test.go::TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt, server:go:internal/explorer/lifecycle/builder_full_ui_replay_test.go::TestBuilderFullUIEmptyRootCreationDispatchUsesRealService, server:go:internal/server/openapi_explorer_construction_proposal_test.go::TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly, server:go:internal/server/openapi_explorer_write_scope_test.go::TestGeneratedExplorerMutationRoutesForwardAuthResourcePath, server:go:internal/server/explorer_receipt_contract_test.go::TestNativeV2RouteUsesAuthorizedPersistedReceipt, server:go:internal/server/openapi_route_inventory_test.go::TestOpenAPIDocumentsOperationSpecificFailureStatuses)
- `BuilderPreview.Inv_DiscoveryOwnedByCurrentKey`: 9 tests (unit:vitest:ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx::replays fallback preview schedule: ${schedule}, unit:vitest:ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx::replays ready replacement: ready A; start B; reject beginApply(A); ready B; apply only B, server:go:internal/server/explorer_row_change_route_test.go::TestAssessAndApplyRowChangeThroughPublicAPI, server:go:internal/server/explorer_receipt_contract_test.go::TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt, server:go:internal/explorer/lifecycle/builder_full_ui_replay_test.go::TestBuilderFullUIEmptyRootCreationDispatchUsesRealService, server:go:internal/server/openapi_explorer_construction_proposal_test.go::TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly, server:go:internal/server/openapi_explorer_write_scope_test.go::TestGeneratedExplorerMutationRoutesForwardAuthResourcePath, server:go:internal/server/explorer_receipt_contract_test.go::TestNativeV2RouteUsesAuthorizedPersistedReceipt, server:go:internal/server/openapi_route_inventory_test.go::TestOpenAPIDocumentsOperationSpecificFailureStatuses)
- `BuilderPreview.Inv_DraftMonotonic`: 9 tests (unit:vitest:ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx::replays fallback preview schedule: ${schedule}, unit:vitest:ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx::replays ready replacement: ready A; start B; reject beginApply(A); ready B; apply only B, server:go:internal/server/explorer_row_change_route_test.go::TestAssessAndApplyRowChangeThroughPublicAPI, server:go:internal/server/explorer_receipt_contract_test.go::TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt, server:go:internal/explorer/lifecycle/builder_full_ui_replay_test.go::TestBuilderFullUIEmptyRootCreationDispatchUsesRealService, server:go:internal/server/openapi_explorer_construction_proposal_test.go::TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly, server:go:internal/server/openapi_explorer_write_scope_test.go::TestGeneratedExplorerMutationRoutesForwardAuthResourcePath, server:go:internal/server/explorer_receipt_contract_test.go::TestNativeV2RouteUsesAuthorizedPersistedReceipt, server:go:internal/server/openapi_route_inventory_test.go::TestOpenAPIDocumentsOperationSpecificFailureStatuses)
- `BuilderPreview.Inv_PreviewOwnedByCurrentProposal`: 9 tests (unit:vitest:ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx::replays fallback preview schedule: ${schedule}, unit:vitest:ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx::replays ready replacement: ready A; start B; reject beginApply(A); ready B; apply only B, server:go:internal/server/explorer_row_change_route_test.go::TestAssessAndApplyRowChangeThroughPublicAPI, server:go:internal/server/explorer_receipt_contract_test.go::TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt, server:go:internal/explorer/lifecycle/builder_full_ui_replay_test.go::TestBuilderFullUIEmptyRootCreationDispatchUsesRealService, server:go:internal/server/openapi_explorer_construction_proposal_test.go::TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly, server:go:internal/server/openapi_explorer_write_scope_test.go::TestGeneratedExplorerMutationRoutesForwardAuthResourcePath, server:go:internal/server/explorer_receipt_contract_test.go::TestNativeV2RouteUsesAuthorizedPersistedReceipt, server:go:internal/server/openapi_route_inventory_test.go::TestOpenAPIDocumentsOperationSpecificFailureStatuses)

## Duplicate test names

No duplicate test names found.

## Repeated Playwright checks

- `API build identity stayed unchanged during browser run`: 5 cases (playwright:builder-controls:recompile, playwright:standalone-misc:semantic-construction-choice-route-fallback, playwright:cda-five-hop-related-expansion:medication-positive-fixture, playwright:basic-related-field-after-unpivot:gender-all, playwright:basic-related-field-after-unpivot:gender-missing-all)
- `Preview renders both independent fixture Patients`: 3 cases (playwright:builder-controls:recompile, playwright:builder-controls:first-table, playwright:builder-authoring:authoring)
- `create blank Explorer action-to-render within budget`: 3 cases (playwright:builder-controls:recompile, playwright:builder-published-membership:membership, playwright:builder-combine-nullable:lifecycle)
- `create blank Explorer completed`: 2 cases (playwright:builder-controls:recompile, playwright:builder-published-membership:membership)
- `created a fresh Explorer distinct from the bootstrap`: 5 cases (playwright:builder-controls:recompile, playwright:builder-authoring:suggestions, playwright:builder-published-membership:membership, playwright:builder-combine-draft:append-derived-edit, playwright:builder-combine-nullable:lifecycle)
- `fixture browser observer reported no unexpected network or runtime errors`: 9 cases (playwright:builder-controls:recompile, playwright:builder-controls:explorer-deletion, playwright:builder-controls:explorer-deletion-unavailable, playwright:builder-authoring:authoring, playwright:basic-unpivot-group:unpivot-to-group, playwright:viewer-query:dataset-zip, playwright:viewer-query:runtime-scope-isolation, playwright:basic-related-field-after-unpivot:gender-all, playwright:basic-related-field-after-unpivot:gender-missing-all)
- `open Explorer creation action-to-render within budget`: 3 cases (playwright:builder-controls:recompile, playwright:builder-published-membership:membership, playwright:builder-combine-nullable:lifecycle)
- `open Explorer creation completed`: 2 cases (playwright:builder-controls:recompile, playwright:builder-published-membership:membership)
- `watched source stayed unchanged during browser run`: 5 cases (playwright:builder-controls:recompile, playwright:standalone-misc:semantic-construction-choice-route-fallback, playwright:cda-five-hop-related-expansion:medication-positive-fixture, playwright:basic-related-field-after-unpivot:gender-all, playwright:basic-related-field-after-unpivot:gender-missing-all)
- `open native Explorer creation action-to-render within budget`: 2 cases (playwright:builder-controls:explorer-deletion, playwright:builder-controls:explorer-deletion-unavailable)
- `create the disposable Explorer through the native control action-to-render within budget`: 2 cases (playwright:builder-controls:explorer-deletion, playwright:builder-controls:explorer-deletion-unavailable)
- `builder failure is exposed in an alert`: 2 cases (playwright:builder-load:list, playwright:builder-load:state)
- `builder failure exposes an actionable in-app retry`: 2 cases (playwright:builder-load:list, playwright:builder-load:state)
- `builder recovered through in-app Retry`: 2 cases (playwright:builder-load:list, playwright:builder-load:state)
- `builder in-app Retry action-to-render within budget`: 2 cases (playwright:builder-load:list, playwright:builder-load:state)
- `basic Patient fixture contains the two independently known records`: 2 cases (playwright:builder-authoring:group-entry, playwright:builder-authoring:legacy-v9-rows)
- `independent fixture contains one Patient, four Observations, and three DiagnosticReports`: 7 cases (playwright:builder-published-membership:membership, playwright:builder-combine-draft:join, playwright:builder-combine-draft:append, playwright:builder-combine-draft:append-derived-edit, playwright:builder-combine-draft:group-pivot, playwright:builder-combine-draft:group-pivot-append, playwright:builder-combine-draft:membership)
- `draft Combine uses the exact project, generation, Builder snapshot, and authorization scope`: 7 cases (playwright:builder-published-membership:membership, playwright:builder-combine-draft:join, playwright:builder-combine-draft:append, playwright:builder-combine-draft:append-derived-edit, playwright:builder-combine-draft:group-pivot, playwright:builder-combine-draft:group-pivot-append, playwright:builder-combine-draft:membership)
- `Observation root Preview contains exact independent fixture IDs`: 6 cases (playwright:builder-published-membership:membership, playwright:builder-combine-draft:join, playwright:builder-combine-draft:append-derived-edit, playwright:builder-combine-draft:group-pivot, playwright:builder-combine-draft:group-pivot-append, playwright:builder-combine-draft:membership)
- `first native source table establishes a versioned current draft under the exact scope`: 7 cases (playwright:builder-published-membership:membership, playwright:builder-combine-draft:join, playwright:builder-combine-draft:append, playwright:builder-combine-draft:append-derived-edit, playwright:builder-combine-draft:group-pivot, playwright:builder-combine-draft:group-pivot-append, playwright:builder-combine-draft:membership)
- `DiagnosticReport root Preview contains exact independent fixture IDs`: 6 cases (playwright:builder-published-membership:membership, playwright:builder-combine-draft:join, playwright:builder-combine-draft:append-derived-edit, playwright:builder-combine-draft:group-pivot, playwright:builder-combine-draft:group-pivot-append, playwright:builder-combine-draft:membership)
- `DiagnosticReport native source table establishes a versioned current draft under the exact scope`: 2 cases (playwright:builder-published-membership:membership, playwright:builder-combine-draft:membership)
- `native Combine uses CREATE_TABLE to make an empty rooted target without authoring a source`: 3 cases (playwright:builder-published-membership:membership, playwright:builder-combine-draft:append-derived-edit, playwright:builder-combine-draft:membership)
- `removing Combine and reloading restores its rooted empty target`: 7 cases (playwright:builder-published-membership:membership, playwright:builder-combine-draft:join, playwright:builder-combine-draft:append, playwright:builder-combine-draft:append-derived-edit, playwright:builder-combine-draft:group-pivot, playwright:builder-combine-draft:group-pivot-append, playwright:builder-combine-draft:membership)
- `create current-draft Observation source table action-to-render within budget`: 2 cases (playwright:builder-published-membership:membership, playwright:builder-combine-draft:membership)
- `create current-draft DiagnosticReport source table action-to-render within budget`: 2 cases (playwright:builder-published-membership:membership, playwright:builder-combine-draft:membership)
- `open native Combine and create a separate empty target action-to-render within budget`: 2 cases (playwright:builder-published-membership:membership, playwright:builder-combine-draft:membership)
- `open saved Combine editor after draft input schemas resolve action-to-render within budget`: 6 cases (playwright:builder-published-membership:membership, playwright:builder-combine-draft:join, playwright:builder-combine-draft:append, playwright:builder-combine-draft:group-pivot, playwright:builder-combine-draft:group-pivot-append, playwright:builder-combine-draft:membership)
- `propose Combine removal and render the exact rooted empty output action-to-render within budget`: 2 cases (playwright:builder-published-membership:membership, playwright:builder-combine-draft:membership)
- `apply Combine removal to restore the empty target action-to-render within budget`: 2 cases (playwright:builder-published-membership:membership, playwright:builder-combine-draft:membership)
- `reload removed Combine and select its exact rooted empty output action-to-render within budget`: 2 cases (playwright:builder-published-membership:membership, playwright:builder-combine-draft:membership)
- `No unexpected native HTTP or network errors occurred`: 4 cases (playwright:basic-unpivot-pivot-roundtrip:unpivot-to-pivot, playwright:standalone-reshape-related-field-after-unpivot-gender-all:related-field-after-unpivot-gender-all, playwright:basic-related-field-after-unpivot:gender-all, playwright:basic-related-field-after-unpivot:gender-missing-all)
- `no unexpected browser or authoring errors occur and source, API build, project, and generation remain stable`: 2 cases (playwright:root-quantity-pivot:full-population-lifecycle, playwright:root-quantity-pivot:related-text-only-full-population-lifecycle)
- `Observation native GROUP count rows match the direct fixture oracle`: 3 cases (playwright:builder-combine-draft:join, playwright:builder-combine-draft:append, playwright:builder-combine-draft:append-derived-edit)
- `DiagnosticReport native GROUP count rows match the direct fixture oracle`: 3 cases (playwright:builder-combine-draft:join, playwright:builder-combine-draft:append, playwright:builder-combine-draft:append-derived-edit)
- `automatic Combine proposal is bound to this exact draft CAS and UI proxy scope`: 6 cases (playwright:builder-combine-draft:join, playwright:builder-combine-draft:append, playwright:builder-combine-draft:append-derived-edit, playwright:builder-combine-draft:group-pivot, playwright:builder-combine-draft:group-pivot-append, playwright:builder-combine-draft:membership)
- `Canceling Combine removal preserves its saved step, full workspace, and draft CAS after reload`: 5 cases (playwright:builder-combine-draft:join, playwright:builder-combine-draft:append, playwright:builder-combine-draft:append-derived-edit, playwright:builder-combine-draft:group-pivot, playwright:builder-combine-draft:group-pivot-append)
- `Patient native GROUP count rows match the direct fixture oracle`: 2 cases (playwright:builder-combine-draft:append, playwright:builder-combine-draft:append-derived-edit)
- `Patient gender counts persists its native Group→DERIVE output without pinning a revision`: 2 cases (playwright:builder-combine-draft:append, playwright:builder-combine-draft:append-derived-edit)
- `Patient gender counts Group→DERIVE preview matches independent category/count arithmetic`: 2 cases (playwright:builder-combine-draft:append, playwright:builder-combine-draft:append-derived-edit)

## Tests with no exact obligation

- `playwright` `Duplicate, rename, select, delete, copy, and reload tables$` in `scripts/verify-ui/specs/builder-controls.spec.mjs`; suspected models: BuilderPreview, ConstructionLifecycle, WorkspaceLifecycle
- `playwright` `Add columns waits for a current-draft preview` in `scripts/verify-ui/specs/builder-controls.spec.mjs`; suspected models: BuilderPreview, ConstructionLifecycle, WorkspaceLifecycle
- `playwright` `Explorer deletion removes the disposable Explorer, preserves the exact fallback URL, and blocks invalid deletes$` in `scripts/verify-ui/specs/builder-explorer-deletion.spec.mjs`; suspected models: BuilderPreview, ConstructionLifecycle, WorkspaceLifecycle
- `playwright` `Explorer deletion stays hidden and preserves the exact Explorer list and URL across reload$` in `scripts/verify-ui/specs/builder-explorer-deletion-unavailable.spec.mjs`; suspected models: BuilderPreview, ConstructionLifecycle, WorkspaceLifecycle
- `playwright` `Builder load list recovery shows the list failure and recovers through the in-app Retry` in `scripts/verify-ui/specs/builder-load.spec.mjs`; suspected models: WorkspaceLifecycle
- `playwright` `Builder load state recovery shows the state failure and recovers through the in-app Retry` in `scripts/verify-ui/specs/builder-load.spec.mjs`; suspected models: WorkspaceLifecycle
- `playwright` `catalog-backed suggestions expose actionable Patient candidates$` in `scripts/verify-ui/specs/builder-authoring.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `Builder authoring previews, changes an existing table root, and reloads$` in `scripts/verify-ui/specs/builder-authoring.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `native Record type picker resolves Condition-to-Specimen alternatives through the Patient-subject path$` in `scripts/verify-ui/specs/condition-specimen-route-ambiguity.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `native table reorder preserves exact saved order and rows after reload$` in `scripts/verify-ui/specs/builder-authoring.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `cohort-recode` in `scripts/verify-ui/specs/builder-authoring.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `cohort-expand` in `scripts/verify-ui/specs/builder-authoring.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `repeated-empty` in `scripts/verify-ui/specs/builder-authoring.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `standard Group entry proposes an empty-key COUNT_ROWS preview$` in `scripts/verify-ui/specs/builder-authoring.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `selected Patient coverage preserves exact membership through detach, reattach, and reload$` in `scripts/verify-ui/specs/builder-authoring.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `Explorer switching shows distinct tables and restores the selected Explorer after reload$` in `scripts/verify-ui/specs/builder-authoring.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `Dataframe contract panel shows the exact Patient output and lineage$` in `scripts/verify-ui/specs/builder-authoring.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `legacy semantics v9 preserves the saved Patient digest through native row actions and reload$` in `scripts/verify-ui/specs/legacy-v9-workspace.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `raw Patient.id ONE refusal leaves the saved Group intact for same-editor ALL repair$` in `scripts/verify-ui/specs/builder-patient-one-disagreement.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `published MEMBERSHIP uses exact Basic source revisions through INCLUDE, EXCLUDE edit, removal, and reload$` in `scripts/verify-ui/specs/published-membership.spec.mjs`; suspected models: BuilderJourney, BuilderPreview, ConstructionLifecycle, WorkspaceLifecycle
- `playwright` `native cohort Group→Pivot publishes and reloads exact Viewer counts$` in `scripts/verify-ui/specs/cohort-group-pivot-publication.spec.mjs`
- `playwright` `native coded source column survives edit, reload, removal, and reload$` in `scripts/verify-ui/specs/builder-coded-source-column.spec.mjs`; suspected models: BuilderOperations, BuilderPreview, ConstructionLifecycle, RowDefinitionLifecycle, SupportingSurfaces, WorkspaceLifecycle
- `playwright` `Basic fixture expands nested coding with owner, multiplicity, Cancel, Apply, edit, removal and reload$` in `scripts/verify-ui/specs/basic-nested-coding-expand.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `Basic fixture preserves related source and active-record fields through Group lifecycle$` in `scripts/verify-ui/specs/related-expand-field-lifecycle.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `Basic native PRESENCE maps a nonempty zero-match Group to false with exact true comparator and full lifecycle$` in `scripts/verify-ui/specs/basic-named-cohort-presence.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `native component expansion and distinct Pivot survive preview, edit, removal, and reload$` in `scripts/verify-ui/specs/scope-001-repeated-expand-pivot.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `Basic fixture adds standalone coded fields with exact Coding version and display correlation$` in `scripts/verify-ui/specs/standalone-coded-fields.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `selects, previews, cancels, applies, edits, removes, and reloads exact coded rows$` in `scripts/verify-ui/specs/basic-coded-pivot.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, RowDefinitionLifecycle, SupportingSurfaces, WorkspaceLifecycle
- `playwright` `Basic fixture preserves exact row identities through native Pivot→Unpivot lifecycle$` in `scripts/verify-ui/specs/basic-pivot-unpivot.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `Basic fixture preserves exact Patient rows through native Unpivot→Pivot, edit, reload, and restoration$` in `scripts/verify-ui/specs/basic-unpivot-pivot.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `Basic fixture groups exact native Unpivot keys and counts$` in `scripts/verify-ui/specs/basic-unpivot-group.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `date filter keeps exact typed values and source rows through Cancel, Apply, edit, removal, and reload$` in `scripts/verify-ui/specs/basic-temporal-filter.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `date-time filter keeps exact typed values and source rows through Cancel, Apply, edit, removal, and reload$` in `scripts/verify-ui/specs/basic-temporal-filter.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `mapped-plus-orphan-to-empty` in `scripts/verify-ui/specs/population-member-removal.spec.mjs`; suspected models: BuilderJourney, BuilderPreview, ConstructionLifecycle, LifecycleEntry, Population, WorkspaceLifecycle
- `playwright` `removes one mapped contributor, preserves the lower nonempty GROUP, and restores the exact baseline$` in `scripts/verify-ui/specs/population-member-removal.spec.mjs`; suspected models: BuilderJourney, BuilderPreview, ConstructionLifecycle, LifecycleEntry, Population, WorkspaceLifecycle
- `playwright` `full-population-discovery` in `scripts/verify-ui/specs/root-quantity-pivot.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `fixture-lifecycle` in `scripts/verify-ui/specs/root-quantity-pivot.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `matches SUM and MAX output and restores the full CDA source after reload$` in `scripts/verify-ui/specs/root-quantity-pivot.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `related quantity Pivot groups by text and applies SUM/MAX across the full route$` in `scripts/verify-ui/specs/root-quantity-pivot.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `join` in `scripts/verify-ui/specs/draft-combine.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `append` in `scripts/verify-ui/specs/draft-combine.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `append-derived-edit` in `scripts/verify-ui/specs/draft-combine.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `group-pivot` in `scripts/verify-ui/specs/draft-combine.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `group-pivot-append` in `scripts/verify-ui/specs/draft-combine.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `membership` in `scripts/verify-ui/specs/draft-combine.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `join` in `scripts/verify-ui/specs/join.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `append` in `scripts/verify-ui/specs/append.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `lifecycle` in `scripts/verify-ui/specs/nullable-combine.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `@dev-journey:verify-j01$` in `scripts/verify-ui/specs/dev-journeys.spec.mjs`; suspected models: BuilderJourney, BuilderPreview, ConstructionLifecycle, WorkspaceLifecycle
- `playwright` `@dev-journey:verify-j05$` in `scripts/verify-ui/specs/dev-journeys.spec.mjs`; suspected models: BuilderJourney, BuilderPreview, ConstructionLifecycle, WorkspaceLifecycle
- `playwright` `output` in `scripts/verify-ui/specs/viewer-query.spec.mjs`
- `playwright` `Viewer shared filters apply to linked outputs, cancel cleanly, and reset on reload$` in `scripts/verify-ui/specs/viewer-shared-filter.spec.mjs`
- `playwright` `Viewer paginates exact fixture Patient identities and resets page state$` in `scripts/verify-ui/specs/viewer-pagination.spec.mjs`
- `playwright` `Viewer row details show the exact Patient ID through close, reopen, reload, and reopen$` in `scripts/verify-ui/specs/viewer-row-details.spec.mjs`
- `playwright` `Viewer explains the exact Patient Gender cell through source details, close, reopen, and reload$` in `scripts/verify-ui/specs/viewer-cell-explain.spec.mjs`
- `playwright` `Viewer charts expose the exact Gender category count and missing count before and after reload$` in `scripts/verify-ui/specs/viewer-charts.spec.mjs`
- `playwright` `configured-output-action-csv` in `scripts/verify-ui/specs/viewer-output-action.spec.mjs`
- `playwright` `Viewer downloads the exact source DocumentReference from a per-row FileCell and repeats after reload$` in `scripts/verify-ui/specs/viewer-row-file-download.spec.mjs`
- `playwright` `Viewer downloads the published Patient dataset as an exact identity-bound ZIP$` in `scripts/verify-ui/specs/viewer-output-dataset-zip.spec.mjs`
- `playwright` `Viewer dataset ZIP exports the complete authorized population while a facet is selected$` in `scripts/verify-ui/specs/viewer-output-filtered-dataset-zip.spec.mjs`
- `playwright` `Basic Viewer output excludes rows from another project and generation$` in `scripts/verify-ui/specs/basic-runtime-scope-isolation.spec.mjs`
- `playwright` `compound-coded-group-basic` in `scripts/verify-ui/specs/standalone-misc.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `Basic fixture groups literal Observation codes with exact counts through Preview, Cancel, Apply, edit, removal and reload$` in `scripts/verify-ui/specs/basic-coded-group-recorded-code.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `previews, applies, reloads, and removes the exact optional routed semantic value$` in `scripts/verify-ui/specs/semantic-construction-choice-route.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `Preview limits$` in `scripts/verify-ui/specs/standalone-builder.spec.mjs`; suspected models: BuilderJourney, BuilderPreview, ConstructionLifecycle, WorkspaceLifecycle
- `playwright` `create, edit, cancel, apply, and remove a typed CDA filter$` in `scripts/verify-ui/specs/standalone-cda-other.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `filter two-key Observation Group output by subject reference through Cancel, Apply, edit, removal, and reload$` in `scripts/verify-ui/specs/standalone-cda-other.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `Explicit row-root replacement previews a reset, cancels safely, applies, and survives reload$` in `scripts/verify-ui/specs/basic-root-replacement.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, LifecycleEntry, Population, RootChange, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `select Observation directly from a plain Patient table and restore Patient$` in `scripts/verify-ui/specs/standalone-cda-plain-patient-root-selector.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, LifecycleEntry, Population, RootChange, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `rebase Patient rows through Observation and restore the original route and values$` in `scripts/verify-ui/specs/standalone-cda-other.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, RootChange, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `remove an unmapped selected resource, verify the saved route, and reload$` in `scripts/verify-ui/specs/standalone-cda-other.spec.mjs`; suspected models: BuilderPreview, ConstructionLifecycle, LifecycleEntry, Population, WorkspaceLifecycle
- `playwright` `CDA starting collection handoff hand off a starting collection, attach its route, preview, and reload$` in `scripts/verify-ui/specs/standalone-cda-other.spec.mjs`; suspected models: BuilderJourney, BuilderPreview, ConstructionLifecycle, LifecycleEntry, Population, WorkspaceLifecycle
- `playwright` `restricted Calypr auth keeps related Builder rows within the granted path across Cancel and Apply$` in `scripts/verify-ui/specs/scope-001-restricted-auth.spec.mjs`; suspected models: BuilderJourney, BuilderPreview, ConstructionLifecycle, WorkspaceLifecycle
- `playwright` `replace a dependent starting collection across Patient and Observation, then restore its route lifecycle$` in `scripts/verify-ui/specs/standalone-cda-other.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, LifecycleEntry, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `replace the Patient setup with the exact default Observation page on the same table$` in `scripts/verify-ui/specs/cda-same-table-root-replacement.spec.mjs`; suspected models: BuilderJourney, BuilderPreview, ConstructionLifecycle, LifecycleEntry, Population, RootChange, WorkspaceLifecycle
- `playwright` `preserves missing-component owners through EXPANDED and GROUP removal Cancel$` in `scripts/verify-ui/specs/standalone-cda-rows.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `applies source expansion, verifies item values, and restores original Observation rows$` in `scripts/verify-ui/specs/standalone-cda-rows.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `CDA repeated component empty-list policies preserves empty and populated component owners through row policy and GROUP lifecycle$` in `scripts/verify-ui/specs/standalone-cda-rows.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `medication-preserve-parent` in `scripts/verify-ui/specs/standalone-cda-other.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, LifecycleEntry, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `preserve and exclude exact positive Medication matches through native edit and reload$` in `scripts/verify-ui/specs/related-medication-positive-fixture.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, LifecycleEntry, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- 815 more in the JSON report.

## Shared route proof evidence

- `uiTest:ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx`: post:api-v1-projects-project-explorers-explorerId-authoring-v2-commands, post:api-v1-projects-project-explorers-explorerId-authoring-v2-preview
- `goTest:TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly`: post:api-v1-projects-project-explorers-explorerId-authoring-v2-commands, post:api-v1-projects-project-explorers-explorerId-authoring-v2-preview
- `browserCase:builder-controls:recompile`: post:api-v1-projects-project-explorers-explorerId-authoring-v2-commands, post:api-v1-projects-project-explorers-explorerId-authoring-v2-preview

## Bounds

The test index is heuristic. Registry endpoints, Go route strings, and route proofs are strong links. Vitest test names are keyword links. The report marks the mapping on every test.
