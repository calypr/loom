# Explorer dataframe backlog

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

LOOM_RELATED_AFTER_UNPIVOT_CASE=resource-type-all node scripts/verify-cda-related-field-after-unpivot-browser.mjs /tmp/loom-related-resource-type-after-unpivot-native-complete passes 31 timed checks (maximum 2074 ms, no errors). Its independent raw CDA oracle is scoped to project loom_dev_cda_fhir and generation cda-fhir-v1; it selects one Specimen with exactly one subject_Patient link and verifies that the Patient resourceType is Patient. The native flow adds Patient id with ALL, unpivots Specimen ID while retaining the Patient ID array, then adds Patient resourceType with ALL on the same route. Preview, Cancel, Apply, reload, label edit, field removal, and Unpivot removal/reload all pass; ["Patient"] and the retained ID array match the raw source. The source freeze found 1057 files unchanged. The report has no API build-freeze result and makes no restricted-auth claim.

The bounded first-2,000-Specimen scan found one valid resourceType witness. It does not establish project-wide availability or absence. Patient gender ALL remains untested: /tmp/loom-related-field-after-unpivot-execute-file-current/report.json found no populated gender witness and did not reach Builder behavior. Restricted-auth and Pivot compositions also remain untested. See row 113 in the Builder verification matrix.

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
`LOOM_COHORT_SOURCE_COLLECTION_CHANGE=1 node scripts/verify-cda-cohort-membership-revision-browser.mjs /tmp/loom-cohort-source-collection-preview-owner-green`.
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
`LOOM_QUANTITY_FULLPOP=1 node scripts/verify-cda-quantity-pivot-native-drag-browser.mjs /tmp/loom-quantity-fullpop-category-baseline-current`.
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

Partial mapped collection repair now passes: `LOOM_COLLECTION_PARTIAL_LONG_ROUTE=1 node scripts/verify-cda-collection-repair.mjs /tmp/loom-partial-long-route-normalized`. Independent scoped CDA oracle, native exact exclusion, surviving membership/Observation row, unchanged exact saved route and reload all pass; max2525ms, no errors, unchanged source. The bounded CDA prefix supplies one output Observation; multi-result and restricted-auth evidence remain separate.

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
