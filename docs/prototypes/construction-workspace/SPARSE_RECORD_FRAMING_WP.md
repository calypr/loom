# F0. Frame sparse FHIR records in ArangoDB as a model table

**Outcome.** A researcher chooses what one row represents, brings in fields and related FHIR records, resolves one-to-many relationships, and publishes a table whose columns have an explicit source and absence meaning. The researcher does this without AQL or SQL. The result is suitable for downstream model preparation; Loom does not author arbitrary formulas, recodes, or imputation rules.

This is the central product work package. P01–P09 in [the supporting plan](WORK_PACKAGES.md) provide persistence, editors, execution, evidence, publication, and performance. None of them closes F0 on its own.

**Generality rule.** Row, path, code, Quantity, time, and coverage choices come from the selected source's metadata and observed data. F0 must not branch on a specific FHIR resource type or field name to offer its main operations. A source may lack a code, a time field, or a declared coverage interval; the editor then omits that choice or labels the evidence unavailable.

## The construction contract

Every output column must answer: **for each output row, which source records were eligible, what value was taken from them, and what happened if there were zero or several matches?** The answer is saved with the column and survives editing, preview, and publication.

The authoring model has six decisions, in this order:

| Decision | Exact operation | Required policy or evidence |
| --- | --- | --- |
| Define row identity | Choose a root record, grouped key, or expanded related record as one output row. | Stable compound row key; explicit treatment of absent and null group keys or empty expansions; duplicate identity count. |
| Follow a relationship | Traverse an authorized, directed path from that row to source records or select a pinned published table artifact. | Exact path and direction; match cardinality; unmatched-row and dangling-reference behavior; whether duplicate paths mean one source record or several path occurrences. |
| Select contributors | Keep matching records by source field, observed code, nested condition, and optional time window relative to a chosen row field. | Whether the condition removes output rows or only contributors; same-element binding for repeated FHIR fields; selected source and row time fields; window bounds; missing-anchor and excluded-record counts. |
| Represent one match | Project a field or structured record when the path is singular or uniqueness is required. | Refuse an ambiguous first match. If a representative is chosen, require ordering and a tie rule. |
| Represent many matches | Keep a list, expand into rows, or reduce matches to presence, count, distinct count, selected value, minimum, maximum, sum, or mean over compatible numeric values. | Exact reduction and null policy; zero-match result; contributor count; row growth or loss. |
| Arrange a code or category set | Keep selected codes as long rows or pivot a bounded, explicitly selected set into columns. | Observed codes and coverage; duplicate-cell policy; stable headings; handling of unlisted and newly observed codes. |

These operations compose. A grouped result can receive another related column; an expanded result can be filtered; a pivoted result can be joined to a pinned table and transformed again. The backend supplies only valid choices for the selected row context. A failed AQL preview is not the normal way to discover that a choice is unsupported.

Adding a related source is an explicit stage operation, not a mutation of the initial source projection. Its input is the selected stage's row identity and retained source anchors; its output is a new stage with stable column identities. A grouped, expanded, or combined stage can add related data only where those anchors still identify the intended records. The editor explains a lost anchor before proposal.

## The sparse-data rule

The builder distinguishes no related source record, an eligible record with a null value, a recorded zero or false value, a related record excluded by a contributor rule or time window, a missing row-time anchor, and a dangling reference whose target is absent. A row may contain both eligible and excluded records, so evidence reports raw matches, eligible matches, excluded matches, unresolved references, missing anchors, and populated values as separate counts rather than forcing one status. An unauthorized target does not disclose its existence through these counts. A chosen output form may map cases to the same scalar only if its policy says so. Presence and count refer to eligible distinct source records unless the user explicitly selects path-occurrence counting; populated-value count refers to non-null values. The UI shows both source-record frequency and coverage over the current output rows.

The useful unit of authoring is a **source-to-column specification**. One selected source path and contributor rule can produce a related value plus optional support columns such as eligible-record count or source-present status. Those support columns are generated from the same selected records and policy, not from a separate formula. Their names, visibility, and meaning are explicit. This lets a researcher frame sparse data honestly without hand-building parallel joins for every coverage indicator.

A code set can likewise use one contributor rule and one output form to create several columns. The researcher reviews the observed codes, their output-row coverage, and the resulting schema before Apply. Loom freezes the accepted code-to-column mapping; a later source refresh proposes new columns instead of silently changing the published frame.

## Source meaning and coverage

**Semantic identity.** A coded value retains its code system, system version when known, code, source path, and value type. A Quantity retains its unit system, code, and comparator when present. The builder must not merge values because their display labels match. Before grouping, reducing, or pivoting values into one output column, it checks whether their types, codes, units, and comparator meanings are compatible. Minimum, maximum, sum, and mean treat a Quantity as a point value only when it has no comparator; otherwise the editor requires an approved interval-aware policy or refuses the reduction before preview. A versioned, approved mapping or unit policy may reconcile compatible source values; otherwise Loom keeps them separate or reports why the proposed column is invalid. This is source harmonization, not arbitrary derived-column authoring.

**Coverage context.** “No matching record” describes the result of a traversal and contributor rule. It does not prove that the source would have recorded the event or value for that row. For every candidate source, Loom shows output-row coverage, match multiplicity, and the scope of those measurements. If ingestion or source metadata declares a population or interval in which records could be expected, Loom may show that as separate coverage evidence. Without such metadata, observability is unknown. The builder must not infer a negative value from an absent record by default. This rule applies to every resource type and path.

**Time roles.** A source can expose several time fields with different meanings, such as when an event occurred, when a value became available, or when a record entered the system. F0 discovers the fields and their declared roles from versioned metadata with provenance; it does not assume a field name for a resource type. A contributor window saves the chosen source time field, row anchor, bounds, inclusivity, precision policy, and missing-anchor policy. By default a row with no usable anchor remains in the table with no eligible windowed contributors and an explicit missing-anchor count; excluding that row requires a separate row filter. A field containing partial FHIR dates cannot be offered as an exact instant window unless its interval or exclusion rule is explicit. An “as of” claim additionally needs a declared availability time. A resource update timestamp is not availability evidence unless the metadata specifically proves that role. If no availability time is known, Loom can still frame the records, but it cannot claim that the column was knowable at the anchor time.

**Population-scoped discovery.** Search results for fields, paths, and observed codes are evaluated against the current output rows. Each result shows a bounded estimate or exact count of rows with at least one eligible record, the distribution of match counts, and the expected output form or width. Source-wide frequency appears separately. The researcher can compare candidates before adding a column; a rare source remains selectable when it is meaningful. Slow exact profiles load after structural choices and state their denominator and completeness.

The source catalog owns the metadata that makes these choices possible. It supplies path direction, repeated-element ownership, code system and version, Quantity unit, time-field role and precision, and declared coverage population or interval, each with provenance and a version. When metadata is absent, Loom offers only constructions it can prove from the source schema and labels semantic evidence unavailable. A new resource type with equivalent metadata receives the same operations without a resource-specific branch.

**Authorization.** The authorized population includes project, path, and resource-level security-label policy. An unknown or unmapped security label excludes that record until a policy grants it. Discovery counts and examples, capability evidence, row preview, contributor trace, publication, ClickHouse query, export, and caches use that same effective population. A saved construction or pinned table never widens it for another viewer.

## What the researcher sees

The main flow is **Define rows → Add related information → Choose how matches appear → Review frame → Publish**. When the researcher selects a source, the editor asks only the unresolved questions: relationship, contributor conditions, time bounds, output form, and zero/many-match behavior. Discovery shows candidate coverage for the current rows before selection. The preview compares current and proposed row counts, affected columns, coverage, and a few traceable example cells. Selecting a cell reveals the source records that contributed to it.

There is no top-level Calculate action in this package. Count, presence, ordered selection, and reduction appear only while choosing how matching source records become columns. Reshape appears when the researcher changes row identity or turns repeated records into rows or columns. Combine appears when another constructed table is an input; its exact published artifact is pinned.

## Backend contract and concrete gaps

The current backend already has pieces of this grammar: server-issued route and value-form choices in `internal/explorer/capability/construction.go`; source, aggregate, contributor-window, and ordering types in `internal/explorer/authoringv2/semantic_types.go`; row definitions in `internal/explorer/authoringv2/row_definition.go`; aggregate operations in `internal/dataframe/recipe/document_types.go`; and typed construction steps in `internal/explorer/authoringv2/construction.go`. These are reuse points, not proof of end-to-end F0 support.

F0 must close these gaps as one vertical path:

1. Resolve capabilities against the **current output row context**, including after group, expand, pivot, or Combine. Return a reason when a relationship no longer has a valid row anchor. Add a typed stage-local related-source operation and carry semantic anchors across every stage boundary.
2. Persist source path, contributor rule, form, multiplicity policy, absence policy, and stable output identities in an editable step. One backend lowering path produces preview and publication.
3. Offer a bounded code-set expansion and optional support columns from one source-to-column specification. Reuse existing pivot and aggregate execution where their semantics fit. Keep an unbounded or high-cardinality code set available as long rows.
4. Return population-scoped discovery and proposal evidence with output-row coverage, source-record counts, null and absent counts, match multiplicity, row effects, and traceable contributors. Identify sampled or unavailable evidence.
5. Preserve code-system identity, value type, Quantity comparator, unit identity, approved mapping version, time-field role, repeated-element owner, distinct contributor identity, unresolved-reference status, and coverage basis through capability, proposal, AQL lowering, and publication. Reject incompatible reductions and semantic append alignments before Apply.
6. Publish and reopen the reviewed schema and previewed values from the exact construction revision, pinned table inputs, and a provable source-data state through the existing ClickHouse path. Publish a machine-readable data dictionary with row identity, source paths, contributor rules, zero/many-match policies, semantic identities, coverage scope, and evidence limits. On refresh, show changes in schema, types, codes, units, coverage, values, and contributor identities before replacing the accepted result. State when a comparison is sampled.

## Completion checks

F0 closes when a researcher can complete these three structurally different constructions in the browser against the real backend, then reopen and edit them:

1. **One row per entity.** Traverse a one-to-many relation, select an observed code set and a preceding time window, and create value, presence, count, an ordered list, and one numeric-summary column from repeated compatible Quantity records. Verify no related record, recorded null, recorded zero, an out-of-window record, a missing row-time anchor, and a dangling reference on distinct rows. A missing anchor keeps its row and reports no eligible windowed contributor; a dangling reference has separate evidence from no reference. Inspect output-row coverage and contributor traces; verify every list value and its declared ordering after publication.
2. **One row per event.** Expand related records into rows, retain the parent identity, filter contributors without silently removing parent rows, and add a second related field at the expanded stage. Verify stable unique compound row keys, empty-expansion policy, duplicate graph paths, and row growth. Add another related source after grouping when its row anchor remains valid; otherwise inspect the backend reason.
3. **One row per entity and category.** Group or keep long rows, then pivot an explicitly selected category set. Group several rows with absent or null keys and another row with a present key; verify the saved missing-key policy, resulting group count, and contributor count. Verify duplicate-cell handling, unlisted codes, and a source refresh that discovers a new category without altering the accepted schema. Keep a high-cardinality category set as long rows without silently dropping categories.

The three constructions must also pass these checks:

- Use a coded source, a Quantity source, and a repeated or referenced source without either field type. At least one construction uses no Observation records. Reserve a resource type and nested or referenced path absent from implementation fixtures. The existing metadata-driven flow must discover, preview, publish, and reopen it without a resource-specific code change.
- Test two repeated elements with different codes and values. A condition on one element cannot project the other's value. Same-label codes from different systems do not merge. Incompatible units do not combine, and a comparator-bearing Quantity never becomes an exact point value through reduction.
- For every construction, show valid choices before Apply, preview rows successfully, save, publish, query actual ClickHouse values, and reopen the same editable meaning. Compare the published data dictionary and refresh report with actual schema and values. A later step consumes the framed result.
- Frame FHIR rows in AQL, group or expand them, combine with a pinned published input, continue with another step, and publish. Use a pinned cohort table to keep and to exclude matching rows in separate proposals.
- Change source records between preview and Publish. Loom either uses an immutable source generation or rejects the stale preview. Refresh a published frame with value-only, contributor-only, semantic-identity, and coverage changes; review them before replacement.
- Test two principals against mixed security labels through discovery, preview, trace, ClickHouse query, and export. An unknown label cannot leak counts or values.
- Permit one “as of” claim backed by a declared availability role. Refuse the claim for an event or update time without that role. A partial date's window policy is stated before preview. Unavailable observability remains unknown.
- Measure warm edit-to-visible-preview latency on each path against P08's frozen representative workload. F0 remains open while that gate is unmet.

These cases test the grammar across row identities and relationship shapes. They are not hardcoded product templates.

## Boundary with later ML work

F0 produces a faithful, documented dataframe from sparse records. Source-level code and unit reconciliation is part of faithful framing. Model-specific labels, feature formulas, model normalization, imputation, encodings, and train/test splits are separate downstream work. Publication does not certify a model-ready training process.

The choice to make row identity, contributor windows, and presence/count forms explicit aligns with [OHDSI FeatureExtraction's covariate settings](https://ohdsi.github.io/FeatureExtraction/reference/createCovariateSettings.html) and [temporal cohort covariates](https://ohdsi.github.io/FeatureExtraction/reference/createCohortBasedTemporalCovariateSettings.html). F0 applies the idea to Loom's ArangoDB graph and keeps its authoring grammar independent of a particular medical data model.
