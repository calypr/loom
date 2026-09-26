# Construction workspace work packages

Implementation planning draft, 2026-09-25. [F0: Frame sparse ArangoDB records as a model table](SPARSE_RECORD_FRAMING_WP.md) is the central product package. P01–P09 support its end-to-end acceptance. The [frontend feature ledger](FRONTEND_FEATURE_LEDGER.md) is the delivery and QA order; package implementation alone is not a user-visible completion claim. The [framing contract](FRAMING_CONTRACT.md) sets scope, the [interaction design](DESIGN.md#accepted-interaction-decisions) records accepted UI behavior, and the [full plan audit](PLAN_AUDIT.md) records the failure cases added to each package. The [original gap analysis](GAP_ANALYSIS.md) records source evidence at `arch/integration`, commit `ba882b003289024c3c1c8eb0fbc0d2611fb73eeb`.

## Product contract

The researcher selects a starting table or record type from data already loaded through Loom's API. The workspace shows what one row represents, the current table, its construction steps, and actions for defining rows, adding columns, arranging records, reviewing the frame, and publishing. Each construction action opens a guided editor with valid choices supplied by the backend.

Every action family, including Combine, consumes a table result and produces another table result. A construction may start from loaded records or an exact published table artifact. Actions may be repeated and interleaved: a joined result can be filtered, grouped, joined again, and published through the same proposal and preview lifecycle. An implementation that permits Combine only as a terminal or standalone operation does not satisfy this contract.

Every operation is editable and removable. An earlier edit that breaks later steps remains a proposal until those steps are repaired or explicitly removed. A valid edit automatically previews after a short pause, cancelling superseded requests. Apply requires the latest successful preview for the current proposal. Cancel retains the accepted construction.

Combine retains the exact published artifact of the other table that was selected, including source generation and materialization. Later edits or republishing that table do not propagate automatically. Update input previews an explicit artifact change. This is the user's clarified pinned-input decision and replaces the earlier live-link proposal.

Discovery shows meaning, example values, and available coverage. New pivot categories require review before changing the output columns. Calculate, formula authoring, recoding, and other derived-column operations are outside the builder plan. Counts and reductions of matching source records remain in scope because they determine how those records appear in the dataframe.

The finish action saves the configuration, computes the dataframe, and publishes it to ClickHouse for fast querying. Preview and published data must derive from the same saved construction and source context.

## Package map

F0 defines the operations that turn sparse related records into output rows and columns. It owns the integrated user outcome. The following packages supply the contracts and implementation F0 needs; completing one supporting package does not establish that a usable frame can be built.

| Package | Deliverable | Dependencies | Main uncertainty |
| --- | --- | --- | --- |
| F0 | End-to-end sparse record framing through AQL and ClickHouse | P01–P09 capabilities as needed | Composing row identity, traversal, contributor selection, one-to-many policies, and honest sparsity evidence in one editable flow. |
| P01 | Persistent, composable table operations and intermediate-result compilation | Existing source/compiler baseline | Extending the fixed physical plan layout across operation families without duplicating execution logic. |
| P02 | Workspace shell, history, and shared editor lifecycle | P01 contract; existing source and shape services | Reusing existing controllers while preserving proposal identity and user input. |
| P03 | Discover and add columns | P01 stage context, P02 | Population-scoped path and code discovery, source semantics, time-field roles, and honest output-row coverage. |
| P04 | Population and contributor selection | P01 stage input, P02 | Scoped conditions and explicit treatment of matching, absent, and duplicate records. |
| P05 | Row and column arrangement | P01 stage input, P02 | General grouping, repeated shape operations, and stable wide/long outputs. |
| P06 | Versioned table inputs and cross-engine execution | P01, P02 | Exact published-table references and the AQL-to-ClickHouse boundary needed by table-result joins and append. |
| P07 | Save, publish to ClickHouse, reopen, and inspect | P01–P06 for included operation families | Preserving construction, source, schema, data dictionary, and evidence identity through publication and refresh; P09 defines frame evidence. |
| P08 | Preview performance baseline, hillclimb, and regression checks | Baseline can start immediately; extend with P01–P07 | Current end-to-end latency has not been measured. |
| P09 | Dataframe evidence | P01 stage context; P03, P04, P07 evidence | Showing row identity, population-scoped coverage, source multiplicity, observability limits, and missingness. |

P08 starts at the beginning. Each editor package extends its frozen workload and must report its latency. A separate package makes this work visible; it does not defer performance until the interface is finished.

The packages are ownership and verification boundaries. They are not estimates of equal effort or a requirement to use multiple agents. One implementation instance can complete them in dependency order.

An operator implementation does not close a package merely because it executes. Each package must also satisfy the [framing contract](FRAMING_CONTRACT.md). F0 closes only when the [three construction checks](SPARSE_RECORD_FRAMING_WP.md#completion-checks) pass in the real browser and published result. The primary workspace follows the decisions to define rows, add columns, arrange records, review the frame, and publish. Calculate is not a builder action.

## P01. Persistent, composable table operations and intermediate-result compilation

**Outcome.** Any supported operation can consume the result of an earlier operation. A construction can be saved, reopened, edited, and restored without inferring its meaning from a command log.

**Existing code.** `internal/explorer/authoringv2`, `internal/explorer/lifecycle`, `internal/explorer/arango/drafts.go`, `internal/dataframe/recipe`, `internal/dataframe/semantic`, and `internal/dataframe/compiler/{ir,lower,render/aql}`.

**Required changes.**

- Define durable step identities, typed operation parameters, input references, and stable output-column references. A rename changes a label, not a reference.
- Carry row anchors, repeated-element owner identity, contributor identity, semantic identity, time-role assertion, and authorization scope in stage descriptors and receipts. Later source additions and evidence must not reconstruct these from display names.
- Distinguish a source projection, an earlier step result, and a selected immutable table-input publication. Pin its construction revision, source generation, and materialization identity. Preserve authorization context throughout compilation.
- Give every action family the same stage input/output, proposal, preview, edit, remove, and reload contract. Combine can be a first, intermediate, or final step; a later step can consume its result. Engine-specific lowering must not impose a product-level terminal-step restriction.
- Represent the supported sequence explicitly. Existing fixed slots in `Document`, `recipe.Output`, and `semantic.OutputPlan` do not express arbitrary placement and repetition.
- Extend physical stage boundaries, scopes, and rendering to consume typed output rows. Current validation allows one table reshape and one root scan in an ordinary plan. Removing those checks alone is insufficient; renderer layout, row identity, bounds, and provenance assume that structure too.
- Reuse existing expressions, source traversals, reductions, pivot/unpivot logic, and AQL execution. The backend remains the single evaluator.
- Persist proposed and accepted constructions with optimistic version checks. Reuse existing draft/revision storage where its lifecycle fits. A transient command retry ID does not identify an analytical step.
- Derive downstream dependencies from the typed construction, including row meaning and ordering. Edit/remove produces a proposed surviving construction and explicit repair requirements. Undo restores a recorded accepted revision.
- Define how existing saved configurations become an equivalent initial construction. Preserve their established results and source semantics.

**Contract handed to other packages.** A selected stage has stable identity, exact input artifacts, schema, row meaning, and capability context. A proposal identifies the exact base revision, saved operation meaning, resulting stage descriptors, dependency issues, and matching preview status. Application commits that proposal atomically.

**Completion checks.** Save and reload a repeated transformation chain without losing choices. Edit an early operation and recompute its consumers. Remove an operation with a dependent and an unrelated later operation; require deliberate handling of the dependent and retain the unrelated operation. Undo restores the previous construction. Stale proposals cannot apply. The same compiler path produces preview and final output.

**First implementation checkpoint.** Produce one executable compiler demonstration of expand → filter → group → pivot using typed intermediate columns and final-output preview bounds. It is a compiler contract check, not a restriction of the product to that example. This checkpoint establishes the necessary scope/renderer extension before the rest of the operator adapters are built.

## P02. Workspace shell and editing lifecycle

**Outcome.** The accepted table-first prototype becomes a production framing workspace with named tables, visible row meaning, an editor beside the result, and usable construction history. The primary navigation follows row, column, and source-record decisions.

**Existing code.** `ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx`, its `authoring` state, `components/PreviewTable.tsx`, `ColumnSelector.tsx`, and `TableShapeSettingsPanel.tsx`.

**Required changes.**

- Start from available record types/tables and state row meaning beside the result.
- Keep defining rows, adding columns, arranging records, reviewing the frame, and publication visible as one workflow. Do not mark a generic operation menu as completion of this outcome.
- Route toolbar actions and column shortcuts to the same editor. Preserve selection, scroll, focus, and entered values during requests.
- Reuse existing shape controller behavior for context identity, saved-intent reconstruction, recoverable failure, and stale-response rejection.
- Show current and proposed results distinctly. Apply is enabled only for a successful preview matching the current parameters and base revision.
- Trigger preview automatically after a short pause for a valid edit. Cancel superseded requests and discard late responses. Incomplete forms refine their available choices without executing a row preview.
- Render saved step summaries with Edit, Remove, and Create table from here. Open the exact historical inputs when editing. Copying a construction creates an independent branch.
- Present dependency repair within the proposed change. Keep the accepted table available while the user repairs or explicitly removes affected steps.
- Keep loading, unsupported, incomplete evidence, and request failure distinct. The frontend never invents supported operations from field-name heuristics.

**Completion checks.** Drive these interactions in the browser against the real backend, including late responses, preview failure, Cancel, edit/remove, Undo, and reload. Assert that Apply is disabled before preview, while the newest preview is pending, after failure, and for a stale response; enable it only for the successful preview of the exact current proposal. Every applied step reopens with the same meaning. The table and editor remain usable at narrow widths and through keyboard navigation. Run a task study with five domain researchers who do not write SQL: at least four must independently find an observed code, add a sparse related column, explain its coverage denominator and missing cases, and edit or remove the step without hints or query text. Record where the others stop and revise the editor before closing P02.

## P03. Discover and add columns

**Outcome.** A researcher can find what data exists, relate it to the current rows, and represent sparse or repeated source records in the dataframe.

**Existing code.** Construction choices, semantic inventory, source inspection, contributor policies, and the source catalog. Existing client calls are mapped in `GAP_ANALYSIS.md`.

**Required changes.**

- Place search, browse, field meaning, observed codes, examples, units, and population-scoped coverage in the Add columns panel. Show source-wide frequency separately.
- Let the researcher select one observed code or an explicit set across paged search results. Save the exact code-system, version, and code identities, show each member's output-row coverage, and preview the combined source-to-column rule and resulting schema. A later discovery refresh cannot silently add set members.
- Preserve code system and version, value type, unit identity, and approved mapping or unit-policy version. Refuse incompatible values in one reduced or pivoted column rather than combining them by display label.
- Discover available source time fields and their declared roles. A window uses an explicitly selected field; an “as of” claim requires an availability-time field or equivalent declared evidence.
- Version the source-catalog assertions used for path ownership, code and unit identity, time role and precision, and expected coverage. Show provenance or unavailable status for each assertion. Exact instant windows must reject unsupported partial dates before preview or offer a saved interval/exclusion policy.
- Apply effective resource security-label policy to inventory, observed-code counts, examples, and candidate coverage before returning them. An unknown label does not inherit access merely from its project or path.
- Show applicable relationship and output-form choices from the backend. Distinguish a direct value, a count, a flag, a list, a reduction, and an ordered representative where supported.
- Preserve explicit contributor predicates, time windows, multiplicity policies, and missing-value behavior.
- Preserve Quantity comparator meaning. Offer exact numeric reductions only for point values or with an approved interval-aware policy. Keep a missing row-time anchor in the output with no eligible windowed contributors by default, and report it separately from an out-of-window record.
- Distinguish a missing graph target from no reference and from an unauthorized target. Report a dangling reference without exposing a target hidden by authorization.
- Address the selected intermediate stage. Related-source choices require retained semantic evidence about its rows; matching column labels alone cannot establish a relationship after reshaping.
- Author adding a related source as a typed operation at the selected stage. Preserve its row anchor, source path, contributor identity, repeated-element owner, and output identities through later stages; explain when the anchor is no longer valid.
- Label every count by scope and denominator. Separate source-record frequency from coverage of current output rows. Evidence can load independently from structural choices.
- Reopen a saved addition with the exact source, relationship, contributor scope, policies, and output identity.
- Make time windows, source absence, recorded nulls, and output-row coverage explicit for each added signal where applicable. Offer presence, count, reduction, representative value, or repeated output according to backend support.

**Completion checks.** Close all of these against the real backend:

- Select an explicit multi-code set across two search pages. Inspect each member's output-row coverage and code-system identity, preview the resulting columns, apply, and reopen the exact set. A discovery refresh cannot silently add members.
- Distinguish no source record, recorded null, zero, an out-of-window record, a missing row-time anchor, and a dangling reference. The missing anchor keeps its row; one invalid anchor cannot fail the whole preview. Two graph paths to one resource cannot inflate distinct-source counts.
- Bind a code condition and projected value to the same repeated element. Verify minimum, maximum, sum, and mean over compatible Quantity point values. Refuse incompatible-unit or comparator-bearing point reductions and a same-label, different-system merge.
- Preserve several matching values as an ordered list through preview, publication, and reload. Verify every value and the list's null policy.
- Test a declared availability field that permits an “as of” claim, an event or resource-update field that does not, and a partial date whose unsupported instant window is refused before preview or uses an explicit saved policy.
- Add another related source after group and expansion when the row anchor survives. When no supported relationship remains, show the reason before proposal.

## P04. Population and contributor selection

**Outcome.** Researchers can define which rows belong in the dataframe and which source records contribute to its columns. Filtering, related-record conditions, duplicate handling, and representative-record selection serve those decisions.

**Existing code.** Resource-relative recipe filters, typed predicates, contributor predicates and time windows, and row-selection policies. Existing expression and arithmetic code is not a builder deliverable.

**Required changes.**

- Build nested All/Any/None conditions, typed comparisons, missing-value conditions, and related-record predicates with explicit record scope.
- Add stage-level filtering after framing operations. Resource-field filtering already exists; filtering an intermediate result requires the new stage binding.
- Keep the scope of each condition explicit: current output rows or source records contributing to a selected column.
- State how absence and recorded null values affect each condition and whether the condition changes rows or contributors.
- Support duplicate removal and ranked-row selection with explicit identity and survivor/tie policies. Do not confuse display sorting with analytical ordering.

**Completion checks.** Distinguish excluding an output row from filtering source records that contribute to one column. Construct nested conditions and related-record predicates; reopen and edit them. Compare row counts, contributor counts, and missing semantics with deterministic expected results. Ranked-row selection needs executable coverage of partitions, boundaries, and ties.

## P05. Row and column arrangement

**Outcome.** Group, pivot, unpivot, and expansion work at the chosen point in a construction so related records can become wide columns, long rows, or repeated values with explicit output meaning.

**Existing code.** Table-shape capability/discovery/resolution/proposal services, `TableShapeEditor`, `GroupedPivotEditor`, pivot/unpivot lowering, and row expansion. Existing `GroupRows` reads immutable explicit groups; it is not a general GROUP BY over current-stage columns.

**Required changes.**

- Integrate existing shape controls into the accepted panel and preserve saved categories, authored output names, and all policies.
- Support grouping by stage columns with repeatable summaries. Separate record counts, populated-value counts, and distinct-value counts.
- Save an explicit policy for absent and null group keys. The default may make a named missing-key group only when the user sees that it joins those rows; dropping them requires an explicit choice.
- Adapt pivot and unpivot to intermediate-stage schemas and allow later transformations to consume their outputs.
- Expose both repeated-value expansion and related-record path expansion. A related-record expansion can lower from a route or a retained source-record list, but must preserve the parent and related-record identities, path, contributor policy, and empty-match rule. Repeated lists require empty-list and position behavior; multiple expanded fields require explicit pairing semantics.
- Keep pivot output identities stable. A data refresh discovers new categories and proposes a change; it does not silently grow the schema.

**Completion checks.** Repeated reshape operations produce correct rows. Expand a related-record path into rows, retain its parent identity, verify an empty-match rule, and add another related column from the expanded stage. Separately expand a repeated value list with position behavior. Group several rows with absent or null keys and one row with a present key; verify the saved missing-key policy, group counts, and reload behavior. Duplicate/missing/unlisted category policies survive reload. Category discovery remains bound to the selected source pair. A refreshed source with new categories preserves the accepted columns until the user accepts their addition. Preview limits apply at the correct output stage.

## P06. Versioned table inputs and cross-engine execution

**Outcome.** A constructed table can use an exact published artifact of another constructed table as an input at any point in its operation sequence. Match, append, and membership produce ordinary intermediate results that later operations can consume.

**Existing code.** Source relationship traversal, typed predicates, output contracts, persisted revisions, and materialization identity. These do not currently provide a general constructed-output join or append contract.

**Required changes.**

- Select another table and an immutable published input. Record its table, construction revision, source generation, output, and materialization identity; retain that artifact while referenced. Republishing the same construction against a new source generation creates a different selectable input.
- Add matching-columns, append, and membership operations. Expose compatible key types and match meanings before proposal. Membership keeps or excludes rows according to an exact input artifact and does not invent a Cartesian pairing mode.
- Specify unmatched-row treatment, multiple-match behavior, output naming, type alignment, semantic identity alignment, duplicate handling, and row multiplication. An append or coalesced output cannot merge incompatible code systems, value types, or units without an approved versioned mapping.
- Lower table-result operations through the common compiler/execution path. A lexical recipe `DocumentRef` is not an authored-table input reference.
- Cross an AQL-to-ClickHouse boundary with a typed, scoped, private intermediate result when required. Preserve the same row, column, authorization, and lineage identities across the boundary. Do not expose a terminal-only Combine as completion of this package.
- Implement Update input as a proposed change with schema and row effects. Changes to the other table's current configuration leave the consumer unchanged.
- Define explicit lineage for matched, unmatched, appended, and summarized rows so later evidence remains meaningful.

**Completion checks.** Frame FHIR rows in AQL, group or expand them, Combine that intermediate result with an exact published ClickHouse input, then filter or reshape the result and Combine it again. Publish and reopen this chain; verify row identity, authorization scope, source contributors, missing/duplicate matches, and append alignment against actual values. Use a pinned cohort table to keep matching rows and to exclude matching rows in separate proposals; verify the resulting populations. Refuse an append that aligns same-named but semantically incompatible coded or Quantity columns. Save, reload, edit one input construction, and confirm the consumer retains its selected publication. Publish that input on source generation G1, pin it in the consumer, then refresh and republish on G2; the consumer must still read G1 until Update input previews and applies the G2 artifact. Referenced artifacts survive removal of a table from navigation. A construction can also start from an exact published table input without requiring a loaded-record root.

## P07. Save, ClickHouse publication, and evidence

**Outcome.** The saved configuration produces a queryable ClickHouse dataframe whose contents and meaning agree with the reviewed construction.

**Existing code.** Explorer draft/revision persistence, compilation receipts, `internal/explorer/lifecycle/publish.go`, publication/materialization services, `ui/packages/loom-ui/src/api.ts` publication and query calls, population mapping, and cell trace.

**Required changes.**

- Make save/configuration status and Publish to ClickHouse visible in the workspace. Show build progress, success, and recoverable failure through the existing lifecycle.
- Carry step and input-version identity into the canonical recipe, receipt, public output contract, and published revision.
- Prove the previewed source-data state at Publish with an immutable generation or an optimistic unchanged-data check. Refuse a stale preview; a snapshot token that describes only metadata is insufficient.
- Enforce effective resource security-label policy at compilation, materialization, query, trace, and export. The published artifact records the authorized scope and cannot be read by a principal with a wider or different scope merely because its table name is known.
- Reopen the full construction after publication. Inspect a result's relevant source contributors and transformation meaning.
- Publish a machine-readable data dictionary for row identity, source paths, contributor and multiplicity policies, semantic and time-field assertions with their versions, coverage scope, and evidence limits. Compare schema, code and unit identities, coverage, values, and contributor identities before a refresh replaces the accepted result. Label sampled comparisons and require review of any detected change.
- Extend stage profiles and cell evidence where existing source/shape-specific diagnostics cannot explain composed output. State evidence limits rather than inventing complete lineage.
- Preserve materialization identity across query pages. A failed replacement build must not masquerade as a successful new published dataset.

**Completion checks.** Build a construction in the browser, preview, save, publish to the local ClickHouse-backed stack, query actual values, reload, and inspect evidence. Verify the same config, source-data state, input artifacts, and semantic-assertion versions produced the schema and previewed values. Change source data between preview and Publish and verify immutable-generation use or refusal. Repeat after an explicit input update and a refresh with value-only, contributor-only, code-system, unit, and coverage changes; review the diff before replacement. Include a publication failure and an export/query continuity check. With two principals and mixed resource security labels, verify that discovery, trace, published query, and export reveal only each principal's authorized population.

## P08. Preview performance

**Outcome.** Routine edits produce a correct preview fast enough to support repeated exploration. The performance result is measured on the real interaction path.

**Release targets.** Local selection and cached menu feedback within 100 ms at the 95th percentile. Warm structural capability refinement within 250 ms at the 95th percentile. The first page of a warm field or observed-code search within 500 ms at the 95th percentile. Routine warm edit-to-render preview latency under one second at the 95th percentile on the frozen representative workload. Report cold and expensive transformations separately. These targets are unmeasured requirements, not claims about current performance. P08 and F0 remain open if any release target is missed unless the product owner explicitly changes the target with the measured tradeoff recorded.

**Measurement contract.**

- Start the clock at the user action that requests the changed preview. Include intentional debounce, capability refinements, resolution, compilation, query, serialization, transport, and grid rendering.
- Stop when correct rows for the newest proposal are visibly rendered and eligible for Apply. A spinner, cached old table, or schema-only response is not completion.
- Freeze workload identity, source snapshot, row counts, cardinality, width, construction depth, match multiplicity, concurrency, environment, and cold/warm cache state.
- Cover small parameter changes within filtering, grouping, pivot reduction, related-source additions, and Combine. Include edits to earlier steps and high-cardinality category discovery. These structural workloads test generality without defining the product's domain.
- Measure opening an operation, refining selected columns, finding fields and observed codes, and loading population-scoped coverage as separate actions. Structural choices must not wait for expensive coverage profiles.
- Use a reproducible, authorized FHIR fixture with at least 100,000 root records, 1,000,000 related records, 25 output columns, five composed stages, and both one-to-one and one-to-many paths. Fix its generator seed and source generation before the first baseline. A smaller development fixture can diagnose regressions but cannot close P08.
- Record distributions and variance, including median and tail latency, server stage timings, query work, cache reuse, failures, and cancelled requests. Prove the harness distinguishes a small edit from an expensive recomputation.

**Optimization loop.** Establish a baseline first. Change one measured mechanism at a time, rerun the frozen workload and correctness checks, and retain only demonstrated improvements. Log the hypothesis, change, before/after measurements, and verdict. Candidate mechanisms include scoped compilation reuse, retained unchanged inputs, request coalescing/cancellation, bounded preview work, cached structural choices, decoupled profile scans, and measured query/index changes.

**Correctness constraints.** Cache keys include authorization, source generation, exact input materializations, selected stage, and parameters. Cancellation or stale responses cannot alter the accepted result. Sampling must retain the declared meaning; truncating source rows before grouping or pivoting can change values. Expensive complete counts and distributions can run separately when their scope and completeness are labeled.

**Completion checks.** Meet each release target on the frozen representative workload and demonstrate repeatable gains beyond measurement noise. Report separate warm p50/p95 results for structural choices, field/code discovery, coverage evidence, and row preview; report cold and expensive cases without hiding them in the warm distribution. Keep row cardinality, authorization, schema, and version-consistency checks green. Publish the baseline and final distributions plus the retained changes. Install a regression check with a documented environment and noise tolerance. An unmet target leaves P08 and F0 open and identifies the measured bottleneck and remaining gap.

## P09. Dataframe evidence

**Outcome.** The researcher can understand exactly which ArangoDB records appear in the framed table and where sparse records, multiple matches, row duplication, or incomplete evidence affect it.

**Existing code.** Row-definition and population choices, source inspection, semantic inventory, contributor policies and time windows, dataframe contract, stage receipts, cell trace, publication identity, and available profiles. These provide inputs, not a complete frame report.

**Required changes.**

- Compare source-record frequency with coverage of the actual output rows. Separate absent records, dangling references, recorded nulls, zero values, missing time anchors, and out-of-window values; disclose when the source cannot distinguish them. Keep observability unknown unless the source supplies a declared coverage population or interval.
- Show row count, distinct entity count where applicable, duplicate row identities, contributor multiplicity, and row changes caused by each proposed edit.
- Show the selected time field, its declared role, window boundaries, and excluded records. Withhold “as of” claims when availability time is unknown. Keep model leakage, evaluation splits, and imputation outside builder scope.
- Carry the frame report's exact construction, source generation, input artifacts, scope, and completeness into publication and reopening. Keep expensive profiles separate from the bounded row preview.
- Scope evidence to the effective resource security-label policy and disclose when a count is withheld. Trace cells to the exact contributing source records without exposing excluded or unauthorized records.

**Completion checks.** Review a sparse related column and see its output-row coverage and missingness categories before Apply. Use distinct rows with no source record, a null source value, zero, a missing time anchor, a dangling reference, and an out-of-window source record. Apply a time window and see raw, eligible, excluded, and missing-anchor contributors with their declared denominator. In the browser, compare exact, sampled, and unavailable coverage; every number states its scope and completeness, and unavailable never appears as zero. Publish and reopen; the report identifies the same table revision, schema, and input artifacts. A failed or unavailable check is labeled, not silently passed.

## Handoff and sequencing

Use F0's source-to-column specification and three construction checks to sequence the supporting work. Begin P08 baseline measurement on today's preview path while P01 settles its stage contract. P02 establishes the accepted shell using existing controls and the agreed contract. P09 starts with P03's first source-to-column specification, not with a final report screen. Each checkpoint below ends with executable evidence on the actual backend before work expands to another operator family.

| Checkpoint | Required result before it closes |
| --- | --- |
| A. One sparse related column | P01's stage-local source operation and P03's semantic choices produce a saved, editable column. P02 previews the exact proposal. P09 reports eligible, absent, null, and excluded contributors. P07 publishes and reopens the same reviewed values from a provable source-data state. |
| B. Row and column breadth | P04 and P05 complete F0's entity, event, and category constructions, including missing keys, lists, numeric reductions, comparator handling, long high-cardinality output, and held-out FHIR paths. Each step remains editable and preserves semantics through later stages. |
| C. Versioned table input | P06 consumes a transformed AQL stage and a pinned ClickHouse artifact, then feeds another operation and publication. Refresh and republish of the input do not change the consumer without Update input. |
| D. Researcher and speed gates | P02's non-SQL researcher task study, P08's representative latency gates, and P09's exact, sampled, and unavailable evidence checks pass. F0 closes only after all three constructions, the cross-engine chain, security boundary, publication, and reopening pass in the browser. |

P08 continues through each checkpoint. The source-catalog assertions, resource security-label policy, and publication identity are shared contracts; their owners are P03, P01/P06, and P07 respectively. A package cannot close on a schema-only preview, an implementation-local fixture, or a reported performance miss.

Each package handoff includes the changed contract, actual files, focused executable checks, browser evidence where relevant, and performance impact. Cross-cutting OpenAPI changes originate in `openapi/openapi.yaml`; generated Go and client contracts must stay synchronized.

This plan establishes deliverables and checks. It does not reinstate the withdrawn agent-week estimate. The first compiler-composition checkpoint and the preview baseline provide concrete evidence for sizing the remaining work.
