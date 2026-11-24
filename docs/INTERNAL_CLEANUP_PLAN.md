# `internal/` reorganization plan

Status: implementation in progress from `a04663fa4`. Phase 4a is staged on main. The combined verification snapshot is in `/private/tmp/loom-cleanup-candidate-a04663fa4`. Local commit approval remains pending.
Follows [REPO_CLEANUP_GOAL.md](REPO_CLEANUP_GOAL.md) and the style of
[SERVER_PACKAGE_SPLIT_PLAN.md](SERVER_PACKAGE_SPLIT_PLAN.md), which has been carried out.

## Goal

Too much code sits in too few packages, and several packages own unrelated responsibilities.
Reorganize `internal/` so that each package is named after the domain noun it owns, and its
exported API is the set of verbs on that noun. A reader should be able to tell what the system
does from the package list and each package's main types, without reading every line.

Constraints for every phase:
- Behavior is preserved.
- No API/wire contract changes.
- No database-level changes: collections, document formats, indexes and migrations stay as they are.

## Membership rules (apply to every package)

1. **One noun per package.** A file belongs to the package whose main type it defines or operates on.
2. **No dumping-ground files.** No `types.go` or `helpers.go` that declares other concepts' nouns.
   Types live next to their methods.
3. **No god objects.** A receiver with more than ~25 methods across more than ~5 files signals several
   concepts. Split it into one type per concept.
4. **A missing type shows up as long parameter lists.** Functions with 10 or more parameters get a
   context or state type that owns those values.
5. **Big switches over a sum type become per-variant methods or handlers.**
6. **Test-only production code is deleted or moved into `_test.go` before the package moves.**
7. **A package name says what it is**, and two domain packages never share a leaf name.

## Evidence (measured on `a04663fa4`)

| Package | Prod lines | Concentration signal |
| --- | --- | --- |
| `explorer/lifecycle` | 23.4k / 67 files | `Service`: **161 methods across 56 files**; 254 types; `Config` has 13 function fields |
| `explorer/authoringv2` | 15.3k / 42 | `applyCommand` 776 lines (switch over `Command`); `Command.validate` 265 lines |
| `dataframe/compiler/render/aql` | 15.1k / 42 | `physicalPlanRenderer`: **138 methods across 29 files** |
| `dataframe/compiler/ir` | 12.8k / 28 | 167 types; `physical_stage*` 3.3k and `physical_validation*` 2.8k lines |
| `dataframe/compiler/lower` | 11.9k / 41 | 296 free functions with no governing type; many functions take 11–13 parameters; `recipe_construction*` 5k lines |
| `dataframe/compiler` (root) | 7.5k / 21 | Grab bag of query features: category scan, row lineage, cell trace, paging, preview covering, population mapping |
| `explorer` (root) | 6.1k / 30 | Grab bag: receipts, project config, view builder, interpretation, viewer projection |
| `catalog` | 6.0k / 16 | `types.go` declares 6 concepts; `Profiler` 30 methods/4 files also does semantic extraction |
| `explorer/arango` / `catalog/arango` | 5.5k / 5.3k | `Store` 49 / 54 methods across every aggregate |
| `dataframe/execution` | 5.3k / 21 | `Engine` 41 methods; test-only `Control` facade |
| `ingest` | 5.2k / 30 | Three unrelated pipelines (generation load, repository source load, inventory backfill); `RepositorySourceLoader` 34 methods |

The test-only production code list (about 60 functions) is in the [appendix](#appendix-a-production-code-only-tests-reach).

## Target layout

**Firm** means the layout was traced in source, including how import cycles are avoided.
**Proposed** means the layout comes from file clusters and type metrics. Each proposed layout
gets a short, read-only design check before its phase starts.

### Firm: `catalog`

| Package | Owns | Main types |
| --- | --- | --- |
| `catalog` | Shared identity and small structural facts: resources, relationships, evidence, the read cache | `RelationshipKey`, `ResourceInventoryBuild`, `CapabilityEvidence`, `Cache` |
| `catalog/codedconcept` | Coded-concept observations and their extraction from a FHIR payload | `SemanticObservation`, `Extractor` (new) |
| `catalog/semanticinventory` | Inventory build, aggregation, entries, families, paging cursor | `Build`, `Entry`, `Family`, `Emitter`, `Aggregator` |
| `catalog/fieldprofile` | Per-generation field statistics for one observed row | `Profiler`, `ShapePlanCache`, `ProfileLimits`, `PopulatedField` |

How the import cycle is avoided:
- Today `SemanticInventoryEmitter.ObservePayload` (semantic_inventory.go:480) builds a
  `Profiler{semanticOnly: true}` just to reuse extraction.
- Extraction (semantic.go, semantic_schema.go) reads and writes Profiler `stats`, `limits`,
  `reserve`, `truncated` and identity.
- Move extraction into `codedconcept.Extractor`, which reports through a small recorder interface.
  That interface has two real implementations: the field profiler, and the emitter's semantic-only mode.
- The emitter then no longer depends on `Profiler`, so there is no cycle.

`catalog/arango`: split `Store` into per-concept stores in the same package (`FieldStore`,
`RelationshipStore`, `ResourceStore`, `SemanticInventoryStore`, `EvidenceStore`) over the same
collections. Callers take the narrow store they use.

### Firm: `ingest`

| Package | Owns | Main types |
| --- | --- | --- |
| `ingest` | Shared row decoding and documents | `RowBuilder`, `VertexDocument`, `EdgeDocument`, `RowError` |
| `ingest/generation` | Load NDJSON files into a dataset generation | `LoadOptions`, `LoadSummary`, `PreflightReport`, a `fileLoader` that replaces `loadFile`'s 11 parameters |
| `ingest/sourceload` | Upload, stage, copy and publish repository sources | `RepositorySourceLoader`, split by phase into uploader/stager/publisher |
| `ingest/backfill` | Rebuild the semantic inventory for an existing generation | `SemanticInventoryBackfillOptions`, run heaps |

The external surface is small: `api/bulk/load`, `server` and `cmd/arango-fhir-proto`.

### Firm: `dataframe/recipe` stays one package, organized by noun

- `Bundle`, `Output` and `Construction` reference one another, and `document_types.go` and
  `document_validation.go` reference the fragment and archive types. A package split would
  create cycles, so organize the files instead:
  - split construction.go (1.7k) by construction kind
  - move the types in document_types.go (616) into files named after them
  - fold validation_rich.go into the types it validates
- Rename `recipe/exec` → `recipe/registry`. It is the version registry, and "exec" misleads.

### Firm: keep lifecycle orchestration together and extract its adapter contract

The six-way service split needs a revised boundary. Use these groups as receiver and file ownership inside `lifecycle` first. The following counts use the declaring file and count direct receiver call sites between groups. Calls through callbacks, shared fields, and free functions are outside the count.

| Group | File stems (globs) | Methods | Calls to other groups |
| --- | --- | ---: | --- |
| proposal | `construction_*`, `row_proposal`, `row_comparison`, `proposal_receipt`, `column_policy_proposal`, `row_change` | 54 | authoring 25 (row_change.go:12), interpretation 3 (column_policy_proposal.go:159), sourcestep 13 (construction_proposal.go:1820) |
| sourcestep | `expand_source`, `frame_source`, `unpivot_source`, `derive_source`, `filter_source`, `source_relationship_proposal` | 23 | authoring 9 (source_relationship_proposal.go:213), interpretation 1 (source_relationship_proposal.go:213), proposal 3 (source_relationship_proposal.go:213) |
| fieldcatalog | `semantic_catalog`, `schema_fields`, `column_source` | 7 | authoring 1 (semantic_catalog.go:1191), proposal 4 (semantic_catalog.go:261), sourcestep 1 (semantic_catalog.go:261) |
| selection | `selection`, `population*`, `explicit_groups`, `row_choices` | 23 | authoring 4 (population_member_proposal.go:328), interpretation 3 (population_member_proposal.go:328), proposal 4 (row_choices.go:183) |
| interpretation | `interpretation*`, `policy` | 14 | authoring 6 (interpretation_preview.go:18) |
| authoring | remaining files | 40 | interpretation 17 (publish.go:18), proposal 5 (authoring.go:35), selection 4 (authoring.go:446), sourcestep 2 (authoring.go:35) |

The groups account for all 161 methods and 105 cross-group call sites. Both directions are real dependencies. Authoring applies frame-source commands and resolves interpretations, while those groups use shared authoring compilation and catalog methods (`authoring.go:215,255,468`, `frame_source.go:114`, `interpretation_preview.go:55`). Keep `Service` as the orchestrator while extracting cohesive receiver types. Revisit child packages only once workspace resolution, receipt compilation, and command application have explicit ownership.

Extract adapter-facing contracts to `explorer/lifecycle/ports`. Move `AuthorizedCapability`, `CapabilityResolver`, lifecycle error types/classes, compile/stage requests, and execution/materializer contracts. Keep error constructors, transport requests, and `Service` construction in lifecycle. Receipt still calls `Construction{Derive,Filter,Unpivot}SourceFacts`. These are behavior with shared private helpers, so keep that existing edge until the source-step receiver work (`lifecycle/types.go:21–113`, `errors.go:10–49`, `derive_source.go:423`, `filter_source.go:371`, `unpivot_source.go:477`, `receipt/runtime.go:28–40`, `server/wire_explorer.go:166`).

The cut orders existing authscope, Explorer model, capability, compilation and dataframe types below ports, then lifecycle and adapters, then API/server. Checked the proposed port dependencies against `go list -f '{{.ImportPath}}: {{join .Imports " "}}' ./internal/...`. No dependency reaches lifecycle or any proposed port importer. Keep ports free of service, adapter, and transport imports. Verify the realized graph with `go list -deps` when implemented.

Production importer symbols below are qualified by `lifecycle`. Evidence paths are relative to each importer.

| Importer | Symbols | Source |
| --- | --- | --- |
| `explorer/capability/resolver` | `AuthorizedCapability` | explorer_capability.go:162 |
| `explorer/constructioninputs` | `CapabilityResolver` | catalog.go:97 |
| `explorer/importedsource` | `AuthorizedCapability`, `ClassUnavailable`, `ConstructionSourceStageRequest`, `Error` | explorer_authoring_network.go:19, explorer_authoring_network.go:296, explorer_imported_recipes.go:288 |
| `explorer/receipt` | `AuthorizedCapability`, `ClassUnavailable`, `CompileReceiptRequest`, `ConstructionDeriveSourceFacts`, `ConstructionFilterSourceFacts`, `ConstructionSourceStageCompiler`, `ConstructionSourceStageRequest`, `ConstructionUnpivotSourceFacts`, `Error`, `Execution`, `ExecutionOutput`, `ReceiptMaterializer` | explorer_output_reconcile.go:929, explorer_output_reconcile.go:1087, explorer_output_reconcile.go:1128, explorer_publication.go:19, explorer_publication.go:20, explorer_publication.go:25, explorer_publication.go:59, explorer_receipt_contract.go:32, explorer_receipt_contract.go:110, explorer_receipt_contract.go:113 |
| `api/explorer` | `ArtifactOpenRequest`, `ArtifactRequest`, `AssessRowChangeRequest`, `AuthoringNetworkRequest`, `BrowseFrameSourceOptionsRequest`, `BrowseSemanticInventoryRequest`, `BuilderRequest`, `CellTraceRequest`, `ClassConflict`, `ClassForbidden`, `ClassMalformed`, `ClassNotFound`, `ClassUnavailable`, `ClassUnprocessable`, `ColumnPolicyProposalRequest`, `ColumnSourceRequest`, `Config`, `ConfiguredColumnContextRequest`, `ConfiguredColumnContextResult`, `ConstructionCapabilitiesRequest`, `ConstructionCategoryDiscoveryRequest`, `ConstructionChoiceProposalRequest`, `ConstructionChoiceProposalSelection`, `ConstructionChoiceSearchRequest`, `ConstructionChoiceSearchSource`, `ConstructionColumnPopulationRequest`, `ConstructionDeriveSourceSelection`, `ConstructionExpandSourceSelection`, `ConstructionFilterSourceSelection`, `ConstructionGroupSourceSelection`, `ConstructionPivotSourceSelection`, `ConstructionProposalRequest`, `ConstructionUnpivotSourceSelection`, `CreateInterpretationRevisionFromColumnRequest`, `CreateInterpretationRevisionRequest`, `CreateRequest`, `Error`, `ErrorClass`, `ExplicitGroupCreateRequest`, `ExplicitGroupInput`, `FieldPresentationRequest`, `GetInterpretationRevisionRequest`, `InterpretationRevisionSummary`, `New`, `PopulationMappingRequest`, `PopulationMemberRemovalProposalRequest`, `PopulationRoutesRequest`, `PreviewInterpretationCandidateRequest`, `PreviewRequest`, `PublishRequest`, `PublishedOutputIntent`, `ReconcileRequest`, `RelatedExpandChoiceSearchRequest`, `RelatedExpandContributorChoiceSearchRequest`, `RelatedFieldChoiceSearchRequest`, `RepositoryPublishRequest`, `RowDefinitionChoicesRequest`, `RowDefinitionProposalRequest`, `RowDefinitionSelection`, `RowLineageRequest`, `SearchSchemaFieldsRequest`, `SelectionIntentCreateRequest`, `SelectionReadIntentRequest`, `SelectionSourceIntent`, `Service`, `SourceRelationshipProposalRequest`, `SuggestionsRequest` | explorer_config_route.go:18, explorer_config_route.go:18, explorer_config_route.go:27, explorer_config_route.go:33, explorer_preview_response.go:67, openapi_explorer_artifacts.go:26, openapi_explorer_artifacts.go:54, openapi_explorer_authoring_direct.go:85, openapi_explorer_authoring_direct.go:135, openapi_explorer_authoring_direct.go:192, openapi_explorer_authoring_direct.go:249, openapi_explorer_authoring_direct.go:330, openapi_explorer_authoring_direct.go:332, openapi_explorer_authoring_direct.go:334, openapi_explorer_authoring_direct.go:336, openapi_explorer_authoring_direct.go:338, openapi_explorer_authoring_direct.go:340, openapi_explorer_authoring_direct.go:342, openapi_explorer_authoring_direct.go:432, openapi_explorer_authoring_direct.go:478, openapi_explorer_authoring_direct.go:541, openapi_explorer_authoring_direct.go:571, openapi_explorer_authoring_direct.go:610, openapi_explorer_authoring_direct.go:629, openapi_explorer_authoring_direct.go:693, openapi_explorer_authoring_direct.go:736, openapi_explorer_authoring_direct.go:758, openapi_explorer_authoring_direct.go:787, openapi_explorer_authoring_direct.go:817, openapi_explorer_authoring_direct.go:854, openapi_explorer_authoring_direct.go:926, openapi_explorer_authoring_network.go:20, openapi_explorer_column_policy.go:20, openapi_explorer_construction.go:26, openapi_explorer_construction.go:30, openapi_explorer_construction.go:64, openapi_explorer_construction.go:100, openapi_explorer_construction.go:136, openapi_explorer_construction.go:172, openapi_explorer_construction.go:204, openapi_explorer_construction_choice_proposal.go:24, openapi_explorer_construction_choice_proposal.go:39, openapi_explorer_construction_proposal.go:37, openapi_explorer_construction_proposal.go:64, openapi_explorer_construction_proposal.go:95, openapi_explorer_construction_proposal.go:129, openapi_explorer_construction_proposal.go:136, openapi_explorer_construction_proposal.go:144, openapi_explorer_construction_proposal.go:152, openapi_explorer_construction_proposal.go:160, openapi_explorer_construction_proposal.go:168, openapi_explorer_construction_proposal.go:176, openapi_explorer_direct.go:128, openapi_explorer_direct.go:218, openapi_explorer_explicit_groups.go:24, openapi_explorer_explicit_groups.go:28, openapi_explorer_population_member_proposal.go:30, openapi_explorer_row_definition.go:18, openapi_explorer_row_definition.go:51, openapi_explorer_row_definition.go:57, openapi_explorer_selections.go:32, openapi_explorer_selections.go:35, openapi_explorer_selections.go:44, openapi_explorer_selections.go:71, openapi_explorer_source_relationship.go:21, openapi_frame_source_options.go:20, openapi_semantic_inventory.go:21, openapi_semantic_inventory.go:29 |
| `api/http` | `Error` | server_diagnostic.go:42 |
| `server` | `ArtifactPublishedReader`, `CapabilityResolver`, `Config`, `DelayArtifactRows`, `NewHMACPopulationMappingCursorCodec`, `NewRepositoryExplicitGroupRevisionResolver`, `NewSchemaRowChoiceResolver`, `SelectionSourceResolver` | wire_explorer.go:58, wire_explorer.go:85, wire_explorer.go:89, wire_explorer.go:93, wire_explorer.go:100, wire_explorer.go:104, wire_explorer.go:108, wire_explorer.go:137 |

API Explorer also calls these `Service` methods through `Handlers.application` (`api/explorer/explorer_config_route.go:18` and the route files above): `ApplyCommands`, `ApplyCommandsWithConstructionPreview`, `AssessRowChange`, `BrowseFrameSourceOptions`, `BrowseSemanticInventory`, `Builder`, `CellTrace`, `ColumnSource`, `ConfiguredColumnContext`, `Create`, `CreateExplicitGroupRevision`, `CreateInterpretationRevision`, `CreateInterpretationRevisionFromColumn`, `CreateSelection`, `DescribeAuthoringNetwork`, `DiscoverConstructionCategories`, `Get`, `GetConstructionCapabilities`, `GetConstructionColumnPopulation`, `GetInterpretationRevision`, `List`, `ListInterpretationLibraries`, `ListRowDefinitionChoices`, `OpenArtifact`, `PopulationMapping`, `PrepareArtifact`, `Preview`, `PreviewInterpretationCandidate`, `ProposeColumnPolicy`, `ProposeConstruction`, `ProposeConstructionChoice`, `ProposePopulationMemberRemoval`, `ProposeRowDefinition`, `ProposeSourceRelationship`, `Publish`, `PublishRepository`, `ReadSelection`, `ReconcileWithWorkspace`, `RowLineage`, `SearchConstructionChoices`, `SearchPopulationRoutes`, `SearchRelatedExpandChoices`, `SearchRelatedExpandContributorChoices`, `SearchRelatedFieldChoices`, `SearchSchemaFields`, `Suggestions`.

### Firm: command handlers and Explorer root ownership

- **Command handlers.** Keep the existing `Command` wire struct, validation order, and indexed diagnostics. Dispatch within the model package to handlers for table/root/population, route, column/contributor, imported source, frame source, and proposal/restore variants. The existing column-policy and frame-source dispatches show the seam. Handlers use package-private workspace operations, so this step adds no imports (`authoringv2/commands.go:114,591,880–910,1423–1493`).
- **Stored workspace.** Defer the blanket `stored_*` split. `LegacyNilConstructionStepsDigest` is a method on the model's `Workspace`, and the migration calls internal clone and canonicalization code. A package cut must leave model methods with their type and move only the historical decode, verification, and conversion entrypoints whose dependencies can point back at the model. Arango and acceptance can import that adapter directly. The exact private helper surface is unverified (`authoringv2/stored_workspace_legacy_codec.go:12`, `stored_workspace_legacy_migration.go:78,199`, `stored_workspace_migration.go:22–52`, `explorer/arango/workspace_migration_target.go:36–55`, `acceptance/scenario.go:385,439`).
- **Project config and view builder.** Keep shared config shapes with the root model for the first cut because `Explorer.ProjectConfig` embeds the type. Extract project config preparation and publication into `explorer/projectconfig`, importing root. Extract view-builder orchestration into `explorer/viewbuilder`, importing root and projectconfig, after injecting its repository and moving root's default-selection orchestration to the composition boundary. Keep viewer projection with root because `LoadExplorerState` calls it and its inputs are root revision/config types. This gives the import order root → projectconfig → viewbuilder → API/Arango/ingest (arrows point toward importers). The original full-file split would create a back-edge through `Explorer.ProjectConfig` and `LoadExplorerState`. Verify the final declaration cut before moves (`explorer/types.go:67`, `service.go:43,85`, `view_builder_service.go:12–20,54`, `view_builder_prepare.go:21–25`, `viewer_projection.go:48–52`, `api/explorer/openapi_view_builder.go:1`, `explorer/arango/view_builder.go:157–173`, `ingest/source_publication_adapter.go:95–135`).
- Move `InitialProjectConfigForRecipe` and binding diagnostics with projectconfig so `ProjectConfigsForSourcePublication` needs no viewbuilder import. Viewbuilder in turn uses project's validation and presentation functions. Keep root model validation and cloning with the model and expose only the operations the new services need. Give root's default-view lookup a narrow injected port so root needs neither new package (`project_configs.go:54`, `view_builder_prepare.go:47,68,175`, `project_config_prepare.go:39,486,547`, `project_config.go:128,318`, `service.go:38–54`). Exact private helper counts after declaration moves remain unverified.
- **Explorer Arango (Proposed).** Split `Store` receivers by aggregate inside the existing package. Keep shared transaction helpers and storage contracts there (`explorer/arango/view_builder.go:157–173`).

### Firm: compiler layering and construction state

Keep feature ownership visible within each layer. The compiler selects the output and plans the query, AQL renders it, and execution streams and parses results. Keep feature-specific files aligned across layers. A vertical package would need the renderer's shared stage dispatch and the engine's query/access state. Keep compiler features in the root for this pass.

| Feature | Compiler handoff | Execution boundary |
| --- | --- | --- |
| Row lineage | `compiler/row_lineage.go:671,852` uses dedicated and common AQL rendering | `execution/row_lineage.go:48–57,121–124` |
| Cell trace | `compiler/cell_trace.go:105,150,186` uses the common renderer for several plans | `execution/cell_trace.go:88–115,152–155` |
| Category scan | `compiler/category_scan.go:266,652` renders the scan and its overflow witness | `execution/category_scan.go:76–87,170–226` selects query path and handles the witness |
| Population mapping | `compiler/population_mapping.go:25,45` renders a population terminal | `execution/population_mapping.go:85–139,202–207` reads memberships and streams rows |
| Table shape | `compiler/table_shape_exclusions.go:49,132` renders exclusions through the common entrypoint | `execution/table_shape_exclusions.go:50–85` compiles and streams exclusions |

AQL handles several feature terminals in the same physical validation dispatch (`compiler/render/aql/validation.go:188–200`). Use the feature boundaries for review and test selection.

**Lowering.** All six 11–13-parameter functions take `plan *ir.PhysicalPlan`, `policy ir.PhysicalOptimizationPolicy`, `usedVariables map[string]bool`, and an `index` or `stepIndex`. Put the first three on `constructionLowerer`. Root resource type is shared by five functions and is fixed at the construction loop. Keep the shared index, input identity/schema, input row and stage IDs, input/output maps, provenance flags and variant payloads scoped to the step or handler. The related source, eligibility and expand signatures share step, input/output maps and input row. Group and related coding key also receive a stage pointer. Sources under `compiler/lower`: `recipe_construction.go:428,1196,1649`, `recipe_construction_related_eligibility.go:20`, `recipe_construction_related_expand.go:17`, `recipe_construction_reshape.go:13`, `group_related_occurrence_lower.go:145`.

### Proposed: IR, AQL and execution receiver ownership

- **`ir`:** stays one package because the generated `children.gen.go` needs it.
  - Organize the files by node family: stage, table, combine, scope.
  - Put each node's validation next to the node instead of in `physical_validation*`.
- **`render/aql`:** replace the 138-method `physicalPlanRenderer` with per-family renderers
  (stage sequence, reshape, navigation/related, lineage/trace, category) that share a
  `renderContext`. Keep them in one package.
- **`execution`:** delete `Control`. Split `Engine` (41 methods) into the preview, run and
  explain paths, organized like the compiler's feature groups.

### Remaining packages

Counts are physical lines and direct production Go files at `a04663fa4`, excluding tests. Receiver counts are methods/files. All eight declarations at or above 150 lines or 10 named parameters are cited below. The catch-all candidates are publication and dataframe DTO `types.go`, query `helpers.go`, and published `types.go` (`dataframe/publication/types.go:10–135`, `api/dataframe/types.go:8–661`, `api/dataframe/query/helpers.go:8–43`, `dataframe/published/types.go:10–90`). Arango's `types.go` mixes row visiting and bootstrap types (`store/arango/types.go:3–19`). Query `types.go` describes one introspection response (`api/dataframe/query/types.go:5–30`). Table source paths are relative to each package. Production importers and qualified symbols are in [Appendix B](#appendix-b-production-importers).

| Package | Lines/files | Largest receivers | Verdict and source |
| --- | ---: | --- | --- |
| `dataframe/publication` | 1,337/7 | qualityAccumulator 4/1 | **REORGANIZE IN PLACE.** Separate stream, schema, and transaction declarations. (`runner.go:17`) |
| `dataframe/publication/arango` | 522/2 | Registry 20/1 | **FINE.** One registry owns bundle records, pointers and leases. (`bundle_registry.go:26`) |
| `dataframe/publication/clickhouse` | 1,196/1 | clickHouseBundleTx 23/1, ClickHouseBundleStore 11/1 | **REORGANIZE IN PLACE.** Group transaction methods by the state they operate on. (`bundle_store.go:54,347`) |
| `dataframe/published` | 3,563/15 | Reader 16/6 | **SPLIT.** Extract format encoding from materialization reads. (`read.go:30`) |
| `store/arango` | 2,458/14 | Client 29/8 | **REORGANIZE IN PLACE.** Separate preview index policy from the general client receiver. (`client.go:23`) |
| `store/clickhouse` | 656/1 | Client 11/1 | **FINE.** One driver adapter owns connection and database operations. (`client.go:26,60`) |
| `dataset` | 715/9 | ReleaseService 5/1 | **FINE.** Manifest, selector and release describe generation identity and activation. (`release.go:92,102`) |
| `dataset/arango` | 730/5 | Store 10/3 | **FINE.** Manifest and release adapters share lifecycle storage and a common CAS path. (`manifest_lifecycle.go:29,38 and release_store.go:13`) |
| `api/explorer` | 3,364/24 | Handlers 66/19 | **REORGANIZE IN PLACE.** Move command dispatch and view adoption orchestration behind domain calls. (`explorer_config_route.go:14`) |
| `api/dataframe` | 722/2 | DataframeMaterializationState, FhirAggregateOperation 3/1 each | **REORGANIZE IN PLACE.** Group wire statements by request and result family. (`types.go:8,142,595`) |
| `api/dataframe/dataframe` | 1,322/7 | Service 16/3, Handlers 7/1 | **REORGANIZE IN PLACE.** Remove the path stutter and put run defaults with the query operation. (`service.go:17 and openapi_handlers.go:71`) |
| `api/dataframe/query` | 1,592/12 | Service 16/6 | **REORGANIZE IN PLACE.** Give scoped input resolution its own state. (`input_resolution.go:67`) |
| `api/bulk/load` | 1,370/8 | Handler 8/2, StrictHandlers 7/2 | **REORGANIZE IN PLACE.** Give source-load authorization and action ordering to one application operation. (`source_load.go:64–132`) |
| `api/http` | 782/9 | HTTPServer 11/3 | **FINE.** Health probes, middleware and error responses share the HTTP boundary. (`openapi_health.go:10–54`) |
| `api/recipe` | 127/2 | FieldDiscovery 1/1 | **REORGANIZE IN PLACE.** Merge into recipe runtime as its catalog adapter. (`discovery.go:13 and recipe_discovery.go:11`) |
| `api/authpolicy` | 91/1 | — | **FINE.** Shared authorization error classification serves transport and runtime. (`policy.go:12–47`) |
| `api/columncapabilities` | 62/1 | — | **REORGANIZE IN PLACE.** Merge with the physical column contract in publication. (`capabilities.go:5–35`) |
| `fhir/schema` | 2,367/13 | Index 8/3, ElementView 8/1 | **FINE.** Index and element views govern schema lookup and selectors. (`index.go:25–73,95–106`) |
| `fhir/semantic` | 1,024/2 | stringMaxHeap 5/1, memberValue 4/1 | **REORGANIZE IN PLACE.** Separate traversal-run state from the recursion frame. (`member_walk.go:155,267,441,549`) |
| `authscope` | 1,042/6 | ScopeResolver 10/1, FenceUserAccessClient 6/1 | **REORGANIZE IN PLACE.** Separate permission and path policy from Fence access transport. (`scope_resolver.go:40,141 and fence_resource_access.go:21`) |


**Publication and release reads.** In `publication`, group `LogicalColumn`, `OutputSchema` and `FinalSchemaDigest` by schema, `OutputStream` and `Limits` by streaming, and `Target` and `Transaction` by transaction. Separate output preparation from the publish loop (195 lines, `publication/types.go:10–135`, `runner.go:17`). In the ClickHouse adapter, move lease and reconciliation methods, output table methods, and commit/abort methods into distinct files under the existing store/transaction types (`bundle_store.go:270,392,494,782,879,1069`).

Extract `dataframe/artifactformat` to own `ArtifactFormat`, `ArtifactDescriptor`, `ArtifactRequest` and `WriteArtifact` (151 lines). Move `published/artifact.go` and its encoding tests. Pass the existing authorization-column name explicitly from the reader-side caller or share it with the existing storage-column contract. Reader-side pagination, exact export, access checks and pin resolution stay with `Reader` (`published/artifact.go:29,162,351`, `published/types.go:12`, `published/export.go:64`, `published/page.go:170`). The current file imports only stdlib and `dataframe/unit`. With the column name supplied at the boundary, the new package needs only those imports. `go list` and the callers show the cut runs from unit to artifactformat to Explorer model/lifecycle/artifactfs, with no return import (`explorer/artifact.go:38`, `lifecycle/artifact.go:144`, `artifactfs/store.go:377`). Implementation build remains unverified. Writer and release-reader names remain an owner decision.

**Storage.** Give preview covering index validation, use and audit a dedicated type in `store/arango`. Keep low-level collection access and driver APIs on `Client`, and keep bootstrap declarations together. Preserve the existing index definitions and audit/migration behavior (`store/arango/preview_index.go:24–100,192–206`, `preview_supersedes_audit.go:20–57`, `types.go:3–19`).

**HTTP boundary.** Strict methods decode, authorize, call one application operation and encode. Keep multipart staging, status encoding and HTTP error mapping at the edge. The source scan covers receiver calls in 52 exported Explorer strict methods, four dataframe methods, seven bulk-load methods and three health methods. Health dispatches to `Health`, `Liveness`, and `Readiness` respectively (`api/http/openapi_health.go:10–54`). The call scan includes inline functions. A complete transitive check through every helper remains unverified.

- **Explorer.** `ApplyExplorerBuilderCommands` selects different domain entrypoints by command kind. Put that dispatch in lifecycle and keep the transport's bounded response sink. View adoption reads owners/revisions, adopts, and reloads. Move its sequencing and execution-authorization checks to the view-builder application operation, retaining wire error mapping (`api/explorer/openapi_explorer_authoring_direct.go:349–403`, `openapi_view_builder.go:45–74,154–194`). Other examples of one-call handlers include construction search (`openapi_explorer_construction.go:13–52`).
- **Explorer previews and view writes.** Put proposal-plus-preview sequencing behind one application operation per proposal family and use a bounded transport sink. Save/default/delete each read state, mutate, and reread. Move that sequence with view adoption. The static authoring capability response can remain a declared transport contract (`api/explorer/openapi_explorer_construction_proposal.go:112`, `openapi_explorer_construction_choice_proposal.go:15`, `openapi_explorer_population_member_proposal.go:13`, `openapi_view_builder.go:200,233,266`, `openapi_explorer_authoring_direct.go:589`).
- **Dataframe.** Split the DTO file into published rows/aggregations, FHIR query input and diagnostics. Put the default preview limit with the query operation and keep the one-call rows and aggregation handlers. In query, group the private predicate and traversal helpers by their input type and use a node input plus a scoped resolver to reduce the 10 parameters of `resolveNodeInputRefs` (`api/dataframe/types.go:8,142,595,621`, `dataframe/openapi_handlers.go:71–145`, `query/input_resolution.go:15,52,67`, `query/types.go:5–30`, `query/helpers.go:8–43`).
- **Bulk load.** Keep multipart staging and its limits at the edge. Split the 162-line staging function by multipart read and file validation. Publish and upload currently load status, authorize its scope, then invoke the source loader. Give that ordering to one authorized source-load operation per action. Keep generation request conversion and error mapping in the handler (`api/bulk/load/generation_staging.go:120`, `source_load.go:64–132`, `operations.go:34–93`, `openapi_generations.go:11–52`).
- **Small adapters.** `api/recipe` contains catalog-backed recipe field discovery, with query and server as production callers. Merge `FieldDiscovery` and `RecipeSchemaResolver` into `dataframe/recipe/runtime`, keeping its discovery error contract (`api/recipe/discovery.go:13–64`, `recipe_discovery.go:11`). Move `api/columncapabilities`'s `Tuple` and `Resolve` next to `publication.PhysicalColumn` as column capability definitions. Explorer construction inputs already depend on publication, so this removes an API import from a domain adapter (`api/columncapabilities/capabilities.go:5–35`, `explorer/constructioninputs/catalog.go:595`, `dataframe/publication/bundle.go:146`). The current `go list` graph has neither a path from recipe runtime to query/server nor from publication to the routes or construction-input adapters. Adding their dependencies to the owners gives no return path. Confirm the rewritten graph after the moves.

**FHIR traversal.** Use existing `memberWalkState` for schema index, root, options, visitor, diagnostic sink and budgets. Carry declaring type, canonical/owner prefixes, repeated/resource boundaries and depth in a per-recursion frame. Keep value/object and schema element inputs on each operation. The 14-, 13-, and 15-parameter functions share these arguments across both decoded and raw member walks. Move `RepeatedBoundary` and shared path/boundary helpers out of the legacy walker before relocating the test-only entrypoint (the legacy object walker takes 10 parameters). Preserve visit order, boundary copying and limit accounting (`fhir/semantic/member_walk.go:67,144–159,267–282,441–455,549–565,606–625`, `walk.go:22,105,170–185`).

**Authorization scope.** Keep the published authscope types and resolver together. Move pure auth-resource path rules out of the Fence client file, group the bearer adapter with the other authenticators, and isolate Fence cache bookkeeping from HTTP fetch and decoding (`authscope/fence_resource_access.go:112–132,202–272,284–318`, `scope_resolver.go:109,141–145`, `authentication.go:17,49`). Preserve normalization and cache semantics.

**Sanity pass.** Package size alone does not decide ownership. The codec extraction and two adapter merges above follow import direction and existing callers.

| Package | Verdict |
| --- | --- |
| `dataframe/unit` | **FINE.** Unit identity and arithmetic share one vocabulary (`unit/unit.go:16`, `unit/arithmetic.go:5`). |
| `dataframe/errors` | **FINE.** Shared service error codes support multiple owners (`errors/errors.go:10`). |
| `dataframe/lineage` | **FINE.** Source-child identity is a neutral contract for recipe, lowering and Explorer (`lineage/source_child.go:15`, current import graph). |
| `dataframe/columntransform` | **FINE.** Shared transform vocabulary keeps recipe, lowering and authoring independent (`columntransform/types.go:5`, current import graph). |
| `dataframe/expression` | **FINE.** Expression types, checking and recipe adaptation share one domain (`expression/expression_types.go:9`, `recipe_adapter.go:12`). |
| `dataframe/spec` | **FINE.** Selectors and filter semantics govern the input query model (`spec/selectors.go:10`, `filter_semantics.go:10`). |
| `dataframe/semantic` | **FINE.** Graph, nodes and execution context describe the same logical plan (`semantic/semantic_nodes.go:10`, `execution_context.go:5`, `semantic_validation.go:10`). |
| `projectid` | **FINE.** Canonical project identity is a shared dependency (`projectid/projectid.go:8`). |
| `ci` | **FINE.** Test package owns repository gates only (`ci/gofmt_gate_test.go:1`, `vet_gate_test.go:1`). |
| `repositoryseed` | **FINE.** Preparation owns the repository manifest contract (`repositoryseed/preflight.go:21`). External caller inventory is outside this pass. |
| `explorer/artifactfs` | **FINE.** File storage and its cleanup serve the same artifact lifecycle (`artifactfs/store.go:23`, `private_artifact_reconciler.go:24`). |
| `dataframe/execution/chartifact` | **FINE.** Execution artifact state and identity share a storage contract (`chartifact/artifact.go:25`). |
| `dataframe/execution/chartifact/arango` | **FINE.** One adapter implements the catalog contract (`chartifact/arango/catalog.go:22`). |
| `dataframe/recipe/runtime` | **FINE.** Execution authorization, combine resolution and publication are runtime adapters (`runtime/recipe_authorization.go:15`, `recipe_combine_resolution.go:15`, `recipe_publication.go:19`). |
| `dataframe/compiler/capability` and `optimize` | **FINE.** Compiler proof requests and physical optimization have separate entrypoints (`capability/probe.go:20–70`, `optimize/optimize.go:12`). |
| `dataframe/compiler/render/clickhouse` | **FINE.** Workspace inputs and rendered combine queries share one backend contract (`workspace_combine.go:10`, `combine.go:15`). |
| `dataframe/execution/arango` | **FINE.** Query execution and driver error mapping form one backend adapter (`query_adapter.go:12`, `query.go:17`). |
| `dataframe/recipe/exec`, `exec/arango` and `schema` | **FINE.** Version registration, its Arango adapter and field resolution have distinct contracts (`exec/persistent.go:11–35`, `exec/arango/revisions.go:22`, `schema/resolve.go:87`). |
| `explorer/capability` and `capability/resolver` | **FINE.** Snapshot repository and authorization resolver have separate ownership (`capability/repository.go:17`, `resolver/scope.go:10`). |
| `explorer/compilation`, `constructioninputs`, `importedsource`, `receipt` and `recipeimport` | **FINE.** Keep the existing workspace compilation, input browsing, source runtime, receipt execution and import translation boundaries. Change their lifecycle-port imports in Phase 4a where applicable (`compilation/compile.go:120`, `constructioninputs/catalog.go:34`, `importedsource/explorer_imported_recipes.go:27`, `receipt/runtime.go:28–40`, `recipeimport/source_plan_edit.go:11`). |
| `server` | **FINE.** Startup and composition are covered by the completed [server split](SERVER_PACKAGE_SPLIT_PLAN.md) (`server/run.go:21`, `wire_explorer.go:166`). |
| `testkit/*` | **FINE for this pass.** Keep reusable fixtures and environment setup grouped by subject. Detailed test-suite review is outside scope (`testkit/fixtures.go:10`, `arangotest/arangotest.go:20,37`). |
| `acceptance` | **FINE for this pass.** Fixture locking and scenario execution serve one acceptance workflow (`acceptance/fixture.go:50`, `scenario.go:30,41`). |

### Renames (mechanical, done early because they make everything else easier to read)

| Current | Proposed | Why |
| --- | --- | --- |
| `dataframe/semantic` | `dataframe/logicalplan` | Third different meaning of "semantic" (after `fhir/semantic` and catalog semantic) |
| `explorer/authoringv2` | `explorer/authoring/document` | No v1 exists |
| `explorer/lifecycle` | split, see above | Extract ports first. The receiver call graph defers the original six-package move |
| `dataframe/compiler/capability` | `dataframe/compiler/support` (or similar) | Collides with `explorer/capability` |
| `dataframe/recipe/schema` | `dataframe/recipe/fielddiscovery` | Collides with `fhir/schema` |
| `dataframe/recipe/exec` | `dataframe/recipe/registry` | It is a registry, not an executor |
| `dataframe/spec` | `dataframe/querymodel` | It holds the filter, grain and selector vocabulary |
| `dataframe/publication` / `published` | Owner decision: rename or document | Writer vs reader is invisible in the names. Candidates are `dataframe/publish` and `dataframe/materialized` (`publication/runner.go:17`, `published/read.go:30`) |
| `api/dataframe/dataframe` | `api/dataframe/routes` | Drop the path stutter. Transport package names may repeat under `api/` |

## Phases

Each named unit in a row gets its own PR. Target under ~3,000 moved lines of logic per PR, excluding pure renames. Budgets refer to the source families above and remain estimates until the staged diff can be counted. Keep each PR buildable. Commit cadence remains an owner decision.

Commands use Bash brace expansion for exact package paths. In a batch, include each named destination as soon as it exists. Use the corresponding current path if a rename is declined. The verification section still applies to every PR.

| Phase | PR units and moved-line budget | Focused tests | Test relocation |
| --- | --- | --- | --- |
| 0 | Restore Go 1.27 reachability tooling and resolve Appendix A one package at a time. | `go test ./internal/{catalog,ingest,explorer,explorer/arango,explorer/lifecycle,dataframe/compiler,dataframe/compiler/lower,dataframe/compiler/render/aql,dataframe/execution,dataframe/published,api/http,api/dataframe/dataframe,fhir/semantic,store/arango,acceptance}` | Keep test-only helpers with their assertions. Retain production walker helpers. |
| 1 | Owner-approved renames. Mechanical. | `go test ./internal/...` | Move package suites with their subjects. Zero new boundaries. |
| 2 | Preserving the semantic coverage reader, extract the recorder, inventory, and field profile in separate PRs. Split catalog Arango receivers by store family in subsequent PRs. Budget 1–2.8k each (`catalog/semantic_inventory.go:480–531`, `catalog/arango/store.go`). | `go test ./internal/{catalog,catalog/codedconcept,catalog/semanticinventory,catalog/fieldprofile,catalog/arango,ingest,explorer/lifecycle,explorer/capability/resolver,server}` | 3/19 cross-group candidates (`catalog/semantic_inventory_test.go:34,246`, `write_profiler_test.go:24–26`). Keep adapter tests together because adapter receivers share their package. |
| 3 | After 2, extract generation, source load, then backfill. Budget 0.9–2.8k each. If source load exceeds budget, move staging before publication (`ingest/source_load_copy.go:1`, `semantic_inventory_backfill.go:1`). | `go test ./internal/{ingest,ingest/generation,ingest/sourceload,ingest/backfill,api/bulk/load,catalog/arango,server}` | 11/37 cross-group candidates. Row parity spans the destinations (`ingest/edge_row_parity_test.go:17,57,88`). Blocks a mechanical test move. |
| 4a | Lifecycle ports and adapter callers, <1k (`lifecycle/types.go:21–113`, `errors.go:10–47`). | `go test ./internal/{explorer/lifecycle,explorer/lifecycle/ports,explorer/capability/resolver,explorer/constructioninputs,explorer/importedsource,explorer/receipt,api/explorer,api/http,testkit/constructionfixtures,testkit/explorerfixtures,server}` | Zero known cross-private references for exported ports. Keep private error constructors and their tests in lifecycle. |
| 4b | One in-package receiver PR each for field browse, selection, interpretation and source steps. Then construction core, group/pivot, route/category, row proposal and authoring PRs. Cap each at 2.8k of logic (`lifecycle/construction_proposal.go:1`, 2,014 physical lines). | `go test ./internal/{explorer/lifecycle,explorer/receipt,explorer/importedsource,api/explorer,server}` | Zero new package crossings. The rejected six-way split had ≥20/84 cross-group candidates (`lifecycle/filter_source_pivot_clear_test.go:131,955`, `service_test.go:884)). It is blocked pending ownership and test partitioning. |
| 5a | Command dispatch by variant (~1k at `authoringv2/commands.go:591,880`). Defer stored package extraction. | `go test ./internal/{explorer/authoring/document,explorer/lifecycle,explorer/compilation,explorer/recipeimport,explorer/arango,api/explorer}` | Zero new boundaries for handlers. The blanket stored split had 5/51 candidates (`authoringv2/table_shape_test.go:37,247`). Keep archived shape fixtures with model behavior pending a narrower cut. |
| 5b | Project-config preparation, then view-builder orchestration. Separate PRs under 3k each. | `go test ./internal/{explorer,explorer/projectconfig,explorer/viewbuilder,explorer/arango,explorer/receipt,ingest,api/explorer,server}` | 0/23 candidates for the revised file cut (`view_builder_prepare_test.go:15,148`, `viewer_projection_order_test.go:82`). Keep viewer projection in root and recount if individual declarations move. |
| 5c | Explorer Arango aggregate receivers in place, publication/view-builder first, then drafts, receipts, selections and interpretations. Budget <3k per aggregate (`explorer/arango/publication.go:13`, `view_builder.go:157`). | `go test ./internal/{explorer/arango,explorer/lifecycle,explorer/receipt,api/explorer,server}` | Zero new boundaries. Retain shared transaction integration checks. |
| 5d | Recipe construction kinds, document type files, then rich validation. One in-package PR per family, each under 3k (`recipe/construction.go:1`, `document_types.go:1`). | `go test ./internal/{dataframe/recipe,dataframe/compiler/lower,dataframe/expression,explorer/authoring/document,explorer/compilation}` | Zero new boundaries. Test recipe wire and validation behavior in place. |
| 6a | Lowerer state, related sources, then group/reshape in separate PRs under 3k (`lower/recipe_construction.go:428,1196,1649`). | `go test ./internal/dataframe/{compiler/lower,compiler,compiler/ir,compiler/render/aql,execution}` | Zero new boundaries. Keep lowering behavioral checks together. |
| 6b | Compiler root features. One PR each for lineage/trace, category, and population/table shape, under 3k each. | `go test ./internal/dataframe/{compiler,compiler/render/aql,execution}` | Zero new boundaries. Keep feature checks in their existing layers. |
| 6c | IR stage, table, combine and scope families separately, splitting any family over budget. | `go test ./internal/dataframe/{compiler/ir,compiler/lower,compiler/optimize,compiler/render/aql,compiler/render/clickhouse,compiler,execution}` | Zero new boundaries. Keep generator tests beside IR. |
| 7a | AQL stage, reshape, navigation, lineage/trace and category renderers. One <3k PR per family. | `go test ./internal/dataframe/{compiler/render/aql,compiler,execution}` | Zero new boundaries. Keep shared renderer fixtures. |
| 7b | Execution preview, run and explain. One <3k PR per receiver after its compiler and renderer cuts. | `go test ./internal/{dataframe/execution,dataframe/execution/arango,dataframe/recipe/runtime,explorer/receipt,api/dataframe/query,server}` | Zero new boundaries. Keep execution fakes with engine tests. |
| 8 | Review complete. Implement the [long-function first pass](#phase-8-long-function-review) one owner per PR. | Run the focused packages below and `go test ./internal/...` for the combined revision. | In-package cuts. Recount test dependencies if a package boundary changes. |
| 9a | Publication declarations and publish loop (~1.3k). | `go test ./internal/{dataframe/publication,dataframe/publication/arango,dataframe/publication/clickhouse,dataframe/execution,dataframe/published,dataset,server}` | Zero new boundaries. |
| 9b | ClickHouse bundle transaction file families (~1.2k). | `go test ./internal/{dataframe/publication/clickhouse,dataframe/publication,store/clickhouse,server}` | Zero new boundaries. |
| 9c | Artifact format extraction (one source file). | `go test ./internal/{dataframe/artifactformat,dataframe/published,explorer,explorer/lifecycle,explorer/artifactfs,api/explorer}` | 0/13 cross-group candidates. Move codec tests (`published/artifact_test.go:86`). |
| 9d | Merge column capabilities into publication (62 lines). | `go test ./internal/{dataframe/publication,explorer/constructioninputs,api/dataframe/routes,server}` | Move mapping tests with the adapter. |
| 10 | Arango preview index receiver/file ownership (<1k, `store/arango/preview_index.go:24`). | `go test ./internal/{store/arango,dataframe/execution/arango,dataset/arango,server}` | Zero new boundaries. |
| 11a | Explorer command dispatch, proposal-plus-preview, then view mutation orchestration after 4a and 5b. <1k each. | `go test ./internal/{api/explorer,explorer/lifecycle,explorer/viewbuilder,server}` | Keep wire assertions at the edge. Private overlap for the future helper cut is unverified. |
| 11b | Dataframe DTO grouping (~0.7k). | `go test ./internal/{api/dataframe,api/dataframe/routes,api/dataframe/query,api/explorer,server}` | Zero new boundaries. |
| 11c | Query scoped input resolution and preview default (<1k, `input_resolution.go:67`). | `go test ./internal/{api/dataframe/query,api/dataframe/routes,server}` | Zero new boundaries. |
| 11d | Bulk source-load application operation, then multipart staging cleanup. <1k each. | `go test ./internal/{api/bulk/load,ingest,dataset,server}` | Keep multipart checks at the edge. Private overlap for the future application cut is unverified. |
| 11e | Merge recipe discovery into runtime (127 lines). | `go test ./internal/{dataframe/recipe/runtime,dataframe/recipe/fielddiscovery,api/dataframe/query,server}` | Move discovery tests with the adapter. |
| 12 | FHIR member walk state (~1k). | `go test ./internal/{fhir/semantic,fhir/schema,catalog,ingest}` | Zero new boundaries. Move legacy tests after separating shared path helpers. |
| 13 | Authscope path and access-client file ownership (~1k). | `go test ./internal/{authscope,api/authpolicy,api/http,api/explorer,api/dataframe/query,dataframe/recipe/runtime,server}` | Zero new boundaries. |

Counts are source-level candidates from private declaration and reference scans of same-package tests. They can include shadowed names and miss private field uses. Recount against the final declaration cut. Broader test-suite organization is outside this plan.

Keep 2–3, 4–5 and 6–7 in dependency order within each track. Run independent tracks in separate worktrees when implementation is authorized. Serialize merges and recheck import rewrites.

## Overnight execution

This runbook applies when the owner authorizes implementation. The phase table defines review units, test targets, and dependencies. Use current package paths until each rename is approved. The plan may be untracked, so read it from this checkout before switching to an implementation checkout.

For Phase 0, run the reachability scanner with Go 1.27 first on `PATH`. The executable alone can be built with Go 1.27 and still resolve Go 1.22 when it launches `go list`. Use the cached `golang.org/x/tools` revision `v0.53.1-0.20261120204933-70e48cd7db64` with Go 1.27.4. Compare `deadcode -filter=github.com/calypr/loom/internal ./...` with the same invocation using `-test`. Check reflection, generated edges, command roots, and existing tests before removing reported code.

**Defaults for an unattended run.** Preserve the production semantic coverage reader and wire behavior. Phases 2–3 can proceed in dependency order. Defer the Phase 1 rename list, including publication names, until approved. The command-handler refactor can run in `authoringv2`. Defer the stored-workspace extraction. Use one local commit per completed review unit under AGENTS.md. Pushes, PR publication, deployment, and database changes require separate authorization.

1. Record the branch, HEAD, dirty files, package audit, and current import graph. Check the focused baseline for 4a and identify any pre-existing failures. Treat Phase 0 reachability as a separate track. A missing tool gates deletions that rely on it.
2. Start with 4a. Then advance the Explorer track in dependency order through 4b, 5a–c, and 11a. Start 11a command dispatch and proposal-preview units once their lifecycle prerequisites pass. View mutation work waits for the view-builder boundary.
3. Advance independent tracks through 5d and 6–7, then the publication, storage, transport, FHIR, and authscope units in 9–13. Run a unit earlier when its prerequisites are met and it uses an independent checkout or runtime. Run 8 after the other eligible units. Run the catalog and ingest track through 2–3 with the production reader intact.
4. Check each unit against its focused tests and the verification section on the exact source snapshot. Bound each run and retain its command, log path, exit status, and deadline. Follow AGENTS.md for review, separate worktrees during concurrent implementation, and serialized checks on shared services. After a failure, diagnose it and retry only with a changed hypothesis or a justified budget.
5. If a prerequisite, test, or review remains blocked, leave the unit uncommitted and isolate its changes from the checkout used for the next unit. Skip its dependents and continue eligible work. Leave unrelated edits intact.
6. Stop when eligible units have either passed or been recorded as blocked. Report local commit IDs, pending changes, tests and review outcomes, deferred decisions, and the next runnable unit.

**Implementation status.** Phase 4a is staged on `main` at `a04663fa4`. The integrated implementation is on local branch `internal-cleanup` (`dd0468c70`), checked out in `/private/tmp/loom-cleanup-fieldprofile-a04663fa4`. Keep the production semantic coverage reader wired.

- **Catalog and ingest, phases 2–3.** `catalog/codedconcept.Extractor` preserves pairing before the standalone Coding scan. `catalog/semanticinventory.Emitter` uses it directly. The exact contribution keys and ordering, coded-value pairing, and retained emitter version passed their existing assertions. An independent baseline comparison over 112 NDJSON fixtures (270 rows) matched the serialized bytes of 313 retained and 313 profiler contributions and all field-profile documents. Both result manifests hash to `dc0671bdec1e736306f180dc3e7be4489316fe127fbef7dae55c274bb9d53e49`. `catalog/fieldprofile.Profiler` owns field profiling and semantic coverage reconciliation, with storage still in `catalog/arango`. `ingest/generation` owns generation and plain-file loading, `ingest/sourceload` owns source validation, leases, copy and publish, and both use the row builder in `ingest`. The package audit verified the resulting import graph. See `catalog/codedconcept/extractor.go`, `catalog/semanticinventory/inventory.go`, `catalog/fieldprofile/write_profiler.go`, `ingest/generation/generation_orchestration.go`, and `ingest/sourceload/source_load.go`.
- **Ownership work, phases 4–13.** The implemented cuts cover lifecycle ports, row proposals, construction bases, row sources, routes and scans, command intent and handling, project config and view builder, lowering stage handlers, receipt output reconciliation, compiler column handling, AQL stage output, the ingest file scanner and writer, IR validation, and the publication, transport, FHIR and authscope changes. The AST recount found 92 production functions of at least 150 physical lines, down from 100 at the baseline. The inventory is in `/tmp/loom-cleanup-latest-long-functions.tsv`.
- **Verification and integration.** Build, vet, Go comment lint, `package_audit_verify.py --full`, and the generation file-loader race tests passed on `/tmp/loom-cleanup-integrated-a04663fa4.patch` (SHA-256 `1aaca2fcefc1b4cfe3ec42af85b267556465862b2fe9d6337302cbfc75ac077c`). The log is `/tmp/loom-cleanup-integrated-full.log`. Source review checked writer batching, stored keys, loader order, and IR/AQL behavior. Live database checks are unverified. The owner authorized local commits on `internal-cleanup`.


## Phase 8 long-function review

Reviewed all 92 production functions of at least 150 physical lines in the verified implementation (patch SHA-256 `1aaca2fcefc1b4cfe3ec42af85b267556465862b2fe9d6337302cbfc75ac077c`). The locations refer to that integrated tree. The review found 18 first-pass candidates, 52 follow-ups, 19 to keep, and 3 deferred with the stored-workspace work. The cutoff selects functions for review. Each proposed extraction needs a separate responsibility or state lifetime.

Start with the file loader, typed operation translation, and semantic inventory browsing. They expose three distinct seams: worker versus coordinator state, typed mapping by operation, and authorization/cursor admission versus item assembly. Scope each review unit to one owner and under roughly 3k moved lines of logic. Keep these cuts within the existing packages. Recount private test dependencies if a later change introduces a package boundary.

| First-pass owners | Focused verification |
| --- | --- |
| Generation file pipeline | `go test ./internal/ingest/generation ./internal/ingest/sourceload ./internal/api/bulk/load ./internal/server`. Run `go test -race ./internal/ingest/generation -run '^TestLoadFile' -count=1` for the file pipeline. |
| Construction translator and semantic column compiler, separate PRs | `go test ./internal/explorer/compilation ./internal/explorer/receipt ./internal/explorer/lifecycle ./internal/dataframe/compiler/lower`. |
| Semantic inventory browse and authoring command preparation, separate PRs | `go test ./internal/explorer/lifecycle ./internal/explorer/receipt ./internal/api/explorer ./internal/server`. Browse must preserve scope rejection before storage reads (`semantic_catalog_test.go:487`). |
| Pivot renderer and intrinsic call renderer, separate PRs | `go test ./internal/dataframe/compiler/render/aql ./internal/dataframe/compiler ./internal/dataframe/execution`. |
| Viewer projection, source-plan editing, receipt metadata and construction inputs, separate PRs | `go test ./internal/explorer ./internal/explorer/projectconfig ./internal/explorer/viewbuilder ./internal/explorer/recipeimport ./internal/explorer/receipt ./internal/explorer/constructioninputs ./internal/api/explorer`. |
| Group lowerer, recipe operation validation and expression call inference, separate PRs | `go test ./internal/dataframe/compiler/lower ./internal/dataframe/compiler/ir ./internal/dataframe/recipe ./internal/dataframe/expression ./internal/dataframe/compiler`. |
| Capability snapshot builder and evidence reducer, separate PRs | `go test ./internal/explorer/capability ./internal/explorer/capability/resolver ./internal/explorer/lifecycle`. |
| ClickHouse preparation and semantic-source validation, separate PRs | `go test ./internal/dataframe/execution ./internal/dataframe/publication/clickhouse ./internal/explorer/authoringv2 ./internal/explorer/compilation ./internal/explorer/lifecycle`. |

The generation file loader now delegates spill-run finalization and source-file entry batching to `generation_file_inventory.go`. The focused package tests and loader race checks passed. Row-worker extraction remains open, along with the other first-pass units. Live database checks retain the deferral in the verification section.

<details>
<summary>All 92 function verdicts and source locations</summary>

| Source and function | Lines | Verdict | Reason or seam |
| --- | ---: | --- | --- |
| `internal/ingest/generation/generation_workers.go:205` · `load` | 463 | First pass | Extract the row workers (lines 326–532) and spill/merge (535–599) separately. Keep cancellation and channel closure in the file coordinator. Retain the cancellation and writer-failure assertions (`file_pipeline_test.go:167,208`). |
| `internal/ingest/generation/generation_orchestration.go:53` · `loadGeneration` | 369 | Follow-up | Keep the lifecycle coordinator. Consider extracting catalog finalization (lines 332–420) with manifest transitions and failure defers visible at the top level. |
| `internal/explorer/compilation/construction.go:326` · `recipeConstructionOperation` | 346 | First pass | Dispatch the authored operation variants to typed translators; keep one switch and preserve errors for unresolved selectors. |
| `internal/explorer/lifecycle/semantic_catalog.go:264` · `BrowseSemanticInventory` | 324 | First pass | Separate authorized browse context/cursor resolution from item projection; preserve authorization and cursor error precedence. |
| `internal/dataframe/compiler/render/aql/stage_sequence.go:13` · `renderPhysicalStageSequence` | 313 | Follow-up | Extract preview-frontier and two-scan source preparation before allocating renderer bind variables; preserve name allocation order. |
| `internal/dataframe/compiler/render/aql/table_reshape.go:37` · `renderGroupedTablePivot` | 309 | First pass | Split literal/coded category rendering and unlisted-evidence projection behind pivot renderer methods. |
| `internal/dataframe/compiler/lower/recipe_construction_related_expand.go:17` · `lowerConstructionRelatedExpand` | 309 | Follow-up | Separate anchor selection, route lowering and contributor projection within constructionLowerer; share only pure route preparation with eligibility. |
| `internal/explorer/compilation/semantic_compile_columns.go:31` · `compile` | 306 | First pass | Move indexed field fan-out, ordinary field projection and final emission onto column compiler methods, preserving emission order and deduplication. |
| `internal/explorer/viewer_projection.go:52` · `buildViewerProjection` | 305 | First pass | Extract per-view projection from revision lookup and output indexing; preserve explicit view and column order. |
| `internal/dataframe/compiler/lower/recipe_construction_reshape.go:13` · `lowerConstructionGroup` | 301 | First pass | Separate key and aggregate lowering; keep shared bindings and row-value ordering on the group lowerer. |
| `internal/catalog/arango/semantic_coverage.go:2088` · `LookupSemanticCoverage` | 297 | Follow-up | Separate snapshot read (lines 2106–2151), completeness proof (2153–2238), and matching (2241–2382). Retain the partition isolation and build-state tests (`semantic_coverage_test.go:743,1060`). |
| `internal/explorer/recipeimport/source_plan_edit.go:23` · `EditPlan` | 294 | First pass | Separate field edit normalization, relationship updates and replacement validation under the existing plan editor. |
| `internal/explorer/receipt/explorer_output_reconcile.go:111` · `ReconcileFinalOutputMetadataWithCombineInputs` | 289 | First pass | Extract per-output metadata reconciliation from workspace-wide indexes; keep authored and emitted ordering. |
| `internal/dataframe/recipe/construction_operation.go:53` · `validateConstructionOperation` | 267 | First pass | Keep common row-value rules once, then validate each operation variant in its own handler. |
| `internal/dataframe/compiler/lower/recipe_schema.go:14` · `recipeOutputSchema` | 250 | Follow-up | Separate semantic node traversal from physical post-shaping schema adjustment; preserve output order. |
| `internal/dataframe/compiler/ir/clone.go:425` · `clonePhysicalOperation` | 248 | Keep | Deep-copy coverage is easiest to audit beside all pointer fields. Keep populated payload checks even when operation kind differs. |
| `internal/explorer/arango/workspace_migration.go:150` · `planStoredWorkspaces` | 244 | Deferred | Keep encoding and table-shape migration planning together while stored-workspace work is deferred. |
| `internal/dataframe/compiler/render/aql/navigation_layout.go:16` · `buildNavigationRenderLayout` | 243 | Follow-up | Extract root predicate parsing from the post-window operation scan; preserve operation indexes in diagnostics. |
| `internal/dataframe/compiler/lower/recipe_construction.go:1400` · `lowerConstructionRelatedSource` | 234 | Follow-up | Pair with related-expand lowerer review. Extract shared route lowering only after verifying anchor and contributor differences. |
| `internal/catalog/fieldprofile/semantic_coverage.go:992` · `ReconcileSemanticCoverage` | 226 | Keep | One reducer. Occurrence policy already delegates (lines 1034–1109), followed by canonicalizing paths and diagnostics under the same evidence caps. |
| `internal/explorer/capability/builder.go:110` · `Build` | 225 | First pass | Separate resource/node, relationship, and field snapshot assembly within the builder; keep deterministic output ordering. |
| `internal/dataframe/compiler/row_lineage.go:52` · `CompileRowLineageOutput` | 225 | Follow-up | Extract typed lineage plan variants before the final renderer call; retain validation and unsupported-case ordering. |
| `internal/dataframe/compiler/lower/recipe_construction_related_eligibility.go:20` · `lowerConstructionRelatedEligibility` | 224 | Follow-up | Pair with related-expand review; isolate eligibility predicate lowering after source and route resolution. |
| `internal/explorer/lifecycle/semantic_catalog.go:613` · `semanticInventoryPresentationForOutput` | 219 | Follow-up | Keep with semantic inventory browse work; separate scoped coverage lookup/filtering from field item projection. |
| `internal/explorer/constructioninputs/catalog.go:148` · `List` | 217 | First pass | Separate reference eligibility from pinned page reads; keep pin acquisition and release in List. |
| `internal/explorer/authoringv2/stored_table_shape.go:104` · `Validate` | 217 | Deferred | Stored table-shape decoder validation belongs with the deferred stored-workspace work. |
| `internal/explorer/authoringv2/construction_change.go:53` · `AnalyzeConstructionCandidate` | 216 | Follow-up | Separate proposed edit classification from removal and downstream-impact checks; keep diagnostic order. |
| `internal/dataframe/compiler/render/aql/calls.go:10` · `renderCall` | 216 | First pass | Group AQL call handlers by intrinsic family after common arity and operand rendering; preserve the supported-name list. |
| `internal/explorer/lifecycle/construction_proposal.go:731` · `ProposeConstruction` | 215 | Follow-up | Extract source rebinding from receipt/preview assembly; keep candidate authorization and persistence sequencing explicit. |
| `internal/explorer/authoringv2/construction_change.go:610` · `rebuildStageColumns` | 212 | Follow-up | Dispatch rebuilding by stage variant; leave common schema identity validation in the entrypoint. |
| `internal/dataframe/compiler/ir/physical_stage_group_validation.go:43` · `validatePhysicalStageGroup` | 211 | Follow-up | Group key/aggregate/output checks can be named separately inside the stage validator; preserve first error. |
| `internal/dataframe/compiler/lower/recipe_construction.go:291` · `appendRecipeConstructionStages` | 209 | Follow-up | Separate cohort stage insertion from iteration through authored construction; preserve exact stage ordering. |
| `internal/dataframe/compiler/ir/physical_stage_related_validation.go:66` · `validatePhysicalStageRelatedExpand` | 208 | Follow-up | Isolate anchor proof and projection checks within the related-expand validator. |
| `internal/explorer/lifecycle/construction_group_related_field_choices.go:29` · `appendRelatedGroupFieldChoices` | 202 | Follow-up | Separate related route evaluation from candidate emission; preserve route ranking and cancellation checks. |
| `internal/explorer/capability/resolver/explorer_capability.go:278` · `capabilityEvidenceFromCatalog` | 202 | First pass | Extract relationship evidence and the field/concept reducer from snapshot assembly; keep coverage warnings and key order. |
| `internal/dataframe/semantic/recipe_rich_shaping.go:17` · `lowerRecipePivots` | 202 | Follow-up | Make each pivot's typed selector/category lowering a separate method; keep input-order diagnostics. |
| `internal/explorer/compilation_receipt.go:374` · `validateReceiptConstructionStages` | 201 | Follow-up | Extract per-output stage chain validation while keeping receipt-level cross-output checks in the receipt. |
| `internal/dataframe/execution/engine.go:885` · `withPreparedClickHouse` | 201 | First pass | Separate private/published artifact preparation from execution callback; keep read pins and cleanup with the outer owner. |
| `internal/dataframe/compiler/lower/child_sets.go:366` · `projectPhysicalChildSet` | 201 | Follow-up | Name collection and rewrite passes on the same child-set projection state; preserve traversal and liveness rules. |
| `internal/explorer/receipt/explorer_receipt_contract.go:114` · `Compile` | 198 | Follow-up | Separate receipt assembly from compile/defer lifecycle; keep the scoped compile and telemetry ordering visible. |
| `internal/ingest/backfill/semantic_inventory_backfill.go:76` · `BackfillSemanticInventory` | 197 | Follow-up | Extract per-collection paging (lines 150–258). Keep claim and finalization in the outer driver and advance the checkpoint only after a successful flush (`semantic_inventory_backfill_test.go:19,155`). |
| `internal/explorer/lifecycle/authoring.go:43` · `applyCommandsWithConstructionPreview` | 197 | First pass | Isolate command-specific preparation callbacks; retain transactional apply and checked postconditions together. |
| `internal/dataframe/recipe/document_validation.go:32` · `validate` | 196 | Follow-up | Pull per-output structural checks into an output validator; leave bundle-wide identifiers and error paths in the bundle. |
| `internal/dataframe/compiler/ir/physical_table_shape.go:12` · `validatePhysicalGroupedPivot` | 196 | Follow-up | Split pivot shape/identity checks from category checks; preserve error order. |
| `internal/dataframe/semantic/recipe_rich_shaping.go:354` · `lowerRecipeAggregates` | 195 | Follow-up | Extract one aggregate's lowering and unit/window validation within the owning semantic recipe lowerer. |
| `internal/dataframe/compiler/ir/physical_validation_refs.go:277` · `validatePhysicalCorrelationWithCodeBind` | 195 | Follow-up | Separate selector validation from normalized bind validation while preserving matching order and scope error precedence. |
| `internal/explorer/lifecycle/explicit_groups.go:53` · `CreateExplicitGroupRevision` | 193 | Follow-up | Extract group input validation and member resolution; retain revision write and idempotency in one flow. |
| `internal/dataframe/expression/expression_checking.go:178` · `inferCall` | 187 | First pass | Use call-family inference helpers after common checks; keep per-call type and nullability rules explicit. |
| `internal/dataframe/compiler/render/aql/row_lineage_related_coding.go:10` · `renderRelatedCodingIdentityGroupRowLineage` | 186 | Follow-up | Review with the other lineage renderers; isolate key matching from member projection without merging differing identity rules. |
| `internal/dataframe/compiler/lower/plan_orchestration.go:15` · `buildGenericPhysicalPlanWithPolicy` | 186 | Follow-up | Move semantic graph walking into a plan builder with explicit alias and projection state. |
| `internal/dataframe/compiler/ir/physical_validation_expr.go:10` · `validatePhysicalExpression` | 186 | Follow-up | Use per-expression payload validators after common checks; retain cycle detection before recursion. |
| `internal/dataframe/compiler/render/aql/render.go:350` · `renderPhysicalPlanWithOptions` | 185 | Follow-up | Extract root/window/terminal rendering from layout assembly; keep bind names and statement order. |
| `internal/dataframe/compiler/render/aql/group_row_value_preview.go:25` · `terminalGroupRowValuePreviewWindowFor` | 185 | Keep | One eligibility recognizer for one optimization. Keep ordered proof conditions and its returned window shape together. |
| `internal/explorer/authoringv2/semantic_types.go:605` · `validate` | 183 | First pass | Dispatch source-kind validation after common checks; preserve error paths for saved authoring documents. |
| `internal/dataframe/compiler/render/aql/table_reshape_streaming.go:295` · `renderGroupedTablePivotStreamingMissingPreview` | 183 | Follow-up | Review with pivot rendering; separate streaming accumulator rendering from terminal output and scope gating. |
| `internal/dataframe/compiler/lower/group_related_occurrence_lower.go:145` · `lowerConstructionGroupRelatedCodingKey` | 182 | Follow-up | Separate related occurrence projection from coding key construction inside the lowerer; preserve source availability metadata. |
| `internal/dataframe/compiler/lower/recipe_derived_columns.go:27` · `appendRecipeDerivedColumnsWithVariables` | 180 | Follow-up | Name dependency visit and emission on the same derived-column compiler state; keep cycle and ordering behavior. |
| `internal/dataframe/compiler/render/aql/row_lineage.go:19` · `renderConstructionRowLineage` | 175 | Follow-up | Review with the lineage renderers; separate key proof assembly from return projection. |
| `internal/explorer/authoringv2/row_change.go:103` · `AssessRowChange` | 172 | Follow-up | Separate token/route resolution from impact calculation; keep exact draft and snapshot checks in entrypoint. |
| `internal/explorer/lifecycle/construction_pivot_source.go:445` · `constructionCandidateWithPivotSources` | 171 | Follow-up | Extract exact pivot-source resolution from candidate projection updates; preserve stable slot IDs. |
| `internal/explorer/capability/construction_routes.go:51` · `PlanConstructionRoutes` | 171 | Keep | One route enumerator with ordered search state and bounds; splitting the traversal hides its ranking invariants. |
| `internal/dataframe/compiler/render/aql/validation.go:114` · `validateRenderableOperation` | 171 | Follow-up | Dispatch renderability checks by operation family; preserve unsupported kind and scope diagnostics. |
| `internal/ingest/sourceload/source_load_upload.go:26` · `UploadFile` | 170 | Keep | Keep validation, lease, import, rehash and durable acknowledgement in one visible sequence. Existing helpers already own the individual operations (`source_load_ack_e2e_test.go:25`). |
| `internal/dataframe/compiler/render/aql/construction_group.go:222` · `renderConstructionGroupRelatedOccurrenceInput` | 170 | Follow-up | Separate related input projection from coding unnest rendering; preserve bindings and row scope. |
| `internal/dataframe/compiler/lower/recipe_construction.go:1229` · `lowerConstructionStep` | 170 | Follow-up | Output finalization already follows typed handlers. Name the remaining companion/anchor propagation only where stage invariants can be kept together. |
| `internal/dataframe/compiler/ir/physical_validation_expr.go:312` · `validatePhysicalExpressionObjectCycles` | 170 | Keep | The expression, predicate, object and subplan visitors share one cycle detector and one stack lifetime. |
| `internal/explorer/lifecycle/construction_group_field_choices.go:17` · `constructionGroupFieldChoices` | 168 | Follow-up | Separate existing-owner enumeration from new source-expansion choices; preserve ranking and scope with related-field choices. |
| `internal/explorer/compilation_receipt.go:874` · `ValidateArchive` | 162 | Deferred | Archive version checks remain with the receipt until the compatibility work is reopened. |
| `internal/explorer/authoringv2/source_parameters.go:120` · `Validate` | 162 | Follow-up | Separate recursive extension/value inspection from variant-specific parameter validators, retaining depth and validation precedence. |
| `internal/dataframe/compiler/render/aql/row_lineage.go:195` · `renderCodingIdentityGroupRowLineage` | 162 | Follow-up | Review with related and semantic lineage; extract a shared key projection only if emitted AQL and identity semantics match. |
| `internal/explorer/lifecycle/source_relationship_proposal.go:52` · `ProposeSourceRelationship` | 161 | Keep | One authorized proposal sequence with existing collaborators; keep binding, receipt creation and errors in order. |
| `internal/dataframe/compiler/render/aql/row_lineage.go:358` · `renderSemanticGroupRowLineage` | 161 | Follow-up | Review with coding lineage; keep semantic family identity specific while separating key match from return assembly. |
| `internal/dataframe/compiler/ir/physical_combine.go:87` · `validate` | 161 | Follow-up | Extract per-input and per-output shape validation, then dispatch combine-kind rules; preserve first-error semantics. |
| `internal/explorer/lifecycle/construction_category_discovery.go:123` · `compileConstructionCategoryCandidate` | 160 | Keep | One candidate compile pipeline with existing focused helpers; keep exact draft/candidate/receipt checks in one place. |
| `internal/explorer/lifecycle/construction_group_field_choices.go:285` · `resolveConstructionGroupFieldSources` | 159 | Follow-up | Separate per-step choice binding from document updates; keep companion placement and row order. |
| `internal/explorer/lifecycle/artifact.go:49` · `PrepareArtifact` | 159 | Keep | One artifact write lifetime; retain lookup, write/abort and commit ordering in one visible owner. |
| `internal/dataframe/compiler/lower/recipe_dynamic_columns.go:14` · `appendRecipeDynamicColumns` | 159 | Follow-up | Extract per-column dynamic selector lowering; keep shared prefix/name state and output order on lowerer. |
| `internal/explorer/lifecycle/frame_source.go:105` · `BrowseFrameSourceOptions` | 158 | Follow-up | Separate cursor admission from page filling while keeping eligibility and cursor ordering in one flow. |
| `internal/explorer/lifecycle/construction_proposal.go:1588` · `prepareConstructionProposal` | 158 | Keep | One proposal acceptance protocol. Keep live workspace and receipt match, preview and acceptance order readable together. |
| `internal/explorer/arango/project_config_publication.go:56` · `PublishProjectConfigs` | 158 | Keep | Keep transaction writes together. Extraction would obscure atomic title/config/revision publication; existing validation helpers suffice. |
| `internal/dataframe/compiler/recipe_paging.go:505` · `rootPageRelatedCandidateSeed` | 158 | Keep | One related candidate seed recognizer. Keep refusal conditions beside the exact accepted layout. |
| `internal/dataframe/compiler/lower/recipe_construction_group_sources.go:21` · `lowerConstructionGroupScalarSource` | 158 | Follow-up | Dispatch scalar source roles locally after common validation; preserve selector and cardinality failures. |
| `internal/dataframe/compiler/preview_partition_liveness.go:211` · `collectPreviewExpressionUses` | 157 | Keep | One liveness traversal with variant-specific references. Keep recursive visits and reachable expression arms visible together. |
| `internal/dataframe/execution/preview.go:98` · `execute` | 156 | Follow-up | Isolate preview stream selection from row consumption with limits, caching and cancellation state on the executor. |
| `internal/explorer/authoringv2/commands.go:1062` · `validateEditableSourcePolicy` | 154 | Follow-up | Extract aggregate-specific policy checks; keep generic source gates first. |
| `internal/dataframe/compiler/lower/recipe_compile.go:275` · `compileRecipeOutput` | 154 | Keep | One recipe-output lowering pipeline with named stages; lifecycle/order is clearer in one driver. |
| `internal/dataframe/compiler/ir/clone.go:787` · `clonePhysicalExpression` | 152 | Keep | Keep expression payload deep copies together while representations allow multiple populated payload fields. |
| `internal/explorer/lifecycle/unpivot_source.go:251` · `constructionCandidateWithUnpivotSources` | 151 | Follow-up | Separate exact input resolution from candidate update; keep earlier-step order and collision handling. |
| `internal/dataframe/compiler/render/aql/related_group_preview.go:40` · `relatedGroupPreviewShapeFor` | 151 | Keep | One preview shape recognizer. Keep all refusal predicates beside the accepted shape. |
| `internal/dataframe/compiler/lower/recipe_construction_coded_group.go:13` · `lowerConstructionCodedGroup` | 151 | Keep | One typed coded-group lowering path; construction and metadata use the same resolved bindings. |
| `internal/dataframe/artifactformat/artifact.go:162` · `WriteArtifact` | 151 | Keep | One archive write lifetime. Keep member order, pending errors and writer finalization together. |
| `internal/explorer/compilation/compile.go:609` · `compileImportedDocument` | 150 | Keep | One imported-document compile path with existing collaborators; preserve the source and authored-column mapping passes in order. |

</details>

## Verification (every phase)

1. Before and after any package move:
   - `python3 scripts/package_audit_verify.py <pkg>`, then `--full`
   - inspect the dependency diff
2. Run:
   - `go build ./... && go vet ./...`
   - `go test` on moved packages and their importers
   - `make lint-go-comments`
   - `make test-go`
3. `make test-integration` is deferred while database work is out of scope. Record it as unverified for phases that touch `*/arango`.
4. Use `pstack:interrogate` for phases 4, 5 and 7, which carry the highest risk of hidden behavior coupling.
5. No test assertion is weakened. Deleted tests are recorded in [DELETED_GUARANTEES.md](verification/DELETED_GUARANTEES.md).

## Decisions for the owner

1. **Semantic coverage.** Preserve the production reader during cleanup. Server wiring uses the catalog for capability resolution, inventory browsing, and coverage lookup. Builder field presentation calls the lookup and handles complete, partial, and unavailable coverage (`server/wire_explorer.go:43,121–123`, `explorer/lifecycle/semantic_catalog.go:632–665`). The current source scan found no production callers for `WriteSemanticCoverageContributions` or either `ObservePayloadWithCoverage` entrypoint (`catalog/arango/semantic_coverage.go:380`, `catalog/semantic_inventory.go:496`, `catalog/write_profiler.go:59`). In Phase 0, remove code proven unreachable from production entrypoints. Retain shared reader helpers, existing stored-data support, and test fixtures that exercise production behavior. Phases 2–3 can proceed with that boundary. Feature retirement or writer restoration is a separate behavior change.

2. **The rename list.** Approve or adjust the names.
3. **Commit cadence.** One commit per phase or per package, and whether each phase gets its own PR.

## Appendix A: production code only tests reach

Measured with `deadcode` before Go 1.27 and rechecked with `rg` on `a04663fa4`.
Phase 0 re-derives this list.

- **`catalog`:** `NewShapePlanCacheWithLimit`, `Profiler.RetentionBytes`, `retentionBudget.usage`,
  `Cache.Stats`, `fieldCatalogStats.addDistinctWithLimits`/`addPivotColumnWithLimits` (duplicate
  the Profiler versions), both `ObservePayloadWithCoverage`, `DiscoverSemanticCodeableConceptTemplates`
  and its schema walk (a test oracle).
- **`dataframe/execution`:** the `Control` type (6 methods plus `outputRows` and `recipeControlBackend`),
  `CompiledQueryFingerprint`. **`dataframe/published`:** `ExportFormat.Normalize` (published/export.go:213). Current source has no direct caller. Recheck reachability before removal.
- **`dataframe/compiler/*`:** `ComposeClickHouseCombine`, `validateClickHousePrefixBindings`,
  `RenderClickHousePrefix`, `RenderCombine`, `RenderCompositeClickHouseCombine`,
  `NewPrivateAQLPrefixScopeOperand`, `LowerCorrelatedPredicate`, `PublicOutputColumns`,
  `RenderPhysicalPlanWithTerminalProjection`.
- **`api/*`:**
  - no direct source callers found by `rg`: `ColumnFromPhysical` (dataframe/dataframe/model.go:51), `resolvePublishedSelector` (dataframe/dataframe/rows.go:87), `StatusForErrorCode` (http/errors.go:103)
  - test-only direct caller: `AggregateRowsResult` (dataframe/dataframe/model.go:73, model_test.go:41–42).
- **`fhir/semantic`:** `Walk` and its legacy object walker are test-only candidates (walk.go:28,105, walk_test.go:174,207,318). Keep `RepeatedBoundary` and the path/boundary helpers used by the production member walker (walk.go:22,170–185, member_walk.go:67,457,504).
- **`store/arango`:** `ensurePreviewCoveringIndex*`, `previewCoveringIndexMatches`,
  `migratePreviewSupersededCollection`. Check what `cmd/arango-preview-index-*` calls first.
- **`acceptance`:** `canonicalFileStats` (acceptance/fixture.go:604, fixture_test.go:182).
- **`explorer/*`:** `DecodeProjectConfigs`, `NewSemanticConstructionChoice`,
  `verifyConstructionProposalPivotCategories`, `semanticInventoryConstructionChoice`,
  `insertWorkspaceProjection`. Check `cmd/arango-workspace-migration` first.
- **`ingest`:** `loadRowGenerated`.

## Appendix B: production importers

<details>
<summary>Direct production importer and symbol inventory</summary>

The table covers direct production imports inside `internal/` for the remaining-package audit. It excludes testkit and acceptance by scope. Each caller has a source example under its directory. Symbols are package-qualified selectors and types. Calls on constructed receivers are tracked in the boundary notes above. This is a syntax inventory, so name shadowing and callers outside `internal/` are unverified.

| Dependency | Importer and source example | Qualified symbols |
| --- | --- | --- |
| `dataframe/publication` | `api/bulk/load` (service.go:53) | `BundleExecution`, `BundlePointer` |
| `dataframe/publication` | `api/dataframe/dataframe` (model.go:51) | `BundleExecution`, `BundleFailed`, `BundleLoading`, `BundlePending`, `BundlePreflight`, `BundlePublished`, `BundleQueued`, `BundleReady`, `BundleRunning`, `BundleState`, `BundleValidating`, `ErrBundleNotFound`, `PhysicalColumn` |
| `dataframe/publication` | `dataframe/execution` (publication_schema.go:26) | `ColumnDiscovered`, `ColumnExplicit`, `FlatColumnName`, `LogicalColumn` |
| `dataframe/publication` | `dataframe/publication/arango` (bundle_registry.go:28) | `BundleCatalog`, `BundleExecution`, `BundleExecutionPageFunc`, `BundleLoading`, `BundleOutputRecord`, `BundlePending`, `BundlePointer`, `BundlePreflight`, `BundlePublished`, `BundleQueued`, `BundleReady`, `BundleRunning`, `BundleState`, `DataframeSelector`, `ErrBundleLeaseLost`, `ErrBundleNotFound`, `ErrBundlePointerConflict`, `ErrExecutionReadPinLost`, `ExactExecutionCatalog`, `ExecutionReadPinCatalog`, `PagedBundleCatalog` |
| `dataframe/publication` | `dataframe/publication/clickhouse` (bundle_store.go:24) | `BundleCatalog`, `BundleExecution`, `BundleExecutionPageFunc`, `BundleFailed`, `BundleIdentity`, `BundleLoading`, `BundleOutputRecord`, `BundlePending`, `BundlePointer`, `BundlePreflight`, `BundlePublished`, `BundleQueued`, `BundleReady`, `BundleRunning`, `BundleState`, `BundleValidating`, `CloneQualityReports`, `ColumnDiscovered`, `ErrBundleLeaseLost`, `ErrBundleNotFound`, `ErrBundlePointerConflict`, `ErrExecutionReadPinActive`, `ErrExecutionReadPinLost`, `ExecutionReadPinCatalog`, `FinalSchemaDigest`, `LogicalColumn`, `OutputSchema`, `PagedBundleCatalog`, `PhysicalColumn`, `PublicationIdentity`, `PublishedOutput`, `QualityReport`, `SourceRowMetadata`, `Target`, `Transaction`, `ValidateQualityReportBindings`, `ValidateQualityReports`, `WithPhase` |
| `dataframe/publication` | `dataframe/published` (current.go:36) | `BundleCatalog`, `BundleExecution`, `BundleOutputRecord`, `BundlePublished`, `ErrBundleNotFound`, `ErrExecutionReadPinLost`, `ErrSelectionSourceIdentityChanged`, `ErrSelectionSourceNotAddressable`, `ExactExecutionCatalog`, `ExecutionReadPinCatalog`, `PhysicalColumn`, `SourceRowMetadata` |
| `dataframe/publication` | `dataframe/recipe/runtime` (recipe_publication.go:62) | `BundleIdentity`, `DefaultQualityPolicyVersion`, `Limits`, `OutputStream`, `PublicationIdentity`, `Publish`, `QualifyFlatRow`, `QualityPolicy`, `SourceRowMetadata`, `Target` |
| `dataframe/publication` | `explorer` (project_config_prepare.go:134) | `BundleExecution`, `BundleOutputRecord`, `BundlePublished`, `CloneQualityReports`, `ColumnDiscovered`, `ErrSelectionSourceNotAddressable`, `FinalSchemaDigest`, `FlatColumnName`, `LogicalColumn`, `OutputSchema`, `PhysicalColumn`, `PublicationIdentity`, `QualityReport`, `SourceRowMetadata`, `WithPublicationMetadata` |
| `dataframe/publication` | `explorer/arango` (project_config_publication.go:346) | `BundleExecution`, `BundlePublished`, `CloneQualityReports`, `QualityReport` |
| `dataframe/publication` | `explorer/constructioninputs` (catalog.go:174) | `BundleCatalog`, `BundleExecution`, `BundleOutputRecord`, `BundlePointer`, `BundlePublished`, `BundleState`, `ErrBundleNotFound` |
| `dataframe/publication` | `explorer/lifecycle` (artifact.go:603) | `BundleIdentity`, `BundleOutputRecord`, `CloneQualityReports`, `DefaultQualityPolicyVersion`, `ErrBundleNotFound`, `ErrSelectionSourceIdentityChanged`, `PhysicalColumn`, `QualityComplete`, `QualityPassed`, `QualityReport`, `ValidateQualityReports` |
| `dataframe/publication` | `explorer/receipt` (explorer_publication.go:72) | `BundleIdentity`, `CloneQualityReports`, `PhysicalColumn`, `SourceRowMetadata`, `Target` |
| `dataframe/publication` | `ingest` (source_publication_adapter.go:89) | `BundleCatalog`, `BundleIdentity`, `BundlePublished`, `Target` |
| `dataframe/publication` | `server` (run.go:85) | `BundleCatalog`, `NewPublicationVerifier`, `Target` |
| `dataframe/publication/arango` | `explorer/arango` (activation.go:54) | `BundleExecutionsCollection` |
| `dataframe/publication/arango` | `explorer/receipt` (explorer_publication.go:19) | `Registry` |
| `dataframe/publication/arango` | `server` (wire_dataframe.go:53) | `BootstrapSpec`, `New`, `Registry` |
| `dataframe/publication/clickhouse` | `server` (wire_dataframe.go:135) | `NewBundleStore` |
| `dataframe/published` | `api/dataframe/dataframe` (aggregates.go:45) | `AggregateBatchRequest`, `AggregateJob`, `AggregateJobResult`, `AggregateResponseDateHistogram`, `AggregateResponseHistogram`, `AggregateResponseLegacy`, `AggregateResponseMissing`, `AggregateResponseMode`, `AggregateResponseStats`, `AggregateResponseTerms`, `AggregateResult`, `AggregationResult`, `AggregationsResult`, `Column`, `DataframeSelector`, `ExcludeSelfFilters`, `Filter`, `Materialization`, `NormalizeAggregationResults`, `Page`, `PageRequest`, `Reader`, `Sort` |
| `dataframe/published` | `api/explorer` (openapi_explorer_selections.go:46) | `Filter` |
| `dataframe/published` | `dataframe/recipe/runtime` (recipe_combine_resolution.go:58) | `Column`, `Materialization`, `StateReady` |
| `dataframe/published` | `explorer` (artifact.go:38) | `ArtifactFormat`, `Filter`, `ValidateTypedFilters` |
| `dataframe/published` | `explorer/artifactfs` (store.go:377) | `ArtifactFormatCSV`, `ArtifactFormatJSONL` |
| `dataframe/published` | `explorer/constructioninputs` (catalog.go:461) | `Column`, `Materialization`, `Reader`, `StateReady` |
| `dataframe/published` | `explorer/lifecycle` (artifact.go:66) | `ArtifactColumn`, `ArtifactDescriptor`, `ArtifactFormatAuto`, `ArtifactFormatJSONL`, `ArtifactIdentity`, `ArtifactInterpretationIdentity`, `ArtifactRequest`, `ArtifactRowIdentity`, `ArtifactRowVisitor`, `Column`, `ExactExportRequest`, `ExactExportResult`, `ExactExportVisitor`, `Filter`, `Materialization`, `ResolveArtifactFormat`, `SourceResourceRef`, `StreamRequest`, `WriteArtifact` |
| `dataframe/published` | `server` (wire_dataframe.go:39) | `Reader`, `SelectionSourceAdapter` |
| `store/arango` | `catalog/arango` (relationships.go:213) | `AQLExecutor`, `BatchInserter`, `BootstrapSpec`, `CollectionSpec`, `QueryHasRow`, `RowQueryer` |
| `store/arango` | `dataframe/execution` (control_explain_types.go:59) | `ExplainAssessment`, `ExplainPlan`, `ExplainResult`, `IsQueryResourceLimitExceeded`, `PreviewCoveringIndex`, `WithQueryMemoryLimit` |
| `store/arango` | `dataframe/execution/arango` (explain_wiring.go:15) | `AssessExplainResult`, `Client`, `ExplainRequest`, `ExplainResult`, `IsQueryMemoryLimitExceeded`, `IsQueryOutOfMemory`, `IsQueryResourceLimitExceeded`, `IsQueryUserAssertion`, `RowQueryer`, `RowVisitor`, `WithQueryMaxNumberOfPlans` |
| `store/arango` | `dataframe/execution/chartifact/arango` (catalog.go:53) | `BootstrapSpec`, `CollectionSpec`, `QueryHasRow`, `RowQueryer` |
| `store/arango` | `dataframe/publication/arango` (bundle_registry.go:43) | `AQLExecutor`, `BootstrapSpec`, `CollectionSpec`, `QueryHasRow`, `RowBatchClient`, `RowQueryer`, `TransactionCollections`, `TransactionalRowClient` |
| `store/arango` | `dataframe/recipe/exec/arango` (registry.go:98) | `BootstrapSpec`, `CollectionSpec`, `DecodeValue`, `QueryLast`, `RowBatchClient` |
| `store/arango` | `dataset/arango` (collections.go:15) | `BootstrapSpec`, `Client`, `CollectionSpec`, `DecodeValue`, `RowQueryer` |
| `store/arango` | `explorer/arango` (activation.go:53) | `BootstrapSpec`, `Client`, `CollectionSpec`, `DecodeValue`, `RowQueryer`, `TransactionCollections`, `TransactionFunc` |
| `store/arango` | `ingest` (backend.go:15) | `BootstrapSpec`, `Client`, `CollectionSpec`, `ConnectionOptions`, `Open`, `PersistentIndexSpec`, `QueryHasRow`, `RowQueryer`, `TransactionCollections`, `TransactionalRowClient`, `WithRawInsertObserver` |
| `store/arango` | `server` (run.go:57) | `BootstrapSpec`, `Client`, `CollectionSpec`, `ConnectionOptions`, `Open` |
| `store/clickhouse` | `dataframe/execution/chartifact` (artifact.go:111) | `Column` |
| `store/clickhouse` | `dataframe/publication/clickhouse` (bundle_store.go:47) | `Column` |
| `store/clickhouse` | `server` (wire_dataframe.go:66) | `Client`, `New`, `Options` |
| `dataset` | `api/bulk/load` (errors.go:71) | `ErrGenerationNotReady`, `ErrManifestNotFound`, `Manifest`, `NewRef`, `Ref`, `State` |
| `dataset` | `api/dataframe/query` (active_generation.go:14) | `ActiveResolver`, `ErrNoActiveGeneration`, `ResolveActive` |
| `dataset` | `dataframe/publication` (bundle.go:17) | `DataframeSelector`, `ErrPublicationRecordMissing`, `PublicationVerification`, `PublicationVerifier` |
| `dataset` | `dataframe/publication/clickhouse` (bundle_store.go:907) | `ActiveReleaseResolver`, `ErrNoActiveRelease` |
| `dataset` | `dataframe/published` (current.go:76) | `ActiveReleaseResolver`, `ActiveResolver`, `DataframeSelector`, `ErrNoActiveGeneration`, `ErrNoActiveRelease`, `ProjectRelease`, `ReleasePublication`, `ResolveActive` |
| `dataset` | `dataset/arango` (document_codec.go:60) | `ActiveRelease`, `ActiveResolver`, `ErrGenerationNotReady`, `ErrInvalidTransition`, `ErrManifestNotFound`, `ErrNoActiveGeneration`, `ErrNoActiveRelease`, `ErrReleaseActivationConflict`, `Manifest`, `NewRef`, `ProjectRelease`, `Ref`, `ReleaseRepository`, `SchemaSnapshot`, `State`, `StateFailed`, `StateLoading`, `StateReady`, `StateStaged` |
| `dataset` | `explorer` (project_config.go:51) | `ActiveRelease`, `DataframeSelector`, `ProjectRelease` |
| `dataset` | `explorer/arango` (activation.go:60) | `ActiveRelease`, `ProjectRelease`, `Ref` |
| `dataset` | `explorer/capability/resolver` (explorer_capability.go:64) | `ActiveResolver`, `ResolveActive` |
| `dataset` | `explorer/lifecycle` (policy.go:20) | `DataframeSelector`, `ErrReleaseActivationConflict`, `ErrReleaseRequirementsUnmet`, `ProjectRelease`, `ReleaseRequirementsError` |
| `dataset` | `ingest` (generation_identity.go:27) | `ActivationRequest`, `DataframeSelector`, `ErrNoActiveGeneration`, `ErrNoActiveRelease`, `ErrReleaseActivationConflict`, `IdentityFormatCompact`, `IdentityFormatLegacy`, `Manifest`, `NewManifest`, `NewRef`, `NewSchemaSnapshot`, `Ref`, `ReleaseService`, `SchemaSnapshot`, `StateFailed`, `StateLoading`, `StateStaged` |
| `dataset` | `server` (config.go:42) | `ActiveResolver`, `DataframeSelector`, `NewReleaseRuntime`, `ReleaseRuntime`, `ReleaseService` |
| `dataset/arango` | `explorer/arango` (activation.go:55) | `ActiveDocumentKey`, `CompareAndSwapActivateRelease`, `CompareAndSwapPublishRelease`, `LifecycleCollection`, `ManifestDocumentKey` |
| `dataset/arango` | `ingest` (backend.go:109) | `ActiveDocumentKey`, `BootstrapSpec`, `CollectionSpecs`, `ErrManifestAlreadyExists`, `LifecycleCollection`, `New`, `Store` |
| `dataset/arango` | `server` (wire_storage.go:30) | `BootstrapSpec`, `New`, `Store` |
| `api/explorer` | `server` (wire_dataframe.go:50) | `Config`, `Handlers`, `NewHandlers`, `PreserveConfiguredQueryError` |
| `api/dataframe` | `api/dataframe/dataframe` (aggregates.go:18) | `DataframeAggregateInput`, `DataframeAggregationsInput`, `DataframeAggregationsResult`, `DataframeColumn`, `DataframeDatasetInput`, `DataframeFilterInput`, `DataframeMaterialization`, `DataframeMaterializationState`, `DataframePageInfo`, `DataframeRowConnection`, `DataframeRowsInput`, `DataframeSelector`, `DataframeSelectorInput`, `Diagnostics`, `FhirDataframeResult` |
| `api/dataframe` | `api/dataframe/query` (graph_service.go:119) | `FhirAggregateInput`, `FhirCatalogProjectionInput`, `FhirDataframeInput`, `FhirFieldPredicateInput`, `FhirFieldPredicateOperation`, `FhirFieldSelectInput`, `FhirFieldSelectorInput`, `FhirFilterInput`, `FhirFilterValueInput`, `FhirFilterValueKindBoolean`, `FhirFilterValueKindCode`, `FhirFilterValueKindDate`, `FhirFilterValueKindDateTime`, `FhirFilterValueKindDecimal`, `FhirFilterValueKindInteger`, `FhirFilterValueKindString`, `FhirPivotInput`, `FhirRepresentativeSliceInput`, `FhirTraversalMatchMode`, `FhirTraversalMatchModeRequired`, `FhirTraversalStepInput` |
| `api/dataframe/dataframe` | `server` (wire_dataframe.go:172) | `Config`, `Handlers`, `NewHandlers`, `NewRecipeExecutionAuthorizer`, `NewRecipeExecutionReader`, `NewService`, `Service` |
| `api/dataframe/query` | `api/dataframe/dataframe` (openapi_handlers.go:27) | `Service` |
| `api/dataframe/query` | `server` (wire_dataframe.go:171) | `Config`, `NewService`, `Service` |
| `api/bulk/load` | `server` (wire_http.go:44) | `Config`, `IngestRunner`, `NewHandler`, `NewService`, `NewStrictHandlers`, `Service`, `ServiceConfig`, `SourceLoadLimits`, `SourceLoader`, `StrictHandlers` |
| `api/http` | `api/bulk/load` (openapi_generations.go:17) | `MapServiceError`, `RawJSON` |
| `api/http` | `api/dataframe/dataframe` (aggregates.go:111) | `MapServiceError`, `RequestIDFromContext`, `UnexpectedResponseStatus` |
| `api/http` | `api/explorer` (openapi_explorer_authoring_direct.go:261) | `DecodeStrict`, `MapServiceError`, `RequestIDFromContext` |
| `api/http` | `dataframe/execution/arango` (query.go:76) | `LogServerDiagnostic`, `RequestIDFromContext` |
| `api/http` | `explorer/receipt` (explorer_receipt_contract.go:124) | `LogServerDiagnostic`, `RequestIDFromContext` |
| `api/http` | `server` (wire_http.go:62) | `HTTPConfig`, `HTTPServer`, `HealthHandlers`, `NewHTTPServer`, `NewHealthHandlers` |
| `api/recipe` | `api/dataframe/query` (recipe_resolve.go:15) | `NewFieldDiscovery` |
| `api/recipe` | `server` (wire_dataframe.go:114) | `RecipeSchemaResolver` |
| `api/authpolicy` | `api/dataframe/dataframe` (openapi_handlers.go:239) | `Classify`, `OperationConcealedLookup`, `OperationDataframeRead` |
| `api/authpolicy` | `api/dataframe/query` (errors.go:18) | `Classify`, `OperationQueryRead` |
| `api/authpolicy` | `api/explorer` (openapi_explorer_authoring_direct.go:63) | `Classify`, `OperationExplorerAuthorization` |
| `api/authpolicy` | `dataframe/recipe/runtime` (recipe_authorization.go:65) | `Classify`, `OperationRecipeAuthorization` |
| `api/columncapabilities` | `api/dataframe/dataframe` (model.go:56) | `Resolve` |
| `api/columncapabilities` | `explorer/constructioninputs` (catalog.go:595) | `Resolve` |
| `fhir/schema` | `api/bulk/load` (source_load.go:69) | `ConcreteResourceType` |
| `fhir/schema` | `api/dataframe/query` (fieldrefs.go:60) | `FieldSelectorSpec`, `LookupField`, `ParseSelector`, `PredicateContains`, `SelectorFromField`, `SelectorStep` |
| `fhir/schema` | `catalog` (relationships.go:33) | `ConcreteResourceType`, `DefaultPivotSpec`, `DefinitionName`, `ElementListView`, `FieldSelectorSpec`, `GeneratedIndex`, `Index`, `JSONType`, `JSONTypeArray`, `PivotFamilyObservationCodeValue`, `PrimitiveUnknown`, `ResolveTerminalScalarMetadata`, `ResourceType`, `ResourceTypes`, `SelectorExpression` |
| `fhir/schema` | `catalog/arango` (evidence.go:29) | `ConcreteResourceType`, `LookupTraversal`, `ResolveCompilerTraversal`, `ResourceTypes` |
| `fhir/schema` | `dataframe/compiler/capability` (probe.go:154) | `ConcreteResourceType`, `CorrelatedBinding`, `CorrelatedKey`, `FieldKind`, `FieldKindUnknown`, `FieldsForResource`, `LookupTraversal`, `PrimitiveBoolean`, `PrimitiveDate`, `PrimitiveDateTime`, `PrimitiveDecimal`, `PrimitiveInteger`, `PrimitiveKind`, `PrimitiveString`, `PrimitiveUnknown`, `ResolveCompilerTraversal`, `ResolveFieldSemantics`, `ResolveTerminalScalarMetadata`, `TraversalDirection`, `TraversalMultiplicity`, `ValidateCorrelatedBinding` |
| `fhir/schema` | `dataframe/compiler/ir` (physical_validation_expr.go:102) | `CorrelatedSelectionScope`, `CorrelatedSelectionScopeExact`, `CorrelatedSelectionScopeSystemVersion`, `CorrelatedValueScope`, `CorrelatedValueScopeCoding`, `CorrelatedValueScopeOwner`, `DefinitionExists`, `FieldKindArray`, `FieldKindObject`, `HasResource`, `PrimitiveBoolean`, `PrimitiveDate`, `PrimitiveDateTime`, `PrimitiveDecimal`, `PrimitiveInteger`, `PrimitiveString`, `ResolveFieldSemantics`, `ResolveTerminalScalarMetadata` |
| `fhir/schema` | `dataframe/compiler/lower` (correlated_binding.go:17) | `ChoiceValueSelectorOptions`, `CompilerTraversal`, `CorrelatedBinding`, `CorrelatedKey`, `CorrelatedSelectionScopeSystemVersion`, `CorrelatedValueScopeCoding`, `DefinitionName`, `ExtensionBinding`, `FieldKindArray`, `GeneratedIndex`, `HasResource`, `LookupTraversal`, `PrimitiveBoolean`, `PrimitiveDate`, `PrimitiveDateTime`, `PrimitiveDecimal`, `PrimitiveInteger`, `PrimitiveKind`, `PrimitiveString`, `ResolveCompilerTraversal`, `ResolveFieldSemantics`, `ResolveTerminalScalarMetadata`, `RowCardinalityMany`, `RowCardinalityOne`, `RowPathArray`, `RowPathScalar`, `SelectorExpression`, `TerminalScalarMetadata`, `TraversalOutbound`, `TraversalSpec`, `ValidateCorrelatedBinding`, `ValidateCorrelatedKey`, `ValidateExtensionBinding` |
| `fhir/schema` | `dataframe/compiler/render/aql` (collections.go:284) | `CorrelatedSelectionScopeSystemVersion`, `CorrelatedValueScopeCoding` |
| `fhir/schema` | `dataframe/recipe` (document_types.go:226) | `CorrelatedBinding`, `CorrelatedKey`, `CorrelatedSelectionScopeSystemVersion`, `ExtensionBinding`, `ValidateCorrelatedKey` |
| `fhir/schema` | `dataframe/recipe/schema` (discovery.go:112) | `CanonicalizePath`, `DefaultPivotSpec`, `FieldSelectorSpec`, `FieldSelectorSpecFromPath`, `PivotSpec`, `PrimitiveUnknown`, `ResolvePath`, `ResolveTerminalScalarMetadata`, `SelectorExpression` |
| `fhir/schema` | `dataframe/semantic` (recipe_filter.go:21) | `CorrelatedBinding`, `CorrelatedKey`, `CorrelatedSelectionScopeSystemVersion`, `DefinitionExists`, `ExtensionBinding`, `FieldKindArray`, `FieldKindObject`, `FieldPredicateSpec`, `FieldSelectorSpec`, `HasResource`, `LookupTraversal`, `PredicateContains`, `PrimitiveBoolean`, `PrimitiveDate`, `PrimitiveDateTime`, `PrimitiveDecimal`, `PrimitiveInteger`, `PrimitiveKind`, `PrimitiveString`, `PrimitiveUnknown`, `ResolveFieldSemantics`, `ResolvePath`, `ResolveTerminalScalarMetadata`, `SelectorExpression`, `ValidateCorrelatedBinding`, `ValidateCorrelatedKey`, `ValidateExtensionBinding`, `ValidatePivotSelectors` |
| `fhir/schema` | `dataframe/spec` (filter.go:112) | `CanonicalResourceType`, `ConcreteResourceType`, `ContainsFilter`, `CorrelatedBinding`, `CorrelatedKey`, `CorrelatedSelectionScopeSystemVersion`, `FieldKindArray`, `ParseSelector`, `PrimitiveBoolean`, `PrimitiveDate`, `PrimitiveDateTime`, `PrimitiveDecimal`, `PrimitiveInteger`, `PrimitiveKind`, `PrimitiveString`, `ResolveFieldSemantics`, `ResolveTerminalScalarMetadata`, `Selector`, `SelectorStep`, `ValidateCorrelatedBinding`, `ValidateCorrelatedKey` |
| `fhir/schema` | `explorer/authoringv2` (commands.go:1888) | `CanonicalizePath`, `CorrelatedBinding`, `CorrelatedKey`, `CorrelatedSelectionScopeSystemVersion`, `CorrelatedValueScopeCoding`, `ExtensionBinding`, `IdentifierBinding`, `LookupField`, `PrimitiveDateTime`, `PrimitiveString`, `PrimitiveUnknown`, `QuantityIdentityPaths`, `ResolveTerminalScalarMetadata`, `ValidateCorrelatedBinding`, `ValidateCorrelatedKey`, `ValidateExtensionBinding`, `ValidateIdentifierBinding` |
| `fhir/schema` | `explorer/capability/resolver` (explorer_capability.go:402) | `PrimitiveUnknown`, `ResolveTerminalScalarMetadata` |
| `fhir/schema` | `explorer/compilation` (row_expansion.go:99) | `CorrelatedSelectionScopeSystemVersion`, `DefinitionName`, `GeneratedIndex`, `PrimitiveBoolean`, `PrimitiveDate`, `PrimitiveDateTime`, `PrimitiveDecimal`, `PrimitiveInteger`, `PrimitiveUnknown`, `QuantityIdentityPaths`, `ResolveTerminalScalarMetadata`, `RowCardinalityMany`, `RowCardinalityOne`, `RowPathArray`, `RowPathScalar`, `ValidateIdentifierBinding` |
| `fhir/schema` | `explorer/lifecycle` (construction_group_field_choices.go:50) | `ConcreteResourceType`, `DefinitionName`, `GeneratedIndex`, `Index`, `PrimitiveBoolean`, `PrimitiveDate`, `PrimitiveDateTime`, `PrimitiveDecimal`, `PrimitiveInteger`, `PrimitiveKind`, `PrimitiveString`, `PrimitiveUnknown`, `ResolveTerminalScalarMetadata`, `RowCardinalityMany`, `RowCardinalityOne`, `RowPathArray`, `RowPathFacts`, `RowPathScalar` |
| `fhir/schema` | `explorer/recipeimport` (source_rule_validation.go:41) | `CanonicalizePath`, `CorrelatedBinding`, `CorrelatedValueScopeCoding`, `IdentifierBinding`, `ResolvePath`, `ValidateCorrelatedBinding`, `ValidateExtensionOwnerPath`, `ValidateIdentifierBinding` |
| `fhir/schema` | `fhir/semantic` (member_walk.go:205) | `DefinitionName`, `Element`, `ElementListView`, `ElementView`, `Index`, `JSONTypeArray`, `JSONTypeObject`, `ResourceType`, `ResourceTypes` |
| `fhir/schema` | `ingest` (documents.go:35) | `ConcreteResourceType`, `ResourceTypes` |
| `fhir/schema` | `server` (wire_explorer.go:96) | `GeneratedIndex` |
| `fhir/semantic` | `catalog` (semantic_coverage.go:403) | `MemberFact`, `ResourceBoundary`, `WalkDiagnostic`, `WalkDiagnosticCode`, `WalkLimitError`, `WalkMemberValues`, `WalkMembersWithResourceTypes`, `WalkMembersWithResourceTypesMaxFacts`, `WalkUnknownMember`, `WalkUnresolvedResourceType` |
| `authscope` | `api/authpolicy` (policy.go:36) | `ErrAuthorizationBackendUnavailable`, `ErrForbidden`, `ErrUnauthenticated` |
| `authscope` | `api/bulk/load` (handler.go:13) | `Authorizer`, `NormalizeAuthResourcePath`, `Principal`, `PrincipalFromContext` |
| `authscope` | `api/dataframe/dataframe` (openapi_handlers.go:271) | `AuthorizeProject`, `ErrForbidden`, `ErrUnauthenticated`, `NormalizeAuthResourcePaths`, `Principal`, `PrincipalFromContext`, `ReadScope`, `ReadScopeRestricted`, `ScopeResolver` |
| `authscope` | `api/dataframe/query` (input_resolution.go:24) | `AuthorizeProject`, `NormalizeAuthResourcePaths`, `Principal`, `PrincipalFromContext`, `ReadScope`, `ReadScopeRestricted`, `ReadScopeUnrestricted`, `ScopeResolver` |
| `authscope` | `api/explorer` (explorer_config_route.go:12) | `Authorizer`, `NormalizeAuthResourcePath`, `Principal`, `PrincipalFromContext`, `SubjectFromContext` |
| `authscope` | `api/http` (errors.go:39) | `Authenticator`, `Authorizer`, `BearerTokenAuthenticator`, `ContextWithPrincipal`, `ErrAuthorizationBackendUnavailable`, `ErrForbidden`, `ErrUnauthenticated` |
| `authscope` | `api/recipe` (discovery.go:23) | `ErrForbidden`, `ErrUnauthenticated`, `ReadScopeMode`, `ReadScopeRestricted`, `ReadScopeUnrestricted` |
| `authscope` | `dataframe/compiler/capability` (probe.go:24) | `ReadScopeMode` |
| `authscope` | `dataframe/compiler/lower` (auth_scope.go:10) | `ReadScopeMode`, `ReadScopeRestricted`, `ReadScopeUnrestricted` |
| `authscope` | `dataframe/execution` (clickhouse_artifact.go:72) | `ReadScopeMode`, `ReadScopeRestricted`, `ReadScopeUnrestricted` |
| `authscope` | `dataframe/execution/chartifact` (artifact.go:432) | `ReadScopeMode`, `ReadScopeRestricted`, `ReadScopeUnrestricted` |
| `authscope` | `dataframe/recipe` (document_types.go:594) | `ReadScopeMode` |
| `authscope` | `dataframe/recipe/runtime` (recipe_authorization.go:27) | `PrincipalFromContext`, `ReadScope`, `ReadScopeMode`, `ReadScopeRestricted`, `ReadScopeUnrestricted`, `ScopeResolver` |
| `authscope` | `dataframe/semantic` (execution_context.go:9) | `ReadScopeMode` |
| `authscope` | `explorer/arango` (selection_references.go:16) | `ReadScope` |
| `authscope` | `explorer/capability/resolver` (explorer_capability.go:59) | `AuthorizeProject`, `Principal`, `PrincipalFromContext`, `ReadScope`, `ReadScopeRestricted`, `ReadScopeUnrestricted`, `ScopeResolver` |
| `authscope` | `explorer/constructioninputs` (catalog.go:170) | `ErrAuthorizationBackendUnavailable`, `ErrForbidden`, `ErrUnauthenticated`, `PrincipalFromContext`, `ReadScope`, `ReadScopeRestricted`, `ScopeResolver` |
| `authscope` | `explorer/lifecycle` (artifact.go:656) | `ErrForbidden`, `ErrUnauthenticated`, `ReadScope`, `ReadScopeRestricted`, `ReadScopeUnrestricted` |
| `authscope` | `explorer/receipt` (explorer_combine_output_metadata.go:26) | `ScopeResolver` |
| `authscope` | `ingest` (source_publication_adapter.go:53) | `PrincipalFromContext`, `ScopeResolver` |
| `authscope` | `server` (config.go:333) | `AllowAllAuthorizer`, `Authenticator`, `Authorizer`, `BasicAuthenticator`, `CalyprAuthenticator`, `NewFenceUserAccessClientWithTrustedIssuer`, `NewScopeResolver`, `Principal`, `ScopeAuthorizer`, `ScopeResolver`, `ScopeResolverConfig`, `StaticAuthenticator`, `ValidateTrustedFenceIssuer` |

</details>
