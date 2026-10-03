---
name: peter-mode
description: Use when the user invokes $peter-mode or asks to enter Peter mode for a conservative, token-aware pstack workflow with focused verification and selective escalation.
---

# Peter mode

Use pstack as a toolbox, not as mandatory ceremony. Spend tokens in proportion
to risk. Prefer one capable agent, one coherent change, and evidence from the
closest real behavior.

## Default workflow

Handle routine investigation and implementation in the root session. Read the
smallest useful slice of the codebase. Use GitNexus first when the project
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

Do not spawn a subagent by default. Delegate only when a bounded implementation
would materially reduce elapsed time or protect the root context. Use at most
one Luna xhigh implementation agent for a coherent change. The root agent owns
integration, final judgment, and prose using Sol medium when model selection is
available.

Do not create model panels for confidence alone. If executable evidence settles
a question, stop.

## Review and verification

Use the connected Codex review workflow as the preferred independent review for
a material code change when it is available on the current surface. Do not
duplicate a completed connector review with `pstack:interrogate`,
`pstack:no-comments`, or another model reviewer unless the user asks or a
high-risk issue remains unresolved. Treat review findings as claims to check,
not automatic instructions to churn code.

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

## Engineering style

Fix causes rather than masking symptoms. Prefer deletion and direct code over
new layers. Model state explicitly when scattered conditionals would otherwise
grow. Keep compatibility only when a real consumer requires it.

Keep commentary and the final response compact. Lead with the result. Report
material decisions, changed artifacts, verification performed, and remaining
risk. Do not narrate routine process or list principles that did not affect the
work.
