# Builder route action proposals

Status: generated review queue. These proposals do not change the route ledger.

## Summary

| Measure | Count |
| --- | ---: |
| routes | 24 |
| high | 17 |
| medium | 5 |
| low | 2 |
| readOnly | 16 |
| stateful | 8 |
| exactEvidence | 15 |
| suffixOnlyEvidence | 2 |

## Review queue

| Tier | Route | Mode | Kind | Confidence | Proposed actions | Evidence |
| ---: | --- | --- | --- | --- | --- | --- |
| 1 | `GET /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/builder` | stateful | builder | high | `HydrateBuilder` | browser, playwright:recompile scripts/verify-ui/specs/builder-controls.spec.mjs, go:TestBuilderUnavailableResponseExplainsResourceInventoryRebuild internal/server/explorer_capability_test.go +2 more |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/construction-proposals` | read | read | high | none | go:TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly internal/server/openapi_explorer_construction_proposal_test.go, go:TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal internal/explorer/lifecycle/builder_full_ui_replay_test.go, go:TestConstructionProposalSemanticGroupChoiceReconcilesCompiledOutputMetadata internal/server/openapi_explorer_construction_proposal_test.go +13 more |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/interpretation-preview` | read | read | high | none | go:TestInterpretationPreviewRouteRejectsMissingBody internal/server/openapi_interpretation_routes_test.go, go:TestInterpretationReadRoutesEnforceProjectReadAuthorization internal/server/openapi_interpretation_routes_test.go, ui:renders only server-listed applicable summaries, then reviews, cancels, and applies the exact receipt ui/packages/loom-ui/src/features/ExplorerBuilder/components/InterpretationPanel.unit.test.tsx |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/interpretation-revisions` | stateful | interpretation | medium | `BeginCreate`, `CancelCreate`, `CreateBlocked`, `CreateFailed`, `CreateRevision` | go:TestGeneratedExplorerMutationRoutesForwardAuthResourcePath internal/server/openapi_explorer_write_scope_test.go, ui:creates from the saved column without sending rules or auto-applying the result ui/packages/loom-ui/src/features/ExplorerBuilder/components/InterpretationPanel.unit.test.tsx, ui:revises the selected applicable head with its exact parent revision ui/packages/loom-ui/src/features/ExplorerBuilder/components/InterpretationPanel.unit.test.tsx |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/population-mapping` | stateful | population | medium | `CoverageComplete`, `CoverageIncomplete`, `CoverageStale`, `RequestCoverage` | go:TestPopulationMappingRouteUsesReceiptBoundAuthoredFilterConstruction internal/server/openapi_explorer_population_mapping_construction_test.go |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/population-member-proposals` | read | read | high | none | go:TestPopulationMemberRemovalProposalHTTPPreviewsAndAppliesExactScopedCAS internal/server/openapi_explorer_population_member_proposal_test.go |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/population-routes` | read | read | high | none | none |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/publish` | stateful | publish | low | none | go:TestGeneratedExplorerMutationRoutesForwardAuthResourcePath internal/server/openapi_explorer_write_scope_test.go, go:TestOpenAPIDocumentsOperationSpecificFailureStatuses internal/server/openapi_route_inventory_test.go |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/row-change` | stateful | row-change | low | `ApplyPreview`, `AssessmentBlocked`, `AssessmentNoChange`, `AssessmentReady`, `AssessmentStale`, `BeginChange`, `Cancel`, `PreviewReady`, `PreviewUnavailable`, `WorkspaceChanged` | go:TestAssessAndApplyRowChangeThroughPublicAPI internal/server/explorer_row_change_route_test.go |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/row-definition-proposals` | read | read | high | none | go:TestCase008ExplicitGroupRowDefinitionPreviewCompilesCandidateReceipt internal/server/case008_row_definition_preview_route_test.go, go:TestBuilderFullUIRowDefinitionProposalDispatchUsesRealService internal/explorer/lifecycle/builder_full_ui_replay_test.go, go:TestRowDefinitionHTTPContractListsChoicesAndPreviewsWithoutDraftMutation internal/server/openapi_explorer_row_definition_test.go |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/table-shape-category-discoveries` | read | read | high | none | none |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/selections` | stateful | selection | medium | `ExcludeMember`, `ExcludeRouteFound`, `ExcludeRouteMissing` | none |
| 1 | `GET /api/v1/projects/{project}/explorers/{explorerId}/selections/{selectionRevision}` | read | read | high | none | none |
| 2 | `GET /api/v1/projects/{project}/explorers` | read | read | high | none | browser, playwright:recompile scripts/verify-ui/specs/builder-controls.spec.mjs, go:TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly internal/server/openapi_explorer_construction_proposal_test.go +25 more |
| 2 | `POST /api/v1/projects/{project}/explorers` | stateful | explorer | medium | `CreateExplorer`, `HydrateBuilder`, `SelectExplorer` | browser, playwright:recompile scripts/verify-ui/specs/builder-controls.spec.mjs, go:TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly internal/server/openapi_explorer_construction_proposal_test.go +25 more |
| 2 | `DELETE /api/v1/projects/{project}/explorers/{explorerId}` | stateful | explorer | medium | `DeleteCapability`, `DeleteExplorer` | go:TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly internal/server/openapi_explorer_construction_proposal_test.go, go:TestReconcileSelectedOutputScopesReceiptAndPreviewPastStaleConsumer internal/server/explorer_reconcile_scope_test.go, go:TestAssessAndApplyRowChangeThroughPublicAPI internal/server/explorer_row_change_route_test.go +24 more |
| 2 | `GET /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/capability` | read | read | high | none | browser, playwright:recompile scripts/verify-ui/specs/builder-controls.spec.mjs, go:TestMigrateLegacyWorkspaceForCurrentAPIUsesScopedCatalogAndReusesPublishBody internal/acceptance/scenario_test.go +2 more |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/column-source` | read | read | high | none | none |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/configured-column-context` | read | read | high | none | browser, playwright:recompile scripts/verify-ui/specs/builder-controls.spec.mjs, go:TestConfiguredColumnContextRouteRejectsMissingBody internal/server/openapi_interpretation_routes_test.go +1 more |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/construction-capabilities` | read | read | high | none | browser, playwright:recompile scripts/verify-ui/specs/builder-controls.spec.mjs, go:TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly internal/server/openapi_explorer_construction_proposal_test.go +8 more |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/frame-source-options` | read | read | high | none | none |
| 2 | `GET /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/row-definition-choices` | read | read | high | none | go:TestBuilderFullUIRequestDecoderPreservesNullableBodyAndEndpoint internal/explorer/lifecycle/builder_full_ui_replay_test.go, go:TestRowDefinitionHTTPContractListsChoicesAndPreviewsWithoutDraftMutation internal/server/openapi_explorer_row_definition_test.go |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/row-lineage` | read | read | high | none | none |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/suggestions` | read | read | high | none | browser, playwright:recompile scripts/verify-ui/specs/builder-controls.spec.mjs, go:TestBuilderFullUIRequestDecoderPreservesNullableBodyAndEndpoint internal/explorer/lifecycle/builder_full_ui_replay_test.go |

## Bounds

The proposal is a path-and-affordance heuristic. Every row needs a handler review before it enters `builder-route-overrides.json`.
