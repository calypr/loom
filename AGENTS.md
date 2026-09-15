## Peter mode

When the user invokes `$peter-mode` or asks to enter Peter mode, use the
project-local `peter-mode` skill as the pstack entry point. Do not invoke or
load `pstack:poteto-mode` first. Peter mode replaces poteto-mode for that task.

## GitNexus

This project uses GitNexus as its local repository knowledge graph.

- Install the tested version with `npm install --global gitnexus@1.6.12` (Node 22.18+ in the 22.x series, or 24.11+).
- For codebase questions, start with GitNexus MCP `query`, `context`, `impact`, or `trace`. CLI equivalents include `gitnexus query "<question>" -r loom` and `gitnexus context "<symbol>" -r loom`.
- After source changes, refresh with `gitnexus analyze . --index-only` before relying on the graph. `.gitnexusrc` disables file injection and embeddings; `.gitnexusignore` excludes local datasets but includes generated contracts and tests. Analysis is local and does not call an LLM.
- Keep machine-local Codex MCP settings in the ignored `.codex/config.toml`. This checkout uses a read-only GitNexus server scoped to Loom. Other checkouts can use the CLI without that local configuration. Restart the Codex session after changing MCP configuration.
- Consult `docs/PACKAGE_AUDIT.csv`, `docs/PACKAGE_AUDIT_DECISIONS.csv`, and `docs/EXPLORER_BACKLOG.md` for recorded decisions before repeating broad audits.
- The graph is navigation evidence, not proof of completeness, runtime behavior, or dead code. Confirm consequential findings in source and executable checks.
- GitNexus wiki generation is separate, optional LLM-generated module prose. It is not enabled by this setup. Do not assume a wiki reflects uncommitted edits.

## Package audit safety

For every package combine, move, or deletion decision:

- Start with `regression-guard:verify-change` when available. Use it as the verification controller for the before/after bracket.
- Start from `docs/PACKAGE_AUDIT.csv`, then use `pstack:blast-radius` to find contracts that import searches miss.
- State the behavior that must remain true and prove it with executable code. Mark it unproven if no test or runtime check exercises it.
- Run `python3 scripts/package_audit_verify.py <location>` before and after the change.
- Run `python3 scripts/package_audit_verify.py --full` after any package move, merge, or deletion.
- Change one package boundary at a time. Regenerate the audit table and inspect its dependency/importer diff before proceeding.
- Never treat a table row, zero static importers, or a passing compile as deletion authorization.
