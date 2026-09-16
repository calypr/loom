# Build a training dataset without learning FHIR

> Superseded on 2026-09-16. This document records the earlier Patient-first proposal, not the execution contract. Use [the implementation plan](ML_DATAFRAME_IMPLEMENTATION_PLAN.md) and [technical design](ML_DATAFRAME_TECHNICAL_DESIGN.md). The current execution tables contain B01-B08, which replace F1-F4. Do not implement the separate `DatasetDesignV1` adapter or the Patient-only workflow below.

## Product promise

A researcher can create a defensible training table without knowing FHIR paths, Loom recipes, AQL, or storage column names.

The primary workflow is:

```text
Rows -> Features -> Check -> Export
```

The FHIR graph remains available in **Advanced**. It supports inspection but does not organize the primary workflow.

The replacement plan's canonical execution tables are [ISSUES.csv](ml-dataframe/ISSUES.csv) and [WORK_PACKAGES.csv](ml-dataframe/WORK_PACKAGES.csv). Validate them with:

```sh
python3 scripts/validate_architecture_plan.py --plan-dir docs/product/ml-dataframe
```

## The first useful dataset

The first release builds one concrete dataset:

- One row represents one Patient.
- `Gender` is a direct Patient value.
- `Observation count` counts all related Observations.
- `Final observation count` counts related Observations with final status.
- `Has preliminary observation` is a yes or no feature.
- A related Observation value requires an explicit multiple-match choice.

The local fixture contains two Patients. Patient 001 has two Observations, and Patient 002 has one. The expected feature values are literal acceptance data:

| Patient | Gender | Observation count | Final observation count | Has preliminary observation |
| --- | --- | ---: | ---: | --- |
| `dev-patient-001` | `female` | 2 | 1 | true |
| `dev-patient-002` | null | 1 | 1 | false |

After a feature ships, every later package must preserve its values through inline preview, full Preview, publication, GraphQL, Viewer, and the available download format. Authoring results share one receipt. The published revision records that receipt, and Viewer plus downloads use the resulting materialization.

## What Loom already provides

Loom already has most of the execution path:

- Builder loads a server-owned draft and an authorization-scoped capability catalog.
- Versioned commands create tables, roots, routes, and ordinary field columns.
- Reconcile produces an immutable receipt. Preview and publish consume that receipt.
- The compiler supports root fields, repeated projections, routes, and aggregates such as `COUNT` and `EXISTS`.
- Viewer reads published rows through GraphQL and supports filters, paging, details, and CSV download.
- `make verify-fast` drives a real browser through Builder, Preview, Publish, Viewer, filter, CSV, and reload.

The product does not yet let a researcher state feature meaning. Builder submits the default field projection. It cannot author aggregate sources. Preview does not measure row-key integrity, coverage, or dropped related values. Export does not produce one data artifact with schema, quality, and provenance.

The compiler also has a correctness trap. A scalar field from multiple related resources can become `FIRST(FLATTEN(...))`. Loom can drop related values and still report the output as lossless and ML-ready. The new workflow must reject that implicit choice.

## Product shape

The workflow uses a closed dataset design. It does not expose a formula editor or a generic predicate language.

```ts
type DatasetDesignV1 = {
  version: 1
  title: string
  grain: RowGrain
  features: FeatureDefinition[]
}

type FeatureDefinition = {
  key: string
  label: string
  role: "identifier" | "feature" | "outcome" | "timestamp" | "ignore" | "unspecified"
  missingMeaning: "unknown" | "not_observed" | "not_applicable" | "zero" | "false"
  source: FeatureSource
}

type FeatureSource =
  | { kind: "root-value"; field: FieldRef; projection: RootProjection }
  | { kind: "related-value"; route: RouteRef; field: FieldRef; multiple: RelatedMultiplicity }
  | { kind: "related-count"; route: RouteRef; predicate?: ObservationStatusPredicate }
  | { kind: "related-exists"; route: RouteRef; predicate?: ObservationStatusPredicate }

type RelatedMultiplicity =
  | { kind: "require-one" }
  | { kind: "keep-all" }
  | { kind: "first-by-resource-id"; acknowledged: true }

type ObservationStatusPredicate = {
  kind: "observation-status-is"
  code: string
}
```

`FieldRef` and `RouteRef` are catalog identities. The user never types a FHIR path. A design adapter validates these values against the capability snapshot and lowers the four feature variants to the existing V2 compiler.

The receipt echoes the normalized feature, output shape, loss policy, and exact lineage. The UI never reconstructs meaning from physical column names.

One receipt-bound evidence operation grows with the product:

```ts
type DatasetEvidence = {
  receiptId: string
  outputId: string
  snapshotToken: string
  sourceGeneration: string
  complete: boolean
  limits: { rows: number; bytes: number; durationMs: number }
  rows: { total: number; key: string; nullKeys: number; duplicateKeys: number }
  relationships: RelationshipEvidence[]
  features: FeatureEvidence[]
  readiness?: { state: "ready" | "ready_with_warnings" | "blocked"; reasons: ReadinessReason[] }
}
```

F1 adds the evidence identity, limits, completeness state, row-key results, and root-value coverage. F2 adds relationship counts and dropped-value evidence. F3 adds readiness policy, bounded distributions, and repair links. Unknown or incomplete evidence never becomes `ready`.

## The four work packages

### F1: Create a Patient training table

The researcher creates a dataset, selects **Patient, one row for each patient**, and adds **Gender** as a value. The Rows screen shows `2 rows`, `0 null keys`, and `0 duplicate keys`. The Gender feature card shows `50% populated` and an inline sample with `female` and null.

F1 adds `DatasetDesignV1`, the root-value variant, and a one-way adapter to the existing V2 compiler. It adds receipt identity, source generation, measurement limits, completeness, row-key results, and root-value coverage to the receipt-bound evidence operation. It also adds the third hostile-fixture Observation for later packages. It does not add related-feature controls.

The existing Preview, Publish, Viewer, CSV, and reload path must work for the new design. The Rows screen, inline preview, evidence response, and full Preview share one receipt ID. Publish records the revision produced from that receipt. Viewer and CSV use the materialization and selector returned by that publication.

### F2: Turn related records into features

From **Features**, the researcher clicks **Add feature** and chooses one of three cards:

- **A value** selects one field. A related value must use `require-one`, `keep-all`, or acknowledged `first-by-resource-id`.
- **A count** counts related records and may filter by one Observation status.
- **Yes / No** reports whether any related Observation has that status.

The feature card shows an inline sample, coverage, and **How this is made**. That disclosure contains the resource, relationship, exact path, operation, missing meaning, and stable key. An Observation-status feature shows its status code. A future Coding feature must also show its coding system.

Before the user saves a related value, Loom shows the observed `none`, `one`, and `many` counts. `require-one` cannot save while any root has `many`. If later data violates a saved `require-one` invariant, Check blocks publication with `RELATIONSHIP_CARDINALITY_VIOLATION`. `first-by-resource-id` states that it drops values, requires acknowledgement, and sets `lossless=false`. `indexed` is not offered as a solution to multiple related resources because it solves repetition inside one resource.

F2 adds only the remaining closed source variants and an explicit Observation-status predicate. The adapter lowers them to the existing route, projection, `WherePath` and `WhereEquals`, `COUNT`, and `EXISTS` support. It does not add arbitrary equality, a generic Coding predicate, latest-value logic, unit conversion, time windows, or custom expressions.

### F3: Check and repair the dataset

The **Check** screen reports:

- Row count and row-key integrity.
- Feature coverage and missingness.
- Relationship `none`, `one`, and `many` counts.
- Dropped or truncated related values.
- A bounded range or distinct count when the feature type supports it.
- Stable readiness reasons with a direct repair action.

A Check request runs against the validated receipt through the same Preview execution boundary. It does not query a published dataset or a current GraphQL pointer.

A repair action returns to and focuses the exact feature card. Null or duplicate row keys, an identity or schema mismatch, and unacknowledged value loss block publication. Sparse coverage, an unspecified role, and an unknown missing-value meaning produce warnings. Loom records feature role and missing meaning. It does not claim to decide whether a feature is clinically safe.

FHIR explanation is not a separate project. Each feature card, Preview column, Viewer column, and artifact descriptor uses the same versioned feature descriptor and the same null and array display policy.

### F4: Download one reproducible training artifact

The researcher clicks **Publish and export** and downloads `loom-dataset-artifact-v1.zip`. The archive contains exactly:

```text
data.csv
manifest.json
schema.json
provenance.json
quality.json
README.md
```

The server resolves one receipt, revision, output, and materialization before it reads rows. Every file names those identities. `manifest.json` records every SHA-256 checksum. The server exposes the archive only after every member and checksum succeeds.

CSV is the first data member because Loom already has a CSV codec. Parquet is a later format capability, not a hidden requirement for artifact v1. The existing Viewer CSV remains a compatibility action until the server artifact flow replaces all callers.

The live test starts export A, publishes revision B, and proves that every member of A remains pinned to A. A forced later-page failure must not produce an enabled download or a file presented as complete.

## Execution order

The packages run in order: F1, F2, F3, then F4. They share the dataset design, API, and Builder contracts, so the plan has no parallel package branches. Each package lands on `arch/integration` after its focused and live gates pass. The source baseline is the `arch/integration` tip recorded in `BASELINE.json`, not the separate planning branch that stores these documents.

## Screen contract

### Rows

`[data-testid="rows-step"]` asks, "What should one row represent?" `[data-testid="row-grain-patient"]` says, "One row for each patient." `[data-testid="row-key-evidence"]` shows the proposed key, observed row count, null keys, duplicate keys, completeness, and receipt identity.

### Features

`[data-testid="features-step"]` offers **A value**, **A count**, and **Yes / No**. Each `[data-feature-key]` card owns its preview, coverage, lineage disclosure, warning, and edit action. `[data-testid="route-cardinality"]` shows `none`, `one`, and `many` before a related value can save.

### Check

`[data-testid="check-step"]` lists only measured results and concrete actions. Each `[data-reason-code]` action focuses the responsible row or `[data-feature-key]` control. `[data-testid="readiness-state"]` names the receipt that produced the rows and evidence. `[data-testid="publish-dataset"]` is disabled while a blocking reason exists.

### Export

`[data-testid="export-step"]` shows publication identity, artifact progress, row and feature counts, format, and failure state. `[data-testid="artifact-download"]` is enabled only after `[data-testid="artifact-status"]` reports complete. A partial archive never appears as complete.

## Verification

Each issue runs focused Go or UI behavior tests. Each package runs `make dev-doctor && make verify-fast` against the owned warm stack. The driver extends the existing browser journey instead of adding a second test harness.

The integrated tranche runs the Go, OpenAPI, GraphQL, and UI gates once. Run `make verify-full` only when the fixture, watcher, build, or driver mechanics change.

The local loop must assert literal DOM text, API fields, row values, receipt and publication identities, parsed CSV or archive members, and checksums. A screenshot alone is not evidence.

## Migration and compatibility

- New wizard datasets write `datasetDesignVersion: 1` and retain the design digest in their receipts and revisions.
- Existing V2 drafts and published revisions remain readable through the current Builder and Viewer.
- Loom does not infer a design or clinical intent from an existing draft.
- A legacy related `FIRST` column remains available in the legacy flow. It cannot enter or publish through the wizard until the user chooses and acknowledges a supported policy.
- The wizard can ship behind a per-Explorer feature flag during F1.
- Coordinated API changes are allowed, but each package must leave one executable end-to-end path.

## Exclusions

This tranche does not add a generic DSL, formulas, arbitrary predicates, latest-value selection, time-window enforcement, unit normalization, terminology mapping, imputation, encoding, train and test splits, automated clinical leakage decisions, row expansion, or model training.

These are future product decisions. They must enter as named feature templates with their own literal data and acceptance journeys, not as escape hatches around the typed design.
