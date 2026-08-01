# Loom GraphQL API

Loom has three GraphQL surfaces with different responsibilities:

- `/graphql/graph` reads and compiles against the authorized Arango graph.
- `/graphql/dataframe` compiles and executes FHIR dataframe requests against
  the authorized Arango graph.
- `/graphql/flat` reads published dataframe outputs from ClickHouse.

All surfaces are authorization-aware. Clients provide logical names and FHIR
fields; they do not provide AQL, SQL, physical collection names, or physical
ClickHouse tables.

## Local setup

Start the local services and server as described in [QUICKSTART.md](QUICKSTART.md).
The useful development URLs are:

```text
GraphQL graph: http://127.0.0.1:8080/graphql/graph
GraphQL dataframe: http://127.0.0.1:8080/graphql/dataframe
GraphQL flat:  http://127.0.0.1:8080/graphql/flat
Playground:    http://127.0.0.1:8080/graphql/graph
Apollo:        http://127.0.0.1:8080/apollo
```

The examples below show GraphQL documents and variables separately. They can be
pasted into Apollo Sandbox or sent as a normal GraphQL HTTP POST.

## Graph API

The graph endpoint is the Arango/compiler surface for explicit graph traversal,
typed FHIR reads, discovery, and recipe control operations.

### Discover populated fields and routes

Use builder introspection before constructing a dataframe query. It reports
populated field references and generated traversal routes for the selected
project and root resource.

```graphql
query Builder($input: DataframeBuilderIntrospectionInput!) {
  dataframeBuilderIntrospection(input: $input) {
    project
    rootResourceType
    authResourcePaths
    root {
      resourceType
      fields { fieldRef label path }
      traversals { fromType label toType edgeCount }
    }
    relatedResources {
      viaLabel
      edgeCount
      target { resourceType fields { fieldRef label path } }
    }
  }
}
```

```json
{
  "input": {
    "project": "ARANGODB_PROTO",
    "rootResourceType": "Patient"
  }
}
```

Field references are resolved against the active READY generation and the
caller's authorization scope. Do not assume that a syntactically valid FHIR
field is populated in every project.

### FHIR dataframe compilation

Send `runFhirDataframe` to `/graphql/dataframe`. It compiles a FHIR-shaped
dataframe request and executes it as one scoped Arango query. It is a mutation
for historical/API compatibility; the operation is still a read.

```graphql
mutation RunDataframe($input: FhirDataframeInput!, $limit: Int) {
  runFhirDataframe(input: $input, limit: $limit) {
    columns
    rows
    rowCount
    diagnostics { compilationMs arangoQueryMs totalMs }
  }
}
```

```json
{
  "limit": 100,
  "input": {
    "project": "ARANGODB_PROTO",
    "rootResourceType": "Patient",
    "rootFields": [
      {
        "name": "patient_id",
        "fieldRef": "Patient.id",
        "valueMode": "AUTO"
      },
      {
        "name": "gender",
        "fieldRef": "Patient.gender",
        "valueMode": "AUTO"
      }
    ],
    "rootFilters": [
      {
        "select": "gender",
        "operator": "EQUALS",
        "values": [{ "kind": "STRING", "string": "female" }]
      }
    ],
    "traverse": [{
      "edgeLabel": "subject_Patient",
      "toResourceType": "Condition",
      "alias": "condition",
      "matchMode": "OPTIONAL",
      "aggregates": [{ "name": "condition_count", "operation": "COUNT" }]
    }]
  }
}
```

The dataframe input supports fields, filters, pivots, aggregates, slices,
catalog projections, and recursive traversal shaping. Its traversal default is
`OPTIONAL`; this is intentionally different from graph-path mode.

### Typed FHIR reads

The graph endpoint also exposes generated resource roots for the supported FHIR
types. These are convenient for resource-shaped reads, not graph path queries:

Use these roots only at `/graphql/graph`; the `/graphql/flat` endpoint exposes
the ClickHouse dataframe API and does not define `Patient`, `Observation`, or
other FHIR resource roots.

```graphql
query Patients($project: String!, $filters: [FhirFilterInput!]) {
  Patient(project: $project, filters: $filters, limit: 25) {
    id
    resourceType
    gender
    birthDate
  }
}
```

```json
{
  "project": "ARANGODB_PROTO",
  "filters": [{
    "select": "gender",
    "operator": "EQUALS",
    "values": [{ "kind": "STRING", "string": "female" }]
  }]
}
```

Typed reads use the same filter contract as dataframe and graph reads, but
return generated FHIR structs. `limit` defaults to 25. Read-only callers may
request at most 10,000 resources; callers with write access to the selected
project may request any positive GraphQL `Int` limit. Nested
`Reference.resource` fields perform separate authorized lookups; use
`fhirGraph` when you need one compiled query with explicit relationship paths.

### Explicit graph paths

`fhirGraph` returns complete FHIR resources organized into node paths. It is
the right operation when the caller wants relationships and traversal prefixes,
not flattened dataframe columns.

```graphql
query Graph($input: FhirGraphQueryInput!) {
  fhirGraph(input: $input) {
    sourceGeneration
    returnedCount
    pageInfo { hasMore }
    paths {
      terminalAlias
      nodes { alias resourceType id resource }
      relationships {
        alias label fromResourceType toResourceType
      }
    }
  }
}
```

```json
{
  "input": {
    "project": "ARANGODB_PROTO",
    "rootResourceType": "Patient",
    "rootFilters": [{
      "select": "gender",
      "operator": "EQUALS",
      "values": [{ "kind": "STRING", "string": "female" }]
    }],
    "traverse": [{
      "edgeLabel": "subject_Patient",
      "toResourceType": "Specimen",
      "alias": "specimen",
      "matchMode": "REQUIRED",
      "filters": [{
        "select": "status",
        "operator": "EQUALS",
        "values": [{ "kind": "STRING", "string": "available" }]
      }],
      "traverse": [{
        "edgeLabel": "subject_Specimen",
        "toResourceType": "DocumentReference",
        "alias": "document",
        "matchMode": "OPTIONAL",
        "filters": [{
          "select": "status",
          "operator": "EXISTS"
        }]
      }]
    }],
    "limit": 100
  }
}
```

Graph mode rules:

- Traversals default to `REQUIRED`; the dataframe API defaults to `OPTIONAL`.
- Each matched prefix is returned. A two-hop match can produce both the
  `Patient -> Specimen` path and the `Patient -> Specimen -> DocumentReference`
  path.
- Sibling traversals form independent path unions; they do not form a
  Cartesian product.
- An optional miss contributes no synthetic/null node and does not disqualify
  the root.
- The final limit is applied after path construction, union, semantic
  deduplication, and deterministic sorting. Loom fetches one extra row to set
  `pageInfo.hasMore`.
- The current bounds are maximum depth 4, maximum 32 traversal declarations,
  and maximum limit 10,000.
- Returned resources are sanitized FHIR JSON. Storage keys, edge documents,
  project metadata, generation metadata, and authorization metadata are not
  exposed.

### Graph filters

`rootFilters` filters the root resource. Each traversal's `filters` filters the
target node in that traversal, including recursively nested traversals.

Supported operators are:

| Operator | Values | Notes |
| --- | --- | --- |
| `EQUALS` | one | Exact typed comparison. |
| `NOT_EQUALS` | one | Exact typed non-equality. |
| `IN` | one or more | Membership in a bound value list. |
| `EXISTS` | none | At least one selected value exists. |
| `MISSING` | none | No selected value exists. |
| `CONTAINS_TEXT` | one string | Case-sensitive AQL text containment. |
| `GT`, `GTE`, `LT`, `LTE` | one | Numeric or FHIR date/date-time ordering. |

Supported value kinds are `STRING`, `CODE`, `BOOLEAN`, `INTEGER`, `DECIMAL`,
`DATE`, and `DATE_TIME`. Omit `quantifier` for scalar selectors; repeated
selectors must specify `ANY`, `ALL`, or `NONE`.
Filter lists are combined with `AND`; explicit boolean filter trees are not
part of the current GraphQL contract.

Filters must resolve to generated FHIR scalar fields. Arbitrary object
selection, edge-property filtering, parent/child field comparisons, and
code-system/display pairing are not currently supported.

Every filter supplies the required `select` member. You may also supply
`fieldRef` to resolve a catalog-backed field reference; after resolution the
compiler uses the resulting selector. The active generation and authorization
scope are resolved server-side. `authResourcePaths` can narrow the caller's
scope but cannot widen it.

### Explain graph compilation

```graphql
query ExplainGraph($input: FhirGraphQueryInput!, $live: Boolean!) {
  explainFhirGraph(input: $input, live: $live) {
    sourceGeneration
    rootResourceType
    traversalCount
    maxDepth
    limit
    live
  }
}
```

`live: false` performs semantic and physical validation without executing the
query. `live: true` issues an Arango `EXPLAIN` request only; it does not open a
result cursor. Raw AQL, bind values, collections, and scope paths are not
returned through this API.

## Flat API

The flat endpoint reads only published, READY dataframe outputs from ClickHouse.
The client supplies the logical `dataType`; Loom resolves the authorized
project set and current READY publication from the catalog.

### Discover datasets and columns

```graphql
query Datasets {
  dataframeDatasets {
    id
    name
    revision
    state
    rowCount
    columns {
      name
      clickhouseType
      logicalType
      nullable
      repeated
      filterable
      sortable
      aggregatable
    }
  }
}
```

For one logical dataset:

```graphql
query Dataset($input: DataframeDatasetInput!) {
  dataframeDataset(input: $input) {
    name
    revision
    rowCount
    columns { name logicalType }
  }
}
```

```json
{ "input": { "dataType": "cases" } }
```

Use the returned column metadata to choose valid projection, filter, sort, and
aggregation fields. A logical `dataType` is not a physical table name.

### Read rows

```graphql
query Rows($input: DataframeRowsInput!) {
  dataframeRows(input: $input) {
    materialization { name revision rowCount }
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
    "dataType": "cases",
    "columns": ["case_id", "status", "amount"],
    "filters": [
      { "column": "status", "op": "EQ", "value": "open" },
      { "column": "amount", "op": "GTE", "value": 100 }
    ],
    "sort": { "column": "case_id", "desc": false },
    "first": 100
  }
}
```

`first` is bounded by the server's configured maximum. If `hasNextPage` is
true, pass `pageInfo.endCursor` as `after` in the next request. Cursor values
are opaque and must not be decoded or modified by clients. Rows are JSON and
the returned `columns` array gives their selected column order.

Flat row filters are SQL/ClickHouse column filters, not FHIR selectors. The
currently supported operations are:

| Operation | Meaning |
| --- | --- |
| `EQ`, `NEQ` | Equality or inequality. |
| `IN`, `NOT_IN` | Membership; pass a JSON array value. |
| `LT`, `LTE`, `GT`, `GTE` | Ordered comparison. |
| `CONTAINS` | Case-insensitive text containment. |
| `STARTS_WITH` | Text prefix match. |
| `EXISTS` | Non-null value. |
| `IS_NULL` | Null value. |
| `ARRAY_CONTAINS` | Array contains the supplied value. |
| `ARRAY_OVERLAPS` | Array overlaps the supplied JSON array. |

Filters are combined with `AND`. Columns must be present in the published
schema, and sort/filter/aggregation capability metadata is enforced before
ClickHouse execution.

### Aggregates and batch aggregations

Single aggregate:

```graphql
query Aggregate($input: DataframeAggregateInput!) {
  dataframeAggregate(input: $input) {
    materialization { name revision }
    columns
    rows
  }
}
```

```json
{
  "input": {
    "dataType": "cases",
    "groupBy": ["status"],
    "filters": [{ "column": "amount", "op": "GTE", "value": 100 }],
    "operation": "COUNT",
    "column": "case_id"
  }
}
```

Supported aggregate operations are `COUNT`, `COUNT_DISTINCT`, `SUM`, `AVG`,
`MIN`, and `MAX`. `dataframeAggregations` additionally supports named
`TERMS`, `HISTOGRAM`, and `DATE_HISTOGRAM` specifications.

## Authorization and generations

Graph reads resolve the active READY dataset generation for the requested
project. Flat reads resolve authorized current READY publications for the
logical `dataType`. Clients should not send generation IDs or physical table
names as query selectors.

`authResourcePaths` on graph requests is a narrowing request. It cannot widen
the principal's authorized scope. Flat reads apply authorization to each
published source automatically, including federated reads across projects.

## Current boundaries

The APIs intentionally do not expose arbitrary AQL or SQL, shortest-path/BFS/
DFS traversal, variable-depth graph search, custom edge queries, recursive flat
filter trees, or arbitrary sorting over graph paths. Those features require
additional semantic and authorization contracts before they should be added.
