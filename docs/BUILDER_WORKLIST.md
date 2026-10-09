# Builder worklist

[BUILDER_WORKLIST.tsv](BUILDER_WORKLIST.tsv) is the active queue. Each row has one stable ID, exact case or feature, next action, owner, prerequisite, evidence reference, implementation batch and browser batch. This queue is not a list of confirmed bugs. The replacement goal is active and defined in [BUILDER_GOAL.md](BUILDER_GOAL.md). Initial reconciliation kept five exact accepted lifecycles closed and separated metadata corrections from browser work.

[BUILDER_VERIFICATION.tsv](BUILDER_VERIFICATION.tsv) retains historical runs and observations. [EXPLORER_BACKLOG.md](EXPLORER_BACKLOG.md) retains product rationale. [BUILDER_OPEN_TRANSITIONS.md](BUILDER_OPEN_TRANSITIONS.md) retains investigation history. Their dated priorities do not schedule current work. The verifier registry remains the authority for executable case names and required checks; this queue schedules those contracts without duplicating them.

## Read the states

- `evidence-review`: a previous pass is claimed. Review exact evidence and relevant source changes. Reuse valid scoped proof; rerun only for a concrete missing check or changed behavior.
- `metadata-review`: retained behavior proof exists, but its contract, identity or evidence mapping needs reconciliation. Do not rerun solely for bookkeeping.
- `evidence-unmapped`: a report has not been reliably matched to this exact case.
- `not-verified`: the registered path lacks a verified lifecycle.
- `incomplete-lifecycle`: a named behavior, request, identity or timing proof is missing.
- `harness-failure`: the driver failed before it could establish the feature's behavior.
- `implementation-ready`: an isolated patch and its focused checks are ready for consolidated review; full browser acceptance remains pending.
- `implementation-active`: an assigned owner is preparing the bounded correction or exact case readiness in isolation. This is not a product or QA pass.
- `ready-implementation`: a bounded correction is identified but unfinished.
- `ready-integration-checks`: staged changes exist; combined checks and review remain.
- `fixture-investigation`: the required data shape was unavailable within a bounded search. This is neither a product failure nor proof of global absence.
- `missing-contract`: an existing visible feature has no exact registered lifecycle contract.
- `inventory-audit`: visible features and transition classes still need reconciliation with the registry.
- `closed`: the exact scoped lifecycle and applicable source evidence have been reviewed. Keep its evidence and scope; do not silently expand its claim.

Do not promote an evidence claim to `closed` from a registry status, a historical summary's green label, or an aggregate test count. Keep correctness, persistence, usability, timing and identity limitations explicit.

## Work in batches

For the current batch, finish all ready independent implementation first, then
conduct consolidated team and foreground review, then full browser QA. Cheap
faithful regressions belong to implementation. Do not interleave full browser
runs with each small patch. Explicit dependencies and unavailable witnesses
remain a separate blocked list; they do not hold all ready work indefinitely.

The current phase is the next correction batch. The first frozen QA wave at commit `58939856914c32cd002c5fa1fd6dcf07a4c9bc32` is terminal, and the freeze is released. Six additional scoped lifecycles were accepted: basic Group, legacy rows and positive Medication fixture; real-CDA mapped-member removal, Presence including an empty group and published Append. Eleven queue rows are now scoped closed, including the five preserved prior passes. Basic fixture passes do not establish CDA behavior.

Remaining wave findings are driver chronology/source/selector errors (Suggestions, coded source and Patient ONE), eight focused-plan metadata failures, and a bounded nonempty-zero-match Presence witness gap. Owners prepare cheap faithful isolated corrections, leads consolidate review, then the affected complete cases rerun against the next frozen checkpoint. Unchanged accepted evidence is reused.

Ready source corrections: the canonical gate now validates the real focused plans (nine planner tests, 39 valid registered plans); Patient ONE uses the native sibling Summary selector; the runner review packet clears a stale fallback reason only for clean passing reports (three focused regressions pass). The coded-source fixture correction is accepted with five focused tests, exact six-row source inputs and watched fixture fingerprint coverage. Suggestions chronology remains in correction after review found a successful-path lexical-scope failure. The broader runner test file has an unchanged Root Quantity retained-check-name mismatch; this packet does not claim a full-suite pass.


1. Assign independent rows to separate worktrees. Use at most three teams under AGENTS.md. Each issue has one implementation owner and one lead reviewer. Owners run cheap faithful regressions while implementing; they do not wait for the full browser suite.
2. Finish and review all ready corrections in the bounded implementation batch. Merge reviewed patches serially into the agreed integration branch, preserving unrelated changes. Resolve overlapping registry hunks through one merge owner.
3. Freeze the integrated source once. Before launching concurrently, explicitly record each case's disposable Explorer or project, artifact directory, target, fixture setup and cleanup effects. A case with unknown shared-state effects is not parallel-ready.
4. Run independent full native cases concurrently against that frozen source. Do not mutate watched files, shared source data, publication targets or common Explorers. Cases sharing mutable state run in dependency order. HMR/rebuild probes run alone.
5. Retain first-failure diagnostics and split product, harness, fixture and environment failures. Prepare the next correction batch against cheap regressions. Rerun affected full cases after integration; do not repeat unchanged passing cases without a concrete shared risk.
6. Concurrent latency measurements are provisional. Confirm budget failures serially on the same data and stable source. Keep the five-second default and only the existing approved ten-second Pivot exceptions.

The previous one-case-at-a-time integration priority is replaced by this bounded batch sequence. Cheap checks remain continuous. Full browser verification follows the reviewed integrated batch. Do not attempt every pending case in one uncontrolled run.

## Batch plan

| Batch | Work | Exit condition |
| --- | --- | --- |
| R0 | Reconcile remaining claimed or unmapped evidence, metadata gaps and the goal-scope inventory. Exact accepted cases are already marked closed. This starts with read-only evidence work. | Each row is closed with explicit scope, or has one concrete missing deliverable and an implementation/test batch. No speculative browser reruns. |
| B1 | Finish presence chooser scoping, collection-removal persistence/timing evidence, and missing-component Group evidence/binding. | Combined focused checks and lead/root review pass; then all three exact native lifecycles run against one frozen checkpoint with isolated QA state. |
| B2 | Remaining repeated/related and source transition gaps after evidence reconciliation. | Bounded ready cases have independent oracles, complete contracts and current lifecycle evidence. |
| B3 | Remaining Combine/Append and basic row-operation lifecycle gaps after evidence reconciliation. | Exact selection-to-table paths, saved edits, removal/restoration and reload are proven for each selected scope. |
| C1 | Six explicitly unmapped feature contracts. Reuse existing workflows before writing new ones. | Exact registered cases and cheap prerequisites exist; schedule browser cases in waves of at most ten new exploratory sequences. |
| F1 | Bounded fixture gaps, including the separate literal empty-array witness. | Find a valid scoped witness or retain a precise unresolved data requirement; do not launch known-unreachable browser paths. |

B2/B3 are candidate queues, not launch approval. R0 can remove redundant work or identify a missing product contract. Dependency edges and mutable-state ownership determine actual concurrent test groups. Preserve the full goal inventory even when a case waits for data or a decision.

## Current batch handoffs

- Team A: `/private/tmp/loom-teamA-final.diff`, SHA-256 `ca9d07d0ff2fc16ad30578dd1d0dc747931259aae430885ff60ac45a64f78ce8`. Presence selector, Suggestions retry/native Add, coded-column lifecycle, Group/legacy/Patient ONE rendered-row corrections and contracts passed focused checks and source review. Full browser QA remains pending.
- Team B: `/private/tmp/loom-teamB-batch-final.patch`, SHA-256 `891aad2760fad2a4228342412a48b105a946c30fda40b28a04bf8ea9ae992623`. Collection-removal checkpoint retention, zero-match ONE/ALL route matching, EXISTS/EQUALS contracts passed 31 focused tests and source review. Distinct-status witnesses remain bounded-search gated; full browser QA remains pending.
- Team C: isolated assembly at `/private/tmp/loom-teamc-integrated-fd14`. Repeated populated/missing contracts, coded Pivot corrections, Medication and published Append readiness passed lead review and focused checks. Literal empty-array remains WITNESS-001. The combined owner reconciles the registry and named coverage gaps before producing the final artifact.
- The canonical gate is `node scripts/maintenance/playwright/check-native-playwright.mjs`. The isolated combined candidate passes 74/74 mappings; coverage checks pass 41/41. Explicit reshape registrations and self-contained DiagnosticReport Append callbacks resolve the retained six gate findings. Exact coded Pivot discovery lists two tests. These source checks do not establish browser lifecycle acceptance.
- Read-only runtime preflight passed on the configured owned stack: fresh API build stamp, matching mounts/Compose ownership, API `/readyz` and UI document HTTP 200 in three samples. Build identity remained stable. This establishes stack readiness only; runtime CDA identity/witness availability still requires each case’s independent oracle. Recheck freshness after promotion before browser launch.
- Full visible-surface source audit is retained in [BUILDER_SURFACE_INVENTORY.md](BUILDER_SURFACE_INVENTORY.md). Its snapshot precedes combined contract additions; registration does not close lifecycle acceptance.

## Reconciliation notes

The initial queue contains 75 entries: 67 registered-case rows, six feature-contract gaps, one literal empty-array witness gap and one full-goal inventory audit. Current contract additions reconcile with those existing gap rows; use the combined registry for executable counts. Five scoped accepted lifecycles are closed. This is not a count of bugs or proof that all goal features have been inventoried.

Do not rerun Combine Join or derived-Append just to chase earlier unexplained aborted requests. Their next action is attribution from retained request evidence. Keep the later successful runs as non-reproduction evidence. The zero-match EXCLUDE case and Patient membership handoff need metadata/evidence reconciliation rather than another browser run for missing generic timing fields.

The old runner inventory snapshot has 22 cases and is stale. The initial queue captured 67 registered cases; this batch adds contracts. Use the combined live registry for the current executable case count, and reconcile new contracts with their existing GAP rows. Basic control passes do not establish CDA behavior, and API export tests do not establish a native Viewer download lifecycle. Keep those scope questions in the inventory audit.

## Updating the queue

Update the existing row after a result; do not append another task for the same exact case. Add a new row only for a distinct contract or behavior class. Record reviewed source applicability and exact evidence paths. A runnable command does not prove runtime readiness, data independence or parallel safety. Keep basic fixture results separate from real CDA acceptance.

## Ready correction checkpoint

Foreground accepted 14 exact owned paths for promotion, recorded in `/private/tmp/loom-root-ready-batch-manifest.json` against commit `58939856914c32cd002c5fa1fd6dcf07a4c9bc32`. The isolated root-quantity overlay is excluded. Team C owns scoped promotion preserving unrelated dirty work. This is source acceptance, not browser proof.

Next frozen wave: coded-source, Patient ONE, missing and populated repeated rows, integer and string coded Pivot, contributor EXISTS and EQUALS, zero-Observation ONE/ALL, and Suggestions if ready (otherwise starting-collection repair CASE-033). At most ten cases. Each case requires distinct QA identities and artifacts; concurrent timing misses require serial confirmation. CASE-033 historical proof has a concrete changed root predicate paging applicability risk. CASE-054 retained historical proof is not current aggregate-fingerprint proof.

## Completed 58c9 QA wave

The wave at `58c9cae14feef5f9078cfb8a253b505334625708` is terminal; freeze released after matching source/docs/API identities, mount validation and three final healthy samples. CASE-035 missing-component composition (17/17, 1416ms maximum rendered checkpoint) and CASE-060 Contributor EXISTS (16/16, 2083ms maximum settled action) are accepted scoped lifecycles. Thirteen of the 75 queue rows are now scoped closed. Literal empty component arrays remain separate.

Next corrections: Suggestions error-state ownership, Patient ONE sibling preview reader, coded direct-source discovery, canonical created Explorer ID for zero ONE/ALL, EQUALS valid report dimension, populated repeated-row terminal request capture, and stale coded Pivot registration test. No late test failure is silently classified as a product failure or waived. Owners implement in isolated worktrees before consolidated review and the next frozen QA wave.

## Next ready implementation batch

Team B integrated source `a2ff608ae5d4a63353d46ff43109d4498ee284d9` is accepted: six nonoverlapping files match the reviewed worker artifacts byte-for-byte; EXISTS 5/5, EQUALS 4/4 and created-Explorer scope 2/2 focused checks pass. Patient ONE sibling result reader (2/2), exact coded Height/Quantity option selector (1/1), and coded Pivot registration regression (8/8) are also accepted for composition. Full affected browser lifecycles remain pending.

Team C owns the combined promotion and the one registry update adding the new coded-choice regression. Its recorder/fixture terminal-drain correction and separate runner-summary projections remain in implementation. Suggestions needs bounded sanitized request/fault chronology evidence; it does not hold the ready batch indefinitely. All workers keep unfinished source in separate worktrees.

## Latest checkpoint: 38c33ccaf

The frozen wave is terminal and released after matching 1,706-file source fingerprints, API identity, mounts and final health. Fifteen of 75 queue rows are scoped closed. CASE-058 zero-observation ONE/ALL and GAP-006 EQUALS passed complete native lifecycles; CASE-060 EXISTS has refreshed bounded-witness proof. Actual settled render maxima were 1,609ms, 2,044ms and 2,484ms respectively.

The next implementation batch owns Patient ONE receipt/lowering mismatch, direct coded-source null route serialization, Suggestions missing fault state, repeated-row unresolved terminal requests, and coded Pivot Explorer setup. These remain open. The accepted loading-recovery readiness patch is isolated; its browser lifecycle is pending. Preserve all prior scoped evidence and distinguish basic fixtures from CDA acceptance.

## Latest checkpoint: 7645bfc79

The reviewed batch is promoted and its seven-case browser wave is terminal. Before/after source (1,706 files), docs, API identity and mounts matched; final health passed 3/3 and the freeze is released. Sixteen of 75 queue rows are scoped closed. CASE-003 first-table creation/current preview/Add columns/reload passed 10/10 on the basic fixture: exact two Patient IDs, unchanged selected Explorer/output/draft/digest, reload-to-rows 962ms. This does not establish CDA coverage.

The next correction batch separates demonstrated harness defects from open product diagnosis: coded Pivot integer oracle reads the wrong value arm; string setup expects a workspace before the first table exists; table management remains inside Add columns after Gender Apply; list/state recovery counts its exact injected fault as unexpected. The coded-source null-route production fix now allows save/edit/reload/removal/restoration, but three unproven aborted requests keep final acceptance failed. Patient ONE still returns receipt409; retained input lacks exact construction choices and plausible scratch reconstructions do not resolve it. Legacy saved null-route UI compatibility and Suggestions diagnostics remain isolated work.

The repeated-row pre-navigation waiter was rejected because it added an unmeasured five-second delay and changed the tested sequence. Preserve the original continuous action budget and fatal unresolved-request gate; establish cancellation/terminal evidence or correct the owning behavior. Ready patches may advance independently of these unresolved diagnoses.

## Latest checkpoint: eeb62ede

Six browser cases are terminal. The shared source/docs/API identities remained unchanged, mounts passed, final health passed 3/3, and the freeze is released. Eighteen of 75 queue rows are scoped closed. CASE-004 list and CASE-005 state loading recovery each passed their four registered checks on independent basic-fixture projects. Retry-to-render was 196ms and 1105ms respectively; only the exact injected request/console pairs were expected, with no unresolved requests. This is loading-recovery proof, not CDA construction acceptance.

The next correction batch covers generated table identity throughout duplication/rename/reload/deletion, Suggestions injection chronology, and coded Pivot first-failure evidence. Table duplication visibly succeeded, but its driver expected a title-derived output ID. Suggestions' automatic lazy request preceded the assumed Raw FHIR transition. Both Pivot source-selection cases failed before meaningful lifecycle proof; retained DOM was captured after cleanup and therefore does not prove which options were offered at failure. Capture that first failure before cleanup rather than guessing a missing data option. Patient ONE telemetry, coded-source cancellation ownership and legacy null-route compatibility continue as independent isolated work.

## 9c7f199 QA results and next correction batch

The five-case wave is terminal and the shared freeze is released. Before/after source (1,710 files), docs (313 files), API identity and mounts matched; final health passed 3/3. CASE-034 initial CDA starting-collection handoff passed 9/9 checks with independent bounded Observation/Patient rows, saved reload, 27 terminal native requests and a maximum rendered checkpoint of 4,763ms. Nineteen of 75 queue rows are now closed within their recorded scopes. The target is registry configuration-bound; global runtime dataset identity was not independently checked.

Suggestions failed because its alert was absent at the five-second deadline. Both coded Pivot cases have enabled direct component sources but fail at the chooser locator; their diagnostic callback also fails the actual inspection adapter. Repeated rows passed 15 functional checks but retain an unfinished schema-fields request. An exact read-only replay returned 200 in 786ms, which proves the endpoint currently serves that payload, not that the original browser request terminated. These remain open correction work, with no cancellation waiver.

Teams implement and review these independent corrections in isolated worktrees before the next common source freeze. Table controls still need continuous action-to-settled-row evidence; Patient ONE needs the saved pre-request artifact to reproduce its receipt mismatch. Dependencies in route-null baseline testing do not hold the other ready patches.

## 3889f2bb QA and 5da073a3 correction checkpoint

Twenty of 75 queue rows are closed within their recorded scopes. Suggestions' basic-fixture retry, native Patient.id selection and Apply-to-exact-rows path is accepted through the hash-verified supplementary reconciliation of bKY5nG; the original partial report and unverified summary remain unchanged. Broader edit/reload and CDA Suggestions acceptance are separate gaps.

The tables and coded-column wave at 3889f2bb is terminal. Common source/docs/API identities matched, mounts passed and final health passed 3/3; the freeze is released. Tables t53lYM passed all ten lifecycle checks and thirteen render checkpoints (maximum 911ms), but remains failed for an unproved configured-context abort during first-table creation. Coded-column YA9Rh1 passed eleven of twelve checks and eight action-to-render measurements (maximum 567ms); two capability aborts have exact action-bound proofs that the final classifier does not consume, while frame-source-options request126 has no replacement proof and remains fatal. The basic environment-only target does not prove CDA binding.

The canonical coded Pivot source correction is integrated at 5da073a3 after thirteen focused checks. Both full CDA Pivot cases remain pending a fresh freeze. Patient's production-route regression reaches the renderer cause and now reports intended ONE rejection and ALL success with exact IDs; package checks and review remain pending. Batch those ready corrections before affected full reruns; do not suppress unproved aborts or repeat unchanged passing Suggestions evidence.

## 37bab2e9 browser results

The shared freeze is released with matching source862c3360 (1713 files), docs95d3601a (313 files), fresh API24c744/de5e6, passing mounts and health3/3 before and after. Patient ONE→ALL v8cpcP passed all six required checks: exact multiple-values422, unchanged saved Group draft, retained selection repaired to ALL, exact two IDs through Apply and reload. Fifteen named action-to-render assertions passed, maximum943ms. This closes GAP001 only within the registered basic-fixture scope; environment-only target identity, CDA and edit/removal are not proved. Twenty-one of75 queue rows are closed within scope, including the preserved contributor EXISTS closure.

Both coded Pivot cases (jeUHil integer, TPd7cB string) remain failed4/9. Their construction-proposals requests completed200 with terminal events, but the driver rejected normalized categories because it expected chooser choiceId rather than system/code/outputColumnId. This is a harness mismatch, not a measured performance failure. Audit and batch the remaining proposal/edit/removal predicates against real retained DTOs before another full rerun. Table query-owner cancellation and coded diagnostic proof consumption remain independent isolated corrections.


## d4fe4b1a latest retained-evidence checkpoint

The worklist remains 75 cases with 26 scoped closures; this checkpoint adds no closures. The source identity for the current CASE-050, CASE-061, and coded Pivot evidence is fingerprint `cea306d4b7facfa627b3b15af4a7d54595ff858f79538296ea507e786ba09769` across 1,714 files, with integrity PASS.

CASE-027's `ba217bb33981267f81f9fa24b208b8e9265856fa` candidate was rejected because it dropped `captureReport.errors`. Its lead is correcting the collector; there is no accepted replacement or new lifecycle evidence.

CASE-050 retained 16/16 named checks, but the report fails on two observed, unexpected schema-fields `ERR_ABORTED` requests triggered while closing the editor. Five other requests—four semantic-inventory and one construction-capabilities—remain request-only through the bounded drain. Those are separate unresolved records. The target remains environment-only and does not verify global runtime dataset identity.

CASE-061 retained 8/8 lifecycle checks and two render checkpoints, maximum 764ms. Schema-fields cursor request #38 has no terminal event; it began 153ms before same-Page document navigation after request #37 completed. The independent browser-context probe shows explicit `AbortController` aborts produce Page and Context terminal events, while navigation-owned server-socket closure remains request-only. This does not establish that the original request was safely canceled. The environment-only target still does not verify runtime dataset identity.

GAP-004's current promoted-source runs pass 4/9 for integer and 5/9 for string. Both fail because cancellation attribution records a NULL-candidate abort without an observed matching request; neither run emitted render checkpoints, and neither has pending owned requests. The current source fingerprint above replaces the older 78d-only applicability record; the target binds project and generation, while runtime dataset identity remains unchecked.

## 39d416b retained-evidence checkpoint

The worklist remains 75 cases with 26 scoped closures; this checkpoint adds no closures. The CASE-008, CASE-027, and integer/string coded-Pivot runs below used source fingerprint `1a0b103e74c79f84f29f7ae3968c063c95be04ce24b9dd19875e2264f68eaff6` across 1,716 files, with integrity PASS. The exact API build identity was `24c7447e75b9768eaa50b5b65482b55d34c470fcd26e5c17a3a2a1431a260a8c:24c7447e75b9768eaa50b5b65482b55d34c470fcd26e5c17a3a2a1431a260a8c:de5e6f089f7ef07bda9178422bf2e9b28783523cc45abfe749d1c415348d9838` before and after.

CASE-008 passed 1/11 checks, then failed when the named-cohort row-definition proposal returned HTTP 500 with `CANDIDATE_PREVIEW_FAILED`. The request took 421 ms; the 5,364 ms driver action reflects the response wait after the error, not a slow successful operation. No render checkpoint was recorded. The basic-fixture target is environment-only and registry-unbound.

CASE-027 passed all 63 UI checks and recorded 27 render checkpoints, maximum 867 ms, but strict native request completion failed: 97/103 finished, 4 failed, and 2 pending. Pending #654 semantic-inventory and #656 construction-capabilities are request-only. The clock-normalization artifact `/private/tmp/case027-clock-normalization.json` (SHA-256 `fb98c54e76d80baefcb5b47f55310f7523f273fb96fd0f41827b094605bc776f`) aligns those starts 10 ms before the final navigation request and 30 ms before frame commit; it does not prove owner retirement, server arrival, or terminal completion. Schema-fields #128 and #146 are separate explicit `ERR_ABORTED` failures with no navigation in their intervals. No server/query-phase log is retained. Keep these unresolved records fatal.

The integer and string coded-Pivot runs each passed 8/9 checks; both failed the strict native-request gate. Integer retains #83, #85, and #93 as request-only, plus #101 `ERR_ABORTED`. String retains #85 and #93 as request-only and #101 `ERR_ABORTED`; #83 completed with HTTP 200 and `requestfinished`, but body capture failed after navigation. Configured-column-context #105 completed with HTTP 200 and `requestfinished` in both reports; `bodyNotRead` is the intended allowlist behavior. Both CDA targets bind project and generation, but runtime dataset identity remains unchecked.

## d4f4b27 current QA and scoped closure checkpoint

The worklist remains 75 ordered cases with 28 scoped closures (16 `closed`, 12 `closed-scoped`). CASE-024 remains scoped closed for the Epoch66 basic-fixture Membership lifecycle: its 50/50 registered checks match the retained report, the source fingerprint was unchanged across 1,503 files, and the reviewed Membership path has no relevant behavior change. Real CDA, published-source inputs, and restricted authorization remain separate.

The current d4f4b27 source identity is fingerprint `7a835025e47b8f31403481ba54acd38841606d7f9b23bea11a7b7cc69841b58e` across 1,724 files; the exact API build identity is `5068be64023db5a82cc1c263ea3c078cb5c57133deb9004d10c62e65ffe1ebc6:5068be64023db5a82cc1c263ea3c078cb5c57133deb9004d10c62e65ffe1ebc6:db6a8af11c85b84f97e804442c8434e8d3f6c4ab21b93db757f9ff4fb70e0796`. Current-run source, docs (313 files), API, and integrity checks passed.

CASE-008 passed 11/11 registered checks after its promoted DTO/provenance correction, but its final network gate found schema-fields request `schema-fields-33875306-8d49-4e3d-8baf-e0f0155eee4e` with `ERR_ABORTED` during Patient.id ALL Apply and no main-frame navigation. CASE-009 passed 18/18 after the promoted scoped raw-fields locator correction, but schema-fields request `schema-fields-a355125f-9b3f-4854-9582-7dc8aa54cbe8` ended `ERR_ABORTED` with no action or main-frame navigation. Keep both requests fatal. Root assigned the shared abort-probe API correction to these callers; no cancellation cause is established.

CASE-010 remains separate active owner/signal root-cause work. Its observed schema-fields POST `ERR_ABORTED` has no linked action or main-frame navigation; keep it fatal until exact evidence resolves attribution. Do not apply a broad abort waiver.

CASE-016 remains harness-incomplete after 6/9 checks because the driver threw `context.flushHttpDiagnostics is not a function`; one check failed and two are missing. The two controlled `TABLE_PIVOT_CELL_CARDINALITY` 422 previews and console messages are expected repair-validation evidence, not the blocker. Fix the caller/context mismatch and rerun the exact registered lifecycle.

CASE-027's latest full rerun passed 63/63 UI checks and 27 render checkpoints (maximum 868 ms), but its strict ledger was 96/103 finished, 4 `ERR_ABORTED` failures, and 3 request-only requests. The final pending requests had no matching owner-probe evidence. Keep these failures fatal and the lifecycle open pending a concrete terminal-evidence correction and rerun.

CASE-033 is closed only for scoped applicability: its retained 12/12 browser lifecycle plus the promoted current-source production routed-EXISTS cursor regression, which passed four page/selected-row assertions across three root-key pages. There was no fresh CDA/browser lifecycle or runtime-dataset identity claim.

## f563bbd current native-request evidence checkpoint

The worklist remains 75 ordered cases with 30 scoped closures. The shared QA freezes for the 9048d508 and f563bbd cases are released; before/after captures matched source, docs, API identity, mounts, and health. This checkpoint closes CASE-030 and CASE-040 for their exact registered lifecycles only.

CASE-008 and CASE-009 each passed all registered UI checks and recorded a settled render checkpoint. Their exact schema-fields `ERR_ABORTED` requests now correlate to the scoped generated-fields AbortSignal and detached Catalog owner. The native requests still failed, so the strict error gate remains open. CASE-010 passed 53/53 checks and 24 render checkpoints, but its schema-fields request has no exact signal correlation and the separate construction-capabilities request remained pending at the five-second drain. These requests remain fatal.

CASE-016 passed 9/9 checks with no pending requests, but the final generic report gate counted two expected `TABLE_PIVOT_CELL_CARDINALITY` 422 responses and their two console records as errors. No render checkpoints were recorded. Correct the exact expected-error classifier and rerun before claiming closure.

CASE-020 passed 21/21 checks with two render checkpoints; four requests remained pending and four ended `ERR_ABORTED`. CASE-026 passed 45/45 checks with 17 render checkpoints; three requests remained pending and seven ended `ERR_ABORTED`. Both basic-fixture Append lifecycles remain open. CASE-027 passed 63/63 checks with 27 render checkpoints, but its 104-record ledger has four `ERR_ABORTED` failures and two pending requests: semantic-inventory request `feature-catalog-db69526e-954a-4ec7-a0a0-bb8dc22140c5` and construction-capabilities request `cda-request-ece61a95-208f-4a70-bf32-17ea15bf8ebf`. No request is waived based on replacement or navigation alone.

CASE-030 is closed-scoped from the current f563 source run: 9/9 registered checks and 30/30 assertions passed; all 52 owned requests have native terminal events and HTTP 200, and 14 action-to-settled checkpoints completed within 1,990 ms. Its raw oracle is project/generation scoped. The target configuration is bound, but runtime dataset identity and restricted-authorization exclusion remain unproven. Epoch114 remains historical evidence. The sanitized report and retained run artifacts are linked in the CASE-030 worklist row.

CASE-040 is closed-scoped for the exact registered upstream edit/cascade lifecycle: 12/12 checks and 14/14 assertions passed; all 186 owned requests have native terminal events and HTTP 200. The report has zero standardized render checkpoints and leaves generic usability, persistence, and performance dimensions untested. The domain assertions bind the two relevant reloads to settled exact rows: the edited expansion returns 10 rows in 1,939 ms, and cascade restoration returns the exact single root row with zero construction steps in 1,848 ms. Runtime dataset identity and restricted-authorization exclusion remain unproven. Epoch121 remains historical evidence. The sanitized report and retained run artifacts are linked in the CASE-040 worklist row.
