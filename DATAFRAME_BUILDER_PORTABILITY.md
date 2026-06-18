# Dataframe Builder Portability Guide

This document captures the current ArangoDB prototype in implementation-neutral
terms so the same workload can be rebuilt against another database and benchmarked
fairly. The goal is not "rewrite the same code in another syntax." The goal is
"preserve the same semantics, metadata surfaces, and benchmark workloads."

## Why This Exists

The prototype has now proven two separate things:

1. FHIR resources can be loaded into a graph/document database and used to build
   realistic dataframe-style exports.
2. A frontend dataframe builder can be supported if the backend exposes:
   - observed graph connectivity
   - observed populated fields
   - simple semantic pivot hints

That means future work is no longer blocked on "can this be done?" The next
question is "how does this same workload behave in another database?"

This guide defines the workload precisely enough to implement it elsewhere.

## Core Problem Statement

The system ingests FHIR NDJSON, preserves raw payloads, materializes graph edges
from FHIR references, and supports building cohort/dataframe queries over the
graph.

The frontend builder is expected to let a user:

1. Choose a root resource type.
2. Inspect which traversals are observed from that node type.
3. Inspect which fields are actually populated on each node type.
4. Select fields to project into a dataframe.
5. Optionally use simple pivot hints for FHIR-coded fields such as
   `CodeableConcept`.

The builder does not need perfect semantic safety in v1. It is acceptable for a
user to build a query that returns zero rows. The metadata layer exists to make
useful queries discoverable, not to fully prevent invalid ones.

## Current Arango Model

### Vertex Model

Each FHIR resource is stored as a document in a collection named by resource type,
for example:

- `Patient`
- `Condition`
- `DocumentReference`
- `Observation`

Each stored vertex document preserves:

- `_key`
- `id`
- `project`
- `resourceType`
- `payload`
- optional `auth_resource_path`

`payload` is the original FHIR JSON document.

### Edge Model

FHIR references are extracted into `fhir_edge`. Each edge stores:

- `_key`
- `_from`
- `_to`
- `label`
- `project`
- `from_type`
- `to_type`

These edges are reference-derived, not user-authored business edges.

### Metadata Model

Two metadata surfaces exist for the frontend:

1. `discover-populated-references`
   - reads from `fhir_edge`
   - returns observed `(from_type, label, to_type, edge_count)`

2. `fhir_field_catalog` + `discover-populated-fields`
   - built during load
   - stores observed populated field paths for each resource type
   - stores counts and simple pivot hints

## Required Backend Capabilities In Any Target Database

An alternative database implementation should provide the following capabilities.

### 1. Raw Payload Preservation

The system must preserve the original FHIR payload for every resource.

Required semantics:

- one stored resource row/document/vertex per source FHIR resource
- raw payload retrievable later
- project scoping on every stored resource

### 2. Reference Materialization

FHIR references must be materialized into a traversable structure.

This may be:

- graph edges
- relation rows
- adjacency tables
- link collections

But it must preserve:

- source type
- target type
- label / reference role
- project scope

### 3. Project-Scoped Traversal Discovery

The backend must support a fast query that answers:

"What observed references exist from each node type in this project?"

Canonical output shape:

```json
{
  "from_type": "Observation",
  "label": "subject_Patient",
  "to_type": "Patient",
  "edge_count": 12345
}
```

This powers the builder traversal picker.

### 4. Populated Field Catalog

The backend must produce a catalog of observed populated fields per resource type.

Canonical output shape:

```json
{
  "project": "ARANGODB_PROTO",
  "resource_type": "Observation",
  "path": "valueCodeableConcept",
  "kind": "codeable_concept",
  "doc_count": 50270,
  "sample_count": 12,
  "distinct_values": ["M0", "N1", "Stage IVA"],
  "distinct_truncated": false,
  "pivot_candidate": true,
  "pivot_kind": "codeable_concept_display_value",
  "pivot_columns": [
    "American Joint Committee on Cancer pM0",
    "American Joint Committee on Cancer pN1"
  ]
}
```

This powers the field picker.

### 5. Dataframe Query Execution

The backend must support compiled dataframe queries that:

- start from a root node type
- traverse observed reference connections
- project fields from multiple reached node types
- optionally flatten or aggregate arrays
- optionally pivot simple coded structures

The frontend should not be required to author final raw query text. A structured
query spec compiled server-side is preferable, but the same semantics must be
preserved if another backend allows direct query generation.

## Canonical Use Cases

These are the benchmarked use cases that any new backend should implement.

### Use Case 1: Traversal Discovery

Question:

"Show all observed references in the graph for a project."

Expected output:

- one row per observed `(from_type, label, to_type)`
- include `edge_count`

Purpose:

- frontend graph/traversal browser
- graph sanity inspection

### Use Case 2: Field Discovery

Question:

"Show all populated fields on a node type in this dataset."

Expected output:

- one row per canonical path
- population count by document
- capped distinct values
- optional pivot hint metadata

Purpose:

- frontend field picker
- avoid exposing schema-only dead fields

### Use Case 3: Pivot Candidate Discovery

Question:

"Which fields on this resource type behave like codeable concepts and can be used
as simple pivots?"

Expected output:

- subset of field catalog where `pivot_candidate=true`
- include candidate columns derived from distinct displays/text

Purpose:

- frontend mini-table/pivot builder

### Use Case 4: Case-First Dataframe

Question:

"For each case/patient, what downstream samples, files, and assay classes exist?"

Current implementation name:

- `gdc_case_assay_matrix`

Expected semantics:

- root at `Patient`
- traverse to samples/files/study/treatment/condition context
- return one row per patient/case
- summarize assay availability and representative downstream files

Purpose:

- cohort discovery
- case-centric export

### Use Case 5: File-First Dataframe

Question:

"For each file, what specimen, patient, diagnosis, and study context does it
belong to?"

Current implementation name:

- `gdc_file_sample_matrix`

Expected semantics:

- root at `DocumentReference`
- traverse to sample/group/patient/condition/study/treatment
- return one row per file or file/sample grouping

Purpose:

- file-centric export
- Elasticsearch streaming/export benchmark

## Field Catalog Semantics

### Canonical Path Format

Observed field paths use bracket wildcards for arrays:

- `identifier[].value`
- `code.coding[].display`
- `valueCodeableConcept.text`

Do not use numeric array indexes in the catalog. They fragment the metadata and
make the frontend unusable.

### Count Semantics

Primary count is:

- `doc_count = number of documents containing the path`

This is not total raw occurrences across arrays. Document-level presence is the
useful signal for UI field selection.

### Distinct Value Semantics

Distinct values are:

- exact up to a fixed cap
- truncated after the cap is reached
- stored as user-facing samples, not as a lossless full-value index

Current implementation assumptions:

- cap at 50 values
- flag truncation via `distinct_truncated`

### Shape Classification

Current v1 field kinds:

- `scalar`
- `object`
- `array`
- `codeable_concept`
- `coding`

These are not general-purpose schema types. They are builder-facing hints.

## Pivot Semantics

### What "Pivot" Means Here

The current intent is not SQL-style arbitrary pivoting. It is:

- a coded FHIR field often contains a stable display name and a changing value
- the stable display can become a candidate column name
- the changing part is the informative cell value

This matters especially for observation-like resources and GDC-coded staging and
classification patterns.

### Current V1 Scope

V1 only recognizes `CodeableConcept` as a semantic pivot candidate.

Signals used:

- `text`
- `coding[].display`
- `coding[].code`
- `coding[].system`

Current metadata output does not fully compile pivot queries automatically. It
provides enough signal for the frontend and later backend query compiler to know
that a field is pivotable.

### What Another Backend Must Preserve

An alternative implementation must at least preserve:

- detection of `CodeableConcept`-shaped fields
- capped distinct display/text values
- a frontend-readable pivot hint

The exact internal algorithm can vary.

## Performance Constraints

### Load-Time Metadata Requirement

Field profiling is intentionally done during load because:

- the payload is already in memory or already being decoded
- a second full scan is avoidable
- the frontend metadata becomes available immediately after load

Alternative backends may choose a separate profiling job, but that should be
measured as a separate cost.

### Repeated-Shape Optimization

FHIR records often repeat the same structural shape many times.

The current implementation optimizes for this by:

- computing a structure-only shape fingerprint
- caching a traversal plan per resource type and shape
- reusing the plan for subsequent documents with the same shape

This is a core part of the benchmark workload. Another backend should attempt an
equivalent optimization if the implementation language allows it.

### What Should Be Timed

For fair comparison, measure:

1. raw load time
2. edge/reference materialization time
3. field catalog generation time
4. traversal discovery query time
5. field discovery query time
6. case-first dataframe export time
7. file-first dataframe export time
8. time-to-first-row for large exports

### What Should Be Held Constant

When comparing databases:

- same reduced dataset and same full dataset
- same project scoping
- same semantic output shape
- same distinct-value cap
- same pivot scope (`CodeableConcept` only in v1)
- same row count expectations for dataframe queries

## Fair Cross-Database Benchmark Checklist

When porting this to another database, confirm the following before comparing
numbers:

- raw payload is still preserved
- reference edges/relations preserve `from_type`, `to_type`, `label`, `project`
- traversal discovery returns the same observed connection set
- field discovery returns the same canonical paths
- `doc_count` means document presence, not array occurrence count
- distinct-value cap matches
- pivot metadata is scoped to `CodeableConcept` only
- case-first and file-first query outputs are semantically equivalent

If any of these differ, the benchmark is not apples-to-apples.

## Suggested Translation Targets

When asking for a port to another database, the clean framing is:

"Reimplement the workload defined in `DATAFRAME_BUILDER_PORTABILITY.md` using
<target query language / data model>, preserving the same semantics and
benchmark surfaces."

That request should explicitly include:

- target database
- target query language
- whether raw payload is stored as JSON, typed rows, or hybrid
- whether graph edges are native graph edges or explicit relation rows
- whether metadata is computed during load or in a post-load job

## Current Commands In This Repo

These are the current Arango-facing entrypoints that define the workload.

### Load

```bash
rtk ./arango-fhir-proto load \
  --meta-dir ./META \
  --database fhir_proto \
  --batch-size 5000 \
  --progress-every 50000 \
  --auth-resource-path EllrottLab-GDC_Data
```

### Traversal Discovery

```bash
rtk ./arango-fhir-proto discover-populated-references \
  --database fhir_proto \
  --project ARANGODB_PROTO
```

### Field Discovery

```bash
rtk ./arango-fhir-proto discover-populated-fields \
  --database fhir_proto \
  --project ARANGODB_PROTO \
  --resource-type Observation
```

### Pivot-Focused Field Discovery

```bash
rtk ./arango-fhir-proto discover-populated-fields \
  --database fhir_proto \
  --project ARANGODB_PROTO \
  --resource-type Observation \
  --pivot-only
```

### Case-First Export

```bash
rtk ./arango-fhir-proto export-gdc-case-assay-matrix \
  --database fhir_proto \
  --project ARANGODB_PROTO \
  --index gdc_case_assay_matrix \
  --cursor-batch-size 5000 \
  --progress-every 5000 \
  --output ./TEST.ndjson
```

## What Is Out Of Scope For V1

These should not be silently added when porting:

- automatic prevention of zero-row user queries
- append-aware field catalog merging
- full distinct value persistence
- generalized pivoting for every nested FHIR structure
- schema-only field catalogs with no observed-data profiling
- arbitrary query planner intelligence in the frontend

Those are separate benchmark phases.
