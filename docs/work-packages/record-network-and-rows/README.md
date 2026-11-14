# Work package: edit the record network and row definition together

Status: implementation and owned verification complete: all12 final native cases/105 actions, canonical graph/row equivalence and100-operation acceptance pass at source134faf8b…. GRAPH-ROWS-06–11 remain verification-in-progress solely for required authenticated Calypr acceptance (missing short-lived token; actual267-column host frame unverified). See VERIFICATION.md for exact identities and preserved historical failures.

## Product goal

Let a person define which records connect and what one output row represents in the same dataframe editor. Show how either edit changes the other. Keep a useful result preview visible throughout the edit.

The record network answers which records and relationships are available. The row definition identifies one output row. Column policies explain how values from several contributing records fit into that row. An ordered history explains how the table reached its current state.

Example: connect research subjects to patients and observations while keeping one row per research subject. Keep observation values as a list or calculate a summary. Changing to one row per related observation for each subject row is an explicit row operation. Show repeated subject values and the missing-match policy before Apply. If two subject rows refer to the same observation, that expansion can retain both subject-observation pairs. A globally unique observation row unit requires its own explicit root or grouping choice.

Grouping may define a row by several keys. Repeated-field expansion may define a row by an item within a record. Do not restrict row identity to a resource-type name or assume every row is a node in the graph.

The operator clarified that these are two complementary recipe-editing interfaces,
not a graph viewer with a row-settings shortcut. Users can start in **Record
network** or **Define a row**, then combine supported edits from both. The graph
expresses connections; the row expression defines how connected records become
rows and values. Both project the same authoritative recipe and share its preview.

## Follow-up implementation tickets

The [gap analysis](GAPS.md) records integrated editing behavior and remaining
acceptance obligations. Preserve the scoped 01–07 evidence. These follow-ups do not
retroactively claim that evidence proved the clarified two-interface workflow.

| Ticket | Concrete result | Prerequisite |
| --- | --- | --- |
| [GRAPH-ROWS-08](tickets/GRAPH-ROWS-08-shared-editing-contract.md) | One editing contract and proposal owner for both interfaces, with explicit coverage and switching rules | Existing 01–03; reuse persistence APIs |
| [GRAPH-ROWS-09](tickets/GRAPH-ROWS-09-row-recipe-editor.md) | An editable row expression for connections, row units, values, and supported operations | 08 |
| [GRAPH-ROWS-10](tickets/GRAPH-ROWS-10-graph-recipe-editor.md) | An authored-traversal editor that reflects row-expression edits and explains row-stage effects | 08 |
| [GRAPH-ROWS-11](tickets/GRAPH-ROWS-11-composed-workspace.md) | A refactored workspace with both interfaces, one inspector, bounded history, and a shared result preview | Prototype after 08; integrate 09 and 10 |

Finish [06](tickets/GRAPH-ROWS-06-save-and-reload.md) and
[07](tickets/GRAPH-ROWS-07-acceptance.md) with graph-first, row-first, and mixed
editing paths after integration. Implement 09 and 10 in parallel under 08's
contract. One workspace owner integrates shared files for 11. Keep the current
compiler, recipe pin, proposal validation, and unsupported-composition boundaries.

## Current code and the main gap

This section describes the pre-01 baseline. Use [GAPS.md](GAPS.md) for the current
follow-up implementation and remaining acceptance work.

The existing graph in `GuidedGraphWorkspace.tsx` edits `Document.route`. It appears under **Advanced source setup**, then **Advanced graph**. Its route extension panel already uses **Add traversal** and **Add branch**. Imported recipes disable those mutation controls.

The default Helm recipe stores native traversals in its pinned recipe. `ImportedSourceSteps` displays the source plan and edits bounded step settings. `UPDATE_IMPORTED_SOURCE_STEP` and `REFRESH_IMPORTED_SOURCE` cannot add a relationship. A visible graph button therefore cannot complete the default-recipe workflow by itself.

Ordinary documents author `Route` and `Rows` directly. Imported documents derive their effective `Route`, `Rows`, and columns from the immutable pin and project-local source-plan overlay. Existing source edits preserve the original pin and digest. Each authored fact needs one authoritative owner. A common editor contract does not require a second persisted graph or a new universal compiler representation.

The source row definition and the current construction-stage grain are distinct. `Rows.EXPANDED` identifies a repeated field within one route occurrence. Construction `RELATED_EXPAND` expands related records. Field-key grouping is implemented by construction `GROUP`; the source `Rows.GROUPS/FIELD` form is currently rejected. Named source groups use `Rows.GROUPS/EXPLICIT`.

Existing construction operations already distinguish `RELATED_SOURCE`, which adds values, count, or presence, from `RELATED_EXPAND`, which changes rows. Reuse those semantics. The existing OPTIONAL or REQUIRED route match policy is not a request to expand rows.

Source anchors:

- [Existing graph](../../../ui/packages/loom-ui/src/features/ExplorerBuilder/components/GuidedGraphWorkspace.tsx) and [route controls](../../../ui/packages/loom-ui/src/features/ExplorerBuilder/components/RouteExtensionPanel.tsx).
- [Builder integration](../../../ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx) and [ordered history](../../../ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/constructionHistory.ts).
- [Imported source plan](../../../internal/explorer/recipeimport/source_plan.go), [source commands](../../../internal/explorer/authoringv2/source_commands.go), and [source edit boundary](../../../internal/server/explorer_imported_source_edit.go).
- [Row definition](../../../internal/explorer/authoringv2/row_definition.go), [row-change proposal](../../../internal/explorer/authoringv2/row_change.go), and [construction proposal](../../../internal/explorer/lifecycle/construction_proposal.go).

## Required behavior

1. Keep the record network, current row definition, selected operation, and result preview connected to one authoritative saved plan.
2. Adding related data preserves the row identity unless the user explicitly chooses a row-changing operation. A required match can exclude rows without expanding them. Explain both effects.
3. Show one-to-many choices at the relevant relationship or column: retain a list, use a supported summary, require one value, or explicitly expand rows. Preserve the existing distinction between first value and one distinct value.
4. Derive row meaning from identity and stage semantics. Separate total counts, sampled counts, observed multiplicity, and unknown facts. A preview sample does not prove a universal one-to-one relationship.
5. Keep catalog edge IDs, relationship occurrence IDs, value policies, and operation IDs intact. Two routes to Patient remain distinct.
6. Group only consecutive compatible operations. Expansion, filtering, grouping, and other semantic boundaries remain visible. Visual grouping never changes execution order.
7. Show proposed row and column effects before Apply. Cancel, errors, stale responses, and rejected upstream edits preserve the saved plan.
8. Use existing metadata for names. Put FHIR paths and compiler details behind inspection. An unfamiliar field or ambiguous relationship stays explicit.

## Original 01–07 tickets and dependencies

The [ticket files](tickets/) contain scope, code ownership, and acceptance checks. The active repository queue is [BUILDER_WORKLIST.tsv](../../BUILDER_WORKLIST.tsv).

| Ticket | Deliverable | Depends on |
| --- | --- | --- |
| [GRAPH-ROWS-01](tickets/GRAPH-ROWS-01-authoring-contract.md) | Define and hydrate the shared authoring contract | None |
| [GRAPH-ROWS-02](tickets/GRAPH-ROWS-02-relationship-edits.md) | Add, edit, and remove native source relationships safely | 01 |
| [GRAPH-ROWS-03](tickets/GRAPH-ROWS-03-row-effects.md) | Compute row identity and relationship effects | 01 |
| [GRAPH-ROWS-04](tickets/GRAPH-ROWS-04-main-editor.md) | Put the graph and row definition in the main editor | 01; integrate 02 and 03 |
| [GRAPH-ROWS-05](tickets/GRAPH-ROWS-05-values-and-operations.md) | Connect column policies, Group, and ordered operations | 02, 03, 04 |
| [GRAPH-ROWS-06](tickets/GRAPH-ROWS-06-save-and-reload.md) | Preserve the complete plan through save, reload, and conflict | 02, 03; integrate 04 and 05 |
| [GRAPH-ROWS-07](tickets/GRAPH-ROWS-07-acceptance.md) | Prove exact rows and complete native lifecycles | Fixture work now; final run after 02–06 |

The following is the original implementation sequence, retained for provenance.
New work follows the 08–11 sequence above and the follow-up closure in 06 and 07.
Originally, 01 and the independent fixture specification in 07 started first.
Then 02 and 03 ran in separate worktrees, with 04 prototyped against the agreed
response types. Integration of 05 and 06 followed their contracts. The coordinator
owns shared OpenAPI, generated-client, and `BuilderWorkspace.tsx` integration so
parallel jobs do not overwrite each other.

Each remote job reads this README and its ticket. Give it one worktree, explicit file ownership, the baseline commit, and the required checks. Make the branch and [reference recipe](fixtures/README.md) available before dispatch. A local worktree or `/private/tmp` evidence path is not a cloud input.

## First release boundaries

Complete the default Helm recipe workflow and a small independent fixture. Also retain the existing non-imported graph workflow. Reject unsupported compositions with a precise reason and leave the saved plan unchanged.

This package does not authorize a new query compiler, arbitrary many-to-many row products, new FHIR datatype interpretation, publication, deployment, or an LLM integration. An LLM can later propose the same typed plan and use the same validation and preview flow. It must not create a second authoring representation or bypass confirmation of ambiguous relationships.

Imported root-preserving changes remain blocked until an explicit source-root overlay can represent them. A deliberate source replacement may detach the pin and reset the table under the existing replacement review. Expose that consequence before confirmation. Do not patch an imported document's projected Route or Rows independently.

Existing unsupported related-route compositions remain unsupported until a ticket explicitly defines and proves their semantics. Preserve the boundaries in [ROW_OPERATION_SOURCE_INPUT_BACKLOG.md](../../ROW_OPERATION_SOURCE_INPUT_BACKLOG.md) and the existing reliability obligations in [BUILDER_GOAL.md](../../BUILDER_GOAL.md).

## Acceptance and carry-in checks

Use the unmodified [default recipe](fixtures/default-dataframer.json) for import and editor checks. Use independently specified records for exact result assertions. Include zero, one, and several matches, two distinct routes to the same resource type, and independent related branches with two and three matches.

Verify graph edits with unchanged row identity, deliberate expansion, missing-match policies, grouped rows, retained contributor values, upstream edits, and downstream dependency failures. Prove Preview, Cancel, Apply, edit, removal, reload, and recovery from a real stale draft on the relevant native paths. Derive expected records and values independently of the compiler under test.

The presentation snapshot `4b64797e3` has focused tests, a UI build, and scoped review. A preceding browser snapshot proved a loaded 25-row, 267-column preview and page fit at 1536×900 and 1536×1100. The final label-only changes did not receive full native acceptance. Source-step edit/reload/restore and Group preview/cancel were not run before the operator requested the commit. Ticket 07 owns those carry-in checks. Do not report this baseline as a completed authoring lifecycle.

For visible changes, use `.codex/skills/verify/SKILL.md`. For the authenticated Calypr target, also use `.codex/skills/verify-loom-ui/SKILL.md`. Its current driver proves Builder load, not the complete authoring lifecycle. Follow `AGENTS.md` for exact-snapshot review, runtime ownership, and local commits. Use the FHIR modeling skill if a ticket begins interpreting FHIR values rather than copying an already selected scalar.

The release requires the final integrated source and API identity, reviewed contracts, exact expected results, and complete native lifecycle evidence. Record missing coverage as pending. Existing scoped evidence remains reusable when its relevant source and assumptions are unchanged.

The operator explicitly removed the five-second timing gate on 2026-11-13 and
authorized pushing the code once functional checks pass. Functional acceptance
uses bounded 30s actions/requests with exact result, pin, persistence and conflict
checks intact; report latency separately. Deployment and publication remain out
of scope.
