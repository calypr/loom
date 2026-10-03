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

## Package audit safety

For every package combine, move, or deletion decision:

- Start with `regression-guard:verify-change` when available. Use it as the verification controller for the before/after bracket.
- Start from `docs/PACKAGE_AUDIT.csv`, then use `pstack:blast-radius` to find contracts that import searches miss.
- State the behavior that must remain true and prove it with executable code. Mark it unproven if no test or runtime check exercises it.
- Run `python3 scripts/package_audit_verify.py <location>` before and after the change.
- Run `python3 scripts/package_audit_verify.py --full` after any package move, merge, or deletion.
- Change one package boundary at a time. Regenerate the audit table and inspect its dependency/importer diff before proceeding.
- Never treat a table row, zero static importers, or a passing compile as deletion authorization.
