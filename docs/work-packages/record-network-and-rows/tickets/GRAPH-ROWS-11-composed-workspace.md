# GRAPH-ROWS-11: Compose both editors into a legible workspace

Status: ready-implementation for prototype after 08; integration follows 09 and 10. Dependencies: GRAPH-ROWS-08
for shared editing state; GRAPH-ROWS-09 and GRAPH-ROWS-10 for final integration.

## Gap

The Calypr screenshot places a large steps area and whitespace above a graph that
extends below the visible image. Row controls, relationship actions, source settings,
and proposed results use separate presentation areas. The standalone comparison
does not prove the finished Calypr layout during an edit.

## Deliverable

Refactor the existing workspace around three tasks: edit the recipe, inspect one
selected fact, and inspect the result. Keep **Record network** and **Define a row**
easy to reach. Allow a focused editor and a combined view. Switching presentation
must preserve authored state and 08's active proposal.

Keep the current row definition and useful preview visible while either editor is
active. Bound step history and long catalogs with deliberate expansion and internal
navigation. Preserve ordered step editing and operation boundaries. Avoid duplicate
row controls, competing inspectors, and separate previews with unclear ownership.
Display whether the result belongs to the saved or proposed recipe.

Compare two compact compositions using the real default recipe inside the Calypr
navigation frame. Include idle, selected relationship, open row editor, and previewed
change states. Reuse the existing layout comparison where its assumptions hold.
Choose the layout from these working prototypes before a broad component refactor.

Own workspace composition and shared BuilderWorkspace integration. Move existing
controls into clear component boundaries rather than duplicating them. Coordinate
one writer for each shared file. Add no effect-based synchronization.

## Acceptance

- At 1536×900, the Calypr frame contains an identifiable authored path, current row expression, one active inspector, and a preview at least 150px high. Apply and Cancel are reachable without page-level scrolling during routine edits.
- The shorter viewport uses bounded internal navigation for large graphs, histories, and catalogs. At 1536×1100 and back, selection, pending edits, and useful result space survive.
- Both editing entry points remain visible and named. A user can focus either interface or open both without losing the selected table or changing the recipe.
- A 100-operation plan retains ordered groups and discoverable step editing without pushing the working editors out of view. Collapsing history never alters execution order.
- Editing through either interface uses the same result identity and proposal status. Loading, errors, missing values, and unavailable results remain distinguishable.
- Keyboard focus follows editor switches and returns to the initiating control after Apply or Cancel. Current row meaning is available to assistive technology.
- Existing table management, source-step editing, column selection, and publication entry points remain usable. This ticket does not publish data.

## Checks and handoff

Run focused composition checks, then ticket 07's native paths against the integrated
source. Retain final Calypr screenshots for idle and proposed states and the exact
source identity. Static mockups and a successful page load do not establish closure.
Follow repository isolation and review rules. This ticket authorizes no deployment.
