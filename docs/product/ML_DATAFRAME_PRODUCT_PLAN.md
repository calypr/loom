# ML-ready dataframe product tranche

## Product promise

A researcher who does not understand FHIR paths should be able to define what one row means, turn repeated clinical data into explicit features, see whether those features are usable, publish the result, and download a reproducible training artifact. Loom must explain every lossy choice before publication and preserve the data, schema, quality evidence, and provenance under one immutable identity.

This tranche is not another general cleanup pass. Each work package ends in a user-visible result driven through the local Builder, Preview, Publish, Viewer, and export loop. Backend work is included only when a frontend promise requires it.

The canonical execution tables are [ISSUES.csv](ISSUES.csv) and [WORK_PACKAGES.csv](WORK_PACKAGES.csv). Validate them with:

```sh
python3 scripts/validate_architecture_plan.py --plan-dir docs/product/ml-dataframe
```

## Current product boundary

The current flow is mechanically complete:

```text
Builder -> commands -> reconcile receipt -> preview -> publish
        -> Viewer -> paginated rows/facets -> browser-built CSV
```

It is not yet semantically safe for ML authoring:

- Row grain is inferred from the selected root rather than chosen and validated as a product decision.
- Relationship multiplicity is hidden. A one-to-many child can be reduced with `FIRST` while the output still appears lossless and ML-ready.
- Candidate metadata advertises `VALUE`, `INDEXED`, `FIRST`, `ALL`, and `DISTINCT`, but Builder uses the default without a visible choice.
- Builder cannot author the aggregate and typed feature vocabulary already present in lower layers.
- Preview shows cells, not feature coverage, row-key uniqueness, relationship cardinality, or actionable readiness evidence.
- Viewer export collects every page into browser memory and is not visibly pinned to one immutable publication.
- The receipt manifest describes compilation, not a portable data-plus-schema-plus-quality artifact.

## Observable final artifact

The user-visible result is a versioned `LoomDatasetArtifact`:

```text
loom-dataset-artifact/
  data.parquet
  manifest.json
  schema.json
  provenance.json
  quality.json
  README.md
```

CSV and JSONL remain explicit compatibility formats. Parquet is preferred once the server exporter supports it.

The artifact contract must include:

- An immutable publication, revision, and materialization pin.
- Row grain, row key, row and column counts, file digests, and exact output selector.
- Ordered columns with stable key, human label, logical/storage type, nullability, array shape, feature role, and encoding guidance.
- FHIR resource, relationship occurrence, path, choice arm, code system, repeated coordinates, projection policy, derivation, and missing-value meaning.
- Receipt, snapshot, generation, recipe/schema/contract digests, compiler/translation versions, cohort filters, and export time.
- Measured row-key duplicates, coverage, missing/empty/invalid counts, distinctness, distributions, relationship zero/one/many counts, and any collapsed or truncated values.
- `ready`, `ready_with_warnings`, or `blocked` with stable reason codes and plain-language remediation.

An artifact must fail rather than silently complete if its data and schema disagree, its pinned materialization disappears, or a lossy choice was not explicitly authored. A republish after export starts must not change any file in the artifact.

## User journey

1. Choose a project and name the training dataset.
2. Choose what one row represents and see whether the proposed row key is actually unique.
3. Add a related resource and see observed zero/one/many cardinality before selecting fields.
4. Choose how repeated values become features: safe scalar, indexed columns, array, aggregate, distinct list, explicit first, or separate output.
5. Add useful derived features such as Observation count, final-only count, and existence without writing FHIRPath, recipes, or AQL.
6. Preview values alongside coverage, missingness, cardinality, distributions, and actionable warnings.
7. Assign feature roles and review leakage/time-window warnings before publishing.
8. Publish once the receipt and readiness evidence agree.
9. Inspect the same semantics in Viewer.
10. Download a pinned artifact whose data and sidecars all identify the same materialization.

FHIR paths and compiler details remain available under advanced disclosure, but the primary copy explains the consequence in researcher language.

## Delivery sequence

| WP | User-visible outcome | Depends on | Execution |
| --- | --- | --- | --- |
| P0 | The hostile fixture and artifact contract make hard cases executable | Architecture WP04 loop | Serial on integration |
| P1 | Builder explains row grain and refuses silent one-to-many loss | P0; architecture WP01/WP05 | Serial on integration |
| P2 | A user explicitly chooses repeated-value projection | P1 | Serial on integration |
| P3 | A user authors and verifies derived aggregate features | P2 | Parallel with P5 on a dedicated branch |
| P5 | Builder and Viewer share plain-language FHIR semantics | P2 | Parallel with P3 on a dedicated branch |
| P4 | Preview reports measured quality and readiness evidence | P3 and P5 | Serial integration slice |
| P6 | Viewer downloads a pinned portable artifact | P4 and P5; architecture WP03/WP06 | Serial integration slice |

P3 and P5 are the only planned parallel branches. Their worker scopes are disjoint; shared OpenAPI/generated/UI contract files remain integration-owner files. All other packages stay on `arch/integration`.

## Work-package outcomes

### P0 — Contract and hostile fixture

Freeze artifact v1 and add a small FHIR fixture containing two related Observations for one Patient, repeated values, an all-null field, partial population, a choice type, coding system/display, and a missing reference. Extend the local driver with artifact-aware acceptance hooks and record cold/warm timing and memory baselines. Do not replace the fast smoke fixture.

### P1 — What does one row mean?

Expose observed relationship cardinality and require an explicit policy for a one-to-many route. A scalar child cannot claim lossless or ML-ready merely because its field path is scalar. The contract carries stable reason codes and the UI says which root rows are affected.

### P2 — Repeated-value projection chooser

Wire the existing projection vocabulary into Builder. Explain scalar, indexed, array, distinct, and first-value behavior before preview. `FIRST` is explicitly lossy and names its deterministic ordering. `INDEXED` states its width and overflow policy. Preview, receipt, Viewer, and artifact schema must agree.

### P3 — Derived feature studio

Add one guided feature editor and one typed authoring command boundary. The first released features are Observation count, filtered count, and existence. Exact values must agree in Preview, published reads, Viewer, and export before adding broader terminology, unit, or temporal operators.

### P4 — Coverage and readiness evidence

Profile the same immutable output that Preview and export read. Report row-key integrity, coverage/missingness, invalid values, distinctness, type-appropriate distributions, relationship cardinality, and cohort/filter effects. Readiness is a reasoned state with remediation, never an opaque boolean.

### P5 — FHIR explanation and semantic presentation

Define one column descriptor and one null/array rendering policy shared by Builder, Preview, Viewer, and artifact sidecars. Present friendly labels first while retaining exact paths, choice arms, relationships, code systems, and stable physical keys. Record feature roles and leakage/time-window intent without pretending to automate clinical judgment.

### P6 — Pinned portable export

Expose a server-owned streaming export bound to one publication/revision/materialization. Produce a checksummed archive containing data, manifest, schema, provenance, quality, and README. Add progress/cancel/failure handling and prove that a concurrent republish cannot mix revisions. Never present a partial archive as complete.

## Acceptance spine

Every package uses the warm local loop. Each issue runs focused behavior tests; each package runs `make verify-fast`; the integrated tranche runs Go/dataframe/OpenAPI/GraphQL gates, all UI checks/builds, and the complete DOM/API journey once. Run `verify-full` only when watcher/build/dev-driver behavior changes.

The tranche is complete only when these live scenarios pass:

1. A nontechnical user creates a Patient dataset and understands “one row per Patient” without seeing an internal ID as the only explanation.
2. A Patient with two Observations forces an explicit policy. Array/indexed/first outputs and warnings agree across receipt, Preview, Viewer, and artifact schema.
3. Total and final-only Observation counts have exact expected values everywhere.
4. An absent value and an all-null feature produce measured coverage and an actionable readiness state.
5. An export started from revision A remains entirely revision A after revision B is published.
6. Repeated, choice-type, and coding columns have stable machine names and matching human/FHIR explanations.
7. Reloading and switching Builder/Viewer preserves row grain, projection, readiness, and publication identity.
8. A multi-page export failure is recoverable and cannot yield a file presented as complete.

## Explicit non-goals for this tranche

- No general repository cleanup or package reorganization unless a live acceptance scenario proves it necessary.
- No silent one-hot encoding, normalization, imputation, or train/test split policy. Loom preserves semantic metadata and offers guidance; model-specific transformations require an explicit later contract.
- No attempt to infer clinical leakage safety automatically. Loom records feature role, index time, and observation window and warns when evidence is missing.
- No full terminology service or unit-conversion platform before the first aggregate feature slice works end to end.
- No raw source JSON in exported artifacts by default.

## Decisions to lock during P0

- First-class row grains and whether ambiguous child records become a separate output or may expand rows.
- Stable ordering and width rules for `FIRST`, `INDEXED`, and `DISTINCT`.
- The distinction among absent resource, absent path, empty array, null, invalid, and not applicable.
- Required feature roles and the minimum time-window/leakage metadata.
- Whether artifact v1 ships CSV plus sidecars before Parquet, or blocks until Parquet is available.
- Authorization rules for sharing manifests and quality counts across project scopes.
- Maximum profile/export rows, bytes, duration, and explicit truncation/failure behavior.

The first demonstration is intentionally narrow and valuable: select Patient plus repeated Observations, see the ambiguity, choose an explicit representation, inspect coverage, publish, and download a pinned artifact whose data, schema, quality, provenance, and README agree.
