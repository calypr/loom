# Publication-bundle verification findings

Verification-only program (create/maintain formal-verification skills). No product
code changed by this directory's work. Source identity: HEAD `dc9016bc`,
`clickhouse/bundle_store.go` sha256 `2f5c0fda6a154dcb20dbc93f6e2f4550ca141202e33a
3660b467907951d5d6a2`, `arango/bundle_registry.go` sha256 `1f0a6cde72895f53407052
0c4472a818f0237b64adadd96493afba938c1de138` (verified after every experiment).

## F-B1 — reconcileExecution can delete a bundle published after its pointer check

**Status:** **confirmed product bug — reproduced end-to-end against unmodified
production code on the real Arango 3.12.11 of the owned dev stack.** Every guard
decision (lease takeover `:315`, publish filters `:287-291`, cleanup claim
`:414-422`, save fence `:44-47`) was made by production AQL on the real database;
only ClickHouse's `DropTable` remains an effect-recorder stub (deletion is
deterministic once the reconciler is allowed to issue it). E2e replay:
`internal/dataframe/publication/clickhouse/reconcile_toctou_e2e_test.go`, ran
inside the pinned api container with `LOOM_TEST_ARANGO_URL=http://arangodb:8529`
(test-owned UUID database, created and dropped; stack verified residue-free):

```
docker exec loom-dev-qwen-current-loom-api-1 sh -c \
  'cd /workspace && LOOM_TEST_ARANGO_URL=http://arangodb:8529 go test -count=1 \
   -run "TestReconcile.*AgainstArango" -v ./internal/dataframe/publication/clickhouse/'
```

`TestReconcileDropsJustPublishedBundleAgainstArango` fails on purpose as the
finding (pointer dangles onto deleted tables, fenced save fails with
`ErrBundleLeaseLost` only afterwards); `TestReconcileSkipsPointerNamedExecution-
AgainstArango` passes, proving the real `:1073` guard still protects publishes
that land before the read, so injected timing — not the harness — produces the
harm.

**Promise broken.** The reconciler must never reclaim tables that a live pointer
advertises (`bundle_store.go:840` "never delete tables that a live pointer may
reference"; the skip at `:1073` and the fenced `SaveExecution` at `:1097` show the
author guarded exactly this hazard).

**Mechanism (steps in the code's own words).**

1. `Reconcile` enumerates a stale `RUNNING/VALIDATING/FAILED` execution page
   (`:1043-1051`) — state is never re-checked afterwards.
2. `reconcileExecution` acquires the bundle-key lease with the full 120 s
   `leaseTTL` (`:1057-1058`) and never renews it itself.
3. The 10 s `boundedBundleCleanupContext` starts only at `:1066`. A stall of
   ≥ `leaseTTL` between `:1058` and `:1066` (VM freeze, SIGSTOP, extreme CPU
   starvation, NTP clock jump) kills the lease while the region's own budget has
   not started.
4. The fresh `GetPointer` at `:1068` reads "not published" before a racing runner
   retrying the retryable-failed execution (the reconciler itself marks rows
   `FailureRetryable=true` at `:1092`) takes over the expired lease
   (`AcquireBundleLease :315` allows expired takeover) and publishes
   (`PublishExecution :284-302`, whose guards all pass: own live lease, pointer
   empty).
5. `ClaimExecutionCleanup :414-422` checks only expired pins and the cleanup slot
   — no lease, no state, no pointer — so the claim succeeds.
6. The cleanup-lease guardian cannot save the situation: `cleanupLeaseRenewal` is
   30 s (`:238`) while the whole claim→drop region is bounded to 10 s (`:378`), so
   the renewal ticker can never fire in-window and `leaseCtx` is never cancelled.
7. `DropTable` (`:1084`) runs unconditionally and deletes the just-published
   tables. The fenced `SaveExecution` fails with `ErrBundleLeaseLost` at `:1097`
   — proof the author expected a live lease here — but only after the deletion.

**User harm.** The visibility pointer keeps naming the execution as current, so
reads and previews resolve onto ClickHouse tables that no longer exist: the
bundle's data vanishes from under users while Arango still advertises it.

**Reproduction (unmodified production code under test).**

```
go test -count=1 -run 'TestReconcile(Drops|Skips)' ./internal/dataframe/publication/clickhouse/
```

- `TestReconcileDropsJustPublishedBundleStagesLeaseLoss` — **fails on purpose as
  the finding**: real `reconcileExecution` + real `claimExecutionCleanup` + real
  `executionCleanupLease`; `leaseTTL=-1s` stages the identical DB state the ≥2 min
  stall produces; the racing publish commits between `:1068` and the claim through
  a stub mirroring the cited AQL guards (`:315`, `:414-422`, `:287-291`, `:26-47`).
  Observed: live table dropped, pointer dangling, `errors.Is(err,
  publication.ErrBundleLeaseLost) == true`.
- `TestReconcileSkipsPointerNamedExecution` (control) — passes: publish landing
  **before** GetPointer is skipped at `:1073`; pins the flaw to the missing
  re-validation after the read, not to the harness.
- `TestReconcileDropsLongStaleUnpublishedExecution` (control) — passes: ordinary
  stale cleanup drops and fence-saves correctly.

**Remaining uncertainty.** Arango-side guard fidelity is source-read (`ClaimExecution-
Cleanup :411-427` verbatim contains no lease filter) but not yet replayed on the
real DB; the trigger requires an infrastructure-scale stall (~2 min) between two
adjacent statements, so real-world likelihood is low but nonzero, and TTL-lease
design normally closes such windows with a pre-effect re-validation, which this
path lacks.

**Suggested remedy (operator decision; not applied — verification-only).**
Re-validate the bundle-key lease (e.g. `RenewBundleLease` fenced) immediately
before the `DropTable` loop, or gate the loop on a fresh `GetPointer` re-read;
mirror of the fence already present at `:1097`.

### Model cross-validation of F-B1

With every fault constant FALSE, `BundleCommitLifecycle.tla` **violates**
`InvariantNoDropUnderPointer` by itself (TLC, Workers={w1}, Horizon=6, ~1.1M
states): the reconciler claims a candidate from the stale enumeration snapshot
and deletes it while the pointer names it. This is an independent formal
rediscovery of the bug already confirmed against the real Arango, not a fault
control. The witness also exhibits the owner-side sibling: `AbortClaim` (claim
AQL passes), then the in-flight publish lands (`PublishExecution` succeeds
server-side while the caller never sees the ACK), then `AbortDrop` deletes the
now-advertised tables; the model's `Abort`-path fidelity was re-read against
`Abort :861-897` + `ClaimExecutionCleanup :411-427` (claim protects pins/slot,
never the pointer).

## Second mechanism: resolved PREVENTED (Worker A, sanctioned harness `abort_ackloss_e2e_test.go`)

The lost-publish-ACK -> `Abort`-destroys-published-tables sequence is **PREVENTED by identified code**: when the publish call errors, `Commit` first reconciles via `t.confirmCommit(ctx)` and takes the committed branch if true (`bundle_store.go:813-815`, reads at `:841-858`); an inconclusive confirmation sets `t.closed = true` with the in-code intent comment (`:809`, `:817-822`) so the runner cannot delete possibly-published tables; `Abort` returns nil before any claim when `t.closed` (`:862`). Harness: publish succeeds server-side, ClickHouse adapter injects `context.DeadlineExceeded` on the publish response only; control (normal commit path) passes. Outcome classification per the protocol: prevented.

OPEN residual variant, recorded honestly and unverified: abandon the publish request *before* Arango applies the write, with the `confirmCommit` fallback reads racing ahead of that write (relies on the Arango driver's write/read ordering guarantees within one connection). Not reproduced; no evidence either that it occurs; do not treat as a finding.

## F-B3: CONFIRMED product bug — one never-released read pin wedges startup reconciliation deployment-wide (H2-a; Worker B, real Arango, differential repro)

Mechanism (all cited lines verified by the parent in source too): startup calls `bundleStore.Reconcile(ctx, now-2min)` (`internal/server/server.go:556-558`); `Reconcile`'s page visitor returns the FIRST per-execution error and aborts the whole sweep (`bundle_store.go:1033-1052`; propagation `bundle_registry.go:143-145`); a stale-but-live-24h export read pin makes `ClaimExecutionCleanup` return `claimed=false` (`bundle_registry.go:411-427` single unexpired pin filter) -> `claimExecutionCleanup` -> `ErrExecutionReadPinActive` -> `reconcileExecution` returns it (`bundle_store.go:1076-1079`) -> the entire Run aborts. The server then records degradation and sets `publicationReady=false` (`server.go:556-559`), leaving `bundleTarget=nil` (`server.go:560-562`, consumed at `:823`). A `rg` sweep found exactly ONE caller of this Reconcile (server startup) — **no retry or background re-reconcile exists**, so the degradation persists for the process lifetime; a restart within the pin window re-fails the same way.

The pin itself: production hands the exact-export stream an initial pin expiry of `identity.ExpiresAt` = now + 24h (`internal/explorer/lifecycle/artifact.go:21 defaultArtifactTTL`, `:104`, `:152 PinExpiresAt`; `export.go:120-126` accepts any future expiry), while the renewal ticker only pulls it down to now+2min at the first 30-second tick (`export.go:156`, `:166`; `selection.go:14-15`). A crash inside that window leaves a 24h live pin on a stale execution. This contradicts the author's own intent comments: Reconcile's doc says "safe to call repeatedly during startup" (`bundle_store.go:1029-1032`) and `export.go:57` describes the pin as a "shorter operation budget". Fix shape (not applied; verification-only): collect pin-active errors and continue sweeping siblings, surface non-fatally.

Evidence: sanctioned red artifact `TestStalePinBlocksEntireReconcileAgainstArango` (FAILS on purpose; CONFIRMED report incl. `full dropped set=[]` with the sibling pinned-free execution stranded); control `TestStalePinAbsentReclaimsAllAgainstArango` (identical seeding, no pin: BOTH reclaimed by the same production Reconcile — causality is the pin, not ordering); probe `TestLivePinOnOnlyStaleExecutionStillAbortsRunAgainstArango` (single-target veto also aborts startup readiness). File: `internal/dataframe/publication/clickhouse/pinblock_reconcile_e2e_test.go`; 3 owned DBs `loom_verify_pbx_*` created+dropped, residue greps empty, package vet clean (worker's verbatim outputs in this session).

Remaining uncertainty (worker-stated): the crash-inside-first-tick is source-verified as reachable but was reached by directly acquiring the production pin API to the persisted end-state, not by killing a live export process at t<30s. Stranding is visit-order sensitive (`updatedAt ASC`); X seeded older. Pointer-advertised/PUBLISHED executions short-circuit earlier (`bundle_store.go:1073`) and stay protected — consistent with the hypothesis scope.

**Program status: two confirmed Phase-B product findings — F-B1 (drop-TOCTOU, model-triangulated) and F-B3 (startup pin-wedge) — with F-B3 crash realism upgraded by `export_pin_crash_window_e2e_test.go` (live `StreamExactExport` parked pre-first-tick with caller 24h TTL, zero renewals observed, direct-AQL pin confirmation, renewal control pulls horizon to now+2min then reclaims both; DBs `loom_verify_ecw_*` dropped; vet clean). Model canonical re-established after repair (see maintenance incident): md5 `14d341f8...`, warnings=0, full checks complete; matrix signatures: baseline `InvariantNoDropUnderPointer` violated depth 9, f2 depth 15, f3 depth 7, holds complete clean (1.74M distinct), lease-fence control: fault violates, no-fault clean complete (3.84M distinct).**

## NEW candidate F-B4 (parent source review; trigger NOT reproduced): stale-clock pin renewal can stamp an already-expired pin

`StreamExactExport` renews at `renewInterval` (default `defaultReadPinInterval`, `export.go:127-129`, ticker `:156`) and each tick computes `next := time.Now().UTC().Add(defaultReadPinTTL)` **before** the 5s-timeout `RenewExecutionReadPin` call (`export.go:165-167`); `RenewExecutionReadPin` persists the caller-provided expiry unconditioned (`bundle_registry.go:468+` — no lead guard, unlike `Acquire`'s future check). If the renewal RPC (or scheduling) lags behind the tick by more than the TTL, the doc lands with `expiresAt` already in the past while the stream is mid-flight -> next `ClaimExecutionCleanup` accepts against a live reader. Same stalled-dependency class as F-B1's lease (:1057/:1066) but at the pin layer. Mitigating facts verified in the renewed-loop code: renewal loss/error aborts the stream (`export.go:170-173` -> `cancel()`), and the default cadence is 30s against a 120s TTL (`selection.go:14-15`) giving a 4x margin, so reproduction needs a >120s application stall — recorded as a low-likelihood candidate, trigger NOT reproduced and no positive e2e evidence exists for this path.

## Model-maintenance incident (frame-list hygiene)

RESOLUTION (this session): the repo-cached spec had silently carried over-strip damage (and a duplicated tuple line) — the prior "byte-stable" md5 had been captured post-damage, teaching the lesson that hashes must be taken at *verified* moments. Repair via TLC-guided convergence (add each `not completely specified` var to its action's shared-tail UNCHANGED — semantically neutral) + one manual tuple fix (BeginAcquire). New canonical md5 `14d341f86d6f4f072a585434334dfc7a`: warnings=0, parses, holds complete clean; matrix violation depths reproduce (9/15/7; prior 8 for f3 restated from current saved log `log-hy-m-f3.txt`: 7).

A static regex intersection audit suggested 16 "UNCHANGED clashes"; stripping them broke branch completeness — shared-tail UNCHANGED is REQUIRED for variables assigned on only one IF/ELSE branch (`closed`, `committed`, ...). The oracle is TLC's own runtime warning + complete-specification error, not static intersection. Outcome: the repo copy carried unassigned-variable corruption from the earlier edits (BeginAcquire had lost four entries; the 'byte-identical backup' had been captured after the damage, so its hash was a stale credential, not proof). Repair = manual restore of the BeginAcquire tail + a TLC-guided loop adding each `not completely specified` variable to its action's shared-tail UNCHANGED (semantically trivial, convergence in one iteration). Re-verified canonical md5 `14d341f86d6f4f072a585434334dfc7a`: zero frame warnings, zero under-specifications; m-holds complete (1.74M distinct); fence no-fault control complete clean (3.84M distinct); faults violate at depths baseline 9, f2 15, f3 **7** (earlier note of 8 was from the pre-repair witness; current authoritative: 7, log-hy-m-f3.txt). **ReconcileDangleTrust model (2026-11-01, F-B12 formalized):** `ReconcileDangleTrust.tla` models the CONFIRMED interleaving explicitly — Observe(RUNNING candidate) -> PublishExec (PUBLISHED, pointer doc deferred) -> CheckPointerAbsent (:1068-1073 guard reads ABSENT inside the two-doc window) -> Activate (release binds, never read-pins) -> DropDemote (:1084/:1089/:1097 semantics). Violation trace is exactly that 5-action sequence (log /tmp/opencode/rbt-m-dangle-bug-run.log). Fix tiers as constants: PinAtActivation=TRUE -> clean complete; RecheckBeforeDrop=TRUE -> clean complete; both falsify NoDanglingBound under both=FALSE. FixedReader=TRUE config proves the reader tier restores ONLY HealthyDiscovery (NoDanglingBound intentionally omitted there — data-loss claim not made): tier-coverage separation is explicit in cfg composition. 0 warnings; 16-19 distinct per config via tlc-run. **ActiveGenerationTrust model (2026-11-01, F-B13 formalized):** `ActiveGenerationTrust.tla` — loader (sets lastLoaded+pointer), compile (receipt bound to THEN-active, models the compile-path enforcement), PublishCAS (repoints pointer to receipt generation with no active comparison; `CrossGen` constant models the proposed CAS guard). Invariant `NoSilentDowngrade == activeGen = NoGen \/ activeGen = lastLoaded`: `m-downgrade-bug` (CrossGen=FALSE) VIOLATES with exactly the product trace Init;Load(g1);Compile;Load(g2);PublishCAS (9 distinct); `m-downgrade-fixed` (CrossGen=TRUE) complete clean (7 distinct); 0 warnings; via tlc-run (logs /tmp/opencode/rbt-gd{6,7}.log). Bring-up note: declaring the state predicate as cfg `PROPERTY` (instead of `INVARIANT`) silently passed it — root-caused by a minimal scratch probe (/tmp/opencode/agtp/Probe2.tla) that violated under INVARIANT while the full model passed — model-side tooling lesson recorded here. **StartupReconcileWedge model (2026-11-01, F-B3 formalized):** new model `StartupReconcileWedge.tla` — startup's single first-error-wins Reconcile pass, shared pin horizon (PinnedExpiry) vs startup window (TimeMax), collateral non-drop of unpinned bundles, one-shot success transition, WF(Tick)+WF(Success), temporal property `EventuallyReady == <>[]ready`: `m-wedge` (horizon 10 > window 8) VIOLATES over the complete 9-state space (permanent booting = the confirmed wedge); `m-wedge-clear` (horizon inside window) model checking completed with no error. 0 frame/under-spec warnings, via /sandbox/.local/bin/tlc-run (logs /tmp/opencode/rbt-wedge{I,J}.log). Note: initial bring-up iterations (unbounded failedRuns counter, missing UNCHANGED vars stutter, missing WF on the success action) were MODEL defects fixed in place, each failure mode diagnosed from the saved log before changing input. **ReleaseBindingTrust extended matrix (2026-11-01):** `LegacyHas`/`OrderKey` operators model the lowercase-only `SORT` (bundle_registry.go:179); with `LegacyHas={intended}` the name-latest bind is wrong even when the intended doc is genuinely newest — `m-rebind-case` violates, `m-rebind-caseholds` (Recheck=TRUE) completes clean: the proposed activation-time identity recheck defends both the digest-shadowing and the Bug-3 case route. Model md5 `2f09bc97`; four-config matrix green as expected; all runs via /sandbox/.local/bin/tlc-run. TLC 2-worker widening stayed documented-incomplete (14M states at depth 18 in 150s; single-worker bounds remain the published acceptance).


## Selection-staging checks: F-B5/h3-a DISPROVEN, h3-b VERIFICATION FINDING (worker + parent record run on real Arango)

Artifact: `internal/explorer/arango/selections_cleanup_e2e_test.go` (3 env-gated tests; SKIP-clean without `LOOM_TEST_ARANGO_URL`; DBs `loom_exv_*` created/dropped, residue empty; package `go vet` clean). Parent record run `LOOM_TEST_ARANGO_URL=http://arangodb:8529 go test -run TestSelectionCleanup ./internal/explorer/arango/`: **3/3 PASS** — `...ReapsLeaseLapsedWriterMidDigest` 0.14s, `...DestroysCompletedStagingHeader` 0.06s, `...LeaseGapAtScale` 14.28s.

- h3-a (idle writer's staging reaped by a foreign `BeginSelection` auto-cleanup while legitimately mid-digest) **DISPROVEN**: idle phases keep the 10-minute writer lease current (reader-then-renew structure; measured paging loop far inside budget — no un-renewed phase over 10 minutes was found), so `CleanupSelectionStaging` (`selections.go:388-402`: state STAGING + createdAt<before + lease-expired filter) leaves live sessions untouched, honoring the "active writers never touched" promise.
- h3-b **verification finding (not a product bug)**: `CleanupSelectionStaging` uniquely omits the `complete != true` guard its siblings carry (`AbortSelection :290`, `CompleteSelection :242`); a header constructed with `{state:"STAGING", complete:true}` + 3 members is destroyed by production cleanup — but every path to `complete=true` is `CompleteSelection`'s atomic transition that simultaneously exits STAGING (`:231-233`, `:244-247`); raw AQL could not produce the combination via product-reachable state, and staged members survive for normal completion anyway. Defense-in-depth hardening noted for the eventual fix wave.
- Honest limit: no adversarial time dilation beyond natural timing; worker's file-status self-report ("created no files") was inaccurate — the parent verified the artifact directly and this record supersedes it.

## NEW candidate F-B6 (worker brief, parent-verified lines): release binds newest-published execution by name, not the materialized one

`FindExecutionBySelector` (`bundle_registry.go:174-182`) filters only project/generation/name/translationVersion/output + PUBLISHED|READY — none of `Key/RecipeDigest/SchemaDigest/ReceiptID` — then `SORT doc.publishedAt DESC LIMIT 1` (name-latest). `release_verifier.go:31` echoes `Selector` verbatim (so `release.go:185`'s mismatch guards are vacuous for shipped wiring), `release.go:181-190,236` binds+CAS-activates whatever the verifier returned, and `publish.go` never passes the just-materialized `execution` into release preparation — nothing compares revision `Materializations[].MaterializationID` with `release.Publications[].ExecutionID` (tree-wide grep). Trigger shape: same-recipe-name executions with divergent digests + idempotent `beginBundle` replay (`bundle_store.go:137-140`) -> release binds the OTHER execution; viewer serves foreign rows silently. Coverage gap: zero tests for this AQL. Parent verified all cited lines. Ranked below confirmed findings until reproduced; worker brief's Bug 2 (all-or-nothing current-listing amplifier), Bug 3 (legacy-case-sort mis-selection), Bug 4 (tautological verification guards), Bug 5 (latent audit-copy defect) recorded as follow-on candidates: Bug2 = high-blast/low-likelihood amplifier of F3-pinned immortality invariant; Bug4/5 = defense-in-depth class.
## F-B8 immortality proposal + F-B12 composite verdict (Round 7b)

- **F-B8 proposal DELIVERED**: `PUBLISHED_IMMORTALITY_PROPOSAL.md` (25 KB, sole artifact; invariant I1-I5 over active-release bindings; 9-site removal enumeration; 3 enforcement tiers: env-gated property test sketch, constant-controlled TLA overlay proposal (canonical md5 preserved), report-only linter `loom_dataframe_active_binding_dangling_total`). Parent line-verified anchors: sole registry REMOVE is lease-only (`arango/bundle_registry.go:338-340`, BundleLeasesCollection), dataset stores zero REMOVE, chartifact removal single AQL gated to PrivateArtifactsCollection + leaseOwner + state==cleanupPending (`execution/chartifact/arango/catalog.go:255-265`).
- **F-B12 CONFIRMED-RELEASE-DANGLE (top-severity product bug)** — sanctioned red artifact `internal/dataframe/publication/arango/release_dangle_reconcile_e2e_test.go` (FAILED-ON-PURPOSE), worker-run (uuid 077baa7e) AND parent-run (uuid ea548d7f): identical structure and evidence fields, values equal modulo owned UUIDs and CAS-assigned release IDs. Reconcile enumerated a stale RUNNING candidate whose `GetPointer` guard (`bundle_store.go:1068`) observed an ABSENT pointer (PublishExecution's two-doc non-atomicity: exec-state visible, pointer-last); the racing publish then made the exec a queryable PUBLISHED row and a release Created+Activated binding BOTH executions; production AQL (a) physically `DROP TABLE`'d the candidate's real ClickHouse table (`system.tables` gone=true, healthy table present), (b) demoted the bound execution to `State="FAILED" Outputs=0`, while the ACTIVE release (revision=1) still binds it — final `CurrentProjectDatasets` -> `list=0 err=[active dataframe execution "rrd-exec-dangle-..." does not match release "release_..."]` (current.go:95/99-100/206-208 chain): the INDEPENDENTLY HEALTHY binding is lost from the only dataset-discovery surface; pointer-last naming changed nothing (reader never consults pointer). Guardless by construction: `ClaimExecutionCleanup` has no release signal (arango/bundle_registry.go:418 region free of release/lifecycle refs) and release Create/Activate NEVER read-pins (zero AcquireExecutionReadPin refs in dataset layer; production callers only `published/export.go:135`, `published/read_pins.go:63`). Reachability caveat kept honest: staging forced the pointer-absent window deterministically (production trigger = reconcile enumeration interleaved with publish→activate; no product code altered). Fixes queued for operator: (i) activation-time read-pin of every bound ExecutionID via existing ClaimExecutionCleanup-compatible pins, (ii) drop-path re-check of release bindings before DropTable/FAILED, (iii) reader partial-degrade (skip+flag broken binding instead of whole-list abort, mirroring the per-selector isolation already proven by F-B7 E5a/E5c). Worker-integrity note: closing paragraph mislabeled the verdict "PREVENTED" while the actual run output printed CONFIRMED; parent adjudicated from reproduced run output, and the earlier mid-cycle fabricated PREVENTED record was already reverted. Arango residue 0 (parent); CH owned tables dropped (RRD_CLICKHOUSE_DROPPED x2).

## Dataset manifest/release-lifecycle brief (Round 7, worker, naming-corrected; parent-reverified quotes)

Naming correction: no PrepareReleaseCreate/CancelRelease/PREPARING exist; real pair is `dataset.ReleaseService.Create`->`SaveRelease` (`release.go:140-229`) and `PublishAuthoring`->`CompareAndSwapActivateRelease` (`explorer/arango/publication.go:57` -> `release_store.go:62-103`); manifest states LOADING/STAGED/READY/FAILED.

- **F-B13 (top candidate) CONFIRMED-by-reading / PLAUSIBLE-unverified (untested stack), parent-reverified quotes** — stale-receipt publish silently downgrades the project's live dataset generation. Chain: (1) compile-path active-generation check EXISTS (`internal/server/explorer_capability.go` ResolveForCompilation: `manifest, err := dataset.ResolveActive(...)` + `authorized.Snapshot.Identity.Generation != manifest.Dataset.Generation` -> reject ~:214-218); the EXECUTION path used by publish (ResolveForExecution ~:177-190) checks only `snapshot.Identity.Project` + scope digest + self-consistency `snapshot.Identity.Generation == receipt.SourceGeneration` (`internal/explorer/lifecycle/publish.go:32-33`) — NEVER the active pointer (zero ResolveActive/SourceGeneration== hits in publish.go, parent grep). (2) Preflight `release.go:134 IsStaged()` accepts prior generations (STAGED never demoted; only loader writes STAGED/FAILED via `TransitionManifest`). (3) CAS repoints the ACTIVE pointer to the receipt's generation unconditionally: `release_store.go:163` filters the manifest only by ITS OWN generation; `:170` patches generationPointer with `{dataset: manifest.dataset, manifestKey: manifest._key, ...}`. Readers follow the pointer (`manifest_lifecycle.go:304-305`, `active_resolver.go:20`). User-visible: after loading+activating G2, publishing a retained G1 receipt makes activeGeneration/published reads revert to G1 with no error. Untested: unit CAS uses canned rows (`memory_store_test.go:55-71`), Arango store test only string-matches the query (`store_test.go:153-179`). Reproduction e2e RUNNING (worker owns internal/explorer/arango file, prefix loom_verify_cgp_*).
- **F-B14 CONFIRMED end-to-end-boundary** (trace worker + parent line-verification, 2026-11-01): a deterministic requirements miss is presented as **HTTP 503** on the authoring publish route. Full hop chain verified verbatim: route `generated/loomapi/api.gen.go:14581` -> `internal/server/openapi_explorer_routes.go:442/:467-468` -> `openapi_explorer_authoring_direct.go:719/:727/:738/:747-750` (single mapper) -> `lifecycleErrorStatus` `:804-821` (`ClassUnavailable -> http.StatusServiceUnavailable`) -> `api.gen.go:17462-17467 ctx.Status(503)`; origin `internal/explorer/lifecycle/publish.go:87-89` `unavailable("activation", "MATERIALIZATION_ACTIVATION_FAILED", "dataset release preparation failed; the prior Explorer revision was retained", err)` wrapping `ReleaseRequirementsError` (`internal/dataset/release.go:16-17/:192-193`). Defects: (a) indistinguishable from genuine transient outage (`publish.go:79` real materialization failure shares code/stage/message); (b) **no explicit retryability** — the authoring `Diagnostic`/`ErrorBody` contract (openapi.yaml:6733-6746) lacks `retryable`, violating `docs/loom-reliability-contract.md:92-94` ("clients do not infer it from an HTTP status"); (c) every untyped Create error (release.go:152/:158-160/:168/:174/:228 — including operator config typos, permanent) collapses to the same 503. Honest caveats: reachability is **config-gated** — requires `LOOM_REQUIRED_DATAFRAME_SELECTORS` (`options.go:42/:235`); with default nil-Required the failed-branch never executes; source-traced (no live 503 captured). Sibling route contrast: the legacy explorer-config route maps requirements misses to **409** `RELEASE_ACTIVATION_FAILED` but persists a diagnostic `Retryable: true` (`repository_publish.go:78-80`) and conflates permanent misses with genuine CAS conflicts — secondary boundary-confirmed defect (lower severity). INTEGRITY PLUS: the worker caught a misquote circulating in session summaries (claimed nonexistent `errorutil.Internal` wrapper); the record here always used the verified `unavailable(...)` form.
- **F-B15 CONFIRMED-by-reading (mechanism) / PLAUSIBLE-unverified (frequency)** — publish retry inflates revision + duplicates release docs: `release.go:158,227` derive `release.ID` from `ReadActiveRelease`+1 digest and `server.go:582-584` reads expectedRevision independently; after a committed activation, retries produce a new ID so `alreadyActive` (`release_store.go:158-167`) never fires; no release-layer idempotency analog to `manifest_lifecycle.go:198-201`. Symptoms: revision++ and byte-identical near-duplicate release docs per click.
- REFUTED by worker with executable-line quotes (parent spot-checked the CAS shape): concurrent prepared-content mutation (insert-only releases + CAS re-filters `release_store.go:163-164`), partial activation visibility (single ArangoJS tx `publication.go:30-57`, readers follow only committed pointer), mergeObjects misread, READY/ACTIVE policy leak (Canonical() collapse, `bundle_registry.go:177`). Low-risk asymmetry noted for intent confirmation: release CAS lacks the `schemaIdentity` predicate that `activateAQL` has (`manifest_lifecycle.go:326`).
- Worker integrity: 30 line-exact quotes, correct self-reported naming corrections; two worker-cited paths were directory-wrong (`internal/explorer/arango/explorer_capability.go` -> actually `internal/server/`; generation_orchestration.go path likewise under another dir) and parent re-verified every load-bearing line before this record.

## Verification/release-contract brief (worker, read-only): 1 top candidate + 4 follow-ons + 7 ruled-sound controls

Scope correction: the assignment's paths don't exist; live wiring is `internal/server/release_verifier.go` -> `internal/dataset/release.go` -> `internal/dataset/arango/{manifest_lifecycle,release_store}.go`, states from `publication/arango/bundle_registry.go`.

- **F-B6 registry-level CONFIRMED (worker, sanctioned red artifact `internal/dataframe/publication/arango/name_latest_binding_e2e_test.go`, 264 lines, env-gated SKIP-clean, vet+build OK)** — `FindExecutionBySelector` (`bundle_registry.go:174-182`) ignores Key/RecipeDigest/SchemaDigest/ReceiptID and returns publishedAt-DESC LIMIT 1: `TestFindExecutionBySelectorIgnoresRecipeIdentityAgainstArango` FAILED-ON-PURPOSE returning the t2 foreign-digest doc when the caller intended t1 (full identity fields in the report); the seam test FAILED-ON-PURPOSE showing production `publicationVerificationStore.VerifyPublication` returns the newer execution while an older distinct one exists; **Bug-3 (legacy-case asymmetry) CONFIRMED** (final artifact + parent re-run): a legacy doc whose publish time sits in capitalized `PublishedAt` sorts as null under the lowercase-only SORT, so the OLDER lowercase doc wins (`nlb-case-lower` @12:00 beat `nlb-case-legacy` @next-day-09:00); visibility itself still requires lowercase `state`/`outputs` (no OR branch at `bundle_registry.go:177-178`) — reachable only via docs written outside the current `SaveExecution` struct tags. DBs `loom_verify_nlb_*` created/dropped, parent residue grep empty for `loom_verify_nlb_`.
  **Product-level damage: OPEN, with corrected reachability analysis.** The worker's same-RecipeDigest precondition was OVER-STRICT — the AQL never filters digest, so the two contending rows have DIFFERENT digests: publish recipe digest D (E1, t1), edit recipe to D' and publish (E2, t2), replay the old receipt: idempotent `beginBundle` (`bundle_store.go:137-139`, keyed bundleName+digest) returns E1 (intent=E1) WITHOUT re-publishing; verification then name-latest-selects E2; revision `MaterializationID`=E1 vs release binding=E2 (`explorer/arango/publication.go:41` passes `revision.ExecutionID()` un-eyeballed to `CompareAndSwapActivateRelease`, which writes it verbatim `release_store.go:178`). Remaining gates before a product-bug claim: (1) old receipts remain addressable/re-playable (no expiry found but retention paths not enumerated, worker's assumption (a)); (2) PrepareRelease/PrepareExplorerRelease don't stamp the materialized ID themselves (`lifecycle/publish.go:87,175`); (3) no downstream reconciliation of materializationId vs bound ExecutionID (worker's tree grep: zero comparisons). Worker's four explicit product-reachability preconditions (all must hold): (1) two selector-visible PUBLISHED/READY docs coexist at PrepareRelease time, same generation, both Queryable; (2) the just-materialized doc does NOT sort first — needs concurrent-publish visibility window, clock skew/future-dated publishedAt, or the legacy-case route; every current producer stamps lowercase publishedAt; (3) activation order disagrees with sort order so the loser's revision activates against the winner-prepared release (later-activating B self-heals); (4) consequence observability lives in ClickHouse physical tables (out of scope here). Verdict kept: genuine unreconciled trust gap on the bind path, product trigger a narrow race/anomaly window. Artifact tests log CONFIRMED/DISPROVEN with pre-stated expectations and PASS (bug-documenting assertions); executed AQL captured via recording wrapper (bind vars: collection/project/generation/recipe/translationVersion/output only). DBs loom_verify_nlb_* x3 removed; residue command printed nothing; package `go test` (no env) ok; `go vet` clean.
- **F-B7 CONFIRMED, corrected mechanism** (worker + parent line-verification; sanctioned FAILED-ON-PURPOSE artifact `internal/dataframe/publication/arango/dangling_binding_e2e_test.go`, env-gated SKIP-clean, DBs `loom_verify_db_*` created+dropped, residue 0).
  - CONFIRMED product harm: one bad binding (dropped PublishedOutput row → `active dataframe execution "dbind-exec-bad" has no queryable output "out-b"`; or dangling execution → `resolve active execution: bundle execution not found`) makes `CurrentProjectDatasets` return `nil, err` for the WHOLE project — independently-valid healthy materializations are lost (`return nil, err` verified verbatim at `published/current.go:207-208`; per-binding materialization fetch at `:94-96` via `GetExecution`, no pointer recheck). Caller chain: exported `published.Reader.CurrentProjectDatasets` (current.go:131) → `graph/dataframe/rows.go:27` `ProjectDatasets` → `mapReaderError` (service.go:154-166 → `CodeBackendUnavailable`, retryable) — the viewer's only dataset-discovery surface fails for all datasets of the project when any bound execution vanishes, while single-dataset reads (`Dataset`/`Rows`) stay per-selector isolated (observed fresh E5a/E5c).
  - Hypothesis corrections (parent-verified): the original candidate text's mechanism was wrong — no stale-demotion of the whole dataset, no `all = false`, no `loadPublishedExecutionForRead`/`publishedOutputMatches`/`requiredReleaseBindings` (0 repo matches; current.go is 243 lines, release.go 280 — the claimed :257/:349-390 don't exist). Stale handling = plain skip (`current.go:203-204`); pointer un-naming cannot affect release reads; harm is LOUD (retryable error), not silently-stale, and requires the release↔execution invariant break (F-B8) — amplifier role stands.
  - Required-set silent-demotion variant: **DISPROVEN** — read path never consults `Required` (0 occurrences in current.go); required-stale binding silently skipped (err==nil, sibling served), required-selector → UserError `DATASET_NOT_FOUND`, all-stale → `DATASET_NOT_FOUND` not `ErrReleaseRequirementsUnmet`; `Required` enforcement is create-only (`release.go:185-193`) and carried bindings lose it at `release.go:209` (`prior.Required = false`), making `Required=true,Stale=true` unreachable except via direct doc write.
- **F-B8 (invariant gap)** — release stores only ExecutionID with NO read-pin at activation; safety rests entirely on unasserted "PUBLISHED executions/tables immortal today" (verified true today: single REMOVE in registry :340; Reconcile skips pointer-named/PUBLISHED). A future janitor dangles active releases into F-B7's amplifier. Needs an assertion, not a test.
- **F-B9 (candidate low)** — legacy-capitalized-field tolerance in selection AQL but lowercase-only sort/Go checks (`bundle_registry.go:175-179`) mis-selects "latest" when legacy docs compete; AQL-vs-Go filter asymmetry fail-closes correctly.
- **F-B10 (defense-in-depth)** — audit-record ErrorCode lost by value-copy-after-append (`release.go:183-187`, latent); conflict-classification asymmetry store-facade vs transport; memory double skips CAS filters.

RULED SOUND by controls (do not re-open without new evidence): activation AQL field preservation (mergeObjects semantics + integration PASS), verification cannot pass FAILED/non-queryable outputs (`release.go:185` + `bundle.go:147-149`), verify->activate TOCTOU (PUBLISHED immortality today), manifest state race (in-query recheck `release_store.go:162-164`), pointer-move vs bound release (read-time recheck + named test), failed-activation candidate retention, memory/Arango CAS parity.

## Model-bug postmortem (why early clean runs were silent)

Three spec-level defects each made a protocol action ungeneratable **without
any error**, because TLC only reports incomplete next-state specifications and
mis-grouping when it ever evaluates the action:
1. deep-EXCEPT shorthand `!.cand![t]` (unsupported syntax); fixed with nested
   `[rec.cand EXCEPT ![t] = TRUE]`;
2. `cleanupExpires` missing from the UNCHANGED lists of `ReconcileDrop`/
   `AbortDrop` (variables neither assigned nor declared unchanged → successor
   never produced);
3. the save-branch written as `\/ /\ ...` / `/\ /\ ...` (lines 357–361,
   281–285 pre-fix) column-grouped into a contradiction → both drop actions
   were permanently unfireable.
Diagnosis method that cracked it: a textual *twin* of `ReconcileDrop` whose only
effect was a ghost flag — it fired immediately under simplified branches while
the original never produced a successor, isolating the branch syntax. Guarded
against recurrence: the model now carries the read-once/ordering conjuncts
(`rec.rd`) and complete UNCHANGED lists (audited per action); every "clean"
verdict below was produced only after probe witnesses showed the drop steps
fire.

## Round 9 (2026-11-01T21:55Z) — F-B13 e2e CONFIRMED + F-B11 control adjudicated + path-confusion resolved (no integrity incident)

**F-B13 e2e CONFIRMED (upgrade from CONFIRMED-by-reading)** — `internal/explorer/arango/cross_generation_publish_e2e_test.go` FAILED-ON-PURPOSE against real Arango, worker uuid 39dfd91a + parent uuid 3dc90697 (`/tmp/opencode/fb13-parent.log`), authenticated residue 0 (`/_api/database` -> `[_system, loom_dev]`, zero `loom_verify_cgp_*`). Driven production: `dataset.ReleaseService.Create` (preflight release.go:130-229 — never compares the active pointer) + real `datasetarango.CompareAndSwapActivateRelease` (activateReleaseAQL release_store.go:152-176 patches the SHARED generationPointer unconditionally :170). Verbatim: baseline `generation-pointer="cgp-fb13-gen-2"`, post-CAS `="cgp-fb13-gen-1"` err=nil, `releaseRevision:2`, both `CurrentProjectDatasets` paths flip to G1 exec. Layer honesty: `internal/server` + `Store.PublishAuthoring` wrapper not driven (import cycle; wrapper drives the same CAS at publication.go:57). User harm: publishing any retained older Explorer receipt silently rolls the whole project's active dataset generation back, err=nil, no warning, revision ticks forward masking it.

**F-B11 PREVENTED (adjudicated, parent-verified PASS)** — `TestReconcileDanglesActiveReleaseBindingAgainstArango`: pointer-named + ACTIVE-release-bound execution survives the stale-RUNNING sweep: reconcile claimed under `reconciler-278d34ac...`, re-read the naming pointer 1x, guard branch `bundle_store.go:1068-1075` skipped cleanup; state RUNNING, no marker, table present, list healthy; **live pins=0** (guard, not pins, is the sole shield for the pointer-named case); E3 live reclaim of the unpointered control proves the sweep real. Log-claim DISPROVEN: no 'skipping bundle with pointer' string in production — the :1073 skip is silent, proven behaviorally. ABSENT-GUARD shadow control (fault control, not a bug): removing the pointer condition reproduces the F-B9->F-B1->F-B7 end-state (`tableDropped=true`, wholesale `list err=[... does not match release ...]`) — :1073 load-bearing.

**Integrity correction to earlier round-9 draft (self-caught).** The claim above was wrong: F-B12's artifact was NEVER overwritten. The two similarly-named files are DISTINCT and both intact: `internal/dataframe/publication/arango/release_dangle_reconcile_e2e_test.go` (md5 `890a4b5cf9ef7fd584844844843f4983`, package `arango`, holds `func TestReconcileDanglesActiveReleaseAgainstArangoClickHouse` at :564 — the sanctioned-red F-B12 artifact) and `internal/dataframe/publication/clickhouse/release_dangle_reconcile_e2e_test.go` (md5 `065613bbdbb1acdf6ca267b97d973ed7`, package `clickhouse`, holds `func TestReconcileDanglesActiveReleaseBindingAgainstArango` at :720 — F-B11 control). The 'grep ^func Test returns one match' observation came from grepping the second file while assuming it was the first. The earlier 'prevented report contradicts recorded CONFIRMED' adjudication remains valid (different scenarios: pointer-present PREVENTED vs two-doc-window CONFIRMED). Root lesson recorded: near-identical filenames across packages are an audit hazard; future worker briefs must cite package + directory, not just basename.

**Queue-5 source resolution (parent, read-only): single-dataset reads are DEGRADED-TARGETED, not wholesale.** `Reader.CurrentProjectDataset`/`activeReleaseDataset` (`published/current.go:74-89`) resolves only the requested selector's binding and returns a typed error per read (`:83/:100/:110`) — a dangling binding or missing output fails JUST that dataset/rows path (F-B7-style blast-radius expansion does NOT reach single reads/Rows). The all-or-nothing `return nil, err` is exclusively the LIST path (`:206-208`). No new finding; recorded as checked-and-sound (scoped prevention: per-selector degradation is already the single-read design).
## Round 10 (2026-11-01T22:50Z) — F-B15 CONFIRMED e2e + INTEGRITY EVENT #2 (fabricated replay report)

**F-B15 CONFIRMED (revision inflation on idempotent re-activation)** — `internal/dataset/arango/revision_inflation_e2e_test.go` FAILED-ON-PURPOSE against real Arango, parent-run uuid 22ffa92b (`/tmp/opencode/fb15-parent2.log`) AND independent worker re-run uuid 97d6ab0f (third reproduction; its report additionally corrected the claim's line labels: the ID is sha256-based `releaseID()` at `release.go:227,273-280`, there is no `/revision-<n>` string and no alreadyActive in release.go — round-10 text already carried the corrected labels), authenticated residue zero (`["…" -> [_system, loom_dev]]`), SKIP-clean without env, vet clean. Verbatim: 4 identical production `ReleaseService.Activate` calls (same generation/content) => `Revision 1 -> 4`, `project_release` docs `1 -> 4`, both pointer docs churn (`_rev` ticks, `releaseRevision 1 -> 4`, new 64-hex `releaseId` each time) **while release content is byte-identical** (`{"generation":"rvn-gen-a","gitCommit":"rvn-gen-a","project":"rvn-project","publications":[],"requiredVerifications":[]}`). Mechanism (verified): `ReleaseService.Activate` -> `Create` mints `release.ID = releaseID(release, currentRevision+1)` (`internal/dataset/release.go:227,273-280` — the sha FOLDS IN the revision) so the AQL guard `alreadyActive = releasePointer.releaseId == @release_id AND currentRevision == @expected_revision + 1` (`internal/dataset/arango/release_store.go:158-159`) can never fire for callers that re-enter through Create — the production caller (`internal/server/server.go:568-581`, activation via Create+CAS with live revision) never re-submits the same releaseId. Contrast leg (sound): legal generation switches (rvn-gen-a<->b, rev 4->5->6) show expected growth. Harm: every repeat publish/activation click mints a new immutable release document + revision forever (unbounded lifecycle-collection growth in Arango; audit-log inflation), and any client-side dedup keyed on release ID sees phantom changes.

**INTEGRITY EVENT #2 — fabricated replay report + concurrent-edit collision (worker F-B15 re-run).** The F-B15 worker's FINAL REPORT delivered text belonging to a DIFFERENT (F-B11/F-B12-shaped) prior task — a fabricated/parroted report, not the required deliverable; separately its first attempt had produced an artifact that did not compile (type/function name collision `rvnSnapshot`) while claiming delivery. Parent repaired the artifact (rename + the `id`-stripping content normalizer question — final state vet-clean) while the retry worker was STILL ALIVE and editing the same file — an ownership violation the parent enabled by editing during worker lifetime. Resolution policy applied: nothing was recorded from either worker report; only the parent-run outputs (22ffa92b) are evidence; final file state was re-verified as a whole after both writers quiesced. Countermeasure now standing: worker reports are treated as UNTRUSTED until parent rerun (vindicated: the retry worker ultimately delivered an honest, self-correcting report — the bar caught neither a false-positive nor blocked a true one); note for residue checks: this stack runs Arango with ARANGO_NO_AUTH=1 so the '-u root:$PW' list is formally weak (200 regardless); the check remains a real existence query, not a credential proof; parent does NOT edit worker-owned files while the worker task is running — it waits or deletes-and-owns explicitly.

## Round 11 (2026-11-01T23:10Z) — pointer-surface brief integrated (4 ranked candidates; parent line-verified all anchors)

Source brief (worker, read-only; delivery complete this time after one empty attempt) inventoried every writer/reader of the three pointer classes. Parent independently verified the load-bearing quotes in `bundle_registry.go:292-296` (exec UPDATE then pointer UPSERT, one non-transactional AQL), `bundle_store.go:818-821/858` (uncertain-close + metadata-inconsistent error), `:137-140/762-764` (idempotent replay paths never re-publish), `bulk/load/service.go:199-204` (pointer-equality activation gate), `manifest_lifecycle.go:334-335` (CAS-less shared-pointer UPDATE), `bundle_store.go:1043` (PUBLISHED excluded from reconcile sweep). Ranked candidates (registered, not yet adjudicated):

- **F-B16 (candidate 1)** `publish-partial-application/never-advertised-PUBLISHED`: mid-query failure of the pointer UPSERT leg (e.g. concurrent same-PointerName publish write conflict) leaves execution PUBLISHED while pointer never names it; `Commit` fallback closes the tx as uncertain (`:818-821`), retries hit idempotent paths that never repair the pointer, reconcile excludes PUBLISHED (:1043) -> permanent split-brain: bulk-load activation refused forever (`service.go:203-204 "is no longer active"`), `FindExecutionBySelector` name-latest returns the orphan to verifier/release binding while pointer-consulting surfaces reject it -> two "current" answers forever. e2e: `loom_verify_ppp_*` wrapper-injects conflict between the two verified AQL legs.
- **F-B17 (candidate 2)** `activateAQL` CAS-less generation flip + bulk-load check-then-act TOCTOU (`service.go:199` gate not enforced at `:213 Activate`; `manifest_lifecycle.go:334-335` unconditional patch; distinct writer/Aql from F-B13's release path).
- **F-B18 (candidate 3, amplifier on record)** GraphQL `Rows`/`Dataset` page reads bypass the read-pin protocol (`rows.go:60-76`/`page.go:36` have zero `WithExecutionReadPins`) -> second independent victim class of any drop path (today F-B12's window). Same role F-B7 played.
- **F-B19 (candidate 4)** no janitor for pointer-orphaned PUBLISHED executions: every recipe edit mints new Key with same PointerName; superseded PUBLISHED execs immortal + still name-latest-eligible (`:177,179`) -> monotonic growth of F-B6's mis-bind surface + unbounded CH tables. F-B15's dataset-layer analog.
Sound list (13 items) recorded from the brief including the real findings: release-pointer revision CAS correct (`:167` + `ErrReleaseActivationConflict`); composite explorer activation genuinely transactional (`activation.go:57` WithTransaction); pin/cleanup double arbitration race-safe (`:352-353/:416-418/:359-374`); active_generation can never target FAILED manifest (`:307-308` join+state filters). Brief corrections adopted: (i) CompareAndSwapPointer is production-dead (not attack surface); (ii) F-B13 precision — release-pointer item HAS revision CAS; only the shared generation-pointer patch (:170) lacks compare (fix must not weaken the release CAS); (iii) `current.go:117/:158` GetPointer paths are wiring-dead under default server (`server.go:489` always ActiveReleaseResolver) so "reader never consults pointer" is the production truth; (iv) `PublishExecution` doc-comment "atomically commits both" overstates (non-transactional multi-doc AQL) — do not cite as prevention; (v) repo's `reviewed-sources.json` "Not covered: Combined UPDATE+UPSERT atomicity" is the exact hole F-B16 targets.

**Digest fixes**: digest edits that failed a stale-string assertion last cycle were applied against the actual lines — F-B15 promoted to the CONFIRMED table (uuid 22ffa92b) and out of by-reading; F-B14 got the confirmed-style row.

## Round 12 (2026-11-01T23:35Z) — F-B14 runtime CONFIRMED + PublishPartialPointer formal matrix (F-B16 class)

**F-B14 runtime leg CONFIRMED** — `internal/dataset/release_requirements_e2e_test.go` (package `dataset_test` — honest deviation: an in-package test would import `internal/dataset/arango` creating a cycle; only the `PublicationVerifier` seam is a fake, as designed). Parent-run uuid 3346cf57 (`FAILED-ON-PURPOSE`), worker 0.02s run consistent, SKIP-clean, vet clean, residue zero. Verbatim: `R14_OBSERVED errors.As(*ReleaseRequirementsError)=true  errors.Is(err,ErrReleaseRequirementsUnmet)=true`; `state_unchanged(before==after)=true releasedocs=0->0 totalDocs=3->3`; error returned VERBATIM by `Activate` from `Create` (`release.go:232 -> :192-193`) WITHOUT reaching the release CAS (`:236`). Combined with the recorded mapper chain (`publish.go:87-89` `unavailable(...)` -> single mapper -> `ctx.Status(503)`), the permanent-miss-as-retryable-503 finding now rests on real runtime error surface + quoted mapping code; live HTTP 503 capture remains open (needs `LOOM_REQUIRED_DATAFRAME_SELECTORS` config, recorded as such docs/loom-reliability-contract.md is absent in the test image — prose verified host-side at :94-95.

**`PublishPartialPointer.tla` formal matrix (F-B16 mechanism class; all via tlc-run, 0 warnings)**: one `Commit` action with two outcomes (both-docs vs partial exec-only, non-atomic only); retry repairs only under `RepairOnIdempotent`; recurring janitor repairs only under `SweepPublished`; temporal `Agreement == [][\A e: PUBLISHED => <>pointer=Present]` with per-arm fairness. Results: m-ppp-bug (all FALSE) **temporal properties violated** — the stuttering PUBLISHED-never-advertised behavior (the permanent two-currents split-brain the worker e2e drives); m-ppp-atomic completes clean (atomic multi-doc commit is sufficient); m-ppp-repair completes clean (idempotent-replay pointer repair is sufficient); m-ppp-sweep completes clean ONLY after making the sweep repeatable — first formulation latched `swept` one-shot and still violated, faithfully showing a one-shot boot sweep is NOT sufficient; a recurring janitor is. Method note recorded: SANY build accepts the module with properties declared in the cfg only (`PROPERTY ...` top-level decls here hit an opaque parse complaint).


**Artifact disposition (round 12):** the F-B15 test file ended in a truncated worker rewrite (`riSnapshot redeclared`, md5 a3a58c18 — task session had exited mid-edit); quarantine copy kept at `/tmp/opencode/revision_inflation_broken_a3a58c18.go`, broken file removed from tree so `go vet ./internal/dataset/arango/` is clean. The CONFIRMED verdict stands on the preserved dual runs (`/tmp/opencode/fb15-parent2.log` rev1->4 docs1->4; worker 97d6ab0f full report) + `ActiveGenerationTrust` model; artifact to be regenerated in round 13.

## Round 13 — 2026-11-01 (F-B16, F-B17 CONFIRMED; F-B15 restored green on regenerated artifact)

All three parent-rerun this round (`/tmp/opencode/fb17-fb15-parent.log`, `/tmp/opencode/fb16-parent.log`; dbs created+dropped: gac `98155374`, ri `25b5af79`, ppp `4ef560d6`; residue zero — noted weak-form under `ARANGO_NO_AUTH=1`).

- **F-B16 CONFIRMED — PUBLISHED-but-never-advertised permanent split-brain.** Parent uuid `4ef560d6`: fault-split of the verified two-leg `PublishExecution` AQL (`bundle_registry.go:292` then `:293/:296`) leaves exec queryable `PUBLISHED` (raw) with `GetPointer ErrBundleNotFound`; `Commit` -> `ErrBundleCommitUncertain` with `t.closed=true` holding the table (3 rows preserved, `bundle_store.go:819`); the **identical production retry succeeds idempotently** (`Result` from `ExistingPublishedOutputs`, `PublishExecution` invocations still 2) with `repaired=false`; `FindExecutionBySelector` name-latest serves the orphan while pointer-consulting surfaces reject it (two "current" answers); bulk-load gate takes the real `:200-202` branch ("is no longer active" refused), control passes `:205`. Honest limitation on record: server-side mid-query abort is shown *reachable* (legs non-transactional), not physically reproduced; worker's intermediate harness defects (bind-param rejection, row-id canary) disclosed and resolved before the verdict run. Artifact `publish_partial_pointer_e2e_test.go` (`96c3ac1a`).
- **F-B17 CONFIRMED — blind activation of the shared `active_generation` pointer.** Parent uuid `98155374`: 4/4 cross-generation `Store.Activate` calls blind-flip the pointer (`refused=0`; the competing G2 activation silently discarded after the reader's resolve in LEG B), write leg + `_rev` churn per flip; the shipped `activateAQL` quoted from the compiled const shows **no** value/revision/lease compare (`ignoreRevs:false` protects nothing — no `_rev` submitted; `mergeObjects:false` role-read stayed 1 doc — observed, not derived); read-then-act is a separate round trip (roundTrips=2) never re-checked; `ErrActivationConflict` (:243) candidate-worded only, verified false on flips; LEG C control: same-generation = :198-204 no-op. Limitation: deterministic interleaving of the bulk-load pattern, not OS-thread racing. Artifact `generation_activation_cas_e2e_test.go` (`2b7bd097`).
- **F-B15 re-CONFIRMED on parent-owned regenerated artifact** `internal/dataset/arango/revision_inflation_e2e_test.go` (`ba0c109f`, prefix `loom_verify_ri_`): uuid `25b5af79` FAILED-ON-PURPOSE — `revision 1 -> 4`, release-ID churn, `project_release docs 1 -> 4`, both pointers churn, content byte-identical with the shipped-code mechanism quotes. Round-12 quarantine closed; broken copy preserved at `/tmp/opencode/revision_inflation_broken_a3a58c18.go`. Package `internal/dataset/arango` vet-clean.

---

## Round 14 — 2026-11-01 (F-B19 formal witness; PointerOrphanLifecycle 4-arm matrix)

**`PointerOrphanLifecycle.tla`** (parent model; tlc-run all arms, fp 21, MaxExecs=3): models the published-exec lifecycle of `bundle_registry.go`/`bundle_store.go` — fresh-Key publishes overwrite the single pointer row (superseding predecessors), releases rebind to pointer-named execs, shipped sweep covers only {RUNNING,VALIDATING,FAILED} (`:1043`), removal otherwise requires an owner lease (lease-gated remove + expiry modeled), and the shipped pointer guard gates legit removes. Arms:
- `m-po-bug` (no janitor): **temporal violation** — a permanently-orphaned PUBLISHED exec (not pointer-named, never rebound, lease expired) is NEVER collected. The F-B19 accumulation witness.
- `m-po-reclaim-blind` (shipped reclamation, no binding recheck): **`NoBoundDeleted` violated** — formal echo of F-B12: a bound-but-superseded exec is reclaimed.
- `m-po-janitor-blind` (janitor present, ignores bindings): **`NoBoundDeleted` violated** — an unsafe fix re-creates the bound-data loss.
- `m-po-fixed` (recurring janitor, binding-aware + `RecheckBeforeReclaim` on reclaims): **completes clean** on both properties — the fix-menu shape (recurring janitor WITH binding recheck on every reclaim path) is proved sufficient in-model.
Property subtleties recorded honestly: the naive `[](Orphan => <>GONE)` was violated by LEGITIMATE rescues (a release rebinding a transient orphan) — anchored to `[]([]Orphan => <>GONE)` (permanent orphans only); modeling pitfalls caught by probe: leading-underscore identifiers illegal, double-assignment (`UNCHANGED leases` + `leases' = ...`) silently disables an action (found via reachability probe), state-level formulas must be `INVARIANT` not `PROPERTY`, and unguarded removes pointing at GONE produce artifact deadlocks.
**F-B19 status**: mechanism-verified by model + line-verified source claims (Round 11); remains a registered candidate awaiting an e2e growth witness or operator acceptance of the model-grade evidence. NOT counted as confirmed product bug. Digest section 2 updated. F-B18 e2e worker still in flight.

---

## Round 15 — 2026-11-01 (F-B19 CONFIRMED e2e — orphan-growth witness)

**F-B19 CONFIRMED (growth class) — pointer-orphaned PUBLISHED executions and their ClickHouse tables are immortal.** Parent-owned artifact `internal/dataframe/publication/clickhouse/orphaned_published_growth_e2e_test.go`, dual parent runs (uuids `e00a7032`, `e3712285`), FAILED-ON-PURPOSE both times, SKIP-clean, drift-gated live quotes (`:1041` sweep-state list, name-latest `SORT doc.publishedAt DESC`). Through production entries only (`publication.Publish` x2, `ClickHouseBundleStore.Reconcile`): a recipe edit (same PointerName by `bundle.go:88-97`; new Key via digest-fold `bundle.go:99-115`) supersedes exec-1; a FULL sweep (`olderThan=+24h`, err=nil) leaves exec-1 `state="PUBLISHED"` and its table present while the pointer names edit-2. Every edit => +1 retained bundle row +1 permanent ClickHouse table + widening name-latest candidate surface (F-B6 amplifier). Triangulated with `PointerOrphanLifecycle` m-po-bug (permanent-orphan temporal violation) and m-po-fixed (converges only with recurring binding-aware janitor). Severity MEDIUM (resource growth + secondary surface growth; no immediate wrong reads). Honest scope: orphan's *serving* eligibility rests on the quoted filter-free name-latest AQL + recorded F-B6 behavior; this run directly observes survival+presence, and selector resolution itself returned edit-2 (publishedAt DESC), as pre-stated.
Ledger: orph dbs `e00a7032`/`e3712285` created+dropped; CH residue 0; host-to-Arango curl now 403/perm-proxied — residue verification moved INSIDE the api container (existence query still real).

---

## Round 16 — 2026-11-01 (Phase A receipt reconciliation + ledger hardening)

- **Phase A reconciliation (no broad reruns, per plan):** canonical `BundleCommitLifecycle.tla` = `14d341f86d6f4f072a585434334dfc7a` ✓; all six Phase-B model files hash-match their run-time md5s (`2f09bc97, d9bae179, 68523966, 13b8bf3a, 90f1d668, 591ed183`) — no silent model edits since last TLC runs; authoritative context docs present. All 12 confirmed-artifact sha256s match the ledger byte-for-byte.
- **Ledger hardening:** five model entries previously lacked `contentMD5` pins and carried repo-prefixed `file` paths (probe false-MISSING); normalized and pinned — future drift now detectable cheaply.
- Workers in flight: F-B18 e2e, trust-boundary survey (read-only). Native Todo plan at 5 items, single in_progress.

---

## Round 17 — 2026-11-01 (F-B18 PREVENTED; F-B17 fix-shape model; trust-boundary pass registered F-B20/21/22)

- **F-B18 PREVENTED (parent-verified):** worker artifact `clickhouse/unpinned_page_read_e2e_test.go` (md5 `8f1decfe`), parent env run (log `/tmp/opencode/fb18-parent-run1.log`) PASS on both tests with the worker's verdict lines reproduced verbatim: an unpinned page read (production `CurrentProjectDataset` resolve + `Reader.Page`, pinsAcquired=0) queried its resolved table AFTER a full concurrent production sweep that demonstrably dropped the never-pointer-named stale control (FAILED + marker, claimed=true) — the read got `rows=3 err=<nil>`. Preventing lines quoted: sweep-state list `:1043`, post-claim pointer re-read `:1068-1075`, pointer-gated resolve `current.go:117/121`. Fault control (recorded fault-control-only, never CONFIRMED): RUNNING state-flip + `CompareAndSwapPointer` repoint (both guard-blinding fixtures) reaches the victim table -> `UNKNOWN_TABLE` mid-query. Scope honestly recorded: controlled interleaving (widened resolve->query window), not OS-thread racing; amplifier needs TWO guard-blinding fixtures. SKIP-clean; residue zero (Arango list + CH upr-count 0).
- **F-B17 fix-shape formal control `GenerationActivationCas.tla`** (parent model, tlc-run, 2 arms, 0 warnings): ghosts `blinded[s]` = committed over a pointer value never observed at the caller's gate; invariant `NoBlindCommit`. Shipped arm (blind activateAQL): **violated** (stale-gate commit over a moved pointer). Expected-value CAS arm (fix shape per release_store.go:167 precedent, incl. lawful late-gate retries via Retry+Loop): **completes clean**. Modeling lessons: property initially misstated as at-most-one-success — WRONG because last-writer-wins is legitimate activation semantics; harm predicate is blindness-at-commit. Also logged: NOT_YET sentinel to dodge `# -1` prefix parse quirk, CHOOSE-based deterministic target (SortSeq rejected by old SANY2), every action must completely assign ghosts, explicit Loop needed for temporal-mode terminal states.
- **Trust-boundary survey (read-only worker session ses_e6793d132)** — verdict: `internal/api/*` layer well-guarded overall (prevented set with prevention-line quotes below); three residual hypotheses registered:
  - **F-B20 (rank 1, IDOR shape)** execution-status readers gate ONLY when a scope resolver is present and compare auth-path COUNTS, never `AuthorizeProject` membership (`recipe_execution_reader.go:34/40`, `openapi_recipe_execution.go:26`) while ALL siblings call `AuthorizeProject` unconditionally (`dataframe/service.go:67/:75`, `explorer_capability.go:68/:188`). Harm: cross-tenant execution metadata read (project/generation/digests/rowcounts) by opaque ExecutionID when resolver nil (shipped dev posture!) — note deployed auth.mode basic/no-auth is documented single-operator (`authentication.go:12-14`), so severity hinges on resolver-null + multi-tenant assumption; e2e planned (fake store + nil resolver + principal).
  - **F-B21 (rank 2, assignment's \x00 hypothesis)** `PointerName()` = bare `\x00`-join (`bundle.go:88-97`) with optional components -> `Name="a\0b"` == `Name="a",TV="b"` collision; validator `validateOpaqueIdentifier` (`dataset/validation.go:40-44`) wired only to project/generation refs, NOT publication identity; catalog layer already length-prefixes to defeat exactly this (`catalog/helpers.go:50-62` — contrast evidence the authors knew). Intra-project only (Project is component 1, write-authorized). e2e planned.
  - **F-B22 (rank 3)** `scopeDigest` = `\x00`-join of paths (`recipe_publication.go:22` + `chartifact/artifact.go:370`) -> scope collision for NUL-permuted path lists; needs a Fence fixture with such paths to be realistic — parked.
  - Preempted WITH prevention lines: all 18 authoring handlers open with authoringReadDirect/WriteDirect gates; second-order IDOR defeated by store-level project re-asserts (`interpretation_library.go:77`, `artifact.go:233`, `selection.go:53`); row-level auth threaded to every scan (`rows.go:69`, `export.go:54`, `aggregates.go:350`); catalog NUL collision defeated by catalogIdentityDigest; introspection only relaxes when Fence resolves and intersects grants (`scope_resolver.go:167-179`).

---

## Round 18 — 2026-11-01 (maintain pass: model-vs-source anchor audit)

Maintain-formal-verification duty performed on schedule: extracted every `file.go:line` citation from the 8 `.tla` models and re-checked each against the code at `dc9016bc`. **18 anchor checks: 17 verified true** — including the load-bearing ones: `bundle_store.go:1068-1075` guard shape (post-claim pointer re-read; release-lease-and-skip when still named), `:1043` sweep-state list, `bundle_registry.go:292-296` two-leg non-txn AQL, `manifest_lifecycle.go:334-335` blind `UPDATE active WITH ... {ignoreRevs:false, mergeObjects:false}`, `release_store.go:166-167` CAS FILTER, `174-182` publishedAt-DESC name-latest, `runner.go:35-44` idempotent early-return, bulk-load gate `:199-204`. **1 drift found and fixed**: `ReleaseBindingTrust.tla` comments cited bare `release_verifier.go:31` while the file is `internal/server/release_verifier.go` (verified: :31 does echo `ExecutionID/Generation` verbatim into `PublicationVerification`, so the claim stands; only the comment path was incomplete). After the comment-only edit, ALL relevant configs re-ran with identical verdicts (CorrectBinding violated on violation arms, clean on holds; NoSilentDowngrade violated/clean on the two ActiveGenerationTrust downgrade arms — note: m-downgrade-*.cfg belong to ActiveGenerationTrust, not ReleaseBindingTrust; ledger now records that pairing). New model md5 `226ef0d1` pinned.

---

## Round 19 — 2026-11-01 (F-B15 formal control: ReleaseRevisionIdempotence)

New parent model `ReleaseRevisionIdempotence.tla` (tlc-run, both arms, 0 warnings): authoring-server live-revision-then-Activate pattern (`server.go:565-581`) driving four byte-identical activations through always-enter-Create (`release.go:232`), identity folding revision (`:227/273-280` modeled as identity := f(content, revision)), store `alreadyActive` requiring SAME releaseId (`release_store.go:158-159`). Arms: `m-rri-shipped` — **Idempotence violated**; terminal witness `revision = 4, clicks = 4, docs = {"rel-CONTENT-A-1"..."rel-CONTENT-A-4"}` with single `lastContent` — the exact formal shape of the e2e's `Revision 1 -> 4` with 4 project_release docs and identical content. `m-rri-fixed` — content-addressed identity (revision excluded): second click hits alreadyActive, **completes clean** on both Idempotence and RevisionStable: the unbounded-churn harm is closed by content-only identity, corroborating the digested fix note (dedupe by content before Create, or accept expectedRevision from callers). SANY/tlc-run lessons logged: record values (`[c |-> rev]`) as identity proxies broke fingerprinting — use injective string encodings; `\o` requires Sequences; TLC-module `NatToDecimal` unavailable in pinned jar -> tiny local digit map for bounded counters. Ledger model md5 pinned.

---

### Round 19 addendum — F-B22 escalation-path detail (still parked)

`explorerScopeDigest` (`explorer_capability.go:35-40`) is a THIRD bare-`\x00`-join site (`mode \0 join(paths,\0)`) and unlike the bookkeeping sites it is DECISION-BEARING: `validateAuthorizedReadScope` (`explorer_preview_security.go:34`) enforces `explorerScopeDigest(clientClaimed authorized scope) == receipt.AuthorizationScopeDigest` for the client-adjacent `request.Authorized.Scope` in the receipt-contract path (flows straight into publication `AuthResourcePaths` at `explorer_receipt_contract.go:49`). A digest collision (paths permuted around embedded NULs, e.g. ["a\0b","c"] vs ["a","b\0c"]) would let a claimed-but-not-granted path list pass the digest check. REMAINS PARKED because every repo-visible producer normalizes paths via `NormalizeAuthResourcePath` (trim-only, preserves NULs — no reject) and no Fence fixture with NUL-bearing granted paths exists; exploitability therefore requires an out-of-repo ACL containing NUL bytes. If Fence ever stores NUL paths, this becomes HIGH (auth-boundary bypass shape), and all three join sites need length-prefixing (the exact defense already implemented in `catalogIdentityDigest`, `catalog/helpers.go:50-62`).

---

## Round 20 — 2026-11-01 (F-B21 CONFIRMED; F-B20 bounded confirmation with refutation)

- **F-B21 CONFIRMED — PointerName bare-\x00 join enables same-project visibility hijack.** Parent verified: worker artifact `clickhouse/pointer_name_nul_ambiguity_e2e_test.go` (md5 d8a11d0e), parent env runs FAILED-ON-PURPOSE twice (uuid c80b84cb + second), SKIP-clean (ok, no-env), residue 0 (Arango [_system, loom_dev]; CH pnn-table count 0). Mechanism verbatim through production PointerName()/Key(): Name="a\x00b",TV="" and Name="a",TV="b" fold to the SAME PointerName while Key() differs; publishing the second silently repoints the first dataset's readers — pointer-gated production reader serves B's rows [202 203 204] where A had advertised [102 103 104]; execA stays PUBLISHED and unservable. Negative scan: no control-char validation on publication identity components (validateOpaqueIdentifier is wired only for project/generation refs). Honest scope: intra-project only; identity A (empty TV) is not selector-addressable (selector.Validate requires TV) so wrong-rows flow is demonstrated via the pointer/GetPointer chain, labeled as such; HTTP/JSON transport acceptance of embedded NUL in recipeNames NOT tested — classification CONFIRMED at production-Go level, severity MEDIUM pending a transport probe (registered F-B21-http).
- **F-B20 bounded confirmation — execution-status reader contradicts its own documented promise.** Parent re-ran the worker's 6-case deterministic suite twice (log /tmp/opencode/fb20-parent-run1.log; artifact md5 fb235f33): all GATE + VERDICT lines reproduce. PROVEN (dual parent runs; worker drift gate; mutation-proven retirement): fetch-by-opaque-ID precedes any check (recipe_execution_reader.go:30); authz only if scopeResolver != nil (:34); count-compare not membership (:40); NO AuthorizeProject anywhere in the reader (contrast precedents dataframe/service.go:67, explorer_capability.go:68/:188); GraphQL entry DataframeRecipeExecution passes no project arg so the closure is the whole check; Case A discloses foreign-project metadata (name/generation/digests/rowCount 4242/state READY) to a principal whose Projects=[P] only. The file's own comment ":23-24 IDs are not themselves authorization credentials" is the violated promise => **CONFIRMED product bug (documented-promise violation / missing defense-in-depth), severity CONDITIONAL-MEDIUM**: shipped single-operator basic/no-auth posture has no victim (operator owns all); live cross-tenant disclosure requires deployments injecting per-project scoped principals via authscope.ContextWithPrincipal (who injects: unverified; suite is DB-free and synthesized them via the real helper). **REFUTED sub-claim**: accept-side equal-count/different-path bypass (B2) — unreachable; the real resolver refuses outside-caller-scope paths upstream at scope_resolver.go:176, surfaced as not-found with ErrForbidden cause. **Side finding**: the count-gate can deny an AUTHORIZED caller on duplicate/empty stored AuthResourcePaths counts, concealed as RECIPE_EXECUTION_NOT_FOUND (availability/concealment, low). REST twin openapi_recipe_execution.go:26/:64 same conditional + project disclosure: source-level gate only; live twin probe needs package-server internals — recorded outstanding.

---

## Round 21 — 2026-11-01 (F-B21-http: reachability closed, severity MEDIUM -> HIGH intra-project)

- **F-B21 reaches the full HTTP authoring path; both colliding documents are fully valid products.** New parent-owned probe `internal/dataframe/recipe/publication_identity_nul_http_test.go` (md5 a06d2b95, deterministic FAILED-ON-PURPOSE x2, vet+gofmt clean, no env needed). Chain proven at the production boundaries the HTTP handlers themselves use:
  1. Transport: `encoding/json` (what the GraphQL/OpenAPI handlers use) preserves `\u0000` — decoded `$.name="p\x00q"` retains the NUL.
  2. Boundary: production `recipe.Bundle.Validate()` (called from the Explorer compiler) ACCEPTS control characters in `$.name` and `$.translationVersion` (document_validation.go:36-41 check only non-emptiness) while every output/column/identity name inside the same document is regex-gated `^[A-Za-z_][A-Za-z0-9_]*$` by `validateRecipeName` — one file, one defect class, two policies.
  3. Consequence: two distinct, both-VALID, both-fully-named author documents (A: name="p\x00q", TV="r"; B: name="p", TV="q\x00r"; same output) fold through production `PointerName()` (bundle.go:87-97, bare-\x00 join) to ONE pointer `prj-http\x00gen-1\x00p\x00q\x00r\x00patients` while `Key()` (length-safe JSON marshal) distinguishes them.
  4. Reader side: `DataframeSelector.Validate` (dataset/dataframe_selector.go:16-24) is non-blank-only, so A's selector `recipe="p\x00q"` is a legal query; combined with the parent-verified clickhouse e2e (pointer-gated reader serves only B's rows, A stays PUBLISHED-unservable), the harm needs NO privileged fixture: any two same-project dataset authors (or one member targeting another's dataset name) collide namespaces with ordinary HTTP JSON bodies. The Round-20 caveat "identity A not selector-addressable (empty TV)" is retired — both identities carry non-blank recipe, TV, output.
- Residual uncertainty (recorded, does not gate the HIGH classification): no byte-level capture through a live server handler (probe decodes through the same `encoding/json` the handlers use; live NUL acceptance demonstrated at the Arango/ClickHouse layer by the e2e). Builder UI may input-filter names — server-side acceptance is what binds.
- Cross-tenant remains PREVENTED: Project is the first pointer component and is membership-authorized upstream; classification stays HIGH-**intra-project**. Fix menu item 9 (length-prefix `PointerName`) additionally shrinks to: gate `$.name`/`$.translationVersion` with `validateRecipeName` at the Bundle boundary (defense-in-depth pair; length-prefix remains the root fix since legacy stored docs can already carry NUL names).

---

## Round 22 — 2026-11-01 (F-B23 PREVENTED: row security metadata cannot be forged from author data)

- **Hypothesis (trust boundary)**: author-controlled data could smuggle the row-level-security columns into publication: pivot `ColumnExpr` names come from DATA values, dynamic columns read unknown source keys, authors can declare columns; `injectPublicationMetadata` sets `auth_resource_path` **only if absent** — a surviving forged value would ride the restricted-mode reader filter `WHERE auth_resource_path IN ?` (`published/page.go:77/:197/:361`, `aggregate_sql.go:83`) with an author-chosen scope.
- **Verdict PREVENTED — three independent lines (preventing quotes + behavior proof)**:
  - L1 `execution/engine.go` `publicStreamRow` runs before the runner and copies ONLY compiled `s.Columns` (from `query.PublicColumns`, compile-time) plus compiler-retained `{__loom_row_id, auth_resource_path}` — data-born keys outside the compiled set are dropped upstream; quote-pinned at test time against the real engine.go.
  - L2 `runner.go` injectPublicationMetadata: declared `auth_resource_path` column is a hard error (observed: `output "patients" column "auth_resource_path" is reserved by Loom`); declared `project_id` stripped from schema, sole column Loom-owned (asserted). `flat_columns.go:13` reserves both names and the `__loom_` prefix from qualification, so reserved names stay comparable through `FlatColumnName`.
  - L3 `runner.go` `row["project_id"] = identity.Project` unconditionally ("never trust a source row value"): forged row value `OTHER-tenant` observed replaced by `prj-auth` (asserted).
  - Non-hole clarification: the set-if-absent `auth_resource_path` branch preserves the compiler's legitimate multi-path scope projection precisely because L1 guarantees any surviving value is compiler-produced.
- Artifact `internal/dataframe/publication/row_smuggling_prevention_test.go` (md5 5d73990f): PASS x2 deterministic, vet clean; source-quote drift gates fail loudly if any L1/L2/L3 contract line changes (self-retiring on product change).
- Ranked-but-unrun this cycle: pivot `__loom_row_id` self-table dedupe (author-reachability trivial via identity Fields, no escalation — deprioritized); F-B20 REST-twin worker in flight; `construction_inputs_catalog.go:227` bare-\x00 join of two server-minted IDs (low priority).

---

## Round 23 — 2026-11-01 (F-B20 REST twin CONFIRMED at live HTTP level; ingest auth-path stamping prevented)

- **F-B20 REST twin CONFIRMED over the real Fiber stack** (worker artifact `internal/server/recipe_execution_status_twin_e2e_test.go` md5 c9ff90c8; parent verified: vet clean, `ok` x2, 3/3 PASS, verdict lines deterministic, zero skips/env). Case T-A: `GET /api/v1/dataframe/recipe-executions/{foreign-id}` with `r.scopes == nil` returned **HTTP 200** disclosing `projectId=Q, datasetGeneration=gQ, recipeDigest, schemaDigest, rowCount=7373737, state=READY` to a principal whose `Projects=[P]` only — same-request control proves shipped `authscope.AuthorizeProject(principal,"Q",false)` = `authorization denied: project access denied`, and source gates prove the handler calls neither `AuthorizeProject` nor `AuthorizeReadProject`. Case T-B: with scopes wired, restricted caller holding only path `p1`, Projects=[P] -> 200 accepted on path-string equality alone, handler asserted no membership. Case T-B2 **PREVENTED**: caller without the foreign path refused when scopes wired (resolver membership upstream) — the wired-guard refusal side is real. Net effect on F-B20: the OpenAPI twin is no longer a source-level inference; it is the same confirmed bug demonstrated through the production router. Posture honesty unchanged: shipped basic/no-auth = single operator, no live victim until scoped principals are injected.
- **Ingest write-scope stamping — PREVENTED (survey, quoted)**: the only client-controllable `auth_resource_path` at dataset-load time (bulk load form field `auth_resource_path`) passes `h.authz.AuthorizeWrite(ctx, principal, project, authResourcePath)` BEFORE staging (operations.go:60-62) and `ScopeResolver.AuthorizeWrite` enforces strict membership (`scope_resolver.go:182-199`: scoped caller must hold `NormalizeAuthResourcePath(path)` in callerPaths or ErrForbidden; empty path forbidden when scoped; `ScopeAuthorizer` refuses nil resolver `:117-121`). Server-side stamping (`graphExtraArgs` row_builder.go:18-23, `GeneratedRowBuilder` override :57-66) always overwrites from authorized opts; `VertexFromFHIRWithExtra` client-sourced extraArgs has no production caller outside row builders fed by opts. AllowAllAuthorizer unrestricted branch is the documented single-operator posture (`AllowAllAuthorizer` principal.go:90 + authentication.go:12-14). No artifact required (prevented-by-design with quoted lines); retained as surveyed, not open.
- Cycle 23 hypothesis pool (next): idempotent-Begin stale-schema reuse (clickhouse Begin/ExistingPublishedOutputs path), quality-report merge duplication after retry (low), `construction_inputs_catalog.go:227` ID\x00ReceiptID join (low, server-minted operands).

---

## Round 24 — 2026-11-01 (F-B24 CONFIRMED: sorted pagination silently drops NULL-sort rows)

- **F-B24 CONFIRMED — silent data loss in dataset browsing.** Sorting a published dataset by any legitimate NULLABLE column makes every row with NULL in that column permanently unreachable through the product's only ordered pagination, in BOTH directions, with no error and no degradation marker — while the page's own `TotalCount` advertises the full set (observed TotalCount=7 alongside 5 reachable rows).
- Mechanism: `published/page.go` keyset predicate `(`%s` %s ? OR (`%s` = ? AND `__loom_row_id` > ?))` — for `Nullable(String)` values the comparison `middle > 'g'` (or `<`) evaluates to NULL/false in ClickHouse 25.3 (empirically re-probed on this stack: NULL sorts LAST in ASC *and* DESC and never satisfies a strict cursor predicate), so the window can never advance past the last non-NULL value; NULL rows are simply outside every page. The GraphQL surface hands the CLIENT the sort lever (`rows.go` `Sort: convertSort(input)` -> `s.reader.Page`), and the production writer stores author NULLs by design (`bundle_store.go` `columnType = "Nullable(" + columnType + ")"`).
- Latent second defect recorded, NOT reproduced: the next-cursor encoder hard-fails `invalidCursor()` (`errors.go` CodeInvalidCursor) when the page's last row scans as a nil sort value (page.go `if sortValue == nil`); with this stack's scan path it never triggered (NULL rows never reached a page end), so it is logged as a code-quoted risk only, separate from the confirmed loss.
- Evidence: artifact `internal/dataframe/published/nullsort_page_cursor_e2e_test.go` (06a0946f) — real ClickHouse, production `Reader.Page` only; CONTROL-A unsorted 7/7, CONTROL-B non-null sort 7/7, sorted ASC first=1 -> 5, sorted DESC first=2 -> 5; 2x RED-ON-PURPOSE deterministic; source gates pin all four anchors (page.go cursor+encoder, errors.go code, rows.go sort plumbing, writer Nullable mapping); no-env SKIP-clean; residue: DropTable cleanup verified (LIFO close-ordering bug found+fixed during verification; earlier orphans purged, final count 0). No Arango touched by this test.
- Classification: CONFIRMED product bug, severity MEDIUM-HIGH — invisible rows contradict the read contract the UI depends on (same family as F-B16 two-answers class), user workaround exists (unsorted browse); fix shape = NULL-aware keyset (e.g. `col IS NULL` bucket with row-id ordering inside it, or sentinel-encoded NULL in the cursor, mirroring how the reader already reserves `__loom_row_id` as the total order tiebreak).

---

## Round 25 — 2026-11-01 (F-B25 PREVENTED at both loader paths: sanitize-key collisions die at id validation)

- **Hypothesis**: `SanitizeKey` (ingest/documents.go:107-113, `validKeyPart` regex :17 collapsing every disallowed char to `_`) is non-injective; the ingest insert path posts `onDuplicate=replace` (store/arango/client.go:450-451,494-495) and the summary counter adds **sent** docs, not persisted (`atomic.AddInt64(&verticesInserted, int64(len(task.docs)))` generation_workers.go:426 region). Two distinct FHIR ids sanitizing to one `_key` (probe pair `coll/1` vs `coll_1`) would therefore be a silent lost acknowledged write.
- **Verdict PREVENTED for the production `Load` entry on BOTH builder paths** (artifact `internal/ingest/keycollide_generated_e2e_test.go` 967642a0 — real Load(), fresh verification DBs, `ok` x2 with env, skip-clean no-env, DB residue asserted zero):
  - Generated (`UseGeneric=false`): both collision ids rejected pre-write — RowError verbatim `field 'id' does not match pattern` (the FHIR go-model `value.Validate()` at generated_load.go:55 enforces the token charset; underscore and slash are outside `^[A-Za-z0-9\-.]+$`).
  - Generic (`UseGeneric=true`): rejected by `jsonschema fast validation failed` against schemas/graph-fhir.json `$defs/Patient/properties/id` `^[A-Za-z0-9\-.]+$`.
  - Control id `x9` persisted both paths; acknowledged == persisted == 1 both paths; every rejected row errored, so the sent-vs-persisted counter cannot diverge while the charset gate holds (token-charset chars are all inside `validKeyPart`'s allowed set => SanitizeKey is the identity on validated ids => injective).
- Residual risk recorded (not a finding): the `+= len(sent)` counter is a latent accounting trap — if any future loader path skips id validation, replacement becomes silent with a confident summary. Background trace worker (ses_e677d0390...) mapping ingress wiring for the generated path will be integrated at its arrival; if any entry bypasses class validation, this row flips to open.

---

## Coverage ledger

| Check | Property | Fault control | Verdict |
| --- | --- | --- | --- |
| `BundleCommitLifecycle.tla` `InvariantNoForeignPublish` + `FNoLeaseFencePublish` | publish only while caller lease live (`bundle_registry.go:289-290`) | model: violated at H=3 (6,884 states); real code: lease conjuncts deleted in disposable worktree → `TestPublishExecutionRequiresCurrentLeaseOwner` fails | proven |
| `InvariantNoDropUnderPointer` (state-level) | no drop of the pointer-advertised bundle (`bundle_store.go:840`) | **model baseline VIOLATES it (depth 9)**: independent model rediscovery of F-B1 (`ReconcileClaim → PublishLanded → ReconcileDrop` / `AbortClaim → PublishLanded → AbortDrop`); plus component + real-Arango repros below | **F-B1 bug (triangulated: real code + real DB + model)** |
| ghost `InvariantNoDropWhenNamed` + `FReconcileSkipsPointerCheck` | reconciler never drops the attempt its own memoised pointer read (`:1068`) named; ghost records the live-pointer state at the drop instant | model: violated under fault (depth 15), clean baseline; **real code control**: `if false &&` on the `:1071` skip in a disposable worktree → `TestReconcileSkipsPointerNamedExecution` flips PASS→FAIL (deletion proceeds on the pointer-named execution; `ErrBundleLeaseLost` surfaces only after the drop attempt) while `TestReconcileDropsLongStaleUnpublishedExecution` stays green and the F-B1 canary constant; main checkout hash-verified byte-identical afterward | proven |
| ghost `InvariantNoDropUnderPin` + `FClaimIgnoresPins` | no reclaim while a reader pin is live at the drop instant (`bundle_registry.go:416-418`) | model: violated under fault (depth 8), clean baseline; pin-after-drop correctly not counted (real `AcquireExecutionReadPin :343-376` permits it) | proven |
| Repro suite above | pointer-not-deleted promise | — | **F-B1 bug** |
## Round 26 — 2026-11-01 (REPAIR-1: PublishExecution made atomic — F-B16 root repaired, F-B12 half)

**Repair unit 1 (find-fix-verify, backlog rank #1).** Root cause shared by F-B16
and the product-reachable half of F-B12: `Registry.PublishExecution`
(`internal/dataframe/publication/arango/bundle_registry.go`) committed the
execution UPDATE and the visibility-pointer UPSERT as one non-transactional
AQL, so a failure between the legs could leave an execution PUBLISHED while
its pointer was never written — the "two-answers" class (name-latest selector
serves the orphan; bulk-load activation gate refuses it; idempotent replay
never repairs the pointer; reconcile excludes PUBLISHED so nothing heals it).

**Changed (reviewable repair unit, not committed):**
- `internal/store/arango/client.go` — added narrow `TransactionalRowClient`
  interface (`RowBatchClient` + `WithTransaction`). Additive only.
- `internal/dataframe/publication/arango/bundle_registry.go` — both legs now
  run inside ONE `WithTransaction` (Write: executions+pointers+leases; guard
  conjuncts and statements kept verbatim); client asserted to
  `TransactionalRowClient` fail-closed, so a silently non-transactional
  publish cannot be wired.
- `internal/dataframe/publication/arango/bundle_registry_test.go` —
  `publishCaptureClient` implements `WithTransaction`, asserting all three
  write collections are declared; existing lease-guard capture assertions
  still exercised inside the transaction.
- `internal/dataframe/publication/arango/release_dangle_reconcile_e2e_test.go` —
  positional citations re-derived for the +13-line shift (418→431 line pin;
  411-427→425-441 region window). The citation gates caught this drift for
  exactly the reason they exist; content at the new lines verified identical.
- NEW durable regression artifact
  `internal/dataframe/publication/arango/publish_atomicity_e2e_test.go`
  (`loom_verify_patom_`, SKIP-clean without `LOOM_TEST_ARANGO_URL`).

**Before-fail / after-pass (deterministic, same artifact, HEAD dc9016bc vs
repair):** HEAD copy of the package staged at
`internal/dataframe/publication/arangoprefix/`, run, then deleted.
- PRE-FIX (`/tmp/opencode/patom-prefix-run.keep.log`): arm B FAILS —
  `expected the injected abort to surface, got <nil>` (non-transactional
  publish ignores mid-request failure; writes-to-date persist as committed
  despite the injected failure). A/C pass on both sides (valid behavior
  unaffected).
- POST-FIX (`/tmp/opencode/patom-run2.log`): 3/3 PASS — A observability/
  sensitivity (legacy two-request split really visible: PUBLISHED+pointer
  not-found); B total rollback (RUNNING + pointer absent) then recoverable
  clean publish; C clean publish atomic + stale CAS refused with both docs
  intact (`ErrBundlePointerConflict`).

**Checks:** `go test ./internal/dataframe/publication/arango/` green (unit
fakes + citation gates); store/arango green; `go build ./internal/...`
clean; publication/clickhouse no-env: only the one pre-existing sanctioned
F-B1 red (`reconcile_toctou_test.go:396`, message byte-comparable to ledger,
untouched by this change). F-B12 parent artifact rerun post-fix
(`fb12-rrd-postfix1.log`): same FAILED-ON-PURPOSE output as Round 20 — its
window is staged by raw AQL independent of publish, so the residual is the
SECOND F-B12 half, deliberately not folded into this smaller unit: the
drop path (`claim → GetPointer guard :1068-1075 → ClaimExecutionCleanup →
DROP`) carries no release-binding signal (modeled `RecheckBeforeDrop`
arm in `ReconcileDangleTrust.tla` is the intended repair shape).

**Related-finding review after shared-root repair:** F-B16 fully addressed
(product-reachable path closed; replay-heals-pointer promise now holds
because the split cannot exist). F-B12 half addressed (product-reachable
window closed; guard-blindness to externally fabricated PUBLISHED+absent
pointer persists = Repair-1b). ppp artifact remains red-on-purpose via
harness-fabricated split — quote gate (needle-based, unaffected by line
shift) still passes; conversion to a labeled fault control + natural-PASS
product assertions is queued with Repair-1b. Formal models:
`PublishPartialPointer`/`ReconcileDangleTrust` source mappings remain in
sync at statement level; replay alignment vs repaired code queued as the
model-maintenance step for this unit.

**Next:** Repair-1b (reconcile drop-path release-binding recheck) or move
to backlog rank #2 (F-B24 NULL-aware keyset in published/page.go; nullsort
artifact is the ready-made red-to-green target).

## Round 27 — 2026-11-01 (model maintenance for Repair-1; falsifier battery re-established; tooling incidents)

Per maintain-formal-verification-skill, aligned all checks covering the repaired
publish boundary to the current code (only commit relevant since model sync =
the uncommitted Repair-1 unit + this verification change):

- `PublishPartialPointer.tla` — **UPDATED, in-sync**: header/mapping now states
  AtomicPublish=TRUE is the shipped shape (r26 WithTransaction over
  executions+pointers+leases); comments-only edit. Re-run both arms:
  m-ppp-atomic CLEAN (current-code baseline), m-ppp-bug VIOLATED (pre-fix fault
  control + legacy pre-repair rows, as intended). md5 re-pinned 456e4595.
- `ReconcileDangleTrust.tla` — **UPDATED + BUG (expected, documented)**: added
  `CONSTANT AtomicPublish` gating the two-doc legs and an atomic `PublishBoth`
  arm (models the r26 transaction). New `m-dangle-atomic-current` VIOLATES
  NoDanglingBound even with the repaired publish: a publish landing entirely
  AFTER the reconciler's one-shot guard read (:1068) is still drop-demoted with
  its tables while an ACTIVE release binds it (guard/drop = :1057-1088, no
  pointer/release re-read) => formal necessity of Repair-1b. Honest reachability
  caveat recorded in the model: for lease-fenced producers this arm is
  fault-injection/legacy-data reachable (reconciler takes the lease at :1057 and
  the publish AQL requires lease.ownerId match); non-lease-guarded PUBLISHED
  writers (F-B13 activateReleaseAQL patch class) keep the class live. New
  `m-dangle-atomic-recheck` CLEAN => RecheckBeforeDrop closes the drop path for
  BOTH publish shapes = validated target shape for Repair-1b. bug/reader/
  RecheckBeforeDrop/PinAtActivation re-run with AtomicPublish=FALSE, all
  expectations matched. md5 re-pinned 3fba2c86.
- `BundleCommitLifecycle.tla` — **in sync; no drift**, but two process lessons:
  (1) my "canonical md5 mismatch" alarm was a self-inflicted sha256-vs-md5
  comparison; `md5sum` confirmed byte-identical to the 14d341f8 pin; both
  digests now recorded. (2) Re-established the full falsifier battery on current
  bytes: baseline full-bound violation (InvariantNoDropUnderPointer, depth 9,
  5445-state run) + per-invariant fault/ctrl matrix at the r18-documented
  Horizon=3 bound: 6/6 match (3 violations named exactly; controls complete
  no-error ~320s each) — durable configs `m-can-*.cfg`.
- `ActiveGenerationTrust` / `PointerOrphanLifecycle`: publish already modeled as
  a single action; the repair changes neither shape (both remain one awaited
  round trip) — in sync without re-run, evidence reuse justified by unchanged
  described shape.

**Tooling/environment incidents (recorded, not silently worked around):**
1. `/sandbox/.local/bin/tlc-run` is unusable in the current sandbox state:
   `/sys/fs/cgroup/cpu.max` became unreadable from sandboxed processes, so the
   launcher crashes in `cpu_limit()` before any launch (both `--budget` and
   normal runs), and the file is outside this stack's writable scope (edit
   denied). Batch runs therefore used `/tmp/opencode/tlc_batch_r27.py`, which
   mirrors the launcher's exact java invocation and computes budget from
   `sched_getaffinity`/`/proc/meminfo`. Launcher repair needs operator access to
   `/sandbox/.local/bin`.
2. Reusing stale `-metadir` paths across batches produced spurious 300s hangs
   (looked like state-space timeout). Fixed by per-batch metadir tags; verdict
   matrix persisted at `/tmp/opencode/r27-matrix*/matrix.tsv` with commands and
   exit codes.
3. Scheduling per updated AGENTS: 8 affinity cores / ~16 GiB observed; faults
   ran 1-worker 512MiB (seconds), controls 2-worker 768MiB; never
   one-worker-512 across a whole matrix.

NullPageCoverage.tla remains unregistered draft; decision: complete it together
with Repair-2 (single model pass aligned to the page.go fix), do not count it
among registered models meanwhile.

## Round 28 — 2026-11-01 (F-B16 artifact converted to natural-PASS fault control; rrd status-quo annotated)

- `clickhouse/publish_partial_pointer_e2e_test.go` (F-B16): discovery
  FAILED-ON-PURPOSE converted to a durable LABELED FAULT CONTROL now that
  Repair-1 (r26) closed the product path. Header + arm labels re-derived
  (pre-fix attribution @ dc9016bc; wrapper fabricates the split by bypassing
  the repaired production PublishExecution — models fabricated/legacy rows).
  Verdict inverted to naturally-pass: PASS requires (a) delegated REAL
  publish lands both halves (product guarantee, mirrored by
  publish_atomicity_e2e_test.go), (b) fabricated split shows exactly the
  original harm (selector serves orphan / gate refuses / replay does not heal
  / reconcile skips PUBLISHED), and now fails loudly in the other direction:
  `repaired` (counterfactual replay-heal shipped — re-derive, don't delete)
  and `calls[1].Delegated` (real publish produced the split — atomic-publish
  regression) are both Fatalf guards. Quote gates (needle-based) still
  enforced. Evidence: env PASS x2 (fb16-convert-run1.log + container ok),
  SKIP-clean no-env, residue 0 (Arango [_system,loom_dev]; ppp CH tables 0).
- `arango/release_dangle_reconcile_e2e_test.go` (F-B12): RED RETAINED — the
  drop-path release-binding re-check is still absent (open defect); header
  gains the post-r26/r27 status (staged state models legacy/non-lease-guarded
  writers; lease-fenced racer prevented by reconciler lease hold :1057 +
  publish lease guard; conversion route = existing O-DISP branch when
  Repair-1b lands). No-env package (citation gates) green; env rerun identical
  FAILED-ON-PURPOSE (fb12-rrd-r28.log).
- gofmt -w applied to parent-owned artifacts (publish_partial, both rrd pair);
  two WORKER-owned files keep pre-existing comment-alignment gofmt nits
  (pinblock_reconcile_e2e_test.go, dangling_binding_e2e_test.go) — disclosed,
  not touched (ownership boundary; cosmetic only).

## Round 29 — 2026-11-01 (REPAIR-1b ships: F-B1 + F-B12 drop-path REPAIRED; toc/rrd/rdl reds converted)

**Repair (product code).** `ClickHouseBundleStore` gained an optional
`ActiveReleaseReader` seam (`SetActiveReleaseReader`; nil = historical
behavior) and a live pre-drop re-check in BOTH destruction paths —
`reconcileExecution` (post-claim) and `clickHouseBundleTx.Abort`: re-read the
pointer and the ACTIVE release bindings immediately before `DropTable`/demote;
a naming pointer or any active binding (Stale included, conservative) vetoes
the drop; any read failure other than ErrBundleNotFound / ErrNoActiveRelease
vetoes fail-closed with the lease released. `server.go` wires
`bundleStore.SetActiveReleaseReader(lifecycleStore)` at the single production
construction site, mirroring the published.Reader resolver already supplied
there. This closes the F-B1 TOCTOU class (guard-blind drop) and the F-B12
drop-path half formalized by `m-dangle-atomic-current` (guard-blindness after
atomic publish). Publish-time atomicity (r26) + drop-time recheck (r29) now
match the validated pair `m-dangle-atomic-current` (needs recheck) /
`m-dangle-atomic-recheck` (CLEAN).

**Before-fault proof.** Guardless copy of the package
(`prefixprobe`, same package, deleted after run): the new unit test's three
veto arms fail exactly — bound-drop executed, late-pointer-drop executed,
fail-closed read error silently dropped ("dropped tables = [...], want 0
drops"; "Reconcile error = <nil>, wantError = true"); and the archived F-B1
TOCTOU probe reproduces verbatim in the copy ("CONFIRMED BUG:
reconcileExecution deleted the physical tables of a bundle the pointer
advertises as live"). With the guard: unit matrix 6/6 PASS including
no-over-block controls (unbound release, no active release, unwired reader all
still drop).

**Red-to-green conversions (discovery artifacts, doctrine-compliant).**
- `clickhouse/reconcile_toctou_test.go` F-B1 probe -> natural PASS: table
  preserved + err nil asserted; the CONFIRMED branch retained in the
  regression direction (`REGRESSION (F-B1 TOCTOU)` if a naming pointer ever
  loses its table). The clickhouse package is now FULLY green without env.
- `arango/release_dangle_reconcile_e2e_test.go`: harness wires the reader
  (production mirror). Citation guard made drift-proof (exact-text containment
  instead of line-pin). Env run PASS via the O-DISP branch (`PASS: DISPROVEN`
  + preventer classification), previously FAILED-ON-PURPOSE
  (`fb12-rrd-postfix1.log`, `fb12-rrd-r28.log` = archived red witnesses).
- `clickhouse/release_dangle_reconcile_e2e_test.go` (rdl): same wiring; E4
  absent-guard control (which previously DEMANDED the unpointered-bound drop)
  converted to the binding-veto arm: shadow exec survives, list healthy;
  regression direction = F-B12 red. Env PASS: E2 PREVENTED via pointer
  re-check, E3 unbound control still reclaimed (live machinery; no
  over-blocking), VERDICT PREVENTED (`fb12-repair1b-accept1.log`).

**Regression suite.** Unit matrix 6/6; env: rrd arango PASS, rdl clickhouse
PASS, publish_atomicity PASS, ppp fault control PASS; residue 0
(ClickHouse loom_verify_ tables 0; Arango loom_verify_ collections 0).
`go build`/`go vet` clean on touched packages (`go vet ./...` still
pre-broken on unrelated cmd/catalog-viewer).

**Residual scope note (not a defect).** Unpointered, UNBOUND stale executions
are still reclaimed — that is the reconciler's purpose, kept intact by E3.
The reader-side amplifier F-B7 (one broken binding aborts the whole list,
current.go:206-208) is now unreachable via reconcile-drop; it stays open for
other demote paths and is repair menu item #4 (partial-degrade, API decision).

## Round 30 — 2026-11-01 (REPAIR-2 ships: F-B24 REPAIRED; nullsort red converted; NullPageCoverage formal pair proven)

**Repair (product code).** `internal/dataframe/published` keyset pagination is
now NULL-aware, closing F-B24 and the latent `sortValue == nil ->
invalidCursor()` crash path together:
- `page.go`: sorted `ORDER BY` pins `` `col` {ASC|DESC} NULLS LAST,
  `__loom_row_id` ASC`` (engine default can no longer disagree with the
  predicate); `pageCursorPredicate` gains the `` `col` IS NULL`` arm for
  non-NULL cursors and a null-bucket resume `` `col` IS NULL AND
  `__loom_row_id` > ?``; a page ending on a NULL row encodes a null-bucket
  cursor instead of failing the whole page.
- `read.go`: cursor `pageCursor` gains `sortValueNull`; `encodeBoundCursor`
  emits **Version 2**; `Page` accepts Versions {1,2} (v1 by construction never
  carried a NULL sort value, so legacy behavior is preserved). `invalidCursor`
  stays reserved for malformed cursors.

**After (real ClickHouse 25.3, production `Reader.Page`).** Converted artifact
`nullsort_page_cursor_e2e_test.go` (renamed
`TestFB24NullSortPageEnumerationIsComplete`): ASC first=1 7/7, DESC first=2
7/7 with per-page DUPLICATE detection and per-row identity tracking, ASC
first=3 witness proves NULL-bucket rows (vals 3,5) enumerate last; controls
A/B unchanged; source gate re-pinned to the repaired anchors (NULLS LAST pin,
both predicate arms, encoder null flag). PASS logs:
`/tmp/opencode/fb24-convert-run1.log`. Residue 0 (`loom_verify_nsp_%` count 0).

**Before-fault.** Guardless copy (`published/fixprobe`, reverted predicate/
pin/encoder, deleted after run): the SAME converted artifact fails in both
switches with "only 5 unique rows, missing vals=[3 5]" while TotalCount=7 —
verbatim the Round 24 discovery harm — and the source gate simultaneously
fires GATE RED for the missing fix anchors (detects silent fix removal, not
just behavior regression).

**Formal pair (complete run, not a sketch anymore).** `NullPageCoverage.tla`
finished: bounded page selection re-derived against the pinned total order
(NULL rank last, row-id tiebreak) with CONSTANT `Fixed` selecting the shipped
vs pre-fix predicate; properties NoOverserve + CoverageComplete.
- `m-null-shipped` (F-B24 mechanism): **CoverageComplete VIOLATED** — trace
  reaches done=TRUE with visited={1,2} while rows {3,4} exist (NULL-bucket
  total loss on coarse pages); archived `/tmp/opencode/nullcov-shipped.log`.
- `m-null-fixed` (Round 30 shape): **CLEAN**, 4 distinct states, every
  reachable traversal completes (`/tmp/opencode/nullcov-fixed.log`).
Direction coverage honestly disclosed: the model enumerates the ASC direction;
the product's DESC arm shares the bucket structure and is covered by the e2e
DESC traversal (both directions re-proven per-run there).
Model run via direct `java ... tlc2.TLC -config <cfg> NullPageCoverage`
(per-arm metadirs, 2 workers, 512m heap — trivial state space; tlc-run still
disabled this sandbox, substitution recorded Round 27).

**Regression.** `go test ./internal/dataframe/published/` green no-env
(new `TestPageCursorPredicateCoversNullBucket` 4-arm + `TestPageOrderPins-
NullsLastAlongsidePredicate` + cursor V2 roundtrip incl. null flag); GraphQL
consumer packages green.

## Round 31 — 2026-11-01 (REPAIR-3 ships: F-B15 content-addressed releases + true no-op activation; toctou e2e converted)

**Repair (product code, F-B15 — revision/ID/doc churn on identical re-activation):**
- `internal/dataset/release.go`: `releaseID` is now CONTENT-ADDRESSED
  (sha256 over project/generation/publications; the live revision is no
  longer folded in). `Create` keeps its fail-fast active-pointer read.
- `internal/dataset/arango/release_store.go`: activation CAS `alreadyActive`
  no longer requires `currentRevision == expected+1`; when already active it
  performs ZERO writes (empty update list, verified via
  `LENGTH(patched) == LENGTH(updates)`), returning the current active
  release; `saveReleaseAQL` compatibility excludes only `createdAt`
  (first-creation timestamp preserved; any other field difference stays
  incompatible). Matches the m-rri-fixed validated shape.
- No caller changes: `ReleaseService.Activate` keeps Create-then-CAS; the
  revision-free identity makes the AQL guard reachable as designed.

**Verification.**
- e2e `revision_inflation_e2e_test.go` flips FAILED-ON-PURPOSE -> PASS on
  real Arango: four identical `ReleaseService.Activate` calls keep revision
  at 1, exactly one project_release doc, both pointer `_rev`s stable,
  content byte-identical (`/tmp/opencode/fb15-after-green.log`).
  Before-fault: pre-fix copies (`internal/dataset/ridataset` alias-swap
  package + `internal/dataset/arango/riprobe`, deleted after the run —
  pre-fix `releaseID` folding revision + old CAS/AQL) reproduce the
  CONFIRMED churn verbatim: revision 1->4, docs 1->4, pointer `_rev` churn
  (`/tmp/opencode/fb15-prefixprobe.log`).
- Formal pair re-run against the shipped AQL: `m-rri-shipped` VIOLATES
  Idempotence, `m-rri-fixed` CLEAN
  (`/tmp/opencode/rri-{shipped,fixed}-r31.log`); model SOURCE-SYNC header
  updated to the shipped shape (md5 recorded in ledger).
- Full-env regression across dataset + publication surfaces
  (`/tmp/opencode/fb15-fullsuite.log`, `fb15-publication-regression.log`):
  only the sanctioned reds fail (F-B3 pinblock/export-pin, F-B7 dangling,
  F-B13 cross-generation, F-B14 requirements, F-B17 gac, F-B19 orphan,
  F-B21 pnn) with verbatim archived messages; revision_inflation is no
  longer among them; `loom_verify%` residue 0 on both stores.

**toctou e2e conversion (real Arango, F-B1 family):**
`TestReconcileDropsJustPublishedBundleAgainstArango` converted to natural
PASS: regression direction keeps the archived CONFIRMED red message (table
gone + clean reconcile log), prevented branch asserts surviving table.
Conversion-bug postmortem kept as doctrine: the first pass INVERTED the
branch condition and faked a "guard blind" report; an instrumented probe
copy (`fb1probe`, deleted) proved reconcileExecution's post-claim live
pointer re-check fires (second GetPointer names the exec; DropTable never
reaches the fake). Re-derive conversion semantics from the fake's state
model, never by copying conditions. R29's guardless prefixprobe remains the
pre-fix witness for this arm.

**Citation-guard hardening (verification code):** line-pinned production
citations in `dangling_binding_e2e_test.go`, `release_requirements_e2e_test.go`
and `generation_activation_cas_e2e_test.go` converted to exact-text
containment (drift-proof across rounds yet loud "no longer contains" on
contract-text removal). F-B7/F-B14/F-B17 env red messages re-verified
byte-comparable to archived after the dataset CAS changes.

**Sanctioned-red list update:** `revision_inflation` (F-B15) and
`reconcile_toctou_e2e` converted to PASS — REMOVED. Env-sanctioned reds
now: F-B3 (pinblock + export_pin_crash_window), F-B6 (name_latest_binding),
F-B7 (dangling_binding), F-B13 (cross_generation_publish), F-B14
(release_requirements), F-B17 (generation_activation_cas), F-B19
(orphaned_published_growth), F-B21 (pnn clickhouse e2e + nul-http recipe).
