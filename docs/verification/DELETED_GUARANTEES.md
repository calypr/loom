# Deleted verification guarantees

This reference preserves authored promises without restoring the retired reporting system.
A replacement earns coverage only for its named behavior, bounds, real-code replay, and fault controls.
Neither a unit-test count nor a similar model name establishes equivalence.

## Immutable recovery point

The parent tree of the latest authored-model deletion is commit `5646633e0e5c7ff8de568a4f111478d121e7f75f`.
[Browse its verification directory](https://github.com/calypr/loom/tree/5646633e0e5c7ff8de568a4f111478d121e7f75f/verification).
Commit `e736e130` deleted 69 files under `verification`, added 12, and modified two.
The deletions included 15 TLA models, 19 configurations, 25 JSON files, eight compressed archives, one guide, and one MJS generator.
These files remain available through Git. They were not moved intact into the new runner.

All old model links below use that immutable recovery point.
Current dispositions refer to the working tree and the [current formal skill](../../.codex/skills/builder-interaction-formal-verification/SKILL.md).
They do not claim replacement of every old invariant, assumption, fixture, or trace.

## Authored model promises

| Deleted model | Promise recorded by the old source or configuration | Current disposition |
| --- | --- | --- |
| [BuilderJourney.tla](https://github.com/calypr/loom/blob/5646633e0e5c7ff8de568a4f111478d121e7f75f/verification/builder-interactions/BuilderJourney.tla) | Saved Group and Related state, Cancel, current Apply, reload, and preview ownership. | Partial bounded recovery. The recovered 78-case operation-history inventory executes actual editor/service replay, five Apply/fresh reads, one Cancel and one saved Group witness through mounted remounts. Calculate preservation and saved-read/editor faults are checked. Related Apply/restore checks remain separate. Durable reload and complete BuilderJourney equivalence are not established. |
| [BuilderOperations.tla](https://github.com/calypr/loom/blob/5646633e0e5c7ff8de568a4f111478d121e7f75f/verification/builder-interactions/BuilderOperations.tla) | UI-offered operation acceptance, two-step input columns, and saved-operation editor routing. | Partial model and direct Go replay. ConstructionChain covers two ordinary stages and predecessor-column availability. Operation-family and editor-routing guarantees remain open. |
| [BuilderPreview.tla](https://github.com/calypr/loom/blob/5646633e0e5c7ff8de568a4f111478d121e7f75f/verification/builder-interactions/BuilderPreview.tla) | Current proposal and reviewed preview, initiating Apply owner, discovery scope, and draft monotonicity. | Partial model and direct replay. BuilderLifecycle and ApplySettlementRace protect named ownership rules. Pivot semantics and all prior rules are not equivalent. |
| [ColumnPresentation.tla](https://github.com/calypr/loom/blob/5646633e0e5c7ff8de568a4f111478d121e7f75f/verification/builder-interactions/ColumnPresentation.tla) | Labels, capability-gated filters, search, inspection identity, removal, and presentation bounds. | Ordinary checks only. Native table drag covers selected order and row-identity behavior. The old formal scope remains open. |
| [ConstructionLifecycle.tla](https://github.com/calypr/loom/blob/5646633e0e5c7ff8de568a4f111478d121e7f75f/verification/builder-interactions/ConstructionLifecycle.tla) | Applicable proposals, matching Apply owner, and current request identity. | Partial model and direct replay. BuilderLifecycle and ApplySettlementRace cover current proposal and attempt ownership. |
| [DraftRestore.tla](https://github.com/calypr/loom/blob/5646633e0e5c7ff8de568a4f111478d121e7f75f/verification/builder-interactions/DraftRestore.tla) | Requested previous revision, exact restored revision, request clearing, and bounded draft versions. | Ordinary checks only. Public restore and route round trips cover exact persistence and CAS. No dedicated restore model remains. |
| [Interpretation.tla](https://github.com/calypr/loom/blob/5646633e0e5c7ff8de568a4f111478d121e7f75f/verification/builder-interactions/Interpretation.tla) | Explanation before creation, exact revision and review receipt, incomplete review, and stale review invalidation. | Open. No replacement formal interpretation claim is recorded. |
| [LifecycleEntry.tla](https://github.com/calypr/loom/blob/5646633e0e5c7ff8de568a4f111478d121e7f75f/verification/builder-interactions/LifecycleEntry.tla) | Entry loading, selection and focus identity, current draft commands, receipt invalidation, and bounded retries. | Partial model and direct replay. ReconcileLifecycle, ReconcileAcceptance, and ReconcileRefresh cover receipt and recovery rules. Entry and command breadth remains open. |
| [PolicyOneDisagreement.tla](https://github.com/calypr/loom/blob/5646633e0e5c7ff8de568a4f111478d121e7f75f/verification/builder-interactions/PolicyOneDisagreement.tla) | Observed ONE refusal, ALL repair, request ownership, CAS, exact saved state, removal, and restoration. | Open. Historical native and TLC receipts are recoverable. Current ownership checks do not reproduce this complete journey. |
| [Population.tla](https://github.com/calypr/loom/blob/5646633e0e5c7ff8de568a4f111478d121e7f75f/verification/builder-interactions/Population.tla) | Attach-route scope, complete coverage identity, exclusion failure preservation, and exact removal receipts. | Partial public-service model and replay. PopulationMapping checks report scope, complete coverage binding and withholding incomplete counts. PopulationRemoval adds bounded public proposal-to-Apply receipt, provenance, preview-failure and controlled CAS checks. Generated public attachment now covers 482 bounded token/header/route/batch cases with exact edge/direction and stale-choice controls. Attachment replacement, general multi-hop routes and database atomicity remain open. |
| [RootChange.tla](https://github.com/calypr/loom/blob/5646633e0e5c7ff8de568a4f111478d121e7f75f/verification/builder-interactions/RootChange.tla) | Apply only the current root preview, matching epochs, and cancellation invalidation. | Mounted Behavior source-fault checks prevent detached assessment from starting an obsolete preview. General root-change lifecycle, epoch and Apply equivalence remain open. |
| [RowDefinitionLifecycle.tla](https://github.com/calypr/loom/blob/5646633e0e5c7ff8de568a4f111478d121e7f75f/verification/builder-interactions/RowDefinitionLifecycle.tla) | Ready and applied proposals match the selected row definition and its version. | Ordinary checks only. No dedicated replacement row-definition model is recorded. |
| [SupportingSurfaces.tla](https://github.com/calypr/loom/blob/5646633e0e5c7ff8de568a4f111478d121e7f75f/verification/builder-interactions/SupportingSurfaces.tla) | Read-only review, confirmed frame replacement, request-bound row sources, page limits, and graph inspection. | Open. No replacement formal claim covers this collection. |
| [TemporalFilterJourney.tla](https://github.com/calypr/loom/blob/5646633e0e5c7ff8de568a4f111478d121e7f75f/verification/builder-interactions/TemporalFilterJourney.tla) | Observed mutation, reconcile, preview, proposal, CAS, chronology, and reload bindings. | Open. No replacement observed-transport journey establishes the old scope. |
| [WorkspaceLifecycle.tla](https://github.com/calypr/loom/blob/5646633e0e5c7ff8de568a4f111478d121e7f75f/verification/builder-interactions/WorkspaceLifecycle.tla) | Explorer existence, hydration, table and route selection, delete fallback, and accepted preview identity. | Ordinary checks only for broad workspace behavior. Current reconcile and ownership models cover narrower rules. |

`ReconcileLifecycle.tla` was modified rather than deleted. It is outside this list.
The current direct replay adapters execute Apply ownership, receipt acceptance, and the async recovery callback.
They do not prove every React schedule, operation composition, backend result, or browser lifecycle.

## Contracts, reports, and historical evidence

| Deleted material | Retained meaning and disposition |
| --- | --- |
| Generated obligation indexes, readiness summaries, and semantic bookkeeping | Derived associations are not executed correctness checks. Rebuild useful summaries from current sources when needed. Earlier generated-report changes used ignored local caches. Their old generators are not current commands. |
| Authored route contracts, proposal decisions, source bindings, and required-check registrations | These describe intended behavior or evidence requirements. Their removal does not establish that the promise was obsolete. Recover the relevant Git source when a new slice needs its original reader rule. |
| `retained-browser-evidence/formal-tla-receipt.json.gz` and `formal-mutation-receipt.json.gz` | Historical native trace replay and eight negative controls contain substantive scoped evidence. They are not a fresh current-source pass. Preserve their Git locators rather than copying binary reports back into the checkout. |
| Other retained browser archives and their `manifest.json` | The manifest records raw and compressed hashes. The archived fixtures and exact checks must be read before reusing an old pass. Historical success alone does not close a current gap. |
| `BuilderTraceValidation.md` and `operationHistoryModel.mjs` | The retained runner executes eleven fixed schedules adapted from the old generator, including five Apply/read-back cases and one Cancel. One saved Group witness also remounts the editor. The 78-case search and optional full-profile command were removed after no additional fault detection was demonstrated. Repeated key histories and other omitted combinations are not proved equivalent. Recover original assertions from the immutable Git source above. Durable/browser reload and complete old TLA scopes remain open. |

The evidence archives and guide use the same immutable directory above.
For example, `git show 5646633e:verification/builder-interactions/BuilderOperations.tla` reads the old model without restoring it.

## Bounded recovery queue

1. Construction predecessor lineage has a bounded ConstructionChain replay through public `Construction.Validate`. Explicit cohort insertion, arbitrary depth, UI operation families, and editor routing remain open.
2. PopulationMapping now checks the bounded public report boundary. PopulationRemoval adds bounded public proposal-to-Apply receipt, provenance, preview-failure and controlled CAS checks. Generated public attachment now covers 482 bounded token/header/route/batch cases with exact edge/direction and stale-choice controls. Attachment replacement, general multi-hop routes and database atomicity remain open.
3. Restore, root change, and row-definition identity remain open as complete formal scopes.
4. Eleven fixed operation-history schedules retain the reviewed saved shapes, Cancel, key identity and type propagation through `--phase journey --feature operation-history-routine`. The other 67 generated cases and breadth search are no longer maintained. Original source remains at `5646633e:verification/builder-interactions/operationHistoryModel.mjs`. Broader observed transport, repeated key histories, durable reload and restore/late-completion scope remains open.

The [retained coverage decisions](RETAINED_COVERAGE_DECISIONS.md) record browser and database boundaries already recovered.
The [feature map](../../.codex/skills/builder-interaction-formal-verification/features/README.md) names current execution routes.
This reference is not a new runtime catalog or a pass gate. Update a disposition when the replacement has executable evidence.
# FE-17 step 1 retired verification guarantees

Recovery point: commit `45879f3b2` (the immutable source snapshot used for this isolated FE-17 worktree).

Retired checks: `TableShapeSettingsPanel.unit.test.tsx`, `TableShapeEditor.unit.test.tsx`, `TableShapeComparison.unit.test.tsx`, and `tableShapeController.unit.test.ts` covered TableShape-specific stale catalog/epoch behavior, proposal preview/review state, and settings editing. Their source is recoverable with `git show 45879f3b2:<path>`.

Replacement scope: production Builder reshape uses Construction; retain its `ConstructionReshapeEditor.*.unit.test.tsx` coverage. This is a scoped retirement record, not a claim that every prior TableShape assertion is behaviorally equivalent.

Also retired stale automation/test scaffolding: the `legacy.table_shape.grouped_pivot` and `legacy.table_shape.unpivot` adapters in `testSupport/builderContractAdapters/shapeCombine.ts`, the derived TableShape adapter in `core.ts`, the orphan `ConstructionSourcePicker.unit.test.tsx` panel test, and TableShape-only mock setup in BuilderWorkspace tests. These all targeted a panel no production entry renders. Recover from the same commit if needed.

Additional FE-17 step 1 retirements: `ConstructionOperationsPanel.unit.test.tsx` covered orphaned operation-panel selection and proposal interactions. Removed TableShape cases in `api.unit.test.ts` and `types.unit.test.ts` covered legacy endpoint request mapping, scalar/comparison evidence strictness, and proposal catalog/receipt identity validation. Recover these checks at `45879f3b2` using the original paths. These promises target the retired client layer; live Construction coverage remains separately maintained.
# WP-1 retired helper guarantees

Source recovery point: immutable base commit `45879f3b2` (`feature/default-recipe-authoring` HEAD used for the detached WP-1 worktree). The removed definitions can be recovered from that commit with `git show 45879f3b2:<path>`.

The following unreachable production helpers had no runtime callers according to `deadcode -test ./...`; removing them retires no live behavior:

- `internal/acceptance/performance.go: ComparePerformance`: legacy single-pass performance comparison API. The supported report-file comparison functions remain.
- `internal/catalog/write_profiler.go: fieldCatalogStats.addExtensionValueWithLimits`: duplicate limits helper on the stats struct. The active `Profiler.addExtensionValueWithLimits` remains.
- `internal/catalog/arango/semantic_coverage.go`: `semanticCoveragePathAggregateKey`, `containsSemanticCoverageResource`, `semanticCoverageBuildBindVars`, and `semanticCoverageCatalogReferenceFromCatalog`: unused semantic-coverage query/key helpers.
- `internal/dataframe/compiler/lower/group_related_occurrence_lower.go: canonicalGroupRelatedPath`: unused path canonicalization helper.
- `internal/dataframe/compiler/lower/recipe_construction.go: constructionScalarKindsCompatible`: unused scalar-kind compatibility helper.
- `internal/dataframe/compiler/render/aql/render.go: RenderPhysicalPlanWithUnorderedTerminalProjection`: unused renderer entry point. The presence-marker variant remains.
- `internal/dataframe/execution/engine.go: compiledFinalStageID`: unused final-stage identifier helper.
- `internal/dataframe/published/read.go: encodeCursor`: unused cursor encoder.
- `internal/explorer/interpretation.go: NewInterpretationRuleID`: unused interpretation ID constructor.
- `internal/explorer/selection.go: SelectionRuleDigest`: unused digest variant. `SelectionRuleDigestWithMembers` remains.
- `internal/explorer/authoringv2/construction_validation.go: validConstructionGroupSourceRole`: unused validation helper.
- `internal/explorer/lifecycle/construction_choice.go: capabilityCandidateBelongsToRoot`, `frame_source.go: frameSourceCommand`, and `semantic_catalog.go: proveOwnerRecords`: unused lifecycle helpers.
- `internal/explorer/tableshapecap/identity.go: categoriesAsScalars`: unused conversion helper.

The following test-only helpers were also unreachable even from tests. Their removal retires only the helper's unexercised fixture or comparison path; surrounding test cases remain:

- `internal/dataframe/execution/group_preview_scan_test.go: coveringGroupExplain`: unused group-preview explain fixture helper.
- `internal/dataframe/publication/arango/name_latest_binding_e2e_test.go: nameBindingReleaseRepository.savedRelease`: unused saved-release fixture lookup.
- `internal/dataframe/publication/clickhouse/unpinned_page_read_e2e_test.go: uprHarness.seedPublishedOutputRecord`: unused published-output fixture seeder.
- `internal/explorer/lifecycle/derive_source_test.go: deriveSourceIdentityColumns`: unused derive-source test helper.

`PhysicalSetKind`, `PhysicalNodeSetKind`, and `PhysicalNodePathSetKind` had no production writers or readers. The enum and constants were removed, but `PhysicalSet.Kind` remains a serialized string because `physicalPlanFingerprint` hashes JSON-encoded plans. The two renderer fixtures retain the original `"NODE_SET"` string value. `TestPhysicalPlanFingerprintRetainsSetKind` pins the fingerprint for a representative nonzero kind (`27e8342e…b1cec5e`) so this serialized digest input remains stable.
