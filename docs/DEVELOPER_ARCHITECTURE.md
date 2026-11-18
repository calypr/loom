# Developer Architecture

Loom is an Arango-backed compiler for flat, exportable FHIR dataframes.
[Loom's FHIR generator](CODE_GENERATION.md) reads HL7's FHIR R5
StructureDefinitions and Loom's edge policy. Ingest and the compiler use the
resulting Go model and metadata.

## Repository boundaries

The top-level directories have distinct ownership:

| Path | Ownership |
| --- | --- |
| `api/` | Canonical Loom HTTP contract and oapi-codegen configuration. |
| `fhir/` | Source schemas edited by developers. |
| `generated/` | Checked-in generated output; never server business logic. |
| `internal/` | Handwritten server implementation. |
| `cmd/` | Executable entry points and developer generators. |

Generated FHIR structs live in `fhir/model`; raw generated schema tables
live in `fhir/schema`. `internal/fhir/schema` adds the handwritten
selector, traversal, and compiler semantics that consume those tables.

Dataframe requests are defined in OpenAPI. Their Go service inputs and responses
are owned by `internal/api/dataframe`.

## Runtime surfaces

The Builder integration contract is documented in
[`EXPLORER_AUTHORING.md`](EXPLORER_AUTHORING.md). The Builder uses the REST
Explorer lifecycle and submits V2 intent. Every HTTP method and path is defined in the
canonical [`api/openapi.yaml`](../api/openapi.yaml) contract.

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
mounts health, generation ingestion/activation, recipe execution, dataframe queries, and
Explorer operations; handwritten strict-interface implementations remain under
`internal/server`. Snapshot and standalone release-management HTTP APIs are not
part of the server surface; Explorer publication owns its release transaction.

The REST dataframe run endpoint exposes the compiler. Explorer V2
authoring adds an intent-to-native-recipe lowering phase, then calls the same
recipe resolver/compiler and AQL renderer. Do not add a second query compiler
or hand-maintained AQL path behind another endpoint. See
[`EXPLORER_COMPILATION_ARCHITECTURE.md`](EXPLORER_COMPILATION_ARCHITECTURE.md)
for the complete Builder-to-AQL sequence.

The dataframe run endpoint reads the authorized Arango graph. The rows and
aggregations endpoints read published ClickHouse data. Published ClickHouse dataframe discovery and reads require
an explicit authorized project and exact dataframe selector.
Only registered READY publication outputs are exposed; adding a dataset or
column must not require API code generation or a Loom restart. Publication
and published-data reads are ClickHouse-only; Loom has no Elasticsearch
publication or reader fallback.

## Load and storage model

`internal/ingest` reads an NDJSON directory, preflights it against the local
schema, builds vertices and `fhir_edge` documents, profiles populated fields,
and writes batches to Arango. A resource must be present in the configured
schema and have a generated loader.

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
REST request
  -> internal/server OpenAPI handler
  -> generated gqlgen executor and internal resolver binding
  -> internal/api/dataframe/query.Service
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
REST request
  -> internal/server OpenAPI handler
  -> generated graph executor and internal resolver binding
  -> internal/api/dataframe/dataframe.Service
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
no compiler child may import runtime, catalog, or HTTP transport code.

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

The dataframe run request uses structured FHIR fields and traversals. Ingestion
and publication use immutable dataset generations.

The former hard-coded GDC AQL files, GDC export command, browser `/builder`
demo, and unowned bootstrap materializations were removed. They bypassed the
compiler and did not work with immutable generation keys.

## Generated code and tests

Run `make generate-fhir` after changing the graph schema. See
[the code generation guide](CODE_GENERATION.md) for the generated artifacts.

Run `make generate-openapi` after changing the canonical HTTP contract or its
generator configuration under `api/`. Do not hand-edit
`api/loomapi/api.gen.go`.

The normal verification targets are:

```bash
make test
make conformance
make compiler-bench
```

Opt-in Arango tests validate actual execution and Explain behavior. New query
optimizations must add result-shape tests plus Explain/cost expectations before
changing bootstrap indexes or deleting a semantic fallback.
