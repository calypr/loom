# internal/server package split plan

Goal: `internal/server` contains startup and wiring only. Everything else moves to
the package that owns its domain or transport. This plan moves code; it does not
change behavior, routes, or wire contracts.

**Membership rule.** A function stays in `internal/server` only if it parses
configuration, constructs a dependency, or starts/stops the process. Anything that
branches on domain data, classifies errors, encodes wire responses, or runs a loop
belongs to the package that owns that concern, which exposes a constructor the
server calls.

## Current state (main @ 9f47a89c4)

55 production files (~12k lines) and 117 test files (~34k lines) in one package,
imported by `main.go` and `cmd/arango-fhir-server`. It holds four concerns:

1. Startup and wiring: `Run`, `Config`, auth and storage construction.
2. Explorer domain adapters: receipt compilation, validation, and output
   reconciliation; capability resolution; imported recipe sources; construction inputs.
3. Recipe and dataframe runtime adapters: recipe authorization, ClickHouse combine
   resolution, recipe publication, AQL error classification, explain.
4. OpenAPI transport: `HTTPRoutes` (~80 strict-server methods) and
   `explorerHTTPHandlers` (50 `*Direct` methods).

Only (1) belongs here.

## Accidental structure to remove, not relocate

1. **Two transport layers for explorer routes.** Each explorer operation is an
   `HTTPRoutes` method that calls an `explorerHTTPHandlers.*Direct` method and then
   maps errors. Built from scratch, each domain's API package owns one handler type
   whose methods are the `loomapi.StrictServerInterface` methods for that domain.
   The `*Direct` layer disappears.
2. **`HTTPRoutes` is one struct for every domain.** The same rule applies to its
   other routes. Their owners already exist: `internal/api/http` serves health,
   `internal/api/bulk/load` owns generations and source loads (its `Handler` is
   already what the routes call), and `internal/api/dataframe/dataframe` owns
   dataframe reads and recipe executions. The server builds the strict server by
   embedding these handler types in one struct. That embedding is wiring. If two
   domains define the same operation, the code does not compile, which guards
   against overlap.
3. **Domain behavior hidden in wiring closures.** `lifecycle.Config` takes about 30
   function fields, and `server.go` fills many of them with inline closures that hold
   logic (receipt preview, population mapping, cell trace, row lineage, stream
   validation, receipt lookup, combine-schema resolution). Each owning package
   should export a constructed adapter whose methods match those fields, so
   `server.go` passes `adapter.Method` values and contains no logic.
4. **Adapter-to-lifecycle coupling is narrow.** The adapters import `lifecycle` only
   for its port contract (`AuthorizedCapability`, `CompileReceiptRequest`,
   `ConstructionSourceStageRequest`, `Error`/`ClassUnavailable`, `Execution`,
   function types). That rules out `recipeimport` and `compilation` as homes because
   `lifecycle` imports both. Cohesion rules them out as well: `recipeimport` has no
   engine dependencies, and `compilation` is pure workspace translation. The
   adapters therefore get their own packages for cohesion reasons. No port
   extraction is needed.

## Target layout

Each package imports only packages above it in this table, or packages that
already exist.

### Leaf relocations to existing packages

| Destination | Moves from `internal/server` |
|---|---|
| `internal/explorer` | `explorer_authoring_http.go` → exported `AuthoringError` constructors; `project_configs.go`; `local_workspace_writeback.go` |
| `internal/authscope` | `explorer_lifecycle_wire.go` → `SubjectFromContext` (callers use `httpapi.RequestIDFromContext` directly); file deleted |
| `internal/api/http` | `logServerDiagnostic` and diagnostic/redaction helpers (`server.go`); `serviceErrorResponse`, `unexpectedResponseStatus` (`openapi_routes.go`); `mapServiceError`, `rawJSON` (`openapi_data_routes.go`); `decodeStrict` |
| `internal/dataframe/publication` | `release_verifier.go` |
| `internal/explorer/lifecycle` | `artifact_dev.go` (dev delay decorator of `ArtifactPublishedReader`) |
| `internal/explorer/artifactfs` | `private_artifact_reconciler.go` → a generic sweep loop, run once for artifact FS and once for lifecycle private artifacts |
| `internal/api/recipe` | `recipe_discovery.go` |

### New domain and runtime packages

| Package | Contents |
|---|---|
| `internal/dataframe/execution/arango` | `explain_wiring.go`; from `server.go`: `classifyDataframeQueryError`, `configuredAQLQueryRows` (receives the existing error mapper), `loggedPreviewIndexPreparation`, pivot preview budget context |
| `internal/dataframe/recipe/runtime` | `recipe_authorization.go`, `recipe_combine_resolution.go`, `recipe_publication.go` |
| `internal/explorer/capability/resolver` | `explorer_capability.go`; the scope half of `explorer_preview_security.go` (`validateAuthorizedReadScope`, scope digest) |
| `internal/explorer/importedsource` | `explorer_imported_recipes.go`, `explorer_imported_source_edit.go`, `explorer_authoring_network.go` |
| `internal/explorer/constructioninputs` | `construction_inputs_catalog.go` |
| `internal/explorer/receipt` | `explorer_receipt_contract.go`, `explorer_receipt_validation.go`, `explorer_output_reconcile.go`, `explorer_combine_output_metadata.go`, `explorer_lifecycle_helpers.go`, `explorer_publication.go`, `receipt_preview_diagnostic.go`, the receipt half of `explorer_preview_security.go`, plus a `receipt.Runtime` type that replaces the lifecycle closures in `server.go` (compile, compile source stage, scan categories and column population, preview, validate stream, population mapping, cell trace, row lineage, lookup, materialize) |

### Transport: one handler type per domain

| Package | Strict-server methods it owns |
|---|---|
| `internal/api/http` | `GetHealth`, `GetLiveness`, `GetReadiness` |
| `internal/api/bulk/load` | `StrictHandlers` adapts the existing load `Handler` without method name collisions. Generation create/status/activate; source-load begin/status/upload/publish (`openapi_data_routes.go`, `openapi_source_load.go`) |
| `internal/api/dataframe/dataframe` | dataframe rows, aggregations, FHIR dataframe, recipe execution (`openapi_dataframe.go`, `openapi_recipe_execution.go`) |
| `internal/api/explorer` (new) | All explorer, construction, selection, artifact, interpretation, and view-builder operations: `explorer_config_route.go`, `explorer_preview_response.go`, the 16 `openapi_explorer_*.go`, `openapi_frame_source_options.go`, `openapi_semantic_inventory.go`, `openapi_view_builder.go`. Single-use `*Direct` methods merge into strict-server methods. The same handler owns shared request authorization, preview, and response helpers. Move `preserveConfiguredQueryError` with `previewRouteError`. |

`HTTPRoutes`, `routes.go`, and `openapi_routes.go` are deleted.

### What remains in `internal/server`

| File | Role |
|---|---|
| `run.go` | `Run`, signal handling, shutdown, `recordDegradation` |
| `config.go` | today's `options.go`: `Config`, flags, environment, validation |
| `wire_auth.go` | today's `auth_wiring.go` |
| `wire_storage.go` | Arango and ClickHouse clients, registries, stores |
| `wire_dataframe.go` | engine, recipe runtime, publication |
| `wire_explorer.go` | capability resolver, imported sources, receipt runtime, `lifecycle.Config` built from adapter methods |
| `wire_http.go` | `type routes struct{ *httpapi.HealthHandlers; *loadapi.Handler; *dataframeapi.Handlers; *explorerapi.Handlers }` and registration |

Tests that remain here drive the full composition: `core_authoring_protocol_*`,
`*_integration_test.go`, `wiring_test.go`, `config_test.go`,
`openapi_route_inventory_test.go`, `openapi_status_coverage_test.go`. All other tests
move with their subject.

## Phases

Each phase is one coherent commit: code, tests, export changes, and audit refresh.
No phase changes behavior.

| Phase | Scope | Depends on |
|---|---|---|
| 0 | Baseline: `python3 scripts/package_audit_verify.py internal/server`, `go list -deps` snapshot, route inventory and status coverage passing. | — |
| 1 | Leaf relocations into existing packages, plus `dataframe/execution/arango`. | 0 |
| 2 | `dataframe/recipe/runtime` | 1 |
| 3 | `explorer/capability/resolver` | 1 |
| 4 | `explorer/importedsource`; move the shared compiled-stage-to-receipt mapper into `explorer/compilation`. | 3 |
| 5 | `explorer/constructioninputs` | 2, 3 |
| 6 | `explorer/receipt` with `receipt.Runtime`; replace the lifecycle closures in `server.go` | 2, 3, 4 |
| 7a | Health, load, and dataframe handlers move to their API packages; `HTTPRoutes` temporarily embeds them | 1 |
| 7b | `api/explorer`: collapse `*Direct` into strict-server methods | 5, 6, 7a |
| 7c | Delete `HTTPRoutes`; `wire_http.go` composes the embedded routes struct | 7b |
| 8 | Split `server.go` into `run.go` + `wire_*.go`; check every remaining function against the membership rule; remove dead helpers (`staticcheck -checks U1000`) | 7c |

Phases 2, 3, and 7a are independent and can run in parallel worktrees. Every phase
edits `server.go` wiring, so merges are serialized and later phases rebase.

## Per-phase checklist

1. `python3 scripts/package_audit_verify.py` on `internal/server` and the destination before the move.
2. `git mv` files that move whole. Split files only along the seams named above.
3. Export only what a caller outside the new package needs.
4. Move each subject's tests and the fakes they need. If several packages need a fake (`http_test_helpers_test.go`, `openapi_test_helpers_test.go`), move it to `internal/testkit`. Do not copy it.
5. `go build ./... && go vet ./...`, `make lint-go-comments`, `make test-go`; also `make test-integration` for phases 4, 6, 7b, and 7c.
6. Keep `openapi_route_inventory_test.go` unchanged and preserve the status coverage assertions and negative cases. In Phase 7, update status coverage discovery to follow the moved handlers.
7. `python3 scripts/package_audit_verify.py --full` afterward. Inspect the dependency diff: no package other than `main` and `cmd/*` imports `internal/server`, and there are no new cycles.
8. Update the `docs/PACKAGE_AUDIT.csv` rows for touched packages.
9. Run `pstack:interrogate` on phases 6, 7b, and 7c, per AGENTS.md.

## Open decisions

- Package names (`recipe/runtime`, `capability/resolver`, `importedsource`, `constructioninputs`, `receipt`, `api/explorer`, `dataframe/execution/arango`). Settle them before phase 2.
- `config.go` could become `internal/server/config` if `cmd/*` needs configuration without the root. Not required.
- Converting `lifecycle.Config` from function fields to interfaces is a later redesign of `lifecycle`. Phase 6 only needs adapter methods that match the existing function types.
