# ArangoDB dataframe framing contract

This contract defines the builder's scope. Loom frames records stored in ArangoDB into a reproducible dataframe and publishes the result to ClickHouse. The intended user understands the records but does not write AQL or SQL. A published dataframe can feed an ML workflow, but model-specific feature engineering is outside this builder.

## Product boundary

The builder answers five questions:

1. What does one output row represent, and which source records belong in it?
2. Which fields, observed codes, and related records appear as columns?
3. When several source records match a row, do they expand into rows, remain repeated, or reduce to a value, presence flag, or count?
4. Which source records are included by filters, relationships, and optional time windows?
5. What rows and columns did these choices actually produce, and which source records explain them?

Source-record framing compiles through Loom's existing AQL execution path. A pinned published table can require a typed, private AQL-to-ClickHouse intermediate boundary. Both paths use one construction meaning and one preview-to-publication contract. Each supported operation consumes the current table and produces another editable table. The backend tells the frontend which choices are executable for the selected context before Apply.

Adding a related source is a construction operation at the selected stage. It must also work after a group, expansion, pivot, or table input when that stage retains a valid row anchor. A column label alone cannot establish that relationship. If the row anchor is gone, the backend explains why the source cannot be added there. A source condition and projected value from a repeated FHIR element must bind to the same element. The saved specification records whether matching counts distinct source records or graph-path occurrences, and it cannot count the same record twice by accident.

**Calculate is outside the product boundary.** Do not plan an arbitrary formula editor, cross-column arithmetic, scores, recoding, imputation, or derived-column authoring in the builder. A count, selection of a representative record, or supported reduction across matching source records remains in scope. Those operations determine how ArangoDB records are framed, rather than changing values after the dataframe exists. Existing calculation code can remain in the repository but does not count toward builder completion.

## Workspace decisions

| Researcher decision | Required meaning | Main operation types |
| --- | --- | --- |
| Define rows | Root or table input, row identity, population, and repeated rows | Select source, filter, group, expand |
| Add columns | Source meaning, relationship, contributors, multiplicity, value form, and absence | Traverse, match, select, count, reduce |
| Arrange records | Wide, long, grouped, or repeated representation | Group, pivot, unpivot, expand, append |
| Review frame | Row count, coverage, null and absent values, multiplicity, schema, and lineage | Profile, compare, trace |
| Publish | Exact saved construction and input artifacts, queryable result | Compile, materialize, reopen |

The table and editable step history stay visible. A valid edit previews automatically after a short pause. Apply requires a successful row preview for the exact proposal. Editing an earlier step leaves the accepted result available until broken later steps are repaired or explicitly removed. A referenced table remains pinned to the selected published artifact, including its source generation and materialization, until the researcher explicitly updates it.

## Evidence and acceptance

The review must state what each number measures and whether it is exact or sampled. It must show output rows, distinct entities where applicable, duplicate row identities, source-wide frequency, coverage over current output rows, source absence, recorded nulls, zero values, missing time anchors, unresolved references, and the number of records contributing to each output. An unauthorized target cannot be exposed through reference evidence. Source observability remains unknown unless metadata declares a relevant coverage population or interval. A time-window choice must show its selected time field, boundaries, precision and missing-anchor policies, and excluded records; an “as of” claim requires availability-time evidence. Evidence that Loom cannot establish remains labeled unavailable.

Code system and version, value type, unit identity, and approved reconciliation policy belong to the saved source-to-column meaning. Loom must not silently combine values with incompatible identities. Publication includes a machine-readable data dictionary and a review of schema or coverage changes after a source refresh. These rules apply across FHIR resource types, without special cases for Observation.

The minimum numeric reductions over repeated compatible source values are minimum, maximum, sum, and mean. Each has an explicit null and zero-match policy. A comparator-bearing Quantity cannot become an exact point value through one of these reductions without an approved interval-aware policy. Appending or coalescing columns preserves the same code, unit, and source meaning checks as reducing or pivoting one source. High-cardinality categories remain representable as long rows when a bounded wide pivot would hide values. Grouping has an explicit policy for absent and null keys, including whether they share a group.

A successful Apply preview and publication use the same saved construction and provable source-data state. Loom either reads immutable source generations or checks that the source did not change before publication; a stale preview cannot authorize a changed build. A refresh compares schema, semantic identities, coverage, values, and contributing-record identities before replacing an accepted published result. The review states which comparisons are exact and which are sampled.

The builder is complete only when a researcher can construct, reopen, edit, preview, and publish structurally different frames without AQL or SQL. Verification must vary row identity, relationship shape, sparsity, optional time-window selection, and output layout. These cases test generality; they are not hardcoded dataset templates. Preview and ClickHouse publication must agree on the saved construction, schema, previewed values, and pinned input artifacts.

The primary Add columns path must work for sparse related records and explain coverage. Reshape must let the researcher choose what rows and columns represent. Combine must produce an ordinary intermediate table that later steps can consume. Fast previews are part of acceptance and require measured performance work.

Acceptance includes a held-out FHIR resource type and relationship shape absent from the implementation fixtures. The existing metadata-driven flow must discover, preview, publish, and reopen it without resource-specific code changes. All discovery, previews, contributor traces, published queries, exports, and caches use the same authorized population; counts or examples cannot reveal records outside that scope.

## Work-package interpretation

[F0: Frame sparse ArangoDB records as a model table](SPARSE_RECORD_FRAMING_WP.md) defines the exact operations and end-to-end acceptance. The [supporting work packages](WORK_PACKAGES.md) provide implementation boundaries. P01 owns composable construction. P02 owns the workspace and proposal lifecycle. P03 owns source discovery and adding related data. P04 owns population and contributor selection. P05 owns row and column arrangement. P06 owns pinned table inputs and cross-engine execution. P07 owns publication and reopening. P08 owns preview performance. P09 owns evidence about the framed table. No package is complete merely because one generic operator runs.
