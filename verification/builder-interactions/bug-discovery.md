# Builder bug discovery

Status: generated from the mutation probes, retained evidence, source hashes, and the strict allowlist.

## Summary

| Measure | Count |
| --- | ---: |
| probes | 10 |
| evidence | 10 |
| killed | 8 |
| survived | 1 |
| allowedSurvivors | 1 |
| unallowedSurvivors | 0 |
| equivalent | 1 |
| missing | 0 |
| unhashedSources | 0 |
| staleSources | 0 |
| triageErrors | 0 |
| strictPass | yes |
| actionObligations | 153 |
| probedActions | 9 |
| unprobedActions | 144 |

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
| `command-draft-version-guard` | vitest | `LifecycleEntry.StartCommand` | survived | current | automatically previews saved commands |
| `construction-inputs-draft-version-guard` | go | `ReconcileLifecycle.DraftChange` | killed | current | stale draft was accepted |
| `construction-inputs-cursor-draft-guard` | go | `ReconcileLifecycle.DraftChange` | killed | current | cursor from a different draft version was accepted |

## Allowlist

- `command-draft-version-guard`: Known product gap: command draft identity is not yet covered by a targeted CAS test.

## Survivors

- `command-draft-version-guard` is allowlisted: Known product gap: command draft identity is not yet covered by a targeted CAS test.

## Stale or unhashed evidence

Every retained probe has a current source hash.

## Action coverage

| Measure | Count |
| --- | ---: |
| actionObligations | 153 |
| probedActions | 9 |
| unprobedActions | 144 |

Covered actions:

- `BuilderPreview.ApplyAck`: `apply-ack-owner-guard`
- `BuilderPreview.DiscoveryComplete`: `discovery-generation-guard`
- `BuilderPreview.PreviewComplete`: `preview-receipt-guard`
- `BuilderPreview.ProposalComplete`: `proposal-draft-digest-guard`
- `Interpretation.ApplyCandidate`: `interpretation-apply-receipt-guard`
- `LifecycleEntry.StartCommand`: `command-draft-version-guard`
- `Population.ApplyRemoval`: `population-removal-receipt-guard`
- `ReconcileLifecycle.DraftChange`: `construction-inputs-cursor-draft-guard`, `construction-inputs-draft-version-guard`
- `RootChange.WorkspaceChanged`: `root-change-epoch-guard`

Actions without a mutation probe:

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
- 104 more in the JSON report.

## Bounds

The probes are hand-authored guards. The report proves which guards have fresh mutation evidence and which model actions have no probe. It does not generate model-derived mutants yet.
