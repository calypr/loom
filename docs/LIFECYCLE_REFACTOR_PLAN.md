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
| `explorer/authoringcore` (new, name open) | `getOwner`, `currentWorkspace`, `resolveCompilationCapability`, `compile`/`compileAuthorized`/`validateCompiledReceipt` plus binding clone/compare helpers, `lookupReceipt`, `validateExecutableReceiptRoute`, `verifyProposalReceipt`, `catalog`, `Preview`, interpretation/population input resolution | `authoring.go` (≈ lines 392–560, 680–710), `policy.go`, `proof.go`, `dataframe.go`, `preview.go`, `interpretation.go`, `population.go`, `construction/search.go` `currentWorkspace` |
| `explorer/semanticbrowse` | Semantic inventory browse, field presentation, schema field search, column source, frame-source browse and `reauthorizeFrameSourceChoice` | `inventory.go`, `presentation.go`, `frame.go`, `schema.go`, `column.go`, `browse.go` |
| `explorer/publication` (new, name open) | Publish, repository publish, artifact prepare/open/download, artifact descriptors, `DelayArtifactRows` | `publish.go`, `repository.go`, `artifact.go`, `download.go`, `prepare.go`, `descriptor.go`, `limits.go` |
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
- `population.ResolveWorkspacePopulations` is already a free function over the store, so
  depending on it creates no cycle. Move `construction`'s `currentWorkspace` into `Core` and have
  `construction` call `Core`. Keep the per-package error stage names (`construction-choices`,
  etc.) by passing the stage, so error codes and messages stay byte-identical.
- Replace the callback fields in `construction.Config`, `rowdef.Config`, `population.Config` and
  `proposal.Config` with `Core`. The request-shape adapters (`construction.CompileRequest` →
  `compileRequest`) go away. Use one `authoringcore.CompileRequest`.
- Exit: Phase 1 checks, `make test-integration`, `package_audit_verify.py --full`, and a review of
  the import diff to confirm no sibling imports `lifecycle`.

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
- Phase 3 — blocked. The specified core population-resolution implementation would import
  `population` to call `ResolveWorkspacePopulations`. The specified `population.Config` change
  would import core in the reverse direction. A Go overlay containing those two imports
  confirmed `import cycle not allowed`. The production files remain untouched for this phase.
- Phases 4–6 — blocked by the Phase 3 prerequisite.

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

- **Names.** `authoringcore` and `publication` are placeholders. Check for collisions with
  `publicationcontract` and `server/sourcepublication`.
- **Test-driven config mutation may hide a real runtime need.** Phase 2 must confirm that
  production code never changes `Config` after `New`. If a test relies on hot-swapping, replace
  it with a test of the real behaviour.
- **Frame-source reauthorization direction.** Phase 4 decides whether it lives in
  `semanticbrowse` or `construction`, based on the actual imports.
- **The heuristic dead-code list may include false positives.** The compiler is the authority.
- **The test-to-source ratio drops only when Phase 6 deletes duplicates.** Moving tests without
  deduplicating them only redistributes the 29k lines.
