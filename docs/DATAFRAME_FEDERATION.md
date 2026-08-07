# Published Dataframe Federation

This guide describes how Loom turns independently published project dataframes
into one authorized logical dataframe. It covers the publication catalog,
exact selectors, active generations, authorization, schema reconciliation, and
the GraphQL reader surface.

The federation layer is a read boundary. It does not compile recipes or expose
arbitrary ClickHouse tables. Recipe compilation and publication create durable
project-local outputs; federation selects compatible outputs and presents them
as one logical dataset.

## The execution path

```text
GraphQL request
  -> exact dataframe selector
  -> authorized project candidates
  -> project_id filter pushdown
  -> active generation per project
  -> READY/PUBLISHED pointer-backed execution
  -> source-level authorization scope
  -> schema reconciliation
  -> authorized ClickHouse UNION ALL
  -> rows, aggregates, or streaming export
```

The browser never supplies a project, generation, materialization table, or
SQL table name. Loom derives those values from the authenticated principal and
the publication catalog.

## Exact dataframe identity

Every published output is identified by three required values:

```text
(recipe, translationVersion, output)
```

For example:

```text
(documents, v2, DocumentReference)
```

The selector is shared by recipe publication, project releases, federation,
GraphQL, and ETL. A name-only lookup is not sufficient because two recipes can
produce the same output name and one recipe can have multiple versions.

Use the exact selector in new GraphQL requests:

```graphql
query Rows($input: DataframeRowsInput!) {
  dataframeRows(input: $input) {
    materialization {
      selector { recipe translationVersion output }
      availability
      completeness
      includedProjectCount
      expectedProjectCount
      projectStatuses {
        project
        state
        generation
        executionId
        errorCode
        retryable
      }
    }
    columns
    rows
    totalCount
    pageInfo { hasNextPage endCursor }
  }
}
```

```json
{
  "input": {
    "selector": {
      "recipe": "documents",
      "translationVersion": "v2",
      "output": "DocumentReference"
    },
    "first": 100
  }
}
```

The deprecated `dataType` input remains available during migration. It is
resolved through `LOOM_DEFAULT_RECIPE` and
`LOOM_DEFAULT_TRANSLATION_VERSION`; clients must not provide `selector` and
`dataType` together. New clients should use selectors explicitly.

## How sources are selected

For each request, the materialization service performs these steps:

1. Resolve the principal's candidate projects. If project claims are present,
   those claims are authoritative; otherwise Loom discovers projects with
   current published outputs.
2. Apply an equality or `IN` filter on `project_id` before source discovery.
   This avoids reading or reconciling projects the caller did not request.
3. Resolve each project's active dataset generation when an active-generation
   resolver is configured.
4. List successful publication executions and resolve the pointer for each
   `(project, generation, recipe, translationVersion)` identity.
5. Keep only the execution whose pointer still names that execution and whose
   exact selector matches the request.
6. Resolve the caller's read scope for every remaining project and generation.
   Sources with no authorized scope are excluded.
7. Reconcile the authorized sources into a `FederatedDataset`.

The pointer check is important: a completed physical table is not automatically
visible. It becomes readable only through the catalog pointer selected for its
logical identity.

## Publication and visibility

Publication is a durable multi-output bundle. New workflows use these states:

```text
QUEUED -> RUNNING -> VALIDATING -> PUBLISHED
                                  \-> FAILED
```

Physical ClickHouse tables are written and verified before the catalog pointer
is advanced. A reader therefore sees either the prior pointer or the new
verified publication, not a partially written bundle. Legacy stored `READY`
values remain readable and are interpreted as successful publications; new
workflows do not write `READY`.

Project releases add another visibility boundary. A staged generation and its
verified exact-selector publications become active together through the active
release pointer. A newer generation or publication does not become visible
merely because it exists.

## Authorization and tenancy

Each published source carries project identity and may carry
`auth_resource_path` values. Federation enforces both dimensions:

- `project_id` identifies which project contributed a row and supports source
  selection and filtering.
- `auth_resource_path` is an authorization predicate derived from the server's
  scope resolver or principal, never from an arbitrary client filter.
- An unrestricted scope reads the complete source.
- A path-limited scope reads only matching rows and marks the dataset's row
  count as potentially incomplete.
- A source with no effective authorized paths is excluded.

`project_id` is synthesized from catalog metadata for legacy tables that do not
physically contain the column. This prevents a legacy table from claiming a
different project identity.

## Schema reconciliation

Federation reconciles source schemas before issuing the union query.

- Columns present in only some sources become nullable in the logical schema.
- Missing scalar values are returned as `NULL`.
- Missing array values are returned as an empty array of the reconciled type.
- A source with an invalid table, invalid column metadata, or duplicate column
  names is excluded with `RECIPE_CONTRACT_VIOLATION`.
- Incompatible types for the same logical column fail the request with
  `FEDERATION_INCOMPATIBLE` and include the conflicting column, projects, and
  types in structured details.
- Internal columns such as `__loom_row_id` and `auth_resource_path` are not
  exposed as ordinary client columns.

The resulting metadata includes a revision digest derived from the selected
source identities, physical tables, and reconciled columns. It is used for
cursor and read consistency decisions.

## Availability and project status

The materialization metadata reports:

| Availability | Meaning |
| --- | --- |
| `AVAILABLE` | All expected authorized projects have current usable sources. |
| `DEGRADED` | At least one expected project is missing, stale, failed, building, or excluded, but usable sources remain. |
| `UNAVAILABLE` | No usable authorized source exists. |

Project status values are `CURRENT`, `STALE`, `BUILDING`, `FAILED`, `MISSING`,
and `EXCLUDED`. Statuses are filtered to the caller's authorized projects;
unauthorized project identities are never disclosed.

Degraded federation is intentional. One project with a failed publication
should not hide valid data from other authorized projects, but clients should
surface the status metadata when presenting results.

## Read and export surfaces

The same federated source set is used by:

- `dataframeDataset` and `dataframeDatasets` for discovery and metadata;
- `dataframeRows` for cursor-paginated rows;
- `dataframeAggregate` and `dataframeAggregations` for aggregates;
- the dataframe export route for CSV, TSV, JSON, and JSONL streaming.

The legacy `materializationId` input remains a direct single-publication path
for compatibility. It is not the federation path and should not be used for
new multi-project clients.

## Operational setup

For a project to contribute to a federated dataframe, all of the following
must be true:

1. The project has an active or otherwise eligible immutable dataset generation.
2. The exact recipe/version/output has been materialized for that generation.
3. The publication completed successfully and its output was verified.
4. The catalog pointer names that execution.
5. The caller has an authorized project and resource-path scope.
6. The source schema can be reconciled with the other selected projects.

For a new recipe version, publish and verify it first, then promote or select
that exact version. Do not rely on “latest” behavior to switch a production
reader between versions.

Recommended configuration for legacy callers:

```text
LOOM_DEFAULT_RECIPE=documents
LOOM_DEFAULT_TRANSLATION_VERSION=v2
```

Recommended configuration for release verification is a list of exact
selectors, not output names alone. See
`LOOM_REQUIRED_DATAFRAME_SELECTORS` and the release contract documentation.

## Troubleshooting

| Symptom | Likely cause |
| --- | --- |
| `INVALID_SELECTOR` | Selector is incomplete, both selector and `dataType` were supplied, or legacy defaults are not configured. |
| `UNAVAILABLE` / `DATASET_NOT_FOUND` | No pointer-backed exact publication is active or authorized. |
| `DEGRADED` | One or more expected projects are missing, stale, failed, building, or excluded. |
| `FEDERATION_INCOMPATIBLE` | Same logical column has incompatible ClickHouse types across selected sources. |
| `RECIPE_CONTRACT_VIOLATION` | A publication has invalid physical table or column metadata. |
| Incomplete `totalCount` | The caller has path-limited authorization, so count completeness is not guaranteed. |

Structured errors include stable codes and explicit retryability. Clients
should branch on the error code rather than parsing human-readable messages.

## Code map

| Responsibility | Implementation |
| --- | --- |
| Exact selector identity | `internal/dataset/dataframe_selector.go` |
| Publication bundle and pointer identity | `internal/dataframe/publication/bundle.go` |
| Durable ClickHouse publication | `internal/dataframe/publication/clickhouse/` |
| Source selection and schema reconciliation | `internal/dataframe/published/federation.go` |
| Single-source compatibility reads | `internal/dataframe/published/read.go` |
| GraphQL authorization and transport mapping | `internal/api/graphql/graph/materialization/` |
| Public GraphQL schema | `internal/api/graphql/graph/schema/schema.graphqls` |
| Immutable generations and release activation | `internal/dataset/` |
| Stable error taxonomy | `internal/dataframe/errors/errors.go` |

The broader identity, state, migration, and configuration contract is in
[`loom-reliability-contract.md`](loom-reliability-contract.md).
