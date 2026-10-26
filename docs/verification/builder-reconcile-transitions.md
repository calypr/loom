# Builder reconcile transitions

Status: implemented model, controller decisions, trace schedules, and route proof.

`ReconcileLifecycle.tla` is the canonical reconcile request and response model. `LifecycleEntry.tla` keeps the entry and command gating model. `BuilderPreview.tla` calls its reset transition `AcceptCurrentDraft`.

## Transition table

| From | Event | Guard | Effect | TLA action | Source |
|---|---|---|---|---|---|
| idle | reconcile request | snapshot present, rooted tables | capture request identity, clear receipt, reset retry counters | `RequestReconcile` | `BuilderWorkspace.tsx` `reconcileCurrent` |
| requesting | success | request identity still current | bind receipt to current identity | `ReconcileSuccess` | `BuilderWorkspace.tsx` success guard |
| requesting | stale snapshot | stale refresh below maximum | refetch catalog, copy server identity, retry | `ReconcileStale` then `RetryReconcile` | `reconcileRecovery.ts` `refresh-and-retry` |
| requesting | stale snapshot | stale refresh at maximum | repair | `ReconcileFailure(fatal)` | `reconcileRecovery.ts` `repair` |
| requesting | draft conflict | any | refetch and hydrate, stop | `ReconcileConflict` | `reconcileRecovery.ts` `refresh-and-stop` |
| requesting | invalid output or invalid saved state | any | repair | `ReconcileInvalid` | `reconcileRecovery.ts` `repair` |
| requesting | retryable failure | attempt below maximum | wait and retry | `ReconcileFailure(retryable)` then `RetryReconcile` | `reconcileRecovery.ts` `retry` |
| requesting | fatal failure | any | repair | `ReconcileFailure(fatal)` | `reconcileRecovery.ts` `repair` |
| any | draft change | any | clear receipt, reset retry counters, return to idle | `DraftChange` | `builderAuthoringReducer` |
| any | authored workspace change | any | clear receipt, return to idle | `AuthoredChange` | `builderAuthoringReducer` |
| any | explorer change | any | clear receipt, return to idle | `ChangeExplorer` | explorer selection effect |
| any | clear receipt | any | return to idle | `ClearReceipt` | reducer and reconcile reset |

## Guard matrix

| Invariant | UI guard | Server guard | Test |
|---|---|---|---|
| `Inv_ReconcileRequiresRootedTables` | snapshot token, tables present, every table has a root | authoring write scope and request validation | `BuilderWorkspace.reconciliation.unit.test.tsx`, `LifecycleEntry.tla` |
| `Inv_RequestMatchesCurrent` | request captures the latest state at attempt start | snapshot, draft version, and digest validation | `ReconcileLifecycle.tla`, `explorer_reconcile_scope_test.go` |
| `Inv_RetryBounded` | two stale refreshes, two transient retries, three total attempts | not applicable | `reconcileRecovery.unit.test.ts` |
| `Inv_ReceiptOnlyWhenSucceeded` | receipt is assigned only after a successful response | receipt creation only after compilation | `ReconcileLifecycle.tla`, `explorer_reconcile_scope_test.go` |
| `Inv_SuccessReceiptMatchesCurrent` | generation, explorer, workspace fingerprint, receipt snapshot and output | receipt is compiled from the validated request | `reconcileRecovery.unit.test.ts`, selected-output Go test |
| `Inv_DraftChangeInvalidatesReceipt` | reducer clears the receipt on authored change | next reconcile requires a new receipt | `builderAuthoringReducer` tests |
| `Inv_FailureHasNoReceipt` | failure paths never assign a receipt | server returns no receipt on failure | `reconcileRecovery.unit.test.ts` |

The UI cannot re-check the receipt draft version and digest because `ExplorerBuilderCompileResult` does not carry them. The server validates the request draft identity and the Go contract test pins that behavior. The model keeps the server-side binding as an assumption and the UI guard covers the observable fields.

## Remaining trace work

`ReconcileLifecycle` is a required trace model. The formal reconcile schedule covers success, stale refresh and retry, conflict, invalid output, retryable failure, fatal failure, identity rejection, and late result rejection. `verify-builder-traces.mjs` maps reconcile observations to model state. The route proof is recorded in `route-contract.json` and the chain is defined in [Route proof chain](ROUTE_PROOF_CHAIN.md).
