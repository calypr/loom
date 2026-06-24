# Developer Architecture

This document describes the repo as it exists now, not the older `prototype.py`
 era.

The current system is a Go codebase with:

- a CLI for load/discovery/query work
- a Fiber HTTP server
- a gqlgen-backed GraphQL contract
- an Arango-first dataframe compiler/executor
- generated FHIR schema metadata used by the planner

## 1. Runtime Surfaces

### CLI

The CLI entrypoint is:

- [`cmd/arango-fhir-proto/main.go`](../cmd/arango-fhir-proto/main.go)

Current commands:

- `load`
- `query-gdc-case-assay-matrix`
- `export-gdc-case-assay-matrix`
- `discover-populated-references`
- `discover-populated-fields`
- `prepare-gdc-case-assay-matrix`
- `build-scalar-index`
- `benchmark`

The CLI is still useful for:

- bulk load runs
- dataset discovery/debugging
- direct AQL-driven exports
- backend benchmarking and parity experiments

### HTTP Server

The server entrypoint is:

- [`cmd/arango-fhir-server/main.go`](../cmd/arango-fhir-server/main.go)

It wires together:

- Fiber HTTP server
- REST bulk ingest service
- GraphQL dataframe service
- discovery cache invalidation after successful writes
- optional scope-aware auth for reads/writes

Mounted routes are registered in:

- [`internal/writeapi/http.go`](../internal/writeapi/http.go)

Current routes:

- `GET /healthz`
- `GET /graphql`
- `POST /graphql`
- `GET /apollo`
- `POST /api/v1/imports`
- `GET /api/v1/imports/:id`
- `GET /api/v1/imports/:id/events`

## 2. Storage Model

The repo’s logical collections are defined in:

- [`internal/proto/backend.go`](../internal/proto/backend.go)

Current primary collections:

- one collection per FHIR resource type
- `fhir_edge`
- `fhir_field_catalog`
- `patient_file_rollup`

Important details:

- every resource collection gets `project` and `id`-oriented indexes
- `Patient` gets extra `_key` indexes for project-scoped ordered traversal
- `fhir_edge` stores graph connections plus `project`, `from_type`, `to_type`, `label`
- `fhir_field_catalog` stores load-time populated-field metadata
- `patient_file_rollup` is a helper/materialized collection for prepared dataframe paths

The common backend interface is:

- [`internal/store/store.go`](../internal/store/store.go)

Arango is the main execution backend for reads. Backend selection is abstracted
through:

- [`internal/dbio/dbio.go`](../internal/dbio/dbio.go)

## 3. Load Pipeline

The primary load implementation is:

- [`internal/proto/load.go`](../internal/proto/load.go)

High-level flow:

1. discover NDJSON files from `META`
2. open the selected backend
3. bootstrap collections and indexes
4. choose row-builder mode
5. decode rows and generate:
   - vertex documents
   - `fhir_edge` documents
   - field-catalog profile updates
6. batch writes concurrently
7. emit JSON progress events

Key load-related pieces:

- [`internal/proto/files.go`](../internal/proto/files.go): NDJSON discovery and scanners
- [`internal/proto/row_builder.go`](../internal/proto/row_builder.go): generated vs generic row-builder surface
- [`internal/proto/generated_load.go`](../internal/proto/generated_load.go): generated fast-path extraction
- [`internal/catalog/field_catalog.go`](../internal/catalog/field_catalog.go): load-time field profiling

The loader can still run in a slower generic mode, but the intended fast path is
the generated FHIR-specific path.

## 4. Discovery and Builder Metadata

Builder introspection depends on two discovery surfaces:

- populated traversals
- populated fields/pivots

These live in:

- [`internal/catalog/discovery.go`](../internal/catalog/discovery.go)
- [`internal/catalog/field_catalog.go`](../internal/catalog/field_catalog.go)
- [`internal/catalog/auth_resource_paths.go`](../internal/catalog/auth_resource_paths.go)

Important behavior:

- discovery is project-scoped
- discovery can also be auth-resource-path-scoped
- builder traversal hints come from `fhir_edge`
- builder field hints come from `fhir_field_catalog`
- pivot hints are derived at load time and persisted in the catalog

The server wraps discovery in a cache:

- [`internal/catalogcache/cache.go`](../internal/catalogcache/cache.go)

That cache is invalidated after successful writes by the server bootstrap code in:

- [`cmd/arango-fhir-server/main.go`](../cmd/arango-fhir-server/main.go)

## 5. GraphQL Layer

The GraphQL schema is:

- [`internal/graphqlapi/schema.graphqls`](../internal/graphqlapi/schema.graphqls)

The GraphQL package owns:

- schema
- request/response mapping
- auth-aware introspection orchestration
- GraphQL-to-dataframe request normalization

Key files:

- [`internal/graphqlapi/service.go`](../internal/graphqlapi/service.go): main orchestration service
- [`internal/graphqlapi/mappers.go`](../internal/graphqlapi/mappers.go): GraphQL model to internal dataframe builder mapping
- [`internal/graphqlapi/schema.resolvers.go`](../internal/graphqlapi/schema.resolvers.go): gqlgen resolver entrypoints
- [`internal/graphqlapi/handler.go`](../internal/graphqlapi/handler.go): GraphQL, playground, and Apollo handlers
- [`internal/graphqlapi/scalars.go`](../internal/graphqlapi/scalars.go): custom JSON scalar support

The GraphQL split is intentional:

- introspection query:
  - `dataframeBuilderIntrospection`
- dataframe execution mutation:
  - `runFhirDataframe`

GraphQL is the read/builder contract. It is not used for bulk ingest.

## 6. FHIR Schema Metadata vs. FHIR Semantics

One of the biggest current repo boundaries is the distinction between:

- raw structural FHIR schema knowledge
- optimizer/domain semantics layered on top

### Structural schema metadata

Generated schema metadata lives in:

- [`internal/fhirschema/schema.go`](../internal/fhirschema/schema.go)
- [`internal/fhirschema/generated.go`](../internal/fhirschema/generated.go)

This package answers questions like:

- what canonical fields exist for a resource type
- how to decompose a selector into `sourcePath`, `where`, `valuePath`
- whether a path resolves to `CodeableConcept`
- whether a path resolves to `Coding`
- whether an Observation-style pivot key/value pair is structurally valid

This metadata is generated from:

- [`schemas/graph-fhir.json`](../schemas/graph-fhir.json)
- [`cmd/generate/main.go`](../cmd/generate/main.go)

### Semantic/domain rules

Friendly field aliases and lowering hints live in:

- [`internal/fhirsemantics/registry.go`](../internal/fhirsemantics/registry.go)

This package owns things like:

- `fieldRef` aliases such as `Patient.case_id`
- document-reference summary normalization hints
- traversal roles such as patient-neighbor vs direct traversal
- study hydration hints

The intended split is:

- `fhirschema`: what the FHIR structure is
- `fhirsemantics`: how this repo wants to optimize or present it

## 7. Dataframe Compiler and Lowering

The dataframe subsystem is:

- [`internal/dataframe`](../internal/dataframe)

Current important files:

- [`internal/dataframe/dataframe.go`](../internal/dataframe/dataframe.go): service entrypoint, validation, auth-scope prep, compile/run orchestration
- [`internal/dataframe/planner.go`](../internal/dataframe/planner.go): request lowering and optimization planning
- [`internal/dataframe/advanced_types.go`](../internal/dataframe/advanced_types.go): advanced internal builder structures
- [`internal/dataframe/advanced_compile.go`](../internal/dataframe/advanced_compile.go): advanced lowered builder -> optimized AQL

Current execution model:

1. GraphQL request is normalized into internal dataframe `Builder`
2. selectors, pivots, aggregates, and slices are validated against discovery + schema metadata
3. the planner lowers the public traversal-first request into the advanced internal form
4. the advanced compiler emits Arango AQL
5. rows are streamed through the backend query executor

Important current truth:

- GraphQL stays simple
- optimizer complexity lives under the covers
- Arango is the only supported runtime for `runFhirDataframe`

## 8. Query Execution Service

Direct query helpers and older query-oriented flows now live under:

- [`internal/querysvc`](../internal/querysvc)

Key files:

- [`internal/querysvc/query.go`](../internal/querysvc/query.go): direct query execution and bulk export helpers
- [`internal/querysvc/prepare_case_assay.go`](../internal/querysvc/prepare_case_assay.go): helper-table preparation
- [`internal/querysvc/build_scalar_index.go`](../internal/querysvc/build_scalar_index.go): scalar index build path

This package is mostly for:

- CLI-oriented query/export flows
- prepared helper surfaces
- benchmark support

It is distinct from the GraphQL dataframe path, which compiles from the GraphQL
builder contract instead of simply reading a canned query file.

## 9. REST Write API

Bulk ingest is intentionally REST-only.

Core files:

- [`internal/writeapi/http.go`](../internal/writeapi/http.go)
- [`internal/writeapi/service.go`](../internal/writeapi/service.go)

Design choices:

- one uploaded NDJSON file per import request
- explicit `project` and `resource_type`
- async HTTP contract via operation polling
- in-process execution, not a distributed job system

The server persists minimal operation state in memory:

- operation metadata
- status
- event stream
- load summary

This is enough for workflow-runner orchestration without turning this service
into a scheduler.

## 10. Experimental Code

Non-primary backend work has been pushed under:

- [`experimental/`](../experimental/)

Important contents:

- [`experimental/docker-compose.yml`](../experimental/docker-compose.yml): local Arango-only compose
- [`experimental/docker/docker-compose.full.yml`](../experimental/docker/docker-compose.full.yml): Arango + Surreal + Postgres research stack
- [`experimental/queries/`](../experimental/queries/): backend-specific query artifacts outside the primary Arango path
- [`experimental/ARANGO_VS_SURREAL_FHIR_POSTMORTEM.md`](../experimental/ARANGO_VS_SURREAL_FHIR_POSTMORTEM.md): technical comparison write-up

The main repo should now be understood as Arango-first. Experimental backends
remain available for comparison work, but they are not the primary product path.

## 11. Generated Artifacts

There are two generation steps that matter:

- [`cmd/generate/main.go`](../cmd/generate/main.go) for generated FHIR code and schema metadata
- gqlgen for GraphQL generated code

Make targets:

- `make generate-fhir`
- `make generate-graphql`

Relevant generated outputs:

- [`internal/fhir`](../internal/fhir)
- [`internal/fhirschema/generated.go`](../internal/fhirschema/generated.go)
- [`internal/graphqlapi/generated.go`](../internal/graphqlapi/generated.go)
- [`internal/graphqlapi/model/models.go`](../internal/graphqlapi/model/models.go)

## 12. Recommended Places To Change Things

If you want to:

- add a new friendly field alias:
  - [`internal/fhirsemantics/registry.go`](../internal/fhirsemantics/registry.go)
- extend structural selector or pivot validation:
  - [`internal/fhirschema/schema.go`](../internal/fhirschema/schema.go)
- change GraphQL contract:
  - [`internal/graphqlapi/schema.graphqls`](../internal/graphqlapi/schema.graphqls)
  - then run `make generate-graphql`
- change dataframe lowering or optimization:
  - [`internal/dataframe/planner.go`](../internal/dataframe/planner.go)
  - [`internal/dataframe/advanced_compile.go`](../internal/dataframe/advanced_compile.go)
- change bulk ingest HTTP behavior:
  - [`internal/writeapi/http.go`](../internal/writeapi/http.go)
  - [`internal/writeapi/service.go`](../internal/writeapi/service.go)
- change backend bootstrap/index strategy:
  - [`internal/proto/backend.go`](../internal/proto/backend.go)

## 13. Current Reality Check

The repo is no longer “a few hardcoded AQL scripts plus a loader.”

It is currently:

- a generated FHIR-aware load pipeline
- a persisted discovery/catalog system
- a GraphQL builder/read contract
- a semantic lowering layer
- an Arango dataframe compiler/executor
- a REST ingest surface

That is the architecture the docs and future work should assume.
