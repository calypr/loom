# Loom

## Working style

- Start with the requested change. Read the relevant source and make the smallest coherent patch.
- Prefer `rg` and `rg --files` for repository text and file searches; use another tool when ripgrep is unavailable or unsuitable.
- Use skills when explicitly requested or when they solve a concrete problem. Do not stack workflows automatically.
- For repository-wide cleanup, follow `docs/REPO_CLEANUP_GOAL.md`. Age is a screening signal, not deletion evidence.
- Delegate useful independent work proactively; follow the Parallel work rules below. Handle trivial or inseparable work directly.
- Use separate worktrees for concurrent implementation. Keep unfinished changes out of the live checkout during runtime verification.
- Preserve unrelated changes. Do not reset, stash, clean, or overwrite someone else's work.
- Continue through running commands and their results. Report concrete changes, checks, and blockers concisely.

## Parallel work

- For multi-step work, actively look for independent tasks and delegate them without waiting for the user to request subagents. After enough source reading to define ownership, launch useful parallel work before doing those tracks serially.
- At the start of a substantial task and at each checkpoint, identify ready independent investigations, implementation units, verification runs, or reviews. When two or more useful tracks are ready, delegate background workers and keep the parent on integration or a separate track.
- In OpenCode, use `task` with `background: true`; in other harnesses use their native parallel-agent tools. Use up to five background workers alongside the parent when the current serving capacity permits. One served model can run multiple agent contexts.
- Give each worker a concrete deliverable, relevant source paths and constraints, explicit file or issue ownership, and the evidence expected back. Resume an existing worker with its `task_id` when continuing that work; do not duplicate it.
- Keep one writer per file, worktree, or shared mutable resource. This is an ownership boundary, not a one-worker limit for the entire program. Separate worktrees are required for concurrent implementation; independent read-only work and checks on isolated copies can proceed alongside it.
- Delegate long model-checking and acceptance runs when useful independent work remains. Follow the Verification scheduling rules below.
- If implementation cannot be isolated safely, still delegate useful source tracing, adversarial investigation, coverage analysis, or independent review. Keep the parent integrating findings and advancing non-overlapping work instead of waiting on every worker sequentially.
- Do not spawn agents for trivial edits, duplicate investigations, or to fill slots without useful work. At checkpoints, briefly state what is delegated and what the parent owns; when a substantial task stays serial, name the actual dependency or shared-resource constraint.

## Verification

- Preserve existing testing guarantees. Run the closest relevant checks before broadening verification.
- For visible local Builder changes, use `.codex/skills/verify/SKILL.md`. Use its current-dataset CDA checks when applicable.
- For authenticated Calypr Builder changes, use `.codex/skills/verify-loom-ui/SKILL.md`.
- For ingestion, publication, ClickHouse, or GraphQL changes, use `.codex/skills/verify-loom/SKILL.md`.
- Keep verification procedures in those skills. Do not repeat discovery or design that the assignment already settled.
- Serialize checks that mutate the same runtime or database. Do not change tested source during a browser run.
- Distinguish focused checks from complete browser acceptance. State what remains unverified.
- Measure performance before claiming a speedup. Preserve stale-source, evidence-integrity, and negative validation checks when changing test tooling.

## Adversarial review

- Before marking a nontrivial behavior-changing repair unit complete, apply `pstack:interrogate` to its exact source snapshot. Batch closely related edits; do not review every intermediate edit.
- Reviewers inspect the actual diff, new files, affected callers, contracts, and raw evidence. Give them focused, non-overlapping briefs, including review of interactions across changed boundaries. Keep reproduction experiments isolated from the writer's checkout.
- Retain findings only after the coordinator reruns their reproducer and confirms a contract violation. Repair confirmed findings and recheck affected guarantees. Record the reviewed source identity, coverage, unavailable checks, reviewer models, and unresolved findings. Same-model review must be labeled accurately.
- Relevant changes invalidate affected review coverage. While review runs or is blocked, continue independent authorized work. Label the unit review-pending rather than claiming completion or stopping the entire program.

## Local commits

- Local Git commits are authorized for completed substantive Todos, or a tightly coupled set of Todos. Each commit should cover the whole task: implementation, regression tests, and relevant verification or documentation. Do not make microcommits per file, test run, probe, or progress update, or invent tiny Todos just to create commit boundaries.
- Meet the verification and adversarial-review requirements before committing. A checked Todo alone is not evidence. Record the Todo or repair task and relevant validation in the commit message; keep the tested source identity and review receipt traceable to the committed contents.
- The parent owns commits and serializes staging with worker integration. Stage only the task's explicit paths or coherent hunks; inspect the staged diff before committing. Preserve unrelated edits. Do not use blanket staging, reset, stash, clean, amend, rebase, or history rewriting to manufacture a clean tree.
- Apply this policy to the existing uncommitted backlog by grouping completed Todos from its evidence records. Leave unfinished or review-pending work uncommitted and continue independent work. Keep runtime Goal state, caches, logs, credentials, and disposable evidence out of commits.
- Stay on the agreed branch and use the existing repository author identity. Pushes and publication still require operator authorization; local commits do not authorize either.

## Verification scheduling

- During bug hunting, prioritize concrete production-code hypotheses. Use the smallest check that distinguishes the current hypothesis. A model-only pass does not establish a production-code guarantee.
- Before an expensive check, state the unresolved claim, why a smaller check or existing evidence cannot answer it, and the next action for a pass, failure, or timeout. A required final acceptance check is a valid reason. Background execution alone does not justify the cost.
- Run required acceptance checks against the final relevant source snapshot before declaring completion. Reuse evidence whose relevant source, configuration, assumptions, and bounds remain unchanged. Rerun only when these change, new evidence challenges the result, or you investigate suspected nondeterminism.
- After a timeout or inconclusive run, inspect the saved output before repeating it. Change the hypothesis, diagnostic method, or justified resource budget. Do not repeat the same run merely to stay active. Smaller exploratory bounds do not replace required acceptance bounds.
- Before a batch of compute-heavy checks, inspect the current sandbox's CPU quota, CPU affinity and memory limit. For TLC, run `/sandbox/.local/bin/tlc-run --budget`. Use that environment's limits, never the host's advertised core count or a fixed Stravinsky CPU count.
- For TLC checks in OpenShell, use `/sandbox/.local/bin/tlc-run`. Its worker budget reserves CPU headroom. The parent must divide the remaining CPU and memory budget across all active checks and workers. Set `TLC_WORKERS` and `TLC_HEAP_MB` per job, leaving memory for JVM overhead, the agent, compilers and services. The launcher does not detach jobs or capture evidence.
- Run independent cases concurrently when the budget permits, with separate configurations, logs and `-metadir` paths. Prefer several useful checks over assuming one TLC process scales linearly to every core. Do not serialize a whole matrix with one worker and a 512 MiB heap by habit. Small probes may use small budgets when that answers the hypothesis efficiently.
- Use a bounded process pool or supported background workers for a matrix, and give every job a deadline. Report the chosen concurrency, per-job workers and heap. Keep total workers and memory within the shared budget. Do not enlarge verification scope or repeat equivalent controls merely to fill available hardware.
- When useful independent work remains, run long checks through a supported background worker or an observable durable runner. Record the command, source snapshot, full log path, exit status, and deadline. Do not assume shell `&` survives tool cleanup or terminal disconnects.
- Keep the parent on independent investigation or integration while checks run. If a prerequisite or resource limit requires foreground waiting, name that dependency. Bound the wait and preserve results for the next decision.
- Isolate writable source, output files, caches, and state directories before running checks concurrently. Give each TLC run its own explicit `-metadir` and run identity. Serialize mutations only when a runtime or database must be shared. Account for CPU and memory contention even between read-only checks.
- Recheck affected guarantees after relevant source changes. Preserve required acceptance bounds and fault controls. Record timeouts as incomplete evidence, never as passes. Keep model or harness defects distinct from production-code bugs.

## Project conventions

- Do not add frontend `useEffect` or move the same synchronization into `useLayoutEffect`. Derive state during rendering or update it through its owning action.
- Keep authored contracts compact. Generate expanded reports on demand and keep disposable runtime artifacts out of Git.
- GitNexus can help navigate the repository. Verify consequential findings in source and tests. Refresh a stale index before relying on it.
- Consult `docs/PACKAGE_AUDIT.csv`, `docs/PACKAGE_AUDIT_DECISIONS.csv`, and `docs/EXPLORER_BACKLOG.md` before repeating broad audits.
- Before and after package moves, merges, or deletions, run `python3 scripts/package_audit_verify.py <location>`. Run its `--full` mode afterward and inspect the dependency diff.
- Zero static importers or a passing compile alone do not establish that deletion is safe.

## Scope and approvals

- Proceed with authorized reversible work without repeated confirmation.
- Get authorization before destructive actions, publication, external messages, or production changes.
- Keep the agreed branch and deployment target. Do not publish, deploy, or rewrite Git history unless requested.
