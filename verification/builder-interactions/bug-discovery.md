# Builder bug discovery

Status: generated from the mutation probes and retained evidence.

## Summary

| Measure | Count |
| --- | ---: |
| probes | 8 |
| evidence | 8 |
| killed | 6 |
| survived | 1 |
| equivalent | 1 |
| missing | 0 |
| strictPass | no |
| actionObligations | 153 |
| probedActions | 8 |
| unprobedActions | 145 |

## Probes

| Probe | Model action | Status | Expected failure |
| --- | --- | --- | --- |
| `apply-ack-owner-guard` | `BuilderPreview.ApplyAck` | killed | old Apply success after replacement |
| `preview-receipt-guard` | `BuilderPreview.PreviewComplete` | killed | B preview=wrong receipt |
| `proposal-draft-digest-guard` | `BuilderPreview.ProposalComplete` | killed | expectedDraftDigest mismatch |
| `root-change-epoch-guard` | `RootChange.WorkspaceChanged` | killed | different request epoch |
| `population-removal-receipt-guard` | `Population.ApplyRemoval` | killed | another receipt |
| `interpretation-apply-receipt-guard` | `Interpretation.ApplyCandidate` | killed | candidate-receipt |
| `discovery-generation-guard` | `BuilderPreview.DiscoveryComplete` | equivalent | same-key discovery settlement |
| `command-draft-version-guard` | `LifecycleEntry.StartCommand` | survived | automatically previews saved commands |

## Survivors

- `command-draft-version-guard` mutated `ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx`. The focused test did not fail with `automatically previews saved commands`.

## Actions without a mutation probe

- `BuilderOperations.AuthorStep`
- `BuilderOperations.ResetWorkspace`
- `BuilderOperations.RouteSavedStep`
- `BuilderOperations.UndoLastStep`
- `BuilderPreview.AcceptCurrentDraft`
- `BuilderPreview.AdoptDiscovery`
- `BuilderPreview.BeginApply`
- `BuilderPreview.CancelProposal`
- `BuilderPreview.ChangeDraft`
- `BuilderPreview.ChangePivotKey`
- `BuilderPreview.LatePreviewComplete`
- `BuilderPreview.LateProposalComplete`
- `BuilderPreview.ProposalError`
- `BuilderPreview.ReplaceProposal`
- `BuilderPreview.RetryProposal`
- `BuilderPreview.ScheduleDiscovery`
- `BuilderPreview.SetPivotPair`
- `BuilderPreview.StartPreview`
- `BuilderPreview.StartProposal`
- `BuilderPreview.UnmountEditor`
- `ColumnPresentation.CreateRelatedAggregate`
- `ColumnPresentation.InspectColumn`
- `ColumnPresentation.InspectRejected`
- `ColumnPresentation.InspectSucceeded`
- `ColumnPresentation.MoveColumn`
- `ColumnPresentation.RemoveConfiguredColumn`
- `ColumnPresentation.RenameColumn`
- `ColumnPresentation.SearchColumns`
- `ColumnPresentation.SetFeaturePolicy`
- `ColumnPresentation.SetFilterOffered`
- `ColumnPresentation.SetRowValuePolicy`
- `ColumnPresentation.ToggleVisibility`
- `ConstructionLifecycle.BeginApply`
- `ConstructionLifecycle.CancelProposal`
- `ConstructionLifecycle.ChangeDraftIdentity`
- `ConstructionLifecycle.LatePreviewSettlementIgnored`
- `ConstructionLifecycle.LateProposalSettlementIgnored`
- `ConstructionLifecycle.PreviewComplete`
- `ConstructionLifecycle.PreviewError`
- `ConstructionLifecycle.ProposalComplete`
- 105 more in the JSON report.

## Bounds

The probes are hand-authored frontend guards. The report proves which guards have retained mutation evidence. It does not generate model-derived mutants or backend probes yet.
