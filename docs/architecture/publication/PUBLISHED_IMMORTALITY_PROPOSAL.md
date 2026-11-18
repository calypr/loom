# PUBLISHED immortality invariant — statement, danger surface, enforcement tiers

Verification-only proposal. No product code changed; this file is the sole
artifact. It states the invariant that F-B8 leaves unasserted, enumerates every
current code path that could break it, and orders the cheapest enforcements.

Source identity: HEAD `dc9016bc`. `clickhouse/bundle_store.go` sha256
`2f5c0fda6a154dcb20dbc93f6e2f4550ca141202e33a3660b467907951d5d6a2`,
`arango/bundle_registry.go` sha256
`1f0a6cde72895f534070520c4472a818f0237b64adadd96493afba938c1de138` (both equal
the FINDINGS.md canonical pins; `published/current.go` sha256
`9a0f2f16be6821e1650cc39c90fa478e6b4125101760027b68d1d9616ca76426`). Every
`file:line` below was read in this session against that snapshot.

## 1. The invariant

Let:

- `E` = docs in `loom_dataframe_bundle_executions` (`bundle_registry.go:18`).
  `e._key` is the ExecutionID; `e.state ∈ {RUNNING,…,PUBLISHED,READY,…}`
  (`bundle.go:23-48`); `e.outputs[o].physicalTable` names one ClickHouse table;
  `e.outputs[o]` is **queryable** iff `state.Successful() ∧ verifiedAt≠nil ∧
  physicalTable≠""` (`bundle.go:147-148`).
- `P` = pointer docs in `loom_dataframe_bundle_pointers` (`bundle_registry.go:19`);
  `p.executionId` is the single execution the visibility reader advertises.
- `R_p` = the `project_release` doc reached by the `active_project_release`
  pointer for project `p` (`release_store.go:144-149`); each
  `R_p.publications[i]` carries `executionId`, `selector`, `generation`,
  `stale`, `required` (`release.go:30-35`). A binding is **live** iff
  `¬stale ∧ generation = R_p.generation` — the exact skip predicate the read
  path applies (`current.go:203-204`).
- `T` = set of ClickHouse physical tables that currently exist.

Derived predicates:

```
Published(e)      ≡ e.state ∈ {"PUBLISHED","READY"}              // bundle.go:27,34,43-44
Advertised(e)     ≡ ∃ p∈P : p.executionId = e._key               // pointer names e
Bound(e)          ≡ ∃ p, ∀ b∈R_p.publications :
                      b.executionId = e._key ∧ ¬b.stale
                      ∧ b.generation = R_p.generation             // current.go:202-205
QueryableOf(e,o)  ≡ o.state∈{"PUBLISHED","READY"} ∧ o.verifiedAt≠nil ∧ o.physicalTable≠""
DocPresent(e)     ≡ e._key ∈ dom(E)                               // GetExecution ≠ ErrBundleNotFound
```

**Invariant (published-immortality / release-binding integrity):**

```
□ ∀ e∈E : Published(e) ∧ ( Advertsed(e) ∨ Bound(e) )  ⇒
      DocPresent(e)
  ∧   ∀ o∈e.outputs : QueryableOf(e,o) ⇒ o.physicalTable ∈ T
```

Two halves, both required:

- **I1 doc-immortality** — a PUBLISHED execution that is pointer-advertised
  *or* release-bound is never removed from `E`. Its breach is the F-B7
  `bundle execution not found` branch (`current.go:95-97`).
- **I2 table-immortality** — the queryable physical table of such an execution
  is never dropped. Its breach is the F-B7 `no queryable output` /
  `does not match release` branch (`current.go:99-110`) and the F-B1 dangling
  read.

The `Bound(e)` disjunct is the whole point. The existing formal guard
`BundleCommitLifecycle.tla:435 InvariantNoDropUnderPointer` uses `pointer` only,
and the reconciler's runtime skip (`bundle_store.go:1073`) tests only
`pointer.ExecutionID == e._key`. Neither covers a PUBLISHED execution that is
**superseded in the pointer yet still bound by an active release** — precisely
the set a release binds and the viewer resolves with no pointer re-check
(`current.go:94-96`, `GetExecution` by ID). So today's protection for the
release-bound set comes *only* from enumeration scoping (a PUBLISHED doc is
never offered to the dropper at all), not from any predicate at the chokepoint.
That is the unasserted invariant F-B8 names.

Corollary worth stating: `Advertised(e) ⇒` (reader resolves it via pointer,
`current.go:121-122/162`) and `Bound(e)` is independent of the pointer, so the
union is strictly larger than either and strictly larger than what any current
guard inspects.

## 2. Every current path that could violate it

Classification: **[SAFE-SCOPE]** = cannot reach the invariant under today's
scoping (safe for a reason this doc names); **[DANGER-EXTEND]** = safe only by a
scoping assumption that a future change routinely drops.

Deletion primitives that touch execution metadata or physical tables, exhaustively
(grep over `DROP TABLE`/`DropTable`/AQL `REMOVE`/`RemoveDocument`/`Delete`/
`Truncate` across `internal/dataframe/publication/**`, `internal/dataset/arango/**`,
`internal/store/clickhouse`, `internal/dataframe/execution/chartifact/**`,
`internal/explorer/**`):

1. **[SAFE-SCOPE] `Registry.ReleaseBundleLease` — `bundle_registry.go:338-340`
   `REMOVE existing IN @@leases`.** The only AQL `REMOVE` in the registry. Its
   `@@collection` is `BundleLeasesCollection` (:20), owner-matched (:339). It
   touches no execution doc, no pointer doc, no table. Note for the record: the
   F-B8 shorthand "`:340` targets READY/FAILED" is inaccurate — `:340` is
   lease-only; **there is no execution-doc or pointer-doc removal primitive in
   product code at all** (item 3).

2. **[SAFE-SCOPE] Pin/cleanup mutations — `removeReadPin`
   `bundle_registry.go:450-466`, `ClaimExecutionCleanup :411-427`,
   `RenewExecutionCleanup :429-444`.** All are `UPDATE … WITH {pins/cleanup}` on
   `loom_dataframe_execution_read_pins`; they never `REMOVE` a state doc and
   never touch `E`/`P`/`T`. (Reconcile never enumerates them at all.)

3. **[SAFE-SCOPE / ABSENT-BY-DESIGN] Execution-doc and pointer-doc removal.**
   No product primitive removes an execution doc or a pointer doc. `REMOVE` on
   `loom_dataframe_bundle_executions` exists **only** in the F-B7 verification
   harness (`dangling_binding_e2e_test.go:379,396`) simulating a vanished
   execution. `CompareAndSwapPointer :244-270` and `PublishExecution :272-310`
   only rewrite `executionId`; the pointer doc itself is never deleted.
   Consequence: **I1's "execution doc vanished" half has no current product
   producer** — it is reachable today only via item 4 (reconciler demotes/prunes
   an output → `no queryable output`, or `State→FAILED` → `does not match
   release`). A *future* janitor that ages out execution docs (the obvious next
   feature after the chartifact janitor) is the item that would first produce a
   literal doc-removal.

4. **[DANGER-EXTEND] `ClickHouseBundleStore.reconcileExecution` DropTable loop —
   `bundle_store.go:1076-1088` (`DropTable` :1084), driven by `Reconcile`
   :1033-1053.** This is the single product site that can destroy the physical
   tables of an execution the metadata still calls live, so it is the invariant's
   primary danger surface. Today it is kept off PUBLISHED/bound executions by
   three scoping assumptions, none asserted at the drop:
   - **state enumeration** `states = {RUNNING, VALIDATING, FAILED}` (:1043) —
     `VisitExecutionPages`/`executionStates` (`bundle_registry.go:78-168`) never
     include PUBLISHED/READY, so a PUBLISHED doc is only reached via a race.
   - **pointer short-circuit** (:1073) — releases are *not* covered by this, only
     pointer-advertised docs (§1).
   - **cleanup claim** `ClaimExecutionCleanup` `bundle_registry.go:414-422`
     checks pins and the cleanup slot only — **no state, no lease, no pointer
     re-validation** (mirrored verbatim by `BundleCommitLifecycle.tla:347
     ReconcileClaim`).
   Consequences:
   - **F-B1 is a live breach of I2**, not a hypothetical: a candidate
     enumerated as RUNNING/VALIDATING/FAILED is published by a racing runner
     between the memoised `GetPointer` (:1068) and the loop (:1084), so a
     just-PUBLISHED execution's tables are dropped while its output rows are
     then pruned and state forced FAILED (:1089-1094). If that row is, or
     shortly becomes, release-bound it is an F-B7 trigger. **[DANGER-EXTEND]** —
     the enumeration snapshot makes it a claim-time TOCTOU that the claim does
     not close.
   - Widening the `states` list to `BundlePublished` (or feeding
     `ListExecutions(BundlePublished, …)` into any cleanup) converts it into a
     direct sweep-and-erase of the bound set. **[DANGER-EXTEND]**
   - Making the sweep **periodic** (a bundle twin of
     `private_artifact_reconciler.go:18-27,30-59`, which today is wired for the
     chartifact store only — `server.go:863`; the bundle store's `Reconcile` has
     exactly one caller, startup `server.go:556`, no retry/background — F-B3)
     reopens the F-B1 window every tick instead of once per boot. **[DANGER-EXTEND]**

5. **[SAFE-SCOPE] `clickHouseBundleTx.Abort` DropTable loop —
   `bundle_store.go:871-882` (`DropTable` :879).** Drops the *in-flight attempt's
   own* outputs, gated by `idempotent || closed` (:862) and `ensureLease` (:865);
   the lost-publish-ACK variant is prevented by `confirmCommit` + `t.closed`
   (`:841-858`, `:813-822`, findings "Second mechanism: PREVENTED"). It never
   names another or an already-committed execution. Dangerous only if its
   own-attempt/commit fencing is weakened. **[SAFE-SCOPE]**

6. **[SAFE-SCOPE] ClickHouse client — `internal/store/clickhouse/client.go:131-135`
   `DROP TABLE IF EXISTS`.** Unconditional once called; its only guard is
   `validateIdentifier` (format). Every safety property lives in the two callers
   above, not here. Not independently dangerous; recorded as the true lowest-level
   chokepoint.

7. **[SAFE-SCOPE but the template] chartifact janitor —
   `internal/dataframe/execution/chartifact` — `Manager.Reconcile` :893,
   `DropTable` :922, `catalog.Delete` REMOVE :250-279, `ClaimCleanup` :204-247,
   `ListExpired` :165-199.** Runs on a 30 s ticker (`private_artifact_reconciler.go:10,22`)
   so it is the *live* periodic janitor precedent in this codebase. Safe today
   because of namespace + a claim-time re-read:
   - Table names are allow-listed to `loom_private_…` (`validTableName` :936-942;
     prefix enforced `New` :172-173) — it cannot name a publication table.
   - `ClaimCleanup` rechecks `existing.leaseUntil <= expiredBefore` in the write
     (:213) — the pre-effect re-validation the bundle reconciler lacks.
   Danger-if-extended: `ListExpired` filters on **`leaseUntil <= now` only, with
   no state filter** (`catalog.go:179-183`). Cloned onto
   `loom_dataframe_bundle_executions`, it would select exactly the PUBLISHED set:
   committed/published executions carry a stale or `nil` `leaseExpiresAt`
   (`bundle.go:178`; `reconcileExecution` nils it at :1096, commit stops the lease),
   so a lease-only janitor erases every bound table at once. This is the concrete
   "future janitor" F-B8 fears, and it is idiomatic here — the repo already ships
   one. **[SAFE-SCOPE]** *because of the `loom_private_` allow-list; that allow-list
   is the only thing separating this working template from I2.*

8. **[SAFE-SCOPE] `internal/dataset/arango` release lifecycle.**
   `release_store.go` writes/activates only (`saveReleaseAQL` INSERT :132-141,
   `activateReleaseAQL` :152+); **no removal primitive removes a release doc**
   (AQL `REMOVE`/`DELETE` grep over the package returns zero). `manifest_lifecycle.go`
   `delete(...)` hits are Go-map key deletes (`:55,:56,:111,:140`), not AQL. So a
   dangling binding can only arise from what it *binds to* disappearing (items
   3-4), never from the binding being deleted out from under the read.
   **[SAFE-SCOPE]** — this also means a future "purge archived releases/generations"
   janitor is the analogous risk on the *release side*: if it removes an active
   generation's manifest/pointer, `activeReleaseDatasets` fails at
   `ReadActiveRelease` (`current.go:191-196`) and the same total-fail follows.

9. **[SAFE-SCOPE / comparators] Explorer + artifact-fs cleanup.**
   - `explorer/arango/selections.go` `CleanupSelectionStaging` (:388-402, deletes
     STAGING headers by `createdAt`+expired lease; separate object; its
     missing `complete!=true` guard is the h3-b note in FINDINGS). Wrong domain
     object — cannot touch `E`/`P`/`T`.
   - `explorer/artifactfs/store.go` `os.Remove` (:58,:77,:165,:176,:216) — local
     files, not Arango/ClickHouse.
   Both reinforce that "background utility that scans by age and removes" is a
   pervasive, welcomed idiom in this repo; a bundle janitor would be adopted
   without ceremony, which is exactly why the invariant must be *asserted*.

## 3. Enforcement tiers (cheapest first)

### (a) Scoped canary test — no product change [CHEAP, recommended now]

The only removal primitive that can break the invariant today is item 4, so the
cheapest durable assertion is a production-`Reconcile` "published immortality
canary", the same fidelity as the existing `reconcile_*_e2e_test.go` /
`dangling_binding_e2e_test.go` artifacts. It lives beside them
(`internal/dataframe/publication/clickhouse/`); I am **not** creating it (this
file is the only owned artifact).

Sketch:

- Seed a test Arango DB (or a recording fake catalog) with a **PUBLISHED**
  execution `E_pub` that has one queryable output, a pointer naming a *different*
  execution (so the `:1073` skip is deliberately **not** what protects it — it is
  PUBLISHED-but-superseded), and an active release whose non-stale binding names
  `E_pub` (the F-B6/F-B7 shape). Set `updatedAt` very old so it *would* be a
  stale-cleanup target if state were ignored.
- Drive the **real** `ClickHouseBundleStore.Reconcile(ctx, now-2min)` (the exact
  startup call, `server.go:556`) through a recording `clickHouse` stub whose
  `DropTable` fails the test if invoked with `E_pub`'s physical table.
- Assert: (i) `DropTable` never called for `E_pub`'s tables; (ii) the registry
  still returns `E_pub` with `state.Canonical()=="PUBLISHED"` and its queryable
  output intact (`GetExecution` ≠ `ErrBundleNotFound`).
- Add the paired **positive** control (a genuine stale RUNNING execution with no
  pointer/binding *is* dropped) so the canary cannot pass vacuously by, e.g., the
  reconciler being broken entirely.

Why it fails on the future regressions it targets: it hard-wires the exact
scoping assumptions (state enumeration + release binding) into an executable
assertion, so widening the `states` list (:1043), feeding `ListExecutions(PUBLISHED)`
into cleanup, or cloning the chartifact `ListExpired` onto executions turns the
canary red before merge — deterministically, no timing. **[CHEAP]** It reuses
harnesses that already exist and pass.

Honest limit: with today's APIs there is **no remove API to "refuse"** — items 1-3
prove the removal surface is absent — so an "assertion that the removal API
declines PUBLISHED docs" is only meaningful once such an API exists. The
forward-looking version: if/when a `RemoveExecution`/`PurgeExecutions` primitive
is added, the same canary must drive it against `E_pub` and assert refusal. That
half is **[SPECULATIVE]** until the API lands.

### (b) Runtime guard at the single chokepoint [pseudo-diff only; NO edits]

The correct atomic chokepoint is the cleanup claim, not the ClickHouse drop:
`ClickHouseBundleStore.reconcileExecution`'s `DropTable` loop (:1084) is a
ClickHouse-side effect that Arango cannot fence; the one place Arango serialises
cleanup for an execution is `ClaimExecutionCleanup`
(`bundle_registry.go:414-427`). Two options, cheapest last.

Option b1 — pre-drop re-read in `reconcileExecution` (defense-in-depth, cheap,
but *not* race-proof on its own because it is read-then-act across two stores):

```diff
     leaseCtx, releaseCleanup, claimErr := s.claimExecutionCleanup(cleanupCtx, execution.ID, reconcilerID)
     if claimErr != nil { ...ReleaseBundleLease...; return ... }
     defer func() { _ = releaseCleanup() }()
+    // PUBLISHED-immortality guard (F-B8): never drop a queryable output of an
+    // execution that is now PUBLISHED/READY or still bound by an active release.
+    // Fail closed on uncertainty (same posture as confirmCommit :838-858).
+    fresh, ferr := s.catalog.GetExecution(cleanupCtx, execution.ID)
+    if ferr != nil && !errors.Is(ferr, publication.ErrBundleNotFound) {
+        return errors.Join(ferr, s.catalog.ReleaseBundleLease(cleanupCtx, execution.Key, reconcilerID))
+    }
+    if ferr == nil && (fresh.State.Canonical() == publication.BundlePublished ||
+                       s.boundByActiveRelease(cleanupCtx, execution.ID)) {
+        return s.catalog.ReleaseBundleLease(cleanupCtx, execution.Key, reconcilerID)
+    }
     var first error
     ...
```

`boundByActiveRelease` would read the project's active release and test the live-
binding predicate from §1. It closes item 4's TOCTOU only for the reconciler's
*own* serialized claim; an out-of-band janitor that calls `DropTable` directly
still bypasses it.

Option b2 — fold the immortality predicate into the AQL claim (strongest single
change; makes the refusal atomic, and simultaneously closes F-B1's claim window):

```diff
 // Registry.ClaimExecutionCleanup  bundle_registry.go:411-427
-LET state = DOCUMENT(@@collection, @stateKey)
+LET state = DOCUMENT(@@collection, @stateKey)
+LET exec  = DOCUMENT(@@executions, @execKey)                 // join the execution doc
+FILTER exec == null OR exec.state NOT IN ["PUBLISHED","READY"]   // immortality: the claim refuses PUBLISHED/READY
 LET pins = state == null ? [] : NOT_NULL(state.pins, [])
 LET active = (FOR pin IN pins FILTER pin.expiresAt >= @now RETURN pin)
 ...
 // add bindVars: "@execKey": BundleExecutionsCollection, "execKey": executionID
```

Because the claim is the gate every reconciler drop passes through, an atomic
state filter there means the drop can never be reached for a PUBLISHED/READY doc,
regardless of enumeration. It cannot see the *release* set (release docs live in
another collection); a full version adds the same-transaction read of the active
release's binding key. **[CHEAP-b1 / SPECULATIVE-b2]** — b1 is a small,
self-contained function edit; b2 touches the guard-critical claim AQL (the same
AQL whose fidelity F-B1 says is source-read-only against the real DB), so it
needs the real-Arango canary replay before it can be trusted, and it still cannot
cover a ClickHouse-side janitor. Neither pseudo-diff is applied.

### (c) TLA+ janitor-actor sketch over `BundleCommitLifecycle.tla` [describe only; NO edit]

The model (variables `BundleCommitLifecycle.tla:51-73`; actions at `Reconcile*`
:297-383; invariants :435-451) has **no release-binding concept** and its only
drop invariant is `dropped[w] ⇒ pointer # w` (:435-436) — so a drop of a
superseded-but-bound PUBLISHED doc is invisible to it today. Sketch of the
extension that fixes the coverage:

- `CONSTANTS ... , JanitorScopingKept` (BOOLEAN). Reuse `Workers` (or add
  `Janitor`) as the cleanup actor.
- New `VARIABLES bound` (⊆ Workers) initialised all-FALSE, maintained by a
  `Bind(t)` action that sets `bound' = [bound EXCEPT ![t] = TRUE]` while
  `execState[t] = "PUBLISHED"` — modelling "an active release pins this
  execution by id, independent of `pointer`".
- New invariant (the F-B8 gap, absent from :435's pointer-only form):
  ```
  InvariantBoundPublishedImmortal ==
    ∀ t ∈ Workers : ~( dropped[t] ∧ bound[t] ∧ execState[t] = "PUBLISHED" )
  ```
- New actions `JanitorEnumerate/Acquire/Claim/Drop`, a near-copy of
  `ReconcileEnumerate` (:297) … `ReconcileDrop` (:359) with two differences:
  - the enumeration predicate is `JanitorCandidateFor(t) ==`
    `lastTouch[t]+1 <= clock ∧ (JanitorScopingKept ⇒
    execState[t] ∈ {"RUNNING","VALIDATING","FAILED"})` — i.e. it *reuses the very
    scoping (the `CandidateFor` state filter at :93, the `ReconcileSkip` pointer
    guard at :327) that item 4 relies on;
  - no `ReconcileSkip` (a janitor is assumed not to honour it) but a
    `ClaimableFor` reuse (:84) with `FClaimIgnoresPins` available.
- Behaviour to demonstrate (the point of the sketch): with `JanitorScopingKept =
  TRUE` the janitor never offers a PUBLISHED doc to `JanitorClaim`/`JanitorDrop`,
  so `InvariantBoundPublishedImmortal` **holds** — this is the machine-checked
  statement of "the invariant holds today and why." Flip the scoping to model the
  dropped assumption (add a constant `FJanitorIgnoresStateScoping` mirroring
  `FReconcileSkipsPointerCheck` :39, weakening `JanitorCandidateFor` to admit
  `execState[t] = "PUBLISHED"`) and TLC must **violate** the invariant, with the
  witness `Bind(t) → JanitorEnumerate(t) → JanitorClaim(t) → JanitorDrop(t)`.

So the added invariant falsifies current safety **only** when the enumeration /
skip scoping is dropped, faithfully reflecting that today's safety is
scoping-derived, not predicate-derived. Add `dropped` under the same ghost
discipline (`ghost` :67, drop-moment records) so the witness proves the drop
fired. Do **not** edit the model in this task; the declarations above are the
proposal's design, for the eventual verification wave. **[CHEAP]** to add given
the model's existing `Reconcile*` scaffolding and the `FReconcileSkipsPointerCheck`
fault idiom it already carries.

## 4. F-B7 blast radius, and what catches a regression first

The amplifier (F-B7, CONFIRMED): `activeReleaseDatasets`
(`current.go:190-222`) resolves every live binding and **aborts the whole
listing** on the first bad one — `return nil, err` at `current.go:207-208`. One
bound execution reaching any of the three error branches
(`resolve active execution: bundle execution not found` :95-97 /
`does not match release` :99-100 / `no queryable output` :102-110) discards all
the project's independently-healthy materializations. Caller chain: exported
`Reader.CurrentProjectDatasets` (`current.go:131`) → `graph/dataframe/rows.go:27`
→ `mapReaderError` (`service.go:154-166`) → `CodeBackendUnavailable`, retryable —
the viewer's **only** dataset-discovery surface fails project-wide, while
single-dataset `Dataset`/`Rows` stay per-selector isolated.

Therefore **any** breach of §1 (doc removed or table dropped while bound) is
immediately a project-wide outage, not a degraded read. The invariant is the
only thing standing between a janitor bug and a total-fail, and today the only
thing standing between the janitor and the invariant is the enumeration scoping
in item 4.

What catches a regression **first**, cheapest at the top:

1. **The (a) published-immortality canary, in CI.** Earliest possible: it is
   deterministic and fires at merge time on any enumeration-scoping or
   clone-the-janitor change, long before anything runs in prod. Reuse the
   `dangling_binding_e2e_test.go` / `reconcile_*_e2e_test.go` harness that already
   produces green canaries. **[CHEAP]** — the single highest-value assertion and
   the one I would land today.
2. **A dedicated tripwire at the chokepoint, distinct from the F-B7 noise.** The
   (b) guard, when it *refuses* a would-be drop of a PUBLISHED/bound table, must
   emit its own error code / counter (e.g.
   `dataframeerrors.CodePublicationLeaseLost` adjacent, tagged
   "immortality-guard refusals") — not the generic `CodeBackendUnavailable`
   F-B7 already produces, so a would-be janitor erasure is observable *before* it
   reaches users rather than only as the loud `ProjectDatasets` failure. **[CHEAP
   once (b) exists; currently SPECULATIVE** because no guard logs it today.**]**
3. **Release-binding integrity audit (read-only), on a timer or at reconciler
   start, not the hot read path.** For each active release, for each live
   binding, assert `DocPresent(e) ∧ Published(e) ∧ each bound selector output
   QueryableOf(e,o) ∧ o.physicalTable ∈ T` (needs a ClickHouse `EXISTS TABLE`);
   surface a distinct signal per broken binding. This catches an *external*
   deletion (a janitor that bypasses the claim, a manual ClickHouse drop, the
   hypothetical doc-purger) that (a) and (b) cannot see, because they only guard
   product paths. **[SPECULATIVE / heuristic]** — needs a scheduler and a ClickHouse
   existence probe; it is detection, not enforcement, and per FINDINGS the
   read-side does not currently distinguish per-binding failures.
4. Note for the eventual fix wave (out of scope here, product change required):
   making `activeReleaseDatasets` count per-binding failures before the
   all-or-nothing `return nil, err` both turns #3's signal into a first-class
   metric and lets one dangling binding degrade to the F-B8-fine "missing one
   dataset" instead of the F-B7 total-fail. Flagged only to tie the monitoring
   choice to the invariant; not proposed for this verification-only task.

Summary of claims: cheaply implementable today — the (a) CI canary and the (c)
TLA+ janitor addition (reusing existing harness and fault idioms); the (b1)
pre-drop re-read is a small self-contained edit. Speculative — the (b2) claim-AQL
state filter (needs real-Arango replay of a guard-critical AQL), a
remove-API "refusal" test (no such API exists to test yet), and the (3)
binding-integrity audit with a ClickHouse existence probe.
