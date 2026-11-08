---
name: verify-publication-bundle
description: Regression + formal verification entry point for the ClickHouse publication-bundle lease/pointer/reconcile protocol (bundle_store.go, arango/bundle_registry.go). Use when touching publish/commit/abort/reconcile/cleanup-pin logic, lease TTLs, or the Arango publication documents.
---

# Verify publication bundle

Covers `internal/dataframe/publication/clickhouse/bundle_store.go`
(Begin/Commit/ConfirmCommit/Abort/Reconcile) and
`internal/dataframe/publication/arango/bundle_registry.go` (SaveExecution
fence, single-AQL PublishExecution, lease acquire/renew/release, execution read
pins, cleanup claim). Read this file first if resuming that work.

## Source identity

Pin hashes before and after every experiment; a mismatch invalidates results:

```
sha256sum internal/dataframe/publication/clickhouse/bundle_store.go \
          internal/dataframe/publication/arango/bundle_registry.go
```

Last verified: `a5db5f55...69254` / `c0da93a2...ffd7b` at HEAD `218edabb`
(maintenance pass 2026-11-02; prior pins `2f5c0fda...`/`1f0a6cde...` were the
pre-`8160bb97` shape — the Repair-1b/4 wave changed both files and the pins
were refreshed in this pass).

Re-verified 2026-11-07: both digests still match the live bytes in this
checkout, so the pins above stand (no re-pin — the values did not change) and
the Repair-1b post-claim guard is present in `bundle_store.go`.

## Model checks (TLA+)

Canonical model: `BundleCommitLifecycle.tla` (+`.cfg`). Requires
`$TLA2TOOLS_JAR` (tla2tools.jar sha256 `936a2620...`, per
`verification/builder-interactions/tools.lock.json`). Run foreground with a
private `-metadir`:

```
TLC_WORKERS=4 TLC_HEAP_MB=2048 /sandbox/.local/bin/tlc-run -fp 21 -fpbits 6 \
  -metadir /tmp/tla-pb-$$ -config <cfg> BundleCommitLifecycle.tla
```

Properties and their fault controls (each fault constant is TRUE in a scratch
overlay config outside Git; run each separately):

| Invariant | Meaning | Fault constant | Expected |
| --- | --- | --- | --- |
| `InvariantNoDropUnderPointer` | never drop the pointer-advertised bundle (`:840`) | none needed | **violated only in the historical pre-Repair-1b model — see "F-B1: current state vs historical evidence"; the shipped code has the guard and its Go regression passes** |
| `InvariantNoDropWhenNamed` (ghost.m) | drop even though the memoised pointer read (`:1068`, `rec.ptr`/`rec.rd`) named it | `FReconcileSkipsPointerCheck = TRUE` | clean baseline, violated under fault |
| `InvariantNoDropUnderPin` (ghost.p) | drop while a reader pin is live at the drop instant (`claimExecutionCleanup :416-418`) | `FClaimIgnoresPins = TRUE` | clean baseline, violated under fault |
| `InvariantNoForeignPublish` | publish only under a live caller lease (`:289-290`) | `FNoLeaseFencePublish = TRUE` | clean baseline, violated under fault |

Model-editing rules (learned the hard way — see FINDINGS.md postmortem):
- every action must assign or UNCHANGED-declare every VARIABLE, including
  `cleanupExpires` and `ghost`; otherwise TLC silently never fires it;
- never write in-action branches as leading-`\/`/`/\` column groups; use
  `IF ... THEN ... ELSE ...`;
- deep EXCEPT must be `[rec.x EXCEPT !.y = [rec.x.y EXCEPT ![t] = v]]`;
- reconciler snapshot fields live in one `rec` record (`cur`, `cand`, `ptr`,
  `rd`); claim requires `rec.rd[t]` (read-before-claim order per `:1068`→`:1076`).

## F-B1: current state vs historical evidence

F-B1 (reconcile TOCTOU: `reconcileExecution` deleting the physical tables of a
bundle the visibility pointer advertises as live) is **repaired**. Read this
section before treating any F-B1 artifact as an open finding.

Current, live guarantees (re-verified 2026-11-07 in this checkout):

- Production guard: `reconcileExecution` re-reads the pointer after
  `claimExecutionCleanup` and before any `DropTable`
  (`internal/dataframe/publication/clickhouse/bundle_store.go:1178-1184`,
  `Repair-1b (F-B12 drop-path)` comment at `:1168-1177`; the active-release
  binding re-read follows at `:1185-1198`). The Abort drop path carries the
  same post-claim veto (`bundle_store.go:899-909`). A read failure other than
  "no pointer" / "no active release" vetoes the drop fail-closed.
- Named Go regression: `TestReconcileDropsJustPublishedBundleStagesLeaseLoss`
  in `internal/dataframe/publication/clickhouse/reconcile_toctou_test.go`
  **PASSES**. It drives a dead lease plus a publish racing in after the
  pre-claim `GetPointer`, and asserts the drop is denied and the pointer-named
  tables survive. Together with
  `TestReconcileSkipsPointerNamedExecution` and
  `TestReconcileDropsLongStaleUnpublishedExecution` it is a preservation gate
  for the guard, not a finding.
- Non-vacuity (go-test `-overlay`, a throwaway probe rather than a committed
  artifact): deleting only the post-claim pointer re-read at `:1178-1184`
  makes `TestReconcileDropsJustPublishedBundleStagesLeaseLoss` FAIL on table
  loss with the archived F-B1 wording, "deleted the physical tables of a
  bundle the pointer advertises as live". The PASS therefore exercises the
  guard instead of passing vacuously.

Historical, kept for provenance only — do not restore, re-pin, or delete:

- `BundleCommitLifecycle.tla` models the **pre-Repair-1b** shape: the
  reconciler reads the pointer once (`ReconcilePointerRead`), memoises it in
  `rec.ptr`, and neither `ReconcileClaim` nor `ReconcileDrop` re-validates live
  state. Under that model `InvariantNoDropUnderPointer` is genuinely VIOLATED.
  That violation is the archived F-B1 witness — evidence of the bug the shipped
  code no longer has, not a desired current failure and not a bug in the model.
- The model header's line "ALL-FALSE describes the current code" is itself
  historical: with all fault constants FALSE the model still lacks the
  post-claim pointer/binding re-read `reconcileExecution` performs today, so
  the no-fault shape describes the pre-Repair-1b code, not `bundle_store.go`
  as shipped. The fault constants remain exactly what they say — single
  omissions used as falsifiability controls — not a catalogue of live defects.
- The archived witnesses, coverage ledger rows and recovery provenance
  (`FINDINGS.md`, `reviewed-sources.json`, `evidence/`) stay as written. Their
  model digests are historical pins; refresh one only by re-deriving live
  bytes, never by hand. As of this guidance pass the canonical model is
  byte-identical to those pins (`md5sum BundleCommitLifecycle.tla` =
  `14d341f86d6f4f072a585434334dfc7a`, matching `reviewed-sources.json`
  `canonicalProbe.expectedMD5`), so this correction changed no model, witness
  or ledger bytes.

What the model does NOT currently prove, stated plainly: no TLC run in this
checkout covers the shipped post-claim guard. `BundleCommitLifecycle.tla` has
no constant for it, so a clean baseline there says nothing about Repair-1b, and
a violation there is the historical shape rather than a live defect. Formal
assurance for the guard is the Go regression plus the overlay non-vacuity
above. Extending the model with a post-claim-recheck arm (compare
`ReconcileDangleTrust.tla`'s `m-dangle-RecheckBeforeDrop.cfg`) is unstarted
work; do not report model coverage of the guard without running it and
recording the log.

Repairing F-B1 also grants **no** judgment on legacy-written bundle documents:
the separate, still-open mis-selection concern for docs carrying capitalized
legacy field names (lowercase-only sort/Go checks; recorded as the CONFIRMED
"legacy case asymmetry" Bug-3 under F-B6 and the F-B9 candidate in
`FINDINGS.md`) is neither repaired nor cleared by Repair-1b.

## TLC-bound trio entry points (last synced 218edabb)

`PointerOrphanLifecycle`, `ReleaseBindingTrust`, `ReleaseRevisionIdempotence`
have no harness entry point; run them directly from this directory, one
`-metadir` per run. Wave F (218edabb) shipped these models comment-maintained
but NEVER TLC-run; this pass ran every arm — all verdicts below matched
verbatim. Bounds are the cfg constants (MaxExecs=3, MaxStamps=4, Exec={e1,e2});
do not enlarge silently.

```
TLC_WORKERS=2 TLC_HEAP_MB=1024 /sandbox/.local/bin/tlc-run -fp 21 \
  -metadir /tmp/tla-<run-id> -config <cfg> <Model>
```

| Model | cfg | Expected |
| --- | --- | --- |
| PointerOrphanLifecycle | `m-po-fixed` (= shipped shape: janitor present, checks bindings, post-claim recheck) | clean |
| PointerOrphanLifecycle | `m-po-bug` | OrphanGC temporal **VIOLATED** (archived F-B19 witness) |
| PointerOrphanLifecycle | `m-po-janitor-blind` | NoBoundDeleted **VIOLATED** |
| ReleaseBindingTrust | `m-rebind-holds`, `m-rebind-caseholds` (Recheck=TRUE) | clean |
| ReleaseBindingTrust | `m-rebind-violation`, `m-rebind-case` (Recheck=FALSE) | CorrectBinding **VIOLATED** (F-B6-class witness pair) |
| ReleaseRevisionIdempotence | `m-rri-fixed` (content-addressed ID = shipped shape) | clean |
| ReleaseRevisionIdempotence | `m-rri-shipped` (revision-folding ID) | Idempotence **VIOLATED** (archived F-B15 churn control) |

The violated archived arms are the falsifiability control for each clean arm:
if a clean arm ever passes while its counterpart also passes, the checked
broke.

## Component + real-DB reproductions

Keep-them-red findings (they fail ON PURPOSE; do not "fix" them): F-B3
(pinblock, export-pin crash window), F-B6 (name-latest binding), F-B7
(dangling binding), F-B13 (cross-generation publish), F-B14 (release
requirements), F-B17 (generation-activation CAS), F-B19/F-B21 arms as listed
in `FINDINGS.md`.

The F-B1 reconcile TOCTOU trio is NOT one of them any more — Repair-1b
shipped and this command is a green preservation gate:

```
go test -count=1 -run 'TestReconcile(Drops|Skips)' ./internal/dataframe/publication/clickhouse/
```

Expected 2026-11-07: `TestReconcileDropsJustPublishedBundleStagesLeaseLoss`,
`TestReconcileSkipsPointerNamedExecution` and
`TestReconcileDropsLongStaleUnpublishedExecution` all PASS. A failure means
the post-claim guard in `reconcileExecution` was weakened or removed — restore
the guard; converting the trio back to red is a regression, never a finding.

End-to-end on the real Arango of the owned stack (pinned container
`loom-dev-qwen-current-loom-api-1`; `/workspace` is this checkout; only
`sh -c 'cd /workspace && ...'` exec shape is permitted):

```
docker exec loom-dev-qwen-current-loom-api-1 sh -c \
  'cd /workspace && LOOM_TEST_ARANGO_URL=http://arangodb:8529 go test -count=1 \
   -run "Test(Reconcile|Abort).*AgainstArango" -v ./internal/dataframe/publication/clickhouse/'
```

Tests skip when `LOOM_TEST_ARANGO_URL` is unset. Every e2e owns a UUID database
`loom_verify_*` and must drop it via HTTP `DELETE /_api/database/<name>` in
`t.Cleanup`. After any e2e run, confirm stack residue-free:

```
docker exec loom-dev-qwen-current-loom-api-1 sh -c \
  'curl -s http://arangodb:8529/_api/database | grep -o "loom_verify_[a-z0-9_-]*"'
```

(must print nothing).

## Known open operator items

- F-B1 needs nothing here: the pre-drop pointer re-validation / fresh
  `GetPointer` gate shipped as Repair-1b and is covered by the passing
  regression in "F-B1: current state vs historical evidence". Do not remove or
  weaken it while clearing other operator items.
- F-B2 (abort-after-loose-publish-ACK drop) — see FINDINGS.md for reproduction
  status.
- Findings, coverage ledger, and unverified scope live next to this file:
  `FINDINGS.md`, `reviewed-sources.json`. The rest of the historical record is
  `OPERATOR_REVIEW_DIGEST.md` (Phase B adversarial verdicts at `dc9016bc`),
  `PUBLISHED_IMMORTALITY_PROPOSAL.md` (verification-only proposal, no code
  changed), and the saved witnesses under `evidence/`. All four are provenance
  for findings as they stood when recorded; read them as history, and re-derive
  current status from source, tests, and this file.
- This guidance correction edits `SKILL.md` and adds `README.md`, and those
  edits intentionally leave the ledger incomplete: `files["verification/
  publication-bundle/SKILL.md"].sha256` in `reviewed-sources.json` still pins
  the pre-correction bytes (`2ee4c56c...` @`49d6999e`), and `README.md` has no
  ledger entry at all. That is a guidance-doc lag, not model or product drift —
  the canonical model digest is unaffected. In the ledger-owner pass, re-derive
  the SKILL.md pin from live bytes and register `README.md` for a new
  live-bytes pin; never hand-type a digest, and do not read the stale or
  missing value as a reason to touch the model, witnesses, or the guard.

Local TLC runs use `/sandbox/.local/bin/tlc-run` (explicit budget; split via `TLC_WORKERS`/`TLC_HEAP_MB`, one `-metadir` per run). If unreadable `/sys/fs/cgroup/{cpu,memory}.max` crashes the launcher, it needs the OSError guards (applied 2026-11-01).

## Prepare-lane clearance model (ConstructionPrepareLanes.tla)

Bounded quantifier for the ROW-SOURCE-05 P0 contract: "row-operation
preparation never restores inputs another lane cleared" (authoritative
sentence, commit b72a1322). Consumer of record: the 20 named durables
`internal/explorer/lifecycle/filter_source_pivot_clear_test.go` are empirical
samples; this model is the only check quantifying base x lane-propose order x
per-lane {Untouched, Omitted, Cleared, Replaced} carries at every post-prepare
durable state. Properties (plain-words): `ClearedStaysCleared` — a lane the
candidate cleared persists with no server inputs, however the proposals are
ordered; `NoFalseRestore` — only a client Replaced create owns a lane;
`UntouchedOwnerSurvival` — the fix never eats preservation (partial carries
are repaired); `OwnerIsolation` — clearing A never writes B.

Covered code (sync commit `8459f080`, re-synced from `ebf97a40` — five of the
seven regions verified byte-identical shift-only via full-slice hash match; the
two frozen-copy loops DRIFTED at `8459f080` (C1 SET-repair): each gained the
`!constructionOwnerInputsClearedFromCandidate(...)` clause, so they are now
guarded, not unguarded. The `d3a1d12a`/`29d63f6f` (S-B/S-C) edits are confined
to the capability/discovery + resolve paths — `rowSourceInventoryFailure`
(construction_pivot_source.go:205) and `constructionFieldDisplayLabel`
(construction_pivot_source.go:474) — outside every region below):
`preserveConstructionOwnedInputs` internal/explorer/lifecycle/
construction_pivot_source.go:763-793 (call sites pivot :510, derive :257,
unpivot :325); `constructionOwnerInputsClearedFromCandidate` :804-830;
guarded set-branch imports construction_pivot_source.go:580-584 and
filter_source.go:266-270; frozen SET copies derive_source.go:323-328 and
unpivot_source.go:391-396, both guarded on the cleared predicate since
`8459f080` (SET reattribution derive_source.go:301-314);
consumption validation authoringv2/
construction_validation.go:348-376; cascade prune authoringv2/
construction_change.go:525-530. Bounds: 4 lanes x 4 carries, at most one
propose per lane; client-carried items are client intent (A1); candidate
build per request is atomic at the durable layer (A2); assumption A3 — frozen
copies persist only consumed imports (reviewer evidence, sideFinding
row-source-derive-SET-bulkcopy-unmatched-guard), falsifiable by the
m-prep-persist-unconsumed arm, which is now the pre-`8459f080` C1 shape
(frozen copies ignore the cleared clause and the foreign re-import persists —
durables filter_source_pivot_clear_test.go @`8459f080`). Before `8459f080` the
clean arm passed on a description the code did not match: A3 was false at the
frozen sites, so the r64 IN SYNC for this model was a stale pass.

```
TLC_WORKERS=2 TLC_HEAP_MB=1024 /sandbox/.local/bin/tlc-run -fp 21 \
  -metadir /tmp/tla-<run-id> -config <cfg> ConstructionPrepareLanes.tla
```

| cfg | shape | expected |
| --- | --- | --- |
| `m-prep-gated` | shipped (all fault constants FALSE) | clean (61,456 states recorded) |
| `m-prep-helper-ungated` | pre-b72a1322 helper (unconditional restore) | `ClearedStaysCleared` VIOLATED — archived P0 arm (clear blocked by consumption-validation error = user cannot clear) |
| `m-prep-setbranch-ungated` | pivot/filter set imports un-gated | `ClearedStaysCleared` VIOLATED (rs05 removed shape) |
| `m-prep-persist-unconsumed` | pre-`8459f080` C1 shape: derive/unpivot frozen copies ignore the cleared clause and the foreign re-import persists | `ClearedStaysCleared` VIOLATED (persisted resurrection; the C1-revert falsifier) |
| `m-prep-shared-gate` | global cleared flag (over-gated wrong-fix) | `UntouchedOwnerSurvival` VIOLATED; `OwnerIsolation` independently violated (probed) |

A clean arm passing while its counterpart also passes means the check broke
(skill doctrine). Companion real-code replay lane (keep red artifacts red):
`go test -count=1 -run 'TestPivotSource|TestFilterSourceClear|TestFilterSourceCreate|TestProposeConstruction|TestXopPort' ./internal/explorer/lifecycle/`
— model counterexample classes map 1:1 to those named tests, which are the
proven RED-on-pre-fix/GREEN-after shapes (b72a1322 commit evidence: 11 RED +
reviewer kill-set m1/m2/m3/H1/H2/S1/S2). Model runs read with pstack
`/statechart`. Not covered: multi-owner steps per lane kind, partial chain
granularity below carry classes, request-failure HTTP surfaces, the reconcile
receipt guard itself, database atomicity, and the unpivot melt admission set —
the cd39eeaf date-family refusal (`unpivotSourceLogicalTypeIsMeltEligible`
table-scalar-minus-date, internal/explorer/lifecycle/unpivot_source.go:82-130)
is deliberately unmodeled: no model enumerates admitted logical types
(`unpivotSourceLogicalTypeIsTableScalar` and family bits appear in no .tla),
and the refusal is covered by durable Go tests (cd39eeaf:
TestUnpivotSourceMeltEligibilityExcludesDateFamily,
TestConstructionUnpivotSourceRefusesDateFamilyOnAdmission (422
`UNSUPPORTED_UNPIVOT_SOURCE_DATE_FIELD`),
TestConstructionUnpivotSourceCapabilityExcludesDateMeltKind in
unpivot_source_test.go; api_explorer_construction_unpivot_source_test.go)
plus the accepted 30-check unpivot row-source journey date leg
 (docs/BUILDER_VERIFICATION.tsv registry row explorer-unpivot-row-source,
 runs upr17/upr18: discovery-hides-date-family, awaited signed date_time melt
 422, date-column-survives-string-melt). Also unmodeled: the `d3a1d12a` S-B
 row-choice-inventory verdict (`rowSourceInventoryFailure`,
 construction_pivot_source.go:205 — payload `*_SOURCE_INVENTORY_UNAVAILABLE`
 + fail-closed typed error) and the `29d63f6f` S-C label builder
 (`constructionFieldDisplayLabel`, construction_pivot_source.go:474 —
 first non-blank control-free display candidate, else legacy
 `ResourceType.path`). Both sit on the capability/advertisement and resolve
 surfaces, not on the durable server-owned-input state this model quantifies;
 no model enumerates reason codes or label bytes. They are covered by the
 lane durables in derive/filter/pivot/unpivot_source_test.go @`d3a1d12a`/
 `29d63f6f` and the openapi HTTP twins. Revisit only if a model gains an
 admission-set or property-payload predicate.

## Projection-owner survival model (ConstructionProjectionOwnership.tla)

Bounded quantifier for the FORMAL-62-U1 promise: "a construction source
projection is kept in the shipped proposal iff its owner-step survived
prune as an owner kind AND its column is consumed by a surviving step".
Consumers of record: release/apply reviewers + projection-regression triage;
this model is the only check quantifying per-owner
{stepPresent, projPresent, consumed, crossUsed} jointly at prune and at the
validate owner-kind branch. Properties: `KeepIffOwnerSurvived` — both
directions of the kept rule (orphan-ship and legit-drop both fire);
`ShippedRequiresConsumption` — nothing persisted survives validate with an
unconsumed projection. Since Round 66 the branch reject strings additionally
have durable Go + Node contract assertions (projection-owner-branch replay
lane below); the model remains the only check quantifying prune-and-validate
jointly over the full 2^20 input product.

Covered code (sync commit `29d2c5b0`; advance ONLY after re-reading these
exact regions): owners registry + kept rule `pruneConstructionSourceProjections`
internal/explorer/authoringv2/construction_change.go:524-542 (callers
:225/:374); consumption sets `Operation.inputColumnIDs` :839-885; validation
switch validatePivotSourceProjection authoringv2/construction_validation.go:339-395
(pivot/group :349-352, filter :353-356, derive :357-360, unpivot :361-371,
default :372-374, owner-exists :343-346). Bounds: owners
{pivot,filter,derive,unpivot,sort}; "group" shares the pivot/group label and
the unowned-legacy path is out of bound; one projection per owner, distinct
columns; owner at step index 0 so the carry-prefix loop :378-393 is unmodeled
(probed in Go by TestPivotEditCanDropOwnedRootProjectionAcrossPrefix and,
since Round 66, by the replay-lane branches carry-cross-noncarrier and
carry-missing-typed-output); nil payloads subsume into a false consumed bit.

```
TLC_WORKERS=4 TLC_HEAP_MB=2048 /sandbox/.local/bin/tlc-run \
  -metadir /tmp/tla-<run-id> -config <cfg> ConstructionProjectionOwnership.tla
```

Do NOT add `-fp 21 -fpbits 6` (house flag for <10^4-state models): at 3.5M
states it collapses the graph (observed 34 distinct states + fingerprint
collision warning). Quantify choices at the action level; \E on an EXCEPT
right-hand side does not branch under this build.

| cfg | shape | expected |
| --- | --- | --- |
| `m-prj-clean` | shipped (all fault constants FALSE) | clean (3,517,009 distinct states recorded) |
| `m-prj-f-drops` | prune drops a projection whose owner survived and is used | `KeepIffOwnerSurvived` VIOLATED |
| `m-prj-f-orphan` | kept rule ignores owners[] registry | `KeepIffOwnerSurvived` VIOLATED |
| `m-prj-f-noused` | kept rule ignores used[] set | `KeepIffOwnerSurvived` VIOLATED |
| `m-prj-f-pivot` | pivot/group branch vacuous | `ShippedRequiresConsumption` VIOLATED |
| `m-prj-f-filter` | filter branch vacuous | `ShippedRequiresConsumption` VIOLATED |
| `m-prj-f-derive` | derive branch vacuous | `ShippedRequiresConsumption` VIOLATED |
| `m-prj-f-unpivot` | unpivot branch vacuous | `ShippedRequiresConsumption` VIOLATED |
| `m-prj-f-default-orphan` | default branch vacuous (defense-in-depth: needs registry fault to reach) | `ShippedRequiresConsumption` VIOLATED |

Companion real-code lane (kept/drop directions):
`go test -count=1 -run 'TestScalarGroupSourceProjectionRoundTripsAndPrunesOnRemoval|TestPivotOwnedInputsStayPrivateAndAreRemovedWithPivot|TestCompoundSourceGroupingOwnsAndPrunesItsInputs' ./internal/explorer/authoringv2/`

### Projection-owner-branch replay lane (FORMAL-62-U1 replay closure, Round 66)

Replay families for the owner-branch arms (unpivot melt-membership, nil
payloads, filter/derive/pivot consumption, owner dispatch, both carry-prefix
modes) that the model bounds excluded. Adapter
`internal/explorer/authoringv2/construction_projection_replay_test.go`
(16 branches; smoke channel self-asserts, batch channel emits observations);
lane `scripts/lib/formal-projection-replay.mjs` drives the batch channel,
mirrors the expectation table, and runs nine source-fault falsification pairs
(each guard neutralized ⇒ its named branch diverges — flipped acceptance,
lost reject string, or a crashing validator — with positive and restored runs
green); consumer contract `scripts/verify-ui/contracts/source-projection-contracts.mjs`
fails with the branch named when a reject string is renamed, removed, or
swapped, and self-validates against tampered observations.

```
node scripts/lib/formal-projection-replay.mjs --generate /tmp/projection-lane-<run-id> --self-test
```

Expected: `branches 16, accepted 5, rejected 11`; nine `sourceControls`
entries each `positive && negative && restored` with `firedBranch == branch`.
Contract channel: `node --test scripts/verify-ui/contracts/source-projection-contracts.test.mjs`
(4 tests). Boundary: accepts and unguarded rejects run the public
Construction.Validate; nil-payload and non-owner-switch isolations pin exact
strings through the direct validator entry because earlier structural
validators fire first (disclosed in the adapter header). The chain lane
(ConstructionChain) hash-set is unaffected by these files.
Model counterexample classes map: f-orphan/f-drops ↔ those named tests
(accept-side + removal-side assertions exist, RED-capable). Since Round 66
the branch-arm reject strings carry durable Go + Node contract assertions and
the two carry modes have lane fault proofs; the used-only drop (owner alive,
consumers gone) still has no standalone Go assertion. Not model-covered:
legacy unowned group-source path, carry-prefix loop (reject-direction replay
cover exists via the carry-* fault arms, no model state), cross-step
same-column projections, multi-projection owners, HTTP/persist surfaces.
