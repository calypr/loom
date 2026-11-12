# GRAPH-ROWS-05: Connect column policies and ordered operations to the row definition

Status: missing-contract. Dependencies: GRAPH-ROWS-02; GRAPH-ROWS-03; GRAPH-ROWS-04.

## Problem

A graph can make many records available for one row. Column selection, Group summaries, and retained contributor values must explain how those records become values without silently changing the row unit.

## Change

Bind source-field pickers to an authored source occurrence. Bind post-source operations to a concrete construction stage and a capability-provided anchor, such as `relatedExpandAnchors`. Define the occurrence-to-anchor mapping or show the selected anchor explicitly. A global graph selection must not silently retarget a construction operation.

Show the eligible fields and policies from capabilities. Keep Group keys, calculated summaries, and retained contributor values distinct.

Explain list, first-value, one-distinct-value, count, presence, and aggregate behavior only when the backend supports that exact choice. Let the user request an explicit row expansion from the same context. Preserve per-operation null, missing, repeated-value, and coding policies.

Build the compact ordered overview from the authoritative plan. Collapse adjacent compatible column operations within their source scope. Keep row-changing and eligibility boundaries explicit. Selecting a group reveals its original ordered steps and one active inspector.

Own column and Group editor components, source-history presentation, and focused interaction tests. Coordinate shared workspace edits with ticket 04's integration owner.

## Acceptance

- Adding a field from a many-valued relationship requires or displays its actual value policy. It never silently picks a record or changes rows.
- After a deliberate row change, field availability and contributor scope describe that new stage. The operation keeps its explicit stage anchor when graph selection changes. Stale choices cannot apply to the previous stage.
- A 100-operation recipe has usable collapsed groups. Expanding them preserves every operation ID and its execution order.
- Source match requirements and later output-row filters identify the stage where they apply.
- Column runs on opposite sides of Group, Filter, Expand, or a source-scope boundary remain separate.
- Editing an earlier step identifies affected columns and operations and uses the same proposal and preview flow.
- Group keys remain visible when a catalog closes. Summaries and retained contributor values retain their separate policies through Apply and reload.

## Execution contract

Read [the work package](../README.md) and repository `AGENTS.md` before implementation. Use the agreed baseline and an isolated worktree. Preserve unrelated work. Run the smallest relevant checks, then the required final acceptance against a frozen source snapshot. Record source identity, review, commands, outcomes, and remaining gaps. Do not deploy, publish, or push as part of this ticket.
