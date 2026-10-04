# UI interaction verification

## Current execution checkout and named stack

The active execution checkout is `/private/tmp/loom-construction-implementation`.
Owned registry runs require an explicit named target before Docker inspection or
fixture creation. The base project identifies the stack; each run seeds a fresh
`loom_dev_verify_<run-id>` project and preserves the loaded CDA project.

```bash
LOOM_DEV_SOURCE_ROOT=/private/tmp/loom-construction-implementation \
LOOM_DEV_COMPOSE_PROJECT=loom-dev-6d7df93d6a37 \
LOOM_DEV_API_PORT=8188 \
LOOM_DEV_UI_PORT=30008 \
LOOM_DEV_PROJECT=loom_dev_c89a69d7e137 \
node scripts/verify-ui/builder-controls.mjs --case recompile
```

The baseline and historical sections below describe earlier checkpoints. Consult
`docs/BUILDER_VERIFICATION.tsv` for the current execution thread's case evidence.

The imported registry's source inventory is not yet current for this checkout.
A 2026-10-03 scan found 2,069 interaction records and seven unregistered data
hooks (row-change assessment, cell trace, Explorer deletion, capabilities,
population mapping, configured column contexts, and population selections).
Evidence: `/tmp/loom-ui-inventory-current.json` and
`/tmp/loom-ui-inventory-current.md`. The inventory command exits nonzero for
these gaps; this is not a browser pass. Those hooks are now mapped to their owning scenarios with explicit untested
feature entries. Hook registration does not establish browser coverage; regenerate
the canonical inventory after integrating the current preview changes.

## Browser baseline: 2026-10-03

The named stack `loom-dev-6d7df93d6a37` mounts
`/private/tmp/loom-construction-implementation`, not this checkout. Its source
HEAD was `415b805d791c33a7fdc654dea670aa530998de20` with uncommitted changes.
The SHA-256 fingerprint of the 1,117 mounted source files checked before and
after the run was
`0a1e0dd8fc17e94e74e456d6a15b92224a230998cf9dbf03c628d957086cf2bd`.

All seven registered cases ran. The first run failed all seven. Browser DOM
evidence showed that the UI now opens with **Build your first table** and
**Choose Patient rows**; five cases still drive the former graph-first flow.
The Builder load failure displays **Couldn’t load this dataset.** with **Try
again**. After updating that case's selectors, its `list` and `state` fault
and recovery cases passed. The remaining five cases are **verifier flow gaps**;
their timeout reports do not establish product failures. They stopped before
their required feature assertions.

Reports are in `.artifacts/verify-ui-baseline/verify-ui/`. The next verifier
change was to replace the shared graph-first setup with the current row-root
picker path. After that repair, **six of seven cases pass**: both Builder load,
both Builder authoring, table controls, and Viewer query recovery. Each later
report records its own mounted-source fingerprint; the other agent changed
source between some cases, but the source remained stable within each run.

The Recompile case now reaches a controlled 422 reconcile failure and exposes
the Recompile button. Clicking it issued no second reconcile request within
five seconds, so the case fails with a named assertion. This is a product
behavior failure for the execution thread to investigate. Viewer query
recovery passes, while its Gender facet remains untested: the published fixture
declares no runtime filters and renders no facet control. Add a fixture that
declares a runtime Gender filter before closing that separate coverage gap.

The generated inventory currently contains 257 records from 41 scanned production source files.

The [feature registry](../scripts/verify-ui/registry.mjs) maps workflows to data
hooks, endpoints, state transitions, and executable browser scripts. The
[source inventory](UI_INTERACTION_INVENTORY.md) records where production UI code disables controls, hides content,
returns early, or calls a data hook or client method. Neither list establishes
that a feature works. Each browser run produces its own evidence and status.

## Run a workflow

Start the isolated development stack and check its fixture:

```bash
make dev
make dev-doctor
```

Run the workflows independently:

```bash
node scripts/verify-ui/builder-load.mjs
node scripts/verify-ui/builder-authoring.mjs
node scripts/verify-ui/viewer-query.mjs
node scripts/verify-ui/builder-controls.mjs
```

Use `--help` on a script to inspect its options. Run one browser verifier at a
time. Keep watched source unchanged during the run. The existing
`make verify-fast` still checks publication, filtering, and CSV export against
the synthetic fixture.

Install local UI tooling before running the inventory and verifier tests:

```bash
npm ci --prefix ui
make verify-ui-test
```

The inventory check fails when the recorded source locations or expressions
are stale. Regenerate it after reviewing the new hooks and interaction gates.

```bash
node scripts/verify-ui/inventory.mjs
```

The generator writes `docs/UI_INTERACTION_INVENTORY.json` and
`docs/UI_INTERACTION_INVENTORY.md`. Use `--json PATH --markdown PATH` to write
review copies elsewhere.

## Read the evidence

The scripts distinguish usability, correctness, persistence, and performance.
Untested dimensions remain untested even when the checks that ran pass. A failed
workflow exits nonzero and retains the DOM and diagnostic evidence.
Owned-stack reports include the source root and a content fingerprint of the
mounted code. A source change during a browser case fails that case.

A click must reach a visible, enabled control using browser input. A script
must not bypass an overlay with `element.click()` and call the interaction
successful. The result must change as expected after the click.

Request durations help separate backend time from browser work. Action-to-render
time includes discovery, compilation, requests, and rendering. The default
budget is five seconds. Synthetic results do not establish performance on CDA
or NCPI data.

Injected API failures test recovery. Only the deliberately injected failure is
expected. JavaScript exceptions, failed module loads, other unexpected API
errors, and `INTERNAL_ERROR` remain failures. The demo’s missing favicon is
recorded separately as an incidental asset failure. An error message without a usable
repair or retry action does not establish recovery.

## Interpret interaction gates

| Condition | Required evidence |
| --- | --- |
| Empty name, no selected resource, or no columns | Explain the prerequisite and prove that satisfying it enables the action. |
| Request in progress | Record the operation and prove that controls become usable after success or failure. |
| Capability explicitly unsupported by the backend | Report the limitation separately from a failed capability request. |
| Capability or column discovery fails | Show an actionable error and prove retry against the restored backend. |
| Backend validation rejects a draft | Show the diagnostic and prove that the user can repair or retry the draft. |
| Pagination has reached a boundary | Prove the result has no next page or that this is the first page. |
| Overlay, invisible element, or inherited disabled state | Fail the intended interaction and retain the blocking element as evidence. |

## Historical first-wave source findings

Before the control cleanup, the initial inspection found these recovery risks. The current Builder still requests authoring capabilities and retains a capability-gated Explorer deletion path; registered scenarios do not test those responses or actions. The remaining recovery cases are not covered by this pass:

- `BuilderWorkspace.tsx` replaces the workspace on initial load failure without
  a retry action. Viewer has an explicit retry for its runtime and results.
- Capability errors are not inspected before missing feature flags default to
  `false`. This can present a backend failure as an unsupported feature.
- Column discovery stores `suggestionRequestKey` before the request succeeds.
  After failure, another attempt with the same key can return immediately.
- Builder combines commands, reconciliation, preview, publication, creation,
  and deletion into one `busy` flag. Every associated action needs evidence
  that it becomes usable again after a failed request.

The Builder-load and capability failures were reproduced in real Chrome with
one injected HTTP 503 per case. The initial evidence is in
`.artifacts/ui-state-baseline/report.json`. The current synthetic fixture supplies
column candidates in its catalog, so it does not exercise the lazy suggestion
request. Suggestion retry and global `busy` recovery remain untested. Preserve
each failed scenario when fixing the owning code.

## Coverage limits

The first wave covers loading, basic authoring and preview, and Viewer query
recovery. The subsequent [control cleanup](UI_CONTROL_AUDIT.md) adds table
duplication, renaming, deletion, Explorer copying, and Recompile checks.
Current Builder capability and Explorer-deletion behavior remains untested by
the registered browser cases. The registry must keep other workflows visible as untested. Required
composition cases, richer row operations, charts, pagination, large datasets,
authentication, and independent clinical correctness need additional scenarios.
The current Builder invokes preview through its Preview control and limit
changes. Automatic preview is a separate uncovered contract.

Other source hooks have no registered interaction case yet: replacing an
existing table root, inspecting configured-column context, resolving a saved
starting selection or named cohort, checking selected-resource coverage, and
explaining a Viewer cell against its publication receipt. These remain
explicitly untested in the registry.

## Recorded local run

Before the control cleanup on 2026-09-30, the owned synthetic development stack
produced these results. These evidence files record the earlier source snapshot:

| Case | Result | Evidence |
| --- | --- | --- |
| Builder list and state outages | Failed usability: no in-app Retry; reload recovered the reads | `.artifacts/loom-dev/verify-ui/builder-load-{list,state}-muof3jk2-002fa55.json` |
| Builder capability outage | Failed usability: missing actionable error/recovery; reload recovered the read | `.artifacts/loom-dev/verify-ui/builder-load-capability-muof3jk2-002fa55.json` |
| Catalog-backed candidates | Passed candidate rendering and clickability; lazy request recovery untested | `.artifacts/loom-dev/verify-ui/builder-authoring-suggestions-muoezg3o-35f2bb6.json` |
| Basic authoring | Passed creation, projection/filter controls, Preview rows, publication, and reload persistence | `.artifacts/loom-dev/verify-ui/builder-authoring-authoring-muoezg3o-35f2bb6.json` |
| Viewer query outage | Passed visible error, native Retry, fixture rows, facet values, and filtered rows; filter persistence untested | `.artifacts/loom-dev/verify-ui/viewer-query-output-muof4sfb-4ea3e3d.json` |

Measured authoring actions took 25–242 ms, including 222 ms for Preview and
86 ms for publication. These measurements cover two synthetic Patients.
They do not establish large-data performance. Reports include request durations
and browser long tasks; long-task evidence is limited to the final page after
navigation or reload.

`make verify-ui-test` passed 16 tests with no skips, including a real Chrome
negative control for an overlay, inherited disabled state, JavaScript errors,
and an intentionally slow browser task. The inventory drift check passed.

The frontend behavior remains unchanged. The failed Builder cases are regression
reproductions for the next cleanup pass, rather than exceptions to the checks.

The existing `make verify-fast` publication, filtering, and CSV export check
also passed after the shared driver exports were added. UI unit tests (103) and
the UI build passed; this wave did not change frontend implementation files.

## Control-cleanup verification

The shared Chrome driver uses a defined 1440 × 1000 desktop viewport, waits for
hover-induced layout changes and stable target geometry, and retains native
hit testing. Reload checks wait for the new document's load event before reading
state. Moving targets, sticky headers, clipped graph viewports, overlays, and inherited disabled controls
are covered by the driver self-check. Narrow-screen layouts remain unverified.

Graph setup uses the existing **Fit View** control before choosing Patient.
Automatic catalog fitting can leave a resource clipped after asynchronous layout;
the failure remains recorded in `builder-controls-tables-muoi7a7o-a6e1659.json`.
Successful feature runs therefore do not establish that automatic fitting works.

Current canonical follow-up (2026-10-03): the automatic-preview owner replacement
is integrated. `builder-controls --case first-table` and `--case recompile` pass
on the named 6d stack. Reports are
`/tmp/loom-first-table-basic-preview-integrated.json.first-table` and
`/tmp/loom-recompile-basic-preview-integrated.json.recompile`. Both retain the
unchanged 1120-file source fingerprint
`2d9d34eab308a7951979cd6ee48ae06548bad9fb4655322c6bf1c1de31de9bc3`.
First-table readiness records zero premature Add-columns enables and verifies
independent Patient rows plus native editor open/close. Recompile obtains a fresh
successful compilation after its intentionally injected 422. These bounded
cases do not establish save/reload or CDA performance. Earlier RED reports above
remain historical reproduction evidence. The corrected inventory test passes;
the sandbox-only Chrome self-check skips, while both real browser cases run.
