# Complete bounded verification of the Builder core protocol

Status: planned. This document is a work plan, not passing evidence.
Committed starting point: `dc9016bc9080596099c1ad0253ea3ee229544c58` on `feature/deep-clean`.
Record the actual execution snapshot before work. Preserve relevant later changes.

## Execution through the existing OpenCode harness

Use the operator's existing OpenCode harness and continuation mechanism. No orchestration framework migration, local Source adapter, GitHub intake, PR delivery or external messaging is required. Keep local Git history for source review, fingerprints and historical test recovery. Commits, publication and deployment outside the documented owned local test stack still require separate authorization.

Target: one RTX PRO 6000 Blackwell with 96 GB and the operator's Qwen endpoint. Record the exact model ID, weight revision, quantization format, inference server/version, context limit and sampling settings. Confirm the model actually selected by the execution Session. A saved Session from another task does not establish the intended model or task configuration.

### H0. Confirm the existing OpenCode execution path

Record the installed OpenCode version, relevant harness/plugin versions, working directory and supported start, status, stop and resume operations. Use the installed harness's actual instructions and tools. Do not invent a Goal API, CLI flag, automatic job wakeup or retry policy. Reuse the existing continuation mechanism; a new queue, dispatcher, plugin or service is not a prerequisite for this work.

If the harness has Goal/requirement/evidence tools, use them to track this assignment through its supported interface. Bind this work to the intended Session and a clearly named Builder core verification objective. Preserve unrelated goals. The local `.opencode/goals/` contains state from another assignment, including an objective named `view`; it is not this plan's work queue or completion evidence. Do not hand-edit stored goal JSON, copy old model settings or repurpose another task's goal.

Prove the existing execution path with a disposable task: read a file, apply a small patch, run a passing check and a failing check, and collect their real exit statuses. Test compaction and process restart, then confirm the Session resumes with its source identity, evidence and next action intact. Verify manual stop remains stopped until explicitly resumed. Observe how this harness handles long commands and collect their final results using its actual job/session tools. Missing provider or tool compatibility is a precise H0 blocker, not permission to rewrite the harness.

**H0 exit:** the existing OpenCode/Qwen path runs tools correctly, supported continuation and stop/resume behavior are recorded, and the disposable task survives restart without a second writer. This milestone validates the installed harness; it does not implement another one.

### H1. Provision Loom before unattended execution

Choose absolute paths once for the Loom checkout and persistent work/evidence directory. Record where the existing harness persists its Session and task state. Resolve skills through that host's supported mechanism or read their installed files directly. The `/workspace/.loom-setup` locations below are historical preparation paths, not required installation locations.

Install the exact Go version from `go.mod`, supported Node, Java and pinned formal tools, UI dependencies, Chromium and the owned Arango/ClickHouse fixtures required by the selected checks. Export `GO` as the absolute compatible binary and prove `GOTOOLCHAIN=local "$GO" version`; a Go launcher can report a downloaded version but use an older compiler inside verification. Follow `scripts/setup-formal-tools.mjs` and the project skill. Separate cold compilation/setup from timed checks. Do not set `CI=true` merely to hide local timeouts.

Run the baseline formal command and selected real-service doctors on this host. Record unavailable browser, authentication or service prerequisites before selecting work that depends on them. A blocked integration boundary can leave independent work runnable; it cannot satisfy final acceptance. Prove local app setup and seed availability early. The documented OpenShell rebuild of the owned local test stack is authorized; preserve database volumes and unrelated resources.

**H1 exit:** tool paths, commands, fixture ownership and baseline outcomes are recorded outside Git. The first executable Loom task can run without installation guesses.

### One-model execution for the full program

Run the existing Qwen model through OpenCode. A second loaded model, stronger-model coordinator, new dispatcher or continuous human supervision is not a prerequisite. The operator chooses the model and any available final reviewer. Do not change model routing, inference limits or harness configuration without authorization.

The assignment remains the complete M0–M6 and C01–C19 program, including every mandatory command variant found in the core map. Bounded tasks limit the next implementation decision, not the total work. Continue through the remaining backlog across turns and days until acceptance is met, the operator stops the run, or no authorized useful work remains. Do not invent additional scope or keep running after completion just to fill time.

Use three separate operations in the same model's workflow: select a concrete task, implement and verify it, then check its evidence against the acceptance rule. These are responsibilities, not a requirement for three agents. A fresh same-model review context is optional when supported and affordable. Record it as same-model review; it is not independent final approval and cannot repair a missing execution boundary by agreeing with the worker.

Use OpenCode's existing Goal/task state for continuation, the core map for scope, and one local checkpoint for current execution and evidence. No additional authored queue or generated report catalog is needed. Use supported tools rather than editing stored goal JSON. Task data never grants permission to change policy.

### Parallel agents on one served model

One-model execution means one model checkpoint/provider, not one agent or one serial workstream. Follow `AGENTS.md`'s Parallel work instructions: proactively delegate useful independent work without asking the operator to request subagents. In OpenCode, launch background tasks with `task(background=true)`, up to five workers alongside the parent when serving capacity permits. Give each a concrete deliverable and explicit ownership; keep the parent integrating results and progressing a separate ready task.

One implementation writer means one owner per shared file, worktree, or mutable runtime, not one worker for the whole program. Use separate worktrees or isolated copies for concurrent implementation and fault experiments. Independent source investigations, coverage analysis, reviews, and isolated verification runs can run concurrently. Preserve source freezes, unique TLC metadirs, and serialized mutations of a shared runtime. A short local edit may stay direct; a substantial task should not stay serial merely because the operator has not asked for delegation.

### Select, execute, accept, continue

1. **Select.** Read the current diff, actual test output and unmet core-map rows. Reconcile existing results before inventing another task. Reuse H0/H1 and baseline evidence when its relevant source and environment are unchanged. Do not restart setup or the whole audit at each turn.
2. **Define.** Record one task with a scenario/property ID, concrete harm, production entry points, exclusive files, existing code to reuse, exact focused command, expected observations, semantic fault where required, and evidence path. If creating a check is the task, name its intended consumer and asserted boundary first. A task can repair an execution path, reproduce a defect, or close part of a scenario without pretending the entire scenario is complete.
3. **Execute.** Make the smallest coherent change and run the focused check. Keep one implementation writer per shared file or worktree; delegate independent work in parallel. Serialize mutations of a shared runtime and freeze tested source during evidence runs. Preserve unrelated dirty work and inspect existing jobs before resuming after interruption.
4. **Check the evidence.** Read the real command exit, named assertions, source identity and raw artifacts. Valid behavior must work and the required semantic fault must fail for the intended reason. Check that the observed fields actually establish the named property. Separate server, component, browser and database claims. A completed todo, changed file or progress counter cannot satisfy a verification requirement.
5. **Record.** Mark work `in-progress`, `ready-for-review`, `accepted-within-bound`, `failed` or `blocked` in the existing task/checkpoint record. Here `accepted-within-bound` means the declared automated checks passed and their scope was inspected; it is not independent approval. Keep the normative core-map statuses and final evidence contract unchanged. Record review capability separately. Never mark a mandatory scenario passed from a partial boundary.
6. **Continue.** Record the next exact action and immediately take the next runnable task. A task result or progress report does not end the overall Goal. Keep the Goal incomplete until M6, including independent final review. Do not ask the operator to approve each routine task.

The task template and initial instruction are in the handoff. The template is an execution aid, not another scope document. No fresh Session or Goal is required for each task.

### Work available across days

The mandatory scenario floor below remains authoritative. This routing guide divides that work into executable areas; it is not a second status inventory. Reuse existing evidence and recover relevant historical assertions before adding code. Work on only the unmet part of each area.

| Work area | Mandatory attachment | Preferred execution boundary and exit |
| --- | --- | --- |
| Joined valid path and competing edit | M2, C05 | Actual UI/client decoding connected to production handlers. Observe saved identities, compiled receipt and displayed preview; add the competing save/refetch and the matching semantic fault. Separate mocked UI and Go tests support but do not replace the joined path. |
| Hydration and recovery | C01, C03, C07 | Actual query hook/cache and Workspace admission with controlled replies; forced refresh and next-command identity observed. Check public API seams separately where needed. |
| Command queue and owner release | C02, C04 | Pause the real queue/drain boundary; observe outgoing identity, rejection and correct resumption. Do not replace the owner with a model-generated success response. |
| Scoped compilation and receipt contracts | C06, C10, C11 | Production handlers/compiler/receipt consumers. Check output closure, each promised binding, valid retained historical execution and revoked/unretained rejection. |
| Retry and stale-snapshot budgets | C08, C09 | Focused actual callback or mounted schedules with controlled timers. Check mixed counter sequences and owner loss without waiting real retry delays unnecessarily. |
| Current preview and lifetime | C12, C13, C14 | Mounted production behavior for reordered success/error, cancellation, unmount and A–B–A attempt reuse. Reuse the joined browser checkpoint for browser-specific wiring. |
| Restore while work is pending | C15 | Actual restore/command/receipt contracts plus controlled client completion ordering; follow with the required saved-state/reload evidence. |
| Concurrent writes and ambiguous acknowledgements | C16, C17, M4 | Real owned Arango schedules for head/history/rollback claims. Distinguish confirmed save, conflict, committed-but-unconfirmed and legitimately newer reads. |
| Malformed and failed replies | C18 | Actual decoding/handler boundaries with typed errors, no partial success and valid companions. A server-only check does not establish UI decoding. |
| Independent row/schema semantics | C19, M4 | Reuse literal fixtures and real Arango/ClickHouse execution for types, values, nulls, duplicates and cardinality after edit/restore. |
| Usable local lifecycle | M5 | Reuse/adapt one owned real-app journey for edit, Apply, preview, reload and restore. Keep actual user controls and authoritative saved-state checks. |
| Integration and maintainability | M6 | Final source freeze, combined audit, required service/browser checks, executable skill handoff, resource cleanup and independent final review. |

This is enough work for sustained execution; no time estimate is promised. Difficulty or duration does not remove a requirement. Additional command variants discovered in M1 must acquire evidence within this scope. Do not broaden into unrelated product features or restore the entire deleted test suite for volume.

### Testing and debugging discipline

During editing, run the single failing test by exact name, the affected model/replay, or the narrow API case. Show diagnostic output and preserve the actual exit status. Do not hide the useful output behind `grep`, `head` or `tail` while diagnosing a hang. Run the affected file/feature after the focused correction passes. Run broad audits at coherent integration checkpoints, not after every small edit.

After two runs with unchanged inputs and the same failure, rerunning is not the next action. State the last observed completed operation, the unfinished await/process/transition, one falsifiable hypothesis and the smallest experiment that distinguishes it. Use unique instrumentation per test and remove disposable debugging changes after diagnosis. An interval stopping does not by itself prove event-loop blockage; a marker near a test's end does not establish cleanup completed.

Give each investigation an initial 60-minute checkpoint budget, not a licence to speculate for an hour without observations. After two materially different approaches produce no new evidence, checkpoint and change the approach, select independent work, or use an available review capability. Budget expiry does not kill a healthy bounded verifier or excuse abandoning the mandatory requirement. Record what evidence would unblock it. Do not repeatedly relaunch a full self-test to discover where it stopped.

Use fresh evidence directories. Never delete earlier failure evidence to reuse a path. Collect long-command results through demonstrated harness job controls. Use a detached shell process only if H0 established that the harness lacks adequate job ownership; then retain its exact PID identity, exit-status file, source manifest and cleanup evidence. Sleeps and broad process-name counts are not completion checks. Do not start duplicate workers to compensate for an unresolved one.

Treat infrastructure repair as a bounded blocker task with its own reproduction. Change another runner only when the failure reaches that runner. In OpenShell, process-group signaling is restricted; check permitted child-process operations and prove termination without loosening isolation. A cleanup error must remain visible. Missing privileges, timeouts and import cycles are not Builder defects or successful semantic fault controls.

### Reuse before adding verification

Use the pstack skill that resolves the current uncertainty, through its installed OpenCode name and instructions. Do not run every skill on every task. Use maintain-formal-verification-skill for drift and model/observation alignment; create-formal-verification-skill only for a missing promise; statechart for model results; and the applicable repository verification skill for real execution. Use maintain-verification-skill at coherent coverage/recipe checkpoints. Poteto mode guides concise work and small coherent changes. Skill invocation is not evidence of execution.

Inspect the relevant historical tests at `5646633e` and current helpers before adding a browser path. Recover their assertions and independent oracles, adapting only the missing boundary. Browser-native interaction belongs in the browser. Most controlled interleavings belong in focused mounted/API/replay checks. A separate browser journey for each C01–C19 row is not required. Do not claim that fast mocks replace required durable storage or real client/server wiring.

Every new helper/model/adapter needs a named consumer and a missing guarantee that existing code cannot establish. Prefer extending the existing runner and reusing working fixtures. Line count alone is neither acceptance nor a deletion target. Stop building infrastructure once its demonstrated blocker is removed and return to the next protocol requirement.

### Async continuity, review and stopping

Checkpoint source identity, current task, commands and owned processes, raw evidence, result, blockers, next action and last useful progress. After restart/compaction, reconcile this record with live files/processes and resume the first unmet task. H0 proves actual continuation and manual stop; prose alone does not start a 24/7 service. Keep model selection, context limits and host persistence in existing harness configuration.

An optional fresh same-model review can challenge a bounded change, but it must inspect the raw diff and evidence. Do not label it independent assurance or launch concurrent reviewers that exhaust the same server. Independent final review remains mandatory. While it is unavailable, continue any remaining implementation, validation, evidence reconciliation and handoff work, keeping the result provisional. When independent review is the sole missing gate, report review pending and the required handoff rather than inventing approval or keeping an idle loop alive.

Continue repairing authorized local environment issues and work around a blocked task by taking an independent one. Stop for operator action only when no authorized useful task remains, naming the exact unavailable environment/access/review prerequisite, attempted remedies and smallest manual action. Do not stop merely because a test is hard, one approach failed, or another routine decision is needed. Honor an explicit operator stop. Completion ends the program; no additional work is manufactured to keep the model occupied.

## Goal and meaning of complete

Verify how the actual frontend uses Builder state, commands, reconciliation, compilation receipts and preview. A late or mismatched response must not replace current intent. A successful save must agree with authoritative saved state. A preview must belong to the exact accepted compilation context.

Completion means every core operation and interaction identified from current source has an explicit disposition and executable evidence within the final declared bounds established in step 3. It does not mean every possible program execution, every compiler expression or every product feature is formally proved. Every required core operation is mandatory by default. Record exclusions, but excluding a mandatory operation does not satisfy completion. Only an explicit user scope change can remove a mandatory operation from the completion claim. An unresolved core safety property, required skipped check or blocked required boundary prevents completion.

## Is the skill missing, or is implementation missing?

Both need work, but most remaining work is verification implementation.

- The existing project skill runs bounded models, actual-code replays and source faults. Keep it.
- Its coverage descriptions do not yet establish a complete UI-to-backend core protocol. Add the missing operation map, composed properties, execution recipes and completion rules.
- Implement missing models, protocol adapters, semantic checks and a thin real-app journey. Documentation cannot substitute for these.
- Product fixes are driven by reproducible counterexamples. No speculative product rewrite is part of this plan.

The current eleven operation-history witnesses are supporting checks. They are not a replacement for protocol exploration. The retired 78-case generator is reference material in Git, not a dependency to restore.

## Current source anchors

| Boundary | Read and reuse |
| --- | --- |
| UI requests and decoding | `ui/packages/loom-ui/src/api.ts`, including `getBuilder`, `reconcile` and receipt-bound `preview` requests |
| Query admission and cache | `ui/packages/loom-ui/src/react.tsx` and Builder query callers |
| Editor state and command queue | `ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx`, including `applyCommandsWithResult`, `reconcileCurrent`, recovery and preview ownership |
| Public HTTP adapters | `internal/server/openapi_explorer_routes.go` and `internal/server/explorer_authoring_http.go` |
| Authoritative state and reconciliation | `internal/explorer/lifecycle/query.go`, `authoring.go` and `draft_restore.go` |
| Compilation and receipt validation | `internal/server/explorer_receipt_contract.go`, `explorer_receipt_validation.go`, `internal/explorer/compilation_receipt.go` and the compiler callbacks they invoke |
| Persistence | `internal/explorer/arango/drafts.go` and existing transaction/database checks |
| Existing verification | `verification/builder-interactions/`, `scripts/verify-builder-formal-core.mjs`, `scripts/lib/formal-*.mjs`, mounted Workspace tests and public server tests |

“Compilations API” is a product-area label here. Inventory the actual public routes and internal receipt operations; do not invent a standalone `/compilations` endpoint or model only the endpoint name.

## Artifact locations and ownership

The paths below name the maintained inputs and implementation outputs. “Planned” means the file does not exist yet and is not evidence. Update this table before changing a planned file name. Create only files needed by the first executable slice; these names do not justify building unused machinery.

| Artifact | Exact location | State and responsibility |
| --- | --- | --- |
| Qwen execution entry point | `docs/verification/CORE_PROTOCOL_QWEN_HANDOFF.md` | Handoff instructions. Read this plan as the normative acceptance contract. |
| This plan | `docs/verification/CORE_PROTOCOL_VERIFICATION_PLAN.md` | Existing work sequence and completion gates |
| Project verification instructions | `.codex/skills/builder-interaction-formal-verification/SKILL.md` | Existing commands, prerequisites, evidence and maintenance rules |
| Core operation map and property definitions | `.codex/skills/builder-interaction-formal-verification/core-protocol.md` | Planned. One compact document owns route/caller coverage, identities, promises, bounds, dependencies, exclusions and evidence links. Do not create another generated catalog. |
| Feature index | `.codex/skills/builder-interaction-formal-verification/features/README.md` | Existing five feature groups. Link the cross-cutting core map without introducing a duplicate suite-routing group. |
| Public API definitions | `openapi/openapi.yaml` | Existing method/path and wire-contract input, checked against actual UI calls and handlers |
| Composed model and TLC configuration | `verification/builder-interactions/CoreAuthoringProtocol.tla` and `verification/builder-interactions/CoreAuthoringProtocol.cfg` | Planned for the missing composed interaction. Extend existing models instead when that avoids duplication; record that decision in the core map. |
| Model-to-code replay and observation projection | `scripts/lib/formal-core-protocol-replay.mjs` | Planned extension of existing replay infrastructure. Own the production-event projection, legal-prefix checks and explicit stuttering rules. |
| Adapter rejection tests | `scripts/lib/formal-core-protocol-replay.test.mjs` | Planned checks for malformed, stale, incomplete and illegal-path evidence |
| Actual mounted UI protocol driver | `ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.coreProtocol.formal.test.tsx` | Planned. Mount production Workspace/query-cache behavior and reuse existing test support. |
| Actual HTTP/service protocol driver | `internal/server/core_authoring_protocol_test.go` | Planned. Execute production handlers/services/compiler through existing server test infrastructure. |
| Shared UI observations | `ui/packages/loom-ui/testSupport/formalTrace.ts` | Existing trace support. Extend only where the composed driver needs facts not already captured. |
| Runner and evidence integration | `scripts/verify-builder-formal-core.mjs` | Existing command entry point. Add the composed checks here rather than a parallel top-level runner. |
| Property fault and graph registrations | `scripts/lib/formal-source-proofs.mjs`, `scripts/lib/formal-model-controls.mjs` and `scripts/lib/formal-model-graph.mjs` | Existing registries. Register new properties and controls once. |
| Tool and source records | `verification/builder-interactions/tools.lock.json` and `verification/builder-interactions/reviewed-sources.json` | Existing pinned tools and reviewed production hashes/baseline |
| Statechart labels | `verification/builder-interactions/statechart.view.mjs` | Existing pstack rendering customization. Reuse its compiler/IR pipeline. |
| Real local lifecycle instructions and helper | `.codex/skills/verify/SKILL.md` and `.codex/skills/verify/scripts/core-protocol.mjs` | Instructions exist; helper is planned only if the existing executable controls cannot drive the lifecycle. Own launch/doctor/drive/cleanup here. |
| Existing authenticated-target control | `.codex/skills/verify-loom-ui/SKILL.md` and `.codex/skills/verify-loom-ui/scripts/verify.mjs` | Existing alternative target control. Do not claim its load-only drive already covers the planned lifecycle. |
| CI integration | `.github/workflows/builder-formal.yml` and relevant existing service jobs in `.github/workflows/tests.yaml` | Existing unsharded jobs. Add checks to their appropriate dependency boundary. |

The dependency order is operation map and baseline, then protocol/model definitions, then actual drivers and projection, then fault/runner integration, then the real-app lifecycle and final skill handoff. The coordinator owns shared registries and the source freeze. Concurrent writers receive exclusive files.

Runtime reports live outside Git under a fresh run directory. The existing runner owns `verification.json` and generated statecharts. The planned adapter owns `core-protocol/observations.jsonl`, `core-protocol/conformance.json` and `core-protocol/counterexample.json` within that same run directory. The real-app helper records its evidence directory in its report. These planned report names are outputs, not authored dependencies or current proof. Temporary development notes remain under `/workspace/work/`; they are not required inputs for future verification.

## Required skill workflow

Use the existing project skill rather than generating a second one. These are required inputs, not evidence that their workflows have already finished:

The locations below describe the original preparation host. H1 records the installed equivalents on the execution host. Missing required skills are explicit setup blockers, not a reason to assume their procedures ran.

| Skill | Historical preparation location | Required use |
| --- | --- | --- |
| Create formal verification | `/workspace/.loom-setup/pstack-source/pstack/skills/create-formal-verification-skill/SKILL.md` | For each new promise, choose the tool from the question, ground every step in code, declare bounds, show a real-source fault, and prove the runnable project skill before handoff. |
| Maintain formal verification | `/workspace/.loom-setup/pstack/skills/maintain-formal-verification-skill/SKILL.md` | Run existing checks before edits, inspect every relevant change since the sync record including moved/new writers, classify drift versus bug, update from code, rerun and falsify, then update reviewed source/sync records. Outcomes are in sync, updated, bug or blocked. |
| Create user-facing verification | `/workspace/.loom-setup/pstack-source/pstack/skills/create-verification-skill/SKILL.md` | Prove Launch, Doctor, Drive, Evidence and Cleanup against the declared real-app target. |
| Maintain user-facing verification | `/workspace/.loom-setup/pstack/skills/maintain-verification-skill/SKILL.md` | Keep the feature map honest with source reviews and coordinator-owned live drives. This is separate from a TLA pass. |
| Poteto mode | `/workspace/.loom-setup/pstack-source/pstack/skills/poteto-mode/SKILL.md` | Prefer the smallest useful implementation, explicit domain structures, verified behavior and independent final review. It is not a certification service. |
| Statechart | `/workspace/.loom-setup/pstack/skills/statechart/SKILL.md` | Read every TLA result as a chart or shortest counterexample using the pinned existing compiler. Do not duplicate its parser or copy release-specific invocation details into the project skill. |

Read the Codex compatibility adapter and model configuration when that runtime is in use. On another host, locate the same named skills through its supported discovery mechanism and record missing capabilities honestly. Never claim a skill was applied without reading it. User instructions and host permissions take precedence.

Maintenance edits models/harness/docs; a discovered current-code violation remains a finding. The separately authorized product-refinement work in step 5 can then fix a reproduced bug. Keep that product patch distinguishable from verification changes. Never disguise a product regression as a changed specification. Move a sync commit only after confirming the committed source; do not invent a new commit for this dirty tree.

## Recover deleted tests before writing replacements

Many authored browser tests, fixtures, model schedules, contract assertions and row oracles were deleted during earlier cleanup. Their intended guarantees are useful low-cost starting material. Their historical pass is not current evidence, and recovery is not free of adaptation or verification work.

Read `docs/verification/DELETED_GUARANTEES.md` and `docs/verification/RETAINED_COVERAGE_DECISIONS.md` first. The immutable recovery tree is `5646633e0e5c7ff8de568a4f111478d121e7f75f`, before the later verification deletion at `e736e130`. Other deletion commits may hold additional material. Search the relevant path history instead of assuming all deleted content belonged to one commit.

Useful read-only discovery commands from the repo root are:

```sh
git log --all --oneline -- scripts/verify-ui verification/builder-interactions ui/packages/loom-ui/src/features/ExplorerBuilder
git diff --diff-filter=D --name-only 5646633e e736e130 -- scripts/verify-ui verification/builder-interactions
git ls-tree -r --name-only 5646633e -- scripts/verify-ui verification/builder-interactions
git show 5646633e:verification/builder-interactions/BuilderTraceValidation.md
```

For an absent historical object in a shallow checkout, retrieve only the needed history through available authorized Git access or record a history-access blocker. Continue work on available current sources. Never assert an old test did not exist merely because the local shallow checkout lacks it.

| Historical source at `5646633e` | Useful guarantee to inspect | Preferred current attachment |
| --- | --- | --- |
| `verification/builder-interactions/BuilderJourney.tla` and `DraftRestore.tla` | Saved state, restore, current ownership, reload and composed operations | Core protocol properties plus actual command/restore/UI admission replays |
| `verification/builder-interactions/TemporalFilterJourney.tla` and `BuilderTraceValidation.md` | Observed mutation/reconcile/preview ordering and transport-bound identities | Production observation/projection and legal-prefix checks |
| `verification/builder-interactions/RootChange.tla` and `PolicyOneDisagreement.tla` | Epoch/identity changes, stale ownership and proposal/CAS behavior | Reachable focused schedules where the current core map includes that behavior |
| `scripts/verify-ui/specs/builder-saved-route-edit.spec.mjs` and `scripts/verify-ui/workflows/saved-related-expand-route-edit.mjs` | Cancel, reviewed Apply, exact saved route, reload and row identity | Existing mounted/API/real-store checks for semantics; thin browser proof only for native wiring |
| `scripts/verify-ui/workflows/cda-current-draft-upstream-append-workflow.mjs` and its helpers/oracles | Group/Calculate/Filter dependencies, APPEND values/cardinality and current-definition recovery | Reuse current APPEND/query-row profile and extend only a missing asserted boundary |
| `verification/builder-interactions/operationHistoryModel.mjs` | Historical Group edit/cascade/type witnesses | Current eleven fixed schedules already retain the selected slice. Recover a specific missing assertion only when it serves a named gap; do not restore the discarded breadth search wholesale. |
| `verification/builder-interactions/retained-browser-evidence/formal-tla-receipt.json.gz` and `formal-mutation-receipt.json.gz` | Historical trace provenance and negative-control intent | Reference for designing witnesses, never a replacement for fresh execution |

Before adding a test for a mandatory row, record the historical search result in that row or its linked note. Classify relevant assertions as already covered, adapt/reuse, obsolete with source-backed reason, or still missing. This is targeted by the active property, not a new inventory of every deleted file before any implementation can start. Recover useful fixtures/oracles directly; do not wrap or copy the entire retired framework.

Preserve the reader rule during adaptation. Playwright is a runner; `.mjs` is a module format and may itself contain Playwright. Neither establishes the right execution boundary. Move pure decisions to actual-code/generated-input checks, async UI ownership to mounted production behavior, API contracts to real handlers, persistence/query semantics to owned real services, and keep native drag/download/layout/browser wiring in the browser. A mocked UI row fixture does not replace a deleted database-result assertion.

A recovered test earns coverage only after it runs against current code, reaches its asserted boundary and fails for the intended semantic source fault where that property is claimed. Keep independent literal expectations and negative cases. A skipped retired spec, preserved archive or source citation alone is not a recovered check. Cite the original commit/path and explain any scope lost in conversion. When no recovered assertion fits, write the smallest new check and state why.

## Work sequence

### 1. Establish the current operation map and baseline

Read each core UI caller through request decoding, HTTP adapter, service, compiler/receipt and persistence boundary. Recover historical tests only when they assert a relevant promise. The old plan and route counts are historical, not current accounting.

Record the compact operation table in `.codex/skills/builder-interaction-formal-verification/core-protocol.md`. Each row names the user action, actual method/path or internal operation, source functions, identity fields, authoritative state, observable result, existing model/test, missing evidence and explicit exclusions. Include all entry points that perform the same operation. Reuse existing metadata where it already serves this purpose; do not rebuild the retired readiness/report system.

Required families are initial load and refetch; command batches and proposal Apply; restore; whole-workspace and output-scoped reconcile; receipt creation/validation/retrieval as exposed by current code; preview and recompile recovery; context changes and unmount. Determine whether publication belongs to the core protocol through its receipt dependencies and document the boundary. Enumerate command variants and output dependency behaviors rather than assuming one representative validates every family.

Run the current default formal self-test once before edits. Capture source fingerprints and the actual result. Classify subsequent failures as drift, product defects or blocked checks.

**Exit:** every core UI-called operation has a disposition, with targeted historical-test search results for the active missing promises. No stale historical route count or unexamined “covered” label remains.

### 2. Define the protocol and its promises from code

Describe the actors and state before writing TLA+. Distinguish frontend local intent, admitted Builder/query-cache state, server draft head, in-flight command/reconcile/preview attempts, compiled receipt contents and the visible preview.

Trace project, authorization context, Explorer, output, snapshot, draft version/digest, workspace fingerprint, request generation and unique attempt identity. Record which fields are actually sent, stored, checked and returned. Do not invent a receipt guarantee the backend does not make. Capture real await and transaction boundaries; no model action may combine independently interleavable operations.

Resolve these promises against contracts and implementation:

1. Only a response belonging to the active context/attempt may alter current UI state.
2. Delayed Builder/cache responses cannot regress already admitted authoritative state within the same identity. Legitimate Explorer/context switches remain possible.
3. Commands bind to a coherent current identity. Conflicts trigger the documented recovery without silently applying stale user intent under fresh CAS.
4. Reconciliation validates the requested saved draft and snapshot, and its receipt/output closure describes that validated input. Returned full workspace and scoped compilation must remain consistent.
5. Receipt consumers validate the bindings their actual contract supplies. Generic immutable receipts do not universally contain draft CAS fields or require equality with the current saved head. An authorized retained historical receipt may execute, while the UI must prevent a result for obsolete intent from becoming its current preview. Proposal-specific draft/workspace bindings remain independently checked.
6. An Apply acknowledgement identifies the revision actually saved. A later Builder read may show a coherent newer head after another write; verify the acknowledged revision and permitted progression rather than requiring every later read to equal an older acknowledgement. An ambiguous lost acknowledgement may coexist with a committed write; never assert that every error implies rollback.
7. Restore invalidates prior work according to the current contract. Later commands use the restored head; old completions cannot re-admit obsolete state.
8. Cancellation/detachment prevents obsolete UI updates. It does not imply the server cancelled or rolled back a request.
9. Unchanged valid operations can complete. Safety must not pass merely because the adapter rejects everything.

Establish command retry/idempotency and receipt expiry/revocation rules from source before encoding them. Any liveness claim must state fairness and service-availability assumptions; do not infer eventual completion from finite safety checks.

**Exit:** each promise has a plain-language harm, source mapping, observation and proposed failure witness. Unsupported intended behavior is recorded as a contract question or product finding, not silently added to the model.

### 3. Complete bounded TLA+ protocol coverage

Reuse or extend ReconcileLifecycle, ReconcileAcceptance, ReconcileRefresh, ApplySettlementRace and DraftSaveTransaction where their abstraction fits. Keep their documented limits. Add composed protocol models only for missing interactions; do not construct another hand model of compiler arithmetic or duplicate the statechart compiler.

Start with two client contexts/writers, three outputs (target, dependency and unrelated output), two snapshot identities, three logical ownership executions to distinguish A→B→A from attempt reuse, and a baseline plus up to three saved revisions. Bound transport retries separately from logical executions using the actual recovery counters; the current mixed-recovery sequence can reach five reconcile requests in one execution. Include two overlapping requests, reordered/duplicate/failed responses, cancellation, conflict/refetch, restore and a backend commit with lost acknowledgement. Record which actors can actually perform each transition. Partition models by protocol boundary if state growth prevents useful runs; record the shared assumptions and test the composition seams explicitly.

These are initial proposed bounds, not completed coverage. Adjust from reachability and real failure histories. Never silently shrink them to obtain a pass. Explore all reachable transitions of each final stated model and report whether replay covers all transitions, terminal schedules or only a subset. Edge coverage alone does not establish every concrete history.

Use the existing pinned TLC and pstack statechart pipeline. Read failures as shortest paths. Every action must point to an actual code boundary and every behavioral property must have a matching real-code fault control.

**Exit:** stated models pass or retain reproducible findings, diagrams exist, positive completion witnesses exist, and no core property has only an abstract-model pass.

### 4. Replay across actual frontend and backend boundaries

First prove one vertical witness using the existing client and HTTP test infrastructure. Reuse it to bind actual mounted Workspace/query-cache requests to production HTTP handlers and service results. Introduce only the transport glue that this witness needs; do not build a generalized bridge before proving that path. Extend existing adapters rather than start a parallel runner. Preserve client decoding, real request identities and real server responses. A mocked response copied from the model cannot establish frontend/backend conformance.

Run actual compilation/receipt validation at the server boundary. Control only external systems at existing production interfaces and label those trusted assumptions. Use owned real-database fixtures for claims about persisted heads, history, receipt storage and actual query rows. Record exact inputs, responses, admitted UI state, persisted state and model observations under one source fingerprint.

Define an explicit abstraction relation in `scripts/lib/formal-core-protocol-replay.mjs`. Map production observations to model variables, specify which events may stutter without changing abstract state, and reject any replay prefix that is not a legal path of the model. Validate both source and destination states and the named action. Every relevant production transition must either map to a permitted model step or have a documented stuttering justification. Selected matching requests and final results alone are not a conformance proof.

Scheduling controls must pause actual async boundaries. Prove the UI can reach each schedule; unavailable controls or impossible requests invalidate a proposed reproduction. Replays must show both rejected stale work and accepted current work. Keep unsupported, malformed, empty, stale or partial evidence from passing.

**Exit:** the projection and legal-prefix checks hold for every claimed replay, and each core property is exercised on relevant production code with positive and negative observations. Any model-to-code mismatch is repaired and described. Trusted/excluded boundaries remain visible.

### 5. Check semantics and refine actual defects

Protocol identity is insufficient if compilation returns the wrong definition or rows. Reuse arithmetic, construction lineage, saved-route, APPEND and publication checks where they apply. Add targeted input families and independent literal row/schema expectations for the operation families identified in step 1. An identical receipt ID or schema alone is not semantic correctness.

For each current-code violation, retain the failing real reproduction, make the smallest authorized product fix, update the affected model/adapters and show that removing the fix reproduces the original failure. Keep unrelated dirty changes intact. Do not weaken a promise, count a tool failure as a caught fault, quarantine a repro or duplicate equivalent tests to inflate coverage.

**Exit:** no unresolved finding is described as verified. Fixes have real regressions and current/broken/restored evidence. Unchecked compiler families are explicit gaps.

### 6. Prove one complete real-app lifecycle

Follow create-verification-skill's Launch, Doctor, Drive, Evidence and Cleanup requirements for the owned target. The existing local entry points are `make dev`, `make dev-doctor` and `make dev-down`; verify their setup, isolation and ownership first rather than assuming they provision a usable empty instance. The authenticated deployed-target skill has a separate launch model and must not be mixed with the local one. Do not run `make acceptance-real` as a passive check or deploy without authorization.

Repair the existing executable browser recipe only as needed. Prove that an isolated instance starts with required schema/seed data, reaches a usable Builder and has an authenticated valid context where required. Capture native user edit/Apply, reconciliation, accepted compilation/preview, a second edit, reload, restore and stable displayed/saved results. Use actual UI controls and public APIs. Force rare scheduling cases in the faster protocol adapters, not dozens of browser journeys.

Doctor before drive and after failures. Verify instance identity, build/source, ports, auth and seed readiness. Preserve sanitized requests/responses, UI observations/screenshots and independent saved-state evidence. Clean only owned resources and confirm the evidence survives cleanup. A component fixture or successful Builder GET alone cannot close this milestone.

**Exit:** the documented recipe has been followed end to end on its declared target. Any unavailable auth/service prerequisite is a precise blocker, not a pass.

### 7. Finish the skill and verify the handoff

Update the existing skill and feature map with the core operation table, exact runnable commands, model bounds, assumptions, source mappings, reviewed hashes/sync commit, failure interpretation and cleanup. Keep the command path short. Mark older plans historical. Advance sync commits only after checking the actual committed code; use working-tree hashes while changes are uncommitted.

Run touched checks while editing and one full self-test at the final frozen checkpoint. Run relevant owned-database profiles and the thin browser lifecycle separately. Keep existing unsharded CI jobs and ten-minute budgets. If a run exceeds its budget, measure the cause before changing execution structure or scope.

Follow the resulting skill cold, including one scratch production fault and restoration. Then use maintain-verification-skill's per-feature source review and coordinator-driven live pass, and maintain-formal-verification-skill for model/code alignment. Independent reviewers must check the same final source/evidence and report remaining gaps. A whole-application maintenance pass remains separate from this core protocol's completion.

**Exit:** a new agent can select the changed boundary, run the check, interpret a failure and maintain it without this conversation. Final evidence refers to the final source, not an earlier checkpoint.

## Scheduling and first work item

Start with step 1, then prove the smallest composed slice: saved Builder head, reconcile compilation, receipt-bound preview and a competing save/refetch. Expand next to restore and late acknowledgements. This tests the core integration before extending peripheral operation families.

Source readers can investigate UI ownership, backend receipts and historical assertions independently. Give concurrent writers isolated worktrees and exclusive files. One coordinator owns shared registry integration, source freezes and any mutable runtime. Do not make more CI shards or cloud jobs a prerequisite; this environment's cloud job endpoint was previously proxy-blocked.

## Final acceptance checklist

- [ ] Current core operation map is complete against actual callers/routes, with explicit exclusions.
- [ ] Every core promise has model or appropriate real-code verification, declared bounds and assumptions.
- [ ] Model steps reflect real atomic/await/transaction boundaries and composition seams.
- [ ] Relevant production UI, HTTP, service, compiler and persistence paths execute in the evidence.
- [ ] Each claimed property catches a matching semantic production fault and accepts valid work.
- [ ] Rare interleavings cover conflict, stale work, retry, cancellation, restore and ambiguous commit outcomes.
- [ ] Semantic outputs are independently checked where claimed.
- [ ] One owned real-app lifecycle is launched, doctored, driven and cleaned with surviving evidence.
- [ ] Skill commands, feature map and source/sync records describe the final implementation.
- [ ] Final combined audit and required service/browser checks pass, or precise blockers/findings prevent completion.

## Plan review status

Reviewed for implementation readiness with one independent inherited-model reviewer. The review required an explicit model/production observation relation and correct treatment of newer reads after an acknowledged save. Both gates are included above. Subsequent handoff reviews added strict mandatory completion, source-backed retry/receipt semantics, deleted-test recovery and a rigorous prevented-schedule route for existing production guards. The artifact table distinguishes existing dependencies from planned deliverables. That review applies to the earlier technical contract. The revised one-model execution policy still needs its target-host trial. This document is not formal verification, cross-model review or passing evidence for unfinished work.


## Normative execution and acceptance contract

This section governs the milestones above. A checked box, successful tool exit, diagram, large state count or agent statement cannot replace the evidence required here. The work is complete only when every mandatory condition below holds on the final source snapshot.

### A. Starting state and permitted work

1. Read `AGENTS.md`, this plan and the project verification skill. Complete H0/H1 for the existing OpenCode execution path. The core-map file is a planned output until M1 creates it; do not treat its initial absence as broken setup. Resolve applicable skills through the host's supported mechanism.
2. Verify the branch and exact working tree against the committed starting point above. Include the selected OpenCode handoff patch and relevant later source. Record `git status`, tracked and relevant untracked identities, hashes and existing processes before editing. Do not reset, stash, clean or overwrite dirty work. Save a reversible task-start snapshot outside Git.
3. Repair verification and make scoped product fixes only for bugs reproduced by this work. Preserve unrelated behavior. Do not add frontend effects, weaken public contracts, or perform unrelated cleanup. Do not commit, push, open external work, deploy outside the documented owned local test stack, message others or delete shared data without separate authorization. Local progress does not require publication.
4. Reuse the current runner, model compiler, mutation overlays, real query hooks, public handlers and existing service fixtures. No new parser, report catalog, generic scheduler, broad framework or resurrected generator without a demonstrated requirement the current tools cannot meet. Document a proposed addition's necessary consumer before writing it.
5. This is a sustained execution policy, not a promise of infinite background availability. The host must keep Qwen running or restart it. A finished or interrupted session does not keep working because this document says “24/7.” Never claim otherwise.

### B. Mandatory scope and operation-map acceptance

The core map is the single maintained scope record. Each operation/property row must contain a stable identifier, user action, actual caller and HTTP method/path when present, handler/service/compiler boundary, required identities, state read/written, model action/property, real-code check, positive witness, source fault, current evidence location and remaining assumptions. Use concise rows and linked details, not a generated obligation bureaucracy.

An operation is not covered merely because its route appears in a test or model. Distinguish `planned`, `in-progress`, `passed`, `failed`, `blocked`, `not-run` and `not-applicable`. `Not-applicable` requires source evidence that the behavior does not exist or is genuinely outside the user's stated core scope. Lack of a fixture, service, credential, model or time is not non-applicability.

The following remain mandatory until source inspection establishes the exact variants:

- Initial Builder hydration, explicit reload and cache/refetch admission.
- Every UI-accessible command family, batching, queue admission, proposal Apply and previous-revision restore. A shared CAS test covers shared CAS only, not each command's semantics.
- Whole-workspace reconciliation and output-scoped reconciliation with current-draft dependencies.
- Compilation request/result identity, receipt construction, validation, lookup and supported execution/recompile behavior as actually exposed.
- Receipt-bound preview, current UI admission and error recovery.
- Context/output/Explorer changes, cancellation, unmount, overlapping requests, restore and concurrent saves across those boundaries.

Inventory publication's dependency on the same protocol. Any unresolved stateful interaction remains a gap, even if a separate model exists for part of it. Non-core product families can remain excluded with source-backed reasons. Do not claim the entire frontend/backend operation set is verified.

**Map acceptance:** cross-check the actual UI client calls and public definitions in both directions. Every discovered mandatory call/variant has a row; dead/generated-but-unused routes are distinguished from active callers. A reviewer can follow every covered row into production source and current executable evidence. A new core caller must either acquire a row or cause an explicit inventory failure; implement the smallest existing-inventory check that detects this drift.

### C. Mandatory scenario floor

These scenarios are a minimum, not a substitute for bounded exploration. Split a row into distinct schedules wherever actual await boundaries allow different outcomes. Every row needs source entry points, exact requests, observed UI/server state, expected result, a valid-success companion and its coverage classification.

Some harmful interleavings are already prevented by production UI/queue guards. A mandatory scenario can pass in `executed` mode or rigorously evidenced `prevented` mode. Prevention requires all of the following: identify the real guard and preconditions; observe the actual control/request prohibition; exercise a valid-success companion; and remove the relevant guard in a scratch overlay to demonstrate that the forbidden behavior is caught. Where defenses overlap, identify the smallest necessary guard set and count its combined edit as one fault rather than claiming each edit independently exposes harm. The model must reflect the actual guard, not pretend the forbidden transition executes normally.

Record prevented schedules separately from executed attacks, exclusions and non-applicability. Missing services, unavailable fixtures, failed driving or guessed unreachability do not qualify as prevention. Never alter product behavior merely to enable a test, force inaccessible state through internal setters, or claim an unexecuted harmful sequence ran. If neither valid execution nor the prevention proof is available, the scenario remains incomplete.

| ID | Required reachable sequence | Acceptance observation |
| --- | --- | --- |
| C01 | Start Builder GET A; save/admit B; deliver late GET A. Repeat with forced reload and cache publication. | Within the same ownership context, late A cannot regress admitted B or bind the next command to A. A legitimate new-context load still succeeds. |
| C02 | Submit two queued mutations; pause query-owner drain; change authoritative identity before releasing the queued mutation. | Observe the actual identity read after drain. Admit/reject according to current intent and coherence; never infer safety only from request CAS. The queue settles and pending state returns to its correct value. |
| C03 | A command conflicts; production forced refresh admits a new snapshot/head; issue the next visible command. | Exact refreshed identity is sent. The stale intent that caused the conflict is not silently replayed. A valid new command saves successfully. |
| C04 | A command fails or is rejected while a related-query owner is paused; replace that owner or advance its draft before finalization. | Only the still-matching owner/identity may resume. Success does not accidentally resume obsolete work; failure does not wedge the unchanged valid owner. |
| C05 | Reconcile saved A; save B while compilation of captured A is in flight; complete A. | Backend receipt describes captured A where the contract allows it. UI admission follows current intent/ownership, so A cannot replace B's current preview. Valid unchanged A still compiles and previews. |
| C06 | Reconcile one output with a dependency and an unrelated output, then reconcile the complete workspace. | Independent literal output/dependency membership and schemas match the requested closure. Output-scoped result returns the coherent full validated workspace while compiling only its required closure. |
| C07 | Fail reconciliation with draft conflict/desynchronization and finish its forced refresh. Change owner/context during that await. | Distinguish refresh-and-stop from retry. Observe whether state is hydrated, whether another request is sent, and whether obsolete refresh results are ignored. |
| C08 | Produce stale-snapshot failures through the configured stale-refresh budget, including missing/failed refresh data. | Exact refreshed snapshot/CAS values feed permitted retries. Exhaustion repairs/stops according to source. Obsolete owners cannot continue retrying. |
| C09 | Produce retryable failures through the ordinary retry budget, changing ownership during the retry-delay await; mix ordinary retry and stale refresh. | Exact request counts and stop conditions follow both counters. No retry after ownership loss. A later successful allowed attempt can complete. |
| C10 | Return a receipt/response with one mismatched binding at a time, including project/Explorer/output/snapshot or proposal-specific bindings. Also reuse an opaque receipt label across attempts. | The actual validating boundary rejects mismatches it promises to validate. UI current-attempt ownership cannot be inferred from receipt text alone. Equivalent normalized valid identities remain accepted. |
| C11 | Create a valid frozen receipt; save a newer draft; execute that older receipt while its original snapshot is retained and authorized. Then revoke/unretain the snapshot. | Historical execution may succeed under the actual immutable-receipt contract. It cannot be admitted as current UI work without matching current intent. Revoked/unretained execution fails at the authorization boundary. |
| C12 | Start preview A; edit, switch output, change context or start B; finish A and B in both orders with success and error variants. | Old completion cannot replace current rows, clear B's loading state, show its error as B's, or release B's owner. Current B can finish. |
| C13 | Cancel or unmount during command, compile, recovery and preview awaits; deliver late success/error. | No obsolete visible state admission or retry. Observe actual detach semantics; do not assert transport cancellation implies server rollback. Owned pending resources settle. |
| C14 | Reuse identity A after A→B→A, with at least three attempts; reuse receipt/request labels where the contract permits. | An old A attempt cannot own the new A attempt solely because values match. Valid new A completes. Alternate concrete prefixes are replayed where the model otherwise merges histories. |
| C15 | Restore a previous revision while a command, reconcile, recovery GET or preview is pending; complete old work afterward. | Restore and subsequent admission follow actual CAS/ownership contracts. Old work cannot reintroduce obsolete current intent. The next command binds to the restored or legitimately newer coherent head. |
| C16 | Two writers target one saved head; then repeat with a delayed loser. | Observe one permitted winner, typed conflict behavior, exact saved head/history and coherent recovery using actual transaction evidence. Do not use a trusted in-memory store to claim database isolation. |
| C17 | Commit a save, lose its acknowledgement, then read/retry/recover; separately observe a newer concurrent revision after an earlier successful ACK. | Distinguish committed-but-unconfirmed from rolled back. ACK identifies the committed revision; later coherent heads are permitted. Retry follows observed idempotency/CAS behavior, not invented exactly-once guarantees. |
| C18 | Deliver malformed/incomplete decoded data, missing output/receipt, compile failure, unsupported receipt contract and invalid preview response. | Correct boundary reports the intended typed failure without publishing a partial successful result. Relevant valid/error-free companions work. Retained prior state is distinguished from newly accepted state. |
| C19 | Run the same authored definitions through compile/receipt/preview and independently evaluate expected schema and rows. Include a saved edit and restore. | Exact public identities, types, cardinality, values, nulls and duplicates match the specified fixture. Expectations are not computed by the compiler implementation under test. |

Confirm these source facts again before modeling them. At plan review, `BuilderWorkspace.reconcileCurrent` had `maxAttempts = 3` and `maxStaleRefreshes = 2`, enforced by different recovery branches. Three is not automatically the maximum number of HTTP requests across mixed branches; two ordinary retries followed by two stale refreshes can permit five attempts. Model the actual counter interaction and exhaustion boundaries. Do not truncate reachable recovery because the initial ownership example used three attempts.

At plan review, `internal/explorer/lifecycle/preview.go` validated immutable receipt route, retained/authorized snapshot, generation, scope and output membership. It did not universally require equality with the newest draft. `CompilationReceipt` and proposal-specific binding records are different contracts. Preserve these distinctions rather than making an appealing but false “all old receipts are invalid” invariant.

### D. Model and conformance acceptance

- State bounds, initial states, external choices, fairness assumptions and trusted primitives are explicit in the core map and output. Safety exploration completes with no cut state space or unchecked configured rules. A diagnostic partial run cannot satisfy the final gate.
- Tie every modeled atomic action to a real indivisible step. Split network awaits, queue drains, compiler callbacks, transaction publication and acknowledgement. Any trusted atomicity assumption is named and checked at another boundary where required.
- For each observed event, the adapter validates input shape, source state, action and destination state under the documented projection. Reject illegal replay prefixes, including wrong order, omitted relevant transitions and substituted identities. Never allow arbitrary stuttering to erase the guarded action or visible state change.
- Reject missing, duplicate, foreign, stale or truncated graph/trace observations. Independently exercise these rejection paths with real malformed artifacts. Unknown actions fail closed until mapped or justified.
- Report complete bounded TLC exploration separately from replayed model transitions, concrete schedules, generated input families, source controls and ordinary tests. Do not sum these categories or convert test counts into a completion percentage.
- Complete edge replay is not every execution history. Declare the concrete history horizon and branch choices. Replay materially distinct prefixes for A→B→A, retry budget state, reused labels, restore and queue ownership. If two apparently equal model states can produce different allowed concrete behavior, enrich model state or justify the abstraction before claiming coverage.
- The model cannot manufacture the response used as evidence of production behavior. Production requests/responses and persisted bytes are observed independently. Compare them to the model; do not overwrite them with expected values to continue a path.
- Include anti-vacuity observations. Required valid operations reach an actual successful result; stale/forbidden operations reach the intended rejection. A checker that prevents every request is broken.
- Do not claim a full refinement proof from sampled conformance traces. State exactly what the chosen exploration/replay strategy establishes. Any remaining abstraction assumption relevant to a mandatory property is reviewed and visible in the final claim.

### E. Fault-control and defect-refinement acceptance

Every claimed safety property has a targeted semantic source fault in the relevant production boundary. Reuse a fault across properties only when each property independently observes the intended violation. Register each unique fault once; cross-profile reuse is not new unique detection.

A passing fault-control record requires current source passes, the scratch fault reaches the intended production branch and fails the named semantic assertion, and restored source passes. Record source/overlay hashes, selected test/property, actual process execution and exact failure. Import/parse/type errors, tool exits, missing fixtures, timeouts, zero tests, skips and unrelated earlier assertion failures earn no fault credit.

For a real current-code bug, the order is reachable reproduction, reader-visible harm, minimal product fix, passing relevant regression, reintroduced defect caught, and aligned model/diagram/source mapping. Separate verification changes from product changes in the review. Identify introducing history when available; say unknown if not established. Do not fabricate historical causality.

Zero new product bugs is an acceptable outcome if bounded checks and fault controls are meaningful. Do not invent defects or make cosmetic product edits to satisfy a perceived bug quota.

### F. Evidence, resources and restart acceptance

The final `core-protocol/conformance.json` extends the existing evidence conventions. It must identify the final tracked and relevant untracked source manifest, baseline commit, tool hashes/versions, selected model/config/bounds, property/scenario IDs, actual commands, named executed assertions, executed-versus-prevented scenario evidence, observed outcomes, positive witnesses, semantic faults and restored results, runtime identity where used, and links to original process/trace/database/browser artifacts. Keep one result source of truth; derive the human summary from it rather than maintaining a second catalog.

Statuses distinguish selected-but-failed/incomplete, not-run, blocked and passed. A missing service cannot turn a selected integration profile into “not requested” or pass. Discovered checked-in baseline metadata and old `/tmp` reports are useful history; they cannot prove changed current source.

Freeze implementation during a final evidence run. Verify source hashes before and after. If relevant source changes, rerun affected evidence before claiming a final pass. Tool/environment changes that could alter a result also invalidate it. Never repair a broken hash by updating pins without inspecting source and rechecking the affected promise.

Record every owned process, container, port, database and scratch namespace when created. Coordinate shared mutable resources serially. Cleanup targets only recorded owned identities and checks that artifacts survive. Do not delete the canonical development instance, unrelated volumes/worktrees or previous evidence. Retain failure artifacts through cleanup. Redact credentials and keep runtime auth out of committed files and reports.

Keep one resumable local checkpoint at `/workspace/work/loom-core-protocol/CHECKPOINT.md` with last verified milestone, active task, next executable step, source identity, evidence paths, unresolved findings, blockers and owned resources. If `/workspace` is unavailable on the execution host, set and record an equivalent outside-Git work directory once. This local state is not a dependency of the finished skill. On resume, inspect actual files and resource ownership; do not trust an earlier agent's “done.” Preserve evidence outside ephemeral sessions before a host replacement.

If the same command fails twice with unchanged inputs and the same cause, stop blind retries. Diagnose or use an authorized equivalent tool/environment. Record the blocker and continue independent milestones. Do not replace the required real boundary with a mock, shrink a model or relax an assertion to bypass it. If all remaining work is blocked, checkpoint as incomplete with concrete prerequisites. Do not run an idle loop that claims progress.

### G. Milestone receipts and final decision

For each milestone, put its evidence receipt in the local checkpoint. A receipt contains its source manifest, exact successful and failing commands, outputs, scope/assumptions, faults and remaining dependencies. The maintained core map links to the meaningful final evidence, not every exploratory run.

| Milestone | Required receipt before advancing |
| --- | --- |
| H0. Existing OpenCode harness | Actual harness/plugin/model identity, tool execution, long-command result collection, compaction, restart and manual stop/resume proven on disposable work. No new framework or dispatcher. |
| H1. Execution host | Exact tools including explicit local-mode Go, persistent paths, skill discovery, owned service/browser readiness and supervised Qwen tool-use trial. |
| M0. Baseline and ownership | Correct dirty snapshot, usable tools, baseline results with failures classified, owned resource inventory and saved next step. |
| M1. Core map and contracts | Bidirectional caller/route accounting, command variants, stable property IDs, mandatory/optional scope, targeted deleted-test recovery decisions, source-backed identity/recovery semantics and first vertical witness selected. |
| M2. First composed witness | Actual UI request reaches real HTTP/service compile and preview; observed result satisfies projection and one competing save/refetch schedule; relevant semantic fault is caught. No generalized bridge is built first. |
| M3. Bounded protocol coverage | Complete declared TLC exploration, legal replay prefixes and mandatory C01–C18 scheduling/error families, with precise coverage horizons and no guessed unreachable steps. |
| M4. Persistence and semantics | Required real-store schedules, independent row/schema cases including C19, precise committed/ACK distinctions and all relevant source faults restored. |
| M5. Real-app lifecycle | Launch from documented usable setup, doctor, actual user-driven lifecycle, authoritative side effects, cleanup and surviving sanitized evidence on the declared target. |
| M6. Handoff and review | Final frozen combined audit, necessary integration/browser checks, cold execution of the skill, maintained source records and independent review of final diff plus mandatory evidence. |

M2 is intentionally early. Do not spend the run generating inventories and new abstractions before proving one real UI/backend path. Source investigation and concrete regressions may interleave with the model work when that exposes an abstraction error earlier.

**Completion is a conjunction, not a score:** every mandatory operation, property and scenario has the required final-source passing evidence; every stated safety property has valid fault and positive controls; no unresolved mandatory product defect exists; all required services and real-app boundaries ran; source/tool integrity and cleanup hold; and independent final review finds no blocking gap. Missing any term means incomplete. A documented exclusion, blocked browser, skipped service, partial model or review-pending state cannot be averaged away.

“Poteto approved” has no external certification mechanism here. Use it only as shorthand for a named independent review of the final implementation and evidence against this contract. A self-awarded label, plan endorsement or reviewer agreement without inspecting artifacts is not approval. Record reviewer capability and unavailable diversity honestly. If the execution host cannot obtain independent review, finish the verifiable work and report review pending; do not invent a reviewer.

The final report states what was verified, the actual bounds, production bugs/fixes if any, exact commands/evidence, remaining exclusions, cleanup status and commit/publication state. It must identify incomplete mandatory work before discussing optional future improvements.
