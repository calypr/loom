# ARANGODB_PROTO

FHIR graph loader and dataframe server, with ArangoDB as the primary execution
backend.

This repo has two main runtime surfaces:

- `arango-fhir-proto`: CLI for FHIR loading (including immutable complete
  generations) and catalog diagnostics
- `arango-fhir-server`: Fiber server for compiler-backed GraphQL reads and a
  temporary one-file import compatibility endpoint

The current product direction is:

- load raw FHIR NDJSON into one collection per resource type
- store graph edges in `fhir_edge`
- profile populated fields into `fhir_field_catalog`
- lower typed dataframe requests through the FHIR-aware compiler into scoped AQL
- expose the current expert/compatibility GraphQL transport while the guided
  recipe transport is being added

ArangoDB is the only runtime backend. The tracked [`experimental/`](experimental/)
directory contains the local Arango compose setup.

## Docs

- [Quickstart](docs/QUICKSTART.md)
- [Developer Architecture](docs/DEVELOPER_ARCHITECTURE.md)
- [Product Recipes and Dataset Discovery](docs/PRODUCT_RECIPE_DISCOVERY.md)
- [Formal Product Gap Analysis](docs/FORMAL_GAP_ANALYSIS.md)
- [Compiler-First FHIR/AQL Plan](docs/COMPILER_FIRST_PLAN.md)
- [Compiler-First Implementation Status](docs/COMPILER_IMPLEMENTATION_STATUS.md)
- [Terra Ultra Parallel Execution Plan](docs/TERRA_ULTRA_EXECUTION_PLAN.md)
- [Compiler Cleanup Audit](docs/COMPILER_CLEANUP_AUDIT.md)

## Current Layout

- [`cmd/arango-fhir-proto/main.go`](cmd/arango-fhir-proto/main.go): CLI entrypoint
- [`cmd/arango-fhir-server/main.go`](cmd/arango-fhir-server/main.go): HTTP server entrypoint
- [`internal/ingest`](internal/ingest): load pipeline and Arango ingest bootstrap/runtime
- [`internal/catalog`](internal/catalog): populated-field and populated-reference discovery
- [`internal/catalog/cache`](internal/catalog/cache): per-project discovery cache
- [`internal/discovery`](internal/discovery): safe guided capability snapshots built from scoped catalog facts
- [`internal/dataset`](internal/dataset): dataset generation, schema, and scope lifecycle contract
- [`internal/datasetstore`](internal/datasetstore): Arango-backed immutable manifest and active-generation pointer store
- [`internal/graphqlapi`](internal/graphqlapi): GraphQL schema, request mapping, introspection service
- [`internal/dataframe`](internal/dataframe): dataframe validation, lowering, and AQL compilation
- [`internal/export`](internal/export): strict flat-row NDJSON and CSV encoding primitives
- [`internal/dataframeexport`](internal/dataframeexport): streaming bridge from dataframe execution to flat encoders
- [`internal/fhirschema`](internal/fhirschema): generated schema metadata used by planner/validation
- [`internal/dataframebuilder`](internal/dataframebuilder): builder introspection, friendly `fieldRef`s, and GraphQL input translation
- [`internal/recipe`](internal/recipe): versioned product recipe intent and guided template metadata
- [`internal/schemaidentity`](internal/schemaidentity): exact graph-schema identity captured by dataset generations
- [`internal/api`](internal/api): HTTP host, authenticated GraphQL mounting, and legacy import compatibility wiring
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

HTTP wiring lives in [`internal/api/routes.go`](internal/api/routes.go) and [`internal/api/server.go`](internal/api/server.go).

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
- generated schema metadata in `internal/fhirschema`
- derived field aliases in `internal/dataframebuilder` and explicit lowering rules in `internal/dataframe`

What is explicitly experimental:

- the guided discovery, recipe, and streaming-export foundations until their
  public delivery API exists
