# GRAPH-ROWS-10: Make the traversal viewer a complementary recipe editor

Status: verification-in-progress (implementation integrated; current-source native and authenticated acceptance incomplete). Dependencies: GRAPH-ROWS-08; reuse 02–05.

## Gap

The graph already edits source relationships and offers row actions. The remaining
contract must make a row-authored recipe understandable and editable here, including
row-stage effects that cannot be conveyed by resource connectivity alone.

## Deliverable

Distinguish the authored traversal from available catalog connections. Keep full
catalog exploration available. Make repeated occurrences distinguishable by their
path and role. Use metadata labels and retain technical identity in details.

Selecting an authored occurrence or relationship opens one inspector for its
supported actions: add related values, require a match, or create related-record
rows. These actions must name their occurrence and stage. Show where rows are
preserved, excluded, expanded, or grouped. A Group annotation identifies its key
tuple and input stage; it does not pretend to be a FHIR resource or traversal.

Project row-expression changes into the graph and inspector using 08's shared
contract. Graph edits update the row expression and shared preview. Inspection
alone does not change a construction anchor. Keep imported proposal writes and
ordinary Route writes on their existing authoritative paths.

Own graph projection, selection, inspector presentation, and focused tests. Shared
workspace integration belongs to 11. Add new graph node kinds only when the row
semantics cannot be communicated clearly by existing nodes and annotations.

## Acceptance

- Open a row-authored recipe and identify its actual relationship occurrences, current row unit, and policies without reconstructing them from field names.
- Add and edit a relationship visually, then inspect the updated row expression. The selected edge, match policy, and row effect agree.
- Switch from related values to explicit related-record rows through the graph's inspector. The expression identifies input/related-record pairs, not globally unique related records.
- Explain the columns after expansion. Existing list columns retain their identity and policy; a per-match scalar requires an explicit choice. The graph inspector and row expression describe the same before/after values.
- A grouped result shows all key fields and the correct input scope while retaining the original source traversal for inspection.
- Two Patient paths remain distinct, including after a change in the row editor. No graph selection silently retargets a pending operation.
- Unsupported operations explain the exact limitation before Apply. Removing a required upstream relationship uses existing dependency review or rejects atomically.
- Keyboard users can select the path, edit through its inspector, and reach Define a row. Proposed graph state is distinguishable from saved state and Cancel restores it.

## Checks and handoff

Reuse imported/ordinary native cases and exact fixtures. Add focused projection
and selection checks for the shared contract. Ticket 07 owns graph-first and mixed
native closure. Follow repository isolation and review rules. This ticket authorizes
no deployment.

## Integrated follow-up checkpoint

The shared owner, editable row expression, authored traversal and composed workspace
are integrated. Current automated checks and scoped same-model reviews are recorded
in [VERIFICATION.md](../VERIFICATION.md). The 12-case native matrix remains pending
after layout and saved-preview repairs, with ordering repaired and remaining current native cases pending; authenticated Calypr acceptance is blocked by a
missing short-lived token. Historical 01–07 evidence does not close these follow-up
criteria. This ticket remains verification-in-progress.
