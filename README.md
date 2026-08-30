# Loom

Loom turns FHIR-shaped data in ArangoDB into authorized, queryable flat
dataframes in ClickHouse.

ArangoDB is the canonical graph store: Loom loads NDJSON, extracts FHIR
resources and relationships, profiles the populated graph, and compiles typed
dataframe recipes into scoped AQL. ClickHouse is the publication store: Loom
streams resolved recipe outputs into versioned physical tables, records their
schema and visibility in an Arango-backed catalog, and exposes them through a
stable reader API.

Loom is deliberately not an arbitrary AQL gateway, ClickHouse SQL proxy, or a
replacement FHIR model. It is a recipe-driven graph-to-flat data service with
explicit authorization and publication boundaries.

Explorer Builder authoring is intent-driven: the browser submits the versioned
V2 authoring document, Loom lowers it to a native recipe, and the existing
recipe compiler produces the scoped plan/AQL. The browser never constructs or
repairs the recipe AST. See [the Explorer authoring contract](docs/EXPLORER_AUTHORING.md).

```mermaid
flowchart LR
    NDJSON["FHIR NDJSON"] --> Ingest["Ingest and catalog"]
    Ingest --> Arango["ArangoDB\nresources, fhir_edge, field catalog"]
    Recipe["Versioned dataframe recipe"] --> Compile["Resolve, validate, compile"]
    Arango --> Compile
    Compile --> AQL["Scoped AQL stream"]
    AQL --> Publish["Atomic publication catalog commit"]
    Publish --> ClickHouse["Versioned flat tables"]
    Publish --> Catalog["Arango publication catalog"]
    Catalog["Arango publication catalog"] --> Graph["POST /graphql/graph"]
    ClickHouse --> Graph
    Arango --> Graph["POST /graphql/graph"]
    Arango --> Dataframe["POST /graphql/dataframe"]
    Compile --> Graph
    Compile --> Dataframe
```

## What Loom owns

- FHIR NDJSON ingestion into ArangoDB resource collections and `fhir_edge`.
- Immutable dataset generations and active-generation selection.
- Populated-field, reference, traversal, and pivot discovery.
- Strict, versioned dataframe recipes and compiler-backed AQL execution.
- Scoped publication of recipe outputs into ClickHouse.
- A durable Arango catalog that maps exact dataframe selectors to current
  published outputs.
- Multi-project, `project_id`-identifiable and `auth_resource_path`-scoped flat-data reads.

Loom does **not** expose arbitrary ClickHouse tables, arbitrary SQL, or an
Elasticsearch/Guppy fallback. Published dataframe reads require an exact
recipe, translation-version, and output selector.

## Runtime surfaces

| Surface | Purpose |
| --- | --- |
| `arango-fhir-proto` | Operator CLI for loading data, loading immutable generations, catalog discovery, and local dataframe materialization. |
| `arango-fhir-server` | HTTP server for graph compilation/control and flat dataframe reads. |
| `POST /graphql/graph` | Arango graph and recipe control plane: explicit graph traversal, typed FHIR reads, recipe validation, preview, execution, publication reads, and recipe control. |
| `POST /graphql/dataframe` | Arango-backed FHIR dataframe compiler and executor (`runFhirDataframe`). |
| `POST /api/v1/projects/:project/explorers/...` | REST Explorer lifecycle and V2 intent authoring used by the Builder. |
| `PUT /api/v1/projects/:project/resources/:resourceType` | Primary multipart NDJSON resource loader. |

`GET /graphql/graph` serves GraphQL Playground for the graph API. `GET /apollo`
opens Apollo Sandbox pointed at `/graphql/graph`. There is intentionally no
`/graphql` compatibility route.

The canonical contract for every server route is
[`openapi/openapi.yaml`](openapi/openapi.yaml). It includes health, ingestion,
snapshot/release, recipe execution, GraphQL transport, Explorer lifecycle, and
Explorer authoring operations. The `:project` path parameter is the tenancy
identity used for authorization and becomes the published row `project_id`
(for example, `HTAN_INT-BForePC`).

## Data lifecycle

1. Load FHIR NDJSON into ArangoDB. Loom creates resource collections,
   `fhir_edge`, and `fhir_field_catalog`.
2. For production snapshots, load an immutable dataset generation and let the
   active manifest select it for a project.
3. Resolve a registered recipe against the populated graph and caller scope.
4. Compile the resolved recipe to parameterized AQL and stream rows from
   ArangoDB.
5. Publish output streams to new ClickHouse physical tables. Loom injects the
   reserved `project_id` and `auth_resource_path` columns, records
   schema/provenance in Arango, and atomically advances the logical publication
   pointer only once every output is READY.
6. Query the logical dataframe through `/graphql/graph`. The reader resolves
   authorized current outputs from the catalog; it never accepts a physical
   ClickHouse table name.

The configured ClickHouse database is created by the server during startup if
necessary. Publication creates its tables itself. No DDL or alias-management
HTTP endpoint is required. The ClickHouse account needs database creation plus
`CREATE`, `INSERT`, `SELECT`, and `DROP` privileges.

## Local development

The local Compose stack starts both ArangoDB and ClickHouse:

```bash
rtk docker compose -f experimental/docker-compose.yml up -d
make generate-fhir
make generate-graphql
make build
```

Load the bundled example data into a mutable local namespace:

```bash
./bin/arango-fhir-proto load \
  --url http://127.0.0.1:8529 \
  --database fhir_proto \
  --meta-dir META \
  --project ARANGODB_PROTO \
  --auth-resource-path EllrottLab-GDC_Data
```

For a production-shaped immutable snapshot, use `load-generation` instead. It
requires an opaque generation identifier and deliberately forbids truncation:

```bash
./bin/arango-fhir-proto load-generation \
  --url http://127.0.0.1:8529 \
  --database fhir_proto \
  --meta-dir META \
  --project ARANGODB_PROTO \
  --generation local-meta-1 \
  --auth-resource-path EllrottLab-GDC_Data
```

Start Loom locally with an unrestricted development principal:

```bash
./bin/arango-fhir-server \
  --listen :8080 \
  --no-auth \
  --url http://127.0.0.1:8529 \
  --database fhir_proto \
  --clickhouse-url clickhouse://127.0.0.1:9000 \
  --clickhouse-database loom \
  --dataframer-recipe /path/to/dataframer.json
```

Add `--dataset-generations` when reading the active immutable generation. That
mode disables the legacy one-file import endpoint.

Useful local URLs:

- [Graph Playground](http://127.0.0.1:8080/graphql/graph)
- [Apollo Sandbox](http://127.0.0.1:8080/apollo)
- FHIR dataframe: `http://127.0.0.1:8080/graphql/dataframe`
- Published dataframe reader: `http://127.0.0.1:8080/graphql/graph`
- [Health summary](http://127.0.0.1:8080/health)
- [Process liveness](http://127.0.0.1:8080/livez)
- [Dependency readiness](http://127.0.0.1:8080/readyz)

Run the checked-in graph dataframe example after the server is up:

```bash
make dataframe-demo
```

See [the documentation index](docs/README.md) for the complete guide map,
[the Quickstart](docs/QUICKSTART.md) for local setup, and
[the Explorer authoring guide](docs/EXPLORER_AUTHORING.md) for the current
Builder contract.

## Graph and flat GraphQL contracts

The complete request, filter, pagination, authorization, and limitation
reference is maintained in [docs/GRAPHQL_API.md](docs/GRAPHQL_API.md). The
short sections below explain where each surface fits in the system.

### Graph: compile, inspect, and publish

`/graphql/graph` is where a caller asks Loom about the Arango graph or drives
recipe work. `/graphql/dataframe` is the dedicated Arango-backed FHIR
dataframe compiler endpoint. Both use the same authorized Arango backend and
compiler; the separate routes keep client defaults aligned with query intent.

Publication is intentionally a recipe-level operation: Loom must read the
authorized Arango graph, resolve the recipe against the selected generation,
and then write its output to ClickHouse. It is not a raw “insert rows” API.

### Typed FHIR reads

The same `/graphql/graph` endpoint also exposes generated, typed FHIR reads.
The 23 root fields are exactly `BodyStructure`, `Condition`,
`DiagnosticReport`, `DocumentReference`, `FamilyMemberHistory`, `Group`,
`ImagingStudy`, `Medication`, `MedicationAdministration`, `MedicationRequest`,
`MedicationStatement`, `Observation`, `Organization`, `Patient`,
`Practitioner`, `PractitionerRole`, `Procedure`, `ResearchStudy`,
`ResearchSubject`, `Specimen`, `Substance`, `SubstanceDefinition`, and `Task`.
Each field returns a list and accepts `project`, `filters: [FhirFilterInput!]`,
and a `limit` (default 25). Read-only callers are capped at 10,000 rows;
callers with write access to the selected project are uncapped. ID lookup is
an ordinary `id` filter; filters are ANDed using the existing Loom selector
semantics.

Every selectable property in the generated FHIR schema is available, including
primitive-extension fields such as `_birthDate`. `Reference.resource` performs
an authorized outbound lookup for relative, absolute, and versioned references;
reverse relationships are not exposed. Cursor pagination, arbitrary sorting,
recursive filter trees, and same-element sibling correlation are not supported
in this checkpoint. The endpoint is FHIR-shaped, but is not advertised as a
fully HL7-conformant GraphQL implementation.

### Flat: discover and read published dataframes

Published dataframe reads are available through `/graphql/graph`. The schema is intentionally
small and data-driven:

```graphql
query ExplorerRows($input: DataframeRowsInput!) {
  dataframeRows(input: $input) {
    materialization { name revision rowCount columns { name logicalType } }
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
    "first": 100,
    "sort": { "column": "case_id" }
  }
}
```

The reader requires an explicit `projectId` and exact selector
`(recipe, translationVersion, output)`. There is no server-side default recipe,
`dataType` alias, or cross-project federation. Loom authorizes the requested
project, resolves its active pointer-backed publication, and reads that one
physical dataframe. Columns and capabilities remain runtime-discovered, so a
new publication does not require GraphQL regeneration or a server restart.

## Authorization and tenancy

In production, Loom uses either basic authentication or Calypr/Fence-backed
authorization. Calypr mode resolves the caller’s allowed
`auth_resource_path` values and applies them to graph reads and published flat
reads. Publication injects `auth_resource_path` into every ClickHouse output;
the flat reader treats it as an authorization predicate, never as a
client-controlled filter. `project_id` identifies the source project for
Explorer filters; it does not replace authorization paths.

Set `--no-auth` only for local development. It creates an explicit unrestricted
principal and must not be used in a deployment.

The server can be configured with YAML:

```yaml
server:
  listen: ":8080"
  url: http://arangodb:8529
  database: fhir_proto
  clickhouse:
    enabled: true
    url: clickhouse://clickhouse:9000
    database: loom
  dataframer:
    recipe: /etc/loom/dataframer.json

auth:
  mode: calypr
```

Basic mode reads `LOOM_AUTH_BASIC_USERNAME` and `LOOM_AUTH_BASIC_PASSWORD` if
they are not supplied in the config file.

`server.dataframer.recipe` is required only when ClickHouse is enabled. Loom
reads and validates that recipe during startup; deployments can update it
without rebuilding the server image.

## Repository guide

| Path | Responsibility |
| --- | --- |
| [`cmd/`](cmd) | Operator CLI, server executable, and developer tools. |
| [`openapi/`](openapi) | Canonical Loom HTTP specification and generator configuration. |
| [`schemas/`](schemas) | Source FHIR graph schema. |
| [`gqlgen.yml`](gqlgen.yml) | gqlgen source configuration. |
| [`generated/`](generated) | Checked-in generator-managed artifacts; see the code-generation guide before editing. |
| [`generated/fhir`](generated/fhir) | Public generated FHIR resource structs and validation helpers. |
| [`generated/fhirschema`](generated/fhirschema) | Generated raw FHIR schema metadata. |
| [`generated/graphql/graph`](generated/graphql/graph) | gqlgen models, executors, and schemas. |
| [`internal/api/graphql/graph/resolver`](internal/api/graphql/graph/resolver) | GraphQL resolver bindings and transport adapters. |
| [`internal/fhir/schema`](internal/fhir/schema) | Server-only FHIR schema metadata and selector semantics. |
| [`internal/ingest`](internal/ingest) | NDJSON loading, validation, graph extraction, and ingest lifecycle. |
| [`internal/dataset`](internal/dataset) | Immutable generation and active-manifest contracts. |
| [`internal/catalog`](internal/catalog) | Evidence of populated fields, references, and authorization paths. |
| [`internal/explorer`](internal/explorer) | V2 authoring intent, resolved Builder models, server compilation receipts, immutable revisions, publication state, and legacy migration types. |
| [`internal/dataframe/compiler`](internal/dataframe/compiler) | Typed plan IR, lowering, optimization, and AQL rendering. |
| [`internal/dataframe/recipe`](internal/dataframe/recipe) | Recipe contract, validation, schema resolution, execution, and control services. |
| [`internal/dataframe/publication`](internal/dataframe/publication) | Backend-neutral bounded streaming publication contract. |
| [`internal/dataframe/published`](internal/dataframe/published) | Safe single-project published-data reads and aggregates. |
| [`internal/store/arango`](internal/store/arango) | ArangoDB boundary. |
| [`internal/store/clickhouse`](internal/store/clickhouse) | Typed ClickHouse driver boundary and DDL/DML. |
| [`internal/api/graphql/graph`](internal/api/graphql/graph) | GraphQL HTTP transport and error presentation. |
| [`internal/api/graphql/graph/query`](internal/api/graphql/graph/query) | Arango graph and FHIR dataframe API services. |
| [`internal/api/graphql/graph/dataframe`](internal/api/graphql/graph/dataframe) | Published-dataframe reads, aggregates, export, and GraphQL model mapping. |

## Build, generation, and tests

```bash
make build                 # server and CLI binaries
make generate-fhir         # generated FHIR structs and schema metadata
make generate-graphql      # gqlgen bindings
make generate-openapi      # Loom HTTP models and strict Fiber server
make graphql-check         # GraphQL and dataframe checks
make test                  # full Go test suite
make conformance           # compiler conformance corpus
```

The generated FHIR metadata and GraphQL bindings are checked in. Regenerate
them when their inputs change; resolver adapter bodies are the documented
gqlgen-managed exception.
[`docs/CODE_GENERATION.md`](docs/CODE_GENERATION.md) explains every generated
directory, its source of truth, and the required regeneration order.

For a direct build and full verification:

```bash
go build .
go test ./...
```

## Further reading

- [Documentation index](docs/README.md)
- [OpenAPI contract](openapi/README.md)
- [Quickstart](docs/QUICKSTART.md)
- [Explorer authoring contract](docs/EXPLORER_AUTHORING.md)
- [Default dataframer recipe authoring guide](docs/DATAFRAMER_RECIPES.md)
- [Dataframer recipe reference and operating manual](docs/DATAFRAMER_RECIPE_REFERENCE.md)
- [GraphQL API guide](docs/GRAPHQL_API.md)
- [Developer architecture](docs/DEVELOPER_ARCHITECTURE.md)
- [Experimental local stack](experimental/README.md)

The Helm chart lives in the separate `gen3-helm` repository under `helm/loom`.
