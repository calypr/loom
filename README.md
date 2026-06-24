# ARANGODB_PROTO

FHIR graph loader and dataframe server, with ArangoDB as the primary execution
backend.

This repo now has two main runtime surfaces:

- `arango-fhir-proto`: CLI for load, discovery, query, prepare, and benchmark work
- `arango-fhir-server`: Fiber server for GraphQL reads plus a direct REST import endpoint

The current product direction is:

- load raw FHIR NDJSON into one collection per resource type
- store graph edges in `fhir_edge`
- profile populated fields into `fhir_field_catalog`
- expose builder introspection and dataframe execution through GraphQL
ArangoDB is the first-class backend for dataframe execution. SurrealDB and
Postgres code remains under [`experimental/`](experimental/) for research and
benchmarking only.

## Docs

- [Quickstart](docs/QUICKSTART.md)
- [Developer Architecture](docs/DEVELOPER_ARCHITECTURE.md)
- [GraphQL/Dataframe Portability Notes](docs/DATAFRAME_BUILDER_PORTABILITY.md)
- [Arango vs. Surreal post-mortem](experimental/ARANGO_VS_SURREAL_FHIR_POSTMORTEM.md)

## Current Layout

- [`cmd/arango-fhir-proto/main.go`](cmd/arango-fhir-proto/main.go): CLI entrypoint
- [`cmd/arango-fhir-server/main.go`](cmd/arango-fhir-server/main.go): HTTP server entrypoint
- [`internal/ingest`](internal/ingest): load pipeline and Arango ingest bootstrap/runtime
- [`internal/catalog`](internal/catalog): populated-field and populated-reference discovery
- [`internal/catalog/cache`](internal/catalog/cache): per-project discovery cache
- [`internal/graphqlapi`](internal/graphqlapi): GraphQL schema, request mapping, introspection service
- [`internal/dataframe`](internal/dataframe): dataframe validation, lowering, and AQL compilation
- [`internal/fhirschema`](internal/fhirschema): generated schema metadata used by planner/validation
- [`internal/fhirsemantics`](internal/fhirsemantics): friendly `fieldRef`s and semantic lowering hints
- [`internal/api`](internal/api): HTTP API surface and ingest import wiring
- [`internal/authscope`](internal/authscope): shared request principal context and auth-resource-path scope resolution
- [`queries/`](queries/): Arango AQL query artifacts
- [`experimental/`](experimental/): non-primary backend work and benchmark artifacts

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

Start the server in local demo mode:

```bash
./bin/arango-fhir-server \
  --listen :8080 \
  --no-auth \
  --backend arango \
  --url http://127.0.0.1:8529 \
  --database fhir_proto
```

Then open:

- Apollo Sandbox: [http://127.0.0.1:8080/apollo](http://127.0.0.1:8080/apollo)
- GraphQL endpoint: [http://127.0.0.1:8080/graphql](http://127.0.0.1:8080/graphql)
- Health check: [http://127.0.0.1:8080/healthz](http://127.0.0.1:8080/healthz)

The full step-by-step flow, including a sample GraphQL dataframe mutation, lives
in [docs/QUICKSTART.md](docs/QUICKSTART.md).

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

`make generate-graphql` is important now. The GraphQL schema and generated
artifacts are not purely static, and the repo includes a small reproducible
workaround in the generation target for a gqlgen scalar/codegen edge case.

## Current HTTP Surfaces

The server mounts:

- `GET /healthz`
- `GET /apollo`
- `GET /graphql`
- `POST /graphql`
- `POST /api/v1/imports`

HTTP wiring lives in [`internal/api/routes.go`](internal/api/routes.go) and [`internal/api/server.go`](internal/api/server.go).

## Primary Collections

The loader bootstraps:

- one collection per FHIR resource type discovered in the NDJSON input
- `fhir_edge`
- `fhir_field_catalog`
- `patient_file_rollup`

See [`internal/ingest/backend.go`](internal/ingest/backend.go).

## Status

What is current and real:

- GraphQL introspection for populated traversals/fields/pivots
- GraphQL dataframe execution on Arango
- generated schema metadata in `internal/fhirschema`
- semantic alias/lowering hints in `internal/fhirsemantics`

What is explicitly experimental:

- SurrealDB/Postgres benchmarking and comparison
- alternate query artifacts under [`experimental/queries/`](experimental/queries/)
