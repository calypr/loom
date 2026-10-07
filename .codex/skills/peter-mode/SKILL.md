---
name: peter-mode
description: Use when the user invokes $peter-mode or asks to enter Peter mode for a pstack workflow with aggressive Luna delegation, focused verification, Luna max review and merging, and foreground Sol final checkoffs.
---

# Peter mode

Use pstack as a toolbox, not as mandatory ceremony. Spend tokens in proportion
to risk. Close coherent changes with evidence from the closest real behavior; use
parallel workers for independent work.

## Default workflow

Delegate bounded routine investigation and implementation when it can proceed
independently. Read the smallest useful slice of the codebase. Use GitNexus first when the project
requires it. Make the smallest coherent change and inspect the final diff.

Do not load a pstack principle, playbook, or supporting reference unless it
changes a decision in the current task. Do not create a formal plan, decision
trail, reusable verification tool, worktree, commit, or pull request unless the
task needs one or the user asks for it.

Proceed on reversible work without asking. Pause for irreversible actions,
external communication, production changes, or a product choice that evidence
cannot settle.

## Pstack routing

Enter a specialized pstack skill directly when the request names it or its
specific workflow is clearly needed.

- Use `pstack:how` for subsystem explanations and unresolved ownership questions.
- Use `pstack:why` for historical or evidence-backed design rationale.
- Use `pstack:tdd` for requested regression tests or a bug with an obvious cheap test.
- Use `pstack:blast-radius` for public APIs, shared packages, deletion, migration,
  persistence, concurrency, authentication, or deployment changes.
- Use `pstack:architect` only when API shape, core types, ownership, or module
  boundaries remain ambiguous after source inspection and at least two viable
  designs remain.
- Use `pstack:arena`, `pstack:swarm`, `pstack:interrogate`, and
  `pstack:figure-it-out` only when the user invokes them or unresolved high risk
  justifies their added cost.

Do not invoke `pstack:poteto-mode` from this skill. This skill replaces it as
the entry point for the current task.

## Delegation and models

Use at most three parallel teams, each with up to three `gpt-6-luna` workers
at `xhigh` reasoning and one `gpt-6-luna` lead at `max`. Fill teams only with
distinct useful work. Sol assigns distinct bounded workflows and retains the
goal, architecture, priorities, product decisions, final checkoffs, and prose.
Keep configured Sol reasoning; use Sol low when selectable.

Each Luna max lead divides its workflow among workers with nonoverlapping
ownership. Workers use separate Git worktrees and own focused checks. The lead
reviews, coordinates corrections, combines the team's changes, resolves conflicts
with owners, and verifies the final integrated artifact. Preserve uncommitted
integration work when preparing bases. Do not duplicate diagnosis to occupy slots.

When required tests and review pass, Luna max submits the exact diff/commit,
tested source identity, commands/results, and gaps to Sol. Sol pass/fails it.
Rejection returns to the same Luna max and its team for corrections, retesting,
and resubmission. Sol does not routinely perform those corrections or rerun
passing checks. Acceptance authorizes that Luna max to merge into the designated
main integration branch; after the merge, Sol assigns the team its next task.
Use the current agreed integration branch rather than silently changing branches.

Serialize shared-target merges. If a changed target or conflict changes the
accepted artifact, renew verification and final checkoff. Preserve unrelated
changes and hold merges during live-source/browser freezes. Teams continue in
separate worktrees while the live target is frozen. Follow AGENTS.md for ownership
and the verifier skill for lifecycle evidence; focused checks are not browser
acceptance. Small direct tasks may stay with Sol when delegation costs more.

Do not create model panels for confidence alone. If executable evidence settles
a question, stop.

## Review and verification

Use Luna max as the review and merge stage for delegated changes. Do not add
duplicate connector or model reviews for confidence alone. Use another review
workflow when requested or needed for a distinct unresolved risk. Treat findings
as claims to check, not automatic instructions to churn code.

Review does not prove runtime behavior. Run the cheapest direct check that
exercises the changed behavior. For documentation, configuration, and skill
changes, validation plus final artifact and diff inspection is normally enough.
For localized code, run the closest focused test. Add caller or integration
checks only when the plausible failure crosses that boundary.

Do not automatically run the full suite, broad end-to-end checks, repeated
before-and-after brackets, or newly written verification scripts. Reserve those
for destructive changes, migrations, shared contracts, security boundaries,
concurrency, deployment, or failures that focused checks cannot explain. State
anything important that remains untested.

## Commit cadence

Batch commits around meaningful logical work completions, usually touching
5–10 files. Accumulate small related changes until the unit is complete and
verified; do not commit each minor edit or instruction update separately.
Smaller commits are appropriate for important standalone fixes. Do not pad a
commit with unrelated work to reach a file count, or hold a ready substantial
fix merely because it changes fewer files.

## Frontend: no useEffect

Do not use `useEffect` in frontend code. This includes imported, aliased,
and `React.useEffect` calls. When changing a frontend flow that relies on it,
remove the effect from that flow rather than adding another guard or dependency.
Do not move the same synchronization into `useLayoutEffect` to evade this rule.

Derive display state during rendering. Run user-triggered work from the action
that owns it, and keep multi-step operations under one explicit lifecycle.
Use the existing query or subscription owner for external data, with cancellation
and stale-result ownership handled there. Avoid effects that copy props to state,
chain requests from intermediate state, or reset user input after rendering.
Prove the replacement preserves loading, cancellation, navigation, and saved
state behavior with a regression for the original race.

## Engineering style

Fix causes rather than masking symptoms. Prefer deletion and direct code over
new layers. Model state explicitly when scattered conditionals would otherwise
grow. Keep compatibility only when a real consumer requires it.

Keep commentary and the final response compact. Lead with the result. Report
material decisions, changed artifacts, verification performed, and remaining
risk. Do not narrate routine process or list principles that did not affect the
work.
