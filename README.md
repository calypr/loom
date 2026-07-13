# ARANGODB_PROTO

FHIR graph loader and dataframe server, with ArangoDB as the primary execution
backend.

This repo has two main runtime surfaces:

- `arango-fhir-proto`: CLI for FHIR loading (including immutable complete
  generations) and catalog diagnostics
- `arango-fhir-server`: Fiber server for compiler-backed GraphQL reads and a
  temporary one-file import compatibility endpoint. It also exposes published
  dataframe reads backed by ClickHouse when configured.

The current product direction is:

- load raw FHIR NDJSON into one collection per resource type
- store graph edges in `fhir_edge`
- profile populated fields into `fhir_field_catalog`
- lower typed dataframe requests through the FHIR-aware compiler into scoped AQL
- expose the current compiler-backed GraphQL transport

ArangoDB is the only runtime backend. The tracked [`experimental/`](experimental/)
directory contains the local Arango compose setup.

## Docs

- [Quickstart](docs/QUICKSTART.md)
- Helm local-cluster deployment: `gen3-helm/helm/loom`
- [Developer Architecture](docs/DEVELOPER_ARCHITECTURE.md)
- [Compiler Performance](docs/COMPILER_PERFORMANCE.md)
- [Formal Product Gap Analysis](docs/FORMAL_GAP_ANALYSIS.md)
- [Compiler-First FHIR/AQL Plan](docs/COMPILER_FIRST_PLAN.md)
- [Physical Renderer Replacement Plan](docs/PHYSICAL_RENDERER_REPLACEMENT_PLAN.md)
- [Rich Physical Renderer Plan](docs/RICH_PHYSICAL_RENDERER_PLAN.md)
- [Luna Rich Physical Renderer Execution Plan](docs/LUNA_RICH_PHYSICAL_RENDERER_EXECUTION.md)
- [Luna Compiler Finalization Plan](docs/LUNA_COMPILER_FINALIZATION_PLAN.md)
- [Terra Ultra Parallel Execution Plan](docs/TERRA_ULTRA_EXECUTION_PLAN.md)
- [Part 5 Luna Frontend Enablement Plan](docs/LUNA_FRONTEND_ENABLEMENT_PART_5.md)

## Current Layout

- [`cmd/arango-fhir-proto/main.go`](cmd/arango-fhir-proto/main.go): CLI entrypoint
- [`cmd/arango-fhir-server/main.go`](cmd/arango-fhir-server/main.go): HTTP server entrypoint
- [`internal/ingest`](internal/ingest): load pipeline and Arango ingest bootstrap/runtime
- [`internal/catalog`](internal/catalog): populated-field and populated-reference discovery
- [`internal/dataset`](internal/dataset): dataset generation, schema, and scope lifecycle contract
- [`internal/dataset/arango`](internal/dataset/arango): Arango-backed immutable manifest and active-generation pointer store
- [`graphqlapi`](graphqlapi): GraphQL schema, request mapping, introspection service, and gqlgen output
- [`graphqlapi/dataframe`](graphqlapi/dataframe): GraphQL dataframe input translation and builder introspection
- [`internal/dataframe`](internal/dataframe): dataframe validation, lowering, and AQL compilation
- [`fhirstructs`](fhirstructs): generated FHIR structs, validators, and graph-edge extraction
- [`fhirschema`](fhirschema): generated compiler schema metadata and selector/traversal resolution
- [`internal/graphschema`](internal/graphschema): exact graph-schema identity captured by dataset generations
- [`internal/httpapi`](internal/httpapi): HTTP host, authenticated GraphQL mounting, and legacy import compatibility wiring
- [`internal/authscope`](internal/authscope): shared request principal context and auth-resource-path scope resolution
- [`experimental/`](experimental/): local Arango development compose setup

## Local Dev

Start local Arango:

```bash
rtk docker compose -f experimental/docker-compose.yml up -d
```

Generate/build:

```bash
make generate-fhir
make generate-graphql
make build
```

The repository root is also the server command, so a plain build works:

```bash
go build .
```

Load the bundled sample dataset:

```bash
./bin/arango-fhir-proto load \
  --backend arango \
  --url http://127.0.0.1:8529 \
  --database fhir_proto \
  --meta-dir META \
  --project ARANGODB_PROTO \
  --auth-resource-path EllrottLab-GDC_Data
```

For an immutable, generation-qualified load instead of the mutable prototype
load above, use a complete `META` directory and an operator-supplied opaque
generation ID. This command deliberately has no `--truncate` flag:

```bash
./bin/arango-fhir-proto load-generation \
  --generation local-meta-2026-07-11 \
  --backend arango \
  --url http://127.0.0.1:8529 \
  --database fhir_proto \
  --meta-dir META \
  --project ARANGODB_PROTO \
  --auth-resource-path EllrottLab-GDC_Data
```

Start the server in local demo mode:

```bash
./bin/arango-fhir-server \
  --listen :8080 \
  --no-auth \
  --backend arango \
  --url http://127.0.0.1:8529 \
  --database fhir_proto
```

To read the active immutable generation instead, add `--dataset-generations`.
That mode disables `POST /api/v1/imports`; use `load-generation` (or the future
bundle/job API) to create a complete snapshot.

Then open:

- Apollo Sandbox: [http://127.0.0.1:8080/apollo](http://127.0.0.1:8080/apollo)
- GraphQL endpoint: [http://127.0.0.1:8080/graphql](http://127.0.0.1:8080/graphql)
- Health check: [http://127.0.0.1:8080/healthz](http://127.0.0.1:8080/healthz)

The full step-by-step flow, including a sample GraphQL dataframe mutation, lives
in [docs/QUICKSTART.md](docs/QUICKSTART.md).

## Local cluster deployment

The Helm deployment now lives in the `gen3-helm` repository at
`helm/loom`. It owns the Loom chart and its official ClickStack dependencies;
see that repository's README for kind/minikube installation and port
forwarding instructions.

## Published dataframe materializations

Loom can stream a validated dataframe recipe into a versioned ClickHouse table.
The operator command accepts a compiler-shaped JSON recipe:

```json
{
  "project": "ARANGODB_PROTO",
  "rootResourceType": "Patient",
  "fields": [
    {"name": "patient_id", "select": "id", "valueMode": "FIRST"}
  ],
  "schema": [
    {"name": "patient_id", "clickhouseType": "Nullable(String)"}
  ]
}
```

```bash
./bin/arango-fhir-proto materialize-dataframe \
  --request dataframe.json \
  --name case-explorer \
  --clickhouse-url clickhouse://127.0.0.1:9000 \
  --clickhouse-database loom
```

The server exposes READY materializations through the existing GraphQL endpoint:

```graphql
query Rows($input: DataframeRowsInput!) {
  dataframeRows(input: $input) {
    columns
    rows
    pageInfo { hasNextPage endCursor }
    materialization { id name rowCount columns { name clickhouseType } }
  }
}
```

The browser never receives a ClickHouse table name or SQL capability. Loom
validates the requested columns, filters, sort, page size, project, generation,
and materialization state before querying ClickHouse. An explicit `schema`
preflight validates column names/types before the table is created and rejects
rows that emit undeclared or incompatible values. Aggregates accept the same
`EQ` and `CONTAINS` filters as row reads.

If you are starting from a fresh checkout, go there next:

- [Continue with the Quickstart](docs/QUICKSTART.md)

## Build Targets

Important make targets:

- `make generate-fhir`
- `make generate-graphql`
- `make build`
- `make build-server`
- `make build-cli`
- `make graphql-check`
- `make test`
- `make conformance`
- `make compiler-bench`

`make generate-graphql` is important now. The GraphQL schema and generated
artifacts are not purely static, and the repo includes a small reproducible
workaround in the generation target for a gqlgen scalar/codegen edge case.

## Current HTTP Surfaces

The server mounts:

- `GET /healthz`
- `GET /apollo`
- `GET /graphql`
- `POST /graphql`
- `POST /api/v1/imports` (legacy one-file import; disabled with `--dataset-generations`)

HTTP wiring lives in [`internal/httpapi/routes.go`](internal/httpapi/routes.go) and [`internal/httpapi/server.go`](internal/httpapi/server.go).

## Primary Collections

The loader bootstraps:

- one collection per FHIR resource type discovered in the NDJSON input
- `fhir_edge`
- `fhir_field_catalog`
- `loom_dataset_lifecycle` for generation-aware loads (never truncated)

See [`internal/ingest/backend.go`](internal/ingest/backend.go).

## Status

What is current and real:

- GraphQL introspection for populated traversals/fields/pivots
- GraphQL dataframe execution on Arango
- generated schema metadata in `fhirschema`
- derived field aliases in `graphqlapi/dataframe` and explicit lowering rules in `internal/dataframe`
