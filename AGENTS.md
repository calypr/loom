# Loom

## Working style

- Start with the requested change. Read the relevant source and make the smallest coherent patch.
- Use skills when explicitly requested or when they solve a concrete problem. Do not stack workflows automatically.
- For repository-wide cleanup, follow `docs/REPO_CLEANUP_GOAL.md`. Age is a screening signal, not deletion evidence.
- Handle focused tasks directly. Delegate only independent work that saves time, with one owner per file or issue.
- Use separate worktrees for concurrent implementation. Keep unfinished changes out of the live checkout during runtime verification.
- Preserve unrelated changes. Do not reset, stash, clean, or overwrite someone else's work.
- Continue through running commands and their results. Report concrete changes, checks, and blockers concisely.

## Verification

- Preserve existing testing guarantees. Run the closest relevant checks before broadening verification.
- For visible local Builder changes, use `.codex/skills/verify/SKILL.md`. Use its current-dataset CDA checks when applicable.
- For authenticated Calypr Builder changes, use `.codex/skills/verify-loom-ui/SKILL.md`.
- For ingestion, publication, ClickHouse, or GraphQL changes, use `.codex/skills/verify-loom/SKILL.md`.
- Keep verification procedures in those skills. Do not repeat discovery or design that the assignment already settled.
- Serialize checks that mutate the same runtime or database. Do not change tested source during a browser run.
- Distinguish focused checks from complete browser acceptance. State what remains unverified.
- Measure performance before claiming a speedup. Preserve stale-source, evidence-integrity, and negative validation checks when changing test tooling.

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
