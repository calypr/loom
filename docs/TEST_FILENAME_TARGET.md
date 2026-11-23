# Test filename targets

Phase 1 covers 704 underscore test files in 67 packages at `1cda1995a`.
Phase 2 is pending.

The inventory type-checks production and test variants, including external test packages. Its counts are direct references from test entry points to production declarations. Test helpers are followed for environment evidence and for production references through environment-gated test harnesses. The inventory is generated in `.cache/testnames/`.

The required filename-selector search returned no matches. Integration selection uses Go test entry points. Environment evidence in the map comes from environment lookups and testing skip calls.

The operator resolved all four open questions. Integration tests follow the production file where the reference evidence supports that target. Targets are capped at 1,500 lines. Package candidates and shared helpers are recorded here for later decisions.

## Target counts

| Action | Files |
| --- | ---: |
| rename | 396 |
| merge | 289 |
| split | 6 |
| helper | 13 |
| relocate | 0 |
| defer | 0 |

| Package | Rename | Merge | Split | Helper | Relocate | Defer |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `internal/acceptance` | 1 | 2 | 0 | 0 | 0 | 0 |
| `internal/api/bulk/load` | 3 | 4 | 0 | 0 | 0 | 0 |
| `internal/api/dataframe/dataframe` | 7 | 2 | 0 | 0 | 0 | 0 |
| `internal/api/dataframe/query` | 2 | 4 | 0 | 0 | 0 | 0 |
| `internal/api/explorer` | 31 | 27 | 1 | 1 | 0 | 0 |
| `internal/api/http` | 2 | 1 | 0 | 0 | 0 | 0 |
| `internal/authscope` | 2 | 5 | 0 | 0 | 0 | 0 |
| `internal/catalog` | 3 | 1 | 0 | 0 | 0 | 0 |
| `internal/catalog/arango` | 2 | 2 | 0 | 0 | 0 | 0 |
| `internal/catalog/fieldprofile` | 4 | 6 | 0 | 1 | 0 | 0 |
| `internal/dataframe/artifactformat` | 0 | 1 | 0 | 0 | 0 | 0 |
| `internal/dataframe/compiler` | 61 | 29 | 2 | 1 | 0 | 0 |
| `internal/dataframe/compiler/capability` | 1 | 0 | 0 | 0 | 0 | 0 |
| `internal/dataframe/compiler/ir` | 8 | 10 | 0 | 0 | 0 | 0 |
| `internal/dataframe/compiler/lower` | 28 | 6 | 1 | 1 | 0 | 0 |
| `internal/dataframe/compiler/optimize` | 0 | 1 | 0 | 0 | 0 | 0 |
| `internal/dataframe/compiler/render/aql` | 13 | 6 | 0 | 0 | 0 | 0 |
| `internal/dataframe/compiler/render/clickhouse` | 1 | 1 | 0 | 0 | 0 | 0 |
| `internal/dataframe/execution` | 19 | 11 | 0 | 0 | 0 | 0 |
| `internal/dataframe/execution/arango` | 4 | 2 | 0 | 1 | 0 | 0 |
| `internal/dataframe/execution/chartifact/arango` | 0 | 1 | 0 | 0 | 0 | 0 |
| `internal/dataframe/expression` | 1 | 0 | 0 | 0 | 0 | 0 |
| `internal/dataframe/fielddiscovery` | 1 | 1 | 0 | 0 | 0 | 0 |
| `internal/dataframe/lineage` | 1 | 0 | 0 | 0 | 0 | 0 |
| `internal/dataframe/publication` | 4 | 2 | 0 | 0 | 0 | 0 |
| `internal/dataframe/publication/arango` | 4 | 2 | 0 | 0 | 0 | 0 |
| `internal/dataframe/publication/clickhouse` | 7 | 4 | 0 | 0 | 0 | 0 |
| `internal/dataframe/published` | 2 | 3 | 0 | 0 | 0 | 0 |
| `internal/dataframe/recipe` | 7 | 4 | 0 | 0 | 0 | 0 |
| `internal/dataframe/recipe/arango` | 0 | 2 | 0 | 0 | 0 | 0 |
| `internal/dataframe/recipe/runtime` | 4 | 1 | 0 | 0 | 0 | 0 |
| `internal/dataframe/recipeplan` | 4 | 1 | 0 | 0 | 0 | 0 |
| `internal/dataframe/spec` | 2 | 0 | 0 | 0 | 0 | 0 |
| `internal/dataset` | 3 | 3 | 0 | 1 | 0 | 0 |
| `internal/dataset/arango` | 3 | 0 | 0 | 0 | 0 | 0 |
| `internal/explorer` | 12 | 5 | 0 | 0 | 0 | 0 |
| `internal/explorer/arango` | 8 | 15 | 0 | 1 | 0 | 0 |
| `internal/explorer/arango/capabilitydb` | 2 | 1 | 0 | 0 | 0 | 0 |
| `internal/explorer/arango/publicationdb` | 1 | 0 | 0 | 1 | 0 | 0 |
| `internal/explorer/authoringv2` | 6 | 10 | 0 | 0 | 0 | 0 |
| `internal/explorer/capability` | 3 | 0 | 0 | 0 | 0 | 0 |
| `internal/explorer/capability/resolver` | 1 | 2 | 0 | 0 | 0 | 0 |
| `internal/explorer/compilation` | 6 | 8 | 0 | 0 | 0 | 0 |
| `internal/explorer/construction` | 13 | 10 | 0 | 1 | 0 | 0 |
| `internal/explorer/constructioninputs` | 0 | 1 | 0 | 0 | 0 | 0 |
| `internal/explorer/importedsource` | 3 | 4 | 0 | 0 | 0 | 0 |
| `internal/explorer/lifecycle` | 41 | 24 | 2 | 1 | 0 | 0 |
| `internal/explorer/projectconfig` | 4 | 0 | 0 | 0 | 0 | 0 |
| `internal/explorer/proposal` | 2 | 0 | 0 | 0 | 0 | 0 |
| `internal/explorer/receipt` | 8 | 5 | 0 | 1 | 0 | 0 |
| `internal/explorer/recipeimport` | 2 | 7 | 0 | 0 | 0 | 0 |
| `internal/explorer/rowdef` | 2 | 0 | 0 | 0 | 0 | 0 |
| `internal/explorer/selection` | 1 | 0 | 0 | 0 | 0 | 0 |
| `internal/explorer/semanticauthoring` | 1 | 2 | 0 | 0 | 0 | 0 |
| `internal/explorer/workspace` | 9 | 14 | 0 | 1 | 0 | 0 |
| `internal/fhir/binding` | 0 | 1 | 0 | 0 | 0 | 0 |
| `internal/fhir/schema` | 3 | 1 | 0 | 0 | 0 | 0 |
| `internal/foundation/httpctx` | 1 | 0 | 0 | 0 | 0 | 0 |
| `internal/ingest/generation` | 7 | 8 | 0 | 0 | 0 | 0 |
| `internal/ingest/row` | 4 | 6 | 0 | 0 | 0 | 0 |
| `internal/ingest/source` | 1 | 0 | 0 | 0 | 0 | 0 |
| `internal/ingest/sourceload` | 2 | 2 | 0 | 0 | 0 | 0 |
| `internal/semantic` | 3 | 1 | 0 | 1 | 0 | 0 |
| `internal/semantic/arango` | 3 | 5 | 0 | 0 | 0 | 0 |
| `internal/semantic/backfill` | 1 | 1 | 0 | 0 | 0 | 0 |
| `internal/server` | 4 | 2 | 0 | 0 | 0 | 0 |
| `internal/store/arango` | 7 | 9 | 0 | 0 | 0 | 0 |

## Package candidates

Production selectors came from the files defining referenced symbols. Groupcheck reports distinct outgoing symbols and inbound file/symbol pairs. The one-way threshold allows up to three symbol relocations, subject to checking the affected dependencies.

| Test cluster | Test files | Selected production files | Groupcheck files / self / out / in | Assessment |
| --- | ---: | --- | --- | --- |
| Compiler category scan | 11 | `covering.go`, `execution.go`, `indexed.go`, `population.go`, `proof.go`, `related.go`, `scan.go` | 7 / 29 / 13 / 7 | Two-way. At least seven distinct inbound symbols. |
| Compiler row lineage | 7 | Compiler `execution.go`, compiler/rowlineage `compile.go`, `capability.go`, `identity.go` | 1 / 0 / 16 / 6; 3 / 14 / 0 / 0 | Compiler entry point is two-way. The latter selector is already a separate package. |
| Compiler correlated | 8 | Lower `correlation.go`, `orchestration.go`; FHIR binding `correlated.go` | 2 / 0 / 17 / 23; 1 / 0 / 0 / 1 | Lowering is two-way. FHIR binding is a one-way leaf with `correlatedPrimitiveCompatible` as the only incoming symbol. |
| Lifecycle construction | 20 | `authoring.go`, `catalog.go`, `column.go`, `frame.go`, `population.go`, `preview.go`, `query.go`, `routes.go`, `service.go`, `shared.go`, `surface.go`, `types.go` | 12 / 36 / 49 / 46 | Two-way across authoring, service, and preview. |
| Explorer OpenAPI | 24 | `authoring.go`, `construction.go`, `conversion.go`, `direct.go`, `handlers.go`, `openapi.go` | 6 / 18 / 15 / 7 | Two-way, with five distinct incoming symbols. |
| Explorer core authoring protocol | 7 | None directly in the API package | — | Cross-layer protocol tests. No API production selector identified by direct references. |
| Compiler physical plan | 19 | `diagnostics.go`, `execution.go`, `scan.go` | 3 / 3 / 14 / 9 | Two-way across diagnostics, execution, and scan. |

The broader screen grouped tests by the first two filename words within each package. It found 67 repeated prefixes, 65 with a same-directory production selector and two with cross-package coverage. The following results met the numerical threshold and were rechecked with module export data. Groupcheck measures a dependency surface. It does not prove a legal relocation or a cohesive package boundary. Raw outputs are under `.cache/testnames/`.

| Package | Cluster | Production files | Groupcheck files / self / out / in | Candidate assessment |
| --- | --- | --- | --- | --- |
| `internal/dataframe/compiler` | preview partition | `liveness.go`, `paging.go` | 2 / 1 / 7 / 2 | One incoming symbol across two files. Candidate for a narrower paging/liveness boundary. |
| `internal/dataframe/publication` | release verifier | `bundle.go`, `verifier.go` | 2 / 6 / 2 / 6 | Two outgoing symbols. Check whether the verification service and bundle model should share a boundary. |
| `internal/dataframe/recipeplan` | recipe plan | `build.go`, `model.go`, `nodes.go`, `validation.go` | 4 / 5 / 3 / 39 | Three outgoing symbols. The selector spans planning, node types, and validation, so a single responsibility needs further evidence. |
| `internal/explorer` | compilation receipt | `canonical.go`, `legacy.go` | 2 / 0 / 0 / 0 | No cross-boundary edges, but no edges within the selection either. The files are JSON utilities and legacy declarations. |
| `internal/explorer` | viewer projection | `projection.go`, `types.go` | 2 / 1 / 1 / 20 | One outgoing symbol. A narrower projection boundary should exclude revision types shared by the parent. |
| `internal/explorer/arango/capabilitydb` | capability snapshot | `cache.go`, `snapshot.go` | 2 / 7 / 2 / 0 | One-way toward the client interface. The package already owns capability snapshot persistence, so the result is evidence for its current boundary. |
| `internal/explorer/importedsource` | explorer imported | `edit.go`, `recipes.go` | 2 / 6 / 0 / 6 | One-way leaf. The package already owns imported sources and the two files have related editing and resolution responsibilities. |
| `internal/explorer/receipt` | explorer output | `reconcile.go` | 1 / 0 / 3 / 20 | Three outgoing symbols from reconciliation. Candidate for a narrower reconciliation boundary. |
| `internal/ingest/row` | generation identity | `builder.go`, `documents.go`, `generation.go` | 3 / 10 / 1 / 5 | One outgoing symbol. The selector spans document and row construction, so check which declarations belong to generation identity. |
| `internal/semantic` | member walk | `memberwalk.go` | 1 / 0 / 6 / 2 | Two incoming symbols. Candidate for isolated member traversal. |
| `internal/fhir/binding` | correlated | `correlated.go` | 1 / 0 / 0 / 1 | One incoming symbol. The 530-line file also defines extension bindings, so confirm the responsibility and import direction. |

The test files remain in their current packages. Layer ranks, declaration closure, and package ownership require a separate production review.

## Shared helper families

The reshape fixtures in `dataframe/compiler/lower` and `dataframe/execution` are identical except for the package clause. Construction and lifecycle repeat the `filterSource*` helpers. Explorer's workspace model fixtures reuse helper patterns from the workspace package. These are recorded for a future shared-helper decision.

## Relocations and deferrals

The initial map has no relocations or deferrals.

## Filename lint

The final unit will check internal test stems alongside production filenames. It will exclude testdata and recognized Go OS/architecture suffixes. Each deferred path will have an explicit reason in the lint allowlist.

## Phase 1 gate

- Test inventory covers every in-scope tracked file.
- The map validator checks destination spelling, package clauses, source line counts, collisions, and one assignment per test function.
- Go tool tests, vet, and formatting check the inventory command.
- Package test lists are captured under `.cache/testnames/baseline/`, with the timing/status line omitted from comparisons.
