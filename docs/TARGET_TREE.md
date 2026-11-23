# Target tree

Status: **synthesis complete for all 53 audits; awaiting operator approval.**
Stage 0–2 only. No production code has been edited.
Base snapshot: `0fedd22` (isolated copy of the plan's `f3c878aec` tree).
Source of truth: `docs/GARDENING_PLAN.md`. Evidence: `.cache/garden/audit/`,
`.cache/garden/ledger.tsv`, `.cache/garden/synthesis_notes.md`.

The mechanical target graph is `.cache/garden/filemap.tsv` (722 import-bearing production
files) plus `.cache/garden/rewrites.tsv` (the import edits that no file move can express).
`mapcheck.py` reports **85 target packages, 460 edges, 0 cycles, 0 layer violations,
0 forbidden edges** (`.cache/garden/checker_target.txt`).

## Before (measured)

- 80 packages, 751 production files, 185,724 production lines.
- 450 receivers; 13 with >=20 methods, 7 with >=25.
- 82 interfaces; 1,686 structs, 94 with >=15 fields; 41 with >=20 fields.
- 3,860 functions; 1,906 methods; 92 functions >=150 lines, 10 >=300; 72 with >=8 params.
- 41 filename clusters, 15 catch-all files, 4,676 exported declarations.
- 826 `internal/` test files, all white-box `package X` (five largest packages: 216 test files,
  212 white-box).

## Execution status

Landed on `internal-cleanup`:

- 3A dead-code deletion.
- 3C: `foundation/httpctx`, `dataframe/errors/authpolicy`, `server/sourcepublication`, `dataframe/recipeauth`, `dataframe/fielddiscovery`, `authscope` option move, `recipe/exec` merge, `viewbuilder` merge.
- 3D: `catalog/discoverycache`, `internal/digest`, `explorer/config`, `explorer/selection`, `dataframe/semantic` -> `dataframe/recipeplan`, semantic package consolidation, ingest root split into `row`/`identity`/`source`/`bootstrap`/`event`, compiler facade, `cmd/arango-maintenance` merge.
- 3C real moves (facade removal): semantic inventory/coverage persistence into `semantic/arango`; authoring workspace model, codec, and stored migrations into `explorer/workspace`; semantic selection planning and route materialization into `explorer/semanticauthoring`; selection persistence and codec into `explorer/arango/selectiondb` + `explorer/arango/db`; revisions and explorer CRUD into `explorer/arango/workspacedb`; receipt proposal bindings, compilation receipt, and workspace method into `explorer/receiptmodel`; project config model and canonical JSON into `explorer/config`; view-builder DTOs into `explorer/viewer`; explicit-group model into `explorer/selection`; interpretation model into `explorer/interpretation`; workspace projection DTOs into `explorer/workspace`; receipt, authoring-publication, activation, and view-builder persistence into `explorer/arango/publicationdb`; workspace audit into `explorer/arango/workspacedb`.

Still pending: lifecycle splits, 3E shapes, 3F interfaces, 3U UI, full acceptance.

Optional-target decisions:
- `dataframe/compiler/lower/recipe`: skipped. `TARGET_TREE.md` open question 9 still requires operator approval, and the lower package’s parent-to-child helper calls make the split non-move-only without first extracting shared compiled types.
- `api/dataframe/recipeexec`: implemented.

## Allowed layers

Rank 0 `projectid`, `authscope`, `fhir/*`, `foundation/*` -> rank 1 `store/*` ->
rank 2 `catalog`, `dataset`, `semantic` -> rank 3 `ingest` -> rank 4 `dataframe` ->
rank 5 `explorer` -> rank 6 `api/*` -> rank 7 `server`, `cmd/*`.
`testkit` and the `ci`/`acceptance` harnesses are exempt. An Arango adapter is imported only
by its domain's composition root and `server`; it never imports a use-case package.

Two rules are enforced beyond rank:

1. `explorer` may not import `dataframe/compiler/{ir,lower,capability}` directly; it imports
   `dataframe/compiler/facade`.
2. `ingest/sourceload` may not import `explorer` or `dataframe`; its publication adapter moves
   to `server/sourcepublication`.

## Target packages

### Foundation, storage, catalog, semantic

| Package | Responsibility | Main types / files |
| --- | --- | --- |
| `projectid` | Canonical/legacy project and generation identity | `Canonical`, `NormalizeGeneration` (from `catalog/generation.go`) |
| `authscope` | Principal, authn/authz, Fence access, resource-path scopes | `Principal`, `ScopeResolver`, `AuthResourcePathOptions` (from `catalog`) |
| `fhir/schema` | Generated FHIR schema + compiler-semantic helpers | schema lookups |
| `fhir/binding` (new) | Correlated binding, identifier, quantity, pivot helpers | `Correlated`, `Identifier`, `Quantity`, `Pivot` |
| `foundation/httpctx` (new) | Request-ID context and generic diagnostic logging | `RequestIDFromContext`, `ContextWithRequestID`, `LogDiagnostic` |
| `store/arango` | Arango client, query/batch/transaction ports | `RowQueryer`, `TransactionalRowQueryer`, `SelectionMembersCollection` |
| `store/clickhouse` | ClickHouse client, DDL, JSON values, query | client, `RenderCombine` caller |
| `catalog` | Shared catalog identity/structural facts/read cache | `RelationshipKey`, `Cache`, catalog types |
| `catalog/discoverycache` (new) | Bounded discovery cache | `Cache` |
| `internal/digest` (new) | Catalog/semantic digest helper | `Digest` |
| `catalog/fieldcatalog` (new) | Field-profile read/write DTOs | `FieldCatalogDocument`, `PopulatedField` |
| `catalog/fieldprofile` | Profiler and field-statistics writer | `Profiler` (semantic emission shrinks) |
| `catalog/arango` | Catalog adapters over shared collections | `FieldStore`, `RelationshipStore`, `ResourceStore` |
| `semantic` (new) | FHIR member walk, coded-concept extraction, inventory, coverage | `WalkMembersWithResourceTypes`, `Extractor`, `Build`, `EntryAggregator`, `ReconcileSemanticCoverage` |
| `semantic/arango` (new) | Semantic inventory/coverage persistence | `SemanticInventoryStore`, `SemanticCoverageStore` |
| `semantic/backfill` (new) | Semantic inventory backfill use case | backfill worker |

`catalog/codedconcept`, `catalog/semanticinventory`, and `fhir/semantic` are absorbed by
`semantic`; `fieldprofile/semantic_coverage.go` moves with them. This removes the
`semanticinventory -> codedconcept` and `fieldprofile -> fhir/semantic` edges and gives the
pipeline one owner. `dataframe/semantic` is a different concept and is renamed
`dataframe/recipeplan`.

### Dataset and ingest

| Package | Responsibility | Main types |
| --- | --- | --- |
| `dataset` | Manifest/generation/schema/release model and active-release policy | `Ref`, `Manifest`, `SchemaSnapshot`, `ReleaseService` |
| `dataset/arango` | Manifest/release/active-generation persistence | `Store`, CAS release methods |
| `ingest/row` (new) | Row builder, generated load, FHIR documents, integer64, row errors | `RowBuilder`, document encoders |
| `ingest/identity` (new) | Generation identity | `GenerationIdentity` |
| `ingest/source` (new) | File discovery/source preflight/schema | preflight, source schema |
| `ingest/bootstrap` (new) | Arango collection/index bootstrap | backend specs |
| `ingest/event` (new) | Load progress events | progress |
| `ingest/generation` | Generation orchestration and NDJSON pipeline | orchestration, file loader |
| `ingest/sourceload` | Leased source-load state machine | `RepositorySourceLoader` |

The root `ingest` package disappears after these moves. `ingest/backfill` moves to
`semantic/backfill`; the `source_publication_adapter.go` moves to
`server/sourcepublication`; `server/fielddiscovery` is not kept — the discovery adapter moves
to `dataframe/fielddiscovery`.

### Dataframe

| Package | Responsibility | Main types |
| --- | --- | --- |
| `dataframe/compiler` | Compiler facade and public entry points | `Compile*`, `RenderPhysicalPlan` |
| `dataframe/compiler/facade` (new) | The only compiler surface used by `explorer` | aliases/wrappers for `ir`, `lower`, `capability` |
| `dataframe/compiler/ir` | Physical plan model and validation | `PhysicalPlan`, stage sequence |
| `dataframe/compiler/ir/scope` (new) | Whole-relation/workspace-combine scope evidence | `WholeRelationScopeEvidence` |
| `dataframe/compiler/lower` | Recipe/construction lowering + lowering context | `constructionLowerer`, `CompiledRecipeOutput` |
| `dataframe/compiler/lower/recipe` (conditional) | Recipe lowering cluster | `CompileResolvedRecipePlan` |
| `dataframe/compiler/render/aql` | AQL rendering and sub-renderers | `physicalPlanRenderer` |
| `dataframe/compiler/render/aql/internal/aqlsyntax` (new) | Pure AQL syntax helpers | path/identifier helpers |
| `dataframe/compiler/rowlineage` (new) | Row-lineage capability/compile/identity | `CompileRowLineageOutput` |
| `dataframe/compiler/optimize` | Physical-plan optimization | `OptimizePhysicalPlanWithPolicy` |
| `dataframe/compiler/capability` | Compiler capability probes | `Probe*` |
| `dataframe/compiler/render/clickhouse` | ClickHouse combine rendering | `RenderCombineWithLimit` |
| `dataframe/recipe` | Recipe model/runtime/execution adapters; absorbs `columntransform` and `recipe/exec` | `Bundle`, construction model, `PersistentRegistry` |
| `dataframe/recipe/arango` (new) | Recipe/revision Arango adapter (from `recipe/exec/arango`) | `Registry`, `RevisionRegistry` |
| `dataframe/recipeauth` (new) | Recipe read/write authorization; removes runtime→api/catalog edges | `Authorization`, `AuthorizeRead`/`Write` |
| `dataframe/recipeplan` (rename) | Recipe-to-plan lowering formerly `dataframe/semantic` | `BuildRecipePlan`, `SemanticPivot` |
| `dataframe/lineage` | Stable source-child identity (kept separate) | `SourceChild`, `Coordinate` |
| `dataframe/expression` | Expressions; `recipe_adapter.go` moves to `recipeplan` | expression AST |
| `dataframe/execution` | Execution engine | `Engine` |
| `dataframe/execution/arango`, `chartifact*` | Execution adapters | adapters |
| `dataframe/published` | Published-output reads | reader, filters, cursors |
| `dataframe/publication` | Materialization/publication transaction | `BundleCatalog`, `Transaction` |
| `dataframe/fielddiscovery` (new) | Field/schema discovery adapter | `FieldDiscovery`, recipe schema resolver |
| `dataframe/spec`, `unit`, `errors`, `artifactformat` | Leaf contracts | small |

`dataframe/compiler/ir`, `lower`, and `capability` remain packages; the target adds a facade
rather than renaming them. Moving their definitions under `compiler/internal` is a later,
optional step (it forces every non-explorer consumer through the facade).

### Explorer

The following splits are accepted, but are **Phase 3D, conditional** on extracting shared
lifecycle support first. They are not encoded as file moves in `filemap.tsv` because doing so
would create the cycles the audits warn about.

| Package | Responsibility |
| --- | --- |
| `explorer` | Explorer aggregate, shrunk `Store` ports, workspace projection |
| `explorer/workspace` (new) | Versioned authoring document model, canonical codec, stored migration |
| `explorer/config` (new) | Project/view config DTOs and validation; absorbs `viewbuilder` service |
| `explorer/receiptmodel` (new) | Compilation receipt, output contract, archive metadata |
| `explorer/viewer` (new) | Viewer state/runtime DTOs and viewer projection |
| `explorer/reconcile` (new) | Generic ticker reconciler loop shared by artifact/ClickHouse managers |
| `explorer/construction` (new) | Construction base/choice/group/pivot/category/proposal/routes/column population |
| `explorer/rowdef` (new) | Row definition, choices, comparison, proposal, value policy |
| `explorer/proposal` (new) | Proposal-receipt verification and propose+preview |
| `explorer/interpretation` (new) | Interpretation revisions/context/preview |
| `explorer/population` (new) | Population routes, mapping reports, cursors |
| `explorer/selection` (new) | Selection intents, persistence, readers, cursors |
| `explorer/semanticbrowse` (new) | Semantic inventory browse, field presentation, frame/schema-field browse |
| `explorer/rowsource` (new) | Derive/Expand/Filter/Unpivot authoring lanes |
| `explorer/explicitgroup` (new) | Explicit-group revision resolution/creation |
| `explorer/semanticauthoring` (new) | Semantic selection planning and route materialization |
| `explorer/lifecycle` | Residual orchestration, publication, artifacts, network, shared support |
| `explorer/lifecycle/ports` | Consumer-owned ports (kept; shared by 6+ packages) |
| `explorer/receipt` | Receipt contract, validation, execution adapters; reconciliation split by operation |
| `explorer/compilation` | Workspace compilation |
| `explorer/authoringv2` | Command application and proposals; imports `explorer/workspace` |
| `explorer/arango` | Explorer adapters; per-aggregate stores split by file first |
| `explorer/projectconfig` | Project/view config preparation and view-builder service (absorbs `viewbuilder`) |
| `explorer/artifactfs`, `constructioninputs` | Artifact store and construction-input read model |
| `explorer/recipeimport`, `importedsource` | Import translation and source runtime |
| `explorer/capability`, `capability/resolver` | Capability evidence and resolution |

Prerequisite for the split: `lifecycle/support` (errors, policy, authoring network, route
helpers), `constructionBase`, `rowSourceLaneReasons`, and a store port for
`getOwner`/`currentWorkspace` move first. Otherwise the new packages import `lifecycle` and
`lifecycle` imports them.

### API, server, platform

| Package | Responsibility |
| --- | --- |
| `api/explorer` | Generated explorer HTTP adapters |
| `api/dataframe`, `api/dataframe/dataframe`, `api/dataframe/query` | Dataframe HTTP/read/query adapters |
| `api/dataframe/recipeexec` (new, optional) | Recipe-execution responses/errors |
| `api/bulk/load` | Generation/source-load adapters |
| `api/http` | HTTP shell, middleware, error mapping |
| `dataframe/errors/authpolicy` (move) | Auth-error classification; removes `recipe/runtime -> api` |
| `server` | Composition root, config, lifecycle |
| `server/sourcepublication` (new) | Source publication adapter formerly in `ingest/sourceload` |
| `cmd/arango-fhir-server` | Server entry |
| `cmd/arango-fhir-proto` | Operator CLI |
| `cmd/generate`, `cmd/check-go-comments`, `cmd/dataframe-*`, `cmd/loom-*` | Generators/checks/CLIs |
| `cmd/arango-maintenance` (new) | Merge of three one-off Arango commands |
| `cmd/loom-repository/seed` | `repositoryseed` preflight, moved beside its only consumer |
| `testkit/explorerfixtures`, `recipefixtures`, `arangotest` | Test support (merged) |
| `tests/ci` | `internal/ci` gates, moved out of production tree |

### UI

UI has no Go import graph; these are source-import-level recommendations.

- `src/contracts/{primitives,selection,interpretation,trace,importedSource,construction,builder,semantic,rowChange,runtime}`:
  split `types.ts` by dependency layer, not one file per schema.
- `src/api/{transport,cache,errors,explorer,construction,interpretation,selection,output,savedViews}`:
  split `createLoomClient` (791 lines).
- `src/data/react.ts`, `src/savedViews/`, `src/display/`: move root React/query, saved-view, and
  display helpers out of `src/` root.
- `features/ExplorerViewer/components/{FilterRail,ChartPanel,OutputTable,PageControls}.tsx`:
  split the 425-line `components.tsx`; move `Viewer.tsx` orchestration into the feature.
- `features/ExplorerBuilder/workspace/`: split `BuilderWorkspace.tsx` (2,369 lines) into shell,
  selection-owner store, population view, advanced rules, and source inspector.
- `features/ExplorerBuilder/{sourceRules,catalog,traversal,rowDefinition,preview,population,review}`:
  group the current `components/` grab bag by domain.
- `features/ExplorerBuilder/authoring/{state,routes,recovery,network,contracts}` and
  `constructionWorkspace/{shell,proposal,presentation,sources}`: break the current
  `authoring` <-> `constructionWorkspace` cycle by moving shared types into `authoring/contracts`.
- `features/ExplorerBuilder/constructionOperations/reshape/`: split the 1,852-line
  `reshapeForms.ts` and the 1,587/1,648-line editor files.
- Delete unused UI barrels/helpers and the dead `FeaturePolicyEditor`, `DataframeContractPanel`
  component body, `ColumnSourceInspector`, `IconGripVertical`, `columnLabel.ts`,
  `datasetReviewSelection.ts`, and `useDismissibleLayer` re-export after a TypeScript
  unused-export pass.

## Package segmentation matrix

Net package count is a misleading metric: 39 new packages are offset by merges/absorptions
(`testkit`×4, `recipe/exec`, `viewbuilder`, `artifactfs/arango`, `chartifact/arango`, root
`ingest`, `fhir/semantic`, `codedconcept`, `semanticinventory`). The table records the real
split decision for each bulky package and the reason when a split stops at file level.

| Current package | Prod files / lines | Segmentation decision | Gate |
| --- | ---: | --- | --- |
| `explorer/lifecycle` | 72 / 23,738 | Split package: `construction`, `rowdef`, `proposal`, `interpretation`, `population`, `selection`, `semanticbrowse`, `rowsource`, `explicitgroup`, `semanticauthoring`, plus `lifecycle/support` | Extract shared support/store ports first; 83 white-box tests |
| `explorer/authoringv2` | 47 / 15,837 | Split package: `explorer/workspace` (model/codec/migration); commands stay; `semanticauthoring` takes semantic policy | Move `Workspace`/`Document` methods and fixtures together |
| `explorer` | 27 / 5,039 | Split package: `config`, `selection`, `receiptmodel`, `viewer`; `artifact` candidate | Mostly seam-ready; receipt model waits on `Store` split |
| `explorer/arango` | 24 / 5,564 | Split package: `db`, `workspacedb`, `capabilitydb`, `selectiondb`, `publicationdb` | File split first; 33 white-box tests pin private keys |
| `explorer/receipt` | 15 / 3,248 | No new package: split reconciliation files by operation (group/related/reshape/transform) | `authoredOutput*` helpers are internal |
| `dataframe/compiler/render/aql` | 45 / 15,304 | Package split deferred: `aqlsyntax` now; `reshape`, `lineage`, `category` second wave | Requires `renderContext`/`physicalPlanRenderer` extraction; 21 white-box tests |
| `dataframe/compiler/ir` | 41 / 12,290 | Package split deferred: `ir/scope` now; `physical`, `validate` second wave | `PhysicalPlan` anchors validation; exhaustiveness test scans the directory |
| `dataframe/compiler` | 28 / 7,578 | Split package: `model`, `categoryscan`, `lineage`, `population`, `preview` (second wave) | All 94 tests are white-box; `CompiledQuery` aliased by `execution` |
| `dataframe/compiler/lower` | 23 / 12,100 | Package split deferred: `physical` (primitives) second wave; `group`/`recipe` need shared compiled types | Parent calls child helpers today |
| `dataframe/execution` | 21 / 5,259 | No new package: split `Engine` into `resolver`/`queryBackends`/`previewCache` collaborators; merge chartifact Arango adapter | Private preview/stream state would cycle across packages |
| `dataframe/recipe` | 32 / 5,806 | Split package: `recipe/arango`, `recipeauth`, `fielddiscovery`; absorb `recipe/exec` | `recipe/runtime` is split, not merged |
| `catalog/arango` | 10 / 5,222 | Split package: `semantic/arango` now; `fields`, `relationships`, `resources` second wave | Tests pin collection/AQL names |
| `catalog/fieldprofile` | 8 / 3,440 | Split package: `fieldcatalog` (DTOs) and `semantic` (coverage) | Copy `isLoomMetadataField` to avoid a cycle |
| `ingest` root | 13 / 1,320 | Split package: `row`, `identity`, `source`, `bootstrap`, `event` | White-box tests move with files |
| `api/explorer` | 25 / 3,362 | No package split: one generated `StrictServerInterface`; optional `previewwire`/`wiremap` only | 60 white-box tests; one external importer (`server`) |

### Second-wave package splits (not in the mechanical filemap)

These are real segmentation targets, but they are gated on exporting a small seam and converting
white-box tests. They should be scheduled only after the first wave lands.

- `dataframe/compiler/render/aql`: `aql/core` (`renderContext`, `physicalPlanRenderer`,
  `RenderedPhysicalPlan`), then `aql/{reshape,lineage,category}` children.
- `dataframe/compiler/ir`: `ir/physical` (stage types + validation) and `ir/validate`, with
  `PhysicalPlan` moved to the core package.
- `dataframe/compiler`: `model` (`CompiledQuery` + result structs), `categoryscan`, `lineage`,
  `population`, `preview`; root becomes the facade.
- `dataframe/compiler/lower`: `physical` primitives (traversal, storage route, predicates,
  literals, naming); keep group/recipe lowering until compiled types move.
- `dataframe/execution`: optional `executiontest` for `Control`; otherwise collaborators stay
  in-package.
- `dataframe/recipe/runtime`: split `recipe_publication.go` to `dataframe/publication` if the
  publication layer is declared above runtime.
- `catalog/arango`: per-concept packages once the 13 white-box tests are decoupled.
- `api/explorer`: `previewwire` and `wiremap` only if exporting the codecs is acceptable.
- UI: the directory plan in the UI section is already the equivalent package split.

## File moves applied to the target map

| Current | Target | Action |
| --- | --- | --- |
| `fhir/semantic`, `catalog/codedconcept`, `catalog/semanticinventory` | `semantic` | whole-package move |
| `catalog/fieldprofile/semantic_coverage.go` | `semantic` | split/move |
| `catalog/arango/semantic_{inventory,coverage,coverage_envelope}.go` | `semantic/arango` | move |
| `dataframe/semantic/*` | `dataframe/recipeplan` | rename |
| `dataframe/semantic/execution_context.go` | `dataframe/compiler/lower` | move |
| `dataframe/expression/recipe_adapter.go` | `dataframe/recipeplan` | move |
| `dataframe/columntransform/*` | `dataframe/recipe` | merge |
| `fhir/schema/{correlated,identifier,quantity,pivots}.go` | `fhir/binding` | move |
| `catalog/{auth_scope.go,generation.go,cache.go}` | `authscope`, `projectid`, `catalog/discoverycache` | move |
| `catalog/identity/*` | `internal/digest` | move (promoted for semantic reuse) |
| `ingest/*` row/source/bootstrap/event files | `ingest/{row,source,bootstrap,event}` | split |
| `ingest/backfill/*` | `semantic/backfill` | move |
| `api/authpolicy/*` | `dataframe/errors/authpolicy` | move |
| `api/http/request_id.go` | `foundation/httpctx` | move |
| `ingest/sourceload/source_publication_adapter.go` | `server/sourcepublication` | move |
| `dataframe/recipe/runtime/{discovery,recipe_discovery}.go` | `dataframe/fielddiscovery` | move |
| `repositoryseed/*` | `cmd/loom-repository/seed` | move |
| `testkit/{recipetest,capabilityfixtures,constructionfixtures,httpfixtures}` | merged testkit packages | merge |
| `dataframe/recipe/exec/*` | `dataframe/recipe` | merge |
| `dataframe/recipe/exec/arango/*` | `dataframe/recipe/arango` | move |
| `dataframe/recipe/runtime/recipe_authorization.go` | `dataframe/recipeauth` | move |
| `dataframe/execution/chartifact/arango/*` | `dataframe/execution/arango` | merge |
| `dataframe/execution/publication_schema.go` | `dataframe/recipe/runtime` | move |
| `explorer/viewbuilder/*` | `explorer/projectconfig` | merge |
| `explorer/{config_v2,project_config,view_builder}.go` | `explorer/config` | move |
| `explorer/{compilation_receipt,compilation,output_contract,receipt_workspace,recipe_archive_metadata}.go` | `explorer/receiptmodel` | move |
| `explorer/{state_v1,viewer_projection}.go` | `explorer/viewer` | move |
| `explorer/{selection,explicit_groups,explicit_group_repository}.go` | `explorer/selection` | move |
| `explorer/artifactfs/private_artifact_reconciler.go` | `explorer/reconcile` | move |

Import rewrites in `.cache/garden/rewrites.tsv`: explorer -> compiler facade; execution/receipt
-> `foundation/httpctx`; recipe runtime -> `store/arango`; authscope -> `projectid`.

## Merge list

- `authoringv2`: stored archive -> stored migration; source rule -> source plan; availability
  -> catalog section; output dependencies -> current workspace; command halves of framing,
  column policy, source commands -> command apply.
- `render/aql`: selector -> selectors; correlation value scope -> predicates; combine prefix ->
  compiler; dynamic + streaming reshape -> table reshape; related count + group preview ->
  related frontier; cell trace + population mapping -> terminals.
- `ir`: combine scalar -> combine; category related scan -> category scan; navigation -> prefix;
  selector helpers -> validation refs; dataset generation -> scope; five stage-validation files
  -> one; four operation-validation files -> one.
- `lower`: combine metadata -> combine; recipe expression -> expression lowering; column
  transformations + derived columns -> one column-ops file.
- `lifecycle`: errors + capability failure -> failures; explicit-group resolver -> adapter or
  explicit-groups; field browse -> browse; schema-field candidate -> schema fields.
- `catalog`: rebuild -> relationships; options aliases -> capability evidence; record-type
  constants -> inventory types; evidence -> fields/relationships/resources; generic decoders ->
  `decode.go`.
- `dataframe`: publication column capabilities -> published; `semantic` file-level plan/shaping
  split after tests decouple; `recipe/exec` -> `recipe`; `execution/chartifact/arango` ->
  `execution/arango`; `execution/publication_schema.go` -> `recipe/runtime`.
- Explorer: `viewbuilder` -> `projectconfig`; `view_builder_catalog` -> `viewbuilder`; config DTOs
  -> `explorer/config`; receipt model files -> `explorer/receiptmodel`; viewer DTO/projection ->
  `explorer/viewer`; root selection/explicit groups -> `explorer/selection`.
- UI: result-unit display -> value display; saved-view hooks into saved views; chart-type lists
  into one module; `SourcePickerExplanation` -> construction operations; population modules into
  one feature; `RowChangePreviewPanel` -> `ConstructionProposalPreview`.

## Delete / unexport list

Every Go item is a **syntactic candidate: confirm with `deadcode` on Go 1.27 and re-check
reflection, init, registration, and interface satisfaction before deletion.** No deletion is
part of Stage 0–2.

- Entry/tooling: root `main.go` (byte-identical duplicate of `cmd/arango-fhir-server/main.go`);
  `scripts/normalize-ingest-perf-capture.py`; `scripts/lib/builder-route-evidence.mjs`;
  `scripts/verification-disposition/CALLER_INVENTORY.md` -> docs.
- Empty/support: `internal/ingest/doc.go`, `dataset/doc.go`, `dataset/arango/doc.go`,
  `optimize/doc.go`, `render/aql/doc.go`; empty `catalog/identity` is replaced by digest.
- `semantic`: `Freeze`/`DynamicSpec`/`FrozenSchema`, `WalkMembers`,
  `WalkMemberValuesWithResourceTypes` (0 production callers; tests only).
- `lifecycle`: `RowDefinitionComparisonUnavailable`, `ResolvedPopulationRouteChoice`,
  `ConstructionDependencyIssue`, `ConstructionDependencyImpact`,
  `FieldPresentationNoExactRoute`, `FieldPresentationStaleContext`, `firstNonEmpty`,
  `MembershipDigest` (unexport), `PopulationMappingResult` wrapper.
- `authoringv2`: `ConstructionPivotDuplicateMin`, `ConstructionDivisionByZeroError`, five
  `ConstructionFilter*` constants; test-only `Document.AnalyzeStepEdit`/`ProposeStepRemoval`;
  unexport zero-ref transformation/capability helpers and command constants.
- `dataframe`: `RenderPhysicalPlanWithTerminalProjection`, `RenderClickHousePrefix`,
  `NewPrivateAQLPrefixScopeOperand`, `LowerRecipeExpression`, `ComposeClickHouseCombine`,
  `ExportFormat`/`Export*`/`AggregationSpec`/`AggregationsRequest`, `PopulationMappingCompiled`,
  `ReaderCatalog`, `BundleClickHouseStore`, `PopulationMemberReader`, `ConstructionChoiceSource`
  (unexport marker), `InterpretationCandidateResolution`.
- `catalog/arango`: `ErrManifestNotFound` alias; unused record-type constant; unexport
  zero-caller response aliases.
- `dataset`: `Config.SkipPerformance` (never read), `PublicationVerification.PhysicalTable`
  (write-only), `WriteLock` (unexport), `NewSchemaSnapshot`/`NewManifest` (unexport if no
  external constructor is intended).
- `store`: `clickhouse.AddColumn`, `arango.ExplainIndexUse`; unexport profile/explain parsers.
- Execution: `Engine.PreviewBundle`, `MaterializeBundle`, `MaterializeVersion`, `Materialize`,
  `PreviewResolvedBundle`, `ResolveVersion` (0 production refs); `TableShapeExclusions` wrappers
  unless wired; move test-only `Control` DTOs into `_test.go`.
- Recipe: `FragmentLibrary.SortedNames` (0/0/0); unexport `runtime.FieldDiscovery`.
- Explorer root: `project_config_column.go` (dead duplicate of `projectconfig/columns.go`),
  `ErrStaleCatalog`, `ErrIncompleteCatalog`; unexport `PrepareProjectConfigs`,
  `InitialProjectConfigForRecipe`, viewbuilder storage methods, `ErrArtifactUnavailable`.
- Capability: `OperationSort`, `FilterIn`, `FilterExists`, `Chart*` constants,
  `Snapshot.IsUsable` (0/0/0) after contract check.
- `api`: `ErrorResponse`/`HTTPErrorBody`/`MappedError`/`ServiceErrorResponse` (unexport),
  `authscope.InvalidateGeneration`, `authscope.ResolveReadScope`, `Permission*`, config types
  in `server` (unexport; only `Run` has external callers).
- UI: see the UI section and `audit/ui-*.md`.

## Interface changes

- Split `explorer.Store` (19 methods) into explorer CRUD, receipt, revision/activation,
  selection, and draft ports. Move `ViewBuilderRepository`, `ProjectConfigPublisher`, and
  `ExplicitGroupRepository` to their consumers. Shrink fixtures accordingly.
- Move `dataset.ActiveReleaseResolver` no further; keep it as the 1-method read port.
- Split `publication.BundleCatalog` (10 methods) into execution/pointer/lease/read-pin ports;
  delete `published.ReaderCatalog` (0 methods) and use a 4-method read port.
- Collapse single-implementation lifecycle interfaces (`ExplicitGroupRevisionResolver`,
  `PopulationMappingCursorCodec`) to concrete types; keep `SelectionSourceResolver`,
  `SelectionPublishedReader`, `PopulationMemberReader`, `RowChoice*` as real consumer ports.
- Split `authoringv2.Command` (47 fields, 43 methods) into an envelope plus sealed payload
  variants; flatten on marshal. This is behavior-sensitive and belongs to Phase 3E.
- Unexport the private sum markers used only for serialization discrimination.
- Improve `authscope.Authenticator`/`Authorizer` consumers; no shape change needed.
- Replace `explorer/receipt`'s concrete `*execution.Engine` dependency with a consumer-owned port
  for the eight methods it calls; same for `importedsource.ResolveBundle`.
- Split `capability.ConstructionChoiceSource` from a marker interface into a tagged union; keep
  the serialized discriminators.
- `execution.PopulationMemberReader` -> function type; `chartifact.Catalog`/`ClickHouse` stay.

## Layering repair

The target map removes all seven plan inverted edges and the extras:

| Edge | Repair |
| --- | --- |
| `recipe/runtime -> api/authpolicy` | move authpolicy to `dataframe/errors/authpolicy`; move auth adapter to `dataframe/recipeauth` |
| `recipe/runtime -> catalog*` | move discovery adapter to `dataframe/fielddiscovery` |
| `recipe/runtime -> explorer/arango` | move `SelectionMembersCollection` to `store/arango` |
| `execution/arango -> api/http`, `explorer/receipt -> api/http` | move request-ID/diagnostics to `foundation/httpctx` |
| `ingest/sourceload -> explorer*`, `-> dataframe/*` | move publication adapter to `server/sourcepublication` |
| `authscope -> catalog` | move `AuthResourcePathOptions` to `authscope` and generation normalization to `projectid` |
| `explorer -> compiler/{ir,lower,capability}` | import `dataframe/compiler/facade`; rule is encoded as a rewrite |

## Verification

- Stage 0: `scan.tsv` (80 packages, 1,577 files, 0 parse errors); headline counts above.
- Stage 1: 53/53 audit units accepted. Each accepted report had three claims rerun against
  `scan.tsv`/source; corrections were returned to the same auditor.
- Stage 2: `filemap.tsv` + `rewrites.tsv` + `mapcheck.py` -> 85 packages, 460 edges, 0 cycles,
  0 layer violations, 0 forbidden edges. Baseline checker: 77 packages, 469 edges, 29 violations.
  The map applies 83 file moves and 5 whole-file deletes; symbol-level deletions are listed above
  but intentionally left out of the mechanical map.
- Not verified: compilation, tests, runtime behavior, UI typecheck, `deadcode` (Go 1.27 and
  network unavailable). No production code changed, so no behavior claim is made.

## Open questions for the operator

1. Approve the new `semantic`/`semantic/arango` domain and the `dataframe/recipeplan` rename,
   rather than keeping `catalog/semantic` and `dataframe/semantic`.
2. Approve the explorer Phase 3D split order, or keep `lifecycle`/`authoringv2` intact and apply
   only the file-level merges. The split needs shared-support extraction first.
3. Approve `dataframe/compiler/facade` as the explorer boundary, or internalize `ir`/`lower`/
   `capability` under `compiler/internal` and migrate all non-explorer consumers too.
4. Is `cmd` maintenance live? Root `main.go`, the three one-off Arango commands, and the
   local-preview migration are deletion candidates only after migration records are closed.
5. Which tiny packages should merge? Audits recommend keeping `projectid`, `lineage`, `ports`,
   and `optimize` because consumers or cycles make the merge worse.
6. Are zero-static-ref response DTOs a wire contract that must stay exported?
7. Is `FeaturePolicyEditor.tsx` a staged product path or dead code?
8. Should UI `selection.ts` move to `features/selection/`, or stay a root contract because
   Builder, Viewer, and React all consume it?
9. Approve or reject `lower/recipe` given parent-to-child helper calls; a file split is the
   lower-risk alternative.
10. Approve the UI contract split (`src/contracts/*`) and Zod-vs-OpenAPI source of truth.

## Accepted audit units

`lifecycle-construction`, `lifecycle-row-proposal`,
`lifecycle-interpretation-population-selection`, `lifecycle-semantic-rest`,
`authoringv2-command`, `authoringv2-construction`, `authoringv2-stored-source-rest`,
`aql-stages`, `aql-reshape-lineage`, `ir-physical-plan`, `ir-validation`,
`lower-recipe-construction`, `dataframe-published-publication`, `dataframe-semantic-support`,
`catalog-root`, `catalog-arango`, `catalog-fieldprofile`, `catalog-semantic`, `ingest-root`,
`ingest-generation`, `ingest-sourceload`, `store-adapters`, `fhir-schema-semantic`,
`api-dataframe`, `api-support`, `server`, `acceptance-dataset`, `small-packages-testkit`,
`concept-construction`, `concept-semantic`, `concept-workspace`,
`concept-population-selection`, `concept-receipt-lineage`, `cross-interfaces`,
`cross-layering`, `cross-whitebox-tests`, `cross-cmd-scripts-root`, `ui-src-root`,
`ui-construction-operations`, `ui-components`, `ui-authoring-workspace`,
`ui-builder-workspace-root`, `ui-viewer-viewbuilder`, `lower-stages-rest`, `compiler-root`,
`explorer-root`, `api-explorer`, `explorer-arango`, `explorer-receipt-compilation`,
`explorer-capability-imports`, `explorer-projectconfig-viewbuilder`, `dataframe-recipe`,
`dataframe-execution`.
