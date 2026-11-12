# GRAPH-ROWS-01: Define one authoring contract for the record network and row definition

Status: ready-implementation. Dependencies: None.

## Problem

The graph edits `Document.route`, but the default Helm recipe's traversals live in a pinned recipe. A shared editor needs a faithful plan it can read and edit without maintaining a separate canvas model.

## Change

Define the authoritative contract for relationships, row identity, value policies, and ordered operations. Extend the existing authoring model only where it cannot express the approved behavior. Keep compiler lowering behind that boundary.

Specify authoritative ownership for each document mode: ordinary documents author Route and Rows; imported documents derive their effective document from the pinned recipe and source changes. A common read and edit contract must not persist a competing graph.

Specify stable occurrence and operation IDs, exact catalog-edge identity, the starting records, and the relationship between the source plan and later construction stages. Represent repeated resource types as separate occurrences.

Describe both the source row definition and the derived grain at each construction stage. Keep `Rows.EXPANDED`, construction `RELATED_EXPAND`, named source groups, and construction `GROUP` distinct.

Expose structured source, target, edge, and occurrence metadata with available display labels. Mark missing labels explicitly. Keep executable expressions server-owned.

Define the namespace for newly added source relationships. Recipe index-path step IDs differ from route occurrence IDs, and imported columns currently share the `base` occurrence. Hydrate an explicit server-owned mapping between source steps, occurrences, column bindings, and construction anchors.

Implement read-side hydration for the bundled default recipe and existing non-imported documents. Document how the write path in ticket 02 addresses each hydrated relationship. Define a typed project-local overlay for added and removed relationships and traversal match-mode overrides. The existing overlay accepts only the original step set and bounded settings, so these are new authored contracts. Preserve the original recipe pin and digest. Do not infer an editable route from a display title or FHIR path string.

Own `internal/explorer/authoringv2`, `internal/explorer/recipeimport`, and focused contract tests. Coordinate OpenAPI, generated transport, and UI type integration with the parent.

## Acceptance

- All five default outputs hydrate with the same traversal nesting, ordering, match modes, selected fields, caps, value modes, and row semantics.
- Import, serialize, reload, and lower without semantic loss. Compare compiled behavior with independent fixtures, not JSON shape alone.
- Two occurrences of Patient with different paths retain distinct identities and bindings. Source insertion or removal cannot silently retarget path-index-based bindings. Preserve identities or migrate every affected reference atomically.
- Existing legacy documents have an explicit adapter or migration rule. Ambiguous state produces a precise diagnosis rather than a guessed plan.
- The contract distinguishes preserving row identity, excluding unmatched rows, and expanding rows.
- Public contract changes update OpenAPI, generated bindings, runtime validators, and UI-Go semantics-version checks together.
- Publish a concrete contract and fixtures that unblock 02, 03, and 04. Record unsupported compositions explicitly.

## Execution contract

Read [the work package](../README.md) and repository `AGENTS.md` before implementation. Use the agreed baseline and an isolated worktree. Preserve unrelated work. Run the smallest relevant checks, then the required final acceptance against a frozen source snapshot. Record source identity, review, commands, outcomes, and remaining gaps. Do not deploy, publish, or push as part of this ticket.
