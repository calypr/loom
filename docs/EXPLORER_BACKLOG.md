# Explorer dataframe backlog

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

The follow-up acceptance diagnostic `c91095ed1c7a6953` also builds, uploads the fixture, migrates its contract, publishes the Explorer, reaches READY, materializes 100 rows, and reads the Viewer, but its GraphQL check fails: `days_to_death` has 0 non-null rows, expected 6. A bounded project- and generation-scoped raw Observation route query finds 98 `age`, 88 `diagnosticMethod`, and 6 `daysToDeath` source values. It also finds 3151 nested-route Observations and 2829 integer components; no overflow sentinel is hit. All five published Observation aggregates are NULL despite those source witnesses, so compiled aggregation/materialization remains incorrect. Evidence: `/tmp/loom-acceptance-evidence/c91095ed1c7a6953/report.json`. Cleanup exited 0 and removed the run volumes.

Cohort upstream membership fix verified against real Arango: `TestPreCohortDropStagesExcludeMissingPinnedMembersAgainstArango` passes for related eligibility, EXCLUDE expansion, and DROP unpivot, including pinned missing members and restricted authorization. Evidence: `/tmp/loom-cohort-drop-integrated-arango.log`; lower, IR, and AQL package checks pass in `/tmp/loom-cohort-drop-integrated-tests.log`.

### Empty-cohort related traversal is fixed

The compiler now enumerates the exact contributor keys owned by each group and resolves those documents before applying scope and traversal checks. This prevents one group from inheriting another group's related records. The original real-Arango regression now passes in `/tmp/loom-keyset-empty-cohort-arango.log`. The earlier failure and query probe remain available in `/tmp/loom-cohort-filter-membership-corrected.log` and `/tmp/loom-empty-cohort-subquery-array-probe.log`; native red runs are `/tmp/loom-named-cohort-empty-group-native-ready/report.json` and `/tmp/loom-named-cohort-empty-all-native-red/report.json`.

Native COUNT and ALL lifecycles now preserve the empty group and return `0` and `[]`. Both use independent scoped Observation oracles and verify the typed member roster, revision-bound identity, Preview/Cancel/Apply, edit, removal, reload, and source/API guards. Evidence: `/tmp/loom-named-cohort-empty-count-keylookup/report.json` (18 timed checks, maximum 2333 ms) and `/tmp/loom-named-cohort-empty-all-keylookup/report.json` (18 checks, maximum 2271 ms). Real-Arango parity also passes for empty contributors and existing roots with zero matches across COUNT 0, PRESENCE false, and ALL [], in `/tmp/loom-keyset-all-zero-cases-arango.log`. Native nonempty-root zero-match and PRESENCE lifecycles, plus negative scope guards for typed key lookup, remain open; see row 104 in [the Builder verification matrix](BUILDER_VERIFICATION.tsv).

### Standalone cohort CellTrace returns saved values

The earlier receipt-bound trace failure in `/tmp/loom-cohort-cell-trace-real-output-current/report.json` came from the standalone `GROUP_ROWS` terminal lacking a typed CellTrace mapping. The compiler now maps that terminal. `/tmp/loom-cohort-cell-trace-all-value-native/report.json` verifies the saved and reloaded object row identity and `ALL` value `[Specimen]`; traces took 36 and 38 ms, and native actions peaked at 2329 ms. The run had no errors and passed source/API freeze checks. The test inspects the cell through the API after native row inspection. Native cell-trace controls and exact cell-contributor details remain untested. The run does not cover field edit or removal trace states. See row 105 in [the Builder verification matrix](BUILDER_VERIFICATION.tsv).

### Starting-collection edit beneath a cohort passes

Population mapping now uses the exact final construction identity and retained root contributor keys. Compiler and execution suites pass in `/tmp/loom-population-mapping-corrected-tests.log`; the exact routed-cohort Arango regression passes in `/tmp/loom-population-mapping-routed-corrected.log`. The native lifecycle passes in `/tmp/loom-cohort-source-collection-change-complete/report.json`: 28 timed actions, maximum 2018 ms, no errors, and unchanged source/API guards. Native coverage returns HTTP 200 with two selected records, one mapped row, and one unmapped member. Removing the unmapped Specimen creates a child selection revision (201) and saves `SET_TABLE_POPULATION` (200). The independent raw membership query confirms exactly the remaining scoped Specimen. The saved and reloaded table retains the pinned one-member cohort revision, EXCLUDE policy, downstream filter, member field, and row identity. Filter, field, and cohort removal restore the source table with the one-member selection. The earlier 500 is fixed. The first post-fix verifier run stopped because its response-capture allowlist omitted successful `/commands` bodies; the completed run records the full lifecycle.

Exact cohort cell contributors now pass the native CDA lifecycle in `/tmp/loom-cohort-cell-trace-exact-tuples-native/report.json`: saved/reloaded ALL values and contributor tuples match raw FHIR payload values and scoped IDs; source/API guards pass, trace calls take 34/36 ms, and the slowest native action is 2390 ms. Fix and permanent verifier are committed as `53425f4e`. Native cell controls, ONE and repeated-value runtime cases, pagination beyond one page, and field edit/removal trace states remain open.

The earlier coded-group differential failure `/tmp/loom-coded-group-status-differential-context-diagnostic/compound-coded-qa-1791069973760.json` returned HTTP 409 because setup created the table (draft 0→1) and then issued its default-ID command (draft 1→2), making an in-flight configured-column-context request for draft 1 obsolete. That response arrived before any collection action or external API status change. The Builder now waits for first-table ID provisioning before resolving configured-column context; all 10 focused UI tests pass in `/tmp/loom-first-table-context-integrated-tests.log`. The corrected native differential passes in `/tmp/loom-coded-group-status-context-fixed/compound-coded-qa-1791071330817.json`: no failures, maximum timed action 1625 ms, exact two-Observation oracle, Preview/Apply/edit/removal/Cancel/reload, and unchanged source/API guards. The earlier 409 was a stale setup response; the clean rerun passes.

### Saved named-cohort member-field policy edit passes

`/tmp/loom-cohort-policy-edit-native-complete/report.json` passes 41 timed steps (maximum 2619 ms, no errors) with the API build and all 1057 watched source files unchanged. On the pinned two-Specimen cohort, the native flow edits the same saved member field `ALL→ONE→ALL` while retaining its physical column, stable `columnId`, source field, group revision and selected population. The scalar `Specimen` and list `[Specimen]` match the independent raw CDA oracle; Undo and reload restore the expected policy. A distinct-ID `subject.reference` field correctly rejects `ONE` with HTTP 422 `CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES` and a visible “Keep All unique values” message. Its saved `ALL` draft identity, source and population remain unchanged through reload. See row 108 in [the Builder verification matrix](BUILDER_VERIFICATION.tsv).

The run also covers a separate new `ONE` field through Preview/Cancel/Apply/reload/removal/reload; see row 109. Contributor tuples for saved and reloaded values were obtained through receipt-bound API CellTrace calls after native row inspection. This does not test native cell-trace controls, which remain open.
