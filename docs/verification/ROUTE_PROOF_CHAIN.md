# Route proof chain

Status: reference

A route is **route-proven** when every link below exists for that route. The route ledger records the links. The strict route check fails when a Builder route is missing a link.

This is not a proof of the whole Builder. It is proof that one route has model, trace, test, and browser evidence within the stated bounds.

## The chain

1. **Model.** A TLA+ model contains the route's actions and invariants.
2. **Model check.** TLC checks the model against its `.cfg` bounds.
3. **Trace.** A real controller schedule emits a trace whose events map to model actions.
4. **Trace check.** `scripts/verify-builder-traces.mjs` accepts the trace as a legal model path.
5. **Schedule.** The trace comes from the real controller or the decision module the UI calls.
6. **Server test.** A Go or HTTP test hits the real handler and checks request identity and response binding.
7. **Browser case.** A Playwright case clicks the real control and asserts the user-visible result.
8. **Ledger proof.** The route's `proof` object points to the model, trace event, schedule, Go test, UI test, and browser case.
9. **Strict check.** `scripts/verify-builder-route-ledger.mjs` confirms that every reference exists and the status is complete.

A missing link fails the route proof. A route with a model but no trace is `modeled-only`. A route with a trace but no server test is not route-proven.

Tier 1 stateful routes need the full chain. Tier 2 read routes need a pinned request and response contract plus an executable test. They do not need a TLA model or a browser case. The status ladder records the difference.

## What each link proves

| Link | What it proves | What it does not prove |
| --- | --- | --- |
| Model | The named states, actions, and invariants exist. | That the model matches all production behavior. |
| TLC | The model satisfies its invariants within the `.cfg` bounds. | Unbounded refinement of the React or Go code. |
| Trace | One real schedule follows a legal model path. | That every schedule follows the model. |
| Server test | The handler accepts and rejects the tested request shapes and identities. | Every backend path, every transport failure, or production deployment. |
| Browser case | A user can complete the tested path in the real UI. | Every browser, dataset, permission, or network condition. |
| Ledger proof | The references and status are recorded in one place. | That the referenced evidence is current after later source edits. |

## How the ledger records it

Each Builder route has a `proof` object.

```json
{
  "status": "modeled-and-traced",
  "scope": "reconcile request, recovery, and late-result lifecycle",
  "model": "ReconcileLifecycle.tla",
  "modelActions": ["RequestReconcile", "ReconcileSuccess"],
  "coveredActions": ["RequestReconcile", "ReconcileSuccess"],
  "traceEvent": "reconcile-response",
  "schedule": "reconcile-recovery",
  "contractTest": null,
  "goTest": "TestReconcileSelectedOutputScopesReceiptAndPreviewPastStaleConsumer",
  "uiTest": "ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx",
  "browserCase": "builder-controls:recompile",
  "reason": "Reconcile success, stale, conflict, invalid, retryable, and fatal schedules."
}
```

`scope` records which part of the route the proof covers. A proof for an apply command slice says so. `coveredActions` lists the model actions the trace and schedule actually exercise; the obligation report uses that list instead of every action that any caller of the route could reach. The status ladder is:

- `modeled-and-traced`: all links exist.
- `modeled-only`: a model action is mapped, but the route has no trace, schedule, or executable test.
- `contract-tested`: an executable contract test exists, but the route is not modeled and traced.
- `unproven`: no model action and no executable test.
- `excluded`: the route is outside the Builder scope and has a reason.

The strict route check rejects `modeled-only` and `unproven`.

## Reconcile example

Reconcile is the pilot.

- Model: `ReconcileLifecycle.tla`.
- Trace: `ReconcileLifecycle` traces from the formal reconcile schedule.
- Schedule: success, stale refresh and retry, conflict, invalid, retryable failure, fatal failure, identity rejection, and late result rejection.
- Server test: `TestReconcileSelectedOutputScopesReceiptAndPreviewPastStaleConsumer`.
- Browser case: `builder-controls:recompile`, the Recompile recovery case.
- Ledger proof: `POST /authoring/v2/reconcile` points to those artifacts.

The browser case confirms that the Recompile control sends a second reconcile request and receives a successful response. `browser-evidence.json` records the case ID, source fingerprint, API identity, and raw report path. The server test checks the receipt and stale-consumer behavior. The trace checks the controller decisions against the model. The model checks request identity, receipt binding, stale and conflict outcomes, and bounded retry.

## Add a route proof

1. Add or extend a TLA model and run its `.cfg`.
2. Add the trace event binding to `builder-trace-contract.json`.
3. Add a real-controller formal schedule and emit the trace.
4. Add the route's proof record to `builder-route-overrides.json`.
5. Add the Go test to `GO_SERVER_TESTS` when a server route exists.
6. Add the browser case to `browser-evidence.json`.
7. Run `node scripts/verify-builder-formal.mjs`.
8. Run `node scripts/verify-builder-route-ledger.mjs --strict --route "POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/reconcile"` for one route.
9. Run `node scripts/verify-builder-route-ledger.mjs` for the full gate after every Builder route has proof.

The strict route check is the last step. Do not set `modeled-and-traced` before every reference exists.

## Bounds

The chain is bounded by the model config, the trace schedules, the tests, and the browser cases. It does not establish unbounded refinement, full browser lifecycle, server persistence, calculated value correctness, or production transport behavior.
