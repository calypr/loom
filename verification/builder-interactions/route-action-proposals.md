# Builder route action proposals

Status: generated review queue. These proposals do not change the route ledger.

## Summary

| Measure | Count |
| --- | ---: |
| routes | 24 |
| high | 8 |
| medium | 10 |
| low | 6 |
| readOnly | 7 |
| stateful | 17 |

## Review queue

| Tier | Route | Kind | Confidence | Proposed actions | Evidence |
| ---: | --- | --- | --- | --- | --- |
| 1 | `GET /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/builder` | builder | high | `HydrateBuilder` | browser, go:TestBuilderUnavailableResponseExplainsResourceInventoryRebuild internal/server/explorer_capability_test.go, go:TestMigrateLegacyWorkspaceForCurrentAPIUsesScopedCatalogAndReusesPublishBody internal/acceptance/scenario_test.go +4 more |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/construction-proposals` | proposal | low | `CancelProposal`, `PreviewComplete`, `ProposalComplete`, `ProposalError`, `ReplaceProposal`, `RetryProposal`, `StartPreview`, `StartProposal` | go:TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal internal/explorer/lifecycle/builder_full_ui_replay_test.go, go:TestBuilderFullUIScalarFilterAndDeriveRequestsReplayThroughRealService internal/explorer/lifecycle/builder_full_ui_replay_test.go, go:TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly internal/server/openapi_explorer_construction_proposal_test.go +6 more |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/interpretation-preview` | interpretation | medium | `ApplyCandidate`, `ReviewCandidate`, `ReviewReady`, `ReviewStale` | go:TestInterpretationPreviewRouteRejectsMissingBody internal/server/openapi_interpretation_routes_test.go, go:TestInterpretationReadRoutesEnforceProjectReadAuthorization internal/server/openapi_interpretation_routes_test.go, ui:renders only server-listed applicable summaries, then reviews, cancels, and applies the exact receipt ui/packages/loom-ui/src/features/ExplorerBuilder/components/InterpretationPanel.unit.test.tsx |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/interpretation-revisions` | interpretation | medium | `BeginCreate`, `CancelCreate`, `CreateBlocked`, `CreateFailed`, `CreateRevision` | go:TestGeneratedExplorerMutationRoutesForwardAuthResourcePath internal/server/openapi_explorer_write_scope_test.go |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/population-mapping` | population | medium | `CoverageComplete`, `CoverageIncomplete`, `CoverageStale`, `RequestCoverage` | go:TestPopulationMappingRouteUsesReceiptBoundAuthoredFilterConstruction internal/server/openapi_explorer_population_mapping_construction_test.go |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/population-member-proposals` | population | medium | `ApplyRemoval`, `RemovalFailed`, `RemovalReviewReady`, `ReviewRemoval` | go:TestPopulationMemberRemovalProposalHTTPPreviewsAndAppliesExactScopedCAS internal/server/openapi_explorer_population_member_proposal_test.go |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/population-routes` | population | low | `AttachSelection`, `ExcludeMember`, `ExcludeRouteFound`, `ExcludeRouteMissing`, `LookupRoutes`, `ReplaceSelection`, `RouteLookupFailed` | none |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/publish` | other | low | none | go:TestGeneratedExplorerMutationRoutesForwardAuthResourcePath internal/server/openapi_explorer_write_scope_test.go, go:TestOpenAPIDocumentsOperationSpecificFailureStatuses internal/server/openapi_route_inventory_test.go |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/row-change` | row-change | low | `ApplyPreview`, `AssessmentBlocked`, `AssessmentNoChange`, `AssessmentReady`, `AssessmentStale`, `BeginChange`, `Cancel`, `PreviewReady`, `PreviewUnavailable`, `WorkspaceChanged` | go:TestAssessAndApplyRowChangeThroughPublicAPI internal/server/explorer_row_change_route_test.go |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/row-definition-proposals` | proposal | low | `ApplyProposal`, `CancelProposal`, `PreviewComplete`, `ProposalComplete`, `ProposalFailed`, `RequestProposal`, `StartPreview`, `StartProposal` | go:TestBuilderFullUIRowDefinitionProposalDispatchUsesRealService internal/explorer/lifecycle/builder_full_ui_replay_test.go, go:TestCase008ExplicitGroupRowDefinitionPreviewCompilesCandidateReceipt internal/server/case008_row_definition_preview_route_test.go, go:TestBuilderFullUIRequestReplay internal/explorer/lifecycle/builder_full_ui_replay_test.go +1 more |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/table-shape-category-discoveries` | discovery | medium | `DiscoveryComplete`, `ScheduleDiscovery` | none |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/selections` | selection | low | none | none |
| 1 | `GET /api/v1/projects/{project}/explorers/{explorerId}/selections/{selectionRevision}` | selection | medium | `ParseRoute`, `ReplaceExplorer`, `SelectionFailed`, `SelectionReady`, `SetFeatureFocus` | none |
| 2 | `GET /api/v1/projects/{project}/explorers` | explorer | medium | `CreateExplorer`, `DeleteCapability`, `DeleteExplorer`, `ExplorerListComplete`, `ExplorerListError`, `HydrateBuilder`, `SelectExplorer` | browser, go:TestAssessAndApplyRowChangeThroughPublicAPI internal/server/explorer_row_change_route_test.go, go:TestBrowseSemanticInventoryThroughPublicAPI internal/server/openapi_semantic_inventory_test.go +23 more |
| 2 | `POST /api/v1/projects/{project}/explorers` | explorer | medium | `CreateExplorer`, `DeleteCapability`, `DeleteExplorer`, `ExplorerListComplete`, `ExplorerListError`, `HydrateBuilder`, `SelectExplorer` | browser, go:TestAssessAndApplyRowChangeThroughPublicAPI internal/server/explorer_row_change_route_test.go, go:TestBrowseSemanticInventoryThroughPublicAPI internal/server/openapi_semantic_inventory_test.go +23 more |
| 2 | `DELETE /api/v1/projects/{project}/explorers/{explorerId}` | explorer | medium | `CreateExplorer`, `DeleteCapability`, `DeleteExplorer`, `SelectExplorer` | go:TestAssessAndApplyRowChangeThroughPublicAPI internal/server/explorer_row_change_route_test.go, go:TestBrowseSemanticInventoryThroughPublicAPI internal/server/openapi_semantic_inventory_test.go, go:TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt internal/server/explorer_receipt_contract_test.go +20 more |
| 2 | `GET /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/capability` | read | high | none | browser, go:TestMigrateLegacyWorkspaceForCurrentAPIUsesScopedCatalogAndReusesPublishBody internal/acceptance/scenario_test.go, go:TestMigrateLegacyWorkspaceForOldAPIPreservesExactBytes internal/acceptance/scenario_test.go +1 more |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/column-source` | read | high | none | none |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/configured-column-context` | read | high | none | browser, go:TestConfiguredColumnContextRouteRejectsMissingBody internal/server/openapi_interpretation_routes_test.go, go:TestInterpretationReadRoutesEnforceProjectReadAuthorization internal/server/openapi_interpretation_routes_test.go +1 more |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/construction-capabilities` | read | high | none | browser, go:TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly internal/server/openapi_explorer_construction_proposal_test.go, go:TestConstructionProposalSemanticGroupChoiceReconcilesCompiledOutputMetadata internal/server/openapi_explorer_construction_proposal_test.go +4 more |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/frame-source-options` | read | high | none | none |
| 2 | `GET /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/row-definition-choices` | read | high | none | go:TestBuilderFullUIRequestDecoderPreservesNullableBodyAndEndpoint internal/explorer/lifecycle/builder_full_ui_replay_test.go |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/row-lineage` | read | high | none | none |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/suggestions` | proposal | medium | `PreviewComplete`, `ProposalComplete`, `StartPreview`, `StartProposal` | browser, go:TestBuilderFullUIRequestDecoderPreservesNullableBodyAndEndpoint internal/explorer/lifecycle/builder_full_ui_replay_test.go, playwright:recompile scripts/verify-ui/specs/builder-controls.spec.mjs +1 more |

## Bounds

The proposal is a path-and-affordance heuristic. Every row needs a handler review before it enters `builder-route-overrides.json`.
