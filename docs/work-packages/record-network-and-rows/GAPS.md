# Finish the two recipe editors

This follow-up starts from `58544dcbb` and the operator's Calypr screenshots on
2026-11-12. It records product gaps, not new evidence that the existing backend
is broken. [VERIFICATION.md](VERIFICATION.md) retains the earlier scoped results.

## Intended experience

Offer **Record network** and **Define a row** as complementary ways to edit one
dataframe recipe. Users can work mainly in either interface or alternate between
them. Both remain easy to reach, and both share the result preview.

The graph explains how records connect. The row definition explains how those
records become rows and column values. Some edits overlap. Other edits belong
naturally in one interface and appear as meaningful annotations in the other.
Neither interface owns a separate saved recipe.

For example, connect ResearchSubject to Patient and Observation in the graph.
Then define one row per subject with observation values collected into a list.
Change the row definition to one row per subject–observation pair and retain the
same relationship path. Group by chosen fields as another explicit operation.
The graph must distinguish the source relationships from these output-row stages.

## Current implementation and remaining gaps

| Area | What exists | Remaining work | Owner |
| --- | --- | --- | --- |
| Authoritative plan | Ordinary Route/Rows; imported pin plus source overlay; network and stage-identity API | Define edit coverage and correspondence between both interfaces. Reuse existing contracts before adding fields. | [08](tickets/GRAPH-ROWS-08-shared-editing-contract.md) |
| Traversal editor | Catalog nodes aggregate repeated resource occurrences; an adjacent traversal strip identifies exact occurrences; imported proposals support add, match-policy change, and remove | Make the authored path and repeated occurrences clear; connect graph actions and row-stage annotations to the shared editing contract. | [10](tickets/GRAPH-ROWS-10-graph-recipe-editor.md) |
| Row editor | Row-meaning sentence and a panel for records, Group, reshape, and related rows | Add a legible, editable row expression that includes relevant connections, value policies, and stage context. The current action panel is not that representation. | [09](tickets/GRAPH-ROWS-09-row-recipe-editor.md) |
| Shared editing | Existing proposal receipts, draft ownership, Cancel, and conflict handling | Define what happens to pending edits when users switch or combine interfaces. Prove both directions against the same recipe. | 08, [06](tickets/GRAPH-ROWS-06-save-and-reload.md), [07](tickets/GRAPH-ROWS-07-acceptance.md) |
| Workspace | Network and preview beside each other; compact step groups | Refactor the composition so history and whitespace do not displace the actual editors. Test the Calypr frame and open editing states. | [11](tickets/GRAPH-ROWS-11-composed-workspace.md) |
| Product acceptance | Scoped imported/ordinary native passes and independent exact-result fixtures | Prove graph-first, row-first, and mixed editing in the finished UI. Complete retained lifecycle gaps and authenticated Calypr acceptance. | 06, 07 |

Source anchors are [BuilderWorkspace](../../../ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx),
[GuidedGraphWorkspace](../../../ui/packages/loom-ui/src/features/ExplorerBuilder/components/GuidedGraphWorkspace.tsx),
[RowDefinitionSettingsPanel](../../../ui/packages/loom-ui/src/features/ExplorerBuilder/components/RowDefinitionSettingsPanel.tsx),
[AuthoringNetworkInspector](../../../ui/packages/loom-ui/src/features/ExplorerBuilder/components/AuthoringNetworkInspector.tsx),
and [ConstructionWorkspace](../../../ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/ConstructionWorkspace.tsx).
The saved-network inspector currently loads a read-only description inside a
disclosure. It does not provide an abstract recipe editor.

Relationship, source-row, and construction proposals already write to the same
saved draft through separate typed APIs. The UI owns their pending state in
separate components. Coordinating those interactions does not require a new saved
recipe model. Imported relationships currently have no in-place edge-replacement
operation. Ticket 08 must identify that limitation rather than calling match-policy
editing a complete edge editor. Add edge replacement only with explicit dependency
and atomicity semantics if the first-release workflow requires it.

The screenshots show a row-definition link and current row sentence. They also
show a large history area above a partially visible graph. They establish a
composition and discoverability concern, not a failed row command. The visible
dashes do not establish a data defect. Use known fixture expectations to distinguish
missing values, loading, unsupported projections, and actual failures.

## Bound the next implementation

Start with a structured row expression whose editable terms select server-supported
records, occurrences, keys, and policies. Prototype its notation before fixing the
interaction design. A free-text language is a separate design decision.

The first complete workflow supports record rows, related values with explicit
policies, required matches, related-record rows with an empty-match policy, and
Group keys with summaries or retained values. Existing other operations must remain
visible and editable through their existing inspectors. They must survive switching
interfaces without being erased or misrepresented.

Keep imported root replacement, unsupported independent expansions, and ambiguous
source provenance explicit. The new interfaces cannot advertise an operation that
the authoritative plan cannot represent. Preserve the pin and existing compiler.

Implement 08 first. Then 09 and 10 can proceed independently with separate files
and worktrees. Prototype 11 against the agreed contract while those editors develop;
its owner integrates the shared workspace. Finish 06 and 07 on the integrated
snapshot. Runtime verification remains serialized where it shares mutable state.

Completion means a user can start in either editor, combine supported changes from
both, inspect the same proposed result, and reopen the saved recipe with both
representations still agreeing. A visible button, a page load, or two independent
edit panels does not close this work.
