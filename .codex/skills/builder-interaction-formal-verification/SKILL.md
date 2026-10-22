---
name: builder-interaction-formal-verification
description: Verify Builder operation/data-shape support, proposal/preview/apply ownership, Pivot discovery, and bounded UI-contract coverage when changing the construction lifecycle or supported actions.
---

# Builder interaction formal verification

Use this skill when changing `useConstructionLifecycle`, `BuilderWorkspace`'s Apply flow, `ConstructionReshapeEditor` Pivot discovery, construction operation availability, the supported Builder actions in the UI contract inventory, or the TLA+ models and coverage map.

The checked promises are bounded:

- Every operation or data shape the mounted editors offer is one the authoring backend accepts for the modeled input, with no typed refusal of a valid form.
- Only the owner for the current request identity, with a matching proposal response and preview receipt/output with non-null rows, can become ready or begin Apply.
- A completed Apply acknowledgment targets the owner that began Apply, not whichever proposal is current when the acknowledgment arrives. This is now implemented and covered by two regression schedules.
- A Pivot discovery result is adopted only for the current key and generation. The former missing-category refusal finding is retired: the audited source and materialized paths do not reach `MISSING_UNSUPPORTED`. The remaining gap is that materialized outputs collapse an absent input to a present null.
- A captured implementation trace projects to a legal `Next` path in the bounded model. The Phase 1 evidence covers the 70 formal lifecycle schedules.

These are finite-state checks. They are not an unbounded refinement proof of React, Go, transport, persistence, or browser behavior.

## Fast loop

The inner loop is one source change, one focused test, and one mutation probe. Keep each command under 5 seconds. Do not run the integrated gate for a small change. Use it at a batch checkpoint.

Run one mutation probe:

    node scripts/run-builder-mutation-probe.mjs <probe-id> --force

The runner applies Vitest mutants through a Vite overlay, so it does not copy the repository. Go probes run one named regression and reuse `ui/node_modules/.cache/loom-mutation-go`. Evidence is keyed by source, test, and probe hashes. A repeat run skips unchanged probes. Use `--jobs=N` only after individual probes are cheap. The current suite is 50 probes, 47 killed, 3 equivalent, 0 survived, and 94 unprobed actions. The strict allowlist is empty.

Run one focused UI test:

    cd ui/packages/loom-ui
    node ../../node_modules/vitest/vitest.mjs run <test-file> -t "<test name>"

If a focused check cannot finish in 5 seconds, run less. The integrated gate is not the inner loop.

## Integrated command

Run from the repository root:

    node scripts/verify-builder-formal.mjs

The runner:

1. Verifies `verification/builder-interactions/builder-preview-contract.json`. Any changed fingerprinted production or model source fails the run until the source-to-model mapping is reviewed deliberately.
2. Requires Java and TLC 1.7.4. It uses `TLA2TOOLS_JAR` when set; otherwise it downloads the official release into the operating-system temporary directory. The JAR SHA-256 is `936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88`.
3. Runs the two safety models and the three retained finding models through TLC.
4. Runs the real React hook schedule suite. It expects 70 tests: 70 schedules, including two Apply-acknowledgment regressions. The suite writes a trace corpus to a temporary file.
5. Validates every captured trace with TLC through `scripts/verify-builder-traces.mjs`, using `builder-trace-contract.json` and the same TLC pin.
6. Runs selected Go lifecycle replay tests and server boundary tests, then the inventory source-only check.
7. Writes `verification/builder-interactions/report.json` and `report.md` with checks, findings, trace conformance, and coverage gaps.

Useful options:

    node scripts/verify-builder-formal.mjs --skip-go
    node scripts/verify-builder-formal.mjs --strict

`--skip-go` is for UI/model iteration; it is not a substitute for the full run before reporting completion. `--strict` fails while any retained finding or coverage gap remains; it is the full gate, not the default harness run.

UI dependencies are required. The runner prefers `ui/node_modules` and falls back to `ui/packages/loom-ui/node_modules`. Do not commit a local `ui/node_modules` symlink.

## Model architecture

The models are deliberately split so operation combinations do not multiply the lifecycle interleavings.

`BuilderOperations.tla` / `BuilderOperations.cfg`:

- Two authored steps over the mounted operation families for a rooted table.
- Tracks row shape (`records`, `groups`, `expanded`), column classes, whether a construction exists, editor form validity, undo, and reset.
- Models `ADD_FIELD`, `ADD_RELATED`, `FILTER`, `DERIVE`, `GROUP`, `PIVOT`, `CODED_PIVOT`, `UNPIVOT`, `EXPAND`, `RELATED_EXPAND`, the three Combine forms, Row Expanded, Row Explicit Group, and the legacy Table Shape Pivot/Unpivot forms.
- Checks `Inv_StepsWellFormed` and `Inv_NoUiOfferedUnsupportedOperation`.
- Current bound: 36,063 generated states, 2,428 distinct.

`BuilderPreview.tla` / `BuilderPreviewSafety.cfg`:

- Two accepted-draft identities and three proposal generations.
- Models proposal start, replacement, completion, proposal errors, late completion, cancellation, retry, preview start/completion, stale preview, Begin Apply, Apply acknowledgment, draft change, reconcile, Pivot key changes, discovery scheduling/completion, unmount, and adoption. `ApplyAck` targets `applyOwner`, not the current proposal owner; a replacement proposal is left unchanged by an older Apply settlement.
- `StartPreview` remains legal while an older Apply is in flight. `Inv_ApplyUsesReviewedPreview` compares the current preview only when the proposal generation still matches the Apply generation; a replacement preview is a separate finding path.
- Checks preview ownership, reviewed-preview Apply, Apply acknowledgment ownership, discovery ownership, missing-category support, and draft monotonicity.
- Current bound: 496,664 generated states, 66,469 distinct, depth 20.

The discovery finding config was retired on 2026-10-18. The real-AQL audit found no valid UI-offered field with `missing && !PresenceTracked`; source fields with absent values are tracked, while materialized outputs report `present=true`. The TLA model retains the abstract `missing-unsupported` outcome for trace vocabulary, but it is no longer run as an active finding.

## Trace conformance (Phase 1)

`verification/builder-interactions/builder-trace-contract.json` is the shared vocabulary: model action labels, events, request/checkpoint bindings, observation fields, and the Phase 1 required coverage. It is pinned in `builder-preview-contract.json`.

The formal suite records:
- initial observation immediately after capabilities settle;
- one action per settled implementation transition;
- observations `status`, `canApply`, `proposalIdPresent`, `previewReceiptPresent`, and `rowsPresent`;
- unmodeled implementation events separately, so stale settlements are visible rather than silently dropped.

Run the trace validator directly when iterating:

    node scripts/verify-builder-traces.mjs --traces /tmp/loom-formal-traces.json --output /tmp/trace-report.json

The validator deduplicates identical traces, generates a TLA+ case module per unique trace, and checks each step against `BuilderPreview!Next` or `ConstructionLifecycle!Next` with `lastAction'` constrained to the captured label. It also checks required models, actions, events, and the 70-trace Phase 1 count. `--allow-partial` is for local iteration only.

Current Phase 1 result after the Apply fix: 70 traces, 21 unique, 70 validated, 0 invalid. Required models, actions, and events are all covered. The 55 unmodeled events are stale proposal/preview settlements and rejected old `beginApply` calls; they are reported by kind in `report.json`.

Two abstraction limits are intentional:
- A ready proposal with an embedded preview is split into `ProposalComplete`, `StartPreview`, and `PreviewComplete`. The middle observation is a synthetic abstract `previewing` observation because the React hook has no intermediate render. The note says `embedded proposal preview abstract split`.
- The A-B-A schedule uses `ConstructionLifecycle.tla`, not `BuilderPreview.tla`, because the latter tracks monotonic draft identity. The other 60 traces use `BuilderPreview.tla`.

This layer validates action paths and a small observation projection. It is not a full state-refinement proof.

## Findings and resolved regressions

No active findings.

Retired finding:

1. `pivot-missing-category-unsupported`: the mounted Pivot editor was said to offer category fields whose absent values the backend refuses to discover. Retired after the compile-matrix and real-AQL audits found no valid path satisfying `missing && !PresenceTracked`. Source and related fields with absent values are tracked; derived, Filter, Expand, Group, and Unpivot materialized outputs report `present=true`, so they collapse absent to a present null instead of refusing discovery. That missing/null distinction remains a separate correctness gap and is not claimed fixed.

Resolved and covered by regression schedules:

1. `apply-ack-targets-current-owner-success`: `finishApply(true)` now targets the owner that began Apply and leaves a replacement owner untouched.
2. `apply-ack-targets-current-owner-failure`: `finishApply(false)` now targets the owner that began Apply and does not mark a replacement as failed.
3. `beginApply` rejects a second Apply while one is already applying.

Apply-ack browser acceptance attempt (2026-10-18): an isolated dev stack was built from this worktree, but the existing Basic authoring Playwright case failed before Apply because it expects a `Fields and related data` control that the current UI does not render. That is a harness/source mismatch, not an Apply-ack failure. The ownership race remains covered by the two formal hook regression schedules; do not claim a browser pass for it until a current case reaches Apply and reload.

Apply evidence is in `BuilderWorkspace.tsx` (`applyConstructionProposal` / `finishApply`) and `useConstructionLifecycle.ts` (`beginApply` / `finishApply`). The regression tests are named `regression: old Apply ...` at the end of `useConstructionLifecycle.formal.test.tsx`. Pivot retirement evidence is in `internal/dataframe/compiler/pivot_presence_audit_test.go` and `internal/dataframe/compiler/pivot_presence_arango_audit_test.go`; the discovery source remains `internal/explorer/lifecycle/construction_category_discovery.go`, `useConstructionLifecycle.ts`, and `ConstructionReshapeEditor.tsx`.

## Source and coverage maps

`verification/builder-interactions/builder-preview-contract.json` pins the reviewed production sources, the two TLA modules and configs, the trace contract, and the test trace recorder. Open the mapped source before changing the model. A hash mismatch means review, not a fingerprint refresh.

`verification/builder-interactions/affordance-model-map.json` assigns every one of the 57 inventory affordances to one of:

- `modeled`: the operation/data-shape or lifecycle behavior is in a TLA model.
- `partially-modeled`: lifecycle/ownership is modeled, but peripheral routing, persistence, or presentation is not.
- `unmodeled`: outside the current construction and preview model.

It also records implementation evidence as `tested`, `fixture-gap`, or `untested`. The runner rejects missing or unknown inventory IDs and model actions that do not exist in the TLA text.

Current coverage: 57 modeled, 0 partially modeled, 0 unmodeled; 57 implementation-tested, 0 fixture-gap, 0 untested. These counts are a map, not a completion claim. In particular, the Go replay covers Group, Filter, Derive, expanded Row Definition, empty-root creation, and server proposal conversion. It does not cover every Pivot, Unpivot, Combine, Table Shape, related-route, population, or interpretation affordance.

## Narrow runners

`node scripts/verify-builder-interactions.mjs` remains the focused proposal-owner check using `ConstructionLifecycle.tla` and `source-contract.json`. It drives 59 real-hook schedules. Keep it for fast lifecycle review; the integrated runner also runs those schedules.

`node scripts/verify-builder-operation-history.mjs` remains the focused Group operation-history and Go replay check. It covers the COUNT_ROWS-to-MEAN cascade, dependent Filter/Calculate reconciliation, cancel, preview, Apply, and the service-DTO-to-panel presentation fixture. Its controlled mutation mode remains:

    node scripts/verify-builder-operation-history.mjs --allow-source-drift-for-mutation /absolute/path/to/pristine-worktree

Use a same-HEAD pristine worktree and change only `internal/explorer/lifecycle/construction_proposal.go` to bypass dependent-step cascade resolution. A source-hash rejection, preparation failure, test setup error, unrelated assertion, or surviving mutant is a failed probe.

## Reading failures

A trace failure names the trace, action index, label, and generated TLA module. Compare the action with the implementation transition that produced it. Do not weaken an observation implication to make a stale or missing transition pass. A failure caused by an embedded-preview split usually means the synthetic middle observation was not used.

A source-hash failure means the implementation changed. Read the current source, decide whether intended behavior changed, update the model and real-hook schedules together, and then update the manifest deliberately. A hash update alone is not a review.

If TLC stops with a Java RMI `ExportException` saying `Listen failed on port: 0` and a `SocketException` saying `Operation not permitted`, the environment blocked TLC's local ephemeral listener. Keep the log and rerun where local sockets are permitted. Do not change the model around that failure.

If Go cannot write the configured build cache, set `LOOM_GO_CACHE` to a writable directory. The integrated and operation-history runners otherwise use a cache under the operating-system temporary directory.

A normal `--strict` run currently passes with zero active findings and zero coverage gaps. Any new inventory entry must be mapped to modeled, tested behavior before the gate passes again.

A Vitest failure names a concrete hook schedule. Deferred clients may resolve after receiving an abort signal so the suite can test whether stale work still takes control. A failure from that assertion is a source behavior finding; a module-load or environment failure is not.

The inventory `--check` mode still reports pending coverage metadata for most affordances. The integrated runner uses `--source-only` plus the separate coverage map, so a passing integrated run does not mean the inventory's own strict coverage gate passes.
