# Sustained Builder verification with one Qwen model in OpenCode

## Assignment

Implement and verify `docs/verification/CORE_PROTOCOL_VERIFICATION_PLAN.md` to its normative acceptance contract. The goal is dependable verification of actual frontend interaction with Builder, reconciliation, compilation receipts and preview, including real defects and their minimal fixes where authorized. Producing a plan, more models, more tests or green reports is not the completion condition.

The plan is authoritative. Read its **Execution through the existing OpenCode harness** section first, including the one-model execution and debugging rules. Preserve the complete M0–M6 and C01–C19 scope. This is a multi-day program of bounded tasks, not an instruction to stop after one task. Reuse demonstrated H0/H1 capabilities and current evidence; do not restart setup on each turn. Load applicable skills from their installed host locations.

Use the existing OpenCode harness with the operator's Qwen endpoint on the RTX PRO 6000 Blackwell, 96 GB. Record the actual harness/plugin versions, selected model, exact checkpoint and quantization during preflight. Reuse its Session, task/Goal and continuation tools where available. No orchestration migration, local Source adapter, GitHub account or new dispatcher is required. Keep local Git reads for source review. Publication and deployment outside the owned local test stack require separate authorization.

Bind the complete program to its intended Session and clearly named Goal. Use the same model to select a task, implement it, and inspect its evidence as separate operations. No second loaded LLM or passive external monitor is required. A fresh same-model review context is optional, not independent approval. Preserve unrelated goals. Use supported tools to manage goals and evidence; never hand-edit goal JSON or reuse another task's model settings.

## Snapshot and ownership warning

The plan and prior implementation are committed at `dc9016bc9080596099c1ad0253ea3ee229544c58` on `feature/deep-clean`. The OpenCode handoff changes may be a later uncommitted patch. Record the actual HEAD, tracked diff and relevant untracked source before transfer. Do not copy unrelated local artifacts, credentials or caches into the worker snapshot.

Obtain the intended committed checkout plus any explicitly selected later patch and relevant untracked source. Verify hashes after transfer. Preserve pre-existing work. Do not reset, clean, stash, mass-restore from Git or change deployment target. Save your task-start snapshot outside Git and record it.

Nothing in this handoff starts a background job. Qwen's host must keep the worker alive or resume it. Do not claim to be working after your process/session stops.

## Build and test Loom from OpenShell

Use the shared Linux source checkout selected by the OpenCode launcher. The agent can edit that checkout inside OpenShell. Loom's API, UI, ArangoDB and ClickHouse run in separate Docker containers. The API and UI mount the same source read-only. The Mac checkout is separate. Rebuilding does not export edits to the Mac or synchronize newer Mac changes into OpenShell.

Before H1, record `pwd`, `git rev-parse HEAD` and `git status --short`. Confirm that the complete plan and required project skills exist in this checkout. An older shared snapshot may lack the current handoff or implementation. Stop source-dependent execution until the intended source and selected patches have been transferred and checked. A copied handoff document alone does not update product source.

The scoped Docker bridge supports rebuilding this dedicated local test stack. On an already registered shared-volume stack, run these commands inside OpenShell:

```sh
docker loom --help
docker loom rebuild all
docker loom status
```

Use `docker loom rebuild api` or `docker loom rebuild ui` to rebuild one service. The rebuild command starts a background operation. Poll `docker loom status` until its JSON reports `succeeded` or `failed`. Record the status and build log with the task's evidence. Only one rebuild can run at a time. Do not start a second rebuild or edit tested source during runtime verification.

The host Docker engine builds `Dockerfile.dev` from the shared repository root and `ui/apps/demo/Dockerfile.dev` from its `ui` directory. Dockerfiles and dependency manifests can change. Deployment uses the host's registered Compose configuration. Editing `compose.dev.yaml` does not change that registration. Ask the operator to update the registration if a test needs different ports, services or mounts. The sandbox has a scoped `docker` command, without a Docker socket or general daemon access.

After both builds pass, the bridge replaces the selected application containers, retains database volumes, checks readiness and refreshes its allowed container IDs. A build failure prevents deployment. A deployment failure may leave part of the test stack updated. Inspect status and logs before retrying. Rebuilding this owned local fixture is authorized for this assignment. Publication and deployment to any other environment still require separate authorization.

After status reports `succeeded`, check the deployed source and endpoints:

```sh
docker exec "$LOOM_CDA_API_CONTAINER" /workspace/loom-dev-build-stamp.sh --check
curl --fail --silent --show-error "${LOOM_CDA_API_ORIGIN:?}/readyz"
curl --fail --silent --show-error --output /dev/null "${LOOM_CDA_UI_ORIGIN:?}/"
```

Use the launcher's `LOOM_CDA_*` values. The current shared stack uses API port 8183 and UI port 3183. Keep the existing ownership and source-digest checks. A healthy container or successful build does not prove the protocol's acceptance conditions. Run the project's required verification after these setup checks.

If the scoped command is missing or reports that access is not configured, run this setup on the Mac host:

```sh
cd ~/Desktop/ARC-LLM-SETUP
python3 tools/openshell/enable-loom-docker.py oc-loom-qwen \
  --project loom-dev-qwen-current --port 18185 --restart
```

This command applies to the existing registered stack and sandbox. Use their actual registered names if the deployment changes. It installs the command and refreshes the bridge without restarting the model server or OpenCode. See `tools/openshell/README.md` in ARC-LLM-SETUP for the shared-volume setup and access limits. Do not replace this setup with an unrestricted Docker socket mount.

## Resume or begin execution

1. Read `AGENTS.md`, this handoff and the plan. Inspect the actual branch, dirty files, live jobs, checkpoint and existing raw evidence. Preserve work already in progress. Reconcile task statuses with what the evidence actually proves; do not trust a checkpoint's completion claim alone.
2. Complete only the missing or invalidated parts of H0/H1. Record actual tool paths and owned fixture access. Reuse working continuation, environment and baseline results when their relevant source/environment identity is unchanged. Do not run every baseline command just because the instructions were updated.
3. Inspect the first joined UI/client/server witness if present. Reuse it when it truly connects production client requests/decoding to production handlers and displayed results. Distinguish a request held before server entry from a compile already in progress, and client cancellation from a delivered late response. Keep missing assertions as gaps instead of recreating a working driver.
4. Select the next unmet mandatory guarantee from the core map. Recover relevant historical assertions and choose the smallest execution boundary. Define one task using the template below, then implement, check, record and continue.
5. Work through the full M0–M6 and C01–C19 program using the plan's work-area routing guide. Completing the current task never limits how many subsequent tasks you perform. Keep one writer per shared file or worktree, delegate independent work in parallel, and serialize mutations of a shared runtime.

Existing baseline commands from the repository root are:

```sh
node scripts/verify-builder-formal-core.mjs --phase behavior --list
node scripts/verify-builder-formal-core.mjs --phase inventory
node scripts/verify-builder-formal-core.mjs --self-test --out /tmp/loom-qwen-baseline-formal
node scripts/verify-builder-formal-core.mjs --phase journey --feature operation-history-routine --out /tmp/loom-qwen-baseline-journey
```

Use fresh output directories on every run and retain required evidence on persistent storage. These commands do not satisfy the planned composed protocol by themselves. Set `GO` to the absolute compatible executable, then check `GOTOOLCHAIN=local "$GO" version`. Install pinned formal tools following the project skill. Historical `/workspace/.loom-setup` paths are not portable defaults. Missing dependencies are setup work, never passing proof.

## Parallel agents on one served model

One-model execution means one model checkpoint/provider, not one agent or one serial workstream. Follow `AGENTS.md`'s Parallel work instructions: proactively delegate useful independent work without asking the operator to request subagents. In OpenCode, launch background tasks with `task(background=true)`, up to five workers alongside the parent when serving capacity permits. Give each a concrete deliverable and explicit ownership; keep the parent integrating results and progressing a separate ready task.

One implementation writer means one owner per shared file, worktree, or mutable runtime, not one worker for the whole program. Use separate worktrees or isolated copies for concurrent implementation and fault experiments. Independent source investigations, coverage analysis, reviews, and isolated verification runs can run concurrently. Preserve source freezes, unique TLC metadirs, and serialized mutations of a shared runtime. A short local edit may stay direct; a substantial task should not stay serial merely because the operator has not asked for delegation.

## Current bounded task within the full Goal

Use this template for the current bounded task in the existing Session/Goal. Keep the complete acceptance contract in the plan instead of repeating it in every prompt. Do not create another queue or require a fresh Session for every task.

```text
Program: complete M0–M6 and C01–C19 under the existing plan and Goal.
Current task: <stable ID>; milestone/scenario: <ID>.
Observed gap and user-visible harm: <one concrete promise>.
Checkout/source identity, existing evidence and owned processes: <paths/hashes>.
Exclusive write scope and production entry points: <files/functions>.
Existing check/helper and relevant historical assertions to reuse: <locations>.
Expected observable result and exact focused command: <assertion/command>.
If a check is missing, its named consumer and smallest deliverable: <details>.
Valid/fault/restored observations required by this claim: <named failure>.
Evidence and checkpoint: <fresh persistent paths>.
Next useful diagnostic if the check fails: <specific uncertainty to resolve>.
Task exit: <verified result or precise diagnosed blocker>.
Then inspect the evidence, checkpoint it, and immediately select the next
runnable requirement. Keep the overall Goal incomplete until M6 passes.
```

Use checkpoint descriptions for task disposition and the harness's supported status values for its actual tools. Link evidence to existing Goal requirements where supported. Completing a todo, editing a file or increasing a progress counter is not acceptance. Automatable checks should establish facts without a human approving every task. The same model's evidence inspection does not replace independent final review.

## Skills applied to concrete decisions

Read installed skill instructions and the host compatibility adapter. OpenCode may expose skills without the `pstack:` prefix; use actual discovery. Keep repo-local verification instructions and their source maps authoritative for executable commands.

- Use poteto-mode for small coherent implementation and concise reports.
- Use maintain-formal-verification-skill when code/model/observations disagree. Classify in sync, updated, bug or blocked. Do not restart a broad audit for each correction.
- Use create-formal-verification-skill only for a missing promise that existing checks cannot establish. Extend this project's skill/runner. A new model is not the default deliverable.
- Use statechart to interpret model results and reachable counterexamples, then replay them on actual code.
- Use the applicable repo verify/verify-loom/verify-loom-ui skill for the required component, API, service or browser boundary. Use create-verification-skill only when that execution path is absent.
- Use maintain-verification-skill at coherent checkpoints to keep coverage and runnable procedures honest. Reserve adversarial review skills for a meaningful bounded diff with available review capability.

Do not stack all workflows on every task. Applying a skill must resolve the named uncertainty or produce usable evidence. Another skill invocation, diagram, report or test count is not progress by itself.

## Continuous async execution

Keep working across turns and days through the existing OpenCode continuation policy. No routine acknowledgement or per-task permission is required for authorized reversible work. Select the next unmet requirement after every completed unit. Follow the plan's investigation rules instead of repeatedly running the same command.

During debugging, select the exact test name or narrow replay/API case. Keep diagnostic output and actual exit status visible. After a focused correction passes, run the affected file/feature once. Reserve combined audits for integration checkpoints. Two identical failures with unchanged inputs require a new observation or hypothesis, not another full run. After two materially different approaches produce no new evidence, checkpoint and change the approach or take independent work. A 60-minute investigation checkpoint does not authorize weakening checks, inventing closure or killing a healthy verifier.

Every cycle should yield an observed production behavior, reproduced defect, caught semantic fault, repaired mismatch or diagnosed blocker. Record infrastructure repairs as infrastructure work. Stop expanding them when the demonstrated blocker is removed. Most controlled races belong in focused mounted/server checks; reuse the working browser path instead of building a browser journey for every scenario.

Use fresh evidence directories and unique temporary diagnostic markers. Preserve failures. Collect long-command results through demonstrated harness job controls; do not default to `nohup`, long sleeps or broad process-name counts. If H0 establishes that a detached process is necessary, own its exact identity and retain its exit-status record and cleanup proof. Do not launch a duplicate to compensate for an unresolved run.

Checkpoint the source identity, evidence, active command/processes, owned resources, current result, blockers and next exact action after each unit and before yielding where possible. On restart/compaction, inspect live files/processes before resuming. Reuse passed checks only when relevant sources and environment remain applicable. Do not restart the entire investigation.

Repair authorized local environment/tooling problems, including the documented OpenShell Loom rebuild. Preserve unrelated data and do not loosen isolation. If one task is blocked, keep progressing on independent mandatory work. Do not ask for advice merely because a debugging problem is difficult.

Stop only when the complete acceptance contract is satisfied, the operator explicitly stops you, or no authorized useful task remains. For a manual blocker, report the exact environment/access/review prerequisite, attempted remedies, surviving evidence and smallest operator action. Independent final review remains mandatory; if it is the sole missing gate, report review pending rather than pretending same-model agreement is approval. Do not manufacture activity just to keep running.

## Instruction to start or resume the overall Goal

Pass this to the existing OpenCode Session after transferring these updated documents into its actual checkout:

```text
Read AGENTS.md, docs/verification/CORE_PROTOCOL_QWEN_HANDOFF.md and
docs/verification/CORE_PROTOCOL_VERIFICATION_PLAN.md.

Continue the complete Builder core verification program through M0–M6 and
C01–C19 using the existing one-model OpenCode harness. The scope remains the
whole program, even though you execute one bounded task at a time. Work
asynchronously across turns and days until acceptance, explicit stop, or a
manual prerequisite blocks all useful remaining work.

Reconcile the current diff, live jobs, checkpoint and raw evidence first.
Preserve existing implementation and reuse valid completed work. Do not
restart planning, H0/H1 or the broad baseline merely because this handoff changed.

For each unmet requirement, define the concrete gap, reuse the closest code
and historical assertions, implement the smallest check/fix, inspect actual
results, record evidence and immediately continue. Follow the plan's focused
testing, stalled-investigation, review and recovery rules. Do not build another
framework, run every skill on every task, or make every scenario a browser test.

Use supported Goal tools and keep the overall Goal incomplete until the final
acceptance conjunction, including independent review. Same-model self-review
is useful but cannot supply independent approval. No second model is assumed.

Preserve the current authorization boundaries and documented local rebuild
permission. Begin with the next runnable task and report concrete progress
without waiting for routine confirmation.
```

## Important semantic traps

- Historical frozen receipts may remain valid for server execution after later draft saves. Current UI admission is a separate question. Generic receipts do not universally contain draft CAS fields.
- Output-scoped reconcile compiles a dependency closure while returning the full validated workspace.
- Reconcile has separate ordinary-retry and stale-refresh budgets. Verify mixed recovery paths; three ordinary attempts does not cap every reachable mixed sequence at three requests.
- A request error or cancellation does not prove a server write rolled back. Lost acknowledgements and newer concurrent reads must be represented honestly.
- Full bounded model exploration, model-edge replay, actual concrete schedules and browser/database evidence prove different things. Report them separately.
- A real production guard may prevent a proposed harmful interleaving. Follow the plan's strict prevented-schedule proof, including observed prohibition, valid companion and scratch guard fault. Do not bypass the guard to fake normal reachability or mark missing infrastructure as prevention.
- The deleted tests are valuable assertions and oracles, not current proof. Adapt relevant pieces to the correct execution boundary. Do not restore the discarded 78-case generator or retired reporting framework for volume.

## Required final decision

Complete means every mandatory term in the plan's final conjunction passes on the final source snapshot. A documented exclusion, missing service, skipped case, partial model, unavailable required browser target, unresolved bug or missing independent review leaves the task incomplete.

Obtain an independent review of the final diff and actual evidence, not only this plan. Record the review's capability and limitations. Do not self-award “poteto approved,” fabricate cross-family review or claim formal proof from model counts. If review cannot be obtained, report implementation evidence and review pending.

The final report must name verified promises and bounds, real bugs/fixes, exact commands and evidence locations, remaining gaps, resource cleanup and commit/publication state. Do not commit, push, deploy outside the owned local test stack or perform external communication unless separately authorized. The next human should be able to distinguish useful verified work from unfinished obligations without reading the full chat.
