# Build dataframes from the schema

## Current priority: table-first Builder UI cleanup

Complete this subgoal before closing more operation work packages. The table is the main workspace. Keep a compact row summary with the table title; open one Rows editor for starting records, related rows, grouping, expansion, and reshape choices. Keep all executable options available, with plain labels first and technical paths in details. Put coded-value discovery and source management inside Add columns. Complex editors use the workspace width and offer clear Apply and Back to table actions; they do not squeeze beside the table. Keep the left table list compact, with history details available on selection. Remove redundant cards and explanatory copy that obscures the current table. Calculate and derived arithmetic columns remain outside this goal.

Group and Pivot must discover usable keys and values from schema-derived coded and related data inside Rows, before regular column selection. If the operation needs a source field, configure it in the same flow. A Group menu that only offers record IDs and references does not satisfy row design, even if the backend can execute it. Keep discovery independent of any one FHIR resource type.

Direct-source Group now discovers authorized, populated scalar keys in Rows and can count records by one key without adding a raw FHIR column first. A CDA Condition browser path grouped by onset, checked displayed counts against Arango, applied, reloaded, edited, removed, and restored the table. The editor-to-preview time was 4.48 seconds; opening Rows through preview took 6.78 seconds on a fresh table. This is a partial row-first path. Pivot still depends on projected stage columns, and multi-key, related-source, and transformed-stage source grouping are not yet supported. Close those gaps without making users add plumbing columns first.

Accept this subgoal only after fresh-page CDA browser paths for Rows, Add columns, Filter rows, and Reshape show their available options, render a result, save, reload, edit or remove, and restore the prior table. Check the click path and readable labels, inspect the layout at desktop and narrow widths, and record preview times; an interaction that takes more than five seconds needs a measured performance fix or an honest visible limitation. Backend and frontend success are separate checks.

Revision 4. Planned on 2026-09-19. This is the only active product execution plan.
Deliver a column-by-column table builder whose construction choices come from the installed FHIR schema, applicable semantic definitions, observed data, and compiler support.
Keep custom graph authoring and make grouping, expansion, and reductions explicit.
Execute S01, then S02 and S03, then S04 and S05. Do not start another general architecture audit.

## How to read this

Use this plan for scope and implementation decisions, [the acceptance protocol](ml-dataframer/ACCEPTANCE.md) for executable proof, and [the ledger](ml-dataframer/execution.json) for status.
Each task names a result to verify. Mark a package accepted only after its focused tests, live journey, literal outputs, negative cases, performance, and review pass.
All new tasks and KPIs start unaccepted. Existing implementation is reusable, not automatic acceptance of this vision.

Each S package is a backend-to-frontend delivery slice. UI01-UI05 below name the paired frontend work packages. They share their parent's acceptance and ledger tasks, rather than forming a later frontend phase. An API or compiler test cannot close a slice without its working controls and browser journey.

Use pstack's `playbooks/autopilot-stack.md` for ownership and integration, with these user-specific overrides. Work locally until remote writes are requested. Keep sequential work on `arch/integration`; create branches only for genuinely parallel edits. The foreground agent owns integration and judgment. Use one Luna xhigh worker by default and one coherent Sol review. Do not require ten verification agents, a human interaction review, or a full browser run per helper edit. The operator controls landing.

Baseline HEAD is `1301c3b30ceb26e4e37344b61afb890bd6d64564` on `arch/integration`. The worktree has uncommitted implementation changes. This SHA alone is not a reproducible baseline. Capture and review the dirty diff before implementation, then record the exact accepted checkpoint and serving build identity.

## Deliver the frontend with each backend capability

| Frontend package | Backend partner | What changes in the app | Ledger implementation tasks |
| --- | --- | --- | --- |
| UI01. Build the table column by column | S01 schema discovery and preserving execution | Search concepts or fields, add columns, keep the table visible, and inspect the selected column | S01-03 and S01-04 |
| UI02. Inspect and customize the source | S02 relationship planning | Open Advanced graph, author a route, choose codes at a reached node, and resolve genuine source ambiguity | S02-03 and S02-04 |
| UI03. Design the rows | S03 row construction | Select starting records, group by fields or explicit members, and expand repeated values with a before/apply preview | S03-03 and S03-04 |
| UI04. Customize column values and table shape | S04 typed operations | Edit value handling, time, units, recoding, pivots, and derived columns in the selected-column inspector | S04-01 through S04-04 |
| UI05. Review and deliver the dataset | S05 publication and artifact fidelity | Review checks and information loss, publish, inspect cell evidence, and download an explicit output representation | S05-01 through S05-04 |

Use one Builder workspace. Put the dataset/starting-collection and row summary above the table. Place searchable available columns beside the table, with the selected column's inspector in a side panel or drawer. Keep Preview and save state visible while editing. Advanced graph is an alternate authoring view of the same saved intent, not a separate builder or a prerequisite for finding ordinary fields.

Keep technical paths, code systems, and exact source records accessible through the inspector. Show friendly labels first without hiding ambiguity, multiplicity, or reductions. Adding a column displays its result; opening the catalog never adds columns automatically. Retain rename, reorder, remove, filtering, and chart configuration already available in the Builder.

Implement only the controls backed by the paired compiler capability. Show unavailable choices with their actual reason. Required decisions stay inline with the column being edited. An unsupported operation is not a request for the user to repair the dataset.

Reuse the existing paged catalog, graph editor, Rows selector, policy editor, Preview, Viewer, and download handlers. These controls already exist in part. The new work connects them to schema-derived construction choices, adds missing group/shape controls, and makes the authored table the primary view. Existing controls alone are not acceptance of a frontend package.

Every new output shape must render, save/reload, and pass the existing publication/export path in its owning slice. Move the minimum needed consumer support into that slice. S05 owns the combined review/download experience and integrated proof, not permission to defer broken consumers until the end.

## Program checklist

- [ ] Confirm the implementation checkout and preserve every existing change. Inventory unfinished semantic, contract, and catalog edits before accepting or replacing them.
- [ ] Start implementation only after the user approves this revised plan. Planning does not authorize a push, merge, dataset purge, or deployment.
- [ ] Capture a comparable baseline for each changed behavior before editing. Record unavailable features as unavailable instead of inventing a baseline result.
- [ ] Use the existing mounted Go watcher and Vite workflow. Verify the Compose project, source mounts, fixture generation, and serving build before DOM checks.
- [ ] Complete S01 as one backend-plus-frontend slice before expanding the operator set. Do not finish a detached FHIR framework first.
- [ ] After S01 freezes the construction contract, optionally run S02 route/graph work and S03 row-shape work in separate worktrees. Root alone owns shared OpenAPI, generated contracts, `types.ts`/`api.ts`/`react.tsx` boundary integration, workspace migrations, and `BuilderWorkspace.tsx` wiring. Coordinate the shared `PopulationPanel.tsx` route/selection changes through root. Serialize overlapping edits.
- [ ] Run focused checks after each change and the package journey at closure. Run the integrated backend, frontend, browser, and artifact suite once at S05.
- [ ] Record exact evidence and observed KPI values in the ledger. Keep one decision trail, not another status document or spreadsheet with independent status.

## S01: Add a real column from schema-derived choices

Depends on: none.

User outcome. Search a recorded concept or ordinary field, add it, and see correct values without entering a FHIR path. A repeated structured value stays intact instead of silently becoming one scalar.

### UI01. Build the table column by column

Deliver under S01-03 and S01-04, using `BuilderWorkspace.tsx`, `ConceptCatalog.tsx`, `CatalogSelectionDialog.tsx`, `ColumnSelector.tsx`, and `PreviewTable.tsx` in the existing ExplorerBuilder feature.

- Let **New table** use the current project or attached collection, suggest a row resource, and offer other supported row resources through a plain selector. This initial resource-row choice must work before any graph traversal exists. S03 later adds grouping and expansion to it.
- Make **Add columns** search labels, codes, and ordinary fields across authorized catalog pages. Show the source label, code system, value type, and repeated-value status. Never require the graph for ordinary field discovery.
- Let **Add** apply a supported preserving default. Ask only for missing decisions reported by resolution. Distinguish loading, no matches, incomplete inventory, unsupported construction, and genuine unresolved meaning.
- Keep requested columns and their preview visible. Selecting a header opens **Column details** with name, output shape, source summary, and recorded examples. Rename, reorder, and remove operate on the existing stable column IDs.
- Render repeated records as expandable values with their owner/value/unit associations. Expose absence separately from zero, false, and an empty list. A truncated cell is visibly expandable, not silently shortened data.
- Close J01 by authoring three columns from an empty table, inspecting a repeated cell, renaming/reordering a column, and reloading. Exactly the chosen feature columns and their identities survive. No FHIR path entry or graph visit is needed for the unambiguous cases.

Ownership. Extend `internal/fhir/schema/index.go`, `internal/fhir/semantic/walk.go`, `internal/catalog/semantic.go`, `internal/server/explorer_capability.go`, `internal/explorer/capability/domain.go`, and `internal/dataframe/compiler/capability/probe.go`. Integrate through `internal/explorer/lifecycle/semantic_catalog.go`, `internal/explorer/authoringv2/semantic_command.go`, the existing recipe/semantic/IR/AQL path, OpenAPI, and the Builder catalog. These are existing owners, not a new parallel planner service.

- S01-01. Connect generated structure and owner-aware walking to production discovery. Retain typed members, choices, repeated scopes, requiredness, references, and available bindings. Replace name-suffix/resource dispatch with resolved datatype and versioned association metadata. Pin the installed schema and semantic artifacts. Preserve unknown structure with an explicit diagnostic. Do not claim full FHIR/profile support from the current subset.
- S01-02. Add a checked generic owner-record operation through recipe, semantic plan, physical IR, AQL execution, and contributor trace. Keep coding identity, value arm, unit members, absence, and source coordinates together. Extend compiler-backed capabilities to return construction options in the current row context, with output shape, row effects, preservation status, support, and reasons. Introduce the common row-definition contract here, initially supporting resource rows, so S03 extends the same model.
- S01-03. Extend existing catalog resolve/apply and CAS commands. Return safe defaults and genuine choices from the server. Generate the boundary contract and make the UI render it. Delete migrated client-side multiplicity guesses. Add direct and unambiguous related columns through the normal Builder entry, with a live preview and save/reload. Offer only executable output forms; unsupported lossless forms must not fall back to FIRST.
- S01-04. Exercise a new schema-defined resource containing familiar datatypes under unfamiliar names, repeated components, primitive metadata without a value, nested extension ancestry, and mixed value arms. Verify the same rules on open CDA-FHIR. Close the slice with J01 and delete superseded semantic writer/lowerer branches once every caller and draft migration uses the new operation.

- K-S01-a: changing only the test schema, generated metadata, and fixture introduces a previously unseen resource/path and its construction choices with zero resource-specific Go or TypeScript branches.
- K-S01-b: three selected concepts produce only their three requested outputs after Preview, reload, and export. Repeated owner/value/unit tuples and missingness match an independent expected fixture exactly. Unambiguous scalar additions require zero graph or path-entry steps.
- K-S01-c: all 1,000 fixture concepts are discoverable across pages without creating columns on open. Unauthorized entries/counts/examples are absent. Catalog and preview meet the shared latency budgets.

Verify. Run changed Go package/direct-importer tests, schema-resource guard, generated-contract checks, focused catalog UI tests, executed Arango owner-record/trace cases, and J01. Reuse the materialized inventory and retained raw data. Do not rescan the complete CDA corpus per click or metadata edit.

## S02: Choose relationships automatically or author them in the graph

Depends on: S01.

User outcome. Add related values without choosing a technical route when the construction is unambiguous. Open the graph to construct an exact traversal and select codes at any reached node.

### UI02. Inspect and customize the source

Deliver under S02-03 and S02-04, extending `GuidedGraphWorkspace.tsx`, `RouteExtensionPanel.tsx`, `authoring/routeActions.ts`, the catalog dialog, and the existing interpretation editor.

- Add **Inspect source** and **Edit in graph** to Column details. Show the selected column's route, direction, source node, and exact code/value owner. Keep all other columns unchanged.
- Let users start in **Advanced graph**, author a traversal, select any reached occurrence, and search its codes or fields. Apply writes the same column intent used by the catalog view.
- Preserve graph row-start changes, optional/required matches, branch extension/truncation, and relationship alternatives. Keep Viewer pagination, custom actions, and file links throughout the redesign.
- If several meaningful routes remain, show named alternatives with authorized source examples and row/multiplicity effects. Do not ask users to select an unexplained join path or silently choose the shortest path.
- For genuinely unresolved meaning, highlight candidate code and value members within their owner record. Preview a schema-valid pairing before applying it. Preserve explicit choices on editor switches and reload.
- Close J02 by adding a related column without the graph, then authoring a different five-edge route and node-local code in the graph. Cancel leaves the saved column unchanged; Apply and reload preserve the exact authored route and literal values.

Ownership. Extend `internal/fhir/schema/traversals.go`, compiler capability requests, lifecycle resolution, and authoring route handling. Update `GuidedGraphWorkspace.tsx`, `ConceptCatalog.tsx`, and `CatalogSelectionDialog.tsx`. Retire `catalogPaths.ts` as an authority for route validity and selection.

- S02-01. Generate directed relationship options from schema references plus executable storage mappings and authorized observed availability. Compose multiplicity across the route, including inbound fan-out. Enumerate suggestions lazily with explicit search limits and completeness. Never identify the shortest path with the intended meaning or treat a truncated search as no route.
- S02-02. Resolve an unambiguous route on the server and expose plain-language alternatives only when meanings differ. Preserve pinned explicit routes as overrides. Check the complete route and selected outputs through the compiler, not just isolated edges. Keep all operational limits visible.
- S02-03. Enable graph-first traversal and node-local code/value selection using the same construction choices and saved column intent. Permit schema-valid explicit owner/member selection for genuinely unresolved bindings through existing interpretation ownership. Preserve the full owner/extension context. Do not turn unsupported compiler behavior into a human mapping task.
- S02-04. Prove catalog-to-graph-to-catalog editing, a valid five-edge route, inbound relationships, multiple semantically distinct paths, and explicit route persistence through reload/export. Migrate both `CatalogSelectionDialog.tsx` and `PopulationPanel.tsx` off client-side route authority before retiring `catalogPaths.ts`. Remove duplicate source-shape interpretation.

- K-S02-a: an unambiguous related concept can be added without opening the graph; an ambiguous relationship receives distinct source examples rather than an arbitrary shortest-path result.
- K-S02-b: a manually authored five-edge route and its node-local code selection execute with literal expected values and exact contributors, survive reload, and remain unchanged when switching editors.
- K-S02-c: stale/unauthorized routes and cross-owner bindings are rejected without partial mutations. Route searches report truncation honestly and warm construction-choice requests meet the shared latency budget.

Verify. Run traversal/authoring/compiler tests and J02. Compare source tuples for automatic and explicit routes, rather than comparing only rendered labels. Keep cyclic route support distinct from bounded automatic suggestion search.

## S03: Define rows by records, groups, or expansion

Depends on: S01.

User outcome. Change a table from one row per selected record to one row per chosen group, or expand a repeated collection into rows. See the resulting membership and row count before applying.

### UI03. Design the rows

Deliver under S03-03 and S03-04 through `RowDefinitionPanel.tsx`, `PopulationPanel.tsx`, and `RowChangeRepairPanel.tsx`. Reuse project Explorer selection and the existing population commands. The current Population panel attaches saved selections; it is not a record browser. Keep selection attach/clear, coverage checks, and explicit unmapped-record handling while adding the picker through the existing selection owner.

- Add **Choose starting records** with searchable/filterable authorized records, selection scope, and a selection summary. Accept existing Explorer selections with their identities. Distinguish selected rows, all filtered matches, and the complete authorized population.
- Replace the resource-only Rows control with **Records**, **Groups**, and **Expand repeated values** modes. Suggest a default from the starting collection; never require Patient or DocumentReference.
- For groups, offer schema-resolved key fields or explicit member groups. For expansion, offer applicable repeated scopes. Explain missing keys, overlapping membership, and combinations of independent arrays before Apply.
- Show a before/after row preview, example memberships, count completeness, and affected columns. Apply and Cancel are explicit. Incompatible columns are explained and retained until the user resolves the change.
- Close J03 by selecting three fixture records, forming two groups, inspecting the memberships, canceling a different proposal, and applying expansion. Reload/export retain the intended rows; independent arrays do not multiply accidentally.

Ownership. Extend the existing `authoringv2` document and `row_change.go`, population selection/mapping lifecycle, recipe and semantic row plans, physical lowering, and `PopulationPanel.tsx`. Use the construction contract frozen in S01. Do not add a separate dataset-design store.

- S03-01. Model resource rows, grouped rows, and expanded rows as explicit variants in the existing saved document. Resolve grouping keys and repeated scopes against schema metadata. Support grouping by selected typed fields and explicitly selected member groups. Define null/missing keys, stable group identity, empty groups, overlapping membership, and within-group record identity.
- S03-02. Compile row construction and column contributors together. Preserve the distinction between population filters, group membership, and column-only filters. Keep two independent repeated collections separate unless the user explicitly requests combinations. Retain source/member identity and coordinates across grouping and expansion.
- S03-03. Generate applicable Rows controls from available typed fields and repeated scopes. Suggest the starting collection as the default without requiring Patient or DocumentReference. Add authorized source selection, group/expand preview, and explicit apply/cancel through the existing CAS lifecycle. Explain affected columns and reject incompatible changes without silently dropping them.
- S03-04. Verify grouping and expansion on at least two different schema-defined resource shapes. Round-trip selected source tuples through preserved grouped/expanded output. Test missing keys, overlapping groups, empty members, unlinked records, and repeated values with equal contents but different identities.

- K-S03-a: grouping three fixture records into two groups yields exactly the expected memberships; expanding the selected repeated scope yields the expected rows and can reconstruct the selected input tuples.
- K-S03-b: two independent collections of sizes two and three remain independent by default. No accidental six-row Cartesian product or index-zipping is permitted.
- K-S03-c: cancel and rejected changes leave the previous table unchanged; explicit apply, reload, and export retain the selected row definition and stable column identities. J03 passes for two resource shapes without resource-specific UI logic.

Verify. Run row-definition/migration, population, compiler, and literal membership tests plus J03. A grouped count alone is not proof that original members were preserved. A sampled preview count is labeled sampled.

## S04: Apply explicit typed transformations

Depends on: S02, S03.

User outcome. Customize selected columns and table shape through choices valid for their types, with immediate preview of values retained, excluded, or reduced.

### UI04. Customize column values and table shape

Deliver throughout S04-01 to S04-04 by extending `FeaturePolicyEditor.tsx`, `InterpretationPanel.tsx`, and the shared Column details inspector.

- Group settings into **Values**, **Time and units**, and **Table shape**. Populate each control from compiler-backed choices. Keep-all, checked-single, and aggregate outputs show their different meaning before Apply.
- Show before/after example values with contributors, excluded records, and declared information loss. Mark sample-based evidence as sampled. Changing units or time policy updates the same proposed transformation, not another hidden filter.
- Offer exact category recoding, explicit pivot categories and duplicate handling, and typed derived-column operands. Show transformation order, missing-value behavior, and stable output names. Do not require JSON, query strings, or arithmetic code entry.
- Give unsupported combinations a specific explanation. Preserve unsaved edits on recoverable errors; Cancel restores the saved definition. Draft and saved-result states must be distinct.
- Close J04 with actual control interactions for every enabled operator, including a time/unit reduction, a pivot, and a derived column. Check exact values and contributors, save/reload, and an unrelated column that must remain unchanged.

Ownership. Extend existing authoring column/policy types, compiler expression/reduction/temporal/unit logic, interpretation revisions, and `FeaturePolicyEditor.tsx`. The capability contract owns available operations. The UI must not maintain its own operation/type compatibility rules.

- S04-01. Derive supported operations from typed inputs and row context. Cover keep-all records, checked single value, count/exists, numeric min/max/mean/sum, and latest/earliest with explicit time fields and ties. Keep preservation, deduplication, filtering, and aggregation distinct in the construction result.
- S04-02. Expose existing temporal/unit policies and exact typed category recoding through generated choices. Normalize compatible coded units before numeric aggregation. Preserve original coded identity and recorded values. Unknown units/categories and absent timestamps need declared behavior, never coercion or full-dataset learned preprocessing.
- S04-03. Add explicit grouping-derived pivots and unpivots through the same row/output model. Freeze selected pivot categories and stable output keys. Require duplicate-cell handling and missing-cell policy. Provide schema-typed derived-column arithmetic with division/missingness rules, not arbitrary code or query-string execution.
- S04-04. Verify literal results and contributors for every offered operator. Check cancel/reload, changing one column without mutating unrelated columns, unit incompatibility, ordering ties, new pivot categories, and exact distinctions among zero, false, empty, missing, and recorded absence.

- K-S04-a: every enabled operation has a literal execution oracle and corresponding compiler capability; invalid type/operator combinations are unavailable with an accurate reason.
- K-S04-b: the time/unit fixture excludes out-of-window records, converts compatible units before aggregation, and produces exact expected results and contributors. Ties and unsupported units follow explicit policies.
- K-S04-c: pivot/unpivot and derived-column journeys preserve stable identities after reload/export; all deliberate reductions are labeled, and no operation silently changes population or another column.

Verify. Run focused operator and policy tests, executed query cases, and J04. Keep list-valued research output valid; do not label it a scalar training matrix without an explicit representation.

## S05: Publish and export the exact constructed dataframe

Depends on: S01, S02, S03, S04.

User outcome. Publish the authored table, reopen it in Viewer, and download an artifact that preserves its values, types, row membership, construction, and source evidence.

### UI05. Review and deliver the dataset

Deliver throughout S05-01 to S05-04 through `BuilderToolbar.tsx`, `DataframeContractPanel.tsx`, `Viewer.tsx`, `features/ExplorerViewer/components.tsx`, and `CellExplanationDialog.tsx`.

- Add a **Review dataset** summary before publication. Show the row definition, requested columns, result shapes, deliberate reductions, check completeness, and blocking issues with links to the responsible column or row setting.
- Keep **Publish** progress and failures visible. A failed or stale attempt must not look successful or replace the last active publication. Never label a preview sample as a full-population check.
- In Viewer, preserve column labels and structured cell rendering. **Explain this value** identifies exact contributing records and applied transformations using the existing evidence endpoint.
- Make **Download dataset** state the format, row scope, types, and whether values are preserved. For a shape that cannot fit scalar CSV, offer a supported typed/linked representation or return to an explicit reshape. Do not silently stringify records and call the result a scalar ML matrix.
- Close J05 through Publish, Viewer reload, cell inspection, filtering, and download. An independent artifact reader must reproduce the expected values and memberships. Record mechanical readiness without claiming clinical correctness or measured usability.

Ownership. Extend existing publication/check execution, `internal/dataframe/published/artifact.go`, output descriptors, Viewer, and `scripts/loom-dev.mjs`. Use the current immutable publication and artifact pipeline. Do not add a parallel full-population job framework.

- S05-01. Carry construction/row definitions, source generation, schema and interpretation versions, stable output keys, and reduction policies into the publication receipt and artifact descriptors. Migrate drafts explicitly and idempotently; retain immutable publication meaning.
- S05-02. Support the declared result shapes in typed export. Preserve record/list ownership, absent/null distinctions, large numbers, and coded identity. If flat scalar output cannot preserve a selected shape, require explicit reshape/reduction or export typed records/linked tables. A raw-source sidecar alone does not make a lossy table lossless.
- S05-03. Verify the complete authorized population through existing publication validation and expose incomplete/failed results honestly. Keep the prior active publication on failure. Reject stale generation, revoked scope, partial activation, and artifact mismatch. Report mechanical data issues without claiming clinical correctness or leakage-free ML.
- S05-04. Run all five browser journeys on an integrated checkpoint, plus the open CDA workflow, a language-level artifact round trip, full repository checks, and measured watcher/interaction performance. Record final remaining support limits and human usability as unmeasured unless separately studied.

- K-S05-a: Preview, published query, Viewer after reload, and downloaded artifact agree with the literal expected values, row/group membership, types, and source contributors for all declared shapes.
- K-S05-b: unauthorized/stale/failed operations expose no forbidden values, examples, counts, or artifacts and activate no partial publication. Existing publications remain readable with pinned meaning.
- K-S05-c: J01-J05 and the populated CDA workflow pass at one recorded checkpoint. Warm authoring loop median is at most 30 seconds over five measured edits; shared interaction/performance budgets pass without a fabricated comparison to unsupported baseline behavior.

Verify. Run the integrated commands in the acceptance protocol once after the combined work. Save DOM/screenshots, independent literal-output comparisons, artifact round-trip results, generation/scope identities, timings, and the final review verdict.

## Close the program

- [ ] All five ledger packages are accepted with their required evidence. No historical accepted checkbox substitutes for a new journey.
- [ ] Report implemented user capabilities, measured performance, limitations, and the exact branch/checkpoint. Stop short of claiming every conceivable dataframe or complete FHIR conformance.
- [ ] Obtain separate authority for publishing branches or landing changes. Preserve all populated source volumes and unrelated worktrees.

## Appendix A. Construction contract and ownership

Use the schema as the source of structural facts. Use versioned base-FHIR/profile/association metadata for relationships not encoded by JSON shape alone. Use observed data for availability and examples. Use compiler checks for executable support. Do not merge these into one guessed readiness flag.

A construction request names the current row/population context and a concept, field, or explicit graph occurrence. A response describes typed source members, owner/repetition axes, choice arms, route alternatives, output forms, applicable operations, required choices, row effects, and preservation/loss. Include the schema, semantic, dataset, authorization, compiler, and draft identities needed to reject stale decisions.

Keep recognition, data-quality warnings, compiler support, and user decisions separate. An absent profile is not automatically a mapping problem. Do not use Coding.display as identity, array position as cross-record identity, or a sample of one as proof of scalar multiplicity.

Persist user intent in the existing versioned workspace. Read-only resolution persists nothing. Apply resolves references again, validates scope and versions, allocates routes, and commits atomically with the existing command-ID/CAS semantics. Unknown network outcomes reuse the same command ID. Display-label edits do not rename physical keys.

Extend `internal/fhir/schema` for pure structural resolution and `internal/fhir/semantic` for owner-aware facts/associations. Keep `internal/catalog` responsible for authorized observed inventory. The existing capability adapter combines those inputs and compiler proofs. Extend the existing dataframe compiler for generic operators. The UI renders the generated contract; it is not another FHIR interpreter.

Do not eagerly flatten an infinite recursive schema or enumerate every possible graph walk. Expand choices lazily, retain recursion/reference identity, and report bounded suggestion searches separately from validity of an explicitly authored finite route. Cache structural facts by schema/semantic revision and data-dependent facts by generation, authorization, and relevant table context.

Lossless means the selected fields and records retain values, types, multiplicity, ownership, and source/member identities in the declared output representation. It does not promise byte-for-byte reconstruction of all input JSON. Scalar reduction, DISTINCT, filtering, and lossy recoding are explicit user intent. Define and test operator-specific reconstruction properties for preserving operations.

## Appendix B. Existing evidence and unresolved proof

Reuse the generated schema index and owner-aware walker accepted at baseline HEAD. They were not connected to production discovery when this plan was written. Existing schema resolvers, inventory storage, authorization, CAS, publication, and watchers are not being replaced wholesale.

The previous reference experiments demonstrated owner-local FHIR extraction, not the new Loom pipeline or UI. The inspected frontend route helper returned one four-hop route and no five-hop route on the same synthetic chain. Four focused metadata/walker/route-policy tests passed. These facts explain the replacement work; they do not close S01.

Grouping/expansion publication shape, generic record lowering with full trace, and automatic route-choice behavior remain unproven. Resolve each inside its owning package with executed fixtures. Do not open another architecture comparison unless a concrete blocker produces new evidence.

Go remains the implementation language for this plan. Another language requires a working comparison that improves the complete path, including deployment and iteration, not merely a preferred syntax.

## Appendix C. Retired scope and risks

All earlier F1-F4, B01-B08, C01-C12, and standalone F00 planning documents are [archived](history/20260919-superseded/README.md). The archive preserves exact bytes and completion evidence. Use it for history only.

Carry forward schema semantics, complete paged inventory, source selection, exact contributor tracing, time/unit choices, safe migrations, immutable artifacts, and authorization. Their new owners are S01-S05 above.

Defer a general terminology-package administration UI, a cross-project mapping marketplace/library, learned ML preprocessing, model training, a standalone asynchronous Check framework, and a redesigned copy/refresh workflow. Preserve existing behavior in these areas. These are not hidden tasks or prerequisites for this plan.

Risk controls include explicit unsupported metadata diagnostics, no implicit Cartesian joins, no silent FIRST, finite operator support, visible incomplete discovery, and bounded memory for large populations. Export format and ML scalar eligibility must be explicit. Document unsupported cases instead of inventing semantics.

## Appendix D. Read the relevant references

- Read [current Explorer authoring](../EXPLORER_AUTHORING.md), [compiler architecture](../EXPLORER_COMPILATION_ARCHITECTURE.md), and [the OpenAPI source](../../openapi/openapi.yaml) before changing their boundaries.
- Read [the local verification skill](../../.codex/skills/verify/SKILL.md) before browser or watcher work. Read the project FHIR modeling skill before semantic changes.
- Use first-principles redesign to replace duplicate source-specific decisions, Go patterns for implementation ownership, and TypeScript best practices for generated/parsed boundary variants. Use executable checks and one coherent final review, not repeated audit panels.
