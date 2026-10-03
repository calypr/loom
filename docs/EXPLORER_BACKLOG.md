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

Latest PR29 checks fail in runs 37154827017/37154827315. The base acceptance variant now passes all nine stages, proving the fixture/source revision repair; current variant repository publication returns HTTP400 INVALID_REQUEST after ingestion/counts. Isolated Builder-to-Viewer verification reaches `create-observation-table-in-builder` then cannot find Create table. Group lifecycle unit test intermittently cannot find Summary output label 1; local full524test suite passes, so inspect transition timing and actual summary initialization before changing behavior. These three failures are assigned independently; no passing current acceptance or CI claim yet.

Acceptance publication 400 is traced to seven legacy `source.aggregate.where` columns in the fixture. Current strict `AggregateSource.UnmarshalJSON` rejects unknown field where. Preserve strict public validation; migrate fixture publication via the pinned catalog without hardcoded contributor IDs or losing predicates. J04 first-table native-control fix pushed as8ad6c268; CI job attribution to verify-fast remains under review.

Cohort upstream membership fix verified against real Arango: `TestPreCohortDropStagesExcludeMissingPinnedMembersAgainstArango` passes for related eligibility, EXCLUDE expansion, and DROP unpivot, including pinned missing members and restricted authorization. Evidence: `/tmp/loom-cohort-drop-integrated-arango.log`; lower, IR, and AQL package checks pass in `/tmp/loom-cohort-drop-integrated-tests.log`.

### Empty-cohort related traversal is fixed

The compiler now enumerates the exact contributor keys owned by each group and resolves those documents before applying scope and traversal checks. This prevents one group from inheriting another group's related records. The original real-Arango regression now passes in `/tmp/loom-keyset-empty-cohort-arango.log`. The earlier failure and query probe remain available in `/tmp/loom-cohort-filter-membership-corrected.log` and `/tmp/loom-empty-cohort-subquery-array-probe.log`; native red runs are `/tmp/loom-named-cohort-empty-group-native-ready/report.json` and `/tmp/loom-named-cohort-empty-all-native-red/report.json`.

Native COUNT and ALL lifecycles now preserve the empty group and return `0` and `[]`. Both use independent scoped Observation oracles and verify the typed member roster, revision-bound identity, Preview/Cancel/Apply, edit, removal, reload, and source/API guards. Evidence: `/tmp/loom-named-cohort-empty-count-keylookup/report.json` (18 timed checks, maximum 2333 ms) and `/tmp/loom-named-cohort-empty-all-keylookup/report.json` (18 checks, maximum 2271 ms). Real-Arango parity also passes for empty contributors and existing roots with zero matches across COUNT 0, PRESENCE false, and ALL [], in `/tmp/loom-keyset-all-zero-cases-arango.log`. Native nonempty-root zero-match and PRESENCE lifecycles, plus negative scope guards for typed key lookup, remain open; see row 104 in [the Builder verification matrix](BUILDER_VERIFICATION.tsv).

### Standalone cohort CellTrace returns saved values

The earlier receipt-bound trace failure in `/tmp/loom-cohort-cell-trace-real-output-current/report.json` came from the standalone `GROUP_ROWS` terminal lacking a typed CellTrace mapping. The compiler now maps that terminal. `/tmp/loom-cohort-cell-trace-all-value-native/report.json` verifies the saved and reloaded object row identity and `ALL` value `[Specimen]`; traces took 36 and 38 ms, and native actions peaked at 2329 ms. The run had no errors and passed source/API freeze checks. The test inspects the cell through the API after native row inspection. Native cell-trace controls and exact cell-contributor details remain untested. The run does not cover field edit or removal trace states. See row 105 in [the Builder verification matrix](BUILDER_VERIFICATION.tsv).

### Starting-collection edit beneath a cohort remains blocked

The opt-in regression `LOOM_COHORT_SOURCE_COLLECTION_CHANGE=1 node scripts/verify-cda-cohort-membership-revision-browser.mjs /tmp/loom-cohort-source-collection-change-native` passes the preceding cohort, member-field, filter, and revision checks. Native coverage POST then returns 500 because the final population-mapping projection omits `__loom_row_id`. The new collection revision is not applied. The failure report records an unchanged source freeze and matching fresh API build stamps. Fix the final-stage identity mapping, then verify coverage, remove-from-collection, Apply, reload, and exact restoration. Real-Arango parity for empty contributors and existing roots with zero matches across COUNT 0, PRESENCE false, and ALL [] passes in `/tmp/loom-keyset-all-zero-cases-arango.log`.
