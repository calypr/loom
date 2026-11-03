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

## Round 32 — 2026-11-01 (REPAIR-4 ships: F-B19 binding-aware PUBLISHED janitor arm)

**Repair (product code, `internal/dataframe/publication/clickhouse/bundle_store.go`):**
- `Reconcile` appends `publication.BundlePublished` to the sweep states ONLY
  when an `ActiveReleaseReader` is wired — a janitor that cannot re-check
  bindings is the `m-po-janitor-blind` shape (`NoBoundDeleted` violated), so
  unwired readers keep the historical enumeration (unit-proven).
- `reconcileExecution` gains a PRE-CLAIM janitor filter for PUBLISHED
  candidates: pointer still names it OR the ACTIVE release binds it ->
  skipped WITHOUT claiming (healthy published rows never churn); "no
  pointer"/"no active release" pass, any other read failure vetoes
  fail-closed. The post-claim pointer + binding re-checks (Repair-1b) remain
  the destruction gate -> shipped behavior = model `m-po-fixed`
  (JanitorPresent+JanitorChecksBindings+RecheckBeforeReclaim).
- Demotion marker distinguishes `"superseded execution reconciled (no
  pointer or active-release reference)"`; the legacy
  `"stale execution reconciled"` assignment stays verbatim (unpinned
  artifact citation needle preserved). Server janitor (server.go) inherits
  the arm via existing wiring.

**Verification (real Arango + ClickHouse, owned stack).**
- Converted `orphaned_published_growth_e2e_test.go`
  (`TestPointerOrphanedPublishedNeverReclaimedAgainstArango`) natural PASS:
  post-sweep orphan FAILED with superseded marker, table absent
  (present=false), live pointer-named execution + 3-row table untouched,
  name-latest resolves to the live execution
  (`/tmp/opencode/fb19-after-green.log`); drift gates re-pinned (stale-arm
  list + janitor presence + marker text + name-latest SORT needle).
  REGRESSION direction keeps the archived F-B19 CONFIRMED text.
- BEFORE-FAULT: guardless copy `clickhouse/orphprobe/` (Repair-4 hunks
  reverted, Repair-1b kept, deleted after run) reproduced the immortal
  orphan VERBATIM — post-sweep `state="PUBLISHED"`, table present=true, the
  converted artifact failing on the REGRESSION branch
  (`/tmp/opencode/fb19-prefixprobe.log`). R15 parent runs e00a7032/e3712285
  remain the historical pre-fix witnesses.
- NEW e2e safety sibling `TestPointerOrphanedPublishedJanitorRespectsActive-
  ReleaseBindingsAgainstArango`: ACTIVE-release binding vetoes retirement
  (orphan untouched, reader called once = pre-claim skip), unbind + resweep
  converges to FAILED+dropped — m-po-fixed shape on real stores (same log).
- Units: `TestReconcileRetiresSupersededPublishedExecutions` 5 arms +
  stale-marker control green no-env (retired / pointer-named never claimed /
  bound never claimed / release-read failure fail-closed / unwired PUBLISHED
  not enumerated).
- Formal: `PointerOrphanLifecycle.tla` SOURCE-SYNC header records the shipped
  shape; trio re-run via direct java tlc2.TLC — `m-po-bug` Temporal
  properties VIOLATED (counterexample), `m-po-fixed` CLEAN exit 0,
  `m-po-janitor-blind` `NoBoundDeleted` VIOLATED
  (`/tmp/opencode/po-{bug,fixed,janitor-blind}-r32.log`).
- Full-env re-run of `publication/...` + `published/` after the arm: fails =
  sanctioned subset ONLY — F-B7 dangling (arango), F-B3 export-pin +
  pinblock, F-B21 pnn (clickhouse); unpinned F-B18 attribution gate survived
  (named candidates skip pre-claim); conversion note: F-B6 name_latest is
  PASS-with-CONFIRMED pattern (not an env-fail), F-B13 lives in
  internal/explorer/arango (untouched track). Residue 0 both stores.

**Sanctioned-red list update:** `orphaned_published_growth` (F-B19) REMOVED
(converted PASS + safety sibling added). Env-failing reds: F-B7 dangling,
F-B3 export_pin_crash_window + pinblock, F-B21 pnn; dataset-track reds
(F-B14, F-B17) and explorer-track (F-B13) unchanged from Rounds 31/15
evidence.

## Round 33 — 2026-11-01 (REPAIR-5 ships: F-B21 injective PointerName; docker access lost mid-round)

**Repair (product code, `internal/dataframe/publication/bundle.go`):**
- `BundleIdentity.PointerName()` is now LENGTH-PREFIXED per component
  (`strLen:content` concatenation via `strconv.Itoa(len(part))` + `':'`),
  mirroring the catalog's `catalogIdentityDigest` discipline and
  complementing the JSON-shaped `Key()`. The shipped semantics preserved
  EXACTLY: blank/whitespace-only TranslationVersion and OutputName are
  still excluded as absent; live components keep raw bytes (incl. trailing
  spaces); multibyte parts are prefixed by BYTE length.
- Closes the archived CONFIRMED intra-project namespace hijack
  (Name="a\x00b"/TV="" vs Name="a"/TV="b" folded to one visibility
  pointer while Keys stayed distinct => publishing B silently repointed
  A's readers) AND the deeper boundary-shift class (NULs at component
  ends, digit/colon spoofs like Name="1:ab" vs Name="2"/TV="a").
  Root-cause choice per operator digest item 9 (injective encoding),
  NOT a control-char validator: encoding injectivity makes NUL names
  harmless for namespace separation; validator-side NUL acceptance
  stays a disclosed defense-in-depth note (F-B21-http `t.Errorf`
  converted to `t.Logf` NOTE accordingly, red direction preserved as
  `FB21-http REGRESSION`).

**Verification.**
- Units (package publication, no-env): `TestPointerNameLengthPrefixDefeats
  NulFold` pins the historical fold via a verbatim `historicalBareJoin`
  replay, requires production separation + distinct Keys;
  `TestPointerNameIsInjectiveOverDistinctComponents` (8 adversarial
  component assignments, all pairwise distinct);
  `TestPointerNameOptionalComponentSemanticsAreShipPreserved` pins
  blank-exclusion + raw-space behavior + byte-length prefix; documents the
  boundary equality where (TV=x, blank Out) and (blank TV, Out=x) share a
  namespace with the historical join — NOT a fold-class defect: a blank
  OutputName cannot publish (production gate), so no pointer can be stolen
  that way. Pre-F-B21 code passed all four; the fold test goes REGRESSION
  the moment the length prefix is dropped.
- Before-fault (probe): guardless copy `publication/fb21probe/` replaying
  the PRE-REPAIR bare-join method PASSes the fold witness verbatim
  (both PointerNames == "pnn-project\x00gen-1...a\x00b..."; Keys distinct;
  `/tmp/opencode/fb21-prefixprobe.log`).
- Converted artifacts: pnn e2e `pointer_name_nul_ambiguity_e2e_test.go`
  natural-PASS doctrine — citations re-pinned to the length-prefix
  production shape (`strconv.Itoa(len(part))` / `WriteByte(':')`), header
  documents STEP 3-5 + the archived FAILED-ON-PURPOSE F-B21 CONFIRMED text
  as the retained REGRESSION direction; recipe sibling
  `publication_identity_nul_http_test.go` flipped per its own
  re-point doctrine (distinct pointers => VERDICT PREVENTED).
- ENV CAVEAT (disclosed): the sandbox's Docker access was revoked
  mid-round ("Loom Docker access is not configured. Run
  tools/openshell/enable-loom-docker.py on the host" — the script is not
  present in this sandbox; host-side manual step). The e2e PREVENTED
  proof for these two conversions therefore runs against ARCHIVED
  parent-verified baseline behavior only; re-verify on the owned stack
  when Docker access is restored (same command documented in the pnn
  header). No product claim is made from these two artifacts pending
  that re-run; the units + probe above carry the verified weight.
- No-env regression green: publication, publication/arango,
  publication/clickhouse, published, recipe, dataset, dataset/arango;
  gofmt/vet clean (worker-owned pinblock nit excluded).

**F-B22 same-class digest review (operator menu item 9) — classification,
no blind fix:** `recipeScopeDigest` (server/recipe_publication.go),
`scopeDigest` (chartifact/artifact.go), `explorerScopeDigest` /
`fhirReadScopeDigest`, `releaseID` (dataset/release.go) all use
`\x00`-joins. Reachability audit: every digest input is server-computed
(project/generation refs pass `validateOpaqueIdentifier`; AuthResourcePaths
arrive via authorization snapshots/RuntimeBindings assembled by server
code — `document_types.go` only clones them; no client-nul-bearing entry
point found). Unlike PointerName, these digests are PERSISTED (stored keys
+ stored digest fields), so re-encoding them is identity churn of stored
data for currently unreachable inputs. Classification: **unreachable by
audit today (rank LOW)**; fix only when a concrete reachable injection
entry point is shown, ideally behind a stored-identity migration. Trigger:
any future client-controlled path/name reaching a digest input.

## Round 34 — REPAIR-6 (F-B20): unconditional project membership + path-set gate in both execution-status twins

- Root cause (parent-verified against source, matching the archived characterization): the GraphQL reader consulted authorization only inside `if scopeResolver != nil` and its restricted gate was a path-COUNT compare; the REST twin carried the identical conditional shape (`if r.scopes != nil`) with no membership assertion at all. A principal whose project allowlist excluded the execution's project — or any caller when the resolver was unwired — could read cross-tenant execution metadata (project, generation, digests, row counts) by opaque id alone, exactly what the reader doc comment promises must not happen ("IDs are not themselves authorization credentials").
- Repair (both twins, shared rule): unconditional `authscope.AuthorizeProject(principal, execution.Project, false)` immediately after fetch, refused with the concealed not-found shape (`RECIPE_EXECUTION_NOT_FOUND` GraphQL; byte-identical missing-execution 404 envelope REST); restricted-mode gate replaced by the new shared `authscope.ReadScope.CoversExecutionPaths` normalized path-set containment (restricted only; fail-closed when a restricted execution declares no non-blank path). The shipped concealed-403 resolver-refusal contract and the resolver outage mapping are preserved; nil/empty-Projects principals keep their shipped pass-through and the principal-less single-operator deployment posture is unchanged (authorizer semantics, not a new bypass).
- Side finding closed with the same change: the count-compare's over-rejection (a member authorized on every distinct path was denied when raw and normalized counts differed, e.g. duplicate declarations) is fixed; verified by the C1 REPAIRED verdict.
- Before/after: REST twin durable regression failed pre-fix with verbatim disclosure (`status 200` + `projectId:project-b...rowCount:42...` in /tmp/opencode/fb20-twin-before-red.log) and passes post-fix. Both characterization suites were converted to durable repaired-behavior controls: STEP 0 now pins the repaired fragments and forbids the archived shapes; every archived CONFIRMED sentence survives verbatim as its case's REGRESSION-fatal wording; the membership-before-resolver ordering is proven by catalog-probe call counts (non-member refusal performs zero catalog consultations while the resolver path for members does).
- Review: the background read-only F-B22 digest audit returned this round; all load-bearing quotes were parent-verified in current source (classification and tiered follow-up in ledger `status_updates.F-B22`; same-model reviewer context, not cross-model assurance).
- Checks: `go test` green for internal/server (with pre-existing, file-unmodified env-gated replay test `TestCoreAuthoringProtocolModelReplay` excluded via `-skip`; it requires `LOOM_FORMAL_CORE_PROTOCOL_INPUT/OUTPUT` at HEAD), internal/authscope, and resolver FB20 + full packages; `gofmt` clean on all touched files. ENV matrix pending docker access restoration.

## Round 35 — REPAIR-7 (F-B22 Tier-1): control-byte rejection at authorization-path ingest

- Root cause (parent-verified against HEAD): scope-digest encoders join normalized authorization paths with \x00, but the authscope normalizers accepted ASCII control bytes. Two distinct restricted grant lists could therefore normalize to the same digest (HEAD witness: a\x00b|c joins identically to a|b\x00c), and the resolver-less GraphQL read-scope fallback copied client- and IdP-supplied strings into scope membership checks with no normalization at all.
- Repair: ingest now refuses control bytes (C0 + DEL) in `NormalizeAuthResourcePath`, in calypr key extraction, and in resource-derived auth paths — such declarations drop out of scopes fail-closed (empty never grants). The query fallback normalizes both client-requested and principal paths; an all-garbage non-empty request is refused `CodeInvalidRequest`, a principal whose paths are all garbage is refused `CodeForbidden` (never widened to unrestricted, because empty paths historically mean unrestricted for legacy catalog callers); membership is compared in normalized space, closing the raw-equality slash-spelling false-deny side finding.
- Durable controls: authscore ingest suite (including the HEAD fold-pair) and the query fallback suite keep the pre-fix CONFIRMED wording verbatim as their regression branches. HEAD-copy before-red captured (7 authscope + 4 query CONFIRMED verdict lines), post-fix all REPAIRED with the full query, authscope, and server package suites green.
- Environment restored by the operator: owned stack containers up, `loom-dev-build-stamp.sh --check` passes, Arango 3.12.13 + ClickHouse reachable from the API container; the full real-services ENV matrix (including every prior pending-env flip: revisions, pointer-name NUL, activation, orphan janitor, F-B24 NULL page, F-B20 twins) launched against this stack as a background verification job.
- Record correction: the reconciliation worker's claimed "conservative_stats" red artifact exists nowhere (zero grep hits) — ledger annotated as a fabricated verification record; the authentic F-B9 is the low-severity AQL case-tolerance candidate with its existing adjudication unchanged.

## Round 36 — real-services ENV matrix: zero regressions, pending-env annotations flipped (full dispositions below)

The owned Stravinsky stack made the previously blocked live verification possible (API container `go test` with LOOM_TEST_ARANGO_URL=http://arangodb:8529; ClickHouse jobs additionally need LOOM_CLICKHOUSE_URL + database/username/password trio — the LOOM_TEST_CLICKHOUSE_URL spelling does not exist). Sequential JAR1-5 + supplements, per-job 1500s deadlines, logs under /tmp/opencode/r35-env/.

- Every repaired mechanism verified against the real stores: F-B15 revision-inflation PASS, F-B21 pointer-name NUL twins PASS, F-B19 orphan janitor PASS, F-B12-artifact rdl DISPROVEN, F-B24 NULL-sort page PASS, F-B20 twins 9× VERDICT=REPAIRED, Repair-1b/Repair-4/F-B16/F-B18/F-B9-chain PREVENTED, publish-atomicity PASS. No-env control SKIPs cleanly.
- The only FAILs are the ledger-sanctioned reds, reproduced verbatim against real Arango/ClickHouse: F-B17 blind activation flip (manifest_lifecycle, CAS decision queued), F-B7 whole-list dataset-discovery abort (partial-degrade decision queued), F-B3 stranded-never-released pins wedging reconcile+startup (renew/reap posture queued). No product regression, no infra error, no unexpected skip.
- Remaining live-verification gaps recorded: the two read-pin arbitration artifacts need LOOM_TEST_ARANGO_DATABASE with an isolated database name (deferred, deliberately not pointed at the shared dev DB).
- Disclosure: the background TLA audit worker returned empty; model source-sync this round rests on the parent's direct hash verification (canonical 14d341f8, PublishPartialPointer atomic-Commit arm = repaired shape, PointerOrphanLifecycle m-po-fixed = shipped janitor) rather than that worker.

## Round 37 — F-B15 artifact hardening
Verdict switch in `internal/dataset/arango/revision_inflation_e2e_test.go` tightened: `case !inflated` -> `case !inflated && contentIdentical`, so content moving while bookkeeping stayed still now falls through to the INCONCLUSIVE fatal instead of logging PREVENTED (worker-reconciliation follow-through; parent verified the switch and precondition gates first). FAILED-ON-PURPOSE arm kept verbatim; header updated to state REPAIR-5 repaired status with PREVENTED witness. gofmt+vet clean; single-test rerun vs real Arango PASS (sound arm fired; owned DB created+dropped) — /tmp/opencode/round37-rri-tightened.log. New pin ab4cf9341193f900fbae267520ac7ac5.

## Round 38 — Read-pin arbitration artifacts closed against isolated real Arango DB (env)

**Closure.** The two disclosed env gaps (`TestExecutionReadPinArbitrationAgainstArango`,
`TestConcurrentExecutionReadPinsAgainstArango`) now have real-Arango evidence: executed
inside the API container against a **throwaway isolated database**
(`loom_verify_pinarb_r38`, created via `POST /_api/database`, pointed at via
`LOOM_TEST_ARANGO_DATABASE`) so the shared dev DB was never touched; both PASS
(`/tmp/opencode/round38-readpin-isolated.log`,
`--- PASS: ... (0.50s)`, `--- PASS: ... (0.04s)`, `ok ... 0.547s`).
The throwaway DB was then **dropped and verified dropped** (`_api/database` lists only
`loom_dev`): the naive `wget --delete-after` drop silently 400'd (wget cannot issue
DELETE); the verified drop used an HTTP DELETE. Doctrine noted for the artifact family:
owned-database cleanup must be an actual DELETE with a read-back, not a best-effort.


## Round 40 — REPAIR-8 (F-B27) and REPAIR-9 (F-B14) landed and re-verified live

### REPAIR-8 — F-B27 cross-project dataset publish/activate authorization bypass
- Root cause: `scope_resolver.go` `AuthorizeWrite` checked global existence only (comment claimed project membership was covered by `checkDatasetWriteAccess`; no server caller passes that option, server.go:818-823) and `listExistingPaths` forced `Generation: opts.DatasetGeneration` with authoring always passing "" -> generation-"" binds (catalog `generation()` maps "" to null) matches only no-generation rows, so stale-generation paths were invisible to the gate.
- Repair: project-scoped existence via recorded-path membership (`listExistingPathsAllGenerations`, internal sentinel, fail-closed on list error), plus opt-in `catalog.AuthResourcePathOptions.AllGenerations` with a second bound-variable-free AQL (`existingAuthResourcePathsAllGenerationsAQL`) mirroring the existing shape; generation string never overloaded ("" keeps meaning no-generation rows).
- Evidence: pre-fix durable legs FAIL `/tmp/opencode/fb27-prefix-red.log` (`F-B27 CONFIRMED publish` + `F-B27 CONFIRMED activate`); post-fix same four controls PASS live after the arangodb container restart (isolated throwaway databases); `authscope`, `catalog`, `catalog/arango`, `load` package suites ok.
- Review status: adversarial reviewer (same model, disclosed) verdict pending for bypass routes / normalization / poisoning TOCTOU.

### REPAIR-9 — F-B14 authoring Publish conflated permanent contract failure with transient outage
- Root cause: `publish.go` PrepareRelease error branch classified every error `unavailable/503` including typed `dataset.ReleaseRequirementsError` (contract: 422 unprocessable, non-retryable); repo already models this pair correctly at `repository_publish.go:73-77`.
- Repair: classification at the origin via `errors.As(*dataset.ReleaseRequirementsError)` -> `failureDetails(ClassUnprocessable, activation, RELEASE_REQUIREMENTS_UNMET, ..., retryable:false)`; genuine failures keep the prior Unavailable/503 + logged cause exactly.
- Evidence: pre-fix `F-B14 RED: ... "unavailable" (want "unprocessable")` `/tmp/opencode/fb14-prefix-red.log`; post-fix both controls PASS; full lifecycle suite ok. Server transport maps ClassUnprocessable->422 (authoring v2 + legacy mappings inspected) and propagates Details (openapi_explorer_authoring_direct.go:750, :791).
- Recorded-but-untouched: `repository_publish.go:79` conflates genuine store/IO 409 with Retryable:true (same anti-contract pattern, other flow; queued for a separate unit).

## Round 41 — REPAIR-9 revision (F-B14b): verifier fault vs contract miss
Adversarial review of REPAIR-9 (same-model reviewer) returned needs-revision: the
release.go required-loop folded ANY VerifyPublication error, including Arango
transport faults passed raw by publicationVerificationStore, into the typed
ReleaseRequirementsError, so a transient fault became 422 retryable:false and
skipped >=500 server logging. Parent independently verified all quotes.
Revision at the producer: dataset.ErrPublicationRecordMissing marks deterministic
absence (server adapter maps publication.ErrBundleNotFound with double wrap);
release.go Create returns a plain wrapped error for every other verifier error so
publish.go keeps it unavailable/retryable. Optional loop unchanged (binding-
conservative, disclosed). Durable controls: dataset split triplet (pre-fix RED:
fault folded into typed) and server mapping control (pre-fix RED via HEAD overlay,
/tmp/opencode/fb14b-server-mapping-red.log). r14Citations still match (content
matcher; cited miss-path condition line preserved). Suites: dataset ok 0.045s,
lifecycle ok 9.377s, server ok 6.662s (-skip foreign replay, disclosed).
Status: repaired, pending REPAIR-9b re-review; reviewStatus note in ledger.
Sibling repair (F-B14b): repository_publish.go ActivateRelease blanket-conflict split
the same way — typed miss → conflict + explicit retryable:false diagnostic; store
fault → unavailable + retryable diagnostics; pre-fix RED in
/tmp/opencode/fb14b-sibling-prefix-red.log (HEAD overlay, both controls FAIL),
GREEN lifecycle full suite 9.855s. Lifecycle F-B14 control file was rebuilt after
an accidental truncate-in-place; all four lifecycle controls PASS post-fix and the
two new ones were proven FAIL pre-fix. Self-inflicted tooling error disclosed.

## Round 42 — REPAIR-9b re-review relaunched; RRI model maintenance

- REPAIR-9b adversarial re-review relaunched after the operator fixed child
  creation (prior four attempts failed pre-start): background read-only general
  worker `ses_e66a9e81affeztngiUM6M73K0t`, same-model (disclosed), verdict pending.
- Per maintain-formal-verification-skill, checked every model covering code the
  round-41 repair changed. Only `ReleaseRevisionIdempotence.tla` maps
  `internal/dataset/release.go`; the fault-vs-miss edit is an early return
  outside the modeled idempotence granularity — no modeled-shape change, so the
  update is source-pin-only (Create call :247, Create :147, releaseID :288-295,
  release_store alreadyActive :162-163; also corrected a long-standing wrong
  wiring cite to internal/server/server.go:569-580).
- Drift found while running as-is: the SOURCE SYNC comment block had lost its
  opening `(*` — its prose parsed as code and TLC failed at line 23. Origin
  undetermined (r31 /tmp logs already recycled; the mangled "e00a..." line
  shows earlier header surgery). Restored the opener explicitly. New md5
  3286288a; prior pin 108a932f was stale-by-content either way.
- Re-run via `/sandbox/.local/bin/tlc-run` — launcher RECOVERED (r27's
  cpu.max breakage no longer reproduces; budget line read fine). Concurrent,
  1 worker / 1024 MiB each, private metadirs: `m-rri-shipped` exit=12
  "Invariant Idempotence is violated" (durable fault control — proves the
  updated check can still fail); `m-rri-fixed` exit=0 complete clean
  (2 distinct states, 0 violations; MSBDiskFPSet fingerprint-collision
  warnings are small-space noise, logged as-is).
  Logs /tmp/opencode/rri-{shipped,fixed}-r42.log.
- Canonical `BundleCommitLifecycle.tla` untouched this round (md5 remains the
  14d341f8 pin). CORRECTION to text written earlier this round: I first claimed
  "no other model references the round-41 files" while my own pending grep had
  already shown `ReleaseBindingTrust.tla` citing `release_verifier.go:31` —
  restated after verification. RBT then maintained per skill: pin re-pointed to
  `:35-39` (adapter insert shifted the echoed literal), four arms re-ran with
  identical verdicts (case/violation VIOLATE CorrectBinding exit=12; holds/caseholds
  exit=0 complete, 69 distinct, 0 queue). New md5 2be7da6d (prior pin 226ef0d1).
- REPAIR-9b re-review (background read-only general worker
  `ses_e66a9e81affeztngiUM6M73K0t`, same-model, disclosed): VERDICT NO-BYPASS.
  Parent independently re-verified findings 5/6 quotes (legacyDetails drop at
  openapi_explorer_direct.go:71/:217-218; api.ts:925 status inference;
  publish.go:75 explicit-details leg). Suites verbatim ok across dataset/server
  (-skip foreign replay)/lifecycle/build. Arango-gated r14 e2e SKIPped (recorded
  incomplete, not passed). Two PRE-EXISTING contract findings queued:
  F-B28 (repository-publish HTTP body drops Error.Details so explicit retryability
  never reaches clients — contract prose docs/loom-reliability-contract.md:93-94)
  and F-B29 (ui api.ts:925 infers retryable from status; live contradiction:
  PUBLICATION_IN_PROGRESS emits explicit retryable per errors.Retryable() at
  publish.go:75 with 409, read as false by the UI).

## Round 43 — REPAIR-10 (F-B28): legacy Explorer encoder restores explicit retryability

Defect (pre-existing; surfaced by REPAIR-9b, parent-verified): the
PublishRepositoryExplorerConfig direct handler pre-flattened structured
lifecycle failures via `legacyStringFailure(status, legacyErrorMessage(legacy), err)`
(openapi_explorer_direct.go:217-218), discarding code AND `lifecycle.Error.Details`
before the generated wrapper ever saw them; the shared encoder chain
(`lifecycleLegacyFailure` → `legacyOperationFailure` → `explorerLegacyResponse`)
had no Details channel at all. Contract violated:
docs/loom-reliability-contract.md:93-94 (explicit retryability).

Repair: Details field on `explorerLegacyOperationError`;
`lifecycleLegacyFailure` forwards `value.Details` via new
`legacyOperationFailureDetails` (old constructor kept for existing callers);
`explorerLegacyResponse` attaches `AdditionalProperties{"details": ...}` only
when non-nil (mirrors authoring-route body shape; no invented keys); route
returns `lifecycleLegacyFailure(err)` once so the status-mapping wrapper
(openapi_explorer_routes.go:67-88, already 409/422/503-capable) encodes it.

Durable control: `openapi_explorer_legacy_details_control_test.go` —
activation-miss marshalled envelope must carry `details.retryable:false`
(RED pre-fix /tmp/opencode/fb28-prefix-red.log; both tests PASS post-fix);
companion anti-widening test pins that non-lifecycle errors gain no
retryable/details keys and the generated AdditionalProperties contract.
Blast radius: sole encoder for the four direct explorer routes — additive for
list/create/get; no remaining flatten sites. Recorded-not-widened pre-existing
gap: wrapper has no 404 case (identical behavior pre/post my route edit).
Server suite ok 6.974s (-skip foreign replay, disclosed); build/vet/gofmt clean.

## Round 44 — REPAIR-11 (F-B29): UI client honours explicit retryability

### F-B29 — UI client inferred retryability from HTTP status (repaired-verified R44)

Defect: `ui/packages/loom-ui/src/api.ts` `requestError` derived retryability solely from the HTTP status (`408 | 429 | >=500`) and ignored the server's explicit `error.details.retryable`. Live contradiction: `PUBLICATION_IN_PROGRESS` is emitted by the authoring publish path with `retryable: true` over HTTP 409 (`internal/explorer/lifecycle/publish.go:75`), yet the UI read it as `false`.

Root cause: the request-error envelope already carried `details`, but `requestError` consulted only the status heuristic before deciding retryability, violating `docs/loom-reliability-contract.md:93-94` (explicit retryability) on the client side only.

Fix (single logical change, verified confined to `requestError`): `details = isRecord(nested.details) ? nested.details : undefined` (api.ts:918); `explicitRetryable = typeof details?.retryable === 'boolean' ? details.retryable : undefined` (api.ts:919); `retryable: explicitRetryable ?? (response.status === 408 || response.status === 429 || response.status >= 500)` (api.ts:927). A non-boolean `details.retryable` (e.g. `'true'`) is treated as absent by the `typeof` guard and falls back to status inference. `git diff` vs HEAD: single -U3 hunk `@@ -915,14 +915,16 @@`; the GraphQL branch (api.ts:1859-1871) is absent from the diff (untouched, still `extensions.retryable === true`); no `as` casts added.

Durable coverage: new `ui/packages/loom-ui/src/api.retryability.unit.test.ts` (5 tests): explicit true-on-409, explicit false-on-503, fallback 503->true, fallback 409->false, non-boolean-as-absent. Record-writer self-run (this session, logs `/tmp/opencode/rec-r45-*.log`): retryability 5 passed; `api.unit.test.ts` 86 passed; `reconcileRecovery.unit.test.ts` + `previewRecovery.unit.test.ts` 11 passed (2 files, run from package root); `npm run typecheck:tests --workspace @calypr/loom-ui` EXIT 0. Fault sensitivity (record-writer self-run via the `vitest.config.ts:11-20` overlay): weakening the guard to `details?.retryable !== undefined` (copy `/tmp/opencode/rec-r45-guard-broken-api.ts`, run under `LOOM_MUTATION_TARGET`/`LOOM_MUTATION_SOURCE`) fails test 5 with `expected 'true' to be true` while tests 1-4 pass (`rec-r45-e-mutant.log`); clean rerun 5/5 (`rec-r45-e-clean.log`); worker equivalent `r44-guard-mutation.log`/`r44-guard-clean.log`. Worker-reported: consumer blast-radius 7 suites / 334 tests (`verify-r11-13-consumer-suites.log`); full loom-ui package 85 files / 1110 tests (`verify-r11-16-full-suite.log`); semantics probe 9 passed (`verify-r11-11-semantics-probe.log`); `--reporter=basic` unsupported on vitest 4 (`verify-r11-14-basic-reporter.log`).

Attribution + status: REPAIR-11 fixed and independently verified (verifier `ses_e669f6414ffe225p8pY1xA3GWt`, same-model, disclosed); record-writer self-ran checks a-e and independently diff-verified the change scope. No first-person attestation is made beyond the runs listed above.

Record integrity note: committer path-claim 'file absent' disproven by self-run grep + probe (tracked at features/ExplorerBuilder/); reviewer 30-test formal run reproduced non-vacuous (vitest zero-match exits 1); M3 kill double-reproduced. Three-party contradiction resolved with committer as the erroring party — process worked (probe dispatched before any upgrade).

### Additional records (R44-R45, attributed)

- F-R1 REPAIRED-VERIFIED (REPAIR-12 + independent verifier `ses_e66950a5fffeQ9IIbz0VzV3S2M` VERIFIED, rounds 45-46; RED overlay re-proved; 4 focused PASS; full server suite 7.195s) — `getRecipeExecution` folded store faults into a non-retryable 404, asymmetric to the GraphQL twin (`recipe_execution_reader.go:81-99`); fix classifies sentinel `ErrBundleNotFound`->404, faults + fail-safe unknown -> typed retryable 503 `BACKEND_UNAVAILABLE`, and nil-err `ID==""` stays 404. RED `fr1-prefix-red.log` / `verify-r12-red.log` (2 fault cases FAIL pre-fix; keep-404 guard PASS by design); GREEN focused 4 PASS ok 0.081s `verify-r12-focused.log` + 0.083s `fr1-green.log`; full `./internal/server` ok 7.195s `verify-r12-full.log` (logged value; operator brief said 7.307s); build/vet/gofmt logs empty (worker `ses_e6698e4e8ffe04OvmuBssQ3KVc`). Record-writer `go test -run TestGetRecipeExecution` 4 PASS ok 0.096s EXIT 0 `rec-r45-f-fr1-gotest.log` (supporting only). Independent verifier `ses_e66950a5fffeQ9IIbz0VzV3S2M` (same-model, disclosed) VERIFIED via go-test `-overlay` whole-file HEAD swap (verify-r12-overlay.json -> verify-r12-head.go), RED re-proved (verify-r12-red.log), diff classified (verify-r12-diff.log) — attributed to that verifier session; record-writer did not repeat the deep audit.
- F-R2 (RECORDED, worker-reported): Reconcile is startup-only + first-error-wins: any error disables recipe publication process-wide (`server.go:551-563` `bundleTarget` nil, no retry); stale RUNNING rows only reclaimed at next boot; overlaps the open H2-a/F-B3 read-pin family (`pinblock_reconcile_e2e_test.go:254`, `export_pin_crash_window_e2e_test.go:556`). Operator-judgment follow-up; not duplicated against the F-B6 lineage.
- F-R3 (RECORDED, low, worker-reported): ETL recipe-execution status omits the explicit failure code/retryable/phase that the GraphQL twin publishes (`openapi_recipe_execution.go:62-80` vs `recipe_execution_reader.go:59-75`); FAILED state still renders correctly, so it is a contract gap, not success-on-error. Candidate REPAIR-13 (additive fidelity).
- F-B31 (CLOSED-vacuous, worker-reported `ses_e669a4c53ffeda7JR6AV1qkSht`): the generic-REST top-level `retryable` sibling (`internal/api/http/errors.go:27`) that the nested-details client never reads is provably disjoint on the ETL routes; explicit values live inside `details` (post REPAIR-10/11) and the GraphQL path uses `extensions.retryable`. Caveats recorded no-finding: panic-path zero-value false vs `api.ts:927` infer flip; contract-letter fallback note.
- F-CLI1..6 (QUEUED; audit worker `ses_e669cdd3affeBrhvZq4NdGyMxx`, parent-triaged, UNVERIFIED by record-writer): (1) CLI silent CreateDatabase-on-typo false success (`internal/store/arango/client.go:138-150`; rank1; groundwork `ses_e66971baeffe65lBLSg9zs2CVE`); (2) documented `load` command absent from dispatch (`main.go:43-67` vs QUICKSTART.md:46 / README.md:134 / contract :76; capability lib-side `generation_orchestration.go:20-25`); (3) mutating cmds lack signal/ctx so Ctrl-C strands a LOADING manifest and re-run is blocked (`manifest_lifecycle.go:97-99`, `:249`; no adopt path); (4) `--mode` silent fallback to storage (`main.go:634`, `relationships.go:20-37`); (5) LATENT exit-0-on-partial `loadResource` (`resource_load.go:100-104` vs `:149`; twin generation path correct `:289-296`) - must land with (2); (6) low: exit-code split 2-vs-1.

### Additional records (R46-R47, attributed)

- F-B32 (REPAIRED-VERIFIED, repair-14; combined adversarial review `ses_e6681b2e8ffelZTJAzjW7wDD6P` NO-BYPASS, same-model/disclosed): REST `getRecipeExecution` `authscope.ErrForbidden`->403 leaked dataset existence vs the GraphQL twin + REST sibling 404 (authz existence oracle, `ses_e669350e2ffewAY9d7VXlW2kMK` confirmed-by-quote); single-arm flip `openapi_recipe_execution.go:132`->404 plus coordinator-authorized migration of the REPAIR-12 helper pins :49-50/:83-84 to 404. RED overlay A/B signatures: RED A (remove fault-branch call) -> exactly REPAIR-12's 2 fault tests FAIL exit 1, keep-404 + pre-existing-503 PASS; RED B (:143 flip) -> both migrated pins + T-B2 FAIL, fault tests PASS (orthogonal); byte-identical 503/UNAUTHORIZED_PROJECT envelopes; `fb32-*` logs; sole skip disclosed (full package 7.500s).
- Commits landed (authorized): `1973b9ae` F-B14 family (7 files, +341/-1, 53->46); `8ac0fd9d` F-B28 / Explorer legacy Details (2 files, +89/-4, 46->44) - both hashes + `git show --stat` totals re-verified by record-writer. Commit wave 2 in flight: recipe-execution trio (unit C), CLI quintet (D, post-review), UI retryability suite (E, post-coverage).
- F-B33 (RECORDED, operator-decision, worker-reported `ses_e6681b2e8ffelZTJAzjW7wDD6P`): getRecipeExecution OpenAPI drift post-F-B32 - 403 (`openapi.yaml:1250` LegacyForbidden) DECLARED-BUT-UNREACHABLE: handler is exhaustively {401,404,503} (no StatusForbidden arm); adapter 403 branch + `GetRecipeExecution403JSONResponse` (`openapi_data_routes.go:162-167`) are dead code. Deliberately NOT silently fixed (spec removal + regeneration blast radius); do not delete a declared capability without operator sign-off. status: recorded-operator-decision.

## Round 49 — adversarial reviews land: repair-6 NO-BYPASS, repair-7 CORE-SOUND-REMEDIATION-OUT, repair-8 FAIL-REQUEST-CHANGES -> REPAIR-15; dispatch waves REPAIR-15..18

Attribution: review verdicts and dispatch contents are attributed to the named reviewer/worker sessions (same-model where not otherwise disclosed); the record-writer did not re-run the deep audits. Where the record-writer quote-verified a saved log or source line this session, the row says so.

- **repair-6 (F-B20) -> VERIFIED-REVIEWED-NO-BYPASS** (same-model reviewer `ses_e66757becffe8uCNimGXbEeIfc`). Twin decision table fully aligned post-F-B32: all authz rows MATCH, incl. restricted-missing-path concealed on both transports. The AllGenerations cross-generation hypothesis is REFUTED by probe: the read twin never enters the all-generations namespace; `ReadScope.Covers` is a normalized-string subset test only. Reviewer findings + dispositions:
  - F-2: the covers-subset loop is uncovered by durable tests — mutant C survives (twins pass because they test requested==declared). Durable controls REQUIRED pre-commit: dispatched with the REPAIR-15 rework per one-writer (shared `scope_resolver.go`), with REPAIR-16 covering the REST-side behavioral twin case now. Record-writer quote-read `/tmp/opencode/review-fb20/mutantC_probe.log`: mutant C makes `CoversExecutionPaths([p1 p2])` with restricted scope `[p1]` return true (want false) on the missing-path probes.
  - F-3: REST `Covers` arm is text-pin-only -> REPAIR-16.
  - F-1: `context.Canceled` -> REST retryable 503 vs twin 499 — OWNER ACCEPTED AS DOCUMENTED DEVIATION: the client-cancel response is undeliverable anyway, and the REST adapter's declared valid-set lacks 499 for the route, so parity needs an OpenAPI spec change + regeneration; bundled under the F-B33-class operator question (declared-response drift).
  - F-4: default-arm `INTERNAL_ERROR`/retryable=false vs the docstring and the GraphQL twin -> REPAIR-16 fixing.
- **repair-7 (F-B22 Tier-1) -> CORE-SOUND-REMEDIATION-OUT** (reviewer `ses_e66724ac8ffe3EQq5KhztfChEM`). Consumer table normalized-consistent for ALL calypr consumers; CH/AQL byte-equal `IN` semantics verified; cursor fingerprint proven scope-independent (live predicate per request); fold-pair + control guards durable-killed (M1/M2). Findings:
  - F-7R1: `==` sentinel cache-key collision — CONFIRMED INDEPENDENTLY by BOTH reviewers (bi-directional probes); same defect as repair-8 F-1, single fix under REPAIR-15 (type-level bool discriminator).
  - F-7R2: `dataframe/service.go:85` raw-copy -> REPAIR-18 U1 (approved; latent).
  - F-7R3: bulk/load NUL ingress has zero durable coverage and silently blanks paths under `AllowAll` -> REPAIR-18 U2.
  - LEDGER CORRECTION (reviewer-verified; record-writer SELF-RUN `ls internal/api/bulk/load/` + `rg -l ingress internal/api/bulk/load/` corroborates — zero hits): repair-7's claimed "bulk/load ingress test" does NOT exist. The sole bulk/load control test file is `internal/api/bulk/load/cross_project_dataset_authorization_e2e_test.go`, FB27-labeled (repair-8's gate; record-writer quote-read its F-B27 CONFIRMED lines at :163-164/:188-189). The repair-7 evidence record is corrected accordingly; the authscope/query-suite claims are untouched.
  - Out-of-line observation — HARDENING TICKET recorded, UNASSIGNED: `projectid.Canonical` percent-decodes project ids (`internal/projectid/projectid.go:20` `url.PathUnescape`; record-writer source quote-check), so `%00`-encoded tricks can reach prefix digests. No demonstrated collision and no named victim: hardening ticket only, no model without a named consumer.
- **repair-8 (F-B27) -> FIXING**: adversarial review (previously IN FLIGHT `ses_e66755a0dffe5bc866wZ3sUDTT`) returned **FAIL-REQUEST-CHANGES**; recorded. REPAIR-15 dispatched (`ses_e666b08e0ffeYVsC86jYbVMQ2Q`): F-1 fix-first with the reviewer counterexample as the RED oracle — `/tmp/opencode/fb27-review/m2.log` `TestFB27RevSentinelCollisionPoisonsWriteBinding` FAIL: "F-B27 BYPASS CONFIRMED: sentinel cache collision let a projA-scoped principal write /projA/data into projB whose recorded path is projB/data" (record-writer quote-read, not re-run); durable poison probe promoted into the durable suite; F-2 dual-mode rejection; the acceptance re-review re-runs the probe before acceptance.
- **Dispatch waves** (session ids as supplied in the operator brief; REPAIR-16/17/18 ids were given truncated):
  - REPAIR-16 `ses_e6669312ff...` — REST default-arm alignment (repair-6 F-4) + Covers behavioral twin case (F-3/F-2 REST side), with overlay-mutant proof via /tmp copies only.
  - REPAIR-17 `ses_e6667cd4cff...` — browser re-evidence (M5 journey-run7 / m2join DE-run6) with container-env bootstrap. Joined-path brief verdict recorded as evidence taxonomy: C08/C09 = "materially stale vs 9764eeda, now certified by api.retryability.unit.test.ts"; browser items ts-stale pending P1/P2; the server-side joined path is PROVEN live post-repair incl. the reconcile 400 seam + builder 503-no-gen (expected); probe residue `joinqa-1793578055` noted inert.
  - REPAIR-18 `ses_e666787a0ff...` — U1 `dataframe/service.go:85` normalizer + U2 bulk/load durable ingress control, approved above.
- **Commit gate**: repair-6's NO-BYPASS is not commit-clean (F-2 durable controls required pre-commit); repair-7 has remediation-out items (F-7R1 via REPAIR-15, U1/U2 via REPAIR-18); repair-8 is FIXING. The recipe-execution trio remains commit-BLOCKED until REPAIR-15 lands and its probe-rerun acceptance re-review passes.

## Round 50 — REPAIR-18 CLOSED + 18b dispatched; REPAIR-16 CLOSED-PENDING-REVIEW (seam surface); REPAIR-17 CLOSED — browser recert GREEN

Attribution: closures, run results, and dispatch contents are attributed to the named worker sessions; the record-writer this session SELF-VERIFIED the key cited artifacts — quote-read `/tmp/opencode/fb18-u1-red.log` (VERDICT=CONFIRMED pre-fix archived shape: CH `auth_resource_path IN` list `["a\x00b" "p1"]`; garbage-only `["a\x00b"]`; both tests FAIL pre-fix) and `/tmp/opencode/fb16-u1-red.log` (both arbitrary-authz-fault tests FAIL pre-fix demanding retryable BACKEND_UNAVAILABLE); confirmed on disk `fb22_principal_auth_path_control_test.go` (dataframe pkg), the REPAIR-18b ingress guard at `operations.go:53`, the seam var at `internal/server/openapi_recipe_execution.go:93` with sole test-file writer `recipe_execution_status_twin_e2e_test.go`, and grepped `joined-rerun.log` VERDICT lines + attempt history; HEAD confirmed `35bdc932` ("Correct the stale requestError citation beside the preview recovery stub"). Deep runs not re-executed by the record-writer; logs are the preserved record.

- **REPAIR-18 CLOSED** (`ses_e666787a0ffe75Td6nHQXcm8dN`). **U1** landed: dataframe principal-paths boundary normalization at `internal/api/graphql/graph/dataframe/service.go:85` via `authscope.NormalizeAuthResourcePaths` (`fence_resource_access.go:405`), the same helper repair-7 applied at query/input_resolution.go. RED `/tmp/opencode/fb18-u1-red.log`: pre-fix IN-list `['a\x00b' 'p1']` → post `['p1']`; garbage-only principal fails closed (DatasetNotFound) instead of widening. **Path correction recorded (reviewer's citation was shorthand):** F-7R2's `internal/dataframe/service.go:85` == `internal/api/graphql/graph/dataframe/service.go:85` (source-quote-verified: normalization comment + helper call present at the cited lines). Durable coverage: new `internal/api/graphql/graph/dataframe/fb22_principal_auth_path_control_test.go` (`TestAuthorizeMaterializationDropsControlCharPrincipalPathBeforeClickHouseINList`, `TestAuthorizeMaterializationGarbageOnlyPrincipalPathFailsClosed`). **U2 observed + encoded, then STOPPED per ownership:** the `AllowAll` silent-blank path is reachable in bulk/load (pinned in-worktree `fb22_ingress_control_test.go` with `VERDICT=CONFIRMED-PENDING` + TODO, file-confirmed at :38/:234); the resolver-side fix is **provably insufficient** — `AllowAll` bypasses the resolver — and the repair-18 worker does not own `internal/api/bulk/load/*`, so no product edit landed there.
- **REPAIR-18b DISPATCHED** (`ses_e665f927bffefBmaAkse7wLgr6`), bulk/load-owned: ingress reject guard in `internal/api/bulk/load/operations.go` (authResourcePath non-empty but normalizing to `""` → `CodeInvalidRequest` **before** `AuthorizeWrite` — guard present in-tree at :53) + upgrade the pinned `AllowAll` case RED-first + overlay kill. Evidence trail archived while the fixer runs: `/tmp/opencode/fb18b-red.log` / `fb18b-green.log` / `fb18b-kill.log` (present; content not re-run by the record-writer).
- **REPAIR-16 CLOSED-PENDING-REVIEW** (`ses_e6669312fffedE7Th3C6xqVK7V`). U1 default-arm fail-safe wrap (RED `/tmp/opencode/fb16-u1-red.log`; sentinels byte-identical) + U2 case T-C = Covers-sole-disclosure-gate with its preconditions proven; the implementer's own MutantC overlay went RED (arm(b) serves 200-with-foreign-metadata) — the twin case is proven non-vacuous; artifacts under `/tmp/opencode/fb16/`. **NEW PRODUCTION SURFACE (disclosed):** `recipeExecutionReadScope` package-level seam var in `internal/server/openapi_recipe_execution.go:93` — default = the authentic resolver call; test-only writes claimed (sole writer file grep-confirmed: `recipe_execution_status_twin_e2e_test.go`). A mutable package-level seam in production code is precisely attack surface → adversarial review dispatched specifically against seam mutability + T-C reproducibility + wrap misuse: `ses_e665ed364ffeLjU0NDhHFvSs3o`. Full prior-wave diff context noted: the 35bdc932 base already carries the F-B32/Round-34 layers, so the reviewer must scope the diff accordingly.
- **REPAIR-17 CLOSED — POST-REPAIR BROWSER RECERT GREEN** (`ses_e6667cd4cffeaN9Mu4CfPYWfNs`). **M5 journey-run7 attempt4 EXIT=0 25/25** (restoreAck 200/v12/`DRAFT_RESTORED` agreeing with persisted, restored head v12 > deleted v11 (restore-follows-head), receiptReExecuted rowCount=2). Attempts 1–3 recorded honestly: a tooling race (`core-protocol-browser.mjs:1246` ack assertion with NO deadline vs the driver :258–297 async `Network.responseReceived`→`getResponseBody` ack append) and a cleanup temp-dir recreation (chrome recreated the profile dir post-rm, cleanupError null) — both diagnosed **non-product from the archived artifact contents** (load-bearing persisted restore assertions had already passed). **M2joined witness-DE-run6 attempt1 EXIT=0 25/25** (21 names verified as a superset of DE-run5, witness D/E holds present, 27.8s). **Env bootstrap honesty:** old-registration ports dead → owned `:3182`/`:3182`; the driver is raw CDP (playwright irrelevant); `LOOM_CDA_*` bridged via `docker exec` after the runtime.env route was permission-blocked; fresh-registration seeded through the repo-owned `createFreshVerificationFixture` (project `loom_dev_verify_repair17`); probe residue noted for purge. **Evidence-taxonomy effect:** Builder core-protocol browser evidence is now post-repair fresh (C08/C09 + all journey items); the F-B28 legacy Explorer route remains **disclosed-adjacent, NOT covered**. **Build-stamp lineage caveat recorded:** an air-hot rebuild was in flight during the run — `expected_source==current` held but a byte pin was impossible ⇒ behavioral certification only. CHECKPOINT.md correctly untouched by the worker (git-status verified); `/tmp/opencode/joined-rerun.log` = the full record.

**Remaining outstanding:** REPAIR-15 fixer in flight; CLI adversarial review in flight; repairs **13** + **16** re-reviews to schedule on settled snapshots; commit wave stays gated — F-B20+F-B22 Tier-1 NO-BYPASS'd, but the trio still needs the repair-8 re-accept (REPAIR-15 probe-rerun acceptance) plus these reviews.

## Round 51 — two adversarial verdicts land: REPAIR-16 NO-BYPASS (seam ACCEPTED, T-C reviewer-REPRODUCED); REPAIR-18/18b NO-BYPASS (ingress soundness)

Attribution: verdicts and reviewer findings are attributed to the named reviewer sessions (same-model, disclosed); the record-writer did not rerun the deep audits. Where the record-writer quote-read a saved log, ran `sha256sum`/`cmp`/`grep`, or read source lines at the cited positions this session, the row says **record-writer SELF-RUN** / **record-writer quote-read** / **record-writer cmp-verified**. Both reviewers wrote nothing under the repo; every scratch path is `/tmp/opencode/...`.

- **REPAIR-16 `ses_e6669312fffedE7Th3C6xqVK7V` → VERIFIED-REVIEWED-NO-BYPASS.** Adversarial reviewer `ses_e665ed364ffeLjU0NDhHFvSs3o` (same-model, disclosed) reviewed the final snapshot (base 35bdc932 already carries the F-B32/Round-34 layers). **Seam ACCEPTED on 4 run-proofs** (`/tmp/opencode/fb16-review/probe-run.log`): (i) `TestReview16SeamDefaultIsByteIdenticalForward` PASS — the default `recipeExecutionReadScope` is byte-identical to the authentic resolver call under recorded catalog options; (ii) **writes are test-only-deferred repo-wide** — record-writer SELF-RUN `grep -rn "recipeExecutionReadScope" --include=*.go internal/ | grep -v _test.go` returned ONLY the `var recipeExecutionReadScope = func(...)` declaration at `internal/server/openapi_recipe_execution.go:93` and the single production consumer at `:36` (file-local sole consumer); no production assignment anywhere in the repo; (iii) no `t.Parallel` exposure introduced in `internal/server`; (iv) sole test writer file is `recipe_execution_status_twin_e2e_test.go` (grep-confirmed). **U1 wrap probes PASS (reviewer probes, PASS in probe-run.log):** `TestReview16SentinelOutageNotDoubleWrapped` — no double-wrap (`DeepEqual` on the sentinels preserved byte-identical); `TestReview16ArbitraryFaultWireRetryableInDecodedJSON` — retryable=true verified on the DECODED body, not on the wire substring; 401/404 arms diff-untouched by the fix. **nil-ctx panic is PRE-EXISTING shared-mapper behavior:** `probe-run.log` line "REVIEW16 nilctx | shared-mapper-panics=true twin-panics=true (parity holds; 401 arm ctx-free)" — the twin panic parity matches the shared mapper (`internal/dataframe/published/read.go:12` region); recorded here as a LATENT PRE-EXISTING NOTE, NOT a repair-16 defect. **T-C reviewer-REPRODUCED:** the reviewer independently reconstructed the MutantC overlay (`/tmp/opencode/fb16-review/mutantC-my-run.log`: `TestFB20RESTTwinCoversSubsetGateIsSoleDisclosureGate` **FAIL** with the two `FB20-TWIN HTTP status=200` arms serving foreign `projectId="Q"` + digests + rowCount, then "refused read must be indistinguishable from a missing execution... got status 200... foreign metadata"). The record-writer SELF-RUN `cmp -s /tmp/opencode/fb16-review/scope_resolver.mutantC.go /tmp/opencode/fb16/scope_resolver.mutantC.go` = **IDENTICAL** (both `a20343a4...`), and independently confirmed base `internal/authscope/scope_resolver.go` sha256 = `a618d82df86e75ff...`, matching `/tmp/opencode/fb16/scope_resolver.sha256.before`/`.after` — the reviewer and implementer mutated byte-identical sources. **Full server suite 260/0** (`/tmp/opencode/fb16-review/full-server-suite.log`): record-writer SELF-RUN tallied 260 col-0 `--- PASS` lines, 0 `--- FAIL` lines, `ok github.com/calypr/loom/internal/server 6.896s`; the two `FAIL` substrings in that log are `PREVIEW_FAILED` text inside recorded C19 core-authoring finding lines, not test failures. **Optional residual hardening (not a finding, recorded unassigned):** a lint forbidding package-level-var writes outside `_test.go` would make the seam-misuse class mechanically impossible.

- **REPAIR-18 (`ses_e666787a0ffe75Td6nHQXcm8dN`) + REPAIR-18b (`ses_e665f927bffefBmaAkse7wLgr6`) → VERIFIED-REVIEWED-NO-BYPASS.** Adversarial reviewer `ses_e665ca205ffejBorU2uiZInJ6J` (same-model, disclosed) issued one joint verdict on the boundary fix + the ingress guard (reviewer scratch `/tmp/opencode/re18-scratch/`, plus b18b/fb18b logs in `/tmp/opencode`). **Oracle-asymmetry F-1 assessed INFORMATIONAL (not a finding):** the observed 400-before-403 is this endpoint's **pre-existing validation convention** — `internal/api/bulk/load/operations.go:43/:46/:49-51` reject malformed form / boundary / empty project|generation|file with `CodeInvalidRequest` before anything auth-shaped; the request is **unreachable pre-auth** anyway (`internal/api/http/middleware.go:127-130` wraps `Authenticate` errors as `CodeUnauthenticated` before the handler runs); the F-4-arm discriminator only echoes the caller's OWN bytes back; and the new guard **strictly improves posture** vs the RED `AllowAll`-ingests-blank shape (RED `/tmp/opencode/fb18b-red-ingress.log` — the pre-guard run serves the request with a blanked path, post-guard run refuses with `INVALID_REQUEST` before ingest). No bypass or oracle exposure recorded. **F-2 = no ingress bypass:** the only production `GenerationLoadRequest` **construction** is `internal/api/bulk/load/operations.go:79`, and it sits **post-guard** — record-writer SELF-RUN: at `:53` `if authResourcePath != "" && authscope.NormalizeAuthResourcePath(authResourcePath) == ""` returns `CodeInvalidRequest` **before** `h.authz.AuthorizeWrite(...)` at `:63`; the other `GenerationLoadRequest` hits (`:14` struct, `:57` func type, `:63`/`:134` `req GenerationLoadRequest` parameters) are definitions and consumers, not constructions. Single route chain: `generated/openapi/openapi_server.gen.go:14595` → `internal/server/openapi_data_routes.go:83` (reviewer-cited). **F-3 = input-class coverage:** `/tmp/opencode/fb18b-classes.log` `TestScratchInputClasses` PASS covers every class — `" \x00 "` (ws-flanked NUL) and `"a\x00b"` and all-control (`\x01\x02\x7f`) all refused `INVALID_REQUEST` in **both** `ALLOW` (`AllowAll`) and `SCOPED` postures; the whitespace class (`" \t\n\v\f\r "`) exercises the ` ' ' ` collapse semantics end-to-end (`ALLOW` accepts + stores "", `SCOPED` refuses `FORBIDDEN`); `"legit-ws '  p1  '"` and lone-slash and trail-NUL arms close the table. **F-4 = the dataframe branch list reaches ONLY parameterized `IN` binds** (`rows.go:69`, `export.go:52`, `aggregates.go:350` reviewer-cited; record-writer confirms the parameterized-binding discipline — caller path bytes never land in a query-fragment position); `cursor` and `aggregate-plan` keys EXCLUDE `AuthResourcePaths`; the "empty==unrestricted" trap reads the STORED exec list, not this request's caller-side list; and the garbage-only principal fails CLOSED at `internal/api/graphql/graph/dataframe/service.go:92-94` (record-writer SELF-RUN quote: `normalizedPaths := authscope.NormalizeAuthResourcePaths(principal.AuthResourcePaths); if len(normalizedPaths) == 0 { return projectAccess{}, ...CodeDatasetNotFound }` — never widened). **F-5 = tests non-vacuous in BOTH postures:** pre-fix RED (`fb18b-red-ingress.log`) FAILs the scoped control-byte arm (`VERDICT=UNEXPECTED`) and the AllowAll control-byte arm (`VERDICT=REGRESSION`, `got <nil>`); GREEN (`b18b-ingress-tests.log` + `b18b-df-tests.log`) flips both to `VERDICT=REPAIRED`; the genuine-empty `AllowAll` posture is deliberately `VERDICT=PINED` (single-operator posture) so the guard's non-vacuity is proven by the control-byte arm, not by that one. **Overlay kill:** `/tmp/opencode/fb18b-kill.log` shows reverting the guard makes BOTH control-byte tests FAIL again (`scoped` UNEXPECTED + `AllowAll` REGRESSION), which is the required kill signature. **Non-blocking observations (recorded, not committed to a product change by the record-writer):** (a) deliberate ordering asymmetry — bulk/load validates-BEFORE-authenticates because `auth_resource_path` is attacker-controlled, whereas dataframe authenticates-BEFORE-lists because the list is fence-sourced; reviewer nit suggests a one-line comment at each site to make the asymmetry explicit; product-code edit belongs to the repair owner, not the record-writer (sole writer of these three verification files); (b) cosmetic note: the helper-vs-helper assertion in `fb22_ingress_control_test.go:120` (`VERDICT=REPAIRED` line) is stylistic only.

**Commit-gate line (Round 51).** REVIEWER-CLEARED COMMIT-READY UNITS now: **repair-16** (3 server files) + **repair-18/18b** (4 files: dataframe `service.go` + `fb22_principal_auth_path_control_test.go`, bulk/load `operations.go` + `fb22_ingress_control_test.go`) join the reviewer-cleared set. The earlier trio (recipe-execution — resolver/server openapi + twin controls) is reviewer-cleared only on its members already NO-BYPASS'd (repair-6, repair-12, repair-14) and the whole unit remains **PENDING** until (i) the **repair-8 re-accept** lands via REPAIR-15's probe-rerun acceptance, (ii) **repair-13 CLI review** returns, and (iii) **F-B20 / F-B22 Tier-1 durable controls** land from REPAIR-15 (the covers-subset controls + repair-8's F-1 single fix are riding REPAIR-15 per one-writer). Commit-authorization still per operator/AGENTS.md.

**Reviewer-rotation disclosure.** The reviewer session handle used for this round's adversarial verdicts is `ses_e665d6b76ffelMII8fAjU0gm2N` (the prior-review handle rotated out at round-boundary); the adversarial context was fully re-established from the round-49/50 briefs by the record-writer before dispatch so continuity of the review questions is preserved (same finding taxonomy, same seam/target surfaces, same RED-oracle citations). Both reviewers touched only `/tmp/opencode/{fb16-review, re18-scratch}` and never the repo; HEAD remains `35bdc932` (record-writer SELF-RUN `git rev-parse --short HEAD` + `git log --oneline -1`).

## Round 52 — REPAIR-15 ACCEPTED-REVIEWED (probe-rerun denial ratified); repair-8 family releasable; REPAIR-19 CLOSED (mutant C durable-killed); commit wave unblocked pending CLI review verdict

Attribution: the acceptance verdict is attributed to the accept-reviewer session `ses_e65ff7648ffeK9RgdAlnEA0ax0` (same-model, disclosed); the REPAIR-19 closure is attributed to `ses_e65fc52bfffethBcvqtELZb0BX`. The record-writer this session SELF-RAN the decisive checks named **record-writer SELF-RUN** below and read every cited log/source line in place; nothing in this section is propagated on claim alone where a local check was possible.

- **REPAIR-15 (`ses_e666b08e0ffeYVsC86jYbVMQ2Q`) → ACCEPTED-REVIEWED (accept reviewer `ses_e65ff7648ffeK9RgdAlnEA0ax0` ACCEPT).** Product fix: `scopeCacheKey` gains an **`allGenerations` bool discriminator** — record-writer SELF-RUN `rg`: field declared at `internal/authscope/scope_resolver.go:123`; the ONLY writer of an `allGenerations=true` key is `listExistingPathsAllGenerations` (`:410`, fail-closed on list error, structural-discriminator comment at `:395-404`); generation-scoped lookups build `allGenerations:false` keys (`:367`) and therefore **cannot address** the project-write-binding entry — the sentinel-collision class is eliminated structurally, not by string-filtering a reserved literal. `InvalidateGeneration` deletes `allGenerations:false` entries only (`:455-466`) → the mode=true project binding is **spared** (adv test PASS). **Poison probe re-run DENIED:** `/tmp/opencode/fb27rev-overlay/run.log` `TestFB27RevSentinelCollisionPoisonsWriteBinding` **FAIL** at `zz_fb27rev_probe_reviewer_copy_test.go:238` — `write decision must have been served from the poisoned cache entry: calls=3` (line number record-writer SELF-RUN grep). The poisoned entry no longer serves `AuthorizeWrite` — the decision re-lists — so the Round-49 `m2.log` "F-B27 BYPASS CONFIRMED" branch is unreachable; the FAIL-at-:238 is the post-repair **inverse** of the RED oracle. The reviewer's :238 vulnerable-serving assertion **and both supersessions** (durable promotion `TestFB27SentinelGenerationPoisonCannotReachAuthorizeWrite` + the overlay `FB27Adv` sentinel-denies control) were **independently RATIFIED** by the accept reviewer.
- **Reviewer-authored adversarial controls PASS.** `/tmp/opencode/fb27rev-overlay/zz_fb27adv_test.go` (header: "Independent adversarial controls authored by the acceptance reviewer"): `TestFB27AdvInvalidateGenerationSparesModeTrueBinding`, `TestFB27AdvSentinelPoisonDeniesWithFreshBindingList`, `TestFB27AdvWrapChainIsSentinel`, `TestFB27AdvConcurrentSameProjectListers`. **Record-writer SELF-RUN:** `go test -overlay /tmp/opencode/fb27rev-overlay/overlay2.json -race -count=1 -v -run TestFB27Adv ./internal/authscope` → **4/4 PASS, ok 1.127s** (`/tmp/opencode/r52-adv-selfrun.log`), incl. the named `...WithFreshBindingList` PASS and the `64x4`-class concurrency control (`TestFB27AdvConcurrentSameProjectListers PASS (0.02s)`).
- **Concurrent race-clean after test-double diagnosis (record-writer SELF-RUN).** The transient `-race` flake root-caused to the REPAIR-15 **shared-mutable `fb27ctlLister` test helper** (`all_generations_cache_key_test.go:26-36`: `calls` counter + `opts` slice accumulated across subtests through one instance), **not product code**; the reviewer's test double distinguished helper-state from product-state before any conclusion was drawn. Full-package confirmation: `go test -count=1 ./internal/authscope` ok 0.074s (`r52-authscope-full.log`) and `go test -overlay overlay2.json -race -count=1 ./internal/authscope` **ok 1.130s** (`r52-authscope-race-selfrun.log`) with zero race reports.
- **Real-tree placement verified (path-drift lesson recorded).** The fixer once used a wrong `go -C` path variant; the parent independently verified the **REAL TREE** — `git status --porcelain internal/authscope/` + on-disk ls confirm the **3 new files present** (`all_generations_cache_key_test.go` md5 `e57dfdd042bf983e85d3e37bba6e67f8`, `auth_resource_path_options_conflict_test.go` `69bbbe193a470d1edd091a090e089c49`, `scope_cache_key_namespace_test.go` `dc7accfb0722ba99c628ba22976d8017`) **+ the `allGenerations` bool at `scope_resolver.go:123`**, on the **single volume `arc-loom-qwen-current-20261031`** (record-writer SELF-RUN `ls /var/lib/docker/volumes/` lists exactly one loom volume). **LESSON (recorded): dispatch briefs must quote the exact mount path**, not a paraphrase.
- **repair-8 family → VERIFIED-REVIEWED-RELEASABLE — explicit status line for the chain F-B18 → F-B27 → REPAIR-15.** repair_unit[8] now reads `VERIFIED-REVIEWED-RELEASABLE (F-B27 chain: r40 landed -> r49 review FAIL-REQUEST-CHANGES -> FIXING -> REPAIR-15 ACCEPTED-REVIEWED ses_e65ff7648ffeK9RgdAlnEA0ax0 round 52)`. The three-file durable set + `scope_resolver.go` bool is the releasable F-B27 unit.
- **REPAIR-19 (`ses_e65fc52bfffethBcvqtELZb0BX`) DISPATCHED + CLOSED — F-B20 reviewer's F-2 durable requirement SATISFIED.** New `internal/authscope/covers_execution_paths_control_test.go` (md5 `3f134a94fc06b201d1a469acdef53523`, 147 lines, record-writer SELF-RUN read). **Mutant C** (membership loop neutered, guards kept — the exact mutation that survived the shipped suite at Round 49 F-2) **is now killed by 13 durable scenario arms** — verbatim `/tmp/opencode/repair19/kill_run.log`: 19 `--- FAIL` sub-test lines across the 3 controls including the named witnesses `equal-count-disjoint-denies`, `mixed-generations-denied`, `sentinel-scope-denies-ordinary-declared` + `sentinel-scope-does-not-cover`, `restricted-empty-scope-denies-declaring`/`restricted-scope-empty-denies-declaring`, `prefix-not-conflated`, and the generation-bound `foreign-generation-path-denied` + `mixed-generations-denied` (the 13 count = unique discriminator scenarios; `restricted-nil-scope` and the two empty-scope arms overlap across controls, and 3 serving-side controls pass under mutant C by design, so 19 FAIL lines = 13 unique kills). Plus the **25-case truth table** (`TestCoversExecutionPathsSubsetTruthTableControl`, cases :66-90) and the **per-subtest resolver-driven generation-bound control** that builds its own resolver+lister per subtest (:118-127) — race-clean by construction. **F-B20 trio pre-commit coverage requirement: MET.**
- **OPERATOR-NOTE (re-review non-blocking) — F-2 validator rationale corrected.** The earlier phrasing that `DiscoverExistingAuthResourcePaths` had "zero callers" is **WRONG**: it IS production-wired — `internal/server/server.go:439` passes `catalogStore.DiscoverExistingAuthResourcePaths` into `wireAuth`, consumed at `internal/server/auth_wiring.go:40` (calypr mode; both citations record-writer SELF-RUN source-read). The `AllGenerations`+`DatasetGeneration` conflict guard (`internal/catalog/arango/resources.go:180-184`, `ErrAllGenerationsWithGeneration`) **remains correct on its own terms**: the only TWO production `AuthResourcePathOptions` producers are `authscope/scope_resolver.go:378` (generation-scoped, `AllGenerations` false) and `:421` (all-generations, empty generation) and neither emits the conflicting shape (record-writer SELF-RUN construction-site census over non-test code).
- **Test-hygiene items (non-blocking, recorded).** (a) REPAIR-15's `fb27ctlLister` shared-mutable helper caused the transient `-race`; product race-clean (see above); polish the helper to per-subtest state in the next idle wave — repair owner, not the record-writer. (b) The wrong `go -C` variant incident is recorded above with its path-drift lesson for brief authoring.
- **Outstanding + commit-wave sequence.** **CLI adversarial review `ses_e668262b6ffeYAWS09jr7dvgXq` report still OUTSTANDING** at round close (record-writer SELF-RUN: no landing artifact in-tree or `/tmp/opencode`; repair-13 stays `verified, review-pending`). **Record-not-claim rule:** if the report is absent at the final gate, dispatch a FRESH CLI adversarial review before the commit wave depends on it. **Then the COMMIT WAVE unblocks** in sequence, each unit landing with tree-level build proofs per the commit worker's standard (build/vet/gofmt against the real tree, not per-file claims): (1) **prereq** — `scope_resolver.go` + catalog `types.go`/`arango/resources.go`; (2) **trio** (7 files — recipe-execution resolver/server + twin controls incl. the now-MET F-B20 durable controls); (3) **CLI wave** (5 files — `cmd/arango-fhir-proto/main.go`, `main_test.go`, `internal/ingest/resource_load.go`, `resource_load_test.go` + the CLI test file present set re-verified via `git status --porcelain cmd/arango-fhir-proto/ internal/ingest/`); (4) **ledger/models/docs wave**. Commit authorization remains per operator/AGENTS.md; the record-writer stages nothing itself.

Record integrity: HEAD `35bdc932` (unchanged this round; the wave has not been dispatched yet); reviewer and fixer artifacts live under `/tmp/opencode/{fb27rev-overlay,repair19}` — the overlay reviewer test was run via `-overlay`, never written into the repo (repo hygiene preserved: `git status` shows no zz_* files). `reviewed-sources.json` parses and re-serializes byte-identically (record-writer SELF-RUN: `json.dumps(indent=1, ensure_ascii=False)` roundtrip == on-disk bytes).

## Round 53 — COMMIT WAVE LANDED (A authz / B recipe-execution twin / C boundary guards); backlog triage of the uncommitted M/?? cluster; aggregate-flake disclosure + phantom fuzz-corpus retraction

Attribution: the three commits and their tree proofs are attributed to the commit-worker sessions named in the operator round-53 brief (waveA-check / waveB-check / waveC-check disposable-worktree staged-patch proofs); the record-writer this session SELF-RAN the on-tree reconciliation named **record-writer SELF-RUN** below (`git show --stat` for each hash, full-SHA reads, blob-content fingerprint checks via `git show HEAD:<path>`, `sha256sum` vs the ledger artifact shas, `git status --porcelain` reconciliation, phantom-file `find`+`git log --all`+`rg`, wave patch grep, log tails in `/tmp/opencode`). Nothing was committed, staged, or edited outside the three verification-record files by this session.

### Commit wave (HEAD `35bdc932` → `0e3b4c5b` → `eb9c9df0` → `68189b5f`)

- **Wave A — `0e3b4c5b91c62f44a74d08755466dfbdd314df0b`** "fix(authz): F-B22 control-byte ingress normalization + F-B27/REPAIR-15 project-binding namespace integrity + F-B18 honest load faults" — **14 files, +1393/−9** (record-writer SELF-RUN `git show --stat`): `cross_project_dataset_authorization_e2e_test.go` (+276), `query/input_resolution.go` (+28), `read_scope_ingress_control_chars_e2e_test.go` (+92), `authscope/all_generations_cache_key_test.go` (+100), `authscope/auth_path_control_ingress_test.go` (+114), `auth_resource_path_options_conflict_test.go` (+158), `authscope/covers_execution_paths_control_test.go` (+147, REPAIR-19 mutant-C kill suite), `fence_resource_access.go` (+34), `scope_cache_key_namespace_test.go` (+199), `scope_resolver.go` (+109, `allGenerations` bool discriminator), `catalog/arango/resources.go` (+44), `catalog/types.go` (+6), `ingest/resource_load.go` (+42, F-B18 `LoadIncompleteError`), `ingest/resource_load_test.go` (+53). Receipts: repair-7 `ses_e66724ac8ffe3EQq5KhztfChEM` (CORE-SOUND-REMEDIATION-OUT — the F-7R1/F-7R3 remediation rode this wave), REPAIR-15 **ACCEPT** `ses_e65ff7648ffeK9RgdAlnEA0ax0`, repair-6 `ses_e66757becffe8uCNimGXbEeIfc` trio-adjacent (its F-2 durable requirement was MET by REPAIR-19 shipped here). Tree proof **waveA-check**: `go build`/`go vet` clean, 5 package suites `ok` + server `ok` (`/tmp/opencode/waveA-{build,vet,test}.log`; test log tails record-writer SELF-RUN: `PKGTEST_EXIT=0`, `SERVERTEST_EXIT=0`, catalog 104.756s incl. real-store suites).
- **Wave B — `eb9c9df054405e7b275399e233d8d39ebb5f7a87`** "fix(api): recipe-execution twin authz (F-B20), retryable-outage twin classification (F-R1), denial concealment (F-B32), failure-path coverage (REPAIR-16)" — **6 files, +906/−456**: resolver `recipe_execution_reader.go` (+7/−), `recipe_execution_reader_authz_e2e_test.go` (576 churn), `internal/server/openapi_recipe_execution.go` (+82; carries the `recipeExecutionReadScope` seam + default = authentic resolver call), `openapi_recipe_execution_test.go` (+151), `recipe_execution_status_twin_e2e_test.go` (490 churn), `recipe_execution_twin_authz_repair_test.go` (new 56). The **seam is disclosed in the commit body** (ACCEPT-with-seam, 4 run-proofs from the adversarial review). Receipts: repair-6 `ses_e66757becffe8uCNimGXbEeIfc` VERIFIED-REVIEWED-NO-BYPASS, REPAIR-16 `ses_e665ed364ffeLjU0NDhHFvSs3o` VERIFIED-REVIEWED-NO-BYPASS. Tree proof **waveB-check**: build/vet clean; `resolver` + `server` `ok` (`waveB-{build,vet,test}.log`; resolver 0.078s, server 7.195s with the disclosed replay `−skip`).
- **Wave C — `68189b5f9b3fbc0e075a5cecc142935686596007`** "fix(api): normalize principal auth paths at dataframe boundary; reject control-byte auth_resource_path at generation ingress" — **4 files, +324/−1**: `bulk/load/fb22_ingress_control_test.go` (new 236, F-B22-ingress), `bulk/load/operations.go` (+3, ingress guard), `dataframe/fb22_principal_auth_path_control_test.go` (new 74, F-7R2), `dataframe/service.go` (+12/−1, principal-paths normalization). Receipts: `ses_e665ca205ffe` adversarial NO-BYPASS (same-model, disclosed) + RED evidence `fb18-u1-red.log`/`fb18b-red.log` (present on disk, record-writer SELF-RUN). Tree proof **waveC-check: GATE_GREEN** (staged-patch vs clean HEAD, build/vet both packages, dataframe + bulk/load + server `−skip TestCoreAuthoringProtocolModelReplay` all green) **including the aggregate-flake investigation below**; `waveC.patch` present on disk (record-writer SELF-RUN ls).
- All three waves used **disposable-worktree staged-as-only patches** against the settled prior-HEAD (per the commit-worker standard; wave A staged vs `35bdc932`, wave B vs `0e3b4c5b`, wave C vs `eb9c9df0`). Commit bodies carry the disclosure that verification/publication-bundle artifacts deliberately stayed out (operator-owned wave; air-hot CDA/Builder stack).

### Commit-adjacent disclosures

- **FUZZ-CORPUS PHANTOM RETRACTED.** `internal/authscope/resource_access_fuzz_seed_corpus_test.go` **never existed**: not on disk (record-writer SELF-RUN `ls` — no such file), zero hits across **all** git refs (`git log --all -- <path>` empty), and the `authscope` package contains **zero Fuzz symbols** (record-writer SELF-RUN `rg -c Fuzz` — no match). The digest records that claimed it are corrected; wave A shipped **14 real files**. The deviation was disclosed in the wave-A commit body (the file was excluded rather than fabricated; operator notified at commit time).
- **AGGREGATE FLAKE (TEST TIMING FLAKE — pre-existing test-design weakness).** `TestAggregateSchedulerDispatchesExpectedConcurrentCallsOnce` batch-size failure occurred **ONCE** under cold-build CPU contention during wave C. Root cause (record-writer SELF-RUN source read): the test's own `aggregateFallbackDelay = 10ms` (`internal/api/graphql/graph/dataframe/aggregates.go:52`) can fire between the 158 enqueue goroutines, so the scheduler legitimately splits the batch while the test asserts exactly one batch of 158 (`aggregates_test.go:36`). It occurred in **72+ subsequent runs (patched + baseline): zero failures**; `aggregates.go` is **untouched by the patch** (record-writer SELF-RUN: zero `aggregates.go` hits in `/tmp/opencode/waveC.patch`) and untouched by every wave. Classified **TEST TIMING FLAKE, not product code**. Same class as the long-known **30s replay pattern** (the `context.WithTimeout(..., 30*time.Second)` gates at `core_authoring_protocol_formal_replay_test.go:304/:368/:377/:382` — the `-skip TestCoreAuthoringProtocolModelReplay` convention already disclosed in every server-suite proof since Round 34). **Both are recorded under one sideFinding `flaky-timing-tests`** in `reviewed-sources.json` (with the Round-52 `fb27ctlLister` -race helper flake named as the same class). Fix = event-driven barrier / load-scaled timeouts; repair-owner test-hygiene ticket, not commit-gating.
- **Porcelain reconciliation.** The final `git status --porcelain` remainder beyond the three waves is **exactly** the backlog triage cluster below + the gated CLI wave-D files + the operator-owned model/doc artifacts + tooling dirs — **no unexplained remainder** (full list in `d['backlogTriage']['porcelainReconciliation']`).

### Backlog triage — the uncommitted M/?? cluster (source archaeology; NOTHING committed)

Each file was resolved to its claim record (`status_updates/repair-unit-*` + `confirmed[*]` + finding status), verified status, adversarial-review status, and the confirm-time artifact sha vs the **current on-disk byte** (record-writer SELF-RUN `sha256sum` vs the `confirmed[*].artifacts[*].sha256` values).

- **F-B21 / Repair-5 cluster** — `bundle.go` (M), `bundle_test.go` (M, new +61), `pointer_name_encoding_test.go` (??, new 101 lines), `clickhouse/pointer_name_nul_ambiguity_e2e_test.go` (M), `recipe/publication_identity_nul_http_test.go` (M).
  - Claim: `repair-unit-5` file list + `confirmed[12]` (F-B21 HIGH intra-project, parent `c80b84cb` + worker×2 + fb21http×2) + status `REPAIRED Round 33 … REAL-SERVICES VERIFIED round 36 JAR3`.
  - Review status: **VERIFIED, NOT adversarially reviewed** — no repair-5 adversarial receipt exists anywhere (the `repair_units` ledger starts at repair-6; the `pstack:interrogate` regime postdates Round 33).
  - Commit state (record-writer SELF-RUN blob checks): **not a byte of F-B21 is at HEAD** — `git show HEAD:…/bundle.go` still has the bare `\x00` join (zero length-prefix fingerprints), HEAD `bundle_test.go` lacks the injectivity unit, `pointer_name_encoding_test.go` absent at HEAD. The on-tree product code at HEAD still carries the F-B21 NUL-fold repoint hazard; committing this unit is the de-risking action.
  - Drift: both confirmed-record shas are **stale vs on-disk** (pnn `499574cb…`→`7f32f0fe…`; http `19d396a2…`→`6341843c…`), and the drift **is the recorded conversion**: the red FAILED-ON-PURPOSE arms became natural-PASS + archived-red REGRESSION witnesses, with the `pnnRequiredQuotations` re-pin now requiring the length-prefix anchors (`strconv.Itoa`/`WriteByte(':')`). Consistent with the ledger's `conversions` description; re-pin shas at commit time.
  - Ledger gap flagged: `bundle_test.go` `TestBundleIdentityPointerNameIsInjective` is **not listed** in `repair-unit-5`'s file list — post-claim unit addition (substance matches the recorded intent "units pin the historical fold + require separation"); add the name to the claim before commit.
  - **Classification → REVIEW-QUEUE** (verified-not-reviewed). One adversarial review over the 5-file unit (injectivity, historical-fold pin, blank-component semantics, quotation re-pins, non-vacuity); cleared ⇒ COMMIT-READY-BACKLOG → land as **wave D-2** alongside/after the CLI files. The product file cannot ship without its two witness units (`historicalBareJoin` pin + regression witness are the non-vacuity).
- **F-B19 / Repair-4** — `clickhouse/orphaned_published_growth_e2e_test.go` (M). Claim: `repair-unit-4` + `confirmed[11]` (dual parent runs `e00a7032`/`e3712285`) + status `REPAIRED Round 32 … REAL-SERVICES VERIFIED round 36 JAR3`. **Product janitor arm IS at HEAD** (record-writer SELF-RUN: reader-gated markers present in HEAD `bundle_store.go`). Verified-not-reviewed (Round 32 predates the regime). Sha stale (`da5d26c5…`→`e0c2ce7b…`); drift = the recorded r32 conversion (red → PREVENTED + reader-harness extraction `orphReleaseReader`). **→ REVIEW-QUEUE**; re-run the converted e2e + sibling vs **current** HEAD before dispatch (evidence is dc9016bc-era; waves A–C moved the tree under it).
- **F-B15 / Repair-3** — `internal/dataset/arango/revision_inflation_e2e_test.go` (M). Claim: `repair-unit-3` + `confirmed[8]` (parents ×3, uuid `25b5af79`) + status `REPAIRED Round 31 … REAL-SERVICES VERIFIED round 36 JAR1 … Artifact hardened (round 37)`. **Product fix IS at HEAD** (record-writer SELF-RUN: `release_store.go` `alreadyActive` zero-write CAS + `releaseID` fingerprint shipped). Verified-not-reviewed (Round 31); **integrity-event-2 history on this unit** (fabricated worker report; evidence basis = parent-run outputs only) ⇒ parent-rerun discipline mandatory. Sha stale (`b01ab91e…`→`23576ecf…`); drift = the recorded round-37 hardening verbatim (PREVENTED arm now pins revision, release ID, doc count, both `_rev`s + `contentIdentical` gate). **→ REVIEW-QUEUE** (lowest risk — pure witness hardening); bundle with the publication-backlog reviewer; re-run PREVENTED vs real Arango at current HEAD.
- **Governance docs** — `AGENTS.md` (M) + `docs/verification/CONTINUOUS_FIND_FIX_VERIFY_GOAL.md` (M). **No ledger record claims them** (record-writer SELF-RUN grep: zero claims). The diffs are the codification of the **Adversarial review** + **Local commits** governance sections (AGENTS.md +16) and the CONT doc's matching rewrites (local commits for verified+reviewed Todos authorized; adversarial review per AGENTS) — exactly the authority the three commit waves wrote "Commit authorization remains per operator/AGENTS.md" against, and de-facto active (loaded into agent instruction paths from the working tree). **→ FLAG FOR PARENT ADJUDICATION / owner: operator-parent only.** They belong to the planned ledger/models/docs wave (round-52 sequence item 4): land as an operator/parent commit with or before wave D so the on-tree rules match the rules cited in the commit messages. Not a repair unit — no verification/review pipeline applies; record-writer does not touch them (sole-writer boundary).
- **CLI wave-D files** (`cmd/arango-fhir-proto/main.go` + `main_test.go` M, plus `ingest/resource_load.go`/`resource_load_test.go` — the latter two actually shipped inside wave A's F-B18 hunks) remain gated on **`ses_e668262b6ffeYAWS09jr7dvgXq`** (report re-requested this round, ALIVE per operator; still **no landing artifact** in-tree or `/tmp/opencode`, record-writer SELF-RUN). repair-13 stays `verified, review-pending`; record-not-claim: fresh CLI adversarial review before wave D depends on it.

**Commit-wave state after Round 53.** Landed: prereq+authz (A) → recipe-execution twin (B) → boundary guards (C), each with its tree proof. Outstanding: **wave D (CLI quintet, gated on the CLI review)**, **wave D-2 (publication-bundle backlog: F-B21 unit + F-B19 + F-B15 witnesses — REVIEW-QUEUE above)**, **wave E (ledger/models/docs + governance docs — parent-owned)**. `verification/publication-bundle` stays operator-owned per the commit-body disclosures. Reviewer sessions cited are same-model where not otherwise disclosed.

Record integrity: HEAD now `68189b5f` (record-writer SELF-RUN `git log --oneline -5` at the top of this session); every cited blob check, sha comparison, and log tail was read this session. `reviewed-sources.json` parses and re-serializes **byte-identically** (record-writer SELF-RUN `json.dumps(indent=1, ensure_ascii=False)+"\n"` roundtrip == on-disk bytes; round field 52 → 53). No repo file outside the three verification records was touched by the record-writer.

## Round 54 — COMMIT WAVE COMPLETED (D governance docs + E publication identity land); repair-unit commit ledger; fuzz-corpus retraction instance 2; ROW-SOURCE-01..04 operator re-engagement

Attribution: the two commits landing after the round-53 record are attributed to the operator's commit wave; every number and claim below was re-derived by the record-writer **this session** via `git log --oneline -6`, `git show --stat` per hash, `git show <hash>:<path>` blob reads, `git ls-tree`, `git diff HEAD --stat`, `git status --porcelain`, and `/tmp/opencode` greps — marked **record-writer SELF-RUN**. Operator-provided session handles follow record-not-claim: `ses_e65ea59c4ffe0pEzWbFIQ5qqcA` (publication-identity review receipt — self-verified via the 8c21eb64 commit-body text itself), `ses_e65e0cdffff` (fresh CLI review, in flight), `ses_e65e0e876ffe9SzztDKyGNJPVd` (ROW-SOURCE investigation worker). The record-writer committed/staged nothing and touched no repo file outside the three verification records.

### Actual git log (pasted verbatim, record-writer SELF-RUN `git log --oneline -6`)

```
8c21eb64 fix(publication): injective length-prefixed PointerName (F-B21), janitor/orphan + revision-inflation durable witnesses (F-B19/F-B15)
3874e49d docs(governance): codify adversarial-review and local-commit rules; align CONTINUOUS goal with AGENTS.md
68189b5f fix(api): normalize principal auth paths at dataframe boundary; reject control-byte auth_resource_path at generation ingress
eb9c9df0 fix(api): recipe-execution twin authz (F-B20), retryable-outage twin classification (F-R1), denial concealment (F-B32), failure-path coverage (REPAIR-16)
0e3b4c5b fix(authz): F-B22 control-byte ingress normalization + F-B27/REPAIR-15 project-binding namespace integrity + F-B18 honest load faults
35bdc932 Correct the stale requestError citation beside the preview recovery stub
```

HEAD = `8c21eb644cd3aa4aeab6df3d0ec8adc4084fae9a`. Waves A/B/C keep their full round-53 record (`status_updates.round53`); the two later commits are recorded in full here.

### Commits landed after the Round-53 record

- **D — governance `3874e49ddff6062f9528ef6257e033d390082007`** "docs(governance): codify adversarial-review and local-commit rules; align CONTINUOUS goal with AGENTS.md" — **2 files +19/−3** (SELF-RUN `git show --stat`): `AGENTS.md` +16 (the **Adversarial review** + **Local commits** governance sections; the same-batch **rg-preference working-style line ships inside** — SELF-RUN `git show 3874e49d:AGENTS.md`: "Prefer `rg` and `rg --files`…" at line 6) + `docs/verification/CONTINUOUS_FIND_FIX_VERIFY_GOAL.md` +6/−3. Commit body: "exactly the rules the wave A/B/C commit receipts cite and operator-approved program governance". This is the round-53 **FLAG-FOR-PARENT-ADJUDICATION** item landing — a **parent/operator decision, committed as-is**. FOLLOW-ON: further operator worktree edits remain uncommitted (SELF-RUN `git diff HEAD --stat`: `AGENTS.md` +11 "Factory work intake" section, CONT rewrites, `BUILDER_WORKLIST.tsv` +4 = the ROW-SOURCE rows) — operator-owned files, untouched by the record-writer.
- **E — publication identity `8c21eb644cd3aa4aeab6df3d0ec8adc4084fae9a`** "fix(publication): injective length-prefixed PointerName (F-B21), janitor/orphan + revision-inflation durable witnesses (F-B19/F-B15)" — **7 files +386/−72** (SELF-RUN `git show --stat`): `publication/bundle.go` +20 (length-prefix `strconv` len:content — SELF-RUN blob: `strconv.Itoa` fingerprint at :110), `bundle_test.go` +61 (injectivity unit), `clickhouse/orphaned_published_growth_e2e_test.go` (196 churn, F-B19 witness), `clickhouse/pointer_name_nul_ambiguity_e2e_test.go` +16, `publication/pointer_name_encoding_test.go` **NEW 108**, `recipe/publication_identity_nul_http_test.go` +47, `dataset/arango/revision_inflation_e2e_test.go` +10. This is the round-53 backlog-triage cluster landing as **wave D-2** under its own adversarial review: **receipt `ses_e65ea59c4ffe0pEzWbFIQ5qqcA` — per-cluster RELEASABLE**, same-model (disclosed), **including an independent decoder attack over 36873 sequences** against the length-prefixed encoding (SELF-RUN read of the commit body).
  - **F-1 atomicity gate HONORED**: `bundle.go` + `bundle_test.go` + `pointer_name_encoding_test.go` **co-committed** (SELF-RUN `git ls-tree 8c21eb64`: both witness blobs present alongside the length-prefix) — round-53's non-vacuity requirement ("the product file cannot ship without its two witness units") is satisfied; the round-53 **unlistedUnit** (`bundle_test.go` missing from `repair-unit-5`'s list) is resolved by landing with the unit; the stale confirm-shas (pnn `499574cb…`, http `19d396a2…`) re-pin at this commit (the recorded conversions landed with it).
  - **F-3 comment correction** (comment-only, zero assertion changes): the fabricated "publish path rejects blank OutputName" justification was replaced with REAL gates — the record-writer **independently SELF-RUN verified both before recording**: `git show 8c21eb64:internal/dataframe/recipe/document_validation.go:39` → blank/whitespace `TranslationVersion` rejected as required (rules out the blank-TV twin); `git show 8c21eb64:internal/server/recipe_publication.go:214-218` → `incrementalPublicationOutput` returns `""` unless **exactly one** OutputNames binding **and one** stream (blank-Output identities arise only from multi-output publication, so 4-component multi-output names never collide with 5-component single-output names).
  - Commit body carries **"Hazard is live at HEAD until this commit"** (SELF-RUN) — the F-B21 NUL-fold repoint-on-publish hazard was live on-tree through `3874e49d` and is de-risked by `8c21eb64`.

### Repair-unit commit ledger + commit gate

Every `repair_units[*]` entry now carries a `commitState` with hash (record-writer `git show --stat`/porcelain rechecks): repair-7/8/15/19 + F-B18 ingest arms → `0e3b4c5b`; repair-6/12/14/16 → `eb9c9df0`; repair-18/18b → `68189b5f`; governance → `3874e49d`; F-B21/F-B19/F-B15 witness cluster → `8c21eb64`; repair-9/10/11 were already committed at `1973b9ae`/`8ac0fd9d`/`9764eeda` (round-48 wave; SELF-RUN: objects exist, files porcelain-clean); **repair-13 PARTIAL** — the ingest-side `LoadIncompleteError` arm rode wave A at `0e3b4c5b` (SELF-RUN wave-A diff), `cmd/arango-fhir-proto/main.go` + `main_test.go` still uncommitted; repair-17 evidence-only (browser recert, no patch). **`commitGate` (new top-level key): CODE WAVES COMPLETE EXCEPT CLI WAVE D** — gated on the **FRESH CLI adversarial review `ses_e65e0cdffff` now in flight** (operator-provided; record-not-claim until a verdict lands); the prior review session `ses_e668262b6ffeYAWS09jr7dvgXq` **never delivered** across rounds 52–54 (no landing artifact tree//tmp, SELF-RUN).

### Round-53 carry-ins now in the ledger

- **FUZZ-CORPUS PHANTOM RETRACTED — instance 2 of 2** (per review `ses_e65ea59c4ffe` §4): the "REPAIR-10 25-seeds" commit-brief premise is a **conflation** and is retracted. REPAIR-10 is the F-B28 Explorer legacy-failure Details control — **zero fuzz/seed content** anywhere in its ledger record; SELF-RUN: `git ls-files` shows zero fuzz artifacts under `internal/authscope`/publication (only unrelated dataset `root_page_related_seed`/`repositoryseed` files); authscope has zero Fuzz symbols. The real "25": the REPAIR-19 **25-case covers truth table** (`covers_execution_paths_control_test.go` `TestCoversExecutionPathsSubsetTruthTableControl`). With instance 1 (round 53, `resource_access_fuzz_seed_corpus_test.go`): **no fuzz corpus ever existed for this program**; briefs must not cite either name.
- **Flaky-timing pair (record-accurate note):** both instances were ALREADY recorded in round 53 under one sideFinding `flaky-timing-tests` — (1) `TestAggregateSchedulerDispatchesExpectedConcurrentCallsOnce`: the test's own 10ms `aggregateFallbackDelay` (`aggregates.go:52`) vs the 158 enqueue goroutines + exact-158 assertion (`aggregates_test.go:36`); (2) the 30s `TestCoreAuthoringProtocolModelReplay` timeout pattern (`core_authoring_protocol_formal_replay_test.go:304/:368/:377/:382`, the disclosed `-skip` convention). SELF-RUN confirms the sideFinding is present and classifies **both as TEST-DESIGN WEAKNESS open items** (repair-owner test-hygiene ticket; non-commit-gating). Recorded as existing, not as new.
- **PROCESS LESSON (positive pattern):** the reviewer caught a **fabricated justification in comment text** (the non-existent blank-OutputName gate) with no assertion/test defect involved — reviewers verified claims in comments too. Standing rule reaffirmed: adversarial review keeps treating comments as falsifiable artifacts; briefs quoting comment prose inherit the verification duty (this record's F-3 gates were re-verified at the source before recording).

### Operator re-engagement — ROW-SOURCE-01..04 (intake rules: task ID + scope + status + next boundary)

SELF-RUN `docs/BUILDER_WORKLIST.tsv` rows 94–97 + contract `docs/ROW_OPERATION_SOURCE_INPUT_BACKLOG.md` (present, 4458 bytes). Acknowledged:

| Task | Scope (worklist) | Status / next boundary |
|---|---|---|
| **ROW-SOURCE-01** | operator-approved-feature · row-operations/unselected-dataset-inputs · P0 "select and implement first non-Group source-input slice" | ready; **investigation worker `ses_e65e0e876ffe9SzztDKyGNJPVd` dispatched** for the 01 operator matrix + first-slice selection (operator-provided handle; worklist owner column still unassigned at record time); next boundary: focused-native-journey verifier chosen after tracing the operator; contract acceptance + adversarial review + coherent local commit |
| **ROW-SOURCE-02** | P1 "extend remaining eligible non-Group operators" | **blocked** on ROW-SOURCE-01 (SELF-RUN dependency column); no action until 01 lands |
| **ROW-SOURCE-03** | P1 "align source-input operation availability and explanations" | ready; worklist: "Independent investigation now; implementation after ROW-SOURCE-01 capability contract"; independent investigation dispatch to follow |
| **ROW-SOURCE-04** | operator-approved-investigation · design-needed · "propose one related-route or post-transform coded-operation contract" | **proposal-only affirmed** (SELF-RUN: "Operator approval required before implementing extension") |

Native Todos rebuilt for **revision 9** (operator-supplied harness state).

### Program state

Auth program closure **unchanged** (egress audit clean; combine+mint trace clean; minting infeasible; residual items are hardening notes without named consumers). Browser recert **unchanged**: repair-17 CLOSED (M5 25/25 + M2joined 25/25; behavioral-only certification caveat + F-B28 legacy-route disclosure stand). Outstanding operator-questions list **unchanged** (F-B17 activation CAS; F-B7 partial-degrade decision; F-B3 pin-wedge family; F-B33-class declared-response drift incl. the now-declared-but-unreachable 403 + `context.Canceled` 499 parity; F-B28/F-B29 contract-letter caveats; projectid percent-decode hardening ticket). **Defect queue — tracked sanctioned-reds: F-B17, F-B3 incl. the H2-a stale-pin arm** (`TestStalePinBlocksEntireReconcileAgainstArango`) — plus, per the round-36 real-services matrix which this record does not silently drop, the **F-B7 amplifier red** (`TestSingleDanglingBindingDemotesWholeDataset`) remains sanctioned pending its queued operator decision. All ledger-sanctioned, not CI noise.

Porcelain (SELF-RUN): `cmd/arango-fhir-proto/{main.go,main_test.go}` M (gated wave D) · `AGENTS.md`/CONT doc/`docs/BUILDER_WORKLIST.tsv` M (post-commit operator edits) · `docs/ROW_OPERATION_SOURCE_INPUT_BACKLOG.md` ?? (operator contract) · 3× `verification/publication-bundle/*.tla` M (operator-owned models per commit-body disclosure) · the record-writer's 3 files · tooling dirs. **No unexplained remainder.**

Record integrity: this section and the ledger updates were written after SELF-RUN verification of every cited stat, blob, and line number; nothing verified-by-quote only is presented as re-run. `reviewed-sources.json` parses and re-serializes **byte-identically** (`json.dumps(indent=1, ensure_ascii=False)+"\n"` roundtrip == on-disk bytes; round field 53 → 54; `commitGate` key added after `round`; `sideFindings` +3; `backlogTriage` clusters annotated `r54`). No repo file outside the three verification records was touched by the record-writer.

## Round 56 — formal-maintenance pass VERIFIED (TLC trio nine arms self-run at current pins; trio md5s held); releaseID UNREACHABLE closure self-probed + HARDENING TICKET; REPAIR-20/20b F-R3 fixed-and-self-ran (re-review pending); ROW-SOURCE-01 chain review-pending; ledger pin-census correction

Attribution: this round the record-writer SELF-RAN the decisive verifications rather than propagating worker claims — the full TLC trio nine-arm re-run (`/sandbox/.local/bin/tlc-run`, `TLC_WORKERS=2 TLC_HEAP_MB=1024 -fp 21`, one private `-metadir` per arm, isolated model/cfg copies under `/tmp/opencode/r56-tlc`, logs + `.exit` files `r56-tlc/<cfg>.log|.exit`; worker logs `/tmp/opencode/maint-a/` (first pass) and `/tmp/opencode/maint-b/` (post-comment-edit re-run) kept for comparison), the ConstructionChain + CoreAuthoringProtocol model phases from a **clean 218edabb archive extract** (`/tmp/opencode/r56-models`, `.log|.exit` recorded), the two releaseID probes re-executed from `/tmp/opencode/relid-repro` after a **byte-diff proves the probe copy matches the live `release.go`/`dataframe_selector.go`/`validation.go`/`ref.go`**, the REPAIR-20/20b suite re-run on the current tree (`rec-r56-fr20-green.log`, `rec-r56-r20b-twinpin.log`, `rec-r56-fr20-acceptance.log`) and the OpenAPI regen determinism re-proved (`GOPROXY=file://$(go env GOMODCACHE)/cache/download`, oapi-codegen v2.8.0, output `/tmp/opencode/r56-regen/api.gen.go`), plus every cited source line read in place and a full re-hash census over `verification/builder-interactions/reviewed-sources.json`. Worker sessions handle: formal-maintenance `ses_e65d22483ffei8FqkLr6ewyLQG`, relid investigation `ses_e65c40afbffe7cum91XM5tFCkS`, 20b re-review `ses_e65cad76effe` (IN FLIGHT), ROW-SOURCE-01 adversarial review `ses_e65af3b21ffe` + §18 journey `ses_e65af1cbff` (IN FLIGHT) — operator-provided, **record-not-claim**: nothing pending is presented as a verdict. **Zero new product bugs were established this round.** Budget per AGENTS: `tlc-run --budget` reported cpu_limit 256 / worker budget 254 / no memory cap; nine concurrent arms × 2 workers = 18 (well inside), per-job heap 1024 MB.

### 1. Formal-maintenance pass — VERIFIED then recorded

- **Trio md5s HELD (record-writer SELF-RUN `md5sum` on the working trees):** `ReleaseBindingTrust.tla` = `3b0cd15edeaa3534c03c0812948c8e5b`, `ReleaseRevisionIdempotence.tla` = `ba0976d3c80c6399922c56102438bccc`, `PointerOrphanLifecycle.tla` = `9a9ff841696820a16e272274a1cfeb68` — exactly the pins the maintenance worker recorded in `reviewed-sources.json`.
- **Nine-arm self-run, verdicts all match the worker logs and the ledger table** (`/tmp/opencode/r56-tlc/`, exit codes captured): m-po-fixed exit 0 CLEAN (1056 gen/266 distinct, depth 8); m-po-bug exit 13 temporal properties VIOLATED over the model's `OrphanGC` fairness formula (:130-131); m-po-janitor-blind exit 12 `NoBoundDeleted` VIOLATED; m-rebind-holds + m-rebind-caseholds exit 0 CLEAN (206 gen/69 distinct each); m-rebind-violation + m-rebind-case exit 12 `CorrectBinding` VIOLATED with the archived witness-shape action depth 3 (`Publish`; `PrepareRelease`; `Activate` — record-writer SELF-RUN trace-action count, consistent with the r42 "shortest trace depth 3" wording); m-rri-fixed exit 0 CLEAN (3 gen/2 distinct); m-rri-shipped exit 12 `Idempotence` VIOLATED. **0 warning-lines in all nine logs** (record-writer SELF-RUN `grep -ci warning`). Violated-arm `states generated` counts vary run-to-run within a few states (TLC multi-worker BFS termination timing at the counterexample frontier: e.g. janitor-blind worker 139/67 vs self-run 156/73) — clean arms are byte-exact across worker and record runs; only the verdicts and invariant names are treated as binding, matching the r42 record's own discipline.
- **Verdict binding to clean 218edabb (honesty mechanism verified):** the two edited models differ from `218edabb` by **comment lines only** (record-writer SELF-RUN `git diff --numstat 218edabb`: 2/2 and 1/1; diff content is the two anchor citations below); `PointerOrphanLifecycle.tla` and **every `.cfg`** are byte-identical to `218edabb` (SELF-RUN `git diff --name-only 218edabb` over the dir = the 4 files: 2 models + SKILL.md + reviewed-sources.json). Additionally the record-writer **re-ran ConstructionChain (61/61, exit 0) and CoreAuthoringProtocol (145 gen/69 distinct, exit 0) from a clean `git archive 218edabb` extract** with dedicated metadirs — proving the clean-worktree verdicts are independently reproducible, not live-tree artifacts.
- **Comment-drift anchors spot-checked (record-writer SELF-RUN source reads):**
  - RBT `:175-178` — **exact**: `internal/dataset/arango/release_store.go` LET updates CAS patch = :175-177 closing :178 (F-B15 zero-write comment block :172-174); `alreadyActive` :162, `selectedRevision` :163; `FindExecutionBySelector` at `bundle_registry.go:170` with lowercase-only `publishedAt` SORT :179 (no `PublishedAt` fallback in the SORT clause — the model's zero-ordering-key claim holds); `release_verifier.go:35-39` verbatim `ExecutionID: execution.ID` echo.
  - RRI `:163,173-178` — region is correct but **imprecise**: the re-patch statements themselves are :175-177; :172-174 is the F-B15 comment block contained by the cited span. Recorded as-is (no functional error); future syncs should prefer `:163,175-178`.
  - RRI model citations otherwise true of current code (SELF-RUN): `Create` at `internal/dataset/release.go:147`, `Activate→Create` at :247, `releaseID` content fold :288-295 with **revision excluded** per F-B15; RRI :147 does NOT reference release_store's AQL region (it cites that file's `Create`).
- **Outcomes recorded (the four verdict lines):** `PointerOrphanLifecycle` **IN SYNC**; `ReleaseBindingTrust` **UPDATED (comment-only) + IN SYNC**; `ReleaseRevisionIdempotence` **UPDATED (comment-only) + IN SYNC**; plus prior-wave `BundleCommitLifecycle` IN SYNC carried in the ledger. The "first-ever TLC of current bytes" characterisation is from the worker's ledger-history read plus the models' own wave-F note that these shipped comment-maintained but never TLC-run (`ReleaseBindingTrust.tla` maintainNote; the r27/r31/r42 logs were recycled per that ledger) — the record-writer's nine-arm run **corroborates current bytes check clean/violated-classically**; the historical "never run" fact itself is quote-read, not independently re-derivable today.
- **ConstructionChain IN SYNC — with the honest live-tree pin split.** All 29 pins match their `218edabb` blob hashes (record-writer SELF-RUN python census over `verification/builder-interactions/reviewed-sources.json`: **29/29 vs clean commit blobs**, both raw-content and git-blob framing). In the **live worktree today only 25/29 match**: the 4 drifted files are exactly the uncommitted ROW-SOURCE-01 edits (`useConstructionLifecycle.ts`, `construction_validation.go`, `construction_proposal.go`, `construction_change.go`). **Ledger correction recorded against the worker maintainNote wording** ("re-hashed against the HEAD worktree; 28 matched"): as re-hashed against the live worktree the match count is 25 — the 28/1 story is true only against **clean 218edabb blobs**. The `internal/store/arango/client.go` re-pin `aaed9386…`→`f0e43698…` for 8160bb97 is verified TRUE to the 218edabb blob (SELF-RUN); 8160bb97 added composite interfaces (`TransactionalRowClient` at :59/:67, compile-time publish-CAS gate) and the client appears in no model step/variable — no-effect judgement endorsed.
- **CAP replay IN SYNC, ran map-mode; Arango lane BLOCKED-locally, never presented as pass.** Worker artifact read: `maint-a/cap-generated.log` — "production replay PASS" with boundary text `map store by default; every schedule also replays against real ArangoDB when LOOM_TEST_ARANGO_URL is set`; cases=8, completed=6, rejected=2, states=69, transitions=141, graphControls=15, TLC cap model 69 states PASS. Record-writer SELF-RUN: `LOOM_TEST_ARANGO_URL` **not present in this environment** (`env` grep) ⇒ the Arango lane has not run and is not claimed; `TestCoreAuthoringProtocolModelReplay` exists at `core_authoring_protocol_formal_replay_test.go:706` and exits 1 without the two `LOOM_FORMAL_CORE_PROTOCOL_INPUT/OUTPUT` env bindings (the disclosed `-skip` convention; SELF-RUN confirmation), and the full-model verdict above was re-derived by the record-writer from the clean 218edabb extract instead.
- **Uncovered list recorded (what the models do NOT describe):** (1) `filterSources` row-source code deliberately **not modeled until the ROW-SOURCE-01 slice ships**; (2) `PointerName` byte-fold class **go-witnesses only** — WHY: no model carries a PointerName/byte variable, and the injective length-prefix encoding (`bundle.go:100-115`) makes the models' one-row-per-identity abstraction faithful post-`8c21eb64`; the pre-fix fold-hijack class stays UNMODELED (witnesses: `pointer_name_encoding_test.go`, `clickhouse/pointer_name_nul_ambiguity_e2e_test.go` — both present on disk, SELF-RUN); (3) `ReleaseBindingTrust` Recheck=TRUE models a **proposed** activation-time re-verification — the F-B6 witness-pair gap (no shipped recheck) is the point of the Recheck=FALSE pair, not a model defect; (4) releaseID fold UNMODELED (see §2). Cleanup ticket opened: `m-po-probe.cfg` is obsolete (superseded by the m-po-{fixed,bug,janitor-blind} set) — remove in the next maintenance wave.
- **Worker's uncommitted edits, verified and booked to the NEXT record commit** (record-writer SELF-RUN `git diff HEAD --stat`: 2 .tla comment fixes as adjudicated above; `SKILL.md` **+34/−2** = the "TLC-bound trio entry points (last synced 218edabb)" section whose expected-verdict table my nine-arm run just confirmed verbatim, + the pins-refresh paragraph; the two `reviewed-sources.json` machine edits — publication-bundle trio maintenance fields (`lastSyncCommit`, `maintenance20261102`) and builder-interactions `baselineCommit` → `218edabb`). The record-writer staged nothing.

### 2. releaseID UNREACHABLE close — self-probed, then recorded; HARDENING TICKET live

The bug candidate (`internal/dataset/release.go:288-295`): `releaseID` bare-joins `"%s\x00%s"` over `Project`,`Generation` and per-publication `"\x00%s\x00%s"` over `Selector.Key()`,`ExecutionID`, while `Key()` is already length-prefixed — the fold is structurally NOT injective if a `\x00` ever reaches it. **Classification: UNREACHABLE, closure argued on four independent lines, then self-probed** (investigation `ses_e65c40afbffe7cum91XM5tFCkS`; probes re-executed by the record-writer on current bytes):

- **Line 1 — validation:** the only constructor `dataset.NewRef` (`ref.go:14`) runs `Ref.Validate` → `validateOpaqueIdentifier` (`validation.go:27-45`), whose `unicode.IsControl` sweep (:40-43) rejects `\x00` (any control) for BOTH fields; `ReleaseService.Create` funnels every request through it. SELF-RUN probe `TestProbePublicCreateRejectsControlBytesProjectGeneration` PASS: all four control-byte shapes (incl. `\n`, `\x01`) refuse with `invalid dataset reference: … must not contain control characters`.
- **Line 2 — mint single-site:** ExecutionID is minted exactly once via `uuid.NewString()` at `bundle_store.go:175` (assigned :189); the only other `uuid.NewString` in the file (:1082) is the `reconciler-` identity, not an ExecutionID; the public verifier echoes `execution.ID` verbatim (`release_verifier.go:35-39`) — the fold never fabricates bytes.
- **Line 3 — selectors can't smuggle:** `Key()` is length-prefixed (`dataframe_selector.go:34-35`) and SELF-RUN re-probed: `TestProbeKeyEncoderInjective` **exhaustively covered 64,000,000 triples** (alphabet `a b 0 1 : NUL -`, lengths 0..3), **zero collisions**, greedy decoder round-trips all. Compiler-side output names pass safeName (alphanumeric/underscore sanitiser `compile.go:791-807`) plus the label gate `namePattern.MatchString` before acceptance (:785) — the double regex.
- **Line 4 — store fail-closed:** a same-key-different-content SaveRelease cannot overwrite — `release_store.go:35-37` returns `release already exists with different content` when the compat FILTER (:139-141) rejects (the probe's `probeReleases` mirrors this and passed the replay arms).
- **Breached-space numbers, now self-derived (never propagated):** adversary `TestProbeFoldNotInjectiveGivenNULKeyOrEmptyExec` over the NUL/colon adversarial component alphabet: **13,444,704 distinct-content fold pairs explored, 0 collisions found** (self-run `OBSERVED fold injectivity adversary: 13444704 publication options, 0 distinct-content fold collisions found`). Read precisely: no collision INSTANCE exists in the explored space; the non-injectivity of the encoding is nonetheless real in principle (it depends on the NUL-freeness of the joined fields), which is why this is closed UNREACHABLE-with-hardening-rather-than-wontfix.
- **F-B21 reachability contrast (recorded intentionally):** the identical fold class was LIVE-reachable for `PointerName` (pre-`8c21eb64` bare `\x00` join with attacker-influenced Name/OutputName) and was fixed by length-prefixing + 36873-sequence decoder attack; `releaseID`'s joined fields are all control-free-by-construction or uuid-minted, so no named victim exists here (honest distinction, mirrors the projectid-percent-decode hardening posture).
- **HARDENING TICKET recorded, UNASSIGNED:** length-prefix the `releaseID` fold (same `len:content` encoding as `Key()`/`PointerName`) to close the residual by construction rather than by argument — it retires the dependence on validation completeness for the **operator-config-supplied components** (CLI config / server options paths; the `internal/server/options.go:307-309` loop is the selector-validation site in that path). No named consumer harmed; ticket only, no model without a named consumer.

### 3. REPAIR-20 (F-R3 CONFIRMED) + REPAIR-20b — fixed, independently reproduced, re-review IN FLIGHT (record-not-claim)

- **F-R3 CONFIRMED** via independent reproduction (not propagated): `/tmp/opencode/fr3-red.log` quote-verified — `F-R3 FIDELITY GAP CONFIRMED`: the REST/ETL recipe-execution document omits `error/errorCode/errorRetryable/phase` at execution level and `errorCode/errorRetryable/phase` on outputs while the GraphQL twin publishes them for the same FAILED execution; the ETL reader raw-echoed the persisted internal error (`"clickhouse insert rejected"`) instead of the twin-encoded public message. Worker review first-pass F-1/F-2 (raw-error-echo leak + empty-field divergence, **18/64 matrix cells**) confirmed from the archived RED line, SELF-RUN read verbatim: `R20B TWIN PARITY FAILURE: 18 of 64 comparisons DIVERGE … (F-1/F-2: the REST reader must publish the twin's FULL PersistedFailure/SanitizePersistedFailure encoding, not a raw copy)`.
- **20b fix = the twin's OWN chain, verified in code + re-run:** `internal/server/openapi_recipe_execution.go:106` and `:133` now call `dataframeapi.PersistedFailure(...)` — the same constructor the GraphQL twin uses (SELF-RUN). Matrix: 8 shapes × {exec,out0} × {error,errorRetryable,errorCode,phase} = 64 verdict-level comparisons against `testdata/r20_failure_parity/twin_golden.json` (reviewer-commuted golden; `shapes.json` + `twin_golden.json` present on disk, SELF-RUN). **Record-writer SELF-RUN on the current tree:** `TestFR20*` + `TestFR20bEtlFailureFieldsTwinParityMatrix` → exit 0, `R20B VERDICT | PASS | 64/64 comparisons MATCH the twin oracle`, zero DIVERGE lines (`/tmp/opencode/rec-r56-fr20-green.log`); resolver twin-pin `TestR20TwinWireMatchesReviewedGolden` ok (`rec-r56-r20b-twinpin.log`); `internal/acceptance` golden-pair e2e ok 0.209s (`rec-r56-fr20-acceptance.log`); 404/503 arms unchanged by design (PASS in both archived and archived-red runs).
- **Spec state:** `openapi/openapi.yaml` carries exactly **8 additive nullable props** for REPAIR-20 — `error/errorCode/errorRetryable/phase` on `RecipeExecution` (+4, :1821-1824 region) and on `RecipeExecutionOutput` (+4, :1836-1839 region); the rest of the uncommitted spec diff is ROW-SOURCE-01's `filterSourceInput`/`ConstructionFilterSourceInput` (separate, §4). **Regen determinism: record-writer SELF-PROVED** with the file-proxy recipe (`GOPROXY=file://$(go env GOMODCACHE)/cache/download …oapi-codegen@v2.8.0`, config copied to /tmp with only `output:` redirected — repo untouched): regenerated `api.gen.go` is **BYTE-IDENTICAL** to the worker's on-disk `generated/loomapi/api.gen.go`, sha256 `ee1649f377f7054ec5afbb5b7dd8625adfe9197ce5507ec5a374dc115a28fba0` (`/tmp/opencode/r56-regen/`). The network-blocked first attempt is honestly archived (`rs01-regen-err.log`, proxy.golang.org dial refused).
- **Status discipline:** re-review `ses_e65cad76effe` **IN FLIGHT** at record time (no landing artifact verified in-tree or /tmp by the record-writer) ⇒ REPAIR-20b is **recorded, NOT claimed complete**; commit stays gated on that verdict.

### 4. ROW-SOURCE-01 — status chain (REVIEW-PENDING; no completion claim)

Chain as self-checked this session: server RED→GREEN (`rs01-server-red.log`→`rs01-server-green.log`); capability/proposal reconcile with the **6-field signed choice** verified in source (`filter_source.go:31-38`: `choiceId, fieldPath, label, logicalType, isPopulated, columnId`; :25-30 comment bans pivot-style extras) including the **fail-closed matrix**: honest-client `columnId` alone never vouches for a vanished field (`filter_source_test.go:886,940-943` — restore-of-vanished-field refused at the proposal boundary) and the `STALE_FILTER_SOURCE_BINDING` conflict gate at apply (`construction_proposal.go:1614-1616`); UI **1123 tests / 85 files green with wire-body proof** (`rs01-ui-final.log` — `Tests 1123 passed (1123)`, SELF-RUN read; wire-body evidence `rs01-ui-green-wire.log`); HTTP gap caught+fixed via a RED e2e (survived the typed strict-server models: `rs01-http-red.log` shows the capabilities wire response **dropping `filterSourceInput`** pre-fix at `openapi_explorer_construction_filter_source_test.go:169`; GREEN `rs01-http-green5.log` ok 0.335s — the egress/ingress drop class). **Deliberate production-surface disclosure for the adversarial reviewer (flagged for hardest scrutiny):** the new FILTER branch in `internal/server/explorer_output_reconcile.go` (authored-outputs switch case, insertion at :1051+, self-read) — it demands the projection column equal `step.Operation.Filter.ColumnID`, requires a declared stable ID/name/label, pins the compiled source identity (`prior[declared.ID] == declared.Name`), rejects duplicates, and stamps `requires-review`/`CONSTRUCTION_FILTER_SOURCE_ROWS_FILTERED` quality honestly. Adversarial review `ses_e65af3b21ffe` and the §18 journey `ses_e65af1cbff` are **IN FLIGHT** (journey artifacts under `/tmp/opencode/rso-journey`, review scratch under `rso-review`, present; verdicts NOT landed and not claimed). Slice stays **review-pending**; the four drifted pinned files (§1) are exactly this unshipped work.

### Ledger-integrity + program state

- **Ledger integrity:** `reviewed-sources.json` round 55 → **56**, parses and re-serializes **byte-identically** (record-writer SELF-RUN `json.dumps(indent=1, ensure_ascii=False)+"\n"` roundtrip == on-disk bytes); new keys `status_updates.round56`, `repair_units["repair-20"]`, `sideFindings.releaseID-fold-hardening`; the pointerOrphanLifecycle / releaseBindingTrust / releaseRevisionIdempotence entries gain record-writer `recordVerificationR56` fields (self-run log paths + the exactness/caveat notes above). Builder-interactions correction recorded in THIS bundle's ledger (§1 pin-census wording) — the worker file itself was not re-edited by the record-writer.
- HEAD `218edabb121e1c14e9466f4f47b1b635f0b9511e` (SELF-RUN `git rev-parse HEAD`), unchanged all round. Porcelain remainder: the maintenance worker's 5 files (2 .tla + SKILL.md + 2 ledgers — riding the NEXT record commit as booked) + operator-owned doc edits (`AGENTS.md` +10/−1, `BUILDER_WORKLIST.tsv` +4, CONT doc +7/−11, SELF-RUN numstat) + the REPAIR-20b / ROW-SOURCE-01 product+test files + record-writer's 3 files + tooling dirs. **No unexplained remainder.** Workers' session handles pending are record-not-claim; nothing in this round waits on a verdict that is presented as landed.

Record integrity tail (Round 56, written last): record-writer's three files at write time — FINDINGS.md sha256 `2cc5abfe2c0dd6b3836c4948d8419dea72f4839a037af874484cb46779544d5c` **excluding this tail paragraph** (hash of the file before this paragraph was appended; verify by truncating the file at the start of this tail paragraph, i.e. `open(...).read()[:read().index(chr(10)+'Record integrity tail (Round 56, written last):')]`, and hashing those exact bytes), OPERATOR_REVIEW_DIGEST.md sha256 `92d157c7b32659d0d904b3e0b459125f2934cbae917ddcaa183e10a789ac1b08` (full file), reviewed-sources.json sha256 `2d6c62e69800f3415c568a322f723b437694dd2e8ce74a4bc6c50e4d8fb020da` (full file, round=56, canonical `json.dumps(indent=2, ensure_ascii=False) + "\n"` roundtrip BYTE-IDENTICAL — SELF-RUN; note: the r52-r54 records cited `indent=1`, which never matched this file's actual on-disk 2-space form; the verified-canonical recipe is recorded here so future byte-stability claims are reproducible). My-run evidence hashes: r56-tlc nine arms + r56-models two arms (logs+.exit per arm), rec-r56-fr20-green.log (64/64, exit 0), rec-r56-r20b-twinpin.log, rec-r56-fr20-acceptance.log (ok 0.209s), r56-regen/api.gen.go sha256 `ee1649f377f7054ec5afbb5b7dd8625adfe9197ce5507ec5a374dc115a28fba0` == generated/loomapi/api.gen.go byte-for-byte (cmp, SELF-RUN), relid probes re-run on bytes byte-diffed equal to live. `verification/publication-bundle/*` sole-writer respected: the 5 worker files (2 .tla comments, SKILL.md, 2 ledgers) were verified in place and left untouched, booked to the next record commit; nothing was staged or committed; HEAD `218edabb121e1c14e9466f4f47b1b635f0b9511e`. In flight, record-not-claim: REPAIR-20b re-review `ses_e65cad76effe`, ROW-SOURCE-01 adversarial review `ses_e65af3b21ffe` + §18 journey `ses_e65af1cbff`. No pending verdict recorded as landed; zero new product bugs this round.

## Round 57 — REPAIR-20/20b CLOSED-COMMITTED (`caba32df`, full F-R3 closure cycle; twin-side lease-lost phantom tracked); ROW-SOURCE-01 DELIVERED-COMMITTED (`c53759fe`, operator task; §18 JOURNEY ACCEPT); ROW-SOURCE-02 unblocked + queue derived; formal-pass verification-file edits land with one disclosed anchor alignment

Attribution: the record-writer SELF-VERIFIED every decisive claim before recording — `git show --stat` + full commit-body reads for both cited commits, `git log --oneline -3`, the twin-chain and R1-arm source lines in committed bytes, the 6-field choice contract, the lease-lost stamp shape at its cited lines, the full journey ACCEPTANCE-REPORT v3 + api-negatives table (incl. the c759057≠ec2e475 cross-snapshot row), an exact-18-cell list equality run (python), diff-only spec-split marker counts, the rs01 artifact census, and a post-edit re-run of both RRI model arms. Attributed record-not-claim items (operator round-57 brief + commit-body receipts): the reviewer's personal 64/64 re-run and 18-cell kill-exactness verdict (`ses_e65cad76effe`), ROW-SOURCE-01 F-1 CLOSED + R1 accepted (`ses_e65af3b21ffe`), the reviewer-retracted F-3 misread note (SELF-RUN greps of `rso-review`/`rso-rereview` scratch found **zero** F-3/retraction lines — attributed, no on-disk backing; recorded so, not presented as artifact-backed), and the DERIVE/UNPIVOT matrix judgements (r55 investigation `ses_e65e0e876ffe` matrix + brief). **Zero new product-bug verdicts established this round.** HEAD `c53759fea90d2502f1f9dfcee7d6e9a8712bc050`.

### 1. REPAIR-20 / REPAIR-20b — CLOSED-COMMITTED `caba32df3e10e460b5a2fbc94027728abfe0248c`

Full-cycle narrative, every step self-verified this session:

1. **Origin & confirmation.** F-R3 (recorded r45: ETL status omits explicit failure fields the twin publishes) independently CONFIRMED by reproduction — `fr3-red.log:2` CONTROL line quote-read: the GraphQL twin publishes `error/errorCode/errorRetryable/phase` (exec + outputs) for the same FAILED execution while REST/ETL omitted them and raw-echoed persisted internal text.
2. **Adversarial catch (the class the original e2e could not see).** Re-review `ses_e65cad76effe` (same-model, disclosed, both rounds) discovered a **18/64 divergence class** the pre-review candidate matrix showed only after scrutiny was tightened: (a) **raw durable text leak past the sanitizer** — reviewer table (`/tmp/opencode/r20b-reviewer-table.log`, SELF-RUN read) shape `3_code_empty`: REST published `error="raw driver text boom"` / `errorCode=""` / `errorRetryable=true` while the twin publishes the `SanitizePersistedFailure` public message `"an unexpected internal error occurred while processing the data request"` / `INTERNAL_ERROR` / `false`; (b) **presence-field divergence** — the candidate hand-copied the output/execution failure fields instead of building them through the twin's own constructor chain. The original e2e was **structurally blind** to this class: its CONTROL arm asserted the reader's **pre-sanitization persisted intermediate**, not the twin's final wire encoding (`fr3-red.log:2`, `r20b-red.log:2` CONTROL lines SELF-RUN quote-read).
3. **Fix = the twin's OWN chain.** Both failure trios now route through `dataframeapi.PersistedFailure` — SELF-RUN on committed bytes: `internal/server/openapi_recipe_execution.go:106` (output level) and `:133` (execution level) — with the same non-empty Phase gates and the `output.error` MIRRORS-`execution.Error` reader precedent. All-nil persisted state keeps the omitempty keys absent (additive-optional contract).
4. **Reviewer receipts + kills.** Kill re-derivation SELF-RUN-PROVEN: the 18 named diverging cells in `r20b-red.log:87` and in `r20b-kill1-sanitizer-revert.log:87` (sanitizer-arm revert) are **list-equal** (python set/list equality, both 18) — reverting the sanitizer arm re-creates the reviewer's exact cell signature; kill2 (copy-block revert to hand-copied fields) re-fails the matrix at **46/64** (wider class, as expected — errorCode/errorRetryable/phase diverge too); both kill logs end `FAIL` (SELF-RUN tails). The **reviewer-personal 64/64 re-run + exact-18-cell kill verdict** is attributed to `ses_e65cad76effe` per the commit-body receipt (record-not-claim); the 64/64-PASS logs exist on disk (`r20b-green.log`, `r20b-proof-e2e.log`) — r56 already SELF-RAN its own 64/64 green pass.
5. **Commit with deterministic split keeping unreviewed schema out** — SELF-VERIFIED: `git show --stat caba32df` = **9 files +1852/−225** (`openapi_recipe_execution.go` +62; `recipe_execution_etl_failure_fields_e2e_test.go` NEW 624; resolver `recipe_execution_twin_wire_parity_test.go` NEW 160; server + resolver `testdata/r20_failure_parity/{shapes,twin_golden}.json` 210+176 each; `openapi/openapi.yaml` +8; `generated/loomapi/api.gen.go` regenerated). Diff-only marker counts SELF-RUN: **0** `filterSource` occurrences in the committed `openapi.yaml` diff (exactly +8 added lines), **0** `ConstructionFilterSource` strings in the committed `api.gen.go` diff — the row-source hunk pair stayed out. Body records both-direction CONTROL regen (both-features yaml → current `api.gen.go` byte-identical pre-split; committed gen file regenerated from exactly the committed yaml), consistent with the r56 SELF-PROVED regen determinism (`ee1649f3…28fba0` recipe). Tree proof at the staged snapshot in a disposable worktree (build/vet/tests + `make openapi-check` + focused R20B 64/64 & R20-TWIN 8/8 captures).
6. **Twin-side lease-lost phantom — tracked, deliberately NOT fixed (unit scope = EXACT twin parity).** SELF-RUN read `internal/dataframe/publication/clickhouse/bundle_store.go:1207-1213`: the lease-lost reconcile demotion sets `State=BundleFailed`, `Error="stale execution reconciled"`, `FailureCode=CodePublicationLeaseLost`, `FailureRetryable=true` over `Outputs=remaining` — the mirrored execution.Error is sanitized **per output**, so healthy READY outputs inherit a phantom INTERNAL_ERROR trio. Twin-side defect (mirror + per-output sanitizer gate); REST reproduces it verbatim per the EXACT-parity contract and the matrix PINS the reproduction (shapes `5_lease_lost`, `6_output_fault_only`). Recorded as a tracked follow-up in `repair_units['repair-20'].sideFindings` + a top-level `sideFindings` entry (twin-side repair flips both sides + the golden together).
7. **Ledger:** `repair_units['repair-20']` → **CLOSED-COMMITTED** with `commitState=caba32df…`; `commitGate` → **+caba32df** (§2 adds +c53759fe).

### 2. ROW-SOURCE-01 — DELIVERED-COMMITTED `c53759fea90d2502f1f9dfcee7d6e9a8712bc050` — operator task closed (worklist row 94)

**This is the first operator-assigned Builder product task delivered end-to-end under the factory-intake rules and committed. Highlight: operator task completion.**

- **Commit verified by the record-writer directly:** `git log --oneline -3` → `c53759fe` feat(builder): ROW-SOURCE-01 · `caba32df` fix(api) F-R3 parity · `218edabb` docs(verification) round-55; `git show --stat` = **18 files +4631/−585** (tail of the stat verbatim: `ui/packages/loom-ui/src/types.ts +26`, `ui/packages/loom-ui/testSupport/liveConstructionCapabilitiesFixtures.ts NEW +1135` — the durable anti-masking cert). `openapi/openapi.yaml` + `generated/loomapi/api.gen.go` porcelain-clean post-commit (SELF-RUN): both spec features now on HEAD — supersedes r56's "working copies still carry both features".
- **Selection + contract (SELF-verified in committed bytes):** Pivot source-input fields proven WORKING first (contract forbids rebuild) ⇒ no Pivot work; first REAL gap = FILTER consuming eligible unselected root scalar fields. Pinned **6-field server-signed choice** `internal/explorer/lifecycle/filter_source.go:31-38` = `choiceId/fieldPath/label/logicalType/isPopulated/columnId`; server-derived honest-`""` columnId when the materialization vanished/re-pointed/foreign-owned (`constructionOwnedFilterSourceColumnID`, :340-region comment SELF-read); request side strict (`requireExactIdentity`).
- **F-1 reconcile hardening CLOSED via the SHARED `lifecycle.ConstructionFilterSourceFacts`** single source of truth — `filter_source.go:367` SELF-RUN — reviewer-proven load-bearing; armed snapshot required (DeepEquals on snapshot facts) + slot tie for repeated contributors. **R1 blank-token fail-closed confirmed at the exact line:** `internal/server/explorer_output_reconcile.go:1100` `strings.TrimSpace(filterSourceSnapshot.Token) == ""` refuses claiming filter-source facts from an unarmed snapshot.
- **§18 JOURNEY ACCEPT — authoritative run:** `/tmp/opencode/rso-journey2/ACCEPTANCE-REPORT.md` ("ACCEPTANCE REPORT v3 (authoritative)", `ACCEPTANCE-VERDICT: ACCEPT`, SELF-RUN full read) — ONE raw-CDP run on the pristine **rso-journey5** fixture (`loom_dev_verify_rso-journey5`): optgroup mount "Dataset fields (not in output)" (Patient · Birth Date/Gender/ID/Meta Last Updated); **oracle-exact rows computed independently of the compiler** from `testdata/devloop-fixture/Patient.ndjson` pre/post apply (`["dev-patient-001"]`); **materialized-echo reload assertion** (capability echo gender columnId == persisted `filter-input_31e1a292…`, other fields ""); edit (value→male ⇒ preview `[]` asserted; →female restores) / REMOVE (`filterSteps 1→0`, identities unfiltered, `sourceProjectionsAfter: []`) / RE-ADD (honest re-materialization `filter-input_32d24ba1…` owner `filter_e139ddec…`, capability echo equal) — all with verbatim durable-doc JSON in the report; **7/7 API negatives refused** with named diagnostics, incl. N2 **cross-snapshot proof** — live cross-project token with embedded snapshot `c759057…` ≠ current `ec2e475…` → 409 `INVALID_FILTER_SOURCE_CHOICE` (grep-found in `api-negatives.run.log`/`ACCEPTANCE-REPORT.md`). Standard build-stamp bracket held, never weakened: `dd65c951…/dd65c951…/0fa580df…` exit 0 pre/post every run + negatives. **Artifact-placement disclosure (verified correction):** `/tmp/opencode/rso-journey5/` and `rso-journey6/` hold ONLY the `builder-setup.json` seed (+ `.seed.out`); the authoritative journey5 run's artifacts were written under `rso-journey2/` (the report itself states `journey2.net.json` = "journey5 stream") — future verifiers must look there, not at the dir named in shorthand.
- **Reviewer + honesty chain:** adversarial review `ses_e65af3b21ffe` two rounds — F-1 CLOSED + R1 accepted (commit-body receipt; attributed). The **reviewer-retracted F-3 misread note** is recorded from the round-57 brief as attributed only — SELF-RUN scratch greps found no on-disk trace (zero `F-3`/retract hits in `rso-review`/`rso-rereview`). Commit preflight honesty (commit-body SELF-RUN read): **2 brief-path errors caught** (`lifecycle/construction_validation.go` phantom; fixture dir) and the **4th threading file caught by the tree proof** after initial preflight (`explorer_output_reconcile_source_child_test.go`) — all disclosed, zero guessed content. UI gate: workspace vitest 1128/1128 recorded on the identical staged working bytes (`rs01-ui2-workspacetest.log`, commit-disclosed not re-run in the proof worktree). RED/GREEN census corrected: **43 `rs01-*` artifacts total under `/tmp/opencode` — 41 are `.log`** (the commit body's "43 rs01-* logs" counted all artifacts; SELF-RUN `ls | wc -l` both ways). Disposable probe `internal/server/zz_rso_probe_test.go` (r56 note) reached **zero commits** (`git log --all` zero hits) and is absent on disk — deleted pre-commit; the r56 "uncommitted pending review" wording is now moot.
- **Todos:** revision 9 progress revision recorded reflecting 01 COMPLETE (harness-local runtime state; recorded here per precedent, not a repo file).

### 3. ROW-SOURCE-02 unblocked (01 landed); 03 unlocks under the frozen contract; 04 stands

Queue is matrix-derived (r55 investigation matrix `ses_e65e0e876ffe` + operator round-57 brief; attributed record-not-claim):
- **ROW-SOURCE-02 — DERIVE next:** incidentally selected-only per the matrix and row-preserving; its operand channel needs a signed source shape and 01's `ConstructionFilterSourceFacts`/signed-token pattern is directly reusable. **UNPIVOT was judged legitimate-columns-only in the matrix** (only a narrow value-column set qualifies), so it is not queued ahead of DERIVE.
- **ROW-SOURCE-03 — [I]-gaps now implementable** under 01's frozen capability contract: per-choice AND per-exclusion `reasonCode` enum, server-side field titles sourced from the generated FHIR Title, an `UNSUPPORTED` option-state in-contract, and per-input stage-carry reasons. **[P]-class picker items stay on the 03 list.**
- **Dispatch status: to follow at the next scheduling boundary — nothing dispatched at record time (honest).** ROW-SOURCE-04 remains **PROPOSAL-ONLY** (operator decision; do NOT implement).

### 4. Formal-pass verification-file edits — booked r56, landing in this record commit; one disclosed correction

- Verified-in-place before landing (SELF-RUN `git diff`): `ReleaseBindingTrust.tla` — two comment anchors `release_store.go:178` → `:175-178` (header + Publish); `SKILL.md` — +34/−2, the "TLC-bound trio entry points (last synced 218edabb)" section + pins-refresh paragraph, its expected-verdict table == the r56 nine-arm self-run; `reviewed-sources.json` — trio maintenance fields + baseline/sync pins.
- **Disclosed correction by the record-writer (commit-content owner, within the published sole-writer boundary):** the booked `ReleaseRevisionIdempotence.tla` anchor on disk read `release_store.go:163,173-178; alreadyActive arms skip writes`, while this ledger's own r56 adjudication established the precise span **`:163,175-178`** (the F-B15 comment block :172-174 sits inside the older span) and the round-57 brief booked the edit as *including* that recorded improvement. The record-writer applied the one-token alignment and re-validated the anchors against current `release_store.go` bytes (alreadyActive :162, selectedRevision :163, comment block :172-174, `LET updates` patch statements :175-177, closing `]` :178). Comment-only edit; both RRI arms re-ran post-edit via `/sandbox/.local/bin/tlc-run` (`TLC_WORKERS=2 TLC_HEAP_MB=1024 -fp 21`, one private `-metadir` each; logs `/tmp/opencode/r57-tlc/m-rri-{fixed,shipped}.log|.exit`): **m-rri-fixed exit 0 CLEAN (3 gen/2 distinct), m-rri-shipped exit 12 `Idempotence` VIOLATED (5/5), 0 warnings**. New md5 `f86d248cba7f5efbd38f73ce56a3323a` re-pinned in `model.releaseRevisionIdempotence` with `recordVerificationR57`.
- The **second baseline pin — `verification/builder-interactions/reviewed-sources.json`** (baselineCommit → `218edabb…` + client.go re-pin `aaed9386→f0e43698` + maintainNote) is **verified but stays UNCOMMITTED** — operator-side file, outside this commit's published scope (this commit carries exactly the six `verification/publication-bundle` M files).

### 5. Ledger + program state

- `reviewed-sources.json` round 56 → **57**; parses and re-serializes **byte-identically** under the r56-verified canonical recipe `json.dumps(indent=2, ensure_ascii=False)+"\n"` (SELF-RUN). Additions: `status_updates.round57`; `repair_units['repair-20']` → CLOSED-COMMITTED + `commitState`/`sideFindings`; top-level `sideFindings` + `twin-side-lease-lost-phantom`; `model.releaseRevisionIdempotence` md5 re-pin + `recordVerificationR57`; `commitGate` appended with `+caba32df` (+ the c53759fe feature entry).
- HEAD `c53759fea90d2502f1f9dfcee7d6e9a8712bc050` (SELF-RUN). Porcelain remainder after this record commit = operator doc edits (`AGENTS.md`, `docs/BUILDER_WORKLIST.tsv`, CONT doc) + `verification/builder-interactions/reviewed-sources.json` pin (operator-side) + runtime junk — **zero uncommitted product code** (post-`c53759fe` openapi + api.gen clean, SELF-RUN). No unexplained remainder.
- Review posture for this commit: **record-only** — it documents receipts already produced by the cited adversarial reviews; no product diff to attack ⇒ tree proof **N/A by policy for record-only changes**. Pre-commit checks actually run: both cited `git show --stat` reads, JSON parse + canonical byte-stable roundtrip, and the two RRI tlc-run arms as the `.tla` structural+semantic check.

Record integrity tail (Round 57, written last): record-writer's three files at write time — FINDINGS.md sha256 `ae67e27e5e6b5ff628b3c34114b116ab674119a67d79f397caab19a1510351a3` **excluding this tail paragraph** (verify: truncate this file at `chr(10)+'Record integrity tail (Round 57, written last):'` and hash those exact bytes), OPERATOR_REVIEW_DIGEST.md sha256 `0d646c0ce4cd58292996a62cdebcc9baa06944cc9abd79c4b71cdd7e7c112d55` **excluding that file's Round-57 tail paragraph** (same truncation recipe over the digest bytes), reviewed-sources.json sha256 `c98958d0a61fc2997abf43c687fbdf91b20f8538877dcefb2417088f62d27be8` (full file, round=57, canonical `json.dumps(indent=2, ensure_ascii=False)+"\n"` roundtrip BYTE-IDENTICAL — SELF-RUN). My-run evidence this round: /tmp/opencode/r57-tlc both-arm logs+.exit (fixed exit 0 CLEAN 3/2 · shipped exit 12 Idempotence VIOLATED · 0 warnings — doubles as the .tla structural check at commit), exact-18-cell equality (python, r20b-red.log == r20b-kill1-sanitizer-revert.log), diff-only split marker counts 0/0 with openapi +8 added lines SELF-RUN, rs01 census 43 artifacts (41 `.log`). Sole-writer respected: commit = exactly the six verification/publication-bundle M files (FINDINGS.md, OPERATOR_REVIEW_DIGEST.md, reviewed-sources.json, ReleaseBindingTrust.tla, ReleaseRevisionIdempotence.tla, SKILL.md); verification/builder-interactions/reviewed-sources.json left UNCOMMITTED (operator-side); nothing else touched, staged, pushed or amended. HEAD `c53759fea90d2502f1f9dfcee7d6e9a8712bc050`. Round-57 commit subject: 'docs(verification): round 57 — REPAIR-20b + ROW-SOURCE-01 receipts, formal-pass record files'. Nothing in flight presented as landed; ROW-SOURCE-02/03 dispatch status to follow.


## Round 58 — HARDENING `989f7e5a` CLOSED (releaseID length-prefix framing + multibyte kill-proofs + authscope concurrent-recorder polish + F-B15 anchor/ledger sync); SLICE-1 `b92548c7` process event handled with NO history rewrite (all four REQUEST-CHANGES items dispositioned); ROW-SOURCE-03 `cdaa2445` NO-BYPASS (F1/F2 closed — F3 to backlog; sandbox volume-bind defect class recorded, archive-only discipline); ROW-SOURCE-05 `b72a1322` PASS (RED 11 reviewer-count; mut-H2 backlog; derive-SET transient-only); FILTER 17×2 + DERIVE 24×2 journeys ACCEPTED at `b72a1322` in-barrier (scripts COMMIT-PENDING + integrity REVIEW-PENDING); F-DS1 CONFIRMED 500 class (named-422 repair patch REVIEW-PENDING); calibration + 6-pin drift root-cause and re-pin

Attribution: the record-writer's SELF-RUN command inventory with exits is the round's receipt `/tmp/opencode/record58.md` (14 numbered rows) — every claim below is either one of those reruns or a verbatim quote from a named worker artifact (`rs05-hardening-followup.md`, `b9-review.md`, `rs03-review.md`, `rs03-presentation-report.md`, `rs05-fix-report.md`, `rs05-review.md`, `xop-handoff.md`, `rs0-journey-commit.md`, `derive-journey-commit.md`, `fs1-report.md`, `fs1-fix-report.md`, `journeys-review.md`). The HARDENING adversarial-verdict artifact has no path — the reviewer inlined it to the operator; cited as **parent-held** (same-model, disclosed). HEAD `1d0fe6f4` at record time (SELF-RUN); the record-writer touched only its three publication-bundle ledger files plus /tmp; nothing staged or committed.

### 1. HARDENING `989f7e5a` — length-prefixed releaseID framing (unit CLOSED)

- Commit SELF-RUN stat: 5 files +575/−18 — `release.go` +82, `release_id_framing_test.go` NEW 360, authscope `all_generations_cache_key_test.go` +135, RRI `.tla` comment-anchors 4, ledger 12.
- Framing SELF-RUN in live bytes: `releaseID` at `internal/dataset/release.go:345` → `releaseIDFraming :356-365` → `writeLengthPrefixed :367-371` (`strconv.Itoa(len)+':'+content` per joined field; `Selector.Key()` emitted verbatim since it self-prefixes).
- Multibyte kill-proofs (attributed to `rs05-hardening-followup.md`; logs SELF-RUN-verified on disk under `/tmp/opencode/rs05-hardening/`): the reviewer's `utf8.RuneCountInString` mutant — which SURVIVED the original all-ASCII pools because `validateOpaqueIdentifier` (validation.go:31-44) admits any valid UTF-8 ≤512B control-free value — is now killed by two independent arms: exhaustive via CJK `あ` (`release_id_framing_test.go:228` round-trip failure) and random-probe via `Ωあ` (`:352`), `RUNECOUNT_EXIT=1` (`red-runecount.log`); the colon-drop mutant still kills all four arms (`red-colondrop.log COLONDROP_EXIT=1`). GREEN side re-executed by the record-writer at HEAD: 4/4 PASS with counts identical to the receipt — exhaustive P/G **1,881,349** distinct releases, Key-space **3,796,416** triples, random **300,000 → 248,624** frames, `ok 33.625s`.
- authscope concurrent-recorder polish (same commit, r52-deferred race-hygiene landing): mutex-guarded shared lister + `TestFB27ConcurrentDistinctKeyBurstKeepsSharedRecorderConsistent` (added lines SELF-RUN); receipt gate `go test -race ./internal/authscope` ok 1.198s (`authscope-race.log` on disk).
- F-B15 anchor/ledger sync: ledger F-B15 role + `releaseID-fold-hardening` sideFinding RESOLVED + RRI `.tla` `:10`/`:62` anchors re-pointed to `release.go:345/:356-365/:367-371`, comment-only. The no-TLC-rerun position is sound and doubly backed: comment-stripped executable token stream byte-identical (1712 non-comment bytes, nesting+line comments stripped) and the abstract `Identity()` never modeled the byte fold (reviewer-verified, consistent with the 8c21eb64 precedent); bounded replay logs on disk — fixed CLEAN 3/2, shipped 5/5 `Idempotence` VIOLATED (`rri-{fixed,shipped}-gate.log` tails SELF-RUN). **This record is that sync's continuation** — pins reconcile live: `release.go` md5 `1118624d9f2da439e5ae06ba479ad5ee` == ledger F-B15 pin (:600); RRI `.tla` md5 `56558ef9ffa913eb16b524ab43f5e5b1` == ledger pin (:800).
- Review: verdict **NO-BYPASS (parent-held, same-model, disclosed)**; the reviewer's REQUEST-CHANGES pair — F-1 suite hole (multibyte corpus) and F-2 falsifiable-records sync — **both closed** by the follow-up receipt. No on-disk reviewer receipt exists: recorded as parent-held, not artifact-backed.

### 2. SLICE-1 process event `b92548c7` — unauthorized worker commit, handled with NO history rewrite

The DERIVE worker committed outside the parent-owned commit gate. Handling (all SELF-RUN): the commit was **kept** — no reset/revert/amend; `b92548c7` stands in history; the gates were re-run by the interrogating reviewer at an extracted snapshot instead of trusting the commit message, and remediation rode the next authorized commits.

b9 interrogation (same-model, disclosed) — REQUEST-CHANGES, four findings; full disposition:
- **F1 [substantive] third cross-operator restore lane** (`derive_source.go:230` unconditional preserve; reviewer-run probe + **mutE** proved a call-site-only rs05 repair leaves the derive lane open): **CLOSED via `b72a1322` adopting the SHARED-HELPER variant** (xop-handoff item 2/3 vindicated). Live-byte SELF-RUN: the only non-test `preserveConstructionOwnedInputs` call sites remaining are pivot `construction_pivot_source.go:313` and derive `derive_source.go:230`, both gated through `constructionOwnerInputsClearedFromCandidate` inside the helper; the worker's m1-derive unique kill corroborates.
- **F2 [evidence hygiene] gofmt claim not true of the touched file** (`explorer_output_reconcile_derive_source_test.go` alignment hunk): **forward-fixed at `b72a1322`** — record-writer two-way proof: `gofmt -l` flags the b92548c7 blob, clean at b72a1322/HEAD blob.
- **F3 [evidence hygiene / scope] full-scope `go vet ./internal/...` fails while the commit claims vet PASS**: identical findings at parent `b92548c7^` (reviewer) and record-writer reran `go vet ./internal/acceptance/` — **EXIT 1, exactly 3 copylocks at `runner.go:109/143/201`** (`/tmp/opencode/r58-vet-acceptance.log`) — **PRE-EXISTING → NEW BACKLOG** (acceptance-runner copylocks). Slice-1's honestly-passing scope is the focused one (`./internal/explorer/... ./internal/server` EXIT 0; `b9-gates/`).
- **F4 [process] unauthorized commit — FINDING STANDS** (process defect on record; remedy = commit-gate discipline, commits parent-owned).

Functional integrity survived the reviewer's attacks: 8 forgery/misuse classes refused with named reasonCodes (no path accepts an unsigned choice or reconciles a vanished field); the four drift-refusal ledger guards were each mutation-killed one at a time (mut-A..D, each exit 1 killing exactly its paired subtest); server/UI/openapi six-field signed-choice contract byte-exact with the live-payload zod replay; real-compiler compile assertions + PROPAGATE_NULL lowering re-runs EXIT 0. **Derive journey 24/24 twice ACCEPTED at `b72a1322`** (§5). Still owed per worklist row 95 (operator-owned): independent multi-model panel for the slice-1 snapshot; the two F-DS1 operator decisions (§6).

### 3. ROW-SOURCE-03 presentation `cdaa2445` — review NO-BYPASS; F1/F2 closed; F3 → backlog; environment-defect class

- Commit SELF-RUN stat 8 files +472/−32, exactly the review's 5-M + 3-?? snapshot — no excluded-lane file rode it.
- Review **NO-BYPASS** (same-model, `rs03-review.md`): vitest 29/29 across the wave files (6+6+4+13), tsc 0 errors, DOM-escape proven (zero `dangerouslySetInnerHTML` in loom-ui/src; panel renders `{shapeState.message}` as a text node), the 100k-NUL+script-tag bundle probe returned-but-injection-refuted, and the CARRY_FORWARD zero-occurrence checks → NO-FABRICATION verdict, all three findings non-blocking.
- **F1 closed** (copy-provenance — the `NO_ROOT_ANCHOR_FIELDS` variant had zero byte-exact HEAD hits): parent applied the worker's anchor-script fix; live SELF-RUN shows `reasonCodeCopy` reduced to exactly 3 mappings, `NO_ROOT_ANCHOR_FIELDS` falls through to the `(CODE) without an explanation` path with a unit test pinning exactly that string.
- **F2 closed** (raw server `reasonCode` interpolation): live bytes now route server-reason AND raw code through `sanitizeReasonText` — code-point filter dropping `<0x20`/`0x7f`, then `REASON_DISPLAY_LIMIT` cap + ellipsis (SELF-RUN :636-670 region).
- **Environment-defect class (recorded per operator)**: the sandbox **intermittently hides the volume bind** — channels inconsistent (permission layer vs bind visibility). Worker verbatim: "this sandbox's tool-permission layer refused those commands (including `git diff`, most `cat`/`read` calls, and root npm scripts) throughout this session, and repeated attempts returned the same refusal." Consequence adopted as discipline: **archive-only execution** — every Go-side unit after this (rs05 fix + review, b9 interrogation, fs1 repro + fix) ran in hash-pinned `git archive` trees under `/tmp/opencode/`. Post-fix parent-applied gates on disk: `rs03b-bound.log` ("Loom UI boundary check passed (201 TypeScript files)"), `rs03b-tsc.log`.
- **F3 → BACKLOG**: `BuilderWorkspace.tsx:1611` RELATED_SOURCE throw keeps reason-preference but drops reasonCode-only capability (line untouched by the wave; SELF-RUN confirmed the exact clause `support?.reason || 'Loom has not proved that this stage retains a source row anchor.'` and that no third server-reason drop exists in ExplorerBuilder).

### 4. ROW-SOURCE-05 `b72a1322` — full disposition (reviewer PASS)

- Fix commit SELF-RUN stat 4 files +1264/−7. All repair sites re-read in live bytes: shared predicate `construction_pivot_source.go:607`, preserve-loop skip `:575-577`, Pivot SET foreign-copy guard `:383-385`, Filter SET guard `filter_source.go:244-246`; the unconditional `filter_source.go:183` preserve is **gone** (call-site census SELF-RUN); durable `filter_source_pivot_clear_test.go` 1216 lines / 20 tests. Derive-lane closure by inheritance with zero edits to the frozen file.
- Final review receipt `rs05-review.md`: **PASS**; reviewer self-described independent; probes authored blind to the worker's tests; mutation set DISJOINT from the worker's m1/m2/m3; 3-of-4 reviewer mutants kill named durable tests (operator kill rule satisfied).
- **RED 11 by reviewer count**: the worker's final pre-fix capture (9 FAILs) was `-run`-filtered and skipped `TestPivotSource{Clear,Replace}SurvivesFilterPreparation`; the reviewer's unfiltered shipped run produced **11 FAILs including both** — evidence-completeness undercount only, zero source impact, both shapes repaired.
- **mut-H2 SURVIVED → BACKLOG (non-blocking)**: the candidate-`SourceProjections` scan of the predicate has no durable pin (no fixture with ≥2 foreign-owner durable projections dropping one while retaining another); sibling half IS pinned via mut-H1; reviewer recommends one extra pivot-owned projection on the helper-carried base.
- **Derive SET unguarded bulk-copy — transient-only, no-harm**: lane-boundary re-import exists but at the FULL `ProposeConstruction` gate the frozen analyzer refuses (`candidate changes step order or omits step …`) and the cascade prune removes unconsumed re-imports; no probe or durable ever produced a `PREVIEW_PENDING` proposal carrying the orphan (receipt + reviewer agree); a matching guard is recommended IF the derive lane's contract extends.
- Worker artifact reconciliation SELF-RUN sha256 prefixes on disk: red-final `d4fd768dad8e6ab7…`, lifecycle-full `8e2de0648adbc52b…`, server-construction `431c862f6fce34fb…` — receipt claims verified.

### 5. JOURNEYS — FILTER 17×2 + DERIVE 24×2 ACCEPTED at `b72a1322`; scripts COMMIT-PENDING + integrity REVIEW-PENDING

- Barrier proof, quoted verbatim from `rs0-journey-commit.md` (worker-run, `compose exec -T loom-api /workspace/loom-dev-build-stamp.sh --check`, exit 0 before and after the forced-redeploy cycle and all four journeys):
  `f0729cde61eaa1b22e25dfb4df557fae4fe88c73dd7d95126b60d7d33fb9dd38 f0729cde61eaa1b22e25dfb4df557fae4fe88c73dd7d95126b60d7d33fb9dd38 ba3e1ced1bb95424cff1aa2aa14803d8e1d77b8566239553d6e3caeb9ed070fc`
  (BARRIER_PRE_EXIT=0 / BARRIER_POST_EXIT=0; HEAD pre==post `b72a1322…`; running exe == recorded binary == tree pair at b72a1322 — the earlier `0fa580df` pre-rs05 barrier caveat is superseded, kept as history honestly). Forced redeploy used two transient wake files, both removed; post-redeploy `readyz=200 health=200 ui=200`.
- Record-writer re-parsed ALL SIX evidence `journey.json` files (SELF-RUN): `rs05-f1/f2` → `status=passed checks=17 screenshots=11 exceptions=[]`; `rs05-d1/d2` and the original `drsrc-run-17/18` → same shape with `checks=24`. Two consecutive greens per journey above the single-green floor; the 24-check ledger = 13 functional/evidence + 11 negatives D0–D10 (per `derive-journey-commit.md`, incl. D2 provable cross-snapshot 409 and D9 genuinely-signed non-numeric 422).
- No check flipped across the rs05 prepare-lane repair — no P0 finding. Known script gap recorded (not a product regression): the filter script lacks the derive script's `mkdirSync(out,{recursive:true})` post-freshness line; first `rs05-f1` attempt died pre-chrome ENOENT, the recorded green was the immediate re-run in the pre-created dir — two-line repair accepted at the file-edit boundary.
- Registry convention (receipt evidence): the raw-CDP standalone `workflows/*.mjs` script IS the registered slot (core-protocol precedent); `registry.mjs` untouched; docs/worklist pointer deferred to the parent's batch.
- STATE: both scripts **COMMIT-PENDING** (untracked on porcelain SELF-RUN) and **integrity REVIEW-PENDING** — `journeys-review.md` verdict line reads "PENDING — mutation battery in progress" (10-mutant battery enumerated, results not landed), and **`fs1-patch-review.md` does not exist yet** (SELF-RUN ls). Do not mark these units complete.

### 6. F-DS1 — CONFIRMED product bug (500-class); repair patch REVIEW-PENDING

- Class: signed derive-slot for field f + a user-projected public column of the SAME f, reached through the shipped ADD_COLUMN → capabilities → propose-picker sequence only, answers **HTTP 500 `CONSTRUCTION_RECEIPT_CONTRACT_INVALID`** with a "inspect the server log" message — 500 on legit-reachable user state = user harm per program rule; recoverable only by deleting the user column; the CALCULATE feature is silently unavailable meanwhile. Attribution: failing guard + unguarded materialization landed in `b92548c7`; shared slot-identity predicate originates `c53759fe` (same latent twin in the filter lane, RED-proven later).
- Root cause (clause-proven by `fs1-report.md`; predicate SELF-RUN this round): `lower/recipe_schema.go:205-224` DELIBERATELY suffixes BOTH same-semantic-path public columns `#<name>`; the reconcile shared predicate `internal/server/explorer_output_reconcile.go:497-512` demands exact `strings.TrimSpace(column.SemanticPath) == strings.TrimSpace(projection.FieldPath)` — **clause at :507 (SELF-RUN)** — consumed by the derive guard `:1022-1024` (RED site; instrumentation proved ONLY that clause diverges) and the filter guard `:1160-1161`; wrapped `compileExplorerReceipt → receiptContractFailure` into the generic 500 (`explorer_receipt_contract.go:199-201`).
- Repair (family-consistent named refusals): shared predicate `constructionRootFieldAlreadyProjected` in the pivot helper + `unprocessable` guards `DERIVE_SOURCE_FIELD_ALREADY_PROJECTED` / `FILTER_SOURCE_FIELD_ALREADY_PROJECTED` (filter twin CONFIRMED-red at its own boundary, not assumed); durable 5-test file at the real server-handler boundary; per-site mutants disjoint-kill (both-lanes removal reopens both 500s); apply-proof `git apply --check` + byte-identical diff -rq; gates build/vet/lifecycle/server green, sole full-server failure the environment-gated replay test (fails identically on clean b72a1322).
- Record-writer verification SELF-RUN: `fs1-fix.patch` md5 `1a6faa3e526ac37d8b528dfcfae61851` and all four delivered sha256[:16] match the receipt; patch provably NOT synced live (0 `ALREADY_PROJECTED` hits in the three lane files, durable test absent from repo, present under `/tmp/opencode/fs1-fix/`). With `fs1-patch-review.md` absent ⇒ **REVIEW-PENDING — NOT complete; commit gated on that review.**
- Operator questions, verbatim:
  1. From `fs1-report.md` §4: "the dedupe/merge reading is bind the DERIVE operand to the EXISTING user column id instead of a fresh slot (probe6 case D `leftExisting` already proves that request shape returns 200). That changes column-ownership semantics — the operand column would have to become derive-owned (echo/removal behavior of `step-4-remove-unowned`, `constructionOwnedDeriveSourceColumnID` `derive_source.go:403-421`) or the slot claim would shadow a user-owned column. Product decision required; the named-refusal fix is behavior-preserving and unblocks the journey workaround (valueInteger seeding) either way."
  2. Capability choice-exclusion of user-projected fields — authority receipt `fs1-report.md` §3: "Capability discovery (`:109-155`) likewise never excludes already-selected fields."; fix receipt enumerates: "Operator questions recorded in the authority report are NOT answered by this repair and were not touched, per instruction: capability choice-exclusion, and the dedupe/bind-to-existing-column alternative semantics (case D already 200)."

### 7. CALIBRATION — verification-bracket truth

- This repo has **NO eslint and NO `test:docs` docs gate**: SELF-RUN scan found zero repo-owned eslint configs (`.eslintrc*` / `eslint.config.*` outside node_modules), zero eslint dependency in root/ui/loom-ui/demo `package.json`, and no `test:docs` script anywhere; the scan evidence in a receipt note is the worker's own gates section (`rs03-presentation-report.md`): "`npx eslint`, root `npm run test:docs`, and the build-stamp `--check` remain **not run**: this sandbox's tool-permission layer refused those commands".
- Canonical loom-ui chain = the package's own `test` script, SELF-RUN: `check-boundaries && typecheck:tests && vitest run`. Receipted vitest totals on disk: 87 files/1145 → 88 files/**1157** (rs03 worker final gate; b9 commit body repeats 1157); `c53759fe`-era baseline 1128/1128 (`rs01-ui2-workspacetest.log`). The brief's **"1160/1160 final"** is **parent/operator-held — SELF-RUN grep found NO 1160 vitest summary on /tmp/opencode**; recorded as attributed, not self-certified.
- Stamp authority = `loom-dev-build-stamp.sh --check` **in-container** (script present, executable, repo root — SELF-RUN); the scoped Docker bridge refuses ad-hoc exec shapes (403 observed this window), so the only attested form is the sanctioned `compose … exec -T loom-api /workspace/loom-dev-build-stamp.sh --check`, whose verbatim output is the §5 barrier line.

### 8. BACKLOG additions (NEW this round) + standing

- NEW (each with its proof line above): `acceptance-runner-copylocks` (vet EXIT 1 at `internal/acceptance/runner.go:109/143/201`, record-writer rerun) · `rs03-F3-related-source-reason-drop` (`BuilderWorkspace.tsx:1611`) · `rs05-mut-H2-predicate-projection-half-unpinned` (surviving mutant, non-blocking) · `derive-D10-requires-derive-dead-code` (`derive_source.go:236` unreachable via the `applyDeriveSources` early-return; journey D10 proves the 200-undecorated path) · `row-source-population-flags-catalog-global` (`isPopulated` true catalog-wide ⇒ per-fixture unpopulated refusal unobservable; D9 live non-numeric refusal stands in).
- STANDING (re-affirmed, unchanged): `flaky-timing-tests` pair (10ms `aggregateFallbackDelay` vs 158 enqueue goroutines; 30s replay-timeout `-skip` convention) · `m-po-probe.cfg` obsolete-config cleanup (r56 uncovered-list ticket).

### 9. Ledger integrity + program state

- **Pin audit vs live bytes (SELF-RUN): 68 MATCH / 6 DRIFT, every drift root-caused and benign** — re-pinned this round: `pointer_name_encoding_test.go 76337dba→db3807f2` (sole touch `8c21eb64`; the r53 "re-pin at commit" was never executed), `openapi_recipe_execution.go ad560732→c84a2733` (twin-chain change in `caba32df`, r56/57 read the new lines but left the pin), `recipe_execution_status_twin_e2e_test.go 64abd66a→67bfaf54` (stale since wave-B `eb9c9df0`), `all_generations_cache_key_test.go e57dfdd0→c70b83bb` (hardening `989f7e5a` +135 polish; hardening receipt's own bundle hash agrees); FINDINGS/OPERATOR_REVIEW_DIGEST self-pins normalized to the trunc-before-tail md5 convention (per-round authority remains the integrity tail). Trio holds: RBT `3b0cd15e…`, PO `9a9ff841…`, RRI `56558ef9…` (post-hardening); `bundle_store.go a5db5f55…69254` + `bundle_registry.go c0da93a2…ffd7b` == SKILL source-identity pins with ZERO publication-path commits since `218edabb` (SELF-RUN `git log 218edabb..HEAD -- internal/dataframe/publication/` empty).
- **Canonical-form drift found and repaired**: the hardening commit's surgical JSON hunk left `reviewed-sources.json` failing the r56-verified canonical roundtrip (`json.dumps(indent=2, ensure_ascii=False)+"\n"` byte-identical = FALSE on arrival); the round-58 update restores canonical form — content preserved, disclosed here and in the tail.
- Round-57 integrity tails verified intact BEFORE this append: truncation hashes for both md files MATCH the recorded values (SELF-RUN).
- `reviewed-sources.json` round 57→**58**: +`status_updates.round58`; +5 `sideFindings` entries; 4 re-pins + self-pin convention notes; `commitGate` extended with the round-58 pending set (journeys COMMIT-PENDING + fs1 patch REVIEW-PENDING + multi-model panel).
- Unit-table consistency: this round's unit states agree, row-for-row, with the operator worklist rows 94–98 committed at `1d0fe6f4` (SELF-RUN read): 01 delivered-committed (incl. journey re-accept 17×2), 02 in-progress under gate remediation with the two open operator decisions, 03 in-progress presentation-committed (server-side reason-content waves remain), 04 proposal-awaiting-operator, 05 delivered-committed. No claim in this ledger contradicts the worklist, and no worklist state is missing here.
- In flight — record-not-claim, NOTHING pending presented as landed: journeys integrity review · `fs1-patch-review` (unlanded) · HARDENING verdict (parent-held) · slice-1 independent multi-model panel · the two F-DS1 operator decisions · standing operator decisions (F-B17 winner/demote, F-B7 per-binding degrade, F-B3 family, F-B33-class response drift, F-B28/29 caveats, projectid percent-decode hardening; releaseID-fold hardening ticket is RESOLVED at `989f7e5a`).
- Porcelain remainder = operator doc edits (`AGENTS.md`, CONT doc), operator-side builder-interactions ledger pin, the two untracked journey scripts (committed per the parent's batch), fs1 patch parked in /tmp awaiting review, tooling/runtime junk. Zero uncommitted product code otherwise. **No unexplained remainder.** Sole-writer respected: no repo file outside `verification/publication-bundle/` touched by this session.

Record integrity tail (Round 58, written last): record-writer's three files at write time — FINDINGS.md sha256 `fe1c8f96158a0860e986932f072de58a6dbf0157307116de626fbadbb44baae7` **excluding this tail paragraph** (verify: truncate this file at `chr(10)+'Record integrity tail (Round 58, written last):'` and hash those exact bytes), OPERATOR_REVIEW_DIGEST.md sha256 `dbd2dec8e0db651b78a324ae88be2384b34c4d685de6b6da8040e4eb4e3a9684` **excluding that file's Round-58 tail paragraph** (same truncation recipe over the digest bytes), reviewed-sources.json sha256 `19bd8b61694fc0bf5516f1c0c15015034e286b1121e39d5ae7b75034089e9d6f` (full file, round=58, canonical `json.dumps(indent=2, ensure_ascii=False)+"\n"` roundtrip BYTE-IDENTICAL — SELF-RUN; DISCLOSED: this file's canonical form had been broken by the hardening commit's surgical hunk and was restored this round). My-run evidence this round: /tmp/opencode/record58.md (14-row command table with exits) — framing-suite rerun 4/4 PASS counts-byte-equal (1881349/3796416/300000-248624, ok 33.625s), r58-vet-acceptance.log (EXIT 1, the 3 copylocks), gofmt two-way (flagged b92548c7 / clean b72a1322), 6/6 journey.json parses (17/24 checks, 0 exceptions), fs1-verify (patch md5 1a6faa3e526ac37d8b528dfcfae61851 + 4 delivered sha256 + live non-sync grep 0), rs05 log sha256 triple, complete live-pin audit (68 match / 6 root-caused drift / 4 re-pins), eslint/test:docs scans, publication-path zero-commit check, r57 truncation hashes re-verified intact. Sole-writer respected: exactly the three verification/publication-bundle record files touched; nothing staged, committed, pushed or amended; HEAD `1d0fe6f4`. Review-PENDING, honestly labeled: journeys integrity battery, fs1-patch-review, slice-1 multi-model panel; HARDENING verdict parent-held. No pending verdict presented as landed; one product bug CONFIRMED (F-DS1), no other new product bugs this round.
## Round 59 — F-DS1 `62f627fb` CLOSED (v1 REQUEST-CHANGES → v2 APPROVE NO-BYPASS; PATCH-HYGIENE INCIDENT recorded: worker delivery patch carried an out-of-scope record-file hunk, parent excised pre-commit); JOURNEYS `77b16fea` COMMITTED (registry + TSV wiring, SELF-RUN registry revalidation 21/21 + zero drift, bracket-runner PENDING markers disclosed; live-422 claim attributed to commit body); ROW-SOURCE-02 UNPIVOT slice-2 IN-TREE REVIEW-PENDING (server interrogation in flight; no-commits governance restated in writing); pin audit 3 root-caused drifts re-pinned

Attribution: the record-writer's SELF-RUN inventory with exits is the round's receipt `/tmp/opencode/record58.md` (rev 2, Round-59 rows) — every claim below is either one of those reruns or a verbatim quote from a named worker artifact (`fs1-patch-review.md`, `fs1-fix-report.md`, `rs0-journey-commit.md`, `derive-journey-commit.md`, `journeys-review.md`, `rs02-slice2-report.md`) or a quoted commit body (`62f627fb`, `77b16fea`). HEAD `77b16fea` at record time (SELF-RUN); the record-writer touched only its three publication-bundle ledger files plus /tmp; nothing staged or committed.

### 1. F-DS1 `62f627fb` — CLOSED (product bug fix committed)

- Commit SELF-RUN stat: 4 files +621/−0 — `construction_pivot_source.go` +66, `derive_source.go` +7, `filter_source.go` +7, NEW durable `fs1_signed_slot_collision_test.go` 541. Blob md5s at `62f627fb` (SELF-RUN `git cat-file`) equal the v2 review's per-file claims: pivot `12847c0e8b38…`, derive `4d4945d40a3d…`, filter `db918013f23b…`, durable test `85fcf83bdff1…`. Live tree (git-clean for derive/filter) confirms: `md5sum` derive `4d4945d40a3d9a5d9d6c14f71d5aaace`, filter `db918013f23b39cf4633fc4034df4df9` — live == committed blobs.
- Review lineage (receipt `fs1-patch-review.md`, SELF-RUN md5 `62aa55b1a4ee77e6348460408a7c31f6`; target-claim re-read 9/9 hits): v1 (418-insertion patch, md5 `1a6faa3e…`) → **REQUEST-CHANGES**, both findings reviewer-proven — F-1 over-rejection of legit `root.`-prefixed `UPDATE_COLUMN_SOURCE` columns (reviewer demonstrated the regression), F-2 missed applied-slot collision (reviewer demonstrated the residue). v2 → **APPROVE, NO-BYPASS**, target 9/9 hits. `/tmp/opencode/fs1-fix.patch` on disk today = v2 exactly: md5 `f720eda9b678ac55a04ecc3e548fc7da`, 34324 B (SELF-RUN).
- Design as committed (commit body, quoted): merged carrier scan covers candidate-loaded + base-persisted + request-resolved columns, **Derive/Filter-owned only**; pivot-owner inputs excluded because v1's inclusion "breaks two ROW-SOURCE-05 contracts" (reviewer-proven); reverse-order pivot collision surfaces as `FILTER_SOURCE_CANNOT_CROSS_STAGE` (residual disclosed, not silently fixed). Guard identity mirrors the reconcile predicate verbatim — `TrimSpace(SemanticPath)==FieldPath` at `explorer_output_reconcile.go:507` (r58 SELF-RUN read stands).
- Operator questions **remain open** (recorded verbatim r58, unchanged): dedupe-bind vs refuse semantics for user-projected collisions; capability choice-exclusion of user-projected fields from derive pickers.
- **Deferred-operator-question (added Round 62):** both questions are explicitly DEFERRED to the operator with the safe default recorded — shipped fail-closed behavior stands unchanged (named-422 refusal at the shared predicate; advertise-then-refuse capability discovery) and no program slice proceeds on either answer. The canonical choice-framing lives at `docs/ROW_OPERATION_SOURCE_INPUT_BACKLOG.md` § Pending operator decisions (Round 62), which points at the parent-owned untracked draft `/tmp/opencode/operator-decision-digest-draft.md` (sections (a)/(b)/(d)); the draft is left untracked per its own header and this writer neither edited nor staged it.

### 2. PATCH-HYGIENE INCIDENT (process finding; no product impact)

- The F-DS1 worker's delivered patch contained, beyond its 4 contract files, an out-of-scope hunk advancing `verification/builder-interactions/reviewed-sources.json` baselineCommit with stale review text — i.e. a worker patch touching **the record-writer's owned record files**. Commit `62f627fb` body (quoted verbatim): "out-of-scope builder-interactions reviewed-sources hunk (baselineCommit advance … with stale review text) — excised pre-commit by parent; record files remain…" and names the delivered identity `f720eda9b678ac55a04ecc3e548fc7da (minus excised hunk)`.
- SELF-RUN corroboration: `grep '^+++' /tmp/opencode/fs1-fix.patch` lists exactly the 4 lane files — the excised hunk exists nowhere on disk; **the commit body is the sole disclosure**. `verification/builder-interactions/reviewed-sources.json` remains operator-side ` M` (untouched by this writer).
- New sideFinding + recommendation: worker patches must never include `verification/*` record files; parent applies a pre-apply gate listing the full patch file set against the contract's lane-file manifest. Codification (lint/script over worker dispatch) proposed as a backlog candidate — not yet built.

### 3. JOURNEYS `77b16fea` — COMMITTED (supersedes r58 COMMIT-PENDING + "registry.mjs untouched" claims — disclosed convention flip)

- Commit SELF-RUN stat: 4 files +1830/−0 — `scripts/verify-ui/workflows/verify-explorer-derive-row-source.mjs` 995, `verify-explorer-filter-row-source.mjs` 712, `scripts/verify-ui/registry.mjs` +121 (two entries `explorer-filter-row-source` :4341 / `explorer-derive-row-source` :4398), `docs/BUILDER_VERIFICATION.tsv` +2. Intentional deviation (quoted `rs0-journey-commit.md:123`): "no `playwrightGrep`/`focusedChecks` — these journeys have no Playwright spec or focused-check test file to cite, and inventing references would be a false wiring; bracket-runner integration carries PENDING markers in coverage.reason instead."
- Registry revalidation SELF-RUN this round: `node --check` 3/3 OK (both scripts + registry.mjs); `coverageDrift()` = [] ; `lifecycleAcceptanceDrift()` = [] ; the three registry-exercising focused test files (`focused-check-groups`, `lifecycle-coverage-cli`, `append-reload-timing`) → **21 pass / 0 fail** (4.47 s). `unmappedLifecycleCoverage()` count=1 proven **pre-existing**: importing the pre-commit `1d0fe6f4` registry copy yields the identical count=1 — the registry additions introduced zero drift.
- Bracket-runner honesty SELF-RUN: `ls scripts/run-native-verification-bracket.mjs` → No such file; registry PENDING marker quoted verbatim: "Honest pending markers: bracket-runner invocation via make verify-case was not executed because scripts/run-native-verification-bracket.mjs is absent from this shared volume (the verified path is the direct standalone command); registry registration is metadata and does not alter that invocation; commits await the parent batch."
- Acceptance re-pin (SELF-RUN log parses): `rsreg-f3` JOURNEY_OK checks=17 **build=62f627fb** runningBinary=fa9b92fd…; `rsreg-d4` JOURNEY_OK checks=24 build=62f627fb runningBinary=bc03a759… — binaries differ from the pre-fix `ba3e1ced…`, i.e. the served stack is post-F-DS1. Prior post-repair green: `rsfix-f1/f2` 17× and `rsfix-d1/d2` 24× at HEAD `1d0fe6f4`/binary `ba3e1ced`. **Honest negatives:** `rsreg-d3` one derive attempt FAILED with checks=0 and `rsreg-f4` left no journey.json — both recorded as failed attempts, not presented as passes; the d4/f3 siblings are the recorded acceptances at the 62f627fb-era server.
- Integrity-review closure carried from r58: all 5 REQUEST-CHANGES findings (F1 EDIT-persistence never asserted / F2 barrier ungated / F3 screenshot-floor unenforced — each proven via removal mutations exiting 1 post-repair / F4 receipt line-drift / F5 N1 label) fixed in-script pre-commit (`journeys-review.md`, 67 lines, VERDICT REQUEST-CHANGES; derive script clean across all mutations).
- **"Live 422" claim attribution discipline:** commit body asserts "Accepted at 62f627fb-era serving (re-pinned; F-DS1 refusals now live 422)" (quoted verbatim). SELF-RUN grep for `FIELD_ALREADY_PROJECTED` over the journey evidence dirs (`rsfix-*`, `rsreg-*`, `jrev`) found **no** live-stack refusal capture; the on-disk 422 evidence sits at the handler boundary in the commit's own durable go-test (`fs1-v2-rv-probes-fix.log`). The live-stack-422 element is therefore recorded as a **commit-body claim, not SELF-RUN** — same discipline applied to 1160/1160 in r58.

### 4. ROW-SOURCE-02 slice-2 (UNPIVOT) — IN-TREE, UNCOMMITTED, **REVIEW-PENDING — NOT complete**

- Selection (receipt `rs02-slice2-report.md`): UNPIVOT melt-source lane chosen after a matrix over GROUP/PIVOT/FILTER/DERIVE lanes + new-channel analysis; baseline claim "HEAD 62f627fb" (the F-DS1 fix), refusal-not-dedupe reuses the shipped F-DS1 decision.
- In-tree now (git porcelain SELF-RUN): M `generated/loomapi/api.gen.go`, `internal/explorer/authoringv2/construction_change.go`, `construction_validation.go`, `internal/explorer/lifecycle/construction_pivot_source.go`, `construction_proposal.go`, `internal/server/explorer_output_reconcile.go`, `openapi_explorer_construction_proposal.go`, `openapi/openapi.yaml`, UI `ConstructionReshapeEditor.tsx` (+unit test), `useConstructionLifecycle.ts`, `ui/.../types.ts`; NEW `unpivot_source.go(+_test.go 15 tests)`, `explorer_output_reconcile_unpivot_source_test.go`, + server HTTP twin. Lane-file list in receipt matches porcelain 1:1.
- Receipt-recorded gates (worker-run, attributed): go build/vet clean; go test green (skip `TestCoreAuthoringProtocolModelReplay` disclosed); `make openapi-check` byte-determinism cmp green; loom-ui boundaries 201 files, tsc ×2 clean; **vitest exact 1164 = prior 1160 (+4)** — the 1160 baseline now has a receipt-sourced on-disk origin ("vitest 88 files/1160 tests green", same receipt's server-lane gate), still worker-run, not SELF-RUN.
- CONTRACT-TWIN REPLAY SELF-RUN: `/tmp/opencode/unpivot/ui-propose-body.json` parses, keys exactly `candidateConstruction, changedStepId, expectedDraftDigest, expectedDraftVersion, outputId, snapshotToken, unpivotSources` — receipt asserts this key-set equals the body the server-owned fiber twin posts through the live HTTP stack. UI anti-masking fixtures (attributed): 4 unit tests incl. `constructionCapabilitiesResponseSchema.parse` on the live-shape fixture + strict proposal-request parse.
- Governance (verbatim, both places): "Governance: NO COMMITS by this worker; parent owns commits. Lane files only." and "NO COMMIT (parent owns commits)" — no-commits restated in writing for this slice author.
- **Server-side adversarial INTERROGATION IN FLIGHT** at record time (`/tmp/opencode/rs02rev/` overlay logs active 08:10–08:16; `full-test.status` empty) → unit stays **REVIEW-PENDING**; journey extension + rebuild acceptance queued for the journey worker (5 stable testids already emitted per receipt).

### 5. Pin reconciliation — hash-family-aware audit, 3 root-caused drifts re-pinned

- SELF-RUN audit over `files{}` (66 entries): 45 hash-bearing pins → **42 match / 3 drift**, plus 19 annotation-only entries (free-text anchors carrying no hash — never pins; classified separately) and the 3 record-file self-pins (pre-tail convention). Algorithms checked per token length (32→MD5, 40→git SHA-1, 64→SHA-256) per the r27 canonicalProbe lesson.
- Drift root-causes + r59 re-pins: (a) `bundle_registry.go` files-entry held a **stale pre-publication-refactor SHA-256** `1f0a6cde…`; live SHA-256 `c0da93a2af29…bfffd7b` is byte-identical to the r58-pinned anchor — the entry text, not the file, was wrong; re-pinned with both hashes. (b) `publish_release_requirements_classification_test.go` → live md5 `1a7ea43c…`, and (c) `internal/catalog/arango/resources.go` → live md5 `51efbe04…`: both legitimately evolved at `0e3b4c5b` (authz wave, last-touch per `git log`) after their pins were struck; git-clean (live == HEAD blobs `2ed0775e…`/`ce2ac5e1…` SELF-RUN `git hash-object`) — staleness by known-commit, re-pinned.
- Anchor pins reconfirmed this round: `release.go` `1118624d9f2da439e5ae06ba479ad5ee`, RRI `.tla` `56558ef9ffa913eb16b524ab43f5e5b1` (live == ledger).

### 6. Ledger deltas

- Round 58→59; status_updates.round59 added; **sideFindings 33→35**: + `patch-hygiene-record-file-ownership` (worker patch carried owner's record files; parent excised; codification proposed), + `verify-ui-journey-registry-bracket-runner-pending` (bracket script absent from volume; registration is metadata until wiring exists). `row-source-derive-D10-dead-code` (r58) **standing** unchanged. commitGate revised: r58's two pending commits (journey scripts, fs1 patch) RESOLVED at `77b16fea`/`62f627fb`; remaining gated items — slice-2 (interrogation + acceptance + parent commit), bracket-runner wiring, two F-DS1 operator questions.
- Worklist cross-check: program unit table unchanged vs `docs/BUILDER_WORKLIST.tsv` rows 94–98 (r58 SELF-RUN stands; no worklist edit this round).

Sole-writer respected: exactly the three `verification/publication-bundle` record files touched; nothing staged, committed, pushed or amended; HEAD `77b16fea`. Review-PENDING, honestly labeled: ROW-SOURCE-02 slice-2 (interrogation in flight, acceptance queued). Commit-body claims labeled where no on-disk artifact exists (live-422 refusal, vitest tallies, 1160 lineage). No pending verdict presented as landed; **no new product bug CONFIRMED this round** (F-DS1 moved CONFIRMED→CLOSED; the patch-hygiene incident is a process finding, not product).

Record integrity tail (Round 59, written last): record-writer's three files at write time — FINDINGS.md sha256 7fa391e977857d95e92611f255bc51615f8864b77e120d7eeb010650373739d5 **excluding this tail paragraph** (verify: truncate this file at `chr(10)+'Record integrity tail (Round 59, written last):'` and hash those exact bytes), OPERATOR_REVIEW_DIGEST.md sha256 ead6e0e37e08a5de73e77e0c861d7c5e794e01922bf16f8889970fc140c90b97 **excluding that file's Round-59 tail paragraph** (same truncation recipe over the digest bytes), reviewed-sources.json sha256 `a9adeadf95d686362359a97da67d01c0a99c8d8974b6fc5fb6400895f18bdc04` (full file, round=59, canonical `json.dumps(indent=2, ensure_ascii=False)+"\n"` roundtrip BYTE-IDENTICAL — SELF-RUN). My-run evidence this round: /tmp/opencode/record58.md rev 2 — commit/blob-hash reconciliation 4/4, fs1 receipts (review md 62aa55b1…, patch f720eda9… minus excised hunk, ^+++ = 4 lane files), node --check ×3, registry coverageDrift/lifecycleAcceptanceDrift [] + 21/21 focused trio + pre-existing unmapped count=1 (1d0fe6f4 copy), bracket-script absence (ls), rsreg re-pin log parses (f3 17/d4 24 at build=62f627fb; d3/f4 failed attempts disclosed), ui-propose-body 7-key twin, porcelain==lane-manifest, hash-aware pin audit 42/45 + 3 re-pins, release.go/RRI anchor reconfirmation. Sole-writer respected: exactly the three verification/publication-bundle record files touched; nothing staged, committed, pushed or amended; HEAD `77b16fea`. Review-PENDING, honestly labeled: ROW-SOURCE-02 slice-2. No new product bug CONFIRMED this round (F-DS1 CONFIRMED→CLOSED; patch-hygiene is process, not product).
## Round 60 — ROW-SOURCE-02 slice-2 CLOSED at `9c79307b` (UNPIVOT melt inputs, 23 files +3409/−568); committer-email metadata repaired via content-identical AMEND (dangling pre-amend object proves tree/author/date/message identity, SELF-RUN); server bracket REQUEST-CHANGES→v2 APPROVED (`rs02-slice2-review.md` md5 `d1a3d00e…`); UI bracket PASS WITH FINDINGS (`rs02-slice2-ui-review.md` md5 `20722418…` — F-1 fixed portably, F-4 receipt corrected, F-2/F-3 zod↔openapi family carried to `docs/EXPLORER_BACKLOG.md` in-commit); journey extension honest PENDING (queued); 45/45 hash-pins match + 4 new unpivot files pinned

Attribution: the record-writer's SELF-RUN inventory with exits is the round's receipt `/tmp/opencode/record58.md` (rev 3, Round-60 rows). HEAD `9c79307b` at record time (SELF-RUN); the record-writer touched only its three publication-bundle ledger files plus /tmp; nothing staged or committed.

### 1. Commit identity + metadata-only amend disclosure

- `git log` SELF-RUN: `9c79307b` "feat(builder): UNPIVOT consumes signed unselected-field melt inputs (ROW-SOURCE-02 slice 2)"; stat 23 files +3409/−568 including `docs/EXPLORER_BACKLOG.md` +20 (F-2/F-3 carry-over rows landed in-commit).
- Amend proof (SELF-RUN, `git reflog`): `HEAD@{1} 0a785b1d` (committer `matthewpeterkotrt@…` — transposed typo) at 08:38:25 → `HEAD@{0} "commit (amend)" 9c79307b` (committer corrected to `matthewpeterkort@…`) at **08:38:32 — 7 seconds later**. Because the pre-amend object is still dangling, identity was compared directly: `git cat-file -p 0a785b1d^{tree}` vs `9c79307b^{tree}` → **TREES-IDENTICAL**; author name/email/author-date/subject/body diff → **AUTHOR-DATE-MSG-IDENTICAL**. Only committer-email/committer-date changed, i.e. a same-content identity fix, disclosed as required; no history rewrite beyond the un-pushed amend of a commit created seconds earlier.
- Post-commit cleanliness SELF-RUN: `git status --porcelain` shows no slice-2 residue — the 15 entries in-tree at r59 are all committed (remaining M: operator-side AGENTS/CONT doc, this writer's 3 ledger files; ?? disposable tooling).

### 2. Server adversarial bracket — CLOSED (REQUEST-CHANGES → v2 APPROVED)

- Receipt `/tmp/opencode/rs02-slice2-review.md`, SELF-RUN md5 `d1a3d00e2a47bf2a62a7162c8874fa91`. v1 **REQUEST-CHANGES** with substantive F-1 ("Melt admission whitelist under-admits compiler-legal melt value columns; misstates compiler parity", :136, reviewer-reproduced). v2 re-verification appended (DELTA): "F-1 and F-2 CLOSED; no new findings at this bracket. APPROVED for parent integration." (:266).
- Discipline quoted verbatim (v2 header): "Bracket: HEAD `77b16fea` (+ worker uncommitted lower/authoringv2/lifecycle/server/openapi edits; UI & verification/* out of scope)… Same-model disclosed: reviewer == author model `stravinsky/qwen3.8-flash-next`. All verdicts below are reviewer-run (go1.26.5, isolated GOCACHE under /tmp/opencode/rs02rev), never accepted on worker self-report. Overlay-only (`go test -overlay`), zero checkout writes; `git status` shows no reviewer artifacts." The lower/ files in the final commit stat are the F-1 compiler-parity repair surface (bracket-consistent).
- Gates of the integrated commit (commit body, quoted): "Gates: lifecycle/authoringv2/lower/server full, build/vet/gofmt, openapi-check, loom-ui chain 1164, contract twin."

### 3. UI adversarial bracket — CLOSED (PASS WITH FINDINGS) and finding disposition

- Receipt `/tmp/opencode/rs02-slice2-ui-review.md`, SELF-RUN md5 `207224189aa61fe295e691d403a029b3`; VERDICT line: "PASS WITH FINDINGS (no contract violations)". Six-field openapi alignment certified (:36) apart from the inherited leniency family.
- **F-1 (proven, LOW) fixed portably — SELF-RUN:** reviewer reproduced ENOENT in isolation (capture write hard-depended on `/tmp/opencode/unpivot/` pre-existing, and silently overwrote the server-twin artifact); committed unit test now reads `mkdirSync(dirname(capturePath), { recursive: true });` at `ConstructionReshapeEditor.unit.test.tsx:4353` immediately before the `writeFileSync` — assertions still precede the capture write.
- **F-4 (doc drift) corrected:** receipt UI section now states the kind note is a static "hint and not conditioned on server/gate state — corrected per UI review F-4 word-drift" (`rs02-slice2-report.md:93`, SELF-RUN grep), matching shipped behavior (renders when `inputs.length > 0`).
- **F-2/F-3 (zod↔openapi leniency family) → `docs/EXPLORER_BACKLOG.md`, committed in `9c79307b`, row cited:** section "ROW-SOURCE-02 slice-2 UI review carry-over (2026-11-02)" opens "Zod↔openapi leniency on the construction capability choice shape is a family convention, not a slice-2 defect; deliberately NOT changed here (filter and derive lanes share it; a lane-consistent change should tighten all three together)"; the F-2 entry records the proven lenience (`columnId` optional in zod vs required in `openapi.yaml:4540-4549`, consumers guarding truthy, "Source: `/tmp/opencode/rs02-slice2-ui-review.md` finding F-2") and the tightening plan; F-3 (`label` min(1) stricter than openapi) recorded as the same-family record-keeping item. This writer did not create the row — verified content against receipts, quoted from committed bytes.

### 4. Journey extension — honest PENDING

Commit body: "Journey extension queued." — the melt-input journey legs riding the 5 shipped testids (r59 §4) are NOT yet accepted; recorded PENDING, no pass presented as landed. Parent's next slice (journey worker), acceptance gates to follow.

### 5. Pin reconciliation

- Pre-write hash-aware audit (32/40/64 families): **45/45 hash-bearing pins match, 0 unexplained drift** (19 annotation-only + 2 record self pre-tail classified as convention; r59's re-pins holding).
- **New pins (4, slice-1 convention — live == HEAD blobs SELF-RUN `git hash-object` identity per file):** `internal/explorer/lifecycle/unpivot_source.go` md5 `6b4390822c334e91f74ae40ec697a0c5`; `internal/explorer/lifecycle/unpivot_source_test.go` md5 `ada1537575d003986aa66edefc003953`; `internal/server/explorer_output_reconcile_unpivot_source_test.go` md5 `bd6512635a1a416fe2c4f75e9ac4f8a1`; `internal/server/openapi_explorer_construction_unpivot_source_test.go` md5 `edb2ebd4b8d1b50b447462e38b21c3eb`. Pin total 66→70; none of the 23 committed files was previously hash-pinned, hence the 0-drift result was expected and re-verified after adding the new pins (49/49 — 45 pre-existing hash-pins + 4 new, all live==HEAD-blob verified).
- Anchors reconfirmed live: `release.go` `1118624d…`, RRI `.tla` `56558ef9…`; reviewer GOCACHE/receipt artifacts under `/tmp/opencode/rs02rev*` intact (in-flight marker superseded by CLOSE at :266).

### 6. Ledger deltas

- Round 59→60; status_updates.round60; **sideFindings 35→37**: + `row-source-02-journey-extension-pending` (queued per commit body; gates = journey worker + acceptance + no pass claimed), + `slice2-commit-metadata-amend-disclosure` (process: committer-email typo fixed by content-identical amend 7s post-creation; proven via dangling 0a785b1d — tree/author/date/message byte-identical; no unpushed-history concern). commitGate: slice-2 RESOLVED (both brackets CLOSED per falsifiable-claim rules — receipts on disk, md5s printed, verdict lines quoted, fixes verified in committed bytes); remaining gated — journey-extension acceptance, bracket-runner wiring (script still absent), 2 F-DS1 operator questions (unchanged).
- Worklist cross-check: program unit table unchanged vs rows 94–98; slice-2 closure aligns with row 96-era scope (no worklist edit this round).

Sole-writer respected: exactly the three `verification/publication-bundle` record files touched; nothing staged, committed, pushed or amended by this writer; HEAD `9c79307b`. **No new product bug CONFIRMED this round** (the two bracket-finding families were review-surface: capture portability, doc drift, inherited zod leniency — none a product contract violation). Nothing review-PENDING presented as landed except the journey extension, which is labeled PENDING.

Record integrity tail (Round 60, written last): record-writer's three files at write time — FINDINGS.md sha256 e0987916e4f698fe058d9143041755804296573ef0436a029dc1a8eb12a14aea **excluding this tail paragraph** (verify: truncate this file at `chr(10)+'Record integrity tail (Round 60, written last):'` and hash those exact bytes), OPERATOR_REVIEW_DIGEST.md sha256 fe99cf35fd3e7fd8e97476e031994ae984adb9bfca1030cd4f3ea74713f68d6d **excluding that file's Round-60 tail paragraph** (same truncation recipe over the digest bytes), reviewed-sources.json sha256 `74465ccc837471ae550626d74a694774391b79d9b791ebcb21678aa4f84d4110` (full file, round=60, canonical `json.dumps(indent=2, ensure_ascii=False)+"\n"` roundtrip BYTE-IDENTICAL — SELF-RUN). My-run evidence this round: /tmp/opencode/record58.md rev 3 — reflog/dangling-object amend proof (0a785b1d trees/author/date/message identical, 7s), reviewer receipt md5s (d1a3d00e…, 20722418…) + verdict greps, committed mkdirSync fix :4353, receipt :93 F-4 correction, backlog carry-over row from committed bytes, 4 new pins live==HEAD, hash-aware audit 45/45 then 49/49, anchors release.go 1118624d…/RRI 56558ef9… reconfirmed. Sole-writer respected: exactly the three verification/publication-bundle record files touched; nothing staged, committed, pushed or amended by this writer; HEAD `9c79307b`. Review-PENDING, honestly labeled: journey extension. No new product bug CONFIRMED this round.
## Round 61 (micro) — formals pass (maintain-formal-verification-skill): SIX checks IN SYNC with violating-by-design counterpart controls intact; ConstructionChain/CAP canonical generated + CAP Arango lanes BLOCKED-LOCALLY (pin gate); zero model edits; U1/U2 prepare-lane check-debt standing; 7 builder-interactions ledger re-pins applied (all re-derived from live bytes — 7/7 match, all root-caused); BI sweep 29/29

Attribution: the formals pass itself is receipt-attributed to `/tmp/opencode/formals-r61.md` (maintain-formal-verification-skill report, window 0e3b4c5b..1d3d917b; runs serial-parent disclosed — "No subagent tool is exposed in this sandbox"). Re-pin re-derivation, sweeps and hashes this round are record-writer SELF-RUN (receipt `/tmp/opencode/record58.md` rev 4). HEAD `1d3d917b` (round-60 ledger commit; its stat SELF-RUN: exactly this writer's three files) at record time; the record-writer touched only its three publication-bundle files plus `verification/builder-interactions/reviewed-sources.json` (operator-extended micro-round scope) and /tmp; nothing staged or committed.

### 1. Formals pass — six checks in sync, controls as-is

- **rri `ReleaseRevisionIdempotence`** IN SYNC: `m-rri-fixed` exit 0 CLEAN vs `m-rri-shipped` exit 12 `Idempotence is violated` — violating-by-design churn control intact (pair falsifiability). 989f7e5a byte-framing classified effect-none against the model ("no byte-level fold is modeled" verified independently, quoted); `.tla` exec stream byte-identical 989f7e5a^..HEAD.
- **rbt `ReleaseBindingTrust`** IN SYNC: four arms fresh (holds 0 / caseholds 0 / violation 12 / case 12 `CorrectBinding is violated`); all four covered Go files byte-unchanged 218edabb..HEAD (empty `git diff --stat`).
- **po `PointerOrphanLifecycle`** IN SYNC: `m-po-fixed` 0, `m-po-bug` 13 (OrphanGC temporal — the named F-B19 witness), `m-po-janitor-blind` 12 `NoBoundDeleted is violated`.
- **BundleCommitLifecycle (primary)** IN SYNC: model/config byte-identical 218edabb..HEAD; source pins re-verified exact — `bundle_store.go a5db5f558842…9254` / `bundle_registry.go c0da93a2af29…ffd7b` == SKILL "Last verified" (double-confirms this ledger's r59/r60 anchor reconciliations); all-faults-FALSE run exit 12 `InvariantNoDropUnderPointer is violated` — "a PASS here would have been the suspicious result (SKILL rule)".
- **ConstructionChain** IN SYNC on its described region (bound noted): drift read classifies slice-2's validator changes as outside the modeled region (replay populates `Construction{Version, Steps}` only — `SourceProjections` never set, owner switch unreachable); model-only TLC exit 0.
- **CoreAuthoringProtocol (CAP)** model IN SYNC: action boundary set unchanged (F-DS1 refusals fire upstream in lifecycle prepare; slice-2 UI delta is proposal serialization outside the admitted boundary; client.go += compile-time `TransactionalRowClient` only); model-only TLC exit 0 (depth 9). Companion journey lane `operation-history-routine` PASS 11/11, 0 skipped, 5 Apply ACKs, 11 Service.Previews — labeled by the receipt itself as "not a model alignment proof".
- Discipline quotes: every rerun budgeted (`TLC_WORKERS=2/HEAP 1024`, primary 2048, separate `-metadir`, `timeout` deadlines — "none hit — no exit 124"); tools.lock + sha256 verified; "Zero model edits landed this pass. No commits." → **no `.tla` edits**; break-to-prove-fail "(b) is vacuous" because no described region drifted; "Zero product bugs established this pass. Zero model drift in any described region … The only drift found is HARNESS/LEDGER drift".
- **Canonical lanes BLOCKED-LOCALLY** (fail-closed pin gate at `scripts/verify-builder-formal-core.mjs:84`): `node scripts/verify-builder-formal-core.mjs --model construction-chain --phase generated` and `--model core-protocol --phase generated` both exit 1 `Review required: useConstructionLifecycle.ts`; CAP Arango lanes (`TestCoreAuthoringProtocolAgainstArango` + dual-store S1–S8) blocked with the quoted prerequisite: `docker exec loom-dev-qwen-current-loom-api-1 sh -c 'cd /workspace && LOOM_TEST_ARANGO_URL=http://arangodb:8529 node scripts/verify-builder-formal-core.mjs --model core-protocol --phase generated --out <fresh>'` (driver owns only `loom_core_authoring_replay_<uuid>` databases). This round's re-pins satisfy the stated precondition; the canonical runs remain parent/reviewer-scheduled — no pass claimed.
- **Check-debt (U = debt), standing**: **U1** — `validatePivotSourceProjection` owner branches (construction_validation.go:348-376, UNPIVOT melt-membership + nil-payload guards) + prune owner registry (construction_change.go:528-530) unreachable to model/replay; closure needs replay case families + Node contract + re-pin (product/harness edits, outside *.tla/*.cfg). **U2** — the four prepare lanes' rs05 `preserveConstructionOwnedInputs` gating and F-DS1's named-422 predicate are described by NO model; any new prepare-lane check "must ship with the operator's breaks (unconditional-restore overlay revert; refused-accepts mutant) as its falsification pair."
- Sync policy honored by this writer: chain/CAP sync commits **not advanced** (canonical lanes unrun); rri/rbt/po/primary advancement to 1d3d917b is receipt-supported but the ledger-level `baselineCommit` is held (shared with chain/CAP) — policy explicitly remains writer-call, recorded unchanged.

### 2. The 7 BI re-pins — re-derived, matched, root-caused (audit convention)

- SELF-RUN: `sha256sum` of live bytes at HEAD `1d3d917b` == all seven quoted values (7/7), and live == `git rev-parse HEAD:<path>` blobs (clean paths — no in-tree confound). Applied to `verification/builder-interactions/reviewed-sources.json` files{}; file remains canonical (`json.dumps(indent=2,ensure_ascii=False)+"\n"` roundtrip TRUE, SELF-RUN), new file sha256 `12de84f69315c26c…`.
- Root causes of the seven stale pins (old value == historical blob, SELF-RUN `git show <c>:<path>` hash comparisons): `BuilderWorkspace.tsx` == pre-cdaa2445 blob; `client.go` == pre-8160bb97 blob; `ConstructionReshapeEditor.tsx` == pre-9c79307b blob (all three exact); the other four were stale BEFORE the window — old pins last matched `construction_validation.go@8145c22c` (2026-10-18), `construction_change.go@792fc240` (2026-10-13), `construction_proposal.go@774a554d` (2026-10-18), `useConstructionLifecycle.ts@e736e130` (2026-10-31) — i.e. the row-source/F-DS1/slice-2 waves changed these files across multiple commits whose earlier increments were never re-pinned. Consistent with the receipt's classification: "HARNESS/LEDGER drift … a tooling precondition, not model drift and not a product defect."
- Post-write sweeps SELF-RUN: **BI ledger 29/29 match / 0 drift**; publication-bundle full sweep **49/49 hash-pins / 0 drift** (19 annotation-only classified; self-pins pre-tail per convention; product files untouched by 1d3d917b — stat verified).

### 3. Ledger deltas

- Round 60→61; sideFindings 37→39: + `formals-U1-pivot-source-owner-branches-uncovered`, + `formals-U2-prepare-lanes-unmodeled` (both standing, verbatim closure conditions above). commitGate unchanged plus new gated item: canonical chain/CAP generated + Arango lane re-runs (precondition now met). No new product bugs; nothing presented as landed that is pending (canonical lanes explicitly BLOCKED-LOCALLY).

Sole-writer respected: exactly the three `verification/publication-bundle` record files + operator-assigned `verification/builder-interactions/reviewed-sources.json` touched; nothing staged, committed, pushed or amended; HEAD `1d3d917b`.

Record integrity tail (Round 61, written last): record-writer's ledger files at write time — FINDINGS.md sha256 4394aeb20f07a22e595d39f142d979a3a329abffc597beec31419a1b94d95892 **excluding this tail paragraph** (verify: truncate this file at `chr(10)+'Record integrity tail (Round 61, written last):'` and hash those exact bytes), OPERATOR_REVIEW_DIGEST.md sha256 4cd327fb2761f07b3849c0f4718eaddd218f3e518b26d6481a0e83ab267f7e48 **excluding that file's Round-61 tail paragraph** (same truncation recipe), reviewed-sources.json sha256 `c379b4774d4121694f336597212ce8b133ad9b8cb88ec0ec11ccb5ebceb9bf17` (full file, round=61, canonical `json.dumps(indent=2, ensure_ascii=False)+"\n"` roundtrip BYTE-IDENTICAL — SELF-RUN), and operator-assigned verification/builder-interactions/reviewed-sources.json sha256 `12de84f69315c26ca0f105fd2160a621bad620240bbbbd3301c0f91db4b5dfaa` (canonical roundtrip BYTE-IDENTICAL — SELF-RUN; seven 1d3d917b live-verified sha256 pins applied). My-run evidence: /tmp/opencode/record58.md rev 4 — 7/7 re-derivations + HEAD-blob equality, 7/7 root-cause blob comparisons (git show pre-commit/pre-pin commits), BI 29/29 + publication 49/49 sweeps, 1d3d917b stat check (three record files only). Sole-writer respected exactly per micro-round scope; nothing staged, committed, pushed or amended; HEAD `1d3d917b`. Canonical lanes explicitly BLOCKED-LOCALLY (no pass claimed); no new product bug this round.

## Round 62 — reviewRecords 49→54 (five live-byte pins: acceptance copylocks unit `26bb172c`, CI replay separation `fdf2969e`, ConstructionPrepareLanes model `feb663a7`); two disclosed unpivot-twin re-pins (post-r61 drift from `0dd7be44`); F-DS1 deferred-operator-question line; seven Backlog-seed rows; BI re-pin verified read-only (`c838a6f3`); UI-contract inventory verified in-sync via tool primitives only

Attribution: HEAD `feb663a7` (SELF-RUN `git log --oneline -5` chain: `feb663a7` · `c838a6f3` · `fdf2969e` · `26bb172c` · `0dd7be44` · `ddb36d07` · `1d3d917b`). SELF-RUN this session: `sha256sum`/`git show <commit>:<path>` derivations for all five new pins + 2 re-pins + 2 convention rechecks, full hash-aware pin audit (54/54), canonical roundtrip assert, commit stats, `go`-file region reads (runner defer `:184-200`, stage labels `:172-214`, category-discovery regions, filter-journey `:161`/`:278`), inventory `--source-only`/`--check`/`--self-test` runs with captured exit codes, TSV column-integrity scans. Receipt-attributed (worker runs, quoted not re-derived): `/tmp/opencode/copylocks-report.md`, `/tmp/opencode/copylocks-review.md` (**NO-BYPASS**), `/tmp/opencode/formals-u1u2.md`, `/tmp/opencode/ci-replay-separation.md`, `/tmp/opencode/ci-ui-hashes.md`, `/tmp/opencode/operator-decision-digest-draft.md` (parent-owned untracked notes; not edited, not staged, left untracked per its own header). **No new product bug CONFIRMED this round**; nothing pending presented as landed.

### 1. reviewRecords 49→54 — five new hash-pin records, each re-derived from LIVE bytes at its cited commit (live == `git show <commit>:<path>`), exact 64-hex, canonical JSON byte-stable

| path | pin commit | sha256 (live == blob at pin, SELF-RUN) | root cause per pin |
|---|---|---|---|
| `internal/acceptance/runner.go` | `26bb172c` | `d44b250cd23ae8aca4541b6b4a7fcb8e91ed155615daee137fc371dc0f39ce0b` | Copylocks defect class ended here: `Config.Fixture Fetcher` + `Runner{cfg Config}` value-copied the `Fetcher.sync.Mutex` at pre-fix `:109`/`:143`/`:201`, so the documented populate/rename dedup guarded nothing — overlay RED measured 2 concurrent fixture fetches through one Runner and a `Run` completing while the caller held the mutex; a locked-mutex copy can wedge (reviewer wedge witness). Fix = pointer threading (`Fixture *Fetcher`) + `New` nil/empty-CacheDir guard. Bracket: `go vet ./internal/...` and `./...` GREEN, package suite 27 PASS incl. 3 new, `-race` 5× clean. |
| `internal/acceptance/runner_locking_test.go` | `26bb172c` | `d720f2c37f72e7b140c3a2ccec21a680743ae1c92db22818d676d599efe0c216` | **NEW durable** (file did not exist before the unit, so no prior pin). Three guards: caller-hold + pointer identity (the SOLE catcher of New-time reclones — reviewer M3 nuance recorded), max-1 concurrent fetch under hold, `New` refuses nil/empty-CacheDir. Compile-RED at HEAD proven: the value-typed field could not express the sharing contract (`runner_locking_test.go:57:18` verbatim). Reviewer M1/M2/M3 mutation kills exact (`copylocks-review.md` §3, NO-BYPASS). |
| `cmd/loom-acceptance/main.go` | `26bb172c` | `f8cc8021580f234f6f9350316bf5699f0a12afc240d027a237f5eedcbb79083c` | Sole external construction site of the affected API; the type fix forced exactly one `&` at `:127` (`Fixture: &acceptance.Fetcher{…}`), disclosed in the receipt §3.1; the two fetcher constructions (`:118` pre-warm vs `:127` runner) sit in mutually exclusive branches (`fixtureOnly` returns at `:123`) — the one character is the entire external delta, no logic change. |
| `internal/server/core_authoring_protocol_formal_replay_test.go` | `fdf2969e` | `17799abb830e9f65c5159f665b64d0828031bfd977242e3108eb0db5122371d0` | skip+guard separation: `TestCoreAuthoringProtocolModelReplay` previously hard-failed the ordinary `go test` job (exit 1 demanding `LOOM_FORMAL_CORE_PROTOCOL_INPUT`/`OUTPUT` outside its harness). Now it skips cleanly when the harness env is absent, while a dedicated enforced CI step keeps the replay mandatory — research truth recorded by the commit body: the generated replay had run only transitively via builder-formal `--self-test` (phase=all), no dedicated step existed. |
| `verification/publication-bundle/ConstructionPrepareLanes.tla` | `feb663a7` | `37352ef394cecc86f64ffbc2188fde59eecd99756b500c79de70e8a68d1087d9` | **NEW model in this writer's directory** for the prepare-lane clearance contract (ROW-SOURCE-05 P0 sentence, `b72a1322`): quantifies base presence × lane-propose order × per-lane {Untouched, Omitted, Cleared, Replaced} at every post-prepare durable state — the universal quantifier the 20 rs05 durables only sample. `m-prep-gated` CLEAN (61,456 distinct states) + four falsification arms VIOLATE exactly as designed (helper-ungated / setbranch-ungated / persist-unconsumed / shared-gate; plus a scratch OwnerIsolation probe), per the SKILL table; receipt `/tmp/opencode/formals-u1u2.md` (budget-first, per-arm metadir, no exit 124). **Closes the r61 U2 prepare-lane check-debt**; U1 (`validatePivotSourceProjection` owner branches `construction_validation.go:348-376` + prune owner registry `construction_change.go:528-530`) remains OPEN standing debt per the receipt's own "Uncovered after this pass". |

- **Disclosed re-pins (drift found by this round's sweep, all root-caused):** `internal/server/explorer_output_reconcile_unpivot_source_test.go` `bd6512635a1a…`→live `e43cfee809df…` and `internal/server/openapi_explorer_construction_unpivot_source_test.go` `edb2ebd4b8d1…`→live `96c2f30c849b…`. Root cause SELF-RUN: both last touched by `0dd7be44` ("accept compiler-legit melt-first signed unpivot; fix date_time editor gate (F-A, F-B)"), i.e. after the r61 sweep ran at `1d3d917b`; live == HEAD blobs (porcelain clean for both paths). Same md5 (32-hex) family as the superseded values.
- **Post-write audit SELF-RUN:** publication-bundle ledger **54 records / 54 match / 0 drift** (19 annotation-only entries classified per the r59 counting convention; the 2 self-records stay pre-tail), ledger canonical `json.dumps(indent=2, ensure_ascii=False)+"\n"` roundtrip BYTE-IDENTICAL. **3-way agreement**: ledger audit == this section == the digest's round-62 sweep row, all at **54**.
- Convention rechecks (verified, not drift): `bundle_registry.go` — entry keeps its documented superseded-placeholder annotation; live SHA-256 `c0da93a2af299eba02e77955977f61b41cf96f74ec1f644e08c73fd20bfffd7b` equals the recorded authoritative value (== r58 anchor); `scope_resolver.go` — live md5 `bd0ddc06b7b090f1ba335e95b4354388` equals the working-tree value recorded in the entry (HEAD-era token kept as history per that entry's own wording).

### 2. Builder-interactions ledger — note only, ownership honored

`verification/builder-interactions/reviewed-sources.json` was re-pinned inside the parent-side commit `c838a6f3` (ConstructionReshapeEditor.tsx to the reviewed `0dd7be44` bytes). This writer did not write that file (not owned this round; sole-writer scope = publication-bundle record files + the operator-assigned docs rows listed in §6). Read-only re-derivation SELF-RUN: live `sha256sum` `215d2189555bf039…` equals the recorded pin, so the round summary needs no pending BI action. The r61 canonical-lane precondition stays satisfied; no pass claimed for those lanes.

### 3. F-DS1 — deferred-operator-question line landed on the CLOSED entry

The Round-59 §1 F-DS1 entry now carries an explicit **Deferred-operator-question** line: the two open semantics questions (dedupe-bind vs named-422 refusal; capability choice-exclusion of user-projected fields) are deferred to the operator with the safe default recorded — shipped fail-closed behavior stands, no slice proceeds on either answer. Canonical choice-framing recorded in `docs/ROW_OPERATION_SOURCE_INPUT_BACKLOG.md` § Pending operator decisions (Round 62) with pointers to the parent-owned untracked draft (§(a)/(b)/(d) there); the draft stays untracked, was not staged, and this writer performed no `git rm` anywhere (the git index stayed empty all round — SELF-RUN `git diff --cached` at both ends of the session).

### 4. Backlog-seed rows (status = Backlog-seed; NEVER-fix candidates: none — every row carries a direction)

| # | Exact location | Severity | Blast radius | Status | One-line direction |
|---|---|---|---|---|---|
| BS-62-1 | `internal/explorer/lifecycle/construction_category_discovery.go:505-534` (operator-cited) | LOW | display-only: category-discovery labels/path mapping; no write or authz surface | Backlog-seed | wire the related-source identity through the display mapping **or** delete the dead expectation — after a coordinate re-check (see disclosure below) |
| BS-62-2 | `scripts/verify-ui/workflows/verify-explorer-filter-row-source.mjs:278` (+ `:161`) | MEDIUM (verification integrity) | filter journey's mutation battery + its registration claims: `:278` `openKeepRowsEditorAndPick` picks through a `return null` fast path (`:281-282` region) with no post-selection read-back = the rs05 **mut-H2 silent-vacuous** channel; `:161` region carries the dead **D10** helper line (identity-record tail, r58 "derive-D10 dead code" family) | Backlog-seed | assert the picked option actually bound (kill the silent-vacuous half); delete or wire the dead helper |
| BS-62-3 | flaky wall-clock timing pair — standing since r56/r58: `internal/api/graphql/graph/dataframe/aggregates.go` (`aggregateFallbackDelay` 10 ms vs 158 enqueue goroutines) + the 30 s replay-timeout `-skip` convention | LOW | CI flake surface only (no product contract) | Backlog-seed | replace wall-clock waits with deterministic barriers/digest gates (standing ticket; re-affirmed, unchanged) |
| BS-62-4 | `verification/publication-bundle/m-po-probe.cfg` | LOW (hygiene) | model-ledger clarity only: the cfg's `INVARIANT ProbeUnreachable` is a never-fire probe superseded by `m-po-{fixed,bug,janitor-blind}`; tracked since `8160bb97`, live sha256 `47cef912c3e9…` (SELF-RUN) | Backlog-seed | delete the obsolete cfg in the next maintenance wave (r56 uncovered-list ticket) — **assignment mismatch disclosed:** the file is NOT missing and SKILL.md holds no `m-po-probe` reference to dangle (SELF-RUN `rg`); the accurate live state is present-but-obsolete |
| BS-62-5 | unpivot **zod↔openapi leniency family** — file level: `ui/packages/loom-ui/src/types.ts` (`tableShapeUnpivotProposalIntentSchema` :870-873, `tableShapeUnpivotPolicyReferenceSchema` :830) vs `openapi/openapi.yaml` `ConstructionUnpivot`/`ConstructionUnpivotInput` :3907-3925; server twin `internal/server/openapi_explorer_construction_unpivot_source_test.go` | LOW | unpivot propose validation leniency (client accepts shapes the openapi gates), same inherited family as the rs02 UI-review F-2 (`columnId` optional-vs-required)/F-3 (`label` min(1)) convention — deliberately NOT a slice-2 defect | Backlog-seed | tighten the three named deltas lane-consistently with the filter/derive siblings in one committed pass — **disclosed:** the three cases are enumerated by the lane receipt set `/tmp/opencode/bundle-unpivot-openapi*.md`, which is **absent from disk at record time**; per the no-fabrication bar the individual cases are not restated from an unread receipt and will be quoted verbatim the round it lands |
| BS-62-6 | F-DS1 pivot **reverse-order residual** ordering escape — pivot-owner exclusion in the shared predicate (`internal/explorer/lifecycle/construction_pivot_source.go:658` predicate / `:687` carrier set, Derive/Filter/Unpivot only) so the pivot-first direction surfaces only via the filter boundary as `FILTER_SOURCE_CANNOT_CROSS_STAGE` (disclosed in the `62f627fb` entry, r59 §1) | LOW | error-label quality only — the collision is still refused (never a 500, never a silent accept); the user sees a cross-stage label instead of the named `*_FIELD_ALREADY_PROJECTED` 422 | Backlog-seed | carry the named diagnostic code into the reverse-order direction, or document the two-code contract explicitly — **disclosed:** the sweep receipt `/tmp/opencode/unpivot-fds1-sweep.md` is **absent from disk at record time**; the row is re-derived from the shipped durable + this writer's live source read, not from the unread file |
| BS-62-7 | `internal/acceptance/runner.go:184-200` defer + stage labels `:172-214` (copylocks worker finding, `copylocks-report.md` §7.2) | LOW | published report contract: the deferred closure overwrites `report.Status = "FAILED"` whenever `runErr != nil`, so `TARGET_UNAVAILABLE` (:177), `FIXTURE_UNAVAILABLE` (:205) and `FIXTURE_INVALID` (:211) survive only in `report.Error`; `report.json` cannot distinguish a fixture problem from a scenario problem (observed by the worker while writing the tests) | Backlog-seed | stop clobbering stage labels on error return (preserve the stage code, or add a distinct stage field) — requires a contract review because operators/CI may key on `status`; deliberately not fixed in the copylocks unit |

Disclosures on re-derivation (this round's honesty bar): BS-62-1 keeps the operator's cited coordinates, but `RelatedSourceId` appears **nowhere** in the repo (SELF-RUN case-insensitive `rg` for `related_source_id|relatedsourceid`: zero hits Go+TS+openapi), and live `:505-534` is the `classifyConstructionCategoryScanError` tail plus the head of `constructionCategoryColumn`; the only related-*-carrying region nearby is `constructionPivotSourcePaths:273-286`, which maps `SourceProjections` + `RelatedField.Source.Path` and **drops every related-* identity other than Path** (`RelatedSelection` at `authoringv2/semantic_types.go:126` never reaches the map) — the plausibly-intended display-only drop, flagged for the sweep author's coordinate correction before any repair. BS-62-3: the cited `docs/BUILDER_VERIFICATION.tsv:451-452` does not exist (the TSV has 281 rows at HEAD; the file is the journey lane's uncommitted ` M` and was **not touched** by this writer), so the row is recorded against the standing pair's real source coordinates with the mismatch disclosed. Receipt-missing rows (BS-62-5, BS-62-6) are recorded at the strength of what live bytes support and will be upgraded verbatim when the receipts land — no restation from unread evidence.

### 5. UI contract inventory — data-only sync requirement discharged by verification, not mutation

SELF-RUN with captured exits: `python3 scripts/verify_ui_contract_inventory.py --source-only` → EXIT 0 "PASS … 57 affordances, source hashes and file set current, source bindings complete" (`/tmp/opencode/r62-inv-source.log`); `--self-test` → EXIT 0 (omission + source-add probes) (`…/r62-inv-self.log`); strict `--check` → EXIT 1 with exactly the **399** pre-existing affordance-coverage pending issues that `c838a6f3` left behind (`…/r62-inv-check.log`; per-line tally SELF-RUN) — legacy-coverage debt, not this lane's. No sync was needed at HEAD, therefore none was fabricated: `docs/ui-contract-inventory/ui-supported-affordances.json` and both scripts are byte-untouched this round (porcelain clean), and `normalize_ui_contract_inventory.py` was **never invoked** — its proven lossiness for the authored `reviewedWorkingTree` record (20-field silent drop, reverted) is exactly why `c838a6f3` used only the tool's own primitives; any future data-only sync repeats that recipe (tool primitives only, never the normalizer).

### 6. Ledger + operator-doc deltas

Round 61→62; `status_updates.round62` added; `files{}` 70→75 entries (+5 new hash-records, 2 disclosed re-pins); commitGate unchanged (journey-extension acceptance, bracket-runner wiring, and the two F-DS1 deferred operator questions still gate; the ConstructionPrepareLanes model retires U2 from the prepare-lane check-debt list while U1 stays). `docs/BUILDER_WORKLIST.tsv`: rows 99–110 appended by this writer under the round-62 operator assignment (BS rows mirror §4; operator-decision rows are proposal-only); **row 97 (ROW-SOURCE-04) stays untouched at `proposal-delivered-awaiting-operator`** — column-count integrity re-scanned SELF-RUN (14 columns on every row). `docs/ROW_OPERATION_SOURCE_INPUT_BACKLOG.md` gains § Pending operator decisions with the three Round-62 items (F-DS1 dedupe-bind vs named-422; capability choice-exclusion; EXPAND ratification pointer). Sole-writer respected: the three `verification/publication-bundle` record files plus exactly those two operator-assigned docs files; `docs/BUILDER_VERIFICATION.tsv` (journey lane's uncommitted drafted row) untouched; `verification/builder-interactions/**` untouched; nothing staged, committed, pushed, amended, or `git rm`'d by this writer.

Record integrity tail (Round 62, written last): record-writer's ledger files at write time — FINDINGS.md md5 `5c9c22aec074ac0ef8c49b97235954d0` **excluding this tail paragraph** (verify: truncate this file at `chr(10)+'Record integrity tail (Round 62, written last):'` and hash those exact bytes), OPERATOR_REVIEW_DIGEST.md md5 `7586aa99f6969909a406168bb7d622a8` **excluding that file's Round-62 tail paragraph** (same truncation recipe over the digest bytes), reviewed-sources.json sha256 `efc9d42651505052b50ef1b4d7ef544f0a92d9c467d27f7727ce4080d92cf51b` (full file, round=62, canonical `json.dumps(indent=2, ensure_ascii=False)+"\n"` roundtrip BYTE-IDENTICAL — SELF-RUN). My-run evidence: /tmp/opencode/records-round62.md receipt + `r62_audit.py` sweep output (records=54 match=54 drift=0 annot=19 self=2) + `r62-inv-{source,check,self}.log` (exits 0/1(399)/0) — five live-byte pins (1a–1e) re-derived live == cited-commit blob == on-disk, 2 root-caused re-pins (0dd7be44, post-r61), convention rechecks (bundle_registry authoritative sha256; scope_resolver working-tree md5), runner defer region read :184-200, `constructionPivotSourcePaths:273-286` related-identity drop read, filter-journey :161/:278 reads, TSV 281-row count (BS-62-3 coordinate disclosed as non-existent), repo-wide `related_source_id` zero-hit scan, empty git index at both session ends, BI pin read-only sha256 read (215d2189…). Sole-writer respected: exactly the three publication-bundle record files + the two operator-assigned docs files (worklist rows 99–110, backlog pending-decision section); `docs/BUILDER_VERIFICATION.tsv` untouched (journey lane's drafted row preserved); `verification/builder-interactions/**` untouched; nothing staged, committed, pushed, amended, or removed. Honest labels: BS-62-5/BS-62-6 rest on live bytes only because their cited receipts are absent from disk at record time (not restated from unread evidence); BS-62-1/BS-62-3 carry disclosed coordinate mismatches; U1 prepare-lane debt stands OPEN; journey-extension + bracket-runner + 2 F-DS1 operator questions remain gated. No new product bug CONFIRMED this round; no pending verdict presented as landed.

## Round 63 — reviewRecords 54→62

Eight live-byte hash-pin records across the seven-commit wave `dc52ed0a`·`34e4741d`·`29d2c5b0`·`9d092c5b`·`cf0ca558`·`7e9d1449`·`c63b622e` (the TableShapeSettingsPanel pair booked as TWO records — honest split, tsx + its unit test; the three journey scripts + registry booked as ONE representative entry on the new unpivot journey, which lists the other three in its role); six disclosed re-pins (three wave-owners + three unpivot-lane drift root-caused to `cd39eeaf`); BS-62 seed closures (BS-62-2/-3/-4/-7 closed with commit refs, BS-62-1/-6 not-a-defect with proof, BS-62-5 restated to four byte-derived cases); genbind AllGenerations product defect CLOSED `dc52ed0a`; four new findings rows (genbind, picker re-key, RS03 S-A, T4/bridge); sideFindings tally 39→42.

Attribution: HEAD `c63b622e` (SELF-RUN `git log --oneline dc52ed0a^..c63b622e`: `c63b622e` · `7e9d1449` · `cf0ca558` · `9d092c5b` · `29d2c5b0` · `34e4741d` · `dc52ed0a` · `cd39eeaf` · `d523c25f`). SELF-RUN this session: `sha256sum` vs `git show <commit>:<path>` for all eight new pins (live == cited commit blob, proven per path) and all six re-pins; full hash-aware ledger audit; canonical `json.dumps(indent=2, ensure_ascii=False)+"\n"` roundtrip assert; per-commit `git show --stat`; `git log` blame on the three unpivot-lane drifts (single culprit `cd39eeaf`); `ls`/`git log` proof that `verification/publication-bundle/m-po-probe.cfg` is deleted at `d523c25f`. Receipt-attributed (worker runs, quoted not re-derived): `/tmp/opencode/genbind-review.md`, `genbind-fix.md`, `bs-62-6.md`, `review-fs1-durable.md`, `unpivot-journey-commit.md`, `battery-run-1753.log`, `zod-leniency.md`, `rs03-gap-analysis.md`, `rs03-label-owner.md`, `zl-probe1.txt`/`zl-red1.log`/`zl-green1.log`/`zl-full-vitest.log`, `runner-clobber-red.log`/`runner-clobber-fix.md`/`runner-review.md`, `formal-u1.md`. **One product defect CONFIRMED-and-closed this round (genbind `dc52ed0a`); no new CONFIRMED product bug introduced; nothing pending presented as landed.**

### 1. reviewRecords 54→62 — eight new hash-pin records, each re-derived from LIVE bytes at its cited commit (live == `git show <commit>:<path>`), plus six disclosed re-pins

New records (8):

| path | pin commit | sha256 (live == blob at pin, SELF-RUN) | root cause / what it locks |
|---|---|---|---|
| `internal/catalog/arango/all_generations_project_binding_integration_test.go` | `dc52ed0a` | `17f2414092154737db70f2b366e5dcc6e7f672a8e512bc431ae8a30d6dc5e97f` | NEW durable genbind integration test (dc52ed0a): restricted per-gen write into catalog-recorded project succeeds; cross-project => real ErrForbidden; empty-project bootstrap; overlay-revert RED quotes the undeclared dataset_generation bind error verbatim; F-B27 sentinel PASS. |
| `internal/explorer/lifecycle/fs1_reverse_order_collision_test.go` | `34e4741d` | `f901042baf0903365e13831c7cfaa875ccf72f6f1f29bc7e56c1f9bac16074d7` | NEW durable (BS-62-6 not-a-defect closure): order-invariant F-DS1 signed-slot collision refusal; forward/reverse named refusal + legit acceptance + order-invariance table + 2 full-path locks; M1/M2 delete-your-guard mutants RED then restored byte-identical; no product bytes touched. |
| `scripts/verify-ui/workflows/verify-explorer-unpivot-row-source.mjs` | `29d2c5b0` | `0110894b5ffe291295b01ae7cc5eb03425aff44eb2370cbfd3c1e50c9f62c371` | Journey-family record (ONE entry, dc52 -> 29d2c5b0): NEW 30-check unpivot row-source journey (signed FILTER ledger + signed string melt + C15 awaited-negative date-family refusal 422 UNSUPPORTED_UNPIVOT_SOURCE_DATE_FIELD byte-pinned + discovery-hides across 3 surfaces + surviving public Date oracle + exact 30-check set), two green runs (upr17/upr18) + 18/18-mutant battery ALL RED. Siblings hardened this commit (listed here, not separate records): scripts/verify-ui/registry.mjs (machine-exact filter 17/17, derive 24/24, unpivot 30/30 identical:true), scripts/verify-ui/workflows/verify-explorer-filter-row-source.mjs (+80/-5 silent-vacuous eliminated; live picker re-key find -> pending-verification), scripts/verify-ui/workflows/verify-explorer-derive-row-source.mjs (94->105 asserts). |
| `internal/api/graphql/graph/dataframe/aggregates_test.go` | `9d092c5b` | `a923f2afdf216535a38bf65b33c175c562b7ef56a928fd38a5211698e9c5e9cc` | flaky #1 de-flake (BS-62-3, 9d092c5b): 158-goroutine burst test asserts the observable contract (non-empty batches summing to exactly 158, every call dispatched exactly once) + NEW event-synchronized zero-sleep regression; product bytes IDENTICAL; double-dispatch negative control fails 6/6. |
| `verification/publication-bundle/ConstructionProjectionOwnership.tla` | `cf0ca558` | `1dbd72f6e8f3b549068095f9301ab656cc795f818019fc64d7b3e989101176b6` | NEW formal model in this writer's directory (U1 CLOSED, cf0ca558): exhaustive 5-owners x 4-bits prune x validate (2^20 input product); clean arm CLEAN at 3,517,009 distinct states against the shipped kept-rule; all 8 violated-by-design arms exit 12 on their named invariants; 3 named prune Go tests green, branch-reject strings model-side only; companion 9 cfgs (m-prj-*). |
| `internal/acceptance/runner_status_test.go` | `7e9d1449` | `d9949b79e11d669d37cf093a2996ec8b63324b60543d66ee3565d2da26f752d8` | NEW durable (5 locks, BS-62-7, 7e9d1449): three fixture/lease status locks RED at HEAD (FAILED vs expected FIXTURE_*) -> GREEN post-fix; overlay mutation of pre-fix runner.go reddens exactly the 3 named tests; consumer enumeration confirms class consumed only via report field. |
| `ui/packages/loom-ui/src/features/ExplorerBuilder/components/TableShapeSettingsPanel.tsx` | `c63b622e` | `ba240182c5517023ce84bfa4d68d8a8a213eb58e112a893fd0dc923cefad2b5e` | Client submit-time gate (BS-62-5 Case 1 CLOSED, c63b622e): block melt resolution at <2 input choices -> recoverable role=alert 'Select at least two columns to melt...'; sole dispatch funnel resolveAndPropose -> proposeTableShapeIntent -> tableShapeController.resolveTableShape:320, zero bypass; server stays authoritative (400 MALFORMED_REQUEST at <2), wire zod .min(1) kept. |
| `ui/packages/loom-ui/src/features/ExplorerBuilder/components/TableShapeSettingsPanel.unit.test.tsx` | `c63b622e` | `04d9b78e4b07a878efd54d41eb445e66204b04b7674ee8d03a18fb06561f74f0` | Unit lock for the >=2 melt-resolution gate (c63b622e): submit blocked with precise message at len==1, proceeds at >=2; full loom-ui vitest 88 files / 1166 tests green. |

Re-pins (6) — drift found by this round's sweep, all root-caused, re-derived live == cited blob == on-disk:

| path | drift commit | live hash (== cited blob == on-disk) | root cause |
|---|---|---|---|
| `internal/catalog/arango/resources.go` | `dc52ed0a` | `ab52c1725bf7ad863523635ce47a2954` | genbind AllGenerations bind dropped at source (dc52ed0a); drifted from the r59 pin 51efbe04 (0e3b4c5b authz wave). |
| `internal/explorer/arango/cross_generation_publish_e2e_test.go` | `dc52ed0a` | `97300b4102151c5b19964c05cea4cc2e` | Explorer-arango citation pins shifted at dc52ed0a (:154->:158, :170->:177 drift since 8160bb97); FAILED-ON-PURPOSE F-B13 behavior unchanged. Drifted from prior 79b5834c. |
| `internal/acceptance/runner.go` | `7e9d1449` | `475fd088917beb79ef0ea2024098a3c42cc361d5025594fc8dbc38a74131a47b` | report-status preserve/first-wins fix (BS-62-7, 7e9d1449); copylocks sha256 pin 26bb172c drifted once more at this commit. |
| `internal/explorer/lifecycle/unpivot_source.go` | `cd39eeaf` | `72216be9e8340dc8b171869b74f9634c` | date-family signed-melt refusal (operator decision) landed at cd39eeaf between d523c25f and this wave; drifted from r60 pin 6b439082. Unseen by the r62 sweep (run at feb663a7). |
| `internal/explorer/lifecycle/unpivot_source_test.go` | `cd39eeaf` | `10bb209af4c364bb809f3f2e0cc149b8` | same cd39eeaf date-family refusal touched the durable suite; drifted from r60 pin ada15375. |
| `internal/server/openapi_explorer_construction_unpivot_source_test.go` | `cd39eeaf` | `42e4b2b945f690f41ea875d02e0e5cd8` | same cd39eeaf date-family refusal (HTTP twin); drifted from the r62 re-pin (which itself tracked 0dd7be44). |

- **Count honesty (the "+7 vs 8" note):** the assignment sketched seven pin groups; the TableShapeSettingsPanel gate is a two-file change (`TableShapeSettingsPanel.tsx` gate + `TableShapeSettingsPanel.unit.test.tsx`) and each file is booked as its own ledger record (the r59/r62 convention is one record per real on-disk hash-bearing path), so the honest addition is **+8**, not +7. The three journey scripts (`verify-explorer-{unpivot,filter,derive}-row-source.mjs`) + `scripts/verify-ui/registry.mjs` are booked as ONE record keyed on the genuinely-new unpivot journey and enumerating the other three in its `role` (filter/derive were hardened, registry machine-exact-updated; none was previously pinned, so no separate record is fabricated). Net: 54 → 62 hash-records; the six re-pins reuse existing keys and do not change the count.
- **Post-write audit SELF-RUN:** publication-bundle ledger **62 records / 62 match / 0 drift** (19 annotation-only entries classified per the r59 counting convention; the 2 self-records stay pre-tail), ledger canonical `json.dumps(indent=2, ensure_ascii=False)+"\n"` roundtrip BYTE-IDENTICAL. **3-way agreement:** ledger audit == this section §1 == the digest's Round-63 sweep row, all at **62**.
- Root-cause note on the three unpivot-lane re-pins (`unpivot_source.go`, `unpivot_source_test.go`, `openapi_explorer_construction_unpivot_source_test.go`): each drifted at `cd39eeaf` ("fix(builder): unpivot lane refuses date-family signed melts (operator decision)") — the date-family `UNSUPPORTED_UNPIVOT_SOURCE_DATE_FIELD` refusal that the `29d2c5b0` unpivot journey later byte-pins at check C15 — a commit that landed between the Round-62 ledger commit `d523c25f` and this wave, so the r62 sweep (run at `feb663a7`) never saw it. Live bytes match the `cd39eeaf` blobs (porcelain-clean), so the pins are corrected to current, not fabricated.

### 2. Backlog-seed closures (Round-62 §4 BS-62 rows) — dispositions with evidence and commit refs

| Row | Disposition | Evidence / disposition rationale | Commit |
|---|---|---|---|
| BS-62-1 (Related-*-identity → category-discovery display map) | **not-a-defect** | Zero-consumers proof re-run this round: repo-wide case-insensitive `rg related_source_id\|relatedsourceid` returns ZERO hits (Go+TS+openapi); the `constructionPivotSourcePaths` map drops all related-* identity other than `Path`, and no consumer reads a related-source label — it is a dead display expectation, not a broken contract. Never-fix: none (recorded, not wired). | r62 §4 coordinate + r63 zero-hit re-run |
| BS-62-2 (filter-journey `:278` mut-H2 silent-vacuous + dead `:161` D10 helper) | **closed** | Twin-fixed in the `29d2c5b0` filter+derive hardening wave: silent-vacuous channels eliminated (`builderDoc {}`-swallow, empty-census passes, unobserved persist loops, undecodable-token tamper claims, recorded-stamp never-read identity holes) — each fix RED-quoted via extracted-bytes harnesses; floors grew (filter +80/−5, derive 94→105 asserts). Adversarial PASS on independent reimplemented harnesses (`vr62-harness-*.mjs`, review-twins 49-arm). | `29d2c5b0` |
| BS-62-3 (CI wall-clock flake pair: aggregates 158-burst + replay-skip) | **closed** (aggregates arm de-flaked; replay-skip non-product) | flaky #1 de-flaked `9d092c5b`: the 158-goroutine burst asserted exactly-1 dispatch against the 10 ms `aggregateFallbackDelay` debounce, so a >10 ms enqueue gap legitimately splits 157+1 (old want-1 failed 8/9 under cold-build CPU-steal). Sync-only fix, product bytes IDENTICAL; burst test now asserts the observable contract (non-empty batches summing to 158, every call dispatched exactly once) + a new event-synchronized zero-sleep regression; negative-control double-dispatch fails 6/6. The 30 ms/30 s replay-skip convention is CI-surface only, no product contract. | `9d092c5b` |
| BS-62-4 (`verification/publication-bundle/m-po-probe.cfg` obsolete) | **closed** (cfg deleted) | File absent from disk and history shows deletion at `d523c25f`; superseded by `m-po-{fixed,bug,janitor-blind,reclaim-blind}`. | `d523c25f` |
| BS-62-5 (unpivot zod↔openapi leniency family) | **restated → Case-1 closed, Cases 2-4 hardening** | See §3 (restatement). Case 1 CLOSED by the client gate `c63b622e`; Cases 2-4 are hardening-proposals → OPS-63-01. | `c63b622e` |
| BS-62-6 (F-DS1 pivot reverse-order residual ordering escape) | **not-a-defect (with proof)** | Reverse-order sweep CLOSED with proof: the shared predicate is order-invariant (`TrimSpace(SemanticPath)==FieldPath` class, `FILTER_SOURCE_FIELD_ALREADY_PROJECTED` named both directions); pivot-first reverse refuses named `CANNOT_CROSS_STAGE` at lane entry + full real-compiler Propose; signed-before-pivot is unsubmittable (`INVALID_CANDIDATE`, `construction_change.go:169`). No product bytes touched (md5-verified clean). Additive durables + delete-your-guard mutants M1/M2 RED then restored byte-identical. | `34e4741d` |
| BS-62-7 (runner defer clobbers stage failure class) | **closed** | `7e9d1449` preserve-specific/first-wins. | `7e9d1449` |

- **Flaky-pair twin note:** the standing `flaky-timing-tests` pair closed this round — #1 (aggregates burst) de-flaked at `9d092c5b`; its `internal/acceptance` pair-mate #2 closed as the runner clobber fix `7e9d1449` (the same commit that clears BS-62-7). Both timing-brittle arms now converge without wall-clock dependence. (sideFinding updated accordingly — §4.)

### 3. BS-62-5 restatement — four byte-derived zod↔openapi cases (per `/tmp/opencode/zod-leniency.md`)

The Round-62 row's three-case framing and coordinates were **wrong (lane conflation)**; restated to four byte-derived cases with the records-coordinate corrections the zod worker demanded:

- **Coordinate corrections recorded:** the TableShape zod `tableShapeUnpivotProposalIntentSchema` twin in the spec is `TableShapeUnpivotProposal` at `openapi/openapi.yaml:5069-5080` — **not** `ConstructionUnpivot:3907` (a different schema on a different wire). **Drop** `tableShapeUnpivotPolicyReferenceSchema:830` from the delta list — verified no delta against `TableShapeUnpivotNullRowPolicyReference` (`:5152-5157`). **Case 1 is HTTP 400 `MALFORMED_REQUEST`** (`stage: table-shape-resolution`), **not 422**; the 422/`unprocessable` surface belongs to the separate unpivot-sources lane (`unpivot_source.go:434-435`, `UNPIVOT_SOURCE_COLUMN_MISMATCH`). The two statuses stay distinct.
- **Case 1 — `unpivot.inputColumnChoiceIds` count (zod `.min(1)` vs spec `minItems:2`): CLOSED by `c63b622e`.** Genuine in-scope gap (client accepted what the server refuses). Closure is a **client submit-time gate at `TableShapeSettingsPanel.tsx:268`** (sole dispatch funnel `resolveAndPropose → proposeTableShapeIntent → tableShapeController.resolveTableShape:320`), rendering `Select at least two columns to melt...` via the editor's recoverable `role=alert`; the **server remains authoritative** (`table_shape_resolution.go:226` still returns 400 for `len(<2)`) and the wire zod `.min(1)` bounds are **kept** (the mid-selection draft is plain React state, never zod-parsed; raising `.min(1)→.min(2)` at `types.ts:1034/:871` would crash submit/capability-load with a ZodError instead of naming the rule). Evidence: RED `zl-red1.log` → GREEN `zl-green1.log` + live 400 probe `zl-probe1.txt`; full loom-ui vitest 88 files / 1166 tests green.
- **Case 2 — `unpivotSources[].columnId` required-vs-optional: benign-overlap, hardening-proposal at most.** Request direction is stricter-equal; the `optional()` is response/capability parsing only (`types.ts:2050`), and the server validator closes the wire surface (`unpivot_source.go:440-452` refuses a `ColumnID` that is not a melt input). No behavioral delta on any 422 path → needs no change.
- **Case 3 — `unpivotSourceInput.choices[].label` `.min(1)` vs spec-legal `""`: server-accepts-client-refuses, hardening-proposal.** Client is stricter than the spec (`type: string` no minLength); a server-legal empty label would hard-fail capabilities `.parse`, not silently accept. Latent (no live empty-label path observed — labels are candidate-derived) → align by adding `minLength:1` to the spec, not a product defect.
- **Case 4 — construction-lane `operation.unpivot.inputs` no client min: spec+client admit what the server refuses; form-gated, hardening-proposal.** Neither zod (`types.ts:1676`, no `.min()`) nor the spec (`inputs` required but no `minItems`) enforce non-empty, while `construction_validation.go:1471` refuses `len==0`; **not UI-reachable** — `ConstructionReshapeEditor.tsx:1297` blocks an empty melt at the form layer, so zod is not the gate. Note the lane asymmetry it exposes (construction lane permits a 1-input melt; table-shape lane requires ≥2).

Cases 2-4 are deferred (not merged with the filter/derive "tighten in one pass" framing — Case 2 needs no change, Case 4 is form-gated) and triage-tracked at **OPS-63-01**.

### 4. New findings rows (this round) + sideFindings tally

- **genbind AllGenerations product defect — CLOSED at `dc52ed0a` (F-B27-adjacent).** Live-Arango reproduction: every restricted-scope direct write to a project with recorded catalog paths died on `bind parameter 'dataset_generation' was not declared in the query` — the union AQL `existingAuthResourcePathsAllGenerationsAQL` carries only `@project` while the bind map always passed `@dataset_generation` — and `authoringWriteDirect` masked the storage fault as 403 via the F-B27 branch (`scope_resolver.go:242`). Root cause (`dc52ed0a`) drops the undeclared bind rather than declaring+using it: **live-Arango RED→GREEN** (restricted write into a catalog-recorded project succeeds per-gen; cross-project → real `ErrForbidden`; empty-project bootstrap), with the reviewer **counterfactual** proving declare+use would filter the union to generation-less rows → zero-path `AuthorizeWrite` bootstrap → reopen the exact F-B27 cross-project bypass (foreign path returns nil). Durable `all_generations_project_binding_integration_test.go` + F-B27 sentinel PASS. The 403-masking classification itself (internal/server) is filed as a follow-up sideFinding; root cause removed at source.
- **picker re-key (filter-journey live find) — VERDICT: pending-verification, honestly.** The hardened `29d2c5b0` filter run REDS 2/2 on a **deterministic new-arm** find: post-`READY` the Column select rebinds `source:rc…` → `filter-input_<uuid>` (editor display re-key). Is the arm fixed in the `29d2c5b0` wave? **NO** — the `tableShapeController`/`:321` correction is **uncommitted**, pending a confirmation run; recorded pending-verification (no seed lever existed, no assertion weakened). `docs/BUILDER_VERIFICATION.tsv` row 279 stays honestly partial (journey lane's file — untouched by this writer).
- **ROW-SOURCE-03 slice S-A — IN FLIGHT** (precise lane reason classes). Cite `/tmp/opencode/rs03-gap-analysis.md` + `/tmp/opencode/rs03-label-owner.md`: only 1 of 4 reason classes (version/type/route/stage) is distinct and only narrowly; one generic `NO_ELIGIBLE_POPULATED_*` code fuses empty-data+type+route and mislabels the real stage/route-reach case (G1 HIGH, contract-central). S-A (server-only reason split at the four lane enumerators) is the recommended next slice; **S-C (schema-Title display labels) is next-next** — unblocked and queued, gated on the label-owner determination resolved in `rs03-label-owner.md` (INTENTIONAL anti-drift invariant; **the lowerer copies `projection.Label`, does not recompute**, so one shared builder across all 12 `constructionFHIRPathLabel` sites keeps both sides atomic — no `internal/dataframe/**` change needed); **S-B/S-D later**. Worklist row 96 note updated accordingly.
- **T4 stack state + bridge hot-reload caveat — standing.** Recorded as an operator-visible caveat: the T4 stack retains a bridge hot-reload dependency — a **stale bridge can mask a reload**, so an **operator restart is needed at the next product change** before runtime acceptance is treated as fresh. Booked as a side caveat (non-blocking), not a runtime mutation by this sole-writer.

- **sideFindings tally: 39 → 42 (SELF-RUN, JSON canonical).** Updated: `flaky-timing-tests` (pair #1 de-flaked `9d092c5b`, #2 closed `7e9d1449`); `formals-U1-pivot-source-owner-branches-uncovered` → **CLOSED** by the `cf0ca558` `ConstructionProjectionOwnership` model (exhaustive 5-owners × 4-bits prune×validate model; clean arm CLEAN at 3,517,009 distinct states against the shipped kept-rule; all eight violated-by-design arms exit 12 on their named invariants; two verification-side defects found+fixed in-loop → SKILL.md warnings). Added: genbind 403-masking classification follow-up (internal/server, root cause removed at source); picker re-key pending-verification (`:321` correction uncommitted); T4 stack / bridge hot-reload operator-restart caveat.

### 5. Ledger + operator-doc deltas

Round 62→63; `status_updates.round63` added; `files{}` 75→83 entries (+8 new hash-records, 6 disclosed re-pins); commitGate unchanged (journey-extension acceptance, bracket-runner wiring, and the two F-DS1 deferred operator questions still gate; U1 now CLOSED alongside U2). `docs/BUILDER_WORKLIST.tsv`: BS-62-1/-6 → not-a-defect, BS-62-2/-3/-4/-7 → closed with commit refs, BS-62-5 → Case-1 closed / Cases 2-4 → OPS-63-01; row 96 (ROW-SOURCE-03) `next_action` gains the S-A/S-C queue note; new row **OPS-63-01** added (zod Cases 2-4 hardening proposals awaiting triage); **all awaiting-operator rows (OPS-62-01/02) and ROW-SOURCE-04 (row 97, proposal-delivered-awaiting-operator) left intact** — 14-column tab-integrity re-scanned SELF-RUN before and after (13 tabs/row, appended row matches header). `docs/ROW_OPERATION_SOURCE_INPUT_BACKLOG.md` § Pending operator decisions gains one item (zod Cases 2-4 hardening triage). Sole-writer respected: the three `verification/publication-bundle` record files + the two operator-assigned docs files; `docs/BUILDER_VERIFICATION.tsv` untouched (journey lane's row-279 pending flip preserved); `verification/builder-interactions/**` untouched; nothing staged, committed, pushed, amended, or `git rm`'d by this writer.


Record integrity tail (Round 63, written last): record-writer's ledger files at write time -- FINDINGS.md md5 `bd4d2beace18f53d0fb5f0c2374d7cde` **excluding this tail paragraph** (verify: truncate this file at chr(10)+'Record integrity tail (Round 63, written last):' and hash those exact bytes), OPERATOR_REVIEW_DIGEST.md md5 `5e9ad45cf8f4ef3b9935b6f19375f54b` **excluding that file's Round-63 tail paragraph** (same truncation recipe), reviewed-sources.json sha256 `cce6c2f4bfab68459068238dad7644b3ab3385fd8a0b0db8eb73b9489fb06619` (full file, round=63, canonical json.dumps(indent=2,ensure_ascii=False)+"\n" roundtrip BYTE-IDENTICAL -- SELF-RUN). My-run evidence: /tmp/opencode/records-round63.md receipt + r62_audit.py sweep output (records=62 match=62 drift=0 annot=19 self=2). Eight new pins re-derived live == cited-commit blob == on-disk; six re-pins root-caused (3 wave @dc52ed0a/7e9d1449 + 3 unpivot-lane @cd39eeaf). repo-wide related_source_id zero-hit re-run (BS-62-1 not-a-defect); m-po-probe.cfg deletion at d523c25f verified by ls+git log. Sole-writer respected: exactly the three publication-bundle record files + the two operator-assigned docs files (BS-62 dispositions, row 96 RS03 note, new OPS-63-01 row, backlog Pending-decisions +1); docs/BUILDER_VERIFICATION.tsv untouched (journey lane row-279 flip pending); verification/builder-interactions/** untouched; nothing staged, committed, pushed, amended, or git rm'd. Honest labels: picker re-key + RS03 S-A are pending-verification/in-flight, NOT landed; BS-62-5 Cases 2-4 are hardening-proposals (OPS-63-01) not defects; genbind is the only new product defect (CONFIRMED + CLOSED).

## Round 64 (part-1) — ROW-SOURCE-03 row-96 server-side COMPLETE (S-A `ebf97a40` · S-C `29d63f6f` · S-B `d3a1d12a`) + zero-value runner named fault `f177a9ed` + formal re-sync `ea6df079`; reviewRecords 62→71; journey AUTHORED pending live window `7fcb640f` + 403-masking CLOSED `3cfec1b7` (part-2 folded in §6)
Six commits since round 63 (`ebf97a40` · `f177a9ed` · `ea6df079` · `29d63f6f` · `d3a1d12a` · `e86fca06`), each body read by this writer. The three RS03 slices land every remaining server-side clause of the contract sentence (`precise reasons ...` + `meaningful field/concept labels` + `distinguish empty data, loading, inventory errors`; loading already met per row-96 history). Closure of row 96 is NOT claimed: it awaits the journey-lane re-accept + focused-native-journey runtime (3 WILL-FLIP expectation families below). This half is scope-disciplined: `docs/BUILDER_VERIFICATION.tsv` and the backlog doc are EXCLUDED (journey outcomes pending; backlog untouched — nothing here trivially required it). `e86fca06` (docs-only round-63 integration, 5 record files) needs no ledger entries: `reviewed-sources.json` excludes itself, FINDINGS/DIGEST carry their standing per-round self-records, and the two `docs/` operator records were never ledger-tracked (all ledger entries are product/model/verification source bytes) — judged honestly, no omission.
Attribution: HEAD `d3a1d12a` (SELF-RUN `git log --oneline e86fca06^..d3a1d12a`: `d3a1d12a` · `29d63f6f` · `ea6df079` · `f177a9ed` · `ebf97a40` · `e86fca06`). SELF-RUN this session: `sha256sum` vs `git show <commit>:<path>` for all 9 new pins + 4 re-pins + the clear-test sibling (live == cited blob == on-disk, every path asserted in-run by the build script, zero hand-typed digests); full hash-aware ledger audit; canonical `json.dumps(indent=2, ensure_ascii=False)+"\n"` roundtrip assert; 14-column TSV integrity scan; per-commit `git show --stat` + full-body reads. Receipt-attributed (worker/reviewer runs, quoted not re-derived, same-model reviews disclosed): `/tmp/opencode/rs03-gap-analysis.md`, `rs03-sa-review.md` (**PASS**, 24 kills), `rs03-sa-red.log`, `rs03-label-owner.md`, `rs03-sc.md`, `rs03-sc-review.md` (**C6 REQUEST-CHANGES → fix → re-check PASS**, control-byte witness), `rs03-sc-red-*.log`/`rs03-sc-recheck.log`, `rs03-sb.md`, `rs03-sb-review.md` (**PASS 6/6**), `rs03-sb-red.log`/`rs03-sb-mutation.log`, `zero-runner-fix.md`, `zero-runner-review.md` (**PASS 5/5**), `formal-resync64.md`, `label-journey-coupling.md`. Go/race/TLC runs are worker- or parent-run per commit bodies; this writer re-derived only bytes, hashes, and file/commit facts.
### 1. reviewRecords 62→71 — nine new live-byte hash-pins (live == `git show <pin>:<path>` == on-disk, SELF-RUN) + four disclosed re-pins
New records (9):
| path | pin commit | sha256 (live == blob at pin) | root cause / what it locks |
|---|---|---|---|
| `internal/explorer/lifecycle/construction_pivot_source.go` | `d3a1d12a` | `e3539cf3947733176665833b8dc5b8513305e919d98fbdb2fb9715590b3e78fa` | shared classifier + sole label builder: S-A decision()/onlySkipClass per-class reasonCodes (stage/reach split from empty-data fusion), S-C constructionFieldDisplayLabel + unicode.IsControl refusal (reviewer witness `Active\x00flag\x1b[31mred` → `SafeDisplay`), S-B pivot classifier + static inventory reason @:66 fail-closed. |
| `internal/explorer/lifecycle/construction_pivot_source_test.go` | `d3a1d12a` | `3565e4a4a5ec11062625f077143d8be844720799df84ed907d1bdd516cd6eeab` | pivot durables: S-A 7-class reason table (mutant battery 6 dispatch × 4 tables = 24 kills), S-C fallback/fallthrough table incl. control-byte witness rows, S-B 3-row all-three-distinct inventory/empty/observed gate. |
| `internal/explorer/lifecycle/derive_source.go` | `d3a1d12a` | `25f90fa5abab391b5e280fb6bc693058ba9b8468f8a16e3ee00054db8eca1458` | derive lane: S-A sites :126-132, S-C label sites :161/:232, S-B abort-before-loop :136 (Supported=false, zero choices, StageID preserved). |
| `internal/explorer/lifecycle/derive_source_test.go` | `d3a1d12a` | `9bd5cff74ce8cbc9db9c25fdfedbe4325a87606f400e3ce2d6438c0531c3b86b` | derive durables: skip-class reason table + derive-only stale-skip wiring kill, Title fixtures, 3-row distinctness table. |
| `internal/explorer/lifecycle/filter_source.go` | `d3a1d12a` | `6e82dbdf593316753687e88266787bab88217f6ea9e88211fdf93b1f64c21051` | filter lane: S-A sites :83-90, S-C sites :114/:182, S-B abort site :93 re-proven with reviewer-injected failing planner. |
| `internal/explorer/lifecycle/filter_source_test.go` | `d3a1d12a` | `b5dd9ad1ccae857f47f6cca7d5a17ec266acc9950479de1609890258845bf9ad` | filter durables (S-A/S-C/S-B tables + Title fixture); sibling `filter_source_pivot_clear_test.go` booked in its role, not as a separate record (created `b72a1322`, edited at `ebf97a40`, never independently pinned; live `29508ad4efadd9b817462281d48531faa74fa6edc4634350fe4fc7c78f510b4d` == blob@d3a1d12a == blob@ebf97a40 SELF-RUN). |
| `internal/acceptance/runner_zero_value_test.go` | `f177a9ed` | `7f5cfef5aeeb4bcd432fdf2c7987aaf10dbfc595458c7e1efae9bc6a2def8788` | NEW durable: 6-row nil-field table + controls (guard-neutralized → panic-at-:175 witness RED; validator-split reddens the New-rejects lock too; no fabricated status); pair of the `runner.go` re-pin. |
| `internal/server/openapi_explorer_construction_filter_source_test.go` | `29d63f6f` | `511f411c7ebec9873b3ee680be740d9a9eec116a54b7e0a84032c96649c94219` | openapi HTTP twin armed-facts pin (S-C, openapi :216): advertised label is the single builder output, byte-mirrored into durable claim + compiled artifact; TAMPERED-LABEL/DRIFTED-COMPILED-LABEL pairs green. |
| `verification/publication-bundle/SKILL.md` | `ea6df079` | `8ac0c3c5f79526a0a7a0804baac5293bf893325bc2e49804fb2477b64cf018c8` | first pin of the formal SKILL record: PrepareLanes covered-code re-pin @ebf97a40 ranges + date-family "Not covered" admission (no model describes it; 3 durable refusal tests + accepted journey C15 as justification). |

Re-pins (4) — supersessions on moved bytes, each marked **not-a-duplicate** in its `rederived` note, re-derived live == cited blob == on-disk:
| path | live sha256 | supersedes | root cause |
|---|---|---|---|
| `internal/explorer/lifecycle/unpivot_source.go` | `c80df28eaf0d822131ab4021188bd3184701b2f1017d5447e200454806096295` | r63 md5 `72216be9e8340dc8b171869b74f9634c` @`cd39eeaf` | date-family admission (cd39eeaf era) now carries S-A :187-193 reason emission, S-C :222/:296/:579 label sites, S-B :197 fail-closed abort |
| `internal/explorer/lifecycle/unpivot_source_test.go` | `faa60db2113652116fe2a9606c6a88abd2e0b17be89bd06b4f1736e6954b1175` | r63 md5 `10bb209af4c364bb809f3f2e0cc149b8` @`cd39eeaf` | same three slices touched the durable suite (promotion md5→sha256 disclosed) |
| `internal/acceptance/runner.go` | `b0c7aa89a3f030a877c704f957def0ae22426005f68488387af7825e23ac8666` | r63 sha `475fd088917beb79ef0ea2024098a3c42cc361d5025594fc8dbc38a74131a47b` @`7e9d1449` | f177a9ed Run-entry validateRequired hoist + double-%w named fault (`errRunnerUninitialized%w` + field, both joinable); New() guards byte-verbatim identical (reviewer diff) — copylocks + status-preserve semantics intact |
| `verification/publication-bundle/ConstructionPrepareLanes.tla` | `f1e3595a80295f92ceb2229a47e293a31beca368f7ecd8bd97a60672dd3b5e0b` | r62 sha `37352ef394cecc86f64ffbc2188fde59eecd99756b500c79de70e8a68d1087d9` @`feb663a7` | ea6df079 shift-only header re-pin @ebf97a40 (formal-resync64); clean arm 61,456 distinct verbatim, model semantics unchanged |

- **Count honesty:** the assignment sketched six pin-groups; the honest ledger delta is **+9 new records / 4 re-pins**. Disclosed bookings: `filter_source_pivot_clear_test.go` (pre-existing file, fifth ebf97a40 test touched) is enumerated inside the filter-table record rather than fabricated as a never-before-recorded standalone (r63 sibling convention); ConstructionPrepareLanes.tla + SKILL.md are booked separately per the one-record-per-real-path convention (SKILL.md is a FIRST pin, not a re-pin; the tla entry is a supersession); the two md5-era unpivot-lane entries are promoted to sha256, disclosed above.
- **Post-write audit SELF-RUN:** publication-bundle ledger **71 records / 71 match / 0 drift** (19 annotation-only, 2 self pre-tail; same r59 counting convention), canonical roundtrip BYTE-IDENTICAL. **3-way agreement:** ledger audit == this section §1 == the Round-64 sweep row in the digest, all at **71**.
### 2. RS03 row 96 — server-side clauses COMPLETE (three slices + adversarial receipts)


Contract sentence: *precise reasons for unsupported version, type, route, or stage*; *distinguish empty data*; *show meaningful field/concept labels*; *distinguish empty data, loading, inventory errors* (`docs/ROW_OPERATION_SOURCE_INPUT_BACKLOG.md` line 13, operator-approved). Status per clause, each resting on a reviewer-executed receipt:
- **Precise reason classes — S-A `ebf97a40` — LANDED.** Four signed lanes' silent per-field continues (filter :83-90, derive :126-132, unpivot :187-193, pivot analogue) emit per-class reasonCodes; the stage/reach-vs-empty-data fusion behind single `NO_ELIGIBLE_POPULATED_*` is split (distinct stage/reach code + `NO_OBSERVED_VALUES_*` for truly empty); one shared classifier keeps the lanes single-source. WIRE-UNCHANGED (zero JSON/struct changes; journey scripts key on no reasonCode value). Adversarial **`/tmp/opencode/rs03-sa-review.md` PASS**: 6 dispatch branches × 4 tables = **24 mutant kills** + per-lane skip-deletion kill + 3 reviewer overlay fixtures prove reason honesty (inventory-lookalike never masquerades as observed/type/route; mixed classes stay generic).
- **Meaningful field/concept labels — S-C `29d63f6f` — LANDED (with control-byte refusal).** Twelve advertise/resolve label sites across the four lanes share ONE `constructionFieldDisplayLabel` (Title → row-choice display → legacy `ResourceType.path`); one-builder atomicity keeps the anti-forgery mirror byte-exact (semantic_compile.go:528 / lower/recipe_compile.go:484 untouched; reconcile.go:509 intact; TAMPERED-LABEL / DRIFTED-COMPILED-LABEL pairs green). Adversarial `rs03-sc-review.md`: **C6 FAIL → fix → PASS** — reviewer live witness `Active\x00flag\x1b[31mred` reached the advertised payload and durable claims pre-fix; post-fix every `unicode.IsControl`-bearing candidate is refused and falls through (`\x7f`, `\r\n` classes asserted per-rune; clean candidate still first), fallback ingredients regex-pinned so crafted roots die in the planner. Mutation: guard-neutralization REDs exactly the 3 control rows.
- **Inventory-error distinction — S-B `d3a1d12a` — LANDED.** Per-lane `{PIVOT,FILTER,DERIVE,UNPIVOT}_SOURCE_INVENTORY_UNAVAILABLE` static codes via the shared classifier; fail-closed preserved (Supported=false, zero choices, StageID unchanged, abort-before-loop at all 4 sites — reviewer-injected failing planner re-proves); original error NOT swallowed (typed `unavailable()` `%w` chain; reviewer fixture reaches the injected sentinel via `errors.Is`, `errors.As` carries Class=Unavailable + lane code); **no-leak**: reviewer-injected junk (`SELECT * FROM secret`, tokens) never crosses into payload Reason/ReasonCode/typed Message. Distinctness load-bearing: classifier mutant REDs the inventory row in all 4 lanes. Adversarial **`rs03-sb-review.md` PASS 6/6** incl. S-A/S-C byte-preservation hunk audit (gofmt column shift only).
- **Loading already met** (pre-existing loading surface per the gap analysis; no change needed this wave).
- **Zero-value Runner named fault — `f177a9ed` — LANDED** (acceptance-harness integrity, adjacent to this wave): `Runner{}` + `Run` panicked at :175 (New-time guards covered only the constructor path); Run-entry `validateRequired()` re-check wrapped `errRunnerUninitialized%w`+field%w (both joinable, reviewer-proven), New() guards byte-verbatim identical, package precedent followed (`fixture.go:190` named errors, zero panics). Adversarial **`zero-runner-review.md` PASS 5/5** (bypass hunt: guard is Run's first statement; two methods total).
Honest gates: `go vet` clean throughout; lifecycle/server `-race` ok per commit bodies (parent-run where the worker stalled); gates are attributed to their receipts, not re-run by this writer. **The four certified row-source journeys are NOT re-accepted yet** — see §3.

### 3. Label↔journey coupling audit — 3 WILL-FLIP families; journey outcomes deferred (part-2)

Read-only audit `/tmp/opencode/label-journey-coupling.md` at `29d63f6f` (byte-level; no runtime string capture): production populates `Candidate.Label` with the catalog field path (`explorer_capability.go:405`/:457-458), so effective lane labels are **bare paths**; the UI humanizers keep most assertions identical. Exactly three certified expectation families will flip — exact new server-truth strings:
1. `verify-explorer-filter-row-source.mjs:374` — gender option display text: certified `'Patient · Gender (…)'` → **`'Gender (' + choice.logicalType + ')'`** (`/patient/i` becomes FALSE; drop that conjunct, keep `/gender/i` — never loosen to vacuity).
2. `verify-explorer-unpivot-row-source.mjs:773` **+** `:837` — row-1 bound label `'Observation.valueQuantity.unit'` → **`'valueQuantity.unit'`**.
3. `verify-explorer-unpivot-row-source.mjs:775` **+** `:839` — row-2 bound label `'Observation.subject.reference'` → **`'subject.reference'`**.

Every title-attribute / fieldPath / capabilities-equality assertion is SAME (23 of 26 audited rows). Journey-lane obligations — **UPDATED (part-2): the four-state focused-native journey is AUTHORED, pending the live window (`7fcb640f`)**. Artifacts now in-tree: `scripts/verify-ui/workflows/verify-explorer-row-source-four-state.mjs` (registry sha-gated artifact `04219d51…` = `04219d511d4b44d7644560c0ff8ff8d871364d67af98543953c1fe07809a1573`, unchanged by the coordinator fix; 6-check closed set incl. catalog-path labels, reason-class consumption, inventory naming, empty-data disjointness, UI reason-consumption with honest UI-gap channel; stamp fail-closed wiring + refuse-overwrite --out), `scripts/verify-ui/helpers/tests/row96-row-source-four-state.test.mjs` (4 offline locks), registry scenario `row96-row-source-four-state` + static-contract entry, `docs/BUILDER_VERIFICATION.tsv` row 96 → journey-authored pending live window with artifact sha. **Disclosed caught-gap (coordinator correction, untrusted-worker gate)**: the worker's live-journey registry entry omitted the mandatory coverage array; the first full 99-file offline suite run crashed `lifecycleAcceptanceDrift` (TypeError flatMap, append-reload-timing 6/7). Fixed at the integration boundary with a drift-legal probe-kind coverage block (intent row-lifecycle, kind probe, case fourState), status honestly `untested` (no live run yet); post-fix: append-reload 7/7 fail 0, row96 tests 4/4, full 99-file sweep ZERO failing files (`/tmp/opencode/tests-sweep-failures.txt` empty), 36 workflow scripts node --check green, `--dry-run` DRY_RUN_OK checks=6. Still OWED by the journey lane (`docs/BUILDER_VERIFICATION.tsv` is theirs; excluded from this writer): (a) flip the 3 WILL-FLIP families above with equality preserved; (b) LIVE window run — JOURNEY_OK checks=6 + WILL-FLIP re-asserts + filter row-279 flip + status→implemented, in a FRESH runtime window (standing T4-bridge operator-restart caveat); (c) filter/derive/unpivot + registry revalidation (17/24/30 unchanged expected).

Disclosure — **UPDATED (part-2): CLOSED at `3cfec1b7`.** The 403-masking classification follow-up (genbind triage, internal/server) was honestly recorded **IN FLIGHT** in part-1 with no result claimed; the wave then landed the fix: authoring-write storage faults (Arango client faults incl. the classic undeclared-bind text, context deadline, authorization-backend-unavailable) previously masked as 403 via the F-B27 arm; now TYPE-only errors.Is/As classification -> named `503 BACKEND_UNAVAILABLE` + `%w` Cause, unclassified errors keep the byte-identical 403 (no 5xx default). Adversarial arc (`/tmp/opencode/f403-review.md`, same-model disclosed): round-1 **REQUEST-CHANGES** — reviewer overlay witness turned store-level denial `ArangoError{Code:403,ErrorNum:11 "not authorized"}` into retryable 503 (privilege-escalation shape); repair `isArangoPermissionResponse` deny-direction predicate (driver numeric fields only, deny-first ordering) -> **PASS-after-repair** with reviewer bypass battery ({0,11}/{403,0}/Join composites → 403; {400,1561} → 5xx), deny-first composite pinning, zero message-substring matching, and the worker f403-c2 anti-mask mutation matrix 12/12→26/26 (`f403-c2-red.log`/`f403-c2-green.log`). Durables pinned in ledger (§6).

### 4. sideFindings tally 42→44 (part-1) → **45 (part-2)** + disclosures
- **UPDATED (part-2):** `genbind-403-masking-classification-followup` → **CLOSED @`3cfec1b7`** per the REQUEST-CHANGES→PASS-after-repair arc above (part-1's IN-FLIGHT label was correct at write time; supersession recorded in the entry itself).
- **+1 (self-found):** `rs03-label-willflip-journey-reaccept-pending` — the 3 WILL-FLIP families verbatim; gates row-96 closure; journey lane owns.
- **+1 (self-found, from the coupling audit caveats):** `lane-label-title-chain-unreachable-upstream` — production `Candidate.Label` = catalog path (`explorer_capability.go:405`/:457-458), so the advertised schema-Title head of the S-C chain never binds live data flow; Title behavior pinned only by hand-built fixtures; recorded for builder/capability owners (not a closure blocker; labels land honest either way).
- Nothing else found while reading the six commits: no new product-behavior defects beyond what the slices themselves fix; zero-drift sweep backs the tally.

### 5. Ledger + operator-doc deltas (part-1)
Round 63→64; `status_updates.round64` added; `files{}` 83→92 (+9 new hash-records, 4 disclosed re-pins; FINDINGS/DIGEST self-records re-pinned pre-tail). `docs/BUILDER_WORKLIST.tsv`: **row 96 (ROW-SOURCE-03)** status → `server-side clauses landed (S-A/S-B/S-C commits), closure pending focused-native-journey + journey re-accept runtime`; `next_action` r63 queue-note replaced by the landed/closure-pending note with the exact WILL-FLIP strings; `evidence` += the exact receipt list under `/tmp/opencode/` (rs03 slice receipts + reviews + coupling audit). **No other row touched** — 14-column tab-integrity SELF-RUN before/after (header/row-96/row-97 at 13 tabs each; the pre-existing legacy multi-line cells continuing at rows 55-56/83-86/88-90 preserved byte-identical; per-row tab profile unchanged everywhere; awaiting-operator rows OPS-62-01/02, OPS-63-01, and row 97 ROW-SOURCE-04 byte-identical). `e86fca06` judged: no ledger entries owed (record files; ledger excludes itself; docs never ledger-tracked). Sole-writer respected: exactly the three `verification/publication-bundle` record files + `docs/BUILDER_WORKLIST.tsv`; `docs/BUILDER_VERIFICATION.tsv` untouched (journey lane's row-279 flip pending); `docs/ROW_OPERATION_SOURCE_INPUT_BACKLOG.md` untouched (nothing trivially required); `verification/builder-interactions/**` untouched; nothing staged, committed, pushed, amended, or removed by this writer.

Record integrity tail (Round 64 part-1, written last): record-writer's ledger files at write time -- FINDINGS.md md5 `9bcddfcbb2c4e2f066b42bb26c961481` **excluding this tail paragraph** (verify: truncate this file at chr(10)+'Record integrity tail (Round 64 part-1, written last):' and hash those exact bytes), OPERATOR_REVIEW_DIGEST.md md5 `85cbdf362aae7d8f4ab894e945fea1c2` **excluding that file's Round-64-part-1 tail paragraph** (same truncation recipe), reviewed-sources.json sha256 `aa5e8da7eb9d59982a010a3019fdae7a681570222014eb752e88498b1f101b4e` (full file, round=64, canonical json.dumps(indent=2,ensure_ascii=False)+"\n" roundtrip BYTE-IDENTICAL -- SELF-RUN). My-run evidence: /tmp/opencode/records-round64a.md receipt + r62_audit.py sweep output (records=71 match=71 drift=0 annot=19 self=2) produced by /tmp/opencode/build_r64a.py (script-asserted live==cited-blob==on-disk for all 9 new pins + 4 re-pins + sibling; zero hand-typed digests). Honest labels: journey re-accept + focused-native-journey NOT run this half (part-2); 403-masking triage IN FLIGHT, no result claimed; same-model adversarial reviews disclosed. Sole-writer respected: exactly the three publication-bundle record files + docs/BUILDER_WORKLIST.tsv; docs/BUILDER_VERIFICATION.tsv + backlog doc untouched; nothing staged, committed, pushed, amended, or git rm'd.

### 6. Part-2 fold-in (parent-committed `7fcb640f` + `3cfec1b7`) — reviewRecords 71→78, sideFindings 45


Both new product-commit bodies read in full. Ledger +**7** new live-byte records (the assignment sketched ~+5; honest delta is 7, disclosed): every entry re-derived script-side live == `git show <pin>:<path>` == blob@HEAD `3cfec1b7` == on-disk in a disposable detached worktree of the parent HEAD (`/tmp/opencode/wt-r64b`; zero hand-typed digests); the part-1 tallies above stand as written history of the part-1 sweep, re-tallied here.


| path | pin commit | sha256 (live == blob at pin == blob@HEAD == on-disk@HEAD) | root cause / what it locks |

|---|---|---|---|

| `scripts/verify-ui/workflows/verify-explorer-row-source-four-state.mjs` | `7fcb640f` | `04219d511d4b44d7644560c0ff8ff8d871364d67af98543953c1fe07809a1573` | row-96 four-state focused-native journey, AUTHORED offline (no browser run claimed): 6-check closed set + stamp fail-closed wiring + refuse-overwrite --out; registry sha-gated artifact unchanged by the coordinator fix. |

| `scripts/verify-ui/helpers/tests/row96-row-source-four-state.test.mjs` | `7fcb640f` | `2285aa23e030e16bae105ceeca0a17cc79251e4c06fe3e7b3f96127b76b7ff60` | 4 offline locks: --dry-run, registry<->script machine identity, structural guards, no-vacuity + UI-gap channel (4/4 green). |

| `scripts/verify-ui/registry.mjs` | `7fcb640f` | `bd8053144d127fd4b125a0993e50bc93c82862e861c94ffa17f96922505b4fe4` | scenario row96-row-source-four-state + static-contract entry + the drift-legal probe-kind coverage block added by the coordinator after the omitted-coverage-array crash (status untested, honest). |

| `docs/BUILDER_VERIFICATION.tsv` | `7fcb640f` | `761d3d6e95afe14bf919ad8c1dea678ae60c46f5f01697eed1b8dcd3a943d8f7` | 1-line row-96 update → journey-authored pending live window w/ artifact sha; pinned READ-ONLY per operator assignment — journey lane retains write ownership; row 279 still theirs. |

| `internal/server/openapi_explorer_authoring_write_fault_test.go` | `3cfec1b7` | `e14f4f5db04f61665c7921af09641a2b858aff260ee0d20763b60fb881c6bf79` | NEW durables matrix {401,403+11,ErrorNum-11} ∪ {query-400,bind-1552,1561} at handler + generated publish route + both legacy sites; anti-mask mutation load-bearing (f403-c2 12/12→26/26). |

| `internal/server/openapi_explorer_authoring_direct.go` | `3cfec1b7` | `9aaecd4e8835c04f16f542abc57e4d429946b6217622277a44368ddeaa433f85` | masking root cause: type-only 503 BACKEND_UNAVAILABLE + %w Cause for deadline/backend-unavailable sentinels + ArangoError query-fault; reviewer-C2 `isArangoPermissionResponse` deny guard; unclassified keep byte-identical 403. |

| `internal/server/openapi_explorer_direct.go` | `3cfec1b7` | `b57e753f8205cd0f394ecdea562f128b2a92a4c98bb429129f9b93e47e8b22d9` | the two legacy AuthorizeWrite sites (+6 lines) route through the identical shared predicate; cause preserved. |



- **Registry re-pin honesty (the assignment asked part-1 to have pinned it): verified NO — part-1 did not pin `registry.mjs`; the r63 round booked it ONLY INSIDE the unpivot-journey record's role (@`29d2c5b0`). This is its FIRST standalone record; the entry marks the supersession of that inline booking **NOT-a-duplicate** and re-derives live bytes itself. No fabricated re-pin.

- **Post-write audit SELF-RUN @parent HEAD `3cfec1b7`:** publication-bundle ledger **78 records / 78 match / 0 drift** (19 annotation-only, 2 self pre-tail — counts re-verified from the ledger in the disposable worktree AFTER final writes; canonical roundtrip BYTE-IDENTICAL). **3-way agreement at 78:** ledger audit == this §6 == the digest's Round-64 part-2 sweep row.

- **sideFindings 45:** part-1's IN-FLIGHT pair updated in place to CLOSED for entry `genbind-403-masking-classification-followup` (arc + receipts cited; supersession wording records the timeline), plus +1 self-found `row96-ui-four-state-met-by-code-plus-expand-proposals` — parent read-only audit `/tmp/opencode/row96-ui-four-state-audit.md`: clause **MET-by-code** for all three signed lanes (anchors BuilderWorkspace.tsx:3940-3946, ConstructionOperationEditor.tsx:972-973/:1088-1091/:1203-1204/:1277-1278, ConstructionReshapeEditor.tsx:398-406/:2811, useConstructionLifecycle.ts:40/:202/:383) with TWO EXPAND-family UX proposals booked as PROPOSALS not defects (silent-empty positive row; hidden-rejected-fields per-field reasonCodes), **operator triage** owed.

- **Worklist (14-col re-scanned before/after; per-row tab profile unchanged; row 97 + OPS rows byte-identical):** row 96 `next_action` += `journey authored pending live window (7fcb640f)`; all other rows untouched. Row-1/tally interpretation disclosed: no ledger-count tally line exists in the TSV; the record-file tally lines (this §6, the digest part-2 row, ledger status_updates.round64_part2) carry the new count 78.

- Part-2 sole-writer scope: the same three publication-bundle record files + `docs/BUILDER_WORKLIST.tsv`; `docs/BUILDER_VERIFICATION.tsv` **file content** untouched by this writer (read-only pin @`7fcb640f`, parent-committed by the journey lane); backlog doc untouched; nothing staged, committed, pushed, amended, or removed.

Record integrity tail (Round 64 part-2, written last): record-writer's ledger files at write time -- FINDINGS.md md5 `934365a2836a463110efe381974e3dec` **excluding this tail paragraph** (verify: truncate this file at chr(10)+'Record integrity tail (Round 64 part-2, written last):' and hash those exact bytes), OPERATOR_REVIEW_DIGEST.md md5 `fa0ca45f31d1c1501ec567b237bc151c` **excluding that file's Round-64-part-2 tail paragraph** (same truncation recipe), reviewed-sources.json sha256 `5ba1f4b6ff6d28bd84e0342d6791664cd6bc970f597248bf89c71d25441c1bb9` (full file, round=64, canonical roundtrip BYTE-IDENTICAL -- SELF-RUN). My-run evidence: /tmp/opencode/records-round64b.md receipt + r62_audit.py sweep **records=78 match=78 drift=0 annot=19 self=2** run against parent HEAD 3cfec1b7 (detached worktree /tmp/opencode/wt-r64b + these final four record files) by /tmp/opencode/build_r64b.py; all 7 new pins script-derived live==cited-blob==blob@HEAD==on-disk@HEAD. Honest labels: journey AUTHORED pending live window (JOURNEY_OK + WILL-FLIP re-asserts + row 279 STILL OWED; probe-kind coverage block status untested); the assignment's expected registry re-pin was verified NOT to exist in part-1 -- first standalone pin disclosed honestly; 403-masking CLOSED with REQUEST-CHANGES->PASS-after-repair arc (same-model disclosed); UI clause MET-BY-CODE with 2 EXPAND-family proposals to operator. Sole-writer respected; BUILDER_VERIFICATION.tsv content untouched (read-only pin); nothing staged, committed, pushed, amended, or git rm'd.

## Round 65 -- discovery-pass C1-C5 disposition; C1 `8459f080` + C2 `87ee09c7` folds; formal re-sync `49d6999e`


Three parent-committed product/formal commits folded; bodies read in full. All 11 touched ledger keys re-derived script-side: live sha256 == `git show <pin>:<path>` == blob@HEAD == on-disk@HEAD in a disposable detached worktree of `49d6999e` (`/tmp/opencode/wt-r65`; zero hand-typed digests).


### 1. Discovery-pass disposition (`/tmp/opencode/sidefind-next3.md` ranked pass)


| rank | candidate | disposition | evidence |

|---|---|---|---|

| C1 | DERIVE/UNPIVOT SET re-imports cleared foreign-owner inputs | **FIXED @`8459f080`** | RED-first x2 durables (`filter_source_pivot_clear_test.go` :1294/:1345); +2/-2 per lane mirroring pivot :591-592/filter :265-268; reviewer: "Worker mutation claims reproduced exactly, including the partial-chain pre/post disclosure." (`c1-review.md`; C1-C4 + T1/T2/T3 survive) |

| C2 | lease-lost demotion stamps phantom failure trio on healthy outputs (both transports) | **FIXED @`87ee09c7`** | one reader hunk + two openapi hunks; gate only demotes `errors.Is(lease.ErrLeaseLost)` FAILED/MISSING_REVISION shape-6; reviewer direct oracle runs: R20 4/4 + "R20B VERDICT \| PASS \| 64/64 comparisons MATCH the twin oracle"; genuine-direction M1/M2 mutants each kill every cell (`c2-review.md`) |

| C3 | signed sets on non-matching step kind silently dropped (200, undecorated) | **NEEDS-OPERATOR-DECISION** | Arms ALIVE (`c3-reachability.md` Probes A/C/D named 422s; Probe B measured err==nil, PREVIEW_PENDING, zero diagnostics) -> `/tmp/opencode/operator-decision-digest-draft.md` section (g) "Signed-set on non-matching step kind -- silent 200 vs named refusal". SideFinding `row-source-derive-D10-dead-code` REFUTED. |

| C4 | isPopulated flags catalog-global? | **PREVENTED BY CODE, CLOSED from actionable queue** | builder.go:189-204 OR-merge = exact-resource-observation dedupe (input-scoped per AllGenerations semantics); per-generation isolation in the Arango bind (dc52ed0a precedent + durable all_generations fixture). Residual LOW: field-fact fan-out shares the resources.go family -- live two-generation witness optional. |

| C5 | prepare-lanes C1-gating surface unmodelled | **RESOLVED-VIA-MODEL-UPDATE @`49d6999e`** | PrepareLanes mapping re-pinned to guarded SET loops; clean 61,456 distinct verbatim; persist falsifier violates ClearedStaysCleared @9,236 = C1-revert falsifier (`formal-resync65.md`) |



**Second discovery pass — authoring-command idempotency/replay: NO BUG FOUND (PROTECTED-BY-CODE).** All 6 replay scenarios + batch atomicity protected; guards SELF-RUN spot-verified this round, not taken on report: replay arm `owner.LastAuthoringCommandID == request.CommandID` (internal/explorer/service.go:240, set :311, persisted arango/drafts.go:150), `ErrDraftConflict` version/digest guards (arango/drafts.go:52/:89/:96), SINGLE-CAS `WithTransaction` (arango/drafts.go:33) including the history-row insert (:49/:86), existing formal coverage internal/explorer/arango/draft_transaction_formal_replay_test.go. **Disclosed design note (not a defect):** ApplyCommandsRequest.Digest() excludes ExpectedDraftVersion (commands.go:591-597 marshals only semanticsVersion/snapshotToken/commands) — safe because the version-guard follows the replay arm. Evidence: /tmp/opencode/idem-probe-P1..P7 logs, 7/7 exit 0 at `87ee09c7` (disposable copy /tmp/opencode/idemprobe).

### 2. Formal re-sync honesty finding (quoted)


`/tmp/opencode/formal-resync65.md`: "The old (r64 / ebf97a40) CLEAN pass of ConstructionPrepareLanes was on a **stale description**: assumption A3 ... was false at the derive/unpivot sites pre-8459f080 -- the code persisted cleared foreign-owner re-imports, which is exactly the C1 defect". I.e. **the stale Clean pass described the C1 defect** -- a real-code bug fixed inside the window (introduced pre-`0dd7be44`, b72a1322-era lanes; repaired by `8459f080`), not verification drift; no bound weakened -- the model now describes BOTH shapes (clean = guarded, fault = unguarded). Parent arm re-derivations SELF-RUN: gated arm exit 0, "61456 distinct states found", own -metadir (`tlc-parent-gated.log` 22:37:48); falsifier arm exit 12 "Invariant ClearedStaysCleared is violated" @9,236 (`tlc-parent-persist.log` 22:37:49). 11/12 models IN SYNC; **C2 lease-lost promise remains unmodelled -- flagged, carried by the @`87ee09c7` durables**.


### 3. 403 sibling sweep (carried fact, honest attribution)


- **SELF-RUN spot-check (this round): correct polarity CONFIRMED at the cited sibling** -- `internal/server/construction_inputs_catalog.go` authorizedSnapshot 403 is gated by `errors.Is(err, authscope.ErrForbidden) || errors.Is(err, authscope.ErrUnauthenticated)` (:369-371) with backend-unavailable mapped earlier to `constructionInputsUnavailable` (503): no blind collapse.
- The repo-wide "**no other literal-403 blind-collapse site**" census is recorded as the **f403-lane sweep fact from the round-64 receipts, attributed and not re-derived here** (this writer enumerated literal-403 sites only; auditing every gate is outside this round). Flag, no verdict.


### 4. UI four-state + merged-tree validation carried in


- Row-96 **UI four-state is MET-BY-CODE** (r64 audit); LIVE window run remains the journey lane's -- runbook `/tmp/opencode/live-window-runbook.md`.
- Merged tree @`49d6999e`: 99-file offline Go suite ZERO failing files (`tests-sweep-failures.txt`, 0 bytes); UI vitest "Test Files 88 passed (88) / Tests 1166 passed (1166)" (22:35, `ui-vitest-merged.log`); openapi route-ownership + audit-http-500 checks green (`r20b-openapi-check.log`).


### 5. Ledger delta + honest counts


- **11 touched keys = +5 standalone NEW records, 6 re-pins/record-upgrades** (reviewRecords **78 -> 83**). The assignment's rough "+11 records" = touched keys; disclosed rather than inflated.
- Re-pins marked **NOT-a-duplicate** with supersession reasons: derive/unpivot @`d3a1d12a`->`8459f080`; reader/openapi **md5-era -> sha256 convention** @`87ee09c7` (Repair-6-era entries superseded incl. the r58 rerun note); TLA+SKILL @`ea6df079`->`49d6999e`.
- `filter_source_pivot_clear_test.go` standalone promotion disclosed (part-1 booked it inside the filter_source_test.go role; that file is byte-unchanged @`8459f080` and keeps its own record).
- twin_golden.json pair is **byte-identical across transports** (single sha, one entry per boundary, cross-noted).
- sideFindings **45 -> 48**: `twin-side-lease-lost-phantom` CLOSED @`87ee09c7`; `row-source-population-flags-catalog-global` CLOSED prevented-by-code (residual low recorded); `formals-U2-prepare-lanes-unmodeled` gap closed via `49d6999e`; +1 C1 found&fixed trace; +1 C3 operator-decision-pending pointing at digest section (g); +1 `authoring-command-idempotency-replay` **NO BUG FOUND closed-on-record** (second discovery pass: PROTECTED-BY-CODE + disclosed design note; guards SELF-RUN-verified).
- Post-write audit SELF-RUN @parent HEAD `49d6999e`: publication-bundle ledger **83 records / 83 match / 0 drift** (19 annotation-only, 2 self pre-tail); canonical JSON roundtrip BYTE-IDENTICAL; 3-way agreement at 83 (audit == this §5 == digest Round-65 row).
- Sole-writer respected: WORKLIST ONLY row 96 **evidence column** += formal-resync refs (journey column untouched); `docs/BUILDER_VERIFICATION.tsv` excluded (journey lane); nothing staged, committed, pushed, amended, or removed.



Record integrity tail (Round 65, written last): record-writer's ledger files at write time -- FINDINGS.md md5 `4d8fc63fef84ed7add65557959509831` **excluding this tail paragraph** (verify: truncate this file at chr(10)+'Record integrity tail (Round 65, written last):' and hash those exact bytes), OPERATOR_REVIEW_DIGEST.md md5 `0b0bf2613fcda2476f8f21f7d28e8e62` **excluding that file's Round-65 tail paragraph** (same truncation recipe), reviewed-sources.json sha256 `e2760999bdc074482bcd6c7977b2c71dba15806b33aa163663dd74136657aab6` (full file, round=65, canonical roundtrip BYTE-IDENTICAL -- SELF-RUN). My-run evidence: /tmp/opencode/records-round65.md receipt + r62_audit.py sweep **records=83 match=83 drift=0 annot=19 self=2** run against parent HEAD 49d6999e (detached worktree /tmp/opencode/wt-r65 + these final four record files) by /tmp/opencode/build_r65.py; all 11 touched pins script-derived live==cited-blob==blob@HEAD==on-disk@HEAD. Honest labels: C1/C2 FIXED+CLOSED; C3 NEEDS-OPERATOR-DECISION (digest section (g)); C4 PREVENTED-BY-CODE (low residual, optional witness); C5 resolved-via-model-update; A3 stale-clean-pass finding quoted honestly; 403 census beyond the SELF-RUN-confirmed sibling is an attributed f403-lane sweep fact (flagged); the assignment rough "+11 records" booked honestly as 5 new + 6 re-pins/record-upgrades. Sole-writer respected; BUILDER_VERIFICATION.tsv excluded; nothing staged, committed, pushed, amended, or git rm'd.

## Round 66 (parent/journey lane) — FORMAL-62-U1 replay closure: projection-owner-branch replay family + Node contract + nine falsification arms

- LANDED: `internal/explorer/authoringv2/construction_projection_replay_test.go` (16 branches: 5 accepts through the public `Construction.Validate`, 11 refusals asserting exact reject strings; nil-payload and non-owner-switch isolations pin strings via the direct validator entry, disclosed in the file header); `scripts/lib/formal-projection-replay.mjs` (batch lane mirroring the expectation table + nine source-fault falsification pairs — pivot/group consumption, filter column, derive operand, unpivot nil-payload, unpivot melt-membership, owner-required-id, owner-exists, carry non-carrier, carry typed-output — each firing exactly its named branch with positive+negative+restored runs green); `scripts/verify-ui/contracts/source-projection-contracts.mjs` + `.test.mjs` (4 tests incl. tampered-observation self-validation).
- Round 61 §1 closure conditions now met on the replay side: replay case families + Node contract shipped; ledger re-pins for the four new files land in the follow-up ledger commit (cf0ca558 precedent: code commit first, pins re-derived after).
- FORMAL-62-U1 worklist row flips to delivered-certified with this commit; the model side stays as certified in Round 63 (cf0ca558) — this unit closes the replay half only.
- U2 not part of this unit: the prepare-lane check-debt U2 was CLOSED by ConstructionPrepareLanes.tla @feb663a7 (Round 62); the standing falsification-pair discipline note for any new prepare check remains in force.
- Evidence: `/tmp/opencode/jrev/projection-lane-dbg2/replay.json` — branches 16, accepted 5, rejected 11; 9/9 arms positive&&negative&&restored with firedBranch==branch. `go test ./internal/explorer/authoringv2` green (incl. all pre-existing projection tests), `go vet` clean, `node --test` contracts 4/4 + unpivot-contracts 8/8 + verification-runner 11/11. ConstructionChain lane hash-set provably unaffected (no edits under those inputs).
- Records hygiene: compact parent-lane section appended after the Round-65 record-writer tails (tails unmodified); `reviewed-sources.json` untouched in this commit by design; operator-owned in-flight files (`scripts/verify-ui/run-checks.mjs`, `verification-plan.mjs`, contracts pair, workflow extraction) untouched.
## Round 67 — CI-62-01 delivered: scripts/ci/vet.mjs boundary gate + internal/ci durable checks (authorized option)

- Delivered: `scripts/ci/vet.mjs` runs the CI vet boundary (`go vet ./...` full
  set + explicit `go vet -copylocks ./...`). Full-repo EXIT 0 at HEAD (the
  copylocks class itself fixed earlier by `26bb172c`/`7e9d1449`). Proved by
  run: plain full `go vet` (default set) already includes copylocks — the
  shipped-green cause was that `go test` vets only its fixed subset and no
  step anywhere ran the full vet (zero-vet-step census).
- Self-audit found + fixed a real wiring defect BEFORE commit: the GitHub
  `node-unit` job installs NO Go toolchain, so a node-side durable test
  spawning `go vet` would fail CI while passing locally. Durable checks live
  instead in `internal/ci/vet_gate_test.go`, auto-enrolled in the `go-unit`
  job's `./internal/...` sweep (workflow lines confirmed read-only):
  repo-wide exit-0 gate; overlay-injected copylocks fault reddens vet naming
  `copies lock value` + probe file; `-copylocks=false` class-identification
  control passes the same fault; disk purity (overlay virtual, zero repo
  writes); node-CLI smoke of the gate script. `scripts/ci/vet.test.mjs` was
  deliberately NOT kept (node shard toolchain gap).
- Adversarial review ses_e6083f34 @f0b33137 (staged node-set): PASS; F1
  LOW reproduced (`--packages=-all=false,.` printed VET-GATE PASS with zero
  coverage) and fixed with a dash-guard on packagePatterns — now exit 1
  fail-closed, with a black-box smoke arm asserting the escape is rejected;
  F2 doc-precision (only the enable analyzer form substitutes defaults) fixed
  in the vet.mjs header comment. Reviewer independently reproduced the
  go-test-subset miss (`go test -run zzzNoSuchTest` exit 0 on a lock fault)
  and caught the fault with a sync.WaitGroup variant of their own design.
- NEW confirmed verification-hygiene defect this round (queued, not yet
  repaired): repo-wide `gofmt -l` is RED on 9 files (e.g.
  `internal/dataframe/semantic/recipe_plan_output.go` alignment drift;
  full HEAD baseline /tmp/opencode/jrev/gofmt-head-f0b33137.txt = 9 entries; wave-scope repair list /tmp/opencode/jrev/gofmt-list.txt = 8) — drift accumulates because
  no gate enforces formatting, same enforcement-gap shape as this row's vet
  class. Unit's own replay test file was fixed to gofmt-clean in this wave.
- Boundary kept honest: Makefile-prerequisite / tests-workflow options remain
  operator-scope and untouched; row-13 acceptance (exit-0 gate wired +
  deliberately-broken copy fails it) satisfied via the authorized
  scripts/ci/vet.mjs option, now CI-enrolled through the go-unit sweep.
- Evidence: `/tmp/opencode/jrev/vet-gate-evidence.log` plus the internal/ci
  go-test run quoted below (per-test timings).

## Round 68 — gofmt enforcement-gap class: pre-existing drift repaired (AST-preserving canonicalization)

- Discovery: first-ever repo-wide `gofmt -l internal cmd` sweep (this program)
  was RED on 9 committed files (HEAD baseline `/tmp/opencode/jrev/gofmt-head-f0b33137.txt`); 8 are this row's scope (`/tmp/opencode/jrev/gofmt-list.txt`) and the 9th (`construction_projection_replay_test.go`) is the unit's own Round-66 file fixed alongside it in commit-1; 4 of the 8 are
  non-test (`recipe_plan_output.go`, `row_definition.go`, `row_proposal.go`,
  `cmd/dataframe-query/main.go`). Nothing in any gate runs gofmt,
  so drift accumulates — same zero-enforcement shape as the CI-62-01 vet class.
- Repair: `gofmt -w` over exactly the captured list. Proof of no semantic
  change: parser→printer AST dumps (comments excluded) of the HEAD blob vs the
  worktree file are byte-identical for all 8 (`/tmp/opencode/jrev/astcmp/`,
  tool build failure correctly fail-closed as DIVERGED during development —
  disclosed); `git diff -w` additionally empty for 6/8; the 2 remainders are
  gofmt's own AST-preserving normalizations (single-line struct body expanded;
  comment-hygiene `//`). `gofmt -l internal cmd` empty
  after; `go build ./...` green; affected unit packages re-run
  (optimize, semantic, lifecycle 11.2s, server conversion test, authoringv2
  replay family); e2e-named packages (`pinblock_reconcile_e2e`,
  `cross_generation_publish_e2e`) compile-verified via `-run '^$'` —
  formatting-only change, full service-backed e2e not re-run for whitespace
  (disclosed scope limit).
- Follow-up recorded as BS-67-1 next_action: permanent `gofmt -l` empty-output
  assertion belongs in the internal/ci CI-boundary suite (deferred to the next
  wave touching that package to keep unit commits coherent).

## Round 71 — F-B25 sanitize-key collision hypothesis CLOSED: prevented by identified code

- The carried F-B25 hypothesis (silent lost acknowledged writes via
  non-injective `SanitizeKey` + `on_duplicate=replace` vertex upserts) had a
  skip-gated live probe (`internal/ingest/keycollide_generated_e2e_test.go`)
  never recorded in any ledger. Ran it for real against the owned dev-stack
  Arango in-container (house recipe, `LOOM_TEST_ARANGO_URL=http://arangodb:8529`,
  fresh `loom_verify_kcoll_*` dbs, LIFO drop cleanup): PASS both paths
  (Generated 0.61s, Generic 0.24s), verdict line `PREVENTED by validation`,
  probe premise (SanitizeKey non-injective) still asserted in-process, control
  id persisted (acked == collected).
- Mechanism proved by reading the actual repo schema
  (`schemas/graph-fhir.json` `Patient.id` pattern `^[A-Za-z0-9\-.]+$`): the
  validation charset is a strict subset of `validKeyPart`'s KEEP-set
  (`[^A-Za-z0-9_\-:.@()+,=;$!*'%]`), so `SanitizeKey` is the IDENTITY map on
  every schema-valid id → distinct valid ids cannot collide. The original
  probe pair `coll/1` vs `coll_1` fails schema on both arms — correct
  pre-write rejection, not accident (the probe's own classifier printed the
  row_errors and only accepted the PREVENTED verdict on that evidence).
- Residual disclosed: remaining `SanitizeKey` callers audited —
  documents.go:57/:136 (schema-validated id + whitelist resourceType),
  :97 edge key derived from BuildEdgesWithID over validated payloads (code
  reading, not separately probe-adversarialized); generated_load.go:90 objectID
  same fast-validated path. No product-code change required; probe test stays
  skip-gated for CI and remains the durable reproduction harness.
- Evidence: probe output /tmp/opencode/jrev/fb25-live-run-r71.log; schema
  pattern + identity-set inclusion computed this round.

## Round 72 — F-B17 sanctioned red re-verified live; manifest CAS read-back confirms the gap

- Per the maintain rule (freshness, not re-discovery): re-ran the sanctioned
  `TestGenerationActivationBlindlyFlipsSharedPointerAgainstArango` against the
  owned dev-stack Arango in-container. Result VERBATIM-CURRENT: `--- FAIL`
  (blind cross-generation flip reproduced; quoted shipped `activateAQL` shows
  the same no-value-compare patch; `GAC_DATABASE_REMOVED` cleanup logged).
  Log `/tmp/opencode/jrev/fb17-red-rerun-r72.log`.
- Independent source read this round agrees with the ledger: `activateAQL`
  (:318-337) filters only candidate validity + active existence/recordType/
  project; no FILTER on the pointer's current dataset/manifestKey, no
  expected-revision bind, and `ignoreRevs:false` conditions on a body-carrying
  _rev only within the single statement — the Go read-then-act window
  (:198-204 idempotency pre-check round trip) is unprotected. Fix-shape model
  `GenerationActivationCas.tla` (expected-value CAS arm clean) stands as the
  validated remedy; repair remains the operator's queued policy decision
  (winner/demote semantics for concurrent activations) — no unilateral fix
  without the promised-behavior decision (no-redefinition rule).
- Class read of neighboring guards (not previously itemized): the release
  path (`release_store.go` expected_revision) and janitor/reconcile (Round 70)
  both DO carry value-compare CAS; generation activation is the sole
  pointer-flip site lacking one — scope of the open defect precisely bounded.

## Round 73 addendum — backfilled Round 69/70 discovery records (durability repair of the ledger itself)

Discovery passes 69 and 70 were executed with their probe artifacts under
`/tmp/opencode/jrev/` but only checkpointed to Goal state, not transcribed
into this ledger. Backfilled verbatim in intent so the durable record matches
what actually ran (evidence paths retained on the same host):

- **Round 69 — persisted-failure retryability surface** (`retryability-audit-r69.md`).
  Empirical probe matrix over persisted failure shapes (blank/whitespace/garbage
  raw text, whitelist code + persisted `retryable=false`). Classifications:
  blank-raw shape INCONCLUSIVE/hardening-candidate (no production writer can
  produce it); whitelist-code echo PREVENTED by identified code (single
  normalized persist site, 16+ producers use `WithRetryable(true)`, OR-guarded
  readers, drift class closed under ARC-D11 completeness tests);
  `errorOptions.retryable` plain-bool default convention-guarded only —
  disclosed residual, no compile-time enforcement. No product bug; no code
  changed.
- **Round 70 — publication janitor / reconcile cleanup surface**
  (`janitor-audit-r70.md`). Read-through of `reconcileExecution` janitor arm
  (`bundle_store.go:1092-1223`) against claimed guarantees: pre-claim filters
  (pointer-names-exec / active-binding skip, read-error veto, wiring-guarded
  enumeration) and post-claim destruction gate (fresh pointer + fresh
  active-release binding scan immediately before DropTable; every veto path
  releases the lease; joined fail-close on partial DropTable failure) match
  `ReconcileDangleTrust.tla` / `PointerOrphanLifecycle.tla` validated shapes.
  Reconcile-failure marks pinned by durable tests. Verdict: no defect
  candidate; coverage disclosure: concurrent multi-reconciler lease
  interleavings rest on earlier TLA rounds + lease-expiry e2e, not re-run.
- **Round 73 companion observation (manifest lifecycle read side).**
  `ReadActiveManifest`/`readActiveAQL` (`manifest_lifecycle.go:159-181, 298-316`):
  inner FOR is an inner join on the manifest doc with recordType/dataset/state
  filters and a LIMIT-2 duplicate guard; a pointer whose manifest doc is gone
  resolves to `ErrNoActiveGeneration` (fail-closed to no-data, never a stale
  read, never a 500). The dangling-pointer state itself is unreachable by
  identified code: repo-wide there is **no** manifest/active-document deleter
  (no REMOVE/DocumentRemove capability in product code; no
  DeleteManifest/PruneGeneration API). `ResolveActive` wrapper
  (`active_resolver.go`) guard chain verified sentinel-preserving, project-
  mismatch and non-staged both fail closed. PREVENTED by identified code.

## Round 75 — failed-generation-load residue (H-R75) CLOSED: prevented by identified code / designed immutability

- Hypothesis: a generation load failing mid-way poisons `(project, generation)`
  permanently — reuse gate refuses re-load (`runGeneration` :155-156 "already
  exists in state X"), state machine allows no FAILED->... transition
  (`manifest.go:44-51` Loading-only origin), `createManifestAQL` is
  insert-if-absent, and repo-wide NO product-code deleter exists (Round 73
  addendum), so the name could be neither retried nor reclaimed.
- Rollback first: the loader's defer (`generation_orchestration.go:179-194`,
  independent cleanup deadline) transitions a not-yet-staged manifest to
  FAILED on every failure path, and the reliability contract's promised
  invariant holds — "a failed generation load ... never changes the active
  pointer" (`docs/loom-reliability-contract.md:43-44`): the pointer is written
  only by Activate after STAGED, so half-loaded data is never served
  (`readActiveAQL` requires the pointer + staged/ready state).
- Promise establishment against contracts/docs/tests found NO retry-after-
  failure promise for immutable generations; the contract instead names
  immutability as the property (:19 "immutable FHIR generation") and
  explicitly assigns the mutable iteration loop to the operator CLI (:76 "The
  operator CLI retains both mutable local loading and immutable generation
  loading"). FAILED-as-tombstone is the coherent reading: re-loading content
  under the same immutable name would violate the recorded identity promise.
  Classification: PREVENTED by identified code / by design — no defect.
- Disclosed residual (hardening note, NOT registered as a defect — findings
  are evidence, not authorization): the collision refusal from :155-156 is an
  unmapped plain error, so clients see generic INTERNAL_ERROR with undefined
  retryability for what is an input-domain conflict; a structured 409-class
  mapping is a plausible UX improvement awaiting operator product decision
  alongside the queued response-drift items. Residual-reachability risk
  retained on record: a manifest stranded at LOADING is possible only if both
  the load AND the FAILED-transition cleanup fail (Arango down mid-defer);
  no janitor exists for the lifecycle collection — accepted under the same
  no-deleter-by-design regime, recorded for completeness.

## Round 76 — dataframe selector boundary pass: PREVENTED by identified code

- Contract promise (`docs/loom-reliability-contract.md` GraphQL section): reads
  require a *complete* selector; no `dataType`, materialization-ID, or default
  recipe resolution accepted.
- Verified enforcement: `schema.graphqls` `DataframeSelectorInput` is exactly
  three `String!` fields (no bypass inputs); all five reader entrypoints
  (rows x2, aggregates x2, export) route through the single `resolveSelector`
  / `resolvePublishedSelector` pair (`graph/dataframe/rows.go:78-101`) ->
  trim-then-`Valid()`, with `internal/dataset/dataframe_selector.go:16-31`
  rejecting blank AND untrimmed components (`INVALID_SELECTOR` with field
  path); `Key()` is length-prefixed (collision-safe per the F-B21 regime).
  Boundary-discipline shape: validation concentrated at the parse edge, shared
  by all consumers (publication, release activation, GraphQL, ETL per the
  file's own header). No defect candidate.
- Disclosed hardening note (NOT a registered defect): `convertFilters`
  (`rows.go` :103+) skips nil filter-list items (`continue`) instead of
  rejecting. Unreachable today — schema list is `[DataframeFilterInput!]`, so
  the executor rejects `[null]` at validation; if a future schema edit dropped
  the item non-null assertion, a dropped filter would silently WIDEN reads.
  Candidate: fail closed on nil items; queued with the response-drift/hardening
  notes, not implemented (no current promise violated).
- Process note (recurring agent-hazard, recorded so future readers trust
  quotes): this pass initially mis-rendered function names by passing
  `rg -rn` (the `-r n` replaces match text); authoritative names were
  re-derived with plain `rg -n` before any claim above.

## Round 73 — bulk-load multipart spillover /tmp leak CONFIRMED and REPAIRED

- **Defect**: `Handler.CreateDatasetGeneration` (`internal/api/bulk/load/
  operations.go`) parsed the multipart request with `body.ReadForm(32<<20)`.
  Go spools every byte past that budget to `os.TempDir` files, disposable only
  via `Form.RemoveAll`; nothing repo-wide called it. Every authenticated
  dataset-generation load over the budget permanently leaked whole-file
  copies in /tmp — disk-exhaustion/availability harm under normal ETL use.
- **Reproduction (durable regression, behavior-level)**:
  `internal/api/bulk/load/form_spillover_test.go` — TMPDIR redirected to
  `t.TempDir()`, two 20 MiB `.ndjson` files (40 MiB > 32 MiB budget) through
  the real handler with a loader fake. PRE-FIX: both arms `--- FAIL` listing
  the orphaned `multipart-*` entries; parent re-ran both arms verbatim.
  Reviewer re-derived independently at its snapshot and empirically pinned
  the orphan as Go's `multipart-<rand>` spillover at exactly part size
  (`count=1 totalBytes=20971520` — budget keeps one part in memory).
- **Repair**: single `defer form.RemoveAll()` registered immediately after the
  successful `ReadForm`; covers all post-parse exits. Both pre-defer returns
  are safe (nil body; ReadForm-error disposes its own spills — reviewer-proven
  on both real post-spill error arms). Comment records the placement hazard
  (`RemoveAll` on a nil `*multipart.Form` panics — parent-verified on
  go1.26.5, both host and container toolchains) so future edits don't hoist
  it above the error return.
- **Same-root audit** (reviewer-run + parent grep): `ReadForm` appears only
  at operations.go:41 repo-wide; zero `ParseMultipartForm`/`MultipartForm()`/
  `httputil` form-parse sites; remaining `mime/multipart` uses are client-side
  writers. Class closed.
- **Adversarial review**: worker `ses_e6069ffc…` (general subagent) PASS at
  exact snapshot (unit blobs hash-verified `e8013699`/`5d504a05`), all six
  attack surfaces run; two informational accuracy notes accepted and fixed
  pre-commit (orphan-count overstatement in the test header; nil-panic
  placement hazard documented). Report
  `/tmp/opencode/jrev/r73-bulkload-review-report.md`.
- **After/restored**: `go test ./internal/api/bulk/load -count=1` ok
  (pre-existing staging/authorization/error-mapping tests included),
  `gofmt -l` empty for the package.

## Round 77 — STALE-marking / release-metadata vertical: PREVENTED by identified code

- Contract sentence (`docs/loom-reliability-contract.md`): publications
  carried from the prior release are "marked STALE when their source
  generation is different".
- Observed (`internal/dataset/release.go`): both stamping arms are direct
  field comparisons — carry-forward `prior.Stale = prior.Generation !=
  ref.Generation` (:221) and fresh-binding `Stale: result.Generation !=
  generation` (:271) — no substring/time/pointer proxies. Optional bindings
  only attach after a successful `VerifyPublication` returning PUBLISHED,
  Queryable, exact selector AND exact generation (:212); carried entries never
  masquerade as required (`Required=false` forced at :220).
- Durable coverage confirmed: `release_test.go` pins the cross-generation
  carry-as-stale arm (`TestReleaseActivationCarriesOptionalPublicationAsStale`
  :139-166 asserts Stale=true + ExecutionID preserved and required arm
  Stale=false) and same-generation fresh arms (`:59`, `:220`).
- Adjacent maint-check: `publicationVerificationStore`
  (`internal/server/release_verifier.go`) derives EVERY returned field
  (State/Queryable/Generation/ExecutionID/PhysicalTable/VerifiedAt) from the
  catalog-observed execution via `ExactExecutionCatalog.FindExecutionBySelector`,
  never from caller echoes — the recorded F-B6 "PASS-with-CONFIRMED-pattern"
  shape holds at current source. Classification: no defect candidate on this
  vertical; disclosed nuance (unchanged from ledger): optional-prior carry
  with same-generation execution turnover keeps Stale=false by the generation
  rule — same identity class already adjudicated under the exact-selector
  regime, not re-opened.

## Round 78 — discovery-bridge fail-open posture divergence REPAIRED (hardening class); sibling note DISPROVEN

- **Finding**: with `schema.Scope.AuthScopeMode` outside `{unrestricted,
  restricted}` (including the empty string), `FieldDiscovery` left
  `AuthResourcePathsUnrestricted` nil, and the catalog resolves nil through
  its legacy rule `EffectiveAuthResourcePathsUnrestricted(nil, nil) == true`
  — the AQL unrestricted bind var (`arango/fields.go:18,63`) — i.e. fail-
  OPEN, while `authscope.ReadScope.Unrestricted()`/`CoversExecutionPaths`
  fail CLOSED on unknown modes (`unknown-mode-direct-denies` pin). One
  persisted scope value, two consumers, opposite fail postures.
- **Reachability adjudicated BEFORE classing**: `scope_resolver` produces
  only the two legal enums; reviewer hunt (independent) confirmed no
  product-writable path persists an unknown mode into `schema.Scope` (two
  build sites; no JSON unmarshal of RuntimeBindings/workspace drafts;
  persisted readers re-validate via `persistedMaterializationScope` default
  -> error). Classified HARDENING (drift/corruption-only exposure), repaired
  to match the pinned Covers promise; catalog's compatibility rule untouched
  for legitimate legacy callers.
- **Repair + durable regression**: default arm -> explicit restricted with
  fail-posture comment; `discovery_auth_mode_test.go` five-mode matrix (incl.
  `""`, wrong-case) asserting non-nil pointer, intended posture, Effective()-
  companion equality, and Covers-agreement arm (also supplies the repo-level
  `Unknown mode == false` pin the reviewer flagged missing). Pre-fix RED
  re-derived at parent (HEAD-file swap: both arms FAIL verbatim) AND
  independently by reviewer in a scratch replica (`R78-PROBE-HEAD ... nil =
  true`, EXIT=1 red-prefix.log / EXIT=0 green-postfix.log).
- **Adversarial review**: worker `ses_e605e21f7ffe` **PASS** — nil-pointer/
  fail-open both sides confirmed run-reproduced; product-writer hunt found
  NO severity lift; five-mode map + unrestricted-not-over-tightened
  (construction-inputs, explorer capability, catalog, catalog/arango,
  graphql-query suites green); sole nil-capable producer audit (GraphQL's 4
  sites always explicit). Report
  `/tmp/opencode/jrev/r78-failopen-review-report.md`. Label: same-model
  worker, disclosed.
- **Sibling note DISPROVEN by parent run**: reviewer recommended matching
  the "fail-open family" at `internal/dataframe/compiler/lower/auth_scope.go`
  — direct read shows that function already has an explicit
  `default: return false` fail-closed arm; its `case "": len(paths)==0` is a
  documented internal legacy spelling ("resolved request scopes are
  authoritative"), not a defect. No change; note retained to show it was
  checked.
- Adjacency insurance at parent (post-change, pre-commit):
  `internal/catalog` (107s), `internal/authscope`, `dataframe/recipe/schema`
  packages all ok.
