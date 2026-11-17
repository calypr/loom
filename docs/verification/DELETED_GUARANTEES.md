# Deleted verification guarantees

This reference preserves authored promises without restoring the retired reporting system.
A replacement earns coverage only for its named product behavior and stated bounds.
Neither a unit-test count nor a similar model name establishes equivalence.

## Plan 2 retirement (recovery: `45879f3b2`)

Recover any path in this table with `git show 45879f3b2:<path>`. Wildcards name the entire retired harness family, including its helper-only tests. Earlier model tables below are historical promises; they do not describe maintained models or current coverage. No TLA+ model remains in the repository or CI.

The replacement commands are `make test-ui`, `make test-go`, `make test-integration`, and `make e2e`. Browser checks use literal NDJSON as their independent oracle. Model schedules were frozen from the passing `846dc7ffa` snapshot before removing the model toolchain. Passing a replacement does not establish every old workflow, fixture scale, or unbounded invariant.

| Retired path or family | Replacement or disposition |
| --- | --- |
| `verification/builder-interactions/**` | Frozen production-code Go schedules in `internal/explorer/{arango,authoringv2,lifecycle}/testdata`, server core-protocol witnesses, and four UI schedule suites. Model-only invariants and controls dropped. |
| `verification/publication-bundle/**` | Real publication/Arango/ClickHouse tests remain. `FINDINGS.md` and `PUBLISHED_IMMORTALITY_PROPOSAL.md` moved to `docs/architecture/publication/`; model-only controls and generated evidence dropped. |
| `.codex/skills/builder-interaction-formal-verification/**` | Current verification skills point to direct product tests. Core C01–C19 checks remain in `internal/server/core_authoring_protocol_*_test.go`. |
| `scripts/verify-builder-formal-core.mjs` | Direct Make targets and CI commands. No registry or umbrella verdict replaces actual test results. |
| `scripts/setup-formal-tools.mjs`, `scripts/review-builder-sources.mjs` | Dropped: model tooling and fixed source-pin refresh. |
| `scripts/formal-core-protocol-manifest.mjs` | Direct core-protocol Go tests and checked-in schedules; manifest coupling dropped. |
| `scripts/lib/formal-api.mjs` | Its exact Go integration test names run directly from `make test-integration` and CI. |
| `scripts/lib/formal-{derived-policy,append-rows}.mjs` | Real server/Arango/ClickHouse arithmetic and append-row tests run directly. |
| `scripts/lib/formal-{apply,recovery,reconcile,exclusion,publication}-replay.mjs` | Plain Vitest schedule suites in `constructionWorkspace` and `authoring`; actual callbacks and reducers execute. |
| Other `scripts/lib/formal-*.mjs` and `formal-*.test.mjs` | Production replays moved to ordinary Go/Vitest tests; framework, model, trace, and mutation-only tests dropped. |
| `scripts/lib/{reviewed-sources,verification-inventory,verification-budget}.mjs` and inventory test | Fixed pins/inventories dropped. Kept integration tooling retains deadlines, required actual-test completion, cleanup, and current-run source integrity. |
| `.github/workflows/builder-formal.yml` | Direct Go/Vitest jobs and the component-browser job in `tests.yaml`; model/source-proof jobs dropped. End-to-end cadence remains an owner decision. |
| `scripts/verify-ui/**` | Five e2e specs cover authored preview/publish/view/download, root changes, Group/Pivot, ONE/ALL repair, and record networks. Helper-only tests/registries and dormant workflow variants dropped. The deep DocumentReference→Specimen→Patient path and preserved unmatched row now have a raw-NDJSON browser oracle. All seven cases passed three consecutive serialized runs before final retirement; other legacy default-output fields/workflows are not claimed equivalent. |
| `scripts/loom-dev.mjs` J01–J05 journey layer and journey-only tests | New e2e specs. Compose/session, raw fixture seed, bootstrap, identity, Doctor, down, and their tooling tests remain. |
| `scripts/verify-builder-{traces,group-slice}.mjs` | Kept mounted tests and frozen operation-history journeys. Trace ledgers and source-proof controls dropped. |
| `scripts/verify-{selections,population-row,construction-removals,correlated-concepts,cda-mixed-scope-selectors,builder-reconciliation}.mjs` | Direct Go and mounted UI checks; dormant wrapper/reporting paths dropped. |
| `scripts/{run-graph-rows-native,run-traversal-first-native,prepare-graph-rows-history,prepare-traversal-first-targets,revalidate-graph-rows-history}.mjs` | Native replacement is `record-network.spec.mjs`; the retained independent Go graph oracle is compiler/AQL coverage, not equivalent native deep-traversal coverage. |
| `scripts/*.playwright.config.mjs`, `scripts/package{,-lock}.json` | Chrome/CDP specs run through `make e2e`; unused Playwright package/configuration dropped. |
| `scripts/browser/{compare-engines,lightpanda-cases,browser-pool,benchmark-chromium,engine-verdict,batch-scheduler}.mjs` and their helper-only tests | Dropped: dormant alternate-engine scheduling/benchmarks. `run.mjs`, `cdp.mjs`, artifact caching, and kept-tool tests remain. |
| `scripts/audit-lightpanda-coverage.mjs` | Dropped: alternate-engine inventory. |
| `scripts/{run-assigned-check,coverage-status,verification-timing,capture-owned-verification,owned-stack-verification,run-scope-001-restricted-auth}.mjs` | Direct test commands; dormant assignment/evidence wrappers dropped. |
| `scripts/{measure-b05-preview,measure-b07-evidence,verify-b07-faults,measure-authoring-loop}.mjs`, tests, and `scripts/measurements/**` | Dropped: dormant measurement/source-fault infrastructure; no current performance improvement claimed. |
| `scripts/validate_ml_dataframer_plan.mjs`, `scripts/fixtures/generate-cda-mini-fixture.mjs` | Dropped: plan validation and unused generated browser fixture. Literal fixture NDJSON remains. |
| `scripts/{verify_ui_contract_inventory,normalize_ui_contract_inventory}.py`, tests, `scripts/lib/ui-contract-inventory.mjs`, `docs/ui-contract-inventory/**` | Dropped: source checkpoint/inventory and its uncalled wrapper. Runtime parser and real component/API tests remain. |
| `scripts/{validate_architecture_plan,test_validate_architecture_plan}.py`, `scripts/tests/test_ui_ci_workflow.py` | Dropped: planning-document/source-text verification. |
| `scripts/tests/test_acceptance_real.py` | Dropped: six source-text/syntax cases. Real acceptance cleanup tests and release/demo tooling checks remain. |
| `cmd/{explorer-config-v2-convert,preview-covering-index-artifact}/**`, `scripts/preview-covering-index-artifact.mjs` | Dropped: unused offline tools and their three tool-only tests; no served API or operational caller. |
| `internal/api/graphql/graph/dataframe/export{,_test}.go` | Dropped: uncalled writer and its two tests. Published dataset artifact export remains covered by the browser ZIP oracle and Go published-path tests. |
| Retired reports under `docs/verification/**` and plan-named Builder verification narratives | Dropped: generated logs, ledgers, browser archives, and obsolete procedure narratives. Open product defects are in `docs/BACKLOG.md`. |

The old component `drag` branch had no corresponding control in the current product. It is replaced by actual PreviewTable rendering, row-limit callback, row identity and reload checks. Downloads and saved-route component checks remain. The select check dispatches DOM input/change events and does not claim native popup behavior; native downloads and pointer actions remain exercised.

## Historical recovery point

The parent tree of the latest authored-model deletion is commit `5646633e0e5c7ff8de568a4f111478d121e7f75f`.
[Browse its verification directory](https://github.com/calypr/loom/tree/5646633e0e5c7ff8de568a4f111478d121e7f75f/verification).
Commit `e736e130` deleted 69 files under `verification`, added 12, and modified two.
The deletions included 15 TLA models, 19 configurations, 25 JSON files, eight compressed archives, one guide, and one MJS generator.
These files remain available through Git. They were not moved intact into the new runner.

All old model links below use that immutable recovery point.
The dispositions in this historical section describe the earlier audit snapshot, not the current tree. The retired skill is recoverable at `45879f3b2:.codex/skills/builder-interaction-formal-verification/SKILL.md`.
They do not claim replacement of every old invariant, assumption, fixture, or trace.

## Historical authored-model promises

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
| Preview-index runtime `Supersedes` fallback and its cap-time store tests | `internal/store/arango/preview_index_test.go` and `internal/dataframe/compiler/category_scan_explain_arango_test.go` promised that a request could replace an exact old index at cap. Replaced by the versioned WP-19 migration and its AgainstArango legacy/current/mixed/rerun checks. Current runtime tests verify the compiler-selected index and cap refusal without deleting old definitions. Recover the retired request-time tests with `git show 5b3d157d89e80ae87c0247db797f1ee179a9b405:<path>`. |
| Request-time legacy workspace decoding/compile upgrades and Builder nil-step digest repair | WP-18 retires automatic conversion in `DecodeWorkspace`, compilation, commands, canonical serialization, validation, and Builder GET. Existing legacy wire/default/contributor assertions now exercise the explicit offline decoder/migration; the server nil-step test migrates through CAS before GET. Real Arango checks cover read-only dry-run, mutable drafts, preexisting immutable history/revisions, receipt identity, stored projections, reruns, and rollback. Legacy receipts remain verifiable raw artifacts but require recompilation before Preview/Publish; they are not executed through projections. Recover the old runtime behavior and tests with `git show 5ee9e6348c0abd35ea6a7bf5d172f3374559dade:<path>`. |
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


## Standalone Builder smoke: current resource chooser

Recovery point: `fd23b8255:scripts/demo-browser-smoke.sh`. Retired literal assertions required `Preview` and every expected FHIR resource name in the initial Builder HTML. The current UI loads its preview asynchronously and exposes root choices through New table. The replacement waits for the rendered preview, opens New table with a native browser click, and requires each expected resource choice to be visible and enabled. Viewer title/column/cell, stylesheet, and UI-error checks remain. Focused native proof: the seeded NCPI smoke passed with Patient, Condition, Specimen, Observation, and ResearchStudy; final full acceptance is recorded in the harness repair commit. Failure evidence is retained, and negative helper tests cover missing/disabled choices and cleanup failures.

### WP-17: retired TableShape endpoint promises

The four TableShape routes and `APPLY_TABLE_SHAPE_PROPOSAL` are removed after explicit stored-workspace migration. The following tests exercised that retired service surface. Shared receipt fixtures and error assertions were retained for current Construction tests. Stored archive authentication and current Construction migration have separate coverage; final native acceptance remains required before this work package is complete.

| Retired test | Retired guarantee |
|---|---|
| `internal/explorer/lifecycle/table_shape_capabilities_test.go`: `TestTableShapeCapabilitiesRejectInconsistentOwnerDigestAfterStoredV9Migration` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_capabilities_test.go`: `TestTableShapeCatalogDiscoveryPivotAndDerivedReceipts` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_capabilities_test.go`: `TestTableShapeSavedPivotReloadUsesDetachedBaseCatalog` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_capabilities_test.go`: `TestTableScalarToCapabilityScalarPreservesSentinelKeys` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_capabilities_test.go`: `TestTableShapeDiscoveryRejectsStaleAndIncompleteBindings` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_capabilities_test.go`: `TestTableShapeRequestBoundaryRejectsRawDefinitionsAndTamperedChoices` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_capabilities_test.go`: `TestTableShapeCategoryScalarConversionsPreserveFalseAndZero` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_capabilities_test.go`: `TestTableShapeCategoryDiscoveryCanRunBeforeFinalPivotChoices` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_capabilities_test.go`: `TestTableShapeNoScannerReturnsExactRoleRefusal` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_capabilities_test.go`: `TestTableShapeAuthorizationScopeIsRevalidated` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_capabilities_test.go`: `TestTableShapeCatalogReuseValidatesCurrentDraftAndScope` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_comparison_evidence_test.go`: `TestTableShapeExclusionEvidencePreservesTypedValuesAndIdentity` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_comparison_evidence_test.go`: `TestTableShapeExclusionEvidencePreservesOmissionAndSampling` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_comparison_evidence_test.go`: `TestTableShapeUnsupportedAndNoPolicyOnlyProveZeroWhenSemanticsAllowIt` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_comparison_evidence_test.go`: `TestTableShapeExclusionExecutorFailureDegradesWithStableLimitation` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_comparison_evidence_test.go`: `TestTableShapeExclusionExecutorCannotExceedFixedComparisonPage` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_comparison_evidence_test.go`: `TestTableShapeDeclaredInformationLossFollowsCandidateRecipe` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_identity_test.go`: `TestResolveTableShapeRejectsUnlistedSubsetWithErrorPolicy` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_proposal_test.go`: `TestProposeTableShapeBindsCandidateReceiptWithoutMutatingDraft` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_proposal_test.go`: `TestProposeTableShapeRequiresPreviewConfiguration` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_proposal_test.go`: `TestCompareTableShapeReceiptsRejectsMissingOutputContract` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_proposal_test.go`: `TestProposeTableShapeCandidateFeatureErrorReturnsValidationError` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_proposal_test.go`: `TestProposeTableShapePreviewFailuresPropagate` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_proposal_test.go`: `TestTableShapeProposalRequestRejectsRawAuthoringShape` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_proposal_test.go`: `TestProposeTableShapeComposesPivotAndPostPivotDerived` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_proposal_test.go`: `TestProposeTableShapeComposesOrderedDerivedChainWithoutReshape` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_proposal_test.go`: `TestProposeTableShapeComposesUnpivotAndRejectsStaleResolution` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_proposal_test.go`: `TestProposeTableShapeEnforcesModeAndSupportsReplaceAndRemove` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_proposal_test.go`: `TestDiscardingTableShapeProposalDoesNotApplyIt` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_proposal_test.go`: `TestApplyTableShapeProposalAfterStoredV9MigrationKeepsBaseCAS` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_proposal_test.go`: `TestApplyTableShapeProposalSavesCandidateOnceAndIsIdempotent` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_proposal_test.go`: `TestApplyTableShapeProposalRejectsNonAtomicCommandBatch` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_proposal_test.go`: `TestApplyTableShapeProposalRejectsStaleAndTamperedReceipts` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_proposal_test.go`: `TestProposeTableShapeComparesChangedColumnsAndRowsByStableIdentity` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_proposal_test.go`: `TestTableShapeComparisonPreservesCellPresenceAndDegradesTraceFailures` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_proposal_test.go`: `TestTableShapeComparisonBoundsTracesAndGlobalContributors` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_proposal_test.go`: `TestTableShapeComparisonTraceCancellationDoesNotMutateDraft` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_proposal_test.go`: `TestTableShapeComparisonPropagatesStaleTraceAuthorization` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/explorer/lifecycle/table_shape_proposal_test.go`: `TestProposeTableShapeUnpivotReproducesInternalError` | The legacy TableShape service accepts, resolves, previews, or rejects the case named by this test. The legacy service surface is retired. |
| `internal/server/explorer_receipt_contract_test.go`: `TestCompileExplorerReceiptBindsTableShapeProposalBeforeIdentity` | Current receipt compilation accepts a legacy TableShape proposal binding. Current compilation no longer accepts that input; historical receipt bindings remain part of archive authentication and receipt identity. |
| `generated/loomapi/request_boundary_test.go`: four TableShape request constructors; `strict_json.go`: five TableShape decoders | Strict decoding for removed TableShape request types. Current Construction request boundary coverage remains. |

#### FE-17 step 2

| Retired promise | Replacement / evidence |
|---|---|
| `explorerBuilderDocumentSchema` accepts and round-trips opaque persisted `tableShape` through Builder reload. | The current document schema is strict and now rejects the removed field. The test retains a valid Construction document parse and adds a negative legacy-field case. Startup migration and immutable projection authentication are verified by the separate native migration driver. |
| `explorerBuilderCommandSchema` accepts `APPLY_TABLE_SHAPE_PROPOSAL` with a proposal receipt. | The enum/schema entry is removed. The same test now accepts `APPLY_CONSTRUCTION_PROPOSAL` and rejects extra legacy TableShape payload data. Construction category-discovery tests remain. |
| Four TableShape HTTP client methods and associated request/response schemas/types in `api.ts` / `types.ts`. | At isolated baseline `04046c45e`, a whole-UI `rg` scan found no TableShape endpoint names, client methods, or endpoint schemas in `api.ts` or `types.ts`; there were no client endpoint tests to retire. `api.ts` remains byte-identical. |


#### Retired verification script promises

> Historical backend-only disposition, superseded by the PR160 integration section below. The keyword workflow, registry, normalizer and inventory described as retained in this table are retired in the combined tree; these rows do not claim current coverage.

| Retired promise | Reason and current evidence boundary |
|---|---|
| `verify-j04` opened the legacy UI04 TableShape editor, selected a grouped pivot and derived column, and used `/table-shape-capabilities`, category discoveries, resolutions, and proposals. | The historical browser harness and UI04 selectors are retired and absent from the current Builder source. The dev command is removed. The keyword Pivot journey remains on current Construction capabilities, category discovery, proposal, preview, and apply routes. The separate native migrated-workspace driver must verify migrated Pivot and Unpivot lifecycle behavior against independent CDA fixture rows before WP-17 completion. |
| J04 proposal comparison exposed exact contributor identities, complete exclusion partitions, and grouped-pivot information-loss declarations; Cancel preserved the prior workspace; stale `APPLY_TABLE_SHAPE_PROPOSAL` returned 409 without mutation. | The old comparison/command contract is removed. No equivalent exact exclusion/information-loss/stale-proposal claim is made by this scripts patch. The retained keyword workflow still proves native Construction proposal readiness, Cancel digest/version stability, duplicate-output negative behavior, Apply, reload, grid values, and removal. |
| J04 saved `tableShape` reload preserved output/category/derived-column identities, then grouped Observation Preview matched the literal CDA rows and typed/null cells. | The current persisted contract is Construction. The old raw TableShape browser path is retired. The required native migrated-workspace driver checks migrated Pivot/Unpivot/source rows with its independent CDA mini oracle; it does not claim the old multi-derived J04 fixture or its exclusion report. |
| J04 publication retained the grouped preview schema and units; Viewer retained the typed schema and literal rows; downloaded JSONL artifact retained the published identity and rows. | These downstream publication, Viewer, and artifact checks were coupled to the retired UI04 shape workflow and are retired with it here. This artifact makes no replacement claim. The active `.codex/skills/verify/SKILL.md` acceptance route remains required when publication/Viewer/artifact behavior is in scope. |
| The API contract inventory described the four TableShape routes and treated a `tableShapeProposalIntentSchema` discriminant as a current Builder operation. | The normalizer and verifier now map historical pivot/unpivot/derive affordance rows to current Construction operation kinds and Construction proposal endpoints. Construction API route evidence remains in the inventory. |

The pre-migration immutable raw packet and receipt identity assertions belong to the separate WP-17/WP-18 migration and archive-projection checks. This file does not assert that immutable historical raw documents are rewritten.

WP17 retired promise: the removed active TableShape translator reported a constructed column's InputColumns as its immediate legacy named dependencies (for example scaled_patient_count or systolic). Current typed Construction has always reported staged source lineage (patient_count or patient_id/kind/value) while AuthoredColumns retains the authored roots.

This is not a change to current Construction production behavior. The addOutput closure in internal/server/explorer_output_reconcile.go is byte-identical to baseline 04046c45eb860acbbfc02acb091e7eb04f898cbe, SHA256 e3ba1a4fd845dc430f00f375c2ab48a402dbbc2e8dc8f324377d9f608e6dc28b. prove_current_contract.py reruns that comparison. The first actual migrated-fixture run reproduced exactly those two lineage differences; all other emission identity/label/type/nullability/quality/root fields remained identical.

A recompiled migrated draft gets a new current recipe/receipt/digest and follows existing Construction source-dependency semantics. It does not claim an identical old receipt. Immutable old receipts/revisions/public-contract metadata stay original; TestArchivedReceiptRetainsLegacyImmediateInputMetadata authenticates and roundtrips a stored old address-only receipt with InputColumns=[scaled_patient_count], AuthoredColumns=[patient_count], preserving its exact serialized bytes and content address without recompilation. The already-applied native archived-recipe artifact Open/Prepare/execution-rejection test also passed independently (archive-lifecycle-renewal.log).

The old legacy graph-cycle error text is not retained by new current stage fixtures: self/forward input references are rejected at the prior-stage boundary. The migrated negative tests assert exact typed constructionInputMissingError context (step, output, missing ID), preserving cycle/missing-dependency rejection without retaining the legacy model.

The four removed /table-shape-* routes are retired API promises per WP17. The operation-status test now pins construction-proposals with the same failure-status list and explicitly asserts all four retired paths remain absent. Other status/diagnostic/authorization assertions are unchanged.


WP17 compiler oracle migration: the retired recipe.Output.TableReshape entrypoint's unwindowed raw RenderPhysicalPlan emitted Unpivot rows in authored slot order `[z-integer, a-decimal]`. Current Construction StageSequence rendering already sorts final rows by generated identity and emits `[a-decimal, z-integer]`; CompileRecipeOutput's public execution window was already canonically sorted. The obsolete raw-entrypoint ordering assertion is retired without removing current final SORT. The S04 oracle now separately asserts unchanged authored typed Unpivot.Inputs order and corresponding AQL slot-key bindings, then asserts exact current canonical raw and public-window row order. Literal row values, NULL/MISSING, cardinality, selected-key identity, trace contributors/status, and exclusion evidence assertions remain. This does not claim byte-identical legacy/current Unpivot receipts: current raw IDs use the existing scoped `_key` tuple, authenticated legacy IDs remain unchanged, and the fixture's explicit bijective identity mapping retains the old literal witnesses.

#### Retired TableShape capability persistence

The retired endpoint capability catalog and resolution repository have no remaining production consumer. New databases no longer create its collection; existing stored capability documents are left untouched. Historical compilation receipt authentication retains its own original binding and recipe bytes. Current Construction capability and tenant-scoping checks remain separate.

| Retired test | Retired guarantee |
|---|---|
| `internal/explorer/tableshapecap/artifacts_test.go`: `TestReceiptAndChoiceIdentityIsDeterministicAndExcludesCreatedAt` | Identity, validation or persistence behavior for the retired TableShape capability/resolution service described by this test. |
| `internal/explorer/tableshapecap/artifacts_test.go`: `TestEveryBindingFieldChangesCatalogIdentity` | Identity, validation or persistence behavior for the retired TableShape capability/resolution service described by this test. |
| `internal/explorer/tableshapecap/artifacts_test.go`: `TestRoleRefusalHasStableReasonAndCannotExposeChoices` | Identity, validation or persistence behavior for the retired TableShape capability/resolution service described by this test. |
| `internal/explorer/tableshapecap/artifacts_test.go`: `TestProposalIntentIsClosedAndRemoveCarriesNoShape` | Identity, validation or persistence behavior for the retired TableShape capability/resolution service described by this test. |
| `internal/explorer/tableshapecap/artifacts_test.go`: `TestChoiceLookupIsRoleAndParentScoped` | Identity, validation or persistence behavior for the retired TableShape capability/resolution service described by this test. |
| `internal/explorer/tableshapecap/artifacts_test.go`: `TestPolicyChoicesMatchAcceptedCompilerContract` | Identity, validation or persistence behavior for the retired TableShape capability/resolution service described by this test. |
| `internal/explorer/tableshapecap/artifacts_test.go`: `TestPivotCompleteCategoryMembershipIsContentAddressed` | Identity, validation or persistence behavior for the retired TableShape capability/resolution service described by this test. |
| `internal/explorer/tableshapecap/artifacts_test.go`: `TestCategoryProofMustComeFromCompleteBoundedCompilerScan` | Identity, validation or persistence behavior for the retired TableShape capability/resolution service described by this test. |
| `internal/explorer/tableshapecap/artifacts_test.go`: `TestDerivedOperandsAreNumericAndNestedRefsRequireEarlierReceipts` | Identity, validation or persistence behavior for the retired TableShape capability/resolution service described by this test. |
| `internal/explorer/tableshapecap/artifacts_test.go`: `TestScalarTaggedUnionPreservesAllTypedValues` | Identity, validation or persistence behavior for the retired TableShape capability/resolution service described by this test. |
| `internal/explorer/tableshapecap/artifacts_test.go`: `TestReceiptDecodingRejectsUnknownFieldsAndWrongResolutionUnion` | Identity, validation or persistence behavior for the retired TableShape capability/resolution service described by this test. |
| `internal/explorer/tableshapecap/artifacts_test.go`: `TestReadValidationRejectsTamperedContentAddress` | Identity, validation or persistence behavior for the retired TableShape capability/resolution service described by this test. |
| `internal/explorer/tableshapecap/identity_table_shape_test.go`: `TestValidateResolutionAgainstCategoryScanRejectsUnlistedSubsetWithErrorPolicy` | Identity, validation or persistence behavior for the retired TableShape capability/resolution service described by this test. |
| `internal/explorer/tableshapecap/identity_table_shape_test.go`: `TestValidateResolutionAgainstCategoryScanAllowsUnlistedSubsetWithExcludePolicy` | Identity, validation or persistence behavior for the retired TableShape capability/resolution service described by this test. |
| `internal/explorer/arango/table_shape_capabilities_test.go`: `TestTableShapeCapabilityRepositoryIsCreateOnceAndTenantScoped` | Identity, validation or persistence behavior for the retired TableShape capability/resolution service described by this test. |
| `internal/explorer/arango/table_shape_capabilities_test.go`: `TestTableShapeCapabilityRepositoryLookupUsesCurrentRequestIdentity` | Identity, validation or persistence behavior for the retired TableShape capability/resolution service described by this test. |
| `internal/explorer/arango/table_shape_capabilities_test.go`: `TestTableShapeCapabilityRepositoryScopesResolutionByParent` | Identity, validation or persistence behavior for the retired TableShape capability/resolution service described by this test. |
| `internal/explorer/arango/table_shape_capabilities_test.go`: `TestTableShapeCapabilityRepositoryLoadsNestedDerivedParents` | Identity, validation or persistence behavior for the retired TableShape capability/resolution service described by this test. |
| `internal/explorer/arango/table_shape_capabilities_test.go`: `TestTableShapeCapabilityRepositoryLoadsPivotContextForDerivedResolution` | Identity, validation or persistence behavior for the retired TableShape capability/resolution service described by this test. |
| `internal/explorer/arango/table_shape_capabilities_test.go`: `TestTableShapeCapabilityRepositoryFallsBackAfterNullInsertResult` | Identity, validation or persistence behavior for the retired TableShape capability/resolution service described by this test. |
| `internal/explorer/arango/collections_test.go`: TableShape capability indexes | Bootstrap provisions the retired service collection and indexes. Existing databases are not dropped or rewritten. |

#### Construction pivot policy

| Retired test | Replacement guarantee |
|---|---|
| `internal/dataframe/recipe/construction_test.go`: `TestConstructionRejectsExcludeWithEvidencePivotPolicy` | Construction rejects the already-declared `EXCLUDE_WITH_EVIDENCE` policy. Replaced by `TestConstructionPivotUnlistedPolicyContract`: both declared policies succeed and unknown policies remain rejected. Real S04 exclusion partitions remain covered after legacy migration. |

#### Retired TableShape compiler representation assertions

The removed translator cloned source column IDs into Pivot outputs, used a project-plus-key Unpivot identity tuple, embedded category presence inline, and emitted one post-group Pivot limit. Current Construction tests assert explicit unique output IDs, the exact source-key tuple within the project-scoped source, category-presence input sidecars with original source paths, and both post-group and final sorted-output limits. Literal S04 rows, traces, exclusions, authorization partitions, scalar presence, and the independent legacy-to-current identity bijection remain checked. These representation assertions do not promise identical internal plans or raw renderer ordering across the retired and current translators.

### WP-20 / FE-20: Explorer error union retirement

Explorer routes no longer emit the string-or-object `LegacyErrorResponse` union or the service-specific 401 schema; they use `ErrorResponse` with structured code/message and optional details/diagnostics. The old string-constructor and legacy Explorer mapper assertions are replaced by structured-body assertions with identical route statuses, explicit retryability, diagnostic preservation, and cause redaction. Recipe-execution's non-Explorer legacy 403/404 schema and exact 404 string body remain covered. No status-code or rendered UI guarantee is retired.

### WP-21: standalone View Builder API removed

Retired the standalone View Builder HTTP contract and its endpoint-specific guarantees: read/write and publication-scope authorization, route error/status mapping, strict malformed-body rejection, stale catalog/revision conflict handling, catalog adoption, default selection, save binding validation, and delete constraints. The UI, CLI, and scripts had no endpoint caller; README endpoint documentation was removed. These route-level guarantees no longer have a supported caller or API.

Retain and continue testing repository source-publication guarantees: catalog snapshot construction, per-project recipe catalog anchoring in the release/config publication transaction, expected-head checks, and idempotent exact retry behavior. The WP-21 deletion must not remove these internal publication mechanisms or their coverage.


Retired or renamed test inventory:

| Prior file / test | Disposition |
|---|---|
| `internal/explorer/arango/view_builder_integration_test.go`: `TestViewBuilderCatalogSaveDefaultsDeleteAndSourceRebindAgainstArango` | Removed with the standalone View Builder API; publication catalog-anchor coverage is retained separately. |
| `internal/explorer/arango/view_builder_test.go`: `TestAdoptedCatalogSnapshotIsDetachedAndKeepsExactPublishedMetadata` | Removed with the standalone View Builder API; publication catalog-anchor coverage is retained separately. |
| `internal/explorer/arango/view_builder_test.go`: `TestViewBuilderSourcePublicationExpectedHeadsAndExactRetry` | Renamed to `TestProjectConfigSourcePublicationExpectedHeadsAndExactRetry`; publication guarantee retained. |
| `internal/explorer/arango/view_builder_test.go`: `TestSettingsCatalogPublishIsStableOnRetryAndPreservesDefaults` | Removed with the standalone View Builder API; publication catalog-anchor coverage is retained separately. |
| `internal/explorer/arango/view_builder_test.go`: `TestViewBuilderFrameIDsAreRecipeQualified` | Removed with the standalone View Builder API; publication catalog-anchor coverage is retained separately. |
| `internal/explorer/arango/view_builder_test.go`: `TestViewBuilderDefaultRejectsRetargetingOrRemovingItsFrame` | Removed with the standalone View Builder API; publication catalog-anchor coverage is retained separately. |
| `internal/explorer/arango/view_builder_test.go`: `TestProjectRecipeSnapshotMatchesViewFromSameExecution` | Removed with the standalone View Builder API; publication catalog-anchor coverage is retained separately. |
| `internal/explorer/arango/view_builder_test.go`: `TestCatalogSnapshotDoesNotChangeDynamicOrQualityEvidence` | Removed with the standalone View Builder API; publication catalog-anchor coverage is retained separately. |
| `internal/explorer/view_builder_prepare_test.go`: `TestPrepareViewBuilderCatalogSnapshotIsDetachedAndComplete` | Renamed to `TestPrepareProjectConfigCatalogSnapshotIsDetachedAndComplete`; snapshot guarantee retained. |
| `internal/explorer/view_builder_prepare_test.go`: `TestPrepareViewBuilderConfigClonesExactSnapshotWithoutRecipeBody` | Removed with the standalone View Builder API; publication catalog-anchor coverage is retained separately. |
| `internal/explorer/view_builder_prepare_test.go`: `TestPrepareViewBuilderConfigRejectsRecipeAndColumnOutsideCatalog` | Removed with the standalone View Builder API; publication catalog-anchor coverage is retained separately. |
| `internal/explorer/view_builder_prepare_test.go`: `TestSavedViewMetadataDoesNotMutatePublicationCatalog` | Removed with the standalone View Builder API; publication catalog-anchor coverage is retained separately. |
| `internal/explorer/view_builder_service_test.go`: `TestViewBuilderServiceGETDoesNotInitializeProject` | Removed with the standalone View Builder API; publication catalog-anchor coverage is retained separately. |
| `internal/explorer/view_builder_service_test.go`: `TestViewBuilderServiceSavesPresentationAgainstExactCatalog` | Removed with the standalone View Builder API; publication catalog-anchor coverage is retained separately. |
| `internal/server/openapi_error_response_mapper_test.go`: `TestViewBuilderErrorFactoriesKeepHistoricalFallbackText` | Removed with the standalone View Builder API; publication catalog-anchor coverage is retained separately. |
| `internal/server/openapi_view_builder_test.go`: `TestViewBuilderHTTPReadOnlyAndWriteAuthorization` | Removed with the standalone View Builder API; publication catalog-anchor coverage is retained separately. |
| `internal/server/openapi_view_builder_test.go`: `TestViewBuilderHTTPStorageFailuresAndMalformedRequests` | Removed with the standalone View Builder API; publication catalog-anchor coverage is retained separately. |
| `internal/server/openapi_view_builder_test.go`: `TestViewBuilderExecutionAuthorizationKeepsBackendFailuresDistinct` | Removed with the standalone View Builder API; publication catalog-anchor coverage is retained separately. |
| `internal/server/openapi_view_builder_test.go`: `TestViewBuilderScopedEditorCapability` | Removed with the standalone View Builder API; publication catalog-anchor coverage is retained separately. |

### WP-22: typed category rendering

No test or public behavior guarantee is retired. The two old AQL presence-rendering entrypoints and compiler query-text surgery helpers have no remaining callers after migration to typed category terminals. Existing category row/presence/value, source-scope, conservative fallback, overflow, proof and EXPLAIN tests remain; added controls detect missing hints, premature limits, and stale terminal metadata. Query-text fingerprints may change with emitted query text; semantic plan/proof inputs and related query/bind fingerprints are checked separately. This change makes no performance claim.

## PR160 integration: backend and hygiene retirements

WP-17 adapted the historical keyword-Pivot workflow and source inventory before the hygiene branch retired their entire runner/helper family. The integrated tree follows that retirement: the earlier WP-17 statements that these scripts remain are historical, superseded here. The current Make/CI entrypoints use direct product tests and e2e; the old registry is not restored with missing imports.

Retired specifically: the old keyword-named Pivot browser script's native duplicate-name gate, rename, exact Cancel identity, Apply/grid/reload/removal witness; the source-pinned affordance inventory/normalizer/verifier; the offline ExplorerConfigV2 JSON-to-workspace converter (including current semantics and default Records output); and the captured-preview-proposal artifact command plus closure-manifest wrapper. The stored-workspace migration CLI is a different tool and is retained with all WP-17/18 real-Arango cases; it does not replace offline JSON import. Existing compiler keyword/auth/NULL-MISSING tests, category EXPLAIN checks, publication/catalog tests, and ordinary migrated-draft Pivot/Unpivot lifecycle are retained or rerun. No claim that generic e2e replaces the exact retired keyword edge cases or artifact capture workflow. Recover the adapted tools with `git show 9a3cd558c:<path>`.
