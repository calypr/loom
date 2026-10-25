# Builder bug discovery

Status: generated from the mutation probes, retained evidence, source hashes, and the strict allowlist.

## Summary

| Measure | Count |
| --- | ---: |
| probes | 50 |
| evidence | 50 |
| killed | 47 |
| survived | 0 |
| allowedSurvivors | 0 |
| unallowedSurvivors | 0 |
| equivalent | 3 |
| missing | 0 |
| unhashedSources | 0 |
| staleSources | 0 |
| triageErrors | 0 |
| strictPass | yes |
| actionObligations | 194 |
| probedActions | 59 |
| unprobedActions | 135 |

## Probes

| Probe | Runner | Model action | Status | Source hash | Expected failure |
| --- | --- | --- | --- | --- | --- |
| `apply-ack-owner-guard` | vitest | `BuilderPreview.ApplyAck` | killed | current | old Apply success after replacement |
| `preview-receipt-guard` | vitest | `BuilderPreview.PreviewComplete` | killed | current | B preview=wrong receipt |
| `proposal-draft-digest-guard` | vitest | `BuilderPreview.ProposalComplete` | killed | current | expectedDraftDigest mismatch |
| `root-change-epoch-guard` | vitest | `RootChange.WorkspaceChanged` | killed | current | different request epoch |
| `population-removal-receipt-guard` | vitest | `Population.ApplyRemoval` | killed | current | another receipt |
| `interpretation-apply-receipt-guard` | vitest | `Interpretation.ApplyCandidate` | killed | current | candidate-receipt |
| `discovery-generation-guard` | vitest | `BuilderPreview.DiscoveryComplete` | equivalent | current | same-key discovery settlement |
| `command-draft-version-guard` | vitest | `LifecycleEntry.StartCommand` | killed | current | expectedDraftVersion |
| `construction-inputs-draft-version-guard` | go | `ReconcileLifecycle.DraftChange` | killed | current | stale draft was accepted |
| `construction-inputs-cursor-draft-guard` | go | `ReconcileLifecycle.DraftChange` | killed | current | cursor from a different draft version was accepted |
| `proposal-needs-repair-guard` | vitest | `BuilderPreview.ProposalError` | killed | current | expected 'resolved' to be 'rejected' |
| `proposal-cancel-local-action-guard` | vitest | `BuilderPreview.CancelProposal`, `ConstructionLifecycle.CancelProposal`, `RootChange.Cancel`, `RowDefinitionLifecycle.CancelProposal`, `RowDefinitionLifecycle.RequestProposal` | killed | current | to not be called |
| `pivot-pair-guard` | vitest | `BuilderPreview.SetPivotPair` | killed | current | Unable to find role |
| `source-expand-stale-proposal-guard` | vitest | `BuilderPreview.LateProposalComplete`, `ConstructionLifecycle.LateProposalSettlementIgnored` | killed | current | Unable to find role |
| `replacement-preview-output-guard` | vitest | `Interpretation.ReviewCandidate` | killed | current | Unable to find role |
| `row-lineage-capability-guard` | vitest | `SupportingSurfaces.RequestRowSources` | killed | current | to not be called at all |
| `population-repeated-cursor-guard` | vitest | `SupportingSurfaces.NextLineagePage` | killed | current | repeated page cursor |
| `draft-restore-error-guard` | vitest | `DraftRestore.StaleDraft` | killed | current | expected false to be true |
| `builder-load-error-helper-guard` | vitest | `WorkspaceLifecycle.ExplorerListError` | killed | current | expected false to be true |
| `construction-draft-identity-guard` | vitest | `ConstructionLifecycle.ChangeDraftIdentity` | killed | current | expected true to be false |
| `server-draft-sync-guard` | vitest | `BuilderPreview.AcceptCurrentDraft` | killed | current | expected true to be false |
| `shape-combine-row-policy-guard` | vitest | `ColumnPresentation.SetRowValuePolicy` | killed | current | expected true to be false |
| `refresh-preview-catalog-guard` | vitest | `WorkspaceLifecycle.RefreshCatalog` | killed | current | to be called once |
| `row-sources-unavailable-guard` | vitest | `SupportingSurfaces.RowSourcesUnavailable` | killed | current | to contain text |
| `paired-preview-rows-guard` | vitest | `Interpretation.ReviewReady`, `RootChange.PreviewReady`, `Population.RemovalReviewReady` | killed | current | expected true to be false |
| `select-table-command-guard` | vitest | `WorkspaceLifecycle.SelectTable` | killed | current | expected true to be false |
| `select-occurrence-navigation-guard` | vitest | `WorkspaceLifecycle.SelectOccurrence` | killed | current | expected false to be true |
| `construction-proposal-action-guard` | vitest | `RowDefinitionLifecycle.ApplyProposal` | killed | current | expected false to be true |
| `row-definition-action-guard` | vitest | `RowDefinitionLifecycle.SelectRow` | killed | current | expected false to be true |
| `related-field-reduction-action-guard` | vitest | `ColumnPresentation.CreateRelatedAggregate` | killed | current | expected false to be true |
| `dataset-review-single-scope-guard` | vitest | `ReconcileLifecycle.SetTablesRooted` | killed | current | expected true to be false |
| `preview-row-sources-ready-guard` | vitest | `SupportingSurfaces.RowSourcesReady` | killed | current | to not be called |
| `reconcile-empty-output-guard` | go | `ReconcileLifecycle.RequestReconcile`, `ReconcileLifecycle.ReconcileSuccess` | killed | current | reconcile status= |
| `reconcile-draft-identity-guard` | go | `ReconcileLifecycle.ReconcileStale` | killed | current | stale reconcile status= |
| `reconcile-output-whitespace-guard` | go | `ReconcileLifecycle.ReconcileInvalid` | killed | current | padded output status= |
| `automatic-preview-settled-reuse-guard` | vitest | `WorkspaceLifecycle.AcceptPreview`, `WorkspaceLifecycle.RecompileReceipt` | equivalent | current | toHaveBeenCalledTimes |
| `graph-orphans-toggle-guard` | vitest | `SupportingSurfaces.ToggleGraphOrphans` | killed | current | data-node-count |
| `graph-expand-on-pane-guard` | vitest | `SupportingSurfaces.ExpandGraph` | killed | current | expanded |
| `graph-inspect-node-guard` | vitest | `SupportingSurfaces.InspectGraphNode` | equivalent | current | to not be called |
| `population-clear-disabled-guard` | vitest | `Population.ClearAttached` | killed | current | to not be called |
| `dataset-review-focus-guard` | vitest | `SupportingSurfaces.FocusReview` | killed | current | toHaveBeenCalledWith |
| `column-visibility-toggle-guard` | vitest | `ColumnPresentation.ToggleVisibility` | killed | current | toHaveBeenLastCalledWith |
| `column-filter-toggle-guard` | vitest | `ColumnPresentation.SetFilterOffered` | killed | current | toHaveBeenLastCalledWith |
| `column-search-query-guard` | vitest | `ColumnPresentation.SearchColumns` | killed | current | No columns match this search |
| `column-remove-control-guard` | vitest | `ColumnPresentation.RemoveConfiguredColumn` | killed | current | toHaveBeenCalledWith |
| `column-inspect-control-guard` | vitest | `ColumnPresentation.InspectColumn`, `ColumnPresentation.InspectSucceeded` | killed | current | Source for Height |
| `column-rename-fallback-guard` | vitest | `ColumnPresentation.RenameColumn` | killed | current | expected |
| `column-move-end-branch-guard` | vitest | `ColumnPresentation.MoveColumn` | killed | current | toHaveBeenCalledWith |
| `feature-policy-capability-guard` | vitest | `ColumnPresentation.SetFeaturePolicy` | killed | current | Category recoding is unavailable |
| `column-inspect-mismatch-guard` | vitest | `ColumnPresentation.InspectRejected` | killed | current | Unable to find role="alert" |

## Allowlist

No survivor is allowlisted.

## Survivors

No surviving probe.

## Stale or unhashed evidence

Every retained probe has a current source hash.

## Action coverage

| Measure | Count |
| --- | ---: |
| actionObligations | 194 |
| probedActions | 59 |
| unprobedActions | 135 |

Covered actions:

- `BuilderPreview.AcceptCurrentDraft`: `server-draft-sync-guard`
- `BuilderPreview.ApplyAck`: `apply-ack-owner-guard`
- `BuilderPreview.CancelProposal`: `proposal-cancel-local-action-guard`
- `BuilderPreview.DiscoveryComplete`: `discovery-generation-guard`
- `BuilderPreview.LateProposalComplete`: `source-expand-stale-proposal-guard`
- `BuilderPreview.PreviewComplete`: `preview-receipt-guard`
- `BuilderPreview.ProposalComplete`: `proposal-draft-digest-guard`
- `BuilderPreview.ProposalError`: `proposal-needs-repair-guard`
- `BuilderPreview.SetPivotPair`: `pivot-pair-guard`
- `ColumnPresentation.CreateRelatedAggregate`: `related-field-reduction-action-guard`
- `ColumnPresentation.InspectColumn`: `column-inspect-control-guard`
- `ColumnPresentation.InspectRejected`: `column-inspect-mismatch-guard`
- `ColumnPresentation.InspectSucceeded`: `column-inspect-control-guard`
- `ColumnPresentation.MoveColumn`: `column-move-end-branch-guard`
- `ColumnPresentation.RemoveConfiguredColumn`: `column-remove-control-guard`
- `ColumnPresentation.RenameColumn`: `column-rename-fallback-guard`
- `ColumnPresentation.SearchColumns`: `column-search-query-guard`
- `ColumnPresentation.SetFeaturePolicy`: `feature-policy-capability-guard`
- `ColumnPresentation.SetFilterOffered`: `column-filter-toggle-guard`
- `ColumnPresentation.SetRowValuePolicy`: `shape-combine-row-policy-guard`
- `ColumnPresentation.ToggleVisibility`: `column-visibility-toggle-guard`
- `ConstructionLifecycle.CancelProposal`: `proposal-cancel-local-action-guard`
- `ConstructionLifecycle.ChangeDraftIdentity`: `construction-draft-identity-guard`
- `ConstructionLifecycle.LateProposalSettlementIgnored`: `source-expand-stale-proposal-guard`
- `DraftRestore.StaleDraft`: `draft-restore-error-guard`
- `Interpretation.ApplyCandidate`: `interpretation-apply-receipt-guard`
- `Interpretation.ReviewCandidate`: `replacement-preview-output-guard`
- `Interpretation.ReviewReady`: `paired-preview-rows-guard`
- `LifecycleEntry.StartCommand`: `command-draft-version-guard`
- `Population.ApplyRemoval`: `population-removal-receipt-guard`
- `Population.ClearAttached`: `population-clear-disabled-guard`
- `Population.RemovalReviewReady`: `paired-preview-rows-guard`
- `ReconcileLifecycle.DraftChange`: `construction-inputs-cursor-draft-guard`, `construction-inputs-draft-version-guard`
- `ReconcileLifecycle.ReconcileInvalid`: `reconcile-output-whitespace-guard`
- `ReconcileLifecycle.ReconcileStale`: `reconcile-draft-identity-guard`
- `ReconcileLifecycle.ReconcileSuccess`: `reconcile-empty-output-guard`
- `ReconcileLifecycle.RequestReconcile`: `reconcile-empty-output-guard`
- `ReconcileLifecycle.SetTablesRooted`: `dataset-review-single-scope-guard`
- `RootChange.Cancel`: `proposal-cancel-local-action-guard`
- `RootChange.PreviewReady`: `paired-preview-rows-guard`
- `RootChange.WorkspaceChanged`: `root-change-epoch-guard`
- `RowDefinitionLifecycle.ApplyProposal`: `construction-proposal-action-guard`
- `RowDefinitionLifecycle.CancelProposal`: `proposal-cancel-local-action-guard`
- `RowDefinitionLifecycle.RequestProposal`: `proposal-cancel-local-action-guard`
- `RowDefinitionLifecycle.SelectRow`: `row-definition-action-guard`
- `SupportingSurfaces.ExpandGraph`: `graph-expand-on-pane-guard`
- `SupportingSurfaces.FocusReview`: `dataset-review-focus-guard`
- `SupportingSurfaces.InspectGraphNode`: `graph-inspect-node-guard`
- `SupportingSurfaces.NextLineagePage`: `population-repeated-cursor-guard`
- `SupportingSurfaces.RequestRowSources`: `row-lineage-capability-guard`
- `SupportingSurfaces.RowSourcesReady`: `preview-row-sources-ready-guard`
- `SupportingSurfaces.RowSourcesUnavailable`: `row-sources-unavailable-guard`
- `SupportingSurfaces.ToggleGraphOrphans`: `graph-orphans-toggle-guard`
- `WorkspaceLifecycle.AcceptPreview`: `automatic-preview-settled-reuse-guard`
- `WorkspaceLifecycle.ExplorerListError`: `builder-load-error-helper-guard`
- `WorkspaceLifecycle.RecompileReceipt`: `automatic-preview-settled-reuse-guard`
- `WorkspaceLifecycle.RefreshCatalog`: `refresh-preview-catalog-guard`
- `WorkspaceLifecycle.SelectOccurrence`: `select-occurrence-navigation-guard`
- `WorkspaceLifecycle.SelectTable`: `select-table-command-guard`

Actions without a mutation probe:

- `BuilderJourney.AddColumn`
- `BuilderJourney.CancelFilter`
- `BuilderJourney.CdaCreateByIDCountRowsIntent`
- `BuilderJourney.CdaCurrentSnapshot`
- `BuilderJourney.CdaEditByIDCountDistinctIntent`
- `BuilderJourney.CdaEditByIDCountRowsIntent`
- `BuilderJourney.CdaLatePreviewCompleteIgnored`
- `BuilderJourney.CdaLatePreviewErrorIgnored`
- `BuilderJourney.CdaPreviewComplete`
- `BuilderJourney.CdaPreviewError`
- `BuilderJourney.CdaPreviewOwner`
- `BuilderJourney.CdaPreviewRequestCancelled`
- `BuilderJourney.CdaRequestOwnerTokens`
- `BuilderJourney.CdaSnapshot5`
- `BuilderJourney.ChoicePreview`
- `BuilderJourney.ChoicePreviewComplete`
- `BuilderJourney.ChoicePreviewError`
- `BuilderJourney.EditGroup`
- `BuilderJourney.EditGroupAgain`
- `BuilderJourney.EditGroupAgainApply`
- `BuilderJourney.EditGroupApply`
- `BuilderJourney.Group`
- `BuilderJourney.GroupAgain`
- `BuilderJourney.GroupApply`
- `BuilderJourney.GroupEditCancel`
- `BuilderJourney.GroupRemovalCancel`
- `BuilderJourney.LatePreviewCompleteIgnored`
- `BuilderJourney.LatePreviewErrorIgnored`
- `BuilderJourney.MaxReshapes`
- `BuilderJourney.Pivot`
- `BuilderJourney.Preview`
- `BuilderJourney.PreviewComplete`
- `BuilderJourney.PreviewError`
- `BuilderJourney.PreviewRequestCancelled`
- `BuilderJourney.Reload`
- `BuilderJourney.RemoveGroup`
- `BuilderJourney.RemoveGroupApply`
- `BuilderJourney.vars`
- `BuilderOperations.AuthorStep`
- `BuilderOperations.ResetWorkspace`
- 95 more in the JSON report.

## Bounds

The probes are hand-authored guards. The report proves which guards have fresh mutation evidence and which model actions have no probe. It does not generate model-derived mutants yet.
