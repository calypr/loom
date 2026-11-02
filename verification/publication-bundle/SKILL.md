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
| `InvariantNoDropUnderPointer` | never drop the pointer-advertised bundle (`:840`) | none needed | **violates by design today — this is the confirmed F-B1 finding; a PASS would be suspicious** |
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

Sanctioned red artifacts (they fail ON PURPOSE as findings; keep them red,
do not "fix" them):

```
go test -count=1 -run 'TestReconcile(Drops|Skips)' ./internal/dataframe/publication/clickhouse/
```

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

- F-B1 fix authorization (pre-drop lease re-validation / fresh GetPointer gate)
  — operator decision, never apply without approval.
- F-B2 (abort-after-loose-publish-ACK drop) — see FINDINGS.md for reproduction
  status.
- Findings, coverage ledger, and unverified scope live next to this file:
  `FINDINGS.md`, `reviewed-sources.json`.

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

Covered code (sync commit `ebf97a40`, re-synced from `0dd7be44` — all seven
regions verified byte-identical, shift-only, via full-slice hash match; the
`cd39eeaf`+`ebf97a40` edits sit entirely above line ~286 in each file: S-A
per-class discovery/admission emission and the unpivot date-family refusal,
neither touched by this model):
`preserveConstructionOwnedInputs` internal/explorer/lifecycle/
construction_pivot_source.go:716-746 (call sites pivot :463, derive :254,
unpivot :322); `constructionOwnerInputsClearedFromCandidate` :757-783;
guarded set-branch imports construction_pivot_source.go:533-537 and
filter_source.go:263-267; frozen SET copies derive_source.go:320-323 and
unpivot_source.go:388-391 (SET reattribution derive_source.go:298-311);
consumption validation authoringv2/
construction_validation.go:348-376; cascade prune authoringv2/
construction_change.go:525-530. Bounds: 4 lanes x 4 carries, at most one
propose per lane; client-carried items are client intent (A1); candidate
build per request is atomic at the durable layer (A2); assumption A3 — frozen
copies persist only consumed imports (reviewer evidence, sideFinding
row-source-derive-SET-bulkcopy-unmatched-guard), falsifiable by the
m-prep-persist-unconsumed arm.

```
TLC_WORKERS=2 TLC_HEAP_MB=1024 /sandbox/.local/bin/tlc-run -fp 21 \
  -metadir /tmp/tla-<run-id> -config <cfg> ConstructionPrepareLanes.tla
```

| cfg | shape | expected |
| --- | --- | --- |
| `m-prep-gated` | shipped (all fault constants FALSE) | clean (61,456 states recorded) |
| `m-prep-helper-ungated` | pre-b72a1322 helper (unconditional restore) | `ClearedStaysCleared` VIOLATED — archived P0 arm (clear blocked by consumption-validation error = user cannot clear) |
| `m-prep-setbranch-ungated` | pivot/filter set imports un-gated | `ClearedStaysCleared` VIOLATED (rs05 removed shape) |
| `m-prep-persist-unconsumed` | A3 dropped on frozen copies | `ClearedStaysCleared` VIOLATED (persisted resurrection) |
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
422, date-column-survives-string-melt). Revisit only if a model gains an
admission-set property.

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
unconsumed projection (the branch reject strings have zero Go assertions, so
this is the reject-direction coverage).

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
(probed by TestPivotEditCanDropOwnedRootProjectionAcrossPrefix in Go); nil
payloads subsume into a false consumed bit.

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
Model counterexample classes map: f-orphan/f-drops ↔ those named tests
(accept-side + removal-side assertions exist, RED-capable); the branch arms
map to no Go test (zero assertions on the reject strings — reject direction
is model-side only), and the used-only drop (owner alive, consumers gone)
likewise has no standalone Go assertion. Not covered: legacy unowned
group-source path, carry-prefix loop, cross-step same-column projections,
multi-projection owners, HTTP/persist surfaces.
