# Qwen handoff for Builder core verification

## Assignment

Implement and verify `docs/verification/CORE_PROTOCOL_VERIFICATION_PLAN.md` to its normative acceptance contract. The goal is dependable verification of actual frontend interaction with Builder, reconciliation, compilation receipts and preview, including real defects and their minimal fixes where authorized. Producing a plan, more models, more tests or green reports is not the completion condition.

The plan is authoritative. Read it in full before editing. Its artifact table names existing inputs and planned outputs. Its required-skill table identifies create-formal-verification-skill, maintain-formal-verification-skill, create-verification-skill, maintain-verification-skill, poteto-mode and statechart. Read their actual instructions and the current project skill. Do not merely copy their names into a report.

## Snapshot and ownership warning

This work was prepared in `/workspace/loom` on `feature/deep-clean`, based on `e736e130`. There is substantial uncommitted modified and untracked implementation. This handoff and the new plan are also uncommitted at preparation time. A new remote job that checks out the branch's committed HEAD alone will miss them.

Use the same workspace or obtain the complete intended dirty snapshot, including untracked files, before proceeding. Verify actual source and branch instead of trusting this historical description. Preserve all pre-existing work. Do not reset, clean, stash, mass-restore from Git or change deployment target. Save your own task-start snapshot outside Git and record it.

Nothing in this handoff starts a background job. Qwen's host must keep the worker alive or resume it. Do not claim to be working after your process/session stops.

## First execution cycle

1. Read `AGENTS.md`, the complete plan, the project verification skill and the named required skills. Locate the current runner and API/UI code from the plan's source anchors.
2. Inspect the current working tree and owned resources. Record source identity and baseline in `/workspace/work/loom-core-protocol/CHECKPOINT.md`, or the documented equivalent on your host. Do not require earlier agents' `/tmp` artifacts for the finished workflow.
3. Run the existing relevant checks as-is. Keep source/tool failures distinct from property failures. Record blocked infrastructure precisely and continue independent work.
4. Build the compact operation map at `.codex/skills/builder-interaction-formal-verification/core-protocol.md`. Start with actual Builder load/commands, `reconcileCurrent`, backend `ReconcileWithWorkspace`, compilation/receipt validation and receipt-bound preview. Inspect deleted tests for these promises before inventing replacements.
5. Prove the first joined witness through actual UI decoding, production HTTP/service compilation and preview, then add a competing save/refetch. Do not build a generalized new framework first.
6. Continue through M0–M6 and C01–C19. Implement the minimum missing model/adaptation/regression, check it, record evidence, then select the next mandatory gap.

Existing baseline commands from the repository root are:

```sh
node scripts/verify-builder-formal-core.mjs --phase behavior --list
node scripts/verify-builder-formal-core.mjs --phase inventory
node scripts/verify-builder-formal-core.mjs --self-test --out /tmp/loom-qwen-baseline-formal
node scripts/verify-builder-formal-core.mjs --phase journey --feature operation-history-routine --out /tmp/loom-qwen-baseline-journey
```

Use fresh output directories on every run. These commands do not by themselves satisfy the planned composed protocol. Install/use the exact repository toolchain and pinned formal tools following the existing skill; do not silently replace them. In the preparation workspace, the compatible Go executable was `/workspace/.loom-setup/go/bin/go`; verify availability/version on the execution host and set `GO` accordingly. Missing dependencies are setup work, never passing proof.

## Continuous execution policy

Continue reversible authorized implementation without repeated approval requests. Read the plan's scope and publication limits. Keep product fixes attributable to real reproduced defects. Do not weaken the property, lower bounds, remove a mandatory scenario or update reviewed hashes solely to make a report green.

One iteration must end in a concrete result: newly exercised production behavior, a caught semantic fault, a reproduced/fixed defect, a repaired model/code mismatch or a diagnosed blocker. Documentation-only progress is insufficient after the initial map when an executable next step is available. Zero discovered product defects is acceptable; fabricated bugs are not.

After each verifiable unit, update the single local checkpoint with the last verified milestone, source identity, evidence, current task, next exact action, owned resources and blockers. On context reset or host restart, reconcile the checkpoint with actual files/processes and resume from the first unmet acceptance condition. Do not restart the entire audit or blindly rerun unchanged passed checks.

If one milestone is blocked by auth, services, history or tool capabilities, continue independent work. Diagnose repeated identical failures instead of retrying forever. Do not replace a required real boundary with a mock. If every remaining mandatory step is blocked, finish the session as incomplete with a precise prerequisite list and preserved evidence.

Run parallel read-only discovery where useful. Concurrent writers use isolated worktrees and exclusive files. One coordinator integrates shared registries and owns source freezes and mutable runtimes. If your runtime lacks subagents, work serially and record that limitation. Do not invent cloud jobs, model diversity or independent reviews.

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

The final report must name verified promises and bounds, real bugs/fixes, exact commands and evidence locations, remaining gaps, resource cleanup and commit/publication state. Do not commit, push, deploy or perform external communication unless separately authorized. The next human should be able to distinguish useful verified work from unfinished obligations without reading the full chat.
