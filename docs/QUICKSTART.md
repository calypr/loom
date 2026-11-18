# Quickstart

This guide gets a fresh local checkout to:

1. ArangoDB running in Docker
2. sample FHIR data loaded
3. the Loom HTTP server running at `http://127.0.0.1:8080`
5. a real dataframe query returning rows

## 1. Start ArangoDB

The lightweight local compose file is:

- [`experimental/docker-compose.yml`](../experimental/docker-compose.yml)

Bring it up:

```bash
rtk docker compose -f experimental/docker-compose.yml up -d
```

This starts:

- ArangoDB at [http://127.0.0.1:8529](http://127.0.0.1:8529)

## 2. Generate code and build binaries

```bash
make generate
make build
```

This produces:

- [`bin/arango-fhir-proto`](../bin/arango-fhir-proto)
- [`bin/arango-fhir-server`](../bin/arango-fhir-server)

## 3. Load the bundled sample dataset

The repo ships a local sample FHIR dataset under [`META/`](../META).

Load it into the default local database:

```bash
./bin/arango-fhir-proto load \
  --url http://127.0.0.1:8529 \
  --database fhir_proto \
  --meta-dir META \
  --project ARANGODB_PROTO \
  --auth-resource-path EllrottLab-GDC_Data
```

What this does:

- bootstraps one collection per FHIR resource type
- bootstraps `fhir_edge`
- bootstraps `fhir_field_catalog`
- loads raw payloads and generated edges
- profiles populated fields for builder introspection

The bootstrap and collection definitions live in
[`internal/ingest/backend.go`](../internal/ingest/backend.go).

## 4. Start the HTTP server

For local work, run with `--no-auth`:

```bash
./bin/arango-fhir-server \
  --listen :8080 \
  --no-auth \
  --url http://127.0.0.1:8529 \
  --database fhir_proto \
  --dataframer-recipe /path/to/dataframer.json
```

Useful endpoints:

- Health summary: [http://127.0.0.1:8080/health](http://127.0.0.1:8080/health)
- Process liveness: [http://127.0.0.1:8080/livez](http://127.0.0.1:8080/livez)
- Dependency readiness: [http://127.0.0.1:8080/readyz](http://127.0.0.1:8080/readyz)

## 5. Inspect and author an Explorer

Current Builder authoring uses the versioned REST V2 workflow. List project
Explorers, then load the selected Explorer's
`/authoring/v2/builder` state. Persist canonical edits with `POST /commands`,
compile the exact draft to an immutable receipt with `POST /reconcile`, preview that receipt with
`POST /preview`, and activate it only with `POST /publish`. Loom owns catalog
resolution, recipe lowering, compilation, materialization, persistence, and
diagnostics.

Read [the Explorer authoring guide](EXPLORER_AUTHORING.md) for the current
request/response behavior and the canonical
[OpenAPI contract](../openapi/openapi.yaml) for exact wire schemas.

## 6. Run a sample dataframe query

With the local server listening on port 8080, run the compact Patient example.

```bash
go run ./cmd/dataframe-query -request examples/meta_patient_dataframe.json
```

The example selects Patient fields and traverses related resources. Set
`input.project` in the example if the loaded project uses a different name.

Use `make dataframe-demo` for the larger GDC case matrix. Add
`DATAFRAME_REPEAT=10` to repeat the same request. The command prints server
timing, returned row count, and response bytes. See
[the dataframe API guide](DATAFRAME_API.md) for request and response details.

## 7. Shut down local Arango

```bash
rtk docker compose -f experimental/docker-compose.yml down
```
