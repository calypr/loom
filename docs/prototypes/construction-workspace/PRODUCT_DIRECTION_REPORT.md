# Loom dataset builder: product direction

**Status:** Product decision for implementation review, 2026-09-24. This document defines what the builder must accomplish. It does not claim that the current implementation meets the contract.

## Decision

Loom will let a researcher or analyst who understands their data, but does not write SQL, frame records already loaded into ArangoDB as a reproducible dataframe. The researcher will define what one row means, choose which records and fields contribute, inspect the resulting rows and columns, then save the AQL-backed construction and publish its computed result to ClickHouse. The result may be used for machine learning, but model-specific feature engineering happens after publication.

The product is a general dataset builder. It will not ship a catalog of hardcoded clinical dataset recipes. Generality comes from operations that can consume the result of earlier operations, repeat, and compose. The interface will present those operations through the researcher's dataset decisions. An operation counts as useful when it helps produce or assess the intended rows and columns.

“Any dataframe” is the design aim, not a claim of unlimited execution. Loom must show which constructions it can execute before the researcher commits an edit. Unsupported cases must have a specific reason. Each missing capability should become a general operation or data contract, rather than a one-off dataset template.

## The job the builder performs

Institutional records are often sparse, repeated, and spread across related record types. A dataframe needs explicit decisions about its rows, which records contribute to each column, how repeated values collapse or remain repeated, and what absence means. Today those decisions commonly live in bespoke queries and are hard to inspect or reuse.

Loom will make those decisions editable and visible beside the resulting table. It will also show what source fields and codes exist, with their meaning, example values, and coverage. The researcher should be able to answer both “can I build this column?” and “what does this column actually represent?” while authoring.

## The researcher’s path

1. **Define rows.** Select a starting record type or constructed table and state what one row represents. Show the entity key, repeated rows per entity, population rules, and an anchor time when the selection needs one.
2. **Add columns.** Find a source field, observed code, related record, or pinned version of another table. Choose the relationship, contributing records, time window when relevant, and output form. A source may yield a direct value, presence flag, record count, supported reduction, or repeated value.
3. **Arrange rows and columns.** Filter the population, group or expand repeated records, and pivot values when those choices change how the ArangoDB records appear in the dataframe.
4. **Review the frame.** Inspect row counts, coverage, absent and null values, match multiplicity, lineage, and schema. Show what each number measures and when Loom cannot establish a check.
5. **Publish.** Save the editable construction and exact input artifacts, compute the dataframe, publish it to ClickHouse, and let the researcher reopen, query, and export the published result.

The table and its editable history stay visible throughout. A valid change previews automatically after a short pause. Apply requires a successful row preview for that exact proposal. If an earlier edit breaks later steps, the accepted result remains available while the researcher repairs or explicitly removes those steps.

## What the controls mean

**Keep rows, Reshape, and Combine are ways to frame records, not separate product promises.** Keep rows sets the population or selects contributing records. Reshape changes what a row represents or how repeated values appear. Combine brings another table’s columns or rows into a construction and leaves the result available to later operations.

**Calculate is out of scope.** The builder will not author arbitrary formulas, scores, recodes, imputations, or other derived columns from values already in the dataframe. Counts and reductions of matching source records remain in scope because they decide how repeated ArangoDB records are represented in a row. This boundary applies to both the primary flow and advanced controls.

The main controls should follow the researcher’s decisions: **Define rows**, **Add columns**, **Arrange records**, **Review frame**, and **Publish**. The backend supplies the valid choices for each selected table, column, relationship, and operation. The frontend does not guess capabilities and wait for a veto after Apply.

## What an accurate frame requires

Loom must show enough evidence for a researcher to understand what the table contains:

- The row count, distinct entity count, duplicate row identities, and rows per entity.
- Each column's coverage over output rows, source availability, recorded nulls, absent records, and zero values. These states must stay distinct.
- The number of contributing records and the effect of each match, reduction, or exclusion rule on rows and values.
- When a time window selects records, its boundaries and the number of records excluded by it.
- The published schema, units, code sets, source versions, and lineage needed to explain a value.

Loom must label unavailable evidence as unavailable. Publication does not certify that the dataframe is ready for a particular model.

## Acceptance tests for the product

The builder succeeds when a domain researcher can independently frame and publish several structurally different sets of ArangoDB records without AQL or SQL. The checks must vary row meaning, relationship shape, sparsity, time-window selection, and wide, long, or repeated output. Those cases test the builder’s breadth; they do not define the only datasets it can make.

For each supported construction, the researcher must be able to reopen and edit every step, preview changes before Apply, and inspect how a source became an output value. A table used as input retains the exact version selected until the researcher explicitly updates it. The reviewed construction and published ClickHouse result must agree on schema and values.

Responsiveness is part of acceptance. Structural choices must arrive without a population scan, and routine warm row previews must complete under one second at the 95th percentile on P08's frozen representative workload. This is a release target, not a measured claim about the current builder. Performance work uses measured slow paths rather than speculative indexes.

## Current implementation and remaining work

The current work has a typed construction and proposal foundation, a table-first workspace, some source discovery, and several table operations. Related-source selection is present in Add columns. These pieces are useful, but the full path above has not been demonstrated in the browser through publication and reopening.

The largest product gaps are the complete Add columns flow for sparse related data, honest output-row coverage, row meaning through edits, editable composition with versioned table inputs, frame evidence, and end-to-end publication agreement. Existing formula work is outside the builder scope. A terminal-only Combine result does not meet the composability contract. Preview latency remains an explicit measured workstream.

Implementation work should now be judged against the [framing contract](FRAMING_CONTRACT.md) and [revised work packages](WORK_PACKAGES.md). Completing a Filter or Combine editor alone does not close a package. The next integrated demonstration should begin with a defined row, add sparse related records as columns with coverage, review the result, and publish that exact construction. Other shapes must then pass the same contract without hardcoded dataset logic.

## Design references

[OHDSI FeatureExtraction](https://ohdsi.github.io/FeatureExtraction/articles/UsingFeatureExtraction.html) and its [study population contract](https://ohdsi.github.io/PatientLevelPrediction/reference/createStudyPopulation.html) illustrate explicit source concepts, row identity, and time windows. Loom’s framing scope is independent of those specific data models.
