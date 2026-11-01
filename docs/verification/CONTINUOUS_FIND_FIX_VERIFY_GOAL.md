# Continuous Loom find–fix–verify goal

Use the text below as the replacement objective of the existing OpenCode Goal. Update it through the harness's supported tools. Writing this file does not modify the running Goal or grant the running agent new permissions until the operator supplies this objective.

---

Run a continuing find–fix–verify program for Loom. Improve actual product correctness by discovering reproducible defects, repairing their root causes, and retaining trustworthy regression and formal-verification evidence.

Preserve this existing Goal, continuous mode, budgets, completed work, worker histories, source snapshots, findings and checkpoints. Do not create a replacement Goal or restart the assignment.

## Authoritative context

Read AGENTS.md and:

- docs/verification/CORE_PROTOCOL_VERIFICATION_PLAN.md
- docs/verification/CORE_PROTOCOL_QWEN_HANDOFF.md

Follow repository instructions and installed skill compatibility guidance. Preserve the original Builder M0–M6 and C01–C19 acceptance requirements.

This objective authorizes scoped, reversible product-code fixes for defects reproduced by this program. It supersedes earlier verification-only restrictions for those fixes. It does not authorize unrelated features, speculative rewrites, destructive data changes or weakened contracts.

Commits, pushes, publication, external messages and production deployment remain separately authorized. Preserve permission for the documented owned local Loom test-environment rebuild.

## Preserve the Builder checkpoint

Treat the recorded Builder completion as a milestone receipt. Reconcile its evidence against current relevant source without restarting unchanged broad audits. Correct unsupported claims and recheck guarantees affected by subsequent changes.

Do not confuse separate mocked UI and server tests with a joined path. Retain required real-service/browser evidence and disclose its limits.

## Continuing execution loop

Repeat the following cycle across Loom's UI, APIs, compilation, persistence, publication, authorization, retries, recovery and cleanup.

### 1. Select

Inspect the existing confirmed findings before opening more investigations. Deduplicate reports by root cause and rank actionable defects by user harm, confidence, reachability and dependencies.

The existing confirmed findings are the initial repair backlog. Reproduce the highest-priority actionable finding and begin repairing it now. Do not wait for exhaustive discovery across the repository.

Keep a small risk-ranked investigation queue. Alternate discovery and repair without allowing either an indefinitely growing bug backlog or unexamined fixes. Serious confirmed data-loss or authorization defects take precedence over routine exploration.

### 2. Reproduce and classify

Establish the promised behavior from contracts, callers, tests and source. Demonstrate the failure against unmodified production code with a concrete account of user harm.

Classify results as confirmed product bug, verification defect, prevented by identified code, or inconclusive. Disclose controlled boundaries and unverified reachability.

Treat worker reports as untrusted until checked against actual files, commands, process exits and artifacts. A model counterexample or deliberately broken source variant alone is not a discovered product bug.

### 3. Fix

Make the smallest coherent root-cause repair, updating affected callers where necessary. Preserve unrelated work and existing behavior outside the established defect.

Inspect nearby callers and equivalent paths for the same root cause. Include directly affected cases in the repair. Queue unrelated defects separately.

Use exclusive file ownership and isolated worktrees for concurrent implementation. Serialize shared runtime/database mutations and freeze tested source during evidence runs.

A repair must not redefine the promise merely to make its test pass. If the intended behavior genuinely requires an operator decision, record that question and continue independent authorized work.

### 4. Verify and review

Retain a durable regression check that demonstrates the original product defect. Show that this same check fails against the pre-fix implementation and passes against the repaired implementation, while relevant valid behavior still succeeds. Derive its expected result from the established contract.

This before-and-after evidence satisfies fault sensitivity for that defect. Add separate semantic fault controls when a formal-verification guarantee requires them or the regression check's sensitivity remains uncertain. Preserve mandatory controls from the original Builder acceptance plan.

Convert discovery probes into durable behavior assertions. Tests must naturally pass when the behavior is correct, rather than unconditionally failing because they were written as reports. Preserve historical red evidence; do not hide unresolved defects through skips or weakened checks.

Run focused checks while editing, affected suites after coherent fixes, and required integration/browser checks at appropriate checkpoints.

Review the actual diff and raw evidence. Use available separate reviewer contexts where useful and disclose their model/capability. Same-model agreement is not cross-model assurance. Do not invent review results or claim an unavailable independent approval.

### 5. Maintain verification

Read and apply both:

- pstack:create-formal-verification-skill
- pstack:maintain-formal-verification-skill

Use create-formal-verification-skill for a specific missing guarantee. Reuse or extend existing models and harnesses when they fit. Choose TLA+ when state transitions, concurrency, retries or partial failure justify bounded exploration. Use simpler actual-code verification when sufficient.

Use maintain-formal-verification-skill to keep models, properties, action boundaries, source mappings and replay evidence aligned with repaired code. Report in sync, updated, bug or blocked accurately.

A new model or helper needs a named consumer and a guarantee existing checks cannot establish. Neither model counts, explored states, test counts nor lines written are progress measures by themselves.

Recover relevant historical assertions and fixtures before rebuilding them. Keep browser checks for actual browser behavior and joined wiring. Use focused component/server/replay checks for controlled interleavings. Do not build another reporting or orchestration framework.

### 6. Record and repeat

Record the finding, root cause, changed files, source identity, reproduction, positive/fault/restored results, review status and remaining uncertainty. Reassess related findings after a shared root-cause repair.

Keep changes organized into reviewable repair units even without permission to commit. Preserve evidence and checkpoint the next exact action.

Immediately continue to the next actionable finding or investigation. Do not require routine operator approval between authorized tasks.

## Async operation and failed approaches

Continue through the existing OpenCode harness across turns, compaction and restarts. Inspect live jobs before resuming and prevent duplicate writers.

Use exact test-name filters or narrow replay/API cases during diagnosis. Preserve useful diagnostic output and real exit statuses. Collect background command results through demonstrated job controls.

Two unchanged failures require diagnosis rather than another identical run. If materially different approaches stop producing new evidence, checkpoint, change the approach or take independent work. Do not keep increasing timeouts, expanding instrumentation or restarting broad audits without a concrete reason.

Repair authorized local environment problems. Request manual intervention only when a precise prerequisite prevents all remaining useful authorized work. Respect explicit stop instructions and security boundaries.

## Stopping and honest completion

Do not wait for proof that every possible bug has been found. That claim cannot be established for the whole repository.

At each completed investigation pass, establish:

- Actionable confirmed findings in that pass are repaired and verified, or explicitly blocked with concrete prerequisites.
- Affected verification remains aligned and required checks pass.
- Review and evidence limitations are disclosed.
- Remaining risk-ranked hypotheses and examined scope are recorded.

Continue with the next useful investigation pass while one exists. Do not rediscover unchanged issues or manufacture findings to remain active.

If the current investigation queue is exhausted and no useful authorized work remains, checkpoint the examined scope, residual risks and conditions that should trigger another pass. Await new changes or operator direction without a busy loop. Never claim the entire repository is bug-free.

The principal output is verified product defects repaired with maintainable regression coverage. Discovery reports and verification infrastructure support that output; they do not replace it.
