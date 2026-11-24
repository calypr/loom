# Filename boundary target

Status: **Stage 3 approved; Stage 4 in progress.** Base:
`internal-cleanup` at `cb7497a`; tooling commits `85b9112`, `04265eb`, `5ba8a37`, `ba4193a`.
Operator approved the package map with FHIR types kept in `api/dataframe` and the explicit-group
model kept in `selection`.

Source of truth: `docs/FILENAME_BOUNDARY_PLAN.md`. Mechanical evidence:
`.cache/filenames/ledger.tsv`, `.cache/filenames/ledger-verdicts.tsv`,
`.cache/filenames/graph-ledger.tsv`, `scripts/filenames/`.

## Method

- 24 Stage 2 slices covering every underscore production file.
- Two independent reviews per large/mixed slice; findings were rerun by the coordinator.
- No source file was edited in Stages 0-3.
- `graphcheck` was run with the two non-ledger co-move sets and the one duplicate-binding remap:
  - `lower/helpers.go`: `sanitizeColumnName`, `selectorExecutionMode`,
    `selectorExecutionModeForExpression`, `selectorExecutionModeWithContext`, `selectorModeContext`.
  - `catalog/fieldprofile`: moved code binds to the existing `semantic.stringValue`.

## Counts

| Measure | Before | After |
| --- | ---: | ---: |
| Internal packages | 119 | **117** |
| Production files with underscore stem | 540 compiled (+1 build-ignore generator) | **0** |
| Declarations in underscore files | 4,641 | same declarations, relocated |
| Source files split | — | **29** (50 destination files; 4 also retain receiver-bound methods) |
| Average production files per package | 7.13 (849/119) | pending execution |
| New packages | — | **3** |
| Alias-only packages removed | 5 | 0 |
| Production files | 849 | not claimed until execution |

Verdict rows: **4,641**, including the **660** deletion/caller-update rows.
Outcomes: 3,310 rename, 855 merge, 340 split, 136 move.
The 660 rows carry `dest_file=alias`; they are part of the 4,641 total but are not declaration moves.

The 540 compiled files exclude `internal/dataframe/compiler/ir/generate_children.go`
(`//go:build ignore`). Stage 5 lint must exempt build-ignore tooling.

## New packages

| Package | Rows | Evidence |
| --- | ---: | --- |
| `internal/dataframe/compiler/celltrace` | 27 | `files=3 self=10 out=4 in=0`; 923 lines; two parent helper relocations. |
| `internal/dataframe/compiler/lower/physical` | 15 | `files=5 self=5 out=2 in=33`; co-moves `traversal.go` and two helper groups; then one-way. |
| `internal/explorer/authoring` | 5 | `files=2 self=2 out=0 in=0`; authoring error/wire mapping only. |

Existing alias packages that become real destinations (not counted as new):
`internal/dataframe/compiler/ir/scope`, `internal/dataframe/compiler/model`,
`internal/dataframe/compiler/population`, `internal/dataframe/compiler/rowlineage`.
`internal/explorer/reconcile` already exists and receives `artifactfs`' artifact reconciler.

Removed alias-only packages: `internal/dataframe/compiler/lineage`,
`internal/dataframe/compiler/categoryscan`, `internal/dataframe/compiler/preview`,
`internal/dataframe/compiler/ir/physical`, `internal/dataframe/compiler/ir/validate`.
All have zero production and test importers today.

## Graph

`graphcheck .cache/filenames/graph-ledger.tsv` reports **98 packages, 234 edges, 0 cycles,
0 layer violations, 0 forbidden edges**.

## Receiver-bound list

| Receiver | Methods / target | Decision |
| --- | --- | --- |
| `PhysicalPlan` | `Validate`, `ValidateForWorkspaceCompilation` -> `ir/validate` | Stays with receiver; package move blocked by cycle. |
| `PhysicalOperation` | `validatePayload` -> `ir/validate` | Stays with receiver. |
| `PhysicalCategoryScanReturn` | `Validate*` -> `ir/validate` | Stays with receiver. |
| `PhysicalClickHouseCombine` | `Validate*` -> `ir/validate` | Stays with receiver. |
| `PhysicalClickHousePrefix` | `Validate*` -> `ir/validate` | Stays with receiver. |
| `Profiler` | `observeSchemaSemantics`, `observeSchemaSemanticsWithEvidence`, `recordSchemaSemanticObservation` -> `semantic` | Stays with receiver. |
| `Revision` | `AuthoringWorkspaceBytes` -> `explorer/workspace` | Stays with receiver. |
| `Service` | `RevisionWorkspace`, `DraftRevisionWorkspace`, `artifactWorkspace` -> `explorer/workspace` | Stays with receiver. |
| `rowLineageRenderer` | deferred `render/aql/lineage` | Advisory; no move in this pass. |
| `tableShapeRenderer` | deferred `render/aql/reshape` | Advisory; no move in this pass. |

## Judgment calls for the operator

1. **Alias shims are deletion work.** 660 rows carry `dest_file=alias`: 621 in `*_aliases.go`,
   plus forwarding files such as `lifecycle/support_compat.go`, `arango/receipt_wrappers.go`, and
   `arango/workspace_audit_aliases.go`. Stage 4 must delete the shim and update callers; it must
   not materialize these rows.
2. **Duplicate construction code stays for now.** `construction_api.go`, `construction_shared.go`,
   `capability_failure.go`, `population_report.go`, `source_lane_delegates.go`, and
   `construction/support_compat.go` duplicate existing packages. They are renamed/split in place;
   deletion is a `docs/REPO_CLEANUP_GOAL.md` follow-up. `lifecycle/support_compat.go` is only a
   forwarding shim and is deleted with caller updates.
3. **`explorer/project_config.go` stays in `explorer`.** Its real publication DTOs reference
   `explorer.Revision`; moving them to `explorer/config` creates `explorer -> config -> explorer`.
   The aliases/delegates are already owned by `config`.
4. **`lower/physical` needs non-ledger co-moves.** `traversal.go` and the two helper groups in
   `helpers.go` must move with the 15 ledger rows; otherwise the package is not one-way.
5. **`celltrace` vs `lineage`.** `celltrace` is proposed NEW; the existing `lineage` package is a
   duplicate row-lineage shim with zero importers and is removed. The operator may instead fold
   cell trace into a renamed `lineage`.
6. **Explicit-group model stays in `selection` (operator decision).** The model and repository
   move to `selection/revision.go` and `selection/repository.go`; no `selection/group` child is
   created.
7. **FHIR query types stay in `api/dataframe` (operator decision).** `fhir_query.go` becomes
   `fhir.go`; no `api/dataframe/fhir` child is created.
8. **`explorer/authoring` is a new name beside `authoringv2`/`semanticauthoring`.** It contains
   only error/wire mapping; the alternative is to keep those declarations in `explorer`.
9. **`render/aql/category` remains rejected.** It is one-way by references and clears the size bar,
   but moving it requires exporting about ten renderer internals; `TARGET_TREE.md` keeps AQL
   sub-renderers deferred. Revisit as an explicit second-wave design change.
10. **Destination collisions were resolved by more specific stems.** `publication/output_schema.go`
    -> `outputs`, `receipt_preview_diagnostic.go` -> `preview`, `publication/arango/bundle_registry.go`
    -> merge into `registry.go`, `projectconfig/view_builder_prepare.go` -> `snapshot`,
    `arango/selection_references.go` Store -> merge into `store.go`, `api/dataframe/query_diagnostics.go`
    -> `query`, and `testkit/explorerfixtures/selection_store.go` -> `selection`.
    `api/dataframe/query/active_generation.go` merges its `Service` method into `service.go`.
    `api/bulk/load` uses single-word stem `source`, not `sourceload`; its operations file merges
    into the same stem. `store/arango` merges only `Client.Explain` into `explain.go`.
12. **`fieldprofile` metadata predicate moves, not copies.** `TARGET_TREE.md` says “copy
    `isLoomMetadataField`”; this proposal exports/moves it to `semantic` because the plan forbids
    duplication. `fieldprofile` already imports `semantic`.
13. **`lifecycle/support` keeps its existing name.** It predates this pass and is named in
    `TARGET_TREE.md`; no new catch-all package is proposed. Renaming it is a separate migration.
14. **Tests are not bulk-renamed.** 706 test stems still contain underscores; tests follow moved
    declarations only. Stage 5 lint is production-only.

## Resulting package responsibilities

Groups below list every post-move package. Packages in bold are new or become real.

| Packages | Responsibility |
| --- | --- |
| `internal/acceptance` | Acceptance harness. |
| `internal/api/bulk/load` | Generation/source-load HTTP adapters. |
| `internal/api/dataframe`, `internal/api/dataframe/dataframe`, `internal/api/dataframe/query`, `internal/api/dataframe/recipeexec` | Dataframe HTTP, FHIR query DTOs, read/query, and recipe-execution adapters. |
| `internal/api/explorer` | Generated explorer HTTP adapters. |
| `internal/api/http` | HTTP shell, middleware, errors. |
| `internal/authscope` | Principal, scopes, resource paths. |
| `internal/catalog`, `internal/catalog/arango`, `internal/catalog/discoverycache`, `internal/catalog/fieldcatalog`, `internal/catalog/fieldprofile` | Catalog model, adapters, discovery cache, field DTOs, profiler. |
| `internal/dataframe/artifactformat`, `internal/dataframe/columntransform`, `internal/dataframe/expression`, `internal/dataframe/spec`, `internal/dataframe/unit` | Dataframe leaf contracts and transformations. |
| `internal/dataframe/compiler` | Compiler facade/entry points. |
| `internal/dataframe/compiler/capability`, `internal/dataframe/compiler/facade`, `internal/dataframe/compiler/optimize` | Capability probes, consumer facade, plan optimization. |
| `internal/dataframe/compiler/ir`, **`internal/dataframe/compiler/ir/scope`** | Physical plan model/validation and scope evidence. |
| `internal/dataframe/compiler/lower`, **`internal/dataframe/compiler/lower/physical`** | Lowering and physical traversal/route/predicate primitives. |
| **`internal/dataframe/compiler/celltrace`**, **`internal/dataframe/compiler/model`**, `internal/dataframe/compiler/population`, `internal/dataframe/compiler/rowlineage` | Cell trace, compiled query model, population mapping, row-lineage capability/compile. |
| `internal/dataframe/compiler/render/aql`, `internal/dataframe/compiler/render/aql/internal/aqlsyntax`, `internal/dataframe/compiler/render/clickhouse` | AQL/ClickHouse rendering and syntax helpers. |
| `internal/dataframe/errors`, `internal/dataframe/errors/authpolicy` | Dataframe errors and auth classification. |
| `internal/dataframe/execution`, `internal/dataframe/execution/arango`, `internal/dataframe/execution/chartifact`, `internal/dataframe/execution/chartifact/arango` | Execution engine and adapters. |
| `internal/dataframe/fielddiscovery`, `internal/dataframe/lineage` | Discovery adapter and stable lineage identity. |
| `internal/dataframe/publication`, `internal/dataframe/publication/arango`, `internal/dataframe/publication/clickhouse`, `internal/dataframe/published` | Publication transaction/store and published reads. |
| `internal/dataframe/recipe`, `internal/dataframe/recipe/arango`, `internal/dataframe/recipe/runtime`, `internal/dataframe/recipe/schema` | Recipe model/runtime/registry/schema. |
| `internal/dataframe/recipeauth`, `internal/dataframe/recipeplan` | Recipe authorization and recipe-to-plan lowering. |
| `internal/dataset`, `internal/dataset/arango` | Dataset manifest/release model and adapter. |
| `internal/digest`, `internal/projectid` | Digest and project identity helpers. |
| `internal/explorer` | Explorer aggregate, core DTOs, service ports. |
| `internal/explorer/arango`, `internal/explorer/arango/capabilitydb`, `internal/explorer/arango/db`, `internal/explorer/arango/publicationdb`, `internal/explorer/arango/selectiondb`, `internal/explorer/arango/workspacedb` | Explorer Arango composition and per-aggregate stores. |
| **`internal/explorer/authoring`**, `internal/explorer/authoringv2`, `internal/explorer/semanticauthoring` | Authoring wire errors, command application, semantic authoring. |
| `internal/explorer/artifactfs`, `internal/explorer/reconcile` | Artifact storage and ticker reconciler. |
| `internal/explorer/capability`, `internal/explorer/capability/resolver` | Capability evidence and resolution. |
| `internal/explorer/compilation`, `internal/explorer/config`, `internal/explorer/construction`, `internal/explorer/constructioninputs`, `internal/explorer/explicitgroup` | Workspace compilation, config, construction, construction inputs, explicit-group authoring. |
| `internal/explorer/importedsource`, `internal/explorer/interpretation`, `internal/explorer/lifecycle`, `internal/explorer/lifecycle/ports`, `internal/explorer/lifecycle/support` | Imported-source runtime, interpretation, residual lifecycle orchestration and consumer ports. |
| `internal/explorer/population`, `internal/explorer/projectconfig`, `internal/explorer/proposal`, `internal/explorer/receipt`, `internal/explorer/receiptmodel` | Population, project config/view builder, proposals, receipt execution/model. |
| `internal/explorer/recipeimport`, `internal/explorer/rowdef`, `internal/explorer/rowsource` | Import translation, row definition, source lanes. |
| `internal/explorer/selection`, `internal/explorer/semanticbrowse`, `internal/explorer/viewer`, `internal/explorer/workspace` | Selection, explicit-group model, semantic browse, viewer DTOs, authoring workspace model/codec/migration. |
| `internal/fhir/binding`, `internal/fhir/schema` | FHIR binding helpers and generated schema. |
| `internal/foundation/httpctx` | Request context and diagnostics. |
| `internal/ingest/bootstrap`, `internal/ingest/event`, `internal/ingest/generation`, `internal/ingest/identity`, `internal/ingest/row`, `internal/ingest/source`, `internal/ingest/sourceload` | Ingest bootstrap/events/orchestration/identity/row/source/leased loading. |
| `internal/semantic`, `internal/semantic/arango`, `internal/semantic/backfill` | Semantic model/coverage, persistence, backfill. |
| `internal/server`, `internal/server/sourcepublication` | Composition root and source-publication adapter. |
| `internal/store/arango`, `internal/store/clickhouse` | Shared store clients and query ports. |
| `internal/testkit`, `internal/testkit/arangotest`, `internal/testkit/capabilityfixtures`, `internal/testkit/constructionfixtures`, `internal/testkit/explorerfixtures`, `internal/testkit/httpfixtures`, `internal/testkit/recipefixtures`, `internal/testkit/recipetest` | Test support. |

## Verification and limits

- Stage 0: `go test ./scripts/filenames`, `go vet`, `gofmt` clean; baseline prefix groups **40/40**;
  `moveverify HEAD`: **0 differences** (9,092 declarations).
- Stage 1: **4,641 declarations**, **540 compiled underscore files**, 25 source files with more than
  one destination.
- Stage 3: full `graphcheck` passes with the co-move ledger; no source edits.
- Reviews: 9 independent batches in `.cache/filenames/reviews/`, covering all 24 slices. Confirmed
  blockers were folded into `corrections.tsv`; unresolved notes are in `.cache/filenames/followups.md`.
- Duplicate `unit_id` rows were suffixed by `merge_verdicts.py`; no declaration was dropped.
- Review-confirmed execution constraints:
  - `apply` skips `.gen.go` ranges; generated methods stay and are regenerated later.
  - `ir/scope`, `model`, `population`, `rowlineage`, and the alias-only packages must have their
    shim bodies deleted in the same wave.
  - Export `WithGenericPhysicalExecutionWindow` or move it below the three compiler importers.
  - Fieldprofile tests need an explicit split; `decodeSemanticValue` needs export or test move.
  - `compilation_receipt_test.go` uses unexported receipt constants; move/export them with the
    `receiptmodel` alias cleanup.
  - `source_plan_legacy.go` has no row; delete it explicitly when the `ImportedSourcePlan` method
    lands in `plan.go`.
  - The `authscope` move requires caller updates in `internal/semantic/arango/*_test.go`,
    `internal/dataframe/fielddiscovery/*_test.go`, `api/dataframe/query/introspection.go`, and
    `api/dataframe/query/input_resolution.go`.
  - `explain_client.go` contributes only `Client.Explain` to `explain.go`; the other `Client`
    methods stay in their current files.
- Not run in Stage 3: `apply`, `go build`, `go test`, integration, E2E. Those belong to the
  approval-gated execution stage.
