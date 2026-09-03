# Developer Architecture

Loom is an Arango-backed compiler for flat, exportable FHIR dataframes. The
authority for FHIR structure is the checked-in
[`schemas/graph-fhir.json`](../schemas/graph-fhir.json) graph schema and the
generated Go metadata derived from it. Code should not add a second handwritten
FHIR model or a parallel AQL implementation.

## Repository boundaries

The top-level directories have distinct ownership:

| Path | Ownership |
| --- | --- |
| `openapi/` | Canonical Loom HTTP contract and oapi-codegen configuration. |
| `schemas/` | Source schemas edited by developers. |
| `gqlgen.yml` | GraphQL generator configuration. |
| `generated/` | Checked-in generated output; never server business logic. |
| `internal/` | Handwritten server implementation. |
| `cmd/` | Executable entry points and developer generators. |

Generated FHIR structs live in `generated/fhir`; raw generated schema tables
live in `generated/fhirschema`. `internal/fhir/schema` adds the handwritten
selector, traversal, and compiler semantics that consume those tables.

GraphQL is split the same way. Handwritten schemas, HTTP transport, error
presentation, query services, and materialization mapping live under
`internal/api/graphql/graph`. gqlgen models, executors, and resolver bindings live
under `generated/graphql/graph`. The root gqlgen configurations are inputs, so
they do not live in `generated/`.

## Runtime surfaces

The Builder integration contract is documented in
[`EXPLORER_AUTHORING.md`](EXPLORER_AUTHORING.md). The Builder uses the REST
Explorer lifecycle and submits V2 intent; GraphQL does not expose Explorer
lifecycle or authoring types. Every HTTP method and path is defined in the
canonical [`openapi/openapi.yaml`](../openapi/openapi.yaml) contract.

Ownership map: `dataset` owns immutable FHIR generation lifecycle; `catalog`
owns persistence-neutral observed facts; `catalog/arango` owns catalog
persistence; `explorer` owns portable Explorer configs, editable drafts,
immutable revisions, and publication state; `explorer/arango` owns its durable
adapter; `dataframe/publication` owns dataframe publishing contracts and the
runner; `dataframe/publication/{arango,clickhouse}` own storage adapters; and
`dataframe/published` owns safe single-project published-data reads and aggregates.

`cmd/arango-fhir-proto` is the operator CLI. Its supported commands are:

- `load` for the temporary mutable compatibility load;
- `load-generation` for a complete immutable dataset generation;
- `audit-relationship-edges` for a read-only scan of invalid graph endpoint
  types;
- `rebuild-relationship-catalog` for an explicit catalog rebuild after an
  edge repair or backfill;
- `repair-generation` for staging a corrected immutable generation from the
  original source files; activation requires its explicit `--activate` flag;
- `activate-generation` for validating and activating a previously staged
  generation;
- `discover-populated-references` and `discover-populated-fields` for
  catalog diagnostics.

`cmd/arango-fhir-server` owns the HTTP process. Generated OpenAPI registration
mounts health, generation ingestion/activation, recipe execution, GraphQL, and
Explorer operations; handwritten strict-interface implementations remain under
`internal/server`. Snapshot and standalone release-management HTTP APIs are not
part of the server surface; Explorer publication owns its release transaction.

The GraphQL dataframe mutation is the live compiler transport. Explorer V2
authoring adds an intent-to-native-recipe lowering phase, then calls the same
recipe resolver/compiler and AQL renderer. Do not add a second query compiler
or hand-maintained AQL path behind another endpoint. See
[`EXPLORER_COMPILATION_ARCHITECTURE.md`](EXPLORER_COMPILATION_ARCHITECTURE.md)
for the complete Builder-to-AQL sequence.

The HTTP API names its backend boundaries explicitly. `/graphql/graph` is the
Arango graph/control-plane GraphQL endpoint and published ClickHouse dataframe
reader, while `/graphql/dataframe` is the Arango-backed FHIR dataframe
compiler endpoint. Published ClickHouse dataframe discovery and reads require
an explicit authorized project and exact dataframe selector.
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

Immutable loads use `internal/dataset` and `internal/dataset/arango`. Dataset
owns schema identity and generation manifests; ingest owns the Arango vertex
and edge document shapes it writes. Their manifest records project, generation,
and schema identity; the active pointer selects one READY generation per
project.
The generation-qualified physical keys and mandatory generation predicates are
part of the query correctness contract, not an optional filter.

Graph resource identity is schema-owned. Only concrete FHIR root types may
appear as vertex collections, edge endpoints, row grains, or relationship
catalog types. Backbone and other nested definitions remain available to
field/selector resolution but are rejected at graph boundaries. Use
`arango-fhir-proto audit-relationship-edges` to inspect historical generations
for invalid endpoint types. A catalog rebuild filters those rows from the
rebuilt catalog, but does not rewrite `fhir_edge`; malformed generations must
be re-ingested into a new immutable generation before activation. The
`repair-generation` command performs that workflow: it audits the source,
loads the supplied source directory into a distinct staged generation, audits
the result, and leaves the old active generation untouched unless
`--activate` is explicitly supplied.

For example:

```bash
./bin/arango-fhir-proto repair-generation \
  --project ARANGODB_PROTO \
  --source-generation load:old \
  --generation load:repaired \
  --meta-dir META
```

The command stages the target and reports its audit. Activate it separately
after reviewing that the target audit reports zero invalid edges:

```bash
./bin/arango-fhir-proto activate-generation \
  --project ARANGODB_PROTO \
  --generation load:repaired \
  --url http://127.0.0.1:8529 \
  --database fhir_proto
```

For an automated one-shot repair, `repair-generation --activate` performs the
same validation before activation.

Generation repair is deliberately memory-bounded. The loader caps catalog
distinct values, pivot columns, extension observations, cached payload shapes,
and retained field paths; it also bounds parser workers and queued input/write
batches. The CLI applies a Go soft memory limit by default and exposes the
operational controls explicitly:

```bash
./bin/arango-fhir-proto repair-generation \
  --project ARANGODB_PROTO \
  --source-generation load:old \
  --generation load:repaired \
  --meta-dir META \
  --memory-limit 4GiB \
  --workers 2 \
  --writers 2 \
  --line-queue-size 1024 \
  --write-queue-size 8
```

The Go limit is a GC target, not a substitute for a Kubernetes cgroup limit.
Run the operator binary as a separate one-shot migration workload with an
explicit memory request and limit; do not run a large repair inside the
long-lived server container. The Docker image includes `/app/arango-fhir-proto`
for that purpose, and the image target architecture is used so local arm64
clusters do not invoke Rosetta for the migration process.

## Compiler path

The current runtime call path is:

```text
GraphQL request
  -> internal/api/graphql/graph HTTP handler
  -> generated gqlgen executor and internal resolver binding
  -> internal/api/graphql/graph/query.Service
  -> dataframe/execution.Service
  -> dataframe/spec request contracts
  -> dataframe/semantic logical plan
  -> dataframe/compiler/ir typed physical plan
  -> dataframe/compiler/lower FHIR storage lowering
  -> dataframe/compiler/optimize IR rewrites
  -> dataframe/compiler/render/aql parameterized AQL
  -> Arango query execution/streaming
```

Runtime preparation and execution live in `internal/dataframe/execution`;
compiler orchestration lives in `internal/dataframe/compiler`; structured transport
errors live in `internal/dataframe/errors`. `internal/catalog` owns scoped observed-field
and relationship facts. `internal/fhir/schema` owns structural metadata and
selector semantics. These
boundaries matter: catalog observations constrain what is populated, while
schema metadata constrains what a request means.

The ClickHouse read path is parallel but separate:

```text
GraphQL request
  -> internal/api/graphql/graph HTTP handler
  -> generated graph executor and internal resolver binding
  -> internal/api/graphql/graph/dataframe.Service
  -> dataframe/published.Reader
  -> ClickHouse
```

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
| Catalog, auth, generation, cursor, or profiling | `internal/dataframe/execution` |

For the complete ownership and dependency map, see the generated
[package audit lookup table](PACKAGE_AUDIT.md). Historical
package-reorganization plans may exist outside this checkout, but they are not
runtime contract references.

## Compatibility tracks

The raw-structure GraphQL dataframe input remains for the existing compiler
transport. Ingestion and publication now use immutable dataset generations.

The former hard-coded GDC AQL files, GDC export command, browser `/builder`
demo, and unowned bootstrap materializations were removed. They bypassed the
compiler and did not work with immutable generation keys.

## Generated code and tests

Run `make generate-fhir` after changing the graph schema and
`make generate-graphql` after changing the GraphQL schema. Do not hand-edit
generated FHIR resources, schema metadata, or gqlgen output. Generated Go
artifacts live under `generated/`; handwritten transport and service code stays
under `internal/`. See
[`CODE_GENERATION.md`](CODE_GENERATION.md) for the full source/output map and
package boundaries.

Run `make generate-openapi` after changing the canonical HTTP contract or its
generator configuration under `openapi/`. Do not hand-edit
`generated/loomapi/api.gen.go`.

The normal verification targets are:

```bash
make test
make conformance
make compiler-bench
```

Opt-in Arango tests validate actual execution and Explain behavior. New query
optimizations must add result-shape tests plus Explain/cost expectations before
changing bootstrap indexes or deleting a semantic fallback.
