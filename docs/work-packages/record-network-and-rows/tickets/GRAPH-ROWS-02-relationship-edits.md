# GRAPH-ROWS-02: Edit native source relationships through validated proposals

Status: missing-contract. Dependencies: GRAPH-ROWS-01.

## Problem

`UPDATE_IMPORTED_SOURCE_STEP` can change bounded settings. It cannot add a native traversal. The existing graph disables imported-recipe route mutations, so moving its controls does not enable the default workflow.

## Change

Implement catalog-authorized add, edit, and remove relationship proposals against the contract in 01. Extend the project-local draft/source-plan overlay while preserving the immutable recipe pin and digest. Add typed server-owned relationship nodes, stable addition IDs, removal semantics, and a validated OPTIONAL or REQUIRED match-mode override. Existing `UPDATE_IMPORTED_SOURCE_STEP` settings and the fixed original step set cannot express these changes. Resolve the source and all affected workspace outputs before committing a draft change.

Keep graph relationship edits distinct from `RELATED_EXPAND`. An optional relationship that supplies values preserves row identity. A required match can filter source rows. A deliberate expansion follows the row contract in 03.

Preview dependent columns, filters, operations, and consumers before removing or changing an upstream relationship. Reuse existing dependency-repair mechanisms where they apply. Never silently delete downstream work.

Own native source command and lifecycle implementation, `internal/server/explorer_imported_source_edit.go`, and focused transport tests. Keep shared schema integration with the coordinator.

## Acceptance

- Add an eligible relationship to an imported default output through the new typed overlay, preview it, Apply, reload, edit its authored match policy, and remove it. Verify that the original recipe reference and digest stay unchanged.
- Reauthorize catalog edge, direction, route occurrence, project, generation, and snapshot on the server.
- Reject unauthorized, ambiguous, stale, cyclic, or unsupported routes without changing the saved plan or shared default recipe.
- Cancel and proposal failure preserve the saved draft. Applying uses the current version and digest.
- A graph-only value relationship does not become an implicit row expansion.
- An upstream edit with downstream dependents either preserves valid work or offers an explicit repair proposal. A rejected edit is atomic across the workspace.
- Tests use known missing and multiple matches and assert resulting record identities and values.

## Execution contract

Read [the work package](../README.md) and repository `AGENTS.md` before implementation. Use the agreed baseline and an isolated worktree. Preserve unrelated work. Run the smallest relevant checks, then the required final acceptance against a frozen source snapshot. Record source identity, review, commands, outcomes, and remaining gaps. Do not deploy, publish, or push as part of this ticket.
