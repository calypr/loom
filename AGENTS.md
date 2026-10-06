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
- Aggressively delegate menial and bounded work to Luna xhigh. Default to
  the maximum useful parallelism supported by available agent slots and
  independent work; there is no fixed worker cap or conservative worker budget.
  Keep workers supplied with ready tasks as they finish rather than making Sol
  perform work Luna can handle. Do not create redundant or conflicting work
  merely to occupy slots. This overrides worker-count limits in inherited
  workflow guidance, including Peter mode. Keep the configured Sol reasoning
  effort; delegation does not change the running root model.
- Parallelize work with disjoint ownership. Sol coordinates shared files,
  canonical worklists, integration, deployments, and mutable runtime state;
  a worker may perform those actions when explicitly assigned sole ownership.
- Serialize browser performance runs and shared database verification. Prepare
  incomplete production patches outside the watched deployment and integrate
  compiler-complete changes before freezing source for live verification.
- Sol may handle a small task directly when delegation costs more than the
  work, or when the next action requires its current context or judgment.
  Otherwise, prefer giving the work to Luna and reviewing the result.

## Review and correction handoff

Use a Luna max review stage before sending substantial worker changes to the
foreground Sol agent. This separates implementation iterations from final
acceptance; Sol still understands the change and owns integration and judgment.

- Luna xhigh workers implement bounded changes in physically separate staging
  directories and run their focused checks.
- A `gpt-6-luna` reviewer at `max` reasoning reads the complete proposed patch,
  relevant contracts, and executable evidence. It owns the initial review and
  coordinates correction iterations with the implementation workers.
- On a finding, Luna max gives exact correction instructions to the owning
  worker, checks the revised artifact and necessary reruns, and repeats until
  it has no remaining findings. Preserve the rejected patch and preimages so
  corrections remain reviewable. A changed artifact requires renewed review.
- Only then hand the foreground Sol agent the final patch and hash, preserved
  preimages, exact verification commands and results, remaining gaps, and the
  Luna max verdict with resolved findings. Use the configured foreground Sol
  agent, with low reasoning when model selection is available, for final review.
- Sol reads the final diff and consequential source and evidence, then accepts
  or returns concrete findings to Luna max. Luna max manages the next correction
  loop and resubmits; Sol does not routinely implement the corrections itself
  or rerun workers' passing checks.
- Luna max approval is a prerequisite for substantial worker handoffs, not
  permission to merge. Sol retains acceptance, integration order, shared writes,
  product decisions, and completion claims. Small direct changes may stay with
  Sol when the extra handoff would cost more than the work.

## Close workflows before expanding work

Sol owns the integration queue: prioritize reviewing and closing ready units over
starting more implementation threads. Workers return integration-ready patches
with preserved preimages from physically separate staging directories. Follow
the verifier skill's failure-loop discipline for isolation checks, first-failure
diagnostics, lifecycle evidence, and repeated harness-failure checkpoints.
Report reliable workflows and remaining failures separately from testing overhead.

A bottleneck changes worker assignments; it does not suspend useful parallel work.
Keep at least ten productive Luna xhigh workers on broad inventories when the
work and available slots permit, with no fixed upper cap. Refill completed
assignments rather than letting the pool drain while Sol waits on one unit.

- Identify the limiting step and assign workers to shorten it first: reproduce
  the failure, inspect independent evidence, prepare the focused fix and tests,
  review contracts, or prepare the exact deployment and browser invocation.
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
- When review is limiting progress, route independent review and missing-evidence
  collection to workers and close ready submissions before expanding the queue.
  Keep next-unit preparation bounded; worker count is not a measure of progress.

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
workers by default. Sol owns context, review, integration, and completion claims.
Closing one integration priority does not require serializing independent runs
or investigations. Workers return reviewable staged patches with preimages;
they do not edit shared source or runtime during browser runs. Follow the local
verification skill for isolated evidence paths, source freezing, lifecycle proof,
and serial confirmation of performance failures observed under contention.

For broad case inventories, target at least 10 active Luna xhigh workers per
foreground Sol whenever useful independent work and available agent slots permit.
There is no fixed upper cap. Split the ready work into disjoint owned cases,
investigations, and staged fixes; refill assignments as workers finish. Do not
create duplicate work or idle assignments merely to meet the count. Sol owns
context and priorities, reviews returned evidence and patches, and integrates
verified coherent units promptly. Serialize shared writes and dependent steps,
not the independent work that can proceed alongside integration.

## Package audit safety

For every package combine, move, or deletion decision:

- Start with `regression-guard:verify-change` when available. Use it as the verification controller for the before/after bracket.
- Start from `docs/PACKAGE_AUDIT.csv`, then use `pstack:blast-radius` to find contracts that import searches miss.
- State the behavior that must remain true and prove it with executable code. Mark it unproven if no test or runtime check exercises it.
- Run `python3 scripts/package_audit_verify.py <location>` before and after the change.
- Run `python3 scripts/package_audit_verify.py --full` after any package move, merge, or deletion.
- Change one package boundary at a time. Regenerate the audit table and inspect its dependency/importer diff before proceeding.
- Never treat a table row, zero static importers, or a passing compile as deletion authorization.
