# GRAPH-ROWS-04: Edit the record network and row definition in the main workspace

Status: verification-in-progress (implementation and same-model review complete; required current-source native acceptance incomplete). Dependencies: GRAPH-ROWS-01; integrate GRAPH-ROWS-02 and GRAPH-ROWS-03.

## Problem

The graph is hidden under advanced source setup. The row editor occupies a separate full-screen workflow. Users cannot compare their network, row definition, and result while making a relationship decision.

## Change

Reuse the existing graph renderer and route-choice components in the main dataframe editor. Bind them to contract 01 rather than a separate saved canvas model. Make **Related records** or equivalent plain-language entry visible beside column and row actions.

Imported-recipe graph mutations must use ticket 02's native source proposals, never the ordinary document's `ADD_ROUTE` command. Preserve the existing `ADD_ROUTE` workflow for ordinary documents.

Keep an explicit **One row per ...** control visible with the network and preview. Selecting a graph occurrence exposes one inspector. Offer row-preserving data attachment and deliberate row expansion as distinct supported actions. Show the effects from 03 before Apply.

Compare two compact arrangements using the same real default recipe and actual field metadata. Retain the stronger layout. Collapse irrelevant branches while keeping the selected path and current row identity visible. Keep FHIR paths and storage directions in details.

Own `GuidedGraphWorkspace.tsx`, route-picker presentation, and new layout components. The coordinator owns shared `BuilderWorkspace.tsx` integration. Use no new `useEffect` or `useLayoutEffect` synchronization.

## Acceptance

- A user opens the default imported recipe and reaches editable related records from the main editor, without the advanced-source detour.
- The selected path, row definition, selected inspector, and useful actual preview are visible at the tested desktop viewport. The row control distinguishes source RECORDS, GROUPS, and EXPANDED identities from the current construction-stage grain. It describes construction GROUP key tuples and RELATED_EXPAND rows without pretending they changed the source RowDefinition. Unsupported source variants stay unavailable. A root label alone is insufficient.
- Routine graph and row edits require no page-level scrolling. Long catalogs and graphs have deliberate bounded navigation at readable text sizes.
- Ambiguous routes remain distinguishable through meaningful metadata and explicit detail. Do not invent clinical relationship labels.
- Keyboard users can enter the graph, choose a relationship, open its inspector, and return to the row control.
- Saved and proposed states are visually distinct. Cancel restores the saved view without a draft mutation.
- Final native acceptance proves imported graph edits update the native source through ticket 02, while ordinary graph edits save `Document.route`. Both survive reload.

## Execution contract

Read [the work package](../README.md) and repository `AGENTS.md` before implementation. Use the agreed baseline and an isolated worktree. Preserve unrelated work. Run the smallest relevant checks, then the required final acceptance against a frozen source snapshot. Record source identity, review, commands, outcomes, and remaining gaps. Do not deploy or publish. The operator separately authorized the parent to push the completed code after functional checks pass.
