# Builder formal verification plan

Status: active
Baseline: `cd02d1306`

## Goal

Make formal verification reflect the Builder's real API surface. Every UI-called route gets an executable conformance contract. Stateful routes get TLA and trace conformance. Read-only routes get pinned request/response and identity tests. Non-Builder system surfaces are explicitly excluded.

## Progress

- [x] Added `ReconcileLifecycle.tla` and wired it into the formal runner.
- [x] Wired the reconcile Go HTTP test into `GO_SERVER_TESTS`.
- [x] Classified all 54 trace events outside the model vocabulary. 0 remain unclassified.
- [x] Corrected the route ledger to 39 UI-called routes, split 21 Tier 1 and 18 Tier 2. The ledger now accounts for all 37 generated `authoring/v2` routes: 33 in the ledger and 4 excluded with reasons. No generated authoring route is unaccounted.
- [x] Added `usedByBuilder`, `builderEvidence`, and `proof` to the route ledger. The Builder list is 39 routes with mounted Builder source evidence. The 4 non-Builder generated authoring routes carry `usedByBuilder=false` and an exclusion reason.
- [x] Made reconcile the first route-proven route using the Recompile recovery browser case. Added eight formal reconcile schedules, reconcile observation mapping, trace contract bindings, a route proof record, and a browser evidence record. The full edit, reconcile, preview, Apply, reload lifecycle remains open.
- [x] Added route proofs for `preview` and the apply command slice. Both use the existing `BuilderPreview` traces, the preview/apply Go contract test, and the Recompile browser case. The command proof scope is the apply command slice, not every command family. Current proof is 3 `modeled-and-traced`, 33 `modeled-only`, and 3 `unproven`. The route verifier is strict by default; the formal runner keeps `--report-only` until the remaining proof is assigned.
- [x] Added `--model`, `--list-models`, and `LOOM_TLC_WORKERS` to the formal runner.
- [x] Killed six mutation probes: Apply-ack owner guard, preview receipt guard, proposal draft-digest guard, root-change epoch guard, population-removal receipt guard, and interpretation-apply receipt guard.
- [x] Classified discovery-generation guard as equivalent: `invalidatePivotDiscovery` aborts the active controller before the generation check.
- [ ] Add a targeted CAS test for command draft identity. The `command-draft-version-guard` mutant survives the current focused command test, so request identity is not yet proven.
- [x] Added reconcile as a required trace action and event with a formal observation mapping and eight schedules. `ReconcileLifecycle` is now a required trace model.
- [ ] Assign route-level proof to the 33 remaining `modeled-only` routes and resolve the 3 `unproven` override-only routes: `publish`, `configured-column-context`, and `table-shape-category-discoveries`. Then flip the route ledger to a strict gate.
- [ ] Run the full browser lifecycle. The Recompile recovery case passes on the current checkout; the full edit, preview, publish, and reload lifecycle is still open.
- [x] Added a fast PR formal workflow and a full nightly/manual workflow.
- [x] Renamed the `BuilderPreview` reset action to `AcceptCurrentDraft` and renamed the coarse `LifecycleEntry` reconcile actions to entry-level names.
- [x] Extracted `reconcileResultIsCurrent`, `reconcileReceiptMatchesRequest`, and `reconcileRecoveryAction` with unit tests. The UI now uses the same recovery decisions as the model.
- [x] Extended `ReconcileLifecycle.tla` with receipt identity, explorer and fingerprint binding, rooted-table gating, retryable failure classes, and a stale-refresh bound. TLC passes with 1,913,635 generated and 148,629 distinct states.
- [x] Added the reconcile transition and guard table in `docs/verification/builder-reconcile-transitions.md`.
- [x] Added a trace validator mutation self-test. It rejects a corrupted observation, a wrong model mapping, and a missing required event. The formal runner now runs it on every gate.
- [x] Added a route ledger freshness and accounting check. The formal runner fails when the generated ledger is stale or when a generated authoring route is unaccounted.
- [x] Rechecked the Recompile browser case on the current checkout. It passes and issues a successful reconcile request. The harness now uses the same canonical project encoding as the UI.
- [x] Added a TLA-derived obligation report: `scripts/build-test-obligations.mjs` writes `verification/builder-interactions/test-obligations.json` and `.md`. The current pass finds 239 obligations across 153 actions and 86 invariants, with 15 exact-covered, 86 partial, and 138 missing. It also records 26 obligations with multiple same-level tests, 231 repeated Playwright checks, and 866 tests with no exact obligation.
- [x] Added `scripts/generate-builder-browser-evidence.mjs` and made the route ledger verify the browser case `status` and `coveredRoutes`. `browser-evidence.json` now uses `builder-controls:recompile`.
- [x] Added `coveredActions` to route proofs so the obligation map attaches only the TLA actions the trace and schedule exercise, not every action reachable by route callers. The route verifier checks covered actions against the model's action list.
- [x] Audited the 12 routes whose only mapped action was `AuthorStep` and added `routeActions` to the ledger. Seven are read-only and belong in `contract-tested`; `construction-choice-proposals` uses the proposal actions; `table-shape-proposals` and `table-shape-resolutions` are contract-tested because they do not mutate the draft; `explicit-groups` needs a new Population action or a contract test.
- [x] Added the `construction-category-discoveries` pilot trace: `SetPivotPair`, `ScheduleDiscovery`, and `DiscoveryComplete` are bound to the real hook schedule. The formal suite now validates 70/70 traces. The Go contract test rejects a stale discovery before the category scan. The route proof is `trace-tested`, not `modeled-and-traced`, because no browser report covers the route.
- [ ] Add browser evidence for `construction-category-discoveries`. The Docker VM has no free disk, so a new Playwright report cannot run. Until then the strict route check rejects the pilot as `trace-tested`.
- [x] Added a generated readiness report at `verification/builder-interactions/readiness.json` and `.md`. The current report says end-to-end ready is no. It has 36 strict route blockers, 15 audited route-action routes, 24 routes without a route-action audit, 102 candidate-bound missing obligations, 36 unbound missing obligations, trace coverage for 3 of 13 models, and browser coverage for 11 of 39 routes. The full formal runner checks that the report is current.
- [ ] Triage the 102 candidate-bound missing obligations and 24 routes without a route-action audit before adding new browser cases. The 36 unbound missing obligations are model setup actions, not route gaps.

## Tiers

### Tier 1: formal lifecycle routes

Tier 1 covers routes with async ownership, stale or conflict handling, draft or receipt identity, preview or Apply transitions, retries, or irreversible writes.

Each Tier 1 route needs:

- a TLA action or action family;
- a trace event and binding;
- real-controller schedules;
- request and response identity assertions;
- stale, abort, and late-response cases;
- a Go or HTTP contract test where a server route exists;
- one browser lifecycle for the route family.

Initial Tier 1 families:

1. Builder hydration, commands, and reconcile.
2. Preview, construction proposals, and category discovery.
3. Row change and row-definition proposals.
4. Population selections, routes, mapping, and member proposals.
5. Interpretation preview and revisions.
6. Publish.

### Tier 2: contract and binding routes

Tier 2 covers UI-called reads and contract-shaped writes. Each route needs a pinned method and path, request and response shape, identity binding, late-response behavior where applicable, error classification, and one executable test. Tier 2 does not get a TLA model unless a race appears.

Tier 2 includes explorer CRUD, capability, suggestions, semantic inventory, frame-source options, construction inputs and choices, related choices and contributors, row-definition choices, table-shape capabilities, column source, configured-column context, row lineage, and interpretation reads. Schema fields, cell trace, artifacts, and other non-Builder generated routes are excluded.

### Tier 3: excluded

Health, GraphQL, dataset generation read and activation, Apollo, infrastructure reconcilers, authentication middleware, and external FHIR or AQL execution stay outside this program. They keep their normal contract, integration, authorization, and load tests.

## Route ledger

`verification/builder-interactions/route-contract.json` is one ledger with two lists.

- `routes` is the mounted Builder surface. Every route has `usedByBuilder=true` and at least one `builderEvidence` path under `ui/packages/loom-ui/src/features/ExplorerBuilder/`.
- `excludedAuthoringRoutes` is the filtered-out generated API surface. Every route has `usedByBuilder=false`, `proof.status=excluded`, and a reason.

Each Builder entry records:

- stable route id, tier, method, path, and UI callers;
- `usedByBuilder` and `builderEvidence`;
- request identity fields;
- response binding fields;
- stale rule and error classes;
- `proof`: status, scope, model, UI model actions, route actions, covered actions, trace event, schedule, contract test, Go test, UI test, browser case, and reason.

Allowed proof statuses are `modeled-and-traced`, `modeled-only`, `contract-tested`, `unproven`, and `excluded`.

`modeled-only` means an affordance model action is mapped but the route has no route-level trace, schedule, or executable test. `unproven` means no model action is mapped. The strict route gate rejects both. The formal runner uses `--report-only` until route-level proof exists. See [Route proof chain](ROUTE_PROOF_CHAIN.md) for the full evidence chain.

Open scope decision: `publish` is mounted in `BuilderWorkspace.tsx`, but the inventory scope excludes publication, release, and publish workflows. The ledger keeps it because mounted Builder code calls it, and marks it `unproven`. Decide whether to model publish or move it to the excluded list.

## Phases

### Phase 3: Tier 1

1. Generate and dedupe the route ledger.
2. Add the ledger validator.
3. Land the reconcile pilot.
4. Add trace events and schedules for each Tier 1 family.
5. Add mutation faults and browser lifecycles.
6. Exit when every Tier 1 route is `modeled-and-traced`.

### Phase 4: Tier 2 and CI

1. Add one contract and binding test per Tier 2 route.
2. Make the route ledger a strict gate.
3. Add tier, route, and model filters plus `LOOM_TLC_WORKERS`.
4. Run a fast PR gate and a full nightly gate.
5. Retire duplicate narrow runners after parity.

## Reconcile pilot

Reconcile is first because it binds snapshot, draft version, digest, output, authorization scope, and receipt identity.

Required work:

- [x] add `Reconcile` to the trace contract binding and required events;
- [x] cover success, stale snapshot, draft conflict, invalid output, retryable failure, fatal failure, retry limit, and late response;
- [x] assert the receipt matches the submitted identity and reject stale results;
- [x] wire `TestReconcileSelectedOutputScopesReceiptAndPreviewPastStaleConsumer` into `GO_SERVER_TESTS`;
- [x] add the Recompile recovery browser case to `browser-evidence.json`;
- [ ] add one browser edit, reconcile, preview, Apply, reload lifecycle.

## Evidence rule

A unit is done only when its model, trace, test, and browser evidence exist. A passing model alone is not route conformance.

## Definition of done

- Every UI-called Builder route is in the ledger.
- Every Tier 1 route is modeled, traced, and browser-tested.
- Every Tier 2 route is contract-tested.
- Every Tier 3 exclusion has a reason.
- Strict fails on an unclassified or unbound route.
- Reconcile has trace, Go, and browser evidence.
- Selected mutation faults are killed.

## Non-goals

- Do not model every OpenAPI operation in TLA.
- Do not replace integration, authorization, load, or browser tests with models.
- Do not claim the entire system is proven.
- Do not treat a mocked UI test as route conformance.
