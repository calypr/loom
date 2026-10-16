# Explorer dataframe backlog

Current scheduling lives in [BUILDER_WORKLIST.md](BUILDER_WORKLIST.md) and its [queue](BUILDER_WORKLIST.tsv). The dated priorities and results below are retained history, not the active execution order.

## Active goal verification workflow

For each changed UI feature, identify its user path and expected visible result. Use the relevant verifier registry case, creating one if missing. Run it against a stable source checkpoint and independent fixture data; retain the report and source fingerprint. Diagnose and fix failures before rerunning the same case. Keep the changed path mandatory and target roughly 20% of routine work time for verification. If setup or diagnosis exceeds that budget, record the gap and continue implementation without claiming a pass. Add related cases only for a concrete shared risk. Use real CDA follow-up after the basic browser path passes, prioritizing changes to data correctness, composition, and performance at scale; reserve broad sweeps for CI and coverage milestones.

This workflow changes how the active Builder reliability goal is executed. It does not reduce its feature inventory, lifecycle requirements, or completion criteria. Use the already-updated verification skill and the handoff verifier registry; do not rewrite that skill or treat historical reports as evidence for changed source.


The product goal is a researcher-facing workflow that turns nested, sparse
FHIR data into understandable, reproducible training tables. Scalar-shaped
columns alone do not establish that a dataset is suitable for machine learning.

Use the [local development workflow](LOCAL_DEVELOPMENT.md) to implement each
slice. Extend its real browser scenario with the expected user outcome, then
run it against the live backend. Improve the harness when a feature needs it;
do not make further harness polish a prerequisite for product work.

## Delivery order

### Preserve editable Pivot names that are AQL keywords

The real-Arango Pivot fixture exposed an unquoted attribute path for the
physical output name `null`. Source review confirms Builder can generate that
name from a string category or accept it in the output-name editor. Fix the
renderer to encode path segments safely rather than forbidding valid user
names. Add a native `.mjs` case that names a Pivot output `null`, verifies
Preview/Apply/reload and exact values, and restores the source table after
removal. Backend fixture renaming only unblocked the separate lineage tests;
it does not resolve this product defect.

### Restore joins and appends after the current bug pass

User requested on 2026-10-03: the currently disabled Join and Append controls
are temporary. Restore their complete workflows, then run another bounded
verification wave using owned QA Explorers and independent source oracles.
Verify join key matching, unmatched rows, duplicate keys and multiplicity;
verify append column alignment, types, nulls and membership. Exercise native
Preview/Cancel/Apply, editing, removal, reload, and composition with existing
row operations within five seconds. Preserve authorization, project and
generation scope. Disabled controls do not count as completed coverage.

### Join and Append restoration prerequisites

2026-10-04 implementation checkpoint: the reviewed published-version Combine editor is now wired into production. “New table from published versions” creates a separate empty rooted target and preserves the source table. Configuration requests previews automatically; editing and catalog pagination are wired. The staged focused suite passed 52 tests, and the integrated local API/frontend are healthy. Native Join/Append lifecycle evidence is still pending, so matrix rows 101–102 remain untested. This is intermediate published-input support: current-draft capture without a publication detour and absent-schema null padding remain required gaps.

Earlier pre-restoration baseline (superseded for UI wiring, retained for backend gaps): both were unverified and disabled (matrix rows 101–102). Source review confirmed
`ConstructionCombineEditor` has no production caller, while the operations panel
marks `MATCH_COLUMNS` and `APPEND_ROWS` unavailable. Existing combine contracts
accept pinned `TABLE_REVISION` inputs. Source trace confirms the input catalog
lists successful published executions with ready materializations, and runtime
resolution requires that exact immutable table/revision/output identity in the
same project and generation. A current draft is not an input. Candidate combine
results can be previewed directly without publishing the combined result.
Restoration therefore needs server-owned immutable capture or an equivalent
private-artifact boundary for current draft inputs before wiring the editor;
a UI-only restore would force users to publish first or use stale saved data.
Owners: `internal/server/construction_inputs_catalog.go`,
`internal/explorer/lifecycle/construction_proposal.go`, and
`internal/server/recipe_combine_resolution.go`.

Use an independent join fixture with two left and two right rows sharing one
key, plus an unmatched row on each side: `PRESERVE_ALL` LEFT must return four
matching pairs and the unmatched left row; INNER must return the four pairs.
Assert exact source identities and multiplicity. Append must verify aligned
values, nullable values, and missing input columns. Current projection validation
requires every input/output mapping, so absent-schema null padding needs a backend
contract change; do not claim that existing nullable-value support covers it.
Keep publication prerequisites out of the user path where they can be handled
as part of the operation.

### Recoded explicit-cohort member values: runtime fix integrated

Root review found and fixed the composed cohort bind-variable collision in the
staged patch before integration. Scalar string category recoding now happens per
member before ALL/ONE uniqueness reduction; composed cohort expressions use
independent namespaced binds. Focused lowering/authoring/IR tests and 34
PreviewTable tests pass. The integrated Arango regression also passes, including
ALL, ONE, unknown-value errors, empty cohorts, and restricted empty scope:

```bash
docker exec -e LOOM_TEST_ARANGO_URL=http://arangodb:8529 \
-e LOOM_TEST_ARANGO_DATABASE=loom_dev loom-dev-6d7df93d6a37-loom-api-1 \
go test ./internal/dataframe/compiler \
-run '^TestExplicitCohortMemberValuesAgainstArango$' -count=1 -v
```

The native CDA transformed-category lifecycle now passes in
`/tmp/loom-cda-cohort-recode-native-accessible-close/report.json`:45 timed checks,
maximum2259ms, errors[], unchanged1059-file source and fresh API build. Native
Save/Remove, ALL→ONE→ALL, reload, independent member values, and raw ONE
disagreement preservation pass. The Advanced source setup navigation detour
remains open; ordinary column access is a separate improvement.

### First-table Add columns availability fails before current Preview

The explicit local `verify-fast` run against `loom-dev-6d7df93d6a37` recorded a product-level control-state failure in the owned Owner Records Explorer. At source snapshot 76d635, Add columns became enabled in four DOM mutation events before the current draft had an accepted Preview (1921, 2063, 2229, and 2399 ms; `draftVersion=2`, `previewDraftVersion=2`, `acceptedCurrentPreview=false`). The observer expected zero and failed with four. It also retained the positive path: the control was enabled after the current Preview was accepted at 2490/2493 ms and in the final snapshot at 2502 ms. Evidence: `/private/tmp/loom-construction-implementation/.artifacts/loom-dev/c89a69d7e137/mut7md9h-1e62c9d5/report.json` and `/tmp/loom-first-table-preview-window-red.log`; the exact explicit environment and `node scripts/loom-dev.mjs verify-fast` invocation is in matrix row 114. Fix the readiness gate and rerun from a fresh owned fixture. Result correctness, persistence, and action performance remain untested by this observer.

### Registered Recompile recovery case fails

The registered `builder-controls / recompile` case is RED in `/tmp/loom-basic-recompile-76d635.json.recompile`. Recompile appears after the intentionally injected compilation failure, but clicking it issues no second reconcile request. The required backend-compiler invocation, successful compile response, and Preview of both independent fixture Patients are missing. The visible actions render in 216/446/867 ms, all within five seconds; the source fingerprint remains unchanged at 1120 files. The 422 reconcile response is the injected fault, and the favicon 404 is incidental asset noise. Recompile recovery correctness and persistence are untested; the matrix keeps this separate from the first-table availability failure.

### Active Builder regression: related Observation quantity Pivot

User-reported on 2026-10-02 in “CDA indirect route QA 1790619839496”:
Reshape → Turn categories into columns, grouping by
`Observation.valueCodeableConcept.text`, category
`Observation.valueQuantity.code`, values
`Observation.valueQuantity.value` (decimal). Category discovery reports
“finding all category values exceeded the preview time limit; filter the
source rows and try again” and offers “Retry finding categories”.

Prioritize reproducing the existing table's route and operation sequence in a
fresh owned QA Explorer, then add a permanent `.mjs` browser regression.
Investigate discovery query scope, traversal work, and category enumeration;
fix the backend cause rather than requiring users to filter or retry.
Verify discovered categories and decimal values against independent CDA
records, discovery/action-to-render within five seconds, and native
Preview/Cancel/Apply/edit/removal/reload with restoration. Do not mutate the
reported Explorer. Existing Pivot discovery and wide-Pivot reload passes do
not close this case.

Also review the duplicate “Turn categories into columns” heading and explain
or resolve disabled grouping choices. Track these presentation issues
separately from the blocking discovery timeout. Runtime reproduction and a
runnable regression are pending; the screenshot is user-report evidence.

| Priority | User outcome | Current gap | Live acceptance case |
| --- | --- | --- | --- |
| 1 | Know when joining related records loses values | Scalar child fields can use `FIRST`, while the contract still reports lossless and ML-ready | Give one Patient two Observations. Selecting their value must not silently publish a supposedly lossless table that drops one. Explain the ambiguity and require an explicit policy, or clearly report the loss. |
| 2 | Choose how repeated values become columns | Builder submits the candidate's default projection without a visible choice | Choose between separate scalar columns, an array, and an explicitly lossy first value. Preview and the contract must agree with that choice. |
| 3 | Create meaningful derived features | Backend types include aggregates and typed lookups, but Builder commands/controls mainly create field selections | Author a related-record count through the UI, publish it, and verify the exact count in Viewer and CSV. Expand later to concept, unit, and time-window rules. |
| 4 | Judge coverage before training | Preview shows null cells without feature-level missingness or cohort effects | Show populated/total rows and missingness for each chosen feature, including an all-null feature and the effect of a filter. |
| 5 | Understand and reproduce an exported table | CSV uses physical names; paginated export is not visibly pinned to one publication | Export data with a manifest mapping names, meanings, types, null rules, filters, generation, and receipt. Verify all pages use the same publication. |

## First slice

Start with priority 1. The UI should explain the consequence in researcher
language, for example, "Some patients have more than one observation. Choose
how those observations become a feature." Detailed FHIR paths belong in an
expandable explanation, not in the only actionable message.

The next fixture must contain multiple related records. The existing fast
fixture intentionally has one Observation per Patient, so its green result
does not prove that this gap is fixed.

## Source evidence

- [Child-field rendering](../internal/dataframe/compiler/render/aql/navigation_render.go)
  reduces scalar child sets with `FIRST(FLATTEN(...))`.
- [Semantic compilation](../internal/explorer/compilation/semantic_compile.go)
  classifies indexed fields from their internal array boundaries; that does
  not prove one-to-one relationship cardinality.
- [Authoring commands](../internal/explorer/authoringv2/commands.go) and
  [column selection](../ui/packages/loom-ui/src/features/ExplorerBuilder/components/ColumnSelector.tsx)
  are the current field-authoring boundary.
- [The contract panel](../ui/packages/loom-ui/src/features/ExplorerBuilder/components/DataframeContractPanel.tsx)
  is the existing place to surface output shape and losslessness.
- [Viewer export](../ui/packages/loom-ui/src/api.ts) fetches pages and constructs
  CSV in the browser. [Dataframe resolution](../internal/api/graphql/graph/dataframe/service.go)
  resolves the current materialization for a request.

The multi-page republish risk is inferred from the current selector flow, not
reproduced by the synthetic browser scenario. Authentication, real-data scale,
charts, pagination, and richer clinical semantics remain separate coverage.

Direct ordinary Pivot native reserved-name regression now passes: `/tmp/loom-direct-pivot-null-name-complete-lifecycle/report.json`, 21 timed checks max2654ms. It sets physical output `null`, edits to `direct_status`, checks receipt-bound source inspection and typed row identity, and removes the Pivot with exact source restoration through reload. This closes the reserved-name case; composed Group/Pivot ownership remains separate.

### CI follow-up after aea7e66f

PR29 runs 37154827017 and 37154827315 reported separate Builder-to-Viewer and Group lifecycle failures. The isolated Builder-to-Viewer check reaches `create-observation-table-in-builder` but cannot find Create table. The Group lifecycle test intermittently cannot find Summary output label 1, while the local full 524-test suite passes. The summary initialization race was fixed in `cf30b9167`; the full 526-test UI suite passes. The current-control verifier was updated in `4438a68c`, including the J04 first-table flow and receipt freshness. Full Builder-to-Viewer acceptance remains unverified.

The earlier acceptance publication 400 came from seven legacy `source.aggregate.where` fixture columns. Migrating the fixture through the pinned catalog now lets publication succeed without weakening strict `AggregateSource.UnmarshalJSON` validation. The latest pinned-catalog acceptance snapshot, `efd26a4b`, built and started run `9ac8167df204be53`. Generation upload, Arango counts, 17-column and seven-predicate contract migration, Explorer publication, READY execution registry, 100-row ClickHouse materialization, and Viewer lookup all passed. The GraphQL acceptance check failed: `latest_collection_day` returned 0 non-null rows, expected 100. The run is a data-correctness failure, not a full acceptance pass. Its report is `/tmp/loom-acceptance-evidence/9ac8167df204be53/report.json`; its log is `/tmp/loom-pinned-catalog-snapshot-acceptance.log`. Temporary stack cleanup completed with exit code 0.

The follow-up acceptance diagnostic `c91095ed1c7a6953` also builds, uploads the fixture, migrates its contract, publishes the Explorer, reaches READY, materializes 100 rows, and reads the Viewer, but its GraphQL check fails: `days_to_death` has 0 non-null rows, expected 6. A bounded project- and generation-scoped raw Observation route query finds 98 `age`, 88 `diagnosticMethod`, and 6 `daysToDeath` source values. It also finds 3151 nested-route Observations and 2829 integer components; no overflow sentinel is hit. All five published Observation aggregates were NULL in that diagnostic, exposing a compiled aggregation/materialization defect at the time. The later full locked run below verifies the corrected path. Evidence: `/tmp/loom-acceptance-evidence/c91095ed1c7a6953/report.json`. Cleanup exited 0 and removed the run volumes.

Cohort upstream membership fix verified against real Arango: `TestPreCohortDropStagesExcludeMissingPinnedMembersAgainstArango` passes for related eligibility, EXCLUDE expansion, and DROP unpivot, including pinned missing members and restricted authorization. Evidence: `/tmp/loom-cohort-drop-integrated-arango.log`; lower, IR, and AQL package checks pass in `/tmp/loom-cohort-drop-integrated-tests.log`.

### Empty-cohort related traversal is fixed

The compiler now enumerates the exact contributor keys owned by each group and resolves those documents before applying scope and traversal checks. This prevents one group from inheriting another group's related records. The original real-Arango regression now passes in `/tmp/loom-keyset-empty-cohort-arango.log`. The earlier failure and query probe remain available in `/tmp/loom-cohort-filter-membership-corrected.log` and `/tmp/loom-empty-cohort-subquery-array-probe.log`; native red runs are `/tmp/loom-named-cohort-empty-group-native-ready/report.json` and `/tmp/loom-named-cohort-empty-all-native-red/report.json`.

Native COUNT and ALL lifecycles now preserve the empty group and return `0` and `[]`. Both use independent scoped Observation oracles and verify the typed member roster, revision-bound identity, Preview/Cancel/Apply, edit, removal, reload, and source/API guards. Evidence: `/tmp/loom-named-cohort-empty-count-keylookup/report.json` (18 timed checks, maximum 2333 ms) and `/tmp/loom-named-cohort-empty-all-keylookup/report.json` (18 checks, maximum 2271 ms). Real-Arango parity also passes for empty contributors and existing roots with zero matches across COUNT 0, PRESENCE false, and ALL [], in `/tmp/loom-keyset-all-zero-cases-arango.log`. Native nonempty-root zero-match and PRESENCE lifecycles, plus negative scope guards for typed key lookup, remain open; see row 104 in [the Builder verification matrix](BUILDER_VERIFICATION.tsv).

### Standalone cohort CellTrace returns saved values

The earlier receipt-bound trace failure in `/tmp/loom-cohort-cell-trace-real-output-current/report.json` came from the standalone `GROUP_ROWS` terminal lacking a typed CellTrace mapping. The compiler now maps that terminal. `/tmp/loom-cohort-cell-trace-all-value-native/report.json` verifies the saved and reloaded object row identity and `ALL` value `[Specimen]`; traces took 36 and 38 ms, and native actions peaked at 2329 ms. The run had no errors and passed source/API freeze checks. The test obtains row identity through native Builder row inspection, then inspects the cell through the receipt-bound API; it does not exercise a Builder cell action. Generic cell Explain is not exposed in draft PreviewTable. The published Viewer has a separate native Explain action, exercised by `scripts/loom-dev.mjs` J05 and J04, but those use publication evidence and do not prove draft-preview CellTrace. Exact contributor details now pass; field edit/removal trace states remain open. See row 105 in [the Builder verification matrix](BUILDER_VERIFICATION.tsv).

### Starting-collection edit beneath a cohort passes

Population mapping compiler and execution suites pass in /tmp/loom-population-mapping-corrected-tests.log, and the routed-cohort Arango regression passes in /tmp/loom-population-mapping-routed-corrected.log. The latest native report /tmp/loom-cohort-source-collection-effect-free/report.json passes 28 timed actions, maximum 1987 ms, with no errors and unchanged 1057-file source and local-cda-api build freezes. The native lifecycle changes the starting collection beneath the retained cohort member field and authored filter, saves and reloads the exact one-member scoped population, then removes the downstream filter, field, and cohort and restores the source table. Focused Builder UI tests pass 28/28 and TypeScript checking exits 0 in /tmp/loom-population-selection-integrated-tests.log and /tmp/loom-population-selection-integrated-typecheck.log. Other starting-collection routes and composed operations remain separate.

Exact cohort cell contributors now pass the native CDA lifecycle in `/tmp/loom-cohort-cell-trace-exact-tuples-native/report.json`: saved/reloaded ALL values and contributor tuples match raw FHIR payload values and scoped IDs; source/API guards pass, trace calls take 34/36 ms, and the slowest native action is 2390 ms. Fix and permanent verifier are committed as `53425f4e`. Native cell-trace controls, broader ONE/repeated-value cases, pagination beyond one page, and field edit/removal trace states remain open.

The earlier coded-group differential failure `/tmp/loom-coded-group-status-differential-context-diagnostic/compound-coded-qa-1791069973760.json` returned HTTP 409 because setup created the table (draft 0→1) and then issued its default-ID command (draft 1→2), making an in-flight configured-column-context request for draft 1 obsolete. That response arrived before any collection action or external API status change. The Builder now waits for first-table ID provisioning before resolving configured-column context; all 10 focused UI tests pass in `/tmp/loom-first-table-context-integrated-tests.log`. The corrected native differential passes in `/tmp/loom-coded-group-status-context-fixed/compound-coded-qa-1791071330817.json`: no failures, maximum timed action 1625 ms, exact two-Observation oracle, Preview/Apply/edit/removal/Cancel/reload, and unchanged source/API guards. The earlier 409 was a stale setup response; the clean rerun passes.

### Saved named-cohort member-field policy edit passes

`/tmp/loom-cohort-policy-edit-native-complete/report.json` passes 41 timed steps (maximum 2619 ms, no errors) with the API build and all 1057 watched source files unchanged. On the pinned two-Specimen cohort, native editing changes the same saved member field `ALL→ONE→ALL` while preserving its physical column, stable `columnId`, source binding, cohort revision, and population. The scalar `Specimen` and list `[Specimen]` values match the independent raw CDA oracle; Undo and reload restore the expected policy and value. A separate new `ONE` field previews, cancels, applies, reloads, removes, and reloads. A distinct-ID field correctly rejects ambiguous `ONE` with `CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES` and a visible “Keep All unique values” message; the saved `ALL` draft identity, source, and population remain unchanged through reload. See row 108 in [the Builder verification matrix](BUILDER_VERIFICATION.tsv).

The saved and reloaded cell contributor tuples were checked through receipt-bound API CellTrace calls after native row inspection. This does not test native cell-trace controls, which remain open.

### Named-cohort member-field ONE creation and removal pass

`/tmp/loom-cohort-one-policy-native-verified-removal/report.json` passes 20 timed steps (maximum 2911 ms, no errors) with the API build and all 1052 watched source files unchanged. The native flow previews, cancels, applies, and reloads a duplicate-label `ONE` Resource Type field beside a retained `ALL` field. The independently scoped raw CDA oracle contains exactly two Specimen IDs; the saved ONE scalar and its receipt-bound contributor tuples match, while the ALL binding and `[Specimen]` value remain unchanged. Removing ONE and reloading preserves ALL. This verifies a new ONE field lifecycle; saved ONE↔ALL editing now passes in matrix row 108. CellTrace assertions use the receipt-bound API, so native cell-trace controls remain open.

### Locked NCPI publication passes the full acceptance run

The complete Docker Compose run `8c302d38bf076209` now passes, including the 17-column public contract and native Viewer/Builder checks. Its physical materialization and GraphQL output contain 100 rows; the scoped raw CDA oracle agrees on non-null counts: age 98, diagnostic method 88, days to death 6, earliest and latest collection day 100 each, and tumor and normal specimen counts 100 each. The optimizer target-bind restoration and receipt-metadata public-order fixes are both covered by this run. Cleanup exited 0 and removed the acceptance volumes. Evidence: `/tmp/loom-acceptance-evidence/8c302d38bf076209/report.json`, `/tmp/loom-acceptance-metadata-order-fixed.log`, and `/tmp/loom-acceptance-evidence/8c302d38bf076209/wrapper-cleanup.json`.

This resolves the earlier `c91095ed1c7a6953` aggregate-null diagnostic and the `f279da963898172e` enclosing column-order failure. The locked NCPI variant is separate from the expanded-component publication pass in matrix row 84; preserve those as distinct fixture results.

### Mixed-sibling related COUNT native lifecycle passes

The latest native verifier /tmp/loom-mixed-sibling-count-native-cancel-complete/report.json passes 15 timed checks (maximum 3327 ms), with no errors and unchanged source/API build freezes across 1057 watched files. In an owned Explorer, a ready Condition COUNT proposal is previewed and canceled; the saved workspace, draft version, and digest remain unchanged, and reload restores the baseline table. The flow then applies Condition, Observation, and Specimen COUNTs. Independently scoped raw CDA counts match exactly: 1, 2, and 2. Reload preserves the values; three native removals and a final reload restore the baseline table. The final workspace comparison allows only deterministic source columnId and empty construction (steps: [], version 1) canonicalization before full equality passes.

This verifies the `RELATED_SOURCE` StageSequence native lifecycle only. It is separate from the passing legacy top-level `PhysicalSet` SourceAggregate lifecycle below. The 17 Go tests do not substitute for native StageSequence coverage, and Group/Pivot composition remains separate.

### Legacy SourceAggregate sibling counts pass at the compact viewport

The first native run, `/tmp/loom-sourceaggregate-mixed-sibling-native/report.json`, failed at 756x413 before it sent the Specimen command. The visible `Show orphans` label overlapped the enabled Specimen graph node and intercepted the click. The root fix in `GuidedGraphWorkspace` moved the checkbox from an absolutely positioned graph-viewport overlay into the graph header controls. The rerun passes in `/tmp/loom-sourceaggregate-mixed-sibling-native-complete/report.json`, giving a direct RED-to-GREEN result at the same compact viewport.

The native report passes all nine timed checks with a maximum of 2144 ms and no errors. A separate project- and generation-scoped raw FHIR query finds the selected Patient's distinct related IDs and counts: Condition 1, Observation 2, and Specimen 2. The native previews match those values. Reload retains all three legacy top-level `SourceAggregate` COUNT columns; removing them and reloading restores the exact original Patient workspace, population, route bindings, source column, and rendered row. The owned Explorer guard, protected-Explorer guard, source freeze, and API build freeze pass. The driver issues direct add/remove commands and reads Preview results; it does not exercise a staged Preview/Cancel/Apply transaction. This `PhysicalSet` optimizer case is distinct from the `RELATED_SOURCE` StageSequence lifecycle in matrix row 110. Group/Pivot SourceAggregate behavior remains open; see row 112 in [the Builder verification matrix](BUILDER_VERIFICATION.tsv).

### Patient resourceType ALL after Unpivot passes

LOOM_RELATED_AFTER_UNPIVOT_CASE=resource-type-all node scripts/verify-ui/workflows/verify-cda-related-field-after-unpivot-browser.mjs /tmp/loom-related-resource-type-after-unpivot-native-complete passes 31 timed checks (maximum 2074 ms, no errors). Its independent raw CDA oracle is scoped to project loom_dev_cda_fhir and generation cda-fhir-v1; it selects one Specimen with exactly one subject_Patient link and verifies that the Patient resourceType is Patient. The native flow adds Patient id with ALL, unpivots Specimen ID while retaining the Patient ID array, then adds Patient resourceType with ALL on the same route. Preview, Cancel, Apply, reload, label edit, field removal, and Unpivot removal/reload all pass; ["Patient"] and the retained ID array match the raw source. The source freeze found 1057 files unchanged. The report has no API build-freeze result and makes no restricted-auth claim.

The bounded first-2,000-Specimen scan found one valid resourceType witness. It does not establish project-wide availability or absence. At this historical checkpoint, `/tmp/loom-related-field-after-unpivot-execute-file-current/report.json` found no populated gender witness and did not reach Builder behavior. A later exact missing-gender witness and failed post-Unpivot chooser attempt are recorded in the 2026-10-04 source-witness update below. Populated-gender behavior, restricted-auth, and Pivot compositions remain untested. See matrix row139 for the missing/null attempt.

First-table readiness regression baseline (2026-10-03): registered
`builder-controls --case first-table` is integrated. The corrected driver ran
against owned stack `loom-dev-6d7df93d6a37` with unchanged source and reports
`/tmp/loom-first-table-basic-red-fixed-driver.json.first-table`. It recorded six
Add-columns enable events before current-draft preview acceptance; verified ID,
independent Patient rows, native editor open/close, and timing assertions passed.
There were no scenario errors. This remains FAILED until the staged automatic
preview owner is integrated and the same browser case passes. Automatic preview
is retained; the patch replaces its effect/timer ownership.

Automatic-preview ownership unit integrated (2026-10-03): automatic rendering is
retained, with keyed query ownership replacing the effect/timer. Recompile bumps
an explicit request epoch; captured requests cancel on superseding edits and
stale results cannot become accepted previews. Add columns waits for the current
accepted preview without blocking Filter rows. Both registered first-table and
Recompile browser regressions now pass; reports and source fingerprint are in
the matrix. Native first-table action took 924 ms and editor open/close 265/267 ms.
CDA follow-up and broader save/reload remain open; this does not close the goal.

CDA preview-owner follow-up passes:
`LOOM_COHORT_SOURCE_COLLECTION_CHANGE=1 node scripts/verify-ui/workflows/verify-cda-cohort-membership-revision-browser.mjs /tmp/loom-cohort-source-collection-preview-owner-green`.
The report records 28 timed steps, max1890ms, errors[], independent exact-project /
generation raw Specimen membership, unchanged1057-file source freeze and fresh
unchanged API build. Cohort Apply/Cancel/revision replacement, collection repair
under the field/filter, reload, downstream removal and restoration pass. This is
the concrete shared-preview CDA risk check; unrelated feature gaps remain open.

Coverage ledger after preview-owner integration: the current matrix has 126
case rows: 116 overall `passed`, 7 `untested`, 3 `partial`, 0 `failed`, and 0
`unsupported`. These are matrix cases rather than unique feature families. All
four dimensions are `passed` for 110 rows; 16 retain a dimension gap. Fifteen
have at least one `untested` dimension, and the remaining row has an `unproven`
dimension. Do not infer
whole-feature closure from an overall `passed` row. The real CDA recoded-cohort
lifecycle is now recorded as passed in matrix row 121, while Join and Append
restoration remain untested in rows 102–103 and other declared variants remain
open.

Join/Append backend restoration source follow-up: existing private ClickHouse
capture is not a production draft-input path. `execution/clickhouse_artifact.go`
requires at least one exact published revision for `WithPrivateClickHouseArtifact`;
its helper and `chartifact.New` have test callsites only. The composite lowering
seam accepts one unbounded AQL prefix plus exact published inputs and explicitly
rejects grouped prefixes until grouping preserves authorization scope. Restoring
controls alone would therefore fail both draft-only and grouped-input workflows.
A complete fix needs exact immutable draft/output references, full-source capture
with workload bounds, N private input leases held through execution, scope /
project / generation revalidation, and cleanup without publication pointer
changes. Same-workspace and cross-Explorer inputs must be defined before wiring
the editor. Existing published references remain valid; no floating head or
preview-limited source may silently substitute for a complete table.

Current full-population quantity-category discovery RED:
`LOOM_QUANTITY_FULLPOP=1 node scripts/verify-ui/workflows/verify-cda-quantity-pivot-native-drag-browser.mjs /tmp/loom-quantity-fullpop-category-baseline-current`.
HTTP503 CATEGORY_SCAN_TIMEOUT after8277ms; native action-to-render8815ms;
unchanged1057-file source. Independent complete scoped route oracle is built
into this case but is not reached on failure. This is one run, with no causal
speedup claim or API-build stamp proof. Related-category renderer rescans the
target collection per distinct category; index preparation also shares the8s
scan timeout. A stage worker owns a single-terminal-scan rewrite and exact
Arango-equivalence tests; attribution and native rerun remain required.

Category discovery follow-up: terminal-once renderer passes both real-Arango equivalence regressions (restricted root/edge/target scopes and preserved empty witnesses), but the same full-population native CDA case still returns CATEGORY_SCAN_TIMEOUT after 8831ms action-to-render. Evidence: `/tmp/loom-quantity-fullpop-terminal-once/report.json`, unchanged 1057 watched files. This is still failed; isolate receipt resolution, index preparation, and scan time before another performance change.

Registered cohort-recode basic case reached native explicit member-field proposal but exposed a new correctness failure: metadata has two members, rendered cohort member count is zero and Patient.id ALL is empty. Exact source IDs remain in lineage. Evidence `/tmp/loom-cohort-recode-basic-explicit-member.json.cohort-recode`. Investigate canonical versus legacy project identities at fixture/lookup boundary; do not weaken expected values or proceed to CDA recode until resolved. Existing API logs also attribute category timeout to main AQL execution (7.997s), after receipt compile54ms, in `/tmp/loom-category-terminal-once-api.log`.

Timeout attribution is now supported by owned API logs: source watcher rebuilt at 03:56:29 and started the server at 03:56:34; discovery receipt compile54ms, main AQL query `4f713faf3d70d8f3` began04:00:54.603 and failed04:01:02.600 (7.997s), with `execute category scan` timeout cause. This rules out index preparation as the dominant phase for this run. The terminal-first rewrite still needs an execution-plan comparison against the earlier domain-first witness strategy before acceptance.

Cohort zero-member root cause confirmed in source: selection refs and group metadata canonicalize project IDs, while resource validation queries the legacy storage alias. Runtime bindings carry both Project and SelectionProject, but group-row lowering binds only SelectionProject and direct/composed AQL resource lookup compares resource.project to the canonical member ref. Fix in progress: give resource lookups their own storage-project binding; retain canonical membership metadata and generation/auth predicates. The browser fixture expectations remain unchanged.

Performance hypothesis correction: the committed baseline renderer already uses domain-first distinct candidates, bounded reverse witnesses, and per-stage empty-parent witnesses. Since the baseline native case also timed out, restoring that shape alone is not a fix. Next evidence must compare exact compiled queries and index plans, including separate matched-domain and empty-witness costs; earlier manual prototype timings cannot establish that production is fixed.

E001 table-selection effect removal integrated with command/action-owned persistence. Focused creation, duplicate/manual selection, deletion fallback tests and TypeScript passed. Native registered tables case `/tmp/loom-selected-table-event-owner.json.tables` fails at rename because the verifier sets cdp.nextDialogResponse while launchBrowser ignores it; Chrome accepts the existing prompt text, so rename does not issue a command. Creation481ms, column Apply749ms, duplicate451ms passed. Fix the one-shot dialog response harness and rerun the same lifecycle; E001 remains open until native persistence proof.

E001 native closure: registered tables case passes after one-shot dialog repair (`/tmp/loom-selected-table-event-owner-dialog-fixed.json.tables`), all required checks,11timedactions,max1076ms,errors[], unchanged fingerprint5d8ab7552022b1006b866c78496e89b51104abd044c3b5afd6097320b61c6323/1120files. Exact fixture Patient identities/values, duplicate/manual selection restoration, selected-delete fallback, Explorer copy, and last-table deletion verified. Automatic preview remains enabled.

Repeated-empty registered native case now reproduces a product limitation: Expand disabled on a source-only table and instructs users to add a multiple-value column first (`/tmp/loom-repeated-empty-native-row-panel-fixed.json.repeated-empty`). Root source IDs independently verified; no Expand action dispatched. Investigate existing source-row choice versus authored compound source-field+Expand path, avoiding prerequisite user navigation. Full preserve/exclude/Cancel/reload/removal lifecycle remains untested until usable.

Exact query phase profile `/tmp/loom-category-exact-phase-profile.json` compares fresh baseline/candidate captures from the same saved receipt with identical15binds and unchanged indexes. Baseline full6.22s/matched-only1.30s return matching NULL+string fingerprints; candidate full/matched-only kill at12s. Baseline both scanned821261 index entries and materialized821261 documents. Forced-empty witnesses kill12s, but cannot attribute full-query cost to them: baseline full has the same scan/lookup counts and NULL already suppresses witnesses; sequential warm-cache effects confound timing. Investigate category index coverage of the resourceType guard before another fix. Unsuccessful terminal-first rewrite is queued for revert while retaining real-Arango correctness regressions.

Exact baseline EXPLAIN confirms Observation candidate index coverage is false (`/tmp/loom-category-baseline-exact-explain.json`, IndexNode143): the chosen category index has project/generation/category/auth fields and excludes resourceType, while the query retains its type guard. Terminal node146 is also non-covering. A reversible additional covering-index experiment is being prepared; no existing index or type predicate will be removed.

Category discovery attribution follow-up: `/tmp/loom-category-domain-first-selftime.json` profiles the same captured domain-first query twice with equal binds and unchanged indexes. Full warmed database runs took 1.752s and 1.234s; matched-only took 1.208s twice. The candidate Observation IndexNode performed 815,262 items with 1.445s self time, and the full query recorded 821,261 document lookups. Forced empty-witness phases exceeded eight seconds. This is cost attribution, not a controlled speedup or native browser pass; the full-population browser regression remains failed. Next experiment must preserve resource type, project, generation and authorization and include index preparation in end-to-end timing.

Cohort project-binding browser follow-up: `/tmp/loom-cohort-recode-project-binding.json.cohort-recode` ran against unchanged source fingerprint `2f5346866514dd0b9eb2083c233254afb2e79e8932f798b39073ea4acaf908a9` (1120 files). The native Patient.id ALL proposal now contains both literal fixture Patient IDs and Apply completes in 675ms. The lifecycle remains failed: the script waits for a separate `/preview` response after Apply, while the observed command and reconcile return 200 and no such request follows. Investigate current receipt-preview reuse and actual rendered state; do not force an extra Preview action or mark recode/reload passed.

Cohort alias backend regression: after correcting private fixtures to reversible `LOOM_COHORT/<project-with-uuid>` identities, the root ran `docker exec -e LOOM_TEST_ARANGO_URL=http://arangodb:8529 -e LOOM_TEST_ARANGO_DATABASE=loom_dev loom-dev-6d7df93d6a37-loom-api-1 go test ./internal/dataframe/compiler -run 'TestRelatedCountAppendPreservesExplicitCohortMembersAgainstArango|TestExplicitCohortMemberValuesAgainstArango' -count=1 -v`: PASS, 0.186s. This exercises direct/composed cohort values, retained/post-cohort filters, ALL/ONE recoding, unknown-value errors and restricted empty scope. Native recode lifecycle remains open.

Category covering-index experiment: `/tmp/loom-category-5field-index-experiment.json` changes only the captured query index hints, retaining both resourceType guards and all exact binds. Temporary five-field index build took 881ms; query took 343ms with 6000 document lookups versus 1240ms/821261 lookups for the four-field query. Both returned two values (NULL and string) with fingerprint `c1dffe3a`. Only the owned temporary index was dropped; index inventory restoration passed. This single-run directional experiment supports a production patch, not a benchmark speedup claim or browser closure.

Category index integration constraint: root read `EnsurePreviewCoveringIndex` cap4 and `ScanCategoriesCompiled` ignoring non-context preparation errors, then queried owned Observation indexes. All four slots are occupied, including the superseded four-field quantity-code index `loom_pivot_preview_e358392c38a61a57`. A five-field compiler spec alone will silently fall back and is insufficient. Implement a bounded migration/replacement of the superseded same-category index with explicit ownership and in-flight nonforcing-hint compatibility; do not blindly increase the cap or delete unrelated indexes. Native regression must include the existing-index state.

Selected category migration design: only at cap, match the superseded four-field same-path index by exact compiler-derived name and fields, create and verify the new five-field index before removing that exact old index. Failed creation must preserve the old index; unrelated indexes remain untouched; final cap stays four. Existing queries use nonforcing hints and preserve semantics during replacement. Generic root category scans also emit the old shape, so performance compatibility is a concrete shared risk requiring source inspection and a targeted root-category check before acceptance.

Automatic proposal preview source finding: `BuilderWorkspace.tsx` reuses the applied construction-choice proposal preview when receipt ID, snapshot, output, limit, owner and candidate workspace digest match (`matchesAcceptedChoicePreview`, receipt `intentDigest`). A separate `/preview` request is therefore not required after this Apply. The cohort verifier must recognize that exact accepted proposal receipt and verify visible current table cells rather than require a redundant request.

Repeated-value source expansion design: expose signed source repeated scopes alongside existing scalar list columns in Expand. Existing row proposal changes only `Document.Rows`, retains authored steps/column IDs, and compiles source expansion before all authored operations; preview must communicate that ordering and preserve Apply/Cancel. Object-valued Observation.component[] is supported by this source path, while authored scalar-column EXPAND cannot represent it. Mid-sequence object expansion and coexistence with a GROUPS row source remain explicit contract gaps, not closed by this UI change. Native repeated-empty regression must assert actual source shape and exact alpha/beta/empty/missing identities, reload, policy edit, Cancel and removal.

Partial mapped collection repair now passes: `LOOM_COLLECTION_PARTIAL_LONG_ROUTE=1 node scripts/verify-ui/workflows/verify-cda-collection-repair.mjs /tmp/loom-partial-long-route-normalized`. Independent scoped CDA oracle, native exact exclusion, surviving membership/Observation row, unchanged exact saved route and reload all pass; max2525ms, no errors, unchanged source. The bounded CDA prefix supplies one output Observation; multi-result and restricted-auth evidence remain separate.

Related category discovery native GREEN: `/tmp/loom-quantity-fullpop-type-index/report.json` passes fullpopulation discovery in2066ms and the complete independent raw scoped NULL+d oracle (815261Observations). Sourcefreeze unchanged1057files, pre-run APIbuildstamp matchescurrent. Root queried index inventory afterward: exact old fourfield quantity-code index replaced by `loom_pivot_preview_601548896a95cbf8`, remaining three preview indexes preserved/cap4. This closes discovery timeout, not Pivot lifecycle or generic root-category performance follow-up.

Recoding navigation rough edge reproduced: `/tmp/loom-cohort-recode-native-feature-editor.json.cohort-recode` reaches correct raw ALL values, but the configured-field recode editor is under collapsed `[data-testid=construction-source-setup]` (Advanced source setup). PreviewTable Columns exposes member-value policies but no recoding control. First finish the real existing native lifecycle; then expose recoding from ordinary table/column controls without forcing a source-setup detour. Do not treat this navigation gap as backend unsupported behavior.

Effect-free recoding editor follow-up: `/tmp/loom-cohort-recode-effect-free.json.cohort-recode` passes the registered basic fixture lifecycle with all required checks, no page errors, automatic preview, ALL→ONE→ALL, reload, and removal restoring both literal Patient IDs. Timed actions peak at 873ms. Exact-category drafts now preserve unsaved input across unrelated rerenders and reset on saved transformation or column identity changes without an effect. Real CDA follow-up `/tmp/loom-cda-cohort-recode-effect-free/report.json` passed 37 preceding membership/policy/inspection actions (maximum 2181ms) but failed locating Advanced source setup before recoding. This remains a harness/navigation investigation, not a CDA recode pass. Ordinary column access is being implemented separately.

Root Observation quantity category gap: the full scoped CDA oracle in `/tmp/loom-root-quantity-category-post-migration-fixed-oracle/report.json` contains absent `valueQuantity.code` fields. The initial verifier incorrectly required every source record to contain the field and stopped before browser discovery. Source inspection finds an actual declared backend gap in `internal/explorer/lifecycle/construction_category_discovery.go`: materialized Pivot inputs preserve values but lose property presence, so discovery returns MISSING_UNSUPPORTED to avoid conflating absent fields with explicit NULL. Preserve independent missing/NULL counts in the regression, drive the actual browser outcome, and implement presence-aware Pivot categories; filtering to a lucky populated subset does not close this feature. Root-category index migration performance remains unverified until its native query is driven.

Ordinary Columns recoding now passes the registered basic fixture lifecycle in `/tmp/loom-cohort-recode-ordinary-columns.json.cohort-recode`: all required checks, errors[], maximum timed action690ms. Advanced source setup stays closed throughout; Save/Remove, ALL→ONE→ALL, reload and exact raw ID restoration use the same command ownership. CDA follow-up through these shorter controls remains pending; the prior configured-field CDA lifecycle already passes separately.

Root Observation Pivot entry fails natively in `/tmp/loom-root-quantity-category-native-generic-pivot/report.json`: the ID-only table offers an enabled category-to-columns action while other row capabilities are still checking. After clicking, no field editor renders; the eventual capabilities response declares ordinary PIVOT unsupported for insufficient existing scalar columns and CODED_PIVOT supported. The generic alternative is absent at click time. Source-backed Pivot should choose its group/category/value fields within the operation, without a prerequisite Add columns sequence. Investigate both readiness and that availability boundary; category query performance remains untested because no discovery request was made.

Recoding navigation rough edge closed: `/tmp/loom-cda-cohort-recode-menu-contained-select/report.json` passes all45 timed checks through ordinary Columns with Advanced source setup closed, max2287ms/errors[], unchanged1059-file source and fresh APIbuild. The basic counterpart passes at690ms maximum. Commit `f5f39b210` is pushed to the PR branch with the effect-free editor, scoped capability wiring, registered regression, native select dismissal fix and durable evidence in `docs/BUILDER_RECODE_VERIFICATION.md`. Other Builder failures remain open.

Pivot entry diagnosis corrected by the complete capability response: `pivotSourceInput.supported=true` already exposes ten root source choices, including ID and quantity code/value. `BuilderWorkspace` uses that capability correctly for ordinary Pivot. `RowDefinitionSettingsPanel` nevertheless leaves the main Pivot button enabled while its capabilities are loading, allowing an early action before the generic alternative is rendered. Fix that readiness boundary and retain the existing source-backed field workflow; no prerequisite columns or new availability contract are needed.

Direct source expansion browser checkpoint: `/tmp/loom-repeated-source-direct-rows.json.repeated-empty` verifies Configure rows opens the signed repeated-field chooser directly; selecting component[] automatically previews 3→4 rows, Cancel preserves the original three IDs, and Apply preserves both item occurrences plus literal-empty and missing parents. These actions take at most 645 ms, with 1122 watched files unchanged. The full lifecycle is still failed: the driver waited for a construction preview while the native “Choose how to add these fields” route/form dialog was open. Correct the native dialog path and verify actual item values, policy edits, reload and restoration before claiming completion. No manual Preview control was introduced.

Group→Pivot source-inspection database proof: `docker exec -e LOOM_TEST_ARANGO_URL=http://loom-dev-6d7df93d6a37-arangodb-1:8529 -e LOOM_TEST_ARANGO_DATABASE=loom_dev -w /workspace loom-dev-6d7df93d6a37-loom-api-1 go test ./internal/dataframe/compiler -run '^TestConstructionCountRowsGroupPivotRowLineageMatchesScopedSourcePreimageAgainstArango$' -count=1 -v` passed (test0.05s/package0.059s). Exact scoped source contributors page2/2/1 without duplicates, missing/explicitNULL Group bucket, denied auth/foreign project/stale generation exclusions, forged owner and past-end pages, and full-scope unlisted category outside requestedpage all checked. Native CDA lifecycle remains untested; this first slice supports direct scalar COUNT_ROWS Group→ordinary Pivot only.

### Pivot NULL-only category preserves missing distinction

Review of the pending presence implementation found that retaining presence only when a MISSING heading is selected would conflate absent source properties with explicit null after MISSING is deselected. `groupedPivotCategoryMatchPredicate` uses `HAS` on materialized rows without the presence companion; materialized selectors emit the column key for both states. Keep this case open until a NULL-only regression proves that absent values follow the unlisted-category policy rather than entering the NULL cell. Preserve bounded preview planning for ordinary string categories. This is source evidence; no live pass is claimed.

Presence backend checkpoint: real Arango `TestRelatedConstructionPivotTracksMissingNullAndStringAgainstArango` passed (0.05s) after integration. Its independent scoped fixture proves separate STRING, explicit NULL, and MISSING categories and typed Pivot execution, with foreign-project, stale-generation and denied authorization negatives. This is database correctness evidence; CDA native discovery and lifecycle remain open.

CDA root quantity category checkpoint: `/tmp/loom-root-quantity-category-presence-integrated/report.json` now returns COMPLETE with exactly Missing and string d, matching the independent full-population815261-row oracle (778185missing,37076d,0explicitNULL). Native discovery-to-render8047ms and discovery7425ms fail the5second budget. The unavailable-category correctness defect is repaired, but this browser case remains failed for performance; full Pivot Apply/edit/remove lifecycle still needs coverage. Source/API freeze evidence is retained in the report.

Root-category performance diagnosis (2026-10-04): the exact compiler query and uncached Arango profile are retained in `/private/tmp/loom-category-current-compiler-query.json` and `/private/tmp/loom-category-current-profile.json`. The read-only query returns Missing and d, takes 8.458 seconds, and performs 1,630,522 document lookups across two scans of the scoped 815,261 records. Both branches select the unrelated value covering index instead of the requested four-field category index. The current index inventory contains the replacement five-field related-category index but no four-field root-category index; all four preview-index slots are occupied. API logs confirm `preview covering index unavailable` / `preview covering index limit reached`. Root and related discovery need a compatible index strategy; increasing the cap or deleting unrelated indexes is not an established fix. This query profile diagnoses the performance failure and does not close native browser lifecycle coverage.

Shared-index hypothesis check: `/private/tmp/loom-category-shared-index-profile.json` retains a read-only diagnostic with the existing five-field category index hint and an exact bound Observation resource-type predicate. Both branches select that index, the full scoped result remains Missing and d, and the single uncached profiled execution takes 1.772 seconds with 778,185 document lookups. This is a diagnostic observation, not a repeated benchmark or native action-to-render pass. Compiler integration must gate the optimization on the resource-specific ingestion contract, retain project/generation/authorization checks, and then rerun the native regression without reducing its population.

CDA authored cohort EXPAND follow-up: `LOOM_COHORT_FIELD=id LOOM_COHORT_AUTHORED_EXPAND=1 node scripts/verify-cda-cohort-fields-browser.mjs /tmp/loom-cda-cohort-authored-expand-native` reaches saved cohort creation, native member-field Apply, and reload with exact independently scoped Specimen IDs. The run then stops before EXPAND because new value-consuming `browserEval` calls omit an outer `return`; `browserEval` wraps its body in an async function, so those calls report undefined despite the visible Rows control. Source/API freeze and aggregate fingerprint are unchanged. This is a verifier defect, not a demonstrated unavailable EXPAND control. Correct the reads and rerun the full lifecycle before closing CDA coverage.

CDA authored cohort EXPAND lifecycle GREEN: `/tmp/loom-cda-cohort-authored-expand-normalized-document/report.json` passes all22 actions (max1838ms), exact independent Specimen IDs and cohort membership, automatic preview/Cancel/Apply, reload, saved-label edit, removal and source/member-field restoration. Source/API/fingerprint unchanged; errors[]. Earlier return/regex/header/document-normalization harness failures are superseded. This closes the equivalent CDA direct-entry lifecycle, not arbitrary expansion compositions or empty-list policies.

Root quantity category discovery GREEN: `/tmp/loom-root-quantity-category-shared-index-native/report.json` verifies full815261-row source scope, exact Missing+d, COMPLETE discovery, native render2448ms and endpoint1889.771ms. Source1064/API unchanged. The integrated compiler shares the existing type-ordered category index with related discovery, with exact collection/type guards and legacy replacement metadata. Scoped real Arango test `TestRootCategoryScanUsesSharedTypedIndexContractAgainstArango` passes (0.194s), including missing/NULL/string and foreign project/generation/auth/wrong-type exclusions. Matrix entry remains partial because full Pivot lifecycle and persistence are still untested.

2026-10-04 automatic preview error-state investigation: the latest `verify-fast` run (`.artifacts/loom-dev/c89a69d7e137/mutkdhta-1498b7b5`) reaches duplicate-ID-correct native preview, then commits the intended ONE-cardinality violation. Draft 16 visibly reports `RELATIONSHIP_CARDINALITY_VIOLATION`, but the table still displays “Loading your table…” and the verifier records an empty preview status/receipt/output identity. Investigate the UI terminal error state and the verifier DOM contract together; do not accept text-only error detection while loading remains unresolved. Browser acceptance remains failing.

Confirmed owner for the automatic-preview error defect: `BuilderWorkspace` catches preview failure, sets the alert, and returns `undefined`; `useAutomaticPreview` therefore observes successful empty data. Workspace status falls back to `empty`, while `PreviewTable` interprets every rooted/nonempty-column table without rows as loading. Fix the current-identity query error state and render a terminal table error; preserve cancellation and stale-request guards. The original cardinality regression remains the executable proof and must pass recovery back to MAX.

Typed root quantity Pivot basic lifecycle GREEN: `/tmp/loom-root-quantity-pivot-lifecycle-fingerprinted/report.json` passes discovery, exact expected duplicate ERROR validation and enabled SUM repair, automatic preview/Cancel/Apply/reload, SUM-to-MAX plus heading edit, removal and original-row restoration/reload. All12 timings <=1219ms, source/API/fixture/fingerprint unchanged, final unexpected errors empty. This closes the basic typed-category lifecycle; CDA full lifecycle is still required separately.

Direct standard GROUP automatic entry fixed: Rows selection creates the same empty-key COUNT_ROWS form and candidate once, owned by the click event. Registered `group-entry` browser case passes `/tmp/loom-group-entry-native/report.json.group-entry` with five required checks, exact independent two-Patient count2, max1071ms, source fingerprint recorded, no page/runtime errors. Incidental favicon404 recorded. This proves initial preview before any edit, not the empty-key saved lifecycle or CDA case.

Related Observation.status lifecycle follow-up: `/tmp/loom-basic-related-status-catalog-corrected/report.json` passed the initial native exact-membership reload (1249ms), then failed in the verifier at `expandedDoc.construction.steps`: the basic Patient-root fixture correctly has no authored expansion. Catalog assertions now resolve resource type through catalog nodes and validate the supported nested VALUE/SCALAR choice. The remaining fixture/CDA branch assumption is being corrected; this run does not establish lifecycle correctness. Automatic preview remains required.

Related Observation.status basic rerun: `/tmp/loom-basic-related-status-root-branch-fixed/report.json` passes seven native actions (max1134ms), including exact source reload, Group automatic preview, Cancel preservation, Apply and reload. It then times out selecting the expected related route in `openRelatedFieldChooser` (line332). The basic root/no-expansion verifier branch is fixed; route-selector versus missing native-option diagnosis remains open. This is partial evidence, not a completed related-field lifecycle.

Automatic preview terminal-error fix integrated: preview API failures now propagate to the draft-keyed query instead of becoming successful undefined data; same-key errors remain errors, previous-key results are absent, and a new draft automatically recovers. PreviewTable replaces its loading placeholder with the current failure, and the browser regression requires matching draft/output identity plus terminal error status. Focused hook/table tests40/40 and staged TypeScript check pass; integrated diagnostic verifier unit passes. Native verify-fast is running; runtime acceptance remains unproven until that run completes.

Automatic-preview native follow-up: `.artifacts/loom-dev/c89a69d7e137/mutl4l5y-37aa5bc7/report.json` passes the exact changed-draft terminal-error wait and subsequent automatic MAX recovery, then reaches publication and exact literal Viewer rows. The overall verify-fast run remains failed at `training-artifact-has-fixed-members`: expected `data.csv`, actual `data.jsonl`. Check the selected export format and manifest contract before changing that assertion. Add explicit named error/recovery evidence to the report; passing later steps currently proves those waits completed but is not a complete export pass.

Verifier coverage integrity: schema-v2 evidence now requires every current registry-required assertion to pass; missing checks classify partial and failed required checks classify failed, even when the report summary says passed. Reuses registry requiredChecksFor; historical four-dimension evidence remains supported. Focused tests6/6 pass. Root checked actual `/tmp/loom-group-entry-native/report.json.group-entry`: registered entry remains passed; removing its first required assertion makes the same evidence partial. This prevents standalone report metadata from overclaiming lifecycle coverage.

Related status harness contract correction: `/tmp/loom-basic-related-status-choice-output-labels-fixed/report.json` again passes seven Group actions (max1215ms) and now selects the exact route. It times out waiting for the legacy constructionChoices request, while the captured native200 proposal carries candidateConstruction with RELATED_SOURCE. Chooser title Status and generated output Observation status are independently confirmed. Stop selector-only retries: status lifecycle assertions must match the current RELATED_SOURCE and Group row-value proposal/acceptance shapes before another native run. This is a verifier failure; no related-source product failure is established by this run.

Export/recovery verifier correction integrated: inspectJ05ArtifactPackage validates the artifact manifest and parses its selected CSV or JSONL representation; verify-fast retains exact two Patient ID columns, literal memberships, stable row identities, published revision/generation, schema and checksums. Explicit named ONE terminal-error and COUNT automatic-recovery assertions now include observed snapshot/draft/output/receipt witnesses. Four focused worker Node tests and captured JSONL ZIP inspection pass. The native rerun is in progress; overall export acceptance remains unproven until terminal evidence.

Native verify-fast GREEN after terminal-error/export corrections: `.artifacts/loom-dev/c89a69d7e137/mutll1te-03294344/report.json` passes97 assertions, including named exact-output ONE error on saved draft16, automatic COUNT recovery on saved draft17 with current receipt, exact final preview/publication/Viewer rows, manifest-selected artifact data, schema/membership/checksums and Viewer reload. Overall runtime39433ms; aggregate workflow timings do not establish each action <=5s. This is basic-fixture evidence, not full CDA lifecycle.

Related-field product defect found during harness repair: the status-mode native path selects ONE in the grouped-row policy chooser, then submits200 RELATED_SOURCE proposals with form ALL and unchanged GROUP/no rowValues. BuilderWorkspace addSelectedFeatures returns through buildRelatedSourceCandidate before the branch that consumes rowValuePolicy. The visible Require one distinct value control is therefore ignored on this path. Evidence `/tmp/loom-basic-related-status-choice-output-labels-fixed/report.json`; fix policy semantics and add multi-valued CDA regression rather than hiding the control or declaring ALL-only completion.

Full-CDA quantity lifecycle preflight remains in revision: the staged verifier incorrectly compared a bounded preview rowCount to the full raw population count (engine PreviewSummary.RowCount counts consumed rows up to25), and its restoration DOM oracle reused the backend response without raw-record verification. Correct the bounded count expectation and independently fetch scoped tuples for the25 displayed IDs before running. Full source scope/count and aggregate oracle remain separate evidence; no full-CDA lifecycle pass is claimed.

Full-CDA quantity lifecycle second preflight: the corrected candidate separates bounded preview cardinality from full raw scope and checks raw source tuples, but still references a fixture-local timing variable in the CDA removal path. Its reload raw check also reuses the removal response and IDs instead of validating the fresh automatic preview. Returned for correction with focused regressions; no native CDA lifecycle pass is claimed. Automatic preview remains required and is not being removed.

Published Combine verifier preflight: the complete corrected Join/Append driver now covers native empty-target creation, automatic proposal rendering, exact literal INNER/LEFT or stacked rows, Apply/Cancel/edit/removal/reload, and exact published revision inputs. Source-preservation checking still snapshots only selected document fields, omitting other semantic settings; require complete saved-source document comparison before native acceptance. This is a verifier correction, not a proven Combine product failure. Unpublished/grouped draft inputs and nullable duplicate-key Join remain open.

Published Combine first native run: `/tmp/loom-combine-join-native/report.json.join` fails before Join configuration on a harness assertion requiring nonexistent top-level Builder `explorerId`. Actual response retained at `/tmp/loom-combine-join-native/builder-after-publication.json`; it contains catalog scope/snapshot/generation and draft bindings, while workspace.explorer contains title only. Native creation of both source tables, raw field Apply and publication succeeded; all seven recorded timings <=645ms, pageErrors empty, source fingerprint unchanged. Correct exact owned route/response identity checks and rerun fresh. Join lifecycle remains unverified.

Draft Combine execution prerequisite integrated after complete review: ordered private capture supports an all-draft source set without published pins, binds each manifest and renderer expectation to exact output ID as well as stage/plan/schema, and keeps earlier capture leases live while later sources stream. Focused overlay checks passed in execution, chartifact, IR and ClickHouse rendering, including same-stage output substitution, one distinct captured source, context mismatch, later-source failure cleanup, cancellation and lease loss after a nil consumer result. This is internal infrastructure only: production workspace dependency resolution, grouped authorization proof, terminal Combine execution and native unpublished-source UI lifecycle remain open.

Full CDA quantity Pivot product performance failure reproduced: `/tmp/loom-root-quantity-cda-full-lifecycle-native/report.json` independently covers815261 Observations and passes COMPLETE Missing+d discovery2536ms (endpoint1969.599ms). First ERROR-policy Pivot preview exceeds native5s budget; server subsequently returns PREVIEW_TIMEOUT504 in10227ms, request `construction-proposal-d924298d-5d8a-475d-a736-5e93f5a5c265`, AQL query ID9d9661bb21d2313a,7477bytes/39bindvars. Source/API freeze unchanged; no runtime JS exceptions. Optimize owning full-population Pivot query rather than raising deadlines, reducing population, or adding Retry. Browser timeout evidence capture needs improvement so the report retains the final backend diagnostic. Remaining lifecycle is not reached.

Related status ONE/ALL acceptance tightened during review: staged generic-proposal harness repaired ONE by Cancel→reopen chooser→reselect source, which adds a user detour and does not meet same-choice direct repair. Keep source, route and ALL form selected after the exact ONE cardinality failure and allow direct ONE→ALL correction in the owning UI. Backend operation-local policy propagation is in progress; do not classify cancel/reselection as the completed repair lifecycle.

Published Combine follow-up: `/tmp/loom-combine-join-api-recovered-native/report.json.join` remains failed before Join configuration: the verifier expected a current published revision for source output `out_2841400ad84fa86be8d63af5` and found zero. Both source tables were created, configured, previewed and published through native controls; seven recorded actions took 285–711 ms and there were no page exceptions. The earlier nonexistent `explorerId` assertion has been corrected, but revision selection now needs comparison with the actual saved publication/catalog response. Join and Append lifecycle remain unverified.

Current-draft Combine compiler prerequisite integrated: `WORKSPACE_OUTPUT` references resolve within the exact workspace bundle, reject missing/self/cyclic dependencies, and compile dependencies before consumers while retaining authored output order. Group/Pivot input schemas come from finalized compiler outputs, including a negative client-declared type mismatch test. Six focused Go packages pass on the integrated source. This is not a native lifecycle pass: ordinary query/page execution still rejects uncaptured sibling references until full-row, scoped, leased private capture is wired. Published-only UI remains intermediate.

Related-source ONE policy integrated: chooser intent, authored/recipe validation, mapping, typed stage lowering and AQL now preserve operation-local ONE with ALL_MATCHES traversal. Zero distinct present values produce null; one produces a scalar; multiple values produce CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES. Integrated focused tests cover scalar lowering, policy mapping, authored round-trip and exact explicit-cohort contributor lookup. Native lifecycle remains unverified pending awaited chooser ownership: an async 422 must retain the selected route/form/policy and permit direct ONE→ALL repair without reopening. Automatic preview must remain enabled.

Pivot timeout diagnostic: `/tmp/loom-root-pivot-readonly-profile.json` records one profiled SUM execution over the complete CDA scope (815,261 documents), returning one row in 8,503 ms. EXPLAIN/profile identifies materialization, source-row construction and per-category group-row loops. This is a diagnostic, not a benchmark or measured improvement: its generated query differs from the browser request and no repeated before/after comparison exists. The full browser lifecycle still fails its first proposal deadline. Preserve complete-scope ERROR validation, typed MISSING/NULL distinction and independent SUM/MAX checks when optimizing.

Published Combine empty-catalog reproduction: `/tmp/loom-combine-join-catalog-diagnostics-native/report.json.join` records the actual exact-snapshot construction-input catalog as `entries: []` after a successful native publication. Source draft is version6; no revision-match condition was relaxed. The Join picker cannot be configured, so matrix Join status is failed and persistence/Join-result performance remain untested. Investigate publication registry/current-pointer/catalog filtering before rerunning.

Private artifact catalog review remains open before runtime wiring: all conditional updates/removal require Arango revision CAS, and cleanup-pending cannot transition back to active states. Review caught a terminal-state predicate comparing a string state against a boolean cleanup flag; unit query-string assertions passed despite that binding error. Require separate typed state binding plus a real stored-state regression before accepting the adapter. No runtime private artifact provider is integrated yet.

Automatic related-source preview ownership integrated: the chooser awaits the same automatically triggered proposal/receipt run; ONE rejection retains route, form and policy, and direct ALL repair closes only after a ready preview. Four-file patch reviewed; worker reports42 focused tests passing, integrated production TypeScript check passes. Native CDA ONE→ALL lifecycle remains untested for this checkpoint. Automatic preview is preserved; no new Preview click.

Strict Basic related-source ONE→ALL native acceptance: `/tmp/loom-related-one-query-owner-strict-current/report.json` passes all20 cases with maximum action1278ms. Its exact bounded Patient→Observation.status oracle cross-checks the literal fixture and yields one distinct `final` value. ONE previews successfully (HTTP200/READY, scalar `final`), then Cancel and same-chooser switch to ALL pass, followed by Apply/native preview, reload, edit, and removal/source restoration. This run does not exercise a ONE cardinality rejection. All112 native requests are drained;10 aborts are accounted for as expected owner retirements with DOM-anchor/controller/trusted-action evidence, and there are0 unexpected errors. Source fingerprint (1168 files), watched-source freeze (1105 files), and fresh API build stamp are unchanged. This closes the Basic-fixture case only; real-CDA Observation.status conflict repair remains unverified. The latest CDA preflight `/tmp/loom-cda-related-status-query-owner-strict-current/report.json` is unverified with zero browser cases: a bounded search across up to2,000 scoped current-generation `loom_dev_cda_fhir` / `cda-fhir-v1` Patient candidates found no linked Patient→Specimen→Patient→Observation witness with at least two distinct statuses. This is bounded fixture absence, not proof of global absence.

Starting-collection repair fresh readiness run fails: `/tmp/loom-group-unmapped-repair-ready-native/report.json`, invocation `LOOM_UNMAPPED_MEMBER_REPAIR=1 node scripts/verify-ui/workflows/verify-cda-group-related-summary-browser.mjs /tmp/loom-group-unmapped-repair-ready-native`. Initial render1779ms; Group remains unavailable within the5s action budget at configureGroup. No lifecycle pass is claimed; worker is inspecting retained capability/DOM evidence before changing product or harness.

APPEND nullable missing-field implementation integrated after complete patch review: explicit Empty for this table omits that source mapping, makes the output nullable, and renders a typed null while retaining every input row. Recipe/IR/lowering/render/UI regressions cover omissions, incompatible scalar types and saved edit reconstruction; integrated production TypeScript passes. Native absent-field lifecycle remains open, including unpublished draft sources. Published-input enumeration currently fails because persisted authored columns lack stable IDs; a source-backed producer fix is being staged.

Collection repair timeout diagnosis corrected: Group editor was open; the verifier expected the obsolete label Group by Observation FHIR resource ID instead of Group by Observation ID. Selector-only correction integrated with the unchanged5s budget. Retained run also includes RECEIPT_RECOMPILE_REQUIRED409 output_execution; investigation remains open. The previous timeout does not establish a disabled Group product defect.

Full-CDA streaming Pivot optimization integrated after complete review and focused compiler tests pass. Native `/tmp/loom-root-quantity-cda-streaming-native/report.json` still fails first ERROR-policy preview within5000ms; COMPLETE category discovery2397ms, raw815261Observations, Missing778185/d37076. Actual proposal pending at timeout; source1077files and APIbuild unchanged. Command `node scripts/verify-cda-quantity-root-category-browser.mjs full-population-lifecycle /tmp/loom-root-quantity-cda-streaming-native`. No speedup or lifecycle pass claimed; worker assigned exact runtime query/log diagnosis.

Recursive scope test fixture correction integrated: renderer hand-built EXISTS and unused Set subplans now contain the mandatory edge+target project, generation and auth guards. Compiler-produced routes already emit them; validator remains unchanged. Focused overlay test TestSourceGroupPrunesUnusedRelatedSetAndKeepsFilters passes.

Private artifact Arango catalog integrated after final cumulative review: dedicated manifest collection, immutable identity/schema, revision-checked owner/expiry updates, active lease renewal, atomic expired cleanup claims, and terminal cleanup state. Worker unit/race tests pass; opt-in real Arango arbitration tests remain unrun. This enables production storage wiring for unpublished draft Combine; it does not yet establish native Join/Append acceptance.

Private manifest catalog follow-up remains required before live acceptance: raw RFC3339Nano string comparisons can misorder leases with different fractional precision (whole-second versus fractional-second, or .1 versus .11). Root returned the integrated candidate for chronological timestamp comparison/extension/sorting corrections and explicit regressions. Existing unit/race checks do not prove this boundary case; production engine wiring remains staged.

Verifier ownership helpers checked on integrated source: `node --test scripts/verify-ui/helpers/tests/verify-ui/helpers/tests/related-source-capture.test.mjs scripts/verify-ui/helpers/tests/saved-preview-binding.test.mjs scripts/verify-ui/helpers/tests/validation-wait-evidence.test.mjs` passes5checks. These reject stale preview receipts, classify exact operation-level ONE/ALL policies, and retain late validation timeout evidence without converting a timeout to a pass. Native lifecycle evidence remains separate.

Integrated cross-package check passes: `go test ./internal/dataframe/recipe ./internal/dataframe/compiler/ir ./internal/dataframe/compiler/lower ./internal/dataframe/compiler/render/clickhouse ./internal/dataframe/execution/chartifact/... -count=1`, all six packages green. `git diff --check` passes. Opt-in Arango runtime cases remain skipped; lease timestamp correction still required before production acceptance.

Integrated AQL renderer package passes after the streaming Pivot change and scoped fixture correction: `go test ./internal/dataframe/compiler/render/aql -count=1`. Native full-CDA Pivot performance remains failed as recorded separately. Whole-relation authorization evidence integration now explicitly requires proof against the actual optimized capture plan, not merely its pre-optimization lower plan; constructor revalidation remains mandatory before sealing private grouped inputs.

Private lease timestamp correction integrated after cumulative review: all stored lease/create/update timestamps and AQL time binds now use fixed-width nanosecond UTC strings, preserving chronological indexed sorting and MAX extensions. Exact-second/fractional-second encoding and roundtrip regressions pass in worker unit/race checks; real-Arango cutoff/sort cases remain gated/unrun. Reviewed checkpoint9685d66d963a55c5b0426e1c6ac302174b3fbffc; correction194e368e7401a393c5c3ddec8b56e8e33e599a6a. No production deployment or native acceptance claimed.

Integrated verifier checks pass15assertions across builder-combine helpers and coverage classification: `node --test scripts/verify-ui/tests/registry.test.mjs scripts/verify-ui/helpers/tests/coverage-status.test.mjs scripts/verify-ui/helpers/tests/builder-combine.test.mjs`. This is harness evidence only; native Join/Append lifecycle still open.

Whole-relation proof review found two unsupported-shape restrictions requiring correction before integration: pure post-group filters retain authorized provenance and must remain usable; nested workspace Combine outputs need typed compositional evidence from exact ordered input proofs/captures rather than an AQL-only issuer. Workers are coordinating the constructor contract. No evidence patch has been integrated and no grouped/nested draft Combine pass is claimed.

Full-CDA Pivot runtime log diagnosis: the failed native query remained on the materialized dynamic renderer because lifecycle previews set IncludeSourceIdentity=true and the new streaming eligibility gate excluded it. The optimization was not exercised by the failing browser case. Worker assigned a production-shaped eligibility regression and composite-identity-preserving correction; do not claim streaming performance from that run.

Related-source operation-local ONE native failure reproduced on basic fixture: `LOOM_RELATED_ONE_ALL_MODE=basic LOOM_RELATED_ONE_ALL_FIELD=status LOOM_DEV_PROJECT=loom_dev_c89a69d7e137 node scripts/verify-ui/workflows/verify-cda-related-one-all-browser.mjs /tmp/loom-related-status-awaited-basic-native`. Eight actions max1192ms, actual ONE proposal200/92ms carries rowValuePolicyONE but returns ["final"] instead of scalar "final". Strict independent raw scalar assertion fails; do not weaken it to accept arrays. Worker assigned source→recipe→lower→render/preview root-cause regression. Later Apply/edit/remove/reload and CDA conflict-repair remain unreached.

Real Arango private-catalog checks pass on owned manifest keys: `docker exec -w /workspace -e LOOM_TEST_ARANGO_URL=http://arangodb:8529 -e LOOM_TEST_ARANGO_DATABASE=loom_dev loom-dev-6d7df93d6a37-loom-api-1 go test ./internal/dataframe/execution/chartifact/arango -run 'AgainstArango$' -count=1 -v`. All3cases pass: active/expired lease arbitration,20renew-vs-claim and20delete-vs-claim races, exact-second/fractional timestamp cutoff/sort. No browser overlapping; these are storage checks, not native draft Combine acceptance.

Pivot composite-identity eligibility correction integrated after complete review: lifecycle IncludeSourceIdentity requests now enter streaming only for already-composite Pivot outputs, preserving the existing no-single-root-ID semantics. Worker production-shaped regression passes exact query/binds/schema/groupidentity equality and complete scope/assertions before limit. FullCDA native rerun remains next.

Composite-identity Pivot native rerun remains failed: `/tmp/loom-root-quantity-cda-composite-streaming-native/report.json`, COMPLETE discovery2440ms over815261rawObservations, firstERRORproposal exceeds5000ms, restlifecycleunreached. Compiler production-shaped regression alone does not establish runtime eligibility or performance. Worker now assigned actual streaming-query confirmation and one bounded exactqueryprofile if streaming is active; do not repeat gate assumptions without executable evidence.

Published input stable-ID producer fix integrated after full review: all source-authoring call sites now assign deterministic source identities before a table has construction. Real CREATE_TABLE+ADD_COLUMN→workspacecompile→semanticresolve→lower→publication conversion regression confirms the exact authored ID at both schema boundaries; full authoringv2 and focused server/lower tests pass on worker overlay. No catalog-invented IDs or rewrites of existingpublishedrecords. Fresh native Join rerun remains next after serialized runtime diagnostic releases.

Full CDA streaming Pivot diagnostic: the latest native proposal now reaches the expected `TABLE_PIVOT_CELL_CARDINALITY` validation response in 7.197s (6428-byte AQL, 33 binds), rather than the earlier materialized-query timeout. The current production-shaped query uses `COLLECT ... AGGREGATE`; one complete-population diagnostic run reaches the same assertion in 6.013s. This remains a failed five-second browser case. EXPLAIN selects a persistent index followed by materialization, with a substantial population estimate mismatch; index coverage and scan cost are under investigation. Diagnostic artifacts: `/tmp/loom-category-stream-query.json` and `/tmp/loom-category-stream-profile.json`. Assertion failure yields no per-node runtime profile, so these timings do not establish a measured node-level bottleneck or a passing lifecycle.

Fresh native published Join after the stable source-ID fix: `/tmp/loom-join-stable-source-id-native.join` now exposes exact nonempty authored source IDs in the published catalog. The case fails its next prerequisite: root Observation.id and DiagnosticReport.id are both declared `Nullable(String)` with `nullable:true`, so non-null Join keys are not offered. The full Join lifecycle remains unreached. The schema nullability producer is under source investigation; do not relabel nullable keys as non-null or weaken the verifier without proving the resource-ID invariant. Command: `env LOOM_DEV_SOURCE_ROOT=/private/tmp/loom-construction-implementation LOOM_DEV_COMPOSE_PROJECT=loom-dev-6d7df93d6a37 LOOM_DEV_API_PORT=8188 LOOM_DEV_UI_PORT=30008 LOOM_DEV_PROJECT=loom_dev_c89a69d7e137 LOOM_DEV_FIXTURE_DIR=/private/tmp/loom-construction-implementation/testdata/verify-combine node scripts/verify-ui/workflows/builder-combine.mjs --case join --report /tmp/loom-join-stable-source-id-native`.

Related-source ONE wire/profile fix integrated: pinned oapi-codegen v2.8.0 regenerated the request model and embedded schema; decoded spec diff contains only rowValuePolicy ALL/ONE. Focused canonical HTTP/profile tests pass. Fresh native basic status run `/tmp/loom-related-status-one-wire-fixed-native/report.json` now passes scalar ONE, Cancel, same-chooser ALL repair, Cancel/reopen, and ALL Apply with 13 recorded actions <=1337ms. The run then fails in the verifier at line1027: it unconditionally reads `savedGroup.rowValues.find` even though RELATED_SOURCE owns its policy and the Group has no rowValues. This is a harness defect; remaining edit/removal/reload lifecycle is unreached, and full CDA status repair is still untested. Narrow branch correction is staged next.

Native related status lifecycle progressed again: `/tmp/loom-related-status-one-full-lifecycle-native/report.json` passes correct scalar ONE, same-chooser ALL repair, Cancel/Apply, exact row oracle, reload, label edit and edited-header reload; all17completed actions <=1252ms. The corrected semantic header assertion passes. Removal is unreached because the verifier still looks for the legacy REMOVE_COLUMN control for a generated RELATED_SOURCE output; current construction-step removal ownership is under source review. Do not claim a complete lifecycle.

Three-input published APPEND native attempt `/tmp/loom-append-three-input-null-padding-native.append` passes all three source tables, publication, catalog and scalar schema checks, then times out waiting for Input table1 before selecting a Combine operation. Failure DOM shows the enabled Match rows/Stack rows chooser, and selectors are expected only after choosing the operation. This is a harness ordering defect rather than proof of unsupported APPEND. Correct the initial readiness and action-to-render checks, rerun the same full lifecycle. Incidental favicon404 remains recorded.

Starting-collection repair diagnostic rerun `/tmp/loom-group-unmapped-repair-contract-diagnostic-native/report.json` now passes Group automatic preview/Cancel/Apply and related COUNT preview/Apply/reload; completed native actions <=3836ms and source/API remain frozen. Earlier409 is not reproduced, so its specific cause remains unproven. The coverage check now reproduces an actual backend500: request `1668a2fd-1c10-46ec-9d26-12ee54c98671`, population-mapping, internal cause `construction population mapping requires the exact final row identity` at2026-10-04T11:01:16.629Z. Source investigation assigned to final-row-identity population mapping. Repair/removal and final persistence are unreached; do not hide the coverage control or change the public error to claim success.

Related status basic native lifecycle now reaches all20actions, including actual history-step removal proposal, Apply and exact original Group/membership restoration after reload, max1270ms: `/tmp/loom-related-status-one-removal-lifecycle-native/report.json`. Overall report still fails because the network recorder cannot settle one older semantic-inventory request while later identical requests decode200; it also records owner-change ERR_ABORTED as failures. Request-ownership accounting is under investigation; do not suppress all aborts or declare overall green. Product correctness and persistence assertions pass in this basic fixture; real CDA remains separate required verification.

Whole-relation scope evidence is now integrated after cumulative root review (worker patch SHA b6756828589bfe7b6ea2601290f76ddcc415ab54490e623dd42588e01fa2d87c). Opaque ordered operands bind exact workspace outputs, published revisions and private AQL prefixes to the finalized execution plan, scope and schema. Full IR/lower overlay suites passed; integration diff check is clean. This supersedes the earlier unintegrated-proof note. Native unpublished grouped Combine remains unverified until execution and capability wiring are complete.

APPEND verifier follow-up review found a second readiness defect: the editor initializes two inputs, but the staged chooser fix waits for all three before adding the third. The native add-input action and its five-second timing check are required before accepting the correction. The malformed assertion arguments and CSS-transformed headers are also included in the pending harness correction; no fresh APPEND lifecycle pass is claimed.

Canonical scope-evidence integration check passes: `env GOTOOLCHAIN=go1.26.5 GOCACHE=/tmp/loom-scope-integrated-cache go test ./internal/dataframe/compiler/ir ./internal/dataframe/compiler/lower -run 'WholeRelation|WorkspaceCombineScope' -count=1`. Lower scope/composition regressions passed (0.748s); IR compiled but matched no tests under that filter. The worker full-package overlay result remains separate evidence, and this check does not establish native Combine acceptance.

Starting-collection population mapping identity fix integrated (reviewed patch SHA 41215a051591d2cfc5ec90da9dc0c39ae12cf9b105fa09b28ac68df868eddff3). Authored GROUP→RELATED_SOURCE mapping now derives the exact compiler-proven required internal scalar `__loom_row_id` without changing public output identity or membership. Canonical `go test ./internal/dataframe/compiler -run 'TestCompilePopulationMapping(OrdinaryGroupRelated|ConstructionRejects)' -count=1` passes (0.498s), including malformed schema and identity-length rejection. No live verifier process was running during integration. Exact CDA unmapped-member repair browser regression remains pending; the earlier 500 is not yet marked fixed in the coverage matrix.

Exact real CDA unmapped-member repair now passes: `/tmp/loom-group-unmapped-final-identity-fixed-native/report.json`,14 timed actions/max3678ms, errors empty,1087 source files and API build unchanged. Native coverage mapping, unmapped-member removal, preserved mapped contribution/related summary and final reload pass. This closes the reproduced final-row-identity500; mapped contributing-member replacement, restricted authorization and the historical unreproduced409 remain distinct gaps.

Corrected APPEND chooser run `/tmp/loom-append-three-input-chooser-fixed-native.append` now passes native opening478ms and all three source creation/publication/catalog checks. It stops at the verifier selected-target assertion: the Combine editor subview hides the table-tab strip, so the tab-only `selectedTarget` selector returns null while visible header is Editing Observations combined. Exact target binding must be asserted through native returned output identity plus the mounted editor owner, without guessing from title. Worker assigned focused correction; APPEND preview/lifecycle remain unreached.

Historical gap assessment before the attached-member proposal integration: PopulationPanel exposed removal only for unmapped refs, and the mapped selected-member path had no control. The mapped-member removal control and scoped proposal/Apply API are now integrated; 95 focused tests and production TypeScript pass (see the current integration note below). Native browser acceptance is still outstanding: verify removal of a mapped contributing member with automatic impact preview and Cancel/Apply against immutable revisions and an independent two-contributor CDA oracle. Add/replace membership remains a separate inventory item.

APPEND exact native target-binding correction integrated after full review and focused16 Node/34 Builder reconciliation checks. `/tmp/loom-append-native-target-bound.append` passes target creation, chooser458ms, initial selectors235ms and third-input addition228ms; automatic preview then remains absent with invalid output-name feedback and five configured output rows rather than expected three. No proposal request error proves a backend refusal; verifier field operations and UI draft update ownership are being investigated. Lifecycle remains failed/unreached, not an acceptance pass.

APPEND exact-three-row rerun `/tmp/loom-append-exact-three-rows-native.append` passes the complete pre-preview name/mapping matrix and reaches construction-proposals. The backend returns 422: `candidate construction is invalid: construction: combine: append output … is not mapped from input 2`. This is a sparse-mapping validator boundary failure, not a reason to remove automatic preview; a focused backend regression/fix is in progress. Apply/edit/removal/reload remain unreached.

Related status recorder rerun `/tmp/loom-related-one-all-owner-retirement-native/report.json` passes all20 product actions (max1278ms), but overall drain still fails on retired catalog request25074.671. The exact ConceptCatalog semantic-inventory request legitimately omits outputId; the ownership classifier incorrectly requires it. Same-frame loader replacement and Runtime.executionContextsCleared are recorded. Correct the endpoint request-shape rule without weakening unknown/active/critical request failures; overall acceptance remains failed pending rerun.

Related status exact-contract rerun `/tmp/loom-related-one-all-exact-catalog-owner-native/report.json` passes all20 product actions (max1281ms) and correctly recognizes the retired pending catalog request. Overall acceptance still fails on terminal background ERR_ABORTED requests whose cancellation occurs before later page retirement. Full-page retirement alone cannot prove those earlier cancellations; investigate exact user-transition/controller ownership rather than loosening cancellation timing or ignoring aborts.

Sparse APPEND authoring boundary fixed and tested: nullable scalar omissions accepted; all-empty/nonnullable/nonscalar rejected. Rerun `/tmp/loom-append-sparse-authoring-fixed-native.append` reaches a different422 in capabilities/proposals: `source projection must contain at least one column` while compiling the rooted empty destination together with siblings. Workspace-input support integrated; empty-target compilation correction is in progress.

New upstream Group edit→add related column regression integrated at `scripts/verify-ui/workflows/verify-cda-group-edit-before-related-column-browser.mjs`. First realCDA attempt `/tmp/loom-cda-group-edit-before-related-column-native/report.json` finds its bounded independent 2–24 Observation witness but stops on recorder transport-origin mismatch: browser uses UI proxy30008 while the script filters native requests to API8188. No product failure or lifecycle pass established; source-backed harness fix assigned before repeating.

### Nullable Join verifier integration review (2026-10-04)

Nullable scalar `KEY_JOIN` support in `POLICY` and `CUMULATIVE` is accepted and integrated; the focused Node suite passes26/26. The prior native report `/tmp/loom-join-stable-source-id-native.join` remains a stale run: its verifier requires non-null IDs and stops before configuring Join when both published IDs are `Nullable(String)`. Rerun the published native case with ordinary nullable equality semantics and an independent exact INNER/LEFT oracle that confirms NULL does not match NULL; native nullable-key behavior, persistence and timing remain unverified. Unpublished grouped-draft Join and real-CDA acceptance remain separate.


### Native evidence reconciliation (2026-10-04)

The earlier basic Related ONE→ALL run after no-effect query-owner integration, `/tmp/loom-related-one-query-owner-integrated-native/report.json`, completes all20 product actions with a maximum action time of3057ms but is strict-failed on request drain. Its exact basic Patient→Observation.status oracle yields a single `final` value: the ONE proposal returns HTTP200/READY, then Cancel and same-chooser switch to ALL, Apply, native preview, reload, label edit, removal, and restoration pass. It does not exercise a ONE cardinality rejection. The report remains `failed` because strict drain records10 canceled background requests (2 `population-routes`, 8 `construction-choices`/paired-column choices); `expectedOwnerCancellations` is empty. The source fingerprint is identical before/after (1158 files, `66d90729ff8ee8054a5cf0c7fb0c3ea83599ef9206b87d6f7999e67e48269840`), the 1095-file source freeze is unchanged, and the API build stamp is fresh and unchanged before/after. This is action-level correctness and persistence evidence for the basic fixture only; it is not CDA acceptance. The later strict-current report above resolves the drain issue; real-CDA cardinality-conflict behavior remains open.

APPEND actual-receipt metadata handling is fixed in the server, and its focused regression passes in0.944s. The latest published three-input native run `/tmp/loom-append-combine-metadata-integrated-native.append` reaches the proposal after source creation/publication, target binding, three-input chooser, and sparse mapping setup; the completed setup actions are <=649ms. Proposal request `construction-proposal-2a617480-dd2d-4821-aa61-6d927464a887` returns HTTP500 because the first `constructionStages["out_9149fc2a5df7505a1de9b27d"][0]` must be an implicit source projection. Preview and the remaining lifecycle are unreached. An explicit watched-source assertion passes with identical before/after fingerprint (1159 files, `8197e7d7570ea06f34bdecf198083ff3b68775a54b3a826111891b1c2013d204`). The report does not capture an API build freeze. The server test does not establish native APPEND acceptance.

Current-draft Combine compiler dependency planning, opaque whole-relation scope evidence, renderer, execution, and server integration are now present. Focused renderer (0.400s), execution (2.723s), chartifact (1.100s), chartifact/Arango (0.738s), and server (2.981s) checks pass. No native current-workspace output capture or Combine lifecycle has run.

At this historical checkpoint, the bounded Patient-first query `/private/tmp/loom-cda-gender-unpivot-witness/result.json` scanned2,000 scoped current-generation Patients and found0 nonempty `payload.gender`; it skipped reverse-edge lookup and exact-owner reread, so it did not distinguish absent from explicit null. The later exact reread and native chooser failure are recorded in the “Patient.gender missing/null ALL after Unpivot” update below. The old bounded scan remains a nonempty-value sample only, not project-wide absence.


Compiler regression follow-up: `output.go` now places the execution window (SORT followed by LIMIT) after UNNEST defines the expanded row identity and before unrelated sets. The four previously failing root Go regression checks pass (0.388s), including `TestExpandedWindowSortsStableIdentityBeforeUnrelatedSets` in `row_expansion_test.go`. The related Group scope-edge test now rejects a noncanonical `other_edge_project` binding early; its focused test passes. The historical claim that two CohortGroup identity regressions still fail was not pinned to a run artifact. A fresh 2026-10-11 focused check on the authoritative checkout passed TestExpandedWindowSortsStableIdentityBeforeUnrelatedSets, TestGroupedPivotPreviewWindowRunsAfterReshapedRows, TestUnpivotPreviewWindowBoundsDeterministicReshapedRows, TestTableReshapeExecutionWindowBindsExpectedLimit, TestCohortGroupAcceptsRetainedRootKeyDistinctFromExpandedRowIdentity, and TestCohortGroupRejectsForgedRetainedRootKeyProof. Command: go test ./internal/dataframe/compiler ./internal/dataframe/compiler/ir -run '^(TestExpandedWindowSortsStableIdentityBeforeUnrelatedSets|TestGroupedPivotPreviewWindowRunsAfterReshapedRows|TestUnpivotPreviewWindowBoundsDeterministicReshapedRows|TestTableReshapeExecutionWindowBindsExpectedLimit|TestCohortGroupAcceptsRetainedRootKeyDistinctFromExpandedRowIdentity|TestCohortGroupRejectsForgedRetainedRootKeyProof)$'. Exit 0; compiler 0.431 seconds, IR 0.625 seconds. This establishes those six named checks only and does not retrospectively identify the unnamed historical four-test selection. These compiler checks do not change browser-matrix statuses.


### Patient.gender missing/null ALL after Unpivot: source witness proven; native field unavailable

The populated-gender ALL path remains untested. The `gender-null-all` driver reuses the exact Specimen and Patient IDs from `/tmp/loom-related-resource-type-after-unpivot-native-complete/report.json` and rereads the raw payload under project `loom_dev_cda_fhir` / generation `cda-fhir-v1`. The terminal report `/tmp/loom-related-field-gender-null-after-unpivot-native/report.json` proves Specimen `b7cad184-db67-5542-a975-10fffa3e89e7` has one `subject_Patient` target, `afcfb15e-7617-5691-ae2c-ab675322fb33`; `HAS(payload,"gender")` is false and `IS_NULL(payload.gender)` is true. Source-level ALL expectation for that one linked Patient is `[null]`; this is an exact witness, not a claim about the full dataset.

The native report completes 11 prerequisite Patient.id/Unpivot Preview/Cancel/Apply/reload steps (maximum2130ms), then times out because the post-Unpivot chooser does not expose Patient.gender. No gender proposal or exact `[null]` Preview/Apply/edit/removal was reached. Report errors are empty and its source freeze is unchanged (1103 watched files). The offline helper tests pass3/3, but they do not prove Builder output. The DOM formats `[null]` and `[]` as an em dash, so the repaired path must assert exact Preview protocol values. Basic fixture behavior, populated gender, Pivot, and restricted-auth remain separate. See the failed CDA and untested Basic rows in [the Builder verification matrix](BUILDER_VERIFICATION.tsv).

### Latest three-input APPEND native evidence

The latest Basic fixture report `/tmp/loom-append-automatic-output-native.append` passes the exact duplicate-preserving eight-row tuple multiset and literal null-padding oracle. The receipt-bound proposal is HTTP200/READY; automatic preview action-to-render is620ms. Apply returns HTTP200, persists the exact sparse three-input mapping and nullable outputs, and renders the saved table/preview in652ms; the null-padded union survives Builder reload, and edit reconstruction shows the intended explicit Empty mappings. The next edit action fails because the visible `Output field 3 label` control is disabled. The overall report remains `failed`; edit Cancel/reapply, removal, and source restoration are unreached. Source fingerprint is unchanged (1170 files, sha256 `13be141f8a27b11ffb82ee2d1b898a129a81ab1733426f98a83ccbac8af3d41e`); API/schema fingerprint `0d2505334b9204cdacb0efa5dbe5f9771e7e74bc5f7bdfd6b774e833fff140be` was captured, with no API build-freeze result. Investigate the disabled label control and finish the lifecycle within the <=5s action budget. This remains Basic fixture evidence, not CDA or unpublished workspace-output acceptance.

### Attached population member proposal API integration

The mapped-member removal control, public proposal/Apply endpoint, and generated request/response contract are integrated. The focused suite has95 passing tests and production TypeScript passes; generated DTO tests pass in0.487s and focused server tests pass in0.929s. The v4 real-CDA report `/tmp/loom-cda-population-member-removal-native-v4/report.json` passes all9 product assertions: exact one-mapped/one-orphan oracle; native Rows→Change into RowDefinitionSettingsPanel starting-collection settings; automatic impact preview, Cancel, fresh retry; exact Apply; and reload to the expected empty downstream result. All7 timed actions complete within1933ms. Overall report remains `failed` because strict native request drain leaves one `/one-google-bar` request pending. `errors` is empty apart from the separate incidental favicon404; source fingerprint (1172 files), watched-source freeze (1107 files), and API build stamp are unchanged/fresh. Resolve the request at its launch/ownership boundary and rerun strict verification without weakening drain. This proves the selected removal path only; addition/replacement and restricted authorization remain separate.

Current mapped-member removal rerun, `/tmp/loom-cda-population-member-removal-current-v6/report.json`, supersedes the v4 launch diagnosis. All seven product actions pass within 1887 ms, but strict acceptance fails on a ConceptCatalog semantic-inventory response canceled during a UI-only context change. The request scope remains unchanged. Fix the catalog query owner rather than allowing active-owner cancellations; preserve search, pagination, selected columns, source authorization, and stale-result rejection. The catalog baseline has 24 passing unit tests and lacks this race regression.

### Nullable related field after Unpivot — verified 2026-10-04

Pinned CDA missing Patient.gender now passes the full 31-action lifecycle at
`/tmp/loom-related-field-gender-null-schema-full-timing-v9/report.json` (max 4118ms including chooser opening and field discovery).
Schema-backed fields use snapshot-bound identities and compiler-proved choices.
Discovery proves only projections, prioritizes direct fields, publishes pages
progressively, and does not run for the closed Advanced source setup panel.
The raw oracle verifies `[null]`, and edit/removal/reload restore preceding bindings.
Basic-fixture, restricted-auth, populated gender, empty related population and
Pivot cases remain open. Earlier loading failures and harness-only new-preview
request assumptions are superseded by the saved-draft receipt checks in v9.
Harness repair time was not measured; no API-build-freeze claim is made.

## Upstream Group edit request retirement closure

The strict CDA rerun `/tmp/loom-cda-group-edit-read-retirement-full-timing-v2/report.json` passed all 28 actions with a maximum of 4028ms, including opening the related-field chooser and discovering the field. The independent raw oracle, source freeze, and API build identity checks passed with no unexpected errors. The preceding run reproduced six canceled background reads from PopulationMemberRemoval and PairedColumnSuggestions. Those bounded reads now finish after query retirement and discard stale results. Fourteen focused UI tests and the production type check pass. The member-removal dependency slice and these read fixes were committed and pushed as `af1e706183dcd813c30276b3ee89e2f08cae463a`; Join/Append changes remain separate. This run required one timing correction in the verifier; repair time was not measured.

## Full-population Pivot index collision

Current native evidence `/tmp/loom-root-quantity-cda-current-af1e70618/report.json` reproduces the expected cardinality validation arriving after the five-second budget. The production AQL hash `24cdf4793055b7f4` matches the current compiler query artifact at `/tmp/loom-pivot-current-query.json`; EXPLAIN `/tmp/loom-pivot-current-explain-before.json` selects the older eight-field index. The compiler requested scope/_key index has exactly the same key fields as the existing non-preview index `Observation/511`, but different stored values. ArangoDB documents that such indexes cannot coexist. The local migration failed closed before deleting the old index. Compiler key partitioning must change while keeping the full quantity parent object for MISSING versus explicit NULL correctness. The preserved full population has 815261 rows, and no lifecycle pass is claimed. Tooling repairs so far are completed-response capture, container PATH preservation, and the diagnostic-envelope/strict-index-definition boundary; repair time is unknown.

### Corrected index installs; database planner failure remains

The reviewed compiler/store patch now uses IR-proven scalar keys `payload.id` and `payload.status` after the project/generation/auth/_key prefix, and stores only `payload.valueQuantity`. The exact native-shaped artifact `/tmp/loom-pivot-collision-fixed-spec.json` compiles to `loom_pivot_preview_cdeb305dcc142386`. The authorized local migration `/tmp/loom-pivot-collision-fixed-applied.json` created that candidate before deleting the exact old preview index; the generic scope index and three other preview indexes were preserved. Focused compiler regressions and the full Arango package pass. Two full compiler population-identity tests fail identically on saved preimages in a separate stage, so they predate this correction and remain open.

The same native command, `node scripts/verify-cda-quantity-root-category-browser.mjs full-population-lifecycle /tmp/loom-root-quantity-cda-index-collision-fixed`, still fails. Full 815261-row source correctness/discovery passed (2077ms), but the first Pivot proposal returns 503 in 247ms. Owned API logs `/tmp/loom-pivot-collision-native-api.log` and read-only EXPLAIN `/tmp/loom-pivot-collision-fixed-explain.json` prove Arango's planner rejects the query with `missing variable root ... CalculationNode ... while planning registers`. Source and API build stayed unchanged. Preview/Apply/edit/removal/reload remain unreached. Investigate an equivalent query/index shape that preserves full scope and missing/null semantics; do not count index installation or EXPLAIN as lifecycle closure. The diagnostic transport now uses bundled arangosh HTTP requests to retain non-2xx bodies; harness repair time remains unknown.

Read-only isolation `/tmp/loom-pivot-planner-rule-variants-results.jsonl` identifies `optimize-projections`: disabling it permits planning but loses index coverage. The semantics-preserving request in `/tmp/loom-pivot-planner-presence-variants.json` wraps every quantity-parent access in `NOOPT`; EXPLAIN `/tmp/loom-pivot-planner-presence-variants-results.jsonl` retains the candidate with `indexCoversProjections=true`, including the whole stored parent. A single full-scope execution `/tmp/loom-pivot-planner-covered-error-execution.json` reaches expected cardinality validation in 3906ms. This is a diagnostic result, not a performance benchmark or browser lifecycle pass. Encode that parent access in the renderer, then rerun the unchanged browser case.

The reviewer identified that NOOPT is an internal/testing function. Supported `FIRST([parent])` and `NTH([parent], 0)` both retain full covering projections and avoid the planner crash in `/tmp/loom-pivot-planner-supported-parent-variants-results.jsonl`. `FIRST` reaches expected full-scope cardinality validation in a single 2023ms diagnostic (`/tmp/loom-pivot-planner-supported-parent-error-execution.json`); this is not a speedup benchmark. Read-only fixtures `/tmp/loom-pivot-parent-edge-query.json` and `/tmp/loom-pivot-parent-edge-response.json` verify exact original/candidate presence, code, and sibling-value equality across eight missing/null/empty/nonobject and code states, with no warnings. The scoped renderer patch should use the supported parent retrieval and preserve generic/unhinted queries.

### Full CDA quantity Pivot lifecycle closure

`/tmp/loom-root-quantity-cda-parent-rendered-v4/report.json` passes every required check: 14 action/validation timings, maximum 4619ms, 11 passing assertions, zero unexpected browser/network errors, unchanged source and API build. Independent aggregation of all 815261 scoped Observation records checks SUM and MAX cells by status/category, Cancel preserves the draft, Apply and edit persist on reload, and removal/restoration/reload preserve the complete source scope and exact bounded raw tuples. The integrated compiler query artifact `/tmp/loom-pivot-parent-rendered-query.json` (AQL SHA256 `2a750ff7e7c19e03e1e19216002b34c5f67dde705f61e90948dd8c75661337ca`) plans successfully with the corrected index.

Harness failures are recorded separately: v1 combined ERROR validation and subsequent SUM preview in one timer; v2 expected every virtualized source row to be mounted simultaneously; v3 retained the old 12-timing count after two validation timings were added and captured an unrelated Chrome background request. The source-backed virtual-row collector passed focused negative tests and a live check of the retained source restoration (`/tmp/loom-quantity-virtual-preview-focused/report.json`) before full reruns resumed. The final run independently checks every row through scrolling and keeps five-second per-action limits. Request ownership includes external resources initiated by the application and excludes Chrome's background page. Harness repair time was not measured. Restricted-auth and different operation shapes remain open; potential collisions with an existing field-only preview index with the new exact scalar tuple also deserve targeted coverage. The isolated commit candidate is validated below; the retained browser regression is a reference for the separately owned Playwright migration.

### Quantity Pivot explicit-null fixture follow-up

The owned single-stack fixture lifecycle passed in `/tmp/loom-root-quantity-parent-rendered-fixture-v2/report.json`. Four independently checked records distinguish Missing, Null and d; all 14 native actions passed with maximum 1001ms. This covers the explicit-null parent-presence risk absent from the CDA population. The first attempted fixture run failed ownership discovery before any browser action because its Compose environment was not supplied; that was environment repair, not a product failure.

### Isolated Pivot commit validation

The isolated candidate emits byte-identical AQL and bind JSON to `/tmp/loom-pivot-parent-rendered-query.json`. It includes the required public Pivot group-column presentation correction and its focused regression. Artifact, provider and targeted Pivot/authoring tests pass. Seven broader compiler/renderer failures also reproduce on base `af1e706183dcd813c30276b3ee89e2f08cae463a`; they concern pre-existing traversal scope, population identity and expanded-window scope. They are not claimed fixed by this unit. The Playwright migration is owned by the other Codex instance; `/private/tmp/loom-pivot-playwright-reference.tar.gz` retains current assertions and both passing lifecycle reports for handoff.

### Append edit investigation after quantity Pivot closure

Current rerun `/tmp/loom-append-edit-current-45728fdb-v3.json.append` stops before Append configuration because the legacy click helper targets the Patient Add-selected-feature button outside the viewport at y=7501.5. The 1178-file source fingerprint is unchanged. Earlier setup attempts stopped before opening Chrome due incomplete/mismatched named fixture environment; the actual UI default project is `loom_dev_c89a69d7e137`, not the Compose hash suffix. These are harness/environment failures and do not close or reproduce the saved Append edit behavior.

The earlier saved-edit failure DOM in `/tmp/loom-append-automatic-output-native.append` shows Loading available published tables, so the fieldset is disabled by loading-state selection availability. The old driver attempted its next fill after 2ms. Permanent product unavailability is unproven until a native retained-workspace probe waits for the bound catalog response and measures actionability within five seconds. Playwright migration should include scrolling/actionability waiting while preserving timing from the initiating action, rather than marking this historical disabled snapshot as a product closure.

### Published Append editing readiness race — current checkpoint

A held `construction-inputs` response reproduced a frontend race: unrelated draft-output capabilities enabled saved published-input editing before its exact pinned revisions had loaded. Editing a label then emitted an undefined candidate; releasing the catalog restored the controls but did not emit a proposal until another edit. The saved document remained unchanged. Evidence: `/tmp/loom-append-early-label-probe/result.json`.

The editor now waits for the catalog when a saved step has TABLE_REVISION inputs. Saved WORKSPACE_OUTPUT-only edits remain enabled while that catalog loads. The regression failed before the predicate change and the focused editor suite passed 13/13 afterward, including both published-pin restoration and loading draft-only edits.

Full lifecycle closure remains unproven: `/tmp/loom-append-binding-fixed-native-v2.json.append` and `v3` both stopped during Patient source setup at the native Add 1 selected feature button. The final actionability snapshot reports a visible, enabled, pointer-receiving button, but the driver fails its stability check before Append is reached. API build freeze remained unchanged. These are harness investigation overhead, not additional product closures. Browser reruns are paused pending retained-DOM/source diagnosis; the public legacy driver is unchanged because another instance owns the Playwright migration.

### Combine architecture review after product lifecycle closure

Review ownership of `clickHouseArtifactIdentityFromChartifact` in dataframe execution and `RenderWorkspaceClickHouseCombine` in the ClickHouse renderer. The user flagged repeated identity translation and the workspace renderer boundary in commit `017e64030` as architectural debt. Passing behavior tests do not resolve that concern. Keep product lifecycle verification first; then assess whether a shared identity contract and clearer rendering ownership can remove unnecessary translation without weakening scope, authorization, or generation guarantees.
