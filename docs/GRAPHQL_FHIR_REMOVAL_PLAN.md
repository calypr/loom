# Remove the typed FHIR GraphQL API and gqlgen, keep all FHIR validation

Status: plan, 2026-11-17. Base: `origin/feature/default-recipe-authoring` @ `78a50f63d`.
Nothing has been changed yet.

## Decision

Remove the typed FHIR GraphQL query API: the 23 per-resource roots such as `Patient(project:, filters:, limit:)` and the 138 FHIR
GraphQL types. It is a second query engine beside the dataframe compiler, with its own filters, reference
traversal and authorization path. It has no cursor paging and no known consumer.

**Keep every FHIR validation and edge rule in ingest.** Loom is a FHIR graph database; the generated Go
structs that validate resources and extract edges stay.

## What exists today (three outputs from one generator)

`cmd/generate` reads `schemas/graph-fhir.json` and writes three separate things. It does **not** use gqlgen.
gqlgen is a separate step (`make generate-graphql`).

| Output | Lines | Who uses it | Fate |
|---|---|---|---|
| `generated/fhirschema/generated.go`: metadata (paths, types, cardinality, reference targets) | 55k | The compiler and Builder via `internal/fhir/schema` (`lower`, `semantic`, `ir`, `render/aql`, `spec`, `recipe`, `catalog`, `explorer/*`, `ingest`) | **Keep, unchanged** |
| `generated/fhir/*.go`: Go structs + `Validate()` + `ExtractEdges()` | 72k | **Ingest** (see below), `dataframe/spec/filter.go:236,243` (date validators), `api/bulk/load`, and the GraphQL executor | **Keep.** Remove only the GraphQL-specific parts (`graphql.go`, `gqlgen:` struct tags) |
| `generated/graphql/graph/schema/fhir_schema.graphqls` → gqlgen → `executor/fhir_schema.generated.go` | 2.8k + 108k | The typed FHIR GraphQL API only | **Delete** |

## FHIR validation that must keep working (do not touch)

Ingest's default path for the 23 supported resource types is `GeneratedRowBuilder`
(`internal/ingest/generation_workers.go:195-198`, `source_load_validation.go:38-41`). It calls
`loadRowGeneratedInternal` (`internal/ingest/generated_load.go`), which does:

1. **Typed decode** into the generated struct (`sonic.Unmarshal`). Malformed JSON or a wrong type is a validation error.
2. **`Validate()`** (`generated/fhir/validate.go`, 14.9k lines): 66 required-field checks
   (`MissingRequiredFieldError`, classified in `internal/ingest/row_error.go:31`), 357 regex pattern checks,
   and date, dateTime, time, binary, URI and UUID format checks.
3. **A required string `id`.**
4. **`ExtractEdges()`** (`generated/fhir/extract.go`, 53.8k lines): parses every `Reference`, normalizes the
   target type with `fhirschema.ConcreteResourceType`, and **only emits an edge when the target type is allowed
   for that field.** For example, `Specimen.parent` accepts only `Specimen`.
5. **Edge document checks:** `EdgeFromGrip`, edge marshal, and auth-path stamping (`internal/ingest/row_builder.go:61-63,
   155-170`). These are counted as `rowErrorEdge`. Any validation, generation or edge error makes the load incomplete
   (`internal/ingest/resource_load.go:41`, `generation_orchestration.go:291`, `source_load_upload.go:124`).

The fallback `GenericRowBuilder` (`--use-generic` or unsupported types) uses jsonschemagraph
(`class.ValidateFast` + `BuildEdgesWithID`). It's unaffected.

These invariants are the acceptance gate for every phase below. **No change to `validate.go`, `extract.go`, `resources.go`,
`helpers.go`, or the struct fields and `json` tags in `model.go`.**

## Consumers checked (2026-11-17)

| Consumer | Uses | Effect |
|---|---|---|
| Loom UI (`ui/packages/loom-ui/src/api.ts:1836`) | `dataframeRows`, `dataframeAggregations` on `/graphql/graph` | Unaffected by Phase 1 |
| `cmd/dataframe-query`, `examples/*.graphql` | `runFhirDataframe` on `/graphql/dataframe` | Unaffected by Phase 1 |
| `internal/acceptance/scenario.go:929`, `performance.go` | dataframe queries on `/graphql/graph` | Unaffected by Phase 1 |
| `~/Desktop/BMEG/calypr_etl_pod/etl-job/main.go` | the recipe operations (validate, preview, preflight, explain, execution) | **Retired by the owner (2026-11-17); not a consumer.** Confirm it isn't deployed or scheduled anywhere before Phase 2 merges |
| Typed FHIR roots (`Patient(project…` etc.) | **No caller** in Loom, `calypr_etl_pod`, or any other repo under `~/Desktop/BMEG` | Safe to remove |
| Deployed Calypr frontend (source not available locally) | Unknown | Phase 0 check |

---

## Phase 0 — Confirm there is no external use (cheap, required)

The WP-21 view-builder removal broke the deployed Calypr Explorer because in-repo search wasn't enough. Do one of:
- **A.** Search the deployed Loom access logs for GraphQL requests whose query text contains
  any of the 23 root names followed by `(project`, over the last 30 days. Zero hits means go.
- **B.** If logs don't carry the GraphQL body, ship one release where `listFHIR`
  (`internal/api/graphql/graph/resolver/fhir_resolver_helpers.go:57`) logs a structured
  `deprecated_fhir_graphql_root` warning with the resource type and principal. Wait 14 days; zero warnings means go.

Also ask the Calypr frontend owners directly: "Does anything query Loom's `/graphql/graph` with per-resource FHIR roots?"

## Phase 1 — Delete the typed FHIR GraphQL API (no other contract change)

### 1a. Generator (`cmd/generate`)

| File | Edit |
|---|---|
| `cmd/generate/graphql.go` (301 lines), `cmd/generate/graphql_test.go` | Delete |
| `cmd/generate/main.go:17,85-92` | Remove the `-graphql-out` flag and the `generateFHIRGraphQL` call |
| `cmd/generate/main.go:70`, `cmd/generate/resources.go:~30-45` | Stop writing `generated/fhir/graphql.go`; remove the `IsFHIRResource` emission (`resources.go:39`) |
| `cmd/generate/model.go:96-100` | Emit `` `json:"%s"` `` only; drop the `gqlgen:"%s"` tag and its comment |
| `cmd/generate/*_test.go` | Update any golden that asserted the gqlgen tag or `graphql.go` |

### 1b. Generated code (regenerate; never hand-edit)

| Path | Result |
|---|---|
| `generated/graphql/graph/schema/fhir_schema.graphqls` | Deleted |
| `generated/fhir/graphql.go` | Deleted |
| `generated/fhir/model.go` | Regenerated: 2,498 `gqlgen:` tags removed. Field names, types and `json` tags must be **byte-identical otherwise**. Check with `git diff --word-diff` |
| `generated/graphql/graph/executor/fhir_schema.generated.go` (108k) | Deleted by `make generate-graphql` |
| `generated/graphql/graph/executor/root_.generated.go`, `model/models.go` | Regenerated and smaller (FHIR complexity, `FHIRResource`, `FHIRResourceType`, `FHIRPrimitiveExtension` gone) |

### 1c. gqlgen configuration

| File | Edit |
|---|---|
| `gqlgen.yml` | Remove `generated/graphql/graph/schema/fhir_schema.graphqls` from `schema:`, `autobind: github.com/calypr/loom/generated/fhir`, and `struct_tag: gqlgen` |
| `Makefile` `generate-fhir` | Unchanged except that no `.graphqls` is produced. Keep `gofmt` on `generated/fhir` and `generated/fhirschema` |

### 1d. Hand-written GraphQL code

| File | Edit |
|---|---|
| `internal/api/graphql/graph/resolver/fhir_schema.resolvers.go` (3,760) | Delete; gqlgen stops generating it |
| `internal/api/graphql/graph/resolver/fhir_resolver_helpers.go` (152) | Delete (`listFHIR`, `resolveFHIRReference`) |
| `internal/api/graphql/graph/resolver/fhir_reference_loader.go` (410) + `_test.go` (544) | Delete (N+1 reference loader, used only by FHIR fields) |
| `internal/api/graphql/graph/query/fhir_service.go` (236) + `_test.go` (97) | Delete. `ListFHIR` and `ListFHIRInContext` have no other caller |
| `internal/api/graphql/graph/resolver/resolver.go` | Remove FHIR-only fields and wiring, if any |
| `internal/api/graphql/graph/query/*` | **Keep** `FhirFilterInput` handling: it's defined in the hand schema and used by `runFhirDataframe`, `fhirGraph`, `recipe_mapping.go` and `input_resolution.go` |

### 1e. Docs and audit records

`docs/GRAPHQL_API.md`, `README.md`, `docs/DEVELOPER_ARCHITECTURE.md`, `docs/CODE_GENERATION.md`:
remove the per-resource query sections and say the typed FHIR API was retired. Update
`docs/PACKAGE_AUDIT.csv` / `PACKAGE_AUDIT_DECISIONS.csv` through `python3 scripts/package_audit_verify.py` (`--full`).

### Phase 1 verification (all required)

1. `make generate && git diff --exit-code` after the commit (generated code is current; matches the `tests.yaml` contracts job).
2. `go build ./... && go vet ./internal/... && go test ./...`
3. **Ingest unchanged:**
   - `go test ./internal/ingest/... ./internal/api/bulk/load/... ./cmd/generate/...`
   - Run one generation load of `testdata/devloop-fixture` before and after, and compare the summary:
     vertex count, edge count, `validation_errors`, `generation_errors`, `edge_errors`.
   - Feed a known-bad row (missing required field, bad date, wrong reference target type) and confirm the same error category.
4. **Real data:** `go run ./cmd/loom-acceptance --fixture-only`, then `make acceptance-real` on an owned stack. The NCPI
   counts and GraphQL dataframe checks must match the lock.
5. **The UI still works:** `npm --prefix ui test` and `make e2e` (the Viewer reads `dataframeRows` and `dataframeAggregations`).
6. **The API is gone:** a POST to `/graphql/graph` with `{ Patient(project:"x") { id } }` returns a GraphQL validation error
   (`Cannot query field "Patient"`), not a server error.

Expected removal: about 115k generated lines, about 5.2k hand-written lines (resolvers, loader, service and tests),
a smaller `models.go`/`root_`, and faster `make generate` and compiles.

---

## Phase 2: remove gqlgen entirely (approved 2026-11-17, after Phase 1 on the same branch)

The owner retired `calypr_etl_pod`, so no external consumer is known. After Phase 1, gqlgen serves only the hand-written
schema (`internal/api/graphql/graph/schema/schema.graphqls`: 12 queries and 5 mutations), about 45k generated lines.
Phase 2 moves the operations that are still used to REST (OpenAPI with `oapi-codegen`, which already serves everything else) and drops the rest.

### Consumer map (rechecked 2026-11-17)

| Operation | Callers | Action |
|---|---|---|
| `dataframeRows`, `dataframeAggregations` | UI Viewer (`ui/packages/loom-ui/src/api.ts`), `internal/acceptance/scenario.go`, `scripts/loom-dev.mjs`, `scripts/verify-b07-faults.mjs`, `scripts/measure-b07-evidence.mjs` | **Port** to `POST /api/v1/projects/{project}/dataframes/rows` and `/aggregations`, with the same request and response semantics (paging cursor, filters, field selection, authorization). Migrate every caller |
| `dataframeDataset`, `dataframeAggregate` | `internal/acceptance/scenario.go` only | Move acceptance onto the ported endpoints. Port these only if an acceptance check needs data the ported endpoints can't return |
| `runFhirDataframe` | `cmd/dataframe-query`, `examples/*.graphql` | **Port** to REST (default; keeps the CLI working). Convert `examples/*.graphql` to JSON request bodies |
| `validate`/`preview`/`preflight`/`explainDataframeRecipe`, `runDataframeRecipe`, `registerDataframeRecipeRevision`, `dataframeRecipeRevision(s)`, `dataframeRecipeExecution` | `internal/server/recipe_blank_project_test.go`; `scripts/verify-cda-mixed-scope-selectors.mjs` | **Drop the transport.** Rewrite the server test against the service layer so it keeps checking the same behaviour. Delete or port the script, matching what plan 2 decided for `scripts/verify-*`. `recipe-executions/{id}` already exists in REST |
| `fhirGraph`, `explainFhirGraph`, `projectDataframeDatasets` | none | Drop |

### Package move (required before deletion)

`cmd/dataframe-profile/main.go` imports `generated/graphql/graph/model` and `internal/api/graphql/graph/query`.
`internal/server/openapi_recipe_execution.go` imports `internal/api/graphql/graph/dataframe`.

Move the service layers `internal/api/graphql/graph/{query,dataframe}` to `internal/api/dataframe`. Replace the gqlgen
`model` types they use with plain Go types owned by that package; where the OpenAPI types fit, use those instead.
Run `python3 scripts/package_audit_verify.py` before and after the move, then the `--full` mode.

### Stages (separate commits, in order)

- **2a. Add REST.** Add the endpoints to `openapi/openapi.yaml`, regenerate them, and add handlers in `internal/server` over the moved
  service layer. GraphQL still works. Add a parity test: the same request through GraphQL and REST returns identical rows and aggregations.
- **2b. Migrate callers.** Move the UI `api.ts` (with its zod schemas and `exportPopulation.unit.test.tsx`), acceptance, `loom-dev.mjs`, the b07 scripts,
  and `cmd/dataframe-query` onto REST. Rewrite `recipe_blank_project_test.go`.
- **2c. Delete.** Remove:
  - `generated/graphql/**`;
  - `internal/api/graphql/graph/{handler,errors}.go`, `resolver/` and `schema/`;
  - `cmd/gqlgenfix`, `gqlgen.yml`, and `make generate-graphql`/`graphql-check` (also delete them from `tests.yaml`);
  - the `/graphql/graph`, `/graphql/dataframe` and `/apollo` routes, in both `openapi.yaml` and the server mux;
  - `github.com/99designs/gqlgen` and `github.com/vektah/gqlparser/v2` from `go.mod` (`go mod tidy`);
  - the GraphQL sections of the docs.

### Gate before merging Phase 2

Extend Phase 0 to cover **all** of `/graphql/*` and `/apollo`, not just the typed roots. Use the deployed access logs, or ask the Calypr
frontend owners whether anything outside the `loom-ui` package calls Loom GraphQL. Also confirm `calypr_etl_pod` has no live deployment or
cron job. The deployed Calypr Explorer broke once from an assumed-unused API, so this check is not optional.

### Phase 2 verification

1. Everything in Phase 1's verification list, rerun on the final commit.
2. The GraphQL/REST parity test passes at 2a, and its REST half stays as a regression test after 2c.
3. `make e2e` and `npm --prefix ui test`: the Viewer's rows, aggregations and export all work over REST.
4. `make acceptance-real`: the dataframe checks match the lock over REST.
5. `rg -n "gqlgen|gqlparser|/graphql/" --glob '!docs/**'` finds nothing, apart from intentional changelog notes.

## Not in scope

- Replacing `generated/fhir` with metadata-driven validation. The structs are the validation and edge-extraction engine; keep them.
- Any change to `generated/fhirschema`, `internal/fhir/schema`, or the compiler.
