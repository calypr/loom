# Developer Architecture

Loom is an Arango-backed compiler for flat, exportable FHIR dataframes. The
authority for FHIR structure is the checked-in
[`schemas/graph-fhir.json`](../schemas/graph-fhir.json) graph schema and the
generated Go metadata derived from it. Code should not add a second handwritten
FHIR model or a parallel AQL implementation.

## Runtime surfaces

`cmd/arango-fhir-proto` is the operator CLI. Its supported commands are:

- `load` for the temporary mutable compatibility load;
- `load-generation` for a complete immutable dataset generation;
- `discover-populated-references` and `discover-populated-fields` for
  catalog diagnostics.

`cmd/arango-fhir-server` owns the HTTP process. It mounts health, GraphQL, and
developer GraphQL tools. In `--dataset-generations` mode it resolves one active
READY generation and rejects the legacy one-file HTTP import endpoint.

The GraphQL dataframe mutation is the live compiler transport. Do not add a
second query compiler or hand-maintained AQL path behind another endpoint.

The HTTP API names its backend boundaries explicitly. `/graphql/graph` is the
Arango graph/control-plane GraphQL endpoint, `/graphql/dataframe` is the
Arango-backed FHIR dataframe compiler endpoint, and `/graphql/flat` is the
dedicated published ClickHouse dataframe reader. Published ClickHouse dataframe discovery and reads follow the stable-GraphQL,
dynamic-data contract defined in
[`CLICKHOUSE_GRAPHQL_READER_EXECUTION_PLAN.md`](CLICKHOUSE_GRAPHQL_READER_EXECUTION_PLAN.md).
Only registered READY publication outputs are exposed; adding a dataset or
column must not require GraphQL regeneration or a Loom restart. Publication
and published-data reads are ClickHouse-only; Loom has no Elasticsearch
publication or reader fallback.

## Load and storage model

`internal/ingest` reads an NDJSON directory, preflights it against the local
schema, builds vertices and `fhir_edge` documents, profiles populated fields,
and writes batches to Arango. Generated loaders are the fast path for covered
resources; the schema-backed generic loader is the fallback for the other
active graph roots.

Fresh loads bootstrap:

- one document collection per loaded FHIR resource type;
- `fhir_edge`, the only stored relationship representation;
- `fhir_field_catalog`, the evidence source for available fields, pivots, and
  relationships;
- `loom_dataset_lifecycle` in immutable-generation mode.

There is no maintained `patient_file_rollup`, scalar-index collection, or
alternate relationship collection. A future materialization must be introduced
as a compiler-selected physical optimization with write/read ownership,
generation scope, freshness policy, and Explain coverage; it must not be added
as an unused bootstrap collection.

Immutable loads use `internal/dataset`, `internal/dataset/arango`, and
`internal/graphschema`. Their manifest records project, generation, and
schema identity; the active pointer selects one READY generation per project.
The generation-qualified physical keys and mandatory generation predicates are
part of the query correctness contract, not an optional filter.

## Compiler path

The current runtime call path is:

```text
GraphQL request
  -> graphqlapi resolver
  -> dataframebuilder.Service
  -> dataframe/runtime.Service
  -> dataframe/spec request contracts
  -> dataframe/semantic logical plan
  -> dataframe/compiler/ir typed physical plan
  -> dataframe/compiler/lower FHIR storage lowering
  -> dataframe/compiler/optimize IR rewrites
  -> dataframe/compiler/render/aql parameterized AQL
  -> Arango query execution/streaming
```

Runtime preparation and execution live in `internal/dataframe/runtime`;
compiler orchestration lives in `internal/dataframe/compiler`; structured transport
errors live in `internal/dataframe/errors`; and guided templates live in
`internal/dataframe/template`. `internal/catalog` owns scoped observed-field
and relationship facts. `fhirschema` owns generated structural metadata. These
boundaries matter: catalog observations constrain what is populated, while
schema metadata constrains what a request means.

Generic lowering is the default direction. It accepts generated reverse/builder
relationship routes proven to correspond to the physical `INBOUND` `fhir_edge`
layout, plus the explicitly proven `ResearchSubject --study--> ResearchStudy`
`OUTBOUND` route. A schema-valid forward FHIR reference alone is still not
sufficient proof; every other forward route remains rejected until it has a
verified storage contract.

The compiler package orchestrates independent `spec`, `semantic`, `compiler/ir`,
`compiler/lower`, `compiler/optimize`, and `compiler/render/aql` packages.
`spec` owns request contracts, `semantic` owns backend-independent meaning,
`ir` owns typed physical operations and scope proofs, `lower` owns FHIR route
and endpoint decisions, `optimize` owns semantics-preserving IR rewrites, and
`render/aql` owns serialization only. Runtime code may call the compiler, but
no compiler child may import runtime, catalog, or HTTP/GraphQL transport code.

When adding code, use this lookup table:

| Change | Owner |
| --- | --- |
| New request/filter/selector contract | `internal/dataframe/spec` |
| FHIR schema meaning or logical selection | `internal/dataframe/semantic` |
| Physical operation or scope invariant | `internal/dataframe/compiler/ir` |
| FHIR edge route or endpoint lowering | `internal/dataframe/compiler/lower` |
| Cost-gated physical rewrite | `internal/dataframe/compiler/optimize` |
| AQL text or bind emission | `internal/dataframe/compiler/render/aql` |
| Catalog, auth, generation, cursor, or profiling | `internal/dataframe/runtime` |

For the complete ownership map and move history, see
[`DATAFRAME_PACKAGE_REORGANIZATION_PLAN.md`](DATAFRAME_PACKAGE_REORGANIZATION_PLAN.md)
and the current compiler split plan
[`DATAFRAME_PACKAGE_REORGANIZATION_ROUND_2.md`](DATAFRAME_PACKAGE_REORGANIZATION_ROUND_2.md).

## Compatibility tracks and removal order

The following compatibility tracks remain deliberately, but should not grow:

- mutable CLI `load` and `POST /api/v1/imports`, until a complete
  generation-aware upload/job flow replaces them;
- raw-structure GraphQL dataframe input, until a deliberately designed guided
  transport exists.

The former hard-coded GDC AQL files, GDC export command, browser `/builder`
demo, and unowned bootstrap materializations were removed. They bypassed the
compiler and did not work with immutable generation keys.

## Generated code and tests

Run `make generate-fhir` after changing the graph schema and
`make generate-graphql` after changing the GraphQL schema. Do not hand-edit
generated `fhirstructs`, compiler metadata, or gqlgen output. The gqlgen
executable output uses follow-schema layout: `graphqlapi/schema.generated.go`,
`graphqlapi/fhir_schema.generated.go`, `graphqlapi/root_.generated.go`, and
`graphqlapi/prelude.generated.go` are generated from the corresponding schema
sources and share the `graphqlapi` package. See
[`CODE_GENERATION.md`](CODE_GENERATION.md) for the full source/output map and
why generated code is necessarily package-local rather than in one directory.

The normal verification targets are:

```bash
make test
make conformance
make compiler-bench
```

Opt-in Arango tests validate actual execution and Explain behavior. New query
optimizations must add result-shape tests plus Explain/cost expectations before
changing bootstrap indexes or deleting a semantic fallback.
