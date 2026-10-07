## Default workflow

Use the project-local `.codex/skills/peter-mode/SKILL.md` as the default
workflow for Loom work. It uses focused verification and calls a specialized
pstack skill only when the task needs one. Do not load `pstack:poteto-mode` as
the automatic entry point. Follow a different skill when the user explicitly
requests it.

## Sol execution and Luna delegation

Sol owns execution: maintain a working understanding of the goal, current
state, architecture, dependencies, and evidence. Delegate work without
outsourcing that understanding or final judgment.

- Proactively offload bounded investigation and implementation to
  `gpt-6-luna` at `xhigh` reasoning. Suitable work includes source and caller
  inventories, hypothesis checks, focused fixes, verifier scripts, fixtures,
  tests, mechanical refactors, and documentation with a clear brief.
- Keep task decomposition, cross-cutting product and architecture decisions,
  integration order, acceptance of changes, and final communication with Sol.
  Sol must inspect consequential source and executable evidence rather than
  accepting worker summaries as proof.
- Give each worker an outcome, relevant context, invariants, file ownership,
  allowed runtime actions, and a concrete verification target. Workers may
  investigate an open question, but must return material ownership or design
  choices to Sol before committing the project to them.
- Delegate bounded work through at most three teams of three Luna xhigh workers
  and one Luna max lead, as defined below. Keep assignments distinct; worker
  count is not a progress metric. This overrides older unbounded worker-count
  guidance in inherited skills. Keep configured Sol reasoning effort.
- Parallelize work with disjoint ownership. Luna max coordinates shared-file merges and integration; Sol coordinates
  canonical priorities, final checkoffs, deployments, and mutable runtime state;
  a worker may perform those actions when explicitly assigned sole ownership.
- Serialize browser performance runs and shared database verification. Prepare
  incomplete production patches outside the watched deployment and integrate
  compiler-complete changes before freezing source for live verification.
- Sol may handle a small task directly when delegation costs more than the
  work, or when the next action requires its current context or judgment.
  Otherwise, prefer giving the work to Luna and reviewing the result.

## Parallel teams, review, and merging

Run at most three teams in parallel. Each team has up to three `gpt-6-luna`
workers at `xhigh` reasoning and one `gpt-6-luna` lead at `max` reasoning.
Use the full team when distinct useful work permits; do not fill slots with
redundant assignments. Foreground Sol retains priorities, architecture context,
product decisions, final checkoffs, and completion claims, using its configured
reasoning effort (Sol low when selectable).

- Sol assigns each team a distinct bounded workflow from the worklist. The Luna
  max lead divides it into nonoverlapping implementation, fixture, or test tasks
  for its workers. One owner per issue and file; no duplicate diagnosis.
- Workers implement in separate Git worktrees and run their focused checks.
  Preserve uncommitted integration work when preparing bases. Luna max owns
  correction iterations, review, and combining its team's work in an integration
  worktree. It resolves conflicts with the owners and verifies the resolved result.
- Only after review and required tests pass does Luna max send Sol the exact
  integrated diff/commit, tested source identity, commands/results, and remaining
  gaps. Focused tests cannot substitute for a required browser lifecycle.
- Sol pass/fails that submission. On rejection, send concrete findings back to
  the same Luna max lead and its team. They correct, retest, and resubmit; Sol
  does not routinely implement corrections or rerun passing checks.
- On acceptance, that Luna max lead performs the Git merge into the designated
  main integration branch and reports the resulting commit. Sol then assigns
  the team its next task. Use the current agreed integration branch; do not
  silently switch to a branch named main or publish/deploy elsewhere.
- Serialize merges into the shared target, not team implementation. Check the
  target has not changed since acceptance; if conflicts or changed dependencies
  alter the accepted artifact, return it through verification and Sol checkoff.
  Preserve unrelated working changes. Never merge into watched source during
  a browser freeze; hold the accepted merge until the freeze is released.

## Worktree ownership

Separate worktrees are the default for implementation workers and the Luna max
integration owner. The foreground/live checkout is the accepted runtime target.

- Reserve issue and file ownership before edits. Separate worktrees isolate
  unfinished code and checks but do not eliminate merge conflicts. Coordinate
  shared registries, generated files, worklists, and lockfiles with the merge owner.
- Workers may commit owned changes in their own worktrees. They must not stage,
  reset, stash, clean, or overwrite another worker's checkout or the live checkout.
- During browser verification, freeze the live checkout and shared runtime.
  Workers continue implementation and Luna max continues integration in their
  separate worktrees. Promotion waits until the freeze is released.
- A reviewed patch or passing focused check does not establish a full browser
  lifecycle. Run browser acceptance against the promoted, fingerprinted source.

## Close workflows before expanding work

Sol owns priorities and final checkoffs; Luna max owns the merge queue. Prioritize
closing ready units while workers prepare distinct upcoming workflows. Workers
return integration-ready changes with explicit ownership and source provenance. Follow
the verifier skill's failure-loop discipline for isolation checks, first-failure
diagnostics, lifecycle evidence, and repeated harness-failure checkpoints.
Report reliable workflows and remaining failures separately from testing overhead.

A bottleneck changes worker assignments; it does not suspend useful parallel work.
Use up to three teams as defined above. Refill a team with its next distinct
workflow after its accepted merge, rather than directing idle workers at an
already-owned problem.

- Assign one implementation owner per issue or workflow. That owner handles
  diagnosis, the fix, and focused tests. Assign one Luna max reviewer; review is
  a distinct role, not a second implementation or diagnosis assignment.
- Give remaining workers distinct upcoming workflows from the coverage inventory.
  They work ahead through reproduction, implementation, focused checks, and Luna
  max review while Sol closes the current integration priority. Do not send
  multiple workers to independently rediscover the same cause.
- Add support to the current issue only for a specific missing deliverable that
  its owner cannot produce efficiently. Name that deliverable and its boundary;
  once supplied, return the worker to distinct upcoming work. A bottleneck is
  not a reason to duplicate diagnosis, fixtures, tests, or evidence packaging.
- Maintain an assignment list with issue/workflow, owner, reviewer, owned files,
  deliverable, and dependency. Check it before delegating. Reuse completed findings
  and redirect overlapping assignments rather than running them to completion.
- State how each assignment advances the current integration priority or the
  next bounded workflow. Require a patch, executable check, fixture, or decisive
  evidence; do not assign generic audits merely to keep agents occupied.
- While a shared runtime action is serialized, workers continue isolated source
  investigations, fixture preparation, focused tests, and staged fixes. Waiting
  on deployment or a browser does not serialize work that has no such dependency.
- Workers own their checks and browser cases when runtime ownership permits.
  Sol reviews retained evidence instead of becoming the test runner for every
  worker. Delegate integration preparation, but retain acceptance and shared
  writes with the assigned owner.
- When review is limiting progress, reviewers close ready submissions while
  implementation workers continue distinct upcoming workflows. Keep next-unit
  preparation bounded; worker count is not a measure of progress. Keep handoffs
  small: the exact patch, preimages, focused command/results, and remaining gaps.
  Reuse existing evidence formats rather than creating another packaging layer.

- Measure progress by closed user-visible failures and fully verified lifecycles,
  with elapsed time. Commits, scripts, worker activity, and partial assertions
  do not establish completion.
- Sol chooses one failing user workflow as the current integration priority.
  Carry it through diagnosis, root-cause fix, and a passing rerun of the same
  browser case before integrating another substantial thread. Keep the full
  goal and coverage inventory intact; this is sequencing, not reduced scope.
- Continue useful parallel Luna work on independent investigations and staged
  patches. Delegate aggressively, but prioritize integration-ready work and
  review completed submissions before creating more implementation backlog.
  Workers must not mutate shared runtime or watched source during browser runs.
- Follow `.codex/skills/verify/SKILL.md` for browser preparation, source
  freezing, failure evidence, and reruns. Keep testing procedures in that skill
  rather than duplicating them here.
- Distinguish harness corrections from product fixes and selected assertions
  from a complete lifecycle pass. Commit a verified coherent unit before
  integrating the next substantial unit; do not bulk-stage unrelated backlog.
- A live process is a reason to poll its handle, not to end work with a status
  update. Continue through the result and next safe action unless the user
  interrupts, a real dependency prevents progress, or the unit is complete.

## Verification skills

- For the local Compose Builder, load `.codex/skills/verify/SKILL.md` and run
  the relevant browser path. On the loaded CDA dataset, use its `verify-current`
  guidance and `scripts/verify-cda-builder.mjs` for feature-specific DOM checks.
  A small development fixture does not establish CDA correctness.
- For the authenticated Calypr Builder deployment, load
  `.codex/skills/verify-loom-ui/SKILL.md`. It targets that deployment, not the
  local Compose Builder.
- For ingestion, publication, ClickHouse, or GraphQL behavior against the
  locked NCPI fixture, load `.codex/skills/verify-loom/SKILL.md` and run its
  applicable acceptance path.
- Match the verifier to the changed behavior. A successful API response alone
  does not verify a visible Builder feature; check its browser result and saved
  state when those are part of the task.

## GitNexus

This project uses GitNexus as its local repository knowledge graph.

- Install the tested version with `npm install --global gitnexus@1.6.12` (Node 22.18+ in the 22.x series, or 24.11+).
- For codebase questions, start with GitNexus MCP `query`, `context`, `impact`, or `trace`. CLI equivalents include `gitnexus query "<question>" -r loom` and `gitnexus context "<symbol>" -r loom`.
- Do not use Graphify for repository discovery or architecture answers. If the GitNexus index is missing or stale, refresh GitNexus and then verify consequential findings in source rather than falling back to Graphify.
- After source changes, refresh with `gitnexus analyze . --index-only` before relying on the graph. `.gitnexusrc` disables file injection and embeddings; `.gitnexusignore` excludes local datasets but includes generated contracts and tests. Analysis is local and does not call an LLM.
- Keep machine-local Codex MCP settings in the ignored `.codex/config.toml`. This checkout uses a read-only GitNexus server scoped to Loom. Other checkouts can use the CLI without that local configuration. Restart the Codex session after changing MCP configuration.
- Consult `docs/PACKAGE_AUDIT.csv`, `docs/PACKAGE_AUDIT_DECISIONS.csv`, and `docs/EXPLORER_BACKLOG.md` for recorded decisions before repeating broad audits.
- The graph is navigation evidence, not proof of completeness, runtime behavior, or dead code. Confirm consequential findings in source and executable checks.
- GitNexus wiki generation is separate, optional LLM-generated module prose. It is not enabled by this setup. Do not assume a wiki reflects uncommitted edits.

## Frozen-design execution

When the parent assignment says that a design is frozen, treat the assignment
as an implementation stage that starts after discovery and architecture work.

- Start at the implementation step of the selected workflow. Do not
  repeat `how`, `architect`, `figure-it-out`, an arena, or broad discovery that
  the parent already completed.
- Read the named source and required skills, then produce a patch in the first
  progress interval. A progress update must report an edited file, a test or
  runtime result, or evidence that changes the next action.
- One progress interval without a patch is a warning, not an automatic
  failure. Two consecutive updates that only restate the plan require the
  executor to stop and return control to the parent.
- Reopen design work when executable evidence contradicts a frozen invariant,
  the brief omits an ownership or type decision needed to write correct code,
  or two viable implementations remain after direct source inspection. State
  the concrete unresolved question before invoking another design skill.
- Do not count instruction reading, source navigation, or confirmation of an
  existing decision as implementation progress.
- For broad changes, implement one compiler-complete or user-visible unit at a
  time. End each unit with a focused check before starting the next unit.

If the task has no frozen design, use the Peter mode routing rules. This
section limits repeated analysis. It does not prohibit needed investigation or
architecture work.

## Parallel execution and integration

For independent verification cases, delegate case ownership to Luna xhigh
workers by default. Luna max owns review and merging; Sol owns context, final
checkoffs, promotion coordination, and completion claims.
Closing one integration priority does not require serializing independent runs
or investigations. Workers return reviewed changes from separate worktrees with source provenance;
they do not edit shared source or runtime during browser runs. Follow the local
verification skill for isolated evidence paths, source freezing, lifecycle proof,
and serial confirmation of performance failures observed under contention.

For broad inventories, use the three-team structure above. Sol assigns distinct
workflows, pass/fails tested submissions, and assigns new work after an accepted
merge. Team Luna max leads coordinate corrections and accepted merges; serialize
shared-target writes while independent work proceeds in separate worktrees.

## Package audit safety

For every package combine, move, or deletion decision:

- Start with `regression-guard:verify-change` when available. Use it as the verification controller for the before/after bracket.
- Start from `docs/PACKAGE_AUDIT.csv`, then use `pstack:blast-radius` to find contracts that import searches miss.
- State the behavior that must remain true and prove it with executable code. Mark it unproven if no test or runtime check exercises it.
- Run `python3 scripts/package_audit_verify.py <location>` before and after the change.
- Run `python3 scripts/package_audit_verify.py --full` after any package move, merge, or deletion.
- Change one package boundary at a time. Regenerate the audit table and inspect its dependency/importer diff before proceeding.
- Never treat a table row, zero static importers, or a passing compile as deletion authorization.
