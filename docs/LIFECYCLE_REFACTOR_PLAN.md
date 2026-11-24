# `explorer/lifecycle` refactor plan

Status: in progress. Proposed 2026-11-23. Measured at `35e7c64f3` on `internal-cleanup`.
Relation to other plans: this replaces the `explorer/lifecycle` row in `TARGET_TREE.md`
("residual orchestration, publication, artifacts, network, shared support") and is one lane of
`docs/GARDENING_PLAN.md` Phase 3D.

## Problem

`lifecycle` is a half-dismantled god object. Earlier work moved the logic into sibling packages
(`construction`, `rowdef`, `rowsource`, `population`, `proposal`, `selection`). `lifecycle` still
sits in the middle of them, holding forwarding shims, the shared operations those siblings need,
and one large cluster that belongs somewhere else.

| Measure | Value |
| --- | --- |
| Source | 52 files, 8.9k lines (`ports/` and `support/` add 541) |
| Tests | 54 files, 29.2k lines, 390 tests (3.3× source) |
| `Service` | 122 methods; `api/explorer` holds a concrete `*lifecycle.Service` and calls 42 of them |
| `Config` | 44 fields mixing ports, callbacks, limits and cache settings |
| Unused functions (grep heuristic) | 29 never referenced; 15 referenced only by tests |
| Tests that change `service.config.X` after `New` | 99 assignments |

### Findings

1. **Dead forwarding code.** These have no callers outside their own definition. Most are in
   `delegates.go`, `service.go` and `shared.go`: `appendStageColumnIfAbsent`,
   `applicableRowChoiceKinds`, `capabilityRowChoiceKind`, `constructionFieldDisplayLabel`,
   `constructionReceiptStage`, `constructionRootProjectionCanReachStage`,
   `constructionStepInputStageID`, `deriveSourceLogicalTypeIsNumeric`, `fhirIdentifierPath`,
   `importedSourceChoiceUnavailable`, `lifecycleRowChoiceKind`, `preserveConstructionOwnedInputs`,
   `reauthorizeConstruction{Derive,Filter,Unpivot}Sources`, `resolveConstructionExpandSource`,
   `resolveRowChoiceOccurrence`, `rowChoiceOccurrenceSummary`, `rowChoicePolicies`,
   `rowChoiceRouteSummary`, `rowSourceCandidateSkipClass`, `rowSourceChoiceIsStaleVersion`,
   `rowSourceInventoryFailure`, `rowSourceEnumeration.skip`, `uniqueRowSourceCandidate`,
   `unpivotMeltKindsSatisfiable`, `unpivotSourceLogicalTypeIs{MeltEligible,TableScalar}`,
   `validateResolvedRowChoice`. `service.go` also has three empty `const ()` blocks.
   Staticcheck in this environment was built with Go 1.24 and cannot load the module, so the list
   comes from a grep script. Confirm each entry with a compile before deleting it.
2. **Test-only shims.** These exist only so tests can reach sibling behaviour through `lifecycle`:
   `constructionCandidateWith{Filter,Derive,Expand,Unpivot}Sources`,
   `construction{Filter,Derive,Expand,Unpivot}SourceCapability`,
   `resolveConstruction{Filter,Derive,Unpivot}Source`, `reauthorizeConstructionExpandSources`,
   `semanticFrameFamilyMatchesPinnedPrior`, `constructionRootFieldAlreadyProjected`,
   `constructionGroupSourceCandidate`. `delegates_test.go` (1,471 lines) and much of
   `shared_test.go` test `rowsource`/`construction` behaviour this way.
3. **Siblings depend on `lifecycle` through callbacks.** `construction.Config`, `rowdef.Config`,
   `population.Config` and `proposal.Config` receive `Compile`, `GetOwner`, `CurrentWorkspace`,
   `LookupReceipt`, `ValidateExecutableReceiptRoute`, `VerifyProposalReceipt`, `Preview`,
   `Catalog` and `ReauthorizeFrameSourceChoice` as functions passed in from `lifecycle`. That is
   an import cycle hidden by passing functions around.
4. **Services rebuilt on every call.** `syncConstructionService()` resets the construction
   service's config on every call (13 sites). `syncRowDefService()` rebuilds the rowdef service on
   every call (7 sites). `rowSourceService()` builds a new `rowsource.Service` at each of 17 call
   sites. This exists so that tests changing `service.config` after `New` still take effect.
5. **Field browse is in the wrong package.** About 3,050 lines (`inventory.go`,
   `presentation.go`, `frame.go`, `schema.go`, `column.go`, `browse.go`) implement semantic
   inventory, field presentation and frame-source browsing. Their request and response types are
   in `explorer/semanticbrowse`, which is a 141-line package. Most of this code already runs on a
   separate `fieldBrowse` struct, with five dependencies: capability, semantic coverage, selection
   resolution, `getOwner` and `currentWorkspace`.
6. **`lifecycle/ports` and `lifecycle/support` are shared code under `lifecycle`'s name.**
   `construction`, `rowdef`, `rowsource`, `population` and `proposal` all import them.

## Target design

If this were written from scratch, the dependency graph would point one way. The shared
operations sit at the bottom, the use-case packages import them directly, and one thin
composition layer sits on top.

```
api/explorer ──► lifecycle (authoring use cases + wiring)
                    │
     ┌──────────────┼───────────────┬────────────────┬──────────────┐
     ▼              ▼               ▼                ▼              ▼
construction     rowdef         population        proposal     semanticbrowse   publication
     │              │               │                │              │              │
     └──────────────┴───────┬───────┴────────────────┴──────────────┴──────────────┘
                            ▼
                     authoringcore  (owner/workspace load, compile+verify, receipt lookup,
                            │        route validation, preview, catalog)
                            ▼
          explorer store, compilation, capability, receiptmodel, workspace
```

| Package | Owns | Sources |
| --- | --- | --- |
| `explorer/authoringcore` | `getOwner`, `currentWorkspace`, `resolveCompilationCapability`, `compile`/`compileAuthorized`/`validateCompiledReceipt` plus binding clone/compare helpers, `lookupReceipt`, `validateExecutableReceiptRoute`, `verifyProposalReceipt`, `catalog`, `Preview`, interpretation/population input resolution | `authoring.go` (≈ lines 392–560, 680–710), `policy.go`, `proof.go`, `dataframe.go`, `preview.go`, `interpretation.go`, `population.go`, `construction/search.go` `currentWorkspace` |
| `explorer/semanticbrowse` | Semantic inventory browse, field presentation, schema field search, column source, frame-source browse and `reauthorizeFrameSourceChoice` | `inventory.go`, `presentation.go`, `frame.go`, `schema.go`, `column.go`, `browse.go` |
| `explorer/publication` | Publish, repository publish, artifact prepare/open/download, artifact descriptors, `DelayArtifactRows` | `publish.go`, `repository.go`, `artifact.go`, `download.go`, `prepare.go`, `descriptor.go`, `limits.go` |
| `explorer/lifecycle` | Authoring command application (`ApplyCommands*`, policy/relationship/imported-source proposals, authoring network, reconcile, revision restore), explorer CRUD and list, cell trace, and composition (`New`) | remainder |

Rules for the end state:

- No `func`-typed callback in any sibling `Config` points back into `lifecycle`. Siblings take
  `*authoringcore.Core`, or a consumer-owned interface they define, plus real external ports.
- Each sibling service is built once in `New`. No `sync*` or per-call construction.
- `lifecycle` has no unexported one-line forwards to siblings. Exported forwards remain only
  where `api/explorer` needs a single facade method.
- Tests live in the package that owns the behaviour. Tests build a complete `Config` before
  `New`. Nothing changes `service.config` after construction.
- `Config` shrinks to the ports `lifecycle` itself uses, with nested per-package configs
  (`Core`, `Browse`, `Publication`) instead of 44 flat fields.

Optional, decided separately: move `lifecycle/ports` → `explorer/ports` and `lifecycle/support`
→ `explorer/apperr` (or similar), since every sibling imports them. `TARGET_TREE.md` currently
keeps `lifecycle/ports`.

## Phases

Each phase is one commit (or one tightly coupled set) and leaves `make test-go` green. Phases
1–2 change no package boundaries. Phases 3–6 are behaviour-preserving moves.

### Phase 1: delete dead code

- Delete the Finding 1 functions and the empty `const ()` blocks after confirming each with a
  compile. Delete Finding 2 shims together with the tests that exist only to reach them, **only
  where** an equivalent test already exists in the sibling package. Otherwise defer that shim to
  Phase 6.
- Exit: `go build ./...`, `go vet ./internal/explorer/...`, `make test-go`,
  `make lint-go-comments`. No exported symbol changes.

### Phase 2: build services once

- Change the 99 test sites that set `service.config.X` after `New` so they build a complete
  `Config` first. A shared test builder in `lifecycle` (`newTestService(t, opts...)`) keeps this
  compact.
- Replace `syncConstructionService`, `syncRowDefService` and `rowSourceService` with the fields
  built in `New`. Remove `construction.Service.SetConfig` if nothing else uses it.
- Exit: same checks as Phase 1. `rg 'sync(Construction|RowDef)Service|rowSourceService\(' internal`
  returns nothing.

### Phase 3: extract `authoringcore`

- Run `python3 scripts/package_audit_verify.py internal/explorer/lifecycle` before starting.
- Create the package with a concrete `Core` built from the store, capability resolver, receipt
  compiler/lookup/previewer ports, interpretation repository, selection members collection and
  clock. Move the functions listed in the target table.
- Put workspace population input resolution and route validation in `authoringcore`. The
  population service owns proposal and route-choice use cases, and may depend on core.
  Move `construction`'s `currentWorkspace` into Core and have `construction` call Core.
  Keep the per-package error stage names (`construction-choices`, etc.) by passing the stage.
- Replace the callback fields in `construction.Config`, `rowdef.Config`, `population.Config` and
  `proposal.Config` with `Core`. The request-shape adapters (`construction.CompileRequest` →
  `compileRequest`) go away. Use one `authoringcore.CompileRequest`.
- Exit: Phase 1 checks, `make test-integration`, `package_audit_verify.py --full`, and a review of
  the import diff to confirm no sibling imports `lifecycle`.

#### Phase 3 dependency map

Core owns the shared operations below. The four use-case packages can import Core
together once these symbols move with their dependencies.

| Core responsibility | Source and dependency to account for |
| --- | --- |
| Owner and current workspace | `lifecycle/dataframe.go` owns owner loading and the virtual default dataframe. `construction/search.go` owns current workspace loading and its error mapping. Move the loader with its stage parameter and preserve the existing error messages. |
| Compile and catalog | `lifecycle/authoring.go` owns compile, compile authorization, bindings, and catalog. Use one Core compile request across construction, population, proposal, and rowdef. Population input resolution and route validation are already extracted locally. Interpretation resolution needs the interpretation repository. |
| Receipt lookup and executable validation | `lifecycle/policy.go` owns the lookup and route checks. The native-contract predicate depends on compiler, preview, population mapping, or materializer availability. Carry that predicate into Core without importing the population or publication use cases. |
| Verify proposal receipt | Move `proposal.VerifyReceipt` with `lifecycle/proof.go`'s catalog wiring. The verifier uses workspace normalization, receiptmodel, capability, and shared error helpers. |
| Preview | Move the generic receipt preview in `lifecycle/preview.go` with `proposal.PreviewResult` and `proposal.PreviewSinkFactory`. Proposal keeps its own preview orchestration, including authorization, budgets, and timing. |

Remaining callback ownership matters separately from the import graph.

- Construction frame-source reauthorization is Phase 4 work. Keep the existing boundary
  through Phase 3.
- Population member-removal preview can call Core Preview. Selection creation goes through
  the selection service. The document digest currently forwards to `proposal.DocumentDigest`.
  Output lookup and compiler scope forward to `construction.ConstructionDocumentIndex`
  and `construction.ConstructionCompilerScope`. Keep these outside Core or move the
  shared implementations with their callers. Materialization error classification lives
  in `lifecycle/publish.go` and also serves mapping. Preserve its caller-supplied stage
  and current messages when moving it.
- Rowdef candidate workspace preparation currently dispatches to rowdef or construction
  workspace shaping. Keep it outside Core. Moving it into Core would introduce a reverse
  use-case dependency.
- Proposal network and imported-source editors are configured ports. Pass the configured
  functions directly. Compiler availability comes from Core.

A Go build overlay added all four use-case imports of Core together, plus Core's
anticipated imports for the shared operations. The build passed without a Core import
of any of the four use-case packages. This verifies the package directions. The
actual symbol moves, behavior checks, and audit registration remain Phase 3 work.

### Phase 4: move field browse to `semanticbrowse`

- Move the six files and their tests. `fieldBrowse` and `frameSourceBrowse` become
  `semanticbrowse.Service`, which depends on `authoringcore.Core` and the coverage and selection
  ports.
- `construction` currently receives `ReauthorizeFrameSourceChoice` as a callback. Have it depend
  on `semanticbrowse` (or on a small interface it owns) instead. If `semanticbrowse` turns out to
  need `construction`, move frame-source reauthorization into `construction` instead.
- Exit: Phase 3 checks plus `make test-ui` and `make e2e` (or the `.codex/skills/verify` flow),
  because these endpoints drive the visible Builder field browser.

### Phase 5: move publication and artifacts out

- Move the publication files and tests to `explorer/publication`. `server/explorer.go` wires it.
  `api/explorer` artifact and publish handlers call it directly or through the facade.
- Exit: Phase 3 checks plus the `.codex/skills/verify-loom/SKILL.md` publication checks.

### Phase 6: move tests to the code they test, then shrink `Config` and the facade

- Move `delegates_test.go`, the rowsource/construction parts of `shared_test.go` and any other
  shim-pinned tests into `rowsource`/`construction`/`rowdef`, rewritten against their public
  APIs. Delete the remaining Finding 2 shims.
- Group `Config` into nested per-package configs. Update `server/explorer.go` and the 68
  `lifecycle.Config` literal sites (mostly tests).
- Decide whether `api/explorer` handlers take narrow interfaces per route family. They are
  already organized that way since `9f0309833`.
- Exit: Phase 3 checks. `lifecycle` source is at or under about 3k lines. Update
  `TARGET_TREE.md` and `docs/PACKAGE_AUDIT_DECISIONS.csv`.

## Execution status

- Phase 1 — done. Removed 29 declarations and three empty constant blocks from
  `delegates.go`, `service.go` and `shared.go`. The lifecycle package went from 8,358 to
  8,140 source lines and retained 29,228 test lines. The Finding 2 shims remain deferred
  to Phase 6 pending equivalent sibling coverage. The lifecycle test compile, package audit
  before and after, `go build ./...`, `go vet ./internal/explorer/...`, `make test-go` and
  `make lint-go-comments` passed.
- Phase 2 — done in `e06ab6402`. Child services are built once and the tests prepare
  the config before constructing the service. The operator waived that phase's review.
- Phase 3 — done. Core now owns workspace loading, compile and input
  resolution, receipt lookup and verification, and generic receipt preview. Construction,
  rowdef, population, and proposal use it directly. The lifecycle dependency closure adds
  only `internal/explorer/authoringcore`. Lifecycle went from 8,101 to 7,377 source
  lines and from 29,495 to 29,504 test lines. Go build, Explorer vet, comment lint, focused
  audits before and after, full audit, `make test-go` with loopback access, and
  `make test-integration` against disposable Arango and ClickHouse passed.
- Phase 3 gate repairs. The saved-route and publication API failures reproduced on
  base `e06ab6402` with identical HTTP statuses and bodies. A prior package move
  dropped the `explorer_` prefix from WorkspaceStore's owner keys. Commit
  `23a14c133` restored the original key across creation, lookup, draft CAS, and
  publication. Both API cases passed against disposable Arango and ClickHouse after
  the repair. The integration driver then exposed stale package paths for recipe
  Arango tests and semantic backfill. Both paths were corrected and every integration
  profile was checked against its required tests. The full audit passed after the
  authorized Core entry was added and moved files were staged for its formatting check.
- Phase 3 review used three same-model Codex reviewers on the source based on `e06ab6402`
  (Go diff and new-file fingerprint `ee9e753f265638067feb486595f15f706611e543081ac1aa2ee77b102d365f83`).
  Their focused compile and receipt, preview and owner, and service wiring checks found
  no proven behavioral regressions.
- Phase 4 — done. Semantic inventory, field presentation, schema
  search, column source, and frame options moved to `semanticbrowse`. Coded Pivot
  reauthorization moved to construction, where its caller and direct-root constraint
  live. General frame commands retain routed reauthorization and their original error
  stages. Review caught a coded-only restriction leaking into general frame apply.
  SET and REPLACE now have a regression test. The focused Go suites and unchanged
  Explorer API tests passed. The Loom verify flow passed its related-field and
  starting-record Chrome cases on the repaired source. The UI suite passed 1,600
  tests with isolated lockfile dependencies. Lifecycle source went from 6,173 to
  3,350 lines and tests went from 28,516 to 28,379 lines. Build, vet, lint, focused
  and full audits, and integration passed. A prep worker briefly modified unstaged
  Go files during verification. Those edits were reversed and the full audit and
  integration both passed on the restored snapshot.
- Phase 5 — done. Publish, repository publish, and artifact preparation, download,
  descriptors, identity, and row delay now belong to `explorer/publication`. The name
  identifies Explorer publication and artifacts. `server/sourcepublication` remains
  the source-commit adapter. No `publicationcontract` name collision exists.
  Lifecycle went from 7,377 to 6,173 source lines and from 29,504 to 28,516
  test lines. Its dependency closure adds only `internal/explorer/publication`.
  Build, vet, comments, focused and full audit, integration, and the isolated
  real-data acceptance flow with headless Chrome passed. The first acceptance run
  passed data and API checks and failed to load Playwright. Installing it in the
  worktree resolved browser smoke. Three same-model reviewers found no proven regressions.
  The artifact tests moved. Lifecycle retained its facade tests and the population
  mapping limits. Shared materialization error classification moved to support.
- Phase 6 — pending.

## Verification and review

- Before and after every move phase, run `scripts/package_audit_verify.py`, then its `--full`
  mode, and inspect the dependency diff (AGENTS.md).
- Moves must not change behaviour: HTTP responses, error codes and receipt contents stay
  byte-identical. Existing API tests in `internal/api/explorer` are the oracle. Do not edit their
  assertions during a move phase.
- Phases 2–5 change behaviour-adjacent wiring. Apply `pstack:interrogate` to each phase's final
  diff before committing.
- Keep one writer per package per phase. Phases 4 and 5 can run in parallel worktrees once
  Phase 3 lands. Phases 1–3 are sequential.

## Risks and open questions

- **Names.** `authoringcore` is the shared authoring service used by the Explorer use cases.
  Choose the Phase 5 package name after checking `publicationcontract` and
  `server/sourcepublication`.
- **Test-driven config mutation may hide a real runtime need.** Phase 2 must confirm that
  production code never changes `Config` after `New`. If a test relies on hot-swapping, replace
  it with a test of the real behaviour.
- **Frame-source reauthorization direction.** Phase 4 decides whether it lives in
  `semanticbrowse` or `construction`, based on the actual imports.
- **The heuristic dead-code list may include false positives.** The compiler is the authority.
- **The test-to-source ratio drops only when Phase 6 deletes duplicates.** Moving tests without
  deduplicating them only redistributes the 29k lines.
