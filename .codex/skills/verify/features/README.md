# Loom development verification map

The existing recipes describe synthetic smoke coverage. The requirements below
define current Builder coverage; they are not a claim that the harness already
implements or passes every case.

The current evidence ledger is [docs/BUILDER_VERIFICATION.tsv](../../../../docs/BUILDER_VERIFICATION.tsv).
Each row names a specific behavior, rather than claiming an entire feature is
complete. Untested rows include missing or unvalidated historical evidence.
Local report paths are retained artifacts, not portable fixtures. Re-run the
listed command after relevant changes; a historical pass does not verify a new
binary. The inventory remains open as additional visible controls are found.

## Migrated Playwright cases

`builder-authoring/authoring` uses the visible Builder controls to create a
Patient table, add Gender, inspect the automatic Preview, Publish, and reload.
It compares the visible Patient IDs with the independent
`testdata/devloop-fixture/Patient.ndjson` records and checks both configured
fields after reload. Run it against an isolated `loom-dev` session with
`node scripts/verify-ui/builder-authoring.mjs --case authoring`; the exact
session variables and retained passing report are recorded in
`docs/PLAYWRIGHT_MIGRATION.md`. That pass predates the CDA extension and is
historical for its recorded source snapshot.

The same Playwright authoring case can reuse the separately loaded CDA
generation with `--reuse-owned-dataset`. It selects Patient identifier values
with ALL, compares the first 25 visible rows and their duplicate-preserving
value lists with the raw 159,047-Patient file, then checks Publish and reload.
The retained CDA report passes those correctness and persistence assertions
but fails the five-second Publish render gate at 14,690 ms. The migrated
`builder-authoring/suggestions` case passes on that CDA generation for exact
Patient ID candidate actionability; it does not Apply or reload. See
`docs/PLAYWRIGHT_MIGRATION.md` for both commands and report paths. Eleven
other registered Builder cases still use the older browser driver.

## Current Builder requirements

Inventory visible actions, including starting records and selections, named
cohorts, grouping, related-record rows, repeated-value rows, ordinary/coded Pivot,
Unpivot, direct/coded/related columns, ONE/ALL contributing values, contributor
rules, missing-match policies, coverage inspection, Filter rows, and operation
or column edit/removal. Each needs a full browser lifecycle and independent
correctness, persistence, usability, and performance results. Precisely explained
limitations may be verified as limitations, never as implemented capabilities.

## Bounded composition coverage

| Transition | What must remain correct |
| --- | --- |
| Expanded rows → another row operation | Identity and zero/one/many multiplicity |
| Related rows → Group/Pivot/Unpivot | Retained or deliberately ended related bindings |
| Group/cohort → Add columns | Authorized contributors and ONE/ALL semantics |
| Repeated values → field selection | Binding to the correct item without cross-item mixing |
| Changed shape → filter/column edit | Current columns and preserved meaning |
| Earlier edit/removal → later operations | Valid dependencies or actionable repair; no silent corruption |

Cover each distinct preservation/reduction behavior once. Reuse equivalent
coverage across resource types; add cases for genuinely different populated
FHIR shapes or contracts. This is not an all-sequences cross product. Extra
exploration is capped at ten sequences per wave; required features and reported
regressions remain mandatory regardless of that cap.

## Required regressions

- Related-record rows → Unpivot, retaining other columns and related bindings.
  Include the reported `Specimen → Patient → Condition → Observation → Patient`
  path and selection of Specimen ID for Unpivot. Obtain current server-issued
  choices through the UI, not hardcoded signed tokens or draft IDs. Verify
  render, Apply, reload, edit, removal, and restoration.
- Fresh Builder load and editor entry without JavaScript exceptions, missing
  dynamic modules, or unexpected API failures; recheck after deployment.
- Pivot field changes automatically replace category discovery for the new pair,
  without stale category lists, discovery clicks, or contradictory warnings.

Persist these as reusable browser regressions. A saved failure artifact alone
is not an executable regression. Record the invocation, starting operations,
data scope, clicks, elapsed time, source oracle, DOM values/identities, persistence,
errors, cleanup, and evidence path. Untested or skipped cases are not passes.

## Compound coded grouping

Run `node scripts/verify-compound-coded-group.mjs` against the construction
development stack (UI 30008, API 8188) with the loaded CDA fixture. Override
`LOOM_UI_ORIGIN`, `LOOM_API_ORIGIN`, or `LOOM_ARANGO_CONTAINER` for another local
stack. The case uses one Observation and coded values from its component string
field, with a raw Arango record as the independent oracle.

Native clicks verify direct selection in Group records, an unchanged draft
before Apply, one atomic save, reload/edit, an ordinary field alongside coded
keys, and removal restoring the source table. Picker entry and each preview
must finish within five seconds. HTTP, console, runtime, DOM, and timing evidence
is retained under `/tmp/loom-compound-coded-verification` (override with
`LOOM_VERIFY_OUTPUT`). Each run retains its own QA Explorer for review.

## Construction transition debugging

Use an isolated QA copy of a saved construction as the seed. Test transitions
against shared invariants: dependencies and resource identities remain valid;
removing a provider includes its dependent steps in a valid proposal and names
them before Apply; independent later steps remain; proposals leave the
saved draft unchanged; Apply/reload preserve values; query and render time stay
bounded. Cover distinct data shapes and identity changes rather than every
possible operation sequence.

`node scripts/verify-construction-removals.mjs --explorer EXPLORER --output OUTPUT`
proposes removing each visible step alone and with its downstream suffix. It
never applies a proposal. Add `--expect-cascade-step STEP` for a known dependency
regression. Each case records the baseline workspace, exact request/response,
request ID, timing, code revision, working-tree paths, and matching server logs.
The process fails on an unexpected status, broken removal contract, changed draft,
or a proposal exceeding five seconds. Artifacts default to `/tmp/loom-removal-verification-*`.

`node scripts/verify-construction-removal-ui.mjs --explorer EXPLORER --output OUTPUT --step STEP`
checks the removal through native browser clicks. It requires a valid preview
and a warning naming every removed visible step, cancels the proposal, and checks
that the saved draft is unchanged. Add `--apply` only for an isolated QA Explorer
to also verify Apply saves the exact candidate and removal persists after reload.
Its DOM, browser version, timing, and failure report
default to `/tmp/loom-removal-ui-*`. API checks establish the backend contract;
the browser check establishes that the UI presents it correctly.

## Base-setting contract matrix

Run `make verify-base-settings` against the loaded local CDA stack, or
`node scripts/verify-base-settings.mjs --browser --evidence /tmp/base-settings-qa`.
The runner creates owned QA tables and discovers their actual row choices. It
tests each advertised repeated-value path and policy on source-only, filtered,
and grouped tables, plus the reported related-expansion chain's saved-cohort
availability. Source-only fixtures supply both a fully assigned named cohort and
a partially assigned cohort with one real unassigned record.
Counts come from one independently loaded raw FHIR Observation. Missing-value
error policies must return 422 `EMPTY_COLLECTION_ERROR`, never a compiler error,
an Apply button, or HTTP 200 `UNAVAILABLE`. The browser must keep the policy
control usable, allow switching to EXCLUDE in the same dialog, and persist that
repair through Apply and reload. Partially assigned cohorts under ERROR must
return 422 `EXPLICIT_GROUP_UNASSIGNED_MEMBER` with the same repair lifecycle.
Every unexpected executor or identity failure
fails verification; it must never be classified as expected validation.

Browser cases check actionable controls, proposal results, unchanged drafts,
Cancel for record restoration and cohort creation, Apply, reload, rendered row
counts, selected policy persistence, runtime/module/HTTP errors, and a
five-second preview limit. The process exits nonzero on failure and keeps each
case's exact API requests/responses and DOM evidence. API-only runs do not prove
browser usability. Existing QA Explorers are retained; the seed is never edited.

This matrix covers the base row-definition selector and cohort compatibility.
Root-type changes, starting-collection changes, and cohort member editing need
their own transition cases; a passing row matrix does not establish those flows.
Use `--browser-cases records,cohort-ERROR` to run a bounded browser diagnosis
after the complete API preflight. Omit that option for the full browser matrix.

## Upstream edit transitions

`node scripts/verify-upstream-edits.mjs --browser` creates disposable QA Explorers
and exercises expansion policy edits, expansion target changes, grouping-key
removal, and grouping-key addition with later dependent and independent filters.
Use `--related-seed EXPLORER` to select the saved expansion-chain reproduction;
grouping fixtures are created from one selected real CDA Observation. The script
never edits the seed Explorer.

Each case requires a valid preview and an explicit list of dependent removals,
an unchanged draft before Apply and after Cancel, preservation of independent
later filters, exact saved construction, and persistence after reload. Browser
clicks check visibility, disabled state, and overlays; native select popups use
enabled-option selection with input/change events after that actionability check.
Rendered identities/counts are compared with preview rows, and saved results
are checked against raw FHIR records. Action-to-preview time must stay under five
seconds. Requests, responses, correlated server logs, DOM evidence, browser
version, and timings are retained under `/tmp/loom-upstream-edits-*` by default.
Omit `--browser` for API-only diagnosis; that does not establish a UI pass.

## Legacy synthetic preconditions

- Run `make dev`, then require `make dev-doctor` to succeed.
- The default target is `loom-dev`, UI port 3180 and API port 8180. Never
  substitute the canonical `loom-demo` stack or its ports.
- Each browser run owns a fresh synthetic project. The stable manual fixture
  is separate. Retain run projects until an explicit development-volume purge.
- Run one verification command at a time. Do not edit source during full probes.

## Proof rules

Run `make verify-fast` for the complete browser scenario. Read its report and
linked DOM and CSV evidence. Failed or skipped assertions are not passes.
Setup uses ingestion APIs. Authoring, preview, publication, filtering, and
download use the real UI. Independent GraphQL reads check the stored result.

Generated column names vary. Match returned lineage to literal expected
values. Do not replace browser assertions with API-only checks.

## Features

- [Author a dataframe](authoring.md).
- [Preview and publish](publication.md).
- [Filter and export](viewer.md).
- [Iterate on source](iteration.md).

## Historical smoke-driver gaps

Authentication, real NCPI scale, multiple Observations per Patient, aggregate
authoring, projection editing, missingness profiling, pagination, charts, and
immutable training exports are not covered by the historical driver. Builder
features listed above still require new browser cases; do not treat these
historical gaps as exclusions.

### Root record type regression

`make verify-cda-root-settings` creates an owned CDA Explorer and checks native root-type preview, cancel, apply, fresh reload, and restoration. A Patient table with an authored Observation Subject route must retain that relationship without a second Subject/Focus choice. The starting collection and authored column IDs must survive both changes. Preview rows are checked against a bounded raw CDA relationship query; action-to-preview must stay within five seconds. Evidence is written under `/tmp/loom-root-settings-*`.

The root-settings regression also checks starting-collection coverage, clear, connection selection, reattachment, and fresh reload. Long connection labels must keep the dropdown within the viewport and reachable by a native click. The Make target includes three further Patient/Observation round trips to detect growing population-query work; each proposal and collection action must remain under five seconds. `LOOM_ROOT_REPEAT_CYCLES=0 node scripts/verify-cda-root-settings.mjs` runs the shorter lifecycle during diagnosis.

### Collection membership and Filter regressions

`node scripts/verify-cda-collection-repair.mjs` creates an owned Specimen table
and selects a real CDA Specimen with children but no parent. Its saved connection
must traverse to parent roots, rather than child roots. Coverage must identify
the unmapped member; removing it must preserve columns and the exact connection,
and the empty collection must survive reload. The raw edge query is retained
with the report under `/tmp/loom-collection-repair-*`.

`node scripts/verify-cda-filter-browser.mjs` creates an owned table containing
one independently read CDA Specimen. Native controls exercise missing-ID and
ID-equality filters, Cancel, Apply, reload, editing, removal, and restoration.
It records zero/one-row preview and saved-table assertions, browser failures,
and action timings under `/tmp/loom-filter-browser-*`. This source-table case
does not establish filtering after Group, Pivot, or Unpivot. Consult the ledger
for current pass/fail status; script existence alone does not establish a pass.

`node scripts/verify-cda-legacy-collection.mjs` reads an owned pre-fix QA Explorer
without applying commands. Set `LOOM_LEGACY_COLLECTION_SEED_REPORT` to its saved
collection-repair report; the default local seed is
`/tmp/loom-collection-repair-run-2/report.json`. The case requires a persisted
parent connection with `catalogEdgeId` and no `storageDirection`, compares it
with raw CDA edges, and asserts correct zero-row coverage and an unchanged
draft version, digest, and workspace. A missing legacy seed is a fixture gap,
not a pass. Pair this replay with focused Go tests that construct the legacy
shape independently of retained local artifacts.

### Related-record chain to Unpivot

`node scripts/verify-cda-related-unpivot-browser.mjs` creates an owned CDA
Specimen table and uses current server-issued choices through native controls
for Specimen → Patient → Condition → Observation → Patient. Each expansion is
checked against independent project/generation-scoped raw edges, including
preserved rows when a hop has no match. The case turns Specimen ID into rows,
checks the other record-ID columns, cancels and applies, reloads, edits the
Unpivot missing-value policy, and filters its generated Value column with
missing/equality conditions. It verifies filter Cancel, Apply, edit, and reload,
then removes Unpivot together with the dependent filter. The warning must name
both operations; Cancel must preserve them, and Apply must restore the exact
related construction after reload. Preview, Apply, and page-load timings
must remain under five seconds. Artifacts default to
`/tmp/loom-related-unpivot-browser-*`; the ledger names the latest verified run.
This case does not prove zero/many coverage merely because its oracle can
represent it, or prove adding new fields through retained related bindings.

### Related-record rows to Group and Filter

`node scripts/verify-cda-related-group-browser.mjs` creates an owned Specimen
collection, expands Specimen → Patient → Observation, and requires more than
one independently queried related record. It groups by Specimen ID, checks the
raw witness count, cancels and applies, reloads, edits to count distinct
Observation IDs, and filters the generated group key with missing/equality
conditions. Removing Group must warn about the dependent filter; Cancel
preserves both and Apply restores the exact related construction after reload.
The report records native control checks, independent CDA witnesses, browser
errors, and action-to-render timings under `/tmp/loom-related-group-browser-*`.
This case does not establish multiple groups, missing-key policies, or numeric
summary filtering. Consult the coverage ledger for verified evidence.

### Related-record rows to Pivot

`node scripts/verify-cda-related-pivot-browser.mjs` creates an owned Specimen
collection and expands Specimen → Patient → Observation. It pivots independently
queried Observation IDs into columns, grouped by Specimen ID, with the Patient
ID as each cell value. The oracle requires distinct category records and checks
all proposal cells and the persisted category set. Native controls exercise
Cancel, Apply, reload, missing-cell policy editing, and missing/equality filters on a generated
category column. Removing Pivot must name its dependent filter; removal Cancel
preserves both and Apply restores the exact related construction after reload. Reports default to
`/tmp/loom-related-pivot-browser-*` and enforce five-second action-to-render
bounds. This case does not prove category-field rediscovery, duplicate/missing
cells, or category-field edits with dependent filters.

### Editing Pivot categories with a dependent filter

`node scripts/verify-cda-pivot-category-edit-browser.mjs` replays the related
Pivot and generated-column filter lifecycle, then changes the category field
and grouping key. Discovery must select the new category automatically and
preview the independently queried Observation rows. The proposal must warn
that the obsolete filter is removed; Cancel preserves the draft, Apply removes
the warned filter, and reload preserves the new category and original collection.
Removing the edited Pivot must restore the exact related construction.
Reports default to `/tmp/loom-pivot-category-edit-browser-*`. Record performance
outliers separately; a later passing run does not erase an earlier failure.

`node scripts/verify-cda-pivot-reload-browser.mjs <evidence-dir>` uses Playwright
to open an owned wide-Pivot QA Explorer, sweep its virtualized columns, and
check five read-only reloads. Set `LOOM_PIVOT_RELOAD_SEED` to that Explorer's
creation report and set `LOOM_CDA_API_ORIGIN`, `LOOM_CDA_UI_ORIGIN`, and
`LOOM_CDA_API_CONTAINER` to an isolated CDA stack. The driver checks the saved
category set against the retained raw CDA witnesses, verifies exact visible
cell identities and an unchanged draft, enforces five seconds per reload, and
records source/build identity and first-failure evidence. This migration has
passed syntax and isolation-guard checks; it has no live CDA pass from this
branch because the loaded isolated CDA generation has no owned 31-category
Pivot seed report yet.

`node scripts/verify-cda-pivot-category-cycle-browser.mjs` changes an edited
Pivot field pair and returns to the original pair before saving. Both previews
must select discovered categories automatically and match the independent CDA
oracle. Cancel must preserve the entire saved workspace. This regression does
not establish Apply behavior or preservation of downstream column bindings
through the round trip. Reports default to `/tmp/loom-pivot-category-cycle-browser-*`.

The CDA Pivot field-cycle driver now applies a return to the original field pair with a downstream filter and checks exact saved construction after reload. Evidence: `/tmp/loom-pivot-cycle-refresh-fixed/report.json` (24 checks, maximum 2626 ms). This detects both regenerated category IDs and a stalled preview after a command advances the draft version without changing its digest. The intermittent second-expansion Apply failure remains open; one passing replay does not close it.

`node scripts/verify-cda-group-add-fields-browser.mjs` adds a scalar source field after a Group of 31 related CDA Observations, compares proposal cells with raw CDA, checks Cancel, then requires the same ordered cells after Apply/reload and removal. Current failure: adding Resource Type displays ID/count/type in the proposal but ID/type/count after Apply (`/tmp/loom-group-add-fields-complete/report.json`). The driver intentionally fails on the ordering mismatch; reload and removal are not proven.

The Group-to-source-field regression passes at `/tmp/loom-group-add-fields-final/report.json`: 16 native checks, maximum 2153 ms, no browser/HTTP errors. Field addition preserves proposal order through Apply/reload; removing it restores the exact prior construction after reload. New source presentation order accounts for final construction outputs, and generated outputs default to their final-stage position while respecting explicit orders. The saved-render wait also requires the expected column count to avoid reading the previous shape during removal. This covers a scalar root field with ALL; related contributor fields and ONE disagreement are separate gaps.

`node scripts/verify-cda-group-related-values-browser.mjs` exercises adding Observation.id through Specimen → Patient → Observation after Group. It requires a flat list equal to the raw 31 contributors, Cancel without mutation, Apply/reload, and column removal with exact Group restoration. Initial runtime failure is `/tmp/loom-group-related-values-before/report.json`: discovery advertises ALL but authoring rejects it as an unadvertised source projection. The driver remains failing until backend acceptance and lifecycle behavior are proved. Grouped-field coverage is now tested through ALL distinct contributing values, independent of a ONE selection; UI component suites pass 29 tests.

`node scripts/verify-cda-group-one-conflict-browser.mjs` groups two raw CDA sibling Specimens by their shared Patient. ONE must return exactly `CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES` and leave the saved workspace unchanged. Switching the retained selection to ALL must preview a flat, sorted pair of source IDs, then pass Apply, reload, and removal with exact Group restoration. The first extended run (`/tmp/loom-group-one-repair-lifecycle/report.json`) passed repair and persistence but removed a same-label upstream source column; the driver now gives that original source a distinct label. Full lifecycle replay remains pending. Column settings exposing consumed source columns beside final outputs is a separate recorded usability gap.

Both grouped-column regressions now pass: related ALL at `/tmp/loom-group-related-values-fixed/report.json` (16 checks, max2128ms), and retained-selection ONE-to-ALL repair at `/tmp/loom-group-one-repair-final/report.json` (14 checks, max2200ms). Each verifies exact raw CDA values, Apply/reload, removal, and Group restoration. Related ALL also verifies Cancel; ONE verifies the exact expected disagreement code and unchanged draft. Backend authorization now accepts server-resolved scalar ALL on related routes without weakening root scalar projection validation. COUNT/PRESENCE after Group and consumed-source column settings remain separate gaps.

The grouped ONE driver now also asserts final-output column-menu membership: a consumed Specimen ID has no table removal control, and adding a same-label grouped field offers exactly one removal control. This fails before the menu fix (`/tmp/loom-group-column-settings-before/report.json`) and passes after it (`/tmp/loom-group-column-settings-fixed/report.json`, 14 checks, max2425ms). The menu derives authored entries from the final construction outputs, preserving hidden final columns; source editing/removal remains in the existing advanced source setup. PreviewTable has 25 passing component tests. Native rename, hide/show, and drag ordering still need their own complete coverage.

`node scripts/verify-cda-group-related-summary-browser.mjs` checks related Observation COUNT after Group on an exact Specimen → Patient → Observation route; `LOOM_RELATED_FORM=PRESENCE` runs the existence variant. Both require scalar results equal to an independent raw CDA oracle, unchanged Group counts, Cancel, Apply/reload, saved-step label edit/Cancel/Apply/reload, removal, and exact restoration. Initial failures are retained at `/tmp/loom-group-related-count-before/report.json` and `/tmp/loom-group-related-presence-before/report.json`: both returned HTTP400 `constructionChoice.form is unsupported`. The fix retains compiler-proven contributing root keys only when needed, evaluates the signed related-source route across them, deduplicates terminal document identities, and preserves RELATED_SOURCE capability ownership when ROW_VALUES is also supported.

The driver selects two sibling Specimens and groups their expanded rows by their shared Patient. Its independent oracle deduplicates terminal Observation document identities across both contributors: the fixture has 62 expanded rows and 31 distinct Observations. `LOOM_RELATED_ZERO=1` adds an exact-value predicate absent from those records and requires COUNT `0` or PRESENCE `false`. All four variants pass: `/tmp/loom-group-related-count-final/report.json`, `/tmp/loom-group-related-presence-complete/report.json`, `/tmp/loom-group-related-count-zero/report.json`, and `/tmp/loom-group-related-presence-zero-final/report.json`. Positive COUNT and zero-match PRESENCE also cover saved-step label editing. Slowest measured action is 3264ms. The independent source oracle is retained at `/tmp/loom-group-related-summary-oracle.json`; nine synthetic real-Arango COUNT/PRESENCE/ALL cases also pass at `/tmp/loom-group-related-arango-result.log`, including shared targets, equal values on distinct records, empty results, predicates, and project/generation isolation. COUNT after Pivot/Unpivot and zero-match PRESENCE after Pivot are covered below. Other native form/predicate combinations, semantic form/route/predicate replacement, and full-population performance remain unverified.


Reshape composition: `LOOM_SUMMARY_SHAPE=PIVOT|UNPIVOT` extends the grouped-related driver with native reshape preview/Cancel/Apply/reload before adding the summary. COUNT passes both shapes: `/tmp/loom-pivot-related-count-reselection-fixed/report.json` (29 checks,max2347ms) and `/tmp/loom-unpivot-related-count-lifecycle/report.json` (29,max2320ms). Pivot with `LOOM_RELATED_FORM=PRESENCE LOOM_RELATED_ZERO=1` also passes the complete lifecycle at `/tmp/loom-pivot-related-presence-zero-scroll-fixed/report.json` (29,max3123ms). Each includes saved-label edit/Cancel/Apply/reload and summary removal with exact shape restoration. Other native form/predicate combinations remain unverified.

The compiler fixture now exercises 27 real-Arango cases across Group/Pivot/Unpivot × COUNT/PRESENCE/ALL × positive/registered/absent predicates, including shared terminal records, equal values on distinct records, and project/generation isolation (`/tmp/loom-reshaped-related-arango-result.log`). This proves compiler results, not all native UI interactions. Single-column Unpivot capability and unchanged Pivot field selection have failing-before regressions. Native click tests also cover a control clipped by its dialog despite being inside the viewport; scrolling recovery must still reject overlays and disabled fieldsets.


Upstream/source transitions: `LOOM_UPSTREAM_GROUP_EDIT=1 node scripts/verify-cda-group-related-summary-browser.mjs` removes the Patient grouping key while retaining signed RELATED_SOURCE COUNT. Full lifecycle passes at `/tmp/loom-upstream-group-related-count-lifecycle/report.json` (27 checks,max2543ms), including unchanged summary operation, Cancel, Apply/reload, label editing and removal with exact edited-Group restoration. `LOOM_COLLECTION_LONG_ROUTE=1 node scripts/verify-cda-collection-repair.mjs` passes unmapped-member repair across Observation → Specimen → incoming parent, clear to scoped authorized rows, native exact-connection reattachment, and reload (`/tmp/loom-collection-long-route-lifecycle/report.json`,9 checks,max2383ms).

`LOOM_COLLECTION_ROUND_TRIP=1 node scripts/verify-cda-group-related-summary-browser.mjs` now passes the full-population lifecycle (`/tmp/loom-group-related-collection-roundtrip-all-rows/report.json`): 27 native checks, maximum3233ms when clearing the selected collection. All25 virtualized preview groups match independent scoped CDA multiplicity and related-count queries. Reattachment, Cancel/Apply, reload, editing, and removal pass without HTTP/runtime errors. The compiler selects exact complete group contributors before canonical expansion; sparse candidates fall back to canonical execution, and full execution is unchanged. Real Arango regressions exercise duplicate FHIR IDs/edges, shared roots, null/unmatched groups, authorization/project/generation boundaries, terminal Group previews, and sparse candidate fallback. The former504 remains documented in `/tmp/loom-group-related-collection-timeout-regression/report.json`. This proves the narrow first-related-FHIR-ID Group plus COUNT_ROWS shape; other full-population group/summary shapes remain untested.


Cohort member fields: `node scripts/verify-cda-cohort-fields-browser.mjs` and `LOOM_COHORT_FIELD=id node scripts/verify-cda-cohort-fields-browser.mjs` pass 11 native checks each against owned CDA Explorers. Evidence: `/tmp/loom-cohort-fields-ui-order-final/report.json` (maximum 2026ms) and `/tmp/loom-cohort-distinct-ids-ui-order-final/report.json` (maximum 2010ms). Both exercise row-definition Cancel/Apply, member-field Cancel/Apply, exact field values and column order after reload, removal and exact document restoration. The distinct-ID case compares the member-field cell with both independently queried source IDs. Real Arango tests cover hashed storage keys, scoped authorization, repeated values, empty/unassigned groups and ONE disagreement. Member-field editing remains untested; composed policy editing is verified below.

Cohort composition regression: `LOOM_COHORT_COMPOSITION=1 LOOM_COHORT_FILTER_ONE=1 LOOM_COHORT_FIELD=id node scripts/verify-cda-cohort-fields-browser.mjs` now passes the source equality filter → pinned cohort → member-field lifecycle. Evidence: `/tmp/loom-cohort-filter-composition-preview-fixed/report.json`, 11 timed checks, maximum2007ms, exact independent CDA values through Cancel/Apply/removal/reload. Typed cohort stages replace the former terminal-only restriction. Preview skips invalid scalar source identity for grouped rows, and aggregation receives the full input; receipt integrity guards remain unchanged.


The cohort composition wave now also has a discriminating native case: `LOOM_COHORT_COMPOSITION=1 LOOM_COHORT_FILTER_ONE=1 LOOM_COHORT_FIELD=id node scripts/verify-cda-cohort-fields-browser.mjs`. Before the fix it verifies a saved equality filter keeps exactly one of two independent CDA witnesses, then fails because the cohort option is absent (`/tmp/loom-cohort-one-member-filter-before/report.json`, load 1774ms). After backend integration the same driver must exclude the other ID from both cohort members and the selected member-field cell, including reload. `LOOM_COHORT_POLICY_EDIT=1` adds policy preview/Cancel/Apply/reload and asserts that selected member fields, operation positions and columns survive a same-cohort policy change; the composed variant passes in `/tmp/loom-cohort-policy-composition-first/report.json` (15 checks, maximum2181ms). `docs/COHORT_COMPOSITION.md` records the chosen stage integration and required semantic checks. The real-Arango retained-source-filter regression now passes. `TestCohortPreviewWithSmallerRootPageKeepsAllMemberValuesAgainstArango` additionally verifies source EXISTS filter → typed cohort at RootPageRows=1 returns both pinned member IDs without root paging.


`LOOM_COHORT_POST_FILTER=1` extends the cohort member-field driver with a downstream Group-label filter: MISSING preview/Cancel/Apply and empty reload, edit to EXISTS with exact cohort cells restored, then removal/reload. The composed variant passes `/tmp/loom-cohort-post-filter-exists-fixed/report.json`:24 native timed checks,max2040ms, including preview/Cancel/Apply, edit, removal and reload. Backend normalization resolves virtual cohort input schemas, and the editor can reopen value-free EXISTS conditions. The driver captures owned native request bodies, HTTP status, server request IDs, and proposal/preview/error response bodies in `nativeRequests`, with explicit 32KB truncation markers. Large successful catalog bodies are summarized while exact saved workspaces remain in evidence. Native execution must validate this capture as well as the new feature cases; syntax checks alone do not prove them.

Cohort collection round-trip driver: `LOOM_COHORT_COMPOSITION=1 LOOM_COHORT_COLLECTION_ROUND_TRIP=1` clears the selected starting collection to full authorized CDA, checks exact pinned cohort cells and unchanged member fields/construction boundary, reloads, then reattaches the original collection and reloads. The complete native roundtrip now passes `/tmp/loom-cohort-collection-recovery-fixed/report.json`:16 timed checks,max2019ms. Candidate-root narrowing fixes the former full-CDA timeout; validated pinned-cohort metadata restores the source selection after reload. Exact old-revision lookup and scope/membership rejection pass seven focused backend cases. The driver now requires a fresh successful saved-preview response after each collection action within five seconds, preventing stale-cell false positives. Unsafe aggregate prefixes keep their full input; duplicate-ID filtering and selected unassigned values pass the real-Arango regression.

Cohort insertion-point removal: `LOOM_COHORT_COMPOSITION=1 LOOM_COHORT_REMOVE_ANCHOR=1` removes the source EXISTS filter after cohort/member-field creation. Requires native preview/Cancel/Apply/reload to retain cohort intent and fields while rebasing its insertion point to source projection. Native lifecycle passes `/tmp/loom-cohort-anchor-removal-fixed/report.json`:16 timed checks,max2013ms. Preview/Cancel/Apply/reload preserves exact cohort member fields and policy; Apply828ms. Atomic command application now clones and applies receipt-validated construction and row intent together before validation.

Row-action clarity: `LOOM_QA_EXPLORER=cohort-add-fields-browser-1790909921995 node scripts/verify-row-actions-clarity.mjs` passes native desktop/mobile action-card and editor checks, preserving the exact saved workspace. Evidence: `/tmp/loom-row-actions-records-and-fields/report.json` with viewport screenshots. Explains repeated values, unmatched-row default and related field availability after Apply. This is a copy/layout check; it does not prove transformation Apply correctness or latency. The same verifier exposed a disabled related expansion on keyless grouped rows in `/tmp/loom-row-actions-clarity-first/report.json`; contributor-set expansion now passes the full native lifecycle described below.

Composed cohort policy edit passes: `LOOM_COHORT_COMPOSITION=1 LOOM_COHORT_POLICY_EDIT=1 node scripts/verify-cda-cohort-fields-browser.mjs`; `/tmp/loom-cohort-policy-composition-first/report.json`, 15 checks, max2181ms. Policy preview/Cancel/Apply/reload retains member fields and persisted boundary; fresh saved-preview completion is required.

Grouped source expansion: `LOOM_EXPAND_AFTER_GROUP=1 node scripts/verify-cda-group-related-summary-browser.mjs` passes 26 timed native transitions (maximum2045ms) at `/tmp/loom-grouped-related-expansion-virtualized-fixed/report.json`. Two independently selected Specimens contribute to one Patient after grouping, then 31 distinct onward Observations. Full saved previews check exact storage identities and stable row values after reload. Cancel, Apply, no-match policy editing, removal and exact restoration pass. Scoped real-Arango coverage checks all three no-match policies and authorization/project/generation exclusions.

Grouped related filtering now passes both native lifecycles: `LOOM_EXPAND_AFTER_GROUP=1 LOOM_GROUPED_RELATED_FILTER=1 node scripts/verify-cda-group-related-summary-browser.mjs`, with `LOOM_GROUPED_FILTER_DIRECT=1` for filtering directly after Group. Evidence: `/tmp/loom-grouped-related-filter-clone-fixed/report.json` and `/tmp/loom-grouped-related-filter-direct-fixed/report.json`, 42 timed transitions each, maxima2053ms/2032ms. Both verify EXISTS, ABSENT, distinct COUNT, Cancel, Apply, count editing, removal and reload. Compiler support fixes the initial unsupported contributor anchor. Deep-cloning function arguments fixes the later COUNT failure: receipt fingerprinting had mutated live execution metadata and produced a misleading receipt-conflict diagnostic. The editor explains that each matching related record counts once per current row even when multiple starting records link to it. Scoped Arango tests cover distinct counts and authorization/project/generation exclusions; source-record listing remains separately unverified.

Saved filter operators: Set `LOOM_SAVED_FILTER_OPERATOR` to `NOT_EQUALS`, `IN`, or `CONTAINS_TEXT` when running `scripts/verify-cda-filter-browser.mjs` (one value per run). All three now pass complete native lifecycles: `/tmp/loom-saved-not-equals-fixed/report.json` (16 transitions,max2047ms), `/tmp/loom-saved-in-mixed-values-fixed/report.json` (20,max2154ms), and `/tmp/loom-saved-contains-text-fixed/report.json` (16,max2152ms). Each independently queries a scoped CDA Specimen ID, API-seeds a supported saved condition, reopens preserving operator and values, edits through Cancel/Apply/removal/reload, then creates the condition natively through Cancel/Apply/removal/reload. Membership checks two absent values exclude the row, absent+real ID includes it, removing the real member excludes it, Cancel restores the list and reload preserves the edit. Text matching checks both absent text and a matching substring. Initial NOT_EQUALS failure remains recorded at `/tmp/loom-saved-not-equals-red/report.json`: root-key paging validation rejected a physical operator supported by normal AQL rendering. The validator fix is covered by focused paging tests and the full AQL renderer suite; editor unit tests and typecheck pass. Numeric/date ordered comparisons and boolean/code value controls still require distinct data-backed native coverage. These string-family passes do not close aggregate filter coverage.

Typed filter coverage extends the same driver: `LOOM_SAVED_FILTER_OPERATOR=GT` uses a real CDA Observation quantity and checks all four ordered boundaries with independent exact FHIR IDs at `/tmp/loom-saved-decimal-identity-boundaries-fixed/report.json` (28 transitions,max2657ms). GT at equality excludes; GTE/LTE include; LT excludes. Each boundary passes edit/Cancel/Apply/reload and whole-filter removal restores the source. `LOOM_FILTER_VALUE_TYPE=BOOLEAN LOOM_SAVED_FILTER_OPERATOR=NOT_EQUALS` uses real Substance.instance at `/tmp/loom-saved-boolean-identity-fixed/report.json` (16,max2032ms), proving native true/false selection, typed false persistence, exact source identity, saved editing/Cancel/Apply/removal/reload. Catalog evidence `/tmp/loom-typed-filter-inventory.json` lists158 advertised candidates:153 string,3 decimal,1 integer,1 boolean. This snapshot exposes no DATE/DATE_TIME/CODE logical source columns; code fields use strings. Date controls remain unverified due current catalog reachability, without claiming raw fixture-wide absence. Integer filtering after Group is inventoried as a separate changed-row-shape sequence.

Grouped integer filtering: `LOOM_FILTER_VALUE_TYPE=INTEGER_GROUP LOOM_SAVED_FILTER_OPERATOR=GT node scripts/verify-cda-filter-browser.mjs` passes at `/tmp/loom-grouped-integer-stage-fixed/report.json` (32 native timed transitions,max2077ms). Independent scoped CDA Observation membership proves the count1 group, with exact FHIR ID and count displayed. Group creation covers preview/Cancel/Apply/reload; filtering discovers the Count column on the Group output, preserves typed INTEGER including zero, and checks all four ordered boundaries through Cancel/Apply/reload. Saved-filter editing and native creation/removal restore the upstream Group exactly, including source columns and population. Initial `/tmp/loom-grouped-integer-first/report.json` was a harness-only click during capability loading; the enabled-control wait remains included in action-to-render timing. Use the stage descriptor id for STEP_OUTPUT inputs. Upstream Group edits while a count filter is active and repeated integer member projection remain distinct future checks.

Upstream Group editing with an active count filter: add `LOOM_GROUP_FILTER_UPSTREAM_EDIT=1` to the INTEGER_GROUP invocation. `/tmp/loom-grouped-integer-upstream-binding-fixed/report.json` passes39 native timed transitions,max2431ms. Removing the FHIR-ID grouping key previews count-only rows, Cancel leaves the exact workspace unchanged, Apply/reload preserve the LTE INTEGER1 predicate. Restoring the key recovers its same input-column binding and exact public ID/count values; the Count column retains its identity/schema. A recreated key legitimately receives a new opaque output-column ID, so compare bindings rather than the deleted key ID. Final filter removal preserves the restored Group exactly. The earlier two reports failed only verifier assumptions about omitted empty keys and recreated output IDs, with no unexpected browser/API errors. Source-record inspection remains a separate open feature: PreviewTable shows a source-list limitation when transformed-row lineage capability is unavailable; native independent-contributor/pagination checks are still needed.

Related-record explanation now includes a visible Patient/Observation before-and-after example and separates repeated rows from newly available fields. Native desktop/mobile checks assert both explanations and unchanged saved workspace. Evidence: `/tmp/loom-row-actions-records-and-fields/report.json`.

Source-record inspection after ordinary Group: `rtk proxy node scripts/verify-cda-row-sources-browser.mjs /tmp/loom-row-sources-complete` passes on real CDA. Independent105 scoped Specimen IDs equal the full five-page native contributor list, repeated after reload with stable row identity and unchanged workspace. Group preview/Cancel/Apply and removal preview/Cancel/Apply restore source rows, columns and population.22 timed actions max1934ms, no browser/HTTP/module errors. The regression also exposed an offscreen Close button; PreviewTable now bounds the dialog height and keeps its header visible, proved at413px viewport. Evidence: `/tmp/loom-row-sources-complete/report.json`; initial backend RED `/tmp/loom-row-sources-first/report.json`; dialog RED `/tmp/loom-row-sources-fixed/report.json`. Compiler capability now distinguishes publication grain from validated physical Group identity. Five focused tests include actual scoped Arango contributor paging, auth/project/generation exclusions and forged/wrong-stage identities. CODED_GROUP/RELATED_EXPAND identity failures were resolved and verified in the following direct-operation wave. Cohort, Pivot and other contributor transitions remain unproven as recorded in the ledger.

Direct coded-group and one-hop related-expansion source inspection now pass in the generalized `scripts/verify-cda-row-sources-browser.mjs` driver. `LOOM_LINEAGE_MODE=CODED_GROUP` verifies105 independent scoped Observation IDs in the specimen_type group over five native pages, all three code/count rows, reload/identity/read-only workspace, missing-code policy edit with Cancel/Apply/reload, and removal restoration.31 timed actions max4528ms. Before Chrome, this mode requires an executed scoped Arango query test for duplicate-code dedup, paging, missing tuples, auth/project/generation exclusions and forged IDs. `LOOM_LINEAGE_MODE=RELATED_EXPAND` verifies a real scoped Specimen→Patient edge, both exact contributor IDs, reload, no-match policy edit with Cancel/Apply and removal restoration;19 actions max2025ms. No browser/HTTP/module errors in either run. Evidence: `/tmp/loom-coded-row-sources-fixed/report.json`, `/tmp/loom-related-row-sources-fixed/report.json`; prior native REDs use corresponding `-red` paths. Compiler fixes distinguish physical identity from publication grain for both operations, apply coded pagination offset once, and prevent found:false requests from collecting null coding tuples. The query-layer missing-tuple defect was reproduced in the scoped Arango test; the execution layer already rejects found:false results, so this is not evidence of an API disclosure. Ordinary Group with trailing filters is verified in the following composition wave. Other multi-stage, multi-hop, cohort and Pivot lineage remain open.

Ordinary Group followed by multiple filters now retains source inspection. Run `rtk proxy env LOOM_LINEAGE_FILTER=1 node scripts/verify-cda-row-sources-browser.mjs /tmp/loom-group-filter-row-sources-fixed`. Evidence `/tmp/loom-group-filter-row-sources-fixed/report.json`:62 timed native actions,max2051ms,no unexpected errors. Independently selected105 scoped Specimens match five contributor pages through two filters and reload. Editing the earlier filter excludes the group and its old identity returns404 without contributors. Removing that filter preserves the later condition; removing both restores the exact Group, then Group removal restores source columns/population. Preview/Cancel/Apply/removal/reload are checked. Preflight requires the actual scoped Arango regression with50/50/5 pages,past-end,early and final filter exclusion,forged/wrong-stage identities,and authorization/project/generation witnesses. The renderer evaluates final membership before paging contributors; COUNT_ROWS uses a streaming selected-group count. Non-count candidate compilation and the full AQL renderer suite pass, but native non-count inspection remains unproven. Cohort,Pivot,multi-hop and other stage compositions remain open.

Workspace reconciliation unit contracts: `rtk proxy node scripts/verify-builder-reconciliation.mjs /tmp/loom-builder-reconciliation-fixed` records26/26 passing tests and rejects skipped or missing required contracts. RED `/tmp/loom-builder-reconciliation-red/report.json` reproduced9 stale-fixture failures. Corrected fixtures follow current row-settings entry points, construction-choice and row-change preview-before-Apply, current column callback, and COMPLETE category discovery. Preview nonmutation, atomic commands/receipts, pending-command feedback, selection handoff, table-switch request abortion and rejection of a late old-selection response remain asserted. Test TypeScript check passes. This is unit-infrastructure coverage; it does not prove a new native CDA lifecycle or close the intermittent second-expansion preview/performance failures.
