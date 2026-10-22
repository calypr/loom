# Builder journey audit goal

## Status

The historical journey audit completed all eight inventory requirements as of
2026-10-21. The registered `builder-authoring/group-entry` lifecycle now has a
passing native browser run at source commit
`0f16d0bf38c400313d640c7ca9c4585d25004714`; its exact report and proof-source
fingerprint are recorded in `verification/builder-interactions/builder-journey-audit.json`.
That run covers the authored three-column Patient source. The legacy five-column
flattened Basic oracle and the CDA extension-grouping assertions remain in the
legacy spec and are not run by this native case. This case-specific evidence
does not establish browser-ledger freshness or global readiness; multi-report
browser-evidence generation remains blocked by a missing historical
`builder-controls` report and external-path basename handling. F-001 and F-004 remain
open product findings; their journey specs stay red as evidence. A failing
journey is evidence about the product, not a blocked goal.

Progress: every inventory row has a journey spec or registered native case.
`group-twice-preview`, `load-preview`, and `add-column-preview` pass on
synthetic and CDA. `filter-preview` passes on synthetic. The registered native
`edit-remove-reload` route passed with the three-column authored source oracle
(Patient ID, the ALL-projected family list, and gender), ID-only and
ID-plus-gender edits, removal Cancel, and reload restoration. The legacy
five-column flattened Basic source oracle remains in the legacy spec, as does
the independent CDA extension-field variant. Neither set of assertions runs
in or is replaced by the native authored-three-column case.
`group-pivot-preview` and `pivot-error` are red and reproduce F-001.
`unpivot-preview` is red and reproduces F-004. Three rows have terminal CDA
runs: `group-twice-preview`, `load-preview`, and `add-column-preview`.

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
   unchanged. The historical standalone `edit-remove-reload` runner allowed up
   to 30 calls. The registered native Group lifecycle recorded 79 authoring
   backend responses, so it remains above that historical call-count target.
   The read-only `load-preview` baseline records at least 8 calls.
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
  backend calls; the historical standalone `edit-remove-reload` runner used a
  30-call allowance. The registered native Group route recorded 79 authoring
  backend responses and remains above that target. Every journey has at least
  one visible outcome.

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
| `edit-remove-reload` | Registered Group-entry case enters empty-key Group, previews and cancels ID-only edit, applies ID-plus-gender, edits back to ID-only, then cancels and applies removal. | Native pass at source `0f16d0bf`: the three authored columns bind Patient `id`, `name[].family` with `ALL` projection, and `gender`; source identity and fixture values are checked through removal and reload. Empty-key `COUNT_ROWS` is 2; ID-plus-gender matches the fixture; the same Group identity returns to ID-only and survives reload. Five-column Basic and CDA assertions remain in the legacy spec but are not run here. TLA models abstract request/apply-owner chronology; Cancel and reload remain native-only checks. | passing native authored-three-column case; five-column Basic and CDA assertions retained but not covered by this case |
| `pivot-error` | Pivot with error policies. | Typed 4xx product error, no applied draft change. | product-failure; red spec exists, F-001 |

## Completion check

The registered authored-three-column `edit-remove-reload` lifecycle is closed
for the native case recorded in the audit manifest. The wider Group-fragment
consolidation remains open: the separate five-column Basic oracle and CDA
extension-grouping assertions remain in the legacy spec and are not run by
that case. The native run also recorded 79 authoring backend responses, above
the historical 30-call allowance. The open
product findings F-001 and F-004 do not block coverage because the goal
requires real evidence, not green tests.

## Requirement audit (updated 2026-10-22; historical audit dated 2026-10-21)

1. Specs: the historical audit had eight journey specs. The current manifest
   maps `edit-remove-reload` to the registered native
   `builder-authoring/group-entry` case; that authored-three-column lifecycle
   passed at source commit `0f16d0bf38c400313d640c7ca9c4585d25004714`.
   The five-column Basic oracle and CDA extension-field assertions remain in
   the legacy spec and are not run by this native case.
2. Call counts: `load-preview` 8; `group-twice-preview` 17; `group-pivot-preview`
   11; `add-column-preview` 13 synthetic and 14 CDA; `filter-preview` 14;
   `unpivot-preview` 10; the historical `edit-remove-reload` Basic run had 28;
   the native report recorded 79 authoring backend responses, above the
   historical standalone runner's 30-call allowance; `pivot-error` 11. This
   native run closes the lifecycle outcome, not the earlier call-count target.
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
   ID+gender rerun; the registered authored-three-column route now also passes
   the ID-and-gender preview and Apply. This does not replace the separate
   five-column Basic or CDA assertions. No journey spec uses `test.skip`,
   `test.fixme`, or `test.fail`.
6. CDA: `group-twice-preview`, `load-preview`, and `add-column-preview` have
   terminal CDA runs with distinct `*-cda-trace.json` and `*-cda-report.json`
   artifacts.
7. Runner: every run above used `node scripts/run-builder-journey.mjs
   --journey <id>`; the runner resolves exactly one manifest row. The
   `edit-remove-reload` alias selects the registered
   `builder-authoring/group-entry` case. The historical 28-call Basic run
   below belongs to the retained legacy branch; the native run and its exact
   source identity are recorded separately below.
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

- Journey: historical Basic branch of `edit-remove-reload`; the standalone spec
  retains its CDA extension-field branch
- Request: `POST /api/v1/projects/<project>/explorers/<explorer>/authoring/v2/construction-proposals`
- Input: group by `id` and `gender` on a fresh synthetic Patient table
- Historical result: HTTP 500 `INTERNAL_ERROR`
- Historical request ID: `construction-proposal-e8f37264-d8f5-46c5-adf6-738f0b1e53ff`
- Historical rerun: HTTP 200 with two rows and three columns (`id`, `gender`, `Row count`)
- Artifact: `docs/verification/playwright/runtime/builder-journey-F-003.json`
- Trace: `.artifacts/builder-journey/edit-remove-reload-trace.json`
- Status: the error was not reproduced in the historical rerun. That ID+gender
  result remains historical regression evidence; the registered native route
  independently checked the ID-and-gender preview and applied result, followed
  by the separate ID-only persistence oracle. The distinct five-column Basic
  and CDA extension-field assertions remain in the legacy spec, outside this
  native case.
- Next step: retain the old source-shape and CDA assertions until each is
  independently migrated or otherwise covered; do not treat the native
  authored-three-column run as proof for either branch.

## Recorded evidence

Most runs below are historical evidence. In particular, the 28-call Basic
`edit-remove-reload` artifacts come from the standalone spec. The registered
native authored-three-column case has a separate passing browser report and
trace recorded below. The standalone spec retains the distinct five-column
Basic oracle and CDA extension-field assertions; neither is run by or replaced
by this native case.

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
  the metadata revision that records it. The updated source pins now compute to
  `2f8e9eeb273d46c5d6bee1f85e0a2621ff83a0d2c2a9e49e6600fdcb6420731f`; that
  metadata fingerprint has not been browser-tested. The five-column flattened Basic
  oracle and the independent CDA extension-grouping assertions remain in the
  legacy spec, outside this run.
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
`builder-authoring/group-entry` case. The runner gets the exact Playwright spec
and grep from `scripts/verify-ui/registry.mjs`, then calls the native
verification bracket and validates its emitted trace against
`builder-edit-remove-contract.json`. Configure the validated native environment
expected by the native bracket (`LOOM_CDA_*` capture values and `LOOM_DEV_*`
fixture values); `LOOM_CDA_SOURCE_ROOT` must resolve to the checkout running
the command. `docs/verification/playwright/NATIVE_RUN_PLAN.md` records an owned
stack example whose values must match the selected checkout and stack. The
native fixture owns the fresh synthetic project. It checks the empty-key
`COUNT_ROWS` preview, previews and cancels an ID-only edit, applies the
ID-and-gender edit, then edits the same Group back to ID-only and reloads its
exact rows. It previews and cancels removal, applies removal, and reloads the
restored three-column authored source with exact ID, family-list, and gender
values. The legacy five-column flattened Basic source oracle remains a
separate retained branch. The legacy `--project`, `--generation`, and
`--bootstrap` options do not apply to this mapped case.

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
`add-column-preview`. The registered Group native lifecycle has terminal
evidence for its authored-three-column fixture, but its 79 responses exceed
the historical 30-call allowance. The five-column Basic and CDA assertions
remain unrun by that case until each has a replacement proof. The historical F-003 ID+gender rerun did not
reproduce the error. Keep F-001 and F-004 red until the product fixes the pivot
and unpivot proposal errors, then finish the requirement-by-requirement
completion audit.
