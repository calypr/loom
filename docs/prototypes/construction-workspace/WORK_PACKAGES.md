# Construction workspace work packages

Implementation planning draft, 2026-09-24. These packages implement the [accepted interaction decisions](DESIGN.md#accepted-interaction-decisions) and [submenu specification](SUBMENUS.md). The [gap analysis](GAP_ANALYSIS.md) records evidence at `arch/integration`, commit `ba882b003289024c3c1c8eb0fbc0d2611fb73eeb`.

## Product contract

The researcher selects a starting table or record type from data already loaded through Loom's API. The workspace shows what one row represents, the current table, its construction steps, and five action families. Each action opens a guided editor with valid choices supplied by the backend.

Every operation is editable and removable. An earlier edit that breaks later steps remains a proposal until those steps are repaired or explicitly removed. A valid edit automatically previews after a short pause, cancelling superseded requests. Apply requires the latest successful preview for the current proposal. Cancel retains the accepted construction.

Combine retains the version of the other table that was selected. Later edits to that table do not propagate automatically. Update input previews an explicit version change. This is the user's clarified decision and replaces the earlier live-link proposal.

Calculations support guided controls and an optional formula editor for the same expression. The default adds a new column; replacement is explicit. Discovery shows meaning, example values, and available coverage. New pivot categories require review before changing the output columns.

The finish action saves the configuration, computes the dataframe, and publishes it to ClickHouse for fast querying. Preview and published data must derive from the same saved construction and source context.

## Package map

| Package | Deliverable | Dependencies | Main uncertainty |
| --- | --- | --- | --- |
| P01 | Persistent operations and intermediate-result compilation | Existing source/compiler baseline | Extending the fixed physical plan layout without duplicating execution logic. |
| P02 | Workspace shell, history, and shared editor lifecycle | P01 contract; existing source and shape services | Reusing existing controllers while preserving proposal identity and user input. |
| P03 | Discovery and Add columns | P01 stage context, P02 | Relating source information to rows after transformations, with honest coverage evidence. |
| P04 | Keep rows and Calculate | P01 stage input, P02 | Exposing existing expressions and adding any absent ordered-row primitives. |
| P05 | Reshape | P01 stage input, P02 | General grouping over derived columns and repeated shape operations. |
| P06 | Combine with versioned table inputs | P01, P02 | Table-result joins and append are not present in the current authoring contract. |
| P07 | Save, publish to ClickHouse, reopen, and inspect | P01–P06 for included operation families | Preserving construction, source, schema, and evidence identity through publication. |
| P08 | Preview performance baseline, hillclimb, and regression checks | Baseline can start immediately; extend with P01–P07 | Current end-to-end latency has not been measured. |

P08 starts at the beginning. Each editor package extends its frozen workload and must report its latency. A separate package makes this work visible; it does not defer performance until the interface is finished.

The packages are ownership and verification boundaries. They are not estimates of equal effort or a requirement to use multiple agents. One implementation instance can complete them in dependency order.

## P01. Persistent operations and intermediate-result compilation

**Outcome.** Any supported operation can consume the result of an earlier operation. A construction can be saved, reopened, edited, and restored without inferring its meaning from a command log.

**Existing code.** `internal/explorer/authoringv2`, `internal/explorer/lifecycle`, `internal/explorer/arango/drafts.go`, `internal/dataframe/recipe`, `internal/dataframe/semantic`, and `internal/dataframe/compiler/{ir,lower,render/aql}`.

**Required changes.**

- Define durable step identities, typed operation parameters, input references, and stable output-column references. A rename changes a label, not a reference.
- Distinguish a source projection, an earlier step result, and a selected immutable table-input version. Preserve source-generation and authorization context throughout compilation.
- Represent the supported sequence explicitly. Existing fixed slots in `Document`, `recipe.Output`, and `semantic.OutputPlan` do not express arbitrary placement and repetition.
- Extend physical stage boundaries, scopes, and rendering to consume typed output rows. Current validation allows one table reshape and one root scan in an ordinary plan. Removing those checks alone is insufficient; renderer layout, row identity, bounds, and provenance assume that structure too.
- Reuse existing expressions, source traversals, reductions, pivot/unpivot logic, and AQL execution. The backend remains the single evaluator.
- Persist proposed and accepted constructions with optimistic version checks. Reuse existing draft/revision storage where its lifecycle fits. A transient command retry ID does not identify an analytical step.
- Derive downstream dependencies from the typed construction, including row meaning and ordering. Edit/remove produces a proposed surviving construction and explicit repair requirements. Undo restores a recorded accepted revision.
- Define how existing saved configurations become an equivalent initial construction. Preserve their established results and source semantics.

**Contract handed to other packages.** A selected stage has stable identity, input versions, schema, row meaning, and capability context. A proposal identifies the exact base revision, saved operation meaning, resulting stage descriptors, dependency issues, and matching preview status. Application commits that proposal atomically.

**Completion checks.** Save and reload a repeated transformation chain without losing choices. Edit an early operation and recompute its consumers. Remove an operation with a dependent and an unrelated later operation; require deliberate handling of the dependent and retain the unrelated operation. Undo restores the previous construction. Stale proposals cannot apply. The same compiler path produces preview and final output.

**First implementation checkpoint.** Produce one executable compiler demonstration of pivot → derive → filter → unpivot using typed intermediate columns and final-output preview bounds. It is a compiler contract check, not a restriction of the product to that example. This checkpoint establishes the necessary scope/renderer extension before the rest of the operator adapters are built.

## P02. Workspace shell and editing lifecycle

**Outcome.** The accepted prototype becomes a production workspace with a table navigation area, visible actions, selected-column shortcuts, an editor beside the result, and usable construction history.

**Existing code.** `ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx`, its `authoring` state, `components/PreviewTable.tsx`, `ColumnSelector.tsx`, and `TableShapeSettingsPanel.tsx`.

**Required changes.**

- Start from available record types/tables and state row meaning beside the result.
- Route toolbar actions and column shortcuts to the same editor. Preserve selection, scroll, focus, and entered values during requests.
- Reuse existing shape controller behavior for context identity, saved-intent reconstruction, recoverable failure, and stale-response rejection.
- Show current and proposed results distinctly. Apply is enabled only for a successful preview matching the current parameters and base revision.
- Trigger preview automatically after a short pause for a valid edit. Cancel superseded requests and discard late responses. Incomplete forms refine their available choices without executing a row preview.
- Render saved step summaries with Edit, Remove, and Create table from here. Open the exact historical inputs when editing. Copying a construction creates an independent branch.
- Present dependency repair within the proposed change. Keep the accepted table available while the user repairs or explicitly removes affected steps.
- Keep loading, unsupported, incomplete evidence, and request failure distinct. The frontend never invents supported operations from field-name heuristics.

**Completion checks.** Drive these interactions in the browser against the real backend, including late responses, preview failure, Cancel, edit/remove, Undo, and reload. Every applied step reopens with the same meaning. The table and editor remain usable at narrow widths and through keyboard navigation.

## P03. Discovery and Add columns

**Outcome.** A researcher can find what data exists and choose what information each current row should receive.

**Existing code.** Construction choices, semantic inventory, source inspection, contributor policies, and the source catalog. Existing client calls are mapped in `GAP_ANALYSIS.md`.

**Required changes.**

- Place search, browse, field meaning, observed codes, examples, units, and coverage in the Add columns panel.
- Show applicable relationship and output-form choices from the backend. Distinguish a direct value, a count, a flag, a list, a reduction, and an ordered representative where supported.
- Preserve explicit contributor predicates, time windows, multiplicity policies, and missing-value behavior.
- Address the selected intermediate stage. Related-source choices require retained semantic evidence about its rows; matching column labels alone cannot establish a relationship after reshaping.
- Label every count by scope and denominator. Separate source-record frequency from coverage of current output rows. Evidence can load independently from structural choices.
- Reopen a saved addition with the exact source, relationship, contributor scope, policies, and output identity.

**Completion checks.** Find an observed code, inspect its evidence, select a valid construction, preview the added output, apply, and reopen. Repeat after a transformation that preserves a meaningful relationship to the source. If no supported relationship remains, explain the reason before proposing a construction.

## P04. Keep rows and Calculate

**Outcome.** Typed condition and expression editors operate on both source and derived columns.

**Existing code.** Resource-relative recipe filters, `internal/dataframe/expression`, recipe expressions, derived arithmetic, column transformations, and contributor predicates/windows. These are reuse candidates with different current scopes.

**Required changes.**

- Build nested All/Any/None conditions, typed comparisons, missing-value conditions, and related-record predicates with explicit record scope.
- Add stage-level filtering after transformations. Resource-field filtering already exists; filtering a derived output requires the new stage binding.
- Expose existing typed functions through guided controls and a formula editor sharing one canonical expression representation. Switching views preserves the expression exactly.
- Add condition/result rules, recoding, missing-value handling, and explicit replacement. Default to a new column.
- Inventory supported function signatures and exception policies. Add missing rank/lag/running/window behavior only with explicit partition, ordering, frame, and tie semantics.
- Support duplicate removal and ranked-row selection with explicit identity and survivor/tie policies. Do not confuse display sorting with analytical ordering.

**Completion checks.** Filter on a derived value; construct nested conditions and a conditional expression; switch between guided and formula views; recode with an unmapped-value policy; reopen and edit. Compare outputs and missing semantics with deterministic expected results. Each newly added ordered-row primitive needs executable coverage of partitions, boundaries, and ties.

## P05. Reshape

**Outcome.** Group, pivot, unpivot, and expansion work at the chosen point in a construction, with explicit output meaning.

**Existing code.** Table-shape capability/discovery/resolution/proposal services, `TableShapeEditor`, `GroupedPivotEditor`, pivot/unpivot lowering, and row expansion. Existing `GroupRows` reads immutable explicit groups; it is not a general GROUP BY over arbitrary derived columns.

**Required changes.**

- Integrate existing shape controls into the accepted panel and preserve saved categories, authored output names, and all policies.
- Support grouping by stage columns with repeatable summaries. Separate record counts, populated-value counts, and distinct-value counts.
- Adapt pivot and unpivot to intermediate-stage schemas and allow later transformations to consume their outputs.
- Expose supported repeated-value expansion, including empty-list and position behavior. Multiple expanded fields require explicit pairing or combination semantics.
- Keep pivot output identities stable. A data refresh discovers new categories and proposes a change; it does not silently grow the schema.

**Completion checks.** Repeated reshape and calculation produce correct rows. Duplicate/missing/unlisted category policies survive reload. Category discovery remains bound to the selected source pair. A refreshed source with new categories preserves the accepted columns until the user accepts their addition. Preview limits apply at the correct output stage.

## P06. Combine with versioned inputs

**Outcome.** Constructed tables can be matched, appended, and compared without implicit updates between independent constructions.

**Existing code.** Source relationship traversal, typed predicates, output contracts, persisted revisions, and materialization identity. These do not currently provide a general constructed-output join or append contract.

**Required changes.**

- Select another table and an immutable input version. Record enough identity to reproduce that input and retain it while referenced.
- Add matching-columns, append, membership, and supported combination operations. Expose compatible key types and match meanings before proposal.
- Specify unmatched-row treatment, multiple-match behavior, output naming, type alignment, duplicate handling, and row multiplication.
- Lower table-result operations through the common compiler/execution path. A lexical recipe `DocumentRef` is not an authored-table input reference.
- Implement Update input as a proposed change with schema and row effects. Changes to the other table's current configuration leave the consumer unchanged.
- Define explicit lineage for matched, unmatched, appended, and summarized rows so later evidence remains meaningful.

**Completion checks.** Combine two independently constructed inputs. Verify missing/duplicate matches and append alignment. Save, reload, edit one source construction, and confirm the consumer retains its selected version. Update that input explicitly and require a successful preview before Apply. Referenced versions survive removal of a table from navigation.

## P07. Save, ClickHouse publication, and evidence

**Outcome.** The saved configuration produces a queryable ClickHouse dataframe whose contents and meaning agree with the reviewed construction.

**Existing code.** Explorer draft/revision persistence, compilation receipts, `internal/explorer/lifecycle/publish.go`, publication/materialization services, `ui/packages/loom-ui/src/api.ts` publication and query calls, population mapping, and cell trace.

**Required changes.**

- Make save/configuration status and Publish to ClickHouse visible in the workspace. Show build progress, success, and recoverable failure through the existing lifecycle.
- Carry step and input-version identity into the canonical recipe, receipt, public output contract, and published revision.
- Reopen the full construction after publication. Inspect a result's relevant source contributors and transformation meaning.
- Extend stage profiles and cell evidence where existing source/shape-specific diagnostics cannot explain composed output. State evidence limits rather than inventing complete lineage.
- Preserve materialization identity across query pages. A failed replacement build must not masquerade as a successful new published dataset.

**Completion checks.** Build a construction in the browser, preview, save, publish to the local ClickHouse-backed stack, query actual values, reload, and inspect evidence. Verify the same config/input versions produced the schema and values. Repeat after an explicit input update. Include a publication failure and an export/query continuity check.

## P08. Preview performance

**Outcome.** Routine edits produce a correct preview fast enough to support repeated exploration. The performance result is measured on the real interaction path.

**Proposed targets.** Local selection/menu feedback within about 100 ms. Routine warm edit-to-render preview latency under one second at the 95th percentile on a declared workload and environment. Report cold and expensive transformations separately. The target is proposed; current backend performance has not been measured.

**Measurement contract.**

- Start the clock at the user action that requests the changed preview. Include intentional debounce, capability refinements, resolution, compilation, query, serialization, transport, and grid rendering.
- Stop when correct rows for the newest proposal are visibly rendered and eligible for Apply. A spinner, cached old table, or schema-only response is not completion.
- Freeze workload identity, source snapshot, row counts, cardinality, width, construction depth, match multiplicity, concurrency, environment, and cold/warm cache state.
- Cover small parameter changes within filtering, calculations, pivot reduction, source additions, and Combine. Include edits to earlier steps and high-cardinality category discovery. These structural workloads test generality without defining the product's domain.
- Record distributions and variance, including median and tail latency, server stage timings, query work, cache reuse, failures, and cancelled requests. Prove the harness distinguishes a small edit from an expensive recomputation.

**Optimization loop.** Establish a baseline first. Change one measured mechanism at a time, rerun the frozen workload and correctness checks, and retain only demonstrated improvements. Log the hypothesis, change, before/after measurements, and verdict. Candidate mechanisms include scoped compilation reuse, retained unchanged inputs, request coalescing/cancellation, bounded preview work, cached structural choices, decoupled profile scans, and measured query/index changes.

**Correctness constraints.** Cache keys include authorization, source generation, input versions, selected stage, and parameters. Cancellation or stale responses cannot alter the accepted result. Sampling must retain the declared meaning; truncating source rows before grouping or pivoting can change values. Expensive complete counts and distributions can run separately when their scope and completeness are labeled.

**Completion checks.** Demonstrate repeatable gains beyond measurement noise against the declared target workload. Keep calculation, cardinality, authorization, schema, and version-consistency checks green. Publish the baseline and final distributions plus the retained changes. Install a regression check with a documented environment and noise tolerance. If the target remains unmet, report the measured bottleneck and remaining gap explicitly.

## Handoff and sequencing

Begin P08 baseline measurement on today's preview path while P01 settles its stage contract. P02 can establish the accepted shell using existing controls and the agreed contract. Implement P03–P06 as complete editor-to-backend units, preserving a single evaluator. P07 closes the saved-config-to-ClickHouse workflow. P08 continues through each unit.

Each package handoff includes the changed contract, actual files, focused executable checks, browser evidence where relevant, and performance impact. Cross-cutting OpenAPI changes originate in `openapi/openapi.yaml`; generated Go and client contracts must stay synchronized.

This plan establishes deliverables and checks. It does not reinstate the withdrawn agent-week estimate. The first compiler-composition checkpoint and the preview baseline provide concrete evidence for sizing the remaining work.
