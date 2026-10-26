# Builder journey audit goal

## Status

The historical journey audit completed all eight inventory requirements as of
2026-10-21. The authored-three and raw-Basic Group source cases passed on
`0b383cda0ac70f735180de4b669e4d9e2a551b35`: 70/70 and 62/62 native
assertions respectively, with 17 required checks each and passing
BuilderJourney replays.

After the Basic branch retirement, the public `edit-remove-reload` alias
selected the raw-Basic native case on `908b49fdff0fb34a6dba3c129f2344f8c37d12c4`
and passed 62/62 assertions, all 17 checks, and an 18-action replay. The
separate minimal Basic budget probe passed with 28 `/authoring/v2/` responses
inside its preserved 10–45 range and replayed through BuilderJourney. The raw
native run records 59 responses, so it remains a separate exact-data lifecycle.
The CDA extension-grouping branch remains manual-only, unrun, and outside CI
automation coverage. Final-908 reports, trace controls, and source/API/mount
bracket captures are retained under
`docs/verification/playwright/runtime/builder-group-entry-20261023/final-908/`;
exact run identities and hashes are in the audit JSON.

This case-specific evidence does not establish browser-ledger freshness or
global readiness. Multi-report browser-evidence generation remains blocked by
a missing historical `builder-controls` report and external-path basename
handling. F-001 and F-004 remain open product findings; their journey specs
stay red as evidence. The six-column source accountability and QC trail for the
678 MJS files is retained in [the decision TSV](verification/mjs-catalogue-decisions.tsv).

## Goal contract

Objective: create and maintain real Builder journey tests that drive the
frontend UI and backend API together, assert exact outcomes or typed errors, and
validate traces against the matching TLA model. The goal is coverage, not green
tests. Product failures may remain red.

Requirements:

1. Every inventory row resolves to a journey spec under
   `scripts/verify-ui/journey-specs/`, a registered native case, or a recorded
   reason it cannot exist.
2. Every action spec starts from a fresh synthetic fixture or a preflighted CDA
   generation, drives the real UI, and makes 10 to 20 `/authoring/v2/` calls.
   A successful action changes the table at least once. A typed-error journey
   must reach the product action and record the error; it may leave the table
   unchanged. The historical standalone Basic Group path asserted 10–45
   `/authoring/v2/` responses; its final minimal budget probe recorded 28 on
   source `908b49fd`. The raw-Basic native lifecycle records 59 because it
   includes additional owner, cancellation, and repeat-edit checks. The
   budget-only probe is separate from the exact-data acceptance case. The
   read-only `load-preview` baseline records at least 8 calls.
3. Every terminal run records every authoring call and asserts an exact visible
   outcome or a typed product error.
4. Every modeled trace replays through the matching TLA model.
5. Every product failure stays red and has a finding with a request ID, HTTP
   status, and artifact path. No skip, fixme, or expected-failure annotation.
6. At least three inventory rows have a terminal CDA run.
7. The public journey runner selects one row with `--journey <id>`; a mapped
   native case resolves through its registered scenario and case selection.
8. The read-only fast lane lists only read-only specs.

Constraints:

- Do not replace the obligation suite or the TLA+ safety checks.
- A passing journey does not prove the whole Builder.
- The journey suite is not a CI-blocking gate.
- Do not count per-action probes as journeys.

## Why this layer exists

The existing suites cover per-action contracts, lifecycle schedules, and TLA+
models. They do not show that a sequence of real user actions produces the right
table or the right error through the real frontend and API. This goal adds that
integration audit.

## Non-goals

- This goal does not replace the existing obligation suite or the TLA+ safety
  checks.
- A passing journey does not prove the whole Builder.
- The journey suite is not a CI-blocking gate. Run it with
  `npm run verify:builder-journey` or the CDA command below.
- A journey is not a per-action probe. Single-operation journeys have 10 to 20
  backend calls. The historical standalone Group route asserts a 10–45 call
  range; its budget-only probe remains separate from the exact raw-Basic native
  lifecycle, which recorded 59 responses. Every full journey has at least one
  visible outcome.

## Test contract

Every journey must:

1. Start from a fresh synthetic fixture or a preflighted CDA generation.
2. Open the Builder through the real UI. Do not seed the final table state and
   call the assertion a UI test.
3. Perform a user sequence that changes the table or preview. The read-only
   `load-preview` baseline may skip the table change. A typed-error journey
   must reach the product action and record the error; it may leave the table
   unchanged.
4. Record every `/authoring/v2/` request and response status.
5. Assert the exact visible outcome or a typed product error.
6. Write a trace with `backendCalls` and either `outcome` or `error`.
7. Replay the trace through the matching TLA model when one exists.
8. Leave a non-passing product result red. Do not use `test.skip()`,
   `test.fixme()`, or `test.fail()` to hide it.

## Failure classes

Use one class per terminal run:

- `pass`: the exact expected outcome appeared.
- `product-failure`: the test reached the product action and recorded a wrong
  result, a wrong status, or a typed server error.
- `harness-failure`: the test could not reach the product action because the
  test driver, fixture, or browser failed.
- `blocked-dataset`: dataset preflight rejected the project or generation.

Only `product-failure` is product evidence. A harness failure does not close a
journey.

## Journey inventory

The machine-readable list is
`verification/builder-interactions/builder-journey-audit.json`.

| ID | User sequence | Exact oracle | Status |
| --- | --- | --- | --- |
| `load-preview` | Open the authored table and wait for the first preview. | Expected columns and row count from the fixture. | passing on synthetic and CDA |
| `group-twice-preview` | Select rows, Group, Group again, preview. | One row, `row_count = 1`, preview receipt present. | passing on synthetic and CDA |
| `group-pivot-preview` | Select rows, configure the pivot group key, category, and value, preview. | Pivot columns and cell values from the fixture. | product-failure; red spec exists, F-001 |
| `add-column-preview` | Select rows, add a scalar column, preview. | Exact source columns and added values survive preview. | passing on synthetic and CDA |
| `filter-preview` | Select rows, filter, preview, cancel. | Exact filtered rows, then the original rows after cancel. | passing on synthetic |
| `unpivot-preview` | Select rows, stack columns, preview. | Exact field and value rows. | product-failure; red spec exists, F-004 |
| `edit-remove-reload` | Native raw-Basic Group lifecycle, with authored-three independently registered. | Raw Basic passed 62/62 assertions on source `0b383cda` and again through the retired public alias on `908b49fd`; the final run passed all 17 required checks and replayed 18 actions. It checks exact five-column `Patient.ndjson` values, ID-plus-gender preview/apply, second ID-only edit, and source restoration after reload. Authored-three separately passed 70/70 on `0b383cda`. TLA models proposal/apply owner chronology; Cancel and reload remain native-only checks. | native raw-Basic alias passed on 908; authored-three historical pass on 0b |
| `edit-remove-reload-basic-budget` | Minimal standalone Basic Group/edit/remove/reload flow. | Preserves the retired Basic branch's exact 10–45 response census and makes no raw-data claim. The fresh synthetic run recorded 28 responses and passed BuilderJourney replay on `908b49fd`. | manual-only pass; not CI automation coverage |
| `edit-remove-reload-cda-manual` | CDA extension-value-code Group/edit/remove/reload flow. | Checks CDA source setup, grouped row/count shape, restored row/column counts, and visibility of the first two source IDs. It does not compare every raw source row. | manual-only, unrun, and outside CI automation coverage |
| `pivot-error` | Pivot with error policies. | Typed 4xx product error, no applied draft change. | product-failure; red spec exists, F-001 |

## Completion check

The authored-three and raw-Basic native Group lifecycles passed on source
`0b383cda`; the raw-Basic public alias rerun and separate minimal Basic budget
probe passed on `908b49fd`. The budget probe recorded 28 responses within the
original 10–45 range. The raw native run recorded 59 responses and remains a
separate exact-data lifecycle. The CDA extension route remains manual-only and
unrun. F-001 and F-004 remain open product findings; this goal requires real
evidence, not green tests.

## Requirement audit (updated 2026-10-22; historical audit dated 2026-10-21)

1. Specs: the historical audit had eight journey specs. The current manifest
   maps `edit-remove-reload` to the registered native
   `builder-authoring/group-entry-raw-basic` case; the authored-three case is
   separately registered. Both originally passed on source commit `0b383cda`,
   and the raw-Basic public alias passed again on `908b49fd`. The manual Basic
   budget row passed with 28 responses on `908b49fd`; it is not CI automation
   coverage. The separate CDA row remains manual-only and unrun.
2. Call counts: `load-preview` 8; `group-twice-preview` 17; `group-pivot-preview`
   11; `add-column-preview` 13 synthetic and 14 CDA; `filter-preview` 14;
   `unpivot-preview` 10; the historical standalone Basic run had 28; the
   final raw-Basic native report has 59 authoring responses, above the retained
   10–45 budget; `pivot-error` 11. The separate 28-response budget-only result
   preserves the minimal-flow bound.
   Typed-error
   journeys reach the product action and record the error; `pivot-error`
   additionally asserts no draft command and an unchanged table.
3. Terminal evidence: historical green journeys wrote `outcome`; each red
   journey writes `outcome` with `httpStatus`, `errorClass`, and `requestId`.
   The `pivot-error` trace records `commandCount: 0`. The native Group report
   records 70/70 assertions and 17/17 required checks.
4. TLA: the Group trace has one unique validated trace, 24 actions, and passes
   `BuilderJourney` with 26 generated states, 25 distinct states, and depth 25.
   Group-edit Cancel, Group-removal Cancel, and both reloads are explicitly
   reported as unsupported by TLA and checked only by the native workflow.
   `BuilderJourney.cfg` passes safety with 227 generated states and 53
   distinct states.
5. Findings: F-001 and F-004 are open with request IDs, HTTP status, finding
   artifacts, and red specs. F-003 was not reproduced by the historical
   ID+gender rerun; both native Group shapes now pass that independent
   preview-and-Apply check. CDA remains a separate unrun manual route. No
   journey spec uses `test.skip`,
   `test.fixme`, or `test.fail`.
6. CDA: `group-twice-preview`, `load-preview`, and `add-column-preview` have
   terminal CDA runs with distinct `*-cda-trace.json` and `*-cda-report.json`
   artifacts.
7. Runner: the `edit-remove-reload` alias selects the registered
   `builder-authoring/group-entry-raw-basic` case; `group-entry` remains a
   separate authored-three native case. The Basic budget-only row selects the
   same standalone spec with `LOOM_JOURNEY_DATASET=basic-budget`, and the CDA
   manual row selects it with `LOOM_JOURNEY_DATASET=mini` from the CDA manifest.
   Both standalone routes use distinct trace names and contracts.
8. Fast lane: `scripts/verify-ui/fast-specs/` contains only
   `builder-preview-read-only.spec.mjs`.

## Findings

### F-001 Pivot proposal returns HTTP 500

- Journey: `group-pivot-preview`
- Request: `POST /api/v1/projects/<project>/explorers/<explorer>/authoring/v2/construction-proposals`
- Input: group by `id`, category `gender`, values `name__count`, duplicate policy `ERROR`, missing cell policy `NULL`, unlisted category policy `ERROR`
- Result: HTTP 500 `CONSTRUCTION_RECEIPT_INPUT_MISSING`
- The response names the missing construction input column and states that the authored construction and compiler schema disagree.
- Request ID: `construction-proposal-8bc2c5fa-9767-4e3b-b43e-37229cf55622`
- Latest red-run request ID: `construction-proposal-beb11c39-a416-4ae4-843e-05828a8a9afd`
- Artifact: `docs/verification/playwright/runtime/builder-journey-F-001.json`
- Red specs: `scripts/verify-ui/journey-specs/group-pivot-preview.spec.mjs` and `scripts/verify-ui/journey-specs/pivot-error.spec.mjs`
- Latest red-run trace: `.artifacts/builder-journey/pivot-error-descriptive-trace.json`
- Latest red-run report: `.artifacts/builder-journey/pivot-error-descriptive-report.json`
- Status: open with a red spec
- Next step: diagnose the server error. The red specs assert the desired
  HTTP 200 and exact pivot output, plus a typed 4xx with no draft change for
  `pivot-error`.

### F-002 Full formal gate is blocked by source pin drift

- Gate: `node scripts/verify-builder-formal.mjs --model builder-journey`
- Result: the gate refuses to start because
  `BuilderWorkspace.tsx` does not match the pinned source hash.
- Status: pre-existing, unrelated to the journey runner
- Workaround: the journey runner validates its trace with TLC directly.
  Direct `BuilderJourney.cfg` model checking passes.

### F-004 Unpivot proposal returns HTTP 500

- Journey: `unpivot-preview`
- Request: `POST /api/v1/projects/<project>/explorers/<explorer>/authoring/v2/construction-proposals`
- Input: stack `name[].family [0]` and `name[].family [1]` into key/value rows with `PRESERVE` for missing values
- Result: HTTP 500 `CONSTRUCTION_RECEIPT_INPUT_MISSING`
- The response names the missing construction input column and states that the authored construction and compiler schema disagree.
- Request ID: `construction-proposal-21f5a84e-4815-4e43-8d61-203db897bd0e`
- Artifact: `docs/verification/playwright/runtime/builder-journey-F-004.json`
- Red spec: `scripts/verify-ui/journey-specs/unpivot-preview.spec.mjs`
- Latest trace: `.artifacts/builder-journey/unpivot-preview-descriptive-trace.json`
- Latest report: `.artifacts/builder-journey/unpivot-preview-descriptive-report.json`
- Status: open with a red spec
- Next step: diagnose the unpivot proposal server error. The red spec asserts
  HTTP 200 and a four-row key/value preview.

### F-003 Field-based Group proposal returns HTTP 500

- Journey: historical Basic branch of `edit-remove-reload`; the CDA
  extension-field branch remains a separate manual-only journey
- Request: `POST /api/v1/projects/<project>/explorers/<explorer>/authoring/v2/construction-proposals`
- Input: group by `id` and `gender` on a fresh synthetic Patient table
- Historical result: HTTP 500 `INTERNAL_ERROR`
- Historical request ID: `construction-proposal-e8f37264-d8f5-46c5-adf6-738f0b1e53ff`
- Historical rerun: HTTP 200 with two rows and three columns (`id`, `gender`, `Row count`)
- Artifact: `docs/verification/playwright/runtime/builder-journey-F-003.json`
- Trace: `.artifacts/builder-journey/edit-remove-reload-trace.json`
- Status: the error was not reproduced in the historical rerun. The registered
  raw-Basic native case now independently proves the five-column values and
  ID-and-gender preview/apply, followed by ID-only persistence and exact source
  restoration. The authored-three native case remains a distinct source shape.
- Next step: diagnose the still-red pivot and unpivot product journeys.
  Keep the CDA Group route manual-only and unrun until its own browser evidence
  exists.

## Recorded evidence

Most runs below are historical evidence. In particular, the first 28-call
Basic `edit-remove-reload` artifacts came from the standalone spec before the
native Group cases were introduced. The 0f authored-three report records only
that source snapshot. The 0b source has distinct authored-three and raw-Basic
native reports; final-908 reports below record the raw-Basic alias rerun and
minimal Basic budget probe. The candidate keeps CDA extension-field assertions
in a manual-only entry.

- `group-twice-preview` on a fresh synthetic fixture: 10.2 s, 17 authoring
  calls, exact `{ row_count: 1 }`, trace validation pass.
- `group-pivot-preview` on a fresh synthetic fixture: red as designed, 11
  authoring calls, HTTP 500 `CONSTRUCTION_RECEIPT_INPUT_MISSING`, trace validation pass. The
  runner reports `JOURNEY_PRODUCT_FAILURE` and exits 1.
- `group-twice-preview` on `loom_dev_cda_fhir` generation
  `repo-c0ec9689932caec424ad34a9` with a fresh `loom-cda-group-twice-2`
  Explorer: 18.1 s, 17 authoring calls, exact `{ row_count: 1 }`, trace
  validation pass.
- `load-preview` on a fresh synthetic fixture: 12.7 s, 8 authoring calls,
  exact two rows and five columns, unmodeled baseline.
- `load-preview` on `loom_dev_cda_fhir` generation
  `repo-c0ec9689932caec424ad34a9` with a fresh `loom-cda-load-preview-1`
  Explorer: 12.8 s, 8 authoring calls, exact 25 sampled rows and the `id`
  column, unmodeled baseline.
- `add-column-preview` on a fresh synthetic fixture: 6.2 s, 13 authoring
  calls, exact six columns including Birth Date, trace validation pass.
- `add-column-preview` on the same CDA generation with a fresh
  `loom-cda-add-column-3` Explorer: 11.1 s, 14 authoring calls, exact 25
  sampled rows and the `id` plus `Identifier Value` columns, trace validation
  pass.
- `filter-preview` on a fresh synthetic fixture: 8.1 s, 14 authoring calls,
  one-row female preview, cancel and reload restore both source rows, trace
  validation pass.
- `unpivot-preview` on a fresh synthetic fixture: red as designed, 10 authoring
  calls, HTTP 500 `CONSTRUCTION_RECEIPT_INPUT_MISSING`, trace validation pass.
- Retired `edit-remove-reload` journey on a fresh synthetic fixture: 13.2 s,
  28 authoring calls, one default group row, Group by id+gender applied to two
  rows and three columns, source restored, reload preserved, trace validation
  pass. The registered native case instead checks the initial empty-key
  `COUNT_ROWS` value of 2, canonical ID-only persistence of both Patient IDs
  with count 1 each, and restoration of its three authored source columns with
  exact list values; the native evidence and exact tested source identity are
  listed in the next entry. Its ALL-projected family list does not replace the
  legacy five-column flattened Basic source oracle.
- Registered native `builder-authoring:group-entry` run on source commit
  `0f16d0bf38c400313d640c7ca9c4585d25004714`: passed on 2026-10-22 from
  `22:54:15.440Z` through `22:55:08.098Z` (52.658 s bracket; 33.910 s
  Playwright). The selected proof-source fingerprint was
  `280550af8774bbf3d661229d169e05ac2419125bf0fb101a4bc18ff56a9d6154`; the
  watched tree fingerprint stayed
  `0cacb087245167b703b7c01decc7df9347ae7c11016ef64e4199c181301e3c4e` across
  2,092 files, and the API build identity stayed
  `db279cf6a3b2f32e816e94fdd3e43c9871f829477584a4f15189f8a0460bc6be:db279cf6a3b2f32e816e94fdd3e43c9871f829477584a4f15189f8a0460bc6be:0bf9e8e1cc663aeeb2f2704fe8d326ea8a88b24f824a82385e28b9ae25c91a41`.
  On fresh project `loom_dev_verify_mvk51st3-9e45d23`, generation `fixture-v1`,
  Explorer `verify-t3-9e45d23-group-entry`, it passed 70/70 native assertions,
  all 17 required checks, and recorded 79 authoring backend responses. The
  captured trace has 24 actions and one unique validated `BuilderJourney`
  replay (26 generated states, 25 distinct, depth 25). The two Cancel controls
  and two reloads are explicit TLA-unsupported events with native workspace,
  draft, and displayed-value assertions; the model proves abstract
  request/apply ownership chronology only. Trace mutation controls also
  rejected the wrong-owner `EditGroupAgain` mutation and a present native trace
  missing its required `EditGroupAgain` action.
  - Native report:
    `docs/verification/playwright/runtime/builder-group-entry-native-20261022/native-report.json`
    (SHA-256 `332967e7d34a49fda9a45e574da28c83211fdc8aa3b56960f5de5074a70f0d09`).
  - Bracket summary: `docs/verification/playwright/runtime/builder-group-entry-native-20261022/bracket-summary.json`
    (SHA-256 `fab81b855d63ba381065ac2c2c55017e3d8ba90e33896332129a5ddba74d1c81`).
  - Trace: `docs/verification/playwright/runtime/builder-group-entry-native-20261022/trace.json`
    (SHA-256 `1897d24d8154d74269574f02b277a8a6bbc6c08fca8b5cdcefb672b43a70274a`).
  - TLA report: `docs/verification/playwright/runtime/builder-group-entry-native-20261022/tla-report.json`
    (SHA-256 `7527c016a65c9790bca61d6eb844183b0526e4a0f242c83b6e385b23f16c12f2`);
    trace report: `docs/verification/playwright/runtime/builder-group-entry-native-20261022/trace-report.json`
    (SHA-256 `80fb4b1940e5e07c080a8aa5515add7fccb27a3b8b4763890eacc0de8571c7ab`).
  This report is tied to commit `0f16d0bf` and the fingerprints above; it is
  historical evidence for that tested source identity, not a pass claim for
  the metadata revision that records it. At the 0f closeout, the selected
  source pins computed to
  `2f8e9eeb273d46c5d6bee1f85e0a2621ff83a0d2c2a9e49e6600fdcb6420731f`; that
  historical metadata fingerprint was not browser-tested. At this 0f source snapshot,
  the five-column Basic and CDA extension-grouping assertions remained in the
  standalone spec; this run covered only the authored-three native case.
- The later two-case native Group verification on source commit
  `0b383cda0ac70f735180de4b669e4d9e2a551b35` passed 70/70 assertions for
  `builder-authoring:group-entry` and 62/62 for
  `builder-authoring:group-entry-raw-basic`, with 17/17 required checks per
  case. The authored-three case recorded 79 authoring responses and 24 actions;
  the raw Basic case recorded 59 responses and 18 actions. Both trace replays
  passed, and the wrong-owner and missing-`EditGroupAgain` controls rejected
  their mutants. Final 908 evidence is retained below and in the audit manifest;
  it does not supersede the 0b-authored-three source identity.
- On source `908b49fdff0fb34a6dba3c129f2344f8c37d12c4`, the public alias selected
  `builder-authoring:group-entry-raw-basic` and passed 62/62 native assertions,
  all 17 required checks, 59 authoring responses, and an 18-action
  BuilderJourney replay (20 generated states, 19 distinct, depth 19). The
  wrong-owner and missing-`EditGroupAgain` mutants were rejected. The selected
  proof-source fingerprint was
  `ead13338efd0591a4424afeebcb06f2533a7930539b96aa5dab687681a4d3e78`; the
  watched-source fingerprint remained
  `9f47a6f4b3a9b4c97b25a6949f51f49fb7304c67afeb84cc95e6f0560484995` across
  2,092 files, and the API build identity stayed unchanged. Both Cancel actions
  and both reloads remain native-only checks and are explicitly unmodeled.
  - Native report: `docs/verification/playwright/runtime/builder-group-entry-20261023/final-908/raw-basic-alias/native-report.json`
    (SHA-256 `49fc0cc324e3d29542dbc5c4166d73444de4cadec9ceda171d6b612c98c9a75e`).
  - Bracket summary: `docs/verification/playwright/runtime/builder-group-entry-20261023/final-908/raw-basic-alias/bracket-summary.json`
    (SHA-256 `7a72bb91ed225994c6531ebd04d71c5de9821e48dff8d7c3c37e776b24996443`).
  - Journey report, trace, TLA report, trace controls, and source/API/mount/health
    bracket captures are retained in the same `final-908` evidence directory;
    the audit JSON records their hashes.
- The distinct Basic budget probe on source `908b49fd` recorded 28
  `/authoring/v2/` responses, within the preserved 10–45 range, and passed its
  11-action BuilderJourney trace (13 generated states, 12 distinct, depth 12).
  It claims response-budget and lifecycle routing only, not exact raw data.
  Its report, trace, and TLA output are retained under
  `docs/verification/playwright/runtime/builder-group-entry-20261023/final-908/basic-budget/`;
  the separate raw-Basic native lifecycle still records 59 responses. The run
  used selected proof-source fingerprint `ead13338…`. This closeout refreshes
  the audit source pin, so the current metadata-only fingerprint is
  `2307bc1c6953e99e5987389f18d89ba4f5700af1bd38dfa6d4b960e68abdfe56`. It is
  not claimed as browser-tested.
- `pivot-error` on a fresh synthetic fixture: red as designed, 11 authoring
  calls, HTTP 500 `CONSTRUCTION_RECEIPT_INPUT_MISSING` instead of a typed 4xx, no `commands` call,
  trace validation pass.
- `BuilderJourney.cfg`: 227 generated states, 53 distinct states, depth 7, no
  error.

Artifacts are under `.artifacts/builder-journey/`. The CDA runs use the
`*-cda-trace.json` and `*-cda-report.json` paths so synthetic evidence is not
overwritten.

## Commands

Run the implemented journey on a fresh synthetic fixture:

```sh
npm run verify:builder-journey
```

Run it against a loaded CDA dataset and create the Explorer, table, and scalar
column if they are missing:

```sh
node scripts/run-builder-journey.mjs \
  --project loom_dev_cda_fhir \
  --generation repo-c0ec9689932caec424ad34a9 \
  --explorer loom-cda-journey-bootstrap-test \
  --bootstrap
```

Run one inventory row:

```sh
node scripts/run-builder-journey.mjs --journey group-twice-preview
node scripts/run-builder-journey.mjs --journey group-pivot-preview
node scripts/run-builder-journey.mjs --journey edit-remove-reload
```

`edit-remove-reload` is a compatibility ID for the registered native
`builder-authoring/group-entry-raw-basic` case. The runner gets the exact Playwright spec
and grep from `scripts/verify-ui/registry.mjs`, then calls the native
verification bracket and validates its emitted trace against
`builder-edit-remove-contract.json`. Configure the validated native environment
expected by the native bracket (`LOOM_CDA_*` capture values and `LOOM_DEV_*`
fixture values); `LOOM_CDA_SOURCE_ROOT` must resolve to the checkout running
the command. `docs/verification/playwright/NATIVE_RUN_PLAN.md` records an owned
stack example whose values must match the selected checkout and stack. The
native fixture owns the fresh synthetic project. It checks the raw five-column
Patient source and all fixture values, applies the ID-and-gender edit, then
edits the same Group back to ID-only and checks the rows after reload. It
previews and cancels removal, applies removal, and checks exact source
restoration. The authored-three-column ALL-family lifecycle remains separately
registered as `builder-authoring/group-entry`. The legacy `--project`,
`--generation`, and `--bootstrap` options do not apply to this mapped case.

Run the distinct Basic budget-only probe on its fresh synthetic fixture with:

```sh
LOOM_JOURNEY_DATASET=basic-budget \
node scripts/run-builder-journey.mjs --journey edit-remove-reload-basic-budget
```

This standalone path drives the minimal Group/edit/remove/reload flow and checks
the original 10–45 authoring-response range. It does not claim the raw-value
oracle already covered by the native raw-Basic case. The CDA manual-only route
is listed in `verification/builder-interactions/cda-mini-journeys.json` and
requires `LOOM_JOURNEY_DATASET=mini`.

The pivot journey exits 1 while F-001 is open. That is the intended red result.

## How to add a journey

1. Add a spec under `scripts/verify-ui/journey-specs/`.
2. Add one row to
   `verification/builder-interactions/builder-journey-audit.json`.
3. Run the spec on a fresh synthetic fixture.
4. Run it on the CDA project when the dataset is ready.
5. Record a pass or a finding. Leave product failures red.
6. Replay the trace through the matching model.

## Next work unit

All eight inventory rows have journey specs or registered native cases, and
three rows have terminal CDA runs: `group-twice-preview`, `load-preview`, and
`add-column-preview`. The Group consolidation is closed: both native source
shapes passed on `0b383cda`, and the final raw-Basic alias and minimal budget
routes passed on `908b49fd`. The independent CDA Group checks remain
manual-only and unrun. The historical F-003 ID+gender rerun did not reproduce
the error. Keep F-001 and F-004 red until the product fixes the pivot and
unpivot proposal errors, then finish the requirement-by-requirement completion
audit.
