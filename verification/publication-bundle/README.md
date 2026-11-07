# Publication-bundle verification directory

Entry point: **`SKILL.md`** — read it first; it carries the run commands, source
pins, and the model/fault matrix. This README is only a map of what is in here
and which of it is live versus historical.

## Live checks

- Go preservation gate (no external services needed):

  ```
  go test -count=1 -run 'TestReconcile(Drops|Skips)' ./internal/dataframe/publication/clickhouse/
  ```

  All three named tests PASS, including
  `TestReconcileDropsJustPublishedBundleStagesLeaseLoss` — the converted F-B1
  reconcile-TOCTOU reproduction. Re-confirmed 2026-11-07 in this checkout
  (`ok github.com/calypr/loom/internal/dataframe/publication/clickhouse 0.003s`).
- Guard under test: the post-claim pointer re-read plus active-release binding
  re-read in `reconcileExecution`
  (`internal/dataframe/publication/clickhouse/bundle_store.go:1178-1198`,
  `Repair-1b (F-B12 drop-path)`), with the matching veto on the Abort drop path
  (`:899-909`). Deleting only that post-claim re-read makes the named test fail
  on table loss, so its PASS is not vacuous. Do not remove or weaken the guard;
  a red here is a product regression, never a finding to be left in place.

## Historical material (keep, do not restore or re-pin)

| Path | What it is |
| --- | --- |
| `BundleCommitLifecycle.tla` + `.cfg`, `m-can-*.cfg` | Canonical model of the **pre-Repair-1b** commit/lease/pointer/reconcile shape. Its `InvariantNoDropUnderPointer` violation is the archived F-B1 witness — evidence of a bug the shipped code no longer has. |
| `ReconcileDangleTrust.tla`, `PointerOrphanLifecycle.tla`, `ReleaseBindingTrust.tla`, `ReleaseRevisionIdempotence.tla`, `ActiveGenerationTrust.tla`, `PublishPartialPointer.tla`, `GenerationActivationCas.tla`, `StartupReconcileWedge.tla`, `NullPageCoverage.tla`, `ConstructionPrepareLanes.tla`, `ConstructionProjectionOwnership.tla` + their `m-*.cfg` | Bounded models and their fault/overlay arms. Each clean arm is falsified by its counterpart arm; see `SKILL.md` for per-arm expectations. |
| `FINDINGS.md` | Round-by-round findings, coverage ledger, and recovery provenance. Verdicts are as-recorded history; re-derive current status from source and tests. |
| `reviewed-sources.json` | Machine ledger: source/model pins and per-model verdicts. Refresh a digest only by re-deriving live bytes. |
| `OPERATOR_REVIEW_DIGEST.md`, `PUBLISHED_IMMORTALITY_PROPOSAL.md` | Operator-facing Phase B adversarial digest, and a verification-only invariant proposal. Both are proposals/records at their pinned commits, not statements of shipped behavior. |
| `evidence/` | Saved raw witnesses and run logs behind the ledger rows. Immutable provenance. |

## Limits of this directory's assurance

No TLC run here covers the shipped post-claim guard: `BundleCommitLifecycle.tla`
has no constant for it, so a clean or violating run of that model says nothing
about Repair-1b either way. Current assurance for the guard is the passing Go
regression above plus its overlay non-vacuity. A post-claim-recheck model arm is
unstarted work — record a log before claiming model coverage. Repairing F-B1
also says nothing about legacy-key upgrade safety: R1 is a separate open defect.
