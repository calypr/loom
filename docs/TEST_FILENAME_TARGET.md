# Test filename targets

Phase 1 covers 704 underscore test files in 67 packages at `1cda1995a`.
Phase 2 mapped 704 source files into 470 targets. The largest target is 1,487 lines. All 3,323 listed test entries matched their package baselines.

The inventory type-checks production and test variants, including external test packages. Its counts are direct references from test entry points to production declarations. The tool follows package-local test helpers and imported helpers from `internal/testkit` to recover environment lookups and skips. It also records production references used by package-local test helpers. The inventory is generated in `.cache/testnames/`.

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

The TSV contains zero relocation and defer rows. The allowlist contains only the `path` and `reason` header.

## Filename lint

The final lint checks internal production and test stems. It excludes testdata and strips recognized Go OS and architecture suffixes before checking the remaining stem. The allowlist is `scripts/filenames/testallowlist.tsv`, with a path and reason for every entry.

## Phase 1 gate

- Test inventory covers every in-scope tracked file.
- The map validator checks destination spelling, package clauses, source line counts, collisions, and one assignment per test function.
- Go tool tests, vet, and formatting check the inventory command.
- Package test lists are captured under `.cache/testnames/baseline/`, with the timing/status line omitted from comparisons.

## Package commits

Each package below matched its sorted baseline test list and passed declaration-token comparison, `go vet`, compile-only `go test`, and the package test suite. The per-unit logs and staged diffs are under `.cache/testnames/units/`.

| Commit | Package | Matched test entries |
| --- | --- | ---: |
| `bcccfebb7` | `internal/dataframe/artifactformat` | 9 |
| `bab3f4d28` | `internal/dataframe/compiler/capability` | 13 |
| `aef393a70` | `internal/dataframe/compiler/optimize` | 4 |
| `3de1882e2` | `internal/dataframe/execution/chartifact/arango` | 10 |
| `501f17ad2` | `internal/dataframe/expression` | 11 |
| `96468716b` | `internal/dataframe/lineage` | 2 |
| `351b513cf` | `internal/explorer/constructioninputs` | 9 |
| `5d88d2947` | `internal/explorer/selection` | 4 |
| `8c9d6fccb` | `internal/fhir/binding` | 20 |
| `0d31180c5` | `internal/foundation/httpctx` | 1 |
| `26087db1f` | `internal/ingest/source` | 8 |
| `18977c764` | `internal/dataframe/compiler/render/clickhouse` | 18 |
| `842084a71` | `internal/dataframe/fielddiscovery` | 6 |
| `5b80aa245` | `internal/dataframe/recipe/arango` | 8 |
| `882ff4924` | `internal/dataframe/spec` | 3 |
| `f6c7a5eea` | `internal/explorer/arango/publicationdb` | 7 |
| `02f6a77c0` | `internal/explorer/proposal` | 4 |
| `e8d0f8e9d` | `internal/explorer/rowdef` | 6 |
| `d2ebf54fb` | `internal/semantic/backfill` | 7 |
| `f33c7c5cb` | `internal/acceptance` | 34 |
| `984fdd2b8` | `internal/api/http` | 21 |
| `e11e232be` | `internal/dataset/arango` | 20 |
| `ac5792d23` | `internal/explorer/arango/capabilitydb` | 13 |
| `f05646edb` | `internal/explorer/capability` | 30 |
| `fbb8e8b26` | `internal/explorer/capability/resolver` | 25 |
| `fc5880ace` | `internal/explorer/semanticauthoring` | 6 |
| `64058a926` | `internal/catalog` | 12 |
| `42a8b58dd` | `internal/catalog/arango` | 22 |
| `d2f9bcea0` | `internal/explorer/projectconfig` | 16 |
| `d8ea81685` | `internal/fhir/schema` | 33 |
| `985a82c54` | `internal/ingest/sourceload` | 10 |
| `c67a75fac` | `internal/dataframe/published` | 38 |
| `0c5f03d62` | `internal/dataframe/recipe/runtime` | 14 |
| `7110af77c` | `internal/dataframe/recipeplan` | 48 |
| `67ec10d04` | `internal/semantic` | 30 |
| `c867ca6ed` | `internal/api/dataframe/query` | 31 |
| `bee0dee8a` | `internal/dataframe/publication` | 14 |
| `15396bd99` | `internal/dataframe/publication/arango` | 19 |
| `b8b3d7a1b` | `internal/server` | 37 |
| `288ac7009` | `internal/api/bulk/load` | 51 |
| `d317e75ef` | `internal/authscope` | 52 |
| `14142a047` | `internal/dataframe/execution/arango` | 18 |
| `3279068ba` | `internal/dataset` | 32 |
| `8c9ab34bb` | `internal/explorer/importedsource` | 28 |
| `8d88751e6` | `internal/semantic/arango` | 65 |
| `de52c463e` | `internal/api/dataframe/dataframe` | 50 |
| `dd5918acf` | `internal/explorer/recipeimport` | 45 |
| `e226559bf` | `internal/ingest/row` | 30 |
| `d43ad06a4` | `internal/catalog/fieldprofile` | 73 |
| `0f1a56dad` | `internal/dataframe/publication/clickhouse` | 23 |
| `7e6402903` | `internal/dataframe/recipe` | 82 |
| `3684067bd` | `internal/explorer/compilation` | 104 |
| `29f271fb1` | `internal/explorer/receipt` | 60 |
| `f0d2271ce` | `internal/ingest/generation` | 46 |
| `b8c4350cf` | `internal/explorer/authoringv2` | 178 |
| `41b900a87` | `internal/store/arango` | 61 |
| `cc075d6cf` | `internal/explorer` | 83 |
| `fbff285a3` | `internal/dataframe/compiler/ir` | 54 |
| `86e87b6f2` | `internal/dataframe/compiler/render/aql` | 79 |
| `56bcac855` | `internal/explorer/arango` | 71 |
| `196550315` | `internal/explorer/construction` | 71 |
| `88806823e` | `internal/explorer/workspace` | 106 |
| `c672f4155` | `internal/dataframe/execution` | 169 |
| `ffa694037` | `internal/dataframe/compiler/lower` | 180 |
| `9cd8fbde2` | `internal/api/explorer` | 138 |
| `209ab7dd3` | `internal/explorer/lifecycle` | 390 |
| `a2bda2cc6` | `internal/dataframe/compiler` | 361 |

## Wave verification

The integration checks used disposable localhost ArangoDB 3.12.13 and ClickHouse 25.3 containers. The Wave 1 failure reproduced in the two affected tests on the base test source. The per-wave failures are recorded below.

| Wave | Source snapshot | Command | Exit | Log |
| ---: | --- | --- | ---: | --- |
| 1 | `88806823e38a` | `make test-go` | 0 | `.cache/testnames/waves/1/test-go.log` |
| 1 | `88806823e38a` | `make test-integration` | 2 | `.cache/testnames/waves/1/test-integration.log` |
| 2 | `c672f4155b82` | `make test-go` | 0 | `.cache/testnames/waves/2/test-go.log` |
| 2 | `c672f4155b82` | `make test-integration` | 2 | `.cache/testnames/waves/2/test-integration.log` |
| 3 | `ffa6940376c4` | `make test-go` | 0 | `.cache/testnames/waves/3/test-go.log` |
| 3 | `ffa6940376c4` | `make test-integration` | 2 | `.cache/testnames/waves/3/test-integration.log` |
| 4 | `9cd8fbde2dd9` | `make test-go` | 0 | `.cache/testnames/waves/4/test-go.log` |
| 4 | `9cd8fbde2dd9` | `make test-integration` | 2 | `.cache/testnames/waves/4/test-integration.log` |
| 5 | `209ab7dd3ee2` | `make test-go` | 0 | `.cache/testnames/waves/5/test-go.log` |
| 5 | `209ab7dd3ee2` | `make test-integration` | 2 | `.cache/testnames/waves/5/test-integration.log` |
| 6 | `a2bda2cc6b8b` | `make test-go` | 0 | `.cache/testnames/waves/6/test-go.log` |
| 6 | `a2bda2cc6b8b` | `make test-integration` | 2 | `.cache/testnames/waves/6/test-integration.log` |

`TestSavedRelatedRouteRowsAgainstArango` returned HTTP 409 (`DRAFT_CONFLICT`) and `TestPublicationRowsAndFreshViewerAgainstArangoClickHouse` returned HTTP 503 (`PERSISTENCE_UNAVAILABLE` from a draft conflict) on both the base test source and the wave snapshots. The base log is `.cache/testnames/waves/base/draft-conflicts.log`. Because the Make target exits after the first group, the other recipes were run separately on the last wave snapshot.

| Supplemental check | Source snapshot | Exit | Log |
| --- | --- | ---: | --- |
| publication-reconcile | `a2bda2cc6b8b` | 0 | `.cache/testnames/waves/remainder/publication-reconcile.log` |
| draft-cas | `a2bda2cc6b8b` | 1 | `.cache/testnames/waves/remainder/draft-cas.log` |
| profile-driver | `a2bda2cc6b8b` | 1 | `.cache/testnames/waves/remainder/profile-driver.log` |

Publication reconciliation passed. The four draft CAS tests failed on both the final wave and the base test source, with the same draft-conflict behavior. See `.cache/testnames/waves/base/draft-cas.log`.

The profile driver stopped before running profiles because it still selects `./internal/dataframe/recipe/exec/arango`. Its backfill selector also uses the retired `./internal/ingest` package. Both required tests passed when invoked directly in their current packages, `./internal/dataframe/recipe/arango` and `./internal/semantic/backfill`. Those logs are under `.cache/testnames/waves/remainder/`.

## Verification notes

- The six oversized sources were split into whole functions under the strict target cap. The proposed plan had listed the three largest and allowed existing oversized files to keep their size.
- A fresh tracked-file check caught a formatting difference in `internal/dataframe/execution/scanner_test.go`. Go 1.27 gofmt corrected it, with matching declaration tokens and test list. The final checks were rerun.
- The first list comparison found the elapsed-time status line in only the baseline. The raw output was retained and the status line removed from both sides. The test names were identical.
- Go lists a synthetic test binary and black-box tests can import an augmented package. The inventory excludes the binary and loads augmented exports for those tests.
- The final inventory adds 28 environment records reached through imported test helpers. All actions, destinations, and direct production references match the Phase 1 map.
- Git detects all 297 standalone moves with `-M`. Merges into an existing destination appear as a source deletion plus a destination edit. One-to-many splits can appear as deletions and additions when similarity falls below the default threshold.
- Two diagnostic strings in the ClickHouse publication tests reference a former Arango test filename. They are test messages and do not select tests.

## Final checks

- Passed `make lint-filenames`.
- Passed `make lint-go-comments`.
- Passed `make test-go`.
- Passed `python3 scripts/package_audit_verify.py internal --full`.
- The regenerated package audit has the same dependency and importer columns as the base for all 137 packages.
- Final command logs are under `.cache/testnames/final/`.
